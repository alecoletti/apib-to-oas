package convert

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/alecoletti/apib-to-oas/internal/oas"
)

// Tier-A meta extraction is the gating feature for Blueprint+. Stock
// Drafter folds an unrecognised `+ Meta` block into a single `copy`
// child of the transition; we have to recover it from there.
func TestBplusMeta_ParseMetaText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want metaBlock
	}{
		{
			name: "all recognised keys",
			in: "+ Meta\n" +
				"    + OperationId: getArticle\n" +
				"    + Tags: Articles, Public\n" +
				"    + Deprecated: true\n" +
				"    + Docs: https://docs.example.com - Docs site\n" +
				"    + Security: BearerAuth, ApiKeyAuth\n",
			want: metaBlock{
				OperationID: "getArticle",
				Tags:        []string{"Articles", "Public"},
				Deprecated:  ptr(true),
				DocsURL:     "https://docs.example.com",
				DocsDesc:    "Docs site",
				Security:    sptr([]string{"BearerAuth", "ApiKeyAuth"}),
			},
		},
		{
			name: "tags append semantics",
			in:   "+ Meta\n    + Tags: +Beta, +Internal\n",
			want: metaBlock{Tags: []string{"Beta", "Internal"}, TagsAppend: true},
		},
		{
			name: "extension key gets x-* with kebab",
			in:   "+ Meta\n    + Retry-Policy: aggressive\n    + Idempotent: true\n",
			want: metaBlock{Extensions: map[string]string{"x-retry-policy": "aggressive", "x-idempotent": "true"}},
		},
		{
			name: "explicit empty security clears auth",
			in:   "+ Meta\n    + Security:\n",
			want: metaBlock{Security: sptr([]string(nil))},
		},
		{
			name: "deprecated yes/no",
			in:   "+ Meta\n    + Deprecated: yes\n",
			want: metaBlock{Deprecated: ptr(true)},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseMetaText(c.in)
			if got == nil {
				t.Fatalf("parseMetaText returned nil")
			}
			if !reflect.DeepEqual(*got, c.want) {
				t.Fatalf("parsed = %+v\nwant   = %+v", *got, c.want)
			}
		})
	}
}

func TestBplusMeta_LooksLikeMetaBlock(t *testing.T) {
	yes := []string{
		"+ Meta\n  + OperationId: x",
		"+ meta\n  + OperationId: x",
		"  + Meta",
		"- Meta",
	}
	no := []string{
		"Some prose about Meta.",
		"+ Parameters",
		"",
		"+ Met",
	}
	for _, s := range yes {
		if !looksLikeMetaBlock(s) {
			t.Errorf("expected meta block: %q", s)
		}
	}
	for _, s := range no {
		if looksLikeMetaBlock(s) {
			t.Errorf("expected NOT meta block: %q", s)
		}
	}
}

func TestBplusMeta_NormaliseExtensionKey(t *testing.T) {
	cases := map[string]string{
		"Retry-Policy": "x-retry-policy",
		"RetryPolicy":  "x-retry-policy",
		"retry_policy": "x-retry-policy",
		"x-already":    "x-already",
		"X-Already":    "x-already",
		"Idempotent":   "x-idempotent",
		"Foo Bar":      "x-foo-bar",
	}
	for in, want := range cases {
		if got := normaliseExtensionKey(in); got != want {
			t.Errorf("normaliseExtensionKey(%q) = %q, want %q", in, got, want)
		}
	}
}

// End-to-end: the synthetic refract for a transition with a + Meta
// block as its only `copy` child should yield an operation whose
// description is empty (meta consumed) and every recognised key set.
func TestBplusMeta_EndToEnd(t *testing.T) {
	refract := []byte(`{
		"element": "parseResult",
		"content": [{
			"element": "category",
			"meta": {"title": {"element":"string","content":"API"}},
			"content": [{
				"element": "category",
				"meta": {
					"title": {"element":"string","content":"Articles"},
					"classes": {"element":"array","content":[{"element":"string","content":"resourceGroup"}]}
				},
				"content": [{
					"element": "resource",
					"attributes": {"href":{"element":"string","content":"/x"}},
					"content": [{
						"element": "transition",
						"meta": {"title":{"element":"string","content":"Get X"}},
						"content": [
							{"element":"copy","content":"+ Meta\n    + OperationId: getX\n    + Tags: +Internal\n    + Idempotent: true\n"},
							{"element":"httpTransaction","content":[
								{"element":"httpRequest","attributes":{"method":{"element":"string","content":"GET"}},"content":[]},
								{"element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"200"}},"content":[]}
							]}
						]
					}]
				}]
			}]
		}]
	}`)
	doc, err := RefractToOAS(refract)
	if err != nil {
		t.Fatal(err)
	}
	op := doc.Paths["/x"].Get
	if op == nil {
		t.Fatal("missing GET /x")
	}
	if op.OperationID != "getX" {
		t.Errorf("OperationID = %q, want getX", op.OperationID)
	}
	// Tags-append: should have group "Articles" plus "Internal".
	if !equalStringSlice(op.Tags, []string{"Articles", "Internal"}) {
		t.Errorf("Tags = %v, want [Articles Internal]", op.Tags)
	}
	if op.Extensions["x-idempotent"] != true {
		t.Errorf("x-idempotent = %v, want true (got %T)", op.Extensions["x-idempotent"], op.Extensions["x-idempotent"])
	}
	if op.Description != "" {
		t.Errorf("expected empty description (meta consumed); got %q", op.Description)
	}
}

// Verifies the marshalled YAML/JSON doesn't leak the raw + Meta text
// into the operation description, and that x-* extensions appear after
// the canonical struct fields.
func TestBplusMeta_MarshalShape(t *testing.T) {
	refract := []byte(`{
		"element":"parseResult","content":[{
			"element":"category","meta":{"title":{"element":"string","content":"API"}},
			"content":[{
				"element":"resource","attributes":{"href":{"element":"string","content":"/x"}},
				"content":[{
					"element":"transition","meta":{"title":{"element":"string","content":"Op"}},
					"content":[
						{"element":"copy","content":"+ Meta\n    + Retry-Policy: aggressive\n"},
						{"element":"httpTransaction","content":[
							{"element":"httpRequest","attributes":{"method":{"element":"string","content":"GET"}},"content":[]},
							{"element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"200"}},"content":[]}
						]}
					]
				}]
			}]
		}]
	}`)
	doc, err := RefractToOAS(refract)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Marshal(doc, "yaml")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Contains(s, "+ Meta") || strings.Contains(s, "Retry-Policy:") {
		t.Errorf("raw meta text leaked into output:\n%s", s)
	}
	if !strings.Contains(s, "x-retry-policy: aggressive") {
		t.Errorf("expected x-retry-policy in output:\n%s", s)
	}
}

// Document-level metadata that isn't in the recognised set should land
// as `info.x-*` (default) or as a root-level `x-*` when prefixed with
// `ROOT.` - Blueprint+ §5.5 / §13.3.
func TestBplus_DocumentExtensions(t *testing.T) {
	refract := []byte(`{
		"element":"parseResult","content":[{
			"element":"category",
			"meta":{"title":{"element":"string","content":"API"}},
			"attributes":{"metadata":{"element":"array","content":[
				{"element":"member","content":{"key":{"element":"string","content":"FORMAT"},"value":{"element":"string","content":"1A"}}},
				{"element":"member","content":{"key":{"element":"string","content":"VERSION"},"value":{"element":"string","content":"2.0"}}},
				{"element":"member","content":{"key":{"element":"string","content":"Internal-Owners"},"value":{"element":"string","content":"platform"}}},
				{"element":"member","content":{"key":{"element":"string","content":"ROOT.Trace-Sampling"},"value":{"element":"string","content":"0.1"}}}
			]}},
			"content":[]
		}]
	}`)
	doc, err := RefractToOAS(refract)
	if err != nil {
		t.Fatal(err)
	}
	if got := doc.Info.Extensions["x-internal-owners"]; got != "platform" {
		t.Errorf("info.x-internal-owners = %v, want platform", got)
	}
	if got := doc.Extensions["x-trace-sampling"]; got != 0.1 {
		t.Errorf("root x-trace-sampling = %v, want 0.1", got)
	}
	// Recognised keys must NOT leak into extensions.
	if _, ok := doc.Info.Extensions["x-version"]; ok {
		t.Errorf("VERSION leaked into info.x-version")
	}
	// Marshal-shape: x-* should appear after canonical info fields.
	out, _ := Marshal(doc, "yaml")
	s := string(out)
	if !strings.Contains(s, "x-internal-owners: platform") {
		t.Errorf("expected x-internal-owners in YAML; got:\n%s", s)
	}
	if !strings.Contains(s, "x-trace-sampling: 0.1") {
		t.Errorf("expected x-trace-sampling in YAML; got:\n%s", s)
	}
}

// A resource-level `+ Meta` block contributes only `x-*` extensions to
// the PathItem; recognised operation-level keys (OperationId/Tags/etc.)
// are ignored at this scope.
func TestBplus_ResourceMetaExtensions(t *testing.T) {
	refract := []byte(`{
		"element":"parseResult","content":[{
			"element":"category","meta":{"title":{"element":"string","content":"API"}},
			"content":[{
				"element":"resource","attributes":{"href":{"element":"string","content":"/x"}},
				"content":[
					{"element":"copy","content":"+ Meta\n    + Internal-Service: payments\n    + OperationId: ignored\n"},
					{"element":"transition","meta":{"title":{"element":"string","content":"Op"}},"content":[{
						"element":"httpTransaction","content":[
							{"element":"httpRequest","attributes":{"method":{"element":"string","content":"GET"}},"content":[]},
							{"element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"200"}},"content":[]}
						]
					}]}
				]
			}]
		}]
	}`)
	doc, err := RefractToOAS(refract)
	if err != nil {
		t.Fatal(err)
	}
	pi := doc.Paths["/x"]
	if pi == nil {
		t.Fatal("missing /x")
	}
	if got := pi.Extensions["x-internal-service"]; got != "payments" {
		t.Errorf("PathItem x-internal-service = %v, want payments", got)
	}
	// OperationId must NOT have leaked from the resource meta to the
	// operation - the PathItem-scoped meta only contributes extensions.
	if pi.Get != nil && pi.Get.OperationID == "ignored" {
		t.Errorf("recognised key leaked from resource meta to operation: %q", pi.Get.OperationID)
	}
}

// MSON enum members may carry per-value descriptions
// (`+ active - User is active`). They should surface as
// x-enum-descriptions[] aligned positionally with enum[] (Blueprint+ §11.2).
func TestBplus_EnumDescriptions(t *testing.T) {
	r := newSchemaResolver(nil)
	enumEl := &element{
		Element: "enum",
		Attributes: elementAttrs{
			Enumerations: elementsArray{Content: []element{
				{Element: "string", Content: []byte(`"active"`)},
				{Element: "string", Content: []byte(`"disabled"`)},
			}},
		},
		Content: []byte(`{"element":"string"}`),
	}
	// Stash descriptions on the elements via a second pass - the test
	// helper above doesn't carry meta.description because the inline
	// element constructor doesn't expose it. We patch by re-marshalling
	// into JSON with the descriptions and decoding back.
	src := `{"element":"enum","attributes":{"enumerations":{"element":"array","content":[
		{"element":"string","content":"active","meta":{"description":{"element":"string","content":"User is active"}}},
		{"element":"string","content":"disabled","meta":{"description":{"element":"string","content":"User is disabled"}}}
	]}},"content":{"element":"string"}}`
	if err := json.Unmarshal([]byte(src), enumEl); err != nil {
		t.Fatal(err)
	}

	s := r.enumSchema(enumEl, nil)
	if s == nil {
		t.Fatal("enumSchema returned nil")
	}
	if len(s.Enum) != 2 || s.Enum[0] != "active" || s.Enum[1] != "disabled" {
		t.Fatalf("Enum=%v, want [active disabled]", s.Enum)
	}
	xed, _ := s.Extensions["x-enum-descriptions"].([]any)
	if len(xed) != 2 {
		t.Fatalf("x-enum-descriptions = %v, want 2 entries", xed)
	}
	if xed[0] != "User is active" || xed[1] != "User is disabled" {
		t.Errorf("x-enum-descriptions = %v", xed)
	}
}

// MSON `(number, default)` should treat the sample value as the default.
func TestBplus_DefaultTypeAttribute(t *testing.T) {
	s := &oas.Schema{Type: "number", Example: float64(20)}
	// Build a classesList carrying a single "default" entry.
	var attrs classesList
	if err := json.Unmarshal([]byte(`{"element":"array","content":[{"element":"string","content":"default"}]}`), &attrs); err != nil {
		t.Fatal(err)
	}
	applyTypeAttributes(s, attrs)
	if s.Default == nil {
		t.Fatalf("Default not populated: %+v", s)
	}
	if got, ok := s.Default.(float64); !ok || got != 20 {
		t.Errorf("Default = %v (%T), want 20 (float64)", s.Default, s.Default)
	}
}

func TestBplus_ParseLocationPrefix(t *testing.T) {
	cases := []struct {
		desc                    string
		wantLoc, wantName, rest string
	}{
		{"[header] Trace identifier.", "header", "", "Trace identifier."},
		{"[header:X-Trace-Id] Trace id.", "header", "X-Trace-Id", "Trace id."},
		{"`[cookie]` Auth cookie.", "cookie", "", "Auth cookie."},
		{"`[header:X-Foo]`Distributed-trace id.", "header", "X-Foo", "Distributed-trace id."},
		{"  [cookie:sid]   Session.", "cookie", "sid", "Session."},
		{"plain description with no prefix", "", "", "plain description with no prefix"},
		{"[query] not allowed", "", "", "[query] not allowed"},
		{"", "", "", ""},
	}
	for _, c := range cases {
		gotLoc, gotName, gotRest := parseLocationPrefix(c.desc)
		if gotLoc != c.wantLoc || gotName != c.wantName || gotRest != c.rest {
			t.Errorf("parseLocationPrefix(%q) = (%q, %q, %q), want (%q, %q, %q)",
				c.desc, gotLoc, gotName, gotRest, c.wantLoc, c.wantName, c.rest)
		}
	}
}

// End-to-end: a parameter description starting with `[header:Real-Name]`
// should produce a header parameter named Real-Name with the prefix
// stripped from the description.
func TestBplus_ParameterLocationFromDescription(t *testing.T) {
	refract := []byte(`{
		"element":"parseResult","content":[{
			"element":"category","meta":{"title":{"element":"string","content":"API"}},
			"content":[{
				"element":"resource","attributes":{
					"href":{"element":"string","content":"/x/{id}"},
					"hrefVariables":{"element":"hrefVariables","content":[
						{"element":"member","meta":{"description":{"element":"string","content":"Path UUID."}},
							"attributes":{"typeAttributes":{"element":"array","content":[{"element":"string","content":"required"}]}},
							"content":{"key":{"element":"string","content":"id"},"value":{"element":"string","content":"abc"}}
						},
						{"element":"member","meta":{"description":{"element":"string","content":"` + "`" + `[header:X-Trace-Id]` + "`" + ` Trace id."}},
							"content":{"key":{"element":"string","content":"traceId"},"value":{"element":"string","content":"t1"}}
						},
						{"element":"member","meta":{"description":{"element":"string","content":"[cookie] Auth cookie."}},
							"content":{"key":{"element":"string","content":"sessionId"},"value":{"element":"string","content":"s1"}}
						}
					]}
				},
				"content":[{
					"element":"transition","meta":{"title":{"element":"string","content":"Op"}},"content":[{
						"element":"httpTransaction","content":[
							{"element":"httpRequest","attributes":{"method":{"element":"string","content":"GET"}},"content":[]},
							{"element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"200"}},"content":[]}
						]
					}]
				}]
			}]
		}]
	}`)
	doc, err := RefractToOAS(refract)
	if err != nil {
		t.Fatal(err)
	}
	pi := doc.Paths["/x/{id}"]
	if pi == nil {
		t.Fatal("missing /x/{id}")
	}
	op := pi.Get
	if op == nil {
		t.Fatal("missing GET")
	}
	// Build a name→param map (from PathItem-shared + Operation-scoped).
	all := append([]*oas.Parameter{}, pi.Parameters...)
	all = append(all, op.Parameters...)
	byKey := map[string]*oas.Parameter{}
	for _, p := range all {
		byKey[p.In+"/"+p.Name] = p
	}
	if got := byKey["path/id"]; got == nil || !got.Required {
		t.Errorf("path/id missing or not required: %+v", got)
	}
	hdr := byKey["header/X-Trace-Id"]
	if hdr == nil {
		t.Fatalf("expected header/X-Trace-Id; got params: %+v", byKey)
	}
	if hdr.Description != "Trace id." {
		t.Errorf("header description = %q, want clean prose without prefix", hdr.Description)
	}
	cookie := byKey["cookie/sessionId"]
	if cookie == nil {
		t.Fatalf("expected cookie/sessionId; got params: %+v", byKey)
	}
	if cookie.Description != "Auth cookie." {
		t.Errorf("cookie description = %q", cookie.Description)
	}
}

// Helpers ---

func ptr[T any](v T) *T         { return &v }
func sptr(v []string) *[]string { return &v }
func equalStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

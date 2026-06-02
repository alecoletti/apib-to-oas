package convert

import (
	"strings"
	"testing"

	"github.com/alecoletti/apib-to-oas/internal/oas"
)

func TestApplyTypeAttributes_Nullable(t *testing.T) {
	s := &oas.Schema{Type: "string"}
	applyTypeAttributes(s, classesList{Content: []stringValue{{Content: "nullable"}}})
	if !s.Nullable {
		t.Error("expected nullable")
	}
}

func TestApplyTypeAttributes_FixedTypeOnObject(t *testing.T) {
	// Test both the MSON spec spelling ("fixed-type") and the camelCase
	// form Drafter actually emits in its Refract JSON ("fixedType").
	for _, attr := range []string{"fixed-type", "fixedType"} {
		s := &oas.Schema{Type: "object"}
		applyTypeAttributes(s, classesList{Content: []stringValue{{Content: attr}}})
		if got, ok := s.AdditionalProperties.(bool); !ok || got != false {
			t.Errorf("attr=%q: expected additionalProperties=false, got %v", attr, s.AdditionalProperties)
		}
	}
}

func TestApplyTypeAttributes_FixedOnObject(t *testing.T) {
	// fixed on an object: additionalProperties=false + all declared
	// properties promoted to required.
	s := &oas.Schema{
		Type: "object",
		Properties: map[string]*oas.Schema{
			"title": {Type: "string"},
			"body":  {Type: "string"},
		},
		Required: []string{"title"}, // "body" not yet required
	}
	applyTypeAttributes(s, classesList{Content: []stringValue{{Content: "fixed"}}})
	if got, ok := s.AdditionalProperties.(bool); !ok || got != false {
		t.Errorf("expected additionalProperties=false, got %v", s.AdditionalProperties)
	}
	hasTitle, hasBody := false, false
	for _, r := range s.Required {
		switch r {
		case "title":
			hasTitle = true
		case "body":
			hasBody = true
		}
	}
	if !hasTitle || !hasBody {
		t.Errorf("expected both 'title' and 'body' in required, got %v", s.Required)
	}
}

func TestApplyTypeAttributes_FixedOnObject_NoDuplicateRequired(t *testing.T) {
	// When a property is already required, fixed must not add a duplicate.
	s := &oas.Schema{
		Type:       "object",
		Properties: map[string]*oas.Schema{"name": {Type: "string"}},
		Required:   []string{"name"},
	}
	applyTypeAttributes(s, classesList{Content: []stringValue{{Content: "fixed"}}})
	count := 0
	for _, r := range s.Required {
		if r == "name" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("'name' should appear exactly once in required, got %v", s.Required)
	}
}

func TestApplyTypeAttributes_FixedWithExample(t *testing.T) {
	s := &oas.Schema{Type: "string", Example: "OK"}
	applyTypeAttributes(s, classesList{Content: []stringValue{{Content: "fixed"}}})
	if len(s.Enum) != 1 || s.Enum[0] != "OK" {
		t.Errorf("expected enum=[OK], got %v", s.Enum)
	}
}

func TestInferFormat(t *testing.T) {
	cases := []struct {
		example string
		want    string
	}{
		{"7c9b1e8a-2f4d-4d9e-9b5d-1f0a4c2c6c11", "uuid"},
		{"2026-04-19T07:36:02Z", "date-time"},
		{"2026-04-19", "date"},
		{"alice@example.com", "email"},
		{"https://example.com/x", "uri"},
		{"hello world", ""},
		{"", ""},
	}
	for _, c := range cases {
		s := &oas.Schema{Type: "string", Example: c.example}
		if got := inferFormat(s); got != c.want {
			t.Errorf("inferFormat(%q) = %q, want %q", c.example, got, c.want)
		}
	}
}

// TestInferFormat_ArrayStringMSONProperty verifies that format inference
// reaches the items sub-schema for array[string] MSON object properties.
// The same code path governs both named-type properties and inline Attributes
// blocks; this test exercises it via a dataStructure definition.
func TestInferFormat_ArrayStringMSONProperty(t *testing.T) {
	// Simulates an MSON named type:
	//   # Article (object)
	//   + authors:  `editor@example.com`       (array[string])
	//   + ids:      `7c9b1e8a-…`               (array[string])
	//   + published: `2026-04-01T10:00:00Z`    (array[string])
	//   + dates:    `2026-04-01`                (array[string])
	//   + links:    `https://example.com`       (array[string])
	//   + tags:     `sport:football`            (array[string]) — no format
	refract := []byte(`{
	  "element":"parseResult",
	  "content":[{
	    "element":"category",
	    "content":[
	      {"element":"category",
	       "meta":{"classes":{"element":"array","content":[{"element":"string","content":"dataStructures"}]}},
	       "content":[
	         {"element":"dataStructure","content":{
	           "element":"object","meta":{"id":{"element":"string","content":"Article"}},
	           "content":[
	             {"element":"member","content":{"key":{"element":"string","content":"authors"},
	               "value":{"element":"array","content":[{"element":"string","content":"editor@example.com"}]}}},
	             {"element":"member","content":{"key":{"element":"string","content":"ids"},
	               "value":{"element":"array","content":[{"element":"string","content":"7c9b1e8a-2f4d-4d9e-9b5d-1f0a4c2c6c11"}]}}},
	             {"element":"member","content":{"key":{"element":"string","content":"published"},
	               "value":{"element":"array","content":[{"element":"string","content":"2026-04-01T10:00:00Z"}]}}},
	             {"element":"member","content":{"key":{"element":"string","content":"dates"},
	               "value":{"element":"array","content":[{"element":"string","content":"2026-04-01"}]}}},
	             {"element":"member","content":{"key":{"element":"string","content":"links"},
	               "value":{"element":"array","content":[{"element":"string","content":"https://example.com/x"}]}}},
	             {"element":"member","content":{"key":{"element":"string","content":"tags"},
	               "value":{"element":"array","content":[{"element":"string","content":"sport:football"}]}}}
	           ]}}
	       ]}
	    ]
	  }]
	}`)
	doc, err := RefractToOAS(refract)
	if err != nil {
		t.Fatalf("RefractToOAS: %v", err)
	}
	art := doc.Components.Schemas["Article"]
	if art == nil {
		t.Fatal("Article schema missing")
	}
	cases := []struct {
		prop       string
		wantFormat string
	}{
		{"authors", "email"},
		{"ids", "uuid"},
		{"published", "date-time"},
		{"dates", "date"},
		{"links", "uri"},
		{"tags", ""},
	}
	for _, tc := range cases {
		p := art.Properties[tc.prop]
		if p == nil {
			t.Errorf("property %q missing", tc.prop)
			continue
		}
		if p.Type != "array" {
			t.Errorf("%s: want type array; got %q", tc.prop, p.Type)
			continue
		}
		if p.Items == nil {
			t.Errorf("%s: items missing", tc.prop)
			continue
		}
		if got := p.Items.Format; got != tc.wantFormat {
			t.Errorf("%s: items.format = %q, want %q", tc.prop, got, tc.wantFormat)
		}
	}
}

// TestSchemaConstraints_MemberLevel verifies that a `+ Meta` block embedded
// in an MSON object-member description is extracted and applied as JSON
// Schema validation constraints on the property schema.
//
// APIB equivalent (the Meta block is folded into meta.description by Drafter):
//
//	## Article (object)
//	+ slug: my-article (string) - A URL slug.
//	    + Meta
//	        + Pattern: `^[a-z0-9-]+$`
//	        + MinLength: 3
//	        + MaxLength: 64
//	+ score (number) - Quality score.
//	    + Meta
//	        + Minimum: 0
//	        + Maximum: 100
//	        + MultipleOf: 0.5
//	+ tags (array) - Tag list.
//	    + Meta
//	        + MinItems: 1
//	        + MaxItems: 20
//	        + UniqueItems: true
func TestSchemaConstraints_MemberLevel(t *testing.T) {
	refract := []byte(`{
	  "element":"parseResult",
	  "content":[{"element":"category","content":[
	    {"element":"category",
	     "meta":{"classes":{"element":"array","content":[{"element":"string","content":"dataStructures"}]}},
	     "content":[
	       {"element":"dataStructure","content":{
	         "element":"object","meta":{"id":{"element":"string","content":"Article"}},
	         "content":[
	           {"element":"member",
	            "meta":{"description":{"element":"string","content":"A URL slug.\n\n+ Meta\n    + Pattern: ` + "`" + `^[a-z0-9-]+$` + "`" + `\n    + MinLength: 3\n    + MaxLength: 64"}},
	            "content":{"key":{"element":"string","content":"slug"},"value":{"element":"string","content":"my-article"}}},
	           {"element":"member",
	            "meta":{"description":{"element":"string","content":"Quality score.\n\n+ Meta\n    + Minimum: 0\n    + Maximum: 100\n    + MultipleOf: 0.5"}},
	            "content":{"key":{"element":"string","content":"score"},"value":{"element":"number","content":42}}},
	           {"element":"member",
	            "meta":{"description":{"element":"string","content":"Tag list.\n\n+ Meta\n    + MinItems: 1\n    + MaxItems: 20\n    + UniqueItems: true"}},
	            "content":{"key":{"element":"string","content":"tags"},"value":{"element":"array","content":[{"element":"string"}]}}}
	         ]}}
	     ]}
	  ]}]
	}`)

	doc, err := RefractToOAS(refract)
	if err != nil {
		t.Fatalf("RefractToOAS: %v", err)
	}
	art := doc.Components.Schemas["Article"]
	if art == nil {
		t.Fatal("Article schema missing")
	}

	slug := art.Properties["slug"]
	if slug == nil {
		t.Fatal("slug property missing")
	}
	if slug.Pattern != "^[a-z0-9-]+$" {
		t.Errorf("slug.pattern = %q, want ^[a-z0-9-]+$", slug.Pattern)
	}
	if slug.MinLength == nil || *slug.MinLength != 3 {
		t.Errorf("slug.minLength = %v, want 3", slug.MinLength)
	}
	if slug.MaxLength == nil || *slug.MaxLength != 64 {
		t.Errorf("slug.maxLength = %v, want 64", slug.MaxLength)
	}
	if slug.Description != "A URL slug." {
		t.Errorf("slug.description = %q (Meta block should be stripped)", slug.Description)
	}

	score := art.Properties["score"]
	if score == nil {
		t.Fatal("score property missing")
	}
	if score.Minimum == nil || *score.Minimum != 0 {
		t.Errorf("score.minimum = %v, want 0", score.Minimum)
	}
	if score.Maximum == nil || *score.Maximum != 100 {
		t.Errorf("score.maximum = %v, want 100", score.Maximum)
	}
	if score.MultipleOf == nil || *score.MultipleOf != 0.5 {
		t.Errorf("score.multipleOf = %v, want 0.5", score.MultipleOf)
	}

	tags := art.Properties["tags"]
	if tags == nil {
		t.Fatal("tags property missing")
	}
	if tags.MinItems == nil || *tags.MinItems != 1 {
		t.Errorf("tags.minItems = %v, want 1", tags.MinItems)
	}
	if tags.MaxItems == nil || *tags.MaxItems != 20 {
		t.Errorf("tags.maxItems = %v, want 20", tags.MaxItems)
	}
	if !tags.UniqueItems {
		t.Errorf("tags.uniqueItems = false, want true")
	}
}

// TestSchemaConstraints_NamedType verifies that a `+ Meta` block in a
// primitive named-type's description is extracted as constraints on the
// schema emitted for that type.
//
// APIB equivalent:
//
//	## Slug (string)
//	A URL-safe identifier.
//
//	+ Meta
//	    + Pattern: `^[a-z0-9-]+$`
//	    + MinLength: 3
//	    + MaxLength: 64
func TestSchemaConstraints_NamedType(t *testing.T) {
	refract := []byte(`{
	  "element":"parseResult",
	  "content":[{"element":"category","content":[
	    {"element":"category",
	     "meta":{"classes":{"element":"array","content":[{"element":"string","content":"dataStructures"}]}},
	     "content":[
	       {"element":"dataStructure","content":{
	         "element":"string",
	         "meta":{"id":{"element":"string","content":"Slug"},
	                 "description":{"element":"string","content":"A URL-safe identifier.\n\n+ Meta\n    + Pattern: ` + "`" + `^[a-z0-9-]+$` + "`" + `\n    + MinLength: 3\n    + MaxLength: 64"}}
	       }}
	     ]}
	  ]}]
	}`)

	doc, err := RefractToOAS(refract)
	if err != nil {
		t.Fatalf("RefractToOAS: %v", err)
	}
	slug := doc.Components.Schemas["Slug"]
	if slug == nil {
		t.Fatal("Slug schema missing")
	}
	if slug.Type != "string" {
		t.Errorf("type = %q, want string", slug.Type)
	}
	if slug.Pattern != "^[a-z0-9-]+$" {
		t.Errorf("pattern = %q, want ^[a-z0-9-]+$", slug.Pattern)
	}
	if slug.MinLength == nil || *slug.MinLength != 3 {
		t.Errorf("minLength = %v, want 3", slug.MinLength)
	}
	if slug.MaxLength == nil || *slug.MaxLength != 64 {
		t.Errorf("maxLength = %v, want 64", slug.MaxLength)
	}
	if slug.Description != "A URL-safe identifier." {
		t.Errorf("description = %q (Meta block should be stripped)", slug.Description)
	}
}

// TestSchemaConstraints_ExclusiveBounds verifies ExclusiveMinimum /
// ExclusiveMaximum parsing (OAS 3.1 numeric form).
func TestSchemaConstraints_ExclusiveBounds(t *testing.T) {
	refract := []byte(`{
	  "element":"parseResult",
	  "content":[{"element":"category","content":[
	    {"element":"category",
	     "meta":{"classes":{"element":"array","content":[{"element":"string","content":"dataStructures"}]}},
	     "content":[
	       {"element":"dataStructure","content":{
	         "element":"number",
	         "meta":{"id":{"element":"string","content":"Ratio"},
	                 "description":{"element":"string","content":"A ratio.\n\n+ Meta\n    + ExclusiveMinimum: 0\n    + ExclusiveMaximum: 1"}}
	       }}
	     ]}
	  ]}]
	}`)

	doc, err := RefractToOAS(refract)
	if err != nil {
		t.Fatalf("RefractToOAS: %v", err)
	}
	ratio := doc.Components.Schemas["Ratio"]
	if ratio == nil {
		t.Fatal("Ratio schema missing")
	}
	if ratio.ExclusiveMinimum == nil || *ratio.ExclusiveMinimum != 0 {
		t.Errorf("exclusiveMinimum = %v, want 0", ratio.ExclusiveMinimum)
	}
	if ratio.ExclusiveMaximum == nil || *ratio.ExclusiveMaximum != 1 {
		t.Errorf("exclusiveMaximum = %v, want 1", ratio.ExclusiveMaximum)
	}
}

// annotation and verifies it surfaces as a typed Annotation.
func TestExtractAnnotations(t *testing.T) {
	src := `{
		"element": "parseResult",
		"content": [
			{
				"element": "annotation",
				"meta": {"classes": {"element": "array", "content": [{"element": "string", "content": "warning"}]}},
				"attributes": {"code": {"element": "number", "content": 8}, "line": {"element": "number", "content": 3}, "column": {"element": "number", "content": 5}},
				"content": "parameter not found in URI template"
			}
		]
	}`
	anns := ExtractAnnotations([]byte(src))
	if len(anns) != 1 {
		t.Fatalf("expected 1 annotation, got %d", len(anns))
	}
	a := anns[0]
	if a.Severity != "warning" || a.Code != 8 || a.Line != 3 || a.Column != 5 {
		t.Errorf("annotation fields off: %+v", a)
	}
	if !strings.Contains(a.Message, "parameter not found") {
		t.Errorf("message: %q", a.Message)
	}
	if HasErrors(anns) {
		t.Error("warning should not count as error")
	}
}

func TestHasErrors(t *testing.T) {
	if HasErrors(nil) {
		t.Error("nil → false")
	}
	if HasErrors([]Annotation{{Severity: "warning"}}) {
		t.Error("warning-only → false")
	}
	if !HasErrors([]Annotation{{Severity: "warning"}, {Severity: "error"}}) {
		t.Error("any error → true")
	}
}

// TestRecoverMembersFromDescription_FlatMembers verifies that when Drafter
// dumps flat MSON member lines into meta.description, the converter recovers
// them into proper properties with type, required, and description.
func TestRecoverMembersFromDescription_FlatMembers(t *testing.T) {
	desc := "Picture holds picture metadata.\n\n+ url (string, required) - The picture URL.\n+ mediaGuid (string, optional) - Media GUID."
	s := &oas.Schema{Type: "object", Description: desc}
	r := newSchemaResolver(nil)
	r.recoverMembersFromDescription(s, map[string]bool{})

	if s.Description != "Picture holds picture metadata." {
		t.Errorf("description = %q, want prose only", s.Description)
	}
	if len(s.Properties) != 2 {
		t.Fatalf("expected 2 properties, got %d", len(s.Properties))
	}
	if s.Properties["url"] == nil || s.Properties["url"].Type != "string" {
		t.Error("url property missing or wrong type")
	}
	if s.Properties["url"].Description != "The picture URL." {
		t.Errorf("url.description = %q", s.Properties["url"].Description)
	}
	if len(s.Required) != 1 || s.Required[0] != "url" {
		t.Errorf("required = %v, want [url]", s.Required)
	}
}

// TestRecoverMembersFromDescription_OneOf verifies that `+ One Of` blocks
// in the description are recovered as OAS `oneOf` schemas.
func TestRecoverMembersFromDescription_OneOf(t *testing.T) {
	// Simulates what Drafter produces for the Taxonomy type.
	registry := map[string]*element{
		"SportTags":         {Element: "object"},
		"EntertainmentTags": {Element: "object"},
	}
	desc := "Taxonomy groups all tags.\n\n+ freeTexts (array[string], required)\n+ contents (array[string], optional)\n+ One Of\n    + Properties\n        + sport (SportTags, optional)\n    + Properties\n        + entertainment (EntertainmentTags, optional)"
	s := &oas.Schema{Type: "object", Description: desc}
	r := newSchemaResolver(registry).withRefs()
	r.recoverMembersFromDescription(s, map[string]bool{})

	if s.Description != "Taxonomy groups all tags." {
		t.Errorf("description = %q, want prose only", s.Description)
	}
	if s.Properties["freeTexts"] == nil || s.Properties["freeTexts"].Type != "array" {
		t.Error("freeTexts missing or not array")
	}
	if s.Properties["freeTexts"].Items == nil || s.Properties["freeTexts"].Items.Type != "string" {
		t.Error("freeTexts.items should be string")
	}
	if len(s.Required) != 1 || s.Required[0] != "freeTexts" {
		t.Errorf("required = %v, want [freeTexts]", s.Required)
	}
	if len(s.OneOf) != 2 {
		t.Fatalf("expected 2 oneOf options, got %d", len(s.OneOf))
	}
	// First option: sport → $ref SportTags
	opt0 := s.OneOf[0]
	if opt0.Properties["sport"] == nil {
		t.Fatal("oneOf[0] missing sport property")
	}
	if opt0.Properties["sport"].Ref != "#/components/schemas/SportTags" {
		t.Errorf("oneOf[0].sport.$ref = %q, want $ref to SportTags", opt0.Properties["sport"].Ref)
	}
	// Second option: entertainment → $ref EntertainmentTags
	opt1 := s.OneOf[1]
	if opt1.Properties["entertainment"] == nil {
		t.Fatal("oneOf[1] missing entertainment property")
	}
	if opt1.Properties["entertainment"].Ref != "#/components/schemas/EntertainmentTags" {
		t.Errorf("oneOf[1].entertainment.$ref = %q", opt1.Properties["entertainment"].Ref)
	}
}

// TestParseMemberLine covers the MSON member line parser.
func TestParseMemberLine(t *testing.T) {
	cases := []struct {
		input    string
		name     string
		typeName string
		required bool
		desc     string
	}{
		{"+ url (string, required) - The URL.", "url", "string", true, "The URL."},
		{"+ name (string, optional)", "name", "string", false, ""},
		{"+ tags (array[string], required)", "tags", "array[string]", true, ""},
		{"+ sport (SportTags, optional) - Tags.", "sport", "SportTags", false, "Tags."},
		{"+ contents: video, podcast (array[string], optional)", "contents", "array[string]", false, ""},
	}
	for _, tc := range cases {
		m := parseMemberLine(tc.input)
		if m == nil {
			t.Errorf("parseMemberLine(%q) = nil", tc.input)
			continue
		}
		if m.name != tc.name {
			t.Errorf("name = %q, want %q for %q", m.name, tc.name, tc.input)
		}
		if m.typeName != tc.typeName {
			t.Errorf("typeName = %q, want %q for %q", m.typeName, tc.typeName, tc.input)
		}
		if m.required != tc.required {
			t.Errorf("required = %v, want %v for %q", m.required, tc.required, tc.input)
		}
		if m.desc != tc.desc {
			t.Errorf("desc = %q, want %q for %q", m.desc, tc.desc, tc.input)
		}
	}
}

// TestObjectSchema_SelectElement verifies that a Drafter-parsed `select`
// element (MSON `One Of`) is converted to OAS `oneOf` schemas with
// members, descriptions, and type references preserved.
func TestObjectSchema_SelectElement(t *testing.T) {
	// This simulates what Drafter produces when it successfully parses
	// `+ One Of` with `+ Properties` sub-blocks.
	refract := []byte(`{
	  "element": "parseResult",
	  "content": [{
	    "element": "category",
	    "content": [
	      {"element":"category","meta":{"classes":{"element":"array","content":[{"element":"string","content":"dataStructures"}]}},
	       "content":[
	         {"element":"dataStructure","content":{"element":"object","meta":{"id":{"element":"string","content":"SportTags"}},"content":[
	           {"element":"member","attributes":{"typeAttributes":{"element":"array","content":[{"element":"string","content":"optional"}]}},
	            "content":{"key":{"element":"string","content":"team"},"value":{"element":"string"}}}
	         ]}},
	         {"element":"dataStructure","content":{"element":"object","meta":{"id":{"element":"string","content":"Taxonomy"},
	           "description":{"element":"string","content":"Groups tags."}},
	           "content":[
	             {"element":"member","meta":{"description":{"element":"string","content":"Free texts."}},
	              "attributes":{"typeAttributes":{"element":"array","content":[{"element":"string","content":"required"}]}},
	              "content":{"key":{"element":"string","content":"freeTexts"},"value":{"element":"array","content":[{"element":"string"}]}}},
	             {"element":"select","content":[
	               {"element":"option","content":[
	                 {"element":"member","meta":{"description":{"element":"string","content":"Sport tags."}},
	                  "attributes":{"typeAttributes":{"element":"array","content":[{"element":"string","content":"required"}]}},
	                  "content":{"key":{"element":"string","content":"sport"},"value":{"element":"SportTags"}}}
	               ]}
	             ]}
	           ]
	         }}
	       ]},
	      {"element":"resource","attributes":{"href":{"element":"string","content":"/x"}},
	       "content":[{"element":"transition","meta":{"title":{"element":"string","content":"Get"}},
	         "content":[{"element":"httpTransaction","content":[
	           {"element":"httpRequest","attributes":{"method":{"element":"string","content":"GET"}},"content":[]},
	           {"element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"200"},
	             "headers":{"element":"httpHeaders","content":[{"element":"member","content":{"key":{"element":"string","content":"Content-Type"},"value":{"element":"string","content":"application/json"}}}]}},
	             "content":[{"element":"dataStructure","content":{"element":"Taxonomy"}}]}
	         ]}]}]
	      }
	    ]
	  }]
	}`)
	doc, err := RefractToOAS(refract)
	if err != nil {
		t.Fatalf("RefractToOAS: %v", err)
	}
	tax := doc.Components.Schemas["Taxonomy"]
	if tax == nil {
		t.Fatal("Taxonomy schema missing from components")
	}
	if tax.Description != "Groups tags." {
		t.Errorf("description = %q", tax.Description)
	}
	if tax.Properties["freeTexts"] == nil {
		t.Fatal("freeTexts property missing")
	}
	if tax.Properties["freeTexts"].Description != "Free texts." {
		t.Errorf("freeTexts.description = %q", tax.Properties["freeTexts"].Description)
	}
	if len(tax.OneOf) != 1 {
		t.Fatalf("expected 1 oneOf option, got %d", len(tax.OneOf))
	}
	opt := tax.OneOf[0]
	if opt.Properties["sport"] == nil {
		t.Fatal("oneOf[0] missing sport property")
	}
	if opt.Properties["sport"].Description != "Sport tags." {
		t.Errorf("sport.description = %q", opt.Properties["sport"].Description)
	}
	if len(opt.Required) != 1 || opt.Required[0] != "sport" {
		t.Errorf("oneOf[0].required = %v, want [sport]", opt.Required)
	}
}

// TestObjectSchema_DiscriminatorMeta verifies that a `+ Meta` block on a named
// type with `+ Discriminator: <field>` produces a schema.discriminator on the
// parent oneOf schema, and that option titles are preserved when Drafter
// populates meta.title on the option element.
func TestObjectSchema_DiscriminatorMeta(t *testing.T) {
	// Simulate Drafter output for:
	//
	//   ## VideoInput (object)
	//   A tagged-union video input.
	//
	//   + Meta
	//       + Discriminator: source
	//
	//   + One Of
	//       + Properties
	//           + source: existing (string, required, fixed)
	//           + id: abc (string, required)
	//       + Properties
	//           + source: ingest (string, required, fixed)
	//           + url: https://x (string, required)
	//
	// Drafter folds the `+ Meta` block into meta.description as copy text,
	// so we inject it there. Option titles come from meta.title on the
	// option element.
	descWithMeta := `A tagged-union video input.\n\n+ Meta\n    + Discriminator: source`

	refract := []byte(`{
	  "element": "parseResult",
	  "content": [{
	    "element": "category",
	    "content": [
	      {"element":"category","meta":{"classes":{"element":"array","content":[{"element":"string","content":"dataStructures"}]}},
	       "content":[
	         {"element":"dataStructure","content":{
	           "element":"object",
	           "meta":{"id":{"element":"string","content":"VideoInput"},
	                   "description":{"element":"string","content":"` + descWithMeta + `"}},
	           "content":[
	             {"element":"select","content":[
	               {"element":"option",
	                "meta":{"title":{"element":"string","content":"existing"}},
	                "content":[
	                 {"element":"member","attributes":{"typeAttributes":{"element":"array","content":[{"element":"string","content":"required"},{"element":"string","content":"fixed"}]}},
	                  "content":{"key":{"element":"string","content":"source"},"value":{"element":"string","content":"existing"}}},
	                 {"element":"member","attributes":{"typeAttributes":{"element":"array","content":[{"element":"string","content":"required"}]}},
	                  "content":{"key":{"element":"string","content":"id"},"value":{"element":"string","content":"abc"}}}
	               ]},
	               {"element":"option",
	                "meta":{"title":{"element":"string","content":"ingest"}},
	                "content":[
	                 {"element":"member","attributes":{"typeAttributes":{"element":"array","content":[{"element":"string","content":"required"},{"element":"string","content":"fixed"}]}},
	                  "content":{"key":{"element":"string","content":"source"},"value":{"element":"string","content":"ingest"}}},
	                 {"element":"member","attributes":{"typeAttributes":{"element":"array","content":[{"element":"string","content":"required"}]}},
	                  "content":{"key":{"element":"string","content":"url"},"value":{"element":"string","content":"https://x"}}}
	               ]}
	             ]}
	           ]
	         }}
	       ]},
	      {"element":"resource","attributes":{"href":{"element":"string","content":"/v"}},
	       "content":[{"element":"transition","meta":{"title":{"element":"string","content":"Post"}},
	         "content":[{"element":"httpTransaction","content":[
	           {"element":"httpRequest","attributes":{"method":{"element":"string","content":"POST"}},"content":[
	             {"element":"dataStructure","content":{"element":"VideoInput"}}
	           ]},
	           {"element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"204"}},"content":[]}
	         ]}]}]
	      }
	    ]
	  }]
	}`)

	doc, err := RefractToOAS(refract)
	if err != nil {
		t.Fatalf("RefractToOAS: %v", err)
	}

	s := doc.Components.Schemas["VideoInput"]
	if s == nil {
		t.Fatal("VideoInput schema missing from components")
	}
	// Description should be stripped of the Meta block.
	if strings.Contains(s.Description, "Discriminator") {
		t.Errorf("description still contains Meta block: %q", s.Description)
	}
	// Discriminator must be set.
	if s.Discriminator == nil {
		t.Fatal("expected discriminator on VideoInput, got nil")
	}
	if s.Discriminator.PropertyName != "source" {
		t.Errorf("discriminator.propertyName = %q, want %q", s.Discriminator.PropertyName, "source")
	}
	// Two oneOf variants.
	if len(s.OneOf) != 2 {
		t.Fatalf("expected 2 oneOf options, got %d", len(s.OneOf))
	}
	// Option titles.
	if s.OneOf[0].Title != "existing" {
		t.Errorf("oneOf[0].title = %q, want %q", s.OneOf[0].Title, "existing")
	}
	if s.OneOf[1].Title != "ingest" {
		t.Errorf("oneOf[1].title = %q, want %q", s.OneOf[1].Title, "ingest")
	}
	// Each variant must have required=[source,...].
	for i, opt := range s.OneOf {
		hasSource := false
		for _, r := range opt.Required {
			if r == "source" {
				hasSource = true
			}
		}
		if !hasSource {
			t.Errorf("oneOf[%d].required does not include 'source': %v", i, opt.Required)
		}
	}
}

// ---------------------------------------------------------------------------
// + Schema Patch (Blueprint+ §15)
// ---------------------------------------------------------------------------

func TestFindSchemaPatchInCopy_Basic(t *testing.T) {
	text := "+ Schema Patch\n" +
		"    {\n" +
		"      \"if\":   {\"required\":[\"x\"]},\n" +
		"      \"then\": {\"required\":[\"y\"]}\n" +
		"    }"
	body, ok := findSchemaPatchInCopy(text)
	if !ok {
		t.Fatal("expected findSchemaPatchInCopy to return ok=true")
	}
	// Body should contain both if and then keys.
	if !strings.Contains(body, `"if"`) {
		t.Errorf("body missing 'if': %q", body)
	}
	if !strings.Contains(body, `"then"`) {
		t.Errorf("body missing 'then': %q", body)
	}
}

func TestFindSchemaPatchInCopy_FusedWithProse(t *testing.T) {
	text := "Some description here.\n\n" +
		"+ Schema Patch\n" +
		"    {\"else\": {\"properties\":{\"x\":{\"maxItems\":0}}}}"
	body, ok := findSchemaPatchInCopy(text)
	if !ok {
		t.Fatal("expected findSchemaPatchInCopy to return ok=true")
	}
	if !strings.Contains(body, `"else"`) {
		t.Errorf("body missing 'else': %q", body)
	}
}

func TestFindSchemaPatchInCopy_NotPresent(t *testing.T) {
	_, ok := findSchemaPatchInCopy("just prose\n+ Meta\n    + Pattern: foo")
	if ok {
		t.Error("expected ok=false for text with no Schema Patch block")
	}
}

func TestFindSchemaPatchInCopy_DashPrefix(t *testing.T) {
	text := "- Schema Patch\n    {\"not\":{\"type\":\"null\"}}"
	body, ok := findSchemaPatchInCopy(text)
	if !ok {
		t.Fatal("expected ok=true for dash-prefix Schema Patch")
	}
	if !strings.Contains(body, `"not"`) {
		t.Errorf("body missing 'not': %q", body)
	}
}

func TestProseWithoutSchemaPatch_StripsBlock(t *testing.T) {
	text := "Good description.\n\n" +
		"+ Schema Patch\n" +
		"    {\"if\":{\"required\":[\"x\"]}}\n\n" +
		"After patch."
	got := proseWithoutSchemaPatch(text)
	if strings.Contains(got, "Schema Patch") {
		t.Errorf("proseWithoutSchemaPatch left the header: %q", got)
	}
	if strings.Contains(got, `"if"`) {
		t.Errorf("proseWithoutSchemaPatch left JSON body: %q", got)
	}
	if !strings.Contains(got, "Good description.") {
		t.Errorf("proseWithoutSchemaPatch ate good prose: %q", got)
	}
}

func TestApplySchemaPatch_IfThenElse(t *testing.T) {
	s := &oas.Schema{Type: "object"}
	patch := `{
		"if":   {"properties":{"mode":{"const":"exclusive"}},"required":["mode"]},
		"then": {"required":["partnerIDs"]},
		"else": {"properties":{"partnerIDs":{"maxItems":0}}}
	}`
	applySchemaPatch(s, patch)

	if s.If == nil {
		t.Fatal("expected s.If to be set")
	}
	if s.Then == nil {
		t.Fatal("expected s.Then to be set")
	}
	if s.Else == nil {
		t.Fatal("expected s.Else to be set")
	}
	// Verify if.properties.mode.const was preserved.
	if s.If.Properties == nil {
		t.Fatal("s.If.Properties is nil")
	}
	mode, ok := s.If.Properties["mode"]
	if !ok || mode == nil {
		t.Fatal("s.If.Properties['mode'] missing")
	}
	if mode.Const == nil {
		t.Error("s.If.Properties['mode'].Const should be 'exclusive'")
	}
}

func TestApplySchemaPatch_Not(t *testing.T) {
	s := &oas.Schema{Type: "object"}
	applySchemaPatch(s, `{"not":{"type":"null"}}`)
	if s.Not == nil {
		t.Fatal("expected s.Not to be set")
	}
}

func TestApplySchemaPatch_DoesNotOverwrite(t *testing.T) {
	existing := &oas.Schema{Type: "object", Properties: map[string]*oas.Schema{"x": {Type: "string"}}}
	s := &oas.Schema{Type: "object", If: existing}
	applySchemaPatch(s, `{"if":{"required":["y"]}}`)
	// Pre-existing If must not be overwritten.
	if s.If != existing {
		t.Error("applySchemaPatch overwrote an existing If schema")
	}
}

func TestApplySchemaPatch_InvalidJSON(t *testing.T) {
	s := &oas.Schema{Type: "object"}
	// Must not panic or set anything on malformed input.
	applySchemaPatch(s, `{broken json`)
	if s.If != nil || s.Then != nil || s.Else != nil || s.Not != nil {
		t.Error("applySchemaPatch should be a no-op on invalid JSON")
	}
}

func TestExtractConstraintsFromDescription_SchemaPatch(t *testing.T) {
	desc := "An article request.\n\n" +
		"+ Schema Patch\n" +
		"    {\n" +
		"      \"if\":   {\"properties\":{\"accessMode\":{\"const\":\"exclusive\"}},\"required\":[\"accessMode\"]},\n" +
		"      \"then\": {\"required\":[\"partnerAvailability\"]},\n" +
		"      \"else\": {\"properties\":{\"partnerAvailability\":{\"maxItems\":0}}}\n" +
		"    }"
	s := &oas.Schema{Type: "object"}
	cleaned := extractConstraintsFromDescription(s, desc)

	if s.If == nil || s.Then == nil || s.Else == nil {
		t.Fatalf("expected If/Then/Else set; got if=%v then=%v else=%v", s.If, s.Then, s.Else)
	}
	if strings.Contains(cleaned, "Schema Patch") {
		t.Errorf("cleaned description still contains 'Schema Patch': %q", cleaned)
	}
	if !strings.Contains(cleaned, "An article request.") {
		t.Errorf("cleaned description lost prose: %q", cleaned)
	}

	// Verify then.required contains partnerAvailability.
	hasPA := false
	for _, r := range s.Then.Required {
		if r == "partnerAvailability" {
			hasPA = true
		}
	}
	if !hasPA {
		t.Errorf("then.required missing 'partnerAvailability': %v", s.Then.Required)
	}
}

func TestExtractConstraintsFromDescription_MetaAndSchemaPatch(t *testing.T) {
	// Both + Meta and + Schema Patch in the same description — both should be applied.
	desc := "Description.\n\n" +
		"+ Meta\n" +
		"    + MinLength: 1\n\n" +
		"+ Schema Patch\n" +
		"    {\"not\":{\"type\":\"null\"}}"
	s := &oas.Schema{Type: "string"}
	cleaned := extractConstraintsFromDescription(s, desc)

	if s.MinLength == nil || *s.MinLength != 1 {
		t.Errorf("MinLength not applied from Meta block: %v", s.MinLength)
	}
	if s.Not == nil {
		t.Error("Not not applied from Schema Patch block")
	}
	if strings.Contains(cleaned, "Meta") || strings.Contains(cleaned, "Schema Patch") {
		t.Errorf("cleaned description still contains blocks: %q", cleaned)
	}
}

// TestSchemaPatch_DrafterParsedAsMember covers the common case where Drafter
// successfully parses all MSON members but treats `+ Schema Patch` as an
// extra member (key="Schema Patch", description=JSON body). The converter
// must intercept it and apply if/then/else without leaking the pseudo-property.
func TestSchemaPatch_DrafterParsedAsMember(t *testing.T) {
	refract := []byte(`{
	  "element":"parseResult",
	  "content":[{"element":"category","content":[
	    {"element":"category",
	     "meta":{"classes":{"element":"array","content":[{"element":"string","content":"dataStructures"}]}},
	     "content":[
	       {"element":"dataStructure","content":{
	         "element":"object","meta":{"id":{"element":"string","content":"ArticleCreateRequest"}},
	         "content":[
	           {"element":"member",
	            "attributes":{"typeAttributes":{"element":"array","content":[{"element":"string","content":"required"}]}},
	            "content":{"key":{"element":"string","content":"accessMode"},"value":{"element":"string","content":"public"}}},
	           {"element":"member",
	            "attributes":{"typeAttributes":{"element":"array","content":[{"element":"string","content":"optional"}]}},
	            "content":{"key":{"element":"string","content":"partnerAvailability"},"value":{"element":"array","content":[]}}},
	           {"element":"member",
	            "meta":{"description":{"element":"string","content":"{\"if\":{\"properties\":{\"accessMode\":{\"const\":\"exclusive\"}},\"required\":[\"accessMode\"]},\"then\":{\"required\":[\"partnerAvailability\"]},\"else\":{\"properties\":{\"partnerAvailability\":{\"maxItems\":0}}}}"}},
	            "content":{"key":{"element":"string","content":"Schema Patch"},"value":{"element":"string"}}}
	         ]
	       }}
	     ]}
	  ]}]
	}`)

	doc, err := RefractToOAS(refract)
	if err != nil {
		t.Fatalf("RefractToOAS: %v", err)
	}
	s := doc.Components.Schemas["ArticleCreateRequest"]
	if s == nil {
		t.Fatal("ArticleCreateRequest schema missing")
	}

	// "Schema Patch" must NOT appear as a property.
	if _, leaked := s.Properties["Schema Patch"]; leaked {
		t.Error("'Schema Patch' leaked as a property; should have been intercepted")
	}
	// Real properties must survive.
	if s.Properties["accessMode"] == nil {
		t.Error("accessMode property missing")
	}
	if s.Properties["partnerAvailability"] == nil {
		t.Error("partnerAvailability property missing")
	}
	// if/then/else must be applied.
	if s.If == nil {
		t.Fatal("s.If not set after Schema Patch interception")
	}
	if s.Then == nil {
		t.Fatal("s.Then not set after Schema Patch interception")
	}
	if s.Else == nil {
		t.Fatal("s.Else not set after Schema Patch interception")
	}
	// Spot-check the parsed content.
	if s.If.Properties["accessMode"] == nil {
		t.Error("s.If.Properties['accessMode'] missing")
	}
	hasPA := false
	for _, r := range s.Then.Required {
		if r == "partnerAvailability" {
			hasPA = true
		}
	}
	if !hasPA {
		t.Errorf("s.Then.Required missing 'partnerAvailability': %v", s.Then.Required)
	}
}

package convert

import (
	"strings"
	"testing"

	"github.com/alecoletti/apib-to-oas/internal/oas"
)

// minimalAPI wraps a body of category content into a parseResult fixture
// so individual tests can express only the bits that matter. `meta` is
// inserted as the `metadata` array (each entry is one `member`).
func minimalAPI(meta, body string) []byte {
	const tmpl = `{
	  "element":"parseResult",
	  "content":[{
	    "element":"category","meta":{"title":{"element":"string","content":"API"}},
	    "attributes":{"metadata":{"element":"array","content":[%s]}},
	    "content":[%s]
	  }]
	}`
	return []byte(strings.Replace(strings.Replace(tmpl, "%s", meta, 1), "%s", body, 1))
}

func metaEntry(key, val string) string {
	return `{"element":"member","content":{"key":{"element":"string","content":"` + key +
		`"},"value":{"element":"string","content":"` + val + `"}}}`
}

// ---------- §5.4 / §12.2 - document-level SECURITY metadata ------------

func TestBplusV01_DocSecurity_DefaultsApplied(t *testing.T) {
	refract := minimalAPI(metaEntry("SECURITY", "BearerAuth, ApiKeyAuth"), "")
	doc, err := RefractToOAS(refract)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(doc.Security) != 2 {
		t.Fatalf("expected 2 security requirements, got %+v", doc.Security)
	}
	if _, ok := doc.Security[0]["BearerAuth"]; !ok {
		t.Errorf("first req should be BearerAuth, got %+v", doc.Security[0])
	}
	if _, ok := doc.Security[1]["ApiKeyAuth"]; !ok {
		t.Errorf("second req should be ApiKeyAuth, got %+v", doc.Security[1])
	}
}

func TestBplusV01_DocSecurity_EmptyClearsAuth(t *testing.T) {
	refract := minimalAPI(metaEntry("SECURITY", ""), "")
	doc, err := RefractToOAS(refract)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	// Empty SECURITY: present → emit empty doc.Security (callers can
	// distinguish "no SECURITY metadata" from "explicitly cleared").
	// Our parseDocumentSecurity returns (nil, true) which leaves
	// doc.Security as nil. That matches OAS "no document-level default".
	if len(doc.Security) != 0 {
		t.Errorf("expected no doc-level security, got %+v", doc.Security)
	}
}

func TestBplusV01_DocSecurity_SidecarOverridesMetadata(t *testing.T) {
	refract := minimalAPI(metaEntry("SECURITY", "BearerAuth"), "")
	cfg := &SecurityConfig{
		SecuritySchemes: map[string]*oas.SecurityScheme{
			"ApiKeyAuth": {Type: "apiKey", In: "header", Name: "X-API-Key"},
		},
		DefaultSecurity: []oas.SecurityRequirement{{"ApiKeyAuth": []string{}}},
	}
	doc, err := RefractToOASWithOptions(refract, Options{Security: cfg})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(doc.Security) != 1 {
		t.Fatalf("expected sidecar to win, got %+v", doc.Security)
	}
	if _, ok := doc.Security[0]["ApiKeyAuth"]; !ok {
		t.Errorf("sidecar default lost: %+v", doc.Security[0])
	}
}

// ---------- §13 - group-level + Meta → tags[].x-* ----------------------

func TestBplusV01_GroupMeta_EmitsTagExtensions(t *testing.T) {
	// Group "Articles" carries a + Meta block with one recognised key
	// (Tags is meaningless on a group, ignored) and one extension.
	body := `{
	  "element":"category",
	  "meta":{
	    "title":{"element":"string","content":"Articles"},
	    "classes":{"element":"array","content":[{"element":"string","content":"resourceGroup"}]}
	  },
	  "content":[
	    {"element":"copy","content":"+ Meta\n    + Owner: payments-team\n    + Stability: beta\n"},
	    {"element":"resource","attributes":{"href":{"element":"string","content":"/a"}},
	     "content":[{"element":"transition","meta":{"title":{"element":"string","content":"GetA"}},
	       "content":[{"element":"httpTransaction","content":[
	         {"element":"httpRequest","attributes":{"method":{"element":"string","content":"GET"}},"content":[]},
	         {"element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"200"}},"content":[]}
	       ]}]}]}
	  ]
	}`
	doc, err := RefractToOAS(minimalAPI("", body))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(doc.Tags) != 1 || doc.Tags[0].Name != "Articles" {
		t.Fatalf("expected single Articles tag, got %+v", doc.Tags)
	}
	ext := doc.Tags[0].Extensions
	if ext["x-owner"] != "payments-team" {
		t.Errorf("x-owner missing/wrong: %+v", ext)
	}
	if ext["x-stability"] != "beta" {
		t.Errorf("x-stability missing/wrong: %+v", ext)
	}
}

// ---------- §10.3 - `+ Response NNN - description` override -----------

func TestBplusV01_ResponseDescription_TitleOverride(t *testing.T) {
	// httpResponse has a meta.title - should override the canonical
	// reason phrase.
	body := `{"element":"resource","attributes":{"href":{"element":"string","content":"/x"}},
	  "content":[{"element":"transition","meta":{"title":{"element":"string","content":"GetX"}},
	    "content":[{"element":"httpTransaction","content":[
	      {"element":"httpRequest","attributes":{"method":{"element":"string","content":"GET"}},"content":[]},
	      {"element":"httpResponse",
	       "meta":{"title":{"element":"string","content":"Article successfully retrieved"}},
	       "attributes":{"statusCode":{"element":"string","content":"200"}},"content":[]}
	    ]}]}]}`
	doc, err := RefractToOAS(minimalAPI("", body))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	resp := doc.Paths["/x"].Get.Responses["200"]
	if resp == nil {
		t.Fatal("missing 200 response")
	}
	if resp.Description != "Article successfully retrieved" {
		t.Errorf("expected authored title to win, got %q", resp.Description)
	}
}

func TestBplusV01_ResponseDescription_CopyChild(t *testing.T) {
	// No title, but a copy child under httpResponse - collectResponseDescription
	// should pick it up.
	body := `{"element":"resource","attributes":{"href":{"element":"string","content":"/y"}},
	  "content":[{"element":"transition","meta":{"title":{"element":"string","content":"GetY"}},
	    "content":[{"element":"httpTransaction","content":[
	      {"element":"httpRequest","attributes":{"method":{"element":"string","content":"GET"}},"content":[]},
	      {"element":"httpResponse",
	       "attributes":{"statusCode":{"element":"string","content":"404"}},
	       "content":[{"element":"copy","content":"No article matched the supplied ID."}]}
	    ]}]}]}`
	doc, err := RefractToOAS(minimalAPI("", body))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	resp := doc.Paths["/y"].Get.Responses["404"]
	if resp == nil {
		t.Fatal("missing 404 response")
	}
	if resp.Description != "No article matched the supplied ID." {
		t.Errorf("expected copy-child prose, got %q", resp.Description)
	}
}

func TestBplusV01_ResponseDescription_FallsBackToReasonPhrase(t *testing.T) {
	body := `{"element":"resource","attributes":{"href":{"element":"string","content":"/z"}},
	  "content":[{"element":"transition","meta":{"title":{"element":"string","content":"GetZ"}},
	    "content":[{"element":"httpTransaction","content":[
	      {"element":"httpRequest","attributes":{"method":{"element":"string","content":"GET"}},"content":[]},
	      {"element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"500"}},"content":[]}
	    ]}]}]}`
	doc, err := RefractToOAS(minimalAPI("", body))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if got := doc.Paths["/z"].Get.Responses["500"].Description; got != "Internal Server Error" {
		t.Errorf("expected reason phrase fallback, got %q", got)
	}
}

// ---------- §15 E005 - duplicate operationId ---------------------------

func TestBplusV01_E005_DuplicateOperationId(t *testing.T) {
	// Two transitions on different paths share operationId "getThing"
	// (because both transitions are titled "getThing" and we derive
	// operationId from title when no + Meta override is present).
	body := `{"element":"resource","attributes":{"href":{"element":"string","content":"/a"}},
	  "content":[{"element":"transition","meta":{"title":{"element":"string","content":"getThing"}},
	    "content":[{"element":"httpTransaction","content":[
	      {"element":"httpRequest","attributes":{"method":{"element":"string","content":"GET"}},"content":[]},
	      {"element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"200"}},"content":[]}
	    ]}]}]},
	{"element":"resource","attributes":{"href":{"element":"string","content":"/b"}},
	  "content":[{"element":"transition","meta":{"title":{"element":"string","content":"getThing"}},
	    "content":[{"element":"httpTransaction","content":[
	      {"element":"httpRequest","attributes":{"method":{"element":"string","content":"GET"}},"content":[]},
	      {"element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"200"}},"content":[]}
	    ]}]}]}`
	diag := NewDiagnostics()
	if _, err := RefractToOASWithOptions(minimalAPI("", body), Options{Diagnostics: diag}); err != nil {
		t.Fatalf("convert: %v", err)
	}
	found := false
	for _, e := range diag.Items {
		if e.StableCode == CodeDuplicateOperation {
			found = true
			if !strings.Contains(e.Message, "getThing") {
				t.Errorf("E005 message missing operationId: %q", e.Message)
			}
			if !strings.Contains(e.Message, "/a:get") || !strings.Contains(e.Message, "/b:get") {
				t.Errorf("E005 message missing both sites: %q", e.Message)
			}
		}
	}
	if !found {
		t.Fatalf("expected E005 diagnostic, got %+v", diag.Items)
	}
}

func TestBplusV01_E005_NoFalsePositives(t *testing.T) {
	// Single operation should not raise E005.
	body := `{"element":"resource","attributes":{"href":{"element":"string","content":"/a"}},
	  "content":[{"element":"transition","meta":{"title":{"element":"string","content":"getThing"}},
	    "content":[{"element":"httpTransaction","content":[
	      {"element":"httpRequest","attributes":{"method":{"element":"string","content":"GET"}},"content":[]},
	      {"element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"200"}},"content":[]}
	    ]}]}]}`
	diag := NewDiagnostics()
	if _, err := RefractToOASWithOptions(minimalAPI("", body), Options{Diagnostics: diag}); err != nil {
		t.Fatalf("convert: %v", err)
	}
	for _, e := range diag.Items {
		if e.StableCode == CodeDuplicateOperation {
			t.Errorf("unexpected E005 on single op: %+v", e)
		}
	}
}

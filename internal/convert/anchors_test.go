package convert

import (
	"strings"
	"testing"

	"github.com/alecoletti/apib-to-oas/internal/oas"
)

// TestAnchors_PromoteAndShare builds a doc with one path that exposes
// two operations sharing the same parameter, then verifies the YAML
// emitter renders it as `&ref_0` once and `*ref_0` on the second op.
func TestAnchors_PromoteAndShare(t *testing.T) {
	p := &oas.Parameter{Name: "id", In: "path", Required: true, Schema: &oas.Schema{Type: "string"}}
	doc := oas.NewDocument()
	doc.Paths["/items/{id}"] = &oas.PathItem{
		Parameters: []*oas.Parameter{p},
		Get:        &oas.Operation{Summary: "Get item"},
		Delete:     &oas.Operation{Summary: "Delete item"},
	}
	promotePathParams(doc)
	assignAnchors(doc)

	got, err := Marshal(doc, "yaml")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(got)
	if !strings.Contains(s, "&ref_0") {
		t.Errorf("expected &ref_0 anchor in output:\n%s", s)
	}
	if !strings.Contains(s, "*ref_0") {
		t.Errorf("expected *ref_0 alias in output:\n%s", s)
	}
	// Anchor should appear exactly once, alias exactly once.
	if c := strings.Count(s, "&ref_0"); c != 1 {
		t.Errorf("expected exactly 1 anchor, got %d", c)
	}
	if c := strings.Count(s, "*ref_0"); c != 1 {
		t.Errorf("expected exactly 1 alias, got %d", c)
	}
}

func TestAnchors_NoSharingNoAnchors(t *testing.T) {
	// Single op uses one param → no sharing → no anchor required.
	doc := oas.NewDocument()
	doc.Paths["/x/{id}"] = &oas.PathItem{
		Parameters: []*oas.Parameter{{Name: "id", In: "path", Required: true, Schema: &oas.Schema{Type: "string"}}},
		Get:        &oas.Operation{Summary: "x"},
	}
	promotePathParams(doc)
	assignAnchors(doc)

	got, _ := Marshal(doc, "yaml")
	if strings.Contains(string(got), "ref_") {
		t.Errorf("did not expect any ref_ anchor in single-op output:\n%s", got)
	}
}

func TestApplyVersion(t *testing.T) {
	cases := []struct {
		in          string
		wantOpenAPI string
		wantDialect string
	}{
		{"", "3.0.3", ""},
		{"3.0", "3.0.3", ""},
		{"3.0.3", "3.0.3", ""},
		{"3.1", "3.1.0", "https://spec.openapis.org/oas/3.1/dialect/base"},
		{"3.1.0", "3.1.0", "https://spec.openapis.org/oas/3.1/dialect/base"},
		{"3.2", "3.2.0", "https://spec.openapis.org/oas/3.1/dialect/base"},
		{"3.2.0", "3.2.0", "https://spec.openapis.org/oas/3.1/dialect/base"},
		{"4.0.0-rc1", "4.0.0-rc1", ""},
	}
	for _, c := range cases {
		doc := oas.NewDocument()
		applyVersion(doc, c.in)
		if doc.OpenAPI != c.wantOpenAPI {
			t.Errorf("applyVersion(%q): OpenAPI = %q, want %q", c.in, doc.OpenAPI, c.wantOpenAPI)
		}
		if doc.JSONSchemaDialect != c.wantDialect {
			t.Errorf("applyVersion(%q): JSONSchemaDialect = %q, want %q", c.in, doc.JSONSchemaDialect, c.wantDialect)
		}
	}
}

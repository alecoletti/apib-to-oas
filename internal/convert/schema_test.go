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
	s := &oas.Schema{Type: "object"}
	applyTypeAttributes(s, classesList{Content: []stringValue{{Content: "fixed-type"}}})
	if got, ok := s.AdditionalProperties.(bool); !ok || got != false {
		t.Errorf("expected additionalProperties=false, got %v", s.AdditionalProperties)
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

// TestExtractAnnotations parses a hand-built Refract document with an
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

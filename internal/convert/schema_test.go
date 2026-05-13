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

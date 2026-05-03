package convert

import (
	"strings"
	"testing"
)

// TestAttributes_ResourceLevelDefault verifies that a `+ Attributes` block
// declared at the resource level becomes the default 2xx response schema
// for every action that doesn't carry its own.
func TestAttributes_ResourceLevelDefault(t *testing.T) {
	refract := []byte(`{
	  "element": "parseResult",
	  "content": [{
	    "element": "category", "meta": {"classes": {"element": "array", "content": [{"element":"string","content":"api"}]}},
	    "content": [{
	      "element": "resource",
	      "attributes": {"href": {"element":"string","content":"/items/{id}"}},
	      "content": [
	        {"element":"dataStructure","content":{"element":"object","content":[
	          {"element":"member","content":{"key":{"element":"string","content":"id"},"value":{"element":"number","content":42}}},
	          {"element":"member","content":{"key":{"element":"string","content":"name"},"value":{"element":"string","content":"widget"}}}
	        ]}},
	        {"element":"transition","meta":{"title":{"element":"string","content":"Get Item"}},
	         "content":[{"element":"httpTransaction","content":[
	           {"element":"httpRequest","attributes":{"method":{"element":"string","content":"GET"}},"content":[]},
	           {"element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"200"},"headers":{"element":"httpHeaders","content":[
	             {"element":"member","content":{"key":{"element":"string","content":"Content-Type"},"value":{"element":"string","content":"application/json"}}}
	           ]}},"content":[]}
	         ]}]}
	      ]
	    }]
	  }]
	}`)
	doc, err := RefractToOAS(refract)
	if err != nil {
		t.Fatalf("RefractToOAS: %v", err)
	}
	pi := doc.Paths["/items/{id}"]
	if pi == nil || pi.Get == nil {
		t.Fatal("expected GET /items/{id} operation")
	}
	resp := pi.Get.Responses["200"]
	if resp == nil || resp.Content["application/json"] == nil {
		t.Fatal("expected 200 application/json response")
	}
	s := resp.Content["application/json"].Schema
	if s == nil || s.Type != "object" {
		t.Fatalf("expected default object schema from resource Attributes, got %+v", s)
	}
	if _, ok := s.Properties["id"]; !ok {
		t.Errorf("expected default schema to include `id` property; got %+v", s.Properties)
	}
}

// TestAttributes_ActionLevelDefault verifies that a `+ Attributes` block
// at the action level becomes the default request schema.
func TestAttributes_ActionLevelDefault(t *testing.T) {
	refract := []byte(`{
	  "element": "parseResult",
	  "content": [{
	    "element": "category",
	    "content": [{
	      "element": "resource",
	      "attributes": {"href": {"element":"string","content":"/items"}},
	      "content": [{
	        "element":"transition","meta":{"title":{"element":"string","content":"Create"}},
	        "content":[
	          {"element":"dataStructure","content":{"element":"object","content":[
	            {"element":"member","content":{"key":{"element":"string","content":"name"},"value":{"element":"string"}}}
	          ]}},
	          {"element":"httpTransaction","content":[
	            {"element":"httpRequest","attributes":{"method":{"element":"string","content":"POST"},"headers":{"element":"httpHeaders","content":[
	              {"element":"member","content":{"key":{"element":"string","content":"Content-Type"},"value":{"element":"string","content":"application/json"}}}
	            ]}},"content":[
	              {"element":"asset","meta":{"classes":{"element":"array","content":[{"element":"string","content":"messageBody"}]}},"content":"{\"name\":\"x\"}"}
	            ]},
	            {"element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"201"}},"content":[]}
	          ]}
	        ]
	      }]
	    }]
	  }]
	}`)
	doc, err := RefractToOAS(refract)
	if err != nil {
		t.Fatalf("RefractToOAS: %v", err)
	}
	op := doc.Paths["/items"].Post
	if op == nil || op.RequestBody == nil {
		t.Fatal("expected POST /items with requestBody")
	}
	mt := op.RequestBody.Content["application/json"]
	if mt == nil || mt.Schema == nil || mt.Schema.Type != "object" {
		t.Fatalf("expected default object schema from action Attributes, got %+v", mt)
	}
	if _, ok := mt.Schema.Properties["name"]; !ok {
		t.Errorf("expected `name` property; got %+v", mt.Schema.Properties)
	}
}

// TestSchemaBlock_FallbackWhenNoAttributes verifies that a raw `+ Schema`
// asset (messageBodySchema, e.g. user-authored JSON Schema) is used when
// no inline `+ Attributes` dataStructure is present. When both are
// present, the dataStructure wins because it carries richer authoring
// intent (descriptions, formats, examples) - Drafter auto-emits a
// stripped-down `messageBodySchema` for every `+ Attributes` block, so
// preferring the asset would silently trash that intent.
func TestSchemaBlock_FallbackWhenNoAttributes(t *testing.T) {
	rawSchema := `{"type":"object","properties":{"raw":{"type":"string"}},"required":["raw"]}`
	refract := []byte(`{
	  "element": "parseResult",
	  "content": [{
	    "element": "category",
	    "content": [{
	      "element": "resource",
	      "attributes": {"href": {"element":"string","content":"/x"}},
	      "content": [{
	        "element":"transition","meta":{"title":{"element":"string","content":"X"}},
	        "content":[{"element":"httpTransaction","content":[
	          {"element":"httpRequest","attributes":{"method":{"element":"string","content":"GET"}},"content":[]},
	          {"element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"200"},"headers":{"element":"httpHeaders","content":[
	            {"element":"member","content":{"key":{"element":"string","content":"Content-Type"},"value":{"element":"string","content":"application/json"}}}
	          ]}},"content":[
	            {"element":"asset","meta":{"classes":{"element":"array","content":[{"element":"string","content":"messageBodySchema"}]}},
	             "attributes":{"contentType":{"element":"string","content":"application/schema+json"}},
	             "content": ` + jsonString(rawSchema) + `}
	          ]}
	        ]}]
	      }]
	    }]
	  }]
	}`)
	doc, err := RefractToOAS(refract)
	if err != nil {
		t.Fatalf("RefractToOAS: %v", err)
	}
	mt := doc.Paths["/x"].Get.Responses["200"].Content["application/json"]
	if mt == nil || mt.Schema == nil {
		t.Fatal("expected response media type with schema")
	}
	if _, ok := mt.Schema.Properties["raw"]; !ok {
		t.Errorf("expected schema from + Schema block (with `raw` property), got %+v", mt.Schema.Properties)
	}
	if !contains(mt.Schema.Required, "raw") {
		t.Errorf("expected required from raw schema, got %v", mt.Schema.Required)
	}
}

// TestAttributes_PreservesMemberDescriptions guards against a regression
// where Drafter's auto-emitted `messageBodySchema` asset (a stripped
// JSON Schema duplicate of every `+ Attributes` block) was preferred
// over the rich dataStructure, dropping every per-property description /
// example / format / $ref. The fix inverts the precedence so
// dataStructure wins; rawSchema is a fallback when no dataStructure is
// present.
func TestAttributes_PreservesMemberDescriptions(t *testing.T) {
	autoSchema := `{"type":"object","properties":{"id":{"type":"string"}}}`
	refract := []byte(`{
	  "element": "parseResult",
	  "content": [{
	    "element": "category",
	    "content": [{
	      "element": "resource",
	      "attributes": {"href": {"element":"string","content":"/y"}},
	      "content": [{
	        "element":"transition","meta":{"title":{"element":"string","content":"Y"}},
	        "content":[{"element":"httpTransaction","content":[
	          {"element":"httpRequest","attributes":{"method":{"element":"string","content":"GET"}},"content":[]},
	          {"element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"200"},"headers":{"element":"httpHeaders","content":[
	            {"element":"member","content":{"key":{"element":"string","content":"Content-Type"},"value":{"element":"string","content":"application/json"}}}
	          ]}},"content":[
	            {"element":"dataStructure","content":{"element":"object","content":[
	              {"element":"member","meta":{"description":{"element":"string","content":"UUID of the article."}},"content":{"key":{"element":"string","content":"id"},"value":{"element":"string","content":"863219a3-0a50-4c26-89dc-4834b62be3f1"}}}
	            ]}},
	            {"element":"asset","meta":{"classes":{"element":"array","content":[{"element":"string","content":"messageBodySchema"}]}},
	             "attributes":{"contentType":{"element":"string","content":"application/schema+json"}},
	             "content": ` + jsonString(autoSchema) + `}
	          ]}
	        ]}]
	      }]
	    }]
	  }]
	}`)
	doc, err := RefractToOAS(refract)
	if err != nil {
		t.Fatalf("RefractToOAS: %v", err)
	}
	mt := doc.Paths["/y"].Get.Responses["200"].Content["application/json"]
	if mt == nil || mt.Schema == nil {
		t.Fatal("expected schema")
	}
	id := mt.Schema.Properties["id"]
	if id == nil {
		t.Fatal("expected `id` property")
	}
	if id.Description != "UUID of the article." {
		t.Errorf("description dropped: got %q (auto-generated messageBodySchema asset wrongly won over + Attributes)", id.Description)
	}
	if id.Format != "uuid" {
		t.Errorf("format inference dropped: got %q", id.Format)
	}
	if id.Example != "863219a3-0a50-4c26-89dc-4834b62be3f1" {
		t.Errorf("example dropped: got %v", id.Example)
	}
}

// TestRefSiblings_OAS30_WrappedInAllOf verifies that a property whose
// value is a custom-type ref + carries a description (e.g. APIB
// `+ tags (Taxonomy, required) - Structured taxonomy tags.`) emits a
// validator-friendly schema in OAS 3.0: description hoisted to the
// parent, $ref tucked inside `allOf`. OAS 3.1+ permits siblings and is
// left as-is.
//
// The check targets components.schemas (where named-type cross-refs
// actually emit `$ref`), since inline schemas inline the target type
// and the bug doesn't apply.
func TestRefSiblings_OAS30_WrappedInAllOf(t *testing.T) {
	refract := []byte(`{
	  "element":"parseResult",
	  "content":[{
	    "element":"category",
	    "content":[
	      {"element":"category","meta":{"classes":{"element":"array","content":[{"element":"string","content":"dataStructures"}]}},"content":[
	        {"element":"dataStructure","content":{"element":"object","meta":{"id":{"element":"string","content":"Taxonomy"}},"content":[
	          {"element":"member","content":{"key":{"element":"string","content":"name"},"value":{"element":"string"}}}
	        ]}},
	        {"element":"dataStructure","content":{"element":"object","meta":{"id":{"element":"string","content":"Outer"}},"content":[
	          {"element":"member","meta":{"description":{"element":"string","content":"Structured taxonomy tags."}},
	           "attributes":{"typeAttributes":{"element":"array","content":[{"element":"string","content":"required"}]}},
	           "content":{"key":{"element":"string","content":"tags"},"value":{"element":"Taxonomy"}}}
	        ]}}
	      ]}
	    ]
	  }]
	}`)
	doc30, err := RefractToOASWithOptions(refract, Options{OASVersion: "3.0"})
	if err != nil {
		t.Fatalf("3.0: %v", err)
	}
	tags30 := doc30.Components.Schemas["Outer"].Properties["tags"]
	if tags30.Ref != "" {
		t.Errorf("3.0: $ref should be inside allOf, found at top: %q", tags30.Ref)
	}
	if tags30.Description != "Structured taxonomy tags." {
		t.Errorf("3.0: description should remain on parent, got %q", tags30.Description)
	}
	if len(tags30.AllOf) != 1 || tags30.AllOf[0].Ref != "#/components/schemas/Taxonomy" {
		t.Errorf("3.0: expected allOf:[{$ref:Taxonomy}], got %+v", tags30.AllOf)
	}
	doc31, err := RefractToOASWithOptions(refract, Options{OASVersion: "3.1"})
	if err != nil {
		t.Fatalf("3.1: %v", err)
	}
	tags31 := doc31.Components.Schemas["Outer"].Properties["tags"]
	if tags31.Ref != "#/components/schemas/Taxonomy" {
		t.Errorf("3.1: expected $ref preserved at top, got %q", tags31.Ref)
	}
	if tags31.Description != "Structured taxonomy tags." {
		t.Errorf("3.1: expected description as sibling, got %q", tags31.Description)
	}
	if len(tags31.AllOf) != 0 {
		t.Errorf("3.1: expected no allOf wrap, got %+v", tags31.AllOf)
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func jsonString(s string) string {
	// minimal JSON-string encoder for embedding into the refract literal.
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

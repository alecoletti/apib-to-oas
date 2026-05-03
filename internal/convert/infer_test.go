package convert

import "testing"

// When a + Body example is the only payload information (no + Attributes,
// no + Schema), we synthesise a minimal schema from the example so docs
// renderers (Redoc/Swagger UI/Stoplight) actually display the body.
func TestInferSchemaFromExample_RequestBody(t *testing.T) {
	refract := []byte(`{
		"element": "parseResult",
		"content": [{
			"element": "category",
			"meta": {"title": {"element":"string","content":"API"}},
			"content": [{
				"element": "resource",
				"attributes": {"href": {"element":"string","content":"/x"}},
				"content": [{
					"element": "transition",
					"content": [{
						"element": "httpTransaction",
						"content": [
							{"element":"httpRequest","attributes":{"method":{"element":"string","content":"POST"},"headers":{"element":"httpHeaders","content":[
								{"element":"member","content":{"key":{"element":"string","content":"Content-Type"},"value":{"element":"string","content":"application/json"}}}
							]}},"content":[
								{"element":"asset","meta":{"classes":{"element":"array","content":[{"element":"string","content":"messageBody"}]}},"content":"{\"data\":{\"title\":\"hi\",\"count\":3,\"ok\":true,\"tags\":[\"a\"]}}"}
							]},
							{"element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"201"}},"content":[]}
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
	mt := doc.Paths["/x"].Post.RequestBody.Content["application/json"]
	if mt.Schema == nil {
		t.Fatal("expected inferred schema, got nil")
	}
	if mt.Schema.Type != "object" {
		t.Errorf("root type = %q, want object", mt.Schema.Type)
	}
	data := mt.Schema.Properties["data"]
	if data == nil || data.Type != "object" {
		t.Fatalf("data property missing or wrong type: %+v", data)
	}
	if got := data.Properties["title"]; got == nil || got.Type != "string" {
		t.Errorf("data.title = %+v, want type=string", got)
	}
	if got := data.Properties["count"]; got == nil || got.Type != "integer" {
		t.Errorf("data.count = %+v, want type=integer", got)
	}
	if got := data.Properties["ok"]; got == nil || got.Type != "boolean" {
		t.Errorf("data.ok = %+v, want type=boolean", got)
	}
	if got := data.Properties["tags"]; got == nil || got.Type != "array" || got.Items == nil || got.Items.Type != "string" {
		t.Errorf("data.tags = %+v, want array<string>", got)
	}
	// Example must still be attached.
	if mt.Example == nil {
		t.Error("expected example to be preserved alongside inferred schema")
	}
}

// Inference must NOT override an explicit + Attributes schema - that's
// the whole point of being a "last-resort" fallback.
func TestInferSchemaFromExample_DoesNotOverrideAttributes(t *testing.T) {
	refract := []byte(`{
		"element": "parseResult",
		"content": [{
			"element": "category",
			"meta": {"title": {"element":"string","content":"API"}},
			"content": [{
				"element": "resource",
				"attributes": {"href": {"element":"string","content":"/x"}},
				"content": [{
					"element": "transition",
					"content": [{
						"element": "httpTransaction",
						"content": [
							{"element":"httpRequest","attributes":{"method":{"element":"string","content":"POST"},"headers":{"element":"httpHeaders","content":[
								{"element":"member","content":{"key":{"element":"string","content":"Content-Type"},"value":{"element":"string","content":"application/json"}}}
							]}},"content":[
								{"element":"dataStructure","content":{"element":"object","content":[
									{"element":"member","meta":{"description":{"element":"string","content":"The title."}},"content":{"key":{"element":"string","content":"title"},"value":{"element":"string","content":"hi"}}}
								]}},
								{"element":"asset","meta":{"classes":{"element":"array","content":[{"element":"string","content":"messageBody"}]}},"content":"{\"title\":\"hi\"}"}
							]},
							{"element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"201"}},"content":[]}
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
	mt := doc.Paths["/x"].Post.RequestBody.Content["application/json"]
	if mt.Schema == nil || mt.Schema.Properties["title"] == nil {
		t.Fatalf("schema lost: %+v", mt.Schema)
	}
	if got := mt.Schema.Properties["title"].Description; got != "The title." {
		t.Errorf("description was lost - looks like inference clobbered the MSON schema; got %q", got)
	}
}

package convert

import (
	"encoding/json"
	"testing"
)

// TestRefract_PrimitiveHelpers exercises the typed shapes against a
// hand-rolled tiny fixture so failures are easy to localise.
func TestRefract_PrimitiveHelpers(t *testing.T) {
	enumJSON := []byte(`{
	  "element": "enum",
	  "attributes": {
	    "enumerations": {
	      "element": "array",
	      "content": [
	        {"element":"string","content":"publish"},
	        {"element":"string","content":"archive"}
	      ]
	    }
	  },
	  "content": {"element":"string","content":"publish"}
	}`)
	var e element
	if err := json.Unmarshal(enumJSON, &e); err != nil {
		t.Fatal(err)
	}
	got := e.enumerationStrings()
	if len(got) != 2 || got[0] != "publish" || got[1] != "archive" {
		t.Errorf("enumerationStrings: got %v", got)
	}
	if obj := e.contentObject(); obj == nil || obj.Element != "string" || obj.contentString() != "publish" {
		t.Errorf("contentObject: got %+v", obj)
	}

	dsJSON := []byte(`{
	  "element": "dataStructure",
	  "content": {
	    "element": "object",
	    "meta": {"id": {"element":"string","content":"Foo"}},
	    "content": [{"element":"member","content":{"key":{"element":"string","content":"a"},"value":{"element":"string","content":"x"}}}]
	  }
	}`)
	var ds element
	if err := json.Unmarshal(dsJSON, &ds); err != nil {
		t.Fatal(err)
	}
	inner := ds.dataStructureInner()
	if inner == nil || inner.id() != "Foo" {
		t.Fatalf("dataStructureInner: got %+v", inner)
	}
	if len(inner.contentArray()) != 1 {
		t.Errorf("inner.contentArray length: got %d", len(inner.contentArray()))
	}

	refJSON := []byte(`{"element":"ArticleData"}`)
	var ref element
	if err := json.Unmarshal(refJSON, &ref); err != nil {
		t.Fatal(err)
	}
	if !ref.isReference() {
		t.Errorf("ArticleData with no content should be a reference")
	}
}

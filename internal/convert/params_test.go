package convert

import "testing"

// TestQueryParams_ScopedToDeclaringResource guards against query
// parameters from one APIB resource leaking onto operations of an
// unrelated resource that happens to map to the same base path.
//
// Real-world example: `## Search [/x{?q}]` and `## Create [/x]` both
// land on `paths./x` in OAS. Hoisting query params to the PathItem
// (alongside path params) would attach `?q=` to the unrelated POST.
// They must stay per-operation on the GET only.
func TestQueryParams_ScopedToDeclaringResource(t *testing.T) {
	refract := []byte(`{
	  "element": "parseResult",
	  "content": [{
	    "element": "category",
	    "content": [
	      {"element":"resource","attributes":{"href":{"element":"string","content":"/x{?q,limit}"}},
	       "content":[{"element":"transition","meta":{"title":{"element":"string","content":"Search"}},
	         "content":[{"element":"httpTransaction","content":[
	           {"element":"httpRequest","attributes":{"method":{"element":"string","content":"GET"}},"content":[]},
	           {"element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"200"}},"content":[]}
	         ]}]}]
	      },
	      {"element":"resource","attributes":{"href":{"element":"string","content":"/x"}},
	       "content":[{"element":"transition","meta":{"title":{"element":"string","content":"Create"}},
	         "content":[{"element":"httpTransaction","content":[
	           {"element":"httpRequest","attributes":{"method":{"element":"string","content":"POST"}},"content":[]},
	           {"element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"201"}},"content":[]}
	         ]}]}]
	      }
	    ]
	  }]
	}`)
	doc, err := RefractToOAS(refract)
	if err != nil {
		t.Fatalf("RefractToOAS: %v", err)
	}
	pi := doc.Paths["/x"]
	if pi == nil || pi.Get == nil || pi.Post == nil {
		t.Fatal("expected GET + POST under /x")
	}
	// PathItem must NOT carry the query params (they're not intrinsic
	// to the path).
	for _, p := range pi.Parameters {
		if p.In == "query" {
			t.Errorf("query param %q leaked to PathItem.Parameters", p.Name)
		}
	}
	// GET (the declaring resource) must own them.
	gotGet := map[string]bool{}
	for _, p := range pi.Get.Parameters {
		if p.In == "query" {
			gotGet[p.Name] = true
		}
	}
	for _, want := range []string{"q", "limit"} {
		if !gotGet[want] {
			t.Errorf("GET missing query param %q", want)
		}
	}
	// POST must NOT inherit them.
	for _, p := range pi.Post.Parameters {
		if p.In == "query" {
			t.Errorf("query param %q leaked from sibling resource onto POST", p.Name)
		}
	}
}

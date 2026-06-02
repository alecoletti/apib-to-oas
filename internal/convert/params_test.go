package convert

import (
	"testing"

	"github.com/alecoletti/apib-to-oas/internal/oas"
)

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

// TestParseHeaderValue verifies that the Blueprint+ trailing-annotation
// convention is correctly recognised and stripped from header value strings.
// Real Drafter output folds annotations verbatim into the value field, e.g.
// `Authorization: Bearer token (required)` → value = "Bearer token (required)".
func TestParseHeaderValue(t *testing.T) {
	cases := []struct {
		raw     string
		wantVal string
		wantReq bool
	}{
		{"Bearer token (required)", "Bearer token", true},
		{"Bearer token (REQUIRED)", "Bearer token", true},
		{"some-value (optional)", "some-value", false},
		{"abc-123", "abc-123", false},
		{"  spaced  (required)  ", "spaced", true},
		{"(required)", "", true}, // value is empty, just the annotation
		{"no-annotation", "no-annotation", false},
	}
	for _, tc := range cases {
		gotVal, gotReq := parseHeaderValue(tc.raw)
		if gotVal != tc.wantVal || gotReq != tc.wantReq {
			t.Errorf("parseHeaderValue(%q) = (%q, %v), want (%q, %v)",
				tc.raw, gotVal, gotReq, tc.wantVal, tc.wantReq)
		}
	}
}

// TestRequestHeaders_ConvertedToParameters checks that `+ Headers` blocks on
// httpRequest elements are converted to in:header OAS Parameters using the
// real Drafter output format: annotation is embedded in the value string
// (e.g. "Bearer token (required)"), no meta.description, no typeAttributes.
func TestRequestHeaders_ConvertedToParameters(t *testing.T) {
	// This Refract JSON mirrors what real Drafter emits for:
	//   + Request (application/json)
	//       + Headers
	//           Authorization: Bearer token (required)
	//           X-Request-ID: abc-123
	//           Content-Type: application/json
	refract := []byte(`{
	  "element":"parseResult","content":[{
	    "element":"category",
	    "meta":{"classes":{"element":"array","content":[{"element":"string","content":"api"}]}},
	    "content":[{
	      "element":"resource",
	      "attributes":{"href":{"element":"string","content":"/things"}},
	      "content":[{
	        "element":"transition",
	        "content":[{
	          "element":"httpTransaction",
	          "content":[{
	            "element":"httpRequest",
	            "attributes":{
	              "method":{"element":"string","content":"GET"},
	              "headers":{"element":"httpHeaders","content":[
	                {
	                  "element":"member",
	                  "content":{"key":{"element":"string","content":"Content-Type"},"value":{"element":"string","content":"application/json"}}
	                },
	                {
	                  "element":"member",
	                  "content":{"key":{"element":"string","content":"Authorization"},"value":{"element":"string","content":"Bearer abc (required)"}}
	                },
	                {
	                  "element":"member",
	                  "content":{"key":{"element":"string","content":"X-Request-ID"},"value":{"element":"string","content":"abc-123"}}
	                }
	              ]}
	            },
	            "content":[]
	          },{
	            "element":"httpResponse",
	            "attributes":{"statusCode":{"element":"string","content":"200"}},
	            "content":[]
	          }]
	        }]
	      }]
	    }]
	  }]
	}`)

	doc, err := RefractToOAS(refract)
	if err != nil {
		t.Fatalf("RefractToOAS: %v", err)
	}
	pi := doc.Paths["/things"]
	if pi == nil {
		t.Fatal("expected /things path item")
	}
	op := pi.Get
	if op == nil {
		t.Fatal("expected GET operation on /things")
	}

	byName := map[string]*oas.Parameter{}
	for _, p := range op.Parameters {
		byName[p.Name] = p
	}

	// Content-Type must never appear as a parameter.
	if _, ok := byName["Content-Type"]; ok {
		t.Error("Content-Type must not be emitted as an in:header parameter")
	}

	// Authorization: required=true, annotation stripped from example.
	auth, ok := byName["Authorization"]
	if !ok {
		t.Fatal("expected Authorization header parameter")
	}
	if auth.In != "header" {
		t.Errorf("Authorization.In = %q, want header", auth.In)
	}
	if !auth.Required {
		t.Error("Authorization should be required")
	}
	if auth.Schema == nil || auth.Schema.Example != "Bearer abc" {
		t.Errorf("Authorization schema example = %v, want \"Bearer abc\"", auth.Schema)
	}

	// X-Request-ID: optional, plain value unchanged.
	xrid, ok := byName["X-Request-ID"]
	if !ok {
		t.Fatal("expected X-Request-ID header parameter")
	}
	if xrid.In != "header" {
		t.Errorf("X-Request-ID.In = %q, want header", xrid.In)
	}
	if xrid.Required {
		t.Error("X-Request-ID should not be required")
	}
	if xrid.Schema == nil || xrid.Schema.Example != "abc-123" {
		t.Errorf("X-Request-ID schema example = %v, want \"abc-123\"", xrid.Schema)
	}
}

// TestRequestHeaders_MultiTransactionDedup guards that when a transition has
// multiple httpTransaction examples each re-declaring the same headers, the
// parameters are not duplicated (first declaration wins via appendUniqueParam).
func TestRequestHeaders_MultiTransactionDedup(t *testing.T) {
	refract := []byte(`{
	  "element":"parseResult","content":[{
	    "element":"category",
	    "meta":{"classes":{"element":"array","content":[{"element":"string","content":"api"}]}},
	    "content":[{
	      "element":"resource",
	      "attributes":{"href":{"element":"string","content":"/items"}},
	      "content":[{
	        "element":"transition",
	        "content":[
	          {
	            "element":"httpTransaction",
	            "content":[{
	              "element":"httpRequest",
	              "attributes":{
	                "method":{"element":"string","content":"POST"},
	                "headers":{"element":"httpHeaders","content":[
	                  {
	                    "element":"member",
	                    "content":{"key":{"element":"string","content":"X-Token"},"value":{"element":"string","content":"first-value"}}
	                  }
	                ]}
	              },
	              "content":[]
	            },{
	              "element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"201"}},"content":[]
	            }]
	          },
	          {
	            "element":"httpTransaction",
	            "content":[{
	              "element":"httpRequest",
	              "attributes":{
	                "method":{"element":"string","content":"POST"},
	                "headers":{"element":"httpHeaders","content":[
	                  {
	                    "element":"member",
	                    "content":{"key":{"element":"string","content":"X-Token"},"value":{"element":"string","content":"second-value"}}
	                  }
	                ]}
	              },
	              "content":[]
	            },{
	              "element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"201"}},"content":[]
	            }]
	          }
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
	if op == nil {
		t.Fatal("expected POST on /items")
	}
	count := 0
	var firstParam *oas.Parameter
	for _, p := range op.Parameters {
		if p.Name == "X-Token" && p.In == "header" {
			count++
			if firstParam == nil {
				firstParam = p
			}
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 X-Token header parameter, got %d", count)
	}
	// First declaration wins.
	if firstParam != nil && firstParam.Schema != nil && firstParam.Schema.Example != "first-value" {
		t.Errorf("X-Token example = %v, want \"first-value\" (first declaration wins)", firstParam.Schema.Example)
	}
}

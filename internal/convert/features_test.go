package convert

import (
	"testing"

	"github.com/alecoletti/apib-to-oas/internal/oas"
)

// TestExamples_MultipleResponses_SameStatus_EmitMap verifies that two
// `+ Response 200 (application/json)` blocks under the same action are
// rendered as an OAS examples: map keyed by request title (or
// auto-numbered) instead of one overwriting the other.
func TestExamples_MultipleResponses_SameStatus_EmitMap(t *testing.T) {
	refract := []byte(`{
	  "element":"parseResult",
	  "content":[{
	    "element":"category",
	    "content":[{
	      "element":"resource","attributes":{"href":{"element":"string","content":"/x"}},
	      "content":[{
	        "element":"transition","meta":{"title":{"element":"string","content":"Get X"}},
	        "content":[
	          {"element":"httpTransaction","content":[
	            {"element":"httpRequest","meta":{"title":{"element":"string","content":"happy"}},"attributes":{"method":{"element":"string","content":"GET"}},"content":[]},
	            {"element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"200"},"headers":{"element":"httpHeaders","content":[
	              {"element":"member","content":{"key":{"element":"string","content":"Content-Type"},"value":{"element":"string","content":"application/json"}}}
	            ]}},"content":[
	              {"element":"asset","meta":{"classes":{"element":"array","content":[{"element":"string","content":"messageBody"}]}},"content":"{\"a\":1}"}
	            ]}
	          ]},
	          {"element":"httpTransaction","content":[
	            {"element":"httpRequest","meta":{"title":{"element":"string","content":"sad"}},"attributes":{"method":{"element":"string","content":"GET"}},"content":[]},
	            {"element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"200"},"headers":{"element":"httpHeaders","content":[
	              {"element":"member","content":{"key":{"element":"string","content":"Content-Type"},"value":{"element":"string","content":"application/json"}}}
	            ]}},"content":[
	              {"element":"asset","meta":{"classes":{"element":"array","content":[{"element":"string","content":"messageBody"}]}},"content":"{\"a\":2}"}
	            ]}
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
	mt := doc.Paths["/x"].Get.Responses["200"].Content["application/json"]
	if mt == nil {
		t.Fatal("missing media type")
	}
	if mt.Example != nil {
		t.Errorf("expected scalar example to be cleared once examples map used, got %+v", mt.Example)
	}
	if len(mt.Examples) != 2 {
		t.Fatalf("expected 2 examples, got %d: %+v", len(mt.Examples), mt.Examples)
	}
	if _, ok := mt.Examples["happy"]; !ok {
		t.Errorf("expected `happy` example key, got %+v", mt.Examples)
	}
	if _, ok := mt.Examples["sad"]; !ok {
		t.Errorf("expected `sad` example key, got %+v", mt.Examples)
	}
}

// TestExamples_SingleResponse_StillUsesScalar confirms the regression case
// - one transaction keeps the existing `example:` scalar shape.
func TestExamples_SingleResponse_StillUsesScalar(t *testing.T) {
	refract := []byte(`{
	  "element":"parseResult",
	  "content":[{
	    "element":"category",
	    "content":[{
	      "element":"resource","attributes":{"href":{"element":"string","content":"/x"}},
	      "content":[{
	        "element":"transition","meta":{"title":{"element":"string","content":"Get"}},
	        "content":[{"element":"httpTransaction","content":[
	          {"element":"httpRequest","attributes":{"method":{"element":"string","content":"GET"}},"content":[]},
	          {"element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"200"},"headers":{"element":"httpHeaders","content":[
	            {"element":"member","content":{"key":{"element":"string","content":"Content-Type"},"value":{"element":"string","content":"application/json"}}}
	          ]}},"content":[
	            {"element":"asset","meta":{"classes":{"element":"array","content":[{"element":"string","content":"messageBody"}]}},"content":"{\"a\":1}"}
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
	if mt.Example == nil {
		t.Fatalf("expected scalar example, got %+v", mt)
	}
	if len(mt.Examples) != 0 {
		t.Errorf("expected no examples map, got %+v", mt.Examples)
	}
}

// TestSecurity_Apply verifies that a SecurityConfig populates
// components.securitySchemes, document-level security, and per-operation
// overrides as appropriate.
func TestSecurity_Apply(t *testing.T) {
	refract := []byte(`{
	  "element":"parseResult",
	  "content":[{
	    "element":"category","meta":{"title":{"element":"string","content":"X"}},
	    "content":[{
	      "element":"resource","attributes":{"href":{"element":"string","content":"/public"}},
	      "content":[{
	        "element":"transition","meta":{"title":{"element":"string","content":"GetPublic"}},
	        "content":[{"element":"httpTransaction","content":[
	          {"element":"httpRequest","attributes":{"method":{"element":"string","content":"GET"}},"content":[]},
	          {"element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"200"}},"content":[]}
	        ]}]
	      }]
	    }]
	  }]
	}`)
	cfg := &SecurityConfig{
		SecuritySchemes: map[string]*oas.SecurityScheme{
			"bearerAuth": {Type: "http", Scheme: "bearer", BearerFormat: "JWT"},
		},
		DefaultSecurity: []oas.SecurityRequirement{{"bearerAuth": []string{}}},
		Overrides: SecurityOverrides{
			ByPath: map[string][]oas.SecurityRequirement{
				"/public": {}, // no security
			},
		},
	}
	doc, err := RefractToOASWithOptions(refract, Options{Security: cfg})
	if err != nil {
		t.Fatalf("RefractToOASWithOptions: %v", err)
	}
	if doc.Components == nil || doc.Components.SecuritySchemes["bearerAuth"] == nil {
		t.Fatal("expected components.securitySchemes.bearerAuth")
	}
	if len(doc.Security) != 1 {
		t.Errorf("expected document-level default security, got %+v", doc.Security)
	}
	op := doc.Paths["/public"].Get
	if op.Security == nil {
		t.Errorf("expected explicit empty per-op security override, got nil")
	}
	if len(op.Security) != 0 {
		t.Errorf("expected empty []SecurityRequirement on /public, got %+v", op.Security)
	}
}

// TestWebhooks_RoutedOn31 verifies that a category named in
// WEBHOOK_GROUPS metadata routes its resources into doc.Webhooks (not
// doc.Paths) when the OAS version is 3.1+.
func TestWebhooks_RoutedOn31(t *testing.T) {
	refract := []byte(`{
	  "element":"parseResult",
	  "content":[{
	    "element":"category","meta":{"title":{"element":"string","content":"API"}},
	    "attributes":{"metadata":{"element":"array","content":[
	      {"element":"member","content":{"key":{"element":"string","content":"WEBHOOK_GROUPS"},"value":{"element":"string","content":"Notifications"}}}
	    ]}},
	    "content":[{
	      "element":"category","meta":{"title":{"element":"string","content":"Notifications"},"classes":{"element":"array","content":[{"element":"string","content":"resourceGroup"}]}},
	      "content":[{
	        "element":"resource","attributes":{"href":{"element":"string","content":"/hook"}},
	        "content":[{
	          "element":"transition","meta":{"title":{"element":"string","content":"OnPing"}},
	          "content":[{"element":"httpTransaction","content":[
	            {"element":"httpRequest","attributes":{"method":{"element":"string","content":"POST"}},"content":[]},
	            {"element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"200"}},"content":[]}
	          ]}]
	        }]
	      }]
	    }]
	  }]
	}`)
	// 3.0 → still goes to paths.
	doc30, err := RefractToOASWithOptions(refract, Options{OASVersion: "3.0"})
	if err != nil {
		t.Fatalf("3.0: %v", err)
	}
	if doc30.Webhooks != nil {
		t.Errorf("3.0 should not emit webhooks, got %+v", doc30.Webhooks)
	}
	if doc30.Paths["/hook"] == nil {
		t.Errorf("3.0 should keep /hook in paths")
	}
	// 3.1 → routed to webhooks.
	doc31, err := RefractToOASWithOptions(refract, Options{OASVersion: "3.1"})
	if err != nil {
		t.Fatalf("3.1: %v", err)
	}
	if doc31.Webhooks["/hook"] == nil {
		t.Errorf("3.1 expected /hook under webhooks, got %+v", doc31.Webhooks)
	}
	if doc31.Paths["/hook"] != nil {
		t.Errorf("3.1 should NOT have /hook in paths")
	}
}

// TestTags_HierarchicalOn32 verifies that nested resourceGroup categories
// produce tags with `parent: <parentTag>` only when --oas-version 3.2.
func TestTags_HierarchicalOn32(t *testing.T) {
	refract := []byte(`{
	  "element":"parseResult",
	  "content":[{
	    "element":"category","meta":{"title":{"element":"string","content":"API"}},
	    "content":[{
	      "element":"category","meta":{"title":{"element":"string","content":"Outer"},"classes":{"element":"array","content":[{"element":"string","content":"resourceGroup"}]}},
	      "content":[
	        {"element":"resource","attributes":{"href":{"element":"string","content":"/o"}},
	          "content":[{"element":"transition","meta":{"title":{"element":"string","content":"GetO"}},"content":[{"element":"httpTransaction","content":[
	            {"element":"httpRequest","attributes":{"method":{"element":"string","content":"GET"}},"content":[]},
	            {"element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"200"}},"content":[]}
	          ]}]}]},
	        {"element":"category","meta":{"title":{"element":"string","content":"Inner"},"classes":{"element":"array","content":[{"element":"string","content":"resourceGroup"}]}},
	          "content":[{
	            "element":"resource","attributes":{"href":{"element":"string","content":"/i"}},
	            "content":[{"element":"transition","meta":{"title":{"element":"string","content":"GetI"}},"content":[{"element":"httpTransaction","content":[
	              {"element":"httpRequest","attributes":{"method":{"element":"string","content":"GET"}},"content":[]},
	              {"element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"200"}},"content":[]}
	            ]}]}]
	          }]
	        }
	      ]
	    }]
	  }]
	}`)
	doc30, err := RefractToOASWithOptions(refract, Options{OASVersion: "3.0"})
	if err != nil {
		t.Fatalf("3.0: %v", err)
	}
	for _, tg := range doc30.Tags {
		if tg.Parent != "" {
			t.Errorf("3.0 tag %q should not have parent, got %q", tg.Name, tg.Parent)
		}
	}
	doc32, err := RefractToOASWithOptions(refract, Options{OASVersion: "3.2"})
	if err != nil {
		t.Fatalf("3.2: %v", err)
	}
	tagByName := map[string]oas.Tag{}
	for _, tg := range doc32.Tags {
		tagByName[tg.Name] = tg
	}
	if got := tagByName["Inner"].Parent; got != "Outer" {
		t.Errorf("3.2 expected Inner.parent=Outer, got %q", got)
	}
	if got := tagByName["Outer"].Parent; got != "" {
		t.Errorf("3.2 expected Outer.parent='', got %q", got)
	}
}

// TestExamples_DedupesIdenticalRequestBodyAcrossTransactions guards
// against a regression where Drafter's "one httpTransaction per
// Response under a single Request" structure caused the same authored
// request body to be re-attached N times (once per response status),
// producing example1, example2, example3, … all with identical content.
//
// addExample now compares values structurally and skips duplicates, so
// a single authored Request → single `example:` regardless of how many
// `+ Response` blocks share it.
func TestExamples_DedupesIdenticalRequestBodyAcrossTransactions(t *testing.T) {
	body := `{\"data\":{\"title\":\"hi\"}}`
	mkTx := func(status string) string {
		return `{"element":"httpTransaction","content":[
		  {"element":"httpRequest","attributes":{"method":{"element":"string","content":"POST"},"headers":{"element":"httpHeaders","content":[
		    {"element":"member","content":{"key":{"element":"string","content":"Content-Type"},"value":{"element":"string","content":"application/json"}}}
		  ]}},"content":[
		    {"element":"asset","meta":{"classes":{"element":"array","content":[{"element":"string","content":"messageBody"}]}},"content":"` + body + `"}
		  ]},
		  {"element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"` + status + `"}},"content":[]}
		]}`
	}
	refract := []byte(`{
	  "element":"parseResult","content":[{
	    "element":"category","content":[{
	      "element":"resource","attributes":{"href":{"element":"string","content":"/x"}},
	      "content":[{
	        "element":"transition","meta":{"title":{"element":"string","content":"Create"}},
	        "content":[` + mkTx("201") + `,` + mkTx("400") + `,` + mkTx("401") + `,` + mkTx("500") + `]
	      }]
	    }]
	  }]
	}`)
	doc, err := RefractToOAS(refract)
	if err != nil {
		t.Fatalf("RefractToOAS: %v", err)
	}
	mt := doc.Paths["/x"].Post.RequestBody.Content["application/json"]
	if mt == nil {
		t.Fatal("expected request body media type")
	}
	if mt.Example == nil {
		t.Errorf("expected single scalar example, got %+v", mt)
	}
	if len(mt.Examples) != 0 {
		t.Errorf("identical request bodies should not produce an examples map; got %d entries: %+v", len(mt.Examples), mt.Examples)
	}
}

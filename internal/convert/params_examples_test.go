package convert

import "testing"

// TestParams_PreserveExamplesAndRequired guards the mapping from APIB
// hrefVariables to OAS Parameters: the sample value, declared type, and
// `required`/`optional` typeAttribute must all survive the conversion.
//
// Drafter serialises every primitive sample as a JSON string regardless
// of the declared MSON type (e.g. `+ limit: 20 (number)` arrives as
// `value: "20"`); the converter must coerce it back to a number so the
// rendered OAS carries `example: 20`, not `example: "20"`.
func TestParams_PreserveExamplesAndRequired(t *testing.T) {
	refract := []byte(`{
	  "element": "parseResult",
	  "content": [{
	    "element": "category",
	    "content": [{
	      "element":"resource",
	      "attributes":{
	        "href":{"element":"string","content":"/items/{id}{?q,limit}"},
	        "hrefVariables":{"element":"hrefVariables","content":[
	          {"element":"member","meta":{"description":{"element":"string","content":"Item UUID."},"title":{"element":"string","content":"string"}},
	            "attributes":{"typeAttributes":{"element":"array","content":[{"element":"string","content":"required"}]}},
	            "content":{"key":{"element":"string","content":"id"},"value":{"element":"string","content":"8c6e8f54-dc08-4e62-9a20-5e0c9e1c1234"}}},
	          {"element":"member","meta":{"description":{"element":"string","content":"Free-text query."},"title":{"element":"string","content":"string"}},
	            "attributes":{"typeAttributes":{"element":"array","content":[{"element":"string","content":"optional"}]}},
	            "content":{"key":{"element":"string","content":"q"},"value":{"element":"string","content":"news"}}},
	          {"element":"member","meta":{"description":{"element":"string","content":"Page size."},"title":{"element":"string","content":"number"}},
	            "attributes":{"typeAttributes":{"element":"array","content":[{"element":"string","content":"required"}]}},
	            "content":{"key":{"element":"string","content":"limit"},"value":{"element":"string","content":"20"}}}
	        ]}
	      },
	      "content":[{"element":"transition","meta":{"title":{"element":"string","content":"Get"}},
	        "content":[{"element":"httpTransaction","content":[
	          {"element":"httpRequest","attributes":{"method":{"element":"string","content":"GET"}},"content":[]},
	          {"element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"200"}},"content":[]}
	        ]}]}]
	    }]
	  }]
	}`)

	doc, err := RefractToOAS(refract)
	if err != nil {
		t.Fatalf("RefractToOAS: %v", err)
	}
	pi := doc.Paths["/items/{id}"]
	if pi == nil || pi.Get == nil {
		t.Fatal("expected GET on /items/{id}")
	}

	byName := map[string]struct {
		in       string
		desc     string
		required bool
		example  any
		format   string
	}{}
	// Path parameter sits on the PathItem; query parameters on the operation.
	for _, p := range pi.Parameters {
		byName[p.Name] = struct {
			in       string
			desc     string
			required bool
			example  any
			format   string
		}{p.In, p.Description, p.Required, p.Schema.Example, p.Schema.Format}
	}
	for _, p := range pi.Get.Parameters {
		byName[p.Name] = struct {
			in       string
			desc     string
			required bool
			example  any
			format   string
		}{p.In, p.Description, p.Required, p.Schema.Example, p.Schema.Format}
	}

	cases := []struct {
		name, in, desc, format string
		required               bool
		example                any
	}{
		{"id", "path", "Item UUID.", "uuid", true, "8c6e8f54-dc08-4e62-9a20-5e0c9e1c1234"},
		{"q", "query", "Free-text query.", "", false, "news"},
		{"limit", "query", "Page size.", "", true, float64(20)},
	}
	for _, tc := range cases {
		got, ok := byName[tc.name]
		if !ok {
			t.Errorf("missing parameter %q", tc.name)
			continue
		}
		if got.in != tc.in {
			t.Errorf("%s.in = %q, want %q", tc.name, got.in, tc.in)
		}
		if got.desc != tc.desc {
			t.Errorf("%s.description = %q, want %q", tc.name, got.desc, tc.desc)
		}
		if got.required != tc.required {
			t.Errorf("%s.required = %v, want %v", tc.name, got.required, tc.required)
		}
		if got.example != tc.example {
			t.Errorf("%s.schema.example = %#v, want %#v", tc.name, got.example, tc.example)
		}
		if got.format != tc.format {
			t.Errorf("%s.schema.format = %q, want %q", tc.name, got.format, tc.format)
		}
	}
}

// TestParams_ActionLevelOverrideStubs guards against the bug where
// action-level `+ Parameters` (declared under the action header — Drafter
// attaches them to the transition's hrefVariables) lose their description,
// example, and required flag because the URI-template stub the resource
// walker pre-populated was processed first and `appendUniqueParam` rejected
// the richer entry as a duplicate.
//
// Authoring shape this defends:
//
//	## Article Collection [/v2/articles{?q,limit}]
//	### Search Articles [GET]
//	+ Parameters
//	    + q: news (string, optional) - Free-text search.
//	    + limit: 20 (number, optional) - Page size.
func TestParams_ActionLevelOverrideStubs(t *testing.T) {
	refract := []byte(`{
	  "element": "parseResult",
	  "content": [{
	    "element": "category",
	    "content": [{
	      "element":"resource",
	      "attributes":{"href":{"element":"string","content":"/v2/articles{?q,limit}"}},
	      "content":[{"element":"transition","meta":{"title":{"element":"string","content":"Search"}},
	        "attributes":{"hrefVariables":{"element":"hrefVariables","content":[
	          {"element":"member","meta":{"description":{"element":"string","content":"Free-text search."},"title":{"element":"string","content":"string"}},
	            "attributes":{"typeAttributes":{"element":"array","content":[{"element":"string","content":"optional"}]}},
	            "content":{"key":{"element":"string","content":"q"},"value":{"element":"string","content":"news"}}},
	          {"element":"member","meta":{"description":{"element":"string","content":"Page size."},"title":{"element":"string","content":"number"}},
	            "attributes":{"typeAttributes":{"element":"array","content":[{"element":"string","content":"required"}]}},
	            "content":{"key":{"element":"string","content":"limit"},"value":{"element":"string","content":"20"}}}
	        ]}},
	        "content":[{"element":"httpTransaction","content":[
	          {"element":"httpRequest","attributes":{"method":{"element":"string","content":"GET"}},"content":[]},
	          {"element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"200"}},"content":[]}
	        ]}]}]
	    }]
	  }]
	}`)
	doc, err := RefractToOAS(refract)
	if err != nil {
		t.Fatalf("RefractToOAS: %v", err)
	}
	pi := doc.Paths["/v2/articles"]
	if pi == nil || pi.Get == nil {
		t.Fatal("expected GET on /v2/articles")
	}
	by := map[string]*struct {
		desc     string
		required bool
		typ      string
		example  any
	}{}
	for _, p := range pi.Get.Parameters {
		by[p.Name] = &struct {
			desc     string
			required bool
			typ      string
			example  any
		}{p.Description, p.Required, p.Schema.Type, p.Schema.Example}
	}
	q := by["q"]
	if q == nil || q.desc != "Free-text search." || q.example != "news" || q.typ != "string" {
		t.Errorf("q lost authoring detail: %#v", q)
	}
	l := by["limit"]
	if l == nil || l.desc != "Page size." || l.required != true || l.typ != "number" || l.example != float64(20) {
		t.Errorf("limit lost authoring detail: %#v", l)
	}
}

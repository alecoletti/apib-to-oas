package convert

import (
	"strings"
	"testing"

	"github.com/alecoletti/apib-to-oas/internal/oas"
)

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
	if l == nil || l.desc != "Page size." || l.required != true || l.typ != "integer" || l.example != float64(20) {
		t.Errorf("limit lost authoring detail: %#v", l)
	}
}

// TestParams_IntegerInference_MSONProperty verifies that a data-structure
// member typed as `(integer, optional)` — which the drafter pre-processor
// normalises to `(number, optional)` so Drafter can parse it — is emitted
// as `type: integer` because the example value 10 is a whole number.
// A member typed as `(number)` with a fractional example stays `number`.
func TestParams_IntegerInference_MSONProperty(t *testing.T) {
	refract := []byte(`{
	  "element":"parseResult",
	  "content":[{
	    "element":"category",
	    "content":[
	      {"element":"category","meta":{"classes":{"element":"array","content":[{"element":"string","content":"dataStructures"}]}},"content":[
	        {"element":"dataStructure","content":{
	          "element":"object","meta":{"id":{"element":"string","content":"Stats"}},
	          "content":[
	            {"element":"member","meta":{"description":{"element":"string","content":"Whole count."}},"content":{
	              "key":{"element":"string","content":"count"},
	              "value":{"element":"number","content":10}}},
	            {"element":"member","meta":{"description":{"element":"string","content":"Float ratio."}},"content":{
	              "key":{"element":"string","content":"ratio"},
	              "value":{"element":"number","content":0.75}}}
	          ]}}
	      ]}
	    ]
	  }]
	}`)
	doc, err := RefractToOAS(refract)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	stats := doc.Components.Schemas["Stats"]
	if stats == nil {
		t.Fatal("Stats schema missing")
	}
	count := stats.Properties["count"]
	if count == nil || count.Type != "integer" {
		t.Errorf("count: want type integer; got %+v", count)
	}
	ratio := stats.Properties["ratio"]
	if ratio == nil || ratio.Type != "number" {
		t.Errorf("ratio: want type number; got %+v", ratio)
	}
}

// TestParams_IntegerInference_HrefVariables verifies that a hrefVariables
// parameter declared as `(number, optional)` with a whole-number example
// is promoted to `type: integer`. This covers the case where the drafter
// pre-processor normalises `(integer, optional)` → `(number, optional)`.
// A parameter with no example (or fractional example) stays `number`.
func TestParams_IntegerInference_HrefVariables(t *testing.T) {
	refract := []byte(`{
	  "element":"parseResult",
	  "content":[{
	    "element":"category",
	    "content":[{
	      "element":"resource",
	      "attributes":{
	        "href":{"element":"string","content":"/items{?page,ratio}"},
	        "hrefVariables":{"element":"hrefVariables","content":[
	          {"element":"member","meta":{"title":{"element":"string","content":"number"},"description":{"element":"string","content":"Page number."}},
	            "attributes":{"typeAttributes":{"element":"array","content":[{"element":"string","content":"optional"}]}},
	            "content":{"key":{"element":"string","content":"page"},"value":{"element":"string","content":"1"}}},
	          {"element":"member","meta":{"title":{"element":"string","content":"number"},"description":{"element":"string","content":"Float ratio."}},
	            "attributes":{"typeAttributes":{"element":"array","content":[{"element":"string","content":"optional"}]}},
	            "content":{"key":{"element":"string","content":"ratio"},"value":{"element":"string","content":"0.5"}}}
	        ]}
	      },
	      "content":[{"element":"transition","meta":{"title":{"element":"string","content":"List"}},
	        "content":[{"element":"httpTransaction","content":[
	          {"element":"httpRequest","attributes":{"method":{"element":"string","content":"GET"}},"content":[]},
	          {"element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"200"}},"content":[]}
	        ]}]}]
	    }]
	  }]
	}`)
	doc, err := RefractToOAS(refract)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	pi := doc.Paths["/items"]
	if pi == nil || pi.Get == nil {
		t.Fatal("expected GET on /items")
	}
	byName := map[string]*oas.Parameter{}
	for _, p := range pi.Get.Parameters {
		byName[p.Name] = p
	}
	page := byName["page"]
	if page == nil || page.Schema == nil || page.Schema.Type != "integer" {
		t.Errorf("page: want type integer (whole-number example); got %+v", page)
	}
	ratio := byName["ratio"]
	if ratio == nil || ratio.Schema == nil || ratio.Schema.Type != "number" {
		t.Errorf("ratio: want type number (fractional example); got %+v", ratio)
	}
}

// TestParams_ArrayTypeInHrefVariables verifies that parameters declared as
// (array[string]) or (array[number]) produce schema:
//
//	type: array
//	items:
//	  type: string   # (or number / integer)
//
// Previously msonTypeToOAS("array[string]") fell through to the default
// "string" branch, discarding the array wrapper entirely.
func TestParams_ArrayTypeInHrefVariables(t *testing.T) {
	refract := []byte(`{
	  "element":"parseResult",
	  "content":[{
	    "element":"category",
	    "content":[{
	      "element":"resource",
	      "attributes":{
	        "href":{"element":"string","content":"/items{?tags,scores}"},
	        "hrefVariables":{"element":"hrefVariables","content":[
	          {"element":"member",
	           "meta":{"title":{"element":"string","content":"array[string]"},"description":{"element":"string","content":"Tag filter."}},
	           "attributes":{"typeAttributes":{"element":"array","content":[{"element":"string","content":"optional"}]}},
	           "content":{"key":{"element":"string","content":"tags"},"value":{"element":"string","content":"sport:football"}}},
	          {"element":"member",
	           "meta":{"title":{"element":"string","content":"array[number]"},"description":{"element":"string","content":"Score list."}},
	           "attributes":{"typeAttributes":{"element":"array","content":[{"element":"string","content":"optional"}]}},
	           "content":{"key":{"element":"string","content":"scores"},"value":{"element":"string","content":"42"}}}
	        ]}
	      },
	      "content":[{"element":"transition","meta":{"title":{"element":"string","content":"List"}},
	        "content":[{"element":"httpTransaction","content":[
	          {"element":"httpRequest","attributes":{"method":{"element":"string","content":"GET"}},"content":[]},
	          {"element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"200"}},"content":[]}
	        ]}]}]
	    }]
	  }]
	}`)
	doc, err := RefractToOAS(refract)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	pi := doc.Paths["/items"]
	if pi == nil || pi.Get == nil {
		t.Fatal("expected GET on /items")
	}
	byName := map[string]*oas.Parameter{}
	for _, p := range pi.Get.Parameters {
		byName[p.Name] = p
	}

	tags := byName["tags"]
	if tags == nil || tags.Schema == nil {
		t.Fatal("tags parameter missing")
	}
	if tags.Schema.Type != "array" {
		t.Errorf("tags type: want array; got %q", tags.Schema.Type)
	}
	if tags.Schema.Items == nil || tags.Schema.Items.Type != "string" {
		t.Errorf("tags items: want {type:string}; got %+v", tags.Schema.Items)
	}

	scores := byName["scores"]
	if scores == nil || scores.Schema == nil {
		t.Fatal("scores parameter missing")
	}
	if scores.Schema.Type != "array" {
		t.Errorf("scores type: want array; got %q", scores.Schema.Type)
	}
	if scores.Schema.Items == nil || scores.Schema.Items.Type != "number" {
		t.Errorf("scores items: want {type:number}; got %+v", scores.Schema.Items)
	}
}

// TestParams_ArrayItemsFormatInference verifies that format inference is
// applied to the items schema of array[string] parameters when the
// example value's shape is unambiguous (email, UUID, date-time, date,
// URI). Previously inferFormat returned "" for type:"array" and the
// items sub-schema was never examined.
func TestParams_ArrayItemsFormatInference(t *testing.T) {
	//  + author:    `editor@example.com`         (array[string], optional)
	//  + ids:       `7c9b1e8a-…`                 (array[string], optional)
	//  + dates:     `2026-04-01`                  (array[string], optional)
	//  + datetimes: `2026-04-01T10:00:00Z`        (array[string], optional)
	//  + links:     `https://example.com`         (array[string], optional)
	//  + tags:      `sport:football`              (array[string], optional)  — no format
	refract := []byte(`{
	  "element":"parseResult",
	  "content":[{
	    "element":"category",
	    "content":[{
	      "element":"resource",
	      "attributes":{
	        "href":{"element":"string","content":"/items{?author,ids,dates,datetimes,links,tags}"},
	        "hrefVariables":{"element":"hrefVariables","content":[
	          {"element":"member",
	           "meta":{"title":{"element":"string","content":"array[string]"},"description":{"element":"string","content":"Author emails."}},
	           "attributes":{"typeAttributes":{"element":"array","content":[{"element":"string","content":"optional"}]}},
	           "content":{"key":{"element":"string","content":"author"},"value":{"element":"string","content":"editor@example.com"}}},
	          {"element":"member",
	           "meta":{"title":{"element":"string","content":"array[string]"},"description":{"element":"string","content":"Article UUIDs."}},
	           "attributes":{"typeAttributes":{"element":"array","content":[{"element":"string","content":"optional"}]}},
	           "content":{"key":{"element":"string","content":"ids"},"value":{"element":"string","content":"7c9b1e8a-2f4d-4d9e-9b5d-1f0a4c2c6c11"}}},
	          {"element":"member",
	           "meta":{"title":{"element":"string","content":"array[string]"},"description":{"element":"string","content":"Date filter."}},
	           "attributes":{"typeAttributes":{"element":"array","content":[{"element":"string","content":"optional"}]}},
	           "content":{"key":{"element":"string","content":"dates"},"value":{"element":"string","content":"2026-04-01"}}},
	          {"element":"member",
	           "meta":{"title":{"element":"string","content":"array[string]"},"description":{"element":"string","content":"Date-time filter."}},
	           "attributes":{"typeAttributes":{"element":"array","content":[{"element":"string","content":"optional"}]}},
	           "content":{"key":{"element":"string","content":"datetimes"},"value":{"element":"string","content":"2026-04-01T10:00:00Z"}}},
	          {"element":"member",
	           "meta":{"title":{"element":"string","content":"array[string]"},"description":{"element":"string","content":"Links."}},
	           "attributes":{"typeAttributes":{"element":"array","content":[{"element":"string","content":"optional"}]}},
	           "content":{"key":{"element":"string","content":"links"},"value":{"element":"string","content":"https://example.com/x"}}},
	          {"element":"member",
	           "meta":{"title":{"element":"string","content":"array[string]"},"description":{"element":"string","content":"Taxonomy tags."}},
	           "attributes":{"typeAttributes":{"element":"array","content":[{"element":"string","content":"optional"}]}},
	           "content":{"key":{"element":"string","content":"tags"},"value":{"element":"string","content":"sport:football"}}}
	        ]}
	      },
	      "content":[{"element":"transition","meta":{"title":{"element":"string","content":"List"}},
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
	pi := doc.Paths["/items"]
	if pi == nil || pi.Get == nil {
		t.Fatal("expected GET on /items")
	}
	byName := map[string]*oas.Parameter{}
	for _, p := range pi.Get.Parameters {
		byName[p.Name] = p
	}

	cases := []struct {
		name       string
		wantFormat string // expected items.format
	}{
		{"author", "email"},
		{"ids", "uuid"},
		{"dates", "date"},
		{"datetimes", "date-time"},
		{"links", "uri"},
		{"tags", ""}, // opaque string — no format
	}
	for _, tc := range cases {
		p := byName[tc.name]
		if p == nil {
			t.Errorf("parameter %q missing", tc.name)
			continue
		}
		if p.Schema == nil || p.Schema.Type != "array" {
			t.Errorf("%s: want array schema; got %+v", tc.name, p.Schema)
			continue
		}
		if p.Schema.Items == nil {
			t.Errorf("%s: items schema missing", tc.name)
			continue
		}
		if got := p.Schema.Items.Format; got != tc.wantFormat {
			t.Errorf("%s: items.format = %q, want %q", tc.name, got, tc.wantFormat)
		}
	}
}

// TestParams_EnumMembersInHrefVariables verifies that a parameter declared as
// `(enum[string], required)` with a `+ Members` block has its enum values
// lifted into schema.enum. Drafter places the Members values on
// content.value.attributes.enumerations, not on the member's own attributes.
func TestParams_EnumMembersInHrefVariables(t *testing.T) {
	//  + status: draft (enum[string], required) - Status filter
	//      + Members
	//          + draft
	//          + published
	// Drafter Refract produced by the above (value element is "enum" with
	// enumerations on value.attributes, not on the member).
	refract := []byte(`{
	  "element":"parseResult",
	  "content":[{
	    "element":"category",
	    "content":[{
	      "element":"resource",
	      "attributes":{
	        "href":{"element":"string","content":"/items{?status}"},
	        "hrefVariables":{"element":"hrefVariables","content":[
	          {"element":"member",
	           "meta":{"title":{"element":"string","content":"string"},"description":{"element":"string","content":"Status filter."}},
	           "attributes":{"typeAttributes":{"element":"array","content":[{"element":"string","content":"required"}]}},
	           "content":{
	             "key":{"element":"string","content":"status"},
	             "value":{"element":"enum",
	               "attributes":{"enumerations":{"element":"array","content":[
	                 {"element":"string","content":"draft"},
	                 {"element":"string","content":"published"}
	               ]}},
	               "content":{"element":"string","content":"draft"}}}}
	        ]}
	      },
	      "content":[{"element":"transition","meta":{"title":{"element":"string","content":"List"}},
	        "content":[{"element":"httpTransaction","content":[
	          {"element":"httpRequest","attributes":{"method":{"element":"string","content":"GET"}},"content":[]},
	          {"element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"200"}},"content":[]}
	        ]}]}]
	    }]
	  }]
	}`)
	doc, err := RefractToOAS(refract)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	pi := doc.Paths["/items"]
	if pi == nil || pi.Get == nil {
		t.Fatal("expected GET on /items")
	}
	var status *oas.Parameter
	for _, p := range pi.Get.Parameters {
		if p.Name == "status" {
			status = p
		}
	}
	if status == nil {
		t.Fatal("status parameter missing")
	}
	if status.Schema == nil {
		t.Fatal("status schema missing")
	}
	if status.Schema.Type != "string" {
		t.Errorf("status type: want string; got %q", status.Schema.Type)
	}
	if len(status.Schema.Enum) != 2 ||
		status.Schema.Enum[0] != "draft" || status.Schema.Enum[1] != "published" {
		t.Errorf("status enum: want [draft published]; got %v", status.Schema.Enum)
	}
	if !status.Required {
		t.Errorf("status: want required=true; got false")
	}
}

// TestParams_MetaConstraintsExtracted verifies that a Blueprint+ `+ Meta`
// block embedded in a parameter description is stripped from the rendered
// description and its constraint keys are applied to the parameter schema.
//
// Drafter folds the `+ Meta` block verbatim into the description text of
// the hrefVariables member; `paramsFromHrefVariables` must call
// `extractConstraintsFromDescription` to rescue the constraint keys.
func TestParams_MetaConstraintsExtracted(t *testing.T) {
	// descJSON is the JSON-string-encoded description that Drafter would emit:
	// backtick chars are literal in JSON; \n is the JSON newline escape.
	const sortDesc = "`field[:asc|desc]`. Sortable fields.\\n\\n+ Meta\\n    + Pattern: `^(createdAt|updatedAt|title)(:(asc|desc))?$`\\n    + MaxLength: 32"
	refract := []byte(`{"element":"parseResult","content":[{"element":"category","content":[{` +
		`"element":"resource","attributes":{` +
		`"href":{"element":"string","content":"/items{?sort}"},` +
		`"hrefVariables":{"element":"hrefVariables","content":[` +
		`{"element":"member",` +
		`"meta":{"description":{"element":"string","content":"` + sortDesc + `"},` +
		`"title":{"element":"string","content":"string"}},` +
		`"attributes":{"typeAttributes":{"element":"array","content":[{"element":"string","content":"optional"}]}},` +
		`"content":{"key":{"element":"string","content":"sort"},"value":{"element":"string","content":"publishedAt:desc"}}}` +
		`]}},` +
		`"content":[{"element":"transition","meta":{"title":{"element":"string","content":"List"}},` +
		`"content":[{"element":"httpTransaction","content":[` +
		`{"element":"httpRequest","attributes":{"method":{"element":"string","content":"GET"}},"content":[]},` +
		`{"element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"200"}},"content":[]}` +
		`]}]}]}]}]}`)

	doc, err := RefractToOAS(refract)
	if err != nil {
		t.Fatalf("RefractToOAS: %v", err)
	}
	pi, ok := doc.Paths["/items"]
	if !ok {
		t.Fatal("path /items not found")
	}
	if pi.Get == nil {
		t.Fatal("expected GET operation on /items")
	}
	var sort *oas.Parameter
	for _, p := range pi.Get.Parameters {
		if p.Name == "sort" {
			sort = p
			break
		}
	}
	if sort == nil {
		t.Fatal("sort parameter missing")
	}

	// The `+ Meta` block must be stripped from the description.
	if strings.Contains(sort.Description, "+ Meta") {
		t.Errorf("description still contains '+ Meta' block: %q", sort.Description)
	}
	if strings.Contains(sort.Description, "Pattern") {
		t.Errorf("description still contains 'Pattern' key: %q", sort.Description)
	}

	// The constraint keys must be on the schema.
	if sort.Schema == nil {
		t.Fatal("sort schema missing")
	}
	wantPattern := "^(createdAt|updatedAt|title)(:(asc|desc))?$"
	if sort.Schema.Pattern != wantPattern {
		t.Errorf("pattern = %q, want %q", sort.Schema.Pattern, wantPattern)
	}
	wantMax := 32
	if sort.Schema.MaxLength == nil || *sort.Schema.MaxLength != wantMax {
		t.Errorf("maxLength = %v, want %d", sort.Schema.MaxLength, wantMax)
	}
}

// TestParams_MetaConstraints_ArrayItems verifies that item-level constraints
// (Pattern, MinLength, MaxLength) from a `+ Meta` block under an array[string]
// parameter are applied to `schema.items`, not to the array wrapper.
//
// Authoring shape:
//
//   - tag: `sport:football` (array[string], optional) - Taxonomy filter.
//   - Meta
//   - Pattern: `^[a-z]+:[a-z0-9-]+$`
//   - MaxLength: 64
func TestParams_MetaConstraints_ArrayItems(t *testing.T) {
	const tagDesc = "Taxonomy filter.\\n\\n+ Meta\\n    + Pattern: `^[a-z]+:[a-z0-9-]+$`\\n    + MaxLength: 64"
	refract := []byte(`{"element":"parseResult","content":[{"element":"category","content":[{` +
		`"element":"resource","attributes":{` +
		`"href":{"element":"string","content":"/items{?tag}"},` +
		`"hrefVariables":{"element":"hrefVariables","content":[` +
		`{"element":"member",` +
		`"meta":{"description":{"element":"string","content":"` + tagDesc + `"},` +
		`"title":{"element":"string","content":"array[string]"}},` +
		`"attributes":{"typeAttributes":{"element":"array","content":[{"element":"string","content":"optional"}]}},` +
		`"content":{"key":{"element":"string","content":"tag"},"value":{"element":"string","content":"sport:football"}}}` +
		`]}},` +
		`"content":[{"element":"transition","meta":{"title":{"element":"string","content":"List"}},` +
		`"content":[{"element":"httpTransaction","content":[` +
		`{"element":"httpRequest","attributes":{"method":{"element":"string","content":"GET"}},"content":[]},` +
		`{"element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"200"}},"content":[]}` +
		`]}]}]}]}]}`)

	doc, err := RefractToOAS(refract)
	if err != nil {
		t.Fatalf("RefractToOAS: %v", err)
	}
	pi := doc.Paths["/items"]
	if pi == nil || pi.Get == nil {
		t.Fatal("expected GET on /items")
	}
	var tag *oas.Parameter
	for _, p := range pi.Get.Parameters {
		if p.Name == "tag" {
			tag = p
			break
		}
	}
	if tag == nil {
		t.Fatal("tag parameter missing")
	}
	if tag.Schema == nil || tag.Schema.Type != "array" {
		t.Fatalf("tag schema: want type=array; got %+v", tag.Schema)
	}

	// The Meta block must be stripped from the description.
	if strings.Contains(tag.Description, "Meta") {
		t.Errorf("description still contains Meta block: %q", tag.Description)
	}

	// Pattern and MaxLength must land on the ITEMS schema, not the array.
	if tag.Schema.Pattern != "" {
		t.Errorf("array wrapper should have no pattern; got %q", tag.Schema.Pattern)
	}
	if tag.Schema.MaxLength != nil {
		t.Errorf("array wrapper should have no maxLength; got %d", *tag.Schema.MaxLength)
	}
	items := tag.Schema.Items
	if items == nil {
		t.Fatal("tag.schema.items is nil")
	}
	wantPattern := "^[a-z]+:[a-z0-9-]+$"
	if items.Pattern != wantPattern {
		t.Errorf("items.pattern = %q, want %q", items.Pattern, wantPattern)
	}
	wantMax := 64
	if items.MaxLength == nil || *items.MaxLength != wantMax {
		t.Errorf("items.maxLength = %v, want %d", items.MaxLength, wantMax)
	}
}

package convert

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------
// #1 - `nullable: true` on 3.1+ becomes `type: ["string","null"]`.
// ---------------------------------------------------------------

func nullableMemberFixture() []byte {
	return []byte(`{
	  "element":"parseResult",
	  "content":[
	    {"element":"category","meta":{"title":{"element":"string","content":"API"}},
	     "content":[
	       {"element":"resource","attributes":{"href":{"element":"string","content":"/x"}},
	        "content":[{"element":"transition","meta":{"title":{"element":"string","content":"Get"}},
	          "content":[{"element":"httpTransaction","content":[
	            {"element":"httpRequest","attributes":{"method":{"element":"string","content":"GET"}},"content":[]},
	            {"element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"200"},
	              "headers":{"element":"httpHeaders","content":[
	                {"element":"member","content":{"key":{"element":"string","content":"Content-Type"},
	                  "value":{"element":"string","content":"application/json"}}}]}},
	              "content":[
	                {"element":"dataStructure","content":{
	                  "element":"object","content":[
	                    {"element":"member","content":{
	                      "key":{"element":"string","content":"middle_name"},
	                      "value":{"element":"string","content":""}},
	                      "attributes":{"typeAttributes":{"element":"array","content":[
	                        {"element":"string","content":"nullable"}]}}}
	                  ]}}
	              ]}
	          ]}]
	        }]
	       }
	     ]}
	  ]}`)
}

func TestNullable_OAS30_KeepsNullableField(t *testing.T) {
	doc, err := RefractToOASWithOptions(nullableMemberFixture(), Options{OASVersion: "3.0"})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	s := doc.Paths["/x"].Get.Responses["200"].Content["application/json"].Schema.Properties["middle_name"]
	if s == nil {
		t.Fatal("middle_name schema missing")
	}
	if !s.Nullable {
		t.Errorf("OAS 3.0: middle_name should keep Nullable=true; got %+v", s)
	}
	if len(s.TypeList) != 0 {
		t.Errorf("OAS 3.0: TypeList should be empty; got %+v", s.TypeList)
	}
	if s.Type != "string" {
		t.Errorf("OAS 3.0: Type should be string; got %q", s.Type)
	}
}

func TestNullable_OAS31_RewritesToTypeArray(t *testing.T) {
	doc, err := RefractToOASWithOptions(nullableMemberFixture(), Options{OASVersion: "3.1"})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	s := doc.Paths["/x"].Get.Responses["200"].Content["application/json"].Schema.Properties["middle_name"]
	if s == nil {
		t.Fatal("middle_name schema missing")
	}
	if s.Nullable {
		t.Errorf("OAS 3.1: Nullable must be cleared; got %+v", s)
	}
	if len(s.TypeList) != 2 || s.TypeList[0] != "string" || s.TypeList[1] != "null" {
		t.Errorf("OAS 3.1: TypeList should be [string,null]; got %+v", s.TypeList)
	}
	// Marshalled output should now carry an array `type` field.
	body, err := Marshal(doc, "json")
	if err != nil {
		t.Fatal(err)
	}
	js := compactWS(string(body))
	if !strings.Contains(js, `"type":["string","null"]`) {
		t.Errorf("marshalled JSON should contain array type; got:\n%s", body)
	}
	if strings.Contains(js, `"nullable":true`) {
		t.Errorf("marshalled JSON should NOT contain nullable:true; got:\n%s", body)
	}
}

func TestNullable_OAS32_RewritesToTypeArray(t *testing.T) {
	doc, err := RefractToOASWithOptions(nullableMemberFixture(), Options{OASVersion: "3.2"})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	s := doc.Paths["/x"].Get.Responses["200"].Content["application/json"].Schema.Properties["middle_name"]
	if s == nil {
		t.Fatal("middle_name schema missing")
	}
	if len(s.TypeList) != 2 || s.TypeList[0] != "string" || s.TypeList[1] != "null" {
		t.Errorf("OAS 3.2: TypeList should be [string,null]; got %+v", s.TypeList)
	}
}

// compactWS strips ASCII whitespace so substring checks ignore the
// pretty-print indentation that Marshal applies.
func compactWS(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case ' ', '\t', '\n', '\r':
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ---------------------------------------------------------------
// #2 - info.summary + info.license from document metadata.
// ---------------------------------------------------------------

func infoMetadataFixture(extra string) []byte {
	return []byte(`{
	  "element":"parseResult",
	  "content":[
	    {"element":"category","meta":{"title":{"element":"string","content":"My API"}},
	     "attributes":{"metadata":{"element":"array","content":[
	       ` + extra + `
	     ]}},
	     "content":[
	       {"element":"resource","attributes":{"href":{"element":"string","content":"/x"}},
	        "content":[{"element":"transition","meta":{"title":{"element":"string","content":"Get"}},
	          "content":[{"element":"httpTransaction","content":[
	            {"element":"httpRequest","attributes":{"method":{"element":"string","content":"GET"}},"content":[]},
	            {"element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"200"}},"content":[]}
	          ]}]
	        }]
	       }
	     ]}
	  ]}`)
}

func metaPair(k, v string) string {
	return `{"element":"member","content":{"key":{"element":"string","content":"` + k +
		`"},"value":{"element":"string","content":"` + v + `"}}}`
}

func TestInfo_SummaryAndLicense(t *testing.T) {
	src := infoMetadataFixture(strings.Join([]string{
		metaPair("SUMMARY", "A delightfully tiny demo API."),
		metaPair("LICENSE", "Apache 2.0 License"),
		metaPair("LICENSE-ID", "Apache-2.0"),
		metaPair("LICENSE-URL", "https://www.apache.org/licenses/LICENSE-2.0"),
	}, ","))
	doc, err := RefractToOAS(src)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if doc.Info.Summary != "A delightfully tiny demo API." {
		t.Errorf("info.summary = %q", doc.Info.Summary)
	}
	if doc.Info.License == nil {
		t.Fatal("info.license missing")
	}
	if doc.Info.License.Name != "Apache 2.0 License" {
		t.Errorf("license.name = %q", doc.Info.License.Name)
	}
	if doc.Info.License.Identifier != "Apache-2.0" {
		t.Errorf("license.identifier = %q", doc.Info.License.Identifier)
	}
	if doc.Info.License.URL != "https://www.apache.org/licenses/LICENSE-2.0" {
		t.Errorf("license.url = %q", doc.Info.License.URL)
	}
}

func TestInfo_LicenseIDOnlySynthesizesName(t *testing.T) {
	// LICENSE-ID alone (common with SPDX-only authoring).
	src := infoMetadataFixture(metaPair("LICENSE-ID", "MIT"))
	doc, err := RefractToOAS(src)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Info.License == nil || doc.Info.License.Name != "MIT" {
		t.Errorf("expected synthesised name 'MIT'; got %+v", doc.Info.License)
	}
}

func TestInfo_NoLicenseMetadata_LeavesNil(t *testing.T) {
	src := infoMetadataFixture(metaPair("VERSION", "1.0.0"))
	doc, err := RefractToOAS(src)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Info.License != nil {
		t.Errorf("license should be nil when no LICENSE metadata; got %+v", doc.Info.License)
	}
	if doc.Info.Summary != "" {
		t.Errorf("summary should be empty; got %q", doc.Info.Summary)
	}
}

// ---------------------------------------------------------------
// #3 - Response.summary on OAS 3.2 from `+ Response 200 - <title>`.
// ---------------------------------------------------------------

func titledResponseFixture() []byte {
	return []byte(`{
	  "element":"parseResult",
	  "content":[
	    {"element":"category","meta":{"title":{"element":"string","content":"API"}},
	     "content":[
	       {"element":"resource","attributes":{"href":{"element":"string","content":"/x"}},
	        "content":[{"element":"transition","meta":{"title":{"element":"string","content":"Get"}},
	          "content":[{"element":"httpTransaction","content":[
	            {"element":"httpRequest","attributes":{"method":{"element":"string","content":"GET"}},"content":[]},
	            {"element":"httpResponse",
	              "meta":{"title":{"element":"string","content":"Article was found and returned."}},
	              "attributes":{"statusCode":{"element":"string","content":"200"}},
	              "content":[]}
	          ]}]
	        }]
	       }
	     ]}
	  ]}`)
}

func TestResponseSummary_OAS30_StaysInDescription(t *testing.T) {
	doc, err := RefractToOASWithOptions(titledResponseFixture(), Options{OASVersion: "3.0"})
	if err != nil {
		t.Fatal(err)
	}
	resp := doc.Paths["/x"].Get.Responses["200"]
	if resp.Summary != "" {
		t.Errorf("OAS 3.0 should not emit response.summary; got %q", resp.Summary)
	}
	if resp.Description != "Article was found and returned." {
		t.Errorf("OAS 3.0 description should hold the title; got %q", resp.Description)
	}
}

func TestResponseSummary_OAS32_SplitsTitleIntoSummary(t *testing.T) {
	doc, err := RefractToOASWithOptions(titledResponseFixture(), Options{OASVersion: "3.2"})
	if err != nil {
		t.Fatal(err)
	}
	resp := doc.Paths["/x"].Get.Responses["200"]
	if resp.Summary != "Article was found and returned." {
		t.Errorf("OAS 3.2 summary should hold the title; got %q", resp.Summary)
	}
	if resp.Description != "OK" {
		t.Errorf("OAS 3.2 description should fall through to reason phrase; got %q", resp.Description)
	}
}

// ---------------------------------------------------------------
// #4 - tags[].kind via group-level `+ Meta + Kind: nav`.
// ---------------------------------------------------------------

func tagKindFixture() []byte {
	// A resourceGroup category with a `+ Meta` copy block that sets Kind: nav.
	return []byte(`{
	  "element":"parseResult",
	  "content":[
	    {"element":"category","meta":{"title":{"element":"string","content":"API"}},
	     "content":[
	       {"element":"category",
	        "meta":{"title":{"element":"string","content":"Articles"},
	                "classes":{"element":"array","content":[{"element":"string","content":"resourceGroup"}]}},
	        "content":[
	          {"element":"copy","content":"+ Meta\n    + Kind: nav\n"},
	          {"element":"resource","attributes":{"href":{"element":"string","content":"/articles"}},
	           "content":[{"element":"transition","meta":{"title":{"element":"string","content":"List"}},
	             "content":[{"element":"httpTransaction","content":[
	               {"element":"httpRequest","attributes":{"method":{"element":"string","content":"GET"}},"content":[]},
	               {"element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"200"}},"content":[]}
	             ]}]
	           }]
	          }
	        ]}
	     ]}
	  ]}`)
}

func TestTagKind_OAS30_NotEmitted(t *testing.T) {
	doc, err := RefractToOASWithOptions(tagKindFixture(), Options{OASVersion: "3.0"})
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Tags) == 0 || doc.Tags[0].Name != "Articles" {
		t.Fatalf("expected Articles tag; got %+v", doc.Tags)
	}
	if doc.Tags[0].Kind != "" {
		t.Errorf("OAS 3.0 must NOT emit tags[].kind; got %q", doc.Tags[0].Kind)
	}
}

func TestTagKind_OAS32_Emitted(t *testing.T) {
	doc, err := RefractToOASWithOptions(tagKindFixture(), Options{OASVersion: "3.2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Tags) == 0 || doc.Tags[0].Name != "Articles" {
		t.Fatalf("expected Articles tag; got %+v", doc.Tags)
	}
	if doc.Tags[0].Kind != "nav" {
		t.Errorf("OAS 3.2 should emit tags[].kind=nav; got %q", doc.Tags[0].Kind)
	}
}

package convert

import (
	"strings"
	"testing"
)

// W001 - unknown document-level metadata becomes info.x-* AND emits a
// warning with the stable code. W001 is reserved for keys that don't
// look like an intentional extension shape (no `-` / `_` / `.` and no
// CamelCase boundary) - kebab/snake/dotted/CamelCase keys are clearly
// intentional, so they're absorbed silently. Bare PascalCase ("Wibble",
// "Wobble") looks indistinguishable from a typo for a recognised key
// like `VERSION`, so it warns.
func TestDiagnostics_W001_UnknownMetadataKey(t *testing.T) {
	refract := []byte(`{
		"element":"parseResult","content":[{
			"element":"category","meta":{"title":{"element":"string","content":"API"}},
			"attributes":{"metadata":{"element":"array","content":[
				{"element":"member","content":{"key":{"element":"string","content":"FORMAT"},"value":{"element":"string","content":"1A"}}},
				{"element":"member","content":{"key":{"element":"string","content":"Wibble"},"value":{"element":"string","content":"a"}}},
				{"element":"member","content":{"key":{"element":"string","content":"Wobble"},"value":{"element":"string","content":"b"}}},
				{"element":"member","content":{"key":{"element":"string","content":"Retry-Policy"},"value":{"element":"string","content":"aggressive"}}}
			]}},
			"content":[]
		}]
	}`)
	diag := NewDiagnostics()
	_, err := RefractToOASWithOptions(refract, Options{Diagnostics: diag})
	if err != nil {
		t.Fatal(err)
	}
	if len(diag.Items) != 2 {
		t.Fatalf("want 2 W001 entries (Wibble + Wobble; Retry-Policy is extension-shaped), got %d: %+v", len(diag.Items), diag.Items)
	}
	for _, a := range diag.Items {
		if a.StableCode != CodeUnknownMetadataKey {
			t.Errorf("StableCode = %q, want %q", a.StableCode, CodeUnknownMetadataKey)
		}
		if a.Severity != "warning" {
			t.Errorf("Severity = %q, want warning", a.Severity)
		}
	}
}

// W002 - multiple VERSION entries → last non-empty wins + warning.
func TestDiagnostics_W002_MultipleVersion(t *testing.T) {
	refract := []byte(`{
		"element":"parseResult","content":[{
			"element":"category","meta":{"title":{"element":"string","content":"API"}},
			"attributes":{"metadata":{"element":"array","content":[
				{"element":"member","content":{"key":{"element":"string","content":"VERSION"},"value":{"element":"string","content":"1.0.0"}}},
				{"element":"member","content":{"key":{"element":"string","content":"VERSION"},"value":{"element":"string","content":"2.0.0"}}}
			]}},
			"content":[]
		}]
	}`)
	diag := NewDiagnostics()
	doc, err := RefractToOASWithOptions(refract, Options{Diagnostics: diag})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Info.Version != "2.0.0" {
		t.Errorf("Version = %q, want 2.0.0 (last wins)", doc.Info.Version)
	}
	if len(diag.Items) != 1 || diag.Items[0].StableCode != CodeMultipleVersion {
		t.Fatalf("want one W002, got %+v", diag.Items)
	}
}

// W003 - WEBHOOK_GROUPS declared but target is OAS 3.0.
func TestDiagnostics_W003_WebhookOnOAS30(t *testing.T) {
	refract := []byte(`{
		"element":"parseResult","content":[{
			"element":"category","meta":{"title":{"element":"string","content":"API"}},
			"attributes":{"metadata":{"element":"array","content":[
				{"element":"member","content":{"key":{"element":"string","content":"WEBHOOK_GROUPS"},"value":{"element":"string","content":"Notifications"}}}
			]}},
			"content":[]
		}]
	}`)
	diag := NewDiagnostics()
	if _, err := RefractToOASWithOptions(refract, Options{Diagnostics: diag}); err != nil {
		t.Fatal(err)
	}
	if len(diag.Items) != 1 || diag.Items[0].StableCode != CodeWebhookOnOAS30 {
		t.Fatalf("want one W003, got %+v", diag.Items)
	}
	if !strings.Contains(diag.Items[0].Message, "Notifications") {
		t.Errorf("message should name the offending group; got %q", diag.Items[0].Message)
	}

	// Same input on OAS 3.1 should NOT warn.
	diag2 := NewDiagnostics()
	if _, err := RefractToOASWithOptions(refract, Options{OASVersion: "3.1", Diagnostics: diag2}); err != nil {
		t.Fatal(err)
	}
	if len(diag2.Items) != 0 {
		t.Errorf("OAS 3.1 should not warn; got %+v", diag2.Items)
	}
}

// Nil-safe: passing no Diagnostics must not panic.
func TestDiagnostics_NilSafe(t *testing.T) {
	refract := []byte(`{
		"element":"parseResult","content":[{
			"element":"category","meta":{"title":{"element":"string","content":"API"}},
			"attributes":{"metadata":{"element":"array","content":[
				{"element":"member","content":{"key":{"element":"string","content":"Retry-Policy"},"value":{"element":"string","content":"x"}}}
			]}},
			"content":[]
		}]
	}`)
	if _, err := RefractToOAS(refract); err != nil {
		t.Fatal(err)
	}
}

// E001 - non-standard HTTP method skipped + diagnostic emitted.
func TestDiagnostics_E001_InvalidHTTPMethod(t *testing.T) {
	refract := []byte(`{
	  "element":"parseResult","content":[{
	    "element":"category","meta":{"title":{"element":"string","content":"API"}},
	    "content":[{
	      "element":"resource","attributes":{"href":{"element":"string","content":"/x"}},
	      "content":[{
	        "element":"transition","meta":{"title":{"element":"string","content":"Weird"}},
	        "content":[{"element":"httpTransaction","content":[
	          {"element":"httpRequest","attributes":{"method":{"element":"string","content":"FOO"}},"content":[]},
	          {"element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"200"}},"content":[]}
	        ]}]
	      }]
	    }]
	  }]
	}`)
	diag := NewDiagnostics()
	doc, err := RefractToOASWithOptions(refract, Options{Diagnostics: diag})
	if err != nil {
		t.Fatal(err)
	}
	// Operation must be skipped - PathItem may exist (created early) but
	// no method slot should be populated.
	if pi := doc.Paths["/x"]; pi != nil {
		for _, op := range pathOperations(pi) {
			if op != nil {
				t.Errorf("expected no operation on /x for method FOO, got %+v", op)
			}
		}
	}
	found := false
	for _, a := range diag.Items {
		if a.StableCode == CodeInvalidHTTPMethod {
			found = true
			if !strings.Contains(a.Message, "FOO") || !strings.Contains(a.Message, "/x") {
				t.Errorf("E001 message: %q", a.Message)
			}
		}
	}
	if !found {
		t.Fatalf("expected E001 diagnostic, got %+v", diag.Items)
	}
}

// E002 - malformed URI templates produce one diagnostic per problem.
func TestDiagnostics_E002_MalformedURITemplate(t *testing.T) {
	cases := []struct {
		href string
		want string // substring match on the produced message
	}{
		{"/x/{id", "unmatched '{'"},
		{"/x/id}", "unmatched '}'"},
		{"/x/{}", "empty '{}' segment"},
		{"/x/{?}", "operator '?' with no names"},
		{"/x/{?a,,b}", "empty name in"},
	}
	for _, tc := range cases {
		t.Run(tc.href, func(t *testing.T) {
			refract := []byte(`{
			  "element":"parseResult","content":[{
			    "element":"category","meta":{"title":{"element":"string","content":"API"}},
			    "content":[{
			      "element":"resource","attributes":{"href":{"element":"string","content":"` + tc.href + `"}},
			      "content":[{
			        "element":"transition","meta":{"title":{"element":"string","content":"Get"}},
			        "content":[{"element":"httpTransaction","content":[
			          {"element":"httpRequest","attributes":{"method":{"element":"string","content":"GET"}},"content":[]},
			          {"element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"200"}},"content":[]}
			        ]}]
			      }]
			    }]
			  }]
			}`)
			diag := NewDiagnostics()
			if _, err := RefractToOASWithOptions(refract, Options{Diagnostics: diag}); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, a := range diag.Items {
				if a.StableCode == CodeMalformedURI && strings.Contains(a.Message, tc.want) {
					found = true
				}
			}
			if !found {
				t.Errorf("expected E002 with %q for %q, got %+v", tc.want, tc.href, diag.Items)
			}
		})
	}
}

// E003 - malformed bracket prefix (no closing `]`).
func TestDiagnostics_E003_MalformedLocation(t *testing.T) {
	refract := []byte(`{
		"element":"parseResult","content":[{
			"element":"category","meta":{"title":{"element":"string","content":"API"}},
			"content":[{
				"element":"resource","meta":{"title":{"element":"string","content":"X"}},
				"attributes":{
					"href":{"element":"string","content":"/x/{id}"},
					"hrefVariables":{"element":"hrefVariables","content":[
						{"element":"member","meta":{"description":{"element":"string","content":"[header:X-Trace-Id Trace id."}},
							"content":{"key":{"element":"string","content":"traceId"},"value":{"element":"string","content":"t1"}}
						}
					]}
				},
				"content":[{
					"element":"transition","meta":{"title":{"element":"string","content":"Op"}},"content":[{
						"element":"httpTransaction","content":[
							{"element":"httpRequest","attributes":{"method":{"element":"string","content":"GET"}},"content":[]},
							{"element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"200"}},"content":[]}
						]
					}]
				}]
			}]
		}]
	}`)
	diag := NewDiagnostics()
	if _, err := RefractToOASWithOptions(refract, Options{Diagnostics: diag}); err != nil {
		t.Fatal(err)
	}
	if !diag.HasErrors() {
		t.Fatalf("expected an E003 error; got %+v", diag.Items)
	}
	var found bool
	for _, a := range diag.Items {
		if a.StableCode == CodeMalformedLocation {
			found = true
			if !strings.Contains(a.Message, "traceId") {
				t.Errorf("message should name the parameter; got %q", a.Message)
			}
		}
	}
	if !found {
		t.Errorf("no E003 entry found in %+v", diag.Items)
	}
}

// E004 - unknown location word (e.g. `[query]`).
func TestDiagnostics_E004_UnknownLocation(t *testing.T) {
	refract := []byte(`{
		"element":"parseResult","content":[{
			"element":"category","meta":{"title":{"element":"string","content":"API"}},
			"content":[{
				"element":"resource","meta":{"title":{"element":"string","content":"X"}},
				"attributes":{
					"href":{"element":"string","content":"/x/{id}"},
					"hrefVariables":{"element":"hrefVariables","content":[
						{"element":"member","meta":{"description":{"element":"string","content":"[query] not allowed"}},
							"content":{"key":{"element":"string","content":"weird"},"value":{"element":"string","content":"x"}}
						}
					]}
				},
				"content":[{
					"element":"transition","meta":{"title":{"element":"string","content":"Op"}},"content":[{
						"element":"httpTransaction","content":[
							{"element":"httpRequest","attributes":{"method":{"element":"string","content":"GET"}},"content":[]},
							{"element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"200"}},"content":[]}
						]
					}]
				}]
			}]
		}]
	}`)
	diag := NewDiagnostics()
	if _, err := RefractToOASWithOptions(refract, Options{Diagnostics: diag}); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, a := range diag.Items {
		if a.StableCode == CodeUnknownLocation {
			found = true
		}
	}
	if !found {
		t.Errorf("expected E004; got %+v", diag.Items)
	}
}

// W006 - legacy `# METHOD /path` resource shorthand. Drafter normalises
// it into a resource with no title containing one transition.
func TestDiagnostics_W006_DeprecatedShorthand(t *testing.T) {
	refract := []byte(`{
		"element":"parseResult","content":[{
			"element":"category","meta":{"title":{"element":"string","content":"API"}},
			"content":[{
				"element":"resource",
				"attributes":{"href":{"element":"string","content":"/x"}},
				"content":[{
					"element":"transition","meta":{"title":{"element":"string","content":""}},"content":[{
						"element":"httpTransaction","content":[
							{"element":"httpRequest","attributes":{"method":{"element":"string","content":"GET"}},"content":[]},
							{"element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"200"}},"content":[]}
						]
					}]
				}]
			}]
		}]
	}`)
	diag := NewDiagnostics()
	if _, err := RefractToOASWithOptions(refract, Options{Diagnostics: diag}); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, a := range diag.Items {
		if a.StableCode == CodeDeprecatedSyntax {
			found = true
			if !strings.Contains(a.Message, "GET") || !strings.Contains(a.Message, "/x") {
				t.Errorf("message should mention method + path; got %q", a.Message)
			}
		}
	}
	if !found {
		t.Errorf("expected W006; got %+v", diag.Items)
	}

	// Negative: a *named* resource (with a title) must not trigger W006.
	named := []byte(`{
		"element":"parseResult","content":[{
			"element":"category","meta":{"title":{"element":"string","content":"API"}},
			"content":[{
				"element":"resource","meta":{"title":{"element":"string","content":"Things"}},
				"attributes":{"href":{"element":"string","content":"/things"}},
				"content":[{
					"element":"transition","meta":{"title":{"element":"string","content":"List"}},"content":[{
						"element":"httpTransaction","content":[
							{"element":"httpRequest","attributes":{"method":{"element":"string","content":"GET"}},"content":[]},
							{"element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"200"}},"content":[]}
						]
					}]
				}]
			}]
		}]
	}`)
	diag2 := NewDiagnostics()
	if _, err := RefractToOASWithOptions(named, Options{Diagnostics: diag2}); err != nil {
		t.Fatal(err)
	}
	for _, a := range diag2.Items {
		if a.StableCode == CodeDeprecatedSyntax {
			t.Errorf("named resource should not emit W006; got %+v", a)
		}
	}
}

// E006 - referencing an MSON named type that isn't defined emits one
// diagnostic per missing name (deduped: same name from many sites fires
// once).
func TestDiagnostics_E006_UndefinedNamedType(t *testing.T) {
	// Two responses both reference the (undefined) type "Ghost" -
	// expect a single E006.
	refract := []byte(`{
	  "element":"parseResult","content":[{
	    "element":"category","meta":{"title":{"element":"string","content":"API"}},
	    "content":[{
	      "element":"resource","attributes":{"href":{"element":"string","content":"/a"}},
	      "content":[{
	        "element":"transition","meta":{"title":{"element":"string","content":"GetA"}},
	        "content":[{"element":"httpTransaction","content":[
	          {"element":"httpRequest","attributes":{"method":{"element":"string","content":"GET"}},"content":[]},
	          {"element":"httpResponse",
	            "attributes":{"statusCode":{"element":"string","content":"200"},
	              "headers":{"element":"object","content":[
	                {"element":"member","content":{"key":{"element":"string","content":"Content-Type"},"value":{"element":"string","content":"application/json"}}}
	              ]}},
	            "content":[{"element":"dataStructure","content":[{"element":"Ghost"}]}]}
	        ]}]
	      }]},
	    {"element":"resource","attributes":{"href":{"element":"string","content":"/b"}},
	      "content":[{
	        "element":"transition","meta":{"title":{"element":"string","content":"GetB"}},
	        "content":[{"element":"httpTransaction","content":[
	          {"element":"httpRequest","attributes":{"method":{"element":"string","content":"GET"}},"content":[]},
	          {"element":"httpResponse",
	            "attributes":{"statusCode":{"element":"string","content":"200"},
	              "headers":{"element":"object","content":[
	                {"element":"member","content":{"key":{"element":"string","content":"Content-Type"},"value":{"element":"string","content":"application/json"}}}
	              ]}},
	            "content":[{"element":"dataStructure","content":[{"element":"Ghost"}]}]}
	        ]}]
	      }]}
	  ]}]
	}`)
	diag := NewDiagnostics()
	if _, err := RefractToOASWithOptions(refract, Options{Diagnostics: diag}); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, a := range diag.Items {
		if a.StableCode == CodeUndefinedType {
			count++
			if !strings.Contains(a.Message, "Ghost") {
				t.Errorf("E006 message missing type name: %q", a.Message)
			}
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 E006 (deduped), got %d (%+v)", count, diag.Items)
	}
}

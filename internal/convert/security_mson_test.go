package convert

import (
	"strings"
	"testing"

	"github.com/alecoletti/apib-to-oas/internal/oas"
)

// withReservedSecuritySchemes wraps `body` (the inner content of the
// reserved `## SecuritySchemes (object)` named type) into a parseResult
// fixture with a single dummy operation so the converter has something
// to walk.
func withReservedSecuritySchemes(body string) []byte {
	const tmpl = `{
	  "element":"parseResult",
	  "content":[
	    {"element":"category","meta":{"title":{"element":"string","content":"API"}},
	     "content":[
	       {"element":"resource","attributes":{"href":{"element":"string","content":"/x"}},
	        "content":[{"element":"transition","meta":{"title":{"element":"string","content":"Get"}},
	          "content":[{"element":"httpTransaction","content":[
	            {"element":"httpRequest","attributes":{"method":{"element":"string","content":"GET"}},"content":[]},
	            {"element":"httpResponse","attributes":{"statusCode":{"element":"string","content":"200"}},"content":[]}
	          ]}]
	        }]
	       },
	       {"element":"category","meta":{"classes":{"element":"array","content":[{"element":"string","content":"dataStructures"}]}},
	        "content":[
	          {"element":"dataStructure","content":{
	            "element":"object",
	            "meta":{"id":{"element":"string","content":"SecuritySchemes"}},
	            "content":[%s]
	          }}
	        ]
	       }
	     ]}
	  ]}`
	return []byte(strings.Replace(tmpl, "%s", body, 1))
}

// schemeMember builds one MSON member describing a single Security
// Scheme entry. `flatPairs` is "key:value,key:value" (string-typed).
func schemeMember(name, flatPairs string) string {
	var members []string
	for _, kv := range strings.Split(flatPairs, ",") {
		kv = strings.TrimSpace(kv)
		if kv == "" {
			continue
		}
		parts := strings.SplitN(kv, ":", 2)
		k := strings.TrimSpace(parts[0])
		v := ""
		if len(parts) == 2 {
			v = strings.TrimSpace(parts[1])
		}
		members = append(members, `{"element":"member","content":{"key":{"element":"string","content":"`+k+
			`"},"value":{"element":"string","content":"`+v+`"}}}`)
	}
	return `{"element":"member","content":{"key":{"element":"string","content":"` + name +
		`"},"value":{"element":"object","content":[` + strings.Join(members, ",") + `]}}}`
}

// ---- happy-path scheme types ----------------------------------------

func TestMSONSecurity_BearerHTTP(t *testing.T) {
	body := schemeMember("BearerAuth", "type:http, scheme:bearer, bearerFormat:JWT")
	doc, err := RefractToOAS(withReservedSecuritySchemes(body))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	got := doc.Components.SecuritySchemes["BearerAuth"]
	if got == nil {
		t.Fatalf("BearerAuth not promoted; got %+v", doc.Components.SecuritySchemes)
	}
	want := &oas.SecurityScheme{Type: "http", Scheme: "bearer", BearerFormat: "JWT"}
	if got.Type != want.Type || got.Scheme != want.Scheme || got.BearerFormat != want.BearerFormat {
		t.Errorf("BearerAuth = %+v\nwant %+v", got, want)
	}
	// Reserved type must NOT leak into components.schemas.
	if _, leaked := doc.Components.Schemas["SecuritySchemes"]; leaked {
		t.Errorf("SecuritySchemes leaked into components.schemas")
	}
}

func TestMSONSecurity_ApiKey(t *testing.T) {
	body := schemeMember("ApiKeyAuth", "type:apiKey, in:header, name:X-API-Key")
	doc, err := RefractToOAS(withReservedSecuritySchemes(body))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	got := doc.Components.SecuritySchemes["ApiKeyAuth"]
	if got == nil {
		t.Fatalf("ApiKeyAuth not promoted")
	}
	if got.Type != "apiKey" || got.In != "header" || got.Name != "X-API-Key" {
		t.Errorf("ApiKeyAuth = %+v", got)
	}
}

func TestMSONSecurity_OpenIDConnect(t *testing.T) {
	body := schemeMember("OIDC", "type:openIdConnect, openIdConnectUrl:https://auth.example.com/.well-known/oidc")
	doc, err := RefractToOAS(withReservedSecuritySchemes(body))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	got := doc.Components.SecuritySchemes["OIDC"]
	if got == nil {
		t.Fatalf("OIDC not promoted")
	}
	if got.Type != "openIdConnect" || got.OpenIDConnectURL != "https://auth.example.com/.well-known/oidc" {
		t.Errorf("OIDC = %+v", got)
	}
}

func TestMSONSecurity_OAuth2_AuthorizationCode(t *testing.T) {
	// OAuth2 needs a hand-written nested object (flows + scopes), so we
	// build it without the schemeMember helper.
	body := `{"element":"member","content":{
		"key":{"element":"string","content":"OAuth2"},
		"value":{"element":"object","content":[
			{"element":"member","content":{
				"key":{"element":"string","content":"type"},
				"value":{"element":"string","content":"oauth2"}}},
			{"element":"member","content":{
				"key":{"element":"string","content":"flows"},
				"value":{"element":"object","content":[
					{"element":"member","content":{
						"key":{"element":"string","content":"authorizationCode"},
						"value":{"element":"object","content":[
							{"element":"member","content":{
								"key":{"element":"string","content":"authorizationUrl"},
								"value":{"element":"string","content":"https://auth.example.com/authorize"}}},
							{"element":"member","content":{
								"key":{"element":"string","content":"tokenUrl"},
								"value":{"element":"string","content":"https://auth.example.com/token"}}},
							{"element":"member","content":{
								"key":{"element":"string","content":"scopes"},
								"value":{"element":"object","content":[
									{"element":"member","content":{
										"key":{"element":"string","content":"read"},
										"value":{"element":"string","content":"Read access"}}},
									{"element":"member","content":{
										"key":{"element":"string","content":"write"},
										"value":{"element":"string","content":"Write access"}}}
								]}}}
						]}}}
				]}}}
		]}
	}}`
	doc, err := RefractToOAS(withReservedSecuritySchemes(body))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	got := doc.Components.SecuritySchemes["OAuth2"]
	if got == nil {
		t.Fatalf("OAuth2 not promoted; have %+v", doc.Components.SecuritySchemes)
	}
	if got.Type != "oauth2" {
		t.Errorf("type = %q", got.Type)
	}
	if got.Flows == nil || got.Flows.AuthorizationCode == nil {
		t.Fatalf("AuthorizationCode flow missing: %+v", got.Flows)
	}
	ac := got.Flows.AuthorizationCode
	if ac.AuthorizationURL != "https://auth.example.com/authorize" {
		t.Errorf("authorizationUrl = %q", ac.AuthorizationURL)
	}
	if ac.TokenURL != "https://auth.example.com/token" {
		t.Errorf("tokenUrl = %q", ac.TokenURL)
	}
	if ac.Scopes["read"] != "Read access" || ac.Scopes["write"] != "Write access" {
		t.Errorf("scopes = %+v", ac.Scopes)
	}
}

// ---- precedence: sidecar wins ----------------------------------------

func TestMSONSecurity_SidecarOverridesMSON(t *testing.T) {
	body := schemeMember("BearerAuth", "type:http, scheme:bearer, bearerFormat:JWT")
	cfg := &SecurityConfig{
		SecuritySchemes: map[string]*oas.SecurityScheme{
			"BearerAuth": {Type: "http", Scheme: "bearer", BearerFormat: "PASETO-from-sidecar"},
		},
	}
	doc, err := RefractToOASWithOptions(withReservedSecuritySchemes(body), Options{Security: cfg})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	got := doc.Components.SecuritySchemes["BearerAuth"]
	if got == nil {
		t.Fatal("BearerAuth missing")
	}
	if got.BearerFormat != "PASETO-from-sidecar" {
		t.Errorf("sidecar should win: BearerFormat = %q", got.BearerFormat)
	}
}

// ---- E007 - required field missing ----------------------------------

func TestMSONSecurity_E007_HTTPMissingScheme(t *testing.T) {
	body := schemeMember("BearerAuth", "type:http")
	diag := NewDiagnostics()
	if _, err := RefractToOASWithOptions(withReservedSecuritySchemes(body), Options{Diagnostics: diag}); err != nil {
		t.Fatal(err)
	}
	if !diag.HasErrors() {
		t.Fatalf("expected E007; got %+v", diag.Items)
	}
	for _, a := range diag.Items {
		if a.StableCode == CodeMissingSchemeField && strings.Contains(a.Message, "scheme") {
			return
		}
	}
	t.Errorf("expected E007 about missing scheme; got %+v", diag.Items)
}

func TestMSONSecurity_E007_ApiKeyMissingFields(t *testing.T) {
	body := schemeMember("Bad", "type:apiKey")
	diag := NewDiagnostics()
	if _, err := RefractToOASWithOptions(withReservedSecuritySchemes(body), Options{Diagnostics: diag}); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range diag.Items {
		if a.StableCode == CodeMissingSchemeField {
			found = true
		}
	}
	if !found {
		t.Errorf("expected E007 for apiKey without name/in; got %+v", diag.Items)
	}
}

func TestMSONSecurity_E007_MissingType(t *testing.T) {
	body := schemeMember("X", "scheme:bearer")
	diag := NewDiagnostics()
	if _, err := RefractToOASWithOptions(withReservedSecuritySchemes(body), Options{Diagnostics: diag}); err != nil {
		t.Fatal(err)
	}
	for _, a := range diag.Items {
		if a.StableCode == CodeMissingSchemeField && strings.Contains(a.Message, "type") {
			return
		}
	}
	t.Errorf("expected E007 about missing type; got %+v", diag.Items)
}

// ---- E008 - unknown type --------------------------------------------

func TestMSONSecurity_E008_UnknownType(t *testing.T) {
	body := schemeMember("Weird", "type:saml2")
	diag := NewDiagnostics()
	if _, err := RefractToOASWithOptions(withReservedSecuritySchemes(body), Options{Diagnostics: diag}); err != nil {
		t.Fatal(err)
	}
	for _, a := range diag.Items {
		if a.StableCode == CodeUnknownSchemeType && strings.Contains(a.Message, "saml2") {
			return
		}
	}
	t.Errorf("expected E008 for unknown type; got %+v", diag.Items)
}

// ---- W007 - referenced-but-undeclared scheme ------------------------

// undeclaredSchemeFixture builds a parseResult with `SECURITY: BearerAuth`
// document metadata and a single dummy operation, but NO `## SecuritySchemes`
// named type and no sidecar - the canonical case the user just hit.
func undeclaredSchemeFixture(securityValue string) []byte {
	return []byte(`{
	  "element":"parseResult",
	  "content":[
	    {"element":"category","meta":{"title":{"element":"string","content":"API"}},
	     "attributes":{"metadata":{"element":"array","content":[
	       {"element":"member","content":{
	         "key":{"element":"string","content":"SECURITY"},
	         "value":{"element":"string","content":"` + securityValue + `"}}}
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

func TestW007_DocSecurityReferencesUndeclaredScheme(t *testing.T) {
	diag := NewDiagnostics()
	doc, err := RefractToOASWithOptions(undeclaredSchemeFixture("BearerAuth"), Options{Diagnostics: diag})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	// doc.Security IS populated (the metadata was parsed) but the
	// scheme is dangling - that's the failure mode W007 catches.
	if len(doc.Security) != 1 {
		t.Fatalf("expected doc.Security populated; got %+v", doc.Security)
	}
	var got *Annotation
	for i, a := range diag.Items {
		if a.StableCode == CodeUndefinedScheme {
			got = &diag.Items[i]
			break
		}
	}
	if got == nil {
		t.Fatalf("expected W007; got %+v", diag.Items)
	}
	if !strings.Contains(got.Message, "BearerAuth") {
		t.Errorf("W007 message should name the missing scheme; got %q", got.Message)
	}
	if got.Severity != "warning" {
		t.Errorf("W007 should be a warning; got %q", got.Severity)
	}
}

func TestW007_NoWarningWhenSchemeDeclared(t *testing.T) {
	// Same SECURITY: BearerAuth, but this time the sidecar declares it.
	diag := NewDiagnostics()
	cfg := &SecurityConfig{
		SecuritySchemes: map[string]*oas.SecurityScheme{
			"BearerAuth": {Type: "http", Scheme: "bearer"},
		},
	}
	if _, err := RefractToOASWithOptions(undeclaredSchemeFixture("BearerAuth"), Options{Diagnostics: diag, Security: cfg}); err != nil {
		t.Fatal(err)
	}
	for _, a := range diag.Items {
		if a.StableCode == CodeUndefinedScheme {
			t.Errorf("unexpected W007 when scheme is declared via sidecar: %+v", a)
		}
	}
}

func TestW007_MultipleMissingReportedStably(t *testing.T) {
	diag := NewDiagnostics()
	if _, err := RefractToOASWithOptions(undeclaredSchemeFixture("Zeta, Alpha"), Options{Diagnostics: diag}); err != nil {
		t.Fatal(err)
	}
	var msgs []string
	for _, a := range diag.Items {
		if a.StableCode == CodeUndefinedScheme {
			msgs = append(msgs, a.Message)
		}
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 W007 entries; got %d (%+v)", len(msgs), msgs)
	}
	// Sorted alphabetically: Alpha first.
	if !strings.Contains(msgs[0], "Alpha") || !strings.Contains(msgs[1], "Zeta") {
		t.Errorf("expected stable alphabetical order; got %+v", msgs)
	}
}

// Package convert Blueprint+ Tier-A in-source security schemes.
//
// Stock Drafter rejects `## Foo (SecurityScheme)` because `SecurityScheme`
// is not a registered base type, and we deliberately don't fork Drafter.
// The Tier-A path described in specs/apib+.md "## SecuritySchemes (object)"
// instead reserves a
// single named MSON object with a fixed name:
//
//	# Data Structures
//
//	## SecuritySchemes (object)
//	+ BearerAuth (object)
//	    + type: http
//	    + scheme: bearer
//	    + bearerFormat: JWT
//	+ ApiKeyAuth (object)
//	    + type: apiKey
//	    + in: header
//	    + name: X-API-Key
//	+ OAuth2 (object)
//	    + type: oauth2
//	    + flows (object)
//	        + authorizationCode (object)
//	            + tokenUrl: https://auth.example.com/token
//	            + scopes (object)
//	                + read:  Read access
//	                + write: Write access
//
// Each top-level member is one OAS Security Scheme keyed by its MSON
// member name. The reserved type name "SecuritySchemes" is matched
// case-insensitively. After promotion, the type is removed from
// components.schemas so it doesn't double-appear.
//
// Diagnostics:
//   - E007: required field missing for the declared `type` (e.g. an
//     `apiKey` scheme without `name` or `in`).
//   - E008: unknown / unsupported `type` value.
package convert

import (
	"strings"

	"github.com/alecoletti/apib-to-oas/internal/oas"
)

// reservedSecuritySchemesTypeName is the MSON named-type whose top-level
// members the converter promotes into doc.Components.SecuritySchemes.
// Matched case-insensitively.
const reservedSecuritySchemesTypeName = "SecuritySchemes"

// isReservedSecuritySchemesName returns true when name (case-insensitive)
// matches the reserved MSON type that holds Blueprint+ Tier-A security
// schemes.
func isReservedSecuritySchemesName(name string) bool {
	return strings.EqualFold(name, reservedSecuritySchemesTypeName)
}

// extractMSONSecuritySchemes scans an MSON named-type registry for an
// entry named "SecuritySchemes" (case-insensitive) and converts each of
// its top-level members into an *oas.SecurityScheme.
//
// Returns the resulting map (nil if no such named type exists) plus the
// canonical lookup-name used in the registry so the caller can suppress
// it from components.schemas.
func extractMSONSecuritySchemes(reg map[string]*element, diag *Diagnostics) (map[string]*oas.SecurityScheme, string) {
	var (
		container *element
		hitName   string
	)
	for name, def := range reg {
		if isReservedSecuritySchemesName(name) {
			container = def
			hitName = name
			break
		}
	}
	if container == nil || container.Element != "object" {
		return nil, ""
	}
	out := map[string]*oas.SecurityScheme{}
	for _, c := range container.contentArray() {
		if c.Element != "member" {
			continue
		}
		m := decodeMember(&c)
		if m == nil {
			continue
		}
		schemeName := m.Content.Key.Content
		if schemeName == "" {
			continue
		}
		if scheme := decodeSecurityScheme(schemeName, &m.Content.Value, diag); scheme != nil {
			out[schemeName] = scheme
		}
	}
	if len(out) == 0 {
		return nil, hitName
	}
	return out, hitName
}

// decodeSecurityScheme materialises one OAS Security Scheme from an MSON
// `(object)` value. The caller has already extracted the member key as
// the scheme name.
//
// Recognised members (per OAS 3 Security Scheme Object):
//
//	type, description, name, in, scheme, bearerFormat,
//	openIdConnectUrl, flows
//
// `flows` is itself an object whose members are flow names; each flow's
// `scopes` member is a flat key→description map.
//
// Returns nil and emits a diagnostic when `type` is missing/unknown or
// when the type-specific required fields are not satisfied.
func decodeSecurityScheme(name string, val *element, diag *Diagnostics) *oas.SecurityScheme {
	if val == nil || val.Element != "object" {
		diag.Error(CodeMissingSchemeField,
			"security scheme '"+name+"' must be an MSON `(object)`")
		return nil
	}
	flat := decodeStringMembers(val)
	t := strings.TrimSpace(flat["type"])
	if t == "" {
		diag.Error(CodeMissingSchemeField,
			"security scheme '"+name+"' is missing required `type` member")
		return nil
	}
	scheme := &oas.SecurityScheme{
		Type:        t,
		Description: flat["description"],
	}
	switch strings.ToLower(t) {
	case "http":
		scheme.Scheme = flat["scheme"]
		scheme.BearerFormat = flat["bearerFormat"]
		if scheme.Scheme == "" {
			diag.Error(CodeMissingSchemeField,
				"security scheme '"+name+"' (type: http) requires `scheme` member")
			return nil
		}
	case "apikey":
		scheme.Name = flat["name"]
		scheme.In = flat["in"]
		// Re-stamp the canonical OAS spelling so JSON output matches
		// the spec exactly (authors might write "apikey" or "APIKEY").
		scheme.Type = "apiKey"
		if scheme.Name == "" || scheme.In == "" {
			diag.Error(CodeMissingSchemeField,
				"security scheme '"+name+"' (type: apiKey) requires `name` and `in` members")
			return nil
		}
		switch scheme.In {
		case "header", "query", "cookie":
		default:
			diag.Error(CodeMissingSchemeField,
				"security scheme '"+name+"' (type: apiKey) `in` must be header, query, or cookie")
			return nil
		}
	case "oauth2":
		scheme.Type = "oauth2"
		flows := decodeOAuthFlows(name, val, diag)
		if flows == nil {
			return nil
		}
		scheme.Flows = flows
	case "openidconnect":
		scheme.Type = "openIdConnect"
		scheme.OpenIDConnectURL = flat["openIdConnectUrl"]
		if scheme.OpenIDConnectURL == "" {
			diag.Error(CodeMissingSchemeField,
				"security scheme '"+name+"' (type: openIdConnect) requires `openIdConnectUrl` member")
			return nil
		}
	case "mutualtls":
		scheme.Type = "mutualTLS"
	default:
		diag.Error(CodeUnknownSchemeType,
			"security scheme '"+name+"' has unknown type '"+t+"' (expected one of http, apiKey, oauth2, openIdConnect, mutualTLS)")
		return nil
	}
	return scheme
}

// decodeOAuthFlows pulls the `flows (object)` member out of the scheme
// body, then decodes each named flow into an *oas.OAuthFlow.
func decodeOAuthFlows(schemeName string, schemeVal *element, diag *Diagnostics) *oas.OAuthFlows {
	flowsVal := findObjectMember(schemeVal, "flows")
	if flowsVal == nil {
		diag.Error(CodeMissingSchemeField,
			"security scheme '"+schemeName+"' (type: oauth2) requires `flows` member")
		return nil
	}
	out := &oas.OAuthFlows{}
	for _, c := range flowsVal.contentArray() {
		if c.Element != "member" {
			continue
		}
		fm := decodeMember(&c)
		if fm == nil {
			continue
		}
		flowName := fm.Content.Key.Content
		flow := decodeOAuthFlow(schemeName, flowName, &fm.Content.Value, diag)
		if flow == nil {
			continue
		}
		switch strings.ToLower(flowName) {
		case "implicit":
			out.Implicit = flow
		case "password":
			out.Password = flow
		case "clientcredentials":
			out.ClientCredentials = flow
		case "authorizationcode":
			out.AuthorizationCode = flow
		default:
			diag.Error(CodeUnknownSchemeType,
				"security scheme '"+schemeName+"' has unknown flow '"+flowName+"' (expected implicit, password, clientCredentials, authorizationCode)")
		}
	}
	if out.Implicit == nil && out.Password == nil && out.ClientCredentials == nil && out.AuthorizationCode == nil {
		diag.Error(CodeMissingSchemeField,
			"security scheme '"+schemeName+"' (type: oauth2) `flows` declares no recognised flow")
		return nil
	}
	return out
}

// decodeOAuthFlow decodes a single flow object. tokenUrl is required for
// every flow except `implicit`; authorizationUrl is required for
// `implicit` and `authorizationCode` per the OAS spec.
func decodeOAuthFlow(schemeName, flowName string, val *element, diag *Diagnostics) *oas.OAuthFlow {
	if val == nil || val.Element != "object" {
		diag.Error(CodeMissingSchemeField,
			"security scheme '"+schemeName+"' flow '"+flowName+"' must be an MSON `(object)`")
		return nil
	}
	flat := decodeStringMembers(val)
	flow := &oas.OAuthFlow{
		AuthorizationURL: flat["authorizationUrl"],
		TokenURL:         flat["tokenUrl"],
		RefreshURL:       flat["refreshUrl"],
	}
	scopesVal := findObjectMember(val, "scopes")
	if scopesVal != nil {
		flow.Scopes = decodeStringMembers(scopesVal)
	}
	// Per-flow required-field check.
	switch strings.ToLower(flowName) {
	case "implicit":
		if flow.AuthorizationURL == "" {
			diag.Error(CodeMissingSchemeField,
				"security scheme '"+schemeName+"' implicit flow requires `authorizationUrl`")
			return nil
		}
	case "authorizationcode":
		if flow.AuthorizationURL == "" || flow.TokenURL == "" {
			diag.Error(CodeMissingSchemeField,
				"security scheme '"+schemeName+"' authorizationCode flow requires `authorizationUrl` and `tokenUrl`")
			return nil
		}
	case "password", "clientcredentials":
		if flow.TokenURL == "" {
			diag.Error(CodeMissingSchemeField,
				"security scheme '"+schemeName+"' "+flowName+" flow requires `tokenUrl`")
			return nil
		}
	}
	return flow
}

// decodeStringMembers walks an `(object)` element and returns its
// scalar members as a flat map. Nested objects are skipped (callers
// pull those out by name via findObjectMember).
func decodeStringMembers(obj *element) map[string]string {
	out := map[string]string{}
	if obj == nil {
		return out
	}
	for _, c := range obj.contentArray() {
		if c.Element != "member" {
			continue
		}
		m := decodeMember(&c)
		if m == nil {
			continue
		}
		key := m.Content.Key.Content
		if key == "" {
			continue
		}
		// Take a string from either the `value`'s content (typed
		// scalar) or the member-level description (used by Drafter
		// for `+ key: long-prose-value` shapes - see the scopes case).
		if m.Content.Value.Element == "string" || m.Content.Value.Element == "number" || m.Content.Value.Element == "boolean" {
			if s := m.Content.Value.contentString(); s != "" {
				out[key] = s
				continue
			}
		}
		if d := m.Meta.Description.Content; d != "" {
			out[key] = d
		}
	}
	return out
}

// findObjectMember returns the value of the named member in obj when it
// is itself an `(object)` element (i.e. a nested struct). Returns nil
// when the member is absent or scalar-typed.
func findObjectMember(obj *element, name string) *element {
	if obj == nil {
		return nil
	}
	for _, c := range obj.contentArray() {
		if c.Element != "member" {
			continue
		}
		m := decodeMember(&c)
		if m == nil {
			continue
		}
		if m.Content.Key.Content != name {
			continue
		}
		v := m.Content.Value
		if v.Element != "object" {
			return nil
		}
		// decodeMember returned a copy - return a heap pointer to it.
		out := v
		return &out
	}
	return nil
}

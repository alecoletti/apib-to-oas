// Package oas defines a small OpenAPI 3.0 / 3.1 / 3.2 model used as
// the conversion target. Only the fields the converter actually emits
// are modelled; grow it on demand rather than vendoring a full schema.
//
// Struct field declaration order is the order used when the document
// is serialised. JSON output comes from encoding/json (which respects
// struct order); YAML is produced by streaming that JSON through
// convert/marshal.go.
package oas

import (
	"bytes"
	"encoding/json"
)

// Document is an OpenAPI 3.0 / 3.1 / 3.2 document.
//
// Field order: openapi, info, jsonSchemaDialect, servers, security,
// tags, paths, webhooks, components. Putting security and tags near
// the top keeps the document's shape visible without scrolling past
// every operation. The OpenAPI spec doesn't mandate field order.
type Document struct {
	OpenAPI           string                `json:"openapi"`
	Info              Info                  `json:"info"`
	JSONSchemaDialect string                `json:"jsonSchemaDialect,omitempty"`
	Servers           []Server              `json:"servers,omitempty"`
	Security          []SecurityRequirement `json:"security,omitempty"`
	Tags              []Tag                 `json:"tags,omitempty"`
	Paths             map[string]*PathItem  `json:"paths"`
	Webhooks          map[string]*PathItem  `json:"webhooks,omitempty"`
	Components        *Components           `json:"components,omitempty"`

	// Extensions holds root-level `x-*` keys (e.g. authored via the
	// `ROOT.<Foo>` document-metadata convention - see specs/apib+.md §13.3).
	Extensions map[string]any `json:"-"`
}

// MarshalJSON splays root-level `x-*` extensions onto the document.
func (d *Document) MarshalJSON() ([]byte, error) {
	type alias Document
	return marshalWithExtensions((*alias)(d), d.Extensions)
}

// Info is the OAS Info Object.
type Info struct {
	Title       string   `json:"title"`
	Summary     string   `json:"summary,omitempty"` // OAS 3.1+
	Version     string   `json:"version"`
	Description string   `json:"description,omitempty"`
	License     *License `json:"license,omitempty"`

	// Extensions holds `x-*` keys derived from unknown document-level
	// metadata (specs/apib+.md "Document metadata / Unknown keys").
	Extensions map[string]any `json:"-"`
}

// MarshalJSON splays Info `x-*` extensions onto the info object.
func (i Info) MarshalJSON() ([]byte, error) {
	type alias Info
	return marshalWithExtensions(alias(i), i.Extensions)
}

// Server is an OAS Server Object.
type Server struct {
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
	Name        string `json:"name,omitempty"` // OAS 3.2+
}

// License is the OAS License Object. Identifier (SPDX) was added in
// OAS 3.1; the converter only emits it on 3.1+ via a post-walk that
// promotes Name to Identifier when Name matches a SPDX shape.
type License struct {
	Name       string `json:"name"`
	Identifier string `json:"identifier,omitempty"` // 3.1+
	URL        string `json:"url,omitempty"`
}

// PathItem holds the operations available on a single path. Methods are
// modelled as explicit fields so they emit in canonical OAS order.
type PathItem struct {
	Summary     string       `json:"summary,omitempty"`
	Description string       `json:"description,omitempty"`
	Get         *Operation   `json:"get,omitempty"`
	Put         *Operation   `json:"put,omitempty"`
	Post        *Operation   `json:"post,omitempty"`
	Delete      *Operation   `json:"delete,omitempty"`
	Options     *Operation   `json:"options,omitempty"`
	Head        *Operation   `json:"head,omitempty"`
	Patch       *Operation   `json:"patch,omitempty"`
	Parameters  []*Parameter `json:"parameters,omitempty"`

	// Extensions holds `x-*` keys declared via a Blueprint+ `+ Meta`
	// block placed under a `## Resource [...]` header.
	Extensions map[string]any `json:"-"`
}

// MarshalJSON splays PathItem `x-*` extensions onto the path object.
func (p *PathItem) MarshalJSON() ([]byte, error) {
	type alias PathItem
	return marshalWithExtensions((*alias)(p), p.Extensions)
}

// SetOperation assigns op to the slot for the given HTTP method (any case).
// Returns false if the method is not recognised.
func (p *PathItem) SetOperation(method string, op *Operation) bool {
	switch upper(method) {
	case "GET":
		p.Get = op
	case "PUT":
		p.Put = op
	case "POST":
		p.Post = op
	case "DELETE":
		p.Delete = op
	case "OPTIONS":
		p.Options = op
	case "HEAD":
		p.Head = op
	case "PATCH":
		p.Patch = op
	default:
		return false
	}
	return true
}

// Operation is an OAS Operation Object.
type Operation struct {
	Tags         []string              `json:"tags,omitempty"`
	Summary      string                `json:"summary,omitempty"`
	Description  string                `json:"description,omitempty"`
	OperationID  string                `json:"operationId,omitempty"`
	Parameters   []*Parameter          `json:"parameters,omitempty"`
	RequestBody  *RequestBody          `json:"requestBody,omitempty"`
	Responses    map[string]*Response  `json:"responses,omitempty"`
	Security     []SecurityRequirement `json:"security,omitempty"`
	Deprecated   bool                  `json:"deprecated,omitempty"`
	ExternalDocs *ExternalDocs         `json:"externalDocs,omitempty"`

	// Extensions holds OAS specification extensions (`x-*` keys) declared
	// via Blueprint+ `+ Meta` blocks or unknown metadata. Marshalled by
	// MarshalJSON so they appear at the end of the operation object,
	// preserving the canonical struct field order.
	Extensions map[string]any `json:"-"`

	// SecurityCleared records "the author explicitly cleared security
	// on this operation" (Blueprint+ §5.3 / §9.3 - empty `Security:`
	// inside `+ Meta`). When true and Security is empty/nil, the
	// custom MarshalJSON emits `security: []` so the operation does
	// NOT inherit the document-level default. Without this sentinel
	// `omitempty` on the slice would erase the intent.
	SecurityCleared bool `json:"-"`
}

// MarshalJSON splays Extensions ("x-*") into the operation JSON object
// while preserving the declared struct field order. Also injects an
// explicit `"security":[]` when SecurityCleared is set so empty-security
// requirements survive `omitempty`.
func (op *Operation) MarshalJSON() ([]byte, error) {
	type alias Operation
	raw, err := marshalWithExtensions((*alias)(op), op.Extensions)
	if err != nil {
		return nil, err
	}
	if !op.SecurityCleared || len(op.Security) > 0 {
		return raw, nil
	}
	return spliceClearedSecurity(raw)
}

// spliceClearedSecurity inserts `"security":[]` before the trailing `}`
// of an operation's JSON body. Adds a leading comma when the body is
// non-empty.
func spliceClearedSecurity(raw []byte) ([]byte, error) {
	if len(raw) < 2 || raw[len(raw)-1] != '}' {
		return raw, nil
	}
	var buf bytes.Buffer
	buf.Write(raw[:len(raw)-1])
	if len(raw) > 2 {
		buf.WriteByte(',')
	}
	buf.WriteString(`"security":[]`)
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// ExternalDocs is the OAS External Documentation Object.
type ExternalDocs struct {
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
}

// Tag is an OAS Tag Object (root-level tags array).
//
// Parent (OAS 3.2+) names another tag that this one is grouped under,
// allowing a hierarchical navigation in tooling that supports it. The
// converter only emits Parent/Summary when --oas-version 3.2 is selected.
type Tag struct {
	Name        string `json:"name"`
	Summary     string `json:"summary,omitempty"` // OAS 3.2+
	Description string `json:"description,omitempty"`
	Parent      string `json:"parent,omitempty"`
	Kind        string `json:"kind,omitempty"` // OAS 3.2 - nav | badge | audience | …

	// Extensions holds `x-*` keys for the tag, e.g. via a `+ Meta`
	// block placed under a `# Group <Name>` header.
	Extensions map[string]any `json:"-"`
}

// MarshalJSON splays Tag `x-*` extensions onto the tag object.
func (t Tag) MarshalJSON() ([]byte, error) {
	type alias Tag
	return marshalWithExtensions(alias(t), t.Extensions)
}

// Parameter is an OAS Parameter Object.
//
// AnchorID and AliasID are used by the YAML marshaller to emit shared
// parameters as YAML anchors (`&ref_N`) and aliases (`*ref_N`) rather
// than duplicating the body in every operation. They are populated by
// `convert.assignAnchors` immediately before marshalling and are not
// part of the normal JSON serialisation - instead a custom MarshalJSON
// emits sentinel keys `$$anchor` / `$$alias` that the YAML emitter
// translates into the actual anchor / alias syntax.
type Parameter struct {
	AnchorID    string  `json:"-"`
	AliasID     string  `json:"-"`
	Name        string  `json:"name"`
	In          string  `json:"in"`
	Description string  `json:"description,omitempty"`
	Required    bool    `json:"required,omitempty"`
	Deprecated  bool    `json:"deprecated,omitempty"`
	Schema      *Schema `json:"schema,omitempty"`
}

// MarshalJSON injects $$anchor / $$alias sentinel keys when the parameter
// participates in YAML anchor sharing. For aliases, only the sentinel is
// emitted (the body lives at the anchor site). For anchors, the sentinel
// is emitted as the FIRST key of the object so the YAML emitter sees it
// before any real content.
func (p *Parameter) MarshalJSON() ([]byte, error) {
	if p.AliasID != "" {
		return jsonObject([]kv{{"$$alias", p.AliasID}})
	}
	type pAlias Parameter
	body, err := jsonMarshalNoMethod((*pAlias)(p))
	if err != nil {
		return nil, err
	}
	if p.AnchorID == "" {
		return body, nil
	}
	// Splice {"$$anchor":"<id>", ...rest} into the existing JSON object.
	return spliceFirstKey(body, "$$anchor", p.AnchorID)
}

// RequestBody is an OAS Request Body Object.
type RequestBody struct {
	Description string                `json:"description,omitempty"`
	Required    bool                  `json:"required,omitempty"`
	Content     map[string]*MediaType `json:"content,omitempty"`
}

// Response is an OAS Response Object. Summary was added in OAS 3.2;
// the converter only emits it when --oas-version is 3.2 or later.
type Response struct {
	Summary     string                `json:"summary,omitempty"` // OAS 3.2+
	Description string                `json:"description"`
	Headers     map[string]*Header    `json:"headers,omitempty"`
	Content     map[string]*MediaType `json:"content,omitempty"`
}

// Header is an OAS Header Object (subset).
type Header struct {
	Description string  `json:"description,omitempty"`
	Required    bool    `json:"required,omitempty"`
	Deprecated  bool    `json:"deprecated,omitempty"`
	Schema      *Schema `json:"schema,omitempty"`
}

// MediaType is an OAS Media Type Object.
type MediaType struct {
	Schema   *Schema             `json:"schema,omitempty"`
	Example  any                 `json:"example,omitempty"`
	Examples map[string]*Example `json:"examples,omitempty"`
}

// Example is an OAS Example Object. Either Value (inlined) or
// ExternalValue (URL) is set.
type Example struct {
	Summary       string `json:"summary,omitempty"`
	Description   string `json:"description,omitempty"`
	Value         any    `json:"value,omitempty"`
	ExternalValue string `json:"externalValue,omitempty"`
}

// Schema is a reduced but reasonably complete OAS 3.x / JSON Schema
// object.
//
// Field order matches the order most OpenAPI tools emit so diffs
// against existing specs stay quiet.
type Schema struct {
	Ref         string `json:"$ref,omitempty"`
	Title       string `json:"title,omitempty"`
	Type        string `json:"type,omitempty"`
	Format      string `json:"format,omitempty"`
	Description string `json:"description,omitempty"`

	// Composition (any one of these picks a polymorphism strategy).
	OneOf         []*Schema      `json:"oneOf,omitempty"`
	AnyOf         []*Schema      `json:"anyOf,omitempty"`
	AllOf         []*Schema      `json:"allOf,omitempty"`
	Not           *Schema        `json:"not,omitempty"`
	Discriminator *Discriminator `json:"discriminator,omitempty"`

	// Conditional applicators (JSON Schema 2020-12 / OAS 3.1+).
	// Applied via `+ Schema Patch` blocks in Blueprint+ source.
	If   *Schema `json:"if,omitempty"`
	Then *Schema `json:"then,omitempty"`
	Else *Schema `json:"else,omitempty"`

	// Object-shaped fields.
	Items                *Schema            `json:"items,omitempty"`
	Properties           map[string]*Schema `json:"properties,omitempty"`
	Required             []string           `json:"required,omitempty"`
	AdditionalProperties any                `json:"additionalProperties,omitempty"`
	MinProperties        *int               `json:"minProperties,omitempty"`
	MaxProperties        *int               `json:"maxProperties,omitempty"`

	// String / numeric constraints.
	Pattern          string   `json:"pattern,omitempty"`
	MinLength        *int     `json:"minLength,omitempty"`
	MaxLength        *int     `json:"maxLength,omitempty"`
	Minimum          *float64 `json:"minimum,omitempty"`
	Maximum          *float64 `json:"maximum,omitempty"`
	ExclusiveMinimum *float64 `json:"exclusiveMinimum,omitempty"`
	ExclusiveMaximum *float64 `json:"exclusiveMaximum,omitempty"`
	MultipleOf       *float64 `json:"multipleOf,omitempty"`
	MinItems         *int     `json:"minItems,omitempty"`
	MaxItems         *int     `json:"maxItems,omitempty"`
	UniqueItems      bool     `json:"uniqueItems,omitempty"`

	// Documentation / lifecycle.
	Default    any   `json:"default,omitempty"`
	Const      any   `json:"const,omitempty"` // JSON Schema 2020-12 / OAS 3.1+
	Enum       []any `json:"enum,omitempty"`
	Example    any   `json:"example,omitempty"`  // deprecated in OAS 3.1+; use Examples
	Examples   []any `json:"examples,omitempty"` // JSON Schema 2020-12 / OAS 3.1+
	ReadOnly   bool  `json:"readOnly,omitempty"`
	WriteOnly  bool  `json:"writeOnly,omitempty"`
	Deprecated bool  `json:"deprecated,omitempty"`
	Nullable   bool  `json:"nullable,omitempty"` // OAS 3.0 only; 3.1 uses type arrays

	// TypeList, when non-empty, replaces the singular `type:` form with
	// a JSON array (e.g. ["string","null"]). Used by the converter's
	// 3.1+ post-walk to translate `nullable: true` into the JSON Schema
	// 2020-12 idiom required by 3.1 / 3.2. When set, MarshalJSON
	// suppresses both Type and Nullable in the output.
	TypeList []string `json:"-"`

	// Extensions holds `x-*` keys (e.g. `x-enum-descriptions` from
	// Blueprint+ §11.2, or unknown MSON-member keys per §13).
	Extensions map[string]any `json:"-"`
}

// MarshalJSON splays Schema `x-*` extensions onto the schema object,
// and (when TypeList is set) replaces the singular `type:` field with
// a JSON array so the schema validates against OAS 3.1 / 3.2.
func (s *Schema) MarshalJSON() ([]byte, error) {
	type alias Schema
	if len(s.TypeList) == 0 {
		return marshalWithExtensions((*alias)(s), s.Extensions)
	}
	clone := *s
	clone.Type = ""        // suppressed: TypeList wins
	clone.Nullable = false // suppressed: encoded inside TypeList
	clone.TypeList = nil   // suppressed: spliced in below
	raw, err := json.Marshal((*alias)(&clone))
	if err != nil {
		return nil, err
	}
	typeArr, err := json.Marshal(s.TypeList)
	if err != nil {
		return nil, err
	}
	// Splice `"type":[...]` as the first key so it appears at the top
	// of the rendered schema, matching typical OAS field ordering
	// (`$ref`/`title` first, then `type`).
	var buf bytes.Buffer
	buf.WriteByte('{')
	buf.WriteString(`"type":`)
	buf.Write(typeArr)
	if len(raw) > 2 {
		buf.WriteByte(',')
		buf.Write(raw[1 : len(raw)-1])
	}
	buf.WriteByte('}')
	return marshalWithExtensions(json.RawMessage(buf.Bytes()), s.Extensions)
}

// Discriminator is the OAS Discriminator Object (used with oneOf/anyOf).
type Discriminator struct {
	PropertyName string            `json:"propertyName"`
	Mapping      map[string]string `json:"mapping,omitempty"`
}

// Components is an OAS Components Object (subset). SecuritySchemes is
// emitted *after* Schemas so it sits at the very bottom of the document
// - easy to find by scrolling to the end. The high-level `security:`
// requirement still lives near the top of the doc (see Document field
// order), so this only affects where scheme *definitions* render.
type Components struct {
	Schemas         map[string]*Schema         `json:"schemas,omitempty"`
	SecuritySchemes map[string]*SecurityScheme `json:"securitySchemes,omitempty"`
}

// SecurityRequirement is an OAS Security Requirement Object: a map from
// scheme name to the list of scopes (or roles) required. Multiple entries
// in a slice mean "any of"; multiple keys in one map mean "all of".
type SecurityRequirement map[string][]string

// SecurityScheme is an OAS Security Scheme Object. Only the fields the
// converter consumes from a sidecar config are modelled - extend on demand.
type SecurityScheme struct {
	Type             string      `json:"type"`
	Description      string      `json:"description,omitempty"`
	Name             string      `json:"name,omitempty"`             // apiKey
	In               string      `json:"in,omitempty"`               // apiKey
	Scheme           string      `json:"scheme,omitempty"`           // http
	BearerFormat     string      `json:"bearerFormat,omitempty"`     // http+bearer
	Flows            *OAuthFlows `json:"flows,omitempty"`            // oauth2
	OpenIDConnectURL string      `json:"openIdConnectUrl,omitempty"` // openIdConnect
}

// OAuthFlows is an OAS OAuth Flows Object.
type OAuthFlows struct {
	Implicit          *OAuthFlow `json:"implicit,omitempty"`
	Password          *OAuthFlow `json:"password,omitempty"`
	ClientCredentials *OAuthFlow `json:"clientCredentials,omitempty"`
	AuthorizationCode *OAuthFlow `json:"authorizationCode,omitempty"`
}

// OAuthFlow is an OAS OAuth Flow Object.
type OAuthFlow struct {
	AuthorizationURL string            `json:"authorizationUrl,omitempty"`
	TokenURL         string            `json:"tokenUrl,omitempty"`
	RefreshURL       string            `json:"refreshUrl,omitempty"`
	Scopes           map[string]string `json:"scopes,omitempty"`
}

// NewDocument returns an empty but structurally valid OAS 3.0 document.
func NewDocument() *Document {
	return &Document{
		OpenAPI: "3.0.3",
		Info:    Info{Title: "Untitled API", Version: "0.0.0"},
		Paths:   map[string]*PathItem{},
	}
}

// jsonMarshalNoMethod marshals v with `encoding/json` while skipping any
// custom MarshalJSON method on v's type - used by Parameter.MarshalJSON to
// produce the "raw" body before splicing in the anchor sentinel.
func jsonMarshalNoMethod(v any) ([]byte, error) {
	return json.Marshal(v)
}

// kv is a tiny ordered-pair struct used to assemble small JSON objects
// where key order matters (sentinel keys must come first).
type kv struct {
	Key   string
	Value string
}

// jsonObject builds a JSON object from an ordered list of string→string
// pairs. Used for emitting alias sentinels: `{"$$alias":"ref_3"}`.
func jsonObject(pairs []kv) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, p := range pairs {
		if i > 0 {
			buf.WriteByte(',')
		}
		k, err := json.Marshal(p.Key)
		if err != nil {
			return nil, err
		}
		v, err := json.Marshal(p.Value)
		if err != nil {
			return nil, err
		}
		buf.Write(k)
		buf.WriteByte(':')
		buf.Write(v)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// spliceFirstKey rewrites a JSON object body so that (key, value) becomes
// its first entry. The body is assumed to start with '{'. Empty objects
// become {key:value}; non-empty become {key:value, ...rest}.
func spliceFirstKey(body []byte, key, value string) ([]byte, error) {
	if len(body) < 2 || body[0] != '{' {
		return nil, &json.SyntaxError{}
	}
	k, err := json.Marshal(key)
	if err != nil {
		return nil, err
	}
	v, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	out.WriteByte('{')
	out.Write(k)
	out.WriteByte(':')
	out.Write(v)
	if len(body) > 2 { // non-empty existing body
		out.WriteByte(',')
		out.Write(body[1:])
	} else {
		out.WriteByte('}')
	}
	return out.Bytes(), nil
}

// upper uppercases ASCII letters; small helper to avoid importing strings.
func upper(s string) string {
	out := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		out[i] = c
	}
	return string(out)
}

// marshalWithExtensions JSON-encodes base (typically a struct alias to
// avoid recursion) and splices ext entries onto the resulting object so
// they appear at the end, preserving struct field order while still
// emitting deterministic output. Extension keys are sorted alphabetically.
//
// Used by every OAS type that supports `x-*` extensions (Operation,
// PathItem, Schema, Document, Tag, Parameter, …) - keep the helper
// generic so each type's MarshalJSON stays a one-liner.
func marshalWithExtensions(base any, ext map[string]any) ([]byte, error) {
	raw, err := json.Marshal(base)
	if err != nil {
		return nil, err
	}
	if len(ext) == 0 {
		return raw, nil
	}
	// Skip non-x-* keys defensively (the converter should never put them
	// here, but be safe).
	keys := make([]string, 0, len(ext))
	for k := range ext {
		if len(k) > 2 && k[0] == 'x' && k[1] == '-' {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return raw, nil
	}
	sortStrings(keys)
	var buf bytes.Buffer
	// Drop trailing '}'; if base was "{}" we open with empty body.
	buf.Write(raw[:len(raw)-1])
	if len(raw) > 2 {
		buf.WriteByte(',')
	}
	for i, k := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kj, _ := json.Marshal(k) //nolint:errcheck // marshalling a string never fails
		vj, err := json.Marshal(ext[k])
		if err != nil {
			return nil, err
		}
		buf.Write(kj)
		buf.WriteByte(':')
		buf.Write(vj)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// sortStrings is a tiny insertion sort used so we don't pull `sort` into
// this package's import set just for one call site.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

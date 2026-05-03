package convert

import (
	"encoding/json"
	"strings"
)

// This file holds typed Go structs that mirror just enough of the Drafter
// Refract / API Elements JSON output for the converter to walk it. The
// shapes were derived from real `drafter -f json` output against the
// fixtures in `testdata/` (`polls.apib` and friends).
//
// Many Refract values are polymorphic. Recurring patterns are factored into
// small typed wrappers (`stringValue`, `numberValue`, `elementsArray`).
// Where a node's `content` can be a string OR an object OR an array, we use
// `json.RawMessage` and offer `contentString()` / `contentObject()` /
// `contentArray()` helpers so call sites stay declarative.

type parseResult struct {
	Element string    `json:"element"`
	Content []element `json:"content"`
}

// firstCategory returns the first `category` element (the API root) or nil.
func (p *parseResult) firstCategory() *element {
	for i := range p.Content {
		if p.Content[i].Element == "category" {
			return &p.Content[i]
		}
	}
	return nil
}

// annotations returns every parser-emitted note (warning/error) at the root
// of the parse result. Each annotation carries `meta.classes` for severity
// and `attributes.code` for a numeric code.
func (p *parseResult) annotations() []element {
	var out []element
	for _, c := range p.Content {
		if c.Element == "annotation" {
			out = append(out, c)
		}
	}
	return out
}

// dataStructures returns MSON Named Type definitions declared anywhere
// under the top-level API category. In real Drafter output the definitions
// live in a nested `category` with class `dataStructures` (created from
// the `# Data Structures` section), so we walk recursively into child
// categories rather than only inspecting direct children.
//
// The returned map is keyed by the named-type id (e.g. "ArticleData").
// Bare references to a previously-defined named type
// (`{element:"ArticleData"}` with no content) are skipped - only true
// definitions are included.
func (p *parseResult) dataStructures() map[string]*element {
	out := map[string]*element{}
	cat := p.firstCategory()
	if cat == nil {
		return out
	}
	collectDataStructures(cat, out)
	return out
}

// collectDataStructures recursively walks an element's content tree and
// records every dataStructure definition it finds (those with a meta.id on
// the inner type).
func collectDataStructures(e *element, out map[string]*element) {
	for _, c := range e.contentArray() {
		c := c
		switch c.Element {
		case "category":
			collectDataStructures(&c, out)
		case "dataStructure":
			inner := c.dataStructureInner()
			if inner == nil {
				continue
			}
			id := inner.Meta.ID.Content
			if id == "" {
				continue
			}
			copy := *inner
			out[id] = &copy
		}
	}
}

// element is the shared shape of every Refract node we need to inspect.
// `Content` is a json.RawMessage because Refract sometimes encodes it as a
// string (copy/asset bodies, scalar primitive values), an object (member
// kv pairs, dataStructure inner type) or an array (most containers). Use
// the helper methods rather than reading the field directly.
type element struct {
	Element    string          `json:"element"`
	Meta       elementMeta     `json:"meta"`
	Attributes elementAttrs    `json:"attributes"`
	Content    json.RawMessage `json:"content"`
}

// elementMeta covers every meta key Drafter is observed to emit.
//
//   - title       - human title (category, transition, named type alias…)
//   - description - markdown description (one-line stringValue envelope)
//   - classes     - semantic tags ("api", "messageBody", "warning", …)
//   - id          - present on the inner element of a dataStructure that
//     defines an MSON named type (e.g. "ArticleData").
type elementMeta struct {
	Title       stringValue `json:"title"`
	Description stringValue `json:"description"`
	Classes     classesList `json:"classes"`
	ID          stringValue `json:"id"`
}

// elementAttrs covers every attribute key observed in real Drafter output.
//
// HTTP-related fields (Method, StatusCode, Headers, ContentType) sit on
// httpRequest / httpResponse / asset elements. URI-template metadata
// (Href, HrefVariables) sits on resource elements. MSON-related fields
// (Enumerations, TypeAttributes) sit on enum / member elements. Annotation
// diagnostics (Code, Line, Column, SourceMap) sit on `annotation`
// elements.
type elementAttrs struct {
	Href           stringValue        `json:"href"`
	HrefVariables  hrefVariablesValue `json:"hrefVariables"`
	Method         stringValue        `json:"method"`
	StatusCode     stringValue        `json:"statusCode"`
	Headers        headersValue       `json:"headers"`
	ContentType    stringValue        `json:"contentType"`
	Version        stringValue        `json:"version"`
	Metadata       metadataList       `json:"metadata"`
	TypeAttributes classesList        `json:"typeAttributes"`
	Enumerations   elementsArray      `json:"enumerations"`
	Code           numberValue        `json:"code"`
	Line           numberValue        `json:"line"`
	Column         numberValue        `json:"column"`
	SourceMap      json.RawMessage    `json:"sourceMap"`
}

// stringValue is the `{ "element": "string", "content": "..." }` envelope.
type stringValue struct {
	Element string `json:"element"`
	Content string `json:"content"`
}

// numberValue is the `{ "element": "number", "content": <n> }` envelope
// used by annotation codes and source-map line/column positions.
type numberValue struct {
	Element string  `json:"element"`
	Content float64 `json:"content"`
}

// classesList is the `{ "element": "array", "content": [stringValue,...] }`
// envelope used for meta.classes and attributes.typeAttributes.
type classesList struct {
	Element string        `json:"element"`
	Content []stringValue `json:"content"`
}

// elementsArray is the `{ "element": "array", "content": [<element>,...] }`
// envelope used for attributes.enumerations and other heterogeneous lists
// of typed elements.
type elementsArray struct {
	Element string    `json:"element"`
	Content []element `json:"content"`
}

// hrefVariablesValue mirrors `attributes.hrefVariables` as emitted by
// Drafter - an array of MSON members.
type hrefVariablesValue struct {
	Element string         `json:"element"`
	Content []memberStruct `json:"content"`
}

// headersValue mirrors `attributes.headers` (httpHeaders element).
type headersValue struct {
	Element string         `json:"element"`
	Content []memberStruct `json:"content"`
}

// metadataList mirrors `attributes.metadata` on category nodes.
type metadataList struct {
	Element string         `json:"element"`
	Content []memberStruct `json:"content"`
}

// memberStruct is the shared shape of an MSON `member` element.
//
// Keys are always string-typed elements; values are arbitrary elements
// (string, number, boolean, object, array, enum, or a reference to a
// named type - `{ "element": "ArticleData" }`). Use `Content.Value`'s
// helpers (`contentString`, `contentObject`, `contentArray`) accordingly.
type memberStruct struct {
	Element    string          `json:"element"`
	Meta       elementMeta     `json:"meta"`
	Attributes elementAttrs    `json:"attributes"`
	Content    memberKVContent `json:"content"`
}

type memberKVContent struct {
	Key   stringValue `json:"key"`
	Value element     `json:"value"`
}

// contentArray decodes `Content` as a list of child elements. Returns nil
// when the field is absent or not a JSON array.
func (e *element) contentArray() []element {
	if len(e.Content) == 0 || e.Content[0] != '[' {
		return nil
	}
	var out []element
	if err := json.Unmarshal(e.Content, &out); err != nil {
		return nil
	}
	return out
}

// contentObject decodes `Content` as a single child element. Used for
// nodes whose content is one wrapped element (notably `dataStructure`,
// whose content is the typed inner element being declared/referenced).
// Returns nil when the field is absent or not a JSON object.
func (e *element) contentObject() *element {
	if len(e.Content) == 0 || e.Content[0] != '{' {
		return nil
	}
	var out element
	if err := json.Unmarshal(e.Content, &out); err != nil {
		return nil
	}
	return &out
}

// contentString decodes `Content` as a string when the element is a
// copy/asset/etc. Returns "" when the field is absent or not a string.
func (e *element) contentString() string {
	if len(e.Content) == 0 || e.Content[0] != '"' {
		return ""
	}
	var s string
	if err := json.Unmarshal(e.Content, &s); err != nil {
		return ""
	}
	return s
}

// dataStructureInner returns the typed element wrapped inside a
// dataStructure container. Drafter most commonly encodes that as a single
// object child; some malformed inputs may use an array - handle both.
// Returns nil when the structure is empty.
func (e *element) dataStructureInner() *element {
	if obj := e.contentObject(); obj != nil {
		return obj
	}
	if arr := e.contentArray(); len(arr) > 0 {
		return &arr[0]
	}
	return nil
}

// isReference reports whether this element is a bare reference to a named
// type - i.e. its element name is custom (not a Refract / MSON primitive)
// and it carries no defining content of its own.
func (e *element) isReference() bool {
	switch e.Element {
	case "", "string", "number", "boolean", "array", "object", "enum",
		"member", "category", "resource", "transition", "httpTransaction",
		"httpRequest", "httpResponse", "httpHeaders", "asset", "copy",
		"dataStructure", "annotation", "sourceMap", "hrefVariables":
		return false
	}
	return len(e.Content) == 0 && e.Meta.ID.Content == ""
}

// severity returns the first class on a node ("warning", "error",
// "messageBody", …). For annotation elements this is the diagnostic level.
func (e *element) severity() string {
	if len(e.Meta.Classes.Content) == 0 {
		return ""
	}
	return e.Meta.Classes.Content[0].Content
}

// enumerationStrings returns the string-typed values declared in
// `attributes.enumerations`. Non-string enumeration members are skipped.
func (e *element) enumerationStrings() []string {
	var out []string
	for _, ev := range e.Attributes.Enumerations.Content {
		if ev.Element == "string" {
			out = append(out, ev.contentString())
		}
	}
	return out
}

// enumerationStringsWithDescriptions returns parallel slices of enum
// values and their per-value descriptions (`+ active - User is active`).
// Empty descriptions are returned as empty strings so the indices stay
// aligned. Non-string enumeration entries are skipped from both slices.
//
// Used to emit `x-enum-descriptions[]` per Blueprint+ §11.2.
func (e *element) enumerationStringsWithDescriptions() (values, descs []string) {
	for _, ev := range e.Attributes.Enumerations.Content {
		if ev.Element != "string" {
			continue
		}
		values = append(values, ev.contentString())
		descs = append(descs, ev.Meta.Description.Content)
	}
	return values, descs
}

// description / id are tiny convenience accessors so call sites can
// read closer to the spec vocabulary in `specs/apib.md`.
func (e *element) description() string { return e.Meta.Description.Content }
func (e *element) id() string          { return e.Meta.ID.Content }

// method returns the HTTP method from a transition by walking into its
// httpTransaction → httpRequest child. Returns "" if the transition does
// not contain any.
func (e *element) method() string {
	for _, c := range e.contentArray() {
		if c.Element != "httpTransaction" {
			continue
		}
		for _, t := range c.contentArray() {
			if t.Element == "httpRequest" {
				return t.Attributes.Method.Content
			}
		}
	}
	return ""
}

// metadataValue looks up a key from category-level metadata (FORMAT, HOST, …).
func (e *element) metadataValue(key string) string {
	for _, m := range e.Attributes.Metadata.Content {
		if m.Content.Key.Content == key {
			return m.Content.Value.contentString()
		}
	}
	return ""
}

// firstMetadataValue returns the first non-empty metadata value found for
// any of keys, matched case-insensitively. Useful for "soft" conventions
// like `VERSION:` / `API-VERSION:` / `API_VERSION:` that aren't part of
// the formal APIB spec but are widely used.
func firstMetadataValue(e *element, keys ...string) string {
	for _, m := range e.Attributes.Metadata.Content {
		k := m.Content.Key.Content
		for _, want := range keys {
			if strings.EqualFold(k, want) {
				if v := m.Content.Value.contentString(); v != "" {
					return v
				}
			}
		}
	}
	return ""
}

// metadataValuesAll returns every metadata value whose key matches any of
// the given names (case-insensitive), in source order. Used for
// multi-valued conventions like repeated `HOST:` entries that map to the
// OAS `servers` array.
func metadataValuesAll(e *element, keys ...string) []string {
	var out []string
	for _, m := range e.Attributes.Metadata.Content {
		k := m.Content.Key.Content
		for _, want := range keys {
			if strings.EqualFold(k, want) {
				if v := m.Content.Value.contentString(); v != "" {
					out = append(out, v)
				}
				break
			}
		}
	}
	return out
}

// metadataValueIfPresent returns (value, true) for the first matching key
// (case-insensitive) regardless of whether the value is empty, and
// ("", false) when the key is absent. Used by parseDocumentSecurity so an
// empty `SECURITY:` line can explicitly clear auth (distinct from "not
// declared at all").
func metadataValueIfPresent(e *element, key string) (string, bool) {
	for _, m := range e.Attributes.Metadata.Content {
		if strings.EqualFold(m.Content.Key.Content, key) {
			return strings.TrimSpace(m.Content.Value.contentString()), true
		}
	}
	return "", false
}

// asTransition is a convenience alias used by the translator so call
// sites read closer to the spec vocabulary in apib.md.
func (e *element) asTransition() *element { return e }

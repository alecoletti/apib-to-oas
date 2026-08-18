package convert

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/alecoletti/apib-to-oas/internal/oas"
)

// schemaResolver converts MSON-flavoured Refract elements into OAS schemas.
// It carries a registry of named-type definitions (built once per document
// from `parseResult.dataStructures()`) so references like
// `{element:"ArticleData"}` can be inlined recursively.
//
// Cycles are guarded with a per-walk `visited` set so a recursive type
// (a -> b -> a) collapses safely instead of blowing the stack.
//
// `useRefs`, when true, causes references to known named types to be
// emitted as `{$ref: "#/components/schemas/<name>"}` instead of being
// inlined. This mode is used when populating components.schemas so
// schemas there cross-reference each other rather than recursively
// expanding (which would explode the document and lose the named-type
// boundary).
type schemaResolver struct {
	registry map[string]*element
	useRefs  bool

	// diag (when non-nil) receives Blueprint+ §15 codes emitted while
	// resolving - currently E006 (undefined named-type reference). The
	// `seenMissing` set dedupes per-document so a type referenced from
	// 30 places only fires once.
	diag        *Diagnostics
	seenMissing map[string]bool
}

func newSchemaResolver(registry map[string]*element) *schemaResolver {
	if registry == nil {
		registry = map[string]*element{}
	}
	return &schemaResolver{registry: registry, seenMissing: map[string]bool{}}
}

// withRefs returns a sibling resolver that emits `$ref` for known named
// types instead of inlining their definitions. The diagnostics
// collector and seen-set are shared so refs-mode and inline-mode
// passes don't double-fire E006 against the same author.
func (r *schemaResolver) withRefs() *schemaResolver {
	cp := *r
	cp.useRefs = true
	return &cp
}

// withDiagnostics attaches a diagnostics collector. Returns a sibling
// resolver so callers can chain (.withRefs().withDiagnostics(d) etc.).
func (r *schemaResolver) withDiagnostics(d *Diagnostics) *schemaResolver {
	cp := *r
	cp.diag = d
	if cp.seenMissing == nil {
		cp.seenMissing = map[string]bool{}
	}
	return &cp
}

// schemaFor returns a fully resolved OAS schema for an MSON element. The
// element may be:
//
//   - "object"  → {type:object, properties:..., required:...}
//   - "array"   → {type:array, items:<schemaFor first member>}
//   - "enum"    → {type:string, enum:[<enumeration string values>]}
//   - "string" / "number" / "boolean" → {type: ...}
//   - a custom named-type → looked up in the registry; if found, the
//     definition is converted; if missing, falls back to {type: object}.
func (r *schemaResolver) schemaFor(el *element) *oas.Schema {
	if el == nil {
		return nil
	}
	return r.schemaForVisited(el, map[string]bool{})
}

func (r *schemaResolver) schemaForVisited(el *element, visited map[string]bool) *oas.Schema {
	if el == nil {
		return nil
	}
	switch el.Element {
	case "object":
		return r.objectSchema(el, visited)
	case "array":
		return r.arraySchema(el, visited)
	case "enum":
		return r.enumSchema(el, visited)
	case "string", "number", "boolean":
		s := &oas.Schema{Type: msonNumberOASType(el)}
		s.Description = extractConstraintsFromDescription(s, el.description())
		return s
	default:
		// Custom named type. Emit a $ref when in components mode (and
		// the type is known); otherwise resolve via the registry,
		// guarding cycles.
		if r.useRefs {
			if _, ok := r.registry[el.Element]; ok {
				return &oas.Schema{
					Ref:         "#/components/schemas/" + el.Element,
					Description: el.description(),
				}
			}
		}
		if visited[el.Element] {
			return &oas.Schema{Type: "object", Description: "circular reference: " + el.Element}
		}
		def, ok := r.registry[el.Element]
		if !ok {
			// Blueprint+ §15 E006 - referenced named type isn't in the
			// registry. Dedupe so noisy specs don't fire repeatedly.
			if r.diag != nil && el.Element != "" && !r.seenMissing[el.Element] {
				r.seenMissing[el.Element] = true
				r.diag.Error(CodeUndefinedType, "undefined named type referenced: "+el.Element)
			}
			return &oas.Schema{Type: "object", Description: el.description()}
		}
		visited[el.Element] = true
		out := r.schemaForVisited(def, visited)
		delete(visited, el.Element)
		// Carry over an inline description from the *reference site* if any.
		if d := el.description(); d != "" && out != nil && out.Description == "" {
			out.Description = d
		}
		return out
	}
}

// objectSchema converts an `object` element (members under `content`) into
// an OAS object schema with properties, required, and propagation of
// member-level MSON type-attributes (required / nullable / fixed → enum
// of one / readOnly / writeOnly / default value / format inference).
func (r *schemaResolver) objectSchema(el *element, visited map[string]bool) *oas.Schema {
	s := &oas.Schema{Type: "object"}
	s.Description = extractConstraintsFromDescription(s, el.description())
	props := map[string]*oas.Schema{}
	var required []string
	var oneOf []*oas.Schema
	for _, c := range el.contentArray() {
		switch c.Element {
		case "member":
			name, valSchema, isRequired := r.decodeMemberSchema(&c, visited)
			if name == "" {
				continue
			}
			// Blueprint+ `+ Schema Patch` pseudo-member: when Drafter
			// successfully parses all MSON members it treats `+ Schema
			// Patch` as an extra member with key "Schema Patch" (string
			// type) and the indented JSON body as the member description.
			// Intercept it and apply the conditionals to s rather than
			// leaking a spurious property named "Schema Patch".
			if strings.EqualFold(strings.TrimSpace(name), "Schema Patch") {
				if valSchema != nil {
					switch {
					case valSchema.Description != "":
						applySchemaPatch(s, valSchema.Description)
					case valSchema.Example != nil:
						if ex, ok := valSchema.Example.(string); ok {
							applySchemaPatch(s, ex)
						}
					default:
						// Drafter parsed the JSON body as a nested object
						// schema rather than a string description. Re-serialise
						// and apply it as a patch.
						if b, err := json.Marshal(valSchema); err == nil {
							applySchemaPatch(s, string(b))
						}
					}
				}
				continue
			}
			props[name] = valSchema
			if isRequired {
				required = append(required, name)
			}
		case "select":
			oneOf = append(oneOf, r.decodeSelectSchema(&c, visited)...)
		}
	}
	if len(props) > 0 {
		s.Properties = props
	}
	if len(required) > 0 {
		s.Required = required
	}
	if len(oneOf) > 0 {
		s.OneOf = oneOf
	}
	// Whole-object type attributes (rare but valid) - mostly "fixed-type",
	// which translates to `additionalProperties: false`.
	applyTypeAttributes(s, el.Attributes.TypeAttributes)

	// Recovery: Drafter v5.1.0 sometimes fails to parse complex MSON
	// (especially `One Of` blocks or multi-line prose followed by
	// members) and dumps the entire body into meta.description with
	// zero content children. When that happens, try to salvage
	// properties and oneOf from the description text.
	if len(s.Properties) == 0 && len(s.OneOf) == 0 && s.Description != "" {
		r.recoverMembersFromDescription(s, visited)
	}
	return s
}

// decodeMemberSchema converts a single "member" element into an OAS property
// schema. Returns the property name, the resolved schema, and whether the
// member is marked required. Returns ("", nil, false) when the member should
// be skipped.
func (r *schemaResolver) decodeMemberSchema(c *element, visited map[string]bool) (name string, schema *oas.Schema, required bool) {
	m := decodeMember(c)
	if m == nil {
		return "", nil, false
	}
	name = m.Content.Key.Content
	if name == "" {
		return "", nil, false
	}
	valSchema := r.schemaForVisited(&m.Content.Value, visited)
	if valSchema == nil {
		valSchema = &oas.Schema{Type: "string"}
	}
	// Member-level description (from `- name (type) - description`)
	// trumps any inherited from the value type. Description-prefix flags
	// ([deprecated], [readOnly], [writeOnly]) are extracted first; then a
	// `+ Meta` block is rescued as schema constraints (§14) and stripped
	// from the visible prose. This prefix approach is the reliable path
	// when `### Properties` is used — Drafter drops `+ Meta` sub-blocks
	// and unknown type attributes (e.g. `deprecated` on named-type refs)
	// in that mode.
	if d := m.Meta.Description.Content; d != "" {
		d, flags := parseSchemaDescPrefix(d)
		if flags.Deprecated {
			valSchema.Deprecated = true
		}
		if flags.ReadOnly {
			valSchema.ReadOnly = true
		}
		if flags.WriteOnly {
			valSchema.WriteOnly = true
		}
		valSchema.Description = extractConstraintsFromDescription(valSchema, d)
	}
	// Inline default / example value: when the value element carries a
	// scalar `content`, surface it as the property's example.
	if ex := scalarExample(&m.Content.Value); ex != nil && valSchema.Example == nil {
		valSchema.Example = ex
	}
	// Apply member-level MSON type attributes (`required`, `nullable`,
	// `fixed`, `optional`, etc.) - these live on the *member* element,
	// not the value, so they need their own pass.
	applyTypeAttributes(valSchema, m.Attributes.TypeAttributes)
	// Infer JSON Schema `format` from the example value's shape (UUID,
	// RFC 3339 date-time, email, URI, …) when no explicit format is set.
	applyFormatInference(valSchema, &m.Content.Value)
	for _, ta := range m.Attributes.TypeAttributes.Content {
		if ta.Content == "required" {
			required = true
			break
		}
	}
	return name, valSchema, required
}

// decodeSelectSchema converts a "select" element (MSON `One Of`) into a slice
// of OAS oneOf option schemas. Each child "option" element becomes one
// alternative object shape.
func (r *schemaResolver) decodeSelectSchema(c *element, visited map[string]bool) []*oas.Schema {
	var oneOf []*oas.Schema
	for _, opt := range c.contentArray() {
		if opt.Element != "option" {
			continue
		}
		optSchema := &oas.Schema{
			Type:       "object",
			Properties: map[string]*oas.Schema{},
		}
		// Carry option title when Drafter populates meta.title on the option
		// element (e.g. authored as `+ Properties (title)` in extended MSON).
		if t := opt.Meta.Title.Content; t != "" {
			optSchema.Title = t
		}
		for _, om := range opt.contentArray() {
			if om.Element != "member" {
				continue
			}
			mem := decodeMember(&om)
			if mem == nil {
				continue
			}
			mName := mem.Content.Key.Content
			if mName == "" {
				continue
			}
			mSchema := r.schemaForVisited(&mem.Content.Value, visited)
			if mSchema == nil {
				mSchema = &oas.Schema{Type: "string"}
			}
			if d := mem.Meta.Description.Content; d != "" {
				mSchema.Description = d
			}
			if ex := scalarExample(&mem.Content.Value); ex != nil && mSchema.Example == nil {
				mSchema.Example = ex
			}
			applyTypeAttributes(mSchema, mem.Attributes.TypeAttributes)
			optSchema.Properties[mName] = mSchema
			for _, ta := range mem.Attributes.TypeAttributes.Content {
				if ta.Content == "required" {
					optSchema.Required = append(optSchema.Required, mName)
					break
				}
			}
		}
		if len(optSchema.Properties) > 0 {
			oneOf = append(oneOf, optSchema)
		}
	}
	return oneOf
}

// applyFormatInference sets schema.Format (or schema.Items.Format for arrays)
// by pattern-matching the example value when no explicit format is present.
// For plain string schemas the example is already on schema.Example; for
// array[string] schemas the example lives in the first child of the raw value
// element (scalarExample returns nil for array elements).
func applyFormatInference(schema *oas.Schema, rawValue *element) {
	if schema.Format != "" {
		return
	}
	if f := inferFormat(schema); f != "" {
		schema.Format = f
		return
	}
	// array[string]: inferFormat skips type:"array", so inspect the first
	// item of the raw value element instead.
	if schema.Type != "array" || schema.Items == nil || schema.Items.Format != "" {
		return
	}
	items := rawValue.contentArray()
	if len(items) == 0 {
		return
	}
	if firstEx := scalarExample(&items[0]); firstEx != nil {
		if f := inferFormat(&oas.Schema{Type: schema.Items.Type, Example: firstEx}); f != "" {
			schema.Items.Format = f
		}
	}
}

// arraySchema converts an `array` element. The first child element (if any)
// describes the item type; with no children we fall back to `string` items.
//
// When an MSON array member is annotated with a `+ Meta` block (Blueprint+
// §14), Drafter — which does not understand `+ Meta` — treats it as an
// additional sample item of the array.  Those phantom items end up as extra
// children of this element with constraint-keyword keys (e.g.
// {element:"object", content:[{key:"MaxItems",value:20}]}).  We detect them
// via tryExtractMetaFromArrayChild, apply their constraints to s, and skip
// them so they don't pollute the item-type resolution or example bodies.
func (r *schemaResolver) arraySchema(el *element, visited map[string]bool) *oas.Schema {
	children := el.contentArray()
	var items *oas.Schema
	if len(children) > 0 {
		items = r.schemaForVisited(&children[0], visited)
	}
	if items == nil {
		items = &oas.Schema{Type: "string"}
	}
	s := &oas.Schema{Type: "array", Items: items}
	s.Description = extractConstraintsFromDescription(s, el.description())
	// Apply any Blueprint+ `+ Meta` constraint objects that Drafter leaked
	// as extra array children (children beyond index 0).
	for i := 1; i < len(children); i++ {
		if mb := tryExtractMetaFromArrayChild(&children[i]); mb != nil {
			applyMetaToSchema(s, mb)
		}
	}
	return s
}

// enumSchema converts an `enum` element. The base type is taken from the
// inner sample value (e.g. `content.element == "string"`); enumerable values
// come from `attributes.enumerations`. Per-value descriptions are emitted
// as `x-enum-descriptions[]` (positionally aligned with `enum[]`) per
// Blueprint+ §11.2.
func (r *schemaResolver) enumSchema(el *element, _ map[string]bool) *oas.Schema {
	base := "string"
	if inner := el.contentObject(); inner != nil && inner.Element != "" {
		base = msonTypeToOAS(inner.Element)
	}
	s := &oas.Schema{Type: base, Description: el.description()}
	vals, descs := el.enumerationStringsWithDescriptions()
	if len(vals) > 0 {
		s.Enum = make([]any, 0, len(vals))
		for _, v := range vals {
			s.Enum = append(s.Enum, v)
		}
		// Only emit x-enum-descriptions when at least one value is
		// actually described - otherwise the array is just noise.
		anyDesc := false
		for _, d := range descs {
			if d != "" {
				anyDesc = true
				break
			}
		}
		if anyDesc {
			out := make([]any, len(descs))
			for i, d := range descs {
				out[i] = d
			}
			if s.Extensions == nil {
				s.Extensions = map[string]any{}
			}
			s.Extensions["x-enum-descriptions"] = out
		}
	}
	return s
}

// decodeMember re-decodes an element-shaped JSON blob into the typed
// memberStruct shape. The two share most fields but `content` differs
// (object kv-pair vs polymorphic raw message), so a fresh unmarshal is
// the cleanest route.
func decodeMember(e *element) *memberStruct {
	if e == nil || e.Element != "member" {
		return nil
	}
	b, err := json.Marshal(e)
	if err != nil {
		return nil
	}
	var m memberStruct
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
	}
	return &m
}

// scalarExample extracts a simple example value from a primitive-typed
// member value (string/number/boolean). Returns nil for objects/arrays/refs.
func scalarExample(v *element) any {
	if v == nil {
		return nil
	}
	switch v.Element {
	case "string":
		s := v.contentString()
		if s == "" {
			return nil
		}
		return s
	case "number":
		var n float64
		if err := json.Unmarshal(v.Content, &n); err == nil {
			return n
		}
		return nil
	case "boolean":
		var b bool
		if err := json.Unmarshal(v.Content, &b); err == nil {
			return b
		}
		return nil
	}
	return nil
}

// schemaFromDataStructureChild returns the OAS schema for a Refract
// `dataStructure` element commonly found inside an httpRequest /
// httpResponse content array. The dataStructure wraps a single typed
// element (often a bare reference to a named type).
func (r *schemaResolver) schemaFromDataStructureChild(parent *element) *oas.Schema {
	for _, c := range parent.contentArray() {
		if c.Element != "dataStructure" {
			continue
		}
		inner := c.dataStructureInner()
		if inner == nil {
			continue
		}
		return r.schemaFor(inner)
	}
	return nil
}

// applyTypeAttributes maps MSON typeAttributes onto an OAS schema in
// place. Recognised values:
//
//   - "nullable"   → Nullable: true   (3.0; 3.1 would prefer a type array)
//   - "fixed"      → if the schema has an example, lock to it via Enum
//   - "fixed-type" → AdditionalProperties: false on objects
//   - "default"    → if no Default is set yet, copy the example value
//     (MSON convention: `+ limit: 10 (number, default)`
//     treats the sample as the default)
//   - "optional"   → no-op (OAS objects already make properties optional
//     by default; required-ness is encoded separately)
//   - "required"   → no-op here (handled by the parent objectSchema loop)
func applyTypeAttributes(s *oas.Schema, attrs classesList) {
	for _, ta := range attrs.Content {
		switch ta.Content {
		case "nullable":
			s.Nullable = true
		case "fixed":
			if s.Type == "object" {
				// fixed on an object: close additional properties AND promote
				// every declared property to required (MSON §3 semantics —
				// the object must look exactly as declared).
				s.AdditionalProperties = false
				for name := range s.Properties {
					already := false
					for _, r := range s.Required {
						if r == name {
							already = true
							break
						}
					}
					if !already {
						s.Required = append(s.Required, name)
					}
				}
			} else if s.Example != nil && len(s.Enum) == 0 {
				// fixed on a scalar: the only valid value is the example.
				s.Enum = []any{s.Example}
			}
		case "fixed-type", "fixedType": // MSON source: "fixed-type"; Drafter Refract: "fixedType"
			if s.Type == "object" {
				// fixed-type on an object: close additional properties but
				// leave individual property values unconstrained.
				s.AdditionalProperties = false
			}
		case "deprecated":
			s.Deprecated = true
		case "default":
			if s.Default == nil && s.Example != nil {
				s.Default = s.Example
			}
		}
	}
}

// inferFormat picks an OAS / JSON Schema `format` keyword for a string
// schema based on its example value's shape. Conservative: only adds a
// format when the pattern is unambiguous.
func inferFormat(s *oas.Schema) string {
	if s.Type != "string" {
		return ""
	}
	ex, ok := s.Example.(string)
	if !ok || ex == "" {
		return ""
	}
	switch {
	case reUUID.MatchString(ex):
		return "uuid"
	case reDateTime.MatchString(ex):
		return "date-time"
	case reDate.MatchString(ex):
		return "date"
	case reEmail.MatchString(ex):
		return "email"
	case reURI.MatchString(ex):
		return "uri"
	}
	return ""
}

// inferSchemaFromExample produces a minimal JSON-Schema description from a
// decoded example value. Used as a last-resort fallback when the APIB
// authored only a `+ Body` (JSON example) with no `+ Attributes` or
// `+ Schema` - without at least a `type:` most docs renderers (Redoc,
// Swagger UI, Stoplight) won't display the example at all.
//
// The walk is deliberately shallow on detail (no formats, descriptions,
// examples, required[]) to make it obvious in the rendered output that
// the schema is inferred - and so it never gets confused with an
// explicit MSON-derived schema. Returns nil for nil input.
func inferSchemaFromExample(v any) *oas.Schema {
	switch x := v.(type) {
	case nil:
		return nil
	case map[string]any:
		s := &oas.Schema{Type: "object"}
		if len(x) > 0 {
			s.Properties = make(map[string]*oas.Schema, len(x))
			for k, val := range x {
				if child := inferSchemaFromExample(val); child != nil {
					s.Properties[k] = child
				} else {
					// nil child (e.g. JSON null) - emit an empty schema so
					// the property still shows up in the rendered docs.
					s.Properties[k] = &oas.Schema{}
				}
			}
		}
		return s
	case []any:
		s := &oas.Schema{Type: "array"}
		if len(x) > 0 {
			if item := inferSchemaFromExample(x[0]); item != nil {
				s.Items = item
			}
		}
		return s
	case bool:
		return &oas.Schema{Type: "boolean"}
	case float64, float32, int, int32, int64:
		// json.Unmarshal into `any` always produces float64 for numbers;
		// the others are listed for completeness in case callers pass
		// pre-typed values.
		if f, ok := x.(float64); ok && f == float64(int64(f)) {
			return &oas.Schema{Type: "integer"}
		}
		return &oas.Schema{Type: "number"}
	case string:
		return &oas.Schema{Type: "string"}
	default:
		return &oas.Schema{}
	}
}

var (
	reUUID     = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	reDateTime = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:?\d{2})$`)
	reDate     = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	reEmail    = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
	reURI      = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.\-]*://`)
)

// ---------------------------------------------------------------------------
// Description-recovery: rescue MSON member lines (and `+ One Of` blocks)
// that Drafter v5.1.0 folds into meta.description instead of parsing
// into proper content children. This happens for complex named types
// with prose paragraphs followed by member definitions.
// ---------------------------------------------------------------------------

// collectIndentedBlock returns the lines that form an indented body under the
// header at lines[startIdx] (whose indent level is parentIndent). Blank lines
// are included verbatim; the first non-blank line at ≤ parentIndent terminates
// the block. Leading and trailing blank lines are trimmed. The returned newIdx
// is the last consumed index (caller should assign i = newIdx).
func collectIndentedBlock(lines []string, startIdx, parentIndent int) (body []string, newIdx int) {
	i := startIdx
	for i+1 < len(lines) {
		next := lines[i+1]
		if strings.TrimSpace(next) == "" {
			body = append(body, "")
			i++
			continue
		}
		if indentOf(next) > parentIndent {
			body = append(body, strings.TrimSpace(next))
			i++
			continue
		}
		break
	}
	for len(body) > 0 && body[0] == "" {
		body = body[1:]
	}
	for len(body) > 0 && body[len(body)-1] == "" {
		body = body[:len(body)-1]
	}
	return body, i
}

// - Prose (kept as the cleaned description)
// - Member lines (with correct nesting for inline anonymous objects) → properties + required
// - `+ One Of` blocks → oneOf schemas
//
// This is a best-effort recovery for Drafter's failure to parse complex
// MSON bodies. It handles the common cases; deeply nested structures or
// exotic MSON features may not be fully recovered.
func (r *schemaResolver) recoverMembersFromDescription(s *oas.Schema, visited map[string]bool) {
	lines := strings.Split(s.Description, "\n")

	var prose []string
	props := map[string]*oas.Schema{}
	var required []string
	var oneOfBlocks [][]*recoveredMember
	inOneOf := false
	var currentOption []*recoveredMember
	inProse := true
	topIndent := -1 // indent of first member/One-Of line

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if inProse {
				prose = append(prose, line)
			}
			continue
		}
		if isOneOfHeader(trimmed) {
			inProse = false
			inOneOf = true
			currentOption = nil
			if topIndent < 0 {
				topIndent = indentOf(line)
			}
			continue
		}
		if inOneOf {
			currentOption, oneOfBlocks = advanceOneOf(trimmed, currentOption, oneOfBlocks)
			continue
		}
		if isMemberPrefix(trimmed) {
			lineIndent := indentOf(line)
			if topIndent < 0 {
				topIndent = lineIndent
			}
			if lineIndent > topIndent {
				continue // consumed by look-ahead of its parent
			}
			newI, ok := r.processMemberLine(s, lines, i, lineIndent, trimmed, props, &required, &inProse, visited)
			if ok {
				i = newI
			}
			continue
		}
		if inProse {
			prose = append(prose, line)
		}
	}

	if currentOption != nil {
		oneOfBlocks = append(oneOfBlocks, currentOption)
	}
	if len(props) > 0 {
		s.Properties = props
	}
	if len(required) > 0 {
		s.Required = required
	}
	for _, option := range oneOfBlocks {
		s.OneOf = append(s.OneOf, r.oneOfOptionSchema(option, visited))
	}
	s.Description = strings.TrimSpace(strings.Join(prose, "\n"))
}

func isOneOfHeader(trimmed string) bool {
	return strings.HasPrefix(trimmed, "+ One Of") || strings.HasPrefix(trimmed, "- One Of")
}

func isMemberPrefix(trimmed string) bool {
	return strings.HasPrefix(trimmed, "+ ") || strings.HasPrefix(trimmed, "- ")
}

// advanceOneOf handles a single line inside a `+ One Of` block.
func advanceOneOf(trimmed string, current []*recoveredMember, blocks [][]*recoveredMember) ([]*recoveredMember, [][]*recoveredMember) {
	if strings.HasPrefix(trimmed, "+ Properties") || strings.HasPrefix(trimmed, "- Properties") {
		if current != nil {
			blocks = append(blocks, current)
		}
		return []*recoveredMember{}, blocks
	}
	if current != nil && isMemberPrefix(trimmed) {
		if m := parseMemberLine(trimmed); m != nil {
			current = append(current, m)
		}
	}
	return current, blocks
}

// processMemberLine handles one top-level `+/-` member line and its sub-block.
// Returns the updated loop index and true when the line was consumed.
func (r *schemaResolver) processMemberLine(
	s *oas.Schema,
	lines []string,
	i, lineIndent int,
	trimmed string,
	props map[string]*oas.Schema,
	required *[]string,
	inProse *bool,
	visited map[string]bool,
) (newI int, consumed bool) {
	suffix := strings.TrimSpace(trimmed[2:])
	if strings.EqualFold(suffix, "Schema Patch") {
		*inProse = false
		body, newIdx := collectIndentedBlock(lines, i, lineIndent)
		if len(body) > 0 {
			applySchemaPatch(s, strings.Join(body, "\n"))
		}
		return newIdx, true
	}
	m := parseMemberLine(trimmed)
	if m == nil {
		return i, false
	}
	*inProse = false
	subLines, newIdx := collectSubLines(lines, i, lineIndent)
	props[m.name] = r.schemaForMemberWithSubs(m, subLines, visited)
	if m.required {
		*required = append(*required, m.name)
	}
	return newIdx, true
}

// collectSubLines gathers lines following lines[i] that are indented deeper
// than parentIndent. Blank lines are absorbed only when deeper content follows.
func collectSubLines(lines []string, i, parentIndent int) (subLines []string, newI int) {
	for i+1 < len(lines) {
		next := lines[i+1]
		if strings.TrimSpace(next) == "" {
			j := i + 2
			for j < len(lines) && strings.TrimSpace(lines[j]) == "" {
				j++
			}
			if j < len(lines) && indentOf(lines[j]) > parentIndent {
				subLines = append(subLines, next)
				i++
				continue
			}
			break
		}
		if indentOf(next) > parentIndent {
			subLines = append(subLines, next)
			i++
		} else {
			break
		}
	}
	return subLines, i
}

// schemaForMemberWithSubs builds a schema for a recovered member, recursing
// into sub-lines when the member is an inline object type.
func (r *schemaResolver) schemaForMemberWithSubs(m *recoveredMember, subLines []string, visited map[string]bool) *oas.Schema {
	if len(subLines) > 0 && (m.typeName == "object" || m.typeName == "") {
		subProps, subReq := r.recoverMembersNested(subLines, visited)
		if len(subProps) > 0 {
			ps := &oas.Schema{Type: "object", Properties: subProps}
			if len(subReq) > 0 {
				ps.Required = subReq
			}
			if m.desc != "" {
				ps.Description = m.desc
			}
			return ps
		}
	}
	return r.schemaForRecoveredMember(m, visited)
}

// oneOfOptionSchema converts a slice of recoveredMembers into an OAS object schema.
func (r *schemaResolver) oneOfOptionSchema(members []*recoveredMember, visited map[string]bool) *oas.Schema {
	opt := &oas.Schema{Type: "object", Properties: map[string]*oas.Schema{}}
	for _, m := range members {
		opt.Properties[m.name] = r.schemaForRecoveredMember(m, visited)
		if m.required {
			opt.Required = append(opt.Required, m.name)
		}
	}
	return opt
}

// recoverMembersNested parses member lines from a sub-block inside an inline
// object property, recursing for further-nested objects at any depth.
func (r *schemaResolver) recoverMembersNested(lines []string, visited map[string]bool) (props map[string]*oas.Schema, required []string) {
	props = map[string]*oas.Schema{}
	baseIndent := -1
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || !isMemberPrefix(trimmed) {
			continue
		}
		lineIndent := indentOf(line)
		if baseIndent < 0 {
			baseIndent = lineIndent
		}
		if lineIndent != baseIndent {
			continue // consumed by look-ahead of its parent
		}
		m := parseMemberLine(trimmed)
		if m == nil {
			continue
		}
		subLines, newIdx := collectSubLines(lines, i, lineIndent)
		i = newIdx
		ps := r.schemaForMemberWithSubs(m, subLines, visited)
		props[m.name] = ps
		if m.required {
			required = append(required, m.name)
		}
	}
	return props, required
}

// recoveredMember holds a member parsed from description text.
type recoveredMember struct {
	name     string
	example  string   // inline sample value (e.g. "video, podcast, text")
	typeName string   // e.g. "string", "array[string]", "SportTags"
	attrs    []string // e.g. ["required", "optional", "nullable"]
	desc     string   // trailing description after ` - `
	required bool
}

// parseMemberLine extracts a recoveredMember from a `+ name: example (type, attrs) - desc` line.
func parseMemberLine(line string) *recoveredMember {
	// Strip leading `+ ` or `- `
	trimmed := strings.TrimSpace(line)
	if len(trimmed) < 2 {
		return nil
	}
	if trimmed[0] == '+' || trimmed[0] == '-' {
		trimmed = strings.TrimSpace(trimmed[1:])
	} else {
		return nil
	}

	m := &recoveredMember{}

	// Find the description part after ` - ` (but not inside parentheses)
	if idx := findDescSeparator(trimmed); idx >= 0 {
		m.desc = strings.TrimSpace(trimmed[idx+3:])
		trimmed = strings.TrimSpace(trimmed[:idx])
	}

	// Find the type/attrs part inside parentheses
	if lp := strings.LastIndex(trimmed, "("); lp >= 0 {
		if rp := strings.LastIndex(trimmed, ")"); rp > lp {
			typeAttrs := trimmed[lp+1 : rp]
			trimmed = strings.TrimSpace(trimmed[:lp])
			parts := strings.Split(typeAttrs, ",")
			for i, p := range parts {
				p = strings.TrimSpace(p)
				if i == 0 {
					m.typeName = p
				} else {
					switch p {
					case "required":
						m.required = true
						m.attrs = append(m.attrs, p)
					case "optional":
						m.attrs = append(m.attrs, p)
					case "nullable":
						m.attrs = append(m.attrs, p)
					default:
						m.attrs = append(m.attrs, p)
					}
				}
			}
		}
	}

	// Find the example part after `: `
	if idx := strings.Index(trimmed, ":"); idx >= 0 {
		m.name = strings.TrimSpace(trimmed[:idx])
		m.example = strings.TrimSpace(trimmed[idx+1:])
	} else {
		m.name = strings.TrimSpace(trimmed)
	}

	// Strip backtick quoting from name
	m.name = strings.Trim(m.name, "`")

	if m.name == "" || m.name == "One Of" || m.name == "Properties" {
		return nil
	}

	return m
}

// findDescSeparator finds ` - ` outside parentheses in the string.
func findDescSeparator(s string) int {
	depth := 0
	for i := 0; i < len(s)-2; i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		}
		if depth == 0 && i+2 < len(s) && s[i] == ' ' && s[i+1] == '-' && s[i+2] == ' ' {
			return i
		}
	}
	return -1
}

// schemaForRecoveredMember builds an OAS Schema from a description-recovered member.
func (r *schemaResolver) schemaForRecoveredMember(m *recoveredMember, visited map[string]bool) *oas.Schema {
	ps := r.resolveTypeString(m.typeName, visited)
	if ps == nil {
		ps = &oas.Schema{Type: "string"}
	}
	if m.desc != "" {
		ps.Description = m.desc
	}
	for _, a := range m.attrs {
		if a == "nullable" {
			ps.Nullable = true
		}
	}
	return ps
}

// resolveTypeString converts an MSON type string (e.g. "string",
// "array[string]", "array[SportTags]", "SportTags") into an OAS schema,
// using the resolver's registry for named-type lookups.
func (r *schemaResolver) resolveTypeString(typeName string, visited map[string]bool) *oas.Schema {
	if typeName == "" {
		return &oas.Schema{Type: "string"}
	}

	// Handle array[ItemType]
	if strings.HasPrefix(typeName, "array[") && strings.HasSuffix(typeName, "]") {
		inner := typeName[6 : len(typeName)-1]
		items := r.resolveTypeString(inner, visited)
		return &oas.Schema{
			Type:  "array",
			Items: items,
		}
	}

	// Primitive types
	switch typeName {
	case "string":
		return &oas.Schema{Type: "string"}
	case "number":
		return &oas.Schema{Type: "number"}
	case "integer":
		return &oas.Schema{Type: "integer"}
	case "boolean":
		return &oas.Schema{Type: "boolean"}
	case "object":
		return &oas.Schema{Type: "object"}
	}

	// Named type reference: emit $ref when in refs mode, otherwise resolve
	if r.useRefs {
		if _, ok := r.registry[typeName]; ok {
			return &oas.Schema{Ref: "#/components/schemas/" + typeName}
		}
	}
	if def, ok := r.registry[typeName]; ok {
		if visited[typeName] {
			return &oas.Schema{Type: "object", Description: "circular reference: " + typeName}
		}
		visited[typeName] = true
		out := r.schemaForVisited(def, visited)
		delete(visited, typeName)
		return out
	}

	// Unknown type — fall back to object
	return &oas.Schema{Type: "object"}
}

// tryExtractMetaFromArrayChild inspects an array child element that Drafter
// generated from a Blueprint+ `+ Meta` block written as an MSON sub-item of
// an array member.  Drafter does not understand `+ Meta` for dataStructure
// members and folds the block into the array's content as a phantom sample
// item.  The phantom is an `object` element whose members are all recognised
// constraint keywords (MaxItems, MinItems, MaxLength, …).
//
// When all member keys in the element are constraint keywords, the function
// returns a populated *metaBlock so the caller can apply the constraints to
// the array schema.  Returns nil when the element does not look like a Meta
// constraint block (i.e. it is a real sample item).
func tryExtractMetaFromArrayChild(el *element) *metaBlock {
	if el == nil {
		return nil
	}
	// Only objects can carry constraint properties.
	if el.Element != "object" {
		return nil
	}
	members := el.contentArray()
	if len(members) == 0 {
		return nil
	}
	mb := &metaBlock{}
	for _, m := range members {
		if m.Element != "member" {
			return nil // non-member child → real data
		}
		mem := decodeMember(&m)
		if mem == nil {
			return nil
		}
		key := strings.TrimSpace(mem.Content.Key.Content)
		val := strings.TrimSpace(mem.Content.Value.contentString())
		if key == "" {
			return nil
		}
		// applyMetaKey silently ignores truly unknown keys by adding to
		// mb.Extensions.  We track whether the key was recognised by
		// checking Extensions before and after.
		extBefore := len(mb.Extensions)
		applyMetaKey(mb, key, val)
		extAfter := len(mb.Extensions)
		if extAfter > extBefore {
			// Key was not a recognised constraint keyword — this is a
			// real sample item, not a Meta block.
			return nil
		}
	}
	if mb.isEmpty() {
		return nil
	}
	return mb
}

// schemaDescFlags holds schema boolean flags extracted from description
// prefixes. See parseSchemaDescPrefix.
type schemaDescFlags struct {
	Deprecated bool
	ReadOnly   bool
	WriteOnly  bool
}

// parseSchemaDescPrefix scans the leading bracket tokens of a MSON member
// description and extracts recognised schema flags. Recognised tokens
// (case-insensitive):
//
//	[deprecated]            → Deprecated: true
//	[readOnly] / [read-only] → ReadOnly: true
//	[writeOnly] / [write-only] → WriteOnly: true
//
// Multiple prefixes may appear in any order separated by whitespace:
//
//	"[readOnly] [deprecated] Unique identifier."
//
// The returned string has all recognised prefixes stripped and leading
// whitespace trimmed. An unrecognised bracket token stops scanning —
// it is left in the returned string so it is visible in the description.
// This is the reliable description-prefix alternative for contexts where
// Drafter drops `+ Meta` blocks or unknown type attributes (e.g. inside
// `### Properties` sections).
func parseSchemaDescPrefix(s string) (string, schemaDescFlags) {
	var flags schemaDescFlags
	for {
		t := strings.TrimLeft(s, " \t")
		if !strings.HasPrefix(t, "[") {
			break
		}
		end := strings.Index(t, "]")
		if end < 0 {
			break
		}
		word := strings.ToLower(t[1:end])
		switch word {
		case "deprecated":
			flags.Deprecated = true
		case "readonly", "read-only":
			flags.ReadOnly = true
		case "writeonly", "write-only":
			flags.WriteOnly = true
		default:
			// Unrecognised bracket token — stop scanning, leave in string.
			return strings.TrimLeft(s, " \t"), flags
		}
		s = strings.TrimLeft(t[end+1:], " \t")
	}
	return s, flags
}

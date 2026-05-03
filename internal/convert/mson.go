package convert

import (
	"encoding/json"
	"regexp"

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
		return &oas.Schema{
			Type:        msonTypeToOAS(el.Element),
			Description: el.description(),
		}
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
	s := &oas.Schema{
		Type:        "object",
		Description: el.description(),
	}
	props := map[string]*oas.Schema{}
	var required []string
	for _, c := range el.contentArray() {
		if c.Element != "member" {
			continue
		}
		m := decodeMember(&c)
		if m == nil {
			continue
		}
		name := m.Content.Key.Content
		if name == "" {
			continue
		}
		valSchema := r.schemaForVisited(&m.Content.Value, visited)
		if valSchema == nil {
			valSchema = &oas.Schema{Type: "string"}
		}
		// Member-level description (from `- name (type) - description`)
		// trumps any inherited from the value type.
		if d := m.Meta.Description.Content; d != "" {
			valSchema.Description = d
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
		if valSchema.Format == "" {
			if f := inferFormat(valSchema); f != "" {
				valSchema.Format = f
			}
		}
		props[name] = valSchema
		for _, ta := range m.Attributes.TypeAttributes.Content {
			if ta.Content == "required" {
				required = append(required, name)
				break
			}
		}
	}
	if len(props) > 0 {
		s.Properties = props
	}
	if len(required) > 0 {
		s.Required = required
	}
	// Whole-object type attributes (rare but valid) - mostly "fixed-type",
	// which translates to `additionalProperties: false`.
	applyTypeAttributes(s, el.Attributes.TypeAttributes)
	return s
}

// arraySchema converts an `array` element. The first child element (if any)
// describes the item type; with no children we fall back to `string` items.
func (r *schemaResolver) arraySchema(el *element, visited map[string]bool) *oas.Schema {
	children := el.contentArray()
	var items *oas.Schema
	if len(children) > 0 {
		items = r.schemaForVisited(&children[0], visited)
	}
	if items == nil {
		items = &oas.Schema{Type: "string"}
	}
	return &oas.Schema{
		Type:        "array",
		Description: el.description(),
		Items:       items,
	}
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
			if s.Example != nil && len(s.Enum) == 0 {
				s.Enum = []any{s.Example}
			}
		case "fixed-type":
			if s.Type == "object" {
				s.AdditionalProperties = false
			}
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

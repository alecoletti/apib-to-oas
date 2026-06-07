// Package convert maps a Drafter Refract / API Elements JSON AST into an
// OpenAPI 3.0 document.
//
// The conversion pipeline is split into stages:
//
//  1. Decode    - parse the Refract JSON into typed Go structs (refract.go).
//  2. Translate - walk the API Elements tree, producing OAS model nodes
//     (this file).
//  3. Marshal   - serialise the OAS model to YAML or JSON (marshal.go).
//
// Stage 2 must remain pure (no I/O) so it stays easy to test in.
package convert

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/alecoletti/apib-to-oas/internal/oas"
)

// RefractToOAS converts a Drafter JSON document into an OAS 3.0 document.
//
// Use RefractToOASWithOptions for OAS 3.1 output or other knobs.
func RefractToOAS(refract []byte) (*oas.Document, error) {
	return RefractToOASWithOptions(refract, Options{})
}

// Options controls non-default conversion behaviour.
type Options struct {
	// OASVersion overrides the document's `openapi` field. Accepted forms:
	//   - "" (default)        → "3.0.3"
	//   - "3.0" / "3.0.3"     → "3.0.3"
	//   - "3.1" / "3.1.0"     → "3.1.0" + jsonSchemaDialect
	//   - "3.2" / "3.2.0"     → "3.2.0" + jsonSchemaDialect + hierarchical tags
	//   - any other string    → emitted verbatim (advanced use)
	OASVersion string

	// InfoVersion, when non-empty, overrides `info.version` after the
	// converter has populated it from the APIB metadata. Use this to
	// stamp the API version from CI or a build script without editing
	// the source blueprint.
	InfoVersion string

	// Security, when non-nil, populates components.securitySchemes,
	// document-level security and per-operation overrides. Loaded from
	// a sidecar JSON config via LoadSecurityConfig.
	Security *SecurityConfig

	// Diagnostics, when non-nil, collects converter-emitted warnings
	// and errors with stable Blueprint+ codes (E001-E006, W001-W006 -
	// see specs/apib+.md §15). Drafter's own parse diagnostics flow
	// separately through ExtractAnnotations.
	Diagnostics *Diagnostics
}

// Annotation is a parser diagnostic surfaced from the Drafter AST or
// emitted by the Blueprint+ converter. `Severity` is the first class on
// the annotation element ("warning", "error", "note"). `Code` is
// Drafter's numeric diagnostic code (0 when the annotation comes from
// the converter). `StableCode` is the Blueprint+ catalogue code (e.g.
// "E003", "W002") and is non-empty only for converter-emitted entries.
type Annotation struct {
	Severity   string
	Code       int
	StableCode string
	Message    string
	Line       int
	Column     int
}

// HasErrors reports whether at least one annotation has severity "error".
func HasErrors(anns []Annotation) bool {
	for _, a := range anns {
		if a.Severity == "error" {
			return true
		}
	}
	return false
}

// ExtractAnnotations decodes the parser diagnostics emitted by Drafter
// alongside the API document. Returns nil on malformed input rather than
// erroring, since callers will already be processing the document via
// RefractToOAS and a separate failure here would mask the real problem.
//
// Diagnostics that Blueprint+ explicitly handles (multiple `HOST:` /
// `SERVER:` per §2.2; `[header]` / `[cookie]` description prefixes per
// §6.2 that trigger Drafter's "parameter not in URI template" warning)
// are suppressed so the user doesn't see noise about features the spec
// supports.
func ExtractAnnotations(refract []byte) []Annotation {
	if len(refract) == 0 {
		return nil
	}
	var root parseResult
	if err := json.Unmarshal(refract, &root); err != nil {
		return nil
	}
	raw := root.annotations()
	if len(raw) == 0 {
		return nil
	}
	headerCookieParams := collectLocationOverrideNames(&root)
	out := make([]Annotation, 0, len(raw))
	for _, a := range raw {
		code := int(a.Attributes.Code.Content)
		msg := a.contentString()
		if isSuppressedDrafterAnnotation(code, msg, headerCookieParams) {
			continue
		}
		out = append(out, Annotation{
			Severity: a.severity(),
			Code:     code,
			Message:  msg,
			Line:     int(a.Attributes.Line.Content),
			Column:   int(a.Attributes.Column.Content),
		})
	}
	return out
}

// isSuppressedDrafterAnnotation reports whether a Drafter diagnostic
// should be hidden because Blueprint+ handles the construct natively.
//
//   - code 2 (duplicate metadata) for `HOST` / `SERVER` -> §2.2 allows
//     multiple entries.
//   - code 8 (parameter not in URI template) for any name that carries
//     a `[header]` / `[cookie]` description prefix -> §6.2 overrides
//     the URI-template inference for these.
func isSuppressedDrafterAnnotation(code int, msg string, headerCookie map[string]bool) bool {
	switch code {
	case 2:
		// "duplicate definition of 'HOST'" / "'SERVER'"
		if strings.Contains(msg, "'HOST'") || strings.Contains(msg, "'SERVER'") {
			return true
		}
	case 8:
		for name := range headerCookie {
			if strings.Contains(msg, "'"+name+"'") {
				return true
			}
		}
	}
	return false
}

// collectLocationOverrideNames scans the AST for parameters whose
// description starts with a `[header]` or `[cookie]` prefix - these
// don't belong in the URI template, so the Drafter "not in template"
// warning for them is spurious.
func collectLocationOverrideNames(root *parseResult) map[string]bool {
	names := map[string]bool{}
	var walk func(*element)
	walk = func(e *element) {
		if e == nil {
			return
		}
		for _, m := range e.Attributes.HrefVariables.Content {
			if loc, _, _ := parseLocationPrefix(m.Meta.Description.Content); loc != "" {
				if k := m.Content.Key.Content; k != "" {
					names[k] = true
				}
			}
		}
		for _, c := range e.contentArray() {
			c := c
			walk(&c)
		}
	}
	for i := range root.Content {
		walk(&root.Content[i])
	}
	return names
}

// RefractToOASWithOptions is the main entry point. It walks the API
// Elements tree once, producing a typed OAS document, then runs
// post-processing steps (parameter promotion + anchor assignment) so the
// marshaller can emit shared parameters as YAML anchors.
func RefractToOASWithOptions(refract []byte, opts Options) (*oas.Document, error) {
	doc := oas.NewDocument()
	applyVersion(doc, opts.OASVersion)
	if len(refract) == 0 {
		return doc, nil
	}

	var root parseResult
	if err := json.Unmarshal(refract, &root); err != nil {
		return nil, fmt.Errorf("decode refract: %w", err)
	}

	cat := root.firstCategory()
	if cat == nil {
		return doc, nil
	}

	if t := cat.Meta.Title.Content; t != "" {
		doc.Info.Title = t
	}
	if v := cat.Attributes.Version.Content; v != "" {
		doc.Info.Version = v
	}
	// API Blueprint has no native version field; many specs declare it
	// informally as a metadata key (`VERSION:`, `API-VERSION:`, `API_VERSION:`).
	// Promote whichever is present so info.version isn't always the default.
	if vs := metadataValuesAll(cat, "VERSION", "API-VERSION", "API_VERSION"); len(vs) > 0 {
		// Last non-empty wins (Blueprint+ §5.1).
		for i := len(vs) - 1; i >= 0; i-- {
			if v := strings.TrimSpace(vs[i]); v != "" {
				doc.Info.Version = v
				break
			}
		}
		if len(vs) > 1 {
			opts.Diagnostics.Warn(CodeMultipleVersion,
				fmt.Sprintf("multiple VERSION entries (%d); last non-empty wins", len(vs)))
		}
	}
	// CLI / programmatic override always wins.
	if opts.InfoVersion != "" {
		doc.Info.Version = opts.InfoVersion
	}
	if servers := parseServersMetadata(cat, doc.OpenAPI); len(servers) > 0 {
		doc.Servers = servers
	}
	// Blueprint+ Tier-A §5.5 (extended): SUMMARY: → info.summary,
	// LICENSE: → info.license.name, LICENSE-ID: → info.license.identifier
	// (3.1+ SPDX), LICENSE-URL: → info.license.url. The `summary:` and
	// `license.identifier:` fields are 3.1+ only - we still populate
	// them on 3.0 because they're harmless and cheaply ignored, but
	// strict 3.0 validators may flag them.
	applyInfoLicenseAndSummary(doc, cat)
	// Blueprint+ Tier-A §5.4 / §12.2: a `SECURITY:` document metadata
	// becomes the document-level default `security` array. Comma-
	// separated scheme names; an empty value clears any default. The
	// security sidecar (Options.Security) overrides this when both
	// are supplied.
	if sec, ok := parseDocumentSecurity(cat); ok {
		doc.Security = sec
	}
	// Blueprint+ Tier-A: any uppercase metadata key NOT in the recognised
	// set (FORMAT, VERSION, API-VERSION, API_VERSION, HOST, SERVER,
	// WEBHOOK_GROUPS, SECURITY) becomes `info.x-*` (or `x-*` at the root
	// when prefixed with `ROOT.`). See specs/apib+.md "Document metadata"
	// and "Extensions".
	applyDocumentExtensions(doc, cat, opts.Diagnostics)

	// Build the registry of MSON named types once. The resolver is used
	// in two flavours:
	//
	//   * `resolver` (refs ON, via withRefs) - drives the walk, so any
	//     reference to a registered named type inside an operation
	//     emits `$ref: '#/components/schemas/<Name>'` instead of
	//     re-inlining the full definition. This keeps operations small
	//     and lets docs renderers cross-link to the schema section.
	//   * the same resolver is reused below (still refs ON) when
	//     populating components.schemas - nested references between
	//     named types stay as $ref there too, which is exactly what the
	//     OAS components section is for.
	//
	// Inline (anonymous) MSON definitions still inline; only *named*
	// types in the registry switch to refs. Cycles remain guarded.
	resolver := newSchemaResolver(root.dataStructures()).withRefs().withDiagnostics(opts.Diagnostics)

	var descParts []string
	collectInfoCopy(cat, &descParts)
	if len(descParts) > 0 {
		doc.Info.Description = strings.Join(descParts, "\n\n")
	}

	// Walk the entire category tree (including nested resourceGroup
	// categories) so every resource ends up under doc.Paths. Track the
	// nearest enclosing group title so it can be applied as an OAS tag
	// to each operation. For OAS 3.2 we also remember the parent group
	// title so doc.Tags can carry parent: relationships. Webhook groups
	// (declared via the WEBHOOK_GROUPS document metadata) route their
	// resources into doc.Webhooks instead of doc.Paths when --oas-version
	// is 3.1 or 3.2.
	tagInfo := newTagRegistry()
	webhookGroups := parseWebhookGroups(cat.metadataValue("WEBHOOK_GROUPS"))
	wantWebhooks := isOAS31OrLater(doc.OpenAPI)
	if len(webhookGroups) > 0 && !wantWebhooks {
		names := make([]string, 0, len(webhookGroups))
		for n := range webhookGroups {
			names = append(names, n)
		}
		opts.Diagnostics.Warn(CodeWebhookOnOAS30,
			fmt.Sprintf("WEBHOOK_GROUPS declared (%s) but target is OAS %s; webhook routing requires 3.1+", strings.Join(names, ", "), doc.OpenAPI))
	}
	walkResources(cat, walkCtx{
		tag:                 "",
		parentTag:           "",
		isWebhook:           false,
		webhookGroups:       webhookGroups,
		wantWebhooks:        wantWebhooks,
		wantResponseSummary: strings.HasPrefix(doc.OpenAPI, "3.2"),
		diag:                opts.Diagnostics,
	}, doc, resolver, tagInfo)

	// Populate components.schemas from the MSON named-type registry, in a
	// stable alphabetical order. Schemas in this section cross-reference
	// each other via `$ref` instead of inlining, so the document stays
	// readable at scale.
	populateComponentsFromRegistry(doc, resolver, opts.Diagnostics)

	// Emit document-level tags (preserving the order they were first seen
	// while walking) so consumers see the same grouping that's reflected
	// on each operation. For OAS 3.2 we also propagate parent: links and
	// the optional `kind:` (nav | badge | audience) so nested resource
	// groups render as a hierarchy with the right semantic role.
	emit32 := strings.HasPrefix(doc.OpenAPI, "3.2")
	for _, name := range tagInfo.order {
		t := oas.Tag{Name: name, Description: tagInfo.descriptions[name]}
		if emit32 {
			t.Summary = tagInfo.summaries[name]
			t.Parent = tagInfo.parents[name]
			t.Kind = tagInfo.kinds[name]
		}
		if ext := tagInfo.extensions[name]; len(ext) > 0 {
			t.Extensions = ext
		}
		doc.Tags = append(doc.Tags, t)
	}

	// Promote path-level parameters down to each operation that lives
	// under that path, sharing the *same* pointer. After this step,
	// assignAnchors will see those shared pointers and tag the first
	// occurrence with an AnchorID + the rest with an AliasID, so the
	// YAML marshaller emits `&ref_N` / `*ref_N` instead of duplicating
	// the parameter body.
	promotePathParams(doc)
	assignAnchors(doc)

	// Apply the optional security sidecar last so it sees fully populated
	// paths/operations (overrides match by tag / path / operationId).
	if opts.Security != nil {
		applySecurity(doc, opts.Security)
	}

	// Blueprint+ §15 W007: warn when any referenced security scheme is
	// not declared in components.securitySchemes. Runs *after* both the
	// MSON promoter and the sidecar so an author who declares schemes
	// in either place doesn't get a false positive.
	checkUndeclaredSecuritySchemes(doc, opts.Diagnostics)

	// Blueprint+ §15 E005: report duplicate operationIds. We never
	// rename or drop them - last write into the path slot wins
	// structurally; the diagnostic gives authors a chance to fix it.
	checkDuplicateOperationIDs(doc, opts.Diagnostics)

	// In OAS 3.0, sibling keywords next to `$ref` are silently ignored by
	// validators. Wrap any such schemas in `allOf` so descriptions /
	// examples / nullable / etc. survive. OAS 3.1+ allows siblings; leave
	// those alone.
	if strings.HasPrefix(doc.OpenAPI, "3.0") {
		normalizeRefSiblings(doc)
	}

	// OAS 3.1+ removed `nullable: true`. Translate it to the JSON
	// Schema 2020-12 idiom `type: ["<orig>", "null"]` so output passes
	// strict 3.1 / 3.2 validators (Spectral, Redocly CLI, etc.).
	if isOAS31OrLater(doc.OpenAPI) {
		normalizeNullableForJSONSchema(doc)
	}

	// OAS 3.1+ deprecated the singular `example` keyword in Schema Objects
	// in favour of the JSON Schema 2020-12 `examples` array. Promote it so
	// strict 3.1 / 3.2 validators don't warn about the deprecated field.
	if isOAS31OrLater(doc.OpenAPI) {
		normalizeExamplesForJSONSchema(doc)
	}

	// OAS 3.0 doesn't have the `const` keyword (JSON Schema draft-04).
	// Fall back to `enum: [value]` so 3.0 validators still accept it.
	if strings.HasPrefix(doc.OpenAPI, "3.0") {
		normalizeConstForOAS30(doc)
	}

	return doc, nil
}

// populateComponentsFromRegistry builds components.schemas and
// components.securitySchemes from the MSON named-type registry. Extracted from
// RefractToOASWithOptions to keep that function's cyclomatic complexity within
// the project limit.
func populateComponentsFromRegistry(doc *oas.Document, resolver *schemaResolver, diag *Diagnostics) {
	reg := resolver.registry
	if len(reg) == 0 {
		return
	}
	// Blueprint+ Tier-A §12.1: promote a reserved
	// `## SecuritySchemes (object)` named type into
	// components.securitySchemes. The reserved name is removed from the
	// schemas map below so it doesn't double-appear.
	mson, suppress := extractMSONSecuritySchemes(reg, diag)
	if len(mson) > 0 {
		if doc.Components == nil {
			doc.Components = &oas.Components{}
		}
		if doc.Components.SecuritySchemes == nil {
			doc.Components.SecuritySchemes = map[string]*oas.SecurityScheme{}
		}
		for n, s := range mson {
			doc.Components.SecuritySchemes[n] = s
		}
	}
	schemas := map[string]*oas.Schema{}
	for name, def := range reg {
		if suppress != "" && name == suppress {
			continue
		}
		// Registry already stores the inner typed element (e.g. the
		// "object" node) - no need to unwrap a dataStructure shell.
		// resolver is already in refs mode, so nested references between
		// named types stay as $ref here too.
		if s := resolver.schemaFor(def); s != nil {
			schemas[name] = s
		}
	}
	if len(schemas) > 0 {
		if doc.Components == nil {
			doc.Components = &oas.Components{}
		}
		doc.Components.Schemas = schemas
	}
}

// normalizeNullableForJSONSchema converts every `Nullable: true` schema
// to the OAS 3.1+ multi-type form (`type: ["<orig>", "null"]`). Applied
// only when the target is 3.1 or 3.2; on 3.0 we keep `nullable: true`.
//
// Edge cases:
//   - Nullable + Ref != ""  → wraps into `oneOf: [{$ref: …}, {type: "null"}]`.
//     Placing type alongside $ref is invalid in OAS 3.1 JSON Schema 2020-12
//     because $ref is no longer a resolving-only keyword; a sibling
//     `type: ["null"]` would override the referenced schema and make the
//     property accept only null.
//   - Nullable + Type != "" → emits `type: ["<orig>", "null"]`.
//   - Nullable + Type == "" → emits `type: ["null"]` alone.
func normalizeNullableForJSONSchema(doc *oas.Document) {
	walkSchemas(doc, func(s *oas.Schema) {
		if s == nil || !s.Nullable {
			return
		}
		switch {
		case s.Ref != "":
			// $ref + nullable → oneOf: [$ref, {type: "null"}]
			// Preserve description; clear all other sibling fields so
			// no invalid keywords sit next to the oneOf.
			ref := s.Ref
			desc := s.Description
			*s = oas.Schema{
				OneOf: []*oas.Schema{
					{Ref: ref},
					{Type: "null"},
				},
				Description: desc,
			}
		case s.Type != "":
			s.TypeList = []string{s.Type, "null"}
			s.Nullable = false
		default:
			s.TypeList = []string{"null"}
			s.Nullable = false
		}
	})
}

// normalizeExamplesForJSONSchema promotes schema.Example (singular,
// deprecated in OAS 3.1+) to schema.Examples (array, JSON Schema 2020-12).
// Applied after the nullable walk so TypeList-promoted schemas benefit too.
// Only runs when the target is 3.1 or 3.2.
func normalizeExamplesForJSONSchema(doc *oas.Document) {
	walkSchemas(doc, func(s *oas.Schema) {
		if s == nil || s.Example == nil || len(s.Examples) > 0 {
			return
		}
		s.Examples = []any{s.Example}
		s.Example = nil
	})
}

// normalizeConstForOAS30 translates schema.Const to schema.Enum: [value]
// when the target is OAS 3.0, which uses JSON Schema draft-04 and does not
// have the `const` keyword. When Enum is already set, Const is simply
// dropped (the enum provides equivalent validation semantics).
func normalizeConstForOAS30(doc *oas.Document) {
	walkSchemas(doc, func(s *oas.Schema) {
		if s == nil || s.Const == nil {
			return
		}
		if len(s.Enum) == 0 {
			s.Enum = []any{s.Const}
		}
		s.Const = nil
	})
}

// applyVersion normalises an OAS version string and sets it on doc.
// 3.1 / 3.2 we also emit jsonSchemaDialect so consumers know which schema
// vocabulary the components were built against. (3.2 reuses 3.1's
// dialect - the 3.2 release notes are explicit about that.)
func applyVersion(doc *oas.Document, v string) {
	switch strings.TrimSpace(v) {
	case "", "3.0", "3.0.3":
		doc.OpenAPI = "3.0.3"
	case "3.1", "3.1.0":
		doc.OpenAPI = "3.1.0"
		doc.JSONSchemaDialect = "https://spec.openapis.org/oas/3.1/dialect/base"
	case "3.2", "3.2.0":
		doc.OpenAPI = "3.2.0"
		doc.JSONSchemaDialect = "https://spec.openapis.org/oas/3.1/dialect/base"
	default:
		doc.OpenAPI = v
	}
}

// promotePathParams copies each PathItem's Parameters into every operation
// under that path, sharing the underlying *Parameter pointer. The path-
// level slice is then cleared. This sets up parameter sharing for YAML
// anchor emission.
func promotePathParams(doc *oas.Document) {
	for _, pi := range doc.Paths {
		if len(pi.Parameters) == 0 {
			continue
		}
		for _, op := range pathOperations(pi) {
			for _, p := range pi.Parameters {
				if !hasParam(op.Parameters, p) {
					op.Parameters = append(op.Parameters, p)
				}
			}
		}
		pi.Parameters = nil
	}
}

// checkDuplicateOperationIDs scans every operation in doc.Paths and
// emits Blueprint+ E005 (or W005 in Permissive mode - currently always
// W005 since we don't expose Strict/Permissive at this layer) for each
// operationId seen on more than one operation. Sites are reported in a
// stable "path:method, path:method" form so the message diffs cleanly.
//
// Webhooks share the same operationId namespace as paths.
func checkDuplicateOperationIDs(doc *oas.Document, diag *Diagnostics) {
	if diag == nil {
		return
	}
	type site struct{ path, method string }
	seen := map[string][]site{}
	collect := func(scope map[string]*oas.PathItem) {
		for path, pi := range scope {
			for method, op := range methodMap(pi) {
				if op == nil || op.OperationID == "" {
					continue
				}
				seen[op.OperationID] = append(seen[op.OperationID], site{path, method})
			}
		}
	}
	collect(doc.Paths)
	collect(doc.Webhooks)
	// Sort the operationIds we emit for so the diagnostic order is
	// stable across runs (Go map iteration is random).
	dupKeys := make([]string, 0)
	for id, sites := range seen {
		if len(sites) > 1 {
			dupKeys = append(dupKeys, id)
		}
	}
	sortStringsConvert(dupKeys)
	for _, id := range dupKeys {
		sites := seen[id]
		// Stable site order too.
		siteStrs := make([]string, len(sites))
		for i, s := range sites {
			siteStrs[i] = s.path + ":" + s.method
		}
		sortStringsConvert(siteStrs)
		diag.Warn(CodeDuplicateOperation,
			"duplicate operationId '"+id+"' on "+strings.Join(siteStrs, ", "))
	}
}

// methodMap returns all non-nil operations on pi keyed by lowercase
// method name. Used by checkDuplicateOperationIDs to enumerate
// operations along with their HTTP method.
func methodMap(pi *oas.PathItem) map[string]*oas.Operation {
	out := map[string]*oas.Operation{}
	if pi.Get != nil {
		out["get"] = pi.Get
	}
	if pi.Put != nil {
		out["put"] = pi.Put
	}
	if pi.Post != nil {
		out["post"] = pi.Post
	}
	if pi.Delete != nil {
		out["delete"] = pi.Delete
	}
	if pi.Patch != nil {
		out["patch"] = pi.Patch
	}
	if pi.Options != nil {
		out["options"] = pi.Options
	}
	if pi.Head != nil {
		out["head"] = pi.Head
	}
	return out
}

// sortStringsConvert is a tiny insertion sort kept private to this
// package so we don't pull `sort` into the import set just for one
// call site.
func sortStringsConvert(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// pathOperations returns every non-nil Operation defined on pi.
func pathOperations(pi *oas.PathItem) []*oas.Operation {
	var out []*oas.Operation
	for _, op := range []*oas.Operation{pi.Get, pi.Put, pi.Post, pi.Delete, pi.Patch, pi.Options, pi.Head} {
		if op != nil {
			out = append(out, op)
		}
	}
	return out
}

// hasParam reports whether ps already contains the exact pointer p.
func hasParam(ps []*oas.Parameter, p *oas.Parameter) bool {
	for _, x := range ps {
		if x == p {
			return true
		}
	}
	return false
}

// assignAnchors walks the document, counting occurrences of each
// *Parameter pointer. Any pointer seen more than once gets a unique
// `ref_N` anchor on its first occurrence and an alias on the rest.
func assignAnchors(doc *oas.Document) {
	count := map[*oas.Parameter]int{}
	for _, pi := range doc.Paths {
		for _, op := range pathOperations(pi) {
			for _, p := range op.Parameters {
				count[p]++
			}
		}
	}
	idx := 0
	assigned := map[*oas.Parameter]string{}
	// Walk again in deterministic-ish order: range over Paths is random in
	// Go but anchor IDs only need to be unique + stable per render, which
	// they are because we run the same pass per Marshal.
	for _, pi := range doc.Paths {
		for _, op := range pathOperations(pi) {
			for _, p := range op.Parameters {
				if count[p] < 2 {
					continue
				}
				if id, ok := assigned[p]; ok {
					// Convert subsequent references into a sibling alias
					// node so MarshalJSON emits the alias sentinel only.
					alias := *p
					alias.AnchorID = ""
					alias.AliasID = id
					// Replace in the slice.
					for i, q := range op.Parameters {
						if q == p {
							op.Parameters[i] = &alias
						}
					}
					continue
				}
				id := fmt.Sprintf("ref_%d", idx)
				idx++
				p.AnchorID = id
				assigned[p] = id
			}
		}
	}
}

// parseServersMetadata gathers every HOST/SERVER metadata entry from cat
// and converts each to an oas.Server. APIB only formally defines a single
// `HOST:` key, but real-world specs often want to advertise multiple
// environments. We extend the convention so any of:
//
//	HOST: https://api.example.com
//	HOST: https://staging.example.com - Staging
//	HOST: https://sandbox.example.com - Sandbox | sandbox
//
// produce one Server entry each, in source order. The optional description
// is whatever follows the first " - " separator (space-dash-space) so URLs
// containing dashes (almost all of them) round-trip cleanly. An optional
// ` | name` suffix after the description sets server.name (OAS 3.2+).
func parseServersMetadata(cat *element, oasVersion string) []oas.Server {
	raws := metadataValuesAll(cat, "HOST", "SERVER")
	if len(raws) == 0 {
		return nil
	}
	emit32 := strings.HasPrefix(oasVersion, "3.2")
	out := make([]oas.Server, 0, len(raws))
	for _, raw := range raws {
		url, desc, name := splitServerEntry3(raw)
		if url == "" {
			continue
		}
		s := oas.Server{URL: url, Description: desc}
		if emit32 && name != "" {
			s.Name = name
		}
		out = append(out, s)
	}
	return out
}

// splitServerEntry splits "url - description | name" into its three parts.
// The " - " separator (space-dash-space) divides URL from description; a
// subsequent " | " separator divides description from the optional name
// (OAS 3.2+ server.name). Missing parts return "".
func splitServerEntry3(raw string) (url, desc, name string) {
	raw = strings.TrimSpace(raw)
	if i := strings.Index(raw, " - "); i >= 0 {
		url = strings.TrimSpace(raw[:i])
		rest := strings.TrimSpace(raw[i+3:])
		if j := strings.Index(rest, " | "); j >= 0 {
			desc = strings.TrimSpace(rest[:j])
			name = strings.TrimSpace(rest[j+3:])
		} else {
			desc = rest
		}
		return url, desc, name
	}
	return raw, "", ""
}

// splitServerEntry splits "url - description" into (url, description),
// using " - " as the separator so dashes inside URLs survive. Trailing /
// leading whitespace is trimmed on both sides. Missing description → "".
// Kept for backward compatibility (used by applyMetaKey for Docs: parsing).
func splitServerEntry(raw string) (string, string) {
	u, d, _ := splitServerEntry3(raw)
	return u, d
}

// applyInfoLicenseAndSummary reads SUMMARY / LICENSE / LICENSE-ID /
// LICENSE-URL document metadata into doc.Info. The license object is
// populated lazily so a doc with no license metadata stays unaffected.
//
//	SUMMARY:     Short tagline shown next to the title.
//	LICENSE:     Apache 2.0 License        ; → license.name (always)
//	LICENSE-ID:  Apache-2.0                ; → license.identifier (3.1+)
//	LICENSE-URL: https://opensource.org/…  ; → license.url
//
// LICENSE-ID and LICENSE-URL are mutually exclusive in OAS 3.1+; if
// both are present we keep both (per spec it's a validator concern).
func applyInfoLicenseAndSummary(doc *oas.Document, cat *element) {
	if s := strings.TrimSpace(firstMetadataValue(cat, "SUMMARY")); s != "" {
		doc.Info.Summary = s
	}
	name := strings.TrimSpace(firstMetadataValue(cat, "LICENSE"))
	id := strings.TrimSpace(firstMetadataValue(cat, "LICENSE-ID"))
	url := strings.TrimSpace(firstMetadataValue(cat, "LICENSE-URL"))
	if name == "" && id == "" && url == "" {
		return
	}
	lic := &oas.License{Name: name, Identifier: id, URL: url}
	// Name is required by OAS - synthesise something sensible if only
	// identifier/url were supplied.
	if lic.Name == "" {
		switch {
		case id != "":
			lic.Name = id
		default:
			lic.Name = "License"
		}
	}
	doc.Info.License = lic
}

// parseDocumentSecurity reads the `SECURITY:` document metadata (Blueprint+
// §5.4 / §12.2) and converts it to OAS document-level security. Returns
// (nil, true) when an empty SECURITY: clears auth, (req, true) for a
// non-empty list, and (nil, false) when no SECURITY: metadata is present
// (callers should not touch doc.Security in that case).
func parseDocumentSecurity(cat *element) ([]oas.SecurityRequirement, bool) {
	raw, present := metadataValueIfPresent(cat, "SECURITY")
	if !present {
		return nil, false
	}
	var out []oas.SecurityRequirement
	for _, name := range strings.Split(raw, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		out = append(out, oas.SecurityRequirement{name: []string{}})
	}
	return out, true
}

// collectInfoCopy gathers the top-level `copy` blocks directly under the
// API category so they form the document description. Copy elements that
// live deeper (under resourceGroup categories, transitions, etc.) are
// folded into their respective owners instead. Embedded `+ Meta` blocks
// (Blueprint+ §10) are stripped via proseFromCopyText so they don't
// leak into the description.
func collectInfoCopy(cat *element, out *[]string) {
	for _, c := range cat.contentArray() {
		if c.Element != "copy" {
			continue
		}
		if s := proseFromCopyText(c.contentString()); s != "" {
			*out = append(*out, s)
		}
	}
}

// collectCategoryCopy returns the joined `copy` prose declared directly
// under cat (i.e. immediate children only, not recursed into nested
// categories or resources). Used to populate `tags[].description` for
// resource-group categories whose intro spans multiple paragraphs and
// subheaded sections - Drafter parses each markdown block as its own
// `copy` element.
//
// `consumed` (when non-nil) names indices of copy elements to skip
// entirely (their whole content was a `+ Meta` block). Surviving
// copies are passed through proseFromCopyText so any meta block fused
// with prose is also stripped.
func collectCategoryCopy(cat *element, consumed map[int]bool) string {
	var parts []string
	for i, c := range cat.contentArray() {
		if c.Element != "copy" {
			continue
		}
		if consumed[i] {
			continue
		}
		if s := proseFromCopyText(c.contentString()); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n\n")
}

// walkCtx carries everything the recursive walker needs to know about the
// surrounding category context: the nearest enclosing tag (resource group
// title), its own parent tag (for OAS 3.2 hierarchical tags), the joined
// prose declared on the group's category (for `tags[].description`), the
// `x-*` extensions from a group-level `+ Meta` block, whether the current
// subtree is webhook-flagged, the configured webhook detection settings,
// and an optional diagnostics collector for stable Blueprint+ codes
// emitted while walking.
type walkCtx struct {
	tag                 string
	parentTag           string
	tagSummary          string // OAS 3.2+ tags[].summary, from + Meta Summary: key
	tagDesc             string
	tagExt              map[string]any
	tagKind             string
	isWebhook           bool
	webhookGroups       map[string]bool
	wantWebhooks        bool
	wantResponseSummary bool // OAS 3.2+ - split inline title into responses[*].summary
	diag                *Diagnostics
}

// tagRegistry preserves both the discovery order of tags and their parent
// relationships (3.2). Parents may be empty when a group sits at the root.
// Descriptions are the joined `copy` blocks declared directly under the
// group's category - they map to OAS `tags[].description`. Extensions
// hold the `x-*` keys absorbed from a group-level `+ Meta` block.
type tagRegistry struct {
	order        []string
	seen         map[string]bool
	parents      map[string]string
	summaries    map[string]string // OAS 3.2+ tags[].summary
	descriptions map[string]string
	kinds        map[string]string
	extensions   map[string]map[string]any
}

func newTagRegistry() *tagRegistry {
	return &tagRegistry{
		seen:         map[string]bool{},
		parents:      map[string]string{},
		summaries:    map[string]string{},
		descriptions: map[string]string{},
		kinds:        map[string]string{},
		extensions:   map[string]map[string]any{},
	}
}

func (tr *tagRegistry) recordWithSummary(name, parent, summary, desc, kind string, ext map[string]any) {
	if !tr.seen[name] {
		tr.seen[name] = true
		tr.order = append(tr.order, name)
		if parent != "" {
			tr.parents[name] = parent
		}
	}
	if summary != "" && tr.summaries[name] == "" {
		tr.summaries[name] = summary
	}
	// Record description even when the tag was already seen - the first
	// non-empty wins. (A group's prose is declared on the category, so it
	// should arrive on the first resource we visit.)
	if desc != "" && tr.descriptions[name] == "" {
		tr.descriptions[name] = desc
	}
	if kind != "" && tr.kinds[name] == "" {
		tr.kinds[name] = kind
	}
	// Merge extensions; later entries don't clobber earlier ones.
	if len(ext) > 0 {
		bucket := tr.extensions[name]
		if bucket == nil {
			bucket = map[string]any{}
			tr.extensions[name] = bucket
		}
		for k, v := range ext {
			if _, exists := bucket[k]; !exists {
				bucket[k] = v
			}
		}
	}
}

// parseWebhookGroups parses a comma-separated list of group titles from
// the WEBHOOK_GROUPS document metadata. Returns an empty map when the
// metadata is absent, so a nil-safe lookup returns false.
func parseWebhookGroups(raw string) map[string]bool {
	out := map[string]bool{}
	for _, name := range strings.Split(raw, ",") {
		name = strings.TrimSpace(name)
		if name != "" {
			out[name] = true
		}
	}
	return out
}

func isOAS31OrLater(v string) bool {
	return strings.HasPrefix(v, "3.1") || strings.HasPrefix(v, "3.2")
}

// walkResources recursively descends into nested categories (resourceGroup
// in real specs) so every `resource` element is mapped to a path on doc
// (or onto doc.Webhooks when the enclosing group is flagged as a webhook
// group). Tag context flows down via walkCtx.
func walkResources(parent *element, ctx walkCtx, doc *oas.Document, resolver *schemaResolver, tags *tagRegistry) {
	for _, c := range parent.contentArray() {
		c := c
		switch c.Element {
		case "category":
			child := ctx
			if t := c.Meta.Title.Content; t != "" && hasClass(&c, "resourceGroup") {
				child.parentTag = ctx.tag
				child.tag = t
				// Pull a group-level + Meta block (if any) and remember
				// which copy children it consumed so they don't leak
				// into the tag description.
				meta, consumedMeta := extractMetaFromCategory(&c)
				child.tagDesc = collectCategoryCopy(&c, consumedMeta)
				child.tagKind = ""
				child.tagSummary = ""
				if meta != nil {
					if meta.Kind != "" {
						child.tagKind = meta.Kind
					}
					if meta.Summary != "" {
						child.tagSummary = meta.Summary
					}
					if len(meta.Extensions) > 0 {
						// Copy so siblings can't mutate it.
						ext := make(map[string]any, len(meta.Extensions))
						for k, v := range meta.Extensions {
							ext[k] = v
						}
						child.tagExt = ext
					} else {
						child.tagExt = nil
					}
				} else {
					child.tagExt = nil
				}
				if ctx.isWebhook || ctx.webhookGroups[t] || hasClass(&c, "webhook") {
					child.isWebhook = true
				}
			}
			walkResources(&c, child, doc, resolver, tags)
		case "resource":
			if ctx.tag != "" {
				tags.recordWithSummary(ctx.tag, ctx.parentTag, ctx.tagSummary, ctx.tagDesc, ctx.tagKind, ctx.tagExt)
			}
			addResource(&c, ctx, doc, resolver)
		}
	}
}

// hasClass reports whether el carries the given class string in
// meta.classes (e.g. "resourceGroup", "messageBody").
func hasClass(el *element, class string) bool {
	for _, cls := range el.Meta.Classes.Content {
		if cls.Content == class {
			return true
		}
	}
	return false
}

// addResource translates one resource element into a PathItem with its
// transitions / operations.
func addResource(res *element, ctx walkCtx, doc *oas.Document, resolver *schemaResolver) {
	rawHref := res.Attributes.Href.Content
	// Blueprint+ §15 E002 - surface malformed URI templates *before*
	// silent recovery in splitURITemplate swallows the bad bits.
	if rawHref != "" {
		for _, msg := range validateURITemplate(rawHref) {
			ctx.diag.Error(CodeMalformedURI, msg+" in "+rawHref)
		}
	}
	path, queryParams := splitURITemplate(rawHref)
	if path == "" {
		return
	}
	dest := doc.Paths
	if ctx.isWebhook && ctx.wantWebhooks {
		if doc.Webhooks == nil {
			doc.Webhooks = map[string]*oas.PathItem{}
		}
		dest = doc.Webhooks
	}
	pi, ok := dest[path]
	if !ok {
		pi = &oas.PathItem{}
		dest[path] = pi
	}

	// Blueprint+ §15 W006: Drafter normalises the legacy `# METHOD /path`
	// shorthand into a resource with no title containing exactly one
	// transition with a method. Detect that shape and warn so authors
	// migrate to the named-resource form.
	if ctx.diag != nil && res.Meta.Title.Content == "" {
		txs := 0
		var firstMethod string
		for _, rc := range res.contentArray() {
			if rc.Element != "transition" {
				continue
			}
			txs++
			if firstMethod == "" {
				if tr := rc.asTransition(); tr != nil {
					firstMethod = tr.method()
				}
			}
		}
		if txs == 1 && firstMethod != "" {
			ctx.diag.Warn(CodeDeprecatedSyntax,
				"deprecated `# "+firstMethod+" "+path+"` shorthand; use `## <Title> ["+path+"]` + `### <Action> ["+firstMethod+"]`")
		}
	}

	// Path parameters live on the PathItem because they're an intrinsic
	// part of the URL - every operation under this path shares them.
	// Query parameters, however, are *resource-scoped*: two APIB
	// resources can map to the same base path (e.g. `Search [/x{?q}]`
	// and `Create [/x]`) and only one of them declares the query
	// surface. Hoisting query params to the PathItem would leak
	// `?q=...` onto the unrelated POST. Keep them per-operation.
	//
	// `+ Parameters` declared on the resource may include both path
	// and query entries (location is per-name; see paramsFromHrefVariables).
	// We split them: path entries go on the PathItem, query entries
	// become this resource's `resourceQueryParams` and ride along with
	// every operation we create below. URI-template names not declared
	// in `+ Parameters` get a stub query parameter so they still appear
	// in the generated docs.
	resourceParams := paramsFromHrefVariables(res.Attributes.HrefVariables, "path", path, ctx.diag)
	declaredQuery := map[string]bool{}
	resourceQueryParams := []*oas.Parameter{}
	for _, p := range resourceParams {
		switch p.In {
		case "path":
			pi.Parameters = appendUniqueParam(pi.Parameters, p)
		case "query":
			declaredQuery[p.Name] = true
			resourceQueryParams = append(resourceQueryParams, p)
		default:
			// header / cookie declared at resource scope is unusual but
			// supported - attach to every operation in the resource.
			resourceQueryParams = append(resourceQueryParams, p)
		}
	}
	for _, qn := range queryParams {
		if declaredQuery[qn] {
			continue
		}
		resourceQueryParams = append(resourceQueryParams, &oas.Parameter{
			Name: qn, In: "query",
			Schema: &oas.Schema{Type: "string"},
		})
	}

	// Resource-level `+ Attributes` (sibling of the transitions) becomes
	// the default response schema for every action under the resource.
	resourceDefault := resolver.schemaFromDataStructureChild(res)

	// Blueprint+ Tier-A: extract a resource-level `+ Meta` block so any
	// `x-*` extensions land on the PathItem. Operation-level keys are
	// ignored at this scope (see applyMetaToPathItem).
	if resMeta, _ := extractMetaFromResource(res); resMeta != nil {
		applyMetaToPathItem(pi, resMeta)
	}

	for _, rc := range res.contentArray() {
		if rc.Element != "transition" {
			continue
		}
		tr := rc.asTransition()
		if tr == nil {
			continue
		}
		// Action-level `+ Attributes` (sibling of httpTransaction) becomes
		// the default request schema for every Request payload that
		// doesn't provide its own.
		actionDefault := resolver.schemaFromDataStructureChild(tr)
		op, meta := operationFromTransition(tr, resolver, resourceDefault, actionDefault, ctx.wantResponseSummary)
		method := tr.method()
		if method == "" {
			continue
		}
		// Blueprint+ §15 E001 - non-standard HTTP method. We still skip
		// the operation (downstream tools can't render an unknown verb)
		// but at least the author learns why.
		if !isStandardHTTPMethod(method) {
			ctx.diag.Error(CodeInvalidHTTPMethod, "non-standard HTTP method '"+method+"' on "+path)
			continue
		}
		if op.Summary == "" {
			op.Summary = tr.Meta.Title.Content
		}
		if op.OperationID == "" {
			op.OperationID = tr.Meta.Title.Content
		}
		if ctx.tag != "" {
			op.Tags = append(op.Tags, ctx.tag)
		}
		// Blueprint+ Tier-A: re-apply the `+ Meta` block AFTER the
		// inherited group tag is in place, so `Tags: +Beta` correctly
		// appends rather than replaces.
		applyMetaToOperation(op, meta, ctx.tag)

		// Process action-level params FIRST: they are the most
		// specific source, so when a name overlaps with a
		// URI-template stub or a resource-level entry, the richer
		// authoring (description / example / required) must win.
		// `appendUniqueParam` keeps the first occurrence, so the
		// inherited stubs added below silently become no-ops.
		for _, ap := range paramsFromHrefVariables(tr.Attributes.HrefVariables, "query", path, ctx.diag) {
			op.Parameters = appendUniqueParam(op.Parameters, ap)
		}
		// Attach this resource's query parameters to *this* operation
		// only - see the comment above where `resourceQueryParams` is
		// built.
		for _, qp := range resourceQueryParams {
			op.Parameters = appendUniqueParam(op.Parameters, qp)
		}
		pi.SetOperation(method, op)
	}
}

// operationFromTransition builds an OAS Operation from a transition
// element, extracting its description, request body and responses. Also
// returns the parsed Blueprint+ `+ Meta` block (if any) so the caller can
// re-apply it AFTER setting the inherited group tag - this makes the
// `Tags: +Beta` append-to-inherited semantics work correctly.
//
// wantResponseSummary, when true (OAS 3.2+), routes the inline
// `+ Response 200 - <title>` text to `responses[*].summary` instead of
// folding it into `description`. On older versions the title still goes
// into description as a fallback so we don't lose authoring intent.
func operationFromTransition(tr *element, resolver *schemaResolver, resourceDefault, actionDefault *oas.Schema, wantResponseSummary bool) (*oas.Operation, *metaBlock) {
	op := &oas.Operation{Summary: tr.Meta.Title.Content}
	var descParts []string

	meta, consumedMeta := extractMetaFromTransition(tr)

	respExampleCounts := map[string]int{}
	reqExampleCounts := map[string]int{}

	for i, tc := range tr.contentArray() {
		switch tc.Element {
		case "copy":
			if consumedMeta[i] {
				continue
			}
			if s := proseFromCopyText(tc.contentString()); s != "" {
				descParts = append(descParts, s)
			}
		case "httpTransaction":
			var txTitle string
			for _, txc := range tc.contentArray() {
				if txc.Element == "httpRequest" {
					txTitle = txc.Meta.Title.Content
					break
				}
			}
			for _, txc := range tc.contentArray() {
				txc := txc
				switch txc.Element {
				case "httpRequest":
					rb := requestBodyFromElement(&txc, resolver, actionDefault, op.RequestBody, reqExampleCounts, txTitle)
					if rb != nil {
						op.RequestBody = rb
					}
					// Convert `+ Headers` on the request into in:header parameters.
					// appendUniqueParam ensures duplicates across multi-transaction
					// examples are deduplicated (first declaration wins).
					for _, hp := range requestHeaderParams(txc.Attributes.Headers) {
						op.Parameters = appendUniqueParam(op.Parameters, hp)
					}
				case "httpResponse":
					status := txc.Attributes.StatusCode.Content
					if status == "" {
						status = "default"
					}
					if op.Responses == nil {
						op.Responses = map[string]*oas.Response{}
					}
					title := strings.TrimSpace(txc.Meta.Title.Content)
					prose := collectResponseDescription(&txc)
					resp := op.Responses[status]
					if resp == nil {
						resp = &oas.Response{}
						if wantResponseSummary {
							// OAS 3.2: split the title into a real
							// `summary:` field and let `description:`
							// hold prose / reason phrase.
							resp.Summary = title
							switch {
							case prose != "":
								resp.Description = prose
							default:
								resp.Description = defaultStatusDescription(status)
							}
						} else {
							// Pre-3.2: title fills description if no
							// prose, falling through to reason phrase.
							switch {
							case title != "":
								resp.Description = title
							case prose != "":
								resp.Description = prose
							default:
								resp.Description = defaultStatusDescription(status)
							}
						}
						op.Responses[status] = resp
					} else {
						// Subsequent transactions for the same status -
						// upgrade defaults if better content arrived.
						if wantResponseSummary && resp.Summary == "" && title != "" {
							resp.Summary = title
						}
						if resp.Description == defaultStatusDescription(status) || resp.Description == "" {
							switch {
							case !wantResponseSummary && title != "":
								resp.Description = title
							case prose != "":
								resp.Description = prose
							}
						}
					}
					applyHeaders(resp, txc.Attributes.Headers)
					applyBody(resp, &txc, resolver, resourceDefault, status, respExampleCounts, txTitle)
				}
			}
		}
	}
	if len(descParts) > 0 {
		op.Description = strings.Join(descParts, "\n\n")
	}
	return op, meta
}

// requestBodyFromElement turns an httpRequest into an OAS RequestBody.
func requestBodyFromElement(req *element, resolver *schemaResolver, actionDefault *oas.Schema, existing *oas.RequestBody, exampleCounts map[string]int, exampleName string) *oas.RequestBody {
	contentType := primaryContentType(req)
	body := assetMessageBody(req)
	rawSchema := parseRawSchema(assetMessageBodySchema(req))
	schema := resolver.schemaFromDataStructureChild(req)
	switch {
	case schema != nil:
	case rawSchema != nil:
		schema = rawSchema
	case actionDefault != nil:
		schema = actionDefault
	}
	if body == "" && schema == nil {
		return existing
	}
	if contentType == "" {
		contentType = defaultContentType(schema != nil, body != "")
	}
	rb := existing
	if rb == nil {
		rb = &oas.RequestBody{Required: true, Content: map[string]*oas.MediaType{}}
	}
	if rb.Content == nil {
		rb.Content = map[string]*oas.MediaType{}
	}
	mt := rb.Content[contentType]
	if mt == nil {
		mt = &oas.MediaType{Schema: schema}
		rb.Content[contentType] = mt
	} else if mt.Schema == nil && schema != nil {
		mt.Schema = schema
	}
	if body != "" {
		example := stripBlueprintReservedKeys(parseExample(body))
		addExample(mt, exampleCounts, contentType, exampleName, example)
		if mt.Schema == nil {
			mt.Schema = inferSchemaFromExample(example)
		}
	}
	return rb
}

func applyBody(resp *oas.Response, el *element, resolver *schemaResolver, resourceDefault *oas.Schema, status string, exampleCounts map[string]int, exampleName string) {
	body := assetMessageBody(el)
	rawSchema := parseRawSchema(assetMessageBodySchema(el))
	schema := resolver.schemaFromDataStructureChild(el)
	contentType := primaryContentType(el)
	switch {
	case schema != nil:
	case rawSchema != nil:
		schema = rawSchema
	case resourceDefault != nil && isSuccessStatus(status) && contentType != "":
		schema = resourceDefault
	}
	if body == "" && schema == nil {
		// If the author declared an explicit content-type (e.g. text/event-stream
		// on an SSE endpoint) but provided no body or schema, still register the
		// media-type key so the content type appears in the OAS response.
		if contentType != "" {
			if resp.Content == nil {
				resp.Content = map[string]*oas.MediaType{}
			}
			if resp.Content[contentType] == nil {
				resp.Content[contentType] = &oas.MediaType{}
			}
		}
		return
	}
	if contentType == "" {
		contentType = defaultContentType(schema != nil, body != "")
	}
	if resp.Content == nil {
		resp.Content = map[string]*oas.MediaType{}
	}
	mt := resp.Content[contentType]
	if mt == nil {
		mt = &oas.MediaType{}
		resp.Content[contentType] = mt
	}
	if schema != nil && mt.Schema == nil {
		mt.Schema = schema
	}
	if body != "" {
		example := stripBlueprintReservedKeys(parseExample(body))
		key := status + "\x00" + contentType
		addExample(mt, exampleCounts, key, exampleName, example)
		if mt.Schema == nil {
			mt.Schema = inferSchemaFromExample(example)
		}
	}
}

// defaultContentType returns the content-type to use when the author
// declared a schema-bearing body (`+ Attributes` / `+ Schema` /
// dataStructure) but Drafter dropped the `(<media type>)` annotation -
// the most common cause is multiple `+ Request` blocks sharing one
// `+ Response`, where Drafter clones the response without copying its
// content-type. A typed schema cannot serialise as octet-stream, so
// `application/json` is the only honest default.
func defaultContentType(hasSchema, hasBody bool) string {
	switch {
	case hasSchema:
		return "application/json"
	case hasBody:
		// A free-form body example without any other context could be
		// anything; `application/octet-stream` keeps the previous
		// safe-default behaviour.
		return "application/octet-stream"
	default:
		return ""
	}
}

// scalar `example:` form to the `examples:` map on the second distinct
// occurrence. Identical values are deduplicated (Drafter emits one
// httpTransaction per Response, so a single authored body shows up N
// times).
func addExample(mt *oas.MediaType, counts map[string]int, key, title string, value any) {
	if value == nil {
		return
	}
	if exampleAlreadyPresent(mt, value) {
		return
	}
	counts[key]++
	idx := counts[key]
	switch {
	case idx == 1 && title == "":
		if mt.Example == nil && mt.Examples == nil {
			mt.Example = value
		}
	default:
		if mt.Examples == nil {
			mt.Examples = map[string]*oas.Example{}
		}
		if mt.Example != nil {
			mt.Examples[fmt.Sprintf("example%d", 1)] = &oas.Example{Value: mt.Example}
			mt.Example = nil
		}
		name := title
		if name == "" {
			name = fmt.Sprintf("example%d", idx)
		}
		mt.Examples[uniqueExampleName(mt.Examples, name)] = &oas.Example{Value: value}
	}
}

func exampleAlreadyPresent(mt *oas.MediaType, value any) bool {
	if mt.Example != nil && reflect.DeepEqual(mt.Example, value) {
		return true
	}
	for _, ex := range mt.Examples {
		if ex != nil && reflect.DeepEqual(ex.Value, value) {
			return true
		}
	}
	return false
}

func uniqueExampleName(m map[string]*oas.Example, name string) string {
	if _, exists := m[name]; !exists {
		return name
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", name, i)
		if _, exists := m[candidate]; !exists {
			return candidate
		}
	}
}

func isSuccessStatus(status string) bool {
	return len(status) == 3 && status[0] == '2'
}

func parseRawSchema(raw string) *oas.Schema {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var s oas.Schema
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return nil
	}
	return &s
}

func parseExample(body string) any {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return nil
	}
	switch trimmed[0] {
	case '{', '[':
		var v any
		if err := json.Unmarshal([]byte(trimmed), &v); err == nil {
			return v
		}
	}
	return body
}

// metaConstraintKeysLower is the set of Blueprint+ `+ Meta` constraint keys
// (lowercased). Objects whose every key is in this set are phantom "Meta
// constraint objects" that Drafter emits as extra array children when a blank
// line separates a member definition from its `+ Meta` block; they should
// never appear as example array items.
var metaConstraintKeysLower = map[string]bool{
	"maxitems": true, "minitems": true,
	"maxlength": true, "minlength": true,
	"minimum": true, "maximum": true,
	"pattern": true, "uniqueitems": true,
	"exclusiveminimum": true, "exclusivemaximum": true,
	"multipleof": true,
}

// isPhantomMetaConstraintObject reports whether m is a Blueprint+ phantom Meta
// constraint object — i.e. every key (case-insensitive) is a known constraint
// keyword.  An empty map is NOT considered a phantom object.
func isPhantomMetaConstraintObject(m map[string]any) bool {
	if len(m) == 0 {
		return false
	}
	for k := range m {
		if !metaConstraintKeysLower[strings.ToLower(strings.TrimSpace(k))] {
			return false
		}
	}
	return true
}

// stripBlueprintReservedKeys removes Blueprint+ pseudo-member keys and phantom
// Meta constraint objects that must never appear in OAS example bodies.
//
// "Schema Patch" — Drafter synthesises a messageBody asset from every parsed
// MSON member, including the `+ Schema Patch` pseudo-member, so without this
// scrub the patch JSON block leaks into the rendered curl example as
// `"Schema Patch": ""`.
//
// Phantom Meta constraint objects — when a blank line precedes a `+ Meta`
// block inside a MSON named-type member, Drafter treats the Meta block as a
// sub-item of the member (an extra array child).  Those objects consist
// entirely of constraint keys (MaxItems, MaxLength, …) and must be removed
// from array positions in examples.
//
// The function recurses into maps and arrays so nested examples are cleaned
// as well.
func stripBlueprintReservedKeys(v any) any {
	switch val := v.(type) {
	case map[string]any:
		for k := range val {
			if strings.EqualFold(strings.TrimSpace(k), "Schema Patch") {
				delete(val, k)
			}
		}
		for k, child := range val {
			val[k] = stripBlueprintReservedKeys(child)
		}
		return val
	case []any:
		result := make([]any, 0, len(val))
		for _, item := range val {
			if m, ok := item.(map[string]any); ok && isPhantomMetaConstraintObject(m) {
				continue // skip phantom Meta constraint objects
			}
			result = append(result, stripBlueprintReservedKeys(item))
		}
		return result
	}
	return v
}

func applyHeaders(resp *oas.Response, headers headersValue) {
	for _, m := range headers.Content {
		name := m.Content.Key.Content
		if name == "" || strings.EqualFold(name, "Content-Type") {
			continue
		}
		if resp.Headers == nil {
			resp.Headers = map[string]*oas.Header{}
		}
		example, required, deprecated := parseHeaderAnnotations(m.Content.Value.contentString())
		h := &oas.Header{
			Description: strings.TrimSpace(m.Meta.Description.Content),
			Required:    required,
			Deprecated:  deprecated,
			Schema:      &oas.Schema{Type: "string"},
		}
		if example != "" {
			h.Schema.Example = example
		}
		resp.Headers[name] = h
	}
}

// requestHeaderParams converts the `+ Headers` block of an httpRequest into
// OAS Parameter objects with `in: "header"`. Content-Type is skipped because
// it is already captured as the media-type key.
//
// Blueprint+ convention: trailing annotations on the header value are
// recognised and stripped (see parseHeaderAnnotations):
//   - `(required)`   → `required: true`
//   - `(optional)`   → required omitted (default)
//   - `(deprecated)` → `deprecated: true`
//
// Example APIB source:
//
//   - Request (application/json)
//   - Headers
//     Authorization: Bearer token (required)
//     X-Legacy-Token: abc (deprecated)
//     X-Request-ID: abc-123
func requestHeaderParams(headers headersValue) []*oas.Parameter {
	var out []*oas.Parameter
	for _, m := range headers.Content {
		name := m.Content.Key.Content
		if name == "" || strings.EqualFold(name, "Content-Type") {
			continue
		}
		example, required, deprecated := parseHeaderAnnotations(m.Content.Value.contentString())
		// typeAttributes fallback (not emitted by stock Drafter for + Headers,
		// but honoured for forward compatibility).
		if !required {
			for _, ta := range m.Attributes.TypeAttributes.Content {
				if ta.Content == "required" {
					required = true
					break
				}
			}
		}
		if !deprecated {
			for _, ta := range m.Attributes.TypeAttributes.Content {
				if ta.Content == "deprecated" {
					deprecated = true
					break
				}
			}
		}
		p := &oas.Parameter{
			Name:        name,
			In:          "header",
			Required:    required,
			Deprecated:  deprecated,
			Description: strings.TrimSpace(m.Meta.Description.Content),
			Schema:      &oas.Schema{Type: "string"},
		}
		if example != "" {
			p.Schema.Example = example
		}
		out = append(out, p)
	}
	return out
}

// parseHeaderAnnotations strips all trailing Blueprint+ annotations from a
// header value string and returns the clean example value plus flags.
// Annotations may appear in any order and are matched case-insensitively.
// A leading space is required before the opening parenthesis so bare values
// that happen to end in a parenthesised word are not misidentified.
//
// Recognised annotations:
//   - (required)   → required = true
//   - (optional)   → no-op (default is optional)
//   - (deprecated) → deprecated = true
func parseHeaderAnnotations(raw string) (value string, required, deprecated bool) {
	s := strings.TrimSpace(raw)
	for {
		lower := strings.ToLower(s)
		switch {
		case strings.HasSuffix(lower, " (required)") || lower == "(required)":
			required = true
			if lower == "(required)" {
				s = ""
			} else {
				s = strings.TrimSpace(s[:len(s)-len(" (required)")])
			}
			continue
		case strings.HasSuffix(lower, " (optional)") || lower == "(optional)":
			if lower == "(optional)" {
				s = ""
			} else {
				s = strings.TrimSpace(s[:len(s)-len(" (optional)")])
			}
			continue
		case strings.HasSuffix(lower, " (deprecated)") || lower == "(deprecated)":
			deprecated = true
			if lower == "(deprecated)" {
				s = ""
			} else {
				s = strings.TrimSpace(s[:len(s)-len(" (deprecated)")])
			}
			continue
		}
		break
	}
	return s, required, deprecated
}

// parseHeaderValue is a thin wrapper around parseHeaderAnnotations that
// returns only the value and required flag. Used by callers that predate
// the deprecated annotation (kept for backwards compatibility).
func parseHeaderValue(raw string) (value string, required bool) {
	v, r, _ := parseHeaderAnnotations(raw)
	return v, r
}

func primaryContentType(el *element) string {
	for _, m := range el.Attributes.Headers.Content {
		if strings.EqualFold(m.Content.Key.Content, "Content-Type") {
			return m.Content.Value.contentString()
		}
	}
	for _, c := range el.contentArray() {
		if c.Element == "asset" && c.Attributes.ContentType.Content != "" {
			return c.Attributes.ContentType.Content
		}
	}
	return ""
}

// collectResponseDescription joins every `copy` element directly under
// an httpResponse into a single description string. Blueprint+ §10.3
// uses this to override the default reason-phrase ("OK", "Not Found"…)
// when authors document the response with prose. Embedded `+ Meta`
// blocks are stripped so they don't appear in the description.
func collectResponseDescription(resp *element) string {
	var parts []string
	for _, c := range resp.contentArray() {
		if c.Element != "copy" {
			continue
		}
		if s := strings.TrimSpace(proseFromCopyText(c.contentString())); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n\n")
}

// assetMessageBody extracts the first asset element classified as
// "messageBody" from the children of el.
func assetMessageBody(el *element) string {
	for _, c := range el.contentArray() {
		if c.Element != "asset" {
			continue
		}
		for _, cls := range c.Meta.Classes.Content {
			if cls.Content == "messageBody" {
				return strings.TrimRight(c.contentString(), "\n")
			}
		}
	}
	return ""
}

// assetMessageBodySchema extracts the first asset element classified as
// "messageBodySchema" - produced by an APIB `+ Schema` block. The content
// is raw JSON Schema text and is returned verbatim (whitespace trimmed).
func assetMessageBodySchema(el *element) string {
	for _, c := range el.contentArray() {
		if c.Element != "asset" {
			continue
		}
		for _, cls := range c.Meta.Classes.Content {
			if cls.Content == "messageBodySchema" {
				return strings.TrimRight(c.contentString(), "\n")
			}
		}
	}
	return ""
}

// paramsFromHrefVariables converts a Drafter hrefVariables element into OAS
// Parameter objects. defaultIn is "path" or "query" depending on context.
//
// Blueprint+ Tier-A (§9.2): a parameter description prefixed with
// `[header]`, `[header:Real-Name]`, `[cookie]`, or `[cookie:Real-Name]`
// (backticks tolerated) overrides the location to the named slot. The
// optional `:Real-Name` lets authors keep an MSON-friendly identifier
// like `traceId` while emitting the real header name `X-Trace-Id` -
// stock Drafter mangles dashed identifiers, so this indirection is
// the practical Tier-A path. The prefix is stripped from the rendered
// description.
//
// diag (when non-nil) receives Blueprint+ §15 codes:
//   - E003 - malformed bracket prefix (e.g. `[header:` with no `]`).
//   - E004 - unknown location word (anything other than header / cookie).
func paramsFromHrefVariables(hv hrefVariablesValue, defaultIn, path string, diag *Diagnostics) []*oas.Parameter {
	var out []*oas.Parameter
	for _, m := range hv.Content {
		name := m.Content.Key.Content
		if name == "" {
			continue
		}
		// Surface bracket-prefix problems BEFORE the recogniser swallows
		// the description, so authors learn that `[query]` etc. are not
		// recognised rather than seeing them silently kept verbatim.
		if code, msg := peekBracketPrefix(m.Meta.Description.Content); code != "" {
			switch code {
			case CodeMalformedLocation:
				diag.Error(CodeMalformedLocation, msg+" (parameter "+name+")")
			case CodeUnknownLocation:
				diag.Error(CodeUnknownLocation, msg+" (parameter "+name+")")
			}
		}
		// Parse the location-prefix BEFORE deciding `in`, so it wins
		// over the URI-template inference below.
		loc, realName, desc := parseLocationPrefix(m.Meta.Description.Content)

		in := defaultIn
		switch {
		case loc != "":
			in = loc
			if realName != "" {
				name = realName
			}
		case !strings.Contains(path, "{"+name+"}"):
			in = "query"
		}

		schema := paramSchemaFromTitle(m.Meta.Title.Content)
		oasType := schema.Type // used for example coercion + integer inference below
		// MSON enum parameters (e.g. `status: draft (enum[string], required)`)
		// carry their Members values on content.value.attributes.enumerations
		// when the value element is an `enum`. Fall back to the member-level
		// enumerations for the rarer form where values sit on the member itself.
		enumSrc := m.Content.Value.Attributes.Enumerations
		if len(enumSrc.Content) == 0 {
			enumSrc = m.Attributes.Enumerations
		}
		if vals := enumerationValues(enumSrc); len(vals) > 0 {
			schema.Enum = vals
		}
		// Sample value (`+ q: news (string)`) becomes `schema.example`.
		// Drafter stores every sample as a JSON string regardless of the
		// declared type, so coerce based on the OAS type.
		if ex := parameterExampleValue(&m.Content.Value, oasType); ex != nil {
			schema.Example = ex
			// Integer inference: pre-processing normalises `(integer …)` to
			// `(number …)` so Drafter can parse the document, which means
			// meta.title may say "number" even when the author wrote
			// "integer". Re-derive the intent from the example: if it is a
			// whole number promote the schema type to "integer".
			if oasType == "number" {
				if f, ok := ex.(float64); ok && f == float64(int64(f)) {
					schema.Type = "integer"
				}
			}
			if f := inferFormat(schema); f != "" {
				schema.Format = f
			} else if schema.Type == "array" && schema.Items != nil && schema.Items.Format == "" {
				// For array[string] parameters (e.g. `author: editor@example.com
				// (array[string])`), the top-level schema is type "array" so
				// inferFormat skips it. Try to derive the format for the items
				// schema by borrowing the parameter's example value.
				itemCandidate := &oas.Schema{Type: schema.Items.Type, Example: ex}
				if f := inferFormat(itemCandidate); f != "" {
					schema.Items.Format = f
				}
			}
		}
		// Strip any embedded `+ Meta` constraint block from the description
		// and apply the recognised keys (Pattern, MinLength, …) to the
		// parameter's schema. The block is a Blueprint+ §14 convention that
		// Drafter folds into the description verbatim.
		desc = extractConstraintsFromDescription(schema, desc)
		p := &oas.Parameter{
			Name:        name,
			In:          in,
			Description: desc,
			Schema:      schema,
		}
		// Path parameters are required by definition in OAS; otherwise honour MSON.
		if in == "path" {
			p.Required = true
		} else {
			for _, ta := range m.Attributes.TypeAttributes.Content {
				if ta.Content == "required" {
					p.Required = true
				}
			}
		}
		for _, ta := range m.Attributes.TypeAttributes.Content {
			if ta.Content == "deprecated" {
				p.Deprecated = true
			}
		}
		out = append(out, p)
	}
	return out
}

// msonTypeToOAS maps an MSON primitive type to its closest OAS type.
func msonTypeToOAS(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "number":
		return "number"
	case "integer":
		return "integer"
	case "boolean":
		return "boolean"
	case "", "string":
		return "string"
	default:
		return "string"
	}
}

// paramSchemaFromTitle converts the MSON type annotation stored in
// hrefVariables member meta.title into a minimal OAS Schema skeleton.
// It handles compound type notations that msonTypeToOAS cannot:
//
//   - "array[string]"        → {type:array, items:{type:string}}
//   - "array[number]"        → {type:array, items:{type:number}}
//   - "enum[string]"         → {type:string}  (enum values are added later)
//   - "string" / "number" …  → {type:<primitive>}
//
// The returned schema's Type field is what callers use for example
// coercion and integer inference; it is "array" for array compounds.
func paramSchemaFromTitle(title string) *oas.Schema {
	t := strings.ToLower(strings.TrimSpace(title))
	// array[ItemType]
	if strings.HasPrefix(t, "array[") && strings.HasSuffix(t, "]") {
		inner := t[6 : len(t)-1]
		return &oas.Schema{
			Type:  "array",
			Items: &oas.Schema{Type: msonTypeToOAS(inner)},
		}
	}
	// enum[BaseType] — enum values are lifted from the value element
	// separately; here we just need the base type.
	if strings.HasPrefix(t, "enum[") && strings.HasSuffix(t, "]") {
		inner := t[5 : len(t)-1]
		return &oas.Schema{Type: msonTypeToOAS(inner)}
	}
	return &oas.Schema{Type: msonTypeToOAS(t)}
}

// msonNumberOASType resolves the OAS type for a Refract element whose
// element name is "string", "boolean", or "number". For numeric elements
// it promotes "number" to "integer" when the inline example content is a
// whole number (no fractional part).
//
// Motivation: Drafter v5 does not recognise `integer` as a built-in MSON
// primitive; the drafter package pre-processes `(integer …)` annotations
// to `(number …)` so the document parses. This function re-derives the
// original intent from the example value, applying the same heuristic
// that inferSchemaFromExample uses for JSON body examples:
//
//   - limit: 20 (integer, optional)  → preprocessed to (number …)
//     example 20 is whole → integer
//   - price: 9.99 (number)           → 9.99 has a fraction  → number
//   - count (number, optional)       → no example content   → number
func msonNumberOASType(el *element) string {
	if el.Element != "number" {
		return msonTypeToOAS(el.Element)
	}
	if len(el.Content) > 0 {
		var n float64
		if err := json.Unmarshal(el.Content, &n); err == nil && n == float64(int64(n)) {
			return "integer"
		}
	}
	return "number"
}

// parameterExampleValue extracts a typed sample value from a Drafter
// hrefVariables member's `content.value` element. Drafter serialises
// every primitive sample as a JSON string regardless of the declared
// MSON type (e.g. `+ limit: 20 (number)` arrives as `value: "20"`),
// so we read the raw string and coerce it based on the OAS type the
// caller derived from `meta.title`.
//
// Returns nil when the element carries no sample, so the caller can
// skip emitting `example:` rather than rendering an empty value.
func parameterExampleValue(v *element, oasType string) any {
	if v == nil || len(v.Content) == 0 {
		return nil
	}
	var s string
	if err := json.Unmarshal(v.Content, &s); err != nil || s == "" {
		return nil
	}
	switch oasType {
	case "number":
		var n float64
		if err := json.Unmarshal([]byte(s), &n); err == nil {
			return n
		}
	case "integer":
		var n int64
		if err := json.Unmarshal([]byte(s), &n); err == nil {
			return n
		}
	case "boolean":
		var b bool
		if err := json.Unmarshal([]byte(s), &b); err == nil {
			return b
		}
	default:
		return s
	}
	return nil
}

// enumerationValues converts a Drafter `attributes.enumerations`
// envelope into a slice of OAS-friendly enum values. Used by parameter
// schemas to surface `+ status: draft (enum[string])` choices.
func enumerationValues(arr elementsArray) []any {
	if len(arr.Content) == 0 {
		return nil
	}
	out := make([]any, 0, len(arr.Content))
	for i := range arr.Content {
		if v := scalarExample(&arr.Content[i]); v != nil {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// isStandardHTTPMethod reports whether m is one of the verbs OpenAPI's
// PathItem can carry (case-insensitive comparison via strings.ToUpper).
// Used to gate Blueprint+ §15 E001.
func isStandardHTTPMethod(m string) bool {
	switch strings.ToUpper(strings.TrimSpace(m)) {
	case "GET", "PUT", "POST", "DELETE", "PATCH", "OPTIONS", "HEAD", "TRACE":
		return true
	}
	return false
}

// validateURITemplate scans an APIB href for malformed RFC 6570 segments
// and returns one descriptive message per problem found. Empty result
// means the template is well-formed (Blueprint+ §15 E002 candidate).
//
// Recognised problems:
//   - unmatched `{` (no closing `}`)
//   - unmatched `}` (no opening `{`)
//   - empty `{}` segment
//   - operator with no names (e.g. `{?}`, `{&}`)
//   - empty name in a comma-list (e.g. `{?a,,b}`)
func validateURITemplate(href string) []string {
	var out []string
	depth := 0
	start := -1
	for i := 0; i < len(href); i++ {
		switch href[i] {
		case '{':
			if depth > 0 {
				out = append(out, "nested '{' at offset "+itoaConvert(i))
			}
			depth = 1
			start = i
		case '}':
			if depth == 0 {
				out = append(out, "unmatched '}' at offset "+itoaConvert(i))
				continue
			}
			seg := href[start+1 : i]
			depth = 0
			start = -1
			if seg == "" {
				out = append(out, "empty '{}' segment")
				continue
			}
			body := seg
			if body[0] == '?' || body[0] == '&' || body[0] == '+' || body[0] == '#' || body[0] == '/' || body[0] == '.' || body[0] == ';' {
				body = body[1:]
				if body == "" {
					out = append(out, "operator '"+seg[:1]+"' with no names")
					continue
				}
			}
			for _, name := range strings.Split(body, ",") {
				if strings.TrimSpace(name) == "" {
					out = append(out, "empty name in '{"+seg+"}'")
					break
				}
			}
		}
	}
	if depth != 0 {
		out = append(out, "unmatched '{' at offset "+itoaConvert(start))
	}
	return out
}

// itoaConvert is a tiny private int→string so this file doesn't need
// strconv just for diagnostic offsets.
func itoaConvert(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// splitURITemplate splits an API Blueprint href like "/posts/{id}{?limit}"
// into its path component ("/posts/{id}") and query parameter names
// (["limit"]). Currently understands the `?` and `&` operators.
func splitURITemplate(href string) (path string, query []string) {
	if href == "" {
		return "", nil
	}
	var b strings.Builder
	rest := href
	for {
		i := strings.IndexByte(rest, '{')
		if i < 0 {
			b.WriteString(rest)
			break
		}
		j := strings.IndexByte(rest[i:], '}')
		if j < 0 {
			b.WriteString(rest)
			break
		}
		seg := rest[i+1 : i+j]
		if len(seg) > 0 && (seg[0] == '?' || seg[0] == '&') {
			b.WriteString(rest[:i])
			for _, name := range strings.Split(seg[1:], ",") {
				name = strings.TrimSpace(name)
				if name != "" {
					query = append(query, name)
				}
			}
		} else {
			b.WriteString(rest[:i+j+1])
		}
		rest = rest[i+j+1:]
	}
	return b.String(), query
}

// appendUniqueParam appends p to ps unless an entry with the same (Name, In)
// already exists. Simple O(n*m) but parameter lists are tiny.
func appendUniqueParam(ps []*oas.Parameter, p *oas.Parameter) []*oas.Parameter {
	for _, existing := range ps {
		if existing.Name == p.Name && existing.In == p.In {
			return ps
		}
	}
	return append(ps, p)
}

// defaultStatusDescription returns a sensible default response description
// for a numeric HTTP status code.
func defaultStatusDescription(status string) string {
	switch status {
	case "200":
		return "OK"
	case "201":
		return "Created"
	case "202":
		return "Accepted"
	case "204":
		return "No Content"
	case "301":
		return "Moved Permanently"
	case "302":
		return "Found"
	case "304":
		return "Not Modified"
	case "400":
		return "Bad Request"
	case "401":
		return "Unauthorized"
	case "403":
		return "Forbidden"
	case "404":
		return "Not Found"
	case "409":
		return "Conflict"
	case "422":
		return "Unprocessable Entity"
	case "500":
		return "Internal Server Error"
	default:
		return "Response " + status
	}
}

// normalizeRefSiblings rewrites every Schema in doc so a `$ref` is never
// emitted alongside other keywords. OAS 3.0 validators discard such
// siblings (description, nullable, example, …), which silently swallows
// authoring intent. The fix is the standard `allOf` wrapper:
//
//	{ $ref: X, description: Y }   →   { description: Y, allOf: [{$ref: X}] }
//
// This pass is OAS-3.0-only; 3.1+ permits siblings natively.
func normalizeRefSiblings(doc *oas.Document) {
	walkSchemas(doc, normalizeOneSchema)
}

// normalizeOneSchema rewrites s in place if it is a $ref-with-siblings.
func normalizeOneSchema(s *oas.Schema) {
	if s == nil || s.Ref == "" {
		return
	}
	if !schemaHasSiblings(s) {
		return
	}
	moved := &oas.Schema{Ref: s.Ref}
	s.Ref = ""
	// Prepend rather than append so the $ref is the *only* member of allOf
	// and order is deterministic across runs.
	s.AllOf = append([]*oas.Schema{moved}, s.AllOf...)
}

// schemaHasSiblings reports whether s carries any keyword other than $ref.
// Used by normalizeOneSchema to decide whether the OAS-3.0 allOf wrapper
// is needed.
func schemaHasSiblings(s *oas.Schema) bool {
	// Quick struct-field check via reflection over a Ref-cleared copy: if
	// any other field is non-zero, we have siblings. Schema has many
	// fields; reflection keeps this future-proof when new ones are added.
	c := *s
	c.Ref = ""
	v := reflect.ValueOf(c)
	for i := 0; i < v.NumField(); i++ {
		if !v.Field(i).IsZero() {
			return true
		}
	}
	return false
}

// walkSchemas invokes fn on every *oas.Schema reachable from doc
// (components, parameters, request bodies, responses, headers, and all
// nested composition / object / array schemas). Order is unspecified.
func walkSchemas(doc *oas.Document, fn func(*oas.Schema)) {
	visited := map[*oas.Schema]bool{}
	var walk func(*oas.Schema)
	walk = func(s *oas.Schema) {
		if s == nil || visited[s] {
			return
		}
		visited[s] = true
		fn(s)
		walk(s.Items)
		for _, p := range s.Properties {
			walk(p)
		}
		for _, c := range s.OneOf {
			walk(c)
		}
		for _, c := range s.AnyOf {
			walk(c)
		}
		for _, c := range s.AllOf {
			walk(c)
		}
		if ap, ok := s.AdditionalProperties.(*oas.Schema); ok {
			walk(ap)
		}
	}
	walkParams := func(ps []*oas.Parameter) {
		for _, p := range ps {
			if p != nil {
				walk(p.Schema)
			}
		}
	}
	walkContent := func(content map[string]*oas.MediaType) {
		for _, mt := range content {
			if mt != nil {
				walk(mt.Schema)
			}
		}
	}
	walkResponses := func(rs map[string]*oas.Response) {
		for _, r := range rs {
			if r == nil {
				continue
			}
			walkContent(r.Content)
			for _, h := range r.Headers {
				if h != nil {
					walk(h.Schema)
				}
			}
		}
	}
	walkOp := func(op *oas.Operation) {
		if op == nil {
			return
		}
		walkParams(op.Parameters)
		if op.RequestBody != nil {
			walkContent(op.RequestBody.Content)
		}
		walkResponses(op.Responses)
	}
	walkPaths := func(scope map[string]*oas.PathItem) {
		for _, pi := range scope {
			if pi == nil {
				continue
			}
			walkParams(pi.Parameters)
			for _, op := range pathOperations(pi) {
				walkOp(op)
			}
		}
	}
	walkPaths(doc.Paths)
	walkPaths(doc.Webhooks)
	if doc.Components != nil {
		for _, s := range doc.Components.Schemas {
			walk(s)
		}
	}
}

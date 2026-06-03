// Package convert implements the Blueprint+ extensions to the stock APIB / Drafter
// Blueprint+ Tier-A extensions over stock APIB / Drafter.
//
// Drafter does not natively recognise constructs like `+ Meta` blocks, so
// it folds them into the surrounding `copy` element verbatim. This file
// rescues those blocks from copy text and turns them into typed metadata
// applied to the OAS document.
//
// See specs/apib+.md "+ Meta section" for the recognised keys.
package convert

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/alecoletti/apib-to-oas/internal/oas"
)

// ptr helpers — used by metaBlock constraint fields.
func intPtr(v int) *int             { return &v }
func float64Ptr(v float64) *float64 { return &v }
func boolPtr(v bool) *bool          { return &v }

// metaBlock is the parsed result of a Blueprint+ `+ Meta` markdown block.
// Pointer-typed fields distinguish "absent" from "explicitly cleared":
//
//	Security == nil          → inherit from group / document default
//	*Security == nil slice   → no-op (treated as inherit)
//	*Security == empty slice → explicitly clear (no auth on this op)
//	*Security == non-empty   → use these scheme names
type metaBlock struct {
	// Operation-scoped fields.
	OperationID string
	Tags        []string // post-normalisation; see TagsAppend for + semantics
	TagsAppend  bool     // true if any entry had a "+" prefix → merge with inherited
	Deprecated  *bool
	DocsURL     string
	DocsDesc    string
	Security    *[]string
	Kind        string // group-scope only: maps to tags[].kind (OAS 3.2)
	Summary     string // group-scope only: maps to tags[].summary (OAS 3.2)
	Extensions  map[string]string

	// Schema constraint fields (Blueprint+ §14).
	// When non-nil / non-empty they are applied to the JSON Schema of the
	// enclosing MSON named type or member via applyMetaToSchema.
	Discriminator    string // oneOf discriminator propertyName (named-type scope only)
	Pattern          string
	MinLength        *int
	MaxLength        *int
	Minimum          *float64
	Maximum          *float64
	ExclusiveMinimum *float64
	ExclusiveMaximum *float64
	MultipleOf       *float64
	MinItems         *int
	MaxItems         *int
	UniqueItems      *bool
	// Lifecycle / access annotations (Blueprint+ §14).
	ReadOnly  *bool
	WriteOnly *bool
	Const     any // JSON Schema 2020-12 const value; stored as coerced type
}

// isEmpty reports whether the block carries no actionable data.
func (m *metaBlock) isEmpty() bool {
	return m == nil ||
		(m.OperationID == "" && len(m.Tags) == 0 && !m.TagsAppend &&
			m.Deprecated == nil && m.DocsURL == "" &&
			m.Security == nil && m.Kind == "" && m.Summary == "" &&
			len(m.Extensions) == 0 &&
			m.Discriminator == "" && m.Pattern == "" && m.MinLength == nil && m.MaxLength == nil &&
			m.Minimum == nil && m.Maximum == nil &&
			m.ExclusiveMinimum == nil && m.ExclusiveMaximum == nil &&
			m.MultipleOf == nil && m.MinItems == nil && m.MaxItems == nil &&
			m.UniqueItems == nil &&
			m.ReadOnly == nil && m.WriteOnly == nil && m.Const == nil)
}

// recognisedDocumentMetadataKeys lists the document-level metadata keys
// the converter handles natively (case-insensitive). Any uppercase key
// outside this set becomes an `info.x-*` (or root-level `x-*` via
// `ROOT.` prefix) extension per Blueprint+ §5.5 / §13.3.
var recognisedDocumentMetadataKeys = map[string]bool{
	"format":         true,
	"version":        true,
	"api-version":    true,
	"api_version":    true,
	"host":           true,
	"server":         true,
	"webhook_groups": true,
	"security":       true,
	"summary":        true, // info.summary
	"license":        true, // info.license.name
	"license-id":     true, // info.license.identifier (3.1+ SPDX)
	"license-url":    true, // info.license.url
}

// extractMetaFromTransition pulls a Blueprint+ `+ Meta` block out of a
// transition's child `copy` elements. Returns the parsed block (or nil
// when none present) and a set of *indices* into el.contentArray() that
// the caller should skip so the consumed copy doesn't also leak into
// op.Description. When the meta block is fused into the same copy
// element as prose (no blank line separator before it), only the meta
// portion is consumed - the surrounding prose still reaches Description
// via proseFromCopyText().
func extractMetaFromTransition(el *element) (*metaBlock, map[int]bool) {
	return extractMetaFromCopyChildren(el)
}

// looksLikeMetaBlock reports whether s is a Blueprint+ `+ Meta` markdown
// block (case-insensitive on the keyword, tolerant of leading whitespace
// / CRLF). Used by the per-copy fast path; for copies that fuse meta
// with prose, see findMetaInCopy.
func looksLikeMetaBlock(s string) bool {
	t := strings.TrimLeft(s, " \t\r\n")
	t = strings.TrimPrefix(t, "+ ")
	t = strings.TrimPrefix(t, "- ")
	t = strings.TrimPrefix(t, "* ")
	if len(t) < 4 {
		return false
	}
	head := t[:4]
	return strings.EqualFold(head, "Meta")
}

// findMetaInCopy locates a Blueprint+ `+ Meta` block embedded anywhere
// inside a copy element's text. The block runs from its `+ Meta` (or
// `* Meta`, `- Meta`) line until the first non-blank line at the same
// or shallower indentation. Returns ("", false) when no block is
// found. Stock Drafter folds free-form prose, the `+ Meta` header, and
// every nested `+ Key: value` line into one copy element, so this fused
// case is the common one for groups and resources.
func findMetaInCopy(text string) (string, bool) {
	lines := strings.Split(text, "\n")
	metaIndent := -1
	var meta []string
	for _, ln := range lines {
		if metaIndent < 0 {
			stripped := strings.TrimSpace(ln)
			stripped = strings.TrimPrefix(stripped, "+ ")
			stripped = strings.TrimPrefix(stripped, "- ")
			stripped = strings.TrimPrefix(stripped, "* ")
			if strings.EqualFold(strings.TrimSpace(stripped), "Meta") {
				metaIndent = indentOf(ln)
				meta = append(meta, ln)
			}
			continue
		}
		// Inside the meta block: blank lines and deeper-indented lines
		// belong to the block; anything else terminates it.
		if strings.TrimSpace(ln) == "" {
			meta = append(meta, ln)
			continue
		}
		if indentOf(ln) > metaIndent {
			meta = append(meta, ln)
			continue
		}
		break
	}
	if len(meta) == 0 {
		return "", false
	}
	return strings.Join(meta, "\n"), true
}

// proseFromCopyText returns text with any embedded `+ Meta` block
// stripped out. Symmetrical with findMetaInCopy: anything that block
// absorbs is removed from the prose. Trailing blank lines are trimmed.
func proseFromCopyText(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	metaIndent := -1
	for _, ln := range lines {
		if metaIndent < 0 {
			stripped := strings.TrimSpace(ln)
			stripped = strings.TrimPrefix(stripped, "+ ")
			stripped = strings.TrimPrefix(stripped, "- ")
			stripped = strings.TrimPrefix(stripped, "* ")
			if strings.EqualFold(strings.TrimSpace(stripped), "Meta") {
				metaIndent = indentOf(ln)
				continue
			}
			out = append(out, ln)
			continue
		}
		if strings.TrimSpace(ln) == "" {
			continue
		}
		if indentOf(ln) > metaIndent {
			continue
		}
		// Non-blank line at same or shallower indent: meta block ends.
		metaIndent = -1
		out = append(out, ln)
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	for len(out) > 0 && strings.TrimSpace(out[0]) == "" {
		out = out[1:]
	}
	return strings.Join(out, "\n")
}

// indentOf returns the number of leading-whitespace columns in s,
// counting tabs as 4 for parity with most markdown editors.
func indentOf(s string) int {
	n := 0
	for _, r := range s {
		switch r {
		case ' ':
			n++
		case '\t':
			n += 4
		default:
			return n
		}
	}
	return n
}

// extractMetaFromCopyChildren is the shared implementation used by the
// transition / resource / category extractors. It scans every `copy`
// child of el for an embedded `+ Meta` block (full-copy or fused with
// prose), parses it, and returns a set of indices the caller should
// skip when re-assembling prose. Indices are recorded only for copies
// whose entire content was meta - copies with surviving prose stay
// available so callers can re-emit the prose via proseFromCopyText.
func extractMetaFromCopyChildren(el *element) (*metaBlock, map[int]bool) {
	consumed := map[int]bool{}
	var first *metaBlock
	for i, c := range el.contentArray() {
		if c.Element != "copy" {
			continue
		}
		text := c.contentString()
		var (
			metaText string
			ok       bool
		)
		// Always use findMetaInCopy so that fused copies (a `+ Meta`
		// block followed by prose/tables in the same copy element) only
		// extract the actual meta portion. The former fast-path
		// (looksLikeMetaBlock → full text) was incorrect: when prose
		// with markdown tables follows the meta block in the same copy,
		// table rows containing colons were being fed to parseMetaText
		// and turned into spurious x-* extension keys.
		metaText, ok = findMetaInCopy(text)
		if !ok {
			continue
		}
		mb := parseMetaText(metaText)
		if mb == nil {
			continue
		}
		// Mark fully-consumed copies so the prose collectors can skip
		// them without bothering with proseFromCopyText.
		if strings.TrimSpace(proseFromCopyText(text)) == "" {
			consumed[i] = true
		}
		if first == nil {
			first = mb
		} else {
			mergeMetaBlocks(first, mb)
		}
	}
	return first, consumed
}

// parseMetaText parses a markdown `+ Meta` block of the form:
//
//   - Meta
//   - OperationId: getArticle
//   - Tags: Articles, +Beta
//   - Deprecated: true
//   - Docs: https://… - Description
//   - Security: BearerAuth
//   - Idempotent: true
//
// Lines are trimmed, list-marker prefixes (`+`/`-`/`*`) are stripped, and
// each `Key: value` pair is decoded according to recognisedMetaKeys.
// Returns nil when no key/value pairs are present.
func parseMetaText(s string) *metaBlock {
	mb := &metaBlock{}
	for _, raw := range strings.Split(s, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		// Strip list marker.
		switch {
		case strings.HasPrefix(line, "+ "):
			line = line[2:]
		case strings.HasPrefix(line, "- "):
			line = line[2:]
		case strings.HasPrefix(line, "* "):
			line = line[2:]
		}
		// Skip the "Meta" header itself.
		if strings.EqualFold(strings.TrimSpace(line), "Meta") {
			continue
		}
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			continue
		}
		key := strings.TrimSpace(line[:colon])
		val := strings.TrimSpace(line[colon+1:])
		applyMetaKey(mb, key, val)
	}
	if mb.isEmpty() {
		return nil
	}
	return mb
}

// applyMetaKey decodes a single Key/value pair into mb. Unknown keys
// fall through to mb.Extensions with kebab normalisation (§13.2).
//
//nolint:cyclop
func applyMetaKey(mb *metaBlock, key, val string) {
	switch strings.ToLower(key) {
	case "operationid":
		mb.OperationID = val
	case "tags":
		// Comma-separated; entries prefixed with "+" mean "append to
		// inherited" rather than "replace".
		var tags []string
		for _, raw := range strings.Split(val, ",") {
			t := strings.TrimSpace(raw)
			if t == "" {
				continue
			}
			if strings.HasPrefix(t, "+") {
				mb.TagsAppend = true
				t = strings.TrimSpace(t[1:])
				if t == "" {
					continue
				}
			}
			tags = append(tags, t)
		}
		mb.Tags = tags
	case "deprecated":
		b, ok := parseBoolish(val)
		if ok {
			mb.Deprecated = &b
		}
	case "docs":
		// Optional " - description" suffix (same convention as HOST:).
		url, desc := splitServerEntry(val)
		mb.DocsURL = url
		mb.DocsDesc = desc
	case "security":
		// Empty value → explicitly clear.
		var schemes []string
		for _, raw := range strings.Split(val, ",") {
			s := strings.TrimSpace(raw)
			if s != "" {
				schemes = append(schemes, s)
			}
		}
		mb.Security = &schemes
	case "kind":
		mb.Kind = strings.TrimSpace(val)

	// ── Schema constraints (Blueprint+ §14) ──────────────────────────────
	case "discriminator":
		// Sets discriminator.propertyName on the oneOf parent schema.
		// Meaningful only on named types that also declare a `One Of` block.
		mb.Discriminator = strings.TrimSpace(val)
	case "pattern":
		// Backtick-wrapped values are common (avoids Drafter splitting on " - ").
		mb.Pattern = strings.Trim(strings.TrimSpace(val), "`")
	case "minlength":
		if n, err := strconv.Atoi(strings.TrimSpace(val)); err == nil {
			mb.MinLength = intPtr(n)
		}
	case "maxlength":
		if n, err := strconv.Atoi(strings.TrimSpace(val)); err == nil {
			mb.MaxLength = intPtr(n)
		}
	case "minimum":
		if f, err := strconv.ParseFloat(strings.TrimSpace(val), 64); err == nil {
			mb.Minimum = float64Ptr(f)
		}
	case "maximum":
		if f, err := strconv.ParseFloat(strings.TrimSpace(val), 64); err == nil {
			mb.Maximum = float64Ptr(f)
		}
	case "exclusiveminimum":
		if f, err := strconv.ParseFloat(strings.TrimSpace(val), 64); err == nil {
			mb.ExclusiveMinimum = float64Ptr(f)
		}
	case "exclusivemaximum":
		if f, err := strconv.ParseFloat(strings.TrimSpace(val), 64); err == nil {
			mb.ExclusiveMaximum = float64Ptr(f)
		}
	case "multipleof":
		if f, err := strconv.ParseFloat(strings.TrimSpace(val), 64); err == nil && f > 0 {
			mb.MultipleOf = float64Ptr(f)
		}
	case "minitems":
		if n, err := strconv.Atoi(strings.TrimSpace(val)); err == nil {
			mb.MinItems = intPtr(n)
		}
	case "maxitems":
		if n, err := strconv.Atoi(strings.TrimSpace(val)); err == nil {
			mb.MaxItems = intPtr(n)
		}
	case "uniqueitems":
		if b, ok := parseBoolish(val); ok {
			mb.UniqueItems = boolPtr(b)
		}

	// ── Lifecycle / access annotations (Blueprint+ §14) ──────────────────
	case "readonly":
		if b, ok := parseBoolish(val); ok {
			mb.ReadOnly = boolPtr(b)
		}
	case "writeonly":
		if b, ok := parseBoolish(val); ok {
			mb.WriteOnly = boolPtr(b)
		}
	case "const":
		// Coerce boolean/numeric shapes; backtick-wrap tolerated.
		mb.Const = coerceExtensionValue(strings.Trim(strings.TrimSpace(val), "`"))

	// ── Group-scope tag annotations ──────────────────────────────────────
	case "summary":
		mb.Summary = strings.TrimSpace(val)

	default:
		// Unknown -> x-* extension on the operation. Values stay as
		// strings here; coerceExtensionValue converts boolean / numeric
		// shapes when they're spliced onto the OAS object.
		if mb.Extensions == nil {
			mb.Extensions = map[string]string{}
		}
		mb.Extensions[normaliseExtensionKey(key)] = val
	}
}

// coerceExtensionValue converts a string extension value to its closest
// typed representation for OAS emission (`true`/`false` → bool,
// numerics → int/float, everything else stays string). Used by
// applyMetaToOperation / applyMetaToPathItem so `Idempotent: true`
// renders as `x-idempotent: true` (boolean) instead of the quoted
// string `"true"`.
func coerceExtensionValue(s string) any {
	if b, ok := parseBoolish(s); ok {
		return b
	}
	if n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
		return f
	}
	return s
}

// parseBoolish accepts true/false/yes/no/1/0, case-insensitive. The bool
// return is the parsed value; ok reports whether parsing succeeded.
func parseBoolish(s string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "yes", "1", "on":
		return true, true
	case "false", "no", "0", "off":
		return false, true
	}
	if b, err := strconv.ParseBool(s); err == nil {
		return b, true
	}
	return false, false
}

// normaliseExtensionKey applies the kebab/CamelCase/snake folding rule
// from spec §13.2 and prepends "x-".
//
//	Retry-Policy  → x-retry-policy
//	RetryPolicy   → x-retry-policy   (CamelCase split)
//	retry_policy  → x-retry-policy
func normaliseExtensionKey(k string) string {
	k = strings.TrimSpace(k)
	if strings.HasPrefix(strings.ToLower(k), "x-") {
		// Already an x-* key - just lowercase the prefix.
		return "x-" + strings.ToLower(k[2:])
	}
	var b strings.Builder
	b.WriteString("x-")
	prevLower := false
	for i, r := range k {
		switch {
		case r == '-' || r == '_' || r == ' ':
			if b.Len() > 2 && b.String()[b.Len()-1] != '-' {
				b.WriteByte('-')
			}
			prevLower = false
		case r >= 'A' && r <= 'Z':
			if i > 0 && prevLower && b.Len() > 2 && b.String()[b.Len()-1] != '-' {
				b.WriteByte('-')
			}
			b.WriteRune(r + 32)
			prevLower = false
		default:
			b.WriteRune(r)
			prevLower = r >= 'a' && r <= 'z'
		}
	}
	return b.String()
}

// mergeMetaBlocks folds src into dst (dst wins on conflict for non-zero
// fields; this function only fills holes). Used when an action carries
// more than one + Meta block (rare but possible).
func mergeMetaBlocks(dst, src *metaBlock) {
	if dst.OperationID == "" {
		dst.OperationID = src.OperationID
	}
	if len(dst.Tags) == 0 {
		dst.Tags = src.Tags
		dst.TagsAppend = src.TagsAppend
	}
	if dst.Deprecated == nil {
		dst.Deprecated = src.Deprecated
	}
	if dst.DocsURL == "" {
		dst.DocsURL = src.DocsURL
		dst.DocsDesc = src.DocsDesc
	}
	if dst.Security == nil {
		dst.Security = src.Security
	}
	for k, v := range src.Extensions {
		if _, ok := dst.Extensions[k]; ok {
			continue
		}
		if dst.Extensions == nil {
			dst.Extensions = map[string]string{}
		}
		dst.Extensions[k] = v
	}
	// Schema constraints: first non-nil value wins.
	if dst.Pattern == "" {
		dst.Pattern = src.Pattern
	}
	if dst.MinLength == nil {
		dst.MinLength = src.MinLength
	}
	if dst.MaxLength == nil {
		dst.MaxLength = src.MaxLength
	}
	if dst.Minimum == nil {
		dst.Minimum = src.Minimum
	}
	if dst.Maximum == nil {
		dst.Maximum = src.Maximum
	}
	if dst.ExclusiveMinimum == nil {
		dst.ExclusiveMinimum = src.ExclusiveMinimum
	}
	if dst.ExclusiveMaximum == nil {
		dst.ExclusiveMaximum = src.ExclusiveMaximum
	}
	if dst.MultipleOf == nil {
		dst.MultipleOf = src.MultipleOf
	}
	if dst.MinItems == nil {
		dst.MinItems = src.MinItems
	}
	if dst.MaxItems == nil {
		dst.MaxItems = src.MaxItems
	}
	if dst.UniqueItems == nil {
		dst.UniqueItems = src.UniqueItems
	}
}

// applyMetaToOperation writes the Blueprint+ metadata onto an OAS
// operation. Inherited tag is the ambient group tag (§6) so the Tags-
// append (`+Beta`) semantics work.
func applyMetaToOperation(op *oas.Operation, mb *metaBlock, inheritedTag string) {
	if mb == nil {
		return
	}
	if mb.OperationID != "" {
		op.OperationID = mb.OperationID
	}
	if len(mb.Tags) > 0 {
		if mb.TagsAppend && inheritedTag != "" {
			// Start with inherited, then add (de-duped).
			merged := []string{inheritedTag}
			for _, t := range mb.Tags {
				if t != inheritedTag {
					merged = append(merged, t)
				}
			}
			op.Tags = merged
		} else {
			op.Tags = append([]string(nil), mb.Tags...)
		}
	}
	if mb.Deprecated != nil {
		op.Deprecated = *mb.Deprecated
	}
	if mb.DocsURL != "" {
		op.ExternalDocs = &oas.ExternalDocs{
			URL:         mb.DocsURL,
			Description: mb.DocsDesc,
		}
	}
	if mb.Security != nil {
		schemes := *mb.Security
		if len(schemes) == 0 {
			// Explicit clear - emit `security: []` so the operation
			// does NOT inherit the document-level default. The
			// SecurityCleared sentinel survives `omitempty`.
			op.Security = []oas.SecurityRequirement{}
			op.SecurityCleared = true
		} else {
			op.Security = nil
			op.SecurityCleared = false
			for _, name := range schemes {
				op.Security = append(op.Security, oas.SecurityRequirement{name: []string{}})
			}
		}
	}
	if len(mb.Extensions) > 0 {
		if op.Extensions == nil {
			op.Extensions = map[string]any{}
		}
		for k, v := range mb.Extensions {
			op.Extensions[k] = coerceExtensionValue(v)
		}
	}
}

// applyDocumentExtensions sweeps every document-level metadata entry on
// cat and routes unknown keys to either doc.Info.Extensions (default) or
// doc.Extensions (when prefixed with `ROOT.`). Recognised keys are left
// alone so the existing parsers (parseServersMetadata, etc.) continue
// to own them. diag (when non-nil) receives a W001 entry only when the
// absorbed key looks accidental (no `-` / `_` / `.` separator and no
// CamelCase boundary) - bare PascalCase / single-word keys are likely
// typos for a recognised metadata key (`HOST`, `VERSION`, ...). Keys
// that already look like an extension shape stay silent.
func applyDocumentExtensions(doc *oas.Document, cat *element, diag *Diagnostics) {
	for _, m := range cat.Attributes.Metadata.Content {
		key := strings.TrimSpace(m.Content.Key.Content)
		if key == "" {
			continue
		}
		// Strip ROOT. prefix to decide where the extension lives.
		atRoot := false
		bareKey := key
		if strings.HasPrefix(strings.ToUpper(key), "ROOT.") {
			atRoot = true
			bareKey = key[len("ROOT."):]
		}
		if recognisedDocumentMetadataKeys[strings.ToLower(bareKey)] {
			continue
		}
		val := m.Content.Value.contentString()
		if val == "" {
			continue
		}
		xKey := normaliseExtensionKey(bareKey)
		coerced := coerceExtensionValue(val)
		if atRoot {
			if doc.Extensions == nil {
				doc.Extensions = map[string]any{}
			}
			doc.Extensions[xKey] = coerced
		} else {
			if doc.Info.Extensions == nil {
				doc.Info.Extensions = map[string]any{}
			}
			doc.Info.Extensions[xKey] = coerced
		}
		// W001 is reserved for shapes that look unintentional - if the
		// author already wrote the key in extension form (kebab, snake,
		// dotted, or ROOT-prefixed), they meant it. Suppress.
		if !atRoot && !looksLikeExtensionShape(bareKey) {
			diag.Warn(CodeUnknownMetadataKey,
				"unknown metadata key '"+key+"' preserved as "+xKey)
		}
	}
}

// looksLikeExtensionShape reports whether k looks like an intentional
// `x-*` extension key (carries a `-`, `_`, or `.` separator, or has a
// CamelCase boundary). Single-word PascalCase keys (`Wibble`) are NOT
// considered extension-shaped because they're indistinguishable from
// typos for recognised metadata names.
func looksLikeExtensionShape(k string) bool {
	if strings.ContainsAny(k, "-_.") {
		return true
	}
	prevLower := false
	for _, r := range k {
		if r >= 'A' && r <= 'Z' && prevLower {
			return true
		}
		prevLower = r >= 'a' && r <= 'z'
	}
	return false
}

// extractMetaFromResource is the resource-scoped sibling of
// extractMetaFromTransition. A `+ Meta` block placed under a
// `## Resource [...]` header lands as a `copy` child of the resource
// element; recognised keys map onto the PathItem (description / x-*),
// unrecognised keys onto its Extensions.
func extractMetaFromResource(res *element) (*metaBlock, map[int]bool) {
	return extractMetaFromCopyChildren(res)
}

// extractMetaFromCategory is the group-scoped sibling: a `+ Meta` block
// declared directly under a `# Group <Name>` header lands as a `copy`
// child of the category element. Used for tag-level x-* extensions
// (Blueprint+ §10 - group scope) and `tags[].kind` on OAS 3.2+. The
// returned `consumed` set indexes the same content slice walkResources
// iterates, so the caller can avoid double-attributing the meta text
// to anything else.
func extractMetaFromCategory(cat *element) (*metaBlock, map[int]bool) {
	return extractMetaFromCopyChildren(cat)
}

// applyMetaToSchema writes Blueprint+ §14 schema constraints from a `+ Meta`
// block onto an OAS Schema. Only fields not already set are written (first
// value wins, consistent with mergeMetaBlocks).
//
// Item-level constraints (Pattern, MinLength, MaxLength, Minimum, Maximum,
// ExclusiveMinimum, ExclusiveMaximum, MultipleOf) are routed to s.Items when
// the schema is an array — authoring `+ Meta\n+ Pattern: …` under an
// `array[string]` parameter/member targets the item strings, not the wrapper.
// Array-level constraints (MinItems, MaxItems, UniqueItems) always stay on s.
func applyMetaToSchema(s *oas.Schema, mb *metaBlock) {
	if mb == nil {
		return
	}
	// Discriminator (oneOf propertyName) — only applied when not already set.
	if mb.Discriminator != "" && s.Discriminator == nil {
		s.Discriminator = &oas.Discriminator{PropertyName: mb.Discriminator}
	}
	// For array schemas, item-level constraints go on the items sub-schema.
	itemTarget := s
	if s.Type == "array" && s.Items != nil {
		itemTarget = s.Items
	}
	applyItemTargetConstraints(itemTarget, mb)
	// Array-level constraints always stay on the array schema itself.
	if mb.MinItems != nil && s.MinItems == nil {
		s.MinItems = mb.MinItems
	}
	if mb.MaxItems != nil && s.MaxItems == nil {
		s.MaxItems = mb.MaxItems
	}
	if mb.UniqueItems != nil && !s.UniqueItems {
		s.UniqueItems = *mb.UniqueItems
	}
	// Lifecycle / access annotations.
	if mb.ReadOnly != nil && !s.ReadOnly {
		s.ReadOnly = *mb.ReadOnly
	}
	if mb.WriteOnly != nil && !s.WriteOnly {
		s.WriteOnly = *mb.WriteOnly
	}
	if mb.Deprecated != nil && !s.Deprecated {
		s.Deprecated = *mb.Deprecated
	}
	if mb.Const != nil && s.Const == nil {
		s.Const = mb.Const
	}
}

// applyItemTargetConstraints sets the scalar/string validation keywords that
// apply either to the schema itself (non-array) or to its items sub-schema
// (array). Extracted from applyMetaToSchema to keep that function's cyclomatic
// complexity within the project limit.
func applyItemTargetConstraints(t *oas.Schema, mb *metaBlock) {
	if mb.Pattern != "" && t.Pattern == "" {
		t.Pattern = mb.Pattern
	}
	if mb.MinLength != nil && t.MinLength == nil {
		t.MinLength = mb.MinLength
	}
	if mb.MaxLength != nil && t.MaxLength == nil {
		t.MaxLength = mb.MaxLength
	}
	if mb.Minimum != nil && t.Minimum == nil {
		t.Minimum = mb.Minimum
	}
	if mb.Maximum != nil && t.Maximum == nil {
		t.Maximum = mb.Maximum
	}
	if mb.ExclusiveMinimum != nil && t.ExclusiveMinimum == nil {
		t.ExclusiveMinimum = mb.ExclusiveMinimum
	}
	if mb.ExclusiveMaximum != nil && t.ExclusiveMaximum == nil {
		t.ExclusiveMaximum = mb.ExclusiveMaximum
	}
	if mb.MultipleOf != nil && t.MultipleOf == nil {
		t.MultipleOf = mb.MultipleOf
	}
}

// extractConstraintsFromDescription checks desc for an embedded `+ Meta`
// block and/or a `+ Schema Patch` block, applies any recognised constraints
// to s, and returns the cleaned description (blocks stripped). When neither
// block is present desc is returned unchanged.
func extractConstraintsFromDescription(s *oas.Schema, desc string) string {
	if desc == "" {
		return desc
	}
	if metaText, ok := findMetaInCopy(desc); ok {
		if mb := parseMetaText(metaText); mb != nil {
			applyMetaToSchema(s, mb)
			desc = proseFromCopyText(desc)
		}
	}
	if patchBody, ok := findSchemaPatchInCopy(desc); ok {
		applySchemaPatch(s, patchBody)
		desc = proseWithoutSchemaPatch(desc)
	}
	return desc
}

// findSchemaPatchInCopy locates a Blueprint+ `+ Schema Patch` block embedded
// anywhere in a copy element's text. The block runs from its `+ Schema Patch`
// (or `- Schema Patch`, `* Schema Patch`) header line until the first
// non-blank line at the same or shallower indentation level. The returned
// string is the JSON body with common leading whitespace stripped; it is
// ready to feed directly to json.Unmarshal.
// Returns ("", false) when no such block is found.
func findSchemaPatchInCopy(text string) (string, bool) {
	lines := strings.Split(text, "\n")
	patchIndent := -1
	var body []string
	for _, ln := range lines {
		if patchIndent < 0 {
			t := strings.TrimSpace(ln)
			t = strings.TrimPrefix(t, "+ ")
			t = strings.TrimPrefix(t, "- ")
			t = strings.TrimPrefix(t, "* ")
			if strings.EqualFold(strings.TrimSpace(t), "Schema Patch") {
				patchIndent = indentOf(ln)
				continue
			}
			continue
		}
		if strings.TrimSpace(ln) == "" {
			body = append(body, "")
			continue
		}
		if indentOf(ln) > patchIndent {
			body = append(body, strings.TrimSpace(ln))
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
	if len(body) == 0 {
		return "", false
	}
	return strings.Join(body, "\n"), true
}

// proseWithoutSchemaPatch returns text with any embedded `+ Schema Patch`
// block (header + deeper-indented body lines) removed. Symmetrical with
// findSchemaPatchInCopy. Trailing / leading blank lines are trimmed.
func proseWithoutSchemaPatch(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	patchIndent := -1
	for _, ln := range lines {
		if patchIndent < 0 {
			t := strings.TrimSpace(ln)
			t = strings.TrimPrefix(t, "+ ")
			t = strings.TrimPrefix(t, "- ")
			t = strings.TrimPrefix(t, "* ")
			if strings.EqualFold(strings.TrimSpace(t), "Schema Patch") {
				patchIndent = indentOf(ln)
				continue
			}
			out = append(out, ln)
			continue
		}
		if strings.TrimSpace(ln) == "" {
			continue
		}
		if indentOf(ln) > patchIndent {
			continue
		}
		// Non-blank line at same or shallower indent: patch block ends.
		patchIndent = -1
		out = append(out, ln)
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	for len(out) > 0 && strings.TrimSpace(out[0]) == "" {
		out = out[1:]
	}
	return strings.Join(out, "\n")
}

// applySchemaPatch parses rawJSON as a partial JSON Schema object and merges
// the recognised conditional applicator keywords (if, then, else, not) onto s.
// Fields already set on s are not overwritten (first-write wins, consistent
// with the rest of the Blueprint+ constraint system). Unknown keys in the
// patch JSON are silently ignored.
func applySchemaPatch(s *oas.Schema, rawJSON string) {
	rawJSON = strings.TrimSpace(rawJSON)
	if rawJSON == "" {
		return
	}
	var patch oas.Schema
	if err := json.Unmarshal([]byte(rawJSON), &patch); err != nil {
		return
	}
	if patch.If != nil && s.If == nil {
		s.If = patch.If
	}
	if patch.Then != nil && s.Then == nil {
		s.Then = patch.Then
	}
	if patch.Else != nil && s.Else == nil {
		s.Else = patch.Else
	}
	if patch.Not != nil && s.Not == nil {
		s.Not = patch.Not
	}
}

// applyMetaToPathItem writes the unknown `x-*` extensions from a
// resource-level `+ Meta` block onto a PathItem. Recognised operation-
// level keys (OperationId / Tags / Deprecated / Docs / Security) are
// ignored here - they make no sense on a path item and would be
// confusing to silently propagate.
func applyMetaToPathItem(pi *oas.PathItem, mb *metaBlock) {
	if mb == nil || len(mb.Extensions) == 0 {
		return
	}
	if pi.Extensions == nil {
		pi.Extensions = map[string]any{}
	}
	for k, v := range mb.Extensions {
		pi.Extensions[k] = coerceExtensionValue(v)
	}
}

// reLocationPrefix matches a leading `[loc]` or `[loc:Real-Name]` token on
// a parameter description (Blueprint+ §9, Tier-A form). Recognised
// locations: header, cookie. Examples:
//
//	"[header] Trace identifier."         → loc="header" name="" rest="Trace identifier."
//	"[header:X-Trace-Id] Trace id."      → loc="header" name="X-Trace-Id" rest="Trace id."
//	"`[cookie]` Auth cookie."            → loc="cookie" name="" rest="Auth cookie." (backtick wrap tolerated)
//
// The prefix is purely a *convention* over the existing description
// field - no parser changes needed. Stock Drafter mangles dashed
// parameter identifiers like `X-Trace-Id`, so when authors need a
// real header name with dashes they declare a clean MSON identifier
// (e.g. `traceId`) and override via `:Real-Name`.
var reLocationPrefix = regexp.MustCompile(`^` +
	"`?" +
	`\[(header|cookie)(?::([A-Za-z][A-Za-z0-9._\-]*))?\]` +
	"`?" +
	`\s*(.*)$`)

// parseLocationPrefix extracts a `[header]` / `[cookie]` prefix from a
// parameter description. Returns the recognised location ("" when
// absent), an optional real-name override, and the description with
// the prefix stripped. Non-matching descriptions pass through
// unchanged.
func parseLocationPrefix(desc string) (loc, realName, rest string) {
	m := reLocationPrefix.FindStringSubmatch(strings.TrimSpace(desc))
	if m == nil {
		return "", "", desc
	}
	return strings.ToLower(m[1]), m[2], strings.TrimSpace(m[3])
}

// peekBracketPrefix inspects the leading `[...]` token (if any) on a
// parameter description and returns a Blueprint+ §15 diagnostic when
// it's malformed (E003) or names a location word that isn't recognised
// (E004). A clean `[header]` / `[cookie]` returns ("", ""). Non-bracket
// descriptions pass through with no diagnostic.
//
// We deliberately scan independently of `reLocationPrefix` so the
// regex-side stays a pure recogniser - false positives there would
// silently swallow descriptions like "[query] not allowed" that the
// spec wants flagged.
func peekBracketPrefix(desc string) (code, message string) {
	s := strings.TrimSpace(desc)
	s = strings.TrimPrefix(s, "`")
	if !strings.HasPrefix(s, "[") {
		return "", ""
	}
	end := strings.IndexByte(s, ']')
	if end < 0 {
		return CodeMalformedLocation, "unterminated location prefix in description: " + strings.TrimSpace(desc)
	}
	inner := strings.TrimSpace(s[1:end])
	word := inner
	if i := strings.IndexByte(inner, ':'); i >= 0 {
		word = strings.TrimSpace(inner[:i])
	}
	switch strings.ToLower(word) {
	case "header", "cookie":
		return "", "" // recognised, no diagnostic
	case "":
		return CodeMalformedLocation, "empty location prefix []"
	default:
		return CodeUnknownLocation,
			"unknown location prefix [" + word + "]: only `header` and `cookie` are recognised"
	}
}

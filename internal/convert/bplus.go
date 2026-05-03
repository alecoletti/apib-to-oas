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
	"regexp"
	"strconv"
	"strings"

	"github.com/alecoletti/apib-to-oas/internal/oas"
)

// metaBlock is the parsed result of a Blueprint+ `+ Meta` markdown block.
// Pointer-typed fields distinguish "absent" from "explicitly cleared":
//
//	Security == nil          → inherit from group / document default
//	*Security == nil slice   → no-op (treated as inherit)
//	*Security == empty slice → explicitly clear (no auth on this op)
//	*Security == non-empty   → use these scheme names
type metaBlock struct {
	OperationID string
	Tags        []string // post-normalisation; see TagsAppend for + semantics
	TagsAppend  bool     // true if any entry had a "+" prefix → merge with inherited
	Deprecated  *bool
	DocsURL     string
	DocsDesc    string
	Security    *[]string
	Kind        string // group-scope only: maps to tags[].kind (OAS 3.2)
	Extensions  map[string]string
}

// isEmpty reports whether the block carries no actionable data.
func (m *metaBlock) isEmpty() bool {
	return m == nil ||
		(m.OperationID == "" && len(m.Tags) == 0 && !m.TagsAppend &&
			m.Deprecated == nil && m.DocsURL == "" &&
			m.Security == nil && m.Kind == "" && len(m.Extensions) == 0)
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
		if looksLikeMetaBlock(text) {
			metaText, ok = text, true
		} else {
			metaText, ok = findMetaInCopy(text)
		}
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

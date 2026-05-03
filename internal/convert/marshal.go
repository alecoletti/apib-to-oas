package convert

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/alecoletti/apib-to-oas/internal/oas"
)

// Marshal serialises an OAS document in the requested format.
// Supported formats: "yaml" (default) and "json".
//
// JSON is produced by `encoding/json`, which honours struct field
// declaration order and sorts map keys alphabetically - both desirable for
// stable, diffable OAS output.
//
// YAML is produced by streaming that JSON document through jsonToYAML,
// preserving the same key ordering. This avoids any third-party YAML
// dependency while still emitting block-style YAML that humans can read.
func Marshal(doc *oas.Document, format string) ([]byte, error) {
	switch strings.ToLower(format) {
	case "", "yaml", "yml":
		raw, err := json.Marshal(doc)
		if err != nil {
			return nil, fmt.Errorf("encode intermediate json: %w", err)
		}
		return jsonToYAML(raw)
	case "json":
		return json.MarshalIndent(doc, "", "  ")
	default:
		return nil, fmt.Errorf("unsupported format %q", format)
	}
}

// jsonToYAML converts a JSON document into block-style YAML, preserving the
// key order of the input. Only the JSON value types produced by
// encoding/json on the OAS model are handled (object, array, string, number,
// bool, null).
func jsonToYAML(in []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(in))
	dec.UseNumber()
	var out bytes.Buffer
	if err := emitValue(&out, dec, 0, false); err != nil {
		return nil, err
	}
	if !bytes.HasSuffix(out.Bytes(), []byte("\n")) {
		out.WriteByte('\n')
	}
	return out.Bytes(), nil
}

// emitValue writes one JSON value (consumed from dec) as YAML at the given
// indent. inline=true means the value follows a key on the same line and so
// container types must start on the next line.
func emitValue(out *bytes.Buffer, dec *json.Decoder, indent int, inline bool) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	switch v := tok.(type) {
	case json.Delim:
		switch v {
		case '{':
			return emitObject(out, dec, indent, inline)
		case '[':
			return emitArray(out, dec, indent, inline)
		}
		return fmt.Errorf("unexpected delim %q", v)
	case string:
		writeScalarString(out, v, indent, inline)
	case json.Number:
		if inline {
			out.WriteByte(' ')
		}
		out.WriteString(v.String())
		out.WriteByte('\n')
	case bool:
		if inline {
			out.WriteByte(' ')
		}
		if v {
			out.WriteString("true\n")
		} else {
			out.WriteString("false\n")
		}
	case nil:
		if inline {
			out.WriteByte(' ')
		}
		out.WriteString("null\n")
	default:
		return fmt.Errorf("unsupported json token %T", tok)
	}
	return nil
}

func emitObject(out *bytes.Buffer, dec *json.Decoder, indent int, inline bool) error {
	if !dec.More() {
		// empty object
		if inline {
			out.WriteString(" {}\n")
		} else {
			out.WriteString("{}\n")
		}
		// consume closing }
		if _, err := dec.Token(); err != nil {
			return err
		}
		return nil
	}
	// Peek the first key - if it's an anchor/alias sentinel, handle specially.
	first, err := peekFirstKey(dec)
	if err != nil {
		return err
	}
	switch first {
	case "$$alias":
		// Consume the value of $$alias and the closing brace; render `*ref_N`.
		val, err := readStringValue(dec)
		if err != nil {
			return err
		}
		if _, err := dec.Token(); err != nil { // closing }
			return err
		}
		if inline {
			out.WriteByte(' ')
		}
		out.WriteByte('*')
		out.WriteString(val)
		out.WriteByte('\n')
		return nil
	case "$$anchor":
		// Render `&ref_N` after the colon, then carry on with remaining keys.
		val, err := readStringValue(dec)
		if err != nil {
			return err
		}
		if inline {
			out.WriteByte(' ')
		}
		out.WriteByte('&')
		out.WriteString(val)
		out.WriteByte('\n')
		// Fall through to emit remaining keys at the same indent.
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return err
			}
			key, ok := keyTok.(string)
			if !ok {
				return fmt.Errorf("expected object key, got %T", keyTok)
			}
			writeIndent(out, indent)
			out.WriteString(yamlKey(key))
			out.WriteByte(':')
			if err := emitValue(out, dec, indent+1, true); err != nil {
				return err
			}
		}
		if _, err := dec.Token(); err != nil { // closing }
			return err
		}
		return nil
	}
	if inline {
		out.WriteByte('\n')
	}
	// Emit the already-consumed first key.
	writeIndent(out, indent)
	out.WriteString(yamlKey(first))
	out.WriteByte(':')
	if err := emitValue(out, dec, indent+1, true); err != nil {
		return err
	}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := keyTok.(string)
		if !ok {
			return fmt.Errorf("expected object key, got %T", keyTok)
		}
		writeIndent(out, indent)
		out.WriteString(yamlKey(key))
		out.WriteByte(':')
		if err := emitValue(out, dec, indent+1, true); err != nil {
			return err
		}
	}
	// consume closing }
	if _, err := dec.Token(); err != nil {
		return err
	}
	return nil
}

// peekFirstKey reads the next token (which must be a string key) without
// consuming it from the broader stream - we use a side-channel by reading
// then we can't push back. Workaround: read it, and the caller is
// expected to handle either the sentinel path (consuming the value) or
// fall through to a re-emit loop (so we never need to push back). The
// returned key has *already* been consumed.
func peekFirstKey(dec *json.Decoder) (string, error) {
	tok, err := dec.Token()
	if err != nil {
		return "", err
	}
	s, ok := tok.(string)
	if !ok {
		return "", fmt.Errorf("expected object key, got %T", tok)
	}
	return s, nil
}

// readStringValue reads the next JSON value, expecting it to be a string.
func readStringValue(dec *json.Decoder) (string, error) {
	tok, err := dec.Token()
	if err != nil {
		return "", err
	}
	s, ok := tok.(string)
	if !ok {
		return "", fmt.Errorf("expected string value, got %T", tok)
	}
	return s, nil
}

func emitArray(out *bytes.Buffer, dec *json.Decoder, indent int, inline bool) error {
	if !dec.More() {
		if inline {
			out.WriteString(" []\n")
		} else {
			out.WriteString("[]\n")
		}
		if _, err := dec.Token(); err != nil {
			return err
		}
		return nil
	}
	if inline {
		out.WriteByte('\n')
	}
	for dec.More() {
		writeIndent(out, indent)
		out.WriteString("- ")
		if err := emitArrayItem(out, dec, indent); err != nil {
			return err
		}
	}
	if _, err := dec.Token(); err != nil {
		return err
	}
	return nil
}

// emitArrayItem renders the next JSON value as an array item. Scalars sit on
// the "- " line; objects/arrays start their first key inline with "- ".
func emitArrayItem(out *bytes.Buffer, dec *json.Decoder, indent int) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	switch v := tok.(type) {
	case json.Delim:
		switch v {
		case '{':
			return emitObjectInArrayItem(out, dec, indent+1)
		case '[':
			// Nested arrays: start on the next line with deeper indent.
			out.WriteByte('\n')
			return emitArray(out, dec, indent+1, false)
		}
	case string:
		writeScalarString(out, v, indent+1, false)
		return nil
	case json.Number:
		out.WriteString(v.String())
		out.WriteByte('\n')
		return nil
	case bool:
		if v {
			out.WriteString("true\n")
		} else {
			out.WriteString("false\n")
		}
		return nil
	case nil:
		out.WriteString("null\n")
		return nil
	}
	return errors.New("unhandled token in array item")
}

// emitObjectInArrayItem prints the first key inline with the "- " marker
// and subsequent keys at one extra indent level so they align under that key.
// If the first key is an anchor/alias sentinel it's emitted as `&ref_N` /
// `*ref_N` directly after `- ` (and `*ref_N` consumes the whole item).
func emitObjectInArrayItem(out *bytes.Buffer, dec *json.Decoder, indent int) error {
	if !dec.More() {
		out.WriteString("{}\n")
		_, err := dec.Token()
		return err
	}
	first, err := peekFirstKey(dec)
	if err != nil {
		return err
	}
	switch first {
	case "$$alias":
		val, err := readStringValue(dec)
		if err != nil {
			return err
		}
		if _, err := dec.Token(); err != nil { // closing }
			return err
		}
		out.WriteByte('*')
		out.WriteString(val)
		out.WriteByte('\n')
		return nil
	case "$$anchor":
		val, err := readStringValue(dec)
		if err != nil {
			return err
		}
		out.WriteByte('&')
		out.WriteString(val)
		out.WriteByte('\n')
		// Subsequent keys live at indent (one level past the dash).
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return err
			}
			key, _ := keyTok.(string) //nolint:errcheck // JSON object key tokens are always strings
			writeIndent(out, indent)
			out.WriteString(yamlKey(key))
			out.WriteByte(':')
			if err := emitValue(out, dec, indent+1, true); err != nil {
				return err
			}
		}
		if _, err := dec.Token(); err != nil {
			return err
		}
		return nil
	}
	// Normal first-key path: `- key: value\n  next: value\n ...`
	out.WriteString(yamlKey(first))
	out.WriteByte(':')
	if err := emitValue(out, dec, indent+1, true); err != nil {
		return err
	}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return err
		}
		key, _ := keyTok.(string) //nolint:errcheck // JSON object key tokens are always strings
		writeIndent(out, indent)
		out.WriteString(yamlKey(key))
		out.WriteByte(':')
		if err := emitValue(out, dec, indent+1, true); err != nil {
			return err
		}
	}
	if _, err := dec.Token(); err != nil {
		return err
	}
	return nil
}

func writeIndent(out *bytes.Buffer, indent int) {
	for i := 0; i < indent; i++ {
		out.WriteString("  ")
	}
}

func writeScalarString(out *bytes.Buffer, s string, indent int, inline bool) {
	if inline {
		out.WriteByte(' ')
	}
	if strings.ContainsAny(s, "\n") {
		writeBlockScalar(out, s, indent)
		return
	}
	out.WriteString(quoteIfNeeded(s))
	out.WriteByte('\n')
}

// writeBlockScalar emits a multi-line string using YAML literal block style
// (`|`). Every content line is indented by `indent` levels (2 spaces each)
// so it sits one level deeper than its key.
func writeBlockScalar(out *bytes.Buffer, s string, indent int) {
	out.WriteString("|\n")
	for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		writeIndent(out, indent)
		out.WriteString(line)
		out.WriteByte('\n')
	}
}

func yamlKey(k string) string {
	if k == "" {
		return `""`
	}
	if needsQuoting(k) {
		return fmt.Sprintf("%q", k)
	}
	return k
}

func quoteIfNeeded(s string) string {
	if s == "" {
		return `""`
	}
	if needsQuoting(s) {
		return fmt.Sprintf("%q", s)
	}
	return s
}

// needsQuoting is a conservative test: quote if the string contains any
// character that could trip YAML's plain-scalar parser.
func needsQuoting(s string) bool {
	if s == "" {
		return true
	}
	switch s {
	case "true", "false", "null", "yes", "no", "on", "off", "True", "False", "Null", "TRUE", "FALSE", "NULL", "~":
		return true
	}
	if strings.ContainsAny(s, ":#&*!|>'\"%@`,[]{}\t") {
		return true
	}
	if s[0] == ' ' || s[len(s)-1] == ' ' {
		return true
	}
	if s[0] == '-' || s[0] == '?' {
		return true
	}
	// Numeric-looking keys/values should be quoted to preserve type.
	if isNumeric(s) {
		return true
	}
	return false
}

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	dot := false
	start := 0
	if s[0] == '-' || s[0] == '+' {
		start = 1
	}
	if start == len(s) {
		return false
	}
	for i := start; i < len(s); i++ {
		c := s[i]
		if c == '.' && !dot {
			dot = true
			continue
		}
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// Compile-time check that we can still satisfy io.Writer expectations from
// callers that may want to stream YAML in the future.
var _ io.Writer = (*bytes.Buffer)(nil)

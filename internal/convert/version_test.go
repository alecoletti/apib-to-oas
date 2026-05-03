package convert

import (
	"strings"
	"testing"
)

// minimal Refract bytes that exercise the metadata + version code paths.
// Built by hand rather than captured so the tests stay readable.
const (
	refractWithVersionMetadata = `{
		"element": "parseResult",
		"content": [
			{
				"element": "category",
				"meta": { "title": { "element": "string", "content": "Demo API" } },
				"attributes": {
					"metadata": { "element": "array", "content": [
						{"element": "member", "content": {"key": {"element":"string","content":"FORMAT"}, "value": {"element":"string","content":"1A"}}},
						{"element": "member", "content": {"key": {"element":"string","content":"VERSION"}, "value": {"element":"string","content":"2.4.0"}}}
					]}
				},
				"content": []
			}
		]
	}`

	refractNoMetadata = `{
		"element": "parseResult",
		"content": [
			{ "element": "category", "meta": { "title": {"element":"string","content":"Demo"} }, "content": [] }
		]
	}`
)

func TestInfoVersion_FromMetadata(t *testing.T) {
	doc, err := RefractToOAS([]byte(refractWithVersionMetadata))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Info.Version != "2.4.0" {
		t.Errorf("info.version = %q, want %q", doc.Info.Version, "2.4.0")
	}
}

func TestInfoVersion_CLIOverridesMetadata(t *testing.T) {
	doc, err := RefractToOASWithOptions([]byte(refractWithVersionMetadata), Options{InfoVersion: "9.9.9"})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Info.Version != "9.9.9" {
		t.Errorf("info.version = %q, want %q", doc.Info.Version, "9.9.9")
	}
}

func TestInfoVersion_DefaultWhenAbsent(t *testing.T) {
	doc, err := RefractToOAS([]byte(refractNoMetadata))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Info.Version != "0.0.0" {
		t.Errorf("info.version = %q, want default 0.0.0", doc.Info.Version)
	}
}

func TestInfoVersion_CaseInsensitiveMetadataKey(t *testing.T) {
	src := strings.Replace(refractWithVersionMetadata, "VERSION", "api-version", 1)
	doc, err := RefractToOAS([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Info.Version != "2.4.0" {
		t.Errorf("info.version = %q, want %q (case-insensitive lookup)", doc.Info.Version, "2.4.0")
	}
}

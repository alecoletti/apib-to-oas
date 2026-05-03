package convert

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/alecoletti/apib-to-oas/internal/drafter"
)

// TestGolden_FromRefractFixture is hermetic: it reads a pre-captured Refract
// JSON document and asserts the resulting YAML / JSON bytes exactly match
// the committed golden files. Runs everywhere - no drafter binary needed.
func TestGolden_FromRefractFixture(t *testing.T) {
	refract := mustRead(t, "../../testdata/polls.refract.json")
	wantYAML := mustRead(t, "../../testdata/polls.expected.yaml")
	wantJSON := mustRead(t, "../../testdata/polls.expected.json")

	doc, err := RefractToOAS(refract)
	if err != nil {
		t.Fatalf("RefractToOAS: %v", err)
	}

	gotYAML, err := Marshal(doc, "yaml")
	if err != nil {
		t.Fatalf("Marshal yaml: %v", err)
	}
	if string(gotYAML) != string(wantYAML) {
		writeActual(t, "polls.actual.yaml", gotYAML)
		t.Fatalf("yaml mismatch - see testdata/polls.actual.yaml\n--- got ---\n%s", gotYAML)
	}

	gotJSON, err := Marshal(doc, "json")
	if err != nil {
		t.Fatalf("Marshal json: %v", err)
	}
	if string(gotJSON) != string(wantJSON) {
		writeActual(t, "polls.actual.json", gotJSON)
		t.Fatalf("json mismatch - see testdata/polls.actual.json")
	}
}

// TestGolden_EndToEnd runs the full apib -> drafter -> convert pipeline and
// asserts byte-equality with the committed golden YAML. Skipped when no
// drafter binary is available; opt-in fail with APIB_TO_OAS_REQUIRE_DRAFTER=1.
func TestGolden_EndToEnd(t *testing.T) {
	r, err := drafter.New()
	if err != nil {
		if errors.Is(err, drafter.ErrUnsupportedPlatform) && os.Getenv("APIB_TO_OAS_REQUIRE_DRAFTER") == "" {
			t.Skipf("no drafter binary for %s/%s; skipping", runtime.GOOS, runtime.GOARCH)
		}
		t.Fatalf("drafter init: %v", err)
	}

	src := mustRead(t, "../../testdata/polls.apib")
	wantYAML := mustRead(t, "../../testdata/polls.expected.yaml")

	refract, err := r.Parse(context.Background(), src)
	if err != nil {
		t.Fatalf("drafter parse: %v", err)
	}
	doc, err := RefractToOAS(refract)
	if err != nil {
		t.Fatalf("RefractToOAS: %v", err)
	}
	gotYAML, err := Marshal(doc, "yaml")
	if err != nil {
		t.Fatalf("Marshal yaml: %v", err)
	}
	if string(gotYAML) != string(wantYAML) {
		writeActual(t, "polls.e2e.actual.yaml", gotYAML)
		t.Fatalf("e2e yaml mismatch - see testdata/polls.e2e.actual.yaml")
	}
}

func TestSplitURITemplate(t *testing.T) {
	cases := []struct {
		in    string
		path  string
		query []string
	}{
		{"/posts", "/posts", nil},
		{"/posts/{id}", "/posts/{id}", nil},
		{"/posts{?limit}", "/posts", []string{"limit"}},
		{"/posts{?limit,offset}", "/posts", []string{"limit", "offset"}},
		{"/posts/{id}{?expand}", "/posts/{id}", []string{"expand"}},
	}
	for _, c := range cases {
		gotPath, gotQuery := splitURITemplate(c.in)
		if gotPath != c.path {
			t.Errorf("path(%q) = %q, want %q", c.in, gotPath, c.path)
		}
		if !equalSlices(gotQuery, c.query) {
			t.Errorf("query(%q) = %v, want %v", c.in, gotQuery, c.query)
		}
	}
}

func TestRefractToOAS_Empty(t *testing.T) {
	doc, err := RefractToOAS(nil)
	if err != nil {
		t.Fatal(err)
	}
	if doc.OpenAPI == "" || doc.Info.Title == "" {
		t.Fatalf("expected defaults, got %+v", doc)
	}
}

func TestMarshal_YAML_Defaults(t *testing.T) {
	doc, _ := RefractToOAS(nil)
	doc.Info.Title = "Hello"
	doc.Info.Version = "1.0.0"
	out, err := Marshal(doc, "yaml")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{"openapi:", "title: Hello", "version: 1.0.0", "paths: {}"} {
		if !strings.Contains(s, want) {
			t.Fatalf("yaml missing %q:\n%s", want, s)
		}
	}
}

func TestMarshal_JSON_Defaults(t *testing.T) {
	doc, _ := RefractToOAS(nil)
	out, err := Marshal(doc, "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"openapi"`) {
		t.Fatalf("expected json output, got: %s", out)
	}
}

func TestMarshal_UnknownFormat(t *testing.T) {
	doc, _ := RefractToOAS(nil)
	if _, err := Marshal(doc, "xml"); err == nil {
		t.Fatal("expected error for unknown format")
	}
}

func TestNeedsQuoting(t *testing.T) {
	for _, c := range []struct {
		in   string
		want bool
	}{
		{"foo", false},
		{"true", true},
		{"123", true},
		{"foo:bar", true},
		{"hello world", false},
		{"", true},
	} {
		if got := needsQuoting(c.in); got != c.want {
			t.Errorf("needsQuoting(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// helpers

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	wd, _ := os.Getwd()
	abs := filepath.Join(wd, p)
	data, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read %s: %v", abs, err)
	}
	return data
}

func writeActual(t *testing.T, name string, data []byte) {
	t.Helper()
	wd, _ := os.Getwd()
	out := filepath.Join(wd, "..", "..", "testdata", name)
	if err := os.WriteFile(out, data, 0o644); err != nil {
		t.Logf("could not write actual: %v", err)
		return
	}
	t.Logf("wrote actual to %s", out)
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alecoletti/apib-to-oas/internal/drafter"
)

func TestVersionSubcommand(t *testing.T) {
	var out bytes.Buffer
	app := &App{Version: "test", Commit: "abc1234", BuildTime: "2026-08-18T10:00:00Z", Stdout: &out, Stderr: &bytes.Buffer{}}
	if err := app.Run(context.Background(), []string{"version"}); err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(out.String())
	if !strings.Contains(got, "test") {
		t.Fatalf("expected version in output, got %q", got)
	}
	if !strings.Contains(got, "abc1234") {
		t.Fatalf("expected commit in output, got %q", got)
	}
	if !strings.Contains(got, "2026-08-18T10:00:00Z") {
		t.Fatalf("expected build time in output, got %q", got)
	}
}

func TestVersionFlag(t *testing.T) {
	var out bytes.Buffer
	app := &App{Version: "1.2.3", Stdout: &out, Stderr: &bytes.Buffer{}}
	if err := app.Run(context.Background(), []string{"--version"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "1.2.3") {
		t.Fatalf("expected version in output, got %q", out.String())
	}
}

func TestUnknownCommand(t *testing.T) {
	app := &App{Version: "x", Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	if err := app.Run(context.Background(), []string{"nope"}); err == nil {
		t.Fatal("expected error for unknown command")
	}
}

func TestConvertRequiresInput(t *testing.T) {
	app := &App{Version: "x", Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	if err := app.Run(context.Background(), []string{"convert"}); err == nil {
		t.Fatal("expected error when no input file is provided")
	}
}

func TestHelpSucceeds(t *testing.T) {
	var stdout, stderr bytes.Buffer
	app := &App{Version: "x", Stdout: &stdout, Stderr: &stderr}
	if err := app.Run(context.Background(), []string{"--help"}); err != nil {
		t.Fatalf("--help should not error: %v", err)
	}
	combined := stdout.String() + stderr.String()
	for _, want := range []string{"convert", "lint", "version"} {
		if !strings.Contains(combined, want) {
			t.Fatalf("help output should mention %q, got:\n%s", want, combined)
		}
	}
}

func TestConvertAlias(t *testing.T) {
	// `c` is the short alias for `convert`. With no input it should still
	// fail with a positional-args error, proving the alias was resolved.
	app := &App{Version: "x", Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	err := app.Run(context.Background(), []string{"c"})
	if err == nil {
		t.Fatal("expected error for `c` with no input")
	}
}

func TestInferFormat(t *testing.T) {
	for _, c := range []struct {
		in, want string
	}{
		{"-", "yaml"},
		{"openapi.yaml", "yaml"},
		{"openapi.YML", "yaml"},
		{"openapi.json", "json"},
		{"openapi.JSON", "json"},
		{"openapi.txt", "yaml"},
	} {
		if got := inferFormat(c.in); got != c.want {
			t.Errorf("inferFormat(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestExitCode(t *testing.T) {
	if ExitCode(nil) != 0 {
		t.Errorf("nil err should be 0")
	}
	if got := ExitCode(errors.New("plain")); got != ExitGeneric {
		t.Errorf("plain err = %d, want %d", got, ExitGeneric)
	}
	if got := ExitCode(parseErr(errors.New("p"))); got != ExitParseError {
		t.Errorf("parseErr code = %d, want %d", got, ExitParseError)
	}
	if got := ExitCode(convertErr(errors.New("c"))); got != ExitConvertErr {
		t.Errorf("convertErr code = %d, want %d", got, ExitConvertErr)
	}
}

// TestConvert_FormatInferenceWritesJSON verifies that `apib-to-oas convert -o
// out.json` (no --format) auto-selects JSON and writes a valid JSON file.
// Skips on platforms without an embedded drafter.
func TestConvert_FormatInferenceWritesJSON(t *testing.T) {
	if !drafterAvailable() {
		t.Skip("no drafter binary for this platform")
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "openapi.json")
	app := &App{Version: "x", Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	err := app.Run(context.Background(), []string{"convert", "../../testdata/polls.apib", "-o", out})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(data) == 0 || data[0] != '{' {
		t.Fatalf("expected JSON output (starts with `{`), got: %.40s...", data)
	}
}

// TestConvert_StdinInput proves `convert -` reads APIB from app.Stdin.
func TestConvert_StdinInput(t *testing.T) {
	if !drafterAvailable() {
		t.Skip("no drafter binary for this platform")
	}
	src, err := os.ReadFile("../../testdata/polls.apib")
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	app := &App{
		Version: "x",
		Stdout:  &stdout,
		Stderr:  &stderr,
		Stdin:   bytes.NewReader(src),
	}
	if err := app.Run(context.Background(), []string{"convert", "-"}); err != nil {
		t.Fatalf("convert: %v\nstderr: %s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "openapi:") {
		t.Fatalf("expected YAML on stdout, got:\n%s", stdout.String())
	}
}

// TestLint_OK runs lint against a known-clean fixture and expects exit 0
// with an "ok" line on stderr.
func TestLint_OK(t *testing.T) {
	if !drafterAvailable() {
		t.Skip("no drafter binary for this platform")
	}
	var stdout, stderr bytes.Buffer
	app := &App{Version: "x", Stdout: &stdout, Stderr: &stderr}
	if err := app.Run(context.Background(), []string{"lint", "../../testdata/polls.apib"}); err != nil {
		t.Fatalf("lint: %v\nstderr: %s", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "ok") {
		t.Errorf("expected `ok` line on stderr, got: %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("lint should not write to stdout, got: %q", stdout.String())
	}
}

// drafterAvailable reports whether an embedded drafter binary exists for
// the running platform - without that, every end-to-end CLI test would
// fail with ErrUnsupportedPlatform. Mirror the skip pattern used in
// internal/convert/golden_test.go.
func drafterAvailable() bool {
	_, err := drafter.New()
	return err == nil || os.Getenv("APIB_TO_OAS_REQUIRE_DRAFTER") != ""
}

func init() {
	// Silence unused import warnings on platforms without drafter.
	_ = errors.New
}

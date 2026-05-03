package drafter

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestParse_Integration runs only when an embedded drafter binary for the
// current platform is committed under bin/. CI hosts without binaries skip
// gracefully. Override with APIB_TO_OAS_REQUIRE_DRAFTER=1 to fail instead of skip.
func TestParse_Integration(t *testing.T) {
	r, err := New()
	if err != nil {
		if errors.Is(err, ErrUnsupportedPlatform) && os.Getenv("APIB_TO_OAS_REQUIRE_DRAFTER") == "" {
			t.Skipf("no drafter binary for %s/%s; skipping", runtime.GOOS, runtime.GOARCH)
		}
		t.Fatalf("init drafter: %v", err)
	}

	// testdata/sample.apib lives at repo root.
	wd, _ := os.Getwd() // .../internal/drafter
	apib := filepath.Join(wd, "..", "..", "testdata", "sample.apib")
	src, err := os.ReadFile(apib)
	if err != nil {
		t.Fatalf("read sample apib: %v", err)
	}

	out, err := r.Parse(context.Background(), src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	s := string(out)
	for _, want := range []string{`"element"`, `"parseResult"`, `Polls API`} {
		if !strings.Contains(s, want) {
			t.Fatalf("expected %q in output:\n%s", want, s)
		}
	}
}

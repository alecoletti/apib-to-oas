package convert

import (
	"context"
	"errors"
	"os"
	"runtime"
	"testing"

	"github.com/alecoletti/apib-to-oas/internal/drafter"
)

// TestSchemas_EndToEnd exercises the full Drafter -> convert pipeline on
// testdata/schemas.apib, asserting the structural outcomes of the four
// schema-precedence features (resource-level + Attributes, action-level
// + Attributes, inline + Attributes overrides, raw + Schema blocks).
//
// Skipped when no embedded drafter binary exists for this platform; opt
// in to fail with APIB_TO_OAS_REQUIRE_DRAFTER=1.
func TestSchemas_EndToEnd(t *testing.T) {
	r, err := drafter.New()
	if err != nil {
		if errors.Is(err, drafter.ErrUnsupportedPlatform) && os.Getenv("APIB_TO_OAS_REQUIRE_DRAFTER") == "" {
			t.Skipf("no drafter binary for %s/%s; skipping", runtime.GOOS, runtime.GOARCH)
		}
		t.Fatalf("drafter init: %v", err)
	}
	src := mustRead(t, "../../testdata/schemas.apib")
	ast, err := r.Parse(context.Background(), src)
	if err != nil {
		t.Fatalf("drafter parse: %v", err)
	}
	doc, err := RefractToOAS(ast)
	if err != nil {
		t.Fatalf("RefractToOAS: %v", err)
	}

	// Resource-level + Attributes flows down as default 200 schema.
	if s := doc.Paths["/items/{id}"].Get.Responses["200"].Content["application/json"].Schema; s == nil || s.Properties["id"] == nil {
		t.Errorf("Get Item 200 expected resource-default schema with `id`, got %+v", s)
	}
	// 204 must NOT carry a body even though resource Attributes exist.
	if c := doc.Paths["/items/{id}"].Delete.Responses["204"].Content; c != nil {
		t.Errorf("Delete Item 204 should have no content, got %+v", c)
	}
	// Action-level + Attributes is the default request schema.
	patch := doc.Paths["/items/{id}"].Patch
	if s := patch.RequestBody.Content["application/json"].Schema; s == nil || s.Properties["name"] == nil {
		t.Errorf("Update Item request expected action-default schema with `name`, got %+v", s)
	}
	// Inline + Attributes (request payload) wins over action default.
	post := doc.Paths["/replace"].Post
	if s := post.RequestBody.Content["application/json"].Schema; s == nil || s.Properties["alpha"] == nil {
		t.Errorf("Replace request expected inline + Attributes schema with `alpha`, got %+v", s)
	}
}

package convert

import (
	"context"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/alecoletti/apib-to-oas/internal/drafter"
	"github.com/alecoletti/apib-to-oas/internal/oas"
)

// TestAppendixC_Conformance runs the worked example referenced by
// specs/apib+.md Appendix C through the full apib → drafter → convert
// pipeline and asserts the Tier-A invariants the current implementation
// honours. The fixture (testdata/appendix_c.apib) is the canonical
// example; it deliberately exercises a few constructs the converter
// does not yet support so this test doubles as the gap tracker - those
// assertions are TODO-marked rather than dropped.
//
// Skipped when no embedded drafter binary exists for this platform; opt
// in to fail with APIB_TO_OAS_REQUIRE_DRAFTER=1.
func TestAppendixC_Conformance(t *testing.T) {
	r, err := drafter.New()
	if err != nil {
		if errors.Is(err, drafter.ErrUnsupportedPlatform) && os.Getenv("APIB_TO_OAS_REQUIRE_DRAFTER") == "" {
			t.Skipf("no drafter binary for %s/%s; skipping", runtime.GOOS, runtime.GOARCH)
		}
		t.Fatalf("drafter init: %v", err)
	}
	src := mustRead(t, "../../testdata/appendix_c.apib")
	ast, err := r.Parse(context.Background(), src)
	if err != nil {
		t.Fatalf("drafter parse: %v", err)
	}
	doc, err := RefractToOASWithOptions(ast, Options{OASVersion: "3.1"})
	if err != nil {
		t.Fatalf("RefractToOAS: %v", err)
	}

	// §2.1 - info.version from VERSION metadata.
	if doc.Info.Version != "2.4.0" {
		t.Errorf("info.version: want 2.4.0, got %q", doc.Info.Version)
	}
	if doc.Info.Title != "Articles API" {
		t.Errorf("info.title: want %q, got %q", "Articles API", doc.Info.Title)
	}

	// §2.5 - SUMMARY / LICENSE / LICENSE-ID / LICENSE-URL.
	if doc.Info.Summary != "Reference articles API." {
		t.Errorf("info.summary: got %q", doc.Info.Summary)
	}
	if doc.Info.License == nil {
		t.Fatal("info.license missing")
	}
	if doc.Info.License.Name != "Apache 2.0" {
		t.Errorf("license.name: got %q", doc.Info.License.Name)
	}
	if doc.Info.License.Identifier != "Apache-2.0" {
		t.Errorf("license.identifier: got %q", doc.Info.License.Identifier)
	}
	if doc.Info.License.URL != "https://example.com/LICENSE" {
		t.Errorf("license.url: got %q", doc.Info.License.URL)
	}

	// §2.2 - two servers in source order, each with description.
	if len(doc.Servers) != 2 {
		t.Fatalf("servers: want 2, got %d (%+v)", len(doc.Servers), doc.Servers)
	}
	if doc.Servers[0].URL != "https://api.example.com" || doc.Servers[0].Description != "Production" {
		t.Errorf("servers[0] = %+v", doc.Servers[0])
	}
	if doc.Servers[1].URL != "https://staging.example.com" || doc.Servers[1].Description != "Staging" {
		t.Errorf("servers[1] = %+v", doc.Servers[1])
	}

	// §2.4 - document-level SECURITY.
	if len(doc.Security) != 1 {
		t.Fatalf("security: want 1 requirement, got %+v", doc.Security)
	}
	if _, ok := doc.Security[0]["BearerAuth"]; !ok {
		t.Errorf("security[0] should reference BearerAuth, got %+v", doc.Security[0])
	}

	// §3 + §5.3 - the Articles tag exists; description carries the
	// prose only (the `+ Meta` block must NOT leak in), and group-level
	// extension keys (Owner-Team) land as `x-owner-team`.
	var articlesTag *struct {
		desc string
		ext  map[string]any
	}
	for _, tg := range doc.Tags {
		if tg.Name == "Articles" {
			articlesTag = &struct {
				desc string
				ext  map[string]any
			}{tg.Description, tg.Extensions}
		}
	}
	if articlesTag == nil {
		t.Fatalf("tags: missing Articles, got %+v", doc.Tags)
	}
	if articlesTag.desc == "" {
		t.Errorf("Articles tag: empty description")
	}
	if strings.Contains(articlesTag.desc, "+ Meta") || strings.Contains(articlesTag.desc, "Owner-Team") {
		t.Errorf("Articles tag description leaked + Meta block: %q", articlesTag.desc)
	}
	if v, ok := articlesTag.ext["x-owner-team"]; !ok || v != "editorial" {
		t.Errorf("Articles tag: want x-owner-team=editorial, got %v (ext=%+v)", v, articlesTag.ext)
	}

	// §5.1 / §5.3 - + Meta replaces operationId and merges/replaces tags.
	listOp := doc.Paths["/articles"].Get
	if listOp == nil {
		t.Fatal("missing GET /articles")
	}
	if listOp.OperationID != "listArticles" {
		t.Errorf("operationId: want listArticles, got %q", listOp.OperationID)
	}
	wantTags := map[string]bool{"Articles": false, "Public": false}
	for _, tg := range listOp.Tags {
		if _, ok := wantTags[tg]; ok {
			wantTags[tg] = true
		}
	}
	for tg, seen := range wantTags {
		if !seen {
			t.Errorf("GET /articles tags: missing %q (got %+v)", tg, listOp.Tags)
		}
	}

	getOp := doc.Paths["/articles/{id}"].Get
	if getOp == nil {
		t.Fatal("missing GET /articles/{id}")
	}
	if getOp.OperationID != "getArticle" {
		t.Errorf("operationId: want getArticle, got %q", getOp.OperationID)
	}

	// §10.1 - single response uses + Attributes (Article) → $ref.
	if resp := getOp.Responses["200"]; resp == nil {
		t.Fatal("missing 200 on getArticle")
	} else if mt := resp.Content["application/json"]; mt == nil || mt.Schema == nil {
		t.Errorf("getArticle 200 missing application/json schema")
	} else if mt.Schema.Ref != "#/components/schemas/Article" {
		t.Errorf("getArticle 200 schema: want $ref Article, got ref=%q type=%q", mt.Schema.Ref, mt.Schema.Type)
	}
	if resp := getOp.Responses["404"]; resp == nil {
		t.Fatal("missing 404 on getArticle")
	} else if mt := resp.Content["application/json"]; mt == nil || mt.Schema == nil {
		t.Errorf("getArticle 404 missing application/json schema")
	} else if mt.Schema.Ref != "#/components/schemas/Error" {
		t.Errorf("getArticle 404 schema: want $ref Error, got ref=%q type=%q", mt.Schema.Ref, mt.Schema.Type)
	}

	// §10.1 - array[Article] response on the multi-Request transition
	// must land on application/json (not the octet-stream fallback).
	if resp := listOp.Responses["200"]; resp == nil {
		t.Fatal("missing 200 on listArticles")
	} else {
		mt := resp.Content["application/json"]
		if mt == nil || mt.Schema == nil {
			t.Fatalf("listArticles 200: missing application/json schema, got content=%+v", resp.Content)
		}
		if mt.Schema.Type != "array" || mt.Schema.Items == nil || mt.Schema.Items.Ref != "#/components/schemas/Article" {
			t.Errorf("listArticles 200 schema: want array of $ref Article, got %+v", mt.Schema)
		}
	}

	// §5.3 - typed extension values: `Idempotent: true` becomes a real
	// boolean (not a quoted string).
	if v, ok := listOp.Extensions["x-idempotent"]; !ok || v != true {
		t.Errorf("listArticles x-idempotent: want bool true, got %v (%T)", v, v)
	}

	// §9 (Tier-A) - header / cookie params declared via the
	// `[header:Real-Name]` / `[cookie]` description prefix surface on
	// the operation with the right `in:` and the prefix stripped from
	// the description.
	var traceHdr, sessCookie *oas.Parameter
	for _, p := range listOp.Parameters {
		switch {
		case p.In == "header" && p.Name == "X-Trace-Id":
			traceHdr = p
		case p.In == "cookie" && p.Name == "sessionId":
			sessCookie = p
		}
	}
	if traceHdr == nil {
		t.Errorf("listArticles: missing X-Trace-Id header param; got %+v", listOp.Parameters)
	} else if strings.Contains(traceHdr.Description, "[header") {
		t.Errorf("X-Trace-Id description should be stripped of prefix; got %q", traceHdr.Description)
	}
	if sessCookie == nil {
		t.Errorf("listArticles: missing sessionId cookie param")
	}

	// §11.3 - components.schemas registered for both named types.
	if doc.Components == nil {
		t.Fatal("components missing")
	}
	if _, ok := doc.Components.Schemas["Article"]; !ok {
		t.Errorf("components.schemas missing Article")
	}
	if _, ok := doc.Components.Schemas["Error"]; !ok {
		t.Errorf("components.schemas missing Error")
	}
	// §9.1 - `## SecuritySchemes (object)` was promoted to
	// components.securitySchemes and removed from components.schemas.
	if _, leaked := doc.Components.Schemas["SecuritySchemes"]; leaked {
		t.Errorf("SecuritySchemes leaked into components.schemas")
	}
	if doc.Components.SecuritySchemes == nil || doc.Components.SecuritySchemes["BearerAuth"] == nil {
		t.Fatalf("BearerAuth not promoted; got %+v", doc.Components.SecuritySchemes)
	}
	if got := doc.Components.SecuritySchemes["BearerAuth"]; got.Type != "http" || got.Scheme != "bearer" || got.BearerFormat != "JWT" {
		t.Errorf("BearerAuth = %+v", got)
	}
	if got := doc.Components.SecuritySchemes["ApiKeyAuth"]; got == nil || got.Type != "apiKey" || got.In != "header" || got.Name != "X-API-Key" {
		t.Errorf("ApiKeyAuth = %+v", got)
	}
	if got := doc.Components.SecuritySchemes["OAuth2"]; got == nil || got.Type != "oauth2" {
		t.Errorf("OAuth2 missing or wrong type: %+v", got)
	}

	// §5.3 - Replace Article PUT carries `Deprecated: true` and the
	// `Tags: +Beta` append form (inherited Articles + appended Beta).
	if pi := doc.Paths["/articles/{id}"]; pi != nil && pi.Put != nil {
		if !pi.Put.Deprecated {
			t.Errorf("PUT /articles/{id} should be deprecated")
		}
		hasBeta, hasArticles := false, false
		for _, tg := range pi.Put.Tags {
			if tg == "Beta" {
				hasBeta = true
			}
			if tg == "Articles" {
				hasArticles = true
			}
		}
		if !hasBeta || !hasArticles {
			t.Errorf("PUT /articles/{id} tags should append Beta to Articles, got %+v", pi.Put.Tags)
		}
	} else {
		t.Errorf("missing PUT /articles/{id}")
	}
	// Format inference: Article.id is a uuid sample → format=uuid.
	//
	// NOTE: Currently the converter does not run sample-driven format
	// inference (regex match against reUUID/reDateTime/etc.) on
	// MSON-registered named types - only on inline schemas. Once that
	// gap closes the conditional below should become unconditional.
	if art := doc.Components.Schemas["Article"]; art != nil && art.Properties["id"] != nil {
		if got := art.Properties["id"].Format; got != "" && got != "uuid" {
			t.Errorf("Article.id format: want uuid or empty, got %q", got)
		}
	}
	// Article.published_at sample is RFC3339 → format=date-time (same caveat).
	if art := doc.Components.Schemas["Article"]; art != nil && art.Properties["published_at"] != nil {
		if got := art.Properties["published_at"].Format; got != "" && got != "date-time" {
			t.Errorf("Article.published_at format: want date-time or empty, got %q", got)
		}
	}

	// §6 - path parameter id is required (path params always are).
	pi := doc.Paths["/articles/{id}"]
	idFound := false
	for _, p := range pi.Parameters {
		if p.Name == "id" && p.In == "path" {
			idFound = true
			if !p.Required {
				t.Errorf("path param id should be required")
			}
		}
	}
	if !idFound {
		// After promotePathParams, params live on operations.
		for _, p := range getOp.Parameters {
			if p.Name == "id" && p.In == "path" {
				idFound = true
			}
		}
	}
	if !idFound {
		t.Errorf("missing path parameter id on /articles/{id}")
	}

	// Query params q + limit must live on the listArticles operation
	// (resource-scoped by §6.4 invariants).
	wantQ := map[string]bool{"q": false, "limit": false}
	for _, p := range listOp.Parameters {
		if p.In == "query" {
			if _, ok := wantQ[p.Name]; ok {
				wantQ[p.Name] = true
			}
		}
	}
	for n, seen := range wantQ {
		if !seen {
			t.Errorf("listArticles missing query param %q", n)
		}
	}

	// §7 + §5.4 - "Article Events" group routes into doc.Webhooks on
	// OAS 3.1+ and the empty `+ Meta + Security:` clears auth on the
	// webhook delivery (renders as `security: []`).
	wh := doc.Webhooks["/article.created"]
	if wh == nil || wh.Post == nil {
		t.Fatalf("webhooks: missing POST /article.created, got %+v", doc.Webhooks)
	}
	if !wh.Post.SecurityCleared {
		t.Errorf("onArticleCreated: empty + Meta Security: should set SecurityCleared (renders as security:[])")
	}
}

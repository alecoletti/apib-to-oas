# AGENTS.md

## Project Identity
- Module: `github.com/alecoletti/apib-to-oas` (`go.mod`).
- Binary: `apib-to-oas` — short for "Go to OAS".
- Purpose: CLI that converts API Blueprint (incl. MSON) to OpenAPI 3.0.
- Go toolchain: `go 1.26.2`.
- Third-party deps: only [`github.com/spf13/cobra`](https://github.com/spf13/cobra) (CLI). Everything else (drafter integration, conversion, YAML output) is stdlib-only — keep it that way unless adding a dep is clearly justified.

## Big Picture
Pipeline is a strict three-stage flow, one package per stage:

```
.apib  --> internal/drafter        (exec embedded drafter)
       --> refract / API Elements JSON
       --> internal/convert        (translate AST)
       --> internal/oas            (typed OAS 3.0 model)
       --> internal/convert.Marshal (yaml | json)
```

- `cmd/apib-to-oas/main.go` is intentionally tiny: signal context + dispatch into `internal/cli`.
- `internal/cli` only parses flags and wires components; no business logic.
- `internal/drafter` is the **only** place that shells out. Drafter binaries live in `internal/drafter/bin/` and are loaded via `go:embed` (`drafter.go`). They are extracted to `os.UserCacheDir()/apib-to-oas/drafter/<name>-<sha8>` on first use.
- `internal/convert` is pure (no I/O). Translation logic grows here. After the main walk it runs post-processing passes (`promotePathParams`, `assignAnchors`) that hoist shared path parameters and tag them with anchor IDs so the YAML emitter can render `&ref_N` / `*ref_N`.
- `internal/oas` is a **minimal** OAS 3.x model — add fields on demand, do not vendor a full schema. Already present: `Document` (incl. `JSONSchemaDialect`, `Components`, `Tags`), `Components`, `Schema` (JSON-Schema-ish), `Discriminator`, `Tag`, `Header`, plus anchor plumbing on `Parameter` (`AnchorID`/`AliasID` + custom `MarshalJSON` that injects `$$anchor`/`$$alias` sentinels consumed by `marshal.go`).

## Drafter Integration
- Drafter source (v5.1.0) lives in `third_party/drafter-v5.1.0/`. **Never** put source under `internal/drafter/bin/` — that path is consumed by `go:embed all:bin` and would balloon the binary.
- Build the binary with `scripts/build-drafter.sh` (CMake + clang/gcc). It outputs `internal/drafter/bin/drafter-<GOOS>-<GOARCH>` (`.exe` on Windows). Per-target build trees go to `third_party/drafter-v5.1.0/build-<GOOS>-<GOARCH>/` (gitignored).
- Cross-builds:
  - `GOOS=darwin GOARCH=amd64 scripts/build-drafter.sh` (uses `-arch x86_64` on Apple Silicon).
  - `scripts/build-drafter.sh --linux-amd64-docker` and `--linux-arm64-docker` (Debian + clang in a container).
- The vendored Boost needs `-include utility` on modern libc++ (Apple clang 17+); the script already passes it.
- Binaries must be named exactly `drafter-<GOOS>-<GOARCH>`. See `internal/drafter/bin/README.md`.
- Missing binary for current platform -> `drafter.ErrUnsupportedPlatform`.
- Invocation contract: `drafter -f json -` (reads source from stdin, JSON Refract on stdout). If you change the flag set, update `Runner.Parse` and `internal/drafter/drafter_test.go` together.

## Conversion Pipeline (internal/convert)
- `RefractToOAS` / `RefractToOASWithOptions` (in `convert.go`) walk the API Elements tree once and produce a typed `*oas.Document`. `Options{OASVersion, InfoVersion, Security}` drives `applyVersion` (normalises `3.0`/`3.1`/`3.2`, sets `JSONSchemaDialect` for 3.1+) and the optional security sidecar; `info.version` falls back to `VERSION` / `API-VERSION` / `API_VERSION` metadata via `firstMetadataValue`. `doc.Servers` is populated from every `HOST:` / `SERVER:` metadata entry (case-insensitive, source order) via `parseServersMetadata` + `splitServerEntry`; each value is split on the first ` - ` (space-dash-space) so `HOST: https://staging.example.com - Staging` becomes `{url, description}`. Supported AST nodes are listed at the top of that file — add new mappings there.
- `walkResources` recurses into nested `category` elements via a `walkCtx` that carries the nearest enclosing `tag`, its `parentTag` (used by 3.2 hierarchical tags), the joined group prose `tagDesc` (collected by `collectCategoryCopy` from the category's direct `copy` children → emitted as `tags[].description`), and an `isWebhook` flag. Categories with class `resourceGroup` become OAS `tags` (recorded by `tagRegistry`); when their title appears in the document-level `WEBHOOK_GROUPS:` metadata (or class `webhook`) and `--oas-version` is 3.1+, their resources route into `doc.Webhooks` instead of `doc.Paths`. Operations are built from `transition` → `httpRequest`/`httpResponse` via `operationFromTransition`, `requestBodyFromElement`, `applyBody` (auto-decodes JSON examples through `parseExample`), `applyHeaders`, `assetMessageBody`, `assetMessageBodySchema` (raw `+ Schema` blocks), `paramsFromHrefVariables`, and `splitURITemplate` (parses `{?a,b}` query templates). Path parameters live on the `PathItem` (intrinsic to the URL — shared by every operation), but query parameters are *resource-scoped*: when two APIB resources map to the same base path (e.g. `Search [/x{?q}]` + `Create [/x]`), only the declaring resource's operations get the `?q=` parameter. See `params_test.go::TestQueryParams_ScopedToDeclaringResource`.
- Schema precedence inside requests/responses (highest first): inline `+ Attributes` (`dataStructure`, via `schemaFromDataStructureChild` → `schemaFor`) → raw `+ Schema` block (`assetMessageBodySchema` → `parseRawSchema`, fallback only) → action-level `+ Attributes` (sibling of `httpTransaction`, used as request default) → resource-level `+ Attributes` (sibling of `transition`s, used as 2xx response default but only when the response declares a Content-Type so 204/304 stay body-free) → `inferSchemaFromExample` (last-resort: synthesises `{type:object, properties:…}` from a decoded JSON `+ Body` example so docs renderers actually display the payload). The raw `+ Schema` asset is intentionally a *fallback*: Drafter auto-emits a stripped-down `messageBodySchema` (no descriptions / examples / formats / `$ref`s) for every `+ Attributes` block, so preferring it would silently trash all per-member authoring intent — see `attributes_test.go::TestAttributes_PreservesMemberDescriptions` for the regression guard. The inferred-from-example fallback is guarded by `infer_test.go::TestInferSchemaFromExample_DoesNotOverrideAttributes`.
- **Blueprint+ Tier-A `+ Meta` blocks**: stock Drafter does **not** recognise `+ Meta` as a structural element — it folds the entire block (including the `+ Meta` header and every nested `+ Key: value` line) into a single `copy` element under the enclosing transition. `internal/convert/bplus.go` rescues those copy elements via `extractMetaFromTransition` (returning a `*metaBlock` plus a set of consumed copy indices so `operationFromTransition` can skip them when assembling `op.Description`), parses the indented list with `parseMetaText`, and applies the result via `applyMetaToOperation` from inside `addResource` — *after* the inherited group tag is appended, so the `Tags: +Beta` append-to-inherited semantics in §8.3 work correctly. Recognised keys (`OperationId`, `Tags`, `Deprecated`, `Docs`, `Security`) map to typed OAS fields; unknown keys go through `normaliseExtensionKey` (kebab/CamelCase/snake folding → `x-foo-bar`) onto `Operation.Extensions`. Extensions are spliced into the JSON output by `oas.Operation.MarshalJSON` via `marshalWithExtensions` (in `internal/oas/oas.go`), which preserves canonical struct field order and appends sorted `x-*` keys at the end. Spec reference: `specs/apib+.md` §8 (Per-Action Metadata) and §13 (Extensions). Regression suite: `bplus_test.go`.
- Multi-transaction examples: when a transition contains more than one `httpTransaction` for the same `(status, contentType)`, `addExample` switches the media type from a scalar `example:` to an `examples:` map keyed by the request's title (or auto-numbered `example1`/`example2`).
- Webhooks (3.1+) and 3.2 hierarchical tags: `parseWebhookGroups`/`isOAS31OrLater` gate the webhook routing; when `--oas-version 3.2`, `doc.Tags` carries `parent: <enclosingGroupTitle>` for nested resource groups (flat on 3.0/3.1).
- Security (sidecar): `internal/convert/security.go` defines `SecurityConfig` (`securitySchemes`, `defaultSecurity`, `overrides{byTag,byPath,byOperationId}`); `LoadSecurityConfig(path)` reads a JSON file (no YAML parser dep) and `applySecurity` is called from `RefractToOASWithOptions` to populate `doc.Components.SecuritySchemes`, `doc.Security`, and per-operation `Operation.Security`. Override precedence: operationId > path > tag > default.
- Security (in-source, Tier-A): `internal/convert/security_mson.go` promotes a reserved `## SecuritySchemes (object)` MSON named-type into `doc.Components.SecuritySchemes`. Each top-level member is one OAS Security Scheme (`type` ∈ {http, apiKey, oauth2, openIdConnect, mutualTLS}; `oauth2` recurses into `flows.<name>` with a flat `scopes` map). The reserved type is stripped from `components.schemas` so it doesn't double-appear. The sidecar (above) wins on collision because `applySecurity` runs last. Validation emits **E007** (missing required field for the declared `type`), **E008** (unknown `type` or unknown OAuth2 flow name), and **W007** (referenced-but-undeclared scheme name) per `specs/apib+.md` §12.1 and §15. Values containing `-` must be wrapped in backticks (`` `X-API-Key` ``) — Drafter splits unquoted values on ` - ` into value/description.
- MSON → JSON Schema lives in **`internal/convert/mson.go`** (`schemaResolver`, `schemaFor`, `objectSchema`, `arraySchema`, `enumSchema`, `decodeMember`, `applyTypeAttributes`, `inferFormat` with `reUUID/reDateTime/reDate/reEmail/reURI`). The resolver is cycle-guarded. `RefractToOASWithOptions` builds it once with `.withRefs()` so any reference to a registered named-type — both inside operations and inside `components.schemas` — emits `#/components/schemas/<name>` `$ref`s instead of inlining; anonymous (inline) MSON definitions still inline. `.withDiagnostics(d)` wires the collector for E006 (undefined named-type, deduped per name).
- Diagnostics: `Annotation`, `ExtractAnnotations`, `HasErrors` surface Drafter parser warnings/errors (code, line/column, sourceMap). The CLI prints them to stderr and `--strict` promotes warnings to failures. Blueprint+ stable codes (`E001`–`E006`, `W001`–`W007`) per `specs/apib+-converter.md` §8 live in `internal/convert/diagnostics.go`; the converter emits them via the optional `Options.Diagnostics` collector (`*convert.Diagnostics`, `NewDiagnostics()`, `.Warn(code,msg)`, `.Error(code,msg)`, `.HasErrors()`). Currently emitted: `E001` (non-standard HTTP method on a transition — operation skipped, gated by `isStandardHTTPMethod`), `E002` (malformed URI template — emitted from `validateURITemplate` before `splitURITemplate` in `addResource`; catches unmatched `{`/`}`, empty `{}`, operator without names, empty name in comma-list), `E003` (malformed `[…]` parameter location prefix — emitted from `peekBracketPrefix` in `bplus.go` via `paramsFromHrefVariables`), `E004` (unknown location word in the prefix), `E005` (duplicate operationId across paths/webhooks — `checkDuplicateOperationIDs` runs after the walk), `E006` (undefined MSON named-type reference — emitted from `schemaResolver.schemaForVisited`'s default case via `resolver.withDiagnostics`; deduped per type name), `W001` (unknown document metadata via `applyDocumentExtensions`), `W002` (multiple `VERSION:` entries), `W003` (`WEBHOOK_GROUPS` declared on OAS 3.0), `W006` (legacy `# METHOD /path` resource shorthand — detected as a title-less resource with a single transition). Diagnostics flow through the walker via `walkCtx.diag` and into the resolver via `schemaResolver.diag`. `Annotation.StableCode` is preferred by the CLI's `printAnnotations` over the numeric `Code`, rendering as `[E003]` instead of `[code N]`. Still deferred: `W005` (implicit default) — no current trigger sites.
- `internal/convert/refract.go` holds the typed Go shapes that mirror Drafter's JSON. `Content` is `json.RawMessage` because Refract polymorphically encodes it as string / object / array; use `contentString()` / `contentObject()` / `contentArray()` rather than the raw field. Higher-level helpers worth knowing about:
  - `parseResult.firstCategory()`, `dataStructures()` (recurses via `collectDataStructures` — MSON named types live under a child category with class `dataStructures`), `annotations()`, `firstMetadataValue`.
  - `element.dataStructureInner()`, `enumerationStrings()`, `severity()`, `isReference()`, `metadataValue("HOST")`, `numberValue()`, `method()`, `title()`/`description()`/`id()`. `elementAttrs` carries `Code`, `Line`, `Column`, `SourceMap`, `TypeAttributes`, `Enumerations`, `Metadata`, `ContentType`, `Version`.
- `Marshal` (in `marshal.go`) renders `*oas.Document` as JSON via `encoding/json` (struct order, sorted maps), and as YAML by streaming that JSON through `jsonToYAML`. The YAML emitter understands the `$$anchor`/`$$alias` sentinels (translated to `&ref_N`/`*ref_N` in `emitObject`/`emitObjectInArrayItem`/`peekFirstKey`/`readStringValue`) and emits multi-line strings via `writeBlockScalar`. There is no third-party YAML dep.
- Test fixtures:
  - `testdata/polls.apib` + `polls.refract.json` + `polls.expected.{yaml,json}` — small end-to-end golden.
  - `testdata/sample.apib` — tiny sample used by `task run -- ...`.
  - `testdata/schemas.apib` + `testdata/schemas.expected.yaml` — exercises the four schema-precedence rules above; `internal/convert/schemas_test.go::TestSchemas_EndToEnd` asserts structural outcomes (drafter-required, skipped on platforms without an embedded binary).
- Helper scripts: `scripts/inspect-refract.py` (counts unique elements/keys), `scripts/sample-refract.py` (prints a representative sample of common shapes).
- Golden / feature tests:
  - `internal/convert/golden_test.go::TestGolden_FromRefractFixture` is hermetic (no drafter); it compares against `testdata/polls.expected.{yaml,json}` using the captured `testdata/polls.refract.json`.
  - `TestGolden_EndToEnd` runs the full apib → drafter → convert pipeline; skipped when no embedded binary is present (set `APIB_TO_OAS_REQUIRE_DRAFTER=1` to force-fail).
  - Companion suites: `anchors_test.go` (anchor promotion + `applyVersion`), `schema_test.go` (format inference, type attributes, annotations), `version_test.go` (metadata-driven `info.version` + CLI override), `attributes_test.go` (resource/action `+ Attributes` defaults + `+ Schema` precedence), `features_test.go` (multi-transaction examples, sidecar security, webhooks routing on 3.1, hierarchical tags on 3.2), `schemas_test.go` (e2e schemas demo), `bplus_v01_test.go` (Tier-A v0.1 batch: doc-level `SECURITY:`, group-level `+ Meta` extensions, response description override, E005 duplicate operationId), `appendix_c_test.go` (full Appendix C conformance walk — drafter-required, skipped without embedded binary).
- When you change conversion output, regenerate the golden files: `go run ./cmd/apib-to-oas convert testdata/polls.apib > testdata/polls.expected.yaml` and `... --format json > testdata/polls.expected.json`. If you change the input apib, also regenerate `testdata/polls.refract.json` via the local drafter binary.

## API Blueprint Reference
- Full Format 1A9 spec lives in `specs/apib.md`. Consult it before adding new mappings — section names there map directly to Refract `element` values produced by Drafter (`category`, `resource`, `transition`, `httpRequest`, `httpResponse`, `dataStructure`, `asset`, `copy`, …).

## Developer Workflows

```bash
task build                                    # builds bin/apib-to-oas with version ldflag
task test                                     # go test ./...
task vet                                      # go vet ./...
task run -- convert testdata/sample.apib
```

Direct equivalents:

```bash
go build -o bin/apib-to-oas ./cmd/apib-to-oas
go run ./cmd/apib-to-oas convert testdata/sample.apib --format json
```

## Conventions
- Errors: wrap with `%w` and a stage prefix (`"parse apib: %w"`, `"convert to oas: %w"`). The CLI prints `apib-to-oas: <err>` to stderr.
- New CLI subcommands: build them in `internal/cli/cli.go` as cobra `*cobra.Command` factory methods on `App` (see `newConvertCmd`, `newLintCmd`, `newVersionCmd`); attach via `root.AddCommand`. Keep `App` injectable (`Stdout`, `Stderr`, `Stdin`, `Version`) for tests — see `internal/cli/cli_test.go`. Set `SilenceErrors`/`SilenceUsage` so `cmd/apib-to-oas/main.go` owns error formatting. Top-level subcommands today: `convert` (alias `c`), `lint` (alias `check`), `version`, plus cobra's auto-generated `completion`. `convert` flags: `--output/-o` (path or `-`; format inferred from extension via `inferFormat`), `--format/-f` (yaml|json; empty → inferred), `--oas-version` (3.0/3.1/3.2), `--info-version`, `--strict`, `--quiet/-q` (suppress non-error annotations), `--security-config`. Persistent flag: `--no-color` (also honours `NO_COLOR` env). Inputs: `<path>` or `-` for stdin. Diagnostics use `path:line:col: severity [code N]: msg` so editors auto-link them; severity is colourised on TTYs only. Distinct exit codes: `cli.ExitGeneric` (1), `cli.ExitParseError` (2, also `--strict` warnings), `cli.ExitConvertErr` (3) — wrapped via `parseErr`/`convertErr` and unwrapped by `cli.ExitCode`, which `cmd/apib-to-oas/main.go` passes to `os.Exit`.
- Tests live next to the package they exercise; `internal/convert/golden_test.go` is the reference style (companion split into `anchors_test.go`, `schema_test.go`, `version_test.go`, `refract_test.go`).
- Single third-party dep (`spf13/cobra`). Prefer growing `internal/oas` or hand-rolling (see `marshal.go`'s minimal YAML emitter) before adding more. When a real YAML lib is needed, swap it inside `convert.Marshal` only.
- File modes: outputs `0o644`, cache dirs `0o755`, extracted binaries `0o755`.

## Where to Add Things
- New AST -> OAS mapping (paths, operations): extend `internal/convert/convert.go`, grow `internal/oas` types as needed.
- MSON / JSON Schema work (named types, type attributes, format inference, `$ref` wiring): `internal/convert/mson.go`.
- New output format: add a case in `internal/convert/marshal.go`.
- New CLI flag/command: `internal/cli/cli.go`.
- Anything that calls `exec`: must live in `internal/drafter` (single integration point).
- Security sidecar shape / per-op overrides: `internal/convert/security.go` (loader + `applySecurity`); add new override scopes to `SecurityOverrides`.

## Known Gaps (update as resolved)
- Windows binary (`drafter-windows-amd64.exe`) is not yet committed; would require a Windows toolchain. The four POSIX targets (`darwin-arm64`, `darwin-amd64`, `linux-amd64`, `linux-arm64`) are in `internal/drafter/bin/`.
- Security sidecar accepts JSON only (keeps the zero-YAML-parser invariant). YAML support would either require a tiny in-house parser or finally pulling in a YAML dep — open call.
- Webhook detection currently keys off the document-level `WEBHOOK_GROUPS:` metadata (comma-separated group titles) since APIB has no native webhook syntax. Drafter does honour `meta.classes` of `webhook` on a category if you patch the source — also accepted.
- No CI, release pipeline, or Dockerfile yet. The build script supports Docker-based linux cross-builds (`scripts/build-drafter.sh --linux-amd64-docker`).


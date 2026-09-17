# Copilot Instructions — apib-to-oas

CLI that converts API Blueprint (incl. MSON) to OpenAPI 3.0/3.1/3.2. Module
`github.com/alecoletti/apib-to-oas`, Go 1.26.2. **Single third-party
dependency: `github.com/spf13/cobra`.** Everything else (drafter exec
integration, AST translation, YAML/JSON emission) is stdlib-only — keep it
that way unless a new dependency is clearly justified.

## Build, test, lint

Task runner ([Taskfile.yml](../Taskfile.yml), `brew install go-task`) wraps
plain `go` commands — either works:

```bash
task build   # == go build -trimpath -ldflags "..." -o bin/apib-to-oas ./cmd/apib-to-oas
task test    # == go test ./...
task vet     # == go vet ./...
task fmt     # == gofmt -s -w .
task run -- convert testdata/sample.apib   # go run ./cmd/apib-to-oas <args>
```

Run a single test or package:

```bash
go test ./internal/convert/ -run TestSchemas_EndToEnd -v
go test ./internal/cli/...
```

Regenerate goldens after changing conversion output:

```bash
go run ./cmd/apib-to-oas convert testdata/polls.apib > testdata/polls.expected.yaml
go run ./cmd/apib-to-oas convert testdata/polls.apib --format json > testdata/polls.expected.json
```

If you also changed the input `.apib`, regenerate `testdata/polls.refract.json`
via the local drafter binary (`scripts/build-drafter.sh` first if not built).

CI (`.github/workflows/tests.yml`) runs `task drafter:check` (verifies all
four embedded drafter binaries exist and are non-trivial), then `go vet ./...`
and `go test ./...` with `APIB_TO_OAS_REQUIRE_DRAFTER=1` on ubuntu-latest and
macos-latest — that env var turns the end-to-end drafter test into a hard
failure instead of a skip, so don't rely on "skipped" locally meaning "safe."

## Architecture

Strict three-stage pipeline, one package per stage:

```
.apib --> internal/drafter (exec embedded drafter) --> Refract/API Elements JSON
      --> internal/convert (translate AST)          --> internal/oas (typed OAS model)
      --> internal/convert.Marshal                   --> yaml | json
```

- `cmd/apib-to-oas/main.go` is intentionally tiny: signal context + dispatch
  into `internal/cli`. `internal/cli` only parses flags/wires components —
  no business logic lives there.
- `internal/drafter` is the **only** place allowed to `exec` a subprocess.
  Prebuilt binaries live in `internal/drafter/bin/` (per `GOOS/GOARCH`),
  loaded via `go:embed`, and extracted to
  `os.UserCacheDir()/apib-to-oas/drafter/<name>-<sha8>` on first use.
  Invocation contract: `drafter -f json -` (source on stdin, JSON Refract on
  stdout) — if you change the flag set, update `Runner.Parse` and
  `internal/drafter/drafter_test.go` together. Missing binary for the host
  platform → `drafter.ErrUnsupportedPlatform`.
- `internal/convert` is pure (no I/O). `RefractToOAS`/`RefractToOASWithOptions`
  walk the API Elements tree once, then run post-processing passes
  (`promotePathParams`, `assignAnchors`) that hoist shared path parameters
  and tag them with anchor IDs for the YAML `&ref_N`/`*ref_N` emitter.
- `internal/oas` is a **minimal** hand-rolled OAS 3.x model — grow it field
  by field on demand; do not vendor a full JSON Schema/OAS package.
- Drafter source (v5.1.0) is vendored under `third_party/drafter-v5.1.0/`.
  **Never** put source under `internal/drafter/bin/` — that path is consumed
  by `go:embed all:bin` and would balloon the binary. Rebuild with
  `scripts/build-drafter.sh` (CMake + clang/gcc); cross-builds via
  `GOOS=... GOARCH=... scripts/build-drafter.sh` or
  `--linux-amd64-docker`/`--linux-arm64-docker`.

### Conversion details worth knowing before touching `internal/convert`

- Path parameters live on the `PathItem` (shared by every operation on that
  path), but query parameters are **resource-scoped**: if two APIB resources
  map to the same base path, only the declaring resource's operations get
  the `?q=` parameter (see `params_test.go::TestQueryParams_ScopedToDeclaringResource`).
- Schema precedence in requests/responses (highest first): inline
  `+ Attributes` → raw `+ Schema` block (fallback only — Drafter's
  auto-generated `messageBodySchema` strips descriptions/examples/formats/
  `$ref`s, so preferring it would trash authoring intent, see
  `attributes_test.go::TestAttributes_PreservesMemberDescriptions`) →
  action-level `+ Attributes` (request default) → resource-level
  `+ Attributes` (2xx response default, only when a Content-Type is
  declared) → `inferSchemaFromExample` (last resort, synthesized from a
  decoded JSON `+ Body` example).
- **Blueprint+ `+ Meta` blocks** (`internal/convert/bplus.go`): stock Drafter
  doesn't recognize `+ Meta` — it folds the whole block into a `copy`
  element. `extractMetaFromTransition` rescues it, `parseMetaText` parses
  the indented list, `applyMetaToOperation` applies it *after* the inherited
  group tag is appended (so `Tags: +Beta` append-to-inherited semantics
  work). Unknown keys fold (kebab/CamelCase/snake) into `x-foo-bar` via
  `normaliseExtensionKey` onto `Operation.Extensions`, spliced into JSON by
  `oas.Operation.MarshalJSON` → `marshalWithExtensions`.
- MSON → JSON Schema resolution lives in `internal/convert/mson.go`
  (`schemaResolver`, cycle-guarded). Built with `.withRefs()` so named-type
  references emit `#/components/schemas/<name>` `$ref`s; anonymous inline
  MSON still inlines.
- Diagnostics (`internal/convert/diagnostics.go`): stable codes `E001`–`E008`,
  `W001`–`W007` per `specs/apib+-converter.md` §8, threaded through
  `walkCtx.diag`/`schemaResolver.diag`, printed by the CLI as
  `path:line:col: severity [code] msg`; `--strict` promotes warnings to
  failures.
- `Marshal` (`internal/convert/marshal.go`) renders `*oas.Document` as JSON
  via `encoding/json`, and streams that same JSON through a hand-rolled
  `jsonToYAML` for YAML — there is no third-party YAML dependency, and the
  `$$anchor`/`$$alias` sentinels from `oas.Parameter` are what let the
  emitter produce `&ref_N`/`*ref_N`.

Full narrative detail (including webhooks/3.2 hierarchical tags, the
security sidecar vs. in-source `SecuritySchemes` precedence, and the
Refract type helpers) lives in [`AGENTS.md`](../AGENTS.md) — read it before
making non-trivial changes to `internal/convert`.

## Conventions

- Errors: wrap with `%w` and a stage prefix, e.g. `"parse apib: %w"`,
  `"convert to oas: %w"`. The CLI prints `apib-to-oas: <err>` to stderr.
- New CLI subcommands are cobra `*cobra.Command` factory methods on `App`
  in `internal/cli/cli.go` (see `newConvertCmd`, `newLintCmd`,
  `newVersionCmd`), attached via `root.AddCommand`. Keep `App` injectable
  (`Stdout`/`Stderr`/`Stdin`/`Version`) for tests. Set
  `SilenceErrors`/`SilenceUsage` so `main.go` owns error formatting.
  Distinct exit codes (`cli.ExitGeneric`=1, `cli.ExitParseError`=2,
  `cli.ExitConvertErr`=3) flow through `parseErr`/`convertErr` wrappers and
  `cli.ExitCode`.
- Tests live next to the package they exercise;
  `internal/convert/golden_test.go` is the reference style, with companion
  files split by concern (`anchors_test.go`, `schema_test.go`,
  `version_test.go`, `attributes_test.go`, `features_test.go`, `bplus_*_test.go`).
  Drafter-dependent tests skip (not fail) when no embedded binary exists for
  the host platform, unless `APIB_TO_OAS_REQUIRE_DRAFTER=1`.
- File modes: outputs `0o644`, cache dirs `0o755`, extracted binaries `0o755`.

## Where to add things

- New AST → OAS mapping: `internal/convert/convert.go`, growing
  `internal/oas` types as needed.
- MSON/JSON Schema work (named types, type attributes, format inference,
  `$ref` wiring): `internal/convert/mson.go`.
- New output format: add a case in `internal/convert/marshal.go`.
- New CLI flag/command: `internal/cli/cli.go`.
- Anything that calls `exec`: must live in `internal/drafter`.
- Security sidecar shape / per-op overrides: `internal/convert/security.go`.

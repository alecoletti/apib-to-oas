# Contributing to apib-to-oas

Thanks for taking the time to contribute!

> **Read [`AGENTS.md`](../AGENTS.md) first.** It's the authoritative
> tour of the codebase: the three-stage pipeline
> (`drafter → convert → oas`), where each concern lives, the stable
> diagnostic codes, and the conventions every PR is reviewed against.
> This file only covers the *workflow* around contributing.

## Quick Start

```bash
git clone https://github.com/alecoletti/apib-to-oas
cd apib-to-oas
task build           # or: go build -o bin/apib-to-oas ./cmd/apib-to-oas
task test            # or: go test ./...
task vet
```

Run the binary against the sample fixture:

```bash
task run -- convert testdata/sample.apib
```

Requirements:

- Go `1.26.2` (see `go.mod`).
- For end-to-end tests that shell out to drafter, an embedded binary
  for your platform must exist under `internal/drafter/bin/`. Tests
  that need it skip cleanly otherwise; set
  `APIB_TO_OAS_REQUIRE_DRAFTER=1` to force-fail instead.

## Where to Add Things

See [`AGENTS.md` → "Where to Add Things"](../AGENTS.md#where-to-add-things).
TL;DR: AST mappings → `internal/convert/convert.go`, MSON / JSON Schema
→ `internal/convert/mson.go`, Blueprint+ extensions →
`internal/convert/bplus.go` + `specs/apib+.md`, CLI →
`internal/cli/cli.go`. Anything that calls `exec` must stay inside
`internal/drafter`.

## Non-Negotiables

- **Single third-party dep**: only `github.com/spf13/cobra`. The YAML
  emitter, JSON walker, and security-sidecar loader are stdlib-only on
  purpose. Open an issue *before* a PR if you think a new dep is
  unavoidable.
- **No business logic in `cmd/` or `internal/cli/`** — those layers
  only parse flags and wire components.
- **New validations** must emit a stable code from
  `internal/convert/diagnostics.go` (`E0xx` / `W0xx`) per
  `specs/apib+-converter.md` §8 — not a bare string. Document the new
  code in `AGENTS.md`.
- **Errors** wrap with `%w` and a stage prefix
  (`"parse apib: %w"`, `"convert to oas: %w"`).

## Updating Goldens

When you change conversion output, regenerate the golden fixtures:

```bash
go run ./cmd/apib-to-oas convert testdata/polls.apib > testdata/polls.expected.yaml
go run ./cmd/apib-to-oas convert testdata/polls.apib --format json > testdata/polls.expected.json
```

If you change the input `.apib`, also regenerate
`testdata/polls.refract.json` using your local drafter binary so the
hermetic golden test stays in sync.

## Pull Requests

1. Fork and branch off `main`. One feature or fix per PR.
2. Include tests. New mappings need a fixture; new diagnostics need a
   regression test asserting the stable code.
3. Run `task test && task vet` before pushing.
4. **Update [`AGENTS.md`](../AGENTS.md)** if you change pipeline
   structure, add a stable diagnostic code, move where something
   lives, or change a public CLI contract. Treat it as part of the
   code — out-of-date `AGENTS.md` is a review blocker.
5. Add a `CHANGELOG.md` entry under `Unreleased`.
6. For Blueprint+ spec changes, update `specs/apib+.md` (and
   `specs/apib+-converter.md` if it adds a diagnostic) in the same PR
   as the implementation.

## Reporting Bugs

Open an issue with:

- The `.apib` input (or a minimal reproduction).
- The exact CLI invocation.
- What you got vs. what you expected.
- `apib-to-oas version` output.

For converter bugs, the captured Refract JSON is gold —
`drafter -f json - < input.apib > input.refract.json` and attach it.

## Proposing a Feature

Open an issue first to discuss scope, especially for:

- New `APIB+` extensions (touches `specs/apib+.md`).
- New CLI flags / subcommands.
- Anything that would add a third-party dependency.

## Code of Conduct

Be kind, assume good faith, and keep discussions focused on the work.
There's a human on the other side of the screen.





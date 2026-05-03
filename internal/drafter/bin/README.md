# Embedded Drafter Binaries

Place prebuilt [Drafter](https://github.com/apiaryio/drafter) binaries in this
directory. They are embedded into the `goas` binary via `go:embed` and
extracted at runtime by `internal/drafter`.

## Naming

Files MUST be named exactly: `drafter-<GOOS>-<GOARCH>` (with `.exe` on
Windows). Examples:

- `drafter-darwin-arm64`
- `drafter-darwin-amd64`
- `drafter-linux-amd64`
- `drafter-linux-arm64`
- `drafter-windows-amd64.exe`

If no binary matches the current platform, `drafter.New()` returns
`ErrUnsupportedPlatform`.

## Build / Source

Drafter is a C++ project. Until a build script is added, drop binaries here
manually (e.g. produced by Drafter's CMake build) and commit them, or fetch
them at release time. Track upstream version + license obligations.

## Stub

`stub.keep` exists only so this directory is committed even when no binaries
are present. It is harmless: the loader filters by exact filename.


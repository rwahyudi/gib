# Repository Guidelines

## Project Structure & Module Organization

This repository is a Go implementation of the `ib` Infoblox DNS CLI.

- `cmd/ib/main.go` is the executable entry point.
- `internal/ibcli/` contains command wiring, config handling, WAPI access, DNS logic, help rendering, Gum prompts, and Lipgloss table output.
- `internal/ibcli/*_test.go` contains unit tests close to the code they exercise.
- `go.mod` and `go.sum` define the Go module and dependency lock state.

Keep new CLI behavior inside `internal/ibcli` unless it is only process startup logic.

## Build, Test, and Development Commands

Use `/tmp` for Go caches in this sandboxed checkout:

- `env GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go test ./...` runs all tests.
- `env GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go build -buildvcs=false -o ib ./cmd/ib` builds a local binary.
- `env GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go build -buildvcs=false -o /home/rwahyudi/bin/ib ./cmd/ib` updates the user-facing command.
- `gofmt -w <files>` formats edited Go files.

For manual checks, prefer focused commands such as `./ib --help`, `./ib dns create --help`, and `./ib dns list`.

## Coding Style & Naming Conventions

Follow standard Go style: tabs from `gofmt`, short package-local helper names, and exported identifiers only when needed outside a package. Use Cobra for command structure, Lipgloss for styled output, and the existing Gum wrapper for interactive prompts. Keep table output styled, but keep `-o json` and `-o csv` plain and machine-readable.

Preserve operator-facing conventions: compact help text, `Current Context: Profile: ... | View: ... | Zone: ...`, HOST-first create examples, and color-coded DNS record types in table output.

## Testing Guidelines

Add or update tests for behavior changes, especially config parsing, WAPI routing, DNS record normalization, completion, and help text. Keep tests deterministic and avoid live Infoblox calls in unit tests. Use fake data or local test servers where network behavior must be verified.

Name tests by behavior, for example `TestDNSContextLineUsesCompactColonFormat`.

## Commit & Pull Request Guidelines

This checkout does not currently expose Git metadata, so no project-specific commit convention can be inferred. Use concise imperative commit messages such as `Colorize DNS record types` or `Compact context help output`.

Pull requests should include a short summary, verification commands, and sample output when CLI formatting changes.

## Security & Configuration Tips

Do not commit real Infoblox hosts, credentials, `~/.ib/config`, `~/.ib/key`, or completion/cache data. Passwords should remain encrypted at rest, and write requests must continue to use the primary server while read-only GET requests may use a configured read server.

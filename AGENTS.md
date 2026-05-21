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

Preserve operator-facing conventions: compact help text, `Current Context: Profile: ... | View: ... | Zone: ...`, type-first DNS write examples, and color-coded DNS record types in table output.

Keep comments attached to non-obvious behavior such as WAPI read/write routing, config validation order, cache freshness/SWR, refresh leases, dynamic completion, and background subprocess handoff. When changing those flows, update nearby comments in the same patch so they describe the current behavior rather than historical intent.

## Testing Guidelines

Add or update tests for behavior changes, especially config parsing, WAPI routing, DNS record normalization, completion, and help text. Keep tests deterministic and avoid live Infoblox calls in unit tests. Use fake data or local test servers where network behavior must be verified.

Name tests by behavior, for example `TestDNSContextLineUsesCompactColonFormat`.

## Separate-Agent Work

When available, use separate agents for independent, bounded work that can run in parallel with the main implementation. Keep each agent's scope explicit and avoid overlapping writes.

Good separate-agent tasks:

- Review-only scan of one subsystem, such as `completion.go` or `cache.go`.
- Find existing tests and fixtures for a behavior before implementation.
- Draft or update documentation examples after code behavior is already known.
- Run focused verification in parallel, such as completion tests while main work edits DNS logic.
- Inspect generated CLI output from a built binary and report mismatches.
- Compare README, man page, packaging completion, and help text for consistency.

Do not delegate:

- The immediate blocking task on the critical path.
- Broad repo exploration with no concrete question.
- Edits to the same files another agent or the main thread is changing.
- Live Infoblox calls or credential-dependent checks.
- Git staging, commits, tags, or pushes.

Separate-agent output contract:

- State files inspected or changed.
- Report exact commands run and whether they passed.
- For review tasks, list findings first with file/line references.
- For implementation tasks, list changed files and any remaining validation gaps.

## Documentation Consistency Checks

For every behavior change, check whether nearby documentation, README examples, man pages, and diagrams still match the code. Use a separate agent when this can run in parallel with implementation.

Docs and assets to check:

- `README.md`
- `NOTES.md`
- `docs/performance-caching.md`
- `docs/assets/*.svg`
- `packaging/man/ib.1`
- `packaging/bash_completion/ib`
- `packaging/rpm/README.md`

When code changes affect commands, flags, output, cache behavior, config prompts, completion, WAPI routing, or packaging, verify related docs in the same patch. Do not update docs speculatively; only change docs when the code behavior is confirmed.

Good separate-agent doc tasks:

- Compare CLI help output against README and man-page examples.
- Check whether cache or WAPI behavior changes require updates to `docs/performance-caching.md` or SVG diagrams.
- Inspect generated completion behavior against `packaging/bash_completion/ib`.
- Verify command examples still use the current type-first DNS write argument order.
- Report stale docs with file/line references and suggested replacement text.

For infographic changes, prefer editing the source SVG text/labels directly and keep terminology aligned with code names such as `record_cache`, `zone_cache`, SWR, refresh leases, read server, and primary server.

## Release Documentation Validation

Before every release, analyze and validate the README installation instructions with extra scrutiny. Confirm Copr, GitHub release tarball, RPM, DEB, Windows ZIP, completion setup, install paths, and asset filenames match `.goreleaser.yaml`, `gib.spec`, packaging files, and the release tag being published.

README install commands should use the latest available release by default. Prefer commands that resolve `https://api.github.com/repos/rwahyudi/gib/releases/latest` or the `/releases/latest` redirect instead of hardcoded version strings. If a release procedure intentionally pins a version, update every README occurrence to the exact tag before publishing and check with `rg 'releases/download/v[0-9]|ib_[0-9]+\\.[0-9]+\\.[0-9]+|version = \"[0-9]+\\.[0-9]+\\.[0-9]+\"' README.md`.

Validate the live release assets when network access is available, for example with `gh release view --json tagName,assets`. Do not rely only on `.goreleaser.yaml`; confirm the latest release actually contains the assets referenced by README install commands.

For release tasks, report the README install-validation result explicitly in the final response or PR notes, including any stale version strings found and fixed.

## Commit & Pull Request Guidelines

After making file changes, validate the relevant behavior, stage only files changed for the current task, and commit automatically with a concise imperative message. Report the commit hash in the final response.

Pause instead of committing only when validation fails, unrelated worktree changes conflict with the task, or the user explicitly says not to commit. Use messages such as `Colorize DNS record types`, `Compact context help output`, or `Document parallel agent workflows`.

Pull requests should include a short summary, verification commands, and sample output when CLI formatting changes.

## Security & Configuration Tips

Do not commit real Infoblox hosts, credentials, `~/.ib/config`, `~/.ib/key`, or completion/cache data. Passwords should remain encrypted at rest, and write requests must continue to use the primary server while read-only GET requests may use a configured read server.

# Release Notes

## v0.4.2 - 2026-07-28

### DNS and Cache Behavior

- `ib dns create host` now invalidates and refreshes the matching reverse-zone record cache after a successful HOST write.
- `ib dns delete` matches forward DNS record names case-insensitively, including mixed-case names and zones. PTR cleanup also matches PTR targets case-insensitively; direct PTR deletion remains IP-based.

### Search Performance

- Multi-zone search submits the active DNS zone first when it is in scope. Recursive searches submit their resolved root zone first.
- Remaining zone jobs are submitted by descending cached record count, with a lexical zone-name tie-breaker. Search result ordering is unchanged.

### Packaging

- Source builds, the RPM spec, and the manual page now report version `0.4.2`.

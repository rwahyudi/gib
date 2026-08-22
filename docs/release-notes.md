# Release Notes

## Unreleased

### DNS Reliability and Auditing

- Typed `ib dns edit` and `ib dns delete` lookups query the requested record type first, avoiding unnecessary WAPI scans; the broader lookup is retained only to report an actionable record-type mismatch.
- Optional DNS audit logging now records failed create, edit, and delete WAPI attempts, including explicit PTR operations, as redacted JSON Lines events with `result: "failure"`. A forward success followed by a PTR sync failure remains represented by separate events.

## v0.4.3 - 2026-08-08

### Reliability and Safety

- WAPI transport now applies consistent request timeouts, validates response content, and avoids unsafe handling of malformed or unexpected responses.
- DNS and network workflows add safer request handling and clearer operator-facing failures.

### Search and Cache Performance

- Detached DNS record-cache revalidation now releases Badger before slow WAPI serial and record requests, reducing lock contention for repeated global DNS searches.
- Background refresh plans verify that the cached snapshot has not changed before writing, so foreground cache updates are not overwritten by delayed refresh results.

### Network Output

- Network list and detail output now uses clearer, consistent headers for operator readability.

## v0.4.2 - 2026-07-28

### DNS and Cache Behavior

- `ib dns create host` now invalidates and refreshes the matching reverse-zone record cache after a successful HOST write.
- `ib dns delete` matches forward DNS record names case-insensitively, including mixed-case names and zones. PTR cleanup also matches PTR targets case-insensitively; direct PTR deletion remains IP-based.

### Search Performance

- Multi-zone search submits the active DNS zone first when it is in scope. Recursive searches submit their resolved root zone first.
- Remaining zone jobs are submitted by descending cached record count, with a lexical zone-name tie-breaker. Search result ordering is unchanged.

### Packaging

- Source builds, the RPM spec, and the manual page now report version `0.4.2`.

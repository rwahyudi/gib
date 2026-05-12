# Notes

## Code Comment Maintenance

Keep code comments focused on behavior that is easy to break or misunderstand: WAPI GET-versus-write routing, config validation order, cache freshness and stale-while-revalidate decisions, refresh leases, dynamic completion, and detached refresh subprocess handoff.

When changing one of those flows, update the nearby code comment in the same patch. Comments should explain why the behavior exists and what must stay in sync, not restate obvious Go statements.

## README Maintenance

The README includes a concise product blurb, bullet-point Features section, module table, global-switch summary, library links, and security-scanner links. Keep it in sync when adding or removing major CLI behavior, but avoid duplicating the detailed command reference.

## Security Scanning

Security scanning lives in GitHub Actions and Dependabot. Keep README security commands in sync when adding or changing `go test`, `govulncheck`, `gosec`, Trivy, or dependency update automation.

## Release Maintenance

GitHub releases are tag-driven through GoReleaser. The release workflow publishes Linux amd64 tarball, RPM, DEB, and checksum assets. RPM and DEB packages install `/usr/local/bin/ib` and `/etc/bash_completion.d/ib`. Keep README install commands, `.goreleaser.yaml`, and packaged completion paths in sync when changing release behavior.

## Performance and Caching Docs

`docs/performance-caching.md` explains cache freshness, stale-while-revalidate, read/write routing, search workers, and SQLite cache tables. Keep that page plus `docs/assets/cache-decision-flow.svg`, `docs/assets/cache-workers.svg`, and `docs/assets/sqlite-cache-tables.svg` in sync when cache behavior, read-server routing, worker behavior, or cache schema changes. The diagrams use a dark Nord, angular, gradient style with compact boxes, small arrowheads, and small animated traffic markers only on the read/write worker flow.

## Global Cache and Search Settings

The config file stores global cache/search tuning in the `[meta]` section:

```ini
cache_ttl = 300
dns_search_worker_limit = 16
records_cache_swr_ttl = 259200
```

`cache_ttl` is in seconds and controls both zone-list cache entries and per-zone record-cache entries. Freshness is calculated from `cached_at + cache_ttl`; the cache does not store a separate fresh-expiry timestamp. If the setting is missing or invalid, `ib` uses and writes the default value of `300`.

`dns_search_worker_limit` controls how many zones `ib dns search --global` can load in parallel. If the setting is missing or invalid, `ib` uses and writes the default value of `16`.

`records_cache_swr_ttl` is in seconds and controls how long expired per-zone record-cache entries can be served stale while `ib` refreshes them in the background. If the setting is missing or invalid, `ib` uses and writes the default value of `259200` seconds (3 days).

## Config Profiles

`ib config delete PROFILE` removes the non-default profile from the config file and clears local zone-cache, record-cache, and record-refresh lock rows for that profile. Cache rows for other profiles are left intact.

`ib config new` and `ib config edit` Step 05 (`Read Endpoint`) automatically discovers Grid Master Candidates from the primary Grid Master. Candidates with Read-Only API disabled are reported with an indented green `INFO:` line and are not saved. Candidates with Read-Only API enabled must also pass a direct WAPI GET probe before being saved as `read_server`. If no candidate exists or no candidate passes the probe, `read_server` is left blank so both reads and writes use the primary server.

When `read_server` is set, the WAPI client routes GET requests to the GCM read endpoint and keeps POST, PUT, and DELETE requests on the primary server.

During `ib config new` and `ib config edit`, DNS View and Default DNS Zone are only prompted when there are multiple choices. If Infoblox returns exactly one DNS view, that view is selected automatically. If exactly one eligible primary forward zone remains after filtering out secondary zones, that zone is selected automatically.

## DNS Search Progress

For interactive table output, `ib dns search` uses a Bubble Tea progress view on stderr while the search is running. The view shows the search stage, configured worker count, completed zones, match count, and each worker's current zone/cache source. The final record table is still printed normally on stdout after the progress view exits.

The worker state `Checking cache` covers the whole per-zone record load until the worker finishes. That includes opening the SQLite cache, reading and decoding cached JSON records, checking fresh/stale expiry, acquiring the stale-while-revalidate refresh lease and launching the detached refresh subprocess when needed, and, for entries outside SWR, doing foreground serial or `/allrecords` refresh work.

If many global-search workers sit at `Checking cache`, inspect whether most per-zone record caches are stale. A stale-but-inside-SWR cache hit returns cached records after local lease acquisition and detached subprocess launch, but it does not wait for Infoblox serial checks or `/allrecords`. The worker label remains visible while local cached data is read, JSON-decoded, normalized, de-duplicated, sorted, and handed to the matcher, so large cached zones can still spend noticeable time in this state without waiting on Infoblox.

For non-interactive stderr, `-o json`, or `-o csv`, the progress view is disabled so scripts and machine-readable output remain clean.

## DNS List and Search Scope

`ib dns list [ZONE]` lists only the resolved current zone by default. Use `-r` or `--recursive` to include child authoritative zones under that zone. Use `-t`/`--type` to filter comma-separated record types, `-e`/`--exclude` to hide records whose name, value, or comment matches the excluded keyword, and `-s`/`--sort` to sort by `name`, `type`, `value`, `zone`, `ttl`, or `comment`.

`ib dns search KEY` searches only the resolved current zone by default. Use `-z ZONE` to choose a different root zone, and add `-r` or `--recursive` to include child authoritative zones under that root. `--global` still searches every searchable zone in the selected DNS view and cannot be combined with `--recursive`. Search supports the same `-t`/`--type`, `-e`/`--exclude`, and `-s`/`--sort` record filters as list.

For record sorting, a blank `--sort` uses `name`, and a leading minus sorts descending, for example `--sort=-name` or `-s -ttl`. The default sort order is unchanged when `--sort` is omitted.

All `ib dns` subcommands inherit `--zone`/`-z` and `--view`/`-v`. These are per-command context overrides and take precedence over `ib dns zone use`, `ib dns view use`, `IB_ZONE`, `IB_VIEW`, and configured defaults without saving anything to the profile.

DNS record table output always includes a `Current Context:` footer line. When the table has more than five records, the `Total records` badge is shown on the same line.

`ib dns delete NAME` prompts with a Charmbracelet Huh confirmation before deleting a selected record. Use `-y` or `--yes` to skip the confirmation. If the user cancels either the duplicate-record picker or confirmation prompt, `ib` prints `INFO: delete cancelled` and exits without issuing DELETE. If multiple forward records match the same FQDN, interactive table mode first uses a Charmbracelet Huh select picker showing type, name, value, zone, comment, and `_ref`; the selected record is then confirmed before DELETE. Non-interactive mode and `-o json`/`-o csv` fail safely unless `-y` is provided.

When WAPI returns HTML or another non-JSON payload with a successful HTTP status, `ib` reports a contextual WAPI error instead of the raw Go JSON parser error. This most often means the configured server, WAPI version, credentials, reverse proxy, or login/SSO page is answering the request rather than the Infoblox WAPI JSON endpoint.

## Shell Completion

`ib config completion bash`, `ib config completion zsh`, and `ib config completion fish` emit lightweight shell integrations that call the live `ib` binary during tab completion. Profiles, zones, records, flags, and output formats are resolved dynamically by `ib __complete` or `ib __completeNoDesc`, so users do not need to regenerate completion scripts when profile, zone, or record data changes. Regenerate the shell integration only when the completion script template itself changes.

`ib dns search KEY -t <tab><tab>` and `ib dns list -t <tab><tab>` complete supported record type filters such as `a`, `host`, and `txt`. Comma-separated filters are completed from the current segment, so `-t a,` offers remaining types as `a,host`, `a,txt`, and so on.

`ib dns search KEY -s <tab><tab>` and `ib dns list -s <tab><tab>` complete sort fields in ascending and descending forms, including `name`, `type`, `value`, `zone`, `ttl`, `comment`, `-name`, and `-ttl`.

For Bash, `ib <tab><tab>` should complete root commands such as `config`, `dns`, and `help`. If it does not, regenerate and reload the shell integration with `ib config completion bash > ~/.ib-complete.bash` and start a new shell or run `. ~/.ib-complete.bash`.

For Bash, `ib dns create <tab><tab>` prints the `dns create` usage/help under the prompt, then redraws `ib dns create ` without inserting a placeholder candidate. This behavior lives in the generated Bash completion wrapper, so regenerate and reload `~/.ib-complete.bash` after changing the wrapper template.

Global options still complete while using `ib dns create`: `ib dns create -<tab>` offers options such as `--output`, `-o`, and `--help`, and output format values complete after `--output` or `-o`.

For `ib config new` and `ib config edit`, question 7 (`Default DNS Zone`) uses the Bubble Tea filter list when there are multiple choices and keeps an eight-row zone list area visible even when fewer rows are currently matched. Question 6 (`Default DNS View`) still sizes to the available DNS view choices when a picker is needed.

## Zone Record Cache Workflow

Zone record data is cached per profile, DNS view, and zone in the local SQLite cache.

Successful `ib dns create`, `ib dns edit`, and `ib dns delete` operations remove the affected zone's record-cache row and synchronously launch the detached refresh subprocess when no matching refresh lease is active. The write command does not wait for `/allrecords`; the subprocess repopulates the cache in the background. A/AAAA workflows that also create or update PTR records queue refreshes for both the forward zone and the reverse zone. Successful DNS zone create/delete operations also clear and refresh the zone-list cache in the background; deleting a zone removes that zone's record cache instead of trying to refresh records for a zone that no longer exists.

When a command queries zone records:

1. If a record-cache entry exists and has not expired, `ib` returns the cached records without checking Infoblox.
2. If the entry has expired but is still inside `records_cache_swr_ttl`, `ib` returns the stale cached records immediately without checking Infoblox.
3. For that stale response, `ib` synchronously checks whether a refresh is already running for the same profile, DNS view, and zone. If one is running, `ib` does not start another subprocess.
4. If no matching refresh is running, `ib` starts a detached `ib config cache revalidate-record` subprocess for that profile, DNS view, and zone before returning the stale data.
5. The stale response does not wait for Infoblox serial checks, `/allrecords`, or subprocess completion.
6. The background subprocess asks Infoblox for the zone SOA serial number.
7. If the cached serial matches the server serial, `ib` treats the cached data as still valid, renews the cached age timestamp, recomputes normal freshness from `cached_at + cache_ttl`, and extends the stale expiry by `records_cache_swr_ttl`.
8. If the serial changed, if the cache entry is missing, or if the cache entry has no serial to compare, the background subprocess downloads fresh `/allrecords` data, stores the new serial when available, and resets all record-cache timestamps.
9. If an expired entry is already outside `records_cache_swr_ttl`, `ib` performs the serial check in the foreground. Matching serials renew `cached_at`, making the cache fresh again under the current `cache_ttl`; changed or missing serials refresh from `/allrecords`.

The in-flight background refresh marker is stored in the local SQLite cache and expires automatically after 300 seconds. This prevents repeated `ib dns list` or `ib dns search` calls from starting duplicate refreshes while still allowing recovery if a refresh subprocess exits unexpectedly.

`ib dns list --details` may enrich fresh cached rows with per-record detail calls when TTL/detail fields are missing. Stale SWR responses are returned exactly as cached; the background revalidation updates the cache separately.

`ib dns search` uses the same record-cache workflow. It first loads the searchable zone list, skips secondary zones, then loads records from cache or `/allrecords` for the current zone, recursive child-zone scope, or global scope. Multi-zone searches use `dns_search_worker_limit` to bound parallel zone loading.

`ib config cache status` shows cache age and record stale expiry, but not a `fresh_until` column. Freshness is calculated dynamically from each row's `cached_at` timestamp and the current `cache_ttl` setting. Table output keeps the detailed row table and adds a colored statistics footer with cache entries, cached records, fresh entries, SWR-stale entries, expired entries, and active refreshes. JSON output returns `statistics` plus `entries`; CSV output remains row-only for scripts.

## Notes Maintenance

When CLI behavior, config settings, cache workflow, operator-facing output, or troubleshooting guidance changes, update `NOTES.md` in the same work pass. Keep notes concise and operational; explanatory-only or review-only prompts only need a notes update when they establish durable behavior that future work should preserve.

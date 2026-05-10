# Performance & Caching

`ib` is built for large Infoblox DNS zones. List and search commands prefer a
local SQLite cache, use `/allrecords` to avoid one request per record type, and
run multi-zone searches with a bounded worker pool.

## Quick Model

| Area | Behavior |
| --- | --- |
| Cache scope | Cache rows are keyed by profile, DNS view, and zone. |
| Freshness | Fresh until `cached_at + cache_ttl`; `fresh_until` is not stored. |
| Stale window | Record rows can be served stale until `stale_expires_at`. |
| Revalidation | Stale rows inside SWR return immediately and start one background refresh. |
| Read endpoint | GET requests use `read_server` when configured. |
| Write endpoint | POST, PUT, and DELETE always use the primary Grid Master. |
| Workers | Global and recursive search load multiple zones in parallel, limited by `dns_search_worker_limit`. |

Default tuning in the profile config `[meta]` section:

| Setting | Default | Meaning |
| --- | ---: | --- |
| `cache_ttl` | `300` seconds | Normal freshness for zone and record cache rows. |
| `records_cache_swr_ttl` | `259200` seconds | How long expired record rows can be served stale while revalidating. |
| `dns_search_worker_limit` | `16` | Maximum parallel zone workers during multi-zone search. |
| refresh lease TTL | `300` seconds | Local lock lifetime that prevents duplicate refresh subprocesses. |

## Cache Decision Flow

```mermaid
flowchart TD
    A[ib dns list/search asks for zone records] --> B[Read record_cache by profile + view + zone]
    B --> C{Cache row found?}
    C -- No --> H[Foreground SOA serial check]
    C -- Yes --> D{now < cached_at + cache_ttl?}
    D -- Yes --> E[Return fresh cached records]
    D -- No --> F{now < stale_expires_at?}
    F -- Yes --> G[Return stale cached records immediately]
    G --> G1[Try refresh lease]
    G1 --> G2{Lease acquired?}
    G2 -- Yes --> G3[Start detached revalidate-record subprocess]
    G2 -- No --> G4[Existing refresh continues]
    F -- No --> H
    H --> I{Cached serial matches server serial?}
    I -- Yes --> J[Renew cached_at and stale_expires_at]
    J --> K[Return cached records]
    I -- No --> L[Download fresh /allrecords]
    L --> M[Store payload_json, serial, cached_at, stale_expires_at]
    M --> N[Return fresh records]
```

The important performance point is the stale-while-revalidate path: if a record
cache row is expired but still inside `records_cache_swr_ttl`, the user gets
cached records immediately. `ib` only blocks on Infoblox when the row is missing
or already outside the stale window.

## Read, Write, And Worker Flow

![Animated read/write and worker cache flow](assets/cache-workers.svg)

```mermaid
sequenceDiagram
    participant User
    participant CLI as ib CLI
    participant Cache as SQLite cache
    participant Read as GCM read endpoint
    participant Primary as Primary Grid Master
    participant Refresh as Refresh subprocess

    User->>CLI: ib dns search --global app
    CLI->>Cache: load searchable zones and per-zone records
    par Worker 1
        CLI->>Cache: check zone A cache
    and Worker 2
        CLI->>Cache: check zone B cache
    and Worker 3
        CLI->>Cache: check zone C cache
    end
    alt fresh cache
        Cache-->>CLI: records
    else stale inside SWR
        Cache-->>CLI: stale records immediately
        CLI->>Refresh: start if refresh lease acquired
        Refresh->>Read: GET zone SOA serial
        alt serial changed
            Refresh->>Read: GET /allrecords
            Refresh->>Cache: replace record cache
        else serial unchanged
            Refresh->>Cache: renew cached_at and stale_expires_at
        end
    else cache missing or outside SWR
        CLI->>Read: GET zone SOA serial
        CLI->>Read: GET /allrecords when needed
        CLI->>Cache: write record cache
    end

    User->>CLI: ib dns edit app host 192.0.2.20
    CLI->>Primary: PUT record
    CLI->>Cache: delete affected zone cache
    CLI->>Refresh: start background refresh
```

Read-only traffic can use a Grid Master Candidate when `ib config new/edit`
finds one that supports read-only WAPI access. Writes never use that endpoint:
create, edit, delete, and zone mutation commands stay on the primary Grid Master.

## What The Workers Do

For a global search, `ib` first loads the searchable zone list, filters out
secondary zones, and then assigns zones to workers. Each worker does the same
per-zone record load:

1. Open the SQLite cache with a single DB connection and `busy_timeout`.
2. Read the zone's `record_cache` row.
3. Decode JSON records when a cache row exists.
4. Decide fresh, stale-inside-SWR, or expired-outside-SWR.
5. Acquire a refresh lease and launch a detached refresh only when needed.
6. Normalize, deduplicate, sort, and match records by name, value, and comment.

The progress label `Checking cache` covers all of that local work. It can still
take visible time for large cached zones because JSON decoding and record
normalization happen before matching.

## SQLite Cache Tables

```mermaid
erDiagram
    CACHE_META {
        text key PK
        text value
    }
    ZONE_CACHE {
        text cache_key PK
        text profile
        text view
        integer cached_at
        text payload_json
    }
    RECORD_CACHE {
        text cache_key PK
        text profile
        text view
        text zone
        text zone_serial
        integer cached_at
        integer stale_expires_at
        text payload_json
    }
    RECORD_REFRESH_LOCKS {
        text cache_key PK
        text profile
        text view
        text zone
        integer started_at
        integer expires_at
    }
```

| Table | Purpose | Key columns |
| --- | --- | --- |
| `cache_meta` | Stores cache schema metadata. | `key`, `value` |
| `zone_cache` | Caches authoritative zone list payloads per profile/view. | `profile`, `view`, `cached_at`, `payload_json` |
| `record_cache` | Caches `/allrecords` payloads per profile/view/zone. | `profile`, `view`, `zone`, `zone_serial`, `cached_at`, `stale_expires_at`, `payload_json` |
| `record_refresh_locks` | Prevents duplicate background refreshes for the same profile/view/zone. | `profile`, `view`, `zone`, `started_at`, `expires_at` |

`zone_cache` and `record_cache` store Infoblox payloads as JSON text. The CLI
normalizes those payloads into typed records when listing, searching, completing,
or displaying records.

## Cache Updates After Changes

Successful record create, edit, and delete operations remove the affected
zone's record cache row and launch a background revalidation. A/AAAA workflows
that also update PTR records queue refreshes for both the forward and reverse
zones.

Successful DNS zone create and delete operations refresh the zone-list cache in
the background. Deleting a zone removes that zone's record cache instead of
trying to refresh records for a zone that no longer exists.

Use these commands when troubleshooting cache behavior:

```bash
ib config cache status
ib config cache clear
```

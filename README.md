# gib

Go implementation of the `ib` Infoblox DNS CLI.

## Build And Test

Use `/tmp` for Go caches in this sandboxed checkout:

```bash
env GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go test ./...
env GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go build -buildvcs=false -o ib ./cmd/ib
env GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go build -buildvcs=false -o /home/rwahyudi/bin/ib ./cmd/ib
```

## Setup

Create or edit an Infoblox profile:

```bash
ib config new --default
ib config edit
ib config list
```

Profiles store the primary server, optional read server, credentials, WAPI version, DNS view, and default zone. Passwords are encrypted at rest. Do not commit `~/.ib/config`, `~/.ib/key`, or cache data.

## DNS Context

DNS commands use this context order:

```text
command --zone/--view -> ib dns zone/view use -> IB_ZONE/IB_VIEW -> configured defaults
```

Override context for one command without saving it:

```bash
ib dns --zone example.com --view "DNS Zone View" list
ib dns --zone example.com create app host 192.0.2.10 -c "Application host"
ib dns --view "DNS Zone View" search app
```

## Common Commands

```bash
ib dns view list
ib dns view use "DNS Zone View"
ib dns zone list
ib dns zone use example.com
ib dns list
ib dns search app
ib dns create app host 192.0.2.10 -c "Application host"
ib dns edit app host 192.0.2.20 -t 300 -c "Application host"
ib dns delete app
```

`ib dns list` and `ib dns search` operate on the current zone by default. Add `-r` or `--recursive` to include child zones. `ib dns search --global` searches every searchable zone in the selected view.

`ib dns delete` prompts before deleting. Use `-y` or `--yes` to skip the confirmation. If multiple records match, interactive table mode shows a Huh select list so one record can be chosen.

## Cache

Zone and record caches are stored in `~/.ib/cache.sqlite3`.

Record cache freshness uses `cached_at + cache_ttl`. Expired records inside `records_cache_swr_ttl` are returned immediately while a single background refresh process revalidates the zone serial and refreshes `/allrecords` when needed.

Useful cache commands:

```bash
ib config cache status
ib config cache clear
```

Deleting a profile also clears local cache rows for that profile.

## Completion

Generate dynamic shell completion:

```bash
ib config completion bash > ~/.ib-complete.bash
. ~/.ib-complete.bash
```

The generated completion calls the live `ib` binary, so profiles, zones, records, flags, and output formats are resolved dynamically.

In Bash, pressing `<tab><tab>` after `ib dns create ` prints the create usage and redraws `ib dns create ` so the empty name slot is still clear.

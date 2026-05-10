# gib

`gib` builds the `ib` command, a fast operator-focused CLI for managing
Infoblox DNS records without living in the web UI. It keeps day-to-day DNS work
close to the shell: profile setup, view and zone context, record search, record
changes, cache inspection, and shell completion all happen from one compact
command surface.

The CLI is designed for large Infoblox environments. Read-heavy workflows use a
validated Grid Master Candidate when available, record listing and search use a
local SQLite cache backed by `/allrecords`, and stale-while-revalidate keeps
search responsive while background refreshes check zone serials and update
cache rows.

## Features

- Profile management for creating, editing, switching, and deleting Infoblox
  profiles with encrypted local passwords.
- Safe read/write routing: GET requests can use a validated GCM read endpoint,
  while POST, PUT, and DELETE stay on the primary Grid Master.
- DNS context from configured defaults, shell-session view/zone context,
  environment variables, or one-command `--view` and `--zone` overrides.
- DNS record workflows for listing, searching, creating, editing, and deleting
  records, including interactive duplicate selection and confirmation.
- Large-zone performance through `/allrecords`, local SQLite caching,
  worker-limited global search, and stale-while-revalidate refreshes.
- Dynamic shell completion for profiles, views, zones, records, flags, record
  types, and output formats from the live `ib` binary.
- Compact operator output with colorful tables, current-context footers,
  JSON/CSV output, and progress display for larger searches.

## Security Scanning

GitHub Actions runs tests,
[govulncheck](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck),
[gosec](https://github.com/securego/gosec), and
[Trivy](https://github.com/aquasecurity/trivy) filesystem scans on pushes,
pull requests, and a weekly schedule.
[Dependabot](https://docs.github.com/en/code-security/dependabot) monitors Go
modules and GitHub Actions updates weekly.

Run the same checks locally when the tools are installed:

```bash
env GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go test ./...
env GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod govulncheck ./...
gosec ./...
trivy fs --scanners vuln,secret,license .
```

## Installation From GitHub Release

Download packages from the [latest GitHub release](https://github.com/rwahyudi/gib/releases/latest).

Standalone binary:

```bash
curl -LO https://github.com/rwahyudi/gib/releases/download/v0.1.0/ib_0.1.0_linux_amd64.tar.gz
tar -xzf ib_0.1.0_linux_amd64.tar.gz ib
sudo install -m 0755 ib /usr/local/bin/ib
ib --help
```

RHEL derivatives:

```bash
curl -LO https://github.com/rwahyudi/gib/releases/download/v0.1.0/ib_0.1.0_linux_amd64.rpm
sudo dnf install ./ib_0.1.0_linux_amd64.rpm
ib --help
```

Debian derivatives:

```bash
curl -LO https://github.com/rwahyudi/gib/releases/download/v0.1.0/ib_0.1.0_linux_amd64.deb
sudo apt install ./ib_0.1.0_linux_amd64.deb
ib --help
```

RPM and DEB packages install `ib` to `/usr/local/bin/ib` and install Bash completion to `/etc/bash_completion.d/ib`. Open a new shell after package installation to load completion.

## Setup

Create or edit an Infoblox profile:

```bash
ib config new --default
ib config edit
ib config list
```

Profiles store the primary server, auto-detected GCM read endpoint when available, credentials, WAPI version, DNS view, and default zone. If Infoblox returns only one DNS view or one eligible primary forward zone, config selects it automatically. Passwords are encrypted at rest. Do not commit `~/.ib/config`, `~/.ib/key`, or cache data.

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

## Global Switches

- `-o, --output table|json|csv` is available from the root command and applies
  to every command. Use `json` or `csv` for scripts.
- `-z, --zone ZONE` and `-v, --view VIEW` are available on `ib dns` and its
  subcommands. They override the current DNS context for one command only.
- `-g, --global` is a search scope switch for `ib dns search`; it searches every
  searchable zone in the selected DNS view.

## Modules

| Module | Purpose | Start here |
| --- | --- | --- |
| `config` | Manage profiles, encrypted credentials, completion, and local cache. | `ib config new --default` |
| `dns` | Manage Infoblox DNS views, zones, records, searches, and context overrides. | `ib dns list` |

## How It Works

`cmd/ib/main.go` starts the Cobra CLI and hands command behavior to `internal/ibcli`. Profile loading decrypts the stored password, resolves the current DNS view/zone, and builds a WAPI client. GET requests can use a configured GCM read endpoint, while create, update, and delete requests always use the primary server.

DNS listing and search prefer local SQLite cache rows. Freshness is calculated from `cached_at + cache_ttl`; stale rows inside `records_cache_swr_ttl` are returned immediately while one detached refresh process revalidates the zone serial and refreshes `/allrecords` when required.

Code comments are intentionally concentrated around routing, config validation, cache/SWR, leases, completion, and background refresh handoff. Update those comments in the same change whenever the related behavior changes.

For a deeper explanation with diagrams, see [Performance & Caching](docs/performance-caching.md), which includes Nord-styled cache decision, read/write worker-flow, and SQLite table diagrams.

## Libraries Used

- [Cobra](https://github.com/spf13/cobra) provides the command tree, flags, and
  shell completion protocol.
- [pflag](https://github.com/spf13/pflag) handles POSIX-style long and short
  flags underneath Cobra.
- [Lipgloss](https://github.com/charmbracelet/lipgloss) styles tables, context
  footers, and operator-facing messages.
- [Bubble Tea](https://github.com/charmbracelet/bubbletea) and
  [Bubbles](https://github.com/charmbracelet/bubbles) power interactive progress
  and list-style terminal UI.
- [Huh](https://github.com/charmbracelet/huh) provides confirmation and select
  prompts for destructive or ambiguous actions.
- [go-sqlite3](https://github.com/mattn/go-sqlite3) stores local zone and record
  cache data in SQLite.
- [go-isatty](https://github.com/mattn/go-isatty) detects interactive terminals
  so scripts keep clean output.
- [GoReleaser](https://goreleaser.com/) builds release binaries plus RPM and DEB
  packages.

## Command Reference

### Config

| Command | Description |
| --- | --- |
| `ib config` | Show profile overview and short usage. |
| `ib config new [PROFILE]` | Create a profile; validates primary access, auto-detects a usable GCM read endpoint, and selects single DNS view/zone choices automatically. |
| `ib config edit [PROFILE]` | Edit an existing profile; leaving the password blank keeps the current encrypted password. |
| `ib config list` | List configured profiles and their default/read endpoint context. |
| `ib config use PROFILE` | Set the default profile. |
| `ib config delete PROFILE` | Delete a non-default profile and clear its local cache rows. |
| `ib config completion [bash\|zsh\|fish]` | Generate dynamic shell completion. |
| `ib config cache status` | Show local SQLite cache entries. |
| `ib config cache clear` | Clear local SQLite cache entries. |

### DNS

| Command | Description |
| --- | --- |
| `ib dns` | Show DNS help and the current profile/view/zone context. |
| `ib dns list [ZONE]` | List records in the current or provided zone. Add `-r` to include child zones. |
| `ib dns search KEYWORD` | Search records by name, value, or comment. Use `--global` for all searchable zones or `-r` for child zones under the current/root zone. |
| `ib dns create NAME TYPE VALUE` | Create a DNS record, for example `ib dns create app host 192.0.2.10 -c "Application host"`. |
| `ib dns edit NAME [TYPE] [VALUE]` | Edit an existing DNS record. |
| `ib dns delete NAME [ZONE]` | Delete a DNS record; prompts for confirmation unless `-y` is used. |
| `ib dns view list` | List DNS views. |
| `ib dns view use VIEW` | Set the active DNS view for the current shell session. |
| `ib dns zone create ZONE` | Create an authoritative DNS zone. |
| `ib dns zone list [SEARCH]` | List authoritative DNS zones. |
| `ib dns zone info ZONE` | Show DNS zone details. |
| `ib dns zone delete ZONE` | Delete an authoritative DNS zone. |
| `ib dns zone use ZONE` | Set the active DNS zone for the current shell session. |

Common examples:

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

Use `-o json` or `-o csv` for machine-readable output.

During `ib config new` and `ib config edit`, Step 05 discovers Grid Master Candidates and saves `read_server` only when a candidate successfully answers a read-only WAPI GET probe. GET requests then use that GCM; create, update, and delete requests always use the primary server.

## Cache

Zone and record caches are stored in `~/.ib/cache.sqlite3`.

Record cache freshness uses `cached_at + cache_ttl`. Expired records inside `records_cache_swr_ttl` are returned immediately while a single background refresh process revalidates the zone serial and refreshes `/allrecords` when needed.

Successful DNS record create, edit, and delete operations clear the affected zone's record cache and start a background refresh. DNS zone create/delete also refreshes the zone-list cache in the background; deleted zones have their record cache removed.

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

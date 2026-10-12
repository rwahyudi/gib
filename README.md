<p align="center">
  <a href="https://github.com/rwahyudi/gib/releases/latest"><img src="https://img.shields.io/github/v/release/rwahyudi/gib?color=0ea5e9" alt="Latest release"></a>
  <img src="https://img.shields.io/badge/go-1.26.9-00ADD8" alt="Go 1.26.9">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-22c55e" alt="MIT license"></a>
</p>

`gib` installs the `ib` command: a lightweight, operator-focused CLI for Infoblox DNS and IPAM work from the shell.

It is built for daily record and network tasks: compact tables for humans, plain JSON/CSV for automation, safe read/write routing, dynamic completion, and cache-backed searches that stay responsive on large zones.

<p align="center">
  <img src="./docs/assets/go-record1.gif" width="100%" alt="Animated preview of ib DNS record output">
</p>

## What `ib` helps you do

- Manage local and Linux-global Infoblox profiles with encrypted credentials.
- Work in the right DNS context from saved defaults, shell-session view/zone state, environment variables, or one-command `--view` and `--zone` overrides.
- List, search, create, edit, and delete DNS records with type filters, exclusions, sorting, selected columns, duplicate selection, and confirmation.
- Read IPAM network views, IPv4 networks and containers, addresses, VLAN assignments, and next available IPs.
- Manage DHCP fixed-address reservations with explicit IPv4 addresses or next-available IP allocation from an IPAM network.
- Route read-only GET requests to a validated Grid Master Candidate with separate TLS trust while keeping POST, PUT, DELETE, and next-IP function calls on the primary Grid Master.
- Use Badger-backed zone, record, IPAM, VLAN, and DHCP reservation caches with bounded workers and stale-while-revalidate refreshes.
- Generate dynamic shell completion for profiles, views, zones, records, networks, record types, flags, columns, and output formats.
- Emit optional JSON Lines audit events for successful DNS/config writes and failed DNS record WAPI mutations while keeping read workflows quiet.

## Install

For Fedora or EPEL, Copr is the shortest path when you want a distro-built package. The Copr package is named `gib` and installs the command as `/usr/bin/ib`; the GitHub release RPM and DEB install it as `/usr/local/bin/ib`.

```bash
sudo dnf install dnf-plugins-core
sudo dnf copr enable rwahyudi/gib
sudo dnf install gib
```

GitHub release assets use stable filenames, so `/releases/latest/download/...` always points at the newest published version. Linux release assets are built with `CGO_ENABLED=0`, so they do not require a specific glibc version.

Linux tarball:

```bash
curl -fL https://github.com/rwahyudi/gib/releases/latest/download/ib_linux_amd64.tar.gz | tar -xz ib
sudo install -m 0755 ib /usr/local/bin/ib
ib -v
ib --help
```

RPM or DEB package:

```bash
curl -fLO https://github.com/rwahyudi/gib/releases/latest/download/ib_linux_amd64.rpm
sudo dnf install ./ib_linux_amd64.rpm

curl -fLO https://github.com/rwahyudi/gib/releases/latest/download/ib_linux_amd64.deb
sudo apt install ./ib_linux_amd64.deb
```

GitHub release RPM and DEB packages install `ib` to `/usr/local/bin/ib`, Bash completion to `/etc/bash_completion.d/ib`, and the manual page to `/usr/share/man/man1/ib.1` (`man ib`). Copr RPMs use `/usr/bin/ib`. For the tarball, install completion manually:

```bash
sudo mkdir -p /etc/bash_completion.d
ib config completion bash | sudo tee /etc/bash_completion.d/ib >/dev/null
```

Open a new shell after installing completion.

macOS (Homebrew):

```bash
brew install --cask rwahyudi/tap/ib
ib -v
ib --help
```

Tagged releases update the `rwahyudi/tap` cask. To upgrade an existing
installation, run `brew update` followed by `brew upgrade --cask rwahyudi/tap/ib`.

Windows ZIP:

```powershell
New-Item -ItemType Directory -Force "$HOME\bin" | Out-Null
$archive = "$env:TEMP\ib_windows_amd64.zip"
Invoke-WebRequest "https://github.com/rwahyudi/gib/releases/latest/download/ib_windows_amd64.zip" -OutFile $archive
Expand-Archive $archive "$HOME\bin\ib-latest" -Force
Copy-Item "$HOME\bin\ib-latest\ib.exe" "$HOME\bin\ib.exe" -Force
$binPath = Join-Path $HOME "bin"
$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if (($userPath -split ';') -notcontains $binPath) {
    [Environment]::SetEnvironmentVariable("Path", (($userPath, $binPath | Where-Object { $_ }) -join ';'), "User")
}
if (($env:Path -split ';') -notcontains $binPath) {
    $env:Path = (($env:Path, $binPath | Where-Object { $_ }) -join ';')
}
```

Open a new PowerShell window so the user `PATH` change is loaded, then run `ib config completion windows`. For source builds, see [Build From Source](docs/build-from-source.md). For publishing, see [Release Process](docs/release-process.md) and [Release Notes](docs/release-notes.md).

## First run

Create a profile, choose the active DNS context, then run the first read:

```bash
ib config new --default
ib dns view use "DNS Zone View"
ib dns zone use example.com
ib dns list
```

Profiles store the primary server, primary TLS trust or pinned certificate fingerprint, auto-detected WAPI version, optional validated GCM read endpoint with independent TLS settings, credentials, DNS view, default zone, and audit logging settings. Passwords are encrypted at rest. Unix builds use a key file; native Windows builds use user-scope DPAPI for new writes and can still read existing `enc:v1` key-file profiles.

Local profiles live under `~/.ib/`. On Linux, `sudo ib config new --global-config [PROFILE]` creates a shared profile under `/etc/ib/`; normal commands merge `/etc/ib/config` with `~/.ib/config` so user-local metadata can select a global profile without copying secrets.

Do not commit `~/.ib/config`, `~/.ib/key`, `/etc/ib/config`, `/etc/ib/key`, audit logs, or cache data.

## Daily workflows

| Workflow | Start with | Notes |
| --- | --- | --- |
| Configure access | `ib config new --default` | Validates server reachability, primary/read-endpoint TLS trust, credentials, WAPI version, DNS defaults, and optional audit logging. |
| List records | `ib dns list` | Uses the current DNS view/zone unless `--view` or `--zone` is supplied. |
| Search records | `ib dns search app` | Searches record name, value, and comment fields case-insensitively by default. Multiple positional terms must all match; repeated `--any` or `--or` terms require at least one match. Use `--case-sensitive` to preserve case. Add `--global` for all searchable zones or `-r` for child zones under the current/root zone. |
| Create records | `ib dns create host app 192.0.2.10 -c "Application host"` | Type-first syntax keeps A, AAAA, CNAME, host, MX, NS, PTR, SRV, and TXT workflows consistent. |
| Edit or delete records | `ib dns edit host app 192.0.2.20` | Deletes match forward DNS names case-insensitively and prompts for confirmation unless `-y` is used. |
| Delete a zone | `ib dns zone delete example.com` | Requires an interactive confirmation; use `-y` only for deliberate automation. |
| Read IPAM | `ib net list prod --network-view default` | Lists or searches IPv4 networks and containers, including assigned VLAN fields when WAPI supports them. |
| Find addresses | `ib net next-ip 192.0.2.0/24 -n 3` | Resolves networks and containers, then asks the primary server for current next-IP results. |
| Reserve DHCP addresses | `ib dhcp reservation create 00:11:22:33:44:55 --next-ip 192.0.2.0/24` | Creates a fixed-address reservation; use `IP MAC` when the address is already known. MACs accept colon, hyphen, Cisco-period, or 12-hex continuous formats. |
| Inspect VLANs | `ib vlan list --network-view default` | Derives VLAN rows from IPAM network/container metadata; stock NIOS has no VLAN CRUD WAPI. |

Common examples:

```bash
ib dns view list
ib dns zone list
ib dns list --sort name --columns name,value,ttl
ib dns search web prd
ib dns search web --or tst --or dev
ib dns search web prd --any tst --or dev
ib dns search "web app" prd
ib dns search Web PRD --case-sensitive
ib dns search app --global --sort zone --columns zone,name,value -o csv

Search terms are matched against each record's name, value, and comment fields. Terms can match different fields on the same record; for example, `web` may match the name while `prd` matches the comment. Quote a term containing spaces.

ib dns create ptr 192.0.2.10 app.example.com
ib dns delete a app
ib net address 192.0.2.10 --network-view default
ib dhcp reservation list --network-view default
ib dhcp lease list --state ACTIVE
ib dhcp lease show 192.0.2.25 --network-view default
ib dhcp reservation show 192.0.2.25 --network-view default
ib dhcp reservation create 192.0.2.25 00:11:22:33:44:55 --network-view default --name printer -c "Lab printer"
ib dhcp reservation create 00:11:22:33:44:66 --next-ip 192.0.2.0/24 --network-view default
ib dhcp reservation edit 192.0.2.25 --name printer-new
ib dhcp reservation delete 192.0.2.25 -y
ib vlan show 123
```

### DHCP reservations

Reservations use the Infoblox fixedaddress object. Reads use GET routing, while create, edit, delete, and next-IP allocation use the primary server.

```
ib dhcp reservation list [SEARCH] [--network-view VIEW] [--name NAME] [--comment COMMENT] [--refresh]
ib dhcp reservation show IP [--network-view VIEW]
ib dhcp reservation create IP MAC [--network-view VIEW] [-n|--name NAME] [--comment COMMENT]
ib dhcp reservation create MAC --next-ip NETWORK [--network-view VIEW] [-n|--name NAME] [--comment COMMENT]
ib dhcp reservation edit IP [--mac MAC] [--network-view VIEW] [--name NAME] [--comment COMMENT]
ib dhcp reservation delete IP [--network-view VIEW] [-y|--yes]
```

create requires either positional IP MAC or positional MAC with --next-ip NETWORK (an IPv4 CIDR). MACs are accepted in colon, hyphen, Cisco-period, and continuous 12-hex formats and are normalized to lowercase colon form. For next-IP creates, a minimal POST response is checked with a primary GET when a reference is available; if the allocated address still cannot be confirmed, ib reports partial success and says not to retry. On edit, --network-view selects the source reservation and cannot move it to another view; edit requires at least one other field. Delete prompts for confirmation unless --yes is supplied.

### DHCP leases

Leases are read live from the WAPI `lease` object and are never cached. List supports `SEARCH`, `--network-view`, `--network`, `--mac`, `--hostname`, `--state`, `-C|--columns`, and `-s|--sort`; omitted network views use `default`.

```
ib dhcp lease list [SEARCH] [--network-view VIEW] [--network NETWORK] [--mac MAC] [--hostname HOSTNAME] [--state STATE]
ib dhcp lease show IP [--network-view VIEW]
```

## Output controls

`-o, --output table|json|csv` is available from the root command and applies to every command. Table output is styled for operators; JSON and CSV remain plain for `jq`, spreadsheets, and scripts.

List-style commands can sort rows and select columns:

```bash
ib dns list --sort=-name --columns zone,name,value
ib dns zone list --sort zone --columns zone,format,comment -o json
ib net list --sort network --columns network,type,extattrs -o json
```

DNS record fields include `type`, `name`, `value`, `zone`, `ttl`, and `comment`. Zone fields include `zone`, `view`, `format`, `ns_group`, and `comment`. Network default output includes `network`, `type`, `assigned_vlan`, `assigned_vlan_name`, `comment`, and `extattrs`; `network_view` is available through `--columns`.

## How it stays safe and fast

<p align="center">
  <img src="./docs/assets/readme/workflow.svg" width="100%" alt="ib operational model for shell context, read/write routing, cache, and output">
</p>

<p align="center">
  <img src="./docs/assets/audit-write-flow.svg" width="100%" alt="ib audit flow for successful and failed DNS writes with redacted JSON Lines events">
</p>

`cmd/ib/main.go` starts the Cobra CLI and hands behavior to `internal/ibcli`. Profile loading decrypts the stored password, resolves the current DNS view/zone, and builds the WAPI client.

When a profile has a validated `read_server`, read-only GET requests can use that endpoint with its own TLS setting or certificate pin. Create, update, delete, and next-IP function calls always use the primary Grid Master. WAPI responses are size-limited and cancel when the command receives Ctrl-C.

Zone, record, IPAM, VLAN, and DHCP reservation rows are cached in `~/.ib/cache.badger/` for local profiles or `/etc/ib/cache.badger/` for Linux global profiles. Cache namespaces include the profile endpoint, so repointing a profile cannot reuse another Grid's data. Record, IPAM, and DHCP reservation freshness is calculated from `cached_at + cache_ttl`; stale rows inside `records_cache_swr_ttl` can be returned immediately while one lease-protected refresh runs in the background. Use `ib dhcp reservation list --refresh` when the list must wait for fresh WAPI data. Large DNS searches use bounded workers, reuse cache rows, and batch stale multi-zone record revalidation.

For the deeper cache and worker model, see [Performance & Caching](docs/performance-caching.md).

## Debugging and completion

Generate dynamic Bash completion:

```bash
ib config completion bash > ~/.ib-complete.bash
. ~/.ib-complete.bash
```

On Windows, install native PowerShell completion for the current user:

```powershell
ib config completion windows
```

The generated completion calls the live `ib` binary, so profiles, DNS views, zones, records, IPAM network views/networks, flags, and output formats are resolved dynamically.

To see what a command is doing and how long each step takes, add `--debug`. Debug output is written to stderr, so JSON and CSV stdout stay script-friendly:

```bash
ib dns list --debug -o csv > records.csv
ib dns search app --global --debug
```

If a DNS write reports a non-JSON WAPI response, `ib` prints the WAPI object, HTTP status, content type, and a short response snippet. An HTML snippet usually means the configured server, WAPI version, credentials, or a proxy/login page is answering the WAPI request instead of Infoblox JSON.

## Command map

| Module | Purpose | Start here |
| --- | --- | --- |
| `config` | Manage profiles, encrypted credentials, completion, and cache. | `ib config new --default` |
| `dns` | Manage Infoblox DNS views, zones, records, searches, next-IP lookups, and context overrides. | `ib dns list` |
| `net` | Manage IPAM network views, IPv4 networks and containers, addresses, and next-IP lookups. | `ib net list` |
| `dhcp` | Manage DHCP fixed-address reservations with list, show, create, edit, and delete workflows. | `ib dhcp reservation list` |
| `vlan` | List, search, inspect, and select VLANs derived from IPAM VLAN metadata. | `ib vlan list` |

Use `ib <module> --help` or `ib <module> <command> --help` for the full command-specific reference generated by the current binary.

## License

`gib` is licensed under the MIT License. See [LICENSE](LICENSE). Binary release archives also include [THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md) for bundled Go dependency notices. The dependency policy is documented in [docs/licensing.md](docs/licensing.md).

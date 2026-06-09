# Build From Source

Use these instructions when a packaged release is not available for your
platform, or when validating source changes before a release.

## Requirements

- Go 1.24 or newer.
- Git.

Use `/tmp` for Go caches in restricted or sandboxed checkouts:

```bash
export GOCACHE=/tmp/go-build
export GOMODCACHE=/tmp/go-mod
```

## Linux

Install Go with your system package manager, then build:

```bash
git clone https://github.com/rwahyudi/gib.git
cd gib

env GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go test ./...
env GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go build -buildvcs=false -o ib ./cmd/ib

sudo install -m 0755 ib /usr/local/bin/ib
ib --help
```

This local build uses a cgo-free SQLite driver, so it does not require a C
compiler or link against the build host's glibc.

Optional Bash completion:

```bash
sudo mkdir -p /etc/bash_completion.d
ib config completion bash | sudo tee /etc/bash_completion.d/ib >/dev/null
```

Open a new shell after installing completion.

## Windows

Native Windows builds do not need a C compiler because SQLite support is
cgo-free. MSYS2 UCRT64 is still a convenient shell for Go builds.

Install MSYS2:

```powershell
winget install MSYS2.MSYS2
```

Open the **MSYS2 UCRT64** shell and run:

```bash
pacman -S --needed git mingw-w64-ucrt-x86_64-go

git clone https://github.com/rwahyudi/gib.git
cd gib

export CGO_ENABLED=0
export GOCACHE=/tmp/go-build
export GOMODCACHE=/tmp/go-mod

go test ./...
go build -buildvcs=false -o ib.exe ./cmd/ib
```

Copy the binary into a user-local bin directory:

```bash
mkdir -p "$USERPROFILE/bin"
cp ib.exe "$USERPROFILE/bin/ib.exe"
```

Then add that directory to the user `PATH` from PowerShell:

```powershell
$userBin = Join-Path $HOME "bin"
$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if (($userPath -split ";") -notcontains $userBin) {
  $newPath = ($userPath.TrimEnd(";") + ";$userBin").TrimStart(";")
  [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
}
```

Open a new PowerShell window, then verify:

```powershell
ib --help
ib config completion windows
```

Native Windows profile passwords are encrypted with user-scope Windows DPAPI.

## Development Checks

The GitHub workflow runs tests plus vulnerability, security, and license scans.
Run the same checks locally when the tools are installed:

```bash
env GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go test ./...
scripts/check-licenses.sh
env GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod govulncheck ./...
gosec ./...
trivy fs --scanners vuln,secret,license .
```

For RPM/Copr packaging, see [RPM and Copr Packaging](../packaging/rpm/README.md).

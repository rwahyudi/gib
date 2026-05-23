# Release Process

Use this runbook to publish a new `gib` release and the `ib` binaries. The
GitHub release is driven by a pushed `v*` tag, and GoReleaser owns the final
GitHub release assets.

## Release Outputs

The release pipeline publishes stable asset names so install commands can point
at `/releases/latest/download/...` without embedding the version:

- `ib_linux_amd64.tar.gz`
- `ib_linux_amd64.rpm`
- `ib_linux_amd64.deb`
- `ib_windows_amd64.zip`
- `checksums.txt`

GoReleaser builds Linux and Windows amd64 binaries from `./cmd/ib`. Release
builds inject the version and build date into `internal/ibcli` with ldflags, so
`ib -v` should show the tag version and an AEST build time.

## Step 1: Choose and Prepare the Version

1. Choose the next semantic version and tag, for example `0.3.5` and `v0.3.5`.
2. Update versioned source files before tagging:

```bash
rg -n '0\.3\.|gib [0-9]+\.[0-9]+\.[0-9]+|Version:' internal packaging gib.spec README.md docs
```

Common files to check are:

- `gib.spec`: `Version:` and `%changelog`
- `packaging/man/ib.1`: manual page version string
- `internal/ibcli/app.go`: source-build fallback `Version`

3. Keep README install commands on stable latest-release URLs unless a release
   intentionally pins a version.
4. Confirm the README asset names match `.goreleaser.yaml`:

```bash
rg 'releases/latest/download|ib_linux_amd64|ib_windows_amd64|ib_[0-9]+\.[0-9]+\.[0-9]+|releases/download/v[0-9]' README.md
rg 'name_template|file_name_template|formats|package_name|bindir' .goreleaser.yaml
```

## Step 2: Validate Locally

Run the normal local checks from a clean worktree:

```bash
env GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go test ./...
env GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go vet ./...
scripts/check-licenses.sh
env GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go build -buildvcs=false -o /tmp/ib ./cmd/ib
/tmp/ib -v
/tmp/ib --help
```

When release packaging changed, also run a GoReleaser snapshot build before
tagging:

```bash
goreleaser release --snapshot --clean --skip=publish
ls -lh dist/
```

The snapshot should produce the same kinds of archives and packages as the real
release, but with snapshot metadata and no GitHub upload.

## Step 3: Commit, Tag, and Push

Commit all version, docs, and packaging changes first:

```bash
git status --short
git add <changed-files>
git commit -m "Prepare release v0.3.5"
```

Push `main`, then push the tag:

```bash
git push origin main
git tag -a v0.3.5 -m "Release v0.3.5"
git push origin v0.3.5
```

The tag push is what starts the GitHub release workflow. Do not create the
GitHub release manually first; let GoReleaser create or update it from the tag.

## Step 4: What GitHub Actions Does

The release workflow is `.github/workflows/release.yml`.

Trigger:

- Runs only when a tag matching `v*` is pushed.
- Uses `contents: write` so GoReleaser can create the GitHub release and upload
  assets.

Job flow:

1. Check out the repository with `fetch-depth: 0` so GoReleaser can inspect tags
   and changelog data.
2. Set up Go from `go.mod` with the GitHub Actions Go cache.
3. Install Linux packaging and Windows cross-build dependencies:
   `build-essential`, `rpm`, `gcc-mingw-w64-x86-64`, and
   `g++-mingw-w64-x86-64`.
4. Run `go mod download`.
5. Run `go test ./...`.
6. Run `scripts/check-licenses.sh`, which regenerates third-party notices in
   check mode and rejects blocked or review-required license text.
7. Smoke-test the Windows build with `GOOS=windows`, `GOARCH=amd64`,
   `CGO_ENABLED=1`, and the MinGW compiler variables.
8. Run `goreleaser/goreleaser-action@v7` with `goreleaser release --clean`.

GoReleaser then:

1. Builds `ib` for Linux amd64 with CGO enabled.
2. Builds `ib.exe` for Windows amd64 with MinGW-w64 and CGO enabled.
3. Injects `.Version` and `.Date` into `internal/ibcli.Version` and
   `internal/ibcli.BuildDate`.
4. Creates the Linux tarball and Windows ZIP with README and license files.
5. Creates Linux RPM and DEB packages with nFPM. These install `ib` to
   `/usr/local/bin/ib` and package Bash completion under
   `/etc/bash_completion.d/ib`.
6. Writes `checksums.txt`.
7. Publishes the GitHub release under `rwahyudi/gib`.

Other workflows are separate gates:

- `.github/workflows/security.yml` runs on `main`, pull requests, a weekly
  schedule, and manual dispatch. It runs tests, `govulncheck`, `gosec`, and
  Trivy filesystem scans.
- `.github/workflows/license.yml` runs on `main`, pull requests, and manual
  dispatch. It runs the same license policy check used by the release workflow.
- Dependabot updates Go modules and GitHub Actions separately; it does not
  publish releases.

## Step 5: Monitor the Release Workflow

After pushing the tag:

```bash
gh run list --workflow Release --limit 5
gh run watch
```

If the workflow fails before publishing assets, fix the issue on `main`, delete
the failed local and remote tag, recreate it on the fixed commit, and push it
again:

```bash
git tag -d v0.3.5
git push origin :refs/tags/v0.3.5
git tag -a v0.3.5 -m "Release v0.3.5"
git push origin v0.3.5
```

If assets were already published, inspect the release before deleting anything.
Avoid leaving a partial release marked as latest.

## Step 6: Verify the Published Release

Check the live release metadata and asset names:

```bash
gh release view v0.3.5 --json tagName,isLatest,assets
gh release view --json tagName,isLatest,assets
```

Confirm the latest stable URLs resolve:

```bash
curl -fI https://github.com/rwahyudi/gib/releases/latest/download/ib_linux_amd64.tar.gz
curl -fI https://github.com/rwahyudi/gib/releases/latest/download/ib_linux_amd64.rpm
curl -fI https://github.com/rwahyudi/gib/releases/latest/download/ib_linux_amd64.deb
curl -fI https://github.com/rwahyudi/gib/releases/latest/download/ib_windows_amd64.zip
```

Smoke-test the tarball without installing it globally:

```bash
tmp=$(mktemp -d)
curl -fL https://github.com/rwahyudi/gib/releases/latest/download/ib_linux_amd64.tar.gz | tar -xz -C "$tmp" ib
"$tmp/ib" -v
"$tmp/ib" --help
```

Inspect package metadata when package tools are available:

```bash
curl -fLO https://github.com/rwahyudi/gib/releases/latest/download/ib_linux_amd64.rpm
rpm -qpi ib_linux_amd64.rpm
rpm -qlp ib_linux_amd64.rpm
```

For Windows ZIP verification from Linux:

```bash
tmp=$(mktemp -d)
curl -fL -o "$tmp/ib_windows_amd64.zip" https://github.com/rwahyudi/gib/releases/latest/download/ib_windows_amd64.zip
unzip -l "$tmp/ib_windows_amd64.zip"
```

## Step 7: Publish Copr RPM Follow-up

The GitHub RPM is built by GoReleaser. Copr uses `gib.spec` and a vendored Go
source archive instead.

From a Fedora packaging workstation:

```bash
spectool -g -R gib.spec
go_vendor_archive create -c go-vendor-tools.toml gib.spec
go_vendor_license -c go-vendor-tools.toml -C gib.spec report --update-spec --prompt --autofill=auto
go_vendor_license -c go-vendor-tools.toml -C gib.spec report --verify-spec
rpmbuild -bs gib.spec
rpmlint gib.spec ~/rpmbuild/SRPMS/gib-*.src.rpm
mock -r epel-10-x86_64 --rebuild ~/rpmbuild/SRPMS/gib-*.src.rpm
copr-cli build gib ~/rpmbuild/SRPMS/gib-*.src.rpm
```

Do not commit generated SRPM, RPM, or vendor archive artifacts unless the
packaging workflow explicitly changes. Commit only intentional updates to
`gib.spec`, `go-vendor-tools.toml`, docs, or source files.

## Final Checklist

- Versioned source files and changelog are updated.
- README install commands use stable latest-release URLs and asset names match
  `.goreleaser.yaml`.
- Local tests, vet, license check, and optional snapshot release pass.
- `main` is pushed before the `v*` tag.
- Release workflow passes.
- GitHub release is latest and contains all expected assets.
- `ib -v` reports the release version and AEST build time.
- Linux tarball, RPM, DEB, and Windows ZIP links resolve.
- Copr package is rebuilt after the GitHub release.

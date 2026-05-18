# Licensing

`gib` source code is licensed under the MIT License. The canonical project
license text is `LICENSE`.

## Distributed Artifacts

Release archives must include:

- `LICENSE`
- `THIRD_PARTY_LICENSES.md`
- `README.md`

GoReleaser RPM and DEB packages must set package metadata to `MIT` for the
project license and install `LICENSE` plus `THIRD_PARTY_LICENSES.md` under
`/usr/share/doc/ib/`.

The Fedora/Copr source RPM is different because it includes the vendored Go
source archive. Its spec license expression must reflect bundled dependency
licenses. For the current dependency set, `gib.spec` uses:

```text
Apache-2.0 AND BSD-3-Clause AND MIT
```

Regenerate and verify that expression whenever dependencies change:

```bash
spectool -g -R gib.spec
go_vendor_archive create -c go-vendor-tools.toml gib.spec
go_vendor_license -c go-vendor-tools.toml -C gib.spec report --update-spec --prompt --autofill=auto
go_vendor_license -c go-vendor-tools.toml -C gib.spec report --verify-spec
```

## Dependency Policy

Allowed by default:

- MIT
- BSD-2-Clause and BSD-3-Clause
- Apache-2.0
- ISC
- 0BSD

Requires explicit review before merging:

- MPL
- EPL
- LGPL
- Any custom or unclear license
- Any dependency without a standalone license file

Blocked by default:

- GPL
- AGPL
- Any dependency that requires the whole CLI or release archive to be
  distributed under reciprocal source-sharing terms

`github.com/mattn/go-localereader` is an approved exception: upstream declares
MIT in `README.md` but does not ship a standalone license file. The release
notice generator uses the MIT text from `github.com/mattn/go-isatty`, matching
the existing `go-vendor-tools.toml` RPM packaging override.

## Maintenance

After changing `go.mod`, regenerate notices and run the license check:

```bash
scripts/generate-third-party-licenses.sh
scripts/check-licenses.sh
```

CI runs the same check and fails when `THIRD_PARTY_LICENSES.md` is stale or when
blocked license text appears in generated notices.

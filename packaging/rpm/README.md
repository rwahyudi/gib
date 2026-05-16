# RPM and Copr Packaging

This directory documents how to publish `gib` as an RPM package. The RPM
package name is `gib`; the installed command is `/usr/bin/ib`.

## Prerequisites

On a Fedora packaging workstation:

```bash
sudo dnf install copr-cli fedpkg go2rpm+vendor mock rpm-build rpmdevtools rpmlint spectool
```

The spec requires a Go toolchain new enough for the module's `go 1.24`
directive. Start with EPEL 10. Add EPEL 9 only after confirming its buildroot
has an acceptable Go toolchain.

## Build the SRPM

From the repository root:

```bash
spectool -g -R gib.spec
go_vendor_archive create -c go-vendor-tools.toml gib.spec
go_vendor_license -c go-vendor-tools.toml -C gib.spec report --update-spec --prompt --autofill=auto
rpmbuild -bs gib.spec
```

`go_vendor_archive` creates the `Source1` vendor archive referenced by
`gib.spec`. Recreate it for every upstream release. Commit any intentional
changes to `gib.spec` and `go-vendor-tools.toml`, but do not commit generated
SRPM/RPM artifacts.

## Validate Locally

```bash
rpmlint gib.spec ~/rpmbuild/SRPMS/gib-*.src.rpm
mock -r epel-10-x86_64 --rebuild ~/rpmbuild/SRPMS/gib-*.src.rpm
```

After installing the built RPM in a clean test environment:

```bash
rpm -ql gib
rpm -q --requires gib
ib --help
ib config completion bash
```

Optional output-control smoke checks after configuring a test profile:

```bash
ib dns list --sort name --columns name,value,ttl -o csv
ib dns list -o json | jq '.[0]'
```

## Publish to Copr

Create the project once:

```bash
copr-cli create gib --chroot epel-10-x86_64
```

Submit each new SRPM:

```bash
copr-cli build gib ~/rpmbuild/SRPMS/gib-*.src.rpm
```

User installation command:

```bash
sudo dnf install dnf-plugins-core
sudo dnf copr enable rwahyudi/gib
sudo dnf install gib
ib --help
```

## Official EPEL Follow-up

After Copr builds are clean, use this same spec as the Fedora package review
starting point. Submit the review, import the approved package into dist-git,
then request EPEL branches and build them with `fedpkg`.

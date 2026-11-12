#!/usr/bin/env bash
# Block releases on reachable Go vulnerabilities in imported modules and the
# standard library. The release workflow runs this before GoReleaser publishes
# assets, so a finding stops the release instead of shipping it.
set -euo pipefail

# Pin the scanner so releases are reproducible and a new scanner release cannot
# silently change the gate. Bump this deliberately.
govulncheck_version="${GOVULNCHECK_VERSION:-v1.9.0}"

# go.mod pins the release toolchain; download it when the host Go is older.
export GOTOOLCHAIN="${GOTOOLCHAIN:-auto}"

workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"' EXIT

if [[ -n "${VULNCHECK_BIN:-}" ]]; then
	govulncheck="$VULNCHECK_BIN"
else
	mkdir -p "$workdir/bin"
	if ! GOBIN="$workdir/bin" go install "golang.org/x/vuln/cmd/govulncheck@${govulncheck_version}"; then
		echo "ERROR: cannot install govulncheck ${govulncheck_version}; the vulnerability gate cannot run." >&2
		exit 1
	fi
	govulncheck="$workdir/bin/govulncheck"
fi

if ! "$govulncheck" ./...; then
	cat >&2 <<'EOF'

ERROR: reachable Go vulnerabilities found, or the scan could not complete.
Releases are blocked until this check passes.

Upgrade the affected module or the Go toolchain pinned in go.mod, then rerun:
  scripts/check-vulnerabilities.sh
EOF
	exit 1
fi

#!/usr/bin/env bash
set -euo pipefail

scripts/generate-third-party-licenses.sh --check

blocked_pattern='AGPL|Affero|GNU GENERAL PUBLIC LICENSE|GNU LESSER GENERAL PUBLIC LICENSE|Mozilla Public License|Eclipse Public License|Common Development and Distribution License|CDDL'
if grep -E -i -n "$blocked_pattern" THIRD_PARTY_LICENSES.md; then
	echo "Blocked or review-required license text found. Update docs/licensing.md only after explicit approval." >&2
	exit 1
fi

#!/usr/bin/env bash
set -euo pipefail

version="${1:-}"
semver_number='(0|[1-9][0-9]*)'
semver_identifier='(0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)'
semver_build_identifier='[0-9A-Za-z-]+'
semver_re="^${semver_number}\\.${semver_number}\\.${semver_number}(-${semver_identifier}(\\.${semver_identifier})*)?(\\+${semver_build_identifier}(\\.${semver_build_identifier})*)?$"
if [[ ! "$version" =~ $semver_re ]]; then
  echo "invalid semantic version for Debian packaging: $version" >&2
  exit 2
fi

# Debian sorts a hyphen as the package-revision separator, which would make
# 1.2.3-rc.1 newer than the final 1.2.3. A tilde sorts before the empty string,
# preserving SemVer prerelease-to-stable upgrade ordering.
if [[ "$version" =~ ^([0-9]+\.[0-9]+\.[0-9]+)-(.+)$ ]]; then
  printf '%s~%s\n' "${BASH_REMATCH[1]}" "${BASH_REMATCH[2]}"
else
  printf '%s\n' "$version"
fi

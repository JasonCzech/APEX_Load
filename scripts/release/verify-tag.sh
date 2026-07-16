#!/usr/bin/env bash
set -euo pipefail

die() {
  printf 'release tag verification: %s\n' "$*" >&2
  exit 1
}

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "$root_dir/scripts/release/semver.sh"
tag="${1:-}"
expected_commit="${2:-}"

is_apex_release_tag "$tag" \
  || die "tag must be a canonical semantic release tag (vMAJOR.MINOR.PATCH[-prerelease])"
[[ "$expected_commit" =~ ^[0-9a-f]{40}$ ]] || die "expected commit must be a full lowercase SHA"

[[ "$(git cat-file -t "refs/tags/${tag}" 2>/dev/null || true)" == tag ]] \
  || die "${tag} must be an annotated tag"
actual_commit="$(git rev-parse "refs/tags/${tag}^{commit}")"
[[ "$actual_commit" == "$expected_commit" ]] \
  || die "tag resolves to ${actual_commit}, not workflow commit ${expected_commit}"

key_file="${APEXLOAD_RELEASE_TAG_PUBLIC_KEYS_FILE:-}"
key_material="${APEXLOAD_RELEASE_TAG_PUBLIC_KEYS:-}"
[[ -n "$key_file" || -n "$key_material" ]] \
  || die "trusted release-tag public keys are not configured"

gnupg_home="$(mktemp -d)"
trap 'rm -rf "$gnupg_home"' EXIT
chmod 0700 "$gnupg_home"
export GNUPGHOME="$gnupg_home"
if [[ -n "$key_file" ]]; then
  [[ -f "$key_file" ]] || die "release-tag public-key file does not exist"
  gpg --batch --import "$key_file" >/dev/null 2>&1
else
  printf '%s' "$key_material" | gpg --batch --import >/dev/null 2>&1
fi
git verify-tag --raw "$tag" >/dev/null 2>&1 \
  || die "annotated tag signature is not valid under the trusted keyring"

git show-ref --verify --quiet refs/remotes/origin/main \
  || die "origin/main is missing; checkout the full reviewed repository history before verification"
git merge-base --is-ancestor "$expected_commit" refs/remotes/origin/main \
  || die "tagged commit is not reachable from origin/main"

printf 'verified signed release tag %s at %s on origin/main\n' "$tag" "$expected_commit"

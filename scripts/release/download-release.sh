#!/usr/bin/env bash
set -euo pipefail

die() {
  printf 'release download: %s\n' "$*" >&2
  exit 1
}

tag="${1:-}"
output_dir="${2:-}"
repository="${3:-${GITHUB_REPOSITORY:-}}"
[[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z][0-9A-Za-z.-]*)?$ ]] \
  || die "release must be an exact semantic tag"
[[ -n "$output_dir" && -n "$repository" ]] || die "output directory and repository are required"
[[ -n "${GH_TOKEN:-${GH_ENTERPRISE_TOKEN:-}}" ]] || die "a GitHub CLI token is required"

server_url="${GITHUB_SERVER_URL:-https://github.com}"
[[ "$server_url" =~ ^https://[^/]+$ ]] || die "GITHUB_SERVER_URL must be an HTTPS origin"
export GH_HOST="${server_url#https://}"
if [[ "$GH_HOST" != github.com && -z "${GH_ENTERPRISE_TOKEN:-}" ]]; then
  export GH_ENTERPRISE_TOKEN="${GH_TOKEN:-}"
fi

mkdir -p "$output_dir"
[[ -z "$(find "$output_dir" -mindepth 1 -maxdepth 1 -print -quit)" ]] \
  || die "output directory must be empty"
gh release view "$tag" --repo "$repository" --json isDraft,isPrerelease,tagName \
  --jq 'select(.isDraft == false and .tagName != "") | .tagName' | grep -qx "$tag" \
  || die "release is missing, draft, or has the wrong tag"
gh release download "$tag" --repo "$repository" --dir "$output_dir"

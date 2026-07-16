#!/usr/bin/env bash
set -euo pipefail

die() {
  printf 'release download: %s\n' "$*" >&2
  exit 1
}

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "$root_dir/scripts/release/semver.sh"
tag="${1:-}"
output_dir="${2:-}"
repository="${3:-${GITHUB_REPOSITORY:-}}"
expected_tag_object_sha="${4:-}"
is_apex_release_tag "$tag" || die "release must be an exact canonical semantic tag"
[[ -n "$output_dir" && -n "$repository" ]] || die "output directory and repository are required"
if [[ -n "$expected_tag_object_sha" ]]; then
  [[ "$expected_tag_object_sha" =~ ^[0-9a-f]{40}$ ]] \
    || die "expected annotated tag object is invalid"
fi
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

remote_tag_state() {
  local ref_state tag_object_sha tag_object_type tag_state tag_commit
  ref_state="$(gh api "repos/${repository}/git/ref/tags/${tag}")" \
    || die "release tag reference could not be resolved"
  tag_object_sha="$(jq -er '.object.sha' <<< "$ref_state")" \
    || die "release tag reference is malformed"
  tag_object_type="$(jq -er '.object.type' <<< "$ref_state")" \
    || die "release tag reference is malformed"
  [[ "$tag_object_type" == tag && "$tag_object_sha" =~ ^[0-9a-f]{40}$ ]] \
    || die "release tag must remain annotated"
  tag_state="$(gh api "repos/${repository}/git/tags/${tag_object_sha}")" \
    || die "annotated release tag could not be resolved"
  tag_commit="$(jq -er 'select(.object.type == "commit") | .object.sha' <<< "$tag_state")" \
    || die "annotated release tag must resolve directly to a commit"
  [[ "$tag_commit" =~ ^[0-9a-f]{40}$ ]] || die "release tag commit could not be resolved"
  printf '%s\t%s\n' "$tag_object_sha" "$tag_commit"
}

IFS=$'\t' read -r tag_object_sha tag_commit < <(remote_tag_state)
if [[ -n "$expected_tag_object_sha" && "$tag_object_sha" != "$expected_tag_object_sha" ]]; then
  die "remote annotated release tag does not match the locally verified tag object"
fi
gh release download "$tag" --repo "$repository" --dir "$output_dir"
IFS=$'\t' read -r final_tag_object_sha final_tag_commit < <(remote_tag_state)
[[ "$final_tag_object_sha" == "$tag_object_sha" && "$final_tag_commit" == "$tag_commit" ]] \
  || die "release tag changed while its assets were being downloaded"
gh release view "$tag" --repo "$repository" --json isDraft,tagName \
  --jq 'select(.isDraft == false and .tagName != "") | .tagName' | grep -qx "$tag" \
  || die "release became unavailable or draft while its assets were being downloaded"
printf '%s\n' "$tag_commit" > "$output_dir/.release-tag-commit"

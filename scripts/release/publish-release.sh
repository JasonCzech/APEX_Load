#!/usr/bin/env bash
set -euo pipefail

die() {
  printf 'release publication: %s\n' "$*" >&2
  exit 1
}

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
release_tag="${1:-}"
release_commit="${2:-}"
repository="${3:-}"
release_dir="${4:-}"
public_key="${5:-}"
verifier="${6:-$root_dir/scripts/release/verify-release.sh}"

[[ "$release_tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z][0-9A-Za-z.-]*)?$ ]] \
  || die "release tag is invalid"
[[ "$release_commit" =~ ^[0-9a-f]{40}$ ]] || die "release commit is invalid"
[[ "$repository" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] || die "repository is invalid"
[[ -d "$release_dir" && -f "$public_key" && -x "$verifier" ]] \
  || die "release directory, public key, and verifier are required"
[[ -n "${GH_TOKEN:-${GH_ENTERPRISE_TOKEN:-}}" ]] || die "GitHub CLI token is required"

"$verifier" "$release_dir" "$release_tag" "$public_key"
server_url="${GITHUB_SERVER_URL:-https://github.com}"
[[ "$server_url" =~ ^https://[^/]+$ ]] || die "GITHUB_SERVER_URL must be an HTTPS origin"
export GH_HOST="${server_url#https://}"
if [[ "$GH_HOST" != github.com && -z "${GH_ENTERPRISE_TOKEN:-}" ]]; then
  export GH_ENTERPRISE_TOKEN="${GH_TOKEN:-}"
fi

marker="<!-- apexload-release:${repository}:${release_tag}:${release_commit} -->"
release_state="$(gh release view "$release_tag" --repo "$repository" --json isDraft,tagName,body,assets)"
jq -e --arg tag "$release_tag" --arg marker "$marker" \
  '.isDraft == true and .tagName == $tag and .body == $marker' \
  <<< "$release_state" >/dev/null \
  || die "release is no longer the authenticated draft reserved by this workflow"

local_names="$(find "$release_dir" -mindepth 1 -maxdepth 1 -type f -printf '%f\n' \
  | LC_ALL=C sort | jq -R -s -c 'split("\n")[:-1]')"
(( $(jq 'length' <<< "$local_names") > 0 )) || die "signed release bundle is empty"
remote_names="$(jq -c '[.assets[].name] | sort' <<< "$release_state")"
jq -e --argjson local "$local_names" '
  length as $count |
  all(.[];
    . as $name |
    test("^[A-Za-z0-9][A-Za-z0-9._+-]*$") and ($local | index($name)) != null
  ) and (unique | length) == $count
' <<< "$remote_names" >/dev/null \
  || die "draft contains an unexpected or duplicate asset name"

existing_dir="$(mktemp -d)"
stored_dir="$(mktemp -d)"
trap 'rm -rf "$existing_dir" "$stored_dir"' EXIT
if (( $(jq 'length' <<< "$remote_names") > 0 )); then
  gh release download "$release_tag" --repo "$repository" --dir "$existing_dir"
  while IFS= read -r name; do
    [[ -f "$release_dir/$name" && -f "$existing_dir/$name" ]] \
      || die "existing draft asset $name is missing locally or remotely"
    cmp "$release_dir/$name" "$existing_dir/$name" \
      || die "existing draft asset $name differs from the signed bundle"
  done < <(jq -r '.[]' <<< "$remote_names")
fi

while IFS= read -r name; do
  if ! jq -e --arg name "$name" 'index($name) != null' <<< "$remote_names" >/dev/null; then
    gh release upload "$release_tag" "$release_dir/$name" --repo "$repository"
  fi
done < <(jq -r '.[]' <<< "$local_names")

gh release download "$release_tag" --repo "$repository" --dir "$stored_dir"
stored_names="$(find "$stored_dir" -mindepth 1 -maxdepth 1 -type f -printf '%f\n' \
  | LC_ALL=C sort | jq -R -s -c 'split("\n")[:-1]')"
[[ "$stored_names" == "$local_names" ]] \
  || die "stored draft asset set does not exactly match the signed release bundle"
while IFS= read -r name; do
  cmp "$release_dir/$name" "$stored_dir/$name" \
    || die "stored draft asset $name differs from the signed bundle"
done < <(jq -r '.[]' <<< "$local_names")
"$verifier" "$stored_dir" "$release_tag" "$public_key"

gh release view "$release_tag" --repo "$repository" --json isDraft,tagName,body \
  | jq -e --arg tag "$release_tag" --arg marker "$marker" \
    '.isDraft == true and .tagName == $tag and .body == $marker' >/dev/null \
  || die "draft changed while its stored bytes were being verified"
gh release edit "$release_tag" --repo "$repository" --draft=false
gh release view "$release_tag" --repo "$repository" --json isDraft,tagName \
  | jq -e --arg tag "$release_tag" '.isDraft == false and .tagName == $tag' >/dev/null \
  || die "release did not transition from draft to published"

printf 'published byte-verified immutable release %s\n' "$release_tag"

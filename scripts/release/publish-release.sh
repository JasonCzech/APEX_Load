#!/usr/bin/env bash
set -euo pipefail

die() {
  printf 'release publication: %s\n' "$*" >&2
  exit 1
}

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "$root_dir/scripts/release/semver.sh"
release_tag="${1:-}"
release_commit="${2:-}"
repository="${3:-}"
release_dir="${4:-}"
public_key="${5:-}"
expected_tag_object_sha="${6:-}"
verifier="${7:-$root_dir/scripts/release/verify-release.sh}"

is_apex_release_tag "$release_tag" || die "release tag is invalid"
[[ "$release_commit" =~ ^[0-9a-f]{40}$ ]] || die "release commit is invalid"
[[ "$expected_tag_object_sha" =~ ^[0-9a-f]{40}$ ]] || die "verified annotated tag object is invalid"
[[ "$repository" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] || die "repository is invalid"
[[ -d "$release_dir" && -f "$public_key" && -x "$verifier" ]] \
  || die "release directory, public key, and verifier are required"
[[ -n "${GH_TOKEN:-${GH_ENTERPRISE_TOKEN:-}}" ]] || die "GitHub CLI token is required"

"$verifier" "$release_dir" "$release_tag" "$public_key" "$release_commit"
server_url="${GITHUB_SERVER_URL:-https://github.com}"
[[ "$server_url" =~ ^https://[^/]+$ ]] || die "GITHUB_SERVER_URL must be an HTTPS origin"
export GH_HOST="${server_url#https://}"
if [[ "$GH_HOST" != github.com && -z "${GH_ENTERPRISE_TOKEN:-}" ]]; then
  export GH_ENTERPRISE_TOKEN="${GH_TOKEN:-}"
fi
if [[ "$GH_HOST" == github.com ]]; then
  gh api -H 'X-GitHub-Api-Version: 2026-03-10' \
    "repos/${repository}/immutable-releases" \
    | jq -e '.enabled == true' >/dev/null \
    || die "GitHub immutable releases must be enabled before publication"
fi

remote_release_tag_state() {
  local ref_state object_type object_sha tag_state commit
  ref_state="$(gh api "repos/${repository}/git/ref/tags/${release_tag}")" \
    || die "could not resolve the remote release tag"
  object_type="$(jq -er '.object.type' <<< "$ref_state")" \
    || die "remote release tag reference is malformed"
  object_sha="$(jq -er '.object.sha' <<< "$ref_state")" \
    || die "remote release tag reference is malformed"
  [[ "$object_type" == tag && "$object_sha" =~ ^[0-9a-f]{40}$ ]] \
    || die "remote release tag is no longer annotated"
  tag_state="$(gh api "repos/${repository}/git/tags/${object_sha}")" \
    || die "could not resolve the remote annotated release tag"
  commit="$(jq -er 'select(.object.type == "commit") | .object.sha' <<< "$tag_state")" \
    || die "remote annotated release tag does not resolve directly to a commit"
  [[ "$commit" =~ ^[0-9a-f]{40}$ ]] || die "remote release tag commit is malformed"
  printf '%s\t%s\n' "$object_sha" "$commit"
}

assert_remote_release_tag() {
  local remote_object_sha remote_commit
  IFS=$'\t' read -r remote_object_sha remote_commit < <(remote_release_tag_state)
  [[ "$remote_object_sha" == "$expected_tag_object_sha" ]] \
    || die "remote annotated release tag object changed; expected ${expected_tag_object_sha}"
  [[ "$remote_commit" == "$release_commit" ]] \
    || die "remote release tag moved to ${remote_commit}; expected ${release_commit}"
}

assert_remote_release_tag
marker="<!-- apexload-release:${repository}:${release_tag}:${release_commit} -->"
local_names="$(find "$release_dir" -mindepth 1 -maxdepth 1 -type f -printf '%f\n' \
  | LC_ALL=C sort | jq -R -s -c 'split("\n")[:-1]')"
(( $(jq 'length' <<< "$local_names") > 0 )) || die "signed release bundle is empty"
local_asset_contract="$(
  while IFS= read -r name; do
    size="$(stat -c '%s' "$release_dir/$name" 2>/dev/null || stat -f '%z' "$release_dir/$name")"
    digest="$(sha256sum "$release_dir/$name" | awk '{print $1}')"
    jq -cn --arg name "$name" --argjson size "$size" --arg digest "sha256:${digest}" \
      '{name:$name,size:$size,digest:$digest,state:"uploaded"}'
  done < <(jq -r '.[]' <<< "$local_names") \
    | jq -sc 'sort_by(.name)'
)"

release_api_state() {
  gh api -H 'X-GitHub-Api-Version: 2026-03-10' \
    "repos/${repository}/releases/tags/${release_tag}"
}

assert_release_asset_contract() {
  local expected_draft="$1"
  local expected_immutable="$2"
  local state
  state="$(release_api_state)" || die "release state could not be loaded"
  jq -e \
    --arg tag "$release_tag" \
    --arg marker "$marker" \
    --argjson expected_draft "$expected_draft" \
    --argjson expected_immutable "$expected_immutable" \
    --arg host "$GH_HOST" \
    --argjson local "$local_asset_contract" '
      (
        .tag_name == $tag and
        .body == $marker and
        .draft == $expected_draft and
        ($host != "github.com" or .immutable == $expected_immutable)
      ) and (
        ([.assets[] | {
          name,
          size,
          digest: (.digest // null),
          state
        }] | sort_by(.name)) as $remote |
        ($remote | map(.name)) == ($local | map(.name)) and
        ($remote | map({name,size,state})) == ($local | map({name,size,state})) and
        ($remote | all(.name | test("^[A-Za-z0-9][A-Za-z0-9._+-]*$"))) and
        ($remote | map(.name) | unique | length) == ($remote | length) and
        if $host == "github.com" then
          $remote == $local
        else
          all(range(0; $remote | length);
            ($remote[.].digest == null or $remote[.].digest == $local[.].digest))
        end
      )
    ' <<< "$state" >/dev/null \
    || die "release metadata or asset contract changed"
  printf '%s' "$state"
}

initial_state="$(release_api_state)" || die "release state could not be loaded"
jq -e --arg tag "$release_tag" --arg marker "$marker" \
  '.draft == true and .tag_name == $tag and .body == $marker' \
  <<< "$initial_state" >/dev/null \
  || die "release is no longer the authenticated draft reserved by this workflow"
remote_names="$(jq -c '[.assets[].name] | sort' <<< "$initial_state")"
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
"$verifier" "$stored_dir" "$release_tag" "$public_key" "$release_commit"

assert_release_asset_contract true false >/dev/null
assert_remote_release_tag
gh release edit "$release_tag" --repo "$repository" --draft=false
assert_release_asset_contract false true >/dev/null
assert_remote_release_tag

if [[ "$GH_HOST" == github.com ]]; then
  printf 'published byte-verified immutable release %s\n' "$release_tag"
else
  printf 'published byte-verified tamper-evident release %s\n' "$release_tag"
fi

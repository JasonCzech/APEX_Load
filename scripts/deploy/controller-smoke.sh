#!/usr/bin/env bash
set -euo pipefail

die() {
  printf 'controller smoke: %s\n' "$*" >&2
  exit 1
}

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "$root_dir/scripts/release/semver.sh"
base_url="${1:-}"
expected_revision="${2:-}"
expected_version="${3:-}"
expected_build_time="${4:-}"
[[ "$base_url" == http://* || "$base_url" == https://* ]] || die "controller URL is invalid"
[[ "$expected_revision" =~ ^[0-9a-f]{40}$ ]] || die "expected revision is invalid"
expected_commit="${expected_revision:0:7}"
if [[ -n "$expected_version" || -n "$expected_build_time" ]]; then
  is_apex_semver "$expected_version" || die "expected version is invalid"
  [[ "$expected_build_time" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$ ]] \
    || die "expected build time is invalid"
fi

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

retry_get() {
  local path="$1"
  local output="$2"
  local attempt
  for attempt in $(seq 1 30); do
    if curl --fail --silent --show-error --location --max-redirs 0 \
      --connect-timeout 5 --max-time 15 \
      "${base_url%/}${path}" -o "$output"; then
      return 0
    fi
    printf 'request %s attempt %s/30 did not succeed; retrying\n' "$path" "$attempt" >&2
    sleep 5
  done
  return 1
}

retry_get /api/v1/system/health "$tmp_dir/health.json" || die "health endpoint did not recover"
jq -e --arg revision "$expected_revision" --arg commit "$expected_commit" \
  '.status == "healthy" and .revision == $revision and .commit == $commit and (.capacity_availability.live_available_generators // 0) > 0' \
  "$tmp_dir/health.json" >/dev/null \
  || die "health metadata or live generator connectivity does not match the release"
if [[ -n "$expected_version" ]]; then
  jq -e --arg version "$expected_version" --arg build_time "$expected_build_time" \
    '.version == $version and .build_time == $build_time' \
    "$tmp_dir/health.json" >/dev/null \
    || die "health version or build time does not match the signed release manifest"
fi
retry_get /api/v1/system/readiness "$tmp_dir/readiness.json" || die "readiness endpoint did not recover"
jq -e '.status == "ready"' "$tmp_dir/readiness.json" >/dev/null \
  || die "controller reports not-ready"
retry_get / "$tmp_dir/index.html" || die "embedded dashboard index is unavailable"
grep -qi '<html' "$tmp_dir/index.html" || die "dashboard response is not HTML"

assets=()
while IFS= read -r asset; do
  assets+=("$asset")
done < <(grep -Eo '(src|href)="/[^"?#]+' "$tmp_dir/index.html" \
  | sed -E 's/^(src|href)="//' | sort -u)
(( ${#assets[@]} > 0 )) || die "dashboard does not reference any assets"
for asset in "${assets[@]}"; do
  retry_get "$asset" "$tmp_dir/asset" || die "dashboard asset $asset is unavailable"
  [[ -s "$tmp_dir/asset" ]] || die "dashboard asset $asset is empty"
done

printf 'controller health, build metadata, readiness, generator connectivity, and dashboard verified\n'

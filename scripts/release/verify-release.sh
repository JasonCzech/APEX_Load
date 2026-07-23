#!/usr/bin/env bash
set -euo pipefail

die() {
  printf 'release verification: %s\n' "$*" >&2
  exit 1
}

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "$root_dir/scripts/release/semver.sh"
release_dir="${1:-}"
expected_tag="${2:-}"
public_key="${3:-}"
expected_commit="${4:-}"
[[ -d "$release_dir" ]] || die "release directory does not exist"
is_apex_release_tag "$expected_tag" || die "expected tag is not canonical semantic version"
[[ -f "$public_key" ]] || die "portable release public key does not exist"
for required in release-manifest.json SHA256SUMS SHA256SUMS.sig; do
  [[ -f "$release_dir/$required" ]] || die "$required is missing"
done

openssl dgst -sha256 -verify "$public_key" \
  -signature "$release_dir/SHA256SUMS.sig" "$release_dir/SHA256SUMS" >/dev/null \
  || die "SHA256SUMS signature is invalid"

checksum_names="$(mktemp)"
checksum_names_sorted="$(mktemp)"
checksum_names_unique="$(mktemp)"
actual_names="$(mktemp)"
trap 'rm -f "$checksum_names" "$checksum_names_sorted" "$checksum_names_unique" "$actual_names"' EXIT
checksum_line_pattern='^[0-9a-f]{64}  ([A-Za-z0-9][A-Za-z0-9._+-]*)$'
while IFS= read -r checksum_line || [[ -n "$checksum_line" ]]; do
  [[ "$checksum_line" =~ $checksum_line_pattern ]] \
    || die "SHA256SUMS contains an unsafe or malformed asset entry"
  asset_name="${BASH_REMATCH[1]}"
  case "$asset_name" in
    SHA256SUMS|SHA256SUMS.sig) die "SHA256SUMS must not authenticate itself or its signature" ;;
  esac
  printf '%s\n' "$asset_name" >> "$checksum_names"
done < "$release_dir/SHA256SUMS"
[[ -s "$checksum_names" ]] || die "SHA256SUMS does not authenticate any release assets"
LC_ALL=C sort "$checksum_names" > "$checksum_names_sorted"
LC_ALL=C sort -u "$checksum_names" > "$checksum_names_unique"
cmp "$checksum_names_sorted" "$checksum_names_unique" >/dev/null \
  || die "SHA256SUMS contains duplicate asset entries"
shopt -s nullglob dotglob
for release_entry in "$release_dir"/*; do
  [[ -f "$release_entry" && ! -L "$release_entry" ]] \
    || die "release bundle contains a non-regular entry"
  release_name="${release_entry##*/}"
  case "$release_name" in
    SHA256SUMS|SHA256SUMS.sig|.release-tag-commit) continue ;;
  esac
  printf '%s\n' "$release_name" >> "$actual_names"
done
shopt -u nullglob dotglob
LC_ALL=C sort -o "$actual_names" "$actual_names"
cmp "$checksum_names_sorted" "$actual_names" >/dev/null \
  || die "release asset set does not exactly match signed SHA256SUMS"
(
  cd "$release_dir"
  sha256sum --check --strict SHA256SUMS
)

manifest="$release_dir/release-manifest.json"
manifest_schema="$(jq -r '.schema_version' "$manifest")"
[[ "$manifest_schema" == 1 || "$manifest_schema" == 2 ]] || die "unsupported manifest schema"
[[ "$(jq -r '.tag' "$manifest")" == "$expected_tag" ]] || die "manifest tag mismatch"
if [[ -z "$expected_commit" && -f "$release_dir/.release-tag-commit" ]]; then
  expected_commit="$(tr -d '[:space:]' < "$release_dir/.release-tag-commit")"
fi
[[ -n "$expected_commit" ]] || die "expected release commit is required"
[[ "$expected_commit" =~ ^[0-9a-f]{40}$ ]] || die "expected release commit is invalid"
[[ "$(jq -r '.commit' "$manifest")" == "$expected_commit" ]] \
  || die "manifest commit does not match the verified release tag commit"
[[ "$(jq -r '.canonical_apexctl_version' "$manifest")" == "$expected_tag" ]] \
  || die "canonical apexctl version mismatch"
jq -e '
  . as $root |
  (.commit | test("^[0-9a-f]{40}$")) and
  ((.images | length) == 4 or (.images | length) == 5) and
  (all(.images[]; .digest | test("^sha256:[0-9a-f]{64}$"))) and
  (all(.images[]; . as $image |
    (.platforms | length == 2) and
    ([.platforms[].architecture] | sort == ["amd64", "arm64"]) and
    (all(.platforms[];
      (.os == "linux") and
      (.architecture == "amd64" or .architecture == "arm64") and
      (.digest | test("^sha256:[0-9a-f]{64}$")) and
      (.sbom_sha256 | test("^[0-9a-f]{64}$")) and
      (.sbom_asset == ("sbom-apexload-" + $image.component + "-" + $image.profile + "-linux-" + .architecture + ".spdx.json")) and
      (. as $platform | ([ $root.assets[] | select(
        .asset == $platform.sbom_asset and .sha256 == $platform.sbom_sha256
      )] | length) == 1)
    ))
  )) and
  (([.images[] | [.component, .profile]] | sort) as $image_set |
    ($image_set == [["controller", "enterprise"], ["controller", "standard"], ["generator", "enterprise"], ["generator", "standard"]]) or
    ($image_set == [["controller", "enterprise"], ["controller", "standard"], ["generator", "enterprise"], ["generator", "standard"], ["java-vuser-runner", "standard"]])) and
  (.binaries | length == 5) and
  ([.binaries[] | [.os, .architecture]] | unique | length == 5) and
  (.desktop_clients | length == 5) and
  ([.desktop_clients[] | [.os, .architecture]] | unique | length == 5) and
  ((.schema_version == 1) or (
    (.schema_version == 2) and
    (.desktop_installers | length == 2) and
    ([.desktop_installers[] | [.os, .architecture, .format]] | sort == [["linux", "amd64", "deb"], ["linux", "arm64", "deb"]]) and
    ([.desktop_installers[].asset] | unique | length == 2) and
    (all(.desktop_installers[];
      (.name == "apexload-desktop") and
      (.asset == ("apexload-desktop-" + $root.version + "-linux-" + .architecture + ".deb")) and
      (.sha256 | test("^[0-9a-f]{64}$")) and
      (. as $installer | ([ $root.assets[] | select(
        .asset == $installer.asset and .sha256 == $installer.sha256
      )] | length) == 1)
    ))
  )) and
  (.server_bundles | length == 2) and
  ([.server_bundles[] | [.os, .architecture]] | unique | length == 2) and
  (all(.assets[]; (.sha256 | test("^[0-9a-f]{64}$")) and (.size > 0)))
' "$manifest" >/dev/null || die "manifest structure is invalid"

while IFS=$'\t' read -r asset expected; do
  [[ -f "$release_dir/$asset" ]] || die "manifest asset $asset is missing"
  actual="$(sha256sum "$release_dir/$asset" | awk '{print $1}')"
  [[ "$actual" == "$expected" ]] || die "manifest checksum mismatch for $asset"
done < <(jq -r '.assets[] | [.asset, .sha256] | @tsv' "$manifest")

chart_asset="$(jq -r '.helm.asset' "$manifest")"
chart_digest="$(jq -r '.helm.digest | sub("^sha256:"; "")' "$manifest")"
[[ "$(sha256sum "$release_dir/$chart_asset" | awk '{print $1}')" == "$chart_digest" ]] \
  || die "Helm chart digest mismatch"
app_asset="$(jq -r '.app_service.asset' "$manifest")"
app_digest="$(jq -r '.app_service.sha256' "$manifest")"
[[ "$(sha256sum "$release_dir/$app_asset" | awk '{print $1}')" == "$app_digest" ]] \
  || die "App Service package checksum mismatch"

printf 'verified signed release %s\n' "$expected_tag"

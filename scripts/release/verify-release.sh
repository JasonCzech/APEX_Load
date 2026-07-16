#!/usr/bin/env bash
set -euo pipefail

die() {
  printf 'release verification: %s\n' "$*" >&2
  exit 1
}

release_dir="${1:-}"
expected_tag="${2:-}"
public_key="${3:-}"
[[ -d "$release_dir" ]] || die "release directory does not exist"
[[ "$expected_tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z][0-9A-Za-z.-]*)?$ ]] \
  || die "expected tag is not semantic"
[[ -f "$public_key" ]] || die "portable release public key does not exist"
for required in release-manifest.json SHA256SUMS SHA256SUMS.sig; do
  [[ -f "$release_dir/$required" ]] || die "$required is missing"
done

openssl dgst -sha256 -verify "$public_key" \
  -signature "$release_dir/SHA256SUMS.sig" "$release_dir/SHA256SUMS" >/dev/null \
  || die "SHA256SUMS signature is invalid"
(
  cd "$release_dir"
  sha256sum --check --strict SHA256SUMS
)

manifest="$release_dir/release-manifest.json"
[[ "$(jq -r '.schema_version' "$manifest")" == 1 ]] || die "unsupported manifest schema"
[[ "$(jq -r '.tag' "$manifest")" == "$expected_tag" ]] || die "manifest tag mismatch"
[[ "$(jq -r '.canonical_apexctl_version' "$manifest")" == "$expected_tag" ]] \
  || die "canonical apexctl version mismatch"
jq -e '
  . as $root |
  (.commit | test("^[0-9a-f]{40}$")) and
  (.images | length == 4) and
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
  ([.images[] | [.component, .profile]] | unique | length == 4) and
  (.binaries | length == 5) and
  ([.binaries[] | [.os, .architecture]] | unique | length == 5) and
  (.desktop_clients | length == 5) and
  ([.desktop_clients[] | [.os, .architecture]] | unique | length == 5) and
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

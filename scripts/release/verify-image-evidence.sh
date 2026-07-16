#!/usr/bin/env bash
set -euo pipefail

die() {
  printf 'image evidence: %s\n' "$*" >&2
  exit 1
}

manifest="${1:-}"
component="${2:-}"
profile="${3:-}"
public_key="${4:-}"

[[ -f "$manifest" && -f "$public_key" ]] || die "manifest and Cosign public key are required"
[[ "$component" == controller || "$component" == generator ]] || die "invalid component"
[[ "$profile" == standard || "$profile" == enterprise ]] || die "invalid profile"
command -v jq >/dev/null || die "jq is required"
command -v cosign >/dev/null || die "cosign is required"

row="$(jq -cer --arg component "$component" --arg profile "$profile" '
  [.images[] | select(.component == $component and .profile == $profile)]
  | if length == 1 then .[0] else error("expected exactly one matching image") end
' "$manifest")" || die "release manifest image record is missing or duplicated"
repository="$(jq -er '.repository' <<< "$row")"
digest="$(jq -er '.digest' <<< "$row")"
[[ "$repository" =~ ^[A-Za-z0-9.-]+(:[0-9]+)?/[A-Za-z0-9._/-]+$ ]] || die "invalid image repository"
[[ "$digest" =~ ^sha256:[0-9a-f]{64}$ ]] || die "invalid image index digest"

cosign verify --key "$public_key" --insecure-ignore-tlog=true "${repository}@${digest}" >/dev/null
platform_count=0
while IFS= read -r platform; do
  os="$(jq -er '.os' <<< "$platform")"
  architecture="$(jq -er '.architecture' <<< "$platform")"
  platform_digest="$(jq -er '.digest' <<< "$platform")"
  sbom_asset="$(jq -er '.sbom_asset' <<< "$platform")"
  sbom_sha256="$(jq -er '.sbom_sha256' <<< "$platform")"
  [[ "$os" == linux ]] || die "unsupported image platform OS"
  [[ "$architecture" == amd64 || "$architecture" == arm64 ]] || die "unsupported image architecture"
  [[ "$platform_digest" =~ ^sha256:[0-9a-f]{64}$ ]] || die "invalid image platform digest"
  [[ "$sbom_asset" =~ ^sbom-apexload-(controller|generator)-(standard|enterprise)-linux-(amd64|arm64)\.spdx\.json$ ]] \
    || die "invalid SBOM asset name"
  [[ "$sbom_sha256" =~ ^[0-9a-f]{64}$ ]] || die "invalid SBOM checksum"
  sbom_path="$(dirname "$manifest")/$sbom_asset"
  [[ -s "$sbom_path" ]] || die "signed SBOM asset is missing"
  actual_sbom_sha256="$(sha256sum "$sbom_path" | awk '{print $1}')"
  [[ "$actual_sbom_sha256" == "$sbom_sha256" ]] || die "signed SBOM asset checksum does not match the manifest"
  cosign verify --key "$public_key" --insecure-ignore-tlog=true "${repository}@${platform_digest}" >/dev/null
  "$(dirname "${BASH_SOURCE[0]}")/verify-sbom-attestation.sh" \
    "${repository}@${platform_digest}" "$public_key" "$sbom_path"
  platform_count=$((platform_count + 1))
done < <(jq -cer '.platforms[]' <<< "$row")
(( platform_count == 2 )) || die "image platform evidence is incomplete"

printf '%s@%s\n' "$repository" "$digest"

#!/usr/bin/env bash
set -euo pipefail

die() {
  printf 'registry image evidence: %s\n' "$*" >&2
  exit 1
}

image="${1:-}"
public_key="${2:-}"
amd64_sbom="${3:-}"
arm64_sbom="${4:-}"
[[ "$image" =~ ^[A-Za-z0-9.-]+(:[0-9]+)?/[A-Za-z0-9._/-]+@sha256:[0-9a-f]{64}$ ]] \
  || die "an immutable image reference is required"
[[ -f "$public_key" ]] || die "Cosign public key is required"
[[ -s "$amd64_sbom" && -s "$arm64_sbom" ]] || die "exact amd64 and arm64 SBOM assets are required"
command -v cosign >/dev/null || die "cosign is required"
command -v skopeo >/dev/null || die "skopeo is required"
command -v jq >/dev/null || die "jq is required"

cosign verify --key "$public_key" --insecure-ignore-tlog=true "$image" >/dev/null
index="$(skopeo inspect --raw "docker://${image}")" || die "could not inspect image index"
platform_count=0
while IFS=$'\t' read -r architecture digest; do
  [[ "$digest" =~ ^sha256:[0-9a-f]{64}$ ]] || die "invalid platform digest"
  [[ "$architecture" == amd64 || "$architecture" == arm64 ]] || die "invalid platform architecture"
  repository="${image%@*}"
  sbom="$amd64_sbom"
  [[ "$architecture" == arm64 ]] && sbom="$arm64_sbom"
  cosign verify --key "$public_key" --insecure-ignore-tlog=true "${repository}@${digest}" >/dev/null
  "$(dirname "${BASH_SOURCE[0]}")/verify-sbom-attestation.sh" \
    "${repository}@${digest}" "$public_key" "$sbom"
  platform_count=$((platform_count + 1))
done < <(jq -er '
  [.manifests[] | select(
    .platform.os == "linux" and
    (.platform.architecture == "amd64" or .platform.architecture == "arm64")
  )]
  | if length == 2 and ([.[].platform.architecture] | unique | length) == 2
    then .[] | [.platform.architecture, .digest] | @tsv
    else error("expected exactly linux/amd64 and linux/arm64") end
' <<< "$index")
(( platform_count == 2 )) || die "image platform evidence is incomplete"

printf 'verified signatures and SPDX attestations for %s\n' "$image"

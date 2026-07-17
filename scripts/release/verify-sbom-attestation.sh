#!/usr/bin/env bash
set -euo pipefail

die() {
  printf 'SBOM attestation: %s\n' "$*" >&2
  exit 1
}

image="${1:-}"
public_key="${2:-}"
sbom="${3:-}"
[[ "$image" =~ @sha256:[0-9a-f]{64}$ ]] || die "immutable image reference is required"
[[ -f "$public_key" && -s "$sbom" ]] || die "public key and signed SBOM asset are required"

tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT
cosign verify-attestation --key "$public_key" --type spdxjson --output json "$image" > "$tmp"
expected="$(jq -S -c . "$sbom" | sha256sum | awk '{print $1}')"
matched=0
while IFS= read -r payload; do
  actual="$(printf '%s' "$payload" | base64 --decode | jq -S -c '.predicate' \
    | sha256sum | awk '{print $1}')" || continue
  if [[ "$actual" == "$expected" ]]; then
    matched=1
    break
  fi
done < <(jq -er 'if type == "array" then .[].payload else .payload end' "$tmp")
(( matched == 1 )) || die "verified attestation predicate does not match the signed SBOM asset"

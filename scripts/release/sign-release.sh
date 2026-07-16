#!/usr/bin/env bash
set -euo pipefail

die() {
  printf 'release signing: %s\n' "$*" >&2
  exit 1
}

release_dir="${1:-}"
private_key="${2:-}"
public_key="${3:-}"
external_signer="${APEXLOAD_RELEASE_SIGNER_PATH:-}"
[[ -d "$release_dir" ]] || die "release directory does not exist"
[[ -f "$public_key" ]] || die "public verification key does not exist"
[[ -f "$release_dir/release-manifest.json" ]] || die "release-manifest.json is missing"
if [[ -n "$external_signer" ]]; then
  [[ "$external_signer" == /* && -x "$external_signer" ]] \
    || die "external signer must be an executable absolute path"
else
  [[ -f "$private_key" ]] || die "private signing key does not exist"
fi

(
  cd "$release_dir"
  checksum_tmp="$(mktemp .SHA256SUMS.XXXXXX)"
  trap 'rm -f "$checksum_tmp"' EXIT
  find . -maxdepth 1 -type f \
    ! -name SHA256SUMS \
    ! -name SHA256SUMS.sig \
    ! -name '.SHA256SUMS.*' \
    -print0 \
    | sort -z \
    | xargs -0 sha256sum \
    | sed 's#  \./#  #' > "$checksum_tmp"
  mv "$checksum_tmp" SHA256SUMS
)
if [[ -n "$external_signer" ]]; then
  "$external_signer" "$release_dir/SHA256SUMS" "$release_dir/SHA256SUMS.sig"
  [[ -s "$release_dir/SHA256SUMS.sig" ]] || die "external signer did not create a signature"
else
  openssl dgst -sha256 -sign "$private_key" \
    -out "$release_dir/SHA256SUMS.sig" "$release_dir/SHA256SUMS"
fi
openssl dgst -sha256 -verify "$public_key" \
  -signature "$release_dir/SHA256SUMS.sig" "$release_dir/SHA256SUMS" >/dev/null \
  || die "newly-created checksum signature did not verify"

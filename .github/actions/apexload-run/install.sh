#!/usr/bin/env bash
set -euo pipefail

emit_output() {
  local key="$1"
  local value="$2"
  local attempt=0
  local delimiter
  [[ "$key" =~ ^[A-Za-z_][A-Za-z0-9_-]*$ ]] || return 1
  while :; do
    delimiter="__APEXLOAD_OUTPUT_${RANDOM}_${RANDOM}_$$_${attempt}__"
    if ! printf '%s\n' "$value" | tr -d '\r' | grep -Fqx -- "$delimiter"; then
      break
    fi
    attempt=$((attempt + 1))
  done
  {
    printf '%s<<%s\n' "$key" "$delimiter"
    # Always add a separator newline. If value already ends in a newline, this
    # preserves that trailing newline in the parsed environment-file value.
    printf '%s\n' "$value"
    printf '%s\n' "$delimiter"
  } >> "${GITHUB_OUTPUT:?GITHUB_OUTPUT is required}"
}

die() {
  local outcome="$1"
  shift
  if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
    emit_output failure-outcome "$outcome" || true
  fi
  printf 'apexload action: %s\n' "$*" >&2
  exit 2
}

explicit_path="${APEXLOAD_INPUT_APEXCTL_PATH:-}"
requested_version="${APEXLOAD_INPUT_APEXCTL_VERSION:-}"

if [[ -n "$explicit_path" ]]; then
  [[ -f "$explicit_path" && -x "$explicit_path" ]] || die configuration_error "apexctl-path is not an executable file"
  resolved_path="$(cd "$(dirname "$explicit_path")" && pwd -P)/$(basename "$explicit_path")"
  actual_version="$("$resolved_path" version 2>/dev/null | awk 'NR == 1 {print $2}')" \
    || die configuration_error "apexctl-path does not expose a usable version command"
  [[ -n "$actual_version" ]] || die configuration_error "apexctl-path returned an empty version"
  if [[ -n "$requested_version" && "${actual_version#v}" != "${requested_version#v}" ]]; then
    die configuration_error "apexctl-path version does not match the requested version"
  fi
  emit_output path "$resolved_path"
  emit_output version "$actual_version"
  exit 0
fi

if [[ -z "$requested_version" ]]; then
  requested_version="$(tr -d '[:space:]' < "${GITHUB_ACTION_PATH:?}/apexctl-version")"
fi
[[ "$requested_version" =~ ^v?[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]] || die configuration_error "apexctl-version is not a semantic version"
version_number="${requested_version#v}"
version_tag="v${version_number}"

case "${RUNNER_OS:-}" in
  Linux) os=linux ;;
  macOS) os=darwin ;;
  Windows) os=windows ;;
  *) die configuration_error "unsupported runner OS" ;;
esac
case "${RUNNER_ARCH:-}" in
  X64) arch=amd64 ;;
  ARM64) arch=arm64 ;;
  *) die configuration_error "unsupported runner architecture" ;;
esac

suffix=""
[[ "$os" == windows ]] && suffix=".exe"
asset="apexctl_${version_number}_${os}_${arch}${suffix}"

release_base="${APEXLOAD_INPUT_RELEASE_BASE_URL:-}"
if [[ -z "$release_base" ]]; then
  [[ -n "${GITHUB_SERVER_URL:-}" && -n "${GITHUB_ACTION_REPOSITORY:-}" ]] || die configuration_error "cannot derive the release URL"
  release_base="${GITHUB_SERVER_URL%/}/${GITHUB_ACTION_REPOSITORY}/releases/download"
fi
[[ "$release_base" == https://* ]] || die configuration_error "release-base-url must use HTTPS"
release_url="${release_base%/}/${version_tag}"

install_dir="${RUNNER_TEMP:?RUNNER_TEMP is required}/apexload/${version_tag}/${os}-${arch}"
mkdir -p "$install_dir" || die configuration_error "cannot create the apexctl install directory"
checksums="$install_dir/SHA256SUMS"
binary="$install_dir/$asset"
manifest="$install_dir/release-manifest.json"

download_asset() {
  local url="$1"
  local output="$2"
  curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 \
    "$url" --output "$output" || die execution_error "failed to download a required signed release asset"
}

download_asset "$release_url/SHA256SUMS" "$checksums"
download_asset "$release_url/$asset" "$binary"
download_asset "$release_url/release-manifest.json" "$manifest"

signing_key="${APEXLOAD_INPUT_RELEASE_PUBLIC_KEY:-}"
if [[ -z "$signing_key" && -f "$GITHUB_ACTION_PATH/release-signing-key.pem" ]]; then
  signing_key="$GITHUB_ACTION_PATH/release-signing-key.pem"
fi
if [[ -n "$signing_key" ]]; then
  [[ -f "$signing_key" ]] || die configuration_error "release-public-key does not exist"
  signature="$install_dir/SHA256SUMS.sig"
  download_asset "$release_url/SHA256SUMS.sig" "$signature"
  openssl dgst -sha256 -verify "$signing_key" -signature "$signature" "$checksums" >/dev/null \
    || die execution_error "release checksum signature verification failed"
else
  die configuration_error "no trusted release signing key is bundled or configured"
fi

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    die configuration_error "sha256sum or shasum is required"
  fi
}

verify_checksum() {
  local file="$1"
  local name="$2"
  local expected actual
  expected="$(awk -v asset="$name" '$2 == asset || $2 == "*" asset {print $1}' "$checksums")"
  [[ "$expected" =~ ^[0-9a-fA-F]{64}$ ]] || die execution_error "SHA256SUMS is missing a required unique digest"
  actual="$(sha256_file "$file")"
  [[ "$(printf '%s' "$actual" | tr '[:upper:]' '[:lower:]')" == "$(printf '%s' "$expected" | tr '[:upper:]' '[:lower:]')" ]] \
    || die execution_error "release asset checksum verification failed"
}

verify_checksum "$manifest" release-manifest.json
verify_checksum "$binary" "$asset"
command -v node >/dev/null 2>&1 \
  || die configuration_error "the GitHub Actions runner Node runtime is required to validate the release manifest"
binary_checksum="$(sha256_file "$binary" | tr '[:upper:]' '[:lower:]')"
node "$GITHUB_ACTION_PATH/verify-manifest.js" \
  "$manifest" "$version_tag" "$os" "$arch" "$asset" "$binary_checksum" \
  || die execution_error "signed release manifest validation failed"
chmod 0755 "$binary" || die execution_error "cannot make the verified apexctl binary executable"
actual_version="$("$binary" version 2>/dev/null | awk 'NR == 1 {print $2}')" \
  || die execution_error "downloaded apexctl does not expose a usable version command"
[[ "${actual_version#v}" == "$version_number" ]] \
  || die execution_error "downloaded apexctl version does not match its signed release"

emit_output path "$binary"
emit_output version "$version_tag"

#!/usr/bin/env bash
set -euo pipefail

readonly SKOPEO_VERSION="1.20.0"
readonly SKOPEO_LINUX_AMD64_SHA256="e5d3f410839313c0b28ef4659643146dad88653fc831aa15adf705421fa06fc0"
readonly SKOPEO_LINUX_ARM64_SHA256="c53670037b0a861ffaae1ce11baaca511d0722e4b537ab11c19a04660d8e6021"
readonly DEFAULT_RELEASE_BASE_URL="https://github.com/lework/skopeo-binary/releases/download/v${SKOPEO_VERSION}"

die() {
  printf 'Skopeo installation failed: %s\n' "$*" >&2
  exit 1
}

[[ "$#" -eq 1 ]] || die "usage: $0 <destination-directory>"
[[ "$(uname -s)" == Linux ]] || die "only Linux deployment runners are supported"
command -v curl >/dev/null || die "curl is required"
command -v sha256sum >/dev/null || die "sha256sum is required"
command -v install >/dev/null || die "install is required"

case "$(uname -m)" in
  x86_64|amd64)
    architecture="amd64"
    expected_sha256="$SKOPEO_LINUX_AMD64_SHA256"
    ;;
  aarch64|arm64)
    architecture="arm64"
    expected_sha256="$SKOPEO_LINUX_ARM64_SHA256"
    ;;
  *)
    die "unsupported deployment runner architecture"
    ;;
esac

release_base_url="${SKOPEO_RELEASE_BASE_URL:-$DEFAULT_RELEASE_BASE_URL}"
[[ "$release_base_url" =~ ^https://[^[:space:]?#]+$ ]] \
  || die "SKOPEO_RELEASE_BASE_URL must be an HTTPS URL without a query or fragment"

destination_directory="$1"
download_path="$(mktemp)"
trap 'rm -f "$download_path"' EXIT
curl --proto '=https' --proto-redir '=https' --tlsv1.2 --fail --location --silent --show-error \
  --output "$download_path" \
  "${release_base_url%/}/skopeo-linux-${architecture}"
printf '%s  %s\n' "$expected_sha256" "$download_path" | sha256sum --check --status \
  || die "downloaded Skopeo checksum verification failed"

mkdir -p "$destination_directory"
install -m 0755 "$download_path" "$destination_directory/skopeo"
version_output="$("$destination_directory/skopeo" --version 2>&1)" \
  || die "installed Skopeo could not be executed"
[[ "$version_output" == "skopeo version ${SKOPEO_VERSION}" ]] \
  || die "installed Skopeo version did not match ${SKOPEO_VERSION}"

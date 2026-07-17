#!/usr/bin/env bash
set -euo pipefail

readonly BUILDX_VERSION="v0.34.1"
# Reviewed against the official v0.34.1 checksums.txt release asset.
readonly BUILDX_LINUX_AMD64_SHA256="f1332ddb9010bd0b72628266c3a906d9a6979848033df4c8d9bd2cd113bae12b"
readonly BUILDX_LINUX_ARM64_SHA256="c34e32dd6ea2653d960d6c099c9f09b9077e4a37504d2d31e5066eccc3904231"
readonly DEFAULT_RELEASE_BASE_URL="https://github.com/docker/buildx/releases/download/${BUILDX_VERSION}"

die() {
  printf 'Buildx installation failed: %s\n' "$*" >&2
  exit 1
}

[[ "$#" -eq 1 ]] || die "usage: $0 <docker-config-directory>"
[[ "$(uname -s)" == Linux ]] || die "only Linux release runners are supported"
command -v curl >/dev/null || die "curl is required"
command -v sha256sum >/dev/null || die "sha256sum is required"
command -v install >/dev/null || die "install is required"

case "$(uname -m)" in
  x86_64|amd64)
    architecture="amd64"
    expected_sha256="$BUILDX_LINUX_AMD64_SHA256"
    ;;
  aarch64|arm64)
    architecture="arm64"
    expected_sha256="$BUILDX_LINUX_ARM64_SHA256"
    ;;
  *)
    die "unsupported release runner architecture"
    ;;
esac

release_base_url="${BUILDX_RELEASE_BASE_URL:-$DEFAULT_RELEASE_BASE_URL}"
[[ "$release_base_url" =~ ^https://[^[:space:]?#]+$ ]] \
  || die "BUILDX_RELEASE_BASE_URL must be an HTTPS URL without a query or fragment"

docker_config_directory="$1"
download_path="$(mktemp)"
trap 'rm -f "$download_path"' EXIT
curl --proto '=https' --proto-redir '=https' --tlsv1.2 --fail --location --silent --show-error \
  --output "$download_path" \
  "${release_base_url%/}/buildx-${BUILDX_VERSION}.linux-${architecture}"
printf '%s  %s\n' "$expected_sha256" "$download_path" | sha256sum --check --status \
  || die "downloaded Buildx checksum verification failed"

plugin_directory="$docker_config_directory/cli-plugins"
mkdir -p "$plugin_directory"
install -m 0755 "$download_path" "$plugin_directory/docker-buildx"
version_output="$("$plugin_directory/docker-buildx" version 2>&1)" \
  || die "installed Buildx could not be executed"
grep -Eq "^github\.com/docker/buildx ${BUILDX_VERSION}([[:space:]]|$)" <<< "$version_output" \
  || die "installed Buildx version did not match ${BUILDX_VERSION}"

#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RAW_VERSION="${VERSION:-}"
COMMIT="${COMMIT:-$(git -C "$ROOT_DIR" rev-parse HEAD 2>/dev/null || echo unknown)}"
BUILD_TIME="${BUILD_TIME:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
VERSION="$(VERSION="$RAW_VERSION" COMMIT="$COMMIT" BUILD_TIME="$BUILD_TIME" "$ROOT_DIR/scripts/resolve-version.sh")"
BI="github.com/jasonczech/apexload/pkg/buildinfo"
DIST_DIR="${DIST_DIR:-$ROOT_DIR/dist/recording-agent}"
PLATFORMS="${PLATFORMS:-darwin/arm64 darwin/amd64 windows/amd64 linux/amd64 linux/arm64}"
MACOS_CODESIGN_IDENTITY="${APEXLOAD_MACOS_CODESIGN_IDENTITY:-${MACOS_CODESIGN_IDENTITY:-}}"
MACOS_NOTARIZE="${APEXLOAD_MACOS_NOTARIZE:-${MACOS_NOTARIZE:-0}}"
MACOS_NOTARY_PROFILE="${APEXLOAD_MACOS_NOTARY_PROFILE:-${MACOS_NOTARY_PROFILE:-}}"
WINDOWS_CODESIGN="${APEXLOAD_WINDOWS_CODESIGN:-${WINDOWS_CODESIGN:-0}}"
WINDOWS_CODESIGN_TOOL="${APEXLOAD_WINDOWS_CODESIGN_TOOL:-${WINDOWS_CODESIGN_TOOL:-auto}}"
WINDOWS_SIGNTOOL="${APEXLOAD_WINDOWS_SIGNTOOL:-${WINDOWS_SIGNTOOL:-signtool}}"
WINDOWS_CODESIGN_CERT_PATH="${APEXLOAD_WINDOWS_CODESIGN_CERT_PATH:-${WINDOWS_CODESIGN_CERT_PATH:-}}"
WINDOWS_CODESIGN_CERT_PEM="${APEXLOAD_WINDOWS_CODESIGN_CERT_PEM:-${WINDOWS_CODESIGN_CERT_PEM:-}}"
WINDOWS_CODESIGN_KEY_PATH="${APEXLOAD_WINDOWS_CODESIGN_KEY_PATH:-${WINDOWS_CODESIGN_KEY_PATH:-}}"
WINDOWS_CODESIGN_PASSWORD="${APEXLOAD_WINDOWS_CODESIGN_PASSWORD:-${WINDOWS_CODESIGN_PASSWORD:-}}"
WINDOWS_CODESIGN_SHA1="${APEXLOAD_WINDOWS_CODESIGN_SHA1:-${WINDOWS_CODESIGN_SHA1:-}}"
WINDOWS_CODESIGN_SUBJECT="${APEXLOAD_WINDOWS_CODESIGN_SUBJECT:-${WINDOWS_CODESIGN_SUBJECT:-}}"
WINDOWS_CODESIGN_TIMESTAMP_URL="${APEXLOAD_WINDOWS_CODESIGN_TIMESTAMP_URL:-${WINDOWS_CODESIGN_TIMESTAMP_URL:-http://timestamp.digicert.com}}"
WINDOWS_CODESIGN_DIGEST="${APEXLOAD_WINDOWS_CODESIGN_DIGEST:-${WINDOWS_CODESIGN_DIGEST:-sha256}}"
CHECKSUM_SIGN="${APEXLOAD_CHECKSUM_SIGN:-${CHECKSUM_SIGN:-0}}"
CHECKSUM_SIGN_KEY="${APEXLOAD_CHECKSUM_SIGN_KEY:-${CHECKSUM_SIGN_KEY:-}}"
REQUIRE_TRUSTED_PACKAGES="${APEXLOAD_REQUIRE_TRUSTED_PACKAGES:-${REQUIRE_TRUSTED_PACKAGES:-0}}"

truthy() {
  case "$(printf '%s' "${1:-}" | tr '[:upper:]' '[:lower:]')" in
    1|true|yes|y|on) return 0 ;;
    *) return 1 ;;
  esac
}

macos_notarization_requested() {
  truthy "$MACOS_NOTARIZE"
}

windows_codesign_configured() {
  [[ -n "$WINDOWS_CODESIGN_CERT_PATH" || -n "$WINDOWS_CODESIGN_CERT_PEM" || -n "$WINDOWS_CODESIGN_KEY_PATH" || -n "$WINDOWS_CODESIGN_SHA1" || -n "$WINDOWS_CODESIGN_SUBJECT" ]]
}

windows_codesign_requested() {
  truthy "$WINDOWS_CODESIGN" || windows_codesign_configured
}

checksum_signing_requested() {
  truthy "$CHECKSUM_SIGN" || [[ -n "$CHECKSUM_SIGN_KEY" ]]
}

trusted_packages_required() {
  truthy "$REQUIRE_TRUSTED_PACKAGES"
}

write_sha256_manifest() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$@"
  elif command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$@"
  else
    echo "shasum or sha256sum is required to write SHA256SUMS.txt." >&2
    exit 1
  fi
}

sign_macos_binary() {
  local binary_path="$1"
  if [[ -z "$MACOS_CODESIGN_IDENTITY" ]]; then
    if macos_notarization_requested || trusted_packages_required; then
      echo "macOS release signing is required, but APEXLOAD_MACOS_CODESIGN_IDENTITY is not set." >&2
      exit 1
    fi
    echo "macOS Developer ID signing is not configured; $binary_path will trigger Gatekeeper on downloaded Macs." >&2
    return 0
  fi
  if ! command -v codesign >/dev/null 2>&1; then
    echo "codesign is required for macOS release signing but was not found." >&2
    exit 1
  fi
  codesign --force --options runtime --timestamp --sign "$MACOS_CODESIGN_IDENTITY" "$binary_path"
  codesign --verify --strict --verbose=2 "$binary_path"
}

notarize_macos_package() {
  local package="$1"
  local zip_path="$DIST_DIR/${package}.notary.zip"
  if ! macos_notarization_requested; then
    if trusted_packages_required; then
      echo "APEXLOAD_REQUIRE_TRUSTED_PACKAGES requires APEXLOAD_MACOS_NOTARIZE=1 for macOS Recording Agent packages." >&2
      exit 1
    fi
    return 0
  fi
  if ! command -v xcrun >/dev/null 2>&1; then
    echo "xcrun notarytool is required for macOS notarization but was not found." >&2
    exit 1
  fi
  (
    cd "$DIST_DIR"
    rm -f "$(basename "$zip_path")"
    if command -v ditto >/dev/null 2>&1; then
      ditto -c -k --keepParent "$package" "$(basename "$zip_path")"
    elif command -v zip >/dev/null 2>&1; then
      zip -qry "$(basename "$zip_path")" "$package"
    else
      echo "ditto or zip is required to create a notarization upload archive." >&2
      exit 1
    fi
  )

  local submit_args=(notarytool submit "$zip_path" --wait)
  if [[ -n "$MACOS_NOTARY_PROFILE" ]]; then
    submit_args+=(--keychain-profile "$MACOS_NOTARY_PROFILE")
  else
    echo "Recording Agent notarization requires APEXLOAD_MACOS_NOTARY_PROFILE; direct Apple passwords are not accepted because notarytool would expose them in process arguments." >&2
    exit 1
  fi
  xcrun "${submit_args[@]}"
  rm -f "$zip_path"
}

sign_windows_with_signtool() {
  local binary_path="$1"
  local sign_tool="${WINDOWS_SIGNTOOL:-signtool}"
  if ! command -v "$sign_tool" >/dev/null 2>&1; then
    echo "signtool is required for Windows Authenticode signing but was not found." >&2
    exit 1
  fi

  local sign_args=(sign /fd "$WINDOWS_CODESIGN_DIGEST" /td "$WINDOWS_CODESIGN_DIGEST")
  if [[ -n "$WINDOWS_CODESIGN_TIMESTAMP_URL" ]]; then
    sign_args+=(/tr "$WINDOWS_CODESIGN_TIMESTAMP_URL")
  fi
  if [[ -n "$WINDOWS_CODESIGN_CERT_PATH" ]]; then
    echo "signtool PFX/P12 signing is disabled because its password would be exposed in process arguments. Import the certificate into the CurrentUser certificate store and set APEXLOAD_WINDOWS_CODESIGN_SHA1." >&2
    exit 1
  elif [[ -n "$WINDOWS_CODESIGN_SHA1" ]]; then
    sign_args+=(/s My /sha1 "$WINDOWS_CODESIGN_SHA1")
  elif [[ -n "$WINDOWS_CODESIGN_SUBJECT" ]]; then
    sign_args+=(/n "$WINDOWS_CODESIGN_SUBJECT")
  else
    echo "signtool signing requires APEXLOAD_WINDOWS_CODESIGN_SHA1 or APEXLOAD_WINDOWS_CODESIGN_SUBJECT; automatic certificate selection is not permitted." >&2
    exit 1
  fi

  "$sign_tool" "${sign_args[@]}" "$binary_path"
  "$sign_tool" verify /pa /all "$binary_path"
}

sign_windows_with_osslsigncode() {
  local binary_path="$1"
  if ! command -v osslsigncode >/dev/null 2>&1; then
    echo "osslsigncode is required for cross-platform Windows Authenticode signing but was not found." >&2
    exit 1
  fi
  if [[ -z "$WINDOWS_CODESIGN_CERT_PATH" && ( -z "$WINDOWS_CODESIGN_CERT_PEM" || -z "$WINDOWS_CODESIGN_KEY_PATH" ) ]]; then
    echo "Set APEXLOAD_WINDOWS_CODESIGN_CERT_PATH for a PFX/P12 file or APEXLOAD_WINDOWS_CODESIGN_CERT_PEM plus APEXLOAD_WINDOWS_CODESIGN_KEY_PATH for osslsigncode." >&2
    exit 1
  fi

  local signed_path="${binary_path}.signed"
  local sign_args=(sign -h "$WINDOWS_CODESIGN_DIGEST" -in "$binary_path" -out "$signed_path")
  if [[ -n "$WINDOWS_CODESIGN_TIMESTAMP_URL" ]]; then
    sign_args+=(-t "$WINDOWS_CODESIGN_TIMESTAMP_URL")
  fi
  if [[ -n "$WINDOWS_CODESIGN_CERT_PATH" ]]; then
    sign_args+=(-pkcs12 "$WINDOWS_CODESIGN_CERT_PATH")
  else
    sign_args+=(-certs "$WINDOWS_CODESIGN_CERT_PEM" -key "$WINDOWS_CODESIGN_KEY_PATH")
  fi
  [[ -z "$WINDOWS_CODESIGN_PASSWORD" ]] || {
    echo "osslsigncode password arguments are disabled because they expose credentials in the process list. Use an unencrypted protected key file or a certificate-store signing lane." >&2
    exit 1
  }

  rm -f "$signed_path"
  osslsigncode "${sign_args[@]}"
  mv "$signed_path" "$binary_path"
  osslsigncode verify "$binary_path"
}

sign_windows_binary() {
  local binary_path="$1"
  if ! windows_codesign_requested; then
    if trusted_packages_required; then
      echo "APEXLOAD_REQUIRE_TRUSTED_PACKAGES requires Windows Authenticode signing for $binary_path." >&2
      exit 1
    fi
    echo "Windows Authenticode signing is not configured; $binary_path will be unsigned and may trigger SmartScreen or enterprise application-control warnings." >&2
    return 0
  fi

  case "$WINDOWS_CODESIGN_TOOL" in
    auto)
      if [[ -n "$WINDOWS_CODESIGN_SHA1" || -n "$WINDOWS_CODESIGN_SUBJECT" ]]; then
        sign_windows_with_signtool "$binary_path"
      elif [[ -n "$WINDOWS_CODESIGN_CERT_PEM" || -n "$WINDOWS_CODESIGN_KEY_PATH" || -n "$WINDOWS_CODESIGN_CERT_PATH" ]]; then
        sign_windows_with_osslsigncode "$binary_path"
      else
        echo "APEXLOAD_WINDOWS_CODESIGN is enabled, but no explicit certificate thumbprint, subject, or osslsigncode key material was configured." >&2
        exit 1
      fi
      ;;
    signtool)
      sign_windows_with_signtool "$binary_path"
      ;;
    osslsigncode)
      sign_windows_with_osslsigncode "$binary_path"
      ;;
    *)
      echo "Unsupported APEXLOAD_WINDOWS_CODESIGN_TOOL '$WINDOWS_CODESIGN_TOOL'. Use auto, signtool, or osslsigncode." >&2
      exit 1
      ;;
  esac
}

sign_checksum_manifest() {
  local manifest_path="$1"
  if ! checksum_signing_requested; then
    if trusted_packages_required; then
      echo "APEXLOAD_REQUIRE_TRUSTED_PACKAGES requires APEXLOAD_CHECKSUM_SIGN=1 or APEXLOAD_CHECKSUM_SIGN_KEY for $manifest_path." >&2
      exit 1
    fi
    return 0
  fi
  if ! command -v gpg >/dev/null 2>&1; then
    echo "gpg is required for APEXLOAD_CHECKSUM_SIGN but was not found." >&2
    exit 1
  fi
  local gpg_args=(--batch --yes --armor --detach-sign)
  if [[ -n "$CHECKSUM_SIGN_KEY" ]]; then
    gpg_args+=(--local-user "$CHECKSUM_SIGN_KEY")
  fi
  gpg "${gpg_args[@]}" "$manifest_path"
}

# Clear the dist dir's *contents* rather than the directory itself. A bind mount
# (e.g. docker compose's ./dist/recording-agent:/packages/recording-agent) pins to
# the directory inode at container-start; rm -rf + mkdir would orphan that mount and
# leave the container seeing an empty dir until it is recreated.
mkdir -p "$DIST_DIR"
find "$DIST_DIR" -mindepth 1 -delete

for platform in $PLATFORMS; do
  GOOS="${platform%/*}"
  GOARCH="${platform#*/}"
  package="apexload-recording-agent-${VERSION}-${GOOS}-${GOARCH}"
  workdir="$DIST_DIR/$package"
  binary="apexload-record-agent"
  if [[ "$GOOS" == "windows" ]]; then
    binary="${binary}.exe"
  fi

  mkdir -p "$workdir"
  echo "Building $package"
  (
    cd "$ROOT_DIR"
    GOOS="$GOOS" GOARCH="$GOARCH" go build -trimpath \
      -ldflags "-s -w -X ${BI}.Version=${VERSION} -X ${BI}.Commit=${COMMIT} -X ${BI}.BuildTime=${BUILD_TIME}" \
      -o "$workdir/$binary" ./cmd/apexload-record-agent
  )

  if [[ "$GOOS" == "darwin" ]]; then
    sign_macos_binary "$workdir/$binary"
  elif [[ "$GOOS" == "windows" ]]; then
    sign_windows_binary "$workdir/$binary"
  fi

  cat > "$workdir/README.txt" <<EOF
APEX Load Recording Agent ${VERSION}

This package contains the local browser recording bridge used by the APEX Load
dashboard when the controller is remote or cannot launch a headed browser.

Run the exact command generated by the Recording Studio dashboard. It will look
similar to:

  ./${binary} --addr 127.0.0.1:27777 --token <dashboard-token> --allow-origin <dashboard-origin>

The token is already included in the dashboard command. Do not paste it anywhere
separately.

Optional browser dependency preflight:

  ./${binary} install-browsers

The agent binds to 127.0.0.1 only. It does not require admin privileges to run,
but enterprise endpoint controls may require this binary and its browser payload
to be approved by IT.

macOS Gatekeeper:

Production macOS packages must be built with a Developer ID Application
certificate and Apple notarization before distribution. Local development
packages built without APEXLOAD_MACOS_CODESIGN_IDENTITY are not notarized; macOS
may show "Apple could not verify '${binary}' is free of malware." For local
development only, after verifying this package came from your own controller and
the controller checksum endpoint or release SHA256SUMS.txt entry matches, clear
the quarantine attribute:

  xattr -dr com.apple.quarantine ./${binary}

Windows SmartScreen:

Production Windows packages should be Authenticode-signed and timestamped in CI.
Local development packages built without APEXLOAD_WINDOWS_CODESIGN are unsigned;
Windows SmartScreen or enterprise application-control policy may warn before
running '${binary}'.
EOF

  if [[ "$GOOS" == "darwin" ]]; then
    notarize_macos_package "$package"
  fi

  (
    cd "$DIST_DIR"
    tar -czf "${package}.tar.gz" "$package"
    rm -rf "$package"
  )
done

(
  cd "$DIST_DIR"
  write_sha256_manifest ./*.tar.gz > SHA256SUMS.txt
  sign_checksum_manifest "$DIST_DIR/SHA256SUMS.txt"
)

echo "Recording agent packages written to $DIST_DIR"

#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RAW_VERSION="${VERSION:-}"
COMMIT="${COMMIT:-$(git -C "$ROOT_DIR" rev-parse HEAD 2>/dev/null || echo unknown)}"
BUILD_TIME="${BUILD_TIME:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
VERSION="$(VERSION="$RAW_VERSION" COMMIT="$COMMIT" BUILD_TIME="$BUILD_TIME" "$ROOT_DIR/scripts/resolve-version.sh")"
DIST_DIR="${DIST_DIR:-$ROOT_DIR/dist/desktop}"
SOURCE_DIR="$ROOT_DIR/desktop/build/bin"
DESKTOP_PACKAGE_PLATFORM="${APEXLOAD_DESKTOP_PACKAGE_PLATFORM:-${DESKTOP_PACKAGE_PLATFORM:-}}"
if [[ -n "$DESKTOP_PACKAGE_PLATFORM" ]]; then
  if [[ "$DESKTOP_PACKAGE_PLATFORM" != */* ]]; then
    echo "APEXLOAD_DESKTOP_PACKAGE_PLATFORM must use GOOS/GOARCH format, for example windows/amd64." >&2
    exit 1
  fi
  GOOS="${DESKTOP_PACKAGE_PLATFORM%/*}"
  GOARCH="${DESKTOP_PACKAGE_PLATFORM#*/}"
else
  GOOS="${APEXLOAD_DESKTOP_PACKAGE_GOOS:-${GOOS:-$(go env GOOS)}}"
  GOARCH="${APEXLOAD_DESKTOP_PACKAGE_GOARCH:-${GOARCH:-$(go env GOARCH)}}"
fi
PACKAGE="apexload-desktop-${VERSION}-${GOOS}-${GOARCH}"
WORKDIR="$DIST_DIR/$PACKAGE"
MACOS_CODESIGN_IDENTITY="${APEXLOAD_MACOS_CODESIGN_IDENTITY:-${MACOS_CODESIGN_IDENTITY:-}}"
MACOS_NOTARIZE="${APEXLOAD_MACOS_NOTARIZE:-${MACOS_NOTARIZE:-0}}"
MACOS_NOTARY_PROFILE="${APEXLOAD_MACOS_NOTARY_PROFILE:-${MACOS_NOTARY_PROFILE:-}}"
MACOS_NOTARY_APPLE_ID="${APEXLOAD_MACOS_NOTARY_APPLE_ID:-${MACOS_NOTARY_APPLE_ID:-}}"
MACOS_NOTARY_PASSWORD="${APEXLOAD_MACOS_NOTARY_PASSWORD:-${MACOS_NOTARY_PASSWORD:-}}"
MACOS_NOTARY_TEAM_ID="${APEXLOAD_MACOS_NOTARY_TEAM_ID:-${MACOS_NOTARY_TEAM_ID:-}}"
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

clean_desktop_dist() {
  mkdir -p "$DIST_DIR"
  # Remove only the CURRENT platform's artifacts (staging dir, tarball,
  # notary.zip) so a rebuild of this platform starts clean while previously
  # built platforms' tarballs survive in the shared DIST_DIR — they are meant to
  # accumulate and be co-signed in one multi-platform SHA256SUMS.txt (see the
  # apexload-desktop-*.tar.gz glob in the manifest step). The SHA256SUMS files
  # are regenerated over the surviving tarballs later in the run.
  find "$DIST_DIR" -mindepth 1 -name "${PACKAGE}*" -exec rm -rf {} +
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

sign_macos_apps() {
  if [[ "$GOOS" != "darwin" ]]; then
    return 0
  fi
  shopt -s nullglob
  local apps=("$WORKDIR"/*.app)
  shopt -u nullglob
  if [[ ${#apps[@]} -eq 0 ]]; then
    echo "No macOS .app bundle found in $WORKDIR" >&2
    exit 1
  fi
  if [[ -z "$MACOS_CODESIGN_IDENTITY" ]]; then
    if macos_notarization_requested || trusted_packages_required; then
      echo "macOS release signing is required, but APEXLOAD_MACOS_CODESIGN_IDENTITY is not set." >&2
      exit 1
    fi
    echo "macOS Developer ID signing is not configured; desktop .app downloads will trigger Gatekeeper on other Macs." >&2
    return 0
  fi
  if ! command -v codesign >/dev/null 2>&1; then
    echo "codesign is required for macOS release signing but was not found." >&2
    exit 1
  fi
  for app in "${apps[@]}"; do
    codesign --force --deep --options runtime --timestamp --sign "$MACOS_CODESIGN_IDENTITY" "$app"
    codesign --verify --deep --strict --verbose=2 "$app"
  done
}

notarize_macos_package() {
  if [[ "$GOOS" != "darwin" ]] || ! macos_notarization_requested; then
    if [[ "$GOOS" == "darwin" ]] && trusted_packages_required; then
      echo "APEXLOAD_REQUIRE_TRUSTED_PACKAGES requires APEXLOAD_MACOS_NOTARIZE=1 for macOS desktop packages." >&2
      exit 1
    fi
    return 0
  fi
  if ! command -v xcrun >/dev/null 2>&1; then
    echo "xcrun notarytool is required for macOS notarization but was not found." >&2
    exit 1
  fi
  local zip_path="$DIST_DIR/${PACKAGE}.notary.zip"
  (
    cd "$DIST_DIR"
    rm -f "$(basename "$zip_path")"
    if command -v ditto >/dev/null 2>&1; then
      ditto -c -k --keepParent "$PACKAGE" "$(basename "$zip_path")"
    elif command -v zip >/dev/null 2>&1; then
      zip -qry "$(basename "$zip_path")" "$PACKAGE"
    else
      echo "ditto or zip is required to create a notarization upload archive." >&2
      exit 1
    fi
  )

  local submit_args=(notarytool submit "$zip_path" --wait)
  if [[ -n "$MACOS_NOTARY_PROFILE" ]]; then
    submit_args+=(--keychain-profile "$MACOS_NOTARY_PROFILE")
  else
    if [[ -z "$MACOS_NOTARY_APPLE_ID" || -z "$MACOS_NOTARY_PASSWORD" || -z "$MACOS_NOTARY_TEAM_ID" ]]; then
      echo "Set APEXLOAD_MACOS_NOTARY_PROFILE or APEXLOAD_MACOS_NOTARY_APPLE_ID/APEXLOAD_MACOS_NOTARY_PASSWORD/APEXLOAD_MACOS_NOTARY_TEAM_ID for notarization." >&2
      exit 1
    fi
    submit_args+=(--apple-id "$MACOS_NOTARY_APPLE_ID" --password "$MACOS_NOTARY_PASSWORD" --team-id "$MACOS_NOTARY_TEAM_ID")
  fi
  xcrun "${submit_args[@]}"

  shopt -s nullglob
  local apps=("$WORKDIR"/*.app)
  shopt -u nullglob
  for app in "${apps[@]}"; do
    xcrun stapler staple "$app"
    spctl -a -vvv -t exec "$app"
  done
  rm -f "$zip_path"
}

detect_windows_exe_arch() {
  local binary_path="$1"
  if ! command -v file >/dev/null 2>&1; then
    return 0
  fi
  local description
  description="$(file -b "$binary_path" 2>/dev/null || true)"
  case "$description" in
    *x86-64*) printf 'amd64' ;;
    *ARM64*|*aarch64*) printf 'arm64' ;;
    *80386*|*i386*) printf '386' ;;
  esac
}

validate_desktop_artifacts() {
  case "$GOOS" in
    darwin)
      shopt -s nullglob
      local apps=("$WORKDIR"/*.app)
      shopt -u nullglob
      if [[ ${#apps[@]} -eq 0 ]]; then
        echo "Desktop package platform is darwin/$GOARCH, but no .app bundle was found in $WORKDIR." >&2
        exit 1
      fi
      ;;
    windows)
      local exes=()
      while IFS= read -r -d '' exe; do
        exes+=("$exe")
      done < <(find "$WORKDIR" -type f -name '*.exe' -print0)
      if [[ ${#exes[@]} -eq 0 ]]; then
        echo "Desktop package platform is windows/$GOARCH, but no .exe was found in $WORKDIR." >&2
        exit 1
      fi
      for exe in "${exes[@]}"; do
        local actual_arch
        actual_arch="$(detect_windows_exe_arch "$exe")"
        if [[ -n "$actual_arch" && "$actual_arch" != "$GOARCH" ]]; then
          echo "Desktop package platform is windows/$GOARCH, but $exe appears to be windows/$actual_arch. Set WAILS_PLATFORM or APEXLOAD_DESKTOP_PACKAGE_PLATFORM to match the Wails build output." >&2
          exit 1
        fi
      done
      ;;
  esac
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
    sign_args+=(/f "$WINDOWS_CODESIGN_CERT_PATH")
    if [[ -n "$WINDOWS_CODESIGN_PASSWORD" ]]; then
      sign_args+=(/p "$WINDOWS_CODESIGN_PASSWORD")
    fi
  elif [[ -n "$WINDOWS_CODESIGN_SHA1" ]]; then
    sign_args+=(/sha1 "$WINDOWS_CODESIGN_SHA1")
  elif [[ -n "$WINDOWS_CODESIGN_SUBJECT" ]]; then
    sign_args+=(/n "$WINDOWS_CODESIGN_SUBJECT")
  else
    sign_args+=(/a)
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
  if [[ -n "$WINDOWS_CODESIGN_PASSWORD" ]]; then
    sign_args+=(-pass "$WINDOWS_CODESIGN_PASSWORD")
  fi

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
      if [[ -n "$WINDOWS_CODESIGN_CERT_PEM" || -n "$WINDOWS_CODESIGN_KEY_PATH" ]]; then
        sign_windows_with_osslsigncode "$binary_path"
      elif command -v signtool >/dev/null 2>&1; then
        sign_windows_with_signtool "$binary_path"
      elif command -v osslsigncode >/dev/null 2>&1; then
        sign_windows_with_osslsigncode "$binary_path"
      else
        echo "APEXLOAD_WINDOWS_CODESIGN is enabled, but neither signtool nor osslsigncode was found." >&2
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

sign_windows_binaries() {
  if [[ "$GOOS" != "windows" ]]; then
    return 0
  fi
  local exes=()
  while IFS= read -r -d '' exe; do
    exes+=("$exe")
  done < <(find "$WORKDIR" -type f -name '*.exe' -print0)
  if [[ ${#exes[@]} -eq 0 ]]; then
    echo "No Windows .exe found in $WORKDIR" >&2
    exit 1
  fi
  for exe in "${exes[@]}"; do
    sign_windows_binary "$exe"
  done
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

clean_desktop_dist
mkdir -p "$WORKDIR"

if [[ ! -d "$SOURCE_DIR" ]]; then
  echo "Desktop build output not found at $SOURCE_DIR. Run: make desktop" >&2
  exit 1
fi

shopt -s nullglob
artifacts=("$SOURCE_DIR"/*)
if [[ ${#artifacts[@]} -eq 0 ]]; then
  echo "Desktop build output is empty at $SOURCE_DIR. Run: make desktop" >&2
  exit 1
fi

cp -R "${artifacts[@]}" "$WORKDIR/"

validate_desktop_artifacts
sign_windows_binaries
sign_macos_apps
notarize_macos_package

cat > "$WORKDIR/README.txt" <<EOF
APEX Load Desktop ${VERSION}

This package contains the optional Wails desktop workspace. The desktop app runs
an embedded local controller for local authoring and runs, and it can connect to
Remote Workspaces for sync and remote execution.

macOS Gatekeeper:

Production macOS desktop packages must be built with a Developer ID Application
certificate and Apple notarization before distribution. Local development
packages built without APEXLOAD_MACOS_CODESIGN_IDENTITY are ad-hoc signed only;
macOS may show "Apple could not verify 'apexload.app' is free of malware." For
local development only, after verifying this archive came from your own build
and the SHA256SUMS.txt entry matches, clear the quarantine attribute:

  xattr -dr com.apple.quarantine ./apexload.app

Release packaging needs user-supplied macOS Developer ID/notarytool credentials
and Windows Authenticode certificate material in CI.

Windows SmartScreen:

Production Windows desktop packages should be Authenticode-signed and
timestamped in CI. Local development packages built without
APEXLOAD_WINDOWS_CODESIGN are unsigned; Windows SmartScreen or enterprise
application-control policy may warn before running the desktop executable.
EOF

(
  cd "$DIST_DIR"
  tar -czf "${PACKAGE}.tar.gz" "$PACKAGE"
  rm -rf "$PACKAGE"
  write_sha256_manifest apexload-desktop-*.tar.gz > SHA256SUMS.txt
  sign_checksum_manifest "$DIST_DIR/SHA256SUMS.txt"
)

echo "Desktop package written to $DIST_DIR/${PACKAGE}.tar.gz"

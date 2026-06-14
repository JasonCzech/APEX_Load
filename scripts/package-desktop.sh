#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RAW_VERSION="${VERSION:-}"
COMMIT="${COMMIT:-$(git -C "$ROOT_DIR" rev-parse HEAD 2>/dev/null || echo unknown)}"
BUILD_TIME="${BUILD_TIME:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
VERSION="$(VERSION="$RAW_VERSION" COMMIT="$COMMIT" BUILD_TIME="$BUILD_TIME" "$ROOT_DIR/scripts/resolve-version.sh")"
DIST_DIR="${DIST_DIR:-$ROOT_DIR/dist/desktop}"
SOURCE_DIR="$ROOT_DIR/desktop/build/bin"
GOOS="$(go env GOOS)"
GOARCH="$(go env GOARCH)"
PACKAGE="apexload-desktop-${VERSION}-${GOOS}-${GOARCH}"
WORKDIR="$DIST_DIR/$PACKAGE"
MACOS_CODESIGN_IDENTITY="${APEXLOAD_MACOS_CODESIGN_IDENTITY:-${MACOS_CODESIGN_IDENTITY:-}}"
MACOS_NOTARIZE="${APEXLOAD_MACOS_NOTARIZE:-${MACOS_NOTARIZE:-0}}"
MACOS_NOTARY_PROFILE="${APEXLOAD_MACOS_NOTARY_PROFILE:-${MACOS_NOTARY_PROFILE:-}}"
MACOS_NOTARY_APPLE_ID="${APEXLOAD_MACOS_NOTARY_APPLE_ID:-${MACOS_NOTARY_APPLE_ID:-}}"
MACOS_NOTARY_PASSWORD="${APEXLOAD_MACOS_NOTARY_PASSWORD:-${MACOS_NOTARY_PASSWORD:-}}"
MACOS_NOTARY_TEAM_ID="${APEXLOAD_MACOS_NOTARY_TEAM_ID:-${MACOS_NOTARY_TEAM_ID:-}}"

truthy() {
  case "$(printf '%s' "${1:-}" | tr '[:upper:]' '[:lower:]')" in
    1|true|yes|y|on) return 0 ;;
    *) return 1 ;;
  esac
}

macos_notarization_requested() {
  truthy "$MACOS_NOTARIZE"
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
    if macos_notarization_requested; then
      echo "APEXLOAD_MACOS_NOTARIZE is enabled, but APEXLOAD_MACOS_CODESIGN_IDENTITY is not set." >&2
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
    spctl -a -vvv -t open "$app"
  done
  rm -f "$zip_path"
}

rm -rf "$WORKDIR"
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
and Windows signtool certificate material in CI.
EOF

mkdir -p "$DIST_DIR"
(
  cd "$DIST_DIR"
  tar -czf "${PACKAGE}.tar.gz" "$PACKAGE"
  rm -rf "$PACKAGE"
  shasum -a 256 "${PACKAGE}.tar.gz" > SHA256SUMS.txt
)

echo "Desktop package written to $DIST_DIR/${PACKAGE}.tar.gz"

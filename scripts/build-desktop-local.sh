#!/usr/bin/env bash
# Build the HOST-platform Wails desktop app, with a toolchain preflight.
#
# IMPORTANT: Wails desktop apps CANNOT be cross-compiled to other operating
# systems from a single machine — Wails links native webview libraries
# (WebKitGTK on Linux, Cocoa/WebKit on macOS, WebView2 on Windows) and does
# OS-specific bundling. This script builds ONLY the current host's desktop app.
# For all-platform signed packages, use the CI matrix in
# .github/workflows/desktop.yml.
#
# Usage:
#   scripts/build-desktop-local.sh            # preflight + build host desktop app
#   scripts/build-desktop-local.sh --check    # preflight only (no build)
#   scripts/build-desktop-local.sh --install-wails  # go install the wails CLI if missing
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Keep in sync with .github/workflows/desktop.yml.
WAILS_VERSION="${APEXLOAD_WAILS_VERSION:-v2.12.0}"
BUILDINFO_PKG="github.com/jasonczech/apexload/pkg/buildinfo"

CHECK_ONLY=0
INSTALL_WAILS=0
for arg in "$@"; do
  case "$arg" in
    --check) CHECK_ONLY=1 ;;
    --install-wails) INSTALL_WAILS=1 ;;
    *) echo "unknown argument: $arg" >&2; exit 2 ;;
  esac
done

log() { printf '%s\n' "$*"; }
err() { printf 'error: %s\n' "$*" >&2; }

wails_bin() {
  if command -v wails >/dev/null 2>&1; then
    command -v wails
  elif [[ -x "${HOME}/go/bin/wails" ]]; then
    printf '%s\n' "${HOME}/go/bin/wails"
  fi
}

missing=0

# Wails CLI
WAILS="$(wails_bin || true)"
if [[ -z "${WAILS}" ]]; then
  if [[ "${INSTALL_WAILS}" -eq 1 ]]; then
    log "Installing Wails CLI ${WAILS_VERSION}..."
    go install "github.com/wailsapp/wails/v2/cmd/wails@${WAILS_VERSION}"
    WAILS="$(wails_bin || true)"
  fi
fi
if [[ -z "${WAILS}" ]]; then
  err "wails CLI not found. Install it with:"
  err "    go install github.com/wailsapp/wails/v2/cmd/wails@${WAILS_VERSION}"
  err "  (or re-run with --install-wails)"
  missing=1
else
  # Warn (don't fail) if the installed CLI drifts from the pinned version that CI
  # uses, so local builds stay reproducible.
  installed_wails="$("${WAILS}" version 2>/dev/null | tr -d '[:space:]')"
  if [[ -n "${installed_wails}" && "${installed_wails}" != *"${WAILS_VERSION#v}"* ]]; then
    log "warning: installed wails CLI (${installed_wails}) differs from the pinned ${WAILS_VERSION} used by CI."
  fi
fi

# Node / npm (the dashboard frontend build)
if ! command -v node >/dev/null 2>&1; then
  err "node not found. Install Node.js 18+ (the dashboard frontend build needs it)."
  missing=1
fi
if ! command -v npm >/dev/null 2>&1; then
  err "npm not found. Install Node.js 18+ (includes npm)."
  missing=1
fi

# Linux native webview headers. Newer Debian/Ubuntu ship webkit2gtk-4.1 instead
# of 4.0 (Wails builds against either), so accept whichever is present.
if [[ "$(uname -s)" == "Linux" ]]; then
  if command -v pkg-config >/dev/null 2>&1; then
    if ! pkg-config --exists gtk+-3.0 2>/dev/null; then
      err "Linux desktop build needs the GTK3 dev headers. Install with:"
      err "    sudo apt-get install -y libgtk-3-dev"
      missing=1
    fi
    if ! pkg-config --exists webkit2gtk-4.0 2>/dev/null && ! pkg-config --exists webkit2gtk-4.1 2>/dev/null; then
      err "Linux desktop build needs the WebKit2GTK dev headers (4.0 or 4.1). Install with:"
      err "    sudo apt-get install -y libwebkit2gtk-4.1-dev   # or libwebkit2gtk-4.0-dev on older releases"
      missing=1
    fi
  else
    err "pkg-config not found; cannot verify gtk+-3.0 / webkit2gtk dev headers."
    err "    sudo apt-get install -y pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev"
    missing=1
  fi
fi

if [[ "${missing}" -ne 0 ]]; then
  err "desktop toolchain preflight failed (see above)."
  exit 1
fi

log "Desktop toolchain OK (host: $(go env GOOS)/$(go env GOARCH))."
log "Note: this builds the host platform's desktop app only; Wails cannot cross-compile."

if [[ "${CHECK_ONLY}" -eq 1 ]]; then
  exit 0
fi

VERSION="${VERSION:-$("${ROOT_DIR}/scripts/resolve-version.sh" 2>/dev/null || echo 0.1.0)}"
COMMIT="${COMMIT:-$(git -C "${ROOT_DIR}" rev-parse HEAD 2>/dev/null || echo unknown)}"
BUILD_TIME="${BUILD_TIME:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"

log "Building desktop app ${VERSION} (${COMMIT})..."
cd "${ROOT_DIR}/desktop"
COPYFILE_DISABLE=1 "${WAILS}" build -clean \
  -ldflags "-s -w -X ${BUILDINFO_PKG}.Version=${VERSION} -X ${BUILDINFO_PKG}.Commit=${COMMIT} -X ${BUILDINFO_PKG}.BuildTime=${BUILD_TIME}"
log "Desktop app built in desktop/build/bin/."

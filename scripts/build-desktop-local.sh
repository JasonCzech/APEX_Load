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

WAILS_VERSION_FILE="${ROOT_DIR}/desktop/.wails-version"
if [[ ! -f "${WAILS_VERSION_FILE}" ]]; then
  echo "error: missing ${WAILS_VERSION_FILE}" >&2
  exit 1
fi
WAILS_VERSION="$(tr -d '[:space:]' < "${WAILS_VERSION_FILE}")"
if [[ ! "${WAILS_VERSION}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "error: invalid pinned Wails version '${WAILS_VERSION}'" >&2
  exit 1
fi
NSIS_VERSION="3.10"
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

TARGET_PLATFORM="${WAILS_PLATFORM:-$(go env GOOS)/$(go env GOARCH)}"
if [[ ! "$TARGET_PLATFORM" =~ ^[^/]+/[^/]+$ ]]; then
  echo "error: WAILS_PLATFORM must use GOOS/GOARCH format" >&2
  exit 1
fi
TARGET_GOOS="${TARGET_PLATFORM%%/*}"
HOST_GOOS="$(go env GOOS)"
HOST_GOARCH="$(go env GOARCH)"
if [[ "$TARGET_GOOS" != "$HOST_GOOS" ]]; then
  echo "error: WAILS_PLATFORM target OS '$TARGET_GOOS' does not match host OS '$HOST_GOOS'; Wails cannot cross-compile desktop operating systems" >&2
  exit 1
fi

log() { printf '%s\n' "$*"; }
err() { printf 'error: %s\n' "$*" >&2; }

# Keep this in lockstep with Vite's engines.node range. Vite intentionally
# excludes Node 21 and Node 22 releases older than 22.12, so a simple minimum
# major-version check is not sufficient.
node_version_supported() {
  local reported="${1#v}"
  if [[ ! "$reported" =~ ^([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]; then
    return 1
  fi

  local major=$((10#${BASH_REMATCH[1]}))
  local minor=$((10#${BASH_REMATCH[2]}))
  if (( major == 20 )); then
    (( minor >= 19 ))
  elif (( major >= 22 )); then
    (( major > 22 || minor >= 12 ))
  else
    return 1
  fi
}

go_install_bin_dir() {
  local go_bin_dir go_path
  go_bin_dir="$(go env GOBIN 2>/dev/null || true)"
  if [[ -z "$go_bin_dir" ]]; then
    go_path="$(go env GOPATH 2>/dev/null || true)"
    if [[ "$HOST_GOOS" == "windows" ]]; then
      go_bin_dir="${go_path%%;*}/bin"
    else
      go_bin_dir="${go_path%%:*}/bin"
    fi
  fi
  if [[ "$HOST_GOOS" == "windows" ]] && command -v cygpath >/dev/null 2>&1; then
    go_bin_dir="$(cygpath -u "$go_bin_dir")"
  fi
  [[ -n "$go_bin_dir" ]] || return 1
  printf '%s\n' "$go_bin_dir"
}

go_installed_wails_bin() {
  local go_bin_dir candidate
  go_bin_dir="$(go_install_bin_dir || true)"
  [[ -n "$go_bin_dir" ]] || return 1
  for candidate in "$go_bin_dir/wails" "$go_bin_dir/wails.exe"; do
    if [[ -x "$candidate" ]]; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done
  return 1
}

wails_bin() {
  local override="${APEXLOAD_WAILS_BIN:-${WAILS:-}}"
  if [[ -n "$override" ]]; then
    if [[ -x "$override" ]]; then
      printf '%s\n' "$override"
      return 0
    fi
    if command -v "$override" >/dev/null 2>&1; then
      command -v "$override"
      return 0
    fi
    return 1
  fi
  if command -v wails >/dev/null 2>&1; then
    command -v wails
    return 0
  fi
  go_installed_wails_bin
}

missing=0

# Wails CLI
WAILS="$(wails_bin || true)"
installed_wails=""
if [[ -n "${WAILS}" ]]; then
  installed_wails="$("${WAILS}" version 2>/dev/null | sed -n '1{s/[[:space:]]//g;p;q;}')"
fi
if [[ "${INSTALL_WAILS}" -eq 1 && ( -z "${WAILS}" || "${installed_wails}" != "${WAILS_VERSION}" ) ]]; then
  log "Installing Wails CLI ${WAILS_VERSION}..."
  go install "github.com/wailsapp/wails/v2/cmd/wails@${WAILS_VERSION}"
  # Select the exact binary that go install just wrote. An older wails earlier
  # on PATH must not keep winning discovery after an explicit repair request.
  WAILS="$(go_installed_wails_bin || true)"
  installed_wails=""
  if [[ -n "${WAILS}" ]]; then
    installed_wails="$("${WAILS}" version 2>/dev/null | sed -n '1{s/[[:space:]]//g;p;q;}')"
  fi
fi
if [[ -z "${WAILS}" ]]; then
  err "wails CLI not found. Install it with:"
  err "    go install github.com/wailsapp/wails/v2/cmd/wails@${WAILS_VERSION}"
  err "  (or re-run with --install-wails)"
  missing=1
else
  # The CLI generates bindings and platform resources while the module supplies
  # the linked runtime. Mixing versions creates a hybrid build, so fail closed.
  if [[ "${installed_wails}" != "${WAILS_VERSION}" ]]; then
    err "installed wails CLI (${installed_wails}) differs from pinned ${WAILS_VERSION}."
    err "reinstall with: go install github.com/wailsapp/wails/v2/cmd/wails@${WAILS_VERSION}"
    missing=1
  fi
fi

# Node / npm (the dashboard frontend build)
if ! command -v node >/dev/null 2>&1; then
  err "node not found. Install Node.js ^20.19.0 or >=22.12.0 (required by Vite)."
  missing=1
else
  installed_node="$(node --version 2>/dev/null || true)"
  if ! node_version_supported "$installed_node"; then
    err "installed Node.js version '${installed_node:-unknown}' is unsupported; Vite requires Node.js ^20.19.0 or >=22.12.0."
    missing=1
  fi
fi
if ! command -v npm >/dev/null 2>&1; then
  err "npm not found. Install Node.js ^20.19.0 or >=22.12.0 (includes npm)."
  missing=1
fi

if [[ "${TARGET_GOOS}" == "windows" ]]; then
  if ! command -v makensis >/dev/null 2>&1; then
    err "Windows installer builds require the pinned NSIS makensis tool."
    err "    choco install nsis --version=${NSIS_VERSION} --yes --no-progress"
    missing=1
  else
    installed_nsis="$(MSYS2_ARG_CONV_EXCL='*' makensis /VERSION 2>/dev/null | tr -d '[:space:]' || true)"
    if [[ "${installed_nsis}" != "v${NSIS_VERSION}" ]]; then
      err "installed NSIS version '${installed_nsis:-unknown}' differs from pinned v${NSIS_VERSION}."
      err "install NSIS ${NSIS_VERSION} and ensure its makensis is first on PATH."
      missing=1
    fi
  fi
fi

# Linux packages target the WebKitGTK 4.1 ABI. A 4.0-linked binary is not
# interchangeable at runtime, so local/release builds use the same dependency.
if [[ "$(uname -s)" == "Linux" ]]; then
  if command -v pkg-config >/dev/null 2>&1; then
    if ! pkg-config --exists gtk+-3.0 2>/dev/null; then
      err "Linux desktop build needs the GTK3 dev headers. Install with:"
      err "    sudo apt-get install -y libgtk-3-dev"
      missing=1
    fi
    if ! pkg-config --exists webkit2gtk-4.1 2>/dev/null; then
      err "Linux desktop build needs the WebKit2GTK 4.1 development headers. Install with:"
      err "    sudo apt-get install -y libwebkit2gtk-4.1-dev"
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

log "Desktop toolchain OK (host: ${HOST_GOOS}/${HOST_GOARCH})."
log "Note: this builds the host platform's desktop app only; Wails cannot cross-compile."

if [[ "${CHECK_ONLY}" -eq 1 ]]; then
  exit 0
fi

VERSION="${VERSION:-$("${ROOT_DIR}/scripts/resolve-version.sh" 2>/dev/null || echo 0.1.0)}"
COMMIT="${COMMIT:-$(git -C "${ROOT_DIR}" rev-parse HEAD 2>/dev/null || echo unknown)}"
BUILD_TIME="${BUILD_TIME:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
WAILS_BUILD_TAGS="${WAILS_BUILD_TAGS:-}"
if [[ "${TARGET_GOOS}" == "linux" ]]; then
  # webkit2_41 is part of the Linux package ABI contract, not an optional
  # default. Canonicalize caller whitespace/comma separators, then ensure the
  # mandatory tag appears once.
  WAILS_BUILD_TAGS="${WAILS_BUILD_TAGS//[[:space:]]/,}"
  while [[ "$WAILS_BUILD_TAGS" == *,,* ]]; do
    WAILS_BUILD_TAGS="${WAILS_BUILD_TAGS//,,/,}"
  done
  WAILS_BUILD_TAGS="${WAILS_BUILD_TAGS#,}"
  WAILS_BUILD_TAGS="${WAILS_BUILD_TAGS%,}"
  normalized_tags=",${WAILS_BUILD_TAGS},"
  if [[ "$normalized_tags" != *,webkit2_41,* ]]; then
    if [[ -n "$WAILS_BUILD_TAGS" ]]; then
      WAILS_BUILD_TAGS="${WAILS_BUILD_TAGS},webkit2_41"
    else
      WAILS_BUILD_TAGS="webkit2_41"
    fi
  fi
fi

# Keep the declared Info.plist floor and every Mach-O slice aligned. Wails
# otherwise injects an older Intel floor while the arm64 linker clamps higher,
# producing architecture-dependent deployment targets from the same release.
if [[ "${TARGET_GOOS}" == "darwin" ]]; then
  MACOS_MIN_VERSION="12.0"
  export MACOSX_DEPLOYMENT_TARGET="${MACOS_MIN_VERSION}"
  export CGO_CFLAGS="${CGO_CFLAGS:+${CGO_CFLAGS} }-mmacosx-version-min=${MACOS_MIN_VERSION}"
  export CGO_CXXFLAGS="${CGO_CXXFLAGS:+${CGO_CXXFLAGS} }-mmacosx-version-min=${MACOS_MIN_VERSION}"
  export CGO_LDFLAGS="${CGO_LDFLAGS:+${CGO_LDFLAGS} }-mmacosx-version-min=${MACOS_MIN_VERSION}"
fi

log "Building desktop app ${VERSION} (${COMMIT}) for ${TARGET_PLATFORM}..."

# Wails reads productVersion from wails.json before it builds platform resources.
# Serialize this short-lived tracked-file mutation, publish the stamped config
# atomically, and restore it only if it is still byte-for-byte the version we
# published. A user/editor change made during a build is preserved and turns the
# build into an actionable failure instead of being silently overwritten.
WAILS_CONFIG="${ROOT_DIR}/desktop/wails.json"
WAILS_CONFIG_LOCK="${WAILS_CONFIG}.apexload-build.lock"
if ! mkdir "${WAILS_CONFIG_LOCK}" 2>/dev/null; then
  err "another desktop build holds ${WAILS_CONFIG_LOCK}."
  err "if no build is running, remove that stale lock directory and retry."
  exit 1
fi
WAILS_CONFIG_BACKUP="${WAILS_CONFIG_LOCK}/original.json"
WAILS_CONFIG_STAMPED="${WAILS_CONFIG_LOCK}/stamped.json"
WAILS_CONFIG_PUBLISH=""
WAILS_CONFIG_RESTORE=""
WAILS_CONFIG_PUBLISHED=0

finish_wails_config() {
  local status=$?
  local conflict=0
  trap - EXIT HUP INT TERM
  if [[ "${WAILS_CONFIG_PUBLISHED}" -eq 1 ]]; then
    if cmp -s "${WAILS_CONFIG}" "${WAILS_CONFIG_STAMPED}"; then
      WAILS_CONFIG_RESTORE="$(mktemp "${WAILS_CONFIG}.apexload-restore.XXXXXX")"
      cp -p "${WAILS_CONFIG_BACKUP}" "${WAILS_CONFIG_RESTORE}"
      mv "${WAILS_CONFIG_RESTORE}" "${WAILS_CONFIG}"
      WAILS_CONFIG_RESTORE=""
    else
      err "desktop/wails.json changed during the Wails build; preserving the newer file instead of overwriting it."
      conflict=1
    fi
  fi
  [[ -z "${WAILS_CONFIG_PUBLISH}" ]] || rm -f "${WAILS_CONFIG_PUBLISH}"
  [[ -z "${WAILS_CONFIG_RESTORE}" ]] || rm -f "${WAILS_CONFIG_RESTORE}"
  rm -f "${WAILS_CONFIG_BACKUP}" "${WAILS_CONFIG_STAMPED}"
  rmdir "${WAILS_CONFIG_LOCK}" 2>/dev/null || true
  if [[ "${status}" -eq 0 && "${conflict}" -ne 0 ]]; then
    status=1
  fi
  exit "${status}"
}
trap finish_wails_config EXIT
trap 'exit 130' HUP INT TERM
cp -p "${WAILS_CONFIG}" "${WAILS_CONFIG_BACKUP}"
cp -p "${WAILS_CONFIG_BACKUP}" "${WAILS_CONFIG_STAMPED}"
node "${ROOT_DIR}/scripts/stamp-desktop-version.mjs" "${WAILS_CONFIG_BACKUP}" "${VERSION}" "${WAILS_CONFIG_STAMPED}"
WAILS_CONFIG_PUBLISH="$(mktemp "${WAILS_CONFIG}.apexload-publish.XXXXXX")"
cp -p "${WAILS_CONFIG_STAMPED}" "${WAILS_CONFIG_PUBLISH}"
WAILS_CONFIG_PUBLISHED=1
mv "${WAILS_CONFIG_PUBLISH}" "${WAILS_CONFIG}"
WAILS_CONFIG_PUBLISH=""

# Wails' own install cache is keyed only by package.json and node_modules. It
# does not notice package-lock-only changes and marks the cache before npm
# finishes. Run the immutable install ourselves on every serialized desktop
# build so stale or partially installed dependencies can never reach Vite.
(
  cd "${ROOT_DIR}/dashboard"
  npm run check:node
  npm ci --ignore-scripts
)

build_args=(build -clean -trimpath -platform "${TARGET_PLATFORM}" \
  -ldflags "-s -w -X ${BUILDINFO_PKG}.Version=${VERSION} -X ${BUILDINFO_PKG}.Commit=${COMMIT} -X ${BUILDINFO_PKG}.BuildTime=${BUILD_TIME}")
if [[ -n "${WAILS_BUILD_TAGS}" ]]; then
  build_args+=(-tags "${WAILS_BUILD_TAGS}")
fi
if [[ "${TARGET_GOOS}" == "windows" ]]; then
  build_args+=(-nsis -installscope user)
fi

cd "${ROOT_DIR}/desktop"
APEXLOAD_UX_VERSION="${VERSION}" APEXLOAD_UX_BUILD_TIME="${BUILD_TIME}" \
  COPYFILE_DISABLE=1 "${WAILS}" "${build_args[@]}"
log "Desktop app built in desktop/build/bin/."

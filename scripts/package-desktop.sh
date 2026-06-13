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

cat > "$WORKDIR/README.txt" <<EOF
APEX Load Desktop ${VERSION}

This package contains the optional Wails desktop workspace. The desktop app runs
an embedded local controller for local authoring and runs, and it can connect to
Remote Workspaces for sync and remote execution.

Signed installers are intentionally not produced by this scaffold. Production
distribution needs user-supplied macOS Developer ID/notarytool credentials and
Windows signtool certificate material in CI.
EOF

mkdir -p "$DIST_DIR"
(
  cd "$DIST_DIR"
  tar -czf "${PACKAGE}.tar.gz" "$PACKAGE"
  rm -rf "$PACKAGE"
  shasum -a 256 "${PACKAGE}.tar.gz" > SHA256SUMS.txt
)

echo "Desktop package written to $DIST_DIR/${PACKAGE}.tar.gz"

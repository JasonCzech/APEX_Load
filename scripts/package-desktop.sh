#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RAW_VERSION="${VERSION:-}"
COMMIT="${COMMIT:-$(git -C "$ROOT_DIR" rev-parse HEAD 2>/dev/null || echo unknown)}"
BUILD_TIME="${BUILD_TIME:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
VERSION="$(VERSION="$RAW_VERSION" COMMIT="$COMMIT" BUILD_TIME="$BUILD_TIME" "$ROOT_DIR/scripts/resolve-version.sh")"
SEMVER_NUMBER='(0|[1-9][0-9]*)'
SEMVER_IDENTIFIER='(0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)'
SEMVER_BUILD_IDENTIFIER='[0-9A-Za-z-]+'
SEMVER_RE="^${SEMVER_NUMBER}\\.${SEMVER_NUMBER}\\.${SEMVER_NUMBER}(-${SEMVER_IDENTIFIER}(\\.${SEMVER_IDENTIFIER})*)?(\\+${SEMVER_BUILD_IDENTIFIER}(\\.${SEMVER_BUILD_IDENTIFIER})*)?$"
if [[ ! "$VERSION" =~ $SEMVER_RE ]]; then
  echo "Resolved desktop VERSION must be canonical SemVer, got '$VERSION'." >&2
  exit 1
fi
DIST_DIR="${DIST_DIR:-$ROOT_DIR/dist/desktop}"
SOURCE_DIR="${SOURCE_DIR:-$ROOT_DIR/desktop/build/bin}"
DESKTOP_PACKAGE_PLATFORM="${APEXLOAD_DESKTOP_PACKAGE_PLATFORM:-${DESKTOP_PACKAGE_PLATFORM:-}}"
if [[ -n "$DESKTOP_PACKAGE_PLATFORM" ]]; then
  GOOS="${DESKTOP_PACKAGE_PLATFORM%/*}"
  GOARCH="${DESKTOP_PACKAGE_PLATFORM#*/}"
else
  GOOS="${APEXLOAD_DESKTOP_PACKAGE_GOOS:-${GOOS:-$(go env GOOS)}}"
  GOARCH="${APEXLOAD_DESKTOP_PACKAGE_GOARCH:-${GOARCH:-$(go env GOARCH)}}"
fi
case "$GOOS/$GOARCH" in
  linux/amd64|linux/arm64|darwin/arm64|windows/amd64) ;;
  *)
    echo "Unsupported desktop package platform '$GOOS/$GOARCH'; expected linux/amd64, linux/arm64, darwin/arm64, or windows/amd64." >&2
    exit 1
    ;;
esac
PACKAGE="apexload-desktop-${VERSION}-${GOOS}-${GOARCH}"
PACKAGE_BUILD_DIR=""
WORKDIR=""
MACOS_CODESIGN_IDENTITY="${APEXLOAD_MACOS_CODESIGN_IDENTITY:-${MACOS_CODESIGN_IDENTITY:-}}"
MACOS_NOTARIZE="${APEXLOAD_MACOS_NOTARIZE:-${MACOS_NOTARIZE:-0}}"
MACOS_NOTARY_PROFILE="${APEXLOAD_MACOS_NOTARY_PROFILE:-${MACOS_NOTARY_PROFILE:-}}"
MACOS_NOTARY_KEY_PATH="${APEXLOAD_MACOS_NOTARY_KEY_PATH:-${MACOS_NOTARY_KEY_PATH:-}}"
MACOS_NOTARY_KEY_ID="${APEXLOAD_MACOS_NOTARY_KEY_ID:-${MACOS_NOTARY_KEY_ID:-}}"
MACOS_NOTARY_ISSUER_ID="${APEXLOAD_MACOS_NOTARY_ISSUER_ID:-${MACOS_NOTARY_ISSUER_ID:-}}"
WINDOWS_CODESIGN="${APEXLOAD_WINDOWS_CODESIGN:-${WINDOWS_CODESIGN:-0}}"
WINDOWS_CODESIGN_TOOL="${APEXLOAD_WINDOWS_CODESIGN_TOOL:-${WINDOWS_CODESIGN_TOOL:-auto}}"
WINDOWS_SIGNTOOL="${APEXLOAD_WINDOWS_SIGNTOOL:-${WINDOWS_SIGNTOOL:-signtool}}"
WINDOWS_CODESIGN_STORE="${APEXLOAD_WINDOWS_CODESIGN_STORE:-${WINDOWS_CODESIGN_STORE:-My}}"
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
  # The download API treats this directory as a single release channel. Remove
  # artifacts from older versions so lexical filename order can never make an
  # older build appear to be "latest"; retain other platforms of this version.
  find "$DIST_DIR" -mindepth 1 -maxdepth 1 -name 'apexload-desktop-*' \
    ! -name "apexload-desktop-${VERSION}-*" -exec rm -rf {} +
  # Current-platform tarballs/installers are atomically replaced from staging.
  # Only stale transient directories from pre-transactional script versions
  # need explicit removal after the new package has committed.
  rm -rf "${DIST_DIR:?}/${PACKAGE:?}" "${DIST_DIR:?}/${PACKAGE:?}.notary.zip" "${DIST_DIR:?}/${PACKAGE:?}.debroot"
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

validate_desktop_package_filename() {
  local package_name="$1"
  case "$package_name" in
    "apexload-desktop-${VERSION}-linux-amd64.tar.gz"|\
    "apexload-desktop-${VERSION}-linux-amd64.deb"|\
    "apexload-desktop-${VERSION}-linux-arm64.tar.gz"|\
    "apexload-desktop-${VERSION}-linux-arm64.deb"|\
    "apexload-desktop-${VERSION}-darwin-arm64.tar.gz"|\
    "apexload-desktop-${VERSION}-windows-amd64.tar.gz") return 0 ;;
    *)
      echo "Refusing to reuse unrecognized desktop package filename '$package_name'." >&2
      exit 1
      ;;
  esac
}

verify_existing_desktop_packages() {
  if [[ "$#" -eq 0 ]]; then
    return 0
  fi

  local manifest_path="$DIST_DIR/SHA256SUMS.txt"
  local signature_path="${manifest_path}.asc"
  if [[ ! -f "$manifest_path" ]]; then
    echo "Refusing to reuse existing desktop packages without $manifest_path. Remove the unverified packages or restore their checksum manifest." >&2
    exit 1
  fi

  # If a detached signature is present, it is part of the trust record and
  # must verify before any covered bytes are carried into a new manifest. A
  # signed/trusted packaging run must never upgrade an unsigned prior snapshot
  # into newly signed bytes.
  if [[ -f "$signature_path" ]]; then
    if ! command -v gpg >/dev/null 2>&1; then
      echo "gpg is required to verify the existing desktop checksum signature at $signature_path." >&2
      exit 1
    fi
    if ! gpg --batch --verify "$signature_path" "$manifest_path" >/dev/null 2>&1; then
      echo "Existing desktop checksum signature verification failed for $signature_path." >&2
      exit 1
    fi
  elif checksum_signing_requested || trusted_packages_required; then
    echo "Refusing to sign existing desktop packages because their prior checksum manifest is unsigned: $manifest_path." >&2
    exit 1
  fi

  local existing_package
  local existing_name
  local manifest_line
  local expected_hash
  local actual_line
  local actual_hash
  local match_count
  for existing_package in "$@"; do
    existing_name="$(basename "$existing_package")"
    validate_desktop_package_filename "$existing_name"

    expected_hash=""
    match_count=0
    while IFS= read -r manifest_line || [[ -n "$manifest_line" ]]; do
      if [[ "$manifest_line" != *"  $existing_name" ]]; then
        continue
      fi
      expected_hash="${manifest_line%%  *}"
      if [[ ! "$expected_hash" =~ ^[0-9a-f]{64}$ || "$manifest_line" != "$expected_hash  $existing_name" ]]; then
        echo "Existing desktop checksum manifest has a malformed entry for $existing_name." >&2
        exit 1
      fi
      match_count=$((match_count + 1))
    done < "$manifest_path"
    if [[ "$match_count" -ne 1 ]]; then
      echo "Existing desktop checksum manifest must contain exactly one entry for $existing_name; found $match_count." >&2
      exit 1
    fi

    actual_line="$(write_sha256_manifest "$existing_package")"
    actual_hash="${actual_line%% *}"
    if [[ "$actual_hash" != "$expected_hash" ]]; then
      echo "Existing desktop package checksum does not match its prior manifest: $existing_name." >&2
      exit 1
    fi
  done
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
  local zip_path="$PACKAGE_BUILD_DIR/${PACKAGE}.notary.zip"
  (
    cd "$PACKAGE_BUILD_DIR"
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

  local submit_args=(notarytool submit "$zip_path" --wait --timeout 30m)
  if [[ -n "$MACOS_NOTARY_PROFILE" ]]; then
    submit_args+=(--keychain-profile "$MACOS_NOTARY_PROFILE")
  else
    if [[ ! -f "$MACOS_NOTARY_KEY_PATH" || -z "$MACOS_NOTARY_KEY_ID" || -z "$MACOS_NOTARY_ISSUER_ID" ]]; then
      echo "Set APEXLOAD_MACOS_NOTARY_PROFILE or APEXLOAD_MACOS_NOTARY_KEY_PATH/APEXLOAD_MACOS_NOTARY_KEY_ID/APEXLOAD_MACOS_NOTARY_ISSUER_ID for notarization." >&2
      exit 1
    fi
    submit_args+=(--key "$MACOS_NOTARY_KEY_PATH" --key-id "$MACOS_NOTARY_KEY_ID" --issuer "$MACOS_NOTARY_ISSUER_ID")
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

detect_binary_arch() {
  local binary_path="$1"
  if ! command -v file >/dev/null 2>&1; then
    return 0
  fi
  local description
  description="$(file -b "$binary_path" 2>/dev/null || true)"
  case "$description" in
    *x86-64*|*x86_64*) printf 'amd64' ;;
    *ARM64*|*arm64*|*aarch64*) printf 'arm64' ;;
    *80386*|*i386*) printf '386' ;;
  esac
}

validate_binary_arch() {
  local binary_path="$1"
  local expected_os="$2"
  local expected_arch="$3"
  if ! command -v file >/dev/null 2>&1; then
    echo "The 'file' utility is required to validate desktop package architecture." >&2
    exit 1
  fi
  local actual_arch
  local description
  description="$(file -b "$binary_path" 2>/dev/null || true)"
  case "$expected_os" in
    linux) [[ "$description" == *ELF* ]] || { echo "$binary_path is not a Linux ELF binary: $description" >&2; exit 1; } ;;
    darwin) [[ "$description" == *Mach-O* ]] || { echo "$binary_path is not a macOS Mach-O binary: $description" >&2; exit 1; } ;;
    windows) [[ "$description" == *PE32* ]] || { echo "$binary_path is not a Windows PE binary: $description" >&2; exit 1; } ;;
    *) echo "Unsupported binary operating system '$expected_os'." >&2; exit 1 ;;
  esac
  actual_arch="$(detect_binary_arch "$binary_path")"
  if [[ -z "$actual_arch" ]]; then
    echo "Could not determine the architecture of desktop binary $binary_path." >&2
    exit 1
  fi
  if [[ "$actual_arch" != "$expected_arch" ]]; then
    echo "Desktop package is labeled $GOOS/$expected_arch, but $binary_path appears to be $GOOS/$actual_arch." >&2
    exit 1
  fi
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
      if [[ ${#apps[@]} -ne 1 ]]; then
        echo "Desktop package platform is darwin/$GOARCH, but multiple .app bundles were found in $WORKDIR." >&2
        exit 1
      fi
      local darwin_entries=()
      while IFS= read -r -d '' entry; do darwin_entries+=("$entry"); done < <(find "$WORKDIR" -mindepth 1 -maxdepth 1 -print0)
      if [[ ${#darwin_entries[@]} -ne 1 || "${darwin_entries[0]}" != "${apps[0]}" ]]; then
        echo "Desktop macOS build output contains unexpected artifacts; expected exactly one .app bundle." >&2
        exit 1
      fi
      validate_binary_arch "${apps[0]}/Contents/MacOS/apexload" darwin "$GOARCH"
      ;;
    windows)
      local installers=()
      local app_binary="$WORKDIR/apexload.exe"
      if [[ ! -f "$app_binary" ]]; then
        echo "Desktop package platform is windows/$GOARCH, but the Wails app executable is missing at $app_binary." >&2
        exit 1
      fi
      while IFS= read -r -d '' installer; do
        installers+=("$installer")
      done < <(find "$WORKDIR" -maxdepth 1 -type f -name '*-installer.exe' -print0)
      if [[ ${#installers[@]} -eq 0 ]]; then
        echo "Desktop package platform is windows/$GOARCH, but no signed-installable NSIS artifact was found in $WORKDIR. Build Wails with -nsis." >&2
        exit 1
      fi
      if [[ ${#installers[@]} -ne 1 ]]; then
        echo "Desktop Windows build output must contain exactly one NSIS installer." >&2
        exit 1
      fi
      local windows_entries=()
      while IFS= read -r -d '' entry; do windows_entries+=("$entry"); done < <(find "$WORKDIR" -mindepth 1 -maxdepth 1 -print0)
      if [[ ${#windows_entries[@]} -ne 2 ]]; then
        echo "Desktop Windows build output contains unexpected artifacts; expected apexload.exe and one installer." >&2
        exit 1
      fi
      # Standard NSIS installers use an i386 bootstrap executable even when
      # they contain an amd64 payload. Validate the actual Wails binary here;
      # the installer is validated separately by count and install smoke tests.
      validate_binary_arch "$app_binary" windows "$GOARCH"
      ;;
    linux)
      local linux_binary="$WORKDIR/apexload"
      if [[ ! -f "$linux_binary" ]]; then
        echo "Desktop package platform is linux/$GOARCH, but no executable was found at $linux_binary." >&2
        exit 1
      fi
      local linux_entries=()
      while IFS= read -r -d '' entry; do linux_entries+=("$entry"); done < <(find "$WORKDIR" -mindepth 1 -maxdepth 1 -print0)
      if [[ ${#linux_entries[@]} -ne 1 || "${linux_entries[0]}" != "$linux_binary" ]]; then
        echo "Desktop Linux build output contains unexpected artifacts; expected only apexload." >&2
        exit 1
      fi
      validate_binary_arch "$linux_binary" linux "$GOARCH"
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
    echo "signtool PFX/P12 signing is disabled because its password would be exposed in process arguments. Import the certificate into the CurrentUser certificate store and set APEXLOAD_WINDOWS_CODESIGN_SHA1." >&2
    exit 1
  elif [[ -n "$WINDOWS_CODESIGN_SHA1" ]]; then
    sign_args+=(/s "$WINDOWS_CODESIGN_STORE" /sha1 "$WINDOWS_CODESIGN_SHA1")
  elif [[ -n "$WINDOWS_CODESIGN_SUBJECT" ]]; then
    sign_args+=(/s "$WINDOWS_CODESIGN_STORE" /n "$WINDOWS_CODESIGN_SUBJECT")
  else
    echo "signtool signing requires APEXLOAD_WINDOWS_CODESIGN_SHA1 or APEXLOAD_WINDOWS_CODESIGN_SUBJECT; automatic certificate selection is not permitted." >&2
    exit 1
  fi

  # Git Bash/MSYS rewrites slash-prefixed native arguments (for example /fd)
  # into POSIX paths. Convert the one real path explicitly, then disable argv
  # conversion for signtool's Windows switches.
  local native_binary_path="$binary_path"
  if command -v cygpath >/dev/null 2>&1; then
    native_binary_path="$(cygpath -w "$binary_path")"
  fi
  MSYS2_ARG_CONV_EXCL='*' "$sign_tool" "${sign_args[@]}" "$native_binary_path"
  MSYS2_ARG_CONV_EXCL='*' "$sign_tool" verify /pa /all "$native_binary_path"
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

normalize_linux_t64_dependencies() {
  local dependencies="$1"
  local remaining="$dependencies"
  local normalized=""
  local dependency
  local package_name
  local version_constraint
  local legacy_package

  while :; do
    if [[ "$remaining" == *,* ]]; then
      dependency="${remaining%%,*}"
      remaining="${remaining#*,}"
    else
      dependency="$remaining"
      remaining=""
    fi

    # dpkg-shlibdeps separates dependency entries with commas and ordinarily
    # emits one space after each separator. Trim defensively before matching a
    # complete package token so similarly named packages are never rewritten.
    dependency="${dependency#"${dependency%%[![:space:]]*}"}"
    dependency="${dependency%"${dependency##*[![:space:]]}"}"
    if [[ "$dependency" =~ ^(libglib2\.0-0t64|libgtk-3-0t64)([[:space:]]+\([^()]+\))?$ ]]; then
      package_name="${BASH_REMATCH[1]}"
      version_constraint="${BASH_REMATCH[2]:-}"
      legacy_package="${package_name%t64}"
      dependency="${package_name}${version_constraint} | ${legacy_package}${version_constraint}"
    fi

    if [[ -n "$normalized" ]]; then
      normalized+=", "
    fi
    normalized+="$dependency"
    [[ -n "$remaining" ]] || break
  done

  printf '%s' "$normalized"
}

derive_linux_runtime_dependencies() {
  local binary="$1"
  local analysis_dir="$2"
  local stdout_path="$analysis_dir/shlibdeps.stdout"
  local output_lines=()
  local output_line
  local dependencies

  mkdir -p "$analysis_dir/debian"
  cat > "$analysis_dir/debian/control" <<'EOF'
Source: apexload-desktop-dependency-analysis
Section: devel
Priority: optional
Maintainer: Project Stormrunner
Standards-Version: 4.6.2

Package: apexload-desktop
Architecture: any
Description: APEX Load desktop dependency analysis
 Temporary package metadata used only while deriving ELF runtime dependencies.
EOF

  if ! (
    cd "$analysis_dir"
    LC_ALL=C dpkg-shlibdeps -O "$binary"
  ) > "$stdout_path"; then
    echo "dpkg-shlibdeps failed while deriving Linux desktop runtime dependencies." >&2
    exit 1
  fi

  while IFS= read -r output_line || [[ -n "$output_line" ]]; do
    output_lines+=("$output_line")
  done < "$stdout_path"
  if [[ ${#output_lines[@]} -ne 1 || "${output_lines[0]}" != shlibs:Depends=* ]]; then
    echo "dpkg-shlibdeps must emit exactly one non-empty 'shlibs:Depends=' line; got ${#output_lines[@]} line(s)." >&2
    exit 1
  fi
  dependencies="${output_lines[0]#shlibs:Depends=}"
  dependencies="$(normalize_linux_t64_dependencies "$dependencies")"
  if [[ -z "$dependencies" ]]; then
    echo "dpkg-shlibdeps emitted an empty Linux desktop dependency set." >&2
    exit 1
  fi

  printf '%s' "$dependencies"
}

build_linux_deb() {
  if [[ "$GOOS" != "linux" ]]; then
    return 0
  fi
  if ! command -v dpkg-deb >/dev/null 2>&1; then
    echo "dpkg-deb is required to create the installable Linux desktop package." >&2
    exit 1
  fi
  if ! command -v dpkg-shlibdeps >/dev/null 2>&1; then
    echo "dpkg-shlibdeps (provided by dpkg-dev) is required to derive Linux desktop runtime dependencies." >&2
    exit 1
  fi
  local binary="$WORKDIR/apexload"
  if [[ ! -f "$binary" ]]; then
    echo "Linux Wails executable not found at $binary" >&2
    exit 1
  fi
  local deb_arch
  case "$GOARCH" in
    amd64|arm64) deb_arch="$GOARCH" ;;
    386) deb_arch="i386" ;;
    *)
      echo "Unsupported Debian architecture for GOARCH=$GOARCH" >&2
      exit 1
      ;;
  esac

  local deb_root="$PACKAGE_BUILD_DIR/${PACKAGE}.debroot"
  local deb_path="$PACKAGE_BUILD_DIR/${PACKAGE}.deb"
  local dependency_analysis_dir="$PACKAGE_BUILD_DIR/${PACKAGE}.shlibdeps"
  local deb_version
  local elf_runtime_dependencies
  local package_dependencies
  deb_version="$(bash "$ROOT_DIR/scripts/semver-to-debian-version.sh" "$VERSION")"
  rm -rf "$deb_root" "$deb_path" "$dependency_analysis_dir"
  install -d \
    "$deb_root/DEBIAN" \
    "$deb_root/usr/bin" \
    "$deb_root/usr/share/applications" \
    "$deb_root/usr/share/mime/packages" \
    "$deb_root/usr/share/pixmaps"
  install -m 0755 "$binary" "$deb_root/usr/bin/apexload"
  install -m 0644 "$ROOT_DIR/desktop/build/linux/apexload.desktop" \
    "$deb_root/usr/share/applications/apexload.desktop"
  install -m 0644 "$ROOT_DIR/desktop/build/appicon.png" \
    "$deb_root/usr/share/pixmaps/apexload.png"
  install -m 0644 "$ROOT_DIR/desktop/build/linux/apexload-har.xml" \
    "$deb_root/usr/share/mime/packages/apexload-har.xml"

  elf_runtime_dependencies="$(derive_linux_runtime_dependencies "$deb_root/usr/bin/apexload" "$dependency_analysis_dir")"
  rm -rf "$dependency_analysis_dir"
  package_dependencies="${elf_runtime_dependencies}, shared-mime-info, desktop-file-utils, xdg-utils"

  cat > "$deb_root/DEBIAN/control" <<EOF
Package: apexload-desktop
Version: ${deb_version}
Section: devel
Priority: optional
Architecture: ${deb_arch}
Maintainer: Project Stormrunner
Depends: ${package_dependencies}
Description: APEX Load desktop performance testing client
 Kubernetes-native performance testing authoring and local execution client.
EOF
  cat > "$deb_root/DEBIAN/postinst" <<'EOF'
#!/bin/sh
set -e
command -v update-desktop-database >/dev/null 2>&1 && update-desktop-database /usr/share/applications || true
command -v update-mime-database >/dev/null 2>&1 && update-mime-database /usr/share/mime || true
exit 0
EOF
  cat > "$deb_root/DEBIAN/postrm" <<'EOF'
#!/bin/sh
set -e
command -v update-desktop-database >/dev/null 2>&1 && update-desktop-database /usr/share/applications || true
command -v update-mime-database >/dev/null 2>&1 && update-mime-database /usr/share/mime || true
exit 0
EOF
  chmod 0755 "$deb_root/DEBIAN/postinst" "$deb_root/DEBIAN/postrm"
  dpkg-deb --root-owner-group --build "$deb_root" "$deb_path"
  dpkg-deb --info "$deb_path" >/dev/null
  rm -rf "$deb_root"
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

if [[ ! -d "$SOURCE_DIR" ]]; then
  echo "Desktop build output not found at $SOURCE_DIR. Run: make desktop" >&2
  exit 1
fi
SOURCE_DIR="$(cd "$SOURCE_DIR" && pwd -P)"

shopt -s nullglob
artifacts=("$SOURCE_DIR"/*)
if [[ ${#artifacts[@]} -eq 0 ]]; then
  echo "Desktop build output is empty at $SOURCE_DIR. Run: make desktop" >&2
  exit 1
fi

# Build, sign, notarize, package, and sign the checksum manifest entirely in a
# hidden same-filesystem staging directory. Existing downloadable artifacts are
# not touched until every fallible production step has succeeded.
mkdir -p "$DIST_DIR"
DIST_DIR="$(cd "$DIST_DIR" && pwd -P)"
PACKAGE_LOCK_DIR="$DIST_DIR/.apexload-desktop-package.lock"
STAGING_DIR=""
cleanup_desktop_packaging() {
  local status=$?
  trap - EXIT HUP INT TERM
  if [[ -n "$STAGING_DIR" && -d "$STAGING_DIR" ]]; then
    rm -rf "$STAGING_DIR"
  fi
  if [[ -n "$PACKAGE_LOCK_DIR" ]]; then
    rmdir "$PACKAGE_LOCK_DIR" 2>/dev/null || true
  fi
  exit "$status"
}
if ! mkdir "$PACKAGE_LOCK_DIR" 2>/dev/null; then
  echo "Another desktop packaging process holds $PACKAGE_LOCK_DIR. If no packaging process is running, remove that stale lock directory and retry." >&2
  exit 1
fi
trap cleanup_desktop_packaging EXIT
trap 'exit 130' HUP INT TERM

shopt -s nullglob
existing_desktop_packages=(
  "$DIST_DIR"/apexload-desktop-"${VERSION}"-*.tar.gz
  "$DIST_DIR"/apexload-desktop-"${VERSION}"-*.deb
)
shopt -u nullglob
retained_desktop_packages=()
if [[ ${#existing_desktop_packages[@]} -gt 0 ]]; then
  for existing_package in "${existing_desktop_packages[@]}"; do
    existing_name="$(basename "$existing_package")"
    validate_desktop_package_filename "$existing_name"
    case "$existing_name" in
      "${PACKAGE}"*) continue ;;
    esac
    retained_desktop_packages+=("$existing_package")
  done
fi
if [[ ${#retained_desktop_packages[@]} -gt 0 ]]; then
  verify_existing_desktop_packages "${retained_desktop_packages[@]}"
fi

STAGING_DIR="$(mktemp -d "$DIST_DIR/.apexload-desktop-stage.XXXXXX")"
PACKAGE_BUILD_DIR="$STAGING_DIR/output"
WORKDIR="$PACKAGE_BUILD_DIR/$PACKAGE"
mkdir -p "$WORKDIR"

cp -R "${artifacts[@]}" "$WORKDIR/"

validate_desktop_artifacts
sign_windows_binaries
sign_macos_apps
notarize_macos_package
build_linux_deb

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
  cd "$PACKAGE_BUILD_DIR"
  tar -czf "${PACKAGE}.tar.gz" "$PACKAGE"
  rm -rf "$PACKAGE"
)

MANIFEST_DIR="$STAGING_DIR/manifest"
mkdir -p "$MANIFEST_DIR"
shopt -s nullglob
built_desktop_packages=(
  "$PACKAGE_BUILD_DIR"/apexload-desktop-"${VERSION}"-*.tar.gz
  "$PACKAGE_BUILD_DIR"/apexload-desktop-"${VERSION}"-*.deb
)
shopt -u nullglob
if [[ ${#built_desktop_packages[@]} -eq 0 ]]; then
  echo "Desktop packaging produced no distributable artifacts." >&2
  exit 1
fi
if [[ ${#retained_desktop_packages[@]} -gt 0 ]]; then
  for existing_package in "${retained_desktop_packages[@]}"; do
    cp -p "$existing_package" "$MANIFEST_DIR/"
  done
fi
for built_package in "${built_desktop_packages[@]}"; do
  cp -p "$built_package" "$MANIFEST_DIR/"
done

(
  cd "$MANIFEST_DIR"
  shopt -s nullglob
  desktop_packages=(apexload-desktop-"${VERSION}"-*.tar.gz apexload-desktop-"${VERSION}"-*.deb)
  shopt -u nullglob
  manifest_path="$MANIFEST_DIR/SHA256SUMS.txt"
  write_sha256_manifest "${desktop_packages[@]}" > "$manifest_path"
  sign_checksum_manifest "$manifest_path"
)

# Commit only complete bytes. Staging lives under DIST_DIR, so replacing a
# current-platform artifact is an atomic rename on the same filesystem.
for built_package in "${built_desktop_packages[@]}"; do
  mv -f "$built_package" "$DIST_DIR/"
done
mv -f "$MANIFEST_DIR/SHA256SUMS.txt" "$DIST_DIR/SHA256SUMS.txt"
if [[ -f "$MANIFEST_DIR/SHA256SUMS.txt.asc" ]]; then
  mv -f "$MANIFEST_DIR/SHA256SUMS.txt.asc" "$DIST_DIR/SHA256SUMS.txt.asc"
else
  # A prior signed build must never leave an .asc beside newly generated,
  # unsigned checksum bytes.
  rm -f -- "$DIST_DIR/SHA256SUMS.txt.asc"
fi
clean_desktop_dist
rm -rf "$STAGING_DIR"
STAGING_DIR=""
rmdir "$PACKAGE_LOCK_DIR"
PACKAGE_LOCK_DIR=""
trap - EXIT HUP INT TERM

echo "Desktop package written to $DIST_DIR/${PACKAGE}.tar.gz"

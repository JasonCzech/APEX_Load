#!/usr/bin/env bash
set -euo pipefail

binary_path="${1:-}"
if [[ -z "$binary_path" || ! -f "$binary_path" ]]; then
  echo "Windows signing hook requires an existing binary path." >&2
  exit 2
fi

thumbprint="${APEXLOAD_WINDOWS_CODESIGN_SHA1:-}"
subject="${APEXLOAD_WINDOWS_CODESIGN_SUBJECT:-}"
cert_path="${APEXLOAD_WINDOWS_CODESIGN_CERT_PATH:-}"
cert_pem="${APEXLOAD_WINDOWS_CODESIGN_CERT_PEM:-}"
key_path="${APEXLOAD_WINDOWS_CODESIGN_KEY_PATH:-}"
password="${APEXLOAD_WINDOWS_CODESIGN_PASSWORD:-}"
signing_tool="${APEXLOAD_WINDOWS_CODESIGN_TOOL:-auto}"
sign_tool="${APEXLOAD_WINDOWS_SIGNTOOL:-signtool}"
certificate_store="${APEXLOAD_WINDOWS_CODESIGN_STORE:-My}"
digest="${APEXLOAD_WINDOWS_CODESIGN_DIGEST:-sha256}"
timestamp_url="${APEXLOAD_WINDOWS_CODESIGN_TIMESTAMP_URL:-http://timestamp.digicert.com}"

truthy() {
  case "$(printf '%s' "${1:-}" | tr '[:upper:]' '[:lower:]')" in
    1|true|yes|y|on) return 0 ;;
    *) return 1 ;;
  esac
}

has_store_identity() {
  [[ -n "$thumbprint" || -n "$subject" ]]
}

has_file_identity() {
  [[ -n "$cert_path" || -n "$cert_pem" || -n "$key_path" ]]
}

if ! has_store_identity && ! has_file_identity; then
  if truthy "${APEXLOAD_WINDOWS_CODESIGN:-0}" || truthy "${APEXLOAD_REQUIRE_TRUSTED_PACKAGES:-0}"; then
    echo "Windows signing was required, but no certificate identity was configured." >&2
    exit 1
  fi
  # Ordinary PR/local builds intentionally have no protected signing material.
  exit 0
fi

if [[ "$signing_tool" == "auto" ]]; then
  if has_store_identity; then
    signing_tool="signtool"
  else
    signing_tool="osslsigncode"
  fi
fi

case "$signing_tool" in
  signtool)
    if ! has_store_identity; then
      echo "signtool signing requires APEXLOAD_WINDOWS_CODESIGN_SHA1 or APEXLOAD_WINDOWS_CODESIGN_SUBJECT." >&2
      exit 1
    fi
    if ! command -v "$sign_tool" >/dev/null 2>&1; then
      echo "signtool is required by the Windows post-build signing hook." >&2
      exit 1
    fi
    native_binary_path="$binary_path"
    if command -v cygpath >/dev/null 2>&1; then
      native_binary_path="$(cygpath -w "$binary_path")"
    fi
    sign_args=(sign /fd "$digest" /td "$digest")
    [[ -z "$timestamp_url" ]] || sign_args+=(/tr "$timestamp_url")
    if [[ -n "$thumbprint" ]]; then
      sign_args+=(/s "$certificate_store" /sha1 "$thumbprint")
    else
      sign_args+=(/s "$certificate_store" /n "$subject")
    fi

    # Prevent MSYS from rewriting signtool switches such as /fd into paths. The
    # binary itself was explicitly converted above.
    MSYS2_ARG_CONV_EXCL='*' "$sign_tool" "${sign_args[@]}" "$native_binary_path"
    MSYS2_ARG_CONV_EXCL='*' "$sign_tool" verify /pa /all "$native_binary_path"
    ;;
  osslsigncode)
    if ! command -v osslsigncode >/dev/null 2>&1; then
      echo "osslsigncode is required by the Windows post-build signing hook." >&2
      exit 1
    fi
    if [[ -n "$password" ]]; then
      echo "osslsigncode password arguments are disabled because they expose credentials in the process list." >&2
      exit 1
    fi
    if [[ -z "$cert_path" && ( -z "$cert_pem" || -z "$key_path" ) ]]; then
      echo "osslsigncode requires a PFX/P12 or PEM certificate plus key." >&2
      exit 1
    fi
    signed_path="${binary_path}.signed"
    sign_args=(sign -h "$digest" -in "$binary_path" -out "$signed_path")
    [[ -z "$timestamp_url" ]] || sign_args+=(-t "$timestamp_url")
    if [[ -n "$cert_path" ]]; then
      sign_args+=(-pkcs12 "$cert_path")
    else
      sign_args+=(-certs "$cert_pem" -key "$key_path")
    fi
    osslsigncode "${sign_args[@]}"
    mv -f "$signed_path" "$binary_path"
    osslsigncode verify "$binary_path"
    ;;
  *)
    echo "Unsupported APEXLOAD_WINDOWS_CODESIGN_TOOL '$signing_tool'." >&2
    exit 1
    ;;
esac

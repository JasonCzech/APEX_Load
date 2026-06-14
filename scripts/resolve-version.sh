#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SEMVER_RE='^v?[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z][0-9A-Za-z.-]*)?$'
RAW_BUILD_RE='^[0-9a-fA-F]{12,64}$'

trim_v() {
  local value="${1:-}"
  if [[ "$value" =~ ^[vV][0-9] ]]; then
    printf '%s\n' "${value:1}"
  else
    printf '%s\n' "$value"
  fi
}

is_raw_build_version() {
  [[ "${1:-}" =~ $RAW_BUILD_RE ]]
}

short_revision() {
  local value="${1:-}"
  if [[ -n "$value" ]]; then
    printf '%s\n' "${value:0:7}"
    return
  fi
  value="$(git -C "$ROOT_DIR" rev-parse HEAD 2>/dev/null || true)"
  if [[ -n "$value" ]]; then
    printf '%s\n' "${value:0:7}"
    return
  fi
  printf 'unknown\n'
}

build_date() {
  if [[ -n "${BUILD_TIME:-}" ]]; then
    local digits
    digits="$(printf '%s' "$BUILD_TIME" | tr -cd '0-9')"
    if [[ "${#digits}" -ge 8 ]]; then
      printf '%s\n' "${digits:0:8}"
      return
    fi
  fi
  if [[ -n "${SOURCE_DATE_EPOCH:-}" ]]; then
    date -u -d "@$SOURCE_DATE_EPOCH" +%Y%m%d 2>/dev/null && return
    date -u -r "$SOURCE_DATE_EPOCH" +%Y%m%d 2>/dev/null && return
  fi
  date -u +%Y%m%d
}

explicit="${VERSION:-}"
if [[ -n "$explicit" ]]; then
  if ! is_raw_build_version "$explicit"; then
    trim_v "$explicit"
    exit 0
  fi
fi

head_tag="$(
  git -C "$ROOT_DIR" tag --points-at HEAD --sort=-v:refname 2>/dev/null \
    | grep -E "$SEMVER_RE" \
    | head -n 1 || true
)"
if [[ -n "$head_tag" ]]; then
  trim_v "$head_tag"
  exit 0
fi

latest_tag="$(
  git -C "$ROOT_DIR" tag --list --sort=-v:refname 2>/dev/null \
    | grep -E "$SEMVER_RE" \
    | head -n 1 || true
)"

base="0.1.0"
if [[ -n "$latest_tag" ]]; then
  base="$(trim_v "$latest_tag")"
fi

revision="${COMMIT:-}"
if [[ -z "$revision" && -n "$explicit" ]] && is_raw_build_version "$explicit"; then
  revision="$explicit"
fi

printf '%s-dev.%s+%s\n' "$base" "$(build_date)" "$(short_revision "$revision")"

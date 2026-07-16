#!/usr/bin/env bash
set -euo pipefail

die() {
  printf 'registry-reference-state: %s\n' "$*" >&2
  exit 2
}

tool="${1:-}"
reference="${2:-}"
[[ -n "$tool" && -n "$reference" ]] || die "usage: $0 <docker-buildx|skopeo> <registry-reference>"

error_file="$(mktemp)"
trap 'rm -f "$error_file"' EXIT

case "$tool" in
  docker-buildx)
    if docker buildx imagetools inspect "$reference" >/dev/null 2>"$error_file"; then
      printf 'present\n'
      exit 0
    fi
    ;;
  skopeo)
    if skopeo inspect "docker://${reference}" >/dev/null 2>"$error_file"; then
      printf 'present\n'
      exit 0
    fi
    ;;
  *)
    die "unsupported inspection tool: $tool"
    ;;
esac

error_text="$(<"$error_file")"
error_lower="$(printf '%s' "$error_text" | tr '[:upper:]' '[:lower:]')"
reference_lower="$(printf '%s' "$reference" | tr '[:upper:]' '[:lower:]')"

# OCI registries normally report a missing tag or repository using one of the
# standard distribution error codes below. Buildx also reduces that response to
# "<reference>: not found" for some registries. Do not treat a generic command
# failure or an unqualified HTTP 404 as absence: authentication failures and
# transient registry/proxy failures must stop immutable tag promotion.
if [[ "$error_lower" == *"manifest unknown"* ]] ||
  [[ "$error_lower" == *"manifest_unknown"* ]] ||
  [[ "$error_lower" == *"name unknown"* ]] ||
  [[ "$error_lower" == *"name_unknown"* ]] ||
  [[ "$error_lower" == *"${reference_lower}: not found"* ]] ||
  [[ "$error_lower" == *"docker://${reference_lower}: not found"* ]]; then
  printf 'absent\n'
  exit 0
fi

printf 'registry-reference-state: inspection of %s failed without a confirmed not-found response\n' "$reference" >&2
[[ -z "$error_text" ]] || printf '%s\n' "$error_text" >&2
exit 1

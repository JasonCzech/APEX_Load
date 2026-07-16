#!/usr/bin/env bash

# APEX release identifiers intentionally implement the SemVer 2.0.0 core and
# prerelease grammar only. Build metadata is excluded because release tags and
# asset names must have one canonical immutable spelling.

is_apex_semver() {
  local value="${1:-}"
  local core prerelease identifier
  local -a identifiers=()

  [[ -n "$value" && "${#value}" -le 128 && "$value" != v* && "$value" != *+* ]] || return 1
  if [[ "$value" == *-* ]]; then
    core="${value%%-*}"
    prerelease="${value#*-}"
    [[ -n "$prerelease" && "$prerelease" != .* && "$prerelease" != *. && "$prerelease" != *..* ]] || return 1
  else
    core="$value"
    prerelease=""
  fi

  [[ "$core" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || return 1
  [[ -z "$prerelease" ]] && return 0

  IFS='.' read -r -a identifiers <<< "$prerelease"
  for identifier in "${identifiers[@]}"; do
    [[ "$identifier" =~ ^[0-9A-Za-z-]+$ ]] || return 1
    if [[ "$identifier" =~ ^[0-9]+$ ]]; then
      [[ "$identifier" =~ ^(0|[1-9][0-9]*)$ ]] || return 1
    fi
  done
}

is_apex_release_tag() {
  local tag="${1:-}"
  [[ "$tag" == v* ]] && is_apex_semver "${tag#v}"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  case "${1:-}" in
    version)
      is_apex_semver "${2:-}"
      ;;
    tag)
      is_apex_release_tag "${2:-}"
      ;;
    *)
      printf 'usage: %s {version|tag} VALUE\n' "$0" >&2
      exit 2
      ;;
  esac
fi

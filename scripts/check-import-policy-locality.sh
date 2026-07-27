#!/usr/bin/env bash
set -euo pipefail

script_root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_root/.." && pwd)"
allowlist="$script_root/import-policy-locality.allowlist"
print_detected=false

usage() {
  printf '%s\n' \
    'usage: check-import-policy-locality.sh [--repo-root PATH] [--allowlist PATH] [--print-detected]' >&2
}

while [[ $# -gt 0 ]]; do
  case "$1" in
  --repo-root)
    [[ $# -ge 2 ]] || {
      usage
      exit 2
    }
    repo_root="$2"
    shift 2
    ;;
  --allowlist)
    [[ $# -ge 2 ]] || {
      usage
      exit 2
    }
    allowlist="$2"
    shift 2
    ;;
  --print-detected)
    print_detected=true
    shift
    ;;
  *)
    usage
    exit 2
    ;;
  esac
done

if ! command -v rg >/dev/null 2>&1; then
  printf 'ripgrep (rg) is required for the import policy locality check\n' >&2
  exit 1
fi
if [[ ! -d "$repo_root/pkg/convert" ]]; then
  printf 'missing converter root: %s\n' "$repo_root/pkg/convert" >&2
  exit 1
fi
if [[ "$print_detected" != true && ! -f "$allowlist" ]]; then
  printf 'missing import policy locality allowlist: %s\n' "$allowlist" >&2
  exit 1
fi

scan_dir="$(mktemp -d)"
trap 'rm -rf -- "$scan_dir"' EXIT
detected="$scan_dir/detected"
normalized_allowlist="$scan_dir/allowlist"
new_entries="$scan_dir/new"
stale_entries="$scan_dir/stale"

cd "$repo_root"

# Scan every direct converter package except the source-neutral infrastructure
# packages below. This automatically includes new importer directories, and in
# particular covers HAR, DevWeb, and enterprise archives.
importer_dirs=()
for directory in pkg/convert/*; do
  [[ -d "$directory" ]] || continue
  case "$(basename "$directory")" in
  convertutil | correlate | credentialpolicy | diag | importcontract | importreview | paramize | requestimport | sourceview | srcparse | tmpl | uniquify)
    continue
    ;;
  esac
  importer_dirs+=("$directory")
done

if [[ ${#importer_dirs[@]} -eq 0 ]]; then
  printf 'no importer directories found under %s\n' "$repo_root/pkg/convert" >&2
  exit 1
fi

{
  # Archive readers belong in pkg/internal/archiveingest. Tests are excluded so
  # hostile-archive fixtures may still use the standard library directly.
  #
  # The API-side import handlers are scanned too: they read uploaded archives
  # before a converter ever sees them, so an unguarded reader there bypasses the
  # entry-path policy exactly as one in pkg/convert would.
  {
    rg -l \
      --glob '*.go' \
      --glob '!**/*_test.go' \
      '"archive/(zip|tar)"' \
      "${importer_dirs[@]}" \
      pkg/api/handlers_import_enterprise.go \
      pkg/api/handlers_import_jmeter.go \
      pkg/api/handlers_web_api_imports.go \
      pkg/api/handlers_import_assets.go || true
  } | sed 's#^#archive:#'

  # Source adapters may parse source-specific auth, headers, bodies, URLs,
  # cookies, and multipart syntax. They may not add new policy classifiers,
  # sanitizers, redactors, projectors, validators, or credential constructors.
  # Record exact function identities: an exception for one function never
  # exempts another function added to the same file.
  {
    rg -n \
      --glob '*.go' \
      --glob '!**/*_test.go' \
      '^func[[:space:]]+(\([^)]*\)[[:space:]]+)?[A-Za-z_][A-Za-z0-9_]*[[:space:]]*\(' \
      "${importer_dirs[@]}" || true
  } |
    sed -E 's#^([^:]+):[0-9]+:func[[:space:]]+(\([^)]*\)[[:space:]]+)?([A-Za-z_][A-Za-z0-9_]*).*#\1:\3#' |
    awk -F: '
      function has_word(name, lower_word, camel_word, lower_name) {
        lower_name = tolower(name)
        if (lower_name ~ ("(^|_)" lower_word "s?($|_)")) {
          return 1
        }
        if (name ~ ("(^|_)" lower_word "([A-Z0-9_]|$)")) {
          return 1
        }
        if (name ~ (camel_word "s?([A-Z0-9_]|$)")) {
          return 1
        }
        return 0
      }

      function has_policy_verb(name, lower) {
        lower = tolower(name)
        return lower ~ /(classify|externalize|filter|govern|mask|normalize|policy|project|redact|rewrite|sanitize|scrub|serialize|validate)/
      }

      function is_policy_name(name, lower, component) {
        lower = tolower(name)

        # Credential vocabulary/classification is policy regardless of verb.
        if (has_word(name, "credential", "Credential") ||
            has_word(name, "sensitive", "Sensitive") ||
            has_word(name, "secret", "Secret") ||
            has_word(name, "apikey", "APIKey") ||
            has_word(name, "password", "Password") ||
            has_word(name, "passphrase", "Passphrase")) {
          return 1
        }

        # Auth helpers are policy when they apply, construct, project, sanitize,
        # or validate effective authentication rather than merely parse source.
        if (has_word(name, "auth", "Auth") &&
            (lower ~ /(apply|config|effective|inject|token|value)/ || has_policy_verb(name))) {
          return 1
        }

        # Header/body/URL policy verbs are deliberately narrower than parsing
        # verbs such as parse, read, detect, resolve, or append.
        component = has_word(name, "header", "Header") ||
          has_word(name, "body", "Body") ||
          has_word(name, "url", "URL") ||
          has_word(name, "query", "Query") ||
          has_word(name, "cookie", "Cookie") ||
          has_word(name, "multipart", "Multipart") ||
          has_word(name, "form", "Form") ||
          has_word(name, "signature", "Signature")
        if (component && has_policy_verb(name)) {
          return 1
        }

        return 0
      }

      is_policy_name($2) {
        print "policy:" $1 ":" $2
      }
    '
} | sort -u >"$detected"

if [[ "$print_detected" == true ]]; then
  cat "$detected"
  exit 0
fi

sed \
  -e '/^[[:space:]]*#/d' \
  -e '/^[[:space:]]*$/d' \
  "$allowlist" |
  sort -u >"$normalized_allowlist"

comm -13 "$normalized_allowlist" "$detected" >"$new_entries"
comm -23 "$normalized_allowlist" "$detected" >"$stale_entries"

if [[ -s "$new_entries" ]]; then
  printf '%s\n' \
    'new importer-local archive or credential/header/body/URL policy was detected:' >&2
  sed 's/^/  /' "$new_entries" >&2
  printf '%s\n' \
    'Move the behavior into pkg/internal/archiveingest, pkg/convert/credentialpolicy, or pkg/convert/requestimport.' >&2
  exit 1
fi

if [[ -s "$stale_entries" ]]; then
  printf '%s\n' \
    'stale import policy locality exceptions were detected:' >&2
  sed 's/^/  /' "$stale_entries" >&2
  printf '%s\n' \
    'Remove obsolete entries so the allowlist remains an exact compatibility baseline.' >&2
  exit 1
fi

printf 'import policy locality check passed\n'

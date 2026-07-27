#!/usr/bin/env bash
set -euo pipefail

script_root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
fixture_root="$script_root/testdata/import-policy-locality"
guard="$script_root/check-import-policy-locality.sh"

scratch="$(mktemp -d)"
trap 'rm -rf -- "$scratch"' EXIT

while IFS= read -r source; do
  relative="${source#"$fixture_root/root/"}"
  destination="$scratch/${relative%.txt}"
  mkdir -p "$(dirname "$destination")"
  cp "$source" "$destination"
done < <(find "$fixture_root/root" -type f -name '*.txt' | sort)

output="$scratch/output"
if bash "$guard" \
  --repo-root "$scratch" \
  --allowlist "$fixture_root/partial.allowlist" \
  >"$output" 2>&1; then
  printf 'import policy locality self-test unexpectedly accepted forbidden fixtures\n' >&2
  exit 1
fi

expected=(
  'archive:pkg/convert/devweb/archive_reader.go'
  'policy:pkg/convert/enterprisearchive/local_policy.go:projectEnterpriseURL'
  'policy:pkg/convert/har/local_policy.go:redactHARHeaders'
  'policy:pkg/convert/postman/local_policy.go:applyPostmanAuth'
)
for entry in "${expected[@]}"; do
  if ! grep -Fq "$entry" "$output"; then
    printf 'import policy locality self-test missed %s\n' "$entry" >&2
    sed 's/^/  /' "$output" >&2
    exit 1
  fi
done

if grep -Fq 'policy:pkg/convert/postman/local_policy.go:sanitizeBody' "$output"; then
  printf 'exactly allowlisted fixture was reported as new\n' >&2
  exit 1
fi
if grep -Fq 'policy:pkg/convert/har/local_policy.go:parseHARHeaders' "$output"; then
  printf 'source-specific parsing fixture was incorrectly classified as policy\n' >&2
  exit 1
fi

printf 'import policy locality self-test passed\n'

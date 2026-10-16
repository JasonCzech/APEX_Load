#!/usr/bin/env bash
# Both profiles are part of the normal code-change cycle. Each invocation uses
# its own disposable controller/store and separate Playwright artifact directory.
# APEXLOAD_E2E_PROFILES narrows the run to some of them (CI runs each profile
# in its own job).
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"
status=0
for profile in ${APEXLOAD_E2E_PROFILES:-dev-admin local-auth}; do
  case "$profile" in
  dev-admin) port="${APEXLOAD_E2E_PORT:-18081}" ;;
  local-auth) port="${APEXLOAD_E2E_AUTH_PORT:-18082}" ;;
  *)
    echo "unknown dashboard workflow profile: $profile" >&2
    exit 2
    ;;
  esac
  APEXLOAD_E2E_PROFILE="$profile" APEXLOAD_E2E_PORT="$port" scripts/run-e2e.sh || status=1
done
exit "$status"

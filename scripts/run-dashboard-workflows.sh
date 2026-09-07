#!/usr/bin/env bash
# Both profiles are part of the normal code-change cycle. Each invocation uses
# its own disposable controller/store and separate Playwright artifact directory.
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"
status=0
APEXLOAD_E2E_PROFILE=dev-admin APEXLOAD_E2E_PORT="${APEXLOAD_E2E_PORT:-18081}" scripts/run-e2e.sh || status=1
APEXLOAD_E2E_PROFILE=local-auth APEXLOAD_E2E_PORT="${APEXLOAD_E2E_AUTH_PORT:-18082}" scripts/run-e2e.sh || status=1
exit "$status"

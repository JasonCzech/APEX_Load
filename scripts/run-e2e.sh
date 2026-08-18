#!/usr/bin/env bash
# Run the dashboard's Playwright suite against a real, locally-built controller.
#
# This mirrors the recipe in .github/workflows/ci.yml ("Browser smoke against
# the embedded controller dashboard") so a spec that passes here passes there.
# It exists because the alternative — writing browser specs without being able
# to run them — produces specs that assert what the author imagined rather than
# what the application does.
#
# The controller is started on a dedicated port with a throwaway database and
# archive directory, so a run never touches the developer's own apexload.db.
#
# Usage:
#   scripts/run-e2e.sh                       # whole suite
#   scripts/run-e2e.sh tier0-journey         # only specs matching a substring
#   APEXLOAD_E2E_PORT=18099 scripts/run-e2e.sh
#   APEXLOAD_E2E_PROFILE=local-auth scripts/run-e2e.sh tier0-local-auth.spec.ts
#
# Requires a built dashboard in pkg/api/static (cd dashboard && npm run build).
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

port="${APEXLOAD_E2E_PORT:-18081}"
base_url="http://127.0.0.1:${port}"
profile="${APEXLOAD_E2E_PROFILE:-dev-admin}"
workdir="$(mktemp -d -t apexload-e2e.XXXXXX)"
controller_bin="$workdir/controller"
controller_log="$workdir/controller.log"
pid=""

if [[ "$profile" != "dev-admin" && "$profile" != "local-auth" ]]; then
  echo "ERROR: APEXLOAD_E2E_PROFILE must be dev-admin or local-auth, got $profile" >&2
  exit 1
fi

cleanup() {
  local code=$?
  trap - EXIT INT TERM
  if [[ -n "$pid" ]]; then
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
  fi
  if (( code != 0 )) && [[ -f "$controller_log" ]]; then
    echo "--- controller log (last 60 lines) ---" >&2
    tail -60 "$controller_log" >&2
  fi
  rm -rf "$workdir"
  exit "$code"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

if [[ ! -f pkg/api/static/index.html ]]; then
  echo "ERROR: pkg/api/static/index.html is missing — the controller serves the" >&2
  echo "dashboard from there. Build it first: (cd dashboard && npm run build)" >&2
  exit 1
fi

echo "Building controller..." >&2
go build -trimpath -o "$controller_bin" ./cmd/controller

start_controller() {
  echo "Starting controller on ${base_url} (profile=$profile, throwaway DB in $workdir)..." >&2
  APEXLOAD_ADDR="127.0.0.1:${port}" \
    APEXLOAD_DB="$workdir/apexload-e2e.db" \
    APEXLOAD_ARCHIVE_DIR="$workdir/archives" \
    "$controller_bin" >>"$controller_log" 2>&1 &
  pid=$!

  local ready=""
  for _ in $(seq 1 60); do
    if curl --silent --fail "${base_url}/api/v1/system/health" >/dev/null 2>&1; then
      ready=yes
      break
    fi
    if ! kill -0 "$pid" 2>/dev/null; then
      echo "ERROR: controller exited during startup" >&2
      exit 1
    fi
    sleep 1
  done
  if [[ -z "$ready" ]]; then
    echo "ERROR: controller did not become healthy within 60s" >&2
    exit 1
  fi
  echo "Controller healthy." >&2
}

stop_controller() {
  if [[ -n "$pid" ]]; then
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
    pid=""
  fi
}

start_controller

if [[ "$profile" == "local-auth" ]]; then
  settings_headers="$workdir/settings.headers"
  curl --silent --fail --dump-header "$settings_headers" \
    "${base_url}/api/v1/system/settings" >/dev/null
  settings_etag="$(awk 'tolower($1) == "etag:" {sub(/\r$/, "", $2); print $2}' "$settings_headers")"
  if [[ -z "$settings_etag" ]]; then
    echo "ERROR: system settings response carried no ETag" >&2
    exit 1
  fi
  curl --silent --fail --request PUT \
    --header "Content-Type: application/json" \
    --header "If-Match: $settings_etag" \
    --data '{"auth":{"mode":"local"}}' \
    "${base_url}/api/v1/system/settings" >/dev/null
  stop_controller
  start_controller
fi

# A fresh throwaway store is expected to start in the local dev bypass mode.
# There is intentionally no APEXLOAD_AUTH_MODE environment variable: auth mode
# is persisted system state. Check it here so E2E never silently inherits a
# production-mode database or proves the journey under an unknown identity.
auth_status="$(curl --silent --fail "${base_url}/api/v1/auth/me")"
if [[ "$profile" == "dev-admin" ]]; then
  expected_auth='"mode":"dev"'
  expected_enabled='"enabled":false'
else
  expected_auth='"mode":"local"'
  expected_enabled='"enabled":true'
fi
if [[ "$auth_status" != *"$expected_auth"* ]] || [[ "$auth_status" != *"$expected_enabled"* ]]; then
  echo "ERROR: E2E auth profile $profile was not applied; /api/v1/auth/me returned:" >&2
  echo "$auth_status" >&2
  exit 1
fi
echo "Auth profile verified: $profile." >&2

cd dashboard
APEXLOAD_E2E_BASE_URL="$base_url" ./node_modules/.bin/playwright test "$@"

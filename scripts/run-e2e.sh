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
# A run only ever tests the controller it started. A port that something else
# already listens on (a long-running local controller, another run) is refused
# before anything is built, and once the controller answers, the run checks
# that the process it started is the one listening. Without that, the health
# probe would succeed against the other server and every spec would exercise
# the wrong code.
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

have_lsof() {
  command -v lsof >/dev/null 2>&1
}

# port_in_use reports whether anything listens on the port: lsof sees a
# listener on any address, and a connection to 127.0.0.1 that succeeds is one
# too (lsof is optional, and may not list other users' processes).
port_in_use() {
  if have_lsof && lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
    return 0
  fi
  (exec 3<>"/dev/tcp/127.0.0.1/${port}") 2>/dev/null
}

# require_free_port refuses a port another process listens on, naming it when
# lsof can, and never stops that process.
require_free_port() {
  if ! port_in_use; then
    return 0
  fi
  echo "ERROR: port ${port} is already in use; this run would test that server instead of the controller it builds." >&2
  if have_lsof; then
    lsof -nP -iTCP:"$port" -sTCP:LISTEN 2>/dev/null | awk 'NR > 1 { print "  held by pid " $2 " (" $1 ")" }' | sort -u >&2 || true
  fi
  echo "Stop that process or choose another port with APEXLOAD_E2E_PORT." >&2
  exit 1
}

# require_own_listener confirms the process this run started is the one
# answering on the port. Without lsof it settles for the process still being
# alive a moment after it answered, since a controller that lost the port
# exits as soon as it tries to bind.
require_own_listener() {
  if have_lsof; then
    if [[ -z "$(lsof -nP -a -p "$pid" -iTCP:"$port" -sTCP:LISTEN -t 2>/dev/null || true)" ]]; then
      echo "ERROR: ${base_url} answered, but not from the controller this run started (pid ${pid})." >&2
      exit 1
    fi
    return 0
  fi
  sleep 1
  if ! kill -0 "$pid" 2>/dev/null; then
    echo "ERROR: ${base_url} answered, but the controller this run started has exited." >&2
    exit 1
  fi
}

# Refuse an occupied port before spending time on the build.
require_free_port

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

# Public test fixture key, scoped to this disposable controller only. This
# exercises encrypted durable state without modifying a developer key store.
start_controller() {
  require_free_port
  echo "Starting controller on ${base_url} (profile=$profile, throwaway DB in $workdir)..." >&2
  APEXLOAD_ADDR="127.0.0.1:${port}" \
    APEXLOAD_DB="$workdir/apexload-e2e.db" \
    APEXLOAD_ARCHIVE_DIR="$workdir/archives" \
    APEXLOAD_WRAPPING_MASTER_KEY="${APEXLOAD_WRAPPING_MASTER_KEY:-MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=}" \
    "$controller_bin" >>"$controller_log" 2>&1 &
  pid=$!

  local ready=""
  for _ in $(seq 1 60); do
    if ! kill -0 "$pid" 2>/dev/null; then
      echo "ERROR: controller exited during startup" >&2
      exit 1
    fi
    if curl --silent --fail "${base_url}/api/v1/system/health" >/dev/null 2>&1; then
      ready=yes
      break
    fi
    sleep 1
  done
  if [[ -z "$ready" ]]; then
    echo "ERROR: controller did not become healthy within 60s" >&2
    exit 1
  fi
  require_own_listener
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

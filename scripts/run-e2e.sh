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
#
# Requires a built dashboard in pkg/api/static (cd dashboard && npm run build).
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

port="${APEXLOAD_E2E_PORT:-18081}"
base_url="http://127.0.0.1:${port}"
workdir="$(mktemp -d -t apexload-e2e.XXXXXX)"
controller_bin="$workdir/controller"
controller_log="$workdir/controller.log"
pid=""

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

echo "Starting controller on ${base_url} (throwaway DB in $workdir)..." >&2
APEXLOAD_ADDR="127.0.0.1:${port}" \
  APEXLOAD_DB="$workdir/apexload-e2e.db" \
  APEXLOAD_ARCHIVE_DIR="$workdir/archives" \
  "$controller_bin" >"$controller_log" 2>&1 &
pid=$!

ready=""
for _ in $(seq 1 60); do
  if curl --silent --fail "${base_url}/api/v1/system/health" >/dev/null 2>&1; then
    ready=yes
    break
  fi
  # Fail fast if the controller died rather than waiting out the full timeout on
  # a process that is never coming back.
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

cd dashboard
APEXLOAD_E2E_BASE_URL="$base_url" ./node_modules/.bin/playwright test "$@"

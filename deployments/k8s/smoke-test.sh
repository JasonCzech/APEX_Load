#!/bin/sh
set -eu

CONTROLLER_URL="${APEXLOAD_CONTROLLER_URL:-http://127.0.0.1:18080}"
SCRIPT_FILE="${1:-examples/k8s-distributed-smoke.json}"
TEST_NAME="${APEXLOAD_SMOKE_TEST_NAME:-k8s-distributed-smoke}"
TEST_DURATION="${APEXLOAD_SMOKE_DURATION:-1m}"
POLL_INTERVAL="${APEXLOAD_SMOKE_POLL_INTERVAL:-2}"
MAX_POLLS="${APEXLOAD_SMOKE_MAX_POLLS:-20}"
TARGET_TRANSACTIONS="${APEXLOAD_SMOKE_TARGET_TRANSACTIONS:-2}"

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Missing required command: $1" >&2
    exit 1
  fi
}

require_cmd curl
require_cmd jq

if [ ! -f "$SCRIPT_FILE" ]; then
  echo "Script file not found: $SCRIPT_FILE" >&2
  exit 1
fi

script_resp=$(curl -sf -X POST "$CONTROLLER_URL/api/v1/scripts" -H 'Content-Type: application/json' --data-binary @"$SCRIPT_FILE")
script_id=$(printf '%s' "$script_resp" | jq -r '.id')
if [ -z "$script_id" ] || [ "$script_id" = "null" ]; then
  echo "Failed to create script" >&2
  printf '%s
' "$script_resp" >&2
  exit 1
fi

test_payload=$(cat <<JSON
{
  "name": "$TEST_NAME",
  "duration": "$TEST_DURATION",
  "groups": [
    {
      "name": "generator-a",
      "script_id": "$script_id",
      "vusers": 1,
      "ramp_up": "1s"
    },
    {
      "name": "generator-b",
      "script_id": "$script_id",
      "vusers": 1,
      "ramp_up": "1s"
    }
  ]
}
JSON
)

test_resp=$(printf '%s' "$test_payload" | curl -sf -X POST "$CONTROLLER_URL/api/v1/tests" -H 'Content-Type: application/json' --data-binary @-)
test_id=$(printf '%s' "$test_resp" | jq -r '.id')
if [ -z "$test_id" ] || [ "$test_id" = "null" ]; then
  echo "Failed to create test" >&2
  printf '%s
' "$test_resp" >&2
  exit 1
fi

curl -sf -X POST "$CONTROLLER_URL/api/v1/tests/$test_id/start" -H 'Content-Type: application/json' --data '{}' >/dev/null

echo "script_id=$script_id"
echo "test_id=$test_id"

poll=1
stop_sent=0
while [ "$poll" -le "$MAX_POLLS" ]; do
  test_status_resp=$(curl -sf "$CONTROLLER_URL/api/v1/tests/$test_id")
  test_state=$(printf '%s' "$test_status_resp" | jq -r '.status')
  transaction_count=$(printf '%s' "$test_status_resp" | jq -r '.live_metrics.total_transactions // 0')
  echo "poll_${poll} state=$test_state transactions=$transaction_count"
  if [ "$stop_sent" -eq 0 ] && [ "$transaction_count" -ge "$TARGET_TRANSACTIONS" ]; then
    curl -sf -X POST "$CONTROLLER_URL/api/v1/tests/$test_id/stop" -H 'Content-Type: application/json' --data '{}' >/dev/null
    echo "graceful_stop_requested=true"
    stop_sent=1
  fi
  case "$test_state" in
    COMPLETE|FAILED|STOPPED|ABORTED|complete|completed|failed|stopped|aborted)
      printf '%s
' "$test_status_resp"
      if [ "$test_state" = "COMPLETE" ] || [ "$test_state" = "complete" ] || [ "$test_state" = "completed" ]; then
        exit 0
      fi
      exit 1
      ;;
  esac
  sleep "$POLL_INTERVAL"
  poll=$((poll + 1))
done

echo "Timed out waiting for smoke test to reach a terminal state" >&2
curl -sf "$CONTROLLER_URL/api/v1/tests/$test_id"
exit 1

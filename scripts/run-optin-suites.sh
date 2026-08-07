#!/usr/bin/env bash
# Run the test suites that `go test ./...` excludes.
#
# The default suite skips every build-tag- and environment-gated suite, so they run
# in no automated context unless someone runs them on purpose. This script is that
# "on purpose": one command, an explicit per-suite verdict, and a non-zero exit if
# any suite that actually ran failed.
#
# Inventory and rationale: docs/OPTIN_TEST_SUITES.md
#
#   ./scripts/run-optin-suites.sh          # suites needing no external infrastructure
#   ./scripts/run-optin-suites.sh --all    # also the live-broker suites
#   ./scripts/run-optin-suites.sh --list   # print the inventory, run nothing

set -uo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${repo_root}"

run_all=0
list_only=0
for arg in "$@"; do
  case "${arg}" in
    --all) run_all=1 ;;
    --list) list_only=1 ;;
    -h|--help)
      sed -n '2,14p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *)
      printf 'unknown argument: %s (try --help)\n' "${arg}" >&2
      exit 2
      ;;
  esac
done

declare -a results=()
failures=0

record() { results+=("$1"); }

# run_suite <name> <requirement-description> <precondition-cmd> <test-cmd...>
run_suite() {
  local name="$1"; shift
  local requirement="$1"; shift
  local precondition="$1"; shift

  if [[ "${list_only}" == "1" ]]; then
    record "LIST  ${name} — requires: ${requirement}"
    return
  fi
  if ! eval "${precondition}"; then
    record "SKIP  ${name} — unmet: ${requirement}"
    return
  fi
  printf '\n=== %s ===\n' "${name}"
  local output_file
  output_file="$(mktemp "${TMPDIR:-/tmp}/optin-suite.XXXXXX")"
  "$@" 2>&1 | tee "${output_file}"
  local status=${PIPESTATUS[0]}
  if [[ ${status} -ne 0 ]]; then
    record "FAIL  ${name}"
    failures=$((failures + 1))
  elif grep -q '^--- SKIP:' "${output_file}" && ! grep -q '^--- PASS:' "${output_file}"; then
    # `go test` exits 0 when the only test that matched skipped itself, so exit
    # status alone cannot tell "ran and passed" from "ran nothing". A suite whose
    # precondition lives inside the test — the corpus suite auto-discovers its
    # corpus — would otherwise be reported PASS having converted zero archives,
    # the opposite of the per-suite verdict this script exists to give.
    record "SKIP  ${name} — the suite skipped itself; unmet: ${requirement}"
  else
    record "PASS  ${name}"
  fi
  rm -f "${output_file}"
}

# --- Category 3: real client libraries compiled in. No external infrastructure. ---
run_suite "kafka_real (franz-go client)" \
  "nothing (compiles the real client)" "true" \
  go test -tags kafka_real -count=1 ./pkg/protocol/kafka ./pkg/dsl

run_suite "jms_solace_real (AMQP adapter)" \
  "nothing (compiles the real client)" "true" \
  go test -tags jms_solace_real -count=1 ./pkg/protocol/jms

run_suite "jms_servicebus_real (Azure Service Bus adapter)" \
  "nothing (compiles the real client)" "true" \
  go test -tags jms_servicebus_real -count=1 ./pkg/protocol/jms ./pkg/dsl

run_suite "eventhubs_real (azeventhubs adapter)" \
  "nothing (compiles the real client)" "true" \
  go test -tags eventhubs_real -count=1 ./pkg/protocol/eventhubs ./pkg/dsl

# --- Category 1: needs the pinned public LR_Scripts corpus on disk. ---
# The test itself auto-discovers the corpus and skips with instructions when absent,
# so it is always safe to invoke.
run_suite "LR_Scripts corpus smoke" \
  "a pinned LR_Scripts checkout (./scripts/fetch-lr-scripts-corpus.sh)" "true" \
  go test -count=1 -run TestPinnedLRScriptsCorpusSmoke -v ./pkg/convert/enterprisearchive

# --- Category 2: needs live infrastructure or is long-running. --all only. ---
run_suite "HA failover soak" \
  "--all" "[[ ${run_all} == 1 ]]" \
  env APEXLOAD_RUN_HA_FAILOVER_SOAK=1 go test -count=1 -run HAFailoverSoak ./pkg/api

run_suite "performance smoke (perf evidence probes)" \
  "--all" "[[ ${run_all} == 1 ]]" \
  env APEXLOAD_RUN_PERFORMANCE_SMOKE=1 go test -count=1 ./pkg/perfevidence

run_suite "live Kafka integration" \
  "--all and KAFKA_BROKERS" "[[ ${run_all} == 1 && -n \"\${KAFKA_BROKERS:-}\" ]]" \
  go test -tags kafka_integration -count=1 ./pkg/protocol/kafka

run_suite "live Solace AMQP integration" \
  "--all and SOLACE_AMQP_URL + SOLACE_QUEUE" \
  "[[ ${run_all} == 1 && -n \"\${SOLACE_AMQP_URL:-}\" && -n \"\${SOLACE_QUEUE:-}\" ]]" \
  go test -tags 'jms_solace_real jms_solace_integration' -count=1 ./pkg/protocol/jms

run_suite "live Azure Service Bus integration" \
  "--all and SERVICEBUS_CONNECTION_STRING (or SERVICEBUS_NAMESPACE)" \
  "[[ ${run_all} == 1 && ( -n \"\${SERVICEBUS_CONNECTION_STRING:-}\" || -n \"\${SERVICEBUS_NAMESPACE:-}\" ) ]]" \
  go test -tags 'jms_servicebus_real jms_servicebus_integration' -count=1 ./pkg/protocol/jms

run_suite "live Azure Event Hubs round trip" \
  "--all and EVENTHUBS_CONNECTION_STRING" \
  "[[ ${run_all} == 1 && -n \"\${EVENTHUBS_CONNECTION_STRING:-}\" ]]" \
  go test -tags eventhubs_real -count=1 -run TestEventHubsRealRoundTrip ./pkg/protocol/eventhubs

printf '\n=== opt-in suite summary ===\n'
for line in "${results[@]}"; do
  printf '%s\n' "${line}"
done

if [[ "${list_only}" == "1" ]]; then
  printf '\n(--list: nothing was run. See docs/OPTIN_TEST_SUITES.md)\n'
  exit 0
fi

if [[ ${failures} -gt 0 ]]; then
  printf '\n%d opt-in suite(s) FAILED\n' "${failures}"
  exit 1
fi
printf '\nno opt-in suite failures\n'

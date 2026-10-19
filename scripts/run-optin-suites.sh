#!/usr/bin/env bash
# Suite preconditions are single-quoted strings that run_suite evals later.
# shellcheck disable=SC2016
#
# Run the test suites that `go test ./...` excludes.
#
# The default suite skips every build-tag- and environment-gated suite. Dedicated
# automation covers PostgreSQL migration CI and the protected weekly live-protocol
# workflow; this script remains the one-command local runner, with an explicit
# per-suite verdict and a non-zero exit if any suite that ran failed.
#
# Inventory and rationale: docs/qa/TEST_PROGRAM.md
#
#   ./scripts/run-optin-suites.sh          # suites needing no external infrastructure
#   ./scripts/run-optin-suites.sh --all    # also suites needing live infrastructure
#   ./scripts/run-optin-suites.sh --list   # print the inventory, run nothing

set -uo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${repo_root}" || exit 1

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

# Each bounded Kafka qualification owns its broker and requires all genuine
# case markers. Retain its sanitized receipts at a fresh private path; an absent
# tool/fixture or failed case propagates through run_suite as FAIL, never PASS.
run_kafka_fixture() {
  local wrapper="$1"
  local report_dir
  report_dir="$(mktemp -d "${TMPDIR:-/tmp}/apexload-kafka-optin.XXXXXX")" || return 1
  printf 'Kafka qualification receipts: %s\n' "${report_dir}"
  "./scripts/${wrapper}" --report "${report_dir}/report.json"
}

# acceptance_commit prints the commit acceptance requests may name: the
# given APEXLOAD_COMMIT_SHA, or HEAD only when the code is unchanged (no
# modified file and no untracked Go source), so requests never claim a commit
# that was not the measured code. It prints nothing otherwise.
acceptance_commit() {
  if [[ -n "${APEXLOAD_COMMIT_SHA:-}" ]]; then
    printf '%s' "${APEXLOAD_COMMIT_SHA}"
  elif git diff --quiet HEAD 2>/dev/null && [[ -z "$(git ls-files --others --exclude-standard -- pkg cmd api third_party go.mod go.sum 2>/dev/null)" ]]; then
    git rev-parse HEAD 2>/dev/null
  else
    printf 'The working tree differs from HEAD (or has untracked sources): no acceptance requests are written.\n' >&2
  fi
}

# run_browser_acceptance runs the browser package with both engines required
# and the acceptance benchmark, keeping the benchmark results at a fresh
# private path.
run_browser_acceptance() {
  local out_dir commit
  out_dir="$(mktemp -d "${TMPDIR:-/tmp}/apexload-browser-acceptance.XXXXXX")" || return 1
  printf 'Browser benchmark results: %s\n' "${out_dir}"
  commit="$(acceptance_commit)"
  APEXLOAD_BROWSER_TESTS=require APEXLOAD_BROWSER_BENCHMARKS=1 APEXLOAD_BROWSER_BENCHMARK_OUT="${out_dir}" APEXLOAD_COMMIT_SHA="${commit}" \
    go test -count=1 -timeout 30m -v ./pkg/protocol/browser
}

# run_analytics_reference runs the NA-6 native analytics reference workload
# (a 1 GiB partitioned fixture by default), keeping its results and F4
# acceptance request at a fresh private path.
run_analytics_reference() {
  local out_dir commit
  out_dir="$(mktemp -d "${TMPDIR:-/tmp}/apexload-analytics-reference.XXXXXX")" || return 1
  printf 'Native analytics reference results: %s\n' "${out_dir}"
  commit="$(acceptance_commit)"
  APEXLOAD_ANALYTICS_BENCHMARK=1 APEXLOAD_ANALYTICS_BENCHMARK_OUT="${out_dir}" APEXLOAD_COMMIT_SHA="${commit}" \
    go test -tags analytics_duckdb -count=1 -timeout 60m -v -run '^TestNativeAnalyticsReferenceBenchmark$' ./pkg/api
}

# run_telemetry_reference runs the TEL-4 telemetry reference workload,
# keeping its results and F4 acceptance request at a fresh private path.
run_telemetry_reference() {
  local out_dir commit
  out_dir="$(mktemp -d "${TMPDIR:-/tmp}/apexload-telemetry-reference.XXXXXX")" || return 1
  printf 'Telemetry reference results: %s\n' "${out_dir}"
  commit="$(acceptance_commit)"
  APEXLOAD_BROWSER_TESTS=require APEXLOAD_TELEMETRY_REFERENCE=1 APEXLOAD_TELEMETRY_REFERENCE_OUT="${out_dir}" APEXLOAD_COMMIT_SHA="${commit}" \
    go test -count=1 -timeout 30m -v -run '^TestTelemetryReferenceWorkload$' ./pkg/api
}

# run_browser_intelligence_acceptance runs the BI-4 golden in Chromium and
# WebKit, keeping its results and F4 acceptance requests at a fresh private
# path.
run_browser_intelligence_acceptance() {
  local out_dir commit
  out_dir="$(mktemp -d "${TMPDIR:-/tmp}/apexload-browser-intelligence-acceptance.XXXXXX")" || return 1
  printf 'Browser intelligence golden results: %s\n' "${out_dir}"
  commit="$(acceptance_commit)"
  APEXLOAD_BROWSER_TESTS=require APEXLOAD_BI_ACCEPTANCE_OUT="${out_dir}" APEXLOAD_COMMIT_SHA="${commit}" \
    go test -count=1 -timeout 15m -v -run '^TestBrowserIntelligenceGoldenAcceptance$' ./pkg/api
}

run_suite "analytics_duckdb (embedded native analytics)" \
  "CGO-enabled Go toolchain on a supported native platform" '[[ "$(go env CGO_ENABLED)" == "1" ]]' \
  go test -tags analytics_duckdb -count=1 ./pkg/analysis/... ./pkg/api -run 'Test(Analysis|Native|Parquet|Container|Lagged|Bucket)'

run_suite "native analytics reference workload (NA-6: a 1 GiB partitioned import and timed queries, with an F4 acceptance request)" \
  "--all and a CGO-enabled Go toolchain (several GiB of scratch)" '[[ ${run_all} == 1 && "$(go env CGO_ENABLED)" == "1" ]]' \
  run_analytics_reference

run_suite "telemetry fault matrix (TEL-4: the matrix's Go tests under the race detector)" \
  "a CGO-enabled Go toolchain (the race detector)" '[[ "$(go env CGO_ENABLED)" == "1" ]]' \
  go test -race -count=1 -timeout 30m -run '^(TestArchiveSourceCoveragePersistsMissingAndRetiredSources|TestBaselineComparisonExportsCarryTelemetryCompatibility|TestBaselineTelemetryLegacyMetadataCannotImplyZeroDrops|TestBuildRunArchiveReportIncludesTelemetryHealthEvidence|TestCITelemetryVerdictComesFromTheArchiveManifest|TestCSVExportAppliesArchiveRedaction|TestClientRenderReportRefusedOnAFollower|TestClientRenderReportsRotatingTabsShareTheViewerBudget|TestClientRenderSamplesCountOnlyVisibleRenders|TestClockCalibrationExpiryJumpAndStep|TestClockCalibrationStepBetweenProbes|TestClockPipelineUnscoredIsNotHealthy|TestClockStepToleranceBelowTheTransportBudget|TestCounterDerivationsExcludeResetsEpochsMissingAndOverflow|TestDistributedDropCounterRequiresColdAssignedPeers|TestDistributedMetricsPublicationRejectsReorderedOlderAggregate|TestDistributedMetricsSequenceGapIsRetainedInTelemetry|TestDistributedNegativeDropCounterPresenceIsUnavailable|TestDistributedPublicationRetryKeepsHeldJointBatches|TestGeneratorArchiveTelemetryPreservesMeasuredZeroAvailability|TestGeneratorArchiveTelemetryRejectsNegativeCounterAvailability|TestGeneratorEpochKeeperWaitsForAdoptionReconcile|TestMetricsBrokerStampsTheProcessSourceEpoch|TestMetricsBrokerTerminalOverflowPreservesDetectableSequenceGap|TestMissingMetricWindowsAreCountedWithinOneSourceEpoch|TestProjectTelemetryPolicyUpdateChangesOnlyTheOptIn|TestStageDistributionsDiscloseALostRecorder|TestTelemetryCoverageAfterAdoption|TestTelemetryCoverageBeforeAdoptionAndForSilentSourcesIsUnknown|TestTelemetryCoverageBridgesJitterButNotLoss|TestTelemetryCoverageDoesNotRetimeReplayedWindows|TestCollectionWindowsDrainBoundariesAndConcurrency|TestCollectionWindowsRemainIndependentOfRateWindow|TestCollectionWindowsLifecycleReplayAndStopCohort|TestCollectionWindowsWireCompatibilityBoundsAndValidation|TestCollectionWindowsArchiveRetainsIdentityAndPrecision|TestCollectionWindowsCoordinatedCohortAndUnmeasuredPublication|TestTelemetryCoverageLegacyAndMissingCollectionIdentity|TestTelemetryCoverageReplayCannotBridgeTheNextFreshWindow|TestTelemetryCoverageLateSequenceDoesNotMoveCursor|TestTelemetryCoverageTerminalReplayRetiresWithoutNewWindow|TestTelemetryCoverageOlderEpochCannotRetireCurrentSource|TestTelemetryCoverageNewEpochRestartsCollectionIdentity|TestTelemetryCoverageEpochLimitDoesNotCreateUntrackedWindows|TestTelemetryCoverageAlgorithmKeepsLegacyComparisonSeparate|TestTelemetryLiveCoverageAfterAdoptionIsALowerBound|TestTelemetryLostPublicationsClampOnOverflow|TestTelemetryPolicySegmentRejectsAnInvalidPolicy|TestTelemetryStageRecorderReadoptionSamplesFromTheAdoption)$' ./pkg/api ./pkg/distributed/generatorrpc ./pkg/analysis

run_suite "telemetry reference workload (TEL-4: two generators at 2,000 observations a second with a foreground dashboard, and an F4 acceptance request)" \
  "--all, and Playwright Chromium for the foreground dashboard" "[[ ${run_all} == 1 ]]" \
  run_telemetry_reference

# --- Category 3: real client libraries compiled in. No external infrastructure. ---
run_suite "apexload_fault_probe (evacuation guard pause)" \
  "nothing (tests a tagged disposable-cluster fault hook)" "true" \
  go test -tags apexload_fault_probe -run '^TestFaultProbe' -count=1 ./pkg/api

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

run_suite "recording_e2e (real recording and replay)" \
  "--all, pinned Node/Chromium, and desktop display or Xvfb" "[[ ${run_all} == 1 ]]" \
  ./scripts/run-recording-e2e.sh

# --- Category 1: needs the pinned public LR_Scripts corpus on disk. ---
# The test itself auto-discovers the corpus and skips with instructions when absent,
# so it is always safe to invoke.
run_suite "LR_Scripts corpus smoke" \
  "a pinned LR_Scripts checkout (./scripts/fetch-lr-scripts-corpus.sh)" "true" \
  go test -count=1 -run TestPinnedLRScriptsCorpusSmoke -v ./pkg/convert/enterprisearchive

# Local Java evidence checks require explicitly supplied artifacts/JDKs.
run_suite "Java operator AppCDS archive" \
  "APEXLOAD_JAVA_CDS_TEST_JDK_HOME (HotSpot 17+ JDK)" \
  '[[ -n "${APEXLOAD_JAVA_CDS_TEST_JDK_HOME:-}" ]]' \
  go test -count=1 -v -run '^TestOperatorCDSRealArchive$' ./pkg/javarunner

run_suite "Java dedicated evidence fixtures" \
  "APEXLOAD_JAVA_EVIDENCE_FIXTURE_DIR (built fixtures.jar and lib/*.jar), java and javac" \
  '[[ -n "${APEXLOAD_JAVA_EVIDENCE_FIXTURE_DIR:-}" ]] && command -v java >/dev/null && command -v javac >/dev/null' \
  go test -count=1 -v -run '^TestJavaEvidenceFixturesThroughDedicatedHarness$' ./runners/java_vuser

run_suite "Java execution-pod local containment" \
  "APEXLOAD_JAVA_POD_DOCKER_IMAGE (local Alpine JDK21 image), Docker, Go, javac and jar" \
  '[[ -n "${APEXLOAD_JAVA_POD_DOCKER_IMAGE:-}" ]] && command -v docker >/dev/null && command -v go >/dev/null && command -v javac >/dev/null && command -v jar >/dev/null' \
  go test -count=1 -v -run '^TestDedicatedPodDockerContainment$' ./runners/java_vuser

# --- Category 2: needs live infrastructure or is long-running. --all only. ---
run_suite "HA failover soak" \
  "--all" "[[ ${run_all} == 1 ]]" \
  env APEXLOAD_RUN_HA_FAILOVER_SOAK=1 go test -count=1 -run HAFailoverSoak ./pkg/api

# Builds the controller, generator and target images, starts its own disposable
# production-posture stack (two HA controllers, three mTLS generators,
# PostgreSQL, Redis, S3), runs the suite, then removes the stack and its images.
run_suite "multiproject_integration (multi-project concurrency on a disposable Docker stack)" \
  "--all; Docker with about 6 GB for containers, 15 GB free disk, Go, Python 3 and OpenSSL" \
  "[[ ${run_all} == 1 ]]" \
  python3 scripts/multiproject/stack.py run

run_suite "performance smoke (perf evidence probes)" \
  "--all" "[[ ${run_all} == 1 ]]" \
  env APEXLOAD_RUN_PERFORMANCE_SMOKE=1 go test -count=1 ./pkg/perfevidence

run_suite "browser native HAR qualifications (real Chromium replay recording and archive acquisition)" \
  "--all and an installed Playwright Chromium (a missing runtime FAILS)" "[[ ${run_all} == 1 ]]" \
  env APEXLOAD_BROWSER_TESTS=require TZ=America/New_York go test -count=1 -timeout 15m \
    -run '^(TestBrowserReplayActualNativeHAR|TestBrowserResourceActualChromiumAcquisitionArchiveAndPagination)$' ./pkg/api

run_suite "browser acceptance (BE-4: the browser package with Chromium and WebKit required, and the acceptance benchmark)" \
  "--all and installed Playwright Chromium and WebKit (a missing runtime FAILS)" "[[ ${run_all} == 1 ]]" \
  run_browser_acceptance

run_suite "browser intelligence acceptance (BI-4: the golden page in Chromium and WebKit, with F4 acceptance requests)" \
  "--all and installed Playwright Chromium and WebKit (a missing runtime FAILS)" "[[ ${run_all} == 1 ]]" \
  run_browser_intelligence_acceptance

run_suite "root-cause v2 end to end (real local runs, about eight minutes)" \
  "--all" "[[ ${run_all} == 1 ]]" \
  env APEXLOAD_RCA_E2E=1 go test -count=1 -timeout 30m -run TestRCAEndToEnd ./pkg/api

run_suite "full PostgreSQL storage and API suites" \
  "--all and APEXLOAD_TEST_POSTGRES_URL (dedicated test database)" \
  "[[ ${run_all} == 1 && -n \"\${APEXLOAD_TEST_POSTGRES_URL:-}\" ]]" \
  go test -tags postgres_integration -p 1 -timeout=20m -count=1 ./pkg/storage ./pkg/api

# These separate profiles provision their own pinned disposable broker.
# --all is the sole opt-in; do not silently skip missing tools/required fixtures.
run_suite "Kafka owned-seek qualification (kafka_real,kafka_seek_integration)" \
  "--all; Docker, Python 3, OpenSSL, Go; isolated pinned Kafka 3.9.2 TLS/SASL PLAIN fixture" \
  "[[ ${run_all} == 1 ]]" \
  run_kafka_fixture test-kafka-owned-seek.sh

run_suite "Kafka transaction qualification (kafka_real,kafka_transaction_integration)" \
  "--all; Docker, Python 3, OpenSSL, Go; separate isolated pinned Kafka 3.9.2 TLS/SASL PLAIN fixture" \
  "[[ ${run_all} == 1 ]]" \
  run_kafka_fixture test-kafka-transactions.sh

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

run_suite "live Elasticsearch/OpenSearch vendor qualification" \
  "--all, APEXLOAD_ELASTIC_INTEGRATION=1, and dedicated disposable TLS vendor fixture credentials" \
  "[[ ${run_all} == 1 && \"\${APEXLOAD_ELASTIC_INTEGRATION:-}\" == 1 && -n \"\${APEXLOAD_ELASTIC_INTEGRATION_URL:-}\" ]]" \
  go test -count=1 -v -run '^TestVendorQualification$' ./pkg/elastic

printf '\n=== opt-in suite summary ===\n'
for line in "${results[@]}"; do
  printf '%s\n' "${line}"
done

if [[ "${list_only}" == "1" ]]; then
  printf '\n(--list: nothing was run. See docs/qa/TEST_PROGRAM.md)\n'
  exit 0
fi

if [[ ${failures} -gt 0 ]]; then
  printf '\n%d opt-in suite(s) FAILED\n' "${failures}"
  exit 1
fi

printf '\nno opt-in suite failures\n'

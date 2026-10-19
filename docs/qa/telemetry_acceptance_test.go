package qa_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Telemetry acceptance inventory (TEL-4, C8): every fault in TEL-4's matrix
// names the Go, dashboard and end-to-end tests that accept it; the matrix's
// Go tests run together, under the race detector, as the "telemetry fault
// matrix" opt-in suite; and the reference workload is described with what it
// measures. The inventory generates docs/qa/TELEMETRY_ACCEPTANCE.md and fails
// when a named test is missing, when an end-to-end spec does not run in both
// authentication profiles, or when the suite's test selection leaves out a
// listed Go test.

const telemetryAcceptancePath = "TELEMETRY_ACCEPTANCE.md"

type telemetryAcceptanceItem struct {
	Fault string
	// Go are "<package dir>:<TestName>"; Dashboard are
	// "<file under dashboard/src>:<it title>"; EndToEnd are
	// "<spec>:<test title>".
	Go, Dashboard, EndToEnd []string
}

func telemetryAcceptanceInventory() []telemetryAcceptanceItem {
	return []telemetryAcceptanceItem{
		{Fault: "Legacy, nil, zero, negative, overflowing and reset counters", Go: []string{
			"pkg/api:TestBaselineTelemetryLegacyMetadataCannotImplyZeroDrops", "pkg/distributed/generatorrpc:TestGeneratorArchiveTelemetryPreservesMeasuredZeroAvailability",
			"pkg/api:TestDistributedNegativeDropCounterPresenceIsUnavailable", "pkg/distributed/generatorrpc:TestGeneratorArchiveTelemetryRejectsNegativeCounterAvailability",
			"pkg/api:TestTelemetryLostPublicationsClampOnOverflow", "pkg/distributed/generatorrpc:TestMetricsBrokerTerminalOverflowPreservesDetectableSequenceGap",
			"pkg/analysis:TestCounterDerivationsExcludeResetsEpochsMissingAndOverflow"}},
		{Fault: "Cold and missing peers", Go: []string{"pkg/api:TestDistributedDropCounterRequiresColdAssignedPeers", "pkg/api:TestArchiveSourceCoveragePersistsMissingAndRetiredSources", "pkg/api:TestTelemetryCoverageBeforeAdoptionAndForSilentSourcesIsUnknown"}},
		{Fault: "Out-of-order and retried publications", Go: []string{"pkg/api:TestDistributedMetricsPublicationRejectsReorderedOlderAggregate", "pkg/api:TestDistributedPublicationRetryKeepsHeldJointBatches", "pkg/api:TestDistributedMetricsSequenceGapIsRetainedInTelemetry", "pkg/api:TestTelemetryCoverageBridgesJitterButNotLoss", "pkg/api:TestTelemetryCoverageDoesNotRetimeReplayedWindows", "pkg/api:TestTelemetryCoverageLegacyAndMissingCollectionIdentity", "pkg/api:TestTelemetryCoverageReplayCannotBridgeTheNextFreshWindow", "pkg/api:TestTelemetryCoverageLateSequenceDoesNotMoveCursor", "pkg/api:TestTelemetryCoverageTerminalReplayRetiresWithoutNewWindow"}},
		{Fault: "Ownership and source epochs", Go: []string{"pkg/distributed/generatorrpc:TestMetricsBrokerStampsTheProcessSourceEpoch", "pkg/api:TestMissingMetricWindowsAreCountedWithinOneSourceEpoch", "pkg/api:TestGeneratorEpochKeeperWaitsForAdoptionReconcile", "pkg/api:TestTelemetryCoverageOlderEpochCannotRetireCurrentSource", "pkg/api:TestTelemetryCoverageNewEpochRestartsCollectionIdentity", "pkg/api:TestTelemetryCoverageEpochLimitDoesNotCreateUntrackedWindows", "pkg/api:TestTelemetryCoverageAlgorithmKeepsLegacyComparisonSeparate"}},
		{Fault: "Source collection windows and rate denominators", Go: []string{"pkg/distributed/generatorrpc:TestCollectionWindowsDrainBoundariesAndConcurrency", "pkg/distributed/generatorrpc:TestCollectionWindowsRemainIndependentOfRateWindow", "pkg/distributed/generatorrpc:TestCollectionWindowsLifecycleReplayAndStopCohort", "pkg/distributed/generatorrpc:TestCollectionWindowsWireCompatibilityBoundsAndValidation", "pkg/distributed/generatorrpc:TestCollectionWindowsArchiveRetainsIdentityAndPrecision", "pkg/distributed/generatorrpc:TestCollectionWindowsCoordinatedCohortAndUnmeasuredPublication"}},
		{Fault: "Durable controller cursors and source-window recovery", Go: []string{"pkg/api:TestTelemetryCheckpointRoundTripAndReplay", "pkg/api:TestTelemetryCheckpointRejectsIncompatibleEvidence", "pkg/api:TestTelemetryCheckpointDurableRestartAndAtomicFailure", "pkg/api:TestTelemetryCheckpointRetentionAndArchiveCopies", "pkg/api:TestTelemetryCheckpointArchiveRetainsRecoveredWindows", "pkg/api:TestTelemetryCheckpointLegacyAndAmbiguousCommit", "pkg/api:TestTelemetryCheckpointAdoptionDoesNotOverwriteNewerEffects", "pkg/storage:TestTelemetryCheckpointFencesAndOrdering", "pkg/storage:TestTelemetryCheckpointMigrationAndRestart"}},
		{Fault: "Policy changes", Go: []string{"pkg/api:TestTelemetryPolicySegmentRejectsAnInvalidPolicy", "pkg/api:TestProjectTelemetryPolicyUpdateChangesOnlyTheOptIn"},
			EndToEnd: []string{"workflow-telemetry-policy.spec.ts:telemetry policy remains immutable in live, archive and manifest views after settings change", "workflow-telemetry-policy.spec.ts:telemetry manifest shows legacy absence and refuses malformed policy without inferred budgets"}},
		{Fault: "Clock skew and steps", Go: []string{"pkg/api:TestClockCalibrationExpiryJumpAndStep", "pkg/api:TestClockCalibrationStepBetweenProbes", "pkg/api:TestClockPipelineUnscoredIsNotHealthy", "pkg/api:TestClockStepToleranceBelowTheTransportBudget"}},
		{Fault: "Browser background tabs, render stalls and reconnects", Go: []string{"pkg/api:TestClientRenderSamplesCountOnlyVisibleRenders", "pkg/api:TestClientRenderReportsRotatingTabsShareTheViewerBudget", "pkg/api:TestClientRenderReportRefusedOnAFollower"},
			Dashboard: []string{
				"components/clientRenderReporter.test.tsx:measures receive to render and marks a hidden tab as background",
				"components/clientRenderReporter.test.tsx:counts a render as visible only if the tab stayed visible since receipt",
				"components/clientRenderReporter.test.tsx:reports a render whose tab was hidden before it painted as background",
				"components/clientRenderReporter.test.tsx:does not measure a publication received while the tab is hidden",
				"components/useExecutionMetricsStream.test.tsx:seeds history on first open and backfills only the missed window on reconnect"},
			EndToEnd: []string{"workflow-telemetry-stages.spec.ts:an opted-in project collects visible render evidence as an advisory stage"}},
		{Fault: "HA adoption gaps", Go: []string{"pkg/api:TestTelemetryCoverageAfterAdoption", "pkg/api:TestTelemetryLiveCoverageAfterAdoptionIsALowerBound", "pkg/api:TestTelemetryStageRecorderReadoptionSamplesFromTheAdoption", "pkg/api:TestStageDistributionsDiscloseALostRecorder"}},
		{Fault: "Exports", Go: []string{"pkg/api:TestBaselineComparisonExportsCarryTelemetryCompatibility", "pkg/api:TestBuildRunArchiveReportIncludesTelemetryHealthEvidence", "pkg/api:TestCITelemetryVerdictComesFromTheArchiveManifest", "pkg/api:TestCSVExportAppliesArchiveRedaction"},
			EndToEnd: []string{"workflow-telemetry-reevaluation.spec.ts:runs re-evaluate reproducibly under a common telemetry policy, from the API and the comparison view"}},
	}
}

// telemetryFaultMatrixPackages are the packages the fault matrix suite runs.
var telemetryFaultMatrixPackages = []string{"pkg/api", "pkg/distributed/generatorrpc", "pkg/analysis", "pkg/storage"}

func renderTelemetryAcceptance(t *testing.T) string {
	var out strings.Builder
	out.WriteString("# Telemetry acceptance\n\n")
	out.WriteString("Generated by `docs/qa/telemetry_acceptance_test.go` (TEL-4). Regenerate with `APEXLOAD_UPDATE_TELEMETRY_ACCEPTANCE=1 go test ./docs/qa -run TestTelemetryAcceptanceInventory`.\n\n")
	out.WriteString("Each fault in TEL-4's matrix names the tests that accept it. The Go tests run in the default build; `scripts/run-optin-suites.sh` runs them together under the race detector as the \"telemetry fault matrix\" suite. Dashboard tests run in `npm test`; end-to-end specs run in both authentication profiles.\n\n")
	out.WriteString("| TEL-4 fault | Test | Where |\n| --- | --- | --- |\n")
	config, err := os.ReadFile(filepath.Join("..", "..", "dashboard", "playwright.config.ts"))
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range telemetryAcceptanceInventory() {
		first := true
		row := func(test, where string) {
			fault := ""
			if first {
				fault, first = item.Fault, false
			}
			out.WriteString(fmt.Sprintf("| %s | %s | %s |\n", fault, test, where))
		}
		for _, ref := range item.Go {
			file, _, found := browserAcceptanceTestBody(t, ref)
			if !found {
				t.Errorf("%s: test %s does not exist", item.Fault, ref)
				continue
			}
			dir, name, _ := strings.Cut(ref, ":")
			row(fmt.Sprintf("`%s` (`%s/%s`)", name, dir, file), "Go")
		}
		for _, ref := range item.Dashboard {
			file, title, _ := strings.Cut(ref, ":")
			source, err := os.ReadFile(filepath.Join("..", "..", "dashboard", "src", filepath.FromSlash(file)))
			if err != nil || !strings.Contains(string(source), "it('"+title+"'") {
				t.Errorf("%s: dashboard test %q is not in %s (%v)", item.Fault, title, file, err)
			}
			row(fmt.Sprintf("%s (`dashboard/src/%s`)", title, file), "Dashboard")
		}
		for _, ref := range item.EndToEnd {
			spec, title, _ := strings.Cut(ref, ":")
			source, err := os.ReadFile(filepath.Join("..", "..", "dashboard", "e2e", spec))
			if err != nil || !strings.Contains(string(source), "test('"+title+"'") {
				t.Errorf("%s: Playwright test %q is not in %s (%v)", item.Fault, title, spec, err)
			}
			if !strings.Contains(string(config), "'**/"+spec+"'") {
				t.Errorf("%s: %s does not run in the local-auth profile", item.Fault, spec)
			}
			if browserAcceptanceProfileSkip(string(source), title) {
				t.Errorf("%s: %q skips by profile", item.Fault, title)
			}
			row(fmt.Sprintf("%s (`dashboard/e2e/%s`)", title, spec), "End to end, both profiles")
		}
	}
	out.WriteString("\n## Reference workload\n\n`APEXLOAD_TELEMETRY_REFERENCE=1 go test ./pkg/api -run '^TestTelemetryReferenceWorkload$' -v` runs the TEL-4 reference workload in one process: two declared generators (real generator services reached over in-memory gRPC) each run half of `APEXLOAD_TELEMETRY_REFERENCE_RATE` (default 2,000) accepted observations a second against a local target for `APEXLOAD_TELEMETRY_REFERENCE_SECONDS` (default 60), publishing every second. While it runs, a foreground dashboard (headless Chromium on the run's live page, reporting its render evidence) watches it, and archive analyses of an earlier run are read every second. It records each stage's P50/P95/P99 and maximum, the run's coverage and loss accounting, the target's own count of served requests against the archive's samples (unreported loss), clock uncertainty, process CPU and peak RSS, and every failure.\n\n")
	out.WriteString("Stage percentiles are never added. The sum of the sample-age and browser-render maxima is recorded, but it leaves out delivery from the controller's publication to the browser's receipt, so it does not bound capture to visible render. Loss is accounted per path: the archive's samples, and separately the run's live totals (successes and recorded errors) plus its dropped-results counter, must account for every served request but those aborted in flight at the stop (at most one per VUser); a counter that is only a lower bound is noted. Lost publications and dropped subscriber batches are counted in publications, and a dropped batch is also a lost sequence: each lost publication may account for at most the largest window one generator's group published, and a shortfall within that is reported loss of unknown size, classified `live_publications_lost`. A run fails with unreported loss on either path, more results than were served on either path (duplicates), no sample-age P99 (an unqualified clock is classified `clock_unqualified`), a sample-age P99 above 2,000 ms, no browser-render evidence, an accepted rate under 90% of the workload (archived samples over the load window, first to last sample), a failed analysis, or a stop before it was measured; render alone over the target is classified `slow_browser`. With `APEXLOAD_TELEMETRY_REFERENCE_OUT` it writes `telemetry-reference.json`, and, when `APEXLOAD_COMMIT_SHA` names the measured commit, one F4 request (`TEL-4`, `telemetry-reference`): `pending` without failures, `failed` otherwise, never `passed` by itself. `scripts/run-optin-suites.sh --all` runs it as the \"telemetry reference workload\" suite. It does not exercise collector traffic, an overload trial, generators on other hosts or native (DuckDB) analytics, it does not record queue or byte volumes, and it is not the 4 vCPU / 16 GiB reference controller.\n")
	return out.String()
}

func TestTelemetryAcceptanceInventory(t *testing.T) {
	want := renderTelemetryAcceptance(t)
	if t.Failed() {
		return
	}
	if os.Getenv("APEXLOAD_UPDATE_TELEMETRY_ACCEPTANCE") == "1" {
		if err := os.WriteFile(telemetryAcceptancePath, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(telemetryAcceptancePath)
	if err != nil || string(got) != want {
		t.Fatalf("%s is stale or missing (%v); regenerate with APEXLOAD_UPDATE_TELEMETRY_ACCEPTANCE=1", telemetryAcceptancePath, err)
	}
	runner, err := os.ReadFile(filepath.Join("..", "..", "scripts", "run-optin-suites.sh"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(runner)
	for _, suite := range []string{`run_suite "telemetry fault matrix`, `run_suite "telemetry reference workload`} {
		if !strings.Contains(script, suite) {
			t.Errorf("scripts/run-optin-suites.sh does not register %s", suite)
		}
	}
	// The fault matrix suite's own command selects every listed Go test and
	// runs its package.
	_, suite, _ := strings.Cut(script, `run_suite "telemetry fault matrix`)
	suite, _, _ = strings.Cut(suite, "\n\n")
	selected := map[string]bool{}
	if match := regexp.MustCompile(`-run '\^\(([A-Za-z0-9_|]+)\)\$'`).FindStringSubmatch(suite); match != nil {
		for _, name := range strings.Split(match[1], "|") {
			selected[name] = true
		}
	}
	packages := map[string]bool{}
	for _, field := range strings.Fields(suite) {
		if strings.HasPrefix(field, "./") {
			packages[strings.TrimPrefix(field, "./")] = true
		}
	}
	for _, item := range telemetryAcceptanceInventory() {
		for _, ref := range item.Go {
			dir, name, _ := strings.Cut(ref, ":")
			if !selected[name] {
				t.Errorf("the telemetry fault matrix suite does not select %s", name)
			}
			found := false
			for _, pkg := range telemetryFaultMatrixPackages {
				found = found || pkg == dir
			}
			if !found || !packages[dir] {
				t.Errorf("the telemetry fault matrix suite does not run package %s", dir)
			}
		}
	}
}

package api

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func acceptanceRatio(value float64) *float64 { return &value }

func acceptanceManifest(definition TelemetryAcceptancePolicyDefinition) *RunArchiveManifest {
	pin := RunPolicyPin{Kind: storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, ID: "gate", Name: "Gate", Revision: 3, SHA256: "abc", Source: "project_default"}
	manifest := &RunArchiveManifest{TransactionCount: 1000}
	manifest.Provenance.PolicyPins = []RunPolicyPin{pin}
	manifest.Provenance.TelemetryAcceptancePolicy = &TelemetryAcceptancePolicySnapshot{Pin: pin, Definition: definition}
	return manifest
}

func coverageRecord(observed, window int64) *TelemetryCoverageRecord {
	return &TelemetryCoverageRecord{Coverage: TelemetryCoverage{WindowMs: window, ObservedMs: observed, UnknownMs: window - observed,
		Sources: []TelemetrySourceCoverage{{Source: "generator-a", ObservedMs: observed}}}}
}

func TestTelemetryAcceptanceCoverageBelowTheFloorFailsWithTheReason(t *testing.T) {
	manifest := acceptanceManifest(TelemetryAcceptancePolicyDefinition{Version: 1, Mode: "fail_ci", MinWallTimeCoverage: acceptanceRatio(0.9)})
	manifest.TelemetryCoverage = coverageRecord(70_000, 100_000)
	verdict := telemetryAcceptanceForManifest(manifest)
	if verdict == nil || verdict.Outcome != TelemetryAcceptanceFail || verdict.Mode != "fail_ci" || verdict.Policy == nil || verdict.Policy.Revision != 3 {
		t.Fatalf("verdict = %+v", verdict)
	}
	criterion := verdict.Criteria[0]
	if criterion.Observed == nil || *criterion.Observed != 0.7 || criterion.Bound != telemetryAcceptanceExact || *criterion.Numerator != 70_000 || *criterion.Denominator != 100_000 {
		t.Fatalf("criterion = %+v", criterion)
	}
	if len(verdict.Reasons) != 1 || !strings.Contains(verdict.Reasons[0], "70.0%") || !strings.Contains(verdict.Reasons[0], "90.0%") {
		t.Fatalf("reasons = %q", verdict.Reasons)
	}
	// A truncated (lower-bound) observation below the floor cannot fail it.
	manifest.TelemetryCoverage.Coverage.Truncated = true
	if verdict := telemetryAcceptanceForManifest(manifest); verdict.Outcome != TelemetryAcceptanceInconclusive || verdict.Criteria[0].Bound != telemetryAcceptanceLowerBound {
		t.Fatalf("lower-bound coverage = %+v", verdict)
	}
	manifest.TelemetryCoverage = coverageRecord(95_000, 100_000)
	if verdict := telemetryAcceptanceForManifest(manifest); verdict.Outcome != TelemetryAcceptancePass || len(verdict.Reasons) != 0 {
		t.Fatalf("covered run = %+v", verdict)
	}
}

func TestTelemetryAcceptanceUnknownRequiredEvidenceIsInconclusive(t *testing.T) {
	maxLost := int64(0)
	manifest := acceptanceManifest(TelemetryAcceptancePolicyDefinition{Version: 1, Mode: "fail_ci", MinWallTimeCoverage: acceptanceRatio(0.9), MaxLostRecords: &maxLost,
		MaxBreachedKnownTimeFraction: acceptanceRatio(0.1), StageLagPercentiles: []TelemetryLagPercentileRule{{Stage: "end_to_end", Percentile: "p99", MaxMs: 2000}, {Stage: "transport", Percentile: "p95", MaxMs: 500}}})
	verdict := telemetryAcceptanceForManifest(manifest)
	if verdict.Outcome != TelemetryAcceptanceInconclusive || len(verdict.Criteria) != 5 {
		t.Fatalf("verdict = %+v", verdict)
	}
	for _, criterion := range verdict.Criteria {
		if criterion.Outcome != TelemetryAcceptanceInconclusive || criterion.Reason == "" || criterion.Observed != nil {
			t.Fatalf("criterion without evidence = %+v", criterion)
		}
	}
	// One failed criterion fails the verdict even when others are unknown.
	manifest.TelemetryStageDistributions = []TelemetryStageDistribution{{Stage: telemetryStageTransport, Availability: "measured", Samples: 40, P95Ms: acceptanceRatio(900)}}
	verdict = telemetryAcceptanceForManifest(manifest)
	if verdict.Outcome != TelemetryAcceptanceFail || verdict.Criteria[4].Outcome != TelemetryAcceptanceFail || !strings.Contains(verdict.Criteria[4].Reason, "900 ms exceeds 500 ms") {
		t.Fatalf("stage failure = %+v", verdict)
	}
}

func TestTelemetryAcceptanceLossIsExactOnlyWithoutLostBatches(t *testing.T) {
	maxLost := int64(10)
	definition := TelemetryAcceptancePolicyDefinition{Version: 1, MaxLostRecords: &maxLost, MaxLossRatio: acceptanceRatio(0.01)}
	exact := func(value int64) *TelemetryCounterTotal {
		return &TelemetryCounterTotal{State: TelemetryCounterExact, Value: value}
	}
	tests := []struct {
		name        string
		loss        *TelemetryLossAccounting
		records     int64
		lost, ratio string
	}{
		{name: "exact within limits", loss: &TelemetryLossAccounting{DroppedResults: exact(5), LostPublications: exact(0)}, records: 995, lost: TelemetryAcceptancePass, ratio: TelemetryAcceptancePass},
		{name: "a lost publication makes the count a lower bound", loss: &TelemetryLossAccounting{DroppedResults: exact(5), LostPublications: exact(1)}, records: 995, lost: TelemetryAcceptanceInconclusive, ratio: TelemetryAcceptanceInconclusive},
		{name: "a lower bound over the ceiling fails", loss: &TelemetryLossAccounting{DroppedResults: &TelemetryCounterTotal{State: TelemetryCounterLowerBound, Value: 20}}, records: 980, lost: TelemetryAcceptanceFail, ratio: TelemetryAcceptanceFail},
		{name: "unavailable dropped results", loss: &TelemetryLossAccounting{DroppedResults: &TelemetryCounterTotal{State: TelemetryCounterUnavailable, Reason: "ledger_lost"}}, records: 1000, lost: TelemetryAcceptanceInconclusive, ratio: TelemetryAcceptanceInconclusive},
		{name: "no loss accounting", records: 1000, lost: TelemetryAcceptanceInconclusive, ratio: TelemetryAcceptanceInconclusive},
		{name: "shed arrivals are not telemetry loss", loss: &TelemetryLossAccounting{DroppedResults: exact(0), ShedArrivals: exact(5000)}, records: 1000, lost: TelemetryAcceptancePass, ratio: TelemetryAcceptancePass},
		{name: "no records to measure against", loss: &TelemetryLossAccounting{DroppedResults: exact(0)}, records: 0, lost: TelemetryAcceptancePass, ratio: TelemetryAcceptanceInconclusive},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			verdict := evaluateTelemetryAcceptance(definition, nil, telemetryAcceptanceEvidence{loss: test.loss, records: test.records})
			if verdict.Criteria[0].Outcome != test.lost || verdict.Criteria[1].Outcome != test.ratio {
				t.Fatalf("lost %+v ratio %+v", verdict.Criteria[0], verdict.Criteria[1])
			}
		})
	}
	verdict := evaluateTelemetryAcceptance(definition, nil, telemetryAcceptanceEvidence{loss: &TelemetryLossAccounting{DroppedResults: exact(20)}, records: 980})
	if ratio := verdict.Criteria[1]; ratio.Observed == nil || *ratio.Observed != 0.02 || *ratio.Denominator != 1000 || !strings.Contains(ratio.Reason, "20 of 1000 records") {
		t.Fatalf("loss ratio criterion = %+v", ratio)
	}
}

func TestTelemetryAcceptanceBreachedKnownTimeUsesClassifiedTime(t *testing.T) {
	manifest := acceptanceManifest(TelemetryAcceptancePolicyDefinition{Version: 1, MaxBreachedKnownTimeFraction: acceptanceRatio(0.05)})
	manifest.TelemetryCoverage = coverageRecord(100_000, 100_000)
	manifest.TelemetryCoverage.ClassifiedMs, manifest.TelemetryCoverage.BreachedMs = 50_000, 5_000
	manifest.TelemetryCoverage.BreachedKnownFraction = acceptanceRatio(0.1)
	verdict := telemetryAcceptanceForManifest(manifest)
	criterion := verdict.Criteria[0]
	if verdict.Outcome != TelemetryAcceptanceFail || *criterion.Numerator != 5_000 || *criterion.Denominator != 50_000 || !strings.Contains(criterion.Reason, "5000 of 50000 classified ms") {
		t.Fatalf("breach criterion = %+v", criterion)
	}
	// The mode is advisory by default and is reported as such.
	if verdict.Mode != "advisory" {
		t.Fatalf("mode = %q", verdict.Mode)
	}
}

func TestTelemetryAcceptanceWithoutAFrozenDefinitionIsUnknownMode(t *testing.T) {
	manifest := acceptanceManifest(TelemetryAcceptancePolicyDefinition{Version: 1, Mode: "fail_ci", MinWallTimeCoverage: acceptanceRatio(0.9)})
	manifest.Provenance.TelemetryAcceptancePolicy = nil
	verdict := telemetryAcceptanceForManifest(manifest)
	if verdict.Outcome != TelemetryAcceptanceInconclusive || verdict.Mode != telemetryAcceptanceModeUnknown || verdict.Policy == nil {
		t.Fatalf("legacy pin = %+v", verdict)
	}
	// A frozen definition that no longer matches its pin is not trusted.
	manifest = acceptanceManifest(TelemetryAcceptancePolicyDefinition{Version: 1, Mode: "fail_ci", MinWallTimeCoverage: acceptanceRatio(0.9)})
	manifest.Provenance.TelemetryAcceptancePolicy.Pin.SHA256 = "other"
	if verdict := telemetryAcceptanceForManifest(manifest); verdict.Mode != telemetryAcceptanceModeUnknown {
		t.Fatalf("mismatched snapshot = %+v", verdict)
	}
	manifest.Provenance.PolicyPins = nil
	if verdict := telemetryAcceptanceForManifest(manifest); verdict != nil {
		t.Fatalf("a run without a pin has a verdict: %+v", verdict)
	}
}

func TestCITelemetryVerdictMapping(t *testing.T) {
	verdict := func(outcome, mode string) *ciTelemetryVerdict {
		return &ciTelemetryVerdict{TelemetryAcceptanceVerdict: TelemetryAcceptanceVerdict{Outcome: outcome, Mode: mode, Policy: &RunPolicyPin{Name: "Gate", Revision: 2},
			Reasons: []string{"wall-time coverage 70.0% is below the 90.0% floor"}}}
	}
	tests := []struct {
		name, outcome, mode, inconclusive string
		breached                          bool
		want                              string
		enforced                          bool
	}{
		{name: "advisory fail never changes the outcome", outcome: TelemetryAcceptanceFail, mode: "advisory", want: "passed"},
		{name: "advisory inconclusive never changes the outcome", outcome: TelemetryAcceptanceInconclusive, mode: "advisory", want: "passed"},
		{name: "unknown mode never changes the outcome", outcome: TelemetryAcceptanceInconclusive, mode: telemetryAcceptanceModeUnknown, want: "passed"},
		{name: "fail_ci fail fails", outcome: TelemetryAcceptanceFail, mode: "fail_ci", want: ciOutcomeTelemetryFailed, enforced: true},
		{name: "fail_ci pass passes", outcome: TelemetryAcceptancePass, mode: "fail_ci", want: "passed"},
		{name: "inconclusive without a mapping is not a pass", outcome: TelemetryAcceptanceInconclusive, mode: "fail_ci", want: ciOutcomeTelemetryInconclusive, enforced: true},
		{name: "inconclusive mapped to pass", outcome: TelemetryAcceptanceInconclusive, mode: "fail_ci", inconclusive: ciTelemetryInconclusivePass, want: "passed"},
		{name: "inconclusive mapped to fail", outcome: TelemetryAcceptanceInconclusive, mode: "fail_ci", inconclusive: ciTelemetryInconclusiveFail, want: ciOutcomeTelemetryFailed, enforced: true},
		{name: "an SLA breach stays the outcome", outcome: TelemetryAcceptanceFail, mode: "fail_ci", breached: true, want: "sla_breached"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := ciRunResponse{Status: "COMPLETE", SLAEvaluated: true, SLABreached: test.breached, ResultsPresent: true, TelemetryVerdict: verdict(test.outcome, test.mode)}
			finalizeCIRunOutcome(&response, ciRunMetadata{TelemetryInconclusive: test.inconclusive})
			if response.Outcome != test.want || response.TelemetryVerdict.Enforced != test.enforced || response.TelemetryVerdict.InconclusivePolicy != test.inconclusive {
				t.Fatalf("outcome %q enforced %t, want %q %t", response.Outcome, response.TelemetryVerdict.Enforced, test.want, test.enforced)
			}
			if test.enforced && (!strings.Contains(response.Error, "70.0%") || !strings.Contains(response.Error, `"Gate" revision 2`)) {
				t.Fatalf("enforced verdict error %q does not name the policy and reason", response.Error)
			}
			summary := ciGitHubCheckSummary(nil, response, ciRunMetadata{Source: ciTestRunSource("1")})
			if !strings.Contains(summary, "Telemetry acceptance (separate from the application verdict)") || !strings.Contains(summary, "- Verdict: `"+test.outcome+"`") {
				t.Fatalf("check summary does not list the telemetry verdict separately: %s", summary)
			}
			if test.enforced && ciGitHubCheckConclusion(response) != "failure" {
				t.Fatalf("an enforced verdict concluded %q", ciGitHubCheckConclusion(response))
			}
		})
	}
}

func TestCITelemetryVerdictComesFromTheArchiveManifest(t *testing.T) {
	manifest := acceptanceManifest(TelemetryAcceptancePolicyDefinition{Version: 1, Mode: "fail_ci", MinWallTimeCoverage: acceptanceRatio(0.9)})
	manifest.TelemetryCoverage = coverageRecord(70_000, 100_000)
	response := ciRunResponse{Status: "COMPLETE", SLAEvaluated: true}
	applyCITerminalEvidence(&response, &ciValidatedArchiveEvidence{Manifest: manifest, TelemetryVerdict: telemetryAcceptanceForManifest(manifest)}, nil)
	response.ResultsPresent = response.Transactions > 0
	finalizeCIRunOutcome(&response, ciRunMetadata{})
	if response.Outcome != ciOutcomeTelemetryFailed || response.TelemetryVerdict == nil || !response.TelemetryVerdict.Enforced {
		t.Fatalf("outcome %q verdict %+v", response.Outcome, response.TelemetryVerdict)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	telemetry, _ := wire["telemetry_verdict"].(map[string]any)
	if telemetry["outcome"] != TelemetryAcceptanceFail || telemetry["mode"] != "fail_ci" || telemetry["enforced"] != true || telemetry["criteria"] == nil {
		t.Fatalf("wire telemetry_verdict = %s", encoded)
	}
}

func TestCreateCIRunRequiresAnInconclusiveMappingUnderAFailCIPolicy(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	policy := ciRunSourceTestPolicy()
	policy.MaxDurationMinutes = 1
	policy.AllowedTargetHosts = []string{"127.0.0.1"}
	server, store := newCIContractTestServer(t, policy)
	publishCheck := false
	scriptRaw, err := json.Marshal(ciTestScript(target.URL))
	if err != nil {
		t.Fatal(err)
	}
	invoke := func(choice, key string) (int, string) {
		t.Helper()
		request := ciRunCreateRequest{ProjectID: "project-ci", ScriptPath: "tests/performance.json", Script: scriptRaw,
			Execution: ciRunExecutionRequest{VUsers: 1, Duration: "50ms"}, Source: ciTestRunSource("1"), PublishCheck: &publishCheck, TelemetryInconclusive: choice}
		body, _ := json.Marshal(request)
		httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/ci/runs", bytes.NewReader(body))
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Idempotency-Key", key)
		recorder := httptest.NewRecorder()
		server.handleCreateCIRun(recorder, httpReq)
		return recorder.Code, recorder.Body.String()
	}
	if status, body := invoke("maybe", "octo/apex:performance:9001:1:telemetry-invalid"); status != http.StatusBadRequest || !strings.Contains(body, "telemetry_inconclusive must be pass or fail") {
		t.Fatalf("invalid mapping = %d %s", status, body)
	}
	seedPolicy(t, store, "project-ci", "gate", storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, `{"version":1,"apply_by_default":true,"mode":"fail_ci","min_wall_time_coverage":0.9}`)
	status, body := invoke("", "octo/apex:performance:9001:1:telemetry-none")
	if status != http.StatusBadRequest || !strings.Contains(body, "fail_ci") || !strings.Contains(body, "telemetry_inconclusive") {
		t.Fatalf("fail_ci without a mapping = %d %s", status, body)
	}
	status, body = invoke("FAIL", "octo/apex:performance:9001:1:telemetry-fail")
	if status != http.StatusAccepted {
		t.Fatalf("fail_ci with a mapping = %d %s", status, body)
	}
	var created ciRunResponse
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatal(err)
	}
	test, _ := server.runner.GetTest(created.TestID)
	metadata, err := decodeCIRunMetadata(test.MaterializationKey)
	if err != nil || metadata.TelemetryInconclusive != ciTelemetryInconclusiveFail {
		t.Fatalf("accepted metadata = %+v %v", metadata, err)
	}
}

func TestCreateCIRunAcceptsAnAdvisoryPolicyWithoutAMapping(t *testing.T) {
	server, store := newCIContractTestServer(t, ciRunSourceTestPolicy())
	seedPolicy(t, store, "project-ci", "gate", storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, `{"version":1,"apply_by_default":true,"min_wall_time_coverage":0.9}`)
	if status, message := server.ciTelemetryInconclusiveRequirement("project-ci", ""); status != 0 {
		t.Fatalf("advisory default = %d %s", status, message)
	}
	seedPolicy(t, store, "project-ci", "second", storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, `{"version":1,"apply_by_default":true,"mode":"fail_ci","max_loss_ratio":0}`)
	if status, _ := server.ciTelemetryInconclusiveRequirement("project-ci", ""); status != http.StatusConflict {
		t.Fatalf("ambiguous defaults = %d", status)
	}
}

func TestPersistTelemetryLossRecordsTheRunTotalsOnce(t *testing.T) {
	runner := &TestRunner{tests: map[string]*ManagedTest{}}
	started := time.Now()
	local := &ManagedTest{ID: "local", StartedAt: &started, LiveMetrics: &LiveMetrics{Telemetry: &TelemetryHealth{Loss: localTelemetryLossTotals(3, 0)}}}
	manifest := &RunArchiveManifest{}
	runner.persistTelemetryLoss(local, manifest, false)
	if manifest.TelemetryLoss == nil || manifest.TelemetryLoss.DroppedResults.Value != 3 || manifest.TelemetryLoss.DroppedResults.State != TelemetryCounterExact {
		t.Fatalf("local loss = %+v", manifest.TelemetryLoss)
	}
	// Written once: a later step never replaces it.
	local.LiveMetrics.Telemetry.Loss = localTelemetryLossTotals(9, 0)
	runner.persistTelemetryLoss(local, manifest, false)
	if manifest.TelemetryLoss.DroppedResults.Value != 3 {
		t.Fatalf("loss rewritten: %+v", manifest.TelemetryLoss.DroppedResults)
	}
	// A distributed run finalized without its ledger cannot pass on loss.
	distributed := &ManagedTest{ID: "distributed", StartedAt: &started}
	manifest = &RunArchiveManifest{}
	manifest.Provenance.GeneratorShards = []GeneratorShardSummary{{Target: "generator-a"}}
	runner.persistTelemetryLoss(distributed, manifest, false)
	if manifest.TelemetryLoss == nil || manifest.TelemetryLoss.DroppedResults.State != TelemetryCounterUnavailable || manifest.TelemetryLoss.DroppedResults.Reason != "ledger_lost" {
		t.Fatalf("ledgerless loss = %+v", manifest.TelemetryLoss)
	}
	// A local run whose runtime resources ran in the controller is not
	// distributed: with no final snapshot it records no loss.
	manifest = &RunArchiveManifest{}
	manifest.Provenance.GeneratorShards = []GeneratorShardSummary{{Target: localRuntimeResourceTarget}}
	runner.persistTelemetryLoss(&ManagedTest{ID: "local-resources", StartedAt: &started}, manifest, false)
	if manifest.TelemetryLoss != nil {
		t.Fatalf("a local runtime-resource run was treated as distributed: %+v", manifest.TelemetryLoss)
	}
	// An adjunct refresh never writes ledger_lost into an older archive.
	manifest = &RunArchiveManifest{}
	manifest.Provenance.GeneratorShards = []GeneratorShardSummary{{Target: "generator-a"}}
	runner.persistTelemetryLoss(distributed, manifest, true)
	if manifest.TelemetryLoss != nil {
		t.Fatalf("refresh wrote loss: %+v", manifest.TelemetryLoss)
	}
}

func TestLiveTelemetryAcceptanceIsProvisional(t *testing.T) {
	definition := TelemetryAcceptancePolicyDefinition{Version: 1, Mode: "fail_ci", MinWallTimeCoverage: acceptanceRatio(0.9), MaxBreachedKnownTimeFraction: acceptanceRatio(0.1)}
	pin := RunPolicyPin{Kind: storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, ID: "gate", Revision: 1, SHA256: "abc"}
	test := &ManagedTest{initialArchiveProvenance: &RunArchiveProvenance{PolicyPins: []RunPolicyPin{pin}, TelemetryAcceptancePolicy: &TelemetryAcceptancePolicySnapshot{Pin: pin, Definition: definition}},
		LiveMetrics: &LiveMetrics{Telemetry: &TelemetryHealth{Coverage: &TelemetryCoverage{WindowMs: 10_000, ObservedMs: 9_500}}}}
	metrics := cloneRunLiveMetrics(test)
	verdict := metrics.Telemetry.Acceptance
	if verdict == nil || !verdict.Provisional || verdict.Criteria[0].Outcome != TelemetryAcceptancePass || verdict.Criteria[1].Outcome != TelemetryAcceptanceInconclusive || verdict.Outcome != TelemetryAcceptanceInconclusive {
		t.Fatalf("live verdict = %+v", verdict)
	}
	if test.LiveMetrics.Telemetry.Acceptance != nil {
		t.Fatal("the resident sample was annotated")
	}
	if archived := archivedTelemetryHealth(metrics.Telemetry); archived.Acceptance != nil {
		t.Fatal("a history row kept the run verdict")
	}
	// A provisional verdict is never enforced by CI.
	response := ciRunResponse{Status: "COMPLETE", SLAEvaluated: true, ResultsPresent: true, TelemetryVerdict: &ciTelemetryVerdict{TelemetryAcceptanceVerdict: *verdict}}
	finalizeCIRunOutcome(&response, ciRunMetadata{})
	if response.Outcome != "passed" {
		t.Fatalf("provisional verdict enforced: %q", response.Outcome)
	}
}

func TestTelemetryAcceptanceReportSectionRendersEveryFormat(t *testing.T) {
	maxLost := int64(0)
	manifest := acceptanceManifest(TelemetryAcceptancePolicyDefinition{Version: 1, Mode: "fail_ci", MinWallTimeCoverage: acceptanceRatio(0.9), MaxLostRecords: &maxLost})
	manifest.TelemetryCoverage = coverageRecord(70_000, 100_000)
	manifest.TelemetryLoss = &TelemetryLossAccounting{DroppedResults: &TelemetryCounterTotal{State: TelemetryCounterLowerBound, Value: 4}}
	manifest.TelemetryStageDistributions = []TelemetryStageDistribution{{Stage: telemetryStageTransport, Availability: "measured", Samples: 10, P95Ms: acceptanceRatio(12), Excluded: map[string]int64{"clock_unqualified": 2}}}
	manifest.ClockObservationCount = 5
	report := &RunArchiveReport{Manifest: manifest, TelemetryAcceptance: telemetryAcceptanceForManifest(manifest)}
	data := &runArchiveHTMLReportData{Report: report}
	section := reportSectionRegistry["telemetry_acceptance"]
	html, err := section.HTML(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Telemetry verdict FAIL under Gate revision 3", "min_wall_time_coverage", "≥ 4", "lower_bound", "clock_unqualified 2", "5 generator clock observations", "generator-a", "shedded_arrivals"} {
		if !strings.Contains(string(html), want) {
			t.Fatalf("HTML lacks %q: %s", want, html)
		}
	}
	var lines []string
	section.PDF(data, &lines)
	if joined := strings.Join(lines, "\n"); !strings.Contains(joined, "min_wall_time_coverage: fail") || !strings.Contains(joined, "Loss dropped_results: ≥ 4 (lower_bound)") {
		t.Fatalf("PDF lines = %s", joined)
	}
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	if err := section.CSV(data, func(section, key, metric, value string) error {
		return writer.Write([]string{section, key, metric, value})
	}); err != nil {
		t.Fatal(err)
	}
	writer.Flush()
	if text := buffer.String(); !strings.Contains(text, "telemetry_acceptance,,outcome,fail") || !strings.Contains(text, "telemetry_acceptance_criterion,min_wall_time_coverage,basis,70000 / 100000") {
		t.Fatalf("CSV = %s", text)
	}
	// Without a pinned policy the section still records the evidence.
	report.TelemetryAcceptance = nil
	if html, err := section.HTML(data); err != nil || !strings.Contains(string(html), "No telemetry acceptance policy was pinned") {
		t.Fatalf("HTML without a verdict = %s %v", html, err)
	}
	for _, profile := range buildRunArchiveReportProfiles() {
		found := false
		for _, id := range profile.IncludedSections {
			found = found || id == "telemetry_acceptance"
		}
		if !found {
			t.Fatalf("profile %s omits the telemetry acceptance section", profile.ID)
		}
	}
}

func TestTelemetryComparisonShowsEachRunsAcceptanceVerdict(t *testing.T) {
	manifest := acceptanceManifest(TelemetryAcceptancePolicyDefinition{Version: 1, Mode: "fail_ci", MinWallTimeCoverage: acceptanceRatio(0.9)})
	manifest.TelemetryCoverage = coverageRecord(70_000, 100_000)
	manifest.TelemetryLoss = &TelemetryLossAccounting{DroppedResults: &TelemetryCounterTotal{State: TelemetryCounterLowerBound, Value: 4}}
	current := telemetryRunProfile("current", manifest)
	baseline := telemetryRunProfile("baseline", &RunArchiveManifest{})
	if current.Acceptance == nil || current.Acceptance.Outcome != TelemetryAcceptanceFail || baseline.Acceptance != nil {
		t.Fatalf("profiles = %+v / %+v", current.Acceptance, baseline.Acceptance)
	}
	line := baselineTelemetryComparisonLine(BaselineTelemetryComparison{BaselineRunID: "baseline", Current: current, Baseline: baseline, Reasons: []string{telemetryReasonPolicyUnavailable}})
	if !strings.Contains(line, "telemetry acceptance fail (fail_ci, Gate r3) / no acceptance policy") || !strings.Contains(line, "dropped results at least 4 / unavailable") {
		t.Fatalf("comparison line = %s", line)
	}
}

func TestTelemetryAcceptanceWebhookAnnouncesAFailedVerdictOnce(t *testing.T) {
	t.Setenv("APEXLOAD_ALLOW_PRIVATE_WEBHOOKS", "true")
	server, _ := baselineTestServer(t)
	type delivery struct {
		payload map[string]any
		key     string
	}
	received := make(chan delivery, 8)
	hook := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		received <- delivery{payload: payload, key: r.Header.Get("X-APEXLoad-Notification-Key")}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer hook.Close()
	server.runner.notifier = newNotificationDispatcher(hook.Client())
	settings := server.runner.GetSystemSettings()
	settings.Notifications = NotificationsSettings{Webhooks: []NotificationWebhookSettings{{Enabled: true, URL: hook.URL, Format: notificationFormatSlack, Events: []string{"telemetry_acceptance"}}}}
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}
	next := func() delivery {
		t.Helper()
		select {
		case got := <-received:
			return got
		case <-time.After(5 * time.Second):
			t.Fatal("no webhook")
		}
		return delivery{}
	}
	manifest := acceptanceManifest(TelemetryAcceptancePolicyDefinition{Version: 1, Mode: "fail_ci", MinWallTimeCoverage: acceptanceRatio(0.9)})
	manifest.TestName, manifest.CompletedAt = "nightly", time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	manifest.TelemetryCoverage = coverageRecord(70_000, 100_000)
	server.notifyTelemetryAcceptance("run-1", manifest)
	first := next()
	text := fmt.Sprint(first.payload)
	for _, want := range []string{"Telemetry Acceptance: nightly (fail)", "70.0%", "policy:gate@3", "criterion:min_wall_time_coverage fail", "source:generator-a unknown_ms:30000"} {
		if !strings.Contains(text, want) {
			t.Fatalf("webhook lacks %q: %s", want, text)
		}
	}
	// The same archive announces with the same idempotency key.
	server.notifyTelemetryAcceptance("run-1", manifest)
	if again := next(); again.key == "" || again.key != first.key {
		t.Fatalf("idempotency keys %q / %q", first.key, again.key)
	}
	// A passing verdict, or a run without a policy, sends nothing.
	manifest.TelemetryCoverage = coverageRecord(95_000, 100_000)
	server.notifyTelemetryAcceptance("run-2", manifest)
	server.notifyTelemetryAcceptance("run-3", &RunArchiveManifest{})
	select {
	case got := <-received:
		t.Fatalf("unexpected webhook %v", got.payload)
	case <-time.After(300 * time.Millisecond):
	}
}

package api

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
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
		Sources: []TelemetrySourceCoverage{{Source: "generator-a", ObservedMs: observed, RequiredMs: window, UnknownMs: window - observed}}, SourceRequirements: true}}
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

// The report labels an advisory criterion in HTML, PDF and CSV.
func TestTelemetryAcceptanceReportLabelsAdvisoryCriteria(t *testing.T) {
	manifest := acceptanceManifest(TelemetryAcceptancePolicyDefinition{Version: 1, Mode: "fail_ci", StageLagPercentiles: []TelemetryLagPercentileRule{{Stage: "browser_render", Percentile: "p95", MaxMs: 500}}})
	manifest.TelemetryStageDistributions = []TelemetryStageDistribution{{Stage: telemetryStageBrowserRender, Availability: "measured", Advisory: true, Samples: 10, P95Ms: acceptanceRatio(900)}}
	report := &RunArchiveReport{Manifest: manifest, TelemetryAcceptance: telemetryAcceptanceForManifest(manifest)}
	data := &runArchiveHTMLReportData{Report: report}
	section := reportSectionRegistry["telemetry_acceptance"]
	const label = "Fail (advisory, never decides)"
	if html, err := section.HTML(data); err != nil || !strings.Contains(string(html), label) {
		t.Fatalf("HTML = %s %v", html, err)
	}
	var lines []string
	section.PDF(data, &lines)
	if joined := strings.Join(lines, "\n"); !strings.Contains(joined, "Browser render lag, P95 (maximum): "+label) {
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
	if text := buffer.String(); !strings.Contains(text, `telemetry_acceptance_criterion,stage_lag:browser_render/p95,outcome,"fail (advisory, never decides)"`) {
		t.Fatalf("CSV = %s", text)
	}
}

// The webhook names each source's unknown time over its requirement, and over
// the window for coverage recorded before requirements.
func TestTelemetryAcceptanceSourceConditions(t *testing.T) {
	coverage := TelemetryCoverage{WindowMs: 100_000, SourceRequirements: true, Sources: []TelemetrySourceCoverage{
		{Source: "gen-a", ObservedMs: 50_000, RequiredMs: 50_000}, {Source: "gen-b", ObservedMs: 40_000, RequiredMs: 50_000, UnknownMs: 10_000}}}
	if got := telemetryAcceptanceSourceConditions(coverage); len(got) != 1 || got[0] != "source:gen-b unknown_ms:10000" {
		t.Fatalf("conditions = %q", got)
	}
	coverage.SourceRequirements = false
	coverage.Sources = []TelemetrySourceCoverage{{Source: "gen-old", ObservedMs: 70_000}, {Source: "gen-silent"}}
	if got := telemetryAcceptanceSourceConditions(coverage); len(got) != 2 || got[0] != "source:gen-old unknown_ms:30000" || got[1] != "source:gen-silent unknown_ms:100000" {
		t.Fatalf("legacy conditions = %q", got)
	}
}

// The report says each source's unknown time over the time it was required
// (an older archive's sources keep their observed-of-window line) and why
// observed time is a lower bound when the observer was lost.
func TestTelemetryAcceptanceReportCoverageSourcesAndLostObserver(t *testing.T) {
	manifest := acceptanceManifest(TelemetryAcceptancePolicyDefinition{Version: 1, Mode: "fail_ci", MinWallTimeCoverage: acceptanceRatio(0.9)})
	manifest.TelemetryCoverage = coverageRecord(40_000, 100_000)
	manifest.TelemetryCoverage.Coverage.ObserverLost = telemetryCoverageAdopted
	view := buildTelemetryAcceptanceReportView(manifest, telemetryAcceptanceForManifest(manifest))
	if !strings.Contains(view.Coverage, "a controller adopted the run and observed it only from then") {
		t.Fatalf("coverage = %s", view.Coverage)
	}
	if len(view.Sources) != 1 || view.Sources[0].Observed != "40,000 ms observed; required for 100,000 ms, unknown 60,000 ms" {
		t.Fatalf("sources = %+v", view.Sources)
	}
	// Coverage recorded before source requirements lists each source's
	// observed time of the window, including a source never observed.
	legacy := *manifest.TelemetryCoverage
	legacy.Coverage.SourceRequirements = false
	legacy.Coverage.Sources = []TelemetrySourceCoverage{{Source: "generator-old", ObservedMs: 30_000}, {Source: "generator-silent"}}
	manifest.TelemetryCoverage = &legacy
	lines := map[string]string{}
	for _, source := range buildTelemetryAcceptanceReportView(manifest, nil).Sources {
		lines[source.Source] = source.Observed
	}
	if lines["generator-old"] != "30,000 of 100,000 ms" || lines["generator-silent"] != "0 of 100,000 ms" {
		t.Fatalf("legacy sources = %v", lines)
	}
	manifest.TelemetryCoverage.Coverage.ObserverLost = telemetryCoverageLedgerLost
	if view := buildTelemetryAcceptanceReportView(manifest, nil); !strings.Contains(view.Coverage, "finalized without the record of what was observed") {
		t.Fatalf("coverage = %s", view.Coverage)
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
	for _, want := range []string{"Telemetry verdict FAIL under Gate revision 3", "Wall-time coverage (minimum)", "≥ 4", "Lower bound", "clock not qualified 2", "5 generator clock observations", "generator-a", "Shed arrivals", "70,000 of 100,000"} {
		if !strings.Contains(string(html), want) {
			t.Fatalf("HTML lacks %q: %s", want, html)
		}
	}
	var lines []string
	section.PDF(data, &lines)
	if joined := strings.Join(lines, "\n"); !strings.Contains(joined, "Wall-time coverage (minimum): Fail") || !strings.Contains(joined, "Loss Dropped results: ≥ 4 (Lower bound)") {
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
	// A comparison saved before verdicts never claims a run had no policy.
	saved := baseline
	saved.AcceptanceEvaluated = false
	if line := baselineTelemetryComparisonLine(BaselineTelemetryComparison{BaselineRunID: "baseline", Current: current, Baseline: saved, Reasons: []string{telemetryReasonPolicyUnavailable}}); !strings.Contains(line, "/ not recorded (saved before telemetry verdicts)") {
		t.Fatalf("pre-verdict comparison line = %s", line)
	}
}

func TestTelemetryAcceptanceWebhookAnnouncesAFailedVerdictOnce(t *testing.T) {
	t.Setenv("APEXLOAD_ALLOW_PRIVATE_WEBHOOKS", "true")
	server, store := baselineTestServer(t)
	for _, id := range []string{"run-1", "run-changes", "run-legacy", "run-2", "run-3"} {
		if err := store.SaveTest(&storage.TestRecord{ID: id, ProjectID: storage.DefaultProjectID, Name: id, Status: "COMPLETE", StartTime: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
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
	// A failing advisory criterion (browser render) never decided the
	// verdict, so the announcement does not list it.
	manifest := acceptanceManifest(TelemetryAcceptancePolicyDefinition{Version: 1, Mode: "fail_ci", MinWallTimeCoverage: acceptanceRatio(0.9),
		StageLagPercentiles: []TelemetryLagPercentileRule{{Stage: "browser_render", Percentile: "p95", MaxMs: 500}}})
	manifest.TestName, manifest.CompletedAt = "nightly", time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	manifest.TelemetryCoverage = coverageRecord(70_000, 100_000)
	manifest.TelemetryStageDistributions = []TelemetryStageDistribution{{Stage: telemetryStageBrowserRender, Availability: "measured", Advisory: true, Samples: 10, P95Ms: acceptanceRatio(900)}}
	server.notifyTelemetryAcceptance("run-1", manifest)
	first := next()
	text := fmt.Sprint(first.payload)
	for _, want := range []string{"Telemetry Acceptance: nightly (fail)", "70.0%", "policy:gate@3", "criterion:min_wall_time_coverage fail", "source:generator-a unknown_ms:30000"} {
		if !strings.Contains(text, want) {
			t.Fatalf("webhook lacks %q: %s", want, text)
		}
	}
	if strings.Contains(text, "browser_render") {
		t.Fatalf("webhook lists an advisory criterion: %s", text)
	}
	if first.key == "" {
		t.Fatal("the announcement carries no idempotency key")
	}
	// A later attach of the same run's archive (a refresh, a recovery, another
	// leader) announces nothing more.
	server.notifyTelemetryAcceptance("run-1", manifest)
	select {
	case again := <-received:
		t.Fatalf("the run was announced twice: %v", again.payload)
	case <-time.After(300 * time.Millisecond):
	}
	// A refresh that finds the verdict changed announces the new outcome.
	worse := *manifest
	worse.TelemetryCoverage = nil
	server.notifyTelemetryAcceptance("run-changes", &worse)
	if got := next(); !strings.Contains(fmt.Sprint(got.payload), "inconclusive") {
		t.Fatalf("inconclusive announcement = %v", got.payload)
	}
	server.notifyTelemetryAcceptance("run-changes", manifest)
	if got := next(); !strings.Contains(fmt.Sprint(got.payload), "(fail)") {
		t.Fatalf("the worsened verdict was not announced: %v", got.payload)
	}
	// A run pinned before definitions were retained (unknown mode) is not
	// announced.
	legacy := *manifest
	legacy.Provenance.TelemetryAcceptancePolicy = nil
	server.notifyTelemetryAcceptance("run-legacy", &legacy)
	select {
	case got := <-received:
		t.Fatalf("an unknown-mode verdict was announced: %v", got.payload)
	case <-time.After(300 * time.Millisecond):
	}
	// A passing verdict, or a run without a policy, sends nothing.
	manifest.TelemetryCoverage = coverageRecord(95_000, 100_000)
	server.notifyTelemetryAcceptance("run-2", manifest)
	server.notifyTelemetryAcceptance("run-3", &RunArchiveManifest{})
	// A run deleted before its verdict is announced is never announced.
	manifest.TelemetryCoverage = coverageRecord(70_000, 100_000)
	server.notifyTelemetryAcceptance("deleted-run", manifest)
	select {
	case got := <-received:
		t.Fatalf("unexpected webhook %v", got.payload)
	case <-time.After(300 * time.Millisecond):
	}
}

// A stage distribution sampled only from a failover onward cannot decide a
// percentile (only a maximum already above the limit fails), and one whose
// observations were mostly excluded has no upper bound, so it cannot pass.
func TestTelemetryAcceptanceStageLagOnPartialEvidence(t *testing.T) {
	started := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	adopted := started.Add(20 * time.Minute)
	rules := []TelemetryLagPercentileRule{{Stage: "transport", Percentile: "p99", MaxMs: 500}, {Stage: "transport", Percentile: "max", MaxMs: 500}}
	manifest := acceptanceManifest(TelemetryAcceptancePolicyDefinition{Version: 1, Mode: "fail_ci", StageLagPercentiles: rules})
	manifest.Provenance.StartedAt = &started
	stage := TelemetryStageDistribution{Stage: telemetryStageTransport, Availability: "measured", Samples: 100, P99Ms: acceptanceRatio(120), MaxMs: acceptanceRatio(300), SampledFrom: &adopted}
	manifest.TelemetryStageDistributions = []TelemetryStageDistribution{stage}
	verdict := telemetryAcceptanceForManifest(manifest)
	// A p99 of part of the run bounds nothing; its max is a lower bound.
	if verdict.Outcome != TelemetryAcceptanceInconclusive || verdict.Criteria[0].Outcome != TelemetryAcceptanceInconclusive || !strings.Contains(verdict.Criteria[0].Reason, "sampled only from") ||
		verdict.Criteria[0].Bound != telemetryAcceptancePartial || verdict.Criteria[1].Bound != telemetryAcceptanceLowerBound {
		t.Fatalf("post-failover evidence = %+v", verdict)
	}
	// A maximum already above the limit fails whatever was missed.
	manifest.TelemetryStageDistributions[0].MaxMs = acceptanceRatio(900)
	if verdict = telemetryAcceptanceForManifest(manifest); verdict.Outcome != TelemetryAcceptanceFail || verdict.Criteria[1].Outcome != TelemetryAcceptanceFail {
		t.Fatalf("partial maximum above the limit = %+v", verdict)
	}
	// Sampled from the run's start: exact.
	manifest.TelemetryStageDistributions[0].MaxMs, manifest.TelemetryStageDistributions[0].SampledFrom = acceptanceRatio(300), &started
	if verdict = telemetryAcceptanceForManifest(manifest); verdict.Outcome != TelemetryAcceptancePass {
		t.Fatalf("whole-run evidence = %+v", verdict)
	}
	// Mostly excluded observations are partial too.
	manifest.TelemetryStageDistributions[0].Excluded = map[string]int64{"clock_unqualified": 10_000}
	if verdict = telemetryAcceptanceForManifest(manifest); verdict.Outcome != TelemetryAcceptanceInconclusive || !strings.Contains(verdict.Criteria[0].Reason, "excluded 10000") {
		t.Fatalf("mostly excluded evidence = %+v", verdict)
	}
}

// The controller that started a run samples its stages from the start; one
// that adopted it samples from the adoption.
func TestTelemetryStageRecorderCoversTheRunFromItsStart(t *testing.T) {
	started := time.Now().UTC().Add(-time.Minute)
	test := &ManagedTest{StartedAt: &started}
	if since := stageRecorderLocked(test).since; !since.Equal(started) {
		t.Fatalf("starting controller samples from %v, want %v", since, started)
	}
	adopted := &ManagedTest{StartedAt: &started, continuityFailovers: []RunFailover{{AdoptedAt: time.Now().UTC()}}}
	if since := stageRecorderLocked(adopted).since; !since.After(started) {
		t.Fatalf("adopting controller samples from %v", since)
	}
}

// Coverage the finalizing controller could not have observed (a restart lost
// its ledger, or it adopted the run) is a lower bound: below the floor the
// criterion is inconclusive, never a failure; at or above it, a pass.
func TestTelemetryAcceptanceCoverageAfterALostObserverIsALowerBound(t *testing.T) {
	manifest := acceptanceManifest(TelemetryAcceptancePolicyDefinition{Version: 1, Mode: "fail_ci", MinWallTimeCoverage: acceptanceRatio(0.9)})
	for _, tc := range []struct {
		lost     string
		observed int64
		outcome  string
		reason   string
	}{
		{telemetryCoverageLedgerLost, 0, TelemetryAcceptanceInconclusive, "finalized without the record of what was observed"},
		{telemetryCoverageAdopted, 50_000, TelemetryAcceptanceInconclusive, "adopted the run"},
		{telemetryCoverageAdopted, 95_000, TelemetryAcceptancePass, ""},
		{"", 50_000, TelemetryAcceptanceFail, "below the 90.0% floor"},
	} {
		manifest.TelemetryCoverage = coverageRecord(tc.observed, 100_000)
		manifest.TelemetryCoverage.Coverage.ObserverLost = tc.lost
		criterion := telemetryAcceptanceForManifest(manifest).Criteria[0]
		wantBound := telemetryAcceptanceLowerBound
		if tc.lost == "" {
			wantBound = telemetryAcceptanceExact
		}
		if criterion.Outcome != tc.outcome || criterion.Bound != wantBound || !strings.Contains(criterion.Reason, tc.reason) {
			t.Fatalf("%q at %d ms: %+v", tc.lost, tc.observed, criterion)
		}
	}
}

// Health rebuilt from interval history (a restarted controller, a follower)
// carries no loss, coverage or stage evidence, so no provisional verdict is
// shown for it.
func TestLiveTelemetryAcceptanceSkipsHealthRestoredFromHistory(t *testing.T) {
	ceiling := 0.001
	definition := TelemetryAcceptancePolicyDefinition{Version: 1, Mode: "fail_ci", MaxLossRatio: &ceiling}
	pin := RunPolicyPin{Kind: storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, ID: "gate", Revision: 1, SHA256: "abc"}
	restored := intervalSummaryRecordToLiveMetrics(storage.IntervalSummaryRecord{TotalTx: 500, DroppedResultsAvailable: true, TransportLagMs: 12})
	if restored.Telemetry == nil {
		t.Fatal("fixture: the restored summary carries no telemetry health")
	}
	test := &ManagedTest{Status: "RUNNING", initialArchiveProvenance: &RunArchiveProvenance{PolicyPins: []RunPolicyPin{pin}, TelemetryAcceptancePolicy: &TelemetryAcceptancePolicySnapshot{Pin: pin, Definition: definition}}, LiveMetrics: restored}
	if verdict := cloneRunLiveMetrics(test).Telemetry.Acceptance; verdict != nil {
		t.Fatalf("restored health got a provisional verdict: %+v", verdict)
	}
	total, errs := int64(999_000), int64(990)
	test.LiveMetrics = &LiveMetrics{TotalTx: 500, RunTotalTx: &total, RunTotalErrors: &errs, Telemetry: &TelemetryHealth{Loss: &TelemetryLossAccounting{DroppedResults: &TelemetryCounterTotal{State: TelemetryCounterExact, Value: 10}}}}
	if verdict := cloneRunLiveMetrics(test).Telemetry.Acceptance; verdict == nil || verdict.Outcome != TelemetryAcceptancePass {
		t.Fatalf("live health verdict = %+v", verdict)
	}
	// A finished run's verdict is the archived one, never a provisional one.
	test.Status = "COMPLETE"
	if verdict := cloneRunLiveMetrics(test).Telemetry.Acceptance; verdict != nil {
		t.Fatalf("finished run got a provisional verdict: %+v", verdict)
	}
}

// Live loss is measured against the run's results so far, not one window.
func TestLiveTelemetryAcceptanceLossRatioUsesRunTotals(t *testing.T) {
	ceiling := 0.001
	definition := TelemetryAcceptancePolicyDefinition{Version: 1, Mode: "fail_ci", MaxLossRatio: &ceiling}
	pin := RunPolicyPin{Kind: storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, ID: "gate", Revision: 1, SHA256: "abc"}
	loss := &TelemetryLossAccounting{DroppedResults: &TelemetryCounterTotal{State: TelemetryCounterExact, Value: 10}}
	total, errs := int64(999_000), int64(990)
	test := &ManagedTest{initialArchiveProvenance: &RunArchiveProvenance{PolicyPins: []RunPolicyPin{pin}, TelemetryAcceptancePolicy: &TelemetryAcceptancePolicySnapshot{Pin: pin, Definition: definition}},
		LiveMetrics: &LiveMetrics{TotalTx: 500, RunTotalTx: &total, RunTotalErrors: &errs, Telemetry: &TelemetryHealth{Loss: loss}}}
	verdict := cloneRunLiveMetrics(test).Telemetry.Acceptance
	criterion := verdict.Criteria[0]
	if criterion.Outcome != TelemetryAcceptancePass || *criterion.Denominator != 1_000_000 {
		t.Fatalf("live loss ratio = %+v", criterion)
	}
	test.LiveMetrics.RunTotalTx, test.LiveMetrics.RunTotalErrors = nil, nil
	if verdict = cloneRunLiveMetrics(test).Telemetry.Acceptance; verdict.Criteria[0].Outcome != TelemetryAcceptanceInconclusive {
		t.Fatalf("live loss ratio without run totals = %+v", verdict.Criteria[0])
	}
}

// Browser render is viewer-supplied: its criterion is shown, never decisive.
func TestTelemetryAcceptanceBrowserRenderIsAdvisory(t *testing.T) {
	manifest := acceptanceManifest(TelemetryAcceptancePolicyDefinition{Version: 1, Mode: "fail_ci", StageLagPercentiles: []TelemetryLagPercentileRule{{Stage: "browser_render", Percentile: "p95", MaxMs: 500}}})
	manifest.TelemetryStageDistributions = []TelemetryStageDistribution{{Stage: telemetryStageBrowserRender, Availability: "measured", Advisory: true, Samples: 10, P95Ms: acceptanceRatio(900)}}
	verdict := telemetryAcceptanceForManifest(manifest)
	if verdict.Outcome != TelemetryAcceptancePass || !verdict.Criteria[0].Advisory || verdict.Criteria[0].Outcome != TelemetryAcceptanceFail || len(verdict.Reasons) != 0 {
		t.Fatalf("advisory browser render = %+v", verdict)
	}
	manifest.TelemetryStageDistributions = nil
	if verdict = telemetryAcceptanceForManifest(manifest); verdict.Outcome != TelemetryAcceptancePass {
		t.Fatalf("unmeasured browser render decided the run: %+v", verdict)
	}
}

// Shared reasons are listed once, and small ratios stay readable.
func TestTelemetryAcceptanceReasonsAndRatios(t *testing.T) {
	lost := int64(0)
	manifest := acceptanceManifest(TelemetryAcceptancePolicyDefinition{Version: 1, Mode: "fail_ci", MaxLostRecords: &lost, MaxLossRatio: acceptanceRatio(0.0003)})
	if verdict := telemetryAcceptanceForManifest(manifest); len(verdict.Reasons) != 1 {
		t.Fatalf("reasons = %q", verdict.Reasons)
	}
	for value, want := range map[float64]string{0.0003: "0.030%", 0.0004: "0.040%", 0.7: "70.0%", 0: "0.0%", 0.000004: "0.00040%"} {
		if got := formatAcceptanceRatio(value); got != want {
			t.Fatalf("formatAcceptanceRatio(%v) = %q, want %q", value, got, want)
		}
	}
}

// The GitHub Check says what a fail_ci verdict did.
func TestCIGitHubCheckTelemetryEffect(t *testing.T) {
	for _, tc := range []struct {
		verdict ciTelemetryVerdict
		want    string
	}{
		{ciTelemetryVerdict{TelemetryAcceptanceVerdict: TelemetryAcceptanceVerdict{Outcome: TelemetryAcceptancePass, Mode: "fail_ci"}}, "fail_ci: passed"},
		{ciTelemetryVerdict{TelemetryAcceptanceVerdict: TelemetryAcceptanceVerdict{Outcome: TelemetryAcceptanceFail, Mode: "fail_ci"}, Enforced: true}, "enforced: decided the outcome"},
		{ciTelemetryVerdict{TelemetryAcceptanceVerdict: TelemetryAcceptanceVerdict{Outcome: TelemetryAcceptanceInconclusive, Mode: "fail_ci"}, InconclusivePolicy: ciTelemetryInconclusivePass}, "maps to pass"},
		{ciTelemetryVerdict{TelemetryAcceptanceVerdict: TelemetryAcceptanceVerdict{Outcome: TelemetryAcceptanceFail, Mode: "fail_ci"}}, "the run did not otherwise pass"},
		{ciTelemetryVerdict{TelemetryAcceptanceVerdict: TelemetryAcceptanceVerdict{Outcome: TelemetryAcceptanceInconclusive, Mode: telemetryAcceptanceModeUnknown}}, "unknown mode"},
		{ciTelemetryVerdict{TelemetryAcceptanceVerdict: TelemetryAcceptanceVerdict{Outcome: TelemetryAcceptanceFail, Mode: "advisory"}}, "advisory: never changes the outcome"},
	} {
		if text := strings.Join(ciGitHubCheckTelemetryLines(&tc.verdict), "\n"); !strings.Contains(text, tc.want) {
			t.Fatalf("%+v: %s", tc.verdict, text)
		}
	}
}

// A controller that re-adopts a run it once owned keeps its recorder, which
// then counts only from the adoption.
func TestTelemetryStageRecorderReadoptionSamplesFromTheAdoption(t *testing.T) {
	started := time.Now().UTC().Add(-time.Hour)
	test := &ManagedTest{StartedAt: &started}
	recorder := stageRecorderLocked(test)
	adoptedAt := time.Now().UTC()
	recorder.adopted(adoptedAt)
	if !recorder.since.Equal(adoptedAt) {
		t.Fatalf("re-adopted recorder samples from %v, want %v", recorder.since, adoptedAt)
	}
	// An older adoption never moves it back.
	recorder.adopted(started)
	if !recorder.since.Equal(adoptedAt) {
		t.Fatalf("since moved back to %v", recorder.since)
	}
}

// Excluded observations bound a percentile rather than hide it: it passes
// only when the kept percentile it could rise to is within the limit, fails
// when the one it could fall to exceeds it, and the maximum has no upper
// bound at all.
func TestTelemetryAcceptanceExclusionsHidePercentiles(t *testing.T) {
	started := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	rules := []TelemetryLagPercentileRule{{Stage: "transport", Percentile: "max", MaxMs: 500}, {Stage: "transport", Percentile: "p99", MaxMs: 500}, {Stage: "transport", Percentile: "p50", MaxMs: 500}}
	manifest := acceptanceManifest(TelemetryAcceptancePolicyDefinition{Version: 1, Mode: "fail_ci", StageLagPercentiles: rules})
	manifest.Provenance.StartedAt = &started
	stage := TelemetryStageDistribution{Stage: telemetryStageTransport, Availability: "measured", Samples: 1000, P50Ms: acceptanceRatio(100), P99Ms: acceptanceRatio(200), MaxMs: acceptanceRatio(300), SampledFrom: &started,
		Excluded: map[string]int64{"clock_unqualified": 20}}
	manifest.TelemetryStageDistributions = []TelemetryStageDistribution{stage}
	verdict := telemetryAcceptanceForManifest(manifest)
	outcomes := []string{verdict.Criteria[0].Outcome, verdict.Criteria[1].Outcome, verdict.Criteria[2].Outcome}
	if outcomes[0] != TelemetryAcceptanceInconclusive || outcomes[1] != TelemetryAcceptanceInconclusive || outcomes[2] != TelemetryAcceptancePass {
		t.Fatalf("2%% excluded: max/p99/p50 = %v", outcomes)
	}
	if verdict.Criteria[1].Bound != telemetryAcceptancePartial || verdict.Criteria[2].Bound != telemetryAcceptanceUpperBound || *verdict.Criteria[2].Observed != 200 {
		t.Fatalf("2%% excluded bounds: p99 %+v, p50 %+v", verdict.Criteria[1], verdict.Criteria[2])
	}
	manifest.TelemetryStageDistributions[0].Excluded = map[string]int64{"clock_unqualified": 5}
	verdict = telemetryAcceptanceForManifest(manifest)
	if verdict.Criteria[0].Outcome != TelemetryAcceptanceInconclusive || verdict.Criteria[1].Outcome != TelemetryAcceptancePass || verdict.Criteria[1].Bound != telemetryAcceptanceUpperBound || *verdict.Criteria[1].Observed != 300 {
		t.Fatalf("0.5%% excluded: max %+v, p99 %+v", verdict.Criteria[0], verdict.Criteria[1])
	}
	// A heavy tail: 9 of 1,009 excluded, the kept p99 within the limit and
	// the kept max far above it. The excluded could all be slower, so the
	// whole run's p99 could be the kept max: inconclusive, never exact.
	heavy := TelemetryStageDistribution{Stage: telemetryStageTransport, Availability: "measured", Samples: 1000, P50Ms: acceptanceRatio(100), P95Ms: acceptanceRatio(300), P99Ms: acceptanceRatio(450), MaxMs: acceptanceRatio(2000), SampledFrom: &started,
		Excluded: map[string]int64{"clock_unqualified": 9}}
	manifest.TelemetryStageDistributions = []TelemetryStageDistribution{heavy}
	verdict = telemetryAcceptanceForManifest(manifest)
	if p99 := verdict.Criteria[1]; p99.Outcome != TelemetryAcceptanceInconclusive || p99.Bound != telemetryAcceptancePartial || !strings.Contains(p99.Reason, "excluded 9") {
		t.Fatalf("heavy tail p99 = %+v", p99)
	}
	// The p99 can fall no lower than the kept p95, which here exceeds the
	// limit: a conclusive failure at that lower bound.
	heavy.P95Ms, heavy.P99Ms = acceptanceRatio(600), acceptanceRatio(700)
	manifest.TelemetryStageDistributions = []TelemetryStageDistribution{heavy}
	verdict = telemetryAcceptanceForManifest(manifest)
	if p99 := verdict.Criteria[1]; p99.Outcome != TelemetryAcceptanceFail || p99.Bound != telemetryAcceptanceLowerBound || *p99.Observed != 600 {
		t.Fatalf("p99 above the limit whatever was excluded = %+v", p99)
	}
}

// An archived digest bounds an excluded-observation percentile at exactly its
// rank positions: one excluded observation leaves a definite breach a failure
// and a definite pass a pass, where the recorded percentiles alone could only
// say inconclusive. Without a matching digest the recorded ones are used.
func TestTelemetryAcceptanceExclusionBoundsUseTheStageDigest(t *testing.T) {
	digest := engine.NewTDigest(100)
	for i := 1; i <= 100_000; i++ {
		digest.Add(float64(i) / 100)
	}
	snapshot := digest.Snapshot()
	quantile := func(q float64) *float64 { value := math.Round(digest.Quantile(q)*1000) / 1000; return &value }
	stage := TelemetryStageDistribution{Stage: telemetryStageTransport, Availability: "measured", Samples: snapshot.Count, P50Ms: quantile(0.50), P95Ms: quantile(0.95), P99Ms: quantile(0.99), MaxMs: acceptanceRatio(1000),
		Excluded: map[string]int64{"clock_unqualified": 1}}
	rules := []TelemetryLagPercentileRule{{Stage: "transport", Percentile: "p99", MaxMs: 970}, {Stage: "transport", Percentile: "p99", MaxMs: 995}, {Stage: "transport", Percentile: "p50", MaxMs: 400}}
	manifest := acceptanceManifest(TelemetryAcceptancePolicyDefinition{Version: 1, Mode: "fail_ci", StageLagPercentiles: rules})
	manifest.TelemetryStageDistributions = []TelemetryStageDistribution{stage}
	manifest.TelemetryStageDigests = map[string]engine.TDigestSnapshot{telemetryStageTransport: snapshot}
	verdict := telemetryAcceptanceForManifest(manifest)
	if c := verdict.Criteria[0]; c.Outcome != TelemetryAcceptanceFail || c.Bound != telemetryAcceptanceLowerBound || *c.Observed <= 970 {
		t.Fatalf("p99 ≤ 970 with one exclusion = %+v", c)
	}
	if c := verdict.Criteria[1]; c.Outcome != TelemetryAcceptancePass || c.Bound != telemetryAcceptanceUpperBound || *c.Observed > 995 {
		t.Fatalf("p99 ≤ 995 with one exclusion = %+v", c)
	}
	if c := verdict.Criteria[2]; c.Outcome != TelemetryAcceptanceFail || *c.Observed <= 400 {
		t.Fatalf("p50 ≤ 400 with one exclusion = %+v", c)
	}
	// A digest of a different population, or none, falls back to the
	// recorded percentiles: kept p95 950 and max 1000 bracket both p99 limits.
	for _, digests := range []map[string]engine.TDigestSnapshot{nil, {telemetryStageTransport: engine.NewTDigest(100).Snapshot()}} {
		manifest.TelemetryStageDigests = digests
		verdict = telemetryAcceptanceForManifest(manifest)
		if verdict.Criteria[0].Outcome != TelemetryAcceptanceInconclusive || verdict.Criteria[1].Outcome != TelemetryAcceptanceInconclusive || verdict.Criteria[2].Outcome != TelemetryAcceptanceInconclusive {
			t.Fatalf("recorded percentiles only (%d digests) = %+v", len(digests), verdict.Criteria)
		}
	}
}

// The Check shows each bound: at least, at most, or partial evidence.
func TestCIGitHubCheckTelemetryBounds(t *testing.T) {
	observed := func(value float64) *float64 { return &value }
	verdict := ciTelemetryVerdict{TelemetryAcceptanceVerdict: TelemetryAcceptanceVerdict{Outcome: TelemetryAcceptanceInconclusive, Mode: "fail_ci", Criteria: []TelemetryAcceptanceCriterion{
		{Criterion: "stage_lag:transport/p99", Outcome: TelemetryAcceptancePass, Comparator: "<=", Threshold: 500, Unit: "ms", Observed: observed(300), Bound: telemetryAcceptanceUpperBound},
		{Criterion: "stage_lag:transport/p95", Outcome: TelemetryAcceptanceInconclusive, Comparator: "<=", Threshold: 500, Unit: "ms", Observed: observed(450), Bound: telemetryAcceptancePartial},
		{Criterion: "stage_lag:transport/max", Outcome: TelemetryAcceptanceFail, Comparator: "<=", Threshold: 500, Unit: "ms", Observed: observed(900), Bound: telemetryAcceptanceLowerBound}}}}
	text := strings.Join(ciGitHubCheckTelemetryLines(&verdict), "\n")
	for _, want := range []string{"transport/p99: `pass` (observed ≤ 300", "transport/p95: `inconclusive` (observed 450", "(partial evidence)", "transport/max: `fail` (observed ≥ 900"} {
		if !strings.Contains(text, want) {
			t.Fatalf("check lacks %q: %s", want, text)
		}
	}
}

// The Check labels an advisory criterion, and the webhook never lists one.
func TestTelemetryAcceptanceAdvisoryCriteriaAreLabelled(t *testing.T) {
	verdict := ciTelemetryVerdict{TelemetryAcceptanceVerdict: TelemetryAcceptanceVerdict{Outcome: TelemetryAcceptancePass, Mode: "fail_ci", Criteria: []TelemetryAcceptanceCriterion{
		{Criterion: "stage_lag:browser_render/p95", Outcome: TelemetryAcceptanceFail, Comparator: "<=", Threshold: 500, Unit: "ms", Advisory: true}}}}
	if text := strings.Join(ciGitHubCheckTelemetryLines(&verdict), "\n"); !strings.Contains(text, "`fail` (advisory, never decides)") {
		t.Fatalf("check lines = %s", text)
	}
}

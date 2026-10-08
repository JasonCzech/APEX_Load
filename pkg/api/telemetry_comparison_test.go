package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/rca"
	"github.com/jasonczech/apexload/pkg/storage"
)

func telemetryReceipt(t *testing.T, cadenceSeconds int, fanoutBudget float64) *TelemetryBudgetPolicy {
	t.Helper()
	thresholds := ThresholdSettings{MaxTelemetryControllerFanoutMs: &fanoutBudget}
	policy := captureTelemetryBudgetPolicy(RunArchiveExecutionSettingsSnapshot{General: GeneralSettings{MetricsIntervalSeconds: cadenceSeconds}, Thresholds: thresholds}, false, time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC))
	if !validTelemetryBudgetPolicy(policy) {
		t.Fatalf("fixture receipt invalid: %+v", policy)
	}
	return policy
}

func telemetryComparisonRun(id string, receipt *TelemetryBudgetPolicy, breachedPct float64, mutate func(*RunArchiveManifest)) *baselineComparisonRun {
	manifest := &RunArchiveManifest{TestID: id, Provenance: RunArchiveProvenance{TelemetryPolicy: receipt}}
	if mutate != nil {
		mutate(manifest)
	}
	coverage := 100.0
	return &baselineComparisonRun{identity: BaselineRunIdentity{ID: id}, manifest: manifest, telemetry: BaselineTelemetryEvidence{RunID: id, BreachedIntervalPct: &breachedPct, HealthCoveragePct: &coverage}}
}

func TestTelemetryComparisonComparesHealthOnlyForCompatibleRuns(t *testing.T) {
	qualified := rca.WorkloadQualification{Status: rca.BaselineQualified}
	current := telemetryComparisonRun("current", telemetryReceipt(t, 1, 250), 12, nil)

	same := compareBaselineTelemetry(current, telemetryComparisonRun("same", telemetryReceipt(t, 1, 250), 4, nil), qualified)
	if !same.Comparable || len(same.Reasons) != 0 || same.Scope != "whole_run" || same.BreachedIntervalPctDelta == nil || *same.BreachedIntervalPctDelta != 8 {
		t.Fatalf("compatible pair %+v", same)
	}

	cadence := compareBaselineTelemetry(current, telemetryComparisonRun("cadence", telemetryReceipt(t, 3, 250), 4, nil), qualified)
	if cadence.Comparable || !slices.Contains(cadence.Reasons, telemetryReasonCadenceDiffers) || cadence.BreachedIntervalPctDelta != nil {
		t.Fatalf("a cadence mismatch was not flagged: %+v", cadence)
	}
	if cadence.Current.CadenceMs != 1000 || cadence.Baseline.CadenceMs != 3000 {
		t.Fatalf("side-by-side cadence %d / %d", cadence.Current.CadenceMs, cadence.Baseline.CadenceMs)
	}

	budgets := compareBaselineTelemetry(current, telemetryComparisonRun("budgets", telemetryReceipt(t, 1, 900), 4, nil), qualified)
	if budgets.Comparable || !sameStrings(budgets.Reasons, []string{telemetryReasonBudgetsDiffer}) {
		t.Fatalf("different budgets %+v", budgets.Reasons)
	}

	// Browser render is advisory, so its budget alone never makes runs
	// incomparable.
	renderOnly := telemetryReceipt(t, 1, 250)
	renderOnly.Budgets[4].LimitMs, renderOnly.Budgets[4].Enabled = 9999, true
	renderOnly.Hash = telemetryBudgetPolicyHash(renderOnly)
	if got := compareBaselineTelemetry(current, telemetryComparisonRun("render", renderOnly, 4, nil), qualified); !got.Comparable {
		t.Fatalf("a browser render budget made runs incomparable: %+v", got.Reasons)
	}

	legacy := compareBaselineTelemetry(current, telemetryComparisonRun("legacy", nil, 4, nil), qualified)
	if legacy.Comparable || !slices.Contains(legacy.Reasons, telemetryReasonPolicyUnavailable) || legacy.Baseline.PolicyAvailability != "unavailable" {
		t.Fatalf("a run without a receipt compared: %+v", legacy)
	}

	capability := compareBaselineTelemetry(current, telemetryComparisonRun("capability", telemetryReceipt(t, 1, 250), 4, func(m *RunArchiveManifest) {
		m.ClockObservationsFile = "clock_observations.jsonl"
	}), qualified)
	if capability.Comparable || !sameStrings(capability.Reasons, []string{telemetryReasonCapabilityDiffers}) || !slices.Contains(capability.Baseline.SourceCapabilities, "clock_qualification") {
		t.Fatalf("source capability %+v", capability)
	}

	unqualified := compareBaselineTelemetry(current, telemetryComparisonRun("load", telemetryReceipt(t, 1, 250), 4, nil), rca.WorkloadQualification{Status: rca.BaselineUnqualified, Reasons: []string{rca.ReasonLevelsChanged}})
	if unqualified.Comparable || !sameStrings(unqualified.Reasons, []string{telemetryReasonWorkloadUnqualified}) {
		t.Fatalf("unqualified workload %+v", unqualified.Reasons)
	}
	partial := compareBaselineTelemetry(current, telemetryComparisonRun("partial", telemetryReceipt(t, 1, 250), 4, nil), rca.WorkloadQualification{Status: rca.BaselinePartial, Reasons: []string{rca.ReasonScriptChanged}})
	if !partial.Comparable || len(partial.Notes) == 0 {
		t.Fatalf("partial workload %+v", partial)
	}

	if line := baselineTelemetryComparisonLine(cadence); !strings.Contains(line, "not compared (cadence_differs)") || !strings.Contains(line, "1000 / 3000 ms") {
		t.Fatalf("export line %q", line)
	}
	if line := baselineTelemetryComparisonLine(same); !strings.Contains(line, "comparable") || !strings.Contains(line, "+8 pts") {
		t.Fatalf("export line %q", line)
	}
}

func reevaluationPolicy(window time.Duration, fanout, transport *float64) TelemetryReevaluationPolicy {
	seconds := int(window / time.Second)
	payload := TelemetryPolicyDefinition{Version: analysisPolicyVersion, ControllerFanoutMs: fanout, TransportLagMs: transport, SustainedWindowSeconds: &seconds}
	return resolveTelemetryReevaluationPolicy(&storage.AnalysisDefinition{ID: "policy", Revision: 1}, payload, "digest")
}

func telemetryInterval(at time.Time, status string, t *TelemetryHealth) IntervalSummaryRecord {
	record := IntervalSummaryRecord{Timestamp: at, Telemetry: t}
	if status != "" {
		record.TelemetryStatus = &TelemetryRunHealth{Status: status}
	}
	return record
}

func TestTelemetryReevaluationAppliesTheSustainedWindow(t *testing.T) {
	start := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	fanout := 100.0
	policy := reevaluationPolicy(2*time.Second, &fanout, nil)
	slow := func() *TelemetryHealth { return &TelemetryHealth{ControllerFanoutLagMs: 250} }
	fast := func() *TelemetryHealth { return &TelemetryHealth{ControllerFanoutLagMs: 5} }
	intervals := []IntervalSummaryRecord{
		telemetryInterval(start.Add(4*time.Second), "healthy", fast()),
		telemetryInterval(start, "healthy", slow()), // out of order: sorted by time
		telemetryInterval(start.Add(time.Second), "degraded", slow()),
		telemetryInterval(start.Add(2*time.Second), "breached", slow()),
		telemetryInterval(start.Add(3*time.Second), "", nil), // no telemetry: unknown, resets the streak
		telemetryInterval(start.Add(5*time.Second), "healthy", slow()),
	}
	var run TelemetryReevaluationRun
	reevaluateTelemetryIntervals(&run, intervals, policy, false)
	if !run.Answerable || run.Reevaluated == nil {
		t.Fatalf("unanswerable: %v", run.Unanswerable)
	}
	got := *run.Reevaluated
	if got.Intervals != 6 || got.Degraded != 3 || got.Breached != 1 || got.Healthy != 1 || got.Unknown != 1 {
		t.Fatalf("re-evaluated %+v", got)
	}
	if run.Original.Healthy != 3 || run.Original.Degraded != 1 || run.Original.Breached != 1 || run.Original.Unknown != 1 {
		t.Fatalf("original %+v", run.Original)
	}
	if run.StageConditions["controller_fanout"] != 4 {
		t.Fatalf("stage conditions %+v", run.StageConditions)
	}

	// A gap or an unscored interval ends a breach spell: the condition after
	// it starts a new one rather than continuing the old.
	transport := 100.0
	resetPolicy := reevaluationPolicy(2*time.Second, &fanout, &transport)
	unscored := &TelemetryHealth{TransportLagMs: 500, TransportLagQuality: &generatorrpc.ClockQuality{Reason: "uncalibrated"}}
	for name, between := range map[string]*TelemetryHealth{"gap": nil, "unscored": unscored} {
		sequence := []IntervalSummaryRecord{
			telemetryInterval(start, "", slow()),
			telemetryInterval(start.Add(time.Second), "", slow()),
			telemetryInterval(start.Add(2*time.Second), "", between),
			telemetryInterval(start.Add(3*time.Second), "", slow()),
		}
		var spell TelemetryReevaluationRun
		reevaluateTelemetryIntervals(&spell, sequence, resetPolicy, false)
		if spell.Reevaluated == nil || spell.Reevaluated.Breached != 0 || spell.Reevaluated.Degraded != 3 {
			t.Fatalf("%s did not end the breach spell: %+v", name, spell.Reevaluated)
		}
	}
}

func TestTelemetryReevaluationScoresOnlyWhatTheEvidenceCanAnswer(t *testing.T) {
	start := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	transport, zero := 100.0, 0.0
	policy := reevaluationPolicy(10*time.Second, &zero, &transport)
	qualified := &generatorrpc.ClockQuality{Qualified: true, UncertaintyMs: 1}
	intervals := []IntervalSummaryRecord{
		// Fan-out's budget is zero: disabled, never a condition.
		telemetryInterval(start, "", &TelemetryHealth{ControllerFanoutLagMs: 9000}),
		// A distributed run's transport age without its retained quality.
		telemetryInterval(start.Add(time.Second), "", &TelemetryHealth{TransportLagMs: 500}),
		telemetryInterval(start.Add(2*time.Second), "", &TelemetryHealth{TransportLagMs: 500, TransportLagQuality: &generatorrpc.ClockQuality{Reason: "uncalibrated"}}),
		telemetryInterval(start.Add(3*time.Second), "", &TelemetryHealth{TransportLagMs: 500, TransportLagQuality: qualified}),
		telemetryInterval(start.Add(4*time.Second), "", &TelemetryHealth{BrowserRenderLagMs: 99999}),
	}
	var run TelemetryReevaluationRun
	reevaluateTelemetryIntervals(&run, intervals, policy, true)
	got := run.Reevaluated
	if got == nil || got.Healthy != 2 || got.Unscored != 2 || got.Degraded != 1 {
		t.Fatalf("re-evaluated %+v", got)
	}
	if run.StageUnscored["transport:clock_quality_not_retained"] != 1 || run.StageUnscored["transport:uncalibrated"] != 1 || run.StageConditions["transport"] != 1 {
		t.Fatalf("stages %+v %+v", run.StageConditions, run.StageUnscored)
	}

	// A local run's own clock needs no quality.
	run = TelemetryReevaluationRun{}
	reevaluateTelemetryIntervals(&run, intervals[1:2], policy, false)
	if run.Reevaluated == nil || run.Reevaluated.Degraded != 1 {
		t.Fatalf("local transport %+v", run.Reevaluated)
	}

	// A sustained window shorter than the retained interval spacing, or no
	// retained telemetry at all, cannot be answered.
	sparse := []IntervalSummaryRecord{telemetryInterval(start, "", &TelemetryHealth{}), telemetryInterval(start.Add(5*time.Second), "", &TelemetryHealth{}), telemetryInterval(start.Add(10*time.Second), "", &TelemetryHealth{})}
	run = TelemetryReevaluationRun{}
	reevaluateTelemetryIntervals(&run, sparse, reevaluationPolicy(2*time.Second, nil, nil), false)
	if run.Answerable || run.Reevaluated != nil || len(run.Unanswerable) != 1 || !strings.HasPrefix(run.Unanswerable[0], "sustained_window_shorter_than_retained_interval_spacing:5000ms") {
		t.Fatalf("sparse evidence %+v", run)
	}
	run = TelemetryReevaluationRun{}
	reevaluateTelemetryIntervals(&run, []IntervalSummaryRecord{telemetryInterval(start, "healthy", nil)}, policy, false)
	if run.Answerable || run.Unanswerable[0] != "no_retained_telemetry" || run.Original.Healthy != 1 {
		t.Fatalf("no telemetry %+v", run)
	}
}

func TestTelemetryReevaluationPolicyResolvesBuiltInDefaults(t *testing.T) {
	zero := 0.0
	policy := resolveTelemetryReevaluationPolicy(&storage.AnalysisDefinition{ID: "p", Name: "Strict", Revision: 3}, TelemetryPolicyDefinition{Version: analysisPolicyVersion, ControllerFanoutMs: &zero}, "abc")
	byStage := map[string]TelemetryReevaluationBudget{}
	for _, budget := range policy.Budgets {
		byStage[budget.Stage] = budget
	}
	if got := byStage["sample_age"]; !got.Enabled || got.LimitMs != defaultTelemetrySampleAgeBudgetMs || got.Source != "built_in_default" {
		t.Fatalf("default sample age %+v", got)
	}
	if got := byStage["controller_fanout"]; got.Enabled || got.Source != "policy" {
		t.Fatalf("a zero budget must disable its stage: %+v", got)
	}
	if got := byStage["browser_render"]; got.Enabled || !got.Advisory {
		t.Fatalf("browser render is scored: %+v", got)
	}
	if policy.SustainedBreachWindowMs != telemetrySustainedBreachWindow.Milliseconds() || policy.Name != "Strict" || policy.Revision != 3 {
		t.Fatalf("policy %+v", policy)
	}
}

// telemetryReevaluationArchive writes a finalized run archive whose interval
// rows retain telemetry, with its manifest digest recorded.
func telemetryReevaluationArchive(t *testing.T, server *Server, store *storage.SQLiteStore, id string, fanoutLags []float64) string {
	t.Helper()
	start := time.Date(2026, 10, 8, 11, 0, 0, 0, time.UTC)
	root := t.TempDir()
	receipt := telemetryReceipt(t, 1, 250)
	provenance := RunArchiveProvenance{ProjectID: storage.DefaultProjectID, StartedAt: &start, TelemetryPolicy: receipt}
	manifest := RunArchiveManifest{SchemaVersion: archiveSchemaVersion, TestID: id, TestName: id, Status: "COMPLETE", CreatedAt: start, CompletedAt: start.Add(time.Minute), Provenance: provenance, InitialProvenance: &provenance, IntervalSummariesFile: "interval_summaries.jsonl", IntervalSummaryCount: int64(len(fanoutLags))}
	var rows bytes.Buffer
	for i, lag := range fanoutLags {
		status := "healthy"
		if lag > 250 {
			status = "degraded"
		}
		if err := json.NewEncoder(&rows).Encode(IntervalSummaryRecord{Timestamp: start.Add(time.Duration(i) * time.Second), Telemetry: &TelemetryHealth{ControllerFanoutLagMs: lag}, TelemetryStatus: &TelemetryRunHealth{Status: status}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, manifest.IntervalSummariesFile), rows.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), manifestBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(manifestBytes)
	if err := store.SaveTest(&storage.TestRecord{ID: id, Name: id, ProjectID: storage.DefaultProjectID, ScenarioID: "scenario", Status: "COMPLETE", Groups: []any{}, StartTime: start}); err != nil {
		t.Fatal(err)
	}
	end := start.Add(time.Minute)
	record := &storage.RunArchiveRecord{TestID: id, SchemaVersion: archiveSchemaVersion, ArchivePath: root, ManifestPath: filepath.Join(root, "manifest.json"), ManifestSHA256: hex.EncodeToString(sum[:]), CreatedAt: start, CompletedAt: end, IntervalSummaryCount: int64(len(fanoutLags))}
	if err := store.SaveRunArchive(record); err != nil {
		t.Fatal(err)
	}
	server.runner.mu.Lock()
	server.runner.tests[id] = &ManagedTest{ID: id, Name: id, ProjectID: storage.DefaultProjectID, Status: "COMPLETE", StartedAt: &start, EndedAt: &end}
	server.runner.archives[id] = record
	server.runner.mu.Unlock()
	return root
}

func saveTelemetryPolicy(t *testing.T, store *storage.SQLiteStore, id string, fanoutMs float64) {
	t.Helper()
	body, _ := json.Marshal(TelemetryPolicyDefinition{Version: analysisPolicyVersion, ControllerFanoutMs: &fanoutMs})
	if _, err := store.SaveAnalysisDefinition(storage.AnalysisDefinition{ID: id, ProjectID: storage.DefaultProjectID, Kind: storage.AnalysisDefinitionKindTelemetryPolicy, Name: "Common", Revision: 1, Definition: body}, 0); err != nil {
		t.Fatal(err)
	}
}

func treeDigest(t *testing.T, root string) string {
	t.Helper()
	hash := sha256.New()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(root, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		hash.Write([]byte(entry.Name()))
		hash.Write(data)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// The re-evaluation job classifies both runs under one named policy, is
// reproducible from its hashed inputs, and leaves the archives untouched.
func TestTelemetryReevaluationJobIsReproducibleAndDerived(t *testing.T) {
	server, store := baselineTestServer(t)
	currentRoot := telemetryReevaluationArchive(t, server, store, "reeval-current", []float64{10, 400, 400, 400, 10})
	baselineRoot := telemetryReevaluationArchive(t, server, store, "reeval-baseline", []float64{10, 10, 10, 10, 10})
	saveTelemetryPolicy(t, store, "common", 100)
	before := treeDigest(t, currentRoot) + treeDigest(t, baselineRoot)

	request := json.RawMessage(`{"runs":["reeval-current","reeval-baseline"],"policy":{"id":"common","revision":1}}`)
	kind, ok := lookupEvaluationKind(telemetryReevaluationKind)
	if !ok {
		t.Fatal("telemetry_acceptance is not registered")
	}
	fingerprint, err := kind.Prepare(server, storage.DefaultProjectID, request)
	if err != nil {
		t.Fatal(err)
	}
	job := &storage.EvaluationJob{ProjectID: storage.DefaultProjectID, Kind: telemetryReevaluationKind, Request: string(request), InputFingerprint: fingerprint}
	run := func() []byte {
		result, err := kind.Run(context.Background(), server, job)
		if err != nil {
			t.Fatal(err)
		}
		body, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	first, second := run(), run()
	if !bytes.Equal(first, second) {
		t.Fatal("the same hashed inputs produced different results")
	}
	var result TelemetryReevaluationResult
	if err := json.Unmarshal(first, &result); err != nil {
		t.Fatal(err)
	}
	if result.InputFingerprint != fingerprint || !result.AllAnswerable || len(result.Runs) != 2 || result.Policy.SHA256 == "" {
		t.Fatalf("result %+v", result)
	}
	current := result.Runs[0].Reevaluated
	if current == nil || current.Degraded != 3 || current.Healthy != 2 || result.Runs[1].Reevaluated.Healthy != 5 {
		t.Fatalf("re-evaluated %+v / %+v", current, result.Runs[1].Reevaluated)
	}
	if result.Runs[0].Original.Degraded != 3 || result.Runs[0].OriginalPolicyHash == "" {
		t.Fatalf("original %+v", result.Runs[0])
	}
	if after := treeDigest(t, currentRoot) + treeDigest(t, baselineRoot); after != before {
		t.Fatal("re-evaluation changed an archive")
	}

	// A changed archive no longer matches the queued fingerprint.
	stale := *job
	stale.InputFingerprint = strings.Repeat("0", 64)
	if _, err := kind.Run(context.Background(), server, &stale); err == nil || !strings.Contains(err.Error(), "changed after this evaluation was queued") {
		t.Fatalf("stale inputs: %v", err)
	}
}

func TestTelemetryReevaluationRefusesForeignOrInvalidInputs(t *testing.T) {
	server, store := baselineTestServer(t)
	telemetryReevaluationArchive(t, server, store, "reeval-a", []float64{10, 10})
	saveTelemetryPolicy(t, store, "common", 100)
	acceptance, _ := json.Marshal(map[string]any{"version": analysisPolicyVersion, "mode": "advisory", "min_wall_time_coverage": 0.5})
	if _, err := decodeAnalysisPolicy(storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, acceptance); err != nil {
		t.Fatalf("fixture acceptance policy invalid: %v", err)
	}
	if _, err := store.SaveAnalysisDefinition(storage.AnalysisDefinition{ID: "acceptance", ProjectID: storage.DefaultProjectID, Kind: storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, Name: "Acceptance", Revision: 1, Definition: acceptance}, 0); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct{ request, project, reason string }{
		"no runs":          {`{"runs":[],"policy":{"id":"common","revision":1}}`, "", "1 to 10 runs"},
		"latest revision":  {`{"runs":["reeval-a"],"policy":{"id":"common","revision":0}}`, "", "explicit revision"},
		"unknown run":      {`{"runs":["missing"],"policy":{"id":"common","revision":1}}`, "", "not in this project"},
		"duplicate":        {`{"runs":["reeval-a","reeval-a"],"policy":{"id":"common","revision":1}}`, "", "named twice"},
		"wrong kind":       {`{"runs":["reeval-a"],"policy":{"id":"acceptance","revision":1}}`, "", "not a telemetry_policy"},
		"unknown policy":   {`{"runs":["reeval-a"],"policy":{"id":"nope","revision":1}}`, "", "not a telemetry_policy"},
		"unknown field":    {`{"runs":["reeval-a"],"policy":{"id":"common","revision":1},"sql":"x"}`, "", "invalid telemetry re-evaluation request"},
		"too many runs":    {`{"runs":["reeval-a","b","c","d","e","f","g","h","i","j","k"],"policy":{"id":"common","revision":1}}`, "", "1 to 10 runs"},
		"other project id": {`{"runs":["reeval-a"],"policy":{"id":"common","revision":1}}`, "other-project", "not a telemetry_policy"},
	} {
		project := storage.DefaultProjectID
		if tc.project != "" {
			project = tc.project
		}
		if _, err := prepareTelemetryReevaluation(context.Background(), server, project, json.RawMessage(tc.request)); err == nil || !strings.Contains(err.Error(), tc.reason) {
			t.Fatalf("%s: %v, want %q", name, err, tc.reason)
		}
	}
	// A run without a finalized archive (no manifest digest) is refused.
	server.runner.mu.Lock()
	server.runner.archives["reeval-a"].ManifestSHA256 = ""
	server.runner.mu.Unlock()
	if _, err := prepareTelemetryReevaluation(context.Background(), server, storage.DefaultProjectID, json.RawMessage(`{"runs":["reeval-a"],"policy":{"id":"common","revision":1}}`)); err == nil || !strings.Contains(err.Error(), "no finalized archive") {
		t.Fatalf("unfinalized archive: %v", err)
	}
}

// Through the API: the job is queued, run by the evaluation worker and
// published as a hashed result.
func TestTelemetryReevaluationRunsThroughTheEvaluationAPI(t *testing.T) {
	server, store := baselineTestServer(t)
	telemetryReevaluationArchive(t, server, store, "reeval-api", []float64{10, 400, 10})
	saveTelemetryPolicy(t, store, "common", 100)
	body := `{"kind":"telemetry_acceptance","request":{"runs":["reeval-api"],"policy":{"id":"common","revision":1}}}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/analysis/evaluations?project_id="+storage.DefaultProjectID, strings.NewReader(body))
	recorder := httptest.NewRecorder()
	server.handleCreateEvaluationJob(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("create %d %s", recorder.Code, recorder.Body)
	}
	var created EvaluationJobResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if ran, err := server.runNextEvaluationJob(context.Background()); err != nil || !ran {
		t.Fatalf("worker ran=%v err=%v", ran, err)
	}
	job, err := store.GetEvaluationJob(storage.DefaultProjectID, created.ID)
	if err != nil || job.State != storage.EvaluationJobReady || job.ResultSHA256 == "" {
		t.Fatalf("job %+v %v", job, err)
	}
	var result TelemetryReevaluationResult
	if err := json.Unmarshal([]byte(job.Result), &result); err != nil || result.Runs[0].Reevaluated == nil || result.Runs[0].Reevaluated.Degraded != 1 {
		t.Fatalf("result %s %v", job.Result, err)
	}
}

// The comparison engine attaches one telemetry compatibility verdict per
// baseline; runs archived without a policy receipt are never compared.
func TestBaselineComparisonAttachesTelemetryCompatibility(t *testing.T) {
	server, store := baselineTestServer(t)
	current := comparisonArchiveFixture(t, server, store, "current", 200, 150)
	baseline := comparisonArchiveFixture(t, server, store, "baseline", 100, 100)
	request := BaselineComparisonRequest{BaselineIDs: []string{baseline.ID}, Population: "transaction"}
	p := defaultBaselineComparisonPolicy()
	a, err := server.readBaselineComparisonRun(context.Background(), current, request, p)
	if err != nil {
		t.Fatal(err)
	}
	b, err := server.readBaselineComparisonRun(context.Background(), baseline, request, p)
	if err != nil {
		t.Fatal(err)
	}
	result := compareBaselineRuns(a, []*baselineComparisonRun{b}, request, p)
	if result == nil || len(result.TelemetryComparisons) != 1 {
		t.Fatalf("telemetry comparisons %+v", result)
	}
	got := result.TelemetryComparisons[0]
	if got.BaselineRunID != baseline.ID || got.Comparable || !slices.Contains(got.Reasons, telemetryReasonPolicyUnavailable) || got.Scope != "whole_run" {
		t.Fatalf("telemetry comparison %+v", got)
	}
}

// The comparison exports carry each telemetry compatibility verdict.
func TestBaselineComparisonExportsCarryTelemetryCompatibility(t *testing.T) {
	current := telemetryComparisonRun("current", telemetryReceipt(t, 1, 250), 12, nil)
	verdict := compareBaselineTelemetry(current, telemetryComparisonRun("base", telemetryReceipt(t, 3, 250), 4, nil), rca.WorkloadQualification{Status: rca.BaselineQualified})
	data := &runArchiveHTMLReportData{Report: &RunArchiveReport{BaselineComparison: &BaselineComparisonSnapshot{ID: "snap", TelemetryComparisons: []BaselineTelemetryComparison{verdict}}}}
	renderer := baselineComparisonReportRenderer()
	html, err := renderer.HTML(data)
	if err != nil || !strings.Contains(string(html), "not compared (cadence_differs)") {
		t.Fatalf("HTML %v: %s", err, html)
	}
	var lines []string
	renderer.PDF(data, &lines)
	if !slices.ContainsFunc(lines, func(line string) bool { return strings.Contains(line, "not compared (cadence_differs)") }) {
		t.Fatalf("PDF %q", lines)
	}
	var rows [][4]string
	if err := renderer.CSV(data, func(section, key, metric, value string) error {
		rows = append(rows, [4]string{section, key, metric, value})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(rows, func(row [4]string) bool {
		return row[0] == "baseline_telemetry" && row[1] == "base" && row[2] == "telemetry_compatibility" && strings.Contains(row[3], `"cadence_differs"`)
	}) {
		t.Fatalf("CSV %q", rows)
	}
}

// The fingerprint follows the evidence: a run whose archive manifest digest
// changes no longer matches, and a run is refused for a project it is not
// in even when that project has a policy of the same name.
func TestTelemetryReevaluationFingerprintFollowsArchivesAndProject(t *testing.T) {
	server, store := baselineTestServer(t)
	telemetryReevaluationArchive(t, server, store, "reeval-f", []float64{10, 10})
	saveTelemetryPolicy(t, store, "common", 100)
	request := json.RawMessage(`{"runs":["reeval-f"],"policy":{"id":"common","revision":1}}`)
	first, err := prepareTelemetryReevaluation(context.Background(), server, storage.DefaultProjectID, request)
	if err != nil {
		t.Fatal(err)
	}
	server.runner.mu.Lock()
	server.runner.archives["reeval-f"].ManifestSHA256 = strings.Repeat("e", 64)
	server.runner.mu.Unlock()
	second, err := prepareTelemetryReevaluation(context.Background(), server, storage.DefaultProjectID, request)
	if err != nil || second.fingerprint == first.fingerprint {
		t.Fatalf("fingerprint ignored the archive digest: %v", err)
	}

	if err := store.SaveProject(&storage.Project{ID: "other-project", Name: "Other"}); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(TelemetryPolicyDefinition{Version: analysisPolicyVersion, ControllerFanoutMs: ptr(100.0)})
	if _, err := store.SaveAnalysisDefinition(storage.AnalysisDefinition{ID: "other-common", ProjectID: "other-project", Kind: storage.AnalysisDefinitionKindTelemetryPolicy, Name: "Common", Revision: 1, Definition: body}, 0); err != nil {
		t.Fatal(err)
	}
	foreign := json.RawMessage(`{"runs":["reeval-f"],"policy":{"id":"other-common","revision":1}}`)
	if _, err := prepareTelemetryReevaluation(context.Background(), server, "other-project", foreign); err == nil || !strings.Contains(err.Error(), "not in this project") {
		t.Fatalf("a run of another project was accepted: %v", err)
	}
}

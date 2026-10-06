package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/rca"
	"github.com/jasonczech/apexload/pkg/storage"
)

// rcaE2E runs real local load against an in-process HTTP target and drives
// the root-cause feature through the controller's own handlers, with nothing
// faked. Its tests take minutes, so they are opt-in: APEXLOAD_RCA_E2E=1.
type rcaE2E struct {
	t      *testing.T
	server *Server
	store  *storage.SQLiteStore
	// slowFrom, when set, is the time from which the target answers in
	// 60 ms instead of 4 ms.
	slowFrom atomic.Int64
}

func newRCAE2E(t *testing.T) *rcaE2E {
	t.Helper()
	if os.Getenv("APEXLOAD_RCA_E2E") != "1" {
		t.Skip("set APEXLOAD_RCA_E2E=1 to run the root-cause end-to-end tests")
	}
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	t.Setenv("APEXLOAD_RCA_V2_PROJECTS", storage.DefaultProjectID)
	t.Setenv("APEXLOAD_RCA_V2_PRESENT_PROJECTS", storage.DefaultProjectID)
	h := &rcaE2E{t: t}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		delay := 4 * time.Millisecond
		if from := h.slowFrom.Load(); from != 0 && time.Now().UnixNano() >= from {
			delay = 60 * time.Millisecond
		}
		time.Sleep(delay)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(target.Close)
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	h.store = store
	h.server = NewServer(ServerConfig{Store: store, MaxConcurrent: 2})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = h.server.Shutdown(ctx)
		_ = store.Close()
	})
	h.server.runner.StoreScript(&dsl.Script{
		ID: "rca-e2e", Name: "RCA end to end", Protocol: "http", Version: "1.0.0",
		Config:  dsl.ScriptConfig{BaseURL: target.URL},
		Actions: []dsl.Action{{Name: "orders", Type: "request", Method: "GET", Target: "/orders"}},
	})
	return h
}

// run runs a test to completion and waits for the finalize path to write
// the sidecars, which follow the archive's publication.
func (h *rcaE2E) run(req CreateTestRequest, started func()) (*ManagedTest, *RunArchiveManifest) {
	t := h.t
	t.Helper()
	req.ScriptID = "rca-e2e"
	run, err := h.server.runner.CreateTest(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.server.runner.StartTest(run.ID); err != nil {
		t.Fatal(err)
	}
	if started != nil {
		started()
	}
	waitForTestStatusWithin(t, h.server.runner, run.ID, 6*time.Minute, "COMPLETE")
	var last *RunArchiveManifest
	for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if r, err := h.store.GetRunArchive(run.ID); err == nil && r != nil {
			if m, err := loadRunArchiveManifestFromRecord(r); err == nil {
				last = m
				if m.ExemplarsFile != "" && m.PhaseEventsFile != "" {
					return run, m
				}
			}
		}
	}
	if last != nil {
		t.Fatalf("sidecars after a minute: distributions=%q exemplars=%q phase_events=%q", last.IntervalDistributionsFile, last.ExemplarsFile, last.PhaseEventsFile)
	}
	t.Fatal("the run was never archived")
	return nil, nil
}

// call sends a request through the controller's routes and decodes the reply.
func (h *rcaE2E) call(method, path string, body any, want int, out any) {
	t := h.t
	t.Helper()
	var encoded []byte
	if body != nil {
		encoded, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(encoded))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	h.server.mux.ServeHTTP(resp, req)
	if resp.Code != want {
		t.Fatalf("%s %s = %d, want %d: %s", method, path, resp.Code, want, resp.Body.String())
	}
	if out != nil {
		if err := json.Unmarshal(resp.Body.Bytes(), out); err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
	}
}

// analyze queues an analysis, runs it on the worker and returns it.
func (h *rcaE2E) analyze(testID string, request map[string]any) RCAAnalysisResponse {
	t := h.t
	t.Helper()
	base := "/api/v1/tests/" + testID + "/archive/root-cause/analyses"
	var queued RCAAnalysisResponse
	h.call(http.MethodPost, base, request, http.StatusAccepted, &queued)
	if ran, err := h.server.runNextRCAAnalysis(context.Background(), nil); err != nil || !ran {
		t.Fatalf("worker ran=%t err=%v", ran, err)
	}
	var done RCAAnalysisResponse
	h.call(http.MethodGet, base+"/"+queued.AnalysisID, nil, http.StatusOK, &done)
	if done.State != storage.RCAStateCompleted || done.Assessment == nil {
		t.Fatalf("analysis = %+v", done)
	}
	return done
}

func logAssessment(t *testing.T, a *rca.Assessment) {
	t.Helper()
	encoded, _ := json.MarshalIndent(struct {
		Outcome     string
		Verdict     string
		Anomalies   []rca.Anomaly
		Hypotheses  []rca.Hypothesis
		Coverage    []rca.Coverage
		Limitations []rca.Limitation
		Levels      []rca.LoadLevel
		Evaluated   int
	}{a.Outcome, a.Validity.Verdict, a.Anomalies, a.Hypotheses, a.Coverage, a.Limitations, a.Detection.Levels, a.Detection.SeriesEvaluated}, "", "  ")
	t.Logf("assessment:\n%s", encoded)
}

// TestRCAEndToEndFromARealRun: a run whose latency steps up mid-run, through
// the sidecars, a v2 analysis, the report's rca section, the bundle, the
// narration check, the exemplars and a comparison with a fast baseline run.
// It takes about four minutes.
func TestRCAEndToEndFromARealRun(t *testing.T) {
	h := newRCAE2E(t)
	baselineRun, _ := h.run(CreateTestRequest{Name: "RCA end to end baseline", VUsers: 4, Duration: "100s"}, nil)
	const stepAfter = 60 * time.Second
	var stepAt time.Time
	run, manifest := h.run(CreateTestRequest{Name: "RCA end to end", VUsers: 4, Duration: "100s"}, func() {
		stepAt = time.Now().Add(stepAfter)
		h.slowFrom.Store(stepAt.UnixNano())
	})
	t.Logf("sidecars: distributions=%d (%s) exemplars=%d phase_events=%d", manifest.IntervalDistributionCount, manifest.IntervalDistributionsSource, manifest.ExemplarCount, manifest.PhaseEventCount)
	// A run that held one level still archives its stop.
	if manifest.IntervalDistributionCount == 0 || manifest.ExemplarCount == 0 || manifest.PhaseEventCount == 0 {
		t.Fatalf("empty sidecars: %+v", manifest)
	}

	done := h.analyze(run.ID, map[string]any{})
	a := done.Assessment
	logAssessment(t, a)
	// The run held one steady level, which detection and validity judge.
	if len(a.Detection.Levels) != 1 || a.Detection.SeriesEvaluated == 0 || a.Validity.Verdict != rca.ValidityValid || done.ArchiveChanged {
		t.Fatalf("levels %+v, %d series evaluated, validity %s, archive changed %t", a.Detection.Levels, a.Detection.SeriesEvaluated, a.Validity.Verdict, done.ArchiveChanged)
	}
	for _, c := range a.Coverage {
		if c.Evidence == "phase_events" && c.Status != rca.CoverageComplete {
			t.Fatalf("phase event coverage %+v", c)
		}
	}
	// The latency step is found on a p95 series, after the step. A script
	// without authored transactions is measured by the engine's automatic
	// per-iteration transaction and by its requests, which name what slowed.
	var step *rca.Anomaly
	named := false
	for i := range a.Anomalies {
		anomaly := &a.Anomalies[i]
		if !strings.HasSuffix(anomaly.Series, "/p95_ms") || anomaly.Direction != rca.DirectionIncrease {
			continue
		}
		if step == nil && (strings.HasPrefix(anomaly.Series, "transaction/") || strings.HasPrefix(anomaly.Series, "action/")) {
			step = anomaly
		}
		named = named || anomaly.Series == "action/orders/p95_ms"
	}
	if step == nil || !named {
		t.Fatalf("the latency step was not detected (named request %t)", named)
	}
	if gap := step.Onset.Sub(stepAt); gap < -10*time.Second || gap > 15*time.Second {
		t.Fatalf("onset %s is %s from the step at %s", step.Onset, gap, stepAt)
	}

	base := "/api/v1/tests/" + run.ID + "/archive"
	// The report presents this analysis in its rca section.
	var report RunArchiveReport
	h.call(http.MethodGet, base+"/report", nil, http.StatusOK, &report)
	if report.RootCauseView == nil || report.RootCauseView.Source != reportRootCauseV2 || report.RootCauseView.AnalysisID != done.AnalysisID || len(report.RootCauseView.Notes) != 0 {
		t.Fatalf("report root cause = %+v", report.RootCauseView)
	}
	if report.Validity == nil || report.Validity.Verdict != a.Validity.Verdict {
		t.Fatalf("report validity %+v differs from the analysis's %q", report.Validity, a.Validity.Verdict)
	}

	// The bundle carries the excerpt the anomaly's evidence names, and a
	// narrative quoting the bundle passes the check.
	var bundle RCABundle
	h.call(http.MethodGet, base+"/root-cause/analyses/"+done.AnalysisID+"/bundle", nil, http.StatusOK, &bundle)
	if len(step.Evidence) == 0 {
		t.Fatal("the anomaly cites no evidence")
	}
	var excerpt *RCAEvidenceExcerpt
	for i := range bundle.Excerpts {
		if bundle.Excerpts[i].RefID == step.Evidence[0].ID {
			excerpt = &bundle.Excerpts[i]
		}
	}
	if excerpt == nil || len(excerpt.Points) == 0 {
		t.Fatalf("no excerpt for %s in %d excerpts", step.Evidence[0].ID, len(bundle.Excerpts))
	}
	var check RCANarrationCheck
	narrative := "The p95 rose at " + step.Onset.UTC().Format(time.RFC3339) + " [" + step.Evidence[0].ID + "]."
	h.call(http.MethodPost, base+"/root-cause/analyses/"+done.AnalysisID+"/narration-check", map[string]any{"narrative": narrative}, http.StatusOK, &check)
	if !check.OK {
		t.Fatalf("narration %q failed: %+v", narrative, check.Issues)
	}

	// Exemplars name requests whose trace context was sent.
	var page ExemplarPage
	h.call(http.MethodGet, base+"/exemplars?name=orders", nil, http.StatusOK, &page)
	if page.Total == 0 || len(page.Exemplars) == 0 || len(page.Exemplars[0].TraceID) != 32 {
		t.Fatalf("exemplars = %+v", page)
	}

	// Against the fast baseline run, which offered the same workload, the
	// slow level is found at the same load.
	b := h.analyze(run.ID, map[string]any{"baseline_test_id": baselineRun.ID}).Assessment
	qualified := false
	for _, c := range b.Coverage {
		qualified = qualified || c.Evidence == "baseline" && strings.HasPrefix(c.Detail, rca.BaselineQualified)
	}
	loadScoped := false
	for _, anomaly := range b.Anomalies {
		loadScoped = loadScoped || anomaly.Scope == rca.ScopeLoad && strings.HasSuffix(anomaly.Series, "/p95_ms") && anomaly.Direction == rca.DirectionIncrease && anomaly.Baseline.TestID == baselineRun.ID
	}
	if !qualified || !loadScoped || b.Inputs.Baseline == "" {
		logAssessment(t, b)
		t.Fatalf("against the baseline: qualified %t, load-scoped %t", qualified, loadScoped)
	}
}

// TestRCAEndToEndStagedRun: a run that steps from two to four VUsers. Its
// stages are archived as load-phase events, detection finds both levels,
// and the load step itself is not reported as an onset. It takes about
// three and a half minutes.
func TestRCAEndToEndStagedRun(t *testing.T) {
	h := newRCAE2E(t)
	stages := []storage.LoadStage{
		{Type: "ramp_up", Duration: "2s", TargetVUsers: 2}, {Type: "hold", Duration: "100s", TargetVUsers: 2},
		{Type: "ramp_up", Duration: "2s", TargetVUsers: 4}, {Type: "hold", Duration: "100s", TargetVUsers: 4},
	}
	run, manifest := h.run(CreateTestRequest{Name: "RCA staged", Duration: "204s", Groups: []CreateGroupEntry{{Name: "buyers", ScriptID: "rca-e2e", VUsers: 4, Stages: stages}}}, nil)
	record, err := h.store.GetRunArchive(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(record.ArchivePath, manifest.PhaseEventsFile))
	if err != nil {
		t.Fatal(err)
	}
	reached := map[int]bool{}
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		var event PhaseEvent
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatal(err)
		}
		if event.Kind == PhaseLevelReached && event.Level != nil {
			reached[*event.Level] = true
		}
	}
	if !reached[2] || !reached[4] {
		t.Fatalf("archived phase events %s", data)
	}

	a := h.analyze(run.ID, map[string]any{}).Assessment
	logAssessment(t, a)
	if len(a.Detection.Levels) != 2 || a.Detection.Levels[0].VUsers != 2 || a.Detection.Levels[1].VUsers != 4 {
		t.Fatalf("levels %+v", a.Detection.Levels)
	}
	// Every interval at a level is compared only with earlier intervals at
	// the same load, so stepping up is not an onset.
	for _, anomaly := range a.Anomalies {
		if anomaly.Scope == rca.ScopeTime && strings.HasSuffix(anomaly.Series, "/throughput") && anomaly.Direction == rca.DirectionIncrease {
			t.Fatalf("the load step was reported as an onset: %+v", anomaly)
		}
	}
}

package api

import (
	"encoding/json"
	"math"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

func exactQuantile(sorted []float64, q float64) float64 {
	index := int(math.Ceil(q*float64(len(sorted)))) - 1
	return sorted[max(0, min(index, len(sorted)-1))]
}

// Two stage digests merged answer the same percentiles as the exact
// combined samples, within the digest's accuracy (a small fraction of the
// spread); independent stages never sum.
func TestTelemetryStageDigestMergeMatchesIndependentPercentiles(t *testing.T) {
	random := rand.New(rand.NewSource(7))
	var all []float64
	first, second := engine.NewTDigest(telemetryStageCompression), engine.NewTDigest(telemetryStageCompression)
	for i := 0; i < 20000; i++ {
		value := 5 + random.ExpFloat64()*40
		if i%2 == 0 {
			first.Add(value)
		} else {
			second.Add(value)
		}
		all = append(all, value)
	}
	merged := engine.NewTDigestFromSnapshot(first.Snapshot())
	merged.Merge(engine.NewTDigestFromSnapshot(second.Snapshot()))
	sort.Float64s(all)
	spread := all[len(all)-1] - all[0]
	for _, q := range []float64{0.5, 0.95, 0.99} {
		got, want := merged.Quantile(q), exactQuantile(all, q)
		if math.Abs(got-want) > 0.02*spread {
			t.Fatalf("p%.0f merged %.3f, exact %.3f (spread %.1f)", q*100, got, want, spread)
		}
	}
	if merged.Count() != int64(len(all)) {
		t.Fatalf("merged count %d", merged.Count())
	}
}

func TestTelemetryStageRecorderReportsOnlySampledStages(t *testing.T) {
	recorder := newTelemetryStageRecorder()
	for _, value := range []float64{10, 20, 30, 40, 1000} {
		recorder.observe(telemetryStageControllerFanout, value)
	}
	recorder.observe(telemetryStageControllerFanout, math.NaN())
	recorder.exclude(telemetryStageTransport, "clock_unqualified")
	stages := recorder.snapshot()
	if len(stages) != len(telemetryStageOrder) {
		t.Fatalf("stages %d", len(stages))
	}
	byStage := map[string]TelemetryStageDistribution{}
	for _, stage := range stages {
		byStage[stage.Stage] = stage
	}
	fanout := byStage[telemetryStageControllerFanout]
	if fanout.Availability != "measured" || fanout.Samples != 5 || fanout.MaxMs == nil || *fanout.MaxMs != 1000 || fanout.P50Ms == nil || fanout.Excluded["invalid"] != 1 {
		t.Fatalf("fan-out %+v", fanout)
	}
	transport := byStage[telemetryStageTransport]
	if transport.Availability != "unavailable" || transport.P95Ms != nil || transport.Excluded["clock_unqualified"] != 1 {
		t.Fatalf("an unsampled stage reported percentiles: %+v", transport)
	}
	if render := byStage[telemetryStageBrowserRender]; !render.Advisory || render.Availability != "unavailable" {
		t.Fatalf("render %+v", render)
	}
	if digests := recorder.digests(); len(digests) != 1 || digests[telemetryStageControllerFanout].Count != 5 {
		t.Fatalf("digests %+v", digests)
	}
}

// A generator batch is sampled once however often the aggregate re-reads
// it, and its transport lag only once the generator's clock is qualified.
func TestTelemetryStageRecorderSamplesEachGeneratorBatchOnce(t *testing.T) {
	recorder := newTelemetryStageRecorder()
	qualified := &generatorrpc.ClockQuality{Qualified: true}
	batch := func(sequence uint64, quality *generatorrpc.ClockQuality) generatorrpc.MetricsBatch {
		return generatorrpc.MetricsBatch{SourceEpoch: "epoch-a", BatchSequence: sequence, Telemetry: &generatorrpc.MetricsTelemetry{GeneratorProcessingLagMs: 12, TransportLagMs: 30, TransportLagQuality: quality}}
	}
	recorder.observeGeneratorBatch("gen-a", batch(1, qualified))
	recorder.observeGeneratorBatch("gen-a", batch(1, qualified)) // re-aggregated
	recorder.observeGeneratorBatch("gen-a", batch(2, &generatorrpc.ClockQuality{Reason: "uncalibrated"}))
	recorder.observeGeneratorBatch("gen-b", batch(1, qualified)) // another generator
	byStage := map[string]TelemetryStageDistribution{}
	for _, stage := range recorder.snapshot() {
		byStage[stage.Stage] = stage
	}
	if got := byStage[telemetryStageGeneratorProcessing]; got.Samples != 3 {
		t.Fatalf("processing %+v", got)
	}
	if got := byStage[telemetryStageTransport]; got.Samples != 2 || got.Excluded["clock_unqualified"] != 1 {
		t.Fatalf("transport %+v", got)
	}
	// A new process (epoch) restarts its sequence.
	recorder.observeGeneratorBatch("gen-a", generatorrpc.MetricsBatch{SourceEpoch: "epoch-b", BatchSequence: 1, Telemetry: &generatorrpc.MetricsTelemetry{GeneratorProcessingLagMs: 9}})
	for _, stage := range recorder.snapshot() {
		if stage.Stage == telemetryStageGeneratorProcessing && stage.Samples != 4 {
			t.Fatalf("a restarted generator's batch was dropped: %+v", stage)
		}
	}
}

func TestClientRenderSamplesCountOnlyVisibleRenders(t *testing.T) {
	started := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	now := started.Add(10 * time.Minute)
	for _, tc := range []struct {
		sample clientRenderSample
		want   bool
		reason string
	}{
		{clientRenderSample{ReceiveToRenderMs: 16, State: "visible", ObservedAt: now}, true, ""},
		{clientRenderSample{ReceiveToRenderMs: 16, State: "background", ObservedAt: now}, false, "background_tab"},
		{clientRenderSample{ReceiveToRenderMs: 16, State: "disconnected", ObservedAt: now}, false, "disconnected"},
		{clientRenderSample{ReceiveToRenderMs: 16, State: "hidden", ObservedAt: now}, false, "unknown_state"},
		{clientRenderSample{ReceiveToRenderMs: -1, State: "visible", ObservedAt: now}, false, "invalid"},
		{clientRenderSample{ReceiveToRenderMs: 90_000, State: "visible", ObservedAt: now}, false, "stalled_render"},
		{clientRenderSample{ReceiveToRenderMs: 16, State: "visible"}, false, "missing_time"},
		{clientRenderSample{ReceiveToRenderMs: 16, State: "visible", ObservedAt: started.Add(-time.Hour)}, false, "before_run"},
		{clientRenderSample{ReceiveToRenderMs: 16, State: "visible", ObservedAt: now.Add(time.Hour)}, false, "future_time"},
		{clientRenderSample{ReceiveToRenderMs: 16, State: "visible", ObservedAt: started.Add(-time.Minute)}, true, ""},
	} {
		if got, reason := admitClientRenderSample(tc.sample, started, now); got != tc.want || reason != tc.reason {
			t.Fatalf("%+v: admitted %v (%s), want %v (%s)", tc.sample, got, reason, tc.want, tc.reason)
		}
	}
}

// The run-cumulative distributions appear only on public projections of the
// current snapshot: the resident snapshot (also the newest history point)
// and archived interval rows carry none.
func TestStageDistributionsStayOffHistoryAndIntervalRows(t *testing.T) {
	test := &ManagedTest{ID: "projection", Status: "RUNNING", LiveMetrics: &LiveMetrics{Telemetry: &TelemetryHealth{}}}
	test.History = []*LiveMetrics{test.LiveMetrics}
	stageRecorderLocked(test).observe(telemetryStageControllerFanout, 5)
	public := cloneRunLiveMetrics(test)
	if len(public.Telemetry.StageDistributions) != len(telemetryStageOrder) {
		t.Fatalf("public projection %+v", public.Telemetry.StageDistributions)
	}
	if test.LiveMetrics.Telemetry.StageDistributions != nil || test.History[0].Telemetry.StageDistributions != nil {
		t.Fatal("the resident snapshot or a history point carries the distributions")
	}
	if archived := archivedTelemetryHealth(public.Telemetry); archived.StageDistributions != nil {
		t.Fatal("an archived interval row carries the distributions")
	}
	cloned := cloneTelemetryHealth(public.Telemetry)
	*cloned.StageDistributions[2].P50Ms = 99
	if *public.Telemetry.StageDistributions[2].P50Ms != 5 {
		t.Fatal("a clone shares its distributions")
	}
}

func TestClientRenderLimiterAdmitsOneReportPerInterval(t *testing.T) {
	var limiter clientRenderLimiter
	now := time.Now()
	if ok, _ := limiter.allow("a", now); !ok {
		t.Fatal("first report refused")
	}
	if ok, wait := limiter.allow("a", now.Add(time.Second)); ok || wait <= 0 {
		t.Fatal("a second report within the interval was admitted")
	}
	if ok, _ := limiter.allow("b", now.Add(time.Second)); !ok {
		t.Fatal("another tab was limited by the first")
	}
	if ok, _ := limiter.allow("a", now.Add(clientRenderMinInterval)); !ok {
		t.Fatal("a report after the interval was refused")
	}
}

func clientRenderTestServer(t *testing.T, enabled bool) (*Server, *ManagedTest, *storage.SQLiteStore) {
	t.Helper()
	server, store := baselineTestServer(t)
	now := time.Now().UTC()
	project := &storage.Project{ID: "render-project", Name: "Render", CreatedAt: now, UpdatedAt: now}
	if enabled {
		project.Settings.Telemetry = &storage.ProjectTelemetryPolicy{ClientRenderEvidence: true}
	}
	if err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	started := now.Add(-time.Minute)
	test := &ManagedTest{ID: "render-run", Name: "Render", ProjectID: project.ID, Status: "RUNNING", StartedAt: &started}
	server.runner.mu.Lock()
	server.runner.tests[test.ID] = test
	server.runner.mu.Unlock()
	return server, test, store
}

func postClientRender(server *Server, id, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+id+"/telemetry/client-render", strings.NewReader(body))
	request.SetPathValue("id", id)
	recorder := httptest.NewRecorder()
	server.handleReportClientRender(recorder, request)
	return recorder
}

func clientRenderBody(clientID string, samples ...clientRenderSample) string {
	data, _ := json.Marshal(clientRenderReport{ClientID: clientID, Samples: samples})
	return string(data)
}

// A live run's visible renders enter the run's own recorder (not a read
// snapshot's), and the next publication reports them as the advisory
// browser_render stage.
func TestClientRenderReportEntersTheLiveRunDistribution(t *testing.T) {
	server, test, _ := clientRenderTestServer(t, true)
	now := time.Now().UTC()
	response := postClientRender(server, test.ID, clientRenderBody("tab-1",
		clientRenderSample{ReceiveToRenderMs: 12, State: "visible", ObservedAt: now},
		clientRenderSample{ReceiveToRenderMs: 18, State: "visible", ObservedAt: now},
		clientRenderSample{ReceiveToRenderMs: 900, State: "background", ObservedAt: now}))
	if response.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", response.Code, response.Body)
	}
	var result ClientRenderReportResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.Accepted != 2 || result.Excluded["background_tab"] != 1 {
		t.Fatalf("result %+v %v", result, err)
	}
	live := &LiveMetrics{Timestamp: now, Telemetry: &TelemetryHealth{}}
	server.runner.publishLiveMetrics(test, live, nil)
	public, ok := server.runner.GetTest(test.ID)
	if !ok || public.LiveMetrics == nil {
		t.Fatal("no public run view")
	}
	installed := public.LiveMetrics
	var render, fanout *TelemetryStageDistribution
	for i := range installed.Telemetry.StageDistributions {
		switch installed.Telemetry.StageDistributions[i].Stage {
		case telemetryStageBrowserRender:
			render = &installed.Telemetry.StageDistributions[i]
		case telemetryStageControllerFanout:
			fanout = &installed.Telemetry.StageDistributions[i]
		}
	}
	if fanout == nil || fanout.Samples != 1 || fanout.Availability != "measured" {
		t.Fatalf("the publication's own fan-out was not sampled: %+v", fanout)
	}
	server.runner.mu.RLock()
	resident, history := test.LiveMetrics, test.History
	server.runner.mu.RUnlock()
	if len(history) != 1 || history[0] != resident || resident.Telemetry.StageDistributions != nil || resident.Telemetry.BrowserRenderLagMs <= 0 {
		t.Fatal("the resident snapshot must be the newest history point, without distributions but with the render p95")
	}
	if render == nil || render.Samples != 2 || !render.Advisory || render.Excluded["background_tab"] != 1 || installed.Telemetry.BrowserRenderLagMs <= 0 {
		t.Fatalf("render stage %+v, lag %v", render, installed.Telemetry.BrowserRenderLagMs)
	}
	if installed.TelemetryStatus != nil && installed.TelemetryStatus.Status == "degraded" {
		t.Fatalf("render evidence degraded run health: %+v", installed.TelemetryStatus)
	}
}

func TestClientRenderReportIsOptInLiveBoundedAndRateLimited(t *testing.T) {
	now := time.Now().UTC()
	visible := clientRenderSample{ReceiveToRenderMs: 10, State: "visible", ObservedAt: now}

	server, test, _ := clientRenderTestServer(t, false)
	if got := postClientRender(server, test.ID, clientRenderBody("tab", visible)); got.Code != http.StatusConflict {
		t.Fatalf("a project that did not opt in: %d", got.Code)
	}

	server, test, _ = clientRenderTestServer(t, true)
	if got := postClientRender(server, "no-such-run", clientRenderBody("tab", visible)); got.Code != http.StatusNotFound {
		t.Fatalf("unknown run: %d", got.Code)
	}
	tooMany := make([]clientRenderSample, clientRenderMaxSamples+1)
	for i := range tooMany {
		tooMany[i] = visible
	}
	for name, body := range map[string]string{
		"no client":      clientRenderBody("", visible),
		"long client":    clientRenderBody(strings.Repeat("x", 65), visible),
		"no samples":     clientRenderBody("tab"),
		"too many":       clientRenderBody("tab", tooMany...),
		"unknown field":  `{"client_id":"tab","samples":[],"extra":1}`,
		"trailing value": clientRenderBody("tab", visible) + `{}`,
	} {
		if got := postClientRender(server, test.ID, body); got.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d", name, got.Code)
		}
	}
	if got := postClientRender(server, test.ID, `{"client_id":"tab","samples":[`+strings.Repeat(" ", clientRenderMaxBodyBytes)+`]}`); got.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized: %d", got.Code)
	}
	if got := postClientRender(server, test.ID, clientRenderBody("rate-tab", visible)); got.Code != http.StatusAccepted {
		t.Fatalf("first report: %d %s", got.Code, got.Body)
	}
	limited := postClientRender(server, test.ID, clientRenderBody("rate-tab", visible))
	if limited.Code != http.StatusTooManyRequests || limited.Header().Get("Retry-After") == "" {
		t.Fatalf("second report within the interval: %d", limited.Code)
	}

	server.runner.mu.Lock()
	test.Status = "COMPLETED"
	server.runner.mu.Unlock()
	if got := postClientRender(server, test.ID, clientRenderBody("late-tab", visible)); got.Code != http.StatusConflict {
		t.Fatalf("a finished run accepted render evidence: %d", got.Code)
	}
}

// Only the execution leader publishes live metrics, so a follower refuses
// render evidence rather than recording it where no publication reads it.
func TestClientRenderReportRefusedOnAFollower(t *testing.T) {
	server, test, store := clientRenderTestServer(t, true)
	server.runner.leadership = newExecutionLeadership(store, "render-follower")
	got := postClientRender(server, test.ID, clientRenderBody("tab", clientRenderSample{ReceiveToRenderMs: 10, State: "visible", ObservedAt: time.Now()}))
	if got.Code != http.StatusServiceUnavailable {
		t.Fatalf("follower: %d", got.Code)
	}
}

// Finalize records the run's stage distributions and mergeable digests in
// the archive manifest.
func TestStageDistributionsPersistInTheManifest(t *testing.T) {
	runner := &TestRunner{}
	test := &ManagedTest{ID: "manifest-run"}
	runner.mu.Lock()
	recorder := stageRecorderLocked(test)
	runner.mu.Unlock()
	recorder.observe(telemetryStageControllerFanout, 4)
	recorder.observe(telemetryStageControllerFanout, 6)
	manifest := &RunArchiveManifest{}
	runner.persistTelemetryStageDistributions(test, manifest)
	if len(manifest.TelemetryStageDistributions) != len(telemetryStageOrder) || manifest.TelemetryStageDigests[telemetryStageControllerFanout].Count != 2 {
		t.Fatalf("manifest %+v %+v", manifest.TelemetryStageDistributions, manifest.TelemetryStageDigests)
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var decoded RunArchiveManifest
	if err := json.Unmarshal(data, &decoded); err != nil || decoded.TelemetryStageDigests[telemetryStageControllerFanout].Count != 2 {
		t.Fatalf("digest round trip %+v %v", decoded.TelemetryStageDigests, err)
	}
	restored := engine.NewTDigestFromSnapshot(decoded.TelemetryStageDigests[telemetryStageControllerFanout])
	if restored.Quantile(0.5) < 4 || restored.Quantile(0.5) > 6 {
		t.Fatalf("restored digest p50 %v", restored.Quantile(0.5))
	}
	// A second finalize keeps the first record.
	recorder.observe(telemetryStageControllerFanout, 100)
	runner.persistTelemetryStageDistributions(test, manifest)
	if manifest.TelemetryStageDigests[telemetryStageControllerFanout].Count != 2 {
		t.Fatal("a repeated finalize rewrote the recorded distributions")
	}
}

// An editor's telemetry opt-in changes only the telemetry policy, keeping
// every other project setting, and a request without the field is refused.
func TestProjectTelemetryPolicyUpdateChangesOnlyTheOptIn(t *testing.T) {
	server, _, store := clientRenderTestServer(t, false)
	project, err := store.GetProject("render-project")
	if err != nil {
		t.Fatal(err)
	}
	project.Settings.MaxVUsers = 42
	project.Settings.Features.MediaStream = true
	if err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	put := func(body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPut, "/api/v1/projects/render-project/telemetry-policy", strings.NewReader(body))
		request.SetPathValue("id", "render-project")
		recorder := httptest.NewRecorder()
		server.handleUpdateProjectTelemetryPolicy(recorder, request)
		return recorder
	}
	if got := put(`{}`); got.Code != http.StatusBadRequest {
		t.Fatalf("missing field: %d", got.Code)
	}
	if got := put(`{"client_render_evidence":true}`); got.Code != http.StatusOK {
		t.Fatalf("enable: %d %s", got.Code, got.Body)
	}
	saved, err := store.GetProject("render-project")
	if err != nil || saved.Settings.Telemetry == nil || !saved.Settings.Telemetry.ClientRenderEvidence || saved.Settings.MaxVUsers != 42 || !saved.Settings.Features.MediaStream {
		t.Fatalf("saved %+v %v", saved.Settings, err)
	}
	if !clientRenderEvidenceEnabled(server.runner, "render-project") {
		t.Fatal("the runner does not see the opt-in")
	}
	if got := put(`{"client_render_evidence":false}`); got.Code != http.StatusOK || clientRenderEvidenceEnabled(server.runner, "render-project") {
		t.Fatalf("disable: %d", got.Code)
	}
}

// A local run's sample age is the controller's own clock; a distributed
// run's is cross-host and enters the distribution only once qualified.
func TestSampleAgeStageNeedsAQualifiedClockWhenDistributed(t *testing.T) {
	now := time.Now()
	sampleAge := func(test *ManagedTest, quality *generatorrpc.ClockQuality) TelemetryStageDistribution {
		live := &LiveMetrics{Telemetry: &TelemetryHealth{CollectedAt: now, SampleAgeMs: 25, SampleAgeQuality: quality}}
		sampleTelemetryStagesLocked(test, live)
		if live.Telemetry.StageDistributions != nil {
			t.Fatal("sampling stored the distributions on the snapshot")
		}
		for _, stage := range test.telemetryStages.snapshot() {
			if stage.Stage == telemetryStageSampleAge {
				return stage
			}
		}
		t.Fatal("no sample-age stage")
		return TelemetryStageDistribution{}
	}
	if got := sampleAge(&ManagedTest{ID: "local"}, nil); got.Samples != 1 {
		t.Fatalf("local sample age %+v", got)
	}
	distributed := &ManagedTest{ID: "distributed", telemetrySources: &telemetrySourceLedger{}}
	if got := sampleAge(distributed, nil); got.Samples != 0 || got.Excluded["clock_unqualified"] != 1 {
		t.Fatalf("an unqualified cross-host age was sampled: %+v", got)
	}
	if got := sampleAge(distributed, &generatorrpc.ClockQuality{Qualified: true}); got.Samples != 1 {
		t.Fatalf("a qualified cross-host age was dropped: %+v", got)
	}
}

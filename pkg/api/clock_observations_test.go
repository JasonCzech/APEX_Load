package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/collectoragent"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestClockObservationLogKeepsOnePerSourcePerWindow(t *testing.T) {
	at := time.Date(2026, 10, 5, 12, 0, 10, 0, time.UTC)
	observation := func(source string, offset time.Duration) ClockObservation {
		return newClockObservation(source, "", analysis.ClockSourceGenerator, analysis.ClockEstimate{Offset: time.Second, RoundTrip: 4 * time.Millisecond, Uncertainty: 2 * time.Millisecond}, at.Add(offset))
	}
	var log clockObservationLog
	for _, tc := range []struct {
		source string
		offset time.Duration
		want   bool
	}{
		{"gen-a", 0, true},
		{"gen-a", 30 * time.Second, false}, // same minute
		{"gen-b", 30 * time.Second, true},  // another source
		{"gen-a", 50 * time.Second, true},  // next minute
		{"gen-a", -2 * time.Minute, false}, // never backwards
	} {
		if got := log.record(observation(tc.source, tc.offset)); got != tc.want {
			t.Fatalf("%s at +%s recorded=%v, want %v", tc.source, tc.offset, got, tc.want)
		}
	}
	records, truncated := log.snapshot()
	if len(records) != 3 || truncated || records[0].OffsetMs != 1000 || records[0].UncertaintyMs != 2 || records[0].Schema != clockObservationSchema {
		t.Fatalf("records %+v truncated %v", records, truncated)
	}
	for i := 0; len(records) < maxClockObservations; i++ {
		log.record(observation(fmt.Sprintf("fill-%d", i), 0))
		records, _ = log.snapshot()
	}
	if log.record(observation("one-too-many", 0)) {
		t.Fatal("recorded past the run's bound")
	}
	if _, truncated := log.snapshot(); !truncated {
		t.Fatal("bound reached without truncation")
	}
}

func TestCollectorHeartbeatStampsControllerClock(t *testing.T) {
	server, _ := newTimeslotServer(t)
	create := timeslotRequest(t, server, http.MethodPost, "/api/v1/monitoring/collectors", map[string]any{"project_id": "proj-a", "name": "clock", "allowed_metric_packs": []string{"go-runtime"}})
	var created struct {
		Collector storage.MonitoringCollector `json:"collector"`
		Token     string                      `json:"token"`
	}
	if create.Code != http.StatusCreated || json.Unmarshal(create.Body.Bytes(), &created) != nil {
		t.Fatalf("create: %d %s", create.Code, create.Body.String())
	}
	before := time.Now()
	rec := collectorRequest(t, server, http.MethodPost, "/api/v1/monitoring/collectors/"+created.Collector.ID+"/heartbeat", created.Token, collectorHeartbeatRequest{Health: "healthy"})
	after := time.Now()
	if rec.Code != http.StatusOK {
		t.Fatalf("heartbeat: %d %s", rec.Code, rec.Body.String())
	}
	received, err1 := strconv.ParseInt(rec.Header().Get(collectoragent.ClockReceivedHeader), 10, 64)
	sent, err2 := strconv.ParseInt(rec.Header().Get(collectoragent.ClockSentHeader), 10, 64)
	if err1 != nil || err2 != nil || received < before.UnixNano() || sent < received || sent > after.UnixNano() {
		t.Fatalf("clock headers received=%d sent=%d outside [%d, %d]", received, sent, before.UnixNano(), after.UnixNano())
	}
}

func TestCollectorRunPointsCarryAndArchiveTheirClock(t *testing.T) {
	t.Setenv("APEXLOAD_RUN_MONITOR_PROJECTS", "proj-a")
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	t.Setenv("APEXLOAD_MONITOR_SIGNING_SEED", base64.StdEncoding.EncodeToString(key.Seed()))
	server, store := newTimeslotServer(t)
	now := time.Now().UTC()
	for _, err := range []error{
		store.SaveScript(&dsl.Script{ID: "clock-script", Name: "script", Protocol: "http", ProjectID: "proj-a", CreatedAt: now}),
		store.SaveScenario(&storage.Scenario{ID: "clock-scenario", Name: "scenario", Duration: "1m", CreatedAt: now}, []storage.ScenarioScript{{ScriptID: "clock-script"}}),
		store.SaveTest(&storage.TestRecord{ID: "clock-run", Name: "run", ProjectID: "proj-a", ScenarioID: "clock-scenario", Status: "RUNNING", StartedAt: &now, StartTime: now}),
		store.SaveMonitoringCollector(&storage.MonitoringCollector{ID: "clock-collector", Name: "collector", ProjectID: "proj-a", TokenHash: hashAPIKey("clock-token"), Health: "healthy", LastSeenAt: &now, AllowedMetricPacks: []string{"go-runtime"}}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	// No execution backend runs this synthetic run; end it before the server's
	// owned shutdown attempts teardown.
	t.Cleanup(func() {
		endedAt := time.Now().UTC()
		if record, err := store.GetTest("clock-run"); err == nil {
			record.Status, record.EndTime = "COMPLETE", &endedAt
			_ = store.SaveTest(record)
		}
		server.runner.mu.Lock()
		if current := server.runner.tests["clock-run"]; current != nil {
			current.Status, current.EndedAt = "COMPLETE", &endedAt
		}
		server.runner.mu.Unlock()
	})
	created := timeslotRequest(t, server, http.MethodPost, "/api/v1/projects/proj-a/monitor-sources", storage.MonitorSource{Name: "runtime", CollectorID: "clock-collector", Pack: "go-runtime", IntervalSeconds: 15, Enabled: true})
	var source storage.MonitorSource
	if created.Code != 200 || json.Unmarshal(created.Body.Bytes(), &source) != nil {
		t.Fatalf("create source: %d %s", created.Code, created.Body.String())
	}
	if bound := timeslotRequest(t, server, http.MethodPut, "/api/v1/scenarios/clock-scenario/monitor-bindings", storage.MonitorBindings{Sources: []storage.MonitorBinding{{SourceID: source.ID, Required: true}}}); bound.Code != 200 {
		t.Fatalf("bind: %d %s", bound.Code, bound.Body.String())
	}
	monitors := store.(storage.RunMonitorStore)
	if err := monitors.SaveCollectorRunReadiness("proj-a", "clock-collector", []string{"go-runtime"}, nil, now); err != nil {
		t.Fatal(err)
	}
	jobs, err := monitors.StartRunMonitors("proj-a", "clock-run", "clock-scenario", now, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	polled := collectorRequest(t, server, http.MethodGet, "/api/v1/monitoring/collectors/clock-collector/jobs", "clock-token", nil)
	var envelopes []collectoragent.SignedRunJob
	if polled.Code != 200 || json.Unmarshal(polled.Body.Bytes(), &envelopes) != nil || len(envelopes) != 1 {
		t.Fatalf("poll: %d %s", polled.Code, polled.Body.String())
	}
	// The run is held by this controller, so its archive can take observations.
	server.runner.mu.Lock()
	server.runner.tests["clock-run"] = &ManagedTest{ID: "clock-run", ProjectID: "proj-a", Status: "RUNNING"}
	server.runner.mu.Unlock()

	// The collector runs two seconds ahead and measured it on its heartbeat.
	measured := analysis.MeasuredTiming(analysis.ClockSourceCollector, analysis.ClockEstimate{Offset: 2 * time.Second, RoundTrip: 30 * time.Millisecond, Uncertainty: 15 * time.Millisecond})
	point := func(at time.Time, timing *analysis.PointTiming) analysis.MetricPoint {
		v := 3.0
		return analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: "proj-a", RunID: "clock-run", ScenarioID: "clock-scenario", Source: source.ID, Host: "clock-collector", TimestampUTC: at, MetricName: "goroutines", Unit: "count", Kind: analysis.KindGauge, Quality: analysis.QualityValid, Value: &v, Timing: timing}
	}
	path := "/api/v1/monitoring/collectors/clock-collector/jobs/" + jobs[0].ID + "/samples"
	received := now
	for name, timing := range map[string]*analysis.PointTiming{
		"a collector claiming a provider clock": analysis.ProviderTiming(60, nil),
		"a collector claiming the receive time": {ClockSource: analysis.ClockSourceCollector, OffsetStatus: analysis.OffsetUnknown, ReceivedAt: &received},
	} {
		refused := collectoragent.RunSampleBatch{Job: envelopes[0], Points: []analysis.MetricPoint{point(now, timing)}}
		if reply := collectorRequest(t, server, http.MethodPost, path, "clock-token", refused); reply.Code != 400 {
			t.Fatalf("%s: %d %s", name, reply.Code, reply.Body.String())
		}
	}
	// Even with a measured offset, a point well ahead of the controller keeps
	// its clock_skewed flag; offsets are evidence, never silent corrections.
	batch := collectoragent.RunSampleBatch{Job: envelopes[0], Points: []analysis.MetricPoint{point(now, measured), point(now.Add(40*time.Second), measured)}}
	for i := 0; i < 2; i++ { // a retry of the same batch keeps its identity
		if reply := collectorRequest(t, server, http.MethodPost, path, "clock-token", batch); reply.Code != 200 {
			t.Fatalf("batch: %d %s", reply.Code, reply.Body.String())
		}
	}
	series := timeslotRequest(t, server, http.MethodGet, "/api/v1/tests/clock-run/monitor-series?source_id="+source.ID+"&metric=goroutines", nil)
	var body struct {
		Points []analysis.MetricPoint `json:"points"`
	}
	if series.Code != 200 || json.Unmarshal(series.Body.Bytes(), &body) != nil || len(body.Points) != 2 {
		t.Fatalf("series: %d %s", series.Code, series.Body.String())
	}
	if timing := body.Points[0].Timing; timing == nil || timing.OffsetStatus != analysis.OffsetMeasured || *timing.ClockOffsetMs != 2000 || *timing.ClockUncertaintyMs != 15 {
		t.Fatalf("point timing %+v", timing)
	}
	if body.Points[1].Quality != analysis.QualityClockSkewed {
		t.Fatalf("40 s ahead quality = %q, want clock_skewed", body.Points[1].Quality)
	}

	server.runner.mu.RLock()
	observations, _ := server.runner.tests["clock-run"].clockObservations.snapshot()
	server.runner.mu.RUnlock()
	if len(observations) != 1 || observations[0].Source != "clock-collector" || observations[0].ClockSource != analysis.ClockSourceCollector || observations[0].OffsetMs != 2000 || observations[0].RoundTripMs != 30 {
		t.Fatalf("observations %+v", observations)
	}
	archive := t.TempDir()
	record := &storage.RunArchiveRecord{TestID: "clock-run", ArchivePath: archive, ManifestPath: archive + "/manifest.json"}
	admitArchiveForTest(t, record)
	manifest := &RunArchiveManifest{}
	server.runner.mu.RLock()
	test := server.runner.tests["clock-run"]
	server.runner.mu.RUnlock()
	if err := server.runner.persistClockObservationArchive(context.Background(), test, record, manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.ClockObservationsFile != clockObservationsFileName || manifest.ClockObservationCount != 1 || len(manifest.Artifacts) != 1 || manifest.Artifacts[0].RecordType != clockObservationRecordType {
		t.Fatalf("manifest %+v", manifest)
	}
}

func TestProviderPointsCarryProviderTiming(t *testing.T) {
	start := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	bucket := analysis.MetricPoint{TimestampUTC: start, Provider: &analysis.ProviderMeasurement{Name: "dynatrace", ResolutionSeconds: 60, BucketStart: start, BucketEnd: start.Add(time.Minute)}}
	timing := providerPointTiming(bucket)
	if timing.ClockSource != analysis.ClockSourceProvider || timing.OffsetStatus != analysis.OffsetUnknown || timing.ResolutionSeconds != 60 || *timing.ClockUncertaintyMs != 30000 || timing.ReceivedAt != nil || timing.Validate() != nil {
		t.Fatalf("bucket timing %+v", timing)
	}
	sampled := start.Add(-20 * time.Second)
	evaluation := analysis.MetricPoint{TimestampUTC: start, Provider: &analysis.ProviderMeasurement{Name: "prometheus", EvaluationStepSeconds: 15, UnderlyingSampleTime: &sampled, FreshnessKnown: true}}
	if timing := providerPointTiming(evaluation); timing.ResolutionSeconds != 15 || *timing.ClockUncertaintyMs != 7500 || *timing.FreshnessSeconds != 20 {
		t.Fatalf("evaluation timing %+v", timing)
	}
	window := analysis.MetricPoint{TimestampUTC: start.Add(10 * time.Minute), Provider: &analysis.ProviderMeasurement{Name: "prometheus", EvaluationStepSeconds: 15, QueryWindow: true, BucketStart: start, BucketEnd: start.Add(10 * time.Minute)}}
	if timing := providerPointTiming(window); timing.ResolutionSeconds != 600 || *timing.ClockUncertaintyMs != 300000 {
		t.Fatalf("query window timing %+v", timing)
	}
	// Re-reading a bucket must persist identical content for stable finals.
	a, _ := json.Marshal(providerPointTiming(bucket))
	b, _ := json.Marshal(providerPointTiming(bucket))
	if string(a) != string(b) {
		t.Fatal("provider timing is not a function of the point")
	}
}

package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/collectoragent"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestRunMonitoringAPIArtifactsAndRetry(t *testing.T) {
	t.Setenv("APEXLOAD_RUN_MONITOR_PROJECTS", "proj-a")
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	t.Setenv("APEXLOAD_MONITOR_SIGNING_SEED", base64.StdEncoding.EncodeToString(key.Seed()))
	server, store := newTimeslotServer(t)
	now := time.Now().UTC()
	for _, err := range []error{
		store.SaveScript(&dsl.Script{ID: "monitor-script", Name: "script", Protocol: "http", ProjectID: "proj-a", CreatedAt: now}),
		store.SaveScenario(&storage.Scenario{ID: "monitor-scenario", Name: "scenario", Duration: "1m", CreatedAt: now}, []storage.ScenarioScript{{ScriptID: "monitor-script"}}),
		store.SaveTest(&storage.TestRecord{ID: "monitor-run", Name: "run", ProjectID: "proj-a", ScenarioID: "monitor-scenario", Status: "RUNNING", StartedAt: &now, StartTime: now}),
		store.SaveMonitoringCollector(&storage.MonitoringCollector{ID: "monitor-collector", Name: "collector", ProjectID: "proj-a", TokenHash: hashAPIKey("monitor-token"), Health: "healthy", LastSeenAt: &now, AllowedMetricPacks: []string{"go-runtime"}}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	// This fixture tests persisted monitor evidence, with no execution backend.
	// End its synthetic run before the server's owned shutdown attempts teardown.
	t.Cleanup(func() {
		endedAt := time.Now().UTC()
		record, err := store.GetTest("monitor-run")
		if err != nil {
			t.Errorf("load synthetic run for cleanup: %v", err)
			return
		}
		record.Status, record.EndTime = "COMPLETE", &endedAt
		if err := store.SaveTest(record); err != nil {
			t.Errorf("complete synthetic run: %v", err)
			return
		}
		server.runner.mu.Lock()
		if current := server.runner.tests["monitor-run"]; current != nil {
			current.Status, current.EndedAt = "COMPLETE", &endedAt
		}
		server.runner.mu.Unlock()
	})
	created := timeslotRequest(t, server, http.MethodPost, "/api/v1/projects/proj-a/monitor-sources", storage.MonitorSource{Name: "runtime", Service: "orders", CollectorID: "monitor-collector", Pack: "go-runtime", IntervalSeconds: 15, Enabled: true})
	if created.Code != 200 {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	var source storage.MonitorSource
	json.Unmarshal(created.Body.Bytes(), &source)
	bound := timeslotRequest(t, server, http.MethodPut, "/api/v1/scenarios/monitor-scenario/monitor-bindings", storage.MonitorBindings{Sources: []storage.MonitorBinding{{SourceID: source.ID, Required: true}}})
	if bound.Code != 200 {
		t.Fatalf("bind: %d %s", bound.Code, bound.Body.String())
	}
	if err := store.(storage.RunMonitorStore).SaveCollectorRunReadiness("proj-a", "monitor-collector", []string{"go-runtime"}, nil, now); err != nil {
		t.Fatal(err)
	}
	jobs, err := store.(storage.RunMonitorStore).StartRunMonitors("proj-a", "monitor-run", "monitor-scenario", now, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	polled := collectorRequest(t, server, http.MethodGet, "/api/v1/monitoring/collectors/monitor-collector/jobs", "monitor-token", nil)
	if polled.Code != 200 {
		t.Fatalf("poll: %d %s", polled.Code, polled.Body.String())
	}
	var envelopes []collectoragent.SignedRunJob
	json.Unmarshal(polled.Body.Bytes(), &envelopes)
	if len(envelopes) != 1 {
		t.Fatalf("jobs: %s", polled.Body.String())
	}
	v := 3.0
	p := analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: "proj-a", RunID: "monitor-run", ScenarioID: "monitor-scenario", Source: source.ID, Host: "monitor-collector", TimestampUTC: now, MetricName: "goroutines", Unit: "count", Kind: analysis.KindGauge, Quality: analysis.QualityValid, Value: &v}
	batch := collectoragent.RunSampleBatch{Job: envelopes[0], Points: []analysis.MetricPoint{p}}
	path := "/api/v1/monitoring/collectors/monitor-collector/jobs/" + jobs[0].ID + "/samples"
	// Governed providers use their typed ingestion branch. A generic signed host
	// grant cannot manufacture provider-origin/settled bucket provenance.
	for _, provider := range []*analysis.ProviderMeasurement{
		{Name: "appdynamics", MetricID: "forged", Mode: "interval_bucket", BucketStart: now, BucketEnd: now.Add(time.Second), ResolutionSeconds: 1, Statistic: "avg", Settled: true, AggregationEligible: true},
		{Name: "prometheus", MetricID: "forged", QueryID: "query", DefinitionHash: strings.Repeat("a", 64), BackendProfile: "prometheus", Mode: "evaluation_point", Statistic: "gauge", EvaluationStepSeconds: 15, Settled: true},
	} {
		forged := p
		forged.Provider = provider
		if err := forged.Validate(); err != nil {
			t.Fatalf("otherwise valid provider point: %v", err)
		}
		refused := collectoragent.RunSampleBatch{Job: envelopes[0], Points: []analysis.MetricPoint{forged}}
		if reply := collectorRequest(t, server, http.MethodPost, path, "monitor-token", refused); reply.Code != 400 {
			t.Fatalf("generic collector forged %s origin: %d %s", provider.Name, reply.Code, reply.Body.String())
		}
		current, err := store.(storage.RunMonitorStore).GetRunMonitorJob("proj-a", jobs[0].ID)
		receipts, receiptErr := store.(storage.RunMonitorStore).ListMonitorBatchReceipts("proj-a", "monitor-run", "", -1, 10)
		if err != nil || receiptErr != nil || current.NextSequence != 0 || len(receipts) != 0 {
			t.Fatal("refused provider origin advanced durable evidence", current, receipts, err, receiptErr)
		}
	}
	for i := 0; i < 2; i++ {
		reply := collectorRequest(t, server, http.MethodPost, path, "monitor-token", batch)
		if reply.Code != 200 {
			t.Fatalf("batch: %d %s", reply.Code, reply.Body.String())
		}
	}
	receipts, err := store.(storage.RunMonitorStore).ListMonitorBatchReceipts("proj-a", "monitor-run", "", -1, 10)
	if err != nil || len(receipts) != 1 {
		t.Fatalf("receipts: %+v %v", receipts, err)
	}
	if _, err := readMonitorArtifact(context.Background(), receipts[0].ArtifactKey, receipts[0].Digest); err != nil {
		t.Fatal(err)
	}
	series := timeslotRequest(t, server, http.MethodGet, "/api/v1/tests/monitor-run/monitor-series?source_id="+source.ID+"&metric=goroutines", nil)
	if series.Code != 200 {
		t.Fatalf("series: %d %s", series.Code, series.Body.String())
	}
	// Native panel overlays reuse durable monitor receipts and shared correlation.
	q := nativeEvidenceQuery("archive")
	q.OverlaySelection = &storage.NativeOverlaySelection{SourceID: source.ID, Metric: "goroutines", Aggregation: "avg"}
	out := nativeEvidenceEnvelope(now, q)
	value := 12.0
	out.Series = []NativeEvidenceSeries{{Name: "Checkout", Unit: "ms", Points: []NativeEvidencePoint{{From: now, To: now.Add(10 * time.Second), Value: &value, Count: 1}, {From: now.Add(10 * time.Second), To: now.Add(20 * time.Second)}}}}
	if err := server.attachNativeOverlay(context.Background(), "proj-a", "monitor-run", q, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Overlay) != 2 || len(out.Correlation["Checkout"]) != 1 || out.Correlation["Checkout"][0].Pairs != 1 {
		t.Fatalf("overlay coverage: %+v", out)
	}
	q.EvidenceMode = "live"
	q.LiveBucketMode = "merged"
	out.Correlation = nil
	if err := server.attachNativeOverlay(context.Background(), "proj-a", "monitor-run", q, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Correlation["Checkout"]) != 1 || out.Correlation["Checkout"][0].Pairs != 1 {
		t.Fatalf("merged live correlation: %+v", out)
	}
	if err := server.attachNativeOverlay(context.Background(), "proj-b", "monitor-run", q, &out); err == nil {
		t.Fatal("cross-project overlay accepted")
	}
	for _, test := range []struct {
		query         string
		status, count int
	}{
		{"&from=invalid", 400, 0},
		{"&from=" + url.QueryEscape(now.Format(time.RFC3339Nano)) + "&to=" + url.QueryEscape(now.Add(-time.Second).Format(time.RFC3339Nano)), 400, 0},
		{"&from=" + url.QueryEscape(now.Add(time.Second).Format(time.RFC3339Nano)), 200, 0},
		{"&from=" + url.QueryEscape(now.Add(-time.Second).Format(time.RFC3339Nano)) + "&to=" + url.QueryEscape(now.Format(time.RFC3339Nano)), 200, 1},
	} {
		response := timeslotRequest(t, server, http.MethodGet, "/api/v1/tests/monitor-run/monitor-series?source_id="+source.ID+"&metric=goroutines"+test.query, nil)
		if response.Code != test.status {
			t.Fatalf("window: %d %s", response.Code, response.Body.String())
		}
		if test.status == 200 {
			var body struct {
				Points []analysis.MetricPoint `json:"points"`
			}
			if json.Unmarshal(response.Body.Bytes(), &body) != nil || len(body.Points) != test.count {
				t.Fatalf("window selection: %s", response.Body.String())
			}
		}
	}
	t.Setenv("APEXLOAD_DIAGNOSTICS_PROJECTS", "proj-a")
	for _, test := range []struct {
		offset   time.Duration
		coverage string
	}{{0, "observed"}, {2 * time.Second, "missing"}} {
		at := now.Add(test.offset)
		live := &LiveMetrics{Timestamp: at, SampleWindowSeconds: 1, DependencyMap: buildLiveDependencyMap(map[string]*TxMetrics{"orders": {Count: 1, DependencyType: "grpc"}})}
		server.runner.mu.Lock()
		server.runner.tests["monitor-run"] = &ManagedTest{ID: "monitor-run", ProjectID: "proj-a", Status: "RUNNING", LiveMetrics: live, History: []*LiveMetrics{live}}
		server.runner.mu.Unlock()
		reply := timeslotRequest(t, server, http.MethodGet, "/api/v1/tests/monitor-run/dependency-evidence?at="+url.QueryEscape(at.Format(time.RFC3339Nano)), nil)
		var evidence DependencyEvidenceResponse
		if reply.Code != 200 || json.Unmarshal(reply.Body.Bytes(), &evidence) != nil || len(evidence.Monitors) != 1 || evidence.Monitors[0].Coverage != test.coverage {
			t.Fatalf("dependency window: %d %s", reply.Code, reply.Body.String())
		}
	}
	bad := batch
	bad.Job.Payload += " "
	if reply := collectorRequest(t, server, http.MethodPost, path, "monitor-token", bad); reply.Code != 403 {
		t.Fatalf("tampering: %d", reply.Code)
	}
	if reply := collectorRequest(t, server, http.MethodGet, "/api/v1/monitoring/collectors/monitor-collector/jobs", "wrong", nil); reply.Code != 401 {
		t.Fatalf("authentication: %d", reply.Code)
	}
	if err := writeMonitorArtifact(context.Background(), "../../outside", nil); err == nil {
		t.Fatal("path traversal accepted")
	}
	archiveRoot, err := os.MkdirTemp(resolveArchiveRoot(), "monitor-archive-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(archiveRoot)
	record := &storage.RunArchiveRecord{TestID: "monitor-run", ArchivePath: archiveRoot, ManifestPath: filepath.Join(archiveRoot, "manifest.json")}
	manifest := &RunArchiveManifest{TestID: "monitor-run", SchemaVersion: archiveSchemaVersion}
	budget, err := newArchiveAdjunctBudgetForRecord(context.Background(), record)
	if err != nil {
		t.Fatal(err)
	}
	defer budget.close()
	if err := server.runner.persistRunMonitorArchive("proj-a", "monitor-run", record, manifest, budget); err != nil {
		t.Fatal(err)
	}
	if manifest.MonitorEvidenceCount != 1 || len(manifest.MonitorCoverage) != 1 || manifest.MonitorCoverage[0].State != "stopped" {
		t.Fatalf("archive coverage: %+v", manifest)
	}
	archived, err := os.ReadFile(filepath.Join(archiveRoot, manifest.MonitorEvidenceFile))
	if err != nil {
		t.Fatal(err)
	}
	var point analysis.MetricPoint
	if err := json.Unmarshal(archived, &point); err != nil || point.ReceivedAt.IsZero() || point.RunID != "monitor-run" {
		t.Fatalf("archive point: %+v %v", point, err)
	}
	if err := validateImmutableArchiveArtifactIntegrity(archiveRoot, manifest); err != nil {
		t.Fatal(err)
	}
}

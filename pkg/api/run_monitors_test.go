package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
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
	created := timeslotRequest(t, server, http.MethodPost, "/api/v1/projects/proj-a/monitor-sources", storage.MonitorSource{Name: "runtime", CollectorID: "monitor-collector", Pack: "go-runtime", IntervalSeconds: 1, Enabled: true})
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
	p := analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: "proj-a", RunID: "monitor-run", ScenarioID: "monitor-scenario", Source: source.ID, Host: "monitor-collector", TimestampUTC: now, MetricName: "goroutines", Kind: analysis.KindGauge, Quality: analysis.QualityValid, Value: &v}
	batch := collectoragent.RunSampleBatch{Job: envelopes[0], Points: []analysis.MetricPoint{p}}
	path := "/api/v1/monitoring/collectors/monitor-collector/jobs/" + jobs[0].ID + "/samples"
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

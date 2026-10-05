package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/appdynamics"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestRunMonitorPublicationMergeArchivesProviderAndManagementEvidence(t *testing.T) {
	server, durable, management, _, key := managementAPIFixture(t)
	store := durable.(storage.RunMonitorStore)
	start := time.Now().UTC().Add(-15 * time.Minute).Truncate(time.Minute)
	end := start.Add(2 * time.Minute)
	// Establish readiness at the historical execution boundary, then refresh
	// the heartbeat before the current signed poll. A present-day heartbeat
	// cannot prove readiness for a run that began fifteen minutes earlier.
	refreshReadiness := func(at time.Time) {
		t.Helper()
		collector, err := durable.GetMonitoringCollector(management.CollectorID)
		if err != nil {
			t.Fatal(err)
		}
		collector.LastSeenAt = &at
		if err = durable.SaveMonitoringCollector(collector); err != nil {
			t.Fatal(err)
		}
		scope, err := management.ManagementScope.Hash()
		if err != nil {
			t.Fatal(err)
		}
		identity, err := management.ManagementIdentity.Fingerprint()
		if err != nil {
			t.Fatal(err)
		}
		readiness := storage.CollectorManagementReadiness{InstanceID: "instance-1", Capabilities: []string{storage.ManagementEvidenceCapability}, Scopes: []storage.ManagementReadinessScope{{ScopeHash: scope, IdentityHash: identity}}, Packs: []string{management.Pack}, Destinations: []string{management.ReadinessDestination()}, SeenAt: at}
		if err = durable.(storage.ManagementMonitorStore).SaveCollectorManagementReadiness("proj-a", management.CollectorID, readiness); err != nil {
			t.Fatal(err)
		}
	}
	refreshReadiness(start)
	connection := &storage.AppDynamicsConnection{ID: "combined-connection", ProjectID: "proj-a", Name: "APM", Enabled: true, CollectionMode: "direct", Config: appdynamics.Config{Endpoint: "https://apm.example.com", AccountName: "account"}, ClientIDSecretName: "client", ClientSecretSecretName: "secret"}
	connections := durable.(storage.AppDynamicsConnectionStore)
	if err := connections.CreateAppDynamicsConnection(connection); err != nil {
		t.Fatal(err)
	}
	if _, err := connections.UpdateAppDynamicsConnectionHealth(connection.ID, "proj-a", 1, "connected", "", appdynamics.Capabilities{Metrics: true}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	provider := storage.MonitorSource{ID: "combined-provider", ProjectID: "proj-a", Name: "APM source", Enabled: true, Pack: "appdynamics", ExecutorMode: "direct", ConnectionID: connection.ID, IntervalSeconds: 60, AppDynamics: &appdynamics.Selection{ProjectID: "proj-a", Applications: []string{"1"}, Events: true, Metrics: []appdynamics.MetricSelection{{ApplicationID: "1", Path: "Overall Application Performance|Calls per Minute", Name: "calls", Unit: "count", Statistic: "sum"}}}}
	if _, err := store.SaveMonitorSource(provider, 0); err != nil {
		t.Fatal(err)
	}
	record := &storage.TestRecord{ID: "combined-run", Name: "combined", ProjectID: "proj-a", Status: "RUNNING", StartTime: start, StartedAt: &start, MonitorBindings: &storage.MonitorBindings{Sources: []storage.MonitorBinding{{SourceID: management.ID, Required: true}, {SourceID: provider.ID}}}}
	if err := durable.SaveTest(record); err != nil {
		t.Fatal(err)
	}
	jobs, err := store.StartRunMonitors("proj-a", record.ID, "", start, time.Now().UTC().Add(time.Hour))
	if err != nil || len(jobs) != 2 {
		t.Fatal("mixed monitor jobs", jobs, err)
	}
	var managementJob, providerJob storage.RunMonitorJob
	for _, job := range jobs {
		if job.State != "collecting" {
			t.Fatalf("mixed source did not cross readiness: %+v", job)
		}
		if job.Source.ManagementScope != nil {
			managementJob = job
		} else {
			providerJob = job
		}
	}
	refreshReadiness(time.Now().UTC())
	batch, grant := admittedManagementBatch(t, server, managementJob, key, true)
	reply := collectorRequest(t, server, http.MethodPost, "/api/v1/monitoring/collectors/management-collector/jobs/"+managementJob.ID+"/samples", "management-token", batch)
	if reply.Code != 200 {
		t.Fatalf("mixed management publication: %d %s", reply.Code, reply.Body.String())
	}
	value := 3.0
	point := analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: "proj-a", RunID: record.ID, Source: provider.ID, MetricName: "calls", Unit: "count", Kind: analysis.KindGauge, Quality: analysis.QualityValid, TimestampUTC: start, Value: &value, Provider: &analysis.ProviderMeasurement{Name: "appdynamics", MetricID: "1", CatalogPath: "Overall Application Performance|Calls per Minute", EntityIDs: map[string]string{"application": "1"}, BucketStart: start, BucketEnd: start.Add(time.Minute), ResolutionSeconds: 60, Statistic: "sum", Settled: true, AggregationEligible: true}}
	payload := storage.ProviderMonitorBatch{QueryFromUTC: start, QueryUntilUTC: end, Points: []analysis.MetricPoint{point}, Evidence: []appdynamics.Diagnostic{{ID: "42", Kind: "event", ApplicationID: "1", TimestampUTC: start, Summary: "provider evidence"}}}
	if err = server.ingestAppDynamicsBatch(context.Background(), providerJob, 0, payload, executionLeaseTerm{}); err != nil {
		t.Fatal(err)
	}
	ended := time.Now().UTC()
	record.Status, record.EndTime = "COMPLETE", &ended
	terminal := durable.(interface {
		SaveTestWithRunMonitors(*storage.TestRecord, string, string, bool) (bool, error)
	})
	if saved, err := terminal.SaveTestWithRunMonitors(record, "", "", true); err != nil || !saved {
		t.Fatal("mixed terminal boundary", saved, err)
	}
	// Archive finalization must wait for the provider drain. Advance its
	// durable lifecycle clock through the real timeout path, retaining
	// accepted evidence and explicit incomplete provider coverage.
	if err := server.runner.ensureRunMonitorFinalization("proj-a", record.ID); !errors.Is(err, errMonitoringPending) {
		t.Fatalf("mixed archive bypassed provider reconciliation: %v", err)
	}
	if err := durable.(providerMonitorStore).CloseExpiredProviderMonitors("proj-a", record.ID, ended.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(resolveArchiveRoot(), "combined")
	if err = os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	archive := &storage.RunArchiveRecord{TestID: record.ID, ArchivePath: root, ManifestPath: filepath.Join(root, "manifest.json")}
	manifest := &RunArchiveManifest{TestID: record.ID, CreatedAt: start, CompletedAt: ended, Provenance: RunArchiveProvenance{StartedAt: &start}}
	budget, err := newArchiveAdjunctBudgetForRecord(context.Background(), archive)
	if err != nil {
		t.Fatal(err)
	}
	defer budget.close()
	for attempt := 0; attempt < 2; attempt++ {
		if err = server.runner.persistRunMonitorArchive("proj-a", record.ID, archive, manifest, budget); err != nil {
			t.Fatal(err)
		}
		if manifest.MonitoringStatus != "incomplete" || manifest.MonitorEvidenceCount != 1 || manifest.MonitorDiagnosticsCount != 1 || manifest.MonitorManagementSnapshotCount != 1 || manifest.MonitorManagementRowCount != 1 || len(manifest.MonitorCoverage) != 2 {
			t.Fatalf("mixed archive lost a family on attempt%d: %+v", attempt, manifest)
		}
		var managementCoverage bool
		for _, coverage := range manifest.MonitorCoverage {
			if coverage.SourceID == management.ID {
				managementCoverage = coverage.ManagementSnapshots == 1
			}
		}
		if !managementCoverage {
			t.Fatal("management coverage disappeared alongside provider coverage")
		}
		body, err := os.ReadFile(filepath.Join(root, manifest.MonitorManagementFile))
		if err != nil {
			t.Fatal(err)
		}
		var row MonitorManagementRecord
		if err = json.Unmarshal(body, &row); err != nil || row.Evidence.MessagingManagement == nil || row.Evidence.MessagingManagement.PollID != grant.PollID {
			t.Fatal("mixed immutable management archive", row, err)
		}
		body, err = os.ReadFile(filepath.Join(root, manifest.MonitorDiagnosticsFile))
		if err != nil {
			t.Fatal(err)
		}
		var diagnostic RunMonitorDiagnostic
		if err = json.Unmarshal(body, &diagnostic); err != nil || diagnostic.SourceID != provider.ID || diagnostic.Diagnostic.Summary != "provider evidence" {
			t.Fatal("mixed canonical provider archive", diagnostic, err)
		}
	}
	// All monitor families share the reserved archive allowance. Refusing an
	// adjunct record preserves the performance archive and states the loss.
	limited, err := newArchiveAdjunctBudgetForRecord(context.Background(), archive)
	if err != nil {
		t.Fatal(err)
	}
	defer limited.close()
	limited.remaining = 1 << 20
	limitedManifest := &RunArchiveManifest{TestID: record.ID, CreatedAt: start, CompletedAt: ended, Provenance: RunArchiveProvenance{StartedAt: &start}}
	if err = server.runner.persistRunMonitorArchive("proj-a", record.ID, archive, limitedManifest, limited); err != nil {
		t.Fatal("limited mixed archive aborted performance evidence", err)
	}
	if limited.failure() != nil || limitedManifest.MonitorEvidenceCount != 0 || limitedManifest.MonitorDiagnosticsCount != 0 || limitedManifest.MonitorManagementSnapshotCount != 0 || limitedManifest.MonitorManagementRowCount != 0 || limitedManifest.MonitoringStatus != "incomplete" {
		t.Fatalf("mixed archive allowance lost its explicit refusal: %+v failure=%v", limitedManifest, limited.failure())
	}
	for _, coverage := range limitedManifest.MonitorCoverage {
		if coverage.State != "incomplete" || coverage.Reason != "archive_evidence_budget" || !coverage.ProviderCoverage["archive"].Truncated {
			t.Fatalf("mixed source falsely complete after export refusal: %+v", coverage)
		}
	}
	for _, name := range []string{limitedManifest.MonitorEvidenceFile, limitedManifest.MonitorDiagnosticsFile, limitedManifest.MonitorManagementFile} {
		body, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || len(body) != 0 {
			t.Fatalf("limited artifact %s not actually empty: bytes=%d err=%v", name, len(body), err)
		}
	}
}

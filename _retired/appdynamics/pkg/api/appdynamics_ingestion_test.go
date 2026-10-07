package api

import (
	"context"
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/appdynamics"
	"github.com/jasonczech/apexload/pkg/storage"
	"net/http"
	"strings"
	"testing"
	"time"
)

func apiAppDynamicsMonitorFixture(t *testing.T) (*Server, providerMonitorStore, storage.RunMonitorJob, analysis.MetricPoint) {
	t.Helper()
	server, db := newTimeslotServer(t)
	if server.appDynamicsCancel != nil {
		server.appDynamicsCancel()
	}
	t.Setenv("APEXLOAD_RUN_MONITOR_PROJECTS", "proj-a")
	t.Setenv("APEXLOAD_MONITOR_SIGNING_SEED", "")
	connections := db.(storage.AppDynamicsConnectionStore)
	c := &storage.AppDynamicsConnection{ID: "appd-api", ProjectID: "proj-a", Name: "AppD", Enabled: true, CollectionMode: "direct", Config: appdynamics.Config{Endpoint: "https://controller.example", AuthType: "basic", AccountName: "a"}, UsernameSecretName: "protected-user-reference", PasswordSecretName: "protected-password-reference"}
	if err := connections.CreateAppDynamicsConnection(c); err != nil {
		t.Fatal(err)
	}
	if _, err := connections.UpdateAppDynamicsConnectionHealth(c.ID, c.ProjectID, c.Revision, "connected", "", appdynamics.Capabilities{Metrics: true}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	monitors := db.(providerMonitorStore)
	source := storage.MonitorSource{ID: "appd-source", ProjectID: c.ProjectID, Name: "AppD", Enabled: true, Pack: "appdynamics", ExecutorMode: "direct", ConnectionID: c.ID, IntervalSeconds: 60, AppDynamics: &appdynamics.Selection{ProjectID: c.ProjectID, Applications: []string{"1"}, Violations: true, Metrics: []appdynamics.MetricSelection{{ApplicationID: "1", Name: "calls", Path: "calls", Unit: "count", Statistic: "sum"}}}}
	if _, err := monitors.SaveMonitorSource(source, 0); err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Minute)
	record := &storage.TestRecord{ID: "appd-run", ProjectID: c.ProjectID, Name: "AppD run", Status: "RUNNING", StartTime: start, StartedAt: &start, MonitorBindings: &storage.MonitorBindings{Sources: []storage.MonitorBinding{{SourceID: source.ID}}}}
	if err := db.SaveTest(record); err != nil {
		t.Fatal(err)
	}
	jobs, err := monitors.StartRunMonitors(c.ProjectID, record.ID, "", start, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	value := 0.0
	point := analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: c.ProjectID, RunID: record.ID, Source: source.ID, TimestampUTC: start, MetricName: "calls", Kind: analysis.KindGauge, Value: &value, Unit: "count", Quality: analysis.QualityValid, Provider: &analysis.ProviderMeasurement{Name: "appdynamics", MetricID: "1", CatalogPath: "calls", EntityIDs: map[string]string{"application": "1"}, BucketStart: start, BucketEnd: start.Add(time.Minute), ResolutionSeconds: 60, Statistic: "sum", Settled: true, AggregationEligible: true}}
	return server, monitors, jobs[0], point
}
func TestAppDynamicsSharedIngestionCanonicalAndDisclosure(t *testing.T) {
	server, store, job, p := apiAppDynamicsMonitorFixture(t)
	ctx := context.Background()
	payload := storage.ProviderMonitorBatch{QueryFromUTC: job.StartedAt, QueryUntilUTC: job.StartedAt.Add(5 * time.Minute), Points: []analysis.MetricPoint{p}, Evidence: []appdynamics.Diagnostic{{ID: "101", Kind: "violation", ApplicationID: "1", TimestampUTC: p.TimestampUTC, Summary: "password=secret", Properties: map[string]string{"sql": "SELECT credentials FROM secrets", "request_body": "confidential", "correlation": "entity_time"}, DeepLink: "https://evil.example/steal"}}}
	for range 2 {
		if err := server.ingestAppDynamicsBatch(ctx, job, 0, payload, executionLeaseTerm{}); err != nil {
			t.Fatal(err)
		}
	}
	value := 9.0
	payload.Points[0].Value = &value
	payload.Evidence[0].Status = "resolved"
	if err := server.ingestAppDynamicsBatch(ctx, job, 1, payload, executionLeaseTerm{}); err != nil {
		t.Fatal(err)
	}
	page, err := loadCanonicalMonitorData(ctx, store, job.ProjectID, job.RunID, job.Source.ID, "point", "", 100)
	if err != nil || len(page.Points) != 1 || *page.Points[0].Value != 9 || page.Points[0].Quality != analysis.QualityValid {
		t.Fatalf("canonical %+v %v", page, err)
	}
	evidence, err := loadCanonicalMonitorData(ctx, store, job.ProjectID, job.RunID, "", "diagnostic", "", 100)
	if err != nil || len(evidence.Evidence) != 1 || evidence.Evidence[0].Status != "resolved" || evidence.Evidence[0].DeepLink != "" || evidence.Evidence[0].Properties["sql"] != "" || strings.Contains(evidence.Evidence[0].Summary, "secret") {
		t.Fatalf("safe evidence %+v %v", evidence, err)
	}
	public := timeslotRequest(t, server, http.MethodGet, "/api/v1/tests/"+job.RunID+"/monitors", nil)
	if public.Code != 200 || strings.Contains(public.Body.String(), "protected-") || strings.Contains(public.Body.String(), "connection_snapshot") {
		t.Fatalf("safe jobs %d %s", public.Code, public.Body.String())
	}
	receipts, _ := store.ListMonitorBatchReceipts(job.ProjectID, job.RunID, "", -1, 10)
	if len(receipts) != 2 {
		t.Fatal("receipt retry duplicated")
	}
	body, err := readMonitorArtifact(ctx, receipts[1].ArtifactKey, receipts[1].Digest)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(body) || strings.Contains(string(body), "SELECT credentials") {
		t.Fatal("unsafe artifact")
	}
}
func TestAppDynamicsIngestionScopeAndAttribution(t *testing.T) {
	server, _, job, p := apiAppDynamicsMonitorFixture(t)
	for _, change := range []func(*analysis.MetricPoint){func(p *analysis.MetricPoint) { p.ProjectID = "other" }, func(p *analysis.MetricPoint) { p.Host = "unexpected-collector" }, func(p *analysis.MetricPoint) { p.Provider.EntityIDs = map[string]string{"application": "2"} }, func(p *analysis.MetricPoint) { p.ReceivedAt = time.Now().UTC() }} {
		bad := p
		meta := *p.Provider
		bad.Provider = &meta
		change(&bad)
		if err := server.ingestAppDynamicsBatch(context.Background(), job, 0, storage.ProviderMonitorBatch{QueryFromUTC: job.StartedAt, QueryUntilUTC: job.StartedAt.Add(5 * time.Minute), Points: []analysis.MetricPoint{bad}}, executionLeaseTerm{}); err == nil {
			t.Fatal("invalid point accepted")
		}
	}
}

func TestAppDynamicsEvidenceAttributionAndPagination(t *testing.T) {
	server, _, job, p := apiAppDynamicsMonitorFixture(t)
	const requestGUID = "0123456789abcdef0123456789abcdef"
	payload := storage.ProviderMonitorBatch{QueryFromUTC: job.StartedAt, QueryUntilUTC: job.StartedAt.Add(5 * time.Minute), Evidence: []appdynamics.Diagnostic{
		{ID: "101", Kind: "violation", ApplicationID: "1", TimestampUTC: p.TimestampUTC, Status: "ongoing", Properties: map[string]string{"requestguid": requestGUID, "run_id": job.RunID}},
		{ID: "102", Kind: "violation", ApplicationID: "1", TimestampUTC: p.TimestampUTC, Status: "resolved"},
	}}
	if err := server.ingestAppDynamicsBatch(context.Background(), job, 0, payload, executionLeaseTerm{}); err != nil {
		t.Fatal(err)
	}
	url := "/api/v1/tests/" + job.RunID + "/monitor-evidence"
	first := timeslotRequest(t, server, http.MethodGet, url+"?limit=1", nil)
	var page struct {
		Evidence []RunMonitorEvidence `json:"evidence"`
		HasMore  bool                 `json:"has_more"`
		AfterKey string               `json:"after_key"`
	}
	if first.Code != http.StatusOK || json.Unmarshal(first.Body.Bytes(), &page) != nil || len(page.Evidence) != 1 || page.Evidence[0].SourceID != job.Source.ID || !page.HasMore || page.AfterKey == "" {
		t.Fatalf("first page: %d %s", first.Code, first.Body.String())
	}
	firstID := page.Evidence[0].ID
	if firstID == "101" && (page.Evidence[0].Properties["requestguid"] != requestGUID || page.Evidence[0].Properties["run_id"] != job.RunID) {
		t.Fatal("validated correlation identities were redacted")
	}
	second := timeslotRequest(t, server, http.MethodGet, url+"?limit=1&after_key="+page.AfterKey, nil)
	if second.Code != http.StatusOK || json.Unmarshal(second.Body.Bytes(), &page) != nil || len(page.Evidence) != 1 || page.Evidence[0].SourceID != job.Source.ID || page.HasMore || page.Evidence[0].ID == firstID {
		t.Fatalf("second page: %d %s", second.Code, second.Body.String())
	}
	if page.Evidence[0].ID == "101" && (page.Evidence[0].Properties["requestguid"] != requestGUID || page.Evidence[0].Properties["run_id"] != job.RunID) {
		t.Fatal("validated correlation identities were redacted")
	}
	for _, query := range []string{"limit=0", "limit=101", "limit=invalid", "kind=raw_callgraph"} {
		result := timeslotRequest(t, server, http.MethodGet, url+"?"+query, nil)
		if result.Code != http.StatusBadRequest {
			t.Fatalf("invalid query %s: %d %s", query, result.Code, result.Body.String())
		}
	}
}
func TestAppDynamicsDirectReadinessDoesNotRequireCollectorSigning(t *testing.T) {
	server, _, job, _ := apiAppDynamicsMonitorFixture(t)
	record, _ := server.store.GetTest(job.RunID)
	if err := server.runner.validateMonitorStorageAndSigning(record); err != nil {
		t.Fatalf("direct requires unused collector signing key: %v", err)
	}
}

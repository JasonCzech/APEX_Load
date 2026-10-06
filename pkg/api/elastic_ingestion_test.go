package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/elastic"
	"github.com/jasonczech/apexload/pkg/storage"
)

func apiElasticMonitorFixture(t *testing.T, configure ...func(*elastic.Selection)) (*Server, providerMonitorStore, storage.RunMonitorJob, analysis.MetricPoint, analysis.ProviderEvidence) {
	t.Helper()
	server, db := newTimeslotServer(t)
	t.Setenv("APEXLOAD_RUN_MONITOR_PROJECTS", "proj-a")
	connections := db.(storage.ElasticConnectionStore)
	c := &storage.ELKConnection{ID: "elastic-api", ProjectID: "proj-a", Name: "Elastic", Enabled: true, CollectionMode: "direct", Config: elastic.Config{Endpoint: "https://search.example.com", Product: "elasticsearch", AuthType: "api_key"}, APIKeySecretName: "protected-key-reference"}
	if err := connections.CreateELKConnection(c); err != nil {
		t.Fatal(err)
	}
	_, err := connections.UpdateElasticConnectionHealth(c.ID, c.ProjectID, c.Revision, "connected", "", elastic.Capabilities{Product: "elasticsearch", Version: "8.14.0", DSL: true, PIT: true, Logs: true}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	store := db.(providerMonitorStore)
	source := storage.MonitorSource{ID: "elastic-source", ProjectID: c.ProjectID, Name: "Elastic source", Enabled: true, Pack: "elastic", ExecutorMode: "direct", ConnectionID: c.ID, Elastic: &elastic.Selection{ProjectID: c.ProjectID, Indices: []string{"logs-*"}, Logs: true, Metrics: []elastic.MetricSelection{{Name: "calls", Statistic: "count", Unit: "count"}}}}
	for _, apply := range configure {
		apply(source.Elastic)
	}
	saved, err := store.SaveMonitorSource(source, 0)
	if err != nil {
		t.Fatal(err)
	}
	source = *saved
	start := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Minute)
	record := &storage.TestRecord{ID: "elastic-run", ProjectID: c.ProjectID, Name: "Elastic run", Status: "RUNNING", StartTime: start, StartedAt: &start, MonitorBindings: &storage.MonitorBindings{Sources: []storage.MonitorBinding{{SourceID: source.ID}}}}
	if err = db.SaveTest(record); err != nil {
		t.Fatal(err)
	}
	jobs, err := store.StartRunMonitors(c.ProjectID, record.ID, "", start, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	definition := elastic.QueryMetricDefinitions(*source.Elastic)[0]
	v := 0.0
	p := analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: c.ProjectID, RunID: record.ID, Source: source.ID, TimestampUTC: start, MetricName: "calls", Kind: analysis.KindGauge, Value: &v, Unit: "count", Quality: analysis.QualityValid, Provider: &analysis.ProviderMeasurement{Name: "elasticsearch", MetricID: definition.QueryID, QueryID: definition.QueryID, WindowKind: "bucket", BucketStart: start, BucketEnd: start.Add(10 * time.Second), ResolutionSeconds: 10, Statistic: "count", Count: &v, Sum: &v, Settled: true, AggregationEligible: true}}
	var query string
	for _, d := range elastic.QuerySignalDefinitions(*source.Elastic) {
		if d.Kind == "log" {
			query = d.QueryID
		}
	}
	e := analysis.ProviderEvidence{ID: "document-one", Provider: "elasticsearch", QueryID: query, Kind: "log", Index: "logs-2026", DocumentID: "one", TimestampUTC: start.Add(time.Second), Service: "orders", Correlation: "service_time", Log: &analysis.ProviderLog{Message: "Authorization: Bearer private-token", Attributes: map[string]analysis.ScalarValue{"password": {Kind: "string", StringValue: "plain-sensitive-password"}}}}
	return server, store, jobs[0], p, e
}

func TestElasticCanonicalIngestionScrubRetriesAndFilters(t *testing.T) {
	server, store, job, p, e := apiElasticMonitorFixture(t)
	batch := storage.ProviderMonitorBatch{QueryFromUTC: job.StartedAt, QueryUntilUTC: job.StartedAt.Add(time.Minute), Points: []analysis.MetricPoint{p}, Evidence: []analysis.ProviderEvidence{e}}
	for range 2 {
		if err := server.ingestExpandedElasticBatch(context.Background(), job, 0, batch, executionLeaseTerm{}); err != nil {
			t.Fatal(err)
		}
	}
	value := 9.0
	batch.Points[0].Value = &value
	batch.Points[0].Provider.Count, batch.Points[0].Provider.Sum = &value, &value
	batch.Evidence[0].Status = "resolved"
	if err := server.ingestExpandedElasticBatch(context.Background(), job, 1, batch, executionLeaseTerm{}); err != nil {
		t.Fatal(err)
	}
	page, err := loadCanonicalMonitorData(context.Background(), store, job.ProjectID, job.RunID, job.Source.ID, "point", "", 100)
	if err != nil || len(page.Points) != 1 || *page.Points[0].Value != 9 {
		t.Fatalf("head %+v %v", page, err)
	}
	response := timeslotRequest(t, server, http.MethodGet, "/api/v1/tests/"+job.RunID+"/monitor-evidence?signal=log&service=orders", nil)
	if response.Code != 200 || strings.Contains(response.Body.String(), "private-token") || strings.Contains(response.Body.String(), "plain-sensitive-password") {
		t.Fatalf("evidence %d %s", response.Code, response.Body.String())
	}
	public := timeslotRequest(t, server, http.MethodGet, "/api/v1/tests/"+job.RunID+"/monitors", nil)
	if public.Code != 200 || strings.Contains(public.Body.String(), "protected-key-reference") || strings.Contains(public.Body.String(), "elastic_connection_snapshot") {
		t.Fatalf("jobs %d %s", public.Code, public.Body.String())
	}
	receipts, _ := store.ListMonitorBatchReceipts(job.ProjectID, job.RunID, "", -1, 10)
	if len(receipts) != 2 {
		t.Fatal("retry consumed another receipt")
	}
	body, err := readMonitorArtifact(context.Background(), receipts[0].ArtifactKey, receipts[0].Digest)
	if err != nil || !json.Valid(body) || strings.Contains(string(body), "plain-sensitive-password") {
		t.Fatal("unsafe artifact")
	}
}

func TestElasticFeatureDisableDrainsExistingRunAndPreservesEvidence(t *testing.T) {
	server, store, job, point, evidence := apiElasticMonitorFixture(t)
	batch := storage.ProviderMonitorBatch{QueryFromUTC: job.StartedAt, QueryUntilUTC: job.StartedAt.Add(time.Minute), Points: []analysis.MetricPoint{point}, Evidence: []analysis.ProviderEvidence{evidence}}
	if err := server.ingestExpandedElasticBatch(context.Background(), job, 0, batch, executionLeaseTerm{}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APEXLOAD_RUN_MONITOR_PROJECTS", "")
	for _, path := range []string{
		"/api/v1/tests/" + job.RunID + "/monitors",
		"/api/v1/tests/" + job.RunID + "/monitor-evidence?signal=log",
		"/api/v1/tests/" + job.RunID + "/monitor-series?metric=calls&source_id=" + job.Source.ID,
	} {
		response := timeslotRequest(t, server, http.MethodGet, path, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("disabled feature hid existing run data: %s %d %s", path, response.Code, response.Body.String())
		}
	}
	if err := server.runner.validateTestMonitorBindings(job.ProjectID, &storage.MonitorBindings{Sources: []storage.MonitorBinding{{SourceID: job.Source.ID}}}, nil); err == nil {
		t.Fatal("disabled feature accepted a new assignment")
	}
	record, err := server.store.GetTest(job.RunID)
	if err != nil {
		t.Fatal(err)
	}
	end := time.Now().UTC().Add(-time.Second)
	record.Status, record.EndTime = "COMPLETE", &end
	if err := server.runner.saveManagedTestRecordSnapshot(record, ""); err != nil {
		t.Fatal(err)
	}
	draining, err := store.GetRunMonitorJob(job.ProjectID, job.ID)
	if err != nil || draining.State != "reconciling" || draining.LoadEndedAt == nil || !draining.LoadEndedAt.Equal(end) {
		t.Fatalf("disabled feature stranded existing monitor: %+v %v", draining, err)
	}
}

func TestElasticIngestionRejectsForgedScope(t *testing.T) {
	_, _, job, p, e := apiElasticMonitorFixture(t)
	for name, change := range map[string]func(*storage.ProviderMonitorBatch){
		"missing expanded window kind":       func(b *storage.ProviderMonitorBatch) { b.Points[0].Provider.WindowKind = "" },
		"contradictory expanded window kind": func(b *storage.ProviderMonitorBatch) { b.Points[0].Provider.WindowKind = "query_window" },
		"progress private state": func(b *storage.ProviderMonitorBatch) {
			b.Progress = &analysis.ProviderProgress{Cursor: "unapproved-provider-handle"}
		},
		"provider identity": func(b *storage.ProviderMonitorBatch) { b.Points[0].Provider.Backend = "grail" },
		"coverage private cache": func(b *storage.ProviderMonitorBatch) {
			b.Coverage = map[string]analysis.ProviderCoverage{"q": {Cached: true}}
		},
		"evidence backend": func(b *storage.ProviderMonitorBatch) { b.Evidence[0].Backend = "grail" },
		"source":           func(b *storage.ProviderMonitorBatch) { b.Points[0].Source = "other" },
		"metric identity":  func(b *storage.ProviderMonitorBatch) { b.Points[0].Provider.MetricID = "other" },
		"statistic":        func(b *storage.ProviderMonitorBatch) { b.Points[0].Provider.Statistic = "average" },
		"received":         func(b *storage.ProviderMonitorBatch) { b.Points[0].ReceivedAt = time.Now().UTC() },
		"query":            func(b *storage.ProviderMonitorBatch) { b.Evidence[0].QueryID = "unapproved" },
		"index":            func(b *storage.ProviderMonitorBatch) { b.Evidence[0].Index = "private-secrets" },
		"outside":          func(b *storage.ProviderMonitorBatch) { b.Evidence[0].TimestampUTC = job.StartedAt.Add(-time.Second) },
		"coverage": func(b *storage.ProviderMonitorBatch) {
			b.Coverage = map[string]analysis.ProviderCoverage{"unapproved": {QueryID: "unapproved", Status: "complete", From: b.QueryFromUTC, To: b.QueryUntilUTC}}
		},
		"future ingestion": func(b *storage.ProviderMonitorBatch) {
			future := time.Now().UTC().Add(time.Hour)
			b.Evidence[0].IngestedAt = &future
		},
	} {
		t.Run(name, func(t *testing.T) {
			point := p
			metadata := *p.Provider
			point.Provider = &metadata
			batch := storage.ProviderMonitorBatch{QueryFromUTC: job.StartedAt, QueryUntilUTC: job.StartedAt.Add(time.Minute), Points: []analysis.MetricPoint{point}, Evidence: []analysis.ProviderEvidence{e}}
			change(&batch)
			if err := validateElasticBatch(job, &batch, time.Now().UTC(), true); err == nil {
				t.Fatal("forged scope accepted")
			}
		})
	}
}

func TestElasticLiveIngestionCursorPreservesEventWindowAndFinalRead(t *testing.T) {
	start := time.Now().UTC().Add(-time.Hour)
	watermark := start.Add(30 * time.Minute)
	job := storage.RunMonitorJob{StartedAt: start, State: "collecting", QueryWatermark: watermark, ElasticIngestionWatermark: watermark, Source: storage.MonitorSource{Elastic: &elastic.Selection{IngestedField: "event.ingested"}}}
	from, to := expandedElasticQueryWindow(job, start.Add(time.Hour))
	if !from.Equal(start) || !to.Equal(start.Add(time.Hour)) || !elasticIngestionFrom(job).Equal(watermark.Add(-120*time.Second)) {
		t.Fatal("live ingestion filter lost the approved event window")
	}
	job.State = "reconciling"
	job.LoadEndedAt = &to
	if !elasticIngestionFrom(job).IsZero() {
		t.Fatal("final reconciliation omitted earlier ingestion")
	}
	job.Source.Elastic.IngestedField = ""
	job.State = "collecting"
	from, _ = expandedElasticQueryWindow(job, to)
	if !from.Equal(watermark.Add(-120 * time.Second)) {
		t.Fatal("event-only source lost overlap")
	}
}

func TestElasticResolvedAliasAndDataStreamEvidenceScope(t *testing.T) {
	_, _, original, _, evidence := apiElasticMonitorFixture(t)
	for _, concrete := range []string{".ds-application-logs-2026.10.05-000001", "application-logs-2026"} {
		t.Run(concrete, func(t *testing.T) {
			job := original
			selection := *original.Source.Elastic
			selection.Indices = []string{"current-logs"}
			job.Source.Elastic = &selection
			e := evidence
			e.Index = concrete
			for _, d := range elastic.QuerySignalDefinitions(selection) {
				if d.Kind == "log" {
					e.QueryID = d.QueryID
				}
			}
			batch := storage.ProviderMonitorBatch{ResolvedIndices: []string{concrete}, QueryFromUTC: job.StartedAt, QueryUntilUTC: job.StartedAt.Add(time.Minute), Evidence: []analysis.ProviderEvidence{e}}
			if err := validateElasticBatch(job, &batch, time.Now().UTC(), true); err != nil {
				t.Fatal(err)
			}
			job.ElasticResolvedIndices = []string{"different-concrete-index"}
			if err := validateElasticBatch(job, &batch, time.Now().UTC(), true); err == nil {
				t.Fatal("changed fixed-window concrete scope accepted")
			}
			job.ElasticResolvedIndices = nil
			batch.ResolvedIndices = nil
			if err := validateElasticBatch(job, &batch, time.Now().UTC(), true); err == nil {
				t.Fatal("unresolved alias accepted an arbitrary concrete index")
			}
		})
	}
}

func TestElasticOptionalReadinessWarnsAndRequiredReadinessBlocks(t *testing.T) {
	server, _, job, _, _ := apiElasticMonitorFixture(t)
	bindings := &storage.MonitorBindings{Sources: []storage.MonitorBinding{{SourceID: job.Source.ID}}}
	server.runner.mu.Lock()
	server.runner.tests[job.RunID] = &ManagedTest{ID: job.RunID, ProjectID: job.ProjectID, MonitorBindings: bindings}
	server.runner.mu.Unlock()
	if err := server.runner.validatePreparedMonitorReadiness(job.RunID); err != nil {
		t.Fatal("optional unavailable credential blocked preparation", err)
	}
	server.runner.mu.RLock()
	warnings := server.runner.tests[job.RunID].MonitorWarnings
	server.runner.mu.RUnlock()
	if len(warnings) != 1 || strings.Contains(warnings[0], "protected-key-reference") {
		t.Fatalf("warnings %+v", warnings)
	}
	bindings.Sources[0].Required = true
	if err := server.runner.validatePreparedMonitorReadiness(job.RunID); err == nil {
		t.Fatal("required unavailable credential allowed start")
	}
}

func TestElasticPublicJobsHideRetiredHandlesAndPendingWatermarks(t *testing.T) {
	now := time.Now().UTC()
	job := storage.RunMonitorJob{ElasticResolvedIndices: []string{"private-concrete-scope"}, ElasticRetiredContinuations: []*analysis.ProviderContinuation{{PITID: "private-owned-handle"}}, ElasticPendingIngestionWatermark: now}
	public := safeMonitorJob(job)
	body, _ := json.Marshal(public)
	if strings.Contains(string(body), "private-owned-handle") || strings.Contains(string(body), "pending_ingestion_watermark") || strings.Contains(string(body), "private-concrete-scope") {
		t.Fatal("private continuation disclosed", string(body))
	}
}

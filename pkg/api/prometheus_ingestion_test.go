package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/jasonczech/apexload/pkg/prometheus"
	"github.com/jasonczech/apexload/pkg/storage"
)

func apiPrometheusFixture(t *testing.T, configure ...func(*prometheus.Selection)) (*Server, providerMonitorStore, storage.RunMonitorJob, analysis.MetricPoint) {
	t.Helper()
	server, db := newTimeslotServer(t)
	if server.providerMonitoringCancel != nil {
		server.providerMonitoringCancel()
	}
	t.Setenv("APEXLOAD_RUN_MONITOR_PROJECTS", "proj-a")
	t.Setenv("APEXLOAD_MONITOR_SIGNING_SEED", "")
	connections := db.(storage.PrometheusConnectionStore)
	c := &storage.PrometheusConnection{ID: "prom-api", ProjectID: "proj-a", Name: "Prometheus", Enabled: true, CollectionMode: "direct", Config: prometheus.Config{Endpoint: "https://prom.example", AuthType: "bearer"}, TokenSecretName: "protected-token-reference"}
	if err := connections.CreatePrometheusConnection(c); err != nil {
		t.Fatal(err)
	}
	if _, err := connections.UpdatePrometheusConnectionHealth(c.ID, c.ProjectID, c.Revision, "connected", "", prometheus.Capabilities{Metrics: true, Targets: true, Alerts: true}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	monitors := db.(providerMonitorStore)
	query := prometheus.Query{ID: "cpu", Name: "cpu", Expression: "vector(0)", Unit: "percent", Semantics: "gauge"}
	source := storage.MonitorSource{ID: "prom-source", ProjectID: c.ProjectID, Name: "Prometheus", Enabled: true, Pack: "prometheus-query", ExecutorMode: "direct", ConnectionID: c.ID, IntervalSeconds: 30, Prometheus: &prometheus.Selection{ProjectID: c.ProjectID, StepSeconds: 15, Queries: []prometheus.Query{query}}}
	for _, apply := range configure {
		apply(source.Prometheus)
	}
	query = source.Prometheus.Queries[0]
	if _, err := monitors.SaveMonitorSource(source, 0); err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Millisecond).Add(123 * time.Microsecond)
	rule := analysis.MonitorRule{SourceID: source.ID, Metric: "cpu", QueryID: "cpu", Unit: "percent", Statistic: "avg", Comparator: "lte", Threshold: 5}
	record := &storage.TestRecord{ID: "prom-run", ProjectID: c.ProjectID, Name: "Prometheus run", Status: "RUNNING", StartTime: start, StartedAt: &start, GoalConfig: &GoalConfig{MonitorRules: []analysis.MonitorRule{rule}}, MonitorBindings: &storage.MonitorBindings{Sources: []storage.MonitorBinding{{SourceID: source.ID, Required: true}}}}
	if err := db.SaveTest(record); err != nil {
		t.Fatal(err)
	}
	jobs, err := monitors.StartRunMonitors(c.ProjectID, record.ID, "", start, time.Now().UTC().Add(time.Hour))
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	value := 0.0
	p := analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: c.ProjectID, RunID: record.ID, Source: source.ID, TimestampUTC: prometheusGridAnchor(start), MetricName: "cpu", Kind: analysis.KindGauge, Value: &value, Unit: "percent", Quality: analysis.QualityValid, Provider: &analysis.ProviderMeasurement{EntityIDs: map[string]string{"series": strings.Repeat("c", 64)}, Name: "prometheus", Mode: "evaluation_point", BackendProfile: "prometheus", MetricID: query.ID, QueryID: query.ID, DefinitionHash: prometheus.DefinitionHash(query), EvaluationStepSeconds: 15, Statistic: "gauge", Settled: true, AggregationEligible: true}}
	return server, monitors, jobs[0], p
}

type prometheusAPIRoundTrip func(*http.Request) (*http.Response, error)

func (f prometheusAPIRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPrometheusDirectExecutorCollectsWithoutSyntheticCollector(t *testing.T) {
	s, store, job, _ := apiPrometheusFixture(t)
	job.QueryFromUTC = prometheusGridAnchor(job.StartedAt)
	job.QueryUntilUTC = job.QueryFromUTC.Add(5 * time.Minute)
	s.runner.projectSecretStore = &devSecretStore{value: "fixture-token"}
	requests := 0
	s.prometheusClient = func(config prometheus.Config, credentials prometheus.Credentials) (*prometheus.Client, error) {
		if credentials.Token != "fixture-token" {
			t.Fatal("credential did not resolve in executor")
		}
		return prometheus.NewClientWithHTTPClient(config, credentials, &http.Client{Transport: prometheusAPIRoundTrip(func(r *http.Request) (*http.Response, error) {
			requests++
			if r.URL.Path != "/api/v1/query_range" || r.Header.Get("Authorization") != "Bearer fixture-token" {
				t.Fatal("unexpected upstream request", r.URL.Path)
			}
			if err := r.ParseForm(); err != nil {
				t.Fatal("invalid query form", err)
			}
			parse := func(name string) float64 {
				value, err := strconv.ParseFloat(r.Form.Get(name), 64)
				if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
					t.Fatalf("invalid query parameter %s", name)
				}
				return value
			}
			start, end, step := parse("start"), parse("end"), parse("step")
			if step <= 0 || end < start || (end-start)/step > 1000 {
				t.Fatal("invalid or excessive query grid")
			}
			samples := []any{}
			for at := start; at <= end; at += step {
				samples = append(samples, []any{at, "0"})
			}
			body, _ := json.Marshal(map[string]any{"status": "success", "data": map[string]any{"resultType": "matrix", "result": []any{map[string]any{"metric": map[string]string{}, "values": samples}}}})
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
		})})
	}
	s.collectDirectPrometheus(context.Background(), job)
	page, err := loadCanonicalMonitorData(context.Background(), store, job.ProjectID, job.RunID, job.Source.ID, "point", "", 100)
	if err != nil || len(page.Points) != 21 || requests != 2 {
		t.Fatal("direct collection", requests, len(page.Points), err)
	}
	current, err := store.GetRunMonitorJob(job.ProjectID, job.ID)
	if err != nil || !current.QueryWatermark.Equal(job.QueryUntilUTC) || current.NextSequence != 1 || current.Source.CollectorID != "" {
		t.Fatal(fmt.Sprintf("direct state %+v %v", current, err))
	}
}

func TestProviderSettledClaimToleratesSmallClockSkew(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name      string
		age       time.Duration
		settled   bool
		premature bool
	}{
		{"fully settled", 125 * time.Second, true, false},
		{"exactly at allowance", 120 * time.Second, true, false},
		{"collector clock five seconds ahead", 115 * time.Second, true, false},
		{"at tolerance edge", 110 * time.Second, true, false},
		{"beyond tolerance", 105 * time.Second, true, true},
		{"fresh observation", time.Second, true, true},
		{"provisional claim is never premature", time.Second, false, false},
	} {
		if got := providerSettledClaimPremature(tc.settled, now.Add(-tc.age), 120, now); got != tc.premature {
			t.Errorf("%s: premature=%v, want %v", tc.name, got, tc.premature)
		}
	}
	s, _, job, p := apiPrometheusFixture(t)
	recent := prometheusGridAnchor(job.StartedAt)
	for recent.Before(time.Now().UTC().Add(-30 * time.Second)) {
		recent = recent.Add(15 * time.Second)
	}
	p.TimestampUTC = recent
	payload := storage.ProviderMonitorBatch{QueryFromUTC: job.StartedAt, QueryUntilUTC: time.Now().UTC(), Points: []analysis.MetricPoint{p}}
	if err := s.ingestPrometheusBatch(context.Background(), job, 0, payload, executionLeaseTerm{}); err == nil || !strings.Contains(err.Error(), "unsettled") {
		t.Fatalf("recent evaluation claimed settled: %v", err)
	}
}

func TestPrometheusCanonicalRetryRevisionAndPublicProjection(t *testing.T) {
	s, store, job, p := apiPrometheusFixture(t)
	payload := storage.ProviderMonitorBatch{QueryFromUTC: job.StartedAt, QueryUntilUTC: job.StartedAt.Add(time.Minute), Points: []analysis.MetricPoint{p}}
	for range 2 {
		if err := s.ingestPrometheusBatch(context.Background(), job, 0, payload, executionLeaseTerm{}); err != nil {
			t.Fatal(err)
		}
	}
	value := 4.0
	payload.Points[0].Value = &value
	if err := s.ingestPrometheusBatch(context.Background(), job, 0, payload, executionLeaseTerm{}); err == nil {
		t.Fatal("conflicting retry accepted")
	}
	if err := s.ingestPrometheusBatch(context.Background(), job, 1, payload, executionLeaseTerm{}); err != nil {
		t.Fatal(err)
	}
	page, err := loadCanonicalMonitorData(context.Background(), store, job.ProjectID, job.RunID, job.Source.ID, "point", "", 100)
	if err != nil || len(page.Points) != 1 || *page.Points[0].Value != 4 || page.Points[0].Quality != analysis.QualityValid {
		t.Fatal(page, err)
	}
	response := timeslotRequest(t, s, http.MethodGet, "/api/v1/tests/"+job.RunID+"/monitors", nil)
	if response.Code != 200 || strings.Contains(response.Body.String(), "protected-token") || strings.Contains(response.Body.String(), "connection_snapshot") {
		t.Fatal(response.Code, response.Body.String())
	}
	series := timeslotRequest(t, s, http.MethodGet, "/api/v1/tests/"+job.RunID+"/monitor-series?source_id="+job.Source.ID+"&metric=cpu", nil)
	if series.Code != 200 || !strings.Contains(series.Body.String(), "evaluation_point") {
		t.Fatal(series.Code, series.Body.String())
	}
}

func TestPrometheusScopeGridAndCursorValidation(t *testing.T) {
	s, _, job, p := apiPrometheusFixture(t)
	for _, change := range []func(*analysis.MetricPoint){func(p *analysis.MetricPoint) { p.ProjectID = "other" }, func(p *analysis.MetricPoint) { p.TimestampUTC = p.TimestampUTC.Add(time.Millisecond) }, func(p *analysis.MetricPoint) { p.Provider.DefinitionHash = strings.Repeat("a", 64) }, func(p *analysis.MetricPoint) { p.Dimensions = map[string]string{"unapproved": "value"} }, func(p *analysis.MetricPoint) {
		p.Provider.FreshnessKnown = true
		p.Provider.UnderlyingSampleTime = &p.TimestampUTC
	}} {
		bad := p
		m := *p.Provider
		bad.Provider = &m
		change(&bad)
		if err := s.ingestPrometheusBatch(context.Background(), job, 0, storage.ProviderMonitorBatch{QueryFromUTC: job.StartedAt, QueryUntilUTC: job.StartedAt.Add(time.Minute), Points: []analysis.MetricPoint{bad}}, executionLeaseTerm{}); err == nil {
			t.Fatal("unapproved point accepted")
		}
	}
	until := job.StartedAt.Add(time.Minute)
	payload := storage.ProviderMonitorBatch{QueryFromUTC: job.StartedAt, QueryUntilUTC: until, Progress: &monitorprovider.QueryProgress{TotalQueries: 1, NextQuery: 0, Complete: false}, QueryWindowEnd: &until}
	if err := s.ingestPrometheusBatch(context.Background(), job, 0, payload, executionLeaseTerm{}); err == nil {
		t.Fatal("partial cursor advanced watermark")
	}
}

func TestPrometheusConnectionSecretsParticipateInMetadataDiscovery(t *testing.T) {
	server, durable, _, project := newProjectSecretAPIServer(t)
	discovery := &recordingDiscoverySecretStore{}
	server.projectSecretStore = discovery
	connection := &storage.PrometheusConnection{ID: "discovered-prom", ProjectID: project, Name: "Prometheus", Config: prometheus.Config{Endpoint: "https://prom.example", AuthType: "bearer"}, TokenSecretName: "PROM_TOKEN", ClientCertificateSecretName: "PROM_CERT"}
	if err := durable.CreatePrometheusConnection(connection); err != nil {
		t.Fatal(err)
	}
	if complete, err := server.discoverProjectSecretMetadata(context.Background(), project); err != nil || !complete {
		t.Fatal(complete, err)
	}
	discovery.mu.Lock()
	defer discovery.mu.Unlock()
	if len(discovery.indexed) != 2 {
		t.Fatal("Prometheus-only secret references were not discovered", len(discovery.indexed))
	}
}

func TestPrometheusDiagnosticCoverageCannotInventPolls(t *testing.T) {
	s, _, job, _ := apiPrometheusFixture(t)
	job.Source.Prometheus.Targets = true
	job.Source.Prometheus.EvidenceSelector = `{job="sut"}`
	from, to := job.StartedAt, job.StartedAt.Add(time.Minute)
	old := from.Add(-time.Second)
	future := time.Now().UTC().Add(time.Hour)
	for _, c := range []monitorprovider.Coverage{
		{Status: "complete", From: from, To: to},
		{Status: "complete", From: from, To: to, ObservedAt: &old},
		{Status: "complete", From: from, To: to, ObservedAt: &future},
		{Status: "partial", From: from, To: to, Cached: true},
		{Status: "partial", From: from, To: to, CacheAgeSeconds: 1},
	} {
		if err := s.ingestPrometheusBatch(context.Background(), job, 0, storage.ProviderMonitorBatch{QueryFromUTC: from, QueryUntilUTC: to, Coverage: map[string]monitorprovider.Coverage{"targets": c}}, executionLeaseTerm{}); err == nil {
			t.Fatal("invalid poll freshness accepted")
		}
	}
	job.State = "reconciling"
	observed := time.Now().UTC()
	c := monitorprovider.Coverage{Status: "complete", From: from, To: to, ObservedAt: &observed}
	if err := s.ingestPrometheusBatch(context.Background(), job, 0, storage.ProviderMonitorBatch{QueryFromUTC: from, QueryUntilUTC: to, Coverage: map[string]monitorprovider.Coverage{"targets": c}}, executionLeaseTerm{}); err == nil {
		t.Fatal("historical target coverage invented during reconciliation")
	}
}

func TestPrometheusPreparationBudgetsIncludeExactWindowRules(t *testing.T) {
	s, _, job, _ := apiPrometheusFixture(t)
	source := job.Source
	source.Prometheus.Queries[0].TerminalExpression = "sum(increase(requests_total[{{window.duration}}]))"
	source.Prometheus.Queries[0].Semantics = "sum"
	bindings := &storage.MonitorBindings{Sources: []storage.MonitorBinding{{SourceID: source.ID, Required: true}}}
	if err := s.runner.validatePrometheusCollectionBudgets(bindings, []storage.MonitorSource{source}, nil); err != nil {
		t.Fatal(err)
	}
	goals := &GoalConfig{}
	for index := 1; index <= 128; index++ {
		end := index
		goals.MonitorRules = append(goals.MonitorRules, analysis.MonitorRule{SourceID: source.ID, Metric: "cpu", QueryID: "cpu", Unit: "percent", EvaluationMode: "exact_window", Statistic: "sum", EndOffsetSeconds: &end, Comparator: "lte", Threshold: 1})
	}
	if err := s.runner.validatePrometheusCollectionBudgets(bindings, []storage.MonitorSource{source}, goals); err == nil {
		t.Fatal("terminal rule requests were omitted from required reconciliation budget")
	}
}

func TestPrometheusCapturedDiagnosticsCommitAfterLoadStops(t *testing.T) {
	s, store, job, _ := apiPrometheusFixture(t, func(selection *prometheus.Selection) {
		selection.Targets = true
		selection.EvidenceSelector = `{job="sut"}`
	})
	end := time.Now().UTC().Add(-5 * time.Second)
	record, _ := s.store.GetTest(job.RunID)
	record.Status, record.EndTime = "COMPLETE", &end
	if _, err := s.store.(interface {
		SaveTestWithRunMonitors(*storage.TestRecord, string, string, bool) (bool, error)
	}).SaveTestWithRunMonitors(record, "", "", true); err != nil {
		t.Fatal(err)
	}
	current, err := store.GetRunMonitorJob(job.ProjectID, job.ID)
	if err != nil || current.State != "reconciling" {
		t.Fatal(current, err)
	}
	observed := end.Add(-time.Second)
	batch := storage.ProviderMonitorBatch{QueryFromUTC: job.StartedAt, QueryUntilUTC: end, Evidence: []monitorprovider.Diagnostic{{ID: "target-" + strings.Repeat("d", 64), Kind: "target", TimestampUTC: observed, EntityIDs: map[string]string{"job": "sut"}, Status: "up"}}, Coverage: map[string]monitorprovider.Coverage{"targets": {Status: "complete", From: job.StartedAt, To: end, ObservedAt: &observed, Evidence: 1}}}
	batch.Evidence[0].ApplicationID = "excluded-provider-field"
	batch.Evidence[0].DeepLink = "https://prom.example/targets?access_token=excluded"
	if err := s.ingestPrometheusBatch(context.Background(), *current, 0, batch, executionLeaseTerm{}); err != nil {
		t.Fatal("captured in-flight sample was rejected", err)
	}
	page, err := loadCanonicalMonitorData(context.Background(), store, job.ProjectID, job.RunID, job.Source.ID, "diagnostic", "", 100)
	if err != nil || len(page.Evidence) != 1 || page.Evidence[0].ApplicationID != "" || page.Evidence[0].DeepLink != "" {
		t.Fatal(page, err)
	}
	batch.Evidence[0].ID = "arbitrary-captured-identity"
	if err := s.ingestPrometheusBatch(context.Background(), *current, 1, batch, executionLeaseTerm{}); err == nil {
		t.Fatal("non-fingerprint diagnostic identity accepted")
	}
	batch.Evidence[0].ID = "target-" + strings.Repeat("d", 64)
	batch.Evidence[0].TimestampUTC = observed.Add(time.Millisecond)
	if err := s.ingestPrometheusBatch(context.Background(), *current, 1, batch, executionLeaseTerm{}); err == nil {
		t.Fatal("diagnostic and snapshot observation times diverged")
	}
	batch.Evidence[0].TimestampUTC = end.Add(time.Second)
	if err := s.ingestPrometheusBatch(context.Background(), *current, 1, batch, executionLeaseTerm{}); err == nil {
		t.Fatal("post-load current state accepted as run evidence")
	}
}

func TestPrometheusDiagnosticSightingsUseDurableSnapshotHistory(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", "filesystem")
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	s, store, job, _ := apiPrometheusFixture(t, func(selection *prometheus.Selection) {
		selection.Alerts = true
		selection.EvidenceSelector = `{job="sut"}`
	})
	first := job.StartedAt.Add(time.Minute)
	last := first.Add(time.Minute)
	missing := last.Add(time.Minute)
	until := missing.Add(time.Second)
	// Captured snapshots share one frozen query window; each sample retains
	// its actual observation time independently from that window.
	for sequence, at := range []time.Time{first, last, missing} {
		current, err := store.GetRunMonitorJob(job.ProjectID, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		job = *current
		batch := storage.ProviderMonitorBatch{QueryFromUTC: job.StartedAt, QueryUntilUTC: until, Coverage: map[string]monitorprovider.Coverage{"alerts": {Status: "complete", From: job.StartedAt, To: until, ObservedAt: &at}}}
		if sequence < 2 {
			batch.Evidence = []monitorprovider.Diagnostic{{ID: "alert-" + strings.Repeat("e", 64), Kind: "alert", TimestampUTC: at, EntityIDs: map[string]string{"job": "sut"}, Status: "firing"}}
			coverage := batch.Coverage["alerts"]
			coverage.Evidence = 1
			batch.Coverage["alerts"] = coverage
		}
		if err := s.ingestPrometheusBatch(context.Background(), job, int64(sequence), batch, executionLeaseTerm{}); err != nil {
			t.Fatal(err)
		}
		// Reading safe projections must not change immutable retry content.
		page, err := loadCanonicalMonitorData(context.Background(), store, job.ProjectID, job.RunID, job.Source.ID, "diagnostic", "", 100)
		if err != nil || len(page.Evidence) != 1 {
			t.Fatal(page, err)
		}
		d := page.Evidence[0]
		if d.Properties["first_observed_utc"] != first.Format(time.RFC3339Nano) {
			t.Fatal("first sighting was replaced", d)
		}
		response := timeslotRequest(t, s, http.MethodGet, "/api/v1/tests/"+job.RunID+"/monitor-evidence?kind=alert", nil)
		var result struct {
			Evidence []RunMonitorEvidence `json:"evidence"`
		}
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || len(result.Evidence) != 1 || result.Evidence[0].Properties["first_observed_utc"] != first.Format(time.RFC3339Nano) {
			t.Fatalf("filtered API lost provider first-sighting history: %d %s", response.Code, response.Body.String())
		}
		if sequence == 2 && (d.Status != "no_longer_observed" || !d.TimestampUTC.Equal(missing) || d.Properties["last_observed_utc"] != last.Format(time.RFC3339Nano) || d.Properties["state_observed_utc"] != missing.Format(time.RFC3339Nano)) {
			t.Fatal("sampled absence was not projected", d)
		}
		current, err = store.GetRunMonitorJob(job.ProjectID, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.ingestPrometheusBatch(context.Background(), *current, int64(sequence), batch, executionLeaseTerm{}); err != nil {
			t.Fatal("reading evidence changed retry identity", err)
		}
	}
	current, err := store.GetRunMonitorJob(job.ProjectID, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	job = *current
	failed := storage.ProviderMonitorBatch{QueryFromUTC: job.StartedAt, QueryUntilUTC: until, Coverage: map[string]monitorprovider.Coverage{"alerts": {Status: "unavailable", From: job.StartedAt, To: until}}}
	if err := s.ingestPrometheusBatch(context.Background(), job, 3, failed, executionLeaseTerm{}); err != nil {
		t.Fatal(err)
	}
	page, err := loadCanonicalMonitorData(context.Background(), store, job.ProjectID, job.RunID, job.Source.ID, "diagnostic", "", 100)
	if err != nil || len(page.Evidence) != 1 || page.Evidence[0].Status != "no_longer_observed" || !page.Evidence[0].TimestampUTC.Equal(missing) {
		t.Fatal("failed poll reverted the last observed state", page, err)
	}
	receipts, err := store.ListMonitorBatchReceipts(job.ProjectID, job.RunID, job.ID, -1, 10)
	if err != nil || len(receipts) != 4 {
		t.Fatal(receipts, err)
	}
	if err := os.WriteFile(filepath.Join(root, "_analysis", receipts[0].ArtifactKey), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCanonicalMonitorData(context.Background(), store, job.ProjectID, job.RunID, job.Source.ID, "diagnostic", "", 100); err == nil || !strings.Contains(err.Error(), "integrity") {
		t.Fatal("corrupt first sighting was accepted", err)
	}
}

func TestPrometheusPartialDiagnosticSnapshotDoesNotProveAbsence(t *testing.T) {
	at := time.Now().UTC().Add(-time.Minute)
	next := at.Add(30 * time.Second)
	d := monitorprovider.Diagnostic{Kind: "target", Status: "up", TimestampUTC: at}
	for _, coverage := range []monitorprovider.Coverage{{Status: "partial", ObservedAt: &next}, {Status: "complete", Truncated: true, ObservedAt: &next}, {Status: "complete", ObservedAt: &at}, {Status: "complete", ObservedAt: &next, Warnings: []string{"partial upstream results"}}, {Status: "no_data", ObservedAt: &next, Warnings: []string{"partial upstream results"}}} {
		job := storage.RunMonitorJob{Coverage: map[string]monitorprovider.Coverage{"targets": coverage}}
		if got := projectPrometheusDiagnostic(d, at, job); got.Status != "up" || !got.TimestampUTC.Equal(at) {
			t.Fatal("incomplete or cached snapshot established absence", got)
		}
	}
}

func TestPrometheusArchiveUsesCanonicalGridAndFrozenRules(t *testing.T) {
	for _, recovered := range []bool{false, true} {
		name := "original"
		if recovered {
			name = "restamped"
		}
		t.Run(name, func(t *testing.T) { testPrometheusArchiveUsesExecutionGrid(t, recovered) })
	}
}

func testPrometheusArchiveUsesExecutionGrid(t *testing.T, recovered bool) {
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", "filesystem")
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	s, store, job, p := apiPrometheusFixture(t)
	end := prometheusGridAnchor(job.StartedAt).Add(30 * time.Second)
	payload := storage.ProviderMonitorBatch{QueryFromUTC: job.StartedAt, QueryUntilUTC: end}
	for i := range 3 {
		point := p
		point.TimestampUTC = p.TimestampUTC.Add(time.Duration(i) * 15 * time.Second)
		payload.Points = append(payload.Points, point)
	}
	payload.Coverage = map[string]monitorprovider.Coverage{"cpu": {Status: "complete", From: job.StartedAt, To: end, Points: 3, ExpectedPoints: 3}}
	if err := s.ingestPrometheusBatch(context.Background(), job, 0, payload, executionLeaseTerm{}); err != nil {
		t.Fatal(err)
	}
	record, _ := s.store.GetTest(job.RunID)
	record.Status = "COMPLETE"
	record.EndTime = &end
	if saved, err := s.store.(interface {
		SaveTestWithRunMonitors(*storage.TestRecord, string, string, bool) (bool, error)
	}).SaveTestWithRunMonitors(record, "", "", true); err != nil || !saved {
		t.Fatal(saved, err)
	}
	root := filepath.Join(resolveArchiveRoot(), "prom-fixture")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	archive := &storage.RunArchiveRecord{TestID: job.RunID, ArchivePath: root, ManifestPath: filepath.Join(root, "manifest.json")}
	manifest := &RunArchiveManifest{TestID: job.RunID, CreatedAt: job.StartedAt, CompletedAt: end, Provenance: RunArchiveProvenance{StartedAt: &job.StartedAt, GoalConfig: &GoalConfig{MonitorRules: []analysis.MonitorRule{{SourceID: "changed", Metric: "changed"}}}}}
	if recovered {
		manifest = restampExecutionFixture(t, manifest, end.Add(time.Minute))
	}
	budget, err := newArchiveAdjunctBudgetForRecord(context.Background(), archive)
	if err != nil {
		t.Fatal(err)
	}
	defer budget.close()
	if err := s.runner.persistRunMonitorArchive(job.ProjectID, job.RunID, archive, manifest, budget); err != nil {
		t.Fatal(err)
	}
	if len(manifest.MonitorRuleResults) != 1 || manifest.MonitorRuleResults[0].Status != "pass" || manifest.MonitorRuleResults[0].CoveragePct != 100 || manifest.MonitorEvidenceCount != 3 {
		t.Fatal(manifest.MonitorRuleResults, manifest.MonitorEvidenceCount)
	}
	if manifest.MonitorCoverage[0].PrometheusSelection == nil || manifest.MonitorCoverage[0].ProviderCoverage["numeric:cpu"].ExpectedPoints != 3 {
		t.Fatal(manifest.MonitorCoverage)
	}
	if !manifest.MonitorCoverage[0].ProviderCoverage["numeric:cpu"].To.Equal(end) {
		t.Fatal("monitor coverage extended beyond execution", manifest.MonitorCoverage)
	}
	rows, err := store.ListProviderObservationHeads(job.ProjectID, job.RunID, job.Source.ID, "point", "", 100)
	if err != nil || len(rows) != 3 {
		t.Fatal(rows, err)
	}
	body, err := os.ReadFile(filepath.Join(root, manifest.MonitorEvidenceFile))
	if err != nil || !json.Valid([]byte("["+strings.ReplaceAll(strings.TrimSpace(string(body)), "\n", ",")+"]")) {
		t.Fatal(string(body), err)
	}
}

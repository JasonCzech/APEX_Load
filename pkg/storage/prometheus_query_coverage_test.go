package storage

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/jasonczech/apexload/pkg/prometheus"
)

func prometheusMonitorFixture(t *testing.T) (*SQLiteStore, MonitorSource, time.Time) {
	t.Helper()
	s, _, now := monitorFixture(t)
	return seedPrometheusMonitorFixture(t, s, now)
}

func seedPrometheusMonitorFixture(t *testing.T, s *SQLiteStore, now time.Time) (*SQLiteStore, MonitorSource, time.Time) {
	t.Helper()
	start := now.Add(-10 * time.Minute).Truncate(time.Minute)
	c := &PrometheusConnection{ID: "prom", ProjectID: "monitor-p", Name: "Prometheus", Enabled: true, CollectionMode: "direct", Config: prometheus.Config{Endpoint: "https://prometheus.example.com"}}
	if err := s.CreatePrometheusConnection(c); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdatePrometheusConnectionHealth(c.ID, c.ProjectID, c.Revision, "connected", "", prometheus.Capabilities{Metrics: true}, now); err != nil {
		t.Fatal(err)
	}
	source, err := s.SaveMonitorSource(MonitorSource{ID: "prom-source", Name: "PromQL", ProjectID: c.ProjectID, Pack: "prometheus-query", ExecutorMode: "direct", ConnectionID: c.ID, Enabled: true, IntervalSeconds: 60, Prometheus: &prometheus.Selection{ProjectID: c.ProjectID, Queries: []prometheus.Query{{ID: "cpu", Name: "cpu", Expression: "cpu_usage", Unit: "percent", Semantics: "gauge", AllowedLabels: []string{"instance"}}}}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SaveTest(&TestRecord{ID: "monitor-run", Name: "run", ProjectID: c.ProjectID, ScenarioID: "monitor-scenario", Status: "RUNNING", StartTime: start, StartedAt: &start, MonitorBindings: &MonitorBindings{Sources: []MonitorBinding{{SourceID: source.ID, Required: true}}}}); err != nil {
		t.Fatal(err)
	}
	return s, *source, start
}

func prometheusTestPoint(source MonitorSource, at time.Time, identity string) analysis.MetricPoint {
	v := float64(0)
	query := source.Prometheus.Queries[0]
	return analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: source.ProjectID, RunID: "monitor-run", Source: source.ID, TimestampUTC: at, MetricName: query.Name, Kind: analysis.KindGauge, Quality: analysis.QualityValid, Unit: query.Unit, Value: &v, Dimensions: map[string]string{"instance": "same"}, Provider: &analysis.ProviderMeasurement{Name: "prometheus", MetricID: query.ID, QueryID: query.ID, DefinitionHash: prometheus.DefinitionHash(query), BackendProfile: "prometheus", Mode: "evaluation_point", Statistic: "gauge", EvaluationStepSeconds: 15, EntityIDs: map[string]string{"series": identity}, Settled: true, AggregationEligible: true}}
}

func TestPrometheusUnlabelledObservationIdentitySurvivesPersistence(t *testing.T) {
	source := MonitorSource{ID: "source", ProjectID: "project", Prometheus: &prometheus.Selection{Queries: []prometheus.Query{{ID: "cpu", Name: "cpu", Expression: "vector(0)", Unit: "percent", Semantics: "gauge"}}}}
	point := prometheusTestPoint(source, time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC), strings.Repeat("a", 64))
	var unlabelledKey, unlabelledSeries string
	for _, tc := range []struct {
		name   string
		labels map[string]string
	}{
		{"nil", nil},
		{"empty", map[string]string{}},
		{"labelled", map[string]string{"instance": "sut"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			point.Dimensions = tc.labels
			body, err := json.Marshal(point)
			if err != nil {
				t.Fatal(err)
			}
			var decoded analysis.MetricPoint
			if err := json.Unmarshal(body, &decoded); err != nil {
				t.Fatal(err)
			}
			key, series := ObservationKey(point), providerSeriesKey(point)
			if key != ObservationKey(decoded) || series != providerSeriesKey(decoded) {
				t.Fatal("durable JSON changed canonical identity")
			}
			if tc.name == "nil" {
				unlabelledKey, unlabelledSeries = key, series
			}
			if tc.name == "empty" && (key != unlabelledKey || series != unlabelledSeries) {
				t.Fatal("empty labels must equal absent labels")
			}
			if tc.name == "labelled" && (key == unlabelledKey || series == unlabelledSeries) {
				t.Fatal("identity labels must remain distinct")
			}
		})
	}
}

func TestPrometheusChunkCoverageCommitsCursorCountsAndResetsWindow(t *testing.T) {
	s, source, start := prometheusMonitorFixture(t)
	jobs, err := s.StartRunMonitors(source.ProjectID, "monitor-run", "monitor-scenario", start, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	id := jobs[0].ID
	until := start.Add(time.Minute)
	next := start.Add(30 * time.Second)
	batch := ProviderMonitorBatch{Points: []analysis.MetricPoint{prometheusTestPoint(source, start, "one")}, QueryFromUTC: start, QueryUntilUTC: until, Progress: &monitorprovider.QueryProgress{TotalQueries: 1, NextTime: &next}, Coverage: map[string]monitorprovider.Coverage{"cpu": {Status: "partial", From: start, To: until, Points: 2, ExpectedPoints: 2, Warnings: []string{"first"}}}}
	receipt := providerReceipt(t, id, 0, batch, until)
	for range 2 {
		if err = s.AcceptProviderMonitorBatch(source.ProjectID, "", receipt, batch, "", "", 0); err != nil {
			t.Fatal(err)
		}
	}
	job, _ := s.GetRunMonitorJob(source.ProjectID, id)
	if job.QueryTime == nil || !job.QueryTime.Equal(next) || job.Coverage["cpu"].Points != 2 || !job.QueryWatermark.IsZero() {
		t.Fatalf("partial receipt %+v", job)
	}
	batch.Points = []analysis.MetricPoint{prometheusTestPoint(source, next, "one")}
	batch.Progress = &monitorprovider.QueryProgress{TotalQueries: 1, NextQuery: 1, Complete: true}
	batch.QueryWindowEnd = &until
	batch.Coverage = map[string]monitorprovider.Coverage{"cpu": {Status: "complete", From: start, To: until, Points: 3, ExpectedPoints: 3, MissingPoints: 1, Warnings: []string{"first", "second"}}}
	if err = s.AcceptProviderMonitorBatch(source.ProjectID, "", providerReceipt(t, id, 1, batch, until), batch, "", "", 0); err != nil {
		t.Fatal(err)
	}
	job, _ = s.GetRunMonitorJob(source.ProjectID, id)
	cov := job.Coverage["cpu"]
	if job.QueryTime != nil || !job.QueryWatermark.Equal(until) || cov.Status != "partial" || cov.Points != 5 || cov.ExpectedPoints != 5 || cov.MissingPoints != 1 || len(cov.Warnings) != 2 {
		t.Fatalf("lost chunk totals %+v %+v", job, cov)
	}
	until2 := until.Add(time.Minute)
	batch.QueryUntilUTC = until2
	batch.QueryWindowEnd = &until2
	batch.Coverage = map[string]monitorprovider.Coverage{"cpu": {Status: "complete", From: start, To: until2, Points: 9, ExpectedPoints: 9}}
	if err = s.AcceptProviderMonitorBatch(source.ProjectID, "", providerReceipt(t, id, 2, batch, until2), batch, "", "", 0); err != nil {
		t.Fatal(err)
	}
	job, _ = s.GetRunMonitorJob(source.ProjectID, id)
	if job.Coverage["cpu"].Points != 9 || job.Coverage["cpu"].MissingPoints != 0 {
		t.Fatalf("new window reused old totals %+v", job.Coverage)
	}
}

func TestPrometheusSeriesIdentityAndHeadBudgets(t *testing.T) {
	s, source, start := prometheusMonitorFixture(t)
	jobs, err := s.StartRunMonitors(source.ProjectID, "monitor-run", "monitor-scenario", start, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	a, b := prometheusTestPoint(source, start, "a"), prometheusTestPoint(source, start, "b")
	if ObservationKey(a) == ObservationKey(b) || providerSeriesKey(a) == providerSeriesKey(b) {
		t.Fatal("distinct complete label identities merged")
	}
	b = a
	b.Provider = &analysis.ProviderMeasurement{}
	*b.Provider = *a.Provider
	b.Provider.BackendProfile = "azure_managed"
	if providerSeriesKey(a) == providerSeriesKey(b) {
		t.Fatal("backend profile omitted from series identity")
	}
	points := make([]analysis.MetricPoint, 201)
	for i := range points {
		points[i] = prometheusTestPoint(source, start, fmt.Sprint(i))
	}
	batch := ProviderMonitorBatch{Points: points}
	if err = s.AcceptProviderMonitorBatch(source.ProjectID, "", providerReceipt(t, jobs[0].ID, 0, batch, start.Add(time.Minute)), batch, "", "", 0); err != nil {
		t.Fatal(err)
	}
	heads, err := s.ListProviderObservationHeads(source.ProjectID, "monitor-run", source.ID, "point", "", 1000)
	if err != nil || len(heads) != 200 {
		t.Fatalf("source budget heads=%d %v", len(heads), err)
	}
	job, _ := s.GetRunMonitorJob(source.ProjectID, jobs[0].ID)
	if !job.Coverage["retention"].Truncated {
		t.Fatal("series limit lost explicit truncation")
	}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := range 800 {
		if _, err = tx.Exec(`INSERT INTO run_monitor_observation_heads(job_id,record_key,sequence,record_index,record_kind,series_key,content_digest) VALUES(?,?,0,0,'point',?,'fixture')`, jobs[0].ID, fmt.Sprintf("run-budget-%d", i), fmt.Sprintf("series-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	other := jobs[0]
	other.ID = "other-job"
	allowed, err := providerHeadWithinLimits(tx, other, prometheusTestPoint(source, start, "new-series"), nil)
	if err != nil || allowed {
		t.Fatalf("run series quota bypassed %v %v", allowed, err)
	}
}

func TestPrometheusAzureDisplayCasingDoesNotChangeStableNumericDigest(t *testing.T) {
	s, source, start := prometheusMonitorFixture(t)
	jobs, err := s.StartRunMonitors(source.ProjectID, "monitor-run", "monitor-scenario", start, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	point := prometheusTestPoint(source, start, "same")
	point.Provider.BackendProfile = "azure_managed"
	point.Provider.DisplayLabels = map[string]string{"Instance": "SAME"}
	until := start.Add(time.Minute)
	batch := ProviderMonitorBatch{Points: []analysis.MetricPoint{point}, QueryFromUTC: start, QueryUntilUTC: until}
	if err = s.AcceptProviderMonitorBatch(source.ProjectID, "", providerReceipt(t, jobs[0].ID, 0, batch, until), batch, "", "", 0); err != nil {
		t.Fatal(err)
	}
	var first, second string
	if err = s.db.QueryRow(`SELECT content_digest FROM run_monitor_observation_heads WHERE job_id=? AND record_kind='point'`, jobs[0].ID).Scan(&first); err != nil {
		t.Fatal(err)
	}
	batch.Points[0].Provider.DisplayLabels = map[string]string{"instance": "same"}
	if err = s.AcceptProviderMonitorBatch(source.ProjectID, "", providerReceipt(t, jobs[0].ID, 1, batch, until), batch, "", "", 0); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRow(`SELECT content_digest FROM run_monitor_observation_heads WHERE job_id=? AND record_kind='point'`, jobs[0].ID).Scan(&second); err != nil {
		t.Fatal(err)
	}
	heads, err := s.ListProviderObservationHeads(source.ProjectID, "monitor-run", source.ID, "point", "", 10)
	if err != nil || len(heads) != 1 || heads[0].Sequence != 1 || first != second || batch.Points[0].Provider.DisplayLabels["instance"] != "same" {
		t.Fatal("display-only revision changed numeric stability or lost the latest display receipt", heads, err)
	}
}

func TestPrometheusCoverageMessagesAreBoundedAndPreserveTruncation(t *testing.T) {
	prior := monitorprovider.Coverage{Points: 2, ExpectedPoints: 3, MissingPoints: 1, Truncated: true, Freshness: "stale", Warnings: []string{strings.Repeat("x", 1000)}}
	next := monitorprovider.Coverage{Status: "complete", Points: 3, ExpectedPoints: 3}
	for i := range 20 {
		next.Warnings = append(next.Warnings, fmt.Sprint(i))
	}
	merged := mergePrometheusChunkCoverage(prior, next)
	if merged.Status != "partial" || merged.Points != 5 || merged.ExpectedPoints != 6 || merged.MissingPoints != 1 || merged.Freshness != "stale" || len(merged.Warnings) != 8 || len(merged.Warnings[0]) != 512 {
		t.Fatalf("unbounded coverage %+v", merged)
	}
}

func TestPrometheusChunkWarningsSurviveCleanFinalSegment(t *testing.T) {
	for _, tc := range []struct {
		name     string
		warnings []string
		want     string
	}{
		{"unfinished segment", nil, "complete"},
		{"upstream warning", []string{"upstream omitted a shard"}, "partial"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prior := monitorprovider.Coverage{Status: "partial", Points: 2, ExpectedPoints: 2, Warnings: tc.warnings}
			next := monitorprovider.Coverage{Status: "complete", Points: 3, ExpectedPoints: 3}
			merged := mergePrometheusChunkCoverage(prior, next)
			if merged.Status != tc.want || merged.Truncated || merged.Points != 5 || merged.ExpectedPoints != 5 || len(merged.Warnings) != len(tc.warnings) {
				t.Fatalf("final segment changed upstream completeness: %+v", merged)
			}
		})
	}
}

func TestPrometheusSuccessfulEmptyWindowAdvancesQueryWithoutClosingCoverage(t *testing.T) {
	s, source, start := prometheusMonitorFixture(t)
	jobs, err := s.StartRunMonitors(source.ProjectID, "monitor-run", "monitor-scenario", start, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	end := start.Add(time.Minute)
	record, _ := s.GetTest("monitor-run")
	record.Status = "COMPLETE"
	record.EndTime = &end
	if _, err = s.SaveTestWithRunMonitors(record, "", "", true); err != nil {
		t.Fatal(err)
	}
	batch := ProviderMonitorBatch{QueryFromUTC: start, QueryUntilUTC: end, QueryWindowEnd: &end, Final: true, Progress: &monitorprovider.QueryProgress{TotalQueries: 1, NextQuery: 1, Complete: true}, Coverage: map[string]monitorprovider.Coverage{"cpu": {Status: "no_data", From: start, To: end, ExpectedPoints: 5, MissingPoints: 5}}}
	if err = s.AcceptProviderMonitorBatch(source.ProjectID, "", providerReceipt(t, jobs[0].ID, 0, batch, end.Add(180*time.Second)), batch, "", "", 0); err != nil {
		t.Fatal(err)
	}
	job, _ := s.GetRunMonitorJob(source.ProjectID, jobs[0].ID)
	if !job.QueryWatermark.Equal(end) || job.LastSuccessAt != nil || job.State != "reconciling" || job.StablePasses != 0 || job.Coverage["cpu"].Points != 0 {
		t.Fatalf("empty successful query fabricated evidence %+v", job)
	}
}

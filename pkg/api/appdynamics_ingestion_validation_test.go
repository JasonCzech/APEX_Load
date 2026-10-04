package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/appdynamics"
	"github.com/jasonczech/apexload/pkg/storage"
)

func appDynamicsValidationFixture() (storage.RunMonitorJob, storage.ProviderMonitorBatch, time.Time) {
	now := time.Now().UTC()
	start := now.Add(-10 * time.Minute).Truncate(time.Minute)
	end := start.Add(5 * time.Minute)
	value := 10.0
	selection := &appdynamics.Selection{ProjectID: "p", Applications: []string{"1"}, ScopeIDs: map[string][]string{"node": {"7"}}, Events: true, Violations: true, Snapshots: true, Metrics: []appdynamics.MetricSelection{{ApplicationID: "1", Name: "cpu", Path: "Application Infrastructure Performance|*|Individual Nodes|*|Hardware Resources|CPU|%Busy", Statistic: "value", Unit: "percent"}}}
	job := storage.RunMonitorJob{ID: "run:source", ProjectID: "p", RunID: "run", StartedAt: start, LoadEndedAt: &end, State: "reconciling", Source: storage.MonitorSource{ID: "source", ProjectID: "p", Pack: "appdynamics", ExecutorMode: "direct", AppDynamics: selection}, ConnectionSnapshot: &storage.AppDynamicsConnectionSnapshot{Config: appdynamics.Config{Endpoint: "https://controller.example", AccountName: "account"}}}
	p := analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: "p", RunID: "run", Source: "source", MetricName: "cpu", Unit: "percent", Kind: analysis.KindGauge, TimestampUTC: start, Quality: analysis.QualityValid, Value: &value, Dimensions: map[string]string{"password": "captured private value"}, Provider: &analysis.ProviderMeasurement{Name: "appdynamics", MetricID: "2231", CatalogPath: "Application Infrastructure Performance|Services|Individual Nodes|Node A|Hardware Resources|CPU|%Busy", EntityIDs: map[string]string{"application": "1", "tier": "9", "node": "7"}, BucketStart: start, BucketEnd: start.Add(time.Minute), ResolutionSeconds: 60, Statistic: "value", Settled: false, AggregationEligible: true}}
	return job, storage.ProviderMonitorBatch{Points: []analysis.MetricPoint{p}, QueryFromUTC: start, QueryUntilUTC: end}, now
}

func TestAppDynamicsProviderRetryKeepsImmutableProjection(t *testing.T) {
	job, batch, now := appDynamicsValidationFixture()
	batch.Progress = &appdynamics.QueryProgress{NextQuery: 1, TotalQueries: 4}
	if err := validateAppDynamicsProviderBatch(job, &batch, now); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(batch)
	job.QueryOffset = 3
	job.QueryFromUTC = job.StartedAt.Add(time.Minute)
	job.QueryUntilUTC = batch.QueryUntilUTC
	if err := validateAppDynamicsProviderBatchCursor(job, &batch, now.Add(time.Hour), false); err != nil {
		t.Fatal("accepted receipt retry consulted a newer cursor", err)
	}
	after, _ := json.Marshal(batch)
	if string(before) != string(after) || batch.Points[0].Provider.Settled {
		t.Fatal("retry changed its content-addressed provider projection")
	}
	if validateAppDynamicsProviderBatch(job, &batch, now) == nil {
		t.Fatal("new receipt changed a frozen query window")
	}
}

func TestAppDynamicsProviderWindowAndPrematureSettlement(t *testing.T) {
	job, batch, now := appDynamicsValidationFixture()
	batch.QueryFromUTC = time.Time{}
	if validateAppDynamicsProviderBatch(job, &batch, now) == nil {
		t.Fatal("chunk without immutable window accepted")
	}
	job, batch, _ = appDynamicsValidationFixture()
	batch.QueryUntilUTC = job.StartedAt.Add(2 * time.Minute)
	batch.Points[0].Provider.Settled = true
	if validateAppDynamicsProviderBatch(job, &batch, job.StartedAt.Add(150*time.Second)) == nil {
		t.Fatal("premature settlement accepted")
	}
}

func TestAppDynamicsProviderRunAssociationUsesClassification(t *testing.T) {
	job, batch, now := appDynamicsValidationFixture()
	guid := "01234567-89ab-cdef-0123-456789abcdef"
	d := appdynamics.Diagnostic{ID: guid, Kind: "snapshot", ApplicationID: "1", TimestampUTC: job.StartedAt, EntityIDs: map[string]string{"application": "1", "tier": "9", "node": "7"}, Properties: map[string]string{"correlation": "run_header", "run_id": job.RunID}, DeepLink: "https://controller.example/controller/#location=APP_SNAPSHOT_VIEWER&application=1&requestGUID=" + guid}
	batch.Evidence = []appdynamics.Diagnostic{d}
	if validateAppDynamicsProviderBatch(job, &batch, now) == nil {
		t.Fatal("unconfigured run-header association accepted")
	}
	job.Source.AppDynamics.RunHeader = true
	job.Source.AppDynamics.HeaderOrigins = []string{"https://service.example"}
	if err := validateAppDynamicsProviderBatch(job, &batch, now); err != nil || batch.Evidence[0].DeepLink == "" || batch.Evidence[0].Properties["requestguid"] != guid {
		t.Fatal(batch, err)
	}
	batch.Evidence[0].Properties["correlation"] = guid
	if validateAppDynamicsProviderBatch(job, &batch, now) == nil {
		t.Fatal("request GUID was treated as a correlation classification")
	}
}
func TestAppDynamicsProviderValidationRejectsUnselectedMeasurements(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*analysis.MetricPoint)
	}{
		{"application", func(p *analysis.MetricPoint) { p.Provider.EntityIDs["application"] = "2" }},
		{"node", func(p *analysis.MetricPoint) { p.Provider.EntityIDs["node"] = "8" }},
		{"unknown entity", func(p *analysis.MetricPoint) { p.Provider.EntityIDs["password"] = "1" }},
		{"catalog", func(p *analysis.MetricPoint) {
			p.Provider.CatalogPath = "Overall Application Performance|Calls per Minute"
		}},
		{"name", func(p *analysis.MetricPoint) { p.MetricName = "unexpected" }},
		{"unit", func(p *analysis.MetricPoint) { p.Unit = "ms" }},
		{"statistic", func(p *analysis.MetricPoint) { p.Provider.Statistic = "p99" }},
		{"resolution", func(p *analysis.MetricPoint) {
			p.Provider.ResolutionSeconds = 1
			p.Provider.BucketEnd = p.TimestampUTC.Add(time.Second)
		}},
		{"past window", func(p *analysis.MetricPoint) {
			p.TimestampUTC = p.TimestampUTC.Add(-2 * time.Minute)
			p.Provider.BucketStart = p.TimestampUTC
			p.Provider.BucketEnd = p.TimestampUTC.Add(time.Minute)
		}},
		{"future window", func(p *analysis.MetricPoint) {
			p.TimestampUTC = p.TimestampUTC.Add(6 * time.Minute)
			p.Provider.BucketStart = p.TimestampUTC
			p.Provider.BucketEnd = p.TimestampUTC.Add(time.Minute)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job, batch, now := appDynamicsValidationFixture()
			tc.change(&batch.Points[0])
			if validateAppDynamicsProviderBatch(job, &batch, now) == nil {
				t.Fatal("unselected measurement accepted")
			}
		})
	}
	job, batch, now := appDynamicsValidationFixture()
	if err := validateAppDynamicsProviderBatch(job, &batch, now); err != nil {
		t.Fatal(err)
	}
	p := batch.Points[0]
	if p.Dimensions["password"] != "" || p.Dimensions["selection"] != "cpu" || p.Provider.Settled || !p.Provider.AggregationEligible {
		t.Fatal(p)
	}
}
func TestAppDynamicsProviderBoundaryAndQueryCursorValidation(t *testing.T) {
	job, batch, now := appDynamicsValidationFixture()
	p := &batch.Points[0]
	p.TimestampUTC = p.TimestampUTC.Add(-30 * time.Second)
	p.Provider.BucketStart = p.TimestampUTC
	p.Provider.BucketEnd = p.TimestampUTC.Add(time.Minute)
	if err := validateAppDynamicsProviderBatch(job, &batch, now); err != nil || p.Provider.AggregationEligible {
		t.Fatal(batch, err)
	}
	job, batch, now = appDynamicsValidationFixture()
	end := *job.LoadEndedAt
	batch.QueryFromUTC = job.StartedAt
	batch.QueryUntilUTC = end
	batch.Progress = &appdynamics.QueryProgress{NextQuery: 4, TotalQueries: 4, Complete: true}
	batch.QueryWindowEnd = &end
	batch.Coverage = map[string]appdynamics.Coverage{"metrics:0": {Status: "complete", From: job.StartedAt, To: end}}
	if err := validateAppDynamicsProviderBatch(job, &batch, now); err != nil {
		t.Fatal(err)
	}
	batch.Progress.TotalQueries = 5
	if validateAppDynamicsProviderBatch(job, &batch, now) == nil {
		t.Fatal("forged query count accepted")
	}
	batch.Progress.TotalQueries = 4
	batch.Coverage["metrics:99"] = appdynamics.Coverage{Status: "complete", From: job.StartedAt, To: end}
	if validateAppDynamicsProviderBatch(job, &batch, now) == nil {
		t.Fatal("unapproved coverage query accepted")
	}
}
func TestAppDynamicsProviderDiagnosticAndTopologyProjection(t *testing.T) {
	job, batch, now := appDynamicsValidationFixture()
	ids := map[string]string{"application": "1", "node": "7", "tier": "9"}
	d := appdynamics.Diagnostic{ID: "42", Kind: "event", ApplicationID: "1", TimestampUTC: job.StartedAt, EntityIDs: ids, DeepLink: "https://controller.example/controller/#location=APP_EVENT_VIEWER_MODAL&eventSummary=42"}
	batch.Evidence = []appdynamics.Diagnostic{d}
	batch.Topology = []appdynamics.Entity{{ID: "7", Name: "Node A", Type: "node", ParentID: "9", EntityIDs: ids}}
	if err := validateAppDynamicsProviderBatch(job, &batch, now); err != nil || batch.Evidence[0].DeepLink == "" {
		t.Fatal(batch, err)
	}
	for _, link := range []string{"https://controller.example/controller/?token=secret#location=APP_EVENT_VIEWER_MODAL&eventSummary=42", "https://controller.example/other/#location=APP_EVENT_VIEWER_MODAL&eventSummary=42", "https://controller.example/controller/#location=APP_EVENT_VIEWER_MODAL&eventSummary=42&token=secret", "https://evil.example/controller/#location=APP_EVENT_VIEWER_MODAL&eventSummary=42"} {
		d.DeepLink = link
		if got := appDynamicsSafeDiagnosticLink(job.ConnectionSnapshot.Config, d); got != "" {
			t.Fatal(got)
		}
	}
	batch.Topology[0].EntityIDs = map[string]string{"application": "1", "node": "8", "tier": "9"}
	batch.Topology[0].ID = "8"
	if validateAppDynamicsProviderBatch(job, &batch, now) == nil {
		t.Fatal("unselected topology accepted")
	}
}
func TestAppDynamicsProviderAnalyticsQueryIdentity(t *testing.T) {
	job, _, now := appDynamicsValidationFixture()
	job.Source.AppDynamics.ScopeIDs = nil
	q := appdynamics.AnalyticsQuery{Name: "latency", ApplicationID: "1", ApplicationIDField: "applicationId", Kind: "metric", Unit: "ms", Statistic: "average", Query: "SELECT avg(responseTime) AS value, count(*) AS count, sum(responseTime) AS sum FROM transactions"}
	job.Source.AppDynamics.Analytics = []appdynamics.AnalyticsQuery{q}
	value, count, sum := 5.0, 2.0, 10.0
	p := analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: "p", RunID: "run", Source: "source", MetricName: q.Name, Unit: q.Unit, Kind: analysis.KindGauge, TimestampUTC: job.StartedAt, Quality: analysis.QualityValid, Value: &value, Provider: &analysis.ProviderMeasurement{Name: "appdynamics", MetricID: appdynamics.AnalyticsMetricID(q), EntityIDs: map[string]string{"application": "1"}, BucketStart: job.StartedAt, BucketEnd: *job.LoadEndedAt, ResolutionSeconds: 300, Statistic: "average", Count: &count, Sum: &sum, Settled: true, AggregationEligible: true, QueryWindow: true}}
	batch := storage.ProviderMonitorBatch{Points: []analysis.MetricPoint{p}, QueryFromUTC: job.StartedAt, QueryUntilUTC: *job.LoadEndedAt}
	if err := validateAppDynamicsProviderBatch(job, &batch, now); err != nil {
		t.Fatal(err)
	}
	batch.Points[0].Provider.QueryWindow = false
	if validateAppDynamicsProviderBatch(job, &batch, now) == nil {
		t.Fatal("whole-query aggregate was relabeled as an independent bucket")
	}
	batch.Points[0].Provider.QueryWindow = true
	batch.Points[0].Provider.BucketEnd = job.StartedAt.Add(time.Minute)
	batch.Points[0].Provider.ResolutionSeconds = 60
	if validateAppDynamicsProviderBatch(job, &batch, now) == nil {
		t.Fatal("whole-query aggregate claimed a narrower interval")
	}
	batch.Points[0].Provider.BucketEnd = *job.LoadEndedAt
	batch.Points[0].Provider.ResolutionSeconds = 300
	batch.Points[0].Provider.MetricID = "0123456789abcdef0123456789abcdef"
	if validateAppDynamicsProviderBatch(job, &batch, now) == nil {
		t.Fatal("unapproved Analytics query accepted")
	}
}

func TestAppDynamicsProviderAnalyticsCannotForgeAggregationEligibility(t *testing.T) {
	job, _, now := appDynamicsValidationFixture()
	job.Source.AppDynamics.ScopeIDs = nil
	q := appdynamics.AnalyticsQuery{Name: "latency", ApplicationID: "1", ApplicationIDField: "applicationId", Kind: "metric", Unit: "ms", Statistic: "value", Query: "SELECT percentile(responseTime,95) AS value FROM transactions"}
	job.Source.AppDynamics.Analytics = []appdynamics.AnalyticsQuery{q}
	value, count, sum := 5.0, 2.0, 10.0
	p := analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: "p", RunID: "run", Source: "source", MetricName: q.Name, Unit: q.Unit, Kind: analysis.KindGauge, TimestampUTC: job.StartedAt, Quality: analysis.QualityValid, Value: &value, Provider: &analysis.ProviderMeasurement{Name: "appdynamics", MetricID: appdynamics.AnalyticsMetricID(q), EntityIDs: map[string]string{"application": "1"}, BucketStart: job.StartedAt, BucketEnd: *job.LoadEndedAt, ResolutionSeconds: 300, Statistic: "value", Count: &count, Sum: &sum, Settled: true, AggregationEligible: true, QueryWindow: true}}
	batch := storage.ProviderMonitorBatch{Points: []analysis.MetricPoint{p}, QueryFromUTC: job.StartedAt, QueryUntilUTC: *job.LoadEndedAt}
	if err := validateAppDynamicsProviderBatch(job, &batch, now); err != nil || batch.Points[0].Provider.AggregationEligible {
		t.Fatal("collector made a percentile aggregate eligible", batch, err)
	}
	q.Query, q.Statistic = "SELECT avg(responseTime) AS value FROM transactions", "average"
	job.Source.AppDynamics.Analytics[0] = q
	batch.Points[0].Provider.MetricID = appdynamics.AnalyticsMetricID(q)
	batch.Points[0].Provider.Statistic = "average"
	batch.Points[0].Provider.AggregationEligible = true
	if err := validateAppDynamicsProviderBatch(job, &batch, now); err != nil || batch.Points[0].Provider.AggregationEligible {
		t.Fatal("collector forged undeclared average weights", batch, err)
	}
}

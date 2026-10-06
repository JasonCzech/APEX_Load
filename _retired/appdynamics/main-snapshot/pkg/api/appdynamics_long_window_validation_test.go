package api

import (
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/appdynamics"
	"github.com/jasonczech/apexload/pkg/storage"
)

func appDynamicsLongWindowFixture() (storage.RunMonitorJob, storage.ProviderMonitorBatch, time.Time) {
	job, batch, now := appDynamicsValidationFixture()
	from := now.Add(-27 * time.Hour).Truncate(time.Second)
	to := from.Add(26 * time.Hour)
	job.StartedAt, job.LoadEndedAt = from, &to
	batch.QueryFromUTC, batch.QueryUntilUTC = from, to
	batch.Points[0].TimestampUTC = from
	batch.Points[0].Provider.BucketStart = from
	batch.Points[0].Provider.BucketEnd = from.Add(time.Minute)
	return job, batch, now
}

func TestAppDynamicsProviderMultiDayImmutableRunWindow(t *testing.T) {
	job, batch, now := appDynamicsLongWindowFixture()
	if err := validateAppDynamicsProviderBatch(job, &batch, now); err != nil {
		t.Fatal("26-hour reconciliation was rejected", err)
	}
	for _, tc := range []struct {
		name   string
		change func(*storage.RunMonitorJob, *storage.ProviderMonitorBatch)
	}{
		{"before run start", func(_ *storage.RunMonitorJob, b *storage.ProviderMonitorBatch) {
			b.QueryFromUTC = b.QueryFromUTC.Add(-time.Millisecond)
		}},
		{"after load end", func(_ *storage.RunMonitorJob, b *storage.ProviderMonitorBatch) {
			b.QueryUntilUTC = b.QueryUntilUTC.Add(time.Millisecond)
		}},
		{"future active window", func(j *storage.RunMonitorJob, b *storage.ProviderMonitorBatch) {
			j.LoadEndedAt = nil
			b.QueryUntilUTC = now.Add(2 * time.Minute)
		}},
		{"inverted", func(_ *storage.RunMonitorJob, b *storage.ProviderMonitorBatch) {
			b.QueryUntilUTC = b.QueryFromUTC.Add(-time.Second)
		}},
		{"source resolution exceeds day", func(_ *storage.RunMonitorJob, b *storage.ProviderMonitorBatch) {
			b.Points[0].Provider.ResolutionSeconds = 26 * 3600
			b.Points[0].Provider.BucketEnd = b.QueryUntilUTC
		}},
		{"duration saturation", func(j *storage.RunMonitorJob, b *storage.ProviderMonitorBatch) {
			from := time.Date(1600, 1, 1, 0, 0, 0, 0, time.UTC)
			to := from.AddDate(400, 0, 0)
			j.StartedAt, j.LoadEndedAt = from, &to
			b.QueryFromUTC, b.QueryUntilUTC, b.Points = from, to, nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job, batch, now := appDynamicsLongWindowFixture()
			tc.change(&job, &batch)
			if validateAppDynamicsProviderBatch(job, &batch, now) == nil {
				t.Fatal("collection escaped immutable run bounds or source-resolution semantics")
			}
		})
	}
}

func TestAppDynamicsProviderMultiDayAnalyticsAggregate(t *testing.T) {
	job, _, now := appDynamicsLongWindowFixture()
	q := appdynamics.AnalyticsQuery{Name: "transactions", ApplicationID: "1", ApplicationIDField: "applicationId", Kind: "metric", Query: "SELECT count(*) AS value FROM transactions"}
	job.Source.AppDynamics.ScopeIDs, job.Source.AppDynamics.Metrics = nil, nil
	job.Source.AppDynamics.Events, job.Source.AppDynamics.Violations, job.Source.AppDynamics.Snapshots = false, false, false
	job.Source.AppDynamics.Analytics = []appdynamics.AnalyticsQuery{q}
	value := 5.0
	p := analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: "p", RunID: "run", Source: "source", MetricName: q.Name, Unit: "count", Kind: analysis.KindGauge, TimestampUTC: job.StartedAt, Quality: analysis.QualityValid, Value: &value, Provider: &analysis.ProviderMeasurement{Name: "appdynamics", MetricID: appdynamics.AnalyticsMetricID(q), EntityIDs: map[string]string{"application": "1"}, BucketStart: job.StartedAt, BucketEnd: *job.LoadEndedAt, ResolutionSeconds: 26 * 3600, Statistic: "value", Settled: true, AggregationEligible: true, QueryWindow: true}}
	batch := storage.ProviderMonitorBatch{Points: []analysis.MetricPoint{p}, QueryFromUTC: job.StartedAt, QueryUntilUTC: *job.LoadEndedAt}
	if err := validateAppDynamicsProviderBatch(job, &batch, now); err != nil || !batch.Points[0].Provider.AggregationEligible {
		t.Fatal("multi-day aggregate failed immutable full-window validation", batch, err)
	}
	batch.Points[0].Provider.BucketEnd = batch.Points[0].Provider.BucketEnd.Add(-time.Second)
	batch.Points[0].Provider.ResolutionSeconds--
	if validateAppDynamicsProviderBatch(job, &batch, now) == nil {
		t.Fatal("multi-day aggregate narrowed its immutable query interval")
	}
}

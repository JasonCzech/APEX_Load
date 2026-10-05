package storage

import (
	"fmt"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func TestDynatraceResolutionChangesDoNotConsumeExpandedSeriesBudget(t *testing.T) {
	s, source, start := dynatraceMonitorFixture(t)
	jobs, err := s.StartRunMonitors(source.ProjectID, "monitor-run", "monitor-scenario", start, start.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	until := start.Add(5 * time.Minute)
	points := make([]analysis.MetricPoint, 200)
	for i := range points {
		points[i] = dynatracePoint(source, start, 1)
		points[i].Dimensions = map[string]string{"instance": fmt.Sprint(i)}
	}
	acceptDynatraceFixture(t, s, source, 0, ProviderMonitorBatch{Points: points, QueryFromUTC: start, QueryUntilUTC: until}, until)
	coarser := dynatracePoint(source, start, 2)
	coarser.Dimensions = map[string]string{"instance": "0"}
	coarser.Provider.ResolutionSeconds = 300
	coarser.Provider.BucketEnd = until
	if providerSeriesKey(coarser) != providerSeriesKey(points[0]) || ObservationKey(coarser) == ObservationKey(points[0]) {
		t.Fatal("returned resolution must distinguish buckets without creating another series")
	}
	newSeries := dynatracePoint(source, start, 3)
	newSeries.Dimensions = map[string]string{"instance": "201"}
	acceptDynatraceFixture(t, s, source, 1, ProviderMonitorBatch{Points: []analysis.MetricPoint{coarser, newSeries}, QueryFromUTC: start, QueryUntilUTC: until}, until)
	var count, series int
	if err := s.db.QueryRow(`SELECT COUNT(*),COUNT(DISTINCT series_key) FROM run_monitor_observation_heads WHERE job_id=?`, jobs[0].ID).Scan(&count, &series); err != nil {
		t.Fatal(err)
	}
	if count != 201 || series != 200 {
		t.Fatalf("coarser bucket should be retained at the full series budget; heads=%d series=%d", count, series)
	}
	job, err := s.GetRunMonitorJob(source.ProjectID, jobs[0].ID)
	if err != nil || !job.Coverage["retention"].Truncated {
		t.Fatalf("new distinct series must report the exceeded budget: %+v %v", job, err)
	}
}

func TestDynatraceReconciliationStabilityIncludesMetricSemantics(t *testing.T) {
	cases := []struct {
		name   string
		change func(*analysis.MetricPoint)
	}{
		{"unit", func(p *analysis.MetricPoint) { p.Unit = "milliseconds" }},
		{"statistic", func(p *analysis.MetricPoint) { p.Provider.Statistic = "max" }},
		{"synthetic", func(p *analysis.MetricPoint) { p.Provider.Synthetic = true }},
		{"aggregation eligibility", func(p *analysis.MetricPoint) { p.Provider.AggregationEligible = false }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, source, start := dynatraceMonitorFixture(t)
			jobs, err := s.StartRunMonitors(source.ProjectID, "monitor-run", "monitor-scenario", start, start.Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			until := start.Add(2 * time.Minute)
			run, err := s.GetTest("monitor-run")
			if err != nil {
				t.Fatal(err)
			}
			run.Status, run.EndTime = "COMPLETE", &until
			if _, err = s.SaveTestWithRunMonitors(run, "", "", true); err != nil {
				t.Fatal(err)
			}
			batch := ProviderMonitorBatch{Points: []analysis.MetricPoint{dynatracePoint(source, start, 1)}, QueryFromUTC: start, QueryUntilUTC: until, QueryWindowEnd: &until, Final: true, Progress: &analysis.ProviderQueryProgress{NextQuery: 1, TotalQueries: 1, Complete: true}, Coverage: map[string]analysis.ProviderCoverage{"metrics:cpu": {Status: "complete", From: start, To: until}}}
			acceptDynatraceFixture(t, s, source, 0, batch, until.Add(3*time.Minute+time.Second))
			before, err := s.GetRunMonitorJob(source.ProjectID, jobs[0].ID)
			if err != nil || before.StablePasses != 1 {
				t.Fatal(before, err)
			}
			tc.change(&batch.Points[0])
			acceptDynatraceFixture(t, s, source, 1, batch, until.Add(4*time.Minute+time.Second))
			after, err := s.GetRunMonitorJob(source.ProjectID, jobs[0].ID)
			if err != nil || after.State != "reconciling" || after.StablePasses != 1 || after.FinalDigest == before.FinalDigest {
				t.Fatalf("changed metric semantics must restart stability: before=%+v after=%+v error=%v", before, after, err)
			}
		})
	}
}

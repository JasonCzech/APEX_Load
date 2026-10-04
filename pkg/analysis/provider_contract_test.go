package analysis

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestProviderIdentityCompatibility(t *testing.T) {
	base := rulePoint(time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC), 10, 2, 20)
	for _, tc := range []struct {
		name string
		edit func(*ProviderMeasurement)
	}{
		{"provider", func(p *ProviderMeasurement) { p.Name = "dynatrace" }},
		{"backend", func(p *ProviderMeasurement) { p.Backend = "grail" }},
		{"metric", func(p *ProviderMeasurement) { p.MetricID = "other" }},
		{"entity", func(p *ProviderMeasurement) { p.EntityIDs = map[string]string{"service": "other"} }},
		{"statistic", func(p *ProviderMeasurement) { p.Statistic = "time_average" }},
		{"resolution", func(p *ProviderMeasurement) { p.ResolutionSeconds = 120 }},
		{"synthetic", func(p *ProviderMeasurement) { p.Synthetic = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			other := base
			meta := *base.Provider
			other.Provider = &meta
			tc.edit(other.Provider)
			if sameMetric(base, other) {
				t.Fatal("incompatible provider series matched")
			}
		})
	}
	later := base
	meta := *base.Provider
	meta.BucketStart = meta.BucketStart.Add(time.Minute)
	meta.BucketEnd = meta.BucketEnd.Add(time.Minute)
	meta.Settled = false
	later.Provider = &meta
	if !sameMetric(base, later) {
		t.Fatal("bucket clocks or settlement changed metric identity")
	}
}
func TestDynatraceTerminalExactGaugeRule(t *testing.T) {
	start := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	p := rulePoint(start, 10, 2, 20)
	p.Provider.Name = "dynatrace"
	p.Provider.Backend = "classic"
	p.Provider.Statistic = "time_average"
	p.Provider.Count = nil
	p.Provider.Sum = nil
	rule := MonitorRule{Provider: "dynatrace", Backend: "classic", MetricID: p.Provider.MetricID, SourceStatistic: "time_average", ResolutionSeconds: 60, SourceID: p.Source, Metric: p.MetricName, Unit: p.Unit, EvaluationOperation: "time_average", Comparator: "lte", Threshold: 11}
	if r := EvaluateMonitorRule(rule, []MetricPoint{p}, start, start.Add(time.Minute)); r.Status != "pass" || r.Observed == nil || *r.Observed != 10 {
		t.Fatalf("gauge rule: %+v", r)
	}
	for _, tc := range []struct {
		name string
		edit func(*MonitorRule)
	}{{"backend", func(r *MonitorRule) { r.Backend = "grail" }}, {"source statistic", func(r *MonitorRule) { r.SourceStatistic = "average" }}, {"resolution", func(r *MonitorRule) { r.ResolutionSeconds = 120 }}, {"weighted request", func(r *MonitorRule) { r.EvaluationOperation = "weighted_average" }}} {
		t.Run(tc.name, func(t *testing.T) {
			r := rule
			tc.edit(&r)
			if got := EvaluateMonitorRule(r, []MetricPoint{p}, start, start.Add(time.Minute)); got.Status != "not_evaluated" {
				t.Fatalf("incompatible rule evaluated: %+v", got)
			}
		})
	}
}
func TestProviderProgressOmittedFieldsPreserveLegacyJSON(t *testing.T) {
	b, err := json.Marshal(ProviderQueryProgress{NextQuery: 1, TotalQueries: 1, Complete: true})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"next_query":1,"total_queries":1,"complete":true}` {
		t.Fatalf("legacy progress changed: %s", b)
	}
	b, _ = json.Marshal(ProviderDiagnostic{ID: "1", Kind: "event", ApplicationID: "4"})
	if strings.Contains(string(b), `"provider"`) || strings.Contains(string(b), `"backend"`) {
		t.Fatalf("optional provider fields changed legacy evidence: %s", b)
	}
}

func TestDynatraceRequestAverageUsesObservationWeightsAndNeverReconstructsPercentiles(t *testing.T) {
	start := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	first, second := rulePoint(start, 10, 2, 20), rulePoint(start.Add(time.Minute), 30, 8, 240)
	for _, p := range []*MetricPoint{&first, &second} {
		p.Provider.Name = "dynatrace"
		p.Provider.Backend = "grail"
	}
	rule := MonitorRule{Provider: "dynatrace", Backend: "grail", MetricID: first.Provider.MetricID, SourceStatistic: "average", SourceID: first.Source, Metric: first.MetricName, Unit: first.Unit, EvaluationOperation: "weighted_average", Comparator: "eq", Threshold: 26}
	if got := EvaluateMonitorRule(rule, []MetricPoint{first, second}, start, start.Add(2*time.Minute)); got.Status != "pass" || got.Observed == nil || *got.Observed != 26 {
		t.Fatal(got)
	}
	first.Provider.Count = nil
	if got := EvaluateMonitorRule(rule, []MetricPoint{first, second}, start, start.Add(2*time.Minute)); got.Status != "not_evaluated" {
		t.Fatal("missing observation weights were invented", got)
	}
	for _, p := range []*MetricPoint{&first, &second} {
		p.Provider.Statistic = "p95"
		p.Provider.AggregationEligible = true
	}
	rule.EvaluationOperation = "avg"
	rule.SourceStatistic = "p95"
	if got := EvaluateMonitorRule(rule, []MetricPoint{first, second}, start, start.Add(2*time.Minute)); got.Status != "not_evaluated" {
		t.Fatal("whole-run percentile reconstructed from buckets", got)
	}
}

func TestDynatraceTerminalCoverageUsesReturnedBucketOrigin(t *testing.T) {
	start := time.Date(2026, 10, 3, 0, 0, 30, 0, time.UTC)
	points := make([]MetricPoint, 3)
	for i := range points {
		points[i] = rulePoint(start.Add(time.Duration(i)*time.Minute), 10, 2, 20)
		points[i].Provider.Name = "dynatrace"
		points[i].Provider.Backend = "grail"
		points[i].Provider.Statistic = "time_average"
		points[i].Provider.Count, points[i].Provider.Sum = nil, nil
	}
	rule := MonitorRule{SourceID: points[0].Source, Metric: points[0].MetricName, Unit: points[0].Unit, Statistic: "time_average", Comparator: "lte", Threshold: 11}
	end := start.Add(3 * time.Minute)
	for _, subset := range [][]MetricPoint{points[:2], points[1:]} {
		got := EvaluateMonitorRule(rule, subset, start, end)
		if got.Status != "not_evaluated" || got.EligibleBuckets != 2 || got.CoveragePct < 66 || got.CoveragePct > 67 {
			t.Fatalf("missing source bucket incorrectly satisfies coverage: %+v", got)
		}
	}
	if got := EvaluateMonitorRule(rule, points, start, end); got.Status != "pass" || got.CoveragePct != 100 {
		t.Fatalf("complete offset source buckets must satisfy coverage: %+v", got)
	}
	points[2].Provider.Synthetic = true
	if got := EvaluateMonitorRule(rule, points, start, end); got.Status != "not_evaluated" || got.EligibleBuckets != 2 {
		t.Fatalf("synthetic bucket counted as measured acceptance evidence: %+v", got)
	}
	points[2].Provider.Synthetic = false

	// Partially overlapping load boundaries still exclude boundary buckets.
	if got := EvaluateMonitorRule(rule, points, start.Add(-15*time.Second), end.Add(15*time.Second)); got.Status != "pass" || got.CoveragePct != 100 {
		t.Fatalf("full eligible buckets on returned grid: %+v", got)
	}
	shifted := append([]MetricPoint(nil), points...)
	last := *shifted[2].Provider
	last.BucketStart, last.BucketEnd = last.BucketStart.Add(15*time.Second), last.BucketEnd.Add(15*time.Second)
	shifted[2].TimestampUTC, shifted[2].Provider = last.BucketStart, &last
	if got := EvaluateMonitorRule(rule, shifted, start, end.Add(15*time.Second)); got.Status != "not_evaluated" {
		t.Fatalf("different bucket grids were combined: %+v", got)
	}
}

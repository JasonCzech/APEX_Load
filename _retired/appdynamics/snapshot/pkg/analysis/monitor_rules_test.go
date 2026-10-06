package analysis

import (
	"math"
	"testing"
	"time"
)

func rulePoint(at time.Time, value, count, sum float64) MetricPoint {
	return MetricPoint{SchemaVersion: SchemaVersion, ProjectID: "p", RunID: "r", Source: "s", MetricName: "latency", Unit: "ms", TimestampUTC: at, Kind: KindGauge, Value: &value, Quality: QualityValid, Provider: &ProviderMeasurement{Name: "appdynamics", MetricID: "1", BucketStart: at, BucketEnd: at.Add(time.Minute), ResolutionSeconds: 60, Statistic: "average", Count: &count, Sum: &sum, Settled: true, AggregationEligible: true}}
}
func TestMonitoringRuleUsesSourceWeightsAndCoverage(t *testing.T) {
	start := time.Date(2026, 10, 3, 1, 0, 0, 0, time.UTC)
	points := []MetricPoint{rulePoint(start, 10, 1, 10), rulePoint(start.Add(time.Minute), 100, 9, 900)}
	rule := MonitorRule{SourceID: "s", Metric: "latency", Unit: "ms", Statistic: "avg", Comparator: "lt", Threshold: 80}
	result := EvaluateMonitorRule(rule, points, start, start.Add(2*time.Minute))
	if result.Status != "fail" || result.Observed == nil || *result.Observed != 91 || result.CoveragePct != 100 {
		t.Fatalf("weighted %+v", result)
	}
	rule.Severity = "warning"
	if got := EvaluateMonitorRule(rule, points, start, start.Add(2*time.Minute)); got.Status != "warning" {
		t.Fatal(got)
	}
	points[1].Provider.Settled = false
	if got := EvaluateMonitorRule(rule, points, start, start.Add(2*time.Minute)); got.Status != "not_evaluated" || got.CoveragePct != 50 {
		t.Fatal(got)
	}
	if got := EvaluateMonitorRule(rule, points, start.Add(time.Second), start.Add(59*time.Second)); got.Status != "not_evaluated" {
		t.Fatal(got)
	}
}
func TestProviderBucketAggregationPreservesSemantics(t *testing.T) {
	start := time.Date(2026, 10, 3, 1, 0, 0, 0, time.UTC)
	points := []MetricPoint{rulePoint(start, 10, 1, 10), rulePoint(start.Add(time.Minute), 100, 9, 900)}
	got, err := BucketSeries(points, start, start.Add(2*time.Minute), 120, "avg")
	if err != nil || len(got) != 1 || got[0].Value == nil || *got[0].Value != 91 || got[0].Provider != nil {
		t.Fatalf("weighted %+v %v", got, err)
	}
	if _, err := BucketSeries(points, start, start.Add(2*time.Minute), 120, "p95"); err == nil {
		t.Fatal("reconstructed percentile")
	}
	points[0].Provider.Sum = nil
	if _, err := BucketSeries(points, start, start.Add(2*time.Minute), 120, "avg"); err == nil {
		t.Fatal("unweighted average")
	}
}
func TestMonitorRuleValidation(t *testing.T) {
	r := MonitorRule{SourceID: "s", Metric: "m", Unit: "ms", Statistic: "avg", Comparator: "lt", Threshold: 10}
	if r.Validate() != nil {
		t.Fatal(r.Validate())
	}
	r.Threshold = math.NaN()
	if r.Validate() == nil {
		t.Fatal("NaN threshold")
	}
}

func TestMonitoringRuleUsesMatchingAnalyticsQueryWindow(t *testing.T) {
	start := time.Date(2026, 10, 3, 1, 0, 1, 250000000, time.UTC)
	end := start.Add(125 * time.Second)
	makeWindow := func(from, to time.Time, value float64) MetricPoint {
		p := rulePoint(from, value, 1, value)
		p.Provider.QueryWindow = true
		p.Provider.Statistic = "sum"
		p.Provider.BucketEnd = to
		p.Provider.ResolutionSeconds = int(to.Sub(from) / time.Second)
		return p
	}
	queryStart := start.Truncate(time.Second).Add(time.Second)
	queryEnd := end.Truncate(time.Second)
	early := makeWindow(queryStart, queryStart.Add(60*time.Second), 100)
	terminal := makeWindow(queryStart, queryEnd, 200)
	rule := MonitorRule{SourceID: "s", Metric: "latency", Unit: "ms", Statistic: "sum", Comparator: "eq", Threshold: 200}
	got := EvaluateMonitorRule(rule, []MetricPoint{early, terminal}, start, end)
	if got.Status != "pass" || got.Observed == nil || *got.Observed != 200 || got.EligibleBuckets != 1 || got.CoveragePct != 99.2 {
		t.Fatalf("terminal query window: %+v", got)
	}
	if got := EvaluateMonitorRule(rule, []MetricPoint{early}, start, end); got.Status != "not_evaluated" {
		t.Fatalf("early aggregate substituted for full run: %+v", got)
	}
	endOffset := 30
	rule.EndOffsetSeconds = &endOffset
	if got := EvaluateMonitorRule(rule, []MetricPoint{terminal}, start, end); got.Status != "not_evaluated" {
		t.Fatalf("whole-run aggregate substituted for partial rule: %+v", got)
	}
}

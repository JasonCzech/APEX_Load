package analysis

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestResolvedConcreteIndexScopeIsBoundedAndPrivate(t *testing.T) {
	valid := []string{"logs-2026", ".ds-logs-orders-2026.10.05-000001"}
	if err := ValidateResolvedIndices(valid); err != nil {
		t.Fatal(err)
	}
	for _, indices := range [][]string{{""}, {"logs-*"}, {"remote:logs"}, {"logs/other"}, {"logs,other"}, {"logs\nother"}, {"logs\u00a0other"}, {".security-7"}, {"logs", "logs"}, {strings.Repeat("x", 256)}} {
		if err := ValidateResolvedIndices(indices); err == nil {
			t.Fatalf("unsafe concrete scope accepted %+v", indices)
		}
	}
	indices := make([]string, 201)
	for i := range indices {
		indices[i] = "logs-" + strconv.Itoa(i)
	}
	if ValidateResolvedIndices(indices[:200]) != nil || ValidateResolvedIndices(indices) == nil {
		t.Fatal("resolved scope maximum changed")
	}
	from := time.Now().UTC()
	cursor := ProviderContinuation{QueryID: "logs", WindowFrom: from, WindowUntil: from.Add(time.Minute), PITID: "private", ResolvedIndices: valid}
	if err := cursor.Validate(); err != nil {
		t.Fatal(err)
	}
	cursor.ResolvedIndices = []string{".security"}
	if cursor.Validate() == nil {
		t.Fatal("continuation bypassed concrete scope validation")
	}
}

func TestProjectedProviderEvidenceRejectsAmbiguousAndNonfiniteValues(t *testing.T) {
	at := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	good := ProviderEvidence{ID: "document", Kind: "error", Provider: "elasticsearch", TimestampUTC: at, QueryID: "logs", Index: "logs-a", DocumentID: "1", Log: &ProviderLog{Message: "failed", Attributes: map[string]ScalarValue{"count": {Kind: "integer", StringValue: "9223372036854775807"}}}}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, edit := range []struct {
		name string
		fn   func(*ProviderEvidence)
	}{
		{"mixed record", func(e *ProviderEvidence) { e.Transaction = &ProviderTransaction{Name: "txn"} }},
		{"invalid provider", func(e *ProviderEvidence) { e.Provider = "arbitrary" }},
		{"oversized property", func(e *ProviderEvidence) {
			e.Log = &ProviderLog{Attributes: map[string]ScalarValue{"a": {Kind: "integer", StringValue: "9223372036854775808"}}}
		}},
		{"nonfinite", func(e *ProviderEvidence) {
			v := math.NaN()
			e.Log = &ProviderLog{Attributes: map[string]ScalarValue{"a": {Kind: "number", NumberValue: &v}}}
		}},
	} {
		t.Run(edit.name, func(t *testing.T) {
			e := good
			edit.fn(&e)
			if e.Validate() == nil {
				t.Fatal("invalid evidence accepted")
			}
		})
	}
}
func TestProviderWireCompatibilityAndInstantMeasurement(t *testing.T) {
	var old ProviderEvidence
	if err := json.Unmarshal([]byte(`{"id":"1","kind":"event","application_id":"42","timestamp_utc":"2026-10-05T00:00:00Z"}`), &old); err != nil || old.Validate() != nil {
		t.Fatalf("legacy: %+v %v", old, err)
	}
	at := old.TimestampUTC
	v := 2.0
	p := MetricPoint{SchemaVersion: SchemaVersion, ProjectID: "p", RunID: "r", Source: "health", TimestampUTC: at, MetricName: "nodes", Kind: KindGauge, Value: &v, Quality: QualityValid, Provider: &ProviderMeasurement{Name: "opensearch", MetricID: "nodes", WindowKind: "instant", BucketStart: at, BucketEnd: at}}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	p.Provider.AggregationEligible = true
	if p.Validate() == nil {
		t.Fatal("instant measurement accepted for aggregation")
	}
}
func TestExactQueryWindowValueRule(t *testing.T) {
	at := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	p := rulePoint(at, 7, 1, 7)
	p.Provider.Name = "elasticsearch"
	p.Provider.QueryWindow = true
	p.Provider.WindowKind = "query_window"
	p.Provider.Statistic = "value"
	rule := MonitorRule{SourceID: "s", Metric: "latency", Unit: "ms", Statistic: "value", Comparator: "eq", Threshold: 7}
	if got := EvaluateMonitorRule(rule, []MetricPoint{p}, at, at.Add(time.Minute)); got.Status != "pass" {
		t.Fatal(got)
	}
	if got := EvaluateMonitorRule(rule, []MetricPoint{p, p}, at, at.Add(time.Minute)); got.Status != "not_evaluated" {
		t.Fatal("duplicate terminal windows accepted")
	}
	p.Provider.Approximate = true
	p.Provider.AggregationEligible = false
	if got := EvaluateMonitorRule(rule, []MetricPoint{p}, at, at.Add(time.Minute)); got.Status != "pass" {
		t.Fatal("single-window estimate rejected")
	}
}

func TestElasticAnalysisRetainsProviderQuerySemantics(t *testing.T) {
	start := time.Now().UTC().Truncate(time.Minute)
	points := []MetricPoint{rulePoint(start, 10, 1, 10), rulePoint(start.Add(time.Minute), 100, 9, 900)}
	for i := range points {
		points[i].Provider.Name = "elasticsearch"
		points[i].Provider.QueryID = "approved-query-revision"
		points[i].Provider.WindowKind = "bucket"
	}
	derived, err := BucketSeries(points, start, start.Add(2*time.Minute), 120, "avg")
	if err != nil || len(derived) != 1 || derived[0].Provider == nil || derived[0].Validate() != nil {
		t.Fatalf("derived Elastic metric lost metadata %+v %v", derived, err)
	}
	meta := derived[0].Provider
	if meta.Name != "elasticsearch" || meta.QueryID != "approved-query-revision" || meta.Statistic != "average" || meta.DerivedAggregation != "avg" || meta.ResolutionSeconds != 120 || meta.AggregationEligible || meta.Count != nil {
		t.Fatalf("ambiguous derived source semantics %+v", meta)
	}
	if derived[0].Value == nil || *derived[0].Value != 91 {
		t.Fatal("derived average lost source weights")
	}
	for _, edit := range []func(*ProviderMeasurement){
		func(m *ProviderMeasurement) { m.Name = "opensearch" },
		func(m *ProviderMeasurement) { m.QueryID = "different-query" },
		func(m *ProviderMeasurement) { m.MetricID = "different-metric" },
		func(m *ProviderMeasurement) { m.Statistic = "sum" },
		func(m *ProviderMeasurement) { m.CatalogPath = "different/path" },
	} {
		copy := append([]MetricPoint(nil), points...)
		changed := *copy[1].Provider
		copy[1].Provider = &changed
		edit(&changed)
		if _, err = BucketSeries(copy, start, start.Add(2*time.Minute), 120, "avg"); err == nil {
			t.Fatalf("mixed provider query semantics accepted %+v", changed)
		}
	}
	// Processing state may settle between samples; it does not change a
	// measurement's identity for a qualified future baseline.
	copy := points[0]
	metaCopy := *copy.Provider
	copy.Provider = &metaCopy
	metaCopy.Settled = false
	if !sameMetric(copy, points[0]) {
		t.Fatal("settlement state changed source metric identity")
	}
}

func TestFractionalExactWindowRulePreservesBounds(t *testing.T) {
	at := time.Date(2026, 10, 5, 0, 0, 0, 123000000, time.UTC)
	end := at.Add(1525 * time.Millisecond)
	p := rulePoint(at, 7, 1, 7)
	p.Provider.Name = "elasticsearch"
	p.Provider.QueryWindow = true
	p.Provider.WindowKind = "query_window"
	p.Provider.BucketEnd = end
	p.Provider.ResolutionSeconds = 2
	p.Provider.Statistic = "percentile"
	p.Provider.Approximate = true
	p.Provider.AggregationEligible = false
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	rule := MonitorRule{SourceID: "s", Metric: "latency", Unit: "ms", Statistic: "value", Comparator: "eq", Threshold: 7}
	if got := EvaluateMonitorRule(rule, []MetricPoint{p}, at, end); got.Status != "pass" || !got.Approximate {
		t.Fatal(got)
	}
	p.Provider.BucketEnd = end.Add(-time.Millisecond)
	if got := EvaluateMonitorRule(rule, []MetricPoint{p}, at, end); got.Status != "not_evaluated" {
		t.Fatal("different exact interval accepted")
	}
	p.Provider.QueryWindow = false
	p.Provider.WindowKind = "bucket"
	if p.Validate() == nil {
		t.Fatal("fractional native bucket accepted")
	}
}

func TestElasticIntervalCountsSupportSumWithoutChangingAppDynamics(t *testing.T) {
	start := time.Now().UTC().Truncate(time.Minute)
	until := start.Add(2 * time.Minute)
	for _, product := range []string{"elasticsearch", "opensearch", "appdynamics"} {
		t.Run(product, func(t *testing.T) {
			points := []MetricPoint{rulePoint(start, 3, 3, 3), rulePoint(start.Add(time.Minute), 7, 7, 7)}
			for i := range points {
				points[i].MetricName, points[i].Unit = "calls", "count"
				points[i].Provider.Name = product
				points[i].Provider.QueryID = "interval-count"
				points[i].Provider.WindowKind = "bucket"
				points[i].Provider.Statistic = "count"
			}
			rule := MonitorRule{SourceID: "s", Metric: "calls", Unit: "count", Statistic: "sum", Comparator: "eq", Threshold: 10}
			result := EvaluateMonitorRule(rule, points, start, until)
			derived, err := BucketSeries(points, start, until, 120, "sum")
			if product == "appdynamics" {
				if result.Status != "not_evaluated" || err == nil {
					t.Fatal("AppDynamics count semantics changed", result, err)
				}
				return
			}
			if result.Status != "pass" || result.Observed == nil || *result.Observed != 10 || result.CoveragePct != 100 || err != nil || len(derived) != 1 || derived[0].Value == nil || *derived[0].Value != 10 {
				t.Fatalf("interval counts could not sum %+v %+v %v", result, derived, err)
			}
			window := points[0]
			meta := *window.Provider
			window.Provider = &meta
			meta.QueryWindow, meta.WindowKind, meta.ResolutionSeconds, meta.BucketEnd = true, "query_window", 120, until
			value := 10.0
			window.Value = &value
			if result = EvaluateMonitorRule(rule, []MetricPoint{window}, start, until); result.Status != "pass" || result.Observed == nil || *result.Observed != 10 {
				t.Fatal("exact-window count could not sum", result)
			}
			meta.WindowKind, meta.QueryWindow = "instant", false
			meta.ResolutionSeconds, meta.BucketEnd, meta.AggregationEligible = 0, meta.BucketStart, false
			if result = EvaluateMonitorRule(rule, []MetricPoint{window}, start, until); result.Status != "not_evaluated" {
				t.Fatal("instantaneous count accepted as interval sum", result)
			}
		})
	}
}

func TestTerminalValueRuleIgnoresRetainedNativePercentileBuckets(t *testing.T) {
	start := time.Now().UTC().Truncate(time.Minute)
	until := start.Add(2 * time.Minute)
	points := []MetricPoint{rulePoint(start, 200, 1, 200), rulePoint(start.Add(time.Minute), 300, 1, 300), rulePoint(start, 11, 1, 11)}
	for i := range points {
		points[i].Provider.Name = "elasticsearch"
		points[i].Provider.QueryID = "latency-percentile"
		points[i].Provider.WindowKind = "bucket"
		points[i].Provider.Statistic = "percentile"
		points[i].Provider.Approximate = true
		points[i].Provider.AggregationEligible = false
	}
	terminal := points[2].Provider
	terminal.QueryWindow, terminal.WindowKind, terminal.ResolutionSeconds, terminal.BucketEnd = true, "query_window", 120, until
	rule := MonitorRule{SourceID: "s", Metric: "latency", Unit: "ms", Statistic: "value", Comparator: "eq", Threshold: 11}
	result := EvaluateMonitorRule(rule, points, start, until)
	if result.Status != "pass" || result.Observed == nil || *result.Observed != 11 || result.EligibleBuckets != 1 || !result.Approximate {
		t.Fatalf("native buckets blocked exact terminal value %+v", result)
	}
	if result = EvaluateMonitorRule(rule, points[:2], start, until); result.Status != "not_evaluated" {
		t.Fatal("native percentile buckets fabricated terminal value", result)
	}
	rule.Statistic = "avg"
	if result = EvaluateMonitorRule(rule, points, start, until); result.Status != "not_evaluated" {
		t.Fatal("provider percentiles were averaged", result)
	}
}

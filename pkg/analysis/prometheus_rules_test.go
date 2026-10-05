package analysis

import (
	"strings"
	"testing"
	"time"
)

func TestPrometheusEvaluationGridRules(t *testing.T) {
	start := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	value := 0.0
	point := MetricPoint{SchemaVersion: SchemaVersion, ProjectID: "p", Source: "s", MetricName: "cpu", Kind: KindGauge, Value: &value, Unit: "percent", Quality: QualityValid, TimestampUTC: start, Provider: &ProviderMeasurement{Name: "prometheus", MetricID: "cpu", QueryID: "cpu", DefinitionHash: strings.Repeat("a", 64), BackendProfile: "prometheus", Mode: "evaluation_point", EvaluationStepSeconds: 15, Settled: true, AggregationEligible: true, Statistic: "value"}}
	if err := point.Validate(); err != nil {
		t.Fatal(err)
	}
	points := []MetricPoint{point, point, point}
	for i := range points {
		points[i].TimestampUTC = start.Add(time.Duration(i) * 15 * time.Second)
	}
	rule := MonitorRule{SourceID: "s", Metric: "cpu", QueryID: "cpu", Unit: "percent", Statistic: "avg", Comparator: "lte", Threshold: 0}
	if r := EvaluateMonitorRule(rule, points, start, start.Add(30*time.Second)); r.Status != "pass" || r.CoveragePct != 100 || r.Observed == nil || *r.Observed != 0 {
		t.Fatalf("zero grid: %+v", r)
	}
	if r := EvaluateMonitorRule(rule, points[:2], start, start.Add(30*time.Second)); r.Status != "not_evaluated" {
		t.Fatalf("missing grid: %+v", r)
	}
	rule.Statistic = "sum"
	if r := EvaluateMonitorRule(rule, points, start, start.Add(30*time.Second)); r.Status != "not_evaluated" {
		t.Fatalf("unsafe sum: %+v", r)
	}
	rule.Statistic = "avg"
	rule.RequireFreshness = true
	if r := EvaluateMonitorRule(rule, points, start, start.Add(30*time.Second)); r.Status != "not_evaluated" {
		t.Fatalf("unknown freshness: %+v", r)
	}
	bad := point
	metadata := *point.Provider
	bad.Provider = &metadata
	bad.Provider.BucketStart = start
	bad.Provider.BucketEnd = start.Add(15 * time.Second)
	if bad.Validate() == nil {
		t.Fatal("fake source bucket accepted")
	}
}

func TestPrometheusExactWindowRule(t *testing.T) {
	start := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	end := start.Add(time.Minute)
	value := 42.0
	p := MetricPoint{SchemaVersion: SchemaVersion, ProjectID: "p", Source: "s", MetricName: "requests", Kind: KindGauge, Value: &value, Unit: "count", Quality: QualityValid, TimestampUTC: end, Provider: &ProviderMeasurement{Name: "prometheus", MetricID: "q", QueryID: "q", DefinitionHash: strings.Repeat("b", 64), BackendProfile: "azure_managed", Mode: "query_window", QueryWindow: true, BucketStart: start, BucketEnd: end, EvaluationStepSeconds: 15, Settled: true, AggregationEligible: true, Statistic: "sum"}}
	rule := MonitorRule{SourceID: "s", Metric: "requests", QueryID: "q", Unit: "count", Statistic: "sum", Comparator: "gte", Threshold: 40, EvaluationMode: "exact_window"}
	if r := EvaluateMonitorRule(rule, []MetricPoint{p}, start, end); r.Status != "pass" {
		t.Fatalf("total: %+v", r)
	}
	if r := EvaluateMonitorRule(rule, []MetricPoint{p}, start, end.Add(time.Second)); r.Status != "not_evaluated" {
		t.Fatalf("wrong window: %+v", r)
	}
}

func TestPrometheusRulesFoldAzureDimensionsOnly(t *testing.T) {
	start := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, backend, status string
		dimensions            map[string]string
	}{
		{"azure original display case", "azure_managed", "pass", map[string]string{"Job": "CheckoutAPI"}},
		{"azure consistent folded aliases", "azure_managed", "pass", map[string]string{"Job": "CheckoutAPI", "job": "checkoutapi"}},
		{"azure conflicting folded aliases", "azure_managed", "not_evaluated", map[string]string{"Job": "CheckoutAPI", "job": "otherapi"}},
		{"upstream remains case sensitive", "prometheus", "not_evaluated", map[string]string{"Job": "CheckoutAPI"}},
		{"upstream exact canonical case", "prometheus", "pass", map[string]string{"job": "checkoutapi"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			points := make([]MetricPoint, 3)
			for index := range points {
				point := prometheusGridPoint(start.Add(time.Duration(index)*15*time.Second), 0)
				point.Dimensions = map[string]string{"job": "checkoutapi"}
				point.Provider.BackendProfile = tc.backend
				if tc.backend == "azure_managed" {
					point.Provider.DisplayLabels = map[string]string{"Job": "CheckoutAPI"}
				}
				points[index] = point
			}
			rule := MonitorRule{SourceID: "monitor", Metric: "cpu", QueryID: "cpu", Unit: "percent", Dimensions: tc.dimensions, Statistic: "avg", Comparator: "lte", Threshold: 0}
			result := EvaluateMonitorRule(rule, points, start, start.Add(30*time.Second))
			if result.Status != tc.status {
				t.Fatal(result)
			}
			if result.Rule.Dimensions["Job"] != tc.dimensions["Job"] {
				t.Fatal("rule provenance was folded")
			}
			if tc.name == "azure conflicting folded aliases" && !strings.Contains(result.Message, "conflicting") {
				t.Fatal("ambiguous Azure selection was not explained", result)
			}
		})
	}
	points := []MetricPoint{prometheusGridPoint(start, 0), prometheusGridPoint(start.Add(15*time.Second), 0)}
	for index := range points {
		points[index].Provider.BackendProfile = "azure_managed"
		points[index].Dimensions = map[string]string{"job": "checkoutapi", "namespace": "production"}
	}
	rule := MonitorRule{SourceID: "monitor", Metric: "cpu", QueryID: "cpu", Unit: "percent", Dimensions: map[string]string{"Job": "CheckoutAPI"}, Statistic: "avg", Comparator: "lte", Threshold: 0}
	if result := EvaluateMonitorRule(rule, points, start, start.Add(15*time.Second)); result.Status != "not_evaluated" {
		t.Fatal("partial label map silently selected a fuller Azure series", result)
	}
}

func TestPrometheusDisplayLabelsValidateAgainstCanonicalIdentity(t *testing.T) {
	start := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		profile string
		labels  map[string]string
		display map[string]string
		valid   bool
	}{
		{"original casing", "azure_managed", map[string]string{"job": "checkoutapi", "namespace": "production"}, map[string]string{"Job": "CheckoutAPI", "Namespace": "Production"}, true},
		{"canonical-only compatible", "azure_managed", map[string]string{"job": "checkoutapi"}, nil, true},
		{"folded key collision", "azure_managed", map[string]string{"job": "checkoutapi", "namespace": "production"}, map[string]string{"Job": "CheckoutAPI", "job": "checkoutapi"}, false},
		{"value mismatch", "azure_managed", map[string]string{"job": "checkoutapi"}, map[string]string{"Job": "OtherAPI"}, false},
		{"unknown identity", "azure_managed", map[string]string{"job": "checkoutapi"}, map[string]string{"Instance": "checkoutapi"}, false},
		{"partial display map", "azure_managed", map[string]string{"job": "checkoutapi", "namespace": "production"}, map[string]string{"Job": "CheckoutAPI"}, false},
		{"upstream display casing prohibited", "prometheus", map[string]string{"job": "checkoutapi"}, map[string]string{"Job": "CheckoutAPI"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			point := prometheusGridPoint(start, 0)
			point.Dimensions = tc.labels
			point.Provider.BackendProfile = tc.profile
			point.Provider.DisplayLabels = tc.display
			if err := point.Validate(); (err == nil) != tc.valid {
				t.Fatal("display validation", err)
			}
		})
	}
}

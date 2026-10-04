package analysis

import (
	"maps"
	"math"
	"strings"
	"testing"
	"time"
)

func prometheusGridPoint(at time.Time, value float64) MetricPoint {
	return MetricPoint{SchemaVersion: SchemaVersion, ProjectID: "p", RunID: "run", Source: "monitor", MetricName: "cpu", Unit: "percent", Kind: KindGauge, Value: &value, Quality: QualityValid, TimestampUTC: at, Dimensions: map[string]string{"job": "checkout"}, Provider: &ProviderMeasurement{Name: "prometheus", Mode: "evaluation_point", BackendProfile: "prometheus", QueryID: "cpu", DefinitionHash: strings.Repeat("a", 64), MetricID: "cpu", Statistic: "value", EvaluationStepSeconds: 15, EntityIDs: map[string]string{"series": strings.Repeat("b", 64)}, Settled: true, AggregationEligible: true}}
}

func TestPrometheusAnalysisResamplesGridAndRetainsIdentity(t *testing.T) {
	start := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	points := []MetricPoint{prometheusGridPoint(start, 0), prometheusGridPoint(start.Add(15*time.Second), 10), prometheusGridPoint(start.Add(30*time.Second), 20), prometheusGridPoint(start.Add(45*time.Second), 30)}
	for _, tc := range []struct {
		stat string
		want [2]float64
	}{{"avg", [2]float64{5, 25}}, {"min", [2]float64{0, 20}}, {"max", [2]float64{10, 30}}} {
		t.Run(tc.stat, func(t *testing.T) {
			got, err := BucketSeries(points, start, start.Add(time.Minute), 30, tc.stat)
			if err != nil || len(got) != 2 {
				t.Fatal(got, err)
			}
			for index, point := range got {
				if point.Value == nil || *point.Value != tc.want[index] || point.Validate() != nil || point.Quality != QualityValid {
					t.Fatal("incorrect resampling", point)
				}
				meta := point.Provider
				if meta == nil || meta.Mode != "analysis_bucket" || meta.AnalysisStatistic != tc.stat || meta.QueryID != "cpu" || meta.DefinitionHash != points[0].Provider.DefinitionHash || meta.Statistic != "value" || meta.EvaluationStepSeconds != 15 || meta.AggregationEligible || !meta.BucketStart.Equal(point.TimestampUTC) || !maps.Equal(meta.EntityIDs, points[0].Provider.EntityIDs) {
					t.Fatal("lost or fabricated source semantics", meta)
				}
			}
			if points[0].Provider.Mode != "evaluation_point" || points[0].Provider.AnalysisStatistic != "" || !points[0].Provider.BucketStart.IsZero() {
				t.Fatal("resampling mutated canonical input")
			}
		})
	}
}

func TestPrometheusAnalysisGapsRemainMissing(t *testing.T) {
	start := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	for _, quality := range []string{"absent", QualityStale, "unsettled", QualityMissing} {
		t.Run(quality, func(t *testing.T) {
			points := []MetricPoint{prometheusGridPoint(start, 0), prometheusGridPoint(start.Add(15*time.Second), 10), prometheusGridPoint(start.Add(30*time.Second), 20), prometheusGridPoint(start.Add(45*time.Second), 30)}
			switch quality {
			case "absent":
				points = append(points[:1], points[2:]...)
			case "unsettled":
				points[1].Provider.Settled = false
			case QualityMissing:
				points[1].Quality, points[1].Value = QualityMissing, nil
			default:
				points[1].Quality = quality
			}
			got, err := BucketSeries(points, start, start.Add(time.Minute), 30, "avg")
			if err != nil || len(got) != 2 || got[0].Quality != QualityMissing || got[0].Value != nil || got[1].Value == nil || *got[1].Value != 25 {
				t.Fatal("gap filled or valid interval lost", got, err)
			}
		})
	}
	// A shifted phase remains the declared grid, rather than inventing a sample
	// at the requested comparison boundary or extending values past the window.
	points := []MetricPoint{prometheusGridPoint(start.Add(5*time.Second), 1), prometheusGridPoint(start.Add(20*time.Second), 3), prometheusGridPoint(start.Add(35*time.Second), 7)}
	got, err := BucketSeries(points, start, start.Add(40*time.Second), 30, "avg")
	if err != nil || len(got) != 2 || got[1].Value == nil || *got[1].Value != 7 || !got[1].Provider.BucketEnd.Equal(start.Add(40*time.Second)) || got[1].Validate() != nil {
		t.Fatal("partial grid display window", got, err)
	}
}

func TestPrometheusAnalysisRejectsUnsafeReconstructionAndMixedIdentity(t *testing.T) {
	start := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	for _, statistic := range []string{"sum", "count", "rate", "p50", "p95", "p99"} {
		if _, err := BucketSeries([]MetricPoint{prometheusGridPoint(start, 1)}, start, start.Add(30*time.Second), 30, statistic); err == nil {
			t.Fatal("reconstructed", statistic)
		}
	}
	for _, mutate := range []func(*MetricPoint){
		func(p *MetricPoint) { p.Provider.QueryID = "different" },
		func(p *MetricPoint) { p.Provider.DefinitionHash = strings.Repeat("c", 64) },
		func(p *MetricPoint) { p.Provider.Statistic = "quantile" },
		func(p *MetricPoint) { p.Provider.EvaluationStepSeconds = 30 },
		func(p *MetricPoint) { p.Provider.EntityIDs["series"] = strings.Repeat("d", 64) },
		func(p *MetricPoint) { p.TimestampUTC = p.TimestampUTC.Add(time.Second) },
		func(p *MetricPoint) { p.TimestampUTC = start },
	} {
		points := []MetricPoint{prometheusGridPoint(start, 1), prometheusGridPoint(start.Add(15*time.Second), 2)}
		mutate(&points[1])
		if _, err := BucketSeries(points, start, start.Add(30*time.Second), 30, "avg"); err == nil {
			t.Fatal("mixed identity or invalid grid accepted", points)
		}
	}
	point := prometheusGridPoint(start, 1)
	if _, err := BucketSeries([]MetricPoint{point}, start, start.Add(30*time.Second), 5, "avg"); err == nil {
		t.Fatal("upsampled source evidence")
	}
	point.Provider.Mode, point.Provider.QueryWindow = "query_window", true
	point.Provider.BucketStart, point.Provider.BucketEnd = start, start.Add(30*time.Second)
	point.TimestampUTC = point.Provider.BucketEnd
	if _, err := BucketSeries([]MetricPoint{point}, start, start.Add(time.Minute), 30, "avg"); err == nil {
		t.Fatal("exact total spread into intervals")
	}
	point = prometheusGridPoint(start, 1)
	derived, err := BucketSeries([]MetricPoint{point}, start, start.Add(15*time.Second), 15, "avg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BucketSeries(derived, start, start.Add(15*time.Second), 15, "avg"); err == nil {
		t.Fatal("derived display grid rebucketed as evidence")
	}
	rule := MonitorRule{SourceID: "monitor", Metric: "cpu", QueryID: "cpu", Unit: "percent", Statistic: "avg", Comparator: "lte", Threshold: 5}
	if result := EvaluateMonitorRule(rule, derived, start, start.Add(15*time.Second)); result.Status != "not_evaluated" {
		t.Fatal("display projection satisfied monitoring acceptance", result)
	}
	derived[0].Provider.AggregationEligible = true
	if derived[0].Validate() == nil {
		t.Fatal("display projection claimed acceptance eligibility")
	}
}

func TestPrometheusOverlayRequiresCompatiblePreservedProvenance(t *testing.T) {
	start := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	for _, mutate := range []func(*MetricPoint){
		func(p *MetricPoint) { p.Provider.QueryID = "other" },
		func(p *MetricPoint) { p.Provider.DefinitionHash = strings.Repeat("c", 64) },
		func(p *MetricPoint) { p.Provider.BackendProfile = "azure_managed" },
		func(p *MetricPoint) { p.Provider.Statistic = "quantile" },
		func(p *MetricPoint) { p.Provider.EvaluationStepSeconds = 30 },
		func(p *MetricPoint) { p.Provider.EntityIDs["series"] = strings.Repeat("d", 64) },
		func(p *MetricPoint) { p.Dimensions["job"] = "different" },
		func(p *MetricPoint) { p.Provider = nil },
	} {
		left, right := prometheusGridPoint(start, 1), prometheusGridPoint(start, 2)
		right.RunID = "baseline"
		if !CompatibleOverlay(left, right) {
			t.Fatal("compatible baseline refused")
		}
		mutate(&right)
		if CompatibleOverlay(left, right) {
			t.Fatal("incompatible provider overlay accepted")
		}
	}
}

func TestPrometheusCorrelationPreservesProjectionAndRejectsMixedQueries(t *testing.T) {
	start := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	points := []MetricPoint{prometheusGridPoint(start, 1), prometheusGridPoint(start.Add(15*time.Second), 2), prometheusGridPoint(start.Add(30*time.Second), 4), prometheusGridPoint(start.Add(45*time.Second), 8)}
	derived, err := BucketSeries(points, start, start.Add(time.Minute), 15, "avg")
	if err != nil {
		t.Fatal(err)
	}
	result, err := LaggedCorrelation(derived, derived, start, start.Add(time.Minute), 15, 15)
	if err != nil || len(result) != 3 || result[1].Coefficient == nil || math.Abs(*result[1].Coefficient-1) > 1e-12 {
		t.Fatal("derived grid correlation failed", result, err)
	}
	if !derived[0].TimestampUTC.Equal(start) || !derived[0].Provider.BucketStart.Equal(start) || !derived[0].Provider.BucketEnd.Equal(start.Add(15*time.Second)) {
		t.Fatal("correlation shifted response provenance")
	}
	changed := append([]MetricPoint(nil), derived...)
	metadata := *changed[1].Provider
	metadata.DefinitionHash = strings.Repeat("c", 64)
	changed[1].Provider = &metadata
	if _, err := LaggedCorrelation(derived, changed, start, start.Add(time.Minute), 15, 0); err == nil {
		t.Fatal("mixed query identity entered correlation")
	}
}

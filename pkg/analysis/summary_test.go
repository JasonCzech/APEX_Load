package analysis

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

func summaryMapping(measures ...ParquetMeasure) ParquetMapping {
	return ParquetMapping{Version: 1, Timestamp: ParquetColumnPath{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: measures}
}

func summaryMeasure(column, name, statistic string) ParquetMeasure {
	return ParquetMeasure{Value: ParquetColumnPath{column}, Name: name, Kind: KindSummary, Unit: "ms", Statistic: statistic, WindowSeconds: 60}
}

func TestSummaryMeasureDeclarationsAreValidated(t *testing.T) {
	count := summaryMeasure("requests", "requests", "count")
	count.Unit = "count"
	averaged := summaryMeasure("avg", "latency", "avg")
	averaged.SampleCountColumn, averaged.SampleSumColumn = ParquetColumnPath{"n"}, ParquetColumnPath{"total"}
	valid := []ParquetMapping{
		summaryMapping(summaryMeasure("p95", "latency", "p95")),
		summaryMapping(averaged),
		summaryMapping(count),
		// One wide latency, three statistics: the query's aggregation selects.
		summaryMapping(summaryMeasure("p50", "latency", "p50"), summaryMeasure("p95", "latency", "p95"), summaryMeasure("p999", "latency", "p999")),
		// Legacy undeclared summaries stay readable as display-only.
		summaryMapping(ParquetMeasure{Value: ParquetColumnPath{"p95"}, Name: "latency", Kind: KindSummary, Unit: "ms"}),
	}
	for i, mapping := range valid {
		if err := mapping.ValidateDefinition(); err != nil {
			t.Fatalf("valid mapping %d refused: %v", i, err)
		}
	}
	gauge := ParquetMeasure{Value: ParquetColumnPath{"cpu"}, Name: "latency", Kind: KindGauge, Unit: "percent"}
	countInMilliseconds := summaryMeasure("requests", "requests", "count")
	invalid := map[string]ParquetMapping{
		"statistic on a gauge":      summaryMapping(ParquetMeasure{Value: ParquetColumnPath{"cpu"}, Name: "cpu", Kind: KindGauge, Unit: "percent", Statistic: "avg", WindowSeconds: 60}),
		"window without statistic":  summaryMapping(ParquetMeasure{Value: ParquetColumnPath{"p95"}, Name: "latency", Kind: KindSummary, Unit: "ms", WindowSeconds: 60}),
		"unknown statistic":         summaryMapping(summaryMeasure("p95", "latency", "p42")),
		"missing window":            summaryMapping(ParquetMeasure{Value: ParquetColumnPath{"p95"}, Name: "latency", Kind: KindSummary, Unit: "ms", Statistic: "p95"}),
		"window above one day":      summaryMapping(ParquetMeasure{Value: ParquetColumnPath{"p95"}, Name: "latency", Kind: KindSummary, Unit: "ms", Statistic: "p95", WindowSeconds: 86401}),
		"sum without count":         summaryMapping(ParquetMeasure{Value: ParquetColumnPath{"avg"}, Name: "latency", Kind: KindSummary, Unit: "ms", Statistic: "avg", WindowSeconds: 60, SampleSumColumn: ParquetColumnPath{"total"}}),
		"count column is the value": summaryMapping(ParquetMeasure{Value: ParquetColumnPath{"avg"}, Name: "latency", Kind: KindSummary, Unit: "ms", Statistic: "avg", WindowSeconds: 60, SampleCountColumn: ParquetColumnPath{"avg"}}),
		"count and sum share":       summaryMapping(ParquetMeasure{Value: ParquetColumnPath{"avg"}, Name: "latency", Kind: KindSummary, Unit: "ms", Statistic: "avg", WindowSeconds: 60, SampleCountColumn: ParquetColumnPath{"n"}, SampleSumColumn: ParquetColumnPath{"n"}}),
		"count in milliseconds":     summaryMapping(countInMilliseconds),
		"repeated statistic":        summaryMapping(summaryMeasure("p95", "latency", "p95"), summaryMeasure("p95b", "latency", "p95")),
		"summary shares a gauge":    summaryMapping(gauge, summaryMeasure("p95", "latency", "p95")),
		"gauge shares a summary":    summaryMapping(summaryMeasure("p95", "latency", "p95"), gauge),
		"legacy shares a declared":  summaryMapping(ParquetMeasure{Value: ParquetColumnPath{"p50"}, Name: "latency", Kind: KindSummary, Unit: "ms"}, summaryMeasure("p95", "latency", "p95")),
	}
	for name, mapping := range invalid {
		if err := mapping.ValidateDefinition(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	protected := summaryMapping(ParquetMeasure{Value: ParquetColumnPath{"avg"}, Name: "latency", Kind: KindSummary, Unit: "ms", Statistic: "avg", WindowSeconds: 60, SampleCountColumn: ParquetColumnPath{"password"}})
	if err := protected.ValidateDisclosure(); err == nil {
		t.Fatal("a protected column was admitted as a sample count")
	}
}

func TestSummaryDeclarationsRoundTripTheStrictDecoder(t *testing.T) {
	averaged := summaryMeasure("avg", "latency", "avg")
	averaged.SampleCountColumn, averaged.SampleSumColumn = ParquetColumnPath{"n"}, ParquetColumnPath{"total"}
	body, err := json.Marshal(summaryMapping(averaged))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeParquetMappingDefinition(body)
	if err != nil || decoded.Measures[0].Statistic != "avg" || decoded.Measures[0].WindowSeconds != 60 || !slices.Equal(decoded.Measures[0].SampleSumColumn, ParquetColumnPath{"total"}) {
		t.Fatalf("decoded %+v %v", decoded, err)
	}
	for _, bad := range []string{
		strings.Replace(string(body), `"window_seconds":60`, `"window_seconds":"60"`, 1),
		strings.Replace(string(body), `"statistic":"avg"`, `"statistic":"avg","quantile":0.95`, 1),
	} {
		if _, err := DecodeParquetMappingDefinition([]byte(bad)); err == nil {
			t.Fatalf("decoder accepted %s", bad)
		}
	}
	// Mappings without a declaration keep their exact encoding, so stored
	// partition manifests keep their mapping digests.
	legacy, _ := json.Marshal(summaryMapping(ParquetMeasure{Value: ParquetColumnPath{"p95"}, Name: "latency", Kind: KindSummary, Unit: "ms"}))
	for _, key := range []string{"statistic", "window_seconds", "sample_count_column", "sample_sum_column"} {
		if strings.Contains(string(legacy), key) {
			t.Fatalf("legacy encoding gained %s: %s", key, legacy)
		}
	}
}

func TestNewMappingsMustDeclareSummaries(t *testing.T) {
	legacy := summaryMapping(ParquetMeasure{Value: ParquetColumnPath{"p95"}, Name: "latency", Kind: KindSummary, Unit: "ms"})
	if err := legacy.RequireDeclaredSummaries(); err == nil {
		t.Fatal("new undeclared summary admitted")
	}
	if err := summaryMapping(summaryMeasure("p95", "latency", "p95")).RequireDeclaredSummaries(); err != nil {
		t.Fatal(err)
	}
	declared, isLegacy := summaryMapping(summaryMeasure("p50", "latency", "p50"), summaryMeasure("p95", "latency", "p95")).SummaryDeclarations("latency")
	if isLegacy || len(declared) != 2 || declared[1] != (SummaryDeclaration{Statistic: "p95", WindowSeconds: 60}) {
		t.Fatalf("declarations %+v %v", declared, isLegacy)
	}
	if _, isLegacy := legacy.SummaryDeclarations("latency"); !isLegacy {
		t.Fatal("legacy summary not reported")
	}
}

func TestSummaryAggregationRequiresTheDeclaredStatisticAndWholeWindows(t *testing.T) {
	if err := ValidateSummaryAggregation("", 60, 60, "p95"); !errors.Is(err, ErrSummaryStatisticUnknown) {
		t.Fatalf("legacy summary: %v", err)
	}
	for _, c := range []struct {
		statistic, aggregation string
		window, bucket         int
		ok                     bool
	}{
		{"p95", "p95", 60, 60, true},
		{"p95", "p95", 60, 300, true},
		{"p95", "avg", 60, 60, false}, // a percentile is never averaged
		{"avg", "p95", 60, 60, false}, // nor derived from averages
		{"sum", "sum", 60, 30, false}, // narrower than the source window
		{"sum", "sum", 60, 90, false}, // windows would straddle buckets
		{"count", "count", 60, 120, true},
	} {
		err := ValidateSummaryAggregation(c.statistic, c.window, c.bucket, c.aggregation)
		if (err == nil) != c.ok {
			t.Errorf("%+v: %v", c, err)
		}
	}
	lineage, err := SummaryBucketDerivation("p999", 60, 120)
	if err != nil || len(lineage) != 2 || lineage[0] != (DerivationStep{Operation: "p999", Algorithm: "source_summary.v1", BucketSeconds: 60}) || lineage[1].Algorithm != "summary_single_window.v1" {
		t.Fatalf("lineage %+v %v", lineage, err)
	}
	if statistic, window, ok := SummaryWindowFromDerivation(lineage); !ok || statistic != "p999" || window != 60 {
		t.Fatal("source window not recoverable from lineage")
	}
	scalar, _ := ScalarBucketDerivation(MetricPoint{Kind: KindObservation}, 60, "avg")
	if _, _, ok := SummaryWindowFromDerivation(scalar); ok {
		t.Fatal("raw lineage read as a summary")
	}
}

func TestSummaryMissingReasonsStayOnMissingSummaryBuckets(t *testing.T) {
	lineage, _ := SummaryBucketDerivation("p95", 60, 120)
	point := MetricPoint{SchemaVersion: SchemaVersion, ProjectID: "p", Source: "d", MetricName: "latency", TimestampUTC: time.Unix(1_700_000_040, 0).UTC(), Kind: KindGauge, InputKind: KindSummary, Derivation: lineage, Unit: "ms", Quality: QualityMissing, MissingReason: MissingPercentileNotMergeable}
	if err := point.Validate(); err != nil {
		t.Fatal(err)
	}
	value := 1.0
	valued := point
	valued.Value, valued.Quality = &value, QualityValid
	if valued.Validate() == nil {
		t.Fatal("a measured bucket carried a missing reason")
	}
	unknown := point
	unknown.MissingReason = "because"
	if unknown.Validate() == nil {
		t.Fatal("unknown missing reason admitted")
	}
	raw := point
	raw.InputKind, raw.Derivation = KindObservation, nil
	if raw.Validate() == nil {
		t.Fatal("a raw bucket carried a summary missing reason")
	}
}

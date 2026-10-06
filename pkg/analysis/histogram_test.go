package analysis

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func histogramMeasure(h ParquetHistogram) ParquetMeasure {
	return ParquetMeasure{Value: ParquetColumnPath{"inf"}, Name: "latency", Kind: KindHistogram, Unit: "ms", Histogram: &h}
}

func wideHistogram() ParquetHistogram {
	return ParquetHistogram{Bounds: []string{"10", "100", "+Inf"}, BucketCounts: HistogramPerBin, Temporality: HistogramDelta, WindowSeconds: 60, CountColumns: []ParquetColumnPath{{"b10"}, {"b100"}}}
}

func TestHistogramMeasureDeclarationsAreValidated(t *testing.T) {
	long := ParquetHistogram{Bounds: []string{"0.005", "0.1", "1e1", "+Inf"}, BucketCounts: HistogramLeCumulative, Temporality: HistogramCumulative, BoundColumn: ParquetColumnPath{"le"}}
	for name, h := range map[string]ParquetHistogram{"wide": wideHistogram(), "long": long} {
		if err := summaryMapping(histogramMeasure(h)).ValidateDefinition(); err != nil {
			t.Fatalf("%s refused: %v", name, err)
		}
	}
	mutate := func(change func(*ParquetHistogram)) ParquetMapping {
		h := wideHistogram()
		change(&h)
		return summaryMapping(histogramMeasure(h))
	}
	invalid := map[string]ParquetMapping{
		"no declaration":         summaryMapping(ParquetMeasure{Value: ParquetColumnPath{"inf"}, Name: "latency", Kind: KindHistogram, Unit: "ms"}),
		"declaration on a gauge": summaryMapping(ParquetMeasure{Value: ParquetColumnPath{"inf"}, Name: "latency", Kind: KindGauge, Unit: "ms", Histogram: &ParquetHistogram{}}),
		"unordered bounds":       mutate(func(h *ParquetHistogram) { h.Bounds = []string{"100", "10", "+Inf"} }),
		"repeated bound":         mutate(func(h *ParquetHistogram) { h.Bounds = []string{"10", "10.0", "+Inf"} }),
		"inner infinity":         mutate(func(h *ParquetHistogram) { h.Bounds = []string{"+Inf", "100", "1000"} }),
		"NaN bound":              mutate(func(h *ParquetHistogram) { h.Bounds = []string{"NaN", "100", "+Inf"} }),
		"hex bound":              mutate(func(h *ParquetHistogram) { h.Bounds = []string{"0x10", "100", "+Inf"} }),
		"count convention":       mutate(func(h *ParquetHistogram) { h.BucketCounts = "cumulative" }),
		"temporality":            mutate(func(h *ParquetHistogram) { h.Temporality = "gauge" }),
		"delta without window":   mutate(func(h *ParquetHistogram) { h.WindowSeconds = 0 }),
		"cumulative with window": mutate(func(h *ParquetHistogram) { h.Temporality = HistogramCumulative }),
		"missing count column":   mutate(func(h *ParquetHistogram) { h.CountColumns = h.CountColumns[:1] }),
		"both table forms":       mutate(func(h *ParquetHistogram) { h.BoundColumn = ParquetColumnPath{"le"} }),
		"count column is value":  mutate(func(h *ParquetHistogram) { h.CountColumns[1] = ParquetColumnPath{"inf"} }),
		"too many bounds": mutate(func(h *ParquetHistogram) {
			h.Bounds, h.CountColumns = nil, nil
			for i := range MaxHistogramBounds + 1 {
				h.Bounds = append(h.Bounds, strings.Repeat("9", i+1))
			}
		}),
	}
	for name, mapping := range invalid {
		if err := mapping.ValidateDefinition(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	protected := wideHistogram()
	protected.CountColumns[0] = ParquetColumnPath{"api_key"}
	if summaryMapping(histogramMeasure(protected)).ValidateDisclosure() == nil {
		t.Fatal("a protected count column was admitted")
	}
}

func TestHistogramDeclarationsRoundTripTheStrictDecoder(t *testing.T) {
	body, err := json.Marshal(summaryMapping(histogramMeasure(wideHistogram())))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeParquetMappingDefinition(body)
	if err != nil || decoded.Measures[0].Histogram == nil || len(decoded.Measures[0].Histogram.CountColumns) != 2 || decoded.Measures[0].Histogram.Bounds[2] != "+Inf" {
		t.Fatalf("decoded %+v %v", decoded, err)
	}
	for _, bad := range []string{
		strings.Replace(string(body), `"bounds":["10","100","+Inf"]`, `"bounds":[10,100]`, 1),
		strings.Replace(string(body), `"temporality":"delta"`, `"temporality":"delta","exemplars":true`, 1),
	} {
		if _, err := DecodeParquetMappingDefinition([]byte(bad)); err == nil {
			t.Fatalf("decoder accepted %s", bad)
		}
	}
	declaration, found := summaryMapping(histogramMeasure(wideHistogram())).HistogramDeclarationFor("latency")
	if !found || declaration.WindowSeconds != 60 || len(declaration.Bounds) != 3 {
		t.Fatalf("declaration %+v %v", declaration, found)
	}
}

func TestHistogramValueInterpolatesInsideTheRankBin(t *testing.T) {
	bounds := []float64{10, 100, math.Inf(1)}
	for _, c := range []struct {
		counts      []float64
		aggregation string
		value       float64
		bin         *EstimateBin
		reason      string
	}{
		{[]float64{9, 7, 0}, "count", 16, nil, ""},
		{[]float64{9, 7, 0}, "p50", 10 * 8.0 / 9, &EstimateBin{0, 10}, ""},
		{[]float64{9, 7, 0}, "p90", 10 + 90*(14.4-9)/7, &EstimateBin{10, 100}, ""},
		{[]float64{0, 4, 0}, "p50", 55, &EstimateBin{10, 100}, ""},
		{[]float64{1, 1, 8}, "p50", 0, nil, MissingHistogramOverflowBin},
		{[]float64{0, 0, 0}, "p99", 0, nil, MissingHistogramNoObservations},
		{[]float64{0, 0, 0}, "count", 0, nil, ""},
	} {
		value, bin, reason, err := HistogramValue(bounds, c.counts, c.aggregation)
		if err != nil || reason != c.reason || reason == "" && math.Abs(value-c.value) > 1e-12 || (bin == nil) != (c.bin == nil) || bin != nil && *bin != *c.bin {
			t.Errorf("%v %s: %v %+v %q %v", c.counts, c.aggregation, value, bin, reason, err)
		}
	}
	// A non-positive first bound has no lower edge: report the bound itself.
	if value, bin, _, _ := HistogramValue([]float64{-1, 0}, []float64{5, 5}, "p50"); value != -1 || *bin != (EstimateBin{-1, -1}) {
		t.Fatalf("non-positive first bin %v %+v", value, bin)
	}
	if _, _, _, err := HistogramValue(bounds, []float64{1, -1, 0}, "count"); err == nil {
		t.Fatal("negative counts accepted")
	}
}

func TestHistogramLineageAndPointsCarryTheirWindowAndBin(t *testing.T) {
	if _, err := HistogramBucketDerivation(60, 90, "p95"); err == nil {
		t.Fatal("straddling bucket accepted")
	}
	if _, err := HistogramBucketDerivation(0, 90, "avg"); err == nil {
		t.Fatal("averages answered from a histogram")
	}
	lineage, err := HistogramBucketDerivation(0, 90, "p999")
	if err != nil || lineage[1].Algorithm != "histogram_interpolated.v1" {
		t.Fatalf("cumulative lineage %+v %v", lineage, err)
	}
	if window, ok := HistogramWindowFromDerivation(lineage); !ok || window != 0 {
		t.Fatal("cumulative window not recoverable")
	}
	delta, _ := HistogramBucketDerivation(60, 120, "count")
	if window, ok := HistogramWindowFromDerivation(delta); !ok || window != 60 || delta[1].Algorithm != "histogram_count_sum.v1" {
		t.Fatal("delta window not recoverable")
	}
	value := 55.0
	point := MetricPoint{SchemaVersion: SchemaVersion, ProjectID: "p", Source: "d", MetricName: "latency", TimestampUTC: time.Unix(1_700_000_040, 0).UTC(), Kind: KindGauge, InputKind: KindHistogram, Derivation: lineage, Unit: "ms", Quality: QualityValid, Value: &value, EstimateBin: &EstimateBin{10, 100}}
	if err := point.Validate(); err != nil {
		t.Fatal(err)
	}
	outside := point
	outside.EstimateBin = &EstimateBin{60, 100}
	if outside.Validate() == nil {
		t.Fatal("estimate bin excluding its value admitted")
	}
	missing := point
	missing.Value, missing.Quality, missing.EstimateBin, missing.MissingReason = nil, QualityMissing, nil, MissingHistogramCounterReset
	if err := missing.Validate(); err != nil {
		t.Fatal(err)
	}
	missing.MissingReason = MissingPercentileNotMergeable
	if missing.Validate() == nil {
		t.Fatal("summary reason admitted on a histogram bucket")
	}
}

func TestHistogramPartitionsAccountForTheirSourceRows(t *testing.T) {
	at := time.Unix(1_700_000_040, 0).UTC()
	stats := NormalizedPartitionStats{Version: PartitionManifestVersion, SchemaVersion: DistributionSchemaVersion, IdentityVersion: 1, Kind: KindHistogram, Unit: "ms", Rows: 2, Valid: 2, SourceRows: 6, MinTime: &at, MaxTime: &at}
	if err := stats.Validate(); err != nil || stats.AcceptedRows() != 6 {
		t.Fatalf("histogram stats %v %d", err, stats.AcceptedRows())
	}
	for name, change := range map[string]func(*NormalizedPartitionStats){
		"fewer source rows than windows":  func(s *NormalizedPartitionStats) { s.SourceRows = 1 },
		"metric point schema":             func(s *NormalizedPartitionStats) { s.SchemaVersion = SchemaVersion },
		"source rows on a scalar":         func(s *NormalizedPartitionStats) { s.Kind, s.SchemaVersion = KindGauge, SchemaVersion },
		"distribution schema on a scalar": func(s *NormalizedPartitionStats) { s.Kind, s.SourceRows = KindGauge, 0 },
	} {
		bad := stats
		change(&bad)
		if bad.Validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

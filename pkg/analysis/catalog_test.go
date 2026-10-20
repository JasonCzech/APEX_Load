package analysis

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestCatalogRetainsExecutableNativeSemantics(t *testing.T) {
	for _, test := range []struct{ metric, population, aggregation, unit string }{
		{"response_time", PopulationAction, "p90", "ms"},
		{"response_time", PopulationTransaction, "avg", "ms"},
		{"observation_count", PopulationAction, "count", "count"},
		{"error_rate", PopulationTransaction, "avg", "percent"},
		{"observation_rate", PopulationAction, "rate", "count/s"},
		{"error_observation_rate", PopulationAction, "rate", "count/s"},
		{"success_observation_rate", PopulationTransaction, "rate", "count/s"},
	} {
		t.Run(test.metric+test.population+test.aggregation, func(t *testing.T) {
			metric, found := LookupMetric(test.metric)
			if !found || metric.Unit != test.unit || metric.Version != CatalogVersion || metric.MissingRule != MissingUnscored || len(metric.RequiredEvidence) == 0 {
				t.Fatalf("metric contract: %+v", metric)
			}
			if err := ValidateMetricAggregation(test.metric, test.population, test.aggregation); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, test := range []struct{ metric, population, aggregation string }{
		{"tps", PopulationAction, "rate"}, {"resource_cpu", PopulationAction, "avg"},
		{"observation_count", PopulationAction, "sum"}, {"error_rate", PopulationAction, "rate"},
		{"response_time", PopulationMessage, "avg"}, {"response_time", "", "avg"},
		{"response_time", PopulationAction, "select *"}, {"observation_rate", PopulationTransaction, "avg"},
	} {
		if err := ValidateMetricAggregation(test.metric, test.population, test.aggregation); err == nil {
			t.Fatalf("unsupported contract accepted: %+v", test)
		}
	}
	catalog := Catalog()
	if catalog.Version != CatalogVersion || catalog.Bounds.Filters != 16 || catalog.Bounds.GroupingDimensions != 3 || catalog.Bounds.Buckets != MaxSeriesPoints {
		t.Fatalf("catalog bounds: %+v", catalog.Bounds)
	}
	if _, err := json.Marshal(catalog); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogCannotBeMutatedThroughReturnedSlices(t *testing.T) {
	catalog := Catalog()
	catalog.Metrics[0].Name = "sql"
	catalog.Metrics[0].Populations[0] = PopulationMessage
	catalog.Metrics[0].QueryProfiles[0] = "sql"
	catalog.QueryProfiles[0].AllowedFilters[0] = "sql"
	catalog.Metrics[0].RequiredEvidence[0] = "none"
	catalog.Operations[0].InputKinds[0] = KindSummary
	metric, _ := LookupMetric("response_time")
	operation, _ := LookupOperation("avg")
	if !slices.Contains(metric.Populations, PopulationAction) || metric.QueryProfiles[0] == "sql" || Catalog().QueryProfiles[0].AllowedFilters[0] != "action" || metric.RequiredEvidence[0] == "none" || !slices.Contains(operation.InputKinds, KindGauge) {
		t.Fatal("caller changed process-wide validation")
	}
	for i, profile := range catalog.QueryProfiles {
		if profile.JointEvidence {
			catalog.QueryProfiles[i].GroupingDimensions[0] = "sql"
			if Catalog().QueryProfiles[i].GroupingDimensions[0] == "sql" || JointEvidenceDimensions[0] == "sql" {
				t.Fatal("a caller changed the joint grouping dimensions")
			}
		}
	}
	metric.Aggregations[0] = "sql"
	if err := ValidateMetricAggregation("response_time", PopulationAction, "avg"); err != nil {
		t.Fatal("lookup mutation changed catalog")
	}
}

func TestScalarAggregationContractParity(t *testing.T) {
	for _, kind := range []string{KindUnknown, KindGauge, KindCounter, KindObservation, KindHistogram, KindSummary} {
		for _, aggregation := range []string{"avg", "min", "max", "sum", "count", "p50", "p90", "p95", "p99", "rate", "delta", "ratio", "moving_mean", "SQL"} {
			want := (kind == KindGauge || kind == KindObservation) && slices.Contains([]string{"avg", "min", "max", "sum", "count"}, aggregation) || kind == KindObservation && slices.Contains([]string{"p50", "p95", "p99"}, aggregation) || kind == KindCounter && aggregation == "rate"
			if got := ValidateScalarAggregation(kind, aggregation) == nil; got != want {
				t.Errorf("kind=%s aggregation=%s: got=%v want=%v", kind, aggregation, got, want)
			}
		}
	}
}

func TestMeasurementSemanticsQualificationAndCompatibility(t *testing.T) {
	base := MeasurementSemantics{Kind: KindGauge, Unit: "ms", Population: PopulationAction, MetricVersion: "latency.v1"}
	if err := CompatibleMeasurements(base, base); err != nil {
		t.Fatal(err)
	}
	for _, edit := range []func(*MeasurementSemantics){
		func(s *MeasurementSemantics) { s.Kind = KindSummary }, func(s *MeasurementSemantics) { s.Unit = "bytes" },
		func(s *MeasurementSemantics) { s.Population = PopulationMessage }, func(s *MeasurementSemantics) { s.MetricVersion = "latency.v2" },
	} {
		other := base
		edit(&other)
		if err := CompatibleMeasurements(base, other); err == nil {
			t.Fatal("incompatible direct comparison accepted")
		}
	}
	for _, edit := range []func(*MeasurementSemantics){
		func(s *MeasurementSemantics) { s.Kind = KindUnknown }, func(s *MeasurementSemantics) { s.Unit = "s" },
		func(s *MeasurementSemantics) { s.Population = "" }, func(s *MeasurementSemantics) { s.MetricVersion = "" },
		func(s *MeasurementSemantics) { s.Population = strings.Repeat("a", maxMetricIdentityBytes+1) }, func(s *MeasurementSemantics) { s.MetricVersion = "x\x00y" },
	} {
		other := base
		edit(&other)
		if err := other.Validate(); err == nil {
			t.Fatalf("unqualified semantics accepted: %+v", other)
		}
	}
}

func TestRatioCatalogMatchesExecutableContracts(t *testing.T) {
	if _, found := LookupOperation("ratio"); found {
		t.Fatal("generic uncallable ratio advertised")
	}
	contracts := 0
	for _, operation := range Catalog().Operations {
		if operation.UnitRule != "ratio" {
			continue
		}
		contracts++
		for _, population := range operation.Populations {
			denominator := RatioCount{Count: 10, Population: population, Scope: "same", MetricVersion: "v1", Quality: QualityValid, Complete: true}
			numerator := denominator
			numerator.Count = 2
			got, err := EvaluateRatio(operation.Name, numerator, denominator)
			if err != nil || got.Value == nil || *got.Value != .2 {
				t.Fatalf("catalog operation not executable: %+v result=%+v err=%v", operation, got, err)
			}
		}
	}
	if contracts != 7 {
		t.Fatalf("ratio contracts=%d", contracts)
	}
}

func TestCatalogSeparatesCountRatesFromRawCounterRates(t *testing.T) {
	operation, _ := LookupOperation("rate")
	if !slices.Contains(operation.InputKinds, KindObservation) || !slices.Contains(operation.InputKinds, KindCounter) || slices.Contains(operation.RawScalarKinds, KindObservation) {
		t.Fatalf("rate input profiles: %+v", operation)
	}
	if err := ValidateMetricAggregation("observation_rate", PopulationAction, "rate"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateScalarAggregation(KindObservation, "rate"); err == nil {
		t.Fatal("raw observation converted to a counter rate")
	}
}

func TestScalarAggregationErrorsExplainMissingAdapter(t *testing.T) {
	for _, test := range []struct{ kind, aggregation, message string }{
		{KindSummary, "avg", "explicit statistic selector"}, {KindHistogram, "p95", "distribution adapter"},
		{KindCounter, "avg", "counters require rate conversion"}, {KindGauge, "rate", "rates require counters"},
		{KindGauge, "p95", "percentiles require raw observations"},
	} {
		err := ValidateScalarAggregation(test.kind, test.aggregation)
		if err == nil || !strings.Contains(err.Error(), test.message) {
			t.Fatalf("kind %s aggregation %s error %v", test.kind, test.aggregation, err)
		}
	}
}

// New catalog operations must have a real typed execution path. This prevents
// descriptions of future summary/histogram adapters from becoming capabilities.
func TestCatalogOperationsHaveExecutableTypedEntryPoint(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, operation := range Catalog().Operations {
		t.Run(operation.Name, func(t *testing.T) {
			if slices.Contains(operation.InputKinds, KindSummary) || slices.Contains(operation.InputKinds, KindHistogram) {
				t.Fatal("unimplemented summary or histogram input advertised")
			}
			if operation.RawScalar {
				for _, kind := range operation.RawScalarKinds {
					before, after := qualifiedPoint(start, 2), qualifiedPoint(start.Add(time.Second), 4)
					before.Kind, after.Kind = kind, kind
					if kind == KindCounter {
						before.Unit, after.Unit = "count", "count"
					}
					got, err := BucketSeries([]MetricPoint{before, after}, start, start.Add(2*time.Second), 1, operation.Name)
					if err != nil || len(got) != 2 || got[1].Value == nil {
						t.Fatalf("raw scalar entrypoint kind %s: %+v err=%v", kind, got, err)
					}
				}
			}
			if operation.UnitRule == "ratio" {
				for _, population := range operation.Populations {
					denominator := RatioCount{Count: 10, Population: population, Scope: "run/source/window", MetricVersion: "v1", Quality: QualityValid, Complete: true}
					numerator := denominator
					numerator.Count = 2
					got, err := EvaluateRatio(operation.Name, numerator, denominator)
					if err != nil || got.Value == nil {
						t.Fatalf("ratio entrypoint: %+v err=%v", got, err)
					}
				}
				return
			}
			switch operation.Name {
			case "avg", "min", "max", "sum", "count", "p50", "p95", "p99":
				if !operation.RawScalar {
					t.Fatal("scalar aggregation has no executable profile")
				}
			case "p90":
				if operation.RawScalar || len(operation.RawScalarKinds) != 0 {
					t.Fatal("native p90 advertised as raw scalar")
				}
				metric, _ := LookupMetric("response_time")
				if !slices.Contains(metric.Aggregations, "p90") || len(metric.QueryProfiles) == 0 {
					t.Fatal("p90 has no native query profile")
				}
				for _, population := range metric.Populations {
					if err := ValidateMetricAggregation(metric.Name, population, operation.Name); err != nil {
						t.Fatal(err)
					}
				}
			case "p75":
				// The native browser document query evaluates it over the
				// tuple's merged final distribution.
				if operation.RawScalar || !slices.Equal(operation.Populations, []string{PopulationBrowserDocument}) {
					t.Fatal("p75 advertised beyond final document distributions")
				}
				for _, name := range BrowserDocumentMetricNames() {
					metric, _ := LookupMetric(name)
					if len(metric.QueryProfiles) != 1 || ValidateMetricAggregation(name, PopulationBrowserDocument, "p75") != nil || ValidateMetricAggregation(name, PopulationAction, "p75") == nil {
						t.Fatalf("p75 has no native document query for %s", name)
					}
				}
			case "ratio_of_sums":
				got, err := EvaluateJavaHTTPPairedSums("java_http_prepared_per_decoded", JavaHTTPPairedSums{Version: 1, Eligible: 2, Samples: 2, Numerator: 7, Denominator: 12, Complete: true})
				if err != nil || got.Value == nil || *got.Value != 7.0/12 {
					t.Fatalf("paired sum entrypoint: %+v err=%v", got, err)
				}
			case "rate", "delta":
				before, after := qualifiedPoint(start, 2), qualifiedPoint(start.Add(time.Second), 4)
				before.Kind, after.Kind, before.Unit, after.Unit = KindCounter, KindCounter, "count", "count"
				got, err := EvaluateCounter(operation.Name, CounterSample{before, "epoch", true}, CounterSample{after, "epoch", true})
				if err != nil || got.Value == nil {
					t.Fatalf("counter entrypoint: %+v err=%v", got, err)
				}
				if operation.Name == "rate" {
					got, err = EvaluateCountMetric("observation_rate", PopulationAction, CountEvidence{Count: 4, WindowSeconds: 2, Complete: true, Quality: QualityValid})
					if err != nil || got.Value == nil {
						t.Fatalf("observation rate entrypoint: %+v err=%v", got, err)
					}
				}
			case "baseline_delta", "baseline_percent_delta":
				for _, kind := range operation.InputKinds {
					before, after := qualifiedPoint(start, 2), qualifiedPoint(start.Add(time.Second), 4)
					before.Kind, after.Kind = kind, kind
					got, err := EvaluateBaselineDelta(after, before, operation.Name == "baseline_percent_delta")
					if err != nil || got.Value == nil {
						t.Fatalf("baseline entrypoint kind %s: %+v err=%v", kind, got, err)
					}
				}
			case "moving_mean", "moving_stddev":
				for _, kind := range operation.InputKinds {
					before, after := qualifiedPoint(start, 2), qualifiedPoint(start.Add(time.Second), 4)
					before.Kind, after.Kind = kind, kind
					got, err := MovingStatistics([]MetricPoint{before, after}, 1, 2, 1)
					if err != nil || len(got) != 2 || got[1].Mean == nil || got[1].StdDev == nil {
						t.Fatalf("moving entrypoint kind %s: %+v err=%v", kind, got, err)
					}
				}
			default:
				t.Fatalf("catalog operation %s has no proven typed entrypoint", operation.Name)
			}
		})
	}
	if _, found := LookupOperation("summary_value"); found {
		t.Fatal("unimplemented summary selector advertised")
	}
}

func TestCatalogJSONRequiredArraysMatchPublicContract(t *testing.T) {
	payload, err := json.Marshal(Catalog())
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(payload, &wire); err != nil {
		t.Fatal(err)
	}
	check := func(object map[string]any, name string) {
		t.Helper()
		if _, ok := object[name].([]any); !ok {
			t.Fatalf("required %s is not an array: %v", name, object[name])
		}
	}
	for _, name := range []string{"metrics", "operations", "query_profiles"} {
		check(wire, name)
	}
	for _, raw := range wire["metrics"].([]any) {
		object := raw.(map[string]any)
		for _, name := range []string{"input_kinds", "populations", "aggregations", "derivations", "query_profiles", "required_evidence"} {
			check(object, name)
		}
	}
	for _, raw := range wire["operations"].([]any) {
		object := raw.(map[string]any)
		for _, name := range []string{"input_kinds", "required_evidence"} {
			check(object, name)
		}
	}
	emptyFilters := false
	for _, raw := range wire["query_profiles"].([]any) {
		object := raw.(map[string]any)
		for _, name := range []string{"allowed_filters", "allowed_aggregations", "required_evidence"} {
			check(object, name)
		}
		emptyFilters = emptyFilters || len(object["allowed_filters"].([]any)) == 0
	}
	if !emptyFilters {
		t.Fatal("no zero-filter profile tested")
	}
}

func TestCatalogRawScalarQualificationDistinguishesEpochlessRates(t *testing.T) {
	for _, operation := range Catalog().Operations {
		if !operation.RawScalar {
			if operation.RawScalarQualification != "" {
				t.Fatalf("non-scalar operation has scalar qualification: %+v", operation)
			}
			continue
		}
		want := RawScalarQualifiedWhenEvidencePresent
		if operation.Name == "rate" {
			want = RawScalarLegacyResetOnly
		}
		if operation.RawScalarQualification != want {
			t.Fatalf("operation %s qualification %q, want %q", operation.Name, operation.RawScalarQualification, want)
		}
	}
}

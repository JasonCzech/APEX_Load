package analysis

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

var derivationStart = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

func derivationBuckets(values ...*float64) []DerivationBucket {
	out := make([]DerivationBucket, len(values))
	for i, value := range values {
		from := derivationStart.Add(time.Duration(i) * 10 * time.Second)
		out[i] = DerivationBucket{From: from, To: from.Add(10 * time.Second), Value: value, Complete: true}
	}
	return out
}

func f(v float64) *float64 { return &v }

// derivedOutcomes renders each bucket as its value or its reason.
func derivedOutcomes(series DerivedSeries) string {
	parts := make([]string, len(series.Buckets))
	for i, b := range series.Buckets {
		if b.Value != nil {
			body, _ := json.Marshal(*b.Value)
			parts[i] = string(body)
		} else {
			parts[i] = b.Reason
		}
	}
	return strings.Join(parts, " ")
}

func observationSeries(values ...*float64) DerivationSeries {
	return DerivationSeries{ProjectID: "p", Source: "apexload", MetricName: "response_time", Population: PopulationAction, Kind: KindObservation, Unit: "ms", Buckets: derivationBuckets(values...)}
}

func TestQueryDerivationRatioGoldensAndUnscoredPaths(t *testing.T) {
	series := observationSeries(nil, nil, nil, nil, nil)
	counts := [][2]int64{{10, 2}, {0, 0}, {10, 2}, {5, 7}, {4, 4}}
	for i, c := range counts {
		series.Buckets[i].Count, series.Buckets[i].Subset = c[0], c[1]
	}
	series.Buckets[2].Complete = false
	errorRatio, err := ApplyQueryDerivation(series, QueryDerivation{Operation: "error_ratio"}, nil)
	if err != nil || errorRatio.Unit != "ratio" || derivedOutcomes(errorRatio) != "0.2 zero_denominator incomplete_evidence numerator_not_subset 1" {
		t.Fatalf("error ratio %q %v", derivedOutcomes(errorRatio), err)
	}
	successRatio, _ := ApplyQueryDerivation(series, QueryDerivation{Operation: "success_ratio"}, nil)
	if derivedOutcomes(successRatio) != "0.8 zero_denominator incomplete_evidence invalid_population_counts 0" {
		t.Fatalf("success ratio %q", derivedOutcomes(successRatio))
	}

	// Message ratios count the messaging v2 counter among observed messages;
	// acknowledgement and settlement have no counted evidence.
	messages := DerivationSeries{ProjectID: "p", Source: "kafka", MetricName: "message_count", Population: PopulationMessage, Kind: KindObservation, Unit: "count", Buckets: derivationBuckets(nil, nil)}
	messages.Buckets[0].Count, messages.Buckets[0].Subset = 200, 6
	duplicate, _ := ApplyQueryDerivation(messages, QueryDerivation{Operation: "duplicate_ratio"}, nil)
	if derivedOutcomes(duplicate) != "0.03 zero_denominator" {
		t.Fatalf("duplicate ratio %q", derivedOutcomes(duplicate))
	}
	for _, operation := range []string{"acknowledgement_ratio", "ack_ratio", "settlement_ratio"} {
		unavailable, _ := ApplyQueryDerivation(messages, QueryDerivation{Operation: operation}, nil)
		if derivedOutcomes(unavailable) != "evidence_unavailable evidence_unavailable" {
			t.Fatalf("%s %q", operation, derivedOutcomes(unavailable))
		}
	}
	if counter, recorded := MessageRatioCounter("dlq_ratio"); counter != "dead_letter" || !recorded {
		t.Fatalf("dead-letter alias %q %t", counter, recorded)
	}
}

func TestQueryDerivationMovingStatisticsGoldens(t *testing.T) {
	// A complete zero-valued bucket is measured zero; a missing one is a gap.
	series := observationSeries(f(1), f(3), f(0), nil, f(5), f(7))
	mean, err := ApplyQueryDerivation(series, QueryDerivation{Operation: "moving_mean", WindowBuckets: 2, MinimumCoverage: 1}, nil)
	if err != nil || mean.Unit != "ms" || derivedOutcomes(mean) != "insufficient_window 2 1.5 insufficient_valid_buckets insufficient_valid_buckets 6" {
		t.Fatalf("moving mean %q %v", derivedOutcomes(mean), err)
	}
	stddev, _ := ApplyQueryDerivation(series, QueryDerivation{Operation: "rolling_stddev", WindowBuckets: 3, MinimumCoverage: 0.6}, nil)
	if stddev.Operation != "moving_stddev" || derivedOutcomes(stddev) != "insufficient_window insufficient_window 1.2472191289246473 1.5 2.5 0.9999999999999998" {
		t.Fatalf("moving stddev %q", derivedOutcomes(stddev))
	}
	// An incomplete bucket is missing, not zero.
	series.Buckets[1].Complete = false
	partial, _ := ApplyQueryDerivation(series, QueryDerivation{Operation: "moving_mean", WindowBuckets: 2, MinimumCoverage: 1}, nil)
	if derivedOutcomes(partial) != "insufficient_window insufficient_valid_buckets insufficient_valid_buckets insufficient_valid_buckets insufficient_valid_buckets 6" {
		t.Fatalf("incomplete bucket %q", derivedOutcomes(partial))
	}
	if _, err := ApplyQueryDerivation(series, QueryDerivation{Operation: "moving_mean", WindowBuckets: 1, MinimumCoverage: 1}, nil); err == nil {
		t.Fatal("unbounded window accepted")
	}
}

func TestQueryDerivationBaselineDeltaGoldens(t *testing.T) {
	current := observationSeries(f(10), f(20), nil, f(30), f(8))
	baseline := observationSeries(f(5), nil, f(10), f(0), f(10))
	baseline.Buckets = baseline.Buckets[:4]
	delta, err := ApplyQueryDerivation(current, QueryDerivation{Operation: "baseline_delta"}, &baseline)
	if err != nil || delta.Unit != "ms" || derivedOutcomes(delta) != "5 missing_baseline_bucket missing_bucket 30 missing_baseline_bucket" {
		t.Fatalf("delta %q %v", derivedOutcomes(delta), err)
	}
	percent, _ := ApplyQueryDerivation(current, QueryDerivation{Operation: "percent_delta"}, &baseline)
	if percent.Unit != "percent" || derivedOutcomes(percent) != "100 missing_baseline_bucket missing_bucket zero_denominator missing_baseline_bucket" {
		t.Fatalf("percent delta %q", derivedOutcomes(percent))
	}
	if _, err := ApplyQueryDerivation(current, QueryDerivation{Operation: "baseline_delta"}, nil); err == nil {
		t.Fatal("baseline delta without a baseline accepted")
	}
}

func TestQueryDerivationCounterGoldens(t *testing.T) {
	counter := DerivationSeries{ProjectID: "p", Source: "dataset", MetricName: "requests_total", Kind: KindCounter, Unit: "count", CounterEpoch: "epoch-1",
		Buckets: derivationBuckets(f(100), f(150), f(140), nil, f(200), f(260))}
	delta, err := ApplyQueryDerivation(counter, QueryDerivation{Operation: "delta"}, nil)
	if err != nil || derivedOutcomes(delta) != "no_prior_sample 50 counter_reset missing_counter_sample missing_counter_sample 60" {
		t.Fatalf("delta %q %v", derivedOutcomes(delta), err)
	}
	rate, _ := ApplyQueryDerivation(counter, QueryDerivation{Operation: "rate"}, nil)
	if rate.Unit != "count/s" || derivedOutcomes(rate) != "no_prior_sample 5 counter_reset missing_counter_sample missing_counter_sample 6" {
		t.Fatalf("rate %q %s", derivedOutcomes(rate), rate.Unit)
	}
	counter.CounterEpoch = ""
	if unknown, _ := ApplyQueryDerivation(counter, QueryDerivation{Operation: "delta"}, nil); !strings.Contains(derivedOutcomes(unknown), "counter_epoch_changed_or_unknown") {
		t.Fatalf("unknown epoch %q", derivedOutcomes(unknown))
	}
	counter.CounterEpoch, counter.Unit = "epoch-1", "ms"
	if unsupported, _ := ApplyQueryDerivation(counter, QueryDerivation{Operation: "rate"}, nil); !strings.Contains(derivedOutcomes(unsupported), "unsupported_counter_rate_unit") {
		t.Fatalf("rate unit %q", derivedOutcomes(unsupported))
	}
}

func TestQueryDerivationValidationIsTheCatalog(t *testing.T) {
	valid := []struct {
		metric, population, kind string
		derivation               QueryDerivation
		comparison               bool
	}{
		{"error_rate", PopulationAction, KindObservation, QueryDerivation{Operation: "error_ratio"}, false},
		{"observation_count", PopulationTransaction, KindObservation, QueryDerivation{Operation: "success_ratio"}, false},
		{"message_count", PopulationMessage, KindObservation, QueryDerivation{Operation: "redelivery_ratio"}, false},
		{"response_time", PopulationAction, KindObservation, QueryDerivation{Operation: "moving_average", WindowBuckets: 1000, MinimumCoverage: 0.5}, false},
		{"response_time", PopulationAction, KindObservation, QueryDerivation{Operation: "baseline_percent_delta"}, true},
		{"requests_total", "", KindCounter, QueryDerivation{Operation: "rate"}, false},
	}
	for _, tc := range valid {
		if err := ValidateQueryDerivation(tc.metric, tc.population, tc.kind, tc.derivation, tc.comparison); err != nil {
			t.Fatalf("%+v: %v", tc, err)
		}
	}
	invalid := []struct {
		name                     string
		metric, population, kind string
		derivation               QueryDerivation
		comparison               bool
	}{
		{"unknown", "response_time", PopulationAction, KindObservation, QueryDerivation{Operation: "median_of_medians"}, false},
		{"raw scalar", "response_time", PopulationAction, KindObservation, QueryDerivation{Operation: "avg"}, false},
		{"window too wide", "response_time", PopulationAction, KindObservation, QueryDerivation{Operation: "moving_mean", WindowBuckets: 1001, MinimumCoverage: 1}, false},
		{"no coverage", "response_time", PopulationAction, KindObservation, QueryDerivation{Operation: "moving_mean", WindowBuckets: 3}, false},
		{"window on ratio", "error_rate", PopulationAction, KindObservation, QueryDerivation{Operation: "error_ratio", WindowBuckets: 3, MinimumCoverage: 1}, false},
		{"baseline without comparison", "response_time", PopulationAction, KindObservation, QueryDerivation{Operation: "baseline_delta"}, false},
		{"ratio population", "message_count", PopulationMessage, KindObservation, QueryDerivation{Operation: "error_ratio"}, false},
		{"message ratio on actions", "error_rate", PopulationAction, KindObservation, QueryDerivation{Operation: "dead_letter_ratio"}, false},
		{"counter op on observations", "response_time", PopulationAction, KindObservation, QueryDerivation{Operation: "delta"}, false},
	}
	for _, tc := range invalid {
		if err := ValidateQueryDerivation(tc.metric, tc.population, tc.kind, tc.derivation, tc.comparison); err == nil {
			t.Fatalf("%s accepted", tc.name)
		}
	}
}

func TestDashboardQueriesValidateThroughTheCatalog(t *testing.T) {
	// Every name the hard-coded dashboard maps accepted keeps validating.
	for _, metric := range []string{"tps", "response_time", "error_rate", "active_vusers", "throughput", "bytes_sent", "bytes_received", "regional_activity",
		"observation_rate", "error_observation_rate", "success_observation_rate", "observation_count", "transaction_duration", "resource_cpu", "resource_memory"} {
		for _, aggregation := range []string{"avg", "sum", "min", "max", "p50", "p90", "p95", "p99", "stddev", "count", "rate"} {
			if err := ValidateDashboardQuery(metric, aggregation, []string{"action", "phase"}, map[string]string{"region": "eu", "page": "home"}); err != nil {
				t.Fatalf("%s/%s: %v", metric, aggregation, err)
			}
		}
	}
	if canonical, qualified, ok := DashboardMetric("transaction_duration"); !ok || !qualified || canonical != "response_time" {
		t.Fatalf("alias %q %t %t", canonical, qualified, ok)
	}
	if _, qualified, ok := DashboardMetric("active_vusers"); !ok || qualified {
		t.Fatal("a live display card became a qualified measurement")
	}
	for name, check := range map[string]func() error{
		"metric":      func() error { return ValidateDashboardQuery("latency", "avg", nil, nil) },
		"aggregation": func() error { return ValidateDashboardQuery("response_time", "median", nil, nil) },
		"derivation":  func() error { return ValidateDashboardQuery("response_time", "moving_mean", nil, nil) },
		"dimension":   func() error { return ValidateDashboardQuery("response_time", "avg", []string{"customer"}, nil) },
		"filter": func() error {
			return ValidateDashboardQuery("response_time", "avg", nil, map[string]string{"customer": "a"})
		},
		"bounds": func() error {
			return ValidateDashboardQuery("response_time", "avg", []string{"action", "phase", "page", "region"}, nil)
		},
	} {
		if check() == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

// Review fixes: a comparison run only serves a baseline derivation; moving
// statistics refuse buckets that are not one contiguous grid instead of
// indexing past them; message counts declare only what runs.
func TestQueryDerivationComparisonGridAndCatalog(t *testing.T) {
	if err := ValidateQueryDerivation("error_rate", PopulationAction, KindObservation, QueryDerivation{Operation: "error_ratio"}, true); err == nil {
		t.Fatal("a ratio over a comparison run was accepted")
	}
	if err := ValidateQueryDerivation("response_time", PopulationAction, KindObservation, QueryDerivation{Operation: "baseline_delta"}, true); err != nil {
		t.Fatalf("baseline derivation refused: %v", err)
	}
	start := time.Unix(1_800_000_000, 0).UTC()
	value := 1.0
	bucket := func(offset int) DerivationBucket {
		from := start.Add(time.Duration(offset) * time.Second)
		return DerivationBucket{From: from, To: from.Add(time.Second), Value: &value, Complete: true}
	}
	gapped := DerivationSeries{ProjectID: "p", Source: "s", MetricName: "response_time", Kind: KindGauge, Unit: "ms", Buckets: []DerivationBucket{bucket(1), bucket(2), bucket(4)}}
	if _, err := ApplyQueryDerivation(gapped, QueryDerivation{Operation: "moving_mean", WindowBuckets: 2, MinimumCoverage: 1}, nil); err == nil {
		t.Fatal("moving statistics over non-contiguous buckets")
	}
	message, _ := LookupMetric("message_count")
	bytes, _ := LookupMetric("bytes_sent")
	if message.Baseline || slices.Contains(message.Derivations, "baseline_delta") || !slices.Contains(message.Derivations, "moving_mean") || len(bytes.Derivations) != 0 {
		t.Fatalf("catalog declares derivations that cannot run: %+v %+v", message.Derivations, bytes.Derivations)
	}
	if err := ValidateDashboardQuery("message_count", "count", nil, map[string]string{"source": "orders"}); err != nil {
		t.Fatalf("message panel source filter: %v", err)
	}
	if err := ValidateDashboardQuery("response_time", "avg", nil, map[string]string{"source": "orders"}); err == nil {
		t.Fatal("source filter accepted for a non-message metric")
	}
}

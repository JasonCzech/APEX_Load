package analysis

import (
	"errors"
	"math"
	"slices"
	"sort"
	"strings"
	"time"
)

// DerivedValue carries an explicit unavailable reason instead of substituting
// zero for incomplete evidence. A zero pointer value is a qualified zero.
type DerivedValue struct {
	Value   *float64 `json:"value,omitempty"`
	Unit    string   `json:"unit"`
	Quality string   `json:"quality"`
	Reason  string   `json:"reason,omitempty"`
}

func unscored(unit, reason string) DerivedValue {
	return DerivedValue{Unit: unit, Quality: QualityMissing, Reason: reason}
}

func measured(unit string, value float64) DerivedValue {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return unscored(unit, "numeric_range")
	}
	return DerivedValue{Value: &value, Unit: unit, Quality: QualityValid}
}

// CountEvidence is a complete eligible observation population in one elapsed
// window. Counts must remain exactly representable before float conversion.
type CountEvidence struct {
	Count         int64   `json:"count"`
	Failures      int64   `json:"failures"`
	WindowSeconds float64 `json:"window_seconds"`
	Quality       string  `json:"quality"`
	Complete      bool    `json:"complete"`
}

const maxExactCount = int64(1<<53 - 1)

// EvaluateCountMetric implements the catalog's count, percent and elapsed-rate
// metrics without confusing action/transaction observations with messages or
// bytes. Missing contributors and unknown quality are unscored. Complete observed
// zeros remain zero for counts/rates; an empty ratio denominator is unscored.
func EvaluateCountMetric(metric, population string, evidence CountEvidence) (DerivedValue, error) {
	definition, found := LookupMetric(metric)
	if !found || metric == "response_time" {
		return DerivedValue{}, errors.New("unsupported count-derived metric")
	}
	if err := ValidateMetricAggregation(metric, population, definition.Aggregations[0]); err != nil {
		return DerivedValue{}, err
	}
	unit := definition.Unit
	if !evidence.Complete || evidence.Quality != QualityValid {
		return unscored(unit, "incomplete_evidence"), nil
	}
	if evidence.Count < 0 || evidence.Count > maxExactCount || evidence.Failures < 0 || evidence.Failures > evidence.Count {
		return unscored(unit, "invalid_population_counts"), nil
	}
	switch metric {
	case "observation_count":
		return measured(unit, float64(evidence.Count)), nil
	case "error_rate":
		if evidence.Count == 0 {
			return unscored(unit, "empty_population"), nil
		}
		return measured(unit, 100*float64(evidence.Failures)/float64(evidence.Count)), nil
	}
	if evidence.WindowSeconds <= 0 || math.IsNaN(evidence.WindowSeconds) || math.IsInf(evidence.WindowSeconds, 0) {
		return unscored(unit, "invalid_elapsed_window"), nil
	}
	numerator := evidence.Count
	if metric == "error_observation_rate" {
		numerator = evidence.Failures
	}
	if metric == "success_observation_rate" {
		numerator -= evidence.Failures
	}
	return measured(unit, float64(numerator)/evidence.WindowSeconds), nil
}

// RatioCount names the eligible population and observation scope of a count.
// Numerators are subsets of denominators, not arbitrary same-unit series.
type RatioCount struct {
	Count         int64  `json:"count"`
	Population    string `json:"population"`
	Scope         string `json:"scope"`
	MetricVersion string `json:"metric_version"`
	Quality       string `json:"quality"`
	Complete      bool   `json:"complete"`
}

// EvaluateRatio supports only named same-population fraction contracts. A
// duplicate ratio counts deliveries marked duplicate among observed deliveries;
// it is not duplicate occurrences divided by unique business messages.
func EvaluateRatio(operation string, numerator, denominator RatioCount) (DerivedValue, error) {
	definition, found := LookupOperation(operation)
	if !found || definition.UnitRule != "ratio" {
		return DerivedValue{}, errors.New("unsupported ratio contract")
	}
	if !slices.Contains(definition.Populations, denominator.Population) {
		return DerivedValue{}, errors.New("ratio requires its declared eligible population")
	}
	for _, count := range []RatioCount{numerator, denominator} {
		if !count.Complete || count.Quality != QualityValid {
			return unscored("ratio", "incomplete_evidence"), nil
		}
		if count.Count < 0 || count.Count > maxExactCount {
			return unscored("ratio", "invalid_population_counts"), nil
		}
		for _, value := range []string{count.Scope, count.MetricVersion} {
			if strings.TrimSpace(value) == "" || len(value) > maxMetricIdentityBytes || strings.ContainsRune(value, 0) {
				return unscored("ratio", "unknown_population_scope"), nil
			}
		}
	}
	if numerator.Population != denominator.Population || numerator.Scope != denominator.Scope || numerator.MetricVersion != denominator.MetricVersion {
		return unscored("ratio", "incompatible_population_scope"), nil
	}
	if numerator.Count > denominator.Count {
		return unscored("ratio", "numerator_not_subset"), nil
	}
	if denominator.Count == 0 {
		return unscored("ratio", "zero_denominator"), nil
	}
	return measured("ratio", float64(numerator.Count)/float64(denominator.Count)), nil
}

// CounterSample requires an explicit source epoch. A process restart with a
// larger counter cannot be mistaken for a continuous delta.
type CounterSample struct {
	Point    MetricPoint `json:"point"`
	Epoch    string      `json:"epoch"`
	Complete bool        `json:"complete"`
}

func EvaluateCounter(operation string, before, after CounterSample) (DerivedValue, error) {
	if operation != "delta" && operation != "rate" {
		return DerivedValue{}, errors.New("choose counter delta or rate")
	}
	for _, sample := range []CounterSample{before, after} {
		if err := sample.Point.Validate(); err != nil {
			return DerivedValue{}, err
		}
		if sample.Point.Kind != KindCounter {
			return DerivedValue{}, errors.New("counter operation requires counter measurements")
		}
	}
	unit := before.Point.Unit
	if operation == "rate" {
		switch unit {
		case "count", "bytes":
			unit += "/s"
		default:
			return unscored(unit, "unsupported_counter_rate_unit"), nil
		}
	}
	if !sameSeries(before.Point, after.Point) {
		return unscored(unit, "incompatible_counter_series"), nil
	}
	semantics := semanticsOf(before.Point)
	if err := semantics.Validate(); err != nil {
		return unscored(unit, "unknown_measurement_semantics"), nil
	}
	if !before.Complete || !after.Complete || before.Point.Quality != QualityValid || after.Point.Quality != QualityValid {
		return unscored(unit, "incomplete_evidence"), nil
	}
	if strings.TrimSpace(before.Epoch) == "" || len(before.Epoch) > maxMetricIdentityBytes || strings.ContainsRune(before.Epoch, 0) || before.Epoch != after.Epoch {
		return unscored(unit, "counter_epoch_changed_or_unknown"), nil
	}
	elapsed := after.Point.TimestampUTC.Sub(before.Point.TimestampUTC)
	if elapsed <= 0 || !before.Point.TimestampUTC.Add(elapsed).Equal(after.Point.TimestampUTC) {
		return unscored(unit, "invalid_elapsed_window"), nil
	}
	if *before.Point.Value < 0 || *after.Point.Value < 0 || *before.Point.Value > float64(maxExactCount) || *after.Point.Value > float64(maxExactCount) {
		return unscored(unit, "invalid_counter_range"), nil
	}
	if (before.Point.Unit == "count" || before.Point.Unit == "bytes") && (math.Trunc(*before.Point.Value) != *before.Point.Value || math.Trunc(*after.Point.Value) != *after.Point.Value) {
		return unscored(unit, "nonintegral_counter"), nil
	}
	delta := *after.Point.Value - *before.Point.Value
	if delta < 0 {
		return unscored(unit, "counter_reset"), nil
	}
	if operation == "rate" {
		delta /= elapsed.Seconds()
	}
	return measured(unit, delta), nil
}

// EvaluateBaselineDelta requires the same metric identity, population, kind,
// version and normalized unit. Percentage change uses abs(baseline) as the
// denominator so the sign consistently describes the direction of change.
func EvaluateBaselineDelta(current, baseline MetricPoint, percentage bool) (DerivedValue, error) {
	for _, point := range []MetricPoint{current, baseline} {
		if err := point.Validate(); err != nil {
			return DerivedValue{}, err
		}
		if point.Kind != KindGauge && point.Kind != KindObservation {
			return DerivedValue{}, errors.New("baseline deltas require scalar measurements")
		}
	}
	unit := current.Unit
	if percentage {
		unit = "percent"
	}
	if !sameMetric(current, baseline) {
		return unscored(unit, "incompatible_measurement"), nil
	}
	semantics := semanticsOf(current)
	if err := semantics.Validate(); err != nil {
		return unscored(unit, "unknown_measurement_semantics"), nil
	}
	if current.Quality != QualityValid || baseline.Quality != QualityValid {
		return unscored(unit, "incomplete_evidence"), nil
	}
	delta := *current.Value - *baseline.Value
	if percentage {
		if *baseline.Value == 0 {
			return unscored(unit, "zero_denominator"), nil
		}
		// Normalize before differencing to avoid avoidable finite subtraction overflow.
		delta = (*current.Value/math.Abs(*baseline.Value) - *baseline.Value/math.Abs(*baseline.Value)) * 100
	}
	return measured(unit, delta), nil
}

type MovingStatistic struct {
	TimestampUTC    time.Time `json:"timestamp_utc"`
	Mean            *float64  `json:"mean,omitempty"`
	StdDev          *float64  `json:"stddev,omitempty"`
	ValidBuckets    int       `json:"valid_buckets"`
	ExpectedBuckets int       `json:"expected_buckets"`
	Coverage        float64   `json:"coverage"`
	Reason          string    `json:"reason,omitempty"`
}

// MovingStatistics evaluates complete trailing elapsed-time windows over one
// already bucketed scalar series. Missing buckets participate in coverage,
// never as zero. StdDev is population standard deviation of eligible buckets.
// At most 10,000 outputs and 1,000 buckets/window bound memory and work.
func MovingStatistics(points []MetricPoint, bucketSeconds, windowBuckets int, minimumCoverage float64) ([]MovingStatistic, error) {
	if bucketSeconds < 1 || bucketSeconds > 86400 || windowBuckets < 2 || windowBuckets > MaxMovingWindowBuckets || minimumCoverage <= 0 || minimumCoverage > 1 || math.IsNaN(minimumCoverage) {
		return nil, errors.New("invalid bounded moving window or coverage")
	}
	if err := scalarSeries(points); err != nil {
		return nil, err
	}
	if len(points) == 0 {
		return []MovingStatistic{}, nil
	}
	for _, point := range points {
		semantics := semanticsOf(point)
		if err := semantics.Validate(); err != nil {
			return nil, err
		}
	}
	ordered := append([]MetricPoint(nil), points...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].TimestampUTC.Before(ordered[j].TimestampUTC) })
	step := time.Duration(bucketSeconds) * time.Second
	span := ordered[len(ordered)-1].TimestampUTC.Sub(ordered[0].TimestampUTC)
	if span > 366*24*time.Hour || !ordered[0].TimestampUTC.Add(span).Equal(ordered[len(ordered)-1].TimestampUTC) || span/step >= MaxSeriesPoints {
		return nil, errors.New("moving series exceeds bounded elapsed window")
	}
	n := int(span/step) + 1
	values := make([]*float64, n)
	for _, point := range ordered {
		offset := point.TimestampUTC.Sub(ordered[0].TimestampUTC)
		if offset%step != 0 {
			return nil, errors.New("moving statistics require aligned buckets")
		}
		if point.Quality == QualityValid {
			values[int(offset/step)] = point.Value
		}
	}
	result := make([]MovingStatistic, 0, n)
	for i := range values {
		stat := MovingStatistic{TimestampUTC: ordered[0].TimestampUTC.Add(time.Duration(i) * step), ExpectedBuckets: windowBuckets}
		start := max(0, i-windowBuckets+1)
		scale := 0.0
		for _, value := range values[start : i+1] {
			if value != nil {
				stat.ValidBuckets++
				scale = math.Max(scale, math.Abs(*value))
			}
		}
		stat.Coverage = float64(stat.ValidBuckets) / float64(windowBuckets)
		switch {
		case i+1 < windowBuckets:
			stat.Reason = "insufficient_window"
		case stat.ValidBuckets < 2:
			stat.Reason = "insufficient_valid_buckets"
		case stat.Coverage < minimumCoverage:
			stat.Reason = "insufficient_coverage"
		default:
			mean, m2, count := 0.0, 0.0, 0
			for _, value := range values[start : i+1] {
				if value == nil {
					continue
				}
				count++
				x := 0.0
				if scale > 0 {
					x = *value / scale
				}
				delta := x - mean
				mean += delta / float64(count)
				m2 += delta * (x - mean)
			}
			mean *= scale
			stddev := math.Sqrt(math.Max(0, m2/float64(count))) * scale
			if math.IsNaN(mean) || math.IsInf(mean, 0) || math.IsNaN(stddev) || math.IsInf(stddev, 0) {
				stat.Reason = "numeric_range"
			} else {
				stat.Mean, stat.StdDev = &mean, &stddev
			}
		}
		result = append(result, stat)
	}
	return result, nil
}

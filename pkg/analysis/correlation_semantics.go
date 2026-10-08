package analysis

import (
	"sort"
	"strings"
	"time"
)

// Correlation completeness (B6). A lagged correlation associates two series
// that each keep their own semantics: unit, kind, the source semantics the
// buckets came from, population, and the statistic or conversion that made
// them scalar (a counter's per-second rate, a summary's declared statistic,
// never a summary percentile treated as raw observations). The evidence kept
// out of the pairing is counted by reason, stale points and counter resets
// included, and a lag with no coefficient says why.

// Correlation coefficient unavailability reasons.
const (
	CorrelationInsufficientPairs = "insufficient_pairs"
	CorrelationConstantSeries    = "constant_series"
	CorrelationUnscorable        = "unscorable"
)

// CorrelationLagConvention states the sign of a lag.
const CorrelationLagConvention = "lag_seconds > 0 pairs each left bucket with the right series lag seconds later: the right series follows the left."

// Correlation scalar conversions.
const (
	ConversionCounterRate              = "counter_rate"
	ConversionDeclaredSummaryStatistic = "declared_summary_statistic"
	ConversionObservationPercentile    = "observation_percentile"
	// ConversionHistogramPercentileEstimate: a percentile interpolated from
	// histogram buckets, an estimate rather than an observed value.
	ConversionHistogramPercentileEstimate = "histogram_percentile_estimate"
)

// CorrelationSide is one correlated series' semantics and evidence.
type CorrelationSide struct {
	Metric     string `json:"metric"`
	Unit       string `json:"unit,omitempty"`
	Kind       string `json:"kind,omitempty"`
	InputKind  string `json:"input_kind,omitempty"`
	Population string `json:"population,omitempty"`
	// Statistic is the selected aggregation; Conversion says how a
	// non-scalar source became scalar buckets.
	Statistic  string `json:"statistic"`
	Conversion string `json:"conversion,omitempty"`
	// Derivation names the catalog operation the values are, when the series
	// is derived (a moving standard deviation is not its raw statistic).
	Derivation string `json:"derivation,omitempty"`
	Points     int    `json:"points"`
	Valid      int    `json:"valid"`
	// Excluded counts buckets kept out of every pairing, by reason.
	Excluded map[string]int `json:"excluded,omitempty"`
	// ExcludedSamples counts the raw samples bucketing could not use, by
	// reason (stale, clock_skewed, dropped, counter_reset, ...). A side read
	// from an imported dataset is aggregated by its dataset query and has
	// none.
	ExcludedSamples map[string]int `json:"excluded_samples,omitempty"`
}

// CorrelationSemantics describes both sides of a correlation.
type CorrelationSemantics struct {
	Left  CorrelationSide `json:"left"`
	Right CorrelationSide `json:"right"`
	// SameUnits is true only when both series share a declared unit and
	// kind, and the same population where both declare one; otherwise their
	// values are associated, never compared directly.
	SameUnits bool `json:"same_units"`
	// UnitsUndeclared: a side has no evidence or no declared unit, so
	// whether the units match is unknown (not that they differ).
	UnitsUndeclared bool   `json:"units_undeclared,omitempty"`
	LagConvention   string `json:"lag_convention"`
}

// DescribeCorrelationSide reports a series' semantics and exclusions.
func DescribeCorrelationSide(metric, statistic string, points []MetricPoint) CorrelationSide {
	side := CorrelationSide{Metric: metric, Statistic: statistic, Points: len(points)}
	for _, point := range points {
		if side.Unit == "" {
			side.Unit = point.Unit
		}
		if side.Kind == "" {
			side.Kind, side.InputKind, side.Population = point.Kind, point.InputKind, point.Population
			if side.Population == "" {
				// Archived transaction points carry it as a dimension.
				side.Population = point.Dimensions["population"]
			}
		}
		if point.Quality == QualityValid && point.Value != nil {
			side.Valid++
			continue
		}
		reason := point.Quality
		if point.MissingReason != "" {
			reason = point.MissingReason
		}
		if reason == "" || reason == QualityValid {
			reason = QualityMissing
		}
		if side.Excluded == nil {
			side.Excluded = map[string]int{}
		}
		side.Excluded[reason]++
	}
	switch {
	case side.InputKind == KindCounter || side.Kind == KindCounter:
		side.Conversion = ConversionCounterRate
	case side.InputKind == KindSummary || side.Kind == KindSummary:
		side.Conversion = ConversionDeclaredSummaryStatistic
	case (side.InputKind == KindObservation || side.Kind == KindObservation) && isPercentileStatistic(statistic):
		side.Conversion = ConversionObservationPercentile
	case (side.InputKind == KindHistogram || side.Kind == KindHistogram) && isPercentileStatistic(statistic):
		side.Conversion = ConversionHistogramPercentileEstimate
	}
	return side
}

func isPercentileStatistic(statistic string) bool {
	switch statistic {
	case "p50", "p75", "p90", "p95", "p99", "p999":
		return true
	}
	return false
}

// DescribeCorrelation describes both sides of a correlation.
func DescribeCorrelation(leftMetric, leftStatistic string, left []MetricPoint, rightMetric, rightStatistic string, right []MetricPoint) CorrelationSemantics {
	semantics := CorrelationSemantics{Left: DescribeCorrelationSide(leftMetric, leftStatistic, left), Right: DescribeCorrelationSide(rightMetric, rightStatistic, right), LagConvention: CorrelationLagConvention}
	l, r := semantics.Left, semantics.Right
	semantics.UnitsUndeclared = l.Points == 0 || r.Points == 0 || l.Unit == "" || r.Unit == ""
	samePopulation := l.Population == "" || r.Population == "" || l.Population == r.Population
	semantics.SameUnits = !semantics.UnitsUndeclared && l.Unit == r.Unit && l.Kind == r.Kind && l.InputKind == r.InputKind && samePopulation
	return semantics
}

// RawSampleExclusions counts the raw samples a bucketing cannot use as valid
// values, by reason: a quality other than valid (stale, clock_skewed,
// dropped, ...) and, for a raw counter series, each instant carrying a
// second reading (duplicate_counter_instant) and each decrease between
// consecutive readings (counter_reset). A gauge derived from a counter
// (InputKind counter) is already a per-interval value whose decreases are not
// resets. Bucketing records only that a bucket is missing, so the reasons
// are counted before it.
func RawSampleExclusions(points []MetricPoint) map[string]int {
	out := map[string]int{}
	type sample struct {
		at    time.Time
		value float64
	}
	counters := map[string][]sample{}
	for _, point := range points {
		if point.Quality != "" && point.Quality != QualityValid {
			reason := point.Quality
			if point.MissingReason != "" {
				reason = point.MissingReason
			}
			out[reason]++
			continue
		}
		if point.Kind == KindCounter && point.Value != nil {
			key := rawSeriesKey(point)
			counters[key] = append(counters[key], sample{point.TimestampUTC, *point.Value})
		}
	}
	for _, series := range counters {
		sort.SliceStable(series, func(i, j int) bool { return series[i].at.Before(series[j].at) })
		for i := 1; i < len(series); i++ {
			switch {
			case series[i].at.Equal(series[i-1].at):
				out[MissingDuplicateCounterInstant]++
			case series[i].value < series[i-1].value:
				out[MissingCounterReset]++
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func rawSeriesKey(point MetricPoint) string {
	keys := make([]string, 0, len(point.Dimensions))
	for key := range point.Dimensions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := []string{point.Source, point.Host, point.Service, point.MetricName}
	for _, key := range keys {
		parts = append(parts, key+"="+point.Dimensions[key])
	}
	return strings.Join(parts, "\x00")
}

// correlationUnavailable says why paired buckets gave no coefficient.
func correlationUnavailable(pairs []AlignedPair) string {
	if len(pairs) < 3 {
		return CorrelationInsufficientPairs
	}
	constant := func(value func(AlignedPair) *float64) bool {
		values := make([]float64, 0, len(pairs))
		for _, pair := range pairs {
			if v := value(pair); v != nil {
				values = append(values, *v)
			}
		}
		if len(values) == 0 {
			return true
		}
		sort.Float64s(values)
		return values[0] == values[len(values)-1]
	}
	if constant(func(p AlignedPair) *float64 { return p.Left.Value }) || constant(func(p AlignedPair) *float64 { return p.Right.Value }) {
		return CorrelationConstantSeries
	}
	return CorrelationUnscorable
}

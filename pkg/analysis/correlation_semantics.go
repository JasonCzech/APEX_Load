package analysis

import "sort"

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
	Points     int    `json:"points"`
	Valid      int    `json:"valid"`
	// Excluded counts buckets kept out of every pairing, by reason (stale,
	// clock_skewed, dropped, missing, counter_reset, ...).
	Excluded map[string]int `json:"excluded,omitempty"`
}

// CorrelationSemantics describes both sides of a correlation.
type CorrelationSemantics struct {
	Left  CorrelationSide `json:"left"`
	Right CorrelationSide `json:"right"`
	// SameUnits is true only when both series share a declared unit and
	// kind; otherwise their values are associated, never compared directly.
	SameUnits     bool   `json:"same_units"`
	LagConvention string `json:"lag_convention"`
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
	semantics.SameUnits = semantics.Left.Unit != "" && semantics.Left.Unit == semantics.Right.Unit && semantics.Left.Kind == semantics.Right.Kind && semantics.Left.InputKind == semantics.Right.InputKind
	return semantics
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

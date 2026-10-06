package analysis

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
)

// A histogram measure imports bucketed observation counts with declared upper
// bounds. Counts are stored per bin and per window in a separate distribution
// artifact. Series merge only identical bound sets, and percentiles are
// estimated by interpolation inside the bin that holds the rank, labelled
// histogram_interpolated.v1 with that bin's edges. They are never exact
// quantiles and never mix with raw observations.

const (
	// DistributionSchemaVersion is the schema of normalized histogram rows.
	DistributionSchemaVersion = "apexload.metric_distribution.v1"
	MaxHistogramBounds        = 128

	HistogramPerBin       = "per_bin"       // each count is its own bin
	HistogramLeCumulative = "le_cumulative" // each count includes all lower bins (Prometheus le)
	HistogramDelta        = "delta"         // each row counts one window
	HistogramCumulative   = "cumulative"    // each row counts since a process start

	MissingHistogramCounterReset   = "histogram_counter_reset"
	MissingHistogramOverflowBin    = "histogram_percentile_in_overflow_bin"
	MissingHistogramNoObservations = "histogram_no_observations"
	MissingPartialHistogramWindow  = "partial_histogram_window"
	MissingDuplicateHistogramRow   = "duplicate_histogram_window"
)

var histogramMissingReasons = []string{MissingHistogramCounterReset, MissingHistogramOverflowBin, MissingHistogramNoObservations, MissingPartialHistogramWindow, MissingDuplicateHistogramRow}

// HistogramStatistics are the aggregations a histogram series answers.
var HistogramStatistics = []string{"count", "p50", "p75", "p90", "p95", "p99", "p999"}

var histogramQuantiles = map[string]float64{"p50": .5, "p75": .75, "p90": .9, "p95": .95, "p99": .99, "p999": .999}

// A declared bound is plain decimal text, or "+Inf" for the last bound.
var histogramBoundPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][-+]?[0-9]+)?$`)

// ParquetHistogram declares one histogram measure. Wide tables give each
// bound but the last its own count column (CountColumns) and the measure value
// is the last bound's count. Long tables have one row per bound: BoundColumn
// names the row's upper bound and the measure value is its count. Rows of one
// histogram share timestamp, identity and metric name.
type ParquetHistogram struct {
	Bounds        []string            `json:"bounds"`
	BucketCounts  string              `json:"bucket_counts"`
	Temporality   string              `json:"temporality"`
	WindowSeconds int                 `json:"window_seconds,omitempty"`
	BoundColumn   ParquetColumnPath   `json:"bound_column,omitempty"`
	CountColumns  []ParquetColumnPath `json:"count_columns,omitempty"`
}

// ParseBounds returns the declared upper bounds as numbers, in the measure's
// source unit.
func (h ParquetHistogram) ParseBounds() ([]float64, error) {
	if len(h.Bounds) < 1 || len(h.Bounds) > MaxHistogramBounds {
		return nil, fmt.Errorf("a histogram declares 1-%d upper bounds", MaxHistogramBounds)
	}
	out := make([]float64, len(h.Bounds))
	for i, text := range h.Bounds {
		if text == "+Inf" && i == len(h.Bounds)-1 {
			out[i] = math.Inf(1)
		} else if !histogramBoundPattern.MatchString(text) {
			return nil, errors.New("histogram bounds are decimal numbers; only the last may be +Inf")
		} else if value, err := strconv.ParseFloat(text, 64); err != nil || math.IsInf(value, 0) {
			return nil, errors.New("histogram bound out of range")
		} else {
			out[i] = value
		}
		if i > 0 && out[i] <= out[i-1] {
			return nil, errors.New("histogram bounds must strictly increase")
		}
	}
	return out, nil
}

// Paths returns every source column the histogram reads beyond the value.
func (h *ParquetHistogram) Paths() []ParquetColumnPath {
	if h == nil {
		return nil
	}
	paths := append([]ParquetColumnPath{}, h.CountColumns...)
	if len(h.BoundColumn) != 0 {
		paths = append(paths, h.BoundColumn)
	}
	return paths
}

func (m ParquetMeasure) validateHistogram(check func(ParquetColumnPath) error) error {
	if m.Kind != KindHistogram {
		if m.Histogram != nil {
			return errors.New("histogram declarations apply only to histogram measures")
		}
		return nil
	}
	h := m.Histogram
	if h == nil {
		return errors.New("a histogram measure requires its bounds, count convention and temporality")
	}
	if _, err := h.ParseBounds(); err != nil {
		return err
	}
	if h.BucketCounts != HistogramPerBin && h.BucketCounts != HistogramLeCumulative {
		return errors.New("histogram bucket_counts must be per_bin or le_cumulative")
	}
	switch h.Temporality {
	case HistogramDelta:
		if h.WindowSeconds < 1 || h.WindowSeconds > MaxSummaryWindowSeconds {
			return errors.New("a delta histogram requires window_seconds of 1-86400")
		}
	case HistogramCumulative:
		// Windows are the intervals between consecutive samples.
		if h.WindowSeconds != 0 {
			return errors.New("a cumulative histogram takes its windows from consecutive samples")
		}
	default:
		return errors.New("histogram temporality must be delta or cumulative")
	}
	if len(h.BoundColumn) != 0 {
		if len(h.CountColumns) != 0 {
			return errors.New("choose a bound column (long table) or count columns (wide table)")
		}
	} else if len(h.CountColumns) != len(h.Bounds)-1 {
		return errors.New("a wide histogram maps every bound but the last to a count column; the value is the last bound's count")
	}
	seen := [][]string{m.Value}
	if len(m.NameColumn) != 0 {
		seen = append(seen, m.NameColumn)
	}
	for _, path := range h.Paths() {
		for _, other := range seen {
			if slicesEqual(path, other) {
				return errors.New("histogram columns must be distinct")
			}
		}
		if err := check(path); err != nil {
			return err
		}
		seen = append(seen, path)
	}
	return nil
}

// HistogramDeclaration is the declared shape of a fixed-name histogram.
type HistogramDeclaration struct {
	WindowSeconds int // zero for cumulative histograms
	Bounds        []string
}

// HistogramDeclarationFor returns the fixed-name histogram measure called
// metric, if any.
func (m ParquetMapping) HistogramDeclarationFor(metric string) (HistogramDeclaration, bool) {
	for _, measure := range m.Measures {
		if measure.Kind == KindHistogram && measure.Histogram != nil && len(measure.NameColumn) == 0 && measure.Name == metric {
			return HistogramDeclaration{WindowSeconds: measure.Histogram.WindowSeconds, Bounds: slices.Clone(measure.Histogram.Bounds)}, true
		}
	}
	return HistogramDeclaration{}, false
}

// ValidateHistogramAggregation checks a series request against a histogram:
// a count or an interpolated percentile, over buckets holding whole windows
// when the windows are fixed.
func ValidateHistogramAggregation(windowSeconds, bucketSeconds int, aggregation string) error {
	if !slices.Contains(HistogramStatistics, aggregation) {
		return errors.New("a histogram series answers count or an interpolated percentile (p50, p75, p90, p95, p99, p999)")
	}
	if windowSeconds < 0 || windowSeconds > MaxSummaryWindowSeconds || bucketSeconds < 1 {
		return errors.New("invalid histogram window or bucket")
	}
	if windowSeconds > 0 && (bucketSeconds < windowSeconds || bucketSeconds%windowSeconds != 0) {
		return fmt.Errorf("histogram buckets must be whole multiples of the %d-second source window", windowSeconds)
	}
	return nil
}

func histogramSourceAlgorithm(windowSeconds int) string {
	if windowSeconds == 0 {
		return "source_histogram_cumulative.v1"
	}
	return "source_histogram_delta.v1"
}

// HistogramBucketDerivation is the lineage of a bucketed histogram series.
// Its first step records the source window (zero: sample intervals of a
// cumulative histogram).
func HistogramBucketDerivation(windowSeconds, bucketSeconds int, aggregation string) ([]DerivationStep, error) {
	if err := ValidateHistogramAggregation(windowSeconds, bucketSeconds, aggregation); err != nil {
		return nil, err
	}
	algorithm := "histogram_interpolated.v1"
	if aggregation == "count" {
		algorithm = "histogram_count_sum.v1"
	}
	steps := []DerivationStep{
		{Operation: "count", Algorithm: histogramSourceAlgorithm(windowSeconds), BucketSeconds: windowSeconds},
		{Operation: aggregation, Algorithm: algorithm, BucketSeconds: bucketSeconds},
	}
	return steps, validateDerivation(steps, false)
}

// HistogramWindowFromDerivation recovers the source window of a histogram
// series lineage.
func HistogramWindowFromDerivation(steps []DerivationStep) (windowSeconds int, ok bool) {
	if len(steps) != 2 || steps[0].Operation != "count" || steps[0].Algorithm != histogramSourceAlgorithm(steps[0].BucketSeconds) {
		return 0, false
	}
	return steps[0].BucketSeconds, true
}

// EstimateBin is the histogram bin holding an interpolated percentile: the
// true quantile lies within [Lower, Upper].
type EstimateBin struct {
	Lower float64 `json:"lower"`
	Upper float64 `json:"upper"`
}

// HistogramValue reduces one bucket's merged per-bin counts. Counts sum;
// percentiles interpolate linearly inside the bin holding the rank. The first
// bin starts at zero when its bound is positive (as Prometheus does), and a
// rank in an unbounded last bin has no estimate.
func HistogramValue(bounds, counts []float64, aggregation string) (value float64, bin *EstimateBin, missingReason string, err error) {
	if len(bounds) == 0 || len(bounds) != len(counts) {
		return 0, nil, "", errors.New("histogram bounds and counts differ in length")
	}
	total := 0.0
	for _, count := range counts {
		if count < 0 || math.IsNaN(count) || math.IsInf(count, 0) {
			return 0, nil, "", errors.New("invalid histogram bin count")
		}
		total += count
	}
	if aggregation == "count" {
		return total, nil, "", nil
	}
	q, ok := histogramQuantiles[aggregation]
	if !ok {
		return 0, nil, "", errors.New("unsupported histogram aggregation")
	}
	if total == 0 {
		return 0, nil, MissingHistogramNoObservations, nil
	}
	rank, before := q*total, 0.0
	for i, count := range counts {
		if count == 0 || before+count < rank {
			before += count
			continue
		}
		upper := bounds[i]
		if math.IsInf(upper, 1) {
			return 0, nil, MissingHistogramOverflowBin, nil
		}
		lower := 0.0
		if i > 0 {
			lower = bounds[i-1]
		} else if upper <= 0 {
			return upper, &EstimateBin{Lower: upper, Upper: upper}, "", nil
		}
		return lower + (upper-lower)*(rank-before)/count, &EstimateBin{Lower: lower, Upper: upper}, "", nil
	}
	return 0, nil, "", errors.New("histogram rank beyond its counts")
}

// validPointMissingReason admits the missing reasons of derived series.
func validPointMissingReason(inputKind, reason string) bool {
	switch inputKind {
	case KindSummary:
		return validMissingReason(reason)
	case KindHistogram:
		return slices.Contains(histogramMissingReasons, reason)
	}
	return false
}

package analysis

import (
	"errors"
	"fmt"
	"slices"
)

// A summary measurement is a statistic the source already computed over a
// fixed window: CloudWatch-style averages, exported percentiles, per-minute
// counts. It is never a raw observation. A series built from one reports the
// declared statistic and only re-buckets it where that is exact.

// SummaryStatistics are the statistics a summary measure may declare.
var SummaryStatistics = []string{"avg", "min", "max", "sum", "count", "p50", "p75", "p90", "p95", "p99", "p999"}

const (
	// MaxSummaryWindowSeconds bounds a declared source window to one day.
	MaxSummaryWindowSeconds = 86400

	// Missing-bucket reasons for summary series. A missing bucket is unscored.
	MissingPercentileNotMergeable = "precomputed_percentile_not_mergeable"
	MissingAverageWithoutCounts   = "summary_average_without_sample_counts"
	MissingPartialSummaryWindow   = "partial_summary_window"
	MissingDuplicateSummaryWindow = "duplicate_summary_window"
)

var summaryMissingReasons = []string{MissingPercentileNotMergeable, MissingAverageWithoutCounts, MissingPartialSummaryWindow, MissingDuplicateSummaryWindow}

func ValidSummaryStatistic(statistic string) bool {
	return slices.Contains(SummaryStatistics, statistic)
}

// SummaryPercentile reports whether a statistic is a precomputed quantile,
// which can be displayed but never merged with another window.
func SummaryPercentile(statistic string) bool {
	return len(statistic) > 1 && statistic[0] == 'p'
}

func validMissingReason(reason string) bool {
	return reason == "" || slices.Contains(summaryMissingReasons, reason)
}

// ErrSummaryStatisticUnknown is returned for summary measures imported
// without a declared statistic. Their values stay readable in the dataset but
// cannot be aggregated, compared or scored.
var ErrSummaryStatisticUnknown = errors.New("summary measure has no declared statistic and is display-only; re-import it with statistic and window_seconds")

// ValidateSummaryAggregation checks a series request against a declared
// summary. The aggregation selects the statistic: a p95 summary answers only
// p95. Buckets must hold whole source windows.
func ValidateSummaryAggregation(statistic string, windowSeconds, bucketSeconds int, aggregation string) error {
	if statistic == "" {
		return ErrSummaryStatisticUnknown
	}
	if !ValidSummaryStatistic(statistic) || windowSeconds < 1 || windowSeconds > MaxSummaryWindowSeconds {
		return errors.New("invalid summary statistic or window")
	}
	if aggregation != statistic {
		return fmt.Errorf("a summary series reports its declared statistic; request aggregation %q", statistic)
	}
	if bucketSeconds < windowSeconds || bucketSeconds%windowSeconds != 0 {
		return fmt.Errorf("summary buckets must be whole multiples of the %d-second source window", windowSeconds)
	}
	return nil
}

// SummaryRebucketAlgorithm names how whole source windows combine into one
// bucket. Percentiles never combine: a bucket holding more than one window is
// missing with MissingPercentileNotMergeable.
func SummaryRebucketAlgorithm(statistic string) string {
	switch {
	case statistic == "count" || statistic == "sum":
		return "summary_window_sum.v1"
	case statistic == "min" || statistic == "max":
		return "summary_window_extrema.v1"
	case statistic == "avg":
		return "summary_window_weighted_mean.v1"
	default:
		return "summary_single_window.v1"
	}
}

// SummaryBucketDerivation is the lineage of a bucketed summary series: the
// source statistic over its window, then the re-bucketing step.
func SummaryBucketDerivation(statistic string, windowSeconds, bucketSeconds int) ([]DerivationStep, error) {
	if err := ValidateSummaryAggregation(statistic, windowSeconds, bucketSeconds, statistic); err != nil {
		return nil, err
	}
	steps := []DerivationStep{
		{Operation: statistic, Algorithm: "source_summary.v1", BucketSeconds: windowSeconds},
		{Operation: statistic, Algorithm: SummaryRebucketAlgorithm(statistic), BucketSeconds: bucketSeconds},
	}
	return steps, validateDerivation(steps, false)
}

// SummaryWindowFromDerivation recovers the declared source window from a
// summary series lineage, so a receiver can recompute and compare it.
func SummaryWindowFromDerivation(steps []DerivationStep) (statistic string, windowSeconds int, ok bool) {
	if len(steps) != 2 || steps[0].Algorithm != "source_summary.v1" {
		return "", 0, false
	}
	return steps[0].Operation, steps[0].BucketSeconds, true
}

func (m ParquetMeasure) declaredSummary() bool {
	return m.Kind == KindSummary && m.Statistic != ""
}

// validateSummaryFields checks the summary declaration of one measure. The
// fields are meaningful only on summaries; a summary without a statistic is a
// legacy display-only measure.
func (m ParquetMeasure) validateSummaryFields(check func(ParquetColumnPath) error) error {
	if m.Kind != KindSummary {
		if m.Statistic != "" || m.WindowSeconds != 0 || len(m.SampleCountColumn) != 0 || len(m.SampleSumColumn) != 0 {
			return errors.New("statistic, window and sample columns apply only to summary measures")
		}
		return nil
	}
	if m.Statistic == "" {
		if m.WindowSeconds != 0 || len(m.SampleCountColumn) != 0 || len(m.SampleSumColumn) != 0 {
			return errors.New("a summary window or sample column requires a declared statistic")
		}
		return nil
	}
	if !ValidSummaryStatistic(m.Statistic) {
		return errors.New("unsupported summary statistic")
	}
	if m.WindowSeconds < 1 || m.WindowSeconds > MaxSummaryWindowSeconds {
		return errors.New("a summary statistic requires window_seconds of 1-86400")
	}
	if unit, _, err := NormalizeUnit(m.Unit); m.Statistic == "count" && (err != nil || unit != "count") {
		return errors.New("a count summary requires a count unit")
	}
	if len(m.SampleSumColumn) != 0 && len(m.SampleCountColumn) == 0 {
		return errors.New("a summary sample sum requires its sample count")
	}
	for _, path := range []ParquetColumnPath{m.SampleCountColumn, m.SampleSumColumn} {
		if len(path) == 0 {
			continue
		}
		if slicesEqual(path, m.Value) || slicesEqual(path, m.NameColumn) {
			return errors.New("summary sample columns must differ from the value and name columns")
		}
		if err := check(path); err != nil {
			return err
		}
	}
	if len(m.SampleCountColumn) != 0 && slicesEqual(m.SampleCountColumn, m.SampleSumColumn) {
		return errors.New("summary sample count and sum must be different columns")
	}
	return nil
}

// RequireDeclaredSummaries refuses new summary measures that omit their
// statistic. Stored legacy mappings stay valid and display-only; only a new
// mapping admission calls this.
func (m ParquetMapping) RequireDeclaredSummaries() error {
	for _, measure := range m.Measures {
		if measure.Kind == KindSummary && measure.Statistic == "" {
			return errors.New("summary measures must declare statistic and window_seconds")
		}
	}
	return nil
}

// SummaryDeclaration is the declared statistic and window for one fixed
// metric name, used to check a series request before staging any data.
type SummaryDeclaration struct {
	Statistic     string `json:"statistic"`
	WindowSeconds int    `json:"window_seconds"`
}

// SummaryDeclarations returns the summary declarations of the constant-name
// measures called metric. Long-table measures name metrics from data and are
// checked by the query engine instead. legacy reports a summary measure with
// no statistic.
func (m ParquetMapping) SummaryDeclarations(metric string) (declared []SummaryDeclaration, legacy bool) {
	for _, measure := range m.Measures {
		if measure.Kind != KindSummary || len(measure.NameColumn) != 0 || measure.Name != metric {
			continue
		}
		if measure.Statistic == "" {
			legacy = true
			continue
		}
		declared = append(declared, SummaryDeclaration{Statistic: measure.Statistic, WindowSeconds: measure.WindowSeconds})
	}
	return declared, legacy
}

// RejectionInvalidSummarySample is the rejection reason of a summary row whose
// timestamp, name and value are valid but whose sample count or sample sum is
// not (negative, non-numeric or non-finite).
const RejectionInvalidSummarySample = "invalid_summary_sample"

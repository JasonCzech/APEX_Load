package analysis

import (
	"errors"
	"math"
	"time"

	"github.com/jasonczech/apexload/pkg/monitorprovider"
)

// CorrelationScore retains coverage and lag alongside an optional coefficient.
// Positive lag means the right-hand series follows the left-hand series.
type CorrelationScore struct {
	LagSeconds      int      `json:"lag_seconds"`
	Pairs           int      `json:"pairs"`
	ExpectedBuckets int      `json:"expected_buckets"`
	Coverage        float64  `json:"coverage"`
	Coefficient     *float64 `json:"coefficient,omitempty"`
	// Unavailable says why Coefficient is absent: insufficient_pairs or
	// constant_series.
	Unavailable string `json:"unavailable,omitempty"`
}

// LaggedCorrelation uses exact, equally spaced UTC buckets. The caller must
// authorize associations before calling; Align still verifies those identities.
// Gaps are excluded, never replaced with zeros. Scores are associations only.
func LaggedCorrelation(left, right []MetricPoint, from, to time.Time, bucketSeconds, maxLagSeconds int) ([]CorrelationScore, error) {
	if bucketSeconds < 1 || maxLagSeconds < 0 || maxLagSeconds > 3600 || maxLagSeconds%bucketSeconds != 0 || 2*(maxLagSeconds/bucketSeconds)+1 > 121 || !to.After(from) || to.Sub(from) > 366*24*time.Hour {
		return nil, errors.New("invalid correlation window or lag: at most 121 shifts and one hour lag")
	}
	if err := scalarSeries(left); err != nil {
		return nil, err
	}
	if err := scalarSeries(right); err != nil {
		return nil, err
	}
	bucket := time.Duration(bucketSeconds) * time.Second
	n := int(math.Ceil(to.Sub(from).Seconds() / float64(bucketSeconds)))
	if n > MaxSeriesPoints {
		return nil, errors.New("correlation exceeds bucket limit")
	}
	for _, series := range [][]MetricPoint{left, right} {
		for _, p := range series {
			if p.TimestampUTC.Before(from) || !p.TimestampUTC.Before(to) || p.TimestampUTC.Sub(from)%bucket != 0 {
				return nil, errors.New("correlation requires aligned buckets inside the requested window")
			}
		}
	}
	result := make([]CorrelationScore, 0, 2*(maxLagSeconds/bucketSeconds)+1)
	for lag := -maxLagSeconds; lag <= maxLagSeconds; lag += bucketSeconds {
		shifted := make([]MetricPoint, 0, len(right))
		for _, p := range right {
			p.TimestampUTC = p.TimestampUTC.Add(-time.Duration(lag) * time.Second)
			if p.Provider != nil && p.Provider.Name == monitorprovider.ProviderPrometheus && p.Provider.Mode == "analysis_bucket" {
				// Shift only the authorized calculation copy. Its explicit display
				// interval follows that copy's timestamp; response provenance stays
				// at the original source-aligned interval.
				metadata := *p.Provider
				metadata.BucketStart = metadata.BucketStart.Add(-time.Duration(lag) * time.Second)
				metadata.BucketEnd = metadata.BucketEnd.Add(-time.Duration(lag) * time.Second)
				p.Provider = &metadata
			}
			if !p.TimestampUTC.Before(from) && p.TimestampUTC.Before(to) {
				shifted = append(shifted, p)
			}
		}
		pairs, err := Align(left, shifted, 0)
		if err != nil {
			return nil, err
		}
		expected := max(0, n-int(math.Abs(float64(lag/bucketSeconds))))
		score := CorrelationScore{LagSeconds: lag, Pairs: len(pairs), ExpectedBuckets: expected}
		if expected > 0 {
			score.Coverage = float64(len(pairs)) / float64(expected)
		}
		if value, valid := Pearson(pairs); valid {
			score.Coefficient = &value
		} else {
			score.Unavailable = correlationUnavailable(pairs)
		}
		result = append(result, score)
	}
	return result, nil
}

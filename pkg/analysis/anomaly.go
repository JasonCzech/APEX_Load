package analysis

import (
	"errors"
	"fmt"
	"math"
)

const DefaultAnomalyMinBuckets = 30
const DefaultAnomalySigma = 3.0

// AnomalyEvidence describes a qualified statistical deviation. The detector
// is advisory; it makes no SLA or causal assertion.
type AnomalyEvidence struct {
	MetricName      string  `json:"metric_name"`
	Current         float64 `json:"current"`
	BaselineMean    float64 `json:"baseline_mean"`
	BaselineStdDev  float64 `json:"baseline_stddev"`
	BaselineBuckets int     `json:"baseline_buckets"`
	ZScore          float64 `json:"z_score"`
	Threshold       float64 `json:"threshold"`
	Exceeded        bool    `json:"exceeded"`
}

// DetectAnomaly scores one value against at least 30 valid, comparable
// baseline buckets. A false result means the evidence cannot be scored.
func DetectAnomaly(current MetricPoint, baseline []MetricPoint) (AnomalyEvidence, bool, error) {
	if err := current.Validate(); err != nil {
		return AnomalyEvidence{}, false, err
	}
	if err := scalarSeries(baseline); err != nil {
		return AnomalyEvidence{}, false, err
	}
	if current.Kind != KindGauge {
		return AnomalyEvidence{}, false, errors.New("anomaly detection requires bucketed gauges")
	}
	if current.Quality != QualityValid {
		return AnomalyEvidence{}, false, nil
	}
	mean, m2, count := 0.0, 0.0, 0
	for _, point := range baseline {
		if err := point.Validate(); err != nil {
			return AnomalyEvidence{}, false, fmt.Errorf("baseline: %w", err)
		}
		if !sameMetric(point, current) {
			return AnomalyEvidence{}, false, errors.New("baseline contains an incompatible metric")
		}
		if !point.TimestampUTC.Before(current.TimestampUTC) {
			return AnomalyEvidence{}, false, errors.New("baseline buckets must precede current point")
		}
		if point.Quality != QualityValid {
			continue
		}
		count++
		delta := *point.Value - mean
		mean += delta / float64(count)
		m2 += delta * (*point.Value - mean)
	}
	if count < DefaultAnomalyMinBuckets {
		return AnomalyEvidence{}, false, nil
	}
	stddev := math.Sqrt(m2 / float64(count))
	if stddev <= 0 || math.IsNaN(stddev) || math.IsInf(stddev, 0) {
		return AnomalyEvidence{}, false, nil
	}
	z := (*current.Value - mean) / stddev
	if math.IsNaN(z) || math.IsInf(z, 0) {
		return AnomalyEvidence{}, false, nil
	}
	return AnomalyEvidence{
		MetricName: current.MetricName, Current: *current.Value, BaselineMean: mean,
		BaselineStdDev: stddev, BaselineBuckets: count, ZScore: z,
		Threshold: DefaultAnomalySigma, Exceeded: math.Abs(z) >= DefaultAnomalySigma,
	}, true, nil
}

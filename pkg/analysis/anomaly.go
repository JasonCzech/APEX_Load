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
	values := make([]float64, 0, len(baseline))
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
		if point.Quality == QualityValid {
			values = append(values, *point.Value)
		}
	}
	if len(values) < DefaultAnomalyMinBuckets {
		return AnomalyEvidence{}, false, nil
	}
	evidence, ok := ThreeSigmaScore(*current.Value, values, DefaultAnomalySigma)
	evidence.MetricName = current.MetricName
	return evidence, ok, nil
}

// ThreeSigmaScore scores a value against baseline values: the z-score
// against their mean and population standard deviation, exceeded at
// |z| >= sigma. A false result means no spread (or a non-finite score) to
// judge by; callers check the baseline's size.
func ThreeSigmaScore(current float64, baseline []float64, sigma float64) (AnomalyEvidence, bool) {
	mean, m2 := 0.0, 0.0
	for i, value := range baseline {
		delta := value - mean
		mean += delta / float64(i+1)
		m2 += delta * (value - mean)
	}
	count := len(baseline)
	if count == 0 {
		return AnomalyEvidence{}, false
	}
	stddev := math.Sqrt(m2 / float64(count))
	if stddev <= 0 || math.IsNaN(stddev) || math.IsInf(stddev, 0) {
		return AnomalyEvidence{}, false
	}
	z := (current - mean) / stddev
	if math.IsNaN(z) || math.IsInf(z, 0) {
		return AnomalyEvidence{}, false
	}
	return AnomalyEvidence{Current: current, BaselineMean: mean, BaselineStdDev: stddev, BaselineBuckets: count, ZScore: z,
		Threshold: sigma, Exceeded: math.Abs(z) >= sigma}, true
}

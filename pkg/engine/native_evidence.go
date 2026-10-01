package engine

import (
	"encoding/json"
	"errors"
	"math"
)

// ActionPropertyKey is collision-free even when names contain delimiters.
func ActionPropertyKey(action, property, value string) string {
	data, _ := json.Marshal([3]string{action, property, value})
	return string(data)
}

func CloneTDigestSnapshot(in *TDigestSnapshot) *TDigestSnapshot {
	if in == nil || len(in.Centroids) > 1024 {
		return nil
	}
	out := *in
	out.Centroids = append([]TDigestCentroidSnapshot(nil), in.Centroids...)
	return &out
}

// ValidateDurationDigest bounds untrusted persisted/peer distributions before
// allocating or merging them. Values use nanoseconds, matching engine digests.
func ValidateDurationDigest(in *TDigestSnapshot, count int64) error {
	finite := func(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
	if in == nil || count <= 0 || count > 1e12 || in.Count != count || !finite(in.Compression) || in.Compression < 1 || in.Compression > 300 || len(in.Centroids) == 0 || len(in.Centroids) > 1024 || !finite(in.TotalWeight) || math.Abs(in.TotalWeight-float64(count)) > 1e-6*float64(count) || !finite(in.Min) || !finite(in.Max) || in.Min < 0 || in.Max < in.Min || in.Max > float64(math.MaxInt64) {
		return errors.New("unavailable or invalid duration distribution")
	}
	sum := 0.0
	for _, c := range in.Centroids {
		if !finite(c.Mean) || !finite(c.Weight) || c.Weight <= 0 || c.Mean < in.Min || c.Mean > in.Max {
			return errors.New("invalid duration centroid")
		}
		sum += c.Weight
	}
	if !finite(sum) || math.Abs(sum-in.TotalWeight) > 1e-6*in.TotalWeight {
		return errors.New("duration distribution weight mismatch")
	}
	return nil
}

func boundedJointPropertyKey(key string, values map[string]*ActionMetrics) string {
	if len(key) > 512 {
		return metricOverflowLabel
	}
	if _, ok := values[key]; ok || len(values) < 127 {
		return key
	}
	return metricOverflowLabel
}

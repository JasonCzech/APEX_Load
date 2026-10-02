package storage

import "math"

// NativeObservationRate uses retained observation counts and elapsed wall time.
// Missing populations and invalid denominators remain unavailable.
func NativeObservationRate(metric string, count, failures int64, seconds float64) *float64 {
	if !IsNativeObservationRate(metric) || count <= 0 || count > 1<<53-1 || failures < 0 || failures > count || seconds <= 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return nil
	}
	numerator := count
	switch metric {
	case "error_observation_rate":
		numerator = failures
	case "success_observation_rate":
		numerator = count - failures
	}
	value := float64(numerator) / seconds
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	return &value
}
func IsNativeObservationRate(metric string) bool {
	return metric == "observation_rate" || metric == "error_observation_rate" || metric == "success_observation_rate"
}

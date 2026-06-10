package api

import "math"

func percentileFromSortedSamples(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if math.IsNaN(q) || q <= 0 {
		return sorted[0]
	}
	if q >= 1 {
		return sorted[len(sorted)-1]
	}
	if len(sorted) == 1 {
		return sorted[0]
	}

	rank := q * float64(len(sorted)-1)
	lower := int(math.Floor(rank))
	upper := int(math.Ceil(rank))
	if lower == upper {
		return sorted[lower]
	}

	weight := rank - float64(lower)
	return sorted[lower] + (sorted[upper]-sorted[lower])*weight
}

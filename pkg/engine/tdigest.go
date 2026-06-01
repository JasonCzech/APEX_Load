package engine

import (
	"math"
	"sort"
)

type centroid struct {
	mean   float64
	weight float64
}

// TDigest is a streaming quantile estimator using the merging digest algorithm.
// It produces accurate percentile estimates with bounded memory, and supports
// merging digests from multiple sources (required for multi-pod aggregation).
type TDigest struct {
	compression float64
	centroids   []centroid
	buffer      []float64
	bufferCap   int
	totalWeight float64
	min         float64
	max         float64
	count       int64
}

// NewTDigest creates a t-digest with the given compression factor.
// Higher compression = more centroids = better accuracy. Typical values: 100-300.
// Memory usage is O(compression).
func NewTDigest(compression float64) *TDigest {
	if compression <= 0 {
		compression = 200
	}
	bufCap := int(compression) * 5
	return &TDigest{
		compression: compression,
		centroids:   make([]centroid, 0, int(compression)*2),
		buffer:      make([]float64, 0, bufCap),
		bufferCap:   bufCap,
		min:         math.Inf(1),
		max:         math.Inf(-1),
	}
}

// Add records a single value.
func (td *TDigest) Add(val float64) {
	td.buffer = append(td.buffer, val)
	td.count++

	if val < td.min {
		td.min = val
	}
	if val > td.max {
		td.max = val
	}

	if len(td.buffer) >= td.bufferCap {
		td.flushBuffer()
	}
}

// Quantile returns the estimated value at quantile q (0.0 to 1.0).
func (td *TDigest) Quantile(q float64) float64 {
	td.flushBuffer()

	if len(td.centroids) == 0 {
		return 0
	}
	if q <= 0 {
		return td.min
	}
	if q >= 1 {
		return td.max
	}
	if len(td.centroids) == 1 {
		return td.centroids[0].mean
	}

	target := q * td.totalWeight

	// Walk centroids, interpolating between them
	cumulative := 0.0
	for i, c := range td.centroids {
		upper := cumulative + c.weight

		if target < cumulative+c.weight/2 {
			// Target falls in the left half of this centroid
			if i == 0 {
				// Interpolate between min and first centroid
				if c.weight/2 > 0 {
					innerQ := target / (c.weight / 2)
					return td.min + innerQ*(c.mean-td.min)
				}
				return td.min
			}
			prev := td.centroids[i-1]
			midPrev := cumulative - prev.weight/2
			midCurr := cumulative + c.weight/2
			if midCurr-midPrev > 0 {
				frac := (target - midPrev) / (midCurr - midPrev)
				return prev.mean + frac*(c.mean-prev.mean)
			}
			return c.mean
		}

		cumulative = upper

		if i == len(td.centroids)-1 {
			// Interpolate between last centroid and max
			remaining := td.totalWeight - cumulative + c.weight/2
			if remaining > 0 {
				frac := (target - (cumulative - c.weight/2)) / remaining
				return c.mean + frac*(td.max-c.mean)
			}
			return td.max
		}
	}

	return td.max
}

// Count returns the total number of values added.
func (td *TDigest) Count() int64 {
	return td.count
}

// Merge combines another t-digest into this one. This is the key operation
// for distributed aggregation — each generator pod produces its own t-digest,
// and the controller merges them.
func (td *TDigest) Merge(other *TDigest) {
	if other == nil || other.count == 0 {
		return
	}

	other.flushBuffer()

	for _, c := range other.centroids {
		td.centroids = append(td.centroids, c)
		td.totalWeight += c.weight
	}
	td.count += other.count

	if other.min < td.min {
		td.min = other.min
	}
	if other.max > td.max {
		td.max = other.max
	}

	td.compress()
}

// Reset clears the digest for reuse.
func (td *TDigest) Reset() {
	td.centroids = td.centroids[:0]
	td.buffer = td.buffer[:0]
	td.totalWeight = 0
	td.min = math.Inf(1)
	td.max = math.Inf(-1)
	td.count = 0
}

func (td *TDigest) flushBuffer() {
	if len(td.buffer) == 0 {
		return
	}

	sort.Float64s(td.buffer)

	for _, val := range td.buffer {
		td.centroids = append(td.centroids, centroid{mean: val, weight: 1})
		td.totalWeight++
	}
	td.buffer = td.buffer[:0]

	td.compress()
}

func (td *TDigest) compress() {
	if len(td.centroids) <= 1 {
		return
	}

	sort.Slice(td.centroids, func(i, j int) bool {
		return td.centroids[i].mean < td.centroids[j].mean
	})

	result := make([]centroid, 0, len(td.centroids))
	result = append(result, td.centroids[0])

	cumWeight := td.centroids[0].weight

	for i := 1; i < len(td.centroids); i++ {
		c := td.centroids[i]
		last := &result[len(result)-1]

		// Compute the quantile position of the proposed merged centroid
		proposedWeight := last.weight + c.weight
		q := (cumWeight - last.weight/2 + proposedWeight/2) / td.totalWeight

		// Scale function: k1 limit based on quantile position
		limit := td.maxWeight(q)

		if proposedWeight <= limit {
			// Merge into the current centroid
			newWeight := last.weight + c.weight
			last.mean = (last.mean*last.weight + c.mean*c.weight) / newWeight
			last.weight = newWeight
		} else {
			// Start a new centroid
			cumWeight += last.weight
			result = append(result, c)
		}
	}

	td.centroids = result
}

// maxWeight returns the maximum weight a centroid can have at quantile position q.
// Uses the k1 scale function: 4n * q * (1-q) / compression
func (td *TDigest) maxWeight(q float64) float64 {
	return 4 * td.totalWeight * q * (1 - q) / td.compression
}

package api

import (
	"sort"

	"github.com/jasonczech/apexload/pkg/engine"
)

const (
	// Keep small archives exact so report values remain stable and easy to audit.
	// Larger archives are promoted to a t-digest whose memory is independent of
	// the number of transactions in the run.
	archiveExactQuantileLimit = 1_024
	archiveTDigestCompression = 200
)

type archiveQuantileAccumulator struct {
	exact  []float64
	digest *engine.TDigest
}

func (acc *archiveQuantileAccumulator) Add(value float64) {
	if acc == nil {
		return
	}
	if acc.digest == nil && len(acc.exact) < archiveExactQuantileLimit {
		acc.exact = append(acc.exact, value)
		return
	}
	if acc.digest == nil {
		acc.digest = engine.NewTDigest(archiveTDigestCompression)
		for _, sample := range acc.exact {
			acc.digest.Add(sample)
		}
		acc.exact = nil
	}
	acc.digest.Add(value)
}

func (acc *archiveQuantileAccumulator) Count() int64 {
	if acc == nil {
		return 0
	}
	if acc.digest != nil {
		return acc.digest.Count()
	}
	return int64(len(acc.exact))
}

func (acc *archiveQuantileAccumulator) Quantile(q float64) float64 {
	if acc == nil {
		return 0
	}
	if acc.digest != nil {
		return acc.digest.Quantile(q)
	}
	if len(acc.exact) == 0 {
		return 0
	}
	sort.Float64s(acc.exact)
	return archiveAnalysisPercentile(acc.exact, q)
}

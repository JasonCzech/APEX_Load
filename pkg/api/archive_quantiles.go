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

	// Percentiles behind release-gate verdicts (the report overview, the
	// transaction and request populations with their per-name tables, and
	// transaction SLA rules) stay exact for longer and use a finer sketch beyond
	// that. The t-digest can report a value between two latency modes when a
	// slow mode's share sits at the percentile's tail; exact samples avoid that,
	// and compression 1,000 narrows the band roughly fivefold.
	archiveGateExactQuantileLimit = 20_000
	archiveGateTDigestCompression = 1_000

	// One report's gate accumulators share a memory budget for what they hold
	// beyond the ordinary limits: 8 bytes per extra exact sample, and 256 KiB per
	// compression-1,000 digest (measured at about 240 KiB after 10 million
	// samples). An accumulator the budget cannot cover keeps the ordinary limits,
	// so thousands of distinct transactions cost what they cost before.
	archiveGateQuantileBudgetBytes    = 40 << 20
	archiveGateExactSampleBytes       = 8
	archiveGateDigestReservationBytes = 256 << 10
)

// archiveQuantileBudget is the memory one report's gate accumulators share. A
// report is built by a single goroutine, so it needs no locking.
type archiveQuantileBudget struct {
	remainingBytes int64
}

func newArchiveQuantileBudget() *archiveQuantileBudget {
	return &archiveQuantileBudget{remainingBytes: archiveGateQuantileBudgetBytes}
}

// take reserves bytes and reports whether they fit. A nil budget is unlimited.
func (b *archiveQuantileBudget) take(bytes int64) bool {
	if b == nil {
		return true
	}
	if b.remainingBytes < bytes {
		return false
	}
	b.remainingBytes -= bytes
	return true
}

func (b *archiveQuantileBudget) release(bytes int64) {
	if b != nil {
		b.remainingBytes += bytes
	}
}

type archiveQuantileAccumulator struct {
	exact  []float64
	digest *engine.TDigest
	// gate marks a percentile that can decide a release gate.
	gate   bool
	budget *archiveQuantileBudget
}

// newArchiveGateQuantileAccumulator returns an accumulator for a percentile
// that can decide a release gate.
func newArchiveGateQuantileAccumulator(budget *archiveQuantileBudget) *archiveQuantileAccumulator {
	return &archiveQuantileAccumulator{gate: true, budget: budget}
}

func (acc *archiveQuantileAccumulator) Add(value float64) {
	if acc == nil {
		return
	}
	if acc.digest == nil {
		if acc.keepExact() {
			acc.exact = append(acc.exact, value)
			return
		}
		acc.promote()
	}
	acc.digest.Add(value)
}

// keepExact reports whether the next sample still fits the exact list.
func (acc *archiveQuantileAccumulator) keepExact() bool {
	n := len(acc.exact)
	if n < archiveExactQuantileLimit {
		return true
	}
	return acc.gate && n < archiveGateExactQuantileLimit && acc.budget.take(archiveGateExactSampleBytes)
}

// promote moves the exact samples into a t-digest. A gate accumulator trades
// its extra exact samples for the finer sketch while the budget can hold one.
func (acc *archiveQuantileAccumulator) promote() {
	compression := float64(archiveTDigestCompression)
	if acc.gate {
		acc.budget.release(int64(max(len(acc.exact)-archiveExactQuantileLimit, 0)) * archiveGateExactSampleBytes)
		if acc.budget.take(archiveGateDigestReservationBytes) {
			compression = archiveGateTDigestCompression
		}
	}
	acc.digest = engine.NewTDigest(compression)
	for _, sample := range acc.exact {
		acc.digest.Add(sample)
	}
	acc.exact = nil
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

package api

import (
	"sort"

	"github.com/jasonczech/apexload/pkg/engine"
)

const (
	// Keep small archives exact so report values remain stable and easy to audit.
	// Larger archives move to a duration sketch: every percentile is within
	// 0.5% of the value at its rank, and its memory does not grow with the
	// number of transactions in the run.
	archiveExactQuantileLimit = 1_024

	// Percentiles behind release-gate verdicts (the report overview, the
	// transaction and request populations with their per-name tables, and
	// transaction SLA rules) stay exact for longer.
	archiveGateExactQuantileLimit = 20_000

	// One report's gate accumulators share a memory budget for the exact
	// samples they hold beyond the ordinary limit (8 bytes each). An accumulator
	// the budget cannot cover moves to the sketch at the ordinary limit, so
	// thousands of distinct transactions cost what they cost before.
	archiveGateQuantileBudgetBytes = 40 << 20
	archiveGateExactSampleBytes    = 8
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
	sketch *engine.DurationSketch
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
	if acc.sketch == nil {
		if acc.keepExact() {
			acc.exact = append(acc.exact, value)
			return
		}
		acc.promote()
	}
	acc.sketch.Add(value)
}

// keepExact reports whether the next sample still fits the exact list.
func (acc *archiveQuantileAccumulator) keepExact() bool {
	n := len(acc.exact)
	if n < archiveExactQuantileLimit {
		return true
	}
	return acc.gate && n < archiveGateExactQuantileLimit && acc.budget.take(archiveGateExactSampleBytes)
}

// promote moves the exact samples into a sketch and returns a gate
// accumulator's extra exact samples to the budget.
func (acc *archiveQuantileAccumulator) promote() {
	if acc.gate {
		acc.budget.release(int64(max(len(acc.exact)-archiveExactQuantileLimit, 0)) * archiveGateExactSampleBytes)
	}
	acc.sketch = engine.NewDurationSketch()
	for _, sample := range acc.exact {
		acc.sketch.Add(sample)
	}
	acc.exact = nil
}

func (acc *archiveQuantileAccumulator) Count() int64 {
	if acc == nil {
		return 0
	}
	if acc.sketch != nil {
		return acc.sketch.Count()
	}
	return int64(len(acc.exact))
}

func (acc *archiveQuantileAccumulator) Quantile(q float64) float64 {
	if acc == nil {
		return 0
	}
	if acc.sketch != nil {
		return acc.sketch.Quantile(q)
	}
	if len(acc.exact) == 0 {
		return 0
	}
	sort.Float64s(acc.exact)
	return archiveAnalysisPercentile(acc.exact, q)
}

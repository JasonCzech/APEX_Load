package rca

import (
	"fmt"
	"runtime/metrics"
	"sort"
	"sync"
	"time"
)

// LimitationBudgetTruncated means an analysis hit a budget limit and used
// less evidence than it was given. The limitation's detail names the limit.
const LimitationBudgetTruncated = "budget_truncated"

// Budget limits. Each names what was cut when a budget truncates.
const (
	BudgetSeries       = "series"
	BudgetSeriesPoints = "series_points"
	BudgetExemplars    = "exemplars"
	BudgetGraphNodes   = "graph_nodes"
	BudgetGraphEdges   = "graph_edges"
	BudgetWallTime     = "wall_time"
	BudgetHeap         = "heap"
)

// MaxSeriesPoints is the default points one series may carry.
const MaxSeriesPoints = 10000

// Budget bounds one analysis. A zero field means no limit.
type Budget struct {
	MaxSeries       int           `json:"max_series"`
	MaxSeriesPoints int           `json:"max_series_points"`
	MaxExemplars    int           `json:"max_exemplars"`
	MaxGraphNodes   int           `json:"max_graph_nodes"`
	MaxGraphEdges   int           `json:"max_graph_edges"`
	MaxWallTime     time.Duration `json:"max_wall_time_ns"`
	MaxHeapBytes    uint64        `json:"max_heap_bytes"`
}

// DefaultBudget is the plan's default analysis budget.
var DefaultBudget = Budget{
	MaxSeries: 2000, MaxSeriesPoints: MaxSeriesPoints, MaxExemplars: 20000,
	MaxGraphNodes: 5000, MaxGraphEdges: 20000,
	MaxWallTime: 120 * time.Second, MaxHeapBytes: 512 << 20,
}

// BudgetTruncation records one limit an analysis hit.
type BudgetTruncation struct {
	Limit string `json:"limit"`
	// Allowed is the limit; Offered is what the analysis was given or had
	// used when it stopped, so Offered > Allowed.
	Allowed uint64 `json:"allowed"`
	Offered uint64 `json:"offered"`
}

// Budgeter applies a budget through one analysis. The clock and heap reader
// are injected so the engine stays deterministic under test. Its methods are
// safe for concurrent stages.
type Budgeter struct {
	mu          sync.Mutex
	budget      Budget
	started     time.Time
	now         func() time.Time
	heap        func() uint64
	heapBase    uint64
	truncations map[string]*BudgetTruncation
}

// NewBudgeter starts a budget at now(). heap reports the process's live heap
// bytes (LiveHeapBytes in production); the limit applies to its growth from
// the start, since the controller's own heap is not the analysis's. A nil
// heap reader disables the heap limit.
func NewBudgeter(budget Budget, now func() time.Time, heap func() uint64) *Budgeter {
	if now == nil {
		now = time.Now
	}
	b := &Budgeter{budget: budget, started: now(), now: now, heap: heap, truncations: map[string]*BudgetTruncation{}}
	if heap != nil {
		b.heapBase = heap()
	}
	return b
}

// LiveHeapBytes reads the heap the last garbage collection found live. It is
// process-wide, so concurrent analyses see each other's growth.
func LiveHeapBytes() uint64 {
	sample := []metrics.Sample{{Name: "/gc/heap/live:bytes"}}
	metrics.Read(sample)
	if sample[0].Value.Kind() != metrics.KindUint64 {
		return 0
	}
	return sample[0].Value.Uint64()
}

func (b *Budgeter) record(limit string, allowed, offered uint64) {
	t := b.truncations[limit]
	if t == nil {
		t = &BudgetTruncation{Limit: limit, Allowed: allowed}
		b.truncations[limit] = t
	}
	t.Offered = max(t.Offered, offered)
}

func (b *Budgeter) keep(limit string, allowed, offered int) int {
	if allowed <= 0 || offered <= allowed {
		return offered
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.record(limit, uint64(allowed), uint64(offered))
	return allowed
}

// Series returns how many of n series to keep.
func (b *Budgeter) Series(n int) int { return b.keep(BudgetSeries, b.budget.MaxSeries, n) }

// SeriesPoints returns how many of one series' n points to keep.
func (b *Budgeter) SeriesPoints(n int) int {
	return b.keep(BudgetSeriesPoints, b.budget.MaxSeriesPoints, n)
}

// Exemplars returns how many of n exemplars to keep.
func (b *Budgeter) Exemplars(n int) int { return b.keep(BudgetExemplars, b.budget.MaxExemplars, n) }

// GraphNodes and GraphEdges return how many of n to keep.
func (b *Budgeter) GraphNodes(n int) int { return b.keep(BudgetGraphNodes, b.budget.MaxGraphNodes, n) }
func (b *Budgeter) GraphEdges(n int) int { return b.keep(BudgetGraphEdges, b.budget.MaxGraphEdges, n) }

// Exhausted reports whether the wall-time or heap limit has been reached; a
// stage checks it before starting and skips its work when it is.
func (b *Budgeter) Exhausted() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	exhausted := false
	if limit := b.budget.MaxWallTime; limit > 0 {
		if elapsed := b.now().Sub(b.started); elapsed >= limit {
			b.record(BudgetWallTime, uint64(limit/time.Millisecond), uint64(elapsed/time.Millisecond))
			exhausted = true
		}
	}
	if limit := b.budget.MaxHeapBytes; limit > 0 && b.heap != nil {
		if current := b.heap(); current > b.heapBase && current-b.heapBase >= limit {
			b.record(BudgetHeap, limit, current-b.heapBase)
			exhausted = true
		}
	}
	return exhausted
}

// Truncations lists the limits hit, by limit name.
func (b *Budgeter) Truncations() []BudgetTruncation {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]BudgetTruncation, 0, len(b.truncations))
	for _, t := range b.truncations {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Limit < out[j].Limit })
	return out
}

// Detail describes the truncation with its unit: milliseconds for wall time,
// MiB for heap, and a count otherwise.
func (t BudgetTruncation) Detail() string {
	switch t.Limit {
	case BudgetWallTime:
		return fmt.Sprintf("%s: %.1f s allowed, %.1f s used", t.Limit, float64(t.Allowed)/1000, float64(t.Offered)/1000)
	case BudgetHeap:
		return fmt.Sprintf("%s: %d MiB allowed, %d MiB used", t.Limit, t.Allowed>>20, t.Offered>>20)
	}
	return fmt.Sprintf("%s: %d of %d", t.Limit, t.Allowed, t.Offered)
}

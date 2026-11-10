package api

import "time"

// Iteration replay (Phase 2 R4): the report's index of VUser iterations and
// the endpoint that rebuilds one iteration from the archive rows. The shapes
// are mirrored in dashboard/src/types/iterationReplay.ts.

const (
	iterationIndexSchema     = "apexload.iteration_index.v1"
	iterationWaterfallSchema = "apexload.iteration_waterfall.v1"
)

// Iteration index truncation reasons.
const (
	iterationIndexReasonIdentityEvicted = "identity_evicted"
	iterationIndexReasonSample          = "iteration_sample"
)

// Iteration waterfall partial reasons.
const (
	iterationWaterfallReasonScanBudget     = "scan_budget"
	iterationWaterfallReasonAmbiguousVUser = "ambiguous_vuser_id"
)

// RunArchiveIterationIndex summarizes the run's VUser iterations (rows of the
// action phase with iteration > 0) and names a few worth replaying.
type RunArchiveIterationIndex struct {
	Schema string `json:"schema"`
	// IterationsTotal counts closed iterations, timed or not.
	IterationsTotal int64 `json:"iterations_total"`
	// Identities counts the VUser identities (lifetime, else legacy VUser ID)
	// that wrote an iteration row; an evicted identity that wrote again counts
	// twice.
	Identities      int64                              `json:"identities"`
	IdentityQuality RunArchiveIterationIdentityQuality `json:"identity_quality"`
	// Span is each whole timed iteration's first row start to its last row
	// end (PartialIterations are left out).
	Span RunArchiveIterationSpan `json:"span"`
	// RowsOutsideIterations counts rows with no iteration (init, end, or
	// iteration <= 0); they belong to no replay.
	RowsOutsideIterations int64 `json:"rows_outside_iterations,omitempty"`
	// LateRows counts rows of a VUser identity written after it had moved
	// on two or more iterations; they are not part of the index's spans
	// (the replay endpoint still finds them). A late row of the iteration
	// just before the open one joins that iteration's span and row counts.
	LateRows int64 `json:"late_rows,omitempty"`
	// UntimedIterations counts iterations whose rows carry no timestamp.
	UntimedIterations int64 `json:"untimed_iterations,omitempty"`
	// IdentitiesEvicted counts identities released to keep the identity
	// table bounded; their open iteration closed then. Only one that wrote
	// to that iteration again had it split (reason identity_evicted).
	IdentitiesEvicted int64 `json:"identities_evicted,omitempty"`
	// PartialIterations counts timed iterations left out of Span and the
	// p50/p95 picks: cut short by the test's stop or ramp-down (a VUser's
	// last iteration), or split by identity eviction. IterationsTotal still
	// counts them.
	PartialIterations int64 `json:"partial_iterations,omitempty"`
	Truncated         bool  `json:"truncated"`
	// Reasons is sorted, from a closed set: identity_evicted (an evicted
	// identity that wrote again has its iteration split), iteration_sample
	// (the p50/p95 picks come from a bounded sample).
	Reasons         []string                 `json:"reasons,omitempty"`
	Representatives []RunArchiveIterationRef `json:"representatives"`
}

// RunArchiveIterationIdentityQuality counts iterations by identity quality,
// and rows that have no identity at all.
type RunArchiveIterationIdentityQuality struct {
	Lifetime int64 `json:"lifetime"`
	Legacy   int64 `json:"legacy"`
	// NoneRows counts iteration rows with neither a VUser ID nor a lifetime.
	NoneRows int64 `json:"none_rows"`
}

// RunArchiveIterationSpan is the distribution of iteration spans.
type RunArchiveIterationSpan struct {
	Samples           int64   `json:"samples"`
	P50Ms             float64 `json:"p50_ms"`
	P95Ms             float64 `json:"p95_ms"`
	MaxMs             float64 `json:"max_ms"`
	PercentileQuality string  `json:"percentile_quality,omitempty"`
}

// RunArchiveIterationRef locates one iteration worth replaying. first_ordinal
// and last_ordinal are the line indexes of its first and last row, the
// replay endpoint's scan hints.
type RunArchiveIterationRef struct {
	Reason          string    `json:"reason"` // slowest | p50 | p95 | failed
	TargetMs        *float64  `json:"target_ms,omitempty"`
	Sampled         bool      `json:"sampled,omitempty"`
	IdentityQuality string    `json:"identity_quality"` // lifetime | legacy: of the rows shown
	Group           string    `json:"group,omitempty"`
	VUserID         int       `json:"vuser_id,omitempty"`
	VUserLifetime   uint64    `json:"vuser_lifetime,string,omitempty"`
	Iteration       int64     `json:"iteration"`
	FirstOrdinal    int64     `json:"first_ordinal"`
	LastOrdinal     int64     `json:"last_ordinal"`
	StartedAt       time.Time `json:"started_at,omitzero"`
	SpanMs          float64   `json:"span_ms"`
	TransactionRows int64     `json:"transaction_rows"`
	RequestRows     int64     `json:"request_rows"`
	// FailedRows counts failed rows; rows interrupted by the test's stop or
	// ramp-down are not failures.
	FailedRows int64 `json:"failed_rows"`
	// Split is set when the iteration index's identity eviction split this
	// iteration into more than one record: its rows may lie outside
	// first_ordinal..last_ordinal, so a replay must not use them as hints.
	Split bool `json:"split,omitempty"`
	// Interrupted is set when a row of the iteration was interrupted by the
	// test's stop or ramp-down: the iteration was cut short, so its span is
	// left out of span (a slowest ref can exceed span.max_ms).
	Interrupted bool `json:"interrupted,omitempty"`
}

// ArchiveIterationWaterfall is one VUser iteration rebuilt from its archive
// rows (transactions and requests), in start order.
type ArchiveIterationWaterfall struct {
	Schema          string    `json:"schema"`
	IdentityQuality string    `json:"identity_quality"` // lifetime | legacy: of the rows shown
	Group           string    `json:"group,omitempty"`
	VUserID         int       `json:"vuser_id,omitempty"`
	VUserLifetime   uint64    `json:"vuser_lifetime,string,omitempty"`
	Iteration       int64     `json:"iteration"`
	FirstOrdinal    int64     `json:"first_ordinal"`
	LastOrdinal     int64     `json:"last_ordinal"`
	StartedAt       time.Time `json:"started_at,omitzero"`
	SpanMs          float64   `json:"span_ms"`
	// Rows are sorted by start, then ordinal; offset_ms is from StartedAt.
	Rows      []ArchiveWaterfallRow `json:"rows"`
	RowsTotal int                   `json:"rows_total"`
	Truncated bool                  `json:"truncated,omitempty"`
	// Partial is set when rows of the iteration may be missing (the scan
	// budget ran out) or a query by VUser ID alone matched more than one
	// iteration with that number (a reused VUser ID); the first, or the one
	// holding the anchor ordinal, is shown.
	Partial        bool     `json:"partial,omitempty"`
	PartialReasons []string `json:"partial_reasons,omitempty"`
	// ThinkMsInsideTransactions sums elapsed minus duration over the
	// outermost transaction rows (a nested row's think time is already inside
	// its parent's): think time the engine subtracted from them.
	ThinkMsInsideTransactions float64 `json:"think_ms_inside_transactions"`
	// ThinkMsApproximate is set when the iteration held so many sequential
	// transactions that think time was summed in bounded memory: a row
	// archived long after rows it overlaps may be counted or left out.
	ThinkMsApproximate bool `json:"think_ms_approximate,omitempty"`
	// GapsMs is the time inside the span no row covers (think time or pacing
	// between transactions, which the archive does not record). Absent when
	// rows were cut or may be missing.
	GapsMs *float64 `json:"gaps_ms,omitempty"`
}

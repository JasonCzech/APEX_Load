package api

import "time"

// Report-time waterfalls (Phase 2): R1 time anatomy, R2 tail attribution and
// R3 representative waterfalls for business transactions. The shapes below
// are a frozen contract shared with the dashboard (types/timeAnatomy.ts and
// runReport/__fixtures__/timeAnatomyReport.json).

const (
	timeAnatomySchema          = "apexload.time_anatomy.v1"
	timeAnatomyPhaseModel      = "sequential_clipped_v1"
	timeAnatomyJoinRule        = "vuser_lifetime_containment_v1"
	transactionWaterfallSchema = "apexload.transaction_waterfall.v1"
)

// RunArchiveTimeAnatomy explains where each business transaction's time goes:
// its requests (R1), what its slow tail spends extra time on (R2) and the
// instances worth opening as a waterfall (R3).
type RunArchiveTimeAnatomy struct {
	Schema       string                          `json:"schema"`
	PhaseModel   string                          `json:"phase_model"`
	JoinRule     string                          `json:"join_rule"`
	Transactions []RunArchiveTransactionAnatomy  `json:"transactions"`
	Coverage     RunArchiveTimeAnatomyCoverage   `json:"coverage"`
	Truncation   RunArchiveTimeAnatomyTruncation `json:"truncation"`
}

// RunArchiveTimeAnatomyCoverage counts how many rows the join could use.
type RunArchiveTimeAnatomyCoverage struct {
	TransactionRows                int64 `json:"transaction_rows"`
	JoinedTransactionRows          int64 `json:"joined_transaction_rows"`
	TransactionRowsWithoutRequests int64 `json:"transaction_rows_without_requests"`
	UnjoinableTransactionRows      int64 `json:"unjoinable_transaction_rows"`
	InterruptedTransactionRows     int64 `json:"interrupted_transaction_rows"`
	RequestRows                    int64 `json:"request_rows"`
	RequestsInTransactions         int64 `json:"requests_in_transactions"`
	RequestsOutsideTransactions    int64 `json:"requests_outside_transactions"`
	// RequestsClaimedAcrossIteration counts requests claimed by a transaction
	// recorded in a later iteration (the engine's carryOpenSourceTransactions).
	RequestsClaimedAcrossIteration int64 `json:"requests_claimed_across_iteration"`
	// LateRequestRows counts requests written after a transaction whose
	// window they started in had closed and that no transaction claimed by
	// the time they left the buffer (excluded from every transaction).
	LateRequestRows                int64 `json:"late_request_rows"`
	RequestsEndingAfterTransaction int64 `json:"requests_ending_after_transaction"`
	ClampedRequestSamples          int64 `json:"clamped_request_samples"`
	RetriedRequestSamples          int64 `json:"retried_request_samples"`
	LegacyIdentityRows             int64 `json:"legacy_identity_rows"`
	UnknownPopulationRows          int64 `json:"unknown_population_rows"`
	// PartialTransactionRows counts transaction rows whose window overlaps a
	// request the bounded buffer dropped or evicted: they may have lost
	// children, so they count as neither joined nor without requests and
	// stay out of the decomposition.
	PartialTransactionRows int64 `json:"partial_transaction_rows,omitempty"`
}

// RunArchiveTimeAnatomyTruncation says what the bounded accumulator left out.
type RunArchiveTimeAnatomyTruncation struct {
	Truncated bool `json:"truncated"`
	// Reasons is sorted and de-duplicated, from a closed set: transaction_cap,
	// report_transaction_cap, request_row_cap, occurrence_fold,
	// pending_key_cap, pending_global_cap, instance_sample, sample_budget,
	// identity_cap, work_budget.
	Reasons                []string `json:"reasons,omitempty"`
	TransactionsTracked    int      `json:"transactions_tracked"`
	TransactionsOmitted    int64    `json:"transactions_omitted,omitempty"`
	TransactionsNotEmitted int      `json:"transactions_not_emitted,omitempty"`
	RequestRowsOmitted     int64    `json:"request_rows_omitted,omitempty"`
	// PendingRequestsDropped counts requests the bounded buffer refused or
	// dropped, and unclaimed requests a transaction row the work budget
	// refused might have claimed: whether they ran inside a transaction is
	// unknown, so they are not counted as outside one. A request the
	// per-identity cap dropped after a transaction had claimed it is
	// counted in requests_in_transactions only.
	PendingRequestsDropped int64 `json:"pending_requests_dropped,omitempty"`
	// PendingRequestsEvicted counts buffered requests no transaction had
	// claimed that were released early to keep the buffer bounded (least
	// recently seen VUser lifetimes first): whether they ran inside a
	// transaction is unknown. An evicted request a transaction had already
	// claimed is counted in requests_in_transactions only. An
	// eviction only becomes a truncation reason when a later transaction
	// could have claimed the evicted request (PartialTransactionRows).
	PendingRequestsEvicted int64 `json:"pending_requests_evicted,omitempty"`
	// IdentitiesEvicted counts VUser lifetimes whose buffered state was
	// released to keep the identity table bounded.
	IdentitiesEvicted int64 `json:"identities_evicted,omitempty"`
}

// RunArchiveTransactionAnatomy is one business transaction's anatomy.
type RunArchiveTransactionAnatomy struct {
	Name            string `json:"name"`
	Instances       int64  `json:"instances"`
	JoinedInstances int64  `json:"joined_instances"`
	// PartialInstances are instances that may have lost children to a
	// bounded-buffer drop or eviction. They are not joined: the
	// decomposition, request rows and tail means leave them out, and the
	// tail's join coverage counts them as unjoined.
	PartialInstances     int64                            `json:"partial_instances,omitempty"`
	FailedInstances      int64                            `json:"failed_instances"`
	InterruptedInstances int64                            `json:"interrupted_instances"`
	SLAStatus            string                           `json:"sla_status,omitempty"` // "failed" | "passed" | "" (no rule)
	Duration             RunArchivePhaseStat              `json:"duration"`
	Decomposition        RunArchiveTimeDecomposition      `json:"decomposition"`
	Requests             []RunArchiveAnatomyRequest       `json:"requests"`
	OtherRequests        *RunArchiveAnatomyRequest        `json:"other_requests,omitempty"`
	RequestsTruncated    bool                             `json:"requests_truncated,omitempty"`
	Tail                 *RunArchiveTailAttribution       `json:"tail,omitempty"` // nil only when Instances==0
	Representatives      []RunArchiveRepresentativeSample `json:"representatives,omitempty"`
}

// RunArchiveTimeDecomposition is the mean instance vector. For every instance
// duration_ms = sum(phases) + non_request_ms - overlap_ms, so the means obey
// the same identity.
type RunArchiveTimeDecomposition struct {
	Instances              int64                `json:"instances"` // joined, non-interrupted
	AvgDurationMs          float64              `json:"avg_duration_ms"`
	Phases                 RunArchivePhaseMeans `json:"phases"`
	NonRequestMs           float64              `json:"non_request_ms"`
	OverlapMs              float64              `json:"overlap_ms"`
	AvgRequestsPerInstance float64              `json:"avg_requests_per_instance"`
}

// RunArchivePhaseMeans holds one value per request phase.
type RunArchivePhaseMeans struct {
	DNSMs      float64 `json:"dns_ms"`
	ConnectMs  float64 `json:"connect_ms"`
	TLSMs      float64 `json:"tls_ms"`
	ServerMs   float64 `json:"server_ms"`
	DownloadMs float64 `json:"download_ms"`
	UnsplitMs  float64 `json:"unsplit_ms"`
}

// RunArchivePhaseStat summarizes one phase over every sample, zeros included.
type RunArchivePhaseStat struct {
	Samples           int64    `json:"samples"`
	AvgMs             float64  `json:"avg_ms"`
	P50Ms             float64  `json:"p50_ms"`
	P90Ms             float64  `json:"p90_ms"`
	P95Ms             float64  `json:"p95_ms"`
	P99Ms             float64  `json:"p99_ms"`
	MaxMs             float64  `json:"max_ms"`
	ObservedPct       *float64 `json:"observed_pct,omitempty"` // dns/connect/tls/server/download only
	AvgWhenObservedMs *float64 `json:"avg_when_observed_ms,omitempty"`
	PercentileQuality string   `json:"percentile_quality,omitempty"` // "" | "approximate"
}

// RunArchiveAnatomyPhaseStats holds a request row's per-phase statistics.
type RunArchiveAnatomyPhaseStats struct {
	DNS      RunArchivePhaseStat `json:"dns"`
	Connect  RunArchivePhaseStat `json:"connect"`
	TLS      RunArchivePhaseStat `json:"tls"`
	Server   RunArchivePhaseStat `json:"server"`
	Download RunArchivePhaseStat `json:"download"`
	Unsplit  RunArchivePhaseStat `json:"unsplit"`
	Total    RunArchivePhaseStat `json:"total"`
}

// RunArchiveAnatomyRequest is one request row of a transaction's aggregate
// waterfall: one source step and action at one occurrence.
type RunArchiveAnatomyRequest struct {
	Key              string                      `json:"key"`
	ActionName       string                      `json:"action_name"`
	SourceStepIndex  int                         `json:"source_step_index,omitempty"`
	Occurrence       int                         `json:"occurrence"`
	OccurrenceFolded bool                        `json:"occurrence_folded,omitempty"`
	DependencyType   string                      `json:"dependency_type,omitempty"` // first seen by ordinal
	Samples          int64                       `json:"samples"`
	Failed           int64                       `json:"failed"`
	PresencePct      float64                     `json:"presence_pct"`
	OffsetMs         RunArchivePhaseStat         `json:"offset_ms"`
	Phases           RunArchiveAnatomyPhaseStats `json:"phases"`
	Connections      RunArchiveConnectionStats   `json:"connections"`
}

// RunArchiveConnectionStats classifies a row's connections.
type RunArchiveConnectionStats struct {
	New          int64    `json:"new"`
	Reused       int64    `json:"reused"`
	Unknown      int64    `json:"unknown"`
	Inconsistent int64    `json:"inconsistent"`
	ReusePct     *float64 `json:"reuse_pct,omitempty"`
}

// RunArchiveTailAttribution compares the mean instance above P95 with the
// mean instance at or below P50.
type RunArchiveTailAttribution struct {
	Status           string                   `json:"status"` // ok | insufficient_samples | no_tail | low_join_coverage
	ThresholdP50Ms   float64                  `json:"threshold_p50_ms"`
	ThresholdP95Ms   float64                  `json:"threshold_p95_ms"`
	ThresholdQuality string                   `json:"threshold_quality,omitempty"`
	BandEstimate     string                   `json:"band_estimate"` // exact | sampled
	Instances        int64                    `json:"instances"`
	SampledInstances int64                    `json:"sampled_instances"`
	Tail             RunArchiveTailBand       `json:"tail"`
	Median           RunArchiveTailBand       `json:"median"`
	Delta            RunArchiveTailDelta      `json:"delta"`
	Requests         []RunArchiveRequestDelta `json:"requests,omitempty"` // top 5
	Headline         string                   `json:"headline,omitempty"`
}

// RunArchiveTailBand is one band's members and its joined members' means.
type RunArchiveTailBand struct {
	Count         int64                `json:"count"`
	Joined        int64                `json:"joined"`
	Failed        int64                `json:"failed"`
	AvgDurationMs float64              `json:"avg_duration_ms"`
	Phases        RunArchivePhaseMeans `json:"phases"`
	NonRequestMs  float64              `json:"non_request_ms"`
	OverlapMs     float64              `json:"overlap_ms"`
}

// RunArchiveTailDelta splits the tail-minus-median mean into components that
// add up to TotalMs.
type RunArchiveTailDelta struct {
	TotalMs    float64                   `json:"total_ms"`
	Components []RunArchiveTailComponent `json:"components"`
}

// RunArchiveTailComponent is one component of the tail delta.
type RunArchiveTailComponent struct {
	Component string   `json:"component"` // dns|connect|tls|server|download|unsplit|non_request|overlap
	DeltaMs   float64  `json:"delta_ms"`
	SharePct  *float64 `json:"share_pct,omitempty"`
}

// RunArchiveRequestDelta is how much longer one request row runs in the tail.
type RunArchiveRequestDelta struct {
	Key           string  `json:"key"`
	ActionName    string  `json:"action_name"`
	DeltaMs       float64 `json:"delta_ms"`
	DominantPhase string  `json:"dominant_phase"` // setup|server|download|unsplit
	DominantMs    float64 `json:"dominant_ms"`
}

// RunArchiveRepresentativeSample locates one transaction instance worth
// opening as a waterfall.
type RunArchiveRepresentativeSample struct {
	Reason        string    `json:"reason"` // p50 | p95 | slowest | failed
	TargetMs      *float64  `json:"target_ms,omitempty"`
	EventID       string    `json:"event_id"`
	Ordinal       int64     `json:"ordinal"`            // transaction row's line index
	FirstOrdinal  int64     `json:"first_ordinal"`      // min claimed child ordinal (== Ordinal if none)
	Timestamp     time.Time `json:"timestamp,omitzero"` // absent for a row without a timestamp
	DurationMs    float64   `json:"duration_ms"`
	ElapsedMs     float64   `json:"elapsed_ms,omitempty"`
	Success       bool      `json:"success"`
	StatusCode    int       `json:"status_code,omitempty"`
	ErrorClass    string    `json:"error_class,omitempty"`
	Group         string    `json:"group,omitempty"`
	VUserID       int       `json:"vuser_id,omitempty"`
	VUserLifetime uint64    `json:"vuser_lifetime,string,omitempty"` // 63-bit nonce > 2^53
	Iteration     int64     `json:"iteration,omitempty"`
	TraceID       string    `json:"trace_id,omitempty"` // only when trace_sent
	Children      int       `json:"children"`
	Sampled       bool      `json:"sampled,omitempty"`
}

// ArchiveTransactionWaterfall is the drill-down response: one transaction
// instance rebuilt with its requests.
type ArchiveTransactionWaterfall struct {
	Schema                string                       `json:"schema"`
	JoinRule              string                       `json:"join_rule"`
	PhaseModel            string                       `json:"phase_model"`
	IdentityQuality       string                       `json:"identity_quality"` // lifetime | legacy | none
	Root                  ArchiveWaterfallRow          `json:"root"`
	Requests              []ArchiveWaterfallRow        `json:"requests"` // by start, then ordinal
	RequestsTotal         int                          `json:"requests_total"`
	Truncated             bool                         `json:"truncated,omitempty"`
	LateRequests          int                          `json:"late_requests,omitempty"`
	Decomposition         *RunArchiveTimeDecomposition `json:"decomposition,omitempty"`          // Instances==1
	EnclosingTransactions []ArchiveWaterfallRef        `json:"enclosing_transactions,omitempty"` // root is a request; innermost first
	// Partial is set when the root identity's own state made the report's
	// instance partial: its per-identity buffer dropped a request this window
	// could have claimed (pending_key_cap), or the join's CPU budget ran out
	// before the root (work_budget). The report keeps a partial instance out
	// of its decomposition, rows and tail means, so a partial drill-down has
	// no decomposition either (its requests are still listed).
	Partial        bool     `json:"partial,omitempty"`
	PartialReasons []string `json:"partial_reasons,omitempty"`
	// JoinMayDiffer is set for a transaction root when the report's join hit
	// run-wide state the drill-down cannot replay from one identity
	// (JoinMayDifferReasons: pending_global_cap, identity_cap, work_budget),
	// or when the report could not be read (no reasons). The report may then
	// count this instance as partial, or claim fewer requests, than the
	// drill-down shows; requests_total exceeding the representative's
	// children is the per-instance sign. Partial and Decomposition do not
	// reflect run-wide state (a one-identity replay claims every request, so
	// Partial stays false and Decomposition is set), so join_may_differ is
	// the only signal a reader has that the report's instance may be partial.
	JoinMayDiffer        bool     `json:"join_may_differ,omitempty"`
	JoinMayDifferReasons []string `json:"join_may_differ_reasons,omitempty"`
	// TraceLinksUnavailable is set when the run's APM link settings could
	// not be read, so no row carries trace links.
	TraceLinksUnavailable bool `json:"trace_links_unavailable,omitempty"`
}

// ArchiveWaterfallRow is one row of a drill-down waterfall.
type ArchiveWaterfallRow struct {
	EventID           string               `json:"event_id"`
	Ordinal           int64                `json:"ordinal"`
	Population        string               `json:"population"`
	Timestamp         time.Time            `json:"timestamp"`
	OffsetMs          float64              `json:"offset_ms"`
	DurationMs        float64              `json:"duration_ms"`
	ElapsedMs         float64              `json:"elapsed_ms,omitempty"`
	ActionName        string               `json:"action_name"`
	SourceTransaction string               `json:"source_transaction,omitempty"`
	SourceStepIndex   int                  `json:"source_step_index,omitempty"`
	DependencyType    string               `json:"dependency_type,omitempty"`
	DependencyTarget  string               `json:"dependency_target,omitempty"`
	HTTPVersion       string               `json:"http_version,omitempty"`
	StatusCode        int                  `json:"status_code,omitempty"`
	Success           bool                 `json:"success"`
	Interrupted       bool                 `json:"interrupted,omitempty"`
	ErrorClass        string               `json:"error_class,omitempty"`
	ErrorMessage      string               `json:"error_message,omitempty"` // scrubbed, <=512 B
	Phases            RunArchivePhaseMeans `json:"phases"`                  // sums to DurationMs
	Clamped           bool                 `json:"clamped,omitempty"`
	Retried           bool                 `json:"retried,omitempty"`
	Connection        string               `json:"connection,omitempty"` // new | reused | unknown
	TraceID           string               `json:"trace_id,omitempty"`
	// TraceLinks open TraceID in the run's APM providers: expanded per
	// request from the test's validated trace link templates by the
	// transaction drill-down only (the iteration waterfall, which reuses
	// this row, leaves it empty), when the row has a trace id and a template
	// exists.
	TraceLinks    []ExemplarLink `json:"trace_links,omitempty"`
	VUserID       int            `json:"vuser_id,omitempty"`
	VUserLifetime uint64         `json:"vuser_lifetime,string,omitempty"`
	Iteration     int64          `json:"iteration,omitempty"`
}

// ArchiveWaterfallRef names a transaction that encloses a request root.
type ArchiveWaterfallRef struct {
	EventID    string    `json:"event_id"`
	Ordinal    int64     `json:"ordinal"`
	ActionName string    `json:"action_name"`
	Timestamp  time.Time `json:"timestamp"`
	SpanMs     float64   `json:"span_ms"`
}

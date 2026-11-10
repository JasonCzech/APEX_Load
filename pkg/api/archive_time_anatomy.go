package api

import (
	"cmp"
	"encoding/json"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/scrub"
)

// Time anatomy (R1/R2/R3) is built in the report's single pass over
// transactions.jsonl. Request rows wait in a per-VUser-lifetime buffer until
// the transaction row that encloses them is written (the engine writes a
// transaction after its requests), then every transaction row claims the
// buffered requests of the same identity that timeAnatomyClaimsRequest
// accepts.
//
// The buffer is bounded three ways, and each bound keeps the join honest:
//   - per identity (maxTimeAnatomyPendingPerVUser): the buffer keeps the
//     latest-starting requests; see timeAnatomyPendingBuffer.insertCapped.
//   - in total (maxTimeAnatomyPendingTotal): before a request is refused, the
//     least recently seen identities give up their retired requests (an
//     earlier iteration, or one an ends_iteration row closed), then whole
//     least recently seen identities are evicted. A VUser lifetime that ended
//     (every open-workload arrival is one) is never seen again, so it is
//     the first to go and no instance loses a child.
//   - in identities (maxTimeAnatomyIdentities): the least recently seen
//     identity is evicted to admit a new one.
//
// Every request dropped or evicted while it might still be claimed widens
// its identity's drop hull (timeAnatomyDropHull; it survives an identity
// eviction in a small tombstone table, and a tombstone a collision
// overwrites joins a lost hull that every later-created identity inherits).
// A transaction whose window overlaps the hull may have lost children, so it
// is counted as partial rather than joined and stays out of the
// decomposition, the request rows and the tail means. An evicted request is
// counted as evicted, never as outside a transaction.
//
// Buffered strings are bounded too (maxTimeAnatomyPendingBytes), and the
// join's CPU is bounded by timeAnatomyWorkBudget. Async-conversation
// responses and background requests never join (timeAnatomyRequestExcluded),
// and a request recorded during an async dispatch joins only a transaction
// opened in the same dispatch (timeAnatomyDispatchClaimable).
//
// The caps are package variables so tests can lower them.
var (
	maxTimeAnatomyTransactions       = 200
	maxTimeAnatomyReportTransactions = 40
	maxTimeAnatomyRowsPerTransaction = 48
	maxTimeAnatomyRows               = 256
	maxTimeAnatomyOccurrence         = 9
	maxTimeAnatomyPendingPerVUser    = 4096
	maxTimeAnatomyPendingTotal       = 131072
	timeAnatomyInstanceK             = 4096
	timeAnatomyInstanceKMin          = 512
	timeAnatomySampleBudgetBytes     = int64(48 << 20)
	maxTimeAnatomyIdentities         = 65536
	// maxTimeAnatomyPendingBytes bounds the strings the buffered requests
	// hold (action, dependency type, source_transaction), counted per entry
	// whether or not they are interned, next to the entry count.
	maxTimeAnatomyPendingBytes = int64(64 << 20)
	// maxTimeAnatomyWork is the floor of the report's CPU budget for the
	// join (timeAnatomyWorkBudget), which grows by maxTimeAnatomyWork /
	// timeAnatomyWorkRowsPerFloor units per merged row (256 by default), so
	// a large ordinary archive never runs out while a hostile one still
	// spends O(rows). A unit is about one candidate-scan step (a few ns), so
	// the allowance is about a microsecond of join CPU per row. Units are
	// buffer entries an insert moves, entries an iteration flush, legacy
	// restart or eviction walks, claim candidates a transaction row scans,
	// and timeAnatomyJoinedChildWork per child a joined instance folds into
	// its rows. An ordinary archive spends about 25 units per request per
	// enclosing transaction (each request is inserted once, walked by at
	// most two flushes, and scanned and folded once per enclosing
	// transaction); once the budget is spent the join stops, later requests
	// are dropped and later transactions are partial
	// (timeAnatomyReasonWorkBudget).
	maxTimeAnatomyWork = int64(1 << 26)
	// timeAnatomyWorkRowsPerFloor is how many merged rows add another
	// maxTimeAnatomyWork to the budget.
	timeAnatomyWorkRowsPerFloor = int64(1 << 18)
	// timeAnatomyJoinedChildWork is the work charged per claimed child of a
	// joined instance: mergeRows' map lookups and eight quantile adds, and
	// the decomposition's sort, cost about 25 candidate-scan steps per child
	// (measured: ~110 ns against ~4 ns).
	timeAnatomyJoinedChildWork = int64(24)
	// timeAnatomyTombstoneSlots is the size of the table that remembers the
	// drop hull of evicted identities. It is lossy (a colliding eviction
	// overwrites the slot), so an overwritten hull joins the accumulator's
	// lost hull, which every identity created later inherits.
	timeAnatomyTombstoneSlots = uint64(1 << 14)
)

const (
	timeAnatomyClosedRing = 8
	// timeAnatomyEpsilonNs is the clock slack (2 ms) on both window edges.
	timeAnatomyEpsilonNs = int64(2 * time.Millisecond)
	// Row accumulators exist up to 256 x 8 times per report, so they keep
	// fewer exact samples than ordinary report accumulators.
	timeAnatomyExactLimit = 256
	// The longest window the join considers, so nanosecond arithmetic cannot
	// overflow on a corrupt duration (about 116 days).
	timeAnatomyMaxSpanMs   = 1e10
	timeAnatomyInternLimit = 4096
	timeAnatomyChildBytes  = 20
	timeAnatomyTailMinimum = 40
	// timeAnatomyMaxIDBytes is the longest event_id / trace_id an instance
	// keeps; a longer one is not an engine identifier and is dropped.
	timeAnatomyMaxIDBytes = 256
	// timeAnatomyPendingSlack is the spare capacity a filtered buffer may
	// keep beyond twice its live entries before it is reallocated.
	timeAnatomyPendingSlack = 8
)

// Truncation reasons (closed set).
const (
	timeAnatomyReasonTransactionCap       = "transaction_cap"
	timeAnatomyReasonReportTransactionCap = "report_transaction_cap"
	timeAnatomyReasonRequestRowCap        = "request_row_cap"
	timeAnatomyReasonOccurrenceFold       = "occurrence_fold"
	timeAnatomyReasonPendingKeyCap        = "pending_key_cap"
	timeAnatomyReasonPendingGlobalCap     = "pending_global_cap"
	timeAnatomyReasonInstanceSample       = "instance_sample"
	timeAnatomyReasonSampleBudget         = "sample_budget"
	// timeAnatomyReasonIdentityCap: an identity evicted to admit a new one
	// (maxTimeAnatomyIdentities) held requests a later transaction could
	// have claimed.
	timeAnatomyReasonIdentityCap = "identity_cap"
	// timeAnatomyReasonWorkBudget: the join's CPU budget (maxTimeAnatomyWork)
	// ran out.
	timeAnatomyReasonWorkBudget = "work_budget"
)

// Identity qualities, as the drill-down reports them.
const (
	timeAnatomyIdentityLifetime = "lifetime"
	timeAnatomyIdentityLegacy   = "legacy"
	timeAnatomyIdentityNone     = "none"
)

// Phase indexes of every phase vector.
const (
	timeAnatomyPhaseDNS = iota
	timeAnatomyPhaseConnect
	timeAnatomyPhaseTLS
	timeAnatomyPhaseServer
	timeAnatomyPhaseDownload
	timeAnatomyPhaseUnsplit
	timeAnatomyPhaseCount
)

// Observed bits of a request: a raw positive measurement. A positive TTFB
// marks both server and download as observed.
const (
	timeAnatomyObservedDNS uint8 = 1 << iota
	timeAnatomyObservedConnect
	timeAnatomyObservedTLS
	timeAnatomyObservedTTFB
)

// Connection classes of a request row.
const (
	timeAnatomyConnectionUnknown uint8 = iota
	timeAnatomyConnectionNew
	timeAnatomyConnectionReused
)

func timeAnatomyConnectionName(class uint8) string {
	switch class {
	case timeAnatomyConnectionNew:
		return "new"
	case timeAnatomyConnectionReused:
		return "reused"
	}
	return "unknown"
}

// timeAnatomyPos is x when it is finite and positive, else 0.
func timeAnatomyPos(x float64) float64 {
	if x > 0 && !math.IsInf(x, 1) {
		return x
	}
	return 0
}

// archiveRequestPhases is one request's duration split into six parts that
// always sum to Duration, each part >= 0.
type archiveRequestPhases struct {
	Parts    [timeAnatomyPhaseCount]float64
	Duration float64
	Observed uint8
	Clamped  bool
}

// means returns the parts in the report's shape.
func (p archiveRequestPhases) means() RunArchivePhaseMeans {
	return timeAnatomyPhaseMeans(p.Parts)
}

func timeAnatomyPhaseMeans(parts [timeAnatomyPhaseCount]float64) RunArchivePhaseMeans {
	return RunArchivePhaseMeans{
		DNSMs:      parts[timeAnatomyPhaseDNS],
		ConnectMs:  parts[timeAnatomyPhaseConnect],
		TLSMs:      parts[timeAnatomyPhaseTLS],
		ServerMs:   parts[timeAnatomyPhaseServer],
		DownloadMs: parts[timeAnatomyPhaseDownload],
		UnsplitMs:  parts[timeAnatomyPhaseUnsplit],
	}
}

// archiveRequestPhaseSplit splits a request (the sequential_clipped_v1 phase
// model). DNS, connect and TLS are clipped in that order to the duration. A
// positive TTFB counts from request start, so server time is TTFB minus setup,
// clamped to what is left, and download is the rest; without a TTFB the time
// after setup is unsplit. Unlike the dashboard's phasesFromTraceTimings it
// never calls untimed time server time; the browser never re-splits archive
// rows.
func archiveRequestPhaseSplit(durationMs, dnsMs, connectMs, tlsMs, ttfbMs float64) archiveRequestPhases {
	var split archiveRequestPhases
	d := timeAnatomyPos(durationMs)
	split.Duration = d
	dns, connect, tls, ttfb := timeAnatomyPos(dnsMs), timeAnatomyPos(connectMs), timeAnatomyPos(tlsMs), timeAnatomyPos(ttfbMs)
	if dns > 0 {
		split.Observed |= timeAnatomyObservedDNS
	}
	if connect > 0 {
		split.Observed |= timeAnatomyObservedConnect
	}
	if tls > 0 {
		split.Observed |= timeAnatomyObservedTLS
	}
	if ttfb > 0 {
		split.Observed |= timeAnatomyObservedTTFB
	}
	clip := func(value, room float64) float64 {
		room = max(room, 0)
		if value > room {
			split.Clamped = true
			return room
		}
		return value
	}
	dns = clip(dns, d)
	connect = clip(connect, d-dns)
	tls = clip(tls, d-dns-connect)
	setup := dns + connect + tls
	split.Parts[timeAnatomyPhaseDNS] = dns
	split.Parts[timeAnatomyPhaseConnect] = connect
	split.Parts[timeAnatomyPhaseTLS] = tls
	rest := max(d-setup, 0)
	if ttfb > 0 {
		server := ttfb - setup
		if server < 0 {
			server = 0
			split.Clamped = true
		}
		if server > rest {
			server = rest
			split.Clamped = true
		}
		split.Parts[timeAnatomyPhaseServer] = server
		split.Parts[timeAnatomyPhaseDownload] = rest - server
	} else {
		split.Parts[timeAnatomyPhaseUnsplit] = rest
	}
	return split
}

// archiveRequestPhaseSplitOf splits an archived request row.
func archiveRequestPhaseSplitOf(tx *TransactionEvent) archiveRequestPhases {
	return archiveRequestPhaseSplit(tx.DurationMs, tx.DNSMs, tx.ConnectMs, tx.TLSMs, tx.TTFBMs)
}

// timeAnatomyConnectionClass classifies a request's connection: reused when
// the server telemetry says so and no connect time was measured, new when
// connect time was measured (inconsistent when the telemetry also claims
// reuse), unknown otherwise.
func timeAnatomyConnectionClass(tx *TransactionEvent) (class uint8, inconsistent bool) {
	reused := tx.ServerTelemetry != nil && tx.ServerTelemetry.ConnectionReused
	connect := timeAnatomyPos(tx.ConnectMs)
	switch {
	case connect > 0:
		return timeAnatomyConnectionNew, reused
	case reused:
		return timeAnatomyConnectionReused, false
	}
	return timeAnatomyConnectionUnknown, false
}

// timeAnatomyIdentity is the join key: the rows of one VUser lifetime.
type timeAnatomyIdentity struct {
	group    string
	vuserID  int
	lifetime uint64
}

// timeAnatomyIdentityOf returns a row's join identity and its quality. A row
// without a VUser ID and lifetime cannot be joined ("none"); a legacy row with
// a VUser ID but no lifetime joins on the ID alone ("legacy"), which a reused
// ID can confuse.
func timeAnatomyIdentityOf(group string, vuserID int, lifetime uint64) (timeAnatomyIdentity, string) {
	switch {
	case vuserID == 0 && lifetime == 0:
		return timeAnatomyIdentity{}, timeAnatomyIdentityNone
	case lifetime == 0:
		return timeAnatomyIdentity{group: group, vuserID: vuserID}, timeAnatomyIdentityLegacy
	}
	return timeAnatomyIdentity{group: group, vuserID: vuserID, lifetime: lifetime}, timeAnatomyIdentityLifetime
}

// timeAnatomyWindow is a row's [start, end] in Unix nanoseconds: the row
// timestamp is its start and the span is the larger of duration and elapsed.
// ok is false for a row without a timestamp.
func timeAnatomyWindow(timestamp time.Time, durationMs, elapsedMs float64) (startNs, endNs int64, ok bool) {
	if timestamp.IsZero() {
		return 0, 0, false
	}
	span := min(max(timeAnatomyPos(durationMs), timeAnatomyPos(elapsedMs)), timeAnatomyMaxSpanMs)
	startNs = timestamp.UnixNano()
	return startNs, startNs + timeAnatomyMsToNs(span), true
}

func timeAnatomyMsToNs(ms float64) int64 {
	return int64(math.Round(ms * 1e6))
}

// timeAnatomyClaims reports whether a request that started at childStartNs is
// a claim candidate of a transaction window: its start lies inside the
// window widened by 2 ms on both edges. It is only the candidate range (the
// buffer's search range); timeAnatomyClaimsRequest is the full claim rule,
// which also settles the start edge.
func timeAnatomyClaims(windowStartNs, windowEndNs, childStartNs int64) bool {
	return childStartNs >= windowStartNs-timeAnatomyEpsilonNs && childStartNs <= windowEndNs+timeAnatomyEpsilonNs
}

// timeAnatomyClaimWindow is a transaction row as the claim rule sees it.
type timeAnatomyClaimWindow struct {
	StartNs, EndNs int64
	// Name is the row's action_name: the transaction a request's
	// source_transaction names when the request's step lies inside it.
	Name string
	// DispatchEpoch is the async dispatch the transaction was opened in
	// (protocol_evidence async_dispatch_epoch), 0 for one opened outside
	// any dispatch.
	DispatchEpoch int64
}

// timeAnatomyClaimChild is a buffered request as the claim rule sees it.
type timeAnatomyClaimChild struct {
	StartNs           int64
	DurationMs        float64
	SourceTransaction string
	// DispatchEpoch is the async dispatch the request was recorded in
	// (protocol_evidence async_dispatch_epoch), 0 for main-flow work.
	DispatchEpoch int64
}

// timeAnatomyClaimWindowOf is a transaction row's claim window.
func timeAnatomyClaimWindowOf(tx *TransactionEvent, startNs, endNs int64) timeAnatomyClaimWindow {
	return timeAnatomyClaimWindow{StartNs: startNs, EndNs: endNs, Name: tx.ActionName, DispatchEpoch: timeAnatomyDispatchEpoch(tx)}
}

// timeAnatomyClaimChildOf is a request row as the claim rule sees it.
func timeAnatomyClaimChildOf(tx *TransactionEvent, startNs int64) timeAnatomyClaimChild {
	return timeAnatomyClaimChild{StartNs: startNs, DurationMs: tx.DurationMs, SourceTransaction: tx.SourceTransaction, DispatchEpoch: timeAnatomyDispatchEpoch(tx)}
}

// timeAnatomyHintUsable reports whether a source_transaction / action_name
// value can break a start-edge tie: it is set and was not scrubbed.
func timeAnatomyHintUsable(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && !strings.Contains(value, scrub.Redacted)
}

// timeAnatomyClaimsRequest is the join's claim rule: whether a transaction
// window claims a buffered request of the same identity.
//
//   - A request that starts inside [S, E+2ms] is claimed. There is no
//     iteration or source_transaction test, so nested, iteration-level and
//     auto transactions all claim the same request, as the runtime rollup
//     counts it.
//   - A request that starts in the 2 ms clock slack before S, [S-2ms, S), is
//     claimed only when it still runs at S: a sequential previous sibling
//     that ended at or before S is not. When both the request's
//     source_transaction and the window's name are set and unscrubbed, they
//     decide instead: the request is claimed only by the transaction its
//     step belongs to.
//   - A request recorded during an async dispatch (DispatchEpoch > 0) is
//     claimed only by a transaction opened in that same dispatch
//     (timeAnatomyDispatchClaimable), as the engine's sourceTransactionTakes
//     rolls it up.
//   - Anything else is not claimed.
func timeAnatomyClaimsRequest(window timeAnatomyClaimWindow, child timeAnatomyClaimChild) bool {
	if !timeAnatomyDispatchClaimable(window.DispatchEpoch, child.DispatchEpoch) {
		return false
	}
	if !timeAnatomyClaims(window.StartNs, window.EndNs, child.StartNs) {
		return false
	}
	if child.StartNs >= window.StartNs {
		return true
	}
	if timeAnatomyHintUsable(child.SourceTransaction) && timeAnatomyHintUsable(window.Name) {
		return strings.EqualFold(strings.TrimSpace(child.SourceTransaction), strings.TrimSpace(window.Name))
	}
	return child.StartNs+timeAnatomyMsToNs(min(timeAnatomyPos(child.DurationMs), timeAnatomyMaxSpanMs)) > window.StartNs
}

// timeAnatomyAsyncPhasePrefix is the result phase the engine's
// runAsyncCallbacks gives an http_async callback's steps ("async
// on_response", "async on_request", ...). Archives written before the engine
// stamped async_dispatch carry only this marker.
const timeAnatomyAsyncPhasePrefix = "async "

// timeAnatomyDispatchEpochKey is the protocol_evidence key the engine stamps
// on every result recorded during an async dispatch (next to
// async_dispatch=true) and on the business-transaction row of a transaction
// opened inside one: the dispatch's epoch, an integer > 0.
const timeAnatomyDispatchEpochKey = "async_dispatch_epoch"

// timeAnatomyMaxDispatchEpoch is the largest epoch a JSON number carries
// exactly (2^53).
const timeAnatomyMaxDispatchEpoch = int64(1) << 53

// timeAnatomyDispatchEpoch is a row's async dispatch epoch
// (protocol_evidence async_dispatch_epoch), or 0 when it has none: absent,
// not a positive integer, or too large to be exact. A decoded archive holds
// it as a float64; in-memory rows may hold an integer or a json.Number.
func timeAnatomyDispatchEpoch(tx *TransactionEvent) int64 {
	if tx == nil || tx.ProtocolEvidence == nil {
		return 0
	}
	var epoch int64
	switch value := tx.ProtocolEvidence[timeAnatomyDispatchEpochKey].(type) {
	case float64:
		if value != math.Trunc(value) || value < 1 || value > float64(timeAnatomyMaxDispatchEpoch) {
			return 0
		}
		epoch = int64(value)
	case int:
		epoch = int64(value)
	case int32:
		epoch = int64(value)
	case int64:
		epoch = value
	case uint32:
		epoch = int64(value)
	case uint64:
		if value > uint64(timeAnatomyMaxDispatchEpoch) {
			return 0
		}
		epoch = int64(value)
	case json.Number:
		parsed, err := strconv.ParseInt(value.String(), 10, 64)
		if err != nil {
			return 0
		}
		epoch = parsed
	default:
		return 0
	}
	if epoch < 1 || epoch > timeAnatomyMaxDispatchEpoch {
		return 0
	}
	return epoch
}

// timeAnatomyDispatchClaimable is the dispatch half of the claim rule: a
// main-flow request (childEpoch 0) may join any transaction, a request
// recorded during an async dispatch only one opened in that same dispatch.
// It mirrors the engine's sourceTransactionTakes.
func timeAnatomyDispatchClaimable(windowEpoch, childEpoch int64) bool {
	return childEpoch == 0 || childEpoch == windowEpoch
}

// timeAnatomyDispatchRecorded reports a row the engine recorded during an
// async dispatch: an http_async callback's step (phase "async <branch>") or
// any result stamped async_dispatch=true (a parallel branch a callback
// started included).
func timeAnatomyDispatchRecorded(tx *TransactionEvent) bool {
	if len(tx.Phase) >= len(timeAnatomyAsyncPhasePrefix) && strings.EqualFold(tx.Phase[:len(timeAnatomyAsyncPhasePrefix)], timeAnatomyAsyncPhasePrefix) {
		return true
	}
	dispatch, ok := tx.ProtocolEvidence["async_dispatch"].(bool)
	return ok && dispatch
}

// timeAnatomyRequestExcluded reports a request row that is never part of any
// transaction: an async-conversation response, a background request, or a
// result recorded during async dispatch that carries no dispatch epoch (an
// archive written before the engine stamped async_dispatch_epoch, where
// which transaction the dispatch opened cannot be told). Such a row is
// counted as outside any transaction and is never buffered or claimed.
//
// A dispatch-recorded request that carries its epoch is not excluded: it is
// buffered, and timeAnatomyClaimsRequest lets only a transaction opened in
// the same dispatch (the same async_dispatch_epoch) claim it, never one
// opened outside the dispatch. Together they mirror the engine's live rollup
// rule (VUser.resultOutsideSourceTransactions and sourceTransactionTakes),
// so the report and the drill-down (which must call both too) roll up
// exactly what the live phase rollup rolls up. The evidence keys read are
// async_conversation, background_request, async_dispatch and
// async_dispatch_epoch; Phase is read too.
func timeAnatomyRequestExcluded(tx *TransactionEvent) bool {
	if tx == nil {
		return false
	}
	if _, ok := tx.ProtocolEvidence["async_conversation"]; ok {
		return true
	}
	if background, ok := tx.ProtocolEvidence["background_request"].(bool); ok && background {
		return true
	}
	return timeAnatomyDispatchRecorded(tx) && timeAnatomyDispatchEpoch(tx) == 0
}

// timeAnatomyBoundedID keeps an event_id / trace_id only when it is no longer
// than an engine identifier can be.
func timeAnatomyBoundedID(value string) string {
	if len(value) > timeAnatomyMaxIDBytes {
		return ""
	}
	return value
}

// Drop-hull reasons: why a request left an identity's buffer while it might
// still be claimed.
const (
	timeAnatomyDropKeyCap uint8 = 1 << iota
	timeAnatomyDropGlobal
	timeAnatomyDropIdentityCap
	timeAnatomyDropWork
)

// timeAnatomyDropReasons maps a hull's reason bits to truncation reasons.
var timeAnatomyDropReasons = []struct {
	bit    uint8
	reason string
}{
	{timeAnatomyDropKeyCap, timeAnatomyReasonPendingKeyCap},
	{timeAnatomyDropGlobal, timeAnatomyReasonPendingGlobalCap},
	{timeAnatomyDropIdentityCap, timeAnatomyReasonIdentityCap},
	{timeAnatomyDropWork, timeAnatomyReasonWorkBudget},
}

// timeAnatomyDropHull is the start-time hull of the requests an identity's
// buffer lost while they might still be claimed. A transaction window that
// overlaps it may have lost children (it is partial, not joined).
type timeAnatomyDropHull struct {
	loNs, hiNs int64
	reasons    uint8
}

func (h *timeAnatomyDropHull) add(startNs int64, reason uint8) {
	if h.reasons == 0 {
		h.loNs, h.hiNs = startNs, startNs
	} else {
		h.loNs, h.hiNs = min(h.loNs, startNs), max(h.hiNs, startNs)
	}
	h.reasons |= reason
}

func (h *timeAnatomyDropHull) merge(other timeAnatomyDropHull) {
	if other.reasons == 0 {
		return
	}
	if h.reasons == 0 {
		*h = other
		return
	}
	h.loNs, h.hiNs = min(h.loNs, other.loNs), max(h.hiNs, other.hiNs)
	h.reasons |= other.reasons
}

// overlaps reports whether a transaction window [S, E] could have claimed a
// request in the hull (the claim candidate range, both edges widened).
func (h *timeAnatomyDropHull) overlaps(windowStartNs, windowEndNs int64) bool {
	return h.reasons != 0 && windowStartNs-timeAnatomyEpsilonNs <= h.hiNs && windowEndNs+timeAnatomyEpsilonNs >= h.loNs
}

// timeAnatomyEndsAfter reports whether a claimed request ran past its window.
func timeAnatomyEndsAfter(windowEndNs, childStartNs int64, childDurationMs float64) bool {
	return childStartNs+timeAnatomyMsToNs(min(timeAnatomyPos(childDurationMs), timeAnatomyMaxSpanMs)) > windowEndNs+timeAnatomyEpsilonNs
}

// timeAnatomyLateFor reports whether a request written after a transaction
// closed started inside that transaction's window (and so should have been
// claimed by it).
func timeAnatomyLateFor(windowStartNs, windowEndNs, childStartNs int64) bool {
	return childStartNs >= windowStartNs && childStartNs < windowEndNs-timeAnatomyEpsilonNs
}

// timeAnatomyIterationRetained reports whether a buffered request of
// childIteration is still claimable once a request of currentIteration was
// seen: requests of the previous iteration stay, because the engine's
// carryOpenSourceTransactions records a transaction started late in one
// iteration under the next one.
func timeAnatomyIterationRetained(childIteration, currentIteration int64) bool {
	return childIteration >= currentIteration-1
}

// timeAnatomySpan is one claimed request as the decomposition needs it.
type timeAnatomySpan struct {
	StartNs    int64
	DurationMs float64
	Phases     [timeAnatomyPhaseCount]float64
}

// timeAnatomyInstanceVector decomposes one transaction instance:
// duration = sum(Phases) + NonRequest - Overlap, exactly.
type timeAnatomyInstanceVector struct {
	Phases     [timeAnatomyPhaseCount]float64
	Serial     float64 // sum of the claimed requests' durations
	Covered    float64 // union of the requests clipped to the window
	Overlap    float64 // Serial - Covered, >= 0
	NonRequest float64 // duration - Covered, signed
	Children   int
}

type timeAnatomyInterval struct{ start, end float64 }

// timeAnatomyDecompose decomposes a transaction instance from its claimed
// requests. The drill-down endpoint uses it so its decomposition equals the
// report's instance vector. scratch may be nil.
func timeAnatomyDecompose(durationMs float64, windowStartNs, windowEndNs int64, children []timeAnatomySpan, scratch []timeAnatomyInterval) (timeAnatomyInstanceVector, []timeAnatomyInterval) {
	var vector timeAnatomyInstanceVector
	vector.Children = len(children)
	spanMs := float64(windowEndNs-windowStartNs) / 1e6
	intervals := scratch[:0]
	for _, child := range children {
		for phase, value := range child.Phases {
			vector.Phases[phase] += value
		}
		var childTotal float64
		for _, value := range child.Phases {
			childTotal += value
		}
		vector.Serial += childTotal
		offset := float64(child.StartNs-windowStartNs) / 1e6
		start := max(offset, 0)
		end := min(offset+timeAnatomyPos(child.DurationMs), spanMs)
		if end > start {
			intervals = append(intervals, timeAnatomyInterval{start: start, end: end})
		}
	}
	slices.SortFunc(intervals, func(left, right timeAnatomyInterval) int {
		return cmp.Or(cmp.Compare(left.start, right.start), cmp.Compare(left.end, right.end))
	})
	var covered float64
	for index := 0; index < len(intervals); {
		start, end := intervals[index].start, intervals[index].end
		index++
		for index < len(intervals) && intervals[index].start <= end {
			end = max(end, intervals[index].end)
			index++
		}
		covered += end - start
	}
	overlap := vector.Serial - covered
	if math.Abs(overlap) <= 1e-9*max(1, vector.Serial) || overlap < 0 {
		// Float noise from the nanosecond offsets; also never more covered
		// time than request time.
		covered, overlap = vector.Serial, 0
	}
	vector.Covered = covered
	vector.Overlap = overlap
	vector.NonRequest = timeAnatomyPos(durationMs) - covered
	return vector, intervals
}

// decomposition is the vector as a one-instance report decomposition.
func (v timeAnatomyInstanceVector) decomposition(durationMs float64) RunArchiveTimeDecomposition {
	return RunArchiveTimeDecomposition{
		Instances:              1,
		AvgDurationMs:          timeAnatomyPos(durationMs),
		Phases:                 timeAnatomyPhaseMeans(v.Phases),
		NonRequestMs:           v.NonRequest,
		OverlapMs:              v.Overlap,
		AvgRequestsPerInstance: float64(v.Children),
	}
}

// splitmix64 is the instance sample's priority hash.
func splitmix64(x uint64) uint64 {
	x += 0x9E3779B97F4A7C15
	x = (x ^ (x >> 30)) * 0xBF58476D1CE4E5B9
	x = (x ^ (x >> 27)) * 0x94D049BB133111EB
	return x ^ (x >> 31)
}

func timeAnatomyPriority(ordinal int64) uint64 {
	return splitmix64(uint64(ordinal) ^ 0x9E3779B97F4A7C15)
}

// kahanSum is a compensated streaming sum.
type kahanSum struct{ sum, c float64 }

func (k *kahanSum) add(value float64) {
	y := value - k.c
	t := k.sum + y
	k.c = (t - k.sum) - y
	k.sum = t
}

func (k kahanSum) value() float64 { return k.sum }

// timeAnatomyStat is a plain statistic: count, mean, max and quantiles.
type timeAnatomyStat struct {
	n   int64
	sum kahanSum
	max float64
	q   archiveQuantileAccumulator
}

func newTimeAnatomyStat() timeAnatomyStat {
	return timeAnatomyStat{q: archiveQuantileAccumulator{exactLimit: timeAnatomyExactLimit}}
}

func (s *timeAnatomyStat) add(value float64) {
	s.n++
	s.sum.add(value)
	if s.n == 1 || value > s.max {
		s.max = value
	}
	s.q.Add(value)
}

func (s *timeAnatomyStat) approximate() bool { return s.q.PercentileQuality() != "" }

func (s *timeAnatomyStat) result(quality string) RunArchivePhaseStat {
	if s.n == 0 {
		return RunArchivePhaseStat{}
	}
	return RunArchivePhaseStat{
		Samples:           s.n,
		AvgMs:             s.sum.value() / float64(s.n),
		P50Ms:             s.q.Quantile(.50),
		P90Ms:             s.q.Quantile(.90),
		P95Ms:             s.q.Quantile(.95),
		P99Ms:             s.q.Quantile(.99),
		MaxMs:             s.max,
		PercentileQuality: quality,
	}
}

// observedQuantile is a phase statistic over every sample, zeros included,
// that stores only the positive values: a phase measured on 10% of requests
// costs 10% of the samples. Its quantiles map each rank of the full
// population onto the positive values, so while those are exact the result
// equals an exact quantile over all samples.
type observedQuantile struct {
	n, observed      int64
	sum, observedSum kahanSum
	max              float64
	inner            archiveQuantileAccumulator
}

func newObservedQuantile() observedQuantile {
	return observedQuantile{inner: archiveQuantileAccumulator{exactLimit: timeAnatomyExactLimit}}
}

func (o *observedQuantile) add(value float64, observed bool) {
	o.n++
	o.sum.add(value)
	if observed {
		o.observed++
		o.observedSum.add(value)
	}
	if value > o.max {
		o.max = value
	}
	if value > 0 {
		o.inner.Add(value)
	}
}

func (o *observedQuantile) approximate() bool { return o.inner.PercentileQuality() != "" }

// quantile interpolates like percentileFromSortedSamples over the sorted full
// population: n-m zeros followed by the m positive values.
func (o *observedQuantile) quantile(q float64) float64 {
	m := o.inner.Count()
	if o.n == 0 || m == 0 {
		return 0
	}
	if math.IsNaN(q) || q <= 0 {
		if m < o.n {
			return 0
		}
		return o.inner.Quantile(0)
	}
	if q >= 1 {
		return o.max
	}
	zeros := float64(o.n - m)
	rank := q * float64(o.n-1)
	switch {
	case zeros > 0 && rank <= zeros-1:
		return 0
	case rank < zeros:
		return o.inner.Quantile(0) * (rank - (zeros - 1))
	case m == 1:
		return o.inner.Quantile(0)
	}
	return o.inner.Quantile((rank - zeros) / float64(m-1))
}

func (o *observedQuantile) result(quality string) RunArchivePhaseStat {
	if o.n == 0 {
		return RunArchivePhaseStat{}
	}
	observedPct := float64(o.observed) / float64(o.n) * 100
	stat := RunArchivePhaseStat{
		Samples:           o.n,
		AvgMs:             o.sum.value() / float64(o.n),
		P50Ms:             o.quantile(.50),
		P90Ms:             o.quantile(.90),
		P95Ms:             o.quantile(.95),
		P99Ms:             o.quantile(.99),
		MaxMs:             o.max,
		ObservedPct:       &observedPct,
		PercentileQuality: quality,
	}
	if o.observed > 0 {
		avg := o.observedSum.value() / float64(o.observed)
		stat.AvgWhenObservedMs = &avg
	}
	return stat
}

// Pending request flags.
const (
	timeAnatomyPendingSuccess uint8 = 1 << iota
	timeAnatomyPendingClaimed
	timeAnatomyPendingEndedAfter
	timeAnatomyPendingInconsistent
	// timeAnatomyPendingLate marks a request written after a transaction
	// whose window it started in had closed. It counts as a late request row
	// only if it leaves the buffer unclaimed: an enclosing transaction
	// written later can still claim it.
	timeAnatomyPendingLate
)

// timeAnatomyPendingRequest is a request row waiting for its transaction.
type timeAnatomyPendingRequest struct {
	ordinal   int64
	startNs   int64
	iteration int64
	// dispatchEpoch is the async dispatch the request was recorded in, 0
	// for main-flow work (timeAnatomyDispatchClaimable).
	dispatchEpoch int64
	durationMs    float64
	phases        [timeAnatomyPhaseCount]float64
	action        string
	depType       string
	sourceTx      string
	stepIndex     int32
	observed      uint8
	conn          uint8
	flags         uint8
}

func (p *timeAnatomyPendingRequest) has(flag uint8) bool { return p.flags&flag != 0 }

// bytes is the string bytes the entry holds, interned or not.
func (p *timeAnatomyPendingRequest) bytes() int64 {
	return int64(len(p.action) + len(p.depType) + len(p.sourceTx))
}

func (p *timeAnatomyPendingRequest) claimChild() timeAnatomyClaimChild {
	return timeAnatomyClaimChild{StartNs: p.startNs, DurationMs: p.durationMs, SourceTransaction: p.sourceTx, DispatchEpoch: p.dispatchEpoch}
}

// timeAnatomyPendingBuffer is one identity's buffered requests ordered by
// (start, ordinal). The live entries are items[head:]: dropping the earliest
// request only advances head, and the dead prefix is compacted once it is as
// long as the live part, so a full buffer costs O(1) amortized per request.
// Requests arrive nearly in start order, so an insert moves O(1) entries
// (insertCost says how many; the accumulator charges them to its work
// budget).
type timeAnatomyPendingBuffer struct {
	items []timeAnatomyPendingRequest
	head  int
	// minIteration and maxIteration bound the live entries' iterations
	// (meaningful only while len() > 0). filter makes them exact; dropping
	// the head only loosens them, so they stay valid bounds. They let an
	// iteration flush or a legacy restart that cannot release anything skip
	// its O(len) walk.
	minIteration, maxIteration int64
}

func (b *timeAnatomyPendingBuffer) len() int { return len(b.items) - b.head }

func (b *timeAnatomyPendingBuffer) compact() {
	if b.head == 0 {
		return
	}
	n := copy(b.items, b.items[b.head:])
	clear(b.items[n:])
	b.items, b.head = b.items[:n], 0
}

// insertAt is the absolute index a request starting at startNs is inserted
// at: after every buffered request that starts at or before it.
func (b *timeAnatomyPendingBuffer) insertAt(startNs int64) int {
	live := b.items[b.head:]
	if len(live) == 0 || live[len(live)-1].startNs <= startNs {
		return len(b.items)
	}
	at, _ := slices.BinarySearchFunc(live, startNs, func(entry timeAnatomyPendingRequest, target int64) int {
		if entry.startNs <= target {
			return -1
		}
		return 1
	})
	return b.head + at
}

// insertCost is how many buffered entries inserting a request that starts at
// startNs moves.
func (b *timeAnatomyPendingBuffer) insertCost(startNs int64) int {
	return len(b.items) - b.insertAt(startNs)
}

// insert adds a request in (start, ordinal) order. ordinal is larger than
// every buffered ordinal, so an equal start goes last.
func (b *timeAnatomyPendingBuffer) insert(pending timeAnatomyPendingRequest) {
	if b.len() == 0 {
		b.minIteration, b.maxIteration = pending.iteration, pending.iteration
	} else {
		b.minIteration, b.maxIteration = min(b.minIteration, pending.iteration), max(b.maxIteration, pending.iteration)
	}
	if b.head > 0 && b.head >= b.len() {
		b.compact()
	}
	at := b.insertAt(pending.startNs)
	b.items = append(b.items, timeAnatomyPendingRequest{})
	copy(b.items[at+1:], b.items[at:len(b.items)-1])
	b.items[at] = pending
}

// insertCapped is the per-identity cap rule: a buffer holding limit requests
// keeps the latest-starting ones, so either the incoming request (when it
// starts before every buffered one) or the earliest buffered request is
// dropped. It returns the dropped request's start.
func (b *timeAnatomyPendingBuffer) insertCapped(pending timeAnatomyPendingRequest, limit int) (droppedStartNs int64, dropped bool) {
	if b.len() >= limit && b.len() > 0 {
		if pending.startNs < b.items[b.head].startNs {
			return pending.startNs, true
		}
		droppedStartNs = b.items[b.head].startNs
		b.items[b.head] = timeAnatomyPendingRequest{}
		b.head++
		dropped = true
	}
	b.insert(pending)
	return droppedStartNs, dropped
}

// candidates returns the absolute index range [lo, hi) of items whose start
// lies in a window's claim candidate range (timeAnatomyClaims).
func (b *timeAnatomyPendingBuffer) candidates(windowStartNs, windowEndNs int64) (lo, hi int) {
	live := b.items[b.head:]
	from := windowStartNs - timeAnatomyEpsilonNs
	to := windowEndNs + timeAnatomyEpsilonNs
	lo, _ = slices.BinarySearchFunc(live, from, func(entry timeAnatomyPendingRequest, target int64) int {
		return cmp.Compare(entry.startNs, target)
	})
	upper, _ := slices.BinarySearchFunc(live[lo:], to, func(entry timeAnatomyPendingRequest, target int64) int {
		if entry.startNs <= target {
			return -1
		}
		return 1
	})
	return b.head + lo, b.head + lo + upper
}

// filter keeps the live entries keep accepts, in order, and passes every
// other one to drop. A buffer left far below its capacity is reallocated, so
// an identity whose peak iteration has passed does not keep the peak array:
// the buffers' capacity stays within twice their live entries plus a small
// slack each.
func (b *timeAnatomyPendingBuffer) filter(keep func(*timeAnatomyPendingRequest) bool, drop func(*timeAnatomyPendingRequest)) {
	kept := b.items[:0]
	for index := b.head; index < len(b.items); index++ {
		entry := &b.items[index]
		if keep(entry) {
			if len(kept) == 0 {
				b.minIteration, b.maxIteration = entry.iteration, entry.iteration
			} else {
				b.minIteration, b.maxIteration = min(b.minIteration, entry.iteration), max(b.maxIteration, entry.iteration)
			}
			kept = append(kept, *entry)
			continue
		}
		drop(entry)
	}
	clear(b.items[len(kept):])
	b.items, b.head = kept, 0
	b.trim()
}

// trim releases spare capacity: an empty buffer drops its array, and one
// whose capacity exceeds twice its length plus timeAnatomyPendingSlack is
// copied into a right-sized array.
func (b *timeAnatomyPendingBuffer) trim() {
	switch {
	case len(b.items) == 0:
		b.items, b.head = nil, 0
	case cap(b.items) > 2*len(b.items)+timeAnatomyPendingSlack:
		b.compact()
		b.items = slices.Clone(b.items)
	}
}

// timeAnatomyWindowSpan is a recently closed transaction window and the
// async dispatch it was opened in (0 outside any).
type timeAnatomyWindowSpan struct{ startNs, endNs, dispatchEpoch int64 }

// timeAnatomyIdentityState is one identity's buffer and recently closed
// transaction windows. States form a list from least to most recently seen.
type timeAnatomyIdentityState struct {
	key              timeAnatomyIdentity
	prev, next       *timeAnatomyIdentityState
	currentIteration int64
	hasIteration     bool
	// endedIteration is the latest iteration an ends_iteration row closed.
	endedIteration int64
	hasEnded       bool
	pending        timeAnatomyPendingBuffer
	hull           timeAnatomyDropHull
	// ownDrops is set once the identity's own requests widened its hull
	// (or a tombstone restored it). A hull inherited from the accumulator's
	// lost hull alone is not written back to a tombstone: the lost hull
	// only grows, so a recreated identity inherits it again.
	ownDrops bool
	// refused is the hull of the transaction windows the work budget
	// refused to scan (timeAnatomyRefusedWindows).
	refused     timeAnatomyRefusedWindows
	closed      [timeAnatomyClosedRing]timeAnatomyWindowSpan
	closedCount int
	closedNext  int
}

// timeAnatomyRefusedWindows is the hull of the transaction windows of one
// identity whose candidate scan the work budget refused. An unclaimed
// request a refused window might have claimed has an unknown placement
// (PendingRequestsDropped); any other unclaimed request ran outside every
// transaction, budget or not, because every window that could claim it was
// scanned. Main-flow windows (dispatch epoch 0) and dispatch windows keep
// separate hulls: a main-flow window never claims a dispatch request
// (timeAnatomyDispatchClaimable), so only a refused dispatch window can
// leave one's placement unknown.
type timeAnatomyRefusedWindows struct {
	main, dispatch timeAnatomyRefusedHull
}

// timeAnatomyRefusedHull is one hull of timeAnatomyRefusedWindows.
type timeAnatomyRefusedHull struct {
	loNs, hiNs int64
	set        bool
}

func (h *timeAnatomyRefusedHull) add(windowStartNs, windowEndNs int64) {
	if !h.set {
		h.loNs, h.hiNs, h.set = windowStartNs, windowEndNs, true
		return
	}
	h.loNs, h.hiNs = min(h.loNs, windowStartNs), max(h.hiNs, windowEndNs)
}

func (h *timeAnatomyRefusedHull) covers(childStartNs int64) bool {
	return h.set && timeAnatomyClaims(h.loNs, h.hiNs, childStartNs)
}

// add records a refused window opened in dispatch windowEpoch (0: main flow).
func (r *timeAnatomyRefusedWindows) add(windowStartNs, windowEndNs, windowEpoch int64) {
	if windowEpoch == 0 {
		r.main.add(windowStartNs, windowEndNs)
		return
	}
	r.dispatch.add(windowStartNs, windowEndNs)
}

// covers reports whether a refused window might have claimed a request of
// dispatch epoch childEpoch that started at childStartNs (the claim
// candidate range, both edges widened). Every refused window might claim a
// main-flow request; only a refused dispatch window might claim a dispatch
// one (the dispatch hull does not tell epochs apart, which errs towards
// unknown placement).
func (r *timeAnatomyRefusedWindows) covers(childStartNs, childEpoch int64) bool {
	if r.dispatch.covers(childStartNs) {
		return true
	}
	return childEpoch == 0 && r.main.covers(childStartNs)
}

// drop widens the identity's own drop hull.
func (s *timeAnatomyIdentityState) drop(startNs int64, reason uint8) {
	s.hull.add(startNs, reason)
	s.ownDrops = true
}

// timeAnatomyRetired reports whether a buffered request of iteration is
// retired for eviction: older than the identity's current iteration, or in
// an iteration (>= 1) an ends_iteration row closed. Only a transaction
// carried across the boundary (carryOpenSourceTransactions) can still claim
// it, so under pressure it goes before any live request. Iteration 0 (init
// and teardown) is never closed by an ends_iteration row: after an ended
// iteration it is teardown, or the init of a lifetime that reused a legacy
// VUser ID, and both are live.
func (s *timeAnatomyIdentityState) retired(iteration int64) bool {
	return (s.hasIteration && iteration < s.currentIteration) || (s.hasEnded && iteration >= 1 && iteration <= s.endedIteration)
}

// timeAnatomyTombstone remembers an evicted identity's drop hull, so a
// transaction it writes after coming back is still marked partial.
type timeAnatomyTombstone struct {
	hash uint64
	hull timeAnatomyDropHull
}

func timeAnatomyIdentityHash(identity timeAnatomyIdentity) uint64 {
	hash := uint64(14695981039346656037)
	for index := 0; index < len(identity.group); index++ {
		hash = (hash ^ uint64(identity.group[index])) * 1099511628211
	}
	hash = splitmix64(hash ^ uint64(identity.vuserID))
	hash = splitmix64(hash ^ identity.lifetime)
	return max(hash, 1)
}

func (s *timeAnatomyIdentityState) pushClosed(startNs, endNs, dispatchEpoch int64) {
	s.closed[s.closedNext] = timeAnatomyWindowSpan{startNs: startNs, endNs: endNs, dispatchEpoch: dispatchEpoch}
	s.closedNext = (s.closedNext + 1) % timeAnatomyClosedRing
	s.closedCount = min(s.closedCount+1, timeAnatomyClosedRing)
}

// late reports whether a request (recorded in dispatchEpoch, 0 for
// main-flow work) started inside a recently closed window that could have
// claimed it.
func (s *timeAnatomyIdentityState) late(startNs, dispatchEpoch int64) bool {
	for index := 0; index < s.closedCount; index++ {
		closed := &s.closed[index]
		if timeAnatomyDispatchClaimable(closed.dispatchEpoch, dispatchEpoch) && timeAnatomyLateFor(closed.startNs, closed.endNs, startNs) {
			return true
		}
	}
	return false
}

// timeAnatomyRowKey identifies one request row of a transaction.
type timeAnatomyRowKey struct {
	step       int32
	action     string
	occurrence int
}

// timeAnatomyRow accumulates one request row (R1).
type timeAnatomyRow struct {
	key          string
	action       string
	cell         int
	step         int
	occurrence   int
	folded       bool
	depType      string
	samples      int64
	failed       int64
	containing   int64
	lastInstance int64
	offset       timeAnatomyStat
	phases       [timeAnatomyPhaseDownload + 1]observedQuantile
	unsplit      timeAnatomyStat
	total        timeAnatomyStat
	connections  RunArchiveConnectionStats
}

func newTimeAnatomyRow(key timeAnatomyRowKey, depType string) *timeAnatomyRow {
	row := &timeAnatomyRow{
		action:       key.action,
		step:         int(key.step),
		occurrence:   key.occurrence,
		depType:      depType,
		lastInstance: -1,
		offset:       newTimeAnatomyStat(),
		unsplit:      newTimeAnatomyStat(),
		total:        newTimeAnatomyStat(),
	}
	for index := range row.phases {
		row.phases[index] = newObservedQuantile()
	}
	if key.occurrence > 0 {
		row.key = strconv.Itoa(int(key.step)) + "|" + key.action + "#" + strconv.Itoa(key.occurrence)
	}
	return row
}

func (r *timeAnatomyRow) add(child *timeAnatomyPendingRequest, offsetMs float64, instance int64) {
	r.samples++
	if !child.has(timeAnatomyPendingSuccess) {
		r.failed++
	}
	if r.lastInstance != instance {
		r.lastInstance = instance
		r.containing++
	}
	r.offset.add(offsetMs)
	observed := [timeAnatomyPhaseDownload + 1]bool{
		child.observed&timeAnatomyObservedDNS != 0,
		child.observed&timeAnatomyObservedConnect != 0,
		child.observed&timeAnatomyObservedTLS != 0,
		child.observed&timeAnatomyObservedTTFB != 0,
		child.observed&timeAnatomyObservedTTFB != 0,
	}
	for phase := range r.phases {
		r.phases[phase].add(child.phases[phase], observed[phase])
	}
	r.unsplit.add(child.phases[timeAnatomyPhaseUnsplit])
	r.total.add(child.durationMs)
	switch child.conn {
	case timeAnatomyConnectionNew:
		r.connections.New++
	case timeAnatomyConnectionReused:
		r.connections.Reused++
	default:
		r.connections.Unknown++
	}
	if child.has(timeAnatomyPendingInconsistent) {
		r.connections.Inconsistent++
	}
}

func (r *timeAnatomyRow) result(joinedInstances int64) RunArchiveAnatomyRequest {
	quality := ""
	approximate := r.offset.approximate() || r.unsplit.approximate() || r.total.approximate()
	for index := range r.phases {
		approximate = approximate || r.phases[index].approximate()
	}
	if approximate {
		quality = percentileQualityApproximate
	}
	request := RunArchiveAnatomyRequest{
		Key:              r.key,
		ActionName:       r.action,
		SourceStepIndex:  r.step,
		Occurrence:       r.occurrence,
		OccurrenceFolded: r.folded,
		DependencyType:   r.depType,
		Samples:          r.samples,
		Failed:           r.failed,
		Phases: RunArchiveAnatomyPhaseStats{
			DNS:      r.phases[timeAnatomyPhaseDNS].result(quality),
			Connect:  r.phases[timeAnatomyPhaseConnect].result(quality),
			TLS:      r.phases[timeAnatomyPhaseTLS].result(quality),
			Server:   r.phases[timeAnatomyPhaseServer].result(quality),
			Download: r.phases[timeAnatomyPhaseDownload].result(quality),
			Unsplit:  r.unsplit.result(quality),
			Total:    r.total.result(quality),
		},
		Connections: r.connections,
	}
	if r.occurrence > 0 {
		request.OffsetMs = r.offset.result(quality)
		if joinedInstances > 0 {
			request.PresencePct = float64(r.containing) / float64(joinedInstances) * 100
		}
	}
	if classified := r.connections.New + r.connections.Reused; classified > 0 {
		pct := float64(r.connections.Reused) / float64(classified) * 100
		request.Connections.ReusePct = &pct
	}
	return request
}

// timeAnatomyCell is one stored child of a sampled instance: its row and its
// phases folded to the four groups the request deltas compare.
type timeAnatomyCell struct {
	cell     uint16
	setup    float32
	server   float32
	download float32
	unsplit  float32
}

// timeAnatomyInstance is one sampled transaction instance (R2/R3).
type timeAnatomyInstance struct {
	priority     uint64
	ordinal      int64
	firstOrdinal int64
	timestampNs  int64
	durationMs   float64
	elapsedMs    float64
	phases       [timeAnatomyPhaseCount]float64
	nonRequest   float64
	overlap      float64
	eventID      string
	traceID      string
	errorClass   string
	group        string
	vuserID      int
	lifetime     uint64
	iteration    int64
	statusCode   int
	children     int32
	success      bool
	joined       bool
	cells        []timeAnatomyCell
}

var timeAnatomyInstanceBaseBytes = int64(unsafe.Sizeof(timeAnatomyInstance{}))

// bytes is what an instance holds outside its sample slot: its strings and
// its cells (by capacity). The slot itself is counted by the sample's
// capacity (timeAnatomyName.footprint).
func (i *timeAnatomyInstance) bytes() int64 {
	return int64(len(i.eventID)+len(i.traceID)) + int64(cap(i.cells))*timeAnatomyChildBytes
}

func (i *timeAnatomyInstance) representative(reason string) RunArchiveRepresentativeSample {
	sample := RunArchiveRepresentativeSample{
		Reason:        reason,
		EventID:       i.eventID,
		Ordinal:       i.ordinal,
		FirstOrdinal:  i.firstOrdinal,
		DurationMs:    i.durationMs,
		ElapsedMs:     i.elapsedMs,
		Success:       i.success,
		StatusCode:    i.statusCode,
		ErrorClass:    i.errorClass,
		Group:         i.group,
		VUserID:       i.vuserID,
		VUserLifetime: i.lifetime,
		Iteration:     i.iteration,
		TraceID:       i.traceID,
		Children:      int(i.children),
	}
	if i.timestampNs != 0 {
		// A row without a timestamp leaves it zero, which the JSON omits.
		sample.Timestamp = time.Unix(0, i.timestampNs).UTC()
	}
	return sample
}

// timeAnatomyName accumulates one business transaction name.
type timeAnatomyName struct {
	name        string
	instances   int64
	failed      int64
	interrupted int64
	joinedAll   int64
	partial     int64
	// Decomposition sums over joined, non-interrupted instances.
	joined        int64
	durationSum   kahanSum
	phaseSums     [timeAnatomyPhaseCount]kahanSum
	nonRequestSum kahanSum
	overlapSum    kahanSum
	requestCount  int64
	rows          map[timeAnatomyRowKey]*timeAnatomyRow
	rowList       []*timeAnatomyRow
	other         *timeAnatomyRow
	// requestsTruncated is set once a child fell outside the name's rows
	// (into other, or nowhere once the report's row budget was spent).
	requestsTruncated bool
	// sample is a max-heap on priority holding the bottom-k instances; its
	// capacity never exceeds k.
	sample []timeAnatomyInstance
	k      int
	// bytes is the sampled instances' bytes() (strings and cells).
	bytes   int64
	sampled bool
	slowest *timeAnatomyInstance
	failure *timeAnatomyInstance
}

func (n *timeAnatomyName) heapLess(left, right int) bool {
	return n.sample[left].priority > n.sample[right].priority
}

func (n *timeAnatomyName) heapUp(index int) {
	for index > 0 {
		parent := (index - 1) / 2
		if !n.heapLess(index, parent) {
			return
		}
		n.sample[index], n.sample[parent] = n.sample[parent], n.sample[index]
		index = parent
	}
}

func (n *timeAnatomyName) heapDown(index int) {
	for {
		largest := index
		if left := 2*index + 1; left < len(n.sample) && n.heapLess(left, largest) {
			largest = left
		}
		if right := 2*index + 2; right < len(n.sample) && n.heapLess(right, largest) {
			largest = right
		}
		if largest == index {
			return
		}
		n.sample[index], n.sample[largest] = n.sample[largest], n.sample[index]
		index = largest
	}
}

// footprint is the sample's memory: one instance slot per unit of capacity
// plus what the instances hold.
func (n *timeAnatomyName) footprint() int64 {
	return int64(cap(n.sample))*timeAnatomyInstanceBaseBytes + n.bytes
}

// popMax removes the largest priority and returns its bytes.
func (n *timeAnatomyName) popMax() int64 {
	last := len(n.sample) - 1
	removed := n.sample[0].bytes()
	n.sample[0] = n.sample[last]
	n.sample[last] = timeAnatomyInstance{}
	n.sample = n.sample[:last]
	if last > 0 {
		n.heapDown(0)
	}
	n.bytes -= removed
	return removed
}

// archiveTimeAnatomyAccumulator builds RunArchiveTimeAnatomy in the report's
// single pass. merge never fails: every cap truncates with a reason.
type archiveTimeAnatomyAccumulator struct {
	coverage         RunArchiveTimeAnatomyCoverage
	truncation       RunArchiveTimeAnatomyTruncation
	reasons          map[string]struct{}
	identities       map[timeAnatomyIdentity]*timeAnatomyIdentityState
	lruHead, lruTail *timeAnatomyIdentityState // least, most recently seen
	tombstones       []timeAnatomyTombstone
	// lostHull merges every tombstone a colliding eviction overwrote. An
	// identity created without a tombstone of its own inherits it, so an
	// evicted identity whose tombstone was lost still marks every
	// transaction that may have lost children as partial.
	lostHull     timeAnatomyDropHull
	pendingTotal int
	pendingBytes int64
	// work is the CPU spent on the join (timeAnatomyWorkBudget); once
	// workExhausted the join stops. rows counts the rows merged so far,
	// which the budget grows with.
	work          int64
	workExhausted bool
	rows          int64
	// evictedClaimed counts evicted (or per-identity cap displaced)
	// requests a transaction had already claimed (not reported: they are
	// in requests_in_transactions).
	evictedClaimed int64
	// releaseFn and releaseState are releaser's bound function.
	releaseFn        func(*timeAnatomyPendingRequest)
	releaseState     *timeAnatomyIdentityState
	names            map[string]*timeAnatomyName
	globalRows       int // request rows plus other rows
	realRows         int
	sampleBytes      int64
	intern           map[string]string
	scratchClaims    []int
	scratchSpans     []timeAnatomySpan
	scratchIntervals []timeAnatomyInterval
	scratchOccur     map[timeAnatomyRowKey]int
	scratchCellSums  []timeAnatomyCellSum
	scratchTouched   []int
}

// timeAnatomyCellSum folds one instance's children of one row.
type timeAnatomyCellSum struct {
	used   bool
	values [4]float64
}

func newArchiveTimeAnatomyAccumulator() *archiveTimeAnatomyAccumulator {
	return &archiveTimeAnatomyAccumulator{
		reasons:      map[string]struct{}{},
		identities:   map[timeAnatomyIdentity]*timeAnatomyIdentityState{},
		names:        map[string]*timeAnatomyName{},
		intern:       map[string]string{},
		scratchOccur: map[timeAnatomyRowKey]int{},
	}
}

func (a *archiveTimeAnatomyAccumulator) reason(reason string) {
	a.reasons[reason] = struct{}{}
}

func (a *archiveTimeAnatomyAccumulator) internString(value string) string {
	if canonical, ok := a.intern[value]; ok {
		return canonical
	}
	if len(a.intern) < timeAnatomyInternLimit {
		a.intern[value] = value
	}
	return value
}

// merge adds one archived row. ordinal is its line index in
// transactions.jsonl.
func (a *archiveTimeAnatomyAccumulator) merge(tx *TransactionEvent, population string, ordinal int64) {
	if a == nil || tx == nil {
		return
	}
	a.rows++
	switch population {
	case "action":
		a.mergeRequest(tx, ordinal)
	case "transaction":
		a.mergeTransaction(tx, ordinal)
	default:
		a.coverage.UnknownPopulationRows++
	}
}

func (a *archiveTimeAnatomyAccumulator) mergeRequest(tx *TransactionEvent, ordinal int64) {
	a.coverage.RequestRows++
	if timeAnatomyRequestExcluded(tx) {
		// An async-conversation response, a background request or an
		// unstamped dispatch request is never part of a transaction (the
		// live rollup's rule).
		a.coverage.RequestsOutsideTransactions++
		return
	}
	split := archiveRequestPhaseSplitOf(tx)
	if split.Clamped {
		a.coverage.ClampedRequestSamples++
	}
	if tx.AttemptCount > 1 || tx.RetryWaitMs > 0 {
		a.coverage.RetriedRequestSamples++
	}
	identity, quality := timeAnatomyIdentityOf(tx.Group, tx.VUserID, tx.VUserLifetime)
	if quality == timeAnatomyIdentityLegacy {
		a.coverage.LegacyIdentityRows++
	}
	startNs, _, ok := timeAnatomyWindow(tx.Timestamp, tx.DurationMs, 0)
	if quality == timeAnatomyIdentityNone || !ok {
		a.coverage.RequestsOutsideTransactions++
		return
	}
	state := a.state(identity)
	if !state.hasIteration || tx.Iteration != state.currentIteration {
		if quality == timeAnatomyIdentityLegacy {
			a.restartLegacyLifetime(state, tx.Iteration)
		}
		a.flushIteration(state, tx.Iteration)
		state.currentIteration, state.hasIteration = tx.Iteration, true
	}
	class, inconsistent := timeAnatomyConnectionClass(tx)
	pending := timeAnatomyPendingRequest{
		ordinal:       ordinal,
		startNs:       startNs,
		iteration:     tx.Iteration,
		dispatchEpoch: timeAnatomyDispatchEpoch(tx),
		durationMs:    split.Duration,
		phases:        split.Parts,
		action:        a.internString(tx.ActionName),
		depType:       a.internString(tx.DependencyType),
		sourceTx:      a.internString(tx.SourceTransaction),
		stepIndex:     int32(tx.SourceStepIndex),
		observed:      split.Observed,
		conn:          class,
	}
	if tx.Success {
		pending.flags |= timeAnatomyPendingSuccess
	}
	if inconsistent {
		pending.flags |= timeAnatomyPendingInconsistent
	}
	if state.late(startNs, pending.dispatchEpoch) {
		pending.flags |= timeAnatomyPendingLate
	}
	if !a.spendWork(int64(state.pending.insertCost(startNs)) + 1) {
		a.truncation.PendingRequestsDropped++
		a.leaveUnclaimed(&pending)
		state.drop(startNs, timeAnatomyDropWork)
		return
	}
	// A full identity buffer trades one request for another, so only a
	// request that grows the buffer meets the global entry cap; the string
	// byte cap applies to every request.
	full := state.pending.len() >= maxTimeAnatomyPendingPerVUser && state.pending.len() > 0
	entryBytes := pending.bytes()
	overGlobal := func() bool {
		return (!full && a.pendingTotal >= maxTimeAnatomyPendingTotal) || a.pendingBytes+entryBytes > maxTimeAnatomyPendingBytes
	}
	if overGlobal() {
		a.evictPending(state)
		full = state.pending.len() >= maxTimeAnatomyPendingPerVUser && state.pending.len() > 0
		if overGlobal() {
			a.truncation.PendingRequestsDropped++
			a.reason(timeAnatomyReasonPendingGlobalCap)
			a.leaveUnclaimed(&pending)
			state.drop(startNs, timeAnatomyDropGlobal)
			return
		}
	}
	// insertCapped drops the incoming request when it starts before every
	// buffered one, otherwise the earliest buffered one.
	var displaced *timeAnatomyPendingRequest
	displacedClaimed := false
	if full {
		if head := &state.pending.items[state.pending.head]; startNs < head.startNs {
			displaced = &pending
		} else {
			displaced = head
		}
		// The dropped request is counted as dropped, not as outside a
		// transaction. A displaced head a transaction already claimed is
		// in requests_in_transactions, so it is not counted as dropped
		// (of unknown placement) again; the hull still widens with it,
		// since an enclosing transaction could still have claimed it.
		a.leaveUnclaimed(displaced)
		displacedClaimed = displaced.has(timeAnatomyPendingClaimed)
		if displaced != &pending {
			a.pendingBytes -= displaced.bytes()
		}
	}
	before := state.pending.len()
	droppedStartNs, dropped := state.pending.insertCapped(pending, maxTimeAnatomyPendingPerVUser)
	if dropped && displacedClaimed {
		// Already matched: nothing was lost unless a later enclosing window
		// overlaps the drop, and mergeTransaction's hull check adds the
		// reason (and marks that instance partial) when one does.
		a.evictedClaimed++
	} else if dropped {
		a.truncation.PendingRequestsDropped++
		a.reason(timeAnatomyReasonPendingKeyCap)
	}
	if dropped {
		state.drop(droppedStartNs, timeAnatomyDropKeyCap)
	}
	if displaced != &pending {
		a.pendingBytes += entryBytes
	}
	a.pendingTotal += state.pending.len() - before
}

// timeAnatomyWorkBudget is the join's CPU budget once rows rows were merged:
// maxTimeAnatomyWork plus maxTimeAnatomyWork / timeAnatomyWorkRowsPerFloor
// units per row (saturating). The drill-down replay, which sees every row
// up to the root, calls it with the rows before the one it charges, so its
// budget is never below the report's at the same row.
func timeAnatomyWorkBudget(rows int64) int64 {
	floor := max(maxTimeAnatomyWork, 0)
	perRow := floor / max(timeAnatomyWorkRowsPerFloor, 1)
	rows = max(rows, 0)
	if perRow > 0 && rows > (math.MaxInt64-floor)/perRow {
		return math.MaxInt64
	}
	return floor + perRow*rows
}

// timeAnatomyJoinWork is the work a joined instance with claims children is
// charged before it folds them into its rows and decomposition.
func timeAnatomyJoinWork(claims int) int64 {
	return int64(claims) * timeAnatomyJoinedChildWork
}

// spendWork charges units to the join's CPU budget. It reports false, once
// and for every later call, when the budget is spent.
func (a *archiveTimeAnatomyAccumulator) spendWork(units int64) bool {
	if a.workExhausted {
		return false
	}
	if a.work+units > timeAnatomyWorkBudget(a.rows) {
		a.workExhausted = true
		a.reason(timeAnatomyReasonWorkBudget)
		return false
	}
	a.work += units
	return true
}

// restartLegacyLifetime handles a legacy identity (a VUser ID without a
// lifetime) whose ID a new VUser reused: a request of an iteration (>= 1) an
// ends_iteration row already closed, after a request of another iteration,
// cannot come from the same lifetime, whose iterations only move forward. The
// previous lifetime's requests of iteration >= 1 can no longer be claimed
// (its rows are all written), so they retire normally, and the ended mark is
// reset so the new lifetime's requests are not taken for retired ones.
// Iteration 0 (init and teardown) is left to the ordinary iteration flush.
//
// The walk is charged to the work budget; once the budget is spent the
// previous lifetime's requests stay buffered (no transaction claims anything
// any more) and finalize releases them.
func (a *archiveTimeAnatomyAccumulator) restartLegacyLifetime(state *timeAnatomyIdentityState, iteration int64) {
	if !state.hasIteration || !state.hasEnded || iteration < 1 || iteration > state.endedIteration {
		return
	}
	if state.pending.len() > 0 && state.pending.maxIteration >= 1 && a.spendWork(int64(state.pending.len())) {
		state.pending.filter(func(entry *timeAnatomyPendingRequest) bool {
			return entry.iteration < 1
		}, a.releaser(state))
	}
	state.hasEnded, state.endedIteration = false, 0
}

// state returns an identity's state, marking it the most recently seen. A
// new identity beyond maxTimeAnatomyIdentities evicts the least recently
// seen one.
func (a *archiveTimeAnatomyAccumulator) state(identity timeAnatomyIdentity) *timeAnatomyIdentityState {
	if state := a.identities[identity]; state != nil {
		a.unlink(state)
		a.pushBack(state)
		return state
	}
	for len(a.identities) >= max(maxTimeAnatomyIdentities, 1) && a.lruHead != nil {
		// The identity table must stay bounded, so this eviction runs even
		// once the budget is spent. Its walk is charged all the same: it
		// releases every entry it visits, so it is paid for once per
		// buffered request.
		a.spendWork(int64(a.lruHead.pending.len()) + 1)
		a.evictIdentity(a.lruHead, timeAnatomyDropIdentityCap)
	}
	state := &timeAnatomyIdentityState{key: identity}
	restored := false
	if a.tombstones != nil {
		hash := timeAnatomyIdentityHash(identity)
		if slot := &a.tombstones[hash%uint64(len(a.tombstones))]; slot.hash == hash {
			state.hull, state.ownDrops, restored = slot.hull, true, true
			*slot = timeAnatomyTombstone{}
		}
	}
	if !restored {
		// This may be an evicted identity whose tombstone was overwritten:
		// assume it lost what any lost tombstone held.
		state.hull = a.lostHull
	}
	a.identities[identity] = state
	a.pushBack(state)
	return state
}

func (a *archiveTimeAnatomyAccumulator) unlink(state *timeAnatomyIdentityState) {
	if state.prev != nil {
		state.prev.next = state.next
	} else {
		a.lruHead = state.next
	}
	if state.next != nil {
		state.next.prev = state.prev
	} else {
		a.lruTail = state.prev
	}
	state.prev, state.next = nil, nil
}

func (a *archiveTimeAnatomyAccumulator) pushBack(state *timeAnatomyIdentityState) {
	state.prev, state.next = a.lruTail, nil
	if a.lruTail != nil {
		a.lruTail.next = state
	} else {
		a.lruHead = state
	}
	a.lruTail = state
}

// evictIdentity releases an identity's buffered requests and forgets it. Its
// drop hull, widened (with reason) by every request it still held, goes to
// the tombstone table, so a transaction it writes later is marked partial. A
// tombstone the write overwrites joins the lost hull.
func (a *archiveTimeAnatomyAccumulator) evictIdentity(state *timeAnatomyIdentityState, reason uint8) {
	held := state.pending.len()
	for index := state.pending.head; index < len(state.pending.items); index++ {
		entry := &state.pending.items[index]
		a.releaseEvicted(entry)
		state.hull.add(entry.startNs, reason)
	}
	state.pending = timeAnatomyPendingBuffer{}
	if state.hull.reasons != 0 && (held > 0 || state.ownDrops) {
		if a.tombstones == nil {
			a.tombstones = make([]timeAnatomyTombstone, max(timeAnatomyTombstoneSlots, 1))
		}
		hash := timeAnatomyIdentityHash(state.key)
		slot := &a.tombstones[hash%uint64(len(a.tombstones))]
		if slot.hash != 0 && slot.hash != hash {
			a.lostHull.merge(slot.hull)
		}
		*slot = timeAnatomyTombstone{hash: hash, hull: state.hull}
	}
	a.unlink(state)
	delete(a.identities, state.key)
	a.truncation.IdentitiesEvicted++
}

// evictPending is the global cap's eviction rule, run before a request is
// refused. It frees an eighth of the caps (so it runs once per that many
// requests): first the retired requests of the least recently seen
// identities, then whole least recently seen identities other than current.
// An ended VUser lifetime is never seen again, so it is evicted before any
// live one.
//
// Every identity visited and every buffered entry walked is charged to the
// work budget, and eviction stops as soon as the budget is spent (the
// request that triggered it is then refused): a buffer that cannot get under
// target (current alone over it) would otherwise re-walk the whole list on
// every later request.
func (a *archiveTimeAnatomyAccumulator) evictPending(current *timeAnatomyIdentityState) {
	targetCount := maxTimeAnatomyPendingTotal - max(1, maxTimeAnatomyPendingTotal/8)
	targetBytes := maxTimeAnatomyPendingBytes - max(1, maxTimeAnatomyPendingBytes/8)
	over := func() bool { return a.pendingTotal > targetCount || a.pendingBytes > targetBytes }
	for state := a.lruHead; state != nil && over(); state = state.next {
		if !a.spendWork(int64(state.pending.len()) + 1) {
			return
		}
		if state.pending.len() == 0 {
			continue
		}
		state.pending.filter(func(entry *timeAnatomyPendingRequest) bool {
			return !over() || !state.retired(entry.iteration)
		}, func(entry *timeAnatomyPendingRequest) {
			a.releaseEvicted(entry)
			state.drop(entry.startNs, timeAnatomyDropGlobal)
		})
	}
	for state := a.lruHead; state != nil && over(); {
		next := state.next
		held := 0
		if state != current {
			held = state.pending.len()
		}
		if !a.spendWork(int64(held) + 1) {
			return
		}
		if held > 0 {
			a.evictIdentity(state, timeAnatomyDropGlobal)
		}
		state = next
	}
}

// flushIteration drops the buffered requests a request of iteration current
// retires (older than the previous iteration); the unclaimed ones ran outside
// any transaction. A buffer whose oldest iteration is still retained has
// nothing to drop and is not walked (rows alternating between two adjacent
// iterations would otherwise walk a full buffer per row). A walk is charged
// to the work budget; once the budget is spent the buffer is left as it is
// (it no longer grows) and finalize releases it.
func (a *archiveTimeAnatomyAccumulator) flushIteration(state *timeAnatomyIdentityState, current int64) {
	if state.pending.len() == 0 || timeAnatomyIterationRetained(state.pending.minIteration, current) {
		return
	}
	if !a.spendWork(int64(state.pending.len())) {
		return
	}
	state.pending.filter(func(entry *timeAnatomyPendingRequest) bool {
		return timeAnatomyIterationRetained(entry.iteration, current)
	}, a.releaser(state))
}

// release retires a buffered request of state that no transaction can claim
// any more (its iteration was flushed, or the pass ended): an unclaimed one
// ran outside any transaction. Once the work budget is spent, transactions
// stop claiming, so an unclaimed request a refused window might have
// claimed may well have run inside it: it is counted as dropped
// (PendingRequestsDropped), never as outside. One no refused window could
// claim was scanned by every window that could, so it is outside.
func (a *archiveTimeAnatomyAccumulator) release(state *timeAnatomyIdentityState, pending *timeAnatomyPendingRequest) {
	a.pendingTotal--
	a.pendingBytes -= pending.bytes()
	if !pending.has(timeAnatomyPendingClaimed) {
		if state.refused.covers(pending.startNs, pending.dispatchEpoch) {
			a.truncation.PendingRequestsDropped++
		} else {
			a.coverage.RequestsOutsideTransactions++
		}
	}
	a.leaveUnclaimed(pending)
}

// releaser is release bound to state, for timeAnatomyPendingBuffer.filter.
// The bound function is built once, so a flush does not allocate.
func (a *archiveTimeAnatomyAccumulator) releaser(state *timeAnatomyIdentityState) func(*timeAnatomyPendingRequest) {
	a.releaseState = state
	if a.releaseFn == nil {
		a.releaseFn = func(pending *timeAnatomyPendingRequest) { a.release(a.releaseState, pending) }
	}
	return a.releaseFn
}

// releaseEvicted releases a request evicted while a transaction might still
// claim it (the caller widens the drop hull with it, claimed or not: an
// enclosing transaction could still have claimed it). An unclaimed one is
// counted as evicted (PendingRequestsEvicted: whether it ran inside a
// transaction is unknown), never as outside any transaction. A claimed one
// is already counted in requests_in_transactions, so it is not counted
// again.
func (a *archiveTimeAnatomyAccumulator) releaseEvicted(pending *timeAnatomyPendingRequest) {
	a.pendingTotal--
	a.pendingBytes -= pending.bytes()
	if pending.has(timeAnatomyPendingClaimed) {
		a.evictedClaimed++
	} else {
		a.truncation.PendingRequestsEvicted++
	}
	a.leaveUnclaimed(pending)
}

// leaveUnclaimed counts a late request row that leaves the buffer (or is
// refused) without any transaction having claimed it: it was written after
// the transaction it started in, and is excluded from it and from every
// other.
func (a *archiveTimeAnatomyAccumulator) leaveUnclaimed(pending *timeAnatomyPendingRequest) {
	if pending.has(timeAnatomyPendingLate) && !pending.has(timeAnatomyPendingClaimed) {
		a.coverage.LateRequestRows++
	}
}

func (a *archiveTimeAnatomyAccumulator) mergeTransaction(tx *TransactionEvent, ordinal int64) {
	a.coverage.TransactionRows++
	if tx.Interrupted {
		a.coverage.InterruptedTransactionRows++
	}
	identity, quality := timeAnatomyIdentityOf(tx.Group, tx.VUserID, tx.VUserLifetime)
	startNs, endNs, ok := timeAnatomyWindow(tx.Timestamp, tx.DurationMs, tx.ElapsedMs)
	claims := a.scratchClaims[:0]
	var state *timeAnatomyIdentityState
	partial := false
	if quality == timeAnatomyIdentityNone || !ok {
		a.coverage.UnjoinableTransactionRows++
	} else {
		if quality == timeAnatomyIdentityLegacy {
			a.coverage.LegacyIdentityRows++
		}
		state = a.state(identity)
		window := timeAnatomyClaimWindowOf(tx, startNs, endNs)
		lo, hi := state.pending.candidates(startNs, endNs)
		if !a.spendWork(int64(hi-lo) + 1) {
			// The join's CPU budget is spent: the row claims nothing and,
			// since it may have had children, is partial. Every buffered
			// request it might have claimed is of unknown placement.
			partial = true
			hi = lo
			state.refused.add(startNs, endNs, window.DispatchEpoch)
		}
		for index := lo; index < hi; index++ {
			child := &state.pending.items[index]
			if !timeAnatomyClaimsRequest(window, child.claimChild()) {
				continue
			}
			claims = append(claims, index)
			if !child.has(timeAnatomyPendingClaimed) {
				child.flags |= timeAnatomyPendingClaimed
				a.coverage.RequestsInTransactions++
				if child.iteration != tx.Iteration {
					a.coverage.RequestsClaimedAcrossIteration++
				}
			}
			if !child.has(timeAnatomyPendingEndedAfter) && timeAnatomyEndsAfter(endNs, child.startNs, child.durationMs) {
				child.flags |= timeAnatomyPendingEndedAfter
				a.coverage.RequestsEndingAfterTransaction++
			}
		}
		state.pushClosed(startNs, endNs, window.DispatchEpoch)
		if tx.EndsIteration && !tx.Interrupted && (!state.hasEnded || tx.Iteration > state.endedIteration) {
			state.endedIteration, state.hasEnded = tx.Iteration, true
		}
		if state.hull.overlaps(startNs, endNs) {
			partial = true
			for _, drop := range timeAnatomyDropReasons {
				if state.hull.reasons&drop.bit != 0 {
					a.reason(drop.reason)
				}
			}
		}
		// A joined instance folds every claimed child into its rows and
		// decomposition; that is charged too. When the budget cannot pay
		// for it, the instance is partial (its children stay claimed).
		if !partial && len(claims) > 0 && !tx.Interrupted && !a.spendWork(timeAnatomyJoinWork(len(claims))) {
			partial = true
		}
		switch {
		case partial:
			a.coverage.PartialTransactionRows++
		case len(claims) > 0:
			a.coverage.JoinedTransactionRows++
		default:
			a.coverage.TransactionRowsWithoutRequests++
		}
	}
	a.scratchClaims = claims
	a.mergeInstance(tx, ordinal, state, claims, partial, startNs, endNs)
}

// mergeInstance adds one transaction instance. claims index the identity's
// buffer in (start, ordinal) order. A partial instance (one that may have
// lost children to a drop or eviction) is not joined.
func (a *archiveTimeAnatomyAccumulator) mergeInstance(tx *TransactionEvent, ordinal int64, state *timeAnatomyIdentityState, claims []int, partial bool, startNs, endNs int64) {
	name := a.names[tx.ActionName]
	if name == nil {
		if len(a.names) >= maxTimeAnatomyTransactions {
			a.truncation.TransactionsOmitted++
			a.reason(timeAnatomyReasonTransactionCap)
			return
		}
		name = &timeAnatomyName{name: tx.ActionName, rows: map[timeAnatomyRowKey]*timeAnatomyRow{}, k: timeAnatomyInstanceK}
		a.names[tx.ActionName] = name
	}
	name.instances++
	if !tx.Success {
		name.failed++
	}
	joined := len(claims) > 0 && !partial
	if joined {
		name.joinedAll++
	}
	if partial {
		name.partial++
	}
	if tx.Interrupted {
		name.interrupted++
		return
	}
	duration := timeAnatomyPos(tx.DurationMs)
	instance := timeAnatomyInstance{
		priority:     timeAnatomyPriority(ordinal),
		ordinal:      ordinal,
		firstOrdinal: ordinal,
		timestampNs:  startNs,
		durationMs:   tx.DurationMs,
		elapsedMs:    tx.ElapsedMs,
		eventID:      timeAnatomyBoundedID(tx.EventID),
		group:        a.internString(tx.Group),
		vuserID:      tx.VUserID,
		lifetime:     tx.VUserLifetime,
		iteration:    tx.Iteration,
		statusCode:   tx.StatusCode,
		children:     int32(len(claims)),
		success:      tx.Success,
		joined:       joined,
	}
	if !tx.Timestamp.IsZero() {
		instance.timestampNs = tx.Timestamp.UnixNano()
	}
	if tx.TraceSent != nil && *tx.TraceSent {
		instance.traceID = timeAnatomyBoundedID(tx.TraceID)
	}
	if !tx.Success {
		instance.errorClass = engine.ClassifyError(tx.StatusCode, tx.ErrorMessage, tx.Success)
	}
	var pending []timeAnatomyPendingRequest
	if state != nil {
		pending = state.pending.items
	}
	for _, index := range claims {
		instance.firstOrdinal = min(instance.firstOrdinal, pending[index].ordinal)
	}
	if instance.joined {
		spans := a.scratchSpans[:0]
		for _, index := range claims {
			child := &pending[index]
			spans = append(spans, timeAnatomySpan{StartNs: child.startNs, DurationMs: child.durationMs, Phases: child.phases})
		}
		var vector timeAnatomyInstanceVector
		vector, a.scratchIntervals = timeAnatomyDecompose(duration, startNs, endNs, spans, a.scratchIntervals)
		a.scratchSpans = spans
		instance.phases, instance.nonRequest, instance.overlap = vector.Phases, vector.NonRequest, vector.Overlap
		name.joined++
		name.durationSum.add(duration)
		for phase, value := range vector.Phases {
			name.phaseSums[phase].add(value)
		}
		name.nonRequestSum.add(vector.NonRequest)
		name.overlapSum.add(vector.Overlap)
		name.requestCount += int64(len(claims))
		instance.cells = a.mergeRows(name, pending, claims, startNs, ordinal)
	}
	if name.slowest == nil || tx.DurationMs > name.slowest.durationMs {
		slowest := instance
		slowest.cells = nil
		name.slowest = &slowest
	}
	if !tx.Success && name.failure == nil {
		failure := instance
		failure.cells = nil
		name.failure = &failure
	}
	a.insertSample(name, instance)
}

// timeAnatomyOtherRowReserve is the part of maxTimeAnatomyRows only "Other
// requests" rows may use, so a name that finds the request rows spent still
// gets one. Other rows count against maxTimeAnatomyRows too.
func timeAnatomyOtherRowReserve() int {
	return maxTimeAnatomyRows / 8
}

// mergeRows feeds the claimed children (sorted by start, then ordinal) into
// the name's request rows and returns the cells a sampled instance stores:
// one per row, summing its children (the tail only reads per-row sums), so
// an instance holds at most maxTimeAnatomyRowsPerTransaction cells however
// many children it has.
func (a *archiveTimeAnatomyAccumulator) mergeRows(name *timeAnatomyName, pending []timeAnatomyPendingRequest, claims []int, startNs, instance int64) []timeAnatomyCell {
	clear(a.scratchOccur)
	sums := a.scratchCellSums
	touched := a.scratchTouched[:0]
	for _, index := range claims {
		child := &pending[index]
		base := timeAnatomyRowKey{step: child.stepIndex, action: child.action}
		occurrence := a.scratchOccur[base] + 1
		a.scratchOccur[base] = occurrence
		folded := false
		if occurrence > maxTimeAnatomyOccurrence {
			occurrence, folded = maxTimeAnatomyOccurrence, true
			a.reason(timeAnatomyReasonOccurrenceFold)
		}
		key := timeAnatomyRowKey{step: child.stepIndex, action: child.action, occurrence: occurrence}
		row := name.rows[key]
		if row == nil && len(name.rowList) < maxTimeAnatomyRowsPerTransaction && a.globalRows < maxTimeAnatomyRows && a.realRows < maxTimeAnatomyRows-timeAnatomyOtherRowReserve() {
			row = newTimeAnatomyRow(key, child.depType)
			row.cell = len(name.rowList)
			name.rows[key] = row
			name.rowList = append(name.rowList, row)
			a.globalRows++
			a.realRows++
		}
		offsetMs := max(float64(child.startNs-startNs)/1e6, 0)
		if row == nil {
			if name.other == nil && a.globalRows < maxTimeAnatomyRows {
				name.other = newTimeAnatomyRow(timeAnatomyRowKey{action: "Other requests"}, "")
				name.other.key = "other"
				a.globalRows++
			}
			if name.other != nil {
				name.other.add(child, offsetMs, instance)
			}
			name.requestsTruncated = true
			a.truncation.RequestRowsOmitted++
			a.reason(timeAnatomyReasonRequestRowCap)
			continue
		}
		if folded {
			row.folded = true
		}
		row.add(child, offsetMs, instance)
		for len(sums) <= row.cell {
			sums = append(sums, timeAnatomyCellSum{})
		}
		sum := &sums[row.cell]
		if !sum.used {
			sum.used = true
			touched = append(touched, row.cell)
		}
		sum.values[0] += child.phases[timeAnatomyPhaseDNS] + child.phases[timeAnatomyPhaseConnect] + child.phases[timeAnatomyPhaseTLS]
		sum.values[1] += child.phases[timeAnatomyPhaseServer]
		sum.values[2] += child.phases[timeAnatomyPhaseDownload]
		sum.values[3] += child.phases[timeAnatomyPhaseUnsplit]
	}
	var cells []timeAnatomyCell
	if len(touched) > 0 {
		cells = make([]timeAnatomyCell, 0, len(touched))
		for _, cell := range touched {
			sum := &sums[cell]
			cells = append(cells, timeAnatomyCell{
				cell:     uint16(cell),
				setup:    float32(sum.values[0]),
				server:   float32(sum.values[1]),
				download: float32(sum.values[2]),
				unsplit:  float32(sum.values[3]),
			})
			*sum = timeAnatomyCellSum{}
		}
	}
	a.scratchCellSums, a.scratchTouched = sums, touched
	return cells
}

// insertSample keeps the name's bottom-k instances by priority and the
// report's instance samples under their byte budget, which is hard: every
// sample counts its capacity, and when no sample can shrink to k_min the
// largest shrinks below it (timeAnatomyReasonSampleBudget says so).
func (a *archiveTimeAnatomyAccumulator) insertSample(name *timeAnatomyName, instance timeAnatomyInstance) {
	before := name.footprint()
	switch {
	case len(name.sample) < name.k:
	case instance.priority < name.sample[0].priority:
		name.popMax()
		name.sampled = true
		a.reason(timeAnatomyReasonInstanceSample)
	default:
		name.sampled = true
		a.reason(timeAnatomyReasonInstanceSample)
		return
	}
	if len(name.sample) == cap(name.sample) {
		grown := make([]timeAnatomyInstance, len(name.sample), min(max(2*cap(name.sample), 8), name.k))
		copy(grown, name.sample)
		name.sample = grown
	}
	name.sample = append(name.sample, instance)
	name.heapUp(len(name.sample) - 1)
	name.bytes += instance.bytes()
	a.sampleBytes += name.footprint() - before
	for a.sampleBytes > timeAnatomySampleBudgetBytes {
		if !a.shrinkLargestSample() {
			return
		}
	}
}

// shrinkLargestSample halves k for the sample holding the most bytes (ties by
// name), evicts its largest priorities and gives back the spare capacity.
// Samples above k_min shrink first; once none is, samples shrink below k_min
// (down to one instance), so the budget stays hard. It reports false when no
// sample can shrink further.
func (a *archiveTimeAnatomyAccumulator) shrinkLargestSample() bool {
	for _, floor := range []int{timeAnatomyInstanceKMin, 1} {
		var largest *timeAnatomyName
		for _, name := range a.names {
			if name.k <= floor || len(name.sample) == 0 {
				continue
			}
			if largest == nil || name.footprint() > largest.footprint() || (name.footprint() == largest.footprint() && name.name < largest.name) {
				largest = name
			}
		}
		if largest == nil {
			continue
		}
		before := largest.footprint()
		largest.k = max(largest.k/2, floor)
		a.reason(timeAnatomyReasonSampleBudget)
		for len(largest.sample) > largest.k {
			largest.popMax()
			largest.sampled = true
			a.reason(timeAnatomyReasonInstanceSample)
		}
		if cap(largest.sample) > largest.k {
			shrunk := make([]timeAnatomyInstance, len(largest.sample), largest.k)
			copy(shrunk, largest.sample)
			largest.sample = shrunk
		}
		a.sampleBytes += largest.footprint() - before
		return true
	}
	return false
}

// finalize returns the anatomy, or nil when the archive has no business
// transaction rows. named holds the report's per-transaction duration
// accumulators (the SLA rows' numbers); slaStatus maps a name to "failed",
// "passed" or "".
func (a *archiveTimeAnatomyAccumulator) finalize(named map[string]*archivePopulationAccumulator, slaStatus map[string]string) *RunArchiveTimeAnatomy {
	if a == nil || a.coverage.TransactionRows == 0 {
		return nil
	}
	identities := make([]timeAnatomyIdentity, 0, len(a.identities))
	for identity := range a.identities {
		identities = append(identities, identity)
	}
	slices.SortFunc(identities, func(left, right timeAnatomyIdentity) int {
		return cmp.Or(strings.Compare(left.group, right.group), cmp.Compare(left.vuserID, right.vuserID), cmp.Compare(left.lifetime, right.lifetime))
	})
	for _, identity := range identities {
		state := a.identities[identity]
		for index := state.pending.head; index < len(state.pending.items); index++ {
			a.release(state, &state.pending.items[index])
		}
		state.pending = timeAnatomyPendingBuffer{}
	}

	type candidate struct {
		name   *timeAnatomyName
		status string
		rank   int
		weight float64
	}
	candidates := make([]candidate, 0, len(a.names))
	for _, name := range a.names {
		status := slaStatus[name.name]
		rank := 2
		switch status {
		case "failed":
			rank = 0
		case "passed":
			rank = 1
		}
		var weight float64
		if acc := named[name.name]; acc != nil {
			weight = acc.digest.Quantile(.95) * float64(name.instances)
		}
		candidates = append(candidates, candidate{name: name, status: status, rank: rank, weight: weight})
	}
	slices.SortFunc(candidates, func(left, right candidate) int {
		return cmp.Or(cmp.Compare(left.rank, right.rank), cmp.Compare(right.weight, left.weight), strings.Compare(left.name.name, right.name.name))
	})
	a.truncation.TransactionsTracked = len(candidates)
	if len(candidates) > maxTimeAnatomyReportTransactions {
		a.truncation.TransactionsNotEmitted = len(candidates) - maxTimeAnatomyReportTransactions
		a.reason(timeAnatomyReasonReportTransactionCap)
		candidates = candidates[:maxTimeAnatomyReportTransactions]
	}
	anatomy := &RunArchiveTimeAnatomy{
		Schema:       timeAnatomySchema,
		PhaseModel:   timeAnatomyPhaseModel,
		JoinRule:     timeAnatomyJoinRule,
		Transactions: make([]RunArchiveTransactionAnatomy, 0, len(candidates)),
		Coverage:     a.coverage,
	}
	for _, item := range candidates {
		anatomy.Transactions = append(anatomy.Transactions, item.name.result(named[item.name.name], item.status))
	}
	anatomy.Truncation = a.truncation
	if len(a.reasons) > 0 {
		anatomy.Truncation.Reasons = make([]string, 0, len(a.reasons))
		for reason := range a.reasons {
			anatomy.Truncation.Reasons = append(anatomy.Truncation.Reasons, reason)
		}
		slices.Sort(anatomy.Truncation.Reasons)
	}
	anatomy.Truncation.Truncated = len(anatomy.Truncation.Reasons) > 0
	return anatomy
}

// timeAnatomyDurationStat copies the report's per-transaction duration: the
// same numbers as the transaction's SLA row.
func timeAnatomyDurationStat(acc *archivePopulationAccumulator) RunArchivePhaseStat {
	if acc == nil || acc.stats.Transactions == 0 {
		return RunArchivePhaseStat{}
	}
	return RunArchivePhaseStat{
		Samples:           acc.stats.Transactions,
		AvgMs:             acc.total / float64(acc.stats.Transactions),
		P50Ms:             acc.digest.Quantile(.50),
		P90Ms:             acc.digest.Quantile(.90),
		P95Ms:             acc.digest.Quantile(.95),
		P99Ms:             acc.digest.Quantile(.99),
		MaxMs:             acc.stats.MaxResponseMs,
		PercentileQuality: acc.digest.PercentileQuality(),
	}
}

func (n *timeAnatomyName) result(durations *archivePopulationAccumulator, slaStatus string) RunArchiveTransactionAnatomy {
	entry := RunArchiveTransactionAnatomy{
		Name:                 n.name,
		Instances:            n.instances,
		JoinedInstances:      n.joinedAll,
		PartialInstances:     n.partial,
		FailedInstances:      n.failed,
		InterruptedInstances: n.interrupted,
		SLAStatus:            slaStatus,
		Duration:             timeAnatomyDurationStat(durations),
		Requests:             make([]RunArchiveAnatomyRequest, 0, len(n.rowList)),
	}
	if n.joined > 0 {
		count := float64(n.joined)
		var phases [timeAnatomyPhaseCount]float64
		for phase := range phases {
			phases[phase] = n.phaseSums[phase].value() / count
		}
		entry.Decomposition = RunArchiveTimeDecomposition{
			Instances:              n.joined,
			AvgDurationMs:          n.durationSum.value() / count,
			Phases:                 timeAnatomyPhaseMeans(phases),
			NonRequestMs:           n.nonRequestSum.value() / count,
			OverlapMs:              n.overlapSum.value() / count,
			AvgRequestsPerInstance: float64(n.requestCount) / count,
		}
	}
	for _, row := range n.rowList {
		entry.Requests = append(entry.Requests, row.result(n.joined))
	}
	slices.SortStableFunc(entry.Requests, func(left, right RunArchiveAnatomyRequest) int {
		leftStep, rightStep := left.SourceStepIndex, right.SourceStepIndex
		if leftStep == 0 {
			leftStep = math.MaxInt
		}
		if rightStep == 0 {
			rightStep = math.MaxInt
		}
		return cmp.Or(
			cmp.Compare(left.OffsetMs.P50Ms, right.OffsetMs.P50Ms),
			cmp.Compare(leftStep, rightStep),
			strings.Compare(left.ActionName, right.ActionName),
			cmp.Compare(left.Occurrence, right.Occurrence),
		)
	})
	if n.other != nil {
		other := n.other.result(n.joined)
		entry.OtherRequests = &other
	}
	entry.RequestsTruncated = n.requestsTruncated || n.other != nil
	if n.instances > 0 {
		slices.SortFunc(n.sample, func(left, right timeAnatomyInstance) int {
			return cmp.Compare(left.ordinal, right.ordinal)
		})
		tail := n.tail(durations)
		entry.Tail = tail
		entry.Representatives = n.representatives(tail)
	}
	return entry
}

// Tail delta components, in the fixed tie order.
var timeAnatomyComponents = []string{"dns", "connect", "tls", "server", "download", "unsplit", "non_request", "overlap"}

var timeAnatomyComponentLabels = map[string]string{
	"dns":         "DNS",
	"connect":     "connect",
	"tls":         "TLS",
	"server":      "server",
	"download":    "download",
	"unsplit":     "untimed request time",
	"non_request": "time outside requests",
	"overlap":     "overlap",
}

type timeAnatomyBandSums struct {
	band       RunArchiveTailBand
	duration   float64
	phases     [timeAnatomyPhaseCount]float64
	nonRequest float64
	overlap    float64
	cells      [][4]float64
}

func (b *timeAnatomyBandSums) add(instance *timeAnatomyInstance) {
	b.band.Count++
	if !instance.success {
		b.band.Failed++
	}
	if !instance.joined {
		return
	}
	b.band.Joined++
	b.duration += timeAnatomyPos(instance.durationMs)
	for phase, value := range instance.phases {
		b.phases[phase] += value
	}
	b.nonRequest += instance.nonRequest
	b.overlap += instance.overlap
	for _, cell := range instance.cells {
		if int(cell.cell) < len(b.cells) {
			sums := &b.cells[cell.cell]
			sums[0] += float64(cell.setup)
			sums[1] += float64(cell.server)
			sums[2] += float64(cell.download)
			sums[3] += float64(cell.unsplit)
		}
	}
}

func (b *timeAnatomyBandSums) finish() RunArchiveTailBand {
	band := b.band
	if band.Joined == 0 {
		return band
	}
	count := float64(band.Joined)
	var phases [timeAnatomyPhaseCount]float64
	for phase := range phases {
		phases[phase] = b.phases[phase] / count
	}
	band.AvgDurationMs = b.duration / count
	band.Phases = timeAnatomyPhaseMeans(phases)
	band.NonRequestMs = b.nonRequest / count
	band.OverlapMs = b.overlap / count
	return band
}

// cellMeans returns a row's mean setup/server/download/unsplit per joined
// member (a member without the row counts as 0).
func (b *timeAnatomyBandSums) cellMeans(cell int) [4]float64 {
	var means [4]float64
	if b.band.Joined == 0 {
		return means
	}
	for index, value := range b.cells[cell] {
		means[index] = value / float64(b.band.Joined)
	}
	return means
}

func (n *timeAnatomyName) tail(durations *archivePopulationAccumulator) *RunArchiveTailAttribution {
	tail := &RunArchiveTailAttribution{
		BandEstimate:     "exact",
		Instances:        n.instances - n.interrupted,
		SampledInstances: int64(len(n.sample)),
	}
	if n.sampled {
		tail.BandEstimate = "sampled"
	}
	if durations != nil {
		tail.ThresholdP50Ms = durations.digest.Quantile(.50)
		tail.ThresholdP95Ms = durations.digest.Quantile(.95)
		tail.ThresholdQuality = durations.digest.PercentileQuality()
	}
	tailSums := timeAnatomyBandSums{cells: make([][4]float64, len(n.rowList))}
	medianSums := timeAnatomyBandSums{cells: make([][4]float64, len(n.rowList))}
	for index := range n.sample {
		instance := &n.sample[index]
		switch {
		case instance.durationMs > tail.ThresholdP95Ms:
			tailSums.add(instance)
		case instance.durationMs <= tail.ThresholdP50Ms:
			medianSums.add(instance)
		}
	}
	tail.Tail, tail.Median = tailSums.finish(), medianSums.finish()
	tail.Delta = timeAnatomyTailDelta(tail.Tail, tail.Median)

	type rowDelta struct {
		delta RunArchiveRequestDelta
		index int
	}
	var deltas []rowDelta
	for index, row := range n.rowList {
		tailMeans, medianMeans := tailSums.cellMeans(index), medianSums.cellMeans(index)
		var total float64
		dominant, dominantMs := "", math.Inf(-1)
		for phase, label := range []string{"setup", "server", "download", "unsplit"} {
			delta := tailMeans[phase] - medianMeans[phase]
			total += delta
			if delta > dominantMs {
				dominant, dominantMs = label, delta
			}
		}
		if total <= 0 {
			continue
		}
		deltas = append(deltas, rowDelta{index: index, delta: RunArchiveRequestDelta{Key: row.key, ActionName: row.action, DeltaMs: total, DominantPhase: dominant, DominantMs: dominantMs}})
	}
	slices.SortFunc(deltas, func(left, right rowDelta) int {
		return cmp.Or(cmp.Compare(right.delta.DeltaMs, left.delta.DeltaMs), strings.Compare(left.delta.Key, right.delta.Key))
	})
	for index := 0; index < len(deltas) && index < 5; index++ {
		tail.Requests = append(tail.Requests, deltas[index].delta)
	}

	switch {
	case tail.Instances < timeAnatomyTailMinimum:
		tail.Status = "insufficient_samples"
	case tail.ThresholdP95Ms <= tail.ThresholdP50Ms || tail.Tail.Count == 0:
		tail.Status = "no_tail"
	case tail.Tail.Joined < 3 || tail.Median.Joined < 10:
		tail.Status = "insufficient_samples"
	case float64(tail.Tail.Joined) < 0.8*float64(tail.Tail.Count) || float64(tail.Median.Joined) < 0.8*float64(tail.Median.Count):
		tail.Status = "low_join_coverage"
	default:
		tail.Status = "ok"
		tail.Headline = timeAnatomyHeadline(tail)
	}
	return tail
}

// timeAnatomyTailDelta splits tail minus median into components that add up
// to the total, by linearity of the per-instance identity.
func timeAnatomyTailDelta(tail, median RunArchiveTailBand) RunArchiveTailDelta {
	total := tail.AvgDurationMs - median.AvgDurationMs
	values := []float64{
		tail.Phases.DNSMs - median.Phases.DNSMs,
		tail.Phases.ConnectMs - median.Phases.ConnectMs,
		tail.Phases.TLSMs - median.Phases.TLSMs,
		tail.Phases.ServerMs - median.Phases.ServerMs,
		tail.Phases.DownloadMs - median.Phases.DownloadMs,
		tail.Phases.UnsplitMs - median.Phases.UnsplitMs,
		tail.NonRequestMs - median.NonRequestMs,
		-(tail.OverlapMs - median.OverlapMs),
	}
	delta := RunArchiveTailDelta{TotalMs: total, Components: make([]RunArchiveTailComponent, 0, len(values))}
	for index, value := range values {
		if value == 0 {
			value = 0 // no negative zero in the JSON
		}
		component := RunArchiveTailComponent{Component: timeAnatomyComponents[index], DeltaMs: value}
		if total > 1 {
			share := value / total * 100
			component.SharePct = &share
		}
		delta.Components = append(delta.Components, component)
	}
	order := make(map[string]int, len(timeAnatomyComponents))
	for index, name := range timeAnatomyComponents {
		order[name] = index
	}
	slices.SortStableFunc(delta.Components, func(left, right RunArchiveTailComponent) int {
		return cmp.Or(cmp.Compare(math.Abs(right.DeltaMs), math.Abs(left.DeltaMs)), cmp.Compare(order[left.Component], order[right.Component]))
	})
	return delta
}

// timeAnatomyHeadline is the ASCII sentence the Summary and (later) the PDF
// show for an ok tail.
func timeAnatomyHeadline(tail *RunArchiveTailAttribution) string {
	total := tail.Delta.TotalMs
	threshold := max(1, 0.05*math.Abs(total))
	var parts []string
	for _, component := range tail.Delta.Components {
		if len(parts) == 3 {
			break
		}
		if math.Abs(component.DeltaMs) < threshold {
			continue
		}
		parts = append(parts, timeAnatomyComponentLabels[component.Component]+" "+timeAnatomyFormatMs(component.DeltaMs, true)+" ms")
	}
	var builder strings.Builder
	builder.WriteString("P95 tail is ")
	builder.WriteString(timeAnatomyFormatMs(total, true))
	builder.WriteString(" ms vs median")
	if len(parts) > 0 {
		builder.WriteString(": ")
		builder.WriteString(strings.Join(parts, ", "))
	} else {
		builder.WriteString(", spread across phases")
	}
	builder.WriteString(" (samples above P95 ")
	builder.WriteString(timeAnatomyFormatMs(tail.ThresholdP95Ms, false))
	builder.WriteString(" ms average ")
	builder.WriteString(timeAnatomyFormatMs(tail.Tail.AvgDurationMs, false))
	builder.WriteString(" ms; samples at or below P50 average ")
	builder.WriteString(timeAnatomyFormatMs(tail.Median.AvgDurationMs, false))
	builder.WriteString(" ms)")
	if len(tail.Requests) > 0 && total > 0 && tail.Requests[0].DeltaMs >= 0.5*total {
		builder.WriteString("; most of it in ")
		builder.WriteString(timeAnatomyASCII(tail.Requests[0].ActionName))
		builder.WriteString(" (")
		builder.WriteString(timeAnatomyFormatMs(tail.Requests[0].DeltaMs, true))
		builder.WriteString(" ms)")
	}
	return builder.String()
}

// timeAnatomyFormatMs rounds to whole milliseconds with thousands separators,
// signed when asked.
func timeAnatomyFormatMs(value float64, signed bool) string {
	rounded := int64(math.Round(value))
	negative := rounded < 0
	if negative {
		rounded = -rounded
	}
	digits := strconv.FormatInt(rounded, 10)
	var builder strings.Builder
	switch {
	case negative:
		builder.WriteByte('-')
	case signed:
		builder.WriteByte('+')
	}
	for index, digit := range digits {
		if index > 0 && (len(digits)-index)%3 == 0 {
			builder.WriteByte(',')
		}
		builder.WriteRune(digit)
	}
	return builder.String()
}

// timeAnatomyASCII replaces non-ASCII and control characters so the headline
// stays ASCII-only.
func timeAnatomyASCII(value string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e {
			return '?'
		}
		return r
	}, value)
}

// representatives picks p50, p95, slowest and failed, de-duplicated by
// event_id (the first reason wins).
func (n *timeAnatomyName) representatives(tail *RunArchiveTailAttribution) []RunArchiveRepresentativeSample {
	var result []RunArchiveRepresentativeSample
	seen := map[string]bool{}
	appendPick := func(instance *timeAnatomyInstance, reason string, target *float64, sampled bool) {
		if instance == nil || seen[instance.eventID] {
			return
		}
		seen[instance.eventID] = true
		sample := instance.representative(reason)
		sample.TargetMs = target
		sample.Sampled = sampled
		result = append(result, sample)
	}
	sampled := tail != nil && tail.BandEstimate == "sampled"
	if tail != nil {
		for _, pick := range []struct {
			reason string
			target float64
		}{{"p50", tail.ThresholdP50Ms}, {"p95", tail.ThresholdP95Ms}} {
			target := pick.target
			appendPick(n.nearest(target), pick.reason, &target, sampled)
		}
	}
	appendPick(n.slowest, "slowest", nil, false)
	appendPick(n.failure, "failed", nil, false)
	return result
}

// nearest returns the sampled instance closest to target: successful ones
// first, then any; ties go to the lower ordinal. The sample is sorted by
// ordinal.
func (n *timeAnatomyName) nearest(target float64) *timeAnatomyInstance {
	for _, successOnly := range []bool{true, false} {
		var best *timeAnatomyInstance
		bestDistance := math.Inf(1)
		for index := range n.sample {
			instance := &n.sample[index]
			if successOnly && !instance.success {
				continue
			}
			if distance := math.Abs(instance.durationMs - target); distance < bestDistance {
				best, bestDistance = instance, distance
			}
		}
		if best != nil {
			return best
		}
	}
	return nil
}

// timeAnatomySLAStatus derives each transaction's SLA status from the
// manifest's evaluated rules: "failed" if any rule for the name failed,
// "passed" if one was evaluated, otherwise absent.
func timeAnatomySLAStatus(manifest *RunArchiveManifest) map[string]string {
	status := map[string]string{}
	if manifest == nil {
		return status
	}
	record := func(name, result string) {
		if name == "" || result == "" || result == "not_evaluated" {
			return
		}
		if result == "fail" {
			status[name] = "failed"
			return
		}
		if status[name] == "" {
			status[name] = "passed"
		}
	}
	for _, result := range manifest.TransactionRuleResults {
		if population := result.Rule.Population; population != "" && population != "transaction" {
			continue
		}
		record(result.Rule.Transaction, result.Status)
	}
	for _, result := range manifest.TransactionResponseTimeSLAResults {
		record(result.Transaction, result.Status)
	}
	return status
}

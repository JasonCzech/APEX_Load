// Package messaging provides a protocol-neutral acknowledgement / transaction
// session shared by the messaging protocols (JMS family, Kafka, Event Hubs).
//
// It exists so that jms_ack / jms_commit / jms_rollback and kafka_commit are real
// per-VUser state transitions instead of no-ops: the session owns the ack mode
// and the pending-item retention/settlement that is identical across providers,
// plus a commit/abort tally surfaced for tests and debugging introspection. The
// actual broker-side settlement (Service Bus complete/abandon, Solace client-ack,
// Kafka offset commit) is wired per provider.
//
// NOTE: the per-action reliability telemetry (CommitCount / AbortCount /
// RedeliveryCount on ServerTelemetry) is emitted directly by each protocol's
// action handler, NOT derived from this tally — the session counters are a running
// per-VUser total, whereas the telemetry is per-action. Do not "unify" the two
// without also reworking the aggregation, or run totals will change.
package messaging

import "strings"

// AckMode is the JMS-style acknowledgement / transaction mode.
type AckMode string

const (
	AckAuto       AckMode = "auto"
	AckClient     AckMode = "client"
	AckDupsOK     AckMode = "dups_ok"
	AckTransacted AckMode = "transacted"
)

// ParseAckMode maps a config string to an AckMode, defaulting to auto.
func ParseAckMode(s string) AckMode {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "client":
		return AckClient
	case "dups_ok", "dups-ok", "dupsok":
		return AckDupsOK
	case "transacted":
		return AckTransacted
	default:
		return AckAuto
	}
}

// maxPendingItems bounds the number of retained-but-unsettled items a single
// VUser session holds in client/transacted mode. A messaging session is created
// once per VUser and is never reset between iterations, so a script that consumes
// in client/transacted mode without ever ack/commit/rollback would otherwise
// retain one full message per consume for the whole test (unbounded heap growth
// at scale). When the cap is reached the oldest item is evicted and counted.
const maxPendingItems = 10000

// maxObserveWindow bounds the dedup window used by Observe — the count of recent
// message sequences/offsets retained per VUser to detect duplicates. Older entries
// are evicted (oldest-first) so a long-running consumer cannot grow unbounded.
const maxObserveWindow = 4096

// Session tracks per-VUser acknowledgement / transaction state: the pending items
// awaiting settlement, a commit/abort tally for introspection, and a bounded
// sequence observer for duplicate / out-of-order reliability detection. It is NOT
// safe for concurrent use; each VUser owns exactly one. T is the provider's
// consumed item type (e.g. a JMS message) so Rollback can return items to redeliver.
type Session[T any] struct {
	mode           AckMode
	pending        []T
	commits        int
	aborts         int
	droppedPending int

	// Reliability observation state (see Observe / ObserveKeyed). Tracking is
	// per stream key so that providers whose sequences/offsets are per-partition
	// (Kafka) or per-hub (Event Hubs) — or per-subscription (JMS topic fan-out) —
	// are each compared within their own monotonic space. A single global space
	// would flag legitimate cross-partition interleaving as duplicate/out-of-order.
	streams    map[string]*observeStream
	duplicates int
	outOfOrder int
}

// observeStream is the bounded dedup/ordering window for one logical sequence
// space (one partition, hub, or subscription).
type observeStream struct {
	seen    map[int64]struct{} // sequences within the bounded dedup window
	order   []int64            // insertion order, for oldest-first eviction
	maxSeq  int64              // highest sequence observed so far in this stream
	haveSeq bool               // whether any sequence has been observed
}

// New creates a Session for the given acknowledgement mode.
func New[T any](mode AckMode) *Session[T] { return &Session[T]{mode: mode} }

// Mode returns the acknowledgement mode.
func (s *Session[T]) Mode() AckMode { return s.mode }

// NeedsSettlement reports whether consumed items must be retained until an
// explicit ack/commit (client or transacted modes). auto/dups_ok settle on
// receipt and therefore never retain pending items.
func (s *Session[T]) NeedsSettlement() bool {
	return s.mode == AckClient || s.mode == AckTransacted
}

// Track retains a consumed item pending settlement (no-op for auto/dups_ok).
// Growth is bounded to maxPendingItems: when a script consumes without settling,
// the oldest item is evicted (and counted in DroppedPending) so a long-running
// VUser cannot accumulate unbounded retained messages.
func (s *Session[T]) Track(item T) {
	if !s.NeedsSettlement() {
		return
	}
	s.pending = append(s.pending, item)
	if len(s.pending) > maxPendingItems {
		drop := len(s.pending) - maxPendingItems
		// Compact in place so the backing array stays bounded at the cap rather
		// than retaining the evicted prefix.
		s.pending = append(s.pending[:0], s.pending[drop:]...)
		s.droppedPending += drop
	}
}

// DroppedPending returns how many unsettled items were evicted to keep the
// pending set bounded (non-zero only when a script consumes without settling
// past the cap).
func (s *Session[T]) DroppedPending() int { return s.droppedPending }

// Ack settles all pending items (client acknowledgement) and returns them.
func (s *Session[T]) Ack() []T {
	settled := s.pending
	s.pending = nil
	return settled
}

// Commit records a transaction/ack-batch commit, clears pending, and returns
// the settled items.
func (s *Session[T]) Commit() []T {
	s.commits++
	settled := s.pending
	s.pending = nil
	return settled
}

// Rollback records a transaction abort and returns the pending items for
// redelivery, clearing them.
func (s *Session[T]) Rollback() []T {
	s.aborts++
	toRedeliver := s.pending
	s.pending = nil
	return toRedeliver
}

// Observe records a consumed item's monotonic sequence/offset in the default
// (single) sequence space. Use it only when the consumer reads a single ordered
// stream; providers with per-partition/per-hub sequences must use ObserveKeyed so
// independent streams are not conflated. See ObserveKeyed for the semantics.
func (s *Session[T]) Observe(seq int64) (duplicate, outOfOrder bool) {
	return s.ObserveKeyed("", seq)
}

// ObserveKeyed records a consumed item's monotonic sequence/offset within the
// sequence space identified by streamKey, and reports whether it is a duplicate
// (the sequence was already seen within the bounded window for that key) or
// out-of-order (lower than the highest already observed for that key). Sequences/
// offsets in messaging providers are monotonic only within a partition (Kafka),
// hub (Event Hubs), or subscription (JMS topic fan-out), so callers must pass a
// key that identifies that space — e.g. topic+partition — otherwise legitimate
// cross-stream interleaving reads as duplicate/out-of-order.
//
// A negative sequence means the provider exposes no usable sequence, so it is a
// no-op. The caller must NOT Observe a legitimate redelivery (a rolled-back
// message re-consumed with the same sequence) — that is tracked separately as a
// redelivery, not a broker duplicate. Each stream's dedup window is bounded to
// maxObserveWindow recent sequences so it never grows without limit.
func (s *Session[T]) ObserveKeyed(streamKey string, seq int64) (duplicate, outOfOrder bool) {
	if seq < 0 {
		return false, false
	}
	if s.streams == nil {
		s.streams = make(map[string]*observeStream)
	}
	st := s.streams[streamKey]
	if st == nil {
		st = &observeStream{seen: make(map[int64]struct{})}
		s.streams[streamKey] = st
	}
	if _, seen := st.seen[seq]; seen {
		s.duplicates++
		return true, false
	}
	if st.haveSeq && seq < st.maxSeq {
		s.outOfOrder++
		outOfOrder = true
	}
	if !st.haveSeq || seq > st.maxSeq {
		st.maxSeq = seq
	}
	st.haveSeq = true
	st.seen[seq] = struct{}{}
	st.order = append(st.order, seq)
	if len(st.order) > maxObserveWindow {
		evict := len(st.order) - maxObserveWindow
		for _, old := range st.order[:evict] {
			delete(st.seen, old)
		}
		// Compact in place so the backing array stays bounded at the cap.
		st.order = append(st.order[:0], st.order[evict:]...)
	}
	return duplicate, outOfOrder
}

// Duplicates returns the per-VUser running count of observed duplicate messages.
func (s *Session[T]) Duplicates() int { return s.duplicates }

// OutOfOrders returns the per-VUser running count of observed out-of-order messages.
func (s *Session[T]) OutOfOrders() int { return s.outOfOrder }

// Commits returns the per-VUser running count of commits performed (introspection
// only; the per-action CommitCount telemetry is emitted directly by the protocol).
func (s *Session[T]) Commits() int { return s.commits }

// Aborts returns the per-VUser running count of rollbacks performed (introspection
// only; the per-action AbortCount telemetry is emitted directly by the protocol).
func (s *Session[T]) Aborts() int { return s.aborts }

// Pending returns the count of currently-unsettled items.
func (s *Session[T]) Pending() int { return len(s.pending) }

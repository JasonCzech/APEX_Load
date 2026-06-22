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

	// Reliability observation state (see Observe).
	seqSeen    map[int64]struct{} // sequences within the bounded dedup window
	seqOrder   []int64            // insertion order, for oldest-first eviction
	maxSeq     int64              // highest sequence observed so far
	haveSeq    bool               // whether any sequence has been observed
	duplicates int
	outOfOrder int
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

// Observe records a consumed item's monotonic sequence/offset and reports whether
// it is a duplicate (the sequence was already seen within the bounded window) or
// out-of-order (the sequence is lower than the highest already observed). A
// negative sequence means the provider exposes no usable sequence, so Observe is a
// no-op. The caller must NOT Observe a legitimate redelivery (a rolled-back message
// re-consumed with the same sequence) — that is tracked separately as a redelivery,
// not a broker duplicate. The dedup window is bounded to maxObserveWindow recent
// sequences so it never grows without limit.
func (s *Session[T]) Observe(seq int64) (duplicate, outOfOrder bool) {
	if seq < 0 {
		return false, false
	}
	if s.seqSeen == nil {
		s.seqSeen = make(map[int64]struct{}, maxObserveWindow)
	}
	if _, seen := s.seqSeen[seq]; seen {
		s.duplicates++
		return true, false
	}
	if s.haveSeq && seq < s.maxSeq {
		s.outOfOrder++
		outOfOrder = true
	}
	if !s.haveSeq || seq > s.maxSeq {
		s.maxSeq = seq
	}
	s.haveSeq = true
	s.seqSeen[seq] = struct{}{}
	s.seqOrder = append(s.seqOrder, seq)
	if len(s.seqOrder) > maxObserveWindow {
		evict := len(s.seqOrder) - maxObserveWindow
		for _, old := range s.seqOrder[:evict] {
			delete(s.seqSeen, old)
		}
		// Compact in place so the backing array stays bounded at the cap.
		s.seqOrder = append(s.seqOrder[:0], s.seqOrder[evict:]...)
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

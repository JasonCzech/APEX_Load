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

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

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
// at scale). Capacity is rejected explicitly; unsettled items are never evicted.
const maxPendingItems = 10000

// maxPendingBytes is the per-VUser aggregate payload budget for unsettled
// messages. Providers should use TrackSized with their exact retained size.
const maxPendingBytes int64 = 8 << 20

// maxObserveWindow bounds the dedup window used by Observe — the count of recent
// message sequences/offsets retained per VUser to detect duplicates. Older entries
// are evicted (oldest-first) so a long-running consumer cannot grow unbounded.
const maxObserveWindow = 4096

// The observer is also bounded across streams. A per-stream window alone is not
// sufficient because topic/partition names are script- and broker-controlled.
const (
	maxObserveStreams = 128
	maxObserveEntries = 4096
	// maxObserveStreamKeyBytes prevents provider-controlled destination names
	// from turning the bounded stream-count map into an unbounded byte sink.
	// Oversized keys are represented by a collision-resistant fixed-size digest.
	maxObserveStreamKeyBytes = 4 << 10
)

// ErrPendingCapacity reports that retaining another unsettled item would exceed
// the session's hard count or byte budget. Existing pending items remain intact.
var ErrPendingCapacity = errors.New("messaging session pending capacity exceeded")

// Session tracks per-VUser acknowledgement / transaction state: the pending items
// awaiting settlement, a commit/abort tally for introspection, and a bounded
// sequence observer for duplicate / out-of-order reliability detection. It is NOT
// safe for concurrent use; each VUser owns exactly one. T is the provider's
// consumed item type (e.g. a JMS message) so Rollback can return items to redeliver.
type Session[T any] struct {
	mode           AckMode
	pending        []T
	pendingBytes   int64
	commits        int
	aborts         int
	droppedPending int

	// Reliability observation state (see Observe / ObserveKeyed). Tracking is
	// per stream key so that providers whose sequences/offsets are per-partition
	// (Kafka) or per-hub (Event Hubs) — or per-subscription (JMS topic fan-out) —
	// are each compared within their own monotonic space. A single global space
	// would flag legitimate cross-partition interleaving as duplicate/out-of-order.
	streams    map[string]*observeStream
	streamUse  uint64
	observed   int
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
	lastUse uint64             // LRU stamp for aggregate stream admission
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
// Providers retaining message bodies should call TrackSized with the exact size.
func (s *Session[T]) Track(item T) error {
	return s.TrackSized(item, 1)
}

// TrackSized retains a consumed item and accounts retainedBytes against the
// aggregate byte budget. It fails before mutation when either hard limit is
// reached, so the caller can explicitly release/requeue the just-consumed item.
func (s *Session[T]) TrackSized(item T, retainedBytes int64) error {
	if !s.NeedsSettlement() {
		return nil
	}
	if retainedBytes < 0 {
		retainedBytes = 0
	}
	if len(s.pending) >= maxPendingItems || retainedBytes > maxPendingBytes-s.pendingBytes {
		s.droppedPending++
		return ErrPendingCapacity
	}
	s.pending = append(s.pending, item)
	s.pendingBytes += retainedBytes
	return nil
}

// DroppedPending returns how many attempted additions were rejected by the hard
// pending limits. The historical name is retained for API compatibility; no
// unsettled item is silently dropped or evicted.
func (s *Session[T]) DroppedPending() int { return s.droppedPending }

// Ack settles all pending items (client acknowledgement) and returns them.
func (s *Session[T]) Ack() []T {
	settled := s.pending
	s.pending = nil
	s.pendingBytes = 0
	return settled
}

// Commit records a transaction/ack-batch commit, clears pending, and returns
// the settled items.
func (s *Session[T]) Commit() []T {
	s.commits++
	settled := s.pending
	s.pending = nil
	s.pendingBytes = 0
	return settled
}

// Rollback records a transaction abort and returns the pending items for
// redelivery, clearing them.
func (s *Session[T]) Rollback() []T {
	s.aborts++
	toRedeliver := s.pending
	s.pending = nil
	s.pendingBytes = 0
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
	streamKey = boundedObserveStreamKey(streamKey)
	if s.streams == nil {
		s.streams = make(map[string]*observeStream)
	}
	st := s.streams[streamKey]
	if st == nil {
		if len(s.streams) >= maxObserveStreams {
			s.evictLeastRecentlyUsedStream()
		}
		st = &observeStream{seen: make(map[int64]struct{})}
		s.streams[streamKey] = st
	}
	s.streamUse++
	st.lastUse = s.streamUse
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
	s.observed++
	if len(st.order) > maxObserveWindow {
		evict := len(st.order) - maxObserveWindow
		for _, old := range st.order[:evict] {
			delete(st.seen, old)
		}
		s.observed -= evict
		// Compact in place so the backing array stays bounded at the cap.
		st.order = append(st.order[:0], st.order[evict:]...)
	}
	for s.observed > maxObserveEntries {
		s.evictOldestObservedEntry()
	}
	return duplicate, outOfOrder
}

func boundedObserveStreamKey(streamKey string) string {
	if len(streamKey) <= maxObserveStreamKeyBytes {
		return streamKey
	}
	digest := sha256.Sum256([]byte(streamKey))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func (s *Session[T]) evictLeastRecentlyUsedStream() {
	var oldestKey string
	var oldest *observeStream
	for key, candidate := range s.streams {
		if oldest == nil || candidate.lastUse < oldest.lastUse {
			oldestKey, oldest = key, candidate
		}
	}
	if oldest == nil {
		return
	}
	s.observed -= len(oldest.order)
	delete(s.streams, oldestKey)
}

func (s *Session[T]) evictOldestObservedEntry() {
	var oldest *observeStream
	for _, candidate := range s.streams {
		if len(candidate.order) == 0 {
			continue
		}
		if oldest == nil || candidate.lastUse < oldest.lastUse {
			oldest = candidate
		}
	}
	if oldest == nil {
		s.observed = 0
		return
	}
	sequence := oldest.order[0]
	delete(oldest.seen, sequence)
	copy(oldest.order, oldest.order[1:])
	oldest.order = oldest.order[:len(oldest.order)-1]
	s.observed--
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

// PendingBytes returns the accounted bytes retained for explicit settlement.
func (s *Session[T]) PendingBytes() int64 { return s.pendingBytes }

// ObservedStreams returns the number of currently retained observation spaces.
func (s *Session[T]) ObservedStreams() int { return len(s.streams) }

// ObservedEntries returns the aggregate number of retained dedup entries.
func (s *Session[T]) ObservedEntries() int { return s.observed }

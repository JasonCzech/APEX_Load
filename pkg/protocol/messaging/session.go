// Package messaging provides a protocol-neutral acknowledgement / transaction
// session shared by the messaging protocols (JMS family, Kafka, Event Hubs).
//
// It exists so that jms_ack / jms_commit / jms_rollback and kafka_commit are
// real per-VUser state transitions (with commit/abort/redelivery accounting that
// feeds the unified result model) instead of no-ops. The actual broker-side
// settlement (Service Bus complete/abandon, Solace client-ack, Kafka offset
// commit) is wired per provider; this type owns the mode + pending/commit/abort
// bookkeeping that is identical across providers.
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

// Session tracks per-VUser acknowledgement / transaction state. It is NOT safe
// for concurrent use; each VUser owns exactly one. T is the provider's consumed
// item type (e.g. a JMS message) so Rollback can return the items to redeliver.
type Session[T any] struct {
	mode         AckMode
	pending      []T
	commits      int
	aborts       int
	redeliveries int
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
func (s *Session[T]) Track(item T) {
	if s.NeedsSettlement() {
		s.pending = append(s.pending, item)
	}
}

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

// RecordRedeliveries adds to the redelivery counter; called when a previously
// rolled-back item is re-consumed.
func (s *Session[T]) RecordRedeliveries(n int) {
	if n > 0 {
		s.redeliveries += n
	}
}

// Commits returns the number of commits performed.
func (s *Session[T]) Commits() int { return s.commits }

// Aborts returns the number of rollbacks performed.
func (s *Session[T]) Aborts() int { return s.aborts }

// Redeliveries returns the number of redelivered items observed.
func (s *Session[T]) Redeliveries() int { return s.redeliveries }

// Pending returns the count of currently-unsettled items.
func (s *Session[T]) Pending() int { return len(s.pending) }

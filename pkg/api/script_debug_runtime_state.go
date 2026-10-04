package api

import (
	"context"
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
)

const (
	ScriptDebugRuntimeBoundaryHeld        = "boundary_held"
	ScriptDebugRuntimeNotPaused           = "not_paused"
	ScriptDebugRuntimePausePending        = "pause_pending"
	ScriptDebugRuntimeSnapshotUnavailable = "snapshot_unavailable"
	ScriptDebugRuntimeContextCanceled     = "context_canceled"
	ScriptDebugRuntimeIntegrityFenceLost  = "integrity_fence_lost"
	ScriptDebugRuntimeEvidenceUnavailable = "runtime_evidence_unavailable"
	ScriptDebugRuntimeSessionTerminal     = "session_terminal"
	ScriptDebugRuntimeSessionRetired      = "session_retired"
	ScriptDebugRuntimeRevisionExhausted   = "revision_exhausted"
)

// ScriptDebugRuntimeState is a full replacement, including explicit null when
// the engine is not held at the boundary that owns this snapshot. Decimal
// revisions preserve all uint64 values in JavaScript clients.
type ScriptDebugRuntimeState struct {
	Version            int                           `json:"version"`
	Kind               string                        `json:"kind"`
	SessionID          string                        `json:"session_id"`
	SessionIncarnation string                        `json:"session_incarnation"`
	StateRevision      string                        `json:"state_revision"`
	State              string                        `json:"state"`
	Reason             string                        `json:"reason"`
	Snapshot           *engine.ReplayRuntimeSnapshot `json:"snapshot"`
}

// A token denotes one hook invocation that actually waits. Resume invalidates
// it before waking the engine; a delayed defer may only clear its own token.
type scriptDebugHeldBoundary struct {
	ctx      context.Context
	snapshot *engine.ReplayRuntimeSnapshot
	// candidate belongs only to this live hook invocation. A rapid resume/pause
	// can wait again before leaving; that re-admits the same bounded candidate.
	candidate   *engine.ReplayRuntimeSnapshot
	invalidated bool
}

func (s *ScriptDebugSession) clearActiveRuntimeCandidateLocked() {
	for _, token := range []*scriptDebugHeldBoundary{s.activeRuntimeHook, s.heldBoundary} {
		if token != nil {
			token.snapshot, token.candidate = nil, nil
			token.invalidated = true
		}
	}
}

func (s *ScriptDebugSession) advanceRuntimeRevisionLocked() {
	if s.runtimeRevisionExhausted {
		return
	}
	if s.runtimeRevision == math.MaxUint64 {
		s.runtimeRevisionExhausted = true
		s.clearActiveRuntimeCandidateLocked()
		s.heldBoundary = nil
		s.runtimeUnavailableReason = ScriptDebugRuntimeRevisionExhausted
		return
	}
	s.runtimeRevision++
}

func (s *ScriptDebugSession) invalidateRuntimeStateLocked(reason string) {
	if reason != ScriptDebugRuntimeNotPaused && reason != ScriptDebugRuntimePausePending {
		s.clearActiveRuntimeCandidateLocked()
	}
	if s.heldBoundary != nil {
		s.heldBoundary.snapshot = nil
	}
	s.heldBoundary = nil
	s.runtimeUnavailableReason = reason
	s.advanceRuntimeRevisionLocked()
}

// Readers reconcile authority synchronously; cancellation watchers are only
// a liveness aid and cannot authorize stale values between context loss and
// their next scheduled turn. No runtime values are reconstructed here.
func (s *ScriptDebugSession) reconcileRuntimeStateLocked() {
	reason := ""
	switch {
	case s.runtimeRevisionExhausted:
		reason = ScriptDebugRuntimeRevisionExhausted
	case s.RuntimeEvidenceUnavailable:
		reason = ScriptDebugRuntimeEvidenceUnavailable
	case s.retired || s.workerAbandoned:
		reason = ScriptDebugRuntimeSessionRetired
	case s.reservation.failureOrExpired(time.Now()) != nil:
		reason = ScriptDebugRuntimeIntegrityFenceLost
	case s.workerFinished || replaySessionStatusTerminal(s.Status):
		reason = ScriptDebugRuntimeSessionTerminal
	case s.executionContext != nil && s.executionContext.Err() != nil:
		reason = ScriptDebugRuntimeIntegrityFenceLost
	case s.workerContext != nil && s.workerContext.Err() != nil:
		reason = ScriptDebugRuntimeContextCanceled
	case s.activeRuntimeHook != nil && s.activeRuntimeHook.ctx.Err() != nil:
		reason = ScriptDebugRuntimeContextCanceled
	case s.heldBoundary != nil && s.heldBoundary.ctx.Err() != nil:
		reason = ScriptDebugRuntimeContextCanceled
	case s.Status != ScriptDebugPaused:
		reason = ScriptDebugRuntimeNotPaused
	}
	if reason != "" && reason != ScriptDebugRuntimeNotPaused && reason != ScriptDebugRuntimePausePending {
		s.clearActiveRuntimeCandidateLocked()
	}
	if reason != "" && (s.heldBoundary != nil || s.runtimeUnavailableReason != reason) {
		if s.heldBoundary == nil && s.runtimeUnavailableReason == "" {
			s.runtimeUnavailableReason = reason
		} else {
			s.invalidateRuntimeStateLocked(reason)
		}
	}
}

func (s *ScriptDebugSession) runtimeStateLocked() ScriptDebugRuntimeState {
	s.reconcileRuntimeStateLocked()
	reason := s.runtimeUnavailableReason
	if reason == "" {
		reason = ScriptDebugRuntimeNotPaused
		if s.Status == ScriptDebugPaused {
			reason = ScriptDebugRuntimePausePending
		}
	}
	state := ScriptDebugRuntimeState{Version: 1, Kind: "full", SessionID: s.ID,
		SessionIncarnation: s.SessionIncarnation, StateRevision: strconv.FormatUint(s.runtimeRevision, 10),
		State: "unavailable", Reason: reason}
	if s.heldBoundary != nil && s.Status == ScriptDebugPaused && !s.runtimeRevisionExhausted && !s.RuntimeEvidenceUnavailable {
		if s.heldBoundary.snapshot == nil {
			state.Reason = ScriptDebugRuntimeSnapshotUnavailable
		} else {
			token := s.heldBoundary
			cloned := engine.CloneReplayRuntimeSnapshot(*s.heldBoundary.snapshot)
			state.State, state.Reason, state.Snapshot = "paused", ScriptDebugRuntimeBoundaryHeld, &cloned
			s.reconcileRuntimeStateLocked()
			if s.heldBoundary != token {
				state.State, state.Reason, state.Snapshot = "unavailable", s.runtimeUnavailableReason, nil
				state.StateRevision = strconv.FormatUint(s.runtimeRevision, 10)
			}
		}
	}
	return state
}

func (c scriptDebugController) runtimeCandidate(snapshot *engine.ReplayRuntimeSnapshot, action engine.ReplayDebugAction, boundary string) *engine.ReplayRuntimeSnapshot {
	if snapshot == nil {
		return nil
	}
	// A classification failure is sticky even if the remaining DTO is malformed.
	if snapshot.RuntimeEvidenceUnavailable {
		newlyUnavailable := false
		c.manager.withCurrentSession(c.session, func() {
			c.session.mu.Lock()
			newlyUnavailable = !c.session.RuntimeEvidenceUnavailable
			c.session.omitRuntimeEvidenceLocked()
			c.session.mu.Unlock()
		})
		if newlyUnavailable {
			c.manager.broadcastTerminal("script_debug_evidence_unavailable", c.session, action.EventIndex, nil, nil, nil)
		}
		return nil
	}
	blocked := true
	c.manager.withCurrentSession(c.session, func() {
		c.session.mu.Lock()
		c.session.reconcileRuntimeStateLocked()
		blocked = c.session.RuntimeEvidenceUnavailable || c.session.runtimeRevisionExhausted || c.session.retired ||
			c.session.workerAbandoned || c.session.workerFinished || replaySessionStatusTerminal(c.session.Status) ||
			(c.session.executionContext != nil && c.session.executionContext.Err() != nil) ||
			(c.session.workerContext != nil && c.session.workerContext.Err() != nil) || c.session.reservation.failureOrExpired(time.Now()) != nil
		c.session.mu.Unlock()
	})
	if blocked {
		return nil
	}
	if (snapshot.State != "complete" && snapshot.State != "bounded") || snapshot.Boundary != boundary ||
		!engine.ValidateReplayRuntimeSnapshotBounds(*snapshot) {
		return nil
	}
	loc := snapshot.Location
	if loc.Phase != action.Phase || loc.ActionIndex != action.ActionIndex || loc.EventIndex != action.EventIndex ||
		loc.EventID != action.EventID || loc.RequestCount != action.RequestCount || loc.LeafOrdinal != action.LeafOrdinal ||
		!runtimeOptionalIndexEqual(loc.RequestIndex, action.RequestIndex) || !runtimeOptionalIndexEqual(loc.Iteration, action.Iteration) ||
		!slices.Equal(loc.StepPath, action.StepPath) || !slices.Equal(loc.IterationPath, action.IterationPath) {
		return nil
	}
	cloned := engine.CloneReplayRuntimeSnapshot(*snapshot)
	return &cloned
}

func runtimeOptionalIndexEqual(a, b *int) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func (c scriptDebugController) holdRuntimeBoundaryLocked(token *scriptDebugHeldBoundary) bool {
	if c.session.activeRuntimeHook == nil {
		c.session.activeRuntimeHook = token
	}
	c.session.reconcileRuntimeStateLocked()
	if token.candidate == nil {
		token.candidate = token.snapshot
	}
	if token.invalidated || c.session.activeRuntimeHook != token || c.session.workerFinished || c.session.Status != ScriptDebugPaused || c.session.RuntimeEvidenceUnavailable || c.session.runtimeRevisionExhausted ||
		token.ctx.Err() != nil || (c.session.executionContext != nil && c.session.executionContext.Err() != nil) ||
		(c.session.workerContext != nil && c.session.workerContext.Err() != nil) || c.session.reservation.failureOrExpired(time.Now()) != nil {
		token.snapshot = nil
		token.candidate = nil
		token.invalidated = true
		return false
	}
	if c.session.heldBoundary == token {
		return false
	}
	if c.session.heldBoundary != nil {
		return false
	}
	token.snapshot = token.candidate
	c.session.heldBoundary = token
	c.session.runtimeUnavailableReason = ""
	c.session.advanceRuntimeRevisionLocked()
	return !c.session.runtimeRevisionExhausted
}

func (c scriptDebugController) enterRuntimeHook(token *scriptDebugHeldBoundary) {
	c.session.mu.Lock()
	if old := c.session.activeRuntimeHook; old != nil && old != token {
		old.snapshot, old.candidate = nil, nil
		old.invalidated = true
		if c.session.heldBoundary == old {
			c.session.invalidateRuntimeStateLocked(ScriptDebugRuntimePausePending)
		}
	}
	c.session.activeRuntimeHook = token
	token.candidate = token.snapshot
	c.session.reconcileRuntimeStateLocked()
	c.session.mu.Unlock()
}

func (c scriptDebugController) leaveRuntimeBoundary(token *scriptDebugHeldBoundary) {
	c.session.mu.Lock()
	if c.session.heldBoundary == token {
		reason := ScriptDebugRuntimePausePending
		if token.ctx.Err() != nil {
			reason = ScriptDebugRuntimeContextCanceled
		}
		c.session.invalidateRuntimeStateLocked(reason)
		c.session.reconcileRuntimeStateLocked()
	}
	token.snapshot, token.candidate = nil, nil
	token.invalidated = true
	if c.session.activeRuntimeHook == token {
		c.session.activeRuntimeHook = nil
	}
	c.session.mu.Unlock()
}

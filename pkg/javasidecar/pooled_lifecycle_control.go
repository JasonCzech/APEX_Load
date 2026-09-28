package javasidecar

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"

	"github.com/jasonczech/apexload/pkg/javarunner"
)

var errPooledCommitDisabled = errors.New("pooled_commit_disabled")

// A phase runs at most once. Closing done publishes its result to exact retries.
// Its first caller owns cancellation; retry waits never cancel the operation.
type pooledControlPhase struct {
	done     chan struct{}
	response PooledControlResponse
	err      error
}

func (run *PooledRun) lifecycleControl(ctx context.Context, q PooledControlRequest, response PooledControlResponse) (PooledControlResponse, error) {
	if !run.controlDecision.CommitEnabled {
		return response, errPooledCommitDisabled
	}
	finish, err := run.begin(ctx)
	if err != nil {
		return response, err
	}
	defer finish()
	encoded, _ := json.Marshal(q)
	digest := sha256.Sum256(encoded)
	run.controlMu.Lock()
	if previous, ok := run.controlReplies[q.RequestID]; ok {
		run.controlMu.Unlock()
		if previous.digest != digest || previous.phase == nil {
			return response, errPooledRequestConflict
		}
		return run.waitControlPhase(ctx, q, previous.phase)
	}
	p := run.preparations[q.ReservationID]
	if p == nil {
		run.controlMu.Unlock()
		return response, javarunner.ErrHostIdentity
	}
	select {
	case <-p.done:
	default:
		run.controlMu.Unlock()
		return response, javarunner.ErrHostState
	}
	if p.err != nil {
		run.controlMu.Unlock()
		return response, javarunner.ErrHostState
	}
	phaseCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if q.Operation == "commit" {
		if p.commit != nil {
			run.controlMu.Unlock()
			return response, errPooledRequestConflict
		}
		if len(run.controlReplies) >= maxPooledControlReplies {
			run.controlMu.Unlock()
			return response, errPooledControlLimit
		}
		if !run.reservationInState(q.ReservationID, p, javarunner.HostReady) {
			run.controlMu.Unlock()
			return response, javarunner.ErrHostState
		}
		deadline, err := run.ledger.PreparationDeadline(run.owner.epoch, q.ReservationID)
		if err != nil {
			run.controlMu.Unlock()
			return response, err
		}
		var deadlineCancel context.CancelFunc
		phaseCtx, deadlineCancel = context.WithDeadline(phaseCtx, deadline)
		defer deadlineCancel()
	} else {
		if p.closures[q.SlotID] != nil {
			run.controlMu.Unlock()
			return response, errPooledRequestConflict
		}
		if p.commit == nil {
			run.controlMu.Unlock()
			return response, javarunner.ErrHostState
		}
		select {
		case <-p.commit.done:
		default:
			run.controlMu.Unlock()
			return response, javarunner.ErrHostState
		}
		if p.commit.err != nil || !run.reservationHasSlot(q.ReservationID, q.HostID, q.SlotID) {
			run.controlMu.Unlock()
			return response, javarunner.ErrHostIdentity
		}
		if err := run.ledger.CanInvoke(run.owner.epoch, q.HostID, q.SlotID); err != nil {
			run.controlMu.Unlock()
			return response, err
		}
		// Slot closure must remain possible when admission bookkeeping is full.
		// Only one extra record per prepared slot is allowed, and foreign slots
		// never allocate a record. Run lifetime admissions bound this extension.
	}
	response.ReservationID = q.ReservationID
	phase := &pooledControlPhase{done: make(chan struct{}), response: response}
	if q.Operation == "commit" {
		p.commit = phase
	} else {
		if p.closures == nil {
			p.closures = map[string]*pooledControlPhase{}
		}
		p.closures[q.SlotID] = phase
		phase.response.HostID, phase.response.SlotID = q.HostID, q.SlotID
	}
	run.controlReplies[q.RequestID] = pooledControlReply{digest: digest, phase: phase}
	run.controlMu.Unlock()

	run.executeControlPhase(phaseCtx, q, phase)
	return run.waitControlPhase(ctx, q, phase)
}

// Never hold the replay lock during host I/O. Abort, lease renewal, and
// independent slot closures must not wait behind a blocked commit/close.
func (run *PooledRun) executeControlPhase(ctx context.Context, q PooledControlRequest, phase *pooledControlPhase) {
	if q.Operation == "commit" {
		phase.err = run.Commit(ctx, q.ReservationID)
		phase.response.Committed = phase.err == nil
		if phase.err != nil {
			_ = run.ledger.Abort(run.owner.epoch, q.ReservationID)
		}
	} else {
		phase.err = run.CloseSlot(ctx, q.HostID, q.SlotID)
		phase.response.SlotClosed = phase.err == nil
		if phase.err != nil {
			// Even cancellation before the supervisor call must make this
			// permanently failed closure safe; never leave a retryable live slot.
			_ = run.ledger.FailHost(q.HostID, "pooled_cleanup_uncertain")
		}
	}
	close(phase.done)
}

func (run *PooledRun) waitControlPhase(ctx context.Context, q PooledControlRequest, phase *pooledControlPhase) (PooledControlResponse, error) {
	select {
	case <-ctx.Done():
		return PooledControlResponse{}, ctx.Err()
	case <-phase.done:
	}
	if phase.err != nil {
		return phase.response, phase.err
	}
	if q.Operation == "commit" {
		run.controlMu.Lock()
		p := run.preparations[q.ReservationID]
		run.controlMu.Unlock()
		if !run.reservationInState(q.ReservationID, p, javarunner.HostCommitted) {
			return phase.response, javarunner.ErrHostState
		}
	}
	// A successful slot closure is permanent and can be replayed after host
	// retirement. It never asserts host termination or memory-budget release.
	return phase.response, nil
}

func (run *PooledRun) reservationInState(reservation string, p *pooledPreparation, state javarunner.HostState) bool {
	if p == nil {
		return false
	}
	hosts, _ := run.ledger.Snapshot()
	count := 0
	for _, host := range hosts {
		if host.ReservationID == reservation {
			if host.State != state {
				return false
			}
			count += len(host.SlotIDs)
		}
	}
	return count == len(p.slots)
}

func (run *PooledRun) reservationHasSlot(reservation, hostID, slotID string) bool {
	hosts, _ := run.ledger.Snapshot()
	for _, host := range hosts {
		if host.ReservationID == reservation && host.ID == hostID {
			for _, slot := range host.SlotIDs {
				if slot == slotID {
					return true
				}
			}
		}
	}
	return false
}

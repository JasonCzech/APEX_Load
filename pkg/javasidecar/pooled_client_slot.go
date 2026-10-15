package javasidecar

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// PooledClientSlot is a unique, non-reusable slot in a prepared run. Do not copy
// it. Closure is independent of the invocation mutex so cancellation/control
// cannot queue behind user code. Host/memory release remains runner-authoritative.
type PooledClientSlot struct {
	run                         *PooledClientRun
	hostID, slotID, reservation string
	userID                      int64
	invokeMu                    sync.Mutex
	sequence                    uint64
	initialized, ended          bool
	terminal                    atomic.Bool
	closeMu                     sync.Mutex
	closeConfirmed              bool
}

func (s *PooledClientSlot) VUserID() int64 { return s.userID }
func (s *PooledClientSlot) HostID() string { return s.hostID }
func (s *PooledClientSlot) SlotID() string { return s.slotID }

// Mode is the run's frozen decision mode ("pooled_pilot" or "shared").
func (s *PooledClientSlot) Mode() string { return s.run.decision.Mode }

// Invoke consumes a sequence before submitting any work. Any remote failure
// permanently fences this handle, including errors that appear retryable. The
// caller must close it; a later invocation must never reconstruct Java state.
func (s *PooledClientSlot) Invoke(ctx context.Context, method string, params map[string]string, timeout time.Duration) (*PooledInvocationResult, error) {
	if !s.invokeMu.TryLock() {
		return nil, errPooledSlotBusy
	}
	defer s.invokeMu.Unlock()
	if err := s.run.Err(); err != nil {
		return nil, err
	}
	s.run.mu.Lock()
	committed := s.run.committed
	s.run.mu.Unlock()
	if !committed || s.terminal.Load() || s.ended || (!s.initialized && method != "init") || (s.initialized && method == "init") {
		return nil, ErrPooledClientState
	}
	q := s.run.request("invoke", "invoke-"+s.slotID+"-"+strconv.FormatUint(s.sequence+1, 10))
	q.ReservationID, q.HostID, q.SlotID = s.reservation, s.hostID, s.slotID
	q.Sequence, q.Method, q.TimeoutMS = s.sequence+1, method, timeout.Milliseconds()
	q.Params = params
	if timeout < time.Millisecond || timeout > MaximumExecutionTimeout || !validPooledInvocation(q) {
		return nil, ErrPooledClientState
	}
	q.Params = make(map[string]string, len(params))
	for k, v := range params {
		q.Params[k] = v
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.sequence = q.Sequence
	// Give the runner its interrupt grace after the invocation timeout. Parent
	// cancellation still cancels the HTTP operation immediately.
	invokeCtx, cancel := context.WithTimeout(ctx, timeout+2*time.Second)
	defer cancel()
	response, err := s.run.call(invokeCtx, q)
	if err != nil {
		s.terminal.Store(true)
		return nil, err
	}
	result := response.Invocation
	if method == "init" {
		s.initialized = result.ReturnCode == 0
		if !s.initialized {
			s.terminal.Store(true)
		}
	}
	if method == "end" {
		s.ended = true
	}
	return result, nil
}

// Close permanently fences this handle even when the receipt is lost. An exact
// retry can recover a closure receipt; it cannot invoke or allocate a new slot.
// A slot receipt releases no host or memory charge.
func (s *PooledClientSlot) Close(ctx context.Context) error {
	s.terminal.Store(true)
	if !s.closeMu.TryLock() {
		return errPooledSlotBusy
	}
	defer s.closeMu.Unlock()
	if s.closeConfirmed {
		return nil
	}
	q := s.run.request("close_slot", "close-"+s.slotID)
	q.ReservationID, q.HostID, q.SlotID = s.reservation, s.hostID, s.slotID
	_, err := s.run.call(ctx, q)
	if err == nil {
		s.closeConfirmed = true
	}
	return err
}

// TargetClass is the immutable class identity negotiated for this run.
func (s *PooledClientSlot) TargetClass() string { return s.run.decision.TargetClass }

// Err reports loss of the owning run, independently of invocation.
func (s *PooledClientSlot) Err() error { return s.run.Err() }

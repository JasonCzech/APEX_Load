package javasidecar

import (
	"context"
	"errors"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/javarunner"
)

var errPooledInvocationDisabled = errors.New("pooled_invocation_disabled")
var errPooledSlotBusy = errors.New("pooled_slot_busy")

type PooledExit struct {
	Option int   `json:"option"`
	Status int32 `json:"status"`
}

type PooledInvocationResult struct {
	Sequence        uint64      `json:"sequence"`
	Method          string      `json:"method"`
	EventRequestID  string      `json:"event_request_id"`
	ReturnCode      int32       `json:"return_code"`
	Exit            *PooledExit `json:"exit,omitempty"`
	Events          string      `json:"events,omitempty"`
	EventsTruncated bool        `json:"events_truncated,omitempty"`
	DurationMS      int64       `json:"duration_ms"`
}

// Invocation identities are deterministic per slot and monotonically advance.
// Lost responses cannot be replayed: the caller must close/abort, never retry
// initialization or manufacture a new sequence to repeat an ambiguous action.
func validPooledInvocation(q PooledControlRequest) bool {
	if q.VUsers != 0 || q.PrepareTimeoutMS != 0 || !executionIDPattern.MatchString(q.ReservationID) || !executionIDPattern.MatchString(q.HostID) || !executionIDPattern.MatchString(q.SlotID) || q.Sequence == 0 || q.Sequence > 1<<63-1 || q.RequestID != "invoke-"+q.SlotID+"-"+strconv.FormatUint(q.Sequence, 10) || q.TimeoutMS < 1 || q.TimeoutMS > MaximumExecutionTimeout.Milliseconds() || len(q.Params) > 256 {
		return false
	}
	if q.Method != "init" && q.Method != "action" && q.Method != "end" {
		return false
	}
	bytes := 0
	for k, v := range q.Params {
		if len(k) == 0 || len(k) > 126 || len(v) > 65536 || !utf8.ValidString(k) || !utf8.ValidString(v) {
			return false
		}
		bytes += len(k) + len(v)
	}
	return bytes <= 256<<10
}

func validPooledInvocationResult(q PooledControlRequest, r *PooledInvocationResult) bool {
	if r == nil || r.Sequence != q.Sequence || r.Method != q.Method || r.DurationMS < 0 || r.DurationMS > MaximumExecutionTimeout.Milliseconds()+2000 {
		return false
	}
	n, err := strconv.ParseUint(r.EventRequestID, 10, 63)
	if err != nil || n == 0 || strconv.FormatUint(n, 10) != r.EventRequestID {
		return false
	}
	if r.Exit != nil && (q.Method != "action" || (r.Exit.Option != 1 && r.Exit.Option != 2)) {
		return false
	}
	return validPooledEvents(r.Events, r.EventRequestID)
}

func (run *PooledRun) invokeControl(ctx context.Context, q PooledControlRequest, response PooledControlResponse) (PooledControlResponse, error) {
	if !run.controlDecision.InvocationEnabled {
		return response, errPooledInvocationDisabled
	}
	finish, err := run.begin(ctx)
	if err != nil {
		return response, err
	}
	defer finish()
	run.controlMu.Lock()
	if _, exists := run.controlReplies[q.RequestID]; exists {
		run.controlMu.Unlock()
		return response, errPooledRequestConflict
	}
	p := run.preparations[q.ReservationID]
	if p == nil || p.commit == nil {
		run.controlMu.Unlock()
		return response, javarunner.ErrHostState
	}
	select {
	case <-p.commit.done:
	default:
		run.controlMu.Unlock()
		return response, javarunner.ErrHostState
	}
	capture := p.captures[q.SlotID]
	if p.commit.err != nil || capture == nil || !run.reservationHasSlot(q.ReservationID, q.HostID, q.SlotID) {
		run.controlMu.Unlock()
		return response, javarunner.ErrHostIdentity
	}
	run.controlMu.Unlock()
	if !capture.mu.TryLock() {
		return response, errPooledSlotBusy
	}
	defer capture.mu.Unlock()
	if q.Sequence != capture.sequence+1 {
		return response, errPooledRequestConflict
	}
	if capture.ended || (!capture.initialized && q.Method != "init") || (capture.initialized && q.Method == "init") {
		return response, javarunner.ErrHostState
	}
	if err := run.ledger.CanInvoke(run.owner.epoch, q.HostID, q.SlotID); err != nil {
		return response, err
	}
	// Consume the sequence before any mutation or submission, even if capture
	// reset or transport fails. This is a permanent at-most-once boundary.
	capture.sequence = q.Sequence
	if err := capture.reset(); err != nil {
		_ = run.ledger.FailHost(q.HostID, errPooledEventCapture.Error())
		return response, err
	}
	invokeCtx, cancel := context.WithTimeout(ctx, time.Duration(q.TimeoutMS)*time.Millisecond)
	defer cancel()
	started := time.Now()
	frame, err := run.Invoke(invokeCtx, q.HostID, q.SlotID, q.Method, q.Params)
	if err != nil {
		capture.ended = true
		// The supervisor preserves qualified cooperative cancellation. All other
		// uncertain failures must reap the host, not leave a reusable live slot.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			if run.ledger.CanInvoke(run.owner.epoch, q.HostID, q.SlotID) == nil {
				_ = run.ledger.FailHost(q.HostID, "pooled_invocation_cancelled")
			}
		} else if err.Error() != "slot_closed" {
			_ = run.ledger.FailHost(q.HostID, "pooled_invocation_failed")
		}
		return response, err
	}
	result, err := pooledInvocationFrame(q, frame)
	if err != nil {
		_ = run.ledger.FailHost(q.HostID, "pooled_frame_invalid")
		return response, err
	}
	result.DurationMS = time.Since(started).Milliseconds()
	events, truncated, err := capture.read(result.EventRequestID)
	if err != nil {
		_ = run.ledger.FailHost(q.HostID, errPooledEventCapture.Error())
		return response, err
	}
	result.Events, result.EventsTruncated = events, result.EventsTruncated || truncated
	if q.Method == "init" {
		capture.initialized = result.ReturnCode == 0
		if !capture.initialized {
			capture.ended = true
			_ = run.ledger.FailHost(q.HostID, "pooled_invocation_failed")
		}
	}
	if q.Method == "end" {
		capture.ended = true
	}
	if frame.Fields["reap"] == "true" {
		_ = run.ledger.FailHost(q.HostID, "pooled_invocation_failed")
	}
	response.ReservationID, response.HostID, response.SlotID = q.ReservationID, q.HostID, q.SlotID
	response.Invocation = result
	return response, nil
}

func pooledInvocationFrame(q PooledControlRequest, f javarunner.PooledFrame) (*PooledInvocationResult, error) {
	fail := func() (*PooledInvocationResult, error) { return nil, javarunner.ErrPooledFrame }
	if f.Generation != q.HostID || f.Slot != q.SlotID || f.Operation != "invoke" || f.Sequence == 0 || f.Sequence > 1<<63-1 || f.Request != strconv.FormatUint(f.Sequence, 10) || f.Fields["code"] != "ok" || (f.Fields["reap"] != "true" && f.Fields["reap"] != "false") {
		return fail()
	}
	rc, err := strconv.ParseInt(f.Fields["return_code"], 10, 32)
	if err != nil {
		return fail()
	}
	for _, key := range []string{"event_fallback", "event_truncated"} {
		if f.Fields[key] != "true" && f.Fields[key] != "false" {
			return fail()
		}
	}
	r := &PooledInvocationResult{Sequence: q.Sequence, Method: q.Method, EventRequestID: f.Request, ReturnCode: int32(rc), EventsTruncated: f.Fields["event_fallback"] == "true" || f.Fields["event_truncated"] == "true"}
	option, hasOption := f.Fields["exit_option"]
	status, hasStatus := f.Fields["exit_status"]
	if hasOption != hasStatus {
		return fail()
	}
	if hasOption {
		if q.Method != "action" || (option != "1" && option != "2") {
			return fail()
		}
		n, err := strconv.ParseInt(status, 10, 32)
		if err != nil {
			return fail()
		}
		o, _ := strconv.Atoi(option)
		r.Exit = &PooledExit{Option: o, Status: int32(n)}
		if len(f.Fields) != 7 {
			return fail()
		}
	} else if len(f.Fields) != 5 {
		return fail()
	}
	return r, nil
}

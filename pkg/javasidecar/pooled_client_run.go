package javasidecar

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
)

var (
	ErrPooledClientClosed   = errors.New("pooled_client_closed")
	ErrPooledClientState    = errors.New("pooled_client_state_invalid")
	ErrPooledClientDecision = errors.New("pooled_client_decision_changed")
)

// PooledClientOptions must come from an operator-owned, frozen preflight
// decision. This API neither selects pooling nor claims or renews a generator
// owner. The generator must maintain its existing owner heartbeat separately;
// a group lease must never reacquire an owner epoch after takeover.
// Lifetime is supplied to OpenPooledClientRun, independently of action timeouts.
type PooledClientOptions struct {
	ExecutionID string
	GroupID     string
	Decision    PooledControlDecision
}

// PooledClientRun owns one experimental execution/group. Preparation reserves
// the entire admitted peak; dynamic expansion is deliberately unavailable.
// It must not be copied. Close waits for positive runner cleanup; Err preserves
// the first lifetime/lease failure even when cleanup also fails.
type PooledClientRun struct {
	nativeData          bool
	client              *Client // separate owner snapshot; never follows Client.ClaimOwner
	decision            PooledControlDecision
	runID, token        string
	ctx                 context.Context
	cancel              context.CancelCauseFunc
	done                chan struct{}
	phase               sync.Mutex
	mu                  sync.Mutex
	prepared, committed bool
	attempted           bool
	reservation         string
	deadline            time.Time
	cleanupErr          error
	renewEvery          time.Duration
}

// OpenPooledClientRun explicitly negotiates invocation against an already
// claimed owner. An ambiguous claim is never retried and expires at the runner.
func (c *Client) OpenPooledClientRun(ctx context.Context, options PooledClientOptions) (*PooledClientRun, error) {
	if ctx == nil {
		return nil, ErrPooledClientState
	}
	owner, epoch, _, _ := c.ownerAndRuntimeLease()
	if !executionIDPattern.MatchString(owner) || !executionIDPattern.MatchString(epoch) || !options.Decision.InvocationEnabled {
		return nil, ErrPooledClientState
	}
	// Do not copy mutexes or the mutable dedicated runtime lease.
	frozen := &Client{baseURL: c.baseURL, httpClient: c.httpClient, authToken: c.authToken, ownerID: owner, ownerEpoch: epoch}
	claimCtx, cancel := context.WithTimeout(ctx, RuntimeLeaseTTL)
	defer cancel()
	r, err := frozen.PooledControl(claimCtx, PooledControlRequest{Operation: "claim", RequestID: "client-claim", ExecutionID: options.ExecutionID, GroupID: options.GroupID, ProfileID: options.Decision.ProfileID})
	if err != nil {
		return nil, err
	}
	return newPooledClientRun(ctx, frozen, options.Decision, r)
}

func newPooledClientRun(ctx context.Context, client *Client, expected PooledControlDecision, claim PooledControlResponse) (*PooledClientRun, error) {
	runCtx, cancel := context.WithCancelCause(ctx)
	r := &PooledClientRun{client: client, decision: expected, runID: claim.RunID, token: claim.RunToken, ctx: runCtx, cancel: cancel, done: make(chan struct{}), renewEvery: RuntimeLeaseTTL / 3}
	if claim.Decision != expected || !expected.InvocationEnabled {
		cancel(ErrPooledClientDecision)
		go r.maintain()
		return nil, ErrPooledClientDecision
	}
	go r.maintain()
	if err := r.Err(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *PooledClientRun) Decision() PooledControlDecision { return r.decision }
func (r *PooledClientRun) Done() <-chan struct{}           { return r.ctx.Done() }
func (r *PooledClientRun) Err() error                      { return context.Cause(r.ctx) }

// Diagnostics is an explicit host-scoped snapshot, never slot output. Call only
// after negotiating FeaturePooledDiagnosticsV1, and display as untrusted output.
// No diagnostics are retained after the host is reaped.
func (r *PooledClientRun) Diagnostics(ctx context.Context, hostID string) (javarunner.HostDiagnostics, error) {
	q := r.request("diagnostics", "client-diagnostics")
	q.HostID = hostID
	response, err := r.call(ctx, q)
	if err != nil {
		return javarunner.HostDiagnostics{}, err
	}
	return *response.Diagnostics, nil
}

func (r *PooledClientRun) request(op, id string) PooledControlRequest {
	return PooledControlRequest{Operation: op, RequestID: id, RunID: r.runID, RunToken: r.token}
}

func (r *PooledClientRun) call(ctx context.Context, q PooledControlRequest) (PooledControlResponse, error) {
	if err := r.Err(); err != nil {
		return PooledControlResponse{}, err
	}
	callCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(r.ctx, cancel)
	defer stop()
	defer cancel()
	response, err := r.client.PooledControl(callCtx, q)
	if err == nil && response.Decision != r.decision {
		err = ErrPooledClientDecision
		r.cancel(err)
	}
	if cause := r.Err(); cause != nil {
		return PooledControlResponse{}, cause
	}
	return response, err
}

func (r *PooledClientRun) maintain() {
	defer close(r.done)
	ticker := time.NewTicker(r.renewEvery)
	defer ticker.Stop()
	for {
		select {
		case <-r.ctx.Done():
			// Cleanup uses the frozen epoch and independent bounded context. Lease
			// expiry remains authoritative if the runner cannot acknowledge cleanup.
			ctx, cancel := context.WithTimeout(context.Background(), RuntimeLeaseTTL)
			response, err := r.client.PooledControl(ctx, r.request("close", "client-close"))
			if err == nil && response.Decision != r.decision {
				err = ErrPooledClientDecision
			}
			cancel()
			r.mu.Lock()
			r.cleanupErr = err
			r.mu.Unlock()
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(r.ctx, r.renewEvery)
			_, err := r.call(ctx, r.request("renew", "client-renew"))
			cancel()
			if err != nil {
				r.cancel(err)
			}
		}
	}
}

// Close fences new work immediately, independently of blocked preparation or
// invocation. A timeout is not cleanup evidence; callers may wait again.
func (r *PooledClientRun) Close(ctx context.Context) error {
	r.cancel(ErrPooledClientClosed)
	select {
	case <-r.done:
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.cleanupErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Prepare performs one cold reservation/preparation with an operator staging
// deadline. It never invokes user code. Supplied SlotIDs must be empty; identities
// are assigned by the runner. Any failed attempt permanently retires the run.
func (r *PooledClientRun) Prepare(ctx context.Context, users []PooledSlotContext, timeout time.Duration) ([]*PooledClientSlot, error) {
	if !r.phase.TryLock() {
		return nil, ErrPooledClientState
	}
	defer r.phase.Unlock()
	if timeout < time.Millisecond || timeout > time.Hour || len(users) == 0 || len(users) > r.decision.MaxVUsers {
		return nil, ErrPooledClientState
	}
	copied := append([]PooledSlotContext(nil), users...)
	for i := range copied {
		if copied[i].SlotID != "" {
			return nil, ErrPooledClientState
		}
		copied[i].SlotID = "validate-" + strconv.Itoa(i)
	}
	if !validPooledSlots(copied) {
		return nil, ErrPooledClientState
	}
	r.mu.Lock()
	if r.attempted {
		r.mu.Unlock()
		return nil, ErrPooledClientState
	}
	r.attempted = true
	r.mu.Unlock()
	stageCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	q := r.request("reserve", "client-reserve")
	q.VUsers, q.PrepareTimeoutMS = len(users), timeout.Milliseconds()
	allocation, err := r.call(stageCtx, q)
	if err != nil {
		r.cancel(err)
		return nil, err
	}
	deadline := time.UnixMilli(allocation.PrepareDeadlineUnixMS)
	localDeadline, _ := stageCtx.Deadline()
	if deadline.After(localDeadline) {
		deadline = localDeadline
	}
	stageCtx, cancelPrepare := context.WithDeadline(stageCtx, deadline)
	defer cancelPrepare()
	slots := make([]*PooledClientSlot, 0, len(users))
	for _, host := range allocation.Placements {
		for _, slot := range host.SlotIDs {
			copied[len(slots)].SlotID = slot
			slots = append(slots, &PooledClientSlot{run: r, hostID: host.HostID, slotID: slot, reservation: allocation.ReservationID, userID: copied[len(slots)].VUserID})
		}
	}
	q = r.request("prepare", "client-prepare")
	q.ReservationID, q.Slots = allocation.ReservationID, copied
	ready, err := r.call(stageCtx, q)
	if err == nil && ready.PrepareDeadlineUnixMS != allocation.PrepareDeadlineUnixMS {
		err = ErrPooledClientDecision
	}
	if err != nil {
		r.cancel(err)
		return nil, err
	}
	r.mu.Lock()
	r.prepared, r.reservation, r.deadline = true, allocation.ReservationID, deadline
	r.nativeData = ready.NativeData
	r.mu.Unlock()
	return append([]*PooledClientSlot(nil), slots...), nil
}

// Commit opens the execution barrier once. Callers invoke init later according
// to their ramp. A failed or ambiguous commit retires the entire run.
func (r *PooledClientRun) Commit(ctx context.Context) error {
	if !r.phase.TryLock() {
		return ErrPooledClientState
	}
	defer r.phase.Unlock()
	r.mu.Lock()
	if !r.prepared || r.committed {
		r.mu.Unlock()
		return ErrPooledClientState
	}
	reservation, deadline := r.reservation, r.deadline
	r.mu.Unlock()
	commitCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	q := r.request("commit", "client-commit")
	q.ReservationID = reservation
	_, err := r.call(commitCtx, q)
	if err != nil {
		r.cancel(err)
		return err
	}
	r.mu.Lock()
	r.committed = true
	r.mu.Unlock()
	return nil
}

func (r *PooledClientRun) NativeDataAvailable() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.prepared && !r.committed && r.nativeData && r.ctx.Err() == nil
}

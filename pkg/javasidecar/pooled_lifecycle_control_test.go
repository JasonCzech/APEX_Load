package javasidecar

import (
	"context"
	"errors"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
)

func lifecycleFixture(t *testing.T, h *preparationTestHost) (*PooledRun, *Client, PooledControlResponse, PooledControlRequest) {
	t.Helper()
	run, c, placement, prepare := preparationControlFixture(t, h, true)
	if _, err := c.PooledControl(context.Background(), prepare); err != nil {
		t.Fatal(err)
	}
	commit := prepare
	commit.Operation, commit.RequestID, commit.Slots = "commit", "commit-1", nil
	return run, c, placement, commit
}

func closeSlotControl(commit PooledControlRequest, placement PooledControlResponse, index int) PooledControlRequest {
	q := commit
	q.Operation, q.RequestID = "close_slot", "close-slot-"+strconv.Itoa(index)
	q.HostID, q.SlotID = placement.Placements[0].HostID, placement.Placements[0].SlotIDs[index]
	return q
}

func TestPooledLifecycleCommitAndPermanentClosure(t *testing.T) {
	var closes atomic.Int32
	h := &preparationTestHost{ownedTestHost: ownedTestHost{done: make(chan struct{})}, ready: func(context.Context, map[string]map[string]string) error { return nil }, closeSlot: func(context.Context, string) error { closes.Add(1); return nil }}
	run, c, placement, q := lifecycleFixture(t, h)
	for range 2 {
		r, err := c.PooledControl(context.Background(), q)
		if err != nil || !r.Committed || r.Ready || r.Reaped {
			t.Fatalf("commit result: %+v %v", r, err)
		}
	}
	if h.commits.Load() != 1 {
		t.Fatal("commit replay reached host")
	}
	changed := q
	changed.RequestID = "commit-again"
	_, err := c.PooledControl(context.Background(), changed)
	requireControlError(t, err, "pooled_request_conflict")
	first := closeSlotControl(q, placement, 0)
	for range 2 {
		r, err := c.PooledControl(context.Background(), first)
		if err != nil || !r.SlotClosed || r.Reaped {
			t.Fatalf("slot close: %+v %v", r, err)
		}
	}
	hosts, usage := run.ledger.Snapshot()
	if len(hosts) != 1 || hosts[0].State != javarunner.HostDraining || usage.Hosts != 1 || usage.VUsers != 1 || usage.MemoryBytes != placement.Placements[0].MemoryBytes {
		t.Fatalf("slot closure released host charge: %+v %+v", hosts, usage)
	}
	if closes.Load() != 1 {
		t.Fatal("closure replay reached host")
	}
	if err := run.ledger.CanInvoke(run.owner.epoch, first.HostID, placement.Placements[0].SlotIDs[1]); err != nil {
		t.Fatalf("surviving slot lost: %v", err)
	}
	_, err = c.PooledControl(context.Background(), q)
	requireControlError(t, err, "pooled_host_state_invalid")
	second := closeSlotControl(q, placement, 1)
	if _, err := c.PooledControl(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	waitCaptureCleanup(t, preparationRecord(run, q.ReservationID))
	_, usage = run.ledger.Snapshot()
	if usage.Hosts != 0 || usage.VUsers != 0 || usage.MemoryBytes != 0 {
		t.Fatalf("positive reap did not release capacity: %+v", usage)
	}
	if _, err := c.PooledControl(context.Background(), first); err != nil {
		t.Fatalf("permanent closure replay failed after reap: %v", err)
	}
	if closes.Load() != 2 || h.stops.Load() != 1 {
		t.Fatal("cleanup was repeated")
	}
}

func TestPooledLifecycleCommitBlockedControlsAndReplay(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	h := &preparationTestHost{ownedTestHost: ownedTestHost{done: make(chan struct{})}, ready: func(context.Context, map[string]map[string]string) error { return nil }, commit: func(ctx context.Context) error {
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	run, c, placement, q := lifecycleFixture(t, h)
	result := make(chan error, 2)
	go func() { _, err := c.PooledControl(context.Background(), q); result <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("commit never started")
	}
	go func() { _, err := c.PooledControl(context.Background(), q); result <- err }()
	if err := run.ledger.CanInvoke(run.owner.epoch, placement.Placements[0].HostID, placement.Placements[0].SlotIDs[0]); err == nil {
		t.Fatal("invocation allowed before commit receipt")
	}
	for _, op := range []string{"renew", "abort"} {
		control := q
		control.Operation, control.RequestID = op, op+"-blocked"
		if op == "renew" {
			control.ReservationID = ""
		}
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		_, err := c.PooledControl(ctx, control)
		cancel()
		if err != nil {
			t.Fatalf("%s blocked behind commit: %v", op, err)
		}
	}
	close(release)
	for range 2 {
		if err := <-result; err == nil {
			t.Fatal("aborted commit reported success")
		}
	}
	if h.commits.Load() != 1 {
		t.Fatal("concurrent retry committed again")
	}
	waitCaptureCleanup(t, preparationRecord(run, q.ReservationID))
}

func TestPooledLifecycleCommitKeepsPreparationDeadline(t *testing.T) {
	var deadline time.Time
	h := &preparationTestHost{ownedTestHost: ownedTestHost{done: make(chan struct{})}, ready: func(context.Context, map[string]map[string]string) error { return nil }, commit: func(ctx context.Context) error {
		deadline, _ = ctx.Deadline()
		<-ctx.Done()
		return ctx.Err()
	}}
	run, c, placement, q := lifecycleFixture(t, h)
	_, err := c.PooledControl(context.Background(), q)
	requireControlError(t, err, "pooled_control_failed")
	run.controlMu.Lock()
	phase := run.preparations[q.ReservationID].commit
	run.controlMu.Unlock()
	<-phase.done
	if deadline.UnixMilli() != placement.PrepareDeadlineUnixMS {
		t.Fatal("commit extended preparation window")
	}
	waitCaptureCleanup(t, preparationRecord(run, q.ReservationID))
	_, err = c.PooledControl(context.Background(), q)
	requireControlError(t, err, "pooled_control_failed")
	if h.commits.Load() != 1 {
		t.Fatal("failed commit repeated")
	}
}

func TestPooledLifecycleCancelledRetryDoesNotCancelCommit(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	h := &preparationTestHost{ownedTestHost: ownedTestHost{done: make(chan struct{})}, ready: func(context.Context, map[string]map[string]string) error { return nil }, commit: func(ctx context.Context) error {
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	_, c, _, q := lifecycleFixture(t, h)
	result := make(chan error, 1)
	go func() { _, err := c.PooledControl(context.Background(), q); result <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("commit never started")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	_, err := c.PooledControl(ctx, q)
	cancel()
	if err == nil {
		t.Fatal("retry did not time out")
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatalf("retry cancelled original operation: %v", err)
	}
	if h.commits.Load() != 1 {
		t.Fatal("concurrent retry reached host")
	}
}

func TestPooledLifecycleClosureRemainsAvailableWithFullCache(t *testing.T) {
	h := &preparationTestHost{ownedTestHost: ownedTestHost{done: make(chan struct{})}, ready: func(context.Context, map[string]map[string]string) error { return nil }}
	run, c, placement, q := lifecycleFixture(t, h)
	if _, err := c.PooledControl(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	run.controlMu.Lock()
	for i := len(run.controlReplies); i < maxPooledControlReplies; i++ {
		run.controlReplies[strconv.Itoa(i)] = pooledControlReply{}
	}
	run.controlMu.Unlock()
	for i := range 2 {
		closeQ := closeSlotControl(q, placement, i)
		for range 2 {
			if _, err := c.PooledControl(context.Background(), closeQ); err != nil {
				t.Fatal(err)
			}
		}
		closeQ.RequestID += "-different"
		_, err := c.PooledControl(context.Background(), closeQ)
		requireControlError(t, err, "pooled_request_conflict")
	}
	run.controlMu.Lock()
	size := len(run.controlReplies)
	run.controlMu.Unlock()
	if size != maxPooledControlReplies+2 {
		t.Fatalf("unbounded closure records: %d", size)
	}
	waitCaptureCleanup(t, preparationRecord(run, q.ReservationID))
}

func TestPooledLifecycleClosureFailureReapsPeers(t *testing.T) {
	var closes atomic.Int32
	h := &preparationTestHost{ownedTestHost: ownedTestHost{done: make(chan struct{})}, ready: func(context.Context, map[string]map[string]string) error { return nil }, closeSlot: func(context.Context, string) error {
		closes.Add(1)
		return errors.New("untrusted backend diagnostic")
	}}
	run, c, placement, q := lifecycleFixture(t, h)
	if _, err := c.PooledControl(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	closeQ := closeSlotControl(q, placement, 0)
	for range 2 {
		_, err := c.PooledControl(context.Background(), closeQ)
		requireControlError(t, err, "pooled_control_failed")
		if !PooledControlOutcomeAmbiguous(err) {
			t.Fatal("uncertain closure called definitive")
		}
	}
	waitCaptureCleanup(t, preparationRecord(run, q.ReservationID))
	_, err := c.PooledControl(context.Background(), closeSlotControl(q, placement, 1))
	requireControlError(t, err, "pooled_host_reaped")
	if closes.Load() != 1 || h.stops.Load() != 1 {
		t.Fatal("failure retried or cleanup repeated")
	}
}

func TestPooledLifecycleOptInAndValidation(t *testing.T) {
	p := controlProfileFixture(t)
	p.AllowCommit = true
	if _, err := NewPooledControlProfiles(map[string]PooledControlProfile{"pilot": p}); err == nil {
		t.Fatal("commit without preparation accepted")
	}
	_, c, _ := controlServerFixture(t, true)
	claim := claimControl(t, c)
	if claim.Decision.CommitEnabled {
		t.Fatal("preparation implicitly enabled commit")
	}
	q := controlRequest(claim, "commit", "commit")
	q.ReservationID = "reservation"
	_, err := c.PooledControl(context.Background(), q)
	requireControlError(t, err, "pooled_commit_disabled")
	_, c, _ = controlServerFixture(t, true, true)
	if !claimControl(t, c).Decision.CommitEnabled {
		t.Fatal("operator commit flag lost")
	}
}

func TestPooledLifecycleCancellationBeforeSupervisorStillCleansUp(t *testing.T) {
	for _, operation := range []string{"commit", "close_slot"} {
		t.Run(operation, func(t *testing.T) {
			var closes atomic.Int32
			h := &preparationTestHost{ownedTestHost: ownedTestHost{done: make(chan struct{})}, ready: func(context.Context, map[string]map[string]string) error { return nil }, closeSlot: func(context.Context, string) error { closes.Add(1); return nil }}
			run, c, placement, q := lifecycleFixture(t, h)
			if operation == "close_slot" {
				if _, err := c.PooledControl(context.Background(), q); err != nil {
					t.Fatal(err)
				}
				q = closeSlotControl(q, placement, 0)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			phase := &pooledControlPhase{done: make(chan struct{})}
			run.executeControlPhase(ctx, q, phase)
			if !errors.Is(phase.err, context.Canceled) {
				t.Fatalf("original error lost: %v", phase.err)
			}
			waitCaptureCleanup(t, preparationRecord(run, q.ReservationID))
			if (operation == "commit" && h.commits.Load() != 0) || closes.Load() != 0 || h.stops.Load() != 1 {
				t.Fatal("cancelled phase invoked host or missed containment cleanup")
			}
		})
	}
}

func TestPooledLifecycleRejectsUnreadyOrForeignSlots(t *testing.T) {
	h := &preparationTestHost{ownedTestHost: ownedTestHost{done: make(chan struct{})}, ready: func(context.Context, map[string]map[string]string) error { return nil }}
	run, c, placement, prepare := preparationControlFixture(t, h, true)
	commit := prepare
	commit.Operation, commit.RequestID, commit.Slots = "commit", "commit-1", nil
	_, err := c.PooledControl(context.Background(), commit)
	requireControlError(t, err, "pooled_host_identity_invalid")
	if _, err := c.PooledControl(context.Background(), prepare); err != nil {
		t.Fatal(err)
	}
	closeQ := closeSlotControl(commit, placement, 0)
	_, err = c.PooledControl(context.Background(), closeQ)
	requireControlError(t, err, "pooled_host_state_invalid")
	if _, err := c.PooledControl(context.Background(), commit); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"reservation", "host", "slot"} {
		bad := closeQ
		switch field {
		case "reservation":
			bad.ReservationID = "foreign"
		case "host":
			bad.HostID = "foreign"
		case "slot":
			bad.SlotID = "foreign"
		}
		_, err := c.PooledControl(context.Background(), bad)
		requireControlError(t, err, "pooled_host_identity_invalid")
	}
	run.controlMu.Lock()
	_, consumed := run.controlReplies[closeQ.RequestID]
	run.controlMu.Unlock()
	if consumed {
		t.Fatal("foreign slot consumed replay record")
	}
	if _, err := c.PooledControl(context.Background(), closeQ); err != nil {
		t.Fatal(err)
	}
	changed := closeQ
	changed.SlotID = placement.Placements[0].SlotIDs[1]
	_, err = c.PooledControl(context.Background(), changed)
	requireControlError(t, err, "pooled_request_conflict")
}

func TestPooledLifecycleSlotControlsIndependent(t *testing.T) {
	entered := make(chan struct{})
	var calls atomic.Int32
	h := &preparationTestHost{ownedTestHost: ownedTestHost{done: make(chan struct{})}, ready: func(context.Context, map[string]map[string]string) error { return nil }, closeSlot: func(ctx context.Context, _ string) error {
		if calls.Add(1) == 1 {
			close(entered)
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}}
	run, c, placement, q := lifecycleFixture(t, h)
	if _, err := c.PooledControl(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := c.PooledControl(ctx, closeSlotControl(q, placement, 0)); result <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("closure never started")
	}
	controlCtx, controlCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer controlCancel()
	renew := q
	renew.Operation, renew.RequestID, renew.ReservationID = "renew", "renew-closure", ""
	if _, err := c.PooledControl(controlCtx, renew); err != nil {
		t.Fatalf("renewal blocked behind closure: %v", err)
	}
	if _, err := c.PooledControl(controlCtx, closeSlotControl(q, placement, 1)); err != nil {
		t.Fatalf("peer closure blocked: %v", err)
	}
	_, usage := run.ledger.Snapshot()
	if usage.Hosts != 1 || usage.VUsers != 1 {
		t.Fatalf("busy closure released capacity: %+v", usage)
	}
	cancel()
	if err := <-result; err == nil {
		t.Fatal("cancelled closure reported success")
	}
	waitCaptureCleanup(t, preparationRecord(run, q.ReservationID))
}

func TestPooledLifecycleClientRejectsMalformedReceipts(t *testing.T) {
	h := &preparationTestHost{ownedTestHost: ownedTestHost{done: make(chan struct{})}, ready: func(context.Context, map[string]map[string]string) error { return nil }}
	_, c, placement, q := lifecycleFixture(t, h)
	commit, err := c.PooledControl(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*PooledControlResponse){
		func(r *PooledControlResponse) { r.Committed = false },
		func(r *PooledControlResponse) { r.Ready = true },
		func(r *PooledControlResponse) { r.Reaped = true },
		func(r *PooledControlResponse) { r.Decision.CommitEnabled = false },
		func(r *PooledControlResponse) { r.Decision.PreparationEnabled = false },
		func(r *PooledControlResponse) { r.ReservationID = "other" },
		func(r *PooledControlResponse) { r.PrepareDeadlineUnixMS = 1 },
		func(r *PooledControlResponse) { r.SlotID = "unexpected" },
	} {
		r := commit
		mutate(&r)
		if validPooledControlResponse(q, r) {
			t.Fatalf("malformed commit accepted: %+v", r)
		}
	}
	q = closeSlotControl(q, placement, 0)
	closed, err := c.PooledControl(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*PooledControlResponse){
		func(r *PooledControlResponse) { r.SlotClosed = false },
		func(r *PooledControlResponse) { r.Reaped = true },
		func(r *PooledControlResponse) { r.Committed = true },
		func(r *PooledControlResponse) { r.HostID = "other" },
		func(r *PooledControlResponse) { r.SlotID = "other" },
		func(r *PooledControlResponse) { r.Decision.CommitEnabled = false },
	} {
		r := closed
		mutate(&r)
		if validPooledControlResponse(q, r) {
			t.Fatalf("malformed closure accepted: %+v", r)
		}
	}
	q.OwnerID, q.OwnerEpoch = "owner", "epoch"
	for _, mutate := range []func(*PooledControlRequest){
		func(q *PooledControlRequest) { q.HostID = "" },
		func(q *PooledControlRequest) { q.SlotID = "" },
		func(q *PooledControlRequest) { q.ReservationID = "" },
		func(q *PooledControlRequest) { q.Slots = []PooledSlotContext{{SlotID: "slot", VUserID: 1}} },
		func(q *PooledControlRequest) { q.Operation = "commit" },
	} {
		bad := q
		mutate(&bad)
		if validPooledControlRequest(bad) {
			t.Fatalf("invalid lifecycle request accepted: %+v", bad)
		}
	}
}

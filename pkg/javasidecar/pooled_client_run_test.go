package javasidecar

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
)

func clientDecision(p PooledControlProfile) PooledControlDecision {
	d := p.Selection.Decision()
	return PooledControlDecision{ScriptSHA256: p.ScriptSHA256, TargetClass: d.TargetClass, PreparationEnabled: p.AllowPreparation, CommitEnabled: p.AllowCommit, InvocationEnabled: p.AllowInvocation, Mode: d.Mode, ProfileID: "pilot", ResourceProfile: d.ResourceProfile, ConfigurationSHA256: d.ConfigurationSHA256, ContainmentBackend: d.ContainmentBackend, SlotsPerHost: d.SlotsPerHost, HostMemoryBytes: d.HostMemoryBytes, MaxHosts: p.Budget.MaxHosts, MaxVUsers: p.Budget.MaxVUsers, MaxMemoryBytes: p.Budget.MaxMemoryBytes}
}

type managedClientFixture struct {
	run       *PooledClientRun
	serverRun *PooledRun
	client    *Client
	host      *preparationTestHost
	calls     atomic.Uint64
	paths     map[string]string
	hostID    string
}

func newManagedClientFixture(t *testing.T) *managedClientFixture {
	t.Helper()
	f := &managedClientFixture{paths: map[string]string{}}
	f.host = &preparationTestHost{ownedTestHost: ownedTestHost{done: make(chan struct{})}}
	f.host.ready = func(_ context.Context, values map[string]map[string]string) error {
		for slot, v := range values {
			f.paths[slot] = v["APEXLOAD_JAVA_VUSER_EVENT_FILE"]
		}
		return nil
	}
	f.host.invocation = func(_ context.Context, slot, method string, params map[string]string) (javarunner.PooledFrame, bool, error) {
		frame := invocationTestFrame(f.hostID, slot, f.calls.Add(1))
		return frame, false, os.WriteFile(f.paths[slot], []byte(invocationEvent(frame.Request, 1, method)), 0600)
	}
	s, c, _ := controlServerFixture(t, true, true, true)
	f.client = c
	p := s.pooledControlProfiles.profiles["pilot"]
	d := clientDecision(p)
	owner, epoch, _, _ := c.ownerAndRuntimeLease()
	var err error
	f.serverRun, err = s.openPooledRun(context.Background(), owner, epoch, "execution", "group", p.Budget, d.ConfigurationSHA256, javarunner.HostProfile{SlotsPerHost: d.SlotsPerHost, MemoryBytesPerHost: d.HostMemoryBytes}, pooledProvisionFunc(func(_ context.Context, a javarunner.HostAllocation) (javarunner.ManagedHost, error) {
		f.hostID = a.ID
		return f.host, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	s.pooledMu.Lock()
	f.serverRun.controlID, f.serverRun.controlToken, f.serverRun.controlDecision = "pool-managed", strings.Repeat("b", 64), d
	s.pooledMu.Unlock()
	f.run, err = newPooledClientRun(context.Background(), c, d, PooledControlResponse{RunID: f.serverRun.controlID, RunToken: f.serverRun.controlToken, Decision: d})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		_ = f.run.Close(ctx)
	})
	return f
}

func prepareManaged(t *testing.T, f *managedClientFixture) []*PooledClientSlot {
	t.Helper()
	slots, err := f.run.Prepare(context.Background(), []PooledSlotContext{{VUserID: 1}, {VUserID: 2}}, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return slots
}

func TestPooledClientRunColdBarrierAndLifecycle(t *testing.T) {
	f := newManagedClientFixture(t)
	slots := prepareManaged(t, f)
	if f.calls.Load() != 0 || f.host.commits.Load() != 0 {
		t.Fatal("preparation ran user work")
	}
	if slots[0].VUserID() != 1 || slots[1].VUserID() != 2 || slots[0].HostID() != slots[1].HostID() || slots[0].SlotID() == slots[1].SlotID() {
		t.Fatal("incorrect slot assignment")
	}
	if _, err := slots[0].Invoke(context.Background(), "init", nil, time.Second); !errors.Is(err, ErrPooledClientState) {
		t.Fatal(err)
	}
	if err := f.run.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.calls.Load() != 0 || f.host.commits.Load() != 1 {
		t.Fatal("commit invoked user code")
	}
	if err := f.run.Commit(context.Background()); !errors.Is(err, ErrPooledClientState) {
		t.Fatal("repeated commit", err)
	}
	for i, method := range []string{"init", "action", "end"} {
		result, err := slots[0].Invoke(context.Background(), method, map[string]string{"parameter": "value"}, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if result.Sequence != uint64(i+1) || result.Method != method || !strings.Contains(result.Events, method) {
			t.Fatal("wrong result", result)
		}
	}
	if _, err := slots[0].Invoke(context.Background(), "action", nil, time.Second); !errors.Is(err, ErrPooledClientState) {
		t.Fatal("ended slot reused", err)
	}
	if err := slots[0].Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := slots[0].Close(context.Background()); err != nil {
		t.Fatal("idempotent closure", err)
	}
	if _, err := slots[1].Invoke(context.Background(), "init", nil, time.Second); err != nil {
		t.Fatal("peer lost", err)
	}
	if err := f.run.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := slots[1].Invoke(context.Background(), "action", nil, time.Second); !errors.Is(err, ErrPooledClientClosed) {
		t.Fatal("closed run reused", err)
	}
}

func TestPooledClientRunValidatesBeforeReservation(t *testing.T) {
	f := newManagedClientFixture(t)
	for _, users := range [][]PooledSlotContext{nil, {{VUserID: -1}}, {{VUserID: 1}, {VUserID: 1}}, {{SlotID: "caller-slot", VUserID: 1}}} {
		if _, err := f.run.Prepare(context.Background(), users, time.Second); !errors.Is(err, ErrPooledClientState) {
			t.Fatal(err)
		}
	}
	if _, err := f.run.Prepare(context.Background(), []PooledSlotContext{{VUserID: 1}}, 0); !errors.Is(err, ErrPooledClientState) {
		t.Fatal(err)
	}
	slots := prepareManaged(t, f)
	if _, err := f.run.Prepare(context.Background(), []PooledSlotContext{{VUserID: 3}}, time.Second); !errors.Is(err, ErrPooledClientState) {
		t.Fatal("second reservation allowed", err)
	}
	if err := f.run.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"action", "end", "full_lifecycle"} {
		if _, err := slots[0].Invoke(context.Background(), method, nil, time.Second); !errors.Is(err, ErrPooledClientState) {
			t.Fatal(err)
		}
	}
	if _, err := slots[0].Invoke(context.Background(), "init", map[string]string{"": "invalid"}, time.Second); !errors.Is(err, ErrPooledClientState) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := slots[0].Invoke(ctx, "init", nil, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	r, err := slots[0].Invoke(context.Background(), "init", nil, time.Second)
	if err != nil || r.Sequence != 1 {
		t.Fatal("local rejection consumed sequence", r, err)
	}
}

func TestPooledClientRunOpenFreezesOwnerAndDecision(t *testing.T) {
	s, c, _ := controlServerFixture(t, true, true, true)
	expected := clientDecision(s.pooledControlProfiles.profiles["pilot"])
	run, err := c.OpenPooledClientRun(context.Background(), PooledClientOptions{ExecutionID: "execution", GroupID: "group", Decision: expected})
	if err != nil {
		t.Fatal(err)
	}
	defer run.Close(context.Background())
	original, epoch, _, _ := run.client.ownerAndRuntimeLease()
	if _, err := c.ClaimOwner(context.Background(), "replacement"); err != nil {
		t.Fatal(err)
	}
	frozen, frozenEpoch, _, _ := run.client.ownerAndRuntimeLease()
	if frozen != original || frozenEpoch != epoch {
		t.Fatal("owner snapshot changed")
	}
	if _, err := run.Prepare(context.Background(), []PooledSlotContext{{VUserID: 1}}, time.Second); err == nil {
		t.Fatal("old owner admitted")
	}
	if run.Err() == nil {
		t.Fatal("fenced run remained usable")
	}
}

func TestPooledClientRunRejectsChangedDecision(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*PooledControlDecision)
	}{
		{"capacity", func(d *PooledControlDecision) { d.MaxHosts++ }},
		{"script_approval", func(d *PooledControlDecision) { d.ScriptSHA256 = strings.Repeat("f", 64) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, c, _ := controlServerFixture(t, true, true, true)
			expected := clientDecision(s.pooledControlProfiles.profiles["pilot"])
			tc.mutate(&expected)
			if _, err := c.OpenPooledClientRun(context.Background(), PooledClientOptions{ExecutionID: "execution", GroupID: "group", Decision: expected}); !errors.Is(err, ErrPooledClientDecision) {
				t.Fatal(err)
			}
		})
	}
}

func TestPooledClientRunLostInvocationNeverReplays(t *testing.T) {
	f := newManagedClientFixture(t)
	slots := prepareManaged(t, f)
	if err := f.run.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	original := f.run.client
	var invocations atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q PooledControlRequest
		if !decodeRequest(w, r, &q) {
			return
		}
		response, err := original.PooledControl(r.Context(), q)
		if q.Operation == "invoke" {
			invocations.Add(1)
			w.Write([]byte("invalid response after invocation"))
			return
		}
		if err != nil {
			writePooledControlError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, response)
	}))
	defer proxy.Close()
	c, err := NewClient(proxy.URL, proxy.Client())
	if err != nil {
		t.Fatal(err)
	}
	c.ownerID, c.ownerEpoch, _, _ = original.ownerAndRuntimeLease()
	// Replace before any renewal tick; use a distinct handle to avoid mutation.
	run, err := newPooledClientRun(context.Background(), c, f.run.decision, PooledControlResponse{RunID: f.run.runID, RunToken: f.run.token, Decision: f.run.decision})
	if err != nil {
		t.Fatal(err)
	}
	defer run.Close(context.Background())
	run.committed = true
	slot := &PooledClientSlot{run: run, hostID: slots[0].hostID, slotID: slots[0].slotID, reservation: slots[0].reservation}
	if _, err := slot.Invoke(context.Background(), "init", nil, time.Second); err == nil {
		t.Fatal("lost response accepted")
	}
	if _, err := slot.Invoke(context.Background(), "init", nil, time.Second); !errors.Is(err, ErrPooledClientState) {
		t.Fatal("retry allowed", err)
	}
	if invocations.Load() != 1 || f.calls.Load() != 1 {
		t.Fatal("invocation replayed")
	}
	if err := slot.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestPooledClientRunCloseInterruptsBlockedInvocation(t *testing.T) {
	f := newManagedClientFixture(t)
	started := make(chan struct{})
	f.host.invocation = func(ctx context.Context, _, _ string, _ map[string]string) (javarunner.PooledFrame, bool, error) {
		close(started)
		<-ctx.Done()
		return javarunner.PooledFrame{}, false, ctx.Err()
	}
	slots := prepareManaged(t, f)
	if err := f.run.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	invoked := make(chan error, 1)
	go func() { _, err := slots[0].Invoke(context.Background(), "init", nil, 20*time.Second); invoked <- err }()
	<-started
	if _, err := slots[0].Invoke(context.Background(), "init", nil, time.Second); !errors.Is(err, errPooledSlotBusy) {
		t.Fatal("busy invocation queued", err)
	}
	if err := f.run.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-invoked:
		if err == nil {
			t.Fatal("interrupted work succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("close waited for invocation")
	}
}

func TestPooledClientRunRenewsWhileInvocationBlocked(t *testing.T) {
	f := newManagedClientFixture(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	original := f.host.invocation
	f.host.invocation = func(ctx context.Context, slot, method string, params map[string]string) (javarunner.PooledFrame, bool, error) {
		close(entered)
		select {
		case <-release:
			return original(ctx, slot, method, params)
		case <-ctx.Done():
			return javarunner.PooledFrame{}, false, ctx.Err()
		}
	}
	slots := prepareManaged(t, f)
	if err := f.run.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, err := slots[0].Invoke(context.Background(), "init", nil, 3*RuntimeLeaseTTL); result <- err }()
	<-entered
	// The generator's owner heartbeat is separate from per-group renewal.
	// Without it, the owner must expire even while this run renews successfully.
	ownerTicker := time.NewTicker(time.Second)
	defer ownerTicker.Stop()
	timer := time.NewTimer(RuntimeLeaseTTL + time.Second)
	defer timer.Stop()
	waiting := true
	for waiting {
		select {
		case err := <-result:
			t.Fatal("invocation ended before renewal test", err)
		case <-ownerTicker.C:
			if _, err := f.client.ClaimOwner(context.Background(), "owner"); err != nil {
				t.Fatal(err)
			}
		case <-timer.C:
			waiting = false
		}
	}
	if f.run.Err() != nil {
		t.Fatal("lease lost behind invocation", f.run.Err())
	}
	once.Do(func() { close(release) })
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func scriptedManagedClient(t *testing.T, lifetime context.Context, serve func(http.ResponseWriter, *http.Request, PooledControlRequest, PooledControlResponse)) *PooledClientRun {
	t.Helper()
	d := PooledControlDecision{TargetClass: "Actions", InvocationEnabled: true, PreparationEnabled: true, CommitEnabled: true, Mode: "pooled_pilot", ProfileID: "pilot", ResourceProfile: "resource", ConfigurationSHA256: strings.Repeat("a", 64), ContainmentBackend: "delegated_linux", SlotsPerHost: 2, HostMemoryBytes: 1024, MaxHosts: 2, MaxVUsers: 4, MaxMemoryBytes: 2048}
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q PooledControlRequest
		if !decodeRequest(w, r, &q) {
			return
		}
		response := PooledControlResponse{Contract: ContractVersion, PooledContract: PooledControlContract, Operation: q.Operation, RequestID: q.RequestID, RunID: "run", LeaseTTLMS: RuntimeLeaseTTL.Milliseconds(), Decision: d}
		if q.Operation == "claim" {
			response.RunToken = strings.Repeat("b", 64)
			writeJSON(w, http.StatusOK, response)
			return
		}
		serve(w, r, q, response)
	}))
	t.Cleanup(h.Close)
	c, err := NewClient(h.URL, h.Client())
	if err != nil {
		t.Fatal(err)
	}
	c.ownerID, c.ownerEpoch = "owner", "epoch"
	run, err := c.OpenPooledClientRun(lifetime, PooledClientOptions{ExecutionID: "execution", GroupID: "group", Decision: d})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Close(context.Background()) })
	return run
}

func TestPooledClientRunLeaseFailureFencesAndRetainsCleanupFailure(t *testing.T) {
	var renewals, closures atomic.Int32
	run := scriptedManagedClient(t, context.Background(), func(w http.ResponseWriter, _ *http.Request, q PooledControlRequest, r PooledControlResponse) {
		switch q.Operation {
		case "renew":
			renewals.Add(1)
			writeError(w, http.StatusConflict, "pooled_run_fenced")
		case "close":
			closures.Add(1)
			writeJSON(w, http.StatusOK, r) // no positive reaping receipt
		default:
			t.Errorf("unexpected operation %s", q.Operation)
			writeError(w, http.StatusConflict, "pooled_run_fenced")
		}
	})
	select {
	case <-run.Done():
	case <-time.After(4 * time.Second):
		t.Fatal("renewal failure did not fence run")
	}
	var remote *RemoteError
	if !errors.As(run.Err(), &remote) || remote.Code != "pooled_run_fenced" {
		t.Fatal("lost triggering failure", run.Err())
	}
	if err := run.Close(context.Background()); err == nil || !PooledControlOutcomeAmbiguous(err) {
		t.Fatal("uncertain cleanup reported success", err)
	}
	if renewals.Load() != 1 || closures.Load() != 1 {
		t.Fatal("renewal retried or duplicate close", renewals.Load(), closures.Load())
	}
	if !errors.As(run.Err(), &remote) || remote.Code != "pooled_run_fenced" {
		t.Fatal("cleanup replaced original failure")
	}
}

func TestPooledClientRunLifetimeCancellationAndCloseWait(t *testing.T) {
	lifetime, cancel := context.WithCancelCause(context.Background())
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	run := scriptedManagedClient(t, lifetime, func(w http.ResponseWriter, r *http.Request, q PooledControlRequest, response PooledControlResponse) {
		if q.Operation != "close" {
			t.Errorf("unexpected %s", q.Operation)
			return
		}
		close(started)
		select {
		case <-release:
			response.Reaped = true
			writeJSON(w, http.StatusOK, response)
		case <-r.Context().Done():
		}
	})
	cause := errors.New("test owner stopped")
	cancel(cause)
	<-started
	ctx, stop := context.WithCancel(context.Background())
	stop()
	if err := run.Close(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled wait reported cleanup", err)
	}
	if !errors.Is(run.Err(), cause) {
		t.Fatal("lost lifetime cause")
	}
	once.Do(func() { close(release) })
	if err := run.Close(context.Background()); err != nil {
		t.Fatal("could not wait for cleanup again", err)
	}
}

func TestPooledClientRunRejectsDecisionDriftOnRenewalAndCleanup(t *testing.T) {
	for _, op := range []string{"renew", "close"} {
		t.Run(op, func(t *testing.T) {
			run := scriptedManagedClient(t, context.Background(), func(w http.ResponseWriter, _ *http.Request, q PooledControlRequest, r PooledControlResponse) {
				if q.Operation == op {
					r.Decision.ResourceProfile = "changed"
				}
				r.Reaped = q.Operation == "close"
				writeJSON(w, http.StatusOK, r)
			})
			if op == "renew" {
				select {
				case <-run.Done():
				case <-time.After(4 * time.Second):
					t.Fatal("decision drift accepted")
				}
				if !errors.Is(run.Err(), ErrPooledClientDecision) {
					t.Fatal(run.Err())
				}
			} else if err := run.Close(context.Background()); !errors.Is(err, ErrPooledClientDecision) {
				t.Fatal("changed cleanup decision accepted", err)
			}
		})
	}
}

func TestPooledClientRunPreparationTimeoutNeverReleasesInitialization(t *testing.T) {
	f := newManagedClientFixture(t)
	f.host.ready = func(ctx context.Context, _ map[string]map[string]string) error { <-ctx.Done(); return ctx.Err() }
	if _, err := f.run.Prepare(context.Background(), []PooledSlotContext{{VUserID: 1}}, 50*time.Millisecond); err == nil {
		t.Fatal("preparation timeout accepted")
	}
	if err := f.run.Commit(context.Background()); err == nil {
		t.Fatal("commit after failed preparation")
	}
	if f.calls.Load() != 0 || f.host.commits.Load() != 0 {
		t.Fatal("user work after expiry")
	}
	if err := f.run.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestPooledClientRunCloseDoesNotWaitBehindPreparation(t *testing.T) {
	f := newManagedClientFixture(t)
	started := make(chan struct{})
	f.host.ready = func(ctx context.Context, _ map[string]map[string]string) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	prepared := make(chan error, 1)
	go func() {
		_, err := f.run.Prepare(context.Background(), []PooledSlotContext{{VUserID: 1}}, time.Minute)
		prepared <- err
	}()
	<-started
	if err := f.run.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-prepared:
		if err == nil {
			t.Fatal("aborted preparation succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("prepare blocked closure")
	}
}

func TestPooledClientRunLostCommitRetiresRun(t *testing.T) {
	var commits atomic.Int32
	deadline := time.Now().Add(time.Minute).UnixMilli()
	run := scriptedManagedClient(t, context.Background(), func(w http.ResponseWriter, _ *http.Request, q PooledControlRequest, r PooledControlResponse) {
		switch q.Operation {
		case "reserve":
			r.ReservationID = "reservation"
			r.PrepareDeadlineUnixMS = deadline
			r.Placements = []PooledPlacement{{HostID: "host", SlotIDs: []string{"slot"}, MemoryBytes: 1024}}
		case "prepare":
			r.ReservationID = q.ReservationID
			r.Ready = true
			r.PrepareDeadlineUnixMS = deadline
		case "commit":
			commits.Add(1)
			w.Write([]byte("lost commit response"))
			return
		case "close":
			r.Reaped = true
		}
		writeJSON(w, http.StatusOK, r)
	})
	slots, err := run.Prepare(context.Background(), []PooledSlotContext{{VUserID: 1}}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := run.Commit(context.Background()); err == nil {
		t.Fatal("ambiguous commit accepted")
	}
	if err := run.Commit(context.Background()); err == nil {
		t.Fatal("commit replayed")
	}
	if _, err := slots[0].Invoke(context.Background(), "init", nil, time.Second); err == nil {
		t.Fatal("initialization after ambiguous commit")
	}
	if commits.Load() != 1 {
		t.Fatal("commit replayed", commits.Load())
	}
}

func TestPooledClientSlotCloseBypassesBusyInvocationAndPreservesPeer(t *testing.T) {
	f := newManagedClientFixture(t)
	slots := prepareManaged(t, f)
	if err := f.run.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	started, closed := make(chan struct{}), make(chan struct{})
	original := f.host.invocation
	f.host.invocation = func(ctx context.Context, slot, method string, params map[string]string) (javarunner.PooledFrame, bool, error) {
		if slot != slots[0].slotID {
			return original(ctx, slot, method, params)
		}
		close(started)
		select {
		case <-closed:
			return javarunner.PooledFrame{}, true, errors.New("slot_closed")
		case <-ctx.Done():
			return javarunner.PooledFrame{}, false, ctx.Err()
		}
	}
	f.host.closeSlot = func(_ context.Context, slot string) error {
		if slot == slots[0].slotID {
			close(closed)
		}
		return nil
	}
	result := make(chan error, 1)
	go func() { _, err := slots[0].Invoke(context.Background(), "init", nil, time.Minute); result <- err }()
	<-started
	if err := slots[0].Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("closed invocation succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("slot close queued behind invocation")
	}
	if _, err := slots[0].Invoke(context.Background(), "init", nil, time.Second); !errors.Is(err, ErrPooledClientState) {
		t.Fatal("closed slot reused", err)
	}
	if _, err := slots[1].Invoke(context.Background(), "init", nil, time.Second); err != nil {
		t.Fatal("cooperative close reaped peer", err)
	}
}

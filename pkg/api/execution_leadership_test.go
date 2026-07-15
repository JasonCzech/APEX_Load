package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

// leaseResult is a single programmed response from fakeLeaseStore.
type leaseResult struct {
	acquired bool
	err      error
}

type fakeLeaseStore struct {
	mu       sync.Mutex
	calls    int
	results  []leaseResult
	dfltOK   bool
	dfltErr  error
	released chan struct{}
}

func (f *fakeLeaseStore) ReleaseSchedulerLease(string, string) error {
	if f.released != nil {
		close(f.released)
	}
	return nil
}

func TestExecutionLeadershipRelinquishWaitsForLossCleanup(t *testing.T) {
	store := &fakeLeaseStore{dfltOK: true, released: make(chan struct{})}
	l := newExecutionLeadership(store, "owner-release-order")
	l.tick()
	lossStarted := make(chan struct{})
	allowLoss := make(chan struct{})
	l.setOnLoss(func() {
		close(lossStarted)
		<-allowLoss
	})
	done := make(chan struct{})
	go func() {
		l.relinquish()
		close(done)
	}()
	select {
	case <-lossStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("loss cleanup did not start")
	}
	select {
	case <-store.released:
		t.Fatal("durable lease released before loss cleanup completed")
	case <-time.After(100 * time.Millisecond):
	}
	close(allowLoss)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("relinquish did not finish after cleanup")
	}
}

func TestExecutionLeadershipBootstrapOperationContextReplacesCanceledEpoch(t *testing.T) {
	l := newExecutionLeadership(&fakeLeaseStore{}, "owner-bootstrap")
	l.leader.Store(true)
	ctx, cancel := l.operationContext(context.Background())
	defer cancel()
	if err := ctx.Err(); err != nil {
		t.Fatalf("bootstrap operation context is canceled: %v", err)
	}
}

func (f *fakeLeaseStore) TryAcquireSchedulerLease(name string, ownerID string, now time.Time, ttl time.Duration) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if len(f.results) > 0 {
		r := f.results[0]
		f.results = f.results[1:]
		return r.acquired, r.err
	}
	return f.dfltOK, f.dfltErr
}

func TestExecutionLeadershipNilReceiverIsAlwaysLeader(t *testing.T) {
	var l *executionLeadership
	if !l.IsLeader() {
		t.Fatal("nil leadership must report leader (single-node default)")
	}
	// Must not panic.
	l.tick()
	l.setOnAcquire(func() {})
	l.setOnLoss(func() {})
	l.relinquish()
}

func TestExecutionLeadershipAcquiresAndFiresOnAcquireOnce(t *testing.T) {
	store := &fakeLeaseStore{dfltOK: true}
	l := newExecutionLeadership(store, "owner-a")
	// onAcquire now runs asynchronously off the renewal goroutine, so the test
	// synchronizes on a channel rather than reading a shared counter.
	fired := make(chan struct{}, 8)
	l.setOnAcquire(func() { fired <- struct{}{} })

	l.tick()
	if !l.IsLeader() {
		t.Fatal("expected leader after acquiring lease")
	}
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("onAcquire did not fire after acquiring the lease")
	}

	// Renewing the held lease must not re-fire onAcquire.
	l.tick()
	l.tick()
	select {
	case <-fired:
		t.Fatal("onAcquire re-fired on renewal")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestExecutionLeadershipNotAcquiredStaysFollower(t *testing.T) {
	store := &fakeLeaseStore{dfltOK: false}
	l := newExecutionLeadership(store, "owner-b")
	var acquires int
	l.setOnAcquire(func() { acquires++ })

	l.tick()
	if l.IsLeader() {
		t.Fatal("must not be leader when lease is held elsewhere")
	}
	if acquires != 0 {
		t.Fatalf("onAcquire must not fire for a follower, calls = %d", acquires)
	}
}

func TestExecutionLeadershipFailoverFiresOnAcquire(t *testing.T) {
	// Follower for two ticks, then the prior leader's lease expires and we win.
	store := &fakeLeaseStore{results: []leaseResult{
		{acquired: false},
		{acquired: false},
		{acquired: true},
	}, dfltOK: true}
	l := newExecutionLeadership(store, "owner-c")
	fired := make(chan struct{}, 8)
	l.setOnAcquire(func() { fired <- struct{}{} })

	l.tick()
	l.tick()
	if l.IsLeader() {
		t.Fatalf("expected follower before takeover (leader=%v)", l.IsLeader())
	}
	select {
	case <-fired:
		t.Fatal("onAcquire fired while still a follower")
	case <-time.After(100 * time.Millisecond):
	}

	l.tick()
	if !l.IsLeader() {
		t.Fatal("expected leadership after failover takeover")
	}
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("onAcquire did not fire after failover takeover")
	}
}

func TestExecutionLeadershipRelinquishesWhenLeaseLost(t *testing.T) {
	store := &fakeLeaseStore{results: []leaseResult{
		{acquired: true},
		{acquired: false},
	}}
	l := newExecutionLeadership(store, "owner-d")
	lost := make(chan struct{}, 1)
	l.setOnLoss(func() { lost <- struct{}{} })

	l.tick()
	if !l.IsLeader() {
		t.Fatal("expected leader on first tick")
	}
	l.tick()
	if l.IsLeader() {
		t.Fatal("expected to relinquish leadership when lease no longer held")
	}
	select {
	case <-lost:
	case <-time.After(2 * time.Second):
		t.Fatal("onLoss did not fire after lease loss")
	}
}

func TestExecutionLeadershipLossCompletesBeforeReacquireReconcile(t *testing.T) {
	store := &fakeLeaseStore{results: []leaseResult{
		{acquired: true},
		{acquired: false},
		{acquired: true},
	}}
	l := newExecutionLeadership(store, "owner-ordered-callbacks")
	events := make(chan string, 8)
	releaseLoss := make(chan struct{})
	l.setOnAcquire(func() {
		if !l.IsLeader() {
			events <- "acquire-not-admitted"
			return
		}
		events <- "acquire"
	})
	l.setOnLoss(func() {
		events <- "loss-start"
		<-releaseLoss
		events <- "loss-end"
	})

	waitEvent := func(want string) {
		t.Helper()
		select {
		case got := <-events:
			if got != want {
				t.Fatalf("callback event = %q, want %q", got, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for callback event %q", want)
		}
	}

	l.tick()
	waitEvent("acquire")
	l.tick()
	waitEvent("loss-start")

	// Reacquire while the prior generation's loss cleanup is still blocked.
	// The internal lease must renew, but public leadership and reconciliation
	// for the new generation must not overtake that cleanup.
	l.tick()
	if !l.leader.Load() {
		t.Fatal("expected the internal lease to be reacquired")
	}
	if l.IsLeader() {
		t.Fatal("reacquired lease must not admit work while prior loss cleanup is pending")
	}
	select {
	case got := <-events:
		t.Fatalf("callback %q overtook blocked loss cleanup", got)
	case <-time.After(100 * time.Millisecond):
	}

	close(releaseLoss)
	waitEvent("loss-end")
	waitEvent("acquire")
	if !l.IsLeader() {
		t.Fatal("leadership admission did not open after loss cleanup and reconciliation")
	}
}

func TestExecutionLeadershipNilLossCallbackStillDrainsAdmissionGate(t *testing.T) {
	store := &fakeLeaseStore{results: []leaseResult{
		{acquired: true},
		{acquired: false},
		{acquired: true},
	}}
	l := newExecutionLeadership(store, "owner-nil-loss-callback")
	initialAcquireStarted := make(chan struct{})
	releaseInitialAcquire := make(chan struct{})
	reacquired := make(chan bool, 1)
	var acquireCalls atomic.Int32
	l.setOnAcquire(func() {
		if acquireCalls.Add(1) == 1 {
			close(initialAcquireStarted)
			<-releaseInitialAcquire
			return
		}
		reacquired <- l.IsLeader()
	})
	// Deliberately leave onLoss nil. The loss event must still drain and release
	// admission after it reaches the ordered callback worker.

	l.tick()
	select {
	case <-initialAcquireStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("initial acquire callback did not start")
	}
	l.tick()
	l.tick()
	if !l.leader.Load() {
		t.Fatal("expected the internal lease to be reacquired")
	}
	if l.IsLeader() {
		t.Fatal("nil loss callback must still keep admission closed until its queued event drains")
	}

	close(releaseInitialAcquire)
	select {
	case admitted := <-reacquired:
		if !admitted {
			t.Fatal("reacquire reconciliation could not observe leadership after nil loss callback drained")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reacquire reconciliation did not run after nil loss callback drained")
	}
	if !l.IsLeader() {
		t.Fatal("leadership admission did not open after nil loss callback drained")
	}
}

func TestExecutionLeadershipStepsDownOnlyAfterTTLOfErrors(t *testing.T) {
	base := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	current := base
	store := &fakeLeaseStore{results: []leaseResult{
		{acquired: true},             // t0: become leader
		{err: errors.New("db blip")}, // t0+10s: transient error, still within TTL
		{err: errors.New("db down")}, // t0+31s: error past TTL -> step down
	}}
	l := newExecutionLeadership(store, "owner-e")
	l.now = func() time.Time { return current }

	l.tick() // acquire
	if !l.IsLeader() {
		t.Fatal("expected leader after acquire")
	}

	current = base.Add(10 * time.Second)
	l.tick()
	if !l.IsLeader() {
		t.Fatal("must retain leadership through a transient error within the TTL window")
	}

	current = base.Add(31 * time.Second)
	l.tick()
	if l.IsLeader() {
		t.Fatal("must step down once renewal has failed for longer than the TTL")
	}
}

func TestExecutionLeadershipStaleReadTriggersLossCleanup(t *testing.T) {
	base := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	current := base
	l := newExecutionLeadership(&fakeLeaseStore{results: []leaseResult{{acquired: true}}}, "owner-stale-read")
	l.now = func() time.Time { return current }
	lost := make(chan struct{}, 1)
	l.setOnLoss(func() { lost <- struct{}{} })
	l.tick()
	if !l.IsLeader() {
		t.Fatal("expected leader after acquisition")
	}

	// Simulate a wedged renewal goroutine: no tick occurs after the lease TTL.
	current = base.Add(l.ttl)
	if l.IsLeader() {
		t.Fatal("stale lease must fail closed")
	}
	if l.leader.Load() {
		t.Fatal("stale lease check must clear the internal leader flag")
	}
	select {
	case <-lost:
	case <-time.After(2 * time.Second):
		t.Fatal("stale lease check did not trigger loss cleanup")
	}
}

func TestExecutionLeadershipConcurrentRenewalWinsBeforeStaleDecision(t *testing.T) {
	base := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	store := &fakeLeaseStore{dfltOK: true}
	l := newExecutionLeadership(store, "owner-renewal-race")

	var nowCalls atomic.Int32
	staleCheckEntered := make(chan struct{})
	releaseStaleCheck := make(chan struct{})
	l.now = func() time.Time {
		switch nowCalls.Add(1) {
		case 1:
			return base
		case 2:
			close(staleCheckEntered)
			<-releaseStaleCheck
			return base.Add(l.ttl)
		default:
			return base.Add(l.ttl)
		}
	}
	lost := make(chan struct{}, 1)
	l.setOnLoss(func() { lost <- struct{}{} })

	l.tick()
	if !l.leader.Load() {
		t.Fatal("expected initial acquisition")
	}

	isLeaderResult := make(chan bool, 1)
	go func() { isLeaderResult <- l.IsLeader() }()
	select {
	case <-staleCheckEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("stale IsLeader check did not reach its barrier")
	}

	// Complete a renewal while IsLeader is paused. IsLeader must evaluate the
	// refreshed timestamp after the barrier, not act on the stale pre-renewal
	// snapshot it observed before calling now.
	renewed := make(chan struct{})
	go func() {
		l.tick()
		close(renewed)
	}()
	select {
	case <-renewed:
	case <-time.After(2 * time.Second):
		t.Fatal("renewal was blocked by the IsLeader freshness check")
	}
	close(releaseStaleCheck)

	select {
	case leader := <-isLeaderResult:
		if !leader {
			t.Fatal("stale IsLeader snapshot stepped down after a successful concurrent renewal")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("IsLeader did not finish after releasing its barrier")
	}
	if !l.leader.Load() {
		t.Fatal("concurrent renewal must preserve the internal leader flag")
	}
	select {
	case <-lost:
		t.Fatal("concurrent renewal must not trigger loss cleanup")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestExecutionLeadershipLeaseIsExclusiveOnRealStore(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	a := newExecutionLeadership(store, "replica-a")
	b := newExecutionLeadership(store, "replica-b")
	a.tick()
	b.tick()

	if !a.IsLeader() {
		t.Fatal("first replica should win the lease")
	}
	if b.IsLeader() {
		t.Fatal("second replica must not also hold the lease")
	}
}

// --- runner gating ---

func notLeaderRunner(t *testing.T) *TestRunner {
	t.Helper()
	// A leadership whose lease can never be acquired keeps the runner a
	// permanent follower (leader bit defaults false and never flips).
	follower := newExecutionLeadership(&fakeLeaseStore{dfltOK: false}, "follower")
	return NewTestRunner(10, NewHub(), nil, WithExecutionLeadership(follower))
}

func TestNonLeaderRejectsExecutionMutations(t *testing.T) {
	runner := notLeaderRunner(t)
	if runner.isExecutionLeader() {
		t.Fatal("runner should be a follower for this test")
	}

	if _, err := runner.CreateTest(CreateTestRequest{Name: "x"}); !errors.Is(err, errNotExecutionLeader) {
		t.Fatalf("CreateTest err = %v, want errNotExecutionLeader", err)
	}
	if err := runner.StartTest("missing"); !errors.Is(err, errNotExecutionLeader) {
		t.Fatalf("StartTest err = %v, want errNotExecutionLeader", err)
	}
	if err := runner.StopTest("missing"); !errors.Is(err, errNotExecutionLeader) {
		t.Fatalf("StopTest err = %v, want errNotExecutionLeader", err)
	}
	if err := runner.AbortTest("missing"); !errors.Is(err, errNotExecutionLeader) {
		t.Fatalf("AbortTest err = %v, want errNotExecutionLeader", err)
	}
	if err := runner.ScaleVUsers("missing", "g", 1); !errors.Is(err, errNotExecutionLeader) {
		t.Fatalf("ScaleVUsers err = %v, want errNotExecutionLeader", err)
	}
	if err := runner.AdjustDuration("missing", time.Minute); !errors.Is(err, errNotExecutionLeader) {
		t.Fatalf("AdjustDuration err = %v, want errNotExecutionLeader", err)
	}
	if err := runner.PauseTest("missing"); !errors.Is(err, errNotExecutionLeader) {
		t.Fatalf("PauseTest err = %v, want errNotExecutionLeader", err)
	}
	if err := runner.ResumeTest("missing"); !errors.Is(err, errNotExecutionLeader) {
		t.Fatalf("ResumeTest err = %v, want errNotExecutionLeader", err)
	}
	if _, err := runner.UpdateGoalTargetTPS("missing", "g", 5); !errors.Is(err, errNotExecutionLeader) {
		t.Fatalf("UpdateGoalTargetTPS err = %v, want errNotExecutionLeader", err)
	}
}

func TestNonLeaderDoesNotPromotePendingTests(t *testing.T) {
	runner := notLeaderRunner(t)
	pendingSince := time.Now().Add(-2 * time.Minute)
	pending := &ManagedTest{
		ID:           "pending-follower",
		Name:         "Pending",
		Status:       "PENDING",
		TotalVUsers:  1,
		PendingSince: &pendingSince,
	}
	runner.mu.Lock()
	runner.tests[pending.ID] = pending
	runner.mu.Unlock()

	runner.ReconcilePendingTests()
	runner.promotePendingTests()

	if got, _ := runner.GetTest(pending.ID); got.Status != "PENDING" {
		t.Fatalf("follower promoted a pending test to %s; want PENDING", got.Status)
	}
}

func TestBootClobberOfInterruptedRunsIsLeaderOnly(t *testing.T) {
	seed := func(t *testing.T) (storage.Store, string) {
		t.Helper()
		store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		rec := &storage.TestRecord{
			ID:          "interrupted-run",
			Name:        "Interrupted",
			Status:      "RUNNING",
			TotalVUsers: 1,
			Groups:      []TestGroup{{Name: "g1", Protocol: "http", VUsers: 1}},
		}
		if err := store.SaveTest(rec); err != nil {
			t.Fatalf("save test: %v", err)
		}
		return store, rec.ID
	}

	t.Run("leader clobbers to FAILED", func(t *testing.T) {
		store, id := seed(t)
		defer store.Close()
		runner := NewTestRunner(10, NewHub(), store) // default => always leader
		got, ok := runner.GetTest(id)
		if !ok {
			t.Fatal("expected loaded test")
		}
		if got.Status != "FAILED" {
			t.Fatalf("leader boot status = %s, want FAILED", got.Status)
		}
	})

	t.Run("follower leaves the run untouched", func(t *testing.T) {
		store, id := seed(t)
		defer store.Close()
		follower := newExecutionLeadership(&fakeLeaseStore{dfltOK: false}, "follower")
		runner := NewTestRunner(10, NewHub(), store, WithExecutionLeadership(follower))
		got, ok := runner.GetTest(id)
		if !ok {
			t.Fatal("expected loaded test")
		}
		if got.Status != "RUNNING" {
			t.Fatalf("follower boot status = %s, want RUNNING (no clobber)", got.Status)
		}
		// The shared DB row must also be untouched.
		stored, err := store.GetTest(id)
		if err != nil {
			t.Fatalf("get stored test: %v", err)
		}
		if stored.Status != "RUNNING" {
			t.Fatalf("follower mutated shared DB row to %s; want RUNNING", stored.Status)
		}
	})
}

func TestReloadAndReconcileOnLeadershipAcquire(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	// A queued test exists in the DB but the follower has not promoted it.
	pendingSince := time.Now().Add(-2 * time.Minute)
	rec := &storage.TestRecord{
		ID:           "queued-elsewhere",
		Name:         "Queued",
		Status:       "PENDING",
		TotalVUsers:  1,
		PendingSince: &pendingSince,
		Groups:       []TestGroup{{Name: "g1", ScriptID: "script-reload", ScriptName: "Reload", Protocol: "http", VUsers: 1}},
	}
	if err := store.SaveTest(rec); err != nil {
		t.Fatalf("save test: %v", err)
	}

	follower := newExecutionLeadership(&fakeLeaseStore{dfltOK: false}, "follower")
	runner := NewTestRunner(10, NewHub(), store, WithExecutionLeadership(follower))
	runner.StoreScript(&dsl.Script{
		ID:       "script-reload",
		Name:     "Reload",
		Protocol: "http",
		Version:  "1.0.0",
		Actions:  []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})

	// While a follower, reconcile is a no-op.
	runner.reloadAndReconcile()
	if got, _ := runner.GetTest(rec.ID); got.Status != "PENDING" {
		t.Fatalf("follower reload promoted test to %s; want PENDING", got.Status)
	}

	// Simulate acquiring leadership, then the acquire-time reconcile runs.
	follower.mu.Lock()
	follower.leader.Store(true)
	follower.mu.Unlock()
	runner.reloadAndReconcile()
	promoted := waitForStatusAny(t, runner, rec.ID, 5*time.Second, "INITIALIZING", "RUNNING", "COMPLETE")
	if promoted.Status == "PENDING" {
		t.Fatalf("new leader did not promote the queued test on acquire")
	}
}

func TestExecutionMutationHandlersReturn503OnFollower(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	// Leader election is enabled but the renewal loop is never started
	// (RunExecutionLeadership), so this Server stays a follower.
	s := NewServer(ServerConfig{Store: store, HAExecutionLeadership: true})
	if s.isExecutionLeader() {
		t.Fatal("server should be a follower until it acquires the lease")
	}

	cases := []struct {
		name    string
		method  string
		body    string
		handler http.HandlerFunc
	}{
		{"create", http.MethodPost, `{"name":"x"}`, s.handleCreateTest},
		{"start", http.MethodPost, ``, s.handleStartTest},
		{"stop", http.MethodPost, ``, s.handleStopTest},
		{"abort", http.MethodPost, ``, s.handleAbortTest},
		{"pause", http.MethodPost, ``, s.handlePauseTest},
		{"resume", http.MethodPost, ``, s.handleResumeTest},
		{"scale", http.MethodPost, `{"group":"g","target_vusers":1}`, s.handleScaleVUsers},
		{"goal", http.MethodPatch, `{"group":"g","target_tps":5}`, s.handleUpdateGoalTarget},
		{"duration", http.MethodPatch, `{"duration":"5m"}`, s.handleAdjustDuration},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, "/api/v1/tests/test-1", strings.NewReader(tc.body))
			req.SetPathValue("id", "test-1")
			tc.handler(rec, req)
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("%s status = %d, want 503", tc.name, rec.Code)
			}
		})
	}
}

func TestFleetMutationHandlersReturn503OnFollower(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	s := NewServer(ServerConfig{Store: store, HAExecutionLeadership: true})
	if s.isExecutionLeader() {
		t.Fatal("server should be a follower until it acquires the lease")
	}

	cases := []struct {
		name    string
		method  string
		path    string
		body    string
		id      string
		handler http.HandlerFunc
	}{
		{"pool create", http.MethodPost, "/api/v1/fleet/pools", `{"name":"pool","cloud_provider":"kubernetes"}`, "", s.handleCreateFleetPool},
		{"pool update", http.MethodPut, "/api/v1/fleet/pools/pool-1", `{"name":"pool","cloud_provider":"kubernetes"}`, "pool-1", s.handleUpdateFleetPool},
		{"pool delete", http.MethodDelete, "/api/v1/fleet/pools/pool-1", ``, "pool-1", s.handleDeleteFleetPool},
		{"metadata patch", http.MethodPatch, "/api/v1/fleet/generators/gen-1", `{"cordoned":true}`, "gen-1", s.handlePatchGeneratorMetadata},
		{"provision create", http.MethodPost, "/api/v1/fleet/provision-requests", `{"fleet_pool_id":"pool-1","desired_count":2}`, "", s.handleCreateProvisioningRequest},
		{"provision cancel", http.MethodPost, "/api/v1/fleet/provision-requests/req-1/cancel", ``, "req-1", s.handleCancelProvisioningRequest},
		{"provision retry", http.MethodPost, "/api/v1/fleet/provision-requests/req-1/retry", ``, "req-1", s.handleRetryProvisioningRequest},
		{"alert ack", http.MethodPatch, "/api/v1/fleet/alerts/alert-1/ack", `{"note":"seen"}`, "alert-1", s.handleAckFleetAlert},
		{"generator cordon", http.MethodPost, "/api/v1/fleet/generators/gen-1/cordon", ``, "gen-1", s.handleCordonGenerator},
		{"generator uncordon", http.MethodPost, "/api/v1/fleet/generators/gen-1/uncordon", ``, "gen-1", s.handleUncordonGenerator},
		{"generator drain", http.MethodPost, "/api/v1/fleet/generators/gen-1/drain", ``, "gen-1", s.handleDrainGenerator},
		{"generator evacuate", http.MethodPost, "/api/v1/fleet/generators/gen-1/evacuate", `{"dry_run":true}`, "gen-1", s.handleEvacuateGenerator},
		{"drain", http.MethodPatch, "/api/v1/system/generators/drain", `{"target":"gen-a:9090","drain_state":"cordoned"}`, "", s.handleSetGeneratorDrainState},
		{"restart", http.MethodPost, "/api/v1/system/restarts", `{}`, "", s.handleSystemRestart},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			if tc.id != "" {
				req.SetPathValue("id", tc.id)
			}
			tc.handler(rec, req)
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("%s status = %d, want 503", tc.name, rec.Code)
			}
		})
	}
}

func TestMaterializeAndRunExecutionPlanReturns503OnFollower(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	// HA election is enabled but the renewal loop is never started, so the server
	// stays a follower. Running an execution plan must 503 (like every other
	// execution-mutating handler) rather than persisting a junk plan-run.
	s := NewServer(ServerConfig{Store: store, HAExecutionLeadership: true})
	if s.isExecutionLeader() {
		t.Fatal("server should be a follower until it acquires the lease")
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/execution-plans/plan-1/runs", strings.NewReader(`{}`))
	req.SetPathValue("id", "plan-1")
	s.handleMaterializeAndRunExecutionPlan(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("materialize-and-run on follower status = %d, want 503", rec.Code)
	}
}

func TestExecutionMutationHandlersServeNormallyWhenSingleActive(t *testing.T) {
	// No HA flag => no leadership coordinator => always leader => not a 503.
	s := NewServer(ServerConfig{})
	if !s.isExecutionLeader() {
		t.Fatal("single-active server must always be the execution leader")
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tests/test-1", nil)
	req.SetPathValue("id", "test-1")
	s.handleStartTest(rec, req)
	if rec.Code == http.StatusServiceUnavailable {
		t.Fatal("single-active server must not return 503 for execution endpoints")
	}
}

// TestExecutionLeadershipReconcileCoalesces guards B9: triggers that arrive while
// a reconcile is in flight must be COALESCED into a single re-run, not dropped.
// Under the old drop-on-flap behavior the mid-flight triggers were lost and the
// second run would never start (this test would then time out).
func TestExecutionLeadershipReconcileCoalesces(t *testing.T) {
	l := newExecutionLeadership(&fakeLeaseStore{dfltOK: true}, "owner")

	var mu sync.Mutex
	runs := 0
	started := make(chan struct{}, 8)
	release := make(chan struct{})
	onAcquire := func() {
		mu.Lock()
		runs++
		mu.Unlock()
		started <- struct{}{}
		<-release
	}

	waitStarted := func(what string) {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for %s (reconcile coalescing dropped a request?)", what)
		}
	}

	// First trigger starts a worker; it blocks inside onAcquire.
	l.triggerReconcile(onAcquire)
	waitStarted("initial reconcile")

	// Three triggers while in flight must coalesce into exactly one re-run.
	l.triggerReconcile(onAcquire)
	l.triggerReconcile(onAcquire)
	l.triggerReconcile(onAcquire)

	release <- struct{}{} // finish run 1
	waitStarted("coalesced reconcile")
	release <- struct{}{} // finish run 2 (the worker should now drain and exit)

	// A trigger after the worker has drained starts a fresh run.
	l.triggerReconcile(onAcquire)
	waitStarted("post-drain reconcile")
	release <- struct{}{} // finish run 3

	// Settle, then assert exactly three runs: 1 initial + 1 coalesced (not 3) + 1 fresh.
	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		got := runs
		mu.Unlock()
		if got == 3 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("reconcile runs = %d, want 3 (3 mid-flight triggers must coalesce into 1)", got)
		case <-time.After(10 * time.Millisecond):
		}
	}
	// Confirm no spurious extra run is pending.
	select {
	case <-started:
		t.Fatal("an extra reconcile ran; mid-flight triggers did not coalesce")
	case <-time.After(100 * time.Millisecond):
	}
}

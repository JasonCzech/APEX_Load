package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
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
	mu      sync.Mutex
	calls   int
	results []leaseResult
	dfltOK  bool
	dfltErr error
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

	l.tick()
	if !l.IsLeader() {
		t.Fatal("expected leader on first tick")
	}
	l.tick()
	if l.IsLeader() {
		t.Fatal("expected to relinquish leadership when lease no longer held")
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
	follower.leader.Store(true)
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

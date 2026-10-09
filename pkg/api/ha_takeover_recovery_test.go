package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/compute"
	"github.com/jasonczech/apexload/pkg/storage"
)

// perPoolCapacityResolver gives each fleet pool its own fake provider so one
// pool's provider can fail while another's works.
type perPoolCapacityResolver map[string]*fakeCapacityBackend

func (r perPoolCapacityResolver) BackendForPool(_ context.Context, pool *storage.FleetPool) (compute.Backend, *storage.ExecutionTarget, error) {
	backend, ok := r[pool.ID]
	if !ok {
		return nil, nil, errCapacityBackendUnavailable
	}
	return backend, &storage.ExecutionTarget{ID: pool.ExecutionTargetID, Kind: storage.ExecutionTargetAWSECSFargate}, nil
}

func (b *fakeCapacityBackend) setDiscoverErr(err error) {
	b.mu.Lock()
	b.discoverErr = err
	b.mu.Unlock()
}

func (b *fakeCapacityBackend) discoverCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.discoverCalls
}

func saveSweepPools(t *testing.T, store storage.Store) (*fakeCapacityBackend, *fakeCapacityBackend, perPoolCapacityResolver) {
	t.Helper()
	saveCapacityTestPool(t, store, &storage.FleetPool{ID: "pool-up", Name: "up", Status: "active", ExecutionTargetID: "target-up", LifecycleMode: storage.FleetLifecycleWarmBurst})
	saveCapacityTestPool(t, store, &storage.FleetPool{ID: "pool-down", Name: "down", Status: "active", ExecutionTargetID: "target-down", LifecycleMode: storage.FleetLifecycleWarmBurst})
	up := &fakeCapacityBackend{}
	down := &fakeCapacityBackend{discoverErr: errors.New("provider API unavailable")}
	return up, down, perPoolCapacityResolver{"pool-up": up, "pool-down": down}
}

// One unreachable provider used to fail the whole post-takeover sweep, which
// kept the new leader's admission closed. Now only its own pool waits.
func TestTakeoverSweepIsolatesFailingProviderPool(t *testing.T) {
	server, store := newTimeslotServer(t)
	up, down, resolver := saveSweepPools(t, store)
	server.capacityBackends = resolver

	server.capacitySweep.begin(4)
	if ready, _ := server.capacitySweep.poolReady("pool-up"); ready {
		t.Fatal("launches must wait until the first sweep pass has classified the pools")
	}
	err := server.reconcileOrphanedCapacityForTerm(context.Background(), 4)
	if err == nil || !strings.Contains(err.Error(), "pool-down") {
		t.Fatalf("sweep error = %v, want the failing pool reported", err)
	}
	if ready, _ := server.capacitySweep.poolReady("pool-up"); !ready {
		t.Fatal("a healthy pool must be released by its own successful sweep")
	}
	if ready, reason := server.capacitySweep.poolReady("pool-down"); ready || reason == "" {
		t.Fatal("a pool whose sweep failed must keep refusing launches")
	}
	if ready, _ := server.capacitySweep.poolReady("pool-created-after-takeover"); !ready {
		t.Fatal("a pool created after the first pass has no predecessor capacity to sweep")
	}
	if got := server.capacitySweep.pendingCount(); got != 1 {
		t.Fatalf("pending pools = %d, want 1", got)
	}

	down.setDiscoverErr(nil)
	if err := server.reconcileOrphanedCapacityForTerm(context.Background(), 4); err != nil {
		t.Fatalf("retry sweep: %v", err)
	}
	if ready, _ := server.capacitySweep.poolReady("pool-down"); !ready {
		t.Fatal("pool must launch once its retried sweep succeeds")
	}
	if got := server.capacitySweep.pendingCount(); got != 0 {
		t.Fatalf("pending pools = %d after recovery", got)
	}
	// A swept pool may already be launching new capacity; sweeping it again
	// in the same term could terminate those instances as untracked.
	if got := up.discoverCount(); got != 1 {
		t.Fatalf("healthy pool was swept %d times in one term, want once", got)
	}
	// A stale term cannot touch the current sweep.
	server.capacitySweep.begin(5)
	server.capacitySweep.markSwept(4, "pool-up")
	if ready, _ := server.capacitySweep.poolReady("pool-up"); ready {
		t.Fatal("an older term's sweep result must not release a newer term's gate")
	}
}

func TestAwaitCapacityPoolSweepGatesLaunchesUntilPoolIsSwept(t *testing.T) {
	var plain Server
	if err := plain.awaitCapacityPoolSweep(context.Background(), "pool-a", time.Now()); err != nil {
		t.Fatalf("single-controller launches must never be gated: %v", err)
	}
	server := &Server{}
	server.capacitySweep.begin(3)
	if err := server.awaitCapacityPoolSweep(context.Background(), "pool-a", time.Now().Add(50*time.Millisecond)); err == nil || !strings.Contains(err.Error(), "not ready") {
		t.Fatalf("unswept pool launch = %v, want a readiness error", err)
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		server.capacitySweep.markSwept(3, "pool-a")
	}()
	if err := server.awaitCapacityPoolSweep(context.Background(), "pool-a", time.Now().Add(3*time.Second)); err != nil {
		t.Fatalf("launch after sweep: %v", err)
	}
}

func readExecutionReadiness(t *testing.T, server *Server) (int, ExecutionReadinessResponse) {
	t.Helper()
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/api/v1/system/execution-readiness", nil))
	var body ExecutionReadinessResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode readiness: %v body=%s", err, resp.Body.String())
	}
	return resp.Code, body
}

// Provider recovery used to run inside the acquire callback, so a failing
// cloud API held admission closed while the healthy lease renewals blocked
// every takeover. The leader now admits and recovers the provider afterwards.
func TestTakeoverAdmitsWhileProviderSweepFails(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store, HAExecutionLeadership: true})
	_, down, resolver := saveSweepPools(t, store)
	server.capacityBackends = resolver

	server.leadership.tick()
	defer server.leadership.relinquish()
	waitForCondition(t, 5*time.Second, server.isExecutionLeader, "admission despite a failing provider")
	waitForCondition(t, 5*time.Second, func() bool { return server.capacitySweep.pendingCount() == 1 }, "the failing pool to be recorded")
	if ready, _ := server.capacitySweep.poolReady("pool-down"); ready {
		t.Fatal("the failing pool must refuse launches until its sweep succeeds")
	}
	code, body := readExecutionReadiness(t, server)
	if code != http.StatusOK || body.CapacityReconciliationPending != 1 || body.Recovering {
		t.Fatalf("readiness = %d %+v, want ready with one pending pool", code, body)
	}

	down.setDiscoverErr(nil)
	waitForCondition(t, 10*time.Second, func() bool {
		ready, _ := server.capacitySweep.poolReady("pool-down")
		return ready && server.capacitySweep.pendingCount() == 0
	}, "the retried sweep to succeed")
}

// A leader that cannot finish recovery reports it, then steps down so another
// replica can take over instead of holding the lease forever.
func TestRecoveryFailureIsReportedThenLeaderStepsDown(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store, HAExecutionLeadership: true})
	server.executionRecoveryGiveUp = 3 * time.Second
	var attempts atomic.Int32
	server.executionRecoveryStep = func() error {
		attempts.Add(1)
		return errors.New("replica-local recovery fault")
	}

	server.leadership.tick()
	waitForCondition(t, 2*time.Second, func() bool { return attempts.Load() > 0 }, "a failed recovery attempt")
	code, body := readExecutionReadiness(t, server)
	if code != http.StatusServiceUnavailable || !body.Recovering || body.RecoveryFailedAttempts == 0 || body.RecoveryStartedAt == nil {
		t.Fatalf("readiness while recovering = %d %+v, want 503 with recovery state", code, body)
	}
	if strings.Contains(strings.ToLower(body.Message), "fault") {
		t.Fatalf("unauthenticated readiness leaked recovery error detail: %q", body.Message)
	}

	other := newExecutionLeadership(store, "standby")
	defer other.relinquish()
	waitForCondition(t, 15*time.Second, func() bool {
		other.tick()
		return other.IsLeader()
	}, "the stuck leader to release its lease")
	if server.leadership.IsLeader() || server.isExecutionLeader() {
		t.Fatal("the replica that gave up must not keep leading")
	}
	if attempts.Load() < 2 {
		t.Fatalf("recovery attempts = %d, want retries before stepping down", attempts.Load())
	}
	other.relinquish()
	server.leadership.tick()
	if server.leadership.IsLeader() {
		t.Fatal("the replica that gave up must not contend again during its cooldown")
	}
}

func armedContinueRun(id string, generation uint64, client generatorClient) (*ManagedTest, context.Context) {
	ctx, cancel := context.WithCancel(context.Background())
	return &ManagedTest{
		ID:                        id,
		Name:                      id,
		Status:                    "RUNNING",
		ProjectID:                 storage.DefaultProjectID,
		Assignment:                &GeneratorAssignmentSettings{FailoverPolicy: failoverPolicyContinue},
		continuityArmedGeneration: generation,
		executionCtx:              ctx,
		cancel:                    cancel,
		remoteClient:              client,
		remoteClientsByTarget:     map[string]generatorClient{"gen-a:9091": client},
	}, ctx
}

func TestMarkContinuityHandoffSelectsOnlyAdoptableRuns(t *testing.T) {
	runner, _, leadership := newAdoptionTestRunner(t)
	_, generation := leadership.executionLeaseTerm()
	client := &startupLifecycleFakeClient{accept: true}
	armed, _ := armedContinueRun("armed", generation, client)
	failPolicy, _ := armedContinueRun("fail-policy", generation, client)
	failPolicy.Assignment = &GeneratorAssignmentSettings{}
	failPolicy.continuityHandoff = true // a stale mark must be cleared
	staleTerm, _ := armedContinueRun("armed-for-older-term", generation-1, client)
	stopping, _ := armedContinueRun("stopping", generation, client)
	stopping.Status = "STOPPING"
	finalizing, _ := armedContinueRun("finalizing", generation, client)
	finalizing.remoteFinalizing = true
	runner.mu.Lock()
	for _, test := range []*ManagedTest{armed, failPolicy, staleTerm, stopping, finalizing} {
		runner.tests[test.ID] = test
	}
	runner.mu.Unlock()

	if got := runner.markContinuityHandoff(generation); got != 1 {
		t.Fatalf("marked %d runs, want 1", got)
	}
	runner.mu.RLock()
	defer runner.mu.RUnlock()
	if !armed.continuityHandoff {
		t.Fatal("an armed continue-policy run must be handed off")
	}
	for _, test := range []*ManagedTest{failPolicy, staleTerm, stopping, finalizing} {
		if test.continuityHandoff {
			t.Fatalf("%s must not be handed off", test.ID)
		}
	}
}

// A voluntary step-down detaches handed-off runs without a generator Stop, so
// the successor can adopt them; other runs are still stopped.
func TestLeadershipLossTeardownDetachesHandedOffRunsWithoutStopping(t *testing.T) {
	runner, _, leadership := newAdoptionTestRunner(t)
	_, generation := leadership.executionLeaseTerm()
	handoffClient := &startupLifecycleFakeClient{accept: true}
	stopClient := &startupLifecycleFakeClient{accept: true}
	handoff, handoffCtx := armedContinueRun("handoff", generation, handoffClient)
	stopped, _ := armedContinueRun("fail-policy", generation, stopClient)
	stopped.Assignment = &GeneratorAssignmentSettings{}
	runner.mu.Lock()
	runner.tests[handoff.ID], runner.tests[stopped.ID] = handoff, stopped
	runner.mu.Unlock()
	if got := runner.markContinuityHandoff(generation); got != 1 {
		t.Fatalf("marked %d runs, want 1", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := runner.teardownActiveTestsAfterLeadershipLoss(ctx); err != nil {
		t.Fatalf("teardown: %v", err)
	}
	handoffClient.mu.Lock()
	handoffStops, handoffCloses := handoffClient.stopped, handoffClient.closed
	handoffClient.mu.Unlock()
	if handoffStops != 0 || handoffCloses != 1 {
		t.Fatalf("handed-off run cleanup = stop:%d close:%d, want 0/1", handoffStops, handoffCloses)
	}
	stopClient.mu.Lock()
	stops := stopClient.stopped
	stopClient.mu.Unlock()
	if stops != 1 {
		t.Fatalf("non-adoptable run stops = %d, want 1", stops)
	}
	if handoffCtx.Err() == nil {
		t.Fatal("the old leader must stop its own renewals for a handed-off run")
	}
	runner.mu.RLock()
	defer runner.mu.RUnlock()
	if handoff.continuityHandoff || handoff.remoteClientsByTarget != nil {
		t.Fatal("teardown must clear the handoff mark and detach the runtime")
	}
}

// A rolling update sends SIGTERM to the leader. Graceful shutdown used to stop
// every run (and race the lease release), so continue-policy runs survived only
// a crash. Now they are handed off and the lease is released for the successor.
func TestGracefulShutdownHandsOffContinueRunsAndReleasesLease(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store, HAExecutionLeadership: true})
	runner := server.runner
	runner.continuityKeyWrapper = testContinuityWrapper(t)
	runner.execution = newDistributedExecutionBackend([]string{"gen-a:9091"}, 200*time.Millisecond, func(context.Context, string) (generatorClient, error) {
		return nil, errors.New("generator unreachable")
	})
	leadershipDone := make(chan struct{})
	go func() {
		server.RunExecutionLeadership(context.Background())
		close(leadershipDone)
	}()
	waitForCondition(t, 5*time.Second, server.isExecutionLeader, "admission")
	if !runner.runAdoptionEnabled() {
		t.Fatal("test setup must enable run adoption")
	}
	_, generation := server.leadership.executionLeaseTerm()
	client := &startupLifecycleFakeClient{accept: true}
	test, runCtx := armedContinueRun("rolling-update", generation, client)
	runner.mu.Lock()
	runner.tests[test.ID] = test
	runner.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
	select {
	case <-leadershipDone:
	case <-time.After(5 * time.Second):
		t.Fatal("graceful shutdown did not release execution leadership")
	}
	client.mu.Lock()
	stops, closes := client.stopped, client.closed
	client.mu.Unlock()
	if stops != 0 || closes != 1 {
		t.Fatalf("continue-policy run cleanup on shutdown = stop:%d close:%d, want 0/1", stops, closes)
	}
	if runCtx.Err() == nil {
		t.Fatal("the old leader must stop renewing the handed-off run")
	}
	successor := newExecutionLeadership(store, "successor")
	defer successor.relinquish()
	successor.tick()
	if !successor.IsLeader() {
		t.Fatal("the lease must be released for an immediate takeover, not left to expire")
	}
}

func TestReloadActiveStoredTestsLoadsOnlyInFlightRuns(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	runner := NewTestRunner(10, NewHub(), store)
	t.Cleanup(runner.beginShutdown)
	now := time.Now().UTC()
	for _, record := range []*storage.TestRecord{
		{ID: "in-flight", Name: "in-flight", Status: "RUNNING", StartTime: now, Groups: []any{}},
		{ID: "historical", Name: "historical", Status: "COMPLETE", StartTime: now.Add(-time.Hour), EndTime: &now, Groups: []any{}},
	} {
		if err := store.SaveTest(record); err != nil {
			t.Fatal(err)
		}
	}
	runner.mu.Lock()
	delete(runner.tests, "in-flight")
	delete(runner.tests, "historical")
	runner.tests["memory-only"] = &ManagedTest{ID: "memory-only", Name: "memory-only", Status: "COMPLETE"}
	runner.mu.Unlock()

	supported, err := runner.reloadActiveStoredTests()
	if err != nil || !supported {
		t.Fatalf("reloadActiveStoredTests = (%v, %v)", supported, err)
	}
	runner.mu.RLock()
	defer runner.mu.RUnlock()
	if runner.tests["in-flight"] == nil {
		t.Fatal("an in-flight run must be reloaded")
	}
	if runner.tests["historical"] != nil {
		t.Fatal("the active-only reload must not hydrate historical runs")
	}
	if runner.tests["memory-only"] == nil {
		t.Fatal("a partial snapshot must never prune runs it did not list")
	}
}

// adoptionOrderStore records how far acquisition had progressed when the full
// test reload began.
type adoptionOrderStore struct {
	*storage.SQLiteStore
	runner           atomic.Pointer[TestRunner]
	fullReloadMarker atomic.Int64
}

func (s *adoptionOrderStore) ListTests(projectID string, folderID *string) ([]*storage.TestRecord, error) {
	if runner := s.runner.Load(); runner != nil && projectID == "" && folderID == nil {
		s.fullReloadMarker.CompareAndSwap(-1, int64(runner.runAdoptionDoneGeneration.Load()))
	}
	return s.SQLiteStore.ListTests(projectID, folderID)
}

// Adoption must not wait for every historical run to hydrate: the orphans'
// generator grace and the epoch keeper are both running out meanwhile.
func TestReloadAndReconcileAdoptsBeforeFullReload(t *testing.T) {
	raw, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	store := &adoptionOrderStore{SQLiteStore: raw}
	store.fullReloadMarker.Store(-1)
	runner := NewTestRunner(10, NewHub(), store, WithRunContinuityKeyWrapper(testContinuityWrapper(t)))
	t.Cleanup(runner.beginShutdown)
	leadership := newExecutionLeadership(raw, "ordering-controller")
	leadership.tick()
	runner.leadership = leadership
	runner.durableExecutionFence = true
	runner.execution = newDistributedExecutionBackend([]string{"gen-a:9091"}, 200*time.Millisecond, func(context.Context, string) (generatorClient, error) {
		return nil, errors.New("generator unreachable")
	})
	store.runner.Store(runner)
	_, generation := leadership.executionLeaseTerm()

	if err := runner.reloadAndReconcile(); err != nil {
		t.Fatalf("reloadAndReconcile: %v", err)
	}
	if got := store.fullReloadMarker.Load(); got != int64(generation) {
		t.Fatalf("adoption marker at full reload = %d, want %d (adoption must finish first)", got, generation)
	}
	if got := runner.runAdoptionDoneGeneration.Load(); got != generation {
		t.Fatalf("adoption marker = %d, want %d", got, generation)
	}
}

// Graceful shutdown finalizes stopped runs asynchronously: the terminal status
// is committed first, then generator cleanup and archive collection follow. The
// lease must be kept until the runs this replica stopped are fully finalized,
// but never for a handed-off run or a row with no runtime here (such as a
// STOPPING row left by an earlier failover).
func TestAwaitRunsFinalizedWaitsForOwnedRunCleanup(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)
	t.Cleanup(runner.beginShutdown)
	runCtx, runCancel := context.WithCancel(context.Background())
	defer runCancel()
	client := &startupLifecycleFakeClient{accept: true}
	stopping := &ManagedTest{ID: "stopping", Status: "STOPPING", executionCtx: runCtx, cancel: runCancel, remoteClientsByTarget: map[string]generatorClient{"gen-a": client}}
	runner.mu.Lock()
	runner.tests[stopping.ID] = stopping
	runner.tests["handed-off"] = &ManagedTest{ID: "handed-off", Status: "RUNNING", executionCtx: runCtx, cancel: runCancel, continuityHandoff: true}
	runner.tests["row-only"] = &ManagedTest{ID: "row-only", Status: "STOPPING"}
	runner.mu.Unlock()
	owned := runner.ownedActiveRuns()
	if len(owned) != 1 || owned["stopping"] != stopping {
		t.Fatalf("owned runs = %v, want only the stopping run with a live runtime", owned)
	}
	// A run still RUNNING after the stop pass was not stopped (its stop
	// failed); waiting cannot finalize it, so it must not hold the lease.
	owned["stop-failed"] = &ManagedTest{ID: "stop-failed", Status: "RUNNING", executionCtx: runCtx, cancel: runCancel}
	// A run finalizing on its own stays RUNNING, and must still be waited for.
	selfFinalizing := &ManagedTest{ID: "self-finalizing", Status: "RUNNING", executionCtx: runCtx, cancel: runCancel, remoteFinalizing: true}
	owned[selfFinalizing.ID] = selfFinalizing
	runner.mu.Lock()
	runner.tests["stop-failed"] = owned["stop-failed"]
	runner.tests[selfFinalizing.ID] = selfFinalizing
	runner.mu.Unlock()

	expectPending := func(stage string) {
		t.Helper()
		short, cancelShort := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancelShort()
		if runner.awaitRunsFinalized(short, owned) {
			t.Fatalf("%s: shutdown must keep the lease while the run is finalizing", stage)
		}
	}
	expectPending("before the terminal status")
	runner.mu.Lock()
	stopping.Status = "ABORTED"
	runner.mu.Unlock()
	expectPending("while generator clients are still attached")
	unlockArchive := runner.lockArchiveCommitFence(stopping.ID)
	runner.mu.Lock()
	stopping.remoteClientsByTarget = nil
	runner.mu.Unlock()
	expectPending("while the archive is being committed")
	unlockArchive()
	expectPending("while a run is finalizing on its own")
	runner.mu.Lock()
	selfFinalizing.Status, selfFinalizing.remoteFinalizing = "COMPLETE", false
	runner.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if !runner.awaitRunsFinalized(ctx, owned) {
		t.Fatal("the wait must end once the run is terminal, detached and its archive committed")
	}
}

// A replica that led before, or refreshed as a follower, can hold a RUNNING
// copy of a continue-policy run that another leader has since finished or
// deleted. Adoption now runs before the full reload, so the active-only reload
// must refresh such copies; otherwise adoption acts on a terminal row, its
// intent write is refused, and every recovery retry fails the same way.
func TestReloadAndReconcileRefreshesStaleActiveCopiesBeforeAdoption(t *testing.T) {
	runner, store, _ := newAdoptionTestRunner(t)
	finished := storedContinueRun(t, runner, store, "finished-elsewhere", "continue")
	record, err := store.GetTest(finished.ID)
	if err != nil {
		t.Fatal(err)
	}
	ended := time.Now().UTC()
	record.Status, record.EndTime = "COMPLETE", &ended
	if err := store.SaveTest(record); err != nil {
		t.Fatal(err)
	}
	runner.mu.Lock()
	runner.tests[finished.ID] = finished
	runner.tests["deleted-elsewhere"] = &ManagedTest{ID: "deleted-elsewhere", Name: "deleted", Status: "RUNNING"}
	runner.mu.Unlock()

	if err := runner.reloadAndReconcile(); err != nil {
		t.Fatalf("reloadAndReconcile acted on a stale active copy: %v", err)
	}
	runner.mu.RLock()
	defer runner.mu.RUnlock()
	if got := runner.tests[finished.ID]; got == nil || got.Status != "COMPLETE" {
		t.Fatalf("stale copy of a finished run was not refreshed: %+v", got)
	}
	if runner.tests["deleted-elsewhere"] != nil {
		t.Fatal("a stale copy of a deleted run must be pruned")
	}
}

// The leadership loop outlives the signal so shutdown can stop runs under the
// lease. It must therefore refuse to start once shutdown has begun, or a
// replica could acquire the lease after shutdown's release point.
func TestExecutionLeadershipDoesNotStartAfterShutdownBegins(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store, HAExecutionLeadership: true})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
	done := make(chan struct{})
	go func() {
		server.RunExecutionLeadership(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("leadership started after shutdown began")
	}
	other := newExecutionLeadership(store, "healthy-replica")
	defer other.relinquish()
	other.tick()
	if !other.IsLeader() {
		t.Fatal("a shutting-down replica must not hold the execution lease")
	}
}

// Cloud capacity is tagged and discovered by owner identity. It must be shared
// by every replica and survive restarts, or an HA successor (or a restarted
// controller) cannot see, adopt or clean up its predecessor's instances.
func TestCapacityOwnerIdentityIsSharedAcrossReplicas(t *testing.T) {
	t.Setenv("APEXLOAD_CONTROLLER_ID", "")
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	owner := func(server *Server) string {
		resolver, ok := server.capacityBackends.(*capacityBackendResolver)
		if !ok {
			t.Fatalf("capacity backends = %T", server.capacityBackends)
		}
		owner, err := resolver.capacityOwnerID()
		if err != nil {
			t.Fatalf("capacity owner: %v", err)
		}
		return owner
	}
	first := NewServer(ServerConfig{Store: store, HAExecutionLeadership: true})
	second := NewServer(ServerConfig{Store: store, HAExecutionLeadership: true})
	if first.controllerID == second.controllerID {
		t.Fatal("test setup: replicas must have distinct process identities")
	}
	if owner(first) == "" || owner(first) != owner(second) {
		t.Fatalf("capacity owners = %q and %q, want one shared identity", owner(first), owner(second))
	}
	if owner(first) == first.controllerID {
		t.Fatal("capacity owner must not be the per-process controller identity")
	}

	t.Setenv("APEXLOAD_CONTROLLER_ID", "operator-configured")
	configured := NewServer(ServerConfig{Store: store})
	if got := owner(configured); got != "operator-configured" {
		t.Fatalf("configured capacity owner = %q, want the explicit APEXLOAD_CONTROLLER_ID", got)
	}
}

type flakyOwnerSettings struct {
	failures int
	stored   string
}

func (f *flakyOwnerSettings) EnsureSetting(_ string, candidate string) (string, error) {
	if f.failures > 0 {
		f.failures--
		return "", errors.New("database unavailable")
	}
	if f.stored == "" {
		f.stored = candidate
	}
	return f.stored, nil
}

// A boot-time database blip must not freeze the process onto its per-process
// identity: instances launched under it would again be invisible to every
// successor. The owner is retried and only cached once durable.
func TestCapacityOwnerRetriesInsteadOfFallingBack(t *testing.T) {
	t.Setenv("APEXLOAD_CONTROLLER_ID", "")
	settings := &flakyOwnerSettings{failures: 1}
	owner := newCapacityOwner(settings, "per-process-identity")
	if got, err := owner.identity(); err == nil || !errors.Is(err, errCapacityOwnerUnavailable) || got != "" {
		t.Fatalf("owner during outage = (%q, %v), want errCapacityOwnerUnavailable", got, err)
	}
	got, err := owner.identity()
	if err != nil || got == "" || got == "per-process-identity" || got != settings.stored {
		t.Fatalf("owner after recovery = (%q, %v), want the durable identity %q", got, err, settings.stored)
	}
}

func TestTakeoverSweepKeepsPoolGatedWhileOwnerUnavailable(t *testing.T) {
	server, store := newTimeslotServer(t)
	saveCapacityTestPool(t, store, &storage.FleetPool{ID: "pool-owner", Name: "owner", Status: "active", ExecutionTargetID: "target-owner", LifecycleMode: storage.FleetLifecycleWarmBurst})
	server.capacityBackends = ownerUnavailableResolver{}
	server.capacitySweep.begin(9)
	if err := server.reconcileOrphanedCapacityForTerm(context.Background(), 9); err == nil || !errors.Is(err, errCapacityOwnerUnavailable) {
		t.Fatalf("sweep error = %v, want the owner outage", err)
	}
	if ready, _ := server.capacitySweep.poolReady("pool-owner"); ready {
		t.Fatal("a pool whose instances cannot be recognized yet must stay gated, not be treated as swept")
	}
}

type ownerUnavailableResolver struct{}

func (ownerUnavailableResolver) BackendForPool(context.Context, *storage.FleetPool) (compute.Backend, *storage.ExecutionTarget, error) {
	return nil, nil, fmt.Errorf("%w: %w", errCapacityBackendUnavailable, errCapacityOwnerUnavailable)
}

// Cloud targets must not build a backend under a guessed identity while the
// owner is unavailable; Kubernetes and static targets never use it.
func TestCapacityResolverRequiresOwnerOnlyForCloudTargets(t *testing.T) {
	t.Setenv("APEXLOAD_CONTROLLER_ID", "")
	resolver := &capacityBackendResolver{enabled: true, controllerID: "per-process-identity", owner: newCapacityOwner(&flakyOwnerSettings{failures: 1 << 20}, "per-process-identity")}
	for _, kind := range []string{storage.ExecutionTargetAWSECSFargate, storage.ExecutionTargetAzureContainerApps, storage.ExecutionTargetAzureContainerInstances} {
		_, err := resolver.BackendForTarget(context.Background(), &storage.ExecutionTarget{ID: "t", Kind: kind})
		if !errors.Is(err, errCapacityOwnerUnavailable) || !errors.Is(err, errCapacityBackendUnavailable) {
			t.Fatalf("%s backend during owner outage: %v", kind, err)
		}
	}
	for _, kind := range []string{storage.ExecutionTargetKubernetes, storage.ExecutionTargetStatic} {
		_, err := resolver.BackendForTarget(context.Background(), &storage.ExecutionTarget{ID: "t", Kind: kind})
		if errors.Is(err, errCapacityOwnerUnavailable) || !errors.Is(err, errCapacityBackendUnavailable) {
			t.Fatalf("%s backend should keep its legacy-adapter result: %v", kind, err)
		}
	}
}

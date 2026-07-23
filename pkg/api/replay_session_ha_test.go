package api

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

func openReplaySessionHAStores(t *testing.T) (*storage.SQLiteStore, *storage.SQLiteStore) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "replay-session-ha.db")
	first, err := storage.NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("open first replay-session store: %v", err)
	}
	second, err := storage.NewSQLiteStore(path)
	if err != nil {
		_ = first.Close()
		t.Fatalf("open second replay-session store: %v", err)
	}
	t.Cleanup(func() {
		_ = second.Close()
		_ = first.Close()
	})
	return first, second
}

func TestHAReplaySessionIDReservationConflictsAcrossServersAndReusesAfterRelease(t *testing.T) {
	firstStore, secondStore := openReplaySessionHAStores(t)
	first := NewServer(ServerConfig{Store: firstStore, HAExecutionLeadership: true})
	second := NewServer(ServerConfig{Store: secondStore, HAExecutionLeadership: true})
	sessionID := uuid.New().String()

	got, releaseFirst, signal, err := first.reserveReplaySessionID(sessionID, "project-a")
	if err != nil || got != sessionID || signal == nil {
		t.Fatalf("first reservation = (%q, signal=%v, %v), want acquired HA lease", got, signal != nil, err)
	}
	if _, _, _, err := second.reserveReplaySessionID(sessionID, "project-b"); !errors.Is(err, errReplaySessionIDConflict) {
		t.Fatalf("second-server collision error = %v, want %v", err, errReplaySessionIDConflict)
	}

	releaseFirst()
	got, releaseSecond, signal, err := second.reserveReplaySessionID(sessionID, "project-b")
	if err != nil || got != sessionID || signal == nil {
		t.Fatalf("released ID reuse = (%q, signal=%v, %v), want acquired HA lease", got, signal != nil, err)
	}
	releaseSecond()
}

func TestHAReplaySessionTerminalTombstoneConflictsUntilPruned(t *testing.T) {
	firstStore, secondStore := openReplaySessionHAStores(t)
	first := NewServer(ServerConfig{Store: firstStore, HAExecutionLeadership: true})
	second := NewServer(ServerConfig{Store: secondStore, HAExecutionLeadership: true})
	sessionID := uuid.New().String()

	_, releaseActive, guard, err := first.reserveReplaySessionID(sessionID, "project-a")
	if err != nil {
		t.Fatalf("reserve terminal session: %v", err)
	}
	defer releaseActive()
	if err := guard.retainForTerminal(time.Minute); err != nil {
		t.Fatalf("retain terminal session: %v", err)
	}
	if _, _, _, err := second.reserveReplaySessionID(sessionID, "project-b"); !errors.Is(err, errReplaySessionIDConflict) {
		t.Fatalf("terminal tombstone collision = %v, want %v", err, errReplaySessionIDConflict)
	}

	guard.releaseRetainedID()
	_, releaseReused, _, err := second.reserveReplaySessionID(sessionID, "project-b")
	if err != nil {
		t.Fatalf("reuse after tombstone prune: %v", err)
	}
	releaseReused()
}

func TestHAReplaySessionDelayedReleaseIsOwnerFenced(t *testing.T) {
	firstStore, secondStore := openReplaySessionHAStores(t)
	first := NewServer(ServerConfig{Store: firstStore, HAExecutionLeadership: true})
	second := NewServer(ServerConfig{Store: secondStore, HAExecutionLeadership: true})
	sessionID := uuid.New().String()

	_, releaseOldOwner, _, err := first.reserveReplaySessionID(sessionID, "project-a")
	if err != nil {
		t.Fatalf("reserve old owner: %v", err)
	}
	releaseOldOwner()
	_, releaseNewOwner, _, err := second.reserveReplaySessionID(sessionID, "project-b")
	if err != nil {
		t.Fatalf("reserve successor: %v", err)
	}
	defer releaseNewOwner()

	// Invoke the old callback again after ownership moved. Its durable release
	// still carries the old token and must not expire the successor's lease.
	releaseOldOwner()
	if _, _, _, err := first.reserveReplaySessionID(sessionID, "project-c"); !errors.Is(err, errReplaySessionIDConflict) {
		t.Fatalf("reservation after stale release = %v, want successor conflict", err)
	}

	releaseNewOwner()
	_, releaseReused, _, err := first.reserveReplaySessionID(sessionID, "project-c")
	if err != nil {
		t.Fatalf("reserve after successor release: %v", err)
	}
	releaseReused()
}

func TestHAReplaySessionReservationFailsClosedWithoutLeaseCapability(t *testing.T) {
	server := NewServer(ServerConfig{HAExecutionLeadership: true})
	if _, _, _, err := server.reserveReplaySessionID(uuid.New().String(), "project-a"); !errors.Is(err, errReplaySessionLeaseUnavailable) {
		t.Fatalf("reservation error = %v, want %v", err, errReplaySessionLeaseUnavailable)
	}
	server.replaySessionMu.Lock()
	defer server.replaySessionMu.Unlock()
	if len(server.replaySessionReservations) != 0 {
		t.Fatalf("local reservations = %d after failed HA claim, want 0", len(server.replaySessionReservations))
	}
}

type loseReplayLeaseOnRenewStore struct {
	storage.Store
	delegate   *storage.SQLiteStore
	renewCalls atomic.Int32
}

type trackReplayLeaseRetentionStore struct {
	storage.Store
	delegate    *storage.SQLiteStore
	retainCalls atomic.Int32
	renewCalls  atomic.Int32
}

func (s *trackReplayLeaseRetentionStore) TryAcquireReplaySessionLease(ctx context.Context, sessionID, ownerToken, projectID string, ttl time.Duration, globalLimit, projectLimit int) (storage.ReplaySessionLeaseAcquireResult, error) {
	return s.delegate.TryAcquireReplaySessionLease(ctx, sessionID, ownerToken, projectID, ttl, globalLimit, projectLimit)
}

func (s *trackReplayLeaseRetentionStore) RenewReplaySessionLease(ctx context.Context, sessionID, ownerToken, projectID string, ttl time.Duration) (bool, error) {
	s.renewCalls.Add(1)
	return s.delegate.RenewReplaySessionLease(ctx, sessionID, ownerToken, projectID, ttl)
}

func (s *trackReplayLeaseRetentionStore) RetainReplaySessionLease(ctx context.Context, sessionID, ownerToken, projectID string, ttl time.Duration) (bool, error) {
	s.retainCalls.Add(1)
	return s.delegate.RetainReplaySessionLease(ctx, sessionID, ownerToken, projectID, ttl)
}

func (s *trackReplayLeaseRetentionStore) ReleaseReplaySessionLease(ctx context.Context, sessionID, ownerToken, projectID string) (bool, error) {
	return s.delegate.ReleaseReplaySessionLease(ctx, sessionID, ownerToken, projectID)
}

func (s *trackReplayLeaseRetentionStore) ReleaseRetainedReplaySessionLease(ctx context.Context, sessionID, ownerToken, projectID string) (bool, error) {
	return s.delegate.ReleaseRetainedReplaySessionLease(ctx, sessionID, ownerToken, projectID)
}

func TestHAReplaySessionRenewalStopsWithExecutionLeadershipEpoch(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "replay-session-leadership-loss.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Close() })
	store := &trackReplayLeaseRetentionStore{Store: base, delegate: base}
	server := NewServer(ServerConfig{Store: store, HAExecutionLeadership: true})
	server.replaySessionLeaseTTL = 120 * time.Millisecond
	server.leadership.tick()
	if !server.leadership.IsLeader() {
		t.Fatal("server did not acquire execution leadership")
	}
	executionCtx, releaseExecutionCtx := server.runner.executionOperationContext(context.Background())
	defer releaseExecutionCtx()
	_, release, signal, err := server.reserveReplaySessionIDContext(executionCtx, uuid.New().String(), "project-a")
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	deadline := time.Now().Add(2 * time.Second)
	for store.renewCalls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if store.renewCalls.Load() == 0 {
		t.Fatal("replay-session lease did not begin renewal")
	}
	server.leadership.relinquish()
	select {
	case <-signal.done:
	case <-time.After(time.Second):
		t.Fatal("leadership loss did not fence the replay-session lease")
	}
	callsAfterLoss := store.renewCalls.Load()
	time.Sleep(2 * server.replaySessionLeaseTTL)
	if got := store.renewCalls.Load(); got != callsAfterLoss {
		t.Fatalf("replay-session lease renewed after leadership loss: calls %d -> %d", callsAfterLoss, got)
	}
}

func (s *loseReplayLeaseOnRenewStore) TryAcquireReplaySessionLease(ctx context.Context, sessionID, ownerToken, projectID string, ttl time.Duration, globalLimit, projectLimit int) (storage.ReplaySessionLeaseAcquireResult, error) {
	return s.delegate.TryAcquireReplaySessionLease(ctx, sessionID, ownerToken, projectID, ttl, globalLimit, projectLimit)
}

func (s *loseReplayLeaseOnRenewStore) RenewReplaySessionLease(context.Context, string, string, string, time.Duration) (bool, error) {
	s.renewCalls.Add(1)
	return false, nil
}

func (s *loseReplayLeaseOnRenewStore) RetainReplaySessionLease(ctx context.Context, sessionID, ownerToken, projectID string, ttl time.Duration) (bool, error) {
	return s.delegate.RetainReplaySessionLease(ctx, sessionID, ownerToken, projectID, ttl)
}

func (s *loseReplayLeaseOnRenewStore) ReleaseReplaySessionLease(ctx context.Context, sessionID, ownerToken, projectID string) (bool, error) {
	return s.delegate.ReleaseReplaySessionLease(ctx, sessionID, ownerToken, projectID)
}

func (s *loseReplayLeaseOnRenewStore) ReleaseRetainedReplaySessionLease(ctx context.Context, sessionID, ownerToken, projectID string) (bool, error) {
	return s.delegate.ReleaseRetainedReplaySessionLease(ctx, sessionID, ownerToken, projectID)
}

func TestReplaySessionLeaseGuardFailsClosedAtLocalDeadline(t *testing.T) {
	signal := newReplaySessionLeaseSignal()
	deadline := time.Now()
	signal.setValidUntil(deadline)

	if err := signal.failureOrExpired(deadline); !errors.Is(err, errReplaySessionLeaseLost) {
		t.Fatalf("failureOrExpired = %v, want %v", err, errReplaySessionLeaseLost)
	}
	select {
	case <-signal.done:
	default:
		t.Fatal("expired guard did not notify the active replay watcher")
	}
}

func TestHAReplaySessionRenewalLossTerminatesActiveBrowserReplay(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "replay-session-renewal-loss.db"))
	if err != nil {
		t.Fatalf("open replay-session store: %v", err)
	}
	t.Cleanup(func() { _ = base.Close() })
	store := &loseReplayLeaseOnRenewStore{Store: base, delegate: base}
	server := NewServer(ServerConfig{Store: store, HAExecutionLeadership: true})
	server.replaySessionLeaseTTL = 150 * time.Millisecond

	session, err := server.browserReplay.Start("browser-script", &dsl.Script{
		Name: "Paused HA replay", Protocol: "browser", ProjectID: "project-a",
	}, BrowserReplayStartRequest{ReplaySessionID: uuid.New().String(), StartPaused: true})
	if err != nil {
		t.Fatalf("start browser replay: %v", err)
	}
	server.browserReplay.mu.RLock()
	runtime := server.browserReplay.sessions[session.ID]
	server.browserReplay.mu.RUnlock()
	if runtime == nil {
		t.Fatal("browser replay runtime was not retained")
	}
	select {
	case <-runtime.workerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("browser replay worker did not terminate after renewal loss")
	}
	runtime.mu.RLock()
	status, errMsg := runtime.Status, runtime.Error
	runtime.mu.RUnlock()
	if status != BrowserReplayFailed || !strings.Contains(errMsg, errReplaySessionLeaseLost.Error()) {
		t.Fatalf("browser replay after renewal loss = status %q error %q", status, errMsg)
	}
	if _, ok := server.browserReplay.Get(session.ID); ok {
		t.Fatal("lease-lost browser capability remained addressable after its global release")
	}
	if store.renewCalls.Load() == 0 {
		t.Fatal("browser replay terminated without attempting lease renewal")
	}
}

func TestHAStoppedBrowserReplayRemainsTrackedAndActiveUntilWorkerFinishes(t *testing.T) {
	firstStore, secondStore := openReplaySessionHAStores(t)
	trackedStore := &trackReplayLeaseRetentionStore{Store: firstStore, delegate: firstStore}
	first := NewServer(ServerConfig{Store: trackedStore, HAExecutionLeadership: true})
	second := NewServer(ServerConfig{Store: secondStore, HAExecutionLeadership: true})
	first.browserReplay.retention.maxSessions = 1
	t.Cleanup(first.browserReplay.Close)

	started := make(chan struct{})
	unblock := make(chan struct{})
	var unblockOnce sync.Once
	releaseWorker := func() { unblockOnce.Do(func() { close(unblock) }) }
	t.Cleanup(releaseWorker)
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		close(started)
		<-unblock // Deliberately ignore cancellation to exercise capacity fencing.
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})
	oldID := uuid.New().String()
	oldSnapshot, err := first.browserReplay.Start("old-script", &dsl.Script{
		Name: "blocked old replay", Protocol: protocolName, ProjectID: "project-a",
		Actions: []dsl.Action{{Name: "blocked", Type: "request", Target: "/blocked"}},
	}, BrowserReplayStartRequest{ReplaySessionID: oldID})
	if err != nil {
		t.Fatalf("start old replay: %v", err)
	}
	<-started
	first.browserReplay.mu.RLock()
	oldRuntime := first.browserReplay.sessions[oldSnapshot.ID]
	first.browserReplay.mu.RUnlock()
	if oldRuntime == nil {
		t.Fatal("old runtime session was not retained by manager")
	}
	if _, ok := first.browserReplay.Stop(oldID); !ok {
		t.Fatal("stop old replay returned not found")
	}
	if got := trackedStore.retainCalls.Load(); got != 0 {
		t.Fatalf("retention transitions after Stop = %d, want 0 until worker exit", got)
	}

	newID := uuid.New().String()
	if _, err := first.browserReplay.Start("new-script", &dsl.Script{
		Name: "replacement replay", Protocol: protocolName, ProjectID: "project-a",
	}, BrowserReplayStartRequest{ReplaySessionID: newID, StartPaused: true}); !errors.Is(err, errBrowserReplaySessionCapacity) {
		t.Fatalf("replacement while stopped worker is blocked = %v, want %v", err, errBrowserReplaySessionCapacity)
	}
	first.browserReplay.mu.RLock()
	_, oldStillMapped := first.browserReplay.sessions[oldID]
	first.browserReplay.mu.RUnlock()
	if !oldStillMapped {
		t.Fatal("stopped unfinished worker was evicted from the manager")
	}
	if _, _, _, err := second.reserveReplaySessionID(oldID, "project-b"); !errors.Is(err, errReplaySessionIDConflict) {
		t.Fatalf("blocked worker ID reservation = %v, want active conflict", err)
	}

	releaseWorker()
	select {
	case <-oldRuntime.workerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("blocked old worker did not finish after release")
	}
	if got := trackedStore.retainCalls.Load(); got != 1 {
		t.Fatalf("retention transitions after worker exit = %d, want 1", got)
	}

	if _, err := first.browserReplay.Start("new-script", &dsl.Script{
		Name: "replacement replay", Protocol: protocolName, ProjectID: "project-a",
	}, BrowserReplayStartRequest{ReplaySessionID: newID, StartPaused: true}); err != nil {
		t.Fatalf("start replacement after worker exit: %v", err)
	}
	first.browserReplay.mu.RLock()
	_, oldStillMapped = first.browserReplay.sessions[oldID]
	first.browserReplay.mu.RUnlock()
	if oldStillMapped {
		t.Fatal("finished terminal worker was not evicted at the configured manager cap")
	}
	_, releaseReused, _, err := second.reserveReplaySessionID(oldID, "project-b")
	if err != nil {
		t.Fatalf("reserve old ID after worker exit and eviction: %v", err)
	}
	releaseReused()
}

func TestHAIdleCancelledScriptDebugRemainsTrackedAndActiveUntilWorkerFinishes(t *testing.T) {
	firstStore, secondStore := openReplaySessionHAStores(t)
	trackedStore := &trackReplayLeaseRetentionStore{Store: firstStore, delegate: firstStore}
	first := NewServer(ServerConfig{Store: trackedStore, HAExecutionLeadership: true})
	second := NewServer(ServerConfig{Store: secondStore, HAExecutionLeadership: true})
	first.scriptDebug.retention.maxSessions = 1
	first.scriptDebug.retention.idleCancel = time.Minute
	clockNow := time.Now().UTC()
	first.scriptDebug.retention.now = func() time.Time { return clockNow }
	t.Cleanup(first.scriptDebug.Close)

	started := make(chan struct{})
	unblock := make(chan struct{})
	var unblockOnce sync.Once
	releaseWorker := func() { unblockOnce.Do(func() { close(unblock) }) }
	t.Cleanup(releaseWorker)
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		close(started)
		<-unblock // Deliberately ignore cancellation to exercise capacity fencing.
		return &protocol.Result{
			ActionName: action.Name,
			Success:    true,
			StatusCode: 200,
			CorrelationEvidence: &protocol.CorrelationEvidence{
				RequestURL:   "/raw",
				ResponseBody: "must-be-rejected-after-stop",
			},
		}, nil
	})
	oldID := uuid.New().String()
	oldSnapshot, err := first.scriptDebug.StartChecked("old-script", &dsl.Script{
		Name: "blocked old debug", Protocol: protocolName, ProjectID: "project-a",
		Actions: []dsl.Action{{Name: "blocked", Type: "request", Target: "/blocked"}},
	}, ScriptDebugStartRequest{ReplaySessionID: oldID})
	if err != nil {
		t.Fatalf("start old debugger: %v", err)
	}
	<-started
	first.scriptDebug.mu.RLock()
	oldRuntime := first.scriptDebug.sessions[oldSnapshot.ID]
	first.scriptDebug.mu.RUnlock()
	if oldRuntime == nil {
		t.Fatal("old debugger runtime session was not retained by manager")
	}
	clockNow = clockNow.Add(2 * time.Minute)
	if removed := first.scriptDebug.PruneExpired(); removed != 0 {
		t.Fatalf("idle cancellation removed %d unfinished sessions, want 0", removed)
	}
	oldRuntime.mu.RLock()
	oldStatus := oldRuntime.Status
	oldRuntime.mu.RUnlock()
	if oldStatus != ScriptDebugStopped {
		t.Fatalf("idle-cancelled debugger status = %q, want %q", oldStatus, ScriptDebugStopped)
	}
	if got := trackedStore.retainCalls.Load(); got != 0 {
		t.Fatalf("debug retention transitions after idle cancellation = %d, want 0 until worker exit", got)
	}

	newID := uuid.New().String()
	if _, err := first.scriptDebug.StartChecked("new-script", &dsl.Script{
		Name: "replacement debugger", Protocol: protocolName, ProjectID: "project-a",
	}, ScriptDebugStartRequest{ReplaySessionID: newID, StartPaused: true}); !errors.Is(err, errScriptDebugSessionCapacity) {
		t.Fatalf("replacement debugger while stopped worker is blocked = %v, want %v", err, errScriptDebugSessionCapacity)
	}
	first.scriptDebug.mu.RLock()
	_, oldStillMapped := first.scriptDebug.sessions[oldID]
	first.scriptDebug.mu.RUnlock()
	if !oldStillMapped {
		t.Fatal("stopped unfinished debugger worker was evicted from the manager")
	}
	if _, _, _, err := second.reserveReplaySessionID(oldID, "project-b"); !errors.Is(err, errReplaySessionIDConflict) {
		t.Fatalf("blocked debugger worker ID reservation = %v, want active conflict", err)
	}

	releaseWorker()
	select {
	case <-oldRuntime.workerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("blocked debugger worker did not finish after release")
	}
	if got := trackedStore.retainCalls.Load(); got != 1 {
		t.Fatalf("debug retention transitions after worker exit = %d, want 1", got)
	}

	if _, err := first.scriptDebug.StartChecked("new-script", &dsl.Script{
		Name: "replacement debugger", Protocol: protocolName, ProjectID: "project-a",
	}, ScriptDebugStartRequest{ReplaySessionID: newID, StartPaused: true}); err != nil {
		t.Fatalf("start replacement debugger after worker exit: %v", err)
	}
	first.scriptDebug.mu.RLock()
	_, oldStillMapped = first.scriptDebug.sessions[oldID]
	first.scriptDebug.mu.RUnlock()
	if oldStillMapped {
		t.Fatal("finished terminal debugger worker was not evicted at the configured manager cap")
	}
	_, releaseReused, _, err := second.reserveReplaySessionID(oldID, "project-b")
	if err != nil {
		t.Fatalf("reserve old debugger ID after worker exit and eviction: %v", err)
	}
	releaseReused()
}

type blockingAcquireReplayLeaseStore struct {
	storage.Store
	delegate    *storage.SQLiteStore
	blockID     string
	enteredOnce sync.Once
	entered     chan struct{}
	release     chan struct{}
}

func (s *blockingAcquireReplayLeaseStore) TryAcquireReplaySessionLease(ctx context.Context, sessionID, ownerToken, projectID string, ttl time.Duration, globalLimit, projectLimit int) (storage.ReplaySessionLeaseAcquireResult, error) {
	if sessionID == s.blockID {
		s.enteredOnce.Do(func() { close(s.entered) })
		<-s.release
	}
	return s.delegate.TryAcquireReplaySessionLease(ctx, sessionID, ownerToken, projectID, ttl, globalLimit, projectLimit)
}

func (s *blockingAcquireReplayLeaseStore) RenewReplaySessionLease(ctx context.Context, sessionID, ownerToken, projectID string, ttl time.Duration) (bool, error) {
	return s.delegate.RenewReplaySessionLease(ctx, sessionID, ownerToken, projectID, ttl)
}

func (s *blockingAcquireReplayLeaseStore) RetainReplaySessionLease(ctx context.Context, sessionID, ownerToken, projectID string, ttl time.Duration) (bool, error) {
	return s.delegate.RetainReplaySessionLease(ctx, sessionID, ownerToken, projectID, ttl)
}

func (s *blockingAcquireReplayLeaseStore) ReleaseReplaySessionLease(ctx context.Context, sessionID, ownerToken, projectID string) (bool, error) {
	return s.delegate.ReleaseReplaySessionLease(ctx, sessionID, ownerToken, projectID)
}

func (s *blockingAcquireReplayLeaseStore) ReleaseRetainedReplaySessionLease(ctx context.Context, sessionID, ownerToken, projectID string) (bool, error) {
	return s.delegate.ReleaseRetainedReplaySessionLease(ctx, sessionID, ownerToken, projectID)
}

// A slow HA lease acquisition for one session must not serialize every other
// session start behind the process-wide reservation mutex, and a concurrent
// local claim of the same pending ID must conflict immediately.
func TestHAReplaySessionReservationDoesNotHoldMutexAcrossLeaseAcquire(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "replay-session-pending.db"))
	if err != nil {
		t.Fatalf("open replay-session store: %v", err)
	}
	t.Cleanup(func() { _ = base.Close() })
	blockID := uuid.New().String()
	store := &blockingAcquireReplayLeaseStore{
		Store:    base,
		delegate: base,
		blockID:  blockID,
		entered:  make(chan struct{}),
		release:  make(chan struct{}),
	}
	server := NewServer(ServerConfig{Store: store, HAExecutionLeadership: true})

	type reserveResult struct {
		id      string
		release func()
		err     error
	}
	blockedDone := make(chan reserveResult, 1)
	go func() {
		id, release, _, err := server.reserveReplaySessionID(blockID, "project-a")
		blockedDone <- reserveResult{id: id, release: release, err: err}
	}()
	select {
	case <-store.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("blocked lease acquire was never entered")
	}

	// While the first reservation's durable acquire is stalled inside the store,
	// an unrelated reservation must complete promptly.
	otherDone := make(chan reserveResult, 1)
	go func() {
		id, release, _, err := server.reserveReplaySessionID(uuid.New().String(), "project-a")
		otherDone <- reserveResult{id: id, release: release, err: err}
	}()
	select {
	case result := <-otherDone:
		if result.err != nil {
			t.Fatalf("concurrent reservation failed: %v", result.err)
		}
		result.release()
	case <-time.After(2 * time.Second):
		t.Fatal("concurrent reservation stalled behind the blocked lease acquire (replaySessionMu held across store I/O)")
	}

	// The pending entry must already own the ID locally.
	if _, _, _, err := server.reserveReplaySessionID(blockID, "project-b"); !errors.Is(err, errReplaySessionIDConflict) {
		t.Fatalf("pending-ID collision error = %v, want %v", err, errReplaySessionIDConflict)
	}

	close(store.release)
	select {
	case result := <-blockedDone:
		if result.err != nil || result.id != blockID {
			t.Fatalf("blocked reservation = (%q, %v), want acquired lease", result.id, result.err)
		}
		result.release()
	case <-time.After(2 * time.Second):
		t.Fatal("blocked reservation did not finish after the store was released")
	}
}

type flakyRenewReplayLeaseStore struct {
	storage.Store
	delegate   *storage.SQLiteStore
	renewCalls atomic.Int32
	failCount  int32 // number of leading renew calls that error; negative = always
}

func (s *flakyRenewReplayLeaseStore) TryAcquireReplaySessionLease(ctx context.Context, sessionID, ownerToken, projectID string, ttl time.Duration, globalLimit, projectLimit int) (storage.ReplaySessionLeaseAcquireResult, error) {
	return s.delegate.TryAcquireReplaySessionLease(ctx, sessionID, ownerToken, projectID, ttl, globalLimit, projectLimit)
}

func (s *flakyRenewReplayLeaseStore) RenewReplaySessionLease(ctx context.Context, sessionID, ownerToken, projectID string, ttl time.Duration) (bool, error) {
	call := s.renewCalls.Add(1)
	if s.failCount < 0 || call <= s.failCount {
		return false, errors.New("transient store outage")
	}
	return s.delegate.RenewReplaySessionLease(ctx, sessionID, ownerToken, projectID, ttl)
}

func (s *flakyRenewReplayLeaseStore) RetainReplaySessionLease(ctx context.Context, sessionID, ownerToken, projectID string, ttl time.Duration) (bool, error) {
	return s.delegate.RetainReplaySessionLease(ctx, sessionID, ownerToken, projectID, ttl)
}

func (s *flakyRenewReplayLeaseStore) ReleaseReplaySessionLease(ctx context.Context, sessionID, ownerToken, projectID string) (bool, error) {
	return s.delegate.ReleaseReplaySessionLease(ctx, sessionID, ownerToken, projectID)
}

func (s *flakyRenewReplayLeaseStore) ReleaseRetainedReplaySessionLease(ctx context.Context, sessionID, ownerToken, projectID string) (bool, error) {
	return s.delegate.ReleaseRetainedReplaySessionLease(ctx, sessionID, ownerToken, projectID)
}

func TestHAReplaySessionRenewalRetriesTransientErrorsWithinValidity(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "replay-session-renew-retry.db"))
	if err != nil {
		t.Fatalf("open replay-session store: %v", err)
	}
	t.Cleanup(func() { _ = base.Close() })
	store := &flakyRenewReplayLeaseStore{Store: base, delegate: base, failCount: 1}
	server := NewServer(ServerConfig{Store: store, HAExecutionLeadership: true})
	server.replaySessionLeaseTTL = 400 * time.Millisecond

	_, release, signal, err := server.reserveReplaySessionID(uuid.New().String(), "project-a")
	if err != nil {
		t.Fatalf("reserve session: %v", err)
	}
	defer release()

	// The first renewal errors; a retry inside the validity window must succeed
	// and keep the lease alive well past the original validity deadline.
	deadline := time.Now().Add(3 * time.Second)
	for store.renewCalls.Load() < 3 {
		if time.Now().After(deadline) {
			t.Fatalf("renewals observed = %d, want >= 3 (retry after transient error)", store.renewCalls.Load())
		}
		if err := signal.failureOrExpired(time.Now()); err != nil {
			t.Fatalf("lease failed despite transient-only renewal error: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := signal.failureOrExpired(time.Now()); err != nil {
		t.Fatalf("lease guard reports %v after successful retries, want live lease", err)
	}
	select {
	case <-signal.done:
		t.Fatal("lease signal latched failure despite recovered renewals")
	default:
	}
}

func TestHAReplaySessionRenewalFailsClosedWhenErrorsOutlastValidity(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "replay-session-renew-dead.db"))
	if err != nil {
		t.Fatalf("open replay-session store: %v", err)
	}
	t.Cleanup(func() { _ = base.Close() })
	store := &flakyRenewReplayLeaseStore{Store: base, delegate: base, failCount: -1}
	server := NewServer(ServerConfig{Store: store, HAExecutionLeadership: true})
	server.replaySessionLeaseTTL = 300 * time.Millisecond

	_, release, signal, err := server.reserveReplaySessionID(uuid.New().String(), "project-a")
	if err != nil {
		t.Fatalf("reserve session: %v", err)
	}
	defer release()

	select {
	case <-signal.done:
	case <-time.After(3 * time.Second):
		t.Fatal("persistently failing renewal never fenced the lease")
	}
	if err := signal.failureOrExpired(time.Now()); !errors.Is(err, errReplaySessionLeaseLost) {
		t.Fatalf("lease failure = %v, want %v", err, errReplaySessionLeaseLost)
	}
	if calls := store.renewCalls.Load(); calls < 2 {
		t.Fatalf("renew attempts before fencing = %d, want retries before the validity deadline", calls)
	}
}

func TestHAScriptDebugLeaseLossStillBroadcastsTerminalFailure(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "replay-session-terminal-broadcast.db"))
	if err != nil {
		t.Fatalf("open replay-session store: %v", err)
	}
	t.Cleanup(func() { _ = base.Close() })
	store := &loseReplayLeaseOnRenewStore{Store: base, delegate: base}
	server := NewServer(ServerConfig{Store: store, HAExecutionLeadership: true})
	server.replaySessionLeaseTTL = 150 * time.Millisecond

	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})
	sessionID := uuid.New().String()
	if _, err := server.scriptDebug.StartChecked("debug-script", &dsl.Script{
		Name: "lease loss debug", Protocol: protocolName, ProjectID: "project-a",
		Actions: []dsl.Action{{Name: "step", Type: "request", Target: "/step"}},
	}, ScriptDebugStartRequest{ReplaySessionID: sessionID, StartPaused: true}); err != nil {
		t.Fatalf("start debug session: %v", err)
	}
	server.scriptDebug.mu.RLock()
	runtime := server.scriptDebug.sessions[sessionID]
	server.scriptDebug.mu.RUnlock()
	if runtime == nil {
		t.Fatal("debug runtime was not retained")
	}

	message := waitForHubBroadcastType(t, server.hub, "script_debug_failed")
	select {
	case <-runtime.workerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("debug worker did not terminate after renewal loss")
	}
	runtime.mu.RLock()
	status, errMsg := runtime.Status, runtime.Error
	runtime.mu.RUnlock()
	if status != ScriptDebugFailed || !strings.Contains(errMsg, errReplaySessionLeaseLost.Error()) {
		t.Fatalf("debug session after renewal loss = status %q error %q", status, errMsg)
	}
	if _, ok := server.scriptDebug.Get(sessionID); ok {
		t.Fatal("lease-lost debug capability remained addressable after its global release")
	}
	if message["replay_session_id"] != sessionID {
		t.Fatalf("terminal broadcast session id = %v, want %q", message["replay_session_id"], sessionID)
	}
	if _, hasReport := message["report"]; hasReport {
		t.Fatal("lease-lost terminal broadcast leaked a report payload")
	}
}

func TestHAServerShutdownBoundsUncooperativeDebugWorkerAndReleasesLease(t *testing.T) {
	firstStore, secondStore := openReplaySessionHAStores(t)
	first := NewServer(ServerConfig{Store: firstStore, HAExecutionLeadership: true})
	second := NewServer(ServerConfig{Store: secondStore, HAExecutionLeadership: true})
	first.scriptDebug.closeTimeout = 50 * time.Millisecond

	started := make(chan struct{})
	unblock := make(chan struct{})
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		close(started)
		<-unblock // Deliberately outlive cancellation and the bounded shutdown wait.
		return &protocol.Result{
			ActionName: action.Name,
			Success:    true,
			StatusCode: 200,
			CorrelationEvidence: &protocol.CorrelationEvidence{
				RequestURL:   "/raw",
				ResponseBody: "must-not-survive-close",
			},
		}, nil
	})
	sessionID := uuid.New().String()
	snapshot, err := first.scriptDebug.StartChecked("debug-script", &dsl.Script{
		Name: "blocked debug replay", Protocol: protocolName, ProjectID: "project-a",
		Actions: []dsl.Action{{Name: "blocked", Type: "request", Target: "/blocked"}},
	}, ScriptDebugStartRequest{ReplaySessionID: sessionID})
	if err != nil {
		t.Fatalf("start debug replay: %v", err)
	}
	<-started
	first.scriptDebug.mu.RLock()
	runtimeSession := first.scriptDebug.sessions[snapshot.ID]
	first.scriptDebug.mu.RUnlock()
	if runtimeSession == nil {
		t.Fatal("debug runtime session was not retained by manager")
	}
	_ = drainHubBroadcasts(t, first.hub)

	shutdownStarted := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	err = first.Shutdown(ctx)
	cancel()
	if err != nil {
		t.Fatalf("shutdown blocked by uncooperative replay: %v", err)
	}
	if elapsed := time.Since(shutdownStarted); elapsed > 500*time.Millisecond {
		t.Fatalf("shutdown took %s, want bounded drain below 500ms", elapsed)
	}
	_, releaseAfterShutdown, _, err := second.reserveReplaySessionID(sessionID, "project-b")
	if err != nil {
		t.Fatalf("reserve ID after shutdown release: %v", err)
	}
	releaseAfterShutdown()
	runtimeSession.mu.RLock()
	if len(runtimeSession.correlationObservations) != 0 || runtimeSession.correlationEvidenceBytes != 0 || len(runtimeSession.Events) != 0 || runtimeSession.FinalReport != nil {
		runtimeSession.mu.RUnlock()
		t.Fatal("shutdown retained private or replay evidence on the detached session")
	}
	runtimeSession.mu.RUnlock()

	close(unblock)
	select {
	case <-runtimeSession.workerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("uncooperative debug worker did not finish after release")
	}
	for _, message := range drainHubBroadcasts(t, first.hub) {
		if message["replay_session_id"] == sessionID {
			t.Fatalf("closed debug worker published stale message: %#v", message)
		}
	}
	closedAgain := time.Now()
	first.scriptDebug.Close()
	if elapsed := time.Since(closedAgain); elapsed > 100*time.Millisecond {
		t.Fatalf("idempotent manager Close took %s", elapsed)
	}
}

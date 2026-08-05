package api

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

type blockingStartCommitStore struct {
	storage.Store
	target interface {
		SaveTestStartIfNoDeploymentLease(*storage.TestRecord, string, string, string) error
		SaveTestStartIfExecutionLeaseHeldAndNoDeploymentLease(*storage.TestRecord, string, string, string, string, string) (bool, error)
		TransitionTestToPending(storage.TestPendingTransition) (storage.TestStatusTransitionResult, error)
		TransitionTestFromShakeout(storage.TestShakeoutTransition) (storage.TestStatusTransitionResult, error)
		StartAdmissionRevision() (int64, error)
	}
	runner              *TestRunner
	mu                  sync.Mutex
	entered             chan struct{}
	release             chan struct{}
	blocked             bool
	blockMode           string
	passiveReturnErr    error
	calledWhileRunnerMu atomic.Bool
}

func (s *blockingStartCommitStore) blockNextStartCommit() (<-chan struct{}, chan<- struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Install the narrow fault-injection facade only after test creation. The
	// production SQLite store remains the source for every other optional
	// atomic-create/admission capability.
	s.runner.testStore = s
	s.entered = make(chan struct{})
	s.release = make(chan struct{})
	s.blocked = false
	s.blockMode = "start"
	return s.entered, s.release
}

func (s *blockingStartCommitStore) blockNextCommittedStartReturn() (<-chan struct{}, chan<- struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runner.testStore = s
	s.entered = make(chan struct{})
	s.release = make(chan struct{})
	s.blocked = false
	s.blockMode = "start_return"
	return s.entered, s.release
}

func (s *blockingStartCommitStore) blockNextOutcomeCommit() (<-chan struct{}, chan<- struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runner.testStore = s
	s.entered = make(chan struct{})
	s.release = make(chan struct{})
	s.blocked = false
	s.blockMode = "outcome"
	return s.entered, s.release
}

func (s *blockingStartCommitStore) blockNextCommittedPassiveReturn(mode string) (<-chan struct{}, chan<- struct{}) {
	return s.blockNextCommittedPassiveResponse(mode, nil)
}

func (s *blockingStartCommitStore) blockNextCommittedPassiveLostResponse(mode string, responseErr error) (<-chan struct{}, chan<- struct{}) {
	return s.blockNextCommittedPassiveResponse(mode, responseErr)
}

func (s *blockingStartCommitStore) blockNextCommittedPassiveResponse(mode string, responseErr error) (<-chan struct{}, chan<- struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runner.testStore = s
	s.entered = make(chan struct{})
	s.release = make(chan struct{})
	s.blocked = false
	s.blockMode = mode + "_return"
	s.passiveReturnErr = responseErr
	return s.entered, s.release
}

func (s *blockingStartCommitStore) beforeStartCommit() {
	if s.runner != nil {
		if s.runner.mu.TryLock() {
			s.runner.mu.Unlock()
		} else {
			s.calledWhileRunnerMu.Store(true)
		}
	}
	s.mu.Lock()
	if s.entered == nil || s.blocked || s.blockMode != "start" {
		s.mu.Unlock()
		return
	}
	s.blocked = true
	entered := s.entered
	release := s.release
	close(entered)
	s.mu.Unlock()
	<-release
}

func (s *blockingStartCommitStore) afterStartCommit() {
	if s.runner != nil {
		if s.runner.mu.TryLock() {
			s.runner.mu.Unlock()
		} else {
			s.calledWhileRunnerMu.Store(true)
		}
	}
	s.mu.Lock()
	if s.entered == nil || s.blocked || s.blockMode != "start_return" {
		s.mu.Unlock()
		return
	}
	s.blocked = true
	entered := s.entered
	release := s.release
	close(entered)
	s.mu.Unlock()
	<-release
}

func (s *blockingStartCommitStore) afterPassiveCommit(mode string) error {
	s.mu.Lock()
	if s.entered == nil || s.blocked || s.blockMode != mode+"_return" {
		s.mu.Unlock()
		return nil
	}
	s.blocked = true
	entered := s.entered
	release := s.release
	close(entered)
	s.mu.Unlock()
	<-release
	s.mu.Lock()
	responseErr := s.passiveReturnErr
	s.mu.Unlock()
	return responseErr
}

func (s *blockingStartCommitStore) SaveTest(test *storage.TestRecord) error {
	s.mu.Lock()
	shouldBlock := s.entered != nil && !s.blocked && s.blockMode == "outcome"
	if shouldBlock {
		s.blocked = true
		entered := s.entered
		release := s.release
		if s.runner != nil {
			if s.runner.mu.TryLock() {
				s.runner.mu.Unlock()
			} else {
				s.calledWhileRunnerMu.Store(true)
			}
		}
		close(entered)
		s.mu.Unlock()
		<-release
	} else {
		s.mu.Unlock()
	}
	return s.Store.SaveTest(test)
}

func (s *blockingStartCommitStore) SaveTestStartIfNoDeploymentLease(test *storage.TestRecord, leaseName, ownerID, tokenHash string) error {
	s.beforeStartCommit()
	err := s.target.SaveTestStartIfNoDeploymentLease(test, leaseName, ownerID, tokenHash)
	if err == nil {
		s.afterStartCommit()
	}
	return err
}

func (s *blockingStartCommitStore) SaveTestStartIfExecutionLeaseHeldAndNoDeploymentLease(test *storage.TestRecord, executionLeaseName, executionOwnerID, deploymentLeaseName, deploymentOwnerID, deploymentTokenHash string) (bool, error) {
	s.beforeStartCommit()
	saved, err := s.target.SaveTestStartIfExecutionLeaseHeldAndNoDeploymentLease(test, executionLeaseName, executionOwnerID, deploymentLeaseName, deploymentOwnerID, deploymentTokenHash)
	if err == nil && saved {
		s.afterStartCommit()
	}
	return saved, err
}

func (s *blockingStartCommitStore) TransitionTestToPending(transition storage.TestPendingTransition) (storage.TestStatusTransitionResult, error) {
	result, err := s.target.TransitionTestToPending(transition)
	if err == nil && (result == storage.TestStatusTransitionUpdated || result == storage.TestStatusTransitionUnchanged) {
		if responseErr := s.afterPassiveCommit("pending"); responseErr != nil {
			return storage.TestStatusTransitionFenced, responseErr
		}
	}
	return result, err
}

func (s *blockingStartCommitStore) TransitionTestFromShakeout(transition storage.TestShakeoutTransition) (storage.TestStatusTransitionResult, error) {
	s.mu.Lock()
	shouldBlock := s.entered != nil && !s.blocked && s.blockMode == "outcome"
	if shouldBlock {
		s.blocked = true
		entered := s.entered
		release := s.release
		if s.runner != nil {
			if s.runner.mu.TryLock() {
				s.runner.mu.Unlock()
			} else {
				s.calledWhileRunnerMu.Store(true)
			}
		}
		close(entered)
		s.mu.Unlock()
		<-release
	} else {
		s.mu.Unlock()
	}
	result, err := s.target.TransitionTestFromShakeout(transition)
	if err == nil && (result == storage.TestStatusTransitionUpdated || result == storage.TestStatusTransitionUnchanged) {
		if responseErr := s.afterPassiveCommit("outcome"); responseErr != nil {
			return storage.TestStatusTransitionFenced, responseErr
		}
	}
	return result, err
}

func (s *blockingStartCommitStore) StartAdmissionRevision() (int64, error) {
	return s.target.StartAdmissionRevision()
}

func newBlockingStartCommitRunner(t *testing.T, maxTests int) (*TestRunner, *storage.SQLiteStore, *blockingStartCommitStore) {
	t.Helper()
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "start-commit.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Close() })
	wrapped := &blockingStartCommitStore{Store: base, target: base}
	runner := NewTestRunner(maxTests, NewHub(), base)
	wrapped.runner = runner
	return runner, base, wrapped
}

func assertRunnerLockAvailableDuringStartCommit(t *testing.T, runner *TestRunner, base *storage.SQLiteStore, testID, localExpected, durableBefore string) {
	t.Helper()
	lockAcquired := make(chan struct{})
	go func() {
		runner.mu.Lock()
		close(lockAcquired)
		runner.mu.Unlock()
	}()
	select {
	case <-lockAcquired:
	case <-time.After(time.Second):
		t.Fatal("start commit held the global runner mutex")
	}
	runner.mu.RLock()
	local := runner.tests[testID]
	localStatus := ""
	if local != nil {
		localStatus = local.Status
	}
	runner.mu.RUnlock()
	if localStatus != localExpected {
		t.Fatalf("local staged status = %q, want %q", localStatus, localExpected)
	}
	durable, err := base.GetTest(testID)
	if err != nil {
		t.Fatal(err)
	}
	if durable.Status != durableBefore {
		t.Fatalf("durable status before commit = %q, want %q", durable.Status, durableBefore)
	}
}

func TestExecutionCapableStartCommitsDoNotHoldRunnerLock(t *testing.T) {
	for _, shakeout := range []bool{false, true} {
		name := "initializing"
		if shakeout {
			name = "shakeout"
		}
		t.Run(name, func(t *testing.T) {
			runner, base, wrapped := newBlockingStartCommitRunner(t, 2)
			backend := &executionBackendSpy{called: make(chan string, 1)}
			runner.setExecutionBackend(backend)
			protocolName := "http"
			if shakeout {
				protocolName = registerRunnerTestProtocol(t, "start-commit-shakeout", func() protocol.Protocol {
					return &shakeoutActionProtocol{name: "start-commit-shakeout", execute: func(context.Context) error { return nil }}
				})
			}
			runner.StoreScript(&dsl.Script{
				ID: "start-commit-script", Name: "Start commit", Version: "1.0.0", Protocol: protocolName,
				Config:  dsl.ScriptConfig{BaseURL: "https://example.test"},
				Actions: []dsl.Action{{Name: "action", Type: "request", Target: "/"}},
			})
			created, err := runner.CreateTest(CreateTestRequest{
				Name: "start commit", ScriptID: "start-commit-script", VUsers: 1, Duration: "1m", ScriptShakeout: shakeout,
			})
			if err != nil {
				t.Fatal(err)
			}
			entered, release := wrapped.blockNextStartCommit()
			startDone := make(chan error, 1)
			go func() { startDone <- runner.StartTest(created.ID) }()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("start did not reach blocked durable commit")
			}
			assertRunnerLockAvailableDuringStartCommit(t, runner, base, created.ID, "STARTING", "QUEUED")
			close(release)
			select {
			case err := <-startDone:
				var shakeoutGate *ScriptShakeoutGateError
				if err != nil && !errors.As(err, &shakeoutGate) {
					t.Fatalf("StartTest: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("start did not finish after durable commit resumed")
			}
			if wrapped.calledWhileRunnerMu.Load() {
				t.Fatal("start persistence observed tr.mu held")
			}
		})
	}
}

func TestPendingPromotionStartCommitDoesNotHoldRunnerLock(t *testing.T) {
	runner, base, wrapped := newBlockingStartCommitRunner(t, 1)
	backend := &executionBackendSpy{called: make(chan string, 1)}
	runner.setExecutionBackend(backend)
	runner.StoreScript(&dsl.Script{
		ID: "pending-start-commit-script", Name: "Pending start commit", Version: "1.0.0", Protocol: "http",
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "action", Type: "request", Target: "/"}},
	})
	holder, err := runner.CreateTest(CreateTestRequest{Name: "holder", ScriptID: "pending-start-commit-script", VUsers: 1, Duration: "1m"})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := runner.CreateTest(CreateTestRequest{Name: "pending", ScriptID: "pending-start-commit-script", VUsers: 1, Duration: "1m"})
	if err != nil {
		t.Fatal(err)
	}
	runner.mu.Lock()
	runner.tests[holder.ID].Status = "RUNNING"
	runner.mu.Unlock()
	if err := runner.StartTest(pending.ID); err != nil {
		t.Fatalf("queue pending test: %v", err)
	}
	queued, _ := runner.GetTest(pending.ID)
	if queued.Status != "PENDING" {
		t.Fatalf("queued status = %q, want PENDING", queued.Status)
	}
	runner.mu.Lock()
	runner.tests[holder.ID].Status = "COMPLETE"
	runner.mu.Unlock()
	entered, release := wrapped.blockNextStartCommit()
	promoteDone := make(chan struct{})
	go func() {
		runner.promotePendingTests()
		close(promoteDone)
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("pending promotion did not reach blocked durable start commit")
	}
	assertRunnerLockAvailableDuringStartCommit(t, runner, base, pending.ID, "STARTING", "PENDING")
	close(release)
	select {
	case <-promoteDone:
	case <-time.After(5 * time.Second):
		t.Fatal("pending promotion did not finish")
	}
	if wrapped.calledWhileRunnerMu.Load() {
		t.Fatal("pending promotion persistence observed tr.mu held")
	}
	select {
	case id := <-backend.called:
		if id != pending.ID {
			t.Fatalf("backend started %q, want %q", id, pending.ID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("pending test was not dispatched after commit")
	}
}

func TestCompletedShakeoutOutcomeCommitDoesNotHoldRunnerLock(t *testing.T) {
	runner, base, wrapped := newBlockingStartCommitRunner(t, 2)
	backend := &executionBackendSpy{called: make(chan string, 1)}
	runner.setExecutionBackend(backend)
	protocolName := registerRunnerTestProtocol(t, "shakeout-outcome-commit", func() protocol.Protocol {
		return &shakeoutActionProtocol{name: "shakeout-outcome-commit", execute: func(context.Context) error { return nil }}
	})
	runner.StoreScript(&dsl.Script{
		ID: "shakeout-outcome-commit-script", Name: "Shakeout outcome commit", Version: "1.0.0", Protocol: protocolName,
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "one shot", Type: "request", Target: "/"}},
	})
	created, err := runner.CreateTest(CreateTestRequest{
		Name: "shakeout outcome commit", ScriptID: "shakeout-outcome-commit-script", VUsers: 1, Duration: "1m", ScriptShakeout: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	entered, release := wrapped.blockNextOutcomeCommit()
	startDone := make(chan error, 1)
	go func() { startDone <- runner.StartTest(created.ID) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("shakeout did not reach blocked outcome commit")
	}
	assertRunnerLockAvailableDuringStartCommit(t, runner, base, created.ID, "SHAKEOUT", "SHAKEOUT")
	runner.mu.RLock()
	localStatus := runner.tests[created.ID].Status
	runner.mu.RUnlock()
	if localStatus != "SHAKEOUT" {
		t.Fatalf("uncommitted shakeout outcome was exposed as %q", localStatus)
	}
	if retryErr := runner.resumePendingScriptShakeoutOutcome(created.ID, nil); !errors.Is(retryErr, errStartAdmissionEvidenceStale) {
		t.Fatalf("concurrent outcome recovery error = %v, want operation-token fence", retryErr)
	}
	close(release)
	select {
	case err := <-startDone:
		if err != nil {
			t.Fatalf("StartTest: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("start did not finish after outcome commit resumed")
	}
	if wrapped.calledWhileRunnerMu.Load() {
		t.Fatal("shakeout outcome persistence observed tr.mu held")
	}
}

func TestStagedStartCommitDoesNotPublishDisplacedRuntime(t *testing.T) {
	runner, base, wrapped := newBlockingStartCommitRunner(t, 2)
	backend := &executionBackendSpy{called: make(chan string, 1)}
	runner.setExecutionBackend(backend)
	runner.StoreScript(&dsl.Script{
		ID: "displaced-start-script", Name: "Displaced start", Version: "1.0.0", Protocol: "http",
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "action", Type: "request", Target: "/"}},
	})
	created, err := runner.CreateTest(CreateTestRequest{
		Name: "displaced start", ScriptID: "displaced-start-script", VUsers: 1, Duration: "1m",
	})
	if err != nil {
		t.Fatal(err)
	}
	entered, release := wrapped.blockNextStartCommit()
	startDone := make(chan error, 1)
	go func() { startDone <- runner.StartTest(created.ID) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("start did not reach blocked durable commit")
	}
	runner.mu.RLock()
	displaced := runner.tests[created.ID]
	runner.mu.RUnlock()
	replacement := *created
	replacement.Status = "INITIALIZING"
	replacement.executionCtx = nil
	replacement.cancel = nil
	replacement.executionBackend = nil
	replacement.passiveOperationToken = ""
	runner.mu.Lock()
	runner.tests[created.ID] = &replacement
	runner.mu.Unlock()
	close(release)
	select {
	case startErr := <-startDone:
		if startErr == nil {
			t.Fatal("displaced runtime was treated as a successful local start")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("displaced start did not return")
	}
	select {
	case id := <-backend.called:
		t.Fatalf("displaced runtime dispatched backend for %q", id)
	case <-time.After(100 * time.Millisecond):
	}
	runner.mu.RLock()
	current := runner.tests[created.ID]
	runner.mu.RUnlock()
	if current != &replacement || current.Status != "INITIALIZING" {
		t.Fatalf("replacement runtime was rolled back or replaced: %#v", current)
	}
	if displaced == nil || displaced.cancel != nil || displaced.executionCtx != nil || displaced.passiveOperationToken != "" {
		t.Fatalf("displaced runtime retained an uncontrollable execution context: %#v", displaced)
	}
	durable, err := base.GetTest(created.ID)
	if err != nil || durable.Status != "INITIALIZING" {
		t.Fatalf("durable committed state = %#v, %v", durable, err)
	}
}

func TestDeleteCannotCrossStagedStartCommit(t *testing.T) {
	runner, base, wrapped := newBlockingStartCommitRunner(t, 2)
	backend := &executionBackendSpy{called: make(chan string, 1)}
	runner.setExecutionBackend(backend)
	runner.StoreScript(&dsl.Script{
		ID: "delete-staged-start-script", Name: "Delete staged start", Version: "1.0.0", Protocol: "http",
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "action", Type: "request", Target: "/"}},
	})
	created, err := runner.CreateTest(CreateTestRequest{
		Name: "delete staged start", ScriptID: "delete-staged-start-script", VUsers: 1, Duration: "1m",
	})
	if err != nil {
		t.Fatal(err)
	}
	entered, release := wrapped.blockNextStartCommit()
	startDone := make(chan error, 1)
	go func() { startDone <- runner.StartTest(created.ID) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("start did not reach blocked durable commit")
	}
	deleted, deleteErr := runner.DeleteTestWithError(created.ID)
	if deleteErr != nil || deleted {
		t.Fatalf("delete during staged start = (%t, %v), want blocked", deleted, deleteErr)
	}
	durable, err := base.GetTest(created.ID)
	if err != nil || durable.Status != "QUEUED" {
		t.Fatalf("blocked delete changed pre-commit row = %#v, %v", durable, err)
	}
	close(release)
	select {
	case err := <-startDone:
		if err != nil {
			t.Fatalf("StartTest: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("start did not finish after commit resumed")
	}
	select {
	case startedID := <-backend.called:
		if startedID != created.ID {
			t.Fatalf("backend started %q, want %q", startedID, created.ID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("staged start was not dispatched after delete was blocked")
	}
}

func TestStartIdentityFenceReloadsDurableReplacement(t *testing.T) {
	runner, base, wrapped := newBlockingStartCommitRunner(t, 2)
	backend := &executionBackendSpy{called: make(chan string, 1)}
	runner.setExecutionBackend(backend)
	runner.StoreScript(&dsl.Script{
		ID: "start-replacement-script", Name: "Start replacement", Version: "1.0.0", Protocol: "http",
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "action", Type: "request", Target: "/"}},
	})
	created, err := runner.CreateTest(CreateTestRequest{
		Name: "stale start", ScriptID: "start-replacement-script", VUsers: 1, Duration: "1m",
	})
	if err != nil {
		t.Fatal(err)
	}
	entered, release := wrapped.blockNextStartCommit()
	startDone := make(chan error, 1)
	go func() { startDone <- runner.StartTest(created.ID) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("start did not reach blocked durable commit")
	}
	original, err := base.GetTest(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := base.DeleteTest(created.ID); err != nil {
		t.Fatalf("DeleteTest(original): %v", err)
	}
	replacement := *original
	replacement.RefKey = original.RefKey + "-REPLACED"
	replacement.Name = "durable replacement"
	replacement.Status = "QUEUED"
	replacement.StartTime = original.StartTime.Add(time.Second)
	if err := base.SaveTest(&replacement); err != nil {
		t.Fatalf("SaveTest(replacement): %v", err)
	}
	close(release)
	select {
	case startErr := <-startDone:
		if !errors.Is(startErr, storage.ErrTestStartIdentityChanged) {
			t.Fatalf("stale start error = %v, want ErrTestStartIdentityChanged", startErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stale start did not return after replacement")
	}
	local, ok := runner.GetTest(created.ID)
	if !ok || local.RefKey != replacement.RefKey || local.Name != replacement.Name || local.Status != "QUEUED" || !local.CreatedAt.Equal(replacement.StartTime) {
		t.Fatalf("leader cache did not converge to durable replacement: %#v", local)
	}
	durable, err := base.GetTest(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if durable.RefKey != replacement.RefKey || durable.Status != "QUEUED" || !durable.StartTime.Equal(replacement.StartTime) {
		t.Fatalf("stale start overwrote durable replacement: %#v", durable)
	}
	select {
	case startedID := <-backend.called:
		t.Fatalf("stale start dispatched backend for %q", startedID)
	default:
	}
}

func TestStagedStartCommitDoesNotDispatchAfterShutdownBegins(t *testing.T) {
	runner, base, wrapped := newBlockingStartCommitRunner(t, 2)
	backend := &executionBackendSpy{called: make(chan string, 1)}
	runner.setExecutionBackend(backend)
	runner.StoreScript(&dsl.Script{
		ID: "shutdown-start-script", Name: "Shutdown start", Version: "1.0.0", Protocol: "http",
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "action", Type: "request", Target: "/"}},
	})
	created, err := runner.CreateTest(CreateTestRequest{Name: "shutdown start", ScriptID: "shutdown-start-script", VUsers: 1, Duration: "1m"})
	if err != nil {
		t.Fatal(err)
	}
	entered, release := wrapped.blockNextStartCommit()
	startDone := make(chan error, 1)
	go func() { startDone <- runner.StartTest(created.ID) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("start did not reach blocked durable commit")
	}
	runner.mu.Lock()
	runner.shuttingDown = true
	if staged := runner.tests[created.ID]; staged != nil && staged.cancel != nil {
		staged.cancel()
	}
	runner.mu.Unlock()
	close(release)
	select {
	case startErr := <-startDone:
		if !errors.Is(startErr, errRunnerShuttingDown) {
			t.Fatalf("StartTest error = %v, want shutdown fence", startErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("start did not return after shutdown")
	}
	select {
	case id := <-backend.called:
		t.Fatalf("shutdown-displaced start dispatched backend for %q", id)
	case <-time.After(100 * time.Millisecond):
	}
	durable, err := base.GetTest(created.ID)
	if err != nil || durable.Status != "INITIALIZING" {
		t.Fatalf("durable committed state = %#v, %v", durable, err)
	}
	runner.mu.RLock()
	retired := runner.tests[created.ID]
	runner.mu.RUnlock()
	if retired == nil || retired.Status != "INITIALIZING" || retired.passiveOperationToken != "" || retired.executionCtx != nil || retired.cancel != nil {
		t.Fatalf("shutdown left a non-reloadable staged runtime: %#v", retired)
	}
}

func TestCommittedStagedStartIsReloadableAfterSameProcessLeaseReacquisition(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "start-reacquire.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Close() })
	leadership := newExecutionLeadership(base, "start-reacquire")
	runner := NewTestRunner(2, NewHub(), base, WithExecutionLeadership(leadership))
	wrapped := &blockingStartCommitStore{Store: base, target: base, runner: runner}
	backend := &executionBackendSpy{called: make(chan string, 1)}
	runner.setExecutionBackend(backend)
	leadership.tick()
	if !leadership.IsAdmitted() {
		t.Fatal("initial execution term was not admitted")
	}
	t.Cleanup(leadership.relinquish)
	runner.StoreScript(&dsl.Script{
		ID: "reacquire-start-script", Name: "Reacquire start", Version: "1.0.0", Protocol: "http",
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "action", Type: "request", Target: "/"}},
	})
	created, err := runner.CreateTest(CreateTestRequest{Name: "reacquire start", ScriptID: "reacquire-start-script", VUsers: 1, Duration: "1m"})
	if err != nil {
		t.Fatal(err)
	}
	entered, release := wrapped.blockNextCommittedStartReturn()
	startDone := make(chan error, 1)
	go func() { startDone <- runner.StartTest(created.ID) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("start did not reach committed-response barrier")
	}
	durable, err := base.GetTest(created.ID)
	if err != nil || durable.Status != "INITIALIZING" {
		t.Fatalf("durable state before term handoff = %#v, %v", durable, err)
	}
	leadership.relinquish()
	leadership.tick()
	if !leadership.IsAdmitted() {
		t.Fatal("same process did not reacquire execution leadership")
	}
	// Acquisition can race the old callback. This first refresh deliberately
	// observes the owned STARTING token and skips it, reproducing the ordering
	// that previously stranded the row forever.
	if err := runner.reloadStoredTests(); err != nil {
		t.Fatalf("reload while old start callback is blocked: %v", err)
	}
	runner.testStore = base
	close(release)
	select {
	case startErr := <-startDone:
		if !errors.Is(startErr, errNotExecutionLeader) {
			t.Fatalf("old start error = %v, want execution-term fence", startErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("old start callback did not return after term handoff")
	}
	select {
	case id := <-backend.called:
		t.Fatalf("old execution term dispatched backend for %q", id)
	case <-time.After(100 * time.Millisecond):
	}
	runner.mu.RLock()
	retired := runner.tests[created.ID]
	runner.mu.RUnlock()
	if retired == nil || retired.Status == "STARTING" || retired.passiveOperationToken != "" || retired.executionCtx != nil || retired.cancel != nil {
		t.Fatalf("old term left a non-reloadable staged runtime: %#v", retired)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		durable, err = base.GetTest(created.ID)
		if err == nil && durable.Status != "STARTING" && durable.Status != "INITIALIZING" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	runner.mu.RLock()
	recovered := runner.tests[created.ID]
	runner.mu.RUnlock()
	if recovered == nil || recovered.Status == "STARTING" || recovered.passiveOperationToken != "" {
		t.Fatalf("successor recovery retained staged ownership: %#v", recovered)
	}
	durable, err = base.GetTest(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if durable.Status == "STARTING" || durable.Status == "INITIALIZING" {
		t.Fatalf("successor recovery left non-executable durable state %q", durable.Status)
	}
}

package api

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func newPassiveTransitionTestRunner(t *testing.T, name string) (*TestRunner, *storage.SQLiteStore, *ManagedTest) {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), name+".db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	runner := NewTestRunner(2, NewHub(), store)
	runner.StoreScript(&dsl.Script{
		ID: name + "-script", Name: name, Version: "1.0.0", Protocol: "http",
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
	})
	created, err := runner.CreateTest(CreateTestRequest{Name: name, ScriptID: name + "-script", VUsers: 1, Duration: "1m"})
	if err != nil {
		t.Fatal(err)
	}
	runner.mu.RLock()
	internal := runner.tests[created.ID]
	runner.mu.RUnlock()
	if internal == nil {
		t.Fatal("created test is missing from runner")
	}
	return runner, store, internal
}

func persistLocalShakeoutFixture(t *testing.T, runner *TestRunner, store *storage.SQLiteStore, test *ManagedTest) {
	t.Helper()
	runner.mu.Lock()
	test.Status = "SHAKEOUT"
	test.executionCtx, test.cancel = context.WithCancel(context.Background())
	record := buildManagedTestRecord(test)
	runner.mu.Unlock()
	if err := store.SaveTest(record); err != nil {
		t.Fatal(err)
	}
}

func TestBlockedShakeoutOutcomeAndAbortHaveSingleDurableWinner(t *testing.T) {
	t.Run("outcome then abort retry", func(t *testing.T) {
		runner, store, test := newPassiveTransitionTestRunner(t, "outcome-wins")
		persistLocalShakeoutFixture(t, runner, store, test)
		wrapped := &blockingStartCommitStore{Store: store, target: store, runner: runner}
		entered, release := wrapped.blockNextOutcomeCommit()
		outcomeDone := make(chan error, 1)
		go func() {
			outcomeDone <- runner.markScriptShakeoutPassedWithExpectedTermAndFingerprint(test.ID, ScriptShakeoutReport{
				Passed: true, StartedAt: time.Now().Add(-time.Second).UTC(), EndedAt: time.Now().UTC(),
			}, scriptShakeoutInputFingerprint{EvidenceSHA256: "evidence", ProcessSHA256: "process", ReusableAfterRestart: true}, nil)
		}()
		select {
		case <-entered:
		case err := <-outcomeDone:
			t.Fatalf("shakeout outcome returned before CAS barrier: %v", err)
		case <-time.After(3 * time.Second):
			t.Fatal("shakeout outcome did not reach blocked CAS")
		}
		if abortErr := runner.AbortCIRun(test.ID, "cancel during outcome"); !errors.Is(abortErr, errStartAdmissionEvidenceStale) {
			t.Fatalf("concurrent abort error = %v, want passive-operation fence", abortErr)
		}
		close(release)
		select {
		case err := <-outcomeDone:
			if err != nil {
				t.Fatalf("shakeout outcome: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("shakeout outcome did not finish")
		}
		if err := runner.AbortCIRun(test.ID, "retry after outcome"); err != nil {
			t.Fatalf("abort retry: %v", err)
		}
		runner.mu.RLock()
		local := runner.tests[test.ID]
		runner.mu.RUnlock()
		durable, err := store.GetTest(test.ID)
		if err != nil {
			t.Fatal(err)
		}
		if local == nil || local.Status != "ABORTED" || local.passiveOperationToken != "" || durable.Status != "ABORTED" {
			t.Fatalf("abort retry did not converge: local=%#v durable=%#v", local, durable)
		}
	})

	t.Run("abort before outcome", func(t *testing.T) {
		runner, store, test := newPassiveTransitionTestRunner(t, "abort-wins")
		persistLocalShakeoutFixture(t, runner, store, test)
		if err := runner.AbortCIRun(test.ID, "cancel first"); err != nil {
			t.Fatalf("AbortCIRun: %v", err)
		}
		err := runner.markScriptShakeoutPassedWithExpectedTermAndFingerprint(test.ID, ScriptShakeoutReport{
			Passed: true, StartedAt: time.Now().Add(-time.Second).UTC(), EndedAt: time.Now().UTC(),
		}, scriptShakeoutInputFingerprint{EvidenceSHA256: "evidence", ProcessSHA256: "process", ReusableAfterRestart: true}, nil)
		if err == nil || !strings.Contains(err.Error(), "ABORTED") {
			t.Fatalf("late shakeout outcome error = %v, want ABORTED fence", err)
		}
		durable, getErr := store.GetTest(test.ID)
		if getErr != nil || durable.Status != "ABORTED" {
			t.Fatalf("late outcome resurrected durable row: %#v, %v", durable, getErr)
		}
	})
}

func TestBlockedPendingTransitionFencesAbortUntilCASCompletes(t *testing.T) {
	runner, store, test := newPassiveTransitionTestRunner(t, "pending-abort")
	wrapper := &blockingPendingTransitionStore{
		Store:   store,
		target:  store,
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	runner.testStore = wrapper
	pendingDone := make(chan error, 1)
	go func() {
		runner.mu.Lock()
		pendingDone <- runner.markTestPendingLocked(test, "capacity unavailable")
		runner.mu.Unlock()
	}()
	select {
	case <-wrapper.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("pending transition did not reach blocked CAS")
	}
	if abortErr := runner.AbortCIRun(test.ID, "cancel during pending"); !errors.Is(abortErr, errStartAdmissionEvidenceStale) {
		t.Fatalf("concurrent abort error = %v, want passive-operation fence", abortErr)
	}
	close(wrapper.release)
	select {
	case err := <-pendingDone:
		if err != nil {
			t.Fatalf("pending transition: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pending transition did not finish")
	}
	if err := runner.AbortCIRun(test.ID, "retry after pending"); err != nil {
		t.Fatalf("abort retry: %v", err)
	}
	runner.mu.RLock()
	local := runner.tests[test.ID]
	runner.mu.RUnlock()
	durable, err := store.GetTest(test.ID)
	if err != nil {
		t.Fatal(err)
	}
	if local == nil || local.Status != "ABORTED" || local.passiveOperationToken != "" || durable.Status != "ABORTED" {
		t.Fatalf("pending/abort did not converge: local=%#v durable=%#v", local, durable)
	}
}

func newHAPassiveTransitionRunner(t *testing.T, name string, shakeout bool) (*TestRunner, *storage.SQLiteStore, *blockingStartCommitStore, *executionLeadership, *ManagedTest, executionLeaseTerm, *executionBackendSpy) {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), name+"-ha.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	leadership := newExecutionLeadership(store, name)
	backend := &executionBackendSpy{called: make(chan string, 2)}
	runner := NewTestRunner(2, NewHub(), store, WithExecutionLeadership(leadership), WithExecutionBackend(backend))
	leadership.tick()
	if !leadership.IsAdmitted() {
		t.Fatal("initial execution term was not admitted")
	}
	t.Cleanup(leadership.relinquish)
	runner.StoreScript(&dsl.Script{
		ID: name + "-script", Name: name, Version: "1.0.0", Protocol: "http",
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
	})
	created, err := runner.CreateTest(CreateTestRequest{
		Name: name, ScriptID: name + "-script", VUsers: 1, Duration: "1m", ScriptShakeout: shakeout,
	})
	if err != nil {
		t.Fatal(err)
	}
	runner.mu.RLock()
	internal := runner.tests[created.ID]
	runner.mu.RUnlock()
	if internal == nil {
		t.Fatal("created test is missing from HA runner")
	}
	term, err := runner.captureExecutionLeaseTerm()
	if err != nil {
		t.Fatal(err)
	}
	wrapper := &blockingStartCommitStore{Store: store, target: store, runner: runner}
	return runner, store, wrapper, leadership, internal, term, backend
}

func handoffWhilePassiveReturnIsBlocked(t *testing.T, runner *TestRunner, store *storage.SQLiteStore, leadership *executionLeadership) {
	t.Helper()
	leadership.relinquish()
	leadership.tick()
	if !leadership.IsAdmitted() {
		t.Fatal("same process did not reacquire execution leadership")
	}
	// Model the acquire callback winning the race and completing its scan while
	// the old callback still owns the passive token. No second manual reload is
	// issued after the old callback retires; production recovery must self-arm.
	if err := runner.reloadStoredTests(); err != nil {
		t.Fatalf("acquisition scan while passive callback is blocked: %v", err)
	}
	// The transition call is already inside the wrapper. Restore production
	// persistence before releasing its response so successor recovery exercises
	// the real store's complete fencing surface.
	runner.testStore = store
}

func waitForRecoveredBackendStart(t *testing.T, runner *TestRunner, store *storage.SQLiteStore, backend *executionBackendSpy, testID string) {
	t.Helper()
	select {
	case startedID := <-backend.called:
		if startedID != testID {
			t.Fatalf("recovered backend started %q, want %q", startedID, testID)
		}
	case <-time.After(5 * time.Second):
		runner.mu.RLock()
		local := runner.tests[testID]
		runner.mu.RUnlock()
		durable, _ := store.GetTest(testID)
		t.Fatalf("successor term did not resume the skipped passive transition: local=%#v durable=%#v", local, durable)
	}
}

func TestOldTermShakeoutOutcomeSelfRecoversAfterAcquireScanSkippedToken(t *testing.T) {
	runner, store, wrapper, leadership, test, oldTerm, backend := newHAPassiveTransitionRunner(t, "outcome-handoff", true)
	runner.mu.Lock()
	test.Status = "SHAKEOUT"
	test.executionFence = oldTerm.ownerID
	test.executionGeneration = oldTerm.generation
	test.executionCtx, test.cancel = context.WithCancel(context.Background())
	record := buildManagedTestRecord(test)
	runner.mu.Unlock()
	if err := store.SaveTest(record); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := runner.scriptShakeoutExecutableFingerprint(test.ID)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := wrapper.blockNextCommittedPassiveReturn("outcome")
	outcomeDone := make(chan error, 1)
	go func() {
		outcomeDone <- runner.markScriptShakeoutPassedWithExpectedTermAndFingerprint(test.ID, ScriptShakeoutReport{
			Passed: true, StartedAt: time.Now().Add(-time.Second).UTC(), EndedAt: time.Now().UTC(),
		}, fingerprint, &oldTerm)
	}()
	select {
	case <-entered:
	case err := <-outcomeDone:
		t.Fatalf("outcome returned before committed-response barrier: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("outcome did not reach committed-response barrier")
	}
	durable, err := store.GetTest(test.ID)
	if err != nil || durable.Status != "QUEUED" {
		t.Fatalf("committed outcome state = %#v, %v", durable, err)
	}
	handoffWhilePassiveReturnIsBlocked(t, runner, store, leadership)
	close(release)
	select {
	case err := <-outcomeDone:
		if err != nil {
			t.Fatalf("old-term outcome callback: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("old-term outcome callback did not return")
	}
	waitForRecoveredBackendStart(t, runner, store, backend, test.ID)
	if err := runner.AbortCIRun(test.ID, "test cleanup"); err != nil {
		t.Fatalf("cleanup abort: %v", err)
	}
}

func TestOldTermPendingTransitionSelfPromotesAfterAcquireScanSkippedToken(t *testing.T) {
	runner, store, wrapper, leadership, test, oldTerm, backend := newHAPassiveTransitionRunner(t, "pending-handoff", false)
	entered, release := wrapper.blockNextCommittedPassiveReturn("pending")
	pendingDone := make(chan error, 1)
	go func() {
		runner.mu.Lock()
		pendingDone <- runner.markTestPendingLockedWithExpectedTerm(test, "capacity unavailable", &oldTerm)
		runner.mu.Unlock()
	}()
	select {
	case <-entered:
	case err := <-pendingDone:
		t.Fatalf("pending transition returned before committed-response barrier: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("pending transition did not reach committed-response barrier")
	}
	durable, err := store.GetTest(test.ID)
	if err != nil || durable.Status != "PENDING" {
		t.Fatalf("committed pending state = %#v, %v", durable, err)
	}
	handoffWhilePassiveReturnIsBlocked(t, runner, store, leadership)
	close(release)
	select {
	case err := <-pendingDone:
		if err != nil {
			t.Fatalf("old-term pending callback: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("old-term pending callback did not return")
	}
	waitForRecoveredBackendStart(t, runner, store, backend, test.ID)
	if err := runner.AbortCIRun(test.ID, "test cleanup"); err != nil {
		t.Fatalf("cleanup abort: %v", err)
	}
}

func TestLostShakeoutOutcomeResponseAfterTermHandoffSelfRecovers(t *testing.T) {
	runner, store, wrapper, leadership, test, oldTerm, backend := newHAPassiveTransitionRunner(t, "outcome-lost-handoff", true)
	runner.mu.Lock()
	test.Status = "SHAKEOUT"
	test.executionFence = oldTerm.ownerID
	test.executionGeneration = oldTerm.generation
	test.executionCtx, test.cancel = context.WithCancel(context.Background())
	record := buildManagedTestRecord(test)
	runner.mu.Unlock()
	if err := store.SaveTest(record); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := runner.scriptShakeoutExecutableFingerprint(test.ID)
	if err != nil {
		t.Fatal(err)
	}
	lostResponse := errors.New("committed shakeout outcome response was lost")
	entered, release := wrapper.blockNextCommittedPassiveLostResponse("outcome", lostResponse)
	outcomeDone := make(chan error, 1)
	go func() {
		outcomeDone <- runner.markScriptShakeoutPassedWithExpectedTermAndFingerprint(test.ID, ScriptShakeoutReport{
			Passed: true, StartedAt: time.Now().Add(-time.Second).UTC(), EndedAt: time.Now().UTC(),
		}, fingerprint, &oldTerm)
	}()
	select {
	case <-entered:
	case err := <-outcomeDone:
		t.Fatalf("outcome returned before lost-response barrier: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("outcome did not reach lost-response barrier")
	}
	durable, err := store.GetTest(test.ID)
	if err != nil || durable.Status != "QUEUED" {
		t.Fatalf("committed outcome state = %#v, %v", durable, err)
	}
	handoffWhilePassiveReturnIsBlocked(t, runner, store, leadership)
	close(release)
	select {
	case err := <-outcomeDone:
		if !errors.Is(err, errStartAdmissionEvidenceStale) {
			t.Fatalf("old-term lost outcome response = %v, want stale classification", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("old-term outcome callback did not return")
	}
	waitForRecoveredBackendStart(t, runner, store, backend, test.ID)
	if err := runner.AbortCIRun(test.ID, "test cleanup"); err != nil {
		t.Fatalf("cleanup abort: %v", err)
	}
}

func TestLostShakeoutReviewResponseAfterTermHandoffTerminalizesCIWithoutReplay(t *testing.T) {
	runner, store, wrapper, leadership, test, oldTerm, backend := newHAPassiveTransitionRunner(t, "review-lost-handoff", true)
	metadata, err := encodeCIRunMetadata(ciRunMetadata{
		SchemaVersion:  ciRunSchemaVersion,
		Source:         ciTestRunSource("review-lost-handoff"),
		ScriptIdentity: "script_id:" + test.ScriptID,
		IdempotencySHA: sha256String("review-lost-handoff"),
		PublishCheck:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	runner.mu.Lock()
	test.MaterializationKey = metadata
	test.Status = "SHAKEOUT"
	test.executionFence = oldTerm.ownerID
	test.executionGeneration = oldTerm.generation
	test.executionCtx, test.cancel = context.WithCancel(context.Background())
	record := buildManagedTestRecord(test)
	runner.mu.Unlock()
	if err := store.SaveTest(record); err != nil {
		t.Fatal(err)
	}

	lostResponse := errors.New("committed shakeout review response was lost")
	entered, release := wrapper.blockNextCommittedPassiveLostResponse("outcome", lostResponse)
	outcomeDone := make(chan error, 1)
	go func() {
		outcomeDone <- runner.markScriptShakeoutReviewWithExpectedTerm(test.ID, ScriptShakeoutReport{
			Passed: false, Summary: "authored dependency could not be represented",
			StartedAt: time.Now().Add(-time.Second).UTC(), EndedAt: time.Now().UTC(),
		}, &oldTerm)
	}()
	select {
	case <-entered:
	case err := <-outcomeDone:
		t.Fatalf("review outcome returned before lost-response barrier: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("review outcome did not reach lost-response barrier")
	}
	durable, err := store.GetTest(test.ID)
	if err != nil || durable.Status != "SHAKEOUT_REVIEW" {
		t.Fatalf("committed review state = %#v, %v", durable, err)
	}
	handoffWhilePassiveReturnIsBlocked(t, runner, store, leadership)
	close(release)
	select {
	case err := <-outcomeDone:
		if !errors.Is(err, errStartAdmissionEvidenceStale) {
			t.Fatalf("old-term lost review response = %v, want stale classification", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("old-term review callback did not return")
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		durable, err = store.GetTest(test.ID)
		if err != nil {
			t.Fatal(err)
		}
		if durable.Status == "FAILED" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("successor term left CI review in %q", durable.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case startedID := <-backend.called:
		t.Fatalf("successor recovery replayed or launched rejected CI test %q", startedID)
	default:
	}
}

func TestLostPendingResponseAfterTermHandoffSelfPromotes(t *testing.T) {
	runner, store, wrapper, leadership, test, oldTerm, backend := newHAPassiveTransitionRunner(t, "pending-lost-handoff", false)
	lostResponse := errors.New("committed pending response was lost")
	entered, release := wrapper.blockNextCommittedPassiveLostResponse("pending", lostResponse)
	pendingDone := make(chan error, 1)
	go func() {
		runner.mu.Lock()
		pendingDone <- runner.markTestPendingLockedWithExpectedTerm(test, "capacity unavailable", &oldTerm)
		runner.mu.Unlock()
	}()
	select {
	case <-entered:
	case err := <-pendingDone:
		t.Fatalf("pending transition returned before lost-response barrier: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("pending transition did not reach lost-response barrier")
	}
	durable, err := store.GetTest(test.ID)
	if err != nil || durable.Status != "PENDING" {
		t.Fatalf("committed pending state = %#v, %v", durable, err)
	}
	handoffWhilePassiveReturnIsBlocked(t, runner, store, leadership)
	close(release)
	select {
	case err := <-pendingDone:
		if !errors.Is(err, errStartAdmissionEvidenceStale) || !errors.Is(err, lostResponse) {
			t.Fatalf("old-term lost pending response = %v, want stale plus injected loss", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("old-term pending callback did not return")
	}
	waitForRecoveredBackendStart(t, runner, store, backend, test.ID)
	if err := runner.AbortCIRun(test.ID, "test cleanup"); err != nil {
		t.Fatalf("cleanup abort: %v", err)
	}
}

func TestOldPassiveTransitionRescanDoesNotStartSameIDReplacement(t *testing.T) {
	runner, store, _, leadership, test, oldTerm, backend := newHAPassiveTransitionRunner(t, "rescan-replacement", false)
	identity, ok := queuedStartRecoveryIdentityForTest(test)
	if !ok {
		t.Fatal("original identity was not capturable")
	}
	leadership.relinquish()
	leadership.tick()
	if !leadership.IsAdmitted() {
		t.Fatal("same process did not reacquire execution leadership")
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	runner.beforePassiveTransitionRescan = func(id string) {
		if id == test.ID {
			close(entered)
			<-release
		}
	}
	runner.mu.Lock()
	runner.schedulePassiveTransitionRescanAfterTermChangeLocked(test.ID, identity, oldTerm.ownerID, oldTerm.generation)
	runner.mu.Unlock()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("successor rescan did not reach replacement barrier")
	}

	durable, err := store.GetTest(test.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteTest(test.ID); err != nil {
		t.Fatal(err)
	}
	durable.Name = "same-ID replacement"
	durable.RefKey += "-replacement"
	durable.StartTime = durable.StartTime.Add(time.Microsecond)
	durable.Status = "QUEUED"
	durable.StartedAt = nil
	durable.EndTime = nil
	durable.Error = ""
	if err := store.SaveTest(durable); err != nil {
		t.Fatal(err)
	}
	close(release)

	deadline := time.Now().Add(3 * time.Second)
	for {
		runner.mu.RLock()
		current := runner.tests[test.ID]
		reloaded := current != nil && current.RefKey == durable.RefKey
		runner.mu.RUnlock()
		if reloaded {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("successor rescan did not load replacement")
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case startedID := <-backend.called:
		t.Fatalf("old passive transition rescan started replacement %q", startedID)
	case <-time.After(150 * time.Millisecond):
	}
}

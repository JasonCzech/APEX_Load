package api

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func openSharedExecutionPlanStores(t *testing.T) (*storage.SQLiteStore, *storage.SQLiteStore) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "execution-plan-term.db")
	oldStore, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(old): %v", err)
	}
	t.Cleanup(func() { _ = oldStore.Close() })
	newStore, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(new): %v", err)
	}
	t.Cleanup(func() { _ = newStore.Close() })
	return oldStore, newStore
}

func storeExecutionPlanTermScript(t *testing.T, runner *TestRunner, id, projectID string) {
	t.Helper()
	if _, err := runner.StoreScriptDurable(&dsl.Script{
		ID: id, ProjectID: projectID, Name: id, Protocol: "http", Version: "1.0.0",
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test", RunLogic: dsl.RunLogicConfig{Iterations: 1}},
		Actions: []dsl.Action{{Name: "Home", Type: "GET", Target: "/"}},
	}); err != nil {
		t.Fatalf("StoreScriptDurable(%s): %v", id, err)
	}
}

func executionPlanTermCreateRequest(name, scriptID, projectID string) CreateTestRequest {
	return CreateTestRequest{
		Name: name, Duration: "30s", ProjectID: projectID, ScriptID: scriptID, VUsers: 1, RampUp: "1s",
	}
}

func TestExecutionPlanOldGoroutineCannotAdoptSameProcessReacquiredTerm(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "same-process-term.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	leadership := newExecutionLeadership(store, "same-process-plan")
	leadership.tick()
	if !leadership.IsAdmitted() {
		t.Fatal("initial leadership acquisition was not admitted")
	}
	defer leadership.relinquish()

	backend := &executionBackendSpy{called: make(chan string, 4)}
	runner := NewTestRunner(2, NewHub(), store, WithExecutionLeadership(leadership), WithExecutionBackend(backend))
	const projectID = "same-process-project"
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{ID: projectID, Name: projectID, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	storeExecutionPlanTermScript(t, runner, "same-process-script", projectID)
	oldTerm, err := runner.captureExecutionLeaseTerm()
	if err != nil {
		t.Fatalf("capture old term: %v", err)
	}

	// Persist the sequential entry claim, including its deterministic test ID,
	// but stop before test creation. This is the exact former random-ID gap.
	runID := "exec-plan-run-same-process"
	claimedTestID := executionPlanEntryTestID(runID, 0, 0)
	anchor := &storage.ExecutionPlanRun{
		ID: runID, PlanID: "same-process-plan", ProjectID: projectID, RunMode: "sequential", Status: "running",
		Entries:   []storage.ExecutionPlanRunEntry{{Order: 0, ScenarioID: "scenario-a", Status: "pending", RunName: "same process"}},
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	created, err := runner.createExecutionPlanRunDurableWithTerm(anchor, oldTerm)
	if err != nil || !created {
		t.Fatalf("create plan-run anchor = (%v, %v)", created, err)
	}
	anchor.Entries[0].Status = "running"
	anchor.Entries[0].TestID = claimedTestID
	anchor.TransitionToken = ""
	if err := runner.persistExecutionPlanRunDurableWithTerm(anchor, oldTerm); err != nil {
		t.Fatalf("persist entry claim: %v", err)
	}

	// Also stage a second queued test so the post-commit/pre-start boundary is
	// covered independently from the post-claim/pre-create boundary.
	stagedTestID := "same-process-staged-before-start"
	request := executionPlanTermCreateRequest("staged before start", "same-process-script", projectID)
	staged, err := runner.createTestWithIDAndTerm(request, false, stagedTestID, oldTerm)
	if err != nil {
		t.Fatalf("stage test under old term: %v", err)
	}

	leadership.relinquish()
	leadership.tick()
	if !leadership.IsAdmitted() {
		t.Fatal("same process did not reacquire leadership")
	}
	newTerm, err := runner.captureExecutionLeaseTerm()
	if err != nil {
		t.Fatalf("capture successor term: %v", err)
	}
	if newTerm.ownerID == oldTerm.ownerID || newTerm.generation <= oldTerm.generation {
		t.Fatalf("reacquired term = %+v, old = %+v", newTerm, oldTerm)
	}

	if _, err := runner.createTestWithIDAndTerm(request, false, claimedTestID, oldTerm); !errors.Is(err, errNotExecutionLeader) {
		t.Fatalf("old-term create error = %v, want errNotExecutionLeader", err)
	}
	if _, err := store.GetTest(claimedTestID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("old-term create persisted claimed test: %v", err)
	}
	if err := runner.startExecutionPlanTestWithTerm(staged.ID, oldTerm); !errors.Is(err, errNotExecutionLeader) {
		t.Fatalf("old-term start error = %v, want errNotExecutionLeader", err)
	}
	select {
	case startedID := <-backend.called:
		t.Fatalf("old goroutine started %s after same-process reacquisition", startedID)
	case <-time.After(100 * time.Millisecond):
	}
	durableStaged, err := store.GetTest(staged.ID)
	if err != nil {
		t.Fatalf("GetTest(staged): %v", err)
	}
	if durableStaged.Status != "QUEUED" {
		t.Fatalf("old-term start changed staged test to %q", durableStaged.Status)
	}
}

func TestExecutionPlanFailureDoesNotMutateStaleReusedIDCache(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "materialization-cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	leadership := newExecutionLeadership(store, "materialization-cache-plan")
	leadership.tick()
	if !leadership.IsAdmitted() {
		t.Fatal("leadership acquisition was not admitted")
	}
	defer leadership.relinquish()

	const (
		testID   = "deterministic-reused-test-id"
		ownedKey = "plan-run/entry-owned"
	)
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := store.SaveTest(&storage.TestRecord{
		ID: testID, MaterializationKey: ownedKey, Name: "Durable owned entry", Status: "QUEUED",
		ProjectID: storage.DefaultProjectID, StartTime: now, Groups: []any{},
	}); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}
	runner := NewTestRunner(2, NewHub(), store, WithExecutionLeadership(leadership))
	term, err := runner.captureExecutionLeaseTerm()
	if err != nil {
		t.Fatalf("captureExecutionLeaseTerm: %v", err)
	}
	canceled := false
	stale := &ManagedTest{
		ID: testID, MaterializationKey: "another-plan/entry", Name: "Stale unrelated cache",
		Status: "RUNNING", ProjectID: "another-project", CreatedAt: now.Add(-time.Hour),
		cancel: func() { canceled = true },
	}
	runner.mu.Lock()
	runner.tests[testID] = stale
	runner.mu.Unlock()

	if err := runner.failExecutionPlanStagedTestWithTerm(testID, "owned cleanup", term, ownedKey); err != nil {
		t.Fatalf("failExecutionPlanStagedTestWithTerm: %v", err)
	}
	durable, err := store.GetTest(testID)
	if err != nil {
		t.Fatalf("GetTest: %v", err)
	}
	if durable.Status != "FAILED" || durable.Error != "owned cleanup" {
		t.Fatalf("owned durable materialization was not terminalized: %+v", durable)
	}
	runner.mu.RLock()
	retained := runner.tests[testID]
	runner.mu.RUnlock()
	if retained != stale || stale.Status != "RUNNING" || stale.Error != "" || canceled {
		t.Fatalf("stale unrelated cache was mutated: retained=%p stale=%p status=%q error=%q canceled=%v", retained, stale, stale.Status, stale.Error, canceled)
	}
}

func TestExecutionPlanExpectedTermStartRejectsReacquireBetweenPrecheckAndMutation(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "start-mutation-term.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	leadership := newExecutionLeadership(store, "start-mutation-plan")
	leadership.tick()
	if !leadership.IsAdmitted() {
		t.Fatal("initial leadership acquisition was not admitted")
	}
	defer leadership.relinquish()

	backend := &executionBackendSpy{called: make(chan string, 4)}
	runner := NewTestRunner(2, NewHub(), store, WithExecutionLeadership(leadership), WithExecutionBackend(backend))
	const projectID = "start-mutation-project"
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{ID: projectID, Name: projectID, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	storeExecutionPlanTermScript(t, runner, "start-mutation-script", projectID)
	oldTerm, err := runner.captureExecutionLeaseTerm()
	if err != nil {
		t.Fatalf("capture old term: %v", err)
	}
	staged, err := runner.createTestWithIDAndTerm(
		executionPlanTermCreateRequest("staged at start boundary", "start-mutation-script", projectID),
		false,
		"start-mutation-staged",
		oldTerm,
	)
	if err != nil {
		t.Fatalf("stage test under old term: %v", err)
	}

	precheckComplete := make(chan struct{})
	allowStartMutation := make(chan struct{})
	runner.beforeExecutionPlanStartMutation = func() {
		close(precheckComplete)
		<-allowStartMutation
	}
	startErr := make(chan error, 1)
	go func() {
		startErr <- runner.startExecutionPlanTestWithTerm(staged.ID, oldTerm)
	}()

	select {
	case <-precheckComplete:
	case <-time.After(2 * time.Second):
		t.Fatal("execution-plan start did not reach the precheck/start mutation barrier")
	}
	leadership.relinquish()
	leadership.tick()
	if !leadership.IsAdmitted() {
		t.Fatal("same process did not reacquire leadership at the start barrier")
	}
	newTerm, err := runner.captureExecutionLeaseTerm()
	if err != nil {
		t.Fatalf("capture successor term: %v", err)
	}
	if newTerm.ownerID == oldTerm.ownerID || newTerm.generation <= oldTerm.generation {
		t.Fatalf("reacquired term = %+v, old = %+v", newTerm, oldTerm)
	}
	close(allowStartMutation)

	select {
	case err := <-startErr:
		if !errors.Is(err, errNotExecutionLeader) {
			t.Fatalf("old-term start error = %v, want errNotExecutionLeader", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("old-term start remained blocked after releasing the barrier")
	}
	select {
	case startedID := <-backend.called:
		t.Fatalf("old goroutine launched %s after same-process reacquisition", startedID)
	case <-time.After(100 * time.Millisecond):
	}
	durableStaged, err := store.GetTest(staged.ID)
	if err != nil {
		t.Fatalf("GetTest(staged): %v", err)
	}
	if durableStaged.Status != "QUEUED" {
		t.Fatalf("old-term start changed staged test to %q", durableStaged.Status)
	}
}

func TestSequentialExecutionPlanRecoveryClosesClaimBeforeCreateCrashWindow(t *testing.T) {
	oldStore, newStore := openSharedExecutionPlanStores(t)
	const (
		oldOwner = "sequential-old/term"
		newOwner = "sequential-new/term"
	)
	now := time.Now().UTC()
	acquired, oldLease, err := oldStore.TryAcquireSchedulerLeaseTerm(executionLeaseName, oldOwner, now, time.Minute)
	if err != nil || !acquired || oldLease == nil {
		t.Fatalf("acquire old lease = (%v, %#v, %v)", acquired, oldLease, err)
	}
	plan := &storage.ExecutionPlan{ID: "sequential-crash-plan", ProjectID: "project-a", Name: "Sequential crash", RunMode: "sequential", CreatedAt: now, UpdatedAt: now}
	if saved, err := oldStore.SaveExecutionPlanIfLeaseTermHeld(plan, executionLeaseName, oldOwner, oldLease.Generation); err != nil || !saved {
		t.Fatalf("save plan = (%v, %v)", saved, err)
	}
	runID := "exec-plan-run-sequential-crash"
	testID := executionPlanEntryTestID(runID, 0, 7)
	run := &storage.ExecutionPlanRun{
		ID: runID, PlanID: plan.ID, ProjectID: plan.ProjectID, RunMode: "sequential", Status: "running",
		Entries: []storage.ExecutionPlanRunEntry{{
			Order: 7, ScenarioID: "scenario-a", TestID: testID, Status: "running", RunName: plan.Name,
		}},
		TransitionToken: "entry-claim", CreatedAt: now, UpdatedAt: now,
	}
	if created, err := oldStore.CreateExecutionPlanRunIfAbsentAndLeaseTermHeld(run, executionLeaseName, oldOwner, oldLease.Generation); err != nil || !created {
		t.Fatalf("persist claimed run = (%v, %v)", created, err)
	}
	if err := oldStore.ReleaseSchedulerLease(executionLeaseName, oldOwner); err != nil {
		t.Fatalf("release old lease: %v", err)
	}
	newLeadership := newExecutionLeadership(newStore, newOwner)
	newLeadership.tick()
	if !newLeadership.IsAdmitted() {
		t.Fatal("successor did not acquire leadership")
	}
	defer newLeadership.relinquish()
	backend := &executionBackendSpy{called: make(chan string, 2)}
	_ = NewTestRunner(2, NewHub(), newStore, WithExecutionLeadership(newLeadership), WithExecutionBackend(backend))

	recovered, err := newStore.GetExecutionPlanRun(runID)
	if err != nil {
		t.Fatalf("GetExecutionPlanRun: %v", err)
	}
	if recovered.Status != "halted" || recovered.CompletedAt == nil || len(recovered.Entries) != 1 ||
		recovered.Entries[0].Status != "failed" || recovered.Entries[0].TestID != testID {
		t.Fatalf("recovered claimed-before-create run = %+v", recovered)
	}
	if !strings.Contains(recovered.Entries[0].Detail, "missing test") {
		t.Fatalf("recovery detail = %q, want missing-test evidence", recovered.Entries[0].Detail)
	}
	if _, err := newStore.GetTest(testID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("recovery created an unanchored replacement test: %v", err)
	}
	select {
	case startedID := <-backend.called:
		t.Fatalf("recovery started duplicate test %s", startedID)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestParallelExecutionPlanRecoveryTerminalizesFirstStagedTestAfterCrash(t *testing.T) {
	oldStore, newStore := openSharedExecutionPlanStores(t)
	oldLeadership := newExecutionLeadership(oldStore, "parallel-old")
	oldLeadership.tick()
	if !oldLeadership.IsAdmitted() {
		t.Fatal("old controller did not acquire leadership")
	}
	oldRunner := NewTestRunner(2, NewHub(), oldStore, WithExecutionLeadership(oldLeadership))
	const projectID = "parallel-crash-project"
	now := time.Now().UTC()
	if err := oldStore.SaveProject(&storage.Project{ID: projectID, Name: projectID, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	storeExecutionPlanTermScript(t, oldRunner, "parallel-crash-script", projectID)
	oldTerm, err := oldRunner.captureExecutionLeaseTerm()
	if err != nil {
		t.Fatalf("capture old term: %v", err)
	}

	runID := "exec-plan-run-parallel-crash"
	firstTestID := executionPlanEntryTestID(runID, 0, 10)
	secondTestID := executionPlanEntryTestID(runID, 1, 20)
	firstMaterializationKey := executionPlanEntryMaterializationKey(runID, 0, 10)
	secondMaterializationKey := executionPlanEntryMaterializationKey(runID, 1, 20)
	anchor := &storage.ExecutionPlanRun{
		ID: runID, PlanID: "parallel-crash-plan", ProjectID: projectID, RunMode: "parallel", Status: "running",
		Entries: []storage.ExecutionPlanRunEntry{
			{Order: 10, ScenarioID: "scenario-a", TestID: firstTestID, Status: "pending", MaterializationKey: firstMaterializationKey},
			{Order: 20, ScenarioID: "scenario-b", TestID: secondTestID, Status: "pending", MaterializationKey: secondMaterializationKey},
		},
		TransitionToken: "parallel-anchor", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if created, err := oldRunner.createExecutionPlanRunDurableWithTerm(anchor, oldTerm); err != nil || !created {
		t.Fatalf("create parallel anchor = (%v, %v)", created, err)
	}
	request := executionPlanTermCreateRequest("first staged entry", "parallel-crash-script", projectID)
	request.MaterializationKey = firstMaterializationKey
	if test, err := oldRunner.createTestWithIDAndTerm(request, false, firstTestID, oldTerm); err != nil || test.ID != firstTestID {
		t.Fatalf("create first staged test = (%#v, %v)", test, err)
	}

	if err := oldStore.ReleaseSchedulerLease(executionLeaseName, oldTerm.ownerID); err != nil {
		t.Fatalf("release old lease: %v", err)
	}
	newLeadership := newExecutionLeadership(newStore, "parallel-new")
	newLeadership.tick()
	if !newLeadership.IsAdmitted() {
		t.Fatal("successor did not acquire leadership")
	}
	defer newLeadership.relinquish()
	backend := &executionBackendSpy{called: make(chan string, 2)}
	_ = NewTestRunner(2, NewHub(), newStore, WithExecutionLeadership(newLeadership), WithExecutionBackend(backend))

	recovered, err := newStore.GetExecutionPlanRun(runID)
	if err != nil {
		t.Fatalf("GetExecutionPlanRun: %v", err)
	}
	if recovered.Status != "failed" || recovered.CompletedAt == nil || recovered.TransitionToken != interruptedParallelExecutionPlanTransitionToken {
		t.Fatalf("recovered parallel run = %+v", recovered)
	}
	if len(recovered.Entries) != 2 || recovered.Entries[0].Status != "failed" || recovered.Entries[1].Status != "failed" ||
		recovered.Entries[0].TestID != firstTestID || recovered.Entries[1].TestID != secondTestID {
		t.Fatalf("recovered parallel entries = %+v", recovered.Entries)
	}
	first, err := newStore.GetTest(firstTestID)
	if err != nil {
		t.Fatalf("GetTest(first staged): %v", err)
	}
	if first.Status != "FAILED" || !strings.Contains(first.Error, "materialization was interrupted") {
		t.Fatalf("first staged test was not terminalized: %+v", first)
	}
	if _, err := newStore.GetTest(secondTestID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("recovery created missing second staged test: %v", err)
	}
	select {
	case startedID := <-backend.called:
		t.Fatalf("pending promotion started staged test %s before parallel recovery", startedID)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestExecutionPlanApprovalClaimUsesRecoverableFinalRunID(t *testing.T) {
	oldStore, newStore := openSharedExecutionPlanStores(t)
	oldLeadership := newExecutionLeadership(oldStore, "approval-old")
	oldLeadership.tick()
	if !oldLeadership.IsAdmitted() {
		t.Fatal("old controller did not acquire leadership")
	}
	oldRunner := NewTestRunner(2, NewHub(), oldStore, WithExecutionLeadership(oldLeadership))
	oldTerm, err := oldRunner.captureExecutionLeaseTerm()
	if err != nil {
		t.Fatalf("capture old term: %v", err)
	}
	now := time.Now().UTC()
	const projectID = "approval-crash-project"
	if err := oldStore.SaveProject(&storage.Project{
		ID: projectID, Name: projectID, Settings: storage.ProjectSettings{MaxVUsers: 1}, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	storeExecutionPlanTermScript(t, oldRunner, "approval-crash-script", projectID)
	scenario, err := oldRunner.StoreScenario(CreateScenarioRequest{
		Name: "approval crash", Duration: "30s", Groups: []CreateGroupEntry{{ScriptID: "approval-crash-script", VUsers: 2, RampUp: "1s"}},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	plan := &storage.ExecutionPlan{
		ID: "approval-crash-plan", ProjectID: projectID, Name: "Approval crash", RunMode: "parallel",
		Entries: []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: scenario.ID}}, CreatedAt: now, UpdatedAt: now,
	}
	if saved, err := oldStore.SaveExecutionPlanIfLeaseTermHeld(plan, executionLeaseName, oldTerm.ownerID, oldTerm.generation); err != nil || !saved {
		t.Fatalf("save plan = (%v, %v)", saved, err)
	}
	approval := &storage.ExecutionPlanApproval{
		ID: "approval-crash", PlanID: plan.ID, ProjectID: projectID, Policy: "quota_burst", Status: "approved",
		RequestedAt: now, PlanContentHash: oldRunner.executionPlanApprovalContentHash(plan), CreatedAt: now, UpdatedAt: now,
	}
	if err := oldRunner.saveExecutionPlanApprovalWithTerm(approval, oldTerm); err != nil {
		t.Fatalf("save approval: %v", err)
	}
	finalRunID := "exec-plan-run-approval-crash"
	claimed, err := oldRunner.claimExecutionPlanApprovalWithTerm(approval.ID, finalRunID, now, oldTerm)
	if err != nil || !claimed {
		t.Fatalf("claim approval = (%v, %v)", claimed, err)
	}
	if _, err := oldStore.GetExecutionPlanRun(finalRunID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("crash point unexpectedly has a run anchor: %v", err)
	}
	claimedApproval, err := oldStore.GetExecutionPlanApproval(approval.ID)
	if err != nil {
		t.Fatalf("GetExecutionPlanApproval: %v", err)
	}
	if claimedApproval.RunID != finalRunID || strings.HasPrefix(claimedApproval.RunID, "claim-") {
		t.Fatalf("approval claim id = %q, want final run id %q", claimedApproval.RunID, finalRunID)
	}

	if err := oldStore.ReleaseSchedulerLease(executionLeaseName, oldTerm.ownerID); err != nil {
		t.Fatalf("release old lease: %v", err)
	}
	newLeadership := newExecutionLeadership(newStore, "approval-new")
	newLeadership.tick()
	if !newLeadership.IsAdmitted() {
		t.Fatal("successor did not acquire leadership")
	}
	defer newLeadership.relinquish()
	newRunner := NewTestRunner(2, NewHub(), newStore, WithExecutionLeadership(newLeadership))
	newTerm, err := newRunner.captureExecutionLeaseTerm()
	if err != nil {
		t.Fatalf("capture successor term: %v", err)
	}
	// A normal manual retry has no caller-supplied run ID. It must discover the
	// approved orphan claim and reuse it instead of generating a second identity.
	result, err := newRunner.materializeAndRunExecutionPlanWithRunIDAndTerm(plan, "", "", "", "", newTerm)
	if err != nil {
		t.Fatalf("resume after approval-claim crash: %v", err)
	}
	if result.RunID != finalRunID || result.Blocked {
		t.Fatalf("resumed result = %+v, want exact claimed run", result)
	}
	run, err := newStore.GetExecutionPlanRun(finalRunID)
	if err != nil {
		t.Fatalf("GetExecutionPlanRun: %v", err)
	}
	if run.ID != finalRunID || run.PlanID != plan.ID || run.Status != "completed" {
		t.Fatalf("recovered approval run = %+v", run)
	}
	freshApproval, err := newStore.GetExecutionPlanApproval(approval.ID)
	if err != nil {
		t.Fatalf("reload approval: %v", err)
	}
	if freshApproval.RunID != finalRunID {
		t.Fatalf("approval was rebound to %q, want %q", freshApproval.RunID, finalRunID)
	}
}

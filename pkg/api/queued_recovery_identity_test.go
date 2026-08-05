package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestQueuedStartRecoveryIdentityUsesDurableTimestampPrecision(t *testing.T) {
	createdAt := time.Date(2026, time.August, 5, 16, 0, 0, 123456789, time.UTC)
	original := &ManagedTest{
		ID: "stable-id", RefKey: "RUN-1", ProjectID: storage.DefaultProjectID,
		CreatedAt: createdAt, MaterializationKey: "materialization-a",
	}
	identity, ok := queuedStartRecoveryIdentityForTest(original)
	if !ok {
		t.Fatal("identity was not captured")
	}
	reloaded := *original
	reloaded.CreatedAt = createdAt.Truncate(time.Microsecond)
	if !identity.matches(&reloaded) {
		t.Fatal("same durable incarnation did not match after PostgreSQL timestamp normalization")
	}
	replacement := reloaded
	replacement.CreatedAt = reloaded.CreatedAt.Add(time.Microsecond)
	if identity.matches(&replacement) {
		t.Fatal("replacement one microsecond later matched the original incarnation")
	}
}

func TestQueuedRecoveryDropsSameIDReplacementBeforeAttempt(t *testing.T) {
	backend := &executionBackendSpy{called: make(chan string, 1)}
	runner := NewTestRunner(2, NewHub(), nil, WithExecutionBackend(backend))
	old := &ManagedTest{
		ID: "reused-recovery-id", RefKey: "RUN-OLD", ProjectID: storage.DefaultProjectID,
		Status: "QUEUED", CreatedAt: time.Now().UTC(),
	}
	runner.mu.Lock()
	runner.tests[old.ID] = old
	runner.mu.Unlock()
	snapshot := *old
	runner.enqueueQueuedCIRecoveryRetryForManagedTestWithTerm(&snapshot, executionLeaseTerm{})
	runner.mu.RLock()
	_, initiallyQueued := runner.ciRecoveryRetrying[old.ID]
	runner.mu.RUnlock()
	if !initiallyQueued {
		t.Fatal("same-incarnation API snapshot did not enqueue recovery")
	}

	replacement := *old
	replacement.RefKey = "RUN-NEW"
	replacement.CreatedAt = old.CreatedAt.Add(time.Microsecond)
	runner.mu.Lock()
	runner.tests[old.ID] = &replacement
	runner.mu.Unlock()

	time.Sleep(100 * time.Millisecond)
	select {
	case started := <-backend.called:
		t.Fatalf("stale recovery started same-ID replacement %q", started)
	default:
	}
	runner.mu.RLock()
	_, retained := runner.ciRecoveryRetrying[old.ID]
	runner.mu.RUnlock()
	if retained {
		t.Fatal("stale recovery state remained attached to replacement")
	}
}

func TestCIRecoveryIdentityChangeDuringStartDoesNotFailReplacement(t *testing.T) {
	runner, store, wrapper := newBlockingStartCommitRunner(t, 2)
	backend := &executionBackendSpy{called: make(chan string, 1)}
	runner.setExecutionBackend(backend)
	policy := ciRunSourceTestPolicy()
	runner.projectStore = &staticCIProjectReadStore{project: &storage.Project{
		ID: storage.DefaultProjectID, Settings: storage.ProjectSettings{GitHub: storage.GitHubProjectPolicy{Actions: policy}},
	}}
	storePreflightScript(runner, "ci-recovery-replacement-script", storage.DefaultProjectID)
	metadata, err := encodeCIRunMetadata(ciRunMetadata{
		SchemaVersion: ciRunSchemaVersion, Source: ciTestRunSource("replacement"),
		ScriptIdentity: "script_id:ci-recovery-replacement-script", PolicyRevision: policy.Revision,
		IdempotencySHA: sha256String("ci-recovery-replacement"), PublishCheck: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := runner.CreateTest(CreateTestRequest{
		Name: "old CI incarnation", ProjectID: storage.DefaultProjectID,
		ScriptID: "ci-recovery-replacement-script", VUsers: 1, Duration: "1m", MaterializationKey: metadata,
	})
	if err != nil {
		t.Fatal(err)
	}
	runner.mu.RLock()
	old := runner.tests[created.ID]
	identity, ok := queuedStartRecoveryIdentityForTest(old)
	runner.mu.RUnlock()
	if !ok {
		t.Fatal("old identity was not captured")
	}
	entered, release := wrapper.blockNextStartCommit()
	done := make(chan error, 1)
	go func() {
		done <- runner.recoverQueuedCIRunForIdentityWithTerm(created.ID, identity, executionLeaseTerm{})
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("CI recovery did not reach blocked start commit")
	}

	if err := store.DeleteTest(created.ID); err != nil {
		t.Fatal(err)
	}
	runner.mu.Lock()
	replacement := cloneManagedTestForReadLocked(old)
	replacement.Name = "replacement CI incarnation"
	replacement.RefKey = old.RefKey + "-replacement"
	replacement.CreatedAt = old.CreatedAt.Add(time.Microsecond)
	replacement.Status = "QUEUED"
	replacement.executionCtx = nil
	replacement.cancel = nil
	replacement.executionFence = ""
	replacement.executionGeneration = 0
	replacement.passiveOperationToken = ""
	replacementRecord := buildManagedTestRecord(replacement)
	runner.tests[created.ID] = replacement
	runner.mu.Unlock()
	if err := store.SaveTest(replacementRecord); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, storage.ErrTestStartIdentityChanged) {
			t.Fatalf("stale recovery returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stale recovery did not return")
	}
	durable, err := store.GetTest(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if durable.Status != "QUEUED" || durable.Error != "" ||
		!durable.StartTime.UTC().Truncate(time.Microsecond).Equal(replacement.CreatedAt.UTC().Truncate(time.Microsecond)) {
		t.Fatalf("replacement was mutated by stale recovery: %#v", durable)
	}
	select {
	case started := <-backend.called:
		t.Fatalf("stale recovery dispatched replacement %q", started)
	default:
	}
}

func TestFailedCIStartResponseDoesNotTerminalizeSameIDReplacement(t *testing.T) {
	runner := NewTestRunner(2, NewHub(), nil)
	metadata, err := encodeCIRunMetadata(ciRunMetadata{
		SchemaVersion: ciRunSchemaVersion, Source: ciTestRunSource("response-replacement"),
		ScriptIdentity: "script_id:response-replacement", IdempotencySHA: sha256String("response-replacement"),
	})
	if err != nil {
		t.Fatal(err)
	}
	old := &ManagedTest{
		ID: "reused-ci-response-id", RefKey: "RUN-OLD", ProjectID: storage.DefaultProjectID,
		Status: "QUEUED", CreatedAt: time.Now().UTC(), MaterializationKey: metadata,
	}
	replacement := *old
	replacement.RefKey = "RUN-NEW"
	replacement.Name = "replacement"
	replacement.CreatedAt = old.CreatedAt.Add(time.Microsecond)
	runner.mu.Lock()
	runner.tests[old.ID] = &replacement
	runner.mu.Unlock()

	server := &Server{runner: runner}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/ci/runs", nil)
	server.respondFailedCIRunStart(recorder, request, old, "controller could not start CI run", errors.New("permanent launch rejection"))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("response = %d body=%s, want conflict", recorder.Code, recorder.Body.String())
	}
	runner.mu.RLock()
	current := runner.tests[old.ID]
	runner.mu.RUnlock()
	if current != &replacement || current.Status != "QUEUED" || current.Error != "" {
		t.Fatalf("stale handler mutated replacement: %#v", current)
	}
}

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

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/secretstore"
	"github.com/jasonczech/apexload/pkg/storage"
)

// scheduleStartRevisionRaceStore advances an admission-bearing table exactly
// once between the scheduler's evidence read and start transaction. A legacy
// ExpectedStartAdmissionRevision=0 start would commit on the first call; a
// coherent scheduler start is fenced and transparently retries with a newer
// nonzero revision.
type scheduleStartRevisionRaceStore struct {
	*storage.SQLiteStore
	runner *TestRunner

	mutateOnce         sync.Once
	mutationErr        error
	revisionsMu        sync.Mutex
	startRevisions     []int64
	revisionReads      atomic.Int32
	revisionReadLocked atomic.Bool
}

type blockedMaterializedOccurrenceStore struct {
	*storage.SQLiteStore
	blocked chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *blockedMaterializedOccurrenceStore) CompareAndSwapScenarioScheduleOccurrence(occurrence *storage.ScenarioScheduleOccurrence, expectedRevision int64) (bool, error) {
	if occurrence != nil && occurrence.Status == "materialized" && occurrence.ReasonCode == "started" {
		s.once.Do(func() {
			close(s.blocked)
			<-s.release
		})
	}
	return s.SQLiteStore.CompareAndSwapScenarioScheduleOccurrence(occurrence, expectedRevision)
}

func (s *scheduleStartRevisionRaceStore) StartAdmissionRevision() (int64, error) {
	s.revisionReads.Add(1)
	if s.runner != nil {
		if s.runner.mu.TryLock() {
			s.runner.mu.Unlock()
		} else {
			s.revisionReadLocked.Store(true)
		}
	}
	return s.SQLiteStore.StartAdmissionRevision()
}

func (s *scheduleStartRevisionRaceStore) SaveTestStartIfNoDeploymentLease(test *storage.TestRecord, deploymentLeaseName, authorizedDeploymentOwnerID, authorizedDeploymentTokenHash string) error {
	s.revisionsMu.Lock()
	s.startRevisions = append(s.startRevisions, test.ExpectedStartAdmissionRevision)
	s.revisionsMu.Unlock()
	s.mutateOnce.Do(func() {
		// Keep the injected reservation outside the test's launch window. Its only
		// purpose is to advance the database-owned admission revision immediately
		// before the first start transaction validates its evidence.
		start := time.Date(2040, time.January, 1, 0, 0, 0, 0, time.UTC)
		s.mutationErr = s.SQLiteStore.SaveCapacityReservation(&storage.CapacityReservation{
			ID: "scheduler-start-revision-race", ProjectID: storage.DefaultProjectID,
			StartAt: start, EndAt: start.Add(time.Hour), ApprovalState: "approved",
		})
	})
	if s.mutationErr != nil {
		return s.mutationErr
	}
	return s.SQLiteStore.SaveTestStartIfNoDeploymentLease(test, deploymentLeaseName, authorizedDeploymentOwnerID, authorizedDeploymentTokenHash)
}

func (s *scheduleStartRevisionRaceStore) observedStartRevisions() []int64 {
	s.revisionsMu.Lock()
	defer s.revisionsMu.Unlock()
	return append([]int64(nil), s.startRevisions...)
}

type durableScheduleStartBackend struct {
	store   *storage.SQLiteStore
	started chan string
	status  chan string
}

func (b *durableScheduleStartBackend) Run(_ *TestRunner, test *ManagedTest) {
	status := ""
	if record, err := b.store.GetTest(test.ID); err == nil && record != nil {
		status = record.Status
	}
	b.status <- status
	b.started <- test.ID
}

func TestRecurringScheduleStartUsesRevisionedDurableEvidence(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = base.Close() })
	store := &scheduleStartRevisionRaceStore{SQLiteStore: base}
	runner := NewTestRunner(2, NewHub(), store)
	store.runner = runner
	backend := &durableScheduleStartBackend{
		store: base, started: make(chan string, 2), status: make(chan string, 2),
	}
	runner.setExecutionBackend(backend)
	script, err := runner.StoreScriptDurable(&dsl.Script{
		ID: "scheduler-start-revision-script", Name: "Scheduler start revision", Protocol: "http", Version: "1",
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
	})
	if err != nil {
		t.Fatalf("StoreScriptDurable: %v", err)
	}
	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		Name: "Scheduler start revision", Duration: "1m",
		Groups: []CreateGroupEntry{{ScriptID: script.ID, VUsers: 1}},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	now := time.Date(2026, time.August, 5, 12, 0, 0, 0, time.UTC)
	schedule := &storage.ScenarioSchedule{
		ID: "scheduler-start-revision-schedule", ScenarioID: scenario.ID, ProjectID: scenario.ProjectID,
		Name: "Scheduler start revision", CronExpr: "*/5 * * * *", Timezone: "UTC", Enabled: true,
		MissedRunPolicy: "catch_up_once", NextRunAt: cloneTimePtr(&now),
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	}
	runner.mu.Lock()
	err = runner.saveScenarioScheduleLocked(schedule)
	runner.mu.Unlock()
	if err != nil {
		t.Fatalf("saveScenarioScheduleLocked: %v", err)
	}

	scheduler := newScenarioScheduler(runner)
	scheduler.now = func() time.Time { return now }
	if err := scheduler.RunDueSchedules(); err != nil {
		t.Fatalf("RunDueSchedules: %v", err)
	}
	var startedID string
	select {
	case startedID = <-backend.started:
	case <-time.After(2 * time.Second):
		t.Fatal("recurring scheduled test was not dispatched")
	}
	select {
	case status := <-backend.status:
		if status != "INITIALIZING" {
			t.Fatalf("durable status observed by backend = %q, want INITIALIZING", status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("backend did not report durable pre-launch status")
	}

	revisions := store.observedStartRevisions()
	if len(revisions) != 2 || revisions[0] <= 0 || revisions[1] <= revisions[0] {
		t.Fatalf("start admission revisions = %#v, want stale nonzero revision followed by a newer retry", revisions)
	}
	if store.revisionReads.Load() < 4 {
		t.Fatalf("StartAdmissionRevision reads = %d, want before/after reads for both attempts", store.revisionReads.Load())
	}
	if store.revisionReadLocked.Load() {
		t.Fatal("scheduler read start-admission revision while runner.mu was held")
	}
	record, err := base.GetTest(startedID)
	if err != nil || record == nil || record.Status != "INITIALIZING" {
		t.Fatalf("durable scheduled test = (%#v, %v), want committed INITIALIZING", record, err)
	}

	// Re-running the same due sweep must not materialize or dispatch a second
	// test after the first response/launch boundary completed.
	if err := scheduler.RunDueSchedules(); err != nil {
		t.Fatalf("second RunDueSchedules: %v", err)
	}
	select {
	case duplicate := <-backend.started:
		t.Fatalf("second sweep dispatched duplicate test %q", duplicate)
	case <-time.After(100 * time.Millisecond):
	}

	// A successor controller reconstructs the durable schedule and test rows.
	// It may fail-close the interrupted INITIALIZING runtime, but it must not
	// reinterpret the already-advanced occurrence as another launch.
	restarted := NewTestRunner(2, NewHub(), store)
	store.runner = restarted
	restartBackend := &durableScheduleStartBackend{
		store: base, started: make(chan string, 1), status: make(chan string, 1),
	}
	restarted.setExecutionBackend(restartBackend)
	restartScheduler := newScenarioScheduler(restarted)
	restartScheduler.now = func() time.Time { return now }
	if err := restartScheduler.RunDueSchedules(); err != nil {
		t.Fatalf("RunDueSchedules after restart: %v", err)
	}
	select {
	case duplicate := <-restartBackend.started:
		t.Fatalf("restart dispatched duplicate scheduled test %q", duplicate)
	case <-time.After(100 * time.Millisecond):
	}
}

func saveDueScenarioScheduleForStartRegression(t *testing.T, runner *TestRunner, scenario *StoredScenario, id string, now time.Time) *storage.ScenarioSchedule {
	t.Helper()
	schedule := &storage.ScenarioSchedule{
		ID: id, ScenarioID: scenario.ID, ProjectID: scenario.ProjectID,
		Name: id, CronExpr: "*/5 * * * *", Timezone: "UTC", Enabled: true,
		MissedRunPolicy: "catch_up_once", NextRunAt: cloneTimePtr(&now),
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	}
	runner.mu.Lock()
	err := runner.saveScenarioScheduleLocked(schedule)
	runner.mu.Unlock()
	if err != nil {
		t.Fatalf("saveScenarioScheduleLocked: %v", err)
	}
	return schedule
}

func TestRecurringScheduleStartRejectsMissingStaticSecretBeforeBackendLaunch(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = base.Close() })
	runner := NewTestRunner(2, NewHub(), base)
	backend := &durableScheduleStartBackend{
		store: base, started: make(chan string, 1), status: make(chan string, 1),
	}
	runner.setExecutionBackend(backend)
	script, err := runner.StoreScriptDurable(&dsl.Script{
		ID: "scheduled-missing-secret-script", Name: "Scheduled missing secret", Protocol: "http", Version: "1",
		Config: dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{
			Name: "request", Type: "request", Target: "/",
			Headers: map[string]string{"Authorization": "Bearer {{secret.SCHEDULE_TOKEN}}"},
		}},
	})
	if err != nil {
		t.Fatalf("StoreScriptDurable: %v", err)
	}
	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		Name: "Scheduled missing secret", Duration: "1m",
		Groups: []CreateGroupEntry{{ScriptID: script.ID, VUsers: 1}},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	now := time.Date(2026, time.August, 5, 13, 0, 0, 0, time.UTC)
	schedule := saveDueScenarioScheduleForStartRegression(t, runner, scenario, "scheduled-missing-secret", now)
	scheduler := newScenarioScheduler(runner)
	scheduler.now = func() time.Time { return now }
	if err := scheduler.RunDueSchedules(); err != nil {
		t.Fatalf("RunDueSchedules: %v", err)
	}
	select {
	case startedID := <-backend.started:
		t.Fatalf("backend launched missing-secret scheduled test %q", startedID)
	default:
	}
	occurrence, err := base.GetScenarioScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, now)
	if err != nil || occurrence == nil {
		t.Fatalf("GetScenarioScheduleOccurrenceByScheduleAndPlannedAt = (%#v, %v)", occurrence, err)
	}
	if occurrence.Status != "failed" || occurrence.ReasonCode != "start_failed" ||
		!strings.Contains(occurrence.ConflictSummary, "SCHEDULE_TOKEN") || !strings.Contains(occurrence.ConflictSummary, "configure") {
		t.Fatalf("missing-secret occurrence = %#v", occurrence)
	}
	record, err := base.GetTest(occurrence.TestID)
	if err != nil || record == nil || record.Status != "FAILED" {
		t.Fatalf("missing-secret durable test = (%#v, %v), want FAILED", record, err)
	}
}

func TestRecurringScheduleRunsScriptShakeoutOnceAndDoesNotReplayAfterRestart(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = base.Close() })
	var executions atomic.Int32
	protocolName := registerRunnerTestProtocol(t, "scheduled-shakeout-once", func() protocol.Protocol {
		return &shakeoutActionProtocol{name: "scheduled-shakeout-once", execute: func(context.Context) error {
			executions.Add(1)
			return nil
		}}
	})
	runner := NewTestRunner(2, NewHub(), base)
	backend := &durableScheduleStartBackend{
		store: base, started: make(chan string, 1), status: make(chan string, 1),
	}
	runner.setExecutionBackend(backend)
	script, err := runner.StoreScriptDurable(&dsl.Script{
		ID: "scheduled-shakeout-once-script", Name: "Scheduled shakeout once", Protocol: protocolName, Version: "1",
		Actions: []dsl.Action{{Name: "one shot", Type: "request", Target: "/"}},
	})
	if err != nil {
		t.Fatalf("StoreScriptDurable: %v", err)
	}
	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		Name: "Scheduled shakeout once", Duration: "1m", ScriptShakeout: true,
		Groups: []CreateGroupEntry{{ScriptID: script.ID, VUsers: 1}},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	now := time.Date(2026, time.August, 5, 14, 0, 0, 0, time.UTC)
	saveDueScenarioScheduleForStartRegression(t, runner, scenario, "scheduled-shakeout-once", now)
	scheduler := newScenarioScheduler(runner)
	scheduler.now = func() time.Time { return now }
	if err := scheduler.RunDueSchedules(); err != nil {
		t.Fatalf("RunDueSchedules: %v", err)
	}
	select {
	case <-backend.started:
	case <-time.After(2 * time.Second):
		t.Fatal("scheduled run was not dispatched after shakeout")
	}
	if got := executions.Load(); got != 1 {
		t.Fatalf("shakeout executions = %d, want one", got)
	}

	restarted := NewTestRunner(2, NewHub(), base)
	restartBackend := &durableScheduleStartBackend{
		store: base, started: make(chan string, 1), status: make(chan string, 1),
	}
	restarted.setExecutionBackend(restartBackend)
	restartScheduler := newScenarioScheduler(restarted)
	restartScheduler.now = func() time.Time { return now }
	if err := restartScheduler.RunDueSchedules(); err != nil {
		t.Fatalf("RunDueSchedules after restart: %v", err)
	}
	select {
	case duplicate := <-restartBackend.started:
		t.Fatalf("restart dispatched duplicate scheduled test %q", duplicate)
	case <-time.After(100 * time.Millisecond):
	}
	if got := executions.Load(); got != 1 {
		t.Fatalf("shakeout executions after restart = %d, want one", got)
	}
}

func TestRecurringScheduleDefersPostShakeoutProviderOutageWithoutReplay(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = base.Close() })
	const projectID = storage.DefaultProjectID
	providerErr := errors.New("scheduled secret provider temporarily unavailable")
	secretStore := &versionedShakeoutSecretStore{
		value: "stable-before-and-after-shakeout",
		metadata: secretstore.Metadata{
			ProjectID: projectID, Name: "TOKEN", Provider: "scheduled-test", UpdatedAt: time.Unix(100, 0).UTC(),
		},
	}
	var executions atomic.Int32
	protocolName := registerRunnerTestProtocol(t, "scheduled-shakeout-provider-retry", func() protocol.Protocol {
		return &shakeoutActionProtocol{name: "scheduled-shakeout-provider-retry", execute: func(context.Context) error {
			executions.Add(1)
			secretStore.mu.Lock()
			secretStore.err = providerErr
			secretStore.mu.Unlock()
			return nil
		}}
	})
	runner := NewTestRunner(2, NewHub(), base, WithProjectSecretStore(secretStore))
	backend := &durableScheduleStartBackend{
		store: base, started: make(chan string, 1), status: make(chan string, 1),
	}
	runner.setExecutionBackend(backend)
	script, err := runner.StoreScriptDurable(&dsl.Script{
		ID: "scheduled-provider-retry-script", ProjectID: projectID, Name: "Scheduled provider retry",
		Protocol: protocolName, Version: "1", Actions: []dsl.Action{{
			Name: "one shot", Type: "request", Target: "/",
			Headers: map[string]string{"Authorization": "Bearer {{secret.TOKEN}}"},
		}},
	})
	if err != nil {
		t.Fatalf("StoreScriptDurable: %v", err)
	}
	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		Name: "Scheduled provider retry", Duration: "1m", ScriptShakeout: true,
		Groups: []CreateGroupEntry{{ScriptID: script.ID, VUsers: 1}},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	now := time.Date(2026, time.August, 5, 15, 0, 0, 0, time.UTC)
	schedule := saveDueScenarioScheduleForStartRegression(t, runner, scenario, "scheduled-provider-retry", now)
	scheduler := newScenarioScheduler(runner)
	scheduler.now = func() time.Time { return now }
	if err := scheduler.RunDueSchedules(); err != nil {
		t.Fatalf("RunDueSchedules: %v", err)
	}
	occurrence, err := base.GetScenarioScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, now)
	if err != nil || occurrence == nil {
		t.Fatalf("GetScenarioScheduleOccurrenceByScheduleAndPlannedAt = (%#v, %v)", occurrence, err)
	}
	if occurrence.Status != "queued" || occurrence.ReasonCode != "start_retrying" || occurrence.MaterializedAt != nil {
		t.Fatalf("deferred occurrence = %#v, want queued/start_retrying", occurrence)
	}
	runner.mu.RLock()
	deferred := runner.tests[occurrence.TestID]
	deferredOK := deferred != nil && deferred.Status == "SHAKEOUT" && deferred.pendingScriptShakeoutOutcome != nil &&
		deferred.pendingScriptShakeoutOutcome.VerificationPending && deferred.executionCtx == nil
	runner.mu.RUnlock()
	if !deferredOK {
		t.Fatalf("deferred scheduled test = %#v, want retained verification-pending SHAKEOUT", deferred)
	}
	select {
	case startedID := <-backend.started:
		t.Fatalf("backend launched during provider outage: %q", startedID)
	case <-time.After(100 * time.Millisecond):
	}

	secretStore.mu.Lock()
	secretStore.err = nil
	secretStore.mu.Unlock()
	var startedID string
	select {
	case startedID = <-backend.started:
	case <-time.After(5 * time.Second):
		t.Fatal("scheduled run did not start after provider recovery")
	}
	if startedID != occurrence.TestID {
		t.Fatalf("backend started %q, want %q", startedID, occurrence.TestID)
	}
	if got := executions.Load(); got != 1 {
		t.Fatalf("shakeout executions = %d, want one across provider retry", got)
	}
	record, err := base.GetTest(startedID)
	if err != nil || record == nil || record.Status != "INITIALIZING" {
		t.Fatalf("eventually started durable test = (%#v, %v), want INITIALIZING", record, err)
	}
	eventualOccurrence, err := base.GetScenarioScheduleOccurrenceByTestID(startedID)
	if err != nil || eventualOccurrence == nil || eventualOccurrence.Status != "materialized" || eventualOccurrence.ReasonCode != "started" {
		t.Fatalf("eventual occurrence = (%#v, %v), want materialized/started", eventualOccurrence, err)
	}
}

func TestRecurringSchedulePostShakeoutRecoveryWaitsForDeploymentLeaseWithoutStrandingOccurrence(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = base.Close() })
	const projectID = storage.DefaultProjectID
	providerErr := errors.New("scheduled secret provider temporarily unavailable")
	secretStore := &versionedShakeoutSecretStore{
		value: "stable-before-and-after-shakeout",
		metadata: secretstore.Metadata{
			ProjectID: projectID, Name: "TOKEN", Provider: "scheduled-test", UpdatedAt: time.Unix(100, 0).UTC(),
		},
	}
	var executions atomic.Int32
	protocolName := registerRunnerTestProtocol(t, "scheduled-shakeout-deployment-lease", func() protocol.Protocol {
		return &shakeoutActionProtocol{name: "scheduled-shakeout-deployment-lease", execute: func(context.Context) error {
			executions.Add(1)
			secretStore.mu.Lock()
			secretStore.err = providerErr
			secretStore.mu.Unlock()
			return nil
		}}
	})
	runner := NewTestRunner(2, NewHub(), base, WithProjectSecretStore(secretStore))
	backend := &durableScheduleStartBackend{
		store: base, started: make(chan string, 1), status: make(chan string, 1),
	}
	runner.setExecutionBackend(backend)
	script, err := runner.StoreScriptDurable(&dsl.Script{
		ID: "scheduled-deployment-lease-script", ProjectID: projectID, Name: "Scheduled deployment lease",
		Protocol: protocolName, Version: "1", Actions: []dsl.Action{{
			Name: "one shot", Type: "request", Target: "/",
			Headers: map[string]string{"Authorization": "Bearer {{secret.TOKEN}}"},
		}},
	})
	if err != nil {
		t.Fatalf("StoreScriptDurable: %v", err)
	}
	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		Name: "Scheduled deployment lease", Duration: "1m", ScriptShakeout: true,
		Groups: []CreateGroupEntry{{ScriptID: script.ID, VUsers: 1}},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	now := time.Date(2026, time.August, 5, 15, 15, 0, 0, time.UTC)
	schedule := saveDueScenarioScheduleForStartRegression(t, runner, scenario, "scheduled-deployment-lease", now)
	scheduler := newScenarioScheduler(runner)
	scheduler.now = func() time.Time { return now }
	if err := scheduler.RunDueSchedules(); err != nil {
		t.Fatalf("RunDueSchedules: %v", err)
	}
	occurrence, err := base.GetScenarioScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, now)
	if err != nil || occurrence == nil || occurrence.Status != "queued" || occurrence.ReasonCode != "start_retrying" {
		t.Fatalf("deferred occurrence = (%#v, %v), want queued/start_retrying", occurrence, err)
	}

	const leaseOwner = "scheduled-recovery-deployment"
	const leaseTokenHash = "scheduled-recovery-token-hash"
	if _, _, err := base.AcquireDeploymentLease(
		storage.DefaultDeploymentLeaseName,
		leaseOwner,
		leaseTokenHash,
		time.Minute,
		storage.DeploymentLeaseContext{Environment: "test"},
		true,
	); err != nil {
		t.Fatalf("AcquireDeploymentLease: %v", err)
	}
	secretStore.mu.Lock()
	secretStore.err = nil
	secretStore.mu.Unlock()

	// The completed one-shot result may become QUEUED, but maintenance must keep
	// both it and the calendar retry alive rather than retiring the coordinator.
	time.Sleep(200 * time.Millisecond)
	select {
	case startedID := <-backend.started:
		t.Fatalf("backend launched through active deployment lease: %q", startedID)
	default:
	}
	duringLease, err := base.GetScenarioScheduleOccurrenceByTestID(occurrence.TestID)
	if err != nil || duringLease == nil || duringLease.Status != "queued" || duringLease.ReasonCode != "start_retrying" {
		t.Fatalf("occurrence during deployment lease = (%#v, %v), want retained queued/start_retrying", duringLease, err)
	}
	if _, err := base.ReleaseDeploymentLease(storage.DefaultDeploymentLeaseName, leaseOwner, leaseTokenHash); err != nil {
		t.Fatalf("ReleaseDeploymentLease: %v", err)
	}

	var startedID string
	select {
	case startedID = <-backend.started:
	case <-time.After(5 * time.Second):
		t.Fatal("scheduled run did not resume after deployment lease release")
	}
	if startedID != occurrence.TestID {
		t.Fatalf("backend started %q, want %q", startedID, occurrence.TestID)
	}
	if got := executions.Load(); got != 1 {
		t.Fatalf("shakeout executions = %d, want one across provider and deployment-lease recovery", got)
	}
	eventualOccurrence, err := base.GetScenarioScheduleOccurrenceByTestID(startedID)
	if err != nil || eventualOccurrence == nil || eventualOccurrence.Status != "materialized" || eventualOccurrence.ReasonCode != "started" {
		t.Fatalf("eventual occurrence = (%#v, %v), want materialized/started", eventualOccurrence, err)
	}
}

func TestRecurringSchedulePostShakeoutPermanentRecoveryErrorTerminalizesCorrelation(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = base.Close() })
	const projectID = storage.DefaultProjectID
	providerErr := errors.New("scheduled secret provider temporarily unavailable")
	secretStore := &versionedShakeoutSecretStore{
		value: "stable-before-and-after-shakeout",
		metadata: secretstore.Metadata{
			ProjectID: projectID, Name: "TOKEN", Provider: "scheduled-test", UpdatedAt: time.Unix(100, 0).UTC(),
		},
	}
	var executions atomic.Int32
	protocolName := registerRunnerTestProtocol(t, "scheduled-shakeout-permanent-recovery-error", func() protocol.Protocol {
		return &shakeoutActionProtocol{name: "scheduled-shakeout-permanent-recovery-error", execute: func(context.Context) error {
			executions.Add(1)
			secretStore.mu.Lock()
			secretStore.err = providerErr
			secretStore.mu.Unlock()
			return nil
		}}
	})
	runner := NewTestRunner(2, NewHub(), base, WithProjectSecretStore(secretStore))
	backend := &durableScheduleStartBackend{
		store: base, started: make(chan string, 1), status: make(chan string, 1),
	}
	runner.setExecutionBackend(backend)
	script, err := runner.StoreScriptDurable(&dsl.Script{
		ID: "scheduled-permanent-recovery-error-script", ProjectID: projectID, Name: "Scheduled permanent recovery error",
		Protocol: protocolName, Version: "1", Actions: []dsl.Action{{
			Name: "one shot", Type: "request", Target: "/",
			Headers: map[string]string{"Authorization": "Bearer {{secret.TOKEN}}"},
		}},
	})
	if err != nil {
		t.Fatalf("StoreScriptDurable: %v", err)
	}
	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		Name: "Scheduled permanent recovery error", Duration: "1m", ScriptShakeout: true,
		Groups: []CreateGroupEntry{{ScriptID: script.ID, VUsers: 1}},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	now := time.Date(2026, time.August, 5, 15, 20, 0, 0, time.UTC)
	schedule := saveDueScenarioScheduleForStartRegression(t, runner, scenario, "scheduled-permanent-recovery-error", now)
	scheduler := newScenarioScheduler(runner)
	scheduler.now = func() time.Time { return now }
	if err := scheduler.RunDueSchedules(); err != nil {
		t.Fatalf("RunDueSchedules: %v", err)
	}
	occurrence, err := base.GetScenarioScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, now)
	if err != nil || occurrence == nil || occurrence.Status != "queued" || occurrence.ReasonCode != "start_retrying" {
		t.Fatalf("deferred occurrence = (%#v, %v), want queued/start_retrying", occurrence, err)
	}

	// Model a permanent runtime restoration rejection discovered only after the
	// provider recovers and the completed shakeout result becomes QUEUED.
	runner.mu.Lock()
	deferred := runner.tests[occurrence.TestID]
	if deferred == nil {
		runner.mu.Unlock()
		t.Fatal("deferred scheduled test is missing")
	}
	deferred.runtimeHydrationError = "injected incompatible runtime snapshot"
	runner.mu.Unlock()
	secretStore.mu.Lock()
	secretStore.err = nil
	secretStore.mu.Unlock()

	deadline := time.Now().Add(5 * time.Second)
	for {
		record, recordErr := base.GetTest(occurrence.TestID)
		durableOccurrence, occurrenceErr := base.GetScenarioScheduleOccurrenceByTestID(occurrence.TestID)
		durableSchedule, scheduleErr := base.GetScenarioSchedule(schedule.ID)
		if recordErr == nil && record != nil && record.Status == "FAILED" &&
			strings.Contains(record.Error, "incompatible runtime snapshot") &&
			occurrenceErr == nil && durableOccurrence != nil && durableOccurrence.Status == "failed" &&
			durableOccurrence.ReasonCode == "pending_promotion_failed" && durableOccurrence.MaterializedAt == nil &&
			scheduleErr == nil && durableSchedule != nil && durableSchedule.NextRequiredAction == "operator_decision_required" &&
			strings.Contains(durableSchedule.LastError, "incompatible runtime snapshot") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("permanent recovery failure did not converge: test=(%#v, %v) occurrence=(%#v, %v) schedule=(%#v, %v)", record, recordErr, durableOccurrence, occurrenceErr, durableSchedule, scheduleErr)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := executions.Load(); got != 1 {
		t.Fatalf("shakeout executions = %d, want one across permanent recovery failure", got)
	}
	select {
	case startedID := <-backend.started:
		t.Fatalf("backend launched permanently rejected scheduled test %q", startedID)
	default:
	}
}

func TestRecurringScheduleProviderRecoveryWithChangedFingerprintRequiresReviewWithoutReplay(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = base.Close() })
	const projectID = storage.DefaultProjectID
	providerErr := errors.New("scheduled secret provider temporarily unavailable")
	secretStore := &versionedShakeoutSecretStore{
		value: "before-shakeout",
		metadata: secretstore.Metadata{
			ProjectID: projectID, Name: "TOKEN", Provider: "scheduled-test", UpdatedAt: time.Unix(100, 0).UTC(),
		},
	}
	var executions atomic.Int32
	protocolName := registerRunnerTestProtocol(t, "scheduled-shakeout-provider-rotation", func() protocol.Protocol {
		return &shakeoutActionProtocol{name: "scheduled-shakeout-provider-rotation", execute: func(context.Context) error {
			executions.Add(1)
			secretStore.mu.Lock()
			secretStore.err = providerErr
			secretStore.mu.Unlock()
			return nil
		}}
	})
	runner := NewTestRunner(2, NewHub(), base, WithProjectSecretStore(secretStore))
	backend := &durableScheduleStartBackend{
		store: base, started: make(chan string, 1), status: make(chan string, 1),
	}
	runner.setExecutionBackend(backend)
	script, err := runner.StoreScriptDurable(&dsl.Script{
		ID: "scheduled-provider-rotation-script", ProjectID: projectID, Name: "Scheduled provider rotation",
		Protocol: protocolName, Version: "1", Actions: []dsl.Action{{
			Name: "one shot", Type: "request", Target: "/",
			Headers: map[string]string{"Authorization": "Bearer {{secret.TOKEN}}"},
		}},
	})
	if err != nil {
		t.Fatalf("StoreScriptDurable: %v", err)
	}
	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		Name: "Scheduled provider rotation", Duration: "1m", ScriptShakeout: true,
		Groups: []CreateGroupEntry{{ScriptID: script.ID, VUsers: 1}},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	now := time.Date(2026, time.August, 5, 15, 30, 0, 0, time.UTC)
	schedule := saveDueScenarioScheduleForStartRegression(t, runner, scenario, "scheduled-provider-rotation", now)
	scheduler := newScenarioScheduler(runner)
	scheduler.now = func() time.Time { return now }
	if err := scheduler.RunDueSchedules(); err != nil {
		t.Fatalf("RunDueSchedules: %v", err)
	}
	occurrence, err := base.GetScenarioScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, now)
	if err != nil || occurrence == nil || occurrence.Status != "queued" || occurrence.ReasonCode != "start_retrying" {
		t.Fatalf("deferred occurrence = (%#v, %v), want queued/start_retrying", occurrence, err)
	}

	secretStore.mu.Lock()
	secretStore.err = nil
	secretStore.value = "rotated-after-shakeout"
	secretStore.metadata.UpdatedAt = time.Unix(200, 0).UTC()
	secretStore.mu.Unlock()
	deadline := time.Now().Add(5 * time.Second)
	for {
		current, ok := runner.GetTest(occurrence.TestID)
		runner.mu.RLock()
		managed := runner.tests[occurrence.TestID]
		passiveFenceReleased := managed != nil && managed.passiveOperationToken == "" && managed.executionFence == "" && managed.executionGeneration == 0
		runner.mu.RUnlock()
		durableOccurrence, occurrenceErr := base.GetScenarioScheduleOccurrenceByTestID(occurrence.TestID)
		durableSchedule, scheduleErr := base.GetScenarioSchedule(schedule.ID)
		if ok && current.Status == "SHAKEOUT_REVIEW" && passiveFenceReleased && occurrenceErr == nil && durableOccurrence != nil &&
			durableOccurrence.Status == "blocked" && durableOccurrence.ReasonCode == "script_shakeout_review" &&
			durableOccurrence.MaterializedAt == nil && scheduleErr == nil && durableSchedule != nil &&
			durableSchedule.NextRequiredAction == "operator_decision_required" &&
			strings.Contains(durableSchedule.LastError, "inputs changed") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("shakeout review correlation did not converge: test=%#v occurrence=(%#v, %v) schedule=(%#v, %v)", current, durableOccurrence, occurrenceErr, durableSchedule, scheduleErr)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := executions.Load(); got != 1 {
		t.Fatalf("shakeout executions = %d, want one across provider rotation", got)
	}
	select {
	case startedID := <-backend.started:
		t.Fatalf("backend launched review-gated scheduled test %q", startedID)
	default:
	}
}

func TestRecurringScheduleSweepRepairsCrashAfterDurableShakeoutReview(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = base.Close() })
	now := time.Date(2026, time.August, 5, 15, 45, 0, 0, time.UTC)
	const (
		scenarioID   = "scenario-shakeout-review-crash"
		scheduleID   = "schedule-shakeout-review-crash"
		occurrenceID = "occurrence-shakeout-review-crash"
		testID       = "test-shakeout-review-crash"
	)
	if err := base.SaveScenario(&storage.Scenario{
		ID: scenarioID, Name: "Shakeout review crash", Duration: "1m", CreatedAt: now.Add(-time.Hour), UpdatedAt: now,
	}, nil); err != nil {
		t.Fatalf("SaveScenario: %v", err)
	}
	if err := base.SaveTest(&storage.TestRecord{
		ID: testID, Name: "Shakeout review crash", Status: "SHAKEOUT_REVIEW",
		ScenarioID: scenarioID, ProjectID: storage.DefaultProjectID, Duration: int64(time.Minute),
		StartTime: now, Error: "Script shakeout inputs changed while the check was running; full load test has not started.", Groups: []any{},
	}); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}
	if err := base.SaveScenarioSchedule(&storage.ScenarioSchedule{
		ID: scheduleID, ScenarioID: scenarioID, ProjectID: storage.DefaultProjectID, Revision: 1,
		Name: "Shakeout review crash", CronExpr: "*/5 * * * *", Timezone: "UTC", Enabled: true,
		MissedRunPolicy: "catch_up_once", LastRunTestID: testID, NextRunAt: &now,
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveScenarioSchedule: %v", err)
	}
	if err := base.SaveScenarioScheduleOccurrence(&storage.ScenarioScheduleOccurrence{
		ID: occurrenceID, ScheduleID: scheduleID, ScenarioID: scenarioID, ProjectID: storage.DefaultProjectID,
		ScheduleRevision: 1, PlannedAt: now, Status: "queued", ReasonCode: "start_retrying",
		TestID: testID, ConflictSummary: "controller stopped after durable shakeout review", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveScenarioScheduleOccurrence: %v", err)
	}

	runner := NewTestRunner(2, NewHub(), base)
	backend := &durableScheduleStartBackend{
		store: base, started: make(chan string, 1), status: make(chan string, 1),
	}
	runner.setExecutionBackend(backend)
	scheduler := newScenarioScheduler(runner)
	scheduler.now = func() time.Time { return now }
	if err := scheduler.RunDueSchedules(); err != nil {
		t.Fatalf("RunDueSchedules: %v", err)
	}
	occurrence, err := base.GetScenarioScheduleOccurrenceByTestID(testID)
	if err != nil || occurrence == nil || occurrence.Status != "blocked" || occurrence.ReasonCode != "script_shakeout_review" || occurrence.MaterializedAt != nil {
		t.Fatalf("recovered occurrence = (%#v, %v), want blocked/script_shakeout_review", occurrence, err)
	}
	schedule, err := base.GetScenarioSchedule(scheduleID)
	if err != nil || schedule == nil || schedule.NextRequiredAction != "operator_decision_required" || !strings.Contains(schedule.LastError, "inputs changed") {
		t.Fatalf("recovered schedule = (%#v, %v), want operator decision", schedule, err)
	}
	select {
	case startedID := <-backend.started:
		t.Fatalf("crash recovery launched review-gated test %q", startedID)
	default:
	}
}

func TestRecurringScheduleRetainsLifecycleGateUntilRunnerStateRevalidated(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = base.Close() })
	runner := NewTestRunner(2, NewHub(), base)
	runner.setExecutionBackend(&durableScheduleStartBackend{
		store: base, started: make(chan string, 1), status: make(chan string, 1),
	})
	script, err := runner.StoreScriptDurable(&dsl.Script{
		ID: "scheduled-lifecycle-gate-script", Name: "Scheduled lifecycle gate", Protocol: "http", Version: "1",
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
	})
	if err != nil {
		t.Fatalf("StoreScriptDurable: %v", err)
	}
	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		Name: "Scheduled lifecycle gate", Duration: "1m",
		Groups: []CreateGroupEntry{{ScriptID: script.ID, VUsers: 1}},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	now := time.Date(2026, time.August, 5, 16, 0, 0, 0, time.UTC)
	schedule := saveDueScenarioScheduleForStartRegression(t, runner, scenario, "scheduled-lifecycle-gate", now)
	hookEntered := make(chan struct{})
	releaseHook := make(chan struct{})
	var gateCount atomic.Int32
	var availableTokens atomic.Int32
	runner.beforeScenarioScheduleStartLifecycleRelease = func() {
		runner.remoteControlMu.Lock()
		gateCount.Store(int32(len(runner.remoteControlGates)))
		for _, gate := range runner.remoteControlGates {
			availableTokens.Store(int32(len(gate.lifecycle)))
		}
		runner.remoteControlMu.Unlock()
		close(hookEntered)
		<-releaseHook
	}
	scheduler := newScenarioScheduler(runner)
	scheduler.now = func() time.Time { return now }
	sweepDone := make(chan error, 1)
	go func() { sweepDone <- scheduler.RunDueSchedules() }()
	select {
	case <-hookEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("scheduler did not reach lifecycle revalidation barrier")
	}
	if gateCount.Load() != 1 || availableTokens.Load() != 0 {
		t.Fatalf("lifecycle gate state at runner revalidation = gates %d available tokens %d, want 1/0", gateCount.Load(), availableTokens.Load())
	}
	occurrence, err := base.GetScenarioScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, now)
	if err != nil || occurrence == nil {
		t.Fatalf("GetScenarioScheduleOccurrenceByScheduleAndPlannedAt = (%#v, %v)", occurrence, err)
	}
	testID := scenarioScheduleOccurrenceTestID(occurrence.ID)
	stopDone := make(chan error, 1)
	go func() { stopDone <- runner.StopTest(testID) }()
	select {
	case err := <-stopDone:
		t.Fatalf("concurrent stop crossed lifecycle gate before scheduler validation: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseHook)
	if err := <-sweepDone; err != nil {
		t.Fatalf("RunDueSchedules: %v", err)
	}
	if err := <-stopDone; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("StopTest after scheduler validation: %v", err)
	}
	stopped, ok := runner.GetTest(testID)
	if !ok || stopped.Status != "ABORTED" {
		t.Fatalf("concurrent stop result = %#v, want ABORTED after scheduler validation", stopped)
	}
}

func TestRecurringScheduleStopDuringOccurrenceCommitCannotLeaveFalseMaterialization(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = base.Close() })
	store := &blockedMaterializedOccurrenceStore{
		SQLiteStore: base,
		blocked:     make(chan struct{}),
		release:     make(chan struct{}),
	}
	runner := NewTestRunner(2, NewHub(), store)
	backend := &durableScheduleStartBackend{
		store: base, started: make(chan string, 1), status: make(chan string, 1),
	}
	runner.setExecutionBackend(backend)
	script, err := runner.StoreScriptDurable(&dsl.Script{
		ID: "scheduled-occurrence-stop-race-script", Name: "Scheduled occurrence stop race", Protocol: "http", Version: "1",
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
	})
	if err != nil {
		t.Fatalf("StoreScriptDurable: %v", err)
	}
	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		Name: "Scheduled occurrence stop race", Duration: "1m",
		Groups: []CreateGroupEntry{{ScriptID: script.ID, VUsers: 1}},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	now := time.Date(2026, time.August, 5, 17, 0, 0, 0, time.UTC)
	schedule := saveDueScenarioScheduleForStartRegression(t, runner, scenario, "scheduled-occurrence-stop-race", now)

	releaseReached := make(chan struct{})
	allowLifecycleRelease := make(chan struct{})
	runner.beforeScenarioScheduleStartLifecycleRelease = func() {
		close(releaseReached)
		<-allowLifecycleRelease
	}
	scheduler := newScenarioScheduler(runner)
	scheduler.now = func() time.Time { return now }
	sweepDone := make(chan error, 1)
	go func() { sweepDone <- scheduler.RunDueSchedules() }()
	select {
	case <-store.blocked:
	case <-time.After(3 * time.Second):
		t.Fatal("scheduler did not block on the materialized occurrence commit")
	}
	occurrence, err := base.GetScenarioScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, now)
	if err != nil || occurrence == nil {
		t.Fatalf("GetScenarioScheduleOccurrenceByScheduleAndPlannedAt = (%#v, %v)", occurrence, err)
	}
	testID := scenarioScheduleOccurrenceTestID(occurrence.ID)
	stopDone := make(chan error, 1)
	go func() { stopDone <- runner.StopTest(testID) }()
	select {
	case err := <-stopDone:
		t.Fatalf("StopTest crossed the lifecycle gate while occurrence persistence was blocked: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(store.release)
	select {
	case <-releaseReached:
	case <-time.After(3 * time.Second):
		t.Fatal("scheduler did not finish both correlation commits")
	}
	select {
	case err := <-stopDone:
		t.Fatalf("StopTest crossed the lifecycle gate before the scheduler released it: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(allowLifecycleRelease)
	if err := <-sweepDone; err != nil {
		t.Fatalf("RunDueSchedules: %v", err)
	}
	if err := <-stopDone; err != nil {
		t.Fatalf("StopTest: %v", err)
	}

	select {
	case startedID := <-backend.started:
		t.Fatalf("backend launched preempted scheduled test %q", startedID)
	case <-time.After(150 * time.Millisecond):
	}
	durableTest, err := base.GetTest(testID)
	if err != nil || durableTest == nil || durableTest.Status != "ABORTED" {
		t.Fatalf("durable test after Stop = (%#v, %v), want ABORTED", durableTest, err)
	}
	durableOccurrence, err := base.GetScenarioScheduleOccurrenceByTestID(testID)
	if err != nil || durableOccurrence == nil {
		t.Fatalf("GetScenarioScheduleOccurrenceByTestID = (%#v, %v)", durableOccurrence, err)
	}
	if durableOccurrence.Status != "failed" || durableOccurrence.ReasonCode != "prelaunch_aborted" || durableOccurrence.MaterializedAt != nil {
		t.Fatalf("occurrence after Stop = %#v, want failed/prelaunch_aborted without materialized_at", durableOccurrence)
	}
	durableSchedule, err := base.GetScenarioSchedule(schedule.ID)
	if err != nil {
		t.Fatalf("GetScenarioSchedule: %v", err)
	}
	if durableSchedule.LastRunTestID != testID || durableSchedule.LastRunAt != nil || !strings.Contains(durableSchedule.LastError, "cancelled before start") {
		t.Fatalf("schedule after Stop = %#v, want correlated aborted prelaunch state", durableSchedule)
	}
}

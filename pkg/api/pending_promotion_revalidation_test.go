package api

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/secretstore"
	"github.com/jasonczech/apexload/pkg/storage"
)

type flakyPendingProjectStore struct {
	mu        sync.Mutex
	project   *storage.Project
	remaining int
	err       error
}

type missingPendingProjectStore struct {
	runner      *TestRunner
	testID      string
	replacement *ManagedTest
}

func (s *missingPendingProjectStore) GetProject(string) (*storage.Project, error) {
	if s.replacement != nil {
		s.runner.mu.Lock()
		s.runner.tests[s.testID] = s.replacement
		s.runner.mu.Unlock()
	}
	return nil, sql.ErrNoRows
}

func (s *flakyPendingProjectStore) GetProject(id string) (*storage.Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.remaining > 0 {
		s.remaining--
		return nil, s.err
	}
	if s.project == nil || normalizeProjectID(s.project.ID) != normalizeProjectID(id) {
		return nil, nil
	}
	clone := *s.project
	return &clone, nil
}

type blockingPendingPromotionProtocol struct {
	entered     chan<- struct{}
	release     <-chan struct{}
	enteredOnce *sync.Once
	executions  *atomic.Int32
}

func (p *blockingPendingPromotionProtocol) Name() string { return "pending-promotion-blocking" }

func (p *blockingPendingPromotionProtocol) Init(context.Context, map[string]any) error { return nil }

func (p *blockingPendingPromotionProtocol) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	if p.executions != nil {
		p.executions.Add(1)
	}
	p.enteredOnce.Do(func() { p.entered <- struct{}{} })
	select {
	case <-p.release:
		return &protocol.Result{ActionName: action.Name, Success: true}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *blockingPendingPromotionProtocol) Teardown(context.Context) error    { return nil }
func (p *blockingPendingPromotionProtocol) HealthCheck(context.Context) error { return nil }

func TestPendingPromotionRetriesTransientProjectEvidenceWithoutAnotherTrigger(t *testing.T) {
	const projectID = "pending-retry-project"
	policy := ciRunSourceTestPolicy()
	runner := NewTestRunner(2, NewHub(), nil)
	storePreflightScript(runner, "pending-retry-policy-script", projectID)
	metadata, err := encodeCIRunMetadata(ciRunMetadata{
		SchemaVersion: ciRunSchemaVersion, Source: ciTestRunSource("1"), ScriptIdentity: "script_id:pending-retry-policy-script",
		PolicyRevision: policy.Revision, IdempotencySHA: sha256String("pending-retry-policy"),
	})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := runner.CreateTest(CreateTestRequest{
		Name: "Pending transient project read", ProjectID: projectID, ScriptID: "pending-retry-policy-script",
		VUsers: 1, Duration: "1m", MaterializationKey: metadata,
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	pendingSince := time.Now().UTC().Add(-time.Minute)
	runner.mu.Lock()
	runner.tests[pending.ID].Status = "PENDING"
	runner.tests[pending.ID].PendingSince = &pendingSince
	runner.tests[pending.ID].PendingReason = "waiting for capacity"
	runner.mu.Unlock()
	runner.projectStore = &flakyPendingProjectStore{
		remaining: 1, err: errors.New("temporary project store outage"),
		project: &storage.Project{
			ID: projectID, Name: "Pending retry", UpdatedAt: time.Now().UTC(),
			Settings: storage.ProjectSettings{GitHub: storage.GitHubProjectPolicy{Actions: policy}},
		},
	}
	backend := &executionBackendSpy{called: make(chan string, 1)}
	runner.setExecutionBackend(backend)

	runner.promotePendingTests()
	select {
	case startedID := <-backend.called:
		if startedID != pending.ID {
			t.Fatalf("backend started %q, want %q", startedID, pending.ID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("term-bound pending retry did not recover the lone row after project evidence stabilized")
	}
}

func TestMissingPendingProjectEvictsOnlyObservedTestIdentity(t *testing.T) {
	for _, replace := range []bool{false, true} {
		name := "evict stale identity"
		if replace {
			name = "preserve concurrent replacement"
		}
		t.Run(name, func(t *testing.T) {
			runner := NewTestRunner(2, NewHub(), nil)
			storePreflightScript(runner, "missing-pending-project-script", "missing-pending-project")
			pending, err := runner.CreateTest(CreateTestRequest{
				Name: "missing pending project", ProjectID: "missing-pending-project",
				ScriptID: "missing-pending-project-script", VUsers: 1, Duration: "1m",
			})
			if err != nil {
				t.Fatalf("CreateTest: %v", err)
			}
			pendingSince := time.Now().UTC().Add(-time.Minute)
			runner.mu.Lock()
			old := runner.tests[pending.ID]
			old.Status = "PENDING"
			old.PendingSince = &pendingSince
			old.PendingReason = "waiting for capacity"
			runner.mu.Unlock()

			var replacement *ManagedTest
			if replace {
				replacement = &ManagedTest{
					ID: pending.ID, Name: "replacement identity", Status: "QUEUED", ProjectID: old.ProjectID,
					CreatedAt: time.Now().UTC(), TotalVUsers: 1,
				}
			}
			runner.projectStore = &missingPendingProjectStore{runner: runner, testID: pending.ID, replacement: replacement}
			runner.promotePendingTests()

			runner.mu.RLock()
			retained, exists := runner.tests[pending.ID]
			runner.mu.RUnlock()
			if replace {
				if !exists || retained != replacement {
					t.Fatalf("concurrent replacement = %#v, want exact pointer preserved", retained)
				}
			} else if exists {
				t.Fatalf("deleted-project pending ghost remained process-local: %#v", retained)
			}
		})
	}
}

func TestUnversionedPendingShakeoutEvidenceDefersOnlyThatRow(t *testing.T) {
	runner := NewTestRunner(2, NewHub(), nil)
	storePreflightScript(runner, "pending-unversioned-first", storage.DefaultProjectID)
	storePreflightScript(runner, "pending-unversioned-second", storage.DefaultProjectID)
	first, err := runner.CreateTest(CreateTestRequest{
		Name: "older unversioned shakeout evidence", ScriptID: "pending-unversioned-first", VUsers: 1, Duration: "1m", ScriptShakeout: true,
	})
	if err != nil {
		t.Fatalf("CreateTest(first): %v", err)
	}
	second, err := runner.CreateTest(CreateTestRequest{
		Name: "later promotable row", ScriptID: "pending-unversioned-second", VUsers: 1, Duration: "1m",
	})
	if err != nil {
		t.Fatalf("CreateTest(second): %v", err)
	}
	now := time.Now().UTC()
	firstPendingSince := now.Add(-2 * time.Minute)
	secondPendingSince := now.Add(-time.Minute)
	runner.mu.Lock()
	firstCurrent := runner.tests[first.ID]
	firstCurrent.Status = "PENDING"
	firstCurrent.PendingSince = &firstPendingSince
	firstCurrent.PendingReason = "waiting for capacity"
	firstCurrent.StartAuthorization = &QueuedStartAuthorization{ScriptShakeoutPass: &ScriptShakeoutPassEvidence{
		PassedAt: now.Add(-3 * time.Minute), EvidenceSHA256: sha256String("unversioned-shakeout-proof"), ReusableAfterRestart: false,
	}}
	firstCurrent.validatedScriptShakeoutPass = ""
	secondCurrent := runner.tests[second.ID]
	secondCurrent.Status = "PENDING"
	secondCurrent.PendingSince = &secondPendingSince
	secondCurrent.PendingReason = "waiting for capacity"
	runner.mu.Unlock()
	backend := &executionBackendSpy{called: make(chan string, 1)}
	runner.setExecutionBackend(backend)

	runner.promotePendingTests()
	select {
	case startedID := <-backend.called:
		if startedID != second.ID {
			t.Fatalf("backend started %q, want later promotable row %q", startedID, second.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("unversioned first row stranded a valid later pending row")
	}
	deferred, ok := runner.GetTest(first.ID)
	if !ok || deferred.Status != "PENDING" {
		t.Fatalf("unversioned row = %#v, want retained PENDING", deferred)
	}
	if !strings.HasPrefix(deferred.PendingReason, pendingScriptShakeoutManualReviewReasonPrefix) {
		t.Fatalf("pending reason = %q, want explicit no-replay/rematerialization review", deferred.PendingReason)
	}
}

func TestNonRetryablePendingShakeoutFingerprintFailureDoesNotStrandLaterRow(t *testing.T) {
	runner := NewTestRunner(2, NewHub(), nil)
	storePreflightScript(runner, "pending-conflicting-overlays", storage.DefaultProjectID)
	storePreflightScript(runner, "pending-after-conflicting-overlays", storage.DefaultProjectID)
	first, err := runner.CreateTest(CreateTestRequest{
		Name: "conflicting shakeout overlays", Duration: "1m", ScriptShakeout: true,
		Groups: []CreateGroupEntry{
			{Name: "first", ScriptID: "pending-conflicting-overlays", VUsers: 1},
			{Name: "second", ScriptID: "pending-conflicting-overlays", VUsers: 1},
		},
	})
	if err != nil {
		t.Fatalf("CreateTest(first): %v", err)
	}
	second, err := runner.CreateTest(CreateTestRequest{
		Name: "later valid row", ScriptID: "pending-after-conflicting-overlays", VUsers: 1, Duration: "1m",
	})
	if err != nil {
		t.Fatalf("CreateTest(second): %v", err)
	}
	now := time.Now().UTC()
	firstPendingSince := now.Add(-2 * time.Minute)
	secondPendingSince := now.Add(-time.Minute)
	runner.mu.Lock()
	firstCurrent := runner.tests[first.ID]
	firstCurrent.Status = "PENDING"
	firstCurrent.PendingSince = &firstPendingSince
	firstCurrent.PendingReason = "waiting for capacity"
	firstCurrent.Groups[0].runtimeSecretValues = map[string]string{"TOKEN": "first-value"}
	firstCurrent.Groups[1].runtimeSecretValues = map[string]string{"TOKEN": "different-value"}
	secondCurrent := runner.tests[second.ID]
	secondCurrent.Status = "PENDING"
	secondCurrent.PendingSince = &secondPendingSince
	secondCurrent.PendingReason = "waiting for capacity"
	runner.mu.Unlock()
	backend := &executionBackendSpy{called: make(chan string, 1)}
	runner.setExecutionBackend(backend)

	runner.promotePendingTests()
	select {
	case startedID := <-backend.called:
		if startedID != second.ID {
			t.Fatalf("backend started %q, want later valid row %q", startedID, second.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("non-retryable fingerprint failure stranded a valid later row")
	}
	failed, ok := runner.GetTest(first.ID)
	if !ok || failed.Status != "FAILED" {
		t.Fatalf("conflicting-overlay row = %#v, want terminal FAILED", failed)
	}
	if !strings.Contains(failed.Error, "conflicting executable values") {
		t.Fatalf("failure detail = %q, want conflicting executable value evidence", failed.Error)
	}
}

func TestPendingPromotionRetriesProjectRevisionCASWithoutAnotherTrigger(t *testing.T) {
	records, err := storage.NewSQLiteStore(t.TempDir() + "/pending-retry-cas.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = records.Close() })
	runner := NewTestRunner(2, NewHub(), records)
	storePreflightScript(runner, "pending-retry-cas-script", storage.DefaultProjectID)
	pending, err := runner.CreateTest(CreateTestRequest{
		Name: "Pending start CAS churn", ScriptID: "pending-retry-cas-script", VUsers: 1, Duration: "1m",
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	pendingSince := time.Now().UTC().Add(-time.Minute)
	runner.mu.Lock()
	runner.tests[pending.ID].Status = "PENDING"
	runner.tests[pending.ID].PendingSince = &pendingSince
	runner.tests[pending.ID].PendingReason = "waiting for capacity"
	runner.mu.Unlock()
	runner.testStore = &scriptedTestPersistence{
		testPersistence: records,
		err:             storage.ErrTestProjectRevisionChanged,
		failSaveCalls:   map[int]bool{1: true},
	}
	backend := &executionBackendSpy{called: make(chan string, 1)}
	runner.setExecutionBackend(backend)

	runner.promotePendingTests()
	select {
	case startedID := <-backend.called:
		if startedID != pending.ID {
			t.Fatalf("backend started %q, want %q", startedID, pending.ID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("term-bound pending retry did not recover the lone row after start CAS churn")
	}
}

func TestPendingPromotionRechecksQuotaAfterShakeout(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var enteredOnce sync.Once
	protocolName := registerRunnerTestProtocol(t, "pending-promotion-race", func() protocol.Protocol {
		return &blockingPendingPromotionProtocol{entered: entered, release: release, enteredOnce: &enteredOnce}
	})
	runner.StoreScript(&dsl.Script{
		ID:       "pending-promotion-race-script",
		Name:     "Pending promotion race",
		Protocol: protocolName,
		Version:  "1",
		Actions:  []dsl.Action{{Name: "shakeout", Type: "request", Target: "/"}},
	})

	pending, err := runner.CreateTest(CreateTestRequest{
		Name:           "Pending with shakeout",
		Duration:       "30s",
		ScriptID:       "pending-promotion-race-script",
		VUsers:         1,
		ScriptShakeout: true,
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	pendingSince := time.Now().UTC().Add(-time.Minute)
	runner.mu.Lock()
	runner.tests[pending.ID].Status = "PENDING"
	runner.tests[pending.ID].PendingSince = &pendingSince
	runner.tests[pending.ID].PendingReason = "waiting for capacity"
	runner.mu.Unlock()

	done := make(chan struct{})
	go func() {
		runner.promotePendingTests()
		close(done)
	}()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("pending promotion did not enter shakeout")
	}

	// Consume the only global slot while the promotion path is outside tr.mu.
	// The final admission check must observe this run and keep the candidate queued.
	runner.mu.Lock()
	runner.tests["quota-race-winner"] = &ManagedTest{
		ID:          "quota-race-winner",
		Name:        "Concurrent winner",
		Status:      "RUNNING",
		TotalVUsers: 1,
		Duration:    time.Minute,
		CreatedAt:   time.Now().UTC(),
	}
	runner.mu.Unlock()
	close(release)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("pending promotion did not finish after shakeout")
	}

	got, ok := runner.GetTest(pending.ID)
	if !ok {
		t.Fatalf("pending test %s disappeared", pending.ID)
	}
	if got.Status != "PENDING" {
		t.Fatalf("pending status = %q, want PENDING after quota was consumed", got.Status)
	}
	if !strings.Contains(got.PendingReason, "global test slot") {
		t.Fatalf("pending reason = %q, want refreshed global quota reason", got.PendingReason)
	}
}

func TestPendingPromotionFailsAfterScheduledAvailabilityExpires(t *testing.T) {
	runner := NewTestRunner(4, NewHub(), nil)
	started := make(chan string, 1)
	runner.setExecutionBackend(&scheduleExecutionBackend{started: started})

	now := time.Now().UTC()
	scheduledAt := now.Add(-24 * time.Hour)
	pendingSince := now.Add(-time.Minute)
	test := &ManagedTest{
		ID:               "expired-window-pending",
		Name:             "Expired scheduled run",
		Status:           "PENDING",
		ScenarioID:       "expired-window-scenario",
		ProjectID:        "expired-window-project",
		TotalVUsers:      1,
		Duration:         time.Minute,
		DurationStr:      "1m",
		CreatedAt:        scheduledAt,
		ScheduledStartAt: &scheduledAt,
		PendingSince:     &pendingSince,
		PendingReason:    "waiting for capacity",
		Groups:           []TestGroup{{Name: "load", Protocol: "http", VUsers: 1}},
	}
	runner.mu.Lock()
	runner.tests[test.ID] = test
	runner.scenarios[test.ScenarioID] = &StoredScenario{
		ID:        test.ScenarioID,
		ProjectID: test.ProjectID,
		Schedules: []*storage.ScenarioSchedule{{
			ID:         "expired-window-schedule",
			ScenarioID: test.ScenarioID,
			ProjectID:  test.ProjectID,
			Timezone:   "UTC",
			AvailabilityWindows: []storage.ScenarioScheduleWindow{{
				Name:       "scheduled day only",
				DaysOfWeek: []int{int(scheduledAt.Weekday())},
				StartTime:  "00:00",
				EndTime:    "23:59",
				Timezone:   "UTC",
			}},
		}},
	}
	runner.mu.Unlock()

	runner.promotePendingTests()

	got, ok := runner.GetTest(test.ID)
	if !ok {
		t.Fatalf("pending test %s disappeared", test.ID)
	}
	if got.Status != "FAILED" {
		t.Fatalf("status = %q, want FAILED after availability window expired", got.Status)
	}
	if !strings.Contains(got.Error, "launch window is no longer valid") || !strings.Contains(got.Error, "availability") {
		t.Fatalf("error = %q, want expired availability detail", got.Error)
	}
	select {
	case id := <-started:
		t.Fatalf("expired scheduled run %s reached execution backend", id)
	default:
	}
}

func TestPendingPromotionFailsWhenBoundTimeslotIsInvalidated(t *testing.T) {
	runner, store := storeBackedRunner(t)
	if err := store.SaveCapacityReservation(approvedTimeslot(time.Now().UTC())); err != nil {
		t.Fatalf("SaveCapacityReservation: %v", err)
	}
	timeslotID := "ts-1"
	test, err := runner.CreateTest(CreateTestRequest{
		Name:       "Timeslot pending",
		Duration:   "10m",
		ScriptID:   "script-1",
		ProjectID:  "proj-a",
		VUsers:     1,
		TimeslotID: &timeslotID,
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	pendingSince := time.Now().UTC().Add(-time.Minute)
	runner.mu.Lock()
	managed := runner.tests[test.ID]
	managed.Status = "PENDING"
	managed.PendingSince = &pendingSince
	managed.PendingReason = "waiting for capacity"
	if err := runner.saveManagedTestRecord(managed); err != nil {
		runner.mu.Unlock()
		t.Fatalf("persist pending test: %v", err)
	}
	runner.mu.Unlock()

	reservation, err := store.GetCapacityReservation(timeslotID)
	if err != nil {
		t.Fatalf("GetCapacityReservation: %v", err)
	}
	reservation.ApprovalState = "rejected"
	if err := store.SaveCapacityReservation(reservation); err != nil {
		t.Fatalf("invalidate capacity reservation: %v", err)
	}

	runner.promotePendingTests()

	got, ok := runner.GetTest(test.ID)
	if !ok {
		t.Fatalf("pending test %s disappeared", test.ID)
	}
	if got.Status != "FAILED" {
		t.Fatalf("status = %q, want FAILED after timeslot invalidation", got.Status)
	}
	if !strings.Contains(got.Error, "capacity reservation is no longer valid") || !strings.Contains(got.Error, "no longer approved") {
		t.Fatalf("error = %q, want invalidated timeslot detail", got.Error)
	}
}

func TestPendingPromotionUsesActualTimeForFutureCapacityTimeslot(t *testing.T) {
	runner, store := storeBackedRunner(t)
	futureStart := ceilToNextQuarterHour(time.Now().UTC().Add(time.Hour))
	timeslotID := "pending-future-slot"
	capacity := 100
	if err := store.SaveCapacityReservation(&storage.CapacityReservation{
		ID: timeslotID, ProjectID: "proj-a", StartAt: futureStart, EndAt: futureStart.Add(time.Hour),
		ApprovalState:     "approved",
		RequestedCapacity: &storage.CapacityReservationRequest{VUsers: &capacity},
	}); err != nil {
		t.Fatalf("SaveCapacityReservation: %v", err)
	}
	created, err := runner.CreateTest(CreateTestRequest{
		Name: "Future timeslot pending promotion", Duration: "10m", ScriptID: "script-1", VUsers: 10, ProjectID: "proj-a",
		ScheduledStartAt: &futureStart, TimeslotID: &timeslotID,
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	pendingSince := time.Now().UTC().Add(-time.Minute)
	runner.mu.Lock()
	pending := runner.tests[created.ID]
	pending.Status = "PENDING"
	pending.PendingSince = &pendingSince
	pending.PendingReason = "waiting for capacity"
	if err := runner.saveManagedTestRecord(pending); err != nil {
		runner.mu.Unlock()
		t.Fatalf("persist pending state: %v", err)
	}
	runner.mu.Unlock()

	started := make(chan string, 1)
	runner.setExecutionBackend(&scheduleExecutionBackend{started: started})
	runner.promotePendingTests()
	select {
	case id := <-started:
		t.Fatalf("future-slot pending test %q dispatched early", id)
	case <-time.After(100 * time.Millisecond):
	}
	current, ok := runner.GetTest(created.ID)
	if !ok {
		t.Fatalf("pending test %s disappeared", created.ID)
	}
	if current.Status != "FAILED" {
		t.Fatalf("status = %q, want FAILED after actual-time promotion check", current.Status)
	}
	if !strings.Contains(current.Error, "capacity reservation is no longer valid") || !strings.Contains(current.Error, "no longer fits") {
		t.Fatalf("error = %q, want actual-time timeslot failure", current.Error)
	}
}

func TestPendingCapacityOverrideSurvivesQueueAndHAReload(t *testing.T) {
	runner, store := storeBackedRunner(t)
	settings := runner.GetSystemSettings()
	settings.General.MaxConcurrentTests = 1
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 1
	settings.CapacityPlanning.Generator.MaxVUsers = 10000
	if _, err := runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings: %v", err)
	}

	active, err := runner.CreateTest(CreateTestRequest{
		Name: "Capacity slot holder", Duration: "30s", ScriptID: "script-1", ProjectID: "proj-a", VUsers: 1,
	})
	if err != nil {
		t.Fatalf("CreateTest(active): %v", err)
	}
	pending, err := runner.CreateTest(CreateTestRequest{
		Name: "Capacity override queued", Duration: "30s", ScriptID: "script-1", ProjectID: "proj-a", VUsers: 15000,
	})
	if err != nil {
		t.Fatalf("CreateTest(pending): %v", err)
	}
	runner.mu.Lock()
	runner.tests[active.ID].Status = "RUNNING"
	runner.mu.Unlock()

	actor := testCreationActor{Subject: "user:capacity-approver", DisplayName: "Capacity Approver"}
	if err := runner.StartTestWithActor(pending.ID, StartTestOptions{OverrideCapacityShortfall: true}, actor); err != nil {
		t.Fatalf("StartTestWithActor: %v", err)
	}
	queued, ok := runner.GetTest(pending.ID)
	if !ok || queued.Status != "PENDING" {
		t.Fatalf("queued test = %#v, want PENDING", queued)
	}
	if queued.StartAuthorization == nil || queued.StartAuthorization.CapacityShortfall == nil {
		t.Fatalf("start authorization = %#v, want capacity approval", queued.StartAuthorization)
	}
	capacityApproval := queued.StartAuthorization.CapacityShortfall
	if capacityApproval.ApprovedBySubject != actor.Subject || capacityApproval.TotalVUsers != 15000 || capacityApproval.ShortfallGenerators <= 0 {
		t.Fatalf("capacity approval = %#v, want bounded actor/workload provenance", capacityApproval)
	}

	// A standby reconstructs the approval solely from the shared durable row.
	// This exercises the same decode/load path used during HA acquisition without
	// allowing the standby to reconcile or promote execution state.
	follower := newExecutionLeadership(store, "capacity-authorization-follower")
	reloadedRunner := NewTestRunner(1, NewHub(), store, WithExecutionLeadership(follower))
	reloaded, ok := reloadedRunner.GetTest(pending.ID)
	if !ok || reloaded.StartAuthorization == nil || reloaded.StartAuthorization.CapacityShortfall == nil {
		t.Fatalf("reloaded start authorization = %#v, want persisted capacity approval", reloaded)
	}
	if got := reloaded.StartAuthorization.CapacityShortfall; got.ApprovedBySubject != actor.Subject || got.TotalVUsers != 15000 || got.ShortfallGenerators != capacityApproval.ShortfallGenerators {
		t.Fatalf("reloaded capacity approval = %#v, want %#v", got, capacityApproval)
	}

	started := make(chan string, 1)
	runner.setExecutionBackend(&scheduleExecutionBackend{started: started})
	runner.mu.Lock()
	runner.tests[active.ID].Status = "COMPLETE"
	runner.mu.Unlock()
	runner.promotePendingTests()

	select {
	case gotID := <-started:
		if gotID != pending.ID {
			t.Fatalf("started test = %q, want %q", gotID, pending.ID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("capacity-authorized pending test was not promoted")
	}
	startedTest, _ := runner.GetTest(pending.ID)
	if startedTest.Status != "INITIALIZING" {
		t.Fatalf("status = %q, want INITIALIZING after authorized promotion", startedTest.Status)
	}
}

func TestPendingCapacityOverrideRequiresReapprovalWhenShortfallWorsens(t *testing.T) {
	runner, _ := storeBackedRunner(t)
	settings := runner.GetSystemSettings()
	settings.General.MaxConcurrentTests = 1
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 1
	settings.CapacityPlanning.Generator.MaxVUsers = 10000
	if _, err := runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings(initial): %v", err)
	}

	active, err := runner.CreateTest(CreateTestRequest{
		Name: "Capacity slot holder", Duration: "30s", ScriptID: "script-1", ProjectID: "proj-a", VUsers: 1,
	})
	if err != nil {
		t.Fatalf("CreateTest(active): %v", err)
	}
	pending, err := runner.CreateTest(CreateTestRequest{
		Name: "Capacity reapproval queued", Duration: "30s", ScriptID: "script-1", ProjectID: "proj-a", VUsers: 15000,
	})
	if err != nil {
		t.Fatalf("CreateTest(pending): %v", err)
	}
	runner.mu.Lock()
	runner.tests[active.ID].Status = "RUNNING"
	runner.mu.Unlock()

	actor := testCreationActor{Subject: "user:capacity-approver", DisplayName: "Capacity Approver"}
	if err := runner.StartTestWithActor(pending.ID, StartTestOptions{OverrideCapacityShortfall: true}, actor); err != nil {
		t.Fatalf("StartTestWithActor(initial approval): %v", err)
	}
	queued, ok := runner.GetTest(pending.ID)
	if !ok || queued.Status != "PENDING" || queued.StartAuthorization == nil || queued.StartAuthorization.CapacityShortfall == nil {
		t.Fatalf("queued test = %#v, want PENDING with capacity approval", queued)
	}
	initialApproval := queued.StartAuthorization.CapacityShortfall
	if initialApproval.RecommendedGenerators != 2 || initialApproval.ShortfallGenerators != 1 {
		t.Fatalf("initial capacity approval = %#v, want 2 recommended/1 shortfall", initialApproval)
	}

	// A lower per-generator density makes the same immutable test require one
	// additional generator. The previous approval must not silently expand to
	// cover that larger shortfall when the global test slot becomes available.
	settings = runner.GetSystemSettings()
	settings.CapacityPlanning.Generator.MaxVUsers = 5000
	if _, err := runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings(worsened capacity): %v", err)
	}
	started := make(chan string, 1)
	runner.setExecutionBackend(&scheduleExecutionBackend{started: started})
	runner.mu.Lock()
	runner.tests[active.ID].Status = "COMPLETE"
	runner.mu.Unlock()
	runner.promotePendingTests()

	select {
	case id := <-started:
		t.Fatalf("test %q dispatched with a stale capacity approval", id)
	case <-time.After(100 * time.Millisecond):
	}
	reapproval, ok := runner.GetTest(pending.ID)
	if !ok || reapproval.Status != "PENDING" {
		t.Fatalf("test after worsened capacity = %#v, want PENDING", reapproval)
	}
	if !strings.HasPrefix(reapproval.PendingReason, pendingCapacityAuthorizationReasonPrefix) {
		t.Fatalf("pending reason = %q, want explicit capacity reapproval reason", reapproval.PendingReason)
	}
	if reapproval.CapacityAdvisory == nil || reapproval.CapacityAdvisory.RecommendedGenerators <= initialApproval.RecommendedGenerators || reapproval.CapacityAdvisory.ShortfallGenerators <= initialApproval.ShortfallGenerators {
		t.Fatalf("worsened capacity advisory = %#v, want larger recommendation and shortfall than %#v", reapproval.CapacityAdvisory, initialApproval)
	}
	worsenedAdvisory := cloneCapacityAdvisory(reapproval.CapacityAdvisory)
	if got := reapproval.StartAuthorization.CapacityShortfall; got.RecommendedGenerators != 2 || got.ShortfallGenerators != 1 {
		t.Fatalf("stale authorization was silently expanded: %#v", got)
	}

	if err := runner.StartTestWithActor(pending.ID, StartTestOptions{OverrideCapacityShortfall: true}, actor); err != nil {
		t.Fatalf("StartTestWithActor(reapproval): %v", err)
	}
	select {
	case gotID := <-started:
		if gotID != pending.ID {
			t.Fatalf("started test = %q, want %q", gotID, pending.ID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("capacity-reapproved pending test was not dispatched")
	}
	startedTest, _ := runner.GetTest(pending.ID)
	if startedTest.Status != "INITIALIZING" {
		t.Fatalf("status = %q, want INITIALIZING after reapproval", startedTest.Status)
	}
	updatedApproval := startedTest.StartAuthorization.CapacityShortfall
	if updatedApproval.RecommendedGenerators != worsenedAdvisory.RecommendedGenerators || updatedApproval.ShortfallGenerators != worsenedAdvisory.ShortfallGenerators {
		t.Fatalf("updated capacity approval = %#v, want bounds from %#v", updatedApproval, worsenedAdvisory)
	}
}

func TestPendingShakeoutOverrideSurvivesQueueAndHAReload(t *testing.T) {
	runner, store := storeBackedRunner(t)
	settings := runner.GetSystemSettings()
	settings.General.MaxConcurrentTests = 1
	if _, err := runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings: %v", err)
	}
	runner.StoreScript(&dsl.Script{
		ID:        "pending-shakeout-override-script",
		Name:      "Pending shakeout override",
		ProjectID: "proj-a",
		Protocol:  "definitely-not-supported",
		Version:   "1",
		Actions:   []dsl.Action{{Name: "broken", Type: "request", Target: "/"}},
	})

	active, err := runner.CreateTest(CreateTestRequest{
		Name: "Shakeout slot holder", Duration: "30s", ScriptID: "script-1", ProjectID: "proj-a", VUsers: 1,
	})
	if err != nil {
		t.Fatalf("CreateTest(active): %v", err)
	}
	review, err := runner.CreateTest(CreateTestRequest{
		Name:           "Shakeout override queued",
		Duration:       "30s",
		ScriptID:       "pending-shakeout-override-script",
		ProjectID:      "proj-a",
		VUsers:         1,
		ScriptShakeout: true,
	})
	if err != nil {
		t.Fatalf("CreateTest(review): %v", err)
	}
	if err := runner.StartTest(review.ID); err == nil {
		t.Fatal("initial failing shakeout returned nil")
	}
	blocked, _ := runner.GetTest(review.ID)
	if blocked.Status != "SHAKEOUT_REVIEW" || strings.TrimSpace(blocked.Error) == "" {
		t.Fatalf("shakeout result = %#v, want SHAKEOUT_REVIEW evidence", blocked)
	}
	wantEvidenceHash := sha256String(strings.TrimSpace(blocked.Error))

	runner.mu.Lock()
	runner.tests[active.ID].Status = "RUNNING"
	runner.mu.Unlock()
	actor := testCreationActor{Subject: "user:shakeout-approver", DisplayName: "Shakeout Approver"}
	if err := runner.StartTestWithActor(review.ID, StartTestOptions{OverrideScriptShakeoutError: true}, actor); err != nil {
		t.Fatalf("StartTestWithActor(override): %v", err)
	}
	queued, _ := runner.GetTest(review.ID)
	if queued.Status != "PENDING" || queued.StartAuthorization == nil || queued.StartAuthorization.ScriptShakeout == nil {
		t.Fatalf("queued test = %#v, want PENDING with shakeout approval", queued)
	}
	shakeoutApproval := queued.StartAuthorization.ScriptShakeout
	if shakeoutApproval.ApprovedBySubject != actor.Subject || shakeoutApproval.ReviewEvidenceSHA256 != wantEvidenceHash {
		t.Fatalf("shakeout approval = %#v, want actor and reviewed-evidence hash %q", shakeoutApproval, wantEvidenceHash)
	}

	follower := newExecutionLeadership(store, "shakeout-authorization-follower")
	reloadedRunner := NewTestRunner(1, NewHub(), store, WithExecutionLeadership(follower))
	reloaded, ok := reloadedRunner.GetTest(review.ID)
	if !ok || reloaded.StartAuthorization == nil || reloaded.StartAuthorization.ScriptShakeout == nil {
		t.Fatalf("reloaded test = %#v, want persisted shakeout approval", reloaded)
	}
	if got := reloaded.StartAuthorization.ScriptShakeout; got.ApprovedBySubject != actor.Subject || got.ReviewEvidenceSHA256 != wantEvidenceHash {
		t.Fatalf("reloaded shakeout approval = %#v, want actor/evidence provenance", got)
	}

	started := make(chan string, 1)
	runner.setExecutionBackend(&scheduleExecutionBackend{started: started})
	runner.mu.Lock()
	runner.tests[active.ID].Status = "COMPLETE"
	runner.mu.Unlock()
	runner.promotePendingTests()
	select {
	case gotID := <-started:
		if gotID != review.ID {
			t.Fatalf("started test = %q, want %q", gotID, review.ID)
		}
	case <-time.After(5 * time.Second):
		latest, _ := runner.GetTest(review.ID)
		t.Fatalf("shakeout-authorized pending test was not promoted; status=%q error=%q", latest.Status, latest.Error)
	}
	startedTest, _ := runner.GetTest(review.ID)
	if startedTest.Status != "INITIALIZING" {
		t.Fatalf("status = %q, want INITIALIZING; persisted approval must skip shakeout rerun", startedTest.Status)
	}
}

func TestCapacityStartAuthorizationIsBounded(t *testing.T) {
	now := time.Now().UTC()
	authorization := &CapacityShortfallStartAuthorization{
		ApprovedAt:            now,
		TotalVUsers:           15000,
		RecommendedGenerators: 2,
		AvailableGenerators:   1,
		ShortfallGenerators:   1,
	}
	tests := []struct {
		name     string
		advisory CapacityAdvisory
		want     bool
	}{
		{name: "same reviewed shortfall", advisory: CapacityAdvisory{RequiresOverride: true, TotalVUsers: 15000, RecommendedGenerators: 2, AvailableGenerators: 1, ShortfallGenerators: 1}, want: true},
		{name: "improved shortfall", advisory: CapacityAdvisory{RequiresOverride: true, TotalVUsers: 15000, RecommendedGenerators: 2, AvailableGenerators: 2, ShortfallGenerators: 0}, want: true},
		{name: "changed workload", advisory: CapacityAdvisory{RequiresOverride: true, TotalVUsers: 16000, RecommendedGenerators: 2, AvailableGenerators: 1, ShortfallGenerators: 1}, want: false},
		{name: "larger recommendation", advisory: CapacityAdvisory{RequiresOverride: true, TotalVUsers: 15000, RecommendedGenerators: 3, AvailableGenerators: 2, ShortfallGenerators: 1}, want: false},
		{name: "larger shortfall", advisory: CapacityAdvisory{RequiresOverride: true, TotalVUsers: 15000, RecommendedGenerators: 2, AvailableGenerators: 0, ShortfallGenerators: 2}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := capacityStartAuthorizationAllows(authorization, &test.advisory); got != test.want {
				t.Fatalf("capacityStartAuthorizationAllows() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestPendingPromotionRevalidatesRequiredSecrets(t *testing.T) {
	const (
		projectID  = "pending-promotion-secret-project"
		secretName = "API_TOKEN"
	)
	secretEnvironmentName := engine.ProjectSecretEnvironmentName(projectID, secretName)
	t.Setenv(secretEnvironmentName, "available-while-queued")

	runner := NewTestRunner(1, NewHub(), nil)
	runner.StoreScript(&dsl.Script{
		ID:        "pending-promotion-secret-script",
		Name:      "Pending promotion secret",
		ProjectID: projectID,
		Protocol:  "http",
		Version:   "1",
		Actions: []dsl.Action{{
			Name:    "authorized request",
			Type:    "GET",
			Target:  "/",
			Headers: map[string]string{"Authorization": "Bearer {{secret.API_TOKEN}}"},
		}},
	})
	active, err := runner.CreateTest(CreateTestRequest{
		Name: "Secret slot holder", Duration: "30s", ScriptID: "pending-promotion-secret-script", ProjectID: projectID, VUsers: 1,
	})
	if err != nil {
		t.Fatalf("CreateTest(active): %v", err)
	}
	pending, err := runner.CreateTest(CreateTestRequest{
		Name: "Secret queued run", Duration: "30s", ScriptID: "pending-promotion-secret-script", ProjectID: projectID, VUsers: 1,
	})
	if err != nil {
		t.Fatalf("CreateTest(pending): %v", err)
	}
	runner.mu.Lock()
	runner.tests[active.ID].Status = "RUNNING"
	runner.mu.Unlock()
	if err := runner.StartTest(pending.ID); err != nil {
		t.Fatalf("StartTest(pending): %v", err)
	}
	queued, _ := runner.GetTest(pending.ID)
	if queued.Status != "PENDING" {
		t.Fatalf("status = %q, want PENDING while the global slot is occupied", queued.Status)
	}

	// Simulate rotation/removal after the start request was accepted into the
	// queue but before capacity became available.
	if err := os.Unsetenv(secretEnvironmentName); err != nil {
		t.Fatalf("Unsetenv(%s): %v", secretEnvironmentName, err)
	}
	started := make(chan string, 1)
	runner.setExecutionBackend(&scheduleExecutionBackend{started: started})
	runner.mu.Lock()
	runner.tests[active.ID].Status = "COMPLETE"
	runner.mu.Unlock()
	runner.promotePendingTests()

	deferred, ok := runner.GetTest(pending.ID)
	if !ok {
		t.Fatalf("pending test %s disappeared", pending.ID)
	}
	if deferred.Status != "PENDING" {
		t.Fatalf("status = %q, want PENDING after correctable required secret removal", deferred.Status)
	}
	if !strings.Contains(deferred.PendingReason, "secret requirements are no longer satisfied") || !strings.Contains(deferred.PendingReason, secretName) {
		t.Fatalf("pending reason = %q, want durable promotion secret preflight detail", deferred.PendingReason)
	}
	select {
	case id := <-started:
		t.Fatalf("backend launched pending test %q after required secret removal", id)
	default:
	}
	if err := os.Setenv(secretEnvironmentName, "restored-without-controller-signal"); err != nil {
		t.Fatalf("Setenv(%s): %v", secretEnvironmentName, err)
	}
	select {
	case id := <-started:
		if id != pending.ID {
			t.Fatalf("backend launched %q, want %q", id, pending.ID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("term-bound retry cadence did not observe the repaired external secret")
	}
}

func TestPendingPromotionRevalidatesRequiredSecretsAfterShakeout(t *testing.T) {
	const (
		projectID  = "pending-shakeout-secret-project"
		secretName = "SESSION_TOKEN"
	)
	secretStore := &versionedShakeoutSecretStore{
		value: "available-at-shakeout-start",
		metadata: secretstore.Metadata{
			ProjectID: projectID, Name: secretName, Provider: "versioned-test", UpdatedAt: time.Now().UTC(),
		},
	}

	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var enteredOnce sync.Once
	var executions atomic.Int32
	protocolName := registerRunnerTestProtocol(t, "pending-shakeout-secret", func() protocol.Protocol {
		return &blockingPendingPromotionProtocol{entered: entered, release: release, enteredOnce: &enteredOnce, executions: &executions}
	})
	runner := NewTestRunner(1, NewHub(), nil, WithProjectSecretStore(secretStore))
	runner.StoreScript(&dsl.Script{
		ID:        "pending-shakeout-secret-script",
		Name:      "Pending shakeout secret race",
		ProjectID: projectID,
		Protocol:  protocolName,
		Version:   "1",
		Actions: []dsl.Action{{
			Name:    "authorized shakeout request",
			Type:    "request",
			Target:  "/",
			Headers: map[string]string{"Authorization": "Bearer {{secret.SESSION_TOKEN}}"},
		}},
	})
	pending, err := runner.CreateTest(CreateTestRequest{
		Name:           "Secret removed during shakeout",
		Duration:       "30s",
		ScriptID:       "pending-shakeout-secret-script",
		ProjectID:      projectID,
		VUsers:         1,
		ScriptShakeout: true,
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	pendingSince := time.Now().UTC().Add(-time.Minute)
	runner.mu.Lock()
	runner.tests[pending.ID].Status = "PENDING"
	runner.tests[pending.ID].PendingSince = &pendingSince
	runner.tests[pending.ID].PendingReason = "waiting for capacity"
	runner.mu.Unlock()
	started := make(chan string, 1)
	runner.setExecutionBackend(&scheduleExecutionBackend{started: started})

	done := make(chan struct{})
	go func() {
		runner.promotePendingTests()
		close(done)
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("pending promotion did not enter shakeout with the configured secret")
	}
	// The action already resolved and used the secret to enter the protocol.
	// Remove it while shakeout is outside tr.mu; the post-shakeout check must
	// observe the removal before INITIALIZING.
	secretStore.mu.Lock()
	secretStore.value = ""
	secretStore.mu.Unlock()
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("pending promotion did not finish after shakeout release")
	}

	runner.mu.RLock()
	deferred := runner.tests[pending.ID]
	verificationPending := deferred != nil && deferred.Status == "SHAKEOUT" && deferred.executionCtx == nil &&
		deferred.pendingScriptShakeoutOutcome != nil && deferred.pendingScriptShakeoutOutcome.VerificationPending &&
		deferred.pendingScriptShakeoutOutcome.PassStatus == "PENDING"
	unverifiedPassPublished := deferred != nil && deferred.StartAuthorization != nil &&
		scriptShakeoutPassAllows(deferred.StartAuthorization.ScriptShakeoutPass)
	runner.mu.RUnlock()
	if !verificationPending {
		t.Fatalf("post-action secret outage state = %#v, want private SHAKEOUT verification-pending outcome", deferred)
	}
	if unverifiedPassPublished {
		t.Fatalf("unverified post-action fingerprint was published as a reusable pass: %#v", deferred.StartAuthorization)
	}
	secretStore.mu.Lock()
	secretStore.value = "available-at-shakeout-start"
	secretStore.mu.Unlock()
	select {
	case id := <-started:
		if id != pending.ID {
			t.Fatalf("backend launched %q, want %q", id, pending.ID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("term-bound retry cadence did not resume promotion after the secret was restored")
	}
	if got := executions.Load(); got != 1 {
		t.Fatalf("shakeout action executions after secret-gate retry = %d, want one", got)
	}
}

func TestPendingPromotionTerminalizesMalformedSecretReferences(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	runner.StoreScript(&dsl.Script{
		ID:       "pending-malformed-secret-script",
		Name:     "Pending malformed secret",
		Protocol: "http",
		Version:  "1",
		Actions:  []dsl.Action{{Name: "request", Type: "GET", Target: "/"}},
	})
	pending, err := runner.CreateTest(CreateTestRequest{
		Name: "Malformed immutable secret reference", Duration: "30s", ScriptID: "pending-malformed-secret-script", VUsers: 1,
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	pendingSince := time.Now().UTC().Add(-time.Minute)
	runner.mu.Lock()
	managed := runner.tests[pending.ID]
	managed.Status = "PENDING"
	managed.PendingSince = &pendingSince
	managed.PendingReason = "waiting for capacity"
	// Simulate an immutable snapshot restored from legacy/corrupt storage. This
	// cannot be fixed by configuring a provider value, unlike a missing name.
	managed.groupSpecs[0].Script.Actions[0].Body = "{{secret.BAD NAME}}"
	runner.mu.Unlock()
	started := make(chan string, 1)
	runner.setExecutionBackend(&scheduleExecutionBackend{started: started})

	runner.promotePendingTests()
	failed, ok := runner.GetTest(pending.ID)
	if !ok {
		t.Fatalf("pending test %s disappeared", pending.ID)
	}
	if failed.Status != "FAILED" {
		t.Fatalf("status = %q, want FAILED for immutable malformed secret reference", failed.Status)
	}
	if !strings.Contains(failed.Error, "secret requirements are no longer satisfied") || !strings.Contains(failed.Error, "inspect secret references") {
		t.Fatalf("error = %q, want terminal secret-reference evidence", failed.Error)
	}
	select {
	case id := <-started:
		t.Fatalf("backend launched malformed-secret pending test %q", id)
	default:
	}
}

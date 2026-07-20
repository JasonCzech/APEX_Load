package api

import (
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

// sqliteScenarioQuotaRaceStore consumes the last controller slot immediately
// after the recurring run's durable create. That puts the scheduler through its
// real QUEUED -> PENDING path while retaining production SQLite lifecycle
// transactions for the later promotion/failure.
type sqliteScenarioQuotaRaceStore struct {
	*storage.SQLiteStore
	runner                  *TestRunner
	once                    sync.Once
	forcedOccurrenceReadErr error
}

func (s *sqliteScenarioQuotaRaceStore) CreateTestIfAbsent(test *storage.TestRecord) (bool, error) {
	created, err := s.SQLiteStore.CreateTestIfAbsent(test)
	if err == nil && created && test != nil && strings.EqualFold(test.Status, "QUEUED") && s.runner != nil {
		s.once.Do(func() {
			s.runner.mu.Lock()
			s.runner.tests["scenario-pending-quota-holder"] = &ManagedTest{
				ID: "scenario-pending-quota-holder", Name: "Quota holder", Status: "RUNNING", TotalVUsers: 1,
			}
			s.runner.mu.Unlock()
		})
	}
	return created, err
}

func (s *sqliteScenarioQuotaRaceStore) GetScenarioScheduleOccurrenceByTestID(testID string) (*storage.ScenarioScheduleOccurrence, error) {
	if s.forcedOccurrenceReadErr != nil {
		return nil, s.forcedOccurrenceReadErr
	}
	return s.SQLiteStore.GetScenarioScheduleOccurrenceByTestID(testID)
}

type recurringQuotaPendingFixture struct {
	runner      *TestRunner
	store       *storage.SQLiteStore
	persistence *sqliteScenarioQuotaRaceStore
	scenarioID  string
	scheduleID  string
	testID      string
}

func newRecurringQuotaPendingFixture(t *testing.T) recurringQuotaPendingFixture {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	raceStore := &sqliteScenarioQuotaRaceStore{SQLiteStore: store}
	runner := NewTestRunner(1, NewHub(), raceStore)
	raceStore.runner = runner
	runner.StoreScript(&dsl.Script{
		ID: "scenario-pending-lifecycle-script", Name: "Scenario pending lifecycle", Protocol: "http", Version: "1",
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "request", Type: "request", Method: "GET", Target: "/"}},
	})
	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		// The duration intentionally overlaps the next one-minute recurrence.
		// Promotion must exclude this occurrence's own parent schedule during
		// launch-window revalidation instead of terminalizing on self-conflict.
		Name: "Scenario pending lifecycle", Duration: "2m",
		Groups: []CreateGroupEntry{{Name: "Primary", ScriptID: "scenario-pending-lifecycle-script", VUsers: 1}},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	// Anchor the sweep exactly on a cron boundary. With a two-minute run and a
	// one-minute recurrence, the next projection always overlaps; the regression
	// does not depend on which second of the wall-clock minute executes the test.
	now := time.Now().UTC().Truncate(time.Minute)
	schedule := &storage.ScenarioSchedule{
		ID: "scenario-pending-lifecycle-schedule", ScenarioID: scenario.ID, ProjectID: storage.DefaultProjectID,
		Revision: 1, Name: "Recurring quota pending", CronExpr: "* * * * *", Timezone: "UTC", Enabled: true,
		NextRunAt: &now, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	}
	runner.mu.Lock()
	if err := runner.saveScenarioScheduleLocked(schedule); err != nil {
		runner.mu.Unlock()
		t.Fatalf("saveScenarioScheduleLocked: %v", err)
	}
	runner.mu.Unlock()

	scheduler := newScenarioScheduler(runner)
	scheduler.now = func() time.Time { return now }
	if err := scheduler.RunDueSchedules(); err != nil {
		t.Fatalf("RunDueSchedules: %v", err)
	}
	occurrences, err := store.ListScenarioScheduleOccurrences(schedule.ID)
	if err != nil {
		t.Fatalf("ListScenarioScheduleOccurrences: %v", err)
	}
	if len(occurrences) != 1 || occurrences[0].Status != "queued" || occurrences[0].TestID == "" {
		t.Fatalf("quota-pending occurrence = %#v, want one queued test", occurrences)
	}
	pending, ok := runner.GetTest(occurrences[0].TestID)
	if !ok || pending.Status != "PENDING" {
		t.Fatalf("scheduled test = %#v, want PENDING", pending)
	}
	return recurringQuotaPendingFixture{
		runner: runner, store: store, persistence: raceStore, scenarioID: scenario.ID, scheduleID: schedule.ID, testID: pending.ID,
	}
}

func (fixture recurringQuotaPendingFixture) releaseQuota() {
	fixture.runner.mu.Lock()
	delete(fixture.runner.tests, "scenario-pending-quota-holder")
	fixture.runner.mu.Unlock()
}

func addNewerScenarioScheduleOccurrence(t *testing.T, fixture recurringQuotaPendingFixture) *storage.ScenarioScheduleOccurrence {
	t.Helper()
	older, err := fixture.store.GetScenarioScheduleOccurrenceByTestID(fixture.testID)
	if err != nil || older == nil {
		t.Fatalf("GetScenarioScheduleOccurrenceByTestID = (%#v, %v), want queued occurrence", older, err)
	}
	newer := &storage.ScenarioScheduleOccurrence{
		ID: "scenario-pending-lifecycle-newer-" + fixture.testID, ScheduleID: fixture.scheduleID,
		ScenarioID: fixture.scenarioID, ProjectID: storage.DefaultProjectID,
		ScheduleRevision: older.ScheduleRevision, PlannedAt: older.PlannedAt.Add(time.Minute),
		Status: "blocked", ReasonCode: "schedule_conflict", ConflictSummary: "newer projected occurrence",
		CreatedAt: older.CreatedAt.Add(time.Minute), UpdatedAt: older.UpdatedAt.Add(time.Minute),
	}
	if err := fixture.store.SaveScenarioScheduleOccurrence(newer); err != nil {
		t.Fatalf("SaveScenarioScheduleOccurrence(newer): %v", err)
	}
	return newer
}

func TestRecurringQuotaPendingOccurrenceReconcilesWhenPromoted(t *testing.T) {
	fixture := newRecurringQuotaPendingFixture(t)
	started := make(chan string, 1)
	fixture.runner.setExecutionBackend(&scheduleExecutionBackend{started: started})
	fixture.releaseQuota()

	fixture.runner.promotePendingTests()

	occurrence, err := fixture.store.GetScenarioScheduleOccurrenceByTestID(fixture.testID)
	if err != nil {
		t.Fatalf("GetScenarioScheduleOccurrenceByTestID: %v", err)
	}
	if occurrence == nil || occurrence.Status != "materialized" || occurrence.ReasonCode != "started" || occurrence.MaterializedAt == nil {
		t.Fatalf("promoted occurrence = %#v, want materialized/started with timestamp", occurrence)
	}
	if occurrence.ConflictSummary != "" {
		t.Fatalf("promoted occurrence conflict = %q, want empty", occurrence.ConflictSummary)
	}
	schedule, err := fixture.store.GetScenarioSchedule(fixture.scheduleID)
	if err != nil {
		t.Fatalf("GetScenarioSchedule: %v", err)
	}
	if schedule.LastRunTestID != fixture.testID || schedule.LastRunAt == nil || !schedule.LastRunAt.Equal(*occurrence.MaterializedAt) {
		t.Fatalf("promoted schedule lifecycle = test %q at %v, want %q at %v", schedule.LastRunTestID, schedule.LastRunAt, fixture.testID, occurrence.MaterializedAt)
	}
	fixture.runner.mu.RLock()
	cached := cloneScenarioSchedule(fixture.runner.scenarios[fixture.scenarioID].Schedules[0])
	fixture.runner.mu.RUnlock()
	if cached.LastOccurrence == nil || cached.LastOccurrence.Status != "materialized" || cached.LastRunAt == nil {
		t.Fatalf("cached promoted schedule = %#v, want reconciled lifecycle", cached)
	}
	select {
	case id := <-started:
		if id != fixture.testID {
			t.Fatalf("started test = %q, want %q", id, fixture.testID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("promoted recurring test did not reach the execution backend")
	}
}

func TestRecurringQuotaPendingOccurrenceLookupFailureDefersPromotion(t *testing.T) {
	fixture := newRecurringQuotaPendingFixture(t)
	fixture.releaseQuota()
	fixture.persistence.forcedOccurrenceReadErr = errors.New("temporary occurrence lookup failure")

	fixture.runner.promotePendingTests()

	pending, ok := fixture.runner.GetTest(fixture.testID)
	if !ok || pending.Status != "PENDING" {
		t.Fatalf("test after occurrence lookup failure = %#v, want PENDING", pending)
	}
	occurrence, err := fixture.store.GetScenarioScheduleOccurrenceByTestID(fixture.testID)
	if err != nil {
		t.Fatalf("GetScenarioScheduleOccurrenceByTestID: %v", err)
	}
	if occurrence == nil || occurrence.Status != "queued" || occurrence.MaterializedAt != nil {
		t.Fatalf("occurrence after lookup failure = %#v, want queued/non-materialized", occurrence)
	}
	schedule, err := fixture.store.GetScenarioSchedule(fixture.scheduleID)
	if err != nil {
		t.Fatalf("GetScenarioSchedule: %v", err)
	}
	if schedule.LastRunTestID != fixture.testID || schedule.LastRunAt != nil {
		t.Fatalf("schedule after lookup failure = test %q at %v, want pending pointer without LastRunAt", schedule.LastRunTestID, schedule.LastRunAt)
	}
}

func TestRecurringQuotaPendingLifecycleCacheUsesLatestPlannedOccurrence(t *testing.T) {
	tests := []struct {
		name             string
		transition       func(*testing.T, recurringQuotaPendingFixture)
		correlatedStatus string
		correlatedReason string
	}{
		{
			name: "promoted",
			transition: func(t *testing.T, fixture recurringQuotaPendingFixture) {
				fixture.runner.setExecutionBackend(&scheduleExecutionBackend{started: make(chan string, 1)})
				fixture.releaseQuota()
				fixture.runner.promotePendingTests()
			},
			correlatedStatus: "materialized",
			correlatedReason: "started",
		},
		{
			name: "failed",
			transition: func(t *testing.T, fixture recurringQuotaPendingFixture) {
				fixture.releaseQuota()
				fixture.runner.mu.Lock()
				fixture.runner.tests[fixture.testID].groupSpecs[0].Script.Actions[0].Body = "{{secret.BAD NAME}}"
				fixture.runner.mu.Unlock()
				fixture.runner.promotePendingTests()
			},
			correlatedStatus: "failed",
			correlatedReason: "pending_promotion_failed",
		},
		{
			name: "aborted",
			transition: func(t *testing.T, fixture recurringQuotaPendingFixture) {
				if err := fixture.runner.StopTest(fixture.testID); err != nil {
					t.Fatalf("StopTest: %v", err)
				}
			},
			correlatedStatus: "failed",
			correlatedReason: "pending_promotion_aborted",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRecurringQuotaPendingFixture(t)
			newer := addNewerScenarioScheduleOccurrence(t, fixture)

			test.transition(t, fixture)

			correlated, err := fixture.store.GetScenarioScheduleOccurrenceByTestID(fixture.testID)
			if err != nil {
				t.Fatalf("GetScenarioScheduleOccurrenceByTestID: %v", err)
			}
			if correlated == nil || correlated.Status != test.correlatedStatus || correlated.ReasonCode != test.correlatedReason {
				t.Fatalf("correlated occurrence = %#v, want %s/%s", correlated, test.correlatedStatus, test.correlatedReason)
			}
			latest, err := fixture.store.LatestScenarioScheduleOccurrence(fixture.scheduleID)
			if err != nil {
				t.Fatalf("LatestScenarioScheduleOccurrence: %v", err)
			}
			if latest == nil || latest.ID != newer.ID {
				t.Fatalf("durable latest occurrence = %#v, want %q", latest, newer.ID)
			}
			fixture.runner.mu.RLock()
			cached := cloneScenarioSchedule(fixture.runner.scenarios[fixture.scenarioID].Schedules[0])
			fixture.runner.mu.RUnlock()
			if cached.LastOccurrence == nil || cached.LastOccurrence.ID != latest.ID || !cached.LastOccurrence.PlannedAt.Equal(latest.PlannedAt) {
				t.Fatalf("cached LastOccurrence = %#v, want durable latest %#v", cached.LastOccurrence, latest)
			}
		})
	}
}

func TestRecurringQuotaPendingOccurrenceReconcilesTerminalPromotionFailure(t *testing.T) {
	fixture := newRecurringQuotaPendingFixture(t)
	fixture.releaseQuota()
	fixture.runner.mu.Lock()
	managed := fixture.runner.tests[fixture.testID]
	// Simulate a malformed immutable snapshot restored from legacy/corrupt
	// storage. Unlike a missing secret value, this promotion gate is terminal.
	managed.groupSpecs[0].Script.Actions[0].Body = "{{secret.BAD NAME}}"
	fixture.runner.mu.Unlock()

	fixture.runner.promotePendingTests()

	failed, ok := fixture.runner.GetTest(fixture.testID)
	if !ok || failed.Status != "FAILED" {
		t.Fatalf("terminalized test = %#v, want FAILED", failed)
	}
	if !strings.Contains(failed.Error, "secret requirements are no longer satisfied") {
		t.Fatalf("terminalized test error = %q, want promotion gate detail", failed.Error)
	}
	occurrence, err := fixture.store.GetScenarioScheduleOccurrenceByTestID(fixture.testID)
	if err != nil {
		t.Fatalf("GetScenarioScheduleOccurrenceByTestID: %v", err)
	}
	if occurrence == nil || occurrence.Status != "failed" || occurrence.ReasonCode != "pending_promotion_failed" || occurrence.MaterializedAt != nil {
		t.Fatalf("failed occurrence = %#v, want terminal pre-launch failure without materialized_at", occurrence)
	}
	if !strings.Contains(occurrence.ConflictSummary, "secret requirements are no longer satisfied") {
		t.Fatalf("failed occurrence summary = %q, want promotion gate detail", occurrence.ConflictSummary)
	}
	schedule, err := fixture.store.GetScenarioSchedule(fixture.scheduleID)
	if err != nil {
		t.Fatalf("GetScenarioSchedule: %v", err)
	}
	if schedule.LastRunAt != nil {
		t.Fatalf("failed pre-launch schedule LastRunAt = %v, want nil", schedule.LastRunAt)
	}
	if schedule.NextRequiredAction != "operator_decision_required" || !strings.Contains(schedule.LastError, "secret requirements are no longer satisfied") {
		t.Fatalf("failed schedule action/error = %q / %q", schedule.NextRequiredAction, schedule.LastError)
	}
	fixture.runner.mu.RLock()
	cached := cloneScenarioSchedule(fixture.runner.scenarios[fixture.scenarioID].Schedules[0])
	fixture.runner.mu.RUnlock()
	if cached.LastOccurrence == nil || cached.LastOccurrence.Status != "failed" || cached.LastRunAt != nil {
		t.Fatalf("cached failed schedule = %#v, want reconciled failure", cached)
	}
}

func TestRecurringQuotaPendingOccurrenceReconcilesUserCancellation(t *testing.T) {
	fixture := newRecurringQuotaPendingFixture(t)

	if err := fixture.runner.StopTest(fixture.testID); err != nil {
		t.Fatalf("StopTest: %v", err)
	}
	aborted, ok := fixture.runner.GetTest(fixture.testID)
	if !ok || aborted.Status != "ABORTED" || aborted.EndedAt == nil {
		t.Fatalf("cancelled test = %#v, want terminal ABORTED", aborted)
	}
	occurrence, err := fixture.store.GetScenarioScheduleOccurrenceByTestID(fixture.testID)
	if err != nil {
		t.Fatalf("GetScenarioScheduleOccurrenceByTestID: %v", err)
	}
	if occurrence == nil || occurrence.Status != "failed" || occurrence.ReasonCode != "pending_promotion_aborted" || occurrence.MaterializedAt != nil {
		t.Fatalf("cancelled occurrence = %#v, want non-materialized terminal cancellation", occurrence)
	}
	if !strings.Contains(occurrence.ConflictSummary, "cancelled before start") {
		t.Fatalf("cancelled occurrence summary = %q, want cancellation detail", occurrence.ConflictSummary)
	}
	schedule, err := fixture.store.GetScenarioSchedule(fixture.scheduleID)
	if err != nil {
		t.Fatalf("GetScenarioSchedule: %v", err)
	}
	if schedule.LastRunAt != nil {
		t.Fatalf("cancelled pre-launch schedule LastRunAt = %v, want nil", schedule.LastRunAt)
	}
	if schedule.NextRequiredAction != "" || !strings.Contains(schedule.LastError, "cancelled before start") {
		t.Fatalf("cancelled schedule action/error = %q / %q, want informational cancellation only", schedule.NextRequiredAction, schedule.LastError)
	}
	fixture.runner.mu.RLock()
	cached := cloneScenarioSchedule(fixture.runner.scenarios[fixture.scenarioID].Schedules[0])
	fixture.runner.mu.RUnlock()
	if cached.LastOccurrence == nil || cached.LastOccurrence.ReasonCode != "pending_promotion_aborted" || cached.LastRunAt != nil {
		t.Fatalf("cached cancelled schedule = %#v, want reconciled cancellation", cached)
	}
}

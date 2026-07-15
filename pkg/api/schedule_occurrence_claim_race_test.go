package api

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

type blockingScenarioOccurrenceClaimStore struct {
	*storage.SQLiteStore
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *blockingScenarioOccurrenceClaimStore) ClaimScenarioScheduleOccurrenceIfCurrent(occurrence *storage.ScenarioScheduleOccurrence) (bool, error) {
	s.once.Do(func() { close(s.entered) })
	<-s.release
	return s.SQLiteStore.ClaimScenarioScheduleOccurrenceIfCurrent(occurrence)
}

type blockingExecutionPlanOccurrenceClaimStore struct {
	*storage.SQLiteStore
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *blockingExecutionPlanOccurrenceClaimStore) ClaimExecutionPlanScheduleOccurrenceIfCurrent(occurrence *storage.ExecutionPlanScheduleOccurrence) (bool, error) {
	s.once.Do(func() { close(s.entered) })
	<-s.release
	return s.SQLiteStore.ClaimExecutionPlanScheduleOccurrenceIfCurrent(occurrence)
}

func waitForScheduleClaim(t *testing.T, entered <-chan struct{}) {
	t.Helper()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the stale schedule sweep to reach its occurrence claim")
	}
}

func waitForScheduleSweep(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("schedule sweep: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the schedule sweep to finish")
	}
}

func TestScenarioScheduleClaimLosesRaceToConcurrentDisable(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "scenario-claim.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Close() })
	store := &blockingScenarioOccurrenceClaimStore{
		SQLiteStore: base,
		entered:     make(chan struct{}),
		release:     make(chan struct{}),
	}
	now := time.Now().UTC().Truncate(time.Second)
	scenario := &storage.Scenario{ID: "scenario-claim-race", Name: "Claim Race", Duration: "1m", CreatedAt: now, UpdatedAt: now}
	if err := store.SaveScenario(scenario, nil); err != nil {
		t.Fatal(err)
	}
	schedule := &storage.ScenarioSchedule{
		ID: "scenario-schedule-claim-race", ScenarioID: scenario.ID, ProjectID: "default", Revision: 1,
		Name: "Claim Race", CronExpr: "* * * * *", Timezone: "UTC", Enabled: true, NextRunAt: &now,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveScenarioSchedule(schedule); err != nil {
		t.Fatal(err)
	}

	runner := NewTestRunner(1, NewHub(), store)
	scheduler := newScenarioScheduler(runner)
	scheduler.now = func() time.Time { return now }
	done := make(chan error, 1)
	go func() { done <- scheduler.RunDueSchedules() }()
	waitForScheduleClaim(t, store.entered)

	enabled := false
	updated, found, err := runner.UpdateScenarioSchedule(scenario.ID, schedule.ID, CreateScenarioScheduleRequest{
		Name: schedule.Name, CronExpr: schedule.CronExpr, Timezone: schedule.Timezone, Enabled: &enabled,
	})
	if err != nil || !found {
		close(store.release)
		t.Fatalf("disable scenario schedule = (%+v, %v, %v), want updated schedule", updated, found, err)
	}
	if updated.Enabled {
		close(store.release)
		t.Fatal("scenario schedule remained enabled")
	}
	close(store.release)
	waitForScheduleSweep(t, done)

	occurrence, err := store.GetScenarioScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if occurrence != nil {
		t.Fatalf("stale scenario occurrence was claimed after disable: %+v", occurrence)
	}
	if tests := runner.ListTests(); len(tests) != 0 {
		t.Fatalf("stale scenario schedule created %d tests after disable", len(tests))
	}
}

func TestExecutionPlanScheduleClaimLosesRaceToConcurrentDisable(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "execution-plan-claim.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Close() })
	store := &blockingExecutionPlanOccurrenceClaimStore{
		SQLiteStore: base,
		entered:     make(chan struct{}),
		release:     make(chan struct{}),
	}
	now := time.Now().UTC().Truncate(time.Second)
	plan := &storage.ExecutionPlan{
		ID: "execution-plan-claim-race", ProjectID: "default", Name: "Claim Race", RunMode: "parallel",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveExecutionPlan(plan); err != nil {
		t.Fatal(err)
	}
	schedule := &storage.ExecutionPlanSchedule{
		ID: "execution-plan-schedule-claim-race", PlanID: plan.ID, ProjectID: plan.ProjectID, Revision: 1,
		Name: "Claim Race", CronExpr: "* * * * *", Timezone: "UTC", Enabled: true, NextRunAt: &now,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveExecutionPlanSchedule(schedule); err != nil {
		t.Fatal(err)
	}

	runner := NewTestRunner(1, NewHub(), store)
	done := make(chan error, 1)
	go func() {
		runner.sweepDueExecutionPlanSchedules(now)
		done <- nil
	}()
	waitForScheduleClaim(t, store.entered)

	enabled := false
	updated, found, err := runner.UpdateExecutionPlanSchedule(plan.ID, schedule.ID, CreateExecutionPlanScheduleRequest{
		Name: schedule.Name, CronExpr: schedule.CronExpr, Timezone: schedule.Timezone, Enabled: &enabled,
	})
	if err != nil || !found {
		close(store.release)
		t.Fatalf("disable execution-plan schedule = (%+v, %v, %v), want updated schedule", updated, found, err)
	}
	if updated.Enabled {
		close(store.release)
		t.Fatal("execution-plan schedule remained enabled")
	}
	close(store.release)
	waitForScheduleSweep(t, done)

	occurrence, err := store.GetExecutionPlanScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if occurrence != nil {
		t.Fatalf("stale execution-plan occurrence was claimed after disable: %+v", occurrence)
	}
	runs, err := store.ListExecutionPlanRuns(plan.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Fatalf("stale execution-plan schedule created %d runs after disable", len(runs))
	}
}

func TestScenarioScheduleEntryPointsRefreshReplicaState(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "scenario-ha-refresh.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	newStaleReplica := func(owner string) *TestRunner {
		runner := NewTestRunner(2, NewHub(), store)
		runner.leadership = newExecutionLeadership(store, owner)
		return runner
	}
	creator := newStaleReplica("creator")
	approvalReader := newStaleReplica("approval-reader")
	occurrenceReader := newStaleReplica("occurrence-reader")
	resolver := newStaleReplica("resolver")
	healthReader := newStaleReplica("health-reader")
	occurrenceDeleter := newStaleReplica("occurrence-deleter")
	// Warm every replica's normal HA-read throttle while the shared store is
	// still empty. Schedule entry points must force a refresh despite this recent
	// stale snapshot after another replica commits the rows below.
	for _, runner := range []*TestRunner{creator, approvalReader, occurrenceReader, resolver, healthReader, occurrenceDeleter} {
		runner.refreshScenariosForHARead()
	}

	now := time.Now().UTC().Truncate(time.Second)
	scenario := &storage.Scenario{ID: "scenario-ha-refresh", Name: "HA Refresh", Duration: "1m", CreatedAt: now, UpdatedAt: now}
	createTarget := &storage.Scenario{ID: "scenario-ha-create-target", Name: "HA Create", Duration: "1m", CreatedAt: now, UpdatedAt: now}
	if err := store.SaveScenario(scenario, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveScenario(createTarget, nil); err != nil {
		t.Fatal(err)
	}
	plannedAt := now.Add(time.Minute)
	schedule := &storage.ScenarioSchedule{
		ID: "scenario-schedule-ha-refresh", ScenarioID: scenario.ID, ProjectID: "default", Revision: 1,
		Name: "HA Refresh", CronExpr: "* * * * *", Timezone: "UTC", Enabled: true, NextRunAt: &plannedAt,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveScenarioSchedule(schedule); err != nil {
		t.Fatal(err)
	}
	occurrence := &storage.ScenarioScheduleOccurrence{
		ID: "scenario-occurrence-ha-refresh", ScheduleID: schedule.ID, ScenarioID: scenario.ID, ProjectID: "default",
		ScheduleRevision: schedule.Revision, PlannedAt: plannedAt, Status: "materialized", ReasonCode: "started",
		MaterializedAt: &now, CreatedAt: now, UpdatedAt: now,
	}
	created, err := store.CreateScenarioScheduleOccurrenceIfAbsent(occurrence)
	if err != nil || !created {
		t.Fatalf("create occurrence = (%v, %v), want (true, nil)", created, err)
	}
	advancedAt := plannedAt.Add(time.Minute)
	advanced := *schedule
	advanced.NextRunAt = &advancedAt
	advanced.UpdatedAt = now.Add(time.Second)
	changed, err := store.UpdateScenarioScheduleIfRevision(&advanced, schedule.Revision)
	if err != nil || !changed {
		t.Fatalf("advance schedule = (%v, %v), want (true, nil)", changed, err)
	}
	approval := &storage.ScenarioScheduleApproval{
		ID: "scenario-approval-ha-refresh", ScheduleID: schedule.ID, ScenarioID: scenario.ID, ProjectID: "default",
		ScheduleRevision: advanced.Revision, Policy: "manual", Status: "pending", RequestedBy: "reviewer",
		RequestedAt: now, RequiredRole: RoleProjectAdmin, CreatedAt: now, UpdatedAt: now,
	}
	advanced.PendingApprovalID = approval.ID
	advanced.NextRequiredAction = "approval_required"
	advanced.UpdatedAt = now.Add(2 * time.Second)
	changed, err = store.UpdateScenarioScheduleIfRevision(&advanced, advanced.Revision)
	if err != nil || !changed {
		t.Fatalf("attach approval to schedule = (%v, %v), want (true, nil)", changed, err)
	}
	approval.ScheduleRevision = advanced.Revision
	if err := store.SaveScenarioScheduleApproval(approval); err != nil {
		t.Fatal(err)
	}

	enabled := false
	createdSchedule, err := creator.CreateScenarioSchedule(createTarget.ID, CreateScenarioScheduleRequest{
		Name: "Created on stale replica", CronExpr: "0 * * * *", Timezone: "UTC", Enabled: &enabled,
	})
	if err != nil || createdSchedule == nil {
		t.Fatalf("create schedule after replica refresh = (%+v, %v)", createdSchedule, err)
	}
	if approvals, ok := approvalReader.ListScenarioScheduleApprovals(scenario.ID, schedule.ID); !ok || len(approvals) != 1 || approvals[0].ID != approval.ID {
		t.Fatalf("approval history after replica refresh = (%+v, %v)", approvals, ok)
	}
	if occurrences, ok := occurrenceReader.ListScenarioScheduleOccurrences(scenario.ID, schedule.ID); !ok || len(occurrences) != 1 || occurrences[0].ID != occurrence.ID {
		t.Fatalf("occurrence history after replica refresh = (%+v, %v)", occurrences, ok)
	}
	resolved, resolvedSchedule, ok, err := resolver.ResolveScenarioScheduleApproval(scenario.ID, schedule.ID, approval.ID, true, "reviewer", "approved")
	if err != nil || !ok || resolved == nil || resolved.Status != "approved" || resolvedSchedule == nil {
		t.Fatalf("resolve approval after replica refresh = (%+v, %+v, %v, %v)", resolved, resolvedSchedule, ok, err)
	}
	if health := healthReader.ScenarioScheduleHealth(now); health.Total != 2 {
		t.Fatalf("schedule health total after replica refresh = %d, want 2", health.Total)
	}
	deleted, err := occurrenceDeleter.DeleteScenarioScheduleOccurrence(scenario.ID, schedule.ID, occurrence.ID)
	if err != nil || !deleted {
		t.Fatalf("delete occurrence after replica refresh = (%v, %v), want (true, nil)", deleted, err)
	}
}

func TestScenarioScheduleCosmeticWindowUpdatePreservesDueOccurrence(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "scenario-window-due-anchor.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC().Truncate(time.Second)
	due := now.Add(-10 * time.Second)
	scenario := &storage.Scenario{ID: "scenario-window-due-anchor", Name: "Window anchor", Duration: "1m", CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)}
	if err := store.SaveScenario(scenario, nil); err != nil {
		t.Fatal(err)
	}
	windows := []storage.ScenarioScheduleWindow{
		{Name: "Morning label", DaysOfWeek: []int{1, 2}, StartTime: "09:00", EndTime: "10:00"},
		{Name: "Lunch label", StartTime: "11:00", EndTime: "12:00", Timezone: "UTC"},
	}
	schedule := &storage.ScenarioSchedule{
		ID: "scenario-schedule-window-due-anchor", ScenarioID: scenario.ID, ProjectID: "default", Revision: 1,
		Name: "Original", CronExpr: "* * * * *", Timezone: "UTC", Enabled: true,
		BlackoutWindows: windows, NextRunAt: &due,
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	}
	if err := store.SaveScenarioSchedule(schedule); err != nil {
		t.Fatal(err)
	}
	runner := NewTestRunner(1, NewHub(), store)
	enabled := true
	updated, found, err := runner.UpdateScenarioSchedule(scenario.ID, schedule.ID, CreateScenarioScheduleRequest{
		Name: "Renamed schedule", CronExpr: schedule.CronExpr, Timezone: schedule.Timezone, Enabled: &enabled,
		// Names, list order, duplicate/day order, surrounding whitespace, an
		// explicit fallback timezone, and spelling "all days" as 0..6 are all
		// presentation-equivalent to the durable windows above.
		BlackoutWindows: []storage.ScenarioScheduleWindow{
			{Name: "Renamed lunch", DaysOfWeek: []int{6, 5, 4, 3, 2, 1, 0}, StartTime: " 11:00 ", EndTime: "12:00"},
			{Name: "Renamed morning", DaysOfWeek: []int{2, 1, 2}, StartTime: "09:00", EndTime: "10:00", Timezone: "UTC"},
		},
	})
	if err != nil || !found || updated == nil || updated.NextRunAt == nil {
		t.Fatalf("cosmetic update = (%+v, %v, %v)", updated, found, err)
	}
	if !updated.NextRunAt.Equal(due) {
		t.Fatalf("cosmetic window update moved due occurrence from %v to %v", due, updated.NextRunAt)
	}
}

func TestScenarioScheduleEligibilityChangesRecomputeNextRun(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*CreateScenarioScheduleRequest, time.Time)
	}{
		{
			name: "finite pause",
			mutate: func(req *CreateScenarioScheduleRequest, oldNext time.Time) {
				from := oldNext.Add(-time.Minute)
				until := oldNext.Add(2 * time.Minute)
				req.DisabledFrom = &from
				req.DisabledUntil = &until
			},
		},
		{
			name: "blackout window",
			mutate: func(req *CreateScenarioScheduleRequest, oldNext time.Time) {
				req.BlackoutWindows = []storage.ScenarioScheduleWindow{{
					DaysOfWeek: []int{int(oldNext.Weekday())}, StartTime: oldNext.Format("15:04"),
					EndTime: oldNext.Add(2 * time.Minute).Format("15:04"), Timezone: "UTC",
				}}
			},
		},
		{
			name: "maintenance window",
			mutate: func(req *CreateScenarioScheduleRequest, oldNext time.Time) {
				req.MaintenanceWindows = []storage.ScenarioScheduleWindow{{
					DaysOfWeek: []int{int(oldNext.Weekday())}, StartTime: oldNext.Format("15:04"),
					EndTime: oldNext.Add(2 * time.Minute).Format("15:04"), Timezone: "UTC",
				}}
			},
		},
		{
			name: "availability window",
			mutate: func(req *CreateScenarioScheduleRequest, oldNext time.Time) {
				start := oldNext.Add(2 * time.Minute)
				req.AvailabilityWindows = []storage.ScenarioScheduleWindow{{
					DaysOfWeek: []int{int(start.Weekday())}, StartTime: start.Format("15:04"),
					EndTime: start.Add(2 * time.Minute).Format("15:04"), Timezone: "UTC",
				}}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "eligibility.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			now := time.Now().UTC().Truncate(time.Minute)
			scenario := &storage.Scenario{ID: "scenario-eligibility", Name: "Eligibility", Duration: "1m", CreatedAt: now, UpdatedAt: now}
			if err := store.SaveScenario(scenario, nil); err != nil {
				t.Fatal(err)
			}
			runner := NewTestRunner(1, NewHub(), store)
			enabled := true
			created, err := runner.CreateScenarioSchedule(scenario.ID, CreateScenarioScheduleRequest{
				Name: "Eligibility", CronExpr: "* * * * *", Timezone: "UTC", Enabled: &enabled,
			})
			if err != nil || created == nil || created.NextRunAt == nil {
				t.Fatalf("create schedule = (%+v, %v)", created, err)
			}
			oldNext := created.NextRunAt.UTC()
			req := CreateScenarioScheduleRequest{Name: created.Name, CronExpr: created.CronExpr, Timezone: created.Timezone, Enabled: &enabled}
			tc.mutate(&req, oldNext)
			updated, found, err := runner.UpdateScenarioSchedule(scenario.ID, created.ID, req)
			if err != nil || !found || updated == nil || updated.NextRunAt == nil {
				t.Fatalf("update schedule = (%+v, %v, %v)", updated, found, err)
			}
			if !updated.NextRunAt.After(oldNext) {
				t.Fatalf("next run remained %v after eligibility changed; old next was %v", updated.NextRunAt, oldNext)
			}
			allowed, err := scheduleAllowsCandidate(updated, *updated.NextRunAt)
			if err != nil || !allowed {
				t.Fatalf("recomputed next run %v is not eligible: allowed=%v err=%v", updated.NextRunAt, allowed, err)
			}
		})
	}
}

func TestExecutionPlanScheduleCosmeticUpdatePreservesDueOccurrence(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "execution-plan-due-anchor.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC().Truncate(time.Second)
	due := now.Add(-10 * time.Second)
	plan := &storage.ExecutionPlan{
		ID: "execution-plan-due-anchor", ProjectID: "default", Name: "Due Anchor", RunMode: "parallel",
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	}
	if err := store.SaveExecutionPlan(plan); err != nil {
		t.Fatal(err)
	}
	schedule := &storage.ExecutionPlanSchedule{
		ID: "execution-plan-schedule-due-anchor", PlanID: plan.ID, ProjectID: plan.ProjectID, Revision: 1,
		Name: "Original", CronExpr: "* * * * *", Timezone: "UTC", RunNameTemplate: "Original run",
		Enabled: true, MissedRunPolicy: "catch_up_once", NextRunAt: &due,
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	}
	if err := store.SaveExecutionPlanSchedule(schedule); err != nil {
		t.Fatal(err)
	}
	runner := NewTestRunner(1, NewHub(), store)
	enabled := true
	updated, found, err := runner.UpdateExecutionPlanSchedule(plan.ID, schedule.ID, CreateExecutionPlanScheduleRequest{
		Name: "Renamed", CronExpr: schedule.CronExpr, Timezone: schedule.Timezone,
		RunNameTemplate: "Renamed run", Enabled: &enabled, MissedRunPolicy: schedule.MissedRunPolicy,
	})
	if err != nil || !found || updated == nil || updated.NextRunAt == nil {
		t.Fatalf("cosmetic update = (%+v, %v, %v)", updated, found, err)
	}
	if !updated.NextRunAt.Equal(due) {
		t.Fatalf("cosmetic update moved due occurrence from %v to %v", due, updated.NextRunAt)
	}
}

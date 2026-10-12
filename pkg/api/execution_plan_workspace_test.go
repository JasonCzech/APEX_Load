package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

// newWorkspacePlanRunner is newPlanTestRunnerForProject with the store kept and
// a Storefront application and R2026.11 release seeded in proj-a.
func newWorkspacePlanRunner(t *testing.T, labels ...string) (*TestRunner, *storage.SQLiteStore, *executionBackendSpy, []string) {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	runner := NewTestRunner(10, NewHub(), store)
	spy := &executionBackendSpy{called: make(chan string, 16)}
	runner.setExecutionBackend(spy)
	now := time.Now().UTC()
	for _, projectID := range []string{"proj-a", "proj-b"} {
		if err := store.SaveProject(&storage.Project{ID: projectID, Name: projectID, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatalf("SaveProject(%s): %v", projectID, err)
		}
	}
	for _, app := range []struct{ id, project string }{{"app-store", "proj-a"}, {"app-other", "proj-b"}} {
		if err := store.SaveApplication(&storage.Application{ID: app.id, ProjectID: app.project, Name: app.id, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatalf("SaveApplication(%s): %v", app.id, err)
		}
	}
	for _, release := range []struct{ id, app, project string }{{"rel-2611", "app-store", "proj-a"}, {"rel-2612", "app-store", "proj-a"}, {"rel-other", "app-other", "proj-b"}} {
		if err := store.SaveRelease(&storage.Release{ID: release.id, ApplicationID: release.app, ProjectID: release.project, Name: release.id, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatalf("SaveRelease(%s): %v", release.id, err)
		}
	}
	ids := make([]string, 0, len(labels))
	for _, label := range labels {
		scriptID := "script-" + label
		runner.StoreScript(&dsl.Script{
			ID: scriptID, Name: label, Protocol: "http", Version: "1.0.0", ProjectID: "proj-a",
			Config:  dsl.ScriptConfig{BaseURL: "https://example.com", RunLogic: dsl.RunLogicConfig{Iterations: 1}},
			Actions: []dsl.Action{{Name: "Home", Type: "GET", Target: "/"}},
		})
		scenario, err := runner.StoreScenario(CreateScenarioRequest{
			Name: label, Duration: "30s",
			Groups: []CreateGroupEntry{{Name: "G", ScriptID: scriptID, VUsers: 1, RampUp: "1s"}},
		})
		if err != nil {
			t.Fatalf("StoreScenario(%s): %v", label, err)
		}
		ids = append(ids, scenario.ID)
	}
	return runner, store, spy, ids
}

func savedPlan(t *testing.T, store *storage.SQLiteStore, plan *storage.ExecutionPlan) *storage.ExecutionPlan {
	t.Helper()
	now := time.Now().UTC()
	plan.CreatedAt, plan.UpdatedAt = now, now
	if err := store.SaveExecutionPlan(plan); err != nil {
		t.Fatalf("SaveExecutionPlan: %v", err)
	}
	loaded, err := store.GetExecutionPlan(plan.ID)
	if err != nil {
		t.Fatalf("GetExecutionPlan: %v", err)
	}
	return loaded
}

func managedTestFor(t *testing.T, runner *TestRunner, id string) *ManagedTest {
	t.Helper()
	test, ok := runner.GetTest(id)
	if !ok || test == nil {
		t.Fatalf("test %s not found", id)
	}
	return test
}

func TestParallelPlanFilesRunsUnderItsReleaseAndLinksThem(t *testing.T) {
	runner, store, _, ids := newWorkspacePlanRunner(t, "browse", "cart", "checkout")
	disabled := false
	plan := savedPlan(t, store, &storage.ExecutionPlan{
		ID: "plan-parallel", ProjectID: "proj-a", Name: "Nightly regression", RunMode: "parallel",
		ApplicationID: "app-store", ReleaseID: "rel-2611",
		Entries: []storage.ExecutionPlanEntry{
			{Order: 0, ScenarioID: ids[0]},
			{Order: 1, ScenarioID: ids[1], Enabled: &disabled},
			{Order: 2, ScenarioID: ids[2]},
		},
	})
	if plan.ApplicationID != "app-store" || plan.ReleaseID != "rel-2611" {
		t.Fatalf("stored plan placement = %q/%q", plan.ApplicationID, plan.ReleaseID)
	}
	result, err := runner.MaterializeAndRunExecutionPlan(plan, "", "")
	if err != nil {
		t.Fatalf("MaterializeAndRunExecutionPlan: %v", err)
	}
	if result.Created != 2 {
		t.Fatalf("created = %d, want 2: %+v", result.Created, result.Results)
	}
	numbers := map[int]int{}
	for _, entry := range result.Results {
		if entry.TestID == "" {
			continue
		}
		test := managedTestFor(t, runner, entry.TestID)
		if test.ApplicationID != "app-store" || test.ReleaseID != "rel-2611" {
			t.Fatalf("run %s filed under %q/%q, want app-store/rel-2611", test.ID, test.ApplicationID, test.ReleaseID)
		}
		link := test.ExecutionPlan
		if link == nil || link.PlanID != plan.ID || link.PlanRunID != result.RunID || link.PlanName != "Nightly regression" || link.EntryCount != 2 {
			t.Fatalf("run %s plan link = %+v", test.ID, link)
		}
		numbers[link.EntryOrder] = link.EntryNumber
		record, err := store.GetTest(test.ID)
		if err != nil {
			t.Fatalf("GetTest: %v", err)
		}
		if stored := decodeStoredExecutionPlanLink(record.ExecutionPlan); stored == nil || *stored != *link {
			t.Fatalf("persisted plan link = %+v, want %+v", stored, link)
		}
	}
	if numbers[0] != 1 || numbers[2] != 2 {
		t.Fatalf("entry numbers by order = %v, want order 0 → 1 and order 2 → 2 (disabled entry not counted)", numbers)
	}
}

func TestSequentialPlanFilesEveryEntryUnderItsRelease(t *testing.T) {
	runner, store, spy, ids := newWorkspacePlanRunner(t, "first", "second")
	plan := savedPlan(t, store, &storage.ExecutionPlan{
		ID: "plan-seq", ProjectID: "proj-a", Name: "Ordered", RunMode: "sequential", ReleaseID: "rel-2612", ApplicationID: "app-store",
		Entries: []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: ids[0]}, {Order: 1, ScenarioID: ids[1]}},
	})
	run, err := runner.StartExecutionPlanRun(plan, "", "")
	if err != nil {
		t.Fatalf("StartExecutionPlanRun: %v", err)
	}
	first, ok := waitForStart(t, spy, 2*time.Second)
	if !ok {
		t.Fatal("entry 1 did not start")
	}
	completeManagedTest(t, runner, first, "COMPLETE")
	second, ok := waitForStart(t, spy, 2*time.Second)
	if !ok {
		t.Fatal("entry 2 did not start")
	}
	for number, id := range []string{first, second} {
		test := managedTestFor(t, runner, id)
		if test.ReleaseID != "rel-2612" || test.ApplicationID != "app-store" {
			t.Fatalf("entry %d filed under %q/%q", number+1, test.ApplicationID, test.ReleaseID)
		}
		if link := test.ExecutionPlan; link == nil || link.PlanRunID != run.ID || link.EntryNumber != number+1 || link.EntryCount != 2 || link.PlanName != "Ordered" {
			t.Fatalf("entry %d plan link = %+v", number+1, test.ExecutionPlan)
		}
	}
}

func TestPlanWithoutReleaseKeepsLegacyFilingButLinksRuns(t *testing.T) {
	runner, store, _, ids := newWorkspacePlanRunner(t, "only")
	plan := savedPlan(t, store, &storage.ExecutionPlan{
		ID: "plan-legacy", ProjectID: "proj-a", Name: "Legacy", RunMode: "parallel",
		Entries: []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: ids[0]}},
	})
	result, err := runner.MaterializeAndRunExecutionPlan(plan, "", "")
	if err != nil || result.Created != 1 {
		t.Fatalf("materialize = %+v, %v", result, err)
	}
	test := managedTestFor(t, runner, result.Results[0].TestID)
	if test.ReleaseID == "rel-2611" || test.ReleaseID == "" {
		t.Fatalf("release = %q, want the project's legacy release", test.ReleaseID)
	}
	if test.ExecutionPlan == nil || test.ExecutionPlan.PlanID != plan.ID {
		t.Fatalf("plan link = %+v", test.ExecutionPlan)
	}
}

func TestPlanOverlapPolicySkipRefusesASecondRunUntilTheFirstEnds(t *testing.T) {
	runner, store, _, ids := newWorkspacePlanRunner(t, "only")
	plan := savedPlan(t, store, &storage.ExecutionPlan{
		ID: "plan-skip", ProjectID: "proj-a", Name: "Skip", RunMode: "parallel", OverlapPolicy: storage.ExecutionPlanOverlapSkip,
		Entries: []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: ids[0]}},
	})
	first, err := runner.MaterializeAndRunExecutionPlan(plan, "", "")
	if err != nil || first.Created != 1 {
		t.Fatalf("first run = %+v, %v", first, err)
	}
	second, err := runner.MaterializeAndRunExecutionPlan(plan, "", "")
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if second.Overlap == nil || second.Overlap.ActiveRunID != first.RunID || second.Overlap.Policy != storage.ExecutionPlanOverlapSkip || second.RunID != "" {
		t.Fatalf("second run = %+v, want refused with the first run named", second)
	}
	if !strings.Contains(second.Overlap.Message, "already running") {
		t.Fatalf("overlap message = %q", second.Overlap.Message)
	}
	completeManagedTest(t, runner, first.Results[0].TestID, "COMPLETE")
	third, err := runner.MaterializeAndRunExecutionPlan(plan, "", "")
	if err != nil || third.Overlap != nil || third.Created != 1 {
		t.Fatalf("run after the first ended = %+v, %v", third, err)
	}
}

func TestPlanOverlapPolicyAllowCountsTheRunInProgressForQuota(t *testing.T) {
	runner, store, _, ids := newWorkspacePlanRunner(t, "only")
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{ID: "proj-a", Name: "proj-a", Settings: storage.ProjectSettings{MaxVUsers: 1}, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	plan := savedPlan(t, store, &storage.ExecutionPlan{
		ID: "plan-allow", ProjectID: "proj-a", Name: "Allow", RunMode: "parallel", OverlapPolicy: storage.ExecutionPlanOverlapAllow,
		Entries: []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: ids[0]}},
	})
	first, err := runner.MaterializeAndRunExecutionPlan(plan, "", "")
	if err != nil || first.Created != 1 || first.Blocked {
		t.Fatalf("first run (1 VUser within quota 1) = %+v, %v", first, err)
	}
	second, err := runner.MaterializeAndRunExecutionPlan(plan, "", "")
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if !second.Blocked || second.Approval == nil {
		t.Fatalf("second run = %+v, want blocked: the two runs together exceed quota 1", second)
	}
	if reason := second.Approval.DecisionReason; !strings.Contains(reason, "plus 1 VUsers from 1 run of this plan already in progress makes 2 together") {
		t.Fatalf("approval reason = %q, want the combined peak named", reason)
	}
}

func TestStopSequentialPlanRunSkipsEntriesNotStarted(t *testing.T) {
	runner, store, spy, ids := newWorkspacePlanRunner(t, "first", "second", "third")
	plan := savedPlan(t, store, &storage.ExecutionPlan{
		ID: "plan-stop", ProjectID: "proj-a", Name: "Stoppable", RunMode: "sequential",
		Entries: []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: ids[0]}, {Order: 1, ScenarioID: ids[1]}, {Order: 2, ScenarioID: ids[2]}},
	})
	run, err := runner.StartExecutionPlanRun(plan, "", "")
	if err != nil {
		t.Fatalf("StartExecutionPlanRun: %v", err)
	}
	first, ok := waitForStart(t, spy, 2*time.Second)
	if !ok {
		t.Fatal("entry 1 did not start")
	}
	view, _ := runner.StopExecutionPlanRun(plan.ID, run.ID, testCreationActor{Type: "user", Subject: "user:alice", DisplayName: "Alice"})
	if view == nil {
		t.Fatal("stop returned no run")
	}
	for _, entry := range view.Entries[1:] {
		if entry.Status != "skipped" || entry.Detail != "plan stopped by Alice" {
			t.Fatalf("entry after stop = %+v, want skipped by the stop", entry.ExecutionPlanRunEntry)
		}
	}
	// The test backend cannot stop gracefully, so the stop ends the running
	// entry by aborting it; finishing it here is then a no-op.
	if test := managedTestFor(t, runner, first); !isTerminalTestStatus(test.Status) {
		completeManagedTest(t, runner, first, "COMPLETE")
	}
	if id, started := waitForStart(t, spy, 300*time.Millisecond); started {
		t.Fatalf("entry %s started after the plan was stopped", id)
	}
	stored, err := store.GetExecutionPlanRun(run.ID)
	if err != nil {
		t.Fatalf("GetExecutionPlanRun: %v", err)
	}
	if stored.Status != "halted" || stored.Entries[0].Status == "running" || stored.Entries[0].Status == "pending" {
		t.Fatalf("stopped run = %s with entries %+v, want halted once the running entry ended", stored.Status, stored.Entries)
	}
	if final := runner.ExecutionPlanRunViewFor(stored); final.Outcome != "stopped" || final.Active {
		t.Fatalf("stopped run view = outcome %q active %v", final.Outcome, final.Active)
	}
	if _, err := runner.StopExecutionPlanRun(plan.ID, run.ID, testCreationActor{}); err != errExecutionPlanRunNotActive {
		t.Fatalf("stopping a finished run = %v, want errExecutionPlanRunNotActive", err)
	}
}

func TestExecutionPlanActivityReportsActiveAndLastRuns(t *testing.T) {
	runner, store, _, ids := newWorkspacePlanRunner(t, "only")
	plan := savedPlan(t, store, &storage.ExecutionPlan{
		ID: "plan-activity", ProjectID: "proj-a", Name: "Activity", RunMode: "parallel",
		Entries: []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: ids[0]}},
	})
	first, err := runner.MaterializeAndRunExecutionPlan(plan, "", "")
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	activity, err := runner.ExecutionPlanActivityFor(plan)
	if err != nil {
		t.Fatalf("activity: %v", err)
	}
	if activity.ActiveRun == nil || activity.ActiveRun.ID != first.RunID || activity.ActiveRun.Outcome != "running" || activity.LastRun != nil {
		t.Fatalf("activity while running = %+v", activity)
	}
	if entry := activity.ActiveRun.Entries[0]; entry.TestStatus == "" || entry.PlannedSeconds != 30 {
		t.Fatalf("active entry view = %+v, want live status and the scenario's 30 s", entry)
	}
	if activity.ActiveRun.EstimatedEndAt == nil {
		t.Fatal("an active run of fixed-duration scenarios should estimate its end")
	}
	completeManagedTest(t, runner, first.Results[0].TestID, "FAILED")
	activity, err = runner.ExecutionPlanActivityFor(plan)
	if err != nil {
		t.Fatalf("activity: %v", err)
	}
	if activity.ActiveRun != nil || activity.LastRun == nil || activity.LastRun.Outcome != "failed" || activity.LastRun.EndedAt == nil {
		t.Fatalf("activity after the run failed = %+v", activity)
	}
}

func TestScheduledPlanRunQueuesBehindThePreviousRun(t *testing.T) {
	runner, store, _, ids := newWorkspacePlanRunner(t, "only")
	plan := savedPlan(t, store, &storage.ExecutionPlan{
		ID: "plan-queue", ProjectID: "proj-a", Name: "Queue", RunMode: "parallel", OverlapPolicy: storage.ExecutionPlanOverlapQueue,
		ReleaseID: "rel-2611", ApplicationID: "app-store",
		Entries: []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: ids[0]}},
	})
	manual, err := runner.MaterializeAndRunExecutionPlan(plan, "", "")
	if err != nil || manual.Created != 1 {
		t.Fatalf("manual run = %+v, %v", manual, err)
	}
	releaseOverride := "rel-2612"
	schedule, err := runner.CreateExecutionPlanSchedule(plan.ID, CreateExecutionPlanScheduleRequest{Name: "Nightly", CronExpr: "0 9 * * *", Timezone: "UTC", ReleaseID: &releaseOverride})
	if err != nil {
		t.Fatalf("CreateExecutionPlanSchedule: %v", err)
	}
	if schedule.ReleaseID != "rel-2612" {
		t.Fatalf("schedule release = %q", schedule.ReleaseID)
	}
	now := time.Now().UTC()
	due := now.Add(-30 * time.Second).Truncate(time.Second)
	schedule.NextRunAt = &due
	if err := store.SaveExecutionPlanSchedule(schedule); err != nil {
		t.Fatalf("SaveExecutionPlanSchedule: %v", err)
	}
	runner.sweepDueExecutionPlanSchedules(now)
	occurrence, err := store.GetExecutionPlanScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, due)
	if err != nil || occurrence == nil {
		t.Fatalf("occurrence: %v", err)
	}
	if occurrence.Status != "blocked" || occurrence.ReasonCode != executionPlanWaitingForPreviousRun || !strings.Contains(occurrence.ConflictSummary, "Waiting for the plan's run in progress") {
		t.Fatalf("occurrence while the previous run is active = %+v", occurrence)
	}
	held, _ := store.GetExecutionPlanSchedule(schedule.ID)
	if held.NextRequiredAction != executionPlanWaitingForPreviousRun || held.NextRunAt == nil || !held.NextRunAt.Equal(due) || held.ReleaseID != "rel-2612" {
		t.Fatalf("schedule while held = %+v", held)
	}
	activity, err := runner.ExecutionPlanActivityFor(plan)
	if err != nil || len(activity.Waiting) != 1 || activity.Waiting[0].ReasonCode != executionPlanWaitingForPreviousRun {
		t.Fatalf("activity while queued = %+v, %v", activity, err)
	}
	// The sweep skips a disabled schedule, so its held run is not waiting on anything.
	paused := *held
	paused.Enabled = false
	if waiting, err := executionPlanScheduleWaitingOccurrence(store, &paused); err != nil || waiting != nil {
		t.Fatalf("a disabled schedule's held run = %+v, %v; want none", waiting, err)
	}
	// A second sweep while still running writes nothing new.
	revision := occurrence.Revision
	runner.sweepDueExecutionPlanSchedules(now.Add(time.Second))
	if again, _ := store.GetExecutionPlanScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, due); again.Revision != revision || again.Status != "blocked" {
		t.Fatalf("occurrence rewritten while still waiting: %+v", again)
	}

	completeManagedTest(t, runner, manual.Results[0].TestID, "COMPLETE")
	runner.sweepDueExecutionPlanSchedules(now.Add(2 * time.Second))
	occurrence, _ = store.GetExecutionPlanScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, due)
	if occurrence.Status != "materialized" || occurrence.RunID == "" {
		t.Fatalf("occurrence after the previous run finished = %+v", occurrence)
	}
	planRun, err := store.GetExecutionPlanRun(occurrence.RunID)
	if err != nil || planRun == nil || planRun.Entries[0].TestID == "" {
		t.Fatalf("queued plan run = %+v, %v", planRun, err)
	}
	if test := managedTestFor(t, runner, planRun.Entries[0].TestID); test.ReleaseID != "rel-2612" {
		t.Fatalf("scheduled run filed under %q, want the schedule's release rel-2612", test.ReleaseID)
	}
	advanced, _ := store.GetExecutionPlanSchedule(schedule.ID)
	if advanced.NextRequiredAction != "" || advanced.NextRunAt == nil || !advanced.NextRunAt.After(due) {
		t.Fatalf("schedule after the queued run started = %+v", advanced)
	}
}

func TestChangingAScheduleSkipsTheRunItHeldQueued(t *testing.T) {
	runner, store, _, ids := newWorkspacePlanRunner(t, "only")
	plan := savedPlan(t, store, &storage.ExecutionPlan{
		ID: "plan-queue-edit", ProjectID: "proj-a", Name: "Queue", RunMode: "parallel", OverlapPolicy: storage.ExecutionPlanOverlapQueue,
		Entries: []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: ids[0]}},
	})
	if _, err := runner.MaterializeAndRunExecutionPlan(plan, "", ""); err != nil {
		t.Fatalf("manual run: %v", err)
	}
	schedule, err := runner.CreateExecutionPlanSchedule(plan.ID, CreateExecutionPlanScheduleRequest{Name: "Nightly", CronExpr: "0 9 * * *", Timezone: "UTC"})
	if err != nil {
		t.Fatalf("CreateExecutionPlanSchedule: %v", err)
	}
	now := time.Now().UTC()
	due := now.Add(-30 * time.Second).Truncate(time.Second)
	schedule.NextRunAt = &due
	if err := store.SaveExecutionPlanSchedule(schedule); err != nil {
		t.Fatalf("SaveExecutionPlanSchedule: %v", err)
	}
	runner.sweepDueExecutionPlanSchedules(now)
	if held, _ := store.GetExecutionPlanScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, due); held == nil || held.Status != "blocked" {
		t.Fatalf("occurrence before the edit = %+v, want queued", held)
	}
	// Renaming keeps the held time, so the run stays queued.
	if _, _, err := runner.UpdateExecutionPlanSchedule(plan.ID, schedule.ID, CreateExecutionPlanScheduleRequest{Name: "Renamed", CronExpr: "0 9 * * *", Timezone: "UTC"}); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if held, _ := store.GetExecutionPlanScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, due); held.Status != "blocked" {
		t.Fatalf("occurrence after a rename = %+v, want still queued", held)
	}
	if renamed, _ := store.GetExecutionPlanSchedule(schedule.ID); renamed.NextRequiredAction != executionPlanWaitingForPreviousRun || renamed.LastError == "" {
		t.Fatalf("schedule after a rename = %+v, want it still showing the queued run", renamed)
	}
	// A new cadence moves the schedule off the held run: nothing would start it.
	if _, _, err := runner.UpdateExecutionPlanSchedule(plan.ID, schedule.ID, CreateExecutionPlanScheduleRequest{Name: "Renamed", CronExpr: "0 10 * * *", Timezone: "UTC"}); err != nil {
		t.Fatalf("change cadence: %v", err)
	}
	skipped, _ := store.GetExecutionPlanScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, due)
	if skipped.Status != "skipped" || skipped.ReasonCode != "schedule_changed" {
		t.Fatalf("occurrence after the cadence changed = %+v, want skipped", skipped)
	}
}

func TestScheduledPlanRunIsSkippedWhileThePreviousRunIsActive(t *testing.T) {
	runner, store, _, ids := newWorkspacePlanRunner(t, "only")
	plan := savedPlan(t, store, &storage.ExecutionPlan{
		ID: "plan-sched-skip", ProjectID: "proj-a", Name: "Skip", RunMode: "parallel", OverlapPolicy: storage.ExecutionPlanOverlapSkip,
		Entries: []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: ids[0]}},
	})
	if _, err := runner.MaterializeAndRunExecutionPlan(plan, "", ""); err != nil {
		t.Fatalf("manual run: %v", err)
	}
	schedule, err := runner.CreateExecutionPlanSchedule(plan.ID, CreateExecutionPlanScheduleRequest{Name: "Nightly", CronExpr: "0 9 * * *", Timezone: "UTC"})
	if err != nil {
		t.Fatalf("CreateExecutionPlanSchedule: %v", err)
	}
	now := time.Now().UTC()
	due := now.Add(-30 * time.Second).Truncate(time.Second)
	schedule.NextRunAt = &due
	if err := store.SaveExecutionPlanSchedule(schedule); err != nil {
		t.Fatalf("SaveExecutionPlanSchedule: %v", err)
	}
	runner.sweepDueExecutionPlanSchedules(now)
	occurrence, _ := store.GetExecutionPlanScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, due)
	if occurrence == nil || occurrence.Status != "skipped" || occurrence.ReasonCode != "previous_run_active" {
		t.Fatalf("occurrence = %+v, want skipped because the previous run was active", occurrence)
	}
	advanced, _ := store.GetExecutionPlanSchedule(schedule.ID)
	if advanced.NextRunAt == nil || !advanced.NextRunAt.After(now) || advanced.NextRequiredAction != "" {
		t.Fatalf("schedule after a skipped run = %+v, want it moved on", advanced)
	}
}

func TestPreviewExecutionPlanScheduleRuns(t *testing.T) {
	runner, store, _, ids := newWorkspacePlanRunner(t, "only")
	plan := savedPlan(t, store, &storage.ExecutionPlan{ID: "plan-preview", ProjectID: "proj-a", Name: "Preview", Entries: []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: ids[0]}}})
	preview, err := runner.PreviewExecutionPlanScheduleRuns(plan.ID, CreateExecutionPlanScheduleRequest{CronExpr: "30 1 * * *", Timezone: "America/Chicago"}, 5)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if preview.Count != 5 || len(preview.Occurrences) != 5 || preview.Timezone != "America/Chicago" {
		t.Fatalf("preview = %+v", preview)
	}
	chicago, _ := time.LoadLocation("America/Chicago")
	for index, at := range preview.Occurrences {
		local := at.In(chicago)
		if local.Hour() != 1 || local.Minute() != 30 || (index > 0 && !at.After(preview.Occurrences[index-1])) {
			t.Fatalf("occurrence %d = %s (%s local), want 1:30 each day in order", index, at, local)
		}
	}
	if _, err := runner.PreviewExecutionPlanScheduleRuns(plan.ID, CreateExecutionPlanScheduleRequest{CronExpr: "not cron"}, 5); err == nil {
		t.Fatal("an invalid cron expression should not preview")
	}
	if _, err := runner.PreviewExecutionPlanScheduleRuns("missing", CreateExecutionPlanScheduleRequest{CronExpr: "0 1 * * *"}, 5); err == nil {
		t.Fatal("a missing plan should not preview")
	}
}

func TestExecutionPlanReleaseAndOverlapThroughTheAPI(t *testing.T) {
	server, store := newTimeslotServer(t)
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{ID: "proj-b", Name: "B", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	for _, app := range []struct{ id, project string }{{"app-a", "proj-a"}, {"app-b", "proj-b"}, {"app-c", "proj-a"}} {
		if err := store.SaveApplication(&storage.Application{ID: app.id, ProjectID: app.project, Name: app.id, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatalf("SaveApplication: %v", err)
		}
	}
	for _, release := range []struct{ id, app, project string }{{"rel-a", "app-a", "proj-a"}, {"rel-b", "app-b", "proj-b"}, {"rel-a-old", "app-a", "proj-a"}, {"rel-a2", "app-a", "proj-a"}, {"rel-c", "app-c", "proj-a"}} {
		if err := store.SaveRelease(&storage.Release{ID: release.id, ApplicationID: release.app, ProjectID: release.project, Name: release.id, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatalf("SaveRelease: %v", err)
		}
	}
	if err := store.ArchiveRelease("rel-a-old"); err != nil {
		t.Fatalf("ArchiveRelease: %v", err)
	}
	entries := []map[string]any{{"order": 0, "scenario_id": "scn-1"}}
	for _, tc := range []struct {
		name string
		body map[string]any
		want string
	}{
		{"release in another project", map[string]any{"release_id": "rel-b"}, "does not belong to the plan's project"},
		{"archived release", map[string]any{"release_id": "rel-a-old"}, "is archived"},
		{"unknown release", map[string]any{"release_id": "rel-missing"}, "not found"},
		{"application without release", map[string]any{"application_id": "app-a"}, "requires a release_id"},
		{"application not the release's", map[string]any{"application_id": "app-b", "release_id": "rel-a"}, "does not match the release"},
		{"unknown overlap policy", map[string]any{"overlap_policy": "sometimes"}, "overlap_policy must be"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{"project_id": "proj-a", "name": "plan", "entries": entries}
			for key, value := range tc.body {
				body[key] = value
			}
			rec := timeslotRequest(t, server, http.MethodPost, "/api/v1/execution-plans", body)
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), tc.want) {
				t.Fatalf("status = %d body = %s, want 400 mentioning %q", rec.Code, rec.Body.String(), tc.want)
			}
		})
	}

	create := timeslotRequest(t, server, http.MethodPost, "/api/v1/execution-plans", map[string]any{
		"project_id": "proj-a", "name": "Workspace plan", "entries": entries, "release_id": "rel-a",
	})
	if create.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", create.Code, create.Body.String())
	}
	var plan storage.ExecutionPlan
	_ = json.Unmarshal(create.Body.Bytes(), &plan)
	if plan.ApplicationID != "app-a" || plan.ReleaseID != "rel-a" || plan.OverlapPolicy != storage.ExecutionPlanOverlapSkip {
		t.Fatalf("created plan placement = %q/%q overlap %q, want app-a/rel-a and the skip default", plan.ApplicationID, plan.ReleaseID, plan.OverlapPolicy)
	}

	// An update from a client that predates the fields keeps them.
	update := timeslotRequest(t, server, http.MethodPut, "/api/v1/execution-plans/"+plan.ID, map[string]any{
		"name": "Renamed", "revision": plan.Revision, "entries": entries,
	})
	if update.Code != http.StatusOK {
		t.Fatalf("update = %d %s", update.Code, update.Body.String())
	}
	stored, _ := store.GetExecutionPlan(plan.ID)
	if stored.Name != "Renamed" || stored.ReleaseID != "rel-a" || stored.ApplicationID != "app-a" || stored.OverlapPolicy != storage.ExecutionPlanOverlapSkip {
		t.Fatalf("after an update without the fields = %+v", stored)
	}
	// An explicit empty release clears it; the policy changes.
	update = timeslotRequest(t, server, http.MethodPut, "/api/v1/execution-plans/"+plan.ID, map[string]any{
		"name": "Renamed", "revision": stored.Revision, "entries": entries, "release_id": "", "overlap_policy": "queue",
	})
	if update.Code != http.StatusOK {
		t.Fatalf("clearing update = %d %s", update.Code, update.Body.String())
	}
	stored, _ = store.GetExecutionPlan(plan.ID)
	if stored.ReleaseID != "" || stored.ApplicationID != "" || stored.OverlapPolicy != storage.ExecutionPlanOverlapQueue {
		t.Fatalf("after clearing the release = %+v", stored)
	}

	list := timeslotRequest(t, server, http.MethodGet, "/api/v1/execution-plans?project_id=proj-a&include_activity=true", nil)
	var listed struct {
		ExecutionPlans []struct {
			ID       string                 `json:"id"`
			Activity *ExecutionPlanActivity `json:"activity"`
		} `json:"execution_plans"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil || len(listed.ExecutionPlans) != 1 || listed.ExecutionPlans[0].Activity == nil {
		t.Fatalf("list with activity = %s (%v)", list.Body.String(), err)
	}
	plain := timeslotRequest(t, server, http.MethodGet, "/api/v1/execution-plans?project_id=proj-a", nil)
	if strings.Contains(plain.Body.String(), `"activity"`) {
		t.Fatalf("a plain list carries activity: %s", plain.Body.String())
	}

	// A schedule may only choose another release of the plan's application.
	scheduleWithRelease := func(releaseID string) *httptest.ResponseRecorder {
		return timeslotRequest(t, server, http.MethodPost, "/api/v1/execution-plans/"+plan.ID+"/schedules", map[string]any{
			"name": "nightly", "cron_expr": "0 2 * * *", "timezone": "UTC", "release_id": releaseID,
		})
	}
	if schedule := scheduleWithRelease("rel-a2"); schedule.Code != http.StatusBadRequest || !strings.Contains(schedule.Body.String(), "choose the plan's application and release") {
		t.Fatalf("schedule release on a plan without one = %d %s", schedule.Code, schedule.Body.String())
	}
	update = timeslotRequest(t, server, http.MethodPut, "/api/v1/execution-plans/"+plan.ID, map[string]any{
		"name": "Renamed", "revision": stored.Revision, "entries": entries, "release_id": "rel-a",
	})
	if update.Code != http.StatusOK {
		t.Fatalf("restoring the release = %d %s", update.Code, update.Body.String())
	}
	for releaseID, want := range map[string]string{"rel-b": "does not belong", "rel-c": "not a release of the plan's application"} {
		if schedule := scheduleWithRelease(releaseID); schedule.Code != http.StatusBadRequest || !strings.Contains(schedule.Body.String(), want) {
			t.Fatalf("schedule with release %s = %d %s, want 400 mentioning %q", releaseID, schedule.Code, schedule.Body.String(), want)
		}
	}
	if schedule := scheduleWithRelease("rel-a2"); schedule.Code != http.StatusCreated {
		t.Fatalf("schedule with another release of the plan's application = %d %s", schedule.Code, schedule.Body.String())
	}
	// The plan cannot leave the application that schedule's release is in.
	stored, _ = store.GetExecutionPlan(plan.ID)
	moved := timeslotRequest(t, server, http.MethodPut, "/api/v1/execution-plans/"+plan.ID, map[string]any{
		"name": "Renamed", "revision": stored.Revision, "entries": entries, "release_id": "rel-c",
	})
	if moved.Code != http.StatusConflict || !strings.Contains(moved.Body.String(), `schedule \"nightly\" files its runs under a release of another application`) {
		t.Fatalf("moving the plan away from its schedule's release = %d %s", moved.Code, moved.Body.String())
	}
	preview := timeslotRequest(t, server, http.MethodPost, "/api/v1/execution-plans/"+plan.ID+"/schedules/preview?count=3", map[string]any{
		"name": "x", "cron_expr": "0 2 * * *", "timezone": "UTC",
	})
	var previewBody ScenarioSchedulePreview
	if preview.Code != http.StatusOK || json.Unmarshal(preview.Body.Bytes(), &previewBody) != nil || previewBody.Count != 3 {
		t.Fatalf("preview = %d %s", preview.Code, preview.Body.String())
	}

	// An application whose release a plan files under cannot be deleted from under it.
	pinned := timeslotRequest(t, server, http.MethodPost, "/api/v1/execution-plans", map[string]any{
		"project_id": "proj-a", "name": "Pinned", "entries": entries, "release_id": "rel-a",
	})
	if pinned.Code != http.StatusCreated {
		t.Fatalf("pinned plan = %d %s", pinned.Code, pinned.Body.String())
	}
	if err := store.DeleteApplication("app-a"); err == nil || !strings.Contains(err.Error(), "associated results, schedules") {
		t.Fatalf("deleting an application a plan files under = %v, want refused", err)
	}
}

func TestRunCalendarShowsPlanStartedRunsAsPlanRuns(t *testing.T) {
	event := runCalendarEventFromTest(&ManagedTest{
		ID: "t1", Name: "Nightly / Checkout", Status: "RUNNING", CreatedAt: time.Now().UTC(),
		ExecutionPlan: &TestExecutionPlanLink{PlanID: "plan-1", PlanName: "Nightly", PlanRunID: "run-1", EntryNumber: 1, EntryCount: 2},
	}, nil, SystemInventoryResponse{}, nil, time.Now().UTC())
	if event.Source != "execution_plan" || event.RunType != "execution_plan" || event.PlanID != "plan-1" || event.PlanName != "Nightly" || event.PlanRunID != "run-1" {
		t.Fatalf("plan-started run event = %+v", event)
	}
	runner := NewTestRunner(1, NewHub(), nil)
	runner.mu.Lock()
	runner.tests["t1"] = &ManagedTest{ID: "t1", Status: "RUNNING", ExecutionPlan: &TestExecutionPlanLink{PlanID: "plan-1", PlanRunID: "run-1"}}
	runner.mu.Unlock()
	if snapshots := runner.listOperationalTestSnapshots(); len(snapshots) != 1 || snapshots[0].ExecutionPlan == nil || snapshots[0].ExecutionPlan.PlanRunID != "run-1" {
		t.Fatalf("the calendar's run snapshot dropped the plan link: %+v", snapshots)
	}
	adHoc := runCalendarEventFromTest(&ManagedTest{ID: "t2", Name: "Ad hoc", Status: "RUNNING", CreatedAt: time.Now().UTC()}, nil, SystemInventoryResponse{}, nil, time.Now().UTC())
	if adHoc.Source != "ad_hoc" || adHoc.PlanID != "" {
		t.Fatalf("ad-hoc run event = %+v", adHoc)
	}
}

func TestScheduleHealthCountsHeldPlanRunsApart(t *testing.T) {
	runner, store, _, ids := newWorkspacePlanRunner(t, "only")
	plan := savedPlan(t, store, &storage.ExecutionPlan{ID: "plan-health", ProjectID: "proj-a", Name: "Health", Entries: []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: ids[0]}}})
	now := time.Now().UTC()
	past := now.Add(-time.Hour)
	for index, action := range []string{"approval_required", executionPlanWaitingForPreviousRun} {
		schedule, err := runner.CreateExecutionPlanSchedule(plan.ID, CreateExecutionPlanScheduleRequest{Name: action, CronExpr: "0 2 * * *", Timezone: "UTC"})
		if err != nil {
			t.Fatalf("CreateExecutionPlanSchedule: %v", err)
		}
		schedule.NextRunAt = &past
		schedule.NextRequiredAction = action
		schedule.LastError = "held " + string(rune('a'+index))
		if err := store.SaveExecutionPlanSchedule(schedule); err != nil {
			t.Fatalf("SaveExecutionPlanSchedule: %v", err)
		}
	}
	health := (&testRunnerSchedulingService{runner: runner}).Health(now)
	if health.ExecutionPlanAwaitingApproval != 1 || health.ExecutionPlanWaitingPreviousRun != 1 || health.ExecutionPlanDue != 0 || health.ExecutionPlanBlocked != 0 {
		t.Fatalf("health = awaiting %d, queued %d, due %d, blocked %d; want held runs counted apart",
			health.ExecutionPlanAwaitingApproval, health.ExecutionPlanWaitingPreviousRun, health.ExecutionPlanDue, health.ExecutionPlanBlocked)
	}
}

func TestExecutionPlanRunEntryContextRebuildsTheLinkFromTheSnapshot(t *testing.T) {
	run := &storage.ExecutionPlanRun{ID: "run-1", PlanID: "plan-1", Entries: []storage.ExecutionPlanRunEntry{
		{Order: 0, Status: "complete", PlanName: "Nightly", ReleaseID: "rel-1", ApplicationID: "app-1"},
		{Order: 1, Status: "skipped", Detail: "entry disabled"},
		{Order: 2, Status: "pending", PlanName: "Nightly", ReleaseID: "rel-1", ApplicationID: "app-1"},
	}}
	var req CreateTestRequest
	executionPlanRunEntryContext(run, "Nightly", 2).apply(&req, 2)
	if req.ReleaseID != "rel-1" || req.ApplicationID != "app-1" {
		t.Fatalf("placement = %q/%q", req.ApplicationID, req.ReleaseID)
	}
	want := TestExecutionPlanLink{PlanID: "plan-1", PlanName: "Nightly", PlanRunID: "run-1", EntryOrder: 2, EntryNumber: 2, EntryCount: 2}
	if req.ExecutionPlan == nil || *req.ExecutionPlan != want {
		t.Fatalf("link = %+v, want %+v", req.ExecutionPlan, want)
	}
	var legacy CreateTestRequest
	executionPlanRunEntryContext(run, "", 1).apply(&legacy, 1)
	if legacy.ReleaseID != "" || legacy.ExecutionPlan == nil || legacy.ExecutionPlan.EntryNumber != 0 {
		t.Fatalf("an entry without a release keeps the legacy filing: %+v", legacy)
	}
	if decodeStoredExecutionPlanLink(map[string]any{"plan_id": "", "plan_run_id": "x"}) != nil || executionPlanLinkForStorage(nil) != nil {
		t.Fatal("an empty link must not persist or decode as a link")
	}
}

func TestConcurrentStartsOfASkipPlanStartOneRun(t *testing.T) {
	runner, store, _, ids := newWorkspacePlanRunner(t, "only")
	plan := savedPlan(t, store, &storage.ExecutionPlan{
		ID: "plan-race", ProjectID: "proj-a", Name: "Race", RunMode: "parallel", OverlapPolicy: storage.ExecutionPlanOverlapSkip,
		Entries: []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: ids[0]}},
	})
	results := make(chan *ExecutionPlanMaterializeResult, 4)
	errs := make(chan error, 4)
	start := make(chan struct{})
	for range 4 {
		go func() {
			<-start
			result, err := runner.MaterializeAndRunExecutionPlan(plan, "", "")
			results <- result
			errs <- err
		}()
	}
	close(start)
	started, refused := 0, 0
	for range 4 {
		if err := <-errs; err != nil {
			t.Fatalf("start: %v", err)
		}
		switch result := <-results; {
		case result.Overlap != nil:
			refused++
		case result.Created == 1:
			started++
		default:
			t.Fatalf("unexpected start result %+v", result)
		}
	}
	if started != 1 || refused != 3 {
		t.Fatalf("started %d and refused %d of 4 concurrent starts, want exactly one run", started, refused)
	}
}

func TestStopPlanWaitsForAParallelRunStillBeingStaged(t *testing.T) {
	runner, store, _, ids := newWorkspacePlanRunner(t, "first", "second")
	plan := savedPlan(t, store, &storage.ExecutionPlan{
		ID: "plan-stop-staging", ProjectID: "proj-a", Name: "Staging", RunMode: "parallel",
		Entries: []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: ids[0]}, {Order: 1, ScenarioID: ids[1]}},
	})
	result, err := runner.MaterializeAndRunExecutionPlan(plan, "", "")
	if err != nil || result.Created != 2 {
		t.Fatalf("parallel run = %+v, %v", result, err)
	}
	// A start of the plan still creating its entries' runs holds this lock.
	unlockStart := runner.lockExecutionPlanStart(plan.ID)
	stopped := make(chan error, 1)
	go func() {
		_, err := runner.StopExecutionPlanRun(plan.ID, result.RunID, testCreationActor{DisplayName: "Alice"})
		stopped <- err
	}()
	select {
	case err := <-stopped:
		unlockStart()
		t.Fatalf("Stop plan returned (%v) while the plan's runs were still being created", err)
	case <-time.After(150 * time.Millisecond):
	}
	unlockStart()
	select {
	case err := <-stopped:
		if err != nil && !strings.Contains(err.Error(), "stop run") {
			t.Fatalf("Stop plan after staging: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stop plan did not finish once staging ended")
	}
	for _, entry := range result.Results {
		if status := managedTestFor(t, runner, entry.TestID).Status; status != "STOPPING" && !isTerminalTestStatus(status) {
			t.Fatalf("entry run %s is %s after Stop plan, want stopping or ended", entry.TestID, status)
		}
	}
	// The run records the stop, as a sequential run does, so it reads as
	// stopping, and then stopped, rather than running.
	stored, err := store.GetExecutionPlanRun(result.RunID)
	if err != nil {
		t.Fatalf("GetExecutionPlanRun: %v", err)
	}
	for _, entry := range stored.Entries {
		if !entry.Stopped || entry.Detail != "plan stopped by Alice" {
			t.Fatalf("parallel entry after Stop plan = %+v, want it marked stopped by Alice", entry)
		}
	}
	if view := runner.ExecutionPlanRunViewFor(stored); view.Outcome != "running" && view.Outcome != "stopped" {
		t.Fatalf("outcome after Stop plan = %q, want running (stopping) or stopped", view.Outcome)
	}
}

func TestParallelStopMarkIsReappliedAfterAnotherWriter(t *testing.T) {
	runner, store, _, ids := newWorkspacePlanRunner(t, "only")
	plan := savedPlan(t, store, &storage.ExecutionPlan{
		ID: "plan-stop-conflict", ProjectID: "proj-a", Name: "Conflict", RunMode: "parallel",
		Entries: []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: ids[0]}},
	})
	result, err := runner.MaterializeAndRunExecutionPlan(plan, "", "")
	if err != nil || result.Created != 1 {
		t.Fatalf("parallel run = %+v, %v", result, err)
	}
	stale, err := store.GetExecutionPlanRun(result.RunID)
	if err != nil {
		t.Fatalf("GetExecutionPlanRun: %v", err)
	}
	// Another writer of the run (a queue-handoff retry) commits first.
	moved := *stale
	moved.Entries = cloneExecutionPlanRunEntries(stale.Entries)
	moved.UpdatedAt = time.Now().UTC()
	if swapped, err := store.CompareAndSwapExecutionPlanRun(&moved, stale.Revision); err != nil || !swapped {
		t.Fatalf("CompareAndSwapExecutionPlanRun = %v, %v", swapped, err)
	}
	runner.markParallelPlanRunStopped(stale, []string{result.Results[0].TestID}, "plan stopped by Bob")
	stored, err := store.GetExecutionPlanRun(result.RunID)
	if err != nil {
		t.Fatalf("GetExecutionPlanRun: %v", err)
	}
	if !stored.Entries[0].Stopped || stored.Entries[0].Detail != "plan stopped by Bob" {
		t.Fatalf("entry after a stop that lost a write race = %+v, want it marked stopped", stored.Entries[0])
	}
}

func TestStopArrivingWhileAnEntryIsCreatedCancelsThatEntry(t *testing.T) {
	runner, store, spy, ids := newWorkspacePlanRunner(t, "first", "second")
	plan := savedPlan(t, store, &storage.ExecutionPlan{
		ID: "plan-stop-race", ProjectID: "proj-a", Name: "Race", RunMode: "sequential",
		Entries: []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: ids[0]}, {Order: 1, ScenarioID: ids[1]}},
	})
	var runID string
	claims := 0
	runner.afterExecutionPlanEntryClaim = func() {
		claims++
		if claims == 2 {
			// Stop plan lands after entry 2 is claimed, before its run exists.
			if _, err := runner.StopExecutionPlanRun(plan.ID, runID, testCreationActor{DisplayName: "Alice"}); err != nil {
				t.Errorf("stop during entry creation: %v", err)
			}
		}
	}
	run, err := runner.StartExecutionPlanRun(plan, "", "")
	if err != nil {
		t.Fatalf("StartExecutionPlanRun: %v", err)
	}
	runID = run.ID
	first, ok := waitForStart(t, spy, 2*time.Second)
	if !ok {
		t.Fatal("entry 1 did not start")
	}
	completeManagedTest(t, runner, first, "COMPLETE")
	if id, started := waitForStart(t, spy, 300*time.Millisecond); started {
		t.Fatalf("entry 2 (%s) started although the plan was stopped while it was being created", id)
	}
	stored, err := store.GetExecutionPlanRun(runID)
	if err != nil {
		t.Fatalf("GetExecutionPlanRun: %v", err)
	}
	if stored.Status != "halted" || stored.Entries[1].Status != "skipped" || !strings.HasPrefix(stored.Entries[1].Detail, "plan stopped by Alice") {
		t.Fatalf("run after a stop during entry creation = %s %+v", stored.Status, stored.Entries)
	}
	if test := managedTestFor(t, runner, stored.Entries[1].TestID); !isTerminalTestStatus(test.Status) {
		t.Fatalf("the cancelled entry's run is %s, want ended", test.Status)
	}
	if view := runner.ExecutionPlanRunViewFor(stored); view.Outcome != "stopped" {
		t.Fatalf("outcome = %q, want stopped", view.Outcome)
	}
}

func TestStoppingTheLastEntryEndsTheRunAsStopped(t *testing.T) {
	runner, store, spy, ids := newWorkspacePlanRunner(t, "only")
	plan := savedPlan(t, store, &storage.ExecutionPlan{
		ID: "plan-stop-last", ProjectID: "proj-a", Name: "Last", RunMode: "sequential",
		Entries: []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: ids[0]}},
	})
	run, err := runner.StartExecutionPlanRun(plan, "", "")
	if err != nil {
		t.Fatalf("StartExecutionPlanRun: %v", err)
	}
	only, ok := waitForStart(t, spy, 2*time.Second)
	if !ok {
		t.Fatal("entry did not start")
	}
	if _, err := runner.StopExecutionPlanRun(plan.ID, run.ID, testCreationActor{DisplayName: "Alice"}); err != nil && !strings.Contains(err.Error(), "stop run") {
		t.Fatalf("stop: %v", err)
	}
	if test := managedTestFor(t, runner, only); !isTerminalTestStatus(test.Status) {
		completeManagedTest(t, runner, only, "ABORTED")
	}
	stored, err := store.GetExecutionPlanRun(run.ID)
	if err != nil {
		t.Fatalf("GetExecutionPlanRun: %v", err)
	}
	if stored.Status != "halted" || !strings.HasPrefix(stored.Entries[0].Detail, "plan stopped by Alice") {
		t.Fatalf("run after stopping its last entry = %s %+v", stored.Status, stored.Entries)
	}
	if view := runner.ExecutionPlanRunViewFor(stored); view.Outcome != "stopped" {
		t.Fatalf("outcome = %q, want stopped (the stop, not a failure, ended it)", view.Outcome)
	}
}

func TestPlanActivityIgnoresStaleApprovalsAndDecisionsAfterAFailure(t *testing.T) {
	runner, store, _, ids := newWorkspacePlanRunner(t, "only")
	plan := savedPlan(t, store, &storage.ExecutionPlan{ID: "plan-stale", ProjectID: "proj-a", Name: "Stale", Entries: []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: ids[0]}}})
	now := time.Now().UTC()
	for _, approval := range []*storage.ExecutionPlanApproval{
		{ID: "appr-stale", PlanID: plan.ID, ProjectID: "proj-a", Policy: "quota_burst", Status: "pending", PlanContentHash: "sha256:old", RequestedAt: now, CreatedAt: now, UpdatedAt: now},
		{ID: "appr-current", PlanID: plan.ID, ProjectID: "proj-a", Policy: "quota_burst", Status: "pending", PlanContentHash: runner.executionPlanApprovalContentHash(plan), RequestedAt: now.Add(-time.Minute), CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.SaveExecutionPlanApproval(approval); err != nil {
			t.Fatalf("SaveExecutionPlanApproval: %v", err)
		}
	}
	schedule, err := runner.CreateExecutionPlanSchedule(plan.ID, CreateExecutionPlanScheduleRequest{Name: "Nightly", CronExpr: "0 2 * * *", Timezone: "UTC"})
	if err != nil {
		t.Fatalf("CreateExecutionPlanSchedule: %v", err)
	}
	// After a failed run the schedule asks for a decision but has moved on to
	// its next run: nothing is held.
	schedule.NextRequiredAction = "operator_decision_required"
	schedule.LastError = "materialize failed"
	if err := store.SaveExecutionPlanSchedule(schedule); err != nil {
		t.Fatalf("SaveExecutionPlanSchedule: %v", err)
	}
	activity, err := runner.ExecutionPlanActivityFor(plan)
	if err != nil {
		t.Fatalf("activity: %v", err)
	}
	if activity.PendingApproval == nil || activity.PendingApproval.ID != "appr-current" {
		t.Fatalf("pending approval = %+v, want only the request for the plan as it is", activity.PendingApproval)
	}
	if len(activity.Waiting) != 0 {
		t.Fatalf("waiting = %+v, want none: the decision follows a failure, the next run is not held", activity.Waiting)
	}
}

func TestAFailureAfterStopPlanKeepsItsErrorAndReadsFailed(t *testing.T) {
	runner, store, spy, ids := newWorkspacePlanRunner(t, "first", "second")
	plan := savedPlan(t, store, &storage.ExecutionPlan{
		ID: "plan-stop-fail", ProjectID: "proj-a", Name: "Fails", RunMode: "sequential",
		Entries: []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: ids[0]}, {Order: 1, ScenarioID: ids[1]}},
	})
	run, err := runner.StartExecutionPlanRun(plan, "", "")
	if err != nil {
		t.Fatalf("StartExecutionPlanRun: %v", err)
	}
	first, ok := waitForStart(t, spy, 2*time.Second)
	if !ok {
		t.Fatal("entry 1 did not start")
	}
	// Stop plan is recorded; before the running entry stops it fails on its own.
	runner.seqMu.Lock()
	if _, err := runner.stopSequentialPlanRunLocked(runner.seqPlanRuns[run.ID], "plan stopped by Alice"); err != nil {
		runner.seqMu.Unlock()
		t.Fatalf("stop: %v", err)
	}
	runner.seqMu.Unlock()
	test := managedTestFor(t, runner, first)
	runner.mu.RLock()
	live := runner.tests[test.ID]
	runner.mu.RUnlock()
	if err := runner.completeTestRun(live, "FAILED", time.Now().UTC(), "script error on login", nil); err != nil {
		t.Fatalf("completeTestRun: %v", err)
	}
	stored, err := store.GetExecutionPlanRun(run.ID)
	if err != nil {
		t.Fatalf("GetExecutionPlanRun: %v", err)
	}
	if stored.Status != "halted" || stored.Entries[0].Detail != "script error on login" || !stored.Entries[0].Stopped || !stored.Entries[1].Stopped {
		t.Fatalf("run after a failure following a stop = %s %+v", stored.Status, stored.Entries)
	}
	if view := runner.ExecutionPlanRunViewFor(stored); view.Outcome != "failed" {
		t.Fatalf("outcome = %q, want failed: the entry's own failure outranks the stop", view.Outcome)
	}
}

func TestStopBeforeRecoveryRestoresTheRunIsAppliedOnRestore(t *testing.T) {
	runner, store, spy, ids := newWorkspacePlanRunner(t, "first", "second")
	plan := savedPlan(t, store, &storage.ExecutionPlan{
		ID: "plan-stop-takeover", ProjectID: "proj-a", Name: "Takeover", RunMode: "sequential",
		Entries: []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: ids[0]}, {Order: 1, ScenarioID: ids[1]}},
	})
	run, err := runner.StartExecutionPlanRun(plan, "", "")
	if err != nil {
		t.Fatalf("StartExecutionPlanRun: %v", err)
	}
	first, ok := waitForStart(t, spy, 2*time.Second)
	if !ok {
		t.Fatal("entry 1 did not start")
	}
	// A takeover: this controller holds no state for the run yet.
	runner.seqMu.Lock()
	delete(runner.seqPlanRuns, run.ID)
	delete(runner.seqPlanByTest, first)
	runner.seqMu.Unlock()
	if _, err := runner.StopExecutionPlanRun(plan.ID, run.ID, testCreationActor{DisplayName: "Alice"}); err != nil && !strings.Contains(err.Error(), "stop run") {
		t.Fatalf("stop: %v", err)
	}
	runner.seqMu.Lock()
	pending := runner.pendingPlanStops[run.ID]
	runner.seqMu.Unlock()
	if pending != "plan stopped by Alice" {
		t.Fatalf("pending stop = %q", pending)
	}
	if test := managedTestFor(t, runner, first); !isTerminalTestStatus(test.Status) {
		completeManagedTest(t, runner, first, "COMPLETE")
	}
	if err := runner.recoverSequentialExecutionPlanRuns(); err != nil {
		t.Fatalf("recovery: %v", err)
	}
	if id, started := waitForStart(t, spy, 300*time.Millisecond); started {
		t.Fatalf("entry 2 (%s) started after the plan was stopped before recovery", id)
	}
	stored, err := store.GetExecutionPlanRun(run.ID)
	if err != nil {
		t.Fatalf("GetExecutionPlanRun: %v", err)
	}
	if stored.Status != "halted" || stored.Entries[1].Status != "skipped" || !stored.Entries[1].Stopped {
		t.Fatalf("run restored after a stop = %s %+v", stored.Status, stored.Entries)
	}
	runner.seqMu.Lock()
	_, stillPending := runner.pendingPlanStops[run.ID]
	runner.seqMu.Unlock()
	if stillPending {
		t.Fatal("the pending stop should be consumed when recovery restores the run")
	}
}

// stopCommitUnknownStore commits the armed CAS but loses its response and the
// next read, so the write's outcome is unknown to the writer.
type stopCommitUnknownStore struct {
	base     *storage.SQLiteStore
	mu       sync.Mutex
	armed    bool
	failGets int
}

func (s *stopCommitUnknownStore) SaveExecutionPlanRun(run *storage.ExecutionPlanRun) error {
	return s.base.SaveExecutionPlanRun(run)
}

func (s *stopCommitUnknownStore) GetExecutionPlanRun(id string) (*storage.ExecutionPlanRun, error) {
	s.mu.Lock()
	if s.failGets > 0 {
		s.failGets--
		s.mu.Unlock()
		return nil, errors.New("simulated read failure")
	}
	s.mu.Unlock()
	return s.base.GetExecutionPlanRun(id)
}

func (s *stopCommitUnknownStore) ListExecutionPlanRuns(planID string, limit, offset int) ([]*storage.ExecutionPlanRun, error) {
	return s.base.ListExecutionPlanRuns(planID, limit, offset)
}

func (s *stopCommitUnknownStore) CreateExecutionPlanRunIfAbsent(run *storage.ExecutionPlanRun) (bool, error) {
	return s.base.CreateExecutionPlanRunIfAbsent(run)
}

func (s *stopCommitUnknownStore) CompareAndSwapExecutionPlanRun(run *storage.ExecutionPlanRun, expectedRevision int64) (bool, error) {
	updated, err := s.base.CompareAndSwapExecutionPlanRun(run, expectedRevision)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.armed && err == nil && updated {
		s.armed = false
		s.failGets = 1
		return false, errors.New("simulated lost commit response")
	}
	return updated, err
}

func TestStopWhoseCommitIsUnknownKeepsTheCommittedStop(t *testing.T) {
	runner, store, spy, ids := newWorkspacePlanRunner(t, "first", "second")
	double := &stopCommitUnknownStore{base: store}
	runner.execPlanRunStore = double
	plan := savedPlan(t, store, &storage.ExecutionPlan{
		ID: "plan-stop-unknown", ProjectID: "proj-a", Name: "Unknown", RunMode: "sequential",
		Entries: []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: ids[0]}, {Order: 1, ScenarioID: ids[1]}},
	})
	run, err := runner.StartExecutionPlanRun(plan, "", "")
	if err != nil {
		t.Fatalf("StartExecutionPlanRun: %v", err)
	}
	first, ok := waitForStart(t, spy, 2*time.Second)
	if !ok {
		t.Fatal("entry 1 did not start")
	}
	double.mu.Lock()
	double.armed = true
	double.mu.Unlock()
	runner.seqMu.Lock()
	marked, err := runner.stopSequentialPlanRunLocked(runner.seqPlanRuns[run.ID], "plan stopped by Alice")
	stopDetail := runner.seqPlanRuns[run.ID].stopDetail
	runner.seqMu.Unlock()
	if err != nil || !marked || stopDetail != "plan stopped by Alice" {
		t.Fatalf("stop with an unknown commit = %v, %v, %q; want the committed stop kept", marked, err, stopDetail)
	}
	completeManagedTest(t, runner, first, "COMPLETE")
	if id, started := waitForStart(t, spy, 300*time.Millisecond); started {
		t.Fatalf("entry 2 (%s) started after a stop whose commit outcome was unknown", id)
	}
	stored, err := store.GetExecutionPlanRun(run.ID)
	if err != nil {
		t.Fatalf("GetExecutionPlanRun: %v", err)
	}
	if stored.Status != "halted" || !stored.Entries[1].Stopped {
		t.Fatalf("run = %s %+v, want halted with entry 2 stopped", stored.Status, stored.Entries)
	}
}

func TestRecoveryDoesNotRecreateAStoppedEntrysRun(t *testing.T) {
	runner, store, spy, ids := newWorkspacePlanRunner(t, "first", "second")
	plan := savedPlan(t, store, &storage.ExecutionPlan{
		ID: "plan-stop-recover", ProjectID: "proj-a", Name: "Recover", RunMode: "sequential",
		Entries: []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: ids[0]}, {Order: 1, ScenarioID: ids[1]}},
	})
	run, err := runner.StartExecutionPlanRun(plan, "", "")
	if err != nil {
		t.Fatalf("StartExecutionPlanRun: %v", err)
	}
	first, ok := waitForStart(t, spy, 2*time.Second)
	if !ok {
		t.Fatal("entry 1 did not start")
	}
	// A takeover with the running entry's run missing from this controller, and
	// a stop waiting for recovery.
	runner.seqMu.Lock()
	delete(runner.seqPlanRuns, run.ID)
	delete(runner.seqPlanByTest, first)
	runner.pendingPlanStops = map[string]string{run.ID: "plan stopped by Alice"}
	runner.seqMu.Unlock()
	runner.mu.Lock()
	delete(runner.tests, first)
	runner.mu.Unlock()
	if err := runner.recoverSequentialExecutionPlanRuns(); err != nil {
		t.Fatalf("recovery: %v", err)
	}
	if id, started := waitForStart(t, spy, 300*time.Millisecond); started {
		t.Fatalf("recovery started %s after the plan was stopped", id)
	}
	stored, err := store.GetExecutionPlanRun(run.ID)
	if err != nil {
		t.Fatalf("GetExecutionPlanRun: %v", err)
	}
	if stored.Status != "halted" || stored.Entries[0].Status != "skipped" || !stored.Entries[0].Stopped || !stored.Entries[1].Stopped {
		t.Fatalf("recovered stopped run = %s %+v", stored.Status, stored.Entries)
	}
}

func TestStopDetailIsReadFromTheStopNotAFailure(t *testing.T) {
	entries := []storage.ExecutionPlanRunEntry{
		{Status: "failed", Detail: "script error on login", Stopped: true},
		{Status: "skipped", Detail: "plan stopped by Alice", Stopped: true},
	}
	if got := executionPlanRunStopDetailOf(entries); got != "plan stopped by Alice" {
		t.Fatalf("stop detail = %q", got)
	}
	if got := executionPlanRunStopDetailOf(entries[:1]); got != "plan stopped" {
		t.Fatalf("stop detail without a stop-written entry = %q, want the plain description", got)
	}
	if got := executionPlanRunStopDetailOf([]storage.ExecutionPlanRunEntry{{Status: "failed", Detail: "plan stopped by a script message"}}); got != "" {
		t.Fatalf("an unflagged entry is not a stop: %q", got)
	}
}

func TestPrunePendingPlanStopsForgetsRunsNoLongerRunning(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	runner.pendingPlanStops = map[string]string{"live": "plan stopped", "gone": "plan stopped"}
	runner.prunePendingPlanStops([]*storage.ExecutionPlanRun{{ID: "live", RunMode: "sequential", Status: "running"}})
	if _, ok := runner.pendingPlanStops["gone"]; ok || runner.pendingPlanStops["live"] == "" {
		t.Fatalf("pending stops after pruning = %v", runner.pendingPlanStops)
	}
}

func TestASilentFailureAfterStopPlanSaysSo(t *testing.T) {
	runner, store, spy, ids := newWorkspacePlanRunner(t, "only")
	plan := savedPlan(t, store, &storage.ExecutionPlan{
		ID: "plan-stop-silent", ProjectID: "proj-a", Name: "Silent", RunMode: "sequential",
		Entries: []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: ids[0]}},
	})
	run, err := runner.StartExecutionPlanRun(plan, "", "")
	if err != nil {
		t.Fatalf("StartExecutionPlanRun: %v", err)
	}
	only, ok := waitForStart(t, spy, 2*time.Second)
	if !ok {
		t.Fatal("entry did not start")
	}
	runner.seqMu.Lock()
	if _, err := runner.stopSequentialPlanRunLocked(runner.seqPlanRuns[run.ID], "plan stopped by Alice"); err != nil {
		runner.seqMu.Unlock()
		t.Fatalf("stop: %v", err)
	}
	runner.seqMu.Unlock()
	completeManagedTest(t, runner, only, "FAILED")
	stored, err := store.GetExecutionPlanRun(run.ID)
	if err != nil {
		t.Fatalf("GetExecutionPlanRun: %v", err)
	}
	if stored.Entries[0].Detail != "run failed before the plan stop took effect" {
		t.Fatalf("detail of a silent failure after a stop = %q", stored.Entries[0].Detail)
	}
}

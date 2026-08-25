package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

type approvalSnapshotBlockingBackend struct {
	started     chan string
	release     chan struct{}
	releaseOnce sync.Once
	count       atomic.Int32
}

func TestScenarioPeakVUsersUsesOffsetStageWindows(t *testing.T) {
	scenario := &StoredScenario{
		Duration: "2m",
		Scripts: []ScenarioScriptRef{
			{Name: "first", VUsers: 60, Stages: []storage.LoadStage{{Type: "hold", Duration: "1m", TargetVUsers: 60}}},
			{Name: "second", VUsers: 80, StartOffset: "1m", Stages: []storage.LoadStage{{Type: "hold", Duration: "1m", TargetVUsers: 80}}},
		},
	}
	if got := scenarioPeakVUsers(scenario); got != 80 {
		t.Fatalf("scenario peak = %d, want 80", got)
	}
}

func (b *approvalSnapshotBlockingBackend) Run(_ *TestRunner, test *ManagedTest) {
	b.started <- test.ID
	if b.count.Add(1) == 1 {
		<-b.release
	}
}

func (b *approvalSnapshotBlockingBackend) unblock() {
	b.releaseOnce.Do(func() { close(b.release) })
}

func TestExecutionPlanRunGovernanceGate(t *testing.T) {
	server, store := newTimeslotServer(t)
	if err := store.SaveProject(&storage.Project{ID: "proj-a", Name: "A", Settings: storage.ProjectSettings{MaxVUsers: 10}, CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	server.runner.StoreScript(&dsl.Script{
		ID: "s", Name: "S", Protocol: "http", Version: "1.0.0", ProjectID: "proj-a",
		Config:  dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions: []dsl.Action{{Name: "a", Type: "request", Target: "/"}},
	})
	scenario, err := server.runner.StoreScenario(CreateScenarioRequest{
		Name: "Big", Duration: "5m",
		Groups: []CreateGroupEntry{{ScriptID: "s", VUsers: 100}},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	now := time.Now().UTC()
	plan := &storage.ExecutionPlan{
		ID: "plan-gov-1", ProjectID: "proj-a", Name: "Gov Plan", RunMode: "parallel",
		Entries:   []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: scenario.ID}},
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveExecutionPlan(plan); err != nil {
		t.Fatalf("SaveExecutionPlan: %v", err)
	}

	// Peak 100 VUsers > project quota 10 → first run is blocked + pending approval.
	res1, err := server.runner.MaterializeAndRunExecutionPlan(plan, "user:alice", "Alice")
	if err != nil {
		t.Fatalf("materialize 1: %v", err)
	}
	if !res1.Blocked || res1.Approval == nil || res1.Approval.Status != "pending" {
		t.Fatalf("first run = %+v, want blocked + pending approval", res1)
	}
	if res1.Approval.PlanContentHash == "" {
		t.Fatal("pending approval must be bound to a plan content hash")
	}
	// Approve it.
	approved, ok, err := server.runner.ResolveExecutionPlanApproval(plan.ID, res1.Approval.ID, true, "user:admin", "ok")
	if err != nil || !ok || approved.Status != "approved" {
		t.Fatalf("resolve = (%+v, ok=%v, err=%v), want approved", approved, ok, err)
	}
	if approved.PlanContentHash != res1.Approval.PlanContentHash {
		t.Fatalf("approved hash = %q, want %q", approved.PlanContentHash, res1.Approval.PlanContentHash)
	}
	// Re-resolving a non-pending approval is a conflict.
	if _, found, err := server.runner.ResolveExecutionPlanApproval(plan.ID, res1.Approval.ID, true, "user:admin", "again"); err == nil || !found {
		t.Fatalf("double-resolve = (found=%v, err=%v), want found + error", found, err)
	}
	// Second run consumes the approval and proceeds.
	res2, err := server.runner.MaterializeAndRunExecutionPlan(plan, "user:alice", "Alice")
	if err != nil {
		t.Fatalf("materialize 2: %v", err)
	}
	if res2.Blocked || res2.RunID == "" {
		t.Fatalf("second run = %+v, want allowed with a run id", res2)
	}
	if len(res2.Results) != 1 || res2.Results[0].TestID == "" {
		t.Fatalf("second run results = %+v, want created test id", res2.Results)
	}
	createdTest, ok := server.runner.GetTest(res2.Results[0].TestID)
	if !ok || createdTest.ExecutionPlanApproval == nil {
		t.Fatalf("created test = %+v, want execution plan approval snapshot", createdTest)
	}
	if createdTest.ExecutionPlanApproval.ID != approved.ID || createdTest.ExecutionPlanApproval.PlanID != plan.ID || createdTest.ExecutionPlanApproval.RequiredRole != RoleProjectAdmin || createdTest.ExecutionPlanApproval.ResolvedBy != "user:admin" || createdTest.ExecutionPlanApproval.PlanContentHash != approved.PlanContentHash {
		t.Fatalf("approval snapshot = %+v, want approved plan governance", createdTest.ExecutionPlanApproval)
	}
	// Third run is blocked again — the approval was single-use.
	res3, err := server.runner.MaterializeAndRunExecutionPlan(plan, "user:alice", "Alice")
	if err != nil {
		t.Fatalf("materialize 3: %v", err)
	}
	if !res3.Blocked {
		t.Fatalf("third run = %+v, want blocked again", res3)
	}
}

func TestExecutionPlanRunGovernanceIncludesStagesAndGoalMax(t *testing.T) {
	server, store := newTimeslotServer(t)
	if err := store.SaveProject(&storage.Project{ID: "proj-a", Name: "A", Settings: storage.ProjectSettings{MaxVUsers: 10}, CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	server.runner.StoreScript(&dsl.Script{
		ID: "s-peak", Name: "S", Protocol: "http", Version: "1.0.0", ProjectID: "proj-a",
		Config:  dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions: []dsl.Action{{Name: "a", Type: "request", Target: "/"}},
	})
	maxVUsers := 40
	cases := []struct {
		name     string
		req      CreateScenarioRequest
		scenario *StoredScenario
		want     int
	}{
		{
			name: "stage peak",
			scenario: &StoredScenario{
				ID: "legacy-stage-peak", ProjectID: "proj-a", Name: "Staged", Duration: "5m",
				Scripts: []ScenarioScriptRef{{Order: 0, ScriptID: "s-peak", VUsers: 1, Stages: []storage.LoadStage{{Name: "hold", Type: "hold", Duration: "1m", TargetVUsers: 25}}}},
			},
			want: 25,
		},
		{
			name: "goal max",
			req: CreateScenarioRequest{
				Name: "Goal", Duration: "5m", ScenarioMode: "goal_oriented", GoalConfig: &GoalConfig{MaxVUsers: &maxVUsers},
				Groups: []CreateGroupEntry{{ScriptID: "s-peak", VUsers: 1}},
			},
			want: 40,
		},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scenario := tc.scenario
			if scenario == nil {
				var err error
				scenario, err = server.runner.StoreScenario(tc.req)
				if err != nil {
					t.Fatalf("StoreScenario: %v", err)
				}
			} else {
				server.runner.mu.Lock()
				server.runner.scenarios[scenario.ID] = scenario
				server.runner.mu.Unlock()
			}
			plan := &storage.ExecutionPlan{
				ID: "plan-peak-" + string(rune('a'+i)), ProjectID: "proj-a", Name: tc.name, RunMode: "parallel",
				Entries: []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: scenario.ID}},
			}
			if got := server.runner.executionPlanAggregatePeak(plan); got != tc.want {
				t.Fatalf("aggregate peak = %d, want %d", got, tc.want)
			}
			if err := store.SaveExecutionPlan(plan); err != nil {
				t.Fatalf("SaveExecutionPlan: %v", err)
			}
			res, err := server.runner.MaterializeAndRunExecutionPlan(plan, "user:alice", "Alice")
			if err != nil {
				t.Fatalf("MaterializeAndRunExecutionPlan: %v", err)
			}
			if !res.Blocked || res.Approval == nil {
				t.Fatalf("result = %+v, want approval block", res)
			}
			if !strings.Contains(res.Approval.DecisionReason, "Aggregate peak "+strconv.Itoa(tc.want)+" VUsers") {
				t.Fatalf("decision reason = %q, want peak %d", res.Approval.DecisionReason, tc.want)
			}
		})
	}
}

func TestExecutionPlanApprovalInvalidatedWhenPlanChanges(t *testing.T) {
	server, store := newTimeslotServer(t)
	if err := store.SaveProject(&storage.Project{ID: "proj-a", Name: "A", Settings: storage.ProjectSettings{MaxVUsers: 10}, CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	server.runner.StoreScript(&dsl.Script{
		ID: "s-stale", Name: "S", Protocol: "http", Version: "1.0.0", ProjectID: "proj-a",
		Config:  dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions: []dsl.Action{{Name: "a", Type: "request", Target: "/"}},
	})
	scenario, err := server.runner.StoreScenario(CreateScenarioRequest{
		Name: "Big", Duration: "5m",
		Groups: []CreateGroupEntry{{ScriptID: "s-stale", VUsers: 100}},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	plan := &storage.ExecutionPlan{
		ID: "plan-stale", ProjectID: "proj-a", Name: "Stale", RunMode: "parallel",
		Entries: []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: scenario.ID}},
	}
	if err := store.SaveExecutionPlan(plan); err != nil {
		t.Fatalf("SaveExecutionPlan: %v", err)
	}
	first, err := server.runner.MaterializeAndRunExecutionPlan(plan, "user:alice", "Alice")
	if err != nil {
		t.Fatalf("materialize first: %v", err)
	}
	if !first.Blocked || first.Approval == nil {
		t.Fatalf("first result = %+v, want pending approval", first)
	}
	originalHash := first.Approval.PlanContentHash

	plan.Entries = append(plan.Entries, storage.ExecutionPlanEntry{Order: 1, ScenarioID: scenario.ID})
	if err := store.SaveExecutionPlan(plan); err != nil {
		t.Fatalf("SaveExecutionPlan mutated: %v", err)
	}
	if _, ok, err := server.runner.ResolveExecutionPlanApproval(plan.ID, first.Approval.ID, true, "user:admin", "late"); err == nil || !ok || !strings.Contains(err.Error(), "no longer matches") {
		t.Fatalf("stale resolve = ok %v err %v, want mismatch error", ok, err)
	}
	second, err := server.runner.MaterializeAndRunExecutionPlan(plan, "user:alice", "Alice")
	if err != nil {
		t.Fatalf("materialize second: %v", err)
	}
	if !second.Blocked || second.Approval == nil || second.Approval.ID == first.Approval.ID {
		t.Fatalf("second result = %+v, want fresh pending approval", second)
	}
	if second.Approval.PlanContentHash == "" || second.Approval.PlanContentHash == originalHash {
		t.Fatalf("fresh approval hash = %q, original %q", second.Approval.PlanContentHash, originalHash)
	}
}

func TestApprovedSequentialExecutionPlanBypassesProjectVUserGate(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	if err := store.SaveProject(&storage.Project{ID: "proj-a", Name: "A", Settings: storage.ProjectSettings{MaxVUsers: 10}, CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	runner := NewTestRunner(10, NewHub(), store)
	spy := &executionBackendSpy{called: make(chan string, 1)}
	runner.setExecutionBackend(spy)
	runner.StoreScript(&dsl.Script{
		ID: "s-seq-approved", ProjectID: "proj-a", Name: "S", Protocol: "http", Version: "1.0.0",
		Config:  dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions: []dsl.Action{{Name: "a", Type: "request", Target: "/"}},
	})
	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		Name: "Big Sequential", Duration: "5m",
		Groups: []CreateGroupEntry{{ScriptID: "s-seq-approved", VUsers: 100}},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	plan := &storage.ExecutionPlan{
		ID: "plan-seq-approved", ProjectID: "proj-a", Name: "Approved Sequential", RunMode: "sequential",
		Entries: []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: scenario.ID}},
	}
	if err := store.SaveExecutionPlan(plan); err != nil {
		t.Fatalf("SaveExecutionPlan: %v", err)
	}
	blocked, err := runner.MaterializeAndRunExecutionPlan(plan, "user:alice", "Alice")
	if err != nil {
		t.Fatalf("blocked materialize: %v", err)
	}
	if !blocked.Blocked || blocked.Approval == nil {
		t.Fatalf("blocked result = %+v, want approval block", blocked)
	}
	if _, ok, err := runner.ResolveExecutionPlanApproval(plan.ID, blocked.Approval.ID, true, "user:admin", "ok"); err != nil || !ok {
		t.Fatalf("ResolveExecutionPlanApproval: ok=%v err=%v", ok, err)
	}
	allowed, err := runner.MaterializeAndRunExecutionPlan(plan, "user:alice", "Alice")
	if err != nil {
		t.Fatalf("allowed materialize: %v", err)
	}
	if allowed.Blocked || allowed.RunID == "" {
		t.Fatalf("allowed result = %+v, want started plan run", allowed)
	}
	startedID, ok := waitForStart(t, spy, 2*time.Second)
	if !ok {
		persistedRun, _ := store.GetExecutionPlanRun(allowed.RunID)
		t.Fatalf("approved over-quota sequential entry did not start; tests=%+v run=%+v", runner.ListTests(), persistedRun)
	}
	started, ok := runner.GetTest(startedID)
	if !ok {
		t.Fatalf("started test %q not found", startedID)
	}
	if started.Status == "PENDING" {
		t.Fatalf("approved test is pending: %s", started.PendingReason)
	}
	if started.ExecutionPlanApproval == nil || started.ExecutionPlanApproval.PlanContentHash == "" {
		t.Fatalf("started test approval snapshot = %+v, want content hash", started.ExecutionPlanApproval)
	}
}

func TestApprovedSequentialPlanRejectsScenarioChangeBeforeLaterEntry(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir() + "/approval-snapshot.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{ID: "proj-a", Name: "A", Settings: storage.ProjectSettings{MaxVUsers: 10}, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	runner := NewTestRunner(10, NewHub(), store)
	backend := &approvalSnapshotBlockingBackend{started: make(chan string, 2), release: make(chan struct{})}
	runner.setExecutionBackend(backend)
	t.Cleanup(backend.unblock)
	runner.StoreScript(&dsl.Script{
		ID: "approval-snapshot-script", Name: "S", Protocol: "http", Version: "1.0.0", ProjectID: "proj-a",
		Config: dsl.ScriptConfig{BaseURL: "https://example.com"}, Actions: []dsl.Action{{Name: "a", Type: "request", Target: "/"}},
	})
	makeScenario := func(name string, vusers int) *StoredScenario {
		scenario, err := runner.StoreScenario(CreateScenarioRequest{Name: name, Duration: "5m", Groups: []CreateGroupEntry{{ScriptID: "approval-snapshot-script", VUsers: vusers}}})
		if err != nil {
			t.Fatal(err)
		}
		return scenario
	}
	firstScenario := makeScenario("First", 100)
	secondScenario := makeScenario("Second", 100)
	plan := &storage.ExecutionPlan{
		ID: "plan-approved-snapshot", ProjectID: "proj-a", Name: "Approved Snapshot", RunMode: "sequential",
		Entries: []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: firstScenario.ID}, {Order: 1, ScenarioID: secondScenario.ID}},
	}
	if err := store.SaveExecutionPlan(plan); err != nil {
		t.Fatal(err)
	}
	blocked, err := runner.MaterializeAndRunExecutionPlan(plan, "user:alice", "Alice")
	if err != nil || !blocked.Blocked || blocked.Approval == nil {
		t.Fatalf("initial materialize = %+v, %v", blocked, err)
	}
	if _, ok, err := runner.ResolveExecutionPlanApproval(plan.ID, blocked.Approval.ID, true, "user:admin", "approved"); err != nil || !ok {
		t.Fatalf("approve = %t, %v", ok, err)
	}
	started, err := runner.MaterializeAndRunExecutionPlan(plan, "user:alice", "Alice")
	if err != nil || started.Blocked || started.RunID == "" {
		t.Fatalf("approved materialize = %+v, %v", started, err)
	}
	var firstTestID string
	select {
	case firstTestID = <-backend.started:
	case <-time.After(2 * time.Second):
		t.Fatal("first sequential entry did not start")
	}
	if _, found, err := runner.UpdateScenario(secondScenario.ID, CreateScenarioRequest{
		Name: "Second expanded", Duration: "5m", Groups: []CreateGroupEntry{{ScriptID: "approval-snapshot-script", VUsers: 10_000}},
	}); err != nil || !found {
		t.Fatalf("update second scenario = %t, %v", found, err)
	}
	runner.mu.Lock()
	firstTest := runner.tests[firstTestID]
	if firstTest == nil {
		runner.mu.Unlock()
		t.Fatalf("first test %s disappeared", firstTestID)
	}
	firstTest.Status = "COMPLETE"
	runner.mu.Unlock()
	if err := runner.advanceExecutionPlanForTest(firstTest, "COMPLETE"); err != nil {
		t.Fatalf("advance first entry: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		run, loadErr := store.GetExecutionPlanRun(started.RunID)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		if run.Status == "halted" || run.Status == "failed" {
			if len(run.Entries) != 2 || !strings.Contains(run.Entries[1].Detail, "content changed after quota approval") {
				t.Fatalf("terminal run = %+v", run)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run did not reject changed scenario: %+v", run)
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case id := <-backend.started:
		t.Fatalf("changed second entry launched as %s", id)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestExecutionPlanScheduleSweep(t *testing.T) {
	server, store := newTimeslotServer(t)
	now := time.Now().UTC()
	plan := &storage.ExecutionPlan{
		ID:        "plan-sched-1",
		ProjectID: "proj-a",
		Name:      "Nightly Plan",
		RunMode:   "parallel",
		Entries:   []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: "missing-x"}},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.SaveExecutionPlan(plan); err != nil {
		t.Fatalf("SaveExecutionPlan: %v", err)
	}

	schedule, err := server.runner.CreateExecutionPlanSchedule(plan.ID, CreateExecutionPlanScheduleRequest{Name: "Nightly", CronExpr: "0 9 * * *", Timezone: "UTC"})
	if err != nil {
		t.Fatalf("CreateExecutionPlanSchedule: %v", err)
	}
	if schedule.NextRunAt == nil {
		t.Fatal("expected next_run_at on a new enabled schedule")
	}

	// Force the schedule due and sweep.
	past := now.Add(-time.Minute)
	schedule.NextRunAt = &past
	if err := store.SaveExecutionPlanSchedule(schedule); err != nil {
		t.Fatalf("SaveExecutionPlanSchedule: %v", err)
	}
	server.runner.sweepDueExecutionPlanSchedules(now)

	occurrences, err := store.ListExecutionPlanScheduleOccurrences(schedule.ID)
	if err != nil || len(occurrences) != 1 {
		t.Fatalf("occurrences = %d (err %v), want 1", len(occurrences), err)
	}
	if occurrences[0].Status != "materialized" || occurrences[0].RunID == "" {
		t.Fatalf("occurrence = %+v, want materialized with a run id", occurrences[0])
	}
	updated, err := store.GetExecutionPlanSchedule(schedule.ID)
	if err != nil || updated == nil {
		t.Fatalf("GetExecutionPlanSchedule: %v", err)
	}
	if updated.NextRunAt == nil || !updated.NextRunAt.After(past) {
		t.Fatalf("next_run_at = %v, want advanced past %v", updated.NextRunAt, past)
	}
	if updated.LastRunRunID == "" {
		t.Fatal("expected last_run_run_id to be set after materialization")
	}

	// Idempotent: a second sweep (schedule no longer due) creates no duplicate.
	server.runner.sweepDueExecutionPlanSchedules(now)
	if again, _ := store.ListExecutionPlanScheduleOccurrences(schedule.ID); len(again) != 1 {
		t.Fatalf("after second sweep occurrences = %d, want 1 (no duplicate)", len(again))
	}
}

func TestExecutionPlanScheduleRunNameTemplateNamesMaterializedTests(t *testing.T) {
	server, store := newTimeslotServer(t)
	if err := store.SaveProject(&storage.Project{ID: "proj-a", Name: "A", CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	server.runner.StoreScript(&dsl.Script{
		ID: "script-template", Name: "Template", Protocol: "http", Version: "1.0.0", ProjectID: "proj-a",
		Config:  dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions: []dsl.Action{{Name: "Home", Type: "request", Target: "/"}},
	})
	scenario, err := server.runner.StoreScenario(CreateScenarioRequest{
		Name: "Checkout Scenario", Duration: "5m",
		Groups: []CreateGroupEntry{{ScriptID: "script-template", VUsers: 1}},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	plannedAt := time.Date(2026, 7, 2, 9, 30, 0, 0, time.UTC)
	plan := &storage.ExecutionPlan{
		ID:        "plan-template",
		ProjectID: "proj-a",
		Name:      "Nightly Plan",
		RunMode:   "parallel",
		Entries:   []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: scenario.ID, ScenarioName: "Checkout"}},
		CreatedAt: plannedAt,
		UpdatedAt: plannedAt,
	}
	if err := store.SaveExecutionPlan(plan); err != nil {
		t.Fatalf("SaveExecutionPlan: %v", err)
	}
	schedule, err := server.runner.CreateExecutionPlanSchedule(plan.ID, CreateExecutionPlanScheduleRequest{
		Name:            "Weekday",
		CronExpr:        "30 9 * * *",
		Timezone:        "UTC",
		RunNameTemplate: "{{plan}} {{date}} {{time}}",
	})
	if err != nil {
		t.Fatalf("CreateExecutionPlanSchedule: %v", err)
	}
	schedule.NextRunAt = &plannedAt
	if err := store.SaveExecutionPlanSchedule(schedule); err != nil {
		t.Fatalf("SaveExecutionPlanSchedule: %v", err)
	}

	server.runner.sweepDueExecutionPlanSchedules(plannedAt.Add(time.Minute))

	occurrences, err := store.ListExecutionPlanScheduleOccurrences(schedule.ID)
	if err != nil || len(occurrences) != 1 {
		t.Fatalf("occurrences = %d (err %v), want 1", len(occurrences), err)
	}
	run, err := store.GetExecutionPlanRun(occurrences[0].RunID)
	if err != nil || run == nil || len(run.Entries) != 1 {
		t.Fatalf("plan run = %+v err=%v, want one entry", run, err)
	}
	test, ok := server.runner.GetTest(run.Entries[0].TestID)
	if !ok {
		t.Fatalf("materialized test %q not found", run.Entries[0].TestID)
	}
	if test.Name != "Nightly Plan 2026-07-02 09:30 / Checkout" {
		t.Fatalf("test name = %q, want rendered schedule template", test.Name)
	}
}

func TestExecutionPlanScheduleSweepAppliesMissedRunPolicies(t *testing.T) {
	for _, tc := range []struct {
		name               string
		policy             string
		wantStatus         string
		wantReason         string
		wantRequiredAction string
	}{
		{name: "skip", policy: "skip", wantStatus: "skipped", wantReason: "missed_run_skipped"},
		{name: "operator decision", policy: "require_operator_decision", wantStatus: "blocked", wantReason: "missed_run_requires_decision", wantRequiredAction: "operator_decision_required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, store := newTimeslotServer(t)
			now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
			plan := &storage.ExecutionPlan{
				ID:        "plan-missed-" + tc.policy,
				ProjectID: "proj-a",
				Name:      "Missed Plan",
				RunMode:   "parallel",
				Entries:   []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: "missing"}},
				CreatedAt: now,
				UpdatedAt: now,
			}
			if err := store.SaveExecutionPlan(plan); err != nil {
				t.Fatalf("SaveExecutionPlan: %v", err)
			}
			missed := now.Add(-10 * time.Minute)
			schedule := &storage.ExecutionPlanSchedule{
				ID:              "schedule-missed-" + tc.policy,
				PlanID:          plan.ID,
				ProjectID:       "proj-a",
				Revision:        1,
				Name:            "Missed",
				CronExpr:        "* * * * *",
				Timezone:        "UTC",
				RunNameTemplate: "Missed",
				Enabled:         true,
				MissedRunPolicy: tc.policy,
				NextRunAt:       &missed,
				CreatedAt:       now.Add(-time.Hour),
				UpdatedAt:       now.Add(-time.Hour),
			}
			if err := store.SaveExecutionPlanSchedule(schedule); err != nil {
				t.Fatalf("SaveExecutionPlanSchedule: %v", err)
			}
			server.runner.sweepDueExecutionPlanSchedules(now)
			occurrences, err := store.ListExecutionPlanScheduleOccurrences(schedule.ID)
			if err != nil {
				t.Fatalf("ListExecutionPlanScheduleOccurrences: %v", err)
			}
			if len(occurrences) != 1 || occurrences[0].Status != tc.wantStatus || occurrences[0].ReasonCode != tc.wantReason {
				t.Fatalf("occurrences = %+v, want status=%s reason=%s", occurrences, tc.wantStatus, tc.wantReason)
			}
			updated, err := store.GetExecutionPlanSchedule(schedule.ID)
			if err != nil {
				t.Fatalf("GetExecutionPlanSchedule: %v", err)
			}
			if updated.NextRequiredAction != tc.wantRequiredAction {
				t.Fatalf("next_required_action = %q, want %q", updated.NextRequiredAction, tc.wantRequiredAction)
			}
		})
	}
}

func TestCreateExecutionPlanScheduleAllowsOpenEndedPause(t *testing.T) {
	server, store := newTimeslotServer(t)
	now := time.Now().UTC()
	if err := store.SaveExecutionPlan(&storage.ExecutionPlan{ID: "plan-paused", ProjectID: "proj-a", Name: "Plan", RunMode: "parallel", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveExecutionPlan: %v", err)
	}
	pausedFrom := now.Add(-time.Hour)
	create := timeslotRequest(t, server, http.MethodPost, "/api/v1/execution-plans/plan-paused/schedules", map[string]any{
		"name":          "Paused",
		"cron_expr":     "0 9 * * *",
		"timezone":      "UTC",
		"disabled_from": pausedFrom,
	})
	if create.Code != http.StatusCreated {
		t.Fatalf("create paused schedule = %d body=%s", create.Code, create.Body.String())
	}
	var schedule storage.ExecutionPlanSchedule
	if err := json.Unmarshal(create.Body.Bytes(), &schedule); err != nil {
		t.Fatalf("decode schedule: %v", err)
	}
	if schedule.NextRunAt != nil || schedule.NextRequiredAction != "schedule_paused" {
		t.Fatalf("schedule = %+v, want nil next_run_at and schedule_paused", schedule)
	}
}

func TestExecutionPlanScheduleCRUD(t *testing.T) {
	server, store := newTimeslotServer(t)
	now := time.Now().UTC()
	if err := store.SaveExecutionPlan(&storage.ExecutionPlan{ID: "plan-crud-1", ProjectID: "proj-a", Name: "Plan", RunMode: "parallel", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveExecutionPlan: %v", err)
	}
	create := timeslotRequest(t, server, http.MethodPost, "/api/v1/execution-plans/plan-crud-1/schedules", map[string]any{"name": "Daily", "cron_expr": "0 9 * * *", "timezone": "UTC"})
	if create.Code != http.StatusCreated {
		t.Fatalf("create schedule = %d body=%s", create.Code, create.Body.String())
	}
	var created storage.ExecutionPlanSchedule
	_ = json.Unmarshal(create.Body.Bytes(), &created)
	if created.ID == "" || created.PlanID != "plan-crud-1" {
		t.Fatalf("created schedule = %+v", created)
	}
	list := timeslotRequest(t, server, http.MethodGet, "/api/v1/execution-plans/plan-crud-1/schedules", nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list = %d body=%s", list.Code, list.Body.String())
	}
	del := timeslotRequest(t, server, http.MethodDelete, "/api/v1/execution-plans/plan-crud-1/schedules/"+created.ID, nil)
	if del.Code != http.StatusOK {
		t.Fatalf("delete = %d body=%s", del.Code, del.Body.String())
	}
	// Creating a schedule for an unknown plan is a 404.
	missing := timeslotRequest(t, server, http.MethodPost, "/api/v1/execution-plans/nope/schedules", map[string]any{"name": "X", "cron_expr": "0 9 * * *"})
	if missing.Code != http.StatusNotFound {
		t.Fatalf("create on missing plan = %d, want 404", missing.Code)
	}
}

func TestExecutionPlanCRUD(t *testing.T) {
	server, _ := newTimeslotServer(t)

	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{"missing name", map[string]any{"project_id": "p", "entries": []any{}}},
		{"bad run_mode", map[string]any{"project_id": "p", "name": "x", "run_mode": "weird"}},
		{"entry missing scenario", map[string]any{"project_id": "p", "name": "x", "entries": []map[string]any{{"order": 0}}}},
		{"invalid runtime overrides", map[string]any{"project_id": "p", "name": "x", "entries": []map[string]any{{"order": 0, "scenario_id": "scn-1", "runtime_overrides": []any{"invalid"}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := timeslotRequest(t, server, http.MethodPost, "/api/v1/execution-plans", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 body=%s", rec.Code, rec.Body.String())
			}
		})
	}

	create := timeslotRequest(t, server, http.MethodPost, "/api/v1/execution-plans", map[string]any{
		"project_id": "proj-a", "name": "nightly", "run_mode": "sequential",
		"entries": []map[string]any{{"order": 0, "scenario_id": "scn-1"}},
	})
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var plan storage.ExecutionPlan
	_ = json.Unmarshal(create.Body.Bytes(), &plan)
	if plan.ID == "" || plan.Status != "draft" || plan.RunMode != "sequential" {
		t.Fatalf("created plan = %+v", plan)
	}

	list := timeslotRequest(t, server, http.MethodGet, "/api/v1/execution-plans?project_id=proj-a", nil)
	var listResp struct {
		ExecutionPlans []storage.ExecutionPlan `json:"execution_plans"`
		Count          int                     `json:"count"`
	}
	_ = json.Unmarshal(list.Body.Bytes(), &listResp)
	if listResp.Count != 1 {
		t.Fatalf("list count = %d, want 1", listResp.Count)
	}

	upd := timeslotRequest(t, server, http.MethodPut, "/api/v1/execution-plans/"+plan.ID, map[string]any{
		"name": "nightly-v2", "run_mode": "parallel", "revision": plan.Revision,
		"entries": []map[string]any{{"order": 0, "scenario_id": "scn-1"}, {"order": 1, "scenario_id": "scn-2"}},
	})
	if upd.Code != http.StatusOK {
		t.Fatalf("update status = %d body=%s", upd.Code, upd.Body.String())
	}
	var updated storage.ExecutionPlan
	_ = json.Unmarshal(upd.Body.Bytes(), &updated)
	if updated.RunMode != "parallel" || len(updated.Entries) != 2 {
		t.Fatalf("update not applied: %+v", updated)
	}

	del := timeslotRequest(t, server, http.MethodDelete, "/api/v1/execution-plans/"+plan.ID, nil)
	if del.Code != http.StatusOK {
		t.Fatalf("delete status = %d", del.Code)
	}
	if rec := timeslotRequest(t, server, http.MethodGet, "/api/v1/execution-plans/"+plan.ID, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("get after delete = %d, want 404", rec.Code)
	}
}

// TestExecutionPlanMaterializeOrchestration exercises the run orchestration:
// disabled entries are skipped, and a sequential plan halts after a failing
// entry (scenarios are absent, so test creation fails — that's the failure path
// the orchestration must report honestly).
func TestExecutionPlanMaterializeOrchestration(t *testing.T) {
	server, _ := newTimeslotServer(t)

	mkPlan := func(runMode string, entries []map[string]any) string {
		rec := timeslotRequest(t, server, http.MethodPost, "/api/v1/execution-plans", map[string]any{
			"project_id": "proj-a", "name": "plan", "run_mode": runMode, "entries": entries,
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("create plan status = %d body=%s", rec.Code, rec.Body.String())
		}
		var p storage.ExecutionPlan
		_ = json.Unmarshal(rec.Body.Bytes(), &p)
		return p.ID
	}
	run := func(id string) map[string]any {
		rec := timeslotRequest(t, server, http.MethodPost, "/api/v1/execution-plans/"+id+"/runs", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("run status = %d body=%s", rec.Code, rec.Body.String())
		}
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}
	statuses := func(out map[string]any) []string {
		raw, _ := out["results"].([]any)
		got := make([]string, 0, len(raw))
		for _, r := range raw {
			m, _ := r.(map[string]any)
			got = append(got, m["status"].(string))
		}
		return got
	}

	// Sequential: first entry fails (no scenario) → second is halted/skipped.
	seq := run(mkPlan("sequential", []map[string]any{
		{"order": 0, "scenario_id": "missing-1"},
		{"order": 1, "scenario_id": "missing-2"},
	}))
	if got := statuses(seq); len(got) != 2 || got[0] != "failed" || got[1] != "skipped" {
		t.Fatalf("sequential statuses = %v, want [failed skipped]", got)
	}

	// Parallel: both entries attempted (both fail, none halted).
	par := run(mkPlan("parallel", []map[string]any{
		{"order": 0, "scenario_id": "missing-1"},
		{"order": 1, "scenario_id": "missing-2"},
	}))
	if got := statuses(par); len(got) != 2 || got[0] != "failed" || got[1] != "failed" {
		t.Fatalf("parallel statuses = %v, want [failed failed]", got)
	}

	// Disabled entries are skipped regardless of mode.
	dis := run(mkPlan("parallel", []map[string]any{
		{"order": 0, "scenario_id": "missing-1", "enabled": false},
	}))
	if got := statuses(dis); len(got) != 1 || got[0] != "skipped" {
		t.Fatalf("disabled statuses = %v, want [skipped]", got)
	}
}

// TestExecutionPlanRunHistory verifies a sequential materialize persists a
// queryable plan-run (here it halts immediately because the scenarios are
// absent) that the GET runs / run endpoints expose.
func TestExecutionPlanRunHistory(t *testing.T) {
	server, _ := newTimeslotServer(t)

	create := timeslotRequest(t, server, http.MethodPost, "/api/v1/execution-plans", map[string]any{
		"project_id": "proj-a", "name": "history", "run_mode": "sequential",
		"entries": []map[string]any{
			{"order": 0, "scenario_id": "missing-1"},
			{"order": 1, "scenario_id": "missing-2"},
		},
	})
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var plan storage.ExecutionPlan
	_ = json.Unmarshal(create.Body.Bytes(), &plan)

	runRec := timeslotRequest(t, server, http.MethodPost, "/api/v1/execution-plans/"+plan.ID+"/runs", nil)
	if runRec.Code != http.StatusOK {
		t.Fatalf("run status = %d body=%s", runRec.Code, runRec.Body.String())
	}
	var runResp struct {
		RunID  string `json:"run_id"`
		Status string `json:"status"`
	}
	_ = json.Unmarshal(runRec.Body.Bytes(), &runResp)
	if runResp.RunID == "" {
		t.Fatalf("sequential run response missing run_id: %s", runRec.Body.String())
	}
	if runResp.Status != "halted" {
		t.Fatalf("run status = %q, want halted", runResp.Status)
	}

	list := timeslotRequest(t, server, http.MethodGet, "/api/v1/execution-plans/"+plan.ID+"/runs", nil)
	var listResp struct {
		Runs  []storage.ExecutionPlanRun `json:"runs"`
		Count int                        `json:"count"`
	}
	_ = json.Unmarshal(list.Body.Bytes(), &listResp)
	if listResp.Count != 1 || len(listResp.Runs) != 1 || listResp.Runs[0].ID != runResp.RunID {
		t.Fatalf("run list = %+v, want one run %q", listResp, runResp.RunID)
	}

	get := timeslotRequest(t, server, http.MethodGet, "/api/v1/execution-plans/"+plan.ID+"/runs/"+runResp.RunID, nil)
	if get.Code != http.StatusOK {
		t.Fatalf("get run status = %d body=%s", get.Code, get.Body.String())
	}
	var got storage.ExecutionPlanRun
	_ = json.Unmarshal(get.Body.Bytes(), &got)
	if got.Status != "halted" || len(got.Entries) != 2 || got.Entries[1].Status != "skipped" {
		t.Fatalf("run detail = %+v", got)
	}

	// A run id that does not belong to the plan in the path is not found.
	miss := timeslotRequest(t, server, http.MethodGet, "/api/v1/execution-plans/other-plan/runs/"+runResp.RunID, nil)
	if miss.Code != http.StatusNotFound {
		t.Fatalf("cross-plan get = %d, want 404", miss.Code)
	}
}

// TestExecutionPlanParallelRunHistory verifies a parallel materialize now
// persists a queryable plan-run record (run_id + per-entry statuses) just like
// the sequential path, so parallel run history survives a reload.
func TestExecutionPlanParallelRunHistory(t *testing.T) {
	server, _ := newTimeslotServer(t)

	create := timeslotRequest(t, server, http.MethodPost, "/api/v1/execution-plans", map[string]any{
		"project_id": "proj-a", "name": "parallel-history", "run_mode": "parallel",
		"entries": []map[string]any{
			{"order": 0, "scenario_id": "missing-1"},
			{"order": 1, "scenario_id": "missing-2", "enabled": false},
		},
	})
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var plan storage.ExecutionPlan
	_ = json.Unmarshal(create.Body.Bytes(), &plan)

	runRec := timeslotRequest(t, server, http.MethodPost, "/api/v1/execution-plans/"+plan.ID+"/runs", nil)
	if runRec.Code != http.StatusOK {
		t.Fatalf("run status = %d body=%s", runRec.Code, runRec.Body.String())
	}
	var runResp struct {
		RunID   string `json:"run_id"`
		RunMode string `json:"run_mode"`
		Status  string `json:"status"`
	}
	_ = json.Unmarshal(runRec.Body.Bytes(), &runResp)
	if runResp.RunID == "" || runResp.RunMode != "parallel" {
		t.Fatalf("parallel run response missing run_id/run_mode: %s", runRec.Body.String())
	}

	list := timeslotRequest(t, server, http.MethodGet, "/api/v1/execution-plans/"+plan.ID+"/runs", nil)
	var listResp struct {
		Runs  []storage.ExecutionPlanRun `json:"runs"`
		Count int                        `json:"count"`
	}
	_ = json.Unmarshal(list.Body.Bytes(), &listResp)
	if listResp.Count != 1 || len(listResp.Runs) != 1 || listResp.Runs[0].ID != runResp.RunID {
		t.Fatalf("parallel run list = %+v, want one run %q", listResp, runResp.RunID)
	}
	got := listResp.Runs[0]
	if got.RunMode != "parallel" || len(got.Entries) != 2 {
		t.Fatalf("parallel run = %+v, want parallel with 2 entries", got)
	}
	byOrder := map[int]storage.ExecutionPlanRunEntry{}
	for _, e := range got.Entries {
		byOrder[e.Order] = e
	}
	// Entry 0 fails (missing scenario); entry 1 is disabled -> skipped.
	if byOrder[0].Status != "failed" || byOrder[1].Status != "skipped" {
		t.Fatalf("parallel entry statuses = %+v, want order0 failed / order1 skipped", got.Entries)
	}
}

func TestExecutionPlanEntryDataPoolValidation(t *testing.T) {
	server, _ := newTimeslotServer(t)
	for _, tc := range []struct {
		name string
		pool map[string]any
	}{
		{"missing pool_id", map[string]any{"alias": "users"}},
		{"missing alias", map[string]any{"pool_id": "pool-1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := timeslotRequest(t, server, http.MethodPost, "/api/v1/execution-plans", map[string]any{
				"project_id": "proj-a", "name": "dp", "run_mode": "parallel",
				"entries": []map[string]any{{"order": 0, "scenario_id": "scn-1", "data_pools": []map[string]any{tc.pool}}},
			})
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestExecutionPlanRouteAccessRequirements(t *testing.T) {
	for _, tc := range []struct {
		method    string
		path      string
		wantRoles []string
	}{
		{http.MethodPost, "/api/v1/execution-plans", []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}},
		{http.MethodGet, "/api/v1/execution-plans", []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}},
		{http.MethodGet, "/api/v1/execution-plans/p1", []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}},
		{http.MethodPut, "/api/v1/execution-plans/p1", []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}},
		{http.MethodDelete, "/api/v1/execution-plans/p1", []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}},
		{http.MethodPost, "/api/v1/execution-plans/p1/runs", []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}},
		{http.MethodGet, "/api/v1/execution-plans/p1/runs", []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}},
		{http.MethodGet, "/api/v1/execution-plans/p1/runs/r1", []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			requirement, ok := accessRequirementForRequest(req)
			if !ok {
				t.Fatalf("no access requirement for %s %s", tc.method, tc.path)
			}
			if len(requirement.GlobalRoles) != len(tc.wantRoles) {
				t.Fatalf("GlobalRoles = %#v, want %#v", requirement.GlobalRoles, tc.wantRoles)
			}
		})
	}
}

package storage

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestDeleteTerminalScheduleOccurrencesBeforeIsBoundedAndProtectsLiveRows(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "retention.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-90 * 24 * time.Hour)
	if err := store.SaveProject(&Project{ID: "retention-project", Name: "Retention", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	script := &dsl.Script{ID: "retention-script", Name: "Retention", Protocol: "http", ProjectID: "retention-project"}
	if err := store.SaveScript(script); err != nil {
		t.Fatal(err)
	}
	scenario := &Scenario{ID: "retention-scenario", Name: "Retention", Duration: "1m", CreatedAt: now, UpdatedAt: now}
	if err := store.SaveScenario(scenario, []ScenarioScript{{ScriptID: script.ID, VUsers: 1}}); err != nil {
		t.Fatal(err)
	}
	currentDue := cutoff.Add(-time.Minute)
	scenarioSchedule := &ScenarioSchedule{
		ID: "retention-scenario-schedule", ScenarioID: scenario.ID, ProjectID: "retention-project",
		Name: "Retention", CronExpr: "0 * * * *", Enabled: true, NextRunAt: &currentDue,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveScenarioSchedule(scenarioSchedule); err != nil {
		t.Fatal(err)
	}

	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 1002; index++ {
		plannedAt := cutoff.Add(-time.Duration(index+2) * time.Minute)
		if _, err := tx.Exec(`INSERT INTO scenario_schedule_occurrences
			(id, schedule_id, scenario_id, project_id, planned_at, status, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			fmt.Sprintf("old-terminal-%04d", index), scenarioSchedule.ID, scenario.ID, "retention-project",
			plannedAt, "materialized", plannedAt, plannedAt); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
	}
	seedScenarioOccurrence := func(id, status string, plannedAt time.Time) {
		t.Helper()
		if _, err := tx.Exec(`INSERT INTO scenario_schedule_occurrences
			(id, schedule_id, scenario_id, project_id, planned_at, status, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			id, scenarioSchedule.ID, scenario.ID, "retention-project", plannedAt, status, plannedAt, plannedAt); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
	}
	seedScenarioOccurrence("old-active", "materializing", cutoff.Add(-30*time.Second))
	seedScenarioOccurrence("recent-terminal", "failed", cutoff.Add(time.Minute))
	seedScenarioOccurrence("current-due-terminal", "blocked", currentDue)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	pruned, err := store.DeleteTerminalScenarioScheduleOccurrencesBefore(cutoff, 5000)
	if err != nil || pruned != 1000 {
		t.Fatalf("first scenario prune = %d, %v; want hard-capped 1000", pruned, err)
	}
	pruned, err = store.DeleteTerminalScenarioScheduleOccurrencesBefore(cutoff, 1000)
	if err != nil || pruned != 2 {
		t.Fatalf("second scenario prune = %d, %v; want remaining 2", pruned, err)
	}
	for _, id := range []string{"old-active", "recent-terminal", "current-due-terminal"} {
		occurrence, err := store.GetScenarioScheduleOccurrence(id)
		if err != nil || occurrence == nil {
			t.Fatalf("protected scenario occurrence %q = %#v, %v", id, occurrence, err)
		}
	}

	plan := &ExecutionPlan{ID: "retention-plan", ProjectID: "retention-project", Name: "Retention", RunMode: "sequential", CreatedAt: now, UpdatedAt: now}
	if err := store.SaveExecutionPlan(plan); err != nil {
		t.Fatal(err)
	}
	planCurrentDue := cutoff.Add(-2 * time.Minute)
	planSchedule := &ExecutionPlanSchedule{
		ID: "retention-plan-schedule", PlanID: plan.ID, ProjectID: "retention-project", Name: "Retention",
		CronExpr: "0 * * * *", Enabled: true, NextRunAt: &planCurrentDue, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveExecutionPlanSchedule(planSchedule); err != nil {
		t.Fatal(err)
	}
	for _, occurrence := range []struct {
		id        string
		status    string
		plannedAt time.Time
	}{
		{id: "plan-old-terminal", status: "skipped", plannedAt: cutoff.Add(-time.Hour)},
		{id: "plan-old-active", status: "materializing", plannedAt: cutoff.Add(-30 * time.Minute)},
		{id: "plan-recent-terminal", status: "failed", plannedAt: cutoff.Add(time.Minute)},
		{id: "plan-current-due-terminal", status: "blocked", plannedAt: planCurrentDue},
	} {
		if err := store.SaveExecutionPlanScheduleOccurrence(&ExecutionPlanScheduleOccurrence{
			ID: occurrence.id, ScheduleID: planSchedule.ID, PlanID: plan.ID, ProjectID: "retention-project",
			PlannedAt: occurrence.plannedAt, Status: occurrence.status, CreatedAt: occurrence.plannedAt, UpdatedAt: occurrence.plannedAt,
		}); err != nil {
			t.Fatal(err)
		}
	}
	pruned, err = store.DeleteTerminalExecutionPlanScheduleOccurrencesBefore(cutoff, 1000)
	if err != nil || pruned != 1 {
		t.Fatalf("execution-plan prune = %d, %v; want 1", pruned, err)
	}
	remaining, err := store.ListExecutionPlanScheduleOccurrences(planSchedule.ID)
	if err != nil || len(remaining) != 3 {
		t.Fatalf("remaining plan occurrences = %d, %v; want 3", len(remaining), err)
	}
}

func TestDeleteTerminalScheduleOccurrencesBeforeRejectsUnboundedRequest(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "retention.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if got, err := store.DeleteTerminalScenarioScheduleOccurrencesBefore(time.Now(), 0); err != nil || got != 0 {
		t.Fatalf("scenario zero-limit prune = %d, %v", got, err)
	}
	if got, err := store.DeleteTerminalExecutionPlanScheduleOccurrencesBefore(time.Now(), -1); err != nil || got != 0 {
		t.Fatalf("execution-plan negative-limit prune = %d, %v", got, err)
	}
}

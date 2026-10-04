package api

import (
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestRestoredScheduleOccurrencesSkipWithoutObservation(t *testing.T) {
	for _, kind := range []string{"script", "scenario"} {
		t.Run(kind, func(t *testing.T) {
			server, store := newAuditTestServer(t)
			defer store.Close()
			script, err := server.runner.StoreScriptDurable(&dsl.Script{ID: "restored-schedule-source", Name: "Source", Protocol: "http", Version: "1", Config: dsl.ScriptConfig{BaseURL: "https://example.test"}, Actions: []dsl.Action{{Name: "Get", Type: "request", Method: "GET", Target: "/"}}})
			if err != nil {
				t.Fatal(err)
			}
			scenario, err := server.runner.StoreScenario(CreateScenarioRequest{Name: "Restored", Duration: "1m", Groups: []CreateGroupEntry{{ScriptID: script.ID, VUsers: 1}}})
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			plannedAt := now.Truncate(time.Minute).Add(-2 * time.Minute)
			schedule := &storage.ScenarioSchedule{ID: "unobserved-archive", ScenarioID: scenario.ID, ProjectID: scenario.ProjectID, Revision: 1, Name: "Catch up", CronExpr: "* * * * *", Timezone: "UTC", Enabled: true, MissedRunPolicy: "catch_up_all", NextRunAt: &plannedAt, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)}
			server.runner.mu.Lock()
			err = server.runner.saveScenarioScheduleLocked(schedule)
			server.runner.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			id, version := script.ID, script.Script.UpdatedAt
			if kind == "scenario" {
				id, version = scenario.ID, scenario.UpdatedAt
			}
			if _, err := store.SetObjectArchived(kind, id, version, true); err != nil {
				t.Fatal(err)
			}
			if kind == "script" {
				source, err := store.GetScript(id)
				if err != nil {
					t.Fatal(err)
				}
				version = source.UpdatedAt
			} else {
				source, _, err := store.GetScenario(id)
				if err != nil {
					t.Fatal(err)
				}
				version = source.UpdatedAt
			}
			if _, err := store.SetObjectArchived(kind, id, version, false); err != nil {
				t.Fatal(err)
			}
			// No sweep ran during the archive interval; restart drops local state.
			runner := NewTestRunner(1, NewHub(), store)
			scheduler := newScenarioScheduler(runner)
			scheduler.now = func() time.Time { return now }
			if err := scheduler.RunDueSchedules(); err != nil {
				t.Fatal(err)
			}
			occurrence, err := store.GetScenarioScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, plannedAt)
			if err != nil || occurrence == nil || occurrence.Status != "skipped" || occurrence.ReasonCode != "object_archived" || !strings.Contains(occurrence.ConflictSummary, "restoration") {
				t.Fatalf("restoration replayed missed occurrence: %#v %v", occurrence, err)
			}
			future, err := store.GetScenarioSchedule(schedule.ID)
			if err != nil || future.NextRunAt == nil || !future.NextRunAt.After(now) {
				t.Fatalf("restoration did not advance to future: %#v %v", future, err)
			}
		})
	}
}

func TestRestoredExecutionPlanScheduleSkipsMissedOccurrences(t *testing.T) {
	store, plan, schedule, now := newExecutionPlanScheduleRecoveryFixture(t, "parallel")
	runner := NewTestRunner(1, NewHub(), store)
	if err := store.SaveProject(&storage.Project{ID: plan.ProjectID, Name: "Plan project", DomainID: "default"}); err != nil {
		t.Fatal(err)
	}
	script, err := runner.StoreScriptDurable(&dsl.Script{ProjectID: plan.ProjectID, ID: "restored-plan-script", Name: "Source", Protocol: "http", Version: "1", Config: dsl.ScriptConfig{BaseURL: "https://example.test"}, Actions: []dsl.Action{{Name: "Get", Type: "request", Method: "GET", Target: "/"}}})
	if err != nil {
		t.Fatal(err)
	}
	scenario, err := runner.StoreScenario(CreateScenarioRequest{Name: "Scenario", Duration: "1m", Groups: []CreateGroupEntry{{ScriptID: script.ID, VUsers: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	plan.Entries = []storage.ExecutionPlanEntry{{ScenarioID: scenario.ID}}
	if err := store.SaveExecutionPlan(plan); err != nil {
		t.Fatal(err)
	}
	version := script.Script.UpdatedAt
	if _, err := store.SetObjectArchived("script", script.ID, version, true); err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetScript(script.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetObjectArchived("script", script.ID, stored.UpdatedAt, false); err != nil {
		t.Fatal(err)
	}
	plannedAt := schedule.NextRunAt.UTC()
	if err := runner.runDueExecutionPlanScheduleOnce(schedule, now); err != nil {
		t.Fatal(err)
	}
	occurrence, err := store.GetExecutionPlanScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, plannedAt)
	if err != nil || occurrence == nil {
		t.Fatalf("occurrence %#v %v", occurrence, err)
	}
	if occurrence.Status != "skipped" || occurrence.ReasonCode != "object_archived" || !strings.Contains(occurrence.ConflictSummary, "restoration") {
		t.Fatalf("restoration replayed plan occurrence: %#v", occurrence)
	}
}

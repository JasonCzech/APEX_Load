package api

import (
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestGetScenarioReturnsDeepSnapshot(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	now := time.Now().UTC()
	nextRun := now.Add(time.Hour)
	materializedAt := now.Add(-time.Minute)
	iterations := 3
	targetTPS := 25.0
	goalTPS := 20.0
	runner.mu.Lock()
	runner.scenarios["scenario-snapshot"] = &StoredScenario{
		ID: "scenario-snapshot", ProjectID: storage.DefaultProjectID, Name: "Snapshot", Duration: "1m",
		Assignment:      &GeneratorAssignmentSettings{SelectedTargets: []string{"generator-a"}, RegionWeights: map[string]int{"east": 1}},
		GoalConfig:      &GoalConfig{TargetAverageTPS: &goalTPS},
		RuntimeSettings: &RuntimeSettingsOverride{Env: map[string]string{"BASE_URL": "https://before.example"}},
		Scripts: []ScenarioScriptRef{{
			ScriptID: "script-a", Iterations: &iterations,
			Stages:          []storage.LoadStage{{Type: "steady", Duration: "1m", TargetTPS: &targetTPS}},
			RuntimeSettings: &RuntimeSettingsOverride{Env: map[string]string{"TOKEN": "before"}},
			Assignment:      &GeneratorAssignmentSettings{RequiredLabels: map[string]string{"zone": "a"}},
		}},
		Schedules: []*storage.ScenarioSchedule{{
			ID: "schedule-a", ScenarioID: "scenario-snapshot", Name: "Daily", NextRunAt: &nextRun,
			Governance:      &storage.ScenarioScheduleGovernance{RBACReview: storage.ScenarioScheduleRBACReview{RequiredRoles: []string{"editor"}}},
			BlackoutWindows: []storage.ScenarioScheduleWindow{{DaysOfWeek: []int{1, 2}}},
			LastOccurrence:  &storage.ScenarioScheduleOccurrence{ID: "occurrence-a", MaterializedAt: &materializedAt},
		}},
		DataPools: []storage.ScenarioDataPoolBinding{{PoolID: "pool-a", Alias: "customers"}},
	}
	runner.mu.Unlock()

	snapshot, ok := runner.GetScenario("scenario-snapshot")
	if !ok {
		t.Fatal("scenario snapshot not found")
	}
	snapshot.Assignment.SelectedTargets[0] = "generator-b"
	snapshot.Assignment.RegionWeights["east"] = 9
	*snapshot.GoalConfig.TargetAverageTPS = 999
	snapshot.RuntimeSettings.Env["BASE_URL"] = "https://after.example"
	*snapshot.Scripts[0].Iterations = 99
	*snapshot.Scripts[0].Stages[0].TargetTPS = 999
	snapshot.Scripts[0].RuntimeSettings.Env["TOKEN"] = "after"
	snapshot.Scripts[0].Assignment.RequiredLabels["zone"] = "b"
	changedNext := now.Add(24 * time.Hour)
	*snapshot.Schedules[0].NextRunAt = changedNext
	snapshot.Schedules[0].Governance.RBACReview.RequiredRoles[0] = "viewer"
	snapshot.Schedules[0].BlackoutWindows[0].DaysOfWeek[0] = 7
	*snapshot.Schedules[0].LastOccurrence.MaterializedAt = changedNext
	snapshot.DataPools[0].Alias = "tampered"

	again, _ := runner.GetScenario("scenario-snapshot")
	if again.Assignment.SelectedTargets[0] != "generator-a" || again.Assignment.RegionWeights["east"] != 1 || *again.GoalConfig.TargetAverageTPS != goalTPS {
		t.Fatalf("top-level scenario pointers alias runner state: %+v", again)
	}
	if again.RuntimeSettings.Env["BASE_URL"] != "https://before.example" || *again.Scripts[0].Iterations != iterations || *again.Scripts[0].Stages[0].TargetTPS != targetTPS {
		t.Fatalf("script/runtime snapshot aliases runner state: %+v", again.Scripts[0])
	}
	if again.Scripts[0].RuntimeSettings.Env["TOKEN"] != "before" || again.Scripts[0].Assignment.RequiredLabels["zone"] != "a" {
		t.Fatalf("nested script settings alias runner state: %+v", again.Scripts[0])
	}
	if !again.Schedules[0].NextRunAt.Equal(nextRun) || again.Schedules[0].Governance.RBACReview.RequiredRoles[0] != "editor" || again.Schedules[0].BlackoutWindows[0].DaysOfWeek[0] != 1 || !again.Schedules[0].LastOccurrence.MaterializedAt.Equal(materializedAt) {
		t.Fatalf("schedule snapshot aliases runner state: %+v", again.Schedules[0])
	}
	if again.DataPools[0].Alias != "customers" {
		t.Fatalf("data-pool snapshot aliases runner state: %+v", again.DataPools)
	}
}

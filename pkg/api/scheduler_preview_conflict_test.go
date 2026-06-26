package api

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// The schedule preview surfaces the same standing quota/capacity conflict the
// materialization sweep would record, so an operator can fix it before saving.
func TestPreviewScenarioScheduleRunsReportsConflict(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(2, hub, nil)
	runner.StoreScript(&dsl.Script{
		ID:       "script-preview-conflict",
		Name:     "Preview Conflict Script",
		Protocol: "http",
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://preview.example.test"},
		Actions:  []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})

	// One configured generator that can hold at most 100 VUsers.
	settings := runner.GetSystemSettings()
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 1
	settings.CapacityPlanning.Generator.MaxVUsers = 100
	if _, err := runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}

	heavy, err := runner.StoreScenario(CreateScenarioRequest{
		Name:     "Heavy Scenario",
		Duration: "5m",
		Groups:   []CreateGroupEntry{{Name: "load", ScriptID: "script-preview-conflict", VUsers: 5000}},
	})
	if err != nil {
		t.Fatalf("StoreScenario(heavy) returned error: %v", err)
	}
	light, err := runner.StoreScenario(CreateScenarioRequest{
		Name:     "Light Scenario",
		Duration: "5m",
		Groups:   []CreateGroupEntry{{Name: "load", ScriptID: "script-preview-conflict", VUsers: 10}},
	})
	if err != nil {
		t.Fatalf("StoreScenario(light) returned error: %v", err)
	}

	req := CreateScenarioScheduleRequest{Name: "Hourly", CronExpr: "0 * * * *"}

	heavyPreview, err := runner.PreviewScenarioScheduleRuns(heavy.ID, req, 3)
	if err != nil {
		t.Fatalf("PreviewScenarioScheduleRuns(heavy) returned error: %v", err)
	}
	if len(heavyPreview.Occurrences) == 0 {
		t.Fatal("expected upcoming occurrences for an hourly schedule")
	}
	if heavyPreview.ConflictSummary == "" {
		t.Fatal("expected a capacity conflict summary for an over-capacity scenario")
	}

	lightPreview, err := runner.PreviewScenarioScheduleRuns(light.ID, req, 3)
	if err != nil {
		t.Fatalf("PreviewScenarioScheduleRuns(light) returned error: %v", err)
	}
	if lightPreview.ConflictSummary != "" {
		t.Fatalf("right-sized scenario should preview cleanly, got conflict: %q", lightPreview.ConflictSummary)
	}
}

package storage

import "testing"

func TestScenarioTelemetryPolicyIsAuthoredContent(t *testing.T) {
	exerciseScenarioTelemetryPolicy(t, newScenarioRevisionTestStore(t))
}

// exerciseScenarioTelemetryPolicy: a scenario's chosen telemetry acceptance
// policy is stored with it, read back by every reader, and is authored
// content: a change to it alone mints a new content revision.
func exerciseScenarioTelemetryPolicy(t *testing.T, store *SQLiteStore) {
	old := seedScenarioRevision(t, store)
	candidate := *old.Scenario
	candidate.TelemetryAcceptancePolicyID = "strict"
	result, err := store.UpdateScenarioAuthoringIfCurrent(&candidate, old.Scripts, old.DataPools, scenarioAuthoringExpected(old))
	if err != nil || !result.Applied {
		t.Fatalf("save: %#v %v", result, err)
	}
	current := requireScenarioAuthoring(t, store, old.Scenario.ID)
	if current.ContentRevision == old.ContentRevision || current.Scenario.TelemetryAcceptancePolicyID != "strict" {
		t.Fatalf("authoring snapshot: revision %q (was %q), policy %q", current.ContentRevision, old.ContentRevision, current.Scenario.TelemetryAcceptancePolicyID)
	}
	read, _, err := store.GetScenario(old.Scenario.ID)
	if err != nil || read.TelemetryAcceptancePolicyID != "strict" {
		t.Fatalf("GetScenario: %#v %v", read, err)
	}
	listed, err := store.ListScenarios()
	if err != nil || len(listed) == 0 || listed[0].TelemetryAcceptancePolicyID != "strict" {
		t.Fatalf("ListScenarios: %v", err)
	}
	// A direct write of the column alone is an authored change too.
	if _, err := store.db.Exec(`UPDATE scenarios SET telemetry_acceptance_policy_id=? WHERE id=?`, "lenient", old.Scenario.ID); err != nil {
		t.Fatal(err)
	}
	bumped := requireScenarioAuthoring(t, store, old.Scenario.ID)
	if bumped.ContentRevision == current.ContentRevision {
		t.Fatal("changing only the telemetry acceptance policy kept the content revision")
	}
	// Clearing the choice goes back to the project's default.
	cleared := *bumped.Scenario
	cleared.TelemetryAcceptancePolicyID = ""
	if result, err := store.UpdateScenarioAuthoringIfCurrent(&cleared, bumped.Scripts, bumped.DataPools, scenarioAuthoringExpected(bumped)); err != nil || !result.Applied {
		t.Fatalf("clear: %#v %v", result, err)
	}
	if read, _, err := store.GetScenario(old.Scenario.ID); err != nil || read.TelemetryAcceptancePolicyID != "" {
		t.Fatalf("cleared GetScenario: %#v %v", read, err)
	}
}

func TestScenarioTelemetryBudgetPolicyIsAuthoredContent(t *testing.T) {
	exerciseScenarioTelemetryBudgetPolicy(t, newScenarioRevisionTestStore(t))
}

// exerciseScenarioTelemetryBudgetPolicy: a scenario's chosen telemetry budget
// policy is stored beside its acceptance policy, read back by every reader,
// and is authored content: a change to it alone mints a new content revision.
func exerciseScenarioTelemetryBudgetPolicy(t *testing.T, store *SQLiteStore) {
	old := seedScenarioRevision(t, store)
	candidate := *old.Scenario
	candidate.TelemetryAcceptancePolicyID = "strict"
	candidate.TelemetryBudgetPolicyID = "tight"
	result, err := store.UpdateScenarioAuthoringIfCurrent(&candidate, old.Scripts, old.DataPools, scenarioAuthoringExpected(old))
	if err != nil || !result.Applied {
		t.Fatalf("save: %#v %v", result, err)
	}
	current := requireScenarioAuthoring(t, store, old.Scenario.ID)
	if current.ContentRevision == old.ContentRevision || current.Scenario.TelemetryBudgetPolicyID != "tight" || current.Scenario.TelemetryAcceptancePolicyID != "strict" {
		t.Fatalf("authoring snapshot: revision %q (was %q), budget %q, acceptance %q", current.ContentRevision, old.ContentRevision, current.Scenario.TelemetryBudgetPolicyID, current.Scenario.TelemetryAcceptancePolicyID)
	}
	read, _, err := store.GetScenario(old.Scenario.ID)
	if err != nil || read.TelemetryBudgetPolicyID != "tight" || read.TelemetryAcceptancePolicyID != "strict" {
		t.Fatalf("GetScenario: %#v %v", read, err)
	}
	listed, err := store.ListScenarios()
	if err != nil || len(listed) == 0 || listed[0].TelemetryBudgetPolicyID != "tight" {
		t.Fatalf("ListScenarios: %v", err)
	}
	// A direct write of the column alone is an authored change too.
	if _, err := store.db.Exec(`UPDATE scenarios SET telemetry_budget_policy_id=? WHERE id=?`, "loose", old.Scenario.ID); err != nil {
		t.Fatal(err)
	}
	bumped := requireScenarioAuthoring(t, store, old.Scenario.ID)
	if bumped.ContentRevision == current.ContentRevision || bumped.Scenario.TelemetryBudgetPolicyID != "loose" {
		t.Fatalf("changing only the telemetry budget policy kept the content revision (budget %q)", bumped.Scenario.TelemetryBudgetPolicyID)
	}
	// Clearing the choice goes back to the system threshold settings and
	// keeps the acceptance choice.
	cleared := *bumped.Scenario
	cleared.TelemetryBudgetPolicyID = ""
	if result, err := store.UpdateScenarioAuthoringIfCurrent(&cleared, bumped.Scripts, bumped.DataPools, scenarioAuthoringExpected(bumped)); err != nil || !result.Applied {
		t.Fatalf("clear: %#v %v", result, err)
	}
	if read, _, err := store.GetScenario(old.Scenario.ID); err != nil || read.TelemetryBudgetPolicyID != "" || read.TelemetryAcceptancePolicyID != "strict" {
		t.Fatalf("cleared GetScenario: %#v %v", read, err)
	}
	// The plain save path stores and clears it too.
	plain := *read
	plain.TelemetryBudgetPolicyID = "tight"
	if err := store.SaveScenario(&plain, old.Scripts); err != nil {
		t.Fatal(err)
	}
	if read, _, err := store.GetScenario(old.Scenario.ID); err != nil || read.TelemetryBudgetPolicyID != "tight" {
		t.Fatalf("SaveScenario: %#v %v", read, err)
	}
}

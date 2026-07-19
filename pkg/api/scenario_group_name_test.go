package api

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func newGroupNameTestRunner(t *testing.T) *TestRunner {
	t.Helper()
	runner := NewTestRunner(1, NewHub(), nil)
	stored := runner.StoreScript(&dsl.Script{
		ID:       "script-group-names",
		Name:     "Checkout",
		Protocol: "http",
		Version:  "1.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions:  []dsl.Action{{Name: "Home", Type: "request", Method: "GET", Target: "/"}},
	})
	if stored == nil {
		t.Fatal("StoreScript returned nil")
	}
	return runner
}

func duplicateNamedGroups() []CreateGroupEntry {
	return []CreateGroupEntry{
		{Name: "  Buyers   East ", ScriptID: "script-group-names", VUsers: 2},
		{Name: "buyers east", ScriptID: "script-group-names", VUsers: 3},
	}
}

func TestCreateTestRejectsDuplicateNormalizedEffectiveGroupNames(t *testing.T) {
	runner := newGroupNameTestRunner(t)
	_, err := runner.CreateTest(CreateTestRequest{Name: "Duplicate groups", Duration: "1m", Groups: duplicateNamedGroups()})
	if err == nil || !strings.Contains(err.Error(), "effective group name") {
		t.Fatalf("CreateTest error = %v, want duplicate effective group name", err)
	}
}

func TestStoreScenarioRejectsDuplicateNormalizedEffectiveGroupNames(t *testing.T) {
	runner := newGroupNameTestRunner(t)
	_, err := runner.StoreScenario(CreateScenarioRequest{Name: "Duplicate groups", Duration: "1m", Groups: duplicateNamedGroups()})
	if err == nil || !strings.Contains(err.Error(), "effective group name") {
		t.Fatalf("StoreScenario error = %v, want duplicate effective group name", err)
	}
}

func TestUpdateScenarioRejectsDuplicateGroupNamesWithoutReplacingStoredAggregate(t *testing.T) {
	runner := newGroupNameTestRunner(t)
	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		Name: "Distinct groups", Duration: "1m",
		Groups: []CreateGroupEntry{
			{Name: "Buyers", ScriptID: "script-group-names", VUsers: 2},
			{Name: "Sellers", ScriptID: "script-group-names", VUsers: 3},
		},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	_, ok, err := runner.UpdateScenario(scenario.ID, CreateScenarioRequest{Name: scenario.Name, Duration: scenario.Duration, Groups: duplicateNamedGroups()})
	if !ok || err == nil || !strings.Contains(err.Error(), "effective group name") {
		t.Fatalf("UpdateScenario = ok:%v err:%v, want found duplicate rejection", ok, err)
	}
	stored, found := runner.GetScenario(scenario.ID)
	if !found || len(stored.Scripts) != 2 || stored.Scripts[0].Name != "Buyers" || stored.Scripts[1].Name != "Sellers" {
		t.Fatalf("stored scenario changed after rejected update: %#v", stored)
	}
}

func TestCompilationRejectsExplicitNameCollidingWithGeneratedEffectiveName(t *testing.T) {
	runner := newGroupNameTestRunner(t)
	_, err := runner.CreateTest(CreateTestRequest{
		Name: "Generated collision", Duration: "1m",
		Groups: []CreateGroupEntry{
			{Name: "Checkout (#2)", ScriptID: "script-group-names", VUsers: 2},
			{ScriptID: "script-group-names", VUsers: 3},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "effective group name") {
		t.Fatalf("CreateTest error = %v, want generated-name collision", err)
	}
}

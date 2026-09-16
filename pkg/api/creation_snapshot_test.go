package api

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestScenarioCreateAndUpdateReturnDeepSnapshots(t *testing.T) {
	runner := NewTestRunner(2, NewHub(), nil)
	runner.StoreScript(&dsl.Script{
		ID: "snapshot-script", ProjectID: "snapshot-project", Name: "Snapshot", Protocol: "http", Version: "1",
		Config: dsl.ScriptConfig{BaseURL: "https://example.test"}, Actions: []dsl.Action{{Name: "request", Type: "request", Method: "GET", Target: "/"}},
	})
	request := CreateScenarioRequest{
		Name: "Before", Duration: "1m",
		RuntimeSettings: &RuntimeSettingsOverride{Env: map[string]string{"ROOT": "before"}},
		Groups: []CreateGroupEntry{{
			ScriptID: "snapshot-script", VUsers: 1,
			RuntimeSettings: &RuntimeSettingsOverride{Env: map[string]string{"GROUP": "before"}},
			Assignment:      &GeneratorAssignmentSettings{RequiredLabels: map[string]string{"zone": "a"}},
		}},
	}
	created, err := runner.StoreScenario(request)
	if err != nil {
		t.Fatal(err)
	}
	created.Name = "tampered"
	created.RuntimeSettings.Env["ROOT"] = "tampered"
	created.Scripts[0].RuntimeSettings.Env["GROUP"] = "tampered"
	created.Scripts[0].Assignment.RequiredLabels["zone"] = "tampered"
	stored, _ := runner.GetScenario(created.ID)
	if stored.Name != "Before" || stored.RuntimeSettings.Env["ROOT"] != "before" || stored.Scripts[0].RuntimeSettings.Env["GROUP"] != "before" || stored.Scripts[0].Assignment.RequiredLabels["zone"] != "a" {
		t.Fatalf("create response aliases canonical scenario: %#v", stored)
	}

	request.Name = "After"
	updated, ok, err := runner.UpdateScenario(created.ID, request)
	if err != nil || !ok {
		t.Fatalf("UpdateScenario = %v, %v", ok, err)
	}
	updated.Name = "tampered update"
	updated.RuntimeSettings.Env["ROOT"] = "tampered update"
	again, _ := runner.GetScenario(created.ID)
	if again.Name != "After" || again.RuntimeSettings.Env["ROOT"] != "before" {
		t.Fatalf("update response aliases canonical scenario: %#v", again)
	}
}

func TestCreateTestReturnsDeepSnapshot(t *testing.T) {
	runner := NewTestRunner(2, NewHub(), nil)
	runner.StoreScript(&dsl.Script{
		ID: "test-snapshot-script", ProjectID: "snapshot-project", Name: "Snapshot", Protocol: "http", Version: "1",
		Config: dsl.ScriptConfig{BaseURL: "https://example.test"}, Actions: []dsl.Action{{Name: "request", Type: "request", Method: "GET", Target: "/"}},
	})
	created, err := runner.CreateTest(CreateTestRequest{
		Name: "Snapshot run", ProjectID: "snapshot-project", Duration: "1m",
		Assignment: &GeneratorAssignmentSettings{RequiredLabels: map[string]string{"zone": "a"}},
		Groups: []CreateGroupEntry{{
			ScriptID: "test-snapshot-script", VUsers: 1,
			RuntimeSettings: &RuntimeSettingsOverride{Env: map[string]string{"REGION": "before"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	created.Name = "tampered"
	created.Assignment.RequiredLabels["zone"] = "tampered"
	created.Groups[0].RuntimeSettings.Env["REGION"] = "tampered"
	stored, ok := runner.GetTest(created.ID)
	if !ok {
		t.Fatal("created test missing")
	}
	if stored.Name != "Snapshot run" || stored.Assignment.RequiredLabels["zone"] != "a" || stored.Groups[0].RuntimeSettings.Env["REGION"] != "before" {
		t.Fatalf("create response aliases canonical test: %#v", stored)
	}
}

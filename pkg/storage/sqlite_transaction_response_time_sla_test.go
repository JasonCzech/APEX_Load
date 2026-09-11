package storage

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestScenarioGroupTransactionResponseTimeSLARoundTrip(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	script := &dsl.Script{ID: "sla-script", Name: "SLA script", Protocol: "http", ProjectID: DefaultProjectID}
	if err := store.SaveScript(script); err != nil {
		t.Fatal(err)
	}
	threshold := 1250.5
	scenario := &Scenario{ID: "sla-scenario", Name: "SLA scenario", Duration: "1m"}
	if err := store.SaveScenario(scenario, []ScenarioScript{{ScenarioID: scenario.ID, ScriptID: script.ID, VUsers: 1, TransactionResponseTimeSLAMs: &threshold}}); err != nil {
		t.Fatal(err)
	}
	_, groups, err := store.GetScenario(scenario.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0].TransactionResponseTimeSLAMs == nil || *groups[0].TransactionResponseTimeSLAMs != threshold {
		t.Fatalf("stored groups = %#v", groups)
	}
}

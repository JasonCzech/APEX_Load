package storage

import (
	"path/filepath"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestScenarioConditionalCreateUpdateDelete(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "scenarios.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.SaveScript(&dsl.Script{ID: "script", Name: "Script", Protocol: "http", ProjectID: DefaultProjectID}); err != nil {
		t.Fatal(err)
	}
	scenario := &Scenario{ID: "scenario", RefKey: "DEV-SCN-1", Name: "initial", Duration: "1m"}
	scripts := []ScenarioScript{{ScenarioID: scenario.ID, ScriptID: "script", VUsers: 1}}
	created, err := store.CreateScenarioWithDataPoolBindingsIfAbsent(scenario, scripts, nil)
	if err != nil || !created {
		t.Fatalf("conditional create = %t, %v", created, err)
	}
	duplicate := &Scenario{ID: scenario.ID, RefKey: "other", Name: "overwrite", Duration: "2m"}
	created, err = store.CreateScenarioWithDataPoolBindingsIfAbsent(duplicate, scripts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("duplicate conditional create overwrote the scenario")
	}
	priorUpdatedAt := scenario.UpdatedAt
	updatedScenario := *scenario
	updatedScenario.Name = "updated"
	updated, err := store.UpdateScenarioWithDataPoolBindingsIfUnchanged(&updatedScenario, scripts, nil, priorUpdatedAt)
	if err != nil || !updated {
		t.Fatalf("conditional update = %t, %v", updated, err)
	}
	stale := *scenario
	stale.Name = "stale"
	updated, err = store.UpdateScenarioWithDataPoolBindingsIfUnchanged(&stale, scripts, nil, priorUpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if updated {
		t.Fatal("stale conditional update succeeded")
	}
	if deleted, err := store.DeleteScenarioIfUnchanged(scenario.ID, priorUpdatedAt); err != nil || deleted {
		t.Fatalf("stale conditional delete = %t, %v", deleted, err)
	}
	if deleted, err := store.DeleteScenarioIfUnchanged(scenario.ID, updatedScenario.UpdatedAt); err != nil || !deleted {
		t.Fatalf("current conditional delete = %t, %v", deleted, err)
	}
	if _, _, err := store.GetScenario(scenario.ID); err == nil {
		t.Fatal("conditionally deleted scenario still exists")
	}
}

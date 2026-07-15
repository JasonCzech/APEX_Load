package storage

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestSQLiteScenarioAggregateCannotCrossProjectBoundaries(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	for _, projectID := range []string{"scenario-project-a", "scenario-project-b"} {
		if err := store.SaveProject(&Project{ID: projectID, Name: projectID, CreatedAt: now}); err != nil {
			t.Fatalf("SaveProject(%s): %v", projectID, err)
		}
	}
	for _, script := range []*dsl.Script{
		{ID: "scenario-script-a", Name: "A", Protocol: "http", ProjectID: "scenario-project-a", CreatedAt: now},
		{ID: "scenario-script-b", Name: "B", Protocol: "http", ProjectID: "scenario-project-b", CreatedAt: now},
	} {
		if err := store.SaveScript(script); err != nil {
			t.Fatalf("SaveScript(%s): %v", script.ID, err)
		}
	}
	for _, pool := range []*SharedDataPool{
		{ID: "scenario-pool-a", Name: "A", ProjectID: "scenario-project-a", CreatedAt: now},
		{ID: "scenario-pool-a-2", Name: "A2", ProjectID: "scenario-project-a", CreatedAt: now},
		{ID: "scenario-pool-b", Name: "B", ProjectID: "scenario-project-b", CreatedAt: now},
	} {
		if err := store.SaveDataPool(pool); err != nil {
			t.Fatalf("SaveDataPool(%s): %v", pool.ID, err)
		}
	}

	scenario := &Scenario{ID: "scenario-owned-a", Name: "Owned by A", Duration: "1m", CreatedAt: now}
	scripts := []ScenarioScript{{ScenarioID: scenario.ID, ScriptID: "scenario-script-a"}}
	bindings := []ScenarioDataPoolBinding{{ID: "scenario-binding-a", PoolID: "scenario-pool-a", Alias: "users", AccessMode: "read_only", SelectionStrategy: "sequential"}}
	if err := store.SaveScenarioWithDataPoolBindings(scenario, scripts, bindings); err != nil {
		t.Fatalf("SaveScenarioWithDataPoolBindings: %v", err)
	}

	forged := *scenario
	forged.Name = "Forged"
	if err := store.SaveScenario(&forged, []ScenarioScript{{ScenarioID: forged.ID, ScriptID: "scenario-script-b"}}); err == nil {
		t.Fatal("SaveScenario allowed project ownership rewrite")
	}
	got, gotScripts, err := store.GetScenario(scenario.ID)
	if err != nil {
		t.Fatalf("GetScenario: %v", err)
	}
	if got.Name != scenario.Name || len(gotScripts) != 1 || gotScripts[0].ScriptID != "scenario-script-a" {
		t.Fatalf("rejected rewrite changed scenario: %#v scripts=%#v", got, gotScripts)
	}

	if err := store.SaveScenarioDataPoolBindings(scenario.ID, []ScenarioDataPoolBinding{{ID: "scenario-binding-b", PoolID: "scenario-pool-b", Alias: "users", AccessMode: "read_only", SelectionStrategy: "sequential"}}); err == nil {
		t.Fatal("SaveScenarioDataPoolBindings allowed a foreign-project pool")
	}
	gotBindings, err := store.ListScenarioDataPoolBindings(scenario.ID)
	if err != nil {
		t.Fatalf("ListScenarioDataPoolBindings: %v", err)
	}
	if len(gotBindings) != 1 || gotBindings[0].PoolID != "scenario-pool-a" {
		t.Fatalf("rejected binding rewrite changed durable bindings: %#v", gotBindings)
	}

	wrongParent := *scenario
	if err := store.SaveScenario(&wrongParent, []ScenarioScript{{ScenarioID: "another-scenario", ScriptID: "scenario-script-a"}}); err == nil {
		t.Fatal("SaveScenario accepted a child row owned by another scenario")
	}
}

func TestSQLiteScenarioDataPoolBindingCASAdvancesScenarioVersion(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	now := time.Now().UTC()
	projectID := "scenario-binding-cas-project"
	if err := store.SaveProject(&Project{ID: projectID, Name: projectID, CreatedAt: now}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	if err := store.SaveScript(&dsl.Script{ID: "scenario-binding-cas-script", Name: "Script", Protocol: "http", ProjectID: projectID, CreatedAt: now}); err != nil {
		t.Fatalf("SaveScript: %v", err)
	}
	for _, poolID := range []string{"scenario-binding-cas-pool-1", "scenario-binding-cas-pool-2"} {
		if err := store.SaveDataPool(&SharedDataPool{ID: poolID, Name: poolID, ProjectID: projectID, CreatedAt: now}); err != nil {
			t.Fatalf("SaveDataPool(%s): %v", poolID, err)
		}
	}
	scenario := &Scenario{ID: "scenario-binding-cas", Name: "CAS", Duration: "1m", CreatedAt: now}
	if err := store.SaveScenarioWithDataPoolBindings(scenario,
		[]ScenarioScript{{ScenarioID: scenario.ID, ScriptID: "scenario-binding-cas-script"}},
		[]ScenarioDataPoolBinding{{ID: "binding-cas-1", PoolID: "scenario-binding-cas-pool-1", Alias: "users", AccessMode: "read_only", SelectionStrategy: "sequential"}},
	); err != nil {
		t.Fatalf("SaveScenarioWithDataPoolBindings: %v", err)
	}
	loaded, _, err := store.GetScenario(scenario.ID)
	if err != nil {
		t.Fatalf("GetScenario: %v", err)
	}
	expected := loaded.UpdatedAt
	second := []ScenarioDataPoolBinding{{ID: "binding-cas-2", PoolID: "scenario-binding-cas-pool-2", Alias: "users", AccessMode: "read_only", SelectionStrategy: "sequential"}}
	updatedAt, updated, err := store.UpdateScenarioDataPoolBindingsIfUnchanged(scenario.ID, second, expected)
	if err != nil || !updated {
		t.Fatalf("first conditional update = (%s, %v, %v), want success", updatedAt, updated, err)
	}
	if !updatedAt.After(expected) {
		t.Fatalf("updated_at = %s, want after %s", updatedAt, expected)
	}
	if _, updated, err := store.UpdateScenarioDataPoolBindingsIfUnchanged(scenario.ID,
		[]ScenarioDataPoolBinding{{ID: "binding-stale", PoolID: "scenario-binding-cas-pool-1", Alias: "users", AccessMode: "read_only", SelectionStrategy: "sequential"}}, expected); err != nil || updated {
		t.Fatalf("stale conditional update = (%v, %v), want false, nil", updated, err)
	}
	got, _, err := store.GetScenario(scenario.ID)
	if err != nil {
		t.Fatalf("GetScenario after update: %v", err)
	}
	if !got.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("durable updated_at = %s, want %s", got.UpdatedAt, updatedAt)
	}
	gotBindings, err := store.ListScenarioDataPoolBindings(scenario.ID)
	if err != nil {
		t.Fatalf("ListScenarioDataPoolBindings: %v", err)
	}
	if len(gotBindings) != 1 || gotBindings[0].PoolID != "scenario-binding-cas-pool-2" {
		t.Fatalf("stale update changed winning bindings: %#v", gotBindings)
	}
}

func TestSQLiteDataPoolRowsPageIsBounded(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	pool := &SharedDataPool{ID: "bounded-rows-pool", Name: "Bounded rows", ProjectID: DefaultProjectID}
	if err := store.SaveDataPool(pool); err != nil {
		t.Fatalf("SaveDataPool: %v", err)
	}
	base := time.Now().UTC().Add(-time.Hour)
	for i, id := range []string{"bounded-row-1", "bounded-row-2", "bounded-row-3"} {
		if err := store.SaveDataPoolRow(&SharedDataPoolRow{ID: id, PoolID: pool.ID, Data: map[string]string{"id": id}, CreatedAt: base.Add(time.Duration(i) * time.Minute)}); err != nil {
			t.Fatalf("SaveDataPoolRow(%s): %v", id, err)
		}
	}
	page, err := store.ListDataPoolRowsPage(pool.ID, 1, 1)
	if err != nil {
		t.Fatalf("ListDataPoolRowsPage: %v", err)
	}
	if len(page) != 1 || page[0].ID != "bounded-row-2" {
		t.Fatalf("page = %#v, want bounded-row-2 only", page)
	}
}

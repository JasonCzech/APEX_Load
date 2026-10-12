package api

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestPlanEntryDataPoolOverridesKeepTheScenarioLiveTables(t *testing.T) {
	scenario := &StoredScenario{ID: "s1", DataPools: []storage.ScenarioDataPoolBinding{
		{Alias: "work", AccessMode: "live", SelectionStrategy: "manual", LiveOptions: &storage.LiveTableOptions{Profile: "native_queue"}},
		{Alias: "Customers", PoolID: "pool-customers", AccessMode: "read_only", SelectionStrategy: "sequential"},
	}}
	entry := storage.ExecutionPlanEntry{ScenarioID: "s1", DataPools: []storage.ScenarioDataPoolBinding{
		{Alias: "customers", PoolID: "pool-vip", AccessMode: "read_only", SelectionStrategy: "unique"},
		{Alias: "regions", PoolID: "pool-regions", AccessMode: "read_only", SelectionStrategy: "sequential"},
	}}
	req, err := buildPlanEntryCreateRequest("Nightly", "proj-a", nil, entry)
	if err != nil {
		t.Fatalf("buildPlanEntryCreateRequest: %v", err)
	}
	got := createTestRequestFromScenarioRevision(req, scenario, nil).DataPools
	if len(got) != 3 {
		t.Fatalf("bindings = %+v, want the scenario's two with one overridden, plus the new alias", got)
	}
	if got[0].Alias != "work" || got[0].AccessMode != "live" || got[0].LiveOptions == nil {
		t.Fatalf("the scenario's live table was not kept: %+v", got[0])
	}
	if got[1].PoolID != "pool-vip" || got[1].SelectionStrategy != "unique" {
		t.Fatalf("the override did not replace the scenario's binding for its alias: %+v", got[1])
	}
	if got[2].Alias != "regions" {
		t.Fatalf("an override for a new alias was not added: %+v", got[2])
	}
	// The scenario's own list is untouched.
	if scenario.DataPools[1].PoolID != "pool-customers" {
		t.Fatalf("the scenario's bindings were modified: %+v", scenario.DataPools)
	}

	// A run created directly with data pools still names its whole list.
	direct := createTestRequestFromScenarioRevision(CreateTestRequest{ScenarioID: "s1", DataPools: entry.DataPools}, scenario, nil).DataPools
	if len(direct) != 2 || direct[0].Alias != "customers" {
		t.Fatalf("a direct request's data pools = %+v, want exactly its own", direct)
	}
}

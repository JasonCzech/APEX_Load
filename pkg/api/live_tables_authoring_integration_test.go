package api

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestLiveTableLoadModelValidationKeepsSnapshotChecksSeparate(t *testing.T) {
	server, scriptID, snapshotID := seedDataPoolContractServer(t)
	now := time.Now().UTC()
	if err := server.store.SaveDataPool(&storage.SharedDataPool{ID: "empty-live-seed", Name: "Empty seed", ProjectID: storage.DefaultProjectID, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		binding storage.ScenarioDataPoolBinding
	}{
		{"empty", storage.ScenarioDataPoolBinding{Alias: "work", AccessMode: "live", SelectionStrategy: "lease", LiveOptions: &storage.LiveTableOptions{SeedPolicy: "empty", Columns: []string{"id"}}}},
		{"original", storage.ScenarioDataPoolBinding{Alias: "work", AccessMode: "live", LiveOptions: &storage.LiveTableOptions{SeedPolicy: "original_seed", SeedRunID: "prior-run"}}},
		{"original with stale pool witness", storage.ScenarioDataPoolBinding{PoolID: "removed-pool", Alias: "work", AccessMode: "live", LiveOptions: &storage.LiveTableOptions{SeedPolicy: "original_seed", SeedRunID: "prior-run"}}},
		{"current empty pool", storage.ScenarioDataPoolBinding{PoolID: "empty-live-seed", Alias: "work", AccessMode: "live", SelectionStrategy: "pop", LiveOptions: &storage.LiveTableOptions{Columns: []string{"id"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report, err := server.runner.ValidateScenarioLoadModel(CreateScenarioRequest{
				Name: "Live and snapshot", Duration: "1m", Groups: []CreateGroupEntry{{ScriptID: scriptID, VUsers: 3}},
				DataPools: []storage.ScenarioDataPoolBinding{tc.binding, {PoolID: snapshotID, Alias: "customers", SelectionStrategy: "unique"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			liveReady, snapshotBlocked := false, false
			for _, item := range report.Items {
				if item.Dimension != "data_pool_rows" {
					continue
				}
				if item.Group == "work" {
					if item.Status != "ready" || item.RequiredRows != 0 || item.AvailableRows != 0 {
						t.Fatalf("live binding received snapshot row-demand findings: %+v", item)
					}
					liveReady = true
				}
				if item.Status == "blocked" && strings.Contains(item.Summary, "unique") && item.RequiredRows == 3 && item.AvailableRows == 1 {
					snapshotBlocked = true
				}
			}
			if !liveReady || !snapshotBlocked {
				t.Fatalf("missing live readiness or existing snapshot underfill: %+v", report.Items)
			}
		})
	}
	// An empty reusable snapshot remains invalid; only explicit live bindings
	// can receive rows from producers after a run starts.
	_, items, _ := validateLoadModelDataPoolBindings(server.store, []storage.ScenarioDataPoolBinding{{PoolID: "empty-live-seed", Alias: "static", AccessMode: "read_only"}})
	if len(items) != 1 || items[0].Status != "blocked" || !strings.Contains(items[0].Summary, "no rows") {
		t.Fatalf("empty snapshot lost its existing validation: %+v", items)
	}
}

func TestLiveTableExecutionPlanOverridesValidateAndConvertWithoutSeedPool(t *testing.T) {
	server, store, _, scenario := newScenarioPolicyAvailabilityFixture(t)
	service := newTestRunnerExecutionPlanService(server.runner, store)
	for _, policy := range []string{"empty", "original_seed"} {
		t.Run(policy, func(t *testing.T) {
			options := &storage.LiveTableOptions{SeedPolicy: policy, Columns: []string{"id"}}
			if policy == "original_seed" {
				options.SeedRunID = "prior-run"
			}
			plan := &storage.ExecutionPlan{Name: "Live plan", ProjectID: storage.DefaultProjectID, Entries: []storage.ExecutionPlanEntry{{ScenarioID: scenario.ID, DataPools: []storage.ScenarioDataPoolBinding{{Alias: "work", AccessMode: "live", LiveOptions: options}}}}}
			if status, message := service.Validate(plan); status != 0 {
				t.Fatalf("valid live override rejected: %d %s", status, message)
			}
			req, err := buildPlanEntryCreateRequest(plan.Name, plan.ProjectID, nil, plan.Entries[0])
			if err != nil || len(req.DataPools) != 1 || req.DataPools[0].AccessMode != "live" || req.DataPools[0].LiveOptions.SeedPolicy != policy || req.DataPools[0].SelectionStrategy != "manual" {
				t.Fatalf("live override changed during plan conversion: %+v, %v", req.DataPools, err)
			}
		})
	}
	for _, mode := range []string{"read_only", "live"} {
		plan := &storage.ExecutionPlan{Name: "Missing seed", ProjectID: storage.DefaultProjectID, Entries: []storage.ExecutionPlanEntry{{ScenarioID: scenario.ID, DataPools: []storage.ScenarioDataPoolBinding{{PoolID: "missing", Alias: "work", AccessMode: mode}}}}}
		if status, _ := service.Validate(plan); status != http.StatusBadRequest {
			t.Fatalf("%s current pool dependency was not checked: %d", mode, status)
		}
	}
}

func TestLiveTableScenarioAuthoringProjectionIncludesOptionOnlyChanges(t *testing.T) {
	server, store, _, scenario := newScenarioPolicyAvailabilityFixture(t)
	binding := storage.ScenarioDataPoolBinding{Alias: "work", AccessMode: "live", LiveOptions: &storage.LiveTableOptions{SeedPolicy: "empty", Columns: []string{"id"}}}
	save := func() {
		t.Helper()
		response := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+scenario.ID+"/data-pools", map[string]any{"data_pools": []storage.ScenarioDataPoolBinding{binding}}, "")
		if response.Code != http.StatusOK {
			t.Fatalf("save live binding: %d %s", response.Code, response.Body.String())
		}
	}
	save()
	first, firstTag, _ := requireScenarioAuthoringView(t, server, scenario.ID)
	if len(first.Scenario.DataPools) != 1 || first.Scenario.DataPools[0].LiveOptions == nil || first.Scenario.DataPools[0].LiveOptions.SeedPolicy != "empty" {
		t.Fatalf("canonical authoring view omitted live options: %+v", first.Scenario.DataPools)
	}
	binding.ID = first.Scenario.DataPools[0].ID
	binding.LiveOptions.EndMode, binding.LiveOptions.OutputPoolName = "save_as_new", "Retained work"
	save()
	next, nextTag, _ := requireScenarioAuthoringView(t, server, scenario.ID)
	if firstTag == nextTag || reflect.DeepEqual(first.Scenario, next.Scenario) || next.Scenario.DataPools[0].LiveOptions.EndMode != "save_as_new" {
		t.Fatal("option-only change must alter both canonical document and revision")
	}
	snapshot, err := store.GetScenarioAuthoringSnapshot(scenario.ID)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := buildScenarioAuthoringProjection(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	projection.Scenario.DataPools[0].LiveOptions.Columns[0] = "mutated"
	if snapshot.DataPools[0].LiveOptions.Columns[0] != "id" {
		t.Fatal("authoring projection aliases the durable live options")
	}
}

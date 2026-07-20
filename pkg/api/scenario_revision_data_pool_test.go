package api

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestCreateTestRequestFromScenarioPreservesRevisionDataPoolSnapshot(t *testing.T) {
	currentBinding := storage.ScenarioDataPoolBinding{
		ID:                "binding-current",
		ScenarioID:        "scenario-1",
		PoolID:            "pool-current",
		PoolName:          "Current customers",
		Alias:             "customers",
		AccessMode:        "read_only",
		SelectionStrategy: "random",
	}
	scenario := &StoredScenario{DataPools: []storage.ScenarioDataPoolBinding{currentBinding}}
	originalBinding := storage.ScenarioDataPoolBinding{
		ID:                "binding-original",
		ScenarioID:        "scenario-1",
		PoolID:            "pool-original",
		PoolName:          "Original customers",
		Alias:             "customers",
		AccessMode:        "consume",
		SelectionStrategy: "unique",
	}

	t.Run("explicit historical snapshot wins", func(t *testing.T) {
		got := createTestRequestFromScenario(CreateTestRequest{
			DataPools: []storage.ScenarioDataPoolBinding{originalBinding},
		}, scenario)
		if len(got.DataPools) != 1 || got.DataPools[0] != originalBinding {
			t.Fatalf("revision snapshot was replaced by current scenario bindings: %+v", got.DataPools)
		}
	})

	t.Run("explicit empty historical snapshot stays empty", func(t *testing.T) {
		got := createTestRequestFromScenario(CreateTestRequest{
			DataPools: []storage.ScenarioDataPoolBinding{},
		}, scenario)
		if got.DataPools == nil || len(got.DataPools) != 0 {
			t.Fatalf("explicit empty snapshot was refilled from the scenario: %+v", got.DataPools)
		}
	})

	t.Run("omitted bindings inherit the current scenario", func(t *testing.T) {
		got := createTestRequestFromScenario(CreateTestRequest{}, scenario)
		if len(got.DataPools) != 1 || got.DataPools[0] != currentBinding {
			t.Fatalf("omitted bindings did not inherit scenario bindings: %+v", got.DataPools)
		}
		got.DataPools[0].Alias = "mutated"
		if scenario.DataPools[0].Alias != "customers" {
			t.Fatalf("inherited request aliases the mutable scenario snapshot: %+v", scenario.DataPools)
		}
	})
}

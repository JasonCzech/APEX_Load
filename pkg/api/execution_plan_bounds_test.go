package api

import (
	"net/http"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestValidateExecutionPlanBoundsMaterializationFanout(t *testing.T) {
	server := &Server{}
	entries := make([]storage.ExecutionPlanEntry, maxExecutionPlanEntries+1)
	for index := range entries {
		entries[index].ScenarioID = "scenario"
	}
	status, message := server.validateExecutionPlan(&storage.ExecutionPlan{Name: "oversized", Entries: entries})
	if status != http.StatusBadRequest || message == "" {
		t.Fatalf("oversized plan validation = (%d, %q), want 400", status, message)
	}
}

func TestValidateExecutionPlanBoundsNestedEntryPayloads(t *testing.T) {
	server := &Server{}
	tests := []struct {
		name  string
		entry storage.ExecutionPlanEntry
	}{
		{
			name: "data pools",
			entry: storage.ExecutionPlanEntry{
				ScenarioID: "scenario",
				DataPools:  make([]storage.ScenarioDataPoolBinding, maxExecutionPlanDataPoolsPerEntry+1),
			},
		},
		{
			name: "runtime overrides",
			entry: storage.ExecutionPlanEntry{
				ScenarioID:       "scenario",
				RuntimeOverrides: make([]byte, maxExecutionPlanRuntimeOverrideBytes+1),
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, message := server.validateExecutionPlan(&storage.ExecutionPlan{Name: "oversized", Entries: []storage.ExecutionPlanEntry{test.entry}})
			if status != http.StatusBadRequest || message == "" {
				t.Fatalf("nested payload validation = (%d, %q), want 400", status, message)
			}
		})
	}
}

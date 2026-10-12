package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestLiveTableDiscardSurvivesMissingFinalizationAndRestart(t *testing.T) {
	for _, policy := range []string{"explicit", "default_options", "default_mode"} {
		t.Run(policy, func(t *testing.T) {
			runner, store, test := newLiveRuntimeFixture(t, "durable", "discard")
			ctx := context.Background()
			id := liveTableID(test.ID, "work")
			run := runner.liveRun(test.ID)
			rt := run.tables[id]
			// Simulate a crash after the closed discard checkpoint commits but
			// before the separate finalization result is recorded.
			if err := rt.table.Close(ctx, true); err != nil {
				t.Fatal(err)
			}
			if err := runner.checkpointLiveTable(ctx, run, rt, true); err != nil {
				t.Fatal(err)
			}
			if err := store.CloseLiveTable(ctx, id, rt.table.Stats().Revision, liveStorageTerm(run.term)); err != nil {
				t.Fatal(err)
			}
			ended := time.Now()
			if err := store.UpdateTestStatus(test.ID, "FAILED", &ended, "crash fixture"); err != nil {
				t.Fatal(err)
			}
			record, err := store.GetTest(test.ID)
			if err != nil {
				t.Fatal(err)
			}
			switch policy {
			case "default_options":
				record.DataPools[0].LiveOptions = nil
			case "default_mode":
				record.DataPools[0].LiveOptions.EndMode = ""
			}
			if err := store.SaveTest(record); err != nil {
				t.Fatal(err)
			}
			runner.liveTablesMu.Lock()
			delete(runner.liveTables, test.ID)
			runner.liveTablesMu.Unlock()
			runner.mu.Lock()
			delete(runner.tests, test.ID)
			runner.mu.Unlock()
			server := &Server{runner: runner, store: store}
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.SetPathValue("id", test.ID)
			response := httptest.NewRecorder()
			server.handleListLiveTables(response, req)
			var metadata struct {
				Tables []liveTableView `json:"tables"`
			}
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &metadata) != nil || len(metadata.Tables) != 1 {
				t.Fatalf("discarded metadata unavailable: %d %s", response.Code, response.Body.String())
			}
			final := metadata.Tables[0].Finalization
			if final == nil || final.Mode != "discard" || final.State != "complete" || final.OutputPoolID != "" || final.Reason != "discarded" {
				t.Fatalf("discarded output offered for recovery: %+v", final)
			}
			// Rejection precedes inspection/decryption and therefore also works
			// while the raw inspection slot is occupied or keys are unavailable.
			runner.liveTablesInspectMu.Lock()
			req = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"output_pool_name":"Must not recreate discarded output"}`))
			req.SetPathValue("id", test.ID)
			req.SetPathValue("tableID", id)
			response = httptest.NewRecorder()
			server.handleFinalizeLiveTable(response, req)
			runner.liveTablesInspectMu.Unlock()
			if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "discard policy") {
				t.Fatalf("discarded output saved: %d %s", response.Code, response.Body.String())
			}
			pools, err := store.ListDataPoolsForProject(false, test.ProjectID)
			if err != nil || len(pools) != 1 || pools[0].ID != "live-seed" {
				t.Fatalf("discard created another pool: %+v %v", pools, err)
			}
			liveIntegrationAssertRows(t, store, "live-seed", 3)
		})
	}
}

func TestLiveTableConfiguredEndModeDoesNotInventMissingPolicy(t *testing.T) {
	test := &ManagedTest{ID: "run", DataPools: []storage.ScenarioDataPoolBinding{{Alias: "work", AccessMode: "read_only"}}}
	if mode, known := liveTableConfiguredEndMode(test, liveTableID(test.ID, "work")); known || mode != "" {
		t.Fatalf("ordinary binding provided a live output policy: %q %v", mode, known)
	}
}

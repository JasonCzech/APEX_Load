package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/livetable"
	"github.com/jasonczech/apexload/pkg/storage"
)

// This is a valid live shape: 16,000 fields across sparse rows fit its limits.
// A reusable pool instead flattens those rows into a 128-column CSV, exceeding
// the existing two-million-cell execution limit even with tiny field values.
func liveSparseOutputFixture(t *testing.T, mode string) (*TestRunner, *storage.SQLiteStore, *ManagedTest) {
	t.Helper()
	runner, store, _ := newLiveRuntimeFixture(t, "memory", "discard")
	columns := make([]string, 128)
	columns[0] = "value"
	for i := 1; i < len(columns); i++ {
		columns[i] = fmt.Sprintf("column_%d", i)
	}
	options := &storage.LiveTableOptions{Columns: columns, EndMode: mode, MaxRows: 16000}
	if mode == "save_as_new" {
		options.OutputPoolName = "Sparse output"
	}
	test := liveIntegrationNewRun(t, runner, store, "sparse-output", []storage.ScenarioDataPoolBinding{{PoolID: "live-seed", Alias: "work", AccessMode: "live", LiveOptions: options}})
	if err := runner.prepareLiveTables(context.Background(), test); err != nil {
		t.Fatal(err)
	}
	rows := make([]livetable.Row, 16000)
	for i := range rows {
		rows[i] = livetable.Row{ID: fmt.Sprintf("produced-%d", i), Values: map[string]string{columns[i%len(columns)]: "x"}}
	}
	run := runner.liveRun(test.ID)
	run.mu.Lock()
	rt := run.tables[liveTableID(test.ID, "work")]
	// Install the state a producer can create without dispatching 16,000
	// operations in this API regression. The real core still validates it.
	table, err := livetable.New(livetable.Config{ID: rt.record.ID, RunID: test.ID, Profile: livetable.ProfileNative, Columns: columns, Rows: rows, Limits: liveCoreLimits(rt.binding.LiveOptions)})
	if err == nil {
		rt.table = table
	}
	run.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return runner, store, test
}

func assertLiveOutputDidNotChangePools(t *testing.T, store *storage.SQLiteStore, test *ManagedTest) {
	t.Helper()
	pools, err := store.ListDataPoolsForProject(false, test.ProjectID)
	if err != nil || len(pools) != 1 || pools[0].ID != "live-seed" {
		t.Fatalf("invalid output created a pool: %+v %v", pools, err)
	}
	rows, err := store.ListDataPoolRows("live-seed")
	if err != nil || len(rows) != 3 {
		t.Fatalf("invalid output replaced source rows: %+v %v", rows, err)
	}
	want := map[string]string{"row-one": "private-customer-one", "row-two": "private-customer-two", "inactive": "historical"}
	for _, row := range rows {
		if row.Data["value"] != want[row.ID] {
			t.Fatalf("source row changed: %+v", row)
		}
	}
	if result, err := store.GetLiveTableFinalization(context.Background(), liveTableID(test.ID, "work"), test.ProjectID); !errors.Is(err, sql.ErrNoRows) || result != nil {
		t.Fatalf("invalid output finalized: %+v %v", result, err)
	}
}

func TestLiveTableAutomaticOutputRejectsNonExecutableSparsePools(t *testing.T) {
	for _, mode := range []string{"write_back", "save_as_new"} {
		t.Run(mode, func(t *testing.T) {
			runner, store, test := liveSparseOutputFixture(t, mode)
			ended := time.Now()
			if err := store.UpdateTestStatus(test.ID, "COMPLETE", &ended, ""); err != nil {
				t.Fatal(err)
			}
			runner.mu.Lock()
			test.Status = "COMPLETE"
			runner.mu.Unlock()
			for attempt := 0; attempt < 2; attempt++ {
				if err := runner.finalizeLiveTables(context.Background(), test, "COMPLETE"); err != nil {
					t.Fatalf("nonretryable output validation blocked runtime release: %v", err)
				}
				assertLiveOutputDidNotChangePools(t, store, test)
			}
			summary := runner.liveTableArchiveSummaries(test.ID)
			if len(summary) != 1 || summary[0].State != "closed" || summary[0].Finalization == nil || summary[0].Finalization.State != "pending" || summary[0].Finalization.Reason != "output_limits" {
				t.Fatalf("output limits not exposed for the retained closed table: %+v", summary)
			}
			run := runner.liveRun(test.ID)
			runner.releaseLiveTableRuntime(run)
			if run.budget != 0 || run.tables[liveTableID(test.ID, "work")].table != nil {
				t.Fatal("nonretryable output retained runtime resources")
			}
			table, release, err := runner.inspectLiveTable(context.Background(), test, liveTableID(test.ID, "work"))
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			if table.Stats().Counts.Available != 16000 {
				t.Fatal("closed output could not be reopened for export")
			}
		})
	}
}

func TestLiveTableManualOutputRejectsNonExecutableSparsePool(t *testing.T) {
	runner, store, test := liveSparseOutputFixture(t, "write_back")
	// Failed runs retain their final rows for explicit operator recovery.
	if err := runner.finalizeLiveTables(context.Background(), test, "FAILED"); err != nil {
		t.Fatal(err)
	}
	ended := time.Now()
	if err := store.UpdateTestStatus(test.ID, "FAILED", &ended, "test fixture"); err != nil {
		t.Fatal(err)
	}
	runner.mu.Lock()
	test.Status = "FAILED"
	runner.mu.Unlock()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"output_pool_name":"Invalid sparse output"}`))
	req.SetPathValue("id", test.ID)
	req.SetPathValue("tableID", liveTableID(test.ID, "work"))
	response := httptest.NewRecorder()
	(&Server{runner: runner, store: store}).handleFinalizeLiveTable(response, req)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "executable data pool limits") {
		t.Fatalf("invalid manual output response: %d %s", response.Code, response.Body.String())
	}
	assertLiveOutputDidNotChangePools(t, store, test)
}

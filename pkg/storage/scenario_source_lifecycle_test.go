package storage

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// Capture the durable rows directly for deliberately corrupted owner fixtures.
// The qualified reader must refuse corruption, so it cannot be the oracle.
func rawScenarioSourceRows(t *testing.T, store *SQLiteStore, id string) string {
	t.Helper()
	all := [][][]any{}
	for _, q := range []string{`SELECT * FROM scenarios WHERE id=?`, `SELECT * FROM scenario_scripts WHERE scenario_id=? ORDER BY sort_order,script_id`, `SELECT * FROM scenario_data_pool_bindings WHERE scenario_id=? ORDER BY id`} {
		rows, err := store.db.Query(q, id)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		records := [][]any{}
		for rows.Next() {
			values := make([]any, len(columns))
			ptr := make([]any, len(columns))
			for i := range ptr {
				ptr[i] = &values[i]
			}
			if err := rows.Scan(ptr...); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			records = append(records, values)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, records)
	}
	raw, err := json.Marshal(all)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
func assertSourceMutationRawRefused(t *testing.T, store *SQLiteStore, old *ScenarioAuthoringSnapshot, admission ScenarioSourceAdmission) {
	t.Helper()
	before := rawScenarioSourceRows(t, store, old.Scenario.ID)
	for _, create := range []bool{true, false} {
		result, err := trySourceMutation(store, old, create, admission)
		if err == nil || result != nil {
			t.Fatalf("create=%v accepted %#v/%v", create, result, err)
		}
		if rawScenarioSourceRows(t, store, old.Scenario.ID) != before {
			t.Fatal("refused mutation changed durable rows")
		}
	}
	if _, err := store.GetScenarioAuthoringSnapshot("source-new"); !errors.Is(err, ErrScenarioAuthoringNotFound) {
		t.Fatal("failed create installed candidate", err)
	}
}

func exerciseScenarioSourceLifecycle(t *testing.T, store *SQLiteStore) {
	old := seedScenarioRevision(t, store)
	initial := requireScenarioSources(t, store, old.Scripts)
	id := old.Scripts[0].ScriptID
	source, err := store.GetScript(id)
	if err != nil {
		t.Fatal(err)
	}
	if applied, err := store.SetObjectArchived("script", id, source.UpdatedAt, true); err != nil || !applied {
		t.Fatal(applied, err)
	}
	if _, err := store.GetScenarioSourceAuthoringSnapshot(old.Scripts); err == nil {
		t.Fatal("initial archived source accepted")
	}
	assertSourceMutationRawRefused(t, store, old, initial.Admission)
	archived, err := store.GetScript(id)
	if err != nil {
		t.Fatal(err)
	}
	if applied, err := store.SetObjectArchived("script", id, archived.UpdatedAt, false); err != nil || !applied {
		t.Fatal(applied, err)
	}
	initial = requireScenarioSources(t, store, old.Scripts)
	if _, err := store.db.Exec(`UPDATE shared_data_pools SET archived_at=? WHERE id=?`, time.Now().UTC(), old.DataPools[0].PoolID); err != nil {
		t.Fatal(err)
	}
	assertSourceMutationRawRefused(t, store, old, initial.Admission)
	if _, err := store.db.Exec(`UPDATE shared_data_pools SET archived_at=NULL,project_id=? WHERE id=?`, "scn-foreign", old.DataPools[0].PoolID); err != nil {
		t.Fatal(err)
	}
	assertSourceMutationRawRefused(t, store, old, initial.Admission)
}
func TestScenarioSourceLifecycle(t *testing.T) {
	exerciseScenarioSourceLifecycle(t, newScenarioRevisionTestStore(t))
}

func exerciseScenarioSourceRecreation(t *testing.T, store *SQLiteStore) {
	old := seedScenarioRevision(t, store)
	// Use an unbound source so the supported delete need not violate a durable FK.
	// The requested group change still uses the current parent nonce for update.
	requested := *old
	requested.Scripts = []ScenarioScript{{ScriptID: "scn-script-two", Name: "Recreated source", VUsers: 1}}
	old = &requested
	initial := requireScenarioSources(t, store, old.Scripts)
	id := old.Scripts[0].ScriptID
	source, err := store.GetScriptAuthoringSnapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	expected := ScriptAuthoringPrecondition{ProjectID: source.Script.ProjectID, CreatedAt: source.Script.CreatedAt, ContentRevision: source.ContentRevision}
	if applied, err := store.DeleteScriptAuthoringIfCurrent(id, expected); err != nil || !applied {
		t.Fatal(applied, err)
	}
	assertSourceMutationRawRefused(t, store, old, initial.Admission)
	copied := *source.Script
	copied.CreatedAt = copied.CreatedAt.Add(time.Hour)
	if err := store.SaveScript(&copied); err != nil {
		t.Fatal(err)
	}
	assertSourceMutationRawRefused(t, store, old, initial.Admission)
}
func TestScenarioSourceRecreation(t *testing.T) {
	exerciseScenarioSourceRecreation(t, newScenarioRevisionTestStore(t))
}

package storage

import (
	"path/filepath"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func newRecordingTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestScriptRecordingRoundTrip(t *testing.T) {
	store := newRecordingTestStore(t)

	// Absent baseline returns (nil, nil).
	got, err := store.GetScriptRecording("script-1")
	if err != nil || got != nil {
		t.Fatalf("expected (nil,nil) for absent recording, got (%v, %v)", got, err)
	}

	record := storeRecording("script-1")
	if err := store.SaveScriptRecording(record); err != nil {
		t.Fatalf("SaveScriptRecording: %v", err)
	}
	if record.SchemaVersion != scriptRecordingSchemaVersion {
		t.Errorf("schema version not stamped, got %q", record.SchemaVersion)
	}
	if record.RequestCount != 2 {
		t.Errorf("request count = %d, want 2", record.RequestCount)
	}
	if record.CreatedAt.IsZero() || record.UpdatedAt.IsZero() {
		t.Error("created_at/updated_at not stamped")
	}

	loaded, err := store.GetScriptRecording("script-1")
	if err != nil {
		t.Fatalf("GetScriptRecording: %v", err)
	}
	if loaded == nil || len(loaded.Observations) != 2 {
		t.Fatalf("expected 2 observations, got %#v", loaded)
	}
	if loaded.Observations[0].ResponseBody != `{"token":"abc"}` {
		t.Errorf("response body not round-tripped: %q", loaded.Observations[0].ResponseBody)
	}
	if loaded.Observations[1].RequestHeaders["X-Token"] != "abc" {
		t.Errorf("request headers not round-tripped: %#v", loaded.Observations[1].RequestHeaders)
	}
	if len(loaded.CorrelationObservations) != 0 {
		t.Errorf("plaintext correlation observations were persisted: %#v", loaded.CorrelationObservations)
	}
	var persistedSidecar string
	if err := store.db.QueryRow(`SELECT correlation_observations_json FROM script_recordings WHERE script_id = ?`, "script-1").Scan(&persistedSidecar); err != nil {
		t.Fatal(err)
	}
	if persistedSidecar != "[]" {
		t.Fatalf("plaintext correlation sidecar = %q, want empty encrypted slot", persistedSidecar)
	}

	// Upsert replaces observations.
	record.Observations = record.Observations[:1]
	record.CorrelationObservations = []ScriptRecordingObservation{{ID: 0, ResponseBody: `{"token":"still-raw"}`}}
	if err := store.SaveScriptRecording(record); err != nil {
		t.Fatalf("re-save: %v", err)
	}
	loaded, _ = store.GetScriptRecording("script-1")
	if len(loaded.Observations) != 1 || len(loaded.CorrelationObservations) != 0 {
		t.Errorf("upsert did not replace observations, got obs=%d corr=%d", len(loaded.Observations), len(loaded.CorrelationObservations))
	}
}

func TestScriptRecordingCascadeOnDeleteScript(t *testing.T) {
	store := newRecordingTestStore(t)

	if err := store.SaveScript(&dsl.Script{ID: "script-1", Name: "demo", Protocol: "http"}); err != nil {
		t.Fatalf("SaveScript: %v", err)
	}
	if err := store.SaveScriptRecording(storeRecording("script-1")); err != nil {
		t.Fatalf("SaveScriptRecording: %v", err)
	}

	if err := store.DeleteScript("script-1"); err != nil {
		t.Fatalf("DeleteScript: %v", err)
	}
	got, err := store.GetScriptRecording("script-1")
	if err != nil {
		t.Fatalf("GetScriptRecording after delete: %v", err)
	}
	if got != nil {
		t.Errorf("recording should be cascade-deleted with its script, got %#v", got)
	}
}

func TestScriptRecordingCapsObservations(t *testing.T) {
	store := newRecordingTestStore(t)
	record := &ScriptRecordingRecord{ScriptID: "big", Source: "har"}
	for i := 0; i < maxRecordingObservations+50; i++ {
		record.Observations = append(record.Observations, storage_obs(i))
		record.CorrelationObservations = append(record.CorrelationObservations, storage_obs(i))
	}
	if err := store.SaveScriptRecording(record); err != nil {
		t.Fatalf("SaveScriptRecording: %v", err)
	}
	if record.RequestCount != maxRecordingObservations {
		t.Errorf("request count = %d, want cap %d", record.RequestCount, maxRecordingObservations)
	}
	if len(record.Observations) != maxRecordingObservations || len(record.CorrelationObservations) != 0 {
		t.Errorf("observation caps = obs %d corr %d, want %d", len(record.Observations), len(record.CorrelationObservations), maxRecordingObservations)
	}
}

// storeRecording builds a small two-observation baseline for tests.
func storeRecording(scriptID string) *ScriptRecordingRecord {
	return &ScriptRecordingRecord{
		ScriptID: scriptID,
		Source:   "browser_recording",
		Observations: []ScriptRecordingObservation{
			{ID: 0, RequestURL: "/login", ResponseBody: `{"token":"abc"}`, ResponseHeaders: map[string]string{"Content-Type": "application/json"}},
			{ID: 1, RequestURL: "/orders", RequestHeaders: map[string]string{"X-Token": "abc"}},
		},
		CorrelationObservations: []ScriptRecordingObservation{
			{ID: 0, RequestURL: "/login", ResponseBody: `{"token":"raw"}`, ResponseHeaders: map[string]string{"Content-Type": "application/json"}},
			{ID: 1, RequestURL: "/orders", RequestHeaders: map[string]string{"X-Token": "raw"}},
		},
	}
}

func storage_obs(i int) ScriptRecordingObservation {
	return ScriptRecordingObservation{ID: i, RequestURL: "/step"}
}

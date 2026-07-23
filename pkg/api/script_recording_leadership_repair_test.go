package api

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestExecutionLeadershipTakeoverRepairsLegacyRecordingWrittenAfterFollowerInit(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "apexload.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// Construct the standby first. Its storage initialization is complete before
	// the simulated old controller writes the legacy row below.
	server := NewServer(ServerConfig{
		Store:                 store,
		HAExecutionLeadership: true,
		MaxConcurrent:         1,
	})
	if server.leadership == nil {
		t.Fatal("HA execution leadership was not configured")
	}
	defer server.leadership.relinquish()

	script := &dsl.Script{ID: "legacy-after-follower-init", Name: "Legacy recording", Protocol: "http"}
	if err := store.SaveScript(script); err != nil {
		t.Fatal(err)
	}
	parent, err := store.GetScript(script.ID)
	if err != nil {
		t.Fatal(err)
	}

	legacyDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer legacyDB.Close()
	now := time.Now().UTC()
	legacyObservations := `[{"id":0,"request_url":"https://legacy.example/private","request_headers":{"Authorization":"Bearer secret"}}]`
	// This is the actual pre-binding/pre-projection column list: it deliberately
	// omits project_id, script_incarnation, and both projection metadata columns.
	if _, err := legacyDB.Exec(`INSERT INTO script_recordings
		(script_id, schema_version, source, sensitivity, request_count, observations_json, correlation_observations_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		script.ID, "apexload.recording.legacy", "legacy-controller", "scrubbed", 1,
		legacyObservations, "[]", now, now); err != nil {
		t.Fatal(err)
	}

	// Acquiring the execution lease invokes the takeover repair without closing
	// or reopening the already-initialized follower store.
	server.leadership.tick()
	deadline := time.Now().Add(5 * time.Second)
	for !server.leadership.IsAdmitted() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !server.leadership.IsAdmitted() {
		t.Fatal("successor was not admitted after takeover repair")
	}

	var (
		projectID         string
		incarnation       string
		observations      string
		projectionVersion int
		projectedAt       time.Time
		updatedAt         time.Time
	)
	if err := legacyDB.QueryRow(`SELECT COALESCE(project_id, ''), COALESCE(script_incarnation, ''),
			COALESCE(observations_json, ''), COALESCE(observations_projection_version, 0),
			observations_projected_at, updated_at
		FROM script_recordings WHERE script_id = ?`, script.ID).Scan(
		&projectID,
		&incarnation,
		&observations,
		&projectionVersion,
		&projectedAt,
		&updatedAt,
	); err != nil {
		t.Fatal(err)
	}
	wantIncarnation := storage.ScriptIncarnation(parent.ID, parent.ProjectID, parent.RefKey, parent.CreatedAt)
	if projectID != parent.ProjectID || incarnation != wantIncarnation {
		t.Fatalf("takeover binding = (%q, %q), want (%q, %q)", projectID, incarnation, parent.ProjectID, wantIncarnation)
	}
	if observations != `[{"id":0}]` {
		t.Fatalf("takeover observations = %q, want structural projection", observations)
	}
	if projectionVersion != 1 || !projectedAt.Equal(updatedAt) {
		t.Fatalf("takeover projection metadata = (version %d, projected %s, updated %s)", projectionVersion, projectedAt, updatedAt)
	}
}

package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// Historical disclosure fixtures must model pre-manifest persisted bytes, not
// ask today's normalizer to certify a mapping it correctly refuses. This SQL
// seam belongs only to tests and creates no new writer/attempt proof.
func newLegacyAnalysisFixtureServer(t *testing.T) (*Server, storage.Store, string) {
	t.Helper()
	path := t.TempDir() + "/legacy-analysis.db"
	store, err := storage.NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{ID: "proj-a", Name: "Historical analysis", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	return server, store, path
}
func sealLegacyAnalysisFixture(t *testing.T, path string, in *storage.AnalysisImport) *storage.AnalysisImport {
	t.Helper()
	oldRevision := in.Revision
	copy := *in
	copy.PartitionManifestVersion = 0
	copy.PartitionManifest = nil
	copy.Owner = ""
	copy.LeaseExpiresAt = time.Time{}
	copy.State = "ready"
	copy.Revision++
	body, err := json.Marshal(copy)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	result, err := db.Exec(`UPDATE analysis_imports SET payload=?,revision=?,state=?,owner=?,lease_expires_at=? WHERE id=? AND project_id=? AND revision=? AND state='mapped'`, string(body), copy.Revision, copy.State, "", time.Time{}, copy.ID, copy.ProjectID, oldRevision)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		t.Fatal("historical fixture CAS failed", rows, err)
	}
	return &copy
}

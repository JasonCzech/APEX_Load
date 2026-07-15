package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestAuthoringSessionOwnershipResolutionDoesNotRecordUnauthorizedAccess(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	now := time.Now().UTC().Truncate(time.Microsecond)
	projectID := "authoring-resolution-project"
	if err := store.SaveProject(&storage.Project{ID: projectID, Name: "Authoring", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	lastAccessedAt := now.Add(-time.Hour)
	session := &storage.AuthoringSession{
		ID: "authoring-resolution-session", ProjectID: projectID, Source: "manual", Name: "Draft", Status: "draft",
		CreatedAt: now, UpdatedAt: now, LastAccessedAt: lastAccessedAt,
	}
	if err := store.SaveAuthoringSession(session); err != nil {
		t.Fatalf("SaveAuthoringSession: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/authoring/sessions/"+session.ID, nil)
	req.SetPathValue("id", session.ID)
	resolvedProjectID, status, message := resolveAuthoringSessionProjectID(req, store)
	if status != 0 || resolvedProjectID != projectID {
		t.Fatalf("resolveAuthoringSessionProjectID = %q, %d, %q", resolvedProjectID, status, message)
	}
	afterResolution, err := store.GetAuthoringSession(session.ID)
	if err != nil {
		t.Fatalf("GetAuthoringSession: %v", err)
	}
	if !afterResolution.LastAccessedAt.Equal(lastAccessedAt) {
		t.Fatalf("authorization lookup advanced last_accessed_at to %v, want %v", afterResolution.LastAccessedAt, lastAccessedAt)
	}

	server := NewServer(ServerConfig{Store: store})
	loaded, ok := server.getAuthoringSession(httptest.NewRecorder(), session.ID)
	if !ok || loaded == nil {
		t.Fatal("authorized handler-stage load failed")
	}
	if !loaded.LastAccessedAt.After(lastAccessedAt) {
		t.Fatalf("authorized handler-stage load did not advance last_accessed_at: %v", loaded.LastAccessedAt)
	}
}

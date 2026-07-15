package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestCreateReleaseSLAWaiverRejectsArchivedRelease(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "release-sla-governance.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Now().UTC()
	project := &storage.Project{ID: "waiver-http-project", Name: "Waiver HTTP Project", CreatedAt: now, UpdatedAt: now}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	application := &storage.Application{ID: "waiver-http-application", ProjectID: project.ID, Name: "Waiver HTTP Application", CreatedAt: now, UpdatedAt: now}
	if err := store.SaveApplication(application); err != nil {
		t.Fatalf("SaveApplication: %v", err)
	}
	release := &storage.Release{ID: "waiver-http-release", ApplicationID: application.ID, ProjectID: project.ID, Name: "Waiver HTTP Release", CreatedAt: now, UpdatedAt: now}
	if err := store.SaveRelease(release); err != nil {
		t.Fatalf("SaveRelease: %v", err)
	}
	if err := store.ArchiveRelease(release.ID); err != nil {
		t.Fatalf("ArchiveRelease: %v", err)
	}

	server := NewServer(ServerConfig{Store: store})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/releases/"+release.ID+"/sla-governance/waivers", bytes.NewBufferString(`{"reason":"risk accepted"}`))
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusConflict {
		t.Fatalf("create waiver status = %d body=%s, want %d", resp.Code, resp.Body.String(), http.StatusConflict)
	}
	waivers, err := store.ListReleaseSLAWaivers(release.ID)
	if err != nil {
		t.Fatalf("ListReleaseSLAWaivers: %v", err)
	}
	if len(waivers) != 0 {
		t.Fatalf("persisted waivers = %+v, want none", waivers)
	}
}

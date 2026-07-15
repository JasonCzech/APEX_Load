package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestAuthoringSessionListIsPaginated(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	projectID := "authoring-page-project"
	if err := store.SaveProject(&storage.Project{ID: projectID, Name: projectID, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	for i, id := range []string{"authoring-page-1", "authoring-page-2", "authoring-page-3"} {
		if err := store.SaveAuthoringSession(&storage.AuthoringSession{
			ID: id, ProjectID: projectID, Source: "manual", Name: id, Status: "draft",
			Draft: &dsl.Script{ID: "draft-" + id, ProjectID: projectID, Name: id, Protocol: "http"},
		}); err != nil {
			t.Fatalf("SaveAuthoringSession(%s): %v", id, err)
		}
		if i < 2 {
			time.Sleep(2 * time.Millisecond)
		}
	}
	server := NewServer(ServerConfig{Store: store})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/authoring/sessions?project_id="+projectID+"&limit=1&offset=1", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", resp.Code, resp.Body.String())
	}
	var payload struct {
		Sessions []*storage.AuthoringSession `json:"sessions"`
		Limit    int                         `json:"limit"`
		Offset   int                         `json:"offset"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Limit != 1 || payload.Offset != 1 || len(payload.Sessions) != 1 || payload.Sessions[0].ID != "authoring-page-2" {
		t.Fatalf("paginated response = %#v", payload)
	}
}

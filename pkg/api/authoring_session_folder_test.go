package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

// A Java Vuser script with runner actions cannot be created through POST
// /scripts, so the editor saves it as an authoring session instead. Without a
// folder on that request every reviewed script lands at the project root,
// silently losing the placement the author chose.
func TestCreateAuthoringSessionBindsFolderToDraft(t *testing.T) {
	projectID := "project-authoring-folder"
	folderID := "folder-authoring"
	server := authoringFolderTestServer(t, map[string]string{folderID: projectID})

	session := createAuthoringSessionForTest(t, server, map[string]any{
		"project_id": projectID,
		"folder_id":  folderID,
		"source":     "manual",
		"name":       "Java Checkout Flow",
		"status":     "reviewing",
		"draft":      javaVuserApprovalDraftForTest(),
	}, http.StatusCreated)

	if session.Draft == nil {
		t.Fatal("session draft = nil")
	}
	if session.Draft.FolderID == nil || *session.Draft.FolderID != folderID {
		t.Fatalf("draft folder_id = %v, want %q", session.Draft.FolderID, folderID)
	}
}

func TestCreateAuthoringSessionRejectsFolderFromAnotherProject(t *testing.T) {
	server := authoringFolderTestServer(t, map[string]string{
		"folder-other": "project-other",
	}, "project-owner")

	createAuthoringSessionForTest(t, server, map[string]any{
		"project_id": "project-owner",
		"folder_id":  "folder-other",
		"source":     "manual",
		"name":       "Java Checkout Flow",
		"draft":      javaVuserApprovalDraftForTest(),
	}, http.StatusBadRequest)
}

// The approve path re-validates the folder because a folder that was valid when
// the session was created can be deleted or moved during review, and nothing
// downstream of approval checks the placement.
func TestApproveAuthoringSessionRejectsAFolderDeletedDuringReview(t *testing.T) {
	projectID := "project-approve-folder"
	folderID := "folder-approve"
	server := authoringFolderTestServer(t, map[string]string{folderID: projectID})

	session := createAuthoringSessionForTest(t, server, map[string]any{
		"project_id": projectID,
		"folder_id":  folderID,
		"source":     "manual",
		"name":       "Java Checkout Flow",
		"status":     "reviewing",
		"draft":      javaVuserApprovalDraftForTest(),
	}, http.StatusCreated)

	// The folder disappears while the draft sits in review.
	if err := server.store.DeleteFolder(folderID); err != nil {
		t.Fatalf("DeleteFolder returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/authoring/sessions/"+session.ID+"/approve", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("approve status = %d, want %d body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "folder") {
		t.Fatalf("approve body = %s, want it to name the folder problem", resp.Body.String())
	}
}

// authoringFolderTestServer builds a server backed by a real store containing
// the given folders (folderID -> projectID) plus any extra projects.
func authoringFolderTestServer(t *testing.T, folders map[string]string, extraProjects ...string) *Server {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	projects := make(map[string]struct{})
	for _, projectID := range extraProjects {
		projects[projectID] = struct{}{}
	}
	for _, projectID := range folders {
		projects[projectID] = struct{}{}
	}
	for projectID := range projects {
		if err := store.SaveProject(&storage.Project{ID: projectID, Name: projectID}); err != nil {
			t.Fatalf("SaveProject(%q) returned error: %v", projectID, err)
		}
	}
	for folderID, projectID := range folders {
		if err := store.SaveFolder(&storage.Folder{ID: folderID, ProjectID: projectID, Name: folderID, Category: "scripts"}); err != nil {
			t.Fatalf("SaveFolder(%q) returned error: %v", folderID, err)
		}
	}
	return NewServer(ServerConfig{Store: store, MaxConcurrent: 1})
}

func javaVuserApprovalDraftForTest() dsl.Script {
	return dsl.Script{
		Name:     "Java Checkout Flow",
		Version:  "1.0.0",
		Protocol: "java_vuser",
		Actions: []dsl.Action{{
			Name: "Java action",
			Type: "java_vuser_run",
		}},
	}
}

func createAuthoringSessionForTest(t *testing.T, server *Server, payload map[string]any, wantStatus int) *storage.AuthoringSession {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/authoring/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != wantStatus {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, wantStatus, resp.Body.String())
	}
	if wantStatus >= http.StatusBadRequest {
		return nil
	}
	var session storage.AuthoringSession
	if err := json.Unmarshal(resp.Body.Bytes(), &session); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	return &session
}

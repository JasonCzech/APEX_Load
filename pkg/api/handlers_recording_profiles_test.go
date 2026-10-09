package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestRecordingProfileEndpoints(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})
	do := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		return resp
	}
	created := do(http.MethodPost, "/api/v1/projects", `{"name":"Shop"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create project: %d %s", created.Code, created.Body.String())
	}
	var project storage.Project
	_ = json.Unmarshal(created.Body.Bytes(), &project)
	base := "/api/v1/projects/" + project.ID + "/recording-profiles"

	empty := do(http.MethodGet, base, "")
	var listed recordingProfilesResponse
	if empty.Code != http.StatusOK || json.Unmarshal(empty.Body.Bytes(), &listed) != nil || len(listed.Profiles) != 0 || len(listed.Rules) == 0 {
		t.Fatalf("empty list: %d %s", empty.Code, empty.Body.String())
	}

	put := do(http.MethodPut, base, `{"profiles":[{"id":"default","name":"Default","think_time_threshold_ms":1500,"drop_headers":["X-Debug"],"correlation":{"user_rules":[{"name":"OrderID","type":"boundary","left":"order=","right":"&"}]}}]}`)
	if put.Code != http.StatusOK {
		t.Fatalf("put: %d %s", put.Code, put.Body.String())
	}
	got := do(http.MethodGet, base, "")
	if err := json.Unmarshal(got.Body.Bytes(), &listed); err != nil || len(listed.Profiles) != 1 || listed.Profiles[0].ThinkTimeThresholdMs != 1500 || len(listed.Profiles[0].Correlation.UserRules) != 1 {
		t.Fatalf("persisted profiles: %s", got.Body.String())
	}

	if bad := do(http.MethodPut, base, `{"profiles":[{"id":"x","name":"X","resource_mode":"inline"}]}`); bad.Code != http.StatusBadRequest || !strings.Contains(bad.Body.String(), "resource_mode") {
		t.Fatalf("an invalid profile must be rejected: %d %s", bad.Code, bad.Body.String())
	}
	if stale := do(http.MethodPut, base, `{"profiles":[],"expected_updated_at":"2001-01-01T00:00:00Z"}`); stale.Code != http.StatusConflict {
		t.Fatalf("a stale write must conflict: %d %s", stale.Code, stale.Body.String())
	}

	// A project settings save that does not know about profiles keeps them.
	if update := do(http.MethodPut, "/api/v1/projects/"+project.ID, `{"name":"Shop 2","settings":{"max_vusers":10}}`); update.Code != http.StatusOK {
		t.Fatalf("project update: %d %s", update.Code, update.Body.String())
	}
	kept := do(http.MethodGet, base, "")
	if err := json.Unmarshal(kept.Body.Bytes(), &listed); err != nil || len(listed.Profiles) != 1 {
		t.Fatalf("profiles lost by a project update: %s", kept.Body.String())
	}

	if missing := do(http.MethodPost, "/api/v1/recorder/start", `{"project_id":"`+project.ID+`","recording_profile_id":"nope"}`); missing.Code != http.StatusBadRequest {
		t.Fatalf("an unknown profile must be refused: %d %s", missing.Code, missing.Body.String())
	}
}

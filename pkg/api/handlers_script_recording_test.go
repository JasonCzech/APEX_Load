package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func recordingTestServer(t *testing.T) *Server {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	server := NewServer(ServerConfig{})
	server.store = store
	now := time.Now().UTC()
	for _, projectID := range []string{"p1", "p2"} {
		if err := store.SaveProject(&storage.Project{ID: projectID, Name: projectID, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatalf("SaveProject(%s): %v", projectID, err)
		}
	}
	return server
}

func TestScriptRecordingStatusEndpoint(t *testing.T) {
	server := recordingTestServer(t)

	// Absent baseline → exists:false.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/s1/recording/status", nil)
	req.SetPathValue("id", "s1")
	resp := httptest.NewRecorder()
	server.handleGetScriptRecordingStatus(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d", resp.Code)
	}
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	if out["exists"] != false {
		t.Fatalf("expected exists:false, got %v", out)
	}

	// With a baseline → exists:true + request_count.
	if err := server.store.SaveScriptRecording(&storage.ScriptRecordingRecord{
		ScriptID: "s1", Source: "browser_recording",
		Observations: []storage.ScriptRecordingObservation{{ID: 0}, {ID: 1}},
	}); err != nil {
		t.Fatalf("seed baseline: %v", err)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/scripts/s1/recording/status", nil)
	req.SetPathValue("id", "s1")
	resp = httptest.NewRecorder()
	server.handleGetScriptRecordingStatus(resp, req)
	json.NewDecoder(resp.Body).Decode(&out)
	if out["exists"] != true || out["request_count"].(float64) != 2 {
		t.Fatalf("expected exists:true count:2, got %v", out)
	}
}

func TestDiagnoseScriptReplayEndpoint(t *testing.T) {
	server := recordingTestServer(t)

	// 404 without a baseline.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/s1/diagnose-replay", strings.NewReader(`{"replay":[]}`))
	req.SetPathValue("id", "s1")
	resp := httptest.NewRecorder()
	server.handleDiagnoseScriptReplay(resp, req)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("expected 404 without baseline, got %d", resp.Code)
	}

	// Seed a baseline where step 1's response carries a CSRF reused by step 2.
	if err := server.store.SaveScriptRecording(&storage.ScriptRecordingRecord{
		ScriptID: "s1",
		Observations: []storage.ScriptRecordingObservation{
			{ID: 1, ResponseBody: `{"csrf_token":"TOKEN-AAA-1111-recorded"}`},
			{ID: 2, RequestBody: "csrf=TOKEN-AAA-1111-recorded"},
		},
	}); err != nil {
		t.Fatalf("seed baseline: %v", err)
	}

	// Replay where step 1 rotated the token but step 2 still sends the old one.
	body := `{"replay":[{"id":1,"response_body":"{\"csrf_token\":\"TOKEN-BBB-2222-replayed\"}"},{"id":2,"request_body":"csrf=TOKEN-AAA-1111-recorded"}]}`
	req = httptest.NewRequest(http.MethodPost, "/api/v1/scripts/s1/diagnose-replay", strings.NewReader(body))
	req.SetPathValue("id", "s1")
	resp = httptest.NewRecorder()
	server.handleDiagnoseScriptReplay(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	var out correlationDiagnoseResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.Blocking || out.Counts.Errors != 1 {
		t.Fatalf("expected 1 blocking error from the stored baseline diff, got %+v", out)
	}
	if len(out.Suggestions) != 1 || out.Suggestions[0].FailingStepID != 2 {
		t.Fatalf("unexpected suggestions: %+v", out.Suggestions)
	}
}

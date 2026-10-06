package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func acceptanceBody(result string, artifacts bool) map[string]any {
	body := map[string]any{"requirement_id": "BE-4", "feature": "browser-har", "platform": "linux-amd64", "commit_sha": "abcdef1234567", "suite": "browser-acceptance", "result": result}
	if artifacts {
		body["artifacts"] = []map[string]string{{"name": "matrix.json", "sha256": strings.Repeat("a", 64)}}
	}
	return body
}

func TestFeatureAcceptanceRecordsDriveSupportStatus(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	if response := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/system/acceptance-records", acceptanceBody("passed", false), ""); response.Code != http.StatusBadRequest {
		t.Fatalf("a pass without evidence was recorded: %d %s", response.Code, response.Body.String())
	}
	for _, invalid := range []map[string]any{
		{"requirement_id": "be4", "feature": "x", "commit_sha": "abcdef1", "suite": "s", "result": "failed"},
		{"requirement_id": "BE-4", "feature": "Browser HAR", "commit_sha": "abcdef1", "suite": "s", "result": "failed"},
		{"requirement_id": "BE-4", "feature": "x", "commit_sha": "main", "suite": "s", "result": "failed"},
		{"requirement_id": "BE-4", "feature": "x", "commit_sha": "abcdef1", "suite": "s", "result": "supported"},
	} {
		if response := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/system/acceptance-records", invalid, ""); response.Code != http.StatusBadRequest {
			t.Fatalf("invalid record accepted %v: %d", invalid, response.Code)
		}
	}
	status := func() []FeatureSupportStatus {
		response := authoringAPIRequest(t, server, http.MethodGet, "/api/v1/system/acceptance-status?requirement=BE-4", nil, "")
		var body struct {
			Statuses []FeatureSupportStatus `json:"statuses"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body.Statuses
	}
	if response := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/system/acceptance-records", acceptanceBody("passed", true), ""); response.Code != http.StatusCreated {
		t.Fatalf("pass: %d %s", response.Code, response.Body.String())
	}
	if current := status(); len(current) != 1 || !current[0].Supported {
		t.Fatalf("after pass: %#v", current)
	}
	time.Sleep(2 * time.Millisecond)
	if response := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/system/acceptance-records", acceptanceBody("revoked", false), ""); response.Code != http.StatusCreated {
		t.Fatalf("revoke: %d %s", response.Code, response.Body.String())
	}
	current := status()
	if len(current) != 1 || current[0].Supported || current[0].Latest.Result != "revoked" {
		t.Fatalf("a revoked claim still reads as supported: %#v", current)
	}
	list := authoringAPIRequest(t, server, http.MethodGet, "/api/v1/system/acceptance-records?requirement=BE-4", nil, "")
	if !strings.Contains(list.Body.String(), `"result":"passed"`) || !strings.Contains(list.Body.String(), `"result":"revoked"`) {
		t.Fatalf("records are not append-only history: %s", list.Body.String())
	}
}

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

// Cookies and session, anti-forgery and tracing headers are state the source
// recorded, not credentials: a HAR import does not ask for a project secret
// for them, saves them as recorded with no credential locator, and the saved
// script shows them. An operator credential beside them is still reviewed.
func TestHARImportKeepsRecordedStateHeadersOutOfCredentialReview(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "recorded-state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})
	const (
		cookie = "JSESSIONID=recorded-session-41; BIGipServer=pool-9"
		csrf   = "recorded-csrf-52"
		bearer = "operator-bearer-63"
	)
	har := map[string]any{"log": map[string]any{"entries": []any{map[string]any{
		"request": map[string]any{
			"method": "GET", "url": "https://api.example.test/orders",
			"headers": []any{
				map[string]any{"name": "Cookie", "value": cookie},
				map[string]any{"name": "X-CSRF-Token", "value": csrf},
				map[string]any{"name": "Authorization", "value": "Bearer " + bearer},
			},
		},
		"response": map[string]any{"status": 200, "content": map[string]any{"text": `{"ok":true}`}},
	}}}}
	content, err := json.Marshal(har)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{"name": "Recorded state", "content": string(content)})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/har?project_id="+storage.DefaultProjectID, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("import status = %d body=%.400s", response.Code, response.Body.String())
	}
	var imported struct {
		Script       map[string]any `json:"script"`
		Requirements []struct {
			Label string `json:"label"`
			Path  string `json:"path"`
		} `json:"secret_binding_requirements"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &imported); err != nil {
		t.Fatal(err)
	}
	authorization := 0
	for _, requirement := range imported.Requirements {
		text := strings.ToLower(requirement.Label + " " + requirement.Path)
		if strings.Contains(text, "cookie") || strings.Contains(text, "csrf") {
			t.Errorf("recorded state went to credential review: %+v", requirement)
		}
		if strings.Contains(text, "authorization") {
			authorization++
		}
	}
	if authorization == 0 {
		t.Fatalf("the Authorization header was not reviewed: %+v", imported.Requirements)
	}

	created := postGenericImportBatch(t, server, map[string]any{
		"operation_id":   "recorded-state-headers",
		"source_profile": "har",
		"scripts":        []any{imported.Script},
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("save status = %d body=%.400s", created.Code, created.Body.String())
	}
	var saved struct {
		Scripts []StoredScript `json:"scripts"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &saved); err != nil || len(saved.Scripts) != 1 {
		t.Fatalf("save response = %.400s (%v)", created.Body.String(), err)
	}
	read := httptest.NewRecorder()
	server.mux.ServeHTTP(read, httptest.NewRequest(http.MethodGet, "/api/v1/scripts/"+saved.Scripts[0].ID, nil))
	if read.Code != http.StatusOK {
		t.Fatalf("read status = %d body=%.400s", read.Code, read.Body.String())
	}
	view := read.Body.String()
	for _, shown := range []string{cookie, csrf} {
		if !strings.Contains(view, shown) {
			t.Errorf("saved script view hid recorded state %q: %.2000s", shown, view)
		}
	}
	stored, ok := server.runner.GetScript(saved.Scripts[0].ID)
	if !ok || stored.Script == nil || len(stored.Script.Actions) != 1 {
		t.Fatal("saved script is unavailable")
	}
	action := stored.Script.Actions[0]
	if len(action.HeaderList) != 3 || action.HeaderList[0].Value != cookie || action.HeaderList[1].Value != csrf {
		t.Fatalf("saved headers = %#v, want the recorded state kept as imported", action.HeaderList)
	}
	targets, _ := json.Marshal(action.Metadata[dsl.ImportPreservedCredentialTargetsMetadataKey])
	if !strings.Contains(string(targets), `"index":2`) || strings.Contains(string(targets), `"index":0`) || strings.Contains(string(targets), `"index":1`) {
		t.Fatalf("credential locators = %s, want only the Authorization header", targets)
	}
}

// A HAR whose only sensitive-looking headers are recorded state has nothing
// for credential review and saves as recorded.
func TestHARImportWithOnlyRecordedStateHeadersSaves(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "recorded-state-only.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})
	const cookie = "JSESSIONID=recorded-session-41; BIGipServer=pool-9"
	content := `{"log":{"entries":[{"request":{"method":"GET","url":"https://api.example.test/orders","headers":[` +
		`{"name":"Cookie","value":"` + cookie + `"},{"name":"X-CSRF-Token","value":"recorded-csrf-52"},` +
		`{"name":"traceparent","value":"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}]},` +
		`"response":{"status":200,"content":{"text":"{}"}}}]}}`
	body, err := json.Marshal(map[string]any{"name": "Recorded state only", "content": content})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/har?project_id="+storage.DefaultProjectID, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("import status = %d body=%.400s", response.Code, response.Body.String())
	}
	var imported struct {
		Script       map[string]any    `json:"script"`
		Requirements []json.RawMessage `json:"secret_binding_requirements"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &imported); err != nil {
		t.Fatal(err)
	}
	if len(imported.Requirements) != 0 {
		t.Fatalf("credential review = %s, want none", imported.Requirements)
	}
	created := postGenericImportBatch(t, server, map[string]any{
		"operation_id":   "recorded-state-only",
		"source_profile": "har",
		"scripts":        []any{imported.Script},
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("save status = %d body=%.400s", created.Code, created.Body.String())
	}
	var saved struct {
		Scripts []StoredScript `json:"scripts"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &saved); err != nil || len(saved.Scripts) != 1 {
		t.Fatalf("save response = %.400s (%v)", created.Body.String(), err)
	}
	stored, ok := server.runner.GetScript(saved.Scripts[0].ID)
	if !ok || stored.Script == nil || len(stored.Script.Actions) != 1 ||
		len(stored.Script.Actions[0].HeaderList) == 0 || stored.Script.Actions[0].HeaderList[0].Value != cookie {
		t.Fatalf("saved script did not keep the recorded cookie: %#v", stored)
	}
}

package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHARImportGivesClientGeneratedUUIDsFreshValues(t *testing.T) {
	const (
		clientSession = "6f1c2a3b-4d5e-4f60-8172-93a4b5c6d7e8"
		serverRecord  = "a0a0a0a0-b1b1-4c2c-8d3d-e4e4e4e4e4e4"
	)
	entry := func(method, path, body, response string) map[string]any {
		request := map[string]any{"method": method, "url": "https://example.test" + path, "httpVersion": "HTTP/1.1", "headers": []any{}}
		if body != "" {
			request["postData"] = map[string]any{"mimeType": "application/json", "text": body}
		}
		return map[string]any{"startedDateTime": "2026-01-01T00:00:00.000Z", "request": request, "response": map[string]any{
			"status": 200, "httpVersion": "HTTP/1.1", "headers": []any{}, "content": map[string]any{"mimeType": "application/json", "text": response}}}
	}
	har, err := json.Marshal(map[string]any{"log": map[string]any{"version": "1.2", "creator": map[string]any{"name": "synthetic", "version": "1"}, "entries": []any{
		entry("GET", "/records", "", `{"record":"`+serverRecord+`"}`),
		entry("POST", "/events", `{"clientSessionId":"`+clientSession+`","record":"`+serverRecord+`"}`, `{}`),
		entry("POST", "/events", `{"clientSessionId":"`+clientSession+`"}`, `{}`),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"name": "Client ids", "content": string(har)})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/har", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	NewServer(ServerConfig{}).mux.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var decoded struct {
		Script struct {
			Parameters []struct {
				Name, Type, Update string
			} `json:"parameters"`
			Actions []struct {
				Body string `json:"body"`
			} `json:"actions"`
		} `json:"script"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, parameter := range decoded.Script.Parameters {
		found = found || parameter.Name == "client_session_id" && parameter.Type == "uuid" && parameter.Update == "each_iteration"
	}
	if !found {
		t.Fatalf("parameters = %+v", decoded.Script.Parameters)
	}
	bodies := ""
	for _, action := range decoded.Script.Actions {
		bodies += action.Body
	}
	if strings.Contains(bodies, clientSession) || !strings.Contains(bodies, "{{param.client_session_id}}") {
		t.Fatalf("bodies = %s", bodies)
	}
	if !strings.Contains(strings.Join(decoded.Warnings, "\n"), "client-generated UUID") {
		t.Fatalf("warnings = %v", decoded.Warnings)
	}
}

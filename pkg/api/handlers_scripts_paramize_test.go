package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// suggestResponse mirrors the handler's embedded paramize.Result plus the note.
type suggestResponse struct {
	Suggestions []paramize.Suggestion `json:"suggestions"`
	Summary     paramize.Summary      `json:"summary"`
	Note        string                `json:"note"`
}

func countSuggestionsByKind(suggestions []paramize.Suggestion, kind paramize.Kind) int {
	n := 0
	for _, s := range suggestions {
		if s.Kind == kind {
			n++
		}
	}
	return n
}

func TestHandleSuggestScriptParameterization(t *testing.T) {
	server := NewServer(ServerConfig{})
	stored := server.runner.StoreScript(&dsl.Script{
		Name:     "Saved Order Flow",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://api.example.com"},
		Actions: []dsl.Action{
			{Name: "get user", Type: "http_request", Method: "GET", Target: "/users?user_id=12345"},
			{Name: "get orders", Type: "http_request", Method: "GET", Target: "/orders?user_id=12345"},
			{
				Name: "login", Type: "http_request", Method: "POST", Target: "/login",
				Headers: map[string]string{"Authorization": "Bearer tok123", "Accept": "application/json"},
				Body:    `{"password": "hunter2"}`,
			},
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/"+stored.ID+"/parameterize/suggest", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var got suggestResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v body=%s", err, resp.Body.String())
	}

	if dataParams := countSuggestionsByKind(got.Suggestions, paramize.KindDataParam); dataParams < 1 {
		t.Errorf("data_param suggestions = %d, want >= 1", dataParams)
	}
	if secrets := countSuggestionsByKind(got.Suggestions, paramize.KindSecret); secrets < 1 {
		t.Errorf("secret suggestions = %d, want >= 1", secrets)
	}
	// A saved script carries no captured observations, so the correlation pass is a no-op.
	if correlations := countSuggestionsByKind(got.Suggestions, paramize.KindCorrelation); correlations != 0 {
		t.Errorf("correlation suggestions = %d, want 0 (no observations available in the editor)", correlations)
	}
	if got.Note == "" {
		t.Error("expected a note explaining the correlation limitation")
	}
}

func TestHandleSuggestScriptParameterizationNotFound(t *testing.T) {
	server := NewServer(ServerConfig{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/does-not-exist/parameterize/suggest", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusNotFound, resp.Body.String())
	}
}

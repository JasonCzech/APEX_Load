package postman

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/jasonczech/apexload/pkg/engine"
	_ "github.com/jasonczech/apexload/pkg/protocol/http"
)

// TestPostmanImportReplaySmokeChainsBearerToken proves the headline chain works
// end-to-end: a login request's test script captures the access token, and a
// later request replays it in its Authorization header.
func TestPostmanImportReplaySmokeChainsBearerToken(t *testing.T) {
	var mu sync.Mutex
	var profileAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/login":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "tok-abc"})
		case r.Method == http.MethodGet && r.URL.Path == "/profile":
			mu.Lock()
			profileAuth = r.Header.Get("Authorization")
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"ok": "true"})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	result, err := Convert(ImportRequest{
		Content: `{
  "info": {"name": "Login Chain"},
  "variable": [{"key": "baseUrl", "value": "` + server.URL + `"}],
  "item": [
    {
      "name": "Login",
      "event": [
        {"listen": "test", "script": {"exec": [
          "pm.environment.set(\"token\", pm.response.json().access_token);"
        ]}}
      ],
      "request": {"method": "POST", "url": "{{baseUrl}}/login", "body": {"mode": "raw", "raw": "{\"u\":\"a\"}"}}
    },
    {
      "name": "Get Profile",
      "request": {
        "method": "GET",
        "header": [{"key": "Authorization", "value": "Bearer {{token}}"}],
        "url": "{{baseUrl}}/profile"
      }
    }
  ]
}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}

	report, err := engine.ReplayScript(context.Background(), result.Script, engine.ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatalf("ReplayScript returned error: %v", err)
	}
	if !report.Success {
		t.Fatalf("replay failed: %s events=%#v", report.Error, report.Events)
	}
	if got := report.FinalVariables["token"]; got != "tok-abc" {
		t.Fatalf("captured token = %q, want tok-abc", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if profileAuth != "Bearer tok-abc" {
		t.Fatalf("profile Authorization = %q, want chained 'Bearer tok-abc'", profileAuth)
	}
}

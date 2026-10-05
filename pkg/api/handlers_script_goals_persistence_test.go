package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Every reopen constructs a new controller and SQL connection. Assertions on
// the create/update response alone would only prove the runner's mutable cache.
func TestScriptGoalsSurviveControllerReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "authoring-goals.db")
	var server *Server
	var store *storage.SQLiteStore
	closeController := func(t *testing.T) {
		t.Helper()
		if server != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err := server.Shutdown(ctx)
			cancel()
			server = nil
			if err != nil {
				t.Errorf("shutdown controller: %v", err)
			}
		}
		if store != nil {
			if err := store.Close(); err != nil {
				t.Errorf("close SQL store: %v", err)
			}
			store = nil
		}
	}
	t.Cleanup(func() { closeController(t) })
	openController := func(t *testing.T) {
		t.Helper()
		var err error
		store, err = storage.NewSQLiteStore(path)
		if err != nil {
			t.Fatal(err)
		}
		seedAuthoringScriptProjects(t, store, "goals-project")
		server = NewServer(ServerConfig{Store: store, MaxConcurrent: 2})
	}
	request := func(t *testing.T, method, path string, payload any, status int) []byte {
		t.Helper()
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		if resp.Code != status {
			t.Fatalf("%s %s: %d, want %d: %s", method, path, resp.Code, status, resp.Body.String())
		}
		return append([]byte(nil), resp.Body.Bytes()...)
	}
	openController(t)
	p95, zero, tps, errors := 217.5, 0.0, 8.75, 17
	min, max := 1, 6
	goals := &dsl.GoalConfig{
		TargetAverageTPS: &tps, P95LatencyMs: &p95, MaxErrorPct: &zero,
		MaxErrorCount: &errors, MinVUsers: &min, MaxVUsers: &max,
		StopOnSLABreach: true, StopOnMaxErrors: true, SLABreachWindow: "11s",
		TransactionRules: []dsl.TransactionSLARule{{
			Population: "transaction", Protocol: "http", Group: "checkout", Transaction: "Purchase",
			Metric: "p95_duration_ms", Comparator: "lte", Threshold: 215.25, Severity: "fail",
		}},
	}
	script := dsl.Script{
		Name: "Durable authored goals", Protocol: "http", Version: "1.0.0", GoalConfig: goals,
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "Purchase", Type: "request", Method: "GET", Target: "/"}},
	}
	var created StoredScript
	if err := json.Unmarshal(request(t, http.MethodPost, "/api/v1/scripts", map[string]any{"project_id": "goals-project", "script": script}, http.StatusCreated), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" {
		t.Fatal("create did not return its canonical identity")
	}
	states := []struct {
		name string
		goal *dsl.GoalConfig
	}{
		{"complete", goals},
		{"explicit-zero-and-false", &dsl.GoalConfig{MaxErrorPct: &zero, StopOnSLABreach: false, StopOnMaxErrors: false}},
		{"present-empty", &dsl.GoalConfig{}},
		{"cleared", nil},
	}
	for index, state := range states {
		t.Run(state.name, func(t *testing.T) {
			if index > 0 {
				script.GoalConfig = state.goal
				request(t, http.MethodPut, "/api/v1/scripts/"+created.ID, map[string]any{"script": script}, http.StatusOK)
			}
			closeController(t)
			openController(t)
			var reopened StoredScript
			if err := json.Unmarshal(request(t, http.MethodGet, "/api/v1/scripts/"+created.ID, nil, http.StatusOK), &reopened); err != nil {
				t.Fatal(err)
			}
			if reopened.Script == nil || !reflect.DeepEqual(reopened.Script.GoalConfig, state.goal) {
				t.Fatalf("canonical reopened goals = %#v, want %#v", reopened.Script, state.goal)
			}
			var list struct {
				Scripts []*StoredScript `json:"scripts"`
			}
			if err := json.Unmarshal(request(t, http.MethodGet, "/api/v1/scripts?project_id=goals-project", nil, http.StatusOK), &list); err != nil {
				t.Fatal(err)
			}
			if len(list.Scripts) != 1 || list.Scripts[0].Script == nil || !reflect.DeepEqual(list.Scripts[0].Script.GoalConfig, state.goal) {
				t.Fatalf("listed goals disagree with canonical reopened document: %#v", list.Scripts)
			}
		})
	}
}

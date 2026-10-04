package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkflowPreparationAndSources(t *testing.T) {
	var gotPath, gotMethod string
	var body map[string]any
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"state":"ready"}`))
	}))
	defer s.Close()
	for _, tc := range []struct {
		kind         string
		args         []string
		method, path string
	}{
		{"prepare", []string{"create", "--id", "run", "--ttl", "5m"}, "POST", "/api/v1/tests/run/preparation"},
		{"prepare", []string{"start", "--id", "run", "--preparation-id", "p", "--revision", "abc"}, "POST", "/api/v1/tests/run/start"},
		{"prepare", []string{"renew", "--id", "run", "--preparation-id", "p", "--revision", "2"}, "POST", "/api/v1/tests/run/preparation"},
		{"analysis", []string{"mappings", "--project", "p"}, "GET", "/api/v1/analysis/mappings"},
		{"analysis", []string{"comparisons", "--project", "p"}, "GET", "/api/v1/analysis/comparisons"},
		{"monitor", []string{"dependencies", "--id", "run", "--at", "2026-01-01T00:00:00Z"}, "GET", "/api/v1/tests/run/dependency-evidence"},
		{"monitor", []string{"sources", "--project", "p"}, "GET", "/api/v1/projects/p/monitor-sources"},
		{"analysis", []string{"capabilities", "--project", "p"}, "GET", "/api/v1/analysis/capabilities"},
	} {
		var output bytes.Buffer
		args := append(tc.args, "--api", s.URL)
		if err := executeWorkflowCommand(context.Background(), tc.kind, args, &output); err != nil {
			t.Fatal(err)
		}
		if gotPath != tc.path || gotMethod != tc.method || !strings.Contains(output.String(), "ready") {
			t.Fatalf("request %s %s output %s", gotMethod, gotPath, output.String())
		}
		if tc.args[0] == "create" && body["ttl_seconds"] != float64(300) {
			t.Fatalf("ttl: %+v", body)
		}
	}
}

func TestWorkflowRejectsIncompleteCommandsWithoutNetwork(t *testing.T) {
	for _, tc := range []struct {
		kind string
		args []string
	}{{"prepare", nil}, {"prepare", []string{"start", "--id", "run"}}, {"analysis", []string{"create", "--project", "p"}}, {"monitor", []string{"sources"}}, {"monitor", []string{"dependencies", "--id", "run", "--at", "invalid"}}} {
		if err := executeWorkflowCommand(context.Background(), tc.kind, tc.args, &bytes.Buffer{}); err == nil {
			t.Fatalf("accepted %+v", tc)
		}
	}
}

func TestMonitoringControllerWorkflowParity(t *testing.T) {
	var calls []string
	var bodies []map[string]json.RawMessage
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.RequestURI())
		var body map[string]json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		w.Write([]byte(`{}`))
	}))
	defer api.Close()
	config := filepath.Join(t.TempDir(), "request.json")
	for _, tc := range []struct {
		args  []string
		body  string
		route string
	}{
		{[]string{"create-source", "--project", "p", "--file", config}, `{"name":"APM","revision":0}`, "POST /api/v1/projects/p/monitor-sources"},
		{[]string{"update-source", "--project", "p", "--id", "s", "--file", config}, `{"name":"APM","revision":2}`, "PUT /api/v1/projects/p/monitor-sources/s"},
		{[]string{"delete-source", "--project", "p", "--id", "s", "--revision", "2"}, `{}`, "DELETE /api/v1/projects/p/monitor-sources/s?revision=2"},
		{[]string{"series", "--id", "run", "--source-id", "s", "--metric", "calls", "--after-key", "cursor", "--limit", "50"}, `{}`, "GET /api/v1/tests/run/monitor-series?after_key=cursor&limit=50&metric=calls&source_id=s"},
		{[]string{"evidence", "--id", "run", "--source-id", "s", "--kind", "snapshot", "--after-key", "cursor"}, `{}`, "GET /api/v1/tests/run/monitor-evidence?after_key=cursor&kind=snapshot&limit=100&source_id=s"},
		{[]string{"series", "--id", "run", "--source-id", "s", "--metric", "calls", "--limit", "999"}, `{}`, "GET /api/v1/tests/run/monitor-series?limit=999&metric=calls&source_id=s"},
		{[]string{"create-run", "--project", "p", "--file", config}, `{"monitor_bindings":[],"goal_config":{"monitor_rules":[]}}`, "POST /api/v1/tests"},
	} {
		if err := os.WriteFile(config, []byte(tc.body), 0600); err != nil {
			t.Fatal(err)
		}
		if err := executeWorkflowCommand(context.Background(), "monitor", append(tc.args, "--api", api.URL), &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
		if calls[len(calls)-1] != tc.route {
			t.Fatalf("route %q want %q", calls[len(calls)-1], tc.route)
		}
	}
	if string(bodies[len(bodies)-1]["monitor_bindings"]) != "[]" {
		t.Fatal("explicit disable lost its distinction from inherited monitoring")
	}
	if err := os.WriteFile(config, []byte(`{"project_id":"other"}`), 0600); err != nil {
		t.Fatal(err)
	}
	before := len(calls)
	if err := executeWorkflowCommand(context.Background(), "monitor", []string{"create-run", "--project", "p", "--file", config, "--api", api.URL}, &bytes.Buffer{}); err == nil || len(calls) != before {
		t.Fatal("conflicting project sent to controller")
	}
	for _, args := range [][]string{
		{"series", "--limit", "0"}, {"series", "--limit", "1000"},
		{"evidence", "--limit", "0"}, {"evidence", "--limit", "101"},
	} {
		if err := executeWorkflowCommand(context.Background(), "monitor", append(args, "--id", "run", "--api", api.URL), &bytes.Buffer{}); err == nil || len(calls) != before {
			t.Fatalf("invalid bounded page sent to controller: %v", args)
		}
	}
}

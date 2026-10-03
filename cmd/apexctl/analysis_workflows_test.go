package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
		{"analysis", []string{"progress", "--id", "run", "--mode", "archive", "--group", "workers", "--source-id", "source"}, "GET", "/api/v1/tests/run/messaging-progress"},
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
	}{{"analysis", []string{"progress", "--id", "run", "--mode", "invalid"}}, {"analysis", []string{"progress"}}, {"prepare", nil}, {"prepare", []string{"start", "--id", "run"}}, {"analysis", []string{"create", "--project", "p"}}, {"monitor", []string{"sources"}}, {"monitor", []string{"dependencies", "--id", "run", "--at", "invalid"}}} {
		if err := executeWorkflowCommand(context.Background(), tc.kind, tc.args, &bytes.Buffer{}); err == nil {
			t.Fatalf("accepted %+v", tc)
		}
	}
}

func TestMonitorManagementQueryPreservesExactValues(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/tests/run/monitor-management" || r.URL.Query().Get("source_id") != "source" || r.URL.Query().Get("limit") != "3" || r.URL.Query().Get("cursor") != "opaque" {
			t.Errorf("unexpected query %s", r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"records":[{"value":"9007199254740993"},{"value":"0"}],"has_more":false}`))
	}))
	defer server.Close()
	var output bytes.Buffer
	if err := executeWorkflowCommand(context.Background(), "monitor", []string{"management", "--id", "run", "--source-id", "source", "--limit", "3", "--cursor", "opaque", "--from", "2026-10-01T00:00:00Z", "--to", "2026-10-02T00:00:00Z", "--api", server.URL}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"9007199254740993"`) || !strings.Contains(output.String(), `"0"`) {
		t.Fatalf("exact values lost: %s", output.String())
	}
	for _, args := range [][]string{{"management"}, {"management", "--id", "run", "--limit", "101"}, {"management", "--id", "run", "--from", "invalid"}, {"management", "--id", "run", "--from", "2026-10-02T00:00:00Z", "--to", "2026-10-01T00:00:00Z"}} {
		if err := executeWorkflowCommand(context.Background(), "monitor", args, &bytes.Buffer{}); err == nil {
			t.Fatalf("accepted invalid query %v", args)
		}
	}
}

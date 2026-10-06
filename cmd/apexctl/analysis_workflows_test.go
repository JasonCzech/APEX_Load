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
	"sync/atomic"
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

func TestMonitorSourceRevisionedCRUD(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/api/v1/projects/project/monitor-sources/source" {
			t.Errorf("source path: %s", r.URL.Path)
		}
		if r.Method == http.MethodDelete {
			if (r.URL.Query().Get("revision") != "7" && r.URL.Query().Get("revision") != "4611686018427387904") || r.ContentLength > 0 {
				t.Error("delete lost revision or unexpectedly supplied a body")
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var source struct {
			Revision int64 `json:"revision"`
			Identity struct {
				CredentialRef string `json:"credential_ref"`
			} `json:"management_identity"`
		}
		if r.Method != http.MethodPut || json.NewDecoder(r.Body).Decode(&source) != nil || source.Revision != 7 || source.Identity.CredentialRef != "management-secret" {
			t.Error("update lost exact source revision or separate management credential policy")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"revision":8,"management_identity":{"credential_version":"opaque-pin"}}`))
	}))
	defer server.Close()
	file := filepath.Join(t.TempDir(), "source.json")
	if err := os.WriteFile(file, []byte(`{"revision":7,"management_identity":{"credential_ref":"management-secret"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := executeWorkflowCommand(context.Background(), "monitor", []string{"update-source", "--project", "project", "--id", "source", "--file", file, "--api", server.URL}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "opaque-pin") {
		t.Error("controller-generated identity pin missing from CLI output")
	}
	if err := executeWorkflowCommand(context.Background(), "monitor", []string{"delete-source", "--project", "project", "--id", "source", "--revision", "7", "--api", server.URL}, &output); err != nil {
		t.Fatal(err)
	}
	if err := executeWorkflowCommand(context.Background(), "monitor", []string{"delete-source", "--project", "project", "--id", "source", "--revision", "4611686018427387904", "--api", server.URL}, &output); err != nil {
		t.Fatalf("final stored source revision cannot be deleted: %v", err)
	}
	for _, args := range [][]string{{"delete-source", "--project", "project", "--id", "source"}, {"delete-source", "--project", "project", "--id", "source", "--revision", "-1"}, {"delete-source", "--project", "project", "--id", "source", "--revision", "1.5"}, {"update-source", "--project", "project", "--id", "source"}, {"update-source", "--id", "source", "--file", file}} {
		if err := executeWorkflowCommand(context.Background(), "monitor", append(args, "--api", server.URL), &output); err == nil {
			t.Errorf("invalid source command admitted: %v", args)
		}
	}
	if calls.Load() != 3 {
		t.Errorf("invalid command used the network: calls=%d", calls.Load())
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
		{[]string{"evidence", "--id", "run", "--source-id", "prom", "--kind", "target", "--after-key", "cursor", "--limit", "50"}, `{}`, "GET /api/v1/tests/run/monitor-evidence?after_key=cursor&kind=target&limit=50&source_id=prom"},
		{[]string{"evidence", "--id", "run", "--source-id", "prom", "--kind", "alert"}, `{}`, "GET /api/v1/tests/run/monitor-evidence?kind=alert&limit=100&source_id=prom"},
		{[]string{"evidence", "--id", "run", "--source-id", "dyn", "--kind", "problem"}, `{}`, "GET /api/v1/tests/run/monitor-evidence?kind=problem&limit=100&source_id=dyn"},
		{[]string{"evidence", "--id", "run", "--source-id", "dyn", "--kind", "span"}, `{}`, "GET /api/v1/tests/run/monitor-evidence?kind=span&limit=100&source_id=dyn"},
		{[]string{"evidence", "--id", "run", "--source-id", "dyn", "--kind", "trace"}, `{}`, "GET /api/v1/tests/run/monitor-evidence?kind=trace&limit=100&source_id=dyn"},
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
		{"evidence", "--kind", "unknown"},
		{"evidence", "--kind", "topology"},
	} {
		if err := executeWorkflowCommand(context.Background(), "monitor", append(args, "--id", "run", "--api", api.URL), &bytes.Buffer{}); err == nil || len(calls) != before {
			t.Fatalf("invalid bounded page sent to controller: %v", args)
		}
	}
	var help bytes.Buffer
	if err := executeWorkflowCommand(context.Background(), "monitor", []string{"evidence", "--help"}, &help); err != nil || !strings.Contains(help.String(), "target or alert") || !strings.Contains(help.String(), "problem, span and trace") {
		t.Fatal("Prometheus evidence filters absent from help", help.String(), err)
	}
}

func TestMonitorMergedCommandsPreserveDistinctDefaultPageBounds(t *testing.T) {
	for _, tc := range []struct{ action, wantLimit string }{{"management", "20"}, {"series", "100"}, {"evidence", "100"}} {
		t.Run(tc.action, func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Query().Get("limit") != tc.wantLimit {
					t.Errorf("limit=%q, want %q", r.URL.Query().Get("limit"), tc.wantLimit)
				}
				if r.URL.Query().Get("source_id") != "source" {
					t.Error("source filter lost")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{}`))
			}))
			defer server.Close()
			if err := executeWorkflowCommand(context.Background(), "monitor", []string{tc.action, "--id", "run", "--source-id", "source", "--api", server.URL}, &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 1 {
				t.Fatalf("calls=%d", calls.Load())
			}
		})
	}
}

func TestRCAWorkflowCommands(t *testing.T) {
	var gotPath, gotMethod string
	var body map[string]any
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod, body = r.URL.Path, r.Method, nil
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"state":"queued"}`))
	}))
	defer s.Close()
	for _, tc := range []struct {
		args         []string
		method, path string
	}{
		{[]string{"analyze", "--id", "run", "--enrich", "--baseline-id", "base"}, "POST", "/api/v1/tests/run/archive/root-cause/analyses"},
		{[]string{"status", "--id", "run", "--analysis-id", "a1"}, "GET", "/api/v1/tests/run/archive/root-cause/analyses/a1"},
		{[]string{"show", "--id", "run"}, "GET", "/api/v1/tests/run/archive/root-cause/v2"},
		{[]string{"bundle", "--id", "run", "--analysis-id", "a1"}, "GET", "/api/v1/tests/run/archive/root-cause/analyses/a1/bundle"},
	} {
		var output bytes.Buffer
		if err := executeWorkflowCommand(context.Background(), "rca", append(tc.args, "--api", s.URL), &output); err != nil {
			t.Fatal(err)
		}
		if gotPath != tc.path || gotMethod != tc.method || !strings.Contains(output.String(), "queued") {
			t.Fatalf("%v: request %s %s", tc.args, gotMethod, gotPath)
		}
		if tc.args[0] == "analyze" && (body["enrich"] != true || body["baseline_test_id"] != "base") {
			t.Fatalf("analyze body %+v", body)
		}
	}
	for _, args := range [][]string{{"show"}, {"status", "--id", "run"}, {"retry", "--id", "run"}} {
		if err := executeWorkflowCommand(context.Background(), "rca", append(args, "--api", s.URL), &bytes.Buffer{}); err == nil {
			t.Fatalf("%v accepted", args)
		}
	}
}

func TestMonitoringEvidenceFiltersAndBounds(t *testing.T) {
	var calls int
	var gotQuery map[string][]string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		gotQuery = r.URL.Query()
		_, _ = w.Write([]byte(`{}`))
	}))
	defer api.Close()
	for _, kind := range []string{"event", "violation", "snapshot", "log", "transaction", "error", "span", "health", "target", "alert", "problem", "trace"} {
		t.Run(kind, func(t *testing.T) {
			args := []string{"evidence", "--id", "run", "--api", api.URL, "--kind", kind, "--service", "checkout / payments", "--severity", "error", "--trace-id", "trace+id", "--from", "2026-10-06T10:00:00.123456789Z", "--until", "2026-10-06T10:01:00Z"}
			if err := executeWorkflowCommand(context.Background(), "monitor", args, &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
			for key, want := range map[string]string{"kind": kind, "service": "checkout / payments", "severity": "error", "trace_id": "trace+id", "from": "2026-10-06T10:00:00.123456789Z", "until": "2026-10-06T10:01:00Z", "limit": "100"} {
				if values := gotQuery[key]; len(values) != 1 || values[0] != want {
					t.Fatalf("%s: got %v, want %q", key, values, want)
				}
			}
		})
	}
	for _, invalid := range [][]string{
		{"--limit", "101"}, {"--limit", "0"}, {"--kind", "unsupported"},
		{"--from", "2026-10-06T10:00:00+00:00"}, {"--from", "2026-02-30T10:00:00Z"},
		{"--from", "2026-10-06T10:00:00Z", "--until", "2026-10-06T10:00:00Z"},
		{"--from", "2026-10-06T10:01:00Z", "--until", "2026-10-06T10:00:00Z"},
		{"--service", strings.Repeat("x", 513)}, {"--severity", "error\nwarning"},
		{"--source-id", strings.Repeat("s", 257)}, {"--after-key", strings.Repeat("c", 65)},
	} {
		before := calls
		args := append([]string{"evidence", "--id", "run", "--api", api.URL}, invalid...)
		if err := executeWorkflowCommand(context.Background(), "monitor", args, &bytes.Buffer{}); err == nil || calls != before {
			t.Fatalf("invalid filters reached controller: %v", invalid)
		}
	}
	if err := executeWorkflowCommand(context.Background(), "monitor", []string{"series", "--id", "run", "--api", api.URL, "--limit", "999"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("series ceiling changed: %v", err)
	}
}

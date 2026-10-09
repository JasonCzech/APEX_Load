package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type recordedCLIRequest struct {
	Method, Path, Query string
	Body                map[string]any
}

func browserIntelligenceCLIServer(t *testing.T, reply string) (*httptest.Server, *[]recordedCLIRequest) {
	t.Helper()
	var requests []recordedCLIRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := recordedCLIRequest{Method: r.Method, Path: r.URL.EscapedPath(), Query: r.URL.RawQuery}
		if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
			_ = json.Unmarshal(raw, &request.Body)
		}
		requests = append(requests, request)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(server.Close)
	return server, &requests
}

func TestAnalysisInteractionsAndHAREntryCommands(t *testing.T) {
	server, requests := browserIntelligenceCLIServer(t, `{"status":"ready"}`)
	var out bytes.Buffer
	if err := executeWorkflowCommand(context.Background(), "analysis", []string{"interactions", "--api", server.URL, "--run", "run 1"}, &out); err != nil {
		t.Fatal(err)
	}
	if err := executeWorkflowCommand(context.Background(), "analysis", []string{"har-entry", "--api", server.URL, "--run", "run-1", "--request-id", "gen-request&7"}, &out); err != nil {
		t.Fatal(err)
	}
	got := *requests
	if len(got) != 2 || got[0].Path != "/api/v1/tests/run%201/archive/browser-interactions" || got[1].Path != "/api/v1/tests/run-1/archive/browser/har-entry" || got[1].Query != "request_id=gen-request%267" {
		t.Fatalf("requests %+v", got)
	}
	if !strings.Contains(out.String(), `"status": "ready"`) {
		t.Fatalf("output %s", out.String())
	}
	for name, args := range map[string][]string{
		"interactions without run":    {"interactions", "--api", server.URL},
		"har-entry without request":   {"har-entry", "--api", server.URL, "--run", "run-1"},
		"interactions positional arg": {"interactions", "--api", server.URL, "--run", "run-1", "extra"},
	} {
		if err := executeWorkflowCommand(context.Background(), "analysis", args, &out); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
}

func TestAnalysisPoliciesCommandsUseRevisionedSaves(t *testing.T) {
	server, requests := browserIntelligenceCLIServer(t, `{"id":"policy-1","revision":4}`)
	definition := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(definition, []byte(`{"version":1,"metrics":[{"metric":"lcp","good":2500,"poor":4000,"weight":1}],"baseline":{"mode":"none"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) error {
		var out bytes.Buffer
		return executeWorkflowCommand(context.Background(), "analysis", append([]string{"policies"}, append(args, "--api", server.URL)...), &out)
	}
	for _, args := range [][]string{
		{"list", "--project", "p1", "--kind", "browser_policy"},
		{"get", "--project", "p1", "--id", "policy-1", "--revision", "2"},
		{"save", "--project", "p1", "--kind", "browser_policy", "--name", "Vitals", "--file", definition},
		{"save", "--project", "p1", "--kind", "browser_policy", "--name", "Vitals", "--file", definition, "--id", "policy-1", "--revision", "3"},
		{"delete", "--project", "p1", "--id", "policy-1", "--revision", "4"},
	} {
		if err := run(args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	got := *requests
	want := []struct{ method, path, query string }{
		{http.MethodGet, "/api/v1/analysis/policies", "kind=browser_policy&project_id=p1"},
		{http.MethodGet, "/api/v1/analysis/policies/policy-1", "project_id=p1&revision=2"},
		{http.MethodPost, "/api/v1/analysis/policies", "project_id=p1"},
		{http.MethodPut, "/api/v1/analysis/policies/policy-1", "project_id=p1"},
		{http.MethodDelete, "/api/v1/analysis/policies/policy-1", "project_id=p1&revision=4"},
	}
	if len(got) != len(want) {
		t.Fatalf("requests %+v", got)
	}
	for index, expected := range want {
		if got[index].Method != expected.method || got[index].Path != expected.path || got[index].Query != expected.query {
			t.Fatalf("request %d = %+v, want %+v", index, got[index], expected)
		}
	}
	if got[2].Body["kind"] != "browser_policy" || got[2].Body["name"] != "Vitals" || got[2].Body["revision"] != nil || got[2].Body["definition"] == nil {
		t.Fatalf("create body %+v", got[2].Body)
	}
	if got[3].Body["revision"] != float64(3) {
		t.Fatalf("update body %+v", got[3].Body)
	}
	for name, args := range map[string][]string{
		"no project":              {"list", "--kind", "browser_policy"},
		"unknown kind":            {"list", "--project", "p1", "--kind", "chart"},
		"update without revision": {"save", "--project", "p1", "--kind", "browser_policy", "--name", "V", "--file", definition, "--id", "policy-1"},
		"revision without id":     {"save", "--project", "p1", "--kind", "browser_policy", "--name", "V", "--file", definition, "--revision", "2"},
		"delete without revision": {"delete", "--project", "p1", "--id", "policy-1"},
		"unknown verb":            {"rename", "--project", "p1"},
	} {
		before := len(*requests)
		if err := run(args...); err == nil {
			t.Fatalf("%s: accepted", name)
		}
		if len(*requests) != before {
			t.Fatalf("%s: sent a request", name)
		}
	}
}

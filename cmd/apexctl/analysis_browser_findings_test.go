package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAnalysisBrowserFindingsEvaluatesWaitsAndPrints(t *testing.T) {
	previous := browserFindingsPollInterval
	browserFindingsPollInterval = time.Millisecond
	t.Cleanup(func() { browserFindingsPollInterval = previous })
	var polls atomic.Int32
	var created map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/analysis/evaluations" && r.URL.Query().Get("project_id") == "proj":
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &created)
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"id":"job-1","state":"queued"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/analysis/evaluations/job-1":
			if polls.Add(1) < 2 {
				_, _ = w.Write([]byte(`{"id":"job-1","state":"running"}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":"job-1","state":"ready","result":{"policy":{"revision":2,"source":"requested"}}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/tests/run-1/archive/browser-findings":
			_, _ = w.Write([]byte(`{"run_id":"run-1","status":"ready","result":{"score":{"score":62.5}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	var out bytes.Buffer
	args := []string{"--api", server.URL, "--run", "run-1", "--evaluate", "--project", "proj", "--policy", "browser", "--revision", "2", "--wait"}
	if err := executeAnalysisBrowserFindingsCommand(context.Background(), args, &out); err != nil {
		t.Fatal(err)
	}
	request, _ := created["request"].(map[string]any)
	if created["kind"] != "browser_intelligence" || request["run"] != "run-1" || request["policy"].(map[string]any)["revision"] != float64(2) || polls.Load() != 2 {
		t.Fatalf("created %+v polls %d", created, polls.Load())
	}
	// A named revision's evaluation is printed itself, not the run's result.
	if !strings.Contains(out.String(), `"source": "requested"`) || strings.Contains(out.String(), `"score": 62.5`) {
		t.Fatalf("output %s", out.String())
	}
	out.Reset()
	if err := executeAnalysisBrowserFindingsCommand(context.Background(), []string{"--api", server.URL, "--run", "run-1", "--evaluate", "--project", "proj", "--wait"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"score": 62.5`) {
		t.Fatalf("pinned output %s", out.String())
	}
	out.Reset()
	if err := executeWorkflowCommand(context.Background(), "analysis", []string{"browser-findings", "--api", server.URL, "--run", "run-1"}, &out); err != nil || !strings.Contains(out.String(), `"status": "ready"`) {
		t.Fatalf("dispatch %v %s", err, out.String())
	}
	for name, bad := range map[string][]string{
		"no run":                  {"--api", server.URL},
		"policy without eval":     {"--api", server.URL, "--run", "r", "--policy", "p", "--revision", "1"},
		"eval without project":    {"--api", server.URL, "--run", "r", "--evaluate"},
		"policy without revision": {"--api", server.URL, "--run", "r", "--evaluate", "--project", "p", "--policy", "p"},
		"positional":              {"--api", server.URL, "--run", "r", "x"},
	} {
		if err := executeAnalysisBrowserFindingsCommand(context.Background(), bad, &out); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

func TestAnalysisBrowserFindingsFailsUnlessReady(t *testing.T) {
	previous := browserFindingsPollInterval
	browserFindingsPollInterval = time.Millisecond
	t.Cleanup(func() { browserFindingsPollInterval = previous })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"id":"job-2","state":"queued"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"job-2","state":"failed","failure":"run r pinned no browser_policy; name one"}`))
	}))
	t.Cleanup(server.Close)
	var out bytes.Buffer
	err := executeAnalysisBrowserFindingsCommand(context.Background(), []string{"--api", server.URL, "--run", "r", "--evaluate", "--project", "p", "--wait"}, &out)
	if err == nil || !strings.Contains(err.Error(), "ended failed") || !strings.Contains(err.Error(), "pinned no browser_policy") {
		t.Fatalf("err %v", err)
	}
}

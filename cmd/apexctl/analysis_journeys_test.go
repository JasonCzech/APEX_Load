package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAnalysisJourneysPrintsTheReport(t *testing.T) {
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/tests/run-1/archive/journeys" {
			http.NotFound(w, r)
			return
		}
		queries = append(queries, r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"availability":"available","weighted_failure_index":0.25}`))
	}))
	t.Cleanup(server.Close)
	var out bytes.Buffer
	if err := executeAnalysisJourneysCommand(context.Background(), []string{"--api", server.URL, "--run", "run-1"}, &out); err != nil {
		t.Fatal(err)
	}
	if err := executeAnalysisJourneysCommand(context.Background(), []string{"--api", server.URL, "--run", "run-1", "--policy", "checkout", "--revision", "3"}, &out); err != nil {
		t.Fatal(err)
	}
	if len(queries) != 2 || queries[0] != "" || queries[1] != "policy=checkout&revision=3" {
		t.Fatalf("queries %q", queries)
	}
	if !strings.Contains(out.String(), `"weighted_failure_index": 0.25`) {
		t.Fatalf("output %s", out.String())
	}
	for name, args := range map[string][]string{
		"no run":              {"--api", server.URL},
		"revision, no policy": {"--api", server.URL, "--run", "run-1", "--revision", "2"},
		"negative revision":   {"--api", server.URL, "--run", "run-1", "--policy", "p", "--revision", "-1"},
		"positional":          {"--api", server.URL, "--run", "run-1", "extra"},
	} {
		if err := executeAnalysisJourneysCommand(context.Background(), args, &out); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
	// Dispatch through the analysis workflow command.
	out.Reset()
	if err := executeWorkflowCommand(context.Background(), "analysis", []string{"journeys", "--api", server.URL, "--run", "run-1"}, &out); err != nil || !strings.Contains(out.String(), "available") {
		t.Fatalf("dispatch: %v %s", err, out.String())
	}
}

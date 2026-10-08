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

func TestTelemetryReevaluateQueuesAndWaits(t *testing.T) {
	previous := telemetryReevaluatePollInterval
	telemetryReevaluatePollInterval = time.Millisecond
	t.Cleanup(func() { telemetryReevaluatePollInterval = previous })
	var polls atomic.Int32
	var created map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("project_id") != "proj" {
			http.Error(w, `{"error":"project"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/analysis/evaluations":
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &created)
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"id":"job-1","state":"queued"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/analysis/evaluations/job-1":
			if polls.Add(1) < 2 {
				_, _ = w.Write([]byte(`{"id":"job-1","state":"running"}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":"job-1","state":"ready","result":{"all_answerable":true}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	var out bytes.Buffer
	args := []string{"--api", server.URL, "--project", "proj", "--runs", "run-a, run-b", "--policy", "common", "--revision", "2", "--wait"}
	if err := executeTelemetryReevaluateCommand(context.Background(), args, &out); err != nil {
		t.Fatal(err)
	}
	request, _ := created["request"].(map[string]any)
	if created["kind"] != "telemetry_acceptance" || len(request["runs"].([]any)) != 2 || request["policy"].(map[string]any)["revision"] != float64(2) {
		t.Fatalf("created %+v", created)
	}
	if !strings.Contains(out.String(), `"state": "ready"`) || !strings.Contains(out.String(), `"all_answerable": true`) || polls.Load() != 2 {
		t.Fatalf("output %s (polls %d)", out.String(), polls.Load())
	}
}

func TestTelemetryReevaluateFailsUnlessReadyAndValidatesFlags(t *testing.T) {
	previous := telemetryReevaluatePollInterval
	telemetryReevaluatePollInterval = time.Millisecond
	t.Cleanup(func() { telemetryReevaluatePollInterval = previous })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"id":"job-2","state":"queued"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"job-2","state":"failed","failure":"run run-a has no finalized archive"}`))
	}))
	t.Cleanup(server.Close)
	var out bytes.Buffer
	err := executeTelemetryReevaluateCommand(context.Background(), []string{"--api", server.URL, "--project", "proj", "--runs", "run-a", "--policy", "p", "--revision", "1", "--wait"}, &out)
	if err == nil || !strings.Contains(err.Error(), "ended failed") || !strings.Contains(out.String(), "no finalized archive") {
		t.Fatalf("failed job: %v (%s)", err, out.String())
	}
	for name, args := range map[string][]string{
		"no revision": {"--project", "proj", "--runs", "a", "--policy", "p"},
		"no runs":     {"--project", "proj", "--policy", "p", "--revision", "1"},
		"too many":    {"--project", "proj", "--runs", "a,b,c,d,e,f,g,h,i,j,k", "--policy", "p", "--revision", "1"},
		"no project":  {"--runs", "a", "--policy", "p", "--revision", "1"},
		"positional":  {"--project", "proj", "--runs", "a", "--policy", "p", "--revision", "1", "extra"},
	} {
		if err := executeTelemetryReevaluateCommand(context.Background(), append([]string{"--api", server.URL}, args...), &out); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}

package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCIRootCauseReachesTheSummaryAndOutputs(t *testing.T) {
	dir := t.TempDir()
	args := defaultCIArgs()
	args.jsonPath = filepath.Join(dir, "result.json")
	args.junitPath = filepath.Join(dir, "result.xml")
	args.githubOutput = filepath.Join(dir, "github-output")
	args.githubSummary = filepath.Join(dir, "summary.md")
	result := CIResult{SchemaVersion: ciSchemaVersion, Outcome: ciOutcomePassed, Status: "COMPLETE",
		Validity:  &CIRunValidity{Verdict: "suspect"},
		RootCause: &CIRunRootCause{State: "completed", Outcome: "supported", TopHypothesis: "Pool exhaustion", Confidence: "high"}}
	if err := finalizeCIResult(args, result); err != nil {
		t.Fatal(err)
	}
	summary, _ := os.ReadFile(args.githubSummary)
	if !strings.Contains(string(summary), "| Root cause | Pool exhaustion (high confidence) |") {
		t.Fatalf("summary:\n%s", summary)
	}
	parsed := parseGitHubEnvironmentFile(t, mustReadFile(t, args.githubOutput))
	if parsed["validity"] != "suspect" || parsed["root-cause"] != "Pool exhaustion (high confidence)" {
		t.Fatalf("outputs %+v", parsed)
	}
	// Without a root cause or validity the outputs are present and empty.
	args.githubOutput = filepath.Join(dir, "github-output-empty")
	if err := finalizeCIResult(args, CIResult{SchemaVersion: ciSchemaVersion, Outcome: ciOutcomePassed, Status: "COMPLETE"}); err != nil {
		t.Fatal(err)
	}
	empty := parseGitHubEnvironmentFile(t, mustReadFile(t, args.githubOutput))
	if value, ok := empty["root-cause"]; !ok || value != "" {
		t.Fatalf("empty root-cause output %q present=%v", value, ok)
	}
}

// TestCIRootCauseTextMatchesTheCheck keeps the job summary's and outputs'
// line equal to the controller's GitHub Check row (pkg/api ciRootCauseText,
// whose test holds the same table).
func TestCIRootCauseTextMatchesTheCheck(t *testing.T) {
	for _, tc := range []struct {
		root *CIRunRootCause
		want string
	}{
		{nil, ""},
		{&CIRunRootCause{State: "completed", Outcome: "supported", TopHypothesis: "Pool exhaustion", Confidence: "high"}, "Pool exhaustion (high confidence)"},
		{&CIRunRootCause{State: "completed", Outcome: "no_supported_hypothesis"}, "no cause supported"},
		{&CIRunRootCause{State: "completed", Outcome: "insufficient_evidence"}, "insufficient evidence"},
		{&CIRunRootCause{State: "none"}, "not analyzed"},
		{&CIRunRootCause{State: "none", AutoAnalyze: true}, "analysis pending"},
		{&CIRunRootCause{State: "queued"}, "analysis queued"},
	} {
		if got := tc.root.text(); got != tc.want {
			t.Errorf("text(%+v) = %q, want %q", tc.root, got, tc.want)
		}
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestWaitForCIRootCausePollsUntilTheAnalysisEnds(t *testing.T) {
	oldPoll := ciPollInterval
	ciPollInterval = time.Millisecond
	t.Cleanup(func() { ciPollInterval = oldPoll })
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch calls.Add(1) {
		case 1:
			_, _ = io.WriteString(w, `{"schema_version":1,"test_id":"run","status":"COMPLETE","outcome":"passed","root_cause":{"state":"running"}}`)
		default:
			_, _ = io.WriteString(w, `{"schema_version":1,"test_id":"run","status":"COMPLETE","outcome":"passed","root_cause":{"state":"completed","outcome":"no_supported_hypothesis"}}`)
		}
	}))
	defer server.Close()
	client := NewApiClientWithAuth(server.URL, "key", "")
	start := &CIRunResponse{TestID: "run", RootCause: &CIRunRootCause{State: "none", AutoAnalyze: true}}
	got := waitForCIRootCause(context.Background(), &ciAuthSession{mode: "api-key"}, client, start, time.Minute)
	if got == nil || got.State != "completed" || got.text() != "no cause supported" || calls.Load() != 2 {
		t.Fatalf("root cause %+v after %d polls", got, calls.Load())
	}
	// A settled root cause is not polled; a project that does not analyze
	// automatically has nothing to wait for.
	calls.Store(0)
	for _, settled := range []*CIRunRootCause{nil, {State: "completed", Outcome: "supported"}, {State: "none"}, {State: "failed"}} {
		if waitForCIRootCause(context.Background(), &ciAuthSession{mode: "api-key"}, client, &CIRunResponse{TestID: "run", RootCause: settled}, time.Minute); calls.Load() != 0 {
			t.Fatalf("polled a settled root cause %+v", settled)
		}
	}
	// The wait ends at its deadline with the last state seen.
	stuck := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"schema_version":1,"test_id":"run","status":"COMPLETE","root_cause":{"state":"queued"}}`)
	}))
	defer stuck.Close()
	got = waitForCIRootCause(context.Background(), &ciAuthSession{mode: "api-key"}, NewApiClientWithAuth(stuck.URL, "key", ""), &CIRunResponse{TestID: "run", RootCause: &CIRunRootCause{State: "queued"}}, 50*time.Millisecond)
	if got == nil || got.State != "queued" || got.text() != "analysis queued" {
		t.Fatalf("deadline root cause %+v", got)
	}
}

func TestParseCIArgsBoundsWaitAnalysis(t *testing.T) {
	base := []string{"--script-id", "script", "--project-id", "p", "--controller-url", "https://controller.example"}
	if args, err := parseCIArgs(append(base, "--wait-analysis", "5m")); err != nil || args.waitAnalysis != 5*time.Minute {
		t.Fatalf("5m: %+v %v", args.waitAnalysis, err)
	}
	if _, err := parseCIArgs(append(base, "--wait-analysis", "11m")); err == nil {
		t.Fatal("an 11m wait was accepted")
	}
}

func TestRCAWaitPollsTheAnalysisUntilItEnds(t *testing.T) {
	oldPoll := rcaWaitPoll
	rcaWaitPoll = time.Millisecond
	t.Cleanup(func() { rcaWaitPoll = oldPoll })
	var paths []string
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch calls.Add(1) {
		case 1:
			_, _ = io.WriteString(w, `{"analysis_id":"a1","state":"queued"}`)
		case 2:
			_, _ = io.WriteString(w, `{"analysis_id":"a1","state":"running"}`)
		default:
			_, _ = io.WriteString(w, `{"analysis_id":"a1","state":"completed","assessment":{}}`)
		}
	}))
	defer server.Close()
	var output bytes.Buffer
	if err := executeWorkflowCommand(context.Background(), "rca", []string{"analyze", "--id", "run", "--wait", "1m", "--api", server.URL}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"completed"`) || len(paths) != 3 || paths[0] != "POST /api/v1/tests/run/archive/root-cause/analyses" || paths[2] != "GET /api/v1/tests/run/archive/root-cause/analyses/a1" {
		t.Fatalf("output %s paths %v", output.String(), paths)
	}
	// show waits while an automatic analysis is expected.
	calls.Store(0)
	paths = nil
	auto := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if calls.Add(1) == 1 {
			_, _ = io.WriteString(w, `{"state":"none","auto_analyze":true}`)
			return
		}
		_, _ = io.WriteString(w, `{"analysis_id":"a2","state":"completed"}`)
	}))
	defer auto.Close()
	output.Reset()
	if err := executeWorkflowCommand(context.Background(), "rca", []string{"show", "--id", "run", "--wait", "1m", "--api", auto.URL}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "a2") || len(paths) != 2 {
		t.Fatalf("show --wait output %s paths %v", output.String(), paths)
	}
	for _, args := range [][]string{{"bundle", "--id", "run", "--analysis-id", "a1", "--wait", "1m"}, {"show", "--id", "run", "--wait", "11m"}} {
		if err := executeWorkflowCommand(context.Background(), "rca", append(args, "--api", server.URL), &bytes.Buffer{}); err == nil {
			t.Fatalf("%v accepted", args)
		}
	}
}

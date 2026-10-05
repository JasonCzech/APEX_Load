package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/rca"
)

// writeRCACorpus writes a two-trial corpus: a CPU fault recovered first and
// a quiet healthy control, one saved bare and one as an analysis response.
func writeRCACorpus(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	start := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	manifest := rca.EvalManifest{Schema: rca.EvalManifestSchema, Trials: []rca.EvalTrial{
		{ID: "cpu-1", Seed: "seed-cpu-1", Class: "cpu_stress", RunID: "run-cpu", BaselineRunID: "run-base", FaultStart: &start, ExpectedSignature: "resource_pressure", Assessment: "assessments/cpu-1.json"},
		{ID: "ok-1", Seed: "seed-ok-1", Class: rca.HealthyClass, RunID: "run-ok", Assessment: "assessments/ok-1.json"},
	}}
	write := func(name string, value any) {
		data, _ := json.Marshal(value)
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("manifest.json", manifest)
	recovered := rca.Assessment{SchemaVersion: rca.AssessmentSchemaVersion, Outcome: rca.OutcomeSupported, Inputs: rca.InputDigests{Engine: "e1"},
		Anomalies:  []rca.Anomaly{{ID: "cause", Onset: start.Add(10 * time.Second)}},
		Hypotheses: []rca.Hypothesis{{ID: "h", Signature: "resource_pressure", Rank: 1, Confidence: rca.ConfidenceHigh, Chain: []rca.ChainStep{{From: "cause", To: "s"}}}}}
	write("assessments/cpu-1.json", recovered)
	write("assessments/ok-1.json", map[string]any{"state": "completed", "assessment": rca.Assessment{SchemaVersion: rca.AssessmentSchemaVersion, Outcome: rca.OutcomeNoSupportedHypothesis, Inputs: rca.InputDigests{Engine: "e1"}}})
	return dir
}

func TestRCAEvalScoresACorpusDeterministically(t *testing.T) {
	corpus := writeRCACorpus(t)
	var first, second bytes.Buffer
	if err := executeWorkflowCommand(context.Background(), "rca", []string{"eval", "--corpus", corpus, "--split", "all"}, &first); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "scores.json")
	if err := executeWorkflowCommand(context.Background(), "rca", []string{"eval", "--corpus", corpus, "--split", "all", "--out", out}, &second); err != nil {
		t.Fatal(err)
	}
	saved, _ := os.ReadFile(out)
	if first.String() != second.String() || string(saved) != second.String() {
		t.Fatal("scores differ between runs")
	}
	var scores rca.EvalScores
	if err := json.Unmarshal(first.Bytes(), &scores); err != nil || scores.Top1.Successes != 1 || scores.FalseAttribution.Successes != 0 || scores.CorpusReady {
		t.Fatalf("%v %+v", err, scores)
	}
	// The release gate is judged on the held-out split only.
	if err := executeWorkflowCommand(context.Background(), "rca", []string{"eval", "--corpus", corpus, "--split", "all", "--require-bars"}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "held_out") {
		t.Fatalf("require bars on all: %v", err)
	}
	// Two trials are far short of the corpus minimums, so the bars fail.
	err := executeWorkflowCommand(context.Background(), "rca", []string{"eval", "--corpus", corpus, "--split", "held_out", "--require-bars"}, &bytes.Buffer{})
	var verdict *verdictError
	if !errors.As(err, &verdict) || verdict.code != exitCodeTestFailure {
		t.Fatalf("require bars: %v", err)
	}
}

func TestRCAEvalRefusesPathsOutsideTheCorpus(t *testing.T) {
	corpus := writeRCACorpus(t)
	manifest, _ := os.ReadFile(filepath.Join(corpus, "manifest.json"))
	escaped := strings.Replace(string(manifest), "assessments/cpu-1.json", "../outside.json", 1)
	if err := os.WriteFile(filepath.Join(corpus, "manifest.json"), []byte(escaped), 0o644); err != nil {
		t.Fatal(err)
	}
	err := executeWorkflowCommand(context.Background(), "rca", []string{"eval", "--corpus", corpus, "--split", "all"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "leaves the corpus") {
		t.Fatalf("%v", err)
	}
}

func TestRCAEvalReplaysTrialsThroughTheController(t *testing.T) {
	corpus := writeRCACorpus(t)
	previous := rcaEvalPollInterval
	rcaEvalPollInterval = time.Millisecond
	defer func() { rcaEvalPollInterval = previous }()
	var polls atomic.Int32
	baselines := map[string]string{}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			var request map[string]string
			_ = json.NewDecoder(r.Body).Decode(&request)
			baselines[r.URL.Path] = request["baseline_test_id"]
			w.Write([]byte(`{"analysis_id":"a1","state":"queued"}`))
			return
		}
		// The first poll finds it still running.
		if polls.Add(1)%2 == 1 {
			w.Write([]byte(`{"analysis_id":"a1","state":"running"}`))
			return
		}
		response, _ := json.Marshal(map[string]any{"analysis_id": "a1", "state": "completed", "assessment": rca.Assessment{SchemaVersion: rca.AssessmentSchemaVersion, Outcome: rca.OutcomeNoSupportedHypothesis, Inputs: rca.InputDigests{Engine: "e2"}}})
		w.Write(response)
	}))
	defer s.Close()
	var output bytes.Buffer
	if err := executeWorkflowCommand(context.Background(), "rca", []string{"eval", "--corpus", corpus, "--split", "all", "--replay", "--api", s.URL}, &output); err != nil {
		t.Fatal(err)
	}
	var scores rca.EvalScores
	if err := json.Unmarshal(output.Bytes(), &scores); err != nil || len(scores.Engines) != 1 || scores.Engines[0] != "e2" || scores.Top1.Successes != 0 {
		t.Fatalf("%v %+v", err, scores)
	}
	saved, err := os.ReadFile(filepath.Join(corpus, "assessments", "cpu-1.json"))
	if err != nil || !strings.Contains(string(saved), `"e2"`) {
		t.Fatalf("replayed assessment not saved: %v %s", err, saved)
	}
	// Each trial is replayed against its own baseline, if it has one.
	if baselines["/api/v1/tests/run-cpu/archive/root-cause/analyses"] != "run-base" || baselines["/api/v1/tests/run-ok/archive/root-cause/analyses"] != "" {
		t.Fatalf("baselines %v", baselines)
	}
}

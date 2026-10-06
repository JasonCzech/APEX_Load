package api

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func init() {
	registerEvaluationKind(evaluationKind{
		Name:    "test_outcomes",
		Prepare: func(*Server, string, json.RawMessage) (string, error) { return "fp-outcomes", nil },
		Run: func(ctx context.Context, _ *Server, job *storage.EvaluationJob) (any, error) {
			var in testEvaluationInput
			_ = json.Unmarshal([]byte(job.Request), &in)
			switch in.Mode {
			case "large":
				return strings.Repeat("x", storage.MaxEvaluationJobResultBytes+1), nil
			case "unencodable":
				return math.Inf(1), nil
			}
			return map[string]int{"value": in.Value}, nil
		},
	})
	registerEvaluationKind(evaluationKind{
		Name:     "test_deadline",
		Deadline: 20 * time.Millisecond,
		Prepare:  func(*Server, string, json.RawMessage) (string, error) { return "fp-deadline", nil },
		Run: func(ctx context.Context, _ *Server, _ *storage.EvaluationJob) (any, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})
	registerEvaluationKind(evaluationKind{
		Name: "test_no_fingerprint",
		// A kind that fails to fingerprint its inputs produces an invalid job.
		Prepare: func(*Server, string, json.RawMessage) (string, error) { return "", nil },
		Run:     func(context.Context, *Server, *storage.EvaluationJob) (any, error) { return nil, nil },
	})
}

func queueEvaluationKind(t *testing.T, server *Server, kind string, input testEvaluationInput) EvaluationJobResponse {
	t.Helper()
	response := evaluationRequest(t, server, http.MethodPost, evaluationsPath, map[string]any{"kind": kind, "request": input}, "")
	if response.Code != http.StatusAccepted {
		t.Fatalf("queue %s: %d %s", kind, response.Code, response.Body.String())
	}
	var job EvaluationJobResponse
	if err := json.Unmarshal(response.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	return job
}

// Every way a job ends without a result carries its own failure code.
func TestEvaluationJobFailureCodes(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	for _, tc := range []struct {
		kind string
		mode string
		code string
	}{
		{"test_echo", "fail", storage.EvaluationFailureRunError},
		{"test_echo", "panic", storage.EvaluationFailurePanic},
		{"test_outcomes", "large", storage.EvaluationFailureResultTooLarge},
		{"test_outcomes", "unencodable", storage.EvaluationFailureResultEncoding},
		{"test_deadline", "", storage.EvaluationFailureDeadline},
	} {
		job := queueEvaluationKind(t, server, tc.kind, testEvaluationInput{Mode: tc.mode})
		if ran, err := server.runNextEvaluationJob(context.Background()); err != nil || !ran {
			t.Fatalf("%s/%s: run %v %v", tc.kind, tc.mode, ran, err)
		}
		done := readEvaluation(t, server, job.ID)
		if done.State != storage.EvaluationJobFailed || done.FailureCode != tc.code || done.Result != nil {
			t.Fatalf("%s/%s: state %s code %q, want failed %q", tc.kind, tc.mode, done.State, done.FailureCode, tc.code)
		}
	}
}

// A kind that produces an invalid job is a server fault, not a bad request.
func TestEvaluationJobFromAnInvalidKindIsAServerError(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	response := evaluationRequest(t, server, http.MethodPost, evaluationsPath, map[string]any{"kind": "test_no_fingerprint", "request": testEvaluationInput{}}, "")
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("invalid job: %d %s", response.Code, response.Body.String())
	}
}

// A claimed job whose kind this controller no longer runs is left running
// for its lease to expire, so a controller that runs the kind can take it.
func TestEvaluationJobOfAnUnregisteredKindLeavesItsLease(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	if _, _, err := store.CreateEvaluationJob(&storage.EvaluationJob{ID: "vanished", ProjectID: storage.DefaultProjectID, Actor: "user:alice", Kind: "vanished_kind", Request: `{}`, InputFingerprint: "fp"}, 10); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimEvaluationJob("owner", time.Now(), time.Minute, []string{"vanished_kind"}, evaluationJobMaxAttempts)
	if err != nil || claimed == nil {
		t.Fatalf("claim: %#v %v", claimed, err)
	}
	if err := server.runEvaluationJob(context.Background(), store, claimed, "owner", executionLeaseTerm{}); err != nil {
		t.Fatal(err)
	}
	if job, err := store.GetEvaluationJob(storage.DefaultProjectID, "vanished"); err != nil || job.State != storage.EvaluationJobRunning || job.CompletedAt != nil {
		t.Fatalf("the job was finished instead of left to its lease: %#v %v", job, err)
	}
}

// Shutting the worker down stops a running job without recording an outcome;
// its lease expires and another controller retries it.
func TestEvaluationWorkerShutdownLeavesTheLeaseToExpire(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	server.startEvaluationWorker()
	stop := server.evaluationCancel
	if stop == nil {
		t.Fatal("the evaluation worker did not start")
	}
	job := queueEvaluation(t, server, testEvaluationInput{Mode: "block"}, "")
	deadline := time.Now().Add(10 * time.Second)
	for readEvaluation(t, server, job.ID).State != storage.EvaluationJobRunning {
		if time.Now().After(deadline) {
			t.Fatal("the worker never started the job")
		}
		time.Sleep(10 * time.Millisecond)
	}
	stop()
	server.evaluationCancel = nil
	runner := server.evaluationJobRunner()
	for {
		if _, running := runner.running.Load(job.ID); !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the job kept running after shutdown")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if left := readEvaluation(t, server, job.ID); left.State != storage.EvaluationJobRunning || left.CompletedAt != nil || left.FailureCode != "" || left.Attempts != 1 {
		t.Fatalf("shutdown recorded an outcome: %#v", left)
	}
}

// evaluationFinishesBeforeCancelStore finishes the job just before the cancel
// lands, as a runner on another controller would.
type evaluationFinishesBeforeCancelStore struct {
	*storage.SQLiteStore
	t *testing.T
}

func (s *evaluationFinishesBeforeCancelStore) CancelEvaluationJob(projectID, id string, now time.Time) (bool, error) {
	claimed, err := s.ClaimEvaluationJob("other-controller", time.Now(), time.Minute, []string{"test_echo"}, evaluationJobMaxAttempts)
	if err != nil || claimed == nil || claimed.ID != id {
		s.t.Errorf("claim before cancel: %#v %v", claimed, err)
	} else if done, err := s.CompleteEvaluationJobFenced(id, "other-controller", storage.EvaluationJobReady, `{"doubled":2}`, "sha", "", "", time.Now(), "", "", 0); err != nil || !done {
		s.t.Errorf("finish before cancel: %v %v", done, err)
	}
	return s.SQLiteStore.CancelEvaluationJob(projectID, id, now)
}

// A cancel that loses to completion still ends in a finished job, so it
// answers 200 with that job rather than a conflict.
func TestEvaluationCancelAfterTheJobFinishedAnswersTheFinishedJob(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	job := queueEvaluation(t, server, testEvaluationInput{Value: 1}, "")
	server.store = &evaluationFinishesBeforeCancelStore{SQLiteStore: store, t: t}
	t.Cleanup(func() { server.store = store })
	response := evaluationRequest(t, server, http.MethodDelete, "/api/v1/analysis/evaluations/"+job.ID+"?project_id="+storage.DefaultProjectID, nil, "")
	var answered EvaluationJobResponse
	if err := json.Unmarshal(response.Body.Bytes(), &answered); err != nil || response.Code != http.StatusOK || answered.State != storage.EvaluationJobReady {
		t.Fatalf("cancel after completion: %d %s", response.Code, response.Body.String())
	}
}

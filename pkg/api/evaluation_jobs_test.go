package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

type testEvaluationInput struct {
	Value int    `json:"value"`
	Mode  string `json:"mode,omitempty"`
}

func init() {
	registerEvaluationKind(evaluationKind{
		Name: "test_echo",
		Prepare: func(_ *Server, projectID string, request json.RawMessage) (string, error) {
			var in testEvaluationInput
			if err := decodeAnalysisDefinitionJSON(request, &in); err != nil {
				return "", err
			}
			if in.Value < 0 {
				return "", errors.New("value must be non-negative")
			}
			sum := sha256.Sum256(append([]byte(projectID+"\x00"), request...))
			return hex.EncodeToString(sum[:]), nil
		},
		Run: func(ctx context.Context, _ *Server, job *storage.EvaluationJob) (any, error) {
			var in testEvaluationInput
			_ = json.Unmarshal([]byte(job.Request), &in)
			switch in.Mode {
			case "fail":
				return nil, errors.New("deliberate failure")
			case "panic":
				panic("deliberate panic")
			case "block":
				<-ctx.Done()
				return nil, ctx.Err()
			}
			return map[string]int{"doubled": in.Value * 2}, nil
		},
	})
}

const evaluationsPath = "/api/v1/analysis/evaluations?project_id=" + storage.DefaultProjectID

func evaluationRequest(t *testing.T, server *Server, method, path string, body any, key string) *httptest.ResponseRecorder {
	t.Helper()
	var encoded []byte
	if body != nil {
		var err error
		if encoded, err = json.Marshal(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(encoded))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, req)
	return response
}

func queueEvaluation(t *testing.T, server *Server, input testEvaluationInput, key string) EvaluationJobResponse {
	t.Helper()
	response := evaluationRequest(t, server, http.MethodPost, evaluationsPath, map[string]any{"kind": "test_echo", "request": input}, key)
	if response.Code != http.StatusAccepted {
		t.Fatalf("queue: %d %s", response.Code, response.Body.String())
	}
	var job EvaluationJobResponse
	if err := json.Unmarshal(response.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	return job
}

func readEvaluation(t *testing.T, server *Server, id string) EvaluationJobResponse {
	t.Helper()
	response := evaluationRequest(t, server, http.MethodGet, "/api/v1/analysis/evaluations/"+id+"?project_id="+storage.DefaultProjectID, nil, "")
	if response.Code != http.StatusOK {
		t.Fatalf("read: %d %s", response.Code, response.Body.String())
	}
	var job EvaluationJobResponse
	if err := json.Unmarshal(response.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	return job
}

func TestEvaluationJobRunsToAReadyResult(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	job := queueEvaluation(t, server, testEvaluationInput{Value: 21}, "")
	if job.State != storage.EvaluationJobQueued || job.InputFingerprint == "" {
		t.Fatalf("queued job: %#v", job)
	}
	if ran, err := server.runNextEvaluationJob(context.Background()); err != nil || !ran {
		t.Fatalf("run: %v %v", ran, err)
	}
	done := readEvaluation(t, server, job.ID)
	sum := sha256.Sum256(done.Result)
	if done.State != storage.EvaluationJobReady || string(done.Result) != `{"doubled":42}` || done.ResultSHA256 != hex.EncodeToString(sum[:]) || done.CompletedAt == nil {
		t.Fatalf("ready job: %#v %s", done, done.Result)
	}
	if ran, _ := server.runNextEvaluationJob(context.Background()); ran {
		t.Fatal("a finished job ran again")
	}
}

func TestEvaluationJobFailuresAndPanicsAreTerminal(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	for mode, want := range map[string]string{"fail": "deliberate failure", "panic": "the job stopped unexpectedly"} {
		job := queueEvaluation(t, server, testEvaluationInput{Mode: mode}, "")
		if _, err := server.runNextEvaluationJob(context.Background()); err != nil {
			t.Fatal(err)
		}
		if done := readEvaluation(t, server, job.ID); done.State != storage.EvaluationJobFailed || done.Failure != want {
			t.Fatalf("%s: %#v", mode, done)
		}
	}
	if ran, _ := server.runNextEvaluationJob(context.Background()); ran {
		t.Fatal("a failed job was re-claimed")
	}
}

func TestEvaluationJobIdempotencyValidationAndQuota(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	first := queueEvaluation(t, server, testEvaluationInput{Value: 1}, "evaluation-key-0001")
	again := evaluationRequest(t, server, http.MethodPost, evaluationsPath, map[string]any{"kind": "test_echo", "request": testEvaluationInput{Value: 1}}, "evaluation-key-0001")
	var replayed EvaluationJobResponse
	_ = json.Unmarshal(again.Body.Bytes(), &replayed)
	if again.Code != http.StatusAccepted || again.Header().Get("Idempotent-Replayed") != "true" || replayed.ID != first.ID {
		t.Fatalf("replay: %d %s", again.Code, again.Body.String())
	}
	if changed := evaluationRequest(t, server, http.MethodPost, evaluationsPath, map[string]any{"kind": "test_echo", "request": testEvaluationInput{Value: 2}}, "evaluation-key-0001"); changed.Code != http.StatusConflict {
		t.Fatalf("changed request: %d %s", changed.Code, changed.Body.String())
	}
	for _, body := range []map[string]any{
		{"kind": "no_such_kind", "request": map[string]int{"value": 1}},
		{"kind": "test_echo", "request": map[string]int{"value": -1}},
		{"kind": "test_echo", "request": map[string]any{"value": 1, "sql": "select 1"}},
	} {
		if response := evaluationRequest(t, server, http.MethodPost, evaluationsPath, body, ""); response.Code != http.StatusBadRequest {
			t.Fatalf("invalid evaluation %v: %d %s", body, response.Code, response.Body.String())
		}
	}
	for i := 1; i < evaluationJobsActivePerProject; i++ {
		queueEvaluation(t, server, testEvaluationInput{Value: 100 + i}, "")
	}
	if full := evaluationRequest(t, server, http.MethodPost, evaluationsPath, map[string]any{"kind": "test_echo", "request": testEvaluationInput{Value: 999}}, ""); full.Code != http.StatusTooManyRequests {
		t.Fatalf("quota: %d %s", full.Code, full.Body.String())
	}
}

func TestEvaluationJobCancellationStopsRunningWorkAndRecordsNothing(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	job := queueEvaluation(t, server, testEvaluationInput{Mode: "block"}, "")
	ran := make(chan error, 1)
	go func() {
		_, err := server.runNextEvaluationJob(context.Background())
		ran <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for readEvaluation(t, server, job.ID).State != storage.EvaluationJobRunning {
		if time.Now().After(deadline) {
			t.Fatal("job never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if response := evaluationRequest(t, server, http.MethodDelete, "/api/v1/analysis/evaluations/"+job.ID+"?project_id="+storage.DefaultProjectID, nil, ""); response.Code != http.StatusOK {
		t.Fatalf("cancel: %d %s", response.Code, response.Body.String())
	}
	select {
	case err := <-ran:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation did not stop the running job")
	}
	if done := readEvaluation(t, server, job.ID); done.State != storage.EvaluationJobCancelled || done.Result != nil {
		t.Fatalf("cancelled job: %#v", done)
	}
	// A retried cancel is idempotent: it neither fails nor hides the job.
	if retried := evaluationRequest(t, server, http.MethodDelete, "/api/v1/analysis/evaluations/"+job.ID+"?project_id="+storage.DefaultProjectID, nil, ""); retried.Code != http.StatusOK {
		t.Fatalf("retried cancel: %d %s", retried.Code, retried.Body.String())
	}
	if again := readEvaluation(t, server, job.ID); again.Hidden || again.State != storage.EvaluationJobCancelled {
		t.Fatalf("a retried cancel changed the job: %#v", again)
	}
	for range 2 {
		if hide := evaluationRequest(t, server, http.MethodDelete, "/api/v1/analysis/evaluations/"+job.ID+"?project_id="+storage.DefaultProjectID+"&hide=true", nil, ""); hide.Code != http.StatusOK {
			t.Fatalf("hide: %d %s", hide.Code, hide.Body.String())
		}
	}
	list := evaluationRequest(t, server, http.MethodGet, evaluationsPath, nil, "")
	var listed struct {
		Jobs []EvaluationJobResponse `json:"jobs"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil || len(listed.Jobs) != 0 {
		t.Fatalf("hidden job still listed: %s %v", list.Body.String(), err)
	}
}

func TestEvaluationJobOnlyCreatorOrEditorMayCancel(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	job := queueEvaluation(t, server, testEvaluationInput{Value: 3}, "")
	viewer := &AuthenticatedUser{Subject: "bob", ProjectRoles: map[string]map[string]struct{}{storage.DefaultProjectID: {RoleViewer: {}}}}
	editor := &AuthenticatedUser{Subject: "carol", ProjectRoles: map[string]map[string]struct{}{storage.DefaultProjectID: {RoleEditor: {}}}}
	cancelAs := func(user *AuthenticatedUser) int {
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/analysis/evaluations/"+job.ID+"?project_id="+storage.DefaultProjectID, nil)
		req.SetPathValue("id", job.ID)
		req = withAuthenticatedContext(req, authResolution{User: user})
		response := httptest.NewRecorder()
		serveEvaluationJobs(server, response, req)
		return response.Code
	}
	if code := cancelAs(viewer); code != http.StatusForbidden {
		t.Fatalf("another viewer cancelled the job: %d", code)
	}
	if code := cancelAs(editor); code != http.StatusOK {
		t.Fatalf("editor could not cancel: %d", code)
	}
}

// serveEvaluationJobs dispatches a test request to the evaluation handler its
// route registers.
func serveEvaluationJobs(server *Server, w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost:
		server.handleCreateEvaluationJob(w, r)
	case r.Method == http.MethodDelete:
		server.handleDeleteEvaluationJob(w, r)
	case r.PathValue("id") != "":
		server.handleGetEvaluationJob(w, r)
	default:
		server.handleListEvaluationJobs(w, r)
	}
}

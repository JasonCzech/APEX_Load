package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestEvaluationJobReviewContract(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })

	// A viewer cancels its own job.
	viewer := &AuthenticatedUser{Subject: "viewer-own", ProjectRoles: map[string]map[string]struct{}{storage.DefaultProjectID: {RoleViewer: {}}}}
	asViewer := func(method, path string, body any) *httptest.ResponseRecorder {
		var encoded []byte
		if body != nil {
			encoded, _ = json.Marshal(body)
		}
		req := httptest.NewRequest(method, path, strings.NewReader(string(encoded)))
		req.Header.Set("Content-Type", "application/json")
		if i := strings.Index(path, "/evaluations/"); i >= 0 {
			id := path[i+len("/evaluations/"):]
			if q := strings.Index(id, "?"); q >= 0 {
				id = id[:q]
			}
			req.SetPathValue("id", id)
		}
		req = withAuthenticatedContext(req, authResolution{User: viewer})
		response := httptest.NewRecorder()
		server.handleEvaluationJobs(response, req)
		return response
	}
	created := asViewer(http.MethodPost, evaluationsPath, map[string]any{"kind": "test_echo", "request": testEvaluationInput{Value: 7}})
	var own EvaluationJobResponse
	if err := json.Unmarshal(created.Body.Bytes(), &own); err != nil || created.Code != http.StatusAccepted {
		t.Fatalf("viewer create: %d %s", created.Code, created.Body.String())
	}
	if cancelled := asViewer(http.MethodDelete, "/api/v1/analysis/evaluations/"+own.ID+"?project_id="+storage.DefaultProjectID, nil); cancelled.Code != http.StatusOK {
		t.Fatalf("viewer cancelling its own job: %d %s", cancelled.Code, cancelled.Body.String())
	}

	// Hiding an unfinished job is refused; the job must be cancelled first.
	running := queueEvaluation(t, server, testEvaluationInput{Value: 8}, "")
	if hide := evaluationRequest(t, server, http.MethodDelete, "/api/v1/analysis/evaluations/"+running.ID+"?project_id="+storage.DefaultProjectID+"&hide=true", nil, ""); hide.Code != http.StatusConflict {
		t.Fatalf("hiding an unfinished job: %d %s", hide.Code, hide.Body.String())
	}

	// A completed job's listing omits its bodies; GET {id} returns them.
	for {
		ran, err := server.runNextEvaluationJob(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !ran {
			break
		}
	}
	list := evaluationRequest(t, server, http.MethodGet, evaluationsPath, nil, "")
	if strings.Contains(list.Body.String(), `"result":`) || strings.Contains(list.Body.String(), `"request":`) {
		t.Fatalf("listing carried bodies: %s", list.Body.String())
	}
	if done := readEvaluation(t, server, running.ID); done.State != storage.EvaluationJobReady || len(done.Result) == 0 || len(done.Request) == 0 || done.Attempts != 1 {
		t.Fatalf("job read by id: %#v", done)
	}

	// The key is judged by the request, and its conflict is coded.
	key := "evaluation-key-review"
	queueEvaluation(t, server, testEvaluationInput{Value: 9}, key)
	changed := evaluationRequest(t, server, http.MethodPost, evaluationsPath, map[string]any{"kind": "test_echo", "request": testEvaluationInput{Value: 10}}, key)
	if changed.Code != http.StatusConflict || idempotencyErrorCode(t, changed) != "idempotency_key_reused" {
		t.Fatalf("changed request: %d %s", changed.Code, changed.Body.String())
	}

	// A missing project is not found, not a retryable outage.
	missing := evaluationRequest(t, server, http.MethodPost, "/api/v1/analysis/evaluations?project_id=no-such-project", map[string]any{"kind": "test_echo", "request": testEvaluationInput{Value: 1}}, "")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing project: %d %s", missing.Code, missing.Body.String())
	}

	// Failures carry a code.
	failing := queueEvaluation(t, server, testEvaluationInput{Mode: "fail"}, "")
	for {
		ran, err := server.runNextEvaluationJob(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !ran {
			break
		}
	}
	if done := readEvaluation(t, server, failing.ID); done.FailureCode != storage.EvaluationFailureRunError {
		t.Fatalf("failure code: %#v", done)
	}
}

// Evaluations are read-only analyses: a results:read service account may use
// them, through the scope rules rather than the generic read/write fallback.
func TestEvaluationRoutesNeedOnlyResultsRead(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		if scope := staticServiceAccountScopeForRoute(method, "/api/v1/analysis/evaluations"); scope != "results:read" {
			t.Fatalf("%s evaluations scope %q", method, scope)
		}
		if scope := staticServiceAccountScopeForRoute(method, "/api/v1/analysis/evaluations/job-1"); scope != "results:read" {
			t.Fatalf("%s evaluation scope %q", method, scope)
		}
	}
}

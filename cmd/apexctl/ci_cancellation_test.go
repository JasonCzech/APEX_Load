package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/cmd/apexctl/internal/ciclient"
)

func TestControllerCIResultKeepsCommandPolicyOutsideCoordinator(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		observed                bool
		cancellation            error
		err                     error
		wantOutcome, wantStatus string
	}{
		{name: "create authentication", err: &ciControllerAuthFailure{Err: ciConfigurationErrorf("missing authentication")}, wantOutcome: ciOutcomeConfigurationError, wantStatus: "AUTH_FAILED"},
		{name: "create transport", err: errors.New("network"), wantOutcome: ciOutcomeExecutionError, wantStatus: "CREATE_FAILED"},
		{name: "observation transport", observed: true, err: errors.New("network"), wantOutcome: ciOutcomeExecutionError, wantStatus: "STATUS_FAILED"},
		{name: "cancelled with abort deadline", observed: true, cancellation: context.Canceled, err: errors.Join(context.Canceled, context.DeadlineExceeded), wantOutcome: ciOutcomeCancelled, wantStatus: "CANCELLED"},
		{name: "workflow deadline", observed: true, cancellation: context.DeadlineExceeded, err: context.DeadlineExceeded, wantOutcome: ciOutcomeTimedOut, wantStatus: "TIMED_OUT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := ciclient.State[*CIRunResponse]{TestID: "deterministic", Observed: tc.observed, Cancellation: tc.cancellation}
			if tc.observed {
				state.Response = &CIRunResponse{TestID: "accepted", Status: "RUNNING", Transactions: 7}
			}
			result := controllerCIResult(state, tc.err)
			if result.Outcome != tc.wantOutcome || result.Status != tc.wantStatus {
				t.Fatalf("result=%+v", result)
			}
			if tc.observed {
				if result.TestID != "accepted" || result.Transactions != 7 {
					t.Fatalf("last complete observation lost: %+v", result)
				}
			} else if result.TestID != "deterministic" {
				t.Fatalf("deterministic identity lost: %+v", result)
			}
		})
	}
}

func controllerCancellationArgs(t *testing.T, controllerURL string) ciArgs {
	t.Helper()
	args := defaultCIArgs()
	args.controllerURL, args.projectID, args.scriptID = controllerURL, "project-1", "script-1"
	args.scriptPath, args.authMode, args.apiKey = "", "api-key", "key"
	dir := t.TempDir()
	args.jsonPath, args.junitPath = filepath.Join(dir, "result.json"), filepath.Join(dir, "result.xml")
	return args
}

func TestExecuteControllerCICancellationDuringObservation(t *testing.T) {
	oldPoll := ciPollInterval
	ciPollInterval = time.Millisecond
	t.Cleanup(func() { ciPollInterval = oldPoll })
	for _, tc := range []struct {
		name, stage                       string
		deadline, disableAbort, failAbort bool
	}{
		{name: "cancel HTTP", stage: "http"},
		{name: "deadline HTTP", stage: "http", deadline: true},
		{name: "cancel retry backoff", stage: "backoff"},
		{name: "deadline retry backoff", stage: "backoff", deadline: true},
		{name: "cancel with abort disabled", stage: "http", disableAbort: true},
		{name: "abort confirmation failure", stage: "http", failAbort: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withCIRetryPolicy(t, 3, time.Hour, time.Hour, time.Hour, 100*time.Millisecond)
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			pollStarted := make(chan struct{})
			var polls, aborts atomic.Int32
			var args ciArgs
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.Method + " " + r.URL.Path {
				case "POST /api/v1/ci/runs":
					w.WriteHeader(http.StatusAccepted)
					_, _ = io.WriteString(w, `{"schema_version":1,"test_id":"test-1","status":"RUNNING","transactions":7}`)
				case "GET /api/v1/ci/runs/test-1":
					if polls.Add(1) == 1 {
						close(pollStarted)
					}
					if tc.stage == "backoff" {
						http.Error(w, "retry this observation", http.StatusServiceUnavailable)
						return
					}
					<-r.Context().Done()
				case "POST /api/v1/ci/runs/test-1/abort":
					aborts.Add(1)
					// Evidence must exist before any remote abort, retaining the last
					// observed metrics even when the pending status request failed.
					data, err := os.ReadFile(args.jsonPath)
					var evidence CIResult
					if err != nil || json.Unmarshal(data, &evidence) != nil || evidence.TestID != "test-1" || evidence.Transactions != 7 {
						t.Errorf("pre-abort evidence = %s, err = %v", data, err)
					}
					var request struct {
						Reason string `json:"reason"`
					}
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Errorf("abort request: %v", err)
					}
					wantReason := "cancelled"
					if tc.deadline {
						wantReason = "timed_out"
					}
					if request.Reason != wantReason {
						t.Errorf("abort reason = %q, want %q", request.Reason, wantReason)
					}
					if tc.failAbort {
						<-r.Context().Done()
						return
					}
					_, _ = io.WriteString(w, `{"schema_version":1,"test_id":"test-1","status":"ABORTED"}`)
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			args = controllerCancellationArgs(t, server.URL)
			args.abortOnCancel = !tc.disableAbort
			if !tc.deadline {
				go func() {
					select {
					case <-pollStarted:
						if tc.stage == "backoff" {
							// Let the 503 body return before cancelling the hour-long retry wait.
							time.Sleep(50 * time.Millisecond)
						}
						cancel()
					case <-ctx.Done():
					}
				}()
			}
			result, err := executeControllerCI(ctx, args, nil)
			wantErr, wantOutcome, wantStatus := context.Canceled, ciOutcomeCancelled, "CANCELLED"
			if tc.deadline {
				wantErr, wantOutcome, wantStatus = context.DeadlineExceeded, ciOutcomeTimedOut, "TIMED_OUT"
			}
			if !errors.Is(err, wantErr) || result.Outcome != wantOutcome || result.Status != wantStatus || result.TestID != "test-1" {
				t.Fatalf("result = %#v, err = %v", result, err)
			}
			if polls.Load() != 1 {
				t.Fatalf("poll calls = %d, want 1", polls.Load())
			}
			wantAborts := int32(1)
			if tc.disableAbort {
				wantAborts = 0
			}
			if aborts.Load() != wantAborts {
				t.Errorf("aborts = %d, want %d", aborts.Load(), wantAborts)
			}
			if tc.failAbort && !strings.Contains(err.Error(), "controller may still be running") {
				t.Errorf("lost abort failure: %v", err)
			}
		})
	}
}

func TestExecuteControllerCICancellationDuringPollAuthRefresh(t *testing.T) {
	withCIRetryPolicy(t, 3, time.Millisecond, time.Millisecond, time.Second, time.Second)
	oldPoll := ciPollInterval
	ciPollInterval = time.Millisecond
	t.Cleanup(func() { ciPollInterval = oldPoll })
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var tokens, aborts atomic.Int32
	oidc := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tokens.Add(1) == 2 {
			cancel()
			<-r.Context().Done()
			return
		}
		_, _ = io.WriteString(w, `{"value":"github-id-token"}`)
	}))
	defer oidc.Close()
	originalClient := newGitHubOIDCHTTPClient
	newGitHubOIDCHTTPClient = func() *http.Client { return oidc.Client() }
	t.Cleanup(func() { newGitHubOIDCHTTPClient = originalClient })
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", oidc.URL+"/token")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "request-token")
	t.Setenv("GITHUB_SERVER_URL", oidc.URL)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /.well-known/apexload-ci":
			_, _ = io.WriteString(w, `{"schema_version":1,"controller_id":"controller-1","oidc_audience":"apexload://controller-1","ci_api_version":"v1","auth_modes":["oidc"]}`)
		case "POST /api/v1/auth/ci/github/exchange":
			_, _ = io.WriteString(w, `{"access_token":"ci-access-token","token_type":"Bearer","expires_in":600}`)
		case "POST /api/v1/ci/runs":
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"schema_version":1,"test_id":"test-1","status":"RUNNING"}`)
		case "GET /api/v1/ci/runs/test-1":
			http.Error(w, "refresh authentication", http.StatusUnauthorized)
		case "POST /api/v1/ci/runs/test-1/abort":
			aborts.Add(1)
			_, _ = io.WriteString(w, `{"schema_version":1,"test_id":"test-1","status":"ABORTED"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	args := controllerCancellationArgs(t, server.URL)
	args.authMode = "oidc"
	result, err := executeControllerCI(ctx, args, nil)
	if !errors.Is(err, context.Canceled) || result.Status != "CANCELLED" || result.Outcome != ciOutcomeCancelled || aborts.Load() != 1 || tokens.Load() != 3 {
		t.Fatalf("result=%#v err=%v aborts=%d tokens=%d", result, err, aborts.Load(), tokens.Load())
	}
}

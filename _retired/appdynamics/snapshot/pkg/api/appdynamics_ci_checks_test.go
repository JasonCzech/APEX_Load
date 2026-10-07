package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestAppDynamicsCICheckPreservesMonitoringIncompleteOutcome(t *testing.T) {
	for _, test := range []struct {
		name, outcome, explanation string
	}{
		{"classified evidence", "monitoring_incomplete", "Required monitoring evidence was incomplete"},
		{"standalone synchronization", "passed", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(githubAllowInsecureHTTPEnv, "true")
			privateKey, _ := testGitHubAppKeyPEM(t)
			t.Setenv("APEXLOAD_APPDYNAMICS_CI_CHECK_KEY", privateKey)
			var testID string
			var stateMu sync.Mutex
			summaries := make(chan string, 1)
			var stub *httptest.Server
			stub = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/app/installations/101/access_tokens":
					writeJSON(w, http.StatusCreated, map[string]any{"token": "installation-token", "expires_at": time.Now().UTC().Add(time.Hour)})
				case r.Method == http.MethodPatch && r.URL.Path == "/repos/octo/apex/check-runs/77":
					var body struct {
						Status, Conclusion string
						Output             struct{ Summary string }
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Errorf("decode Check update: %v", err)
						http.Error(w, "invalid update", http.StatusBadRequest)
						return
					}
					summaries <- body.Output.Summary
					stateMu.Lock()
					externalID := testID
					stateMu.Unlock()
					writeJSON(w, http.StatusOK, map[string]any{
						"id": 77, "status": body.Status, "conclusion": body.Conclusion,
						"html_url": stub.URL + "/octo/apex/runs/77", "external_id": externalID,
						"head_sha": "0123456789abcdef0123456789abcdef01234567",
					})
				default:
					http.Error(w, "unexpected request", http.StatusNotFound)
				}
			}))
			defer stub.Close()
			policy := ciTestActionsPolicy()
			policy.ServerURL = stub.URL
			server, store := newCIContractTestServer(t, policy)
			settings := server.runner.GetSystemSettings()
			settings.GitHub = GitHubSettings{Enabled: true, ChecksEnabled: true, AppID: 12345, AppPrivateKeyEnvVar: "APEXLOAD_APPDYNAMICS_CI_CHECK_KEY", APIBaseURL: stub.URL, WebBaseURL: stub.URL}
			if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
				t.Fatal(err)
			}
			run := createTerminalCIContractTest(t, server, store, "COMPLETE", "")
			stateMu.Lock()
			testID = run.ID
			stateMu.Unlock()
			source := ciTestRunSource("1")
			source.ServerURL, source.APIURL = stub.URL, stub.URL
			metadata := ciRunMetadata{Source: source, PublishCheck: true, CheckInstallationID: 101, RequireResults: true}
			link := &storage.GitHubCheckLink{
				ID:   uuid.NewSHA1(uuid.NameSpaceURL, []byte("apexload-ci-check:"+run.ID)).String(),
				Kind: "ci", TestID: run.ID, Owner: "octo", Repo: "apex", CommitSHA: source.SHA,
				CheckRunID: 77, CheckRunURL: stub.URL + "/octo/apex/runs/77", Status: "in_progress",
			}
			if err := store.SaveGitHubCheckLink(link); err != nil {
				t.Fatal(err)
			}
			// buildCIRunResponse has already validated the durable archive before
			// handing this terminal snapshot to the independent publisher.
			response := ciRunResponse{Status: "COMPLETE", Outcome: test.outcome, Error: test.explanation, Transactions: 1, SLAEvaluated: true, MonitoringIncomplete: true, terminalEvidenceChecked: true, terminalEvidenceVerified: true}
			if _, err := server.syncCIGitHubCheck(context.Background(), run, metadata, response); err != nil {
				t.Fatal(err)
			}
			summary := <-summaries
			if !strings.Contains(summary, "| Outcome | `monitoring_incomplete` |") || !strings.Contains(summary, "Evidence explanation: ") || strings.Contains(summary, "Execution error: ") {
				t.Fatalf("incomplete monitoring classification changed during publication: %s", summary)
			}
			published, err := storedCIGitHubCheck(store, run.ID)
			if err != nil || published == nil || published.Status != "completed" || published.Conclusion != "failure" || published.PendingConclusion != "" {
				t.Fatalf("incomplete Check was not durably finalized as failure: %+v %v", published, err)
			}
		})
	}
}

func TestAppDynamicsCICheckSummaryEvidenceLabels(t *testing.T) {
	for _, test := range []struct{ outcome, label string }{
		{"execution_error", "Execution error: "},
		{"monitoring_incomplete", "Evidence explanation: "},
	} {
		t.Run(test.outcome, func(t *testing.T) {
			summary := ciGitHubCheckSummary(nil, ciRunResponse{Status: "COMPLETE", Outcome: test.outcome, Error: ciTerminalEvidenceFailureReason}, ciRunMetadata{})
			if !strings.Contains(summary, test.label+ciTerminalEvidenceFailureReason) {
				t.Fatalf("safe evidence reason lost its expected label: %s", summary)
			}
		})
	}
}

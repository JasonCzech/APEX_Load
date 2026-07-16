package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

type ciCheckStubState struct {
	mu          sync.Mutex
	tokenCalls  int
	createCalls int
	updateCalls int
	creates     []map[string]any
	updates     []map[string]any
}

func (state *ciCheckStubState) snapshot() (int, int, int, []map[string]any, []map[string]any) {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.tokenCalls, state.createCalls, state.updateCalls, append([]map[string]any(nil), state.creates...), append([]map[string]any(nil), state.updates...)
}

func TestControllerOwnedGitHubCheckLifecycle(t *testing.T) {
	t.Setenv(githubAllowInsecureHTTPEnv, "true")
	t.Setenv("APEXLOAD_PUBLIC_URL", "https://apex.example.test")
	privateKey, _ := testGitHubAppKeyPEM(t)
	t.Setenv("APEXLOAD_CI_CHECK_APP_KEY", privateKey)

	state := &ciCheckStubState{}
	var stub *httptest.Server
	stub = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/app/installations/101/access_tokens":
			state.mu.Lock()
			state.tokenCalls++
			state.mu.Unlock()
			writeJSON(w, http.StatusCreated, map[string]any{"token": "installation-token", "expires_at": time.Now().UTC().Add(time.Hour)})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/octo/apex/actions/runs/9001":
			writeJSON(w, http.StatusOK, map[string]any{
				"id": 9001, "event": "push", "head_sha": "0123456789abcdef0123456789abcdef01234567",
				"repository":      map[string]any{"id": 12345, "full_name": "octo/apex"},
				"head_repository": map[string]any{"id": 12345, "full_name": "octo/apex"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/octo/apex/commits/0123456789abcdef0123456789abcdef01234567/check-runs":
			if r.URL.Query().Get("check_name") != ciGitHubCheckName || r.URL.Query().Get("filter") != "all" {
				t.Errorf("unexpected Check recovery query: %s", r.URL.RawQuery)
			}
			writeJSON(w, http.StatusOK, map[string]any{"total_count": 0, "check_runs": []any{}})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/octo/apex/check-runs":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode create Check body: %v", err)
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			state.mu.Lock()
			state.createCalls++
			state.creates = append(state.creates, body)
			state.mu.Unlock()
			writeJSON(w, http.StatusCreated, map[string]any{"id": 77, "status": "in_progress", "html_url": stub.URL + "/octo/apex/runs/77"})
		case r.Method == http.MethodPatch && r.URL.Path == "/repos/octo/apex/check-runs/77":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode update Check body: %v", err)
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			state.mu.Lock()
			state.updateCalls++
			state.updates = append(state.updates, body)
			state.mu.Unlock()
			response := map[string]any{"id": 77, "status": body["status"], "html_url": stub.URL + "/octo/apex/runs/77"}
			if conclusion, ok := body["conclusion"]; ok {
				response["conclusion"] = conclusion
			}
			writeJSON(w, http.StatusOK, response)
		default:
			http.Error(w, fmt.Sprintf("unexpected request %s %s", r.Method, r.URL.Path), http.StatusNotFound)
		}
	}))
	defer stub.Close()

	actionsPolicy := ciTestActionsPolicy()
	actionsPolicy.ServerURL = stub.URL
	server, store := newCIContractTestServer(t, actionsPolicy)
	now := time.Now().UTC()
	if err := store.SaveGitHubAppInstallation(&storage.GitHubAppInstallation{
		InstallationID: 101, AccountLogin: "octo", AccountType: "Organization", RepositorySelection: "selected",
		PermissionsJSON: `{"checks":"write"}`, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveGitHubAppInstallation: %v", err)
	}
	settings := server.runner.GetSystemSettings()
	settings.GitHub = GitHubSettings{
		Enabled: true, ChecksEnabled: true, AppID: 12345, AppPrivateKeyEnvVar: "APEXLOAD_CI_CHECK_APP_KEY",
		APIBaseURL: stub.URL, WebBaseURL: stub.URL,
	}
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings: %v", err)
	}

	script, err := server.runner.StoreScriptDurable(&dsl.Script{
		ID: "ci-check-script", ProjectID: "project-ci", Name: "Check lifecycle", Version: "1.0.0", Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: "https://api.example.test"}, Actions: []dsl.Action{{Name: "health", Type: "request", Target: "/health"}},
	})
	if err != nil {
		t.Fatalf("StoreScriptDurable: %v", err)
	}
	source := ciTestRunSource("1")
	source.ServerURL, source.APIURL = stub.URL, stub.URL
	metadata := ciRunMetadata{
		SchemaVersion: ciRunSchemaVersion, Source: source, ScriptIdentity: "script_id:" + script.ID,
		RequireSLA: true, RequireResults: true, PublishCheck: true, CheckInstallationID: 101,
		PolicyRevision: actionsPolicy.Revision, IdempotencySHA: sha256String("ci-check-lifecycle-idempotency-key"),
	}
	materializationKey, err := encodeCIRunMetadata(metadata)
	if err != nil {
		t.Fatal(err)
	}
	testRun, err := server.runner.CreateTest(CreateTestRequest{
		Name: "CI Check lifecycle", ProjectID: "project-ci", ScriptID: script.ID, VUsers: 1, Duration: "1m",
		MaterializationKey: materializationKey,
		GoalConfig: &GoalConfig{
			P95LatencyMs: ptr(250.0), MaxErrorPct: ptr(5.0),
			MaxAvgServerProcessingMs: ptr(100.0), MaxAvgTransferMs: ptr(50.0), MaxErrorCount: ptr(10),
		},
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	projectPolicy := storage.GitHubProjectPolicy{AppInstallationID: 101, Actions: actionsPolicy}
	client, err := server.validateCICheckPublishing(context.Background(), projectPolicy, source)
	if err != nil {
		t.Fatalf("validateCICheckPublishing: %v", err)
	}
	if err := server.saveCIGitHubCheckIntent(testRun.ID, testRun.ProjectID, metadata); err != nil {
		t.Fatalf("saveCIGitHubCheckIntent: %v", err)
	}
	link, err := server.createCIGitHubCheck(context.Background(), client, projectPolicy, testRun, metadata)
	if err != nil {
		t.Fatalf("createCIGitHubCheck: %v", err)
	}
	if link.CheckRunID != 77 || link.Status != "in_progress" || link.Conclusion != "" {
		t.Fatalf("created Check link = %#v", link)
	}
	_, createCalls, updateCalls, creates, _ := state.snapshot()
	if createCalls != 1 || updateCalls != 0 || len(creates) != 1 {
		t.Fatalf("Check API calls after create = create:%d update:%d", createCalls, updateCalls)
	}
	if creates[0]["status"] != "in_progress" || creates[0]["head_sha"] != source.SHA || creates[0]["external_id"] != testRun.ID {
		t.Fatalf("create Check body = %#v", creates[0])
	}
	if _, exists := creates[0]["conclusion"]; exists {
		t.Fatalf("in-progress Check carried a final conclusion: %#v", creates[0])
	}

	complete := *testRun
	complete.Status = "COMPLETE"
	complete.LiveMetrics = &LiveMetrics{
		P95Ms: 180, ErrorPct: 1.67, TotalErrors: 2,
		Transactions: map[string]*TxMetrics{
			"health": {ServerTelemetry: &ServerTelemetrySummary{AvgServerProcessingMs: 80, AvgTransferMs: 20}},
		},
	}
	passed := ciRunResponse{
		Status: "COMPLETE", Outcome: "passed", Transactions: 120, Errors: 2, TPS: 40, DurationMs: 3000,
		SLAEvaluated: true, ResultsPresent: true, ReportURL: "https://apex.example.test/tests/" + testRun.ID,
	}
	checkURL, err := server.syncCIGitHubCheck(context.Background(), &complete, metadata, passed)
	if err != nil {
		t.Fatalf("sync passed Check: %v", err)
	}
	if checkURL != stub.URL+"/octo/apex/runs/77" {
		t.Fatalf("check URL = %q", checkURL)
	}
	_, _, updateCalls, _, updates := state.snapshot()
	if updateCalls != 1 || updates[0]["status"] != "completed" || updates[0]["conclusion"] != "success" {
		t.Fatalf("passed Check updates = %#v", updates)
	}
	output, _ := updates[0]["output"].(map[string]any)
	summary, _ := output["summary"].(string)
	for _, required := range []string{
		"120", "octo/apex@" + source.SHA, source.WorkflowRef,
		"| P95 latency | ≤ 250.00 ms | 180.00 ms | `PASS` |",
		"| Error rate | ≤ 5.00% | 1.67% | `PASS` |",
		"| Average server processing | ≤ 100.00 ms | 80.00 ms | `PASS` |",
		"| Average transfer | ≤ 50.00 ms | 20.00 ms | `PASS` |",
		"| Error count | ≤ 10 | 2 | `PASS` |",
	} {
		if !strings.Contains(summary, required) {
			t.Fatalf("Check summary missing %q: %s", required, summary)
		}
	}

	if _, err := server.syncCIGitHubCheck(context.Background(), &complete, metadata, passed); err != nil {
		t.Fatalf("idempotent sync: %v", err)
	}
	_, _, updateCalls, _, _ = state.snapshot()
	if updateCalls != 1 {
		t.Fatalf("idempotent sync issued %d updates, want 1", updateCalls)
	}

	// Once the durable Check is completed, conflicting terminal snapshots and a
	// stale nonterminal snapshot are strict no-ops.
	breached := passed
	breached.Outcome, breached.SLABreached, breached.SLADetails = "sla_breached", true, []string{"P95 latency breached threshold"}
	if _, err := server.syncCIGitHubCheck(context.Background(), &complete, metadata, breached); err != nil {
		t.Fatalf("sync breached Check: %v", err)
	}
	aborted := complete
	aborted.Status = "ABORTED"
	cancelled := passed
	cancelled.Status, cancelled.Outcome = "ABORTED", "cancelled"
	if _, err := server.syncCIGitHubCheck(context.Background(), &aborted, metadata, cancelled); err != nil {
		t.Fatalf("sync cancelled Check: %v", err)
	}
	running := complete
	running.Status = "RUNNING"
	inProgress := passed
	inProgress.Status, inProgress.Outcome = "RUNNING", "running"
	if _, err := server.syncCIGitHubCheck(context.Background(), &running, metadata, inProgress); err != nil {
		t.Fatalf("sync nonterminal Check: %v", err)
	}

	tokenCalls, _, updateCalls, _, updates := state.snapshot()
	if tokenCalls != 1 {
		t.Fatalf("installation token calls = %d, want cached single call", tokenCalls)
	}
	if updateCalls != 1 {
		t.Fatalf("total Check updates = %d, want immutable single terminal update; updates=%#v", updateCalls, updates)
	}

	links, err := store.ListGitHubCheckLinks(testRun.ID, "", 10)
	if err != nil || len(links) != 1 {
		t.Fatalf("ListGitHubCheckLinks = %#v, %v", links, err)
	}
	if links[0].ScriptID != "" {
		t.Fatalf("CI Check link script id = %q, want test-only lifecycle anchor", links[0].ScriptID)
	}
	if links[0].Status != "completed" || links[0].Conclusion != "success" || links[0].PendingConclusion != "" {
		t.Fatalf("persisted Check lifecycle = %#v", links[0])
	}
	intents, err := store.ListPendingGitHubCheckIntents(10)
	if err != nil || len(intents) != 0 {
		t.Fatalf("completed Check left pending intents = %#v, %v", intents, err)
	}
	if deleted, deleteErr := server.runner.DeleteScriptDurableInProject(script.ID, "project-ci"); deleteErr != nil || !deleted {
		t.Fatalf("DeleteScriptDurableInProject = %t, %v", deleted, deleteErr)
	}
	links, err = store.ListGitHubCheckLinks(testRun.ID, "", 10)
	if err != nil || len(links) != 1 {
		t.Fatalf("script cleanup removed test-anchored CI Check link: %#v, %v", links, err)
	}
}

func TestCIGitHubCheckReconciliationRecoversIntentAfterRestart(t *testing.T) {
	t.Setenv(githubAllowInsecureHTTPEnv, "true")
	t.Setenv("APEXLOAD_PUBLIC_URL", "https://apex.example.test")
	privateKey, _ := testGitHubAppKeyPEM(t)
	t.Setenv("APEXLOAD_CI_CHECK_RECOVERY_KEY", privateKey)

	const (
		testID = "7aa02adb-8398-4fc4-9748-218d07a0712a"
		sha    = "0123456789abcdef0123456789abcdef01234567"
	)
	state := &ciCheckStubState{}
	var stub *httptest.Server
	stub = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/app/installations/101/access_tokens":
			state.mu.Lock()
			state.tokenCalls++
			state.mu.Unlock()
			writeJSON(w, http.StatusCreated, map[string]any{"token": "installation-token", "expires_at": time.Now().UTC().Add(time.Hour)})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/octo/apex/commits/"+sha+"/check-runs":
			writeJSON(w, http.StatusOK, map[string]any{
				"total_count": 1,
				"check_runs": []map[string]any{{
					"id": 88, "name": ciGitHubCheckName, "head_sha": sha, "external_id": testID,
					"status": "in_progress", "html_url": stub.URL + "/octo/apex/runs/88", "app": map[string]any{"id": 12345},
				}},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/octo/apex/check-runs":
			state.mu.Lock()
			state.createCalls++
			state.mu.Unlock()
			http.Error(w, "duplicate Check creation", http.StatusConflict)
		default:
			http.Error(w, fmt.Sprintf("unexpected request %s %s", r.Method, r.URL.Path), http.StatusNotFound)
		}
	}))
	defer stub.Close()

	policy := ciTestActionsPolicy()
	policy.ServerURL = stub.URL
	server, store := newCIContractTestServer(t, policy)
	now := time.Now().UTC()
	if err := store.SaveGitHubAppInstallation(&storage.GitHubAppInstallation{
		InstallationID: 101, AccountLogin: "octo", AccountType: "Organization", RepositorySelection: "selected",
		PermissionsJSON: `{"checks":"write"}`, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	settings := server.runner.GetSystemSettings()
	settings.GitHub = GitHubSettings{
		Enabled: true, ChecksEnabled: true, AppID: 12345, AppPrivateKeyEnvVar: "APEXLOAD_CI_CHECK_RECOVERY_KEY",
		APIBaseURL: stub.URL, WebBaseURL: stub.URL,
	}
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}
	script, err := server.runner.StoreScriptDurable(&dsl.Script{
		ID: "ci-check-recovery-script", ProjectID: "project-ci", Name: "Recovery", Version: "1.0.0", Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: "https://api.example.test"}, Actions: []dsl.Action{{Name: "health", Type: "request", Target: "/health"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	source := ciTestRunSource("1")
	source.SHA = sha
	metadata := ciRunMetadata{
		SchemaVersion: ciRunSchemaVersion, Source: source, ScriptIdentity: "script_id:" + script.ID,
		PublishCheck: true, CheckInstallationID: 101, PolicyRevision: policy.Revision, IdempotencySHA: sha256String("restart-recovery"),
	}
	materializationKey, err := encodeCIRunMetadata(metadata)
	if err != nil {
		t.Fatal(err)
	}
	testRun, err := server.runner.CreateTest(CreateTestRequest{
		RequestedTestID: testID, Name: "CI Check recovery", ProjectID: "project-ci", ScriptID: script.ID,
		VUsers: 1, Duration: "1m", MaterializationKey: materializationKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.saveCIGitHubCheckIntent(testRun.ID, testRun.ProjectID, metadata); err != nil {
		t.Fatal(err)
	}
	if links, err := store.ListGitHubCheckLinks(testID, "", 10); err != nil || len(links) != 0 {
		t.Fatalf("pre-restart links = %#v, %v", links, err)
	}

	// Simulate the process stopping after GitHub accepted the Check but before
	// the local link was saved. The durable intent and test must survive reopen.
	dbPath := store.BackendInfo().DSN
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := server.Shutdown(shutdownCtx); err != nil {
		cancel()
		t.Fatal(err)
	}
	cancel()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	restarted := NewServer(ServerConfig{Store: reopened, MaxConcurrent: 4})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = restarted.Shutdown(ctx)
		_ = reopened.Close()
	})
	restarted.reconcileIncompleteCIGitHubChecks(context.Background())

	links, err := reopened.ListGitHubCheckLinks(testID, "", 10)
	if err != nil || len(links) != 1 || links[0].CheckRunID != 88 {
		t.Fatalf("recovered Check link = %#v, %v", links, err)
	}
	intent, err := reopened.GetGitHubCheckIntent(testID)
	if err != nil || intent != nil {
		t.Fatalf("recovered Check left intent = %#v, %v", intent, err)
	}
	_, createCalls, _, _, _ := state.snapshot()
	if createCalls != 0 {
		t.Fatalf("recovery created %d duplicate Checks", createCalls)
	}
}

func TestCreateCIRunRetryRecoversAmbiguousRemoteCheckCreation(t *testing.T) {
	t.Setenv(githubAllowInsecureHTTPEnv, "true")
	t.Setenv("APEXLOAD_PUBLIC_URL", "https://apex.example.test")
	privateKey, _ := testGitHubAppKeyPEM(t)
	t.Setenv("APEXLOAD_CI_CHECK_RETRY_KEY", privateKey)

	state := &ciCheckStubState{}
	remoteAccepted := false
	observedDurableIntent := false
	var server *Server
	var store *storage.SQLiteStore
	var stub *httptest.Server
	stub = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/app/installations/101/access_tokens":
			writeJSON(w, http.StatusCreated, map[string]any{"token": "installation-token", "expires_at": time.Now().UTC().Add(time.Hour)})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/octo/apex/actions/runs/9001":
			writeJSON(w, http.StatusOK, map[string]any{
				"id": 9001, "event": "push", "head_sha": ciTestRunSource("1").SHA,
				"repository": map[string]any{"id": 12345, "full_name": "octo/apex"},
			})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/repos/octo/apex/commits/") && strings.HasSuffix(r.URL.Path, "/check-runs"):
			// This request occurs only after test materialization. Verify the
			// pre-create outbox record is already durable at that boundary.
			intents, intentErr := store.ListPendingGitHubCheckIntents(10)
			externalID := ""
			if intentErr != nil || len(intents) != 1 {
				t.Errorf("Check lookup observed intents %#v, %v", intents, intentErr)
			} else if _, exists := server.runner.GetTest(intents[0].TestID); !exists {
				t.Errorf("Check intent test %q was not materialized", intents[0].TestID)
			} else {
				externalID = intents[0].TestID
				state.mu.Lock()
				observedDurableIntent = true
				state.mu.Unlock()
			}
			state.mu.Lock()
			accepted := remoteAccepted
			state.mu.Unlock()
			if accepted {
				if externalID == "" {
					http.Error(w, "missing durable intent", http.StatusInternalServerError)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{
					"total_count": 1,
					"check_runs": []map[string]any{{
						"id": 88, "name": ciGitHubCheckName, "head_sha": ciTestRunSource("1").SHA,
						"external_id": externalID, "status": "in_progress",
						"html_url": stub.URL + "/octo/apex/runs/88", "app": map[string]any{"id": 12345},
					}},
				})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"total_count": 0, "check_runs": []any{}})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/octo/apex/check-runs":
			state.mu.Lock()
			state.createCalls++
			remoteAccepted = true
			state.mu.Unlock()
			// Simulate GitHub committing the Check while the response is lost.
			http.Error(w, "upstream response lost", http.StatusBadGateway)
		case r.Method == http.MethodPatch && r.URL.Path == "/repos/octo/apex/check-runs/88":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"id": 88, "status": "completed", "conclusion": body["conclusion"],
				"html_url": stub.URL + "/octo/apex/runs/88",
			})
		default:
			http.Error(w, fmt.Sprintf("unexpected request %s %s", r.Method, r.URL.Path), http.StatusNotFound)
		}
	}))
	defer stub.Close()

	policy := ciTestActionsPolicy()
	policy.ServerURL = stub.URL
	server, store = newCIContractTestServer(t, policy)
	project, err := store.GetProject("project-ci")
	if err != nil || project == nil {
		t.Fatalf("GetProject = %#v, %v", project, err)
	}
	project.Settings.GitHub.AppInstallationID = 101
	if err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.SaveGitHubAppInstallation(&storage.GitHubAppInstallation{
		InstallationID: 101, AccountLogin: "octo", AccountType: "Organization", RepositorySelection: "selected",
		PermissionsJSON: `{"checks":"write"}`, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	settings := server.runner.GetSystemSettings()
	settings.GitHub = GitHubSettings{
		Enabled: true, ChecksEnabled: true, AppID: 12345, AppPrivateKeyEnvVar: "APEXLOAD_CI_CHECK_RETRY_KEY",
		APIBaseURL: stub.URL, WebBaseURL: stub.URL,
	}
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}

	publishCheck := true
	scriptRaw, _ := json.Marshal(ciTestScript("https://api.example.test"))
	request := ciRunCreateRequest{
		ProjectID: "project-ci", ScriptPath: "tests/performance.json", Script: scriptRaw,
		Execution: ciRunExecutionRequest{VUsers: 1, Duration: "1m"}, Goals: ciRunGoalsRequest{},
		Source: ciTestRunSource("1"), PublishCheck: &publishCheck,
	}
	request.Source.ServerURL, request.Source.APIURL = stub.URL, stub.URL
	invoke := func() *httptest.ResponseRecorder {
		t.Helper()
		body, marshalErr := json.Marshal(request)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/ci/runs", bytes.NewReader(body))
		req.Header.Set("Idempotency-Key", "ambiguous-check-create-retry-9001-1")
		recorder := httptest.NewRecorder()
		server.handleCreateCIRun(recorder, req)
		return recorder
	}
	first := invoke()
	state.mu.Lock()
	observedIntent := observedDurableIntent
	state.mu.Unlock()
	if first.Code != http.StatusBadGateway || !observedIntent {
		t.Fatalf("first create = %d, body=%s, observed_intent=%t", first.Code, first.Body.String(), observedIntent)
	}
	second := invoke()
	if second.Code != http.StatusAccepted {
		t.Fatalf("retry create = %d, body=%s", second.Code, second.Body.String())
	}
	_, createCalls, _, _, _ := state.snapshot()
	if createCalls != 1 {
		t.Fatalf("ambiguous retry issued %d Check creates, want 1", createCalls)
	}
	var response ciRunResponse
	if err := json.Unmarshal(second.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	links, err := store.ListGitHubCheckLinks(response.TestID, "", 10)
	if err != nil || len(links) != 1 || links[0].CheckRunID != 88 {
		t.Fatalf("recovered retry link = %#v, %v", links, err)
	}
	intent, err := store.GetGitHubCheckIntent(response.TestID)
	if err != nil || intent != nil {
		t.Fatalf("successful retry left intent = %#v, %v", intent, err)
	}
}

func TestCIGitHubCheckConclusionMapping(t *testing.T) {
	for _, test := range []struct{ outcome, want string }{
		{outcome: "passed", want: "success"},
		{outcome: "sla_breached", want: "failure"},
		{outcome: "execution_error", want: "failure"},
		{outcome: "configuration_error", want: "failure"},
		{outcome: "timed_out", want: "failure"},
		{outcome: "cancelled", want: "cancelled"},
	} {
		if got := ciGitHubCheckConclusion(ciRunResponse{Outcome: test.outcome}); got != test.want {
			t.Fatalf("outcome %q conclusion = %q, want %q", test.outcome, got, test.want)
		}
	}
}

func TestCIGitHubCheckSummaryRendersConfiguredSLAResultsDeterministically(t *testing.T) {
	testRun := &ManagedTest{
		Status: "COMPLETE",
		GoalConfig: &GoalConfig{
			P95LatencyMs: ptr(200.0), MaxErrorPct: ptr(1.0),
			MaxAvgServerProcessingMs: ptr(75.0), MaxAvgTransferMs: ptr(40.0), MaxErrorCount: ptr(3),
		},
		LiveMetrics: &LiveMetrics{
			P95Ms: 250, ErrorPct: 0.5, TotalErrors: 4,
			Transactions: map[string]*TxMetrics{
				"first":  {ServerTelemetry: &ServerTelemetrySummary{AvgServerProcessingMs: 50, AvgTransferMs: 45}},
				"second": {ServerTelemetry: &ServerTelemetrySummary{AvgServerProcessingMs: 90, AvgTransferMs: 20}},
			},
		},
	}
	metadata := ciRunMetadata{Source: ciRunSource{Repository: "octo/apex", SHA: "abc", WorkflowRef: "octo/apex/.github/workflows/ci.yml@refs/heads/main", RunID: "1", RunAttempt: "2"}}
	summary := ciGitHubCheckSummary(testRun, ciRunResponse{Status: "COMPLETE", Outcome: "sla_breached", SLAEvaluated: true, SLABreached: true}, metadata)
	wantRows := []string{
		"| P95 latency | ≤ 200.00 ms | 250.00 ms | `FAIL` |",
		"| Error rate | ≤ 1.00% | 0.50% | `PASS` |",
		"| Average server processing | ≤ 75.00 ms | 90.00 ms | `FAIL` |",
		"| Average transfer | ≤ 40.00 ms | 45.00 ms | `FAIL` |",
		"| Error count | ≤ 3 | 4 | `FAIL` |",
	}
	previous := -1
	for _, row := range wantRows {
		index := strings.Index(summary, row)
		if index < 0 {
			t.Fatalf("Check summary missing SLA row %q:\n%s", row, summary)
		}
		if index <= previous {
			t.Fatalf("Check SLA rows are not deterministic:\n%s", summary)
		}
		previous = index
	}

	pending := ciGitHubCheckSummary(&ManagedTest{Status: "QUEUED", GoalConfig: &GoalConfig{P95LatencyMs: ptr(200.0)}}, ciRunResponse{Status: "QUEUED", Outcome: "queued"}, metadata)
	if !strings.Contains(pending, "| P95 latency | ≤ 200.00 ms | Not available | `PENDING` |") {
		t.Fatalf("queued Check did not expose a pending configured SLA:\n%s", pending)
	}
	terminal := ciGitHubCheckSummary(&ManagedTest{Status: "FAILED", GoalConfig: &GoalConfig{P95LatencyMs: ptr(200.0)}}, ciRunResponse{Status: "FAILED", Outcome: "execution_error"}, metadata)
	if !strings.Contains(terminal, "| P95 latency | ≤ 200.00 ms | Not available | `NOT EVALUATED` |") {
		t.Fatalf("failed Check did not expose an unevaluated configured SLA:\n%s", terminal)
	}
}

func TestValidateCICheckPublishingRequiresChecksWrite(t *testing.T) {
	t.Setenv(githubAllowInsecureHTTPEnv, "true")
	t.Setenv("APEXLOAD_PUBLIC_URL", "https://apex.example.test")
	privateKey, _ := testGitHubAppKeyPEM(t)
	t.Setenv("APEXLOAD_CI_CHECK_PERMISSIONS_KEY", privateKey)
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "should not be called", http.StatusInternalServerError)
	}))
	defer stub.Close()
	policy := ciTestActionsPolicy()
	policy.ServerURL = stub.URL
	server, store := newCIContractTestServer(t, policy)
	settings := server.runner.GetSystemSettings()
	settings.GitHub = GitHubSettings{Enabled: true, ChecksEnabled: true, AppID: 1, AppPrivateKeyEnvVar: "APEXLOAD_CI_CHECK_PERMISSIONS_KEY", APIBaseURL: stub.URL, WebBaseURL: stub.URL}
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.SaveGitHubAppInstallation(&storage.GitHubAppInstallation{InstallationID: 101, AccountLogin: "octo", AccountType: "Organization", RepositorySelection: "selected", PermissionsJSON: `{"contents":"read"}`, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	source := ciTestRunSource("1")
	source.ServerURL = stub.URL
	projectPolicy := storage.GitHubProjectPolicy{AppInstallationID: 101, Actions: policy}
	if _, err := server.validateCICheckPublishing(context.Background(), projectPolicy, source); err == nil || !strings.Contains(err.Error(), "Checks: write") {
		t.Fatalf("validateCICheckPublishing error = %v, want Checks: write", err)
	}
}

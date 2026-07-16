package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func ciTestScript(baseURL string) *dsl.Script {
	return &dsl.Script{
		Name: "CI smoke", Version: "1.0.0", Protocol: "http",
		Config:  dsl.ScriptConfig{BaseURL: baseURL},
		Actions: []dsl.Action{{Name: "health", Type: "request", Method: http.MethodGet, Target: "/health"}},
	}
}

func ciTestRunRequest() ciRunCreateRequest {
	return ciRunCreateRequest{
		ProjectID: "project-ci",
		Execution: ciRunExecutionRequest{VUsers: 5, Duration: "2m", RampUp: "10s"},
		Goals:     ciRunGoalsRequest{RequireResults: true},
		Source:    ciTestRunSource("1"),
	}
}

func ciTestRunSource(attempt string) ciRunSource {
	return ciRunSource{
		Provider: "github", ServerURL: "https://github.com", APIURL: "https://api.github.com",
		Repository: "octo/apex", RepositoryID: "12345", Ref: "refs/heads/main",
		SHA:         "0123456789abcdef0123456789abcdef01234567",
		WorkflowRef: "octo/apex/.github/workflows/performance.yml@refs/heads/main",
		RunID:       "9001", RunAttempt: attempt, Actor: "octocat", EventName: "push",
		Environment: "production", RunnerEnvironment: "github-hosted", ScriptPath: "tests/performance.json",
	}
}

func ciRunSourceTestPolicy() storage.GitHubActionsPolicy {
	return ciTestActionsPolicy()
}

func TestValidateCIRunRequestEnforcesPolicyAndSecretBoundary(t *testing.T) {
	basePolicy := ciRunSourceTestPolicy()
	tests := []struct {
		name      string
		mutate    func(*ciRunCreateRequest, *storage.GitHubActionsPolicy, *dsl.Script)
		wantError string
	}{
		{name: "allowed"},
		{name: "vuser cap", mutate: func(req *ciRunCreateRequest, _ *storage.GitHubActionsPolicy, _ *dsl.Script) {
			req.Execution.VUsers = 51
		}, wantError: "vusers exceeds"},
		{name: "duration cap", mutate: func(req *ciRunCreateRequest, _ *storage.GitHubActionsPolicy, _ *dsl.Script) {
			req.Execution.Duration = "31m"
		}, wantError: "duration exceeds"},
		{name: "protocol", mutate: func(_ *ciRunCreateRequest, _ *storage.GitHubActionsPolicy, script *dsl.Script) {
			script.Protocol = "kafka"
		}, wantError: "protocol"},
		{name: "target host", mutate: func(_ *ciRunCreateRequest, _ *storage.GitHubActionsPolicy, script *dsl.Script) {
			script.Config.BaseURL = "https://attacker.example"
		}, wantError: "target host"},
		{name: "dynamic target", mutate: func(_ *ciRunCreateRequest, _ *storage.GitHubActionsPolicy, script *dsl.Script) {
			script.Config.BaseURL = "https://{{env.TARGET_HOST}}"
		}, wantError: "dynamic target"},
		{name: "template action target", mutate: func(_ *ciRunCreateRequest, _ *storage.GitHubActionsPolicy, script *dsl.Script) {
			script.Actions[0].Target = "{{var.target_url}}"
		}, wantError: "dynamic target"},
		{name: "OAuth token target", mutate: func(_ *ciRunCreateRequest, _ *storage.GitHubActionsPolicy, script *dsl.Script) {
			script.Actions = append(script.Actions, dsl.Action{Name: "token", Type: "http_oauth2_token", Params: map[string]any{"token_url": "https://attacker.example/token"}})
		}, wantError: "target host"},
		{name: "sequence child target", mutate: func(_ *ciRunCreateRequest, _ *storage.GitHubActionsPolicy, script *dsl.Script) {
			script.Actions = []dsl.Action{{Name: "sequence", Type: "http_sequence", Params: map[string]any{"requests": []any{map[string]any{"name": "child", "type": "request", "method": "GET", "target": "https://attacker.example/child"}}}}}
		}, wantError: "target host"},
		{name: "browser open-window param target", mutate: func(_ *ciRunCreateRequest, policy *storage.GitHubActionsPolicy, script *dsl.Script) {
			script.Protocol = "browser"
			policy.AllowedProtocols = append(policy.AllowedProtocols, "browser")
			script.Actions = []dsl.Action{{Name: "popup", Type: "browser_open_window", Params: map[string]any{"url": "https://attacker.example/window"}}}
		}, wantError: "target host"},
		{name: "browser proxy target", mutate: func(_ *ciRunCreateRequest, policy *storage.GitHubActionsPolicy, script *dsl.Script) {
			script.Protocol = "browser"
			policy.AllowedProtocols = append(policy.AllowedProtocols, "browser")
			script.Config.Browser.Proxy.Server = "http://attacker.example:3128"
		}, wantError: "target host"},
		{name: "plaintext script credential", mutate: func(_ *ciRunCreateRequest, _ *storage.GitHubActionsPolicy, script *dsl.Script) {
			script.Config.Database.DSN = "postgres://admin:password@db.example.test/app"
		}, wantError: "plaintext credential"},
		{name: "gRPC proto filesystem path", mutate: func(_ *ciRunCreateRequest, _ *storage.GitHubActionsPolicy, script *dsl.Script) {
			script.Config.GRPC.ProtoFiles = []string{"/shared/other-project/service.proto"}
		}, wantError: "filesystem paths"},
		{name: "browser upload filesystem path", mutate: func(_ *ciRunCreateRequest, policy *storage.GitHubActionsPolicy, script *dsl.Script) {
			script.Protocol = "browser"
			policy.AllowedProtocols = append(policy.AllowedProtocols, "browser")
			script.Actions = []dsl.Action{{Name: "upload", Type: "browser_set_files", Selector: "#file", Params: map[string]any{"files": []any{"/shared/other-project/secret.txt"}}}}
		}, wantError: "cannot read filesystem"},
		{name: "plaintext variable credential", mutate: func(req *ciRunCreateRequest, _ *storage.GitHubActionsPolicy, _ *dsl.Script) {
			req.Execution.Variables = map[string]string{"api_token": "plaintext"}
		}, wantError: "controller-managed project secret"},
		{name: "required SLA missing threshold", mutate: func(req *ciRunCreateRequest, _ *storage.GitHubActionsPolicy, _ *dsl.Script) {
			req.Goals.RequireSLA = true
		}, wantError: "no SLA threshold"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := ciTestRunRequest()
			policy := basePolicy
			script := ciTestScript("https://api.example.test")
			if test.mutate != nil {
				test.mutate(&req, &policy, script)
			}
			err := validateCIRunRequest(req, policy, script)
			if test.wantError == "" && err != nil {
				t.Fatalf("validateCIRunRequest returned error: %v", err)
			}
			if test.wantError != "" && (err == nil || !strings.Contains(err.Error(), test.wantError)) {
				t.Fatalf("validateCIRunRequest error = %v, want substring %q", err, test.wantError)
			}
		})
	}

	req := ciTestRunRequest()
	script := ciTestScript("https://api.example.test")
	script.Config.Database.DSN = "{{secret.database_dsn}}"
	if err := validateCIRunRequest(req, basePolicy, script); err != nil {
		t.Fatalf("controller-managed secret reference rejected: %v", err)
	}
}

func TestCollectCITargetHostsCoversProtocolConnectionForms(t *testing.T) {
	tests := []struct {
		name     string
		script   *dsl.Script
		want     string
		deferred bool
	}{
		{
			name: "database multi-host DSN",
			script: &dsl.Script{Protocol: "database", Config: dsl.ScriptConfig{Database: dsl.DatabaseConfig{
				Driver: "postgres", DSN: "host=db-a.example.test,db-b.example.test port=5432 dbname=app",
			}}},
			want: "db-a.example.test,db-b.example.test",
		},
		{
			name:   "explicit gRPC resolver target",
			script: &dsl.Script{Protocol: "grpc", Config: dsl.ScriptConfig{BaseURL: "dns:///grpc.example.test:443"}},
			want:   "grpc.example.test",
		},
		{
			name: "Service Bus connection string",
			script: &dsl.Script{Protocol: "jms-servicebus", Config: dsl.ScriptConfig{JMS: dsl.JMSConfig{
				BrokerURL: "Endpoint=sb://orders.servicebus.windows.net/;SharedAccessKeyName=name;SharedAccessKey=secret",
			}}},
			want: "orders.servicebus.windows.net",
		},
		{
			name: "Kafka logical topic is not a target host",
			script: &dsl.Script{Protocol: "kafka", Config: dsl.ScriptConfig{Kafka: dsl.KafkaConfig{
				Brokers: []string{"broker.example.test:9092"},
			}}, Actions: []dsl.Action{{Type: "kafka_produce", Target: "orders.created"}}},
			want: "broker.example.test",
		},
		{
			name: "controller-managed database DSN",
			script: &dsl.Script{Protocol: "database", Config: dsl.ScriptConfig{Database: dsl.DatabaseConfig{
				Driver: "postgres", DSN: "{{secret.database_dsn}}",
			}}},
			deferred: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			hosts, deferred, err := collectCITargetHosts(test.script, "")
			if err != nil || strings.Join(hosts, ",") != test.want || deferred != test.deferred {
				t.Fatalf("collectCITargetHosts = %v, deferred=%t, err=%v; want %q, deferred=%t", hosts, deferred, err, test.want, test.deferred)
			}
		})
	}
}

func TestValidateCIRunAllowsControllerResolvedDatabaseTarget(t *testing.T) {
	req := ciTestRunRequest()
	policy := ciRunSourceTestPolicy()
	policy.AllowedProtocols = []string{"database"}
	policy.AllowedTargetHosts = []string{"db.example.test"}
	script := &dsl.Script{
		Name: "database CI", Version: "1.0.0", Protocol: "database",
		Config:  dsl.ScriptConfig{Database: dsl.DatabaseConfig{Driver: "postgres", DSN: "{{secret.database_dsn}}"}},
		Actions: []dsl.Action{{Name: "health", Type: "db_health"}},
	}
	if err := validateCIRunRequest(req, policy, script); err != nil {
		t.Fatalf("controller-resolved database target rejected before runtime enforcement: %v", err)
	}
}

func TestTrustedGitHubAPIURLGitHubComAndGHES(t *testing.T) {
	tests := []struct {
		name, serverURL, apiURL string
		want                    bool
	}{
		{name: "github dot com API", serverURL: "https://github.com", apiURL: "https://api.github.com", want: true},
		{name: "github same origin", serverURL: "https://github.com", apiURL: "https://github.com/api/v3", want: true},
		{name: "github API cleartext", serverURL: "https://github.com", apiURL: "http://api.github.com", want: false},
		{name: "github API alternate port", serverURL: "https://github.com", apiURL: "https://api.github.com:8443", want: false},
		{name: "github lookalike", serverURL: "https://github.com", apiURL: "https://api.github.com.evil.test", want: false},
		{name: "ghes API path", serverURL: "https://github.corp.example", apiURL: "https://github.corp.example/api/v3", want: true},
		{name: "ghes alternate host", serverURL: "https://github.corp.example", apiURL: "https://api.github.corp.example", want: false},
		{name: "ghes cleartext", serverURL: "https://github.corp.example", apiURL: "http://github.corp.example/api/v3", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := trustedGitHubAPIURL(test.serverURL, test.apiURL); got != test.want {
				t.Fatalf("trustedGitHubAPIURL(%q, %q) = %t, want %t", test.serverURL, test.apiURL, got, test.want)
			}
		})
	}
}

func TestValidateCIRunSourceRejectsForksAndPullRequestTarget(t *testing.T) {
	policy := ciRunSourceTestPolicy()
	tests := []struct {
		name      string
		mutate    func(*ciRunSource)
		wantError string
	}{
		{name: "trusted push"},
		{name: "trusted same repository pull request", mutate: func(source *ciRunSource) {
			source.EventName = "pull_request"
			source.HeadRepository, source.HeadRepositoryID = source.Repository, source.RepositoryID
		}},
		{name: "fork repository name", mutate: func(source *ciRunSource) {
			source.EventName = "pull_request"
			source.HeadRepository, source.HeadRepositoryID = "contributor/apex", source.RepositoryID
		}, wantError: "fork pull requests"},
		{name: "fork repository id", mutate: func(source *ciRunSource) {
			source.EventName = "pull_request"
			source.HeadRepository, source.HeadRepositoryID = source.Repository, "99999"
		}, wantError: "fork pull requests"},
		{name: "missing PR head metadata", mutate: func(source *ciRunSource) { source.EventName = "pull_request" }, wantError: "fork pull requests"},
		{name: "pull request target", mutate: func(source *ciRunSource) { source.EventName = "pull_request_target" }, wantError: "pull_request_target"},
		{name: "untrusted API URL", mutate: func(source *ciRunSource) { source.APIURL = "https://api.github.com.evil.test" }, wantError: "source.api_url"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := ciTestRunSource("1")
			if test.mutate != nil {
				test.mutate(&source)
			}
			err := validateCIRunSource(policy, source)
			if test.wantError == "" && err != nil {
				t.Fatalf("validateCIRunSource returned error: %v", err)
			}
			if test.wantError != "" && (err == nil || !strings.Contains(err.Error(), test.wantError)) {
				t.Fatalf("validateCIRunSource error = %v, want substring %q", err, test.wantError)
			}
		})
	}
}

func TestValidateGitHubWorkflowRunSourceRejectsForgedMetadata(t *testing.T) {
	source := ciTestRunSource("1")
	baseRun := githubWorkflowRun{
		ID: 9001, Event: "push", HeadBranch: "main", HeadSHA: source.SHA,
		Path: ".github/workflows/performance.yml@main", RunAttempt: 1,
		Actor:          githubWorkflowRunActor{ID: 42, Login: "octocat"},
		Repository:     githubWorkflowRunRepository{ID: 12345, FullName: "octo/apex"},
		HeadRepository: githubWorkflowRunRepository{ID: 12345, FullName: "octo/apex"},
	}
	tests := []struct {
		name      string
		mutate    func(*githubWorkflowRun, *ciRunSource)
		wantError string
	}{
		{name: "verified push"},
		{name: "repository", mutate: func(run *githubWorkflowRun, _ *ciRunSource) { run.Repository.ID = 999 }, wantError: "repository"},
		{name: "commit", mutate: func(run *githubWorkflowRun, _ *ciRunSource) { run.HeadSHA = strings.Repeat("f", 40) }, wantError: "commit"},
		{name: "event", mutate: func(run *githubWorkflowRun, _ *ciRunSource) { run.Event = "workflow_dispatch" }, wantError: "event"},
		{name: "attempt", mutate: func(run *githubWorkflowRun, _ *ciRunSource) { run.RunAttempt = 2 }, wantError: "attempt"},
		{name: "actor", mutate: func(run *githubWorkflowRun, _ *ciRunSource) { run.Actor.Login = "mallory" }, wantError: "actor"},
		{name: "workflow", mutate: func(run *githubWorkflowRun, _ *ciRunSource) { run.Path = ".github/workflows/untrusted.yml@main" }, wantError: "workflow path"},
		{name: "workflow ref", mutate: func(_ *githubWorkflowRun, source *ciRunSource) {
			source.WorkflowRef = "octo/apex/.github/workflows/performance.yml@refs/heads/feature"
		}, wantError: "workflow ref"},
		{name: "ref", mutate: func(run *githubWorkflowRun, _ *ciRunSource) { run.HeadBranch = "feature" }, wantError: "branch or tag"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			run, candidate := baseRun, source
			if test.mutate != nil {
				test.mutate(&run, &candidate)
			}
			err := validateGitHubWorkflowRunSource(&run, candidate)
			if test.wantError == "" && err != nil {
				t.Fatalf("validateGitHubWorkflowRunSource returned error: %v", err)
			}
			if test.wantError != "" && (err == nil || !strings.Contains(err.Error(), test.wantError)) {
				t.Fatalf("validateGitHubWorkflowRunSource error = %v, want %q", err, test.wantError)
			}
		})
	}
}

func TestValidateGitHubWorkflowRunSourceRejectsForkPullRequest(t *testing.T) {
	source := ciTestRunSource("1")
	source.EventName = "pull_request"
	source.Ref = "refs/pull/17/merge"
	source.WorkflowRef = "octo/apex/.github/workflows/performance.yml@refs/pull/17/merge"
	source.HeadRepository, source.HeadRepositoryID = source.Repository, source.RepositoryID
	source.HeadSHA = strings.Repeat("a", 40)
	run := &githubWorkflowRun{
		ID: 9001, Event: "pull_request", HeadBranch: "feature", HeadSHA: source.HeadSHA,
		Path: ".github/workflows/performance.yml@main", RunAttempt: 1,
		Actor:          githubWorkflowRunActor{Login: source.Actor},
		Repository:     githubWorkflowRunRepository{ID: 12345, FullName: "octo/apex"},
		HeadRepository: githubWorkflowRunRepository{ID: 999, FullName: "contributor/apex"},
		PullRequests: []githubWorkflowRunPullRequest{{
			Number: 17, MergeCommitSHA: source.SHA,
			Head: githubWorkflowRunPullRequestRef{SHA: source.HeadSHA, Repo: githubWorkflowRunRepository{ID: 999, FullName: "contributor/apex"}},
			Base: githubWorkflowRunPullRequestRef{Repo: githubWorkflowRunRepository{ID: 12345, FullName: "octo/apex"}},
		}},
	}
	if err := validateGitHubWorkflowRunSource(run, source); err == nil || !strings.Contains(err.Error(), "fork pull requests") {
		t.Fatalf("fork validation error = %v", err)
	}
	run.HeadRepository = run.Repository
	run.PullRequests[0].Head.Repo = run.Repository
	if err := validateGitHubWorkflowRunSource(run, source); err != nil {
		t.Fatalf("same-repository pull request rejected: %v", err)
	}
}

func TestGitHubClientListsPullRequestsForCommit(t *testing.T) {
	t.Setenv(githubAllowInsecureHTTPEnv, "true")
	sha := strings.Repeat("a", 40)
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/repos/octo/apex/commits/"+sha+"/pulls" {
			http.Error(w, "unexpected request", http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, []map[string]any{{
			"number": 17, "merge_commit_sha": strings.Repeat("b", 40),
			"head": map[string]any{"sha": sha, "repo": map[string]any{"id": 12345, "full_name": "octo/apex"}},
			"base": map[string]any{"sha": strings.Repeat("c", 40), "repo": map[string]any{"id": 12345, "full_name": "octo/apex"}},
		}})
	}))
	defer stub.Close()
	client, err := newGitHubClient(GitHubSettings{APIBaseURL: stub.URL, WebBaseURL: stub.URL}, "token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	pulls, err := client.ListPullRequestsForCommit(context.Background(), "octo", "apex", sha)
	if err != nil || len(pulls) != 1 || pulls[0].Number != 17 || pulls[0].Head.SHA != sha || pulls[0].MergeCommitSHA != strings.Repeat("b", 40) {
		t.Fatalf("ListPullRequestsForCommit = %#v, %v", pulls, err)
	}
}

func TestAPIKeyFallbackVerifiesWorkflowRunWithoutPublishingCheck(t *testing.T) {
	t.Setenv(githubAllowInsecureHTTPEnv, "true")
	privateKey, _ := testGitHubAppKeyPEM(t)
	t.Setenv("APEXLOAD_CI_API_KEY_APP", privateKey)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer target.Close()

	var workflowCalls int
	var githubStub *httptest.Server
	githubStub = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/app/installations/101/access_tokens":
			writeJSON(w, http.StatusCreated, map[string]any{"token": "installation-token", "expires_at": time.Now().UTC().Add(time.Hour)})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/octo/apex/actions/runs/9001":
			workflowCalls++
			writeJSON(w, http.StatusOK, map[string]any{
				"id": 9001, "event": "push", "head_branch": "main", "head_sha": ciTestRunSource("1").SHA,
				"path": ".github/workflows/performance.yml@main", "run_attempt": 1,
				"actor":           map[string]any{"id": 42, "login": "octocat"},
				"repository":      map[string]any{"id": 12345, "full_name": "octo/apex"},
				"head_repository": map[string]any{"id": 12345, "full_name": "octo/apex"},
			})
		default:
			http.Error(w, fmt.Sprintf("unexpected request %s %s", r.Method, r.URL.Path), http.StatusNotFound)
		}
	}))
	defer githubStub.Close()

	policy := ciTestActionsPolicy()
	policy.ServerURL = githubStub.URL
	policy.AllowedEnvironments = nil
	policy.RequiredRunnerEnvironment = ""
	policy.AllowedTargetHosts = []string{"127.0.0.1"}
	server, store := newCIContractTestServer(t, policy)
	project, err := store.GetProject("project-ci")
	if err != nil || project == nil {
		t.Fatalf("GetProject = %#v, %v", project, err)
	}
	project.Settings.GitHub.AppInstallationID = 101
	if err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	settings := server.runner.GetSystemSettings()
	settings.GitHub = GitHubSettings{Enabled: true, AppID: 1, AppPrivateKeyEnvVar: "APEXLOAD_CI_API_KEY_APP", APIBaseURL: githubStub.URL, WebBaseURL: githubStub.URL}
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}

	publishCheck := false
	scriptRaw, _ := json.Marshal(ciTestScript(target.URL))
	reqBody := ciRunCreateRequest{
		ProjectID: "project-ci", Script: scriptRaw, ScriptPath: "tests/performance.json",
		Execution: ciRunExecutionRequest{VUsers: 1, Duration: "50ms"}, Goals: ciRunGoalsRequest{},
		Source: ciTestRunSource("1"), PublishCheck: &publishCheck,
	}
	reqBody.Source.ServerURL, reqBody.Source.APIURL = githubStub.URL, githubStub.URL
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ci/runs", bytes.NewReader(body))
	req.Header.Set("Idempotency-Key", "api-key-fallback-workflow-run-verification")
	req = withAuthenticatedContext(req, authResolution{
		User:           authenticatedUserFromServiceAccount(&storage.ServiceAccount{ID: "ci-key"}),
		ServiceAccount: &AuthenticatedServiceAccount{ID: "ci-key", Scopes: []string{"tests:run", "results:read"}},
	})
	recorder := httptest.NewRecorder()
	server.handleCreateCIRun(recorder, req)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("API-key CI create status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	if workflowCalls != 1 {
		t.Fatalf("workflow-run verification calls = %d, want 1", workflowCalls)
	}
}

func TestAPIKeyFallbackRequiresOIDCForJobScopedPolicy(t *testing.T) {
	policy := ciTestActionsPolicy()
	server, _ := newCIContractTestServer(t, policy)
	if _, err := server.verifyAPIKeyCISource(context.Background(), storage.GitHubProjectPolicy{AppInstallationID: 101, Actions: policy}, ciTestRunSource("1")); err == nil || !strings.Contains(err.Error(), "use GitHub OIDC") {
		t.Fatalf("verifyAPIKeyCISource error = %v", err)
	}
}

func TestCreateCIRunIdempotencyReusesAttemptAndSeparatesNewAttempt(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()

	policy := ciRunSourceTestPolicy()
	policy.MaxDurationMinutes = 1
	policy.AllowedTargetHosts = []string{"127.0.0.1"}
	server, _ := newCIContractTestServer(t, policy)
	publishCheck := false
	scriptRaw, err := json.Marshal(ciTestScript(target.URL))
	if err != nil {
		t.Fatal(err)
	}
	request := ciRunCreateRequest{
		ProjectID: "project-ci", ScriptPath: "tests/performance.json", Script: scriptRaw,
		Execution: ciRunExecutionRequest{VUsers: 1, Duration: "50ms"},
		Goals:     ciRunGoalsRequest{RequireSLA: false, RequireResults: false}, Source: ciTestRunSource("1"),
		PublishCheck: &publishCheck,
	}

	invoke := func(key string, req ciRunCreateRequest) (int, ciRunResponse) {
		t.Helper()
		body, marshalErr := json.Marshal(req)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/ci/runs", bytes.NewReader(body))
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Idempotency-Key", key)
		recorder := httptest.NewRecorder()
		server.handleCreateCIRun(recorder, httpReq)
		var response ciRunResponse
		if recorder.Code == http.StatusAccepted {
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode CI run response: %v; body=%s", err, recorder.Body.String())
			}
		}
		return recorder.Code, response
	}

	const firstKey = "octo/apex:performance:9001:1:script-sha256-aaaaaaaa"
	status, first := invoke(firstKey, request)
	if status != http.StatusAccepted || first.TestID == "" {
		t.Fatalf("first create = %d, %#v", status, first)
	}
	status, retry := invoke(firstKey, request)
	if status != http.StatusAccepted || retry.TestID != first.TestID {
		t.Fatalf("retry = %d, id %q; want %q", status, retry.TestID, first.TestID)
	}

	changedAttemptSameKey := request
	changedAttemptSameKey.Source.RunAttempt = "2"
	if status, _ := invoke(firstKey, changedAttemptSameKey); status != http.StatusConflict {
		t.Fatalf("changed attempt with reused key status = %d, want 409", status)
	}

	const secondKey = "octo/apex:performance:9001:2:script-sha256-aaaaaaaa"
	status, second := invoke(secondKey, changedAttemptSameKey)
	if status != http.StatusAccepted || second.TestID == "" || second.TestID == first.TestID {
		t.Fatalf("new attempt = %d, %#v; first id=%q", status, second, first.TestID)
	}
}

func TestCIRunOutcomeMapping(t *testing.T) {
	tests := []struct {
		name, status, want string
		breached, results  bool
		metadata           ciRunMetadata
	}{
		{name: "complete passed", status: "COMPLETE", results: true, want: "passed"},
		{name: "complete SLA breach", status: "COMPLETE", breached: true, results: true, want: "sla_breached"},
		{name: "complete required SLA without results", status: "COMPLETE", metadata: ciRunMetadata{RequireSLA: true}, want: "execution_error"},
		{name: "complete required results missing", status: "COMPLETE", metadata: ciRunMetadata{RequireResults: true}, want: "execution_error"},
		{name: "complete optional empty", status: "COMPLETE", want: "passed"},
		{name: "failed", status: "FAILED", want: "execution_error"},
		{name: "aborted", status: "ABORTED", want: "cancelled"},
		{name: "queued", status: "QUEUED", want: "queued"},
		{name: "running", status: "RUNNING", want: "running"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ciRunOutcome(test.status, test.breached, test.results, test.metadata); got != test.want {
				t.Fatalf("ciRunOutcome = %q, want %q", got, test.want)
			}
		})
	}
}

func TestCIClaimsMatchRunAttemptAndImmutableSource(t *testing.T) {
	identity := ciTestIdentity()
	claims := &ciBearerClaims{
		ProjectID: "project-ci", Repository: identity.Repository, RepositoryID: identity.RepositoryID,
		Ref: identity.Ref, SHA: identity.SHA, WorkflowRef: identity.WorkflowRef,
		RunID: identity.RunID, RunAttempt: identity.RunAttempt, EventName: identity.EventName,
	}
	source := ciTestRunSource("1")
	if err := claims.matchCIRunSource("project-ci", source); err != nil {
		t.Fatalf("matchCIRunSource rejected matching source: %v", err)
	}
	source.RunAttempt = "2"
	if err := claims.matchCIRunSource("project-ci", source); err == nil || !strings.Contains(err.Error(), "run_attempt") {
		t.Fatalf("matchCIRunSource attempt error = %v", err)
	}
}

func TestValidateCIRunIdempotencyKeyBoundary(t *testing.T) {
	if _, err := validateCIRunIdempotencyKey(strings.Repeat("a", 16)); err != nil {
		t.Fatalf("minimum valid key rejected: %v", err)
	}
	for _, raw := range []string{strings.Repeat("a", 15), strings.Repeat("a", 513), "valid-looking-key\nforged"} {
		if _, err := validateCIRunIdempotencyKey(raw); err == nil {
			t.Fatalf("invalid key accepted: %q", raw)
		}
	}
}

func TestCIRunDurationPolicyRejectsOverflowingPolicyLimit(t *testing.T) {
	req := ciTestRunRequest()
	req.Execution.Duration = time.Minute.String()
	policy := ciRunSourceTestPolicy()
	policy.MaxDurationMinutes = int(^uint(0) >> 1)
	if err := validateCIRunRequest(req, policy, ciTestScript("https://api.example.test")); err == nil || !strings.Contains(err.Error(), "duration exceeds") {
		t.Fatalf("overflowing policy duration error = %v", err)
	}
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseCIArgsControllerAndLocalValidation(t *testing.T) {
	t.Setenv("APEXLOAD_API_KEY", "env-key")
	controller, err := parseCIArgs([]string{
		"--controller-url", "https://controller.example",
		"--project-id", "project-1",
		"--script-id", "script-1",
		"--auth-mode", "api-key",
		"--duration", "2m",
		"--ramp-up", "10s",
	})
	if err != nil {
		t.Fatalf("parse controller args: %v", err)
	}
	if controller.apiKey != "env-key" || controller.duration != 2*time.Minute || controller.scriptID != "script-1" {
		t.Fatalf("controller args = %#v", controller)
	}

	local, err := parseCIArgs([]string{
		"--execution-mode", "local",
		"--publish-check=false",
		"--script-path", "script.json",
	})
	if err != nil {
		t.Fatalf("parse local args: %v", err)
	}
	if local.publishCheck || local.executionMode != "local" {
		t.Fatalf("local args = %#v", local)
	}

	if _, err := parseCIArgs([]string{
		"--controller-url", "https://controller.example",
		"--project-id", "project-1",
		"--script-id", "script-1",
		"--script-path", "script.json",
	}); err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("both script sources error = %v", err)
	}
}

func TestValidateControllerURLFailsClosed(t *testing.T) {
	t.Setenv("APEXLOAD_ALLOW_INSECURE_HTTP", "")
	for _, raw := range []string{
		"http://controller.example",
		"https://user:password@controller.example",
		"https://controller.example?token=x",
		"not-a-url",
	} {
		if err := validateControllerURL(raw); err == nil {
			t.Errorf("validateControllerURL(%q) unexpectedly passed", raw)
		}
	}
	if err := validateControllerURL("http://127.0.0.1:8080"); err != nil {
		t.Fatalf("localhost development URL rejected: %v", err)
	}
}

func TestLoadCIVariablesRejectsSensitiveKeys(t *testing.T) {
	dir := t.TempDir()
	valid := filepath.Join(dir, "valid.json")
	if err := os.WriteFile(valid, []byte(`{"region":"eastus","namespace":"pr-1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	variables, err := loadCIVariables(valid)
	if err != nil || variables["namespace"] != "pr-1" {
		t.Fatalf("variables=%v err=%v", variables, err)
	}
	invalid := filepath.Join(dir, "invalid.json")
	if err := os.WriteFile(invalid, []byte(`{"api_token":"plaintext"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCIVariables(invalid); err == nil || !strings.Contains(err.Error(), "controller-managed") {
		t.Fatalf("sensitive variable error = %v", err)
	}
}

func TestSourceFromEnvironmentIncludesForkIdentity(t *testing.T) {
	eventPath := filepath.Join(t.TempDir(), "event.json")
	event := `{"pull_request":{"head":{"sha":"head-sha","repo":{"id":222,"full_name":"fork/repo"}}}}`
	if err := os.WriteFile(eventPath, []byte(event), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_REPOSITORY", "base/repo")
	t.Setenv("GITHUB_REPOSITORY_ID", "111")
	t.Setenv("GITHUB_EVENT_PATH", eventPath)
	source := sourceFromEnvironment()
	if source.Provider != "github" || source.HeadRepository != "fork/repo" || source.HeadRepositoryID != "222" || source.HeadSHA != "head-sha" {
		t.Fatalf("source = %#v", source)
	}
}

func TestCreateCIRunContractAndIdempotencyHeader(t *testing.T) {
	var captured CIRunRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/ci/runs" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Idempotency-Key"); got != "key-1" {
			t.Fatalf("Idempotency-Key = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"schema_version":1,"test_id":"test-1","status":"QUEUED","outcome":"","status_url":"/api/v1/ci/runs/test-1"}`)
	}))
	defer server.Close()

	client := NewApiClientWithAuth(server.URL, "key", "")
	response, err := client.CreateCIRun(context.Background(), CIRunRequest{
		ProjectID: "project-1", ScriptID: "script-1",
		Execution: CIExecution{VUsers: 5, Duration: "1m", RampUp: "5s"},
		Source:    CISource{Provider: "github", Repository: "owner/repo"},
	}, "key-1")
	if err != nil {
		t.Fatalf("CreateCIRun: %v", err)
	}
	if response.TestID != "test-1" || captured.ProjectID != "project-1" || captured.Execution.VUsers != 5 {
		t.Fatalf("response=%#v request=%#v", response, captured)
	}
}

func TestCIAPIErrorIsTypedAndDoesNotLeakResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":"reflected-super-secret-token"}`)
	}))
	defer server.Close()

	client := NewApiClientWithAuth(server.URL, "key", "")
	_, err := client.CreateCIRun(context.Background(), CIRunRequest{}, "idempotency-key")
	var apiErr *CIAPIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("CreateCIRun error = %T %v, want *CIAPIError", err, err)
	}
	if apiErr.StatusCode != http.StatusForbidden || apiErr.Method != http.MethodPost || apiErr.Path != "/api/v1/ci/runs" {
		t.Fatalf("CIAPIError = %#v", apiErr)
	}
	if strings.Contains(err.Error(), "super-secret-token") {
		t.Fatalf("safe error leaked response body: %v", err)
	}
}

func TestCIFailureOutcomeClassifiesControllerAndTransportErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "bad request", err: &CIAPIError{StatusCode: http.StatusBadRequest}, want: ciOutcomeConfigurationError},
		{name: "forbidden policy", err: &CIAPIError{StatusCode: http.StatusForbidden}, want: ciOutcomeConfigurationError},
		{name: "missing project", err: &CIAPIError{StatusCode: http.StatusNotFound}, want: ciOutcomeConfigurationError},
		{name: "idempotency conflict", err: &CIAPIError{StatusCode: http.StatusConflict}, want: ciOutcomeConfigurationError},
		{name: "rate limit", err: &CIAPIError{StatusCode: http.StatusTooManyRequests}, want: ciOutcomeExecutionError},
		{name: "controller unavailable", err: &CIAPIError{StatusCode: http.StatusServiceUnavailable}, want: ciOutcomeExecutionError},
		{name: "network", err: errors.New("connection refused"), want: ciOutcomeExecutionError},
		{name: "local configuration", err: ciConfigurationErrorf("missing setting"), want: ciOutcomeConfigurationError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ciFailureOutcome(test.err); got != test.want {
				t.Fatalf("ciFailureOutcome(%v) = %q, want %q", test.err, got, test.want)
			}
		})
	}
}

func TestExecuteControllerCIMapsCreateHTTPFailures(t *testing.T) {
	script := filepath.Join(t.TempDir(), "script.json")
	if err := os.WriteFile(script, []byte(`{"name":"ci","protocol":"http","actions":[{"name":"home","type":"request","method":"GET","target":"https://example.com"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		status int
		want   string
	}{
		{status: http.StatusBadRequest, want: ciOutcomeConfigurationError},
		{status: http.StatusForbidden, want: ciOutcomeConfigurationError},
		{status: http.StatusNotFound, want: ciOutcomeConfigurationError},
		{status: http.StatusConflict, want: ciOutcomeConfigurationError},
		{status: http.StatusTooManyRequests, want: ciOutcomeExecutionError},
		{status: http.StatusInternalServerError, want: ciOutcomeExecutionError},
	}
	for _, test := range tests {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, `{"error":"must-not-appear-in-error"}`)
			}))
			defer server.Close()
			args := defaultCIArgs()
			args.controllerURL, args.projectID, args.scriptPath = server.URL, "project-1", script
			args.authMode, args.apiKey = "api-key", "key-1"
			result, err := executeControllerCI(context.Background(), args, nil)
			if err == nil || result.Outcome != test.want || result.Status != "CREATE_FAILED" {
				t.Fatalf("result=%#v err=%v", result, err)
			}
			if strings.Contains(err.Error(), "must-not-appear") {
				t.Fatalf("controller body leaked: %v", err)
			}
		})
	}
}

func TestRequestGitHubIDTokenSendsExactAudience(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("audience"); got != "apexload://controller-1" {
			t.Fatalf("audience = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer request-token" {
			t.Fatalf("authorization = %q", got)
		}
		_, _ = io.WriteString(w, `{"value":"github-id-token"}`)
	}))
	defer server.Close()
	originalFactory := newGitHubOIDCHTTPClient
	newGitHubOIDCHTTPClient = func() *http.Client { return server.Client() }
	t.Cleanup(func() { newGitHubOIDCHTTPClient = originalFactory })
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", server.URL+"?existing=value")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "request-token")
	t.Setenv("GITHUB_SERVER_URL", server.URL)

	token, err := requestGitHubIDToken(context.Background(), "apexload://controller-1")
	if err != nil || token != "github-id-token" {
		t.Fatalf("token=%q err=%v", token, err)
	}
}

func TestValidateGitHubOIDCRequestOriginRejectsCredentialExfiltration(t *testing.T) {
	t.Setenv("GITHUB_SERVER_URL", "https://github.com")
	malicious, _ := url.Parse("https://attacker.example/oidc")
	if err := validateGitHubOIDCRequestOrigin(malicious); err == nil {
		t.Fatal("unexpected OIDC host was accepted")
	}
	trusted, _ := url.Parse("https://pipelines.actions.githubusercontent.com/token")
	if err := validateGitHubOIDCRequestOrigin(trusted); err != nil {
		t.Fatalf("GitHub.com OIDC host rejected: %v", err)
	}
}

func TestExecuteControllerCIPollsTerminalResult(t *testing.T) {
	oldPoll := ciPollInterval
	ciPollInterval = time.Millisecond
	t.Cleanup(func() { ciPollInterval = oldPoll })
	var statusCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-APEXLoad-API-Key"); got != "key-1" {
			t.Fatalf("API key = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/ci/runs":
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"schema_version":1,"test_id":"test-1","status":"QUEUED"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/ci/runs/test-1":
			statusCalls.Add(1)
			_, _ = io.WriteString(w, `{"schema_version":1,"test_id":"test-1","status":"COMPLETE","outcome":"passed","sla_evaluated":true,"sla_breached":false,"transactions":20,"errors":0,"tps":10,"duration_ms":2000,"report_url":"https://reports.example/test-1","check_url":"https://github.example/checks/1"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("APEXLOAD_ALLOW_INSECURE_HTTP", "true")
	script := filepath.Join(t.TempDir(), "script.json")
	if err := os.WriteFile(script, []byte(`{"name":"ci","protocol":"http","actions":[{"name":"home","type":"request","method":"GET","target":"https://example.com"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	args := defaultCIArgs()
	args.controllerURL, args.projectID, args.scriptPath = server.URL, "project-1", script
	args.authMode, args.apiKey = "api-key", "key-1"
	result, err := executeControllerCI(context.Background(), args, nil)
	if err != nil {
		t.Fatalf("executeControllerCI: %v", err)
	}
	if result.Outcome != ciOutcomePassed || result.Transactions != 20 || statusCalls.Load() != 1 {
		t.Fatalf("result=%#v calls=%d", result, statusCalls.Load())
	}
}

func TestExecuteControllerCIFailsWhenRequiredCheckUpdateFails(t *testing.T) {
	oldPoll := ciPollInterval
	ciPollInterval = time.Millisecond
	t.Cleanup(func() { ciPollInterval = oldPoll })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/ci/runs":
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"schema_version":1,"test_id":"test-1","status":"QUEUED","check_url":"https://github.example/checks/1"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/ci/runs/test-1":
			_, _ = io.WriteString(w, `{"schema_version":1,"test_id":"test-1","status":"COMPLETE","outcome":"passed","sla_evaluated":true,"transactions":20,"check_url":"https://github.example/checks/1","error":"GitHub Check update failed: upstream unavailable"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	script := filepath.Join(t.TempDir(), "script.json")
	if err := os.WriteFile(script, []byte(`{"name":"ci","protocol":"http","actions":[{"name":"home","type":"request","method":"GET","target":"https://example.com"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	args := defaultCIArgs()
	args.controllerURL, args.projectID, args.scriptPath = server.URL, "project-1", script
	args.authMode, args.apiKey = "api-key", "key-1"
	result, err := executeControllerCI(context.Background(), args, nil)
	if err == nil || result.Outcome != ciOutcomeExecutionError || result.Status != "CHECK_FAILED" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestEnforceRequiredCICheckRejectsMissingURL(t *testing.T) {
	args := defaultCIArgs()
	args.publishCheck = true
	response := &CIRunResponse{Status: "COMPLETE", Outcome: ciOutcomePassed}
	result := ciResultFromResponse(response)
	if err := enforceRequiredCICheck(args, response, &result); err == nil {
		t.Fatal("missing required Check URL unexpectedly passed")
	}
	if result.Outcome != ciOutcomeExecutionError || result.Status != "CHECK_FAILED" || !strings.Contains(result.Message, "Check URL") {
		t.Fatalf("result = %#v", result)
	}

	args.publishCheck = false
	result = ciResultFromResponse(response)
	if err := enforceRequiredCICheck(args, response, &result); err != nil {
		t.Fatalf("optional Check URL was enforced: %v", err)
	}
}

func TestExecuteControllerCIAbortsOnCancellation(t *testing.T) {
	oldPoll := ciPollInterval
	ciPollInterval = time.Hour
	t.Cleanup(func() { ciPollInterval = oldPoll })
	aborted := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/ci/runs":
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"schema_version":1,"test_id":"test-1","status":"RUNNING"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/ci/runs/test-1/abort":
			aborted <- struct{}{}
			_, _ = io.WriteString(w, `{"test_id":"test-1","status":"ABORTED"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	script := filepath.Join(t.TempDir(), "script.json")
	_ = os.WriteFile(script, []byte(`{"name":"ci"}`), 0o600)
	args := defaultCIArgs()
	args.controllerURL, args.projectID, args.scriptPath = server.URL, "project-1", script
	args.authMode, args.apiKey = "api-key", "key-1"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := executeControllerCI(ctx, args, nil)
	if err == nil || result.TestID != "" {
		t.Fatalf("pre-cancelled result=%#v err=%v", result, err)
	}

	ctx, cancel = context.WithCancel(context.Background())
	go func() {
		time.Sleep(5 * time.Millisecond)
		cancel()
	}()
	result, err = executeControllerCI(ctx, args, nil)
	if err == nil || result.TestID != "test-1" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	select {
	case <-aborted:
	case <-time.After(time.Second):
		t.Fatal("controller abort endpoint was not called")
	}
}

func TestFinalizeCIResultWritesSafeOutputsAndEvidence(t *testing.T) {
	dir := t.TempDir()
	args := defaultCIArgs()
	args.jsonPath = filepath.Join(dir, "evidence", "result.json")
	args.junitPath = filepath.Join(dir, "evidence", "result.xml")
	args.githubOutput = filepath.Join(dir, "github-output")
	args.githubSummary = filepath.Join(dir, "summary.md")
	firstDelimiter := githubOutputDelimiter("report-url", "")
	reportURL := "https://example.com/report?ratio=100%\r\n" + firstDelimiter + "\nname<<EOF\r\ntrailing"
	result := CIResult{
		SchemaVersion: ciSchemaVersion, Outcome: ciOutcomeSLABreached, Status: "COMPLETE",
		SLAEvaluated: true, SLABreached: true, SLADetails: []string{"P95 exceeded\nnext-line"},
		Transactions: 10, Errors: 1, TPS: 5, ReportURL: reportURL,
	}
	if err := finalizeCIResult(args, result); err != nil {
		t.Fatalf("finalizeCIResult: %v", err)
	}
	for _, path := range []string{args.jsonPath, args.junitPath, args.githubOutput, args.githubSummary} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("missing %s: %v", path, err)
		}
	}
	output, _ := os.ReadFile(args.githubOutput)
	parsed := parseGitHubEnvironmentFile(t, output)
	if parsed["report-url"] != reportURL {
		t.Fatalf("report-url did not round-trip:\n got: %q\nwant: %q\nfile:\n%s", parsed["report-url"], reportURL, output)
	}
	if parsed["apexload-evidence-complete"] != "true" {
		t.Fatalf("missing evidence completion marker: %q", parsed["apexload-evidence-complete"])
	}
	if strings.Contains(string(output), "%25") || githubOutputDelimiter("report-url", reportURL) == firstDelimiter {
		t.Fatalf("output did not use raw collision-safe heredoc serialization:\n%s", output)
	}
}

func TestDeriveCIIdempotencyKeyChangesForRunAttempt(t *testing.T) {
	request := CIRunRequest{ScriptID: "script-1", Source: CISource{RepositoryID: "1", WorkflowRef: "owner/repo/.github/workflows/perf.yml@refs/heads/main", RunID: "100", RunAttempt: "1"}}
	first := deriveCIIdempotencyKey(request)
	request.Source.RunAttempt = "2"
	second := deriveCIIdempotencyKey(request)
	if first == second || !strings.HasPrefix(first, "apexload-ci-v1-") {
		t.Fatalf("keys first=%q second=%q", first, second)
	}
}

func parseGitHubEnvironmentFile(t *testing.T, contents []byte) map[string]string {
	t.Helper()
	values := make(map[string]string)
	for len(contents) > 0 {
		headerEnd := bytes.IndexByte(contents, '\n')
		if headerEnd < 0 {
			t.Fatalf("unterminated environment-file header: %q", contents)
		}
		header := strings.TrimSuffix(string(contents[:headerEnd]), "\r")
		name, delimiter, ok := strings.Cut(header, "<<")
		if !ok || name == "" || delimiter == "" {
			t.Fatalf("invalid environment-file header %q", header)
		}
		contents = contents[headerEnd+1:]
		marker := []byte("\n" + delimiter + "\n")
		valueEnd := bytes.Index(contents, marker)
		if valueEnd < 0 {
			t.Fatalf("missing delimiter %q in environment file", delimiter)
		}
		values[name] = string(contents[:valueEnd])
		contents = contents[valueEnd+len(marker):]
	}
	return values
}

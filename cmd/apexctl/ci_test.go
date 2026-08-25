package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestMain(m *testing.M) {
	// Tests must not inherit the outer GitHub Actions trust or checkout
	// provenance. Tests that exercise trusted GitHub inputs set their own
	// isolated action context, repository, and commit explicitly.
	for _, name := range []string{
		"GITHUB_ACTIONS",
		"GITHUB_EVENT_NAME",
		"GITHUB_EVENT_PATH",
		"GITHUB_REPOSITORY",
		"GITHUB_REPOSITORY_ID",
		"GITHUB_SHA",
		"GITHUB_WORKSPACE",
		"APEXLOAD_GITHUB_HEAD_REPOSITORY",
		"APEXLOAD_GITHUB_HEAD_REPOSITORY_ID",
		"APEXLOAD_GITHUB_HEAD_SHA",
	} {
		_ = os.Unsetenv(name)
	}
	os.Exit(m.Run())
}

func TestInspectCILocalActionsTraversesSwitchBranches(t *testing.T) {
	script := &dsl.Script{Actions: []dsl.Action{{
		Type: "control_switch",
		Control: &dsl.ControlFlow{
			Cases:   []dsl.SwitchCase{{Steps: []dsl.Action{{Type: "POST", BodyFile: "payload.json"}}}},
			Default: []dsl.Action{{Type: "browser_set_files"}},
		},
	}}}
	bodyFile, browserInput := inspectCILocalActions(script)
	if !bodyFile || !browserInput {
		t.Fatalf("switch local-file detection = body:%t browser:%t", bodyFile, browserInput)
	}
}

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
	for _, args := range [][]string{
		{"--controller-url", "https://controller.example", "--project-id", "project-1", "--script-id", "script-1", "--p95-latency-ms", "0"},
		{"--controller-url", "https://controller.example", "--project-id", "project-1", "--script-id", "script-1", "--p95-latency-ms", "NaN"},
		{"--controller-url", "https://controller.example", "--project-id", "project-1", "--script-id", "script-1", "--max-avg-server-processing-ms", "+Inf"},
		{"--controller-url", "https://controller.example", "--project-id", "project-1", "--script-id", "script-1", "--max-error-pct", "101"},
		{"--controller-url", "https://controller.example", "--project-id", "project-1", "--script-id", "script-1", "--max-avg-transfer-ms", "-1"},
		{"--controller-url", "https://controller.example", "--project-id", "project-1", "--script-id", "script-1", "--max-error-count", "-1"},
	} {
		if _, err := parseCIArgs(args); err == nil {
			t.Fatalf("invalid SLA args unexpectedly passed: %v", args)
		}
	}
}

func TestParseCIArgsRejectsCollidingEvidenceAndGitHubFiles(t *testing.T) {
	base := []string{
		"--execution-mode", "local",
		"--publish-check=false",
		"--script-path", "script.json",
		"--require-sla=false",
	}
	for _, test := range []struct {
		name string
		args []string
	}{
		{name: "JSON and JUnit", args: []string{"--json", "same.out", "--junit", "same.out"}},
		{name: "JSON and GitHub output", args: []string{"--json", "same.out", "--junit", "junit.xml", "--github-output", "same.out"}},
		{name: "JUnit and summary", args: []string{"--json", "result.json", "--junit", "same.out", "--github-summary", "same.out"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parseCIArgs(append(append([]string(nil), base...), test.args...)); err == nil || !strings.Contains(err.Error(), "distinct files") {
				t.Fatalf("colliding output error = %v", err)
			}
		})
	}
}

func TestValidateDistinctCIOutputPathsRejectsSymlinkAliases(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "alias")
	if err := os.Symlink(target, alias); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	args := defaultCIArgs()
	args.jsonPath = target
	args.junitPath = alias
	if err := validateDistinctCIOutputPaths(args); err == nil || !strings.Contains(err.Error(), "distinct files") {
		t.Fatalf("symlink alias error = %v", err)
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

func TestDecodeBoundedSingleJSONRejectsOversizedAndTrailingResponses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		body    string
		max     int64
		want    string
		wantErr string
	}{
		{name: "valid", body: `{"value":"token"}`, max: 64, want: "token"},
		{name: "trailing object", body: `{"value":"token"}{"value":"forged"}`, max: 64, wantErr: "one JSON object"},
		{name: "trailing garbage", body: `{"value":"token"}garbage`, max: 64, wantErr: "trailing data"},
		{name: "oversized after valid prefix", body: `{"value":"token"}` + strings.Repeat(" ", 64), max: 32, wantErr: "exceeds 32 bytes"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var decoded struct {
				Value string `json:"value"`
			}
			err := decodeBoundedSingleJSON(strings.NewReader(test.body), test.max, &decoded)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("decode error = %v, want substring %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if decoded.Value != test.want {
				t.Fatalf("decoded value = %q, want %q", decoded.Value, test.want)
			}
		})
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

func TestLoadCIVariablesEnforcesControllerCompatibleBounds(t *testing.T) {
	tests := []struct {
		name    string
		raw     []byte
		wantErr string
	}{
		{name: "null", raw: []byte(`null`), wantErr: "JSON object"},
		{name: "invalid key", raw: []byte(`{"bad\nkey":"value"}`), wantErr: "invalid key"},
		{name: "oversized value", raw: []byte(`{"region":"` + strings.Repeat("x", maxCIVariableValueBytes+1) + `"}`), wantErr: "exceeds"},
		{name: "NUL value", raw: []byte("{\"region\":\"bad\\u0000value\"}"), wantErr: "invalid"},
	}
	tooMany := make(map[string]string, maxCIVariableEntries+1)
	for i := 0; i <= maxCIVariableEntries; i++ {
		tooMany[fmt.Sprintf("key_%d", i)] = "value"
	}
	tooManyRaw, err := json.Marshal(tooMany)
	if err != nil {
		t.Fatal(err)
	}
	tests = append(tests, struct {
		name    string
		raw     []byte
		wantErr string
	}{name: "too many entries", raw: tooManyRaw, wantErr: "exceeds"})

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "variables.json")
			if err := os.WriteFile(path, test.raw, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadCIVariables(path); err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("load variables error = %v, want substring %q", err, test.wantErr)
			}
		})
	}
}

func TestLoadTrustedCIVariablesUsesCommittedSnapshot(t *testing.T) {
	trustedVariables := `{"region":"eastus","scenario":"trusted"}`
	repository, _, commit := newCIScriptRepository(t, `{"name":"script"}`, map[string]string{
		"variables.json": trustedVariables,
	})
	variablesPath := filepath.Join(repository, "variables.json")
	if err := os.WriteFile(variablesPath, []byte(`{"scenario":"workspace-modified"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_WORKSPACE", repository)
	t.Setenv("GITHUB_SHA", commit)
	variables, err := loadTrustedCIVariables(t.Context(), variablesPath)
	if err != nil {
		t.Fatalf("load trusted variables: %v", err)
	}
	if variables["scenario"] != "trusted" || variables["region"] != "eastus" {
		t.Fatalf("variables = %#v, want committed snapshot", variables)
	}
}

func TestSourceFromEnvironmentUsesTrustedActionContextInsteadOfMutableEventFile(t *testing.T) {
	eventPath := filepath.Join(t.TempDir(), "event.json")
	event := `{"pull_request":{"head":{"sha":"forged-sha","repo":{"id":111,"full_name":"base/repo"}}}}`
	if err := os.WriteFile(eventPath, []byte(event), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_REPOSITORY", "base/repo")
	t.Setenv("GITHUB_REPOSITORY_ID", "111")
	t.Setenv("GITHUB_EVENT_PATH", eventPath)
	t.Setenv("APEXLOAD_GITHUB_HEAD_REPOSITORY", "fork/repo")
	t.Setenv("APEXLOAD_GITHUB_HEAD_REPOSITORY_ID", "222")
	t.Setenv("APEXLOAD_GITHUB_HEAD_SHA", "trusted-head-sha")
	source := sourceFromEnvironment()
	if source.Provider != "github" || source.HeadRepository != "fork/repo" || source.HeadRepositoryID != "222" || source.HeadSHA != "trusted-head-sha" {
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

func TestValidateCIRunAPIResponseFailsClosed(t *testing.T) {
	validRunning := &CIRunResponse{SchemaVersion: ciSchemaVersion, TestID: "test-1", Status: "RUNNING", Outcome: "running", Transactions: 5, TPS: 1.5}
	validComplete := &CIRunResponse{SchemaVersion: ciSchemaVersion, TestID: "test-1", Status: "COMPLETE", Outcome: ciOutcomePassed, Transactions: 5, TPS: 1.5}
	for _, test := range []struct {
		name     string
		response *CIRunResponse
		expected string
		wantOK   bool
	}{
		{name: "running", response: validRunning, expected: "test-1", wantOK: true},
		{name: "complete", response: validComplete, expected: "test-1", wantOK: true},
		{name: "wrong schema", response: &CIRunResponse{SchemaVersion: 2, TestID: "test-1", Status: "RUNNING", Outcome: "running"}},
		{name: "wrong test", response: validRunning, expected: "test-2"},
		{name: "early pass", response: &CIRunResponse{SchemaVersion: 1, TestID: "test-1", Status: "RUNNING", Outcome: ciOutcomePassed, Transactions: 5}},
		{name: "terminal queued", response: &CIRunResponse{SchemaVersion: 1, TestID: "test-1", Status: "COMPLETE", Outcome: "queued", Transactions: 5}},
		{name: "negative metrics", response: &CIRunResponse{SchemaVersion: 1, TestID: "test-1", Status: "RUNNING", Outcome: "running", Transactions: -1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateCIRunAPIResponse(test.response, test.expected)
			if test.wantOK && err != nil {
				t.Fatalf("valid response rejected: %v", err)
			}
			if !test.wantOK && err == nil {
				t.Fatal("invalid response was accepted")
			}
		})
	}
}

func TestCIAPIErrorIsTypedAndDoesNotLeakResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "17")
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
	if apiErr.StatusCode != http.StatusForbidden || apiErr.Method != http.MethodPost || apiErr.Path != "/api/v1/ci/runs" || apiErr.RetryAfter != 17*time.Second {
		t.Fatalf("CIAPIError = %#v", apiErr)
	}
	if strings.Contains(err.Error(), "super-secret-token") {
		t.Fatalf("safe error leaked response body: %v", err)
	}
}

func TestParseCIRetryAfterIsBoundedAndSupportsHTTPDates(t *testing.T) {
	now := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		value string
		want  time.Duration
	}{
		{name: "empty", value: "", want: 0},
		{name: "seconds", value: "30", want: 30 * time.Second},
		{name: "negative", value: "-1", want: 0},
		{name: "invalid", value: "soon", want: 0},
		{name: "HTTP date", value: now.Add(45 * time.Second).Format(http.TimeFormat), want: 45 * time.Second},
		{name: "past HTTP date", value: now.Add(-time.Second).Format(http.TimeFormat), want: 0},
		{name: "bounded seconds", value: "999999999999", want: 24 * time.Hour},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := parseCIRetryAfter(test.value, now); got != test.want {
				t.Fatalf("parseCIRetryAfter(%q) = %s, want %s", test.value, got, test.want)
			}
		})
	}
}

func TestDefaultControllerRetryPolicyCoversLeadershipFailover(t *testing.T) {
	const leadershipLease = 30 * time.Second
	totalDelay := time.Duration(0)
	for attempt := 0; attempt < ciControllerRequestAttempts-1 && totalDelay < ciControllerRetryWindow; attempt++ {
		totalDelay += ciControllerRetryDelay(attempt, nil)
	}
	if totalDelay < leadershipLease {
		t.Fatalf("controller retry delays cover %s, want at least %s", totalDelay, leadershipLease)
	}
	if ciControllerRetryWindow < leadershipLease || ciControllerRetryWindow > time.Minute {
		t.Fatalf("controller retry window = %s, want bounded coverage of %s", ciControllerRetryWindow, leadershipLease)
	}
	if ciAbortConfirmationTimeout < leadershipLease || ciAbortConfirmationTimeout > time.Minute {
		t.Fatalf("abort confirmation timeout = %s, want bounded coverage of %s", ciAbortConfirmationTimeout, leadershipLease)
	}
}

func TestControllerRetryDelayHonorsRetryAfterAndBackoffCap(t *testing.T) {
	withCIRetryPolicy(t, 4, 10*time.Millisecond, 40*time.Millisecond, time.Second, time.Second)
	if got := ciControllerRetryDelay(2, nil); got != 40*time.Millisecond {
		t.Fatalf("exponential delay = %s, want 40ms", got)
	}
	if got := ciControllerRetryDelay(8, nil); got != 40*time.Millisecond {
		t.Fatalf("capped delay = %s, want 40ms", got)
	}
	retryAfter := &CIAPIError{StatusCode: http.StatusTooManyRequests, RetryAfter: 75 * time.Millisecond}
	if got := ciControllerRetryDelay(0, retryAfter); got != 75*time.Millisecond {
		t.Fatalf("Retry-After delay = %s, want 75ms", got)
	}
}

func TestRetryControllerCIRequestDoesNotRetryPermanentClientErrors(t *testing.T) {
	withCIRetryPolicy(t, 8, time.Millisecond, 2*time.Millisecond, 50*time.Millisecond, 50*time.Millisecond)
	client := NewApiClientWithAuth("https://controller.example", "api-key", "")
	auth := &ciAuthSession{mode: "api-key", projectID: "project-1"}
	var calls atomic.Int32
	_, err := retryControllerCIRequest(t.Context(), auth, client, func() (*CIRunResponse, error) {
		calls.Add(1)
		return nil, &CIAPIError{Method: http.MethodPost, Path: "/api/v1/ci/runs", StatusCode: http.StatusForbidden}
	})
	var apiErr *CIAPIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusForbidden {
		t.Fatalf("retry error = %T %v", err, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("permanent client error was attempted %d times, want 1", calls.Load())
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

func TestValidateLocalCIGitHubTrustRejectsUntrustedPullRequests(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_REPOSITORY", "octo/apex")
	t.Setenv("GITHUB_REPOSITORY_ID", "123")
	t.Run("pull request target", func(t *testing.T) {
		t.Setenv("GITHUB_EVENT_NAME", "pull_request_target")
		if err := validateLocalCIGitHubTrust(); err == nil {
			t.Fatal("pull_request_target was allowed to execute local load")
		}
	})
	t.Run("fork pull request", func(t *testing.T) {
		t.Setenv("GITHUB_EVENT_NAME", "pull_request")
		t.Setenv("APEXLOAD_GITHUB_HEAD_REPOSITORY", "mallory/apex")
		t.Setenv("APEXLOAD_GITHUB_HEAD_REPOSITORY_ID", "999")
		if err := validateLocalCIGitHubTrust(); err == nil {
			t.Fatal("fork pull request was allowed to execute local load")
		}
	})
	t.Run("missing trusted action context", func(t *testing.T) {
		t.Setenv("GITHUB_EVENT_NAME", "pull_request")
		t.Setenv("APEXLOAD_GITHUB_HEAD_REPOSITORY", "")
		t.Setenv("APEXLOAD_GITHUB_HEAD_REPOSITORY_ID", "")
		if err := validateLocalCIGitHubTrust(); err == nil || !strings.Contains(err.Error(), "trusted action context") {
			t.Fatalf("missing trusted identity error = %v", err)
		}
	})
	t.Run("same repository pull request", func(t *testing.T) {
		t.Setenv("GITHUB_EVENT_NAME", "pull_request")
		t.Setenv("APEXLOAD_GITHUB_HEAD_REPOSITORY", "Octo/Apex")
		t.Setenv("APEXLOAD_GITHUB_HEAD_REPOSITORY_ID", "123")
		if err := validateLocalCIGitHubTrust(); err != nil {
			t.Fatalf("same-repository pull request rejected: %v", err)
		}
	})
}

func TestExecuteControllerCIRejectsForkBeforeAuthenticationOrAPIRequests(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "must not be called", http.StatusInternalServerError)
	}))
	defer server.Close()

	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_EVENT_NAME", "pull_request")
	t.Setenv("GITHUB_REPOSITORY", "octo/apex")
	t.Setenv("GITHUB_REPOSITORY_ID", "123")
	t.Setenv("APEXLOAD_GITHUB_HEAD_REPOSITORY", "mallory/apex")
	t.Setenv("APEXLOAD_GITHUB_HEAD_REPOSITORY_ID", "999")
	args := defaultCIArgs()
	args.controllerURL = server.URL
	args.projectID = "project-1"
	args.scriptID = "script-1"
	args.authMode = "api-key"
	args.apiKey = "key-1"
	args.publishCheck = false

	result, err := executeControllerCI(t.Context(), args, nil)
	if err == nil || result.Status != "UNTRUSTED_SOURCE" || !strings.Contains(err.Error(), "fork pull requests") {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if calls.Load() != 0 {
		t.Fatalf("controller received %d requests for a fork pull request", calls.Load())
	}
}

func TestExecuteControllerCIRejectsRequestLargerThanControllerLimit(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "must not be called", http.StatusInternalServerError)
	}))
	defer server.Close()

	padding := strings.Repeat("x", maxCIScriptBytes-128)
	scriptPath := filepath.Join(t.TempDir(), "large-script.json")
	if err := os.WriteFile(scriptPath, []byte(`{"name":"large","padding":"`+padding+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	args := defaultCIArgs()
	args.controllerURL = server.URL
	args.projectID = "project-1"
	args.scriptPath = scriptPath
	args.authMode = "api-key"
	args.apiKey = "key-1"
	args.publishCheck = false
	args.requireSLA = false
	args.requireResults = false

	result, err := executeControllerCI(t.Context(), args, nil)
	if err == nil || result.Status != "INVALID" || !strings.Contains(err.Error(), "API limit") {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if calls.Load() != 0 {
		t.Fatalf("controller received %d requests for an oversized payload", calls.Load())
	}
}

func TestLoadTrustedCIScriptSnapshotAcceptsCommittedGitHubScript(t *testing.T) {
	scriptContents := `{"name":"trusted","protocol":"http","config":{"base_url":"https://example.test"},"actions":[{"name":"home","type":"request","method":"GET","target":"/"}]}`
	repository, scriptPath, commit := newCIScriptRepository(t, scriptContents, nil)

	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_WORKSPACE", repository)
	t.Setenv("GITHUB_SHA", commit)
	for _, event := range []string{"push", "pull_request", "workflow_dispatch"} {
		t.Run(event, func(t *testing.T) {
			t.Setenv("GITHUB_EVENT_NAME", event)
			raw, resolved, err := loadTrustedCIScriptSnapshot(t.Context(), scriptPath)
			if err != nil {
				t.Fatalf("load trusted script: %v", err)
			}
			if string(raw) != scriptContents {
				t.Fatalf("script bytes = %q, want committed bytes", raw)
			}
			if same, err := sameCIFile(resolved, scriptPath); err != nil || !same {
				t.Fatalf("resolved script = %q, want %q (same=%t err=%v)", resolved, scriptPath, same, err)
			}
		})
	}
}

func TestLoadTrustedCIScriptSnapshotUsesCommittedBytesDespiteWorkspaceFiltersAndModifications(t *testing.T) {
	trusted := `{"name":"trusted","protocol":"http","config":{"base_url":"https://example.test"},"actions":[{"name":"home","type":"request","method":"GET","target":"/"}]}`
	repository, scriptPath, commit := newCIScriptRepository(t, trusted, map[string]string{
		".gitattributes": "script.json filter=trusted-script\n",
	})
	runCITestGit(t, repository, "config", "filter.trusted-script.clean", "git show HEAD:script.json")
	modified := `{"name":"attacker-controlled","protocol":"http","config":{"base_url":"https://attacker.example"},"actions":[{"name":"exfiltrate","type":"request","method":"POST","target":"/"}]}`
	if err := os.WriteFile(scriptPath, []byte(modified), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_WORKSPACE", repository)
	t.Setenv("GITHUB_SHA", commit)
	raw, _, err := loadTrustedCIScriptSnapshot(t.Context(), scriptPath)
	if err != nil {
		t.Fatalf("load committed script: %v", err)
	}
	if string(raw) != trusted {
		t.Fatalf("script bytes = %q, want committed bytes %q", raw, trusted)
	}
}

func TestLoadTrustedCIScriptSnapshotSupportsCheckoutBelowGitHubWorkspace(t *testing.T) {
	workspace := t.TempDir()
	repository := filepath.Join(workspace, "source", "apexload")
	trusted := `{"name":"nested-checkout","protocol":"http","config":{"base_url":"https://example.test"},"actions":[{"name":"home","type":"request","method":"GET","target":"/"}]}`
	repository, scriptPath, commit := newCIScriptRepositoryAt(t, repository, trusted, nil)

	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_WORKSPACE", workspace)
	t.Setenv("GITHUB_SHA", commit)
	raw, resolved, err := loadTrustedCIScriptSnapshot(t.Context(), scriptPath)
	if err != nil {
		t.Fatalf("load nested checkout script: %v", err)
	}
	if string(raw) != trusted {
		t.Fatalf("script bytes = %q, want %q", raw, trusted)
	}
	if same, err := sameCIFile(resolved, scriptPath); err != nil || !same {
		t.Fatalf("resolved script = %q, want nested checkout path %q (same=%t err=%v)", resolved, scriptPath, same, err)
	}
}

func TestLoadTrustedCIScriptSnapshotRejectsAlternateCheckoutForIssueComment(t *testing.T) {
	trusted := `{"name":"trusted","protocol":"http","config":{"base_url":"https://example.test"},"actions":[{"name":"home","type":"request","method":"GET","target":"/"}]}`
	repository, scriptPath, trustedCommit := newCIScriptRepository(t, trusted, nil)
	if err := os.WriteFile(scriptPath, []byte(`{"name":"alternate","protocol":"http","config":{"base_url":"https://attacker.example"},"actions":[{"name":"home","type":"request","method":"GET","target":"/"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	runCITestGit(t, repository, "add", "--", "script.json")
	runCITestGit(t, repository, "commit", "--quiet", "-m", "alternate checkout")

	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_EVENT_NAME", "issue_comment")
	t.Setenv("GITHUB_WORKSPACE", repository)
	t.Setenv("GITHUB_SHA", trustedCommit)
	if _, _, err := loadTrustedCIScriptSnapshot(t.Context(), scriptPath); err == nil || !strings.Contains(err.Error(), "HEAD does not match GITHUB_SHA") {
		t.Fatalf("alternate checkout error = %v, want HEAD mismatch", err)
	}
}

func TestLoadTrustedCIScriptSnapshotRejectsPathsOutsideWorkspaceAndSymlinks(t *testing.T) {
	trusted := `{"name":"trusted","protocol":"http","config":{"base_url":"https://example.test"},"actions":[{"name":"home","type":"request","method":"GET","target":"/"}]}`
	repository, scriptPath, commit := newCIScriptRepository(t, trusted, nil)
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_WORKSPACE", repository)
	t.Setenv("GITHUB_SHA", commit)

	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte(trusted), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadTrustedCIScriptSnapshot(t.Context(), outside); err == nil || !strings.Contains(err.Error(), "inside GITHUB_WORKSPACE") {
		t.Fatalf("outside path error = %v", err)
	}

	link := filepath.Join(repository, "linked-script.json")
	if err := os.Symlink(scriptPath, link); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	if _, _, err := loadTrustedCIScriptSnapshot(t.Context(), link); err == nil || !strings.Contains(err.Error(), "symbolic links") {
		t.Fatalf("symlink path error = %v", err)
	}
}

func TestValidateGitHubLocalCIAssetIsolationFailsClosedForWorkspaceBackedInputs(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "true")
	tests := []struct {
		name    string
		script  string
		wantErr string
	}{
		{
			name:    "body file",
			script:  `{"name":"body","protocol":"http","config":{"base_url":"https://example.test"},"actions":[{"name":"post","type":"request","method":"POST","target":"/","body_file":"payload.json"}]}`,
			wantErr: "body_file",
		},
		{
			name:    "nested sequence body file",
			script:  `{"name":"sequence","protocol":"http","config":{"base_url":"https://example.test"},"actions":[{"name":"seq","type":"http_sequence","params":{"requests":[{"name":"post","type":"request","method":"POST","target":"/","body_file":"payload.json"}]}}]}`,
			wantErr: "body_file",
		},
		{
			name:    "data source path",
			script:  `{"name":"data","protocol":"http","config":{"base_url":"https://example.test"},"data_sources":[{"name":"users","type":"csv","path":"users.csv"}],"actions":[{"name":"get","type":"request","method":"GET","target":"/"}]}`,
			wantErr: "inline_data",
		},
		{
			name:    "grpc descriptor",
			script:  `{"name":"grpc","protocol":"grpc","config":{"grpc":{"proto_files":["service.pb"]}},"actions":[{"name":"call","type":"grpc_unary","target":"svc.Method","body":"{}"}]}`,
			wantErr: "descriptor",
		},
		{
			name:    "HTTP TLS file",
			script:  `{"name":"tls","protocol":"http","config":{"base_url":"https://example.test","http":{"tls":{"ca_cert_file":"ca.pem"}}},"actions":[{"name":"get","type":"request","method":"GET","target":"/"}]}`,
			wantErr: "HTTP TLS",
		},
		{
			name:    "JMS TLS file",
			script:  `{"name":"jms","protocol":"jms-solace","config":{"jms":{"mock":true,"destination":"orders","tls":{"ca_cert_secret":"ca.pem"}}},"actions":[{"name":"send","type":"jms_send","body":"{}"}]}`,
			wantErr: "JMS TLS",
		},
		{
			name:    "browser upload",
			script:  `{"name":"browser","protocol":"browser","config":{"base_url":"https://example.test"},"actions":[{"name":"upload","type":"browser_set_files","selector":"#file","value":"payload.bin"}]}`,
			wantErr: "browser_set_files",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateGitHubLocalCIAssetIsolation([]byte(test.script))
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("asset isolation error = %v, want substring %q", err, test.wantErr)
			}
		})
	}

	inline := []byte(`{"name":"inline","protocol":"http","config":{"base_url":"https://example.test"},"data_sources":[{"name":"users","type":"csv","inline_data":"name\nalice\n"}],"actions":[{"name":"get","type":"request","method":"GET","target":"/","body":"{}"}]}`)
	if err := validateGitHubLocalCIAssetIsolation(inline); err != nil {
		t.Fatalf("inline-only local CI script rejected: %v", err)
	}
}

func TestExecuteLocalCIFailsBeforeReadingWorkspaceBackedBodyFile(t *testing.T) {
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetCalls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	script := `{"name":"body","protocol":"http","config":{"base_url":"` + target.URL + `"},"actions":[{"name":"post","type":"request","method":"POST","target":"/","body_file":"payload.json"}]}`
	repository, scriptPath, commit := newCIScriptRepository(t, script, map[string]string{
		"payload.json": `{"trusted":true}`,
	})
	if err := os.WriteFile(filepath.Join(repository, "payload.json"), []byte(`{"attacker":"workspace"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_EVENT_NAME", "push")
	t.Setenv("GITHUB_WORKSPACE", repository)
	t.Setenv("GITHUB_SHA", commit)
	args := defaultCIArgs()
	args.executionMode = "local"
	args.scriptPath = scriptPath
	args.publishCheck = false
	args.requireSLA = false
	args.requireResults = false

	result, err := executeLocalCI(t.Context(), args, nil)
	if err == nil || result.Status != "UNTRUSTED_SOURCE" || !strings.Contains(err.Error(), "body_file") {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if targetCalls.Load() != 0 {
		t.Fatalf("local target received %d requests before asset isolation failure", targetCalls.Load())
	}
}

func TestExecuteLocalCIFailsBeforeStartingLoadWhenRequiredSLAIsMissing(t *testing.T) {
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetCalls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	scriptPath := filepath.Join(t.TempDir(), "script.json")
	script := `{"name":"missing-sla","protocol":"http","config":{"base_url":"` + target.URL + `"},"actions":[{"name":"get","type":"request","method":"GET","target":"/"}]}`
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}

	args := defaultCIArgs()
	args.executionMode = "local"
	args.scriptPath = scriptPath
	args.publishCheck = false
	args.requireSLA = true
	args.requireResults = false
	result, err := executeLocalCI(t.Context(), args, nil)
	if err == nil || result.Status != "MISSING_SLA" || result.Outcome != ciOutcomeConfigurationError {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if targetCalls.Load() != 0 {
		t.Fatalf("target received %d requests before missing-SLA preflight", targetCalls.Load())
	}
}

func TestExecuteControllerCIScriptIDDoesNotRequireWorkspaceProvenance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/ci/runs" {
			http.NotFound(w, r)
			return
		}
		var request CIRunRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.ScriptID != "controller-script-1" || request.ScriptPath != "" || len(request.Script) != 0 {
			t.Fatalf("controller request = %#v", request)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"schema_version":1,"test_id":"test-1","status":"COMPLETE","outcome":"passed"}`)
	}))
	defer server.Close()

	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_WORKSPACE", filepath.Join(t.TempDir(), "missing-workspace"))
	t.Setenv("GITHUB_SHA", "not-a-git-object")
	args := defaultCIArgs()
	args.controllerURL = server.URL
	args.projectID = "project-1"
	args.scriptID = "controller-script-1"
	args.authMode = "api-key"
	args.apiKey = "key-1"
	args.publishCheck = false
	args.requireSLA = false
	args.requireResults = false

	result, err := executeControllerCI(t.Context(), args, nil)
	if err != nil {
		t.Fatalf("execute controller script ID: %v", err)
	}
	if result.Outcome != ciOutcomePassed || result.TestID != "test-1" {
		t.Fatalf("result = %#v", result)
	}
}

func TestLoadCIVariablesRejectsSensitiveKeysAndValues(t *testing.T) {
	for _, variables := range []map[string]string{
		{"authorization": "opaque"},
		{"AWS_ACCESS_KEY_ID": "AKIAIOSFODNN7EXAMPLE"},
		{"header": "Bearer short"},
		{"endpoint": "https://user:password@example.test/path"},
		{"endpoint": "https://example.test/path?sig=short"},
	} {
		raw, err := json.Marshal(variables)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "variables.json")
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadCIVariables(path); err == nil {
			t.Fatalf("sensitive variables were accepted: %#v", variables)
		}
	}
}

func TestExecuteControllerCIMapsCreateHTTPFailures(t *testing.T) {
	withCIRetryPolicy(t, 2, time.Millisecond, time.Millisecond, 5*time.Millisecond, 5*time.Millisecond)
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
			evidenceDir := t.TempDir()
			args.jsonPath = filepath.Join(evidenceDir, "result.json")
			args.junitPath = filepath.Join(evidenceDir, "results.xml")
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

func TestExecuteControllerCIRetriesInitialOIDCDiscoveryThroughFailover(t *testing.T) {
	withCIRetryPolicy(t, 5, time.Millisecond, 2*time.Millisecond, 50*time.Millisecond, 50*time.Millisecond)
	oidcServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("audience"); got != "apexload://controller-1" {
			t.Fatalf("OIDC audience = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer request-token" {
			t.Fatalf("OIDC authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"value":"github-id-token"}`)
	}))
	defer oidcServer.Close()
	originalOIDCClient := newGitHubOIDCHTTPClient
	newGitHubOIDCHTTPClient = func() *http.Client { return oidcServer.Client() }
	t.Cleanup(func() { newGitHubOIDCHTTPClient = originalOIDCClient })
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", oidcServer.URL+"/token")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "request-token")
	t.Setenv("GITHUB_SERVER_URL", oidcServer.URL)

	var discoveryCalls atomic.Int32
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/.well-known/apexload-ci":
			if discoveryCalls.Add(1) == 1 {
				http.Error(w, "active endpoint is changing leaders", http.StatusServiceUnavailable)
				return
			}
			_, _ = io.WriteString(w, `{"schema_version":1,"controller_id":"controller-1","oidc_audience":"apexload://controller-1","ci_api_version":"v1","auth_modes":["oidc"]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/auth/ci/github/exchange":
			_, _ = io.WriteString(w, `{"access_token":"ci-access-token","token_type":"Bearer","expires_in":600}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/ci/runs":
			if got := r.Header.Get("Authorization"); got != "Bearer ci-access-token" {
				t.Fatalf("controller authorization = %q", got)
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"schema_version":1,"test_id":"test-1","status":"COMPLETE","outcome":"passed"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer controller.Close()

	script := filepath.Join(t.TempDir(), "script.json")
	if err := os.WriteFile(script, []byte(`{"name":"ci","protocol":"http","actions":[{"name":"home","type":"request","method":"GET","target":"https://example.com"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	args := defaultCIArgs()
	args.controllerURL, args.projectID, args.scriptPath = controller.URL, "project-1", script
	args.authMode = "oidc"
	args.publishCheck = false
	args.requireSLA = false
	args.requireResults = false

	result, err := executeControllerCI(t.Context(), args, nil)
	if err != nil {
		t.Fatalf("execute controller CI: %v", err)
	}
	if result.Outcome != ciOutcomePassed || discoveryCalls.Load() != 2 {
		t.Fatalf("result=%#v discovery calls=%d", result, discoveryCalls.Load())
	}
}

func TestExecuteControllerCIPermanentDiscoveryFailureIsNotRetried(t *testing.T) {
	withCIRetryPolicy(t, 5, time.Millisecond, 2*time.Millisecond, 50*time.Millisecond, 50*time.Millisecond)
	var discoveryCalls atomic.Int32
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/.well-known/apexload-ci" {
			discoveryCalls.Add(1)
			http.NotFound(w, r)
			return
		}
		http.NotFound(w, r)
	}))
	defer controller.Close()
	script := filepath.Join(t.TempDir(), "script.json")
	if err := os.WriteFile(script, []byte(`{"name":"ci"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	args := defaultCIArgs()
	args.controllerURL, args.projectID, args.scriptPath = controller.URL, "project-1", script
	args.authMode = "oidc"
	args.publishCheck = false
	args.requireSLA = false
	args.requireResults = false

	result, err := executeControllerCI(t.Context(), args, nil)
	if err == nil || result.Status != "AUTH_FAILED" || discoveryCalls.Load() != 1 {
		t.Fatalf("result=%#v err=%v discovery calls=%d", result, err, discoveryCalls.Load())
	}
}

func TestExecuteControllerCIRetryableCreateFailureDoesNotAbortAcceptedRun(t *testing.T) {
	oldAttempts := ciControllerRequestAttempts
	ciControllerRequestAttempts = 1
	t.Cleanup(func() { ciControllerRequestAttempts = oldAttempts })
	var abortCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/ci/runs":
			http.Error(w, "GitHub Check response was lost", http.StatusBadGateway)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/abort"):
			abortCalls.Add(1)
			_, _ = io.WriteString(w, `{"test_id":"unexpected","status":"ABORTED"}`)
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
	args.abortOnCancel = true

	result, err := executeControllerCI(context.Background(), args, nil)
	if err == nil || result.Status != "CREATE_FAILED" || result.Outcome != ciOutcomeExecutionError {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if got := abortCalls.Load(); got != 0 {
		t.Fatalf("retryable create failure attempted %d destructive aborts", got)
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
	withCIRetryPolicy(t, 4, time.Millisecond, 2*time.Millisecond, 20*time.Millisecond, 20*time.Millisecond)
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
			_, _ = io.WriteString(w, `{"schema_version":1,"test_id":"test-1","status":"ABORTED"}`)
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
	args.jsonPath = filepath.Join(filepath.Dir(script), "cancel-result.json")
	args.junitPath = filepath.Join(filepath.Dir(script), "cancel-result.xml")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := executeControllerCI(ctx, args, nil)
	if err == nil || result.TestID == "" || result.Outcome != ciOutcomeCancelled || result.Status != "CANCELLED" {
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

func TestAbortCIRunRejectsMismatchedControllerIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/ci/runs/test-1/abort" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"schema_version":1,"test_id":"different-run","status":"ABORTED"}`)
	}))
	defer server.Close()
	client := NewApiClientWithAuth(server.URL, "api-key", "")
	if _, err := client.AbortCIRunWithReason(context.Background(), "test-1", "cancelled"); err == nil || !strings.Contains(err.Error(), "test_id") {
		t.Fatalf("mismatched abort response error = %v", err)
	}
}

func TestAbortControllerCIRunRetriesAcrossLeaderFailoverAndConfirms(t *testing.T) {
	withCIRetryPolicy(t, 5, time.Millisecond, 2*time.Millisecond, 50*time.Millisecond, 100*time.Millisecond)
	var abortCalls atomic.Int32
	var statusCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/ci/runs/test-1/abort":
			if abortCalls.Add(1) <= 3 {
				http.Error(w, "leader unavailable", http.StatusServiceUnavailable)
				return
			}
			_, _ = io.WriteString(w, `{"schema_version":1,"test_id":"test-1","status":"STOPPING"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/ci/runs/test-1":
			if statusCalls.Add(1) == 1 {
				http.Error(w, "new leader warming up", http.StatusServiceUnavailable)
				return
			}
			_, _ = io.WriteString(w, `{"schema_version":1,"test_id":"test-1","status":"ABORTED","outcome":"cancelled"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := NewApiClientWithAuth(server.URL, "api-key", "")
	auth := &ciAuthSession{mode: "api-key", projectID: "project-1"}
	if err := abortControllerCIRun(t.Context(), auth, client, "test-1", "cancelled"); err != nil {
		t.Fatalf("abort controller CI run: %v", err)
	}
	if abortCalls.Load() < 5 || statusCalls.Load() != 2 {
		t.Fatalf("abort calls=%d status calls=%d", abortCalls.Load(), statusCalls.Load())
	}
}

func TestExecuteControllerCIStatusFailureDoesNotAbortRemoteRun(t *testing.T) {
	oldPoll := ciPollInterval
	oldAttempts := ciControllerRequestAttempts
	ciPollInterval = time.Millisecond
	ciControllerRequestAttempts = 1
	t.Cleanup(func() {
		ciPollInterval = oldPoll
		ciControllerRequestAttempts = oldAttempts
	})

	for _, test := range []struct {
		name       string
		abort      bool
		wantAborts int32
	}{
		{name: "disabled", abort: false, wantAborts: 0},
		{name: "enabled for cancellation only", abort: true, wantAborts: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			var abortCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/api/v1/ci/runs":
					w.WriteHeader(http.StatusAccepted)
					_, _ = io.WriteString(w, `{"schema_version":1,"test_id":"test-1","status":"RUNNING"}`)
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/ci/runs/test-1":
					http.Error(w, "temporary controller failure", http.StatusInternalServerError)
				case r.Method == http.MethodPost && r.URL.Path == "/api/v1/ci/runs/test-1/abort":
					abortCalls.Add(1)
					_, _ = io.WriteString(w, `{"test_id":"test-1","status":"ABORTED"}`)
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
			args.abortOnCancel = test.abort
			args.jsonPath = filepath.Join(filepath.Dir(script), "status-failure.json")
			args.junitPath = filepath.Join(filepath.Dir(script), "status-failure.xml")

			result, err := executeControllerCI(context.Background(), args, nil)
			if err == nil || result.Status != "STATUS_FAILED" || result.Outcome != ciOutcomeExecutionError {
				t.Fatalf("result=%#v err=%v", result, err)
			}
			if got := abortCalls.Load(); got != test.wantAborts {
				t.Fatalf("abort calls = %d, want %d", got, test.wantAborts)
			}
		})
	}
}

func TestExecuteControllerCIAbortsDeterministicRunAfterTimedOutCreate(t *testing.T) {
	aborted := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/ci/runs":
			time.Sleep(100 * time.Millisecond)
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"schema_version":1,"test_id":"late","status":"RUNNING"}`)
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/v1/ci/runs/") && strings.HasSuffix(r.URL.Path, "/abort"):
			testID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/ci/runs/"), "/abort")
			aborted <- testID
			_, _ = fmt.Fprintf(w, `{"schema_version":1,"test_id":%q,"status":"ABORTED"}`, testID)
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
	args.jsonPath = filepath.Join(filepath.Dir(script), "timeout-result.json")
	args.junitPath = filepath.Join(filepath.Dir(script), "timeout-result.xml")
	request := CIRunRequest{
		ProjectID: args.projectID, ScriptPath: args.scriptPath,
		Execution: CIExecution{VUsers: args.vusers, Duration: args.duration.String(), RampUp: args.rampUp.String()},
		Goals:     CIGoals{RequireSLA: args.requireSLA, RequireResults: args.requireResults},
		Source:    sourceFromEnvironment(), PublishCheck: args.publishCheck,
	}
	raw, _ := os.ReadFile(script)
	request.Script = json.RawMessage(raw)
	idempotencyKey := deriveCIIdempotencyKey(request)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	result, err := executeControllerCI(ctx, args, nil)
	if err == nil || result.Outcome != ciOutcomeTimedOut || result.Status != "TIMED_OUT" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	select {
	case got := <-aborted:
		if got != deterministicCITestID(args.projectID, idempotencyKey) {
			t.Fatalf("abort test id = %q, want deterministic CI id", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed-out create did not attempt deterministic abort")
	}
}

func TestDerivedCIIdempotencyKeySeparatesGateConfiguration(t *testing.T) {
	base := CIRunRequest{
		Name:      "gate",
		ScriptID:  "script-1",
		Execution: CIExecution{VUsers: 10, Duration: "1m", RampUp: "0s"},
		Goals:     CIGoals{RequireSLA: true, RequireResults: true},
		Source: CISource{
			ServerURL: "https://github.com", RepositoryID: "1", Repository: "octo/apex",
			WorkflowRef: "octo/apex/.github/workflows/ci.yml@refs/heads/main", RunID: "10", RunAttempt: "1",
		},
	}
	changed := base
	changed.Execution.VUsers = 20
	if deriveCIIdempotencyKey(base) == deriveCIIdempotencyKey(changed) {
		t.Fatal("different gate execution settings produced the same idempotency key")
	}
	changed = base
	threshold := 500.0
	changed.Goals.P95LatencyMs = &threshold
	if deriveCIIdempotencyKey(base) == deriveCIIdempotencyKey(changed) {
		t.Fatal("different gate SLA settings produced the same idempotency key")
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

func newCIScriptRepository(t *testing.T, scriptContents string, extraFiles map[string]string) (repository, scriptPath, commit string) {
	t.Helper()
	return newCIScriptRepositoryAt(t, t.TempDir(), scriptContents, extraFiles)
}

func newCIScriptRepositoryAt(t *testing.T, repository, scriptContents string, extraFiles map[string]string) (string, string, string) {
	t.Helper()
	if err := os.MkdirAll(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	runCITestGit(t, repository, "init", "--quiet")
	runCITestGit(t, repository, "config", "user.email", "apexload-ci@example.test")
	runCITestGit(t, repository, "config", "user.name", "APEX Load CI")
	scriptPath := filepath.Join(repository, "script.json")
	if err := os.WriteFile(scriptPath, []byte(scriptContents), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, contents := range extraFiles {
		path := filepath.Join(repository, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runCITestGit(t, repository, "add", "--", ".")
	runCITestGit(t, repository, "commit", "--quiet", "-m", "trusted script")
	commit := strings.TrimSpace(runCITestGit(t, repository, "rev-parse", "HEAD"))
	if !validCIGitObjectID(commit) {
		t.Fatalf("test repository commit = %q, want Git object ID", commit)
	}
	return repository, scriptPath, commit
}

func runCITestGit(t *testing.T, repository string, arguments ...string) string {
	t.Helper()
	commandArgs := append([]string{"-C", repository}, arguments...)
	command := exec.Command("git", commandArgs...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
	return string(output)
}

func withCIRetryPolicy(t *testing.T, attempts int, baseDelay, maxDelay, window, abortTimeout time.Duration) {
	t.Helper()
	oldAttempts := ciControllerRequestAttempts
	oldBaseDelay := ciControllerRetryBaseDelay
	oldMaxDelay := ciControllerRetryMaxDelay
	oldWindow := ciControllerRetryWindow
	oldAbortTimeout := ciAbortConfirmationTimeout
	ciControllerRequestAttempts = attempts
	ciControllerRetryBaseDelay = baseDelay
	ciControllerRetryMaxDelay = maxDelay
	ciControllerRetryWindow = window
	ciAbortConfirmationTimeout = abortTimeout
	t.Cleanup(func() {
		ciControllerRequestAttempts = oldAttempts
		ciControllerRetryBaseDelay = oldBaseDelay
		ciControllerRetryMaxDelay = oldMaxDelay
		ciControllerRetryWindow = oldWindow
		ciAbortConfirmationTimeout = oldAbortTimeout
	})
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

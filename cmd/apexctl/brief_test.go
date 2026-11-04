package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func briefTestServer(t *testing.T, blocking bool, requests *[]*http.Request) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*requests = append(*requests, r)
		if strings.Contains(r.URL.Path, "missing") {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":"the run has not finished","code":"run_not_finished","remediation":"wait for the run"}`))
			return
		}
		if strings.Contains(r.URL.Path, "old-controller") {
			http.NotFound(w, r)
			return
		}
		verdict := `{"schema":"apexload.agent_brief.v1","verdict":{"status":"pass","blocking":false}}`
		if blocking {
			verdict = `{"schema":"apexload.agent_brief.v1","verdict":{"status":"fail","blocking":true}}`
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("format") {
		case "bundle":
			_, _ = w.Write([]byte(`{"brief":` + verdict + `,"markdown":"## APEX Load: brief"}`))
		case "md":
			w.Header().Set("Content-Type", "text/markdown")
			_, _ = w.Write([]byte("## APEX Load: brief"))
		case "ctrf":
			_, _ = w.Write([]byte(`{"reportFormat":"CTRF","results":{"extra":{"apexload":{"blocking":` + map[bool]string{true: "true", false: "false"}[blocking] + `}}}}`))
		default:
			_, _ = w.Write([]byte(verdict))
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestBriefCommandExitsOnTheBlockingFlagInEveryFormat(t *testing.T) {
	for _, blocking := range []bool{false, true} {
		for _, format := range []string{"json", "md", "ctrf"} {
			var requests []*http.Request
			server := briefTestServer(t, blocking, &requests)
			var out, errOut bytes.Buffer
			code := executeBriefCommand(context.Background(), []string{"run-1", "--api", server.URL, "--format", format, "--detail", "summary"}, &out, &errOut)
			want := exitCodeSuccess
			if blocking {
				want = exitCodeTestFailure
			}
			if code != want {
				t.Fatalf("blocking=%v format=%s: exit %d, want %d (%s)", blocking, format, code, want, errOut.String())
			}
			if format == "md" && (strings.TrimSpace(out.String()) != "## APEX Load: brief" || requests[0].URL.Query().Get("format") != "bundle") {
				t.Fatalf("markdown output %q via %s", out.String(), requests[0].URL.RawQuery)
			}
			if requests[0].URL.Path != "/api/v1/tests/run-1/archive/agent-brief" || requests[0].URL.Query().Get("baseline") != "latest" {
				t.Fatalf("request = %s?%s", requests[0].URL.Path, requests[0].URL.RawQuery)
			}
		}
	}
}

func TestBriefCommandNoFailOutputFileAndErrors(t *testing.T) {
	var requests []*http.Request
	server := briefTestServer(t, true, &requests)
	out := filepath.Join(t.TempDir(), "brief.json")
	var stdout, stderr bytes.Buffer
	if code := executeBriefCommand(context.Background(), []string{"run-1", "--api", server.URL, "--no-fail", "--out", out, "--comparison", "cmp-1"}, &stdout, &stderr); code != exitCodeSuccess {
		t.Fatalf("--no-fail exit %d: %s", code, stderr.String())
	}
	if body, err := os.ReadFile(out); err != nil || !strings.Contains(string(body), `"blocking":true`) {
		t.Fatalf("output file = %q, %v", body, err)
	}
	if query := requests[0].URL.Query(); query.Get("comparison_id") != "cmp-1" || query.Has("baseline") {
		t.Fatalf("comparison query = %s", requests[0].URL.RawQuery)
	}
	for _, tc := range []struct {
		args []string
		code int
		text string
	}{
		{[]string{"missing", "--api", server.URL}, exitCodeControllerOrTransport, "run_not_finished"},
		{[]string{"old-controller", "--api", server.URL}, exitCodeControllerOrTransport, "does not serve agent briefs"},
		{[]string{"run-1", "--api", server.URL, "--detail", "huge"}, exitCodeUsageOrConfiguration, "--detail"},
		{[]string{"run-1", "--api", server.URL, "--format", "xml"}, exitCodeUsageOrConfiguration, "--format"},
		{[]string{}, exitCodeUsageOrConfiguration, ""},
	} {
		stdout.Reset()
		stderr.Reset()
		if code := executeBriefCommand(context.Background(), tc.args, &stdout, &stderr); code != tc.code || !strings.Contains(stderr.String()+stdout.String(), tc.text) {
			t.Errorf("%v: exit %d (want %d), output %q", tc.args, code, tc.code, stderr.String())
		}
	}
}

func TestCIAgentOutputsAreFetchedFromTheCIRouteAndWritten(t *testing.T) {
	var requests []*http.Request
	server := briefTestServer(t, false, &requests)
	dir := t.TempDir()
	args := ciArgs{agentBriefPath: filepath.Join(dir, "brief.json"), ctrfPath: filepath.Join(dir, "ctrf.json"), githubSummary: filepath.Join(dir, "summary.md")}
	client := NewApiClientWithAuth(server.URL, "key", "")
	outputs := fetchCIAgentOutputs(context.Background(), args, nil, client, "run-1")
	for _, r := range requests {
		if r.URL.Path != "/api/v1/ci/runs/run-1/agent-brief" || r.Header.Get("X-APEXLoad-API-Key") != "key" {
			t.Fatalf("request %s with key %q", r.URL.Path, r.Header.Get("X-APEXLoad-API-Key"))
		}
	}
	if len(requests) != 3 {
		t.Fatalf("%d requests", len(requests))
	}
	if err := writeCIAgentOutputs(args, outputs); err != nil {
		t.Fatal(err)
	}
	if err := appendCIAgentSummary(args.githubSummary, outputs.markdown); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{args.agentBriefPath: "apexload.agent_brief.v1", args.ctrfPath: "CTRF", args.githubSummary: "## APEX Load: brief"} {
		if body, err := os.ReadFile(path); err != nil || !strings.Contains(string(body), want) {
			t.Fatalf("%s = %q, %v", path, body, err)
		}
	}
	if none := fetchCIAgentOutputs(context.Background(), ciArgs{}, nil, client, "run-1"); none.brief != nil || none.markdown != nil || none.ctrf != nil {
		t.Fatal("outputs fetched without any flag asking for them")
	}
}

func TestCIAgentOutputFlagsNeedControllerMode(t *testing.T) {
	_, err := parseCIArgs([]string{"--execution-mode", "local", "--publish-check=false", "--script-path", "s.json", "--agent-brief", "brief.json"})
	if err == nil || !strings.Contains(err.Error(), "controller mode") {
		t.Fatalf("local --agent-brief error = %v", err)
	}
	_, err = parseCIArgs([]string{"--execution-mode", "local", "--publish-check=false", "--script-path", "s.json", "--ctrf", "apexload-result.json"})
	if err == nil {
		t.Fatal("local --ctrf accepted")
	}
}

func TestCIGitHubSummaryKeepsTheGateSummaryAndAppendsTheBrief(t *testing.T) {
	dir := t.TempDir()
	args := ciArgs{
		jsonPath: filepath.Join(dir, "result.json"), junitPath: filepath.Join(dir, "result.xml"),
		githubSummary: filepath.Join(dir, "summary.md"), agentBriefPath: filepath.Join(dir, "brief.json"),
	}
	result := CIResult{SchemaVersion: ciSchemaVersion, Outcome: ciOutcomePassed, Status: "COMPLETE", ReportURL: "https://load.example.test/tests/run-1"}
	result.agentOutputs = ciAgentOutputs{markdown: []byte("## APEX Load: PASS (not blocking): run\n"), brief: []byte(`{"schema":"apexload.agent_brief.v1"}`)}
	if err := finalizeCIResult(args, result); err != nil {
		t.Fatal(err)
	}
	summary, _ := os.ReadFile(args.githubSummary)
	gate, brief := strings.Index(string(summary), "## APEX Load performance gate"), strings.Index(string(summary), "## APEX Load: PASS")
	if gate < 0 || brief < gate || !strings.Contains(string(summary), "[Open APEX Load report]") {
		t.Fatalf("summary:\n%s", summary)
	}
	if body, err := os.ReadFile(args.agentBriefPath); err != nil || !strings.Contains(string(body), "agent_brief") {
		t.Fatalf("brief file = %q %v", body, err)
	}
}

func TestCIAgentOutputsRemoveStaleFilesAndStopOnCancel(t *testing.T) {
	dir := t.TempDir()
	args := ciArgs{agentBriefPath: filepath.Join(dir, "brief.json"), ctrfPath: filepath.Join(dir, "ctrf.json")}
	for _, path := range []string{args.agentBriefPath, args.ctrfPath} {
		if err := os.WriteFile(path, []byte("previous run"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeCIAgentOutputs(args, ciAgentOutputs{}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{args.agentBriefPath, args.ctrfPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s from an earlier run survived: %v", path, err)
		}
	}
	block := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-block }))
	defer server.Close()
	defer close(block)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	outputs := fetchCIAgentOutputs(ctx, args, nil, NewApiClientWithAuth(server.URL, "key", ""), "run-1")
	if outputs.brief != nil || outputs.ctrf != nil {
		t.Fatal("a cancelled fetch returned outputs")
	}
}

func TestCIAgentOutputsNeverRemoveAnotherEvidenceFile(t *testing.T) {
	dir := t.TempDir()
	evidence := filepath.Join(dir, "apexload-result.json")
	if err := os.WriteFile(evidence, []byte(`{"outcome":"configuration_error"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	args := ciArgs{jsonPath: evidence, agentBriefPath: evidence}
	if err := writeCIAgentOutputs(args, ciAgentOutputs{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(evidence); err != nil {
		t.Fatalf("the JSON evidence was removed: %v", err)
	}
}

func TestAgentBriefRequestNeverFollowsARedirectOffTheControllerOrigin(t *testing.T) {
	var leaked []string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = append(leaked, r.Header.Get("X-APEXLoad-API-Key")+"|"+r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"verdict":{"blocking":false}}`))
	}))
	defer other.Close()
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer controller.Close()
	for _, client := range []*ApiClient{NewApiClientWithAuth(controller.URL, "secret-key", ""), NewApiClientWithAuth(controller.URL, "", "secret-token")} {
		if _, err := client.agentBriefRequest(context.Background(), "/api/v1/tests/run-1/archive/agent-brief", nil); err == nil || !strings.Contains(err.Error(), "cross-origin redirect") {
			t.Fatalf("redirect error = %v", err)
		}
	}
	if len(leaked) != 0 {
		t.Fatalf("credentials reached another origin: %v", leaked)
	}
	if got := briefErrorExcerpt([]byte("line1\nline2\x1b[31m" + strings.Repeat("x", 400))); strings.ContainsAny(got, "\n\x1b") || len(got) > 210 {
		t.Fatalf("excerpt = %q", got)
	}
}

func TestBriefMarkdownOnGitHubActionsStdoutStopsWorkflowCommands(t *testing.T) {
	var requests []*http.Request
	server := briefTestServer(t, false, &requests)
	t.Setenv("GITHUB_ACTIONS", "true")
	var out, errOut bytes.Buffer
	if code := executeBriefCommand(context.Background(), []string{"run-1", "--api", server.URL, "--format", "md"}, &out, &errOut); code != exitCodeSuccess {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if !strings.HasPrefix(lines[0], "::stop-commands::apexload-") || lines[len(lines)-1] != "::"+strings.TrimPrefix(lines[0], "::stop-commands::")+"::" {
		t.Fatalf("output = %q", out.String())
	}
}

func TestBriefJSONOnGitHubActionsStdoutStaysPlainJSON(t *testing.T) {
	var requests []*http.Request
	server := briefTestServer(t, false, &requests)
	t.Setenv("GITHUB_ACTIONS", "true")
	var out, errOut bytes.Buffer
	if code := executeBriefCommand(context.Background(), []string{"run-1", "--api", server.URL}, &out, &errOut); code != exitCodeSuccess {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if !strings.HasPrefix(out.String(), "{") {
		t.Fatalf("JSON stdout was wrapped: %q", out.String())
	}
}

func TestBriefJSONOnGitHubActionsEscapesLegacyWorkflowCommands(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"verdict":{"blocking":false},"findings":[{"untrusted":{"text":"oops ##[warning]forged"}}]}`))
	}))
	defer server.Close()
	t.Setenv("GITHUB_ACTIONS", "true")
	var out, errOut bytes.Buffer
	if code := executeBriefCommand(context.Background(), []string{"run-1", "--api", server.URL}, &out, &errOut); code != exitCodeSuccess {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if strings.Contains(out.String(), "##[") {
		t.Fatalf("legacy command survived: %s", out.String())
	}
	var decoded struct {
		Findings []struct {
			Untrusted struct{ Text string } `json:"untrusted"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil || decoded.Findings[0].Untrusted.Text != "oops ##[warning]forged" {
		t.Fatalf("decoded = %+v, %v", decoded, err)
	}
}

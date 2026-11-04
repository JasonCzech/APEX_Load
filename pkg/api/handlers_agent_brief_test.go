package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

// TestAgentBriefEndpointsServeARealArchivedRun runs a short test to an
// archive and reads its brief through every route and format.
func TestAgentBriefEndpointsServeARealArchivedRun(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})
	protocolName := "agent-brief-stub-" + uuid.NewString()
	protocol.Register(protocolName, func() protocol.Protocol { return &archiveQueryStubProtocol{} })
	server.runner.StoreScript(&dsl.Script{ID: "script-agent-brief", Name: "Agent Brief Script", Protocol: protocolName, Version: "1.0.0",
		Actions: []dsl.Action{{Name: "step-1", Type: "request", Target: "/"}}})
	run, err := server.runner.CreateTest(CreateTestRequest{Name: "Agent Brief Run", Duration: "1500ms", ScriptID: "script-agent-brief", VUsers: 1,
		ScenarioMode: "goal_oriented", GoalConfig: &GoalConfig{P95LatencyMs: ptr(100000.0), MaxErrorPct: ptr(99.0)}})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.runner.StartTest(run.ID); err != nil {
		t.Fatal(err)
	}
	waitForTestStatus(t, server.runner, run.ID, "COMPLETE")
	server.runner.mu.Lock()
	server.runner.tests["still-running"] = &ManagedTest{ID: "still-running", Name: "Running", Status: "RUNNING", CreatedAt: time.Now()}
	server.runner.mu.Unlock()

	get := func(path string, header ...string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		for i := 0; i+1 < len(header); i += 2 {
			req.Header.Set(header[i], header[i+1])
		}
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		return resp
	}
	base := "/api/v1/tests/" + run.ID + "/archive/agent-brief"

	resp := get(base)
	if resp.Code != http.StatusOK {
		t.Fatalf("brief status = %d body=%s", resp.Code, resp.Body.String())
	}
	var brief AgentBrief
	if err := json.Unmarshal(resp.Body.Bytes(), &brief); err != nil {
		t.Fatal(err)
	}
	if brief.Run.ID != run.ID || brief.Verdict.Source != "report_verdict" || brief.DetailLevel != agentDetailStandard {
		t.Fatalf("brief = %+v", brief.Verdict)
	}
	// A stub run's measurement validity varies with the host; the verdict
	// must follow it either way.
	if brief.Validity.Verdict == "invalid" {
		if brief.Verdict.Status != agentStatusInvalid || !brief.Verdict.Blocking || agentFindingByKind(&brief, agentKindValidity) == nil {
			t.Fatalf("invalid run verdict = %+v", brief.Verdict)
		}
	} else if brief.Verdict.Status == agentStatusInvalid || brief.Verdict.Status == agentStatusFail {
		t.Fatalf("valid passing run verdict = %+v", brief.Verdict)
	}
	if brief.Baseline.Selection != agentBaselineSelectionLatest || !containsString(brief.Verdict.ReasonCodes, agentReasonBaselineUnavailable) {
		t.Fatalf("baseline = %+v reasons = %v", brief.Baseline, brief.Verdict.ReasonCodes)
	}
	schema := compileAgentContractSchema(t, "brief.schema.json")
	decoded, err := jsonschema.UnmarshalJSON(bytes.NewReader(resp.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(decoded); err != nil {
		t.Fatalf("served brief does not match its schema: %v", err)
	}
	etag := resp.Header().Get("ETag")
	if etag != `"`+brief.Digest+`"` {
		t.Fatalf("ETag = %q, digest %q", etag, brief.Digest)
	}
	if again := get(base); !bytes.Equal(again.Body.Bytes(), resp.Body.Bytes()) {
		t.Fatal("two reads of the same archive differ")
	}
	if notModified := get(base, "If-None-Match", etag); notModified.Code != http.StatusNotModified || notModified.Body.Len() != 0 {
		t.Fatalf("conditional read = %d", notModified.Code)
	}

	if md := get(base + "?format=md&detail=summary"); md.Code != http.StatusOK || !strings.HasPrefix(md.Header().Get("Content-Type"), "text/markdown") || !strings.Contains(md.Body.String(), "## APEX Load: ") {
		t.Fatalf("markdown = %d %q", md.Code, md.Body.String())
	}
	var bundle struct {
		Brief    *AgentBrief `json:"brief"`
		Markdown string      `json:"markdown"`
	}
	if b := get(base + "?format=bundle"); b.Code != http.StatusOK || json.Unmarshal(b.Body.Bytes(), &bundle) != nil || bundle.Brief == nil || bundle.Markdown == "" {
		t.Fatalf("bundle = %d %s", b.Code, b.Body.String())
	}
	var ctrf AgentCTRFReport
	if c := get(base + "?format=ctrf"); c.Code != http.StatusOK || json.Unmarshal(c.Body.Bytes(), &ctrf) != nil || ctrf.ReportFormat != "CTRF" || ctrf.Results.Summary.Tests == 0 {
		t.Fatalf("ctrf = %d %s", c.Code, c.Body.String())
	}

	for _, tc := range []struct {
		path, code string
		status     int
	}{
		{base + "?detail=everything", agentErrInvalidDetail, 400},
		{base + "?format=xml", agentErrInvalidFormat, 400},
		{base + "?baseline=previous", agentErrInvalidBaseline, 400},
		{base + "?comparison_id=missing", agentErrComparisonNotFound, 404},
		{"/api/v1/tests/no-such-run/archive/agent-brief", agentErrRunNotFound, 404},
		{"/api/v1/tests/still-running/archive/agent-brief", agentErrRunNotFinished, 409},
		{base + "/findings/fnd_sla_000000000000", agentErrFindingNotFound, 404},
		{"/api/v1/ci/runs/" + run.ID + "/agent-brief", agentErrRunNotFound, 404},
	} {
		resp := get(tc.path)
		var body map[string]string
		_ = json.Unmarshal(resp.Body.Bytes(), &body)
		if resp.Code != tc.status || body["code"] != tc.code {
			t.Errorf("%s = %d %v, want %d %s", tc.path, resp.Code, body, tc.status, tc.code)
		}
	}
	var full AgentBrief
	_ = json.Unmarshal(get(base+"?detail=full").Body.Bytes(), &full)
	if len(full.Findings) > 0 {
		findingPath := base + "/findings/" + full.Findings[0].ID
		first := get(findingPath)
		if first.Code != http.StatusOK || first.Header().Get("ETag") == "" {
			t.Fatalf("finding = %d %s", first.Code, first.Body.String())
		}
		if again := get(findingPath, "If-None-Match", first.Header().Get("ETag")); again.Code != http.StatusNotModified {
			t.Fatalf("conditional finding read = %d", again.Code)
		}
	}
	// Every evidence link a brief emits serves something.
	for _, finding := range full.Findings {
		for _, evidence := range finding.Evidence {
			if resp := get("/api/v1" + evidence.URI); resp.Code >= 400 {
				t.Errorf("evidence %s for %s = %d %s", evidence.URI, finding.ID, resp.Code, resp.Body.String())
			}
		}
	}
	if resp := get("/api/v1/tests/still-running/archive/agent-brief"); !strings.Contains(resp.Body.String(), "apex_wait_for_run") {
		t.Errorf("run_not_finished gives no remediation: %s", resp.Body.String())
	}
}

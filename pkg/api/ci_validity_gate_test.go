package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/rca"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestCIValidityGateOutcomes(t *testing.T) {
	tests := []struct {
		name, gate, verdict, status string
		breached, monitoringGap     bool
		want                        string
		enforced                    bool
	}{
		{name: "advisory invalid", verdict: rca.ValidityInvalid, status: "COMPLETE", want: "passed"},
		{name: "invalid gate fails invalid", gate: ciValidityGateInvalid, verdict: rca.ValidityInvalid, status: "COMPLETE", want: "run_invalid", enforced: true},
		{name: "invalid gate passes suspect", gate: ciValidityGateInvalid, verdict: rca.ValiditySuspect, status: "COMPLETE", want: "passed"},
		{name: "suspect gate fails suspect", gate: ciValidityGateSuspect, verdict: rca.ValiditySuspect, status: "COMPLETE", want: "run_invalid", enforced: true},
		{name: "suspect gate fails invalid", gate: ciValidityGateSuspect, verdict: rca.ValidityInvalid, status: "COMPLETE", want: "run_invalid", enforced: true},
		{name: "unknown never fails", gate: ciValidityGateSuspect, verdict: rca.ValidityUnknown, status: "COMPLETE", want: "passed"},
		{name: "valid passes", gate: ciValidityGateSuspect, verdict: rca.ValidityValid, status: "COMPLETE", want: "passed"},
		{name: "invalid outranks an SLA breach", gate: ciValidityGateInvalid, verdict: rca.ValidityInvalid, status: "COMPLETE", breached: true, want: "run_invalid", enforced: true},
		{name: "invalid outranks incomplete monitoring", gate: ciValidityGateInvalid, verdict: rca.ValidityInvalid, status: "COMPLETE", monitoringGap: true, want: "run_invalid", enforced: true},
		{name: "failed run stays an execution error", gate: ciValidityGateInvalid, verdict: rca.ValidityInvalid, status: "FAILED", want: "execution_error"},
		{name: "aborted run stays cancelled", gate: ciValidityGateInvalid, verdict: rca.ValidityInvalid, status: "ABORTED", want: "cancelled"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := ciRunResponse{
				Status: test.status, SLABreached: test.breached, SLAEvaluated: true, ResultsPresent: true, MonitoringIncomplete: test.monitoringGap,
				Validity: &ciRunValidity{Verdict: test.verdict, Reasons: []string{rca.RuleDroppedResults}},
			}
			finalizeCIRunOutcome(&response, ciRunMetadata{ValidityGate: test.gate})
			if response.Outcome != test.want || response.Validity.Enforced != test.enforced || response.Validity.Gate != test.gate {
				t.Fatalf("outcome %q enforced %t gate %q, want %q %t %q", response.Outcome, response.Validity.Enforced, response.Validity.Gate, test.want, test.enforced, test.gate)
			}
			if test.enforced && (!strings.Contains(response.Error, test.verdict) || !strings.Contains(response.Error, rca.RuleDroppedResults)) {
				t.Fatalf("gate failure error %q does not name the verdict and its reasons", response.Error)
			}
		})
	}
}

func TestCIValidityGateWithoutVerdictKeepsTheOutcome(t *testing.T) {
	response := ciRunResponse{Status: "COMPLETE", SLAEvaluated: true, ResultsPresent: true}
	finalizeCIRunOutcome(&response, ciRunMetadata{ValidityGate: ciValidityGateSuspect})
	if response.Outcome != "passed" || response.Validity != nil {
		t.Fatalf("a run without a validity verdict = %q %+v", response.Outcome, response.Validity)
	}
}

func TestCIValidityGateNeverPassesARunItCouldNotJudge(t *testing.T) {
	tests := []struct {
		name, gate string
		breached   bool
		want       string
	}{
		{name: "gated pass fails closed", gate: ciValidityGateInvalid, want: "execution_error"},
		{name: "gated breach stays a breach", gate: ciValidityGateInvalid, breached: true, want: "sla_breached"},
		{name: "advisory pass stays a pass", want: "passed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := ciRunResponse{Status: "COMPLETE", SLAEvaluated: true, SLABreached: test.breached, ResultsPresent: true, validityUnavailable: true}
			finalizeCIRunOutcome(&response, ciRunMetadata{ValidityGate: test.gate})
			if response.Outcome != test.want {
				t.Fatalf("outcome = %q, want %q", response.Outcome, test.want)
			}
			if test.want == "execution_error" && response.Error != ciValidityUnavailableReason {
				t.Fatalf("error = %q", response.Error)
			}
		})
	}
}

func TestCachedRunArchiveValidityReportsAnUnbuildableReport(t *testing.T) {
	record := &storage.RunArchiveRecord{TestID: "missing", ArchivePath: t.TempDir(), ManifestPath: "/nonexistent/manifest.json", ManifestSHA256: strings.Repeat("0", 64)}
	if validity, err := newArchiveReadCaches().cachedRunArchiveValidity(record); err == nil || validity != nil {
		t.Fatalf("unbuildable report = %+v, %v", validity, err)
	}
}

func TestCIRunValidityComesFromTheDurableArchiveReport(t *testing.T) {
	for _, gate := range []string{"", ciValidityGateInvalid} {
		t.Run("gate="+gate, func(t *testing.T) {
			t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
			t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
			server, store := newCIContractTestServer(t, ciRunSourceTestPolicy())
			testRun := createTerminalCIContractTest(t, server, store, "COMPLETE", "")
			setCIRunValidityGate(t, server, testRun, gate)
			record := persistValidCIRunArchive(t, store, testRun, 2, true, false, nil)
			// The report built over this archive judged the run invalid.
			if _, err := server.archiveReadCaches().reports.getOrLoad(runArchiveReportCacheKey(record), func() (*RunArchiveReport, error) {
				return &RunArchiveReport{Validity: &rca.Validity{Verdict: rca.ValidityInvalid, Reasons: []string{rca.RuleDroppedResults}}}, nil
			}); err != nil {
				t.Fatal(err)
			}
			testRun, _ = server.runner.GetTest(testRun.ID)
			response, err := server.buildCIRunResponse(context.Background(), testRun)
			if err != nil {
				t.Fatalf("buildCIRunResponse: %v", err)
			}
			if response.Validity == nil || response.Validity.Verdict != rca.ValidityInvalid || response.Validity.Gate != gate {
				t.Fatalf("validity = %+v", response.Validity)
			}
			want := "passed"
			if gate != "" {
				want = "run_invalid"
			}
			if response.Outcome != want || response.Validity.Enforced != (gate != "") {
				t.Fatalf("outcome = %q enforced %t, want %q", response.Outcome, response.Validity.Enforced, want)
			}
			summary := ciGitHubCheckSummary(testRun, response, ciRunMetadata{Source: ciTestRunSource("1")})
			if !strings.Contains(summary, "| Run validity | `invalid` |") || !strings.Contains(summary, "| Validity gate | `"+firstNonEmpty(gate, "advisory")+"` |") {
				t.Fatalf("check summary omits validity: %s", summary)
			}
			if gate != "" && (ciGitHubCheckConclusion(response) != "failure" || !strings.Contains(summary, "Validity gate: Run validity is invalid")) {
				t.Fatalf("gate failure not reported to the check: %s", summary)
			}
		})
	}
}

func TestCIRunWithAnUnjudgedArchivePassesTheGate(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	server, store := newCIContractTestServer(t, ciRunSourceTestPolicy())
	testRun := createTerminalCIContractTest(t, server, store, "COMPLETE", "")
	setCIRunValidityGate(t, server, testRun, ciValidityGateSuspect)
	persistValidCIRunArchive(t, store, testRun, 2, true, false, nil)
	testRun, _ = server.runner.GetTest(testRun.ID)
	response, err := server.buildCIRunResponse(context.Background(), testRun)
	if err != nil {
		t.Fatalf("buildCIRunResponse: %v", err)
	}
	// An archive without interval summaries cannot be judged; the gate
	// judges evidence, not its absence.
	if response.Outcome != "passed" || response.Validity == nil || response.Validity.Verdict != rca.ValidityUnknown || response.Validity.Enforced || response.Validity.Gate != ciValidityGateSuspect {
		t.Fatalf("outcome = %q, validity %+v", response.Outcome, response.Validity)
	}
}

// setCIRunValidityGate rewrites a CI run's accepted metadata with a gate.
func setCIRunValidityGate(t *testing.T, server *Server, testRun *ManagedTest, gate string) {
	t.Helper()
	server.runner.mu.Lock()
	defer server.runner.mu.Unlock()
	managed := server.runner.tests[testRun.ID]
	metadata, err := decodeCIRunMetadata(managed.MaterializationKey)
	if err != nil {
		t.Fatal(err)
	}
	metadata.ValidityGate = gate
	if managed.MaterializationKey, err = encodeCIRunMetadata(metadata); err != nil {
		t.Fatal(err)
	}
}

func TestCreateCIRunFreezesTheValidityGateAtAcceptance(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	policy := ciRunSourceTestPolicy()
	policy.MaxDurationMinutes = 1
	policy.AllowedTargetHosts = []string{"127.0.0.1"}
	policy.ValidityGate = " Suspect "
	server, store := newCIContractTestServer(t, policy)
	publishCheck := false
	scriptRaw, err := json.Marshal(ciTestScript(target.URL))
	if err != nil {
		t.Fatal(err)
	}
	request := ciRunCreateRequest{
		ProjectID: "project-ci", ScriptPath: "tests/performance.json", Script: scriptRaw,
		Execution: ciRunExecutionRequest{VUsers: 1, Duration: "50ms"}, Source: ciTestRunSource("1"), PublishCheck: &publishCheck,
	}
	const key = "octo/apex:performance:9001:1:script-sha256-validity"
	invoke := func() (int, ciRunResponse) {
		t.Helper()
		body, _ := json.Marshal(request)
		httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/ci/runs", bytes.NewReader(body))
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Idempotency-Key", key)
		recorder := httptest.NewRecorder()
		server.handleCreateCIRun(recorder, httpReq)
		var response ciRunResponse
		_ = json.Unmarshal(recorder.Body.Bytes(), &response)
		return recorder.Code, response
	}
	gateOf := func(id string) string {
		t.Helper()
		test, ok := server.runner.GetTest(id)
		if !ok {
			t.Fatalf("run %s not found", id)
		}
		metadata, err := decodeCIRunMetadata(test.MaterializationKey)
		if err != nil {
			t.Fatal(err)
		}
		return metadata.ValidityGate
	}
	status, first := invoke()
	if status != http.StatusAccepted || gateOf(first.TestID) != ciValidityGateSuspect {
		t.Fatalf("create = %d, gate %q", status, gateOf(first.TestID))
	}
	// A later policy change is a new revision; the accepted run keeps its
	// gate, and an exact retry is still the same run.
	project, err := store.GetProject("project-ci")
	if err != nil {
		t.Fatal(err)
	}
	project.Settings.GitHub.Actions.ValidityGate = ""
	project.Settings.GitHub.Actions.Revision++
	if err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	status, retry := invoke()
	if status != http.StatusAccepted || retry.TestID != first.TestID || gateOf(first.TestID) != ciValidityGateSuspect {
		t.Fatalf("retry = %d %q, gate %q", status, retry.TestID, gateOf(first.TestID))
	}
}

func TestGitHubActionsPolicyValidatesTheValidityGate(t *testing.T) {
	for gate, ok := range map[string]bool{"": true, "invalid": true, " SUSPECT ": true, "warn": false, "valid": false} {
		err := validateGitHubActionsPolicy(storage.GitHubActionsPolicy{ValidityGate: gate})
		if (err == nil) != ok {
			t.Fatalf("validity_gate %q: err %v, want ok=%t", gate, err, ok)
		}
	}
}

func TestValidityGateChangeIsANewPolicyRevision(t *testing.T) {
	server, _ := newCIContractTestServer(t, ciRunSourceTestPolicy())
	prior := ciRunSourceTestPolicy()
	prior.Revision = 3
	next := prior
	next.ValidityGate = ciValidityGateInvalid
	stamped := server.stampGitHubActionsPolicyUpdate(httptest.NewRequest(http.MethodPut, "/", nil), prior, next)
	if stamped.Revision != 4 {
		t.Fatalf("revision = %d, want 4", stamped.Revision)
	}
	if githubProjectPoliciesEqual(storage.GitHubProjectPolicy{Actions: prior}, storage.GitHubProjectPolicy{Actions: next}) {
		t.Fatal("a gate change must be a GitHub policy mutation, which requires system_admin")
	}
}

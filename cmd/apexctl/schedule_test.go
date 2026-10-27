package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/runcontract"
)

func TestParseAnalysisPolicyFlag(t *testing.T) {
	sel, isDefault, err := parseAnalysisPolicyFlag("--browser-policy", "")
	if err != nil || sel != nil || isDefault {
		t.Fatalf("empty flag: sel=%+v isDefault=%t err=%v", sel, isDefault, err)
	}
	sel, isDefault, err = parseAnalysisPolicyFlag("--browser-policy", "project_default")
	if err != nil || sel != nil || !isDefault {
		t.Fatalf("project_default: sel=%+v isDefault=%t err=%v", sel, isDefault, err)
	}
	sel, isDefault, err = parseAnalysisPolicyFlag("--browser-policy", "vitals-policy@3")
	if err != nil || isDefault || sel == nil || sel.ID != "vitals-policy" || sel.Revision != 3 {
		t.Fatalf("valid policy: sel=%+v isDefault=%t err=%v", sel, isDefault, err)
	}
	for _, bad := range []string{
		" vitals@1",
		"vitals@1 ",
		"vitals",
		"@1",
		"vitals@",
		"vitals@0",
		"vitals@-1",
		"vitals@01",
		"vitals@1@2",
		"vitals @1",
		strings.Repeat("a", 257) + "@1",
	} {
		if _, _, err := parseAnalysisPolicyFlag("--browser-policy", bad); err == nil {
			t.Fatalf("parseAnalysisPolicyFlag(%q) unexpectedly succeeded", bad)
		}
	}
}

func TestParseRunArgsBrowserAndJourneyPoliciesRequireAPI(t *testing.T) {
	t.Setenv("APEXLOAD_API", "")
	noAPI := parseRunArgs([]string{"--script", "example.json", "--browser-policy", "vitals@2"})
	if noAPI.err == nil || !strings.Contains(noAPI.err.Error(), "requires --api") {
		t.Fatalf("expected requires --api error, got %v", noAPI.err)
	}
	withAPI := parseRunArgs([]string{
		"--script", "example.json",
		"--api", "http://127.0.0.1:8080",
		"--browser-policy", "vitals@2",
		"--journey-policy", "checkout@4",
	})
	if withAPI.err != nil ||
		withAPI.browserPolicy == nil || withAPI.browserPolicy.ID != "vitals" || withAPI.browserPolicy.Revision != 2 ||
		withAPI.journeyPolicy == nil || withAPI.journeyPolicy.ID != "checkout" || withAPI.journeyPolicy.Revision != 4 {
		t.Fatalf("withAPI = %#v", withAPI)
	}
	badSyntax := parseRunArgs([]string{"--script", "example.json", "--api", "http://127.0.0.1:8080", "--browser-policy", "vitals@0"})
	if badSyntax.err == nil {
		t.Fatal("expected syntax error for revision 0")
	}
}

func TestParseCIArgsBrowserAndJourneyPoliciesRequireControllerMode(t *testing.T) {
	t.Setenv("APEXLOAD_API_KEY", "ci-key")
	local := []string{
		"--execution-mode", "local",
		"--publish-check=false",
		"--script-path", "script.json",
		"--browser-policy", "vitals@2",
	}
	if _, err := parseCIArgs(local); err == nil || !strings.Contains(err.Error(), "requires controller mode") {
		t.Fatalf("expected controller mode error for local CI mode, got %v", err)
	}
	controller, err := parseCIArgs([]string{
		"--controller-url", "https://controller.example",
		"--project-id", "project-1",
		"--script-id", "script-1",
		"--auth-mode", "api-key",
		"--browser-policy", "vitals@2",
		"--journey-policy", "checkout@3",
	})
	if err != nil {
		t.Fatalf("parseCIArgs controller: %v", err)
	}
	if controller.browserPolicy == nil || controller.browserPolicy.ID != "vitals" || controller.browserPolicy.Revision != 2 ||
		controller.journeyPolicy == nil || controller.journeyPolicy.ID != "checkout" || controller.journeyPolicy.Revision != 3 {
		t.Fatalf("controller policies = browser:%#v journey:%#v", controller.browserPolicy, controller.journeyPolicy)
	}
}

func TestAPIClientCreateTestEchoesAndCleansUpUnconfirmedAnalysisPolicySelections(t *testing.T) {
	t.Setenv("APEXLOAD_ALLOW_INSECURE_HTTP", "true")
	var deletedID string
	echoSelections := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/tests":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode create test body: %v", err)
			}
			resp := map[string]any{"id": "test-echo-1", "status": "PENDING"}
			if echoSelections {
				resp["analysis_policy_selections"] = body["analysis_policy_selections"]
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(resp)
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/v1/tests/"):
			deletedID = strings.TrimPrefix(r.URL.Path, "/api/v1/tests/")
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client := NewApiClient(srv.URL)
	client.BrowserPolicy = &runcontract.AnalysisPolicySelection{ID: "vitals", Revision: 2}
	if _, err := client.createTestContext(context.Background(), "script-1", 1, "1m", "0s", nil); err == nil || !strings.Contains(err.Error(), "vitals@2") {
		t.Fatalf("expected unconfirmed analysis policy error, got %v", err)
	}
	if deletedID != "test-echo-1" {
		t.Fatalf("expected unconfirmed test-echo-1 to be deleted, deletedID=%q", deletedID)
	}

	echoSelections = true
	id, err := client.createTestContext(context.Background(), "script-1", 1, "1m", "0s", nil)
	if err != nil || id != "test-echo-1" {
		t.Fatalf("createTestContext with echoed selection: id=%q err=%v", id, err)
	}
}

func TestExecuteScheduleLifecycleAndEchoVerification(t *testing.T) {
	t.Setenv("APEXLOAD_ALLOW_INSECURE_HTTP", "true")
	if handler, ok := apexctlCommandHandlers["schedule"]; !ok || handler == nil {
		t.Fatal("apexctl schedule is not wired into apexctlCommandHandlers")
	}

	var (
		deletedScheduleID string
		echoOnCreate      = true
		stored            = existingScheduleWire{
			ID:              "sched-1",
			Name:            "Nightly",
			CronExpr:        "0 9 * * *",
			Timezone:        "America/New_York",
			ReleaseID:       "rel-1",
			BlackoutWindows: json.RawMessage(`[{"name":"freeze"}]`),
			Enabled:         true,
			AnalysisPolicySelections: &runcontract.AnalysisPolicySelections{
				Browser: &runcontract.AnalysisPolicySelection{ID: "vitals", Revision: 2},
				Journey: &runcontract.AnalysisPolicySelection{ID: "checkout", Revision: 1},
			},
		}
		lastPutPayload scheduleUpsertPayload
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/scenarios/scen-1/schedules":
			_ = json.NewEncoder(w).Encode(map[string]any{"schedules": []existingScheduleWire{stored}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/scenarios/scen-1/schedules/preview":
			var payload scheduleUpsertPayload
			_ = json.NewDecoder(r.Body).Decode(&payload)
			_ = json.NewEncoder(w).Encode(map[string]any{"valid": true, "schedule": payload})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/scenarios/scen-1/schedules":
			var payload scheduleUpsertPayload
			_ = json.NewDecoder(r.Body).Decode(&payload)
			resp := existingScheduleWire{
				ID:       "sched-created",
				Name:     payload.Name,
				CronExpr: payload.CronExpr,
			}
			if echoOnCreate {
				resp.AnalysisPolicySelections = payload.AnalysisPolicySelections
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(resp)
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/scenarios/scen-1/schedules/sched-1":
			lastPutPayload = scheduleUpsertPayload{}
			_ = json.NewDecoder(r.Body).Decode(&lastPutPayload)
			stored.Name = lastPutPayload.Name
			stored.CronExpr = lastPutPayload.CronExpr
			if lastPutPayload.AnalysisPolicySelections != nil {
				if lastPutPayload.AnalysisPolicySelections.Browser == nil && lastPutPayload.AnalysisPolicySelections.Journey == nil {
					stored.AnalysisPolicySelections = nil
				} else {
					stored.AnalysisPolicySelections = lastPutPayload.AnalysisPolicySelections
				}
			}
			_ = json.NewEncoder(w).Encode(stored)
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/v1/scenarios/scen-1/schedules/"):
			deletedScheduleID = strings.TrimPrefix(r.URL.Path, "/api/v1/scenarios/scen-1/schedules/")
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/scenarios/scen-1/schedules/sched-1/occurrences":
			_ = json.NewEncoder(w).Encode(map[string]any{"occurrences": []map[string]any{{"id": "occ-1", "status": "failed", "reason_code": "analysis_policy_unavailable"}}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/scenarios/scen-1/schedules/sched-1/occurrences/occ-1/resolve":
			var payload scheduleResolvePayload
			_ = json.NewDecoder(r.Body).Decode(&payload)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "occ-1", "action": payload.Action, "reason": payload.Reason})
		default:
			http.Error(w, "not found: "+r.Method+" "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	run := func(args ...string) (int, string, string) {
		var stdout, stderr bytes.Buffer
		code := executeSchedule(context.Background(), args, &stdout, &stderr)
		return code, stdout.String(), stderr.String()
	}

	// 1. list
	if code, out, errOut := run("list", "--api", srv.URL, "--scenario", "scen-1"); code != exitCodeSuccess || !strings.Contains(out, "sched-1") {
		t.Fatalf("list: code=%d out=%s err=%s", code, out, errOut)
	}
	// 2. preview
	if code, out, errOut := run("preview", "--api", srv.URL, "--scenario", "scen-1", "--name", "Nightly", "--cron", "0 9 * * *", "--browser-policy", "vitals@2", "--count", "3"); code != exitCodeSuccess || !strings.Contains(out, `"revision": 2`) {
		t.Fatalf("preview: code=%d out=%s err=%s", code, out, errOut)
	}
	// 3. create with echoed selections
	if code, out, errOut := run("create", "--api", srv.URL, "--scenario", "scen-1", "--name", "Nightly", "--cron", "0 9 * * *", "--browser-policy", "vitals@2", "--journey-policy", "checkout@1"); code != exitCodeSuccess || !strings.Contains(out, "sched-created") {
		t.Fatalf("create: code=%d out=%s err=%s", code, out, errOut)
	}
	// 4. create when controller fails to echo selections deletes the created schedule and fails closed
	echoOnCreate = false
	if code, _, errOut := run("create", "--api", srv.URL, "--scenario", "scen-1", "--name", "Nightly", "--cron", "0 9 * * *", "--browser-policy", "vitals@2"); code != exitCodeControllerOrTransport || deletedScheduleID != "sched-created" || !strings.Contains(errOut, "did not confirm") {
		t.Fatalf("unconfirmed create: code=%d deleted=%q err=%s", code, deletedScheduleID, errOut)
	}
	echoOnCreate = true
	// 5. partial update changing only --browser-policy preserves existing journey policy
	if code, _, errOut := run("update", "--api", srv.URL, "--scenario", "scen-1", "--schedule", "sched-1", "--expected-revision", "1", "--browser-policy", "vitals@3"); code != exitCodeSuccess {
		t.Fatalf("partial update: code=%d err=%s", code, errOut)
	}
	if lastPutPayload.AnalysisPolicySelections == nil ||
		lastPutPayload.AnalysisPolicySelections.Browser == nil || lastPutPayload.AnalysisPolicySelections.Browser.Revision != 3 ||
		lastPutPayload.AnalysisPolicySelections.Journey == nil || lastPutPayload.AnalysisPolicySelections.Journey.ID != "checkout" || lastPutPayload.AnalysisPolicySelections.Journey.Revision != 1 {
		t.Fatalf("partial update did not preserve journey policy: %+v", lastPutPayload.AnalysisPolicySelections)
	}
	// 6. update with project_default on both clears selections and preserves windows/timezone even when --name and --cron are passed
	if code, _, errOut := run("update", "--api", srv.URL, "--scenario", "scen-1", "--schedule", "sched-1", "--expected-revision", "2", "--name", "Nightly Renamed", "--cron", "0 10 * * *", "--browser-policy", "project_default", "--journey-policy", "project_default"); code != exitCodeSuccess {
		t.Fatalf("clear update: code=%d err=%s", code, errOut)
	}
	if lastPutPayload.AnalysisPolicySelections == nil || lastPutPayload.AnalysisPolicySelections.Browser != nil || lastPutPayload.AnalysisPolicySelections.Journey != nil {
		t.Fatalf("clear update did not send empty AnalysisPolicySelections{}: %+v", lastPutPayload.AnalysisPolicySelections)
	}
	if lastPutPayload.Timezone != "America/New_York" || lastPutPayload.ReleaseID != "rel-1" || string(lastPutPayload.BlackoutWindows) != `[{"name":"freeze"}]` {
		t.Fatalf("update with --name and --cron dropped existing schedule fields: %+v", lastPutPayload)
	}
	// 7. occurrences
	if code, out, errOut := run("occurrences", "--api", srv.URL, "--scenario", "scen-1", "--schedule", "sched-1", "--limit", "10"); code != exitCodeSuccess || !strings.Contains(out, "analysis_policy_unavailable") {
		t.Fatalf("occurrences: code=%d out=%s err=%s", code, out, errOut)
	}
	// 8. resolve
	if code, out, errOut := run("resolve", "--api", srv.URL, "--scenario", "scen-1", "--schedule", "sched-1", "--occurrence", "occ-1", "--action", "retry", "--reason", "restored policy", "--expected-revision", "1"); code != exitCodeSuccess || !strings.Contains(out, "restored policy") {
		t.Fatalf("resolve: code=%d out=%s err=%s", code, out, errOut)
	}
	// 9. delete
	deletedScheduleID = ""
	if code, _, errOut := run("delete", "--api", srv.URL, "--scenario", "scen-1", "--schedule", "sched-1"); code != exitCodeSuccess || deletedScheduleID != "sched-1" {
		t.Fatalf("delete: code=%d deleted=%q err=%s", code, deletedScheduleID, errOut)
	}
}

func TestExecuteControllerCIEchoesAndAbortsUnconfirmedAnalysisPolicySelections(t *testing.T) {
	t.Setenv("APEXLOAD_ALLOW_INSECURE_HTTP", "true")
	var (
		abportedID string
		echo       bool
		lastKey    string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/ci/runs":
			var req CIRunRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode CIRunRequest: %v", err)
			}
			lastKey = r.Header.Get("Idempotency-Key")
			resp := CIRunResponse{SchemaVersion: 1, TestID: "ci-unconfirmed-1", Status: "COMPLETE", Outcome: ciOutcomePassed}
			if echo {
				resp.AnalysisPolicySelections = req.AnalysisPolicySelections
			} else if req.AnalysisPolicySelections != nil {
				resp.Status, resp.Outcome = "RUNNING", ""
			}
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(resp)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/ci/runs/ci-unconfirmed-1/abort":
			abportedID = "ci-unconfirmed-1"
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"schema_version":1,"test_id":"ci-unconfirmed-1","status":"ABORTED"}`)
		default:
			http.Error(w, "unexpected: "+r.Method+" "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	args := defaultCIArgs()
	args.controllerURL, args.projectID, args.scriptID = srv.URL, "project-ci", "script-1"
	args.authMode, args.apiKey = "api-key", "secret-key"
	args.publishCheck, args.requireSLA, args.requireResults = false, false, false
	args.browserPolicy = &runcontract.AnalysisPolicySelection{ID: "vitals", Revision: 2}
	args.journeyPolicy = &runcontract.AnalysisPolicySelection{ID: "checkout", Revision: 1}

	res, runErr := executeControllerCI(context.Background(), args, nil)
	if runErr == nil || !strings.Contains(runErr.Error(), "vitals@2") || res.Outcome != ciOutcomeConfigurationError {
		t.Fatalf("expected configuration error on unconfirmed analysis_policy_selections, got res=%+v err=%v", res, runErr)
	}
	if abportedID != "ci-unconfirmed-1" {
		t.Fatalf("expected unconfirmed CI run to be aborted, abortedID=%q", abportedID)
	}
	keyRev2 := lastKey

	echo = true
	res, runErr = executeControllerCI(context.Background(), args, nil)
	if runErr != nil || res.Outcome != ciOutcomePassed || res.AnalysisPolicySelections == nil ||
		res.AnalysisPolicySelections.Browser == nil || res.AnalysisPolicySelections.Browser.Revision != 2 ||
		res.AnalysisPolicySelections.Journey == nil || res.AnalysisPolicySelections.Journey.Revision != 1 {
		t.Fatalf("expected confirmed CI run to pass and echo AnalysisPolicySelections, got res=%+v err=%v", res, runErr)
	}

	args.browserPolicy = &runcontract.AnalysisPolicySelection{ID: "vitals", Revision: 3}
	if _, err := executeControllerCI(context.Background(), args, nil); err != nil {
		t.Fatalf("executeControllerCI rev 3: %v", err)
	}
	if lastKey == keyRev2 {
		t.Fatalf("expected Idempotency-Key to change when browserPolicy revision changed, got same key %q", lastKey)
	}
}

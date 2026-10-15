package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
)

func TestValidateScriptForReplayBlocksNativeBrunoReviewMetadata(t *testing.T) {
	for _, metadata := range []map[string]any{
		{"source_profile": "bruno_native", "review_required": true},
		{"source_profile": "bruno_native", "promotion_blocker": true},
		{"source_profile": "bruno_native", "review_gate": "bruno_javascript"},
		{"review_required": true, "promotion_blocker": true, "review_gate": "bruno_javascript"},
		{"review_gate": "bruno_encode_url_false_review_required"},
		{"review_gate": "bruno_auth"},
	} {
		t.Run(firstMetadataKey(metadata), func(t *testing.T) {
			script := &dsl.Script{
				Name:     "Review-gated HTTP",
				Protocol: "http",
				Config:   dsl.ScriptConfig{BaseURL: "https://example.test"},
				Actions: []dsl.Action{{
					Name:     "Imported request",
					Type:     "GET",
					Target:   "/orders",
					Metadata: metadata,
				}},
			}

			issues := validateScriptForReplay(script)
			if !containsIssueText(issues, "review-required action blocks replay until review metadata is removed") {
				t.Fatalf("issues = %v, want protocol-neutral review gate", issues)
			}

			// Browser policy is not an escape hatch for a non-browser draft.
			script.Config.Browser.ReviewPolicy.AllowReviewRequired = true
			if issues = validateScriptForReplay(script); !containsIssueText(issues, "review-required action blocks replay") {
				t.Fatalf("issues with browser policy = %v, want non-browser gate to remain", issues)
			}
		})
	}
}

func TestValidateScriptForReplayFindsNestedNativeBrunoReviewGate(t *testing.T) {
	script := &dsl.Script{
		Name:     "Nested review gate",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{
			Name: "Loop",
			Type: "control_loop",
			Control: &dsl.ControlFlow{
				Count: 1,
				Steps: []dsl.Action{{
					Name:     "Imported request",
					Type:     "GET",
					Target:   "/orders",
					Metadata: map[string]any{"source_profile": "bruno_native", "promotion_blocker": "true"},
				}},
			},
		}},
	}

	issues := replayReviewGateIssues(script)
	if !containsIssueText(issues, `actions[0].control.steps[0] "Imported request"`) {
		t.Fatalf("issues = %v, want nested action path", issues)
	}
}

func TestReplayReviewGateFindsPluralOnlyBlockerOnHTTPSequenceChild(t *testing.T) {
	script := &dsl.Script{
		Name:     "Sequence child blocker",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{
			Name:   "Imported sequence",
			Type:   "http_sequence",
			Method: "SEQUENCE",
			Target: "/",
			Params: map[string]any{
				"requests": []any{map[string]any{
					"name":   "Unsupported source behavior",
					"type":   "request",
					"method": "GET",
					"target": "/orders",
					"metadata": map[string]any{
						"execution_blockers": []any{
							"import_missing_variables",
							"import_review_required",
						},
					},
				}},
			},
		}},
	}

	issues := replayReviewGateIssues(script)
	if !containsIssueText(issues, `actions[0].params.requests[0] "Unsupported source behavior"`) ||
		!containsIssueText(issues, "import_missing_variables, import_review_required") {
		t.Fatalf("issues = %v, want deterministic plural sequence-child blockers", issues)
	}
}

func TestStoredReplayGateFindsNativeBrunoGateOnHTTPSequenceChild(t *testing.T) {
	script := &dsl.Script{
		Name:     "Bruno sequence child",
		Protocol: "browser",
		Actions: []dsl.Action{{
			Name: "Sequence",
			Type: "http_sequence",
			Params: map[string]any{
				"requests": []map[string]any{{
					"name": "Imported request",
					"type": "request",
					"metadata": map[string]any{
						"review_gate": "bruno_javascript",
					},
				}},
			},
		}},
	}
	err := storedScriptReplayGateError(script)
	if err == nil || !strings.Contains(err.Error(), "review-required action blocks replay") {
		t.Fatalf("stored replay gate error = %v, want sequence-child Bruno gate", err)
	}
}

func TestValidateScriptForReplayPreservesOtherProtocolApprovalFlows(t *testing.T) {
	script := &dsl.Script{
		Name:     "Approved Java import",
		Protocol: "java_vuser",
		Actions: []dsl.Action{{
			Name:   "Run Java action",
			Type:   "java_vuser_action",
			Target: "Action",
			Metadata: map[string]any{
				"source_profile":    "enterprise_archive",
				"review_required":   true,
				"promotion_blocker": true,
			},
		}},
	}
	if issues := replayReviewGateIssues(script); len(issues) != 0 {
		t.Fatalf("non-Bruno approval metadata was treated as an immutable replay gate: %v", issues)
	}
}

func TestCreateTestBlocksNativeBrunoReviewMetadata(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	runner.StoreScript(&dsl.Script{
		ID:       "review-gated-http",
		Name:     "Review-gated HTTP",
		Protocol: "http",
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{
			Name:     "Imported request",
			Type:     "GET",
			Target:   "/orders",
			Metadata: map[string]any{"source_profile": "bruno_native", "review_required": true, "promotion_blocker": true},
		}},
	})

	_, err := runner.CreateTest(CreateTestRequest{
		Name:     "Blocked run",
		Duration: "30s",
		ScriptID: "review-gated-http",
		VUsers:   1,
	})
	if err == nil || !strings.Contains(err.Error(), "review-required action blocks launch until review metadata is removed") {
		t.Fatalf("CreateTest error = %v, want launch review gate", err)
	}
}

func TestStartGateRechecksStoredRuntimeScriptReviewMetadata(t *testing.T) {
	script := &dsl.Script{
		Name:     "Restored review-gated HTTP",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{
			Name:     "Imported request",
			Type:     "GET",
			Target:   "/orders",
			Metadata: map[string]any{"source_profile": "bruno_native", "promotion_blocker": true},
		}},
	}
	runner := NewTestRunner(1, NewHub(), nil)
	test := &ManagedTest{
		ID:         "restored-review-gated-test",
		Status:     "CREATED",
		groupSpecs: []engine.GroupConfig{{Name: "Imported", Script: script}},
	}

	runner.mu.Lock()
	blocked, err := runner.evaluateStartGatesLocked(test.ID, test, StartTestOptions{})
	runner.mu.Unlock()
	if blocked {
		t.Fatal("blocked = true, want a hard review-gate error")
	}
	if err == nil || !strings.Contains(err.Error(), "review-required action blocks launch until review metadata is removed") {
		t.Fatalf("start gate error = %v, want launch review gate", err)
	}
}

func TestBrowserReviewPolicyBehaviorRemainsOptIn(t *testing.T) {
	script := &dsl.Script{
		Name:     "Browser review gate",
		Protocol: "browser",
		Actions: []dsl.Action{{
			Name:   "Reviewed JavaScript",
			Type:   "browser_evaluate_js",
			Target: "window.ready",
			Metadata: map[string]any{
				"review_required": true,
				"review_gate":     "javascript",
			},
		}},
	}

	if issues := replayReviewGateIssues(script); !containsIssueText(issues, "allow_reviewed_javascript=true before replay") {
		t.Fatalf("issues = %v, want existing reviewed-JavaScript policy gate", issues)
	}
	script.Config.Browser.ReviewPolicy.AllowReviewedJavaScript = true
	if issues := replayReviewGateIssues(script); len(issues) != 0 {
		t.Fatalf("approved browser issues = %v, want none", issues)
	}
	script.Config.Browser.ReviewPolicy.AllowReviewedJavaScript = false
	if err := storedScriptReplayGateError(script); err != nil {
		t.Fatalf("stored browser gate error = %v, want request-time policy override to remain available", err)
	}
}

func TestStoredBrowserExecutionBlockerCannotBeReplacedAtReplay(t *testing.T) {
	script := &dsl.Script{
		Name:     "Blocked browser import",
		Protocol: "browser",
		Actions: []dsl.Action{{
			Name:   "Imported navigation",
			Type:   "browser_navigate",
			Target: "https://example.test",
			Metadata: map[string]any{
				"execution_blocker": "import_review_required",
			},
		}},
	}

	err := storedScriptReplayGateError(script)
	if err == nil ||
		!strings.Contains(err.Error(), "execution blocker import_review_required blocks replay") {
		t.Fatalf("stored browser gate error = %v, want immutable generic execution blocker", err)
	}
}

func TestBrowserPolicyCannotOverrideNativeBrunoReviewGate(t *testing.T) {
	script := &dsl.Script{
		Name:     "Protocol-converted Bruno draft",
		Protocol: "browser",
		Config: dsl.ScriptConfig{Browser: dsl.BrowserConfig{
			ReviewPolicy: dsl.BrowserReviewPolicy{
				AllowReviewedJavaScript: true,
				AllowReviewRequired:     true,
			},
		}},
		Actions: []dsl.Action{{
			Name:   "Navigate",
			Type:   "browser_navigate",
			Target: "https://example.test",
			Metadata: map[string]any{
				"review_gate": "bruno_javascript",
			},
		}},
	}

	if issues := replayReviewGateIssues(script); !containsIssueText(issues, "review-required action blocks replay until review metadata is removed") {
		t.Fatalf("browser replay issues = %v, want immutable Bruno gate", issues)
	}
	if err := storedScriptReplayGateError(script); err == nil ||
		!strings.Contains(err.Error(), "review-required action blocks replay") {
		t.Fatalf("stored browser gate error = %v, want immutable Bruno gate", err)
	}
}

func TestBrunoMissingVariableExecutionBlockerBlocksReplayAndLaunch(t *testing.T) {
	script := &dsl.Script{
		Protocol: "http",
		Actions: []dsl.Action{{
			Name: "Imported request",
			Type: "http_request",
			Metadata: map[string]any{
				"execution_blocker": "bruno_missing_variables",
				"missing_variables": []any{"tenant", "request_id"},
			},
		}},
	}
	for operation, issues := range map[string][]string{
		"replay": replayReviewGateIssues(script),
		"launch": launchReviewGateIssues(script),
	} {
		if len(issues) != 1 ||
			!strings.Contains(issues[0], "bruno_missing_variables (tenant, request_id)") ||
			!strings.Contains(issues[0], "blocks "+operation) {
			t.Fatalf("%s issues = %#v", operation, issues)
		}
	}
}

func TestNativeTransactionValueExecutionBlockerBlocksReplayAndLaunch(t *testing.T) {
	const blocker = "source_transaction_native_value_unsupported"
	script := &dsl.Script{
		ID:       "native-transaction-value-gate",
		Name:     "Native transaction getter review",
		Version:  "1.0.0",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{
			Name: "lr_get_transaction_duration",
			Type: "source_review",
			Params: map[string]any{
				"function":     "lr_get_transaction_duration",
				"support_mode": "review_only",
			},
			Metadata: map[string]any{
				"execution_blocker":  blocker,
				"execution_blockers": []string{blocker},
				"source_file":        "Action.c",
				"source_line":        3,
				"source_call":        `lr_get_transaction_duration("Checkout");`,
			},
		}},
	}

	for operation, issues := range map[string][]string{
		"replay": replayReviewGateIssues(script),
		"launch": launchReviewGateIssues(script),
	} {
		if len(issues) != 1 ||
			!strings.Contains(issues[0], blocker) ||
			!strings.Contains(issues[0], "blocks "+operation) {
			t.Fatalf("%s issues = %#v, want transaction-value execution blocker", operation, issues)
		}
	}

	runner := NewTestRunner(1, NewHub(), nil)
	if _, err := runner.ReplayScriptDefinitionContext(context.Background(), script, engine.ReplayOptions{}); err == nil ||
		!strings.Contains(err.Error(), blocker) {
		t.Fatalf("ReplayScriptDefinitionContext error = %v, want transaction-value blocker", err)
	}
	runner.StoreScript(script)
	if _, err := runner.CreateTest(CreateTestRequest{
		Name:     "Blocked transaction getter",
		Duration: "30s",
		ScriptID: script.ID,
		VUsers:   1,
	}); err == nil || !strings.Contains(err.Error(), blocker) {
		t.Fatalf("CreateTest error = %v, want transaction-value blocker", err)
	}
}

func TestBrunoReviewGateBlocksAuthoringHealthButKeepsDraftSaveable(t *testing.T) {
	script := brunoReviewGatedHTTPScript("health-review-gate")

	validation := buildScriptValidationResponse(script.ID, script)
	if !validation.Valid || len(validation.Issues) != 0 {
		t.Fatalf("validation = valid %v issues %v, want structurally saveable draft", validation.Valid, validation.Issues)
	}
	if validation.Preflight == nil || validation.Preflight.Valid {
		t.Fatalf("preflight = %#v, want replay blocked", validation.Preflight)
	}
	if validation.AuthoringHealth.Status != "blocked" {
		t.Fatalf("authoring health = %#v, want blocked", validation.AuthoringHealth)
	}
	readiness := authoringHealthItem(t, validation.AuthoringHealth, "replay_readiness")
	if readiness.Status != "blocker" || readiness.Count != 1 {
		t.Fatalf("replay readiness = %#v, want one blocker", readiness)
	}
	if validation.AuthoringHealth.Inputs[0].Status != "pass" {
		t.Fatalf("validation input = %#v, want save validation to remain passed", validation.AuthoringHealth.Inputs[0])
	}
}

func TestReplayScriptDefinitionBlocksBrunoReviewGateAtLowLevel(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	script := brunoReviewGatedHTTPScript("low-level-review-gate")

	if _, err := runner.ReplayScriptDefinitionContext(context.Background(), script, engine.ReplayOptions{}); err == nil ||
		!strings.Contains(err.Error(), "review-required action blocks replay until review metadata is removed") {
		t.Fatalf("ReplayScriptDefinitionContext error = %v, want review gate", err)
	}

	runner.StoreScript(script)
	if _, err := runner.ReplayScript(script.ID, engine.ReplayOptions{}); err == nil ||
		!strings.Contains(err.Error(), "review-required action blocks replay until review metadata is removed") {
		t.Fatalf("ReplayScript error = %v, want stored review gate", err)
	}

	debugReleased := false
	debug := NewScriptDebugManager(NewHub())
	if _, err := debug.StartChecked(script.ID, script, ScriptDebugStartRequest{
		releaseExecutionContext: func() { debugReleased = true },
	}); err == nil || !strings.Contains(err.Error(), "review-required action blocks replay") {
		t.Fatalf("ScriptDebugManager.StartChecked error = %v, want review gate", err)
	}
	if !debugReleased {
		t.Fatal("script-debug execution context was not released after review gate")
	}

	browserReleased := false
	browser := NewBrowserReplayManager(NewHub())
	if _, err := browser.Start(script.ID, script, BrowserReplayStartRequest{
		releaseExecutionContext: func() { browserReleased = true },
	}); err == nil || !strings.Contains(err.Error(), "review-required action blocks replay") {
		t.Fatalf("BrowserReplayManager.Start error = %v, want review gate", err)
	}
	if !browserReleased {
		t.Fatal("browser-replay execution context was not released after review gate")
	}
}

func TestStoredReplayEndpointsCannotReplaceReviewGatedScript(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	stored := runner.StoreScript(brunoReviewGatedHTTPScript("stored-review-gate"))
	server := &Server{runner: runner}

	replacement := plainHTTPScript("")
	body, err := json.Marshal(map[string]any{"script": replacement})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		path   string
		handle func(http.ResponseWriter, *http.Request)
	}{
		{name: "classic replay", path: "/api/v1/scripts/" + stored.ID + "/replay", handle: server.handleReplayScript},
		{name: "script debug", path: "/api/v1/scripts/" + stored.ID + "/debug/sessions", handle: server.handleStartScriptDebugScript},
		{name: "replay session", path: "/api/v1/scripts/" + stored.ID + "/replay/sessions", handle: server.handleStartReplaySessionScript},
		{name: "browser replay", path: "/api/v1/scripts/" + stored.ID + "/browser-replay/sessions", handle: server.handleStartBrowserReplayScript},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, test.path, bytes.NewReader(body))
			req.SetPathValue("id", stored.ID)
			resp := httptest.NewRecorder()

			test.handle(resp, req)

			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body=%s, want 400", resp.Code, resp.Body.String())
			}
			if !strings.Contains(resp.Body.String(), "review-required action blocks replay until review metadata is removed") {
				t.Fatalf("body = %s, want stored-script review gate", resp.Body.String())
			}
		})
	}
}

func TestScenarioMaterializationRechecksBrunoReviewGate(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	runner.StoreScript(plainHTTPScript("scenario-review-gate"))
	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		Name:     "Previously ready scenario",
		Duration: "30s",
		Groups:   []CreateGroupEntry{{Name: "Imported", ScriptID: "scenario-review-gate", VUsers: 1}},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}

	runner.StoreScript(brunoReviewGatedHTTPScript("scenario-review-gate"))
	if _, err := runner.CreateTest(CreateTestRequest{ScenarioID: scenario.ID}); err == nil ||
		!strings.Contains(err.Error(), "review-required action blocks launch until review metadata is removed") {
		t.Fatalf("scenario CreateTest error = %v, want launch review gate", err)
	}
}

func TestScheduledStartBlocksBrunoReviewGateBeforeDistributedDispatch(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	runner.StoreScript(plainHTTPScript("scheduled-review-gate"))
	start := time.Now().Add(time.Hour)
	test, err := runner.CreateTest(CreateTestRequest{
		Name:             "Scheduled import",
		Duration:         "30s",
		ScriptID:         "scheduled-review-gate",
		VUsers:           1,
		ScheduledStartAt: &start,
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	if test.Status != "SCHEDULED" {
		t.Fatalf("status = %q, want SCHEDULED", test.Status)
	}

	dialed := make(chan struct{}, 1)
	runner.setExecutionBackend(newDistributedExecutionBackend(
		[]string{"generator.example:9090"},
		time.Second,
		func(context.Context, string) (generatorClient, error) {
			dialed <- struct{}{}
			return nil, context.Canceled
		},
	))
	runner.mu.Lock()
	runtimeScript := runner.tests[test.ID].groupSpecs[0].Script
	runtimeScript.Actions[0].Metadata = map[string]any{
		"source_profile":    "bruno_native",
		"review_required":   true,
		"promotion_blocker": true,
		"review_gate":       "bruno_javascript",
	}
	runner.mu.Unlock()

	err = runner.StartTestWithOptions(test.ID, StartTestOptions{SkipLaunchWindowPreflight: true})
	if err == nil || !strings.Contains(err.Error(), "review-required action blocks launch until review metadata is removed") {
		t.Fatalf("StartTest error = %v, want launch review gate", err)
	}
	select {
	case <-dialed:
		t.Fatal("distributed generator was contacted before the review gate")
	default:
	}
}

func TestConsumeBrunoImportSessionRejectsExpiredProtectedPayload(t *testing.T) {
	server, _, _ := newBrunoImportAPITestServer(t)
	inspection := inspectBrunoJSON(t, server, []byte(`info: {name: Expiring request, type: http}
http:
  method: GET
  url: https://example.test/orders
`), "")
	claim := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/bruno/commit", nil)
	session, err := server.consumeBrunoImportSession(claim, inspection.ImportSessionID, brunoImportTestProject)
	if err != nil {
		t.Fatalf("consume fresh session: %v", err)
	}
	defer clearBrunoImportSession(session)

	session.ExpiresAt = time.Now().UTC().Add(-time.Minute)
	if err := server.persistBrunoImportSession(context.Background(), session); err != nil {
		t.Fatalf("persist expired protected session: %v", err)
	}

	retry := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/bruno/commit", nil)
	if _, err := server.consumeBrunoImportSession(retry, inspection.ImportSessionID, brunoImportTestProject); err == nil ||
		!strings.Contains(err.Error(), "Bruno import session expired") {
		t.Fatalf("consume expired session error = %v, want explicit expiry rejection", err)
	}
}

func plainHTTPScript(id string) *dsl.Script {
	return &dsl.Script{
		ID:       id,
		Name:     "Imported HTTP request",
		Protocol: "http",
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{
			Name:   "Imported request",
			Type:   "GET",
			Target: "/orders",
		}},
	}
}

func brunoReviewGatedHTTPScript(id string) *dsl.Script {
	script := plainHTTPScript(id)
	script.Actions[0].Metadata = map[string]any{
		"source_profile":    "bruno_native",
		"review_required":   true,
		"promotion_blocker": true,
		"review_gate":       "bruno_javascript",
	}
	return script
}

func containsIssueText(issues []string, text string) bool {
	for _, issue := range issues {
		if strings.Contains(issue, text) {
			return true
		}
	}
	return false
}

func firstMetadataKey(metadata map[string]any) string {
	for key := range metadata {
		if key == "source_profile" {
			continue
		}
		return key
	}
	return "metadata"
}

// A request an import held because HTTP cannot send a header names that
// header, so the editor and the debugger can say which one it was. A marker
// from before headers were named names nothing.
func TestImportReviewBlockerNamesTheHeadersLeftOut(t *testing.T) {
	script := &dsl.Script{
		Protocol: "http",
		Actions: []dsl.Action{{
			Name: "Imported request",
			Type: "http_request",
			Metadata: map[string]any{
				"execution_blockers": []any{"import_review_required", "legacy_tls_replacement"},
				dsl.ImportHeaderFidelityMetadataKey: map[string]any{
					"version": 1,
					"omitted_occurrences": []any{
						map[string]any{"source_index": 0, "name": "X-Bad", "reason_code": "http_header_invalid_value"},
						map[string]any{"source_index": 1, "name": "<header>", "reason_code": "http_header_invalid_value"},
						map[string]any{"source_index": 2, "name": `"X Pasted\r\nHeader"`, "reason_code": "http_header_invalid_header_name"},
					},
				},
			},
		}},
	}
	issues := replayReviewGateIssues(script)
	want := `actions[0] "Imported request": execution blocker import_review_required (2 headers left out: X-Bad, "X Pasted\r\nHeader"), legacy_tls_replacement blocks replay until the imported behavior is resolved`
	if len(issues) != 1 || issues[0] != want {
		t.Fatalf("issues = %q, want %q", issues, want)
	}

	// When the same hold also covers an HTTP version, the headers are not
	// presented as the only thing to fix.
	script.Actions[0].Metadata["execution_blockers"] = []any{"import_review_required"}
	script.Actions[0].Metadata["import_review"] = map[string]any{"reason_codes": []any{"http_header_invalid_value", "http_version_not_preserved"}}
	issues = replayReviewGateIssues(script)
	if len(issues) != 1 || !strings.Contains(issues[0], `import_review_required (other review and 2 headers left out: X-Bad, "X Pasted\r\nHeader")`) {
		t.Fatalf("issues = %q, want the other review named", issues)
	}
}

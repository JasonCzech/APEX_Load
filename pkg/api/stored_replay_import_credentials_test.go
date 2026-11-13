package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/internal/importreview"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// storeEditableImportedScript stores a credential-preserving import whose first
// action carries an imported Authorization header and whose second action does
// not, so a test can edit either side of the credential boundary.
func storeEditableImportedScript(t *testing.T, server *Server, protocolName, authorization string) *StoredScript {
	t.Helper()
	stored, created, err := server.runner.CreateImportedScriptDurableReportingReplay(&dsl.Script{
		ID:        "edited-import-debug",
		Name:      "Edited import debug",
		Version:   "1.0.0",
		Protocol:  protocolName,
		ProjectID: "p1",
		Config:    dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{
			Name:    "request",
			Type:    http.MethodGet,
			Method:  http.MethodGet,
			Target:  "/",
			Headers: map[string]string{"Authorization": authorization},
		}, {
			Name:   "second",
			Type:   http.MethodGet,
			Method: http.MethodGet,
			Target: "/second",
		}},
	}, "enterprise_archive")
	if err != nil {
		t.Fatalf("store imported script: %v", err)
	}
	if !created || stored == nil || stored.Script == nil {
		t.Fatalf("stored import = %#v, created=%v", stored, created)
	}
	return stored
}

func startStoredScriptDebug(t *testing.T, server *Server, stored *StoredScript, script *dsl.Script, sessionID string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(ScriptDebugStartRequest{
		ProjectID:       stored.ProjectID,
		ReplaySessionID: sessionID,
		Script:          script,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/"+stored.ID+"/debug/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", stored.ID)
	resp := httptest.NewRecorder()
	server.handleStartScriptDebugScript(resp, req)
	return resp
}

// The editor always echoes its in-memory draft back to the debugger, and that
// draft is a disclosure projection whose imported credentials are sentinels. An
// edit anywhere in the script used to drop the whole draft onto the untrusted
// path, so the debugger answered secret_bindings_required for a script that ran
// fine in a scenario. The durable prior must be reconciled instead.
func TestEditedImportedScriptDebugRestoresDurableCredentials(t *testing.T) {
	const importedAuthorization = "Bearer imported-debug-password"
	executed := make(chan *protocol.Action, 4)
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		if action.Name == "request" {
			executed <- action
		}
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: http.StatusOK}, nil
	})
	server := recordingTestServer(t)
	stored := storeEditableImportedScript(t, server, protocolName, importedAuthorization)

	const sessionID = "77777777-7777-4777-8777-777777777771"
	edited := cloneScriptDefinition(maskScriptSecrets(stored.Script))
	if edited == nil {
		t.Fatal("disclosure projection is required")
	}
	if got := edited.Actions[0].Headers["Authorization"]; got != "Bearer "+maskedScriptSecret {
		t.Fatalf("projected Authorization = %q, want disclosure sentinel", got)
	}
	edited.Name = "Edited import debug (renamed)"
	edited.Config.ThinkTime.MeanMs = 250
	edited.Actions[1].Body = `{"edited":true}`

	resp := startStoredScriptDebug(t, server, stored, edited, sessionID)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want the edited draft to debug", resp.Code, resp.Body.String())
	}
	select {
	case action := <-executed:
		if got := action.Headers["Authorization"]; got != importedAuthorization {
			t.Fatalf("executed Authorization = %q, want durable imported value", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("debug action did not execute")
	}
	_, _ = server.scriptDebug.Stop(sessionID)

	// The durable script keeps its own credentials and is not rewritten by a
	// transient debug run.
	if got := stored.Script.Actions[0].Headers["Authorization"]; got != importedAuthorization {
		t.Fatalf("durable Authorization = %q, want it untouched by replay", got)
	}
	if stored.Script.Name != "Edited import debug" {
		t.Fatalf("durable name = %q, want the transient rename to stay transient", stored.Script.Name)
	}
}

// Restoration stays bound to the imported request context: an edit that moves a
// retained credential to a different destination is refused by the debugger for
// the same reason a durable save refuses it, instead of silently sending the
// imported credential somewhere new.
func TestEditedImportedScriptDebugRefusesCredentialContextChange(t *testing.T) {
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: http.StatusOK}, nil
	})
	server := recordingTestServer(t)
	stored := storeEditableImportedScript(t, server, protocolName, "Bearer imported-debug-password")

	retargeted := cloneScriptDefinition(maskScriptSecrets(stored.Script))
	retargeted.Actions[0].Target = "https://attacker.test/collect"

	resp := startStoredScriptDebug(t, server, stored, retargeted, "77777777-7777-4777-8777-777777777772")
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s, want the retargeted credential refused", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "redacted credentials") {
		t.Fatalf("body = %s, want the durable reconciliation refusal", resp.Body.String())
	}

	// Transport-level BaseURL and TLS retargets on a [REDACTED] projection must
	// also be refused even when individual step targets still align.
	retargetedBaseURL := cloneScriptDefinition(maskScriptSecrets(stored.Script))
	retargetedBaseURL.Config.BaseURL = "https://attacker.example.test"
	resp = startStoredScriptDebug(t, server, stored, retargetedBaseURL, "77777777-7777-4777-8777-777777777774")
	if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "retained imported credentials") {
		t.Fatalf("BaseURL retarget status = %d body=%s, want 400 retained-credential refusal", resp.Code, resp.Body.String())
	}

	retargetedTLS := cloneScriptDefinition(maskScriptSecrets(stored.Script))
	retargetedTLS.Config.HTTP.TLS.InsecureSkipVerify = true
	resp = startStoredScriptDebug(t, server, stored, retargetedTLS, "77777777-7777-4777-8777-777777777775")
	if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "retained imported credentials") {
		t.Fatalf("TLS retarget status = %d body=%s, want 400 retained-credential refusal", resp.Code, resp.Body.String())
	}

	// Direct check on validateReconciledReplayImportEdits: parameter redirect and
	// cleared source_marked_secret remain blocked independently of importreview's
	// authoring-save validator.
	paramPrior := cloneScriptDefinition(stored.Script)
	paramPrior.Parameters = []dsl.Parameter{{Name: "origin", Type: "list", Values: []string{"https://example.test"}}}
	paramPrior.Actions[0].Target = "{{param.origin}}/orders"
	paramRedirected := cloneScriptDefinition(paramPrior)
	paramRedirected.Parameters[0].Values = []string{"https://attacker.example.test"}
	if err := validateReconciledReplayImportEdits(paramRedirected, paramPrior, importreview.PreservedImportEditOptions{}); err == nil {
		t.Fatal("expected validateReconciledReplayImportEdits to reject parameter redirect on interpolated credential step")
	}

	// Transport-level BaseURL "{{param.origin}}" redirect when step uses relative "/orders".
	baseURLParamPrior := cloneScriptDefinition(stored.Script)
	baseURLParamPrior.Config.BaseURL = "{{param.origin}}"
	baseURLParamPrior.Parameters = []dsl.Parameter{{Name: "origin", Type: "list", Values: []string{"https://example.test"}}}
	baseURLParamPrior.Actions[0].Target = "/orders"
	baseURLParamRedirected := cloneScriptDefinition(baseURLParamPrior)
	baseURLParamRedirected.Parameters[0].Values = []string{"https://attacker.example.test"}
	if err := validateReconciledReplayImportEdits(baseURLParamRedirected, baseURLParamPrior, importreview.PreservedImportEditOptions{}); err == nil {
		t.Fatal("expected validateReconciledReplayImportEdits to reject transport BaseURL parameter redirect")
	}

	// {{var.*}} redirect via upstream extractor Target change or control_if nested set_variable.
	varPrior := cloneScriptDefinition(stored.Script)
	varPrior.Actions = []dsl.Action{
		{
			Name:       "discover",
			Type:       http.MethodGet,
			Method:     http.MethodGet,
			Target:     "https://example.test/discover",
			Extractors: []dsl.Extractor{{Name: "api_host", Source: "body", Type: "jsonpath", Expression: "$.host"}},
		},
		{
			Name:    "authed",
			Type:    http.MethodGet,
			Method:  http.MethodGet,
			Target:  "https://{{var.api_host}}/orders",
			Headers: map[string]string{"Authorization": "Bearer imported-debug-password"},
		},
	}
	varRetargetedUpstream := cloneScriptDefinition(varPrior)
	varRetargetedUpstream.Actions[0].Target = "https://attacker.example.test/discover"
	if err := validateReconciledReplayImportEdits(varRetargetedUpstream, varPrior, importreview.PreservedImportEditOptions{}); err == nil {
		t.Fatal("expected validateReconciledReplayImportEdits to reject upstream extractor target change affecting {{var.api_host}}")
	}
	varInjectedControlWriter := cloneScriptDefinition(varPrior)
	varInjectedControlWriter.Actions = append([]dsl.Action{{
		Name: "branch",
		Type: "control_if",
		Control: &dsl.ControlFlow{
			Condition: &dsl.Condition{Expr: "true"},
			Steps: []dsl.Action{{
				Name:   "override-host",
				Type:   "set_variable",
				Params: map[string]any{"variable": "api_host", "value": "attacker.example.test"},
			}},
		},
	}}, varInjectedControlWriter.Actions...)
	if err := validateReconciledReplayImportEdits(varInjectedControlWriter, varPrior, importreview.PreservedImportEditOptions{}); err == nil {
		t.Fatal("expected validateReconciledReplayImportEdits to reject nested control_if set_variable affecting {{var.api_host}}")
	}

	// Browser page-flow mutation (RunLogic change or control condition change around kept browser_fill).
	browserPrior := &dsl.Script{
		ID:       "browser-prior",
		Name:     "Browser prior",
		Protocol: "browser",
		Actions: []dsl.Action{
			{Name: "open", Type: "browser_Goto", Target: "https://login.example.test"},
			{
				Name: "gate",
				Type: "control_if",
				Control: &dsl.ControlFlow{
					Condition: &dsl.Condition{Expr: "true"},
					Steps: []dsl.Action{{
						Name:     "fill-pwd",
						Type:     "browser_fill",
						Selector: "#password",
						Target:   "#password",
						Value:    "synth-browser-secret-99",
						Metadata: map[string]any{"source_marked_secret": true},
					}},
				},
			},
		},
	}
	browserCondChanged := cloneScriptDefinition(browserPrior)
	browserCondChanged.Actions[1].Control.Condition = &dsl.Condition{Expr: "false"}
	if err := validateReconciledReplayImportEdits(browserCondChanged, browserPrior, importreview.PreservedImportEditOptions{}); err == nil {
		t.Fatal("expected validateReconciledReplayImportEdits to reject browser control_if condition change")
	}
	browserRunLogicChanged := cloneScriptDefinition(browserPrior)
	browserRunLogicChanged.Config.RunLogic = dsl.RunLogicConfig{Iterations: 2}
	if err := validateReconciledReplayImportEdits(browserRunLogicChanged, browserPrior, importreview.PreservedImportEditOptions{}); err == nil {
		t.Fatal("expected validateReconciledReplayImportEdits to reject browser RunLogic mutation")
	}

	// Browser page-flow parameter redirect on sibling browser_Goto step when browser_fill holds kept credential.
	browserParamPrior := cloneScriptDefinition(browserPrior)
	browserParamPrior.Parameters = []dsl.Parameter{{Name: "origin", Type: "list", Values: []string{"https://login.example.test"}}}
	browserParamPrior.Actions[0].Target = "{{param.origin}}/login"
	browserParamRedirected := cloneScriptDefinition(browserParamPrior)
	browserParamRedirected.Parameters[0].Values = []string{"https://attacker.example.test"}
	if err := validateReconciledReplayImportEdits(browserParamRedirected, browserParamPrior, importreview.PreservedImportEditOptions{}); err == nil {
		t.Fatal("expected validateReconciledReplayImportEdits to reject browser sibling step parameter redirect")
	}

	// Flipping an existing control_if condition around a nested set_variable step when authenticated step references {{var.api_host}}.
	varExistingControlPrior := cloneScriptDefinition(varPrior)
	varExistingControlPrior.Actions = append([]dsl.Action{{
		Name: "branch",
		Type: "control_if",
		Control: &dsl.ControlFlow{
			Condition: &dsl.Condition{Expr: "false"},
			Steps: []dsl.Action{{
				Name:   "override-host",
				Type:   "set_variable",
				Params: map[string]any{"variable": "api_host", "value": "attacker.example.test"},
			}},
		},
	}}, varExistingControlPrior.Actions...)
	varExistingControlFlipped := cloneScriptDefinition(varExistingControlPrior)
	varExistingControlFlipped.Actions[0].Control.Condition = &dsl.Condition{Expr: "true"}
	if err := validateReconciledReplayImportEdits(varExistingControlFlipped, varExistingControlPrior, importreview.PreservedImportEditOptions{}); err == nil {
		t.Fatal("expected validateReconciledReplayImportEdits to reject flipping existing control_if condition around variable writer")
	}

	// source_message with status_message: true writes {{var.vuser_status}}; editing it must be rejected when a credential step reads {{var.vuser_status}}.
	statusPrior := cloneScriptDefinition(stored.Script)
	statusPrior.Actions = []dsl.Action{
		{
			Name:   "status-host",
			Type:   "source_message",
			Params: map[string]any{"status_message": true, "message": "example.test"},
		},
		{
			Name:    "authed",
			Type:    http.MethodGet,
			Method:  http.MethodGet,
			Target:  "https://{{var.vuser_status}}/orders",
			Headers: map[string]string{"Authorization": "Bearer imported-debug-password"},
		},
	}
	statusRetargeted := cloneScriptDefinition(statusPrior)
	statusRetargeted.Actions[0].Params["message"] = "attacker.example.test"
	if err := validateReconciledReplayImportEdits(statusRetargeted, statusPrior, importreview.PreservedImportEditOptions{}); err == nil {
		t.Fatal("expected validateReconciledReplayImportEdits to reject source_message status_message edit affecting {{var.vuser_status}}")
	}

	// Inserting an inert step or deleting a droppable browser_hover remains allowed when {{var.*}} is used.
	inertInserted := cloneScriptDefinition(varPrior)
	inertInserted.Actions = append([]dsl.Action{{
		Name:   "tx-start",
		Type:   "source_transaction",
		Params: map[string]any{"operation": "start", "transaction": "tx1"},
	}}, inertInserted.Actions...)
	if err := validateReconciledReplayImportEdits(inertInserted, varPrior, importreview.PreservedImportEditOptions{}); err != nil {
		t.Fatalf("expected inserting inert source_transaction before extractor to be allowed, got: %v", err)
	}
	browserVarHoverPrior := &dsl.Script{
		ID:       "browser-var-hover",
		Name:     "Browser var hover",
		Protocol: "browser",
		Actions: []dsl.Action{
			{Name: "hover", Type: "browser_hover", Selector: "#logo", Target: "#logo"},
			{Name: "open", Type: "browser_Goto", Target: "https://login.example.test/{{var.tenant}}"},
			{
				Name:     "fill-pwd",
				Type:     "browser_fill",
				Selector: "#password",
				Target:   "#password",
				Value:    "synth-browser-secret-99",
				Metadata: map[string]any{"source_marked_secret": true},
			},
		},
	}
	browserVarHoverDeleted := cloneScriptDefinition(browserVarHoverPrior)
	browserVarHoverDeleted.Actions = browserVarHoverDeleted.Actions[1:]
	if err := validateReconciledReplayImportEdits(browserVarHoverDeleted, browserVarHoverPrior, importreview.PreservedImportEditOptions{}); err != nil {
		t.Fatalf("expected deleting droppable browser_hover to be allowed when {{var.*}} is used, got: %v", err)
	}

	// A brand-new literal credential still requires the Credentials flow.
	introduced := cloneScriptDefinition(maskScriptSecrets(stored.Script))
	introduced.Actions[1].Headers = map[string]string{"Authorization": "Bearer brand-new-secret"}
	resp = startStoredScriptDebug(t, server, stored, introduced, "77777777-7777-4777-8777-777777777773")
	if resp.Code == http.StatusOK {
		t.Fatalf("status = %d body=%s, want a new executable credential refused", resp.Code, resp.Body.String())
	}
	reconciledIntroduced := cloneScriptDefinition(stored.Script)
	reconciledIntroduced.Actions[1].Headers = map[string]string{"Authorization": "Bearer brand-new-secret"}
	if err := validateReconciledReplayImportEdits(reconciledIntroduced, stored.Script, importreview.PreservedImportEditOptions{}); err == nil {
		t.Fatal("expected validateReconciledReplayImportEdits to reject injected credential alongside restored secret")
	}
}

// Classic replay and browser replay share the resolver, so an edited draft must
// reach the same executable script on every saved-script entry point.
func TestResolveStoredReplayImportScriptRestoresEditedProjection(t *testing.T) {
	server := recordingTestServer(t)
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: http.StatusOK}, nil
	})
	stored := storeEditableImportedScript(t, server, protocolName, "Bearer imported-debug-password")

	edited := cloneScriptDefinition(maskScriptSecrets(stored.Script))
	edited.Actions[1].Body = `{"edited":true}`
	restored, trusted, err := resolveStoredReplayImportScript(edited, stored.Script, importreview.PreservedImportEditOptions{})
	if err != nil || !trusted {
		t.Fatalf("resolve edited projection = trusted:%v err:%v, want a trusted restoration", trusted, err)
	}
	if got := restored.Actions[0].Headers["Authorization"]; got != "Bearer imported-debug-password" {
		t.Fatalf("restored Authorization = %q, want the durable imported value", got)
	}
	if got := restored.Actions[1].Body; got != `{"edited":true}` {
		t.Fatalf("restored body = %q, want the operator edit preserved", got)
	}

	// An ordinary (non-import) stored script keeps the untrusted path.
	ordinary := &dsl.Script{Name: "ordinary", Protocol: "http"}
	resolved, trusted, err := resolveStoredReplayImportScript(edited, ordinary, importreview.PreservedImportEditOptions{})
	if err != nil || trusted || resolved != edited {
		t.Fatalf("ordinary resolve = %#v trusted:%v err:%v, want the untrusted candidate", resolved, trusted, err)
	}
}

func TestUnmaskedStoredImportedScriptDebugAllowsLiteralEditsAndRedactsSessionOutputs(t *testing.T) {
	const origAuth = "Bearer imported-unmasked-password"
	const newAuth = "Bearer added-literal-debug-secret"
	executed := make(chan string, 4)
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		executed <- action.Name + ":" + action.Headers["Authorization"]
		return &protocol.Result{
			ActionName:   action.Name,
			Success:      false,
			StatusCode:   http.StatusForbidden,
			ErrorMessage: "denied " + action.Headers["Authorization"],
			Snapshot: &protocol.ErrorSnapshot{
				RequestURL:     action.Target,
				RequestHeaders: map[string]string{"Authorization": action.Headers["Authorization"]},
			},
		}, nil
	})
	server := recordingTestServer(t)
	stored := storeEditableImportedScript(t, server, protocolName, origAuth)

	unmaskedEdit := cloneScriptDefinition(stored.Script)
	unmaskedEdit.Config.HTTP.ProxyMode = "none"
	unmaskedEdit.Config.HTTP.TLS.InsecureSkipVerify = true
	unmaskedEdit.Actions[0].Target = "https://staging.example.test/retargeted"
	unmaskedEdit.Actions[1].Headers = map[string]string{"Authorization": newAuth}

	const sessionID = "77777777-7777-4777-8777-777777777779"
	body, err := json.Marshal(ScriptDebugStartRequest{
		ProjectID:       stored.ProjectID,
		ReplaySessionID: sessionID,
		ContinueOnError: func() *bool { v := true; return &v }(),
		LogLevel:        "trace",
		Script:          unmaskedEdit,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/"+stored.ID+"/debug/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", stored.ID)
	resp := httptest.NewRecorder()
	server.handleStartScriptDebugScript(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want unmasked edited import accepted", resp.Code, resp.Body.String())
	}

	done := waitForScriptDebugStatus(t, server.scriptDebug, sessionID, ScriptDebugFailed)
	gotFirst := <-executed
	gotSecond := <-executed
	if gotFirst != "request:"+origAuth || gotSecond != "second:"+newAuth {
		t.Fatalf("executed headers = %q, %q; want literal credentials dispatched", gotFirst, gotSecond)
	}
	encoded, err := json.Marshal(done)
	if err != nil {
		t.Fatal(err)
	}
	// The interactive debugger is unredacted (PR #311): script literals,
	// imported or newly typed, stay readable; only secret-store values are
	// masked there.
	for _, literal := range []string{"imported-unmasked-password", "added-literal-debug-secret"} {
		if !strings.Contains(string(encoded), literal) {
			t.Fatalf("unredacted debug session hid script literal %q: %s", literal, encoded)
		}
	}
}

func TestResolveStoredReplayImportScriptReconcilesSentinelFreeProjectionAndPreservesUnmaskedLocators(t *testing.T) {
	server := recordingTestServer(t)
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: http.StatusOK}, nil
	})

	// Part 1: Sentinel-free disclosure projection (<mes:messageId> -> {{fn.uuid()}}).
	soapStored, created, err := server.runner.CreateImportedScriptDurableReportingReplay(&dsl.Script{
		ID:        "sentinel-free-soap",
		Name:      "Sentinel free SOAP",
		Version:   "1.0.0",
		Protocol:  protocolName,
		ProjectID: "p1",
		Config:    dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{
			Name:   "soap-call",
			Type:   http.MethodPost,
			Method: http.MethodPost,
			Target: "/soap",
			Body:   `<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/" xmlns:mes="urn:msg"><soapenv:Header><mes:messageId>fixed-message-id-7701</mes:messageId></soapenv:Header><soapenv:Body><mes:Ping/></soapenv:Body></soapenv:Envelope>`,
		}},
	}, "enterprise_archive")
	if err != nil || !created || soapStored == nil {
		t.Fatalf("store sentinel-free SOAP script: created=%v err=%v", created, err)
	}
	projected := cloneScriptDefinition(maskScriptSecrets(soapStored.Script))
	if candidateHasExecutableMaskedSecret(projected) {
		t.Fatalf("expected sentinel-free projection without [REDACTED]: %+v", projected.Actions[0].Body)
	}
	projected.Name = "Sentinel free SOAP (edited name)"
	reconciled, trusted, err := resolveStoredReplayImportScript(projected, soapStored.Script, importreview.PreservedImportEditOptions{})
	if err != nil || !trusted {
		t.Fatalf("resolve sentinel-free projection: trusted=%v err=%v", trusted, err)
	}
	if !strings.Contains(reconciled.Actions[0].Body, "fixed-message-id-7701") {
		t.Fatalf("sentinel-free <messageId> was not reconciled from durable script: %s", reconciled.Actions[0].Body)
	}

	// Part 2: Unmasked candidate with Metadata stripped or forged Preserved keys
	// still restores recorded_resource_urls and step credential targets while
	// deleting forged server-owned Preserved keys.
	const (
		stepPinSecret = "synth-locator-pin-7702"
		resURLSecret  = "synth-res-url-tok-7703"
	)
	locStored := storeEditableImportedScript(t, server, protocolName, "Bearer orig-7704")
	importreview.EnsureScriptImportPreserved(locStored.Script, "enterprise_archive")["recorded_resource_urls"] = []any{
		"https://cdn.example.test/bundle.js?access_token=" + resURLSecret,
	}
	locStored.Script.Actions[1].Body = `{"opaque":"` + stepPinSecret + `"}`
	if locStored.Script.Actions[1].Metadata == nil {
		locStored.Script.Actions[1].Metadata = map[string]any{}
	}
	locStored.Script.Actions[1].Metadata[dsl.ImportPreservedCredentialTargetsMetadataKey] = []any{
		map[string]any{"field": "body", "pointer": "/opaque"},
	}

	unmaskedCandidate := cloneScriptDefinition(locStored.Script)
	unmaskedCandidate.Actions[0].Target = "https://staging.example.test/orders"
	unmaskedCandidate.Actions[1].Metadata = nil
	unmaskedCandidate.Metadata = &dsl.ScriptMetadata{
		Import: &dsl.ImportMetadata{
			Source: "enterprise_archive",
			Preserved: map[string]any{
				"jsr223_sources": "forged-client-value",
			},
		},
	}

	prepared, trusted, err := resolveStoredReplayImportScript(unmaskedCandidate, locStored.Script, importreview.PreservedImportEditOptions{})
	if err != nil || trusted {
		t.Fatalf("resolve unmasked candidate: trusted=%v err=%v, want untrusted prepared candidate", trusted, err)
	}
	if _, forged := prepared.Metadata.Import.Preserved["jsr223_sources"]; forged {
		t.Fatalf("forged client jsr223_sources survived unmasked preparation: %#v", prepared.Metadata.Import.Preserved)
	}
	vals, err := engine.ImportedCredentialValues(prepared)
	if err != nil {
		t.Fatalf("ImportedCredentialValues(prepared): %v", err)
	}
	joined := strings.Join(vals, "\n")
	for _, want := range []string{stepPinSecret, resURLSecret} {
		if !strings.Contains(joined, want) {
			t.Fatalf("ImportedCredentialValues missed %q after unmasked preparation: %v", want, vals)
		}
	}
}

func TestResolveStoredReplayImportScriptUnmaskedFallbackUntrustedAndBlocksUnsafeJVMFlags(t *testing.T) {
	storedJava := &dsl.Script{
		ID:        "java-import-stored",
		Name:      "Java import stored",
		Version:   "1.0.0",
		Protocol:  "java_vuser",
		ProjectID: "p1",
		Config: dsl.ScriptConfig{
			JavaVuser: dsl.JavaVuserConfig{
				MaxProcesses:           4,
				AdditionalVMParameters: []string{"-Xmx512m", "-Ddatabase.password=synth-java-vm-pwd-8801"},
			},
		},
		Actions: []dsl.Action{{
			Name:   "run",
			Type:   "java_vuser_run",
			Params: map[string]any{"class": "Actions", "method": "action"},
		}},
	}
	ensureCredentialPreservingImportMetadata(storedJava, "enterprise_archive")
	importreview.EnsureScriptImportPreserved(storedJava, "enterprise_archive")[dsl.ImportPreservedRuntimeArtifactKey] = "java_bundle"

	retuned := cloneScriptDefinition(storedJava)
	retuned.Config.JavaVuser.MaxProcesses = 20
	prepared, trusted, err := resolveStoredReplayImportScript(retuned, storedJava, importreview.PreservedImportEditOptions{})
	if err != nil {
		t.Fatalf("resolve unmasked retuned java_vuser candidate: %v", err)
	}
	if trusted {
		t.Fatalf("expected unmasked reconciliation-fallback candidate to report trusted=false")
	}
	if got := prepared.Metadata.Import.Preserved[dsl.ImportPreservedRuntimeArtifactKey]; got != "java_bundle" {
		t.Fatalf("runtime artifact metadata = %#v, want java_bundle preserved", got)
	}

	unsafeAgent := cloneScriptDefinition(storedJava)
	unsafeAgent.Config.JavaVuser.AdditionalVMParameters = []string{"-Xmx512m", "-javaagent:/tmp/untrusted.jar"}
	if _, _, err := resolveStoredReplayImportScript(unsafeAgent, storedJava, importreview.PreservedImportEditOptions{}); err == nil || !strings.Contains(err.Error(), "additional_vm_parameters") {
		t.Fatalf("expected unsafe -javaagent VM flag to be rejected, got err=%v", err)
	}
}

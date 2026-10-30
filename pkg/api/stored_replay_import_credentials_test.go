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

	// A brand-new literal credential still requires the Credentials flow.
	introduced := cloneScriptDefinition(maskScriptSecrets(stored.Script))
	introduced.Actions[1].Headers = map[string]string{"Authorization": "Bearer brand-new-secret"}
	resp = startStoredScriptDebug(t, server, stored, introduced, "77777777-7777-4777-8777-777777777773")
	if resp.Code == http.StatusOK {
		t.Fatalf("status = %d body=%s, want a new executable credential refused", resp.Code, resp.Body.String())
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

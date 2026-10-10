package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/dsl"
)

type importTransportApplyResponse struct {
	Script               *dsl.Script                     `json:"script"`
	Changes              []requestimport.TransportChange `json:"changes"`
	ImportDraftExpiresAt *time.Time                      `json:"import_draft_expires_at"`
	ValidationIssues     []string                        `json:"validation_issues"`
	Error                string                          `json:"error"`
}

func postTransportApply(t *testing.T, server *Server, payload importTransportApplyRequest) (*httptest.ResponseRecorder, importTransportApplyResponse) {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/transport/apply", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, req)
	var decoded importTransportApplyResponse
	_ = json.Unmarshal(response.Body.Bytes(), &decoded)
	return response, decoded
}

func transportTestScript() *dsl.Script {
	return &dsl.Script{Name: "Transport", Version: "1", Protocol: "http", ProjectID: "project-a",
		Config: dsl.ScriptConfig{BaseURL: "https://app.example.test"},
		Actions: []dsl.Action{
			{Name: "home", Type: "GET", Method: "GET", Target: "/"},
			{Name: "batch", Type: "http_sequence", Params: map[string]any{"requests": []any{
				map[string]any{"name": "child", "type": "GET", "method": "GET", "target": "/child"},
			}}},
		}}
}

func TestHandleApplyImportTransport(t *testing.T) {
	server := NewServer(ServerConfig{})
	edits := []requestimport.TransportEdit{
		{Path: "actions[0]", Origin: "https://app.example.test", From: "", To: "auto+h3"},
		{Path: "actions[1].params.requests[0]", Origin: "https://app.example.test", From: "", To: "auto+h3"},
	}
	response, applied := postTransportApply(t, server, importTransportApplyRequest{Script: transportTestScript(), Edits: edits})
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if applied.Script.Actions[0].HTTPVersion.Value() != "auto+h3" || len(applied.Changes) != 2 {
		t.Fatalf("applied = %+v changes = %+v", applied.Script.Actions[0].HTTPVersion, applied.Changes)
	}
	child := applied.Script.Actions[1].Params["requests"].([]any)[0].(map[string]any)
	if child["http_version"] != "auto+h3" {
		t.Fatalf("nested request = %v", child)
	}

	// A review of a different draft is refused, not applied.
	if response, _ := postTransportApply(t, server, importTransportApplyRequest{Script: applied.Script, Edits: edits}); response.Code != http.StatusConflict {
		t.Fatalf("stale edit: status = %d body=%s", response.Code, response.Body.String())
	}
	// Exact HTTP/3 with a custom proxy would make the draft invalid.
	proxied := transportTestScript()
	proxied.Config.HTTP.Proxy = dsl.ProxyConfig{URL: "http://proxy.example.test:8080"}
	response, refused := postTransportApply(t, server, importTransportApplyRequest{Script: proxied, Edits: []requestimport.TransportEdit{
		{Path: "actions[0]", Origin: "https://app.example.test", From: "", To: "3"},
	}})
	if response.Code != http.StatusUnprocessableEntity || len(refused.ValidationIssues) == 0 {
		t.Fatalf("exact 3 through a proxy: status = %d body=%s", response.Code, response.Body.String())
	}
	for _, bad := range []importTransportApplyRequest{{Script: nil, Edits: edits}, {Script: transportTestScript()}} {
		if response, _ := postTransportApply(t, server, bad); response.Code != http.StatusBadRequest {
			t.Fatalf("invalid request: status = %d", response.Code)
		}
	}
}

func TestImportTransportEditRotatesTheAttestedDraftAndSaves(t *testing.T) {
	server := NewServer(ServerConfig{})
	const credential = "transport-preserved-credential-41c9"
	script := transportTestScript()
	script.ID = "transport-attested-import"
	// A preserved credential makes the draft attested.
	script.Actions[0].Headers = map[string]string{"Authorization": "Bearer " + credential}
	ensureCredentialPreservingImportMetadata(script, "sample")
	inspection := server.newScriptImportResponseWithSourceSecrets(
		httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/sample", nil),
		scriptImportResponseOptions{Script: script},
	)
	if failed, _ := inspection[importDraftResponseFailureKey].(bool); failed {
		t.Fatal("failed to secure test import draft")
	}
	payload, _ := json.Marshal(inspection["script"])
	var submitted dsl.Script
	if err := json.Unmarshal(payload, &submitted); err != nil {
		t.Fatal(err)
	}
	originalToken := importDraftAttestationToken(&submitted)
	response, applied := postTransportApply(t, server, importTransportApplyRequest{Script: &submitted, Edits: []requestimport.TransportEdit{
		{Path: "actions[0]", Origin: "https://app.example.test", From: "", To: "2"},
	}})
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if token := importDraftAttestationToken(applied.Script); token == "" || token == originalToken || applied.ImportDraftExpiresAt == nil {
		t.Fatalf("attestation not rotated: %q -> %q expiry=%v", originalToken, token, applied.ImportDraftExpiresAt)
	}
	// The superseded draft can no longer be edited.
	if response, _ := postTransportApply(t, server, importTransportApplyRequest{Script: &submitted, Edits: []requestimport.TransportEdit{
		{Path: "actions[0]", Origin: "https://app.example.test", From: "", To: "1.1"},
	}}); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "import_draft_attestation_invalid") {
		t.Fatalf("superseded draft: status = %d body=%s", response.Code, response.Body.String())
	}
	created := createAttestedImportScript(t, server, applied.Script)
	if created.Code != http.StatusCreated {
		t.Fatalf("save = %d body=%s", created.Code, created.Body.String())
	}
	durable, ok := server.runner.GetScript(applied.Script.ID)
	if !ok || durable.Script.Actions[0].HTTPVersion.Value() != "2" {
		t.Fatalf("durable policy = %+v", durable)
	}
	// The edit kept the attested credential byte-identical.
	if got := durable.Script.Actions[0].Headers["Authorization"]; got != "Bearer "+credential {
		t.Fatalf("durable credential = %q", got)
	}
	if provenance, _ := durable.Script.Actions[0].Metadata[requestimport.TransportEditMetadataKey].(map[string]any); provenance["policy"] != "2" || provenance["previous_policy"] != "inherit" {
		t.Fatalf("durable provenance = %v", durable.Script.Actions[0].Metadata)
	}
}

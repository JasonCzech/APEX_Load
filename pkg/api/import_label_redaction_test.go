package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/scrub"
)

func TestImportTransactionLabelsAreNotSecretCandidates(t *testing.T) {
	const label = "B2B_Checkout_ApexActionController_ServiceAddress_GetSearchResults_2026"
	const secret = "actual-password-fixture-9472"
	if scrub.Value(label, scrub.Policy{}) == label {
		t.Fatal("fixture must exercise generic entropy redaction")
	}
	for _, withSecret := range []bool{false, true} {
		t.Run(map[bool]string{false: "labels only", true: "real credentials"}[withSecret], func(t *testing.T) {
			script := &dsl.Script{ID: "import-labels", Name: label, Version: "1", Protocol: "http", Actions: []dsl.Action{
				{Name: label, Type: "source_transaction", Params: map[string]any{"function": "lr_start_transaction", "args": []any{label}, "call_args": []any{map[string]any{"kind": "string", "raw": "\"" + label + "\"", "value": label}}}, Metadata: map[string]any{"source_transaction": label}},
				{Name: label, Type: "http_sequence", Params: map[string]any{"requests": []any{map[string]any{"name": label, "type": "request", "method": "GET", "target": "/status"}}}},
			}}
			if withSecret {
				script.Config.HTTP.Auth = dsl.AuthConfig{Type: "basic", Password: secret}
				script.Actions = append(script.Actions, dsl.Action{Name: "copied " + secret, Type: "request", Method: "GET", Target: "/status", Headers: map[string]string{"X-API-Key": "real-api-key-fixture-2819"}})
			}
			projected := maskScriptSecrets(script)
			if projected.Actions[0].Params["args"].([]any)[0] != label {
				t.Fatalf("transaction argument = %#v", projected.Actions[0].Params)
			}
			if projected.Actions[0].Metadata["source_transaction"] != label {
				t.Fatal("transaction metadata redacted")
			}
			args := projected.Actions[0].Params["call_args"].([]any)[0].(map[string]any)
			if args["value"] != label || args["raw"] != "\""+label+"\"" {
				t.Fatalf("structured arguments = %#v", args)
			}
			requests := projected.Actions[1].Params["requests"].([]any)
			if requests[0].(map[string]any)["name"] != label {
				t.Fatal("nested request name redacted")
			}
			requirements := discoverSecretBindingRequirements(projected, 0)
			if !withSecret && len(requirements) != 0 {
				t.Fatalf("phantom credentials: %#v", requirements)
			}
			if withSecret {
				payload, _ := json.Marshal(projected)
				if strings.Contains(string(payload), secret) || strings.Contains(string(payload), "real-api-key-fixture-2819") {
					t.Fatal("actual credential leaked")
				}
				if projected.Config.HTTP.Auth.Password != credential.Redacted || !strings.Contains(projected.Actions[2].Name, credential.Redacted) {
					t.Fatal("known credentials must remain redacted including copied labels")
				}
				if len(requirements) == 0 {
					t.Fatal("real credentials must require binding")
				}
				for _, requirement := range requirements {
					if strings.Contains(requirement.Path, "/params/") || strings.Contains(requirement.Path, "/metadata/") {
						t.Fatalf("label became credential: %#v", requirement)
					}
				}
			}
		})
	}
}

func TestEnterpriseImportKeepsTransactionLabelsOutOfCredentialReview(t *testing.T) {
	const label = "B2B_Checkout_ApexActionController_ServiceAddress_GetSearchResults_2026"
	archive := buildEnterpriseRuntimeCredentialArchive(t, map[string]string{
		"Labels.usr":   "[General]\nType=Web (HTTP/HTML)\n[Actions]\nvuser_init=vuser_init.c\nAction=Action.c\nvuser_end=vuser_end.c\n",
		"vuser_init.c": "vuser_init() { return 0; }",
		"vuser_end.c":  "vuser_end() { return 0; }",
		"Action.c":     `Action() { lr_start_transaction("` + label + `"); web_url("` + label + `", "URL=http://127.0.0.1/status", "Resource=0", LAST); lr_end_transaction("` + label + `", LR_AUTO); return 0; }`,
	})
	server := NewServer(ServerConfig{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/enterprise-script-archive", bytes.NewReader(archive))
	req.Header.Set("Content-Type", "application/zip")
	res := httptest.NewRecorder()
	server.mux.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("import %d: %s", res.Code, res.Body.String())
	}
	var result struct {
		Script       dsl.Script                 `json:"script"`
		Requirements []SecretBindingRequirement `json:"secret_binding_requirements"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Requirements) != 0 {
		t.Fatalf("phantom credentials: %#v", result.Requirements)
	}
	found := false
	for _, action := range result.Script.Actions {
		if action.Type == "source_transaction" {
			found = true
			if action.Params["args"].([]any)[0] != label {
				t.Fatalf("transaction label changed: %#v", action.Params)
			}
		}
	}
	if !found {
		t.Fatal("import must produce transaction markers")
	}
}

func TestImportLabelClassificationDoesNotExemptCredentialFields(t *testing.T) {
	const token = "AKIAIOSFODNN7EXAMPLEwJalrXUtnFEMIK7MDENGbPxRfiCY"
	for index, params := range []map[string]any{
		{"name": "password", "type": "string", "value": "short-password"},
		{"api_key": token, "name": "ordinary"},
		{"function": "unknown", "args": []any{token}},
		{"function": "lr_set_transaction", "args": []any{"ordinary", token}},
	} {
		script := &dsl.Script{Name: "safe", Version: "1", Protocol: "http", Actions: []dsl.Action{{Name: "action", Type: "source_transaction", Params: params}}}
		payload, _ := json.Marshal(maskScriptSecrets(script))
		if strings.Contains(string(payload), token) || strings.Contains(string(payload), "short-password") {
			t.Fatalf("credential classification was bypassed in fixture %d: %s", index, payload)
		}
	}
}

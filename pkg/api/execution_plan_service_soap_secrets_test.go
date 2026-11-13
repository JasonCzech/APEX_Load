package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

// Plan entry runtime_overrides are stored and returned verbatim, so a typed
// SOAP WS-Security password must be a project credential reference.
func TestValidateExecutionPlanRejectsPlaintextSOAPWSSecurityPassword(t *testing.T) {
	server := &Server{}
	cases := []struct {
		name     string
		password string
		wantOK   bool
	}{
		{name: "plaintext", password: "P@ss", wantOK: false},
		{name: "mixed literal and reference", password: "x{{secret.SOAP_PASS}}", wantOK: false},
		{name: "reserved controller reference", password: "{{secret.APEX_RUNTIME_OVERRIDE_G1_abc}}", wantOK: false},
		{name: "project credential reference", password: "{{secret.SOAP_PASS}}", wantOK: true},
		{name: "empty", password: "", wantOK: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(map[string]any{
				"soap": map[string]any{
					"ws_security": map[string]any{
						"username_token": map[string]any{"username": "svc", "password": tc.password},
					},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			status, message := server.validateExecutionPlan(&storage.ExecutionPlan{
				Name:    "plan",
				Entries: []storage.ExecutionPlanEntry{{ScenarioID: "scenario", RuntimeOverrides: raw}},
			})
			if tc.wantOK {
				if status != 0 {
					t.Fatalf("validation = (%d, %q), want accepted", status, message)
				}
				return
			}
			if status != http.StatusBadRequest || !strings.Contains(message, "soap.ws_security.username_token.password") {
				t.Fatalf("validation = (%d, %q), want 400 naming the SOAP password field", status, message)
			}
			if tc.password != "" && strings.Contains(message, tc.password) {
				t.Fatalf("validation message echoes the credential: %q", message)
			}
		})
	}
}

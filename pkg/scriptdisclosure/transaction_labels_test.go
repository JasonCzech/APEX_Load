package scriptdisclosure

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/scrub"
)

func TestProjectPreservesTransactionLabels(t *testing.T) {
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
			projected, err := Project(script)
			if err != nil {
				t.Fatal(err)
			}
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

			if withSecret {
				payload, _ := json.Marshal(projected)
				if strings.Contains(string(payload), secret) || strings.Contains(string(payload), "real-api-key-fixture-2819") {
					t.Fatal("actual credential leaked")
				}
				if projected.Config.HTTP.Auth.Password != credential.Redacted || !strings.Contains(projected.Actions[2].Name, credential.Redacted) {
					t.Fatal("known credentials must remain redacted including copied labels")
				}

			}
		})
	}
}

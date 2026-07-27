package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestMaskBrunoNativeCommitScriptsProtectsLegacyReceiptSnapshots(t *testing.T) {
	const canary = "legacy-native-receipt-password-7fc2"
	script := &dsl.Script{
		ID:        "legacy-native-script",
		Name:      "Legacy native script",
		Version:   "1.0.0",
		Protocol:  "http",
		ProjectID: "legacy-native-project",
		Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions: []dsl.Action{{
			Name:   "Legacy login",
			Type:   "request",
			Method: http.MethodPost,
			Target: "/login",
			Headers: map[string]string{
				"Authorization": "Bearer " + canary,
				"Content-Type":  "application/json",
			},
			Body: `{"password":"` + canary + `"}`,
		}},
	}

	projected := maskBrunoNativeCommitScripts([]*dsl.Script{script})
	if len(projected) != 1 || projected[0] == nil {
		t.Fatalf("projected receipt scripts = %#v", projected)
	}
	payload, err := json.Marshal(projected)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), canary) {
		t.Fatalf("legacy receipt response disclosed credential: %s", payload)
	}
	if !strings.Contains(string(payload), maskedScriptSecret) {
		t.Fatalf("legacy receipt response did not contain a disclosure-safe projection: %s", payload)
	}
	if script.Actions[0].Headers["Authorization"] != "Bearer "+canary ||
		!strings.Contains(script.Actions[0].Body, canary) {
		t.Fatal("response projection mutated the immutable receipt snapshot")
	}
}

func TestMaskBrunoNativeCommitScriptsFallsBackToSafeIdentity(t *testing.T) {
	script := &dsl.Script{
		ID:        "legacy-unprojectable-script",
		Name:      "Must not survive",
		Version:   "1.0.0",
		Protocol:  "http",
		ProjectID: "legacy-native-project",
		Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions: []dsl.Action{{
			Name: "Typed credential", Type: "request", Method: http.MethodPost, Target: "/login",
			Headers: map[string]string{"Content-Type": "application/json"},
			Body:    `{"password":7}`,
		}},
	}

	projected := maskBrunoNativeCommitScripts([]*dsl.Script{script})
	if len(projected) != 1 || projected[0] == nil {
		t.Fatalf("fallback receipt scripts = %#v", projected)
	}
	if projected[0].ID != script.ID || projected[0].Protocol != script.Protocol ||
		projected[0].Name != maskedScriptSecret {
		t.Fatalf("fallback receipt identity = %#v", projected[0])
	}
	if len(projected[0].Actions) != 0 || projected[0].Config.BaseURL != "" {
		t.Fatalf("fallback retained unprojectable executable payload: %#v", projected[0])
	}
}

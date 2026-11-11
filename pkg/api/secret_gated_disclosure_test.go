package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// Both string values and numeric counts under secret_gated save directly in
// authored scripts without requiring secret bindings or redacting the DSL.
func TestCreateScriptRequiresBindingsForSecretGatedText(t *testing.T) {
	server := NewServer(ServerConfig{})
	gated := func(params, body any) *dsl.Script {
		encoded, _ := json.Marshal(map[string]any{"secret_gated": body, "region": "west"})
		return &dsl.Script{
			Name: "Secret gated", Version: "1", Protocol: "http",
			Config: dsl.ScriptConfig{BaseURL: "https://example.test"},
			Actions: []dsl.Action{
				{Name: "search", Type: "request", Method: "GET", Target: "/search", Params: map[string]any{"secret_gated": params}},
				{Name: "submit", Type: "request", Method: "POST", Target: "/submit", Headers: map[string]string{"Content-Type": "application/json"}, Body: string(encoded)},
			},
		}
	}

	create := postSecretBindingJSON(t, server, "/api/v1/scripts", map[string]any{"script": gated("syntheticgatedparam", "syntheticgatedbody")})
	if create.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 body=%s", create.Code, create.Body.String())
	}
	if body := create.Body.String(); !strings.Contains(body, "syntheticgatedparam") || !strings.Contains(body, "syntheticgatedbody") || strings.Contains(body, "[REDACTED]") {
		t.Fatalf("saved script response redacted text held under secret_gated: %s", body)
	}
	if got := server.runner.ListScripts(); len(got) != 1 {
		t.Fatalf("stored scripts = %d, want 1", len(got))
	}

	counted := postSecretBindingJSON(t, server, "/api/v1/scripts", map[string]any{"script": gated(3, 2)})
	if counted.Code != http.StatusCreated {
		t.Fatalf("count under secret_gated = %d %s, want it saved", counted.Code, counted.Body.String())
	}
}

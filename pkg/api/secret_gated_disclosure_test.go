package api

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// Only a number under secret_gated is the fidelity count. Text under that key
// in params or a JSON body is a credential to a save: it is refused with a
// binding requirement at each place, never stored raw, while a count saves.
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
	if create.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 body=%s", create.Code, create.Body.String())
	}
	var blocked struct {
		Code         string                     `json:"code"`
		Requirements []SecretBindingRequirement `json:"secret_binding_requirements"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &blocked); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, requirement := range blocked.Requirements {
		paths = append(paths, requirement.Path)
	}
	slices.Sort(paths)
	if want := []string{"/actions/0/params/secret_gated", "/actions/1/body"}; blocked.Code != secretBindingsRequiredCode || !slices.Equal(paths, want) {
		t.Fatalf("blocked = %s at %q, want %s at %q", blocked.Code, paths, secretBindingsRequiredCode, want)
	}
	if body := create.Body.String(); strings.Contains(body, "syntheticgatedparam") || strings.Contains(body, "syntheticgatedbody") {
		t.Fatalf("binding response disclosed text held under secret_gated: %s", body)
	}
	if got := server.runner.ListScripts(); len(got) != 0 {
		t.Fatalf("a script with raw text under secret_gated was stored: %d", len(got))
	}

	counted := postSecretBindingJSON(t, server, "/api/v1/scripts", map[string]any{"script": gated(3, 2)})
	if counted.Code != http.StatusCreated {
		t.Fatalf("count under secret_gated = %d %s, want it saved", counted.Code, counted.Body.String())
	}
}

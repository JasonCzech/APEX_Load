package bruno

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestRetiredBrunoDefaultImportUsesNativeHTTP(t *testing.T) {
	result, err := Inspect(Input{Filename: "request.yml", Content: []byte("info: {name: Health, type: http}\nhttp: {method: GET, url: https://example.test/health}\n")})
	if err != nil {
		t.Fatal(err)
	}
	if result.OutputMode != OutputModeNativeRequests || result.Script != nil || len(result.Scripts) != 1 || result.Scripts[0].Script.Protocol != "http" {
		t.Fatalf("native result = %#v", result)
	}
	if result.Fidelity.Compatibility != "native_requests" || strings.Contains(strings.Join(result.Fidelity.IntentionalExtensions, " "), "collection runs") {
		t.Fatalf("fidelity advertises the retired runner: %#v", result.Fidelity)
	}
	if result.Capabilities.RequiredSandbox != "" || result.NativeProjection.Eligible || len(result.NativeProjection.Reasons) != 0 {
		t.Fatalf("native result advertises retired runner capabilities: %#v", result)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"native_projection"`) || strings.Contains(string(encoded), `"required_sandbox"`) {
		t.Fatal("native import response still exposes retired runner fields")
	}
}

func TestRetiredBrunoRejectsCanonicalAndSandboxImports(t *testing.T) {
	for name, input := range map[string]Input{
		"canonical": {Options: InspectOptions{OutputMode: OutputModeCanonical}},
		"sandbox":   {Sandbox: "safe"},
		"selection": {Options: InspectOptions{Selection: "collection"}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Inspect(input); err == nil || !strings.Contains(err.Error(), dsl.BrunoRunnerRetiredMessage) {
				t.Fatalf("retirement error = %v", err)
			}
		})
	}
}

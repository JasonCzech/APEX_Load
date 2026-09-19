package api

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestLargeImportCredentialReviewDoesNotCountOrdinaryFields(t *testing.T) {
	script := &dsl.Script{
		Name: "Large import", Protocol: "http",
		Actions: []dsl.Action{{
			Name: "request", Type: "request", Method: "GET", Target: "/status",
			Params: map[string]any{"ordinary_blob": strings.Repeat("x", 4<<20)},
		}},
	}
	for i := 0; i < 20; i++ {
		script.Actions[0].Params[fmt.Sprintf("field_%d_password", i)] = fmt.Sprintf("large-review-secret-%d", i)
	}
	masked := maskScriptSecrets(script)
	if masked == nil {
		t.Fatal("projection failed")
	}
	if masked.Actions[0].Params["ordinary_blob"] != script.Actions[0].Params["ordinary_blob"] || masked.Actions[0].Target != "/status" {
		t.Fatal("ordinary request fields were masked")
	}
	requirements := discoverSecretBindingRequirements(masked, 0)
	if len(requirements) != 20 {
		t.Fatalf("credential occurrences = %d, want 20", len(requirements))
	}
	for _, r := range requirements {
		if !strings.HasSuffix(r.Path, "_password") {
			t.Fatalf("noncredential requirement at %s", r.Path)
		}
	}
}

func TestImportSourceEvidenceDoesNotRequireCredentialBindings(t *testing.T) {
	script := redactedBindingTestScript()
	script.Actions[0].Metadata = map[string]any{}
	for key := range importProvenanceEvidenceKeys {
		script.Actions[0].Metadata[key] = maskedScriptSecret
	}
	for key := range httpImportProvenanceEvidenceKeys {
		script.Actions[0].Metadata[key] = maskedScriptSecret
	}
	script.Actions[0].Metadata["outbound_password"] = maskedScriptSecret
	requirements := discoverSecretBindingRequirements(script, 0)
	if len(requirements) != 6 {
		t.Fatalf("requirements = %d, want five executable credentials and outbound metadata", len(requirements))
	}
	for _, r := range requirements {
		if strings.Contains(r.Path, "/metadata/") && !strings.HasSuffix(r.Path, "/outbound_password") {
			t.Fatalf("source evidence produced requirement %s", r.Path)
		}
	}
}

func TestImportDescriptiveHeadersAreNotCredentials(t *testing.T) {
	const password = "descriptive-header-credential-canary"
	for _, header := range []struct{ name, value string }{
		{"User-Agent", "SalesforceMobileSDK/13.1.0 Hybrid SalesforceTablet/13.1.0 WebEngine/SFWKWebViewEngine SalesforceSDK/260.000.0"},
		{"x-dtpc", "6$123456789_1234567890123v987654321e42"},
	} {
		for _, scenario := range []struct{ name, suffix string }{
			{"ordinary", ""},
			{"structured credential", "?password=" + password},
			{"known credential", "?copy=" + password},
		} {
			t.Run(header.name+"/"+scenario.name, func(t *testing.T) {
				script := &dsl.Script{
					Name: "Descriptive headers", Protocol: "http",
					Config: dsl.ScriptConfig{HTTP: dsl.HTTPConfig{Auth: dsl.AuthConfig{
						Type: "basic", Password: password,
					}}},
					Actions: []dsl.Action{{
						Name: "request", Type: "request", Method: "GET", Target: "/",
						Headers:    map[string]string{header.name: header.value + scenario.suffix},
						HeaderList: dsl.HeaderList{{Name: header.name, Value: header.value + scenario.suffix}},
					}},
				}
				masked := maskScriptSecrets(script)
				if masked == nil {
					t.Fatal("projection failed")
				}
				for _, got := range []string{masked.Actions[0].Headers[header.name], masked.Actions[0].HeaderList[0].Value} {
					if (scenario.suffix == "" && !strings.HasPrefix(got, header.value)) || strings.Contains(got, password) {
						t.Fatal("header projection lost descriptive data or disclosed credential")
					}
				}
				if scenario.suffix == "" {
					if got := len(discoverSecretBindingRequirements(masked, 0)); got != 1 {
						t.Fatalf("requirements = %d, want only auth password", got)
					}
				}
			})
		}
	}
}

func TestImportDescriptiveHeaderExceptionsStillMaskUnexpectedTokens(t *testing.T) {
	const token = "aZ9qR7xW5mN3vB1cD8fG6hJ4kL2pS0tU"
	for _, name := range []string{"User-Agent", "x-dtpc"} {
		t.Run(name, func(t *testing.T) {
			script := &dsl.Script{Name: "Unexpected header", Protocol: "http", Actions: []dsl.Action{{Name: "request", Type: "GET", Target: "/", Headers: map[string]string{name: token}, HeaderList: dsl.HeaderList{{Name: name, Value: token}}}}}
			masked := maskScriptSecrets(script)
			if masked == nil {
				t.Fatal("projection failed")
			}
			if strings.Contains(masked.Actions[0].Headers[name], token) || strings.Contains(masked.Actions[0].HeaderList[0].Value, token) {
				t.Fatal("unexpected bare token disclosed by descriptive header exemption")
			}
		})
	}
}

func TestImportSourceEvidenceExemptionsDoNotHideOutboundMetadata(t *testing.T) {
	for _, kind := range []string{"kafka_produce", "jms_send", "unknown_extension"} {
		t.Run(kind, func(t *testing.T) {
			script := &dsl.Script{Name: "Outbound metadata", Protocol: "kafka", Actions: []dsl.Action{{Name: "send", Type: kind, Target: "events", Metadata: map[string]any{"phase": maskedScriptSecret, "source_action_name": maskedScriptSecret}}}}
			if got := len(discoverSecretBindingRequirements(script, 0)); got != 2 {
				t.Fatalf("requirements = %d, want 2 outbound metadata fields", got)
			}
			raw, err := scriptAsJSONValue(script)
			if err != nil {
				t.Fatal(err)
			}
			if !jsonValueContainsExecutableMaskedSecret(raw, "") {
				t.Fatal("orphan sentinel guard bypassed for outbound metadata")
			}
		})
	}
}

func TestHTTPProvenanceExemptionsAreScopedToTypedActionMetadata(t *testing.T) {
	for _, tc := range []struct {
		name   string
		action dsl.Action
		want   int
	}{
		{"HTTP action", dsl.Action{Name: "request", Type: "GET", Target: "/", Metadata: map[string]any{"phase": maskedScriptSecret}}, 0},
		{"messaging action in HTTP script", dsl.Action{Name: "send", Type: "jms_send", Target: "queue", Metadata: map[string]any{"phase": maskedScriptSecret}}, 1},
		{"arbitrary parameters", dsl.Action{Name: "request", Type: "GET", Target: "/", Params: map[string]any{"metadata": map[string]any{"phase": maskedScriptSecret}}}, 1},
		{"action-shaped parameters", dsl.Action{Name: "request", Type: "GET", Target: "/", Params: map[string]any{"actions": []any{map[string]any{"name": "fake", "type": "GET", "metadata": map[string]any{"phase": maskedScriptSecret}}}}}, 1},
		{"sequence", dsl.Action{Name: "sequence", Type: "http_sequence", Params: map[string]any{"requests": []any{map[string]any{"name": "child", "target": "/", "metadata": map[string]any{"phase": maskedScriptSecret}}}}}, 0},
		{"control default", dsl.Action{Name: "switch", Type: "control_switch", Control: &dsl.ControlFlow{Default: []dsl.Action{{Name: "child", Type: "GET", Target: "/", Metadata: map[string]any{"phase": maskedScriptSecret}}}}}, 0},
		{"control branches", dsl.Action{Name: "parallel", Type: "control_parallel", Control: &dsl.ControlFlow{Branches: []dsl.ParallelBranch{{Name: "branch", Steps: []dsl.Action{{Name: "child", Type: "GET", Target: "/", Metadata: map[string]any{"phase": maskedScriptSecret}}}}}}}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := &dsl.Script{Name: "Scope", Protocol: "http", Actions: []dsl.Action{tc.action}}
			if got := len(discoverSecretBindingRequirements(script, 0)); got != tc.want {
				t.Fatalf("requirements = %d, want %d", got, tc.want)
			}
			raw, err := scriptAsJSONValue(script)
			if err != nil {
				t.Fatal(err)
			}
			if got := jsonValueContainsExecutableMaskedSecret(raw, ""); got != (tc.want > 0) {
				t.Fatalf("orphan guard = %v, want %v", got, tc.want > 0)
			}
			if err := reconcileMaskedScriptSecrets(script, nil); (err != nil) != (tc.want > 0) {
				t.Fatalf("create reconciliation error = %v", err)
			}
		})
	}
}

func TestImportDescriptiveHeadersPreserveTemplateReferences(t *testing.T) {
	const reference = "{{var.aZ9qR7xW5mN3vB1cD8fG6hJ4kL2pS0tU}}"
	for _, header := range []struct{ name, value string }{
		{"User-Agent", reference},
		{"User-Agent", "Client/" + reference},
		{"x-dtpc", reference},
		{"x-dtpc", "6$123_" + reference},
	} {
		t.Run(header.name+header.value[:min(6, len(header.value))], func(t *testing.T) {
			script := &dsl.Script{Name: "Templates", Protocol: "http", Actions: []dsl.Action{{Name: "request", Type: "GET", Target: "/", Headers: map[string]string{header.name: header.value}, HeaderList: dsl.HeaderList{{Name: header.name, Value: header.value}}}}}
			masked := maskScriptSecrets(script)
			if masked == nil {
				t.Fatal("projection failed")
			}
			if masked.Actions[0].Headers[header.name] != header.value || masked.Actions[0].HeaderList[0].Value != header.value {
				t.Fatal("header runtime reference changed")
			}
		})
	}
}

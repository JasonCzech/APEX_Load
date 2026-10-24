package importreview

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// importEvaluationScript is a credential-preserving import whose preserved
// evaluation output (fidelity report and replay preflight) holds counts and
// secret names. Only the Authorization header on "orders" is a kept credential.
func importEvaluationScript() *dsl.Script {
	script := &dsl.Script{
		Name: "Kept credential", Version: "1", Protocol: "http",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "enterprise_script_archive",
			Preserved: map[string]any{
				"fidelity_report":  map[string]any{"secret_gated": 2, "dropped_bodies": 1, "promotion_status": "blocked"},
				"replay_preflight": map[string]any{"valid": false, "required_secrets": []any{"API_TOKEN"}, "missing_secrets": []any{"API_TOKEN", "db_password"}},
			},
		}},
		Actions: []dsl.Action{
			{Name: "orders", Type: "GET", Method: "GET", Target: "/orders", Headers: map[string]string{"Authorization": "Bearer projection-synthetic-kept-token"}},
			{Name: "cart", Type: "GET", Method: "GET", Target: "/cart"},
		},
	}
	MarkCredentialPreservingImportScript(script)
	return script
}

// The whole disclosure pipeline, this package's projection followed by
// scriptdisclosure, keeps the fidelity report's secret_gated count and the
// preflight's secret names, and still masks a secret-named key in the report.
func TestMaskScriptSecretsKeepsImportEvaluationCountsAndNames(t *testing.T) {
	const reportSecret = "projection-synthetic-report-secret"
	script := importEvaluationScript()
	script.Metadata.Import.Preserved["fidelity_report"].(map[string]any)["client_secret"] = reportSecret
	masked := MaskScriptSecrets(script)
	if masked == nil {
		t.Fatal("MaskScriptSecrets failed")
	}
	report, _ := masked.Metadata.Import.Preserved["fidelity_report"].(map[string]any)
	if got, _ := report["secret_gated"].(float64); got != 2 {
		t.Fatalf("fidelity_report.secret_gated = %#v, want the count 2", report["secret_gated"])
	}
	if report["client_secret"] != MaskedScriptSecret {
		t.Fatalf("fidelity_report.client_secret = %#v, want it masked", report["client_secret"])
	}
	preflight, _ := masked.Metadata.Import.Preserved["replay_preflight"].(map[string]any)
	missing := ImportPreservedStringSlice(preflight["missing_secrets"])
	if !slices.Equal(missing, []string{"API_TOKEN", "db_password"}) {
		t.Fatalf("replay_preflight.missing_secrets = %#v, want the secret names as stored", preflight["missing_secrets"])
	}
	payload, err := json.Marshal(masked)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), reportSecret) || strings.Contains(string(payload), "projection-synthetic-kept-token") {
		t.Fatal("projection disclosed a credential")
	}
}

// Counts and names in the preserved evaluation output are not kept
// credentials, so they pin no execution context: a save may add a parameter or
// retarget a request that does not carry the kept credential. Retargeting the
// request that carries it is still refused at that request.
func TestValidatePreservedImportCredentialEditsIgnoresImportEvaluationOutput(t *testing.T) {
	prior := importEvaluationScript()
	for name, edit := range map[string]func(*dsl.Script){
		"parameter": func(script *dsl.Script) {
			script.Parameters = append(script.Parameters, dsl.Parameter{Name: "region", Type: "list", Values: []string{"west"}, Update: "once"})
		},
		"request without the kept credential": func(script *dsl.Script) { script.Actions[1].Target = "/cart/summary" },
	} {
		candidate := CloneScriptDefinition(prior)
		edit(candidate)
		if err := ValidatePreservedImportCredentialEdits(candidate, prior); err != nil {
			t.Fatalf("%s: %v, want the edit accepted", name, err)
		}
	}
	candidate := CloneScriptDefinition(prior)
	candidate.Actions[0].Target = "/orders/export"
	err := ValidatePreservedImportCredentialEdits(candidate, prior)
	if err == nil || !strings.Contains(err.Error(), "retained imported credentials at actions[0]") {
		t.Fatalf("retargeting the kept credential's request = %v, want the refusal at actions[0]", err)
	}
}

// This package's own projection keeps only a number under secret_gated, typed
// or decoded from JSON, as the fidelity count. Text under that key is
// secret-named like any other and is masked here, before scriptdisclosure
// runs.
func TestProjectScriptNodeMasksSecretGatedTextAndKeepsOnlyTheCount(t *testing.T) {
	projected, _ := ProjectScriptNode(map[string]any{
		"fidelity_report": map[string]any{"secret_gated": float64(2), "dropped_bodies": float64(1)},
		"preserved":       map[string]any{"secret_gated": 3},
		"params":          map[string]any{"secret_gated": "syntheticgatedtext"},
		"flags":           map[string]any{"secret_gated": true},
	}, ScriptProjectionContext{}).(map[string]any)
	if report, _ := projected["fidelity_report"].(map[string]any); report["secret_gated"] != float64(2) || report["dropped_bodies"] != float64(1) {
		t.Fatalf("fidelity_report = %#v, want the counts kept", projected["fidelity_report"])
	}
	if preserved, _ := projected["preserved"].(map[string]any); preserved["secret_gated"] != 3 {
		t.Fatalf("typed count = %#v, want it kept", projected["preserved"])
	}
	for _, key := range []string{"params", "flags"} {
		if value, _ := projected[key].(map[string]any); value["secret_gated"] != MaskedScriptSecret {
			t.Fatalf("%s.secret_gated = %#v, want a value that is not a count masked", key, projected[key])
		}
	}
}

// Text under secret_gated in params or a JSON body is masked on every read and
// still needs a logical secret binding before a save stores it, while the
// fidelity report's count needs none.
func TestMaskScriptSecretsGatesSecretGatedTextButNotTheCount(t *testing.T) {
	script := &dsl.Script{
		Name: "Secret gated", Version: "1", Protocol: "http",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source:    "enterprise_script_archive",
			Preserved: map[string]any{"fidelity_report": map[string]any{"secret_gated": 2, "promotion_status": "blocked"}},
		}},
		Actions: []dsl.Action{
			{Name: "params", Type: "request", Method: "GET", Target: "https://example.test/search", Params: map[string]any{"secret_gated": "syntheticgatedparam"}},
			{Name: "body", Type: "request", Method: "POST", Target: "https://example.test/submit", Headers: map[string]string{"Content-Type": "application/json"}, Body: `{"secret_gated":"syntheticgatedbody","region":"west"}`},
		},
	}
	masked := MaskScriptSecrets(script)
	if masked == nil {
		t.Fatal("MaskScriptSecrets failed")
	}
	if report, _ := masked.Metadata.Import.Preserved["fidelity_report"].(map[string]any); report["secret_gated"] != float64(2) {
		t.Fatalf("fidelity_report = %#v, want the count kept", report)
	}
	payload, err := json.Marshal(masked)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"syntheticgatedparam", "syntheticgatedbody"} {
		if strings.Contains(string(payload), value) {
			t.Fatalf("projection disclosed %q held under secret_gated", value)
		}
	}
	var paths []string
	for _, requirement := range DiscoverSecretBindingRequirements(masked, 0) {
		paths = append(paths, requirement.Path)
	}
	slices.Sort(paths)
	if want := []string{"/actions/0/params/secret_gated", "/actions/1/body"}; !slices.Equal(paths, want) {
		t.Fatalf("binding requirements at %q, want %q", paths, want)
	}
}

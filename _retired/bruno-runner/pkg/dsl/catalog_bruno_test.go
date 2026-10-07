package dsl

import "testing"

func TestBrunoRunActionCatalogAndValidation(t *testing.T) {
	spec, ok := SpecForActionType("bruno_run")
	if !ok || len(spec.Protocols) != 1 || spec.Protocols[0] != "bruno" {
		t.Fatalf("bruno catalog spec = %#v, %v", spec, ok)
	}
	script := &Script{
		Name:     "Imported Bruno",
		Protocol: "bruno",
		Config: ScriptConfig{Bruno: BrunoConfig{
			BundleRef:            "artifact://sha256/demo",
			BundleSHA256:         "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			CompatibilityVersion: "3.5.2",
			Sandbox:              "safe",
		}},
		Actions: []Action{{
			Name: "Run collection",
			Type: "bruno_run",
			Params: map[string]any{
				"selection": "collection",
				"execution": "sequential",
			},
		}},
	}
	if issues := ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("valid Bruno script issues = %v", issues)
	}
	script.Config.Bruno.BundleSHA256 = ""
	if issues := ValidateScriptIssues(script); !containsIssue(issues, "config.bruno.bundle_sha256 is required") {
		t.Fatalf("digest-less Bruno script issues = %v", issues)
	}
	script.Config.Bruno.BundleSHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	script.Actions[0].Params = map[string]any{"selection": "request"}
	if issues := ValidateScriptIssues(script); len(issues) == 0 {
		t.Fatal("request selection without paths unexpectedly validated")
	}
}

func TestBrunoSecretBindingsRejectPlaintextAndCompositeTemplates(t *testing.T) {
	for _, value := range []string{
		"plaintext-password",
		"Bearer {{secret.BRUNO_TOKEN}}",
		"{{env.BRUNO_TOKEN}}",
		"",
	} {
		script := &Script{
			Name: "Invalid Bruno binding", Version: "1.0", Protocol: "bruno",
			Config: ScriptConfig{Bruno: BrunoConfig{
				BundleRef:      "artifact://bundle",
				SecretBindings: map[string]string{"FOO": value},
			}},
			Actions: []Action{{Name: "Run", Type: "bruno_run", Target: "."}},
		}
		issues := ValidateScriptIssues(script)
		if !containsIssue(issues, `config.bruno.secret_bindings["FOO"] must be an exact {{secret.NAME}} reference`) {
			t.Fatalf("binding %q issues = %v", value, issues)
		}
	}
}

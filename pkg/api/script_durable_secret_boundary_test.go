package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func durableBoundaryScript(id, authorization, reviewSecret string) *dsl.Script {
	return &dsl.Script{
		ID: id, Name: "Durable secret boundary", Version: "1.0.0", Protocol: "http", ProjectID: "project-a",
		Config: dsl.ScriptConfig{BaseURL: "https://example.test"},
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Source: "test", Preserved: map[string]any{
			"source_review": map[string]any{"authorization": "Bearer " + reviewSecret},
		}}},
		Actions: []dsl.Action{{
			Name: "Request", Type: "request", Method: http.MethodGet, Target: "/",
			Headers: map[string]string{"Authorization": authorization},
		}},
	}
}

func TestDurableScriptCreatePathsRejectExecutableCredentials(t *testing.T) {
	for _, test := range []struct {
		name    string
		persist func(*TestRunner, *dsl.Script) error
	}{
		{name: "store", persist: func(runner *TestRunner, script *dsl.Script) error {
			_, err := runner.StoreScriptDurable(script)
			return err
		}},
		{name: "create only", persist: func(runner *TestRunner, script *dsl.Script) error {
			_, err := runner.CreateScriptDurable(script)
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, authorization := range []string{"Bearer raw-credential-never-store", "Bearer " + maskedScriptSecret} {
				runner := NewTestRunner(1, NewHub(), nil)
				script := durableBoundaryScript("blocked-"+strings.ReplaceAll(test.name, " ", "-"), authorization, "review")
				if err := test.persist(runner, script); err == nil || !strings.Contains(err.Error(), "require logical secret bindings") {
					t.Fatalf("authorization %q error = %v", authorization, err)
				}
				if scripts := runner.ListScripts(); len(scripts) != 0 {
					t.Fatalf("blocked durable create persisted scripts: %#v", scripts)
				}
			}
		})
	}
}

func TestDurableScriptWritesPersistOnlyProjectedForm(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	const createReviewSecret = "durable-create-review-never-store"
	stored, err := runner.StoreScriptDurable(durableBoundaryScript(
		"durable-safe", "Bearer {{secret.LOGIN_TOKEN}}", createReviewSecret,
	))
	if err != nil {
		t.Fatal(err)
	}
	assertAuthoringDraftDisclosureSafe(t, stored.Script, createReviewSecret)

	const updateCredential = "durable-update-credential-never-store"
	unsafeUpdate := cloneScriptDefinition(stored.Script)
	unsafeUpdate.Actions[0].Headers["Authorization"] = "Bearer " + updateCredential
	if _, _, err := runner.UpdateScriptDurableInProject(stored.ID, unsafeUpdate, stored.ProjectID); err == nil || !strings.Contains(err.Error(), "require logical secret bindings") {
		t.Fatalf("unsafe durable update error = %v", err)
	}
	unchanged, ok := runner.GetScript(stored.ID)
	if !ok {
		t.Fatal("stored script disappeared after blocked update")
	}
	assertAuthoringDraftDisclosureSafe(t, unchanged.Script, updateCredential)
	if got := unchanged.Script.Actions[0].Headers["Authorization"]; got != "Bearer {{secret.LOGIN_TOKEN}}" {
		t.Fatalf("blocked update changed Authorization = %q", got)
	}

	conditionalUnsafe := cloneScriptDefinition(unchanged.Script)
	conditionalUnsafe.Actions[0].Headers["Authorization"] = "Bearer " + updateCredential
	if _, _, err := runner.UpdateScriptDurableIfUnchanged(conditionalUnsafe, unchanged.Script); err == nil || !strings.Contains(err.Error(), "require logical secret bindings") {
		t.Fatalf("unsafe conditional update error = %v", err)
	}

	const updateReviewSecret = "durable-update-review-never-store"
	safeUpdate := cloneScriptDefinition(unchanged.Script)
	safeUpdate.Metadata.Import.Preserved["source_review"] = map[string]any{"authorization": "Bearer " + updateReviewSecret}
	safeUpdate.Actions[0].Headers["Authorization"] = "Bearer {{secret.ROTATED_TOKEN}}"
	updated, ok, err := runner.UpdateScriptDurableInProject(stored.ID, safeUpdate, stored.ProjectID)
	if err != nil || !ok {
		t.Fatalf("safe durable update = %#v, ok=%t, err=%v", updated, ok, err)
	}
	assertAuthoringDraftDisclosureSafe(t, updated.Script, createReviewSecret, updateReviewSecret)
	if got := updated.Script.Actions[0].Headers["Authorization"]; got != "Bearer {{secret.ROTATED_TOKEN}}" {
		t.Fatalf("safe update Authorization = %q", got)
	}
}

func TestSaveStoredScriptFallbackEnforcesSecretBoundary(t *testing.T) {
	store := &metricsStoreSpy{}
	runner := NewTestRunner(1, NewHub(), store)

	unsafe := durableBoundaryScript("fallback-unsafe", "Bearer raw-fallback-secret", "review")
	if err := runner.saveStoredScript(unsafe); err == nil || !strings.Contains(err.Error(), "require logical secret bindings") {
		t.Fatalf("unsafe fallback write error = %v", err)
	}
	if len(store.savedScripts) != 0 {
		t.Fatalf("unsafe fallback write reached storage: %#v", store.savedScripts)
	}

	const reviewSecret = "fallback-review-never-store"
	safe := durableBoundaryScript("fallback-safe", "Bearer {{secret.FALLBACK_TOKEN}}", reviewSecret)
	if err := runner.saveStoredScript(safe); err != nil {
		t.Fatal(err)
	}
	if len(store.savedScripts) != 1 {
		t.Fatalf("fallback writes = %d, want 1", len(store.savedScripts))
	}
	assertAuthoringDraftDisclosureSafe(t, safe, reviewSecret)
	assertAuthoringDraftDisclosureSafe(t, store.savedScripts[0], reviewSecret)
}

func TestBrunoSecretBindingsRequireExactGovernedReferencesAtDurableBoundary(t *testing.T) {
	newScript := func(id, value string) *dsl.Script {
		return &dsl.Script{
			ID: id, Name: "Bruno binding boundary", Version: "1.0.0", Protocol: "bruno", ProjectID: "project-a",
			Config: dsl.ScriptConfig{Bruno: dsl.BrunoConfig{
				BundleRef:      "artifact://sanitized-bundle",
				SecretBindings: map[string]string{"FOO": value},
			}},
			Actions: []dsl.Action{{Name: "Run", Type: "bruno_run", Target: "."}},
		}
	}

	runner := NewTestRunner(1, NewHub(), nil)
	const plaintext = "innocuous-alias-plaintext-password"
	unsafe := newScript("bruno-unsafe", plaintext)
	if _, err := runner.StoreScriptDurable(unsafe); err == nil || !strings.Contains(err.Error(), "require logical secret bindings") {
		t.Fatalf("unsafe Bruno durable create error = %v", err)
	}
	if len(runner.ListScripts()) != 0 {
		t.Fatalf("unsafe Bruno binding reached storage: %#v", runner.ListScripts())
	}
	projected := maskScriptSecrets(unsafe)
	encoded, err := json.Marshal(projected)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), plaintext) || projected.Config.Bruno.SecretBindings["FOO"] != maskedScriptSecret {
		t.Fatalf("Bruno response projection disclosed executable binding: %s", encoded)
	}

	safe, err := runner.StoreScriptDurable(newScript("bruno-safe", "{{secret.BRUNO_FOO}}"))
	if err != nil {
		t.Fatal(err)
	}
	if got := safe.Script.Config.Bruno.SecretBindings["FOO"]; got != "{{secret.BRUNO_FOO}}" {
		t.Fatalf("stored safe Bruno binding = %q", got)
	}

	update := cloneScriptDefinition(safe.Script)
	update.Config.Bruno.SecretBindings["FOO"] = plaintext
	if _, _, err := runner.UpdateScriptDurableInProject(safe.ID, update, safe.ProjectID); err == nil || !strings.Contains(err.Error(), "require logical secret bindings") {
		t.Fatalf("unsafe Bruno durable update error = %v", err)
	}
	unchanged, ok := runner.GetScript(safe.ID)
	if !ok || unchanged.Script.Config.Bruno.SecretBindings["FOO"] != "{{secret.BRUNO_FOO}}" {
		t.Fatalf("blocked update changed durable Bruno binding: %#v", unchanged)
	}
}

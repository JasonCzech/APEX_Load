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

func TestDurableImportedScriptPreservesCredentialAndMasksReads(t *testing.T) {
	const (
		password     = "imported-password-remains-runnable"
		tableSecret  = "short-table-password"
		reviewSecret = "review-only-password-never-store"
	)
	script := durableBoundaryScript("imported-runnable", "Bearer "+password, reviewSecret)
	script.DataSources = []dsl.DataSource{{
		Name:           "credentials",
		Type:           "csv",
		InlineData:     "username,password\nalice," + tableSecret + "\n",
		Strategy:       "sequential",
		FirstRowHeader: true,
	}}
	script.Parameters = []dsl.Parameter{{
		Name:       "Password",
		Type:       "file",
		DataSource: "credentials",
		Column:     "password",
	}}
	script.Actions = append(script.Actions, dsl.Action{
		Name:   "Source review",
		Type:   "source_review",
		Target: "review",
		Body:   "Authorization: Bearer " + reviewSecret,
		Metadata: map[string]any{
			"authorization": "Bearer " + reviewSecret,
		},
	}, dsl.Action{
		Name:   "Required review",
		Type:   "review_required",
		Target: "review",
		Value:  "password=" + reviewSecret,
		Params: map[string]any{
			"password": reviewSecret,
		},
	})
	runner := NewTestRunner(1, NewHub(), nil)
	stored, created, err := runner.CreateImportedScriptDurableReportingReplay(script, "test")
	if err != nil {
		t.Fatalf("store imported script: %v", err)
	}
	if !created {
		t.Fatal("trusted import unexpectedly resolved as an idempotent replay")
	}
	durable := runner.scripts[script.ID]
	if durable == nil || durable.Script == nil {
		t.Fatal("trusted import did not retain a durable execution copy")
	}
	if got := durable.Script.Actions[0].Headers["Authorization"]; got != "Bearer "+password {
		t.Fatalf("durable Authorization = %q, want imported value", got)
	}
	if got := durable.Script.DataSources[0].InlineData; got != "username,password\nalice,"+tableSecret+"\n" {
		t.Fatalf("durable credential table = %q, want imported bytes", got)
	}
	storedPayload, err := json.Marshal(durable.Script)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(storedPayload), reviewSecret) {
		t.Fatalf("durable import retained non-executable review credential: %s", storedPayload)
	}
	if got := durable.Script.Metadata.Import.Preserved["source_review"].(map[string]any)["authorization"]; got != "Bearer "+maskedScriptSecret {
		t.Fatalf("durable source review authorization = %#v", got)
	}
	if got := durable.Script.Actions[1].Metadata["authorization"]; got != "Bearer "+maskedScriptSecret {
		t.Fatalf("durable review action authorization = %#v", got)
	}
	if got := durable.Script.Actions[2].Params["password"]; got != maskedScriptSecret {
		t.Fatalf("durable required-review password = %#v", got)
	}

	if stored == nil || stored.Script == nil ||
		stored.Script.Actions[0].Headers["Authorization"] != "Bearer "+password {
		t.Fatalf("trusted importer lost its executable return value: %#v", stored)
	}
	masked := maskScriptSecrets(stored.Script)
	if masked == nil || masked.Actions[0].Headers["Authorization"] != "Bearer "+maskedScriptSecret {
		t.Fatalf("stored-script response projection was not masked: %#v", masked)
	}
	if got := masked.DataSources[0].InlineData; got != "username,password\nalice,"+maskedScriptSecret+"\n" {
		t.Fatalf("masked imported credential table = %q", got)
	}
	if got := masked.Metadata.Import.Preserved[dsl.ImportPreservedCredentialPolicyKey]; got != dsl.ImportPreservedCredentialPolicyPreserve {
		t.Fatalf("masked import credential policy = %#v", got)
	}
	roundTrip, err := preparePersistentLibraryScript(masked, durable.Script)
	if err != nil {
		t.Fatalf("reconcile masked imported script: %v", err)
	}
	if got := roundTrip.Actions[0].Headers["Authorization"]; got != "Bearer "+password {
		t.Fatalf("round-trip Authorization = %q, want restored imported value", got)
	}
	if got := roundTrip.DataSources[0].InlineData; got != "username,password\nalice,"+tableSecret+"\n" {
		t.Fatalf("round-trip credential table = %q, want restored imported bytes", got)
	}
	if !isCredentialPreservingImport(roundTrip) {
		t.Fatal("round-trip lost the durable credential-preserving import policy")
	}

	metadataOmitted := cloneScriptDefinition(masked)
	metadataOmitted.Metadata = nil
	restoredPolicy, err := preparePersistentLibraryScript(metadataOmitted, durable.Script)
	if err != nil {
		t.Fatalf("reconcile imported script with omitted metadata: %v", err)
	}
	if !isCredentialPreservingImport(restoredPolicy) ||
		restoredPolicy.Metadata.Import.Source != "test" {
		t.Fatalf("durable import provenance was client-erasable: %#v", restoredPolicy.Metadata)
	}
	if got := restoredPolicy.Actions[0].Headers["Authorization"]; got != "Bearer "+password {
		t.Fatalf("omitted-metadata round-trip Authorization = %q", got)
	}

	rawUnchanged := cloneScriptDefinition(durable.Script)
	rawUnchanged.Name = "Raw import draft rename"
	rawRoundTrip, err := preparePersistentLibraryScript(rawUnchanged, durable.Script)
	if err != nil {
		t.Fatalf("retain exact imported credential during ordinary edit: %v", err)
	}
	if got := rawRoundTrip.Actions[0].Headers["Authorization"]; got != "Bearer "+password {
		t.Fatalf("raw ordinary round-trip Authorization = %q", got)
	}

	changedCredential := cloneScriptDefinition(durable.Script)
	changedCredential.Actions[0].Headers["Authorization"] = "Bearer newly-authored-password"
	if _, err := preparePersistentLibraryScript(changedCredential, durable.Script); err == nil ||
		!strings.Contains(err.Error(), "new executable credentials") {
		t.Fatalf("new credential on imported script error = %v", err)
	}

	newCredentialSurface := cloneScriptDefinition(masked)
	newCredentialSurface.Actions[0].Headers["X-API-Key"] = "newly-authored-api-key"
	if _, err := preparePersistentLibraryScript(newCredentialSurface, durable.Script); err == nil {
		t.Fatalf("new credential surface on imported script error = %v", err)
	}
}

func TestImportedRecordedResourceURLIsPreservedDurablyAndMaskedOnRead(t *testing.T) {
	const (
		username = "recorded-user"
		password = "recorded-password"
		token    = "recorded-query-token"
	)
	rawURL := "https://" + username + ":" + password + "@cdn.example.test/app.js?token=" + token + "&variant=modern"
	script := &dsl.Script{
		ID: "recorded-resource", Name: "Recorded resource", Version: "1", Protocol: "browser",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "browser_recording",
			Preserved: map[string]any{
				"recorded_resource_urls": []string{rawURL},
			},
		}},
		Actions: []dsl.Action{{Name: "Navigate", Type: "browser_navigate", Target: "https://example.test"}},
	}
	durable, err := preparePersistentImportedLibraryScript(script, nil, "browser_recording")
	if err != nil {
		t.Fatal(err)
	}
	durableURLs := stringSliceValue(durable.Metadata.Import.Preserved["recorded_resource_urls"])
	if len(durableURLs) != 1 || durableURLs[0] != rawURL {
		t.Fatalf("durable recorded resource URLs = %#v", durable.Metadata.Import.Preserved["recorded_resource_urls"])
	}

	editable := credentialPreservingImportResponseValue(script)
	editablePayload, err := json.Marshal(editable)
	if err != nil {
		t.Fatal(err)
	}
	var editableScript dsl.Script
	if err := json.Unmarshal(editablePayload, &editableScript); err != nil {
		t.Fatal(err)
	}
	editableURLs := stringSliceValue(editableScript.Metadata.Import.Preserved["recorded_resource_urls"])
	if len(editableURLs) != 1 || editableURLs[0] == rawURL {
		t.Fatalf("editable import draft exposed the exact recorded resource URL: %#v", editableURLs)
	}
	for _, credential := range []string{username, password, token} {
		if strings.Contains(editableURLs[0], credential) {
			t.Fatalf("editable import draft leaked %q: %#v", credential, editableURLs)
		}
	}
	if !strings.Contains(editableURLs[0], "variant=modern") ||
		!strings.Contains(editableURLs[0], maskedScriptSecret) {
		t.Fatalf("editable import draft lost safe URL structure: %#v", editableURLs)
	}

	masked := maskScriptSecrets(durable)
	if masked == nil {
		t.Fatal("maskScriptSecrets returned nil")
	}
	maskedPayload, err := json.Marshal(masked.Metadata.Import.Preserved["recorded_resource_urls"])
	if err != nil {
		t.Fatal(err)
	}
	for _, credential := range []string{username, password, token} {
		if strings.Contains(string(maskedPayload), credential) {
			t.Fatalf("masked recorded resource URL leaked %q: %s", credential, maskedPayload)
		}
	}
	if !strings.Contains(string(maskedPayload), "variant=modern") ||
		!strings.Contains(string(maskedPayload), maskedScriptSecret) {
		t.Fatalf("masked recorded resource URL lost safe structure: %s", maskedPayload)
	}
}

func TestForgedImportPreservationMarkerDoesNotBypassOrdinaryPersistence(t *testing.T) {
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
			runner := NewTestRunner(1, NewHub(), nil)
			script := durableBoundaryScript(
				"forged-"+strings.ReplaceAll(test.name, " ", "-"),
				"Bearer forged-import-credential",
				"review",
			)
			markCredentialPreservingImportScript(script)

			err := test.persist(runner, script)
			if err == nil || !strings.Contains(err.Error(), "require logical secret bindings") {
				t.Fatalf("forged marker persistence error = %v", err)
			}
			if len(runner.scripts) != 0 {
				t.Fatalf("forged marker reached durable state: %#v", runner.scripts)
			}
		})
	}
}

func TestTrustedImportPreservesSensitiveJavaVMArgumentsAndMasksReads(t *testing.T) {
	const canary = "process-visible-canary"
	script := durableBoundaryScript(
		"trusted-import-java-argv",
		"Bearer imported-credential",
		"review",
	)
	// Keep the outer protocol HTTP to prove this universal write-side boundary
	// does not depend on Java-vuser catalog validation.
	script.Config.JavaVuser.AdditionalVMParameters = []string{
		"-Xmx512m",
		"-Dpassword=" + canary,
	}
	prepared, err := preparePersistentImportedLibraryScript(script, nil, "jmeter")
	if err != nil {
		t.Fatalf("trusted import Java VM argument error = %v", err)
	}
	if got := prepared.Config.JavaVuser.AdditionalVMParameters; len(got) != 2 ||
		got[1] != "-Dpassword="+canary {
		t.Fatalf("trusted import Java VM arguments = %#v", got)
	}
	masked := maskScriptSecrets(prepared)
	if got := masked.Config.JavaVuser.AdditionalVMParameters; len(got) != 2 ||
		got[1] != maskedScriptSecret {
		t.Fatalf("masked Java VM arguments = %#v", got)
	}

	ordinary := cloneScriptDefinition(script)
	if _, err := preparePersistentLibraryScript(ordinary, nil); err == nil ||
		!strings.Contains(err.Error(), "additional_vm_parameters") {
		t.Fatalf("ordinary Java VM argument error = %v", err)
	}
}

func TestTrustedImportStillRejectsStructurallyUnsafeJavaVMArguments(t *testing.T) {
	script := durableBoundaryScript(
		"trusted-import-java-unsafe-option",
		"Bearer imported-credential",
		"review",
	)
	script.Config.JavaVuser.AdditionalVMParameters = []string{
		"-Xmx512m",
		"-javaagent:/tmp/untrusted.jar",
	}
	if _, err := preparePersistentImportedLibraryScript(script, nil, "jmeter"); err == nil ||
		!strings.Contains(err.Error(), "agent_hook") {
		t.Fatalf("trusted structural Java VM argument error = %v", err)
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

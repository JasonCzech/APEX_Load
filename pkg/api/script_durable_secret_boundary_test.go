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
			runner := NewTestRunner(1, NewHub(), nil)
			const authorization = "Bearer raw-credential-stored-unredacted"
			script := durableBoundaryScript("allowed-"+strings.ReplaceAll(test.name, " ", "-"), authorization, "review")
			if err := test.persist(runner, script); err != nil {
				t.Fatalf("authorization %q error = %v", authorization, err)
			}
			stored, ok := runner.GetScript(script.ID)
			if !ok || stored == nil || stored.Script.Actions[0].Headers["Authorization"] != authorization {
				t.Fatalf("durable create did not persist literal credential: %#v", stored)
			}
		})
	}
}

func TestDurableImportedScriptPreservesCredentialAndReads(t *testing.T) {
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
	if !strings.Contains(string(storedPayload), reviewSecret) {
		t.Fatalf("durable import lost non-executable review credential: %s", storedPayload)
	}
	if got := durable.Script.Metadata.Import.Preserved["source_review"].(map[string]any)["authorization"]; got != "Bearer "+reviewSecret {
		t.Fatalf("durable source review authorization = %#v", got)
	}
	if got := durable.Script.Actions[1].Metadata["authorization"]; got != "Bearer "+reviewSecret {
		t.Fatalf("durable review action authorization = %#v", got)
	}
	if got := durable.Script.Actions[2].Params["password"]; got != reviewSecret {
		t.Fatalf("durable required-review password = %#v", got)
	}

	if stored == nil || stored.Script == nil ||
		stored.Script.Actions[0].Headers["Authorization"] != "Bearer "+password {
		t.Fatalf("trusted importer lost its executable return value: %#v", stored)
	}
	if viewed := viewStoredScript(stored); viewed == nil || viewed.Script == nil ||
		viewed.Script.Actions[0].Headers["Authorization"] != "Bearer "+password ||
		viewed.Script.DataSources[0].InlineData != "username,password\nalice,"+tableSecret+"\n" {
		t.Fatalf("authoring/CRUD viewStoredScript masked imported script: %#v", viewed)
	}
	masked := maskScriptSecrets(stored.Script)
	if masked == nil || masked.Actions[0].Headers["Authorization"] != "Bearer "+maskedScriptSecret {
		t.Fatalf("runtime disclosure projection was not masked: %#v", masked)
	}
	if got := masked.DataSources[0].InlineData; got != "username,password\nalice,"+maskedScriptSecret+"\n" {
		t.Fatalf("masked imported credential table = %q", got)
	}
	if got := masked.Metadata.Import.Preserved[dsl.ImportPreservedCredentialPolicyKey]; got != dsl.ImportPreservedCredentialPolicyPreserve {
		t.Fatalf("masked import credential policy = %#v", got)
	}

	metadataOmitted := cloneScriptDefinition(durable.Script)
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
	updatedCredential, err := preparePersistentLibraryScript(changedCredential, durable.Script)
	if err != nil || updatedCredential.Actions[0].Headers["Authorization"] != "Bearer newly-authored-password" {
		t.Fatalf("new credential on imported script = %#v, err = %v", updatedCredential, err)
	}

	newCredentialSurface := cloneScriptDefinition(durable.Script)
	newCredentialSurface.Actions[0].Headers["X-API-Key"] = "newly-authored-api-key"
	updatedSurface, err := preparePersistentLibraryScript(newCredentialSurface, durable.Script)
	if err != nil || updatedSurface.Actions[0].Headers["X-API-Key"] != "newly-authored-api-key" {
		t.Fatalf("new credential surface on imported script = %#v, err = %v", updatedSurface, err)
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

	editable := newScriptImportResponse(scriptImportResponseOptions{Script: script})["script"]
	editablePayload, err := json.Marshal(editable)
	if err != nil {
		t.Fatal(err)
	}
	var editableScript dsl.Script
	if err := json.Unmarshal(editablePayload, &editableScript); err != nil {
		t.Fatal(err)
	}
	editableURLs := stringSliceValue(editableScript.Metadata.Import.Preserved["recorded_resource_urls"])
	if len(editableURLs) != 1 || editableURLs[0] != rawURL {
		t.Fatalf("editable import draft did not preserve exact recorded resource URL: %#v", editableURLs)
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
				"ordinary-"+strings.ReplaceAll(test.name, " ", "-"),
				"Bearer authored-credential",
				"review",
			)
			markCredentialPreservingImportScript(script)

			if err := test.persist(runner, script); err != nil {
				t.Fatalf("ordinary persistence error = %v", err)
			}
			if len(runner.scripts) != 1 {
				t.Fatalf("ordinary persistence state = %#v", runner.scripts)
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
	ordinaryPrepared, err := preparePersistentLibraryScript(ordinary, nil)
	if err != nil {
		t.Fatalf("ordinary Java VM argument error = %v", err)
	}
	if got := ordinaryPrepared.Config.JavaVuser.AdditionalVMParameters; len(got) != 2 || got[1] != "-Dpassword="+canary {
		t.Fatalf("ordinary Java VM arguments = %#v", got)
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
	const createReviewSecret = "durable-create-review-preserved"
	stored, err := runner.StoreScriptDurable(durableBoundaryScript(
		"durable-safe", "Bearer {{secret.LOGIN_TOKEN}}", createReviewSecret,
	))
	if err != nil {
		t.Fatal(err)
	}
	if got := stored.Script.Metadata.Import.Preserved["source_review"].(map[string]any)["authorization"]; got != "Bearer "+createReviewSecret {
		t.Fatalf("durable create scrubbed source_review: %#v", got)
	}

	const updateCredential = "durable-update-credential-preserved"
	directUpdate := cloneScriptDefinition(stored.Script)
	directUpdate.Actions[0].Headers["Authorization"] = "Bearer " + updateCredential
	updated, ok, err := runner.UpdateScriptDurableInProject(stored.ID, directUpdate, stored.ProjectID)
	if err != nil || !ok {
		t.Fatalf("durable update = %#v, ok=%t, err=%v", updated, ok, err)
	}
	if got := updated.Script.Actions[0].Headers["Authorization"]; got != "Bearer "+updateCredential {
		t.Fatalf("durable update Authorization = %q", got)
	}
	maskedUpdated := maskScriptSecrets(updated.Script)
	if maskedJSON, _ := json.Marshal(maskedUpdated); strings.Contains(string(maskedJSON), createReviewSecret) || strings.Contains(string(maskedJSON), updateCredential) {
		t.Fatalf("runtime disclosure leaked secret: %s", maskedJSON)
	}

	const updateReviewSecret = "durable-update-review-preserved"
	safeUpdate := cloneScriptDefinition(updated.Script)
	safeUpdate.Metadata.Import.Preserved["source_review"] = map[string]any{"authorization": "Bearer " + updateReviewSecret}
	safeUpdate.Actions[0].Headers["Authorization"] = "Bearer {{secret.ROTATED_TOKEN}}"
	updated, ok, err = runner.UpdateScriptDurableInProject(stored.ID, safeUpdate, stored.ProjectID)
	if err != nil || !ok {
		t.Fatalf("safe durable update = %#v, ok=%t, err=%v", updated, ok, err)
	}
	if got := updated.Script.Metadata.Import.Preserved["source_review"].(map[string]any)["authorization"]; got != "Bearer "+updateReviewSecret {
		t.Fatalf("durable update scrubbed source_review: %#v", got)
	}
	if got := updated.Script.Actions[0].Headers["Authorization"]; got != "Bearer {{secret.ROTATED_TOKEN}}" {
		t.Fatalf("safe update Authorization = %q", got)
	}
	assertAuthoringDraftDisclosureSafe(t, maskScriptSecrets(updated.Script), createReviewSecret, updateCredential, updateReviewSecret)
}

func TestSaveStoredScriptFallbackEnforcesSecretBoundary(t *testing.T) {
	store := &metricsStoreSpy{}
	runner := NewTestRunner(1, NewHub(), store)

	literal := durableBoundaryScript("fallback-literal", "Bearer raw-fallback-secret", "review")
	if err := runner.saveStoredScript(literal); err != nil {
		t.Fatalf("literal fallback write error = %v", err)
	}
	if len(store.savedScripts) != 1 || store.savedScripts[0].Actions[0].Headers["Authorization"] != "Bearer raw-fallback-secret" {
		t.Fatalf("literal fallback write did not persist verbatim: %#v", store.savedScripts)
	}

	const reviewSecret = "fallback-review-preserved"
	safe := durableBoundaryScript("fallback-safe", "Bearer {{secret.FALLBACK_TOKEN}}", reviewSecret)
	if err := runner.saveStoredScript(safe); err != nil {
		t.Fatal(err)
	}
	if len(store.savedScripts) != 2 {
		t.Fatalf("fallback writes = %d, want 2", len(store.savedScripts))
	}
	assertAuthoringDraftDisclosureSafe(t, maskScriptSecrets(store.savedScripts[1]), reviewSecret)
}

func TestBrunoRunnerRetirementPrecedesSecretBindingsAtDurableBoundary(t *testing.T) {
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
	if _, err := runner.StoreScriptDurable(unsafe); err == nil || !strings.Contains(err.Error(), "Bruno Runner has been retired") {
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

	if _, err := runner.StoreScriptDurable(newScript("bruno-safe", "{{secret.BRUNO_FOO}}")); err == nil || !strings.Contains(err.Error(), "Bruno Runner has been retired") {
		t.Fatalf("governed binding cannot restore the retired runtime: %v", err)
	}
	if len(runner.ListScripts()) != 0 {
		t.Fatal("retired runner reached durable storage")
	}

}

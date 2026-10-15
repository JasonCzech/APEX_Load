package api

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

func importedCredentialContextScript(t *testing.T, script *dsl.Script) *dsl.Script {
	t.Helper()
	if script.ID == "" {
		script.ID = "credential-context"
	}
	if script.Name == "" {
		script.Name = "Credential context"
	}
	if script.Version == "" {
		script.Version = "1"
	}
	if script.Protocol == "" {
		script.Protocol = "http"
	}
	if script.ProjectID == "" {
		script.ProjectID = "project-a"
	}
	persisted, err := preparePersistentImportedLibraryScript(script, nil, "test")
	if err != nil {
		t.Fatalf("prepare imported script: %v", err)
	}
	return persisted
}

func TestTrustedImportStampsServerSelectedSourceProfile(t *testing.T) {
	t.Parallel()
	base := &dsl.Script{
		ID: "source-stamp", Name: "Source stamp", Version: "1",
		Protocol: "http", ProjectID: "project-a",
		Config: dsl.ScriptConfig{BaseURL: "https://example.test"},
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "enterprise_script_archive",
		}},
		Actions: []dsl.Action{{
			Name: "request", Type: "request", Method: http.MethodGet, Target: "/",
		}},
	}
	created, err := preparePersistentImportedLibraryScript(base, nil, "postman")
	if err != nil {
		t.Fatalf("create trusted import: %v", err)
	}
	if got := created.Metadata.Import.Source; got != "postman" {
		t.Fatalf("created source = %q, want server profile", got)
	}

	replacement := cloneScriptDefinition(created)
	replacement.Metadata.Import.Source = "ai_generated"
	replaced, err := preparePersistentImportedLibraryScript(
		replacement,
		created,
		"openapi",
	)
	if err != nil {
		t.Fatalf("replace trusted import: %v", err)
	}
	if got := replaced.Metadata.Import.Source; got != "openapi" {
		t.Fatalf("replaced source = %q, want server profile", got)
	}
}

func TestPreservedImportedCredentialsCannotBeRedirected(t *testing.T) {
	const credential = "retained-import-password"
	tests := []struct {
		name        string
		script      *dsl.Script
		mutate      func(*dsl.Script)
		wantContext string
		useMasked   bool
	}{
		{
			name: "masked authorization cannot follow a changed base URL",
			script: &dsl.Script{
				Config: dsl.ScriptConfig{BaseURL: "https://origin.example.test"},
				Actions: []dsl.Action{{
					Name: "request", Type: "request", Method: http.MethodGet, Target: "/private",
					Headers: map[string]string{"Authorization": "Bearer " + credential},
				}},
			},
			mutate: func(script *dsl.Script) {
				script.Config.BaseURL = "https://redirect.example.test"
			},
			wantContext: "actions[0]",
			useMasked:   true,
		},
		{
			name: "basic password cannot follow a changed base URL",
			script: &dsl.Script{
				Config: dsl.ScriptConfig{
					BaseURL: "https://origin.example.test",
					HTTP: dsl.HTTPConfig{Auth: dsl.AuthConfig{
						Type: "basic", Username: "operator", Password: credential,
					}},
				},
				Actions: []dsl.Action{{
					Name: "request", Type: "request", Method: http.MethodGet, Target: "/private",
				}},
			},
			mutate: func(script *dsl.Script) {
				script.Config.BaseURL = "https://redirect.example.test"
			},
			wantContext: "script",
			useMasked:   true,
		},
		{
			name: "proxy password cannot move to a different proxy",
			script: &dsl.Script{
				Config: dsl.ScriptConfig{
					BaseURL: "https://origin.example.test",
					HTTP: dsl.HTTPConfig{
						ProxyMode: "custom",
						Proxy: dsl.ProxyConfig{
							URL: "https://proxy-one.example.test", Username: "operator", Password: credential,
						},
					},
				},
				Actions: []dsl.Action{{
					Name: "request", Type: "request", Method: http.MethodGet, Target: "/private",
				}},
			},
			mutate: func(script *dsl.Script) {
				script.Config.HTTP.Proxy.URL = "https://proxy-two.example.test"
			},
			wantContext: "script",
			useMasked:   true,
		},
		{
			name: "body credential cannot move to a different request target",
			script: &dsl.Script{
				Config: dsl.ScriptConfig{BaseURL: "https://origin.example.test"},
				Actions: []dsl.Action{{
					Name: "request", Type: "request", Method: http.MethodPost, Target: "/session",
					Headers: map[string]string{"Content-Type": "application/json"},
					Body:    `{"password":"` + credential + `"}`,
				}},
			},
			mutate: func(script *dsl.Script) {
				script.Actions[0].Target = "https://redirect.example.test/session"
			},
			wantContext: "actions[0]",
		},
		{
			name: "nested control request credential stays in its child",
			script: &dsl.Script{
				Config: dsl.ScriptConfig{BaseURL: "https://origin.example.test"},
				Actions: []dsl.Action{{
					Name: "branch", Type: "control_if", Target: "branch",
					Control: &dsl.ControlFlow{Steps: []dsl.Action{{
						Name: "request", Type: "request", Method: http.MethodGet, Target: "/nested",
						Headers: map[string]string{"Authorization": "Bearer " + credential},
					}}},
				}},
			},
			mutate: func(script *dsl.Script) {
				script.Actions[0].Control.Steps[0].Target = "https://redirect.example.test/nested"
			},
			wantContext: "actions[0].control.steps[0]",
		},
		{
			name: "HTTP sequence child credential stays in its child",
			script: &dsl.Script{
				Config: dsl.ScriptConfig{BaseURL: "https://origin.example.test"},
				Actions: []dsl.Action{{
					Name: "sequence", Type: "http_sequence", Method: "SEQUENCE", Target: "sequence",
					Params: map[string]any{"requests": []any{map[string]any{
						"name": "request", "type": "request", "method": http.MethodGet, "target": "/sequence",
						"headers": map[string]any{"Authorization": "Bearer " + credential},
					}}},
				}},
			},
			mutate: func(script *dsl.Script) {
				requests := script.Actions[0].Params["requests"].([]any)
				requests[0].(map[string]any)["target"] = "https://redirect.example.test/sequence"
			},
			wantContext: "actions[0].params.requests[0]",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prior := importedCredentialContextScript(t, test.script)
			candidate := cloneScriptDefinition(prior)
			if test.useMasked {
				candidate = maskScriptSecrets(prior)
			}
			test.mutate(candidate)

			_, err := preparePersistentLibraryScript(candidate, prior)
			if err == nil {
				t.Fatal("redirected retained credential was accepted")
			}
			if !strings.Contains(err.Error(), "execution context for retained imported credentials") ||
				!strings.Contains(err.Error(), test.wantContext) {
				t.Fatalf("redirect error = %v, want disclosure-safe context %q", err, test.wantContext)
			}
			if strings.Contains(err.Error(), credential) {
				t.Fatalf("redirect error disclosed the credential: %v", err)
			}
		})
	}
}

func TestPreservedImportedCredentialContextAllowsSafeEditsAndGovernedRebinding(t *testing.T) {
	const credential = "retained-import-password"
	prior := importedCredentialContextScript(t, &dsl.Script{
		Config: dsl.ScriptConfig{
			BaseURL:   "https://origin.example.test",
			ThinkTime: dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 100},
		},
		Actions: []dsl.Action{
			{
				Name: "credential request", Type: "request", Method: http.MethodGet, Target: "/private",
				Headers:    map[string]string{"Authorization": "Bearer " + credential},
				Assertions: []dsl.Assertion{{Type: "status", Expected: 200}},
			},
			{
				Name: "public request", Type: "request", Method: http.MethodGet, Target: "/public",
			},
		},
	})

	ordinaryEdit := maskScriptSecrets(prior)
	ordinaryEdit.Name = "Renamed script"
	ordinaryEdit.Metadata.Import.Source = "forged-source-profile"
	ordinaryEdit.Config.ThinkTime.MeanMs = 250
	ordinaryEdit.Actions[0].Name = "Renamed credential request"
	ordinaryEdit.Actions[0].Assertions[0].Expected = 204
	persisted, err := preparePersistentLibraryScript(ordinaryEdit, prior)
	if err != nil {
		t.Fatalf("safe masked non-credential edit: %v", err)
	}
	if got := persisted.Actions[0].Headers["Authorization"]; got != "Bearer "+credential {
		t.Fatalf("safe edit changed retained Authorization = %q", got)
	}
	if got := persisted.Metadata.Import.Source; got != prior.Metadata.Import.Source {
		t.Fatalf("ordinary edit changed server-owned import source = %q", got)
	}

	unrelatedWireEdit := cloneScriptDefinition(prior)
	unrelatedWireEdit.Actions[1].Target = "/new-public-target"
	if _, err := preparePersistentLibraryScript(unrelatedWireEdit, prior); err != nil {
		t.Fatalf("unrelated request edit borrowed no credential: %v", err)
	}

	governed := cloneScriptDefinition(prior)
	governed.Config.BaseURL = "https://new-destination.example.test"
	governed.Actions[0].Target = "/new-private-target"
	governed.Actions[0].Headers["Authorization"] = "Bearer {{secret.IMPORTED_TOKEN}}"
	persisted, err = preparePersistentLibraryScript(governed, prior)
	if err != nil {
		t.Fatalf("governed rebinding to a new destination: %v", err)
	}
	if got := persisted.Actions[0].Headers["Authorization"]; got != "Bearer {{secret.IMPORTED_TOKEN}}" {
		t.Fatalf("governed Authorization = %q", got)
	}

	trusted := cloneScriptDefinition(prior)
	trusted.Config.BaseURL = "https://trusted-reimport.example.test"
	persisted, err = preparePersistentImportedLibraryScript(trusted, prior, "test")
	if err != nil {
		t.Fatalf("trusted reimport changed destination: %v", err)
	}
	if got := persisted.Actions[0].Headers["Authorization"]; got != "Bearer "+credential {
		t.Fatalf("trusted reimport Authorization = %q", got)
	}
}

func TestDurableImportedCredentialRedirectIsRejectedBeforeMutation(t *testing.T) {
	const credential = "durable-retained-password"
	runner := NewTestRunner(1, NewHub(), nil)
	source := &dsl.Script{
		ID: "durable-context-fence", Name: "Durable context fence", Version: "1",
		Protocol: "http", ProjectID: "project-a",
		Config: dsl.ScriptConfig{BaseURL: "https://origin.example.test"},
		Actions: []dsl.Action{{
			Name: "request", Type: "request", Method: http.MethodGet, Target: "/private",
			Headers: map[string]string{"Authorization": "Bearer " + credential},
		}},
	}
	if _, _, err := runner.CreateImportedScriptDurableReportingReplay(source, "test"); err != nil {
		t.Fatalf("create imported script: %v", err)
	}
	prior := runner.scripts[source.ID].Script
	update := maskScriptSecrets(prior)
	update.Config.BaseURL = "https://redirect.example.test"
	if _, _, err := runner.UpdateScriptDurableInProject(source.ID, update, "project-a"); err == nil ||
		!strings.Contains(err.Error(), "execution context for retained imported credentials") {
		t.Fatalf("redirected durable update error = %v", err)
	}
	current := runner.scripts[source.ID].Script
	if current.Config.BaseURL != "https://origin.example.test" ||
		current.Actions[0].Headers["Authorization"] != "Bearer "+credential {
		t.Fatalf("rejected update mutated durable script: %#v", current)
	}
}

func TestMaskedUpdateCannotRemoveImportedCredentialRuntimeRedactionLocators(t *testing.T) {
	const credentialValue = "7319"
	source := &dsl.Script{
		ID: "credential-locator-fence", Name: "Credential locator fence",
		Version: "1", Protocol: "http", ProjectID: "project-a",
		Config: dsl.ScriptConfig{BaseURL: "https://origin.example.test"},
		Actions: []dsl.Action{{
			Name: "submit", Type: "request", Method: http.MethodPost, Target: "/pin",
			Headers: map[string]string{"Content-Type": "application/json"},
			Body:    `{"password":` + credentialValue + `}`,
			Metadata: map[string]any{
				dsl.ImportPreservedCredentialTargetsMetadataKey: []credentialpolicy.TargetRef{{
					RequestKey: "actions[0]",
					Field:      "body",
					Pointer:    "/password",
				}},
			},
		}},
	}
	prior := importedCredentialContextScript(t, source)
	masked := maskScriptSecrets(prior)
	if masked == nil || masked.Actions[0].Body != maskedScriptSecret {
		t.Fatalf("masked typed credential body = %#v", masked)
	}
	delete(
		masked.Actions[0].Metadata,
		dsl.ImportPreservedCredentialTargetsMetadataKey,
	)

	persisted, err := preparePersistentLibraryScript(masked, prior)
	if err != nil {
		t.Fatalf("masked update: %v", err)
	}
	if _, restored := persisted.Actions[0].Metadata[dsl.ImportPreservedCredentialTargetsMetadataKey]; !restored {
		t.Fatal("ordinary update removed server-owned credential locator metadata")
	}
	values, err := engine.ImportedCredentialValues(persisted)
	if err != nil {
		t.Fatalf("runtime credential redaction values: %v", err)
	}
	if len(values) != 1 || values[0] != credentialValue {
		t.Fatalf("runtime credential redaction values = %#v, want %q", values, credentialValue)
	}
}

func TestMaskedOpenAPIPathUpdateRetainsSelfContainedCredentialLocator(t *testing.T) {
	const credentialValue = "path-password-7319"
	segment := "pre-" + credentialValue + "-post"
	start := len("pre-")
	end := start + len(credentialValue)
	shape := []byte(segment)
	for index := start; index < end; index++ {
		shape[index] = 0
	}
	shapeHash := sha256.Sum256(shape)
	pointer := fmt.Sprintf(
		"openapi-path-v1:cGFzc3dvcmQ:%d:%x:%d-%d:%d-%d",
		len(segment),
		shapeHash,
		start,
		end,
		start,
		end,
	)
	source := &dsl.Script{
		ID: "openapi-path-locator-fence", Name: "OpenAPI path locator fence",
		Version: "1", Protocol: "http", ProjectID: "project-a",
		Config: dsl.ScriptConfig{BaseURL: "https://origin.example.test"},
		Actions: []dsl.Action{{
			Name: "request", Type: "request", Method: http.MethodGet,
			Target: "/auth/" + segment,
			Metadata: map[string]any{
				"openapi": map[string]any{
					"path": "/auth/pre-{password}-post",
				},
				dsl.ImportPreservedCredentialTargetsMetadataKey: []map[string]any{{
					"field":   "url.path",
					"index":   2,
					"pointer": pointer,
				}},
			},
		}},
	}
	prior := importedCredentialContextScript(t, source)
	masked := maskScriptSecrets(prior)
	if masked == nil {
		t.Fatal("maskScriptSecrets returned nil")
	}
	delete(masked.Actions[0].Metadata, "openapi")

	persisted, err := preparePersistentLibraryScript(masked, prior)
	if err != nil {
		t.Fatalf("masked update without optional OpenAPI metadata: %v", err)
	}
	if got := persisted.Actions[0].Target; got != source.Actions[0].Target {
		t.Fatalf("persisted target = %q, want exact executable %q", got, source.Actions[0].Target)
	}
	if _, restored := persisted.Actions[0].Metadata[dsl.ImportPreservedCredentialTargetsMetadataKey]; !restored {
		t.Fatal("ordinary update removed the self-contained OpenAPI credential locator")
	}
	if _, restored := persisted.Actions[0].Metadata["openapi"]; restored {
		t.Fatal("optional OpenAPI source metadata was unexpectedly recreated")
	}
	values, err := engine.ImportedCredentialValues(persisted)
	if err != nil {
		t.Fatalf("runtime credential redaction values: %v", err)
	}
	if !containsString(values, credentialValue) {
		t.Fatalf("runtime credential values = %#v, want path component %q", values, credentialValue)
	}
	redaction, err := engine.WithSecretValueRedactionChecked(nil, values)
	if err != nil {
		t.Fatalf("construct runtime redaction matcher: %v", err)
	}
	if got := redaction.ScrubText("echo=" + credentialValue); strings.Contains(got, credentialValue) {
		t.Fatalf("component-only response echo escaped redaction: %q", got)
	}
}

func TestMaskedUpdateCannotRemoveRawImportArtifactDisclosureMarker(t *testing.T) {
	const rawBundleRef = "artifact://raw/source-object"
	source := &dsl.Script{
		ID: "raw-artifact-marker-fence", Name: "Raw artifact marker fence",
		Version: "1", Protocol: "bruno", ProjectID: "project-a",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "bruno",
			Preserved: map[string]any{
				dsl.ImportPreservedRuntimeArtifactKey: "raw",
			},
		}},
		Config: dsl.ScriptConfig{Bruno: dsl.BrunoConfig{
			BundleRef:      rawBundleRef,
			BundleSHA256:   strings.Repeat("a", 64),
			ManifestSHA256: strings.Repeat("b", 64),
		}},
		Actions: []dsl.Action{{Name: "run", Type: "bruno_run", Target: "."}},
	}
	prior := importedCredentialContextScript(t, source)
	masked := maskScriptSecrets(prior)
	if masked == nil || masked.Config.Bruno.BundleRef != maskedScriptSecret {
		t.Fatalf("raw artifact disclosure projection = %#v", masked)
	}
	delete(
		masked.Metadata.Import.Preserved,
		dsl.ImportPreservedRuntimeArtifactKey,
	)

	persisted, err := preparePersistentLibraryScript(masked, prior)
	if err != nil {
		t.Fatalf("masked raw artifact update: %v", err)
	}
	if got := persisted.Metadata.Import.Preserved[dsl.ImportPreservedRuntimeArtifactKey]; got != "raw" {
		t.Fatalf("runtime artifact marker = %#v, want raw", got)
	}
	if persisted.Config.Bruno.BundleRef != rawBundleRef {
		t.Fatalf("durable raw bundle ref = %q", persisted.Config.Bruno.BundleRef)
	}
	projected := maskScriptSecrets(persisted)
	if projected == nil || projected.Config.Bruno.BundleRef != maskedScriptSecret {
		t.Fatalf("later read exposed raw bundle reference: %#v", projected)
	}
}

func TestRecordedProvenanceDoesNotLockParameterEdits(t *testing.T) {
	prior := &dsl.Script{Protocol: "browser", Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Source: "browser_recorder", Preserved: map[string]any{"recorder_session_id": "rec_1789860341606891000"}}}, Actions: []dsl.Action{{Name: "Customer", Type: "browser_fill", Selector: "#customer", Value: "captured"}}}
	candidate := cloneScriptDefinition(prior)
	candidate.Parameters = []dsl.Parameter{{Name: "customer", Type: "list", Values: []string{"new-customer"}}}
	candidate.Actions[0].Value = "{{param.customer}}"
	if err := validatePreservedImportCredentialEdits(candidate, prior); err != nil {
		t.Fatal(err)
	}
	if prior.Metadata.Import.Preserved["recorder_session_id"] == nil || candidate.Metadata.Import.Preserved["recorder_session_id"] == nil {
		t.Fatal("validation mutated provenance")
	}
	prior.Config.Browser.HTTPCredentials.Password = "real-credential"
	candidate.Config.Browser.HTTPCredentials.Password = "real-credential"
	if err := validatePreservedImportCredentialEdits(candidate, prior); err == nil {
		t.Fatal("credential-bearing transport context was allowed to change")
	}
}

// An OpenAPI import's commit fence is masked on disclosure like a credential,
// but it is provenance no request sends: editing the base URL of a script with
// no credentials is an ordinary edit, while a retained credential still cannot
// follow a changed base URL.
func TestImportCommitFenceDoesNotLockBaseURLEdits(t *testing.T) {
	fence := protectedImportSourceCommitFence{
		Version: protectedImportSourceCommitFenceVersion, OwnerDigest: strings.Repeat("a", 64), SourceProfile: "openapi",
		SourceSHA256: strings.Repeat("b", 64), SourceSizeBytes: 221, ScriptFingerprint: strings.Repeat("c", 64),
	}
	prior := &dsl.Script{
		Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "http://127.0.0.1:60557"},
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Source: "openapi", Preserved: map[string]any{}}},
		Actions:  []dsl.Action{{Name: "listOrders", Type: "request", Method: http.MethodGet, Target: "/orders"}},
	}
	attachProtectedImportSourceCommitFence(prior, fence)
	if reflect.DeepEqual(maskScriptSecrets(prior).Metadata.Import.Preserved, prior.Metadata.Import.Preserved) {
		t.Fatal("the commit fence is no longer masked on disclosure; this test no longer covers it")
	}
	candidate := cloneScriptDefinition(prior)
	candidate.Config.BaseURL = "http://127.0.0.1:60557/"
	if err := validatePreservedImportCredentialEdits(candidate, prior); err != nil {
		t.Fatalf("base URL edit of a credential-free import was refused: %v", err)
	}
	if _, kept := prior.Metadata.Import.Preserved[storage.ImportSourceCommitFenceMetadataKey]; !kept {
		t.Fatal("validation mutated provenance")
	}
	prior.Actions[0].Headers = map[string]string{"Authorization": "Bearer retained-import-token"}
	candidate.Actions[0].Headers = map[string]string{"Authorization": "Bearer retained-import-token"}
	candidate.Config.BaseURL = "https://redirect.example.test"
	if err := validatePreservedImportCredentialEdits(candidate, prior); err == nil {
		t.Fatal("a retained credential was allowed to follow a changed base URL")
	}
}

func TestImportedCredentialsPermitReplayEvidenceCapture(t *testing.T) {
	prior := &dsl.Script{Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://example.test"}, Actions: []dsl.Action{{Type: "request", Method: "GET", Target: "/", Headers: map[string]string{"Authorization": "Bearer synthetic-credential"}}}}
	candidate := cloneScriptDefinition(prior)
	// Diagnostic capture changes retained evidence, never request routing.
	candidate.Config.Results.MessageDetail = &dsl.MessageDetailConfig{Mode: "sampled", SampleRate: 1}
	if err := validatePreservedImportCredentialEdits(candidate, prior); err != nil {
		t.Fatal(err)
	}
	candidate.Config.BaseURL = "https://elsewhere.test"
	if err := validatePreservedImportCredentialEdits(candidate, prior); err == nil {
		t.Fatal("credential destination change was allowed")
	}
}

// A cookie is session state the source recorded, not a retained credential:
// editing it, or the request that sends it, is an ordinary edit.
func TestPreservedImportedCookieEditsAreOrdinaryEdits(t *testing.T) {
	prior := importedCredentialContextScript(t, &dsl.Script{
		Config: dsl.ScriptConfig{BaseURL: "https://origin.example.test"},
		Actions: []dsl.Action{{
			Name: "request", Type: "request", Method: http.MethodGet, Target: "/account",
			Headers: map[string]string{"Cookie": "session=recorded-cookie-4821"},
		}},
	})
	candidate := cloneScriptDefinition(prior)
	candidate.Actions[0].Target = "https://other.example.test/account"
	candidate.Actions[0].Headers["Cookie"] = "session=edited-cookie-9173"
	if _, err := preparePersistentLibraryScript(candidate, prior); err != nil {
		t.Fatalf("cookie edit refused: %v", err)
	}
}

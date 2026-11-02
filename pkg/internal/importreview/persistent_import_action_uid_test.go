package importreview

import (
	"net/http"
	"slices"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestPreparePersistentLibraryScriptAllowsActionDeletionWithRetainedCredentials(t *testing.T) {
	rawPrior := &dsl.Script{
		ID:       "sfdc-har-1",
		Name:     "Salesforce HAR Import",
		Version:  "1.0.0",
		Protocol: "http",
		Config: dsl.ScriptConfig{
			BaseURL: "https://org.my.salesforce.com",
		},
		Actions: []dsl.Action{
			{
				Name:   "01 - GET login",
				Type:   "request",
				Method: http.MethodGet,
				Target: "/",
				Metadata: map[string]any{
					"har.entry_index": 1,
				},
			},
			{
				Name:   "94 - POST aura",
				Type:   "request",
				Method: http.MethodPost,
				Target: "/aura?r=19",
				Metadata: map[string]any{
					"har.entry_index":    94,
					"execution_blocker":  "har_request_body_not_exported",
					"execution_blockers": []string{"har_request_body_not_exported"},
				},
			},
			{
				Name:   "97 - POST aura",
				Type:   "request",
				Method: http.MethodPost,
				Target: "/aura?r=22",
				Headers: map[string]string{
					"Authorization": "Bearer sfdc-live-retained-session-token-xyz",
				},
				HeaderList: []dsl.HeaderEntry{
					{Name: "Authorization", Value: "Bearer sfdc-live-retained-session-token-xyz"},
				},
				Metadata: map[string]any{
					"har.entry_index": 97,
					dsl.ImportPreservedCredentialTargetsMetadataKey: []credentialpolicy.TargetRef{
						{RequestKey: "actions[2]", Field: "header_list", Index: 0},
					},
				},
			},
		},
	}

	EnsureScriptImportActionUIDs(rawPrior)
	persistedPrior, err := PreparePersistentImportedLibraryScript(rawPrior, nil, "har")
	if err != nil {
		t.Fatalf("PreparePersistentImportedLibraryScript: %v", err)
	}
	for i, action := range persistedPrior.Actions {
		if !dsl.IsValidImportActionUID(action.Metadata[dsl.ImportActionUIDMetadataKey]) {
			t.Fatalf("action %d missing valid import_action_uid: %#v", i, action.Metadata)
		}
	}

	t.Run("deleting blocked action preserves later action credentials and locators", func(t *testing.T) {
		projected := MaskScriptSecrets(persistedPrior)
		if projected == nil {
			t.Fatal("MaskScriptSecrets returned nil")
		}
		// Delete middle blocked step (Step 94, index 1), shifting index 2 -> index 1.
		projected.Actions = append([]dsl.Action{projected.Actions[0]}, projected.Actions[2:]...)

		saved, err := PreparePersistentLibraryScript(projected, persistedPrior)
		if err != nil {
			t.Fatalf("deleting blocked action before retained-credential action failed: %v", err)
		}
		if len(saved.Actions) != 2 {
			t.Fatalf("saved actions len = %d, want 2", len(saved.Actions))
		}
		if got := saved.Actions[1].Headers["Authorization"]; got != "Bearer sfdc-live-retained-session-token-xyz" {
			t.Fatalf("shifted action Authorization = %q, want durable raw credential restored", got)
		}
		locators, ok := saved.Actions[1].Metadata[dsl.ImportPreservedCredentialTargetsMetadataKey]
		if !ok || locators == nil {
			t.Fatalf("shifted action lost import_credential_targets: %#v", saved.Actions[1].Metadata)
		}
		if locatorItems, ok := locators.([]any); !ok || len(locatorItems) != 1 {
			t.Fatalf("unexpected locator items: %#v", locators)
		} else if locatorMap, ok := locatorItems[0].(map[string]any); !ok || locatorMap["request_key"] != "actions[1]" {
			t.Fatalf("expected shifted locator request_key=actions[1], got %#v", locatorItems[0])
		}
		if _, leaked := saved.Actions[0].Metadata[dsl.ImportPreservedCredentialTargetsMetadataKey]; leaked {
			t.Fatalf("action 0 unexpectedly received import_credential_targets: %#v", saved.Actions[0].Metadata)
		}
	})

	t.Run("inserting non-credential action before retained-credential action succeeds", func(t *testing.T) {
		projected := MaskScriptSecrets(persistedPrior)
		inserted := dsl.Action{
			Name:   "Think pause",
			Type:   "think_time",
			Params: map[string]any{"duration_ms": 500},
		}
		projected.Actions = []dsl.Action{
			projected.Actions[0],
			inserted,
			projected.Actions[2],
		}
		saved, err := PreparePersistentLibraryScript(projected, persistedPrior)
		if err != nil {
			t.Fatalf("inserting non-credential action failed: %v", err)
		}
		if got := saved.Actions[2].Headers["Authorization"]; got != "Bearer sfdc-live-retained-session-token-xyz" {
			t.Fatalf("shifted action Authorization = %q, want durable raw credential restored", got)
		}
	})

	t.Run("duplicating retained-credential action is rejected fail-closed", func(t *testing.T) {
		projected := MaskScriptSecrets(persistedPrior)
		projected.Actions = append(projected.Actions, projected.Actions[2])
		if _, err := PreparePersistentLibraryScript(projected, persistedPrior); err == nil {
			t.Fatal("expected error when duplicating a retained-credential action, got nil")
		}
	})

	t.Run("redirecting retained-credential action target after deletion is rejected", func(t *testing.T) {
		projected := MaskScriptSecrets(persistedPrior)
		projected.Actions = []dsl.Action{projected.Actions[0], projected.Actions[2]}
		projected.Actions[1].Target = "https://attacker.example.test/collect"
		if _, err := PreparePersistentLibraryScript(projected, persistedPrior); err == nil {
			t.Fatal("expected error when redirecting retained-credential action target, got nil")
		}
	})
}

func TestDiscoverSecretBindingRequirementsExcludesNestedHTTPProvenanceMetadata(t *testing.T) {
	script := &dsl.Script{
		ID:       "sfdc-provenance",
		Name:     "SFDC Provenance",
		Version:  "1.0.0",
		Protocol: "http",
		Actions: []dsl.Action{
			{
				Name:   "05 - GET frontdoor",
				Type:   "request",
				Method: http.MethodGet,
				Target: "/secur/frontdoor.jsp?sid={{var.corr_salesforce_frontdoor_sid}}",
				Metadata: map[string]any{
					"salesforce": map[string]any{
						"aura_descriptor": "serviceComponent://ui.force.components.controllers.hostConfig.HostConfigController/ACTION$" + MaskedScriptSecret,
						"aura_page_uri":   "/lightning/page/home?token=" + MaskedScriptSecret,
					},
					"source_refs": []any{
						map[string]any{"pointer": "/log/entries/4", "note": MaskedScriptSecret},
					},
				},
			},
		},
	}

	reqs := DiscoverSecretBindingRequirements(script, 0)
	if len(reqs) != 0 {
		t.Fatalf("expected 0 secret binding requirements for nested HTTP provenance metadata, got %d: %#v", len(reqs), reqs)
	}

	// Ensure non-credential-preserving save AND subsequent update both succeed without orphan sentinel errors.
	saved, err := PreparePersistentLibraryScript(script, nil)
	if err != nil {
		t.Fatalf("initial non-credential-preserving save failed: %v", err)
	}
	edited := CloneScriptDefinition(saved)
	edited.Name = "SFDC Provenance Edited"
	if _, err := PreparePersistentLibraryScript(edited, saved); err != nil {
		t.Fatalf("subsequent update with redacted provenance metadata failed: %v", err)
	}

	// Messaging protocols still treat custom action metadata as wire properties.
	kafkaScript := &dsl.Script{
		ID:       "kafka-wire-meta",
		Name:     "Kafka Wire Metadata",
		Version:  "1.0.0",
		Protocol: "kafka",
		Actions: []dsl.Action{
			{
				Name:   "produce",
				Type:   "kafka_produce",
				Target: "orders",
				Metadata: map[string]any{
					"salesforce": MaskedScriptSecret,
				},
			},
		},
	}
	if kafkaReqs := DiscoverSecretBindingRequirements(kafkaScript, 0); len(kafkaReqs) != 1 {
		t.Fatalf("expected Kafka wire metadata to still require 1 secret binding, got %d", len(kafkaReqs))
	}
}

func TestExplicitBodyBlockerClearingAndCredentialGraduation(t *testing.T) {
	validAuraBody := "message=%7B%22actions%22%3A%5B%7B%22id%22%3A%221%3Ba%22%2C%22descriptor%22%3A%22aura%3A%2F%2FComponentController%2FACTION%24getComponent%22%2C%22callingDescriptor%22%3A%22UNKNOWN%22%2C%22params%22%3A%7B%7D%7D%5D%7D&aura.context=%7B%22mode%22%3A%22PROD%22%2C%22fwuid%22%3A%22abc%22%2C%22app%22%3A%22one%3Aone%22%2C%22loaded%22%3A%7B%7D%7D&aura.token={{var.corr_salesforce_aura_token}}"

	prior := &dsl.Script{
		ID:       "repair-body",
		Name:     "Repair Body",
		Version:  "1.0.0",
		Protocol: "http",
		Config: dsl.ScriptConfig{
			BaseURL: "https://org.my.salesforce.com",
		},
		Actions: []dsl.Action{
			{
				Name:   "01 - GET one.app",
				Type:   "request",
				Method: http.MethodGet,
				Target: "/one/one.app",
				Headers: map[string]string{
					"Authorization": "Bearer sfdc-live-retained-session-token-xyz",
				},
				HeaderList: []dsl.HeaderEntry{
					{Name: "Authorization", Value: "Bearer sfdc-live-retained-session-token-xyz"},
				},
				Extractors: []dsl.Extractor{
					{
						Name:       "corr_salesforce_aura_token",
						Source:     "header",
						Type:       "regex",
						Expression: "__Host-ERIC_PROD=([^;]+)",
					},
				},
				Metadata: map[string]any{
					dsl.ImportPreservedCredentialTargetsMetadataKey: []credentialpolicy.TargetRef{
						{RequestKey: "actions[0]", Field: "header_list", Index: 0},
					},
				},
			},
			{
				Name:   "94 - POST aura",
				Type:   "request",
				Method: http.MethodPost,
				Target: "/aura?r=19",
				Metadata: map[string]any{
					"execution_blocker":  "har_request_body_not_exported",
					"execution_blockers": []string{"har_request_body_not_exported"},
					"import_review": map[string]any{
						"reason_codes": []string{"har_request_body_not_exported"},
					},
				},
			},
		},
	}

	persisted, err := PreparePersistentImportedLibraryScript(prior, nil, "har")
	if err != nil {
		t.Fatalf("initial import save: %v", err)
	}
	if !IsCredentialPreservingImport(persisted) {
		t.Fatal("expected initial import save to preserve credentials")
	}

	// Supplying a syntactically valid Aura body WITHOUT explicitly clearing the
	// blocker must keep har_request_body_not_exported active.
	editedBodyOnly := MaskScriptSecrets(persisted)
	editedBodyOnly.Actions[1].Body = validAuraBody
	stillBlocked, err := PreparePersistentLibraryScript(editedBodyOnly, persisted)
	if err != nil {
		t.Fatalf("body-only save failed: %v", err)
	}
	if codes := ExecutionReviewBlockerCodes(stillBlocked.Actions[1].Metadata); !slices.Contains(codes, "har_request_body_not_exported") {
		t.Fatalf("expected har_request_body_not_exported to remain until explicitly cleared, got %v", codes)
	}
	if !IsCredentialPreservingImport(stillBlocked) {
		t.Fatal("expected script with retained Authorization header to remain credential-preserving")
	}

	// Explicitly clearing the blocker AND binding the retained Authorization
	// header to {{secret.sfdc_token}} clears the blocker and graduates the
	// script out of preserve_imported_credentials mode.
	explicitlyCleared := MaskScriptSecrets(stillBlocked)
	if explicitlyCleared == nil {
		t.Fatal("MaskScriptSecrets(stillBlocked) returned nil")
	}
	explicitlyCleared.Actions[0].Headers["Authorization"] = "Bearer {{secret.sfdc_token}}"
	explicitlyCleared.Actions[0].HeaderList[0].Value = "Bearer {{secret.sfdc_token}}"
	delete(explicitlyCleared.Actions[1].Metadata, "execution_blocker")
	delete(explicitlyCleared.Actions[1].Metadata, "execution_blockers")
	delete(explicitlyCleared.Actions[1].Metadata, "import_review")

	graduated, err := PreparePersistentLibraryScript(explicitlyCleared, stillBlocked)
	if err != nil {
		t.Fatalf("explicit clear + secret binding save failed: %v", err)
	}
	if codes := ExecutionReviewBlockerCodes(graduated.Actions[1].Metadata); len(codes) != 0 {
		t.Fatalf("expected explicit blocker removal to persist, got %v", codes)
	}
	if IsCredentialPreservingImport(graduated) {
		t.Fatalf("expected script with all credentials bound to {{secret.*}} to graduate out of credential-preserving mode, policy=%#v", graduated.Metadata.Import.Preserved[dsl.ImportPreservedCredentialPolicyKey])
	}
	if _, hasTargets := graduated.Actions[0].Metadata[dsl.ImportPreservedCredentialTargetsMetadataKey]; hasTargets {
		t.Fatalf("expected graduated action to drop stale import_credential_targets: %#v", graduated.Actions[0].Metadata)
	}
	// After graduation, ordinary target edits are permitted cleanly.
	graduatedEdit := CloneScriptDefinition(graduated)
	graduatedEdit.Actions[0].Target = "https://sandbox2.my.salesforce.com/one/one.app"
	if _, err := PreparePersistentLibraryScript(graduatedEdit, graduated); err != nil {
		t.Fatalf("expected graduated script to allow ordinary target edits, got: %v", err)
	}

	// An imported script whose durable prior never carried retained credentials
	// (or a Java Vuser script governed by authoring-session approval hashes) must
	// not have its import_value_policy silently mutated on an unchanged update.
	credentialFreePrior, err := PreparePersistentImportedLibraryScript(&dsl.Script{
		Name:     "Credential Free Imported Script",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{
			{Name: "1 - GET home", Type: "request", Method: http.MethodGet, Target: "/"},
		},
	}, nil, "openapi")
	if err != nil {
		t.Fatalf("credential-free initial import save: %v", err)
	}
	unchangedClean, err := PreparePersistentLibraryScript(CloneScriptDefinition(credentialFreePrior), credentialFreePrior)
	if err != nil {
		t.Fatalf("credential-free unchanged update: %v", err)
	}
	if !IsCredentialPreservingImport(unchangedClean) {
		t.Fatalf("expected credential-free imported prior to keep stable import_value_policy across unchanged update, got %#v", unchangedClean.Metadata.Import.Preserved[dsl.ImportPreservedCredentialPolicyKey])
	}
}

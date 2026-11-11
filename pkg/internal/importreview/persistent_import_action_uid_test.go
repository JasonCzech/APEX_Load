package importreview

import (
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
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
		projected := CloneScriptDefinition(persistedPrior)
		if projected == nil {
			t.Fatal("CloneScriptDefinition returned nil")
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
		projected := CloneScriptDefinition(persistedPrior)
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

	t.Run("duplicating retained-credential action succeeds with unique UID", func(t *testing.T) {
		projected := CloneScriptDefinition(persistedPrior)
		projected.Actions = append(projected.Actions, projected.Actions[2])
		saved, err := PreparePersistentLibraryScript(projected, persistedPrior)
		if err != nil {
			t.Fatalf("duplicating retained-credential action failed: %v", err)
		}
		if got := saved.Actions[3].Headers["Authorization"]; got != "Bearer sfdc-live-retained-session-token-xyz" {
			t.Fatalf("duplicated action Authorization = %q, want literal credential preserved", got)
		}
		if MaskScriptSecrets(saved) == nil {
			t.Fatal("MaskScriptSecrets failed on duplicated retained-credential script")
		}
	})

	t.Run("redirecting retained-credential action target after deletion succeeds", func(t *testing.T) {
		projected := CloneScriptDefinition(persistedPrior)
		projected.Actions = []dsl.Action{projected.Actions[0], projected.Actions[2]}
		projected.Actions[1].Target = "https://sandbox.example.test/collect"
		saved, err := PreparePersistentLibraryScript(projected, persistedPrior)
		if err != nil {
			t.Fatalf("redirecting retained-credential action target failed: %v", err)
		}
		if saved.Actions[1].Target != "https://sandbox.example.test/collect" {
			t.Fatalf("saved target = %q, want updated target", saved.Actions[1].Target)
		}
		if MaskScriptSecrets(saved) == nil {
			t.Fatal("MaskScriptSecrets failed after redirecting action target")
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
	editedBodyOnly := CloneScriptDefinition(persisted)
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
	explicitlyCleared := CloneScriptDefinition(stillBlocked)
	if explicitlyCleared == nil {
		t.Fatal("CloneScriptDefinition(stillBlocked) returned nil")
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

// A client-supplied import_action_uid alone must not move a retained
// credential's durable locators. An editor who copies a credential step's UID
// onto an earlier step (or an init step, which the walk visits first) must
// neither hand that step the locators nor strip them from the credential step
// that reconciliation matched and restored.
func TestRestoreLocatorsIgnoresForgedDuplicateImportActionUID(t *testing.T) {
	const token = "Bearer sfdc-live-retained-session-token-xyz"
	rawPrior := &dsl.Script{
		ID: "sfdc-har-forged-uid", Name: "Salesforce HAR Import", Version: "1.0.0", Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: "https://org.my.salesforce.com"},
		Init: []dsl.Action{
			{Name: "warm up", Type: "request", Method: http.MethodGet, Target: "/warm"},
		},
		Actions: []dsl.Action{
			{Name: "01 - GET login", Type: "request", Method: http.MethodGet, Target: "/"},
			{
				Name: "05 - POST verify", Type: "request", Method: http.MethodPost, Target: "/verify",
				HeaderList: []dsl.HeaderEntry{{Name: "Authorization", Value: token}},
				Metadata: map[string]any{
					dsl.ImportPreservedCredentialTargetsMetadataKey: []credentialpolicy.TargetRef{
						{RequestKey: "actions[1]", Field: "header_list", Index: 0},
					},
				},
			},
		},
	}
	EnsureScriptImportActionUIDs(rawPrior)
	prior, err := PreparePersistentImportedLibraryScript(rawPrior, nil, "har")
	if err != nil {
		t.Fatalf("PreparePersistentImportedLibraryScript: %v", err)
	}
	credentialUID, _ := prior.Actions[1].Metadata[dsl.ImportActionUIDMetadataKey].(string)
	if !dsl.IsValidImportActionUID(credentialUID) {
		t.Fatalf("precondition: credential step needs a UID: %#v", prior.Actions[1].Metadata)
	}

	forge := func(t *testing.T, action *dsl.Action) {
		t.Helper()
		if action.Metadata == nil {
			action.Metadata = map[string]any{}
		}
		action.Metadata[dsl.ImportActionUIDMetadataKey] = credentialUID
	}
	for _, tc := range []struct {
		name   string
		forged func(*dsl.Script) *dsl.Action
	}{
		{"earlier action", func(s *dsl.Script) *dsl.Action { return &s.Actions[0] }},
		{"init step", func(s *dsl.Script) *dsl.Action { return &s.Init[0] }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := CloneScriptDefinition(prior)
			if candidate == nil {
				t.Fatal("CloneScriptDefinition returned nil")
			}
			forged := tc.forged(candidate)
			forge(t, forged)

			saved, err := PreparePersistentLibraryScript(candidate, prior)
			if err != nil {
				t.Fatalf("PreparePersistentLibraryScript: %v", err)
			}
			if got := saved.Actions[1].HeaderList[0].Value; got != token {
				t.Fatalf("Authorization = %q, want the retained credential restored", got)
			}
			if got := saved.Actions[1].Metadata[dsl.ImportActionUIDMetadataKey]; got != credentialUID {
				t.Fatalf("credential step UID = %#v, want it to keep %q", got, credentialUID)
			}
			locators, _ := saved.Actions[1].Metadata[dsl.ImportPreservedCredentialTargetsMetadataKey].([]any)
			if len(locators) != 1 {
				t.Fatalf("credential step lost its durable locator: %#v", saved.Actions[1].Metadata)
			}
			if locator, _ := locators[0].(map[string]any); locator["request_key"] != "actions[1]" {
				t.Fatalf("credential locator = %#v, want actions[1]", locators[0])
			}
			forgedSaved := tc.forged(saved)
			if _, leaked := forgedSaved.Metadata[dsl.ImportPreservedCredentialTargetsMetadataKey]; leaked {
				t.Fatalf("forged step received the credential locators: %#v", forgedSaved.Metadata)
			}
			if got := forgedSaved.Metadata[dsl.ImportActionUIDMetadataKey]; got == credentialUID {
				t.Fatalf("forged step kept the duplicated UID %q", credentialUID)
			}
			if reread := MaskScriptSecrets(saved); reread == nil || reread.Actions[1].HeaderList[0].Value == token {
				t.Fatalf("the next read discloses the retained credential: %#v", reread)
			}
		})
	}
}

// A step renamed because it duplicated another step's UID must take a UID no
// other step uses. Reusing one (a copy made on an earlier save, or a UID the
// editor set on another step) would store two steps with the same UID; step
// matching then skips every UID-stamped step of that collection, and the next
// save would strip the retained credential's durable locators.
func TestRestoreLocatorsMintsUnusedCopyImportActionUIDs(t *testing.T) {
	const token = "Bearer sfdc-live-retained-session-token-xyz"
	rawPrior := &dsl.Script{
		ID: "sfdc-har-copy-uid", Name: "Salesforce HAR Import", Version: "1.0.0", Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: "https://org.my.salesforce.com"},
		Actions: []dsl.Action{
			{Name: "02 - GET home", Type: "request", Method: http.MethodGet, Target: "/home"},
			{Name: "03 - GET menu", Type: "request", Method: http.MethodGet, Target: "/menu"},
			{
				Name: "05 - POST verify", Type: "request", Method: http.MethodPost, Target: "/verify",
				HeaderList: []dsl.HeaderEntry{{Name: "Authorization", Value: token}},
				Metadata: map[string]any{
					dsl.ImportPreservedCredentialTargetsMetadataKey: []credentialpolicy.TargetRef{
						{RequestKey: "actions[2]", Field: "header_list", Index: 0},
					},
				},
			},
		},
	}
	EnsureScriptImportActionUIDs(rawPrior)
	prior, err := PreparePersistentImportedLibraryScript(rawPrior, nil, "har")
	if err != nil {
		t.Fatalf("PreparePersistentImportedLibraryScript: %v", err)
	}
	copiedUID, _ := prior.Actions[0].Metadata[dsl.ImportActionUIDMetadataKey].(string)
	if !dsl.IsValidImportActionUID(copiedUID) {
		t.Fatalf("precondition: step needs a UID: %#v", prior.Actions[0].Metadata)
	}

	duplicateFirst := func(script *dsl.Script) {
		dup := script.Actions[0]
		dup.Metadata = maps.Clone(dup.Metadata)
		script.Actions = append(script.Actions, dup)
	}
	save := func(t *testing.T, stored *dsl.Script, edit func(*dsl.Script)) *dsl.Script {
		t.Helper()
		candidate := CloneScriptDefinition(stored)
		if candidate == nil {
			t.Fatal("CloneScriptDefinition returned nil")
		}
		if edit != nil {
			edit(candidate)
		}
		saved, err := PreparePersistentLibraryScript(candidate, stored)
		if err != nil {
			t.Fatalf("PreparePersistentLibraryScript: %v", err)
		}
		return saved
	}
	assertSafe := func(t *testing.T, saved *dsl.Script) {
		t.Helper()
		seen := map[string]int{}
		for i, action := range saved.Actions {
			uid, _ := action.Metadata[dsl.ImportActionUIDMetadataKey].(string)
			if uid == "" {
				continue
			}
			if prev, dup := seen[uid]; dup {
				t.Fatalf("actions[%d] and actions[%d] share import_action_uid %q", prev, i, uid)
			}
			seen[uid] = i
		}
		locators, _ := saved.Actions[2].Metadata[dsl.ImportPreservedCredentialTargetsMetadataKey].([]any)
		if len(locators) != 1 {
			t.Fatalf("credential step lost its durable locator: %#v", saved.Actions[2].Metadata)
		}
		if got := saved.Actions[2].HeaderList[0].Value; got != token {
			t.Fatalf("Authorization = %q, want the retained credential", got)
		}
	}

	t.Run("duplicated twice across saves", func(t *testing.T) {
		saved1 := save(t, prior, duplicateFirst)
		assertSafe(t, saved1)
		saved2 := save(t, saved1, duplicateFirst)
		assertSafe(t, saved2)
		saved3 := save(t, saved2, func(s *dsl.Script) { s.Name = "Renamed" })
		assertSafe(t, saved3)
	})

	t.Run("copy name already set on another step", func(t *testing.T) {
		saved1 := save(t, prior, func(s *dsl.Script) {
			s.Actions[1].Metadata = maps.Clone(s.Actions[1].Metadata)
			s.Actions[1].Metadata[dsl.ImportActionUIDMetadataKey] = copiedUID + "-copy-2"
			duplicateFirst(s)
		})
		assertSafe(t, saved1)
		saved2 := save(t, saved1, func(s *dsl.Script) { s.Name = "Renamed" })
		assertSafe(t, saved2)
	})
}

// An import commit must not store two steps with the same import_action_uid.
// A step copied in the JSON authoring view keeps its UID; if the trusted
// commit stored it as is, step matching would treat the repeated stored UID as
// ambiguous, skip every UID-stamped step, and fail every later masked edit of
// the retained-credential step.
func TestPreparePersistentImportedLibraryScriptRenamesDuplicateImportActionUIDs(t *testing.T) {
	const token = "Bearer sfdc-live-retained-session-token-xyz"
	draft := &dsl.Script{
		ID: "sfdc-har-commit-dup-uid", Name: "Salesforce HAR Import", Version: "1.0.0", Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: "https://org.my.salesforce.com"},
		Actions: []dsl.Action{
			{Name: "02 - GET home", Type: "request", Method: http.MethodGet, Target: "/home"},
			{
				Name: "05 - POST verify", Type: "request", Method: http.MethodPost, Target: "/verify",
				HeaderList: []dsl.HeaderEntry{{Name: "Authorization", Value: token}},
				Metadata: map[string]any{
					dsl.ImportPreservedCredentialTargetsMetadataKey: []credentialpolicy.TargetRef{
						{RequestKey: "actions[1]", Field: "header_list", Index: 0},
					},
				},
			},
		},
	}
	EnsureScriptImportActionUIDs(draft)
	copiedUID, _ := draft.Actions[0].Metadata[dsl.ImportActionUIDMetadataKey].(string)
	if !dsl.IsValidImportActionUID(copiedUID) {
		t.Fatalf("precondition: step needs a UID: %#v", draft.Actions[0].Metadata)
	}
	// The JSON view copy: the request step pasted again at the end, UID kept.
	dup := draft.Actions[0]
	dup.Metadata = maps.Clone(dup.Metadata)
	draft.Actions = append(draft.Actions, dup)

	stored, err := PreparePersistentImportedLibraryScript(draft, nil, "har")
	if err != nil {
		t.Fatalf("PreparePersistentImportedLibraryScript: %v", err)
	}
	if got := stored.Actions[0].Metadata[dsl.ImportActionUIDMetadataKey]; got != copiedUID {
		t.Fatalf("first step UID = %#v, want it to keep %q", got, copiedUID)
	}
	seen := map[string]int{}
	for i, action := range stored.Actions {
		uid, _ := action.Metadata[dsl.ImportActionUIDMetadataKey].(string)
		if uid == "" {
			continue
		}
		if prev, repeated := seen[uid]; repeated {
			t.Fatalf("stored actions[%d] and actions[%d] share import_action_uid %q", prev, i, uid)
		}
		seen[uid] = i
	}

	// The next ordinary edit must still match the credential step.
	candidate := CloneScriptDefinition(stored)
	if candidate == nil {
		t.Fatal("CloneScriptDefinition returned nil")
	}
	candidate.Actions[0].Target = "/home?tab=1"
	saved, err := PreparePersistentLibraryScript(candidate, stored)
	if err != nil {
		t.Fatalf("PreparePersistentLibraryScript after commit: %v", err)
	}
	if got := saved.Actions[1].HeaderList[0].Value; got != token {
		t.Fatalf("Authorization = %q, want the retained credential restored", got)
	}
	locators, _ := saved.Actions[1].Metadata[dsl.ImportPreservedCredentialTargetsMetadataKey].([]any)
	if len(locators) != 1 {
		t.Fatalf("credential step lost its durable locator: %#v", saved.Actions[1].Metadata)
	}
}

func TestPruneStaleImportedCredentialTargetsPreservesSiblingsNestedRequestsAndSecretColumns(t *testing.T) {
	const (
		siblingPin    = "neutral-sibling-pin-9911"
		seqChildPin   = "neutral-sequence-pin-8822"
		asyncChildPin = "neutral-async-pin-7733"
		csvPin        = "neutral-csv-secret-6644"
	)
	rawPrior := &dsl.Script{
		ID:       "prune-granular-locators",
		Name:     "Granular Locator Pruning",
		Version:  "1.0.0",
		Protocol: "http",
		Config: dsl.ScriptConfig{
			BaseURL:  "https://api.synthetic.example.test",
			Database: dsl.DatabaseConfig{DSN: "postgres://svc:db-pass-5511@db.synthetic.example.test:5432/app"},
			HTTP: dsl.HTTPConfig{
				Proxy: dsl.ProxyConfig{URL: "http://proxy-user:proxy-pass-4411@proxy.synthetic.example.test:8080"},
			},
		},
		DataSources: []dsl.DataSource{{
			Name:       "accounts",
			Type:       "csv",
			InlineData: "user,secret_pin\nalice," + csvPin + "\n",
		}},
		Metadata: &dsl.ScriptMetadata{
			Import: &dsl.ImportMetadata{
				Source: "har",
				Preserved: map[string]any{
					dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
					"recorded_input_secret_columns":        []any{"secret_pin"},
					dsl.ImportPreservedCredentialTargetsMetadataKey: []credentialpolicy.TargetRef{
						{Field: "config.database.dsn"},
						{Field: "config.http.proxy.url"},
					},
				},
			},
		},
		Actions: []dsl.Action{
			{
				Name:   "01 - Sibling Targets",
				Type:   "request",
				Method: http.MethodPost,
				Target: "/login?acct_pin=" + siblingPin,
				Headers: map[string]string{
					"Content-Type": "application/json",
				},
				Body: `{"stale_secret":"old-body-secret-1111"}`,
				Metadata: map[string]any{
					dsl.ImportPreservedCredentialTargetsMetadataKey: []credentialpolicy.TargetRef{
						{RequestKey: "actions[0]", Field: "target.query", Pointer: "acct_pin"},
						{RequestKey: "actions[0]", Field: "body.json", Pointer: "/stale_secret"},
					},
				},
			},
			{
				Name: "02 - Sequence Child Targets",
				Type: "http_sequence",
				Params: map[string]any{
					"requests": []any{
						map[string]any{
							"name":    "seq-child-0",
							"method":  http.MethodPost,
							"target":  "/step-a",
							"headers": map[string]any{"Content-Type": "application/json"},
							"body":    `{"seq_pin":"` + seqChildPin + `"}`,
							"metadata": map[string]any{
								dsl.ImportPreservedCredentialTargetsMetadataKey: []credentialpolicy.TargetRef{
									{RequestKey: "actions[1].params.requests[0]", Field: "body.json", Pointer: "/seq_pin"},
									{RequestKey: "actions[1].params.requests[0]", Field: "header_list", Index: 9},
								},
							},
						},
					},
				},
			},
			{
				Name: "03 - Async Child Target",
				Type: "http_async_start",
				Params: map[string]any{
					"conversation_id": "conv-1",
					"request": map[string]any{
						"name":    "async-req",
						"method":  http.MethodPost,
						"target":  "/poll",
						"headers": map[string]any{"Content-Type": "application/json"},
						"body":    `{"async_pin":"` + asyncChildPin + `"}`,
						"metadata": map[string]any{
							dsl.ImportPreservedCredentialTargetsMetadataKey: []credentialpolicy.TargetRef{
								{RequestKey: "actions[2].params.request", Field: "body.json", Pointer: "/async_pin"},
							},
						},
					},
				},
			},
		},
	}
	EnsureScriptImportActionUIDs(rawPrior)
	// Store initial valid baseline (with valid seq header_list omitted so baseline passes disclosure).
	seqReqs := rawPrior.Actions[1].Params["requests"].([]any)
	seqReq0 := seqReqs[0].(map[string]any)
	seqReq0["header_list"] = []any{map[string]any{"name": "X-Temp-Secret", "value": "temp-header-secret-3311"}}
	seqReq0["metadata"].(map[string]any)[dsl.ImportPreservedCredentialTargetsMetadataKey] = []credentialpolicy.TargetRef{
		{RequestKey: "actions[1].params.requests[0]", Field: "body.json", Pointer: "/seq_pin"},
		{RequestKey: "actions[1].params.requests[0]", Field: "header_list", Index: 0},
	}

	prior, err := PreparePersistentImportedLibraryScript(rawPrior, nil, "har")
	if err != nil {
		t.Fatalf("PreparePersistentImportedLibraryScript: %v", err)
	}

	// Operator clears proxy URL, replaces actions[0].Body removing /stale_secret, and removes header_list on seq child 0.
	candidate := CloneScriptDefinition(prior)
	candidate.Config.HTTP.Proxy.URL = ""
	candidate.Actions[0].Body = `{"hello":"world"}`
	candSeqReq0 := candidate.Actions[1].Params["requests"].([]any)[0].(map[string]any)
	delete(candSeqReq0, "header_list")

	saved, err := PreparePersistentLibraryScript(candidate, prior)
	if err != nil {
		t.Fatalf("PreparePersistentLibraryScript after partial edits: %v", err)
	}

	// 1. Engine disclosure inspection must succeed and recover all surviving neutral-named credentials.
	disclosedVals, err := engine.ImportedCredentialValuesForDisclosure(saved)
	if err != nil {
		t.Fatalf("ImportedCredentialValuesForDisclosure(saved): %v", err)
	}
	for _, want := range []string{siblingPin, seqChildPin, asyncChildPin} {
		if !slices.Contains(disclosedVals, want) {
			t.Fatalf("ImportedCredentialValuesForDisclosure missing %q in %v", want, disclosedVals)
		}
	}

	// 2. recorded_input_secret_columns must remain intact.
	cols, ok := saved.Metadata.Import.Preserved["recorded_input_secret_columns"].([]any)
	if !ok || len(cols) != 1 || cols[0] != "secret_pin" {
		t.Fatalf("recorded_input_secret_columns lost or mutated: %#v", saved.Metadata.Import.Preserved["recorded_input_secret_columns"])
	}

	// 3. Runtime/replay/run-snapshot disclosure projection (MaskScriptSecrets) must still scrub all surviving neutral credentials & CSV column.
	masked := MaskScriptSecrets(saved)
	encodedMasked, err := json.Marshal(masked)
	if err != nil {
		t.Fatal(err)
	}
	maskedJSON := string(encodedMasked)
	for _, secret := range []string{siblingPin, seqChildPin, asyncChildPin, csvPin} {
		if strings.Contains(maskedJSON, secret) {
			t.Fatalf("MaskScriptSecrets leaked %q after partial edit: %s", secret, maskedJSON)
		}
	}
}

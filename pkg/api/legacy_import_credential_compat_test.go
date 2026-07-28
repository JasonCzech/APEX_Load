package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestModernImportValuePolicyKeepsEstablishedLibraryHash(t *testing.T) {
	modern := &dsl.Script{
		ID:            "modern-postman",
		Name:          "Modern Postman",
		Version:       "1.0",
		Protocol:      "http",
		ProjectID:     storage.DefaultProjectID,
		GeneratedBy:   "apexload-postman-converter",
		GeneratedFrom: "postman",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "postman",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
				"modern_field":                         "retained",
			},
		}},
		Config: dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{
			Name: "request", Type: "request", Method: "GET", Target: "/",
		}},
	}
	got := hashLibraryScript(modern)
	want := hashLibraryScriptBeforeLegacyPolicyCompatibility(modern)
	if got != want {
		t.Fatalf("modern import hash changed: got %q want established %q", got, want)
	}

	legacyView := cloneScriptDefinition(modern)
	delete(legacyView.Metadata.Import.Preserved, dsl.ImportPreservedCredentialPolicyKey)
	if gotLegacy := hashLibraryScript(legacyView); gotLegacy != got {
		t.Fatalf("legacy and recognized views hash differently: legacy %q modern %q", gotLegacy, got)
	}
}

func hashLibraryScriptBeforeLegacyPolicyCompatibility(script *dsl.Script) string {
	if script == nil {
		return hashCanonical(nil)
	}
	copied := maskScriptSecrets(script)
	if copied == nil {
		return hashCanonical(nil)
	}
	copied.ID = ""
	copied.RefKey = ""
	copied.ProjectID = ""
	copied.FolderID = nil
	copied.CreatedAt = time.Time{}
	copied.UpdatedAt = time.Time{}
	if copied.Metadata != nil && copied.Metadata.Import != nil && copied.Metadata.Import.Preserved != nil {
		delete(copied.Metadata.Import.Preserved, javaVuserApprovalEvidenceKey)
	}
	return hashCanonical(copied)
}

func TestLegacyDurableImportMaskedUpdatePreservesExactCredentials(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "legacy-import.db"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()

	legacy := &dsl.Script{
		ID:            "legacy-loadrunner",
		Name:          "Legacy LoadRunner",
		Version:       "1.0",
		Protocol:      "http",
		ProjectID:     storage.DefaultProjectID,
		GeneratedBy:   "apexload-loadrunner-converter",
		GeneratedFrom: "loadrunner",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "loadrunner",
		}},
		Config: dsl.ScriptConfig{
			BaseURL: "https://example.test",
			HTTP: dsl.HTTPConfig{Auth: dsl.AuthConfig{
				Type: "basic", Username: "operator",
				// A real imported password may equal the disclosure sentinel.
				Password: maskedScriptSecret,
			}},
		},
		Actions: []dsl.Action{{
			Name: "request", Type: "request", Method: "GET", Target: "/",
			Auth: &dsl.AuthConfig{
				Type: "digest", Username: "request-user", Password: "request-password",
			},
		}},
	}
	hashBeforeRecognition := hashLibraryScriptBeforeLegacyPolicyCompatibility(legacy)
	if err := store.SaveScript(legacy); err != nil {
		t.Fatalf("seed legacy import: %v", err)
	}
	prior, err := store.GetScript(legacy.ID)
	if err != nil {
		t.Fatalf("load legacy import: %v", err)
	}
	if !isCredentialPreservingImport(prior) {
		t.Fatal("durable legacy import was not recognized as credential-preserving")
	}
	if got := hashLibraryScript(prior); got == hashBeforeRecognition {
		t.Fatalf("test did not reproduce the pre-policy hash transition: both were %q", got)
	}
	if !libraryScriptHashMatches(prior, hashBeforeRecognition) {
		t.Fatalf(
			"legacy recognition no longer accepts pre-policy library hash %q (current %q)",
			hashBeforeRecognition,
			hashLibraryScript(prior),
		)
	}

	masked := maskScriptSecrets(prior)
	if masked == nil || masked.Actions[0].Auth == nil ||
		masked.Actions[0].Auth.Password != maskedScriptSecret {
		t.Fatalf("masked legacy script = %#v", masked)
	}
	masked.Name = "Operator-reviewed LoadRunner"
	persisted, err := preparePersistentLibraryScript(masked, prior)
	if err != nil {
		t.Fatalf("masked legacy update required credential re-entry: %v", err)
	}
	if persisted.Config.HTTP.Auth.Password != maskedScriptSecret {
		t.Fatalf("literal sentinel password changed: %q", persisted.Config.HTTP.Auth.Password)
	}
	if persisted.Actions[0].Auth == nil || persisted.Actions[0].Auth.Password != "request-password" {
		t.Fatalf("ordinary imported password was not restored: %#v", persisted.Actions[0].Auth)
	}
	if issues := validateStoredExecutionScript(persisted); len(issues) != 0 {
		t.Fatalf("preserved legacy import is not replay-valid: %v", issues)
	}
	if err := store.SaveScript(persisted); err != nil {
		t.Fatalf("save masked legacy update: %v", err)
	}
	reloaded, err := store.GetScript(legacy.ID)
	if err != nil {
		t.Fatalf("reload updated legacy import: %v", err)
	}
	if reloaded.Config.HTTP.Auth.Password != maskedScriptSecret ||
		reloaded.Actions[0].Auth == nil ||
		reloaded.Actions[0].Auth.Password != "request-password" {
		t.Fatalf("durable update lost imported credentials: %#v", reloaded)
	}
}

func TestLegacyDurableImportKeepsPrePolicyLibrarySyncLinkSynced(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()

	legacy := &dsl.Script{
		ID:            "legacy-linked-postman",
		Name:          "Legacy linked Postman",
		Version:       "1.0",
		Protocol:      "http",
		ProjectID:     storage.DefaultProjectID,
		GeneratedBy:   "apexload-postman-converter",
		GeneratedFrom: "postman",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "postman",
		}},
		Config: dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{
			Name: "request", Type: "request", Method: "GET", Target: "/",
			Auth: &dsl.AuthConfig{
				Type: "basic", Username: "operator", Password: "linked-password",
			},
		}},
	}
	prePolicyHash := hashLibraryScriptBeforeLegacyPolicyCompatibility(legacy)
	if err := store.SaveScript(legacy); err != nil {
		t.Fatalf("seed pre-policy import: %v", err)
	}
	if err := server.runner.reloadStoredScripts(); err != nil {
		t.Fatalf("reload pre-policy import: %v", err)
	}
	recognized, ok := server.runner.GetScript(legacy.ID)
	if !ok || recognized == nil || !isCredentialPreservingImport(recognized.Script) {
		t.Fatalf("legacy import was not recognized after durable reload: %#v", recognized)
	}
	if hashLibraryScript(recognized.Script) == prePolicyHash {
		t.Fatalf("test did not reproduce the legacy/current hash difference: %q", prePolicyHash)
	}

	const remoteHash = "sha256:remote-unchanged"
	if err := server.rememberLibraryLink(
		"remote",
		storage.DefaultProjectID,
		"script",
		"source-controller",
		"source-script",
		legacy.ID,
		"source-script",
		prePolicyHash,
		remoteHash,
	); err != nil {
		t.Fatalf("seed pre-policy sync link: %v", err)
	}
	annotated := server.annotateRemoteCatalogItem(
		"remote",
		storage.DefaultProjectID,
		LibraryCatalogItem{
			Kind:             "script",
			SourceInstanceID: "source-controller",
			SourceItemID:     "source-script",
			ContentHash:      remoteHash,
		},
	)
	if annotated.SyncStatus != "synced" {
		t.Fatalf(
			"recognized legacy import with pre-policy link status = %q, want synced",
			annotated.SyncStatus,
		)
	}

	edited := cloneScriptDefinition(recognized.Script)
	edited.Name = "Operator-edited linked Postman"
	if libraryScriptHashMatches(edited, prePolicyHash) {
		t.Fatal("legacy hash compatibility hid a real local script edit")
	}
}

func TestRollingUpgradeAcceptsPrePolicyScriptBundleAndDependencyHashes(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	source := &dsl.Script{
		ID:            "rolling-source-script",
		Name:          "Rolling source script",
		Version:       "1.0",
		Protocol:      "http",
		ProjectID:     "source-project",
		GeneratedBy:   "apexload-postman-converter",
		GeneratedFrom: "postman",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "postman",
		}},
		Config: dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{
			Name: "request", Type: "request", Method: "GET", Target: "/",
		}},
	}
	prePolicyHash := hashLibraryScriptBeforeLegacyPolicyCompatibility(source)
	if hashLibraryScript(source) == prePolicyHash {
		t.Fatalf("test did not reproduce rolling hash versions: %q", prePolicyHash)
	}
	scenarioRequest := CreateScenarioRequest{
		Name: "Rolling scenario", Duration: "1m", ScenarioMode: "vuser_driven",
		Groups: []CreateGroupEntry{{
			Name: "Users", ScriptID: source.ID, Role: GroupRoleLoad,
			VUsers: 1, InitStrategy: "at_runtime",
		}},
	}
	bundle := LibraryBundle{
		SchemaVersion: libraryBundleSchemaVersion,
		Source: LibraryBundleSource{
			InstanceID: "old-controller", ProjectID: "source-project",
		},
		Items: []LibraryBundleItem{
			{
				Kind: "script", SourceInstanceID: "old-controller",
				SourceItemID: source.ID, ProjectID: "source-project",
				Name: source.Name, Script: source, ContentHash: prePolicyHash,
			},
			{
				Kind: "scenario", SourceInstanceID: "old-controller",
				SourceItemID: "rolling-source-scenario", ProjectID: "source-project",
				Name: scenarioRequest.Name,
				Scenario: &LibraryScenarioPayload{
					ID: "rolling-source-scenario", Request: scenarioRequest,
				},
				ContentHash: hashCanonical(scenarioRequest),
				Dependencies: []LibraryDependency{{
					Kind: "script", ID: source.ID, ProjectID: "source-project",
					ContentHash: prePolicyHash,
				}},
			},
		},
	}
	imported, err := server.importLibraryBundle(
		LibraryImportRequest{
			ProjectID: storage.DefaultProjectID,
			Bundle:    bundle,
		},
		"rolling-remote",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(imported.Conflicts) != 0 || len(imported.Items) != 2 {
		t.Fatalf("rolling old-controller import = %#v", imported)
	}
}

func TestRollingUpgradeAcceptsMetadataLessRecorderBundleAndDependencyHash(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	source := &dsl.Script{
		ID:          "rolling-recorder-script",
		Name:        "Old recorder script",
		Version:     "1.0",
		Protocol:    "http",
		ProjectID:   "source-project",
		GeneratedBy: "recorder",
		Config:      dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{
			Name: "request", Type: "request", Method: "GET", Target: "/",
		}},
	}
	if source.Metadata != nil || source.GeneratedFrom != "" {
		t.Fatalf("old recorder fixture unexpectedly has modern provenance: %#v", source)
	}
	prePolicyHash := hashLibraryScriptBeforeLegacyPolicyCompatibility(source)
	if hashLibraryScript(source) == prePolicyHash {
		t.Fatalf("test did not reproduce metadata-less recorder hash versions: %q", prePolicyHash)
	}
	scenarioRequest := CreateScenarioRequest{
		Name: "Old recorder scenario", Duration: "1m", ScenarioMode: "vuser_driven",
		Groups: []CreateGroupEntry{{
			Name: "Users", ScriptID: source.ID, Role: GroupRoleLoad,
			VUsers: 1, InitStrategy: "at_runtime",
		}},
	}
	bundle := LibraryBundle{
		SchemaVersion: libraryBundleSchemaVersion,
		Source: LibraryBundleSource{
			InstanceID: "old-recorder-controller", ProjectID: "source-project",
		},
		Items: []LibraryBundleItem{
			{
				Kind: "script", SourceInstanceID: "old-recorder-controller",
				SourceItemID: source.ID, ProjectID: "source-project",
				Name: source.Name, Script: source, ContentHash: prePolicyHash,
			},
			{
				Kind: "scenario", SourceInstanceID: "old-recorder-controller",
				SourceItemID: "rolling-recorder-scenario", ProjectID: "source-project",
				Name: scenarioRequest.Name,
				Scenario: &LibraryScenarioPayload{
					ID: "rolling-recorder-scenario", Request: scenarioRequest,
				},
				ContentHash: hashCanonical(scenarioRequest),
				Dependencies: []LibraryDependency{{
					Kind: "script", ID: source.ID, ProjectID: "source-project",
					ContentHash: prePolicyHash,
				}},
			},
		},
	}
	imported, err := server.importLibraryBundle(
		LibraryImportRequest{
			ProjectID: storage.DefaultProjectID,
			Bundle:    bundle,
		},
		"rolling-recorder-remote",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(imported.Conflicts) != 0 || len(imported.Items) != 2 {
		t.Fatalf("metadata-less recorder rolling import = %#v", imported)
	}
}

func TestRollingUpgradeRemoteDiffTreatsPrePolicyHashAsSynced(t *testing.T) {
	t.Setenv(remoteOutboundAllowPrivateHostsEnv, "1")
	t.Setenv("APEXLOAD_LIBRARY_ALLOW_INSECURE_HTTP", "1")
	server, store := newAuditTestServer(t)
	defer store.Close()

	local := &dsl.Script{
		ID:            "rolling-local-script",
		Name:          "Rolling script",
		Version:       "1.0",
		Protocol:      "http",
		ProjectID:     storage.DefaultProjectID,
		GeneratedBy:   "apexload-postman-converter",
		GeneratedFrom: "postman",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "postman",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
			},
		}},
		Config: dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{
			Name: "request", Type: "request", Method: "GET", Target: "/",
		}},
	}
	if _, err := server.runner.StoreScriptDurable(local); err != nil {
		t.Fatal(err)
	}
	remoteScript := cloneScriptDefinition(local)
	remoteScript.ID = "rolling-remote-script"
	remoteScript.ProjectID = "remote-project"
	delete(
		remoteScript.Metadata.Import.Preserved,
		dsl.ImportPreservedCredentialPolicyKey,
	)
	remoteHash := hashLibraryScriptBeforeLegacyPolicyCompatibility(remoteScript)
	if hashLibraryScript(local) == remoteHash {
		t.Fatalf("test did not reproduce rolling diff hashes: %q", remoteHash)
	}
	remoteBundle := LibraryBundle{
		SchemaVersion: libraryBundleSchemaVersion,
		Source: LibraryBundleSource{
			InstanceID: "old-controller", ProjectID: "remote-project",
		},
		Items: []LibraryBundleItem{{
			Kind: "script", SourceInstanceID: "old-controller",
			SourceItemID: remoteScript.ID, ProjectID: "remote-project",
			Name: remoteScript.Name, Script: remoteScript, ContentHash: remoteHash,
		}},
	}
	remoteHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/library/export" {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusOK, remoteBundle)
	}))
	defer remoteHTTP.Close()
	server.libraryMu.Lock()
	server.libraryRemotes["rolling"] = &LibraryRemote{
		ID: "rolling", Name: "Rolling", BaseURL: remoteHTTP.URL, AuthMethod: "none",
		ProjectMappings: map[string]string{
			storage.DefaultProjectID: "remote-project",
		},
	}
	server.libraryMu.Unlock()
	if err := server.persistLibraryRemotes(); err != nil {
		t.Fatal(err)
	}

	body, err := json.Marshal(LibraryTransferRequest{
		Kind: "script", LocalID: local.ID, RemoteID: remoteScript.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/library/remotes/rolling/diff",
		bytes.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("rolling diff status = %d body=%s", response.Code, response.Body.String())
	}
	var result struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "synced" {
		t.Fatalf("rolling old/new hash diff status = %q, want synced", result.Status)
	}
}

func TestCallerAuthoredLegacyProvenanceCannotForgeCredentialPreservation(t *testing.T) {
	forged := &dsl.Script{
		ID:            "forged-import",
		Name:          "Forged import",
		Version:       "1.0",
		Protocol:      "http",
		ProjectID:     storage.DefaultProjectID,
		GeneratedBy:   "apexload-loadrunner-converter",
		GeneratedFrom: "loadrunner",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "loadrunner",
		}},
		Config: dsl.ScriptConfig{
			BaseURL: "https://example.test",
			HTTP: dsl.HTTPConfig{Auth: dsl.AuthConfig{
				Type: "basic", Username: "operator", Password: "forged-password",
			}},
		},
		Actions: []dsl.Action{{
			Name: "request", Type: "request", Method: "GET", Target: "/",
		}},
	}
	if isCredentialPreservingImport(forged) {
		t.Fatal("caller-authored legacy provenance established import trust")
	}
	if _, err := preparePersistentLibraryScript(forged, nil); err == nil {
		t.Fatal("caller-authored legacy provenance persisted an executable credential")
	} else if err != errScriptCredentialBindingsRequired {
		t.Fatalf("forged import error = %v, want credential bindings required", err)
	}
}

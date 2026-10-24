package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"github.com/jasonczech/apexload/pkg/importartifact"
	"github.com/jasonczech/apexload/pkg/storage"
)

type failSecondEnterpriseBatchScriptStore struct {
	*libraryAtomicForwardingStore
	mu    sync.Mutex
	count int
}

func (s *failSecondEnterpriseBatchScriptStore) CreateScriptIfAbsent(script *dsl.Script) (bool, error) {
	s.mu.Lock()
	s.count++
	count := s.count
	s.mu.Unlock()
	if count == 2 {
		return false, errors.New("injected second enterprise script failure")
	}
	return s.libraryAtomicForwardingStore.CreateScriptIfAbsent(script)
}

func TestHandleSaveEnterpriseArchiveImportCommitsBatchToSelectedFolder(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "enterprise-batch.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{ID: "project-enterprise", Name: "Enterprise", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveFolder(&storage.Folder{ID: "folder-enterprise", ProjectID: "project-enterprise", Name: "Imported", Category: "scripts", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{Store: store})
	response := postEnterpriseArchiveBatch(t, server, map[string]any{
		"operation_id": "enterprise-batch-success",
		"folder_id":    "folder-enterprise",
		"scripts": []any{
			enterpriseBatchTestScript("First", "project-enterprise"),
			enterpriseBatchTestScript("Second", "project-enterprise"),
		},
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d body=%s", response.Code, http.StatusCreated, response.Body.String())
	}
	var body struct {
		Count   int            `json:"count"`
		Scripts []StoredScript `json:"scripts"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Count != 2 || len(body.Scripts) != 2 {
		t.Fatalf("response = %#v, want two scripts", body)
	}
	for _, script := range body.Scripts {
		if script.FolderID == nil || *script.FolderID != "folder-enterprise" {
			t.Fatalf("stored script folder = %#v, want folder-enterprise", script.FolderID)
		}
	}
}

func TestHandleSaveEnterpriseArchiveImportRequiresOperationID(t *testing.T) {
	server := NewServer(ServerConfig{})
	response := postEnterpriseArchiveBatch(t, server, map[string]any{
		"scripts": []any{
			enterpriseBatchTestScript("Missing operation", storage.DefaultProjectID),
		},
	})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "operation_id is required") {
		t.Fatalf("body = %s, want operation_id guidance", response.Body.String())
	}
	if scripts := server.runner.ListScripts(); len(scripts) != 0 {
		t.Fatalf("missing operation id persisted scripts: %#v", scripts)
	}
}

func TestHandleSaveEnterpriseArchiveImportRollsBackEveryScriptOnLaterFailure(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "enterprise-batch-rollback.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	store := &failSecondEnterpriseBatchScriptStore{
		libraryAtomicForwardingStore: &libraryAtomicForwardingStore{Store: base},
	}
	server := NewServer(ServerConfig{Store: store})
	response := postEnterpriseArchiveBatch(t, server, map[string]any{
		"operation_id": "enterprise-batch-rollback",
		"scripts": []any{
			enterpriseBatchTestScript("First", storage.DefaultProjectID),
			enterpriseBatchTestScript("Second", storage.DefaultProjectID),
		},
	})
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d body=%s", response.Code, http.StatusInternalServerError, response.Body.String())
	}
	if scripts, err := base.ListScripts(storage.DefaultProjectID, nil); err != nil {
		t.Fatal(err)
	} else if len(scripts) != 0 {
		t.Fatalf("failed batch left scripts behind: %#v", scripts)
	}
}

func TestHandleSaveEnterpriseArchiveImportReplaysExactCommittedBatch(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "enterprise-batch-replay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})
	payload := map[string]any{
		"operation_id": "enterprise-batch-replay",
		"scripts": []any{
			enterpriseBatchTestScript("First", storage.DefaultProjectID),
			enterpriseBatchTestScript("Second", storage.DefaultProjectID),
		},
	}
	first := postEnterpriseArchiveBatch(t, server, payload)
	if first.Code != http.StatusCreated {
		t.Fatalf("first status = %d body=%s", first.Code, first.Body.String())
	}
	second := postEnterpriseArchiveBatch(t, server, payload)
	if second.Code != http.StatusCreated {
		t.Fatalf("replay status = %d body=%s", second.Code, second.Body.String())
	}
	var firstBody, secondBody struct {
		Count   int            `json:"count"`
		Scripts []StoredScript `json:"scripts"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &firstBody); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &secondBody); err != nil {
		t.Fatal(err)
	}
	if firstBody.Count != 2 || secondBody.Count != 2 {
		t.Fatalf("counts = %d/%d, want 2/2", firstBody.Count, secondBody.Count)
	}
	for index := range firstBody.Scripts {
		if firstBody.Scripts[index].ID != secondBody.Scripts[index].ID {
			t.Fatalf("script %d replay id = %q, want %q", index, secondBody.Scripts[index].ID, firstBody.Scripts[index].ID)
		}
	}
	if scripts, err := store.ListScripts(storage.DefaultProjectID, nil); err != nil {
		t.Fatal(err)
	} else if len(scripts) != 2 {
		t.Fatalf("replayed batch stored %d scripts, want exactly 2", len(scripts))
	}

	conflictingPayload := map[string]any{
		"operation_id": "enterprise-batch-replay",
		"scripts": []any{
			enterpriseBatchTestScript("Changed", storage.DefaultProjectID),
			enterpriseBatchTestScript("Second", storage.DefaultProjectID),
		},
	}
	conflict := postEnterpriseArchiveBatch(t, server, conflictingPayload)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("conflict status = %d, want %d body=%s", conflict.Code, http.StatusConflict, conflict.Body.String())
	}
}

func TestHandleSaveEnterpriseArchiveImportRestartRecoversCommittedCredentialedBatch(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "enterprise-restart-recovery.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	store := &failOnceDirectImportReceiptStore{SQLiteStore: base, failComplete: true}
	firstServer := NewServer(ServerConfig{Store: sqliteOverlay(store)})
	script := &dsl.Script{
		Name: "Enterprise restart recovery", Version: "1.0.0", Protocol: "http", ProjectID: storage.DefaultProjectID,
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Source: "loadrunner", Preserved: map[string]any{}}},
		Config:   dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions: []dsl.Action{{
			Name: "Request", Type: "request", Method: http.MethodGet, Target: "/",
			HeaderList: dsl.HeaderList{{Name: "Authorization", Value: "Bearer enterprise-restart-secret"}},
		}},
	}
	preview := firstServer.newScriptImportResponseWithSourceSecrets(
		httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/enterprise-script-archive", nil),
		scriptImportResponseOptions{Script: script, Scripts: []*dsl.Script{script}},
	)
	masked, ok := preview["scripts"].([]any)
	if !ok || len(masked) != 1 {
		t.Fatalf("preview scripts = %#v", preview["scripts"])
	}
	payload := map[string]any{
		"operation_id": "enterprise-credential-restart-recovery",
		"scripts":      masked,
	}
	first := postEnterpriseArchiveBatch(t, firstServer, payload)
	if first.Code != http.StatusInternalServerError {
		t.Fatalf("injected completion status = %d body=%s", first.Code, first.Body.String())
	}
	reuse := postEnterpriseArchiveBatch(t, firstServer, map[string]any{
		"operation_id": "enterprise-credential-restart-different-operation",
		"scripts":      masked,
	})
	if reuse.Code != http.StatusConflict {
		t.Fatalf("post-commit capability reuse status = %d body=%s", reuse.Code, reuse.Body.String())
	}

	restarted := NewServer(ServerConfig{Store: sqliteOverlay(store)})
	recovered := postEnterpriseArchiveBatch(t, restarted, payload)
	if recovered.Code != http.StatusCreated {
		t.Fatalf("restart recovery status = %d body=%s", recovered.Code, recovered.Body.String())
	}
	scripts, err := base.ListScripts(storage.DefaultProjectID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(scripts) != 1 || scripts[0].Actions[0].HeaderList[0].Value != "Bearer enterprise-restart-secret" {
		t.Fatalf("recovered scripts = %#v, want one exact credentialed script", scripts)
	}
}

func TestHandleSaveEnterpriseArchiveImportRestoresAttestedCredentialInEmbeddedFolder(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "enterprise-attested.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	const projectID = "project-enterprise-attested"
	const folderID = "folder-enterprise-attested"
	if err := store.SaveProject(&storage.Project{ID: projectID, Name: "Enterprise", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveFolder(&storage.Folder{ID: folderID, ProjectID: projectID, Name: "Imported", Category: "scripts", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{Store: store})
	script := &dsl.Script{
		Name: "Attested LoadRunner", Version: "1.0.0", Protocol: "http", ProjectID: projectID,
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Source: "loadrunner", Preserved: map[string]any{}}},
		Config:   dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions: []dsl.Action{{
			Name: "Request", Type: "request", Method: http.MethodGet, Target: "/",
			HeaderList: dsl.HeaderList{{Name: "Authorization", Value: "Bearer enterprise-exact-secret"}},
		}},
	}
	preview := server.newScriptImportResponseWithSourceSecrets(
		httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/enterprise-script-archive", nil),
		scriptImportResponseOptions{Script: script, Scripts: []*dsl.Script{script}},
	)
	masked, ok := preview["script"].(map[string]any)
	if !ok {
		t.Fatalf("masked preview script = %T", preview["script"])
	}
	masked["name"] = "UI renamed LoadRunner"
	// The exact candidate is at the library root; the browser-selected embedded
	// destination must survive restoration of that server-owned candidate.
	masked["folder_id"] = folderID
	response := postEnterpriseArchiveBatch(t, server, map[string]any{
		"operation_id": "enterprise-attested-folder",
		"scripts":      []any{masked},
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Scripts []StoredScript `json:"scripts"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Scripts) != 1 {
		t.Fatalf("scripts = %#v", body.Scripts)
	}
	stored, ok := server.runner.GetScript(body.Scripts[0].ID)
	if !ok || stored == nil || stored.Script == nil {
		t.Fatal("stored attested enterprise script not found")
	}
	if stored.FolderID == nil || *stored.FolderID != folderID {
		t.Fatalf("folder = %#v, want %q", stored.FolderID, folderID)
	}
	if got := stored.Script.Actions[0].HeaderList[0].Value; got != "Bearer enterprise-exact-secret" {
		t.Fatalf("restored credential = %q", got)
	}
	if got := stored.Script.Name; got != "UI renamed LoadRunner" {
		t.Fatalf("restored script name = %q, want requested UI name", got)
	}
	if got := stored.Script.Metadata.Import.Source; got != "loadrunner" {
		t.Fatalf("durable source profile = %q, want loadrunner", got)
	}
	if !strings.Contains(response.Body.String(), "enterprise-exact-secret") {
		t.Fatalf("save response did not preserve credential: %s", response.Body.String())
	}
}

func TestHandleSaveEnterpriseArchiveImportAllocatesAttestedNamesAcrossArchives(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "enterprise-attested-names.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})
	previewScript := func(credential string) map[string]any {
		t.Helper()
		script := &dsl.Script{
			Name: "Shared Human Name", Version: "1.0.0", Protocol: "http", ProjectID: storage.DefaultProjectID,
			Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Source: "loadrunner", Preserved: map[string]any{}}},
			Config:   dsl.ScriptConfig{BaseURL: "https://api.example.test"},
			Actions: []dsl.Action{{
				Name: "Request", Type: "request", Method: http.MethodGet, Target: "/",
				HeaderList: dsl.HeaderList{{Name: "Authorization", Value: "Bearer " + credential}},
			}},
		}
		preview := server.newScriptImportResponseWithSourceSecrets(
			httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/enterprise-script-archive", nil),
			scriptImportResponseOptions{Script: script, Scripts: []*dsl.Script{script}},
		)
		masked, ok := preview["script"].(map[string]any)
		if !ok {
			t.Fatalf("masked preview script = %T", preview["script"])
		}
		return masked
	}
	first := previewScript("first-attested-enterprise-secret")
	second := previewScript("second-attested-enterprise-secret")
	// Simulate independently converted archives whose human names differ only
	// by case. The backend, not just the dashboard, owns the final collision
	// guarantee.
	second["name"] = "shared human name"
	payload := map[string]any{
		"operation_id": "enterprise-attested-name-batch",
		"scripts":      []any{first, second},
	}
	response := postEnterpriseArchiveBatch(t, server, payload)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Scripts []StoredScript `json:"scripts"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Scripts) != 2 || body.Scripts[0].Name != "Shared Human Name" ||
		body.Scripts[1].Name != "shared human name_2" {
		t.Fatalf("allocated scripts = %#v", body.Scripts)
	}
	for index, want := range []string{
		"Bearer first-attested-enterprise-secret",
		"Bearer second-attested-enterprise-secret",
	} {
		stored, ok := server.runner.GetScript(body.Scripts[index].ID)
		if !ok || stored == nil || stored.Script == nil {
			t.Fatalf("stored script %d not found", index)
		}
		if got := stored.Script.Actions[0].HeaderList[0].Value; got != want {
			t.Fatalf("stored credential %d = %q, want %q", index, got, want)
		}
	}

	// A lost-response retry reaches the durable receipt before the one-shot
	// attestations and returns the exact same allocated assets.
	replay := postEnterpriseArchiveBatch(t, server, payload)
	if replay.Code != http.StatusCreated {
		t.Fatalf("replay status = %d body=%s", replay.Code, replay.Body.String())
	}
	var replayBody struct {
		Scripts []StoredScript `json:"scripts"`
	}
	if err := json.Unmarshal(replay.Body.Bytes(), &replayBody); err != nil {
		t.Fatal(err)
	}
	if len(replayBody.Scripts) != 2 || replayBody.Scripts[0].ID != body.Scripts[0].ID ||
		replayBody.Scripts[1].ID != body.Scripts[1].ID ||
		replayBody.Scripts[1].Name != "shared human name_2" {
		t.Fatalf("replayed allocation = %#v, first = %#v", replayBody.Scripts, body.Scripts)
	}
}

func TestHandleSaveEnterpriseArchiveImportRejectsUnavailableCredentialWithoutCapability(t *testing.T) {
	server := NewServer(ServerConfig{})
	script := enterpriseBatchTestScript("Unavailable credential", storage.DefaultProjectID)
	actions := script["actions"].([]any)
	action := actions[0].(map[string]any)
	action["header_list"] = []any{
		map[string]any{"name": "Authorization", "value": "Bearer [REDACTED]"},
	}

	response := postEnterpriseArchiveBatch(t, server, map[string]any{
		"operation_id": "enterprise-missing-capability",
		"scripts":      []any{script},
	})
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d body=%s", response.Code, http.StatusConflict, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "missing its server capability") {
		t.Fatalf("response did not explain the save gate: %s", response.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["code"] != enterpriseArchiveDraftInvalidCode {
		t.Fatalf("response code = %q, want %q", body["code"], enterpriseArchiveDraftInvalidCode)
	}
	if scripts := server.runner.ListScripts(); len(scripts) != 0 {
		t.Fatalf("rejected batch stored scripts: %#v", scripts)
	}
}

func TestHandleSaveEnterpriseArchiveImportCodesInvalidAttestationForFreshConversion(t *testing.T) {
	server := NewServer(ServerConfig{})
	script := enterpriseBatchTestScript("Expired capability", storage.DefaultProjectID)
	metadata := script["metadata"].(map[string]any)
	importMetadata := metadata["import"].(map[string]any)
	importMetadata["preserved"] = map[string]any{
		importDraftAttestationMetadataKey: "expired-enterprise-capability",
	}

	response := postEnterpriseArchiveBatch(t, server, map[string]any{
		"operation_id": "enterprise-expired-capability",
		"scripts":      []any{script},
	})
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d body=%s", response.Code, http.StatusConflict, response.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["code"] != enterpriseArchiveDraftInvalidCode || !strings.Contains(body["error"], "invalid or expired") {
		t.Fatalf("response = %#v, want coded expired-draft guidance", body)
	}
	if scripts := server.runner.ListScripts(); len(scripts) != 0 {
		t.Fatalf("invalid attestation stored scripts: %#v", scripts)
	}
}

func TestHandleSaveEnterpriseArchiveImportReportsBusyProtectedDraftAsRetryable(t *testing.T) {
	// Another request (an in-flight guided-repair inspection or a duplicate
	// save) holds the protected draft when this save arrives.
	server, masked, manager, held := protectedDraftHeldByAnotherRequest(t)
	payload := map[string]any{"operation_id": "enterprise-busy-draft", "scripts": masked}
	assertImportDraftBusyResponse(t, postEnterpriseArchiveBatch(t, server, payload))
	if scripts := server.runner.ListScripts(); len(scripts) != 0 {
		t.Fatalf("busy save stored scripts: %#v", scripts)
	}

	if err := manager.Release(held.Claim); err != nil {
		t.Fatal(err)
	}
	saved := postEnterpriseArchiveBatch(t, server, payload)
	if saved.Code != http.StatusCreated {
		t.Fatalf("retry status = %d body=%s", saved.Code, saved.Body.String())
	}
}

// The review saves a validated archive it repaired as reviewed: validate,
// inspect, one select_tls apply and save, with no conversion in between. The
// server restores the exact repaired candidate under the requested name.
func TestHandleSaveEnterpriseArchiveImportSavesValidatedGuidedRepairWithoutConversion(t *testing.T) {
	flow := validateAndRepairGuidedTLSArchive(t)
	draft := cloneEnterpriseDraftValue(t, flow.repaired)
	draft["name"] = "Repaired TLS checkout"
	response := postEnterpriseArchiveBatch(t, flow.server, map[string]any{
		"operation_id": "guided-repair-save",
		"scripts":      []any{draft},
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("save of the repaired draft = %d body=%s", response.Code, response.Body.String())
	}
	stored := storedEnterpriseBatchScript(t, flow.server, response)
	if stored.Name != "Repaired TLS checkout" {
		t.Fatalf("stored name = %q, want the name the review requested", stored.Name)
	}
	if tls := stored.Config.HTTP.TLS; tls.MinVersion != "1.2" || tls.MaxVersion != "1.3" {
		t.Fatalf("stored TLS = %#v, want the repaired 1.2-1.3 range", tls)
	}
	findings, err := importreview.FindingsFromScript(stored)
	if err != nil {
		t.Fatal(err)
	}
	for _, finding := range findings {
		if finding.Code == "legacy_tls_replacement" {
			t.Fatalf("stored script still requires a TLS repair at %s: %+v", finding.ActionPath, findings)
		}
	}
	// Only the repair writes migration evidence; a conversion of the archive
	// would have stamped both settings as gates again.
	var migrated []string
	if err := dslwalk.Walk(stored, func(ref dslwalk.SurfaceRef) error {
		if dslwalk.MetadataString(ref.Metadata(), "source_function") == "web_set_sockets_option" &&
			strings.Contains(dslwalk.MetadataString(ref.Metadata(), "migration_change"), "TLS 1.2–1.3") {
			migrated = append(migrated, ref.Path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(migrated, ",") != strings.Join(flow.gates, ",") {
		t.Fatalf("migration evidence on %v, want every TLS step the validation gated %v", migrated, flow.gates)
	}
	// The saved copy holds the exact value the protected candidate kept.
	raw, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), guidedTLSRepairCredential) {
		t.Fatal("stored script lost the protected candidate's credential")
	}
	// The saved evidence is the preflight the repair restamped, not the
	// validation's, which both TLS gates failed.
	if preflight := preservedReplayPreflight(stored); preflight == nil || !preflight.Valid ||
		replayPreflightCheckStatus(preflight, "review_gates") != "pass" {
		t.Fatalf("stored preflight = %+v, want the one restamped from the repaired draft", preflight)
	}
	// The restamped evidence holds counts and codes, which disclosure keeps.
	assertImportEvaluationDisclosedAsStored(t, stored)
}

// The repaired candidate is restored only when the submitted draft is the
// exact one the repair returned. An edited executable field is refused, and
// the refusal leaves the repaired candidate in place for the reviewed save.
func TestHandleSaveEnterpriseArchiveImportRejectsTamperedGuidedRepairDraft(t *testing.T) {
	flow := validateAndRepairGuidedTLSArchive(t)
	for _, tc := range []struct {
		name   string
		tamper func(t *testing.T, draft map[string]any)
	}{
		// The TLS range is outside every credential binding, so only the
		// exact-repair digest refuses this edit.
		{"tls range", func(t *testing.T, draft map[string]any) {
			draft["config"].(map[string]any)["http"].(map[string]any)["tls"].(map[string]any)["max_version"] = "1.2"
		}},
		{"request target", func(t *testing.T, draft map[string]any) {
			for _, action := range draft["actions"].([]any) {
				if action := action.(map[string]any); action["target"] == "/cart" {
					action["target"] = "/cart/export"
					return
				}
			}
			t.Fatal("repaired draft has no cart request")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			draft := cloneEnterpriseDraftValue(t, flow.repaired)
			tc.tamper(t, draft)
			response := postEnterpriseArchiveBatch(t, flow.server, map[string]any{
				"operation_id": "guided-repair-tampered-" + strings.ReplaceAll(tc.name, " ", "-"),
				"scripts":      []any{draft},
			})
			assertEnterpriseDraftInvalidResponse(t, response)
			if scripts := flow.server.runner.ListScripts(); len(scripts) != 0 {
				t.Fatalf("tampered repaired draft stored scripts: %#v", scripts)
			}
		})
	}
	saved := postEnterpriseArchiveBatch(t, flow.server, map[string]any{
		"operation_id": "guided-repair-after-tamper",
		"scripts":      []any{cloneEnterpriseDraftValue(t, flow.repaired)},
	})
	if saved.Code != http.StatusCreated {
		t.Fatalf("save of the untouched repaired draft after a refusal = %d body=%s", saved.Code, saved.Body.String())
	}
	if tls := storedEnterpriseBatchScript(t, flow.server, saved).Config.HTTP.TLS; tls.MaxVersion != "1.3" {
		t.Fatalf("stored TLS = %#v, want the repaired range rather than a tampered one", tls)
	}
}

// The repair consumed the validated candidate. A review still holding the
// pre-repair draft, and its old token, cannot save it in place of the repair.
func TestHandleSaveEnterpriseArchiveImportRejectsPreRepairDraftAfterRepair(t *testing.T) {
	flow := validateAndRepairGuidedTLSArchive(t)
	response := postEnterpriseArchiveBatch(t, flow.server, map[string]any{
		"operation_id": "guided-repair-pre-repair",
		"scripts":      []any{cloneEnterpriseDraftValue(t, flow.validated)},
	})
	assertEnterpriseDraftInvalidResponse(t, response)
	if scripts := flow.server.runner.ListScripts(); len(scripts) != 0 {
		t.Fatalf("pre-repair draft stored scripts: %#v", scripts)
	}
	saved := postEnterpriseArchiveBatch(t, flow.server, map[string]any{
		"operation_id": "guided-repair-after-pre-repair",
		"scripts":      []any{cloneEnterpriseDraftValue(t, flow.repaired)},
	})
	if saved.Code != http.StatusCreated {
		t.Fatalf("save of the repaired draft after the stale refusal = %d body=%s", saved.Code, saved.Body.String())
	}
	if tls := storedEnterpriseBatchScript(t, flow.server, saved).Config.HTTP.TLS; tls.MinVersion != "1.2" || tls.MaxVersion != "1.3" {
		t.Fatalf("stored TLS = %#v, want the repaired 1.2-1.3 range", tls)
	}
}

// The review's atomic batch sends a repaired validated draft as reviewed next
// to a fresh conversion of another archive. Both candidates are restored and
// committed together.
func TestHandleSaveEnterpriseArchiveImportSavesGuidedRepairBesideFreshConversion(t *testing.T) {
	flow := validateAndRepairGuidedTLSArchive(t)
	other := buildEnterpriseArchiveProtocolArchive(t, "Web", "Action()\n{\n\tweb_add_header(\"Authorization\", \"Bearer fresh-conversion-synthetic-token\");\n\tweb_url(\"catalog\", \"URL=https://example.test/catalog\", \"Resource=0\", LAST);\n\treturn 0;\n}\n")
	converted := serveEnterpriseArchiveDraft(t, flow.server, "/api/v1/scripts/import/enterprise-script-archive", other)
	repaired := cloneEnterpriseDraftValue(t, flow.repaired)
	repaired["name"] = "Repaired TLS checkout"
	converted["name"] = "Fresh catalog"
	response := postEnterpriseArchiveBatch(t, flow.server, map[string]any{
		"operation_id": "guided-repair-mixed-batch",
		"scripts":      []any{repaired, converted},
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("mixed batch save = %d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Scripts []StoredScript `json:"scripts"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Scripts) != 2 || body.Scripts[0].Name != "Repaired TLS checkout" || body.Scripts[1].Name != "Fresh catalog" {
		t.Fatalf("mixed batch stored %#v, want the repaired and the converted script", body.Scripts)
	}
	for index, want := range []string{guidedTLSRepairCredential, "Bearer fresh-conversion-synthetic-token"} {
		stored, ok := flow.server.runner.GetScript(body.Scripts[index].ID)
		if !ok || stored == nil || stored.Script == nil {
			t.Fatalf("stored script %d not found", index)
		}
		raw, err := json.Marshal(stored.Script)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), want) {
			t.Fatalf("stored script %d lost its protected credential", index)
		}
		tls := stored.Script.Config.HTTP.TLS
		if repairedLane := index == 0; repairedLane != (tls.MinVersion == "1.2" && tls.MaxVersion == "1.3") {
			t.Fatalf("stored script %d TLS = %#v; only the repaired lane selects 1.2-1.3", index, tls)
		}
	}
}

// A guided repair saved from the review is still an ordinary
// credential-preserving import. The repair restamps the fidelity report, whose
// counts are not credentials, so the editor's next save may add a parameter or
// retarget a request that does not carry the kept credential. Only a change to
// the execution context of the request that carries it is refused.
//
// The candidate is the saved-import fixture registered directly, which has no
// source review; TestHandleSaveEnterpriseArchiveImportGuidedRepairOfArchiveKeepsLaterEditorSavesOpen
// runs the same saves on a converted archive.
func TestHandleSaveEnterpriseArchiveImportGuidedRepairKeepsLaterEditorSavesOpen(t *testing.T) {
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{Key: bytes.Repeat([]byte{0x5d}, 32), KeyID: "guided-repair-later-edits"})
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	server := NewServer(ServerConfig{Store: store, BrunoArtifactDir: t.TempDir(), BrunoArtifactWrapper: wrapper})
	draft := storedRepairScript("guided-repair-later-edits", "http")
	id, _, err := server.registerImportCandidateSession(httptest.NewRequest(http.MethodPost, "/", nil), enterpriseArchiveSaveProfile, draft)
	if err != nil {
		t.Fatal(err)
	}
	token := importDraftProtectedTokenPrefix + id
	applied := postGuidedImportRepair(t, server, "apply", ImportRepairRequest{
		CandidateID: token, Revision: token, ProjectID: draft.ProjectID,
		Finding: "legacy_tls_replacement", ActionPath: "init[0]",
		Operation: ImportRepairOperation{Type: "select_tls", TLSMin: "1.2", TLSMax: "1.3"},
	})
	response := postEnterpriseArchiveBatch(t, server, map[string]any{
		"operation_id": "guided-repair-later-edits",
		"scripts":      []any{cloneEnterpriseDraftValue(t, applied.Script)},
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("save of the repaired draft = %d body=%s", response.Code, response.Body.String())
	}
	saved := storedEnterpriseBatchScript(t, server, response)
	if !isCredentialPreservingImport(saved) || saved.Actions[0].Headers["Authorization"] != "Bearer "+storedRepairCredential {
		t.Fatal("the saved repair is not a credential-preserving import holding its kept credential")
	}
	assertImportEvaluationDisclosedAsStored(t, saved)

	if edited := putAuthoringEdit(t, server, saved.ID, addAuthoringParameter); edited.Code != http.StatusOK {
		t.Fatalf("PUT adding a parameter = %d %s, want 200", edited.Code, edited.Body.String())
	}
	if edited := putAuthoringEdit(t, server, saved.ID, retargetAuthoringAction("home", "/home")); edited.Code != http.StatusOK {
		t.Fatalf("PUT retargeting a request without the kept credential = %d %s, want 200", edited.Code, edited.Body.String())
	}
	refused := putAuthoringEdit(t, server, saved.ID, retargetAuthoringAction("orders", "/orders/export"))
	if refused.Code != http.StatusBadRequest || !strings.Contains(refused.Body.String(), "execution context for retained imported credentials at actions[0]") {
		t.Fatalf("PUT retargeting the kept credential's request = %d %s, want the refusal at that request", refused.Code, refused.Body.String())
	}

	current, err := store.GetScript(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := current.Actions[0].Headers["Authorization"]; got != "Bearer "+storedRepairCredential || current.Actions[0].Target != "/orders" {
		t.Fatalf("kept credential request = %q %q, want it unchanged", current.Actions[0].Target, got)
	}
	if len(current.Parameters) != 1 || current.Parameters[0].Name != "region" || current.Init[1].Target != "/home" {
		t.Fatalf("the stored script lost an accepted edit: parameters=%+v home=%q", current.Parameters, current.Init[1].Target)
	}
	if tls := current.Config.HTTP.TLS; tls.MinVersion != "1.2" || tls.MaxVersion != "1.3" {
		t.Fatalf("stored TLS = %#v, want the repaired range", tls)
	}
}

// A converted archive keeps its source text in the preserved source review,
// and that text quotes the Authorization header the conversion kept. The quote
// is review evidence that no run reads, so after the reviewed save of a
// repaired archive the editor may add a parameter or retarget a request without
// the kept credential. Moving the kept credential's own request to another host
// is still refused at that request.
func TestHandleSaveEnterpriseArchiveImportGuidedRepairOfArchiveKeepsLaterEditorSavesOpen(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	flow := validateAndRepairGuidedTLSArchiveWithStore(t, store)
	response := postEnterpriseArchiveBatch(t, flow.server, map[string]any{
		"operation_id": "guided-repair-archive-later-edits",
		"scripts":      []any{cloneEnterpriseDraftValue(t, flow.repaired)},
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("save of the repaired archive = %d body=%s", response.Code, response.Body.String())
	}
	saved := storedEnterpriseBatchScript(t, flow.server, response)
	if !isCredentialPreservingImport(saved) {
		t.Fatal("the saved archive is not a credential-preserving import")
	}
	assertSourceReviewQuotesMaskedCredential(t, saved, guidedTLSRepairCredential)
	credentialPath := namedActionPath(t, saved, "orders")

	if edited := putAuthoringEdit(t, flow.server, saved.ID, addAuthoringParameter); edited.Code != http.StatusOK {
		t.Fatalf("PUT adding a parameter = %d %s, want 200", edited.Code, edited.Body.String())
	}
	if edited := putAuthoringEdit(t, flow.server, saved.ID, retargetAuthoringAction("cart", "https://example.test/cart/summary")); edited.Code != http.StatusOK {
		t.Fatalf("PUT retargeting a request without the kept credential = %d %s, want 200", edited.Code, edited.Body.String())
	}
	refused := putAuthoringEdit(t, flow.server, saved.ID, retargetAuthoringAction("orders", "https://other.example.test/orders"))
	if refused.Code != http.StatusBadRequest || !strings.Contains(refused.Body.String(), "execution context for retained imported credentials at "+credentialPath) {
		t.Fatalf("PUT moving the kept credential's request to another host = %d %s, want the refusal at %s", refused.Code, refused.Body.String(), credentialPath)
	}
	if strings.Contains(refused.Body.String(), "guided-repair-synthetic-token") {
		t.Fatal("the refusal disclosed the kept credential")
	}

	current, err := store.GetScript(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	orders, cart := namedAction(t, current, "orders"), namedAction(t, current, "cart")
	if orders.Headers["Authorization"] != guidedTLSRepairCredential || orders.Target != namedAction(t, saved, "orders").Target {
		t.Fatalf("kept credential request = %q %q, want it unchanged", orders.Target, orders.Headers["Authorization"])
	}
	if len(current.Parameters) != 1 || current.Parameters[0].Name != "region" || cart.Target != "https://example.test/cart/summary" {
		t.Fatalf("the stored script lost an accepted edit: parameters=%+v cart=%q", current.Parameters, cart.Target)
	}
	if tls := current.Config.HTTP.TLS; tls.MinVersion != "1.2" || tls.MaxVersion != "1.3" {
		t.Fatalf("stored TLS = %#v, want the repaired range", tls)
	}
	assertSourceReviewQuotesMaskedCredential(t, current, guidedTLSRepairCredential)
}

// assertSourceReviewQuotesMaskedCredential fails unless the script's preserved
// source review quotes credential and disclosure masks that quote, which is
// what made the review read as a kept, script-wide credential.
func assertSourceReviewQuotesMaskedCredential(t *testing.T, script *dsl.Script, credential string) {
	t.Helper()
	masked := maskScriptSecrets(script)
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil ||
		masked == nil || masked.Metadata == nil || masked.Metadata.Import == nil {
		t.Fatal("script has no import metadata to disclose")
	}
	stored := mustMarshalJSON(t, script.Metadata.Import.Preserved["source_review"])
	if !strings.Contains(stored, credential) {
		t.Fatalf("preserved source review does not quote the kept credential: %s", stored)
	}
	if disclosed := mustMarshalJSON(t, masked.Metadata.Import.Preserved["source_review"]); strings.Contains(disclosed, credential) || disclosed == stored {
		t.Fatal("disclosure did not mask the source review's quote of the kept credential")
	}
}

func namedAction(t *testing.T, script *dsl.Script, name string) dsl.Action {
	t.Helper()
	return script.Actions[namedActionIndex(t, script, name)]
}

// namedActionPath is the disclosure-safe path a refusal names for the action.
func namedActionPath(t *testing.T, script *dsl.Script, name string) string {
	t.Helper()
	return fmt.Sprintf("actions[%d]", namedActionIndex(t, script, name))
}

func namedActionIndex(t *testing.T, script *dsl.Script, name string) int {
	t.Helper()
	for index, action := range script.Actions {
		if action.Name == name {
			return index
		}
	}
	t.Fatalf("script has no action named %q", name)
	return -1
}

// guidedTLSRepairCredential makes the archive's draft a protected candidate,
// which guided repair requires.
const guidedTLSRepairCredential = "Bearer guided-repair-synthetic-token"

// guidedTLSRepairAction has two legacy SSL settings, so validation stamps two
// legacy_tls_replacement gates that one select_tls apply resolves.
var guidedTLSRepairAction = "Action()\n{\n" +
	"\tweb_set_sockets_option(\"SSL_VERSION\", \"2&3\");\n" +
	"\tweb_add_header(\"Authorization\", \"" + guidedTLSRepairCredential + "\");\n" +
	"\tweb_url(\"orders\", \"URL=https://example.test/orders\", \"Resource=0\", LAST);\n" +
	"\tweb_set_sockets_option(\"SSL_VERSION\", \"AUTO\");\n" +
	"\tweb_url(\"cart\", \"URL=https://example.test/cart\", \"Resource=0\", LAST);\n" +
	"\treturn 0;\n}\n"

type guidedRepairArchiveFlow struct {
	server *Server
	// validated is the draft the review held before the repair, with the
	// validation's token; repaired is the apply response's script.
	validated map[string]any
	repaired  map[string]any
	gates     []string
}

// validateAndRepairGuidedTLSArchive validates the TLS archive, inspects its
// protected candidate for the gates, and applies one select_tls repair.
func validateAndRepairGuidedTLSArchive(t *testing.T) guidedRepairArchiveFlow {
	t.Helper()
	return validateAndRepairGuidedTLSArchiveWithStore(t, nil)
}

// newGuidedRepairArchiveServer can hold protected import candidates. The
// editor's later saves need durable revisions, so a store is optional.
func newGuidedRepairArchiveServer(t *testing.T, store *storage.SQLiteStore) *Server {
	t.Helper()
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{Key: bytes.Repeat([]byte{0x5c}, 32), KeyID: "guided-repair-save"})
	if err != nil {
		t.Fatal(err)
	}
	config := ServerConfig{BrunoArtifactDir: t.TempDir(), BrunoArtifactWrapper: wrapper}
	if store != nil {
		config.Store = store
	}
	return NewServer(config)
}

// guidedRepairArchiveValidatePath scopes a store-backed validation to the
// default project, which project-scoped import access requires.
func guidedRepairArchiveValidatePath(store *storage.SQLiteStore) string {
	const path = "/api/v1/scripts/import/enterprise-script-archive/validate"
	if store == nil {
		return path
	}
	return path + "?project_id=" + storage.DefaultProjectID
}

// validateAndRepairGuidedTLSArchiveWithStore runs the guided TLS repair flow on
// a server backed by store, or on an in-memory server when store is nil.
func validateAndRepairGuidedTLSArchiveWithStore(t *testing.T, store *storage.SQLiteStore) guidedRepairArchiveFlow {
	t.Helper()
	server := newGuidedRepairArchiveServer(t, store)
	archive := buildEnterpriseArchiveProtocolArchive(t, "Web", guidedTLSRepairAction)
	validated := serveEnterpriseArchiveDraft(t, server, guidedRepairArchiveValidatePath(store), archive)
	validatedScript := enterpriseDraftScript(t, validated)
	findings, err := importreview.FindingsFromScript(validatedScript)
	if err != nil {
		t.Fatal(err)
	}
	var gates []string
	for _, finding := range findings {
		if finding.Code == "legacy_tls_replacement" {
			gates = append(gates, finding.ActionPath)
		}
	}
	if len(gates) != 2 {
		t.Fatalf("validation TLS findings at %v, want one per legacy SSL setting; findings %+v", gates, findings)
	}
	token := importDraftAttestationToken(validatedScript)
	if !strings.HasPrefix(token, importDraftProtectedTokenPrefix) {
		t.Fatalf("validated draft has no protected candidate: %q", token)
	}

	inspected := postGuidedImportRepair(t, server, "preview", ImportRepairRequest{
		CandidateID: token, Revision: token, ProjectID: validatedScript.ProjectID,
		Operation: ImportRepairOperation{Type: "inspect"},
	})
	var inspectedGates []string
	for _, finding := range inspected.Findings {
		if finding.Code == "legacy_tls_replacement" {
			inspectedGates = append(inspectedGates, finding.ActionPath)
		}
	}
	if strings.Join(inspectedGates, ",") != strings.Join(gates, ",") {
		t.Fatalf("inspection TLS findings at %v, want the validation's %v", inspectedGates, gates)
	}

	applied := postGuidedImportRepair(t, server, "apply", ImportRepairRequest{
		CandidateID: token, Revision: token, ProjectID: validatedScript.ProjectID,
		Finding: "legacy_tls_replacement", ActionPath: inspectedGates[0],
		Operation: ImportRepairOperation{Type: "select_tls", TLSMin: "1.2", TLSMax: "1.3"},
	})
	if applied.CandidateID == token || !strings.HasPrefix(applied.CandidateID, importDraftProtectedTokenPrefix) {
		t.Fatalf("apply candidate = %q, want a new protected candidate replacing %q", applied.CandidateID, token)
	}
	for _, finding := range applied.Findings {
		if finding.Code == "legacy_tls_replacement" {
			t.Fatalf("one select_tls apply left a TLS gate at %s", finding.ActionPath)
		}
	}
	repaired := applied.Script
	repairedScript := enterpriseDraftScript(t, repaired)
	if got := importDraftAttestationToken(repairedScript); got != applied.CandidateID {
		t.Fatalf("repaired draft token = %q, want the apply candidate %q", got, applied.CandidateID)
	}
	if preflight := preservedReplayPreflight(repairedScript); preflight == nil || !preflight.Valid ||
		replayPreflightCheckStatus(preflight, "review_gates") != "pass" {
		t.Fatalf("repaired draft preflight = %+v, want the restamped, valid one", preflight)
	}
	return guidedRepairArchiveFlow{server: server, validated: validated, repaired: repaired, gates: gates}
}

// guidedImportRepairResponse keeps the script as the browser receives it, so
// a save sends back exactly what the review holds.
type guidedImportRepairResponse struct {
	CandidateID string                 `json:"candidate_id"`
	Script      map[string]any         `json:"script"`
	Findings    []importreview.Finding `json:"findings"`
}

func postGuidedImportRepair(t *testing.T, server *Server, phase string, req ImportRepairRequest) guidedImportRepairResponse {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/repairs/"+phase, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("repair %s = %d body=%s", phase, response.Code, response.Body.String())
	}
	var decoded guidedImportRepairResponse
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Script == nil {
		t.Fatalf("repair %s returned no script: %s", phase, response.Body.String())
	}
	return decoded
}

// serveEnterpriseArchiveDraft posts an archive to the validate or conversion
// route and returns the draft script as the browser receives it.
func serveEnterpriseArchiveDraft(t *testing.T, server *Server, path string, archive []byte) map[string]any {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(archive))
	request.Header.Set("Content-Type", "application/zip")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("%s = %d body=%s", path, response.Code, response.Body.String())
	}
	var body struct {
		Script map[string]any `json:"script"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Script == nil {
		t.Fatalf("%s returned no script: %s", path, response.Body.String())
	}
	return body.Script
}

func assertEnterpriseDraftInvalidResponse(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d body=%s", response.Code, http.StatusConflict, response.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["code"] != enterpriseArchiveDraftInvalidCode {
		t.Fatalf("response = %#v, want code %q", body, enterpriseArchiveDraftInvalidCode)
	}
}

func storedEnterpriseBatchScript(t *testing.T, server *Server, response *httptest.ResponseRecorder) *dsl.Script {
	t.Helper()
	var body struct {
		Scripts []StoredScript `json:"scripts"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Scripts) != 1 {
		t.Fatalf("save response scripts = %#v, want one", body.Scripts)
	}
	stored, ok := server.runner.GetScript(body.Scripts[0].ID)
	if !ok || stored == nil || stored.Script == nil {
		t.Fatalf("stored script %q not found", body.Scripts[0].ID)
	}
	return stored.Script
}

func cloneEnterpriseDraftValue(t *testing.T, draft map[string]any) map[string]any {
	t.Helper()
	var clone map[string]any
	if err := json.Unmarshal([]byte(mustMarshalJSON(t, draft)), &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

func enterpriseDraftScript(t *testing.T, draft map[string]any) *dsl.Script {
	t.Helper()
	var script dsl.Script
	if err := json.Unmarshal([]byte(mustMarshalJSON(t, draft)), &script); err != nil {
		t.Fatal(err)
	}
	return &script
}

func postEnterpriseArchiveBatch(t *testing.T, server *Server, payload map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/enterprise-script-archive/save", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	return response
}

func enterpriseBatchTestScript(name, projectID string) map[string]any {
	return map[string]any{
		"name": name, "version": "1.0.0", "protocol": "http", "project_id": projectID,
		"metadata": map[string]any{"import": map[string]any{"source": enterpriseArchiveSaveProfile}},
		"config":   map[string]any{"base_url": "https://api.example.test"},
		"actions":  []any{map[string]any{"name": "Request", "type": "request", "method": "GET", "target": "/"}},
	}
}

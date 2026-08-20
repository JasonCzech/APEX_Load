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

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/recorder"
	"github.com/jasonczech/apexload/pkg/storage"
)

type failSecondGenericImportScriptStore struct {
	*libraryAtomicForwardingStore
	mu    sync.Mutex
	count int
}

func (s *failSecondGenericImportScriptStore) CreateScriptIfAbsent(script *dsl.Script) (bool, error) {
	s.mu.Lock()
	s.count++
	count := s.count
	s.mu.Unlock()
	if count == 2 {
		return false, errors.New("injected second generic import script failure")
	}
	return s.libraryAtomicForwardingStore.CreateScriptIfAbsent(script)
}

func TestCommitGenericImportBatchRollsBackEveryScriptOnLaterFailure(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "generic-batch-rollback.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	store := &failSecondGenericImportScriptStore{
		libraryAtomicForwardingStore: &libraryAtomicForwardingStore{Store: base},
	}
	server := NewServer(ServerConfig{Store: store})
	response := postGenericImportBatch(t, server, map[string]any{
		"operation_id":   "generic-batch-rollback",
		"source_profile": "raml",
		"scripts": []any{
			genericImportBatchTestScript("First", storage.DefaultProjectID, ""),
			genericImportBatchTestScript("Second", storage.DefaultProjectID, ""),
		},
	})
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 body=%s", response.Code, response.Body.String())
	}
	if scripts, err := base.ListScripts(storage.DefaultProjectID, nil); err != nil {
		t.Fatal(err)
	} else if len(scripts) != 0 {
		t.Fatalf("failed atomic batch left scripts behind: %#v", scripts)
	}
}

func TestCommitGenericImportBatchRestoresOpaqueCredentialAndReplaysReceipt(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "generic-batch-replay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})
	const credential = "generic-import-replay-canary"
	script := genericImportBatchScriptWithCredential("RAML checkout", credential)
	preview := server.newScriptImportResponseWithSourceSecrets(
		httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/raml", nil),
		scriptImportResponseOptions{Script: script, Scripts: []*dsl.Script{script}},
	)
	masked, ok := preview["scripts"].([]any)
	if !ok || len(masked) != 1 {
		t.Fatalf("preview scripts = %#v", preview["scripts"])
	}
	encoded, err := json.Marshal(masked)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(credential)) {
		t.Fatalf("preview disclosed source credential: %s", encoded)
	}
	payload := map[string]any{
		"operation_id":   "generic-opaque-replay",
		"source_profile": "raml",
		"scripts":        masked,
	}
	created := postGenericImportBatch(t, server, payload)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", created.Code, created.Body.String())
	}
	if strings.Contains(created.Body.String(), credential) {
		t.Fatalf("create response disclosed source credential: %s", created.Body.String())
	}
	var first struct {
		Scripts []StoredScript `json:"scripts"`
		Count   int            `json:"count"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if first.Count != 1 || len(first.Scripts) != 1 {
		t.Fatalf("create response = %#v", first)
	}
	stored, ok := server.runner.GetScript(first.Scripts[0].ID)
	if !ok || stored == nil || stored.Script == nil {
		t.Fatal("durable script is unavailable")
	}
	if got := stored.Script.Actions[0].HeaderList[0].Value; got != "Bearer "+credential {
		t.Fatalf("durable credential = %q, want exact source value", got)
	}

	replayed := postGenericImportBatch(t, server, payload)
	if replayed.Code != http.StatusCreated {
		t.Fatalf("replay status = %d body=%s", replayed.Code, replayed.Body.String())
	}
	var second struct {
		Scripts []StoredScript `json:"scripts"`
	}
	if err := json.Unmarshal(replayed.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if len(second.Scripts) != 1 || second.Scripts[0].ID != first.Scripts[0].ID {
		t.Fatalf("replay scripts = %#v, want original id %q", second.Scripts, first.Scripts[0].ID)
	}
}

func TestCommitGenericImportBatchRequiresOperationID(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "generic-batch-operation-id.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})

	response := postGenericImportBatch(t, server, map[string]any{
		"source_profile": "raml",
		"scripts": []any{
			genericImportBatchTestScript("Missing operation", storage.DefaultProjectID, ""),
		},
	})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "operation_id is required") {
		t.Fatalf("body = %s, want operation_id guidance", response.Body.String())
	}
	if scripts, err := store.ListScripts(storage.DefaultProjectID, nil); err != nil {
		t.Fatal(err)
	} else if len(scripts) != 0 {
		t.Fatalf("missing operation id persisted scripts: %#v", scripts)
	}
}

func TestCommitGenericImportBatchRejectsCrossProfileDraftWithoutConsumingCapability(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "generic-batch-profile-fence.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})
	const credential = "profile-fence-canary"
	script := genericImportBatchScriptWithCredential("RAML profile fence", credential)
	preview := server.newScriptImportResponseWithSourceSecrets(
		httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/raml", nil),
		scriptImportResponseOptions{Script: script, Scripts: []*dsl.Script{script}},
	)
	masked, ok := preview["scripts"].([]any)
	if !ok || len(masked) != 1 {
		t.Fatalf("preview scripts = %#v", preview["scripts"])
	}

	rejected := postGenericImportBatch(t, server, map[string]any{
		"operation_id":   "cross-profile-rejected",
		"source_profile": "k6",
		"scripts":        masked,
	})
	if rejected.Code != http.StatusConflict {
		t.Fatalf("cross-profile status = %d, want 409 body=%s", rejected.Code, rejected.Body.String())
	}
	if scripts, err := store.ListScripts(storage.DefaultProjectID, nil); err != nil {
		t.Fatal(err)
	} else if len(scripts) != 0 {
		t.Fatalf("cross-profile request persisted scripts: %#v", scripts)
	}

	accepted := postGenericImportBatch(t, server, map[string]any{
		"operation_id":   "profile-capability-still-valid",
		"source_profile": "raml",
		"scripts":        masked,
	})
	if accepted.Code != http.StatusCreated {
		t.Fatalf("correct-profile status = %d, want 201 body=%s", accepted.Code, accepted.Body.String())
	}
	var saved struct {
		Scripts []StoredScript `json:"scripts"`
	}
	if err := json.Unmarshal(accepted.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Scripts) != 1 {
		t.Fatalf("saved scripts = %#v, want one", saved.Scripts)
	}
	stored, ok := server.runner.GetScript(saved.Scripts[0].ID)
	if !ok || stored == nil || stored.Script == nil {
		t.Fatal("durable script is unavailable")
	}
	if got := stored.Script.Actions[0].HeaderList[0].Value; got != "Bearer "+credential {
		t.Fatalf("durable credential = %q, want exact source value", got)
	}
}

func TestCommitGenericImportBatchRestartDoesNotUseLostCapabilityForNewCreate(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "generic-restart-new-create.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	firstServer := NewServer(ServerConfig{Store: store})
	script := genericImportBatchScriptWithCredential("Restart invalid capability", "restart-new-create-secret")
	preview := firstServer.newScriptImportResponseWithSourceSecrets(
		httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/raml", nil),
		scriptImportResponseOptions{Script: script, Scripts: []*dsl.Script{script}},
	)
	masked := preview["scripts"].([]any)

	// No first save occurred, so there are no receipt-bound deterministic assets
	// to adopt. A new process must reject the now-unknown token rather than using
	// restart recovery as authority for a fresh credentialed create.
	restarted := NewServer(ServerConfig{Store: store})
	response := postGenericImportBatch(t, restarted, map[string]any{
		"operation_id":   "generic-restart-must-not-create",
		"source_profile": "raml",
		"scripts":        masked,
	})
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "invalid or expired") {
		t.Fatalf("status = %d, want 409 invalid capability body=%s", response.Code, response.Body.String())
	}
	if scripts, err := store.ListScripts(storage.DefaultProjectID, nil); err != nil {
		t.Fatal(err)
	} else if len(scripts) != 0 {
		t.Fatalf("lost capability authorized a new create: %#v", scripts)
	}
}

func TestCommitGenericImportBatchAcceptsCompoundWorkflowProfiles(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "generic-batch-profile-family.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})

	browser := genericImportBatchTestScript("Browser recording", storage.DefaultProjectID, "")
	browser["protocol"] = "browser"
	browser["config"] = map[string]any{"browser": map[string]any{"engine": "chromium"}}
	browser["actions"] = []any{map[string]any{
		"name": "Open checkout", "type": "browser_navigate", "target": "https://example.test/checkout",
	}}
	setGenericImportBatchTestScriptProfile(browser, "browser_recorder")
	httpSidecar := genericImportBatchTestScript("Browser recording HTTP", storage.DefaultProjectID, "")
	setGenericImportBatchTestScriptProfile(httpSidecar, "browser_recording_har")

	browserResponse := postGenericImportBatch(t, server, map[string]any{
		"operation_id":   "browser-profile-family",
		"source_profile": "browser_recorder",
		"scripts":        []any{browser, httpSidecar},
	})
	if browserResponse.Code != http.StatusCreated {
		t.Fatalf("browser family status = %d, want 201 body=%s", browserResponse.Code, browserResponse.Body.String())
	}

	salesforce := genericImportBatchTestScript("Native Salesforce", storage.DefaultProjectID, "")
	setGenericImportBatchTestScriptProfile(salesforce, "salesforce")
	harResponse := postGenericImportBatch(t, server, map[string]any{
		"operation_id":   "har-salesforce-profile-family",
		"source_profile": "har",
		"scripts":        []any{salesforce},
	})
	if harResponse.Code != http.StatusCreated {
		t.Fatalf("HAR Salesforce family status = %d, want 201 body=%s", harResponse.Code, harResponse.Body.String())
	}

	github := genericImportBatchTestScript("GitHub repository script", storage.DefaultProjectID, "")
	setGenericImportBatchTestScriptProfile(github, "github")
	githubResponse := postGenericImportBatch(t, server, map[string]any{
		"operation_id":   "github-exact-profile",
		"source_profile": "github",
		"scripts":        []any{github},
	})
	if githubResponse.Code != http.StatusCreated {
		t.Fatalf("GitHub exact profile status = %d, want 201 body=%s", githubResponse.Code, githubResponse.Body.String())
	}
}

func TestCommitGenericImportBatchRecordingSessionContract(t *testing.T) {
	tests := []struct {
		name        string
		profile     string
		sessionIDs  []string
		scriptCount int
		wantStatus  int
		wantMessage string
	}{
		{
			name: "recorder omission rejected", profile: "recorder", scriptCount: 1,
			wantStatus: http.StatusBadRequest, wantMessage: "require one source_recording_session_ids entry",
		},
		{
			name: "length mismatch", profile: "recorder", scriptCount: 1,
			sessionIDs: []string{"first", "second"}, wantStatus: http.StatusBadRequest,
			wantMessage: "one entry per script",
		},
		{
			name: "non-recorder profile", profile: "raml", scriptCount: 1,
			sessionIDs: []string{"unexpected-session"}, wantStatus: http.StatusConflict,
			wantMessage: "only by the proxy recorder",
		},
		{
			name: "empty recorder session", profile: "recorder", scriptCount: 1,
			sessionIDs: []string{""}, wantStatus: http.StatusBadRequest,
			wantMessage: "requires a source recording session",
		},
		{
			name: "missing recorder session", profile: "recorder", scriptCount: 1,
			sessionIDs: []string{"missing-recorder-session"}, wantStatus: http.StatusNotFound,
			wantMessage: "source recording session was not found",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "recording-contract.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			server := NewServer(ServerConfig{Store: store})
			scripts := make([]any, 0, test.scriptCount)
			for index := 0; index < test.scriptCount; index++ {
				script := genericImportBatchTestScript(fmt.Sprintf("Recording %d", index+1), storage.DefaultProjectID, "")
				setGenericImportBatchTestScriptProfile(script, test.profile)
				scripts = append(scripts, script)
			}
			payload := map[string]any{
				"operation_id":   "recording-contract-" + strings.ReplaceAll(test.name, " ", "-"),
				"source_profile": test.profile,
				"scripts":        scripts,
			}
			if test.sessionIDs != nil {
				payload["source_recording_session_ids"] = test.sessionIDs
			}
			response := postGenericImportBatch(t, server, payload)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d body=%s", response.Code, test.wantStatus, response.Body.String())
			}
			if test.wantMessage != "" && !strings.Contains(response.Body.String(), test.wantMessage) {
				t.Fatalf("body = %s, want %q", response.Body.String(), test.wantMessage)
			}
			if test.wantStatus != http.StatusCreated {
				stored, err := store.ListScripts(storage.DefaultProjectID, nil)
				if err != nil {
					t.Fatal(err)
				}
				if len(stored) != 0 {
					t.Fatalf("rejected request stored scripts: %#v", stored)
				}
			}
		})
	}
}

func TestCommitGenericImportBatchPersistsRecorderBaselineAndReplaysCompletedReceipt(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "recording-baseline.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})
	masked, sessionID := genericRecorderBatchPreview(t, server, "recorder-baseline-session")
	payload := map[string]any{
		"operation_id":                 "recorder-baseline-operation",
		"source_profile":               "recorder",
		"source_recording_session_ids": []string{sessionID},
		"scripts":                      masked,
	}
	created := postGenericImportBatch(t, server, payload)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", created.Code, created.Body.String())
	}
	var body struct {
		Scripts []StoredScript `json:"scripts"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Scripts) != 1 {
		t.Fatalf("created scripts = %#v", body.Scripts)
	}
	assertGenericRecorderBaseline(t, store, body.Scripts[0].ID)

	// Completed receipts replay without needing an intentionally ephemeral
	// source session or its already-consumed draft capability.
	if !server.recorder.DeleteSession(sessionID) {
		t.Fatal("delete source recording session")
	}
	replayed := postGenericImportBatch(t, server, payload)
	if replayed.Code != http.StatusCreated {
		t.Fatalf("completed replay status = %d body=%s", replayed.Code, replayed.Body.String())
	}
	var replay struct {
		Scripts []StoredScript `json:"scripts"`
	}
	if err := json.Unmarshal(replayed.Body.Bytes(), &replay); err != nil {
		t.Fatal(err)
	}
	if len(replay.Scripts) != 1 || replay.Scripts[0].ID != body.Scripts[0].ID {
		t.Fatalf("replayed scripts = %#v, want id %q", replay.Scripts, body.Scripts[0].ID)
	}

	conflicting := cloneGenericImportBatchPayload(payload)
	conflicting["source_recording_session_ids"] = []string{"different-session"}
	conflict := postGenericImportBatch(t, server, conflicting)
	if conflict.Code != http.StatusConflict || !strings.Contains(conflict.Body.String(), "different import batch") {
		t.Fatalf("digest conflict status = %d body=%s", conflict.Code, conflict.Body.String())
	}
}

func TestCommitGenericImportBatchRecoversCommittedRecorderBaselineWithoutLiveSession(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "recording-baseline-recovery.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	store := &failOnceDirectImportReceiptStore{SQLiteStore: base, failComplete: true}
	server := NewServer(ServerConfig{Store: store})
	masked, sessionID := genericRecorderBatchPreview(t, server, "recorder-baseline-recovery-session")
	payload := map[string]any{
		"operation_id":                 "recorder-baseline-recovery-operation",
		"source_profile":               "recorder",
		"source_recording_session_ids": []string{sessionID},
		"scripts":                      masked,
	}
	first := postGenericImportBatch(t, server, payload)
	if first.Code != http.StatusInternalServerError {
		t.Fatalf("injected completion status = %d body=%s", first.Code, first.Body.String())
	}
	differentOperation := cloneGenericImportBatchPayload(payload)
	differentOperation["operation_id"] = "recorder-baseline-recovery-different-operation"
	reuse := postGenericImportBatch(t, server, differentOperation)
	if reuse.Code != http.StatusConflict {
		t.Fatalf("post-commit capability reuse status = %d body=%s", reuse.Code, reuse.Body.String())
	}
	stored, err := base.ListScripts(storage.DefaultProjectID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 {
		t.Fatalf("committed scripts = %#v, want one", stored)
	}
	assertGenericRecorderBaseline(t, base, stored[0].ID)
	requestedJSON, err := json.Marshal(masked[0])
	if err != nil {
		t.Fatal(err)
	}
	var requested dsl.Script
	if err := json.Unmarshal(requestedJSON, &requested); err != nil {
		t.Fatal(err)
	}
	durable, ok, err := server.runner.GetScriptDurable(stored[0].ID)
	if err != nil || !ok || durable == nil {
		t.Fatalf("load committed script for recovery identity: ok=%t err=%v", ok, err)
	}
	if !recoveredImportScriptBatchIdentityMatches(
		[]*StoredScript{durable},
		[]*dsl.Script{&requested},
		func(script *dsl.Script) bool {
			return genericImportSourceProfilesCompatible("recorder", genericImportDraftProfile(script, ""))
		},
	) {
		actualIdentity, _ := json.Marshal(recoveredImportDisclosureIdentity(durable.Script))
		expectedIdentity, _ := json.Marshal(recoveredImportDisclosureIdentity(&requested))
		t.Fatalf("committed recorder recovery identity changed: actual=%s expected=%s", actualIdentity, expectedIdentity)
	}
	for _, mutation := range []struct {
		name  string
		apply func(*dsl.Script)
	}{
		{
			name: "action target",
			apply: func(script *dsl.Script) {
				script.Actions[0].Target = "/different-cart"
			},
		},
		{
			name: "credential header name",
			apply: func(script *dsl.Script) {
				value := script.Actions[0].Headers["Authorization"]
				delete(script.Actions[0].Headers, "Authorization")
				script.Actions[0].Headers["X-Different-Header"] = value
			},
		},
	} {
		t.Run("rejects changed "+mutation.name, func(t *testing.T) {
			changed := *durable
			changed.Script = cloneScriptDefinition(durable.Script)
			mutation.apply(changed.Script)
			if recoveredImportScriptBatchIdentityMatches(
				[]*StoredScript{&changed},
				[]*dsl.Script{&requested},
				func(script *dsl.Script) bool {
					return genericImportSourceProfilesCompatible("recorder", genericImportDraftProfile(script, ""))
				},
			) {
				t.Fatalf("restart recovery accepted changed %s", mutation.name)
			}
		})
	}
	if !server.recorder.DeleteSession(sessionID) {
		t.Fatal("delete source recording session")
	}

	// A restarted controller has neither the source session nor the local
	// one-shot credential/lineage attestation. Recovery must happen from the
	// exact durable receipt, deterministic script ID, and bound baseline.
	restarted := NewServer(ServerConfig{Store: store})
	recovered := postGenericImportBatch(t, restarted, payload)
	if recovered.Code != http.StatusCreated {
		t.Fatalf("recovery status = %d body=%s", recovered.Code, recovered.Body.String())
	}
	if scripts, err := base.ListScripts(storage.DefaultProjectID, nil); err != nil {
		t.Fatal(err)
	} else if len(scripts) != 1 || scripts[0].ID != stored[0].ID {
		t.Fatalf("recovery duplicated or changed assets: %#v", scripts)
	}
	assertGenericRecorderBaseline(t, base, stored[0].ID)
}

func TestCommitGenericImportBatchPreservesAttestedEmbeddedFolder(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "generic-attested-folder.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	folder := &storage.Folder{
		ID: "generic-attested-folder", ProjectID: storage.DefaultProjectID,
		Name: "Imported", Category: "scripts", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := store.SaveFolder(folder); err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{Store: store})
	script := genericImportBatchScriptWithCredential("Attested embedded folder", "generic-folder-secret")
	preview := server.newScriptImportResponseWithSourceSecrets(
		httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/raml", nil),
		scriptImportResponseOptions{Script: script, Scripts: []*dsl.Script{script}},
	)
	masked := preview["scripts"].([]any)
	masked[0].(map[string]any)["folder_id"] = folder.ID
	response := postGenericImportBatch(t, server, map[string]any{
		"operation_id": "generic-attested-embedded-folder", "source_profile": "raml", "scripts": masked,
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	stored, err := store.ListScripts(storage.DefaultProjectID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || stored[0].FolderID == nil || *stored[0].FolderID != folder.ID {
		t.Fatalf("stored folder = %#v, want %q", stored, folder.ID)
	}
}

func TestCrossPlatformGenericImporterBatchContractMatrix(t *testing.T) {
	profiles := []string{
		"broker_config", "browser_recorder", "browser_recording_har", "devweb",
		"eventhubs_capture", "github", "har", "k6", "raml", "raw_http",
		"recorder_export", "rest_json", "salesforce", "sample", "truclient", "wsdl",
	}
	for _, profile := range profiles {
		profile := profile
		t.Run(profile, func(t *testing.T) {
			store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), profile+"-batch-contract.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			now := time.Now().UTC()
			folderID := profile + "-import-folder"
			if err := store.SaveFolder(&storage.Folder{
				ID: folderID, ProjectID: storage.DefaultProjectID, Name: "Imported " + profile,
				Category: "scripts", CreatedAt: now, UpdatedAt: now,
			}); err != nil {
				t.Fatal(err)
			}
			server := NewServer(ServerConfig{Store: store})
			const credential = "cross-platform-import-contract-canary"
			scripts := []*dsl.Script{
				genericImportBatchScriptWithCredential(profile+" first", credential+"-1"),
				genericImportBatchScriptWithCredential(profile+" second", credential+"-2"),
			}
			for _, script := range scripts {
				script.Metadata.Import.Source = profile
			}
			preview := server.newScriptImportResponseWithSourceSecrets(
				httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/"+profile, nil),
				scriptImportResponseOptions{Scripts: scripts},
			)
			masked, ok := preview["scripts"].([]any)
			if !ok || len(masked) != len(scripts) {
				t.Fatalf("preview scripts = %#v", preview["scripts"])
			}
			for _, candidate := range masked {
				candidate.(map[string]any)["folder_id"] = folderID
			}
			payload := map[string]any{
				"operation_id":   "cross-platform-" + profile,
				"source_profile": profile,
				"scripts":        masked,
			}
			created := postGenericImportBatch(t, server, payload)
			if created.Code != http.StatusCreated {
				t.Fatalf("create status = %d body=%s", created.Code, created.Body.String())
			}
			if strings.Contains(created.Body.String(), credential) {
				t.Fatalf("create response disclosed protected credential: %s", created.Body.String())
			}
			stored, err := store.ListScripts(storage.DefaultProjectID, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(stored) != len(scripts) {
				t.Fatalf("stored scripts = %d, want %d", len(stored), len(scripts))
			}
			ids := make(map[string]struct{}, len(stored))
			for _, script := range stored {
				if script.FolderID == nil || *script.FolderID != folderID {
					t.Fatalf("stored destination = %#v, want %q", script.FolderID, folderID)
				}
				if len(script.Actions) == 0 || len(script.Actions[0].HeaderList) == 0 ||
					!strings.Contains(script.Actions[0].HeaderList[0].Value, credential) {
					t.Fatalf("stored credential was not restored for %q", script.Name)
				}
				ids[script.ID] = struct{}{}
			}

			// A fresh controller must replay the completed durable receipt rather
			// than consume the now-lost process-local preview capabilities or
			// create duplicate scripts.
			restarted := NewServer(ServerConfig{Store: store})
			replayed := postGenericImportBatch(t, restarted, payload)
			if replayed.Code != http.StatusCreated {
				t.Fatalf("restart replay status = %d body=%s", replayed.Code, replayed.Body.String())
			}
			after, err := store.ListScripts(storage.DefaultProjectID, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(after) != len(stored) {
				t.Fatalf("restart replay duplicated scripts: before=%d after=%d", len(stored), len(after))
			}
			for _, script := range after {
				if _, ok := ids[script.ID]; !ok {
					t.Fatalf("restart replay changed script ID to %q", script.ID)
				}
			}
		})
	}
}

func TestGenericImportSourceProfilesCompatible(t *testing.T) {
	tests := []struct {
		name      string
		requested string
		actual    string
		want      bool
	}{
		{name: "exact", requested: "raml", actual: "raml", want: true},
		{name: "GitHub exact", requested: "github", actual: "github", want: true},
		{name: "browser sidecar", requested: "browser_recorder", actual: "browser_recording_har", want: true},
		{name: "HAR Salesforce promotion", requested: "har", actual: "salesforce", want: true},
		{name: "sample alias", requested: "sample-import", actual: "sample", want: true},
		{name: "sample raw HTTP", requested: "sample", actual: "raw_http", want: true},
		{name: "sample curl", requested: "sample", actual: "curl", want: true},
		{name: "sample REST JSON", requested: "sample", actual: "rest_json", want: true},
		{name: "sample HAR", requested: "sample", actual: "har", want: true},
		{name: "browser reverse rejected", requested: "browser_recording_har", actual: "browser_recorder", want: false},
		{name: "Salesforce reverse rejected", requested: "salesforce", actual: "har", want: false},
		{name: "different converters", requested: "raml", actual: "k6", want: false},
		{name: "sample family is one way", requested: "raw_http", actual: "sample", want: false},
		{name: "unsupported actual", requested: "raml", actual: "postman", want: false},
		{name: "missing actual", requested: "raml", actual: "", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := genericImportSourceProfilesCompatible(test.requested, test.actual); got != test.want {
				t.Fatalf("compatible(%q, %q) = %t, want %t", test.requested, test.actual, got, test.want)
			}
		})
	}
}

func genericRecorderBatchPreview(t *testing.T, server *Server, sessionID string) ([]any, string) {
	t.Helper()
	session, err := server.recorder.StartSessionForProject(
		sessionID, "Recorded checkout", storage.DefaultProjectID, recorder.SessionFilters{}, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	session.BaseURL = "https://shop.example.test"
	session.Requests = append(session.Requests, recorder.RecordedRequest{
		Seq: 1, Method: http.MethodGet, URL: "https://shop.example.test/cart", Path: "/cart",
		Headers:      map[string]string{"Authorization": "Bearer restart-recording-secret"},
		ResponseBody: `{"cart":"recorded"}`,
	})
	converted := recorder.ConvertSessionWithOptions(session, "Recorded checkout", recorder.ConvertOptions{})
	if converted == nil || converted.Script == nil {
		t.Fatal("recording conversion produced no script")
	}
	converted.Script.ProjectID = storage.DefaultProjectID
	ensureCredentialPreservingImportMetadata(converted.Script, "recorder")
	snapshotDigest, err := recordingBaselineSnapshotDigest(session)
	if err != nil {
		t.Fatal(err)
	}
	preview := server.newScriptImportResponseWithSourceSecrets(
		httptest.NewRequest(http.MethodPost, "/api/v1/recordings/"+sessionID+"/preview", nil),
		scriptImportResponseOptions{
			Script: converted.Script, Scripts: []*dsl.Script{converted.Script},
			RecordingSessionID: sessionID, RecordingSnapshotDigest: snapshotDigest,
		},
	)
	masked, ok := preview["scripts"].([]any)
	if !ok || len(masked) != 1 {
		t.Fatalf("recording preview scripts = %#v", preview["scripts"])
	}
	return masked, sessionID
}

func assertGenericRecorderBaseline(t *testing.T, store storage.Store, scriptID string) {
	t.Helper()
	record, err := store.GetScriptRecording(scriptID)
	if err != nil {
		t.Fatal(err)
	}
	if record == nil || record.Source != "proxy_recording" || record.RequestCount != 1 {
		t.Fatalf("recording baseline = %#v, want one proxy request", record)
	}
}

func cloneGenericImportBatchPayload(input map[string]any) map[string]any {
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func postGenericImportBatch(t *testing.T, server *Server, payload map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/commit-batch", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	return response
}

func genericImportBatchTestScript(name, projectID, credential string) map[string]any {
	headerList := []any{}
	if credential != "" {
		headerList = append(headerList, map[string]any{"name": "Authorization", "value": "Bearer " + credential})
	}
	return map[string]any{
		"name": name, "version": "1.0.0", "protocol": "http", "project_id": projectID,
		"config":   map[string]any{"base_url": "https://example.test"},
		"metadata": map[string]any{"import": map[string]any{"source": "raml", "preserved": map[string]any{}}},
		"actions": []any{map[string]any{
			"name": name + " request", "type": "request", "method": "GET", "target": "/orders", "header_list": headerList,
		}},
	}
}

func setGenericImportBatchTestScriptProfile(script map[string]any, profile string) {
	metadata := script["metadata"].(map[string]any)
	importMetadata := metadata["import"].(map[string]any)
	importMetadata["source"] = profile
}

func genericImportBatchScriptWithCredential(name, credential string) *dsl.Script {
	return &dsl.Script{
		Name: name, Version: "1.0.0", Protocol: "http", ProjectID: storage.DefaultProjectID,
		Config:   dsl.ScriptConfig{BaseURL: "https://example.test"},
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Source: "raml", Preserved: map[string]any{}}},
		Actions: []dsl.Action{{
			Name: name + " request", Type: "request", Method: "GET", Target: "/orders",
			HeaderList: dsl.HeaderList{{Name: "Authorization", Value: "Bearer " + credential}},
		}},
	}
}

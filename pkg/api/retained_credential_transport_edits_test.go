package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/importartifact"
	"github.com/jasonczech/apexload/pkg/internal/importreview"
	"github.com/jasonczech/apexload/pkg/secretstore"
	"github.com/jasonczech/apexload/pkg/storage"
)

// A credential-preserving import pins the proxy and TLS settings its kept
// credentials are sent with, unless a system admin allows those edits.
func TestRetainedCredentialTransportEditsRequireAdminSetting(t *testing.T) {
	server, records, _, projectID := newProjectSecretAPIServer(t)
	settings := server.runner.GetSystemSettings()
	settings.ScriptImports.AllowSourceSecretMigration = true
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("enable source-secret migration: %v", err)
	}
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{
		Key:   bytes.Repeat([]byte{0x5d}, 32),
		KeyID: "transport-edit-session",
	})
	if err != nil {
		t.Fatalf("create wrapper: %v", err)
	}
	server.protectedSourceSessions = &secretstore.ProtectedSourceSessionStore{Records: records, Wrapper: wrapper}

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", "ACME.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(buildSanitizedACMEArchive(t)); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/enterprise-script-archive?project_id="+projectID, &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("import status = %d; body=%s", response.Code, response.Body.String())
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	var imported dsl.Script
	if err := json.Unmarshal(envelope["script"], &imported); err != nil {
		t.Fatal(err)
	}
	imported.ID = ""
	imported.ProjectID = projectID
	created := postSecretBindingJSON(t, server, "/api/v1/scripts", map[string]any{"project_id": projectID, "script": &imported})
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d; body=%s", created.Code, created.Body.String())
	}
	var createdBody struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &createdBody); err != nil || createdBody.ID == "" {
		t.Fatalf("created script id: %v %s", err, created.Body.String())
	}

	save := func(edit func(*dsl.Script)) *httptest.ResponseRecorder {
		t.Helper()
		get := httptest.NewRecorder()
		server.mux.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/v1/scripts/"+createdBody.ID, nil))
		if get.Code != http.StatusOK {
			t.Fatalf("get status = %d; body=%s", get.Code, get.Body.String())
		}
		var view struct {
			Script dsl.Script `json:"script"`
		}
		if err := json.Unmarshal(get.Body.Bytes(), &view); err != nil {
			t.Fatal(err)
		}
		edit(&view.Script)
		payload, err := json.Marshal(map[string]any{"script": &view.Script})
		if err != nil {
			t.Fatal(err)
		}
		put := httptest.NewRequest(http.MethodPut, "/api/v1/scripts/"+createdBody.ID, bytes.NewReader(payload))
		put.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		server.mux.ServeHTTP(recorder, put)
		return recorder
	}
	customProxy := func(script *dsl.Script) {
		script.Config.HTTP.ProxyMode = "custom"
		script.Config.HTTP.Proxy.URL = "http://proxy.example.test:8888"
	}
	insecureTLS := func(script *dsl.Script) { script.Config.HTTP.TLS.InsecureSkipVerify = true }

	for name, edit := range map[string]func(*dsl.Script){"custom proxy": customProxy, "TLS verification": insecureTLS} {
		if got := save(edit); got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "retained imported credentials") {
			t.Fatalf("%s with the setting off: status = %d; body=%s", name, got.Code, got.Body.String())
		}
	}
	if got := save(func(script *dsl.Script) { script.Config.HTTP.ProxyMode = "none" }); got.Code != http.StatusOK {
		t.Fatalf("No Proxy status = %d; body=%s", got.Code, got.Body.String())
	}

	// The fallback save used for stores without conditional persistence runs the
	// retained-credential check a second time; it must agree with the first pass.
	secondPass := func() error {
		t.Helper()
		current, ok := server.runner.GetScript(createdBody.ID)
		if !ok || current == nil || current.Script == nil {
			t.Fatal("stored script missing")
		}
		payload, err := json.Marshal(current.Script)
		if err != nil {
			t.Fatal(err)
		}
		var candidate dsl.Script
		if err := json.Unmarshal(payload, &candidate); err != nil {
			t.Fatal(err)
		}
		candidate.Config.HTTP.TLS.InsecureSkipVerify = !candidate.Config.HTTP.TLS.InsecureSkipVerify
		return server.runner.saveStoredScriptWithImportTrust(&candidate, current.Script, false, "")
	}
	if err := secondPass(); err == nil || !strings.Contains(err.Error(), "retained imported credentials") {
		t.Fatalf("fallback save with the setting off: %v, want the context refusal", err)
	}

	settings = server.runner.GetSystemSettings()
	settings.ScriptImports.AllowRetainedCredentialTransportEdits = true
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("enable transport edits: %v", err)
	}
	if err := secondPass(); err != nil {
		t.Fatalf("fallback save with the setting on: %v", err)
	}
	for name, edit := range map[string]func(*dsl.Script){"custom proxy": customProxy, "TLS verification": insecureTLS} {
		if got := save(edit); got.Code != http.StatusOK {
			t.Fatalf("%s with the setting on: status = %d; body=%s", name, got.Code, got.Body.String())
		}
	}
	// Debugging or replaying an unsaved draft accepts exactly what a save does.
	stored, ok := server.runner.GetScript(createdBody.ID)
	if !ok || stored == nil || stored.Script == nil {
		t.Fatal("stored script missing")
	}
	draft := maskScriptSecrets(stored.Script)
	draft.Config.HTTP.TLS.InsecureSkipVerify = !stored.Script.Config.HTTP.TLS.InsecureSkipVerify
	if _, _, err := resolveStoredReplayImportScript(draft, stored.Script, server.runner.preservedImportEditOptions()); err != nil {
		t.Fatalf("replay draft with the setting on: %v", err)
	}
	if _, _, err := resolveStoredReplayImportScript(draft, stored.Script, importreview.PreservedImportEditOptions{}); err == nil {
		t.Fatal("replay draft with the policy off was accepted")
	}
	// Repository content is written by people who may not be editors, so a
	// GitHub import keeps the strict check even with the setting on.
	remote := maskScriptSecrets(stored.Script)
	remote.Config.HTTP.TLS.InsecureSkipVerify = !stored.Script.Config.HTTP.TLS.InsecureSkipVerify
	if err := server.runner.PreflightGitHubScriptImport(remote, storage.GitHubScriptSyncTarget{}); err == nil ||
		!strings.Contains(err.Error(), "retained imported credentials") {
		t.Fatalf("GitHub import with the setting on: %v, want the context refusal", err)
	}
	// The setting widens only the transport route: a request header added to a
	// kept-credential request is still refused.
	if got := save(func(script *dsl.Script) { script.Config.HTTP.DefaultHeaders = map[string]string{"X-Debug": "1"} }); got.Code != http.StatusBadRequest {
		t.Fatalf("default header with the setting on: status = %d; body=%s", got.Code, got.Body.String())
	}
}

// The audit trail must say which script-import policy moved: the migration key
// keeps its original meaning, and the transport policy has its own.
func TestSystemSettingsUpdateReportsWhichScriptImportPolicyChanged(t *testing.T) {
	server := NewServer(ServerConfig{})
	service := server.systemSettingsDomainService()

	current := server.runner.GetSystemSettings()
	candidate := current
	candidate.ScriptImports.AllowRetainedCredentialTransportEdits = true
	body, err := json.Marshal(candidate)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Update(candidate, body, systemSettingsRevision(current), nil)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !result.PolicyChanged || !result.RetainedCredentialTransportChanged || result.SourceSecretMigrationChanged {
		t.Fatalf("transport-only change flags = %+v", result.systemSettingsUpdatePlan)
	}

	current = server.runner.GetSystemSettings()
	candidate = current
	candidate.ScriptImports.AllowSourceSecretMigration = true
	body, err = json.Marshal(candidate)
	if err != nil {
		t.Fatal(err)
	}
	result, err = service.Update(candidate, body, systemSettingsRevision(current), nil)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !result.PolicyChanged || !result.SourceSecretMigrationChanged || result.RetainedCredentialTransportChanged {
		t.Fatalf("migration-only change flags = %+v", result.systemSettingsUpdatePlan)
	}
}

// An authoring-session draft of an imported script that keeps credentials
// accepts the same proxy and TLS edits a save does, and refuses them otherwise.
func TestAuthoringSessionUpdateHonorsRetainedCredentialTransportPolicy(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	const projectID = "project-session-transport"
	seedAuthoringScriptProjects(t, store, projectID)
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})

	draft := &dsl.Script{
		Name: "Retained credential draft", Version: "1", Protocol: "http", ProjectID: projectID,
		Config: dsl.ScriptConfig{BaseURL: "https://origin.example.test"},
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source:    "enterprise_script_archive",
			Preserved: map[string]any{},
		}},
		Actions: []dsl.Action{{
			Name: "orders", Type: "GET", Method: http.MethodGet, Target: "https://origin.example.test/orders",
			Headers: map[string]string{"Authorization": "Bearer synthetic-session-kept-token-value"},
		}},
	}
	ensureCredentialPreservingImportMetadata(draft, "enterprise_script_archive")
	now := time.Now().UTC()
	created, err := store.CreateAuthoringSession(&storage.AuthoringSession{
		ID: "session-retained-transport", ProjectID: projectID, Source: "enterprise_script_archive",
		Name: "Retained credential draft", Status: "editing", Draft: draft,
		CreatedAt: now, UpdatedAt: now, LastAccessedAt: now,
	})
	if err != nil || !created {
		t.Fatalf("seed authoring session: created=%v err=%v", created, err)
	}

	update := func(edit func(*dsl.Script)) *httptest.ResponseRecorder {
		t.Helper()
		get := authoringSecretRequest(t, server, http.MethodGet, "/api/v1/scripts/authoring/sessions/session-retained-transport", nil)
		if get.Code != http.StatusOK {
			t.Fatalf("get session status = %d; body=%s", get.Code, get.Body.String())
		}
		var view storage.AuthoringSession
		if err := json.Unmarshal(get.Body.Bytes(), &view); err != nil || view.Draft == nil {
			t.Fatalf("decode session: %v %s", err, get.Body.String())
		}
		edit(view.Draft)
		return authoringSecretRequest(t, server, http.MethodPut, "/api/v1/scripts/authoring/sessions/session-retained-transport", map[string]any{
			"project_id": projectID, "source": "enterprise_script_archive", "name": "Retained credential draft",
			"status": "editing", "draft": view.Draft,
		})
	}
	customProxy := func(script *dsl.Script) {
		script.Config.HTTP.ProxyMode = "custom"
		script.Config.HTTP.Proxy.URL = "http://proxy.example.test:8888"
	}

	if got := update(func(script *dsl.Script) { script.Config.HTTP.RequestTimeoutMs = 4321 }); got.Code != http.StatusOK {
		t.Fatalf("timeout edit status = %d; body=%s", got.Code, got.Body.String())
	}
	if got := update(customProxy); got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "retained imported credentials") {
		t.Fatalf("custom proxy with the setting off: status = %d; body=%s", got.Code, got.Body.String())
	}

	settings := server.runner.GetSystemSettings()
	settings.ScriptImports.AllowRetainedCredentialTransportEdits = true
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("enable transport edits: %v", err)
	}
	if got := update(customProxy); got.Code != http.StatusOK {
		t.Fatalf("custom proxy with the setting on: status = %d; body=%s", got.Code, got.Body.String())
	}
	persisted, err := store.GetAuthoringSession("session-retained-transport")
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Draft.Config.HTTP.ProxyMode != "custom" ||
		persisted.Draft.Actions[0].Headers["Authorization"] != "Bearer synthetic-session-kept-token-value" {
		t.Fatalf("persisted draft = mode %q, Authorization %q; want the proxy edit with the kept credential restored",
			persisted.Draft.Config.HTTP.ProxyMode, persisted.Draft.Actions[0].Headers["Authorization"])
	}
}

// The settings PUT names the policy that moved in its audit trail, requires the
// durable pre-commit audit for either policy, and leaves the live setting
// untouched when that audit cannot be written.
func TestRetainedCredentialTransportPolicyAuditAtHandlerLevel(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})

	put := func(mutate func(*SystemSettings)) *httptest.ResponseRecorder {
		t.Helper()
		settings := server.runner.GetSystemSettings()
		mutate(&settings)
		body, err := json.Marshal(settings)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPut, "/api/v1/system/settings", bytes.NewReader(body))
		withCurrentSystemSettingsIfMatch(request, server)
		response := httptest.NewRecorder()
		server.handleUpdateSystemSettings(response, request)
		return response
	}

	server.store = storeOverlay(&selectiveAuditFailureStore{Store: store, action: "system.settings.update", status: "pending"})
	if got := put(func(settings *SystemSettings) { settings.ScriptImports.AllowRetainedCredentialTransportEdits = true }); got.Code != http.StatusServiceUnavailable {
		t.Fatalf("transport policy with the pending audit failing: status = %d; body=%s", got.Code, got.Body.String())
	}
	if server.runner.preservedImportEditOptions().AllowTransportRouteEdits ||
		server.runner.GetSystemSettings().ScriptImports.AllowRetainedCredentialTransportEdits {
		t.Fatal("transport policy changed without a durable intent audit")
	}
	server.store = store

	if got := put(func(settings *SystemSettings) { settings.ScriptImports.AllowRetainedCredentialTransportEdits = true }); got.Code != http.StatusOK {
		t.Fatalf("transport policy status = %d; body=%s", got.Code, got.Body.String())
	}
	if !server.runner.preservedImportEditOptions().AllowTransportRouteEdits {
		t.Fatal("the save path did not pick up the enabled policy")
	}
	if got := put(func(settings *SystemSettings) { settings.ScriptImports.AllowSourceSecretMigration = true }); got.Code != http.StatusOK {
		t.Fatalf("migration policy status = %d; body=%s", got.Code, got.Body.String())
	}

	events, err := store.ListAuditEvents(storage.AuditEventFilter{Action: "system.settings.update", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	var transportOnly, migrationOnly bool
	for _, event := range events {
		if event.Status != "success" {
			continue
		}
		transport, _ := event.Details["retained_credential_transport_policy_changed"].(bool)
		migration, _ := event.Details["source_secret_migration_policy_changed"].(bool)
		switch {
		case transport && !migration:
			transportOnly = true
			if event.Details["allow_retained_credential_transport_edits"] != true {
				t.Fatalf("transport audit details = %#v", event.Details)
			}
		case migration && !transport:
			migrationOnly = true
		default:
			t.Fatalf("success audit flags are not exclusive: %#v", event.Details)
		}
	}
	if !transportOnly || !migrationOnly {
		t.Fatalf("audit events = %#v, want one transport-only and one migration-only success event", events)
	}
}

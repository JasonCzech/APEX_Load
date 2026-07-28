package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"github.com/jasonczech/apexload/pkg/storage"
)

type failOnceDirectImportReceiptStore struct {
	*storage.SQLiteStore
	claims       int
	failComplete bool
}

func (s *failOnceDirectImportReceiptStore) ClaimDirectImportReceipt(
	projectID, sourceProfile, operationKeyHash, requestSHA256, ownerToken string,
	now time.Time,
	lease time.Duration,
) (*storage.DirectImportReceiptClaim, error) {
	s.claims++
	if s.claims > 1 {
		now = now.Add(3 * directImportReceiptLease())
	}
	return s.SQLiteStore.ClaimDirectImportReceipt(
		projectID, sourceProfile, operationKeyHash, requestSHA256, ownerToken,
		now, lease,
	)
}

func (s *failOnceDirectImportReceiptStore) CompleteDirectImportReceipt(
	id, ownerToken string,
	scriptIDs []string,
	scenarioID string,
	now time.Time,
) (bool, error) {
	if s.failComplete {
		s.failComplete = false
		return false, errors.New("injected lost receipt completion")
	}
	return s.SQLiteStore.CompleteDirectImportReceipt(
		id, ownerToken, scriptIDs, scenarioID, now,
	)
}

func TestJMeterSaveLostResponseRetryReturnsSameDurableAssets(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()

	const (
		operationID = "jmeter-preview-operation-6dcd59de"
		canary      = "jmeter-idempotency-password-canary"
	)
	payload := map[string]any{
		"scripts": []map[string]any{{
			"name":           "Idempotent_JMeter",
			"version":        "1.0.0",
			"protocol":       "http",
			"generated_by":   "jmeter-import",
			"generated_from": "jmeter",
			"metadata": map[string]any{"import": map[string]any{
				"source": "jmeter",
				"preserved": map[string]any{
					jmeterDirectImportOperationKey: operationID,
				},
			}},
			"config": map[string]any{"base_url": "https://api.example.test"},
			"actions": []map[string]any{{
				"name": "Request", "type": "request", "method": "GET", "target": "/one",
				"headers": map[string]string{"Authorization": "Bearer " + canary},
			}},
		}},
		"scenario_draft": map[string]any{
			"name": "Idempotent JMeter Scenario", "duration": "2m",
			"scenario_mode": "vuser_driven",
			"groups": []map[string]any{{
				"name": "Users", "script_name": "Idempotent_JMeter",
				"vusers": 3, "initialization": "at_runtime",
			}},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	first := postJSON(t, server, "/api/v1/scripts/import/jmeter/save", body, "")
	if first.Code != http.StatusCreated {
		t.Fatalf("first save status = %d body=%s", first.Code, first.Body.String())
	}
	second := postJSON(t, server, "/api/v1/scripts/import/jmeter/save", body, "")
	if second.Code != http.StatusCreated {
		t.Fatalf("lost-response retry status = %d body=%s", second.Code, second.Body.String())
	}
	if strings.Contains(first.Body.String(), canary) || strings.Contains(second.Body.String(), canary) {
		t.Fatal("JMeter idempotency response disclosed imported credentials")
	}

	var firstResult, secondResult struct {
		Scripts  []StoredScript  `json:"scripts"`
		Scenario *StoredScenario `json:"scenario"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &firstResult); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &secondResult); err != nil {
		t.Fatal(err)
	}
	if len(firstResult.Scripts) != 1 || len(secondResult.Scripts) != 1 ||
		firstResult.Scripts[0].ID != secondResult.Scripts[0].ID ||
		firstResult.Scenario == nil || secondResult.Scenario == nil ||
		firstResult.Scenario.ID != secondResult.Scenario.ID {
		t.Fatalf("retry identities changed: first=%+v second=%+v", firstResult, secondResult)
	}
	if got := len(server.runner.ListScripts()); got != 1 {
		t.Fatalf("durable script count = %d, want 1", got)
	}
	if got := len(server.runner.ListScenarios()); got != 1 {
		t.Fatalf("durable scenario count = %d, want 1", got)
	}
	stored, ok := server.runner.GetScript(firstResult.Scripts[0].ID)
	if !ok || stored.Script.Actions[0].Headers["Authorization"] != "Bearer "+canary {
		t.Fatalf("destination script did not retain imported credential: %#v", stored)
	}

	changedCredential := bytes.Replace(
		body,
		[]byte(canary),
		[]byte("different-jmeter-idempotency-password"),
		1,
	)
	credentialConflict := postJSON(
		t,
		server,
		"/api/v1/scripts/import/jmeter/save",
		changedCredential,
		"",
	)
	if credentialConflict.Code != http.StatusConflict {
		t.Fatalf(
			"changed credential with same operation id status = %d, want 409 body=%s",
			credentialConflict.Code,
			credentialConflict.Body.String(),
		)
	}
	if strings.Contains(
		credentialConflict.Body.String(),
		"different-jmeter-idempotency-password",
	) {
		t.Fatal("JMeter credential-conflict response disclosed the changed credential")
	}
	stored, ok = server.runner.GetScript(firstResult.Scripts[0].ID)
	if !ok || stored.Script.Actions[0].Headers["Authorization"] != "Bearer "+canary {
		t.Fatalf("credential conflict changed the first destination script: %#v", stored)
	}

	changed := bytes.Replace(body, []byte(`"/one"`), []byte(`"/two"`), 1)
	conflict := postJSON(t, server, "/api/v1/scripts/import/jmeter/save", changed, "")
	if conflict.Code != http.StatusConflict {
		t.Fatalf("changed request with same operation id status = %d, want 409 body=%s",
			conflict.Code, conflict.Body.String())
	}
}

func TestJMeterLegacyNoKeyStillCreatesIntentionalDuplicates(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	body := []byte(`{"scripts":[{
		"name":"Legacy_Duplicate","version":"1","protocol":"http",
		"config":{"base_url":"https://example.test"},
		"actions":[{"name":"Request","type":"request","method":"GET","target":"/"}]
	}]}`)
	first := postJSON(t, server, "/api/v1/scripts/import/jmeter/save", body, "")
	second := postJSON(t, server, "/api/v1/scripts/import/jmeter/save", body, "")
	if first.Code != http.StatusCreated || second.Code != http.StatusCreated {
		t.Fatalf("legacy saves = %d/%d, want 201/201; bodies=%s / %s",
			first.Code, second.Code, first.Body.String(), second.Body.String())
	}
	if got := len(server.runner.ListScripts()); got != 2 {
		t.Fatalf("legacy no-key duplicate count = %d, want 2", got)
	}
}

func TestJMeterExplicitIdempotencyKeyReplaysThroughAuthenticatedMiddleware(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	enableTrustedHeaderAuth(t, server)

	body := []byte(`{"scripts":[{
		"name":"Explicit_Key","version":"1","protocol":"http",
		"config":{"base_url":"https://example.test"},
		"actions":[{"name":"Request","type":"request","method":"GET","target":"/"}]
	}]}`)
	save := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(
			http.MethodPost,
			"/api/v1/scripts/import/jmeter/save",
			bytes.NewReader(body),
		)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "jmeter-explicit-retry-key")
		req.Header.Set("X-Auth-Request-User", "direct-import-editor")
		req.Header.Set("X-APEXLoad-Roles", RoleSystemAdmin)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		return resp
	}
	first, second := save(), save()
	if first.Code != http.StatusCreated || second.Code != http.StatusCreated {
		t.Fatalf(
			"explicit-key saves = %d/%d, want 201/201; bodies=%s / %s",
			first.Code,
			second.Code,
			first.Body.String(),
			second.Body.String(),
		)
	}
	var firstResult, secondResult struct {
		Scripts []StoredScript `json:"scripts"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &firstResult); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &secondResult); err != nil {
		t.Fatal(err)
	}
	if len(firstResult.Scripts) != 1 || len(secondResult.Scripts) != 1 ||
		firstResult.Scripts[0].ID != secondResult.Scripts[0].ID {
		t.Fatalf(
			"authenticated idempotent retry returned different assets: %#v / %#v",
			firstResult,
			secondResult,
		)
	}
	if got := len(server.runner.ListScripts()); got != 1 {
		t.Fatalf("authenticated idempotent retry stored %d scripts, want 1", got)
	}
}

func TestJMeterRetryHealsAssetsCommittedBeforeReceiptCompletion(t *testing.T) {
	base, err := storage.NewSQLiteStore(t.TempDir() + "/receipts.db")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer base.Close()
	store := &failOnceDirectImportReceiptStore{
		SQLiteStore: base, failComplete: true,
	}
	server := NewServer(ServerConfig{Store: store})
	body := []byte(`{"scripts":[{
		"name":"Crash_Recovery","version":"1","protocol":"http",
		"generated_by":"jmeter-import","generated_from":"jmeter",
		"metadata":{"import":{"source":"jmeter","preserved":{
			"direct_import_operation_id_v1":"crash-recovery-operation-729c"
		}}},
		"config":{"base_url":"https://example.test"},
		"actions":[{"name":"Request","type":"request","method":"GET","target":"/"}]
	}]}`)
	first := postJSON(t, server, "/api/v1/scripts/import/jmeter/save", body, "")
	if first.Code != http.StatusInternalServerError {
		t.Fatalf("injected completion failure status = %d, want 500 body=%s",
			first.Code, first.Body.String())
	}
	if got := len(server.runner.ListScripts()); got != 1 {
		t.Fatalf("asset commit before receipt failure stored %d scripts, want 1", got)
	}
	second := postJSON(t, server, "/api/v1/scripts/import/jmeter/save", body, "")
	if second.Code != http.StatusCreated {
		t.Fatalf("stale receipt healing status = %d, want 201 body=%s",
			second.Code, second.Body.String())
	}
	if got := len(server.runner.ListScripts()); got != 1 {
		t.Fatalf("stale receipt healing stored %d scripts, want 1", got)
	}
}

func TestBrowserRecordingLostResponseRetryReturnsSameDurableScript(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	stubBrowserRecordingDependencyProbe(t, nil)
	if runtime.GOOS == "linux" {
		t.Setenv("DISPLAY", ":0")
	}
	if err := store.SaveProject(&storage.Project{
		ID: "proj-1", Name: "Browser recorder project",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("save browser project: %v", err)
	}
	fake := newFakeBrowserRecorder(recordedBrowserArtifact())
	fake.install(server.browserRecording)
	id := startBrowserRecordingViaAPI(t, server)
	stopBrowserRecordingViaAPI(t, server, id)

	body := []byte(`{"name":"Idempotent Browser Save","expected_script_count":1}`)
	path := "/api/v1/browser-recorder/sessions/" + id + "/convert"
	first := postJSON(t, server, path, body, "")
	if first.Code != http.StatusCreated {
		t.Fatalf("first convert status = %d body=%s", first.Code, first.Body.String())
	}
	second := postJSON(t, server, path, body, "")
	if second.Code != http.StatusCreated {
		t.Fatalf("lost-response retry status = %d body=%s", second.Code, second.Body.String())
	}
	var firstResult, secondResult struct {
		Script StoredScript `json:"script"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &firstResult); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &secondResult); err != nil {
		t.Fatal(err)
	}
	if firstResult.Script.ID == "" || firstResult.Script.ID != secondResult.Script.ID {
		t.Fatalf("browser retry IDs = %q/%q", firstResult.Script.ID, secondResult.Script.ID)
	}
	if got := len(server.runner.ListScripts()); got != 1 {
		t.Fatalf("browser retry durable script count = %d, want 1", got)
	}
}

func TestBrowserRecordingRetryRejectsChangedCapturedCredential(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	stubBrowserRecordingDependencyProbe(t, nil)
	if runtime.GOOS == "linux" {
		t.Setenv("DISPLAY", ":0")
	}
	if err := store.SaveProject(&storage.Project{
		ID: "proj-1", Name: "Browser recorder project",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("save browser project: %v", err)
	}

	const (
		firstCredential  = "browser-import-credential-one"
		secondCredential = "browser-import-credential-two"
	)
	artifact := recordedBrowserArtifact()
	artifact.Steps[0].Masked = false
	artifact.Steps[0].MarkSecret = true
	artifact.Steps[0].Value = firstCredential
	fake := newFakeBrowserRecorder(artifact)
	fake.install(server.browserRecording)
	id := startBrowserRecordingViaAPI(t, server)
	stopBrowserRecordingViaAPI(t, server, id)

	body := []byte(`{"name":"Credential-fenced Browser Save","expected_script_count":1}`)
	path := "/api/v1/browser-recorder/sessions/" + id + "/convert"
	first := postJSON(t, server, path, body, "")
	if first.Code != http.StatusCreated {
		t.Fatalf("first convert status = %d body=%s", first.Code, first.Body.String())
	}
	var firstResult struct {
		Script StoredScript `json:"script"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &firstResult); err != nil {
		t.Fatal(err)
	}
	stored, ok := server.runner.GetScript(firstResult.Script.ID)
	if !ok || !scriptContainsExactActionValue(stored.Script, firstCredential) {
		t.Fatalf("first browser destination omitted the captured credential: %#v", stored)
	}

	server.browserRecording.mu.RLock()
	session := server.browserRecording.sessions[id]
	server.browserRecording.mu.RUnlock()
	if session == nil {
		t.Fatal("stopped browser recording session disappeared")
	}
	session.mu.Lock()
	if session.final == nil || len(session.final.Steps) == 0 {
		session.mu.Unlock()
		t.Fatal("stopped browser recording has no editable final artifact")
	}
	session.final.Steps[0].Value = secondCredential
	session.mu.Unlock()

	conflict := postJSON(t, server, path, body, "")
	if conflict.Code != http.StatusConflict {
		t.Fatalf(
			"changed browser credential with same operation status = %d, want 409 body=%s",
			conflict.Code,
			conflict.Body.String(),
		)
	}
	if strings.Contains(conflict.Body.String(), secondCredential) {
		t.Fatal("browser credential-conflict response disclosed the changed credential")
	}
	stored, ok = server.runner.GetScript(firstResult.Script.ID)
	if !ok || !scriptContainsExactActionValue(stored.Script, firstCredential) ||
		scriptContainsExactActionValue(stored.Script, secondCredential) {
		t.Fatalf("credential conflict changed the first browser destination: %#v", stored)
	}
}

func TestDirectImportRequestDigestIsExactKeyedAndSharedAcrossReplicas(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir() + "/direct-digest.db")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()
	first := NewServer(ServerConfig{Store: store})
	second := NewServer(ServerConfig{Store: store})

	script := &dsl.Script{
		Name: "Credential request", Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{
			Name: "request", Type: "request", Method: "GET", Target: "/",
			Auth: &dsl.AuthConfig{
				Type: "basic", Username: "operator", Password: "first-password",
			},
		}},
	}
	firstDigest, err := first.directImportScriptsRequestDigest(
		[]*dsl.Script{script},
		map[string]any{"mode": "save"},
	)
	if err != nil {
		t.Fatalf("first digest: %v", err)
	}
	replicaDigest, err := second.directImportScriptsRequestDigest(
		[]*dsl.Script{script},
		map[string]any{"mode": "save"},
	)
	if err != nil {
		t.Fatalf("replica digest: %v", err)
	}
	if firstDigest != replicaDigest {
		t.Fatalf("HA replicas produced different request digests: %q / %q", firstDigest, replicaDigest)
	}
	if !strings.HasPrefix(firstDigest, "hmac-sha256:") ||
		strings.Contains(firstDigest, "first-password") {
		t.Fatalf("request digest is not disclosure-safe HMAC: %q", firstDigest)
	}

	changed := cloneScriptDefinition(script)
	changed.Actions[0].Auth.Password = "second-password"
	changedDigest, err := first.directImportScriptsRequestDigest(
		[]*dsl.Script{changed},
		map[string]any{"mode": "save"},
	)
	if err != nil {
		t.Fatalf("changed digest: %v", err)
	}
	if changedDigest == firstDigest {
		t.Fatal("request digest ignored an exact credential change")
	}
}

func TestDirectImportPreparedAssetsRejectsCredentialOnlyMismatch(t *testing.T) {
	prepared := &dsl.Script{
		ID: "deterministic-script", Name: "Prepared", Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{
			Name: "request", Type: "request", Method: "GET", Target: "/",
			Auth: &dsl.AuthConfig{
				Type: "basic", Username: "operator", Password: "prepared-password",
			},
		}},
	}
	storedScript := cloneScriptDefinition(prepared)
	storedScript.Actions[0].Auth.Password = "peer-edited-password"
	stored := &StoredScript{ID: prepared.ID, Script: storedScript}
	if directImportPreparedAssetsMatch(
		nil,
		[]*StoredScript{stored},
		[]*dsl.Script{prepared},
		nil,
		nil,
	) {
		t.Fatal("stale direct-import recovery adopted a credential-only asset mismatch")
	}
}

func scriptContainsExactActionValue(script *dsl.Script, value string) bool {
	if script == nil {
		return false
	}
	found := false
	_ = dslwalk.WalkActions(script, func(ref dslwalk.ActionRef) error {
		if ref.Action.Value == value {
			found = true
		}
		return nil
	})
	return found
}

func postJSON(t *testing.T, server *Server, path string, body []byte, idempotencyKey string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	return resp
}

package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

type failSecondAsyncAPIBatchScriptStore struct {
	*libraryAtomicForwardingStore
	mu    sync.Mutex
	count int
}

func (s *failSecondAsyncAPIBatchScriptStore) CreateScriptIfAbsent(script *dsl.Script) (bool, error) {
	s.mu.Lock()
	s.count++
	count := s.count
	s.mu.Unlock()
	if count == 2 {
		return false, errors.New("injected second AsyncAPI script failure")
	}
	return s.libraryAtomicForwardingStore.CreateScriptIfAbsent(script)
}

func TestCommitAsyncAPIImportBatchRollsBackEveryScriptOnLaterFailure(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "asyncapi-batch-rollback.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	store := &failSecondAsyncAPIBatchScriptStore{
		libraryAtomicForwardingStore: &libraryAtomicForwardingStore{Store: base},
	}
	server := NewServer(ServerConfig{Store: store})
	response := postAsyncAPIBatch(t, server, map[string]any{
		"operation_id": "asyncapi-batch-rollback",
		"scripts": []any{
			asyncAPIBatchTestScript("First", storage.DefaultProjectID, ""),
			asyncAPIBatchTestScript("Second", storage.DefaultProjectID, ""),
		},
	})
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d body=%s", response.Code, http.StatusInternalServerError, response.Body.String())
	}
	if scripts, err := base.ListScripts(storage.DefaultProjectID, nil); err != nil {
		t.Fatal(err)
	} else if len(scripts) != 0 {
		t.Fatalf("failed atomic batch left scripts behind: %#v", scripts)
	}
}

func TestCommitAsyncAPIImportBatchRequiresOperationID(t *testing.T) {
	server := NewServer(ServerConfig{})
	response := postAsyncAPIBatch(t, server, map[string]any{
		"scripts": []any{
			asyncAPIBatchTestScript("Missing operation", storage.DefaultProjectID, ""),
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

func TestCommitAsyncAPIImportBatchRestoresOpaqueDraftsWithEmbeddedFolderAndReplaysCompletedReceipt(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "asyncapi-batch-replay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})
	folder := &storage.Folder{
		ID: "asyncapi-selected-folder", Name: "AsyncAPI", ProjectID: storage.DefaultProjectID,
		Category: "scripts", CreatedAt: time.Now().UTC(),
	}
	if err := store.SaveFolder(folder); err != nil {
		t.Fatal(err)
	}
	first := asyncAPIBatchScriptWithCredential("First", "asyncapi-first-secret")
	second := asyncAPIBatchScriptWithCredential("Second", "asyncapi-second-secret")
	preview := server.newScriptImportResponseWithSourceSecrets(
		httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/asyncapi", nil),
		scriptImportResponseOptions{Script: first, Scripts: []*dsl.Script{first, second}},
	)
	if firstToken, secondToken := importDraftAttestationToken(first), importDraftAttestationToken(second); firstToken == "" || secondToken == "" {
		t.Fatalf("AsyncAPI preview capabilities = first %q second %q", firstToken, secondToken)
	}
	masked, ok := preview["scripts"].([]any)
	if !ok || len(masked) != 2 {
		t.Fatalf("preview scripts = %#v, want two opaque drafts", preview["scripts"])
	}
	firstDraft, ok := masked[0].(map[string]any)
	if !ok {
		t.Fatalf("first preview draft = %T, want object", masked[0])
	}
	firstDraft["name"] = "Renamed First"
	for index, value := range masked {
		draft, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("preview draft %d = %T, want object", index, value)
		}
		// The server-owned attested candidate has no folder. This simulates the
		// dashboard selecting a destination on the disclosed draft itself.
		draft["folder_id"] = folder.ID
	}
	previewJSON, err := json.Marshal(masked)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(previewJSON, []byte("asyncapi-first-secret")) ||
		!bytes.Contains(previewJSON, []byte("asyncapi-second-secret")) {
		t.Fatalf("preview did not preserve source credentials: %s", previewJSON)
	}
	payload := map[string]any{
		"operation_id": "asyncapi-opaque-replay",
		"scripts":      masked,
	}
	created := postAsyncAPIBatch(t, server, payload)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", created.Code, created.Body.String())
	}
	if !strings.Contains(created.Body.String(), "asyncapi-first-secret") ||
		!strings.Contains(created.Body.String(), "asyncapi-second-secret") {
		t.Fatalf("batch response did not preserve credentials: %s", created.Body.String())
	}
	var firstResponse struct {
		Scripts []StoredScript `json:"scripts"`
		Count   int            `json:"count"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &firstResponse); err != nil {
		t.Fatal(err)
	}
	if firstResponse.Count != 2 || len(firstResponse.Scripts) != 2 {
		t.Fatalf("created response = %#v", firstResponse)
	}
	for index, want := range []string{"Bearer asyncapi-first-secret", "Bearer asyncapi-second-secret"} {
		stored, ok := server.runner.GetScript(firstResponse.Scripts[index].ID)
		if !ok || stored == nil || stored.Script == nil {
			t.Fatalf("stored script %d not found", index)
		}
		if got := stored.Script.Actions[0].HeaderList[0].Value; got != want {
			t.Fatalf("stored script %d credential = %q, want %q", index, got, want)
		}
		if stored.FolderID == nil || *stored.FolderID != folder.ID {
			t.Fatalf("stored script %d folder = %#v, want %q", index, stored.FolderID, folder.ID)
		}
		if index == 0 && stored.Script.Name != "Renamed First" {
			t.Fatalf("stored script name = %q, want requested rename", stored.Script.Name)
		}
	}

	// A completed receipt replays before the consumed one-shot draft tokens are
	// inspected, which makes retry after a lost 201 safe and idempotent.
	replayed := postAsyncAPIBatch(t, server, payload)
	if replayed.Code != http.StatusCreated {
		t.Fatalf("replay status = %d body=%s", replayed.Code, replayed.Body.String())
	}
	var replayResponse struct {
		Scripts []StoredScript `json:"scripts"`
	}
	if err := json.Unmarshal(replayed.Body.Bytes(), &replayResponse); err != nil {
		t.Fatal(err)
	}
	for index := range firstResponse.Scripts {
		if replayResponse.Scripts[index].ID != firstResponse.Scripts[index].ID {
			t.Fatalf("replay script %d id = %q, want %q", index, replayResponse.Scripts[index].ID, firstResponse.Scripts[index].ID)
		}
	}
}

func TestCommitAsyncAPIImportBatchRestartRecoversCommittedCredentialedBatch(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "asyncapi-restart-recovery.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	store := &failOnceDirectImportReceiptStore{SQLiteStore: base, failComplete: true}
	firstServer := NewServer(ServerConfig{Store: store})
	script := asyncAPIBatchScriptWithCredential("Restart recovery", "asyncapi-restart-secret")
	preview := firstServer.newScriptImportResponseWithSourceSecrets(
		httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/asyncapi", nil),
		scriptImportResponseOptions{Script: script, Scripts: []*dsl.Script{script}},
	)
	masked, ok := preview["scripts"].([]any)
	if !ok || len(masked) != 1 {
		t.Fatalf("preview scripts = %#v", preview["scripts"])
	}
	payload := map[string]any{
		"operation_id": "asyncapi-credential-restart-recovery",
		"scripts":      masked,
	}
	first := postAsyncAPIBatch(t, firstServer, payload)
	if first.Code != http.StatusInternalServerError {
		t.Fatalf("injected completion status = %d body=%s", first.Code, first.Body.String())
	}
	reuse := postAsyncAPIBatch(t, firstServer, map[string]any{
		"operation_id": "asyncapi-credential-restart-different-operation",
		"scripts":      masked,
	})
	if reuse.Code != http.StatusConflict {
		t.Fatalf("post-commit capability reuse status = %d body=%s", reuse.Code, reuse.Body.String())
	}

	// The second process shares only durable storage; the masked token's local
	// credential candidate is deliberately gone.
	restarted := NewServer(ServerConfig{Store: store})
	recovered := postAsyncAPIBatch(t, restarted, payload)
	if recovered.Code != http.StatusCreated {
		t.Fatalf("restart recovery status = %d body=%s", recovered.Code, recovered.Body.String())
	}
	scripts, err := base.ListScripts(storage.DefaultProjectID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(scripts) != 1 || scripts[0].Actions[0].HeaderList[0].Value != "Bearer asyncapi-restart-secret" {
		t.Fatalf("recovered scripts = %#v, want one exact credentialed script", scripts)
	}
}

func TestCommitAsyncAPIImportBatchRejectsMixedAndUnauthorizedProjects(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "asyncapi-batch-projects.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})
	tests := []struct {
		name              string
		scripts           []any
		authorizedProject string
	}{
		{
			name: "mixed projects",
			scripts: []any{
				asyncAPIBatchTestScript("First", "project-a", ""),
				asyncAPIBatchTestScript("Second", "project-b", ""),
			},
		},
		{
			name:              "authorized scope mismatch",
			authorizedProject: "project-a",
			scripts: []any{
				asyncAPIBatchTestScript("First", "project-b", ""),
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{
				"operation_id": "asyncapi-project-rejection-" + strings.ReplaceAll(test.name, " ", "-"),
				"scripts":      test.scripts,
			})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/asyncapi/commit-batch", bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			if test.authorizedProject != "" {
				bindAuthorizedProjectToRequest(request, test.authorizedProject)
			}
			response := httptest.NewRecorder()
			server.handleCommitAsyncAPIImportBatch(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 body=%s", response.Code, response.Body.String())
			}
		})
	}
	for _, projectID := range []string{"project-a", "project-b"} {
		if scripts, err := store.ListScripts(projectID, nil); err != nil {
			t.Fatal(err)
		} else if len(scripts) != 0 {
			t.Fatalf("rejected project batch persisted scripts in %q: %#v", projectID, scripts)
		}
	}
}

func postAsyncAPIBatch(t *testing.T, server *Server, payload map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/asyncapi/commit-batch", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	return response
}

func asyncAPIBatchTestScript(name, projectID, credential string) map[string]any {
	headerList := []any{}
	if credential != "" {
		headerList = append(headerList, map[string]any{"name": "Authorization", "value": "Bearer " + credential})
	}
	return map[string]any{
		"name": name, "version": "1.0.0", "protocol": "kafka", "project_id": projectID,
		"config":   map[string]any{"kafka": map[string]any{"mock": true, "topic": "orders"}},
		"metadata": map[string]any{"import": map[string]any{"source": "asyncapi", "preserved": map[string]any{}}},
		"actions": []any{map[string]any{
			"name": name + " publish", "type": "kafka_produce", "target": "orders", "header_list": headerList,
		}},
	}
}

func asyncAPIBatchScriptWithCredential(name, credential string) *dsl.Script {
	return &dsl.Script{
		Name: name, Version: "1.0.0", Protocol: "kafka", ProjectID: storage.DefaultProjectID,
		Config:   dsl.ScriptConfig{Kafka: dsl.KafkaConfig{Mock: true, Topic: "orders"}},
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Source: "asyncapi", Preserved: map[string]any{}}},
		Actions: []dsl.Action{{
			Name: name + " publish", Type: "kafka_produce", Target: "orders",
			HeaderList: dsl.HeaderList{{Name: "Authorization", Value: "Bearer " + credential}},
		}},
	}
}

package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

type githubBulkImportFile struct {
	Path string
	SHA  string
	Body []byte
}

type githubBulkImportStub struct {
	server *httptest.Server
	mu     sync.Mutex
	files  []githubBulkImportFile
	gets   int
	refs   []string
}

func newGitHubBulkImportStub(t *testing.T, files []githubBulkImportFile) *githubBulkImportStub {
	t.Helper()
	stub := &githubBulkImportStub{files: files}
	mux := http.NewServeMux()
	mux.HandleFunc("/user", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"login": "octocat", "id": 1})
	})
	mux.HandleFunc("/repos/octocat/scripts/git/ref/heads/main", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"object": map[string]any{"sha": "commit-main"}})
	})
	mux.HandleFunc("/repos/octocat/scripts/contents/", func(w http.ResponseWriter, r *http.Request) {
		stub.mu.Lock()
		stub.gets++
		stub.refs = append(stub.refs, r.URL.Query().Get("ref"))
		stub.mu.Unlock()
		rel := strings.TrimPrefix(r.URL.Path, "/repos/octocat/scripts/contents/")
		if rel == "imports" {
			entries := make([]map[string]any, 0, len(stub.files))
			for _, file := range stub.files {
				entries = append(entries, map[string]any{
					"type": "file", "name": file.Path[strings.LastIndex(file.Path, "/")+1:],
					"path": file.Path, "sha": file.SHA, "size": len(file.Body),
				})
			}
			_ = json.NewEncoder(w).Encode(entries)
			return
		}
		for _, file := range stub.files {
			if file.Path != rel {
				continue
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"type": "file", "name": file.Path[strings.LastIndex(file.Path, "/")+1:],
				"path": file.Path, "sha": file.SHA, "size": len(file.Body),
				"encoding": "base64", "content": base64.StdEncoding.EncodeToString(file.Body),
			})
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	})
	stub.server = httptest.NewServer(mux)
	t.Cleanup(stub.server.Close)
	return stub
}

func githubBulkImportScriptJSON(t *testing.T, id, name string) []byte {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"script": &dsl.Script{
		ID: id, Name: name, Version: "1.0.0", Protocol: "http",
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "Home", Type: "request", Method: "GET", Target: "/"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func runGitHubBulkImport(t *testing.T, server *Server, stubURL string) *storage.GitHubSyncJob {
	t.Helper()
	settings := server.currentGitHubSettings()
	client, err := newGitHubClient(settings, "gho_testtoken", time.Second)
	if err != nil {
		t.Fatalf("newGitHubClient: %v", err)
	}
	service := newGitHubSyncService(
		server.runner,
		server.store.(storage.GitHubEnterpriseStore),
		server.store.(storage.GitHubScriptLinkStore),
	)
	job, err := service.Run(context.Background(), &githubResolvedClient{Client: client, Settings: settings}, githubSyncJobInput{
		ProjectID: "team-a", Kind: "import", Owner: "octocat", Repo: "scripts", Branch: "main", Path: "imports",
	})
	if err != nil {
		t.Fatalf("bulk import against %s: %v", stubURL, err)
	}
	return job
}

// The sync-job route itself adds the repository's scripts to the library.
func TestGitHubImportSyncJobRouteCreatesTheScript(t *testing.T) {
	const path = "imports/order-flow.json"
	stub := newGitHubBulkImportStub(t, []githubBulkImportFile{{
		Path: path, SHA: "blob-order", Body: githubBulkImportScriptJSON(t, "", "Order Flow"),
	}})
	server := newGitHubTestServer(t, stub.server.URL)
	seedGitHubConnection(t, server)
	body, err := json.Marshal(githubSyncJobRequest{ProjectID: "team-a", Kind: "import", Owner: "octocat", Repo: "scripts", Branch: "main", Path: "imports"})
	if err != nil {
		t.Fatal(err)
	}
	job := postGitHubJSON(t, server, "/api/v1/integrations/github/sync-jobs", body, http.StatusCreated)
	if job["status"] != "complete" || job["created"] != float64(1) || job["errors"] != float64(0) {
		t.Fatalf("import sync job = %#v", job)
	}
	stored, ok := server.runner.GetScript(deterministicGitHubImportScriptID("team-a", "octocat", "scripts", "main", path))
	if !ok || stored.ProjectID != "team-a" || stored.Name != "Order Flow" {
		t.Fatalf("imported script = %#v, %t", stored, ok)
	}
}

func TestGitHubBulkImportIDLessRetryIsIdempotentAndSynced(t *testing.T) {
	const path = "imports/order-flow.json"
	stub := newGitHubBulkImportStub(t, []githubBulkImportFile{{
		Path: path, SHA: "blob-order", Body: githubBulkImportScriptJSON(t, "", "Order Flow"),
	}})
	server := newGitHubTestServer(t, stub.server.URL)
	seedGitHubConnection(t, server)

	first := runGitHubBulkImport(t, server, stub.server.URL)
	if first.Status != "complete" || first.Created != 1 || first.Updated != 0 || first.Processed != 1 || first.Errors != 0 || first.Conflicts != 0 {
		t.Fatalf("first id-less import = %#v", first)
	}
	scriptID := deterministicGitHubImportScriptID("team-a", "octocat", "scripts", "main", path)
	persisted, err := server.store.GetScript(scriptID)
	if err != nil {
		t.Fatalf("GetScript(%s): %v", scriptID, err)
	}
	firstUpdatedAt := persisted.UpdatedAt
	firstLink, err := server.store.(storage.GitHubScriptLinkStore).GetGitHubScriptLink(scriptID)
	if err != nil || firstLink == nil {
		t.Fatalf("first imported link = %#v, %v", firstLink, err)
	}
	if firstLink.Branch != "main" || firstLink.RemoteCommitSHA != "commit-main" {
		t.Fatalf("imported link branch/commit witness = %#v", firstLink)
	}
	_, firstHash, err := githubScriptEnvelope(persisted)
	if err != nil || firstLink.ContentHash != firstHash {
		t.Fatalf("first imported content hash = %q, want %q, err=%v", firstLink.ContentHash, firstHash, err)
	}

	second := runGitHubBulkImport(t, server, stub.server.URL)
	if second.Status != "complete" || second.Created != 0 || second.Updated != 0 || second.Processed != 1 || second.Errors != 0 || second.Conflicts != 0 {
		t.Fatalf("repeated id-less import = %#v", second)
	}
	scripts, err := server.store.ListScripts("team-a", nil)
	if err != nil || len(scripts) != 1 {
		t.Fatalf("scripts after repeated import = %d, %v", len(scripts), err)
	}
	retried, err := server.store.GetScript(scriptID)
	if err != nil {
		t.Fatal(err)
	}
	if !retried.UpdatedAt.Equal(firstUpdatedAt) {
		t.Fatalf("idempotent retry changed updated_at from %s to %s", firstUpdatedAt, retried.UpdatedAt)
	}
	retriedLink, err := server.store.(storage.GitHubScriptLinkStore).GetGitHubScriptLink(scriptID)
	if err != nil || retriedLink == nil || retriedLink.ContentHash != firstHash {
		t.Fatalf("retry link/hash = %#v, %v; want hash %q", retriedLink, err, firstHash)
	}
	stub.mu.Lock()
	refs := append([]string(nil), stub.refs...)
	stub.mu.Unlock()
	if len(refs) != 4 {
		t.Fatalf("two imports issued contents refs %#v, want directory+file for each", refs)
	}
	for _, ref := range refs {
		if ref != "commit-main" {
			t.Fatalf("bulk import read mutable or mixed ref %q; all refs=%#v", ref, refs)
		}
	}
	status := doGitHubJSON(t, server, http.MethodGet, "/api/v1/scripts/"+scriptID+"/github/status")
	if status["status"] != "synced" || status["local_drift"] != false || status["remote_drift"] != false {
		t.Fatalf("status after idempotent retry = %#v", status)
	}
}

func TestGitHubBulkImportDuplicateIDsFailPreflightBeforeMutation(t *testing.T) {
	const duplicateID = "duplicate-script-id"
	stub := newGitHubBulkImportStub(t, []githubBulkImportFile{
		{Path: "imports/one.json", SHA: "blob-one", Body: githubBulkImportScriptJSON(t, duplicateID, "One")},
		{Path: "imports/two.json", SHA: "blob-two", Body: githubBulkImportScriptJSON(t, duplicateID, "Two")},
	})
	server := newGitHubTestServer(t, stub.server.URL)
	job := runGitHubBulkImport(t, server, stub.server.URL)
	if job.Status != "partial" || job.Conflicts == 0 || job.Processed != 0 || job.Created != 0 || job.Updated != 0 {
		t.Fatalf("duplicate-id import = %#v", job)
	}
	scripts, err := server.store.ListScripts("team-a", nil)
	if err != nil || len(scripts) != 0 {
		t.Fatalf("duplicate-id preflight left scripts = %#v, %v", scripts, err)
	}
	if link, err := server.store.(storage.GitHubScriptLinkStore).GetGitHubScriptLink(duplicateID); err != nil || link != nil {
		t.Fatalf("duplicate-id preflight left link = %#v, %v", link, err)
	}
}

func TestGitHubBulkImportDuplicateNormalizedTargetLeavesCountUnchanged(t *testing.T) {
	const path = "imports/duplicate.json"
	payload := githubBulkImportScriptJSON(t, "", "Duplicate Target")
	stub := newGitHubBulkImportStub(t, []githubBulkImportFile{
		{Path: path, SHA: "blob-duplicate", Body: payload},
		{Path: path, SHA: "blob-duplicate", Body: payload},
	})
	server := newGitHubTestServer(t, stub.server.URL)
	before, err := server.store.ListScripts("team-a", nil)
	if err != nil {
		t.Fatal(err)
	}
	job := runGitHubBulkImport(t, server, stub.server.URL)
	if job.Status != "partial" || job.Conflicts == 0 || job.Processed != 0 || job.Created != 0 || job.Updated != 0 {
		t.Fatalf("duplicate-target import = %#v", job)
	}
	after, err := server.store.ListScripts("team-a", nil)
	if err != nil || len(after) != len(before) {
		t.Fatalf("duplicate-target import changed script count from %d to %d: %v", len(before), len(after), err)
	}
	scriptID := deterministicGitHubImportScriptID("team-a", "octocat", "scripts", "main", path)
	if link, err := server.store.(storage.GitHubScriptLinkStore).GetGitHubScriptLink(scriptID); err != nil || link != nil {
		t.Fatalf("duplicate-target preflight left link = %#v, %v", link, err)
	}
}

func TestGitHubBulkImportSuppliedIDConflictPreservesScriptAndLink(t *testing.T) {
	const scriptID = "local-owned-id"
	stub := newGitHubBulkImportStub(t, []githubBulkImportFile{{
		Path: "imports/remote.json", SHA: "blob-remote", Body: githubBulkImportScriptJSON(t, scriptID, "Remote overwrite"),
	}})
	server := newGitHubTestServer(t, stub.server.URL)
	stored, err := server.runner.StoreScriptDurable(&dsl.Script{
		ID: scriptID, Name: "Local source", Version: "1", Protocol: "http", ProjectID: "team-a",
		Config:  dsl.ScriptConfig{BaseURL: "https://local.example"},
		Actions: []dsl.Action{{Name: "Local", Type: "request", Method: "GET", Target: "/local"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	linkStore := server.store.(storage.GitHubScriptLinkStore)
	if err := linkStore.SaveGitHubScriptLink(&storage.GitHubScriptLink{
		ScriptID: scriptID, RefKey: stored.RefKey, Owner: "octocat", Repo: "scripts", Branch: "main",
		Path: "imports/local.json", LastCommitSHA: "blob-local", RemoteBlobSHA: "blob-local", ContentHash: "local-hash",
	}); err != nil {
		t.Fatal(err)
	}
	beforeScript, err := server.store.GetScript(scriptID)
	if err != nil {
		t.Fatal(err)
	}
	beforeLink, err := linkStore.GetGitHubScriptLink(scriptID)
	if err != nil {
		t.Fatal(err)
	}
	beforeFingerprint, err := storage.ImportSourceScriptFingerprint(beforeScript)
	if err != nil {
		t.Fatal(err)
	}

	job := runGitHubBulkImport(t, server, stub.server.URL)
	if job.Status != "partial" || job.Conflicts != 1 || job.Processed != 0 || job.Created != 0 || job.Updated != 0 {
		t.Fatalf("supplied-id conflict import = %#v", job)
	}
	afterScript, err := server.store.GetScript(scriptID)
	if err != nil {
		t.Fatal(err)
	}
	afterFingerprint, err := storage.ImportSourceScriptFingerprint(afterScript)
	if err != nil {
		t.Fatal(err)
	}
	afterLink, err := linkStore.GetGitHubScriptLink(scriptID)
	if err != nil {
		t.Fatal(err)
	}
	if beforeFingerprint != afterFingerprint || !beforeScript.UpdatedAt.Equal(afterScript.UpdatedAt) {
		t.Fatalf("supplied-id conflict changed script: before=%#v after=%#v", beforeScript, afterScript)
	}
	if !reflect.DeepEqual(beforeLink, afterLink) {
		t.Fatalf("supplied-id conflict changed link: before=%#v after=%#v", beforeLink, afterLink)
	}
}

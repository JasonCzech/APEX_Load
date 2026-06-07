package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

// githubWriteStub records the writes it receives so tests can assert on push
// payloads (commit message, base64 content, optimistic-concurrency sha).
type githubWriteStub struct {
	server   *httptest.Server
	mu       sync.Mutex
	lastPut  map[string]any
	fileSHA  string // current blob sha returned by GET contents
	branches map[string]bool
	pulls    int
}

func newGitHubWriteStub(t *testing.T) *githubWriteStub {
	t.Helper()
	stub := &githubWriteStub{fileSHA: "", branches: map[string]bool{"main": true}}
	mux := http.NewServeMux()
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"login": "octocat", "id": 1})
	})
	mux.HandleFunc("/repos/octocat/scripts/contents/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			stub.mu.Lock()
			sha := stub.fileSHA
			stub.mu.Unlock()
			if sha == "" {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"type": "file", "name": "x.json", "path": "apexload/scripts/x.json",
				"sha": sha, "encoding": "base64", "content": "e30=",
			})
		case http.MethodPut:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			stub.mu.Lock()
			stub.lastPut = body
			stub.fileSHA = "newblob"
			stub.mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"content": map[string]any{"sha": "newblob", "path": "apexload/scripts/x.json"},
				"commit":  map[string]any{"sha": "commit123"},
			})
		default:
			http.Error(w, "bad method", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/repos/octocat/scripts/git/ref/heads/", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"object": map[string]any{"sha": "basehead"}})
	})
	mux.HandleFunc("/repos/octocat/scripts/git/refs", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"ref": "refs/heads/new"})
	})
	mux.HandleFunc("/repos/octocat/scripts/pulls", func(w http.ResponseWriter, r *http.Request) {
		stub.mu.Lock()
		stub.pulls++
		stub.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"number": 42, "html_url": "https://ghe.example.com/octocat/scripts/pull/42", "state": "open"})
	})
	stub.server = httptest.NewServer(mux)
	t.Cleanup(stub.server.Close)
	return stub
}

func storeGitHubTestScript(t *testing.T, server *Server) *StoredScript {
	t.Helper()
	stored := server.runner.StoreScript(&dsl.Script{
		Name:      "Order Flow",
		Version:   "1.0.0",
		Protocol:  "http",
		ProjectID: "team-a",
		Config:    dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions:   []dsl.Action{{Name: "Home", Type: "request", Method: "GET", Target: "/"}},
	})
	return stored
}

func TestGitHubPushCommitCreatesLink(t *testing.T) {
	stub := newGitHubWriteStub(t)
	server := newGitHubTestServer(t, stub.server.URL)
	seedGitHubConnection(t, server)
	stored := storeGitHubTestScript(t, server)

	body, _ := json.Marshal(githubPushRequest{
		Owner: "octocat", Repo: "scripts", Branch: "main",
		Path: "apexload/scripts/x.json", Message: "Add order flow", Strategy: "commit",
	})
	resp := postGitHubJSON(t, server, "/api/v1/scripts/"+stored.ID+"/github/push", body, http.StatusOK)
	if resp["pushed"] != true || resp["commit_sha"] != "commit123" {
		t.Fatalf("unexpected push response: %#v", resp)
	}

	// PUT payload: message + base64 content, no sha on first create.
	stub.mu.Lock()
	put := stub.lastPut
	stub.mu.Unlock()
	if put["message"] != "Add order flow" {
		t.Fatalf("commit message = %#v", put["message"])
	}
	if _, hasSHA := put["sha"]; hasSHA {
		t.Fatalf("first create should not send a sha, got %#v", put["sha"])
	}
	if content, _ := put["content"].(string); content == "" {
		t.Fatal("expected base64 content in PUT payload")
	}

	// Link persisted.
	linkStore := server.store.(storage.GitHubScriptLinkStore)
	link, err := linkStore.GetGitHubScriptLink(stored.ID)
	if err != nil || link == nil {
		t.Fatalf("expected link, got (%v, %v)", link, err)
	}
	if link.LastCommitSHA != "newblob" || link.Owner != "octocat" || link.Branch != "main" {
		t.Fatalf("unexpected link: %#v", link)
	}
}

func TestGitHubPushPullRequestStrategy(t *testing.T) {
	stub := newGitHubWriteStub(t)
	server := newGitHubTestServer(t, stub.server.URL)
	seedGitHubConnection(t, server)
	stored := storeGitHubTestScript(t, server)

	body, _ := json.Marshal(githubPushRequest{
		Owner: "octocat", Repo: "scripts", Branch: "main",
		Path: "apexload/scripts/x.json", Strategy: "pr", PRTitle: "Sync order flow",
	})
	resp := postGitHubJSON(t, server, "/api/v1/scripts/"+stored.ID+"/github/push", body, http.StatusOK)
	if resp["pull_request_url"] != "https://ghe.example.com/octocat/scripts/pull/42" {
		t.Fatalf("expected PR url, got %#v", resp)
	}
	stub.mu.Lock()
	pulls := stub.pulls
	stub.mu.Unlock()
	if pulls != 1 {
		t.Fatalf("expected 1 PR created, got %d", pulls)
	}
}

func TestGitHubPushConflictWhenRemoteMoved(t *testing.T) {
	stub := newGitHubWriteStub(t)
	server := newGitHubTestServer(t, stub.server.URL)
	seedGitHubConnection(t, server)
	stored := storeGitHubTestScript(t, server)

	// Pre-seed a link whose recorded sha differs from the (newer) remote sha.
	linkStore := server.store.(storage.GitHubScriptLinkStore)
	if err := linkStore.SaveGitHubScriptLink(&storage.GitHubScriptLink{
		ScriptID: stored.ID, Owner: "octocat", Repo: "scripts", Branch: "main",
		Path: "apexload/scripts/x.json", LastCommitSHA: "oldsha", ContentHash: "abc",
	}); err != nil {
		t.Fatalf("seed link: %v", err)
	}
	stub.mu.Lock()
	stub.fileSHA = "remotemoved" // remote now ahead of the link's oldsha
	stub.mu.Unlock()

	body, _ := json.Marshal(githubPushRequest{
		Owner: "octocat", Repo: "scripts", Branch: "main", Path: "apexload/scripts/x.json", Strategy: "commit",
	})
	postGitHubJSON(t, server, "/api/v1/scripts/"+stored.ID+"/github/push", body, http.StatusConflict)
}

func TestGitHubStatusReportsDrift(t *testing.T) {
	stub := newGitHubWriteStub(t)
	server := newGitHubTestServer(t, stub.server.URL)
	seedGitHubConnection(t, server)
	stored := storeGitHubTestScript(t, server)

	// Push first so a link exists and local hash matches.
	body, _ := json.Marshal(githubPushRequest{
		Owner: "octocat", Repo: "scripts", Branch: "main", Path: "apexload/scripts/x.json", Strategy: "commit",
	})
	postGitHubJSON(t, server, "/api/v1/scripts/"+stored.ID+"/github/push", body, http.StatusOK)

	// Right after push: synced (remote sha == link sha, local unchanged).
	status := doGitHubJSON(t, server, http.MethodGet, "/api/v1/scripts/"+stored.ID+"/github/status")
	if status["linked"] != true || status["status"] != "synced" {
		t.Fatalf("expected synced, got %#v", status)
	}

	// Mutate the remote sha → remote_drift.
	stub.mu.Lock()
	stub.fileSHA = "remotechanged"
	stub.mu.Unlock()
	status = doGitHubJSON(t, server, http.MethodGet, "/api/v1/scripts/"+stored.ID+"/github/status")
	if status["remote_drift"] != true || status["status"] != "remote_drift" {
		t.Fatalf("expected remote_drift, got %#v", status)
	}

	// Mutate the local script → local_drift (combined with remote = diverged).
	stored.Script.Actions = append(stored.Script.Actions, dsl.Action{Name: "Logout", Type: "request", Method: "GET", Target: "/logout"})
	server.runner.UpdateScript(stored.ID, stored.Script)
	status = doGitHubJSON(t, server, http.MethodGet, "/api/v1/scripts/"+stored.ID+"/github/status")
	if status["local_drift"] != true || status["status"] != "diverged" {
		t.Fatalf("expected diverged, got %#v", status)
	}
}

func TestGitHubStatusNotLinked(t *testing.T) {
	stub := newGitHubWriteStub(t)
	server := newGitHubTestServer(t, stub.server.URL)
	stored := storeGitHubTestScript(t, server)

	status := doGitHubJSON(t, server, http.MethodGet, "/api/v1/scripts/"+stored.ID+"/github/status")
	if status["linked"] != false || status["status"] != "not_linked" {
		t.Fatalf("expected not_linked, got %#v", status)
	}
}

func TestRenderGitHubPathTemplate(t *testing.T) {
	stored := &StoredScript{ID: "id-1", RefKey: "S-7", Name: "Order Flow", ProjectID: "team-a"}
	got := renderGitHubPathTemplate("apexload/scripts/{project}/{ref_key}.json", stored)
	want := "apexload/scripts/team-a/S-7.json"
	if got != want {
		t.Fatalf("renderGitHubPathTemplate = %q, want %q", got, want)
	}
	// Falls back to id when ref_key is empty.
	stored.RefKey = ""
	if got := renderGitHubPathTemplate("{ref_key}.json", stored); !strings.Contains(got, "id-1") {
		t.Fatalf("expected id fallback, got %q", got)
	}
}

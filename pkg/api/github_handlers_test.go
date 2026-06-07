package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

const sampleGitHubScriptJSON = `{
  "script": {
    "name": "Order Flow",
    "version": "1.0.0",
    "protocol": "http",
    "config": {"base_url": "https://example.test"},
    "actions": [
      {"name": "Home", "type": "request", "method": "GET", "target": "/"}
    ]
  }
}`

// newGitHubReadStub extends the OAuth/identity stub with the read endpoints used
// by the browse + import flows: repos, branches, and contents (file + dir).
func newGitHubReadStub(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"login": "octocat", "id": 1})
	})
	mux.HandleFunc("/user/repos", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer gho_testtoken" {
			http.Error(w, "bad token", http.StatusUnauthorized)
			return
		}
		// Single page: returning fewer than per_page stops pagination.
		_ = json.NewEncoder(w).Encode([]map[string]any{{
			"name": "scripts", "full_name": "octocat/scripts", "private": true,
			"default_branch": "main", "html_url": "https://ghe.example.com/octocat/scripts",
			"owner": map[string]any{"login": "octocat"},
		}})
	})
	mux.HandleFunc("/repos/octocat/scripts/branches", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"name": "main", "commit": map[string]any{"sha": "abc123"}},
			{"name": "dev", "commit": map[string]any{"sha": "def456"}},
		})
	})
	mux.HandleFunc("/repos/octocat/scripts/contents/", func(w http.ResponseWriter, r *http.Request) {
		rel := strings.TrimPrefix(r.URL.Path, "/repos/octocat/scripts/contents/")
		switch rel {
		case "apexload/scripts":
			// Directory listing.
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"type": "file", "name": "order-flow.json", "path": "apexload/scripts/order-flow.json", "sha": "filesha1", "size": 128},
			})
		case "apexload/scripts/order-flow.json":
			// GitHub wraps base64 content with newlines.
			encoded := base64.StdEncoding.EncodeToString([]byte(sampleGitHubScriptJSON))
			wrapped := encoded[:10] + "\n" + encoded[10:]
			_ = json.NewEncoder(w).Encode(map[string]any{
				"type": "file", "name": "order-flow.json", "path": "apexload/scripts/order-flow.json",
				"sha": "filesha1", "size": 128, "encoding": "base64", "content": wrapped,
				"html_url": "https://ghe.example.com/octocat/scripts/blob/main/apexload/scripts/order-flow.json",
			})
		case "apexload/scripts/broken.json":
			encoded := base64.StdEncoding.EncodeToString([]byte("not json at all"))
			_ = json.NewEncoder(w).Encode(map[string]any{
				"type": "file", "name": "broken.json", "path": "apexload/scripts/broken.json",
				"sha": "filesha2", "encoding": "base64", "content": encoded,
			})
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	})
	return httptest.NewServer(mux)
}

// seedGitHubConnection stores an encrypted connection for the synthetic local
// user so the browse/import handlers can resolve a token.
func seedGitHubConnection(t *testing.T, server *Server) {
	t.Helper()
	enc, err := encryptGitHubToken("gho_testtoken")
	if err != nil {
		t.Fatalf("encryptGitHubToken: %v", err)
	}
	store := server.store.(storage.GitHubConnectionStore)
	if err := store.SaveGitHubConnection(&storage.GitHubConnection{
		UserID: "local", GitHubLogin: "octocat", GitHubUserID: "1",
		AccessTokenEnc: enc, TokenType: "bearer", Scopes: "repo",
	}); err != nil {
		t.Fatalf("SaveGitHubConnection: %v", err)
	}
}

func TestGitHubListReposAndBranches(t *testing.T) {
	stub := newGitHubReadStub(t)
	defer stub.Close()
	server := newGitHubTestServer(t, stub.URL)
	seedGitHubConnection(t, server)

	repos := doGitHubJSON(t, server, http.MethodGet, "/api/v1/integrations/github/repos")
	list, _ := repos["repos"].([]any)
	if len(list) != 1 {
		t.Fatalf("expected 1 repo, got %#v", repos)
	}
	first := list[0].(map[string]any)
	if first["full_name"] != "octocat/scripts" || first["owner"] != "octocat" {
		t.Fatalf("unexpected repo: %#v", first)
	}

	branches := doGitHubJSON(t, server, http.MethodGet, "/api/v1/integrations/github/branches?owner=octocat&repo=scripts")
	bl, _ := branches["branches"].([]any)
	if len(bl) != 2 {
		t.Fatalf("expected 2 branches, got %#v", branches)
	}
}

func TestGitHubContentsDirAndFile(t *testing.T) {
	stub := newGitHubReadStub(t)
	defer stub.Close()
	server := newGitHubTestServer(t, stub.URL)
	seedGitHubConnection(t, server)

	dir := doGitHubJSON(t, server, http.MethodGet, "/api/v1/integrations/github/contents?owner=octocat&repo=scripts&path=apexload/scripts")
	if dir["type"] != "dir" {
		t.Fatalf("expected dir listing, got %#v", dir)
	}
	entries, _ := dir["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %#v", dir)
	}

	file := doGitHubJSON(t, server, http.MethodGet, "/api/v1/integrations/github/contents?owner=octocat&repo=scripts&path=apexload/scripts/order-flow.json")
	if file["type"] != "file" || file["sha"] != "filesha1" {
		t.Fatalf("unexpected file response: %#v", file)
	}
}

func TestGitHubImportScriptReview(t *testing.T) {
	stub := newGitHubReadStub(t)
	defer stub.Close()
	server := newGitHubTestServer(t, stub.URL)
	seedGitHubConnection(t, server)

	body, _ := json.Marshal(githubImportRequest{
		ProjectID: "team-a", Owner: "octocat", Repo: "scripts",
		Path: "apexload/scripts/order-flow.json", Ref: "main",
	})
	resp := postGitHubJSON(t, server, "/api/v1/scripts/import/github", body, http.StatusOK)

	script, ok := resp["script"].(map[string]any)
	if !ok {
		t.Fatalf("expected script in response, got %#v", resp)
	}
	if script["name"] != "Order Flow" {
		t.Fatalf("unexpected script name: %#v", script["name"])
	}
	if script["project_id"] != "team-a" {
		t.Fatalf("expected project_id team-a, got %#v", script["project_id"])
	}

	sourceReview, ok := resp["source_review"].(map[string]any)
	if !ok || sourceReview["commit_sha"] != "filesha1" || sourceReview["owner"] != "octocat" {
		t.Fatalf("unexpected source_review: %#v", resp["source_review"])
	}
	if sourceReview["path"] != "apexload/scripts/order-flow.json" {
		t.Fatalf("source_review path = %#v", sourceReview["path"])
	}
}

func TestGitHubImportInvalidScriptIsRejected(t *testing.T) {
	stub := newGitHubReadStub(t)
	defer stub.Close()
	server := newGitHubTestServer(t, stub.URL)
	seedGitHubConnection(t, server)

	body, _ := json.Marshal(githubImportRequest{
		ProjectID: "team-a", Owner: "octocat", Repo: "scripts",
		Path: "apexload/scripts/broken.json", Ref: "main",
	})
	postGitHubJSON(t, server, "/api/v1/scripts/import/github", body, http.StatusUnprocessableEntity)
}

func TestGitHubImportDirectoryIsRejected(t *testing.T) {
	stub := newGitHubReadStub(t)
	defer stub.Close()
	server := newGitHubTestServer(t, stub.URL)
	seedGitHubConnection(t, server)

	body, _ := json.Marshal(githubImportRequest{
		ProjectID: "team-a", Owner: "octocat", Repo: "scripts",
		Path: "apexload/scripts", Ref: "main",
	})
	postGitHubJSON(t, server, "/api/v1/scripts/import/github", body, http.StatusBadRequest)
}

func TestGitHubBrowseRequiresConnection(t *testing.T) {
	stub := newGitHubReadStub(t)
	defer stub.Close()
	server := newGitHubTestServer(t, stub.URL)
	// No connection seeded.

	req := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/github/repos", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("repos without connection = %d, want 401; body=%s", resp.Code, resp.Body.String())
	}
}

func postGitHubJSON(t *testing.T, server *Server, path string, body []byte, wantStatus int) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != wantStatus {
		t.Fatalf("POST %s status = %d, want %d; body=%s", path, resp.Code, wantStatus, resp.Body.String())
	}
	var out map[string]any
	if resp.Body.Len() > 0 {
		_ = json.Unmarshal(resp.Body.Bytes(), &out)
	}
	return out
}

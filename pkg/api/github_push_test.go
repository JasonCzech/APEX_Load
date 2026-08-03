package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

// githubWriteStub records the writes it receives so tests can assert on push
// payloads (commit message, base64 content, optimistic-concurrency sha).
type githubWriteStub struct {
	server        *httptest.Server
	mu            sync.Mutex
	lastPut       map[string]any
	fileSHA       string // current blob sha returned by GET contents
	branches      map[string]bool
	putBranches   []string
	contentRefs   []string
	contentGets   int
	contentErrAt  int
	blockFirstGet bool
	getStarted    chan struct{}
	getRelease    chan struct{}
	blockFirstPut bool
	putStarted    chan struct{}
	putRelease    chan struct{}
	putCalls      int
	branchCreates int
	branchStatus  int
	pulls         int
	pullStatus    int
	contentErr    int
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
			status := stub.contentErr
			stub.contentGets++
			block := stub.blockFirstGet && stub.contentGets == 1
			started := stub.getStarted
			release := stub.getRelease
			if stub.contentErrAt > 0 && stub.contentGets == stub.contentErrAt {
				status = http.StatusInternalServerError
			}
			stub.contentRefs = append(stub.contentRefs, r.URL.Query().Get("ref"))
			stub.mu.Unlock()
			if block {
				close(started)
				<-release
			}
			if status != 0 {
				http.Error(w, "contents failed", status)
				return
			}
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
			stub.putCalls++
			block := stub.blockFirstPut && stub.putCalls == 1
			started := stub.putStarted
			release := stub.putRelease
			stub.mu.Unlock()
			if block {
				close(started)
				<-release
			}
			stub.mu.Lock()
			stub.lastPut = body
			stub.fileSHA = "newblob"
			if branch, _ := body["branch"].(string); branch != "" {
				stub.putBranches = append(stub.putBranches, branch)
			}
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
		stub.mu.Lock()
		stub.branchCreates++
		status := stub.branchStatus
		stub.mu.Unlock()
		if status == 0 {
			status = http.StatusCreated
		}
		if status < http.StatusOK || status >= http.StatusMultipleChoices {
			http.Error(w, "branch create failed", status)
			return
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"ref": "refs/heads/new"})
	})
	mux.HandleFunc("/repos/octocat/scripts/pulls", func(w http.ResponseWriter, r *http.Request) {
		stub.mu.Lock()
		stub.pulls++
		status := stub.pullStatus
		stub.mu.Unlock()
		if status == 0 {
			status = http.StatusCreated
		}
		if status < http.StatusOK || status >= http.StatusMultipleChoices {
			http.Error(w, "pull request failed", status)
			return
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"number": 42, "html_url": "https://ghe.example.com/octocat/scripts/pull/42", "state": "open"})
	})
	stub.server = httptest.NewServer(mux)
	t.Cleanup(stub.server.Close)
	return stub
}

func storeGitHubTestScript(t *testing.T, server *Server) *StoredScript {
	t.Helper()
	now := time.Now().UTC()
	if err := server.store.SaveProject(&storage.Project{ID: "team-a", Name: "Team A", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveProject(team-a): %v", err)
	}
	stored, err := server.runner.StoreScriptDurable(&dsl.Script{
		Name:      "Order Flow",
		Version:   "1.0.0",
		Protocol:  "http",
		ProjectID: "team-a",
		Config:    dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions:   []dsl.Action{{Name: "Home", Type: "request", Method: "GET", Target: "/"}},
	})
	if err != nil {
		t.Fatalf("StoreScriptDurable: %v", err)
	}
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
	link, err := server.store.(storage.GitHubScriptLinkStore).GetGitHubScriptLink(stored.ID)
	if err != nil || link == nil || !strings.HasPrefix(link.Branch, "apexload/") || link.RemoteBlobSHA != "newblob" ||
		link.BaseBranch != "main" || link.BaseBlobSHA != "" || !link.BaseMissing {
		t.Fatalf("first PR should link to its synced temporary head, got (%#v, %v)", link, err)
	}
}

func TestGitHubPushPullRequestRejectsExistingUnvettedBranch(t *testing.T) {
	stub := newGitHubWriteStub(t)
	server := newGitHubTestServer(t, stub.server.URL)
	seedGitHubConnection(t, server)
	stored := storeGitHubTestScript(t, server)
	stub.mu.Lock()
	stub.branchStatus = http.StatusUnprocessableEntity
	stub.mu.Unlock()

	body, _ := json.Marshal(githubPushRequest{
		Owner: "octocat", Repo: "scripts", Branch: "main", Path: "apexload/scripts/x.json",
		Strategy: "pr", PRBranch: "apexload/pre-existing",
	})
	postGitHubJSON(t, server, "/api/v1/scripts/"+stored.ID+"/github/push", body, http.StatusBadGateway)
	stub.mu.Lock()
	put, branches, pulls := stub.lastPut, stub.branchCreates, stub.pulls
	stub.mu.Unlock()
	if branches != 1 || pulls != 0 || put != nil {
		t.Fatalf("unvetted branch was used: branches=%d pulls=%d put=%#v", branches, pulls, put)
	}
}

func TestGitHubPushPullRequestRetainsBaseDriftWitness(t *testing.T) {
	stub := newGitHubWriteStub(t)
	server := newGitHubTestServer(t, stub.server.URL)
	seedGitHubConnection(t, server)
	stored := storeGitHubTestScript(t, server)
	stub.mu.Lock()
	stub.fileSHA = "baseblob"
	stub.mu.Unlock()
	linkStore := server.store.(storage.GitHubScriptLinkStore)
	original := &storage.GitHubScriptLink{
		ScriptID: stored.ID, RefKey: stored.RefKey, Owner: "octocat", Repo: "scripts", Branch: "main",
		Path: "apexload/scripts/x.json", RemoteBlobSHA: "baseblob", RemoteCommitSHA: "basecommit", ContentHash: "prior-local",
	}
	if err := linkStore.SaveGitHubScriptLink(original); err != nil {
		t.Fatalf("seed base witness: %v", err)
	}

	body, _ := json.Marshal(githubPushRequest{
		Owner: "octocat", Repo: "scripts", Branch: "main", Path: "apexload/scripts/x.json", Strategy: "pr",
	})
	postGitHubJSON(t, server, "/api/v1/scripts/"+stored.ID+"/github/push", body, http.StatusOK)
	link, err := linkStore.GetGitHubScriptLink(stored.ID)
	if err != nil {
		t.Fatalf("read base witness: %v", err)
	}
	if link == nil || !strings.HasPrefix(link.Branch, "apexload/") || link.RemoteBlobSHA != "newblob" ||
		link.BaseBranch != original.Branch || link.BaseBlobSHA != original.RemoteBlobSHA || link.BaseMissing || link.ContentHash == "" {
		t.Fatalf("single-script PR did not retain its base witness: got %#v, prior %#v", link, original)
	}
}

func TestGitHubPushPullRequestRejectsUnrelatedLinkBeforeMutation(t *testing.T) {
	stub := newGitHubWriteStub(t)
	server := newGitHubTestServer(t, stub.server.URL)
	seedGitHubConnection(t, server)
	stored := storeGitHubTestScript(t, server)
	linkStore := server.store.(storage.GitHubScriptLinkStore)
	original := &storage.GitHubScriptLink{
		ScriptID: stored.ID, RefKey: stored.RefKey, Owner: "octocat", Repo: "scripts", Branch: "release/old",
		Path: "apexload/scripts/x.json", RemoteBlobSHA: "releaseblob", ContentHash: "prior-local",
	}
	if err := linkStore.SaveGitHubScriptLink(original); err != nil {
		t.Fatalf("seed unrelated link: %v", err)
	}

	body, _ := json.Marshal(githubPushRequest{
		Owner: "octocat", Repo: "scripts", Branch: "main", Path: "apexload/scripts/x.json", Strategy: "pr",
	})
	postGitHubJSON(t, server, "/api/v1/scripts/"+stored.ID+"/github/push", body, http.StatusConflict)
	stub.mu.Lock()
	put, branchCreates, pulls := stub.lastPut, stub.branchCreates, stub.pulls
	stub.mu.Unlock()
	if put != nil || branchCreates != 0 || pulls != 0 {
		t.Fatalf("unrelated link allowed PR mutation: put=%#v branches=%d pulls=%d", put, branchCreates, pulls)
	}
	link, err := linkStore.GetGitHubScriptLink(stored.ID)
	if err != nil || link == nil || link.Branch != original.Branch || link.RemoteBlobSHA != original.RemoteBlobSHA {
		t.Fatalf("rejected PR changed unrelated link: got (%#v, %v), want %#v", link, err, original)
	}
}

func TestGitHubPushPullRequestRefreshesPendingHeadAndBaseWitness(t *testing.T) {
	stub := newGitHubWriteStub(t)
	server := newGitHubTestServer(t, stub.server.URL)
	seedGitHubConnection(t, server)
	stored := storeGitHubTestScript(t, server)
	stub.mu.Lock()
	stub.fileSHA = "baseblob"
	stub.mu.Unlock()

	firstBody, _ := json.Marshal(githubPushRequest{
		Owner: "octocat", Repo: "scripts", Branch: "main", Path: "apexload/scripts/x.json",
		Strategy: "pr", PRBranch: "apexload/first",
	})
	postGitHubJSON(t, server, "/api/v1/scripts/"+stored.ID+"/github/push", firstBody, http.StatusOK)

	// The first PUT affected only the temporary head in real GitHub. Reset the
	// branch-agnostic test stub so the next base preflight sees the same blob.
	stub.mu.Lock()
	stub.fileSHA = "baseblob"
	stub.mu.Unlock()
	secondBody, _ := json.Marshal(githubPushRequest{
		Owner: "octocat", Repo: "scripts", Branch: "main", Path: "apexload/scripts/x.json",
		Strategy: "pr", PRBranch: "apexload/second",
	})
	postGitHubJSON(t, server, "/api/v1/scripts/"+stored.ID+"/github/push", secondBody, http.StatusOK)

	link, err := server.store.(storage.GitHubScriptLinkStore).GetGitHubScriptLink(stored.ID)
	if err != nil || link == nil || link.Branch != "apexload/second" || link.RemoteBlobSHA != "newblob" ||
		link.BaseBranch != "main" || link.BaseBlobSHA != "baseblob" || link.BaseMissing {
		t.Fatalf("second PR did not refresh head plus base witness: got (%#v, %v)", link, err)
	}
}

func TestGitHubPushAfterFirstPullRequestDetectsBaseChange(t *testing.T) {
	stub := newGitHubWriteStub(t)
	server := newGitHubTestServer(t, stub.server.URL)
	seedGitHubConnection(t, server)
	stored := storeGitHubTestScript(t, server)
	stub.mu.Lock()
	stub.fileSHA = "baseblob"
	stub.mu.Unlock()

	prBody, _ := json.Marshal(githubPushRequest{
		Owner: "octocat", Repo: "scripts", Branch: "main", Path: "apexload/scripts/x.json",
		Strategy: "pr", PRBranch: "apexload/pending",
	})
	postGitHubJSON(t, server, "/api/v1/scripts/"+stored.ID+"/github/push", prBody, http.StatusOK)

	// The stub now returns newblob for main, modeling a base change after the
	// PR preflight. The secondary witness must stop a direct overwrite.
	commitBody, _ := json.Marshal(githubPushRequest{
		Owner: "octocat", Repo: "scripts", Branch: "main", Path: "apexload/scripts/x.json", Strategy: "commit",
	})
	postGitHubJSON(t, server, "/api/v1/scripts/"+stored.ID+"/github/push", commitBody, http.StatusConflict)
	stub.mu.Lock()
	putBranches := append([]string(nil), stub.putBranches...)
	stub.mu.Unlock()
	if len(putBranches) != 1 || putBranches[0] != "apexload/pending" {
		t.Fatalf("base change allowed another write: put branches=%v", putBranches)
	}
}

func TestGitHubBulkPushPullRequestRejectsUnrelatedLinkBeforeMutation(t *testing.T) {
	stub := newGitHubWriteStub(t)
	server := newGitHubTestServer(t, stub.server.URL)
	seedGitHubConnection(t, server)
	stored := storeGitHubTestScript(t, server)

	stub.mu.Lock()
	stub.fileSHA = "baseblob"
	stub.mu.Unlock()
	linkStore := server.store.(storage.GitHubScriptLinkStore)
	original := &storage.GitHubScriptLink{
		ScriptID: stored.ID, Owner: "octocat", Repo: "scripts", Branch: "release/old",
		Path: "apexload/scripts/team-a/" + stored.RefKey + ".json", RemoteBlobSHA: "stale-other-branch",
	}
	if err := linkStore.SaveGitHubScriptLink(original); err != nil {
		t.Fatalf("seed different-branch link: %v", err)
	}

	body, err := json.Marshal(githubSyncJobRequest{
		ProjectID: "team-a", Kind: "push", Owner: "octocat", Repo: "scripts", Branch: "main",
		ScriptIDs: []string{stored.ID}, Strategy: "pr",
	})
	if err != nil {
		t.Fatal(err)
	}
	job := postGitHubJSON(t, server, "/api/v1/integrations/github/sync-jobs", body, http.StatusCreated)
	if job["status"] != "partial" || job["updated"] != float64(0) || job["errors"] != float64(0) || job["conflicts"] != float64(1) {
		t.Fatalf("unexpected bulk PR job: %#v", job)
	}

	stub.mu.Lock()
	pulls := stub.pulls
	putBranches := append([]string(nil), stub.putBranches...)
	contentRefs := append([]string(nil), stub.contentRefs...)
	stub.mu.Unlock()
	if pulls != 0 || len(putBranches) != 0 {
		t.Fatalf("unrelated link allowed bulk PR mutation: pulls=%d put branches=%v", pulls, putBranches)
	}
	if len(contentRefs) != 1 || contentRefs[0] != "main" {
		t.Fatalf("contents refs = %v, want a complete base-branch preflight", contentRefs)
	}

	link, err := linkStore.GetGitHubScriptLink(stored.ID)
	if err != nil {
		t.Fatalf("read persisted link: %v", err)
	}
	if link == nil || link.Branch != original.Branch || link.RemoteBlobSHA != original.RemoteBlobSHA {
		t.Fatalf("rejected PR changed unrelated link: got %#v, want %#v", link, original)
	}
}

func TestGitHubBulkPushPullRequestLinksFirstTemporaryHead(t *testing.T) {
	stub := newGitHubWriteStub(t)
	server := newGitHubTestServer(t, stub.server.URL)
	seedGitHubConnection(t, server)
	stored := storeGitHubTestScript(t, server)
	stub.mu.Lock()
	stub.fileSHA = "baseblob"
	stub.mu.Unlock()

	body, _ := json.Marshal(githubSyncJobRequest{
		ProjectID: "team-a", Kind: "push", Owner: "octocat", Repo: "scripts", Branch: "main",
		ScriptIDs: []string{stored.ID}, Strategy: "pr",
	})
	job := postGitHubJSON(t, server, "/api/v1/integrations/github/sync-jobs", body, http.StatusCreated)
	if job["status"] != "complete" || job["updated"] != float64(1) {
		t.Fatalf("unexpected first bulk PR job: %#v", job)
	}
	link, err := server.store.(storage.GitHubScriptLinkStore).GetGitHubScriptLink(stored.ID)
	if err != nil || link == nil || !strings.HasPrefix(link.Branch, "apexload/bulk-") || link.RemoteBlobSHA != "newblob" || link.ContentHash == "" ||
		link.BaseBranch != "main" || link.BaseBlobSHA != "baseblob" || link.BaseMissing {
		t.Fatalf("first bulk PR temporary-head link = (%#v, %v)", link, err)
	}
}

func TestGitHubBulkPushPullRequestFailureDoesNotReplaceLink(t *testing.T) {
	stub := newGitHubWriteStub(t)
	server := newGitHubTestServer(t, stub.server.URL)
	seedGitHubConnection(t, server)
	stored := storeGitHubTestScript(t, server)

	stub.mu.Lock()
	stub.fileSHA = "baseblob"
	stub.pullStatus = http.StatusInternalServerError
	stub.mu.Unlock()
	linkStore := server.store.(storage.GitHubScriptLinkStore)
	original := &storage.GitHubScriptLink{
		ScriptID: stored.ID, RefKey: stored.RefKey, Owner: "octocat", Repo: "scripts", Branch: "main",
		Path: "apexload/scripts/team-a/" + stored.RefKey + ".json", RemoteBlobSHA: "baseblob", RemoteCommitSHA: "basecommit", ContentHash: "old-local-hash",
	}
	if err := linkStore.SaveGitHubScriptLink(original); err != nil {
		t.Fatalf("seed original link: %v", err)
	}

	body, err := json.Marshal(githubSyncJobRequest{
		ProjectID: "team-a", Kind: "push", Owner: "octocat", Repo: "scripts", Branch: "main",
		ScriptIDs: []string{stored.ID}, Strategy: "pr",
	})
	if err != nil {
		t.Fatal(err)
	}
	job := postGitHubJSON(t, server, "/api/v1/integrations/github/sync-jobs", body, http.StatusCreated)
	if job["status"] != "partial" || job["updated"] != float64(1) || job["errors"] != float64(1) {
		t.Fatalf("unexpected failed bulk PR job: %#v", job)
	}

	link, err := linkStore.GetGitHubScriptLink(stored.ID)
	if err != nil {
		t.Fatalf("read link after PR failure: %v", err)
	}
	if link == nil || link.Branch != original.Branch || link.RemoteBlobSHA != original.RemoteBlobSHA || link.RemoteCommitSHA != original.RemoteCommitSHA || link.ContentHash != original.ContentHash {
		t.Fatalf("PR failure replaced the last successful link: got %#v, want %#v", link, original)
	}
}

func TestGitHubBulkPushPullRequestRetainsExistingBaseWitness(t *testing.T) {
	stub := newGitHubWriteStub(t)
	server := newGitHubTestServer(t, stub.server.URL)
	seedGitHubConnection(t, server)
	stored := storeGitHubTestScript(t, server)
	stub.mu.Lock()
	stub.fileSHA = "baseblob"
	stub.mu.Unlock()
	linkStore := server.store.(storage.GitHubScriptLinkStore)
	targetPath := "apexload/scripts/team-a/" + stored.RefKey + ".json"
	original := &storage.GitHubScriptLink{
		ScriptID: stored.ID, RefKey: stored.RefKey, Owner: "octocat", Repo: "scripts", Branch: "main",
		Path: targetPath, RemoteBlobSHA: "baseblob", RemoteCommitSHA: "basecommit", ContentHash: "prior-local",
	}
	if err := linkStore.SaveGitHubScriptLink(original); err != nil {
		t.Fatalf("seed base witness: %v", err)
	}

	body, _ := json.Marshal(githubSyncJobRequest{
		ProjectID: "team-a", Kind: "push", Owner: "octocat", Repo: "scripts", Branch: "main",
		ScriptIDs: []string{stored.ID}, Strategy: "pr",
	})
	job := postGitHubJSON(t, server, "/api/v1/integrations/github/sync-jobs", body, http.StatusCreated)
	if job["status"] != "complete" || job["updated"] != float64(1) {
		t.Fatalf("unexpected bulk PR job: %#v", job)
	}
	link, err := linkStore.GetGitHubScriptLink(stored.ID)
	if err != nil {
		t.Fatalf("read base witness: %v", err)
	}
	if link == nil || !strings.HasPrefix(link.Branch, "apexload/bulk-") || link.RemoteBlobSHA != "newblob" ||
		link.BaseBranch != original.Branch || link.BaseBlobSHA != original.RemoteBlobSHA || link.BaseMissing || link.ContentHash == "" {
		t.Fatalf("bulk PR did not retain its base witness: got %#v, prior %#v", link, original)
	}

	// The stub now reports a different blob on main. The retained base witness
	// must turn a later direct push into a conflict rather than an overwrite.
	settings := server.currentGitHubSettings()
	client, err := newGitHubClient(settings, "gho_testtoken", time.Second)
	if err != nil {
		t.Fatalf("newGitHubClient: %v", err)
	}
	direct := &storage.GitHubSyncJob{ProjectID: "team-a", Owner: "octocat", Repo: "scripts", Branch: "main"}
	newGitHubSyncService(server.runner, nil, linkStore).runPush(
		context.Background(), &githubResolvedClient{Client: client, Settings: settings}, direct, []string{stored.ID}, "commit",
	)
	if direct.Conflicts != 1 || direct.Updated != 0 {
		t.Fatalf("later base push = %#v, want retained-witness conflict", direct)
	}
}

type githubLinkReadErrorStore struct {
	storage.GitHubScriptSyncClaimStore
	err error
}

type storeWithoutGitHubLinks struct {
	storage.Store
	storage.GitHubEnterpriseStore
}

type storeWithoutGitHubEnterprise struct {
	storage.Store
	storage.GitHubScriptLinkStore
}

type storeWithoutGitHubClaims struct {
	storage.Store
	storage.GitHubEnterpriseStore
	storage.GitHubScriptLinkStore
}

type storeWithoutGitHubImport struct {
	storage.Store
	storage.GitHubEnterpriseStore
	storage.GitHubScriptLinkStore
}

func (s githubLinkReadErrorStore) SaveGitHubScriptLink(*storage.GitHubScriptLink) error {
	return s.err
}

func (s githubLinkReadErrorStore) GetGitHubScriptLink(string) (*storage.GitHubScriptLink, error) {
	return nil, s.err
}

func (s githubLinkReadErrorStore) DeleteGitHubScriptLink(string) error {
	return s.err
}

func TestGitHubBulkPushDoesNotWriteAfterPreflightReadErrors(t *testing.T) {
	tests := []struct {
		name          string
		configureStub func(*githubWriteStub)
		linkStore     func(*Server) storage.GitHubScriptLinkStore
	}{
		{
			name: "github contents",
			configureStub: func(stub *githubWriteStub) {
				stub.contentErr = http.StatusInternalServerError
			},
			linkStore: func(server *Server) storage.GitHubScriptLinkStore {
				return server.store.(storage.GitHubScriptLinkStore)
			},
		},
		{
			name:          "stored link",
			configureStub: func(*githubWriteStub) {},
			linkStore: func(server *Server) storage.GitHubScriptLinkStore {
				return githubLinkReadErrorStore{
					GitHubScriptSyncClaimStore: server.store.(storage.GitHubScriptSyncClaimStore),
					err:                        errors.New("link read failed"),
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := newGitHubWriteStub(t)
			server := newGitHubTestServer(t, stub.server.URL)
			stored := storeGitHubTestScript(t, server)
			stub.mu.Lock()
			test.configureStub(stub)
			stub.mu.Unlock()

			settings := server.currentGitHubSettings()
			client, err := newGitHubClient(settings, "gho_testtoken", time.Second)
			if err != nil {
				t.Fatalf("newGitHubClient: %v", err)
			}
			job := &storage.GitHubSyncJob{ProjectID: "team-a", Owner: "octocat", Repo: "scripts", Branch: "main"}
			service := newGitHubSyncService(server.runner, nil, test.linkStore(server))
			service.runPush(context.Background(), &githubResolvedClient{Client: client, Settings: settings}, job, []string{stored.ID}, "commit")

			stub.mu.Lock()
			put, contentGets := stub.lastPut, stub.contentGets
			stub.mu.Unlock()
			if job.Errors != 1 || job.Updated != 0 || job.Processed != 0 || put != nil || contentGets != 1 {
				t.Fatalf("job after preflight failure = %#v, put=%#v content_gets=%d", job, put, contentGets)
			}
		})
	}
}

func TestGitHubBulkPushRequiresLinkStorage(t *testing.T) {
	stub := newGitHubWriteStub(t)
	server := newGitHubTestServer(t, stub.server.URL)
	stored := storeGitHubTestScript(t, server)
	settings := server.currentGitHubSettings()
	client, err := newGitHubClient(settings, "gho_testtoken", time.Second)
	if err != nil {
		t.Fatalf("newGitHubClient: %v", err)
	}
	job := &storage.GitHubSyncJob{ProjectID: "team-a", Owner: "octocat", Repo: "scripts", Branch: "main"}
	newGitHubSyncService(server.runner, nil, nil).runPush(
		context.Background(), &githubResolvedClient{Client: client, Settings: settings}, job, []string{stored.ID}, "commit",
	)

	stub.mu.Lock()
	put, contentGets := stub.lastPut, stub.contentGets
	stub.mu.Unlock()
	if job.Errors != 1 || job.Updated != 0 || put != nil || contentGets != 0 {
		t.Fatalf("job without link storage = %#v, put=%#v content_gets=%d", job, put, contentGets)
	}
}

func TestGitHubSyncRunRequiresClientAndDurableJobStore(t *testing.T) {
	stub := newGitHubWriteStub(t)
	server := newGitHubTestServer(t, stub.server.URL)
	settings := server.currentGitHubSettings()
	client, err := newGitHubClient(settings, "gho_testtoken", time.Second)
	if err != nil {
		t.Fatalf("newGitHubClient: %v", err)
	}
	input := githubSyncJobInput{ProjectID: "team-a", Kind: "import", Owner: "octocat", Repo: "scripts", Branch: "main"}
	service := newGitHubSyncService(server.runner, server.store.(storage.GitHubEnterpriseStore), server.store.(storage.GitHubScriptLinkStore))
	if _, err := service.Run(context.Background(), nil, input); err == nil {
		t.Fatal("Run accepted a nil resolved client")
	}
	service = newGitHubSyncService(server.runner, nil, server.store.(storage.GitHubScriptLinkStore))
	if _, err := service.Run(context.Background(), &githubResolvedClient{Client: client, Settings: settings}, input); err == nil {
		t.Fatal("Run accepted unavailable durable job storage")
	}
	stub.mu.Lock()
	put, contentGets := stub.lastPut, stub.contentGets
	stub.mu.Unlock()
	if put != nil || contentGets != 0 {
		t.Fatalf("invalid Run dependencies touched GitHub: put=%#v content_gets=%d", put, contentGets)
	}
}

func TestGitHubBulkImportFailsClosedWithoutAtomicImportStorage(t *testing.T) {
	stub := newGitHubWriteStub(t)
	server := newGitHubTestServer(t, stub.server.URL)
	seedGitHubConnection(t, server)
	baseStore := server.store
	enterpriseStore := baseStore.(storage.GitHubEnterpriseStore)
	withoutImport := storeWithoutGitHubImport{
		Store:                 baseStore,
		GitHubEnterpriseStore: enterpriseStore,
		GitHubScriptLinkStore: baseStore.(storage.GitHubScriptLinkStore),
	}
	settings := server.currentGitHubSettings()
	client, err := newGitHubClient(settings, "gho_testtoken", time.Second)
	if err != nil {
		t.Fatalf("newGitHubClient: %v", err)
	}
	input := githubSyncJobInput{
		ProjectID: "team-a", Kind: "import", Owner: "octocat", Repo: "scripts", Branch: "main", Path: "apexload/scripts",
	}
	service := newGitHubSyncService(server.runner, enterpriseStore, withoutImport)
	if _, err := service.Run(context.Background(), &githubResolvedClient{Client: client, Settings: settings}, input); err == nil ||
		!strings.Contains(err.Error(), "atomic github import storage is unavailable") {
		t.Fatalf("Run without atomic import storage error = %v", err)
	}
	jobs, err := enterpriseStore.ListGitHubSyncJobs("team-a", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 0 {
		t.Fatalf("Run persisted %d jobs before checking atomic import capability", len(jobs))
	}

	server.store = withoutImport
	body, _ := json.Marshal(githubSyncJobRequest{
		ProjectID: "team-a", Kind: "import", Owner: "octocat", Repo: "scripts", Branch: "main", Path: "apexload/scripts",
	})
	postGitHubJSON(t, server, "/api/v1/integrations/github/sync-jobs", body, http.StatusServiceUnavailable)
	jobs, err = enterpriseStore.ListGitHubSyncJobs("team-a", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 0 {
		t.Fatalf("endpoint persisted %d jobs before checking atomic import capability", len(jobs))
	}
	stub.mu.Lock()
	put, contentGets := stub.lastPut, stub.contentGets
	stub.mu.Unlock()
	if put != nil || contentGets != 0 {
		t.Fatalf("missing atomic import capability touched GitHub: put=%#v content_gets=%d", put, contentGets)
	}
}

func TestGitHubBulkPushEndpointRejectsUnavailableLinkStorage(t *testing.T) {
	stub := newGitHubWriteStub(t)
	server := newGitHubTestServer(t, stub.server.URL)
	seedGitHubConnection(t, server)
	stored := storeGitHubTestScript(t, server)
	baseStore := server.store
	server.store = storeWithoutGitHubLinks{
		Store: baseStore, GitHubEnterpriseStore: baseStore.(storage.GitHubEnterpriseStore),
	}

	body, _ := json.Marshal(githubSyncJobRequest{
		ProjectID: "team-a", Kind: "push", Owner: "octocat", Repo: "scripts", Branch: "main",
		ScriptIDs: []string{stored.ID}, Strategy: "commit",
	})
	postGitHubJSON(t, server, "/api/v1/integrations/github/sync-jobs", body, http.StatusServiceUnavailable)
	stub.mu.Lock()
	put, contentGets := stub.lastPut, stub.contentGets
	stub.mu.Unlock()
	if put != nil || contentGets != 0 {
		t.Fatalf("endpoint touched GitHub without link storage: put=%#v content_gets=%d", put, contentGets)
	}
}

func TestGitHubPushEndpointsRejectUnavailableClaimStorage(t *testing.T) {
	stub := newGitHubWriteStub(t)
	server := newGitHubTestServer(t, stub.server.URL)
	seedGitHubConnection(t, server)
	stored := storeGitHubTestScript(t, server)
	baseStore := server.store
	server.store = storeWithoutGitHubClaims{
		Store:                 baseStore,
		GitHubEnterpriseStore: baseStore.(storage.GitHubEnterpriseStore),
		GitHubScriptLinkStore: baseStore.(storage.GitHubScriptLinkStore),
	}

	singleBody, _ := json.Marshal(githubPushRequest{
		Owner: "octocat", Repo: "scripts", Branch: "main", Path: "apexload/scripts/x.json", Strategy: "commit",
	})
	postGitHubJSON(t, server, "/api/v1/scripts/"+stored.ID+"/github/push", singleBody, http.StatusServiceUnavailable)

	bulkBody, _ := json.Marshal(githubSyncJobRequest{
		ProjectID: "team-a", Kind: "push", Owner: "octocat", Repo: "scripts", Branch: "main",
		ScriptIDs: []string{stored.ID}, Strategy: "commit",
	})
	postGitHubJSON(t, server, "/api/v1/integrations/github/sync-jobs", bulkBody, http.StatusServiceUnavailable)
	stub.mu.Lock()
	put, contentGets := stub.lastPut, stub.contentGets
	stub.mu.Unlock()
	if put != nil || contentGets != 0 {
		t.Fatalf("endpoint touched GitHub without claim storage: put=%#v content_gets=%d", put, contentGets)
	}
}

func TestGitHubBulkPushEndpointRejectsUnavailableJobStorage(t *testing.T) {
	stub := newGitHubWriteStub(t)
	server := newGitHubTestServer(t, stub.server.URL)
	seedGitHubConnection(t, server)
	stored := storeGitHubTestScript(t, server)
	baseStore := server.store
	server.store = storeWithoutGitHubEnterprise{
		Store: baseStore, GitHubScriptLinkStore: baseStore.(storage.GitHubScriptLinkStore),
	}

	body, _ := json.Marshal(githubSyncJobRequest{
		ProjectID: "team-a", Kind: "push", Owner: "octocat", Repo: "scripts", Branch: "main",
		ScriptIDs: []string{stored.ID}, Strategy: "commit",
	})
	postGitHubJSON(t, server, "/api/v1/integrations/github/sync-jobs", body, http.StatusServiceUnavailable)
	stub.mu.Lock()
	put, contentGets := stub.lastPut, stub.contentGets
	stub.mu.Unlock()
	if put != nil || contentGets != 0 {
		t.Fatalf("endpoint touched GitHub without job storage: put=%#v content_gets=%d", put, contentGets)
	}
}

func TestGitHubBulkPushPreflightsAllCandidatesBeforePRMutation(t *testing.T) {
	stub := newGitHubWriteStub(t)
	server := newGitHubTestServer(t, stub.server.URL)
	first := storeGitHubTestScript(t, server)
	second := storeGitHubTestScript(t, server)
	stub.mu.Lock()
	stub.contentErrAt = 2
	stub.mu.Unlock()

	settings := server.currentGitHubSettings()
	client, err := newGitHubClient(settings, "gho_testtoken", time.Second)
	if err != nil {
		t.Fatalf("newGitHubClient: %v", err)
	}
	job := &storage.GitHubSyncJob{ID: "preflight", ProjectID: "team-a", Owner: "octocat", Repo: "scripts", Branch: "main"}
	newGitHubSyncService(server.runner, nil, server.store.(storage.GitHubScriptLinkStore)).runPush(
		context.Background(), &githubResolvedClient{Client: client, Settings: settings}, job, []string{first.ID, second.ID}, "pr",
	)

	stub.mu.Lock()
	put, contentGets, branchCreates, pulls := stub.lastPut, stub.contentGets, stub.branchCreates, stub.pulls
	stub.mu.Unlock()
	if job.Errors != 1 || job.Updated != 0 || contentGets != 2 || branchCreates != 0 || pulls != 0 || put != nil {
		t.Fatalf("job after later preflight failure = %#v, gets=%d branches=%d pulls=%d put=%#v", job, contentGets, branchCreates, pulls, put)
	}
}

func TestGitHubBulkPushRejectsPathCollisionsBeforeMutation(t *testing.T) {
	stub := newGitHubWriteStub(t)
	server := newGitHubTestServer(t, stub.server.URL)
	first := storeGitHubTestScript(t, server)
	second := storeGitHubTestScript(t, server)
	settings := server.currentGitHubSettings()
	client, err := newGitHubClient(settings, "gho_testtoken", time.Second)
	if err != nil {
		t.Fatalf("newGitHubClient: %v", err)
	}
	job := &storage.GitHubSyncJob{ProjectID: "team-a", Owner: "octocat", Repo: "scripts", Branch: "main"}
	newGitHubSyncService(server.runner, nil, server.store.(storage.GitHubScriptLinkStore)).runPush(
		context.Background(), &githubResolvedClient{
			Client: client, Settings: settings, Policy: storage.GitHubProjectPolicy{PathTemplate: "same.json"},
		}, job, []string{first.ID, second.ID}, "commit",
	)

	stub.mu.Lock()
	put, contentGets := stub.lastPut, stub.contentGets
	stub.mu.Unlock()
	if job.Conflicts != 1 || job.Updated != 0 || contentGets != 0 || put != nil {
		t.Fatalf("colliding path job = %#v, gets=%d put=%#v", job, contentGets, put)
	}
}

func TestGitHubSyncClaimSerializesSingleAndBulkPushes(t *testing.T) {
	stub := newGitHubWriteStub(t)
	server := newGitHubTestServer(t, stub.server.URL)
	seedGitHubConnection(t, server)
	stored := storeGitHubTestScript(t, server)
	stub.mu.Lock()
	stub.blockFirstGet = true
	stub.getStarted = make(chan struct{})
	stub.getRelease = make(chan struct{})
	started := stub.getStarted
	release := stub.getRelease
	stub.mu.Unlock()
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()

	firstBody, _ := json.Marshal(githubPushRequest{
		Owner: "octocat", Repo: "scripts", Branch: "main", Path: "apexload/scripts/first.json", Strategy: "commit",
	})
	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/"+stored.ID+"/github/push", strings.NewReader(string(firstBody)))
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		firstDone <- resp
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("first push did not reach its blocked GitHub preflight")
	}

	secondBody, _ := json.Marshal(githubPushRequest{
		Owner: "octocat", Repo: "scripts", Branch: "release", Path: "apexload/scripts/second.json", Strategy: "commit",
	})
	postGitHubJSON(t, server, "/api/v1/scripts/"+stored.ID+"/github/push", secondBody, http.StatusConflict)

	settings := server.currentGitHubSettings()
	client, err := newGitHubClient(settings, "gho_testtoken", time.Second)
	if err != nil {
		t.Fatalf("newGitHubClient: %v", err)
	}
	job := &storage.GitHubSyncJob{ID: "concurrent", ProjectID: "team-a", Owner: "octocat", Repo: "scripts", Branch: "main"}
	newGitHubSyncService(server.runner, nil, server.store.(storage.GitHubScriptLinkStore)).runPush(
		context.Background(), &githubResolvedClient{Client: client, Settings: settings}, job, []string{stored.ID}, "commit",
	)
	stub.mu.Lock()
	contentGets, put := stub.contentGets, stub.lastPut
	stub.mu.Unlock()
	if job.Conflicts != 1 || job.Errors != 0 || job.Updated != 0 || contentGets != 1 || put != nil {
		t.Fatalf("concurrent bulk push = %#v, content_gets=%d put=%#v", job, contentGets, put)
	}

	close(release)
	released = true
	select {
	case response := <-firstDone:
		if response.Code != http.StatusOK {
			t.Fatalf("first push status = %d; body=%s", response.Code, response.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first push did not complete after its preflight was released")
	}
	link, err := server.store.(storage.GitHubScriptLinkStore).GetGitHubScriptLink(stored.ID)
	if err != nil || link == nil || link.Path != "apexload/scripts/first.json" {
		t.Fatalf("winning push link = (%#v, %v)", link, err)
	}
}

func TestGitHubPushCommitRejectsExistingRemoteWithoutWitness(t *testing.T) {
	stub := newGitHubWriteStub(t)
	server := newGitHubTestServer(t, stub.server.URL)
	seedGitHubConnection(t, server)
	stored := storeGitHubTestScript(t, server)
	stub.mu.Lock()
	stub.fileSHA = "existing-remote-blob"
	stub.mu.Unlock()

	body, _ := json.Marshal(githubPushRequest{
		Owner: "octocat", Repo: "scripts", Branch: "main",
		Path: "apexload/scripts/existing.json", Strategy: "commit",
	})
	postGitHubJSON(t, server, "/api/v1/scripts/"+stored.ID+"/github/push", body, http.StatusConflict)

	stub.mu.Lock()
	contentGets, put := stub.contentGets, stub.lastPut
	stub.mu.Unlock()
	if contentGets != 1 || put != nil {
		t.Fatalf("first direct sync touched existing remote: gets=%d put=%#v", contentGets, put)
	}
	link, err := server.store.(storage.GitHubScriptLinkStore).GetGitHubScriptLink(stored.ID)
	if err != nil || link != nil {
		t.Fatalf("rejected first sync link = (%#v, %v), want no link", link, err)
	}
}

func TestGitHubBulkPushCommitRejectsExistingRemoteWithoutWitness(t *testing.T) {
	stub := newGitHubWriteStub(t)
	server := newGitHubTestServer(t, stub.server.URL)
	stored := storeGitHubTestScript(t, server)
	stub.mu.Lock()
	stub.fileSHA = "existing-remote-blob"
	stub.mu.Unlock()
	settings := server.currentGitHubSettings()
	client, err := newGitHubClient(settings, "gho_testtoken", time.Second)
	if err != nil {
		t.Fatalf("newGitHubClient: %v", err)
	}
	job := &storage.GitHubSyncJob{ID: "existing-remote", ProjectID: "team-a", Owner: "octocat", Repo: "scripts", Branch: "main"}
	newGitHubSyncService(server.runner, nil, server.store.(storage.GitHubScriptLinkStore)).runPush(
		context.Background(), &githubResolvedClient{Client: client, Settings: settings}, job, []string{stored.ID}, "commit",
	)

	stub.mu.Lock()
	contentGets, put := stub.contentGets, stub.lastPut
	stub.mu.Unlock()
	if job.Conflicts != 1 || job.Errors != 0 || job.Updated != 0 || contentGets != 1 || put != nil {
		t.Fatalf("first bulk direct sync = %#v, gets=%d put=%#v", job, contentGets, put)
	}
}

func TestGitHubPushPullRequestAllowsExistingBaseWithoutPriorWitness(t *testing.T) {
	stub := newGitHubWriteStub(t)
	server := newGitHubTestServer(t, stub.server.URL)
	seedGitHubConnection(t, server)
	stored := storeGitHubTestScript(t, server)
	stub.mu.Lock()
	stub.fileSHA = "existing-base-blob"
	stub.mu.Unlock()

	body, _ := json.Marshal(githubPushRequest{
		Owner: "octocat", Repo: "scripts", Branch: "main", Path: "apexload/scripts/existing.json",
		Strategy: "pr", PRBranch: "apexload/review-existing",
	})
	postGitHubJSON(t, server, "/api/v1/scripts/"+stored.ID+"/github/push", body, http.StatusOK)

	stub.mu.Lock()
	putBranches := append([]string(nil), stub.putBranches...)
	stub.mu.Unlock()
	if len(putBranches) != 1 || putBranches[0] != "apexload/review-existing" {
		t.Fatalf("PR against existing base wrote branches %v", putBranches)
	}
	link, err := server.store.(storage.GitHubScriptLinkStore).GetGitHubScriptLink(stored.ID)
	if err != nil || link == nil || link.BaseBranch != "main" || link.BaseBlobSHA != "existing-base-blob" || link.BaseMissing {
		t.Fatalf("PR existing-base witness = (%#v, %v)", link, err)
	}
}

func TestGitHubPushRejectsTargetOwnedByDifferentScript(t *testing.T) {
	stub := newGitHubWriteStub(t)
	server := newGitHubTestServer(t, stub.server.URL)
	seedGitHubConnection(t, server)
	first := storeGitHubTestScript(t, server)
	second := storeGitHubTestScript(t, server)

	firstBody, _ := json.Marshal(githubPushRequest{
		Owner: "octocat", Repo: "scripts", Branch: "main",
		Path: "apexload/scripts/shared.json", Strategy: "commit",
	})
	postGitHubJSON(t, server, "/api/v1/scripts/"+first.ID+"/github/push", firstBody, http.StatusOK)

	// Owner and repository names are case-insensitive GitHub coordinates. The
	// second script must be rejected by the durable target fence before it can
	// make even a remote preflight request.
	secondBody, _ := json.Marshal(githubPushRequest{
		Owner: "OCTOCAT", Repo: "SCRIPTS", Branch: "main",
		Path: "apexload/scripts/shared.json", Strategy: "commit",
	})
	postGitHubJSON(t, server, "/api/v1/scripts/"+second.ID+"/github/push", secondBody, http.StatusConflict)

	stub.mu.Lock()
	contentGets := stub.contentGets
	putBranches := append([]string(nil), stub.putBranches...)
	stub.mu.Unlock()
	if contentGets != 1 || len(putBranches) != 1 || putBranches[0] != "main" {
		t.Fatalf("target owner fence allowed remote activity: gets=%d put branches=%v", contentGets, putBranches)
	}
	link, err := server.store.(storage.GitHubScriptLinkStore).GetGitHubScriptLink(second.ID)
	if err != nil || link != nil {
		t.Fatalf("rejected script link = (%#v, %v), want no link", link, err)
	}
}

func TestGitHubPullRequestOwnsBothBaseAndHeadTargets(t *testing.T) {
	stub := newGitHubWriteStub(t)
	server := newGitHubTestServer(t, stub.server.URL)
	seedGitHubConnection(t, server)
	first := storeGitHubTestScript(t, server)
	second := storeGitHubTestScript(t, server)
	const targetPath = "apexload/scripts/shared-pr.json"
	const headBranch = "apexload/shared-pr"

	prBody, _ := json.Marshal(githubPushRequest{
		Owner: "octocat", Repo: "scripts", Branch: "main", Path: targetPath,
		Strategy: "pr", PRBranch: headBranch,
	})
	postGitHubJSON(t, server, "/api/v1/scripts/"+first.ID+"/github/push", prBody, http.StatusOK)

	for _, branch := range []string{"main", headBranch} {
		commitBody, _ := json.Marshal(githubPushRequest{
			Owner: "octocat", Repo: "scripts", Branch: branch, Path: targetPath, Strategy: "commit",
		})
		postGitHubJSON(t, server, "/api/v1/scripts/"+second.ID+"/github/push", commitBody, http.StatusConflict)
	}

	stub.mu.Lock()
	contentGets := stub.contentGets
	putBranches := append([]string(nil), stub.putBranches...)
	pulls := stub.pulls
	stub.mu.Unlock()
	if contentGets != 1 || pulls != 1 || len(putBranches) != 1 || putBranches[0] != headBranch {
		t.Fatalf("PR target fence allowed remote activity: gets=%d pulls=%d put branches=%v", contentGets, pulls, putBranches)
	}
	link, err := server.store.(storage.GitHubScriptLinkStore).GetGitHubScriptLink(second.ID)
	if err != nil || link != nil {
		t.Fatalf("rejected script link = (%#v, %v), want no link", link, err)
	}
}

func TestGitHubTargetClaimSerializesDifferentScriptsAcrossSingleAndBulkPushes(t *testing.T) {
	stub := newGitHubWriteStub(t)
	server := newGitHubTestServer(t, stub.server.URL)
	seedGitHubConnection(t, server)
	first := storeGitHubTestScript(t, server)
	second := storeGitHubTestScript(t, server)
	stub.mu.Lock()
	stub.blockFirstGet = true
	stub.getStarted = make(chan struct{})
	stub.getRelease = make(chan struct{})
	started := stub.getStarted
	release := stub.getRelease
	stub.mu.Unlock()
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()

	firstBody, _ := json.Marshal(githubPushRequest{
		Owner: "octocat", Repo: "scripts", Branch: "main", Path: "same.json", Strategy: "commit",
	})
	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/"+first.ID+"/github/push", strings.NewReader(string(firstBody)))
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		firstDone <- resp
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("single push did not reach its blocked GitHub preflight")
	}

	settings := server.currentGitHubSettings()
	client, err := newGitHubClient(settings, "gho_testtoken", time.Second)
	if err != nil {
		t.Fatalf("newGitHubClient: %v", err)
	}
	job := &storage.GitHubSyncJob{ID: "cross-script-target", ProjectID: "team-a", Owner: "octocat", Repo: "scripts", Branch: "main"}
	newGitHubSyncService(server.runner, nil, server.store.(storage.GitHubScriptLinkStore)).runPush(
		context.Background(), &githubResolvedClient{
			Client: client, Settings: settings, Policy: storage.GitHubProjectPolicy{PathTemplate: "same.json"},
		}, job, []string{second.ID}, "commit",
	)
	stub.mu.Lock()
	contentGets, put := stub.contentGets, stub.lastPut
	stub.mu.Unlock()
	if job.Conflicts != 1 || job.Errors != 0 || job.Updated != 0 || contentGets != 1 || put != nil {
		t.Fatalf("cross-script bulk push = %#v, content_gets=%d put=%#v", job, contentGets, put)
	}

	close(release)
	released = true
	select {
	case response := <-firstDone:
		if response.Code != http.StatusOK {
			t.Fatalf("single push status = %d; body=%s", response.Code, response.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("single push did not complete after its preflight was released")
	}
}

func TestGitHubSinglePushClaimBlocksScriptDeletionDuringRemotePut(t *testing.T) {
	stub := newGitHubWriteStub(t)
	server := newGitHubTestServer(t, stub.server.URL)
	seedGitHubConnection(t, server)
	stored := storeGitHubTestScript(t, server)
	stub.mu.Lock()
	stub.blockFirstPut = true
	stub.putStarted = make(chan struct{})
	stub.putRelease = make(chan struct{})
	started := stub.putStarted
	release := stub.putRelease
	stub.mu.Unlock()
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()

	body, _ := json.Marshal(githubPushRequest{
		Owner: "octocat", Repo: "scripts", Branch: "main",
		Path: "apexload/scripts/delete-single.json", Strategy: "commit",
	})
	pushDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/"+stored.ID+"/github/push", strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		pushDone <- resp
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("single push did not reach its blocked remote PUT")
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/scripts/"+stored.ID, nil)
	deleteResp := httptest.NewRecorder()
	server.mux.ServeHTTP(deleteResp, deleteReq)
	if deleteResp.Code != http.StatusConflict {
		t.Fatalf("delete during single PUT status = %d, want %d; body=%s", deleteResp.Code, http.StatusConflict, deleteResp.Body.String())
	}
	if _, ok := server.runner.GetScript(stored.ID); !ok {
		t.Fatal("conflicting delete removed the script from the runner")
	}

	close(release)
	released = true
	select {
	case response := <-pushDone:
		if response.Code != http.StatusOK {
			t.Fatalf("single push status = %d; body=%s", response.Code, response.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("single push did not complete after its PUT was released")
	}
	deleteReq = httptest.NewRequest(http.MethodDelete, "/api/v1/scripts/"+stored.ID, nil)
	deleteResp = httptest.NewRecorder()
	server.mux.ServeHTTP(deleteResp, deleteReq)
	if deleteResp.Code != http.StatusOK {
		t.Fatalf("delete after single push status = %d, want %d; body=%s", deleteResp.Code, http.StatusOK, deleteResp.Body.String())
	}
}

func TestGitHubBulkPushClaimBlocksScriptDeletionDuringRemotePut(t *testing.T) {
	stub := newGitHubWriteStub(t)
	server := newGitHubTestServer(t, stub.server.URL)
	seedGitHubConnection(t, server)
	stored := storeGitHubTestScript(t, server)
	stub.mu.Lock()
	stub.blockFirstPut = true
	stub.putStarted = make(chan struct{})
	stub.putRelease = make(chan struct{})
	started := stub.putStarted
	release := stub.putRelease
	stub.mu.Unlock()
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()

	body, _ := json.Marshal(githubSyncJobRequest{
		ProjectID: "team-a", Kind: "push", Owner: "octocat", Repo: "scripts", Branch: "main",
		ScriptIDs: []string{stored.ID}, Strategy: "commit",
	})
	pushDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/integrations/github/sync-jobs", strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		pushDone <- resp
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("bulk push did not reach its blocked remote PUT")
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/scripts/"+stored.ID, nil)
	deleteResp := httptest.NewRecorder()
	server.mux.ServeHTTP(deleteResp, deleteReq)
	if deleteResp.Code != http.StatusConflict {
		t.Fatalf("delete during bulk PUT status = %d, want %d; body=%s", deleteResp.Code, http.StatusConflict, deleteResp.Body.String())
	}
	if _, ok := server.runner.GetScript(stored.ID); !ok {
		t.Fatal("conflicting delete removed the script from the runner")
	}

	close(release)
	released = true
	select {
	case response := <-pushDone:
		if response.Code != http.StatusCreated {
			t.Fatalf("bulk push status = %d; body=%s", response.Code, response.Body.String())
		}
		var job storage.GitHubSyncJob
		if err := json.Unmarshal(response.Body.Bytes(), &job); err != nil {
			t.Fatalf("decode bulk push job: %v", err)
		}
		if job.Status != "complete" || job.Updated != 1 || job.Errors != 0 || job.Conflicts != 0 {
			t.Fatalf("bulk push job after release = %#v", job)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("bulk push did not complete after its PUT was released")
	}
	deleteReq = httptest.NewRequest(http.MethodDelete, "/api/v1/scripts/"+stored.ID, nil)
	deleteResp = httptest.NewRecorder()
	server.mux.ServeHTTP(deleteResp, deleteReq)
	if deleteResp.Code != http.StatusOK {
		t.Fatalf("delete after bulk push status = %d, want %d; body=%s", deleteResp.Code, http.StatusOK, deleteResp.Body.String())
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

func TestGitHubStatusReportsBaseWitnessDrift(t *testing.T) {
	stub := newGitHubWriteStub(t)
	server := newGitHubTestServer(t, stub.server.URL)
	seedGitHubConnection(t, server)
	stored := storeGitHubTestScript(t, server)
	_, contentHash, err := githubScriptEnvelope(stored.Script)
	if err != nil {
		t.Fatalf("githubScriptEnvelope: %v", err)
	}
	if err := server.store.(storage.GitHubScriptLinkStore).SaveGitHubScriptLink(&storage.GitHubScriptLink{
		ScriptID: stored.ID, RefKey: stored.RefKey, Owner: "octocat", Repo: "scripts",
		Branch: "apexload/pending", Path: "apexload/scripts/x.json", RemoteBlobSHA: "headblob",
		BaseBranch: "main", BaseBlobSHA: "baseblob", ContentHash: contentHash,
	}); err != nil {
		t.Fatalf("seed pending PR link: %v", err)
	}
	stub.mu.Lock()
	stub.fileSHA = "headblob"
	stub.mu.Unlock()

	status := doGitHubJSON(t, server, http.MethodGet, "/api/v1/scripts/"+stored.ID+"/github/status")
	if status["remote_drift"] != false || status["base_remote_checked"] != true ||
		status["base_remote_drift"] != true || status["status"] != "base_drift" {
		t.Fatalf("expected base witness drift, got %#v", status)
	}
	if status["base_branch"] != "main" || status["base_blob_sha"] != "baseblob" || status["base_missing"] != false {
		t.Fatalf("base witness fields missing from status: %#v", status)
	}

	stub.mu.Lock()
	stub.contentGets = 0
	stub.contentErrAt = 2
	stub.mu.Unlock()
	status = doGitHubJSON(t, server, http.MethodGet, "/api/v1/scripts/"+stored.ID+"/github/status")
	if status["remote_drift"] != false || status["base_remote_check_failed"] != true || status["status"] != "partial_remote_check" {
		t.Fatalf("failed base check was reported as fully synced: %#v", status)
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

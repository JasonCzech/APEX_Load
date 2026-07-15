package api

import (
	"bytes"
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func testGitHubAppKeyPEM(t *testing.T) (string, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}
	return string(pem.EncodeToMemory(block)), key
}

func TestCreateGitHubAppJWTSignsRS256(t *testing.T) {
	keyPEM, key := testGitHubAppKeyPEM(t)
	t.Setenv("APEXLOAD_TEST_APP_KEY", keyPEM)

	now := time.Unix(1_700_000_000, 0).UTC()
	token, err := createGitHubAppJWT(GitHubSettings{
		Enabled:             true,
		AppID:               12345,
		AppPrivateKeyEnvVar: "APEXLOAD_TEST_APP_KEY",
	}, now)
	if err != nil {
		t.Fatalf("createGitHubAppJWT: %v", err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("jwt should have 3 parts, got %q", token)
	}
	var payload map[string]any
	decodedPayload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if err := json.Unmarshal(decodedPayload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["iss"] != "12345" {
		t.Fatalf("payload iss = %#v, want 12345", payload["iss"])
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, sum[:], signature); err != nil {
		t.Fatalf("jwt signature did not verify: %v", err)
	}
}

func TestGitHubInstallationTokenCachePrunesAndStaysBounded(t *testing.T) {
	server := &Server{githubTokens: make(map[string]githubCachedToken)}
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	server.githubTokens["expired"] = githubCachedToken{
		Token:     "expired-token",
		ExpiresAt: now.Add(githubInstallationTokenSkew),
	}

	total := githubInstallationTokenCacheMaxEntries + 32
	for index := 0; index < total; index++ {
		key := fmt.Sprintf("installation:repo-%04d", index)
		server.cacheGitHubInstallationToken(key, githubCachedToken{
			Token:     fmt.Sprintf("token-%04d", index),
			ExpiresAt: now.Add(time.Hour + time.Duration(index)*time.Second),
		}, now)
	}

	if got := len(server.githubTokens); got != githubInstallationTokenCacheMaxEntries {
		t.Fatalf("cached installation tokens = %d, want %d", got, githubInstallationTokenCacheMaxEntries)
	}
	if _, ok := server.githubTokens["expired"]; ok {
		t.Fatal("expired installation token was not pruned")
	}
	if _, ok := server.githubTokens["installation:repo-0000"]; ok {
		t.Fatal("earliest-expiring installation token was not evicted")
	}
	newestKey := fmt.Sprintf("installation:repo-%04d", total-1)
	if cached, ok := server.githubCachedInstallationToken(newestKey, now); !ok || cached.Token == "" {
		t.Fatalf("newest cached token = (%#v, %t), want present", cached, ok)
	}

	server.cacheGitHubInstallationToken("already-expired", githubCachedToken{
		Token:     "should-not-be-cached",
		ExpiresAt: now.Add(githubInstallationTokenSkew),
	}, now)
	if _, ok := server.githubTokens["already-expired"]; ok {
		t.Fatal("unusable installation token was cached")
	}
}

func TestGitHubInstallationClientCachesToken(t *testing.T) {
	t.Setenv(githubAllowInsecureHTTPEnv, "true")
	keyPEM, _ := testGitHubAppKeyPEM(t)
	t.Setenv("APEXLOAD_TEST_APP_KEY", keyPEM)
	calls := 0
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/app/installations/101/access_tokens" {
			http.NotFound(w, r)
			return
		}
		calls++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode token request: %v", err)
		}
		repos, _ := body["repositories"].([]any)
		if len(repos) != 1 || repos[0] != "scripts" {
			t.Fatalf("repositories = %#v, want scripts", body["repositories"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "installation-token",
			"expires_at": time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
		})
	}))
	defer stub.Close()

	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})
	settings := GitHubSettings{
		Enabled:             true,
		AppID:               12345,
		AppPrivateKeyEnvVar: "APEXLOAD_TEST_APP_KEY",
		APIBaseURL:          stub.URL,
		WebBaseURL:          stub.URL,
	}

	first, err := server.githubInstallationClient(context.Background(), settings, 101, "octo", "scripts")
	if err != nil {
		t.Fatalf("first githubInstallationClient: %v", err)
	}
	second, err := server.githubInstallationClient(context.Background(), settings, 101, "octo", "scripts")
	if err != nil {
		t.Fatalf("second githubInstallationClient: %v", err)
	}
	if first.token != "installation-token" || second.token != "installation-token" {
		t.Fatalf("unexpected cached tokens: %q %q", first.token, second.token)
	}
	if calls != 1 {
		t.Fatalf("token endpoint calls = %d, want 1", calls)
	}
}

func TestVerifyGitHubWebhookSignature(t *testing.T) {
	payload := []byte(`{"zen":"keep it logically tidy"}`)
	mac := hmac.New(sha256.New, []byte("secret"))
	mac.Write(payload)
	header := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if !verifyGitHubWebhookSignature("secret", payload, header) {
		t.Fatal("valid webhook signature did not verify")
	}
	if verifyGitHubWebhookSignature("secret", payload, header+"bad") {
		t.Fatal("invalid webhook signature verified")
	}
}

func TestEnforceGitHubProjectPolicy(t *testing.T) {
	policy := normalizeGitHubProjectPolicy(storage.GitHubProjectPolicy{
		Enabled:            true,
		AllowedRepos:       []string{"octo/*"},
		AllowedBranches:    []string{"main", "release/*"},
		PathTemplate:       "apexload/scripts/{ref_key}.json",
		RequirePullRequest: true,
		AllowImport:        true,
		AllowPush:          true,
	})

	if err := enforceGitHubProjectPolicy(policy, "push", "octo", "scripts", "main", "apexload/scripts/order.json", "commit"); err == nil {
		t.Fatal("direct commit should be rejected when PRs are required")
	}
	if err := enforceGitHubProjectPolicy(policy, "push", "octo", "scripts", "main", "apexload/scripts/order.json", "pr"); err != nil {
		t.Fatalf("PR push should be allowed, got %v", err)
	}
	if err := enforceGitHubProjectPolicy(policy, "import", "other", "scripts", "main", "apexload/scripts/order.json", ""); err == nil {
		t.Fatal("disallowed repo should be rejected")
	}
	if err := enforceGitHubProjectPolicy(policy, "import", "octo", "scripts", "dev", "apexload/scripts/order.json", ""); err == nil {
		t.Fatal("disallowed branch should be rejected")
	}
	if err := enforceGitHubProjectPolicy(policy, "import", "octo", "scripts", "main", "outside/order.json", ""); err == nil {
		t.Fatal("path outside template prefix should be rejected")
	}
	for _, test := range []struct {
		name   string
		owner  string
		repo   string
		branch string
		path   string
	}{
		{name: "owner traversal", owner: "..", repo: "scripts", branch: "main", path: "apexload/scripts/order.json"},
		{name: "repository traversal", owner: "octo", repo: "../scripts", branch: "main", path: "apexload/scripts/order.json"},
		{name: "branch traversal", owner: "octo", repo: "scripts", branch: "../main", path: "apexload/scripts/order.json"},
		{name: "path traversal", owner: "octo", repo: "scripts", branch: "main", path: "apexload/scripts/../../.github/workflows/pwn.yml"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := enforceGitHubProjectPolicy(policy, "import", test.owner, test.repo, test.branch, test.path, ""); err == nil {
				t.Fatal("unsafe GitHub target should be rejected")
			}
		})
	}
}

func TestGitHubControllerCredentialRequiresEnabledProjectPolicy(t *testing.T) {
	t.Setenv("APEXLOAD_TEST_GITHUB_PAT", "controller-pat")
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "github-policy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	project := &storage.Project{ID: "project-a", Name: "A", CreatedAt: now, UpdatedAt: now}
	if err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{Store: store})
	settings := server.runner.GetSystemSettings()
	settings.GitHub.Enabled = true
	settings.GitHub.PATEnvVar = "APEXLOAD_TEST_GITHUB_PAT"
	settings.GitHub.AutomationMode = "pat"
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}

	deniedResponse := httptest.NewRecorder()
	if _, ok := server.githubClientForProjectRequest(deniedResponse, httptest.NewRequest(http.MethodGet, "/", nil), project.ID, "octo", "scripts"); ok {
		t.Fatal("controller PAT was available without an enabled project policy")
	}
	if deniedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("disabled-policy PAT status = %d, want 401 body=%s", deniedResponse.Code, deniedResponse.Body.String())
	}

	project.Settings.GitHub = storage.GitHubProjectPolicy{
		Enabled:         true,
		AllowedRepos:    []string{"octo/scripts"},
		AllowedBranches: []string{"main"},
		AllowImport:     true,
		AllowPush:       true,
	}
	project.UpdatedAt = time.Now().UTC()
	if err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	allowedResponse := httptest.NewRecorder()
	if _, ok := server.githubClientForProjectRequest(allowedResponse, httptest.NewRequest(http.MethodGet, "/", nil), project.ID, "octo", "scripts"); !ok {
		t.Fatalf("enabled-policy PAT was rejected: status=%d body=%s", allowedResponse.Code, allowedResponse.Body.String())
	}
}

func TestCreateGitHubSyncJobRejectsInvalidKindBeforePersisting(t *testing.T) {
	stub := newGitHubStub(t)
	defer stub.Close()
	server := newGitHubTestServer(t, stub.URL)

	body := []byte(`{"project_id":"team-a","kind":"sideways","owner":"octocat","repo":"scripts"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/integrations/github/sync-jobs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("sync job status = %d, want 400; body=%s", resp.Code, resp.Body.String())
	}
	store := server.store.(storage.GitHubEnterpriseStore)
	jobs, err := store.ListGitHubSyncJobs("team-a", 10)
	if err != nil {
		t.Fatalf("ListGitHubSyncJobs: %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("persisted jobs = %d, want 0", len(jobs))
	}
}

func TestPublishGitHubCheckRejectsForeignProjectScript(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "github-check-project.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{ID: "project-b", Name: "B", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveScript(&dsl.Script{ID: "script-b", Name: "Foreign", ProjectID: "project-b", Protocol: "http"}); err != nil {
		t.Fatal(err)
	}
	server.runner.mu.Lock()
	server.runner.tests["test-a"] = &ManagedTest{ID: "test-a", Name: "A", ProjectID: "project-a", Status: "COMPLETE"}
	server.runner.mu.Unlock()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tests/test-a/github/check-run", strings.NewReader(`{"script_id":"script-b","owner":"octo","repo":"scripts","commit_sha":"abc"}`))
	req.SetPathValue("id", "test-a")
	resp := httptest.NewRecorder()
	server.handlePublishGitHubCheckRun(resp, req)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("foreign-script check publication status = %d, want 403; body=%s", resp.Code, resp.Body.String())
	}
}

func TestPublishGitHubCheckEnforcesProjectRepositoryPolicy(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "github-check-policy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{
		ID: "project-a", Name: "A", CreatedAt: now, UpdatedAt: now,
		Settings: storage.ProjectSettings{GitHub: storage.GitHubProjectPolicy{
			Enabled:         true,
			AllowedRepos:    []string{"approved/scripts"},
			AllowedBranches: []string{"main"},
			ChecksEnabled:   true,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{Store: store})
	server.runner.mu.Lock()
	server.runner.tests["test-a"] = &ManagedTest{ID: "test-a", Name: "A", ProjectID: "project-a", Status: "COMPLETE"}
	server.runner.mu.Unlock()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tests/test-a/github/check-run", strings.NewReader(`{"owner":"unapproved","repo":"scripts","branch":"main","commit_sha":"abc"}`))
	req.SetPathValue("id", "test-a")
	resp := httptest.NewRecorder()
	server.handlePublishGitHubCheckRun(resp, req)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("unapproved check repository status = %d, want 403; body=%s", resp.Code, resp.Body.String())
	}
}

func TestGitHubOAuthRefreshTokenRotation(t *testing.T) {
	t.Setenv(githubAllowInsecureHTTPEnv, "true")
	t.Setenv("APEXLOAD_GITHUB_TOKEN_KEY", "unit-test-encryption-key")
	t.Setenv("APEXLOAD_TEST_GH_SECRET", "client-secret")
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/login/oauth/access_token" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "old-refresh" {
			t.Fatalf("unexpected refresh form: %v", r.Form)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "new-access",
			"refresh_token": "new-refresh",
			"token_type":    "bearer",
			"scope":         "repo,workflow",
			"expires_in":    3600,
		})
	}))
	defer stub.Close()

	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})
	accessEnc, err := encryptGitHubToken("old-access")
	if err != nil {
		t.Fatalf("encrypt access: %v", err)
	}
	refreshEnc, err := encryptGitHubToken("old-refresh")
	if err != nil {
		t.Fatalf("encrypt refresh: %v", err)
	}
	expired := time.Now().UTC().Add(-time.Minute)
	conn := &storage.GitHubConnection{
		UserID: "local", AccessTokenEnc: accessEnc, RefreshTokenEnc: refreshEnc,
		TokenType: "bearer", Scopes: "repo", ExpiresAt: &expired,
	}
	if err := store.SaveGitHubConnection(conn); err != nil {
		t.Fatalf("SaveGitHubConnection: %v", err)
	}

	token, ok := server.githubTokenForConnection(context.Background(), GitHubSettings{
		Enabled:            true,
		ClientID:           "client-id",
		ClientSecretEnvVar: "APEXLOAD_TEST_GH_SECRET",
		APIBaseURL:         stub.URL,
		WebBaseURL:         stub.URL,
	}, conn, store)
	if !ok || token != "new-access" {
		t.Fatalf("githubTokenForConnection = (%q, %v), want new-access,true", token, ok)
	}
	stored, err := store.GetGitHubConnection("local")
	if err != nil || stored == nil {
		t.Fatalf("GetGitHubConnection = (%#v, %v)", stored, err)
	}
	rotatedRefresh, err := decryptGitHubToken(stored.RefreshTokenEnc)
	if err != nil || rotatedRefresh != "new-refresh" {
		t.Fatalf("rotated refresh = (%q, %v)", rotatedRefresh, err)
	}
}

func TestGitHubOAuthRefreshLosesToConcurrentDisconnect(t *testing.T) {
	t.Setenv(githubAllowInsecureHTTPEnv, "true")
	t.Setenv("APEXLOAD_GITHUB_TOKEN_KEY", "unit-test-encryption-key")
	t.Setenv("APEXLOAD_TEST_GH_SECRET", "client-secret")
	refreshStarted := make(chan struct{})
	allowRefresh := make(chan struct{})
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(refreshStarted)
		<-allowRefresh
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "new-access", "refresh_token": "new-refresh", "expires_in": 3600})
	}))
	defer stub.Close()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})
	accessEnc, _ := encryptGitHubToken("old-access")
	refreshEnc, _ := encryptGitHubToken("old-refresh")
	expired := time.Now().UTC().Add(-time.Minute)
	conn := &storage.GitHubConnection{UserID: "disconnecting", AccessTokenEnc: accessEnc, RefreshTokenEnc: refreshEnc, ExpiresAt: &expired}
	if err := store.SaveGitHubConnection(conn); err != nil {
		t.Fatal(err)
	}
	type tokenResult struct {
		token string
		ok    bool
	}
	result := make(chan tokenResult, 1)
	go func() {
		token, ok := server.githubTokenForConnection(context.Background(), GitHubSettings{Enabled: true, ClientID: "client-id", ClientSecretEnvVar: "APEXLOAD_TEST_GH_SECRET", APIBaseURL: stub.URL, WebBaseURL: stub.URL}, conn, store)
		result <- tokenResult{token: token, ok: ok}
	}()
	<-refreshStarted
	if err := store.DeleteGitHubConnection(conn.UserID); err != nil {
		t.Fatal(err)
	}
	close(allowRefresh)
	got := <-result
	if got.ok || got.token != "" {
		t.Fatalf("refresh after disconnect = (%q, %t), want unusable", got.token, got.ok)
	}
	if persisted, err := store.GetGitHubConnection(conn.UserID); err != nil || persisted != nil {
		t.Fatalf("connection after refresh race = %#v, %v", persisted, err)
	}
}

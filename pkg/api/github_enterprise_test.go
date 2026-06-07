package api

import (
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
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestGitHubInstallationClientCachesToken(t *testing.T) {
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
}

func TestGitHubOAuthRefreshTokenRotation(t *testing.T) {
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

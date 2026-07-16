package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-jose/go-jose/v3"
	jwtv5 "github.com/golang-jwt/jwt/v5"
	"github.com/jasonczech/apexload/pkg/storage"
)

const ciTestSigningKey = "apexload-ci-contract-test-signing-key-0123456789"

func ciTestActionsPolicy() storage.GitHubActionsPolicy {
	return storage.GitHubActionsPolicy{
		Enabled:                   true,
		Issuer:                    "https://token.actions.githubusercontent.com",
		ServerURL:                 "https://github.com",
		Audience:                  "apexload://ci-contract-tests",
		AllowedRepositoryIDs:      []string{"12345"},
		AllowedRepositories:       []string{"octo/apex"},
		AllowedRefs:               []string{"refs/heads/main"},
		AllowedWorkflowRefs:       []string{"octo/apex/.github/workflows/performance.yml@refs/heads/main"},
		AllowedEnvironments:       []string{"production"},
		RequiredRunnerEnvironment: "github-hosted",
		MaxVUsers:                 50,
		MaxDurationMinutes:        30,
		AllowedProtocols:          []string{"http"},
		AllowedTargetHosts:        []string{"api.example.test"},
		ChecksEnabled:             true,
		Revision:                  7,
	}
}

func ciTestIdentity() githubActionsOIDCClaims {
	return githubActionsOIDCClaims{
		Subject:           "repo:octo/apex:ref:refs/heads/main",
		JTI:               "oidc-jti-1",
		Repository:        "octo/apex",
		RepositoryID:      "12345",
		Ref:               "refs/heads/main",
		SHA:               "0123456789abcdef0123456789abcdef01234567",
		WorkflowRef:       "octo/apex/.github/workflows/performance.yml@refs/heads/main",
		Environment:       "production",
		RunnerEnvironment: "github-hosted",
		RunID:             "9001",
		RunAttempt:        "1",
		Actor:             "octocat",
		EventName:         "push",
	}
}

func newCIContractTestServer(t *testing.T, policy storage.GitHubActionsPolicy) (*Server, *storage.SQLiteStore) {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "ci-contract.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	now := time.Now().UTC()
	project := &storage.Project{
		ID: "project-ci", Name: "CI contract", CreatedAt: now, UpdatedAt: now,
		Settings: storage.ProjectSettings{GitHub: storage.GitHubProjectPolicy{Actions: policy}},
	}
	if err := store.SaveProject(project); err != nil {
		_ = store.Close()
		t.Fatalf("SaveProject: %v", err)
	}
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 4})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		_ = store.Close()
	})
	return server, store
}

func TestValidateGitHubActionsIdentityPolicyAllowAndDeny(t *testing.T) {
	basePolicy := ciTestActionsPolicy()
	baseClaims := ciTestIdentity()
	tests := []struct {
		name   string
		mutate func(*storage.GitHubActionsPolicy, *githubActionsOIDCClaims)
		wantOK bool
	}{
		{name: "allowed", wantOK: true},
		{name: "job workflow fallback", mutate: func(_ *storage.GitHubActionsPolicy, claims *githubActionsOIDCClaims) {
			claims.JobWorkflowRef, claims.WorkflowRef = claims.WorkflowRef, ""
		}, wantOK: true},
		{name: "disabled", mutate: func(policy *storage.GitHubActionsPolicy, _ *githubActionsOIDCClaims) { policy.Enabled = false }},
		{name: "missing immutable claim", mutate: func(_ *storage.GitHubActionsPolicy, claims *githubActionsOIDCClaims) { claims.SHA = "" }},
		{name: "repository id", mutate: func(_ *storage.GitHubActionsPolicy, claims *githubActionsOIDCClaims) { claims.RepositoryID = "999" }},
		{name: "repository name", mutate: func(_ *storage.GitHubActionsPolicy, claims *githubActionsOIDCClaims) {
			claims.Repository = "octo/other"
		}},
		{name: "ref", mutate: func(_ *storage.GitHubActionsPolicy, claims *githubActionsOIDCClaims) {
			claims.Ref = "refs/heads/feature"
		}},
		{name: "workflow", mutate: func(_ *storage.GitHubActionsPolicy, claims *githubActionsOIDCClaims) {
			claims.WorkflowRef = "octo/apex/.github/workflows/other.yml@refs/heads/main"
		}},
		{name: "environment", mutate: func(_ *storage.GitHubActionsPolicy, claims *githubActionsOIDCClaims) { claims.Environment = "staging" }},
		{name: "runner environment", mutate: func(_ *storage.GitHubActionsPolicy, claims *githubActionsOIDCClaims) {
			claims.RunnerEnvironment = "self-hosted"
		}},
		{name: "pull request target", mutate: func(_ *storage.GitHubActionsPolicy, claims *githubActionsOIDCClaims) {
			claims.EventName = "pull_request_target"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy, claims := basePolicy, baseClaims
			if test.mutate != nil {
				test.mutate(&policy, &claims)
			}
			err := validateGitHubActionsIdentity(policy, claims)
			if test.wantOK && err != nil {
				t.Fatalf("validateGitHubActionsIdentity returned error: %v", err)
			}
			if !test.wantOK && err == nil {
				t.Fatal("validateGitHubActionsIdentity accepted a denied identity")
			}
		})
	}
}

func TestCIBearerSigningExpiryAndPolicyInvalidation(t *testing.T) {
	t.Setenv("APEXLOAD_CI_TOKEN_KEY", ciTestSigningKey)
	policy := ciTestActionsPolicy()
	server, store := newCIContractTestServer(t, policy)

	token, expiresAt, err := server.issueCIBearerToken("project-ci", policy, ciTestIdentity())
	if err != nil {
		t.Fatalf("issueCIBearerToken: %v", err)
	}
	if !expiresAt.After(time.Now().Add(9*time.Minute)) || expiresAt.After(time.Now().Add(11*time.Minute)) {
		t.Fatalf("unexpected CI bearer expiry: %s", expiresAt)
	}
	claims, err := server.verifyCIBearer(token)
	if err != nil {
		t.Fatalf("verifyCIBearer: %v", err)
	}
	if claims.ProjectID != "project-ci" || claims.PolicyRevision != policy.Revision || len(claims.Scopes) != 2 {
		t.Fatalf("unexpected CI bearer claims: %#v", claims)
	}

	t.Setenv("APEXLOAD_CI_TOKEN_KEY", "different-ci-contract-test-signing-key-9876543210")
	if _, err := server.verifyCIBearer(token); err == nil {
		t.Fatal("token verified with a different controller signing key")
	}
	t.Setenv("APEXLOAD_CI_TOKEN_KEY", ciTestSigningKey)

	key, err := server.ciTokenSigningKey()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	expiredClaims := ciBearerClaims{
		ProjectID: "project-ci", PolicyRevision: policy.Revision,
		Repository: "octo/apex", RepositoryID: "12345", Ref: "refs/heads/main", SHA: ciTestIdentity().SHA,
		WorkflowRef: ciTestIdentity().WorkflowRef, Environment: "production", RunnerEnvironment: "github-hosted",
		RunID: "9001", RunAttempt: "1", EventName: "push", Scopes: []string{"tests:run", "results:read"},
		RegisteredClaims: jwtv5.RegisteredClaims{
			Issuer: ciBearerIssuer, Audience: jwtv5.ClaimStrings{ciBearerAudience}, Subject: "github-actions:12345",
			IssuedAt: jwtv5.NewNumericDate(now.Add(-time.Hour)), ExpiresAt: jwtv5.NewNumericDate(now.Add(-2 * time.Minute)),
		},
	}
	expiredRaw, err := jwtv5.NewWithClaims(jwtv5.SigningMethodHS256, expiredClaims).SignedString(key)
	if err != nil {
		t.Fatalf("sign expired token: %v", err)
	}
	if _, err := server.verifyCIBearer(ciBearerPrefix + expiredRaw); err == nil {
		t.Fatal("expired CI bearer token verified")
	}

	project, err := store.GetProject("project-ci")
	if err != nil || project == nil {
		t.Fatalf("GetProject: %#v, %v", project, err)
	}
	project.Settings.GitHub.Actions.Revision++
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject revision: %v", err)
	}
	if _, err := server.verifyCIBearer(token); err == nil {
		t.Fatal("token survived a policy revision")
	}

	updatedPolicy := project.Settings.GitHub.Actions
	newToken, _, err := server.issueCIBearerToken("project-ci", updatedPolicy, ciTestIdentity())
	if err != nil {
		t.Fatalf("issue revised token: %v", err)
	}
	project.Settings.GitHub.Actions.Enabled = false
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject disable: %v", err)
	}
	if _, err := server.verifyCIBearer(newToken); err == nil {
		t.Fatal("token survived policy disablement")
	}
}

func TestValidateCIRunnerServiceAccountLeastPrivilege(t *testing.T) {
	expires := time.Now().UTC().Add(time.Hour)
	valid := func() *storage.ServiceAccount {
		return &storage.ServiceAccount{
			ProjectRoles: map[string][]string{"project-ci": {RoleCIRunner}},
			Scopes:       []string{"tests:run", "results:read"},
			ExpiresAt:    &expires,
		}
	}
	tests := []struct {
		name   string
		mutate func(*storage.ServiceAccount)
		wantOK bool
	}{
		{name: "valid", wantOK: true},
		{name: "missing expiry", mutate: func(account *storage.ServiceAccount) { account.ExpiresAt = nil }},
		{name: "extra project role", mutate: func(account *storage.ServiceAccount) {
			account.ProjectRoles["project-ci"] = append(account.ProjectRoles["project-ci"], RoleViewer)
		}},
		{name: "extra global role", mutate: func(account *storage.ServiceAccount) { account.GlobalRoles = []string{RoleViewer} }},
		{name: "missing scope", mutate: func(account *storage.ServiceAccount) { account.Scopes = []string{"tests:run"} }},
		{name: "extra scope", mutate: func(account *storage.ServiceAccount) { account.Scopes = append(account.Scopes, "tests:write") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			account := valid()
			if test.mutate != nil {
				test.mutate(account)
			}
			err := validateCIRunnerServiceAccount(account)
			if test.wantOK && err != nil {
				t.Fatalf("validateCIRunnerServiceAccount returned error: %v", err)
			}
			if !test.wantOK && err == nil {
				t.Fatal("validateCIRunnerServiceAccount accepted an overprivileged account")
			}
		})
	}
}

type ciOIDCTestIssuer struct {
	server *httptest.Server
	mu     sync.RWMutex
	key    *rsa.PrivateKey
	kid    string
}

func newCIOIDCTestIssuer(t *testing.T) *ciOIDCTestIssuer {
	t.Helper()
	issuer := &ciOIDCTestIssuer{}
	issuer.rotate(t, "key-1")
	mux := http.NewServeMux()
	issuer.server = httptest.NewServer(mux)
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"issuer": issuer.server.URL, "jwks_uri": issuer.server.URL + "/jwks",
			"authorization_endpoint": issuer.server.URL + "/authorize", "token_endpoint": issuer.server.URL + "/token",
			"response_types_supported": []string{"id_token"}, "subject_types_supported": []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		issuer.mu.RLock()
		jwk := jose.JSONWebKey{Key: &issuer.key.PublicKey, KeyID: issuer.kid, Algorithm: string(jose.RS256), Use: "sig"}
		issuer.mu.RUnlock()
		writeJSON(w, http.StatusOK, map[string]any{"keys": []jose.JSONWebKey{jwk}})
	})
	t.Cleanup(issuer.server.Close)
	return issuer
}

func (issuer *ciOIDCTestIssuer) rotate(t *testing.T, kid string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate OIDC key: %v", err)
	}
	issuer.mu.Lock()
	issuer.key, issuer.kid = key, kid
	issuer.mu.Unlock()
}

func (issuer *ciOIDCTestIssuer) sign(t *testing.T, audience, tokenIssuer string, claims githubActionsOIDCClaims, expiresAt time.Time, nonce string) string {
	t.Helper()
	issuer.mu.RLock()
	key, kid := issuer.key, issuer.kid
	issuer.mu.RUnlock()
	if tokenIssuer == "" {
		tokenIssuer = issuer.server.URL
	}
	now := time.Now().UTC()
	payload := jwtv5.MapClaims{
		"iss": tokenIssuer, "sub": claims.Subject, "aud": audience, "iat": now.Unix(), "nbf": now.Add(-time.Second).Unix(), "exp": expiresAt.Unix(),
		"jti": claims.JTI, "repository": claims.Repository, "repository_id": claims.RepositoryID, "ref": claims.Ref, "sha": claims.SHA,
		"workflow_ref": claims.WorkflowRef, "job_workflow_ref": claims.JobWorkflowRef, "environment": claims.Environment,
		"runner_environment": claims.RunnerEnvironment, "run_id": claims.RunID, "run_attempt": claims.RunAttempt,
		"actor": claims.Actor, "event_name": claims.EventName, "nonce": nonce,
	}
	token := jwtv5.NewWithClaims(jwtv5.SigningMethodRS256, payload)
	token.Header["kid"] = kid
	raw, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("sign OIDC token: %v", err)
	}
	return raw
}

func exchangeCITestToken(t *testing.T, server *Server, projectID, idToken string) (int, githubCIExchangeResponse) {
	t.Helper()
	status, response, err := exchangeCITestTokenRaw(server, projectID, idToken, "192.0.2.10:12345")
	if err != nil {
		t.Fatal(err)
	}
	return status, response
}

func exchangeCITestTokenRaw(server *Server, projectID, idToken, remoteAddr string) (int, githubCIExchangeResponse, error) {
	body, err := json.Marshal(githubCIExchangeRequest{ProjectID: projectID, IDToken: idToken})
	if err != nil {
		return 0, githubCIExchangeResponse{}, err
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/ci/github/exchange", bytes.NewReader(body))
	req.RemoteAddr = remoteAddr
	rec := httptest.NewRecorder()
	server.handleGitHubCIExchange(rec, req)
	var response githubCIExchangeResponse
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			return rec.Code, githubCIExchangeResponse{}, fmt.Errorf("decode exchange response: %w; body=%s", err, rec.Body.String())
		}
	}
	return rec.Code, response, nil
}

func TestGitHubCIExchangeOIDCValidationReplayAndKeyRotation(t *testing.T) {
	t.Setenv("APEXLOAD_CI_TOKEN_KEY", ciTestSigningKey)
	t.Setenv("APEXLOAD_CI_OIDC_AUDIENCE", "apexload://ci-contract-tests")
	issuer := newCIOIDCTestIssuer(t)
	policy := ciTestActionsPolicy()
	policy.Issuer = issuer.server.URL
	policy.ServerURL = issuer.server.URL
	server, _ := newCIContractTestServer(t, policy)

	now := time.Now().UTC()
	identity := ciTestIdentity()
	validToken := issuer.sign(t, policy.Audience, "", identity, now.Add(5*time.Minute), "first")
	status, first := exchangeCITestToken(t, server, "project-ci", validToken)
	if status != http.StatusOK || first.AccessToken == "" {
		t.Fatalf("valid exchange = %d, %#v", status, first)
	}
	status, retry := exchangeCITestToken(t, server, "project-ci", validToken)
	if status != http.StatusOK || retry.AccessToken != first.AccessToken {
		t.Fatalf("idempotent exchange = %d, token_equal=%t", status, retry.AccessToken == first.AccessToken)
	}
	replayed := issuer.sign(t, policy.Audience, "", identity, now.Add(5*time.Minute), "different-fingerprint")
	if status, _ := exchangeCITestToken(t, server, "project-ci", replayed); status != http.StatusUnauthorized {
		t.Fatalf("same-jti replay status = %d, want 401", status)
	}

	tests := []struct {
		name        string
		audience    string
		tokenIssuer string
		mutate      func(*githubActionsOIDCClaims)
		expiresAt   time.Time
		wantStatus  int
	}{
		{name: "wrong audience", audience: "apexload://wrong", expiresAt: now.Add(5 * time.Minute), wantStatus: http.StatusUnauthorized},
		{name: "wrong issuer", audience: policy.Audience, tokenIssuer: "https://issuer.invalid", expiresAt: now.Add(5 * time.Minute), wantStatus: http.StatusUnauthorized},
		{name: "wrong repository", audience: policy.Audience, mutate: func(claims *githubActionsOIDCClaims) { claims.Repository = "octo/other" }, expiresAt: now.Add(5 * time.Minute), wantStatus: http.StatusForbidden},
		{name: "wrong ref", audience: policy.Audience, mutate: func(claims *githubActionsOIDCClaims) { claims.Ref = "refs/heads/feature" }, expiresAt: now.Add(5 * time.Minute), wantStatus: http.StatusForbidden},
		{name: "wrong workflow", audience: policy.Audience, mutate: func(claims *githubActionsOIDCClaims) {
			claims.WorkflowRef = "octo/apex/.github/workflows/other.yml@refs/heads/main"
		}, expiresAt: now.Add(5 * time.Minute), wantStatus: http.StatusForbidden},
		{name: "expired", audience: policy.Audience, expiresAt: now.Add(-2 * time.Minute), wantStatus: http.StatusUnauthorized},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			claims := ciTestIdentity()
			claims.JTI = fmt.Sprintf("negative-jti-%d", index)
			if test.mutate != nil {
				test.mutate(&claims)
			}
			raw := issuer.sign(t, test.audience, test.tokenIssuer, claims, test.expiresAt, test.name)
			if status, _ := exchangeCITestToken(t, server, "project-ci", raw); status != test.wantStatus {
				t.Fatalf("exchange status = %d, want %d", status, test.wantStatus)
			}
		})
	}

	issuer.rotate(t, "key-2")
	rotatedClaims := ciTestIdentity()
	rotatedClaims.JTI = "rotated-key-jti"
	rotated := issuer.sign(t, policy.Audience, "", rotatedClaims, time.Now().Add(5*time.Minute), "rotated")
	if status, response := exchangeCITestToken(t, server, "project-ci", rotated); status != http.StatusOK || response.AccessToken == "" {
		t.Fatalf("rotated-key exchange = %d, %#v", status, response)
	}
}

func TestGitHubCIExchangeConcurrentIdenticalTokenReturnsOneBearer(t *testing.T) {
	t.Setenv("APEXLOAD_CI_TOKEN_KEY", ciTestSigningKey)
	t.Setenv("APEXLOAD_CI_OIDC_AUDIENCE", "apexload://ci-contract-tests")
	issuer := newCIOIDCTestIssuer(t)
	policy := ciTestActionsPolicy()
	policy.Issuer = issuer.server.URL
	policy.ServerURL = issuer.server.URL
	server, _ := newCIContractTestServer(t, policy)
	idToken := issuer.sign(t, policy.Audience, "", ciTestIdentity(), time.Now().Add(5*time.Minute), "concurrent")

	const goroutines = 20
	type result struct {
		status   int
		response githubCIExchangeResponse
		err      error
	}
	results := make(chan result, goroutines)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for index := 0; index < goroutines; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			status, response, err := exchangeCITestTokenRaw(server, "project-ci", idToken, fmt.Sprintf("192.0.2.%d:12345", index+1))
			results <- result{status: status, response: response, err: err}
		}(index)
	}
	close(start)
	wg.Wait()
	close(results)

	var authoritativeToken string
	var authoritativeExpiry time.Time
	for result := range results {
		if result.err != nil || result.status != http.StatusOK {
			t.Fatalf("concurrent exchange status=%d err=%v", result.status, result.err)
		}
		if authoritativeToken == "" {
			authoritativeToken = result.response.AccessToken
			authoritativeExpiry = result.response.ExpiresAt
		}
		if result.response.AccessToken != authoritativeToken || !result.response.ExpiresAt.Equal(authoritativeExpiry) {
			t.Fatal("concurrent identical exchanges returned different bearer tokens")
		}
	}
}

func TestGitHubCIExchangeSharedRedisCoordinatesControllers(t *testing.T) {
	t.Setenv("APEXLOAD_CI_TOKEN_KEY", ciTestSigningKey)
	t.Setenv("APEXLOAD_CI_OIDC_AUDIENCE", "apexload://ci-contract-tests")
	issuer := newCIOIDCTestIssuer(t)
	policy := ciTestActionsPolicy()
	policy.Issuer = issuer.server.URL
	policy.ServerURL = issuer.server.URL
	serverA, store := newCIContractTestServer(t, policy)
	redisServer := miniredis.RunT(t)
	newRedis := func() CacheBackend {
		cache, err := newCacheBackend(cacheRuntimeConfig{
			Backend: cacheBackendRedis, RedisURL: "redis://" + redisServer.Addr() + "/0", KeyPrefix: "ci-ha-test",
			Timeout: time.Second, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second,
		})
		if err != nil {
			t.Fatalf("new Redis cache: %v", err)
		}
		return cache
	}
	serverA.cache = newRedis()
	serverB := NewServer(ServerConfig{Store: store, Cache: newRedis(), MaxConcurrent: 4})
	serverC := NewServer(ServerConfig{Store: store, Cache: newRedis(), MaxConcurrent: 4})
	for _, server := range []*Server{serverB, serverC} {
		server := server
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = server.Shutdown(ctx)
		})
	}

	identity := ciTestIdentity()
	idToken := issuer.sign(t, policy.Audience, "", identity, time.Now().Add(5*time.Minute), "shared")
	const goroutines = 16
	type result struct {
		status int
		token  string
		err    error
	}
	results := make(chan result, goroutines)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for index := 0; index < goroutines; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			server := serverA
			if index%2 == 1 {
				server = serverB
			}
			status, response, err := exchangeCITestTokenRaw(server, "project-ci", idToken, fmt.Sprintf("198.51.100.%d:12345", index+1))
			results <- result{status: status, token: response.AccessToken, err: err}
		}(index)
	}
	close(start)
	wg.Wait()
	close(results)

	var authoritative string
	for result := range results {
		if result.err != nil || result.status != http.StatusOK {
			t.Fatalf("cross-controller exchange status=%d err=%v", result.status, result.err)
		}
		if authoritative == "" {
			authoritative = result.token
		}
		if result.token != authoritative {
			t.Fatal("controllers sharing Redis returned different bearer tokens")
		}
	}

	replay := issuer.sign(t, policy.Audience, "", identity, time.Now().Add(5*time.Minute), "different-fingerprint")
	status, _, err := exchangeCITestTokenRaw(serverC, "project-ci", replay, "203.0.113.10:12345")
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusUnauthorized {
		t.Fatalf("cross-controller replay status = %d, want 401", status)
	}

	for _, key := range redisServer.Keys() {
		if strings.Contains(key, identity.JTI) || strings.Contains(key, "project-ci") || strings.Contains(key, issuer.server.URL) {
			t.Fatalf("Redis cache key exposes trusted exchange input: %q", key)
		}
	}
	key := ciOIDCExchangeKey(policy.Issuer, "project-ci", identity.JTI)
	if len(key) != sha256.Size*2 || strings.Contains(key, identity.JTI) || key == ciOIDCExchangeKey("https://different-issuer.example", "project-ci", identity.JTI) {
		t.Fatalf("CI exchange cache key is not an issuer-bound digest: %q", key)
	}
}

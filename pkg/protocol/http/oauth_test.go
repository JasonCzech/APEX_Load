package http

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func resetOAuthTokenCacheForTest() {
	oauthTokenCache = newBoundedOAuthTokenCache()
	// Never overwrite a live singleflight mutex: DoChan can still be
	// releasing it after its result is received. Cache reset is sufficient.
}

func TestFetchOAuth2TokenClientCredentials(t *testing.T) {
	resetOAuthTokenCacheForTest()
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Content-Type"); !strings.Contains(got, oauthFormContentType) {
			t.Fatalf("content type = %q, want form", got)
		}
		form := mustParseForm(t, r)
		if got := form.Get("grant_type"); got != OAuthGrantClientCredentials {
			t.Fatalf("grant_type = %q", got)
		}
		if got := form.Get("client_id"); got != "client-1" {
			t.Fatalf("client_id = %q", got)
		}
		if got := form.Get("client_secret"); got != "secret-1" {
			t.Fatalf("client_secret = %q", got)
		}
		if got := form.Get("scope"); got != "api refresh_token" {
			t.Fatalf("scope = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "token-1",
			"token_type":   "Bearer",
			"expires_in":   300,
			"instance_url": "https://example.my.salesforce.com",
		})
	}))
	defer server.Close()

	token, err := FetchOAuth2Token(context.Background(), OAuthTokenRequest{
		TokenURL:     server.URL,
		GrantType:    OAuthGrantClientCredentials,
		ClientID:     "client-1",
		ClientSecret: "secret-1",
		Scope:        "api refresh_token",
	})
	if err != nil {
		t.Fatalf("FetchOAuth2Token returned error: %v", err)
	}
	if token.AccessToken != "token-1" {
		t.Fatalf("access token = %q", token.AccessToken)
	}
	if token.TokenType != "Bearer" {
		t.Fatalf("token type = %q", token.TokenType)
	}
	if token.InstanceURL != "https://example.my.salesforce.com" {
		t.Fatalf("instance_url = %q", token.InstanceURL)
	}
	if time.Until(token.ExpiresAt) <= 0 {
		t.Fatalf("expires_at was not set in the future")
	}
	if calls != 1 {
		t.Fatalf("token endpoint calls = %d, want 1", calls)
	}
}

func TestFetchOAuth2TokenUsesConfiguredHTTPClient(t *testing.T) {
	resetOAuthTokenCacheForTest()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-OAuth-Client"); got != "configured" {
			t.Fatalf("X-OAuth-Client = %q, want configured", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "custom-client-token",
			"expires_in":   300,
		})
	}))
	defer server.Close()

	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		req.Header.Set("X-OAuth-Client", "configured")
		return http.DefaultTransport.RoundTrip(req)
	})}
	token, err := FetchOAuth2Token(context.Background(), OAuthTokenRequest{
		TokenURL:     server.URL,
		GrantType:    OAuthGrantClientCredentials,
		ClientID:     "client-1",
		ClientSecret: "secret-1",
		HTTPClient:   client,
	})
	if err != nil {
		t.Fatalf("FetchOAuth2Token returned error: %v", err)
	}
	if token.AccessToken != "custom-client-token" {
		t.Fatalf("access token = %q", token.AccessToken)
	}
}

func TestFetchOAuth2TokenAcceptsStringExpiresIn(t *testing.T) {
	resetOAuthTokenCacheForTest()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "string-expiry-token",
			"expires_in":   "600",
		})
	}))
	defer server.Close()

	token, err := FetchOAuth2Token(context.Background(), OAuthTokenRequest{
		TokenURL:     server.URL,
		GrantType:    OAuthGrantClientCredentials,
		ClientID:     "client-1",
		ClientSecret: "secret-1",
	})
	if err != nil {
		t.Fatalf("FetchOAuth2Token returned error: %v", err)
	}
	if token.AccessToken != "string-expiry-token" {
		t.Fatalf("access token = %q", token.AccessToken)
	}
	if time.Until(token.ExpiresAt) < 9*time.Minute {
		t.Fatalf("expires_at = %s, want roughly 10 minutes from now", token.ExpiresAt)
	}
}

func TestFetchOAuth2TokenPasswordGrant(t *testing.T) {
	resetOAuthTokenCacheForTest()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		form := mustParseForm(t, r)
		if got := form.Get("grant_type"); got != OAuthGrantPassword {
			t.Fatalf("grant_type = %q", got)
		}
		if got := form.Get("client_id"); got != "client-1" {
			t.Fatalf("client_id = %q", got)
		}
		if got := form.Get("username"); got != "operator@example.test" {
			t.Fatalf("username = %q", got)
		}
		if got := form.Get("password"); got != "secret-password" {
			t.Fatalf("password = %q", got)
		}
		if got := form.Get("client_secret"); got != "client-secret" {
			t.Fatalf("client_secret = %q", got)
		}
		if got := form.Get("scope"); got != "orders:read" {
			t.Fatalf("scope = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "password-token",
			"expires_in":   300,
		})
	}))
	defer server.Close()

	token, err := FetchOAuth2Token(context.Background(), OAuthTokenRequest{
		TokenURL:     server.URL,
		GrantType:    OAuthGrantPassword,
		ClientID:     "client-1",
		ClientSecret: "client-secret",
		Username:     "operator@example.test",
		Password:     "secret-password",
		Scope:        "orders:read",
	})
	if err != nil {
		t.Fatalf("FetchOAuth2Token returned error: %v", err)
	}
	if token.AccessToken != "password-token" {
		t.Fatalf("access token = %q", token.AccessToken)
	}
}

func TestFetchOAuth2TokenSharesConcurrentRefresh(t *testing.T) {
	resetOAuthTokenCacheForTest()
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		time.Sleep(25 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "shared-token",
			"expires_in":   300,
		})
	}))
	defer server.Close()

	req := OAuthTokenRequest{
		TokenURL:     server.URL,
		GrantType:    OAuthGrantClientCredentials,
		ClientID:     "client",
		ClientSecret: "secret",
	}
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, err := FetchOAuth2Token(context.Background(), req)
			if err != nil {
				errs <- err
				return
			}
			if token.AccessToken != "shared-token" {
				errs <- &tokenMismatchError{got: token.AccessToken}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("FetchOAuth2Token concurrent call failed: %v", err)
		}
	}
	if calls != 1 {
		t.Fatalf("token endpoint calls = %d, want 1", calls)
	}
}

func TestFetchOAuth2TokenJWTBearerAssertion(t *testing.T) {
	resetOAuthTokenCacheForTest()
	privateKeyPEM := testRSAPrivateKeyPEM(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		form := mustParseForm(t, r)
		if got := form.Get("grant_type"); got != jwtBearerGrantURN {
			t.Fatalf("grant_type = %q", got)
		}
		assertion := form.Get("assertion")
		if assertion == "" {
			t.Fatalf("assertion was empty")
		}
		claims := jwt.MapClaims{}
		_, _, err := jwt.NewParser().ParseUnverified(assertion, claims)
		if err != nil {
			t.Fatalf("parse assertion: %v", err)
		}
		if got := claims["iss"]; got != "connected-app" {
			t.Fatalf("iss = %v", got)
		}
		if got := claims["sub"]; got != "integration@example.com" {
			t.Fatalf("sub = %v", got)
		}
		if got := claims["aud"]; got != "https://login.salesforce.com" {
			t.Fatalf("aud = %v", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "jwt-token",
			"expires_in":   300,
		})
	}))
	defer server.Close()

	token, err := FetchOAuth2Token(context.Background(), OAuthTokenRequest{
		TokenURL:   server.URL,
		GrantType:  OAuthGrantJWTBearer,
		ClientID:   "connected-app",
		Audience:   "https://login.salesforce.com",
		Subject:    "integration@example.com",
		PrivateKey: privateKeyPEM,
	})
	if err != nil {
		t.Fatalf("FetchOAuth2Token returned error: %v", err)
	}
	if token.AccessToken != "jwt-token" {
		t.Fatalf("access token = %q", token.AccessToken)
	}
}

func TestFetchOAuth2TokenEndpointError(t *testing.T) {
	resetOAuthTokenCacheForTest()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":             "invalid_client",
			"error_description": "bad client secret",
		})
	}))
	defer server.Close()

	_, err := FetchOAuth2Token(context.Background(), OAuthTokenRequest{
		TokenURL:     server.URL,
		GrantType:    OAuthGrantClientCredentials,
		ClientID:     "client",
		ClientSecret: "wrong",
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "invalid_client") {
		t.Fatalf("error = %v, want invalid_client", err)
	}
}

func TestValidateOAuthTokenRequestRequiresSecureRemoteEndpoint(t *testing.T) {
	request := OAuthTokenRequest{
		TokenURL:     "http://identity.example.test/oauth/token",
		GrantType:    OAuthGrantClientCredentials,
		ClientID:     "client",
		ClientSecret: "secret",
	}
	if err := validateOAuthTokenRequest(request); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("remote plaintext token URL error = %v, want HTTPS rejection", err)
	}
	request.TokenURL = "http://127.0.0.1:8080/oauth/token"
	if err := validateOAuthTokenRequest(request); err != nil {
		t.Fatalf("loopback development token URL rejected: %v", err)
	}
	request.TokenURL = "file:///tmp/token"
	if err := validateOAuthTokenRequest(request); err == nil {
		t.Fatal("non-HTTP token URL was accepted")
	}
}

func TestHardenedOAuthHTTPClientSeparatesCredentialTransport(t *testing.T) {
	jar, err := newBoundedCookieJarWithBudget(&responseCaptureBudget{limit: 1024})
	if err != nil {
		t.Fatal(err)
	}
	baseTransport := &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion:         tls.VersionTLS10,
		InsecureSkipVerify: true,
	}}
	base := &http.Client{Transport: baseTransport, Jar: jar, Timeout: time.Second}
	client := hardenedOAuthHTTPClient(base)
	if client == nil || client.Jar != nil || client.CheckRedirect == nil {
		t.Fatalf("hardened OAuth client = %#v", client)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport == baseTransport {
		t.Fatalf("OAuth transport was not independently cloned: %#v", client.Transport)
	}
	if transport.TLSClientConfig == baseTransport.TLSClientConfig || transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("OAuth transport inherited insecure target TLS configuration")
	}
	if transport.MaxResponseHeaderBytes != maxOAuthResponseHeaderBytes {
		t.Fatalf("OAuth response header limit = %d, want %d", transport.MaxResponseHeaderBytes, maxOAuthResponseHeaderBytes)
	}
	if !baseTransport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("hardening mutated the ordinary load transport")
	}
}

func TestHardenedOAuthHTTPClientRefusesCredentialRedirect(t *testing.T) {
	resetOAuthTokenCacheForTest()
	var receiverCalls atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		receiverCalls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "stolen"})
	}))
	defer receiver.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", receiver.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()

	_, err := FetchOAuth2Token(context.Background(), OAuthTokenRequest{
		TokenURL: redirector.URL, GrantType: OAuthGrantClientCredentials, ClientID: "client", ClientSecret: "secret",
	})
	if err == nil || !strings.Contains(err.Error(), "307") {
		t.Fatalf("redirecting token endpoint error = %v", err)
	}
	if receiverCalls.Load() != 0 {
		t.Fatal("OAuth client forwarded credential-bearing request across a redirect")
	}
}

func TestFetchOAuth2TokenBoundsResponseFieldsAndTTL(t *testing.T) {
	for name, payload := range map[string]map[string]any{
		"access token": {"access_token": strings.Repeat("a", maxOAuthAccessTokenBytes+1)},
		"token type":   {"access_token": "token", "token_type": strings.Repeat("t", maxOAuthTokenTypeBytes+1)},
		"instance URL": {"access_token": "token", "instance_url": strings.Repeat("u", maxOAuthInstanceURLBytes+1)},
	} {
		t.Run(name, func(t *testing.T) {
			resetOAuthTokenCacheForTest()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(payload)
			}))
			defer server.Close()
			_, err := FetchOAuth2Token(context.Background(), OAuthTokenRequest{
				TokenURL: server.URL, GrantType: OAuthGrantClientCredentials, ClientID: "client", ClientSecret: "secret",
			})
			if err == nil || !strings.Contains(err.Error(), "exceeds") {
				t.Fatalf("oversized %s error = %v", name, err)
			}
		})
	}

	resetOAuthTokenCacheForTest()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"token","padding":"` + strings.Repeat("x", maxOAuthTokenResponseBytes) + `"}`))
	}))
	defer server.Close()
	_, err := FetchOAuth2Token(context.Background(), OAuthTokenRequest{
		TokenURL: server.URL, GrantType: OAuthGrantClientCredentials, ClientID: "client", ClientSecret: "secret",
	})
	if err == nil || !strings.Contains(err.Error(), "response exceeds") {
		t.Fatalf("oversized response error = %v", err)
	}

	resetOAuthTokenCacheForTest()
	ttlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "token", "expires_in": "999999999999999999999999"})
	}))
	defer ttlServer.Close()
	token, err := FetchOAuth2Token(context.Background(), OAuthTokenRequest{
		TokenURL: ttlServer.URL, GrantType: OAuthGrantClientCredentials, ClientID: "client", ClientSecret: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	remaining := time.Until(token.ExpiresAt)
	if remaining <= maxOAuthTokenTTL-time.Minute || remaining > maxOAuthTokenTTL {
		t.Fatalf("bounded token TTL = %s, want approximately %s", remaining, maxOAuthTokenTTL)
	}
}

func TestOAuthTokenFetchSlotsHonorCancellation(t *testing.T) {
	slots := make(chan struct{}, 1)
	if err := acquireOAuthTokenFetchSlot(context.Background(), slots); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := acquireOAuthTokenFetchSlot(ctx, slots); err == nil {
		t.Fatal("saturated OAuth acquisition gate ignored cancellation")
	}
	releaseOAuthTokenFetchSlot(slots)
	if err := acquireOAuthTokenFetchSlot(context.Background(), slots); err != nil {
		t.Fatalf("released OAuth acquisition slot was not reusable: %v", err)
	}
	releaseOAuthTokenFetchSlot(slots)
}

func TestBoundedOAuthTokenCacheEvictsLeastRecentlyUsed(t *testing.T) {
	cache := newBoundedOAuthTokenCacheWithLimits(2, 1024)
	expires := time.Now().Add(time.Hour)
	cache.store("first", OAuthToken{AccessToken: "one", ExpiresAt: expires})
	cache.store("second", OAuthToken{AccessToken: "two", ExpiresAt: expires})
	if _, ok := cache.load("first", time.Now()); !ok {
		t.Fatal("first cache entry was not available")
	}
	cache.store("third", OAuthToken{AccessToken: "three", ExpiresAt: expires})
	if _, ok := cache.load("second", time.Now()); ok {
		t.Fatal("least-recently-used OAuth token was not evicted")
	}
	if len(cache.entries) != 2 || cache.bytes > cache.maxBytes {
		t.Fatalf("cache retained entries=%d bytes=%d", len(cache.entries), cache.bytes)
	}
}

type tokenMismatchError struct {
	got string
}

func (e *tokenMismatchError) Error() string {
	return "access token = " + e.got
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func mustParseForm(t *testing.T, r *http.Request) url.Values {
	t.Helper()
	if err := r.ParseForm(); err != nil {
		t.Fatalf("parse form: %v", err)
	}
	return r.PostForm
}

func testRSAPrivateKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	der := x509.MarshalPKCS1PrivateKey(key)
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der}))
}

func TestFetchOAuth2TokenRedactsSecretsInError(t *testing.T) {
	resetOAuthTokenCacheForTest()
	const secret = "super-secret-value-1234567890"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A misbehaving endpoint echoes our own client_secret back in the
		// human-readable error_description.
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":             "invalid_client",
			"error_description": "client authentication failed for secret " + secret,
		})
	}))
	defer server.Close()

	_, err := FetchOAuth2Token(context.Background(), OAuthTokenRequest{
		TokenURL:     server.URL,
		GrantType:    OAuthGrantClientCredentials,
		ClientID:     "client-1",
		ClientSecret: secret,
	})
	if err == nil {
		t.Fatal("expected an error for a 400 token response")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked the client secret: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("error should mark the redaction: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "invalid_client") {
		t.Fatalf("error should retain the safe RFC 6749 error code: %q", err.Error())
	}
}

func TestRedactOAuthErrorDescriptionCapsLength(t *testing.T) {
	long := strings.Repeat("a", maxOAuthErrorDescriptionLen+50)
	got := redactOAuthErrorDescription(long, OAuthTokenRequest{})
	if len([]rune(got)) > maxOAuthErrorDescriptionLen+1 { // +1 for the ellipsis rune
		t.Fatalf("description not capped: len=%d", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("capped description should end with an ellipsis: %q", got)
	}
}

func TestOAuthClientSecretBasicUsesEncodedCredentials(t *testing.T) {
	resetOAuthTokenCacheForTest()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "client%3Aid" || password != "secret%2Bvalue" {
			t.Errorf("unexpected encoded basic credentials")
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Form.Has("client_id") || r.Form.Has("client_secret") {
			t.Error("credentials duplicated in body")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"basic-token","expires_in":3600}`))
	}))
	defer server.Close()
	token, err := FetchOAuth2Token(context.Background(), OAuthTokenRequest{TokenURL: server.URL, GrantType: OAuthGrantClientCredentials, ClientAuthentication: "client_secret_basic", ClientID: "client:id", ClientSecret: "secret+value"})
	if err != nil || token.AccessToken != "basic-token" {
		t.Fatal(token, err)
	}
}

func TestOAuthConcurrentWaiterCanCancel(t *testing.T) {
	resetOAuthTokenCacheForTest()
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		_, _ = w.Write([]byte(`{"access_token":"shared-token","expires_in":3600}`))
	}))
	defer server.Close()
	req := OAuthTokenRequest{TokenURL: server.URL, GrantType: OAuthGrantClientCredentials, ClientID: "cancel-client", ClientSecret: "secret"}
	first := make(chan error, 1)
	go func() { _, err := FetchOAuth2Token(context.Background(), req); first <- err }()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := FetchOAuth2Token(ctx, req)
	close(release)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err = <-first; err != nil {
		t.Fatal(err)
	}
}

func TestKafka331OAuthBasicAndCredentialSafeError(t *testing.T) {
	resetOAuthTokenCacheForTest()
	req := OAuthTokenRequest{GrantType: OAuthGrantClientCredentials, ClientAuthentication: "kafka_3_3_1_basic", ClientID: " client:id ", ClientSecret: " secret/? "}
	want := "Basic " + base64.URLEncoding.EncodeToString([]byte("client:id:secret/?"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != want {
			t.Error("Kafka 3.3.1 Basic wire format mismatch")
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Form.Has("client_id") || r.Form.Has("client_secret") {
			t.Error("duplicated credentials")
		}
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_client", "error_description": want})
	}))
	defer server.Close()
	req.TokenURL = server.URL
	_, err := FetchOAuth2Token(context.Background(), req)
	if err == nil || strings.Contains(err.Error(), strings.TrimPrefix(want, "Basic ")) {
		t.Fatal("encoded credential leaked")
	}
}

func TestOAuthCacheSeparatesAuthenticationAndSecretWhitespace(t *testing.T) {
	base := OAuthTokenRequest{TokenURL: "https://identity.test/token", GrantType: OAuthGrantClientCredentials, ClientID: "client", ClientSecret: "secret", ClientAuthentication: "client_secret_post"}
	first := oauthTokenCacheKey(base)
	base.ClientSecret = " secret "
	if first == oauthTokenCacheKey(base) {
		t.Fatal("distinct secrets share a token cache entry")
	}
	base.ClientSecret = "secret"
	base.ClientAuthentication = "client_secret_basic"
	if first == oauthTokenCacheKey(base) {
		t.Fatal("authentication methods share a token cache entry")
	}
}

func TestKafka331OAuthMatchesPinnedJavaVectors(t *testing.T) {
	resetOAuthTokenCacheForTest()
	data, err := os.ReadFile("testdata/kafka331_oauth_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors struct {
		Vectors [][]string `json:"vectors"`
	}
	if err = json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors.Vectors) != 7 {
		t.Fatal("incomplete pinned OAuth vectors")
	}
	decode := func(s string) string {
		b, e := base64.StdEncoding.DecodeString(s)
		if e != nil {
			t.Fatal(e)
		}
		return string(b)
	}
	for i, v := range vectors.Vectors {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, e := io.ReadAll(r.Body)
				if e != nil {
					t.Error(e)
				}
				if r.Header.Get("Authorization") != v[3] || string(body) != decode(v[4]) {
					t.Error("wire format differs from pinned Java callback")
				}
				_, _ = w.Write([]byte(`{"access_token":"synthetic-token","expires_in":60}`))
			}))
			defer server.Close()
			_, err := FetchOAuth2Token(context.Background(), OAuthTokenRequest{TokenURL: server.URL, GrantType: OAuthGrantClientCredentials, ClientAuthentication: "kafka_3_3_1_basic", ClientID: decode(v[0]), ClientSecret: decode(v[1]), Scope: decode(v[2])})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCancelledOAuthCallerCannotReceiveCachedToken(t *testing.T) {
	req := OAuthTokenRequest{TokenURL: "https://synthetic.invalid/token", GrantType: OAuthGrantClientCredentials, ClientID: "cancelled-cache-client", ClientSecret: "synthetic-secret", ClientAuthentication: "client_secret_post"}
	token := OAuthToken{AccessToken: "cached-token", ExpiresAt: time.Now().Add(time.Hour)}
	storeOAuthToken(oauthTokenCacheKey(req), token)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := FetchOAuth2Token(ctx, req)
	if !errors.Is(err, context.Canceled) || got.AccessToken != "" {
		t.Fatalf("cancelled caller received token: %v", err)
	}
}

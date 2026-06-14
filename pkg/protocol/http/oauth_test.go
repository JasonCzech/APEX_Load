package http

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/sync/singleflight"
)

func resetOAuthTokenCacheForTest() {
	oauthTokenCache = sync.Map{}
	oauthTokenGroup = singleflight.Group{}
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

type tokenMismatchError struct {
	got string
}

func (e *tokenMismatchError) Error() string {
	return "access token = " + e.got
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

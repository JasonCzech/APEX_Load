package dynatrace

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
}
func fixtureConfig() Config {
	return Config{EnvironmentURL: "https://fixture.live.dynatrace.com", PlatformURL: "https://fixture.apps.dynatrace.com", PlatformAuthType: AuthToken}
}
func fixtureClient(t *testing.T, f roundTripFunc) *Client {
	t.Helper()
	c, err := NewClientWithHTTPClient(fixtureConfig(), Credentials{EnvironmentToken: "environment-fixture", PlatformToken: "platform-fixture"}, &http.Client{Transport: f})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func TestConfigurationOriginsAndSecretNondisclosure(t *testing.T) {
	for _, url := range []string{"http://tenant.live.dynatrace.com", "https://evil.example", "https://tenant.live.dynatrace.com@evil.example", "https://tenant.live.dynatrace.com/path", "https://tenant.live.dynatrace.com?x=1", "https://tenant.live.dynatrace.com:8443", "https://tenant.live.dynatrace.com#fragment"} {
		t.Run(url, func(t *testing.T) {
			cfg := fixtureConfig()
			cfg.EnvironmentURL = url
			if cfg.Validate() == nil {
				t.Fatal("unsafe origin accepted")
			}
		})
	}
	raw, _ := json.Marshal(Credentials{EnvironmentToken: "secret-one", ClientSecret: "secret-two", VersionIdentity: "secret-three"})
	if string(raw) != "{}" {
		t.Fatal(string(raw))
	}
	cfg := fixtureConfig().Defaults()
	cfg.MaxScanBytes = 20_000_000_000
	cfg.MaxRunScanBytes = 200_000_000_000
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}
func TestDialValidatesAllAnswersAndPinsPublicIP(t *testing.T) {
	for _, answers := range [][]netip.Addr{{netip.MustParseAddr("127.0.0.1")}, {netip.MustParseAddr("169.254.169.254")}, {netip.MustParseAddr("168.63.129.16")}, {netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("10.1.2.3")}} {
		called := false
		_, err := dialPublic(t.Context(), "tcp", "fixture.live.dynatrace.com:443", func(context.Context, string, string) ([]netip.Addr, error) { return answers, nil }, func(context.Context, string, string) (net.Conn, error) { called = true; return nil, nil })
		if err == nil || called {
			t.Fatalf("private DNS answer dialed: %v", answers)
		}
	}
	_, err := dialPublic(t.Context(), "tcp", "fixture.live.dynatrace.com:443", func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
	}, func(_ context.Context, _ string, address string) (net.Conn, error) {
		if address != "8.8.8.8:443" {
			t.Fatal(address)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestHTTPClassificationsBoundsAndRedirectPolicy(t *testing.T) {
	for _, tc := range []struct {
		status     int
		body, kind string
	}{{401, "token=secret", "authentication_denied"}, {403, "denied", "permission_denied"}, {404, "missing", "unsupported"}, {429, "wait", "throttled"}, {400, "too many data points", "query_budget"}} {
		c := fixtureClient(t, func(r *http.Request) (*http.Response, error) {
			resp := response(tc.status, tc.body)
			resp.Header.Set("Retry-After", "2")
			return resp, nil
		})
		_, err := c.request(t.Context(), false, "GET", "/api/v2/events", nil, nil)
		var h *HTTPError
		if !errors.As(err, &h) || h.Kind != tc.kind || h.RetryAfter != 2*time.Second || strings.Contains(err.Error(), "token=secret") {
			t.Fatalf("classification %v", err)
		}
	}
	c := fixtureClient(t, func(*http.Request) (*http.Response, error) { return response(200, strings.Repeat("x", 1025)), nil })
	c.cfg.MaxResponseBytes = 1024
	if _, err := c.request(t.Context(), false, "GET", "/x", nil, nil); err == nil {
		t.Fatal("oversized response accepted")
	}
	if c.http.CheckRedirect(nil, nil) != http.ErrUseLastResponse {
		t.Fatal("redirect accepted")
	}
}
func TestOAuthConcurrentExpiryVersionAndCancellation(t *testing.T) {
	cfg := fixtureConfig()
	cfg.PlatformAuthType = AuthOAuth
	cfg.OAuthResource = "urn:dtaccount:fixture"
	cfg.OAuthScopes = []string{"storage:logs:read"}
	creds := Credentials{ClientID: "fixture-client", ClientSecret: "fixture-secret", VersionIdentity: "v1"}
	var calls atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Hostname() != "sso.dynatrace.com" {
			t.Fatal(r.URL)
		}
		calls.Add(1)
		time.Sleep(10 * time.Millisecond)
		return response(200, `{"access_token":"token-fixture","expires_in":120,"token_type":"Bearer"}`), nil
	})}
	c, err := NewClientWithHTTPClient(cfg, creds, client)
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for i := 0; i < 12; i++ {
		workers.Go(func() {
			v, e := c.platformToken(t.Context())
			if e != nil || v != "token-fixture" {
				t.Errorf("token %s %v", v, e)
			}
		})
	}
	workers.Wait()
	if calls.Load() != 1 {
		t.Fatal(calls.Load())
	}
	c.token.mu.Lock()
	c.token.expires = time.Now().Add(-time.Second)
	c.token.mu.Unlock()
	if _, err := c.platformToken(t.Context()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal(calls.Load())
	}
	if sharedToken(cfg, creds) == sharedToken(cfg, Credentials{ClientID: creds.ClientID, ClientSecret: creds.ClientSecret, VersionIdentity: "v2"}) {
		t.Fatal("secret version shared token state")
	}
	other := cfg
	other.PlatformURL = "https://other.apps.dynatrace.com"
	if sharedToken(cfg, creds) == sharedToken(other, creds) {
		t.Fatal("origin shared token state")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := c.platformToken(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

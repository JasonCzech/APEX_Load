package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestValidateOutboundURLAllowingHosts(t *testing.T) {
	allowed := map[string]struct{}{"ghes.internal.example": {}, "10.0.0.5": {}}

	cases := []struct {
		name    string
		raw     string
		allowed map[string]struct{}
		wantErr bool
	}{
		{"allowed private name", "https://ghes.internal.example/api/v3", allowed, false},
		{"allowed private ip", "https://10.0.0.5/api/v3", allowed, false},
		{"private name not allowed", "https://other.internal/api/v3", allowed, false}, // private *name* (non-IP, non-localhost) passes the syntactic check today
		{"private ip not allowed", "https://10.0.0.9/api/v3", allowed, true},
		{"localhost not allowed", "https://localhost/api/v3", allowed, true},
		{"public host with no allow-list", "https://api.github.com", nil, false},
		{"loopback ip blocked with no allow-list", "https://127.0.0.1", nil, true},
		{"bad scheme", "ftp://ghes.internal.example", allowed, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateOutboundURLAllowingHosts(tc.raw, tc.allowed)
			if tc.wantErr != (err != nil) {
				t.Fatalf("validateOutboundURLAllowingHosts(%q) err=%v, wantErr=%v", tc.raw, err, tc.wantErr)
			}
		})
	}
}

func TestHostAllowlistedHTTPClientPermitsConfiguredHostOnly(t *testing.T) {
	// httptest listens on loopback (127.0.0.1), which is normally SSRF-blocked.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	host := mustHost(t, server.URL)

	// Without the allow-list, the loopback target is blocked at dial time.
	blocked := newHostAllowlistedHTTPClient(nil, 2*time.Second)
	if blockedResp, err := blocked.Get(server.URL); err == nil {
		_ = blockedResp.Body.Close()
		t.Fatal("expected loopback connection to be blocked by the SSRF dialer")
	}

	// With the host explicitly allow-listed, the same target connects.
	allowed := newHostAllowlistedHTTPClient([]string{host}, 2*time.Second)
	resp, err := allowed.Get(server.URL)
	if err != nil {
		t.Fatalf("expected allow-listed host to connect, got: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	// A *different* loopback host must still be blocked even with an allow-list set.
	stillBlocked := newHostAllowlistedHTTPClient([]string{"some-other-host.example"}, 2*time.Second)
	if _, err := stillBlocked.Get(server.URL); err == nil {
		t.Fatal("expected non-allow-listed loopback target to remain blocked")
	}
}

func mustHost(t *testing.T, raw string) string {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return parsed.Hostname()
}

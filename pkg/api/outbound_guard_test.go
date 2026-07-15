package api

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIsBlockedOutboundIP(t *testing.T) {
	cases := []struct {
		ip      string
		blocked bool
	}{
		{"127.0.0.1", true},             // loopback
		{"::1", true},                   // loopback v6
		{"10.0.0.1", true},              // RFC1918
		{"172.16.5.4", true},            // RFC1918
		{"192.168.1.1", true},           // RFC1918
		{"169.254.169.254", true},       // link-local / cloud metadata
		{"fe80::1", true},               // link-local v6
		{"fc00::1", true},               // unique-local v6
		{"0.0.0.0", true},               // unspecified
		{"224.0.0.1", true},             // multicast
		{"8.8.8.8", false},              // public
		{"1.1.1.1", false},              // public
		{"2606:4700:4700::1111", false}, // public v6
	}
	for _, c := range cases {
		ip := net.ParseIP(c.ip)
		if ip == nil {
			t.Fatalf("ParseIP(%q) = nil", c.ip)
		}
		if got := isBlockedOutboundIP(ip); got != c.blocked {
			t.Errorf("isBlockedOutboundIP(%s) = %v, want %v", c.ip, got, c.blocked)
		}
	}
}

func TestValidateOutboundRemoteHost(t *testing.T) {
	// Default (guard on): literal private/loopback/link-local hosts are rejected;
	// DNS names and public literals pass (DNS is re-checked at dial time).
	reject := []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "::1", "192.168.0.5"}
	for _, h := range reject {
		if err := validateOutboundRemoteHost(h); err == nil {
			t.Errorf("validateOutboundRemoteHost(%q) = nil, want error", h)
		}
	}
	allow := []string{"example.com", "remote.internal.test", "8.8.8.8"}
	for _, h := range allow {
		if err := validateOutboundRemoteHost(h); err != nil {
			t.Errorf("validateOutboundRemoteHost(%q) = %v, want nil", h, err)
		}
	}

	// Opt-in disables the guard entirely.
	t.Setenv(remoteOutboundAllowPrivateHostsEnv, "1")
	for _, h := range reject {
		if err := validateOutboundRemoteHost(h); err != nil {
			t.Errorf("with allow-private, validateOutboundRemoteHost(%q) = %v, want nil", h, err)
		}
	}
}

func TestNormalizeLibraryRemoteBaseURLBlocksPrivateHosts(t *testing.T) {
	if _, err := normalizeLibraryRemoteBaseURL("https://token@library.example.com/library"); err == nil {
		t.Error("normalizeLibraryRemoteBaseURL accepted URL userinfo, want error")
	}
	if _, err := normalizeLibraryRemoteBaseURL("http://10.0.0.1:8080/library"); err == nil {
		t.Error("normalizeLibraryRemoteBaseURL accepted a private IP base URL, want error")
	}
	if _, err := normalizeLibraryRemoteBaseURL("http://127.0.0.1:9090"); err == nil {
		t.Error("normalizeLibraryRemoteBaseURL accepted a loopback base URL, want error")
	}
	if _, err := normalizeLibraryRemoteBaseURL("https://library.example.com/library"); err != nil {
		t.Errorf("normalizeLibraryRemoteBaseURL rejected a public host: %v", err)
	}

	t.Setenv(remoteOutboundAllowPrivateHostsEnv, "1")
	if _, err := normalizeLibraryRemoteBaseURL("http://10.0.0.1:8080/library"); err != nil {
		t.Errorf("with allow-private, normalizeLibraryRemoteBaseURL rejected internal host: %v", err)
	}
}

func TestSameOriginRedirectPolicyRejectsSensitiveRedirects(t *testing.T) {
	original := httptest.NewRequest(http.MethodPost, "https://api.example.test/start", nil)
	tests := []struct {
		name   string
		target string
		wantOK bool
	}{
		{name: "same origin", target: "https://api.example.test/next", wantOK: true},
		{name: "different host", target: "https://attacker.example/next"},
		{name: "different port", target: "https://api.example.test:8443/next"},
		{name: "https downgrade", target: "http://api.example.test/next"},
		{name: "userinfo", target: "https://token@api.example.test/next"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := httptest.NewRequest(http.MethodPost, tt.target, nil)
			err := sameOriginRedirectPolicy(target, []*http.Request{original})
			if tt.wantOK && err != nil {
				t.Fatalf("sameOriginRedirectPolicy() = %v, want nil", err)
			}
			if !tt.wantOK && err == nil {
				t.Fatal("sameOriginRedirectPolicy() = nil, want error")
			}
		})
	}
}

func TestGuardedHTTPClientBlocksLoopbackByDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Guard ON (default): the loopback dial is refused.
	t.Setenv(remoteOutboundAllowPrivateHostsEnv, "")
	blocked := newGuardedHTTPClient(2 * time.Second)
	if resp, err := blocked.Get(srv.URL); err == nil {
		resp.Body.Close()
		t.Fatal("guarded client dialed a loopback server, want SSRF block")
	} else if !strings.Contains(err.Error(), "blocked outbound") {
		t.Fatalf("error = %v, want a 'blocked outbound' SSRF error", err)
	}

	// Opt-in: the same dial now succeeds.
	t.Setenv(remoteOutboundAllowPrivateHostsEnv, "1")
	allowed := newGuardedHTTPClient(2 * time.Second)
	resp, err := allowed.Get(srv.URL)
	if err != nil {
		t.Fatalf("allow-private guarded client failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestGuardedHTTPClientReusesOnlyMatchingPolicy(t *testing.T) {
	t.Setenv(remoteOutboundAllowPrivateHostsEnv, "")
	blockedA := newGuardedHTTPClient(3 * time.Second)
	blockedB := newGuardedHTTPClient(3 * time.Second)
	if blockedA != blockedB {
		t.Fatal("matching guarded HTTP policy created duplicate connection pools")
	}

	t.Setenv(remoteOutboundAllowPrivateHostsEnv, "1")
	allowed := newGuardedHTTPClient(3 * time.Second)
	if allowed == blockedA {
		t.Fatal("allow-private and blocked policies shared one HTTP client")
	}
	if differentTimeout := newGuardedHTTPClient(4 * time.Second); differentTimeout == allowed {
		t.Fatal("different client timeouts shared one HTTP client")
	}
}

func TestGuardedHTTPClientBoundsRedirects(t *testing.T) {
	// Redirect-based SSRF is blocked at dial time (every hop re-checks the resolved
	// IP via the same DialContext proven in TestGuardedHTTPClientBlocksLoopbackByDefault).
	// Here we assert the independent defense-in-depth control: the redirect chain is
	// bounded rather than followed indefinitely.
	t.Setenv(remoteOutboundAllowPrivateHostsEnv, "1") // loopback allowed so we exercise redirects
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/again", http.StatusFound)
	}))
	defer srv.Close()

	client := newGuardedHTTPClient(2 * time.Second)
	resp, err := client.Get(srv.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("unbounded redirect chain was followed, want a redirect cap error")
	}
	if !strings.Contains(err.Error(), "stopped after") {
		t.Fatalf("error = %v, want redirect-cap error", err)
	}
}

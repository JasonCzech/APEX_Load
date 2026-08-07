package kubeops

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
)

// serverCAPEM renders a test server's certificate as a PEM bundle so a remote
// client can be given an inline trust anchor.
func serverCAPEM(t *testing.T, server *httptest.Server) string {
	t.Helper()
	cert := server.Certificate()
	if cert == nil {
		t.Fatal("test server has no certificate")
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))
}

func TestNewRemoteClientRejectsInvalidConfigurations(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{
			name:    "plaintext endpoint",
			cfg:     Config{Namespace: "apex", APIServerURL: "http://cluster.example", CAPEM: "x", TokenSource: StaticTokenSource{Value: "t"}},
			wantErr: "must use https",
		},
		{
			name:    "embedded credentials",
			cfg:     Config{Namespace: "apex", APIServerURL: "https://user:pw@cluster.example", CAPEM: "x", TokenSource: StaticTokenSource{Value: "t"}},
			wantErr: "must not embed credentials",
		},
		{
			name:    "path component",
			cfg:     Config{Namespace: "apex", APIServerURL: "https://cluster.example/api", CAPEM: "x", TokenSource: StaticTokenSource{Value: "t"}},
			wantErr: "must not include a path",
		},
		{
			name:    "no CA and no system roots",
			cfg:     Config{Namespace: "apex", APIServerURL: "https://cluster.example", TokenSource: StaticTokenSource{Value: "t"}},
			wantErr: "CA certificate is required",
		},
		{
			name:    "no token source",
			cfg:     Config{Namespace: "apex", APIServerURL: "https://cluster.example", CAPEM: "x"},
			wantErr: "token source is required",
		},
		{
			name:    "no namespace",
			cfg:     Config{APIServerURL: "https://cluster.example", CAPEM: "x", TokenSource: StaticTokenSource{Value: "t"}},
			wantErr: "namespace is required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewRemoteClient(tc.cfg)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("NewRemoteClient error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

// TestNewRemoteClientNeverFallsBackToInClusterEnvironment pins the property
// that makes a misconfigured registry entry safe: a remote client must not
// silently resolve to the controller's own cluster.
func TestNewRemoteClientNeverFallsBackToInClusterEnvironment(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")
	t.Setenv("KUBERNETES_SERVICE_PORT", "443")
	if _, err := NewRemoteClient(Config{Namespace: "apex", CAPEM: "x", TokenSource: StaticTokenSource{Value: "t"}}); err == nil {
		t.Fatal("NewRemoteClient with no API server URL succeeded; it must not fall back to the in-cluster environment")
	}
}

func TestRemoteClientUsesInlineCAAndStaticToken(t *testing.T) {
	var gotAuth string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
	}))
	defer server.Close()

	client, err := NewRemoteClient(Config{
		Namespace:    "apex",
		APIServerURL: server.URL,
		CAPEM:        serverCAPEM(t, server),
		TokenSource:  StaticTokenSource{Value: "remote-token"},
	})
	if err != nil {
		t.Fatalf("NewRemoteClient: %v", err)
	}
	if _, err := client.ListPods(context.Background(), "apex", ""); err != nil {
		t.Fatalf("ListPods: %v", err)
	}
	if gotAuth != "Bearer remote-token" {
		t.Fatalf("Authorization = %q, want the static token", gotAuth)
	}
}

// TestRemoteClientVerifiesCertificateChain proves verification is genuinely on:
// system_roots must reject the test server's self-signed certificate, which is
// not in any host trust store. (Handing the client a different httptest
// server's CA would not test anything — the net/http test servers all share one
// built-in certificate.)
func TestRemoteClientVerifiesCertificateChain(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
	}))
	defer server.Close()

	client, err := NewRemoteClient(Config{
		Namespace:    "apex",
		APIServerURL: server.URL,
		TLSMode:      TLSModeSystemRoots,
		TokenSource:  StaticTokenSource{Value: "remote-token"},
	})
	if err != nil {
		t.Fatalf("NewRemoteClient: %v", err)
	}
	if _, err := client.ListPods(context.Background(), "apex", ""); err == nil {
		t.Fatal("ListPods succeeded against an untrusted certificate; TLS verification must not be bypassed")
	}
}

func TestRemoteClientRejectsMalformedCABundle(t *testing.T) {
	client, err := NewRemoteClient(Config{
		Namespace:    "apex",
		APIServerURL: "https://cluster.example",
		CAPEM:        "not a certificate",
		TokenSource:  StaticTokenSource{Value: "t"},
	})
	if err != nil {
		t.Fatalf("NewRemoteClient: %v", err)
	}
	// Construction is lazy, matching the in-cluster client: the CA error must
	// surface on first use rather than at registration time.
	if _, err := client.ListPods(context.Background(), "apex", ""); err == nil ||
		!strings.Contains(err.Error(), "did not contain a PEM certificate") {
		t.Fatalf("ListPods error = %v, want a PEM parse failure", err)
	}
}

// recordingTokenSource counts Invalidate calls and hands out a fresh token
// after each one, standing in for a minted (Azure AD) credential.
type recordingTokenSource struct {
	mu            sync.Mutex
	tokens        []string
	index         int
	invalidations int
}

func (s *recordingTokenSource) Token(context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.index >= len(s.tokens) {
		return s.tokens[len(s.tokens)-1], nil
	}
	return s.tokens[s.index], nil
}

func (s *recordingTokenSource) Describe() string { return "recording" }

func (s *recordingTokenSource) Invalidate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invalidations++
	if s.index < len(s.tokens)-1 {
		s.index++
	}
}

func TestClientInvalidatesCachedTokenBeforeRetry(t *testing.T) {
	var seen []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		if len(seen) == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
	}))
	defer server.Close()

	source := &recordingTokenSource{tokens: []string{"stale", "fresh"}}
	client, err := NewRemoteClient(Config{
		Namespace:    "apex",
		APIServerURL: server.URL,
		CAPEM:        serverCAPEM(t, server),
		TokenSource:  source,
	})
	if err != nil {
		t.Fatalf("NewRemoteClient: %v", err)
	}
	if _, err := client.ListPods(context.Background(), "apex", ""); err != nil {
		t.Fatalf("ListPods: %v", err)
	}
	if source.invalidations != 1 {
		t.Fatalf("Invalidate called %d times, want exactly 1", source.invalidations)
	}
	if len(seen) != 2 || seen[0] != "Bearer stale" || seen[1] != "Bearer fresh" {
		t.Fatalf("Authorization headers = %v, want the retry to carry the refreshed token", seen)
	}
}

func TestClientDialGuardBlocksDeniedAddresses(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
	}))
	defer server.Close()

	client, err := NewRemoteClient(Config{
		Namespace:    "apex",
		APIServerURL: server.URL,
		CAPEM:        serverCAPEM(t, server),
		TokenSource:  StaticTokenSource{Value: "t"},
		DialGuard: func(netip.Addr) error {
			return errDeniedForTest
		},
	})
	if err != nil {
		t.Fatalf("NewRemoteClient: %v", err)
	}
	if _, err := client.ListPods(context.Background(), "apex", ""); err == nil {
		t.Fatal("ListPods succeeded despite a dial guard that denies every address")
	}
}

var errDeniedForTest = &KubernetesAPIError{Method: "TEST", Path: "/", StatusCode: 0, Body: "denied by policy"}

func TestAzureAuthorityMustBeAnAllowedLoginEndpoint(t *testing.T) {
	base := AzureServicePrincipalConfig{TenantID: "t", ClientID: "c", ClientSecret: "s"}

	attacker := base
	attacker.Authority = "https://login.microsoftonline.com.evil.example"
	if _, err := NewAzureServicePrincipalTokenSource(attacker); err == nil {
		t.Fatal("an unlisted authority host was accepted; the client secret could be exfiltrated")
	}

	sovereign := base
	sovereign.Authority = "https://login.microsoftonline.us"
	if _, err := NewAzureServicePrincipalTokenSource(sovereign); err != nil {
		t.Fatalf("sovereign-cloud authority rejected: %v", err)
	}

	if _, err := NewAzureServicePrincipalTokenSource(base); err != nil {
		t.Fatalf("default authority rejected: %v", err)
	}
}

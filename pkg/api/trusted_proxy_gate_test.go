package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

// A2 — when a trusted-proxy CIDR allowlist is configured, identity headers from a
// source outside the allowlist are rejected; a source inside it is honored.
func TestTrustedProxyGateCIDRAllowlist(t *testing.T) {
	store := auditEventStore(t)
	now := time.Now().UTC()
	admin := &storage.User{ID: uuid.New().String(), Subject: "gw-admin", Active: true, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveUser(admin); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}
	if err := store.SaveUserRoleBinding(&storage.UserRoleBinding{ID: uuid.New().String(), UserID: admin.ID, ScopeType: "global", Role: RoleSystemAdmin, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveUserRoleBinding: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	settings := server.runner.GetSystemSettings()
	settings.Auth.Mode = authModeTrustedHeaders
	settings.Auth.TrustedProxyCIDRs = []string{"10.0.0.0/8"}
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings: %v", err)
	}

	do := func(remoteAddr string) int {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/users", nil)
		req.RemoteAddr = remoteAddr
		req.Header.Set("X-Auth-Request-User", admin.Subject)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		return resp.Code
	}

	if code := do("203.0.113.7:5555"); code != http.StatusUnauthorized {
		t.Fatalf("untrusted source status = %d, want 401", code)
	}
	if code := do("10.1.2.3:5555"); code != http.StatusOK {
		t.Fatalf("trusted source status = %d, want 200", code)
	}
}

// A2 — a configured shared secret gates identity headers (constant-time match).
func TestTrustedProxyGateSharedSecret(t *testing.T) {
	t.Setenv("APEXLOAD_TEST_PROXY_SECRET", "s3cret-token")
	store := auditEventStore(t)
	now := time.Now().UTC()
	admin := &storage.User{ID: uuid.New().String(), Subject: "secret-admin", Active: true, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveUser(admin); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}
	if err := store.SaveUserRoleBinding(&storage.UserRoleBinding{ID: uuid.New().String(), UserID: admin.ID, ScopeType: "global", Role: RoleSystemAdmin, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveUserRoleBinding: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	settings := server.runner.GetSystemSettings()
	settings.Auth.Mode = authModeTrustedHeaders
	settings.Auth.TrustedProxySecretEnvVar = "APEXLOAD_TEST_PROXY_SECRET"
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings: %v", err)
	}

	do := func(secret string) int {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/users", nil)
		req.Header.Set("X-Auth-Request-User", admin.Subject)
		if secret != "" {
			req.Header.Set(trustedProxySecretHeader, secret)
		}
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		return resp.Code
	}

	if code := do(""); code != http.StatusUnauthorized {
		t.Fatalf("missing secret status = %d, want 401", code)
	}
	if code := do("wrong"); code != http.StatusUnauthorized {
		t.Fatalf("wrong secret status = %d, want 401", code)
	}
	if code := do("s3cret-token"); code != http.StatusOK {
		t.Fatalf("correct secret status = %d, want 200", code)
	}
}

// A2 — in production, trusted-headers with no gate configured fails closed.
func TestTrustedProxyGateProductionFailsClosed(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "production")
	settings := AuthSettings{Mode: authModeTrustedHeaders, UserHeader: "X-Auth-Request-User"}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/users", nil)
	req.Header.Set("X-Auth-Request-User", "someone")
	if err := enforceTrustedProxyGate(req, settings); err == nil {
		t.Fatalf("expected production trusted-headers without a gate to fail closed")
	}
	// With a gate configured, the production guard is satisfied (the request still
	// needs to match, but the unconfigured-in-production hard error is gone).
	settings.TrustedProxyCIDRs = []string{"10.0.0.0/8"}
	req.RemoteAddr = "10.0.0.5:1234"
	if err := enforceTrustedProxyGate(req, settings); err != nil {
		t.Fatalf("configured gate with trusted source should pass, got %v", err)
	}
}

func TestTrustedProxyGateProductionRejectsWeakSharedSecret(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "production")
	t.Setenv("APEXLOAD_TEST_PROXY_SECRET", "weak")
	settings := AuthSettings{
		Mode:                     authModeTrustedHeaders,
		UserHeader:               "X-Auth-Request-User",
		RolesHeader:              "X-APEXLoad-Roles",
		TrustedProxySecretEnvVar: "APEXLOAD_TEST_PROXY_SECRET",
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/users", nil)
	req.Header.Set(trustedProxySecretHeader, "weak")
	if trustedProxyRequestAllowed(req, settings) {
		t.Fatal("weak production trusted-proxy secret must not authorize forwarded identity headers")
	}
	if err := validateAuthSettings(settings); err == nil || !strings.Contains(err.Error(), "at least 32 bytes") {
		t.Fatalf("validateAuthSettings error = %v, want production minimum-length failure", err)
	}

	strong := strings.Repeat("s", productionSecretMinBytes)
	t.Setenv("APEXLOAD_TEST_PROXY_SECRET", strong)
	req.Header.Set(trustedProxySecretHeader, strong)
	if !trustedProxyRequestAllowed(req, settings) {
		t.Fatal("strong production trusted-proxy secret should authorize the trusted request")
	}
	if err := validateAuthSettings(settings); err != nil {
		t.Fatalf("validateAuthSettings rejected strong production secret: %v", err)
	}
}

func TestTrustedProxyGateRejectsMalformedCIDRConfiguration(t *testing.T) {
	settings := AuthSettings{
		Mode:              authModeTrustedHeaders,
		UserHeader:        "X-Auth-Request-User",
		RolesHeader:       "X-APEXLoad-Roles",
		TrustedProxyCIDRs: []string{"10.0.0.0/8", "not-a-network"},
	}
	if err := validateAuthSettings(settings); err == nil || !strings.Contains(err.Error(), "invalid IP address or CIDR") {
		t.Fatalf("validateAuthSettings error = %v, want invalid trusted-proxy CIDR failure", err)
	}

	normalized := normalizeAuthSettings(AuthSettings{TrustedProxyCIDRs: []string{" 10.0.0.1 ", "10.0.0.1", "", "2001:db8::/32"}})
	if got, want := strings.Join(normalized.TrustedProxyCIDRs, ","), "10.0.0.1,2001:db8::/32"; got != want {
		t.Fatalf("normalized trusted proxy networks = %q, want %q", got, want)
	}
}

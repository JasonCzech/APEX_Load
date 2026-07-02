package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

func auditEventStore(t *testing.T) *storage.SQLiteStore {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func countAuditEvents(t *testing.T, store *storage.SQLiteStore, action string) int {
	t.Helper()
	events, err := store.ListAuditEvents(storage.AuditEventFilter{Action: action, Limit: 100})
	if err != nil {
		t.Fatalf("ListAuditEvents(%s): %v", action, err)
	}
	return len(events)
}

// A4 — a middleware-level authentication failure (unonboarded subject) is audited.
func TestAuthDenialIsAudited(t *testing.T) {
	store := auditEventStore(t)
	// A non-empty user table so the empty-store bootstrap does not admit the stranger.
	now := time.Now().UTC()
	if err := store.SaveUser(&storage.User{ID: uuid.New().String(), Subject: "known", Active: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/users", nil)
	req.Header.Set("X-Auth-Request-User", "stranger")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("unonboarded status = %d, want 401 body=%s", resp.Code, resp.Body.String())
	}
	if got := countAuditEvents(t, store, "auth.access.denied"); got != 1 {
		t.Fatalf("auth.access.denied events = %d, want 1", got)
	}
}

func TestAuthDenialThrottleIgnoresPathAndSubjectRotation(t *testing.T) {
	store := auditEventStore(t)
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	for _, tc := range []struct {
		subject string
		path    string
	}{
		{subject: "stranger-1", path: "/api/v1/auth/users"},
		{subject: "stranger-2", path: "/api/v1/scripts"},
		{subject: "stranger-3", path: "/api/v1/tests"},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		req.RemoteAddr = "203.0.113.10:12345"
		req.Header.Set("X-Auth-Request-User", tc.subject)
		server.recordAuthDenial(req, authResolution{}, http.StatusUnauthorized, "unknown user subject")
	}

	if got := countAuditEvents(t, store, "auth.access.denied"); got != 1 {
		t.Fatalf("auth.access.denied events = %d, want 1 despite path/subject rotation", got)
	}
}

// A4 — trusted-header logins are audited once per dedup window (not per request).
func TestTrustedHeaderLoginAuditedAndDeduped(t *testing.T) {
	store := auditEventStore(t)
	admin := seedAdmin(t, store, "login-admin")

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/users", nil)
		req.Header.Set("X-Auth-Request-User", admin.Subject)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("admin GET status = %d, want 200 body=%s", resp.Code, resp.Body.String())
		}
	}
	if got := countAuditEvents(t, store, "auth.session.login"); got != 1 {
		t.Fatalf("auth.session.login events = %d, want 1 (deduped)", got)
	}
}

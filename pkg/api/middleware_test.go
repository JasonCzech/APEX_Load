package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/recorder"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestAccessRequirementCoversAuthGovernanceRoutes(t *testing.T) {
	systemAdminOnly := []string{RoleSystemAdmin}
	allGlobalReaders := []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}
	allGlobalWriters := []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}
	tests := []struct {
		name        string
		method      string
		path        string
		globalRoles []string
	}{
		{name: "list service accounts", method: http.MethodGet, path: "/api/v1/auth/service-accounts", globalRoles: systemAdminOnly},
		{name: "create service account", method: http.MethodPost, path: "/api/v1/auth/service-accounts", globalRoles: systemAdminOnly},
		{name: "update service account", method: http.MethodPatch, path: "/api/v1/auth/service-accounts/svc-1", globalRoles: systemAdminOnly},
		{name: "rotate service account", method: http.MethodPost, path: "/api/v1/auth/service-accounts/svc-1/rotate", globalRoles: systemAdminOnly},
		{name: "delete service account", method: http.MethodDelete, path: "/api/v1/auth/service-accounts/svc-1", globalRoles: systemAdminOnly},
		{name: "list audit events", method: http.MethodGet, path: "/api/v1/auth/audit-events", globalRoles: systemAdminOnly},
		{name: "export audit events", method: http.MethodGet, path: "/api/v1/auth/audit-events/export", globalRoles: systemAdminOnly},
		{name: "read system inventory", method: http.MethodGet, path: "/api/v1/system/inventory", globalRoles: allGlobalReaders},
		{name: "read system services", method: http.MethodGet, path: "/api/v1/system/services", globalRoles: allGlobalReaders},
		{name: "read usage accounting", method: http.MethodGet, path: "/api/v1/system/usage", globalRoles: allGlobalReaders},
		{name: "export usage accounting", method: http.MethodGet, path: "/api/v1/system/usage/export", globalRoles: allGlobalReaders},
		{name: "list usage snapshots", method: http.MethodGet, path: "/api/v1/system/usage/snapshots", globalRoles: allGlobalReaders},
		{name: "capture usage snapshot", method: http.MethodPost, path: "/api/v1/system/usage/snapshots", globalRoles: systemAdminOnly},
		{name: "read parity status", method: http.MethodGet, path: "/api/v1/system/parity-status", globalRoles: allGlobalReaders},
		{name: "read browser benchmark evidence", method: http.MethodGet, path: "/api/v1/system/browser-benchmarks", globalRoles: allGlobalReaders},
		{name: "plan archive retention", method: http.MethodPost, path: "/api/v1/archives/retention/plan", globalRoles: systemAdminOnly},
		{name: "apply archive retention", method: http.MethodPost, path: "/api/v1/archives/retention/apply", globalRoles: systemAdminOnly},
		{name: "read system settings", method: http.MethodGet, path: "/api/v1/system/settings", globalRoles: allGlobalReaders},
		{name: "update system settings", method: http.MethodPut, path: "/api/v1/system/settings", globalRoles: systemAdminOnly},
		{name: "read library capabilities", method: http.MethodGet, path: "/api/v1/library/capabilities", globalRoles: allGlobalReaders},
		{name: "read library catalog", method: http.MethodGet, path: "/api/v1/library/catalog", globalRoles: allGlobalReaders},
		{name: "export library bundle", method: http.MethodPost, path: "/api/v1/library/export", globalRoles: allGlobalReaders},
		{name: "import library bundle", method: http.MethodPost, path: "/api/v1/library/import", globalRoles: allGlobalWriters},
		{name: "list library remotes", method: http.MethodGet, path: "/api/v1/library/remotes", globalRoles: allGlobalReaders},
		{name: "create library remote", method: http.MethodPost, path: "/api/v1/library/remotes", globalRoles: systemAdminOnly},
		{name: "probe library remote", method: http.MethodPost, path: "/api/v1/library/remotes/remote-1/probe", globalRoles: systemAdminOnly},
		{name: "read remote library catalog", method: http.MethodGet, path: "/api/v1/library/remotes/remote-1/catalog", globalRoles: allGlobalReaders},
		{name: "diff remote library item", method: http.MethodPost, path: "/api/v1/library/remotes/remote-1/diff", globalRoles: allGlobalReaders},
		{name: "pull remote library item", method: http.MethodPost, path: "/api/v1/library/remotes/remote-1/pull", globalRoles: allGlobalWriters},
		{name: "push remote library item", method: http.MethodPost, path: "/api/v1/library/remotes/remote-1/push", globalRoles: allGlobalWriters},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			requirement, ok := accessRequirementForRequest(req)
			if !ok {
				t.Fatalf("accessRequirementForRequest(%s %s) returned no requirement", tt.method, tt.path)
			}
			if !reflect.DeepEqual(requirement.GlobalRoles, tt.globalRoles) {
				t.Fatalf("global roles = %#v, want %#v", requirement.GlobalRoles, tt.globalRoles)
			}
		})
	}
}

func TestSecurityHeadersAllowLoopbackRecordingBridgeConnections(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	resp := httptest.NewRecorder()
	securityHeadersMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(resp, req)

	csp := resp.Header().Get("Content-Security-Policy")
	for _, want := range []string{
		"connect-src",
		"http://127.0.0.1:*",
		"http://localhost:*",
		"ws://127.0.0.1:*",
		"ws://localhost:*",
	} {
		if !strings.Contains(csp, want) {
			t.Fatalf("Content-Security-Policy = %q, want %q for local recording bridge access", csp, want)
		}
	}
}

func TestTrustedHeaderAuthRequiresIdentityForSystemSettings(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/settings", nil)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusUnauthorized, resp.Body.String())
	}
}

func TestTrustedHeaderAuthAllowsServiceAccountAPIKeyForSystemSettings(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	key := "apexload-service-account-key"
	sum := sha256.Sum256([]byte(key))
	now := time.Now()
	if err := store.SaveServiceAccount(&storage.ServiceAccount{
		ID:          "svc-settings",
		Name:        "Settings Automation",
		Description: "Applies controller settings",
		KeyHash:     fmt.Sprintf("%x", sum[:]),
		Active:      true,
		GlobalRoles: []string{RoleSystemAdmin},
		Scopes:      []string{"automation:*"},
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("SaveServiceAccount returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/settings", nil)
	req.Header.Set("X-APEXLoad-API-Key", key)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("service account GET status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
}

func TestTrustedHeaderAuthRateLimitsServiceAccountAPIKeys(t *testing.T) {
	t.Setenv("APEXLOAD_API_KEY_RATE_LIMIT_PER_MINUTE", "1")
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	key := "apexload-rate-limited-service-account-key"
	sum := sha256.Sum256([]byte(key))
	now := time.Now()
	if err := store.SaveServiceAccount(&storage.ServiceAccount{
		ID:          "svc-rate-limited",
		Name:        "Rate Limited Automation",
		KeyHash:     fmt.Sprintf("%x", sum[:]),
		Active:      true,
		GlobalRoles: []string{RoleSystemAdmin},
		Scopes:      []string{"automation:*"},
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("SaveServiceAccount returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	first := httptest.NewRequest(http.MethodGet, "/api/v1/system/settings", nil)
	first.Header.Set("X-APEXLoad-API-Key", key)
	firstResp := httptest.NewRecorder()
	server.mux.ServeHTTP(firstResp, first)
	if firstResp.Code != http.StatusOK {
		t.Fatalf("first service account GET status = %d, want %d body=%s", firstResp.Code, http.StatusOK, firstResp.Body.String())
	}
	if firstResp.Header().Get("X-RateLimit-Remaining") != "0" {
		t.Fatalf("remaining header = %q, want 0", firstResp.Header().Get("X-RateLimit-Remaining"))
	}

	second := httptest.NewRequest(http.MethodGet, "/api/v1/system/settings", nil)
	second.Header.Set("X-APEXLoad-API-Key", key)
	secondResp := httptest.NewRecorder()
	server.mux.ServeHTTP(secondResp, second)
	if secondResp.Code != http.StatusTooManyRequests {
		t.Fatalf("second service account GET status = %d, want %d body=%s", secondResp.Code, http.StatusTooManyRequests, secondResp.Body.String())
	}
}

func TestTrustedHeaderAuthReservesIdempotencyKeysForMutations(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	first := httptest.NewRequest(http.MethodPost, "/api/v1/system/usage/snapshots", nil)
	first.Header.Set("X-Auth-Request-User", "alice")
	first.Header.Set("X-APEXLoad-Roles", RoleSystemAdmin)
	first.Header.Set("Idempotency-Key", "capture-usage-now")
	firstResp := httptest.NewRecorder()
	server.mux.ServeHTTP(firstResp, first)
	if firstResp.Code != http.StatusCreated {
		t.Fatalf("first snapshot status = %d, want %d body=%s", firstResp.Code, http.StatusCreated, firstResp.Body.String())
	}

	second := httptest.NewRequest(http.MethodPost, "/api/v1/system/usage/snapshots", nil)
	second.Header.Set("X-Auth-Request-User", "alice")
	second.Header.Set("X-APEXLoad-Roles", RoleSystemAdmin)
	second.Header.Set("Idempotency-Key", "capture-usage-now")
	secondResp := httptest.NewRecorder()
	server.mux.ServeHTTP(secondResp, second)
	if secondResp.Code != http.StatusConflict {
		t.Fatalf("duplicate snapshot status = %d, want %d body=%s", secondResp.Code, http.StatusConflict, secondResp.Body.String())
	}
	if !strings.Contains(secondResp.Body.String(), "duplicate idempotency key") {
		t.Fatalf("duplicate body = %s, want idempotency message", secondResp.Body.String())
	}
}

func TestFailedMutationReleasesOnlyItsOwnedIdempotencyReservation(t *testing.T) {
	cache := newMemoryCacheBackend(cacheRuntimeConfig{KeyPrefix: "idempotency-test"})
	request := func() *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/system/usage/snapshots", nil)
		req.Header.Set("Idempotency-Key", "retry-after-validation")
		return req
	}
	resolution := authResolution{User: &AuthenticatedUser{Subject: "alice"}}

	firstResponse := httptest.NewRecorder()
	reservation, ok := enforceIdempotencyKey(firstResponse, request(), cache, resolution)
	if !ok || reservation == nil {
		t.Fatalf("first reservation failed: status=%d body=%s", firstResponse.Code, firstResponse.Body.String())
	}
	serveReservedMutation(firstResponse, request(), cache, reservation, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusBadRequest, "invalid request")
	}))

	retryResponse := httptest.NewRecorder()
	retry, ok := enforceIdempotencyKey(retryResponse, request(), cache, resolution)
	if !ok || retry == nil {
		t.Fatalf("failed mutation retained reservation: status=%d body=%s", retryResponse.Code, retryResponse.Body.String())
	}
	if released, err := cache.ReleaseIdempotencyKey(context.Background(), retry.key, "different-owner"); err != nil || released {
		t.Fatalf("different owner released retry reservation: released=%v err=%v", released, err)
	}
}

func TestCanceledClientFailureReleasesIdempotencyReservation(t *testing.T) {
	cache := newMemoryCacheBackend(cacheRuntimeConfig{KeyPrefix: "idempotency-canceled"})
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodPost, "/api/v1/system/usage/snapshots", nil).WithContext(ctx)
	request.Header.Set("Idempotency-Key", "retry-after-cancel")
	resolution := authResolution{User: &AuthenticatedUser{Subject: "alice"}}

	response := httptest.NewRecorder()
	reservation, ok := enforceIdempotencyKey(response, request, cache, resolution)
	if !ok || reservation == nil {
		t.Fatalf("reservation failed: status=%d body=%s", response.Code, response.Body.String())
	}
	serveReservedMutation(response, request, cache, reservation, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		cancel()
		writeError(w, http.StatusBadRequest, "invalid request")
	}))

	retryRequest := httptest.NewRequest(http.MethodPost, request.URL.Path, nil)
	retryRequest.Header.Set("Idempotency-Key", "retry-after-cancel")
	retryResponse := httptest.NewRecorder()
	retry, ok := enforceIdempotencyKey(retryResponse, retryRequest, cache, resolution)
	if !ok || retry == nil {
		t.Fatalf("canceled client failure retained reservation: status=%d body=%s", retryResponse.Code, retryResponse.Body.String())
	}
}

func TestServerFailureReleasesIdempotencyReservation(t *testing.T) {
	cache := newMemoryCacheBackend(cacheRuntimeConfig{KeyPrefix: "idempotency-server-failure"})
	request := func() *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/system/usage/snapshots", nil)
		req.Header.Set("Idempotency-Key", "ambiguous-server-failure")
		return req
	}
	resolution := authResolution{User: &AuthenticatedUser{Subject: "alice"}}

	response := httptest.NewRecorder()
	reservation, ok := enforceIdempotencyKey(response, request(), cache, resolution)
	if !ok || reservation == nil {
		t.Fatalf("reservation failed: status=%d body=%s", response.Code, response.Body.String())
	}
	serveReservedMutation(response, request(), cache, reservation, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusInternalServerError, "commit result unknown")
	}))

	retryResponse := httptest.NewRecorder()
	if retry, ok := enforceIdempotencyKey(retryResponse, request(), cache, resolution); !ok || retry == nil {
		t.Fatalf("5xx reservation was retained: retry=%#v status=%d body=%s", retry, retryResponse.Code, retryResponse.Body.String())
	}
}

func TestTrustedHeaderAuthRejectsServiceAccountWithoutRouteScope(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	key := "apexload-limited-service-account-key"
	sum := sha256.Sum256([]byte(key))
	now := time.Now()
	if err := store.SaveServiceAccount(&storage.ServiceAccount{
		ID:          "svc-limited",
		Name:        "Limited Automation",
		KeyHash:     fmt.Sprintf("%x", sum[:]),
		Active:      true,
		GlobalRoles: []string{RoleSystemAdmin},
		Scopes:      []string{"results:read"},
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("SaveServiceAccount returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/settings", nil)
	req.Header.Set("X-APEXLoad-API-Key", key)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusForbidden {
		t.Fatalf("limited service account status = %d, want %d body=%s", resp.Code, http.StatusForbidden, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "token scope") {
		t.Fatalf("limited service account body = %s, want token scope error", resp.Body.String())
	}
}

func TestServiceAccountLibraryScopesAreKindScoped(t *testing.T) {
	importBody := func(kind string) map[string]any {
		return map[string]any{
			"bundle": map[string]any{
				"schema_version": libraryBundleSchemaVersion,
				"items": []map[string]any{{
					"kind": kind,
				}},
			},
		}
	}
	transferBody := func(kind string) map[string]any {
		return map[string]any{"kind": kind, "id": "asset-1"}
	}
	tests := []struct {
		name    string
		scopes  []string
		method  string
		path    string
		body    any
		allowed bool
	}{
		{name: "read scope can catalog", scopes: []string{"results:read"}, method: http.MethodGet, path: "/api/v1/library/catalog", allowed: true},
		{name: "read scope can export", scopes: []string{"results:read"}, method: http.MethodPost, path: "/api/v1/library/export", body: transferBody("script"), allowed: true},
		{name: "read scope can diff", scopes: []string{"results:read"}, method: http.MethodPost, path: "/api/v1/library/remotes/remote-1/diff", body: transferBody("script"), allowed: true},
		{name: "read scope cannot import script", scopes: []string{"results:read"}, method: http.MethodPost, path: "/api/v1/library/import", body: importBody("script"), allowed: false},
		{name: "script scope imports scripts", scopes: []string{"scripts:write"}, method: http.MethodPost, path: "/api/v1/library/import", body: importBody("script"), allowed: true},
		{name: "script scope pulls scripts", scopes: []string{"scripts:write"}, method: http.MethodPost, path: "/api/v1/library/remotes/remote-1/pull", body: transferBody("script"), allowed: true},
		{name: "script scope pushes scripts", scopes: []string{"scripts:write"}, method: http.MethodPost, path: "/api/v1/library/remotes/remote-1/push", body: transferBody("script"), allowed: true},
		{name: "script scope cannot import scenarios", scopes: []string{"scripts:write"}, method: http.MethodPost, path: "/api/v1/library/import", body: importBody("scenario"), allowed: false},
		{name: "script scope cannot push results", scopes: []string{"scripts:write"}, method: http.MethodPost, path: "/api/v1/library/remotes/remote-1/push", body: transferBody("result"), allowed: false},
		{name: "scenario scope imports scenarios", scopes: []string{"tests:run"}, method: http.MethodPost, path: "/api/v1/library/import", body: importBody("scenario"), allowed: true},
		{name: "scenario scope pulls scenarios", scopes: []string{"tests:run"}, method: http.MethodPost, path: "/api/v1/library/remotes/remote-1/pull", body: transferBody("scenario"), allowed: true},
		{name: "scenario scope cannot import scripts", scopes: []string{"tests:run"}, method: http.MethodPost, path: "/api/v1/library/import", body: importBody("script"), allowed: false},
		{name: "result scope imports result metadata", scopes: []string{"results:write"}, method: http.MethodPost, path: "/api/v1/library/import", body: importBody("result"), allowed: true},
		{name: "result scope imports streamed archives", scopes: []string{"results:write"}, method: http.MethodPost, path: "/api/v1/library/import/archive", allowed: true},
		{name: "result scope pulls results", scopes: []string{"results:write"}, method: http.MethodPost, path: "/api/v1/library/remotes/remote-1/pull", body: transferBody("result"), allowed: true},
		{name: "result scope cannot read catalog", scopes: []string{"results:write"}, method: http.MethodGet, path: "/api/v1/library/catalog", allowed: false},
		{name: "settings scope creates remotes", scopes: []string{"settings:manage"}, method: http.MethodPost, path: "/api/v1/library/remotes", allowed: true},
		{name: "settings scope updates remotes", scopes: []string{"settings:manage"}, method: http.MethodPut, path: "/api/v1/library/remotes/remote-1", allowed: true},
		{name: "settings scope deletes remotes", scopes: []string{"settings:manage"}, method: http.MethodDelete, path: "/api/v1/library/remotes/remote-1", allowed: true},
		{name: "settings scope probes remotes", scopes: []string{"settings:manage"}, method: http.MethodPost, path: "/api/v1/library/remotes/remote-1/probe", allowed: true},
		{name: "script scope cannot create remotes", scopes: []string{"scripts:write"}, method: http.MethodPost, path: "/api/v1/library/remotes", allowed: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := scopedLibraryRequest(t, tt.method, tt.path, tt.body)
			account := &AuthenticatedServiceAccount{ID: "svc-library", Name: "Library Automation", Scopes: tt.scopes}
			if got := serviceAccountAllowsRequest(account, req); got != tt.allowed {
				t.Fatalf("serviceAccountAllowsRequest = %v, want %v", got, tt.allowed)
			}
			if tt.body != nil {
				var decoded map[string]any
				if err := json.NewDecoder(req.Body).Decode(&decoded); err != nil {
					t.Fatalf("request body was not reusable after scope resolution: %v", err)
				}
			}
		})
	}
}

func TestTrustedHeaderAuthRejectsUnknownServiceAccountAPIKey(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/settings", nil)
	req.Header.Set("X-APEXLoad-API-Key", "missing-key")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("unknown service account GET status = %d, want %d body=%s", resp.Code, http.StatusUnauthorized, resp.Body.String())
	}
}

func TestTrustedHeaderAuthReportsMatchedDisabledOrExpiredServiceAccountAPIKey(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	expiredAt := now.Add(-time.Minute)
	accounts := []struct {
		name     string
		key      string
		account  *storage.ServiceAccount
		wantBody string
	}{
		{
			name: "disabled",
			key:  "disabled-key",
			account: &storage.ServiceAccount{
				ID:          "svc-disabled",
				Name:        "Disabled automation",
				KeyHash:     hashAPIKey("disabled-key"),
				Active:      false,
				GlobalRoles: []string{RoleViewer},
				Scopes:      []string{"system:read"},
				CreatedAt:   now,
				UpdatedAt:   now,
			},
			wantBody: "service account is disabled",
		},
		{
			name: "expired",
			key:  "expired-key",
			account: &storage.ServiceAccount{
				ID:          "svc-expired",
				Name:        "Expired automation",
				KeyHash:     hashAPIKey("expired-key"),
				Active:      true,
				GlobalRoles: []string{RoleViewer},
				Scopes:      []string{"system:read"},
				CreatedAt:   now,
				UpdatedAt:   now,
				ExpiresAt:   &expiredAt,
			},
			wantBody: "service account key is expired",
		},
	}
	for _, tc := range accounts {
		if err := store.SaveServiceAccount(tc.account); err != nil {
			t.Fatalf("SaveServiceAccount %s returned error: %v", tc.name, err)
		}
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	for _, tc := range accounts {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/system/settings", nil)
			req.Header.Set("X-APEXLoad-API-Key", tc.key)
			resp := httptest.NewRecorder()

			server.mux.ServeHTTP(resp, req)

			if resp.Code != http.StatusUnauthorized {
				t.Fatalf("%s service account status = %d, want %d body=%s", tc.name, resp.Code, http.StatusUnauthorized, resp.Body.String())
			}
			if !strings.Contains(resp.Body.String(), tc.wantBody) {
				t.Fatalf("%s service account body = %q, want %q", tc.name, resp.Body.String(), tc.wantBody)
			}
		})
	}
}

func TestTrustedHeaderHumanUsersRequireActiveLocalRecordAndLocalRoles(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	if err := store.SaveUser(&storage.User{
		ID:        "seed-user",
		Subject:   "seed",
		Active:    true,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveUser seed returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	missingReq := httptest.NewRequest(http.MethodGet, "/api/v1/system/inventory", nil)
	missingReq.Header.Set("X-Auth-Request-User", "alice")
	missingReq.Header.Set("X-APEXLoad-Roles", RoleSystemAdmin)
	missingResp := httptest.NewRecorder()
	server.mux.ServeHTTP(missingResp, missingReq)
	if missingResp.Code != http.StatusUnauthorized {
		t.Fatalf("missing local user status = %d, want %d body=%s", missingResp.Code, http.StatusUnauthorized, missingResp.Body.String())
	}

	if err := store.SaveUser(&storage.User{
		ID:        "user-alice",
		Subject:   "alice",
		Active:    true,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveUser alice returned error: %v", err)
	}
	noLocalRoleReq := httptest.NewRequest(http.MethodGet, "/api/v1/system/inventory", nil)
	noLocalRoleReq.Header.Set("X-Auth-Request-User", "alice")
	noLocalRoleReq.Header.Set("X-APEXLoad-Roles", RoleSystemAdmin)
	noLocalRoleResp := httptest.NewRecorder()
	server.mux.ServeHTTP(noLocalRoleResp, noLocalRoleReq)
	if noLocalRoleResp.Code != http.StatusForbidden {
		t.Fatalf("no local roles status = %d, want %d body=%s", noLocalRoleResp.Code, http.StatusForbidden, noLocalRoleResp.Body.String())
	}

	if err := store.SaveUserRoleBinding(&storage.UserRoleBinding{
		ID:        "alice-viewer",
		UserID:    "user-alice",
		ScopeType: "global",
		Role:      RoleViewer,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveUserRoleBinding returned error: %v", err)
	}
	viewerReq := httptest.NewRequest(http.MethodGet, "/api/v1/system/inventory", nil)
	viewerReq.Header.Set("X-Auth-Request-User", "alice")
	viewerResp := httptest.NewRecorder()
	server.mux.ServeHTTP(viewerResp, viewerReq)
	if viewerResp.Code != http.StatusOK {
		t.Fatalf("local viewer status = %d, want %d body=%s", viewerResp.Code, http.StatusOK, viewerResp.Body.String())
	}

	if err := store.UpdateUserActive("user-alice", false, now.Add(time.Minute)); err != nil {
		t.Fatalf("UpdateUserActive returned error: %v", err)
	}
	disabledReq := httptest.NewRequest(http.MethodGet, "/api/v1/system/inventory", nil)
	disabledReq.Header.Set("X-Auth-Request-User", "alice")
	disabledResp := httptest.NewRecorder()
	server.mux.ServeHTTP(disabledResp, disabledReq)
	if disabledResp.Code != http.StatusUnauthorized {
		t.Fatalf("disabled local user status = %d, want %d body=%s", disabledResp.Code, http.StatusUnauthorized, disabledResp.Body.String())
	}
}

func TestTrustedHeaderAuthSystemSettingsRequireSystemAdmin(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	settings := enableTrustedHeaderAuth(t, server)
	body, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/api/v1/system/settings", bytes.NewReader(body))
	req.Header.Set("X-Auth-Request-User", "alice")
	req.Header.Set("X-APEXLoad-Roles", RoleViewer)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusForbidden {
		t.Fatalf("viewer PUT status = %d, want %d body=%s", resp.Code, http.StatusForbidden, resp.Body.String())
	}

	req2 := httptest.NewRequest(http.MethodPut, "/api/v1/system/settings", bytes.NewReader(body))
	withCurrentSystemSettingsIfMatch(req2, server)
	req2.Header.Set("X-Auth-Request-User", "root")
	req2.Header.Set("X-APEXLoad-Roles", RoleSystemAdmin)
	resp2 := httptest.NewRecorder()

	server.mux.ServeHTTP(resp2, req2)

	if resp2.Code != http.StatusOK {
		t.Fatalf("system admin PUT status = %d, want %d body=%s", resp2.Code, http.StatusOK, resp2.Body.String())
	}
}

func TestDevUserPreviewEnforcesSystemSettingsRBAC(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	settings := server.runner.GetSystemSettings()
	body, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/system/settings", nil)
	getReq.Header.Set(devViewHeader, devViewModeUser)
	getResp := httptest.NewRecorder()
	server.mux.ServeHTTP(getResp, getReq)
	if getResp.Code != http.StatusOK {
		t.Fatalf("dev user preview GET status = %d, want %d body=%s", getResp.Code, http.StatusOK, getResp.Body.String())
	}

	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/system/settings", bytes.NewReader(body))
	putReq.Header.Set(devViewHeader, devViewModeUser)
	putResp := httptest.NewRecorder()
	server.mux.ServeHTTP(putResp, putReq)
	if putResp.Code != http.StatusForbidden {
		t.Fatalf("dev user preview PUT status = %d, want %d body=%s", putResp.Code, http.StatusForbidden, putResp.Body.String())
	}

	adminReq := httptest.NewRequest(http.MethodPut, "/api/v1/system/settings", bytes.NewReader(body))
	withCurrentSystemSettingsIfMatch(adminReq, server)
	adminReq.Header.Set(devViewHeader, devViewModeAdmin)
	adminResp := httptest.NewRecorder()
	server.mux.ServeHTTP(adminResp, adminReq)
	if adminResp.Code != http.StatusOK {
		t.Fatalf("dev admin preview PUT status = %d, want %d body=%s", adminResp.Code, http.StatusOK, adminResp.Body.String())
	}
}

func TestDevUserPreviewAllowsEditorWorkflowsButDeniesSystemAdminRoutes(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	project := &storage.Project{ID: uuid.New().String(), Name: "Preview Project", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})

	userReq := httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(`{"name":"Preview Project"}`))
	userReq.Header.Set(devViewHeader, devViewModeUser)
	userResp := httptest.NewRecorder()
	server.mux.ServeHTTP(userResp, userReq)
	if userResp.Code != http.StatusCreated {
		t.Fatalf("dev user preview project create status = %d, want %d body=%s", userResp.Code, http.StatusCreated, userResp.Body.String())
	}

	adminReq := httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(`{"name":"Preview Project"}`))
	adminReq.Header.Set(devViewHeader, devViewModeAdmin)
	adminResp := httptest.NewRecorder()
	server.mux.ServeHTTP(adminResp, adminReq)
	if adminResp.Code != http.StatusCreated {
		t.Fatalf("dev admin preview project create status = %d, want %d body=%s", adminResp.Code, http.StatusCreated, adminResp.Body.String())
	}

	scriptBody := []byte(`{"project_id":"` + project.ID + `","script":{"name":"Preview Script","version":"1.0.0","protocol":"http","config":{"base_url":"https://example.com"},"actions":[{"name":"Open","type":"http","method":"GET","target":"/"}]}}`)
	scriptReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts", bytes.NewReader(scriptBody))
	scriptReq.Header.Set("Content-Type", "application/json")
	scriptReq.Header.Set(devViewHeader, devViewModeUser)
	scriptReq.Header.Set(devProjectIDHeader, project.ID)
	scriptResp := httptest.NewRecorder()
	server.mux.ServeHTTP(scriptResp, scriptReq)
	if scriptResp.Code != http.StatusCreated {
		t.Fatalf("dev user preview script create status = %d, want %d body=%s", scriptResp.Code, http.StatusCreated, scriptResp.Body.String())
	}
}

func TestDevPreviewHeadersIgnoredWhenTrustedHeaderAuthEnabled(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/settings", nil)
	req.Header.Set(devViewHeader, devViewModeUser)
	req.Header.Set(devProjectIDHeader, "project-a")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("trusted-header auth with dev preview status = %d, want %d body=%s", resp.Code, http.StatusUnauthorized, resp.Body.String())
	}
}

func TestTrustedHeaderAuthRequiresIdentityForSystemInventory(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/inventory", nil)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusUnauthorized, resp.Body.String())
	}
}

func TestTrustedHeaderAuthAllowsViewerToReadSystemInventory(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/inventory", nil)
	req.Header.Set("X-Auth-Request-User", "viewer-user")
	req.Header.Set("X-APEXLoad-Roles", RoleViewer)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("viewer GET status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
}

func TestTrustedHeaderAuthProjectRoleGuardsProjectEndpoints(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	project := &storage.Project{ID: uuid.New().String(), Name: "Project One", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+project.ID, nil)
	getReq.Header.Set("X-Auth-Request-User", "viewer-user")
	getReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleViewer)
	getResp := httptest.NewRecorder()

	server.mux.ServeHTTP(getResp, getReq)

	if getResp.Code != http.StatusOK {
		t.Fatalf("project viewer GET status = %d, want %d body=%s", getResp.Code, http.StatusOK, getResp.Body.String())
	}

	updateBody := []byte(`{"name":"Renamed"}`)
	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/projects/"+project.ID, bytes.NewReader(updateBody))
	putReq.Header.Set("X-Auth-Request-User", "viewer-user")
	putReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleViewer)
	putResp := httptest.NewRecorder()

	server.mux.ServeHTTP(putResp, putReq)

	if putResp.Code != http.StatusForbidden {
		t.Fatalf("project viewer PUT status = %d, want %d body=%s", putResp.Code, http.StatusForbidden, putResp.Body.String())
	}

	adminReq := httptest.NewRequest(http.MethodPut, "/api/v1/projects/"+project.ID, bytes.NewReader(updateBody))
	adminReq.Header.Set("X-Auth-Request-User", "project-admin")
	adminReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleProjectAdmin)
	adminResp := httptest.NewRecorder()

	server.mux.ServeHTTP(adminResp, adminReq)

	if adminResp.Code != http.StatusOK {
		t.Fatalf("project admin PUT status = %d, want %d body=%s", adminResp.Code, http.StatusOK, adminResp.Body.String())
	}
	if got, err := store.GetProject(project.ID); err != nil || got.Name != "Renamed" {
		t.Fatalf("project rename failed, got=%v err=%v", got, err)
	}
}

func TestTrustedHeaderRepositoryRoleAllowsRepositoryProjectAndReleaseCreation(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	createProject := httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(`{"name":"Repository Managed Project"}`))
	createProject.Header.Set("X-Auth-Request-User", "repo-manager")
	createProject.Header.Set("X-APEXLoad-Application-Repository-Roles", storage.DefaultApplicationRepositoryID+"="+RoleEditor)
	projectResp := httptest.NewRecorder()
	server.mux.ServeHTTP(projectResp, createProject)
	if projectResp.Code != http.StatusCreated {
		t.Fatalf("repository role project create status = %d, want %d body=%s", projectResp.Code, http.StatusCreated, projectResp.Body.String())
	}
	var project storage.Project
	if err := json.Unmarshal(projectResp.Body.Bytes(), &project); err != nil {
		t.Fatalf("unmarshal project: %v", err)
	}

	createRelease := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+project.ID+"/releases", strings.NewReader(`{"name":"2026.05"}`))
	createRelease.Header.Set("X-Auth-Request-User", "repo-manager")
	createRelease.Header.Set("X-APEXLoad-Application-Repository-Roles", storage.DefaultApplicationRepositoryID+"="+RoleEditor)
	releaseResp := httptest.NewRecorder()
	server.mux.ServeHTTP(releaseResp, createRelease)
	if releaseResp.Code != http.StatusCreated {
		t.Fatalf("repository role release create status = %d, want %d body=%s", releaseResp.Code, http.StatusCreated, releaseResp.Body.String())
	}
}

func TestTrustedHeaderAuthProjectRoleGuardsTestStreamEndpoint(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	project := &storage.Project{ID: uuid.New().String(), Name: "Checkout", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	testRecord := &storage.TestRecord{ID: uuid.New().String(), ProjectID: project.ID, Name: "Checkout Smoke", Status: "RUNNING", StartTime: time.Now()}
	if err := store.SaveTest(testRecord); err != nil {
		t.Fatalf("SaveTest returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	unauthorizedReq := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testRecord.ID+"/stream", nil)
	unauthorizedResp := httptest.NewRecorder()
	server.mux.ServeHTTP(unauthorizedResp, unauthorizedReq)
	if unauthorizedResp.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized test stream status = %d, want %d body=%s", unauthorizedResp.Code, http.StatusUnauthorized, unauthorizedResp.Body.String())
	}

	forbiddenReq := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testRecord.ID+"/stream", nil)
	forbiddenReq.Header.Set("X-Auth-Request-User", "viewer-user")
	forbiddenReq.Header.Set("X-APEXLoad-Project-Roles", uuid.New().String()+"="+RoleViewer)
	forbiddenResp := httptest.NewRecorder()
	server.mux.ServeHTTP(forbiddenResp, forbiddenReq)
	if forbiddenResp.Code != http.StatusForbidden {
		t.Fatalf("foreign project test stream status = %d, want %d body=%s", forbiddenResp.Code, http.StatusForbidden, forbiddenResp.Body.String())
	}

	viewerReq := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testRecord.ID+"/stream", nil)
	viewerReq.Header.Set("X-Auth-Request-User", "viewer-user")
	viewerReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleViewer)
	viewerResp := httptest.NewRecorder()
	server.mux.ServeHTTP(viewerResp, viewerReq)
	if viewerResp.Code == http.StatusUnauthorized || viewerResp.Code == http.StatusForbidden {
		t.Fatalf("project viewer test stream status = %d, want websocket upgrade attempt body=%s", viewerResp.Code, viewerResp.Body.String())
	}
	if viewerResp.Code != http.StatusBadRequest {
		t.Fatalf("project viewer test stream status = %d, want %d body=%s", viewerResp.Code, http.StatusBadRequest, viewerResp.Body.String())
	}
}

func TestTrustedHeaderAuthScopesGlobalWebSocketToProjectRoles(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	project := &storage.Project{ID: uuid.New().String(), Name: "Checkout", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	unauthorizedReq := httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil)
	unauthorizedResp := httptest.NewRecorder()
	server.mux.ServeHTTP(unauthorizedResp, unauthorizedReq)
	if unauthorizedResp.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized global websocket status = %d, want %d body=%s", unauthorizedResp.Code, http.StatusUnauthorized, unauthorizedResp.Body.String())
	}

	viewerReq := httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil)
	viewerReq.Header.Set("X-Auth-Request-User", "viewer-user")
	viewerReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleViewer)
	viewerResp := httptest.NewRecorder()
	server.mux.ServeHTTP(viewerResp, viewerReq)
	if viewerResp.Code != http.StatusForbidden {
		t.Fatalf("project viewer unfiltered global websocket status = %d, want %d body=%s", viewerResp.Code, http.StatusForbidden, viewerResp.Body.String())
	}

	globalViewerReq := httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil)
	globalViewerReq.Header.Set("X-Auth-Request-User", "global-viewer")
	globalViewerReq.Header.Set("X-APEXLoad-Roles", RoleViewer)
	globalViewerResp := httptest.NewRecorder()
	server.mux.ServeHTTP(globalViewerResp, globalViewerReq)
	if globalViewerResp.Code != http.StatusBadRequest {
		t.Fatalf("global viewer unfiltered websocket status = %d, want %d body=%s", globalViewerResp.Code, http.StatusBadRequest, globalViewerResp.Body.String())
	}

	filteredReq := httptest.NewRequest(http.MethodGet, "/api/v1/ws?project_id="+project.ID, nil)
	filteredReq.Header.Set("X-Auth-Request-User", "viewer-user")
	filteredReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleViewer)
	filteredResp := httptest.NewRecorder()
	server.mux.ServeHTTP(filteredResp, filteredReq)
	if filteredResp.Code != http.StatusBadRequest {
		t.Fatalf("project viewer filtered global websocket status = %d, want %d body=%s", filteredResp.Code, http.StatusBadRequest, filteredResp.Body.String())
	}
}

func TestTrustedHeaderAuthScopesApplicationListAndCreateToProjectRoles(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	project := &storage.Project{ID: uuid.New().String(), Name: "Project One", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	if err := store.SaveApplication(&storage.Application{ID: uuid.New().String(), ProjectID: project.ID, Name: "Payments", CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("SaveApplication returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/applications", nil)
	listReq.Header.Set("X-Auth-Request-User", "viewer-user")
	listReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleViewer)
	listResp := httptest.NewRecorder()
	server.mux.ServeHTTP(listResp, listReq)
	if listResp.Code != http.StatusForbidden {
		t.Fatalf("unfiltered application list status = %d, want %d body=%s", listResp.Code, http.StatusForbidden, listResp.Body.String())
	}

	filteredReq := httptest.NewRequest(http.MethodGet, "/api/v1/applications?project_id="+project.ID, nil)
	filteredReq.Header.Set("X-Auth-Request-User", "viewer-user")
	filteredReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleViewer)
	filteredResp := httptest.NewRecorder()
	server.mux.ServeHTTP(filteredResp, filteredReq)
	if filteredResp.Code != http.StatusOK {
		t.Fatalf("filtered application list status = %d, want %d body=%s", filteredResp.Code, http.StatusOK, filteredResp.Body.String())
	}

	createBody := []byte(`{"name":"Checkout","project_id":"` + project.ID + `"}`)
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/applications", bytes.NewReader(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	createReq.Header.Set("X-Auth-Request-User", "editor-user")
	createReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleEditor)
	createResp := httptest.NewRecorder()
	server.mux.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("project editor application create status = %d, want %d body=%s", createResp.Code, http.StatusCreated, createResp.Body.String())
	}
	var created storage.Application
	if err := json.Unmarshal(createResp.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal application: %v", err)
	}
	if created.ProjectID != project.ID {
		t.Fatalf("created application project id = %q, want %q", created.ProjectID, project.ID)
	}
}

func TestTrustedHeaderAuthProjectRoleGuardsApplicationAndReleaseEndpoints(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	project := &storage.Project{ID: uuid.New().String(), Name: "Project One", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	application := &storage.Application{ID: uuid.New().String(), ProjectID: project.ID, Name: "Payments", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveApplication(application); err != nil {
		t.Fatalf("SaveApplication returned error: %v", err)
	}
	release := &storage.Release{ID: uuid.New().String(), ApplicationID: application.ID, Name: "2026.05", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveRelease(release); err != nil {
		t.Fatalf("SaveRelease returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/applications/"+application.ID, nil)
	getReq.Header.Set("X-Auth-Request-User", "viewer-user")
	getReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleViewer)
	getResp := httptest.NewRecorder()
	server.mux.ServeHTTP(getResp, getReq)
	if getResp.Code != http.StatusOK {
		t.Fatalf("application viewer GET status = %d, want %d body=%s", getResp.Code, http.StatusOK, getResp.Body.String())
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/api/v1/applications/"+application.ID, bytes.NewReader([]byte(`{"name":"Renamed"}`)))
	updateReq.Header.Set("Content-Type", "application/json")
	updateReq.Header.Set("X-Auth-Request-User", "viewer-user")
	updateReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleViewer)
	updateResp := httptest.NewRecorder()
	server.mux.ServeHTTP(updateResp, updateReq)
	if updateResp.Code != http.StatusForbidden {
		t.Fatalf("application viewer PUT status = %d, want %d body=%s", updateResp.Code, http.StatusForbidden, updateResp.Body.String())
	}

	releasesReq := httptest.NewRequest(http.MethodGet, "/api/v1/applications/"+application.ID+"/releases", nil)
	releasesReq.Header.Set("X-Auth-Request-User", "viewer-user")
	releasesReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleViewer)
	releasesResp := httptest.NewRecorder()
	server.mux.ServeHTTP(releasesResp, releasesReq)
	if releasesResp.Code != http.StatusOK {
		t.Fatalf("release list viewer GET status = %d, want %d body=%s", releasesResp.Code, http.StatusOK, releasesResp.Body.String())
	}

	archiveReq := httptest.NewRequest(http.MethodPost, "/api/v1/releases/"+release.ID+"/archive", nil)
	archiveReq.Header.Set("X-Auth-Request-User", "viewer-user")
	archiveReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleViewer)
	archiveResp := httptest.NewRecorder()
	server.mux.ServeHTTP(archiveResp, archiveReq)
	if archiveResp.Code != http.StatusForbidden {
		t.Fatalf("release viewer archive status = %d, want %d body=%s", archiveResp.Code, http.StatusForbidden, archiveResp.Body.String())
	}

	createReleaseReq := httptest.NewRequest(http.MethodPost, "/api/v1/applications/"+application.ID+"/releases", bytes.NewReader([]byte(`{"name":"2026.06"}`)))
	createReleaseReq.Header.Set("Content-Type", "application/json")
	createReleaseReq.Header.Set("X-Auth-Request-User", "editor-user")
	createReleaseReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleEditor)
	createReleaseResp := httptest.NewRecorder()
	server.mux.ServeHTTP(createReleaseResp, createReleaseReq)
	if createReleaseResp.Code != http.StatusCreated {
		t.Fatalf("release editor create status = %d, want %d body=%s", createReleaseResp.Code, http.StatusCreated, createReleaseResp.Body.String())
	}

	createProjectReleaseReq := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+project.ID+"/releases", bytes.NewReader([]byte(`{"name":"2026.07","application_id":"`+application.ID+`"}`)))
	createProjectReleaseReq.Header.Set("Content-Type", "application/json")
	createProjectReleaseReq.Header.Set("X-Auth-Request-User", "editor-user")
	createProjectReleaseReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleEditor)
	createProjectReleaseResp := httptest.NewRecorder()
	server.mux.ServeHTTP(createProjectReleaseResp, createProjectReleaseReq)
	if createProjectReleaseResp.Code != http.StatusCreated {
		t.Fatalf("project release editor create status = %d, want %d body=%s", createProjectReleaseResp.Code, http.StatusCreated, createProjectReleaseResp.Body.String())
	}
}

func TestTrustedHeaderAuthScopesTestListByContextToProjectRoles(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	project := &storage.Project{ID: uuid.New().String(), Name: "Project One", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	application := &storage.Application{ID: uuid.New().String(), ProjectID: project.ID, Name: "Payments", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveApplication(application); err != nil {
		t.Fatalf("SaveApplication returned error: %v", err)
	}
	release := &storage.Release{ID: uuid.New().String(), ApplicationID: application.ID, Name: "2026.05", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveRelease(release); err != nil {
		t.Fatalf("SaveRelease returned error: %v", err)
	}
	if err := store.SaveTest(&storage.TestRecord{ID: uuid.New().String(), ProjectID: project.ID, Name: "Checkout", Status: "COMPLETE", ApplicationID: application.ID, ReleaseID: release.ID, StartTime: time.Now()}); err != nil {
		t.Fatalf("SaveTest returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/tests?application_id="+application.ID+"&release_id="+release.ID, nil)
	listReq.Header.Set("X-Auth-Request-User", "viewer-user")
	listReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleViewer)
	listResp := httptest.NewRecorder()
	server.mux.ServeHTTP(listResp, listReq)
	if listResp.Code != http.StatusOK {
		t.Fatalf("project viewer test context list status = %d, want %d body=%s", listResp.Code, http.StatusOK, listResp.Body.String())
	}

	otherProject := &storage.Project{ID: uuid.New().String(), Name: "Project Two", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(otherProject); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	foreignApplication := &storage.Application{ID: uuid.New().String(), ProjectID: otherProject.ID, Name: "Billing", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveApplication(foreignApplication); err != nil {
		t.Fatalf("SaveApplication returned error: %v", err)
	}
	foreignRelease := &storage.Release{ID: uuid.New().String(), ApplicationID: foreignApplication.ID, Name: "2026.07", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveRelease(foreignRelease); err != nil {
		t.Fatalf("SaveRelease returned error: %v", err)
	}

	inconsistentReq := httptest.NewRequest(http.MethodGet, "/api/v1/tests?application_id="+application.ID+"&release_id="+foreignRelease.ID, nil)
	inconsistentReq.Header.Set("X-Auth-Request-User", "viewer-user")
	inconsistentReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleViewer)
	inconsistentResp := httptest.NewRecorder()
	server.mux.ServeHTTP(inconsistentResp, inconsistentReq)
	if inconsistentResp.Code != http.StatusBadRequest {
		t.Fatalf("inconsistent test context list status = %d, want %d body=%s", inconsistentResp.Code, http.StatusBadRequest, inconsistentResp.Body.String())
	}
}

func TestTrustedHeaderAuthResolvesFolderProjectForMutations(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	project := &storage.Project{ID: uuid.New().String(), Name: "Project One", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	folder := &storage.Folder{ID: uuid.New().String(), ProjectID: project.ID, Name: "Results", Category: "results", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveFolder(folder); err != nil {
		t.Fatalf("SaveFolder returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	body := []byte(`{"name":"Archived Results"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/folders/"+folder.ID, bytes.NewReader(body))
	req.Header.Set("X-Auth-Request-User", "project-admin")
	req.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleProjectAdmin)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("folder update status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	updated, err := store.GetFolder(folder.ID)
	if err != nil {
		t.Fatalf("GetFolder returned error: %v", err)
	}
	if updated.Name != "Archived Results" {
		t.Fatalf("folder name = %q, want Archived Results", updated.Name)
	}
}

func TestTrustedHeaderAuthScopesProjectFolderListToProjectRoles(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	project := &storage.Project{ID: uuid.New().String(), Name: "Project One", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	folder := &storage.Folder{ID: uuid.New().String(), ProjectID: project.ID, Name: "Scenarios", Category: "scenarios", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveFolder(folder); err != nil {
		t.Fatalf("SaveFolder returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	unauthorizedReq := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+project.ID+"/folders", nil)
	unauthorizedResp := httptest.NewRecorder()
	server.mux.ServeHTTP(unauthorizedResp, unauthorizedReq)
	if unauthorizedResp.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized project folders GET status = %d, want %d body=%s", unauthorizedResp.Code, http.StatusUnauthorized, unauthorizedResp.Body.String())
	}

	viewerReq := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+project.ID+"/folders", nil)
	viewerReq.Header.Set("X-Auth-Request-User", "viewer-user")
	viewerReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleViewer)
	viewerResp := httptest.NewRecorder()
	server.mux.ServeHTTP(viewerResp, viewerReq)
	if viewerResp.Code != http.StatusOK {
		t.Fatalf("project viewer folders GET status = %d, want %d body=%s", viewerResp.Code, http.StatusOK, viewerResp.Body.String())
	}
}

func TestTrustedHeaderEditorCanCreateOnlyScriptFolders(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	project := &storage.Project{ID: uuid.New().String(), Name: "Editor folders", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	resultsParent := &storage.Folder{ID: uuid.New().String(), ProjectID: project.ID, Name: "Results", Category: "results", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveFolder(resultsParent); err != nil {
		t.Fatalf("SaveFolder returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)
	create := func(subject, role, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+project.ID+"/folders", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Auth-Request-User", subject)
		req.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+role)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		return resp
	}

	if resp := create("editor", RoleEditor, `{"name":"Imported APIs","category":"scripts"}`); resp.Code != http.StatusCreated {
		t.Fatalf("editor script folder status = %d, want %d body=%s", resp.Code, http.StatusCreated, resp.Body.String())
	}
	if resp := create("editor", RoleEditor, `{"name":"Editor Results","category":"results"}`); resp.Code != http.StatusForbidden {
		t.Fatalf("editor result folder status = %d, want %d body=%s", resp.Code, http.StatusForbidden, resp.Body.String())
	}
	if resp := create("editor", RoleEditor, `{"name":"Nested Wrongly","category":"scripts","parent_id":"`+resultsParent.ID+`"}`); resp.Code != http.StatusForbidden {
		t.Fatalf("editor script folder under results status = %d, want %d body=%s", resp.Code, http.StatusForbidden, resp.Body.String())
	}
	if resp := create("viewer", RoleViewer, `{"name":"Viewer Scripts","category":"scripts"}`); resp.Code != http.StatusForbidden {
		t.Fatalf("viewer script folder status = %d, want %d body=%s", resp.Code, http.StatusForbidden, resp.Body.String())
	}
	if resp := create("project-admin", RoleProjectAdmin, `{"name":"Admin Results","category":"results"}`); resp.Code != http.StatusCreated {
		t.Fatalf("project admin result folder status = %d, want %d body=%s", resp.Code, http.StatusCreated, resp.Body.String())
	}
}

func TestTrustedHeaderAuthProjectRoleGuardsStoredScriptEndpoints(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	project := &storage.Project{ID: uuid.New().String(), Name: "Project One", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	script := &dsl.Script{
		ID:        uuid.New().String(),
		Name:      "Checkout",
		Version:   "1.0.0",
		Protocol:  "http",
		ProjectID: project.ID,
		Config:    dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:   []dsl.Action{{Name: "Open", Type: "http", Method: "GET", Target: "/"}},
	}
	if err := store.SaveScript(script); err != nil {
		t.Fatalf("SaveScript returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/"+script.ID, nil)
	getReq.Header.Set("X-Auth-Request-User", "viewer-user")
	getReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleViewer)
	getResp := httptest.NewRecorder()
	server.mux.ServeHTTP(getResp, getReq)
	if getResp.Code != http.StatusOK {
		t.Fatalf("script viewer GET status = %d, want %d body=%s", getResp.Code, http.StatusOK, getResp.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/scripts/"+script.ID, nil)
	deleteReq.Header.Set("X-Auth-Request-User", "viewer-user")
	deleteReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleViewer)
	deleteResp := httptest.NewRecorder()
	server.mux.ServeHTTP(deleteResp, deleteReq)
	if deleteResp.Code != http.StatusForbidden {
		t.Fatalf("script viewer DELETE status = %d, want %d body=%s", deleteResp.Code, http.StatusForbidden, deleteResp.Body.String())
	}

	editorDeleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/scripts/"+script.ID, nil)
	editorDeleteReq.Header.Set("X-Auth-Request-User", "editor-user")
	editorDeleteReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleEditor)
	editorDeleteResp := httptest.NewRecorder()
	server.mux.ServeHTTP(editorDeleteResp, editorDeleteReq)
	if editorDeleteResp.Code != http.StatusOK {
		t.Fatalf("script editor DELETE status = %d, want %d body=%s", editorDeleteResp.Code, http.StatusOK, editorDeleteResp.Body.String())
	}
	if _, err := store.GetScript(script.ID); err == nil {
		t.Fatal("expected deleted script to be removed from storage")
	}
}

func TestTrustedHeaderAuthScopesScriptListAndCreateToProjectRoles(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	project := &storage.Project{ID: uuid.New().String(), Name: "Project One", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	existing := &dsl.Script{
		ID:        uuid.New().String(),
		Name:      "Existing",
		Version:   "1.0.0",
		Protocol:  "http",
		ProjectID: project.ID,
		Config:    dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:   []dsl.Action{{Name: "Open", Type: "http", Method: "GET", Target: "/"}},
	}
	if err := store.SaveScript(existing); err != nil {
		t.Fatalf("SaveScript returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/scripts", nil)
	listReq.Header.Set("X-Auth-Request-User", "viewer-user")
	listReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleViewer)
	listResp := httptest.NewRecorder()
	server.mux.ServeHTTP(listResp, listReq)
	if listResp.Code != http.StatusForbidden {
		t.Fatalf("unfiltered script list status = %d, want %d body=%s", listResp.Code, http.StatusForbidden, listResp.Body.String())
	}

	filteredReq := httptest.NewRequest(http.MethodGet, "/api/v1/scripts?project_id="+project.ID, nil)
	filteredReq.Header.Set("X-Auth-Request-User", "viewer-user")
	filteredReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleViewer)
	filteredResp := httptest.NewRecorder()
	server.mux.ServeHTTP(filteredResp, filteredReq)
	if filteredResp.Code != http.StatusOK {
		t.Fatalf("filtered script list status = %d, want %d body=%s", filteredResp.Code, http.StatusOK, filteredResp.Body.String())
	}

	body := []byte(`{"project_id":"` + project.ID + `","script":{"name":"Created","version":"1.0.0","protocol":"http","config":{"base_url":"https://example.com"},"actions":[{"name":"Open","type":"http","method":"GET","target":"/"}]}}`)
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts", bytes.NewReader(body))
	createReq.Header.Set("Content-Type", "application/json")
	createReq.Header.Set("X-Auth-Request-User", "editor-user")
	createReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleEditor)
	createResp := httptest.NewRecorder()
	server.mux.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("project editor create status = %d, want %d body=%s", createResp.Code, http.StatusCreated, createResp.Body.String())
	}
}

func TestTrustedHeaderAuthProjectRoleGuardsDraftScriptExecution(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)
	projectID := uuid.New().String()

	replayBody := []byte(`{"script":{"name":"Draft Replay","version":"1.0.0","protocol":"http","project_id":"` + projectID + `","config":{"base_url":"https://example.com"},"actions":[]}}`)
	viewerReplayReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/replay", bytes.NewReader(replayBody))
	viewerReplayReq.Header.Set("Content-Type", "application/json")
	viewerReplayReq.Header.Set("X-Auth-Request-User", "viewer-user")
	viewerReplayReq.Header.Set("X-APEXLoad-Project-Roles", projectID+"="+RoleViewer)
	viewerReplayResp := httptest.NewRecorder()
	server.mux.ServeHTTP(viewerReplayResp, viewerReplayReq)
	if viewerReplayResp.Code != http.StatusForbidden {
		t.Fatalf("draft replay viewer status = %d, want %d body=%s", viewerReplayResp.Code, http.StatusForbidden, viewerReplayResp.Body.String())
	}

	editorReplayReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/replay", bytes.NewReader(replayBody))
	editorReplayReq.Header.Set("Content-Type", "application/json")
	editorReplayReq.Header.Set("X-Auth-Request-User", "editor-user")
	editorReplayReq.Header.Set("X-APEXLoad-Project-Roles", projectID+"="+RoleEditor)
	editorReplayResp := httptest.NewRecorder()
	server.mux.ServeHTTP(editorReplayResp, editorReplayReq)
	if editorReplayResp.Code == http.StatusForbidden || editorReplayResp.Code == http.StatusUnauthorized {
		t.Fatalf("draft replay editor status = %d, want auth to pass body=%s", editorReplayResp.Code, editorReplayResp.Body.String())
	}

	browserBody := []byte(`{"script":{"name":"Draft Browser Replay","version":"1.0.0","protocol":"browser","project_id":"` + projectID + `","config":{"browser":{"engine":"chromium"}},"actions":[]}}`)
	viewerBrowserReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/browser-replay/sessions", bytes.NewReader(browserBody))
	viewerBrowserReq.Header.Set("Content-Type", "application/json")
	viewerBrowserReq.Header.Set("X-Auth-Request-User", "viewer-user")
	viewerBrowserReq.Header.Set("X-APEXLoad-Project-Roles", projectID+"="+RoleViewer)
	viewerBrowserResp := httptest.NewRecorder()
	server.mux.ServeHTTP(viewerBrowserResp, viewerBrowserReq)
	if viewerBrowserResp.Code != http.StatusForbidden {
		t.Fatalf("draft browser replay viewer status = %d, want %d body=%s", viewerBrowserResp.Code, http.StatusForbidden, viewerBrowserResp.Body.String())
	}

	editorBrowserReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/browser-replay/sessions", bytes.NewReader(browserBody))
	editorBrowserReq.Header.Set("Content-Type", "application/json")
	editorBrowserReq.Header.Set("X-Auth-Request-User", "editor-user")
	editorBrowserReq.Header.Set("X-APEXLoad-Project-Roles", projectID+"="+RoleEditor)
	editorBrowserResp := httptest.NewRecorder()
	server.mux.ServeHTTP(editorBrowserResp, editorBrowserReq)
	if editorBrowserResp.Code == http.StatusForbidden || editorBrowserResp.Code == http.StatusUnauthorized {
		t.Fatalf("draft browser replay editor status = %d, want auth to pass body=%s", editorBrowserResp.Code, editorBrowserResp.Body.String())
	}

	debugSessionID := uuid.New().String()
	debugBody := []byte(`{"project_id":"` + projectID + `","replay_session_id":"` + debugSessionID + `","start_paused":true,"script":{"name":"Draft Debug","version":"1.0.0","protocol":"http","config":{"base_url":"https://example.com"},"actions":[{"name":"Open","type":"http","method":"GET","target":"/"}]}}`)
	viewerDebugReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/debug/sessions", bytes.NewReader(debugBody))
	viewerDebugReq.Header.Set("Content-Type", "application/json")
	viewerDebugReq.Header.Set("X-Auth-Request-User", "viewer-user")
	viewerDebugReq.Header.Set("X-APEXLoad-Project-Roles", projectID+"="+RoleViewer)
	viewerDebugResp := httptest.NewRecorder()
	server.mux.ServeHTTP(viewerDebugResp, viewerDebugReq)
	if viewerDebugResp.Code != http.StatusForbidden {
		t.Fatalf("draft debug viewer status = %d, want %d body=%s", viewerDebugResp.Code, http.StatusForbidden, viewerDebugResp.Body.String())
	}

	editorDebugReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/debug/sessions", bytes.NewReader(debugBody))
	editorDebugReq.Header.Set("Content-Type", "application/json")
	editorDebugReq.Header.Set("X-Auth-Request-User", "editor-user")
	editorDebugReq.Header.Set("X-APEXLoad-Project-Roles", projectID+"="+RoleEditor)
	editorDebugResp := httptest.NewRecorder()
	server.mux.ServeHTTP(editorDebugResp, editorDebugReq)
	if editorDebugResp.Code != http.StatusOK {
		t.Fatalf("draft debug editor status = %d, want %d body=%s", editorDebugResp.Code, http.StatusOK, editorDebugResp.Body.String())
	}
	_, _ = server.scriptDebug.Stop(debugSessionID)
}

func TestTrustedHeaderAuthProjectRoleGuardsJMeterImportSave(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)
	projectID := uuid.New().String()
	body := []byte(`{"scripts":[{"name":"Imported Flow","version":"1.0.0","protocol":"http","project_id":"` + projectID + `","config":{"base_url":"https://example.com"},"actions":[{"name":"Open","type":"http","method":"GET","target":"/"}]}]}`)

	viewerReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/jmeter/save", bytes.NewReader(body))
	viewerReq.Header.Set("Content-Type", "application/json")
	viewerReq.Header.Set("X-Auth-Request-User", "viewer-user")
	viewerReq.Header.Set("X-APEXLoad-Project-Roles", projectID+"="+RoleViewer)
	viewerResp := httptest.NewRecorder()
	server.mux.ServeHTTP(viewerResp, viewerReq)
	if viewerResp.Code != http.StatusForbidden {
		t.Fatalf("jmeter save viewer status = %d, want %d body=%s", viewerResp.Code, http.StatusForbidden, viewerResp.Body.String())
	}

	editorReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/jmeter/save", bytes.NewReader(body))
	editorReq.Header.Set("Content-Type", "application/json")
	editorReq.Header.Set("X-Auth-Request-User", "editor-user")
	editorReq.Header.Set("X-APEXLoad-Project-Roles", projectID+"="+RoleEditor)
	editorResp := httptest.NewRecorder()
	server.mux.ServeHTTP(editorResp, editorReq)
	if editorResp.Code != http.StatusCreated {
		t.Fatalf("jmeter save editor status = %d, want %d body=%s", editorResp.Code, http.StatusCreated, editorResp.Body.String())
	}
}

func TestTrustedHeaderAuthRequiresViewerForJMeterSupportMatrix(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	unauthorizedReq := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/import/jmeter/support-matrix", nil)
	unauthorizedResp := httptest.NewRecorder()
	server.mux.ServeHTTP(unauthorizedResp, unauthorizedReq)
	if unauthorizedResp.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized support matrix status = %d, want %d body=%s", unauthorizedResp.Code, http.StatusUnauthorized, unauthorizedResp.Body.String())
	}

	viewerReq := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/import/jmeter/support-matrix", nil)
	viewerReq.Header.Set("X-Auth-Request-User", "viewer-user")
	viewerReq.Header.Set("X-APEXLoad-Roles", RoleViewer)
	viewerResp := httptest.NewRecorder()
	server.mux.ServeHTTP(viewerResp, viewerReq)
	if viewerResp.Code != http.StatusOK {
		t.Fatalf("viewer support matrix status = %d, want %d body=%s", viewerResp.Code, http.StatusOK, viewerResp.Body.String())
	}
}

func TestTrustedHeaderAuthRequiresViewerForEnterpriseArchiveSupportMatrix(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	unauthorizedReq := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/import/enterprise-script-archive/support-matrix", nil)
	unauthorizedResp := httptest.NewRecorder()
	server.mux.ServeHTTP(unauthorizedResp, unauthorizedReq)
	if unauthorizedResp.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized support matrix status = %d, want %d body=%s", unauthorizedResp.Code, http.StatusUnauthorized, unauthorizedResp.Body.String())
	}

	viewerReq := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/import/enterprise-script-archive/support-matrix", nil)
	viewerReq.Header.Set("X-Auth-Request-User", "viewer-user")
	viewerReq.Header.Set("X-APEXLoad-Roles", RoleViewer)
	viewerResp := httptest.NewRecorder()
	server.mux.ServeHTTP(viewerResp, viewerReq)
	if viewerResp.Code != http.StatusOK {
		t.Fatalf("viewer support matrix status = %d, want %d body=%s", viewerResp.Code, http.StatusOK, viewerResp.Body.String())
	}
}

func TestTrustedHeaderAuthRequiresViewerForWebAPISupportMatrices(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	for _, path := range []string{
		"/api/v1/scripts/web-api/support-matrix",
		"/api/v1/scripts/import/devweb/support-matrix",
	} {
		unauthorizedReq := httptest.NewRequest(http.MethodGet, path, nil)
		unauthorizedResp := httptest.NewRecorder()
		server.mux.ServeHTTP(unauthorizedResp, unauthorizedReq)
		if unauthorizedResp.Code != http.StatusUnauthorized {
			t.Fatalf("%s unauthorized status = %d, want %d body=%s", path, unauthorizedResp.Code, http.StatusUnauthorized, unauthorizedResp.Body.String())
		}

		viewerReq := httptest.NewRequest(http.MethodGet, path, nil)
		viewerReq.Header.Set("X-Auth-Request-User", "viewer-user")
		viewerReq.Header.Set("X-APEXLoad-Roles", RoleViewer)
		viewerResp := httptest.NewRecorder()
		server.mux.ServeHTTP(viewerResp, viewerReq)
		if viewerResp.Code != http.StatusOK {
			t.Fatalf("%s viewer status = %d, want %d body=%s", path, viewerResp.Code, http.StatusOK, viewerResp.Body.String())
		}
	}
}

func TestTrustedHeaderAuthGuardsOpenAPIImport(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)
	projectID := uuid.New().String()
	body, err := json.Marshal(map[string]string{
		"project_id": projectID,
		"content":    `{"openapi":"3.0.3","info":{"title":"Orders"},"servers":[{"url":"https://api.example.test"}],"paths":{"/orders":{"get":{"operationId":"listOrders","responses":{"200":{}}}}}}`,
	})
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}

	viewerReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/openapi", bytes.NewReader(body))
	viewerReq.Header.Set("Content-Type", "application/json")
	viewerReq.Header.Set("X-Auth-Request-User", "viewer-user")
	viewerReq.Header.Set("X-APEXLoad-Project-Roles", projectID+"="+RoleViewer)
	viewerResp := httptest.NewRecorder()
	server.mux.ServeHTTP(viewerResp, viewerReq)
	if viewerResp.Code != http.StatusForbidden {
		t.Fatalf("openapi viewer status = %d, want %d body=%s", viewerResp.Code, http.StatusForbidden, viewerResp.Body.String())
	}

	editorReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/openapi", bytes.NewReader(body))
	editorReq.Header.Set("Content-Type", "application/json")
	editorReq.Header.Set("X-Auth-Request-User", "editor-user")
	editorReq.Header.Set("X-APEXLoad-Roles", RoleEditor)
	editorResp := httptest.NewRecorder()
	server.mux.ServeHTTP(editorResp, editorReq)
	if editorResp.Code != http.StatusOK {
		t.Fatalf("openapi editor status = %d, want %d body=%s", editorResp.Code, http.StatusOK, editorResp.Body.String())
	}
}

func TestTrustedHeaderAuthRequiresSystemAdminForAuthDiagnostics(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	unauthorizedReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/diagnostics", nil)
	unauthorizedResp := httptest.NewRecorder()
	server.mux.ServeHTTP(unauthorizedResp, unauthorizedReq)
	if unauthorizedResp.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized auth diagnostics status = %d, want %d body=%s", unauthorizedResp.Code, http.StatusUnauthorized, unauthorizedResp.Body.String())
	}

	viewerReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/diagnostics", nil)
	viewerReq.Header.Set("X-Auth-Request-User", "viewer-user")
	viewerReq.Header.Set("X-APEXLoad-Roles", RoleViewer)
	viewerResp := httptest.NewRecorder()
	server.mux.ServeHTTP(viewerResp, viewerReq)
	if viewerResp.Code != http.StatusForbidden {
		t.Fatalf("viewer auth diagnostics status = %d, want %d body=%s", viewerResp.Code, http.StatusForbidden, viewerResp.Body.String())
	}

	adminReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/diagnostics", nil)
	adminReq.Header.Set("X-Auth-Request-User", "admin-user")
	adminReq.Header.Set("X-APEXLoad-Roles", RoleSystemAdmin)
	adminResp := httptest.NewRecorder()
	server.mux.ServeHTTP(adminResp, adminReq)
	if adminResp.Code != http.StatusOK {
		t.Fatalf("system admin auth diagnostics status = %d, want %d body=%s", adminResp.Code, http.StatusOK, adminResp.Body.String())
	}
}

func TestTrustedHeaderAuthProjectRoleGuardsImportPreviewEndpoints(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)
	projectID := uuid.New().String()

	viewerEnterpriseArchiveReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/enterprise-script-archive?project_id="+projectID, bytes.NewReader([]byte("zip")))
	viewerEnterpriseArchiveReq.Header.Set("Content-Type", "application/zip")
	viewerEnterpriseArchiveReq.Header.Set("X-Auth-Request-User", "viewer-user")
	viewerEnterpriseArchiveReq.Header.Set("X-APEXLoad-Project-Roles", projectID+"="+RoleViewer)
	viewerEnterpriseArchiveResp := httptest.NewRecorder()
	server.mux.ServeHTTP(viewerEnterpriseArchiveResp, viewerEnterpriseArchiveReq)
	if viewerEnterpriseArchiveResp.Code != http.StatusForbidden {
		t.Fatalf("enterprise archive import viewer status = %d, want %d body=%s", viewerEnterpriseArchiveResp.Code, http.StatusForbidden, viewerEnterpriseArchiveResp.Body.String())
	}

	editorEnterpriseArchiveReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/enterprise-script-archive?project_id="+projectID, bytes.NewReader([]byte("zip")))
	editorEnterpriseArchiveReq.Header.Set("Content-Type", "application/zip")
	editorEnterpriseArchiveReq.Header.Set("X-Auth-Request-User", "editor-user")
	editorEnterpriseArchiveReq.Header.Set("X-APEXLoad-Project-Roles", projectID+"="+RoleEditor)
	editorEnterpriseArchiveResp := httptest.NewRecorder()
	server.mux.ServeHTTP(editorEnterpriseArchiveResp, editorEnterpriseArchiveReq)
	if editorEnterpriseArchiveResp.Code == http.StatusForbidden || editorEnterpriseArchiveResp.Code == http.StatusUnauthorized {
		t.Fatalf("enterprise archive import editor status = %d, want auth to pass body=%s", editorEnterpriseArchiveResp.Code, editorEnterpriseArchiveResp.Body.String())
	}

	viewerSampleReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/sample", bytes.NewReader([]byte(`{"project_id":"`+projectID+`","format":"raw_http","content":"GET / HTTP/1.1\nHost: example.com\n\n"}`)))
	viewerSampleReq.Header.Set("Content-Type", "application/json")
	viewerSampleReq.Header.Set("X-Auth-Request-User", "viewer-user")
	viewerSampleReq.Header.Set("X-APEXLoad-Project-Roles", projectID+"="+RoleViewer)
	viewerSampleResp := httptest.NewRecorder()
	server.mux.ServeHTTP(viewerSampleResp, viewerSampleReq)
	if viewerSampleResp.Code != http.StatusForbidden {
		t.Fatalf("sample import viewer status = %d, want %d body=%s", viewerSampleResp.Code, http.StatusForbidden, viewerSampleResp.Body.String())
	}

	editorSampleReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/sample", bytes.NewReader([]byte(`{"project_id":"`+projectID+`","format":"raw_http","content":"GET / HTTP/1.1\nHost: example.com\n\n"}`)))
	editorSampleReq.Header.Set("Content-Type", "application/json")
	editorSampleReq.Header.Set("X-Auth-Request-User", "editor-user")
	editorSampleReq.Header.Set("X-APEXLoad-Project-Roles", projectID+"="+RoleEditor)
	editorSampleResp := httptest.NewRecorder()
	server.mux.ServeHTTP(editorSampleResp, editorSampleReq)
	if editorSampleResp.Code == http.StatusForbidden || editorSampleResp.Code == http.StatusUnauthorized {
		t.Fatalf("sample import editor status = %d, want auth to pass body=%s", editorSampleResp.Code, editorSampleResp.Body.String())
	}

	mismatchBody := []byte(`{"project_id":"other-` + projectID + `","content":"{\"log\":{\"entries\":[]}}"}`)
	mismatchReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/har?project_id="+projectID, bytes.NewReader(mismatchBody))
	mismatchReq.Header.Set("Content-Type", "application/json")
	mismatchReq.Header.Set("X-Auth-Request-User", "editor-user")
	mismatchReq.Header.Set("X-APEXLoad-Project-Roles", projectID+"="+RoleEditor)
	mismatchResp := httptest.NewRecorder()
	server.mux.ServeHTTP(mismatchResp, mismatchReq)
	if mismatchResp.Code != http.StatusBadRequest {
		t.Fatalf("HAR import mismatched project_id status = %d, want %d body=%s", mismatchResp.Code, http.StatusBadRequest, mismatchResp.Body.String())
	}
}

func TestTrustedHeaderAuthReadsEnterpriseArchiveMultipartProjectID(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)
	projectID := uuid.New().String()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	if err := writer.WriteField("project_id", projectID); err != nil {
		t.Fatalf("WriteField returned error: %v", err)
	}
	part, err := writer.CreateFormFile("file", "script.zip")
	if err != nil {
		t.Fatalf("CreateFormFile returned error: %v", err)
	}
	// A minimally valid (empty) ZIP: the archive must clear the handler's magic
	// pre-check so this test still isolates the AUTH boundary rather than passing
	// on an unrelated 400.
	if _, err := part.Write(minimalEmptyZIP()); err != nil {
		t.Fatalf("part.Write returned error: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/enterprise-script-archive", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-Auth-Request-User", "editor-user")
	req.Header.Set("X-APEXLoad-Project-Roles", projectID+"="+RoleEditor)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code == http.StatusForbidden || resp.Code == http.StatusUnauthorized || resp.Code == http.StatusBadRequest {
		t.Fatalf("enterprise archive multipart editor status = %d, want auth to pass body=%s", resp.Code, resp.Body.String())
	}

	missingProjectBody := &bytes.Buffer{}
	missingWriter := multipart.NewWriter(missingProjectBody)
	missingPart, err := missingWriter.CreateFormFile("file", "script.zip")
	if err != nil {
		t.Fatalf("CreateFormFile without project returned error: %v", err)
	}
	if _, err := missingPart.Write(minimalEmptyZIP()); err != nil {
		t.Fatalf("missingPart.Write returned error: %v", err)
	}
	if err := missingWriter.Close(); err != nil {
		t.Fatalf("missingWriter.Close returned error: %v", err)
	}
	missingReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/enterprise-script-archive", missingProjectBody)
	missingReq.Header.Set("Content-Type", missingWriter.FormDataContentType())
	missingReq.Header.Set("X-Auth-Request-User", "editor-user")
	missingReq.Header.Set("X-APEXLoad-Project-Roles", storage.DefaultProjectID+"="+RoleEditor)
	missingResp := httptest.NewRecorder()
	server.mux.ServeHTTP(missingResp, missingReq)
	if missingResp.Code != http.StatusBadRequest {
		t.Fatalf("enterprise archive multipart missing project status = %d, want %d body=%s", missingResp.Code, http.StatusBadRequest, missingResp.Body.String())
	}
}

func TestImportMultipartProjectLookupHonorsRouteBodyLimits(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)
	projectID := uuid.New().String()
	tests := []struct {
		name  string
		path  string
		limit int64
	}{
		{name: "k6 import", path: "/api/v1/scripts/import/k6", limit: k6ImportMaxBodyBytes},
		{name: "devweb import", path: "/api/v1/scripts/import/devweb", limit: webAPIImportMaxBodyBytes},
		{name: "asyncapi import", path: "/api/v1/scripts/import/asyncapi", limit: webAPIImportMaxBodyBytes},
		{name: "raml import", path: "/api/v1/scripts/import/raml", limit: webAPIImportMaxBodyBytes},
		{name: "har import", path: "/api/v1/scripts/import/har", limit: harImportMaxBodyBytes},
		{name: "salesforce import", path: "/api/v1/scripts/import/salesforce", limit: harImportMaxBodyBytes},
		{name: "jmeter import", path: "/api/v1/scripts/import/jmeter", limit: jmeterImportMaxBodyBytes},
		{name: "enterprise archive import", path: "/api/v1/scripts/import/enterprise-script-archive", limit: enterpriseArchiveImportMaxBodyBytes},
		{name: "cloudtest import", path: "/api/v1/scripts/import/cloudtest", limit: webAPIImportMaxBodyBytes},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := oversizedMultipartImportRequest(t, tt.path, projectID, tt.limit+1024)
			req.Header.Set("X-Auth-Request-User", "editor-user")
			req.Header.Set("X-APEXLoad-Project-Roles", projectID+"="+RoleEditor)
			resp := httptest.NewRecorder()

			server.httpServer.Handler.ServeHTTP(resp, req)

			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
			}
		})
	}
}

func TestRequestBodyLimitsUseSmallDefaultAndExplicitImportEnvelopes(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		want   int64
	}{
		{name: "HAR capture import", method: http.MethodPost, path: "/api/v1/scripts/import/har", want: 300<<20 + 1<<20},
		{name: "Salesforce capture import", method: http.MethodPost, path: "/api/v1/scripts/import/salesforce", want: 300<<20 + 1<<20},
		{name: "import asset upload", method: http.MethodPost, path: "/api/v1/scripts/import/session/assets/upload", want: 50 << 20},
		{name: "ordinary settings mutation", method: http.MethodPut, path: "/api/v1/system/settings", want: defaultMaxRequestBodyBytes},
		{name: "library bundle import", method: http.MethodPost, path: "/api/v1/library/import", want: libraryBundleImportMaxBodyBytes},
		{name: "library archive import", method: http.MethodPost, path: "/api/v1/library/import/archive", want: libraryArchiveImportMaxBodyBytes},
		{name: "enterprise archive batch save", method: http.MethodPost, path: "/api/v1/scripts/import/enterprise-script-archive/save", want: enterpriseArchiveImportSaveMaxBodyBytes},
		{name: "JMeter batch save", method: http.MethodPost, path: "/api/v1/scripts/import/jmeter/save", want: jmeterImportSaveMaxBodyBytes},
		{name: "AsyncAPI commit", method: http.MethodPost, path: "/api/v1/scripts/import/asyncapi/commit", want: webAPIImportCommitMaxBodyBytes},
		{name: "AsyncAPI batch commit", method: http.MethodPost, path: "/api/v1/scripts/import/asyncapi/commit-batch", want: webAPIImportCommitMaxBodyBytes},
		{name: "generic import batch commit", method: http.MethodPost, path: "/api/v1/scripts/import/commit-batch", want: webAPIImportCommitMaxBodyBytes},
		{name: "CloudTest import", method: http.MethodPost, path: "/api/v1/scripts/import/cloudtest", want: webAPIImportMaxBodyBytes},
		{name: "OpenAPI commit", method: http.MethodPost, path: "/api/v1/scripts/import/openapi/commit", want: webAPIImportCommitMaxBodyBytes},
		{name: "Postman save", method: http.MethodPost, path: "/api/v1/scripts/import/postman/save", want: webAPIImportCommitMaxBodyBytes},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			if got := requestMaxBodyBytes(req); got != tt.want {
				t.Fatalf("requestMaxBodyBytes() = %d, want %d", got, tt.want)
			}
		})
	}
	if defaultMaxRequestBodyBytes != 8<<20 {
		t.Fatalf("default request envelope = %d, want 8 MiB", defaultMaxRequestBodyBytes)
	}
}

func TestBodyLimitMiddlewareRejectsOversizedOrdinaryMutation(t *testing.T) {
	readAll := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/system/settings",
		io.LimitReader(zeroReader{}, defaultMaxRequestBodyBytes+1),
	)
	resp := httptest.NewRecorder()
	bodyLimitMiddleware(readAll).ServeHTTP(resp, req)

	if resp.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusRequestEntityTooLarge, resp.Body.String())
	}
}

func oversizedMultipartImportRequest(t *testing.T, path string, projectID string, fileBytes int64) *http.Request {
	t.Helper()
	prefix := &bytes.Buffer{}
	writer := multipart.NewWriter(prefix)
	if err := writer.WriteField("project_id", projectID); err != nil {
		t.Fatalf("WriteField returned error: %v", err)
	}
	if _, err := writer.CreateFormFile("file", "oversized.bin"); err != nil {
		t.Fatalf("CreateFormFile returned error: %v", err)
	}
	suffix := "\r\n--" + writer.Boundary() + "--\r\n"
	body := io.MultiReader(bytes.NewReader(prefix.Bytes()), io.LimitReader(zeroReader{}, fileBytes), strings.NewReader(suffix))
	req := httptest.NewRequest(http.MethodPost, path, body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

func TestTrustedHeaderAuthScopesRecorderListAndStartToProjectRoles(t *testing.T) {
	stubBrowserRecordingDependencyProbe(t, nil)
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)
	projectID := uuid.New().String()
	_, err := server.recorder.StartSessionForProject("rec-1", "Checkout Recording", projectID, recorder.SessionFilters{}, nil)
	if err != nil {
		t.Fatalf("StartSessionForProject returned error: %v", err)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/recorder", nil)
	listReq.Header.Set("X-Auth-Request-User", "viewer-user")
	listReq.Header.Set("X-APEXLoad-Project-Roles", projectID+"="+RoleViewer)
	listResp := httptest.NewRecorder()
	server.mux.ServeHTTP(listResp, listReq)
	if listResp.Code != http.StatusForbidden {
		t.Fatalf("unfiltered recorder list status = %d, want %d body=%s", listResp.Code, http.StatusForbidden, listResp.Body.String())
	}

	filteredReq := httptest.NewRequest(http.MethodGet, "/api/v1/recorder?project_id="+projectID, nil)
	filteredReq.Header.Set("X-Auth-Request-User", "viewer-user")
	filteredReq.Header.Set("X-APEXLoad-Project-Roles", projectID+"="+RoleViewer)
	filteredResp := httptest.NewRecorder()
	server.mux.ServeHTTP(filteredResp, filteredReq)
	if filteredResp.Code != http.StatusOK {
		t.Fatalf("filtered recorder list status = %d, want %d body=%s", filteredResp.Code, http.StatusOK, filteredResp.Body.String())
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/api/v1/recorder/status", nil)
	statusReq.Header.Set("X-Auth-Request-User", "viewer-user")
	statusReq.Header.Set("X-APEXLoad-Project-Roles", projectID+"="+RoleViewer)
	statusResp := httptest.NewRecorder()
	server.mux.ServeHTTP(statusResp, statusReq)
	if statusResp.Code != http.StatusForbidden {
		t.Fatalf("unfiltered recorder status = %d, want %d body=%s", statusResp.Code, http.StatusForbidden, statusResp.Body.String())
	}

	filteredStatusReq := httptest.NewRequest(http.MethodGet, "/api/v1/recorder/status?project_id="+projectID, nil)
	filteredStatusReq.Header.Set("X-Auth-Request-User", "viewer-user")
	filteredStatusReq.Header.Set("X-APEXLoad-Project-Roles", projectID+"="+RoleViewer)
	filteredStatusResp := httptest.NewRecorder()
	server.mux.ServeHTTP(filteredStatusResp, filteredStatusReq)
	if filteredStatusResp.Code != http.StatusOK {
		t.Fatalf("filtered recorder status = %d, want %d body=%s", filteredStatusResp.Code, http.StatusOK, filteredStatusResp.Body.String())
	}

	body := []byte(`{"name":"Checkout Recording","project_id":"` + projectID + `"}`)
	viewerStartReq := httptest.NewRequest(http.MethodPost, "/api/v1/recorder/start", bytes.NewReader(body))
	viewerStartReq.Header.Set("Content-Type", "application/json")
	viewerStartReq.Header.Set("X-Auth-Request-User", "viewer-user")
	viewerStartReq.Header.Set("X-APEXLoad-Project-Roles", projectID+"="+RoleViewer)
	viewerStartResp := httptest.NewRecorder()
	server.mux.ServeHTTP(viewerStartResp, viewerStartReq)
	if viewerStartResp.Code != http.StatusForbidden {
		t.Fatalf("recorder viewer start status = %d, want %d body=%s", viewerStartResp.Code, http.StatusForbidden, viewerStartResp.Body.String())
	}

	editorStartReq := httptest.NewRequest(http.MethodPost, "/api/v1/recorder/start", bytes.NewReader(body))
	editorStartReq.Header.Set("Content-Type", "application/json")
	editorStartReq.Header.Set("X-Auth-Request-User", "editor-user")
	editorStartReq.Header.Set("X-APEXLoad-Project-Roles", projectID+"="+RoleEditor)
	editorStartResp := httptest.NewRecorder()
	server.mux.ServeHTTP(editorStartResp, editorStartReq)
	if editorStartResp.Code != http.StatusConflict {
		t.Fatalf("recorder editor start status = %d, want %d body=%s", editorStartResp.Code, http.StatusConflict, editorStartResp.Body.String())
	}
}

func TestTrustedHeaderAuthScopesRecorderCACertToProjectRoles(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)
	projectID := uuid.New().String()

	unauthorizedReq := httptest.NewRequest(http.MethodGet, "/api/v1/recorder/ca.pem", nil)
	unauthorizedResp := httptest.NewRecorder()
	server.mux.ServeHTTP(unauthorizedResp, unauthorizedReq)
	if unauthorizedResp.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized recorder ca status = %d, want %d body=%s", unauthorizedResp.Code, http.StatusUnauthorized, unauthorizedResp.Body.String())
	}

	viewerReq := httptest.NewRequest(http.MethodGet, "/api/v1/recorder/ca.pem", nil)
	viewerReq.Header.Set("X-Auth-Request-User", "viewer-user")
	viewerReq.Header.Set("X-APEXLoad-Project-Roles", projectID+"="+RoleViewer)
	viewerResp := httptest.NewRecorder()
	server.mux.ServeHTTP(viewerResp, viewerReq)
	if viewerResp.Code != http.StatusForbidden {
		t.Fatalf("unfiltered recorder ca status = %d, want %d body=%s", viewerResp.Code, http.StatusForbidden, viewerResp.Body.String())
	}

	filteredReq := httptest.NewRequest(http.MethodGet, "/api/v1/recorder/ca.pem?project_id="+projectID, nil)
	filteredReq.Header.Set("X-Auth-Request-User", "viewer-user")
	filteredReq.Header.Set("X-APEXLoad-Project-Roles", projectID+"="+RoleViewer)
	filteredResp := httptest.NewRecorder()
	server.mux.ServeHTTP(filteredResp, filteredReq)
	if filteredResp.Code != http.StatusOK {
		t.Fatalf("filtered recorder ca status = %d, want %d body=%s", filteredResp.Code, http.StatusOK, filteredResp.Body.String())
	}
}

func TestTrustedHeaderAuthScopesBrowserRecorderSessionListToProjectRoles(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)
	projectID := uuid.New().String()

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/browser-recorder/sessions", nil)
	listReq.Header.Set("X-Auth-Request-User", "viewer-user")
	listReq.Header.Set("X-APEXLoad-Project-Roles", projectID+"="+RoleViewer)
	listResp := httptest.NewRecorder()
	server.mux.ServeHTTP(listResp, listReq)
	if listResp.Code != http.StatusForbidden {
		t.Fatalf("unfiltered browser recorder list status = %d, want %d body=%s", listResp.Code, http.StatusForbidden, listResp.Body.String())
	}

	filteredReq := httptest.NewRequest(http.MethodGet, "/api/v1/browser-recorder/sessions?project_id="+projectID, nil)
	filteredReq.Header.Set("X-Auth-Request-User", "viewer-user")
	filteredReq.Header.Set("X-APEXLoad-Project-Roles", projectID+"="+RoleViewer)
	filteredResp := httptest.NewRecorder()
	server.mux.ServeHTTP(filteredResp, filteredReq)
	if filteredResp.Code != http.StatusOK {
		t.Fatalf("filtered browser recorder list status = %d, want %d body=%s", filteredResp.Code, http.StatusOK, filteredResp.Body.String())
	}

	globalReq := httptest.NewRequest(http.MethodGet, "/api/v1/browser-recorder/sessions", nil)
	globalReq.Header.Set("X-Auth-Request-User", "global-viewer")
	globalReq.Header.Set("X-APEXLoad-Roles", RoleViewer)
	globalResp := httptest.NewRecorder()
	server.mux.ServeHTTP(globalResp, globalReq)
	if globalResp.Code != http.StatusOK {
		t.Fatalf("global browser recorder list status = %d, want %d body=%s", globalResp.Code, http.StatusOK, globalResp.Body.String())
	}
}

func TestTrustedHeaderAuthProjectRoleGuardsRecorderSessionEndpoints(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)
	projectID := uuid.New().String()
	session, err := server.recorder.StartSessionForProject("rec-1", "Checkout Recording", projectID, recorder.SessionFilters{}, nil)
	if err != nil {
		t.Fatalf("StartSessionForProject returned error: %v", err)
	}
	session.Requests = append(session.Requests, recorder.RecordedRequest{
		Seq:        1,
		Method:     http.MethodGet,
		URL:        "https://shop.example.com/api/cart",
		Path:       "/api/cart",
		StatusCode: http.StatusOK,
	})

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/recorder/rec-1", nil)
	getReq.Header.Set("X-Auth-Request-User", "viewer-user")
	getReq.Header.Set("X-APEXLoad-Project-Roles", projectID+"="+RoleViewer)
	getResp := httptest.NewRecorder()
	server.mux.ServeHTTP(getResp, getReq)
	if getResp.Code != http.StatusOK {
		t.Fatalf("recorder viewer GET status = %d, want %d body=%s", getResp.Code, http.StatusOK, getResp.Body.String())
	}

	previewReq := httptest.NewRequest(http.MethodPost, "/api/v1/recorder/rec-1/convert/preview", bytes.NewReader([]byte(`{"name":"Checkout Preview"}`)))
	previewReq.Header.Set("Content-Type", "application/json")
	previewReq.Header.Set("X-Auth-Request-User", "viewer-user")
	previewReq.Header.Set("X-APEXLoad-Project-Roles", projectID+"="+RoleViewer)
	previewResp := httptest.NewRecorder()
	server.mux.ServeHTTP(previewResp, previewReq)
	if previewResp.Code != http.StatusForbidden {
		t.Fatalf("recorder viewer preview status = %d, want %d body=%s", previewResp.Code, http.StatusForbidden, previewResp.Body.String())
	}

	editorPreviewReq := httptest.NewRequest(http.MethodPost, "/api/v1/recorder/rec-1/convert/preview", bytes.NewReader([]byte(`{"name":"Checkout Preview"}`)))
	editorPreviewReq.Header.Set("Content-Type", "application/json")
	editorPreviewReq.Header.Set("X-Auth-Request-User", "editor-user")
	editorPreviewReq.Header.Set("X-APEXLoad-Project-Roles", projectID+"="+RoleEditor)
	editorPreviewResp := httptest.NewRecorder()
	server.mux.ServeHTTP(editorPreviewResp, editorPreviewReq)
	if editorPreviewResp.Code != http.StatusOK {
		t.Fatalf("recorder editor preview status = %d, want %d body=%s", editorPreviewResp.Code, http.StatusOK, editorPreviewResp.Body.String())
	}

	stopReq := httptest.NewRequest(http.MethodPost, "/api/v1/recorder/rec-1/stop", nil)
	stopReq.Header.Set("X-Auth-Request-User", "viewer-user")
	stopReq.Header.Set("X-APEXLoad-Project-Roles", projectID+"="+RoleViewer)
	stopResp := httptest.NewRecorder()
	server.mux.ServeHTTP(stopResp, stopReq)
	if stopResp.Code != http.StatusForbidden {
		t.Fatalf("recorder viewer stop status = %d, want %d body=%s", stopResp.Code, http.StatusForbidden, stopResp.Body.String())
	}

	editorStopReq := httptest.NewRequest(http.MethodPost, "/api/v1/recorder/rec-1/stop", nil)
	editorStopReq.Header.Set("X-Auth-Request-User", "editor-user")
	editorStopReq.Header.Set("X-APEXLoad-Project-Roles", projectID+"="+RoleEditor)
	editorStopResp := httptest.NewRecorder()
	server.mux.ServeHTTP(editorStopResp, editorStopReq)
	if editorStopResp.Code != http.StatusOK {
		t.Fatalf("recorder editor stop status = %d, want %d body=%s", editorStopResp.Code, http.StatusOK, editorStopResp.Body.String())
	}
}

func TestTrustedHeaderAuthProjectRoleGuardsBrowserReplaySessionEndpoints(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)
	projectID := uuid.New().String()
	pauseBody := []byte(`{"script":{"name":"Draft Browser Replay","version":"1.0.0","protocol":"browser","project_id":"` + projectID + `","config":{"browser":{"engine":"chromium"}},"actions":[{"name":"Open homepage","type":"browser_navigate","target":"/"}]},"start_paused":true}`)
	startReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/browser-replay/sessions", bytes.NewReader(pauseBody))
	startReq.Header.Set("Content-Type", "application/json")
	startReq.Header.Set("X-Auth-Request-User", "editor-user")
	startReq.Header.Set("X-APEXLoad-Project-Roles", projectID+"="+RoleEditor)
	startResp := httptest.NewRecorder()
	server.mux.ServeHTTP(startResp, startReq)
	if startResp.Code != http.StatusOK {
		t.Fatalf("browser replay editor start status = %d, want %d body=%s", startResp.Code, http.StatusOK, startResp.Body.String())
	}

	var startPayload struct {
		ReplaySessionID string `json:"replay_session_id"`
	}
	if err := json.Unmarshal(startResp.Body.Bytes(), &startPayload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if startPayload.ReplaySessionID == "" {
		t.Fatal("expected replay_session_id in browser replay response")
	}

	viewerGetReq := httptest.NewRequest(http.MethodGet, "/api/v1/browser-replay/sessions/"+startPayload.ReplaySessionID, nil)
	viewerGetReq.Header.Set("X-Auth-Request-User", "viewer-user")
	viewerGetReq.Header.Set("X-APEXLoad-Project-Roles", projectID+"="+RoleViewer)
	viewerGetResp := httptest.NewRecorder()
	server.mux.ServeHTTP(viewerGetResp, viewerGetReq)
	if viewerGetResp.Code != http.StatusOK {
		t.Fatalf("browser replay viewer GET status = %d, want %d body=%s", viewerGetResp.Code, http.StatusOK, viewerGetResp.Body.String())
	}

	viewerResumeReq := httptest.NewRequest(http.MethodPost, "/api/v1/browser-replay/sessions/"+startPayload.ReplaySessionID+"/resume", nil)
	viewerResumeReq.Header.Set("X-Auth-Request-User", "viewer-user")
	viewerResumeReq.Header.Set("X-APEXLoad-Project-Roles", projectID+"="+RoleViewer)
	viewerResumeResp := httptest.NewRecorder()
	server.mux.ServeHTTP(viewerResumeResp, viewerResumeReq)
	if viewerResumeResp.Code != http.StatusForbidden {
		t.Fatalf("browser replay viewer resume status = %d, want %d body=%s", viewerResumeResp.Code, http.StatusForbidden, viewerResumeResp.Body.String())
	}

	editorResumeReq := httptest.NewRequest(http.MethodPost, "/api/v1/browser-replay/sessions/"+startPayload.ReplaySessionID+"/resume", nil)
	editorResumeReq.Header.Set("X-Auth-Request-User", "editor-user")
	editorResumeReq.Header.Set("X-APEXLoad-Project-Roles", projectID+"="+RoleEditor)
	editorResumeResp := httptest.NewRecorder()
	server.mux.ServeHTTP(editorResumeResp, editorResumeReq)
	if editorResumeResp.Code != http.StatusOK {
		t.Fatalf("browser replay editor resume status = %d, want %d body=%s", editorResumeResp.Code, http.StatusOK, editorResumeResp.Body.String())
	}

	editorPauseReq := httptest.NewRequest(http.MethodPost, "/api/v1/browser-replay/sessions/"+startPayload.ReplaySessionID+"/pause", nil)
	editorPauseReq.Header.Set("X-Auth-Request-User", "editor-user")
	editorPauseReq.Header.Set("X-APEXLoad-Project-Roles", projectID+"="+RoleEditor)
	editorPauseResp := httptest.NewRecorder()
	server.mux.ServeHTTP(editorPauseResp, editorPauseReq)
	if editorPauseResp.Code != http.StatusOK {
		t.Fatalf("browser replay editor pause status = %d, want %d body=%s", editorPauseResp.Code, http.StatusOK, editorPauseResp.Body.String())
	}

	editorStopReq := httptest.NewRequest(http.MethodPost, "/api/v1/browser-replay/sessions/"+startPayload.ReplaySessionID+"/stop", nil)
	editorStopReq.Header.Set("X-Auth-Request-User", "editor-user")
	editorStopReq.Header.Set("X-APEXLoad-Project-Roles", projectID+"="+RoleEditor)
	editorStopResp := httptest.NewRecorder()
	server.mux.ServeHTTP(editorStopResp, editorStopReq)
	if editorStopResp.Code != http.StatusOK {
		t.Fatalf("browser replay editor stop status = %d, want %d body=%s", editorStopResp.Code, http.StatusOK, editorStopResp.Body.String())
	}
}

func TestTrustedHeaderAuthScopesDataPoolListAndCreateToProjectRoles(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	project := &storage.Project{ID: uuid.New().String(), Name: "Checkout", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	if err := store.SaveDataPool(&storage.SharedDataPool{ID: uuid.New().String(), Name: "Customers", ProjectID: project.ID, CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("SaveDataPool returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/data-pools", nil)
	listReq.Header.Set("X-Auth-Request-User", "viewer-user")
	listReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleViewer)
	listResp := httptest.NewRecorder()
	server.mux.ServeHTTP(listResp, listReq)
	if listResp.Code != http.StatusForbidden {
		t.Fatalf("unfiltered data pool list status = %d, want %d body=%s", listResp.Code, http.StatusForbidden, listResp.Body.String())
	}

	filteredReq := httptest.NewRequest(http.MethodGet, "/api/v1/data-pools?project_id="+project.ID, nil)
	filteredReq.Header.Set("X-Auth-Request-User", "viewer-user")
	filteredReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleViewer)
	filteredResp := httptest.NewRecorder()
	server.mux.ServeHTTP(filteredResp, filteredReq)
	if filteredResp.Code != http.StatusOK {
		t.Fatalf("filtered data pool list status = %d, want %d body=%s", filteredResp.Code, http.StatusOK, filteredResp.Body.String())
	}

	createBody := []byte(`{"name":"New Pool","project_id":"` + project.ID + `"}`)
	viewerCreateReq := httptest.NewRequest(http.MethodPost, "/api/v1/data-pools", bytes.NewReader(createBody))
	viewerCreateReq.Header.Set("Content-Type", "application/json")
	viewerCreateReq.Header.Set("X-Auth-Request-User", "viewer-user")
	viewerCreateReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleViewer)
	viewerCreateResp := httptest.NewRecorder()
	server.mux.ServeHTTP(viewerCreateResp, viewerCreateReq)
	if viewerCreateResp.Code != http.StatusForbidden {
		t.Fatalf("data pool viewer create status = %d, want %d body=%s", viewerCreateResp.Code, http.StatusForbidden, viewerCreateResp.Body.String())
	}

	editorCreateReq := httptest.NewRequest(http.MethodPost, "/api/v1/data-pools", bytes.NewReader(createBody))
	editorCreateReq.Header.Set("Content-Type", "application/json")
	editorCreateReq.Header.Set("X-Auth-Request-User", "editor-user")
	editorCreateReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleEditor)
	editorCreateResp := httptest.NewRecorder()
	server.mux.ServeHTTP(editorCreateResp, editorCreateReq)
	if editorCreateResp.Code != http.StatusCreated {
		t.Fatalf("data pool editor create status = %d, want %d body=%s", editorCreateResp.Code, http.StatusCreated, editorCreateResp.Body.String())
	}
}

func TestTrustedHeaderAuthProjectRoleGuardsDataPoolEndpoints(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	project := &storage.Project{ID: uuid.New().String(), Name: "Checkout", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	pool := &storage.SharedDataPool{ID: uuid.New().String(), Name: "Customers", ProjectID: project.ID, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveDataPool(pool); err != nil {
		t.Fatalf("SaveDataPool returned error: %v", err)
	}
	if err := store.SaveDataPoolRow(&storage.SharedDataPoolRow{ID: uuid.New().String(), PoolID: pool.ID, Data: map[string]string{"customer_id": "C-100"}, CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("SaveDataPoolRow returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/data-pools/"+pool.ID, nil)
	getReq.Header.Set("X-Auth-Request-User", "viewer-user")
	getReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleViewer)
	getResp := httptest.NewRecorder()
	server.mux.ServeHTTP(getResp, getReq)
	if getResp.Code != http.StatusOK {
		t.Fatalf("data pool viewer GET status = %d, want %d body=%s", getResp.Code, http.StatusOK, getResp.Body.String())
	}

	rowsReq := httptest.NewRequest(http.MethodGet, "/api/v1/data-pools/"+pool.ID+"/rows", nil)
	rowsReq.Header.Set("X-Auth-Request-User", "viewer-user")
	rowsReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleViewer)
	rowsResp := httptest.NewRecorder()
	server.mux.ServeHTTP(rowsResp, rowsReq)
	if rowsResp.Code != http.StatusOK {
		t.Fatalf("data pool viewer rows GET status = %d, want %d body=%s", rowsResp.Code, http.StatusOK, rowsResp.Body.String())
	}

	viewerRowCreateReq := httptest.NewRequest(http.MethodPost, "/api/v1/data-pools/"+pool.ID+"/rows", bytes.NewReader([]byte(`{"data":{"customer_id":"C-101"}}`)))
	viewerRowCreateReq.Header.Set("Content-Type", "application/json")
	viewerRowCreateReq.Header.Set("X-Auth-Request-User", "viewer-user")
	viewerRowCreateReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleViewer)
	viewerRowCreateResp := httptest.NewRecorder()
	server.mux.ServeHTTP(viewerRowCreateResp, viewerRowCreateReq)
	if viewerRowCreateResp.Code != http.StatusForbidden {
		t.Fatalf("data pool viewer row create status = %d, want %d body=%s", viewerRowCreateResp.Code, http.StatusForbidden, viewerRowCreateResp.Body.String())
	}

	currentPool, err := store.GetDataPool(pool.ID)
	if err != nil {
		t.Fatalf("GetDataPool returned error: %v", err)
	}
	editorUpdateBody, err := json.Marshal(map[string]any{"name": "Customers Updated", "updated_at": currentPool.UpdatedAt})
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	editorUpdateReq := httptest.NewRequest(http.MethodPut, "/api/v1/data-pools/"+pool.ID, bytes.NewReader(editorUpdateBody))
	editorUpdateReq.Header.Set("Content-Type", "application/json")
	editorUpdateReq.Header.Set("X-Auth-Request-User", "editor-user")
	editorUpdateReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleEditor)
	editorUpdateResp := httptest.NewRecorder()
	server.mux.ServeHTTP(editorUpdateResp, editorUpdateReq)
	if editorUpdateResp.Code != http.StatusOK {
		t.Fatalf("data pool editor update status = %d, want %d body=%s", editorUpdateResp.Code, http.StatusOK, editorUpdateResp.Body.String())
	}

	editorRowCreateReq := httptest.NewRequest(http.MethodPost, "/api/v1/data-pools/"+pool.ID+"/rows", bytes.NewReader([]byte(`{"data":{"customer_id":"C-101"}}`)))
	editorRowCreateReq.Header.Set("Content-Type", "application/json")
	editorRowCreateReq.Header.Set("X-Auth-Request-User", "editor-user")
	editorRowCreateReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleEditor)
	editorRowCreateResp := httptest.NewRecorder()
	server.mux.ServeHTTP(editorRowCreateResp, editorRowCreateReq)
	if editorRowCreateResp.Code != http.StatusCreated {
		t.Fatalf("data pool editor row create status = %d, want %d body=%s", editorRowCreateResp.Code, http.StatusCreated, editorRowCreateResp.Body.String())
	}

	editorDeleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/data-pools/"+pool.ID, nil)
	editorDeleteReq.Header.Set("X-Auth-Request-User", "editor-user")
	editorDeleteReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleEditor)
	editorDeleteResp := httptest.NewRecorder()
	server.mux.ServeHTTP(editorDeleteResp, editorDeleteReq)
	if editorDeleteResp.Code != http.StatusOK {
		t.Fatalf("data pool editor delete status = %d, want %d body=%s", editorDeleteResp.Code, http.StatusOK, editorDeleteResp.Body.String())
	}
}

func TestTrustedHeaderAuthProjectRoleGuardsStoredTestEndpoints(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	project := &storage.Project{ID: uuid.New().String(), Name: "Project One", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	folder := &storage.Folder{ID: uuid.New().String(), ProjectID: project.ID, Name: "Results", Category: "results", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveFolder(folder); err != nil {
		t.Fatalf("SaveFolder returned error: %v", err)
	}
	testRecord := &storage.TestRecord{ID: uuid.New().String(), Name: "Run One", Status: "QUEUED", ProjectID: project.ID, StartTime: time.Now()}
	if err := store.SaveTest(testRecord); err != nil {
		t.Fatalf("SaveTest returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testRecord.ID, nil)
	getReq.Header.Set("X-Auth-Request-User", "viewer-user")
	getReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleViewer)
	getResp := httptest.NewRecorder()
	server.mux.ServeHTTP(getResp, getReq)
	if getResp.Code != http.StatusOK {
		t.Fatalf("test viewer GET status = %d, want %d body=%s", getResp.Code, http.StatusOK, getResp.Body.String())
	}

	manifestReq := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testRecord.ID+"/manifest", nil)
	manifestReq.Header.Set("X-Auth-Request-User", "viewer-user")
	manifestReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleViewer)
	manifestResp := httptest.NewRecorder()
	server.mux.ServeHTTP(manifestResp, manifestReq)
	if manifestResp.Code != http.StatusOK {
		t.Fatalf("test viewer manifest status = %d, want %d body=%s", manifestResp.Code, http.StatusOK, manifestResp.Body.String())
	}

	moveBody := []byte(`{"folder_id":"` + folder.ID + `"}`)
	viewerMoveReq := httptest.NewRequest(http.MethodPatch, "/api/v1/tests/"+testRecord.ID+"/move", bytes.NewReader(moveBody))
	viewerMoveReq.Header.Set("Content-Type", "application/json")
	viewerMoveReq.Header.Set("X-Auth-Request-User", "viewer-user")
	viewerMoveReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleViewer)
	viewerMoveResp := httptest.NewRecorder()
	server.mux.ServeHTTP(viewerMoveResp, viewerMoveReq)
	if viewerMoveResp.Code != http.StatusForbidden {
		t.Fatalf("test viewer move status = %d, want %d body=%s", viewerMoveResp.Code, http.StatusForbidden, viewerMoveResp.Body.String())
	}

	editorMoveReq := httptest.NewRequest(http.MethodPatch, "/api/v1/tests/"+testRecord.ID+"/move", bytes.NewReader(moveBody))
	editorMoveReq.Header.Set("Content-Type", "application/json")
	editorMoveReq.Header.Set("X-Auth-Request-User", "editor-user")
	editorMoveReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleEditor)
	editorMoveResp := httptest.NewRecorder()
	server.mux.ServeHTTP(editorMoveResp, editorMoveReq)
	if editorMoveResp.Code != http.StatusOK {
		t.Fatalf("test editor move status = %d, want %d body=%s", editorMoveResp.Code, http.StatusOK, editorMoveResp.Body.String())
	}
}

func TestTrustedHeaderAuthScopesTestListAndCreateToProjectRoles(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	project := &storage.Project{ID: uuid.New().String(), Name: "Project One", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	existingScript := &dsl.Script{
		ID:        uuid.New().String(),
		Name:      "Checkout",
		Version:   "1.0.0",
		Protocol:  "http",
		ProjectID: project.ID,
		Config:    dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:   []dsl.Action{{Name: "Open", Type: "http", Method: "GET", Target: "/"}},
	}
	if err := store.SaveScript(existingScript); err != nil {
		t.Fatalf("SaveScript returned error: %v", err)
	}
	testRecord := &storage.TestRecord{ID: uuid.New().String(), Name: "Run One", Status: "QUEUED", ProjectID: project.ID, StartTime: time.Now()}
	if err := store.SaveTest(testRecord); err != nil {
		t.Fatalf("SaveTest returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/tests", nil)
	listReq.Header.Set("X-Auth-Request-User", "viewer-user")
	listReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleViewer)
	listResp := httptest.NewRecorder()
	server.mux.ServeHTTP(listResp, listReq)
	if listResp.Code != http.StatusForbidden {
		t.Fatalf("unfiltered test list status = %d, want %d body=%s", listResp.Code, http.StatusForbidden, listResp.Body.String())
	}

	filteredReq := httptest.NewRequest(http.MethodGet, "/api/v1/tests?project_id="+project.ID, nil)
	filteredReq.Header.Set("X-Auth-Request-User", "viewer-user")
	filteredReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleViewer)
	filteredResp := httptest.NewRecorder()
	server.mux.ServeHTTP(filteredResp, filteredReq)
	if filteredResp.Code != http.StatusOK {
		t.Fatalf("filtered test list status = %d, want %d body=%s", filteredResp.Code, http.StatusOK, filteredResp.Body.String())
	}

	createBody := []byte(`{"project_id":"` + project.ID + `","name":"Created","duration":"30s","groups":[{"name":"buyers","script_id":"` + existingScript.ID + `","vusers":1}]}`)
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/tests", bytes.NewReader(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	createReq.Header.Set("X-Auth-Request-User", "editor-user")
	createReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleEditor)
	createResp := httptest.NewRecorder()
	server.mux.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("project editor test create status = %d, want %d body=%s", createResp.Code, http.StatusCreated, createResp.Body.String())
	}

	estimateReq := httptest.NewRequest(http.MethodPost, "/api/v1/tests/estimate", bytes.NewReader(createBody))
	estimateReq.Header.Set("Content-Type", "application/json")
	estimateReq.Header.Set("X-Auth-Request-User", "viewer-user")
	estimateReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleViewer)
	estimateResp := httptest.NewRecorder()
	server.mux.ServeHTTP(estimateResp, estimateReq)
	if estimateResp.Code != http.StatusForbidden {
		t.Fatalf("project viewer estimate status = %d, want %d body=%s", estimateResp.Code, http.StatusForbidden, estimateResp.Body.String())
	}

	preflightEditorReq := httptest.NewRequest(http.MethodPost, "/api/v1/tests/preflight", bytes.NewReader(createBody))
	preflightEditorReq.Header.Set("Content-Type", "application/json")
	preflightEditorReq.Header.Set("X-Auth-Request-User", "editor-user")
	preflightEditorReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleEditor)
	preflightEditorResp := httptest.NewRecorder()
	server.mux.ServeHTTP(preflightEditorResp, preflightEditorReq)
	if preflightEditorResp.Code != http.StatusOK {
		t.Fatalf("project editor preflight status = %d, want %d body=%s", preflightEditorResp.Code, http.StatusOK, preflightEditorResp.Body.String())
	}

	preflightViewerReq := httptest.NewRequest(http.MethodPost, "/api/v1/tests/preflight", bytes.NewReader(createBody))
	preflightViewerReq.Header.Set("Content-Type", "application/json")
	preflightViewerReq.Header.Set("X-Auth-Request-User", "viewer-user")
	preflightViewerReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleViewer)
	preflightViewerResp := httptest.NewRecorder()
	server.mux.ServeHTTP(preflightViewerResp, preflightViewerReq)
	if preflightViewerResp.Code != http.StatusForbidden {
		t.Fatalf("project viewer preflight status = %d, want %d body=%s", preflightViewerResp.Code, http.StatusForbidden, preflightViewerResp.Body.String())
	}
}

func TestTrustedHeaderAuthProjectRoleGuardsScenarioEndpoints(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	project := &storage.Project{ID: uuid.New().String(), Name: "Project One", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	script := &dsl.Script{
		ID:        uuid.New().String(),
		Name:      "Checkout",
		Version:   "1.0.0",
		Protocol:  "http",
		ProjectID: project.ID,
		Config:    dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:   []dsl.Action{{Name: "Open", Type: "http", Method: "GET", Target: "/"}},
	}
	if err := store.SaveScript(script); err != nil {
		t.Fatalf("SaveScript returned error: %v", err)
	}
	scenario := &storage.Scenario{ID: uuid.New().String(), Name: "Smoke", Duration: "1m", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveScenario(scenario, []storage.ScenarioScript{{ScenarioID: scenario.ID, Order: 0, ScriptID: script.ID, VUsers: 5}}); err != nil {
		t.Fatalf("SaveScenario returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/scenarios/"+scenario.ID, nil)
	getReq.Header.Set("X-Auth-Request-User", "viewer-user")
	getReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleViewer)
	getResp := httptest.NewRecorder()
	server.mux.ServeHTTP(getResp, getReq)
	if getResp.Code != http.StatusOK {
		t.Fatalf("scenario viewer GET status = %d, want %d body=%s", getResp.Code, http.StatusOK, getResp.Body.String())
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/api/v1/scenarios/"+scenario.ID, bytes.NewReader([]byte(`{"name":"Smoke","duration":"1m","groups":[{"script_id":"`+script.ID+`","vusers":5}]}`)))
	updateReq.Header.Set("Content-Type", "application/json")
	updateReq.Header.Set("X-Auth-Request-User", "viewer-user")
	updateReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleViewer)
	updateResp := httptest.NewRecorder()
	server.mux.ServeHTTP(updateResp, updateReq)
	if updateResp.Code != http.StatusForbidden {
		t.Fatalf("scenario viewer PUT status = %d, want %d body=%s", updateResp.Code, http.StatusForbidden, updateResp.Body.String())
	}

	dataPoolsReq := httptest.NewRequest(http.MethodGet, "/api/v1/scenarios/"+scenario.ID+"/data-pools", nil)
	dataPoolsReq.Header.Set("X-Auth-Request-User", "viewer-user")
	dataPoolsReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleViewer)
	dataPoolsResp := httptest.NewRecorder()
	server.mux.ServeHTTP(dataPoolsResp, dataPoolsReq)
	if dataPoolsResp.Code != http.StatusOK {
		t.Fatalf("scenario viewer data pools GET status = %d, want %d body=%s", dataPoolsResp.Code, http.StatusOK, dataPoolsResp.Body.String())
	}

	resultsReq := httptest.NewRequest(http.MethodGet, "/api/v1/scenarios/"+scenario.ID+"/results", nil)
	resultsReq.Header.Set("X-Auth-Request-User", "viewer-user")
	resultsReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleViewer)
	resultsResp := httptest.NewRecorder()
	server.mux.ServeHTTP(resultsResp, resultsReq)
	if resultsResp.Code != http.StatusOK {
		t.Fatalf("scenario viewer results GET status = %d, want %d body=%s", resultsResp.Code, http.StatusOK, resultsResp.Body.String())
	}

	scheduleBody := []byte(`{"name":"Nightly Smoke","cron_expr":"0 0 * * *","timezone":"UTC"}`)
	viewerScheduleReq := httptest.NewRequest(http.MethodPost, "/api/v1/scenarios/"+scenario.ID+"/schedules", bytes.NewReader(scheduleBody))
	viewerScheduleReq.Header.Set("Content-Type", "application/json")
	viewerScheduleReq.Header.Set("X-Auth-Request-User", "viewer-user")
	viewerScheduleReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleViewer)
	viewerScheduleResp := httptest.NewRecorder()
	server.mux.ServeHTTP(viewerScheduleResp, viewerScheduleReq)
	if viewerScheduleResp.Code != http.StatusForbidden {
		t.Fatalf("scenario viewer schedule POST status = %d, want %d body=%s", viewerScheduleResp.Code, http.StatusForbidden, viewerScheduleResp.Body.String())
	}

	editorScheduleReq := httptest.NewRequest(http.MethodPost, "/api/v1/scenarios/"+scenario.ID+"/schedules", bytes.NewReader(scheduleBody))
	editorScheduleReq.Header.Set("Content-Type", "application/json")
	editorScheduleReq.Header.Set("X-Auth-Request-User", "editor-user")
	editorScheduleReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleEditor)
	editorScheduleResp := httptest.NewRecorder()
	server.mux.ServeHTTP(editorScheduleResp, editorScheduleReq)
	if editorScheduleResp.Code != http.StatusCreated {
		t.Fatalf("scenario editor schedule POST status = %d, want %d body=%s", editorScheduleResp.Code, http.StatusCreated, editorScheduleResp.Body.String())
	}

	adminUpdateReq := httptest.NewRequest(http.MethodPut, "/api/v1/scenarios/"+scenario.ID, bytes.NewReader([]byte(`{"name":"Smoke Renamed","duration":"1m","groups":[{"script_id":"`+script.ID+`","vusers":5}]}`)))
	adminUpdateReq.Header.Set("Content-Type", "application/json")
	adminUpdateReq.Header.Set("X-Auth-Request-User", "editor-user")
	adminUpdateReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleEditor)
	adminUpdateResp := httptest.NewRecorder()
	server.mux.ServeHTTP(adminUpdateResp, adminUpdateReq)
	if adminUpdateResp.Code != http.StatusOK {
		t.Fatalf("scenario editor PUT status = %d, want %d body=%s", adminUpdateResp.Code, http.StatusOK, adminUpdateResp.Body.String())
	}
}

func TestTrustedHeaderAuthScopesScenarioListAndCreateToProjectRoles(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	project := &storage.Project{ID: uuid.New().String(), Name: "Project One", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	script := &dsl.Script{
		ID:        uuid.New().String(),
		Name:      "Checkout",
		Version:   "1.0.0",
		Protocol:  "http",
		ProjectID: project.ID,
		Config:    dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:   []dsl.Action{{Name: "Open", Type: "http", Method: "GET", Target: "/"}},
	}
	if err := store.SaveScript(script); err != nil {
		t.Fatalf("SaveScript returned error: %v", err)
	}
	scenario := &storage.Scenario{ID: uuid.New().String(), Name: "Smoke", Duration: "1m", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveScenario(scenario, []storage.ScenarioScript{{ScenarioID: scenario.ID, Order: 0, ScriptID: script.ID, VUsers: 5}}); err != nil {
		t.Fatalf("SaveScenario returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/scenarios", nil)
	listReq.Header.Set("X-Auth-Request-User", "viewer-user")
	listReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleViewer)
	listResp := httptest.NewRecorder()
	server.mux.ServeHTTP(listResp, listReq)
	if listResp.Code != http.StatusForbidden {
		t.Fatalf("unfiltered scenario list status = %d, want %d body=%s", listResp.Code, http.StatusForbidden, listResp.Body.String())
	}

	filteredReq := httptest.NewRequest(http.MethodGet, "/api/v1/scenarios?project_id="+project.ID, nil)
	filteredReq.Header.Set("X-Auth-Request-User", "viewer-user")
	filteredReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleViewer)
	filteredResp := httptest.NewRecorder()
	server.mux.ServeHTTP(filteredResp, filteredReq)
	if filteredResp.Code != http.StatusOK {
		t.Fatalf("filtered scenario list status = %d, want %d body=%s", filteredResp.Code, http.StatusOK, filteredResp.Body.String())
	}

	createBody := []byte(`{"name":"Created","duration":"30s","groups":[{"name":"buyers","script_id":"` + script.ID + `","vusers":1}]}`)
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/scenarios", bytes.NewReader(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	createReq.Header.Set("X-Auth-Request-User", "editor-user")
	createReq.Header.Set("X-APEXLoad-Project-Roles", project.ID+"="+RoleEditor)
	createResp := httptest.NewRecorder()
	server.mux.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("project editor scenario create status = %d, want %d body=%s", createResp.Code, http.StatusCreated, createResp.Body.String())
	}
}

func TestTrustedHeaderAuthRejectsCrossProjectTestMoves(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	projectOne := &storage.Project{ID: uuid.New().String(), Name: "Project One", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	projectTwo := &storage.Project{ID: uuid.New().String(), Name: "Project Two", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(projectOne); err != nil {
		t.Fatalf("SaveProject(projectOne) returned error: %v", err)
	}
	if err := store.SaveProject(projectTwo); err != nil {
		t.Fatalf("SaveProject(projectTwo) returned error: %v", err)
	}
	destinationFolder := &storage.Folder{ID: uuid.New().String(), ProjectID: projectTwo.ID, Name: "Other Results", Category: "results", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveFolder(destinationFolder); err != nil {
		t.Fatalf("SaveFolder returned error: %v", err)
	}
	testRecord := &storage.TestRecord{ID: uuid.New().String(), Name: "Run One", Status: "QUEUED", ProjectID: projectOne.ID, StartTime: time.Now()}
	if err := store.SaveTest(testRecord); err != nil {
		t.Fatalf("SaveTest returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	body := []byte(`{"folder_id":"` + destinationFolder.ID + `"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/tests/"+testRecord.ID+"/move", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Auth-Request-User", "editor-user")
	req.Header.Set("X-APEXLoad-Project-Roles", projectOne.ID+"="+RoleEditor)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("cross-project test move status = %d, want %d body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
}

func TestScopedResolverNotFoundHidesResourceExistence(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	scopedReq := httptest.NewRequest(http.MethodGet, "/api/v1/tests/missing-test-id", nil)
	scopedReq.Header.Set("X-Auth-Request-User", "scoped-viewer")
	scopedReq.Header.Set("X-APEXLoad-Project-Roles", "project-a="+RoleViewer)
	scopedResp := httptest.NewRecorder()
	server.mux.ServeHTTP(scopedResp, scopedReq)
	if scopedResp.Code != http.StatusForbidden {
		t.Fatalf("scoped missing test status = %d, want %d body=%s", scopedResp.Code, http.StatusForbidden, scopedResp.Body.String())
	}
	if strings.Contains(strings.ToLower(scopedResp.Body.String()), "not found") {
		t.Fatalf("scoped missing test body leaked not-found detail: %s", scopedResp.Body.String())
	}

	globalReq := httptest.NewRequest(http.MethodGet, "/api/v1/tests/missing-test-id", nil)
	globalReq.Header.Set("X-Auth-Request-User", "global-viewer")
	globalReq.Header.Set("X-APEXLoad-Roles", RoleViewer)
	globalResp := httptest.NewRecorder()
	server.mux.ServeHTTP(globalResp, globalReq)
	if globalResp.Code != http.StatusNotFound {
		t.Fatalf("global missing test status = %d, want %d body=%s", globalResp.Code, http.StatusNotFound, globalResp.Body.String())
	}
}

func TestProjectScopedMiddlewareAuditsDenials(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	if err := store.SaveUser(&storage.User{
		ID:        "user-limited",
		Subject:   "limited-user",
		Active:    true,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveUser returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/recorder?project_id=project-a", nil)
	req.Header.Set("X-Auth-Request-User", "limited-user")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("recorder scoped denial status = %d, want %d body=%s", resp.Code, http.StatusForbidden, resp.Body.String())
	}

	events, err := store.ListAuditEvents(storage.AuditEventFilter{Action: auditActionAccessDenied, Status: "failure", Limit: 10})
	if err != nil {
		t.Fatalf("ListAuditEvents returned error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("audit denial events = %#v, want one", events)
	}
	if events[0].ActorSubject != "limited-user" || events[0].ResourceID != "GET /api/v1/recorder" {
		t.Fatalf("audit denial event = %#v, want limited user and recorder route", events[0])
	}
}

func enableTrustedHeaderAuth(t *testing.T, server *Server) SystemSettings {
	t.Helper()
	settings := server.runner.GetSystemSettings()
	settings.Auth.Mode = authModeTrustedHeaders
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}
	return settings
}

func scopedLibraryRequest(t *testing.T, method string, path string, body any) *http.Request {
	t.Helper()
	if body == nil {
		return httptest.NewRequest(method, path, nil)
	}
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal scoped library request: %v", err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// minimalEmptyZIP returns the 22-byte end-of-central-directory record that makes
// a valid, empty ZIP archive.
func minimalEmptyZIP() []byte {
	zipBytes := make([]byte, 22)
	copy(zipBytes, []byte{'P', 'K', 0x05, 0x06})
	return zipBytes
}

package api

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestHandleGetAuthStatusReturnsDevByDefault(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var payload AuthStatusResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal auth status: %v", err)
	}
	if payload.Enabled {
		t.Fatal("expected auth to be bypassed by default")
	}
	if payload.Authenticated {
		t.Fatal("expected no authenticated user when auth is in dev mode")
	}
	if payload.Mode != authModeDev {
		t.Fatalf("mode = %q, want %q", payload.Mode, authModeDev)
	}
}

func TestHandleGetAuthStatusReturnsDevUserPreview(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.Header.Set(devViewHeader, devViewModeUser)
	req.Header.Set(devProjectIDHeader, "project-a")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var payload AuthStatusResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal auth status: %v", err)
	}
	if payload.DevViewMode != devViewModeUser || !payload.Simulated {
		t.Fatalf("dev preview = (%q, %v), want user simulated", payload.DevViewMode, payload.Simulated)
	}
	if !payload.Authenticated || payload.User == nil {
		t.Fatalf("expected synthetic authenticated user, got %#v", payload)
	}
	if payload.User.Subject != "dev-user" || payload.User.DisplayName != "Dev User Preview" {
		t.Fatalf("user = %#v, want dev-user preview", payload.User)
	}
	if got := payload.User.GlobalRoles; len(got) != 1 || got[0] != RoleViewer {
		t.Fatalf("global roles = %#v, want viewer", got)
	}
	if got := payload.User.ProjectRoles["project-a"]; len(got) != 1 || got[0] != RoleEditor {
		t.Fatalf("project-a roles = %#v, want editor", got)
	}
}

func TestHandleGetAuthStatusDevUserPreviewWithoutProjectDoesNotGrantDefaultEditor(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.Header.Set(devViewHeader, devViewModeUser)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload AuthStatusResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal auth status: %v", err)
	}
	if payload.User == nil {
		t.Fatalf("expected synthetic dev user, got %#v", payload)
	}
	if len(payload.User.ProjectRoles) != 0 {
		t.Fatalf("project roles = %#v, want none without %s", payload.User.ProjectRoles, devProjectIDHeader)
	}
}

func TestHandleGetAuthStatusIgnoresDevPreviewOutsideDevMode(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.Header.Set(devViewHeader, devViewModeUser)
	req.Header.Set(devProjectIDHeader, "project-a")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var payload AuthStatusResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal auth status: %v", err)
	}
	if payload.Simulated || payload.DevViewMode != "" {
		t.Fatalf("dev preview should be ignored outside dev mode, got %#v", payload)
	}
	if payload.Authenticated || payload.User != nil {
		t.Fatalf("expected unauthenticated trusted-header status without identity, got %#v", payload)
	}
}

func TestHandleGetAuthStatusReturnsUnauthenticatedWhenTrustedHeadersEnabled(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	settings := enableTrustedHeaderAuth(t, server)
	settings.Auth.ProviderName = "Microsoft Entra ID"
	settings.Auth.LoginURL = "https://login.example.test/oauth2/start"
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var payload AuthStatusResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal auth status: %v", err)
	}
	if !payload.Enabled {
		t.Fatal("expected auth to be enabled")
	}
	if payload.ProviderName != "Microsoft Entra ID" {
		t.Fatalf("provider name = %q, want Microsoft Entra ID", payload.ProviderName)
	}
	if payload.LoginURL != "https://login.example.test/oauth2/start" {
		t.Fatalf("login url = %q, want configured login url", payload.LoginURL)
	}
	if payload.Authenticated {
		t.Fatal("expected request without headers to remain unauthenticated")
	}
	if payload.User != nil {
		t.Fatalf("expected no user, got %#v", payload.User)
	}
	if payload.Mode != authModeTrustedHeaders {
		t.Fatalf("mode = %q, want %q", payload.Mode, authModeTrustedHeaders)
	}
}

func TestHandleGetAuthStatusReturnsTrustedHeaderIdentity(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.Header.Set("X-Auth-Request-User", "alice")
	req.Header.Set("X-Auth-Request-Name", "Alice Admin")
	req.Header.Set("X-Auth-Request-Email", "alice@example.com")
	req.Header.Set("X-APEXLoad-Roles", RoleSystemAdmin+","+RoleViewer)
	req.Header.Set("X-APEXLoad-Project-Roles", "project-a="+RoleEditor+"|"+RoleViewer+";project-b="+RoleProjectAdmin)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var payload AuthStatusResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal auth status: %v", err)
	}
	if !payload.Authenticated || payload.User == nil {
		t.Fatalf("expected authenticated user, got %#v", payload)
	}
	if payload.User.Subject != "alice" {
		t.Fatalf("subject = %q, want alice", payload.User.Subject)
	}
	if payload.User.DisplayName != "Alice Admin" {
		t.Fatalf("display name = %q, want Alice Admin", payload.User.DisplayName)
	}
	if payload.User.Email != "alice@example.com" {
		t.Fatalf("email = %q, want alice@example.com", payload.User.Email)
	}
	if len(payload.User.GlobalRoles) != 2 || payload.User.GlobalRoles[0] != RoleSystemAdmin || payload.User.GlobalRoles[1] != RoleViewer {
		t.Fatalf("global roles = %#v, want [system_admin viewer]", payload.User.GlobalRoles)
	}
	if got := payload.User.ProjectRoles["project-a"]; len(got) != 2 || got[0] != RoleEditor || got[1] != RoleViewer {
		t.Fatalf("project-a roles = %#v, want [editor viewer]", got)
	}
	if got := payload.User.ProjectRoles["project-b"]; len(got) != 1 || got[0] != RoleProjectAdmin {
		t.Fatalf("project-b roles = %#v, want [project_admin]", got)
	}
}

func TestHandleGetAuthDiagnosticsReturnsTrustedHeaderReadiness(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	settings := enableTrustedHeaderAuth(t, server)
	settings.Auth.ProviderName = "Microsoft Entra ID"
	settings.Auth.LoginURL = "https://login.example.test/oauth2/start"
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/diagnostics", nil)
	req.Header.Set("X-Auth-Request-User", "alice")
	req.Header.Set("X-APEXLoad-Roles", RoleSystemAdmin)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var payload AuthDiagnosticsResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal auth diagnostics: %v", err)
	}
	if !payload.Ready {
		t.Fatalf("expected trusted header diagnostics to be ready, got %#v", payload)
	}
	if payload.ProviderModel != "upstream_gateway" {
		t.Fatalf("provider model = %q, want upstream_gateway", payload.ProviderModel)
	}
	if payload.Mode != authModeTrustedHeaders {
		t.Fatalf("mode = %q, want %q", payload.Mode, authModeTrustedHeaders)
	}
	if len(payload.Checks) < 4 {
		t.Fatalf("checks = %#v, want trusted header diagnostics", payload.Checks)
	}
	if payload.Checks[0].Status != "pass" || payload.Checks[1].Status != "pass" {
		t.Fatalf("first checks = %#v, want configured subject and role header checks", payload.Checks[:2])
	}
}

// configureOIDCDiagnosticsServer puts the server in OIDC mode with a usable
// session secret (so an admin session cookie validates) but an unset client
// secret env var (so readiness still reports "not ready").
func configureOIDCDiagnosticsServer(t *testing.T) *Server {
	t.Helper()
	t.Setenv("APEXLOAD_AUTH_SESSION_SECRET", "test-session-secret-0123456789abcdef")
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	settings := server.runner.GetSystemSettings()
	settings.Auth.Mode = authModeOIDC
	settings.Auth.ProviderName = "Example OIDC"
	settings.Auth.IssuerURL = "https://login.example.test/tenant/v2.0"
	settings.Auth.ClientID = "apexload-dashboard"
	settings.Auth.ClientSecretEnvVar = "APEXLOAD_OIDC_CLIENT_SECRET"
	settings.Auth.RedirectURL = "https://apexload.example.test/api/v1/auth/callback"
	settings.Auth.SessionSecretEnvVar = "APEXLOAD_AUTH_SESSION_SECRET"
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}
	return server
}

// oidcAdminSessionCookie mints a signed OIDC session cookie whose token carries
// the system-admin global role. On an empty store the bootstrap path returns the
// upstream session user as-is, so this authenticates as an admin.
func oidcAdminSessionCookie(t *testing.T, server *Server, roles ...string) *http.Cookie {
	t.Helper()
	settings := normalizeAuthSettings(server.runner.GetSystemSettings().Auth)
	secret := strings.TrimSpace(getenvOrFatal(t, settings.SessionSecretEnvVar))
	encoded, err := encodeSignedCookieValue(oidcSessionToken{
		Subject:     "sysadmin",
		GlobalRoles: roles,
		IssuedAt:    time.Now().UTC(),
		ExpiresAt:   time.Now().UTC().Add(time.Hour),
	}, secret)
	if err != nil {
		t.Fatalf("encodeSignedCookieValue returned error: %v", err)
	}
	return &http.Cookie{Name: oidcSessionCookieName, Value: encoded, Path: "/"}
}

func TestHandleGetAuthDiagnosticsFailsOIDCReadinessWhenSecretsMissing(t *testing.T) {
	server := configureOIDCDiagnosticsServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/diagnostics", nil)
	req.AddCookie(oidcAdminSessionCookie(t, server, RoleSystemAdmin))
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var payload AuthDiagnosticsResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal auth diagnostics: %v", err)
	}
	if payload.Ready {
		t.Fatalf("expected oidc diagnostics to report missing env vars, got %#v", payload)
	}
	if payload.ProviderModel != "native_oidc" {
		t.Fatalf("provider model = %q, want native_oidc", payload.ProviderModel)
	}
}

// TestHandleGetAuthDiagnosticsRequiresAdminInOIDCMode locks in that the
// diagnostics endpoint is gated behind a system admin in OIDC mode: anonymous
// callers get 401, non-admins get 403, and an admin session gets 200. Previously
// only trusted-headers mode was gated, leaking config + an outbound discovery
// probe to anonymous OIDC callers.
func TestHandleGetAuthDiagnosticsRequiresAdminInOIDCMode(t *testing.T) {
	server := configureOIDCDiagnosticsServer(t)

	anon := httptest.NewRequest(http.MethodGet, "/api/v1/auth/diagnostics", nil)
	anonResp := httptest.NewRecorder()
	server.mux.ServeHTTP(anonResp, anon)
	if anonResp.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d, want %d body=%s", anonResp.Code, http.StatusUnauthorized, anonResp.Body.String())
	}

	nonAdmin := httptest.NewRequest(http.MethodGet, "/api/v1/auth/diagnostics", nil)
	nonAdmin.AddCookie(oidcAdminSessionCookie(t, server, RoleViewer))
	nonAdminResp := httptest.NewRecorder()
	server.mux.ServeHTTP(nonAdminResp, nonAdmin)
	if nonAdminResp.Code != http.StatusForbidden {
		t.Fatalf("non-admin status = %d, want %d body=%s", nonAdminResp.Code, http.StatusForbidden, nonAdminResp.Body.String())
	}

	admin := httptest.NewRequest(http.MethodGet, "/api/v1/auth/diagnostics", nil)
	admin.AddCookie(oidcAdminSessionCookie(t, server, RoleSystemAdmin))
	adminResp := httptest.NewRecorder()
	server.mux.ServeHTTP(adminResp, admin)
	if adminResp.Code != http.StatusOK {
		t.Fatalf("admin status = %d, want %d body=%s", adminResp.Code, http.StatusOK, adminResp.Body.String())
	}
}

func TestHandleGetAuthRoleCatalogReturnsBuiltInRoles(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/roles", nil)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var payload AuthRoleCatalogResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal auth role catalog: %v", err)
	}
	if payload.ProviderModel != "upstream_gateway" {
		t.Fatalf("provider model = %q, want upstream_gateway", payload.ProviderModel)
	}
	if len(payload.Roles) != 4 {
		t.Fatalf("role count = %d, want 4", len(payload.Roles))
	}
	if payload.Roles[0].ID != RoleSystemAdmin || payload.Roles[0].Name != "System Admin" {
		t.Fatalf("first role = %#v, want system admin descriptor", payload.Roles[0])
	}
	if len(payload.Roles[0].Capabilities) == 0 {
		t.Fatalf("expected system admin capabilities, got %#v", payload.Roles[0].Capabilities)
	}
	if payload.Roles[3].ID != RoleViewer {
		t.Fatalf("last role = %#v, want viewer descriptor", payload.Roles[3])
	}
}

func TestHandleGetAuthAccessMatrixReturnsResourcePolicies(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/access-matrix", nil)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var payload AuthAccessMatrixResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal auth access matrix: %v", err)
	}
	if payload.Version != "trusted_headers_v1" {
		t.Fatalf("version = %q, want trusted_headers_v1", payload.Version)
	}
	if len(payload.Resources) < 6 {
		t.Fatalf("resource count = %d, want >= 6", len(payload.Resources))
	}
	if payload.Resources[0].ID != "system_configuration" {
		t.Fatalf("first resource = %#v, want system configuration", payload.Resources[0])
	}
	if len(payload.Resources[0].Operations) != 2 {
		t.Fatalf("system configuration operations = %#v, want 2 operations", payload.Resources[0].Operations)
	}
	if payload.Resources[0].Operations[1].GlobalRoles[0] != RoleSystemAdmin {
		t.Fatalf("manage system roles = %#v, want system admin", payload.Resources[0].Operations[1].GlobalRoles)
	}
	projectsResource := findAuthAccessResource(payload.Resources, "projects_and_folders")
	if projectsResource == nil || projectsResource.Operations[1].ProjectRoles[0] != RoleProjectAdmin {
		t.Fatalf("project governance resource = %#v, want project admin project scope", projectsResource)
	}
	repositoryResource := findAuthAccessResource(payload.Resources, "domains_and_application_repository")
	if repositoryResource == nil || repositoryResource.Operations[0].ID != "view_domains" || repositoryResource.Operations[4].ID != "manage_repository_projects" {
		t.Fatalf("repository resource = %#v, want domain and repository operations", repositoryResource)
	}
	identityAndAudit := payload.Resources[len(payload.Resources)-1]
	if identityAndAudit.ID != "identity_and_audit" {
		t.Fatalf("last resource = %#v, want identity_and_audit", identityAndAudit)
	}
	if len(identityAndAudit.Operations) != 6 || identityAndAudit.Operations[0].ID != "view_auth_diagnostics" || identityAndAudit.Operations[2].ID != "view_user_activity" || identityAndAudit.Operations[3].ID != "manage_users" || identityAndAudit.Operations[4].ID != "manage_user_bindings" {
		t.Fatalf("identity and audit operations = %#v, want diagnostics, audit, user, activity, and service account operations", identityAndAudit.Operations)
	}
	if got := identityAndAudit.Operations[3].GlobalRoles; len(got) != 1 || got[0] != RoleSystemAdmin {
		t.Fatalf("manage_users roles = %#v, want system admin only", got)
	}
	if got := identityAndAudit.Operations[4].ProjectRoles; len(got) != 1 || got[0] != RoleProjectAdmin {
		t.Fatalf("manage_user_bindings project roles = %#v, want project admin", got)
	}
}

func findAuthAccessResource(resources []AuthAccessResource, id string) *AuthAccessResource {
	for i := range resources {
		if resources[i].ID == id {
			return &resources[i]
		}
	}
	return nil
}

func TestHandleGetAuthEffectiveAccessReturnsOpenAccessWhenDevModeBypassesAuth(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/effective-access", nil)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var payload AuthEffectiveAccessResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal auth effective access: %v", err)
	}
	if payload.Enabled {
		t.Fatal("expected dev mode to keep upstream auth disabled")
	}
	if payload.Authenticated {
		t.Fatal("expected no authenticated user in dev mode")
	}
	if !payload.Resources[0].Operations[0].Allowed || payload.Resources[0].Operations[0].GrantSource != "dev_mode" {
		t.Fatalf("first operation = %#v, want allowed via dev mode", payload.Resources[0].Operations[0])
	}
}

func TestHandleGetAuthEffectiveAccessReturnsDevUserPreviewGrants(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/effective-access?project_id=project-a", nil)
	req.Header.Set(devViewHeader, devViewModeUser)
	req.Header.Set(devProjectIDHeader, "project-a")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var payload AuthEffectiveAccessResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal auth effective access: %v", err)
	}
	if payload.DevViewMode != devViewModeUser || !payload.Simulated || !payload.Authenticated {
		t.Fatalf("dev preview metadata = %#v, want authenticated simulated user preview", payload)
	}
	if allowed := effectiveAccessOperationAllowed(payload.Resources, "view_tests"); !allowed {
		t.Fatal("expected dev user preview to allow view_tests")
	}
	if allowed := effectiveAccessOperationAllowed(payload.Resources, "manage_tests"); !allowed {
		t.Fatal("expected dev user preview to allow manage_tests")
	}
	if allowed := effectiveAccessOperationAllowed(payload.Resources, "manage_scripts"); !allowed {
		t.Fatal("expected dev user preview to allow manage_scripts")
	}
	if allowed := effectiveAccessOperationAllowed(payload.Resources, "manage_system"); allowed {
		t.Fatal("expected dev user preview to deny manage_system")
	}
	if allowed := effectiveAccessOperationAllowed(payload.Resources, "manage_projects"); allowed {
		t.Fatal("expected dev user preview to deny manage_projects")
	}
}

func TestHandleGetAuthEffectiveAccessEvaluatesProjectRoles(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/effective-access?project_id=project-a", nil)
	req.Header.Set("X-Auth-Request-User", "editor-user")
	req.Header.Set("X-APEXLoad-Project-Roles", "project-a="+RoleEditor)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var payload AuthEffectiveAccessResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal auth effective access: %v", err)
	}
	if !payload.Enabled || !payload.Authenticated {
		t.Fatalf("expected enabled authenticated payload, got %#v", payload)
	}
	if payload.ProjectID != "project-a" {
		t.Fatalf("project id = %q, want project-a", payload.ProjectID)
	}
	var manageTests AuthEffectiveAccessOperation
	var manageSystem AuthEffectiveAccessOperation
	for _, resource := range payload.Resources {
		for _, operation := range resource.Operations {
			switch operation.ID {
			case "manage_tests":
				manageTests = operation
			case "manage_system":
				manageSystem = operation
			}
		}
	}
	if !manageTests.Allowed || manageTests.GrantSource != "project" {
		t.Fatalf("manage tests = %#v, want allowed via project role", manageTests)
	}
	if manageSystem.Allowed {
		t.Fatalf("manage system = %#v, want denied for project editor", manageSystem)
	}
}

func TestHandleGetAuthEffectiveAccessSplitsUserLifecycleAndBindings(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/effective-access?project_id=project-a", nil)
	req.Header.Set("X-Auth-Request-User", "project-admin")
	req.Header.Set("X-APEXLoad-Project-Roles", "project-a="+RoleProjectAdmin)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload AuthEffectiveAccessResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal auth effective access: %v", err)
	}
	if allowed := effectiveAccessOperationAllowed(payload.Resources, "manage_users"); allowed {
		t.Fatal("expected project admin to be denied manage_users")
	}
	if allowed := effectiveAccessOperationAllowed(payload.Resources, "manage_user_bindings"); !allowed {
		t.Fatal("expected project admin to be allowed manage_user_bindings")
	}
}

func TestHandleServiceAccountCRUD(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	expiresAt := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
	body, _ := json.Marshal(map[string]any{
		"name":                   "Nightly CI",
		"description":            "Runs automation",
		"global_roles":           []string{"system_admin"},
		"project_roles":          map[string][]string{"project-a": {"editor"}},
		"scopes":                 []string{"tests:run", "results:read"},
		"expires_at":             expiresAt.Format(time.RFC3339),
		"rotation_interval_days": 30,
	})
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/service-accounts", bytes.NewReader(body))
	createResp := httptest.NewRecorder()

	server.mux.ServeHTTP(createResp, createReq)

	if createResp.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d body=%s", createResp.Code, http.StatusCreated, createResp.Body.String())
	}
	var created AuthServiceAccountCreateResponse
	if err := json.Unmarshal(createResp.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal service account create: %v", err)
	}
	if created.APIKey == "" || created.ServiceAccount == nil {
		t.Fatalf("create payload = %#v, want service account and api key", created)
	}
	if !created.ServiceAccount.Active {
		t.Fatalf("expected created service account to be active, got %#v", created.ServiceAccount)
	}
	if created.ServiceAccount.GlobalRoles[0] != RoleSystemAdmin {
		t.Fatalf("global roles = %#v, want system_admin", created.ServiceAccount.GlobalRoles)
	}
	if len(created.ServiceAccount.Scopes) != 2 || created.ServiceAccount.Scopes[0] != "tests:run" {
		t.Fatalf("scopes = %#v, want tests:run/results:read", created.ServiceAccount.Scopes)
	}
	if created.ServiceAccount.ExpiresAt == nil || !created.ServiceAccount.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("expires_at = %#v, want %v", created.ServiceAccount.ExpiresAt, expiresAt)
	}
	if created.ServiceAccount.KeyRotatedAt == nil || created.ServiceAccount.RotationIntervalDays != 30 || created.ServiceAccount.RotationDueAt == nil {
		t.Fatalf("rotation metadata = %#v, want key_rotated_at, interval, and due date", created.ServiceAccount)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/service-accounts", nil)
	listResp := httptest.NewRecorder()
	server.mux.ServeHTTP(listResp, listReq)
	if listResp.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d body=%s", listResp.Code, http.StatusOK, listResp.Body.String())
	}
	var listed AuthServiceAccountListResponse
	if err := json.Unmarshal(listResp.Body.Bytes(), &listed); err != nil {
		t.Fatalf("unmarshal service account list: %v", err)
	}
	if listed.Count != 1 || listed.ServiceAccounts[0].ID != created.ServiceAccount.ID {
		t.Fatalf("listed service accounts = %#v, want created account", listed)
	}

	rotateReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/service-accounts/"+created.ServiceAccount.ID+"/rotate", nil)
	rotateResp := httptest.NewRecorder()
	server.mux.ServeHTTP(rotateResp, rotateReq)
	if rotateResp.Code != http.StatusOK {
		t.Fatalf("rotate status = %d, want %d body=%s", rotateResp.Code, http.StatusOK, rotateResp.Body.String())
	}
	var rotated AuthServiceAccountCreateResponse
	if err := json.Unmarshal(rotateResp.Body.Bytes(), &rotated); err != nil {
		t.Fatalf("unmarshal service account rotate: %v", err)
	}
	if rotated.APIKey == "" || rotated.APIKey == created.APIKey {
		t.Fatalf("rotate payload = %#v, want new api key", rotated)
	}
	if rotated.ServiceAccount == nil || rotated.ServiceAccount.KeyRotatedAt == nil || !rotated.ServiceAccount.KeyRotatedAt.After(*created.ServiceAccount.KeyRotatedAt) {
		t.Fatalf("rotated metadata = %#v, want newer key_rotated_at", rotated.ServiceAccount)
	}

	updateReq := httptest.NewRequest(http.MethodPatch, "/api/v1/auth/service-accounts/"+created.ServiceAccount.ID, bytes.NewReader([]byte(`{"active":false}`)))
	updateResp := httptest.NewRecorder()
	server.mux.ServeHTTP(updateResp, updateReq)
	if updateResp.Code != http.StatusOK {
		t.Fatalf("update status = %d, want %d body=%s", updateResp.Code, http.StatusOK, updateResp.Body.String())
	}
	var updated storage.ServiceAccount
	if err := json.Unmarshal(updateResp.Body.Bytes(), &updated); err != nil {
		t.Fatalf("unmarshal service account update: %v", err)
	}
	if updated.Active {
		t.Fatalf("expected service account to be disabled, got %#v", updated)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/auth/service-accounts/"+created.ServiceAccount.ID, nil)
	deleteResp := httptest.NewRecorder()
	server.mux.ServeHTTP(deleteResp, deleteReq)
	if deleteResp.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want %d body=%s", deleteResp.Code, http.StatusOK, deleteResp.Body.String())
	}
}

func TestHandleGetAuditEventsReturnsRecordedSystemSettingsChange(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	settings := server.runner.GetSystemSettings()
	settings.General.MaxConcurrentTests = 5
	body, _ := json.Marshal(settings)
	updateReq := httptest.NewRequest(http.MethodPut, "/api/v1/system/settings", bytes.NewReader(body))
	updateResp := httptest.NewRecorder()
	server.mux.ServeHTTP(updateResp, updateReq)
	if updateResp.Code != http.StatusOK {
		t.Fatalf("settings update status = %d, want %d body=%s", updateResp.Code, http.StatusOK, updateResp.Body.String())
	}

	auditReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/audit-events", nil)
	auditResp := httptest.NewRecorder()
	server.mux.ServeHTTP(auditResp, auditReq)
	if auditResp.Code != http.StatusOK {
		t.Fatalf("audit list status = %d, want %d body=%s", auditResp.Code, http.StatusOK, auditResp.Body.String())
	}
	var payload AuthAuditEventListResponse
	if err := json.Unmarshal(auditResp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal audit list: %v", err)
	}
	if payload.Count == 0 {
		t.Fatal("expected at least one audit event")
	}
	if payload.Events[0].Action != "system.settings.update" {
		t.Fatalf("first audit event = %#v, want system.settings.update", payload.Events[0])
	}
	if payload.Events[0].ActorType != "dev_mode" {
		t.Fatalf("actor type = %q, want dev_mode", payload.Events[0].ActorType)
	}

	filteredReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/audit-events?action=system.settings.update&actor_type=dev_mode&limit=1&offset=0", nil)
	filteredResp := httptest.NewRecorder()
	server.mux.ServeHTTP(filteredResp, filteredReq)
	if filteredResp.Code != http.StatusOK {
		t.Fatalf("filtered audit list status = %d, want %d body=%s", filteredResp.Code, http.StatusOK, filteredResp.Body.String())
	}
	var filtered AuthAuditEventListResponse
	if err := json.Unmarshal(filteredResp.Body.Bytes(), &filtered); err != nil {
		t.Fatalf("unmarshal filtered audit list: %v", err)
	}
	if filtered.Count != 1 || filtered.Limit != 1 || filtered.Offset != 0 {
		t.Fatalf("filtered audit payload = %#v, want one paged event", filtered)
	}

	exportReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/audit-events/export?action=system.settings.update&actor_type=dev_mode&limit=10", nil)
	exportResp := httptest.NewRecorder()
	server.mux.ServeHTTP(exportResp, exportReq)
	if exportResp.Code != http.StatusOK {
		t.Fatalf("audit export status = %d, want %d body=%s", exportResp.Code, http.StatusOK, exportResp.Body.String())
	}
	if got := exportResp.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/csv") {
		t.Fatalf("audit export content type = %q, want text/csv", got)
	}
	if got := exportResp.Header().Get("Content-Disposition"); !strings.Contains(got, "apexload-audit-events.csv") {
		t.Fatalf("audit export disposition = %q, want filename", got)
	}
	csvRows, err := csv.NewReader(strings.NewReader(exportResp.Body.String())).ReadAll()
	if err != nil {
		t.Fatalf("read audit export csv: %v", err)
	}
	if len(csvRows) < 2 {
		t.Fatalf("audit export rows = %#v, want header plus event", csvRows)
	}
	if csvRows[0][0] != "id" || csvRows[0][2] != "action" || csvRows[0][11] != "details_json" {
		t.Fatalf("audit export header = %#v", csvRows[0])
	}
	if csvRows[1][2] != "system.settings.update" || csvRows[1][4] != "dev_mode" {
		t.Fatalf("audit export event row = %#v", csvRows[1])
	}
}

func TestAuthUserAdminLifecycleAndActivity(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	createBody := bytes.NewBufferString(`{"subject":"oidc|alice","email":"alice@example.com","display_name":"Alice Admin"}`)
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/users", createBody)
	createResp := httptest.NewRecorder()
	server.mux.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create user status = %d, want %d body=%s", createResp.Code, http.StatusCreated, createResp.Body.String())
	}
	var created storage.User
	if err := json.Unmarshal(createResp.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal created user: %v", err)
	}
	if created.ID == "" || created.Subject != "oidc|alice" || !created.Active {
		t.Fatalf("created user = %#v, want active alice", created)
	}

	bindingReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/users/"+created.ID+"/bindings", bytes.NewBufferString(`{"scope_type":"project","scope_id":"project-a","role":"project_admin"}`))
	bindingResp := httptest.NewRecorder()
	server.mux.ServeHTTP(bindingResp, bindingReq)
	if bindingResp.Code != http.StatusCreated {
		t.Fatalf("create binding status = %d, want %d body=%s", bindingResp.Code, http.StatusCreated, bindingResp.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/users?q=alice", nil)
	listResp := httptest.NewRecorder()
	server.mux.ServeHTTP(listResp, listReq)
	if listResp.Code != http.StatusOK {
		t.Fatalf("list users status = %d, want %d body=%s", listResp.Code, http.StatusOK, listResp.Body.String())
	}
	var users AuthUserListResponse
	if err := json.Unmarshal(listResp.Body.Bytes(), &users); err != nil {
		t.Fatalf("unmarshal users: %v", err)
	}
	if users.Count != 1 || len(users.Users[0].Bindings) != 1 || users.Users[0].Bindings[0].ScopeID != "project-a" {
		t.Fatalf("users = %#v, want alice with project binding", users)
	}

	disableReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/users/"+created.ID+"/disable", nil)
	disableResp := httptest.NewRecorder()
	server.mux.ServeHTTP(disableResp, disableReq)
	if disableResp.Code != http.StatusOK {
		t.Fatalf("disable user status = %d, want %d body=%s", disableResp.Code, http.StatusOK, disableResp.Body.String())
	}
	enableReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/users/"+created.ID+"/enable", nil)
	enableResp := httptest.NewRecorder()
	server.mux.ServeHTTP(enableResp, enableReq)
	if enableResp.Code != http.StatusOK {
		t.Fatalf("enable user status = %d, want %d body=%s", enableResp.Code, http.StatusOK, enableResp.Body.String())
	}

	activityReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/user-activity?actor_subject=dev-mode&action=auth.user.binding.create&project_id=project-a", nil)
	activityResp := httptest.NewRecorder()
	server.mux.ServeHTTP(activityResp, activityReq)
	if activityResp.Code != http.StatusOK {
		t.Fatalf("activity status = %d, want %d body=%s", activityResp.Code, http.StatusOK, activityResp.Body.String())
	}
	var activity AuthUserActivityResponse
	if err := json.Unmarshal(activityResp.Body.Bytes(), &activity); err != nil {
		t.Fatalf("unmarshal activity: %v", err)
	}
	if activity.Count != 1 || activity.Events[0].Action != "auth.user.binding.create" {
		t.Fatalf("activity = %#v, want binding create event", activity)
	}

	exportReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/user-activity/export?actor_subject=dev-mode&limit=10", nil)
	exportResp := httptest.NewRecorder()
	server.mux.ServeHTTP(exportResp, exportReq)
	if exportResp.Code != http.StatusOK {
		t.Fatalf("activity export status = %d, want %d body=%s", exportResp.Code, http.StatusOK, exportResp.Body.String())
	}
	rows, err := csv.NewReader(strings.NewReader(exportResp.Body.String())).ReadAll()
	if err != nil {
		t.Fatalf("read activity export csv: %v", err)
	}
	if len(rows) < 2 || rows[0][5] != "actor_subject" || rows[1][5] != "dev-mode" {
		t.Fatalf("activity export rows = %#v, want dev-mode actor", rows)
	}
}

func TestScopedProjectAdminCanOnlyManageScopedUserBindings(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)
	now := time.Now().UTC()
	for _, user := range []*storage.User{
		{ID: "admin-user-id", Subject: "admin-user", Email: "admin@example.com", Active: true, CreatedAt: now, UpdatedAt: now},
		{ID: "delegated-user-id", Subject: "delegated-user", Email: "delegated@example.com", Active: true, CreatedAt: now, UpdatedAt: now},
		{ID: "target-user-id", Subject: "target-user", Email: "target@example.com", Active: true, CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.SaveUser(user); err != nil {
			t.Fatalf("SaveUser(%s) returned error: %v", user.ID, err)
		}
	}
	for _, binding := range []*storage.UserRoleBinding{
		{ID: "admin-system", UserID: "admin-user-id", ScopeType: "global", Role: RoleSystemAdmin, CreatedAt: now, UpdatedAt: now},
		{ID: "delegated-project-a", UserID: "delegated-user-id", ScopeType: "project", ScopeID: "project-a", Role: RoleProjectAdmin, CreatedAt: now, UpdatedAt: now},
		{ID: "target-project-a", UserID: "target-user-id", ScopeType: "project", ScopeID: "project-a", Role: RoleViewer, CreatedAt: now, UpdatedAt: now},
		{ID: "target-project-b", UserID: "target-user-id", ScopeType: "project", ScopeID: "project-b", Role: RoleViewer, CreatedAt: now, UpdatedAt: now},
		{ID: "target-global", UserID: "target-user-id", ScopeType: "global", Role: RoleViewer, CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.SaveUserRoleBinding(binding); err != nil {
			t.Fatalf("SaveUserRoleBinding(%s) returned error: %v", binding.ID, err)
		}
	}

	createReq := trustedHeaderJSONRequest(http.MethodPost, "/api/v1/auth/users", `{"subject":"new-user"}`, "delegated-user")
	createResp := httptest.NewRecorder()
	server.mux.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusForbidden {
		t.Fatalf("delegated create user status = %d, want %d body=%s", createResp.Code, http.StatusForbidden, createResp.Body.String())
	}

	updateReq := trustedHeaderJSONRequest(http.MethodPatch, "/api/v1/auth/users/target-user-id", `{"display_name":"Nope"}`, "delegated-user")
	updateResp := httptest.NewRecorder()
	server.mux.ServeHTTP(updateResp, updateReq)
	if updateResp.Code != http.StatusForbidden {
		t.Fatalf("delegated update user status = %d, want %d body=%s", updateResp.Code, http.StatusForbidden, updateResp.Body.String())
	}

	disableReq := trustedHeaderJSONRequest(http.MethodPost, "/api/v1/auth/users/target-user-id/disable", `{}`, "delegated-user")
	disableResp := httptest.NewRecorder()
	server.mux.ServeHTTP(disableResp, disableReq)
	if disableResp.Code != http.StatusForbidden {
		t.Fatalf("delegated disable user status = %d, want %d body=%s", disableResp.Code, http.StatusForbidden, disableResp.Body.String())
	}

	listReq := trustedHeaderJSONRequest(http.MethodGet, "/api/v1/auth/users?q=target", ``, "delegated-user")
	listResp := httptest.NewRecorder()
	server.mux.ServeHTTP(listResp, listReq)
	if listResp.Code != http.StatusOK {
		t.Fatalf("delegated list users status = %d, want %d body=%s", listResp.Code, http.StatusOK, listResp.Body.String())
	}
	var users AuthUserListResponse
	if err := json.Unmarshal(listResp.Body.Bytes(), &users); err != nil {
		t.Fatalf("unmarshal delegated users: %v", err)
	}
	if users.Count != 1 || len(users.Users) != 1 || len(users.Users[0].Bindings) != 1 || users.Users[0].Bindings[0].ScopeID != "project-a" {
		t.Fatalf("delegated visible bindings = %#v, want only project-a binding", users.Users)
	}

	allowedBindingReq := trustedHeaderJSONRequest(http.MethodPost, "/api/v1/auth/users/target-user-id/bindings", `{"scope_type":"project","scope_id":"project-a","role":"editor"}`, "delegated-user")
	allowedBindingResp := httptest.NewRecorder()
	server.mux.ServeHTTP(allowedBindingResp, allowedBindingReq)
	if allowedBindingResp.Code != http.StatusCreated {
		t.Fatalf("delegated project binding status = %d, want %d body=%s", allowedBindingResp.Code, http.StatusCreated, allowedBindingResp.Body.String())
	}

	for name, body := range map[string]string{
		"other project": `{"scope_type":"project","scope_id":"project-b","role":"editor"}`,
		"global role":   `{"scope_type":"global","role":"viewer"}`,
		"system admin":  `{"scope_type":"project","scope_id":"project-a","role":"system_admin"}`,
	} {
		req := trustedHeaderJSONRequest(http.MethodPost, "/api/v1/auth/users/target-user-id/bindings", body, "delegated-user")
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		if resp.Code != http.StatusForbidden {
			t.Fatalf("%s binding status = %d, want %d body=%s", name, resp.Code, http.StatusForbidden, resp.Body.String())
		}
	}

	deleteReq := trustedHeaderJSONRequest(http.MethodDelete, "/api/v1/auth/users/target-user-id/bindings/target-project-b", ``, "delegated-user")
	deleteResp := httptest.NewRecorder()
	server.mux.ServeHTTP(deleteResp, deleteReq)
	if deleteResp.Code != http.StatusForbidden {
		t.Fatalf("delegated delete other project binding status = %d, want %d body=%s", deleteResp.Code, http.StatusForbidden, deleteResp.Body.String())
	}

	adminUpdateReq := trustedHeaderJSONRequest(http.MethodPatch, "/api/v1/auth/users/target-user-id", `{"display_name":"Target Renamed"}`, "admin-user")
	adminUpdateResp := httptest.NewRecorder()
	server.mux.ServeHTTP(adminUpdateResp, adminUpdateReq)
	if adminUpdateResp.Code != http.StatusOK {
		t.Fatalf("system admin update user status = %d, want %d body=%s", adminUpdateResp.Code, http.StatusOK, adminUpdateResp.Body.String())
	}
}

func TestCanManageBindingAllowsScopedDelegationAcrossSupportedScopes(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/users/user-id/bindings", nil)
	user := &AuthenticatedUser{
		ProjectRoles:    map[string]map[string]struct{}{"project-a": {RoleProjectAdmin: {}}},
		DomainRoles:     map[string]map[string]struct{}{"domain-a": {RoleProjectAdmin: {}}},
		RepositoryRoles: map[string]map[string]struct{}{"repo-a": {RoleProjectAdmin: {}}},
		GlobalRoles:     map[string]struct{}{},
	}
	req = req.WithContext(context.WithValue(req.Context(), authUserContextKey{}, user))
	cases := []struct {
		name    string
		binding *storage.UserRoleBinding
		want    bool
	}{
		{name: "project in scope", binding: &storage.UserRoleBinding{ScopeType: "project", ScopeID: "project-a", Role: RoleEditor}, want: true},
		{name: "project out of scope", binding: &storage.UserRoleBinding{ScopeType: "project", ScopeID: "project-b", Role: RoleEditor}, want: false},
		{name: "domain in scope", binding: &storage.UserRoleBinding{ScopeType: "domain", ScopeID: "domain-a", Role: RoleViewer}, want: true},
		{name: "repository in scope", binding: &storage.UserRoleBinding{ScopeType: "application_repository", ScopeID: "repo-a", Role: RoleViewer}, want: true},
		{name: "global denied", binding: &storage.UserRoleBinding{ScopeType: "global", Role: RoleViewer}, want: false},
		{name: "system admin denied", binding: &storage.UserRoleBinding{ScopeType: "project", ScopeID: "project-a", Role: RoleSystemAdmin}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := canManageBinding(req, nil, tc.binding); got != tc.want {
				t.Fatalf("canManageBinding() = %v, want %v", got, tc.want)
			}
		})
	}
}

func trustedHeaderJSONRequest(method string, path string, body string, subject string) *http.Request {
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Auth-Request-User", subject)
	return req
}

func effectiveAccessOperationAllowed(resources []AuthEffectiveAccessResource, operationID string) bool {
	for _, resource := range resources {
		for _, operation := range resource.Operations {
			if operation.ID == operationID {
				return operation.Allowed
			}
		}
	}
	return false
}

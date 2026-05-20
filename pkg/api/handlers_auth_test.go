package api

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/stormrunner/pkg/storage"
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
	req.Header.Set("X-Stormrunner-Roles", RoleSystemAdmin+","+RoleViewer)
	req.Header.Set("X-Stormrunner-Project-Roles", "project-a="+RoleEditor+"|"+RoleViewer+";project-b="+RoleProjectAdmin)
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
	req.Header.Set("X-Stormrunner-Roles", RoleSystemAdmin)
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

func TestHandleGetAuthDiagnosticsFailsOIDCReadinessWhenSecretsMissing(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	settings := server.runner.GetSystemSettings()
	settings.Auth.Mode = authModeOIDC
	settings.Auth.ProviderName = "Example OIDC"
	settings.Auth.IssuerURL = "https://login.example.test/tenant/v2.0"
	settings.Auth.ClientID = "stormrunner-dashboard"
	settings.Auth.ClientSecretEnvVar = "STORMRUNNER_OIDC_CLIENT_SECRET"
	settings.Auth.RedirectURL = "https://stormrunner.example.test/api/v1/auth/callback"
	settings.Auth.SessionSecretEnvVar = "STORMRUNNER_AUTH_SESSION_SECRET"
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/diagnostics", nil)
	req.Header.Set("X-Auth-Request-User", "sysadmin")
	req.Header.Set("X-Stormrunner-Roles", RoleSystemAdmin)
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
	if len(identityAndAudit.Operations) != 3 || identityAndAudit.Operations[0].ID != "view_auth_diagnostics" {
		t.Fatalf("identity and audit operations = %#v, want diagnostics + audit + service account operations", identityAndAudit.Operations)
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
	req.Header.Set("X-Stormrunner-Project-Roles", "project-a="+RoleEditor)
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

func TestHandleServiceAccountCRUD(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
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
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
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
	if got := exportResp.Header().Get("Content-Disposition"); !strings.Contains(got, "stormrunner-audit-events.csv") {
		t.Fatalf("audit export disposition = %q, want filename", got)
	}
	csvRows, err := csv.NewReader(strings.NewReader(exportResp.Body.String())).ReadAll()
	if err != nil {
		t.Fatalf("read audit export csv: %v", err)
	}
	if len(csvRows) < 2 {
		t.Fatalf("audit export rows = %#v, want header plus event", csvRows)
	}
	if csvRows[0][0] != "id" || csvRows[0][2] != "action" || csvRows[0][10] != "details_json" {
		t.Fatalf("audit export header = %#v", csvRows[0])
	}
	if csvRows[1][2] != "system.settings.update" || csvRows[1][4] != "dev_mode" {
		t.Fatalf("audit export event row = %#v", csvRows[1])
	}
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

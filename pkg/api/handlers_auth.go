package api

import (
	"context"
	"crypto/rand"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/stormrunner/pkg/storage"
)

type AuthUserSummary struct {
	Subject      string              `json:"subject"`
	DisplayName  string              `json:"display_name,omitempty"`
	Email        string              `json:"email,omitempty"`
	GlobalRoles  []string            `json:"global_roles,omitempty"`
	ProjectRoles map[string][]string `json:"project_roles,omitempty"`
}

type AuthStatusResponse struct {
	Enabled           bool             `json:"enabled"`
	Mode              string           `json:"mode"`
	ProviderName      string           `json:"provider_name,omitempty"`
	LoginURL          string           `json:"login_url,omitempty"`
	LogoutURL         string           `json:"logout_url,omitempty"`
	LogoutCSRF        string           `json:"logout_csrf_token,omitempty"`
	Authenticated     bool             `json:"authenticated"`
	SessionStatus     string           `json:"session_status,omitempty"`
	SessionExpiresAt  string           `json:"session_expires_at,omitempty"`
	SessionTTLSeconds int64            `json:"session_ttl_seconds,omitempty"`
	User              *AuthUserSummary `json:"user,omitempty"`
}

type AuthLogoutResponse struct {
	RedirectURL string `json:"redirect_url"`
}

type AuthDiagnosticsCheck struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Status  string `json:"status"`
	Summary string `json:"summary"`
	Detail  string `json:"detail,omitempty"`
}

type AuthDiagnosticsResponse struct {
	Enabled       bool                   `json:"enabled"`
	Mode          string                 `json:"mode"`
	ProviderName  string                 `json:"provider_name,omitempty"`
	ProviderModel string                 `json:"provider_model"`
	Ready         bool                   `json:"ready"`
	Summary       string                 `json:"summary"`
	Checks        []AuthDiagnosticsCheck `json:"checks"`
}

type AuthRoleDefinition struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Scope        string   `json:"scope"`
	Description  string   `json:"description"`
	Capabilities []string `json:"capabilities,omitempty"`
}

type AuthRoleCatalogResponse struct {
	ProviderModel string               `json:"provider_model"`
	Roles         []AuthRoleDefinition `json:"roles"`
}

type AuthAccessOperation struct {
	ID           string   `json:"id"`
	Label        string   `json:"label"`
	GlobalRoles  []string `json:"global_roles,omitempty"`
	ProjectRoles []string `json:"project_roles,omitempty"`
}

type AuthAccessResource struct {
	ID          string                `json:"id"`
	Name        string                `json:"name"`
	Description string                `json:"description"`
	Operations  []AuthAccessOperation `json:"operations"`
}

type AuthAccessMatrixResponse struct {
	Version   string               `json:"version"`
	Resources []AuthAccessResource `json:"resources"`
}

type AuthEffectiveAccessOperation struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Allowed     bool   `json:"allowed"`
	GrantSource string `json:"grant_source,omitempty"`
}

type AuthEffectiveAccessResource struct {
	ID          string                         `json:"id"`
	Name        string                         `json:"name"`
	Description string                         `json:"description"`
	Operations  []AuthEffectiveAccessOperation `json:"operations"`
}

type AuthEffectiveAccessResponse struct {
	Enabled       bool                          `json:"enabled"`
	Authenticated bool                          `json:"authenticated"`
	ProjectID     string                        `json:"project_id,omitempty"`
	Resources     []AuthEffectiveAccessResource `json:"resources"`
}

type AuthServiceAccountRequest struct {
	Name                 string              `json:"name"`
	Description          string              `json:"description,omitempty"`
	GlobalRoles          []string            `json:"global_roles,omitempty"`
	ProjectRoles         map[string][]string `json:"project_roles,omitempty"`
	Scopes               []string            `json:"scopes,omitempty"`
	ExpiresAt            string              `json:"expires_at,omitempty"`
	RotationIntervalDays int                 `json:"rotation_interval_days,omitempty"`
}

type AuthServiceAccountUpdateRequest struct {
	Active *bool `json:"active"`
}

type AuthServiceAccountCreateResponse struct {
	ServiceAccount *storage.ServiceAccount `json:"service_account"`
	APIKey         string                  `json:"api_key"`
}

type AuthServiceAccountListResponse struct {
	ServiceAccounts []*storage.ServiceAccount `json:"service_accounts"`
	Count           int                       `json:"count"`
}

type AuthAuditEventListResponse struct {
	Events []*storage.AuditEvent `json:"events"`
	Count  int                   `json:"count"`
	Limit  int                   `json:"limit"`
	Offset int                   `json:"offset"`
}

var authRoleCatalog = []AuthRoleDefinition{
	{
		ID:          RoleSystemAdmin,
		Name:        "System Admin",
		Scope:       "global",
		Description: "Full platform administration across settings, projects, and executions.",
		Capabilities: []string{
			"Manage global settings",
			"Create and delete projects",
			"Read and mutate all test assets",
		},
	},
	{
		ID:          RoleProjectAdmin,
		Name:        "Project Admin",
		Scope:       "global_or_project",
		Description: "Govern a project's assets, quotas, and execution lifecycle.",
		Capabilities: []string{
			"Manage project governance",
			"Edit scripts and scenarios",
			"Start, stop, and delete project tests",
		},
	},
	{
		ID:          RoleEditor,
		Name:        "Editor",
		Scope:       "global_or_project",
		Description: "Create and modify scripts, scenarios, and active executions without governance controls.",
		Capabilities: []string{
			"Create and edit scripts",
			"Launch and control tests",
			"Use draft replay tooling",
		},
	},
	{
		ID:          RoleViewer,
		Name:        "Viewer",
		Scope:       "global_or_project",
		Description: "Read-only access to dashboards, archives, and project-scoped monitoring surfaces.",
		Capabilities: []string{
			"View dashboards and results",
			"Read scripts and scenarios",
			"Access project-scoped live telemetry",
		},
	},
}

var authAccessMatrix = []AuthAccessResource{
	{
		ID:          "system_configuration",
		Name:        "System Configuration",
		Description: "Controller-wide settings, infrastructure inventory, usage accounting, and global observer surfaces.",
		Operations: []AuthAccessOperation{
			{ID: "view_system", Label: "View settings, inventory, and usage accounting", GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}},
			{ID: "manage_system", Label: "Update system settings", GlobalRoles: []string{RoleSystemAdmin}},
		},
	},
	{
		ID:          "projects_and_folders",
		Name:        "Projects and Folders",
		Description: "Project catalog, governance defaults, and result-folder organization.",
		Operations: []AuthAccessOperation{
			{ID: "view_projects", Label: "View projects and project details", GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin}},
			{ID: "manage_projects", Label: "Create, update, delete projects and folders", GlobalRoles: []string{RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleProjectAdmin}},
		},
	},
	{
		ID:          "applications_and_releases",
		Name:        "Applications and Releases",
		Description: "Application inventory and release context used to organize test history.",
		Operations: []AuthAccessOperation{
			{ID: "view_applications", Label: "View applications and releases", GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin}},
			{ID: "manage_applications", Label: "Create, update, archive, and delete applications or releases", GlobalRoles: []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleEditor, RoleProjectAdmin}},
		},
	},
	{
		ID:          "scripts_and_imports",
		Name:        "Scripts, Imports, and Replay",
		Description: "Script CRUD, validation, exports, protocol imports, and replay tooling.",
		Operations: []AuthAccessOperation{
			{ID: "view_scripts", Label: "View, validate, and export scripts", GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin}},
			{ID: "manage_scripts", Label: "Create, edit, move, import, and replay scripts", GlobalRoles: []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleEditor, RoleProjectAdmin}},
		},
	},
	{
		ID:          "tests_and_scenarios",
		Name:        "Tests and Scenarios",
		Description: "Scenario definitions, queued runs, live controls, archives, and comparison baselines.",
		Operations: []AuthAccessOperation{
			{ID: "view_tests", Label: "View scenarios, tests, metrics, and archives", GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin}},
			{ID: "manage_tests", Label: "Create, start, stop, pause, move, scale, and delete tests or scenarios", GlobalRoles: []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleEditor, RoleProjectAdmin}},
		},
	},
	{
		ID:          "data_pools",
		Name:        "Shared Data Pools",
		Description: "Reusable project data sets and row-level mutations for scenario execution.",
		Operations: []AuthAccessOperation{
			{ID: "view_data_pools", Label: "View data pools and rows", GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin}},
			{ID: "manage_data_pools", Label: "Create, update, and delete pools or rows", GlobalRoles: []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleEditor, RoleProjectAdmin}},
		},
	},
	{
		ID:          "recorder_and_browser_replay",
		Name:        "Recorder and Browser Replay",
		Description: "Project-scoped recorder sessions, replay sessions, and related control endpoints.",
		Operations: []AuthAccessOperation{
			{ID: "view_replay", Label: "Monitor recorder or browser replay sessions", GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin}},
			{ID: "manage_replay", Label: "Create or control recorder and browser replay sessions", GlobalRoles: []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleEditor, RoleProjectAdmin}},
		},
	},
	{
		ID:          "identity_and_audit",
		Name:        "Identity, Service Accounts, and Audit",
		Description: "Service-account API keys, auth troubleshooting, and governance audit history.",
		Operations: []AuthAccessOperation{
			{ID: "view_auth_diagnostics", Label: "View auth diagnostics", GlobalRoles: []string{RoleSystemAdmin}},
			{ID: "view_audit", Label: "View audit events", GlobalRoles: []string{RoleSystemAdmin}},
			{ID: "manage_service_accounts", Label: "Create, disable, rotate, and delete service-account API keys", GlobalRoles: []string{RoleSystemAdmin}},
		},
	},
}

func (s *Server) handleGetAuthStatus(w http.ResponseWriter, r *http.Request) {
	settings := normalizeAuthSettings(s.runner.GetSystemSettings().Auth)
	loginURL, logoutURL := authStatusURLs(settings)
	response := AuthStatusResponse{
		Enabled:      !isAuthBypassMode(settings.Mode),
		Mode:         settings.Mode,
		ProviderName: settings.ProviderName,
		LoginURL:     loginURL,
		LogoutURL:    logoutURL,
	}

	if isAuthBypassMode(settings.Mode) {
		writeJSON(w, http.StatusOK, response)
		return
	}

	if settings.Mode == authModeOIDC {
		sessionResolution := resolveOIDCSession(r, settings)
		response.SessionStatus = sessionResolution.Status
		if !sessionResolution.Session.ExpiresAt.IsZero() {
			response.SessionExpiresAt = sessionResolution.Session.ExpiresAt.Format(time.RFC3339)
			if ttl := int64(time.Until(sessionResolution.Session.ExpiresAt).Seconds()); ttl > 0 {
				response.SessionTTLSeconds = ttl
			}
		}
		if sessionResolution.User == nil {
			writeJSON(w, http.StatusOK, response)
			return
		}
		response.Authenticated = true
		response.User = authUserSummary(sessionResolution.User)
		if csrfToken, err := issueOIDCLogoutCSRFToken(sessionResolution.User, settings); err == nil {
			response.LogoutCSRF = csrfToken
		}
		writeJSON(w, http.StatusOK, response)
		return
	}

	resolution, err := resolveRequestAuth(r, settings, s.store)
	if err != nil {
		writeJSON(w, http.StatusOK, response)
		return
	}

	response.Authenticated = true
	response.User = authUserSummary(resolution.User)
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleGetAuthDiagnostics(w http.ResponseWriter, r *http.Request) {
	settings := normalizeAuthSettings(s.runner.GetSystemSettings().Auth)
	if settings.Mode == authModeTrustedHeaders {
		resolution, err := resolveRequestAuth(r, settings, s.store)
		if err != nil {
			writeError(w, http.StatusUnauthorized, err.Error())
			return
		}
		if resolution.User == nil || !resolution.User.hasGlobalRole(RoleSystemAdmin) {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
	}
	writeJSON(w, http.StatusOK, buildAuthDiagnosticsResponse(r, settings, s.store))
}

func (s *Server) handleGetAuthRoleCatalog(w http.ResponseWriter, _ *http.Request) {
	providerModel := "upstream_gateway"
	if normalizeAuthSettings(s.runner.GetSystemSettings().Auth).Mode == authModeOIDC {
		providerModel = "native_oidc"
	}
	writeJSON(w, http.StatusOK, AuthRoleCatalogResponse{
		ProviderModel: providerModel,
		Roles:         authRoleCatalog,
	})
}

func buildAuthDiagnosticsResponse(r *http.Request, settings AuthSettings, store storage.Store) AuthDiagnosticsResponse {
	response := AuthDiagnosticsResponse{
		Enabled:       !isAuthBypassMode(settings.Mode),
		Mode:          settings.Mode,
		ProviderName:  settings.ProviderName,
		ProviderModel: authProviderModel(settings),
		Checks:        make([]AuthDiagnosticsCheck, 0, 8),
	}

	switch settings.Mode {
	case authModeTrustedHeaders:
		response.Checks = append(response.Checks,
			buildTrustedHeaderConfiguredCheck("subject_header", "Subject Header", settings.UserHeader != "", settings.UserHeader, "Stormrunner needs a subject header to resolve the authenticated user."),
			buildTrustedHeaderConfiguredCheck("role_headers", "Role Header Mapping", settings.RolesHeader != "" || settings.ProjectRolesHeader != "", strings.TrimSpace(settings.RolesHeader)+" | "+strings.TrimSpace(settings.ProjectRolesHeader), "Configure at least one global or project role header so RBAC can grant access."),
			buildOptionalOperatorCheck("provider_label", "Provider Label", settings.ProviderName, "Add a provider label so the sign-in gate points operators at the right upstream gateway."),
			buildOptionalOperatorCheck("login_url", "Login Entry Point", settings.LoginURL, "Set an upstream login URL so blocked users have a clear path back into the authenticating gateway."),
		)
		if resolution, err := resolveRequestAuth(r, settings, store); err == nil && resolution.User != nil {
			response.Checks = append(response.Checks, AuthDiagnosticsCheck{
				ID:      "current_request_identity",
				Label:   "Current Request Identity",
				Status:  "pass",
				Summary: "Trusted headers authenticated the current request.",
				Detail:  resolution.User.Subject,
			})
		} else {
			response.Checks = append(response.Checks, AuthDiagnosticsCheck{
				ID:      "current_request_identity",
				Label:   "Current Request Identity",
				Status:  "warn",
				Summary: "No authenticated identity reached Stormrunner on the current request.",
				Detail:  "Open the dashboard through the authenticating gateway and verify the configured subject and role headers are forwarded.",
			})
		}
	case authModeOIDC:
		response.Checks = append(response.Checks,
			buildRequiredURLCheck("issuer_url", "Issuer URL", settings.IssuerURL),
			buildRequiredURLCheck("redirect_url", "Redirect URL", settings.RedirectURL),
			buildEnvVarCheck("client_secret_env", "Client Secret Environment Variable", settings.ClientSecretEnvVar),
			buildEnvVarCheck("session_secret_env", "Session Secret Environment Variable", settings.SessionSecretEnvVar),
			buildScopesCheck(settings.Scopes),
			buildOptionalOperatorCheck("provider_label", "Provider Label", settings.ProviderName, "Add a provider label so the dashboard sign-in flow names the IdP clearly for operators."),
		)
		response.Checks = append(response.Checks, buildOIDCDiscoveryCheck(r.Context(), settings))
		sessionResolution := resolveOIDCSession(r, settings)
		if sessionResolution.User != nil {
			response.Checks = append(response.Checks, AuthDiagnosticsCheck{
				ID:      "current_session",
				Label:   "Current Session",
				Status:  "pass",
				Summary: "Stormrunner has an active signed OIDC session for the current request.",
				Detail:  sessionResolution.User.Subject,
			})
		} else if sessionResolution.Status == "expired" {
			response.Checks = append(response.Checks, AuthDiagnosticsCheck{
				ID:      "current_session",
				Label:   "Current Session",
				Status:  "warn",
				Summary: "The local Stormrunner OIDC session has expired.",
				Detail:  "Sign in again through the native OIDC flow.",
			})
		} else if sessionResolution.Status == "invalid" {
			response.Checks = append(response.Checks, AuthDiagnosticsCheck{
				ID:      "current_session",
				Label:   "Current Session",
				Status:  "warn",
				Summary: "Stormrunner could not verify the local OIDC session cookie.",
				Detail:  "Clear the browser session cookie and sign in again.",
			})
		} else {
			response.Checks = append(response.Checks, AuthDiagnosticsCheck{
				ID:      "current_session",
				Label:   "Current Session",
				Status:  "warn",
				Summary: "No active Stormrunner OIDC session is present on the current request.",
				Detail:  "Use the native OIDC login flow after the issuer, redirect URL, and secret environment variables are configured.",
			})
		}
	default:
		response.Checks = append(response.Checks,
			AuthDiagnosticsCheck{
				ID:      "development_bypass",
				Label:   "Development Bypass",
				Status:  "info",
				Summary: "Stormrunner is running with local auth bypass enabled.",
				Detail:  "All requests are allowed locally until you switch to Trusted Headers or Native OIDC.",
			},
		)
	}

	response.Ready = authDiagnosticsReady(response.Checks)
	response.Summary = buildAuthDiagnosticsSummary(response.Mode, response.Ready, response.Checks)
	return response
}

func (s *Server) handleGetAuthAccessMatrix(w http.ResponseWriter, _ *http.Request) {
	version := "trusted_headers_v1"
	if normalizeAuthSettings(s.runner.GetSystemSettings().Auth).Mode == authModeOIDC {
		version = "rbac_v1"
	}
	writeJSON(w, http.StatusOK, AuthAccessMatrixResponse{
		Version:   version,
		Resources: authAccessMatrix,
	})
}

func (s *Server) handleGetAuthEffectiveAccess(w http.ResponseWriter, r *http.Request) {
	settings := normalizeAuthSettings(s.runner.GetSystemSettings().Auth)
	projectID := normalizeProjectID(strings.TrimSpace(r.URL.Query().Get("project_id")))
	response := AuthEffectiveAccessResponse{
		Enabled:   !isAuthBypassMode(settings.Mode),
		ProjectID: projectID,
	}

	if isAuthBypassMode(settings.Mode) {
		response.Resources = buildDevModeEffectiveAccess(authAccessMatrix)
		writeJSON(w, http.StatusOK, response)
		return
	}

	resolution, err := resolveRequestAuth(r, settings, s.store)
	if err != nil {
		response.Resources = buildDeniedEffectiveAccess(authAccessMatrix)
		writeJSON(w, http.StatusOK, response)
		return
	}

	response.Authenticated = true
	response.Resources = buildUserEffectiveAccess(authAccessMatrix, resolution.User, projectID)
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleListServiceAccounts(w http.ResponseWriter, _ *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	accounts, err := s.store.ListServiceAccounts()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, AuthServiceAccountListResponse{ServiceAccounts: accounts, Count: len(accounts)})
}

func (s *Server) handleCreateServiceAccount(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	var req AuthServiceAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	account, apiKey, err := buildServiceAccount(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.SaveServiceAccount(account); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordAuditEvent(r, "auth.service_account.create", "success", "service_account", account.ID, "", map[string]any{"name": account.Name})
	writeJSON(w, http.StatusCreated, AuthServiceAccountCreateResponse{ServiceAccount: account, APIKey: apiKey})
}

func (s *Server) handleUpdateServiceAccount(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "service account id is required")
		return
	}
	account, err := s.store.GetServiceAccount(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "service account not found")
		return
	}
	var req AuthServiceAccountUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.Active == nil {
		writeError(w, http.StatusBadRequest, "active field is required")
		return
	}
	updatedAt := time.Now().UTC()
	if err := s.store.UpdateServiceAccountActive(account.ID, *req.Active, updatedAt); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	account.Active = *req.Active
	account.UpdatedAt = updatedAt
	s.recordAuditEvent(r, "auth.service_account.update", "success", "service_account", account.ID, "", map[string]any{"active": account.Active})
	writeJSON(w, http.StatusOK, account)
}

func (s *Server) handleRotateServiceAccount(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "service account id is required")
		return
	}
	account, err := s.store.GetServiceAccount(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "service account not found")
		return
	}
	apiKey, err := generateAPIKey()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	updatedAt := time.Now().UTC()
	hashedKey := hashAPIKey(apiKey)
	if err := s.store.RotateServiceAccountKey(account.ID, hashedKey, updatedAt); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	account.KeyHash = hashedKey
	account.UpdatedAt = updatedAt
	account.KeyRotatedAt = &updatedAt
	if account.RotationIntervalDays > 0 {
		due := updatedAt.AddDate(0, 0, account.RotationIntervalDays)
		account.RotationDueAt = &due
	}
	s.recordAuditEvent(r, "auth.service_account.rotate", "success", "service_account", account.ID, "", map[string]any{"name": account.Name})
	writeJSON(w, http.StatusOK, AuthServiceAccountCreateResponse{ServiceAccount: account, APIKey: apiKey})
}

func (s *Server) handleDeleteServiceAccount(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	id := r.PathValue("id")
	if strings.TrimSpace(id) == "" {
		writeError(w, http.StatusBadRequest, "service account id is required")
		return
	}
	if err := s.store.DeleteServiceAccount(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordAuditEvent(r, "auth.service_account.delete", "success", "service_account", id, "", nil)
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

func (s *Server) handleListAuditEvents(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	filter, ok := parseAuditEventFilter(w, r, 50)
	if !ok {
		return
	}
	events, err := s.store.ListAuditEvents(filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, AuthAuditEventListResponse{Events: events, Count: len(events), Limit: filter.Limit, Offset: filter.Offset})
}

func (s *Server) handleExportAuditEvents(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	filter, ok := parseAuditEventFilter(w, r, 1000)
	if !ok {
		return
	}
	events, err := s.store.ListAuditEvents(filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="stormrunner-audit-events.csv"`)
	writer := csv.NewWriter(w)
	if err := writer.Write([]string{"id", "created_at", "action", "status", "actor_type", "actor_subject", "actor_display_name", "project_id", "resource_type", "resource_id", "details_json"}); err != nil {
		return
	}
	for _, event := range events {
		details := ""
		if len(event.Details) > 0 {
			if encoded, err := json.Marshal(event.Details); err == nil {
				details = string(encoded)
			}
		}
		record := []string{
			event.ID,
			event.CreatedAt.UTC().Format(time.RFC3339),
			event.Action,
			event.Status,
			event.ActorType,
			event.ActorSubject,
			event.ActorDisplayName,
			event.ProjectID,
			event.ResourceType,
			event.ResourceID,
			details,
		}
		if err := writer.Write(record); err != nil {
			return
		}
	}
	writer.Flush()
}

func parseAuditEventFilter(w http.ResponseWriter, r *http.Request, defaultLimit int) (storage.AuditEventFilter, bool) {
	limit := defaultLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if _, err := fmt.Sscanf(raw, "%d", &limit); err != nil || limit <= 0 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return storage.AuditEventFilter{}, false
		}
	}
	offset := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		if _, err := fmt.Sscanf(raw, "%d", &offset); err != nil || offset < 0 {
			writeError(w, http.StatusBadRequest, "offset must be a non-negative integer")
			return storage.AuditEventFilter{}, false
		}
	}
	return storage.AuditEventFilter{
		Limit:        limit,
		Offset:       offset,
		Action:       strings.TrimSpace(r.URL.Query().Get("action")),
		ActorType:    strings.TrimSpace(r.URL.Query().Get("actor_type")),
		ProjectID:    normalizeProjectID(strings.TrimSpace(r.URL.Query().Get("project_id"))),
		ResourceType: strings.TrimSpace(r.URL.Query().Get("resource_type")),
		ResourceID:   strings.TrimSpace(r.URL.Query().Get("resource_id")),
		Status:       strings.TrimSpace(r.URL.Query().Get("status")),
	}, true
}

func buildServiceAccount(req AuthServiceAccountRequest) (*storage.ServiceAccount, string, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, "", fmt.Errorf("service account name is required")
	}
	globalRoles, projectRoles, err := normalizeServiceAccountRoles(req.GlobalRoles, req.ProjectRoles)
	if err != nil {
		return nil, "", err
	}
	scopes, err := normalizeServiceAccountScopes(req.Scopes)
	if err != nil {
		return nil, "", err
	}
	var expiresAt *time.Time
	if raw := strings.TrimSpace(req.ExpiresAt); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return nil, "", fmt.Errorf("service account expires_at must be an RFC3339 timestamp")
		}
		parsed = parsed.UTC()
		if !parsed.After(time.Now().UTC()) {
			return nil, "", fmt.Errorf("service account expires_at must be in the future")
		}
		expiresAt = &parsed
	}
	rotationIntervalDays := req.RotationIntervalDays
	if rotationIntervalDays < 0 {
		return nil, "", fmt.Errorf("service account rotation_interval_days must be zero or greater")
	}
	if rotationIntervalDays == 0 {
		rotationIntervalDays = 90
	}
	apiKey, err := generateAPIKey()
	if err != nil {
		return nil, "", err
	}
	now := time.Now().UTC()
	account := &storage.ServiceAccount{
		ID:                   uuid.New().String(),
		Name:                 name,
		Description:          strings.TrimSpace(req.Description),
		KeyHash:              hashAPIKey(apiKey),
		Active:               true,
		GlobalRoles:          globalRoles,
		ProjectRoles:         projectRoles,
		Scopes:               scopes,
		CreatedAt:            now,
		UpdatedAt:            now,
		ExpiresAt:            expiresAt,
		KeyRotatedAt:         &now,
		RotationIntervalDays: rotationIntervalDays,
	}
	due := now.AddDate(0, 0, rotationIntervalDays)
	account.RotationDueAt = &due
	return account, apiKey, nil
}

func normalizeServiceAccountScopes(scopes []string) ([]string, error) {
	allowed := map[string]struct{}{
		"*":                       {},
		"automation:*":            {},
		"read":                    {},
		"write":                   {},
		"tests:run":               {},
		"scripts:write":           {},
		"results:read":            {},
		"system:read":             {},
		"settings:manage":         {},
		"service_accounts:manage": {},
		"audit:read":              {},
	}
	if len(scopes) == 0 {
		return []string{"automation:*"}, nil
	}
	set := make(map[string]struct{})
	out := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		for _, token := range strings.FieldsFunc(scope, func(r rune) bool {
			return r == ',' || r == '|' || r == ' ' || r == '\t' || r == '\n'
		}) {
			normalized := strings.ToLower(strings.TrimSpace(token))
			if normalized == "" {
				continue
			}
			if _, ok := allowed[normalized]; !ok {
				return nil, fmt.Errorf("unsupported service account scope %q", normalized)
			}
			if _, exists := set[normalized]; !exists {
				set[normalized] = struct{}{}
				out = append(out, normalized)
			}
		}
	}
	if len(out) == 0 {
		return []string{"automation:*"}, nil
	}
	return out, nil
}

func normalizeServiceAccountRoles(globalRoles []string, projectRoles map[string][]string) ([]string, map[string][]string, error) {
	allowed := map[string]struct{}{
		RoleSystemAdmin:  {},
		RoleProjectAdmin: {},
		RoleEditor:       {},
		RoleViewer:       {},
	}
	normalizeRoles := func(roles []string) ([]string, error) {
		set := make(map[string]struct{})
		for _, role := range roles {
			normalized := strings.ToLower(strings.TrimSpace(role))
			if normalized == "" {
				continue
			}
			if _, ok := allowed[normalized]; !ok {
				return nil, fmt.Errorf("unsupported role %q", normalized)
			}
			set[normalized] = struct{}{}
		}
		out := make([]string, 0, len(set))
		for role := range set {
			out = append(out, role)
		}
		sort.Strings(out)
		return out, nil
	}
	normalizedGlobal, err := normalizeRoles(globalRoles)
	if err != nil {
		return nil, nil, err
	}
	normalizedProjects := make(map[string][]string)
	for projectID, roles := range projectRoles {
		normalizedProjectID := normalizeProjectID(projectID)
		if normalizedProjectID == "" {
			continue
		}
		normalizedRoleList, err := normalizeRoles(roles)
		if err != nil {
			return nil, nil, err
		}
		if len(normalizedRoleList) > 0 {
			normalizedProjects[normalizedProjectID] = normalizedRoleList
		}
	}
	return normalizedGlobal, normalizedProjects, nil
}

func generateAPIKey() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("failed to generate api key: %w", err)
	}
	return "srk_" + hex.EncodeToString(buf), nil
}

func buildDevModeEffectiveAccess(resources []AuthAccessResource) []AuthEffectiveAccessResource {
	out := make([]AuthEffectiveAccessResource, 0, len(resources))
	for _, resource := range resources {
		entry := AuthEffectiveAccessResource{
			ID:          resource.ID,
			Name:        resource.Name,
			Description: resource.Description,
			Operations:  make([]AuthEffectiveAccessOperation, 0, len(resource.Operations)),
		}
		for _, operation := range resource.Operations {
			entry.Operations = append(entry.Operations, AuthEffectiveAccessOperation{
				ID:          operation.ID,
				Label:       operation.Label,
				Allowed:     true,
				GrantSource: "dev_mode",
			})
		}
		out = append(out, entry)
	}
	return out
}

func buildDeniedEffectiveAccess(resources []AuthAccessResource) []AuthEffectiveAccessResource {
	out := make([]AuthEffectiveAccessResource, 0, len(resources))
	for _, resource := range resources {
		entry := AuthEffectiveAccessResource{
			ID:          resource.ID,
			Name:        resource.Name,
			Description: resource.Description,
			Operations:  make([]AuthEffectiveAccessOperation, 0, len(resource.Operations)),
		}
		for _, operation := range resource.Operations {
			entry.Operations = append(entry.Operations, AuthEffectiveAccessOperation{
				ID:      operation.ID,
				Label:   operation.Label,
				Allowed: false,
			})
		}
		out = append(out, entry)
	}
	return out
}

func buildUserEffectiveAccess(resources []AuthAccessResource, user *AuthenticatedUser, projectID string) []AuthEffectiveAccessResource {
	out := make([]AuthEffectiveAccessResource, 0, len(resources))
	for _, resource := range resources {
		entry := AuthEffectiveAccessResource{
			ID:          resource.ID,
			Name:        resource.Name,
			Description: resource.Description,
			Operations:  make([]AuthEffectiveAccessOperation, 0, len(resource.Operations)),
		}
		for _, operation := range resource.Operations {
			allowed := false
			grantSource := ""
			if len(operation.GlobalRoles) > 0 && user.hasGlobalRole(operation.GlobalRoles...) {
				allowed = true
				grantSource = "global"
			} else if projectID != "" && len(operation.ProjectRoles) > 0 && user.hasProjectRole(projectID, operation.ProjectRoles...) {
				allowed = true
				grantSource = "project"
			}
			entry.Operations = append(entry.Operations, AuthEffectiveAccessOperation{
				ID:          operation.ID,
				Label:       operation.Label,
				Allowed:     allowed,
				GrantSource: grantSource,
			})
		}
		out = append(out, entry)
	}
	return out
}

func authUserSummary(user *AuthenticatedUser) *AuthUserSummary {
	if user == nil {
		return nil
	}

	projectRoles := make(map[string][]string, len(user.ProjectRoles))
	for projectID, roles := range user.ProjectRoles {
		projectRoles[projectID] = sortedRoles(roles)
	}

	return &AuthUserSummary{
		Subject:      user.Subject,
		DisplayName:  user.DisplayName,
		Email:        user.Email,
		GlobalRoles:  sortedRoles(user.GlobalRoles),
		ProjectRoles: projectRoles,
	}
}

func sortedRoles(roles map[string]struct{}) []string {
	if len(roles) == 0 {
		return nil
	}
	out := make([]string, 0, len(roles))
	for role := range roles {
		out = append(out, role)
	}
	sort.Strings(out)
	return out
}

func authProviderModel(settings AuthSettings) string {
	switch settings.Mode {
	case authModeOIDC:
		return "native_oidc"
	case authModeTrustedHeaders:
		return "upstream_gateway"
	default:
		return "development_bypass"
	}
}

func buildTrustedHeaderConfiguredCheck(id string, label string, configured bool, configuredValue string, remediation string) AuthDiagnosticsCheck {
	if configured {
		return AuthDiagnosticsCheck{
			ID:      id,
			Label:   label,
			Status:  "pass",
			Summary: "Configured",
			Detail:  strings.TrimSpace(configuredValue),
		}
	}
	return AuthDiagnosticsCheck{
		ID:      id,
		Label:   label,
		Status:  "fail",
		Summary: "Missing required header mapping.",
		Detail:  remediation,
	}
}

func buildOptionalOperatorCheck(id string, label string, value string, remediation string) AuthDiagnosticsCheck {
	trimmed := strings.TrimSpace(value)
	if trimmed != "" {
		return AuthDiagnosticsCheck{
			ID:      id,
			Label:   label,
			Status:  "pass",
			Summary: "Configured",
			Detail:  trimmed,
		}
	}
	return AuthDiagnosticsCheck{
		ID:      id,
		Label:   label,
		Status:  "warn",
		Summary: "Not configured.",
		Detail:  remediation,
	}
}

func buildRequiredURLCheck(id string, label string, value string) AuthDiagnosticsCheck {
	if err := validateRequiredAuthURL(strings.TrimSpace(id), value); err != nil {
		return AuthDiagnosticsCheck{
			ID:      id,
			Label:   label,
			Status:  "fail",
			Summary: err.Error(),
		}
	}
	return AuthDiagnosticsCheck{
		ID:      id,
		Label:   label,
		Status:  "pass",
		Summary: "Configured",
		Detail:  strings.TrimSpace(value),
	}
}

func buildEnvVarCheck(id string, label string, envVar string) AuthDiagnosticsCheck {
	trimmed := strings.TrimSpace(envVar)
	if trimmed == "" {
		return AuthDiagnosticsCheck{
			ID:      id,
			Label:   label,
			Status:  "fail",
			Summary: "Environment variable name is not configured.",
		}
	}
	if _, ok := os.LookupEnv(trimmed); !ok {
		return AuthDiagnosticsCheck{
			ID:      id,
			Label:   label,
			Status:  "fail",
			Summary: "Environment variable is not set on the controller.",
			Detail:  trimmed,
		}
	}
	return AuthDiagnosticsCheck{
		ID:      id,
		Label:   label,
		Status:  "pass",
		Summary: "Environment variable is available to the controller.",
		Detail:  trimmed,
	}
}

func buildScopesCheck(scopes []string) AuthDiagnosticsCheck {
	normalized := normalizeAuthScopes(scopes)
	if len(normalized) == 0 {
		return AuthDiagnosticsCheck{
			ID:      "scopes",
			Label:   "Requested Scopes",
			Status:  "fail",
			Summary: "At least one OIDC scope is required.",
		}
	}
	return AuthDiagnosticsCheck{
		ID:      "scopes",
		Label:   "Requested Scopes",
		Status:  "pass",
		Summary: "Configured",
		Detail:  strings.Join(normalized, ", "),
	}
}

func buildOIDCDiscoveryCheck(ctx context.Context, settings AuthSettings) AuthDiagnosticsCheck {
	if err := validateRequiredAuthURL("issuer_url", settings.IssuerURL); err != nil {
		return AuthDiagnosticsCheck{
			ID:      "issuer_discovery",
			Label:   "OIDC Discovery",
			Status:  "fail",
			Summary: "Issuer discovery is blocked until the issuer URL is valid.",
			Detail:  err.Error(),
		}
	}

	discoveryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := newOIDCProvider(discoveryCtx, settings.IssuerURL); err != nil {
		return AuthDiagnosticsCheck{
			ID:      "issuer_discovery",
			Label:   "OIDC Discovery",
			Status:  "fail",
			Summary: "Stormrunner could not reach the issuer discovery document.",
			Detail:  err.Error(),
		}
	}

	return AuthDiagnosticsCheck{
		ID:      "issuer_discovery",
		Label:   "OIDC Discovery",
		Status:  "pass",
		Summary: "Issuer discovery succeeded.",
		Detail:  strings.TrimSpace(settings.IssuerURL),
	}
}

func authDiagnosticsReady(checks []AuthDiagnosticsCheck) bool {
	for _, check := range checks {
		if check.Status == "fail" {
			return false
		}
	}
	return true
}

func buildAuthDiagnosticsSummary(mode string, ready bool, checks []AuthDiagnosticsCheck) string {
	failed := 0
	warned := 0
	for _, check := range checks {
		switch check.Status {
		case "fail":
			failed++
		case "warn":
			warned++
		}
	}

	if isAuthBypassMode(mode) {
		return "Development bypass is active. Switch to Trusted Headers or Native OIDC before production use."
	}
	if ready {
		if warned > 0 {
			return fmt.Sprintf("Auth mode is ready with %d advisory check(s) still worth reviewing.", warned)
		}
		return "Auth mode is ready."
	}
	return fmt.Sprintf("Auth mode is not ready yet. Resolve %d failing check(s) before relying on this configuration.", failed)
}

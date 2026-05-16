package api

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"encoding/json"
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
	Enabled       bool             `json:"enabled"`
	Mode          string           `json:"mode"`
	ProviderName  string           `json:"provider_name,omitempty"`
	LoginURL      string           `json:"login_url,omitempty"`
	LogoutURL     string           `json:"logout_url,omitempty"`
	Authenticated bool             `json:"authenticated"`
	User          *AuthUserSummary `json:"user,omitempty"`
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
	ID          string                        `json:"id"`
	Name        string                        `json:"name"`
	Description string                        `json:"description"`
	Operations  []AuthEffectiveAccessOperation `json:"operations"`
}

type AuthEffectiveAccessResponse struct {
	Enabled       bool                          `json:"enabled"`
	Authenticated bool                          `json:"authenticated"`
	ProjectID     string                        `json:"project_id,omitempty"`
	Resources     []AuthEffectiveAccessResource `json:"resources"`
}

type AuthServiceAccountRequest struct {
	Name         string              `json:"name"`
	Description  string              `json:"description,omitempty"`
	GlobalRoles  []string            `json:"global_roles,omitempty"`
	ProjectRoles map[string][]string `json:"project_roles,omitempty"`
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
		Description: "Controller-wide settings, infrastructure inventory, and global observer surfaces.",
		Operations: []AuthAccessOperation{
			{ID: "view_system", Label: "View settings and inventory", GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}},
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

	resolution, err := resolveRequestAuth(r, settings, s.store)
	if err != nil {
		writeJSON(w, http.StatusOK, response)
		return
	}

	response.Authenticated = true
	response.User = authUserSummary(resolution.User)
	writeJSON(w, http.StatusOK, response)
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
	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if _, err := fmt.Sscanf(raw, "%d", &limit); err != nil || limit <= 0 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
	}
	offset := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		if _, err := fmt.Sscanf(raw, "%d", &offset); err != nil || offset < 0 {
			writeError(w, http.StatusBadRequest, "offset must be a non-negative integer")
			return
		}
	}
	filter := storage.AuditEventFilter{
		Limit:        limit,
		Offset:       offset,
		Action:       strings.TrimSpace(r.URL.Query().Get("action")),
		ActorType:    strings.TrimSpace(r.URL.Query().Get("actor_type")),
		ProjectID:    normalizeProjectID(strings.TrimSpace(r.URL.Query().Get("project_id"))),
		ResourceType: strings.TrimSpace(r.URL.Query().Get("resource_type")),
		ResourceID:   strings.TrimSpace(r.URL.Query().Get("resource_id")),
		Status:       strings.TrimSpace(r.URL.Query().Get("status")),
	}
	events, err := s.store.ListAuditEvents(filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, AuthAuditEventListResponse{Events: events, Count: len(events), Limit: filter.Limit, Offset: filter.Offset})
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
	apiKey, err := generateAPIKey()
	if err != nil {
		return nil, "", err
	}
	now := time.Now().UTC()
	account := &storage.ServiceAccount{
		ID:           uuid.New().String(),
		Name:         name,
		Description:  strings.TrimSpace(req.Description),
		KeyHash:      hashAPIKey(apiKey),
		Active:       true,
		GlobalRoles:  globalRoles,
		ProjectRoles: projectRoles,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	return account, apiKey, nil
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

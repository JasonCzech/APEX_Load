package api

import (
	"net/http"
	"sort"
	"strings"
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
}

func (s *Server) handleGetAuthStatus(w http.ResponseWriter, r *http.Request) {
	settings := normalizeAuthSettings(s.runner.GetSystemSettings().Auth)
	response := AuthStatusResponse{
		Enabled:      settings.Mode != authModeDisabled,
		Mode:         settings.Mode,
		ProviderName: settings.ProviderName,
		LoginURL:     settings.LoginURL,
		LogoutURL:    settings.LogoutURL,
	}

	if settings.Mode == authModeDisabled {
		writeJSON(w, http.StatusOK, response)
		return
	}

	user, err := authenticatedUserFromHeaders(r, settings)
	if err != nil {
		writeJSON(w, http.StatusOK, response)
		return
	}

	response.Authenticated = true
	response.User = authUserSummary(user)
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleGetAuthRoleCatalog(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, AuthRoleCatalogResponse{
		ProviderModel: "upstream_gateway",
		Roles:         authRoleCatalog,
	})
}

func (s *Server) handleGetAuthAccessMatrix(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, AuthAccessMatrixResponse{
		Version:   "trusted_headers_v1",
		Resources: authAccessMatrix,
	})
}

func (s *Server) handleGetAuthEffectiveAccess(w http.ResponseWriter, r *http.Request) {
	settings := normalizeAuthSettings(s.runner.GetSystemSettings().Auth)
	projectID := normalizeProjectID(strings.TrimSpace(r.URL.Query().Get("project_id")))
	response := AuthEffectiveAccessResponse{
		Enabled:   settings.Mode != authModeDisabled,
		ProjectID: projectID,
	}

	if settings.Mode == authModeDisabled {
		response.Resources = buildDisabledEffectiveAccess(authAccessMatrix)
		writeJSON(w, http.StatusOK, response)
		return
	}

	user, err := authenticatedUserFromHeaders(r, settings)
	if err != nil {
		response.Resources = buildDeniedEffectiveAccess(authAccessMatrix)
		writeJSON(w, http.StatusOK, response)
		return
	}

	response.Authenticated = true
	response.Resources = buildUserEffectiveAccess(authAccessMatrix, user, projectID)
	writeJSON(w, http.StatusOK, response)
}

func buildDisabledEffectiveAccess(resources []AuthAccessResource) []AuthEffectiveAccessResource {
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
				GrantSource: "disabled",
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

package api

import (
	"net/http"
	"sort"
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

func (s *Server) handleGetAuthStatus(w http.ResponseWriter, r *http.Request) {
	settings := normalizeAuthSettings(s.runner.GetSystemSettings().Auth)
	response := AuthStatusResponse{
		Enabled: settings.Mode != authModeDisabled,
		Mode:    settings.Mode,
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

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
package api

import (
	"net/http"
	"strings"
)

const (
	devViewHeader      = "X-Stormrunner-Dev-View"
	devProjectIDHeader = "X-Stormrunner-Dev-Project-ID"

	devViewModeAdmin = "admin"
	devViewModeUser  = "user"
)

func devPreviewModeForRequest(settings AuthSettings, r *http.Request) (string, bool) {
	if settings.Mode != authModeDev {
		return "", false
	}
	switch strings.ToLower(strings.TrimSpace(r.Header.Get(devViewHeader))) {
	case devViewModeAdmin:
		return devViewModeAdmin, true
	case devViewModeUser:
		return devViewModeUser, true
	default:
		return "", false
	}
}

func devPreviewUserForRequest(r *http.Request, mode string) *AuthenticatedUser {
	if mode != devViewModeUser {
		return nil
	}
	projectID := normalizeProjectID(strings.TrimSpace(r.Header.Get(devProjectIDHeader)))
	projectRoles := make(map[string]map[string]struct{})
	if projectID != "" {
		projectRoles[projectID] = map[string]struct{}{RoleEditor: {}}
	}
	return &AuthenticatedUser{
		Subject:     "dev-user",
		DisplayName: "Dev User Preview",
		Email:       "dev-user@stormrunner.local",
		GlobalRoles: map[string]struct{}{
			RoleViewer: {},
		},
		ProjectRoles: projectRoles,
	}
}

func devPreviewResolutionForRequest(settings AuthSettings, r *http.Request) (authResolution, string, bool) {
	mode, ok := devPreviewModeForRequest(settings, r)
	if !ok {
		return authResolution{}, "", false
	}
	user := devPreviewUserForRequest(r, mode)
	if user == nil {
		return authResolution{}, mode, false
	}
	return authResolution{User: user}, mode, true
}

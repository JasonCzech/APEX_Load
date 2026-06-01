package api

import (
	"net/http"
	"strings"

	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	devViewHeader      = "X-APEXLoad-Dev-View"
	devProjectIDHeader = "X-APEXLoad-Dev-Project-ID"

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
	projectRoles := make(map[string]map[string]struct{})
	if rawProjectID := strings.TrimSpace(r.Header.Get(devProjectIDHeader)); rawProjectID != "" {
		projectRoles[normalizeProjectID(rawProjectID)] = map[string]struct{}{RoleEditor: {}}
	}
	domainRoles := map[string]map[string]struct{}{
		storage.DefaultDomainID: {RoleEditor: {}},
	}
	repositoryRoles := map[string]map[string]struct{}{
		storage.DefaultApplicationRepositoryID: {RoleEditor: {}},
	}
	return &AuthenticatedUser{
		Subject:     "dev-user",
		DisplayName: "Dev User Preview",
		Email:       "dev-user@apexload.local",
		GlobalRoles: map[string]struct{}{
			RoleViewer: {},
		},
		ProjectRoles:    projectRoles,
		DomainRoles:     domainRoles,
		RepositoryRoles: repositoryRoles,
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

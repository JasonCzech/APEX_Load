package api

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/storage"
)

const maxDesktopDevProjectIDLength = 256

var desktopDevProjectIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$`)

// DesktopDevPreviewContext is the non-secret development preview state that a
// Wails renderer may hand to a native controller request or one-time browser
// bootstrap. Fields stay private so callers cannot bypass the same validation
// used at both native trust boundaries.
type DesktopDevPreviewContext struct {
	mode      string
	projectID string
}

// ParseDesktopDevPreviewContext validates renderer-supplied preview state.
// Empty mode preserves the dashboard's documented admin default, while an
// empty project deliberately remains empty (a user preview then has only its
// global viewer role). Project IDs created by APEX Load are UUIDs; the slightly
// wider allow-list also covers stable imported/default IDs without accepting
// whitespace, URL syntax, control characters, or unbounded input.
func ParseDesktopDevPreviewContext(rawMode, rawProjectID string) (DesktopDevPreviewContext, error) {
	mode := strings.ToLower(strings.TrimSpace(rawMode))
	if mode == "" {
		mode = devViewModeAdmin
	}
	if mode != devViewModeAdmin && mode != devViewModeUser {
		return DesktopDevPreviewContext{}, fmt.Errorf("desktop development preview mode must be admin or user")
	}
	projectID := strings.TrimSpace(rawProjectID)
	if len(projectID) > maxDesktopDevProjectIDLength || (projectID != "" && !desktopDevProjectIDPattern.MatchString(projectID)) {
		return DesktopDevPreviewContext{}, fmt.Errorf("desktop development preview project ID is invalid")
	}
	return DesktopDevPreviewContext{mode: mode, projectID: projectID}, nil
}

// Mode returns the normalized admin/user mode.
func (c DesktopDevPreviewContext) Mode() string { return c.mode }

// ProjectID returns the validated active project, or an empty string when no
// project is selected.
func (c DesktopDevPreviewContext) ProjectID() string { return c.projectID }

// Apply adds the preview state to an in-process/native request. These headers
// are still honored only by the existing non-production dev-auth gate.
func (c DesktopDevPreviewContext) Apply(header http.Header) {
	if header == nil || (c.mode != devViewModeAdmin && c.mode != devViewModeUser) {
		return
	}
	header.Set(devViewHeader, c.mode)
	if c.projectID != "" {
		header.Set(devProjectIDHeader, c.projectID)
	}
}

const (
	devViewHeader      = "X-APEXLoad-Dev-View"
	devProjectIDHeader = "X-APEXLoad-Dev-Project-ID"

	devViewModeAdmin = "admin"
	devViewModeUser  = "user"
)

func devPreviewModeForRequest(settings AuthSettings, r *http.Request) (string, bool) {
	// Dev-preview headers grant ambient roles and must never be honored in a
	// hardened production deployment, regardless of persisted auth settings.
	if productionMode() {
		return "", false
	}
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

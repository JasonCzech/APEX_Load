package api

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/jasonczech/stormrunner/pkg/recorder"
	"github.com/jasonczech/stormrunner/pkg/storage"
)

const (
	RoleSystemAdmin  = "system_admin"
	RoleProjectAdmin = "project_admin"
	RoleEditor       = "editor"
	RoleViewer       = "viewer"
)

type authUserContextKey struct{}

type AuthenticatedUser struct {
	Subject      string
	DisplayName  string
	Email        string
	GlobalRoles  map[string]struct{}
	ProjectRoles map[string]map[string]struct{}
}

type accessRequirement struct {
	GlobalRoles   []string
	ProjectRoles  []string
	ProjectLookup func(*http.Request, storage.Store) (string, int, string)
}

type requestProjectLookup func(*http.Request) (string, int, string)

func authMiddleware(settings func() AuthSettings, store storage.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodOptions || !strings.HasPrefix(r.URL.Path, "/api/v1/") {
				next.ServeHTTP(w, r)
				return
			}

			requirement, ok := accessRequirementForRequest(r)
			if !ok {
				next.ServeHTTP(w, r)
				return
			}

			current := normalizeAuthSettings(settings())
			if current.Mode == authModeDisabled {
				next.ServeHTTP(w, r)
				return
			}

			user, err := authenticatedUserFromHeaders(r, current)
			if err != nil {
				writeError(w, http.StatusUnauthorized, err.Error())
				return
			}

			if len(requirement.GlobalRoles) > 0 && user.hasGlobalRole(requirement.GlobalRoles...) {
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), authUserContextKey{}, user)))
				return
			}

			if requirement.ProjectLookup != nil {
				projectID, status, msg := requirement.ProjectLookup(r, store)
				if status != 0 {
					writeError(w, status, msg)
					return
				}
				if user.hasProjectRole(projectID, requirement.ProjectRoles...) {
					next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), authUserContextKey{}, user)))
					return
				}
			}

			writeError(w, http.StatusForbidden, "forbidden")
		})
	}
}

func projectScopedAuthMiddleware(settings func() AuthSettings, globalRoles []string, projectRoles []string, projectLookup requestProjectLookup) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			current := normalizeAuthSettings(settings())
			if current.Mode == authModeDisabled {
				next.ServeHTTP(w, r)
				return
			}

			user, err := authenticatedUserFromHeaders(r, current)
			if err != nil {
				writeError(w, http.StatusUnauthorized, err.Error())
				return
			}

			if len(globalRoles) > 0 && user.hasGlobalRole(globalRoles...) {
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), authUserContextKey{}, user)))
				return
			}

			if projectLookup != nil {
				projectID, status, msg := projectLookup(r)
				if status != 0 {
					writeError(w, status, msg)
					return
				}
				if user.hasProjectRole(projectID, projectRoles...) {
					next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), authUserContextKey{}, user)))
					return
				}
			}

			writeError(w, http.StatusForbidden, "forbidden")
		})
	}
}

func AuthenticatedUserFromContext(ctx context.Context) (*AuthenticatedUser, bool) {
	user, ok := ctx.Value(authUserContextKey{}).(*AuthenticatedUser)
	return user, ok
}

func authenticatedUserFromHeaders(r *http.Request, settings AuthSettings) (*AuthenticatedUser, error) {
	subject := strings.TrimSpace(r.Header.Get(settings.UserHeader))
	if subject == "" {
		return nil, fmt.Errorf("authentication required")
	}

	return &AuthenticatedUser{
		Subject:      subject,
		DisplayName:  strings.TrimSpace(r.Header.Get(settings.NameHeader)),
		Email:        strings.TrimSpace(r.Header.Get(settings.EmailHeader)),
		GlobalRoles:  parseRoleHeader(r.Header.Get(settings.RolesHeader)),
		ProjectRoles: parseProjectRolesHeader(r.Header.Get(settings.ProjectRolesHeader)),
	}, nil
}

func (u *AuthenticatedUser) hasGlobalRole(roles ...string) bool {
	for _, role := range roles {
		if _, ok := u.GlobalRoles[role]; ok {
			return true
		}
	}
	return false
}

func (u *AuthenticatedUser) hasProjectRole(projectID string, roles ...string) bool {
	if projectID == "" {
		return false
	}
	projectRoles, ok := u.ProjectRoles[normalizeProjectID(projectID)]
	if !ok {
		return false
	}
	for _, role := range roles {
		if _, ok := projectRoles[role]; ok {
			return true
		}
	}
	return false
}

func parseRoleHeader(value string) map[string]struct{} {
	roles := make(map[string]struct{})
	for _, token := range strings.FieldsFunc(value, func(r rune) bool {
		switch r {
		case ',', '|', ' ', '\t', '\n':
			return true
		default:
			return false
		}
	}) {
		role := strings.ToLower(strings.TrimSpace(token))
		if role == "" {
			continue
		}
		roles[role] = struct{}{}
	}
	return roles
}

func parseProjectRolesHeader(value string) map[string]map[string]struct{} {
	out := make(map[string]map[string]struct{})
	for _, assignment := range strings.Split(value, ";") {
		assignment = strings.TrimSpace(assignment)
		if assignment == "" {
			continue
		}
		parts := strings.SplitN(assignment, "=", 2)
		if len(parts) != 2 {
			parts = strings.SplitN(assignment, ":", 2)
		}
		if len(parts) != 2 {
			continue
		}
		projectID := normalizeProjectID(strings.TrimSpace(parts[0]))
		roles := parseRoleHeader(parts[1])
		if projectID == "" || len(roles) == 0 {
			continue
		}
		out[projectID] = roles
	}
	return out
}

func accessRequirementForRequest(r *http.Request) (accessRequirement, bool) {
	path := r.URL.Path
	method := r.Method

	if path == "/api/v1/system/settings" {
		if method == http.MethodGet {
			return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}, true
		}
		if method == http.MethodPut {
			return accessRequirement{GlobalRoles: []string{RoleSystemAdmin}}, true
		}
	}

	if path == "/api/v1/ws" && method == http.MethodGet {
		if strings.TrimSpace(r.URL.Query().Get("project_id")) == "" {
			return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}, true
		}
		return accessRequirement{
			GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
			ProjectLookup: resolveProjectIDFromQuery,
		}, true
	}

	if path == "/api/v1/projects" {
		switch method {
		case http.MethodGet:
			return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}, true
		case http.MethodPost:
			return accessRequirement{GlobalRoles: []string{RoleProjectAdmin, RoleSystemAdmin}}, true
		}
	}

	if strings.HasPrefix(path, "/api/v1/projects/") {
		switch method {
		case http.MethodGet:
			return accessRequirement{
				GlobalRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin},
				ProjectLookup: func(r *http.Request, _ storage.Store) (string, int, string) {
					return normalizeProjectID(r.PathValue("id")), 0, ""
				},
			}, true
		case http.MethodPut, http.MethodDelete:
			return accessRequirement{
				GlobalRoles:  []string{RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles: []string{RoleProjectAdmin},
				ProjectLookup: func(r *http.Request, _ storage.Store) (string, int, string) {
					return normalizeProjectID(r.PathValue("id")), 0, ""
				},
			}, true
		case http.MethodPost:
			if strings.HasSuffix(path, "/folders") {
				return accessRequirement{
					GlobalRoles:  []string{RoleProjectAdmin, RoleSystemAdmin},
					ProjectRoles: []string{RoleProjectAdmin},
					ProjectLookup: func(r *http.Request, _ storage.Store) (string, int, string) {
						return normalizeProjectID(r.PathValue("pid")), 0, ""
					},
				}, true
			}
		}
	}

	if strings.HasPrefix(path, "/api/v1/folders/") {
		switch method {
		case http.MethodPut, http.MethodDelete:
			return accessRequirement{
				GlobalRoles:   []string{RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleProjectAdmin},
				ProjectLookup: resolveFolderProjectID,
			}, true
		}
	}

	if path == "/api/v1/applications" {
		switch method {
		case http.MethodGet:
			if strings.TrimSpace(r.URL.Query().Get("project_id")) == "" {
				return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}, true
			}
			return accessRequirement{
				GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveApplicationListProjectID,
			}, true
		case http.MethodPost:
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveApplicationCreateProjectID,
			}, true
		}
	}

	if r.PathValue("id") != "" && strings.HasPrefix(path, "/api/v1/applications/") {
		if strings.HasSuffix(path, "/releases") {
			switch method {
			case http.MethodGet:
				return accessRequirement{
					GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
					ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
					ProjectLookup: resolveApplicationProjectID,
				}, true
			case http.MethodPost:
				return accessRequirement{
					GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
					ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
					ProjectLookup: resolveApplicationProjectID,
				}, true
			}
		}

		switch method {
		case http.MethodGet:
			return accessRequirement{
				GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveApplicationProjectID,
			}, true
		case http.MethodPut, http.MethodDelete:
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveApplicationProjectID,
			}, true
		}
	}

	if r.PathValue("id") != "" && strings.HasPrefix(path, "/api/v1/releases/") {
		switch method {
		case http.MethodGet:
			return accessRequirement{
				GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveReleaseProjectID,
			}, true
		case http.MethodPut, http.MethodPost, http.MethodDelete:
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveReleaseProjectID,
			}, true
		}
	}

	if path == "/api/v1/scripts" {
		switch method {
		case http.MethodGet:
			if strings.TrimSpace(r.URL.Query().Get("project_id")) == "" && !r.URL.Query().Has("folder_id") {
				return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}, true
			}
			return accessRequirement{
				GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveScriptListProjectID,
			}, true
		case http.MethodPost:
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveScriptCreateProjectID,
			}, true
		}
	}

	if path == "/api/v1/scripts/replay" && method == http.MethodPost {
		return accessRequirement{
			GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
			ProjectLookup: resolveDraftScriptProjectID,
		}, true
	}

	if path == "/api/v1/scripts/browser-replay/sessions" && method == http.MethodPost {
		return accessRequirement{
			GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
			ProjectLookup: resolveDraftScriptProjectID,
		}, true
	}

	if path == "/api/v1/scripts/import/jmeter/save" && method == http.MethodPost {
		return accessRequirement{
			GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
			ProjectLookup: resolveJMeterSaveProjectID,
		}, true
	}

	if path == "/api/v1/scripts/import/loadrunner" && method == http.MethodPost {
		return accessRequirement{
			GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
			ProjectLookup: resolveImportProjectIDFromForm,
		}, true
	}

	if path == "/api/v1/scripts/import/loadrunner/validate" && method == http.MethodPost {
		return accessRequirement{
			GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
			ProjectLookup: resolveImportProjectIDFromForm,
		}, true
	}

	if path == "/api/v1/scripts/import/jmeter" && method == http.MethodPost {
		return accessRequirement{
			GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
			ProjectLookup: resolveImportProjectIDFromForm,
		}, true
	}

	if path == "/api/v1/scripts/import/jmeter/validate" && method == http.MethodPost {
		return accessRequirement{
			GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
			ProjectLookup: resolveImportProjectIDFromForm,
		}, true
	}

	if path == "/api/v1/scripts/import/sample" && method == http.MethodPost {
		return accessRequirement{
			GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
			ProjectLookup: resolveImportProjectIDFromJSON,
		}, true
	}

	if path == "/api/v1/data-pools" {
		switch method {
		case http.MethodGet:
			if strings.TrimSpace(r.URL.Query().Get("project_id")) == "" {
				return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}, true
			}
			return accessRequirement{
				GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveDataPoolListProjectID,
			}, true
		case http.MethodPost:
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveDataPoolCreateProjectID,
			}, true
		}
	}

	if r.PathValue("id") != "" && strings.HasPrefix(path, "/api/v1/data-pools/") {
		switch method {
		case http.MethodGet:
			return accessRequirement{
				GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveDataPoolProjectID,
			}, true
		case http.MethodPost, http.MethodPut, http.MethodDelete:
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveDataPoolProjectID,
			}, true
		}
	}

	if path == "/api/v1/tests" {
		switch method {
		case http.MethodGet:
			if strings.TrimSpace(r.URL.Query().Get("application_id")) != "" || strings.TrimSpace(r.URL.Query().Get("release_id")) != "" || strings.TrimSpace(r.URL.Query().Get("scenario_id")) != "" {
				return accessRequirement{
					GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
					ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
					ProjectLookup: resolveTestContextProjectID,
				}, true
			}
			if strings.TrimSpace(r.URL.Query().Get("project_id")) == "" && !r.URL.Query().Has("folder_id") {
				return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}, true
			}
			return accessRequirement{
				GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveTestListProjectID,
			}, true
		case http.MethodPost:
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveTestCreateProjectID,
			}, true
		}
	}

	if path == "/api/v1/scenarios" {
		switch method {
		case http.MethodGet:
			if strings.TrimSpace(r.URL.Query().Get("project_id")) == "" {
				return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}, true
			}
			return accessRequirement{
				GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveScenarioListProjectID,
			}, true
		case http.MethodPost:
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveScenarioCreateProjectID,
			}, true
		}
	}

	if r.PathValue("id") != "" && strings.HasPrefix(path, "/api/v1/scenarios/") {
		switch method {
		case http.MethodGet:
			return accessRequirement{
				GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveScenarioProjectID,
			}, true
		case http.MethodPut, http.MethodDelete:
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveScenarioProjectID,
			}, true
		}
	}

	if path == "/api/v1/tests/estimate" && method == http.MethodPost {
		return accessRequirement{
			GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
			ProjectLookup: resolveTestCreateProjectID,
		}, true
	}

	if r.PathValue("id") != "" && strings.HasPrefix(path, "/api/v1/tests/") {
		switch method {
		case http.MethodGet:
			return accessRequirement{
				GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveTestProjectID,
			}, true
		case http.MethodDelete:
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveTestProjectID,
			}, true
		case http.MethodPatch:
			lookup := resolveTestProjectID
			if strings.HasSuffix(path, "/move") {
				lookup = resolveTestMoveProjectID
			}
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: lookup,
			}, true
		case http.MethodPost:
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveTestProjectID,
			}, true
		}
	}

	if r.PathValue("id") != "" && strings.HasPrefix(path, "/api/v1/scripts/") {
		switch method {
		case http.MethodGet:
			return accessRequirement{
				GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveScriptProjectID,
			}, true
		case http.MethodPut, http.MethodDelete, http.MethodPatch:
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveScriptProjectID,
			}, true
		case http.MethodPost:
			if strings.HasSuffix(path, "/validate") || strings.HasSuffix(path, "/export") {
				return accessRequirement{
					GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
					ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
					ProjectLookup: resolveScriptProjectID,
				}, true
			}
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveScriptProjectID,
			}, true
		}
	}

	return accessRequirement{}, false
}

func resolveFolderProjectID(r *http.Request, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "folder storage is not configured"
	}
	folder, err := store.GetFolder(r.PathValue("id"))
	if err != nil {
		return "", http.StatusNotFound, "folder not found"
	}
	return normalizeProjectID(folder.ProjectID), 0, ""
}

func resolveProjectIDFromQuery(r *http.Request, _ storage.Store) (string, int, string) {
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if projectID == "" {
		return "", http.StatusBadRequest, "project_id is required"
	}
	return normalizeProjectID(projectID), 0, ""
}

func resolveApplicationListProjectID(r *http.Request, _ storage.Store) (string, int, string) {
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if projectID == "" {
		return "", http.StatusBadRequest, "project_id is required"
	}
	return normalizeProjectID(projectID), 0, ""
}

func resolveApplicationCreateProjectID(r *http.Request, _ storage.Store) (string, int, string) {
	var req struct {
		ProjectID string `json:"project_id"`
	}
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body"
	}
	return normalizeProjectID(req.ProjectID), 0, ""
}

func resolveApplicationProjectID(r *http.Request, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "application storage is not configured"
	}
	application, err := store.GetApplication(r.PathValue("id"))
	if err != nil {
		return "", http.StatusNotFound, "application not found"
	}
	return normalizeProjectID(application.ProjectID), 0, ""
}

func resolveReleaseProjectID(r *http.Request, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "release storage is not configured"
	}
	release, err := store.GetRelease(r.PathValue("id"))
	if err != nil {
		return "", http.StatusNotFound, "release not found"
	}
	application, err := store.GetApplication(release.ApplicationID)
	if err != nil {
		return "", http.StatusNotFound, "application not found"
	}
	return normalizeProjectID(application.ProjectID), 0, ""
}

func resolveScriptProjectID(r *http.Request, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "script storage is not configured"
	}
	script, err := store.GetScript(r.PathValue("id"))
	if err != nil {
		return "", http.StatusNotFound, "script not found"
	}
	return normalizeProjectID(script.ProjectID), 0, ""
}

func resolveScriptListProjectID(r *http.Request, store storage.Store) (string, int, string) {
	if projectID := strings.TrimSpace(r.URL.Query().Get("project_id")); projectID != "" {
		return normalizeProjectID(projectID), 0, ""
	}
	if !r.URL.Query().Has("folder_id") {
		return "", http.StatusBadRequest, "project_id or folder_id is required for project-scoped script access"
	}
	return resolveFolderProjectIDValue(r.URL.Query().Get("folder_id"), store)
}

func resolveTestProjectID(r *http.Request, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "test storage is not configured"
	}
	test, err := store.GetTest(r.PathValue("id"))
	if err != nil {
		return "", http.StatusNotFound, "test not found"
	}
	return normalizeProjectID(test.ProjectID), 0, ""
}

func resolveTestListProjectID(r *http.Request, store storage.Store) (string, int, string) {
	if projectID := strings.TrimSpace(r.URL.Query().Get("project_id")); projectID != "" {
		return normalizeProjectID(projectID), 0, ""
	}
	if !r.URL.Query().Has("folder_id") {
		return "", http.StatusBadRequest, "project_id or folder_id is required for project-scoped test access"
	}
	return resolveFolderProjectIDValue(r.URL.Query().Get("folder_id"), store)
}

func resolveTestContextProjectID(r *http.Request, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "test storage is not configured"
	}
	resolvedProjectID := ""
	mergeProjectID := func(candidate string) (int, string) {
		normalized := normalizeProjectID(candidate)
		if resolvedProjectID == "" {
			resolvedProjectID = normalized
			return 0, ""
		}
		if resolvedProjectID != normalized {
			return http.StatusBadRequest, "test context filters must belong to the same project"
		}
		return 0, ""
	}
	if applicationID := strings.TrimSpace(r.URL.Query().Get("application_id")); applicationID != "" {
		application, err := store.GetApplication(applicationID)
		if err != nil {
			return "", http.StatusNotFound, "application not found"
		}
		if status, msg := mergeProjectID(application.ProjectID); status != 0 {
			return "", status, msg
		}
	}
	if releaseID := strings.TrimSpace(r.URL.Query().Get("release_id")); releaseID != "" {
		releaseReq := r.Clone(r.Context())
		releaseReq.SetPathValue("id", releaseID)
		releaseProjectID, status, msg := resolveReleaseProjectID(releaseReq, store)
		if status != 0 {
			return "", status, msg
		}
		if status, msg := mergeProjectID(releaseProjectID); status != 0 {
			return "", status, msg
		}
	}
	if scenarioID := strings.TrimSpace(r.URL.Query().Get("scenario_id")); scenarioID != "" {
		scenarioReq := r.Clone(r.Context())
		scenarioReq.SetPathValue("id", scenarioID)
		scenarioProjectID, status, msg := resolveScenarioProjectID(scenarioReq, store)
		if status != 0 {
			return "", status, msg
		}
		if status, msg := mergeProjectID(scenarioProjectID); status != 0 {
			return "", status, msg
		}
	}
	if resolvedProjectID == "" {
		return "", http.StatusBadRequest, "application_id, release_id, or scenario_id is required"
	}
	return resolvedProjectID, 0, ""
}

func resolveTestCreateProjectID(r *http.Request, store storage.Store) (string, int, string) {
	var req CreateTestRequest
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}

	requestedProjectID := normalizeProjectID(req.ProjectID)
	folderProjectID := ""
	if req.FolderID != nil {
		projectID, status, msg := resolveFolderProjectIDValue(*req.FolderID, store)
		if status != 0 {
			return "", status, msg
		}
		folderProjectID = projectID
		if req.ProjectID != "" && requestedProjectID != folderProjectID {
			return "", http.StatusBadRequest, "project_id does not match folder project"
		}
	}

	inferredProjectID, status, msg := resolveCreateTestScriptProjectID(req, store)
	if status != 0 {
		return "", status, msg
	}
	if inferredProjectID != "" {
		if req.ProjectID != "" && requestedProjectID != inferredProjectID {
			return "", http.StatusBadRequest, "project_id does not match test scripts"
		}
		if folderProjectID != "" && folderProjectID != inferredProjectID {
			return "", http.StatusBadRequest, "folder project does not match test scripts"
		}
	}

	if folderProjectID != "" {
		return folderProjectID, 0, ""
	}
	if req.ProjectID != "" {
		return requestedProjectID, 0, ""
	}
	if inferredProjectID != "" {
		return inferredProjectID, 0, ""
	}
	return normalizeProjectID(""), 0, ""
}

func resolveCreateTestScriptProjectID(req CreateTestRequest, store storage.Store) (string, int, string) {
	scriptIDs := make([]string, 0, len(req.Groups)+1)
	if req.ScriptID != "" {
		scriptIDs = append(scriptIDs, req.ScriptID)
	}
	for _, group := range req.Groups {
		if group.ScriptID != "" {
			scriptIDs = append(scriptIDs, group.ScriptID)
		}
	}
	projectID := ""
	for _, scriptID := range scriptIDs {
		script, err := store.GetScript(scriptID)
		if err != nil {
			return "", http.StatusNotFound, "script not found"
		}
		candidate := normalizeProjectID(script.ProjectID)
		if projectID == "" {
			projectID = candidate
			continue
		}
		if candidate != projectID {
			return "", http.StatusBadRequest, "test scripts span multiple projects"
		}
	}
	return projectID, 0, ""
}

func resolveTestMoveProjectID(r *http.Request, store storage.Store) (string, int, string) {
	projectID, status, msg := resolveTestProjectID(r, store)
	if status != 0 {
		return "", status, msg
	}
	var req struct {
		FolderID *string `json:"folder_id"`
	}
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	if req.FolderID == nil {
		return projectID, 0, ""
	}
	destinationProjectID, destStatus, destMsg := resolveFolderProjectIDValue(*req.FolderID, store)
	if destStatus != 0 {
		return "", destStatus, destMsg
	}
	if destinationProjectID != projectID {
		return "", http.StatusBadRequest, "destination folder belongs to a different project"
	}
	return projectID, 0, ""
}

func resolveScenarioProjectID(r *http.Request, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "scenario storage is not configured"
	}
	_, scripts, err := store.GetScenario(r.PathValue("id"))
	if err != nil {
		return "", http.StatusNotFound, "scenario not found"
	}
	return resolveProjectIDFromScenarioScripts(scripts, store)
}

func resolveScenarioListProjectID(r *http.Request, _ storage.Store) (string, int, string) {
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if projectID == "" {
		return "", http.StatusBadRequest, "project_id is required for project-scoped scenario access"
	}
	return normalizeProjectID(projectID), 0, ""
}

func resolveScenarioCreateProjectID(r *http.Request, store storage.Store) (string, int, string) {
	var req CreateScenarioRequest
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	scripts := make([]storage.ScenarioScript, 0, len(req.Groups))
	for index, group := range req.Groups {
		scripts = append(scripts, storage.ScenarioScript{Order: index, ScriptID: group.ScriptID})
	}
	return resolveProjectIDFromScenarioScripts(scripts, store)
}

func resolveProjectIDFromScenarioScripts(scripts []storage.ScenarioScript, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "script storage is not configured"
	}
	projectID := ""
	for _, scenarioScript := range scripts {
		if strings.TrimSpace(scenarioScript.ScriptID) == "" {
			continue
		}
		script, err := store.GetScript(scenarioScript.ScriptID)
		if err != nil {
			return "", http.StatusNotFound, "script not found"
		}
		candidate := normalizeProjectID(script.ProjectID)
		if projectID == "" {
			projectID = candidate
			continue
		}
		if candidate != projectID {
			return "", http.StatusBadRequest, "scenario scripts span multiple projects"
		}
	}
	if projectID == "" {
		return normalizeProjectID(""), 0, ""
	}
	return projectID, 0, ""
}

func resolveDraftScriptProjectID(r *http.Request, _ storage.Store) (string, int, string) {
	var req struct {
		Script *struct {
			ProjectID string `json:"project_id,omitempty"`
		} `json:"script,omitempty"`
	}
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	if req.Script == nil {
		return "", http.StatusBadRequest, "script is required"
	}
	return normalizeProjectID(req.Script.ProjectID), 0, ""
}

func resolveJMeterSaveProjectID(r *http.Request, _ storage.Store) (string, int, string) {
	var req struct {
		Scripts []struct {
			ProjectID string `json:"project_id,omitempty"`
		} `json:"scripts"`
	}
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	if len(req.Scripts) == 0 {
		return "", http.StatusBadRequest, "at least one script is required"
	}
	projectID := ""
	for _, script := range req.Scripts {
		candidate := normalizeProjectID(script.ProjectID)
		if projectID == "" {
			projectID = candidate
			continue
		}
		if candidate != projectID {
			return "", http.StatusBadRequest, "imported scripts span multiple projects"
		}
	}
	return projectID, 0, ""
}

func resolveDataPoolListProjectID(r *http.Request, _ storage.Store) (string, int, string) {
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if projectID == "" {
		return "", http.StatusBadRequest, "project_id is required for project-scoped data pool access"
	}
	return normalizeProjectID(projectID), 0, ""
}

func resolveDataPoolCreateProjectID(r *http.Request, _ storage.Store) (string, int, string) {
	var req struct {
		ProjectID string `json:"project_id,omitempty"`
	}
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	if strings.TrimSpace(req.ProjectID) == "" {
		return "", http.StatusBadRequest, "project_id is required for project-scoped data pool access"
	}
	return normalizeProjectID(req.ProjectID), 0, ""
}

func resolveDataPoolProjectID(r *http.Request, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "storage is not configured"
	}
	pool, err := store.GetDataPool(r.PathValue("id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", http.StatusNotFound, "data pool not found"
		}
		return "", http.StatusInternalServerError, "failed to load data pool"
	}
	return normalizeProjectID(pool.ProjectID), 0, ""
}

func resolveRecorderListProjectID(r *http.Request) (string, int, string) {
	projectID := normalizeProjectID(r.URL.Query().Get("project_id"))
	if projectID == "" {
		return "", http.StatusBadRequest, "project_id is required for project-scoped recorder access"
	}
	return projectID, 0, ""
}

func resolveRecorderCreateProjectID(r *http.Request) (string, int, string) {
	var req struct {
		ProjectID string `json:"project_id,omitempty"`
	}
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	projectID := normalizeProjectID(req.ProjectID)
	if projectID == "" {
		return "", http.StatusBadRequest, "project_id is required for project-scoped recorder access"
	}
	return projectID, 0, ""
}

func resolveRecorderSessionProjectID(rec *recorder.Recorder) requestProjectLookup {
	return func(r *http.Request) (string, int, string) {
		if rec == nil {
			return "", http.StatusServiceUnavailable, "recorder is not configured"
		}
		session, ok := rec.GetSession(r.PathValue("id"))
		if !ok {
			return "", http.StatusNotFound, "recording session not found"
		}
		return normalizeProjectID(session.ProjectID), 0, ""
	}
}

func resolveBrowserReplaySessionProjectID(manager *BrowserReplayManager) requestProjectLookup {
	return func(r *http.Request) (string, int, string) {
		if manager == nil {
			return "", http.StatusServiceUnavailable, "browser replay is not configured"
		}
		session, ok := manager.Get(r.PathValue("session_id"))
		if !ok {
			return "", http.StatusNotFound, "browser replay session not found"
		}
		return normalizeProjectID(session.ProjectID), 0, ""
	}
}

func resolveImportProjectIDFromForm(r *http.Request, _ storage.Store) (string, int, string) {
	if projectID := normalizeProjectID(r.URL.Query().Get("project_id")); projectID != "" {
		return projectID, 0, ""
	}
	if err := r.ParseMultipartForm(50 << 20); err == nil {
		if projectID := normalizeProjectID(r.FormValue("project_id")); projectID != "" {
			return projectID, 0, ""
		}
	}
	return "", http.StatusBadRequest, "project_id is required for project-scoped import access"
}

func resolveImportProjectIDFromJSON(r *http.Request, _ storage.Store) (string, int, string) {
	var req struct {
		ProjectID string `json:"project_id,omitempty"`
	}
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	projectID := normalizeProjectID(req.ProjectID)
	if projectID == "" {
		return "", http.StatusBadRequest, "project_id is required for project-scoped import access"
	}
	return projectID, 0, ""
}

func resolveScriptCreateProjectID(r *http.Request, store storage.Store) (string, int, string) {
	var req struct {
		ProjectID string  `json:"project_id,omitempty"`
		FolderID  *string `json:"folder_id,omitempty"`
	}
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	if req.FolderID != nil {
		return resolveFolderProjectIDValue(*req.FolderID, store)
	}
	return normalizeProjectID(req.ProjectID), 0, ""
}

func resolveFolderProjectIDValue(folderID string, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "folder storage is not configured"
	}
	folder, err := store.GetFolder(folderID)
	if err != nil {
		return "", http.StatusNotFound, "folder not found"
	}
	return normalizeProjectID(folder.ProjectID), 0, ""
}

func decodeRequestBody(r *http.Request, target any) error {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	if len(bytes.TrimSpace(body)) == 0 {
		return io.EOF
	}
	return json.Unmarshal(body, target)
}

func corsMiddleware(allowedOrigins []string) func(http.Handler) http.Handler {
	originSet := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		originSet[strings.ToLower(o)] = true
	}
	allowAll := len(allowedOrigins) == 0

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			if allowAll || originSet[strings.ToLower(origin)] {
				w.Header().Set("Access-Control-Allow-Origin", origin)
			} else if allowAll {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			}

			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-ID")
			w.Header().Set("Access-Control-Max-Age", "86400")

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(rw, r)

		slog.Info("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"remote", r.RemoteAddr,
		)
	})
}

type responseWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (rw *responseWriter) WriteHeader(code int) {
	if !rw.wrote {
		rw.status = code
		rw.wrote = true
	}
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.wrote {
		rw.wrote = true
	}
	return rw.ResponseWriter.Write(b)
}

func (rw *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := rw.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("upstream ResponseWriter does not implement http.Hijacker")
	}
	return h.Hijack()
}

func (rw *responseWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

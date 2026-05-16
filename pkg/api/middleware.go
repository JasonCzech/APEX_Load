package api

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

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
				GlobalRoles:  []string{RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles: []string{RoleProjectAdmin},
				ProjectLookup: resolveFolderProjectID,
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

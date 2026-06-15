package api

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	RoleSystemAdmin  = "system_admin"
	RoleProjectAdmin = "project_admin"
	RoleEditor       = "editor"
	RoleViewer       = "viewer"
)

type authUserContextKey struct{}
type authServiceAccountContextKey struct{}

type AuthenticatedUser struct {
	ID              string
	Subject         string
	DisplayName     string
	Email           string
	GlobalRoles     map[string]struct{}
	ProjectRoles    map[string]map[string]struct{}
	DomainRoles     map[string]map[string]struct{}
	RepositoryRoles map[string]map[string]struct{}
}

type AuthenticatedServiceAccount struct {
	ID          string
	Name        string
	Description string
	Scopes      []string
}

type accessRequirement struct {
	AuthenticatedOnly bool
	GlobalRoles       []string
	ProjectRoles      []string
	ProjectLookup     func(*http.Request, storage.Store) (string, int, string)
	DomainRoles       []string
	DomainLookup      func(*http.Request, storage.Store) (string, int, string)
	RepositoryRoles   []string
	RepositoryLookup  func(*http.Request, storage.Store) (string, int, string)
	MaxBodyBytes      int64
}

type requestProjectLookup func(*http.Request) (string, int, string)

type requestCoordinationStore interface {
	RateLimitStore
	IdempotencyStore
}

type authResolution struct {
	User           *AuthenticatedUser
	ServiceAccount *AuthenticatedServiceAccount
}

func authMiddleware(settings func() AuthSettings, store storage.Store, coordination requestCoordinationStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodOptions || !strings.HasPrefix(r.URL.Path, "/api/v1/") {
				next.ServeHTTP(w, r)
				return
			}

			requirement, ok := accessRequirementForRequest(r)
			if !ok {
				// Fail closed: a request that reached the auth middleware (an auth()-wrapped
				// route) but has no catalog entry is a configuration bug. Denying is safe —
				// TestGenericAuthAPIRoutesDeclareAccessRequirement guarantees every auth-wrapped
				// /api/v1/ route is cataloged, so this only rejects unknown/forgotten paths
				// rather than serving them without an access check.
				writeError(w, http.StatusForbidden, "no access policy is configured for this route")
				return
			}

			current := normalizeAuthSettings(settings())
			if resolution, _, ok := devPreviewResolutionForRequest(current, r); ok {
				if authorizeAccessRequirement(w, r, store, resolution, requirement) {
					next.ServeHTTP(w, withAuthenticatedContext(r, resolution))
				}
				return
			}
			if isAuthBypassMode(current.Mode) {
				next.ServeHTTP(w, r)
				return
			}

			resolution, err := resolveRequestAuth(r, current, store)
			if err != nil {
				writeError(w, http.StatusUnauthorized, err.Error())
				return
			}
			if !enforceServiceAccountRateLimit(w, r, coordination, resolution.ServiceAccount) {
				return
			}
			if authorizeAccessRequirement(w, r, store, resolution, requirement) {
				if !enforceIdempotencyKey(w, r, coordination, resolution) {
					return
				}
				next.ServeHTTP(w, withAuthenticatedContext(r, resolution))
			}
		})
	}
}

func projectScopedAuthMiddleware(settings func() AuthSettings, store storage.Store, coordination requestCoordinationStore, globalRoles []string, projectRoles []string, projectLookup requestProjectLookup) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			current := normalizeAuthSettings(settings())
			requirement := accessRequirement{
				GlobalRoles:  globalRoles,
				ProjectRoles: projectRoles,
			}
			if projectLookup != nil {
				requirement.ProjectLookup = func(r *http.Request, _ storage.Store) (string, int, string) {
					return projectLookup(r)
				}
			}
			if resolution, _, ok := devPreviewResolutionForRequest(current, r); ok {
				if authorizeAccessRequirement(w, r, store, resolution, requirement) {
					next.ServeHTTP(w, withAuthenticatedContext(r, resolution))
				}
				return
			}
			if isAuthBypassMode(current.Mode) {
				next.ServeHTTP(w, r)
				return
			}

			resolution, err := resolveRequestAuth(r, current, store)
			if err != nil {
				writeError(w, http.StatusUnauthorized, err.Error())
				return
			}
			if !enforceServiceAccountRateLimit(w, r, coordination, resolution.ServiceAccount) {
				return
			}
			if authorizeAccessRequirement(w, r, store, resolution, requirement) {
				if !enforceIdempotencyKey(w, r, coordination, resolution) {
					return
				}
				next.ServeHTTP(w, withAuthenticatedContext(r, resolution))
			}
		})
	}
}

func enforceServiceAccountRateLimit(w http.ResponseWriter, r *http.Request, store RateLimitStore, account *AuthenticatedServiceAccount) bool {
	if store == nil || account == nil {
		return true
	}
	limit := configuredAPIKeyRateLimitPerMinute()
	if limit <= 0 {
		return true
	}
	allowed, remaining, err := store.AllowRateLimit(r.Context(), "service-account:"+account.ID, limit, time.Minute)
	if err != nil {
		slog.Warn("service account rate-limit check failed", "service_account_id", account.ID, "err", err)
		writeError(w, http.StatusServiceUnavailable, "service account rate-limit store unavailable")
		return false
	}
	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limit))
	w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
	if !allowed {
		writeError(w, http.StatusTooManyRequests, "service account rate limit exceeded")
		return false
	}
	return true
}

func enforceIdempotencyKey(w http.ResponseWriter, r *http.Request, store IdempotencyStore, resolution authResolution) bool {
	if store == nil || !isMutatingMethod(r.Method) {
		return true
	}
	rawKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if rawKey == "" {
		return true
	}
	actor := "anonymous"
	if resolution.ServiceAccount != nil {
		actor = "service-account:" + resolution.ServiceAccount.ID
	} else if resolution.User != nil && strings.TrimSpace(resolution.User.Subject) != "" {
		actor = "user:" + strings.TrimSpace(resolution.User.Subject)
	}
	scope := strings.Join([]string{actor, r.Method, r.URL.Path, rawKey}, "|")
	reserved, err := store.ReserveIdempotencyKey(r.Context(), scope, configuredIdempotencyKeyTTL())
	if err != nil {
		slog.Warn("idempotency-key reservation failed", "actor", actor, "path", r.URL.Path, "err", err)
		writeError(w, http.StatusServiceUnavailable, "idempotency store unavailable")
		return false
	}
	if !reserved {
		writeError(w, http.StatusConflict, "duplicate idempotency key")
		return false
	}
	return true
}

func isMutatingMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func configuredIdempotencyKeyTTL() time.Duration {
	return firstConfiguredDuration(15*time.Minute, "APEXLOAD_IDEMPOTENCY_KEY_TTL", "APEXLOAD_IDEMPOTENCY_KEY_TTL_SECONDS")
}

func configuredAPIKeyRateLimitPerMinute() int {
	raw := strings.TrimSpace(os.Getenv("APEXLOAD_API_KEY_RATE_LIMIT_PER_MINUTE"))
	if raw == "" {
		return 0
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 0 {
		return 0
	}
	return limit
}

func authorizeAccessRequirement(w http.ResponseWriter, r *http.Request, store storage.Store, resolution authResolution, requirement accessRequirement) bool {
	user := resolution.User
	if user == nil {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return false
	}

	if len(requirement.GlobalRoles) > 0 && user.hasGlobalRole(requirement.GlobalRoles...) {
		if !serviceAccountAllowsRequest(resolution.ServiceAccount, r) {
			writeError(w, http.StatusForbidden, "service account token scope does not allow this operation")
			return false
		}
		return true
	}

	if requirement.AuthenticatedOnly {
		if !serviceAccountAllowsRequest(resolution.ServiceAccount, r) {
			writeError(w, http.StatusForbidden, "service account token scope does not allow this operation")
			return false
		}
		return true
	}

	if requirement.ProjectLookup != nil {
		projectID, status, msg := requirement.ProjectLookup(r, store)
		if status != 0 {
			writeError(w, status, msg)
			return false
		}
		if user.hasProjectRole(projectID, requirement.ProjectRoles...) {
			if !serviceAccountAllowsRequest(resolution.ServiceAccount, r) {
				writeError(w, http.StatusForbidden, "service account token scope does not allow this operation")
				return false
			}
			return true
		}
	}

	if requirement.DomainLookup != nil {
		domainID, status, msg := requirement.DomainLookup(r, store)
		if status != 0 {
			writeError(w, status, msg)
			return false
		}
		if user.hasDomainRole(domainID, requirement.DomainRoles...) {
			if !serviceAccountAllowsRequest(resolution.ServiceAccount, r) {
				writeError(w, http.StatusForbidden, "service account token scope does not allow this operation")
				return false
			}
			return true
		}
	}

	if requirement.RepositoryLookup != nil {
		repositoryID, status, msg := requirement.RepositoryLookup(r, store)
		if status != 0 {
			writeError(w, status, msg)
			return false
		}
		if user.hasRepositoryRole(repositoryID, requirement.RepositoryRoles...) {
			if !serviceAccountAllowsRequest(resolution.ServiceAccount, r) {
				writeError(w, http.StatusForbidden, "service account token scope does not allow this operation")
				return false
			}
			return true
		}
	}

	writeError(w, http.StatusForbidden, "forbidden")
	return false
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
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := strings.TrimSpace(r.Header.Get("Origin"))

			// Only ever reflect an Origin we have explicitly validated. When no
			// allow-list is configured this permits same-origin requests only —
			// never an arbitrary reflected origin.
			if origin != "" && originAllowed(allowedOrigins, r) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Add("Vary", "Origin")
			}

			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-ID, X-APEXLoad-API-Key, X-APEXLoad-Dev-View, X-APEXLoad-Dev-Project-ID")
			w.Header().Set("Access-Control-Max-Age", "86400")

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// defaultMaxRequestBodyBytes is a global backstop for request body size. It is
// set above the largest explicit per-handler http.MaxBytesReader limit (100 MiB
// for Enterprise Script Archive imports) so handlers with their own, smaller limit always win,
// while handlers that decode the body without any limit are still bounded.
const (
	k6ImportMaxBodyBytes                int64 = 10 << 20
	recorderImportMaxBodyBytes          int64 = 10 << 20
	oracleReviewImportMaxBodyBytes      int64 = 10 << 20
	webAPIImportMaxBodyBytes            int64 = 20 << 20
	harImportMaxBodyBytes               int64 = 50 << 20
	jmeterImportMaxBodyBytes            int64 = 50 << 20
	enterpriseArchiveImportMaxBodyBytes int64 = 100 << 20
	defaultMaxRequestBodyBytes          int64 = 128 << 20
)

// bodyLimitMiddleware caps request body size as a defense against memory-
// exhaustion DoS on handlers that decode the body without their own limit.
func bodyLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Limit only methods that carry a body; leave GET/HEAD (incl. the
		// WebSocket upgrade, which hijacks the connection) untouched.
		if r.Body != nil && isMutatingMethod(r.Method) {
			r.Body = http.MaxBytesReader(w, r.Body, requestMaxBodyBytes(r))
		}
		next.ServeHTTP(w, r)
	})
}

func requestMaxBodyBytes(r *http.Request) int64 {
	limit := defaultMaxRequestBodyBytes
	if requirement, ok := accessRequirementForRequest(r); ok && requirement.MaxBodyBytes > 0 && requirement.MaxBodyBytes < limit {
		limit = requirement.MaxBodyBytes
	}
	return limit
}

// securityHeadersMiddleware applies baseline security response headers to every
// response (API, dashboard, and WebSocket handshakes). HSTS is only emitted on
// requests that arrived over TLS (directly or via a TLS-terminating proxy).
func securityHeadersMiddleware(next http.Handler) http.Handler {
	// The bundled dashboard loads the Inter webfont from Google Fonts; allow
	// those specific origins for stylesheet and font fetches.
	const csp = "default-src 'self'; " +
		"script-src 'self'; " +
		"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; " +
		"img-src 'self' data: blob:; " +
		"font-src 'self' data: https://fonts.gstatic.com; " +
		"connect-src 'self' ws: wss: http://127.0.0.1:* http://localhost:* ws://127.0.0.1:* ws://localhost:*; " +
		"worker-src 'self' blob:; " +
		"object-src 'none'; " +
		"base-uri 'self'; " +
		"form-action 'self'; " +
		"frame-ancestors 'none'"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", csp)
		if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
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

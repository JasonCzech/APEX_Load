package api

// audit_auth_events.go — authentication/authorization audit events emitted from the
// auth middleware (denials and trusted-header logins). These are the failed-access and
// session events an enterprise audit log exists to capture; the middleware previously
// returned 401/403 with no trail. A small in-memory throttle prevents an unauthenticated
// bot from flooding the audit table while still recording the first hit per window.

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	authDenialDedupWindow = time.Minute
	authLoginDedupWindow  = time.Hour
	authAuditThrottleCap  = 4096
)

// auditThrottle deduplicates noisy audit events by key within a time window. The
// zero value is ready to use. It is bounded: when it grows past the cap it resets,
// trading a little extra logging for a hard memory ceiling.
type auditThrottle struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

func (t *auditThrottle) allow(key string, now time.Time, window time.Duration) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.seen == nil {
		t.seen = make(map[string]time.Time)
	}
	if last, ok := t.seen[key]; ok && now.Sub(last) < window {
		return false
	}
	if len(t.seen) > authAuditThrottleCap {
		t.seen = make(map[string]time.Time)
	}
	t.seen[key] = now
	return true
}

// resolveAuthEventActor derives the actor for a middleware-level audit event from the
// resolution (which is populated before the request context is). Falls back to the
// trusted-header subject, then anonymous, when no principal resolved.
func (s *Server) resolveAuthEventActor(r *http.Request, resolution authResolution) (string, string, string) {
	if resolution.ServiceAccount != nil {
		return "service_account", "service-account:" + resolution.ServiceAccount.ID, resolution.ServiceAccount.Name
	}
	if resolution.User != nil {
		return "user", resolution.User.Subject, resolution.User.DisplayName
	}
	if s.runner != nil {
		header := normalizeAuthSettings(s.runner.GetSystemSettings().Auth).UserHeader
		if header != "" {
			if subject := strings.TrimSpace(r.Header.Get(header)); subject != "" {
				return "user", subject, ""
			}
		}
	}
	return "anonymous", "anonymous", ""
}

func (s *Server) recordAuthDenial(r *http.Request, resolution authResolution, status int, reason string) {
	if s == nil || s.store == nil {
		return
	}
	actorType, actorSubject, actorDisplayName := s.resolveAuthEventActor(r, resolution)
	if !s.authAudit.allow(authDenialThrottleKey(r, authDenialTrustedIdentity(resolution), status, reason), time.Now().UTC(), authDenialDedupWindow) {
		return
	}
	_ = s.store.SaveAuditEvent(&storage.AuditEvent{
		ID:               uuid.New().String(),
		Action:           auditActionAccessDenied,
		ActorType:        actorType,
		ActorSubject:     actorSubject,
		ActorDisplayName: actorDisplayName,
		ResourceType:     "api_route",
		ResourceID:       r.Method + " " + r.URL.Path,
		Status:           "failure",
		Details: map[string]any{
			"status": status,
			"method": r.Method,
			"path":   r.URL.Path,
			"reason": reason,
		},
		CreatedAt: time.Now().UTC(),
	})
}

// authDenialTrustedIdentity returns a stable identity for a denial ONLY when the
// request resolved to a real principal (a known user or service account). An
// attacker-supplied but unresolved subject (401 "unknown user subject") returns
// "", so the throttle falls back to the remote host and cannot be flooded by
// rotating the claimed subject.
func authDenialTrustedIdentity(resolution authResolution) string {
	if resolution.ServiceAccount != nil {
		return "service-account:" + resolution.ServiceAccount.ID
	}
	if resolution.User != nil {
		return "user:" + strings.TrimSpace(resolution.User.Subject)
	}
	return ""
}

func authDenialThrottleKey(r *http.Request, trustedIdentity string, status int, reason string) string {
	method := ""
	if r != nil {
		method = r.Method
	}
	// Bucket a denial for a resolved principal by that principal: the platform's
	// primary deployment is trusted-header auth behind a proxy, where every
	// request's RemoteAddr is the proxy IP — keying on RemoteAddr collapses all
	// real users' denials into one bucket and hides per-insider forensic detail.
	// The path is deliberately NOT in the key (in either branch): its cardinality
	// is unbounded and attacker-controlled, so including it would let a single
	// authenticated user (or an anonymous source) flood the audit log by rotating
	// path segments. The full path is still recorded in the audit event's Details;
	// the throttle only bounds duplicate (principal, method, status, reason)
	// denials per window. Unresolved/anonymous requests fall back to the remote
	// host as the identity.
	identity := trustedIdentity
	if identity == "" {
		identity = authDenialRemoteHost(r)
	}
	return strings.Join([]string{identity, method, strconv.Itoa(status), coarseAuthDenialReason(reason), "denied"}, "|")
}

func authDenialRemoteHost(r *http.Request) string {
	if r == nil {
		return "unknown"
	}
	remote := strings.TrimSpace(r.RemoteAddr)
	if host, _, err := net.SplitHostPort(remote); err == nil && strings.TrimSpace(host) != "" {
		return strings.TrimSpace(host)
	}
	if remote == "" {
		return "unknown"
	}
	return remote
}

func coarseAuthDenialReason(reason string) string {
	reason = strings.ToLower(strings.TrimSpace(reason))
	if reason == "" {
		return "unspecified"
	}
	if strings.Contains(reason, "role") || strings.Contains(reason, "permission") || strings.Contains(reason, "forbidden") {
		return "forbidden"
	}
	if strings.Contains(reason, "subject") || strings.Contains(reason, "user") || strings.Contains(reason, "account") {
		return "principal"
	}
	if strings.Contains(reason, "token") || strings.Contains(reason, "session") || strings.Contains(reason, "auth") {
		return "authentication"
	}
	return "other"
}

func (s *Server) recordAuthLogin(r *http.Request, resolution authResolution) {
	if s == nil || s.store == nil || resolution.User == nil {
		return
	}
	if !s.authAudit.allow(resolution.User.Subject+"|login", time.Now().UTC(), authLoginDedupWindow) {
		return
	}
	_ = s.store.SaveAuditEvent(&storage.AuditEvent{
		ID:               uuid.New().String(),
		Action:           "auth.session.login",
		ActorType:        "user",
		ActorSubject:     resolution.User.Subject,
		ActorDisplayName: resolution.User.DisplayName,
		ResourceType:     "session",
		ResourceID:       resolution.User.Subject,
		Status:           "success",
		Details:          map[string]any{"mode": authModeTrustedHeaders},
		CreatedAt:        time.Now().UTC(),
	})
}

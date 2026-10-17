package recordbridge

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/recorder"
)

const (
	bridgeTokenHeader       = "X-APEXLoad-Bridge-Token"
	bridgeSessionHeader     = "X-APEXLoad-Bridge-Session"
	bridgeSessionQueryParam = "bridge_session_id"
	defaultBridgePairingTTL = 10 * time.Minute
	defaultBridgeSessionTTL = 12 * time.Hour
	bridgeStatusPath        = "/api/v1/bridge/status"
	bridgePairPath          = "/api/v1/bridge/pair"
	bridgeSystemHealthPath  = "/api/v1/system/health"
	bridgeWebSocketPath     = "/api/v1/ws"
	bridgePairMaxFailures   = 5
	bridgePairTokenMinChars = 32
)

var (
	bridgePairFailureWindow = time.Minute
	bridgePairLockout       = time.Minute
)

// BrowserRecorderBridgeConfig configures the loopback-only browser recording
// bridge used by the APEX Load Recording Agent.
type BrowserRecorderBridgeConfig struct {
	Addr           string
	PairingToken   string
	PairingTTL     time.Duration
	SessionTTL     time.Duration
	AllowedOrigins []string
	// CADir holds the recording proxy's certificate authority. A device
	// trusts it once, so the agent keeps it in one place across runs; empty
	// uses the recorder's default (APEXLOAD_RECORDER_CA_DIR or the working
	// directory's artifacts/recorder).
	CADir           string
	Recording       RecordingManagerOptions
	Replay          ReplayManagerOptions
	DependencyProbe func() error
}

type BrowserRecordingBridge struct {
	mu             sync.RWMutex
	token          string
	startedAt      time.Time
	pairingExpires time.Time
	sessionTTL     time.Duration
	allowedOrigins map[string]struct{}
	sessions       map[string]bridgeSession
	pairFailures   map[string]bridgePairFailure
	server         *Bridge
	// device is the device capture (device_capture.go).
	device *DeviceCapture
}

type bridgeSession struct {
	Origin    string    `json:"origin"`
	ExpiresAt time.Time `json:"expires_at"`
}

type bridgePairFailure struct {
	Count        int
	ResetAt      time.Time
	BlockedUntil time.Time
}

// GenerateBrowserRecorderBridgeToken returns a URL-safe secret suitable for
// one bridge process.
func GenerateBrowserRecorderBridgeToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// NewBrowserRecorderBridgeServer creates a slim localhost API surface for the
// browser recorder. It intentionally does not mount the full controller API or
// start the HTTP proxy, generator discovery, metrics listener, or usage loops.

// bridgeRecorder is the agent's recording proxy: it listens only while a
// device capture runs, and serves the device its setup page.
func bridgeRecorder(caDir string) *recorder.Recorder {
	var rec *recorder.Recorder
	if caDir = strings.TrimSpace(caDir); caDir != "" {
		rec = recorder.NewWithCAFiles("", filepath.Join(caDir, recorder.DefaultCACertFile), "")
	} else {
		rec = recorder.New("")
	}
	rec.SetDeviceSetup(true)
	return rec
}

func (b *BrowserRecordingBridge) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin != "" && b.corsOriginAllowed(origin, r.URL.Path) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Add("Vary", "Origin")
			if strings.EqualFold(strings.TrimSpace(r.Header.Get("Access-Control-Request-Private-Network")), "true") {
				w.Header().Set("Access-Control-Allow-Private-Network", "true")
				w.Header().Add("Vary", "Access-Control-Request-Private-Network")
			}
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-ID, X-APEXLoad-API-Key, X-APEXLoad-Dev-View, X-APEXLoad-Dev-Project-ID, "+bridgeTokenHeader+", "+bridgeSessionHeader)
		w.Header().Set("Access-Control-Max-Age", "600")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (b *BrowserRecordingBridge) corsOriginAllowed(origin, path string) bool {
	if path == bridgePairPath {
		return true
	}
	return b.originAllowed(origin)
}

func (b *BrowserRecordingBridge) originAllowed(origin string) bool {
	if strings.TrimSpace(origin) == "" {
		return true
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	_, ok := b.allowedOrigins[origin]
	return ok
}

func (b *BrowserRecordingBridge) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions || r.URL.Path == bridgeSystemHealthPath {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == bridgeWebSocketPath {
			if !b.authorizeWebSocket(r) {
				writeError(w, http.StatusUnauthorized, "browser recorder bridge pairing session is invalid or expired")
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if !b.authorizeTokenRequest(r) {
			writeError(w, http.StatusUnauthorized, "browser recorder bridge token is missing or invalid")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (b *BrowserRecordingBridge) authorizeTokenRequest(r *http.Request) bool {
	token := strings.TrimSpace(r.Header.Get(bridgeTokenHeader))
	if !constantTimeEqual(token, b.token) {
		return false
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	sessionID := strings.TrimSpace(r.Header.Get(bridgeSessionHeader))
	now := time.Now()
	b.mu.RLock()
	defer b.mu.RUnlock()
	if sessionID != "" {
		session, ok := b.sessions[sessionID]
		return ok && origin != "" && strings.EqualFold(session.Origin, origin) && !now.After(session.ExpiresAt)
	}
	expired := now.After(b.pairingExpires)
	_, originOK := b.allowedOrigins[origin]
	if expired {
		return false
	}
	return origin == "" || originOK
}

func (b *BrowserRecordingBridge) authorizeWebSocket(r *http.Request) bool {
	sessionID := strings.TrimSpace(r.URL.Query().Get(bridgeSessionQueryParam))
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if sessionID == "" || origin == "" {
		return false
	}
	b.mu.RLock()
	session, ok := b.sessions[sessionID]
	b.mu.RUnlock()
	if !ok || !strings.EqualFold(session.Origin, origin) || time.Now().After(session.ExpiresAt) {
		return false
	}
	return true
}

func constantTimeEqual(a, b string) bool {
	if a == "" || b == "" || len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func (b *BrowserRecordingBridge) handlePair(w http.ResponseWriter, r *http.Request) {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		writeError(w, http.StatusBadRequest, "pairing requires a browser Origin")
		return
	}
	now := time.Now()
	pairKey := bridgePairLimitKey(r, origin)
	if retryAt, limited := b.pairRateLimited(pairKey, now); limited {
		writeRetryAfter(w, retryAt, now)
		writeError(w, http.StatusTooManyRequests, "too many invalid browser recorder bridge pairing attempts; retry after the lockout window")
		return
	}
	if now.After(b.pairingExpires) {
		writeError(w, http.StatusUnauthorized, "browser recorder bridge pairing token has expired; restart the APEX Load Recording Agent")
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if !constantTimeEqual(strings.TrimSpace(req.Token), b.token) {
		if retryAt, limited := b.recordPairFailure(pairKey, now); limited {
			writeRetryAfter(w, retryAt, now)
			writeError(w, http.StatusTooManyRequests, "too many invalid browser recorder bridge pairing attempts; retry after the lockout window")
			return
		}
		writeError(w, http.StatusUnauthorized, "browser recorder bridge token is missing or invalid")
		return
	}
	sessionID, err := GenerateBrowserRecorderBridgeToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create bridge pairing session")
		return
	}
	expiresAt := time.Now().UTC().Add(b.sessionTTL)
	b.mu.Lock()
	b.allowedOrigins[origin] = struct{}{}
	b.sessions[sessionID] = bridgeSession{Origin: origin, ExpiresAt: expiresAt}
	b.pruneExpiredSessionsLocked(time.Now())
	delete(b.pairFailures, pairKey)
	b.mu.Unlock()
	b.syncHubAllowedOrigins()

	slog.Info("browser recorder bridge paired", "origin", origin, "expires_at", expiresAt)
	writeJSON(w, http.StatusOK, b.statusPayload(origin, sessionID, expiresAt))
}

func (b *BrowserRecordingBridge) handleStatus(w http.ResponseWriter, r *http.Request) {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	writeJSON(w, http.StatusOK, b.statusPayload(origin, "", time.Time{}))
}

func (b *BrowserRecordingBridge) statusPayload(origin, sessionID string, sessionExpiresAt time.Time) map[string]any {
	paired := false
	if origin != "" {
		paired = b.originAllowed(origin)
	}
	availability := b.server.service.availability()
	active := b.server.service.Recording.Active()
	bridge := map[string]any{
		"paired":             paired,
		"origin":             origin,
		"api_origin":         b.server.BrowserURL(),
		"started_at":         b.startedAt,
		"pairing_expires_at": b.pairingExpires,
		"availability":       availability,
		"active":             active,
	}
	if sessionID != "" {
		bridge["session_id"] = sessionID
		bridge["session_expires_at"] = sessionExpiresAt
	}
	return map[string]any{"bridge": bridge}
}

func (b *BrowserRecordingBridge) pruneExpiredSessionsLocked(now time.Time) {
	for id, session := range b.sessions {
		if now.After(session.ExpiresAt) {
			delete(b.sessions, id)
		}
	}
}

func (b *BrowserRecordingBridge) pairRateLimited(key string, now time.Time) (time.Time, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	failure, ok := b.pairFailures[key]
	if !ok {
		return time.Time{}, false
	}
	if !failure.BlockedUntil.IsZero() && now.Before(failure.BlockedUntil) {
		return failure.BlockedUntil, true
	}
	if now.After(failure.ResetAt) {
		delete(b.pairFailures, key)
	}
	return time.Time{}, false
}

func (b *BrowserRecordingBridge) recordPairFailure(key string, now time.Time) (time.Time, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	failure := b.pairFailures[key]
	if failure.ResetAt.IsZero() || now.After(failure.ResetAt) {
		failure = bridgePairFailure{ResetAt: now.Add(bridgePairFailureWindow)}
	}
	failure.Count++
	if failure.Count >= bridgePairMaxFailures {
		failure.BlockedUntil = now.Add(bridgePairLockout)
		b.pairFailures[key] = failure
		return failure.BlockedUntil, true
	}
	b.pairFailures[key] = failure
	return time.Time{}, false
}

func bridgePairLimitKey(r *http.Request, _ string) string {
	remote := strings.TrimSpace(r.RemoteAddr)
	if host, _, err := net.SplitHostPort(remote); err == nil && host != "" {
		remote = host
	}
	if remote == "" {
		remote = "unknown"
	}
	// Key the pairing rate limit on the remote IP only. The Origin header is
	// attacker-controlled for any non-browser client, so including it let a local
	// process reset the 5-failures-per-minute budget for every invented Origin
	// and brute-force the pairing token unthrottled from a single IP.
	return remote
}

func writeRetryAfter(w http.ResponseWriter, retryAt, now time.Time) {
	delay := time.Until(retryAt).Seconds()
	if !now.IsZero() {
		delay = retryAt.Sub(now).Seconds()
	}
	if delay < 1 {
		delay = 1
	}
	w.Header().Set("Retry-After", fmt.Sprintf("%.0f", delay))
}

func (b *BrowserRecordingBridge) syncHubAllowedOrigins() {
	if b == nil || b.server == nil || b.server.hub == nil {
		return
	}
	b.mu.RLock()
	origins := make([]string, 0, len(b.allowedOrigins))
	for origin := range b.allowedOrigins {
		origins = append(origins, origin)
	}
	b.mu.RUnlock()
	b.server.hub.SetAllowedOrigins(origins)
}

// ValidateBrowserRecorderBridgePairingToken rejects manual bridge tokens that
// are short enough to be practical brute-force targets against the CORS-open
// pairing endpoint. Generated tokens are 32 random bytes encoded as base64url.
func ValidateBrowserRecorderBridgePairingToken(token string) error {
	token = strings.TrimSpace(token)
	if len(token) < bridgePairTokenMinChars {
		return fmt.Errorf("must be at least %d characters", bridgePairTokenMinChars)
	}
	if strings.ContainsAny(token, " \t\r\n") {
		return fmt.Errorf("must not contain whitespace")
	}
	return nil
}

const (
	BridgeTokenHeader   = bridgeTokenHeader
	BridgeSessionHeader = bridgeSessionHeader
	BridgePairPath      = bridgePairPath
	BridgeStatusPath    = bridgeStatusPath
)

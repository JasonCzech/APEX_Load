package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/recorder"
)

const (
	bridgeTokenHeader       = "X-APEXLoad-Bridge-Token"
	bridgeSessionQueryParam = "bridge_session_id"
	defaultBridgePairingTTL = 10 * time.Minute
	defaultBridgeSessionTTL = 12 * time.Hour
	bridgeStatusPath        = "/api/v1/bridge/status"
	bridgePairPath          = "/api/v1/bridge/pair"
	bridgeSystemHealthPath  = "/api/v1/system/health"
	bridgeWebSocketPath     = "/api/v1/ws"
)

// BrowserRecorderBridgeConfig configures the loopback-only browser recording
// bridge used by the APEX Load Recording Agent.
type BrowserRecorderBridgeConfig struct {
	Addr           string
	PairingToken   string
	PairingTTL     time.Duration
	SessionTTL     time.Duration
	AllowedOrigins []string
}

type BrowserRecordingBridge struct {
	mu             sync.RWMutex
	token          string
	startedAt      time.Time
	pairingExpires time.Time
	sessionTTL     time.Duration
	allowedOrigins map[string]struct{}
	sessions       map[string]bridgeSession
	server         *Server
}

type bridgeSession struct {
	Origin    string    `json:"origin"`
	ExpiresAt time.Time `json:"expires_at"`
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
func NewBrowserRecorderBridgeServer(cfg BrowserRecorderBridgeConfig) *Server {
	if strings.TrimSpace(cfg.Addr) == "" {
		cfg.Addr = "127.0.0.1:0"
	}
	if cfg.PairingTTL <= 0 {
		cfg.PairingTTL = defaultBridgePairingTTL
	}
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = defaultBridgeSessionTTL
	}
	token := strings.TrimSpace(cfg.PairingToken)
	if token == "" {
		generated, err := GenerateBrowserRecorderBridgeToken()
		if err != nil {
			// rand.Read should not fail under normal OS entropy sources; keep the
			// failure visible rather than silently creating a weak bridge secret.
			panic(fmt.Sprintf("generate browser recorder bridge token: %v", err))
		}
		token = generated
	}

	hub := NewHub()
	runner := NewTestRunner(1, hub, nil)
	s := &Server{
		hub:              hub,
		runner:           runner,
		browserReplay:    NewBrowserReplayManager(hub),
		scriptDebug:      NewScriptDebugManager(hub),
		browserRecording: NewBrowserRecordingManager(hub),
		browserInstall:   newBrowserInstallState(),
		recorder:         recorder.New(""),
		mux:              http.NewServeMux(),
		bridgeMode:       true,
	}
	bridge := &BrowserRecordingBridge{
		token:          token,
		startedAt:      time.Now().UTC(),
		pairingExpires: time.Now().UTC().Add(cfg.PairingTTL),
		sessionTTL:     cfg.SessionTTL,
		allowedOrigins: make(map[string]struct{}),
		sessions:       make(map[string]bridgeSession),
		server:         s,
	}
	for _, origin := range cfg.AllowedOrigins {
		origin = strings.TrimSpace(origin)
		if origin != "" && origin != "*" {
			bridge.allowedOrigins[origin] = struct{}{}
		}
	}
	s.recordingBridge = bridge
	bridge.syncHubAllowedOrigins()
	s.bridgeRoutes()
	s.httpServer = &http.Server{
		Addr:           cfg.Addr,
		Handler:        securityHeadersMiddleware(bodyLimitMiddleware(s.mux)),
		ReadTimeout:    15 * time.Second,
		WriteTimeout:   60 * time.Second,
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}
	return s
}

func (s *Server) BridgePairingToken() string {
	if s == nil || s.recordingBridge == nil {
		return ""
	}
	return s.recordingBridge.token
}

func (s *Server) BridgePairingExpiresAt() time.Time {
	if s == nil || s.recordingBridge == nil {
		return time.Time{}
	}
	return s.recordingBridge.pairingExpires
}

func (s *Server) bridgeRoutes() {
	bridge := s.recordingBridge
	if bridge == nil {
		return
	}
	cors := bridge.corsMiddleware
	log := loggingMiddleware
	auth := bridge.authMiddleware

	s.mux.Handle("GET "+bridgeSystemHealthPath, cors(log(http.HandlerFunc(s.handleHealthCheck))))
	s.mux.Handle("POST "+bridgePairPath, cors(log(http.HandlerFunc(bridge.handlePair))))
	s.mux.Handle("GET "+bridgeStatusPath, cors(log(auth(http.HandlerFunc(bridge.handleStatus)))))
	s.mux.Handle("GET "+bridgeWebSocketPath, log(auth(http.HandlerFunc(s.handleWebSocket))))

	s.mux.Handle("POST /api/v1/browser-recorder/sessions", cors(log(auth(http.HandlerFunc(s.handleStartBrowserRecording)))))
	s.mux.Handle("GET /api/v1/browser-recorder/sessions/{id}", cors(log(auth(http.HandlerFunc(s.handleGetBrowserRecording)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions/{id}/stop", cors(log(auth(http.HandlerFunc(s.handleStopBrowserRecording)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions/{id}/force-stop", cors(log(auth(http.HandlerFunc(s.handleForceStopBrowserRecording)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions/{id}/pause", cors(log(auth(http.HandlerFunc(s.handlePauseBrowserRecording)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions/{id}/resume", cors(log(auth(http.HandlerFunc(s.handleResumeBrowserRecording)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions/{id}/navigate", cors(log(auth(http.HandlerFunc(s.handleNavigateBrowserRecording)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions/{id}/focus", cors(log(auth(http.HandlerFunc(s.handleFocusBrowserRecording)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions/{id}/control-panel", cors(log(auth(http.HandlerFunc(s.handleOpenBrowserRecordingControlPanel)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions/{id}/transactions/start", cors(log(auth(http.HandlerFunc(s.handleStartBrowserRecordingTransaction)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions/{id}/transactions/end", cors(log(auth(http.HandlerFunc(s.handleEndBrowserRecordingTransaction)))))
	s.mux.Handle("GET /api/v1/browser-recorder/sessions/{id}/transactions/suggestions", cors(log(auth(http.HandlerFunc(s.handleSuggestBrowserRecordingTransactions)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions/{id}/notes", cors(log(auth(http.HandlerFunc(s.handleAddBrowserRecordingNote)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions/{id}/section", cors(log(auth(http.HandlerFunc(s.handleSetBrowserRecordingSection)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions/{id}/pick-mode", cors(log(auth(http.HandlerFunc(s.handleSetBrowserRecordingPickMode)))))
	s.mux.Handle("DELETE /api/v1/browser-recorder/sessions/{id}/steps/last", cors(log(auth(http.HandlerFunc(s.handleDeleteLastBrowserRecordingStep)))))
	s.mux.Handle("PATCH /api/v1/browser-recorder/sessions/{id}/steps/{seq}", cors(log(auth(http.HandlerFunc(s.handleUpdateBrowserRecordingStep)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions/{id}/verify", cors(log(auth(http.HandlerFunc(s.handleVerifyBrowserRecording)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions/{id}/convert/preview", cors(log(auth(http.HandlerFunc(s.handlePreviewBrowserRecordingConversion)))))
	s.mux.Handle("POST /api/v1/browser-recorder/sessions/{id}/convert", cors(log(auth(http.HandlerFunc(s.handleConvertBrowserRecording)))))
	s.mux.Handle("GET /api/v1/browser-recorder/sessions/{id}/export", cors(log(auth(http.HandlerFunc(s.handleExportBrowserRecording)))))
	s.mux.Handle("GET /api/v1/browser-recorder/sessions/{id}/storage-state", cors(log(auth(http.HandlerFunc(s.handleExportBrowserRecordingStorageState)))))
	s.mux.Handle("GET /api/v1/browser-recorder/devices", cors(log(auth(http.HandlerFunc(s.handleListBrowserRecorderDevices)))))
	s.mux.Handle("POST /api/v1/browser-recorder/install", cors(log(auth(http.HandlerFunc(s.handleBrowserRecorderInstall)))))
	s.mux.Handle("GET /api/v1/browser-recorder/install", cors(log(auth(http.HandlerFunc(s.handleGetBrowserRecorderInstall)))))
	s.mux.Handle("OPTIONS /api/", cors(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})))
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
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-ID, X-APEXLoad-API-Key, X-APEXLoad-Dev-View, X-APEXLoad-Dev-Project-ID, "+bridgeTokenHeader)
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
	return origin == "" || b.originAllowed(origin)
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
	if time.Now().After(b.pairingExpires) {
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
	availability := b.server.browserRecordingAvailability()
	active := b.server.browserRecording.Active()
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

package recordbridge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/buildinfo"
	recbrowser "github.com/jasonczech/apexload/pkg/recorder/browser"
)

// Bridge owns only the local recording HTTP surface and its process-scoped
// managers. It has no controller runner, durable store, scheduler or dashboard.
type Bridge struct {
	service      *Service
	pairing      *BrowserRecordingBridge
	hub          *bridgeHub
	mux          *http.ServeMux
	httpServer   *http.Server
	mu           sync.RWMutex
	started      bool
	stopping     bool
	boundAddr    string
	shutdownDone chan struct{}
	shutdownErr  error
}

func NewBrowserRecorderBridgeServer(cfg BrowserRecorderBridgeConfig) *Bridge {
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
		var err error
		token, err = GenerateBrowserRecorderBridgeToken()
		if err != nil {
			panic(fmt.Sprintf("generate browser recorder bridge token: %v", err))
		}
	} else if err := ValidateBrowserRecorderBridgePairingToken(token); err != nil {
		panic(fmt.Sprintf("invalid browser recorder bridge pairing token: %v", err))
	}
	hub := newBridgeHub()
	probe := cfg.DependencyProbe
	if probe == nil {
		probe = recbrowser.CheckChromiumInstalled
	}
	if cfg.Replay.ReserveID == nil {
		cfg.Replay.ReserveID = newLocalReplayReservations().reserve
	}
	service := &Service{Recording: NewBrowserRecordingManager(hub, cfg.Recording), Replay: NewBrowserReplayManager(hub, cfg.Replay), Recorder: bridgeRecorder(cfg.CADir), Install: NewInstallState(), Retention: DefaultBrowserRecordingRetention, Device: &DeviceCapture{}, CanExportRaw: func(*http.Request, string) bool { return true }, Availability: func() BrowserRecordingAvailability { return RecordingAvailability(true, false, probe) }}
	configureStandaloneConversion(service)
	bridge := &Bridge{service: service, hub: hub, mux: http.NewServeMux()}
	pairing := &BrowserRecordingBridge{token: token, startedAt: time.Now().UTC(), pairingExpires: time.Now().UTC().Add(cfg.PairingTTL), sessionTTL: cfg.SessionTTL, allowedOrigins: map[string]struct{}{}, sessions: map[string]bridgeSession{}, pairFailures: map[string]bridgePairFailure{}, server: bridge, device: service.Device}
	for _, origin := range cfg.AllowedOrigins {
		origin = strings.TrimSpace(origin)
		if origin != "" && origin != "*" {
			pairing.allowedOrigins[origin] = struct{}{}
		}
	}
	bridge.pairing = pairing
	pairing.syncHubAllowedOrigins()
	bridge.routes()
	bridge.httpServer = &http.Server{Addr: cfg.Addr, Handler: bridgeHTTPMiddleware(bridge.mux), ReadTimeout: 15 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second, MaxHeaderBytes: 1 << 20}
	return bridge
}
func (b *Bridge) Handler() http.Handler                      { return b.httpServer.Handler }
func (b *Bridge) RecordingManager() *BrowserRecordingManager { return b.service.Recording }
func (b *Bridge) Availability() BrowserRecordingAvailability { return b.service.availability() }
func (b *Bridge) BridgePairingToken() string {
	if b == nil {
		return ""
	}
	return b.pairing.token
}
func (b *Bridge) BridgePairingExpiresAt() time.Time {
	if b == nil {
		return time.Time{}
	}
	return b.pairing.pairingExpires
}
func (b *Bridge) BoundAddr() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.boundAddr != "" {
		return b.boundAddr
	}
	return b.httpServer.Addr
}
func (b *Bridge) BoundAddrReady() bool { b.mu.RLock(); defer b.mu.RUnlock(); return b.boundAddr != "" }
func (b *Bridge) BrowserURL() string {
	addr := b.BoundAddr()
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	if strings.HasPrefix(addr, "[::]:") {
		addr = "127.0.0.1:" + strings.TrimPrefix(addr, "[::]:")
	}
	if strings.HasPrefix(addr, "0.0.0.0:") {
		addr = "127.0.0.1:" + strings.TrimPrefix(addr, "0.0.0.0:")
	}
	return "http://" + addr
}
func (b *Bridge) Start() error {
	b.mu.Lock()
	if b.started || b.stopping {
		b.mu.Unlock()
		return http.ErrServerClosed
	}
	b.started = true
	b.mu.Unlock()
	listener, err := net.Listen("tcp", b.httpServer.Addr)
	if err != nil {
		return err
	}
	b.mu.Lock()
	if b.stopping {
		b.mu.Unlock()
		_ = listener.Close()
		return http.ErrServerClosed
	}
	b.boundAddr = listener.Addr().String()
	b.service.Replay.StartJanitor(context.Background())
	b.service.Recording.StartJanitor(context.Background())
	b.mu.Unlock()
	slog.Info("apexload browser recording bridge starting", "addr", b.httpServer.Addr, "bound_addr", b.BoundAddr())
	err = b.httpServer.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
func (b *Bridge) Shutdown(ctx context.Context) error {
	if b == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	b.mu.Lock()
	if !b.stopping {
		b.stopping = true
		b.shutdownDone = make(chan struct{})
		go b.close(ctx)
	}
	done := b.shutdownDone
	b.mu.Unlock()
	select {
	case <-done:
		b.mu.RLock()
		defer b.mu.RUnlock()
		return b.shutdownErr
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (b *Bridge) close(ctx context.Context) {
	// Closing admission and all live sockets is bounded; manager cleanup proceeds
	// independently so one late browser cannot prevent the other from stopping.
	err := b.httpServer.Shutdown(ctx)
	if err != nil {
		err = errors.Join(err, b.httpServer.Close())
	}
	b.hub.Stop()
	var group sync.WaitGroup
	group.Add(2)
	go func() { defer group.Done(); b.service.Replay.Close() }()
	go func() { defer group.Done(); b.service.Recording.Close() }()
	err = errors.Join(err, b.service.Recorder.StopProxy())
	group.Wait()
	b.mu.Lock()
	b.boundAddr = ""
	b.shutdownErr = err
	close(b.shutdownDone)
	b.mu.Unlock()
}
func (b *Bridge) health(w http.ResponseWriter, r *http.Request) {
	build := buildinfo.Get()
	WriteJSON(w, http.StatusOK, map[string]any{"status": "healthy", "version": build.Version, "display_version": build.DisplayVersion, "commit": build.Commit, "revision": build.Revision, "environment": build.Environment, "build_time": build.BuildTime, "ws_clients": b.hub.ClientCount(), "test_count": 0, "capacity_availability": map[string]any{"source": "configured", "configured_available_generators": 1, "effective_available_generators": 1, "live_available_generators": nil, "reserved_generators": 0, "shared_available_generators": 0, "warnings": nil}})
}
func (s *Service) HandleGetBrowserReplaySession(w http.ResponseWriter, r *http.Request) {
	session, ok := s.Replay.GetForProject(r.PathValue("session_id"), s.authorizedProject(r, ""))
	if !ok {
		s.writeError(w, http.StatusNotFound, "browser replay session not found")
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"session": session})
}

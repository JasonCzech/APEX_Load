package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

type countingDebugAuthAudit struct{ denials atomic.Int32 }

func (a *countingDebugAuthAudit) recordAuthDenial(*http.Request, authResolution, int, string) {
	a.denials.Add(1)
}

type stuckLegacyWebSocketStore struct {
	storage.Store
	entered chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (s *stuckLegacyWebSocketStore) GetServiceAccount(string) (*storage.ServiceAccount, error) {
	s.calls.Add(1)
	s.entered <- struct{}{}
	<-s.release
	return nil, errors.New("unavailable")
}

func TestDebugWebSocketAdmissionBoundsLegacyUpgradeSnapshots(t *testing.T) {
	server := NewServer(ServerConfig{})
	store := &stuckLegacyWebSocketStore{entered: make(chan struct{}, maxConcurrentDebugWSAuthChecks), release: make(chan struct{})}
	server.store = store
	responses := make(chan int, maxConcurrentDebugWSAuthChecks)
	cancels := make([]context.CancelFunc, 0, maxConcurrentDebugWSAuthChecks)
	for i := 0; i < maxConcurrentDebugWSAuthChecks; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		cancels = append(cancels, cancel)
		request := httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil).WithContext(ctx)
		request = withAuthenticatedContext(request, authResolution{User: &AuthenticatedUser{ID: "user"}, ServiceAccount: &AuthenticatedServiceAccount{ID: "account"}})
		request = request.WithContext(context.WithValue(request.Context(), websocketAuthSettingsContextKey{}, AuthSettings{Mode: authModeTrustedHeaders}))
		go func() {
			recorder := httptest.NewRecorder()
			server.handleWebSocket(recorder, request)
			responses <- recorder.Code
		}()
	}
	for i := 0; i < maxConcurrentDebugWSAuthChecks; i++ {
		select {
		case <-store.entered:
		case <-time.After(time.Second):
			t.Fatal("legacy snapshot did not enter store")
		}
	}
	for _, cancel := range cancels {
		cancel()
	}
	for range cancels {
		select {
		case status := <-responses:
			if status != http.StatusServiceUnavailable {
				t.Fatalf("canceled upgrade status = %d", status)
			}
		case <-time.After(time.Second):
			t.Fatal("canceled upgrade waited for legacy store")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if release, err := server.acquireDebugWSAuth(ctx); err == nil {
		release()
		t.Fatal("stuck legacy reads released their slots prematurely")
	}
	if got := store.calls.Load(); got != maxConcurrentDebugWSAuthChecks {
		t.Fatalf("legacy reads = %d", got)
	}
	close(store.release)
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	release, err := server.acquireDebugWSAuth(ctx)
	if err != nil {
		t.Fatalf("legacy completion did not release slot: %v", err)
	}
	release()
}

func TestDebugWebSocketEventAdmissionBoundsAndRecovers(t *testing.T) {
	server := NewServer(ServerConfig{})
	entered := make(chan struct{}, maxConcurrentDebugWSAuthChecks)
	unblock := make(chan struct{})
	authorize := func(ctx context.Context, project string, editor, bounded bool) bool {
		if !editor || !bounded {
			t.Error("event check did not request bounded editor authorization")
		}
		entered <- struct{}{}
		select {
		case <-unblock:
			return project == "editor"
		case <-ctx.Done():
			return false
		}
	}
	results := make(chan bool, maxConcurrentDebugWSAuthChecks)
	for i := 0; i < maxConcurrentDebugWSAuthChecks; i++ {
		go func() { results <- server.debugWSMayDeliver(context.Background(), "editor", authorize) }()
	}
	for i := 0; i < maxConcurrentDebugWSAuthChecks; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("event check did not enter")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if server.debugWSMayDeliver(ctx, "editor", authorize) {
		t.Fatal("canceled event disclosed")
	}
	select {
	case <-entered:
		t.Fatal("extra event started an authority read")
	default:
	}
	close(unblock)
	for i := 0; i < maxConcurrentDebugWSAuthChecks; i++ {
		if !<-results {
			t.Fatal("admitted editor denied")
		}
	}
	if server.debugWSMayDeliver(context.Background(), "viewer", authorize) {
		t.Fatal("viewer received debugger data")
	}
	if !server.debugWSMayDeliver(context.Background(), "editor", authorize) {
		t.Fatal("editor did not recover after capacity released")
	}
}
func (*countingDebugAuthAudit) recordAuthLogin(*http.Request, authResolution) {}

type blockingDebugAuthAdmissionStore struct {
	entered chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (s *blockingDebugAuthAdmissionStore) GetSetting(string) (string, error) { return "", nil }
func (s *blockingDebugAuthAdmissionStore) SaveSetting(string, string) error  { return nil }
func (s *blockingDebugAuthAdmissionStore) GetSettingContext(ctx context.Context, _ string) (string, error) {
	s.calls.Add(1)
	s.entered <- struct{}{}
	select {
	case <-s.release:
		return "", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func TestDebugHTTPAuthAdmissionBoundsReadsAndLeavesWebSocketPathSeparate(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "development")
	server := NewServer(ServerConfig{})
	store := &blockingDebugAuthAdmissionStore{
		entered: make(chan struct{}, maxConcurrentDebugHTTPAuthReads+1),
		release: make(chan struct{}),
	}
	server.runner.settingsStore = store
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	responses := make(chan int, maxConcurrentDebugHTTPAuthReads)
	for i := 0; i < maxConcurrentDebugHTTPAuthReads; i++ {
		go func() {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/api/v1/script-debug/sessions/not-found", nil).WithContext(ctx)
			server.mux.ServeHTTP(recorder, request)
			responses <- recorder.Code
		}()
	}
	for i := 0; i < maxConcurrentDebugHTTPAuthReads; i++ {
		select {
		case <-store.entered:
		case <-time.After(time.Second):
			t.Fatal("admitted debugger read did not enter the store")
		}
	}

	// An unauthenticated burst against either route class must fail closed
	// without starting another durable read or waiting for the stalled ones.
	for _, path := range []string{"/api/v1/script-debug/sessions/not-found", "/api/v1/scripts/debug/sessions"} {
		recorder := httptest.NewRecorder()
		method := http.MethodGet
		if path == "/api/v1/scripts/debug/sessions" {
			method = http.MethodPost
		}
		server.mux.ServeHTTP(recorder, httptest.NewRequest(method, path, nil))
		if recorder.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s: status = %d, want 503", path, recorder.Code)
		}
	}
	if got := store.calls.Load(); got != maxConcurrentDebugHTTPAuthReads {
		t.Fatalf("store calls = %d, want %d", got, maxConcurrentDebugHTTPAuthReads)
	}

	// WebSocket delivery does not consume the HTTP slots. This checks logical
	// admission only; an underlying shared database pool can still be busy.
	wsDone := make(chan error, 1)
	go func() {
		_, err := server.websocketDurableAuthSettingsContext(ctx)
		wsDone <- err
	}()
	select {
	case <-store.entered:
	case <-time.After(time.Second):
		t.Fatal("WebSocket settings read was blocked by HTTP admission")
	}
	close(store.release)
	if err := <-wsDone; err != nil {
		t.Fatalf("WebSocket settings read: %v", err)
	}
	for i := 0; i < maxConcurrentDebugHTTPAuthReads; i++ {
		if code := <-responses; code == http.StatusServiceUnavailable {
			t.Fatal("an admitted debugger request failed admission")
		}
	}
	// All slots return after completion, including when clients cancel.
	if _, err := server.debugHTTPAuthSettings(context.Background()); err != nil {
		t.Fatalf("completed requests did not release admission slots: %v", err)
	}
}

func TestDebugHTTPAuthAdmissionReleasesCanceledRead(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "development")
	server := NewServer(ServerConfig{})
	store := &blockingDebugAuthAdmissionStore{entered: make(chan struct{}, 1), release: make(chan struct{})}
	server.runner.settingsStore = store
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := server.debugHTTPAuthSettings(ctx)
		done <- err
	}()
	<-store.entered
	cancel()
	if err := <-done; err == nil {
		t.Fatal("canceled settings read succeeded")
	}
	close(store.release)
	if _, err := server.debugHTTPAuthSettings(context.Background()); err != nil {
		t.Fatalf("canceled request did not release its slot: %v", err)
	}
}

func TestDebugHTTPAuthAdmissionSkipsAlreadyCanceledRequest(t *testing.T) {
	server := NewServer(ServerConfig{})
	store := &blockingDebugAuthAdmissionStore{entered: make(chan struct{}, 1), release: make(chan struct{})}
	server.runner.settingsStore = store
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := server.debugHTTPAuthSettings(ctx); err == nil {
		t.Fatal("already-canceled settings read succeeded")
	}
	if got := store.calls.Load(); got != 0 {
		t.Fatalf("already-canceled request started %d store reads", got)
	}
}

func TestDebugHTTPSettingsFailureAvoidsAuditWriteButDenialsAreAudited(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "development")
	for _, scoped := range []bool{false, true} {
		audit := &countingDebugAuthAudit{}
		unavailable := true
		settings := func() AuthSettings { return AuthSettings{Mode: authModeTrustedHeaders} }
		fresh := func(context.Context) (AuthSettings, error) {
			if unavailable {
				return AuthSettings{}, errors.New("store unavailable")
			}
			return settings(), nil
		}
		options := []authMiddlewareOption{withAuthAuditRecorder(audit), withDebugAuthSettings(fresh)}
		var middleware func(http.Handler) http.Handler
		path, method := "/api/v1/scripts/debug/sessions", http.MethodPost
		if scoped {
			path, method = "/api/v1/script-debug/sessions/not-found", http.MethodGet
			middleware = projectScopedAuthMiddleware(settings, nil, nil, []string{RoleEditor}, []string{RoleEditor}, nil, options...)
		} else {
			middleware = authMiddleware(settings, nil, nil, options...)
		}
		handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(method, path, nil))
		if response.Code != http.StatusServiceUnavailable || audit.denials.Load() != 0 {
			t.Fatalf("scoped=%t: status=%d audit calls=%d, want 503 and no audit write", scoped, response.Code, audit.denials.Load())
		}
		unavailable = false
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(method, path, nil))
		if response.Code != http.StatusUnauthorized || audit.denials.Load() != 1 {
			t.Fatalf("scoped=%t: status=%d audit calls=%d, want 401 and one audit write", scoped, response.Code, audit.denials.Load())
		}
	}
}

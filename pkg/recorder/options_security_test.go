package recorder

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestNewWithOptionsStrictAuthDoesNotUseEnvironmentFallback(t *testing.T) {
	t.Setenv(envProxyAuthToken, "ambient-token")
	rec := NewWithOptions(Options{
		ProxyAddr:        "127.0.0.1:0",
		RequireProxyAuth: true,
	})

	if got := rec.ProxyAuthToken(); got != "" {
		t.Fatalf("strict proxy token = %q, want empty without explicit token", got)
	}
	if err := rec.validateProxyStartup(); err == nil || !strings.Contains(err.Error(), "authentication token is required") {
		t.Fatalf("validateProxyStartup() = %v, want required-token error", err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
	if rec.authorizeProxyRequest(req) {
		t.Fatal("strict proxy authorized an unauthenticated request")
	}
	if err := rec.StartProxy(); err == nil {
		t.Fatal("strict proxy bound without an explicit token")
	}
	if rec.ProxyRunning() {
		t.Fatal("strict proxy is running after required-token startup failure")
	}
}

func TestNewWithOptionsStrictModeRejectsWeakTokenAndWideBind(t *testing.T) {
	weak := NewWithOptions(Options{
		ProxyAddr:        "127.0.0.1:0",
		ProxyAuthToken:   "short-token",
		RequireProxyAuth: true,
		RequireLoopback:  true,
	})
	if err := weak.validateProxyStartup(); err == nil || !strings.Contains(err.Error(), "at least") {
		t.Fatalf("weak token validation = %v, want minimum-length error", err)
	}

	wide := NewWithOptions(Options{
		ProxyAddr:        "0.0.0.0:0",
		ProxyAuthToken:   strings.Repeat("t", minStrictProxyAuthTokenBytes),
		RequireProxyAuth: true,
		RequireLoopback:  true,
	})
	if err := wide.validateProxyStartup(); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("wide bind validation = %v, want loopback error", err)
	}
}

func TestNewWithOptionsStrictLoopbackIsSafeProductionOptIn(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "production")
	t.Setenv(envProxyAllowProduction, "")
	rec := NewWithOptions(Options{
		ProxyAddr:        "127.0.0.1:0",
		ProxyAuthToken:   strings.Repeat("t", minStrictProxyAuthTokenBytes),
		RequireProxyAuth: true,
		RequireLoopback:  true,
	})
	if err := rec.validateProxyStartup(); err != nil {
		t.Fatalf("strict protected loopback proxy rejected in production: %v", err)
	}

	hosted := NewWithOptions(Options{
		ProxyAddr:      "127.0.0.1:0",
		ProxyAuthToken: strings.Repeat("t", minStrictProxyAuthTokenBytes),
	})
	if err := hosted.validateProxyStartup(); err == nil || !strings.Contains(err.Error(), envProxyAllowProduction) {
		t.Fatalf("hosted production proxy validation = %v, want explicit production opt-in error", err)
	}
}

func TestNewWithOptionsHostedDefaultsRemainEnvironmentBacked(t *testing.T) {
	caDir := t.TempDir()
	t.Setenv(envProxyAuthToken, "hosted-token")
	t.Setenv("APEXLOAD_RECORDER_CA_DIR", caDir)

	rec := NewWithOptions(Options{})
	if got := rec.ProxyAddr(); got != defaultProxyAddr {
		t.Fatalf("ProxyAddr() = %q, want %q", got, defaultProxyAddr)
	}
	if got := rec.ProxyAuthToken(); got != "hosted-token" {
		t.Fatalf("ProxyAuthToken() = %q, want environment token", got)
	}
	if got, want := rec.CACertPath(), filepath.Join(caDir, defaultRecorderCACert); got != want {
		t.Fatalf("CACertPath() = %q, want %q", got, want)
	}
	if got, want := rec.CAKeyPath(), filepath.Join(caDir, defaultRecorderCAKey); got != want {
		t.Fatalf("CAKeyPath() = %q, want %q", got, want)
	}

	explicit := NewWithOptions(Options{ProxyAuthToken: "explicit-token"})
	if got := explicit.ProxyAuthToken(); got != "explicit-token" {
		t.Fatalf("explicit ProxyAuthToken() = %q, want explicit token", got)
	}
}

func TestNewWithOptionsResolvesExplicitCAPathsTogether(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "desktop-recorder-ca.pem")
	keyPath := filepath.Join(dir, "desktop-recorder-key.pem")
	rec := NewWithOptions(Options{CACertPath: certPath, CAKeyPath: keyPath})
	if got := rec.CACertPath(); got != certPath {
		t.Fatalf("CACertPath() = %q, want %q", got, certPath)
	}
	if got := rec.CAKeyPath(); got != keyPath {
		t.Fatalf("CAKeyPath() = %q, want %q", got, keyPath)
	}

	derived := NewWithOptions(Options{CACertPath: certPath})
	if got, want := derived.CAKeyPath(), filepath.Join(dir, defaultRecorderCAKey); got != want {
		t.Fatalf("derived CAKeyPath() = %q, want %q", got, want)
	}
}

func TestProtectedProxyEphemeralLoopbackEnforcesToken(t *testing.T) {
	allowPrivateProxyTargets(t)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()

	rec := NewWithOptions(Options{
		ProxyAddr:        "127.0.0.1:0",
		ProxyAuthToken:   strings.Repeat("r", minStrictProxyAuthTokenBytes),
		RequireProxyAuth: true,
		RequireLoopback:  true,
	})
	if err := rec.StartProxy(); err != nil {
		t.Fatalf("StartProxy(): %v", err)
	}
	proxyAddr := rec.ProxyAddr()
	host, port, err := net.SplitHostPort(proxyAddr)
	if err != nil {
		t.Fatalf("ProxyAddr() %q is not host:port: %v", proxyAddr, err)
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() || port == "0" || port == "" {
		t.Fatalf("ProxyAddr() = %q, want bound ephemeral loopback address", proxyAddr)
	}

	for _, tc := range []struct {
		name       string
		token      string
		wantStatus int
	}{
		{name: "missing", wantStatus: http.StatusProxyAuthRequired},
		{name: "wrong", token: "wrong-token", wantStatus: http.StatusProxyAuthRequired},
		{name: "correct", token: strings.Repeat("r", minStrictProxyAuthTokenBytes), wantStatus: http.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := proxyRequestStatus(t, proxyAddr, target.URL, tc.token); got != tc.wantStatus {
				t.Fatalf("proxy response status = %d, want %d", got, tc.wantStatus)
			}
		})
	}

	clientConn, peerConn := net.Pipe()
	unregister := rec.registerTunnel(&recorderTunnel{clientConn: clientConn})
	defer unregister()
	if err := rec.StopProxy(); err != nil {
		t.Fatalf("StopProxy(): %v", err)
	}
	if rec.ProxyRunning() {
		t.Fatal("ProxyRunning() = true after StopProxy")
	}
	_ = peerConn.SetWriteDeadline(time.Now().Add(time.Second))
	if _, err := peerConn.Write([]byte("probe")); err == nil {
		t.Fatal("registered hijacked tunnel remained open after StopProxy")
	}
	_ = peerConn.Close()

	conn, err := net.DialTimeout("tcp", proxyAddr, 100*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		t.Fatalf("recording proxy listener %s accepted a connection after StopProxy", proxyAddr)
	}
}

func TestStopProxyIsTerminalAndRejectsLateHijackedTunnels(t *testing.T) {
	rec := New("127.0.0.1:0")
	if err := rec.StopProxy(); err != nil {
		t.Fatalf("StopProxy() before start: %v", err)
	}
	if err := rec.StartProxy(); !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("StartProxy() after stop = %v, want http.ErrServerClosed", err)
	}

	clientConn, peerConn := net.Pipe()
	rec.registerTunnel(&recorderTunnel{clientConn: clientConn})()
	defer peerConn.Close()
	_ = peerConn.SetWriteDeadline(time.Now().Add(time.Second))
	if _, err := peerConn.Write([]byte("probe")); err == nil {
		t.Fatal("late hijacked tunnel remained open after proxy stop")
	}
}

func TestExplicitCAPathsPersistPrivateKeyWithOwnerOnlyMode(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "recorder", "desktop-ca.pem")
	keyPath := filepath.Join(dir, "recorder", "desktop-ca-key.pem")
	rec := NewWithOptions(Options{CACertPath: certPath, CAKeyPath: keyPath})

	req := httptest.NewRequest(http.MethodGet, "/ca.pem?format=pem", nil)
	resp := httptest.NewRecorder()
	rec.HandleCACert(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("HandleCACert status = %d, want %d: %s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if _, err := os.Stat(certPath); err != nil {
		t.Fatalf("stat explicit CA certificate: %v", err)
	}
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat explicit CA key: %v", err)
	}
	if got := info.Mode().Perm(); runtime.GOOS != "windows" && got != 0o600 {
		t.Fatalf("CA key mode = %o, want 600", got)
	}
	if runtime.GOOS != "windows" {
		dirInfo, err := os.Stat(filepath.Dir(keyPath))
		if err != nil {
			t.Fatalf("stat explicit CA directory: %v", err)
		}
		if got := dirInfo.Mode().Perm(); got != 0o700 {
			t.Fatalf("CA directory mode = %o, want 700", got)
		}
	}
}

func proxyRequestStatus(t *testing.T, proxyAddr, targetURL, token string) int {
	t.Helper()
	conn, err := net.DialTimeout("tcp", proxyAddr, time.Second)
	if err != nil {
		t.Fatalf("dial recording proxy: %v", err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set proxy connection deadline: %v", err)
	}
	auth := ""
	if token != "" {
		auth = fmt.Sprintf("Proxy-Authorization: Bearer %s\r\n", token)
	}
	request := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: example.test\r\n%sConnection: close\r\n\r\n", targetURL, auth)
	if _, err := io.WriteString(conn, request); err != nil {
		t.Fatalf("write proxy request: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read proxy response: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

package api

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testRecorderProxyToken = "recorder-only-capability-0123456789abcdef"

func TestDesktopRecorderConfigurationFailsClosedWithoutExplicitToken(t *testing.T) {
	t.Setenv("APEXLOAD_RECORDER_PROXY_TOKEN", "ambient-hosted-token")
	server := NewServer(ServerConfig{
		Addr:              "127.0.0.1:0",
		DesktopMode:       true,
		DesktopCapability: "desktop-api-capability",
	})

	if got := server.recorder.ProxyAuthToken(); got != "" {
		t.Fatalf("desktop recorder token = %q, want no environment fallback", got)
	}
	if got := server.recorder.ProxyAddr(); got != "127.0.0.1:0" {
		t.Fatalf("desktop recorder address = %q, want ephemeral loopback default", got)
	}
	if err := server.startRecorderProxy(); err == nil || !strings.Contains(err.Error(), "authentication token is required") {
		t.Fatalf("startRecorderProxy() = %v, want required-token error", err)
	}
	if server.recorder.ProxyRunning() {
		t.Fatal("desktop recorder proxy bound without an explicit token")
	}
	if err := server.Start(); err == nil || !strings.Contains(err.Error(), "authentication token is required") {
		t.Fatalf("Start() = %v, want protected recorder configuration failure", err)
	}
	if server.BoundAddrReady() || server.recorder.ProxyRunning() {
		t.Fatal("desktop server exposed a listener after protected recorder startup failed")
	}
}

func TestDesktopRecorderConfigurationUsesDistinctTokenAndExplicitCAPaths(t *testing.T) {
	appData := t.TempDir()
	certPath := filepath.Join(appData, "recorder", "ca-cert.pem")
	keyPath := filepath.Join(appData, "recorder", "ca-key.pem")
	server := NewServer(ServerConfig{
		Addr:               "127.0.0.1:0",
		DesktopMode:        true,
		DesktopCapability:  "desktop-api-capability",
		ProxyAuthToken:     testRecorderProxyToken,
		RecorderCACertPath: certPath,
		RecorderCAKeyPath:  keyPath,
	})

	if got := server.recorder.ProxyAuthToken(); got != testRecorderProxyToken {
		t.Fatalf("recorder proxy token = %q, want explicit recorder-only capability", got)
	}
	if server.recorder.ProxyAuthToken() == server.DesktopCapability() {
		t.Fatal("recorder proxy token reused the desktop API capability")
	}
	if got := server.recorder.CACertPath(); got != certPath {
		t.Fatalf("recorder CA certificate path = %q, want %q", got, certPath)
	}
	if got := server.recorder.CAKeyPath(); got != keyPath {
		t.Fatalf("recorder CA key path = %q, want %q", got, keyPath)
	}
	if err := server.startRecorderProxy(); err != nil {
		t.Fatalf("startRecorderProxy(): %v", err)
	}
	t.Cleanup(func() { _ = server.recorder.StopProxy() })
	assertBoundLoopbackAddress(t, server.recorder.ProxyAddr())
}

func TestHostedRecorderConfigurationRetainsEnvironmentTokenFallback(t *testing.T) {
	t.Setenv("APEXLOAD_RECORDER_PROXY_TOKEN", "hosted-token")
	server := NewServer(ServerConfig{ProxyAddr: "127.0.0.1:0"})
	if got := server.recorder.ProxyAuthToken(); got != "hosted-token" {
		t.Fatalf("hosted recorder token = %q, want environment token", got)
	}
}

func TestDesktopProtectedRecorderStartsInProductionWithoutHostedOverride(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "production")
	t.Setenv("APEXLOAD_RECORDER_PROXY_ALLOW_PRODUCTION", "")
	server := NewServer(ServerConfig{
		DesktopMode:       true,
		DesktopCapability: "desktop-api-capability",
		ProxyAuthToken:    testRecorderProxyToken,
	})
	if err := server.startRecorderProxy(); err != nil {
		t.Fatalf("startRecorderProxy() in production: %v", err)
	}
	t.Cleanup(func() { _ = server.recorder.StopProxy() })
	assertBoundLoopbackAddress(t, server.recorder.ProxyAddr())
}

func TestDesktopServerLifecycleStopsProtectedRecorderProxy(t *testing.T) {
	server := NewServer(ServerConfig{
		Addr:              "127.0.0.1:0",
		DesktopMode:       true,
		DesktopCapability: "desktop-api-capability",
		ProxyAuthToken:    testRecorderProxyToken,
	})
	startResult := make(chan error, 1)
	go func() { startResult <- server.Start() }()

	deadline := time.Now().Add(3 * time.Second)
	for !server.BoundAddrReady() {
		select {
		case err := <-startResult:
			t.Fatalf("Start() returned before binding: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("desktop API server did not bind")
		}
		time.Sleep(10 * time.Millisecond)
	}

	proxyAddr := server.recorder.ProxyAddr()
	assertBoundLoopbackAddress(t, proxyAddr)
	if !server.recorder.ProxyRunning() {
		t.Fatal("protected recorder proxy is not running after desktop startup")
	}
	if got := unauthenticatedProxyStatus(t, proxyAddr); got != http.StatusProxyAuthRequired {
		t.Fatalf("unauthenticated recorder proxy status = %d, want %d", got, http.StatusProxyAuthRequired)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown(): %v", err)
	}
	select {
	case err := <-startResult:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("Start() after shutdown = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Start() did not return after Shutdown()")
	}
	if server.recorder.ProxyRunning() {
		t.Fatal("protected recorder proxy remained running after server shutdown")
	}
	if conn, err := net.DialTimeout("tcp", proxyAddr, 100*time.Millisecond); err == nil {
		_ = conn.Close()
		t.Fatalf("recorder proxy listener %s accepted a connection after shutdown", proxyAddr)
	}
}

func assertBoundLoopbackAddress(t *testing.T, addr string) {
	t.Helper()
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("address %q is not host:port: %v", addr, err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() || port == "" || port == "0" {
		t.Fatalf("address = %q, want bound ephemeral loopback address", addr)
	}
}

func unauthenticatedProxyStatus(t *testing.T, proxyAddr string) int {
	t.Helper()
	conn, err := net.DialTimeout("tcp", proxyAddr, time.Second)
	if err != nil {
		t.Fatalf("dial recording proxy: %v", err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set proxy deadline: %v", err)
	}
	if _, err := conn.Write([]byte("GET http://example.com/ HTTP/1.1\r\nHost: example.com\r\nConnection: close\r\n\r\n")); err != nil {
		t.Fatalf("write proxy request: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read proxy response: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

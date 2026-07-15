package debugserver

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestStartPprofDisabledWhenBlank(t *testing.T) {
	srv, err := StartPprof("")
	if err != nil {
		t.Fatalf("blank addr should not error: %v", err)
	}
	if srv != nil {
		t.Fatal("blank addr should not start a server")
	}
}

func TestStartPprofFromEnvDisabledWhenUnset(t *testing.T) {
	srv, err := StartPprofFromEnv(func(string) string { return "" })
	if err != nil {
		t.Fatalf("unset env should not error: %v", err)
	}
	if srv != nil {
		t.Fatal("unset env should not start a server")
	}
	if srv, err := StartPprofFromEnv(nil); srv != nil || err != nil {
		t.Fatal("nil getenv should be a no-op")
	}
}

func TestStartPprofFromEnvRejectsNonLoopbackProductionBind(t *testing.T) {
	for _, addr := range []string{":6060", "0.0.0.0:6060", "[::]:6060", "debug.internal:6060", "malformed"} {
		t.Run(addr, func(t *testing.T) {
			values := map[string]string{PprofAddrEnv: addr, appEnvVar: " Prod "}
			srv, err := StartPprofFromEnv(func(key string) string { return values[key] })
			if err == nil {
				if srv != nil {
					_ = srv.Close()
				}
				t.Fatalf("production pprof accepted non-loopback bind %q", addr)
			}
			if srv != nil {
				t.Fatalf("rejected bind %q returned a server", addr)
			}
		})
	}
}

func TestStartPprofFromEnvAllowsExplicitProductionLoopback(t *testing.T) {
	values := map[string]string{PprofAddrEnv: "127.0.0.1:0", appEnvVar: "production"}
	srv, err := StartPprofFromEnv(func(key string) string { return values[key] })
	if err != nil {
		t.Fatalf("production pprof rejected loopback bind: %v", err)
	}
	if srv == nil {
		t.Fatal("production pprof did not start on loopback")
	}
	if err := srv.Close(); err != nil {
		t.Fatalf("close pprof server: %v", err)
	}
	for _, addr := range []string{"127.0.0.1:6060", "[::1]:6060", "localhost:6060", "LOCALHOST.:6060"} {
		if !isExplicitLoopbackBind(addr) {
			t.Errorf("isExplicitLoopbackBind(%q) = false, want true", addr)
		}
	}
}

func TestStartPprofFromEnvPreservesExplicitNonProductionRemoteBind(t *testing.T) {
	values := map[string]string{PprofAddrEnv: "0.0.0.0:0", appEnvVar: "development"}
	srv, err := StartPprofFromEnv(func(key string) string { return values[key] })
	if err != nil {
		t.Fatal(err)
	}
	if srv == nil {
		t.Fatal("non-production pprof did not start")
	}
	_ = srv.Close()
}

func TestServeExposesPprof(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := serve(ln)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})

	url := "http://" + ln.Addr().String() + "/debug/pprof/cmdline"
	var resp *http.Response
	// The server goroutine may not be accepting instantly; retry briefly.
	for i := 0; i < 50; i++ {
		resp, err = http.Get(url)
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from pprof endpoint, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if len(body) == 0 {
		t.Fatalf("expected non-empty pprof cmdline output")
	}
}

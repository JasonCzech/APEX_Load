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

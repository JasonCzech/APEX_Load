// Package debugserver exposes an optional net/http/pprof endpoint for
// diagnosing memory and goroutine growth on a running controller or generator.
//
// It is disabled unless APEXLOAD_PPROF_ADDR is set (e.g. "localhost:6060") and
// always serves on its own dedicated listener, so the unauthenticated pprof
// handlers are never mounted on the application's API mux. Because pprof exposes
// runtime internals without authentication, bind it to localhost or a
// firewalled port only.
//
// Once enabled, capture a heap profile diff over time to find the leaking
// allocation site:
//
//	go tool pprof -proto http://<addr>/debug/pprof/heap > h1.pb.gz   # t0
//	go tool pprof -proto http://<addr>/debug/pprof/heap > h2.pb.gz   # t0 + 15m
//	go tool pprof -base h1.pb.gz h2.pb.gz                            # then: top / list
//
// and /debug/pprof/goroutine?debug=1 to confirm or rule out a goroutine leak.
package debugserver

import (
	"log/slog"
	"net"
	"net/http"
	_ "net/http/pprof" // registers /debug/pprof/* handlers on http.DefaultServeMux
	"strings"
	"time"
)

// PprofAddrEnv is the environment variable that enables the pprof listener.
const PprofAddrEnv = "APEXLOAD_PPROF_ADDR"

// StartPprof starts an HTTP server exposing net/http/pprof on addr in a
// background goroutine. A blank addr disables it and returns (nil, nil). A
// returned error means the listener could not be bound (e.g. port in use); the
// caller should log it but need not treat it as fatal. The returned *http.Server
// can be closed to stop the listener; callers running for the process lifetime
// may ignore it.
func StartPprof(addr string) (*http.Server, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil, nil
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	return serve(ln), nil
}

// StartPprofFromEnv starts the pprof listener using APEXLOAD_PPROF_ADDR read via
// getenv (typically os.Getenv). It is a no-op when the variable is unset.
func StartPprofFromEnv(getenv func(string) string) (*http.Server, error) {
	if getenv == nil {
		return nil, nil
	}
	return StartPprof(getenv(PprofAddrEnv))
}

// serve runs an http.Server bound to ln serving the default mux (where pprof is
// registered). Split out so tests can drive a real, already-bound listener.
func serve(ln net.Listener) *http.Server {
	srv := &http.Server{
		Handler:           http.DefaultServeMux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		slog.Warn("pprof debug server enabled (unauthenticated); bind to localhost or a firewalled port", "addr", ln.Addr().String())
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			slog.Error("pprof debug server failed", "addr", ln.Addr().String(), "err", err)
		}
	}()
	return srv
}

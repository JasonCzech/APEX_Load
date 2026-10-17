package recordbridge

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/scrub"
)

func bridgeHTTPMiddleware(next http.Handler) http.Handler {
	const csp = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; img-src 'self' data: blob:; font-src 'self' data: https://fonts.gstatic.com; connect-src 'self' ws: wss: http://127.0.0.1:* http://localhost:* ws://127.0.0.1:* ws://localhost:*; worker-src 'self' blob:; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", csp)
		if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		if r.Body != nil && (r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch || r.Method == http.MethodDelete) {
			r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
		}
		next.ServeHTTP(w, r)
	})
}

type bridgeResponseWriter struct {
	http.ResponseWriter
	status          int
	wrote, hijacked bool
}

func (w *bridgeResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *bridgeResponseWriter) WriteHeader(status int) {
	if !w.wrote {
		w.status = status
		w.wrote = true
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *bridgeResponseWriter) Write(data []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}
func (w *bridgeResponseWriter) Flush() {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}
func (w *bridgeResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err == nil {
		w.hijacked = true
	}
	return conn, rw, err
}
func bridgeRequestID(raw string) string {
	valid := len(raw) > 0 && len(raw) <= 128
	for _, c := range raw {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			valid = false
			break
		}
	}
	if valid {
		return raw
	}
	var id [8]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(id[:])
}
func bridgeFailureLoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ref := bridgeRequestID(strings.TrimSpace(r.Header.Get("X-Request-ID")))
		w.Header().Set("X-Request-ID", ref)
		response := &bridgeResponseWriter{ResponseWriter: w, status: http.StatusOK}
		defer func() {
			if recovered := recover(); recovered != nil {
				if recovered == http.ErrAbortHandler {
					panic(recovered)
				}
				slog.Error("http handler panic", "request_id", ref, "method", r.Method, "path", r.URL.Path, "panic", scrub.Value(fmt.Sprint(recovered), scrub.Policy{}))
				if !response.wrote && !response.hijacked {
					for _, key := range []string{"Content-Length", "Content-Encoding", "Content-Disposition", "Content-Range", "ETag", "Last-Modified", "Location"} {
						w.Header().Del(key)
					}
					w.Header().Set("Cache-Control", "no-store")
					WriteJSON(response, http.StatusInternalServerError, map[string]string{"error": "internal server error", "ref": ref})
				} else if !response.hijacked {
					panic(http.ErrAbortHandler)
				}
			}
			if response.status >= 400 {
				level := slog.LevelWarn
				if response.status >= 500 {
					level = slog.LevelError
				}
				slog.Log(r.Context(), level, "http request", "method", r.Method, "path", r.URL.Path, "status", response.status, "duration_ms", time.Since(start).Milliseconds(), "request_id", ref)
			}
		}()
		next.ServeHTTP(response, r)
	})
}

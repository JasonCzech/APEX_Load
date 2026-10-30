package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
	"github.com/jasonczech/apexload/pkg/scrub"
	"go.opentelemetry.io/otel/trace"
)

// requestIDHeader carries the per-request correlation ID. An ingress-supplied
// value (ingress-nginx forwards its own $req_id here) is reused so controller
// lines join the ingress access log; otherwise one is generated. The same ID is
// echoed on the response and used as the "ref" of 5xx error bodies, so the one
// value a user can report finds every log line for that request.
const requestIDHeader = "X-Request-ID"

const (
	maxRequestIDBytes = 128
	// Error bodies are short JSON envelopes; the cap only bounds what the access
	// logger buffers when a handler streams an unusually large error payload.
	maxLoggedErrorBodyBytes = 4 << 10
	maxLoggedErrorBytes     = 1 << 10
)

type requestLogContextKey struct{}

// requestLogEntry collects request facts that only inner middleware knows.
// The access logger owns it; the auth middleware records the resolved actor
// through the request context because it runs inside the logger.
type requestLogEntry struct {
	mu    sync.Mutex
	actor string
}

func (e *requestLogEntry) setActor(actor string) {
	e.mu.Lock()
	e.actor = actor
	e.mu.Unlock()
}

func (e *requestLogEntry) actorLabel() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.actor
}

// annotateRequestLogActor records the authenticated principal on the access
// line of the request whose context carries ctx. It is a no-op outside the
// access logger (handler-only tests, background work).
func annotateRequestLogActor(ctx context.Context, actor string) {
	if ctx == nil || actor == "" {
		return
	}
	if entry, ok := ctx.Value(requestLogContextKey{}).(*requestLogEntry); ok && entry != nil {
		entry.setActor(actor)
	}
}

// accessLogResponseWriter extends the status-capturing writer with the
// facts the access line needs: whether the connection was hijacked (so a
// recovered panic must not write) and a bounded copy of any error body, from
// which the client-visible error, code and ref are logged.
type accessLogResponseWriter struct {
	responseWriter
	hijacked  bool
	errorBody []byte
}

func (rw *accessLogResponseWriter) Write(b []byte) (int, error) {
	n, err := rw.responseWriter.Write(b)
	if rw.status >= http.StatusBadRequest && n > 0 {
		if room := maxLoggedErrorBodyBytes - len(rw.errorBody); room > 0 {
			rw.errorBody = append(rw.errorBody, b[:min(n, room)]...)
		}
	}
	return n, err
}

func (rw *accessLogResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, buf, err := rw.responseWriter.Hijack()
	if err == nil {
		rw.hijacked = true
	}
	return conn, buf, err
}

// loggingMiddleware writes one structured access line per API request, at
// INFO for success, WARN for 4xx and ERROR for 5xx, carrying the reason the
// client was given. It also converts a handler panic into a logged 500 instead
// of a dropped connection.
func loggingMiddleware(next http.Handler) http.Handler {
	return requestLoggingMiddleware(nil)(next)
}

// failureLoggingMiddleware is loggingMiddleware writing only failing
// requests (4xx/5xx) and panics. The Recording Agent runs in a terminal the
// user watches; a line for every status poll there buries its startup text
// and real errors.
func failureLoggingMiddleware(next http.Handler) http.Handler {
	return requestLoggingMiddleware(slog.New(minimumLevelHandler{minimum: slog.LevelWarn}))(next)
}

// minimumLevelHandler drops records below minimum and passes the rest to
// handler, or, while handler is nil, to slog.Default() as it is when each
// record is logged, so a later SetDefault still applies.
type minimumLevelHandler struct {
	handler slog.Handler
	minimum slog.Level
}

func (h minimumLevelHandler) next() slog.Handler {
	if h.handler != nil {
		return h.handler
	}
	return slog.Default().Handler()
}

func (h minimumLevelHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= h.minimum && h.next().Enabled(ctx, level)
}

func (h minimumLevelHandler) Handle(ctx context.Context, record slog.Record) error {
	return h.next().Handle(ctx, record)
}

func (h minimumLevelHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return minimumLevelHandler{handler: h.next().WithAttrs(attrs), minimum: h.minimum}
}

func (h minimumLevelHandler) WithGroup(name string) slog.Handler {
	return minimumLevelHandler{handler: h.next().WithGroup(name), minimum: h.minimum}
}

// requestLoggingMiddleware is loggingMiddleware with an injectable logger;
// nil resolves slog.Default() per request so a later SetDefault applies.
func requestLoggingMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			accessLog := logger
			if accessLog == nil {
				accessLog = slog.Default()
			}
			start := time.Now()
			requestID := requestIDForRequest(r)
			w.Header().Set(requestIDHeader, requestID)
			entry := &requestLogEntry{}
			r = r.WithContext(context.WithValue(r.Context(), requestLogContextKey{}, entry))
			rw := &accessLogResponseWriter{responseWriter: responseWriter{ResponseWriter: w, status: http.StatusOK}}

			completed := false
			defer func() {
				if completed {
					logHTTPRequest(accessLog, r, rw, entry, requestID, start, false)
					return
				}
				recovered := recover()
				if recovered == nil {
					// runtime.Goexit unwound the handler; there is no panic to report.
					logHTTPRequest(accessLog, r, rw, entry, requestID, start, false)
					return
				}
				if recovered == http.ErrAbortHandler {
					// A deliberate abort: net/http closes the connection without
					// logging a stack, and so do we.
					logHTTPRequest(accessLog, r, rw, entry, requestID, start, false)
					panic(recovered)
				}
				accessLog.LogAttrs(r.Context(), slog.LevelError, "http handler panic",
					slog.String("request_id", requestID),
					slog.String("method", r.Method),
					slog.String("route", requestRoutePattern(r)),
					slog.String("path", r.URL.Path),
					slog.String("panic", scrub.Value(boundedtext.TruncateUTF8(fmt.Sprint(recovered), maxLoggedErrorBytes), scrub.Policy{})),
					slog.String("stack", string(debug.Stack())),
				)
				if !rw.wrote && !rw.hijacked {
					writePanicResponse(rw, requestID)
					logHTTPRequest(accessLog, r, rw, entry, requestID, start, true)
					return
				}
				logHTTPRequest(accessLog, r, rw, entry, requestID, start, true)
				if !rw.hijacked {
					// The status line is already on the wire; abort the connection so
					// the client sees a failed response rather than a truncated one.
					panic(http.ErrAbortHandler)
				}
			}()
			next.ServeHTTP(rw, r)
			completed = true
		})
	}
}

// writePanicResponse replaces whatever the handler staged before panicking
// with the same generic 5xx envelope writeError produces. Representation
// headers describing the abandoned success body must not ride on the error.
func writePanicResponse(w http.ResponseWriter, requestID string) {
	h := w.Header()
	for _, name := range []string{"Content-Length", "Content-Encoding", "Content-Disposition", "Content-Range", "ETag", "Last-Modified", "Location"} {
		h.Del(name)
	}
	h.Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusInternalServerError, map[string]string{
		"error": "internal server error",
		"ref":   requestID,
	})
}

// statusClientClosedRequest is logged for a request whose client went away
// (net/http cancels the request context) before any response was committed
// (nginx's 499): net/http has nothing to send, and the recorder's default 200
// would read as a success.
const statusClientClosedRequest = 499

func logHTTPRequest(logger *slog.Logger, r *http.Request, rw *accessLogResponseWriter, entry *requestLogEntry, requestID string, start time.Time, panicked bool) {
	status := rw.status
	if !rw.wrote && !rw.hijacked && errors.Is(r.Context().Err(), context.Canceled) {
		status = statusClientClosedRequest
	}
	attrs := make([]slog.Attr, 0, 14)
	attrs = append(attrs,
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
	)
	if route := requestRoutePattern(r); route != "" {
		attrs = append(attrs, slog.String("route", route))
	}
	attrs = append(attrs,
		slog.Int("status", status),
		slog.Int64("duration_ms", time.Since(start).Milliseconds()),
		slog.String("remote", r.RemoteAddr),
		slog.String("request_id", requestID),
	)
	if actor := entry.actorLabel(); actor != "" {
		attrs = append(attrs, slog.String("actor", actor))
	}
	if span := trace.SpanContextFromContext(r.Context()); span.HasTraceID() {
		attrs = append(attrs, slog.String("trace_id", span.TraceID().String()))
	}
	if status >= http.StatusBadRequest {
		message, code, ref := summarizeErrorResponse(rw.Header().Get("Content-Type"), rw.errorBody)
		if message != "" {
			attrs = append(attrs, slog.String("error", message))
		}
		if code != "" {
			attrs = append(attrs, slog.String("code", code))
		}
		if ref != "" {
			attrs = append(attrs, slog.String("ref", ref))
		}
	}
	level := accessLogLevel(status)
	if panicked {
		// A panic after the status line was sent can leave a 2xx status on
		// a broken response; the line must still surface as a failure.
		attrs = append(attrs, slog.Bool("panic", true))
		level = slog.LevelError
	}
	logger.LogAttrs(r.Context(), level, "http request", attrs...)
}

// httpServerErrorLog routes net/http's own diagnostics (panics outside the
// route stack, TLS handshake and accept errors, superfluous WriteHeader calls)
// through the structured logger at WARN. Left nil, they reach slog's bridge
// for the standard log package at INFO, where level-filtered pipelines drop
// them.
func httpServerErrorLog() *log.Logger {
	return slog.NewLogLogger(slog.Default().Handler(), slog.LevelWarn)
}

func accessLogLevel(status int) slog.Level {
	switch {
	case status >= http.StatusInternalServerError:
		return slog.LevelError
	case status >= http.StatusBadRequest:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}

// requestRoutePattern returns the matched ServeMux pattern without its method
// prefix, e.g. "/api/v1/scripts/{id}", so lines group by route rather than by
// concrete IDs.
func requestRoutePattern(r *http.Request) string {
	pattern := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(r.Pattern), r.Method+" "))
	return boundedtext.TruncateUTF8(pattern, 256)
}

// summarizeErrorResponse extracts the client-visible error from a captured
// error body. JSON envelopes ({"error", "code", "ref"}) are the API norm;
// plain-text bodies are logged as text; HTML and unparseable JSON fall back
// to a short prefix. Everything is credential-scrubbed and length-bounded.
func summarizeErrorResponse(contentType string, body []byte) (message, code, ref string) {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return "", "", ""
	}
	if strings.Contains(strings.ToLower(contentType), "json") || body[0] == '{' {
		var envelope struct {
			Error   json.RawMessage `json:"error"`
			Message string          `json:"message"`
			Code    any             `json:"code"`
			Ref     string          `json:"ref"`
		}
		if err := json.Unmarshal(body, &envelope); err == nil {
			message = envelope.Message
			if len(envelope.Error) > 0 {
				var text string
				if json.Unmarshal(envelope.Error, &text) == nil {
					message = text
				} else {
					message = string(envelope.Error)
				}
			}
			if envelope.Code != nil {
				code = boundedtext.TruncateUTF8(fmt.Sprint(envelope.Code), 128)
			}
			if validRequestID(envelope.Ref) {
				ref = envelope.Ref
			}
			return boundedErrorText(message), code, ref
		}
	}
	text := string(body)
	if lower := strings.ToLower(text[:min(len(text), 64)]); strings.HasPrefix(lower, "<!doctype html") || strings.HasPrefix(lower, "<html") {
		return "", "", ""
	}
	return boundedErrorText(text), "", ""
}

func boundedErrorText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return scrub.Value(boundedtext.TruncateUTF8(text, maxLoggedErrorBytes), scrub.Policy{})
}

func requestIDForRequest(r *http.Request) string {
	if id := strings.TrimSpace(r.Header.Get(requestIDHeader)); validRequestID(id) {
		return id
	}
	return newErrorReference()
}

// validRequestID admits the ID shapes proxies generate (hex, UUIDs, ULIDs,
// dotted/colon-separated trace forms) and nothing that could split or spoof
// a log line.
func validRequestID(id string) bool {
	if id == "" || len(id) > maxRequestIDBytes {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.', c == ':':
		default:
			return false
		}
	}
	return true
}

// errorReferenceFor returns the reference a 5xx body should carry: the
// request ID the access logger stamped on the response, so the reported ref
// matches the request's log lines; a fresh random one outside the logger.
func errorReferenceFor(w http.ResponseWriter) string {
	if w != nil {
		if id := w.Header().Get(requestIDHeader); validRequestID(id) {
			return id
		}
	}
	return newErrorReference()
}

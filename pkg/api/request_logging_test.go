package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/testlog"
)

// lockedBuffer lets a test capture the process-wide default logger while
// parallel tests in the package may also be logging.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func newCapturedRequestLogger() (*slog.Logger, *lockedBuffer) {
	logs := &lockedBuffer{}
	return slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})), logs
}

func logRecords(t *testing.T, logs *lockedBuffer) []map[string]any {
	t.Helper()
	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		record := map[string]any{}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("log line is not JSON: %q: %v", line, err)
		}
		records = append(records, record)
	}
	return records
}

func findLogRecord(t *testing.T, logs *lockedBuffer, msg, requestID string) map[string]any {
	t.Helper()
	for _, record := range logRecords(t, logs) {
		if record["msg"] == msg && (requestID == "" || record["request_id"] == requestID) {
			return record
		}
	}
	t.Fatalf("no %q log line for request %q in:\n%s", msg, requestID, logs.String())
	return nil
}

func serveLogged(t *testing.T, logger *slog.Logger, handler http.Handler, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("/", requestLoggingMiddleware(logger)(handler))
	resp := httptest.NewRecorder()
	mux.ServeHTTP(resp, req)
	return resp
}

func TestRequestLoggingRecordsClientErrorReasonAtWarn(t *testing.T) {
	logger, logs := newCapturedRequestLogger()
	resp := serveLogged(t, logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusConflict, "script name already exists in this project")
	}), httptest.NewRequest(http.MethodPost, "/api/v1/scripts", nil))

	requestID := resp.Header().Get(requestIDHeader)
	if !validRequestID(requestID) {
		t.Fatalf("response request ID = %q", requestID)
	}
	line := findLogRecord(t, logs, "http request", requestID)
	if line["level"] != "WARN" || line["status"] != float64(http.StatusConflict) {
		t.Fatalf("4xx access line level/status = %v/%v", line["level"], line["status"])
	}
	if line["error"] != "script name already exists in this project" {
		t.Fatalf("4xx access line error = %v", line["error"])
	}
	if line["method"] != http.MethodPost || line["path"] != "/api/v1/scripts" {
		t.Fatalf("4xx access line request = %v %v", line["method"], line["path"])
	}
}

func TestRequestLoggingServerErrorRefMatchesRequestID(t *testing.T) {
	detailLogs := captureDefaultLogs(t) // writeError logs the detail through the default logger
	logger, logs := newCapturedRequestLogger()
	resp := serveLogged(t, logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusInternalServerError, "database is locked at /var/lib/apexload/apexload.db")
	}), httptest.NewRequest(http.MethodGet, "/api/v1/tests", nil))

	var body map[string]string
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	requestID := resp.Header().Get(requestIDHeader)
	if body["error"] != "internal server error" || body["ref"] == "" || body["ref"] != requestID {
		t.Fatalf("5xx body = %v, request ID %q", body, requestID)
	}
	line := findLogRecord(t, logs, "http request", requestID)
	if line["level"] != "ERROR" || line["ref"] != requestID || line["error"] != "internal server error" {
		t.Fatalf("5xx access line = %v", line)
	}
	if strings.Contains(logs.String(), "database is locked") {
		t.Fatal("access line repeated the internal detail; writeError logs it on its own line")
	}
	var detailLine map[string]any
	for _, record := range logRecords(t, detailLogs) {
		if record["msg"] == "internal server error" && record["ref"] == requestID {
			detailLine = record
		}
	}
	if detailLine == nil || detailLine["level"] != "ERROR" || !strings.Contains(detailLine["detail"].(string), "database is locked") {
		t.Fatalf("no internal detail line under ref %q in:\n%s", requestID, detailLogs.String())
	}
}

func TestRequestLoggingSuccessIsInfoWithRouteAndNoError(t *testing.T) {
	logger, logs := newCapturedRequestLogger()
	mux := http.NewServeMux()
	mux.Handle("GET /api/v1/things/{id}", requestLoggingMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"error": "a field that happens to be called error"})
	})))
	resp := httptest.NewRecorder()
	mux.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/api/v1/things/abc", nil))

	line := findLogRecord(t, logs, "http request", resp.Header().Get(requestIDHeader))
	if line["level"] != "INFO" || line["route"] != "/api/v1/things/{id}" || line["path"] != "/api/v1/things/abc" {
		t.Fatalf("2xx access line = %v", line)
	}
	if _, ok := line["error"]; ok {
		t.Fatalf("2xx access line carried an error: %v", line)
	}
}

func TestRequestLoggingRequestIDPropagation(t *testing.T) {
	for _, test := range []struct {
		name     string
		incoming string
		reuse    bool
	}{
		{name: "ingress hex", incoming: "7c6f0c0a6b1d4e7f9a2b3c4d5e6f7081", reuse: true},
		{name: "uuid", incoming: "0f8fad5b-d9cb-469f-a165-70867728950e", reuse: true},
		{name: "spaces", incoming: "not a request id"},
		{name: "log splitting", incoming: "abc\"},{\"level\":\"ERROR"},
		{name: "oversized", incoming: strings.Repeat("a", maxRequestIDBytes+1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			logger, logs := newCapturedRequestLogger()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/tests", nil)
			req.Header.Set(requestIDHeader, test.incoming)
			resp := serveLogged(t, logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}), req)
			got := resp.Header().Get(requestIDHeader)
			if test.reuse && got != test.incoming {
				t.Fatalf("valid incoming request ID %q replaced by %q", test.incoming, got)
			}
			if !test.reuse && (got == test.incoming || !validRequestID(got)) {
				t.Fatalf("invalid incoming request ID %q produced %q", test.incoming, got)
			}
			findLogRecord(t, logs, "http request", got)
		})
	}
}

func TestRequestLoggingRecoversPanicBeforeResponse(t *testing.T) {
	logger, logs := newCapturedRequestLogger()
	resp := serveLogged(t, logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"stale"`)
		w.Header().Set("Content-Disposition", "attachment; filename=report.pdf")
		var m map[string]int
		m["boom"]++ //nolint:govet,staticcheck // the nil-map write is the runtime panic under test
	}), httptest.NewRequest(http.MethodPost, "/api/v1/scenarios/s1/launch", nil))

	requestID := resp.Header().Get(requestIDHeader)
	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("recovered panic status = %d", resp.Code)
	}
	if resp.Header().Get("ETag") != "" || resp.Header().Get("Content-Disposition") != "" {
		t.Fatalf("panic response kept success representation headers: %v", resp.Header())
	}
	var body map[string]string
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil || body["error"] != "internal server error" || body["ref"] != requestID {
		t.Fatalf("recovered panic body = %q (%v)", resp.Body.String(), err)
	}
	panicLine := findLogRecord(t, logs, "http handler panic", requestID)
	if panicLine["level"] != "ERROR" || !strings.Contains(panicLine["panic"].(string), "nil map") ||
		!strings.Contains(panicLine["stack"].(string), "TestRequestLoggingRecoversPanicBeforeResponse") {
		t.Fatalf("panic line = %v", panicLine)
	}
	access := findLogRecord(t, logs, "http request", requestID)
	if access["level"] != "ERROR" || access["status"] != float64(http.StatusInternalServerError) || access["panic"] != true || access["ref"] != requestID {
		t.Fatalf("panic access line = %v", access)
	}
}

func TestRequestLoggingAbortsConnectionWhenPanicFollowsResponse(t *testing.T) {
	logger, logs := newCapturedRequestLogger()
	handler := requestLoggingMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"partial":`))
		panic("stream encoder failed")
	}))
	resp := httptest.NewRecorder()
	func() {
		defer func() {
			if recovered := recover(); recovered != http.ErrAbortHandler {
				t.Fatalf("panic after response re-panicked with %v; want http.ErrAbortHandler", recovered)
			}
		}()
		handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/api/v1/tests/t1/stream", nil))
	}()
	requestID := resp.Header().Get(requestIDHeader)
	findLogRecord(t, logs, "http handler panic", requestID)
	access := findLogRecord(t, logs, "http request", requestID)
	if access["level"] != "ERROR" || access["status"] != float64(http.StatusOK) || access["panic"] != true {
		t.Fatalf("post-response panic access line = %v", access)
	}
}

func TestRequestLoggingPassesThroughDeliberateAbort(t *testing.T) {
	logger, logs := newCapturedRequestLogger()
	handler := requestLoggingMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(http.ErrAbortHandler)
	}))
	resp := httptest.NewRecorder()
	func() {
		defer func() {
			if recovered := recover(); recovered != http.ErrAbortHandler {
				t.Fatalf("deliberate abort re-panicked with %v", recovered)
			}
		}()
		handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/api/v1/proxy", nil))
	}()
	if strings.Contains(logs.String(), "http handler panic") {
		t.Fatal("deliberate abort was reported as a handler panic")
	}
	findLogRecord(t, logs, "http request", resp.Header().Get(requestIDHeader))
}

func TestRequestLoggingRecordsAuthenticatedActor(t *testing.T) {
	for _, test := range []struct {
		name       string
		resolution authResolution
		want       string
	}{
		{name: "user", resolution: authResolution{User: &AuthenticatedUser{ID: "u-1", Subject: " alice@example.com "}}, want: "user:alice@example.com"},
		{name: "service account", resolution: authResolution{User: &AuthenticatedUser{Subject: "svc"}, ServiceAccount: &AuthenticatedServiceAccount{ID: "sa-7"}}, want: "service-account:sa-7"},
		{name: "subjectless user", resolution: authResolution{User: &AuthenticatedUser{ID: "u-2"}}, want: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			logger, logs := newCapturedRequestLogger()
			inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			})
			resp := serveLogged(t, logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				inner.ServeHTTP(w, withAuthenticatedContext(r, test.resolution))
			}), httptest.NewRequest(http.MethodGet, "/api/v1/tests", nil))
			line := findLogRecord(t, logs, "http request", resp.Header().Get(requestIDHeader))
			got, _ := line["actor"].(string)
			if got != test.want {
				t.Fatalf("actor = %q, want %q", got, test.want)
			}
		})
	}
}

func TestRequestLoggingBoundsAndScrubsLoggedErrors(t *testing.T) {
	const jwt = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	logger, logs := newCapturedRequestLogger()
	resp := serveLogged(t, logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Bypasses writeError's scrubbing on purpose, as a few handlers do.
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("upstream rejected token " + jwt + " " + strings.Repeat("x", 8<<10)))
	}), httptest.NewRequest(http.MethodGet, "/api/v1/monitoring/probe", nil))

	line := findLogRecord(t, logs, "http request", resp.Header().Get(requestIDHeader))
	logged, _ := line["error"].(string)
	if !strings.HasPrefix(logged, "upstream rejected token") || len(logged) > maxLoggedErrorBytes {
		t.Fatalf("plain-text error not bounded: %d bytes", len(logged))
	}
	if strings.Contains(logs.String(), jwt) {
		t.Fatal("access line disclosed a bearer token from the error body")
	}
}

func TestSummarizeErrorResponseShapes(t *testing.T) {
	for _, test := range []struct {
		name, contentType, body, message, code, ref string
	}{
		{name: "envelope", contentType: "application/json", body: `{"error":"bad input","code":"invalid_name","ref":"abc123"}`, message: "bad input", code: "invalid_name", ref: "abc123"},
		{name: "structured error", contentType: "application/json", body: `{"error":{"field":"name"}}`, message: `{"field":"name"}`},
		{name: "message field", contentType: "application/json", body: `{"message":"quota exceeded"}`, message: "quota exceeded"},
		{name: "spoofed ref", contentType: "application/json", body: `{"error":"x","ref":"a b"}`, message: "x"},
		{name: "truncated json", contentType: "application/json", body: `{"error":"cut`, message: `{"error":"cut`},
		{name: "html", contentType: "text/html", body: "<!DOCTYPE html><html><body>Bad Gateway</body></html>"},
		{name: "empty", contentType: "application/json", body: "  "},
	} {
		t.Run(test.name, func(t *testing.T) {
			message, code, ref := summarizeErrorResponse(test.contentType, []byte(test.body))
			if message != test.message || code != test.code || ref != test.ref {
				t.Fatalf("summarizeErrorResponse = (%q, %q, %q), want (%q, %q, %q)", message, code, ref, test.message, test.code, test.ref)
			}
		})
	}
}

func TestErrorReferenceForUsesStampedRequestID(t *testing.T) {
	stamped := httptest.NewRecorder()
	stamped.Header().Set(requestIDHeader, "req-123")
	if got := errorReferenceFor(stamped); got != "req-123" {
		t.Fatalf("errorReferenceFor(stamped) = %q", got)
	}
	unstamped := httptest.NewRecorder()
	if got := errorReferenceFor(unstamped); !validRequestID(got) || got == "req-123" {
		t.Fatalf("errorReferenceFor(unstamped) = %q", got)
	}
}

// TestAPIRoutesStampRequestIDAndLogErrorReason drives a real catalog route so
// the access logger's position in the stack (outside auth, inside CORS) is
// covered, not just the middleware in isolation.
func TestAPIRoutesStampRequestIDAndLogErrorReason(t *testing.T) {
	logs := &lockedBuffer{}
	testlog.SetDefault(t, slog.New(slog.NewJSONHandler(logs, nil)))

	server, _ := newTimeslotServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/no-such-script", nil)
	resp := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(resp, req)

	if resp.Code < http.StatusBadRequest {
		t.Fatalf("missing script returned %d", resp.Code)
	}
	requestID := resp.Header().Get(requestIDHeader)
	if !validRequestID(requestID) {
		t.Fatalf("API route response request ID = %q", requestID)
	}
	line := findLogRecord(t, logs, "http request", requestID)
	var body map[string]any
	_ = json.Unmarshal(resp.Body.Bytes(), &body)
	if line["route"] != "/api/v1/scripts/{id}" || line["status"] != float64(resp.Code) || line["error"] != body["error"] {
		t.Fatalf("route access line = %v, response %d %v", line, resp.Code, body)
	}
}

func TestLogServerErrorLogsScrubbedCauseUnderStampedRef(t *testing.T) {
	logs := &lockedBuffer{}
	testlog.SetDefault(t, slog.New(slog.NewJSONHandler(logs, nil)))

	const jwt = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	w := httptest.NewRecorder()
	w.Header().Set(requestIDHeader, "req-log-server-error")
	ref := logServerError(w, http.StatusServiceUnavailable, errors.New("provider rejected bearer "+jwt), "code", "provider_error")
	if ref != "req-log-server-error" {
		t.Fatalf("ref = %q, want the stamped request ID", ref)
	}
	var line map[string]any
	for _, record := range logRecords(t, logs) {
		if record["ref"] == ref {
			line = record
		}
	}
	if line == nil || line["level"] != "ERROR" || line["msg"] != "internal server error" || line["code"] != "provider_error" || line["status"] != float64(http.StatusServiceUnavailable) {
		t.Fatalf("server error line = %v", line)
	}
	if detail, _ := line["detail"].(string); !strings.HasPrefix(detail, "provider rejected bearer") || strings.Contains(logs.String(), jwt) {
		t.Fatalf("detail not logged or not scrubbed: %v", line["detail"])
	}
}

func TestRecoveredPanicReleasesIdempotencyReservation(t *testing.T) {
	for _, test := range []struct {
		name         string
		handler      http.HandlerFunc
		wantReserved bool
	}{
		{name: "panic before response", handler: func(w http.ResponseWriter, r *http.Request) { panic("store write failed") }},
		{name: "panic after success response", handler: func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
			panic("post-commit encoder failed")
		}, wantReserved: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newMemoryCacheBackend(cacheRuntimeConfig{KeyPrefix: "test", FanoutChannel: "events"})
			ctx := context.Background()
			if reserved, err := store.ReserveIdempotencyKey(ctx, "scope", "owner", time.Minute); err != nil || !reserved {
				t.Fatalf("reserve = %v, %v", reserved, err)
			}
			logger, _ := newCapturedRequestLogger()
			handler := requestLoggingMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				serveReservedMutation(w, r, store, &idempotencyReservation{key: "scope", ownerToken: "owner"}, test.handler)
			}))
			func() {
				defer func() { _ = recover() }() // the post-response case aborts the connection
				handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/v1/scripts", nil))
			}()
			reservedAgain, err := store.ReserveIdempotencyKey(ctx, "scope", "retry", time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if stillHeld := !reservedAgain; stillHeld != test.wantReserved {
				t.Fatalf("reservation held after panic = %v, want %v", stillHeld, test.wantReserved)
			}
		})
	}
}

// The Recording Agent's access log keeps failing requests only; a successful
// status poll every couple of seconds would bury its terminal.
func TestFailureLoggingMiddlewareSkipsSuccessfulRequests(t *testing.T) {
	handler := failureLoggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/missing") {
			writeError(w, http.StatusNotFound, "no such agent session")
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	// Routes are built before the agent's logger is set; it still applies.
	logs := &lockedBuffer{}
	testlog.SetDefault(t, slog.New(slog.NewJSONHandler(logs, nil)))
	for _, path := range []string{"/api/v1/agent-quiet-test/status", "/api/v1/agent-quiet-test/missing"} {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}
	if strings.Contains(logs.String(), "/api/v1/agent-quiet-test/status") {
		t.Fatalf("a successful request was logged: %s", logs.String())
	}
	if !strings.Contains(logs.String(), "/api/v1/agent-quiet-test/missing") || !strings.Contains(logs.String(), "no such agent session") {
		t.Fatalf("a failing request was not logged with its reason: %s", logs.String())
	}
}

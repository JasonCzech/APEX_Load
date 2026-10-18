package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/internal/testlog"
)

func postClientErrorReport(t *testing.T, server *Server, report any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/system/client-errors", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(resp, req)
	return resp
}

func captureDefaultLogs(t *testing.T) *lockedBuffer {
	t.Helper()
	logs := &lockedBuffer{}
	testlog.SetDefault(t, slog.New(slog.NewJSONHandler(logs, nil)))
	return logs
}

func TestClientErrorReportIsLoggedBoundedAndScrubbed(t *testing.T) {
	logs := captureDefaultLogs(t)
	server, _ := newTimeslotServer(t)
	const jwt = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	resp := postClientErrorReport(t, server, ClientErrorReport{
		Kind:           "render",
		Message:        "Cannot read properties of undefined (reading 'actions') token=" + jwt,
		Name:           "TypeError",
		Stack:          strings.Repeat("at ScriptEditor (assets/index-abc.js:1:2)\n", 400),
		ComponentStack: "\n    at ScriptEditor\n    at LazyLoadBoundary",
		Page:           "https://apexload.example.com/scripts/3f2a9c1e-77aa-4c1e-9d0e-5b1f0c2d3e4f?desktop_token=secret-capability#/editor?tab=json",
		RequestMethod:  "get",
		RequestPath:    "/api/v1/scripts/3f2a9c1e-77aa-4c1e-9d0e-5b1f0c2d3e4f?desktop_token=secret-capability",
		RequestStatus:  504,
		RequestID:      "abc\"} forged",
		AppVersion:     "fdfe43cbc",
	})
	if resp.Code != http.StatusNoContent {
		t.Fatalf("report status = %d body=%s", resp.Code, resp.Body.String())
	}
	line := findLogRecord(t, logs, "dashboard client error", "")
	if line["level"] != "WARN" || line["kind"] != "render" || line["name"] != "TypeError" || line["request_method"] != "GET" || line["request_status"] != float64(504) {
		t.Fatalf("client error line = %v", line)
	}
	if line["request_id"] != resp.Header().Get(requestIDHeader) {
		t.Fatalf("client error line request_id = %v, response %q", line["request_id"], resp.Header().Get(requestIDHeader))
	}
	if line["page"] != "https://apexload.example.com/scripts/3f2a9c1e-77aa-4c1e-9d0e-5b1f0c2d3e4f#/editor" ||
		line["request_path"] != "/api/v1/scripts/3f2a9c1e-77aa-4c1e-9d0e-5b1f0c2d3e4f" {
		t.Fatalf("locations kept query state or lost resource IDs: page=%v request_path=%v", line["page"], line["request_path"])
	}
	if stack, _ := line["stack"].(string); len(stack) > maxClientErrorStackBytes || !strings.HasPrefix(stack, "at ScriptEditor") {
		t.Fatalf("stack not bounded: %d bytes", len(stack))
	}
	if _, ok := line["related_request_id"]; ok {
		t.Fatal("malformed related request ID was logged")
	}
	if out := logs.String(); strings.Contains(out, jwt) || strings.Contains(out, "secret-capability") {
		t.Fatal("client error report disclosed a token")
	}
}

func TestClientErrorReportRejectsMalformedReports(t *testing.T) {
	server, _ := newTimeslotServer(t)
	for name, report := range map[string]any{
		"unknown kind":  map[string]any{"kind": "debug", "message": "x"},
		"empty message": map[string]any{"kind": "error", "message": "   "},
		"wrong type":    map[string]any{"kind": "error", "message": "x", "request_status": "504"},
	} {
		t.Run(name, func(t *testing.T) {
			if resp := postClientErrorReport(t, server, report); resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
			}
		})
	}
	oversized := map[string]any{"kind": "error", "message": strings.Repeat("x", maxClientErrorReportBodyBytes)}
	if resp := postClientErrorReport(t, server, oversized); resp.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized report status = %d", resp.Code)
	}
}

func TestClientErrorReportsAreRateLimitedPerReporter(t *testing.T) {
	server, _ := newTimeslotServer(t)
	report := ClientErrorReport{Kind: "network", Message: "Failed to fetch"}
	for i := 0; i < clientErrorReportsPerMinute; i++ {
		if resp := postClientErrorReport(t, server, report); resp.Code != http.StatusNoContent {
			t.Fatalf("report %d status = %d", i+1, resp.Code)
		}
	}
	resp := postClientErrorReport(t, server, report)
	if resp.Code != http.StatusTooManyRequests || resp.Header().Get("Retry-After") == "" {
		t.Fatalf("report over the limit status = %d retry-after=%q", resp.Code, resp.Header().Get("Retry-After"))
	}
}

func TestClientErrorReporterPrefersAuthenticatedPrincipal(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/system/client-errors", nil)
	req.RemoteAddr = "10.1.2.3:4567"
	if got := clientErrorReporter(req); got != "remote:10.1.2.3" {
		t.Fatalf("anonymous reporter = %q", got)
	}
	req = withAuthenticatedContext(req, authResolution{User: &AuthenticatedUser{Subject: "alice"}})
	if got := clientErrorReporter(req); got != "user:alice" {
		t.Fatalf("authenticated reporter = %q", got)
	}
}

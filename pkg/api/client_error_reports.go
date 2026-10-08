package api

import (
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
	"github.com/jasonczech/apexload/pkg/scrub"
)

// Browser-side failures never reach the request log on their own: render
// crashes, uncaught exceptions, and requests that failed before (or without)
// a controller response. The dashboard forwards a deduplicated, rate-limited
// sample here so they land in the same structured log as API failures.
const (
	maxClientErrorReportBodyBytes = 32 << 10
	clientErrorReportsPerMinute   = 30

	maxClientErrorMessageBytes        = 1 << 10
	maxClientErrorStackBytes          = 8 << 10
	maxClientErrorComponentStackBytes = 4 << 10
	maxClientErrorLocationBytes       = 512
	maxClientErrorLabelBytes          = 128
)

var clientErrorReportKinds = map[string]bool{
	"error":              true, // window "error" event
	"unhandledrejection": true, // window "unhandledrejection" event
	"render":             true, // a React error boundary caught a render failure
	"network":            true, // an API request failed or timed out before a response
	"gateway":            true, // a 5xx that carried no controller request ID
}

// ClientErrorReport is one dashboard-observed failure. Every field is
// untrusted browser input: the handler bounds and scrubs each before logging.
type ClientErrorReport struct {
	Kind           string `json:"kind"`
	Message        string `json:"message"`
	Name           string `json:"name,omitempty"`
	Stack          string `json:"stack,omitempty"`
	ComponentStack string `json:"component_stack,omitempty"`
	Source         string `json:"source,omitempty"`
	Page           string `json:"page,omitempty"`
	RequestMethod  string `json:"request_method,omitempty"`
	RequestPath    string `json:"request_path,omitempty"`
	RequestStatus  int    `json:"request_status,omitempty"`
	RequestID      string `json:"request_id,omitempty"`
	AppVersion     string `json:"app_version,omitempty"`
}

func (s *Server) handleReportClientError(w http.ResponseWriter, r *http.Request) {
	var report ClientErrorReport
	if !decodeJSONBody(w, r, &report) {
		return
	}
	report.Kind = strings.TrimSpace(report.Kind)
	if !clientErrorReportKinds[report.Kind] {
		writeError(w, http.StatusBadRequest, "kind must be one of error, unhandledrejection, render, network, gateway")
		return
	}
	if strings.TrimSpace(report.Message) == "" {
		writeError(w, http.StatusBadRequest, "message is required")
		return
	}
	reporter := clientErrorReporter(r)
	if s.cache != nil {
		allowed, _, err := s.cache.AllowRateLimit(r.Context(), "client-error-report:"+reporter, clientErrorReportsPerMinute, time.Minute)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "client error reporting is temporarily unavailable")
			return
		}
		if !allowed {
			w.Header().Set("Retry-After", strconv.Itoa(int(time.Minute/time.Second)))
			writeError(w, http.StatusTooManyRequests, "client error report rate limit exceeded")
			return
		}
	}

	attrs := []slog.Attr{
		slog.String("kind", report.Kind),
		slog.String("message", clientErrorText(report.Message, maxClientErrorMessageBytes)),
		slog.String("reporter", reporter),
		slog.String("request_id", w.Header().Get(requestIDHeader)),
	}
	addText := func(key, value string, maxBytes int) {
		if value = clientErrorText(value, maxBytes); value != "" {
			attrs = append(attrs, slog.String(key, value))
		}
	}
	addText("name", report.Name, maxClientErrorLabelBytes)
	addText("stack", report.Stack, maxClientErrorStackBytes)
	addText("component_stack", report.ComponentStack, maxClientErrorComponentStackBytes)
	addLocation := func(key, value string) {
		if value = clientErrorLocation(value); value != "" {
			attrs = append(attrs, slog.String(key, value))
		}
	}
	addLocation("source", report.Source)
	addLocation("page", report.Page)
	addText("request_method", strings.ToUpper(report.RequestMethod), maxClientErrorLabelBytes)
	addLocation("request_path", report.RequestPath)
	if report.RequestStatus >= 100 && report.RequestStatus <= 599 {
		attrs = append(attrs, slog.Int("request_status", report.RequestStatus))
	}
	if related := strings.TrimSpace(report.RequestID); validRequestID(related) {
		attrs = append(attrs, slog.String("related_request_id", related))
	}
	addText("app_version", report.AppVersion, maxClientErrorLabelBytes)
	addText("user_agent", r.UserAgent(), 256)
	slog.LogAttrs(r.Context(), slog.LevelWarn, "dashboard client error", attrs...)
	w.WriteHeader(http.StatusNoContent)
}

// clientErrorReporter keys rate limiting and attribution: the authenticated
// principal when there is one (auth-bypass deployments have none), otherwise
// the peer address.
func clientErrorReporter(r *http.Request) string {
	resolution := authResolution{}
	resolution.User, _ = AuthenticatedUserFromContext(r.Context())
	resolution.ServiceAccount, _ = AuthenticatedServiceAccountFromContext(r.Context())
	if actor := authResolutionActor(resolution); actor != "" {
		return actor
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return "remote:" + host
}

func clientErrorText(value string, maxBytes int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return scrub.Value(boundedtext.TruncateUTF8(value, maxBytes), scrub.Policy{})
}

// clientErrorLocation keeps the path of a page, script or request URL and
// drops its query and fragment parameters, where browser URLs carry one-time
// tokens (desktop capabilities, OAuth state). A hash route keeps its path.
// Resource IDs in the path are kept: the URL scrubber skips the high-entropy
// heuristic that would otherwise erase them.
func clientErrorLocation(value string) string {
	value = strings.TrimSpace(value)
	if base, fragment, found := strings.Cut(value, "#"); found {
		value = base
		if route, _, _ := strings.Cut(fragment, "?"); strings.HasPrefix(route, "/") {
			value += "#" + route
		}
	}
	if base, rest, found := strings.Cut(value, "?"); found {
		value = base
		if _, route, hashed := strings.Cut(rest, "#"); hashed {
			value += "#" + route
		}
	}
	if value == "" {
		return ""
	}
	return scrub.ValueURL(boundedtext.TruncateUTF8(value, maxClientErrorLocationBytes), scrub.Policy{})
}

package api

import (
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

const (
	desktopBrowserDownloadTTL  = time.Minute
	maxDesktopBrowserDownloads = 64
)

type desktopBrowserDownloadGrant struct {
	capability   string
	resourcePath string
	mode         string
	devViewMode  string
	devProjectID string
	issuedAt     time.Time
	expiresAt    time.Time
}

type createDesktopBrowserDownloadRequest struct {
	ResourcePath string `json:"resourcePath"`
	Mode         string `json:"mode"`
}

// handleCreateDesktopBrowserDownload exchanges a per-tab browser capability
// for a short-lived, single-use navigation URL. Browser navigations cannot add
// the sessionStorage-backed capability header, so the server retains it only
// long enough to re-dispatch one reviewed download request in-process.
func (s *Server) handleCreateDesktopBrowserDownload(w http.ResponseWriter, r *http.Request) {
	capability, ok := s.requireDesktopBrowserCapability(w, r)
	if !ok {
		return
	}
	var request createDesktopBrowserDownloadRequest
	if !decodeJSONBody(w, r, &request) {
		return
	}
	resourcePath, err := validateDesktopBrowserDownloadPath(request.ResourcePath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	mode := strings.ToLower(strings.TrimSpace(request.Mode))
	if mode == "" {
		mode = "download"
	}
	if mode != "download" && mode != "open" {
		writeError(w, http.StatusBadRequest, "desktop browser download mode must be download or open")
		return
	}
	devMode, devModeOK := devPreviewModeForRequest(normalizeAuthSettings(s.runner.GetSystemSettings().Auth), r)
	devProjectID := ""
	if devModeOK && devMode == devViewModeUser {
		// Keep the exact normalized input semantics: an absent project header
		// means global-viewer-only. Converting it to the default project here
		// would accidentally grant the preview editor access.
		devProjectID = strings.TrimSpace(r.Header.Get(devProjectIDHeader))
	}
	token, err := generateDesktopSecret()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "desktop browser download could not be authorized")
		return
	}

	now := time.Now().UTC()
	s.desktopBootstrapMu.Lock()
	for existing, grant := range s.desktopBrowserDownloads {
		if !grant.expiresAt.After(now) {
			delete(s.desktopBrowserDownloads, existing)
		}
	}
	if len(s.desktopBrowserDownloads) >= maxDesktopBrowserDownloads {
		var oldestToken string
		var oldestIssuedAt time.Time
		for existing, grant := range s.desktopBrowserDownloads {
			if oldestToken == "" || grant.issuedAt.Before(oldestIssuedAt) {
				oldestToken = existing
				oldestIssuedAt = grant.issuedAt
			}
		}
		delete(s.desktopBrowserDownloads, oldestToken)
	}
	s.desktopBrowserDownloads[token] = desktopBrowserDownloadGrant{
		capability:   capability,
		resourcePath: resourcePath,
		mode:         mode,
		devViewMode:  devMode,
		devProjectID: devProjectID,
		issuedAt:     now,
		expiresAt:    now.Add(desktopBrowserDownloadTTL),
	}
	s.desktopBootstrapMu.Unlock()

	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]string{
		"url": "/desktop/download?token=" + url.QueryEscape(token),
	})
}

func (s *Server) handleDesktopBrowserDownload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	grant, ok := s.takeDesktopBrowserDownload(token, time.Now().UTC())
	if !ok {
		http.Error(w, "desktop browser download is invalid or expired", http.StatusForbidden)
		return
	}
	if s.httpServer == nil || s.httpServer.Handler == nil {
		http.Error(w, "desktop browser download is unavailable", http.StatusServiceUnavailable)
		return
	}
	target, err := url.ParseRequestURI(grant.resourcePath)
	if err != nil {
		http.Error(w, "desktop browser download target is invalid", http.StatusInternalServerError)
		return
	}

	inner := r.Clone(r.Context())
	inner.Method = http.MethodGet
	inner.URL = target
	inner.RequestURI = target.RequestURI()
	inner.Body = http.NoBody
	inner.GetBody = nil
	inner.ContentLength = 0
	// Do not forward ambient loopback cookies, Range, Origin, or any other
	// browser-controlled header. The retained tab capability supplies only the
	// in-process authentication material, and a full 200 response also avoids a
	// second range request trying to reuse this one-time URL.
	inner.Header = make(http.Header)
	inner.Header.Set(desktopCapabilityHeader, grant.capability)
	if grant.devViewMode != "" {
		inner.Header.Set(devViewHeader, grant.devViewMode)
		if grant.devProjectID != "" {
			inner.Header.Set(devProjectIDHeader, grant.devProjectID)
		}
	}
	response := newDesktopBrowserDownloadResponseWriter(w, grant.mode == "download")
	s.httpServer.Handler.ServeHTTP(response, inner)
	response.finish()
}

// desktopBrowserDownloadResponseWriter delays the downstream status/header
// commit just long enough to distinguish an artifact from an error. Successful
// download-mode responses must opt in with Content-Disposition: attachment;
// errors never retain artifact disposition/integrity headers and therefore
// remain visible browser errors instead of being saved under a trusted suffix.
type desktopBrowserDownloadResponseWriter struct {
	destination       http.ResponseWriter
	header            http.Header
	requireAttachment bool
	wroteHeader       bool
	discardBody       bool
}

func newDesktopBrowserDownloadResponseWriter(destination http.ResponseWriter, requireAttachment bool) *desktopBrowserDownloadResponseWriter {
	return &desktopBrowserDownloadResponseWriter{
		destination:       destination,
		header:            make(http.Header),
		requireAttachment: requireAttachment,
	}
}

func (w *desktopBrowserDownloadResponseWriter) Header() http.Header { return w.header }

// Unwrap lets http.ResponseController reach the real connection. Archive and
// library download handlers extend the server's short default write deadline;
// without this hook, redispatch through the desktop gate silently discarded
// that extension and truncated sufficiently large browser downloads.
func (w *desktopBrowserDownloadResponseWriter) Unwrap() http.ResponseWriter {
	return w.destination
}

func (w *desktopBrowserDownloadResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	if status >= http.StatusMultipleChoices && status < http.StatusBadRequest {
		// A browser navigation follows Location automatically, which would let a
		// downstream redirect escape this status/disposition gate and turn the
		// one-time URL into an unreviewed external download. Native desktop
		// downloads reject redirects too; keep both handoff paths equivalent.
		w.discardBody = true
		w.destination.Header().Set("Cache-Control", "no-store")
		w.destination.Header().Set("Referrer-Policy", "no-referrer")
		w.destination.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.destination.Header().Del("Content-Disposition")
		w.destination.Header().Del("Location")
		w.destination.WriteHeader(http.StatusBadGateway)
		_, _ = w.destination.Write([]byte("desktop browser download target returned a redirect\n"))
		return
	}
	if status >= http.StatusOK && status < http.StatusMultipleChoices && w.requireAttachment && !isAttachmentDisposition(w.header.Get("Content-Disposition")) {
		w.discardBody = true
		w.destination.Header().Set("Cache-Control", "no-store")
		w.destination.Header().Set("Referrer-Policy", "no-referrer")
		w.destination.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.destination.Header().Del("Content-Disposition")
		w.destination.WriteHeader(http.StatusBadGateway)
		_, _ = w.destination.Write([]byte("desktop browser download target did not return an attachment\n"))
		return
	}
	copyDesktopBrowserDownloadHeaders(w.destination.Header(), w.header, status)
	w.destination.Header().Set("Cache-Control", "no-store")
	w.destination.Header().Set("Referrer-Policy", "no-referrer")
	w.destination.WriteHeader(status)
}

func (w *desktopBrowserDownloadResponseWriter) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.discardBody {
		return len(data), nil
	}
	return w.destination.Write(data)
}

func (w *desktopBrowserDownloadResponseWriter) Flush() {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if !w.discardBody {
		if flusher, ok := w.destination.(http.Flusher); ok {
			flusher.Flush()
		}
	}
}

func (w *desktopBrowserDownloadResponseWriter) finish() {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
}

func isAttachmentDisposition(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return value == "attachment" || strings.HasPrefix(value, "attachment;")
}

func copyDesktopBrowserDownloadHeaders(destination, source http.Header, status int) {
	errorResponse := status < http.StatusOK || status >= http.StatusMultipleChoices
	for name, values := range source {
		if errorResponse && isDesktopBrowserArtifactHeader(name) {
			continue
		}
		destination[name] = append([]string(nil), values...)
	}
	if errorResponse {
		for name := range destination {
			if isDesktopBrowserArtifactHeader(name) {
				destination.Del(name)
			}
		}
	}
}

func isDesktopBrowserArtifactHeader(name string) bool {
	switch http.CanonicalHeaderKey(name) {
	case "Content-Disposition", "Content-Range", "Accept-Ranges", "Etag", "Last-Modified", "Digest", "X-Content-Sha256":
		return true
	default:
		return false
	}
}

func (s *Server) takeDesktopBrowserDownload(token string, now time.Time) (desktopBrowserDownloadGrant, bool) {
	if s == nil || token == "" {
		return desktopBrowserDownloadGrant{}, false
	}
	s.desktopBootstrapMu.Lock()
	defer s.desktopBootstrapMu.Unlock()
	grant, ok := s.desktopBrowserDownloads[token]
	if ok {
		delete(s.desktopBrowserDownloads, token)
	}
	if !ok || !grant.expiresAt.After(now) {
		return desktopBrowserDownloadGrant{}, false
	}
	if _, ok := s.desktopBrowserCapabilities[grant.capability]; !ok {
		return desktopBrowserDownloadGrant{}, false
	}
	return grant, true
}

func validateDesktopBrowserDownloadPath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("desktop browser download resourcePath is required")
	}
	target, err := url.ParseRequestURI(raw)
	if err != nil {
		return "", fmt.Errorf("invalid desktop browser download resourcePath")
	}
	if target.IsAbs() || target.Host != "" || target.User != nil || target.Fragment != "" || target.Opaque != "" || strings.HasPrefix(target.Path, "//") {
		return "", fmt.Errorf("desktop browser download must target this controller")
	}
	if !strings.HasPrefix(target.Path, "/api/v1/") || strings.Contains(target.Path, "\\") || path.Clean(target.Path) != target.Path {
		return "", fmt.Errorf("desktop browser download path is invalid")
	}
	segments := strings.Split(strings.TrimPrefix(target.Path, "/"), "/")
	for _, segment := range segments {
		if segment == "" {
			return "", fmt.Errorf("desktop browser download path is invalid")
		}
	}
	if !isAllowedDesktopBrowserDownloadSegments(segments) {
		return "", fmt.Errorf("desktop browser download path is not allowed")
	}
	return target.RequestURI(), nil
}

func isAllowedDesktopBrowserDownloadSegments(segments []string) bool {
	if len(segments) < 3 || segments[0] != "api" || segments[1] != "v1" {
		return false
	}
	// Run archives, individual archive artifacts, and generated reports.
	if len(segments) == 5 && segments[2] == "tests" && segments[3] != "" && (segments[4] == "archive" || segments[4] == "report") {
		return true
	}
	if len(segments) == 6 && segments[2] == "tests" && segments[3] != "" && segments[4] == "archive" && (segments[5] == "browser-diagnostics" || segments[5] == "report") {
		return true
	}
	if len(segments) == 7 && segments[2] == "tests" && segments[3] != "" && segments[4] == "archive" && segments[5] == "files" {
		return true
	}
	// Bruno source bundles.
	if len(segments) == 6 && segments[2] == "scripts" && segments[3] != "" && segments[4] == "bruno-imports" && segments[5] == "source" {
		return true
	}
	// Staged workstation clients.
	if len(segments) == 5 && segments[2] == "downloads" && (segments[3] == "recording-agent" || segments[3] == "desktop") && segments[4] == "latest" {
		return true
	}
	// Controller-native and side-car HAR exports.
	if len(segments) == 5 && segments[2] == "recorder" && segments[3] != "" && segments[4] == "export" {
		return true
	}
	return len(segments) == 6 && segments[2] == "browser-recorder" && segments[3] == "sessions" && segments[4] != "" && segments[5] == "export"
}

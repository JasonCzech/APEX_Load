package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/convert/har"
	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/convert/salesforce"
	"github.com/jasonczech/apexload/pkg/dsl"
	browserprotocol "github.com/jasonczech/apexload/pkg/protocol/browser"
	recbrowser "github.com/jasonczech/apexload/pkg/recorder/browser"
	"github.com/jasonczech/apexload/pkg/scrub"
	"github.com/jasonczech/apexload/pkg/storage"
)

// browserRecordingAvailability reports whether this controller can launch a
// headed recording browser, with a human-readable reason when it cannot.
type browserRecordingAvailability struct {
	Available bool   `json:"available"`
	Code      string `json:"code,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

const (
	browserRecordingUnavailableDisabled             = "disabled"
	browserRecordingUnavailableProductionDisabled   = "production_disabled"
	browserRecordingUnavailableDisplayMissing       = "display_missing"
	browserRecordingUnavailableBrowsersNotInstalled = "browsers_not_installed"
)

var browserRecordingDependencyProbe = recbrowser.CheckChromiumInstalled

// browserRecordingAvailability gates the feature server-side (the UI hint is
// advisory; POST /sessions fails closed on this check). Headed recording is
// off in production posture unless explicitly enabled, and needs a display.
func (s *Server) browserRecordingAvailability() browserRecordingAvailability {
	switch strings.ToLower(envValue("APEXLOAD_BROWSER_RECORDER")) {
	case "off", "disabled", "false", "0":
		return browserRecordingAvailability{Code: browserRecordingUnavailableDisabled, Reason: "browser recording is disabled via APEXLOAD_BROWSER_RECORDER"}
	case "on", "enabled", "true", "1":
		// Explicit opt-in overrides the production-mode default-off.
	default:
		if productionMode() && !s.desktopMode && !s.bridgeMode {
			return browserRecordingAvailability{Code: browserRecordingUnavailableProductionDisabled, Reason: "browser recording launches a headed browser on the controller host and is disabled in production mode (set APEXLOAD_BROWSER_RECORDER=on to override)"}
		}
	}
	if runtime.GOOS == "linux" && strings.TrimSpace(os.Getenv("DISPLAY")) == "" && strings.TrimSpace(os.Getenv("WAYLAND_DISPLAY")) == "" {
		return browserRecordingAvailability{Code: browserRecordingUnavailableDisplayMissing, Reason: "no display detected on the controller host for a headed browser"}
	}
	if browserRecordingDependencyProbe != nil {
		if err := browserRecordingDependencyProbe(); err != nil {
			if availability, ok := browserRecordingAvailabilityFromStartError(err); ok {
				return availability
			}
			return browserRecordingAvailability{Code: browserRecordingUnavailableBrowsersNotInstalled, Reason: "Playwright Chromium is not available on the controller host; install the recording browser and retry"}
		}
	}
	return browserRecordingAvailability{Available: true}
}

func browserRecordingAvailabilityFromStartError(err error) (browserRecordingAvailability, bool) {
	if err == nil {
		return browserRecordingAvailability{}, false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "please install") ||
		strings.Contains(msg, "playwright install") ||
		strings.Contains(msg, "executable doesn't exist") ||
		strings.Contains(msg, "executable does not exist") ||
		strings.Contains(msg, "browser has not been installed") {
		return browserRecordingAvailability{
			Code:   browserRecordingUnavailableBrowsersNotInstalled,
			Reason: "Playwright Chromium is not installed on the controller host; install the recording browser and retry",
		}, true
	}
	return browserRecordingAvailability{}, false
}

// browserStorageStateDir is the confined subdirectory (under the export base)
// where recording storage states are persisted and read back for login reuse.
const browserStorageStateDir = "browser-recorder-state"

// browserStorageStateProjectDir is the project-scoped directory segment used by
// browserStorageStateExportLocation; a nil/empty project maps to "unassigned".
func browserStorageStateProjectDir(projectID string) string {
	dir := sanitizeDownloadName(normalizeProjectID(projectID))
	if dir == "" {
		return "unassigned"
	}
	return dir
}

// confineStorageStatePath resolves a client-supplied storage-state path to an
// absolute path guaranteed to live under the export base dir, rejecting
// traversal/absolute escapes. The file must already exist. It is also scoped to
// the requesting recording's project: the persisted reuse copy contains a RAW,
// unscrubbed Playwright storage state (live session cookies), so without this
// check a user could seed a new recording with another project's live session by
// pointing storage_state_path at that project's export subdirectory.
func confineStorageStatePath(raw, projectID string) (string, error) {
	cleaned := strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/")
	if cleaned == "" || strings.HasPrefix(cleaned, "/") || filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("storage_state_path is not allowed: absolute paths are not permitted")
	}
	cleaned = strings.Trim(cleaned, "/")
	cleaned = strings.TrimPrefix(cleaned, browserStorageStateDir+"/")
	for _, part := range strings.Split(cleaned, "/") {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("storage_state_path is not allowed: path traversal is not permitted")
		}
	}
	// Reject a storage state belonging to a different project (the path is
	// project-scoped as browserStorageStateDir/<project>/<file>).
	if segments := strings.Split(cleaned, "/"); len(segments) < 2 || segments[0] != browserStorageStateProjectDir(projectID) {
		return "", fmt.Errorf("storage_state_path is not allowed: it must belong to this recording's project")
	}
	dir, file := filepathSplitForward(cleaned)
	resolved, err := resolveConfinedExportPath(exportBaseDir(), filepath.Join(browserStorageStateDir, dir), file)
	if err != nil {
		return "", fmt.Errorf("storage_state_path is not allowed: %w", err)
	}
	if _, statErr := os.Stat(resolved); statErr != nil {
		return "", fmt.Errorf("storage_state_path %q not found under the export directory", raw)
	}
	return resolved, nil
}

func filepathSplitForward(p string) (dir, file string) {
	p = filepath.ToSlash(p)
	idx := strings.LastIndex(p, "/")
	if idx < 0 {
		return "", p
	}
	return p[:idx], p[idx+1:]
}

func browserStorageStateExportLocation(info *BrowserRecordingSession) (relativePath, stateFile string) {
	projectDir := browserStorageStateProjectDir(info.ProjectID)
	filename := sanitizeDownloadName(info.Name)
	if filename == "" {
		filename = "browser-recording"
	}
	if sessionID := sanitizeDownloadName(info.ID); sessionID != "" {
		filename += "-" + sessionID
	}
	stateFile = filename + "-state.json"
	relativePath = filepath.ToSlash(filepath.Join(browserStorageStateDir, projectDir, stateFile))
	return relativePath, stateFile
}

func pruneBrowserStorageStateExports(now time.Time) {
	if defaultBrowserRecordingRetention <= 0 {
		return
	}
	root := filepath.Join(exportBaseDir(), browserStorageStateDir)
	cutoff := now.Add(-defaultBrowserRecordingRetention)
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(d.Name()) != ".json" {
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil {
			return nil
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(path)
		}
		return nil
	})
}

func scrubBrowserStorageState(data []byte) ([]byte, error) {
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	scrubBrowserStorageStateNode(doc, "")
	return json.Marshal(doc)
}

func scrubBrowserStorageStateNode(node any, parentKey string) {
	switch typed := node.(type) {
	case []any:
		for _, item := range typed {
			scrubBrowserStorageStateNode(item, parentKey)
		}
	case map[string]any:
		if value, ok := typed["value"].(string); ok {
			name, _ := typed["name"].(string)
			switch {
			case parentKey == "cookies":
				typed["value"] = scrub.Redacted
			case browserStorageStateSensitiveName(name):
				typed["value"] = scrub.Redacted
			default:
				typed["value"] = scrub.Value(value, scrub.Policy{})
			}
		}
		for key, value := range typed {
			if text, ok := value.(string); ok {
				if browserStorageStateSensitiveName(key) {
					typed[key] = scrub.Redacted
				} else {
					typed[key] = scrub.Value(text, scrub.Policy{})
				}
				continue
			}
			scrubBrowserStorageStateNode(value, key)
		}
	}
}

func browserStorageStateSensitiveName(name string) bool {
	normalized := strings.ToLower(strings.TrimSpace(name))
	normalized = strings.NewReplacer("-", "_", " ", "_").Replace(normalized)
	if normalized == "" {
		return false
	}
	for _, marker := range []string{
		"authorization", "password", "passwd", "secret", "token", "access_token",
		"refresh_token", "id_token", "api_key", "apikey", "session", "session_id",
		"sessionid", "auth", "csrf", "xsrf", "client_secret", "private_key",
		"jwt", "bearer", "cookie",
	} {
		if normalized == marker || strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

// validateBrowserRecordingStartURL restricts recording targets to absolute
// http(s) URLs so the controller-driven browser cannot be pointed at local
// files or other schemes. Private/loopback hosts are intentionally allowed:
// recording apps on localhost is the local-first use case.
func validateBrowserRecordingStartURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("start_url must be an absolute http(s) URL")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		return nil
	default:
		return fmt.Errorf("start_url scheme %q is not allowed; use http or https", parsed.Scheme)
	}
}

func (s *Server) handleStartBrowserRecording(w http.ResponseWriter, r *http.Request) {
	availability := s.browserRecordingAvailability()
	if !availability.Available {
		writeError(w, http.StatusServiceUnavailable, availability.Reason)
		return
	}

	var req BrowserRecordingStartRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if err := validateBrowserRecordingStartURL(req.StartURL); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// A storage-state path is a server-side file Playwright will read; confine
	// it to the export directory so a client cannot point it at arbitrary host
	// files (path traversal / absolute paths).
	if strings.TrimSpace(req.StorageStatePath) != "" {
		confined, err := confineStorageStatePath(req.StorageStatePath, req.ProjectID)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		req.StorageStatePath = confined
	}

	session, err := s.browserRecording.Start(req)
	if err != nil {
		if errors.Is(err, errBrowserRecordingActive) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		if availability, ok := browserRecordingAvailabilityFromStartError(err); ok {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"error":        availability.Reason,
				"availability": availability,
			})
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to start browser recording: "+err.Error())
		return
	}

	s.recordAuditEvent(r, "browser_recording.start", "success", "browser_recording", session.ID, session.ProjectID,
		map[string]any{"name": session.Name, "start_url": scrub.ValueURL(session.StartURL, scrub.Policy{}), "capture_har": session.CaptureHAR})
	writeJSON(w, http.StatusCreated, map[string]any{"session": session})
}

func (s *Server) handleGetBrowserRecording(w http.ResponseWriter, r *http.Request) {
	session, ok := s.browserRecording.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "browser recording session not found")
		return
	}
	raw := rawRecordingExportRequested(r)
	if raw && !s.canExportRawRecording(r, session.ProjectID) {
		s.recordAuditEvent(r, "browser_recording.read_raw", "denied", "browser_recording", session.ID, session.ProjectID, map[string]any{"reason": "insufficient_role"})
		writeError(w, http.StatusForbidden, "raw browser recording view requires editor, project_admin, or system_admin access")
		return
	}
	if !raw {
		session = sanitizeBrowserRecordingSessionForResponse(session)
		w.Header().Set("X-APEXLoad-Sensitivity", scrub.SensitivityScrubbed)
	} else {
		w.Header().Set("X-APEXLoad-Sensitivity", scrub.SensitivityRaw)
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": session})
}

func (s *Server) handleListBrowserRecordings(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	sessions := s.browserRecording.List(projectID)
	for index := range sessions {
		sessions[index] = sanitizeBrowserRecordingSessionForResponse(sessions[index])
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions, "count": len(sessions)})
}

func (s *Server) handleDeleteBrowserRecording(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	session, ok, err := s.browserRecording.Delete(id)
	if !ok {
		writeError(w, http.StatusNotFound, "browser recording session not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	s.recordAuditEvent(r, "browser_recording.delete", "success", "browser_recording", session.ID, session.ProjectID,
		map[string]any{"status": session.Status, "step_count": session.StepCount})
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id, "session": session})
}

func (s *Server) handleStopBrowserRecording(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	session, ok := s.browserRecording.Stop(id)
	if !ok {
		writeError(w, http.StatusNotFound, "browser recording session not found")
		return
	}
	s.recordAuditEvent(r, "browser_recording.stop", "success", "browser_recording", session.ID, session.ProjectID,
		map[string]any{"stop_reason": session.StopReason, "step_count": session.StepCount})
	writeJSON(w, http.StatusOK, map[string]any{"session": session})
}

func (s *Server) handleForceStopBrowserRecording(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	session, ok, err := s.browserRecording.ForceStop(id)
	if !ok {
		writeError(w, http.StatusNotFound, "browser recording session not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	s.recordAuditEvent(r, "browser_recording.force_stop", "success", "browser_recording", session.ID, session.ProjectID,
		map[string]any{"stop_reason": session.StopReason, "step_count": session.StepCount})
	writeJSON(w, http.StatusOK, map[string]any{"session": session})
}

func (s *Server) handlePauseBrowserRecording(w http.ResponseWriter, r *http.Request) {
	session, ok, err := s.browserRecording.Pause(r.PathValue("id"))
	s.writeBrowserRecordingControlResult(w, session, ok, err)
}

func (s *Server) handleResumeBrowserRecording(w http.ResponseWriter, r *http.Request) {
	session, ok, err := s.browserRecording.Resume(r.PathValue("id"))
	s.writeBrowserRecordingControlResult(w, session, ok, err)
}

func (s *Server) handleNavigateBrowserRecording(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	// Same scheme allow-list as the start URL: the panel must not be able to
	// point the recording browser at local files or other schemes.
	if err := validateBrowserRecordingStartURL(req.URL); err != nil {
		writeError(w, http.StatusBadRequest, strings.Replace(err.Error(), "start_url", "url", 1))
		return
	}
	id := r.PathValue("id")
	session, ok, err := s.browserRecording.Navigate(id, req.URL)
	if ok && err == nil {
		s.recordAuditEvent(r, "browser_recording.navigate", "success", "browser_recording", id, session.ProjectID,
			map[string]any{"url": scrub.ValueURL(req.URL, scrub.Policy{})})
	}
	s.writeBrowserRecordingControlResult(w, session, ok, err)
}

func (s *Server) handleFocusBrowserRecording(w http.ResponseWriter, r *http.Request) {
	session, ok, err := s.browserRecording.Focus(r.PathValue("id"))
	s.writeBrowserRecordingControlResult(w, session, ok, err)
}

type browserRecordingControlPanelRequest struct {
	URL              string                                `json:"url"`
	BridgeConnection *browserRecordingBridgeConnectionSeed `json:"bridge_connection,omitempty"`
}

type browserRecordingBridgeConnectionSeed struct {
	Origin           string `json:"origin"`
	Token            string `json:"token"`
	SessionID        string `json:"session_id"`
	PairedOrigin     string `json:"paired_origin,omitempty"`
	PairedAt         string `json:"paired_at,omitempty"`
	PairingExpiresAt string `json:"pairing_expires_at,omitempty"`
	SessionExpiresAt string `json:"session_expires_at,omitempty"`
}

func (s *Server) handleOpenBrowserRecordingControlPanel(w http.ResponseWriter, r *http.Request) {
	var req browserRecordingControlPanelRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if err := recbrowser.ValidateControlPanelURL(req.URL); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	panelOrigin, err := absoluteHTTPOrigin(req.URL)
	if err != nil {
		writeError(w, http.StatusBadRequest, "control panel url origin is invalid: "+err.Error())
		return
	}
	if requestOrigin := strings.TrimSpace(r.Header.Get("Origin")); requestOrigin != "" {
		normalizedRequestOrigin, err := absoluteHTTPOrigin(requestOrigin)
		if err != nil {
			writeError(w, http.StatusBadRequest, "request Origin is invalid: "+err.Error())
			return
		}
		if normalizedRequestOrigin != panelOrigin {
			writeError(w, http.StatusBadRequest, "control panel url must use the requesting dashboard origin")
			return
		}
	}
	localStorage := map[string]string{}
	if req.BridgeConnection != nil {
		key, value, err := req.BridgeConnection.localStorageEntry(req.URL)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		localStorage[key] = value
	}

	id := r.PathValue("id")
	session, ok, err := s.browserRecording.OpenControlPanel(id, req.URL, localStorage)
	if ok && err == nil {
		s.recordAuditEvent(r, "browser_recording.control_panel", "success", "browser_recording", id, session.ProjectID,
			map[string]any{"url": scrub.ValueURL(req.URL, scrub.Policy{})})
	}
	s.writeBrowserRecordingControlResult(w, session, ok, err)
}

func (b *browserRecordingBridgeConnectionSeed) localStorageEntry(panelURL string) (string, string, error) {
	if b == nil {
		return "", "", fmt.Errorf("bridge_connection is required")
	}
	remoteOrigin, err := absoluteHTTPOrigin(panelURL)
	if err != nil {
		return "", "", fmt.Errorf("control panel url origin is invalid: %w", err)
	}
	bridgeOrigin, err := absoluteHTTPOrigin(b.Origin)
	if err != nil {
		return "", "", fmt.Errorf("bridge_connection.origin is invalid: %w", err)
	}
	b.Origin = bridgeOrigin
	b.Token = strings.TrimSpace(b.Token)
	b.SessionID = strings.TrimSpace(b.SessionID)
	if b.Token == "" || b.SessionID == "" {
		return "", "", fmt.Errorf("bridge_connection token and session_id are required")
	}
	encoded, err := json.Marshal(b)
	if err != nil {
		return "", "", fmt.Errorf("encode bridge_connection: %w", err)
	}
	return recbrowser.BrowserRecorderBridgeStoragePrefix + remoteOrigin, string(encoded), nil
}

func absoluteHTTPOrigin(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("must be an absolute http(s) URL")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		return parsed.Scheme + "://" + parsed.Host, nil
	default:
		return "", fmt.Errorf("scheme %q is not allowed; use http or https", parsed.Scheme)
	}
}

func (s *Server) handleStartBrowserRecordingTransaction(w http.ResponseWriter, r *http.Request) {
	s.handleBrowserRecordingMarker(w, r, recbrowser.MarkerTransactionStart)
}

func (s *Server) handleEndBrowserRecordingTransaction(w http.ResponseWriter, r *http.Request) {
	s.handleBrowserRecordingMarker(w, r, recbrowser.MarkerTransactionEnd)
}

func (s *Server) handleAddBrowserRecordingNote(w http.ResponseWriter, r *http.Request) {
	s.handleBrowserRecordingMarker(w, r, recbrowser.MarkerNote)
}

func (s *Server) handleBrowserRecordingMarker(w http.ResponseWriter, r *http.Request, kind string) {
	var req struct {
		Name      string `json:"name,omitempty"`
		Text      string `json:"text,omitempty"`
		AtStepSeq int    `json:"at_step_seq,omitempty"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	session, marker, ok, err := s.browserRecording.AddMarker(r.PathValue("id"), kind, req.Name, req.Text, req.AtStepSeq)
	if !ok {
		writeError(w, http.StatusNotFound, "browser recording session not found")
		return
	}
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not live") {
			status = http.StatusConflict
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": session, "marker": marker})
}

// writeBrowserRecordingControlResult maps the shared (session, found, error)
// control-method result onto HTTP: 404 unknown id, 409 not-live, 502 backend
// failures (the browser refused the command), 200 with the fresh snapshot.
func (s *Server) writeBrowserRecordingControlResult(w http.ResponseWriter, session *BrowserRecordingSession, ok bool, err error) {
	if !ok {
		writeError(w, http.StatusNotFound, "browser recording session not found")
		return
	}
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		if strings.Contains(err.Error(), "not live") || strings.Contains(err.Error(), "no stopped artifact") {
			// Both are client-state preconditions (session not live / not yet
			// stopped), not backend failures — 409, not 502.
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": session})
}

type browserRecordingConvertRequest struct {
	Name           string `json:"name"`
	MaxThinkTimeMs int    `json:"max_think_time_ms,omitempty"`
	// StartSeq/EndSeq trim conversion to an inclusive step window (0 = open).
	StartSeq int `json:"start_seq,omitempty"`
	EndSeq   int `json:"end_seq,omitempty"`
	// SplitAtTransactions emits one script per top-level transaction.
	SplitAtTransactions bool `json:"split_at_transactions,omitempty"`
	// AlsoCreateHTTPScript additionally generates a protocol-level HTTP script
	// from the recorded HAR (dual-script generation).
	AlsoCreateHTTPScript bool `json:"also_create_http_script,omitempty"`
	// Parameterize applies safe auto-parameterization suggestions to the
	// converted browser script(s) before preview/save.
	Parameterize bool `json:"parameterize,omitempty"`
	// SecretBindings are save-time, value-free operator choices from the
	// preview. They are applied to the fresh conversion before the atomic
	// create transaction so a same-name "Use project secret" choice cannot be
	// lost or depend on a fallible post-commit update.
	SecretBindings []browserRecordingSecretBinding `json:"secret_bindings,omitempty"`
	// ParameterizationSelections identify only the value-free suggestions the
	// operator selected in the preview. The server re-derives those suggestions
	// from the fresh conversion and applies them before the atomic create
	// transaction; the browser never supplies mutation instructions.
	ParameterizationSelections []browserRecordingParameterizationSelection `json:"parameterization_selections,omitempty"`
	// ExpectedScriptCount fences the preview/save handoff. A changed conversion
	// shape must fail before persistence rather than silently pairing reviewed
	// scripts with a different fresh conversion.
	ExpectedScriptCount int `json:"expected_script_count,omitempty"`
}

// browserRecordingPreviewRequest deliberately excludes save-only secret
// bindings. Keeping a separate wire type makes the generated API contract
// truthful and lets DisallowUnknownFields reject a binding sent to preview.
type browserRecordingPreviewRequest struct {
	Name                 string `json:"name"`
	MaxThinkTimeMs       int    `json:"max_think_time_ms,omitempty"`
	StartSeq             int    `json:"start_seq,omitempty"`
	EndSeq               int    `json:"end_seq,omitempty"`
	SplitAtTransactions  bool   `json:"split_at_transactions,omitempty"`
	AlsoCreateHTTPScript bool   `json:"also_create_http_script,omitempty"`
	Parameterize         bool   `json:"parameterize,omitempty"`
}

func (req browserRecordingPreviewRequest) convertRequest() browserRecordingConvertRequest {
	return browserRecordingConvertRequest{
		Name:                 req.Name,
		MaxThinkTimeMs:       req.MaxThinkTimeMs,
		StartSeq:             req.StartSeq,
		EndSeq:               req.EndSeq,
		SplitAtTransactions:  req.SplitAtTransactions,
		AlsoCreateHTTPScript: req.AlsoCreateHTTPScript,
		Parameterize:         req.Parameterize,
	}
}

type browserRecordingSecretBinding struct {
	RequirementID string `json:"requirement_id"`
	SecretName    string `json:"secret_name"`
	ScriptIndex   int    `json:"script_index"`
}

type browserRecordingParameterizationSelection struct {
	ScriptIndex int      `json:"script_index"`
	SelectedIDs []string `json:"selected_ids"`
}

const maxBridgedBrowserImportScripts = 64
const maxBrowserRecordingParameterizationSelections = 4096

// handleAttestBridgedBrowserRecordingImport is an authenticated,
// operator-asserted handoff for a reviewed draft from the local recording flow
// to the remote controller. Local and remote controllers do not share
// in-memory attestation stores, so a local token can never authorize the later
// remote library write. This browser-recorder-specific boundary strips that
// token, validates the source/protocol pairing and project scope, then issues
// remote one-shot attestations over the exact credential locations and values
// the operator already reviewed.
func (s *Server) handleAttestBridgedBrowserRecordingImport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProjectID string       `json:"project_id,omitempty"`
		Scripts   []dsl.Script `json:"scripts"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if len(req.Scripts) == 0 || len(req.Scripts) > maxBridgedBrowserImportScripts {
		writeError(w, http.StatusBadRequest, "between 1 and 64 browser recording scripts are required")
		return
	}
	projectHint := jsonImportProjectID(r, req.ProjectID)
	if strings.TrimSpace(projectHint) == "" {
		for index := range req.Scripts {
			if strings.TrimSpace(req.Scripts[index].ProjectID) != "" {
				projectHint = req.Scripts[index].ProjectID
				break
			}
		}
	}
	projectID := authorizedProjectOr(r, projectHint)
	if !requireAuthorizedProjectMatch(w, r, projectID) {
		return
	}

	scripts := make([]*dsl.Script, len(req.Scripts))
	scriptValues := make([]any, len(req.Scripts))
	for index := range req.Scripts {
		script := &req.Scripts[index]
		if strings.TrimSpace(script.ProjectID) != "" &&
			normalizeProjectID(script.ProjectID) != projectID {
			writeError(w, http.StatusBadRequest, "browser recording script belongs to a different project")
			return
		}
		script.ProjectID = projectID
		protocol := strings.ToLower(strings.TrimSpace(script.Protocol))
		source := ""
		switch protocol {
		case "browser":
			source = "browser_recorder"
		case "http", "soap":
			source = "browser_recording_har"
		default:
			writeError(w, http.StatusBadRequest, "paired browser recording handoff supports only browser and HTTP scripts")
			return
		}
		// A token issued by the local agent has no authority on this server and
		// must not consume capacity or accidentally collide with a remote token.
		stripImportDraftAttestation(script)
		if script.Metadata == nil {
			script.Metadata = &dsl.ScriptMetadata{}
		}
		if script.Metadata.Import == nil {
			script.Metadata.Import = &dsl.ImportMetadata{}
		}
		script.Metadata.Import.Source = source
		if script.Metadata.Import.Preserved != nil {
			delete(
				script.Metadata.Import.Preserved,
				dsl.ImportPreservedCredentialPolicyKey,
			)
		}
		ensureCredentialPreservingImportMetadata(script, source)
		if err := sanitizePersistentImportReviewSurfaces(script); err != nil {
			writeError(w, http.StatusBadRequest, "browser recording review metadata is invalid")
			return
		}
		if err := validateScriptFolderProject(s.store, script); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := validateJavaVMParameterSafety(script, true); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if issues := validateStoredExecutionScript(script); len(issues) > 0 {
			writeError(w, http.StatusBadRequest, strings.Join(issues, "; "))
			return
		}
		scripts[index] = script
		scriptValues[index] = script
	}

	response := s.newScriptImportResponseWithSourceSecrets(r, scriptImportResponseOptions{
		Script:  scripts[0],
		Scripts: scriptValues,
	})
	writeScriptImportResponse(w, http.StatusOK, response)
}

// convertBrowserRecording runs the shared conversion path for preview and
// save: catalog conversion plus the importer parameterization hook, wrapped in
// the standard import-response envelope the review UI consumes.
func (s *Server) convertBrowserRecording(artifact *recbrowser.Session, info *BrowserRecordingSession, req browserRecordingConvertRequest) *recbrowser.ConvertResult {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = info.Name
	}
	result := recbrowser.Convert(artifact, recbrowser.ConvertOptions{
		ScriptName:          name,
		MaxThinkTimeMs:      req.MaxThinkTimeMs,
		StepRange:           recbrowser.StepRange{StartSeq: req.StartSeq, EndSeq: req.EndSeq},
		SplitAtTransactions: req.SplitAtTransactions,
	})
	projectID := normalizeProjectID(info.ProjectID)
	for _, sc := range result.Scripts {
		if sc == nil {
			continue
		}
		sc.ProjectID = projectID
		attachRecordingProvenance(sc, artifact, info)
		if req.Parameterize {
			applyAutoParameterization(sc)
		}
	}
	if result.Script != nil {
		result.Script.ProjectID = projectID
	}
	return result
}

func validateBrowserRecordingSecretBindings(
	bindings []browserRecordingSecretBinding,
	scriptCount int,
) error {
	seen := make(map[string]struct{}, len(bindings))
	for _, binding := range bindings {
		requirementID := strings.TrimSpace(binding.RequirementID)
		secretName := strings.TrimSpace(binding.SecretName)
		if requirementID == "" || secretName == "" {
			return errors.New("secret binding requirement_id and secret_name are required")
		}
		if binding.ScriptIndex < 0 || binding.ScriptIndex >= scriptCount {
			return fmt.Errorf("secret binding for %q targets an unavailable converted script", requirementID)
		}
		key := strconv.Itoa(binding.ScriptIndex) + "\x00" + requirementID
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("secret binding requirement_id %q is duplicated", requirementID)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func applyBrowserRecordingSecretBindings(
	script *dsl.Script,
	scriptIndex int,
	bindings []browserRecordingSecretBinding,
) (*dsl.Script, error) {
	selected := make([]secretBindingSelection, 0)
	for _, binding := range bindings {
		if binding.ScriptIndex != scriptIndex {
			continue
		}
		selected = append(selected, secretBindingSelection{
			RequirementID: binding.RequirementID,
			SecretName:    binding.SecretName,
		})
	}
	if len(selected) == 0 {
		return script, nil
	}
	resolved, _, err := applyCredentialPreservingSecretBindings(
		script,
		scriptIndex,
		selected,
	)
	if err != nil {
		return nil, err
	}
	return resolved, nil
}

func validateBrowserRecordingParameterizationSelections(
	selections []browserRecordingParameterizationSelection,
	scriptCount int,
) error {
	total := 0
	seen := make(map[string]struct{})
	for _, selection := range selections {
		if selection.ScriptIndex < 0 ||
			(scriptCount >= 0 && selection.ScriptIndex >= scriptCount) {
			return fmt.Errorf(
				"parameterization selection targets unavailable converted script %d",
				selection.ScriptIndex,
			)
		}
		if len(selection.SelectedIDs) == 0 {
			return fmt.Errorf(
				"parameterization selection for script %d has no selected_ids",
				selection.ScriptIndex,
			)
		}
		for _, rawID := range selection.SelectedIDs {
			id := strings.TrimSpace(rawID)
			if id == "" {
				return errors.New("parameterization selected_ids cannot contain an empty ID")
			}
			total++
			if total > maxBrowserRecordingParameterizationSelections {
				return fmt.Errorf(
					"parameterization selection exceeds the %d-suggestion limit",
					maxBrowserRecordingParameterizationSelections,
				)
			}
			key := strconv.Itoa(selection.ScriptIndex) + "\x00" + id
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf(
					"parameterization suggestion %q is duplicated for script %d",
					id,
					selection.ScriptIndex,
				)
			}
			seen[key] = struct{}{}
		}
	}
	return nil
}

func applyBrowserRecordingParameterizationSelections(
	script *dsl.Script,
	scriptIndex int,
	selections []browserRecordingParameterizationSelection,
) (*dsl.Script, error) {
	selected := make(map[string]bool)
	for _, selection := range selections {
		if selection.ScriptIndex != scriptIndex {
			continue
		}
		for _, id := range selection.SelectedIDs {
			selected[strings.TrimSpace(id)] = true
		}
	}
	if len(selected) == 0 {
		return script, nil
	}
	trusted := buildTrustedImportParameterization(script)
	trustedByID := make(map[string]struct{}, len(trusted.Suggestions))
	for _, suggestion := range trusted.Suggestions {
		trustedByID[suggestion.ID] = struct{}{}
	}
	for id := range selected {
		if _, ok := trustedByID[id]; !ok {
			return nil, fmt.Errorf(
				"selected suggestion %q is stale or unavailable for converted script %d",
				id,
				scriptIndex,
			)
		}
	}
	applied, _, err := paramize.ApplySuggestions(script, trusted.Suggestions, selected)
	if err != nil {
		return nil, err
	}
	return applied, nil
}

// attachRecordingProvenance stamps the saved script with enough metadata to
// re-convert the recording later (recorder session id, device, step/marker
// counts). Raw step values stay out of provenance; the HAR is referenced, not
// embedded.
func attachRecordingProvenance(script *dsl.Script, artifact *recbrowser.Session, info *BrowserRecordingSession) {
	if script == nil {
		return
	}
	if script.Metadata == nil {
		script.Metadata = &dsl.ScriptMetadata{}
	}
	if script.Metadata.Import == nil {
		script.Metadata.Import = &dsl.ImportMetadata{}
	}
	script.Metadata.Import.Source = "browser_recorder"
	script.Metadata.Import.SourceScriptName = info.Name
	if script.Metadata.Import.Preserved == nil {
		script.Metadata.Import.Preserved = map[string]any{}
	}
	script.Metadata.Import.Preserved["recorder_session_id"] = artifact.ID
	script.Metadata.Import.Preserved["recorded_steps"] = len(artifact.Steps)
	script.Metadata.Import.Preserved["recorded_markers"] = len(artifact.Markers)
	script.Metadata.Import.Preserved["har_available"] = len(artifact.HAR) > 0
	if artifact.DeviceProfile != "" {
		script.Metadata.Import.Preserved["device_profile"] = artifact.DeviceProfile
	}
	if len(artifact.Annotations) > 0 {
		script.Metadata.Import.Preserved["recorded_annotations"] = len(artifact.Annotations)
	}
}

func applyAutoParameterization(script *dsl.Script) {
	if script == nil {
		return
	}
	result := paramize.Suggest(script, paramize.Options{EnableDataParams: true, EnableSecrets: true})
	selected := make(map[string]bool)
	for _, suggestion := range result.Suggestions {
		if suggestion.AutoApply && !suggestion.HighRisk {
			selected[suggestion.ID] = true
		}
	}
	if len(selected) == 0 {
		return
	}
	applied, _, err := paramize.ApplySuggestions(script, result.Suggestions, selected)
	if err != nil || applied == nil {
		return
	}
	*script = *applied
}

// createHTTPScriptFromHAR converts the recorded side-car HAR into a
// protocol-level HTTP script via the shared HAR importer.
func (s *Server) createHTTPScriptFromHAR(artifact *recbrowser.Session, info *BrowserRecordingSession, name string) (*dsl.Script, []string, error) {
	if len(artifact.HAR) == 0 {
		return nil, nil, fmt.Errorf("no side-car HAR is available to build an HTTP script")
	}
	httpName := strings.TrimSpace(name)
	if httpName == "" {
		httpName = info.Name
	}
	httpName += " (HTTP)"
	result, err := har.Convert(artifact.HAR, har.ConvertOptions{ScriptName: httpName})
	if err != nil {
		return nil, nil, err
	}
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(info.ProjectID)
		result.Script.GeneratedBy = "browser-recorder"
		result.Script.GeneratedFrom = "browser_recording_har"
		review, reviewErr := salesforce.Analyze(result.Script, salesforce.AnalyzeOptions{BuildNative: true, HARData: artifact.HAR})
		if reviewErr != nil {
			result.Warnings = append(result.Warnings, "Salesforce review failed: "+reviewErr.Error())
		} else if review != nil && review.Detected {
			preserved := ensureScriptImportPreserved(result.Script, "browser_recording_har")
			preserved["salesforce_review"] = salesforceReviewForResponse(review)
			if review.NativePreview != nil && review.NativePreview.Script != nil {
				result.Warnings = append(result.Warnings, "Salesforce traffic detected; the HAR Traffic importer auto-generates the native HTTP draft.")
			}
		}
		_ = attachMuleSoftReview(result.Script, "browser_recording_har", nil)
		_ = attachAgentforceReview(result.Script, "browser_recording_har", nil, false)
	}
	return result.Script, result.Warnings, nil
}

func (s *Server) handlePreviewBrowserRecordingConversion(w http.ResponseWriter, r *http.Request) {
	artifact, info, ok := s.browserRecording.Artifact(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "browser recording session not found")
		return
	}
	if artifact == nil {
		writeError(w, http.StatusConflict, "browser recording has no captured session yet")
		return
	}

	var previewReq browserRecordingPreviewRequest
	if !decodeOptionalJSONBody(w, r, &previewReq) {
		return
	}
	req := previewReq.convertRequest()

	result := s.convertBrowserRecording(artifact, info, req)
	warnings := append([]string(nil), result.Warnings...)
	scripts := make([]any, 0, len(result.Scripts)+1)
	for _, sc := range result.Scripts {
		if sc != nil {
			scripts = append(scripts, sc)
		}
	}
	var httpScript *dsl.Script
	if req.AlsoCreateHTTPScript {
		var httpWarnings []string
		var err error
		httpScript, httpWarnings, err = s.createHTTPScriptFromHAR(artifact, info, req.Name)
		if err != nil {
			warnings = append(warnings, "HTTP script generation skipped: "+err.Error())
		} else if httpScript != nil {
			scripts = append(scripts, httpScript)
			warnings = append(warnings, httpWarnings...)
		}
	}
	response := s.newScriptImportResponseWithSourceSecrets(r, scriptImportResponseOptions{
		Script:           result.Script,
		Scripts:          scripts,
		Warnings:         warnings,
		Stats:            result.Stats,
		Parameterization: buildImportParameterization(result.Script, nil),
	})
	if httpScript != nil {
		response["http_script"] = httpScript
	}
	writeScriptImportResponse(w, http.StatusOK, response)
}

func (s *Server) handleConvertBrowserRecording(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	artifact, info, ok := s.browserRecording.Artifact(id)
	if !ok {
		writeError(w, http.StatusNotFound, "browser recording session not found")
		return
	}
	if artifact == nil {
		writeError(w, http.StatusConflict, "browser recording has no captured session yet")
		return
	}
	if info.Status != BrowserRecordingStopped {
		writeError(w, http.StatusConflict, "stop the recording before saving the converted script")
		return
	}

	var req browserRecordingConvertRequest
	if !decodeOptionalJSONBody(w, r, &req) {
		return
	}
	if req.ExpectedScriptCount < 0 ||
		req.ExpectedScriptCount > maxBridgedBrowserImportScripts+1 {
		writeError(w, http.StatusBadRequest, "expected_script_count is outside the supported range")
		return
	}
	if err := validateBrowserRecordingParameterizationSelections(
		req.ParameterizationSelections,
		-1,
	); err != nil {
		writeError(w, http.StatusBadRequest, "browser recording parameterization selections are invalid: "+err.Error())
		return
	}

	result := s.convertBrowserRecording(artifact, info, req)

	// Store every generated script (one for the single-script path, several
	// when splitting at transactions).
	preparedScripts := make([]*dsl.Script, 0, len(result.Scripts))
	for scriptIndex, sc := range result.Scripts {
		if sc == nil {
			continue
		}
		parameterizedScript, err := applyBrowserRecordingParameterizationSelections(
			sc,
			scriptIndex,
			req.ParameterizationSelections,
		)
		if err != nil {
			writeError(w, http.StatusConflict, "browser recording parameterization selections are stale or invalid: "+err.Error())
			return
		}
		boundScript, err := applyBrowserRecordingSecretBindings(
			parameterizedScript,
			scriptIndex,
			req.SecretBindings,
		)
		if err != nil {
			writeError(w, http.StatusConflict, "browser recording secret bindings are stale or invalid: "+err.Error())
			return
		}
		result.Scripts[scriptIndex] = boundScript
		if scriptIndex == 0 {
			result.Script = boundScript
		}
		importedScript, err := preparePersistentImportedLibraryScript(
			boundScript,
			nil,
			"browser_recorder",
		)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "failed to prepare recorded credentials: "+err.Error())
			return
		}
		if err := validateConvertedScriptForCommit(importedScript); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "browser recording did not produce an executable script: "+err.Error())
			return
		}
		if strings.TrimSpace(importedScript.Version) == "" {
			importedScript.Version = "1"
		}
		preparedScripts = append(preparedScripts, importedScript)
	}
	if len(preparedScripts) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "conversion produced no scripts")
		return
	}

	warnings := append([]string(nil), result.Warnings...)
	var httpScript *dsl.Script
	var preparedHTTPScript *dsl.Script
	var httpWarnings []string
	if req.AlsoCreateHTTPScript {
		var err error
		httpScript, httpWarnings, err = s.createHTTPScriptFromHAR(artifact, info, req.Name)
		if err != nil {
			warnings = append(warnings, "HTTP script generation skipped: "+err.Error())
		} else if httpScript == nil {
			warnings = append(warnings, "HTTP script generation skipped: conversion produced no script")
		} else {
			httpScriptIndex := len(result.Scripts)
			httpScript, err = applyBrowserRecordingParameterizationSelections(
				httpScript,
				httpScriptIndex,
				req.ParameterizationSelections,
			)
			if err != nil {
				writeError(w, http.StatusConflict, "browser recording HTTP parameterization selections are stale or invalid: "+err.Error())
				return
			}
			httpScript, err = applyBrowserRecordingSecretBindings(
				httpScript,
				httpScriptIndex,
				req.SecretBindings,
			)
			if err != nil {
				writeError(w, http.StatusConflict, "browser recording HTTP secret bindings are stale or invalid: "+err.Error())
				return
			}
			importedHTTPScript, prepareErr := preparePersistentImportedLibraryScript(
				httpScript,
				nil,
				"browser_recording_har",
			)
			if prepareErr != nil {
				warnings = append(warnings, "HTTP script persistence skipped: failed to prepare recorded credentials: "+prepareErr.Error())
			} else if validationErr := validateConvertedScriptForCommit(importedHTTPScript); validationErr != nil {
				warnings = append(warnings, "HTTP script persistence skipped: "+validationErr.Error())
			} else {
				if strings.TrimSpace(importedHTTPScript.Version) == "" {
					importedHTTPScript.Version = "1"
				}
				preparedHTTPScript = importedHTTPScript
			}
		}
	}
	availableScriptCount := len(result.Scripts)
	if httpScript != nil {
		availableScriptCount++
	}
	if err := validateBrowserRecordingSecretBindings(req.SecretBindings, availableScriptCount); err != nil {
		writeError(w, http.StatusConflict, "browser recording secret bindings are stale or invalid: "+err.Error())
		return
	}
	if err := validateBrowserRecordingParameterizationSelections(
		req.ParameterizationSelections,
		availableScriptCount,
	); err != nil {
		writeError(w, http.StatusConflict, "browser recording parameterization selections are stale or invalid: "+err.Error())
		return
	}

	transactionScripts := append([]*dsl.Script(nil), preparedScripts...)
	if preparedHTTPScript != nil {
		transactionScripts = append(transactionScripts, preparedHTTPScript)
	}
	if req.ExpectedScriptCount > 0 &&
		len(transactionScripts) != req.ExpectedScriptCount {
		writeError(
			w,
			http.StatusConflict,
			fmt.Sprintf(
				"browser recording conversion changed from %d reviewed script(s) to %d persistable script(s); refresh the preview",
				req.ExpectedScriptCount,
				len(transactionScripts),
			),
		)
		return
	}
	projectID := normalizeProjectID(info.ProjectID)
	_, actorSubject, _ := s.resolveAuditActor(r)
	// One stopped recording is one durable conversion operation. The exact
	// scripts and save options are fenced by the keyed request HMAC below; do
	// not fold secret-binding names or other caller options into an
	// independently observable operation identity.
	operationID := strings.TrimSpace(actorSubject) + "\x00session:" + id
	requestDigest, err := s.directImportScriptsRequestDigest(transactionScripts, req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to fingerprint browser recording conversion: "+err.Error())
		return
	}
	attempt, receiptReplay, err := s.claimDirectImportAttempt(
		projectID,
		"browser_recorder",
		operationID,
		requestDigest,
	)
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrDirectImportReceiptBusy):
			w.Header().Set("Retry-After", "2")
			writeError(w, http.StatusConflict, "this browser recording conversion is already being saved")
		case errors.Is(err, storage.ErrDirectImportReceiptConflict):
			writeError(w, http.StatusConflict, "browser recording conversion changed after it was first saved")
		default:
			writeError(w, http.StatusInternalServerError, "failed to reserve browser recording conversion: "+err.Error())
		}
		return
	}
	assetIdentitySeed := directImportOperationHash(projectID, "browser_recorder", operationID)
	if attempt != nil && attempt.receipt != nil {
		assetIdentitySeed = attempt.receipt.ID
	}
	for index, script := range transactionScripts {
		script.ID = deterministicDirectImportAssetID(assetIdentitySeed, "script", index)
		script.RefKey = ""
	}

	writeSavedConversion := func(
		allStored []*StoredScript,
		recordCompletion bool,
	) {
		storedScripts, httpStored := splitBrowserRecordingStoredScripts(
			allStored,
			preparedHTTPScript != nil,
		)
		responseWarnings := append([]string(nil), warnings...)
		if httpStored != nil {
			responseWarnings = append(responseWarnings, httpWarnings...)
			responseWarnings = append(responseWarnings, fmt.Sprintf(
				"also created HTTP script %q (%s) from the recorded HAR",
				disclosureSafeScriptName(httpStored.Name, httpStored.Script),
				httpStored.ID,
			))
		}
		s.writeSavedBrowserRecordingConversion(
			w, r, id, result, httpScript, responseWarnings,
			storedScripts, httpStored, recordCompletion,
		)
	}
	if receiptReplay {
		stored, _, loadErr := s.loadDirectImportReceiptAssets(attempt)
		if loadErr != nil {
			writeError(w, http.StatusConflict, "completed browser recording receipt is inconsistent: "+loadErr.Error())
			return
		}
		writeSavedConversion(stored, false)
		return
	}
	if attempt != nil && !attempt.created {
		if recoverErr := s.recoverExpiredLibraryImportClaims(); recoverErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to recover prior browser recording transaction: "+recoverErr.Error())
			return
		}
		if renewErr := attempt.renew(); renewErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to renew browser recording receipt after recovery: "+renewErr.Error())
			return
		}
		stored, storedScenario, loadErr := s.loadDirectImportAssetsByIDs(
			projectID,
			deterministicScriptIDs(transactionScripts),
			"",
		)
		if loadErr == nil && storedScenario == nil && directImportPreparedAssetsMatch(
			s, stored, transactionScripts, nil, nil,
		) {
			if completeErr := attempt.complete(stored, nil); completeErr != nil {
				writeError(w, http.StatusInternalServerError, "failed to reconcile browser recording receipt: "+completeErr.Error())
				return
			}
			writeSavedConversion(stored, true)
			return
		}
	}

	transaction, err := newCreateImportTransaction(s, "browser-recording", transactionScripts, "", nil)
	if err != nil {
		if attempt != nil {
			if cleanupErr := attempt.rollback(nil); cleanupErr != nil {
				slog.Warn("failed to release browser recording receipt", "recording_session_id", id, "error", cleanupErr)
			}
		}
		writeError(w, http.StatusInternalServerError, "failed to prepare converted scripts: "+err.Error())
		return
	}
	finished := false
	assetsCommitted := false
	defer func() {
		if !finished && !assetsCommitted {
			if cleanupErr := attempt.rollback(transaction); cleanupErr != nil {
				slog.Warn("failed to fully compensate browser recording conversion", "recording_session_id", id, "error", cleanupErr)
			}
		}
	}()

	storedScripts := make([]*StoredScript, 0, len(preparedScripts))
	for _, importedScript := range preparedScripts {
		if err := attempt.renew(); err != nil {
			writeError(w, http.StatusConflict, "browser recording receipt ownership was lost; retry conversion")
			return
		}
		stored, err := transaction.createImportedScript(
			importedScript,
			"browser_recorder",
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save converted script: "+err.Error())
			return
		}
		storedScripts = append(storedScripts, stored)
	}
	var httpStored *StoredScript
	if preparedHTTPScript != nil {
		if err := attempt.renew(); err != nil {
			writeError(w, http.StatusConflict, "browser recording receipt ownership was lost; retry conversion")
			return
		}
		httpStored, err = transaction.createImportedScript(
			preparedHTTPScript,
			"browser_recording_har",
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save converted HTTP script: "+err.Error())
			return
		}
	}
	if err := attempt.renew(); err != nil {
		writeError(w, http.StatusConflict, "browser recording receipt ownership was lost; retry conversion")
		return
	}
	if err := transaction.commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit converted scripts: "+err.Error())
		return
	}
	assetsCommitted = true
	allStored := append([]*StoredScript(nil), storedScripts...)
	if httpStored != nil {
		allStored = append(allStored, httpStored)
	}
	if attempt != nil {
		if err := attempt.complete(allStored, nil); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to finalize browser recording receipt: "+err.Error())
			return
		}
	}
	finished = true
	writeSavedConversion(allStored, true)
}

func splitBrowserRecordingStoredScripts(
	all []*StoredScript,
	hasHTTP bool,
) ([]*StoredScript, *StoredScript) {
	if !hasHTTP || len(all) == 0 {
		return all, nil
	}
	return all[:len(all)-1], all[len(all)-1]
}

func (s *Server) writeSavedBrowserRecordingConversion(
	w http.ResponseWriter,
	r *http.Request,
	recordingID string,
	result *recbrowser.ConvertResult,
	httpScript *dsl.Script,
	warnings []string,
	storedScripts []*StoredScript,
	httpStored *StoredScript,
	recordCompletion bool,
) {
	if result == nil || len(storedScripts) == 0 || storedScripts[0] == nil {
		writeError(w, http.StatusInternalServerError, "browser recording receipt contains no primary script")
		return
	}
	primary := storedScripts[0]
	if recordCompletion {
		disclosedPrimaryName := disclosureSafeScriptName(primary.Name, primary.Script)
		s.browserRecording.NotifyConverted(recordingID, primary.ID, disclosedPrimaryName)
		s.recordAuditEvent(r, "browser_recording.convert", "success", "script", primary.ID, primary.ProjectID,
			map[string]any{
				"recording_session_id": recordingID,
				"name":                 disclosedPrimaryName,
				"actions":              result.Stats.ConvertedActions,
				"scripts":              len(storedScripts),
				"http_script":          httpStored != nil,
			})
	}

	sourceHTTPForResponse := httpScript
	if httpStored == nil {
		sourceHTTPForResponse = nil
	}
	response := s.newScriptImportResponseWithSourceSecrets(r, scriptImportResponseOptions{
		Script:               result.Script,
		Scripts:              appendBrowserRecordingScripts(result.Scripts, sourceHTTPForResponse),
		Warnings:             warnings,
		Stats:                result.Stats,
		Parameterization:     buildImportParameterization(result.Script, nil),
		SkipDraftAttestation: true,
	})
	response["script"] = maskStoredScript(primary)
	response["scripts"] = storedScriptsForResponse(storedScripts, httpStored)
	if httpStored != nil {
		response["http_script"] = maskStoredScript(httpStored)
	}
	writeScriptImportResponse(w, http.StatusCreated, response)
}

func appendBrowserRecordingScripts(scripts []*dsl.Script, httpScript *dsl.Script) []any {
	out := make([]any, 0, len(scripts)+1)
	for _, script := range scripts {
		if script != nil {
			out = append(out, script)
		}
	}
	if httpScript != nil {
		out = append(out, httpScript)
	}
	return out
}

func storedScriptsForResponse(scripts []*StoredScript, httpScript *StoredScript) []any {
	out := make([]any, 0, len(scripts)+1)
	for _, sc := range scripts {
		out = append(out, maskStoredScript(sc))
	}
	if httpScript != nil {
		out = append(out, maskStoredScript(httpScript))
	}
	return out
}

func (s *Server) handleSetBrowserRecordingSection(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Section string `json:"section"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	session, ok, err := s.browserRecording.SetSection(r.PathValue("id"), req.Section)
	s.writeBrowserRecordingControlResult(w, session, ok, err)
}

func (s *Server) handleSetBrowserRecordingPickMode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		On bool `json:"on"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	session, ok, err := s.browserRecording.SetAssertMode(r.PathValue("id"), req.On)
	s.writeBrowserRecordingControlResult(w, session, ok, err)
}

func (s *Server) handleDeleteLastBrowserRecordingStep(w http.ResponseWriter, r *http.Request) {
	session, ok, err := s.browserRecording.DeleteLastStep(r.PathValue("id"))
	s.writeBrowserRecordingControlResult(w, session, ok, err)
}

func (s *Server) handleUpdateBrowserRecordingStep(w http.ResponseWriter, r *http.Request) {
	seq, err := strconv.Atoi(r.PathValue("seq"))
	if err != nil || seq <= 0 {
		writeError(w, http.StatusBadRequest, "invalid step sequence")
		return
	}
	var req struct {
		Excluded        *bool   `json:"excluded,omitempty"`
		Secret          *bool   `json:"mark_secret,omitempty"`
		Param           *bool   `json:"mark_param,omitempty"`
		PromoteSelector *string `json:"promote_selector,omitempty"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	id := r.PathValue("id")
	if req.Excluded != nil {
		session, ok, err := s.browserRecording.SetStepExcluded(id, seq, *req.Excluded)
		s.writeBrowserRecordingControlResult(w, session, ok, err)
		return
	}
	if req.Secret != nil || req.Param != nil {
		session, ok, err := s.browserRecording.PatchStepMark(id, seq, req.Secret, req.Param)
		s.writeBrowserRecordingControlResult(w, session, ok, err)
		return
	}
	if req.PromoteSelector != nil {
		selector := strings.TrimSpace(*req.PromoteSelector)
		if selector == "" {
			writeError(w, http.StatusBadRequest, "promote_selector cannot be empty")
			return
		}
		session, ok, err := s.browserRecording.PromoteStepSelector(id, seq, selector)
		s.writeBrowserRecordingControlResult(w, session, ok, err)
		return
	}
	writeError(w, http.StatusBadRequest, "no step update provided (excluded, mark_secret, mark_param, or promote_selector)")
}

func (s *Server) handleSuggestBrowserRecordingTransactions(w http.ResponseWriter, r *http.Request) {
	suggestions, ok := s.browserRecording.SuggestTransactions(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "browser recording session not found")
		return
	}
	if suggestions == nil {
		suggestions = []recbrowser.TransactionSuggestion{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"suggestions": suggestions})
}

// handleListBrowserRecorderDevices returns the device presets available for
// emulated recording.
func (s *Server) handleListBrowserRecorderDevices(w http.ResponseWriter, r *http.Request) {
	devices := browserprotocol.ListDevices()
	out := make([]map[string]any, 0, len(devices))
	for _, d := range devices {
		out = append(out, map[string]any{
			"name":                d.Name,
			"platform":            d.Platform,
			"width":               d.Width,
			"height":              d.Height,
			"device_scale_factor": d.DeviceScaleFactor,
			"mobile":              d.Mobile,
			"has_touch":           d.HasTouch,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": out})
}

// handleExportBrowserRecordingStorageState writes the recording's exported
// Playwright storage state to a confined export path so it can be reused to
// skip login on a later recording.
func (s *Server) handleExportBrowserRecordingStorageState(w http.ResponseWriter, r *http.Request) {
	artifact, info, ok := s.browserRecording.Artifact(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "browser recording session not found")
		return
	}
	if artifact == nil || len(artifact.StorageState) == 0 {
		writeError(w, http.StatusConflict, "no storage state is available (the recording is still running or captured none)")
		return
	}
	data, err := scrubBrowserStorageState(artifact.StorageState)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to scrub storage state: "+err.Error())
		return
	}
	relativePath, stateFile := browserStorageStateExportLocation(info)
	// Persist a confined copy under the export dir so it can be supplied as a
	// storage_state_path on a later recording. This copy MUST be the RAW storage
	// state: it is read only server-side to seed a Playwright context (login
	// reuse), never served to a client — seeding a context with the scrubbed
	// [REDACTED] cookie values silently breaks session restore. The client
	// download below still receives the scrubbed state. Best effort; failures
	// still allow the caller to download the scrubbed state.
	pruneBrowserStorageStateExports(time.Now())
	if confined, err := resolveConfinedExportPath(exportBaseDir(), filepath.Dir(relativePath), filepath.Base(relativePath)); err == nil {
		if mkErr := os.MkdirAll(filepath.Dir(confined), 0o755); mkErr == nil {
			if writeErr := os.WriteFile(confined, artifact.StorageState, 0o600); writeErr == nil {
				w.Header().Set("X-APEXLoad-StorageState-Path", relativePath)
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, stateFile))
	w.Header().Set("X-APEXLoad-Sensitivity", scrub.SensitivityScrubbed)
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

type browserRecordingVerifyRequest struct {
	ReplaySessionID string `json:"replay_session_id,omitempty"`
}

type browserRecordingVerifyResponse struct {
	ReplaySessionID string                `json:"replay_session_id"`
	ReplaySession   *BrowserReplaySession `json:"replay_session"`
	Warnings        []string              `json:"warnings,omitempty"`
}

// handleVerifyBrowserRecording converts the stopped recording in-memory and
// replays it immediately through the browser-replay engine so the user can see
// per-step pass/fail and self-heal suggestions without saving anything.
// OpenAPI-Responses: 409, 500, 503
func (s *Server) handleVerifyBrowserRecording(w http.ResponseWriter, r *http.Request) {
	if !s.requireExecutionLeader(w) {
		return
	}
	var req browserRecordingVerifyRequest
	if !decodeOptionalJSONBody(w, r, &req) {
		return
	}
	if err := validateExternalReplaySessionID(req.ReplaySessionID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id := r.PathValue("id")
	artifact, info, ok := s.browserRecording.Artifact(id)
	if !ok {
		writeError(w, http.StatusNotFound, "browser recording session not found")
		return
	}
	if artifact == nil {
		writeError(w, http.StatusConflict, "browser recording has no captured session yet")
		return
	}
	if info.Status != BrowserRecordingStopped {
		writeError(w, http.StatusConflict, "stop the recording before verifying it")
		return
	}
	result := s.convertBrowserRecording(artifact, info, browserRecordingConvertRequest{})
	script := result.Script
	if script == nil || len(script.Actions)+len(script.Init)+len(script.Teardown) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "the recording produced no replayable actions")
		return
	}
	if script.ID == "" {
		script.ID = uuid.New().String()
	}
	ignoreThink := true
	executionCtx, releaseExecutionCtx := s.runner.executionOperationContext(context.Background())
	replay, replayErr := s.browserReplay.Start(script.ID, script, BrowserReplayStartRequest{
		GroupName:               script.Name + " — verify",
		ReplaySessionID:         req.ReplaySessionID,
		IgnoreThinkTime:         &ignoreThink,
		executionContext:        executionCtx,
		releaseExecutionContext: releaseExecutionCtx,
	})
	if replayErr != nil {
		releaseExecutionCtx()
		writeBrowserReplayStartError(w, replayErr)
		return
	}
	s.recordAuditEvent(r, "browser_recording.verify", "success", "browser_recording", id, info.ProjectID,
		map[string]any{"replay_session_id": replay.ID, "actions": result.Stats.ConvertedActions})
	response := browserRecordingVerifyResponse{
		ReplaySessionID: replay.ID,
		ReplaySession:   replay,
		Warnings:        result.Warnings,
	}
	writeJSON(w, http.StatusCreated, response)
}

// handleBrowserRecorderInstall provisions the Playwright driver/Chromium in the
// background so a controller without browsers can become recording-capable.
func (s *Server) handleBrowserRecorderInstall(w http.ResponseWriter, r *http.Request) {
	availability := s.browserRecordingAvailability()
	if !availability.Available {
		switch availability.Code {
		case browserRecordingUnavailableDisabled, browserRecordingUnavailableProductionDisabled:
			writeJSON(w, http.StatusForbidden, map[string]any{
				"error":        availability.Reason,
				"availability": availability,
			})
			return
		}
	}
	state := s.browserInstall.start(func() error { return recbrowser.EnsureBrowsers() })
	s.recordAuditEvent(r, "browser_recording.install", "pending", "browser_recording", "", "", map[string]any{"status": state.Status})
	writeJSON(w, http.StatusAccepted, map[string]any{"install": state})
}

func (s *Server) handleGetBrowserRecorderInstall(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"install": s.browserInstall.snapshot()})
}

func (s *Server) handleExportBrowserRecording(w http.ResponseWriter, r *http.Request) {
	extendArtifactWriteDeadline(w)
	artifact, info, ok := s.browserRecording.Artifact(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "browser recording session not found")
		return
	}
	format := strings.ToLower(r.URL.Query().Get("format"))
	if format == "" {
		format = "har"
	}
	if format != "har" {
		writeError(w, http.StatusBadRequest, "unsupported browser recording export format: "+format)
		return
	}
	if artifact == nil || len(artifact.HAR) == 0 {
		writeError(w, http.StatusConflict, "no side-car HAR is available for this recording (HAR capture disabled or recording still running)")
		return
	}

	data := artifact.HAR
	raw := rawRecordingExportRequested(r)
	if raw && !s.canExportRawRecording(r, info.ProjectID) {
		s.recordAuditEvent(r, "browser_recording.export_har_raw", "denied", "browser_recording", info.ID, info.ProjectID, map[string]any{"reason": "insufficient_role"})
		writeError(w, http.StatusForbidden, "raw browser recording export requires editor, project_admin, or system_admin access")
		return
	}

	sensitivity := scrub.SensitivityRaw
	if !raw {
		scrubbed, err := scrub.HAR(data, scrub.Policy{})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to scrub HAR: "+err.Error())
			return
		}
		data = scrubbed
		sensitivity = scrub.SensitivityScrubbed
	}
	s.recordAuditEvent(r, "browser_recording.export_har", "success", "browser_recording", info.ID, info.ProjectID, map[string]any{"sensitivity": sensitivity, "raw": raw})

	filename := sanitizeDownloadName(info.Name)
	if filename == "" {
		filename = "browser-recording"
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.har"`, filename))
	w.Header().Set("X-APEXLoad-Sensitivity", sensitivity)
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

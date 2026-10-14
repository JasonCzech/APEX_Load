package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/convert/har"
	"github.com/jasonczech/apexload/pkg/convert/paramize"
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

// browserStorageStateFileSuffix ends every saved login state's file name.
const browserStorageStateFileSuffix = "-state.json"

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
	stateFile = filename + browserStorageStateFileSuffix
	relativePath = filepath.ToSlash(filepath.Join(browserStorageStateDir, projectDir, stateFile))
	return relativePath, stateFile
}

// browserStorageStateSummary is a login state saved from one of a project's
// browser recordings. Path is what a recording start takes as
// storage_state_path; the file itself is never served.
type browserStorageStateSummary struct {
	Path        string     `json:"path"`
	Name        string     `json:"name"`
	RecordingID string     `json:"recording_id,omitempty"`
	SavedAt     time.Time  `json:"saved_at"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

// browserStorageStateIDSuffix is the recording ID a saved state's file name
// ends with; a state whose recording is gone is named without it.
var browserStorageStateIDSuffix = regexp.MustCompile(`-[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// handleListBrowserRecordingStorageStates lists the login states saved from
// the project's browser recordings, newest first, so a recording can start
// signed in. Expired states are pruned first, as a save does.
func (s *Server) handleListBrowserRecordingStorageStates(w http.ResponseWriter, r *http.Request) {
	projectID := authorizedProjectOr(r, strings.TrimSpace(r.URL.Query().Get("project_id")))
	now := time.Now()
	pruneBrowserStorageStateExports(now)
	projectDir := browserStorageStateProjectDir(projectID)
	states := []browserStorageStateSummary{}
	// The directory a save writes into (handleExportBrowserRecordingStorageState).
	probe, err := resolveConfinedExportPath(exportBaseDir(), filepath.Join(browserStorageStateDir, projectDir), "probe.json")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	entries, err := os.ReadDir(filepath.Dir(probe))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusInternalServerError, "list saved login states: "+err.Error())
		return
	}
	// Name each state by the recording it came from while that recording is kept.
	recordings := map[string]*BrowserRecordingSession{}
	for _, session := range s.browserRecording.List(projectID) {
		if session != nil {
			recordings[sanitizeDownloadName(session.ID)] = session
		}
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), browserStorageStateFileSuffix) {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			continue
		}
		stem := strings.TrimSuffix(entry.Name(), browserStorageStateFileSuffix)
		state := browserStorageStateSummary{Path: projectDir + "/" + entry.Name(), Name: stem, SavedAt: info.ModTime().UTC()}
		if name := browserStorageStateIDSuffix.ReplaceAllString(stem, ""); name != "" {
			state.Name = name
		}
		if defaultBrowserRecordingRetention > 0 {
			expires := info.ModTime().Add(defaultBrowserRecordingRetention).UTC()
			state.ExpiresAt = &expires
		}
		for id, session := range recordings {
			if id != "" && strings.HasSuffix(stem, "-"+id) {
				state.RecordingID = session.ID
				if name := strings.TrimSpace(session.Name); name != "" {
					state.Name = name
				}
				break
			}
		}
		states = append(states, state)
	}
	sort.SliceStable(states, func(i, j int) bool { return states[i].SavedAt.After(states[j].SavedAt) })
	writeJSON(w, http.StatusOK, map[string]any{"states": states, "count": len(states)})
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
	if !validBrowserRecordingQUICMode(req.QUICMode) {
		writeError(w, http.StatusBadRequest, "quic_mode must be auto or off")
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
			ref := logServerError(w, http.StatusServiceUnavailable, err, "code", availability.Code)
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"error":        availability.Reason,
				"availability": availability,
				"ref":          ref,
			})
			return
		}
		if reason, ok := browserRecordingNavigationFailure(err, req.StartURL); ok {
			writeError(w, http.StatusUnprocessableEntity, reason)
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
	projectID := authorizedProjectOr(r, strings.TrimSpace(r.URL.Query().Get("project_id")))
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
	if !decodeOptionalJSONBody(w, r, &req) {
		return
	}

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
	// FolderID is save-only. Every script produced by this one atomic
	// conversion is validated against the recording project and placed in the
	// same scripts-category destination.
	FolderID *string `json:"folder_id,omitempty"`
	// StartSeq/EndSeq trim conversion to an inclusive step window (0 = open).
	StartSeq int `json:"start_seq,omitempty"`
	EndSeq   int `json:"end_seq,omitempty"`
	// SplitAtTransactions emits one script per top-level transaction.
	SplitAtTransactions bool `json:"split_at_transactions,omitempty"`
	// AlsoCreateHTTPScript additionally generates a protocol-level HTTP script
	// from the recorded HAR (dual-script generation).
	AlsoCreateHTTPScript bool `json:"also_create_http_script,omitempty"`
	// HTTPScriptLevel is "url" (default) or "html": an HTML-level HTTP script
	// turns recorded link, image and form clicks into HTML-mode steps.
	HTTPScriptLevel string `json:"http_script_level,omitempty" enums:"url,html"`
	// AsyncScan (default on) turns requests a page repeated in the
	// background into background conversations in the HTTP script; false
	// keeps them as recorded steps.
	AsyncScan *bool `json:"async_scan,omitempty"`
	// RecordingProfileID names a project recording profile the HTTP script
	// applies (filters, headers, think-time threshold, correlation settings,
	// and the options above when left unset).
	RecordingProfileID string `json:"recording_profile_id,omitempty"`
	// HTTPVersionMode maps the HTTP script's captured versions to leaf
	// policies: upgrade (default; captured h3 becomes auto+h3), exact (3) or
	// inherit (none).
	HTTPVersionMode string `json:"http_version_mode,omitempty" enums:"upgrade,exact,inherit"`
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
	HTTPScriptLevel      string `json:"http_script_level,omitempty" enums:"url,html"`
	AsyncScan            *bool  `json:"async_scan,omitempty"`
	RecordingProfileID   string `json:"recording_profile_id,omitempty"`
	HTTPVersionMode      string `json:"http_version_mode,omitempty" enums:"upgrade,exact,inherit"`
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
		HTTPScriptLevel:      req.HTTPScriptLevel,
		AsyncScan:            req.AsyncScan,
		RecordingProfileID:   req.RecordingProfileID,
		HTTPVersionMode:      req.HTTPVersionMode,
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

const (
	browserRecordingSaveVersion    = 1
	browserRecordingSaveVersionKey = "browser_recorder_save_version"
	// "session_id" is intentionally avoided in the durable key name: generic
	// metadata disclosure sanitization treats that spelling as a credential.
	// This value is a non-secret UUID receipt correlation reference.
	browserRecordingSaveSessionIDKey     = "browser_recorder_save_ref"
	browserRecordingSaveIndexKey         = "browser_recorder_save_index"
	browserRecordingSaveCountKey         = "browser_recorder_save_count"
	browserRecordingSaveActorHashKey     = "browser_recorder_save_actor_hash"
	browserRecordingSaveRoleKey          = "browser_recorder_save_role"
	browserRecordingSaveProofKey         = "browser_recorder_save_proof"
	browserRecordingSaveRoleBrowser      = "browser"
	browserRecordingSaveRoleHTTP         = "http"
	browserRecordingHARBaselineNeededKey = "browser_recorder_har_baseline_required"
	browserRecordingSaveActorDomain      = "apexload:browser-recorder-save-actor:v1"
	browserRecordingSaveProofDomain      = "apexload:browser-recorder-save-proof:v1"
	browserRecordingSaveLocatorDomain    = "apexload:browser-recorder-save-locator:v1"
	browserRecordingSaveLocatorKeyPrefix = "import.browser_recorder.locator.v1."
	browserRecordingSaveLocatorStaged    = "staged"
	browserRecordingSaveLocatorPublished = "published"
)
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
	if resolvedProjectID, resolved := mutationProjectFromRequest(r); resolved {
		// Global administrators and Dev Bypass are intentionally not bound to an
		// authorized-project context. This mutating route still has a trusted
		// resolver result, though, including its Default Project fallback when
		// every optional project hint is omitted.
		projectID = resolvedProjectID
	} else if projectID == "" {
		// Handler-only tests and storage-free embeddings do not install the
		// mutation coordinator. Preserve this route's documented resolver
		// default there as well; unlike collection reads, an imported script
		// must always belong to a concrete project.
		projectID = normalizeProjectID(projectHint)
	}
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
		// Handoff proves review/save policy, not an independently observed browser
		// launch on this controller. Discard malformed/unknown claim fields.
		if script.Metadata.Import.Preserved != nil {
			claim := recbrowser.NormalizeRecordingEnvironment(script.Metadata.Import.Preserved[recbrowser.RecordingEnvironmentKey], recbrowser.RecordingObservationAgent)
			recbrowser.AttachRecordingEnvironment(script, claim)
		}
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
	recbrowser.AttachRecordingEnvironment(script, artifact.RecordingEnvironment)
	if artifact.DeviceProfile != "" {
		script.Metadata.Import.Preserved["device_profile"] = artifact.DeviceProfile
	}
	if len(artifact.Annotations) > 0 {
		script.Metadata.Import.Preserved["recorded_annotations"] = len(artifact.Annotations)
	}
}

func stampBrowserRecordingSaveProvenance(
	script *dsl.Script,
	recordingID string,
	index int,
	count int,
	actorHash string,
	role string,
	baselineRequired bool,
) {
	if script == nil {
		return
	}
	if script.Metadata == nil {
		script.Metadata = &dsl.ScriptMetadata{}
	}
	if script.Metadata.Import == nil {
		script.Metadata.Import = &dsl.ImportMetadata{}
	}
	if script.Metadata.Import.Preserved == nil {
		script.Metadata.Import.Preserved = make(map[string]any)
	}
	preserved := script.Metadata.Import.Preserved
	preserved[browserRecordingSaveVersionKey] = browserRecordingSaveVersion
	preserved[browserRecordingSaveSessionIDKey] = strings.TrimSpace(recordingID)
	preserved[browserRecordingSaveIndexKey] = index
	preserved[browserRecordingSaveCountKey] = count
	preserved[browserRecordingSaveActorHashKey] = strings.TrimSpace(actorHash)
	preserved[browserRecordingSaveRoleKey] = strings.TrimSpace(role)
	preserved[browserRecordingHARBaselineNeededKey] = baselineRequired
}

func (s *Server) browserRecordingSaveActorFingerprint(recordingID, actorSubject string) (string, error) {
	payload := struct {
		RecordingID string `json:"recording_id"`
		Actor       string `json:"actor"`
	}{
		RecordingID: strings.TrimSpace(recordingID),
		Actor:       strings.TrimSpace(actorSubject),
	}
	key, err := s.directImportDigestKey()
	if err != nil {
		return "", err
	}
	if len(key) == 0 {
		return hashCanonical(payload), nil
	}
	return directImportHMAC(key, browserRecordingSaveActorDomain, payload)
}

type browserRecordingSaveProvenanceProof struct {
	Version          int    `json:"version"`
	RecordingID      string `json:"recording_id"`
	ScriptID         string `json:"script_id"`
	ProjectID        string `json:"project_id"`
	Index            int    `json:"index"`
	Count            int    `json:"count"`
	ActorHash        string `json:"actor_hash"`
	Role             string `json:"role"`
	BaselineRequired bool   `json:"baseline_required"`
}

func browserRecordingSaveProvenanceProofPayload(script *dsl.Script) (browserRecordingSaveProvenanceProof, error) {
	preserved := browserRecordingImportPreserved(script)
	if script == nil || preserved == nil {
		return browserRecordingSaveProvenanceProof{}, errors.New("browser recording save provenance is missing")
	}
	baselineRequired, _ := preserved[browserRecordingHARBaselineNeededKey].(bool)
	payload := browserRecordingSaveProvenanceProof{
		Version:          browserRecordingPreservedInt(preserved, browserRecordingSaveVersionKey),
		RecordingID:      browserRecordingPreservedString(preserved, browserRecordingSaveSessionIDKey),
		ScriptID:         strings.TrimSpace(script.ID),
		ProjectID:        normalizeProjectID(script.ProjectID),
		Index:            browserRecordingPreservedInt(preserved, browserRecordingSaveIndexKey),
		Count:            browserRecordingPreservedInt(preserved, browserRecordingSaveCountKey),
		ActorHash:        browserRecordingPreservedString(preserved, browserRecordingSaveActorHashKey),
		Role:             browserRecordingPreservedString(preserved, browserRecordingSaveRoleKey),
		BaselineRequired: baselineRequired,
	}
	if payload.Version != browserRecordingSaveVersion || payload.RecordingID == "" || payload.ScriptID == "" ||
		payload.ActorHash == "" || payload.Index < 0 || payload.Count <= 0 {
		return browserRecordingSaveProvenanceProof{}, errors.New("browser recording save provenance is incomplete")
	}
	return payload, nil
}

func (s *Server) browserRecordingSaveProvenanceProof(script *dsl.Script) (string, error) {
	payload, err := browserRecordingSaveProvenanceProofPayload(script)
	if err != nil {
		return "", err
	}
	key, err := s.directImportDigestKey()
	if err != nil {
		return "", err
	}
	if len(key) == 0 {
		return hashCanonical(payload), nil
	}
	return directImportHMAC(key, browserRecordingSaveProofDomain, payload)
}

func (s *Server) verifyBrowserRecordingSaveProvenance(script *dsl.Script) error {
	preserved := browserRecordingImportPreserved(script)
	actual := browserRecordingPreservedString(preserved, browserRecordingSaveProofKey)
	if actual == "" {
		return errors.New("server proof is missing")
	}
	expected, err := s.browserRecordingSaveProvenanceProof(script)
	if err != nil {
		return err
	}
	if actual != expected {
		return errors.New("server proof does not match the saved script")
	}
	return nil
}

type browserRecordingSaveLocator struct {
	Version       int      `json:"version"`
	RecordingID   string   `json:"recording_id"`
	ProjectID     string   `json:"project_id"`
	ScriptIDs     []string `json:"script_ids"`
	ReceiptID     string   `json:"receipt_id,omitempty"`
	OperationHash string   `json:"operation_hash,omitempty"`
	RequestDigest string   `json:"request_digest,omitempty"`
	// State makes the locator a two-phase publication marker. The staged value
	// is durable before the claim commit; published is CAS-promoted only after
	// every script becomes visible. Omitempty keeps previously written v1
	// locators verifiable so compensated entries can still be cleared without
	// treating an unsigned publication state as durable evidence.
	State string `json:"state,omitempty"`
	Proof string `json:"proof"`
}

func (s *Server) browserRecordingSaveLocatorKey(recordingID string) (string, error) {
	key, err := s.directImportDigestKey()
	if err != nil {
		return "", err
	}
	recordingID = strings.TrimSpace(recordingID)
	if recordingID == "" {
		return "", errors.New("browser recording locator requires a recording id")
	}
	if len(key) == 0 {
		return browserRecordingSaveLocatorKeyPrefix + directImportSHA256(recordingID), nil
	}
	hash, err := directImportHMAC(key, browserRecordingSaveLocatorDomain+":key", recordingID)
	if err != nil {
		return "", err
	}
	return browserRecordingSaveLocatorKeyPrefix + strings.TrimPrefix(hash, "hmac-sha256:"), nil
}

func (s *Server) browserRecordingSaveLocatorProof(locator browserRecordingSaveLocator) (string, error) {
	locator.Proof = ""
	key, err := s.directImportDigestKey()
	if err != nil {
		return "", err
	}
	if len(key) == 0 {
		return hashCanonical(locator), nil
	}
	return directImportHMAC(key, browserRecordingSaveLocatorDomain+":proof", locator)
}

func (s *Server) encodeBrowserRecordingSaveLocator(
	recordingID string,
	projectID string,
	scriptIDs []string,
	attempts ...*directImportAttempt,
) (string, error) {
	return s.encodeBrowserRecordingSaveLocatorState(
		recordingID,
		projectID,
		scriptIDs,
		browserRecordingSaveLocatorStaged,
		attempts...,
	)
}

func (s *Server) encodeBrowserRecordingSaveLocatorState(
	recordingID string,
	projectID string,
	scriptIDs []string,
	state string,
	attempts ...*directImportAttempt,
) (string, error) {
	locator := browserRecordingSaveLocator{
		Version:     browserRecordingSaveVersion,
		RecordingID: strings.TrimSpace(recordingID),
		ProjectID:   normalizeProjectID(projectID),
		ScriptIDs:   append([]string(nil), scriptIDs...),
		State:       strings.TrimSpace(state),
	}
	if len(attempts) > 0 && attempts[0] != nil && attempts[0].receipt != nil {
		locator.ReceiptID = strings.TrimSpace(attempts[0].receipt.ID)
		locator.OperationHash = strings.TrimSpace(attempts[0].receipt.OperationKeyHash)
		locator.RequestDigest = strings.TrimSpace(attempts[0].receipt.RequestSHA256)
	}
	return s.encodeBrowserRecordingSaveLocatorValue(locator)
}

func (s *Server) encodeBrowserRecordingSaveLocatorValue(locator browserRecordingSaveLocator) (string, error) {
	if locator.RecordingID == "" || len(locator.ScriptIDs) == 0 ||
		len(locator.ScriptIDs) > maxBridgedBrowserImportScripts+1 {
		return "", errors.New("browser recording locator is incomplete")
	}
	if locator.State != browserRecordingSaveLocatorStaged &&
		locator.State != browserRecordingSaveLocatorPublished {
		return "", errors.New("browser recording locator has an invalid publication state")
	}
	receiptFields := 0
	for _, value := range []string{locator.ReceiptID, locator.OperationHash, locator.RequestDigest} {
		if strings.TrimSpace(value) != "" {
			receiptFields++
		}
	}
	if receiptFields != 0 && receiptFields != 3 {
		return "", errors.New("browser recording locator has incomplete receipt evidence")
	}
	seen := make(map[string]struct{}, len(locator.ScriptIDs))
	for _, scriptID := range locator.ScriptIDs {
		scriptID = strings.TrimSpace(scriptID)
		if scriptID == "" {
			return "", errors.New("browser recording locator contains an empty script id")
		}
		if _, duplicate := seen[scriptID]; duplicate {
			return "", errors.New("browser recording locator contains duplicate script ids")
		}
		seen[scriptID] = struct{}{}
	}
	proof, err := s.browserRecordingSaveLocatorProof(locator)
	if err != nil {
		return "", err
	}
	locator.Proof = proof
	payload, err := json.Marshal(locator)
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func (s *Server) decodeBrowserRecordingSaveLocator(raw string) (*browserRecordingSaveLocator, error) {
	var locator browserRecordingSaveLocator
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&locator); err != nil {
		return nil, err
	}
	if locator.Version != browserRecordingSaveVersion || locator.RecordingID == "" || locator.ProjectID == "" ||
		len(locator.ScriptIDs) == 0 || len(locator.ScriptIDs) > maxBridgedBrowserImportScripts+1 || locator.Proof == "" {
		return nil, errors.New("browser recording locator is incomplete")
	}
	expected, err := s.browserRecordingSaveLocatorProof(locator)
	if err != nil {
		return nil, err
	}
	if expected != locator.Proof {
		return nil, errors.New("browser recording locator proof does not match")
	}
	if locator.State != "" && locator.State != browserRecordingSaveLocatorStaged &&
		locator.State != browserRecordingSaveLocatorPublished {
		return nil, errors.New("browser recording locator has an invalid publication state")
	}
	receiptFields := 0
	for _, value := range []string{locator.ReceiptID, locator.OperationHash, locator.RequestDigest} {
		if strings.TrimSpace(value) != "" {
			receiptFields++
		}
	}
	if receiptFields != 0 && receiptFields != 3 {
		return nil, errors.New("browser recording locator has incomplete receipt evidence")
	}
	seen := make(map[string]struct{}, len(locator.ScriptIDs))
	for _, scriptID := range locator.ScriptIDs {
		if strings.TrimSpace(scriptID) == "" {
			return nil, errors.New("browser recording locator contains an empty script id")
		}
		if _, duplicate := seen[scriptID]; duplicate {
			return nil, errors.New("browser recording locator contains duplicate script ids")
		}
		seen[scriptID] = struct{}{}
	}
	return &locator, nil
}

func (s *Server) persistBrowserRecordingSaveLocator(
	recordingID string,
	projectID string,
	allStored []*StoredScript,
	attempts ...*directImportAttempt,
) error {
	if s == nil || s.store == nil {
		return nil
	}
	scriptIDs := make([]string, 0, len(allStored))
	for _, stored := range allStored {
		if stored == nil || strings.TrimSpace(stored.ID) == "" {
			return errors.New("browser recording locator contains an empty stored script")
		}
		scriptIDs = append(scriptIDs, stored.ID)
	}
	locatorKey, err := s.browserRecordingSaveLocatorKey(recordingID)
	if err != nil {
		return err
	}
	encoded, err := s.encodeBrowserRecordingSaveLocator(recordingID, projectID, scriptIDs, attempts...)
	if err != nil {
		return err
	}
	published, err := s.encodeBrowserRecordingSaveLocatorState(
		recordingID, projectID, scriptIDs, browserRecordingSaveLocatorPublished, attempts...,
	)
	if err != nil {
		return err
	}
	cas, ok := s.store.(settingsCompareAndSwapper)
	if !ok {
		return errors.New("durable browser recording locator requires atomic settings storage")
	}
	existing, err := s.store.GetSetting(locatorKey)
	if err != nil {
		return err
	}
	if existing == encoded || existing == published {
		return nil
	}
	if strings.TrimSpace(existing) != "" {
		return errors.New("browser recording locator conflicts with an existing save")
	}
	won, err := cas.CompareAndSwapSetting(locatorKey, "", encoded)
	if err != nil {
		return err
	}
	if !won {
		existing, err = s.store.GetSetting(locatorKey)
		if err != nil {
			return err
		}
		if existing != encoded && existing != published {
			return errors.New("browser recording locator changed concurrently")
		}
	}
	return nil
}

// publishBrowserRecordingSaveLocator closes the crash window between the
// locator write and the direct-import claim commit. A concurrent stale-locator
// cleanup can only CAS the staged encoding, while this method CAS-promotes it to
// a distinct signed published encoding after the scripts become visible.
func (s *Server) publishBrowserRecordingSaveLocator(
	recordingID string,
	projectID string,
	allStored []*StoredScript,
	attempts ...*directImportAttempt,
) error {
	if s == nil || s.store == nil {
		return nil
	}
	scriptIDs := make([]string, 0, len(allStored))
	for _, stored := range allStored {
		if stored == nil || strings.TrimSpace(stored.ID) == "" {
			return errors.New("browser recording locator contains an empty stored script")
		}
		scriptIDs = append(scriptIDs, stored.ID)
	}
	locatorKey, err := s.browserRecordingSaveLocatorKey(recordingID)
	if err != nil {
		return err
	}
	staged, err := s.encodeBrowserRecordingSaveLocatorState(
		recordingID, projectID, scriptIDs, browserRecordingSaveLocatorStaged, attempts...,
	)
	if err != nil {
		return err
	}
	published, err := s.encodeBrowserRecordingSaveLocatorState(
		recordingID, projectID, scriptIDs, browserRecordingSaveLocatorPublished, attempts...,
	)
	if err != nil {
		return err
	}
	return s.publishBrowserRecordingSaveLocatorEncoding(locatorKey, staged, published)
}

func (s *Server) publishBrowserRecordingSaveLocatorEncoding(locatorKey, staged, published string) error {
	cas, ok := s.store.(settingsCompareAndSwapper)
	if !ok {
		return errors.New("durable browser recording locator requires atomic settings storage")
	}
	current, err := s.store.GetSetting(locatorKey)
	if err != nil {
		return err
	}
	if current == published {
		return nil
	}
	if current != staged {
		return errors.New("browser recording locator changed before publication")
	}
	won, err := cas.CompareAndSwapSetting(locatorKey, staged, published)
	if err != nil {
		return err
	}
	if won {
		return nil
	}
	current, err = s.store.GetSetting(locatorKey)
	if err != nil {
		return err
	}
	if current != published {
		return errors.New("browser recording locator changed concurrently during publication")
	}
	return nil
}

// recoverStagedBrowserRecordingConversionsAtStartup adopts only a complete,
// authenticated save that reached the final pre-commit crash window: every
// claimed script, required HAR baseline, signed locator, and receipt identity
// must agree exactly. Incomplete or invalid groups are left for ordinary claim
// compensation. A storage error is returned so construction can preserve the
// hidden rows rather than destroy a potentially recoverable stopped recording.
func (s *Server) recoverStagedBrowserRecordingConversionsAtStartup() error {
	if s == nil || s.store == nil {
		return nil
	}
	recoverer, ok := s.store.(storage.DirectImportReceiptRecoveryStore)
	if !ok {
		return nil
	}
	claims, err := s.loadLibraryImportClaims()
	if err != nil {
		return err
	}
	byOwner := make(map[string][]libraryImportClaim)
	for _, claim := range claims {
		if claim.RemoteID != "direct-import:browser-recording" {
			continue
		}
		byOwner[claim.OwnerID] = append(byOwner[claim.OwnerID], claim)
	}
	for owner, owned := range byOwner {
		if _, active := activeLibraryImportOwners.Load(owner); active {
			continue
		}
		recoveryOwner := owner
		takenOver := false
		if s.haExecution && !browserRecordingClaimSetCommitted(owned) {
			var takeoverErr error
			recoveryOwner, owned, takeoverErr = s.takeExpiredBrowserRecordingClaims(owner, owned)
			if takeoverErr != nil {
				return fmt.Errorf("take over staged browser recording owner %s: %w", owner, takeoverErr)
			}
			if len(owned) == 0 {
				continue
			}
			takenOver = true
		}
		recovered, recoverErr := s.recoverStagedBrowserRecordingOwner(recoverer, owned, takenOver)
		if recoverErr != nil {
			if takenOver {
				recoverErr = errors.Join(recoverErr, s.expireLibraryImportClaims(recoveryOwner))
			}
			return fmt.Errorf("recover staged browser recording owner %s: %w", recoveryOwner, recoverErr)
		}
		if recovered {
			if err := s.removeLibraryImportClaims(recoveryOwner); err != nil {
				return fmt.Errorf("publish recovered browser recording claims: %w", err)
			}
		} else if takenOver {
			// The expired transaction was incomplete or failed authentication.
			// Expose only our fenced takeover owner to ordinary compensation.
			if err := s.expireLibraryImportClaims(recoveryOwner); err != nil {
				return fmt.Errorf("expire incomplete browser recording takeover: %w", err)
			}
			if err := s.recoverExpiredLibraryImportClaimsWithBrowserCompensation(true); err != nil {
				return fmt.Errorf("compensate incomplete browser recording takeover: %w", err)
			}
		}
	}
	return nil
}

func browserRecordingClaimSetCommitted(claims []libraryImportClaim) bool {
	if len(claims) == 0 {
		return false
	}
	for _, claim := range claims {
		if !claim.Committed {
			return false
		}
	}
	return true
}

// takeExpiredBrowserRecordingClaims establishes the same lease fence used by
// generic HA recovery before adopting an uncommitted staged save. The CAS
// changes the whole original owner set atomically; a live replica that renews
// first wins, while a paused original owner cannot later commit after takeover.
func (s *Server) takeExpiredBrowserRecordingClaims(
	owner string,
	expected []libraryImportClaim,
) (string, []libraryImportClaim, error) {
	owner = strings.TrimSpace(owner)
	if owner == "" || len(expected) == 0 {
		return "", nil, nil
	}
	expectedIdentities := make(map[string]struct{}, len(expected))
	for _, claim := range expected {
		expectedIdentities[claim.Identity] = struct{}{}
	}
	recoveryOwner := uuid.NewString()
	var taken []libraryImportClaim
	err := s.mutateLibraryImportClaims(func(claims *[]libraryImportClaim) (bool, error) {
		taken = taken[:0]
		now := time.Now().UTC()
		indexes := make([]int, 0, len(expectedIdentities))
		for index, claim := range *claims {
			if claim.OwnerID == owner {
				indexes = append(indexes, index)
			}
		}
		if len(indexes) == 0 {
			return false, nil
		}
		if len(indexes) != len(expectedIdentities) {
			return false, errors.New("browser recording claim owner changed shape before takeover")
		}
		for _, index := range indexes {
			claim := (*claims)[index]
			if _, ok := expectedIdentities[claim.Identity]; !ok || claim.Committed ||
				claim.RemoteID != "direct-import:browser-recording" || claim.ExpiresAt.After(now) {
				return false, nil
			}
		}
		for _, index := range indexes {
			(*claims)[index].OwnerID = recoveryOwner
			(*claims)[index].ExpiresAt = now.Add(libraryImportClaimTTL())
			taken = append(taken, (*claims)[index])
		}
		return true, nil
	})
	if err != nil {
		return "", nil, err
	}
	if len(taken) == 0 {
		return "", nil, nil
	}
	return recoveryOwner, taken, nil
}

func (s *Server) recoverStagedBrowserRecordingOwner(
	recoverer storage.DirectImportReceiptRecoveryStore,
	claims []libraryImportClaim,
	requireReceiptLeaseTakeover ...bool,
) (bool, error) {
	if len(claims) == 0 || len(claims) > maxBridgedBrowserImportScripts+1 {
		return false, nil
	}
	sort.Slice(claims, func(i, j int) bool { return claims[i].Sequence < claims[j].Sequence })
	stored := make([]*StoredScript, len(claims))
	recordingID := ""
	projectID := ""
	for index, claim := range claims {
		if claim.Kind != "script" || !claim.Prepared ||
			claim.BeforeScript != nil || strings.TrimSpace(claim.LocalItemID) == "" ||
			claim.Sequence != index {
			return false, nil
		}
		script, err := s.store.GetScript(claim.LocalItemID)
		if errors.Is(err, sql.ErrNoRows) || script == nil {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		matches, err := s.libraryImportClaimScriptHashMatches(claim, script, claim.IntendedHash)
		if err != nil {
			return false, err
		}
		if !matches || normalizeProjectID(script.ProjectID) != normalizeProjectID(claim.ProjectID) {
			return false, nil
		}
		preserved := browserRecordingImportPreserved(script)
		currentRecordingID := browserRecordingPreservedString(preserved, browserRecordingSaveSessionIDKey)
		if currentRecordingID == "" || browserRecordingPreservedInt(preserved, browserRecordingSaveVersionKey) != browserRecordingSaveVersion ||
			browserRecordingPreservedInt(preserved, browserRecordingSaveIndexKey) != index ||
			browserRecordingPreservedInt(preserved, browserRecordingSaveCountKey) != len(claims) {
			return false, nil
		}
		if err := s.verifyBrowserRecordingSaveProvenance(script); err != nil {
			return false, nil
		}
		if recordingID == "" {
			recordingID = currentRecordingID
			projectID = normalizeProjectID(script.ProjectID)
		} else if recordingID != currentRecordingID || projectID != normalizeProjectID(script.ProjectID) {
			return false, nil
		}
		stored[index] = &StoredScript{
			ID: script.ID, RefKey: script.RefKey, Name: script.Name, Protocol: script.Protocol,
			Version: script.Version, ProjectID: script.ProjectID, FolderID: script.FolderID,
			CreatedAt: script.CreatedAt, Script: script,
		}
	}
	locatorKey, err := s.browserRecordingSaveLocatorKey(recordingID)
	if err != nil {
		return false, err
	}
	raw, err := s.store.GetSetting(locatorKey)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(raw) == "" {
		return false, nil
	}
	locator, err := s.decodeBrowserRecordingSaveLocator(raw)
	if err != nil || locator.RecordingID != recordingID || locator.ProjectID != projectID ||
		(locator.State != browserRecordingSaveLocatorStaged && locator.State != browserRecordingSaveLocatorPublished) ||
		locator.ReceiptID == "" || locator.OperationHash == "" || locator.RequestDigest == "" {
		return false, nil
	}
	scriptIDs := deterministicScriptIDsFromStored(stored)
	if !slices.Equal(locator.ScriptIDs, scriptIDs) {
		return false, nil
	}
	baselineValid, err := s.validatePersistedBrowserRecordingHARBaselineForAdoption(stored)
	if err != nil {
		return false, err
	}
	if !baselineValid {
		return false, nil
	}
	var recovered bool
	if len(requireReceiptLeaseTakeover) > 0 && requireReceiptLeaseTakeover[0] {
		recovered, err = s.recoverBrowserRecordingReceiptAfterLease(locator, projectID, scriptIDs)
	} else {
		recovered, err = recoverer.RecoverDirectImportReceipt(
			locator.ReceiptID,
			projectID,
			"browser_recorder",
			locator.OperationHash,
			locator.RequestDigest,
			scriptIDs,
			"",
			time.Now().UTC(),
		)
	}
	if err != nil {
		return false, err
	}
	if !recovered {
		return false, nil
	}
	if err := s.commitRecoveredBrowserRecordingClaims(claims); err != nil {
		return false, err
	}
	if locator.State != browserRecordingSaveLocatorPublished {
		publishedLocator := *locator
		publishedLocator.State = browserRecordingSaveLocatorPublished
		published, err := s.encodeBrowserRecordingSaveLocatorValue(publishedLocator)
		if err != nil {
			return false, err
		}
		if err := s.publishBrowserRecordingSaveLocatorEncoding(locatorKey, raw, published); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (s *Server) commitRecoveredBrowserRecordingClaims(expected []libraryImportClaim) error {
	if len(expected) == 0 {
		return errors.New("recovered browser recording has no claim evidence")
	}
	owner := expected[0].OwnerID
	expectedByIdentity := make(map[string]libraryImportClaim, len(expected))
	for _, claim := range expected {
		if claim.OwnerID != owner {
			return errors.New("recovered browser recording claims have mixed owners")
		}
		expectedByIdentity[claim.Identity] = claim
	}
	return s.mutateLibraryImportClaims(func(claims *[]libraryImportClaim) (bool, error) {
		indexes := make([]int, 0, len(expectedByIdentity))
		for index, claim := range *claims {
			if claim.OwnerID != owner {
				continue
			}
			exact, ok := expectedByIdentity[claim.Identity]
			if !ok || claim.MutationID != exact.MutationID ||
				claim.LocalItemID != exact.LocalItemID || claim.IntendedHash != exact.IntendedHash ||
				claim.Sequence != exact.Sequence || !claim.Prepared {
				return false, errors.New("browser recording claims changed before recovered commit")
			}
			indexes = append(indexes, index)
		}
		if len(indexes) != len(expectedByIdentity) {
			return false, errors.New("browser recording claim set changed before recovered commit")
		}
		changed := false
		for _, index := range indexes {
			if !(*claims)[index].Applied || !(*claims)[index].Committed {
				(*claims)[index].Applied = true
				(*claims)[index].Committed = true
				changed = true
			}
		}
		return changed, nil
	})
}

// recoverBrowserRecordingReceiptAfterLease is the second HA takeover fence.
// Claim expiry alone is insufficient because a paused original request may
// still own the deliberately longer receipt lease and therefore may still
// compensate its assets. Taking the exact pending receipt first makes that
// original rollback lose ownership and skip destructive compensation.
func (s *Server) recoverBrowserRecordingReceiptAfterLease(
	locator *browserRecordingSaveLocator,
	projectID string,
	scriptIDs []string,
) (bool, error) {
	store, ok := s.store.(storage.DirectImportReceiptStore)
	if !ok || locator == nil {
		return false, errors.New("durable browser recording receipt takeover is unavailable")
	}
	ownerToken := uuid.NewString()
	claim, err := store.ClaimDirectImportReceipt(
		projectID,
		"browser_recorder",
		locator.OperationHash,
		locator.RequestDigest,
		ownerToken,
		time.Now().UTC(),
		directImportReceiptLease(),
	)
	if err != nil {
		return false, err
	}
	if claim == nil || claim.Receipt == nil {
		return false, errors.New("browser recording receipt takeover returned no receipt")
	}
	if claim.Receipt.ID != locator.ReceiptID {
		if claim.Created {
			_, _ = store.AbandonDirectImportReceipt(claim.Receipt.ID, ownerToken)
		} else if releaser, ok := store.(storage.DirectImportReceiptReleaseStore); ok && !claim.Replay {
			_, _ = releaser.ReleaseDirectImportReceipt(claim.Receipt.ID, ownerToken, time.Now().UTC())
		}
		return false, nil
	}
	if claim.Replay {
		return claim.Receipt.State == storage.DirectImportReceiptComplete &&
			claim.Receipt.ScenarioID == "" && slices.Equal(claim.Receipt.ScriptIDs, scriptIDs), nil
	}
	if claim.Created {
		_, _ = store.AbandonDirectImportReceipt(claim.Receipt.ID, ownerToken)
		return false, nil
	}
	attempt := &directImportAttempt{store: store, receipt: claim.Receipt, ownerToken: ownerToken}
	stored := make([]*StoredScript, len(scriptIDs))
	for index, id := range scriptIDs {
		stored[index] = &StoredScript{ID: id}
	}
	if err := attempt.complete(stored, nil); err != nil {
		return false, errors.Join(err, attempt.release())
	}
	return true, nil
}

func deterministicScriptIDsFromStored(stored []*StoredScript) []string {
	ids := make([]string, len(stored))
	for index, script := range stored {
		if script != nil {
			ids[index] = script.ID
		}
	}
	return ids
}

// reconcileBrowserRecordingPublication closes the recorder's three durable
// boundaries in one order on initial saves and every retry path: receipt,
// signed locator, then retained terminal-claim cleanup. Each step is
// idempotent. If receipt completion fails, release the pending owner so the
// same stopped session can retry immediately rather than waiting a full lease.
func (s *Server) reconcileBrowserRecordingPublication(
	recordingID string,
	projectID string,
	allStored []*StoredScript,
	attempt *directImportAttempt,
	transaction *createImportTransaction,
	completeReceipt bool,
) error {
	if err := s.persistBrowserRecordingSaveLocator(recordingID, projectID, allStored, attempt); err != nil {
		if completeReceipt && attempt != nil {
			return errors.Join(err, attempt.release())
		}
		return err
	}
	if completeReceipt && attempt != nil {
		if err := attempt.complete(allStored, nil); err != nil {
			return errors.Join(err, attempt.release())
		}
	}
	if err := s.publishBrowserRecordingSaveLocator(recordingID, projectID, allStored, attempt); err != nil {
		return err
	}
	if transaction != nil {
		return transaction.cleanupCommittedClaims()
	}
	return s.cleanupPublishedBrowserRecordingClaims(recordingID, projectID, allStored)
}

// cleanupPublishedBrowserRecordingClaims removes only the exact committed
// owner set authenticated by the published locator, per-script proofs, claim
// digests, and durable receipt. It lets receipt-replay and stale-adoption paths
// finish cleanup without retaining a process-local transaction handle.
func (s *Server) cleanupPublishedBrowserRecordingClaims(
	recordingID string,
	projectID string,
	allStored []*StoredScript,
) error {
	if s == nil || s.store == nil {
		return nil
	}
	recoverer, ok := s.store.(storage.DirectImportReceiptRecoveryStore)
	if !ok {
		return nil
	}
	expectedIDs := deterministicScriptIDsFromStored(allStored)
	for _, stored := range allStored {
		if stored == nil || stored.Script == nil ||
			browserRecordingPreservedString(
				browserRecordingImportPreserved(stored.Script),
				browserRecordingSaveSessionIDKey,
			) != strings.TrimSpace(recordingID) {
			return errors.New("published browser recording scripts do not match the recording id")
		}
	}
	claims, err := s.loadLibraryImportClaims()
	if err != nil {
		return err
	}
	byOwner := make(map[string][]libraryImportClaim)
	for _, claim := range claims {
		if claim.RemoteID == "direct-import:browser-recording" &&
			normalizeProjectID(claim.ProjectID) == normalizeProjectID(projectID) {
			byOwner[claim.OwnerID] = append(byOwner[claim.OwnerID], claim)
		}
	}
	matchingOwner := ""
	var matching []libraryImportClaim
	for owner, owned := range byOwner {
		sort.Slice(owned, func(i, j int) bool { return owned[i].Sequence < owned[j].Sequence })
		ids := make([]string, len(owned))
		for index, claim := range owned {
			ids[index] = claim.LocalItemID
		}
		if !slices.Equal(ids, expectedIDs) {
			continue
		}
		if matchingOwner != "" {
			return errors.New("multiple retained browser recording claim owners match the published save")
		}
		matchingOwner = owner
		matching = owned
	}
	if matchingOwner == "" {
		return nil
	}
	if !browserRecordingClaimSetCommitted(matching) {
		return errors.New("published browser recording claims are not terminal")
	}
	recovered, err := s.recoverStagedBrowserRecordingOwner(recoverer, matching)
	if err != nil {
		return err
	}
	if !recovered {
		return errors.New("published browser recording claims do not match their durable receipt")
	}
	return s.removeLibraryImportClaims(matchingOwner)
}

func (s *Server) removeBrowserRecordingSaveLocator(
	recordingID string,
	projectID string,
	allStored []*StoredScript,
	attempts ...*directImportAttempt,
) error {
	if s == nil || s.store == nil {
		return nil
	}
	scriptIDs := make([]string, 0, len(allStored))
	for _, stored := range allStored {
		if stored != nil {
			scriptIDs = append(scriptIDs, stored.ID)
		}
	}
	locatorKey, err := s.browserRecordingSaveLocatorKey(recordingID)
	if err != nil {
		return err
	}
	encoded, err := s.encodeBrowserRecordingSaveLocator(recordingID, projectID, scriptIDs, attempts...)
	if err != nil {
		return err
	}
	cas, ok := s.store.(settingsCompareAndSwapper)
	if !ok {
		return errors.New("durable browser recording locator requires atomic settings storage")
	}
	won, err := cas.CompareAndSwapSetting(locatorKey, encoded, "")
	if err != nil {
		return err
	}
	if !won {
		current, loadErr := s.store.GetSetting(locatorKey)
		if loadErr != nil {
			return loadErr
		}
		if current == encoded {
			return errors.New("browser recording locator could not be cleared")
		}
	}
	return nil
}

// rollbackBrowserRecordingSave clears the staged locator only after receipt-
// fenced asset compensation completed definitively. An ownership-moved or
// storage-ambiguous rollback leaves both the claim evidence and locator for the
// winning owner/recovery healer; deleting only the locator would strand the
// exact stopped save permanently.
func (s *Server) rollbackBrowserRecordingSave(
	attempt *directImportAttempt,
	transaction *createImportTransaction,
	recordingID string,
	projectID string,
	locatorStored []*StoredScript,
	locatorPrepared bool,
) error {
	if err := attempt.rollback(transaction); err != nil {
		return err
	}
	if !locatorPrepared {
		return nil
	}
	return s.removeBrowserRecordingSaveLocator(
		recordingID,
		projectID,
		locatorStored,
		attempt,
	)
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
// browserRecordingImportWorkflow names browser-recording saves in the shared
// receipt helpers. It skips browser claim compensation because the
// format-specific reconciler is what drives these paths.
var browserRecordingImportWorkflow = importBatchWorkflow{
	Profile:                      "browser_recorder",
	Noun:                         "browser recording transaction",
	SkipBrowserClaimCompensation: true,
}

func (s *Server) createHTTPScriptFromHAR(artifact *recbrowser.Session, info *BrowserRecordingSession, name string) (*dsl.Script, []string, []scriptImportReviewSection, error) {
	generated, err := s.createHTTPScriptFromHARAtLevel(artifact, info, name, recbrowser.HTTPScriptLevelURL, browserHTTPScriptOptions{})
	return generated.Script, generated.Warnings, generated.Sections, err
}

// createHTTPScriptFromHARAtLevel builds the HTTP script at the requested
// level: "url" replays recorded URLs; "html" resolves recorded link, image
// and form clicks against the replayed page.
// browserHTTPScriptOptions are the conversion options of a browser
// recording's HTTP script.
type browserHTTPScriptOptions struct {
	AsyncScan          *bool
	RecordingProfileID string
	HTTPVersionMode    string
}

// browserHTTPScript is a browser recording's generated HTTP script with its
// conversion's warnings, review sections and correlations. Correlations has
// the shape of a HAR import's: the shared converter's, or the native
// Salesforce rewrite's when that replaced the script.
type browserHTTPScript struct {
	Script       *dsl.Script
	Warnings     []string
	Sections     []scriptImportReviewSection
	Correlations any
}

func (s *Server) createHTTPScriptFromHARAtLevel(artifact *recbrowser.Session, info *BrowserRecordingSession, name, level string, scriptOptions browserHTTPScriptOptions) (browserHTTPScript, error) {
	if len(artifact.HAR) == 0 {
		return browserHTTPScript{}, fmt.Errorf("no side-car HAR is available to build an HTTP script")
	}
	profile, err := s.projectRecordingProfile(normalizeProjectID(info.ProjectID), scriptOptions.RecordingProfileID)
	if err != nil {
		return browserHTTPScript{}, err
	}
	if strings.TrimSpace(level) == "" && profile != nil {
		level = profile.HTTPScriptLevel
	}
	httpName := strings.TrimSpace(name)
	if httpName == "" {
		httpName = info.Name
	}
	httpName += " (HTTP)"
	options := har.ConvertOptions{ScriptName: httpName, AsyncScan: scriptOptions.AsyncScan, Profile: profile, HTTPVersionMode: scriptOptions.HTTPVersionMode}
	var levelNotes []string
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "", recbrowser.HTTPScriptLevelURL:
	case recbrowser.HTTPScriptLevelHTML:
		options.HTMLSteps, levelNotes = recbrowser.HTMLLevelSteps(artifact.Steps, artifact.HAR)
	default:
		return browserHTTPScript{}, fmt.Errorf("http_script_level must be url or html")
	}
	result, err := har.Convert(artifact.HAR, options)
	if err != nil {
		return browserHTTPScript{}, err
	}
	result.Warnings = append(result.Warnings, levelNotes...)
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(info.ProjectID)
		result.Script.GeneratedBy = "browser-recorder"
		result.Script.GeneratedFrom = "browser_recording_har"
	}
	// Use the same native rewrite and review as a direct HAR import. Merely
	// attaching its review leaves the executable script with recorded Aura state.
	salesforceDraft := augmentHARImportWithSalesforce(result, artifact.HAR,
		normalizeProjectID(info.ProjectID), httpName, "browser_recording_har", true, false, true)
	result.Script = salesforceDraft.Script
	applyClientGeneratedUUIDs(result.Script, artifact.HAR, result)
	applyClientTimestamps(result.Script, artifact.HAR, result)

	// Run the shared augmentation over the generated HTTP draft and hand its
	// sections back. Before this the detectors ran here with their return value
	// discarded, so an Anypoint or Agentforce finding was preserved on the
	// script and never reached the operator.
	// The HAR converter's source review is deliberately not attached here. It
	// embeds a scrubbed copy of the capture, and this draft is persisted as a
	// credential-preserving script whose recorded values must survive verbatim —
	// the recorder already keeps the capture itself as the script's baseline.
	draft := importDraft{
		Source:           salesforceDraft.Source,
		SourceScriptName: httpName,
		ProjectID:        info.ProjectID,
		Script:           result.Script,
		Unsupported:      result.Unsupported,
		Sections:         salesforceDraft.ReviewSections,
		Warnings:         result.Warnings,
	}
	augmented := draft.augmentWithAgentforceTransform(false)
	recbrowser.AttachRecordingEnvironment(result.Script, artifact.RecordingEnvironment)
	return browserHTTPScript{Script: result.Script, Warnings: augmented.Warnings, Sections: augmented.Sections, Correlations: salesforceDraft.Correlations}, nil
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
	if !decodeOptionalJSONBody(w, r, &previewReq) || !validHTTPVersionMode(w, previewReq.HTTPVersionMode) {
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
	var httpSections []scriptImportReviewSection
	// Correlations are the HTTP output's: browser replay does not correlate.
	var httpCorrelations any
	if req.AlsoCreateHTTPScript {
		generated, err := s.createHTTPScriptFromHARAtLevel(artifact, info, req.Name, req.HTTPScriptLevel, browserHTTPScriptOptions{AsyncScan: req.AsyncScan, RecordingProfileID: req.RecordingProfileID, HTTPVersionMode: req.HTTPVersionMode})
		if err != nil {
			warnings = append(warnings, "HTTP script generation skipped: "+err.Error())
		} else if generated.Script != nil {
			httpScript, httpSections, httpCorrelations = generated.Script, generated.Sections, generated.Correlations
			scripts = append(scripts, httpScript)
			warnings = append(warnings, generated.Warnings...)
		}
	}
	response := s.newScriptImportResponseWithSourceSecrets(r, scriptImportResponseOptions{
		Script:           result.Script,
		Scripts:          scripts,
		Warnings:         warnings,
		Stats:            result.Stats,
		Correlations:     httpCorrelations,
		ReviewSections:   httpSections,
		Parameterization: buildImportParameterization(result.Script, nil),
	})
	if httpScript != nil {
		response["http_script"] = httpScript
	}
	writeScriptImportResponse(w, http.StatusOK, response)
}

func (s *Server) handleConvertBrowserRecording(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req browserRecordingConvertRequest
	if !decodeOptionalJSONBody(w, r, &req) {
		return
	}
	if !validHTTPVersionMode(w, req.HTTPVersionMode) {
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

	artifact, info, ok := s.browserRecording.Artifact(id)
	if !ok {
		s.handleRecoverBrowserRecordingConversion(w, r, id, req)
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
	var httpCorrelationBaseline []correlate.Observation
	var httpCorrelationBaselineErr error
	var httpWarnings []string
	if req.AlsoCreateHTTPScript {
		var err error
		// The convert route persists the drafts and answers with StoredScripts,
		// which carry no review sections; the preview route above is where the
		// generated HTTP draft's sections are shown.
		var generated browserHTTPScript
		generated, err = s.createHTTPScriptFromHARAtLevel(artifact, info, req.Name, req.HTTPScriptLevel, browserHTTPScriptOptions{AsyncScan: req.AsyncScan, RecordingProfileID: req.RecordingProfileID, HTTPVersionMode: req.HTTPVersionMode})
		httpScript, httpWarnings = generated.Script, generated.Warnings
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
				httpCorrelationBaseline, httpCorrelationBaselineErr = har.CorrelationBaseline(artifact.HAR, false)
				if httpCorrelationBaselineErr != nil {
					warnings = append(warnings, "HTTP correlation baseline capture skipped: "+httpCorrelationBaselineErr.Error())
					httpCorrelationBaseline = nil
				}
			}
		}
	}
	if err := validateBrowserRecordingHARBaselinePreparation(
		s.store != nil,
		preparedHTTPScript != nil,
		httpCorrelationBaseline,
		httpCorrelationBaselineErr,
	); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
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
	for index, script := range transactionScripts {
		if err := bindDraftScriptProject(script, projectID); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("converted script %d: %v", index+1, err))
			return
		}
		if status, err := s.applyProtectedImportFolderOverride(script, req.FolderID); err != nil {
			writeError(w, status, err.Error())
			return
		}
	}
	_, actorSubject, _ := s.resolveAuditActor(r)
	// One stopped recording is one durable conversion operation. The exact
	// scripts and save options are fenced by the keyed request HMAC below; do
	// not fold secret-binding names or other caller options into an
	// independently observable operation identity.
	operationID := strings.TrimSpace(actorSubject) + "\x00session:" + id
	assetIdentitySeed := directImportOperationHash(projectID, "browser_recorder", operationID)
	for index, script := range transactionScripts {
		script.ID = deterministicDirectImportAssetID(assetIdentitySeed, "script", index)
		script.RefKey = ""
	}
	actorHash, err := s.browserRecordingSaveActorFingerprint(id, actorSubject)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to bind browser recording save authority: "+err.Error())
		return
	}
	for index, script := range transactionScripts {
		role := browserRecordingSaveRoleBrowser
		baselineRequired := false
		if preparedHTTPScript != nil && index == len(transactionScripts)-1 {
			role = browserRecordingSaveRoleHTTP
			// A storage-free embedded runner has neither durable receipts nor a
			// baseline store to reconcile after restart. Keep its established
			// in-memory dual-script behavior; every durable save requires the
			// baseline before its receipt can complete.
			baselineRequired = s.store != nil && len(httpCorrelationBaseline) > 0
		}
		stampBrowserRecordingSaveProvenance(
			script,
			id,
			index,
			len(transactionScripts),
			actorHash,
			role,
			baselineRequired,
		)
		proof, proofErr := s.browserRecordingSaveProvenanceProof(script)
		if proofErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to attest browser recording save provenance: "+proofErr.Error())
			return
		}
		script.Metadata.Import.Preserved[browserRecordingSaveProofKey] = proof
	}
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
		if baselineErr := s.finalizeBrowserRecordingHARBaseline(stored, httpCorrelationBaseline); baselineErr != nil {
			writeError(w, http.StatusConflict, "completed browser recording receipt is missing its HTTP correlation baseline: "+baselineErr.Error())
			return
		}
		if publicationErr := s.reconcileBrowserRecordingPublication(
			id, projectID, stored, attempt, nil, false,
		); publicationErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to finish browser recording publication: "+publicationErr.Error())
			return
		}
		writeSavedConversion(stored, false)
		return
	}
	if attempt != nil && !attempt.created {
		// This request already owns the durable receipt recovery lease. Recovering
		// staged browser imports here could complete that same receipt under a
		// different owner before the request renews it; only reconcile unrelated
		// non-browser claims at this point.
		if !s.fenceImportBatchAdoption(w, browserRecordingImportWorkflow, attempt, projectID, deterministicScriptIDs(transactionScripts), "") {
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
			if baselineErr := s.finalizeBrowserRecordingHARBaseline(stored, httpCorrelationBaseline); baselineErr != nil {
				writeError(w, http.StatusInternalServerError, "failed to finalize browser recording HTTP baseline: "+baselineErr.Error())
				return
			}
			if publicationErr := s.reconcileBrowserRecordingPublication(
				id, projectID, stored, attempt, nil, true,
			); publicationErr != nil {
				writeError(w, http.StatusInternalServerError, "failed to reconcile browser recording publication: "+publicationErr.Error())
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
	locatorPrepared := false
	var locatorStored []*StoredScript
	defer func() {
		if !finished && !assetsCommitted {
			if cleanupErr := s.rollbackBrowserRecordingSave(
				attempt, transaction, id, projectID, locatorStored, locatorPrepared,
			); cleanupErr != nil {
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
	allStored := append([]*StoredScript(nil), storedScripts...)
	if httpStored != nil {
		allStored = append(allStored, httpStored)
	}
	if baselineErr := s.finalizeBrowserRecordingHARBaseline(allStored, httpCorrelationBaseline); baselineErr != nil {
		writeError(w, http.StatusInternalServerError, "failed to finalize browser recording HTTP baseline: "+baselineErr.Error())
		return
	}
	// Write the O(1) restart locator while these rows are still hidden behind
	// their durable import claims. A locator failure can then roll the whole
	// transaction back without ever publishing an unrecoverable save.
	if err := s.persistBrowserRecordingSaveLocator(id, projectID, allStored, attempt); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to prepare browser recording recovery locator: "+err.Error())
		return
	}
	locatorPrepared = true
	locatorStored = allStored
	if err := transaction.commitRetainingClaims(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit converted scripts: "+err.Error())
		return
	}
	assetsCommitted = true
	if err := s.reconcileBrowserRecordingPublication(
		id, projectID, allStored, attempt, transaction, true,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to finalize browser recording publication: "+err.Error())
		return
	}
	finished = true
	writeSavedConversion(allStored, true)
}

func (s *Server) handleRecoverBrowserRecordingConversion(
	w http.ResponseWriter,
	r *http.Request,
	recordingID string,
	req browserRecordingConvertRequest,
) {
	recovery, err := s.loadDurableBrowserRecordingConversion(recordingID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to inspect saved browser recording: "+err.Error())
		return
	}
	if recovery == nil {
		writeError(w, http.StatusNotFound, "browser recording session not found; a new save requires the original stopped recording capability")
		return
	}
	if req.ExpectedScriptCount > 0 && req.ExpectedScriptCount != len(recovery.Scripts) {
		writeError(w, http.StatusConflict, fmt.Sprintf(
			"saved browser recording contains %d script(s), not the %d reviewed script(s)",
			len(recovery.Scripts),
			req.ExpectedScriptCount,
		))
		return
	}
	_, actorSubject, _ := s.resolveAuditActor(r)
	actorHash, err := s.browserRecordingSaveActorFingerprint(recordingID, actorSubject)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to verify browser recording save authority: "+err.Error())
		return
	}
	hasHTTP := false
	for index, script := range recovery.Scripts {
		preserved := browserRecordingImportPreserved(script)
		if browserRecordingPreservedString(preserved, browserRecordingSaveActorHashKey) != actorHash {
			writeError(w, http.StatusNotFound, "browser recording session not found for the current operator")
			return
		}
		role := browserRecordingPreservedString(preserved, browserRecordingSaveRoleKey)
		switch role {
		case browserRecordingSaveRoleBrowser:
			if hasHTTP {
				writeError(w, http.StatusConflict, "saved browser recording has invalid script role ordering")
				return
			}
		case browserRecordingSaveRoleHTTP:
			if hasHTTP || index != len(recovery.Scripts)-1 {
				writeError(w, http.StatusConflict, "saved browser recording has invalid HTTP companion ordering")
				return
			}
			hasHTTP = true
		default:
			writeError(w, http.StatusConflict, "saved browser recording has invalid script role metadata")
			return
		}
	}
	operationID := strings.TrimSpace(actorSubject) + "\x00session:" + strings.TrimSpace(recordingID)
	requestDigest, err := s.directImportScriptsRequestDigest(recovery.Scripts, req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to fingerprint saved browser recording: "+err.Error())
		return
	}
	attempt, receiptReplay, err := s.claimDirectImportAttempt(
		recovery.ProjectID,
		"browser_recorder",
		operationID,
		requestDigest,
	)
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrDirectImportReceiptBusy):
			w.Header().Set("Retry-After", "2")
			writeError(w, http.StatusConflict, "the prior browser recording save is still finalizing")
		case errors.Is(err, storage.ErrDirectImportReceiptConflict):
			writeError(w, http.StatusConflict, "browser recording save options changed after the original save")
		default:
			writeError(w, http.StatusInternalServerError, "failed to reserve saved browser recording recovery: "+err.Error())
		}
		return
	}
	if attempt == nil {
		writeError(w, http.StatusConflict, "durable browser recording receipt recovery is unavailable")
		return
	}
	if attempt.created {
		if rollbackErr := attempt.rollback(nil); rollbackErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to reject an unrecognized browser recording save: "+rollbackErr.Error())
			return
		}
		writeError(w, http.StatusNotFound, "browser recording session not found; no durable save receipt exists for this operation")
		return
	}

	expectedIDs := deterministicScriptIDs(recovery.Scripts)
	assetIdentitySeed := directImportOperationHash(recovery.ProjectID, "browser_recorder", operationID)
	for index, expectedID := range expectedIDs {
		if expectedID != deterministicDirectImportAssetID(assetIdentitySeed, "script", index) {
			writeError(w, http.StatusConflict, "saved browser recording scripts do not belong to its durable receipt")
			return
		}
	}
	var stored []*StoredScript
	if receiptReplay {
		stored, _, err = s.loadDirectImportReceiptAssets(attempt)
	} else {
		if !s.fenceImportBatchAdoption(w, browserRecordingImportWorkflow, attempt, recovery.ProjectID, expectedIDs, "") {
			return
		}
		stored, _, err = s.loadDirectImportAssetsByIDs(recovery.ProjectID, expectedIDs, "")
	}
	if err != nil || !directImportPreparedAssetsMatch(s, stored, recovery.Scripts, nil, nil) {
		if err == nil {
			err = errors.New("saved scripts no longer match the original browser recording receipt")
		}
		writeError(w, http.StatusConflict, "saved browser recording receipt is inconsistent: "+err.Error())
		return
	}
	if baselineErr := s.finalizeBrowserRecordingHARBaseline(stored, nil); baselineErr != nil {
		writeError(w, http.StatusConflict, "saved browser recording HTTP baseline is incomplete: "+baselineErr.Error())
		return
	}
	if publicationErr := s.reconcileBrowserRecordingPublication(
		recordingID, recovery.ProjectID, stored, attempt, nil, !receiptReplay,
	); publicationErr != nil {
		writeError(w, http.StatusInternalServerError, "failed to reconcile browser recording publication: "+publicationErr.Error())
		return
	}
	browserStored, httpStored := splitBrowserRecordingStoredScripts(stored, hasHTTP)
	if len(browserStored) == 0 {
		writeError(w, http.StatusConflict, "saved browser recording receipt contains no browser script")
		return
	}
	browserScripts := make([]*dsl.Script, 0, len(browserStored))
	for _, item := range browserStored {
		browserScripts = append(browserScripts, item.Script)
	}
	result := &recbrowser.ConvertResult{
		Script:  browserScripts[0],
		Scripts: browserScripts,
		Stats: recbrowser.ConvertStats{
			ConvertedActions: len(browserScripts[0].Actions),
			Scripts:          len(browserScripts),
		},
	}
	var sourceHTTP *dsl.Script
	if httpStored != nil {
		sourceHTTP = httpStored.Script
	}
	s.writeSavedBrowserRecordingConversion(
		w,
		r,
		recordingID,
		result,
		sourceHTTP,
		nil,
		browserStored,
		httpStored,
		!receiptReplay,
	)
}

// validatePersistedBrowserRecordingHARBaselineForAdoption separates an
// incomplete staged save from a storage outage. Incomplete or invalid baseline
// evidence may be compensated by ordinary claim recovery; a read error must
// preserve the claim fence so a later healthy startup can retry adoption.
func (s *Server) validatePersistedBrowserRecordingHARBaselineForAdoption(
	allStored []*StoredScript,
) (bool, error) {
	var httpStored *StoredScript
	required := false
	for _, stored := range allStored {
		if stored == nil || stored.Script == nil {
			continue
		}
		preserved := browserRecordingImportPreserved(stored.Script)
		if browserRecordingPreservedString(preserved, browserRecordingSaveRoleKey) != browserRecordingSaveRoleHTTP {
			continue
		}
		if httpStored != nil {
			return false, nil
		}
		httpStored = stored
		required, _ = preserved[browserRecordingHARBaselineNeededKey].(bool)
	}
	if !required {
		return true, nil
	}
	if httpStored == nil || s == nil || s.store == nil {
		return false, nil
	}
	record, err := s.store.GetScriptRecording(httpStored.ID)
	if err != nil {
		return false, err
	}
	if record == nil || record.Source != "browser_recording_har" || record.RequestCount <= 0 {
		return false, nil
	}
	if err := validateRecordingActionGraph(record, httpStored.Script); err != nil {
		return false, nil
	}
	return true, nil
}

// finalizeBrowserRecordingHARBaseline is a required precondition for receipt
// completion whenever the saved HTTP companion says the stopped HAR produced
// correlation observations. New writes call it before committing the durable
// asset claims; stale reconciliation can repair it while the stopped artifact
// remains available, and restart-only replay verifies it without pretending a
// missing raw HAR can be reconstructed from the HTTP request script.
func (s *Server) finalizeBrowserRecordingHARBaseline(
	allStored []*StoredScript,
	observations []correlate.Observation,
) error {
	var httpStored *StoredScript
	required := false
	for _, stored := range allStored {
		if stored == nil || stored.Script == nil {
			continue
		}
		preserved := browserRecordingImportPreserved(stored.Script)
		if browserRecordingPreservedString(preserved, browserRecordingSaveRoleKey) != browserRecordingSaveRoleHTTP {
			continue
		}
		if httpStored != nil {
			return errors.New("multiple saved HTTP companions were found")
		}
		httpStored = stored
		required, _ = preserved[browserRecordingHARBaselineNeededKey].(bool)
	}
	if !required {
		return nil
	}
	if httpStored == nil || s.store == nil {
		return errors.New("HTTP baseline storage is unavailable")
	}
	validateExisting := func() error {
		record, loadErr := s.store.GetScriptRecording(httpStored.ID)
		if loadErr != nil {
			return loadErr
		}
		if record == nil || record.Source != "browser_recording_har" || record.RequestCount <= 0 {
			return errors.New("HTTP baseline record is missing")
		}
		if validationErr := validateRecordingActionGraph(record, httpStored.Script); validationErr != nil {
			return validationErr
		}
		return nil
	}
	if existingErr := validateExisting(); existingErr == nil {
		return nil
	} else if !errors.Is(existingErr, sql.ErrNoRows) && len(observations) == 0 {
		return existingErr
	}
	if len(observations) == 0 {
		return errors.New("the stopped HAR observations are no longer available")
	}
	if err := s.persistRecordedBaseline(
		httpStored.ID,
		"browser_recording_har",
		httpStored.Script,
		observations,
	); err != nil {
		return err
	}
	return validateExisting()
}

func validateBrowserRecordingHARBaselinePreparation(
	durable bool,
	hasHTTPCompanion bool,
	observations []correlate.Observation,
	derivationErr error,
) error {
	if !durable || !hasHTTPCompanion {
		return nil
	}
	if derivationErr != nil {
		return fmt.Errorf("HTTP correlation baseline could not be prepared: %w", derivationErr)
	}
	if len(observations) == 0 {
		return errors.New("HTTP correlation baseline could not be prepared because the recorded HAR contains no persistable HTTP exchanges")
	}
	return nil
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
	// persist_only=true keeps the state for later recordings without sending
	// even the scrubbed copy; it fails unless the state was kept.
	persistOnly := r.URL.Query().Get("persist_only") == "true"
	var data []byte
	if !persistOnly {
		scrubbed, err := scrubBrowserStorageState(artifact.StorageState)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to scrub storage state: "+err.Error())
			return
		}
		data = scrubbed
	}
	relativePath, stateFile := browserStorageStateExportLocation(info)
	// Persist a confined copy under the export dir so it can be supplied as a
	// storage_state_path on a later recording. This copy MUST be the RAW storage
	// state: it is read only server-side to seed a Playwright context (login
	// reuse), never served to a client — seeding a context with the scrubbed
	// [REDACTED] cookie values silently breaks session restore. The client
	// download below still receives the scrubbed state. For a download the copy
	// is best effort (a failure still allows the download); persist_only fails
	// unless it was kept.
	pruneBrowserStorageStateExports(time.Now())
	persisted := false
	if confined, err := resolveConfinedExportPath(exportBaseDir(), filepath.Dir(relativePath), filepath.Base(relativePath)); err == nil {
		if mkErr := os.MkdirAll(filepath.Dir(confined), 0o755); mkErr == nil {
			if writeErr := os.WriteFile(confined, artifact.StorageState, 0o600); writeErr == nil {
				w.Header().Set("X-APEXLoad-StorageState-Path", relativePath)
				persisted = true
			}
		}
	}
	if persistOnly {
		if !persisted {
			writeError(w, http.StatusInternalServerError, "the login state could not be kept on this recorder")
			return
		}
		// The path in the form the saved-state list and a recording start use.
		writeJSON(w, http.StatusOK, map[string]any{"path": browserStorageStateProjectDir(info.ProjectID) + "/" + stateFile})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, stateFile))
	w.Header().Set("X-APEXLoad-Sensitivity", scrub.SensitivityScrubbed)
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

type browserRecordingVerifyRequest struct {
	Conversion      browserRecordingConvertRequest `json:"conversion,omitempty"`
	ScriptIndex     int                            `json:"script_index,omitempty"`
	ReplaySessionID string                         `json:"replay_session_id,omitempty"`
	// VisibleBrowser replays in a visible browser rather than a headless
	// one. Some sites refuse a headless browser they let a person's browser
	// through; on the Recording Agent that browser opens on the workstation.
	VisibleBrowser bool `json:"visible_browser,omitempty"`
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
	if !validHTTPVersionMode(w, req.Conversion.HTTPVersionMode) {
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
	result := s.convertBrowserRecording(artifact, info, req.Conversion)
	scripts := append([]*dsl.Script(nil), result.Scripts...)
	if req.Conversion.AlsoCreateHTTPScript {
		generated, err := s.createHTTPScriptFromHARAtLevel(artifact, info, req.Conversion.Name, req.Conversion.HTTPScriptLevel, browserHTTPScriptOptions{AsyncScan: req.Conversion.AsyncScan, RecordingProfileID: req.Conversion.RecordingProfileID, HTTPVersionMode: req.Conversion.HTTPVersionMode})
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "HTTP conversion failed: "+err.Error())
			return
		}
		result.Warnings = append(result.Warnings, generated.Warnings...)
		if generated.Script != nil {
			scripts = append(scripts, generated.Script)
		}
	}
	if req.Conversion.ExpectedScriptCount < 0 || req.ScriptIndex < 0 || req.ScriptIndex >= len(scripts) ||
		(req.Conversion.ExpectedScriptCount > 0 && req.Conversion.ExpectedScriptCount != len(scripts)) {
		writeError(w, http.StatusConflict, "conversion outputs changed; refresh the preview before verifying")
		return
	}
	if err := validateBrowserRecordingParameterizationSelections(req.Conversion.ParameterizationSelections, len(scripts)); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateBrowserRecordingSecretBindings(req.Conversion.SecretBindings, len(scripts)); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	script, err := applyBrowserRecordingParameterizationSelections(scripts[req.ScriptIndex], req.ScriptIndex, req.Conversion.ParameterizationSelections)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	script, err = applyBrowserRecordingSecretBindings(script, req.ScriptIndex, req.Conversion.SecretBindings)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if script == nil || len(script.Actions)+len(script.Init)+len(script.Teardown) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "the recording produced no replayable actions")
		return
	}
	if script.ID == "" {
		script.ID = uuid.New().String()
	}
	if req.VisibleBrowser && strings.EqualFold(strings.TrimSpace(script.Protocol), "browser") {
		headless := false
		script = applyBrowserReplayOverrides(script, BrowserReplayStartRequest{Browser: &dsl.BrowserConfig{Headless: &headless}})
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

var browserNavigationErrorCode = regexp.MustCompile(`net::(ERR_[A-Z_]+)`)

// browserRecordingNavigationFailure turns a start page that would not load
// (net::ERR_* from the browser) into a reason the operator can act on,
// instead of a 500 carrying the browser engine's call log.
func browserRecordingNavigationFailure(err error, startURL string) (string, bool) {
	match := browserNavigationErrorCode.FindStringSubmatch(err.Error())
	if match == nil {
		return "", false
	}
	code := match[1]
	var why string
	switch {
	case code == "ERR_NAME_NOT_RESOLVED":
		why = "its host name could not be resolved"
	case code == "ERR_CONNECTION_REFUSED":
		why = "the connection was refused"
	case code == "ERR_SSL_PROTOCOL_ERROR" || strings.HasPrefix(code, "ERR_CERT_") || strings.HasPrefix(code, "ERR_SSL_"):
		why = "the secure (TLS) connection failed"
	case code == "ERR_CONNECTION_TIMED_OUT" || code == "ERR_TIMED_OUT":
		why = "the connection timed out"
	case code == "ERR_ADDRESS_UNREACHABLE" || code == "ERR_INTERNET_DISCONNECTED" || code == "ERR_NETWORK_CHANGED":
		why = "the address is unreachable from the recording machine"
	default:
		why = "the page did not load"
	}
	return fmt.Sprintf("The recording browser could not open %s: %s (%s). Check the address and that the recording machine can reach it, then start again.", scrub.ValueURL(startURL, scrub.Policy{}), why, code), true
}

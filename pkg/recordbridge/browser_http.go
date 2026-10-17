package recordbridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jasonczech/apexload/pkg/protocol/browsercontract"
	recbrowser "github.com/jasonczech/apexload/pkg/recorder/browser"
	"github.com/jasonczech/apexload/pkg/scrub"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const browserStorageStateDir = "browser-recorder-state"
const browserStorageStateFileSuffix = "-state.json"

var browserStorageStateIDSuffix = regexp.MustCompile(`-[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var browserNavigationErrorCode = regexp.MustCompile(`net::(ERR_[A-Z_]+)`)

type browserStorageStateSummary struct {
	Path        string     `json:"path"`
	Name        string     `json:"name"`
	RecordingID string     `json:"recording_id,omitempty"`
	SavedAt     time.Time  `json:"saved_at"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}
type BrowserRecordingBridgeConnectionSeed struct {
	Origin           string `json:"origin"`
	Token            string `json:"token"`
	SessionID        string `json:"session_id"`
	PairedOrigin     string `json:"paired_origin,omitempty"`
	PairedAt         string `json:"paired_at,omitempty"`
	PairingExpiresAt string `json:"pairing_expires_at,omitempty"`
	SessionExpiresAt string `json:"session_expires_at,omitempty"`
}
type browserRecordingControlPanelRequest struct {
	URL              string                                `json:"url"`
	BridgeConnection *BrowserRecordingBridgeConnectionSeed `json:"bridge_connection,omitempty"`
}

func browserStorageStateProjectDir(projectID string) string {
	dir := sanitizeDownloadName(normalizeProjectID(projectID))
	if dir == "" {
		return "unassigned"
	}
	return dir
}

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

func (s *Service) HandleListBrowserRecordingStorageStates(w http.ResponseWriter, r *http.Request) {
	projectID := s.authorizedProject(r, strings.TrimSpace(r.URL.Query().Get("project_id")))
	now := time.Now()
	pruneBrowserStorageStateExports(now, s.retention())
	projectDir := browserStorageStateProjectDir(projectID)
	states := []browserStorageStateSummary{}
	// The directory a save writes into (handleExportBrowserRecordingStorageState).
	probe, err := resolveConfinedExportPath(exportBaseDir(), filepath.Join(browserStorageStateDir, projectDir), "probe.json")
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	entries, err := os.ReadDir(filepath.Dir(probe))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		s.writeError(w, http.StatusInternalServerError, "list saved login states: "+err.Error())
		return
	}
	// Name each state by the recording it came from while that recording is kept.
	recordings := map[string]*BrowserRecordingSession{}
	for _, session := range s.Recording.List(projectID) {
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
		if s.retention() > 0 {
			expires := info.ModTime().Add(s.retention()).UTC()
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

func pruneBrowserStorageStateExports(now time.Time, retention time.Duration) {
	if retention <= 0 {
		return
	}
	root := filepath.Join(exportBaseDir(), browserStorageStateDir)
	cutoff := now.Add(-retention)
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(d.Name()) != ".json" {
			return nil //nolint:nilerr // a best-effort prune skips entries it cannot read
		}
		info, statErr := d.Info()
		if statErr != nil {
			return nil //nolint:nilerr // a best-effort prune skips entries it cannot read
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

func (s *Service) HandleStartBrowserRecording(w http.ResponseWriter, r *http.Request) {
	availability := s.availability()
	if !availability.Available {
		s.writeError(w, http.StatusServiceUnavailable, availability.Reason)
		return
	}

	var req BrowserRecordingStartRequest
	if !s.decodeJSONBody(w, r, &req) {
		return
	}
	if err := validateBrowserRecordingStartURL(req.StartURL); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !ValidBrowserRecordingQUICMode(req.QUICMode) {
		s.writeError(w, http.StatusBadRequest, "quic_mode must be auto or off")
		return
	}
	// A storage-state path is a server-side file Playwright will read; confine
	// it to the export directory so a client cannot point it at arbitrary host
	// files (path traversal / absolute paths).
	if strings.TrimSpace(req.StorageStatePath) != "" {
		confined, err := confineStorageStatePath(req.StorageStatePath, req.ProjectID)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		req.StorageStatePath = confined
	}

	session, err := s.Recording.Start(req)
	if err != nil {
		if errors.Is(err, ErrBrowserRecordingActive) {
			s.writeError(w, http.StatusConflict, err.Error())
			return
		}
		if availability, ok := browserRecordingAvailabilityFromStartError(err); ok {
			ref := s.logServerError(w, http.StatusServiceUnavailable, err, "code", availability.Code)
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"error":        availability.Reason,
				"availability": availability,
				"ref":          ref,
			})
			return
		}
		if reason, ok := browserRecordingNavigationFailure(err, req.StartURL); ok {
			s.writeError(w, http.StatusUnprocessableEntity, reason)
			return
		}
		s.writeError(w, http.StatusInternalServerError, "failed to start browser recording: "+err.Error())
		return
	}

	s.audit(r, "browser_recording.start", "success", "browser_recording", session.ID, session.ProjectID,
		map[string]any{"name": session.Name, "start_url": scrub.ValueURL(session.StartURL, scrub.Policy{}), "capture_har": session.CaptureHAR})
	writeJSON(w, http.StatusCreated, map[string]any{"session": session})
}

func (s *Service) HandleGetBrowserRecording(w http.ResponseWriter, r *http.Request) {
	session, ok := s.Recording.Get(r.PathValue("id"))
	if !ok {
		s.writeError(w, http.StatusNotFound, "browser recording session not found")
		return
	}
	raw := rawRecordingExportRequested(r)
	if raw && !s.canExportRaw(r, session.ProjectID) {
		s.audit(r, "browser_recording.read_raw", "denied", "browser_recording", session.ID, session.ProjectID, map[string]any{"reason": "insufficient_role"})
		s.writeError(w, http.StatusForbidden, "raw browser recording view requires editor, project_admin, or system_admin access")
		return
	}
	if !raw {
		session = SanitizeBrowserRecordingSessionForResponse(session)
		w.Header().Set("X-APEXLoad-Sensitivity", scrub.SensitivityScrubbed)
	} else {
		w.Header().Set("X-APEXLoad-Sensitivity", scrub.SensitivityRaw)
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": session})
}

func (s *Service) HandleListBrowserRecordings(w http.ResponseWriter, r *http.Request) {
	projectID := s.authorizedProject(r, strings.TrimSpace(r.URL.Query().Get("project_id")))
	sessions := s.Recording.List(projectID)
	for index := range sessions {
		sessions[index] = SanitizeBrowserRecordingSessionForResponse(sessions[index])
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions, "count": len(sessions)})
}

func (s *Service) HandleDeleteBrowserRecording(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	session, ok, err := s.Recording.Delete(id)
	if !ok {
		s.writeError(w, http.StatusNotFound, "browser recording session not found")
		return
	}
	if err != nil {
		s.writeError(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r, "browser_recording.delete", "success", "browser_recording", session.ID, session.ProjectID,
		map[string]any{"status": session.Status, "step_count": session.StepCount})
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id, "session": session})
}

func (s *Service) HandleStopBrowserRecording(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	session, ok := s.Recording.Stop(id)
	if !ok {
		s.writeError(w, http.StatusNotFound, "browser recording session not found")
		return
	}
	s.audit(r, "browser_recording.stop", "success", "browser_recording", session.ID, session.ProjectID,
		map[string]any{"stop_reason": session.StopReason, "step_count": session.StepCount})
	writeJSON(w, http.StatusOK, map[string]any{"session": session})
}

func (s *Service) HandleForceStopBrowserRecording(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	session, ok, err := s.Recording.ForceStop(id)
	if !ok {
		s.writeError(w, http.StatusNotFound, "browser recording session not found")
		return
	}
	if err != nil {
		s.writeError(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r, "browser_recording.force_stop", "success", "browser_recording", session.ID, session.ProjectID,
		map[string]any{"stop_reason": session.StopReason, "step_count": session.StepCount})
	writeJSON(w, http.StatusOK, map[string]any{"session": session})
}

func (s *Service) HandlePauseBrowserRecording(w http.ResponseWriter, r *http.Request) {
	session, ok, err := s.Recording.Pause(r.PathValue("id"))
	s.WriteBrowserRecordingControlResult(w, session, ok, err)
}

func (s *Service) HandleResumeBrowserRecording(w http.ResponseWriter, r *http.Request) {
	session, ok, err := s.Recording.Resume(r.PathValue("id"))
	s.WriteBrowserRecordingControlResult(w, session, ok, err)
}

func (s *Service) HandleNavigateBrowserRecording(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	if !s.decodeJSONBody(w, r, &req) {
		return
	}
	// Same scheme allow-list as the start URL: the panel must not be able to
	// point the recording browser at local files or other schemes.
	if err := validateBrowserRecordingStartURL(req.URL); err != nil {
		s.writeError(w, http.StatusBadRequest, strings.Replace(err.Error(), "start_url", "url", 1))
		return
	}
	id := r.PathValue("id")
	session, ok, err := s.Recording.Navigate(id, req.URL)
	if ok && err == nil {
		s.audit(r, "browser_recording.navigate", "success", "browser_recording", id, session.ProjectID,
			map[string]any{"url": scrub.ValueURL(req.URL, scrub.Policy{})})
	}
	s.WriteBrowserRecordingControlResult(w, session, ok, err)
}

func (s *Service) HandleFocusBrowserRecording(w http.ResponseWriter, r *http.Request) {
	session, ok, err := s.Recording.Focus(r.PathValue("id"))
	s.WriteBrowserRecordingControlResult(w, session, ok, err)
}

func (s *Service) HandleOpenBrowserRecordingControlPanel(w http.ResponseWriter, r *http.Request) {
	var req browserRecordingControlPanelRequest
	if !s.decodeJSONBody(w, r, &req) {
		return
	}
	if err := recbrowser.ValidateControlPanelURL(req.URL); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	panelOrigin, err := absoluteHTTPOrigin(req.URL)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "control panel url origin is invalid: "+err.Error())
		return
	}
	if requestOrigin := strings.TrimSpace(r.Header.Get("Origin")); requestOrigin != "" {
		normalizedRequestOrigin, err := absoluteHTTPOrigin(requestOrigin)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "request Origin is invalid: "+err.Error())
			return
		}
		if normalizedRequestOrigin != panelOrigin {
			s.writeError(w, http.StatusBadRequest, "control panel url must use the requesting dashboard origin")
			return
		}
	}
	localStorage := map[string]string{}
	if req.BridgeConnection != nil {
		key, value, err := req.BridgeConnection.localStorageEntry(req.URL)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		localStorage[key] = value
	}

	id := r.PathValue("id")
	session, ok, err := s.Recording.OpenControlPanel(id, req.URL, localStorage)
	if ok && err == nil {
		s.audit(r, "browser_recording.control_panel", "success", "browser_recording", id, session.ProjectID,
			map[string]any{"url": scrub.ValueURL(req.URL, scrub.Policy{})})
	}
	s.WriteBrowserRecordingControlResult(w, session, ok, err)
}

func (b *BrowserRecordingBridgeConnectionSeed) localStorageEntry(panelURL string) (string, string, error) {
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

func (s *Service) HandleStartBrowserRecordingTransaction(w http.ResponseWriter, r *http.Request) {
	s.HandleBrowserRecordingMarker(w, r, recbrowser.MarkerTransactionStart)
}

func (s *Service) HandleEndBrowserRecordingTransaction(w http.ResponseWriter, r *http.Request) {
	s.HandleBrowserRecordingMarker(w, r, recbrowser.MarkerTransactionEnd)
}

func (s *Service) HandleAddBrowserRecordingNote(w http.ResponseWriter, r *http.Request) {
	s.HandleBrowserRecordingMarker(w, r, recbrowser.MarkerNote)
}

func (s *Service) HandleBrowserRecordingMarker(w http.ResponseWriter, r *http.Request, kind string) {
	var req struct {
		Name      string `json:"name,omitempty"`
		Text      string `json:"text,omitempty"`
		AtStepSeq int    `json:"at_step_seq,omitempty"`
	}
	if !s.decodeOptionalJSONBody(w, r, &req) {
		return
	}

	session, marker, ok, err := s.Recording.AddMarker(r.PathValue("id"), kind, req.Name, req.Text, req.AtStepSeq)
	if !ok {
		s.writeError(w, http.StatusNotFound, "browser recording session not found")
		return
	}
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not live") {
			status = http.StatusConflict
		}
		s.writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": session, "marker": marker})
}

func (s *Service) WriteBrowserRecordingControlResult(w http.ResponseWriter, session *BrowserRecordingSession, ok bool, err error) {
	if !ok {
		s.writeError(w, http.StatusNotFound, "browser recording session not found")
		return
	}
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			s.writeError(w, http.StatusNotFound, err.Error())
			return
		}
		if strings.Contains(err.Error(), "not live") || strings.Contains(err.Error(), "no stopped artifact") {
			// Both are client-state preconditions (session not live / not yet
			// stopped), not backend failures — 409, not 502.
			s.writeError(w, http.StatusConflict, err.Error())
			return
		}
		s.writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": session})
}

func (s *Service) HandleSetBrowserRecordingSection(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Section string `json:"section"`
	}
	if !s.decodeJSONBody(w, r, &req) {
		return
	}
	session, ok, err := s.Recording.SetSection(r.PathValue("id"), req.Section)
	s.WriteBrowserRecordingControlResult(w, session, ok, err)
}

func (s *Service) HandleSetBrowserRecordingPickMode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		On bool `json:"on"`
	}
	if !s.decodeJSONBody(w, r, &req) {
		return
	}
	session, ok, err := s.Recording.SetAssertMode(r.PathValue("id"), req.On)
	s.WriteBrowserRecordingControlResult(w, session, ok, err)
}

func (s *Service) HandleDeleteLastBrowserRecordingStep(w http.ResponseWriter, r *http.Request) {
	session, ok, err := s.Recording.DeleteLastStep(r.PathValue("id"))
	s.WriteBrowserRecordingControlResult(w, session, ok, err)
}

func (s *Service) HandleUpdateBrowserRecordingStep(w http.ResponseWriter, r *http.Request) {
	seq, err := strconv.Atoi(r.PathValue("seq"))
	if err != nil || seq <= 0 {
		s.writeError(w, http.StatusBadRequest, "invalid step sequence")
		return
	}
	var req struct {
		Excluded        *bool   `json:"excluded,omitempty"`
		Secret          *bool   `json:"mark_secret,omitempty"`
		Param           *bool   `json:"mark_param,omitempty"`
		PromoteSelector *string `json:"promote_selector,omitempty"`
	}
	if !s.decodeJSONBody(w, r, &req) {
		return
	}
	id := r.PathValue("id")
	if req.Excluded != nil {
		session, ok, err := s.Recording.SetStepExcluded(id, seq, *req.Excluded)
		s.WriteBrowserRecordingControlResult(w, session, ok, err)
		return
	}
	if req.Secret != nil || req.Param != nil {
		session, ok, err := s.Recording.PatchStepMark(id, seq, req.Secret, req.Param)
		s.WriteBrowserRecordingControlResult(w, session, ok, err)
		return
	}
	if req.PromoteSelector != nil {
		selector := strings.TrimSpace(*req.PromoteSelector)
		if selector == "" {
			s.writeError(w, http.StatusBadRequest, "promote_selector cannot be empty")
			return
		}
		session, ok, err := s.Recording.PromoteStepSelector(id, seq, selector)
		s.WriteBrowserRecordingControlResult(w, session, ok, err)
		return
	}
	s.writeError(w, http.StatusBadRequest, "no step update provided (excluded, mark_secret, mark_param, or promote_selector)")
}

func (s *Service) HandleSuggestBrowserRecordingTransactions(w http.ResponseWriter, r *http.Request) {
	suggestions, ok := s.Recording.SuggestTransactions(r.PathValue("id"))
	if !ok {
		s.writeError(w, http.StatusNotFound, "browser recording session not found")
		return
	}
	if suggestions == nil {
		suggestions = []recbrowser.TransactionSuggestion{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"suggestions": suggestions})
}

func (s *Service) HandleListBrowserRecorderDevices(w http.ResponseWriter, r *http.Request) {
	devices := browsercontract.ListDevices()
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

func (s *Service) HandleExportBrowserRecordingStorageState(w http.ResponseWriter, r *http.Request) {
	artifact, info, ok := s.Recording.Artifact(r.PathValue("id"))
	if !ok {
		s.writeError(w, http.StatusNotFound, "browser recording session not found")
		return
	}
	if artifact == nil || len(artifact.StorageState) == 0 {
		s.writeError(w, http.StatusConflict, "no storage state is available (the recording is still running or captured none)")
		return
	}
	// persist_only=true keeps the state for later recordings without sending
	// even the scrubbed copy; it fails unless the state was kept.
	persistOnly := r.URL.Query().Get("persist_only") == "true"
	var data []byte
	if !persistOnly {
		scrubbed, err := scrubBrowserStorageState(artifact.StorageState)
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, "failed to scrub storage state: "+err.Error())
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
	pruneBrowserStorageStateExports(time.Now(), s.retention())
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
			s.writeError(w, http.StatusInternalServerError, "the login state could not be kept on this recorder")
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

func (s *Service) HandleBrowserRecorderInstall(w http.ResponseWriter, r *http.Request) {
	availability := s.availability()
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
	state := s.Install.Start(func() error { return recbrowser.EnsureBrowsers() })
	s.audit(r, "browser_recording.install", "pending", "browser_recording", "", "", map[string]any{"status": state.Status})
	writeJSON(w, http.StatusAccepted, map[string]any{"install": state})
}

func (s *Service) HandleGetBrowserRecorderInstall(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"install": s.Install.Snapshot()})
}

func (s *Service) HandleExportBrowserRecording(w http.ResponseWriter, r *http.Request) {
	s.extendWriteDeadline(w)
	artifact, info, ok := s.Recording.Artifact(r.PathValue("id"))
	if !ok {
		s.writeError(w, http.StatusNotFound, "browser recording session not found")
		return
	}
	format := strings.ToLower(r.URL.Query().Get("format"))
	if format == "" {
		format = "har"
	}
	if format != "har" {
		s.writeError(w, http.StatusBadRequest, "unsupported browser recording export format: "+format)
		return
	}
	if artifact == nil || len(artifact.HAR) == 0 {
		s.writeError(w, http.StatusConflict, "no side-car HAR is available for this recording (HAR capture disabled or recording still running)")
		return
	}

	data := artifact.HAR
	raw := rawRecordingExportRequested(r)
	if raw && !s.canExportRaw(r, info.ProjectID) {
		s.audit(r, "browser_recording.export_har_raw", "denied", "browser_recording", info.ID, info.ProjectID, map[string]any{"reason": "insufficient_role"})
		s.writeError(w, http.StatusForbidden, "raw browser recording export requires editor, project_admin, or system_admin access")
		return
	}

	sensitivity := scrub.SensitivityRaw
	if !raw {
		scrubbed, err := scrub.HAR(data, scrub.Policy{})
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, "failed to scrub HAR: "+err.Error())
			return
		}
		data = scrubbed
		sensitivity = scrub.SensitivityScrubbed
	}
	s.audit(r, "browser_recording.export_har", "success", "browser_recording", info.ID, info.ProjectID, map[string]any{"sensitivity": sensitivity, "raw": raw})

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

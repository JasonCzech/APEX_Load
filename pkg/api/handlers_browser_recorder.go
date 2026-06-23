package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/convert/har"
	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/convert/salesforce"
	"github.com/jasonczech/apexload/pkg/dsl"
	browserprotocol "github.com/jasonczech/apexload/pkg/protocol/browser"
	recbrowser "github.com/jasonczech/apexload/pkg/recorder/browser"
	"github.com/jasonczech/apexload/pkg/scrub"
)

// browserRecordingAvailability reports whether this controller can launch a
// headed recording browser, with a human-readable reason when it cannot.
type browserRecordingAvailability struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// browserRecordingAvailability gates the feature server-side (the UI hint is
// advisory; POST /sessions fails closed on this check). Headed recording is
// off in production posture unless explicitly enabled, and needs a display.
func (s *Server) browserRecordingAvailability() browserRecordingAvailability {
	switch strings.ToLower(envValue("APEXLOAD_BROWSER_RECORDER")) {
	case "off", "disabled", "false", "0":
		return browserRecordingAvailability{Reason: "browser recording is disabled via APEXLOAD_BROWSER_RECORDER"}
	case "on", "enabled", "true", "1":
		// Explicit opt-in overrides the production-mode default-off.
	default:
		if productionMode() && !s.desktopMode && !s.bridgeMode {
			return browserRecordingAvailability{Reason: "browser recording launches a headed browser on the controller host and is disabled in production mode (set APEXLOAD_BROWSER_RECORDER=on to override)"}
		}
	}
	if runtime.GOOS == "linux" && strings.TrimSpace(os.Getenv("DISPLAY")) == "" && strings.TrimSpace(os.Getenv("WAYLAND_DISPLAY")) == "" {
		return browserRecordingAvailability{Reason: "no display detected on the controller host for a headed browser"}
	}
	return browserRecordingAvailability{Available: true}
}

// browserStorageStateDir is the confined subdirectory (under the export base)
// where recording storage states are persisted and read back for login reuse.
const browserStorageStateDir = "browser-recorder-state"

// confineStorageStatePath resolves a client-supplied storage-state path to an
// absolute path guaranteed to live under the export base dir, rejecting
// traversal/absolute escapes. The file must already exist.
func confineStorageStatePath(raw string) (string, error) {
	dir, file := filepathSplitForward(strings.TrimSpace(raw))
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
		confined, err := confineStorageStatePath(req.StorageStatePath)
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
		writeError(w, http.StatusInternalServerError, "failed to start browser recording: "+err.Error())
		return
	}

	s.recordAuditEvent(r, "browser_recording.start", "success", "browser_recording", session.ID, session.ProjectID,
		map[string]any{"name": session.Name, "start_url": session.StartURL, "capture_har": session.CaptureHAR})
	writeJSON(w, http.StatusCreated, map[string]any{"session": session})
}

func (s *Server) handleGetBrowserRecording(w http.ResponseWriter, r *http.Request) {
	session, ok := s.browserRecording.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "browser recording session not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": session})
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
			map[string]any{"url": req.URL})
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
			map[string]any{"url": req.URL})
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
		Name string `json:"name,omitempty"`
		Text string `json:"text,omitempty"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	session, marker, ok, err := s.browserRecording.AddMarker(r.PathValue("id"), kind, req.Name, req.Text)
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

	var req browserRecordingConvertRequest
	json.NewDecoder(r.Body).Decode(&req)

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
	response := newScriptImportResponse(scriptImportResponseOptions{
		Script:           result.Script,
		Scripts:          scripts,
		Warnings:         warnings,
		Stats:            result.Stats,
		Parameterization: buildImportParameterization(result.Script, nil),
	})
	if httpScript != nil {
		response["http_script"] = httpScript
	}
	writeJSON(w, http.StatusOK, response)
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
	json.NewDecoder(r.Body).Decode(&req)

	result := s.convertBrowserRecording(artifact, info, req)

	// Store every generated script (one for the single-script path, several
	// when splitting at transactions).
	storedScripts := make([]*StoredScript, 0, len(result.Scripts))
	for _, sc := range result.Scripts {
		if sc == nil {
			continue
		}
		storedScripts = append(storedScripts, s.runner.StoreScript(sc))
	}
	if len(storedScripts) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "conversion produced no scripts")
		return
	}
	primary := storedScripts[0]

	warnings := append([]string(nil), result.Warnings...)
	var httpStored *StoredScript
	if req.AlsoCreateHTTPScript {
		httpScript, httpWarnings, err := s.createHTTPScriptFromHAR(artifact, info, req.Name)
		if err != nil {
			warnings = append(warnings, "HTTP script generation skipped: "+err.Error())
		} else {
			httpStored = s.runner.StoreScript(httpScript)
			warnings = append(warnings, httpWarnings...)
			warnings = append(warnings, fmt.Sprintf("also created HTTP script %q (%s) from the recorded HAR", httpStored.Name, httpStored.ID))
		}
	}

	s.browserRecording.NotifyConverted(id, primary.ID, primary.Name)
	s.recordAuditEvent(r, "browser_recording.convert", "success", "script", primary.ID, primary.ProjectID,
		map[string]any{"recording_session_id": id, "name": primary.Name, "actions": result.Stats.ConvertedActions,
			"scripts": len(storedScripts), "http_script": httpStored != nil})

	response := newScriptImportResponse(scriptImportResponseOptions{
		Script:           primary,
		Scripts:          storedScriptsForResponse(storedScripts, httpStored),
		Warnings:         warnings,
		Stats:            result.Stats,
		Parameterization: buildImportParameterization(result.Script, nil),
	})
	if httpStored != nil {
		response["http_script"] = httpStored
	}
	writeJSON(w, http.StatusCreated, response)
}

func storedScriptsForResponse(scripts []*StoredScript, httpScript *StoredScript) []any {
	out := make([]any, 0, len(scripts)+1)
	for _, sc := range scripts {
		out = append(out, sc)
	}
	if httpScript != nil {
		out = append(out, httpScript)
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
		secret := req.Secret != nil && *req.Secret
		param := req.Param != nil && *req.Param
		session, ok, err := s.browserRecording.SetStepMark(id, seq, secret, param)
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
	filename := sanitizeDownloadName(info.Name)
	if filename == "" {
		filename = "browser-recording"
	}
	stateFile := filename + "-state.json"
	// Persist a confined copy under the export dir so it can be supplied as a
	// storage_state_path on a later recording (login reuse). Best effort.
	if confined, err := resolveConfinedExportPath(exportBaseDir(), browserStorageStateDir, stateFile); err == nil {
		if mkErr := os.MkdirAll(filepath.Dir(confined), 0o755); mkErr == nil {
			if writeErr := os.WriteFile(confined, artifact.StorageState, 0o600); writeErr == nil {
				w.Header().Set("X-APEXLoad-StorageState-Path", filepath.Join(browserStorageStateDir, stateFile))
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, stateFile))
	w.Header().Set("X-APEXLoad-Sensitivity", scrub.SensitivityRaw)
	w.WriteHeader(http.StatusOK)
	w.Write(artifact.StorageState)
}

// handleVerifyBrowserRecording converts the stopped recording in-memory and
// replays it immediately through the browser-replay engine so the user can see
// per-step pass/fail and self-heal suggestions without saving anything.
func (s *Server) handleVerifyBrowserRecording(w http.ResponseWriter, r *http.Request) {
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
	replay := s.browserReplay.Start(script.ID, script, BrowserReplayStartRequest{
		GroupName:       script.Name + " — verify",
		IgnoreThinkTime: &ignoreThink,
	})
	s.recordAuditEvent(r, "browser_recording.verify", "success", "browser_recording", id, info.ProjectID,
		map[string]any{"replay_session_id": replay.ID, "actions": result.Stats.ConvertedActions})
	writeJSON(w, http.StatusCreated, map[string]any{
		"replay_session_id": replay.ID,
		"replay_session":    replay,
		"warnings":          result.Warnings,
	})
}

// handleBrowserRecorderInstall provisions the Playwright driver/Chromium in the
// background so a controller without browsers can become recording-capable.
func (s *Server) handleBrowserRecorderInstall(w http.ResponseWriter, r *http.Request) {
	state := s.browserInstall.start(func() error { return recbrowser.EnsureBrowsers() })
	s.recordAuditEvent(r, "browser_recording.install", "pending", "browser_recording", "", "", map[string]any{"status": state.Status})
	writeJSON(w, http.StatusAccepted, map[string]any{"install": state})
}

func (s *Server) handleGetBrowserRecorderInstall(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"install": s.browserInstall.snapshot()})
}

func (s *Server) handleExportBrowserRecording(w http.ResponseWriter, r *http.Request) {
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

	// Same contract as the proxy recorder export: raw HAR-with-bodies stays
	// available for correlation analysis, scrub=true applies scrub-on-write,
	// and the response is labeled either way.
	data := artifact.HAR
	sensitivity := scrub.SensitivityRaw
	if r.URL.Query().Get("scrub") == "true" {
		scrubbed, err := scrub.HAR(data, scrub.Policy{})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to scrub HAR: "+err.Error())
			return
		}
		data = scrubbed
		sensitivity = scrub.SensitivityScrubbed
	}

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

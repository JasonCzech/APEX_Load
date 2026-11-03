package api

import (
	"net/http"
	"regexp"
	"time"

	"github.com/jasonczech/apexload/pkg/recordbridge"
	recbrowser "github.com/jasonczech/apexload/pkg/recorder/browser"
)

// browserRecordingAvailability reports whether this controller can launch a
// headed recording browser, with a human-readable reason when it cannot.
type browserRecordingAvailability = recordbridge.BrowserRecordingAvailability

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
	if s.recordingRuntime != nil {
		return s.recordingRuntime.Availability()
	}
	return recordbridge.RecordingAvailability(s.desktopMode || s.bridgeMode, productionMode(), browserRecordingDependencyProbe)
}

func browserRecordingAvailabilityFromStartError(err error) (browserRecordingAvailability, bool) {
	return recordbridge.BrowserRecordingAvailabilityFromStartError(err)
}

// browserStorageStateDir is the confined subdirectory (under the export base)
// where recording storage states are persisted and read back for login reuse.
const browserStorageStateDir = "browser-recorder-state"

// browserStorageStateFileSuffix ends every saved login state's file name.
const browserStorageStateFileSuffix = "-state.json"

// browserStorageStateProjectDir is the project-scoped directory segment used by
// browserStorageStateExportLocation; a nil/empty project maps to "unassigned".
func browserStorageStateProjectDir(projectID string) string {
	return recordbridge.BrowserStorageStateProjectDir(projectID)
}

// confineStorageStatePath resolves a client-supplied storage-state path to an
// absolute path guaranteed to live under the export base dir, rejecting
// traversal/absolute escapes. The file must already exist. It is also scoped to
// the requesting recording's project: the persisted reuse copy contains a RAW,
// unscrubbed Playwright storage state (live session cookies), so without this
// check a user could seed a new recording with another project's live session by
// pointing storage_state_path at that project's export subdirectory.
func confineStorageStatePath(raw, projectID string) (string, error) {
	return recordbridge.ConfineStorageStatePath(raw, projectID)
}

func filepathSplitForward(p string) (dir, file string) { return recordbridge.FilepathSplitForward(p) }

func browserStorageStateExportLocation(info *BrowserRecordingSession) (relativePath, stateFile string) {
	return recordbridge.BrowserStorageStateExportLocation(info)
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
	s.recordingHTTPService().HandleListBrowserRecordingStorageStates(w, r)
}

func pruneBrowserStorageStateExports(now time.Time) {
	recordbridge.PruneBrowserStorageStateExports(now, defaultBrowserRecordingRetention)
}

func scrubBrowserStorageState(data []byte) ([]byte, error) {
	return recordbridge.ScrubBrowserStorageState(data)
}

func scrubBrowserStorageStateNode(node any, parentKey string) {
	recordbridge.ScrubBrowserStorageStateNode(node, parentKey)
}

func browserStorageStateSensitiveName(name string) bool {
	return recordbridge.BrowserStorageStateSensitiveName(name)
}

// validateBrowserRecordingStartURL restricts recording targets to absolute
// http(s) URLs so the controller-driven browser cannot be pointed at local
// files or other schemes. Private/loopback hosts are intentionally allowed:
// recording apps on localhost is the local-first use case.
func validateBrowserRecordingStartURL(raw string) error {
	return recordbridge.ValidateBrowserRecordingStartURL(raw)
}

func (s *Server) handleStartBrowserRecording(w http.ResponseWriter, r *http.Request) {
	s.recordingHTTPService().HandleStartBrowserRecording(w, r)
}

func (s *Server) handleGetBrowserRecording(w http.ResponseWriter, r *http.Request) {
	s.recordingHTTPService().HandleGetBrowserRecording(w, r)
}

func (s *Server) handleListBrowserRecordings(w http.ResponseWriter, r *http.Request) {
	s.recordingHTTPService().HandleListBrowserRecordings(w, r)
}

func (s *Server) handleDeleteBrowserRecording(w http.ResponseWriter, r *http.Request) {
	s.recordingHTTPService().HandleDeleteBrowserRecording(w, r)
}

func (s *Server) handleStopBrowserRecording(w http.ResponseWriter, r *http.Request) {
	s.recordingHTTPService().HandleStopBrowserRecording(w, r)
}

func (s *Server) handleForceStopBrowserRecording(w http.ResponseWriter, r *http.Request) {
	s.recordingHTTPService().HandleForceStopBrowserRecording(w, r)
}

func (s *Server) handlePauseBrowserRecording(w http.ResponseWriter, r *http.Request) {
	s.recordingHTTPService().HandlePauseBrowserRecording(w, r)
}

func (s *Server) handleResumeBrowserRecording(w http.ResponseWriter, r *http.Request) {
	s.recordingHTTPService().HandleResumeBrowserRecording(w, r)
}

func (s *Server) handleNavigateBrowserRecording(w http.ResponseWriter, r *http.Request) {
	s.recordingHTTPService().HandleNavigateBrowserRecording(w, r)
}

func (s *Server) handleFocusBrowserRecording(w http.ResponseWriter, r *http.Request) {
	s.recordingHTTPService().HandleFocusBrowserRecording(w, r)
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
	s.recordingHTTPService().HandleOpenBrowserRecordingControlPanel(w, r)
}

func (b *browserRecordingBridgeConnectionSeed) localStorageEntry(panelURL string) (string, string, error) {
	return (*recordbridge.BrowserRecordingBridgeConnectionSeed)(b).LocalStorageEntry(panelURL)
}

func absoluteHTTPOrigin(raw string) (string, error) { return recordbridge.AbsoluteHTTPOrigin(raw) }

func (s *Server) handleStartBrowserRecordingTransaction(w http.ResponseWriter, r *http.Request) {
	s.recordingHTTPService().HandleStartBrowserRecordingTransaction(w, r)
}

func (s *Server) handleEndBrowserRecordingTransaction(w http.ResponseWriter, r *http.Request) {
	s.recordingHTTPService().HandleEndBrowserRecordingTransaction(w, r)
}

func (s *Server) handleAddBrowserRecordingNote(w http.ResponseWriter, r *http.Request) {
	s.recordingHTTPService().HandleAddBrowserRecordingNote(w, r)
}

func (s *Server) handleBrowserRecordingMarker(w http.ResponseWriter, r *http.Request, kind string) {
	s.recordingHTTPService().HandleBrowserRecordingMarker(w, r, kind)
}

// writeBrowserRecordingControlResult maps the shared (session, found, error)
// control-method result onto HTTP: 404 unknown id, 409 not-live, 502 backend
// failures (the browser refused the command), 200 with the fresh snapshot.
func (s *Server) writeBrowserRecordingControlResult(w http.ResponseWriter, session *BrowserRecordingSession, ok bool, err error) {
	s.recordingHTTPService().WriteBrowserRecordingControlResult(w, session, ok, err)
}

type browserRecordingConvertRequest = recordbridge.BrowserRecordingConvertRequest

// browserRecordingPreviewRequest deliberately excludes save-only secret
// bindings. Keeping a separate wire type makes the generated API contract
// truthful and lets DisallowUnknownFields reject a binding sent to preview.
type browserRecordingPreviewRequest = recordbridge.BrowserRecordingPreviewRequest

type browserRecordingSecretBinding = recordbridge.BrowserRecordingSecretBinding

type browserRecordingParameterizationSelection = recordbridge.BrowserRecordingParameterizationSelection

func (s *Server) handleSetBrowserRecordingSection(w http.ResponseWriter, r *http.Request) {
	s.recordingHTTPService().HandleSetBrowserRecordingSection(w, r)
}

func (s *Server) handleSetBrowserRecordingPickMode(w http.ResponseWriter, r *http.Request) {
	s.recordingHTTPService().HandleSetBrowserRecordingPickMode(w, r)
}

func (s *Server) handleDeleteLastBrowserRecordingStep(w http.ResponseWriter, r *http.Request) {
	s.recordingHTTPService().HandleDeleteLastBrowserRecordingStep(w, r)
}

func (s *Server) handleUpdateBrowserRecordingStep(w http.ResponseWriter, r *http.Request) {
	s.recordingHTTPService().HandleUpdateBrowserRecordingStep(w, r)
}

func (s *Server) handleSuggestBrowserRecordingTransactions(w http.ResponseWriter, r *http.Request) {
	s.recordingHTTPService().HandleSuggestBrowserRecordingTransactions(w, r)
}

// handleListBrowserRecorderDevices returns the device presets available for
// emulated recording.
func (s *Server) handleListBrowserRecorderDevices(w http.ResponseWriter, r *http.Request) {
	s.recordingHTTPService().HandleListBrowserRecorderDevices(w, r)
}

// handleExportBrowserRecordingStorageState writes the recording's exported
// Playwright storage state to a confined export path so it can be reused to
// skip login on a later recording.
func (s *Server) handleExportBrowserRecordingStorageState(w http.ResponseWriter, r *http.Request) {
	s.recordingHTTPService().HandleExportBrowserRecordingStorageState(w, r)
}

type browserRecordingVerifyRequest = recordbridge.BrowserRecordingVerifyRequest

type browserRecordingVerifyResponse = recordbridge.BrowserRecordingVerifyResponse

// handleVerifyBrowserRecording converts the stopped recording in-memory and
// replays it immediately through the browser-replay engine so the user can see
// per-step pass/fail and self-heal suggestions without saving anything.
// OpenAPI-Responses: 409, 500, 503
func (s *Server) handleVerifyBrowserRecording(w http.ResponseWriter, r *http.Request) {
	s.recordingHTTPService().HandleVerifyBrowserRecording(w, r)
}

// handleBrowserRecorderInstall provisions the Playwright driver/Chromium in the
// background so a controller without browsers can become recording-capable.
func (s *Server) handleBrowserRecorderInstall(w http.ResponseWriter, r *http.Request) {
	s.recordingHTTPService().HandleBrowserRecorderInstall(w, r)
}

func (s *Server) handleGetBrowserRecorderInstall(w http.ResponseWriter, r *http.Request) {
	s.recordingHTTPService().HandleGetBrowserRecorderInstall(w, r)
}

func (s *Server) handleExportBrowserRecording(w http.ResponseWriter, r *http.Request) {
	s.recordingHTTPService().HandleExportBrowserRecording(w, r)
}

var browserNavigationErrorCode = regexp.MustCompile(`net::(ERR_[A-Z_]+)`)

// browserRecordingNavigationFailure turns a start page that would not load
// (net::ERR_* from the browser) into a reason the operator can act on,
// instead of a 500 carrying the browser engine's call log.
func browserRecordingNavigationFailure(err error, startURL string) (string, bool) {
	return recordbridge.BrowserRecordingNavigationFailure(err, startURL)
}

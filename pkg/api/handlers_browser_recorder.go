package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jasonczech/apexload/pkg/recordbridge"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/dsl"
	recbrowser "github.com/jasonczech/apexload/pkg/recorder/browser"
	"github.com/jasonczech/apexload/pkg/storage"
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
const maxBrowserRecordingParameterizationSelections = recordbridge.MaxBrowserRecordingParameterizationSelections

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
		// Origin "device" hands over the HTTP script of a device capture the
		// agent's proxy recorded, instead of a browser recording's.
		Origin string `json:"origin,omitempty" enums:"device"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	device := false
	switch strings.TrimSpace(req.Origin) {
	case "":
	case "device":
		device = true
	default:
		writeError(w, http.StatusBadRequest, "origin must be omitted or device")
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
		switch {
		case protocol == "browser" && !device:
			source = "browser_recorder"
		case protocol == "http" || protocol == "soap":
			source = "browser_recording_har"
		case device:
			writeError(w, http.StatusBadRequest, "a device capture handoff supports only HTTP scripts")
			return
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
		if device {
			// The agent's proxy recorded the device; the script is saved
			// through the Recording Agent handoff all the same.
			ensureScriptImportPreserved(script, source)[deviceCaptureOriginKey] = deviceCaptureOrigin
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
	return s.recordingHTTPService().ConvertBrowserRecording(artifact, info, req)
}

var validateBrowserRecordingSecretBindings = recordbridge.ValidateBrowserRecordingSecretBindings

var applyBrowserRecordingSecretBindings = recordbridge.ApplyBrowserRecordingSecretBindings

var validateBrowserRecordingParameterizationSelections = recordbridge.ValidateBrowserRecordingParameterizationSelections

var applyBrowserRecordingParameterizationSelections = recordbridge.ApplyBrowserRecordingParameterizationSelections

// attachRecordingProvenance stamps the saved script with enough metadata to
// re-convert the recording later (recorder session id, device, step/marker
// counts). Raw step values stay out of provenance; the HAR is referenced, not
// embedded.
var attachRecordingProvenance = recordbridge.AttachRecordingProvenance

var stampBrowserRecordingSaveProvenance = recordbridge.StampBrowserRecordingSaveProvenance

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

type browserRecordingSaveProvenanceProof = recordbridge.BrowserRecordingSaveProvenanceProof

var browserRecordingSaveProvenanceProofPayload = recordbridge.BrowserRecordingSaveProvenanceProofPayload

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
			return false, nil //nolint:nilerr // unverified save provenance is not a staged recording save; claim recovery handles it
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
		return false, nil //nolint:nilerr // an unreadable or mismatched locator is not a staged recording save; claim recovery handles it
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

var applyAutoParameterization = recordbridge.ApplyAutoParameterization

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
	return s.recordingHTTPService().CreateHTTPScriptFromHAR(artifact, info, name)
}

// createHTTPScriptFromHARAtLevel builds the HTTP script at the requested
// level: "url" replays recorded URLs; "html" resolves recorded link, image
// and form clicks against the replayed page.
// browserHTTPScriptOptions are the conversion options of a browser
// recording's HTTP script.
type browserHTTPScriptOptions = recordbridge.BrowserHTTPScriptOptions

// browserHTTPScript is a browser recording's generated HTTP script with its
// conversion's warnings, review sections and correlations. Correlations has
// the shape of a HAR import's: the shared converter's, or the native
// Salesforce rewrite's when that replaced the script.
type browserHTTPScript = recordbridge.BrowserHTTPScript

func (s *Server) createHTTPScriptFromHARAtLevel(artifact *recbrowser.Session, info *BrowserRecordingSession, name, level string, scriptOptions browserHTTPScriptOptions) (browserHTTPScript, error) {
	return s.recordingHTTPService().CreateHTTPScriptFromHARAtLevel(artifact, info, name, level, scriptOptions)
}

func (s *Server) handlePreviewBrowserRecordingConversion(w http.ResponseWriter, r *http.Request) {
	s.recordingHTTPService().HandlePreviewBrowserRecordingConversion(w, r)
}

// Shared conversion preparation reports semantic review failures as 422.
// OpenAPI-Responses: 422
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

	prepared, preparationErr := s.recordingHTTPService().PrepareBrowserRecordingConversion(artifact, info, req, s.store != nil)
	if preparationErr != nil {
		writeError(w, preparationErr.Status, preparationErr.Message)
		return
	}
	result, preparedScripts := prepared.Result, prepared.Scripts
	httpScript, preparedHTTPScript := prepared.HTTPScript, prepared.PreparedHTTPScript
	httpCorrelationBaseline, httpWarnings := prepared.HTTPBaseline, prepared.HTTPWarnings
	warnings, transactionScripts := prepared.Warnings, prepared.TransactionScripts
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
		return false, nil //nolint:nilerr // an invalid action graph is invalid baseline evidence for claim recovery; only read errors fail
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

var validateBrowserRecordingHARBaselinePreparation = recordbridge.ValidateBrowserRecordingHARBaselinePreparation

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

var appendBrowserRecordingScripts = recordbridge.AppendBrowserRecordingScripts

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

package recordbridge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/convert/har"
	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/convert/sample"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/internal/importreview"
	"github.com/jasonczech/apexload/pkg/recorder"
	recbrowser "github.com/jasonczech/apexload/pkg/recorder/browser"
	"github.com/jasonczech/apexload/pkg/recorder/marker"
	"net/http"
	"strconv"
	"strings"
)

type BrowserRecordingConvertRequest struct {
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
	SecretBindings []BrowserRecordingSecretBinding `json:"secret_bindings,omitempty"`
	// ParameterizationSelections identify only the value-free suggestions the
	// operator selected in the preview. The server re-derives those suggestions
	// from the fresh conversion and applies them before the atomic create
	// transaction; the browser never supplies mutation instructions.
	ParameterizationSelections []BrowserRecordingParameterizationSelection `json:"parameterization_selections,omitempty"`
	// ExpectedScriptCount fences the preview/save handoff. A changed conversion
	// shape must fail before persistence rather than silently pairing reviewed
	// scripts with a different fresh conversion.
	ExpectedScriptCount int `json:"expected_script_count,omitempty"`
}

type BrowserRecordingPreviewRequest struct {
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

type BrowserRecordingSecretBinding struct {
	RequirementID string `json:"requirement_id"`
	SecretName    string `json:"secret_name"`
	ScriptIndex   int    `json:"script_index"`
}

type BrowserRecordingParameterizationSelection struct {
	ScriptIndex int      `json:"script_index"`
	SelectedIDs []string `json:"selected_ids"`
}

type BrowserHTTPScriptOptions struct {
	AsyncScan          *bool
	RecordingProfileID string
	HTTPVersionMode    string
}

type BrowserHTTPScript struct {
	Script       *dsl.Script
	Warnings     []string
	Sections     []importreview.ScriptImportReviewSection
	Correlations any
	// Baseline is the recorded observation of each request, keyed by its
	// action index in Script.
	Baseline []correlate.Observation
}

type BrowserRecordingVerifyRequest struct {
	Conversion      BrowserRecordingConvertRequest `json:"conversion,omitempty"`
	ScriptIndex     int                            `json:"script_index,omitempty"`
	ReplaySessionID string                         `json:"replay_session_id,omitempty"`
	// VisibleBrowser replays in a visible browser rather than a headless
	// one. Some sites refuse a headless browser they let a person's browser
	// through; on the Recording Agent that browser opens on the workstation.
	VisibleBrowser bool `json:"visible_browser,omitempty"`
}

type BrowserRecordingVerifyResponse struct {
	ReplaySessionID string                `json:"replay_session_id"`
	ReplaySession   *BrowserReplaySession `json:"replay_session"`
	Warnings        []string              `json:"warnings,omitempty"`
}

const MaxBrowserRecordingParameterizationSelections = 4096

func ValidateBrowserRecordingSecretBindings(
	bindings []BrowserRecordingSecretBinding,
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

func ApplyBrowserRecordingSecretBindings(
	script *dsl.Script,
	scriptIndex int,
	bindings []BrowserRecordingSecretBinding,
) (*dsl.Script, error) {
	selected := make([]importreview.SecretBindingSelection, 0)
	for _, binding := range bindings {
		if binding.ScriptIndex != scriptIndex {
			continue
		}
		selected = append(selected, importreview.SecretBindingSelection{
			RequirementID: binding.RequirementID,
			SecretName:    binding.SecretName,
		})
	}
	if len(selected) == 0 {
		return script, nil
	}
	resolved, _, err := importreview.ApplyCredentialPreservingSecretBindings(
		script,
		scriptIndex,
		selected,
	)
	if err != nil {
		return nil, err
	}
	return resolved, nil
}

func ValidateBrowserRecordingParameterizationSelections(
	selections []BrowserRecordingParameterizationSelection,
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
			if total > MaxBrowserRecordingParameterizationSelections {
				return fmt.Errorf(
					"parameterization selection exceeds the %d-suggestion limit",
					MaxBrowserRecordingParameterizationSelections,
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

func ApplyBrowserRecordingParameterizationSelections(
	script *dsl.Script,
	scriptIndex int,
	selections []BrowserRecordingParameterizationSelection,
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
	trusted := importreview.BuildTrustedImportParameterization(script)
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

func AttachRecordingProvenance(script *dsl.Script, artifact *recbrowser.Session, info *BrowserRecordingSession) {
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

func ApplyAutoParameterization(script *dsl.Script) {
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

func RecordingConversionStats(session *recorder.Session) map[string]int {
	excluded := 0
	for _, req := range session.Requests {
		if req.Excluded {
			excluded++
		}
	}
	return map[string]int{"requests_recorded": len(session.Requests), "requests_excluded": excluded}
}

func RecordingBaselineSnapshotDigest(session *recorder.Session) (string, error) {
	payload, err := json.Marshal(recorder.SessionBaseline(session))
	if err != nil {
		return "", fmt.Errorf("marshal recording baseline snapshot: %w", err)
	}
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func (s *Service) HandlePreviewRecordingConversion(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	session, ok := s.Recorder.GetSession(id)
	if !ok {
		s.writeError(w, http.StatusNotFound, "recording session not found")
		return
	}

	var req struct {
		Name         string `json:"name"`
		Parameterize bool   `json:"parameterize"`
		// PageTitleChecks (default on) and ResourceMode (runtime | in_step)
		// mirror VuGen's recording options.
		PageTitleChecks *bool  `json:"page_title_checks,omitempty"`
		ResourceMode    string `json:"resource_mode,omitempty"`
		// AsyncScan (default on) turns requests a page repeated in the
		// background into background conversations; false keeps them inline.
		AsyncScan *bool `json:"async_scan,omitempty"`
		// RecordingProfileID names a project recording profile; it defaults
		// to the one the session was started with.
		RecordingProfileID string `json:"recording_profile_id,omitempty"`
		// HTTPVersionMode maps captured HTTP versions to leaf policies.
		HTTPVersionMode string `json:"http_version_mode,omitempty" enums:"upgrade,exact,inherit"`
	}
	if !s.decodeOptionalJSONBody(w, r, &req) {
		return
	}
	if !s.validHTTPVersionMode(w, req.HTTPVersionMode) {
		return
	}
	profile, err := s.Conversion.SessionRecordingProfile(session, req.RecordingProfileID)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	result := recorder.ConvertSessionWithOptions(session, req.Name, recorder.ConvertOptions{Parameterize: req.Parameterize, PageTitleChecks: req.PageTitleChecks, ResourceMode: req.ResourceMode, AsyncScan: req.AsyncScan, Profile: profile, HTTPVersionMode: req.HTTPVersionMode})
	if result.Script != nil {
		result.Script.ProjectID = importreview.NormalizeProjectID(session.ProjectID)
		importreview.EnsureCredentialPreservingImportMetadata(result.Script, "recorder")
	}
	snapshotDigest, err := RecordingBaselineSnapshotDigest(session)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "failed to bind recording preview snapshot")
		return
	}
	s.writeImportResponseWithSourceSecrets(w, r, http.StatusOK, importreview.ScriptImportResponseOptions{
		Script:                  result.Script,
		Warnings:                result.Warnings,
		Diagnostics:             result.Diagnostics,
		Correlations:            result.Correlations,
		Stats:                   RecordingConversionStats(session),
		RecordingSessionID:      id,
		RecordingSnapshotDigest: snapshotDigest,
	})
}

func (req BrowserRecordingPreviewRequest) ConvertRequest() BrowserRecordingConvertRequest {
	return BrowserRecordingConvertRequest{
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

func (s *Service) ConvertBrowserRecording(artifact *recbrowser.Session, info *BrowserRecordingSession, req BrowserRecordingConvertRequest) *recbrowser.ConvertResult {
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
	projectID := importreview.NormalizeProjectID(info.ProjectID)
	for _, sc := range result.Scripts {
		if sc == nil {
			continue
		}
		sc.ProjectID = projectID
		AttachRecordingProvenance(sc, artifact, info)
		if req.Parameterize {
			ApplyAutoParameterization(sc)
		}
	}
	if result.Script != nil {
		result.Script.ProjectID = projectID
	}
	return result
}

func (s *Service) CreateHTTPScriptFromHAR(artifact *recbrowser.Session, info *BrowserRecordingSession, name string) (*dsl.Script, []string, []importreview.ScriptImportReviewSection, error) {
	generated, err := s.CreateHTTPScriptFromHARAtLevel(artifact, info, name, recbrowser.HTTPScriptLevelURL, BrowserHTTPScriptOptions{})
	return generated.Script, generated.Warnings, generated.Sections, err
}

func (s *Service) CreateHTTPScriptFromHARAtLevel(artifact *recbrowser.Session, info *BrowserRecordingSession, name, level string, scriptOptions BrowserHTTPScriptOptions) (BrowserHTTPScript, error) {
	if len(artifact.HAR) == 0 {
		return BrowserHTTPScript{}, fmt.Errorf("no side-car HAR is available to build an HTTP script")
	}
	profile, err := s.Conversion.ProjectRecordingProfile(importreview.NormalizeProjectID(info.ProjectID), scriptOptions.RecordingProfileID)
	if err != nil {
		return BrowserHTTPScript{}, err
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
	// The HTTP script times the transactions marked while recording with the
	// browser script's start and end markers, each request carries the
	// transaction that was open when it started, and a note attaches to the
	// request after it, as in the browser script.
	for _, window := range artifact.TransactionWindows() {
		options.Transactions = append(options.Transactions, sample.TransactionWindow(window))
	}
	for _, note := range marker.Notes(artifact.Markers) {
		options.Notes = append(options.Notes, sample.TimedNote(note))
	}
	var levelNotes []string
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "", recbrowser.HTTPScriptLevelURL:
	case recbrowser.HTTPScriptLevelHTML:
		options.HTMLSteps, levelNotes = recbrowser.HTMLLevelSteps(artifact.Steps, artifact.HAR)
	default:
		return BrowserHTTPScript{}, fmt.Errorf("http_script_level must be url or html")
	}
	result, err := har.Convert(artifact.HAR, options)
	if err != nil {
		return BrowserHTTPScript{}, err
	}
	result.Warnings = append(result.Warnings, levelNotes...)
	if result.Script != nil {
		result.Script.ProjectID = importreview.NormalizeProjectID(info.ProjectID)
		result.Script.GeneratedBy = "browser-recorder"
		result.Script.GeneratedFrom = "browser_recording_har"
	}
	// Use the same native rewrite and review as a direct HAR import. Merely
	// attaching its review leaves the executable script with recorded Aura state.
	salesforceDraft := importreview.AugmentHARImportWithSalesforce(result, artifact.HAR,
		importreview.NormalizeProjectID(info.ProjectID), httpName, "browser_recording_har", true, false, true)
	result.Script = salesforceDraft.Script
	importreview.ApplyClientGeneratedUUIDs(result.Script, artifact.HAR, result)
	importreview.ApplyClientTimestamps(result.Script, artifact.HAR, result)

	// Run the shared augmentation over the generated HTTP draft and hand its
	// sections back. Before this the detectors ran here with their return value
	// discarded, so an Anypoint or Agentforce finding was preserved on the
	// script and never reached the operator.
	// The HAR converter's source review is deliberately not attached here. It
	// embeds a scrubbed copy of the capture, and this draft is persisted as a
	// credential-preserving script whose recorded values must survive verbatim —
	// the recorder already keeps the capture itself as the script's baseline.
	draft := importreview.ImportDraft{
		Source:           salesforceDraft.Source,
		SourceScriptName: httpName,
		ProjectID:        info.ProjectID,
		Script:           result.Script,
		Unsupported:      result.Unsupported,
		Sections:         salesforceDraft.ReviewSections,
		Warnings:         result.Warnings,
	}
	augmented := draft.AugmentWithAgentforceTransform(false)
	recbrowser.AttachRecordingEnvironment(result.Script, artifact.RecordingEnvironment)
	return BrowserHTTPScript{Script: result.Script, Warnings: augmented.Warnings, Sections: augmented.Sections, Correlations: salesforceDraft.Correlations, Baseline: result.Baseline}, nil
}

func (s *Service) HandlePreviewBrowserRecordingConversion(w http.ResponseWriter, r *http.Request) {
	artifact, info, ok := s.Recording.Artifact(r.PathValue("id"))
	if !ok {
		s.writeError(w, http.StatusNotFound, "browser recording session not found")
		return
	}
	if artifact == nil {
		s.writeError(w, http.StatusConflict, "browser recording has no captured session yet")
		return
	}

	var previewReq BrowserRecordingPreviewRequest
	if !s.decodeOptionalJSONBody(w, r, &previewReq) || !s.validHTTPVersionMode(w, previewReq.HTTPVersionMode) {
		return
	}
	req := previewReq.ConvertRequest()

	result := s.ConvertBrowserRecording(artifact, info, req)
	warnings := append([]string(nil), result.Warnings...)
	scripts := make([]any, 0, len(result.Scripts)+1)
	for _, sc := range result.Scripts {
		if sc != nil {
			scripts = append(scripts, sc)
		}
	}
	var httpScript *dsl.Script
	var httpSections []importreview.ScriptImportReviewSection
	// Correlations are the HTTP output's: browser replay does not correlate.
	var httpCorrelations any
	if req.AlsoCreateHTTPScript {
		generated, err := s.CreateHTTPScriptFromHARAtLevel(artifact, info, req.Name, req.HTTPScriptLevel, BrowserHTTPScriptOptions{AsyncScan: req.AsyncScan, RecordingProfileID: req.RecordingProfileID, HTTPVersionMode: req.HTTPVersionMode})
		if err != nil {
			warnings = append(warnings, "HTTP script generation skipped: "+err.Error())
		} else if generated.Script != nil {
			httpScript, httpSections, httpCorrelations = generated.Script, generated.Sections, generated.Correlations
			scripts = append(scripts, httpScript)
			warnings = append(warnings, generated.Warnings...)
		}
	}
	response := s.importResponse(r, importreview.ScriptImportResponseOptions{
		Script:           result.Script,
		Scripts:          scripts,
		Warnings:         warnings,
		Stats:            result.Stats,
		Correlations:     httpCorrelations,
		ReviewSections:   httpSections,
		Parameterization: importreview.BuildImportParameterization(result.Script, nil),
	})
	if httpScript != nil {
		response["http_script"] = httpScript
	}
	s.writeImportResponse(w, http.StatusOK, response)
}

func (s *Service) HandleVerifyBrowserRecording(w http.ResponseWriter, r *http.Request) {
	if !s.requireExecutionLeader(w) {
		return
	}
	var req BrowserRecordingVerifyRequest
	if !s.decodeOptionalJSONBody(w, r, &req) {
		return
	}
	if !s.validHTTPVersionMode(w, req.Conversion.HTTPVersionMode) {
		return
	}
	if err := ValidateExternalReplaySessionID(req.ReplaySessionID); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id := r.PathValue("id")
	artifact, info, ok := s.Recording.Artifact(id)
	if !ok {
		s.writeError(w, http.StatusNotFound, "browser recording session not found")
		return
	}
	if artifact == nil {
		s.writeError(w, http.StatusConflict, "browser recording has no captured session yet")
		return
	}
	if info.Status != BrowserRecordingStopped {
		s.writeError(w, http.StatusConflict, "stop the recording before verifying it")
		return
	}
	result := s.ConvertBrowserRecording(artifact, info, req.Conversion)
	scripts := append([]*dsl.Script(nil), result.Scripts...)
	if req.Conversion.AlsoCreateHTTPScript {
		generated, err := s.CreateHTTPScriptFromHARAtLevel(artifact, info, req.Conversion.Name, req.Conversion.HTTPScriptLevel, BrowserHTTPScriptOptions{AsyncScan: req.Conversion.AsyncScan, RecordingProfileID: req.Conversion.RecordingProfileID, HTTPVersionMode: req.Conversion.HTTPVersionMode})
		if err != nil {
			s.writeError(w, http.StatusUnprocessableEntity, "HTTP conversion failed: "+err.Error())
			return
		}
		result.Warnings = append(result.Warnings, generated.Warnings...)
		if generated.Script != nil {
			scripts = append(scripts, generated.Script)
		}
	}
	if req.Conversion.ExpectedScriptCount < 0 || req.ScriptIndex < 0 || req.ScriptIndex >= len(scripts) ||
		(req.Conversion.ExpectedScriptCount > 0 && req.Conversion.ExpectedScriptCount != len(scripts)) {
		s.writeError(w, http.StatusConflict, "conversion outputs changed; refresh the preview before verifying")
		return
	}
	if err := ValidateBrowserRecordingParameterizationSelections(req.Conversion.ParameterizationSelections, len(scripts)); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := ValidateBrowserRecordingSecretBindings(req.Conversion.SecretBindings, len(scripts)); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	script, err := ApplyBrowserRecordingParameterizationSelections(scripts[req.ScriptIndex], req.ScriptIndex, req.Conversion.ParameterizationSelections)
	if err != nil {
		s.writeError(w, http.StatusConflict, err.Error())
		return
	}
	script, err = ApplyBrowserRecordingSecretBindings(script, req.ScriptIndex, req.Conversion.SecretBindings)
	if err != nil {
		s.writeError(w, http.StatusConflict, err.Error())
		return
	}
	if script == nil || len(script.Actions)+len(script.Init)+len(script.Teardown) == 0 {
		s.writeError(w, http.StatusUnprocessableEntity, "the recording produced no replayable actions")
		return
	}
	if script.ID == "" {
		script.ID = uuid.New().String()
	}
	authoredDefinition := engine.BrowserOriginalAuthoredDefinition(script)
	if req.VisibleBrowser && strings.EqualFold(strings.TrimSpace(script.Protocol), "browser") {
		headless := false
		script = ApplyBrowserReplayOverrides(script, BrowserReplayStartRequest{Browser: &dsl.BrowserConfig{Headless: &headless}})
	}
	ignoreThink := true
	executionCtx, releaseExecutionCtx := s.executionContext()
	replay, replayErr := s.Replay.Start(script.ID, script, BrowserReplayStartRequest{
		BrowserAuthoredDefinition: authoredDefinition,
		GroupName:                 script.Name + " — verify",
		ReplaySessionID:           req.ReplaySessionID,
		IgnoreThinkTime:           &ignoreThink,
		ExecutionContext:          executionCtx,
		ReleaseExecutionContext:   releaseExecutionCtx,
	})
	if replayErr != nil {
		releaseExecutionCtx()
		s.writeReplayStartError(w, replayErr)
		return
	}
	s.audit(r, "browser_recording.verify", "success", "browser_recording", id, info.ProjectID,
		map[string]any{"replay_session_id": replay.ID, "actions": result.Stats.ConvertedActions})
	response := BrowserRecordingVerifyResponse{
		ReplaySessionID: replay.ID,
		ReplaySession:   replay,
		Warnings:        result.Warnings,
	}
	WriteJSON(w, http.StatusCreated, response)
}

package recordbridge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/har"
	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/convert/sample"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/internal/importreview"
	"github.com/jasonczech/apexload/pkg/recorder"
	recbrowser "github.com/jasonczech/apexload/pkg/recorder/browser"
	"github.com/jasonczech/apexload/pkg/recorder/marker"
	"github.com/jasonczech/apexload/pkg/recordingprofile"
	"net/http"
	"strconv"
	"strings"
)

type RecordingConversionRequest struct {
	Name                       string                                `json:"name"`
	Parameterize               bool                                  `json:"parameterize"`
	PageTitleChecks            *bool                                 `json:"page_title_checks,omitempty"`
	ResourceMode               string                                `json:"resource_mode,omitempty"`
	AsyncScan                  *bool                                 `json:"async_scan,omitempty"`
	RecordingProfileID         string                                `json:"recording_profile_id,omitempty"`
	ProfileID                  string                                `json:"profile_id,omitempty"`
	ConversionPolicy           string                                `json:"conversion_policy,omitempty"`
	CorrelationMode            string                                `json:"correlation_mode,omitempty"`
	IncludeAssertions          *bool                                 `json:"include_assertions,omitempty"`
	InferExtractors            *bool                                 `json:"infer_extractors,omitempty"`
	IncludeStaticAssets        *bool                                 `json:"include_static_assets,omitempty"`
	DisableSalesforce          *bool                                 `json:"disable_salesforce,omitempty"`
	SalesforceNative           *bool                                 `json:"salesforce_native,omitempty"`
	AgentforceTransform        *bool                                 `json:"agentforce_transform,omitempty"`
	ParameterizationSelections []paramize.Selection                  `json:"parameterization_selections,omitempty"`
	SecretBindings             []importreview.SecretBindingSelection `json:"secret_bindings,omitempty"`
	HTTPVersionMode            string                                `json:"http_version_mode,omitempty" enums:"upgrade,exact,inherit"`
}

type ComposedRecordingConversion struct {
	Script                   *dsl.Script
	Warnings                 []string
	Diagnostics              diag.Diagnostics
	Correlations             any
	PreApplied               []paramize.PreAppliedCorrelation
	Sections                 []importreview.ScriptImportReviewSection
	Preflight                *importreview.ReplayPreflight
	EffectiveSettings        *sample.EffectiveSettings
	Baseline                 []correlate.Observation
	ProtectedRequirements    []credentialpolicy.Requirement
	PreparedCredentialReview []importreview.SecretBindingRequirement
	CredentialReviewPrepared bool
}

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
	RecordingProfileID  string `json:"recording_profile_id,omitempty"`
	ProfileID           string `json:"profile_id,omitempty"`
	ConversionPolicy    string `json:"conversion_policy,omitempty"`
	CorrelationMode     string `json:"correlation_mode,omitempty"`
	ResourceMode        string `json:"resource_mode,omitempty"`
	PageTitleChecks     *bool  `json:"page_title_checks,omitempty"`
	IncludeAssertions   *bool  `json:"include_assertions,omitempty"`
	InferExtractors     *bool  `json:"infer_extractors,omitempty"`
	IncludeStaticAssets *bool  `json:"include_static_assets,omitempty"`
	DisableSalesforce   *bool  `json:"disable_salesforce,omitempty"`
	SalesforceNative    *bool  `json:"salesforce_native,omitempty"`
	AgentforceTransform *bool  `json:"agentforce_transform,omitempty"`
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
	ProfileID            string `json:"profile_id,omitempty"`
	ConversionPolicy     string `json:"conversion_policy,omitempty"`
	CorrelationMode      string `json:"correlation_mode,omitempty"`
	ResourceMode         string `json:"resource_mode,omitempty"`
	PageTitleChecks      *bool  `json:"page_title_checks,omitempty"`
	IncludeAssertions    *bool  `json:"include_assertions,omitempty"`
	InferExtractors      *bool  `json:"infer_extractors,omitempty"`
	IncludeStaticAssets  *bool  `json:"include_static_assets,omitempty"`
	DisableSalesforce    *bool  `json:"disable_salesforce,omitempty"`
	SalesforceNative     *bool  `json:"salesforce_native,omitempty"`
	AgentforceTransform  *bool  `json:"agentforce_transform,omitempty"`
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
	AsyncScan                     *bool
	RecordingProfileID            string
	ConversionPolicy              string
	CorrelationMode               string
	ResourceMode                  string
	PageTitleChecks               *bool
	IncludeAssertions             *bool
	InferExtractors               *bool
	IncludeStaticAssets           *bool
	DisableSalesforce             *bool
	SalesforceNative              *bool
	AgentforceTransform           *bool
	HTTPVersionMode               string
	Parameterize                  bool
	HasParameterizationSelections bool
}

type BrowserHTTPScript struct {
	Script                *dsl.Script
	Warnings              []string
	Sections              []importreview.ScriptImportReviewSection
	Correlations          any
	PreApplied            []paramize.PreAppliedCorrelation
	Preflight             *importreview.ReplayPreflight
	EffectiveSettings     *sample.EffectiveSettings
	ProtectedRequirements []credentialpolicy.Requirement
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
			if trimmed := strings.TrimSpace(id); trimmed != "" {
				selected[trimmed] = true
			}
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
	if settings, ok := sample.ReadEffectiveSettings(applied); ok && settings != nil && !settings.Parameterize {
		settings.Parameterize = true
		sample.AttachEffectiveSettings(applied, settings)
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

// RecordingBaselineSnapshotDigestWithBaseline binds a preview to the recording
// snapshot it converted and to the option-resolved baseline it attests. A nil
// baseline yields RecordingBaselineSnapshotDigest, the default-options digest.
func RecordingBaselineSnapshotDigestWithBaseline(session *recorder.Session, baseline *importreview.RecordingBaseline) (string, error) {
	if baseline == nil {
		return RecordingBaselineSnapshotDigest(session)
	}
	var sessionRequests []recorder.RecordedRequest
	var sessionMarkers []marker.Marker
	if session != nil {
		sessionRequests = session.Requests
		sessionMarkers = session.Markers
	}
	payload, err := json.Marshal(struct {
		Requests []recorder.RecordedRequest      `json:"requests"`
		Markers  []marker.Marker                 `json:"markers"`
		Baseline *importreview.RecordingBaseline `json:"baseline"`
	}{
		Requests: sessionRequests,
		Markers:  sessionMarkers,
		Baseline: baseline,
	})
	if err != nil {
		return "", fmt.Errorf("marshal recording baseline snapshot: %w", err)
	}
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func sampleCorrelationsToPreApplied(correlations []sample.Correlation) []paramize.PreAppliedCorrelation {
	if len(correlations) == 0 {
		return nil
	}
	out := make([]paramize.PreAppliedCorrelation, 0, len(correlations))
	for _, c := range correlations {
		if !c.PreApplied {
			continue
		}
		out = append(out, paramize.PreAppliedCorrelation{
			Name:        c.Name,
			Source:      c.Source,
			Expression:  c.Expression,
			ExtractType: c.ExtractType,
			FoundInStep: c.FoundInStep,
			UsedInSteps: c.UsedInSteps,
		})
	}
	return out
}

func ComposeRecordingSessionConversion(
	session *recorder.Session,
	profile *recordingprofile.Profile,
	req RecordingConversionRequest,
) (*ComposedRecordingConversion, *ConversionError) {
	if _, err := sample.ValidateConversionPolicy(req.ConversionPolicy); err != nil {
		return nil, &ConversionError{Status: http.StatusBadRequest, Message: err.Error()}
	}
	if _, err := sample.ValidateCorrelationMode(req.CorrelationMode); err != nil {
		return nil, &ConversionError{Status: http.StatusBadRequest, Message: err.Error()}
	}
	if _, err := sample.ValidateResourceMode(req.ResourceMode); err != nil {
		return nil, &ConversionError{Status: http.StatusBadRequest, Message: err.Error()}
	}
	if _, err := requestimport.NormalizeHTTPVersionMode(req.HTTPVersionMode); err != nil {
		return nil, &ConversionError{Status: http.StatusBadRequest, Message: err.Error()}
	}
	profileID := strings.TrimSpace(req.RecordingProfileID)
	if profileID == "" {
		profileID = strings.TrimSpace(req.ProfileID)
	}

	hasSelectedIDs := len(paramize.SelectedIDsForScript(req.ParameterizationSelections, 0)) > 0
	result := recorder.ConvertSessionWithOptions(session, req.Name, recorder.ConvertOptions{
		ConversionPolicy:    strings.TrimSpace(req.ConversionPolicy),
		CorrelationMode:     strings.TrimSpace(req.CorrelationMode),
		RecordingProfileID:  profileID,
		Parameterize:        req.Parameterize || hasSelectedIDs,
		DeferParameterize:   true,
		PageTitleChecks:     req.PageTitleChecks,
		ResourceMode:        strings.TrimSpace(req.ResourceMode),
		AsyncScan:           req.AsyncScan,
		IncludeAssertions:   req.IncludeAssertions,
		InferExtractors:     req.InferExtractors,
		IncludeStaticAssets: req.IncludeStaticAssets,
		DisableSalesforce:   req.DisableSalesforce,
		SalesforceNative:    req.SalesforceNative,
		AgentforceTransform: req.AgentforceTransform,
		Profile:             profile,
		HTTPVersionMode:     strings.TrimSpace(req.HTTPVersionMode),
	})
	protectedRequirements := result.ProtectedRequirements.Take()
	if result.Script != nil && session != nil {
		result.Script.ProjectID = importreview.NormalizeProjectID(session.ProjectID)
		importreview.EnsureCredentialPreservingImportMetadata(result.Script, "recorder")
	}
	correlations := any(result.Correlations)
	preApplied := sampleCorrelationsToPreApplied(result.SampleCorrelations)
	var sections []importreview.ScriptImportReviewSection
	var preflight *importreview.ReplayPreflight
	var preparedCredentialReview []importreview.SecretBindingRequirement
	var credentialReviewPrepared bool

	if len(result.HARData) > 0 && result.Script != nil {
		harResult := &har.ConvertResult{
			Document:          result.Document,
			Script:            result.Script,
			Warnings:          result.Warnings,
			Unsupported:       result.Diagnostics,
			Stats:             result.Stats,
			Correlations:      result.SampleCorrelations,
			EffectiveSettings: result.EffectiveSettings,
			Baseline:          result.Baseline,
		}
		agentforceTransform := false
		if result.EffectiveSettings != nil {
			agentforceTransform = result.EffectiveSettings.AgentforceTransform
		}
		var salesforceAugmentation importreview.SalesforceHARAugmentation
		if result.EffectiveSettings != nil && !result.EffectiveSettings.DisableSalesforce &&
			(result.EffectiveSettings.SalesforceNative || result.EffectiveSettings.AgentforceTransform) {
			projectID := ""
			if session != nil {
				projectID = session.ProjectID
			}
			salesforceAugmentation = importreview.AugmentHARImportWithSalesforceProfile(
				harResult,
				result.HARData,
				projectID,
				req.Name,
				"recorder",
				result.EffectiveSettings.SalesforceNative,
				agentforceTransform,
				true,
				profile,
			)
			result.Warnings = harResult.Warnings
			result.Diagnostics = harResult.Unsupported
			result.Baseline = harResult.Baseline
			if salesforceAugmentation.Detected {
				if salesforceAugmentation.Script != result.Script {
					preApplied = nil
				}
				result.Script = salesforceAugmentation.Script
				if session != nil {
					result.Script.ProjectID = importreview.NormalizeProjectID(session.ProjectID)
				}
				importreview.EnsureCredentialPreservingImportMetadata(result.Script, "recorder")
				sections = salesforceAugmentation.ReviewSections
				correlations = salesforceAugmentation.Correlations
			}
		}

		sessionName := ""
		sessionProjectID := ""
		if session != nil {
			sessionName = session.Name
			sessionProjectID = session.ProjectID
		}
		draft := importreview.ImportDraft{
			Source:           "recorder",
			SourceScriptName: sessionName,
			ProjectID:        sessionProjectID,
			Script:           result.Script,
			Unsupported:      result.Diagnostics,
			Sections:         sections,
			Warnings:         result.Warnings,
			BeforeFinalize: func(s *dsl.Script, warnings *[]string, unsupported *diag.Diagnostics) error {
				if (result.EffectiveSettings != nil && result.EffectiveSettings.ConversionPolicy == sample.ConversionPolicyUnifiedV1) || salesforceAugmentation.Detected {
					importreview.ApplyClientGeneratedUUIDs(s, result.HARData, harResult)
					importreview.ApplyClientTimestamps(s, result.HARData, harResult)
					result.Warnings = harResult.Warnings
					result.Diagnostics = harResult.Unsupported
					*warnings = result.Warnings
					*unsupported = result.Diagnostics
				}
				if result.EffectiveSettings != nil && result.EffectiveSettings.Parameterize && !hasSelectedIDs {
					var paramWarnings []string
					if result.EffectiveSettings.ConversionPolicy == sample.ConversionPolicyUnifiedV1 {
						paramWarnings = importreview.ApplyAutoParameterizationToScript(s)
					} else {
						paramWarnings = recorder.ParameterizeRecordingInputs(s, sessionName)
					}
					result.Warnings = append(result.Warnings, paramWarnings...)
					*warnings = result.Warnings
				}
				if hasSelectedIDs {
					if _, err := importreview.ApplyParameterizationSelectionsToScript(s, 0, req.ParameterizationSelections); err != nil {
						return err
					}
				}
				if len(req.SecretBindings) > 0 {
					var err error
					_, preparedCredentialReview, credentialReviewPrepared, err = importreview.ApplySecretBindingsToImportDraft(s, 0, req.SecretBindings, protectedRequirements)
					if err != nil {
						return err
					}
				}
				sample.AttachEffectiveSettings(s, result.EffectiveSettings)
				recorder.NormalizeScriptActionsForLineage(s)
				return nil
			},
		}
		augmented, err := draft.AugmentWithAgentforceTransformChecked(agentforceTransform)
		if err != nil {
			importreview.ClearCredentialRequirementValues(protectedRequirements)
			return nil, &ConversionError{Status: http.StatusBadRequest, Message: err.Error()}
		}
		recorder.NormalizeScriptActionsForLineage(result.Script)
		result.Warnings = augmented.Warnings
		sections = augmented.Sections
		preflight = augmented.Preflight
	} else if result.Script != nil {
		sample.AttachEffectiveSettings(result.Script, result.EffectiveSettings)
	}

	return &ComposedRecordingConversion{
		Script:                   result.Script,
		Warnings:                 result.Warnings,
		Diagnostics:              result.Diagnostics,
		Correlations:             correlations,
		PreApplied:               preApplied,
		Sections:                 sections,
		Preflight:                preflight,
		EffectiveSettings:        result.EffectiveSettings,
		Baseline:                 result.Baseline,
		ProtectedRequirements:    protectedRequirements,
		PreparedCredentialReview: preparedCredentialReview,
		CredentialReviewPrepared: credentialReviewPrepared,
	}, nil
}

func (s *Service) HandlePreviewRecordingConversion(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	session, ok := s.Recorder.GetSession(id)
	if !ok {
		s.writeError(w, http.StatusNotFound, "recording session not found")
		return
	}

	var req RecordingConversionRequest
	if !s.decodeOptionalJSONBody(w, r, &req) {
		return
	}
	if !s.validHTTPVersionMode(w, req.HTTPVersionMode) {
		return
	}
	profileID := strings.TrimSpace(req.RecordingProfileID)
	if profileID == "" {
		profileID = strings.TrimSpace(req.ProfileID)
	}
	profile, err := s.Conversion.SessionRecordingProfile(session, profileID)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	composed, convErr := ComposeRecordingSessionConversion(session, profile, req)
	if convErr != nil {
		s.writeError(w, convErr.Status, convErr.Message)
		return
	}
	defer importreview.ClearCredentialRequirementValues(composed.ProtectedRequirements)

	// Attest only the prefix persistence keeps, so the draft capability stays
	// bounded however large the recording is.
	baseline := importreview.NewRecordingBaseline(composed.Baseline)
	snapshotDigest, err := RecordingBaselineSnapshotDigestWithBaseline(session, baseline)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "failed to bind recording preview snapshot")
		return
	}
	s.writeImportResponseWithSourceSecrets(w, r, http.StatusOK, importreview.ScriptImportResponseOptions{
		Script:                   composed.Script,
		Warnings:                 composed.Warnings,
		Diagnostics:              composed.Diagnostics,
		UnsupportedElements:      composed.Diagnostics.Strings(),
		Correlations:             composed.Correlations,
		ReviewSections:           composed.Sections,
		Parameterization:         importreview.BuildImportParameterization(composed.Script, composed.PreApplied),
		Preflight:                composed.Preflight,
		EffectiveSettings:        composed.EffectiveSettings,
		Stats:                    RecordingConversionStats(session),
		RecordingSessionID:       id,
		RecordingSnapshotDigest:  snapshotDigest,
		RecordingBaseline:        baseline,
		ProtectedRequirements:    composed.ProtectedRequirements,
		PreparedCredentialReview: composed.PreparedCredentialReview,
		CredentialReviewPrepared: composed.CredentialReviewPrepared,
	})
}

func hasAnyBrowserRecordingParameterizationSelections(
	selections []BrowserRecordingParameterizationSelection,
) bool {
	for _, selection := range selections {
		for _, id := range selection.SelectedIDs {
			if strings.TrimSpace(id) != "" {
				return true
			}
		}
	}
	return false
}

func hasBrowserRecordingParameterizationSelectionsForScript(
	selections []BrowserRecordingParameterizationSelection,
	scriptIndex int,
) bool {
	for _, selection := range selections {
		if selection.ScriptIndex != scriptIndex {
			continue
		}
		for _, id := range selection.SelectedIDs {
			if strings.TrimSpace(id) != "" {
				return true
			}
		}
	}
	return false
}

func (req BrowserRecordingConvertRequest) HTTPScriptOptions() BrowserHTTPScriptOptions {
	profileID := strings.TrimSpace(req.RecordingProfileID)
	if profileID == "" {
		profileID = strings.TrimSpace(req.ProfileID)
	}
	return BrowserHTTPScriptOptions{
		AsyncScan:                     req.AsyncScan,
		RecordingProfileID:            profileID,
		ConversionPolicy:              req.ConversionPolicy,
		CorrelationMode:               req.CorrelationMode,
		ResourceMode:                  req.ResourceMode,
		PageTitleChecks:               req.PageTitleChecks,
		IncludeAssertions:             req.IncludeAssertions,
		InferExtractors:               req.InferExtractors,
		IncludeStaticAssets:           req.IncludeStaticAssets,
		DisableSalesforce:             req.DisableSalesforce,
		SalesforceNative:              req.SalesforceNative,
		AgentforceTransform:           req.AgentforceTransform,
		HTTPVersionMode:               req.HTTPVersionMode,
		Parameterize:                  req.Parameterize,
		HasParameterizationSelections: hasAnyBrowserRecordingParameterizationSelections(req.ParameterizationSelections),
	}
}

func (req BrowserRecordingConvertRequest) HTTPScriptOptionsForIndex(scriptIndex int) BrowserHTTPScriptOptions {
	opts := req.HTTPScriptOptions()
	if scriptIndex >= 0 {
		opts.HasParameterizationSelections = hasBrowserRecordingParameterizationSelectionsForScript(
			req.ParameterizationSelections,
			scriptIndex,
		)
	}
	return opts
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
		ProfileID:            req.ProfileID,
		ConversionPolicy:     req.ConversionPolicy,
		CorrelationMode:      req.CorrelationMode,
		ResourceMode:         req.ResourceMode,
		PageTitleChecks:      req.PageTitleChecks,
		IncludeAssertions:    req.IncludeAssertions,
		InferExtractors:      req.InferExtractors,
		IncludeStaticAssets:  req.IncludeStaticAssets,
		DisableSalesforce:    req.DisableSalesforce,
		SalesforceNative:     req.SalesforceNative,
		AgentforceTransform:  req.AgentforceTransform,
		HTTPVersionMode:      req.HTTPVersionMode,
		Parameterize:         req.Parameterize,
	}
}

func (s *Service) validBrowserPolicyOptions(w http.ResponseWriter, req BrowserRecordingConvertRequest) bool {
	if _, err := sample.ValidateConversionPolicy(req.ConversionPolicy); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return false
	}
	if _, err := sample.ValidateCorrelationMode(req.CorrelationMode); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return false
	}
	if _, err := sample.ValidateResourceMode(req.ResourceMode); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return false
	}
	return true
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
	for scriptIndex, sc := range result.Scripts {
		if sc == nil {
			continue
		}
		sc.ProjectID = projectID
		AttachRecordingProvenance(sc, artifact, info)
		if req.Parameterize && !hasBrowserRecordingParameterizationSelectionsForScript(req.ParameterizationSelections, scriptIndex) {
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
	if _, err := sample.ValidateConversionPolicy(scriptOptions.ConversionPolicy); err != nil {
		return BrowserHTTPScript{}, err
	}
	if _, err := sample.ValidateCorrelationMode(scriptOptions.CorrelationMode); err != nil {
		return BrowserHTTPScript{}, err
	}
	if _, err := sample.ValidateResourceMode(scriptOptions.ResourceMode); err != nil {
		return BrowserHTTPScript{}, err
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
	options := har.ConvertOptions{
		ScriptName:          httpName,
		RecordingProfileID:  strings.TrimSpace(scriptOptions.RecordingProfileID),
		ConversionPolicy:    strings.TrimSpace(scriptOptions.ConversionPolicy),
		CorrelationMode:     strings.TrimSpace(scriptOptions.CorrelationMode),
		ResourceMode:        strings.TrimSpace(scriptOptions.ResourceMode),
		PageTitleChecks:     scriptOptions.PageTitleChecks,
		IncludeAssertions:   scriptOptions.IncludeAssertions,
		InferExtractors:     scriptOptions.InferExtractors,
		IncludeStaticAssets: scriptOptions.IncludeStaticAssets,
		DisableSalesforce:   scriptOptions.DisableSalesforce,
		SalesforceNative:    scriptOptions.SalesforceNative,
		AgentforceTransform: scriptOptions.AgentforceTransform,
		AsyncScan:           scriptOptions.AsyncScan,
		Profile:             profile,
		EntryPoint:          sample.EntryPointBrowserHTTP,
		HTTPVersionMode:     scriptOptions.HTTPVersionMode,
		Parameterize:        scriptOptions.Parameterize || scriptOptions.HasParameterizationSelections,
	}
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
	protectedRequirements := result.ProtectedRequirements.Take()
	result.Warnings = append(result.Warnings, levelNotes...)
	if result.Script != nil {
		result.Script.ProjectID = importreview.NormalizeProjectID(info.ProjectID)
		result.Script.GeneratedBy = "browser-recorder"
		result.Script.GeneratedFrom = "browser_recording_har"
	}

	salesforceNative := true
	agentforceTransform := false
	disableSalesforce := false
	if result.EffectiveSettings != nil {
		salesforceNative = result.EffectiveSettings.SalesforceNative
		agentforceTransform = result.EffectiveSettings.AgentforceTransform
		disableSalesforce = result.EffectiveSettings.DisableSalesforce
	}
	source := "browser_recording_har"
	sections := importreview.HarImportReviewSections(result)
	correlations := any(result.Correlations)
	preApplied := sampleCorrelationsToPreApplied(result.Correlations)
	// Use the same native rewrite and review as a direct HAR import. Merely
	// attaching its review leaves the executable script with recorded Aura state.
	if !disableSalesforce {
		salesforceDraft := importreview.AugmentHARImportWithSalesforceProfile(
			result,
			artifact.HAR,
			importreview.NormalizeProjectID(info.ProjectID),
			httpName,
			"browser_recording_har",
			salesforceNative,
			agentforceTransform,
			true,
			profile,
		)
		if salesforceDraft.Detected {
			if salesforceDraft.Script != result.Script {
				preApplied = nil
			}
			result.Script = salesforceDraft.Script
			source = salesforceDraft.Source
			sections = salesforceDraft.ReviewSections
			correlations = salesforceDraft.Correlations
		}
	}

	// Run the shared augmentation over the generated HTTP draft and hand its
	// sections back. Before this the detectors ran here with their return value
	// discarded, so an Anypoint or Agentforce finding was preserved on the
	// script and never reached the operator.
	// The HAR converter's source review is deliberately not attached here. It
	// embeds a scrubbed copy of the capture, and this draft is persisted as a
	// credential-preserving script whose recorded values must survive verbatim —
	// the recorder already keeps the capture itself as the script's baseline.
	draft := importreview.ImportDraft{
		Source:           source,
		SourceScriptName: httpName,
		ProjectID:        info.ProjectID,
		Script:           result.Script,
		Unsupported:      result.Unsupported,
		Sections:         sections,
		Warnings:         result.Warnings,
		BeforeFinalize: func(s *dsl.Script, warnings *[]string, unsupported *diag.Diagnostics) error {
			importreview.ApplyClientGeneratedUUIDs(s, artifact.HAR, result)
			importreview.ApplyClientTimestamps(s, artifact.HAR, result)
			if result.EffectiveSettings != nil && result.EffectiveSettings.Parameterize && !scriptOptions.HasParameterizationSelections {
				result.Warnings = append(result.Warnings, importreview.ApplyAutoParameterizationToScript(s)...)
			}
			*warnings = result.Warnings
			*unsupported = result.Unsupported
			sample.AttachEffectiveSettings(s, result.EffectiveSettings)
			importreview.EnsureCredentialPreservingImportMetadata(s, "browser_recording_har")
			return nil
		},
	}
	augmented, err := draft.AugmentWithAgentforceTransformChecked(agentforceTransform)
	if err != nil {
		importreview.ClearCredentialRequirementValues(protectedRequirements)
		return BrowserHTTPScript{}, err
	}
	recbrowser.AttachRecordingEnvironment(result.Script, artifact.RecordingEnvironment)
	return BrowserHTTPScript{
		Script:                result.Script,
		Warnings:              augmented.Warnings,
		Sections:              augmented.Sections,
		Correlations:          correlations,
		PreApplied:            preApplied,
		Preflight:             augmented.Preflight,
		EffectiveSettings:     result.EffectiveSettings,
		ProtectedRequirements: protectedRequirements,
		Baseline:              result.Baseline,
	}, nil
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
	if !s.validBrowserPolicyOptions(w, req) {
		return
	}

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
	var generated BrowserHTTPScript
	httpScriptIndex := len(scripts)
	if req.AlsoCreateHTTPScript {
		var err error
		generated, err = s.CreateHTTPScriptFromHARAtLevel(artifact, info, req.Name, req.HTTPScriptLevel, req.HTTPScriptOptions())
		if err != nil {
			warnings = append(warnings, "HTTP script generation skipped: "+err.Error())
		} else if generated.Script != nil {
			httpScript, httpSections, httpCorrelations = generated.Script, generated.Sections, generated.Correlations
			scripts = append(scripts, httpScript)
			warnings = append(warnings, generated.Warnings...)
		}
	}
	defer importreview.ClearCredentialRequirementValues(generated.ProtectedRequirements)

	browserParam := importreview.BuildImportParameterization(result.Script, nil)
	combinedParam := browserParam
	var httpParam *paramize.Result
	if httpScript != nil {
		httpParam = importreview.BuildImportParameterization(httpScript, generated.PreApplied)
		if httpParam != nil && len(httpParam.Suggestions) > 0 {
			merged := make([]paramize.Suggestion, 0, len(browserParam.Suggestions)+len(httpParam.Suggestions))
			for _, sug := range browserParam.Suggestions {
				if sug.ScriptName == "" && result.Script != nil {
					sug.ScriptName = result.Script.Name
				}
				merged = append(merged, sug)
			}
			for _, sug := range httpParam.Suggestions {
				if sug.ScriptName == "" {
					sug.ScriptName = httpScript.Name
				}
				merged = append(merged, sug)
			}
			combinedParam = &paramize.Result{
				Suggestions: merged,
				Summary:     importreview.ParamizeSummary(merged),
				Warnings:    append(append([]string(nil), browserParam.Warnings...), httpParam.Warnings...),
				AuraPayload: httpParam.AuraPayload,
			}
		}
	}

	response := s.importResponse(r, importreview.ScriptImportResponseOptions{
		Script:                result.Script,
		Scripts:               scripts,
		Warnings:              warnings,
		Stats:                 result.Stats,
		Correlations:          httpCorrelations,
		ReviewSections:        httpSections,
		Parameterization:      combinedParam,
		EffectiveSettings:     generated.EffectiveSettings,
		ProtectedRequirements: generated.ProtectedRequirements,
	})
	if httpScript != nil {
		if responseScripts, ok := response["scripts"].([]any); ok && httpScriptIndex < len(responseScripts) {
			response["http_script"] = responseScripts[httpScriptIndex]
		} else {
			response["http_script"] = importreview.MaskScriptSecrets(httpScript)
		}
		response["http_parameterization"] = importreview.FilterTrustedImportParameterization(httpParam, []*dsl.Script{httpScript})
		if generated.Preflight != nil {
			response["http_preflight"] = generated.Preflight
		}
		if allReqs, ok := response["secret_binding_requirements"].([]importreview.SecretBindingRequirement); ok {
			httpReqs := make([]importreview.SecretBindingRequirement, 0)
			for _, reqItem := range allReqs {
				if reqItem.ScriptIndex == httpScriptIndex {
					httpReqs = append(httpReqs, reqItem)
				}
			}
			response["http_secret_binding_requirements"] = httpReqs
		}
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
	if !s.validHTTPVersionMode(w, req.Conversion.HTTPVersionMode) || !s.validBrowserPolicyOptions(w, req.Conversion) {
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
		generated, err := s.CreateHTTPScriptFromHARAtLevel(artifact, info, req.Conversion.Name, req.Conversion.HTTPScriptLevel, req.Conversion.HTTPScriptOptionsForIndex(len(result.Scripts)))
		if err != nil {
			s.writeError(w, http.StatusUnprocessableEntity, "HTTP conversion failed: "+err.Error())
			return
		}
		defer importreview.ClearCredentialRequirementValues(generated.ProtectedRequirements)
		result.Warnings = append(result.Warnings, generated.Warnings...)
		if generated.Script != nil {
			if len(generated.ProtectedRequirements) > 0 {
				reqs := importreview.MergeReferencedImportCredentialRequirements([]*dsl.Script{generated.Script}, generated.ProtectedRequirements)
				if len(reqs) > 0 {
					_, _ = importreview.AttachImportCredentialReview([]*dsl.Script{generated.Script}, reqs)
					importreview.ClearCredentialRequirementValues(reqs)
				}
			}
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

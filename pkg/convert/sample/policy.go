package sample

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/recordingprofile"
)

const (
	ConversionPolicyLegacy    = "legacy"
	ConversionPolicyUnifiedV1 = "unified_v1"

	CorrelationModeAuto           = "auto"
	CorrelationModeHighConfidence = "high_confidence"
	CorrelationModeAll            = "all"
	CorrelationModeReviewOnly     = "review_only"
	CorrelationModeDisabled       = "disabled"

	EntryPointImportHAR        = "import_har"
	EntryPointImportSalesforce = "import_salesforce"
	EntryPointImportSample     = "import_sample"
	EntryPointRecorderConvert  = "recorder_convert"
	EntryPointBrowserHTTP      = "browser_http"

	PreservedEffectiveSettingsKey = "effective_settings"
)

// EffectiveSettings records the resolved conversion policy and effective
// authoring options applied to produce a script. It is returned on conversion
// responses and persisted in script.Metadata.Import.Preserved["effective_settings"].
type EffectiveSettings struct {
	ConversionPolicy       string   `json:"conversion_policy"`
	CorrelationMode        string   `json:"correlation_mode"`
	HighConfidenceOnly     bool     `json:"high_confidence_only"`
	ReviewOnlyCorrelations bool     `json:"review_only_correlations,omitempty"`
	IncludeAssertions      bool     `json:"include_assertions"`
	InferExtractors        bool     `json:"infer_extractors"`
	PageTitleChecks        bool     `json:"page_title_checks"`
	ResourceMode           string   `json:"resource_mode"`
	AsyncScan              bool     `json:"async_scan"`
	HTTPVersionMode        string   `json:"http_version_mode"`
	DisableSalesforce      bool     `json:"disable_salesforce,omitempty"`
	SalesforceNative       bool     `json:"salesforce_native"`
	AgentforceTransform    bool     `json:"agentforce_transform"`
	Parameterize           bool     `json:"parameterize,omitempty"`
	RecordingProfileID     string   `json:"recording_profile_id,omitempty"`
	RecordingProfileName   string   `json:"recording_profile_name,omitempty"`
	SourceEntryPoint       string   `json:"source_entry_point,omitempty"`
	CapabilityNotes        []string `json:"capability_notes,omitempty"`
	// NativeHighConfidenceOnly is the automatic-rewrite threshold for native
	// Salesforce analysis. Native rewrites have always applied only
	// high-confidence correlations, so this stays true unless the caller
	// explicitly asked for every correlation (correlation_mode "all"). It is
	// resolved per conversion and not persisted.
	NativeHighConfidenceOnly bool `json:"-"`
}

// PolicyResolutionInput collects raw conversion flags from any of the five HTTP
// authoring entry points together with an optional project recording profile.
type PolicyResolutionInput struct {
	EntryPoint             string
	Format                 string
	ConversionPolicy       string
	CorrelationMode        string
	IncludeAssertions      *bool
	InferExtractors        *bool
	IncludeStaticAssets    *bool
	PageTitleChecks        *bool
	ResourceMode           string
	AsyncScan              *bool
	HTTPVersionMode        string
	HighConfidenceOnly     *bool
	ReviewOnlyCorrelations bool
	DisableSalesforce      *bool
	SalesforceNative       *bool
	AgentforceTransform    *bool
	Parameterize           bool
	RecordingProfileID     string
	Profile                *recordingprofile.Profile
	CapabilityNotes        []string
}

// ValidateConversionPolicy normalizes and validates a conversion_policy value.
// An empty string resolves to ConversionPolicyLegacy ("legacy").
func ValidateConversionPolicy(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", ConversionPolicyLegacy:
		return ConversionPolicyLegacy, nil
	case ConversionPolicyUnifiedV1:
		return ConversionPolicyUnifiedV1, nil
	default:
		return "", fmt.Errorf("invalid conversion_policy %q: must be %q or %q", raw, ConversionPolicyLegacy, ConversionPolicyUnifiedV1)
	}
}

// ValidateCorrelationMode normalizes and validates a correlation_mode value.
// An empty string resolves to CorrelationModeAuto ("auto").
func ValidateCorrelationMode(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", CorrelationModeAuto:
		return CorrelationModeAuto, nil
	case CorrelationModeHighConfidence:
		return CorrelationModeHighConfidence, nil
	case CorrelationModeAll:
		return CorrelationModeAll, nil
	case CorrelationModeReviewOnly:
		return CorrelationModeReviewOnly, nil
	case CorrelationModeDisabled:
		return CorrelationModeDisabled, nil
	default:
		return "", fmt.Errorf("invalid correlation_mode %q: must be one of auto, high_confidence, all, review_only, disabled", raw)
	}
}

// ValidateResourceMode normalizes and validates a resource_mode value.
func ValidateResourceMode(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return "", nil
	case "runtime", "in_step", "separate":
		return strings.ToLower(strings.TrimSpace(raw)), nil
	default:
		return "", fmt.Errorf("invalid resource_mode %q: must be runtime, in_step, or separate", raw)
	}
}

// ResolveConversionPolicy resolves explicit request flags, project recording
// profile settings, and conversion policy defaults according to the canonical
// precedence order: Explicit Request Flag > Project Recording Profile > Policy Default.
func ResolveConversionPolicy(input PolicyResolutionInput) (*EffectiveSettings, error) {
	policy, err := ValidateConversionPolicy(input.ConversionPolicy)
	if err != nil {
		return nil, err
	}
	corrMode, err := ValidateCorrelationMode(input.CorrelationMode)
	if err != nil {
		return nil, err
	}
	resourceMode, err := ValidateResourceMode(input.ResourceMode)
	if err != nil {
		return nil, err
	}
	httpVersionMode, err := requestimport.NormalizeHTTPVersionMode(input.HTTPVersionMode)
	if err != nil {
		return nil, err
	}
	if httpVersionMode == "" {
		httpVersionMode = requestimport.HTTPVersionModeUpgrade
	}

	format := normalizeFormat(input.Format)
	if format == "" {
		format = "har"
	}

	pageTitleChecksPtr := input.PageTitleChecks
	asyncScanPtr := input.AsyncScan
	if profile := input.Profile; profile != nil {
		if pageTitleChecksPtr == nil {
			pageTitleChecksPtr = profile.PageTitleChecks
		}
		if resourceMode == "" && input.IncludeStaticAssets == nil && profile.ResourceMode != "" {
			resourceMode = strings.ToLower(strings.TrimSpace(profile.ResourceMode))
		}
		if asyncScanPtr == nil {
			asyncScanPtr = profile.AsyncScan
		}
	}

	includeAssertions := convertutil.BoolOption(input.IncludeAssertions, true)
	pageTitleChecks := includeAssertions && convertutil.BoolOption(pageTitleChecksPtr, true)
	if resourceMode == "" {
		if convertutil.BoolOption(input.IncludeStaticAssets, false) {
			resourceMode = "separate"
		} else {
			resourceMode = "runtime"
		}
	}
	asyncScan := format == "har" && convertutil.BoolOption(asyncScanPtr, true)

	inferExtractors := convertutil.BoolOption(input.InferExtractors, true)
	reviewOnly := input.ReviewOnlyCorrelations
	highConfidenceOnly := policy == ConversionPolicyUnifiedV1 || input.EntryPoint == EntryPointRecorderConvert
	if input.HighConfidenceOnly != nil {
		highConfidenceOnly = *input.HighConfidenceOnly
	}
	// Native Salesforce rewrites keep their historical high-confidence
	// threshold whatever the generic HAR default is; only an explicit
	// "all" (or an explicit caller threshold) widens them.
	nativeHighConfidenceOnly := corrMode != CorrelationModeAll
	if input.HighConfidenceOnly != nil && corrMode == CorrelationModeAuto {
		nativeHighConfidenceOnly = *input.HighConfidenceOnly
	}

	switch corrMode {
	case CorrelationModeDisabled:
		inferExtractors = false
		highConfidenceOnly = false
		reviewOnly = false
	case CorrelationModeReviewOnly:
		reviewOnly = true
		highConfidenceOnly = false
	case CorrelationModeHighConfidence:
		highConfidenceOnly = true
		reviewOnly = false
	case CorrelationModeAll:
		highConfidenceOnly = false
		reviewOnly = false
	}

	effectiveCorrMode := CorrelationModeAll
	switch {
	case !inferExtractors:
		effectiveCorrMode = CorrelationModeDisabled
		highConfidenceOnly = false
		reviewOnly = false
	case input.Profile != nil && input.Profile.Correlation.Disabled && len(input.Profile.Correlation.UserRules) == 0:
		effectiveCorrMode = CorrelationModeDisabled
	case reviewOnly:
		effectiveCorrMode = CorrelationModeReviewOnly
	case highConfidenceOnly:
		effectiveCorrMode = CorrelationModeHighConfidence
	default:
		effectiveCorrMode = CorrelationModeAll
	}

	agentforceTransform := convertutil.BoolOption(input.AgentforceTransform, false)
	defaultSalesforce := false
	if format == "har" {
		switch {
		case policy == ConversionPolicyUnifiedV1:
			defaultSalesforce = true
		case input.EntryPoint == EntryPointImportHAR || input.EntryPoint == EntryPointImportSalesforce || input.EntryPoint == EntryPointBrowserHTTP:
			defaultSalesforce = true
		case agentforceTransform:
			defaultSalesforce = true
		}
	}
	disableSalesforce := input.DisableSalesforce != nil && *input.DisableSalesforce
	salesforceNative := defaultSalesforce
	if input.SalesforceNative != nil {
		salesforceNative = *input.SalesforceNative
	}
	if disableSalesforce {
		salesforceNative = false
	}
	if format != "har" {
		salesforceNative = false
		agentforceTransform = false
	}

	profileID := strings.TrimSpace(input.RecordingProfileID)
	profileName := ""
	if input.Profile != nil {
		if profileID == "" {
			profileID = input.Profile.ID
		}
		profileName = input.Profile.Name
	}

	var notes []string
	if len(input.CapabilityNotes) > 0 {
		notes = append(notes, input.CapabilityNotes...)
	}

	return &EffectiveSettings{
		ConversionPolicy:       policy,
		CorrelationMode:        effectiveCorrMode,
		HighConfidenceOnly:     highConfidenceOnly,
		ReviewOnlyCorrelations: reviewOnly,
		IncludeAssertions:      includeAssertions,
		InferExtractors:        inferExtractors,
		PageTitleChecks:        pageTitleChecks,
		ResourceMode:           resourceMode,
		AsyncScan:              asyncScan,
		HTTPVersionMode:        httpVersionMode,
		DisableSalesforce:      disableSalesforce,
		SalesforceNative:       salesforceNative,
		AgentforceTransform:    agentforceTransform,
		Parameterize:           input.Parameterize,
		RecordingProfileID:     profileID,
		RecordingProfileName:   profileName,
		SourceEntryPoint:       input.EntryPoint,
		CapabilityNotes:        notes,

		NativeHighConfidenceOnly: nativeHighConfidenceOnly,
	}, nil
}

// AttachEffectiveSettings persists EffectiveSettings onto script.Metadata.Import.Preserved["effective_settings"]
// using its JSON-normalized map representation so in-memory and round-tripped scripts match.
func AttachEffectiveSettings(script *dsl.Script, settings *EffectiveSettings) {
	if script == nil || settings == nil {
		return
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		return
	}
	var normalized map[string]any
	if err := json.Unmarshal(encoded, &normalized); err != nil {
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
	script.Metadata.Import.Preserved[PreservedEffectiveSettingsKey] = normalized
}

// ReadEffectiveSettings extracts EffectiveSettings from script.Metadata.Import.Preserved["effective_settings"].
func ReadEffectiveSettings(script *dsl.Script) (*EffectiveSettings, bool) {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil || script.Metadata.Import.Preserved == nil {
		return nil, false
	}
	raw, ok := script.Metadata.Import.Preserved[PreservedEffectiveSettingsKey]
	if !ok || raw == nil {
		return nil, false
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, false
	}
	var settings EffectiveSettings
	if err := json.Unmarshal(encoded, &settings); err != nil {
		return nil, false
	}
	return &settings, true
}

// HasNonDefaultRecordingConversionSettings reports whether settings differ from
// the legacy default proxy-recorder conversion options in a way that could
// change the converted action graph or baseline observation indexing.
func HasNonDefaultRecordingConversionSettings(settings *EffectiveSettings) bool {
	if settings == nil {
		return false
	}
	if settings.ConversionPolicy == ConversionPolicyUnifiedV1 {
		return true
	}
	if settings.RecordingProfileID != "" {
		return true
	}
	if settings.ResourceMode != "" && settings.ResourceMode != "runtime" {
		return true
	}
	if !settings.AsyncScan {
		return true
	}
	if settings.SalesforceNative || settings.AgentforceTransform || settings.Parameterize {
		return true
	}
	if settings.CorrelationMode != "" && settings.CorrelationMode != CorrelationModeHighConfidence && settings.CorrelationMode != CorrelationModeAuto {
		return true
	}
	if !settings.IncludeAssertions || !settings.InferExtractors || !settings.PageTitleChecks {
		return true
	}
	if settings.HTTPVersionMode != "" && settings.HTTPVersionMode != requestimport.HTTPVersionModeUpgrade {
		return true
	}
	if !settings.HighConfidenceOnly || settings.ReviewOnlyCorrelations {
		return true
	}
	return false
}

package har

import (
	"fmt"
	"github.com/jasonczech/apexload/pkg/convert/convertresult"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/sample"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/scrub"
)

const (
	ArtifactModeDesignHAR        = "design_har"
	ArtifactModeDesignHARPair    = "design_har_pair"
	ArtifactModeRuntimeCanaryHAR = "runtime_canary_har"

	DefaultRetentionDays = 7
)

type ConvertOptions struct {
	ScriptName          string `json:"script_name,omitempty"`
	BaseURL             string `json:"base_url,omitempty"`
	ArtifactMode        string `json:"artifact_mode,omitempty"`
	RetentionDays       int    `json:"retention_days,omitempty"`
	RedactSensitive     *bool  `json:"redact_sensitive,omitempty"`
	IncludeAssertions   *bool  `json:"include_assertions,omitempty"`
	InferExtractors     *bool  `json:"infer_extractors,omitempty"`
	SanitizeSensitive   *bool  `json:"sanitize_sensitive,omitempty"`
	IncludeStaticAssets *bool  `json:"include_static_assets,omitempty"`
	// PageTitleChecks adds a check on each HTML page's recorded <title>
	// (default on); ResourceMode is runtime (default), in_step or separate.
	PageTitleChecks    *bool  `json:"page_title_checks,omitempty"`
	ResourceMode       string `json:"resource_mode,omitempty"`
	DefaultThinkTimeMs int    `json:"default_think_time_ms,omitempty"`
	// HTMLSteps (set by the browser recorder) turns the requests of the given
	// 1-based HAR entries into HTML-mode steps.
	HTMLSteps map[int]map[string]any `json:"-"`
	// SalvageTruncated imports the complete entries of a HAR file that was cut
	// off partway through (see SalvageTruncated); the import warns what is lost.
	SalvageTruncated *bool `json:"salvage_truncated,omitempty"`
}

type ArtifactGovernance struct {
	ArtifactMode        string   `json:"artifact_mode"`
	Sensitivity         string   `json:"sensitivity"`
	RetentionDays       int      `json:"retention_days"`
	RedactionEnabled    bool     `json:"redaction_enabled"`
	BodyCaptureRequired bool     `json:"body_capture_required"`
	Scope               string   `json:"scope"`
	Warnings            []string `json:"warnings,omitempty"`
}

type ConvertResult struct {
	Script       *dsl.Script          `json:"script"`
	Warnings     []string             `json:"warnings,omitempty"`
	Unsupported  diag.Diagnostics     `json:"unsupported,omitempty"`
	Stats        sample.ConvertStats  `json:"stats"`
	Correlations []sample.Correlation `json:"correlations,omitempty"`
	SourceReview *sourceview.Review   `json:"source_review,omitempty"`
	ArtifactMode string               `json:"artifact_mode,omitempty"`
	Governance   *ArtifactGovernance  `json:"governance,omitempty"`
	Diff         *DiffReport          `json:"diff,omitempty"`

	// ProtectedRequirements transfers the sample adapter's source values to
	// HAR API workflows without placing them in the public conversion payload.
	ProtectedRequirements credentialpolicy.ProtectedRequirements `json:"-"`
}

// CorrelationBaseline parses an imported HAR with the same filtering and
// transport decoding used by Convert, but keeps the raw request/response
// observations for the server's encrypted correlation-baseline store.
func CorrelationBaseline(data []byte, includeStaticAssets bool) ([]correlate.Observation, error) {
	if len(data) == 0 || strings.TrimSpace(string(data)) == "" {
		return nil, fmt.Errorf("HAR content is required")
	}
	return sample.HARCorrelationBaseline(string(data), includeStaticAssets)
}

func Convert(data []byte, opts ConvertOptions) (*ConvertResult, error) {
	if len(data) == 0 || strings.TrimSpace(string(data)) == "" {
		return nil, fmt.Errorf("HAR content is required")
	}
	artifactMode := normalizeArtifactMode(opts.ArtifactMode)
	retentionDays := normalizeRetentionDays(opts.RetentionDays)
	redactionEnabled := false
	governanceWarnings := []string{}
	sensitivity := scrub.SensitivityRaw
	// Correlation detection and its review samples retain the imported values.
	// Run and debug output are responsible for masking values they emit.
	result, err := sample.Convert(sample.ImportRequest{
		Name:    convertutil.FirstNonEmpty(opts.ScriptName, "Imported_HAR_Script"),
		Format:  "har",
		Content: string(data),
		Options: sample.ConvertOptions{
			BaseURL:             opts.BaseURL,
			IncludeAssertions:   opts.IncludeAssertions,
			InferExtractors:     opts.InferExtractors,
			SanitizeSensitive:   boolPointer(false),
			IncludeStaticAssets: opts.IncludeStaticAssets,
			PageTitleChecks:     opts.PageTitleChecks,
			ResourceMode:        opts.ResourceMode,
			HTMLSteps:           opts.HTMLSteps,
		},
	})
	if err != nil {
		return nil, err
	}
	warnings := append([]string{}, result.Warnings...)
	warnings = append(warnings, governanceWarnings...)
	if result.Script != nil {
		result.Script.GeneratedBy = "apexload-har-converter"
		result.Script.GeneratedFrom = "har"
		if opts.DefaultThinkTimeMs > 0 {
			result.Script.Config.ThinkTime.Type = "fixed"
			result.Script.Config.ThinkTime.MeanMs = float64(opts.DefaultThinkTimeMs)
		}
	}
	converted := &ConvertResult{
		Script:       result.Script,
		Warnings:     warnings,
		Unsupported:  result.Unsupported,
		Stats:        result.Stats,
		Correlations: result.Correlations,
		SourceReview: result.SourceReview,
		ArtifactMode: artifactMode,
		Governance: &ArtifactGovernance{
			ArtifactMode:        artifactMode,
			Sensitivity:         sensitivity,
			RetentionDays:       retentionDays,
			RedactionEnabled:    redactionEnabled,
			BodyCaptureRequired: true,
			Scope:               "http_family_only",
			Warnings:            governanceWarnings,
		},
	}
	protectedRequirements := result.ProtectedRequirements.Take()
	converted.ProtectedRequirements.Store(protectedRequirements)
	for index := range protectedRequirements {
		protectedRequirements[index].Value = ""
	}
	return converted, nil
}

func boolPointer(value bool) *bool {
	return &value
}

func normalizeArtifactMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case ArtifactModeDesignHARPair:
		return ArtifactModeDesignHARPair
	case ArtifactModeRuntimeCanaryHAR:
		return ArtifactModeRuntimeCanaryHAR
	case ArtifactModeDesignHAR, "":
		return ArtifactModeDesignHAR
	default:
		return ArtifactModeDesignHAR
	}
}

func normalizeRetentionDays(value int) int {
	if value > 0 {
		return value
	}
	return DefaultRetentionDays
}

// Accounting reports this conversion's completeness in the terms every importer
// shares. HAR converts through the sample request pipeline, so it counts the
// same way that importer does. See convertresult.Accounting.
func (result *ConvertResult) Accounting() convertresult.Accounting {
	if result == nil {
		return convertresult.Accounting{}
	}
	return convertresult.New(
		result.Stats.TotalRequests,
		result.Stats.ConvertedActions,
		result.Stats.FilteredAssets+result.Stats.SkippedRequests,
		result.SourceReview.ReviewOnlyActionCount(),
	)
}

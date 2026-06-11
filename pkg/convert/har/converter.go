package har

import (
	"fmt"
	"strings"

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
	DefaultThinkTimeMs  int    `json:"default_think_time_ms,omitempty"`
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
}

func Convert(data []byte, opts ConvertOptions) (*ConvertResult, error) {
	if len(data) == 0 || strings.TrimSpace(string(data)) == "" {
		return nil, fmt.Errorf("HAR content is required")
	}
	artifactMode := normalizeArtifactMode(opts.ArtifactMode)
	retentionDays := normalizeRetentionDays(opts.RetentionDays)
	redactionEnabled := boolOption(opts.RedactSensitive, true)
	content := data
	governanceWarnings := []string{}
	sensitivity := scrub.SensitivityRaw
	if redactionEnabled {
		scrubbed, err := scrub.HAR(data, scrub.Policy{})
		if err != nil {
			return nil, err
		}
		content = scrubbed
		sensitivity = scrub.HARSensitivity(scrubbed)
		if sensitivity != scrub.SensitivityScrubbed {
			governanceWarnings = append(governanceWarnings, "HAR scrub-on-write did not produce a scrubbed sensitivity marker.")
		}
	}
	result, err := sample.Convert(sample.ImportRequest{
		Name:    firstNonEmpty(opts.ScriptName, "Imported_HAR_Script"),
		Format:  "har",
		Content: string(content),
		Options: sample.ConvertOptions{
			BaseURL:             opts.BaseURL,
			IncludeAssertions:   opts.IncludeAssertions,
			InferExtractors:     opts.InferExtractors,
			SanitizeSensitive:   opts.SanitizeSensitive,
			IncludeStaticAssets: opts.IncludeStaticAssets,
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
		if opts.DefaultThinkTimeMs > 0 && result.Script.Config.ThinkTime.MeanMs == 0 {
			result.Script.Config.ThinkTime.Type = "fixed"
			result.Script.Config.ThinkTime.MeanMs = float64(opts.DefaultThinkTimeMs)
		}
	}
	return &ConvertResult{
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
	}, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func boolOption(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
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

package har

import (
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/sample"
	"github.com/jasonczech/apexload/pkg/dsl"
)

type ConvertOptions struct {
	ScriptName          string `json:"script_name,omitempty"`
	BaseURL             string `json:"base_url,omitempty"`
	IncludeAssertions   *bool  `json:"include_assertions,omitempty"`
	InferExtractors     *bool  `json:"infer_extractors,omitempty"`
	SanitizeSensitive   *bool  `json:"sanitize_sensitive,omitempty"`
	IncludeStaticAssets *bool  `json:"include_static_assets,omitempty"`
	DefaultThinkTimeMs  int    `json:"default_think_time_ms,omitempty"`
}

type ConvertResult struct {
	Script       *dsl.Script          `json:"script"`
	Warnings     []string             `json:"warnings,omitempty"`
	Unsupported  diag.Diagnostics     `json:"unsupported,omitempty"`
	Stats        sample.ConvertStats  `json:"stats"`
	Correlations []sample.Correlation `json:"correlations,omitempty"`
}

func Convert(data []byte, opts ConvertOptions) (*ConvertResult, error) {
	if len(data) == 0 || strings.TrimSpace(string(data)) == "" {
		return nil, fmt.Errorf("HAR content is required")
	}
	result, err := sample.Convert(sample.ImportRequest{
		Name:    firstNonEmpty(opts.ScriptName, "Imported_HAR_Script"),
		Format:  "har",
		Content: string(data),
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
		Warnings:     result.Warnings,
		Unsupported:  result.Unsupported,
		Stats:        result.Stats,
		Correlations: result.Correlations,
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

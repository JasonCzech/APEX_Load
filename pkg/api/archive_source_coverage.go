package api

import (
	"fmt"
	"strings"
)

// Keep source-loss findings independent of artifact checksums and event counts:
// intact surviving files cannot prove that every generator supplied evidence.
func archiveSourceCoverageFinding(manifest *RunArchiveManifest) *RunArchiveCompletenessFinding {
	if manifest == nil {
		return nil
	}
	finding := func(severity string, penalty float64, message string) *RunArchiveCompletenessFinding {
		return &RunArchiveCompletenessFinding{Dimension: "generator_sources", Severity: severity, Penalty: penalty, Message: message}
	}
	coverage := manifest.SourceCoverage
	if coverage != nil {
		if (coverage.Mode != "required" && coverage.Mode != "best_effort") || len(coverage.Sources) == 0 || len(coverage.Sources) > remoteArchiveMaxTargets {
			return finding("critical", 30, "Generator archive source coverage is invalid; complete run evidence cannot be established.")
		}
		seen := make(map[string]bool, len(coverage.Sources))
		missing := 0
		for _, source := range coverage.Sources {
			target := strings.TrimSpace(source.Target)
			if target == "" || seen[target] {
				return finding("critical", 30, "Generator archive source coverage is invalid; complete run evidence cannot be established.")
			}
			seen[target] = true
			if !source.Collected {
				missing++
			}
		}
		if missing > 0 {
			return finding("critical", 30, fmt.Sprintf("%d of %d known generator archive sources were not collected. Surviving measurements are partial evidence.", missing, len(coverage.Sources)))
		}
		if coverage.Mode == "best_effort" {
			return finding("warning", 10, fmt.Sprintf("All %d known generator archive sources were collected on a best-effort basis; complete run evidence is not established.", len(coverage.Sources)))
		}
	}
	if strings.TrimSpace(manifest.CSVExportIncomplete) != "" {
		return finding("warning", 10, "Archive collection was marked incomplete; complete run evidence cannot be established from the retained files.")
	}
	return nil
}

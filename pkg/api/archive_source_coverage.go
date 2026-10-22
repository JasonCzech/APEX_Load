package api

import (
	"fmt"
	"slices"
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
			return finding("critical", 30, archiveMissingSourcesMessage(coverage.Sources, missing)+liveTableLossNote(archiveLiveTableAliases(manifest)))
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

// archiveMissingSourcesMessage names the generators whose archives were not
// collected and says which groups the surviving measurements still cover.
func archiveMissingSourcesMessage(sources []RunArchiveSource, missing int) string {
	message := fmt.Sprintf("%d of %d known generator archive sources were not collected", missing, len(sources))
	lost := make([]string, 0, missing)
	collectedGroups, missingGroups := map[string]bool{}, map[string]bool{}
	var groupOrder []string
	for _, source := range sources {
		for _, group := range source.Groups {
			if !collectedGroups[group] && !missingGroups[group] {
				groupOrder = append(groupOrder, group)
			}
			if source.Collected {
				collectedGroups[group] = true
			} else {
				missingGroups[group] = true
			}
		}
		if source.Collected {
			continue
		}
		// Named as the report's other generator lists name them (TEL-B).
		lost = append(lost, scrubTelemetryIdentity(source.Label()))
	}
	message += ": " + strings.Join(lost, ", ") + "."
	var covered, partly []string
	for _, group := range groupOrder {
		switch {
		case collectedGroups[group] && missingGroups[group]:
			partly = append(partly, group)
		case collectedGroups[group]:
			covered = append(covered, group)
		}
	}
	switch {
	case len(covered) > 0 && len(partly) > 0:
		return message + " Surviving measurements cover " + strings.Join(covered, ", ") + " and part of " + strings.Join(partly, ", ") + "."
	case len(covered) > 0:
		return message + " Surviving measurements cover " + strings.Join(covered, ", ") + " only."
	case len(partly) > 0:
		return message + " Surviving measurements cover part of " + strings.Join(partly, ", ") + "."
	}
	return message + " Surviving measurements are partial evidence."
}

// archiveLiveTableAliases lists the run's live-table aliases, run-scoped and
// project, in first-seen order.
func archiveLiveTableAliases(manifest *RunArchiveManifest) []string {
	var aliases []string
	add := func(alias string) {
		if alias = strings.TrimSpace(alias); alias != "" && !slices.Contains(aliases, alias) {
			aliases = append(aliases, alias)
		}
	}
	for _, table := range manifest.Provenance.LiveTables {
		add(table.Alias)
	}
	for _, attachment := range manifest.Provenance.ProjectLiveTables {
		add(attachment.Alias)
	}
	return aliases
}

// liveTableLossNote says what losing a generator means for the live tables its
// VUsers were using, or "" for a run without live tables.
func liveTableLossNote(aliases []string) string {
	const effect = ": leases the lost generator still held may be redelivered once they expire, and pops it had in flight may have unknown outcomes."
	switch len(aliases) {
	case 0:
		return ""
	case 1:
		return " Live table " + aliases[0] + effect
	}
	return " Live tables " + strings.Join(aliases, ", ") + effect
}

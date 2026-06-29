package api

import (
	"encoding/csv"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

func writeRunArchiveReportCSV(report *RunArchiveReport, writer io.Writer) error {
	return writeRunArchiveReportCSVWithProfile(report, writer, "engineer")
}

// writeRunArchiveReportCSVWithProfile writes the report as section/key/metric/value
// rows by iterating the profile's SectionOrder (gated by IncludedSections)
// through the shared section registry, so the profile metadata is the single
// source of truth for which sections are emitted and in what order.
func writeRunArchiveReportCSVWithProfile(report *RunArchiveReport, writer io.Writer, profileID string) error {
	profile, ok := lookupRunArchiveReportProfile(profileID)
	if !ok {
		return fmt.Errorf("unknown report profile %q", profileID)
	}
	csvWriter := csv.NewWriter(writer)
	if err := csvWriter.Write([]string{"section", "key", "metric", "value"}); err != nil {
		return err
	}
	if report == nil {
		csvWriter.Flush()
		return csvWriter.Error()
	}
	write := func(section, key, metric, value string) error {
		return csvWriter.Write([]string{section, key, metric, value})
	}

	data := buildRunArchiveReportRenderData(report, profile, nil, nil)
	for _, section := range reportSectionsForProfile(profile) {
		if err := section.CSV(&data, write); err != nil {
			return err
		}
	}

	csvWriter.Flush()
	return csvWriter.Error()
}

func sortedIntMapKeys(values map[string]int) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func writeChangeAttributionCSVRows(write reportCSVWriteFunc, attribution RunArchiveChangeAttribution) error {
	rows := []struct {
		metric string
		value  string
	}{
		{"scenario_revision", attribution.ScenarioRevision},
		{"environment", attribution.Environment},
		{"data_revision", attribution.DataRevision},
		{"runtime_overrides", strconv.FormatBool(attribution.RuntimeOverrides)},
		{"script_revisions", strings.Join(attribution.ScriptRevisions, ";")},
		{"generator_versions", strings.Join(attribution.GeneratorVersions, ";")},
		{"runtime_override_fields", strings.Join(attribution.RuntimeOverrideFields, ";")},
	}
	for _, row := range rows {
		if row.value == "" {
			continue
		}
		if err := write("change_attribution", "", row.metric, row.value); err != nil {
			return err
		}
	}
	return nil
}

func writeArchiveProvenanceCSVRows(write reportCSVWriteFunc, provenance RunArchiveProvenance) error {
	rows := []struct {
		metric string
		value  string
	}{
		{"application_id", provenance.ApplicationID},
		{"release_id", provenance.ReleaseID},
		{"scenario_id", provenance.ScenarioID},
		{"project_id", provenance.ProjectID},
		{"scenario_mode", provenance.ScenarioMode},
		{"script_shakeout", strconv.FormatBool(provenance.ScriptShakeout)},
		{"resource_baseline", strconv.FormatBool(provenance.ResourceBaseline)},
		{"start_delay", provenance.StartDelay},
		{"total_vusers", strconv.Itoa(provenance.TotalVUsers)},
		{"duration", provenance.Duration},
		{"baseline_pinned", strconv.FormatBool(provenance.BaselinePinned)},
		{"created_by_subject", provenance.CreatedBySubject},
		{"created_by_display_name", provenance.CreatedByDisplayName},
		{"started_by_subject", provenance.StartedBySubject},
		{"started_by_display_name", provenance.StartedByDisplayName},
		{"stopped_by_subject", provenance.StoppedBySubject},
		{"stopped_by_display_name", provenance.StoppedByDisplayName},
	}
	if provenance.Governance != nil && provenance.Governance.ExecutionPlanApproval != nil {
		approval := provenance.Governance.ExecutionPlanApproval
		rows = append(rows,
			struct{ metric, value string }{"execution_plan_approval_id", approval.ID},
			struct{ metric, value string }{"execution_plan_approval_plan_id", approval.PlanID},
			struct{ metric, value string }{"execution_plan_approval_policy", approval.Policy},
			struct{ metric, value string }{"execution_plan_approval_status", approval.Status},
			struct{ metric, value string }{"execution_plan_approval_requested_by", approval.RequestedBy},
			struct{ metric, value string }{"execution_plan_approval_resolved_by", approval.ResolvedBy},
			struct{ metric, value string }{"execution_plan_approval_decision_reason", approval.DecisionReason},
			struct{ metric, value string }{"execution_plan_approval_required_role", approval.RequiredRole},
		)
	}
	for _, row := range rows {
		if row.value == "" {
			continue
		}
		if err := write("provenance", "", row.metric, row.value); err != nil {
			return err
		}
	}
	return nil
}

func writeArchiveArtifactInventoryCSVRows(write reportCSVWriteFunc, manifest *RunArchiveManifest) error {
	if len(manifest.Artifacts) > 0 {
		for _, artifact := range manifest.Artifacts {
			if artifact.Name == "" {
				continue
			}
			rows := []struct {
				metric string
				value  string
			}{
				{"kind", artifact.Kind},
				{"format", artifact.Format},
				{"record_type", artifact.RecordType},
				{"count", formatCSVInt(artifact.Count)},
				{"size_bytes", formatCSVInt(artifact.SizeBytes)},
				{"sha256", artifact.SHA256},
			}
			for _, row := range rows {
				if row.value == "" || row.value == "0" && (row.metric == "count" || row.metric == "size_bytes") {
					continue
				}
				if err := write("artifact_inventory", artifact.Name, row.metric, row.value); err != nil {
					return err
				}
			}
		}
		return nil
	}
	rows := []RunArchiveArtifact{
		{Name: manifest.TransactionsFile, Count: manifest.TransactionCount},
		{Name: manifest.TransactionsParquetFile, Count: manifest.TransactionCount},
		{Name: manifest.ResourceSamplesFile, Count: manifest.ResourceSampleCount},
		{Name: manifest.ResourceSamplesParquetFile, Count: manifest.ResourceSampleCount},
		{Name: manifest.IntervalSummariesFile, Count: manifest.IntervalSummaryCount},
		{Name: manifest.IntervalSummariesParquetFile, Count: manifest.IntervalSummaryCount},
		{Name: manifest.TestManifestFile},
		{Name: manifest.TestNotesFile},
	}
	for _, row := range rows {
		if row.Name == "" {
			continue
		}
		if err := write("artifact_inventory", row.Name, "count", formatCSVInt(row.Count)); err != nil {
			return err
		}
	}
	return nil
}

func writeArchiveReportStatsCSVRows(write reportCSVWriteFunc, section string, key string, stats RunArchiveActionStats) error {
	rows := []struct {
		metric string
		value  string
	}{
		{"transactions", formatCSVInt(stats.Transactions)},
		{"errors", formatCSVInt(stats.Errors)},
		{"error_pct", formatCSVFloat(stats.ErrorPct)},
		{"avg_response_ms", formatCSVFloat(stats.AvgResponseMs)},
		{"peak_tps", formatCSVFloat(stats.PeakTPS)},
		{"min_response_ms", formatCSVFloat(stats.MinResponseMs)},
		{"max_response_ms", formatCSVFloat(stats.MaxResponseMs)},
		{"p50_ms", formatCSVFloat(stats.P50Ms)},
		{"p90_ms", formatCSVFloat(stats.P90Ms)},
		{"p95_ms", formatCSVFloat(stats.P95Ms)},
		{"p99_ms", formatCSVFloat(stats.P99Ms)},
	}
	for _, row := range rows {
		if err := write(section, key, row.metric, row.value); err != nil {
			return err
		}
	}
	return nil
}

func sortedArchiveReportStatKeys(values map[string]RunArchiveActionStats) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func formatCSVInt(value int64) string {
	return strconv.FormatInt(value, 10)
}

func formatCSVFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

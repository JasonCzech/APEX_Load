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
	return writeRunArchiveReportCSVWithProfileAndAnnotations(report, writer, profileID, nil, nil)
}

// writeRunArchiveReportCSVWithProfileAndAnnotations writes the CSV with the
// analyst-saved annotations and generator manifest applied, so the audit CSV's
// official_outcome and generator_manifest sections match the HTML report
// instead of showing the auto-derived outcome and a "manifest unavailable" note.
func writeRunArchiveReportCSVWithProfileAndAnnotations(report *RunArchiveReport, writer io.Writer, profileID string, annotations *ReportAnnotations, manifest *TestRunManifest) error {
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

	data := buildRunArchiveReportRenderData(report, profile, annotations, manifest)
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

// writeArchiveIntegrityCSVRows emits the archive re-verification verdict (and each
// artifact's status/hashes) as CSV rows. Called by the artifact_integrity section
// renderer in the registry.
func writeArchiveIntegrityCSVRows(write reportCSVWriteFunc, integrity *RunArchiveIntegrityReport) error {
	rows := []struct {
		metric string
		value  string
	}{
		{"status", integrity.Status},
		{"verified", strconv.FormatBool(integrity.Verified)},
		{"summary", integrity.Summary},
		{"total", strconv.Itoa(integrity.Total)},
		{"matched", strconv.Itoa(integrity.Matched)},
		{"mismatched", strconv.Itoa(integrity.Mismatched)},
		{"missing", strconv.Itoa(integrity.Missing)},
		{"unverifiable", strconv.Itoa(integrity.Unverifiable)},
	}
	for _, row := range rows {
		if err := write("artifact_integrity", "", row.metric, row.value); err != nil {
			return err
		}
	}
	for _, artifact := range integrity.Artifacts {
		key := artifact.Name
		if err := write("artifact_integrity_artifact", key, "status", artifact.Status); err != nil {
			return err
		}
		if artifact.ExpectedSHA256 != "" {
			if err := write("artifact_integrity_artifact", key, "expected_sha256", artifact.ExpectedSHA256); err != nil {
				return err
			}
		}
		if artifact.ActualSHA256 != "" {
			if err := write("artifact_integrity_artifact", key, "actual_sha256", artifact.ActualSHA256); err != nil {
				return err
			}
		}
		if artifact.Detail != "" {
			if err := write("artifact_integrity_artifact", key, "detail", artifact.Detail); err != nil {
				return err
			}
		}
	}
	return nil
}

// writeNetworkPhaseCSVRows emits one network-phase rollup (overall or per-action)
// as CSV rows. Called by the network_phases section renderer in the registry.
func writeNetworkPhaseCSVRows(write reportCSVWriteFunc, key string, phases RunArchiveNetworkPhases) error {
	rows := []struct {
		metric string
		value  string
	}{
		{"count", formatCSVInt(phases.Count)},
		{"avg_dns_ms", formatCSVFloat(phases.AvgDNSMs)},
		{"avg_connect_ms", formatCSVFloat(phases.AvgConnectMs)},
		{"avg_tls_ms", formatCSVFloat(phases.AvgTLSMs)},
		{"avg_ttfb_ms", formatCSVFloat(phases.AvgTTFBMs)},
		{"max_dns_ms", formatCSVFloat(phases.MaxDNSMs)},
		{"max_connect_ms", formatCSVFloat(phases.MaxConnectMs)},
		{"max_tls_ms", formatCSVFloat(phases.MaxTLSMs)},
		{"max_ttfb_ms", formatCSVFloat(phases.MaxTTFBMs)},
	}
	for _, row := range rows {
		if err := write("network_phases", key, row.metric, row.value); err != nil {
			return err
		}
	}
	return nil
}

// writeReleaseTrendSummaryCSVRows emits the compact release-over-release trend as
// CSV rows. Called by the release_trend section renderer in the registry.
func writeReleaseTrendSummaryCSVRows(write reportCSVWriteFunc, trend *RunArchiveReleaseTrendSummary) error {
	rows := []struct {
		metric string
		value  string
	}{
		{"release_id", trend.ReleaseID},
		{"run_count", formatCSVInt(int64(trend.RunCount))},
		{"has_baseline", strconv.FormatBool(trend.HasBaseline)},
		{"regressed", strconv.FormatBool(trend.Regressed)},
		{"regressions", strings.Join(trend.Regressions, ";")},
		{"avg_response_trend_pct", formatCSVFloat(trend.Trajectory.AvgResponseTrendPct)},
		{"error_pct_trend", formatCSVFloat(trend.Trajectory.ErrorPctTrend)},
		{"note", trend.Note},
	}
	for _, row := range rows {
		if row.value == "" {
			continue
		}
		if err := write("release_trend", "", row.metric, row.value); err != nil {
			return err
		}
	}
	if err := writeReleaseTrendPointCSVRows(write, "current", trend.Current); err != nil {
		return err
	}
	if trend.HasBaseline && trend.Baseline != nil {
		if err := writeReleaseTrendPointCSVRows(write, "baseline", *trend.Baseline); err != nil {
			return err
		}
	}
	if trend.Deltas != nil {
		deltaRows := []struct {
			metric string
			value  string
		}{
			{"error_pct_delta", formatCSVFloat(trend.Deltas.ErrorPctDelta)},
			{"avg_response_ms_delta", formatCSVFloat(trend.Deltas.AvgResponseMsDelta)},
			{"avg_response_pct_delta", formatCSVFloat(trend.Deltas.AvgResponsePctDelta)},
			{"peak_tps_delta", formatCSVFloat(trend.Deltas.PeakTPSDelta)},
			{"completeness_delta", formatCSVFloat(trend.Deltas.CompletenessDelta)},
		}
		for _, row := range deltaRows {
			if err := write("release_trend", "delta", row.metric, row.value); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeReleaseTrendPointCSVRows(write reportCSVWriteFunc, key string, point ReleaseTrendPoint) error {
	rows := []struct {
		metric string
		value  string
	}{
		{"run_id", point.RunID},
		{"run_name", point.RunName},
		{"error_pct", formatCSVFloat(point.ErrorPct)},
		{"avg_response_ms", formatCSVFloat(point.AvgResponseMs)},
		{"peak_tps", formatCSVFloat(point.PeakTPS)},
		{"completeness_pct", formatCSVFloat(point.CompletenessPct)},
		{"sla_breached", strconv.FormatBool(point.SLABreached)},
	}
	for _, row := range rows {
		if row.value == "" {
			continue
		}
		if err := write("release_trend", key, row.metric, row.value); err != nil {
			return err
		}
	}
	return nil
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

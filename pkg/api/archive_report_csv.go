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

func writeRunArchiveReportCSVWithProfile(report *RunArchiveReport, writer io.Writer, profileID string) error {
	profile, ok := lookupRunArchiveReportProfile(profileID)
	if !ok {
		return fmt.Errorf("unknown report profile %q", profileID)
	}
	if !runArchiveReportProfileSupportsFormat(profile, "csv") {
		return fmt.Errorf("report profile %q does not support csv export", profile.ID)
	}
	csvWriter := csv.NewWriter(writer)
	if err := csvWriter.Write([]string{"section", "key", "metric", "value"}); err != nil {
		return err
	}
	if report == nil {
		csvWriter.Flush()
		return csvWriter.Error()
	}
	write := func(section string, key string, metric string, value string) error {
		return csvWriter.Write([]string{section, key, metric, value})
	}

	sections := stringSet(profile.IncludedSections)
	if hasCSVSection(sections, "overview") || hasCSVSection(sections, "schema_compatibility") {
		if err := write("overview", "", "schema_version", report.SchemaVersion); err != nil {
			return err
		}
	}
	if hasCSVSection(sections, "overview") {
		overviewRows := []struct {
			metric string
			value  string
		}{
			{"transactions", formatCSVInt(report.Overview.Transactions)},
			{"errors", formatCSVInt(report.Overview.Errors)},
			{"error_pct", formatCSVFloat(report.Overview.ErrorPct)},
			{"avg_response_ms", formatCSVFloat(report.Overview.AvgResponseMs)},
			{"min_response_ms", formatCSVFloat(report.Overview.MinResponseMs)},
			{"max_response_ms", formatCSVFloat(report.Overview.MaxResponseMs)},
			{"peak_tps", formatCSVFloat(report.Overview.PeakTPS)},
			{"peak_active_vusers", formatCSVInt(report.Overview.PeakActiveVUsers)},
			{"completeness_score_pct", formatCSVFloat(report.Completeness.ScorePct)},
			{"completeness_status", report.Completeness.Status},
		}
		for _, row := range overviewRows {
			if err := write("overview", "", row.metric, row.value); err != nil {
				return err
			}
		}
	}

	if hasCSVSection(sections, "schema_compatibility") {
		if err := write("schema_compatibility", "", "status", report.Compatibility.Status); err != nil {
			return err
		}
		if err := write("schema_compatibility", "", "report_schema_version", report.Compatibility.ReportSchemaVersion); err != nil {
			return err
		}
		if err := write("schema_compatibility", "", "manifest_schema_version", report.Compatibility.ManifestSchemaVersion); err != nil {
			return err
		}
		for _, output := range report.Compatibility.Outputs {
			key := output.Format
			if output.Artifact != "" {
				key = key + ":" + output.Artifact
			}
			if err := write("output_compatibility", key, "status", output.Status); err != nil {
				return err
			}
			if output.Reason != "" {
				if err := write("output_compatibility", key, "reason", output.Reason); err != nil {
					return err
				}
			}
		}
	}

	if hasCSVSection(sections, "completeness") {
		if err := write("completeness", "", "score_pct", formatCSVFloat(report.Completeness.ScorePct)); err != nil {
			return err
		}
		if err := write("completeness", "", "status", report.Completeness.Status); err != nil {
			return err
		}
		for _, finding := range report.Completeness.Findings {
			key := finding.Dimension
			if err := write("completeness_finding", key, "severity", finding.Severity); err != nil {
				return err
			}
			if err := write("completeness_finding", key, "message", finding.Message); err != nil {
				return err
			}
		}
	}

	if hasCSVSection(sections, "evidence_tiers") {
		for _, tier := range report.EvidenceTiers {
			if err := write("evidence_tier", tier.Tier, "status", tier.Status); err != nil {
				return err
			}
			if err := write("evidence_tier", tier.Tier, "count", formatCSVInt(tier.Count)); err != nil {
				return err
			}
		}
	}

	if hasCSVSection(sections, "change_attribution") {
		if err := writeChangeAttributionCSVRows(write, report.ChangeAttribution); err != nil {
			return err
		}
	}

	if hasCSVSection(sections, "release_trend") && report.ReleaseTrend != nil {
		if err := writeReleaseTrendSummaryCSVRows(write, report.ReleaseTrend); err != nil {
			return err
		}
	}

	if hasCSVSection(sections, "provenance") && report.Manifest != nil {
		if err := writeArchiveProvenanceCSVRows(write, report.Manifest.Provenance); err != nil {
			return err
		}
	}

	if hasCSVSection(sections, "artifact_inventory") && report.Manifest != nil {
		if err := writeArchiveArtifactInventoryCSVRows(write, report.Manifest); err != nil {
			return err
		}
	}

	if hasCSVSection(sections, "artifact_integrity") && report.Integrity != nil {
		if err := writeArchiveIntegrityCSVRows(write, report.Integrity); err != nil {
			return err
		}
	}

	if hasCSVSection(sections, "actions") {
		actionNames := sortedArchiveReportStatKeys(report.ByAction)
		for _, action := range actionNames {
			if err := writeArchiveReportStatsCSVRows(csvWriter, "action", action, report.ByAction[action]); err != nil {
				return err
			}
		}
		soapOperations := sortedArchiveReportStatKeys(report.BySOAPOperation)
		for _, operation := range soapOperations {
			if err := writeArchiveReportStatsCSVRows(csvWriter, "soap_operation", operation, report.BySOAPOperation[operation]); err != nil {
				return err
			}
		}
	}

	if hasCSVSection(sections, "network_phases") && report.NetworkPhases.Samples > 0 {
		if err := writeNetworkPhaseCSVRows(write, "overall", report.NetworkPhases.Overall); err != nil {
			return err
		}
		actionKeys := make([]string, 0, len(report.NetworkPhases.ByAction))
		for action := range report.NetworkPhases.ByAction {
			actionKeys = append(actionKeys, action)
		}
		sort.Strings(actionKeys)
		for _, action := range actionKeys {
			if err := writeNetworkPhaseCSVRows(write, action, report.NetworkPhases.ByAction[action]); err != nil {
				return err
			}
		}
	}

	if hasCSVSection(sections, "web2_migration") && report.Web2Migration != nil {
		rows := []struct {
			metric string
			value  string
		}{
			{"status", report.Web2Migration.Status},
			{"imported_steps", strconv.Itoa(report.Web2Migration.ImportedSteps)},
			{"replayed_steps", strconv.Itoa(report.Web2Migration.ReplayedSteps)},
			{"failed_steps", strconv.Itoa(report.Web2Migration.FailedSteps)},
			{"conversion_to_http_ready", strconv.FormatBool(report.Web2Migration.ConversionToHTTPReady)},
			{"transaction_groups", strings.Join(report.Web2Migration.TransactionGroups, ";")},
			{"source_profiles", strings.Join(report.Web2Migration.SourceProfiles, ";")},
			{"next_actions", strings.Join(report.Web2Migration.NextActions, ";")},
		}
		for _, row := range rows {
			if row.value == "" {
				continue
			}
			if err := write("web2_migration", "", row.metric, row.value); err != nil {
				return err
			}
		}
		for _, key := range sortedIntMapKeys(report.Web2Migration.ReplayEvidence) {
			if err := write("web2_migration_evidence", key, "count", strconv.Itoa(report.Web2Migration.ReplayEvidence[key])); err != nil {
				return err
			}
		}
	}

	if hasCSVSection(sections, "summary_timeline") {
		for _, point := range report.SummaryTimeline {
			key := point.Timestamp.Format("2006-01-02T15:04:05Z07:00")
			rows := []struct {
				metric string
				value  string
			}{
				{"active_vusers", formatCSVInt(point.ActiveVUsers)},
				{"tps", formatCSVFloat(point.TPS)},
				{"target_tps", formatCSVFloat(point.TargetTPS)},
				{"throughput_gap_pct", formatCSVFloat(point.ThroughputGapPct)},
				{"throughput_target_missed", strconv.FormatBool(point.ThroughputTargetMissed)},
				{"error_pct", formatCSVFloat(point.ErrorPct)},
				{"p95_ms", formatCSVFloat(point.P95Ms)},
				{"total_transactions", formatCSVInt(point.TotalTx)},
				{"total_errors", formatCSVInt(point.TotalErrors)},
			}
			for _, row := range rows {
				if err := write("summary_timeline", key, row.metric, row.value); err != nil {
					return err
				}
			}
		}
	}

	csvWriter.Flush()
	return csvWriter.Error()
}

func stringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	addAlias := func(alias string, sources ...string) {
		for _, source := range sources {
			if _, ok := set[source]; ok {
				set[alias] = struct{}{}
				return
			}
		}
	}
	// Audit deliberately omits the performance Overview block (Peak TPS / throughput
	// gap / response stats are tuning metrics): audit_identity is surfaced via the
	// provenance alias below, not via "overview".
	addAlias("overview", "executive_overview", "technical_overview")
	addAlias("sla", "sla_rollup", "sla_detail", "official_outcome")
	addAlias("graphs", "executive_overview", "technical_overview")
	addAlias("summary_timeline", "technical_overview")
	addAlias("actions", "sla_detail")
	addAlias("errors", "risk_summary", "errors", "root_cause")
	addAlias("resources", "resources")
	addAlias("browser", "browser")
	addAlias("raw_exports", "raw_exports", "artifact_integrity")
	addAlias("artifact_inventory", "artifact_integrity")
	addAlias("provenance", "audit_identity", "authorization", "provenance")
	addAlias("schema_compatibility", "schema_compatibility")
	addAlias("evidence_tiers", "evidence_tiers")
	addAlias("completeness", "completeness")
	addAlias("change_attribution", "change_attribution")
	return set
}

func writeArchiveIntegrityCSVRows(write func(string, string, string, string) error, integrity *RunArchiveIntegrityReport) error {
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

func hasCSVSection(sections map[string]struct{}, section string) bool {
	_, ok := sections[section]
	return ok
}

func sortedIntMapKeys(values map[string]int) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func writeReleaseTrendSummaryCSVRows(write func(string, string, string, string) error, trend *RunArchiveReleaseTrendSummary) error {
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

func writeReleaseTrendPointCSVRows(write func(string, string, string, string) error, key string, point ReleaseTrendPoint) error {
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

func writeChangeAttributionCSVRows(write func(string, string, string, string) error, attribution RunArchiveChangeAttribution) error {
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

func writeArchiveProvenanceCSVRows(write func(string, string, string, string) error, provenance RunArchiveProvenance) error {
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

func writeArchiveArtifactInventoryCSVRows(write func(string, string, string, string) error, manifest *RunArchiveManifest) error {
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

func writeNetworkPhaseCSVRows(write func(string, string, string, string) error, key string, phases RunArchiveNetworkPhases) error {
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

func writeArchiveReportStatsCSVRows(writer *csv.Writer, section string, key string, stats RunArchiveActionStats) error {
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
		if err := writer.Write([]string{section, key, row.metric, row.value}); err != nil {
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

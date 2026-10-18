package api

import (
	"sort"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// buildPowerBIRunRow flattens a run's headline aggregates into the single
// `runs` push-dataset row. The caller must have attached the verdict and SLA
// rollup (attachReportVerdictAndSLA) before mapping.
func buildPowerBIRunRow(report *RunArchiveReport, publishID string, publishedAt time.Time, apexloadURL string) map[string]any {
	started, completed := powerBIRunWindow(report, publishedAt)
	executionEnd := archiveExecutionCompletedAt(report.Manifest)
	if executionEnd.IsZero() {
		// Retain the legacy publish-time fallback when the archive has no end.
		executionEnd = completed
	}
	durationSeconds := 0.0
	// A zero started time (legacy archive with no provenance and no CreatedAt)
	// would otherwise yield a multi-decade duration and garbage avg_tps.
	if !started.IsZero() && executionEnd.After(started) {
		durationSeconds = executionEnd.Sub(started).Seconds()
	}
	avgTPS := 0.0
	if durationSeconds > 0 {
		avgTPS = float64(report.Overview.Transactions) / durationSeconds
	}
	verdict := ""
	if report.Verdict != nil {
		verdict = report.Verdict.Status
	}
	row := map[string]any{
		"run_id":             report.Manifest.TestID,
		"publish_id":         publishID,
		"test_name":          report.Manifest.TestName,
		"project_id":         report.Manifest.Provenance.ProjectID,
		"release_id":         report.Manifest.Provenance.ReleaseID,
		"application_id":     report.Manifest.Provenance.ApplicationID,
		"status":             report.Manifest.Status,
		"verdict":            verdict,
		"started_at":         started.UTC().Format(time.RFC3339),
		"completed_at":       completed.UTC().Format(time.RFC3339),
		"duration_seconds":   durationSeconds,
		"avg_tps":            avgTPS,
		"total_vusers":       int64(report.Manifest.Provenance.TotalVUsers),
		"peak_active_vusers": report.Overview.PeakActiveVUsers,
		"total_transactions": report.Overview.Transactions,
		"total_errors":       report.Overview.Errors,
		"error_pct":          report.Overview.ErrorPct,
		"avg_ms":             report.Overview.AvgResponseMs,
		"min_ms":             report.Overview.MinResponseMs,
		"max_ms":             report.Overview.MaxResponseMs,
		"p50_ms":             report.Overview.P50Ms,
		"p90_ms":             report.Overview.P90Ms,
		"p95_ms":             report.Overview.P95Ms,
		"p99_ms":             report.Overview.P99Ms,
		"peak_tps":           report.Overview.PeakTPS,
		"peak_target_tps":    report.Overview.PeakTargetTPS,
		"apexload_url":       apexloadURL,
		"published_at":       publishedAt.UTC().Format(time.RFC3339),
	}
	addPowerBIRunVersionMix(row, report.NetworkPhases.VersionMix)
	if report.SLARollup != nil {
		row["sla_evaluated"] = report.SLARollup.Evaluated
		row["sla_passed"] = report.SLARollup.Passed
		row["sla_warned"] = report.SLARollup.Warned
		row["sla_failed"] = report.SLARollup.Failed
	} else {
		row["sla_evaluated"] = int64(0)
		row["sla_passed"] = int64(0)
		row["sla_warned"] = int64(0)
		row["sla_failed"] = int64(0)
	}
	return row
}

// powerBIRunWindow resolves the run's wall-clock bounds. StartedAt is the
// authoritative start when recorded; CreatedAt (archive open) is the fallback.
// A missing CompletedAt falls back to publish time so duration stays positive
// for legacy archives.
func powerBIRunWindow(report *RunArchiveReport, publishedAt time.Time) (time.Time, time.Time) {
	started := report.Manifest.CreatedAt
	if report.Manifest.Provenance.StartedAt != nil && !report.Manifest.Provenance.StartedAt.IsZero() {
		started = *report.Manifest.Provenance.StartedAt
	}
	completed := report.Manifest.CompletedAt
	if completed.IsZero() {
		completed = publishedAt
	}
	return started, completed
}

// buildPowerBITransactionRows emits one row per action and one per group,
// distinguished by the kind column. Per-transaction SLA outcomes come from the
// report's SLA compliance rows, which are keyed by action name.
func buildPowerBITransactionRows(report *RunArchiveReport, publishID string, publishedAt time.Time) []map[string]any {
	published := publishedAt.UTC().Format(time.RFC3339)
	rows := make([]map[string]any, 0, len(report.ByAction)+len(report.ByGroup))
	appendStats := func(kind string, name string, stats RunArchiveActionStats) {
		row := map[string]any{
			"run_id":       report.Manifest.TestID,
			"publish_id":   publishID,
			"published_at": published,
			"kind":         kind,
			"name":         name,
			"count":        stats.Transactions,
			"errors":       stats.Errors,
			"error_pct":    stats.ErrorPct,
			"avg_ms":       stats.AvgResponseMs,
			"min_ms":       stats.MinResponseMs,
			"max_ms":       stats.MaxResponseMs,
			"p50_ms":       stats.P50Ms,
			"p90_ms":       stats.P90Ms,
			"p95_ms":       stats.P95Ms,
			"p99_ms":       stats.P99Ms,
			"peak_tps":     stats.PeakTPS,
			"has_sla":      false,
			"sla_ms":       0.0,
			"sla_result":   "",
		}
		var mix *protocol.HTTPVersionMix
		if kind == "action" {
			if sla, ok := report.SLARows[name]; ok {
				row["has_sla"] = sla.HasSLA
				row["sla_ms"] = sla.SLAMs
				row["sla_result"] = sla.ResultKind
			}
			mix = report.NetworkPhases.VersionMixByAction[name]
		}
		addPowerBITransactionVersionMix(row, mix)
		rows = append(rows, row)
	}
	for _, name := range sortedStatKeys(report.ByAction) {
		appendStats("action", name, report.ByAction[name])
	}
	for _, name := range sortedStatKeys(report.ByGroup) {
		appendStats("group", name, report.ByGroup[name])
	}
	return rows
}

// addPowerBIRunVersionMix adds the run's HTTP version mix; a run without HTTP
// actions, or an archive from before the mix existed, reports zeros.
func addPowerBIRunVersionMix(row map[string]any, mix *RunArchiveHTTPVersionMix) {
	if mix == nil {
		mix = &RunArchiveHTTPVersionMix{}
	}
	finding := ""
	if mix.Finding != nil {
		finding = mix.Finding.Summary
	}
	row["http_actions"] = mix.Actions
	row["http1_actions"] = mix.ByVersion["1.1"]
	row["http2_actions"] = mix.ByVersion["2"]
	row["http3_actions"] = mix.ByVersion["3"]
	row["http_no_response_actions"] = mix.ByVersion["unknown"]
	row["http_cache_actions"] = mix.ByVersion["cache"]
	row["http_attempts"] = mix.Attempts
	row["http3_upgrade_eligible"] = mix.UpgradeEligible
	row["http3_fallbacks"] = mix.FellBack
	row["http3_fallback_ratio"] = mix.FallbackRatio.Value
	row["quic_attempted"] = mix.QUICAttempted
	row["quic_failed"] = mix.QUICFailed
	row["quic_failure_ratio"] = mix.QUICFailureRatio.Value
	row["protocol_mix_finding"] = finding
}

// addPowerBITransactionVersionMix adds an action's version counts.
// http_version is the one version every wire response used, "mixed" when
// they differ, and empty without a wire response.
func addPowerBITransactionVersionMix(row map[string]any, mix *protocol.HTTPVersionMix) {
	if mix == nil {
		mix = &protocol.HTTPVersionMix{}
	}
	version := ""
	for _, key := range []string{"1.1", "2", "3"} {
		if mix.ByVersion[key] == 0 {
			continue
		}
		if version != "" {
			version = "mixed"
			break
		}
		version = key
	}
	row["http_version"] = version
	row["http1_count"] = mix.ByVersion["1.1"]
	row["http2_count"] = mix.ByVersion["2"]
	row["http3_count"] = mix.ByVersion["3"]
	var fallbacks int64
	for _, count := range mix.ByFallback {
		fallbacks += count
	}
	row["http_fallbacks"] = fallbacks
	row["http_attempts"] = mix.Attempts
}

func sortedStatKeys(stats map[string]RunArchiveActionStats) []string {
	keys := make([]string, 0, len(stats))
	for name := range stats {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	return keys
}

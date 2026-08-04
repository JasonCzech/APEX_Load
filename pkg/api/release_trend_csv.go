package api

import (
	"encoding/csv"
	"io"
	"sort"
	"strconv"
	"time"
)

type releaseTrendRun struct {
	Test   *ManagedTest
	Report *RunArchiveReport
	Point  *ReleaseTrendPoint
}

func writeReleaseTrendCSV(writer io.Writer, runs []releaseTrendRun) error {
	csvWriter := csv.NewWriter(writer)
	headers := []string{
		"release_id",
		"run_id",
		"run_name",
		"status",
		"started_at",
		"completed_at",
		"transactions",
		"errors",
		"error_pct",
		"avg_response_ms",
		"peak_tps",
		"peak_active_vusers",
		"sla_breached",
		"completeness_score_pct",
		"completeness_status",
		"cpu_peak_pct",
		"memory_peak_mb",
		"telemetry_status",
		"telemetry_breached_intervals",
		"telemetry_sustained_breach_intervals",
	}
	if err := csvWriter.Write(headers); err != nil {
		return err
	}
	sort.SliceStable(runs, func(i, j int) bool {
		return releaseTrendRunCompletedAt(runs[i]).Before(releaseTrendRunCompletedAt(runs[j]))
	})
	for _, run := range runs {
		if run.Test == nil || run.Report == nil {
			continue
		}
		if err := csvWriter.Write(releaseTrendCSVRow(run)); err != nil {
			return err
		}
	}
	csvWriter.Flush()
	return csvWriter.Error()
}

func releaseTrendCSVRow(run releaseTrendRun) []string {
	report := run.Report
	test := run.Test
	startedAt := ""
	if test.StartedAt != nil {
		startedAt = formatArchiveCSVTime(*test.StartedAt)
	} else if report.Manifest != nil && report.Manifest.Provenance.StartedAt != nil {
		startedAt = formatArchiveCSVTime(*report.Manifest.Provenance.StartedAt)
	}
	completedAt := formatArchiveCSVTime(releaseTrendRunCompletedAt(run))
	slaBreached := "false"
	if report.Manifest != nil {
		slaBreached = strconv.FormatBool(report.Manifest.SLABreached)
	}
	telemetryStatus := ""
	telemetryBreachedIntervals := int64(0)
	telemetrySustainedBreachIntervals := int64(0)
	if report.Overview.Telemetry != nil {
		telemetryStatus = report.Overview.Telemetry.Status
		telemetryBreachedIntervals = report.Overview.Telemetry.BreachedIntervals
		telemetrySustainedBreachIntervals = report.Overview.Telemetry.SustainedBreachIntervals
	}
	return []string{
		firstNonEmpty(test.ReleaseID, releaseTrendManifestReleaseID(report)),
		test.ID,
		test.Name,
		test.Status,
		startedAt,
		completedAt,
		formatCSVInt(report.Overview.Transactions),
		formatCSVInt(report.Overview.Errors),
		formatCSVFloat(report.Overview.ErrorPct),
		formatCSVFloat(report.Overview.AvgResponseMs),
		formatCSVFloat(report.Overview.PeakTPS),
		formatCSVInt(report.Overview.PeakActiveVUsers),
		slaBreached,
		formatCSVFloat(report.Completeness.ScorePct),
		report.Completeness.Status,
		formatCSVFloat(report.ResourcePeaks.CPUProcessPct),
		formatCSVFloat(report.ResourcePeaks.MemProcessMB),
		telemetryStatus,
		formatCSVInt(telemetryBreachedIntervals),
		formatCSVInt(telemetrySustainedBreachIntervals),
	}
}

func releaseTrendRunCompletedAt(run releaseTrendRun) time.Time {
	if run.Point != nil && !run.Point.CompletedAt.IsZero() {
		return run.Point.CompletedAt
	}
	if run.Report != nil && run.Report.Manifest != nil && !run.Report.Manifest.CompletedAt.IsZero() {
		return run.Report.Manifest.CompletedAt
	}
	if run.Test != nil && run.Test.EndedAt != nil {
		return *run.Test.EndedAt
	}
	if run.Test != nil {
		return run.Test.CreatedAt
	}
	return time.Time{}
}

func releaseTrendManifestReleaseID(report *RunArchiveReport) string {
	if report == nil || report.Manifest == nil {
		return ""
	}
	return report.Manifest.Provenance.ReleaseID
}

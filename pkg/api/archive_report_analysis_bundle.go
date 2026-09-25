package api

import (
	"archive/zip"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// writeRunArchiveAnalysisBundle provides one row per review entity while the
// existing four-column CSV remains stable for integrations that already use it.
func writeRunArchiveAnalysisBundle(report *RunArchiveReport, writer io.Writer, profileID string, annotations *ReportAnnotations, manifest *TestRunManifest) error {
	profile, ok := lookupRunArchiveReportProfile(profileID)
	if !ok {
		return fmt.Errorf("unknown report profile %q", profileID)
	}
	if report == nil || report.Manifest == nil {
		return fmt.Errorf("archive report is incomplete")
	}
	data := buildRunArchiveReportRenderData(report, profile, annotations, manifest)
	archive := zip.NewWriter(writer)
	writeCSV := func(name string, header []string, rows [][]string) error {
		entry, err := archive.Create(name)
		if err != nil {
			return err
		}
		csvWriter := csv.NewWriter(entry)
		if err := csvWriter.Write(header); err != nil {
			return err
		}
		if err := csvWriter.WriteAll(rows); err != nil {
			return err
		}
		csvWriter.Flush()
		return csvWriter.Error()
	}
	toFloat := func(value float64) string { return strconv.FormatFloat(value, 'f', -1, 64) }
	toInt := func(value int64) string { return strconv.FormatInt(value, 10) }
	runID := report.Manifest.TestID
	completed := ""
	if !report.Manifest.CompletedAt.IsZero() {
		completed = report.Manifest.CompletedAt.UTC().Format(time.RFC3339)
	}
	header := []string{"run_id", "profile", "run_name", "completed_utc", "environment", "outcome", "outcome_explanation", "completeness_pct", "completeness_status"}
	row := []string{runID, profile.ID, reportRunName(report), completed, reportEnvironment(report), data.Verdict.Status, data.Verdict.Explanation, toFloat(report.Completeness.ScorePct), report.Completeness.Status}
	if profile.ID != "audit" {
		header = append(header, "captured_records", "errors", "error_pct", "archive_p95_ms", "peak_successful_transaction_tps", "peak_vusers")
		row = append(row, toInt(report.Overview.Transactions), toInt(report.Overview.Errors), toFloat(report.Overview.ErrorPct), toFloat(report.Overview.P95Ms), toFloat(report.Overview.PeakTPS), toInt(report.Overview.PeakActiveVUsers))
	}
	if err := writeCSV("run_summary.csv", header, [][]string{row}); err != nil {
		_ = archive.Close()
		return err
	}
	gapRows := make([][]string, 0, len(data.EvidenceGaps))
	for _, gap := range data.EvidenceGaps {
		gapRows = append(gapRows, []string{runID, profile.ID, gap})
	}
	if err := writeCSV("evidence_gaps.csv", []string{"run_id", "profile", "gap"}, gapRows); err != nil {
		_ = archive.Close()
		return err
	}
	if profile.ID != "audit" {
		slaRows := make([][]string, 0, len(data.SLARows))
		for _, item := range data.SLARows {
			threshold := ""
			if item.HasSLA {
				threshold = toFloat(item.SLAMs)
			}
			slaRows = append(slaRows, []string{runID, item.Name, toInt(item.Volume), toFloat(item.P95Ms), threshold, toFloat(item.ErrorPct), item.Result})
		}
		if err := writeCSV("transaction_sla.csv", []string{"run_id", "transaction", "sample_count", "p95_ms", "sla_ms", "error_pct", "result"}, slaRows); err != nil {
			_ = archive.Close()
			return err
		}
		findingRows := make([][]string, 0, len(data.Findings))
		for _, finding := range data.Findings {
			findingRows = append(findingRows, []string{runID, finding.ID, finding.Severity, finding.Title, finding.Observed, finding.Evidence, finding.RootCause, finding.Impact})
		}
		if err := writeCSV("findings.csv", []string{"run_id", "finding_id", "severity", "title", "observed", "evidence", "root_cause", "impact"}, findingRows); err != nil {
			_ = archive.Close()
			return err
		}
	}
	if profile.ID == "audit" {
		artifactRows := [][]string{}
		if report.Integrity != nil {
			for _, item := range report.Integrity.Artifacts {
				artifactRows = append(artifactRows, []string{runID, item.Name, item.Status, item.ExpectedSHA256, item.ActualSHA256, toInt(item.SizeBytes), item.Detail})
			}
		}
		if err := writeCSV("artifacts.csv", []string{"run_id", "artifact", "status", "expected_sha256", "actual_sha256", "size_bytes", "detail"}, artifactRows); err != nil {
			_ = archive.Close()
			return err
		}
	}
	readme, err := archive.Create("README.txt")
	if err != nil {
		_ = archive.Close()
		return err
	}
	_, err = io.WriteString(readme, strings.Join([]string{
		"APEX Load analysis tables", "",
		"Each CSV uses one row per entity and includes run_id for joins. Empty tables retain their column headers.",
		"Time is UTC in RFC 3339. Latency fields ending _ms are milliseconds; _pct fields are percentages, not fractions.",
		"Peak TPS is successful named transactions per second. Captured records include the archive population.",
		"The legacy report CSV remains a separate section/key/metric/value export for existing integrations.",
		"", "Profile: " + profile.Label, "Run ID: " + runID, "",
	}, "\n"))
	if err != nil {
		_ = archive.Close()
		return err
	}
	return archive.Close()
}

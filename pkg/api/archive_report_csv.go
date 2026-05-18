package api

import (
	"encoding/csv"
	"io"
	"sort"
	"strconv"
)

func writeRunArchiveReportCSV(report *RunArchiveReport, writer io.Writer) error {
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
	if err := write("overview", "", "schema_version", report.SchemaVersion); err != nil {
		return err
	}
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
	csvWriter.Flush()
	return csvWriter.Error()
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
		{"min_response_ms", formatCSVFloat(stats.MinResponseMs)},
		{"max_response_ms", formatCSVFloat(stats.MaxResponseMs)},
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

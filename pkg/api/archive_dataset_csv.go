package api

import (
	"encoding/csv"
	"encoding/json"
	"io"
	"strconv"
	"time"
)

func writeArchiveTransactionsCSV(items []TransactionEvent, writer io.Writer) error {
	csvWriter := csv.NewWriter(writer)
	if err := csvWriter.Write([]string{
		"timestamp",
		"test_id",
		"test_name",
		"group",
		"action_name",
		"correlation_id",
		"trace_id",
		"span_id",
		"network_profile",
		"dependency_type",
		"dependency_target",
		"duration_ms",
		"dns_ms",
		"connect_ms",
		"tls_ms",
		"ttfb_ms",
		"bytes_sent",
		"bytes_received",
		"status_code",
		"success",
		"error_message",
		"page_url",
		"page_title",
		"extractions_json",
	}); err != nil {
		return err
	}
	for _, item := range items {
		if err := csvWriter.Write([]string{
			formatArchiveCSVTime(item.Timestamp),
			item.TestID,
			item.TestName,
			item.Group,
			item.ActionName,
			item.CorrelationID,
			item.TraceID,
			item.SpanID,
			item.NetworkProfile,
			item.DependencyType,
			item.DependencyTarget,
			formatCSVFloat(item.DurationMs),
			formatCSVFloat(item.DNSMs),
			formatCSVFloat(item.ConnectMs),
			formatCSVFloat(item.TLSMs),
			formatCSVFloat(item.TTFBMs),
			formatCSVInt(item.BytesSent),
			formatCSVInt(item.BytesReceived),
			strconv.Itoa(item.StatusCode),
			strconv.FormatBool(item.Success),
			item.ErrorMessage,
			item.PageURL,
			item.PageTitle,
			formatArchiveCSVJSON(item.Extractions),
		}); err != nil {
			return err
		}
	}
	csvWriter.Flush()
	return csvWriter.Error()
}

func writeArchiveResourcesCSV(items []ResourceSample, writer io.Writer) error {
	csvWriter := csv.NewWriter(writer)
	if err := csvWriter.Write([]string{
		"timestamp",
		"test_id",
		"test_name",
		"active_vusers",
		"cpu_process_pct",
		"cpu_free_pct",
		"mem_process_mb",
		"mem_free_mb",
		"net_sent_kbps",
		"net_recv_kbps",
		"disk_read_kbps",
		"disk_write_kbps",
		"external_metrics_json",
	}); err != nil {
		return err
	}
	for _, item := range items {
		if err := csvWriter.Write([]string{
			formatArchiveCSVTime(item.Timestamp),
			item.TestID,
			item.TestName,
			formatCSVInt(item.ActiveVUsers),
			formatCSVFloat(item.CPUProcessPct),
			formatCSVFloat(item.CPUFreePct),
			formatCSVFloat(item.MemProcessMB),
			formatCSVFloat(item.MemFreeMB),
			formatCSVFloat(item.NetSentKbps),
			formatCSVFloat(item.NetRecvKbps),
			formatCSVFloat(item.DiskReadKbps),
			formatCSVFloat(item.DiskWriteKbps),
			formatArchiveCSVJSON(item.External),
		}); err != nil {
			return err
		}
	}
	csvWriter.Flush()
	return csvWriter.Error()
}

func writeArchiveSummariesCSV(items []IntervalSummaryRecord, writer io.Writer) error {
	csvWriter := csv.NewWriter(writer)
	if err := csvWriter.Write([]string{
		"timestamp",
		"test_id",
		"test_name",
		"active_vusers",
		"tps",
		"target_tps",
		"throughput_gap_pct",
		"throughput_target_missed",
		"avg_response_ms",
		"error_pct",
		"total_transactions",
		"total_errors",
		"p50_ms",
		"p90_ms",
		"p95_ms",
		"p99_ms",
		"browser_json",
		"telemetry_json",
	}); err != nil {
		return err
	}
	for _, item := range items {
		if err := csvWriter.Write([]string{
			formatArchiveCSVTime(item.Timestamp),
			item.TestID,
			item.TestName,
			formatCSVInt(item.ActiveVUsers),
			formatCSVFloat(item.TPS),
			formatCSVFloat(item.TargetTPS),
			formatCSVFloat(item.ThroughputGapPct),
			strconv.FormatBool(item.ThroughputTargetMissed),
			formatCSVFloat(item.AvgResponseMs),
			formatCSVFloat(item.ErrorPct),
			formatCSVInt(item.TotalTx),
			formatCSVInt(item.TotalErrors),
			formatCSVFloat(item.P50Ms),
			formatCSVFloat(item.P90Ms),
			formatCSVFloat(item.P95Ms),
			formatCSVFloat(item.P99Ms),
			formatArchiveCSVJSON(item.Browser),
			formatArchiveCSVJSON(item.Telemetry),
		}); err != nil {
			return err
		}
	}
	csvWriter.Flush()
	return csvWriter.Error()
}

func formatArchiveCSVTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(time.RFC3339Nano)
}

func formatArchiveCSVJSON(value any) string {
	if value == nil {
		return ""
	}
	encoded, err := json.Marshal(value)
	if err != nil || string(encoded) == "null" {
		return ""
	}
	return string(encoded)
}

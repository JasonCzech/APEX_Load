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
		"cpu_user_pct",
		"cpu_system_pct",
		"cpu_iowait_pct",
		"cpu_steal_pct",
		"load1",
		"load5",
		"load15",
		"thread_count",
		"open_fd_count",
		"goroutine_count",
		"heap_alloc_mb",
		"heap_in_use_mb",
		"gc_count_delta",
		"gc_pause_ms",
		"container_cpu_limit_cores",
		"container_cpu_usage_pct",
		"container_cpu_throttled_periods_delta",
		"container_cpu_throttled_seconds_rate",
		"container_memory_working_set_mb",
		"container_memory_limit_mb",
		"container_memory_usage_pct",
		"container_memory_fail_count_delta",
		"container_oom_kill_count_delta",
		"net_drop_in_per_sec",
		"net_drop_out_per_sec",
		"net_error_in_per_sec",
		"net_error_out_per_sec",
		"tcp_retransmits_per_sec",
		"tcp_established",
		"tcp_time_wait",
		"ephemeral_port_utilization_pct",
		"disk_io_time_pct",
		"disk_weighted_io_time_pct",
		"disk_in_progress",
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
			formatCSVFloat(item.CPUUserPct),
			formatCSVFloat(item.CPUSystemPct),
			formatCSVFloat(item.CPUIOWaitPct),
			formatCSVFloat(item.CPUStealPct),
			formatCSVFloat(item.Load1),
			formatCSVFloat(item.Load5),
			formatCSVFloat(item.Load15),
			formatCSVInt(int64(item.ThreadCount)),
			formatCSVInt(int64(item.OpenFDCount)),
			formatCSVInt(int64(item.GoroutineCount)),
			formatCSVFloat(item.HeapAllocMB),
			formatCSVFloat(item.HeapInUseMB),
			formatCSVInt(int64(item.GCCountDelta)),
			formatCSVFloat(item.GCPauseMs),
			formatCSVFloat(item.ContainerCPULimitCores),
			formatCSVFloat(item.ContainerCPUUsagePct),
			formatCSVInt(int64(item.ContainerCPUThrottledPeriodsDelta)),
			formatCSVFloat(item.ContainerCPUThrottledSecondsRate),
			formatCSVFloat(item.ContainerMemoryWorkingSetMB),
			formatCSVFloat(item.ContainerMemoryLimitMB),
			formatCSVFloat(item.ContainerMemoryUsagePct),
			formatCSVInt(int64(item.ContainerMemoryFailCountDelta)),
			formatCSVInt(int64(item.ContainerOOMKillCountDelta)),
			formatCSVFloat(item.NetDropInPerSec),
			formatCSVFloat(item.NetDropOutPerSec),
			formatCSVFloat(item.NetErrorInPerSec),
			formatCSVFloat(item.NetErrorOutPerSec),
			formatCSVFloat(item.TCPRetransmitsPerSec),
			formatCSVInt(int64(item.TCPEstablished)),
			formatCSVInt(int64(item.TCPTimeWait)),
			formatCSVFloat(item.EphemeralPortUtilizationPct),
			formatCSVFloat(item.DiskIOTimePct),
			formatCSVFloat(item.DiskWeightedIOTimePct),
			formatCSVInt(int64(item.DiskInProgress)),
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

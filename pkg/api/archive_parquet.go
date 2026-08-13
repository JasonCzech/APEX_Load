package api

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	local "github.com/xitongsys/parquet-go-source/local"
	"github.com/xitongsys/parquet-go/parquet"
	"github.com/xitongsys/parquet-go/writer"
)

type parquetTransactionEvent struct {
	TimestampUnixMs     int64   `parquet:"name=timestamp_unix_ms, type=INT64"`
	TimestampRFC3339    string  `parquet:"name=timestamp_rfc3339, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	TestID              string  `parquet:"name=test_id, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	TestName            string  `parquet:"name=test_name, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	Group               string  `parquet:"name=group_name, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	Region              string  `parquet:"name=region, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	ActionName          string  `parquet:"name=action_name, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	BusinessTransaction bool    `parquet:"name=business_transaction, type=BOOLEAN"`
	CorrelationID       string  `parquet:"name=correlation_id, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	TraceID             string  `parquet:"name=trace_id, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	SpanID              string  `parquet:"name=span_id, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	NetworkProfile      string  `parquet:"name=network_profile, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	DependencyType      string  `parquet:"name=dependency_type, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	DependencyTarget    string  `parquet:"name=dependency_target, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	DurationMs          float64 `parquet:"name=duration_ms, type=DOUBLE"`
	DNSMs               float64 `parquet:"name=dns_ms, type=DOUBLE"`
	ConnectMs           float64 `parquet:"name=connect_ms, type=DOUBLE"`
	TLSMs               float64 `parquet:"name=tls_ms, type=DOUBLE"`
	TTFBMs              float64 `parquet:"name=ttfb_ms, type=DOUBLE"`
	BytesSent           int64   `parquet:"name=bytes_sent, type=INT64"`
	BytesReceived       int64   `parquet:"name=bytes_received, type=INT64"`
	StatusCode          int64   `parquet:"name=status_code, type=INT64"`
	Success             bool    `parquet:"name=success, type=BOOLEAN"`
	ErrorMessage        string  `parquet:"name=error_message, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	ExtractionsJSON     string  `parquet:"name=extractions_json, type=BYTE_ARRAY, convertedtype=UTF8"`
	ServerTelemetryJSON string  `parquet:"name=server_telemetry_json, type=BYTE_ARRAY, convertedtype=UTF8"`
	SnapshotJSON        string  `parquet:"name=snapshot_json, type=BYTE_ARRAY, convertedtype=UTF8"`
}

type parquetResourceSample struct {
	TimestampUnixMs                   int64   `parquet:"name=timestamp_unix_ms, type=INT64"`
	TimestampRFC3339                  string  `parquet:"name=timestamp_rfc3339, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	TestID                            string  `parquet:"name=test_id, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	TestName                          string  `parquet:"name=test_name, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	ActiveVUsers                      int64   `parquet:"name=active_vusers, type=INT64"`
	CPUProcessPct                     float64 `parquet:"name=cpu_process_pct, type=DOUBLE"`
	CPUFreePct                        float64 `parquet:"name=cpu_free_pct, type=DOUBLE"`
	MemProcessMB                      float64 `parquet:"name=mem_process_mb, type=DOUBLE"`
	MemFreeMB                         float64 `parquet:"name=mem_free_mb, type=DOUBLE"`
	NetSentKbps                       float64 `parquet:"name=net_sent_kbps, type=DOUBLE"`
	NetRecvKbps                       float64 `parquet:"name=net_recv_kbps, type=DOUBLE"`
	DiskReadKbps                      float64 `parquet:"name=disk_read_kbps, type=DOUBLE"`
	DiskWriteKbps                     float64 `parquet:"name=disk_write_kbps, type=DOUBLE"`
	CPUUserPct                        float64 `parquet:"name=cpu_user_pct, type=DOUBLE"`
	CPUSystemPct                      float64 `parquet:"name=cpu_system_pct, type=DOUBLE"`
	CPUIOWaitPct                      float64 `parquet:"name=cpu_iowait_pct, type=DOUBLE"`
	CPUStealPct                       float64 `parquet:"name=cpu_steal_pct, type=DOUBLE"`
	Load1                             float64 `parquet:"name=load1, type=DOUBLE"`
	Load5                             float64 `parquet:"name=load5, type=DOUBLE"`
	Load15                            float64 `parquet:"name=load15, type=DOUBLE"`
	ThreadCount                       int32   `parquet:"name=thread_count, type=INT32"`
	OpenFDCount                       int32   `parquet:"name=open_fd_count, type=INT32"`
	GoroutineCount                    int32   `parquet:"name=goroutine_count, type=INT32"`
	HeapAllocMB                       float64 `parquet:"name=heap_alloc_mb, type=DOUBLE"`
	HeapInUseMB                       float64 `parquet:"name=heap_in_use_mb, type=DOUBLE"`
	GCCountDelta                      int64   `parquet:"name=gc_count_delta, type=INT64"`
	GCPauseMs                         float64 `parquet:"name=gc_pause_ms, type=DOUBLE"`
	ContainerCPULimitCores            float64 `parquet:"name=container_cpu_limit_cores, type=DOUBLE"`
	ContainerCPUUsagePct              float64 `parquet:"name=container_cpu_usage_pct, type=DOUBLE"`
	ContainerCPUThrottledPeriodsDelta int64   `parquet:"name=container_cpu_throttled_periods_delta, type=INT64"`
	ContainerCPUThrottledSecondsRate  float64 `parquet:"name=container_cpu_throttled_seconds_rate, type=DOUBLE"`
	ContainerMemoryWorkingSetMB       float64 `parquet:"name=container_memory_working_set_mb, type=DOUBLE"`
	ContainerMemoryLimitMB            float64 `parquet:"name=container_memory_limit_mb, type=DOUBLE"`
	ContainerMemoryUsagePct           float64 `parquet:"name=container_memory_usage_pct, type=DOUBLE"`
	ContainerMemoryFailCountDelta     int64   `parquet:"name=container_memory_fail_count_delta, type=INT64"`
	ContainerOOMKillCountDelta        int64   `parquet:"name=container_oom_kill_count_delta, type=INT64"`
	NetDropInPerSec                   float64 `parquet:"name=net_drop_in_per_sec, type=DOUBLE"`
	NetDropOutPerSec                  float64 `parquet:"name=net_drop_out_per_sec, type=DOUBLE"`
	NetErrorInPerSec                  float64 `parquet:"name=net_error_in_per_sec, type=DOUBLE"`
	NetErrorOutPerSec                 float64 `parquet:"name=net_error_out_per_sec, type=DOUBLE"`
	TCPRetransmitsPerSec              float64 `parquet:"name=tcp_retransmits_per_sec, type=DOUBLE"`
	TCPEstablished                    int32   `parquet:"name=tcp_established, type=INT32"`
	TCPTimeWait                       int32   `parquet:"name=tcp_time_wait, type=INT32"`
	EphemeralPortUtilizationPct       float64 `parquet:"name=ephemeral_port_utilization_pct, type=DOUBLE"`
	DiskIOTimePct                     float64 `parquet:"name=disk_io_time_pct, type=DOUBLE"`
	DiskWeightedIOTimePct             float64 `parquet:"name=disk_weighted_io_time_pct, type=DOUBLE"`
	DiskInProgress                    int64   `parquet:"name=disk_in_progress, type=INT64"`
	ExternalJSON                      string  `parquet:"name=external_metrics_json, type=BYTE_ARRAY, convertedtype=UTF8"`
}

type parquetIntervalSummary struct {
	TimestampUnixMs        int64   `parquet:"name=timestamp_unix_ms, type=INT64"`
	TimestampRFC3339       string  `parquet:"name=timestamp_rfc3339, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	TestID                 string  `parquet:"name=test_id, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	TestName               string  `parquet:"name=test_name, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	ActiveVUsers           int64   `parquet:"name=active_vusers, type=INT64"`
	TPS                    float64 `parquet:"name=tps, type=DOUBLE"`
	TargetTPS              float64 `parquet:"name=target_tps, type=DOUBLE"`
	ThroughputGapPct       float64 `parquet:"name=throughput_gap_pct, type=DOUBLE"`
	ThroughputTargetMissed bool    `parquet:"name=throughput_target_missed, type=BOOLEAN"`
	AvgResponseMs          float64 `parquet:"name=avg_response_ms, type=DOUBLE"`
	ErrorPct               float64 `parquet:"name=error_pct, type=DOUBLE"`
	TotalTx                int64   `parquet:"name=total_transactions, type=INT64"`
	TotalErrors            int64   `parquet:"name=total_errors, type=INT64"`
	P50Ms                  float64 `parquet:"name=p50_ms, type=DOUBLE"`
	P90Ms                  float64 `parquet:"name=p90_ms, type=DOUBLE"`
	P95Ms                  float64 `parquet:"name=p95_ms, type=DOUBLE"`
	P99Ms                  float64 `parquet:"name=p99_ms, type=DOUBLE"`
	PercentileQuality      string  `parquet:"name=percentile_quality, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	GroupsJSON             string  `parquet:"name=groups_json, type=BYTE_ARRAY, convertedtype=UTF8"`
	TransactionsJSON       string  `parquet:"name=transactions_json, type=BYTE_ARRAY, convertedtype=UTF8"`
	BrowserJSON            string  `parquet:"name=browser_json, type=BYTE_ARRAY, convertedtype=UTF8"`
	BrowserDeviceJSON      string  `parquet:"name=browser_by_device_json, type=BYTE_ARRAY, convertedtype=UTF8"`
	TelemetryJSON          string  `parquet:"name=telemetry_json, type=BYTE_ARRAY, convertedtype=UTF8"`
	TelemetryStatusJSON    string  `parquet:"name=telemetry_status_json, type=BYTE_ARRAY, convertedtype=UTF8"`
}

func writeArchiveParquetFiles(rootDir string, manifest *RunArchiveManifest) error {
	count, err := streamJSONLToParquet(
		filepath.Join(rootDir, manifest.TransactionsFile),
		filepath.Join(rootDir, manifest.TransactionsParquetFile),
		func(item TransactionEvent) parquetTransactionEvent {
			return toParquetTransactions([]TransactionEvent{item})[0]
		},
	)
	if err != nil {
		return fmt.Errorf("write transaction parquet: %w", err)
	}
	if count != manifest.TransactionCount {
		return fmt.Errorf("transaction archive count is %d, want %d", count, manifest.TransactionCount)
	}
	count, err = streamJSONLToParquet(
		filepath.Join(rootDir, manifest.ResourceSamplesFile),
		filepath.Join(rootDir, manifest.ResourceSamplesParquetFile),
		func(item ResourceSample) parquetResourceSample {
			return toParquetResources([]ResourceSample{item})[0]
		},
	)
	if err != nil {
		return fmt.Errorf("write resource parquet: %w", err)
	}
	if count != manifest.ResourceSampleCount {
		return fmt.Errorf("resource archive count is %d, want %d", count, manifest.ResourceSampleCount)
	}
	count, err = streamJSONLToParquet(
		filepath.Join(rootDir, manifest.IntervalSummariesFile),
		filepath.Join(rootDir, manifest.IntervalSummariesParquetFile),
		func(item IntervalSummaryRecord) parquetIntervalSummary {
			return toParquetSummaries([]IntervalSummaryRecord{item})[0]
		},
	)
	if err != nil {
		return fmt.Errorf("write summary parquet: %w", err)
	}
	if count != manifest.IntervalSummaryCount {
		return fmt.Errorf("interval summary archive count is %d, want %d", count, manifest.IntervalSummaryCount)
	}
	return nil
}

// streamJSONLToParquet keeps memory proportional to one bounded archive record.
// Error snapshots can legitimately exceed Scanner's small default token size,
// so use the repository's 64 MiB JSONL ceiling rather than decoding a record
// with no allocation bound.
func streamJSONLToParquet[In any, Out any](inputPath string, outputPath string, convert func(In) Out) (count int64, err error) {
	input, err := os.Open(inputPath)
	if err != nil {
		return 0, err
	}
	defer input.Close()

	fw, err := local.NewLocalFileWriter(outputPath)
	if err != nil {
		return 0, err
	}
	pw, err := writer.NewParquetWriter(fw, new(Out), 4)
	if err != nil {
		_ = fw.Close()
		return 0, err
	}
	pw.CompressionType = parquet.CompressionCodec_SNAPPY
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 0, 64*1024), maxArchiveJSONLRecordBytes)
	for scanner.Scan() {
		var item In
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
			_ = pw.WriteStop()
			_ = fw.Close()
			return count, err
		}
		if err := pw.Write(convert(item)); err != nil {
			_ = pw.WriteStop()
			_ = fw.Close()
			return count, err
		}
		count++
	}
	if err := scanner.Err(); err != nil {
		_ = pw.WriteStop()
		_ = fw.Close()
		return count, err
	}
	if err := pw.WriteStop(); err != nil {
		_ = fw.Close()
		return count, err
	}
	return count, fw.Close()
}

func toParquetTransactions(items []TransactionEvent) []parquetTransactionEvent {
	rows := make([]parquetTransactionEvent, 0, len(items))
	for _, item := range items {
		rows = append(rows, parquetTransactionEvent{
			TimestampUnixMs:     item.Timestamp.UnixMilli(),
			TimestampRFC3339:    item.Timestamp.Format(time.RFC3339Nano),
			TestID:              item.TestID,
			TestName:            item.TestName,
			Group:               item.Group,
			Region:              item.Region,
			ActionName:          item.ActionName,
			BusinessTransaction: item.BusinessTransaction,
			CorrelationID:       item.CorrelationID,
			TraceID:             item.TraceID,
			SpanID:              item.SpanID,
			NetworkProfile:      item.NetworkProfile,
			DependencyType:      item.DependencyType,
			DependencyTarget:    item.DependencyTarget,
			DurationMs:          item.DurationMs,
			DNSMs:               item.DNSMs,
			ConnectMs:           item.ConnectMs,
			TLSMs:               item.TLSMs,
			TTFBMs:              item.TTFBMs,
			BytesSent:           item.BytesSent,
			BytesReceived:       item.BytesReceived,
			StatusCode:          int64(item.StatusCode),
			Success:             item.Success,
			ErrorMessage:        item.ErrorMessage,
			ExtractionsJSON:     mustJSON(item.Extractions),
			ServerTelemetryJSON: mustJSON(item.ServerTelemetry),
			SnapshotJSON:        mustJSON(item.Snapshot),
		})
	}
	return rows
}

func toParquetResources(items []ResourceSample) []parquetResourceSample {
	rows := make([]parquetResourceSample, 0, len(items))
	for _, item := range items {
		rows = append(rows, parquetResourceSample{
			TimestampUnixMs:                   item.Timestamp.UnixMilli(),
			TimestampRFC3339:                  item.Timestamp.Format(time.RFC3339Nano),
			TestID:                            item.TestID,
			TestName:                          item.TestName,
			ActiveVUsers:                      item.ActiveVUsers,
			CPUProcessPct:                     item.CPUProcessPct,
			CPUFreePct:                        item.CPUFreePct,
			MemProcessMB:                      item.MemProcessMB,
			MemFreeMB:                         item.MemFreeMB,
			NetSentKbps:                       item.NetSentKbps,
			NetRecvKbps:                       item.NetRecvKbps,
			DiskReadKbps:                      item.DiskReadKbps,
			DiskWriteKbps:                     item.DiskWriteKbps,
			CPUUserPct:                        item.CPUUserPct,
			CPUSystemPct:                      item.CPUSystemPct,
			CPUIOWaitPct:                      item.CPUIOWaitPct,
			CPUStealPct:                       item.CPUStealPct,
			Load1:                             item.Load1,
			Load5:                             item.Load5,
			Load15:                            item.Load15,
			ThreadCount:                       item.ThreadCount,
			OpenFDCount:                       item.OpenFDCount,
			GoroutineCount:                    item.GoroutineCount,
			HeapAllocMB:                       item.HeapAllocMB,
			HeapInUseMB:                       item.HeapInUseMB,
			GCCountDelta:                      int64(item.GCCountDelta),
			GCPauseMs:                         item.GCPauseMs,
			ContainerCPULimitCores:            item.ContainerCPULimitCores,
			ContainerCPUUsagePct:              item.ContainerCPUUsagePct,
			ContainerCPUThrottledPeriodsDelta: int64(item.ContainerCPUThrottledPeriodsDelta),
			ContainerCPUThrottledSecondsRate:  item.ContainerCPUThrottledSecondsRate,
			ContainerMemoryWorkingSetMB:       item.ContainerMemoryWorkingSetMB,
			ContainerMemoryLimitMB:            item.ContainerMemoryLimitMB,
			ContainerMemoryUsagePct:           item.ContainerMemoryUsagePct,
			ContainerMemoryFailCountDelta:     int64(item.ContainerMemoryFailCountDelta),
			ContainerOOMKillCountDelta:        int64(item.ContainerOOMKillCountDelta),
			NetDropInPerSec:                   item.NetDropInPerSec,
			NetDropOutPerSec:                  item.NetDropOutPerSec,
			NetErrorInPerSec:                  item.NetErrorInPerSec,
			NetErrorOutPerSec:                 item.NetErrorOutPerSec,
			TCPRetransmitsPerSec:              item.TCPRetransmitsPerSec,
			TCPEstablished:                    int32(item.TCPEstablished),
			TCPTimeWait:                       int32(item.TCPTimeWait),
			EphemeralPortUtilizationPct:       item.EphemeralPortUtilizationPct,
			DiskIOTimePct:                     item.DiskIOTimePct,
			DiskWeightedIOTimePct:             item.DiskWeightedIOTimePct,
			DiskInProgress:                    int64(item.DiskInProgress),
			ExternalJSON:                      mustJSON(item.External),
		})
	}
	return rows
}

func toParquetSummaries(items []IntervalSummaryRecord) []parquetIntervalSummary {
	rows := make([]parquetIntervalSummary, 0, len(items))
	for _, item := range items {
		rows = append(rows, parquetIntervalSummary{
			TimestampUnixMs:        item.Timestamp.UnixMilli(),
			TimestampRFC3339:       item.Timestamp.Format(time.RFC3339Nano),
			TestID:                 item.TestID,
			TestName:               item.TestName,
			ActiveVUsers:           item.ActiveVUsers,
			TPS:                    item.TPS,
			TargetTPS:              item.TargetTPS,
			ThroughputGapPct:       item.ThroughputGapPct,
			ThroughputTargetMissed: item.ThroughputTargetMissed,
			AvgResponseMs:          item.AvgResponseMs,
			ErrorPct:               item.ErrorPct,
			TotalTx:                item.TotalTx,
			TotalErrors:            item.TotalErrors,
			P50Ms:                  item.P50Ms,
			P90Ms:                  item.P90Ms,
			P95Ms:                  item.P95Ms,
			P99Ms:                  item.P99Ms,
			PercentileQuality:      item.PercentileQuality,
			GroupsJSON:             mustJSON(item.Groups),
			TransactionsJSON:       mustJSON(item.Transactions),
			BrowserJSON:            mustJSON(item.Browser),
			BrowserDeviceJSON:      mustJSON(item.BrowserByDevice),
			TelemetryJSON:          mustJSON(item.Telemetry),
			TelemetryStatusJSON:    mustJSON(item.TelemetryStatus),
		})
	}
	return rows
}

func mustJSON(value any) string {
	if value == nil {
		return ""
	}
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(data)
}

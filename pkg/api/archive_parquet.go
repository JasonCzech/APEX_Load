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
	ActionName          string  `parquet:"name=action_name, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	CorrelationID       string  `parquet:"name=correlation_id, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	TraceID             string  `parquet:"name=trace_id, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	SpanID              string  `parquet:"name=span_id, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
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
	TimestampUnixMs  int64   `parquet:"name=timestamp_unix_ms, type=INT64"`
	TimestampRFC3339 string  `parquet:"name=timestamp_rfc3339, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	TestID           string  `parquet:"name=test_id, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	TestName         string  `parquet:"name=test_name, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	ActiveVUsers     int64   `parquet:"name=active_vusers, type=INT64"`
	CPUProcessPct    float64 `parquet:"name=cpu_process_pct, type=DOUBLE"`
	CPUFreePct       float64 `parquet:"name=cpu_free_pct, type=DOUBLE"`
	MemProcessMB     float64 `parquet:"name=mem_process_mb, type=DOUBLE"`
	MemFreeMB        float64 `parquet:"name=mem_free_mb, type=DOUBLE"`
	NetSentKbps      float64 `parquet:"name=net_sent_kbps, type=DOUBLE"`
	NetRecvKbps      float64 `parquet:"name=net_recv_kbps, type=DOUBLE"`
	DiskReadKbps     float64 `parquet:"name=disk_read_kbps, type=DOUBLE"`
	DiskWriteKbps    float64 `parquet:"name=disk_write_kbps, type=DOUBLE"`
	ExternalJSON     string  `parquet:"name=external_metrics_json, type=BYTE_ARRAY, convertedtype=UTF8"`
}

type parquetIntervalSummary struct {
	TimestampUnixMs  int64   `parquet:"name=timestamp_unix_ms, type=INT64"`
	TimestampRFC3339 string  `parquet:"name=timestamp_rfc3339, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	TestID           string  `parquet:"name=test_id, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	TestName         string  `parquet:"name=test_name, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	ActiveVUsers     int64   `parquet:"name=active_vusers, type=INT64"`
	TPS              float64 `parquet:"name=tps, type=DOUBLE"`
	AvgResponseMs    float64 `parquet:"name=avg_response_ms, type=DOUBLE"`
	ErrorPct         float64 `parquet:"name=error_pct, type=DOUBLE"`
	TotalTx          int64   `parquet:"name=total_transactions, type=INT64"`
	TotalErrors      int64   `parquet:"name=total_errors, type=INT64"`
	P50Ms            float64 `parquet:"name=p50_ms, type=DOUBLE"`
	P90Ms            float64 `parquet:"name=p90_ms, type=DOUBLE"`
	P95Ms            float64 `parquet:"name=p95_ms, type=DOUBLE"`
	P99Ms            float64 `parquet:"name=p99_ms, type=DOUBLE"`
}

func writeArchiveParquetFiles(rootDir string, manifest *RunArchiveManifest) error {
	transactions, err := readAllJSONLRecords[TransactionEvent](filepath.Join(rootDir, manifest.TransactionsFile))
	if err != nil {
		return fmt.Errorf("read transaction jsonl for parquet export: %w", err)
	}
	resources, err := readAllJSONLRecords[ResourceSample](filepath.Join(rootDir, manifest.ResourceSamplesFile))
	if err != nil {
		return fmt.Errorf("read resource jsonl for parquet export: %w", err)
	}
	summaries, err := readAllJSONLRecords[IntervalSummaryRecord](filepath.Join(rootDir, manifest.IntervalSummariesFile))
	if err != nil {
		return fmt.Errorf("read summary jsonl for parquet export: %w", err)
	}

	if err := writeParquetFile(filepath.Join(rootDir, manifest.TransactionsParquetFile), toParquetTransactions(transactions)); err != nil {
		return fmt.Errorf("write transaction parquet: %w", err)
	}
	if err := writeParquetFile(filepath.Join(rootDir, manifest.ResourceSamplesParquetFile), toParquetResources(resources)); err != nil {
		return fmt.Errorf("write resource parquet: %w", err)
	}
	if err := writeParquetFile(filepath.Join(rootDir, manifest.IntervalSummariesParquetFile), toParquetSummaries(summaries)); err != nil {
		return fmt.Errorf("write summary parquet: %w", err)
	}
	return nil
}

func writeParquetFile[T any](path string, rows []T) error {
	fw, err := local.NewLocalFileWriter(path)
	if err != nil {
		return err
	}
	pw, err := writer.NewParquetWriter(fw, new(T), 4)
	if err != nil {
		_ = fw.Close()
		return err
	}
	pw.CompressionType = parquet.CompressionCodec_SNAPPY
	for _, row := range rows {
		if err := pw.Write(row); err != nil {
			_ = pw.WriteStop()
			_ = fw.Close()
			return err
		}
	}
	if err := pw.WriteStop(); err != nil {
		_ = fw.Close()
		return err
	}
	return fw.Close()
}

func readAllJSONLRecords[T any](path string) ([]T, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 2*1024*1024)

	items := make([]T, 0)
	for scanner.Scan() {
		var item T
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return items, nil
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
			ActionName:          item.ActionName,
			CorrelationID:       item.CorrelationID,
			TraceID:             item.TraceID,
			SpanID:              item.SpanID,
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
			TimestampUnixMs:  item.Timestamp.UnixMilli(),
			TimestampRFC3339: item.Timestamp.Format(time.RFC3339Nano),
			TestID:           item.TestID,
			TestName:         item.TestName,
			ActiveVUsers:     item.ActiveVUsers,
			CPUProcessPct:    item.CPUProcessPct,
			CPUFreePct:       item.CPUFreePct,
			MemProcessMB:     item.MemProcessMB,
			MemFreeMB:        item.MemFreeMB,
			NetSentKbps:      item.NetSentKbps,
			NetRecvKbps:      item.NetRecvKbps,
			DiskReadKbps:     item.DiskReadKbps,
			DiskWriteKbps:    item.DiskWriteKbps,
			ExternalJSON:     mustJSON(item.External),
		})
	}
	return rows
}

func toParquetSummaries(items []IntervalSummaryRecord) []parquetIntervalSummary {
	rows := make([]parquetIntervalSummary, 0, len(items))
	for _, item := range items {
		rows = append(rows, parquetIntervalSummary{
			TimestampUnixMs:  item.Timestamp.UnixMilli(),
			TimestampRFC3339: item.Timestamp.Format(time.RFC3339Nano),
			TestID:           item.TestID,
			TestName:         item.TestName,
			ActiveVUsers:     item.ActiveVUsers,
			TPS:              item.TPS,
			AvgResponseMs:    item.AvgResponseMs,
			ErrorPct:         item.ErrorPct,
			TotalTx:          item.TotalTx,
			TotalErrors:      item.TotalErrors,
			P50Ms:            item.P50Ms,
			P90Ms:            item.P90Ms,
			P95Ms:            item.P95Ms,
			P99Ms:            item.P99Ms,
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

package generatorrpc

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jasonczech/stormrunner/pkg/engine"
	"github.com/jasonczech/stormrunner/pkg/protocol"
	"github.com/jasonczech/stormrunner/pkg/storage"
	local "github.com/xitongsys/parquet-go-source/local"
	"github.com/xitongsys/parquet-go/parquet"
	"github.com/xitongsys/parquet-go/writer"
)

const archiveSchemaVersion = "stormrunner.archive.v1"

type archiveManifest struct {
	SchemaVersion                string    `json:"schema_version"`
	TestID                       string    `json:"test_id"`
	TestName                     string    `json:"test_name"`
	CreatedAt                    time.Time `json:"created_at"`
	CompletedAt                  time.Time `json:"completed_at,omitempty"`
	Status                       string    `json:"status,omitempty"`
	Error                        string    `json:"error,omitempty"`
	TransactionsFile             string    `json:"transactions_file"`
	TransactionsParquetFile      string    `json:"transactions_parquet_file,omitempty"`
	ResourceSamplesFile          string    `json:"resource_samples_file"`
	ResourceSamplesParquetFile   string    `json:"resource_samples_parquet_file,omitempty"`
	IntervalSummariesFile        string    `json:"interval_summaries_file"`
	IntervalSummariesParquetFile string    `json:"interval_summaries_parquet_file,omitempty"`
	TransactionCount             int64     `json:"transaction_count"`
	ResourceSampleCount          int64     `json:"resource_sample_count"`
	IntervalSummaryCount         int64     `json:"interval_summary_count"`
}

type archiveTransactionEvent struct {
	Timestamp     time.Time               `json:"timestamp"`
	TestID        string                  `json:"test_id"`
	TestName      string                  `json:"test_name"`
	Group         string                  `json:"group,omitempty"`
	ActionName    string                  `json:"action_name,omitempty"`
	DurationMs    float64                 `json:"duration_ms"`
	DNSMs         float64                 `json:"dns_ms,omitempty"`
	ConnectMs     float64                 `json:"connect_ms,omitempty"`
	TLSMs         float64                 `json:"tls_ms,omitempty"`
	TTFBMs        float64                 `json:"ttfb_ms,omitempty"`
	BytesSent     int64                   `json:"bytes_sent,omitempty"`
	BytesReceived int64                   `json:"bytes_received,omitempty"`
	StatusCode    int                     `json:"status_code,omitempty"`
	Success       bool                    `json:"success"`
	ErrorMessage  string                  `json:"error_message,omitempty"`
	Extractions   map[string]string       `json:"extractions,omitempty"`
	Snapshot      *protocol.ErrorSnapshot `json:"snapshot,omitempty"`
}

type archiveResourceSample struct {
	Timestamp     time.Time `json:"timestamp"`
	TestID        string    `json:"test_id"`
	TestName      string    `json:"test_name"`
	ActiveVUsers  int64     `json:"active_vusers"`
	CPUProcessPct float64   `json:"cpu_process_pct"`
	CPUFreePct    float64   `json:"cpu_free_pct"`
	MemProcessMB  float64   `json:"mem_process_mb"`
	MemFreeMB     float64   `json:"mem_free_mb"`
	NetSentKbps   float64   `json:"net_sent_kbps"`
	NetRecvKbps   float64   `json:"net_recv_kbps"`
	DiskReadKbps  float64   `json:"disk_read_kbps"`
	DiskWriteKbps float64   `json:"disk_write_kbps"`
}

type archiveIntervalSummary struct {
	Timestamp     time.Time `json:"timestamp"`
	TestID        string    `json:"test_id"`
	TestName      string    `json:"test_name"`
	ActiveVUsers  int64     `json:"active_vusers"`
	TPS           float64   `json:"tps"`
	AvgResponseMs float64   `json:"avg_response_ms"`
	ErrorPct      float64   `json:"error_pct"`
	TotalTx       int64     `json:"total_transactions"`
	TotalErrors   int64     `json:"total_errors"`
	P50Ms         float64   `json:"p50_ms"`
	P90Ms         float64   `json:"p90_ms"`
	P95Ms         float64   `json:"p95_ms"`
	P99Ms         float64   `json:"p99_ms"`
}

type archiveRecorder struct {
	mu              sync.Mutex
	manifest        archiveManifest
	rootDir         string
	manifestPath    string
	txFile          *os.File
	resourceFile    *os.File
	summaryFile     *os.File
	txEncoder       *json.Encoder
	resourceEncoder *json.Encoder
	summaryEncoder  *json.Encoder
}

type parquetTransactionEvent struct {
	TimestampUnixMs  int64   `parquet:"name=timestamp_unix_ms, type=INT64"`
	TimestampRFC3339 string  `parquet:"name=timestamp_rfc3339, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	TestID           string  `parquet:"name=test_id, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	TestName         string  `parquet:"name=test_name, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	Group            string  `parquet:"name=group_name, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	ActionName       string  `parquet:"name=action_name, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	DurationMs       float64 `parquet:"name=duration_ms, type=DOUBLE"`
	DNSMs            float64 `parquet:"name=dns_ms, type=DOUBLE"`
	ConnectMs        float64 `parquet:"name=connect_ms, type=DOUBLE"`
	TLSMs            float64 `parquet:"name=tls_ms, type=DOUBLE"`
	TTFBMs           float64 `parquet:"name=ttfb_ms, type=DOUBLE"`
	BytesSent        int64   `parquet:"name=bytes_sent, type=INT64"`
	BytesReceived    int64   `parquet:"name=bytes_received, type=INT64"`
	StatusCode       int64   `parquet:"name=status_code, type=INT64"`
	Success          bool    `parquet:"name=success, type=BOOLEAN"`
	ErrorMessage     string  `parquet:"name=error_message, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	ExtractionsJSON  string  `parquet:"name=extractions_json, type=BYTE_ARRAY, convertedtype=UTF8"`
	SnapshotJSON     string  `parquet:"name=snapshot_json, type=BYTE_ARRAY, convertedtype=UTF8"`
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

func newArchiveRecorder(testID string, testName string) (*archiveRecorder, error) {
	rootDir := resolveArchiveRoot()
	archiveDir := filepath.Join(rootDir, testID)
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		return nil, fmt.Errorf("create archive dir: %w", err)
	}

	open := func(name string) (*os.File, *json.Encoder, error) {
		file, err := os.Create(filepath.Join(archiveDir, name))
		if err != nil {
			return nil, nil, err
		}
		return file, json.NewEncoder(file), nil
	}

	txFile, txEncoder, err := open("transactions.jsonl")
	if err != nil {
		return nil, fmt.Errorf("open transactions archive: %w", err)
	}
	resourceFile, resourceEncoder, err := open("resources.jsonl")
	if err != nil {
		_ = txFile.Close()
		return nil, fmt.Errorf("open resources archive: %w", err)
	}
	summaryFile, summaryEncoder, err := open("interval_summaries.jsonl")
	if err != nil {
		_ = txFile.Close()
		_ = resourceFile.Close()
		return nil, fmt.Errorf("open summaries archive: %w", err)
	}

	return &archiveRecorder{
		manifest: archiveManifest{
			SchemaVersion:                archiveSchemaVersion,
			TestID:                       testID,
			TestName:                     testName,
			CreatedAt:                    time.Now(),
			TransactionsFile:             "transactions.jsonl",
			TransactionsParquetFile:      "transactions.parquet",
			ResourceSamplesFile:          "resources.jsonl",
			ResourceSamplesParquetFile:   "resources.parquet",
			IntervalSummariesFile:        "interval_summaries.jsonl",
			IntervalSummariesParquetFile: "interval_summaries.parquet",
		},
		rootDir:         archiveDir,
		manifestPath:    filepath.Join(archiveDir, "manifest.json"),
		txFile:          txFile,
		resourceFile:    resourceFile,
		summaryFile:     summaryFile,
		txEncoder:       txEncoder,
		resourceEncoder: resourceEncoder,
		summaryEncoder:  summaryEncoder,
	}, nil
}

func (w *archiveRecorder) RecordResult(result *protocol.Result) error {
	if result == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	event := archiveTransactionEvent{
		Timestamp:     result.StartTime,
		TestID:        w.manifest.TestID,
		TestName:      w.manifest.TestName,
		Group:         result.Group,
		ActionName:    result.ActionName,
		DurationMs:    durationMillis(result.Duration),
		DNSMs:         durationMillis(result.DNSTime),
		ConnectMs:     durationMillis(result.ConnectTime),
		TLSMs:         durationMillis(result.TLSTime),
		TTFBMs:        durationMillis(result.TTFB),
		BytesSent:     result.BytesSent,
		BytesReceived: result.BytesReceived,
		StatusCode:    result.StatusCode,
		Success:       result.Success,
		ErrorMessage:  result.ErrorMessage,
		Extractions:   result.Extractions,
		Snapshot:      result.Snapshot,
	}
	if err := w.txEncoder.Encode(event); err != nil {
		return err
	}
	w.manifest.TransactionCount++
	return nil
}

func (w *archiveRecorder) RecordResourceSample(timestamp time.Time, activeVUsers int64, resources *engine.SystemResources) error {
	if resources == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	record := archiveResourceSample{
		Timestamp:     timestamp,
		TestID:        w.manifest.TestID,
		TestName:      w.manifest.TestName,
		ActiveVUsers:  activeVUsers,
		CPUProcessPct: resources.CPUProcessPct,
		CPUFreePct:    resources.CPUFreePct,
		MemProcessMB:  resources.MemProcessMb,
		MemFreeMB:     resources.MemFreeMb,
		NetSentKbps:   resources.NetSentKbps,
		NetRecvKbps:   resources.NetRecvKbps,
		DiskReadKbps:  resources.DiskReadKbps,
		DiskWriteKbps: resources.DiskWriteKbps,
	}
	if err := w.resourceEncoder.Encode(record); err != nil {
		return err
	}
	w.manifest.ResourceSampleCount++
	return nil
}

func (w *archiveRecorder) RecordIntervalSummary(summary archiveIntervalSummary) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	summary.TestID = w.manifest.TestID
	summary.TestName = w.manifest.TestName
	if err := w.summaryEncoder.Encode(summary); err != nil {
		return err
	}
	w.manifest.IntervalSummaryCount++
	return nil
}

func (w *archiveRecorder) Finalize(status string, completedAt time.Time, errStr string) (*storage.RunArchiveRecord, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.manifest.Status = status
	w.manifest.CompletedAt = completedAt
	w.manifest.Error = errStr

	for _, file := range []*os.File{w.txFile, w.resourceFile, w.summaryFile} {
		if file == nil {
			continue
		}
		if err := file.Sync(); err != nil {
			return nil, err
		}
		if err := file.Close(); err != nil {
			return nil, err
		}
	}
	if err := writeArchiveParquetFiles(w.rootDir, &w.manifest); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(w.manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(w.manifestPath, data, 0o644); err != nil {
		return nil, err
	}

	return &storage.RunArchiveRecord{
		TestID:               w.manifest.TestID,
		SchemaVersion:        w.manifest.SchemaVersion,
		ArchivePath:          w.rootDir,
		ManifestPath:         w.manifestPath,
		TransactionCount:     w.manifest.TransactionCount,
		ResourceSampleCount:  w.manifest.ResourceSampleCount,
		IntervalSummaryCount: w.manifest.IntervalSummaryCount,
		CreatedAt:            w.manifest.CreatedAt,
		CompletedAt:          w.manifest.CompletedAt,
	}, nil
}

func (w *archiveRecorder) ArtifactNames() []string {
	if w == nil {
		return nil
	}
	return archiveArtifactNames(&w.manifest)
}

func archiveArtifactNames(manifest *archiveManifest) []string {
	if manifest == nil {
		return nil
	}
	names := []string{"manifest.json", manifest.TransactionsFile, manifest.ResourceSamplesFile, manifest.IntervalSummariesFile}
	for _, name := range []string{manifest.TransactionsParquetFile, manifest.ResourceSamplesParquetFile, manifest.IntervalSummariesParquetFile} {
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}

func resolveArchiveRoot() string {
	if dir := os.Getenv("STORMRUNNER_ARCHIVE_DIR"); dir != "" {
		return dir
	}
	return filepath.Join("artifacts", "archives")
}

func durationMillis(duration time.Duration) float64 {
	if duration == 0 {
		return 0
	}
	return float64(duration) / float64(time.Millisecond)
}

func writeArchiveParquetFiles(rootDir string, manifest *archiveManifest) error {
	transactions, err := readAllJSONLRecords[archiveTransactionEvent](filepath.Join(rootDir, manifest.TransactionsFile))
	if err != nil {
		return fmt.Errorf("read transaction jsonl for parquet export: %w", err)
	}
	resources, err := readAllJSONLRecords[archiveResourceSample](filepath.Join(rootDir, manifest.ResourceSamplesFile))
	if err != nil {
		return fmt.Errorf("read resource jsonl for parquet export: %w", err)
	}
	summaries, err := readAllJSONLRecords[archiveIntervalSummary](filepath.Join(rootDir, manifest.IntervalSummariesFile))
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

func toParquetTransactions(items []archiveTransactionEvent) []parquetTransactionEvent {
	rows := make([]parquetTransactionEvent, 0, len(items))
	for _, item := range items {
		rows = append(rows, parquetTransactionEvent{
			TimestampUnixMs:  item.Timestamp.UnixMilli(),
			TimestampRFC3339: item.Timestamp.Format(time.RFC3339Nano),
			TestID:           item.TestID,
			TestName:         item.TestName,
			Group:            item.Group,
			ActionName:       item.ActionName,
			DurationMs:       item.DurationMs,
			DNSMs:            item.DNSMs,
			ConnectMs:        item.ConnectMs,
			TLSMs:            item.TLSMs,
			TTFBMs:           item.TTFBMs,
			BytesSent:        item.BytesSent,
			BytesReceived:    item.BytesReceived,
			StatusCode:       int64(item.StatusCode),
			Success:          item.Success,
			ErrorMessage:     item.ErrorMessage,
			ExtractionsJSON:  mustJSON(item.Extractions),
			SnapshotJSON:     mustJSON(item.Snapshot),
		})
	}
	return rows
}

func toParquetResources(items []archiveResourceSample) []parquetResourceSample {
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
		})
	}
	return rows
}

func toParquetSummaries(items []archiveIntervalSummary) []parquetIntervalSummary {
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

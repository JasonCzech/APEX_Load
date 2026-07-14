package generatorrpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
	local "github.com/xitongsys/parquet-go-source/local"
	"github.com/xitongsys/parquet-go/parquet"
	"github.com/xitongsys/parquet-go/source"
	"github.com/xitongsys/parquet-go/writer"
	"golang.org/x/sys/unix"
)

const archiveSchemaVersion = "apexload.archive.v1"

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
	Timestamp      time.Time               `json:"timestamp"`
	TestID         string                  `json:"test_id"`
	TestName       string                  `json:"test_name"`
	Group          string                  `json:"group,omitempty"`
	ActionName     string                  `json:"action_name,omitempty"`
	DurationMs     float64                 `json:"duration_ms"`
	DNSMs          float64                 `json:"dns_ms,omitempty"`
	ConnectMs      float64                 `json:"connect_ms,omitempty"`
	TLSMs          float64                 `json:"tls_ms,omitempty"`
	TTFBMs         float64                 `json:"ttfb_ms,omitempty"`
	BytesSent      int64                   `json:"bytes_sent,omitempty"`
	BytesReceived  int64                   `json:"bytes_received,omitempty"`
	StatusCode     int                     `json:"status_code,omitempty"`
	Success        bool                    `json:"success"`
	ErrorMessage   string                  `json:"error_message,omitempty"`
	NetworkProfile string                  `json:"network_profile,omitempty"`
	ClientMetrics  *protocol.ClientMetrics `json:"client_metrics,omitempty"`
	Extractions    map[string]string       `json:"extractions,omitempty"`
	Snapshot       *protocol.ErrorSnapshot `json:"snapshot,omitempty"`
}

type archiveResourceSample struct {
	Timestamp                         time.Time `json:"timestamp"`
	TestID                            string    `json:"test_id"`
	TestName                          string    `json:"test_name"`
	ActiveVUsers                      int64     `json:"active_vusers"`
	CPUProcessPct                     float64   `json:"cpu_process_pct"`
	CPUFreePct                        float64   `json:"cpu_free_pct"`
	MemProcessMB                      float64   `json:"mem_process_mb"`
	MemFreeMB                         float64   `json:"mem_free_mb"`
	NetSentKbps                       float64   `json:"net_sent_kbps"`
	NetRecvKbps                       float64   `json:"net_recv_kbps"`
	DiskReadKbps                      float64   `json:"disk_read_kbps"`
	DiskWriteKbps                     float64   `json:"disk_write_kbps"`
	CPUUserPct                        float64   `json:"cpu_user_pct,omitempty"`
	CPUSystemPct                      float64   `json:"cpu_system_pct,omitempty"`
	CPUIOWaitPct                      float64   `json:"cpu_iowait_pct,omitempty"`
	CPUStealPct                       float64   `json:"cpu_steal_pct,omitempty"`
	ThreadCount                       int32     `json:"thread_count,omitempty"`
	OpenFDCount                       int32     `json:"open_fd_count,omitempty"`
	GoroutineCount                    int32     `json:"goroutine_count,omitempty"`
	HeapAllocMB                       float64   `json:"heap_alloc_mb,omitempty"`
	HeapInUseMB                       float64   `json:"heap_in_use_mb,omitempty"`
	GCCountDelta                      uint32    `json:"gc_count_delta,omitempty"`
	GCPauseMs                         float64   `json:"gc_pause_ms,omitempty"`
	ContainerCPULimitCores            float64   `json:"container_cpu_limit_cores,omitempty"`
	ContainerCPUUsagePct              float64   `json:"container_cpu_usage_pct,omitempty"`
	ContainerCPUThrottledPeriodsDelta uint64    `json:"container_cpu_throttled_periods_delta,omitempty"`
	ContainerCPUThrottledSecondsRate  float64   `json:"container_cpu_throttled_seconds_rate,omitempty"`
	ContainerMemoryWorkingSetMB       float64   `json:"container_memory_working_set_mb,omitempty"`
	ContainerMemoryLimitMB            float64   `json:"container_memory_limit_mb,omitempty"`
	ContainerMemoryUsagePct           float64   `json:"container_memory_usage_pct,omitempty"`
	ContainerMemoryFailCountDelta     uint64    `json:"container_memory_fail_count_delta,omitempty"`
	ContainerOOMKillCountDelta        uint64    `json:"container_oom_kill_count_delta,omitempty"`
	NetDropInPerSec                   float64   `json:"net_drop_in_per_sec,omitempty"`
	NetDropOutPerSec                  float64   `json:"net_drop_out_per_sec,omitempty"`
	NetErrorInPerSec                  float64   `json:"net_error_in_per_sec,omitempty"`
	NetErrorOutPerSec                 float64   `json:"net_error_out_per_sec,omitempty"`
	TCPRetransmitsPerSec              float64   `json:"tcp_retransmits_per_sec,omitempty"`
	TCPEstablished                    int       `json:"tcp_established,omitempty"`
	TCPTimeWait                       int       `json:"tcp_time_wait,omitempty"`
	DiskIOTimePct                     float64   `json:"disk_io_time_pct,omitempty"`
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
	namespace       *generatorArchiveNamespace
	ownsNamespace   bool
	runRoot         *os.Root
	runIdentity     os.FileInfo
	runName         string
	rootDir         string
	manifestPath    string
	txFile          *os.File
	resourceFile    *os.File
	summaryFile     *os.File
	txEncoder       *json.Encoder
	resourceEncoder *json.Encoder
	summaryEncoder  *json.Encoder
	byteBudget      *generatorArchiveByteBudget
	closed          bool
}

type generatorArchiveByteBudget struct {
	mu            sync.Mutex
	root          string
	maxBytes      int64
	minFreeBytes  int64
	written       int64
	nextFreeCheck int64
	knownFree     int64
	freeKnown     bool
}

type generatorArchiveQuotaWriter struct {
	file   *os.File
	budget *generatorArchiveByteBudget
}

func (w *generatorArchiveQuotaWriter) Write(content []byte) (int, error) {
	if w == nil || w.file == nil || w.budget == nil {
		return 0, fmt.Errorf("generator archive writer is unavailable")
	}
	return w.budget.writeTo(w.file, content)
}

func (b *generatorArchiveByteBudget) writeTo(destination io.Writer, content []byte) (int, error) {
	if b == nil || destination == nil {
		return 0, fmt.Errorf("generator archive byte budget is unavailable")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	length := int64(len(content))
	if b.written > b.maxBytes || length > b.maxBytes-b.written {
		return 0, fmt.Errorf("generator archive exceeds byte limit %d", b.maxBytes)
	}
	if !b.freeKnown || b.written >= b.nextFreeCheck {
		free, err := generatorArchiveFreeBytes(b.root)
		if err != nil {
			return 0, err
		}
		b.knownFree = free
		b.freeKnown = true
		if b.written > int64(^uint64(0)>>1)-(1<<20) {
			b.nextFreeCheck = int64(^uint64(0) >> 1)
		} else {
			b.nextFreeCheck = b.written + 1<<20
		}
	}
	if b.knownFree < b.minFreeBytes || length > b.knownFree-b.minFreeBytes {
		return 0, fmt.Errorf("generator archive free space would fall below reserve %d", b.minFreeBytes)
	}
	n, err := destination.Write(content)
	if n < 0 || n > len(content) {
		return 0, fmt.Errorf("generator archive writer returned invalid byte count %d", n)
	}
	b.written += int64(n)
	if int64(n) >= b.knownFree {
		b.knownFree = 0
	} else {
		b.knownFree -= int64(n)
	}
	if err == nil && n != len(content) {
		err = io.ErrShortWrite
	}
	return n, err
}

// generatorArchiveQuotaParquetFile keeps parquet-go's writes inside the same
// aggregate quota used by the JSONL producers. Embedding retains Seek/Read and
// Close semantics while Write, Open, and Create preserve quota enforcement.
type generatorArchiveQuotaParquetFile struct {
	source.ParquetFile
	budget *generatorArchiveByteBudget
}

func (f *generatorArchiveQuotaParquetFile) Write(content []byte) (int, error) {
	if f == nil || f.ParquetFile == nil || f.budget == nil {
		return 0, fmt.Errorf("generator parquet archive writer is unavailable")
	}
	return f.budget.writeTo(f.ParquetFile, content)
}

func (f *generatorArchiveQuotaParquetFile) Open(name string) (source.ParquetFile, error) {
	opened, err := f.ParquetFile.Open(name)
	if err != nil {
		return nil, err
	}
	return &generatorArchiveQuotaParquetFile{ParquetFile: opened, budget: f.budget}, nil
}

func (f *generatorArchiveQuotaParquetFile) Create(name string) (source.ParquetFile, error) {
	created, err := f.ParquetFile.Create(name)
	if err != nil {
		return nil, err
	}
	return &generatorArchiveQuotaParquetFile{ParquetFile: created, budget: f.budget}, nil
}

func generatorArchiveFreeBytes(path string) (int64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, err
	}
	if stat.Bavail > uint64(^uint64(0)/uint64(stat.Bsize)) {
		return int64(^uint64(0) >> 1), nil
	}
	available := stat.Bavail * uint64(stat.Bsize)
	if available > uint64(^uint64(0)>>1) {
		return int64(^uint64(0) >> 1), nil
	}
	return int64(available), nil
}

type parquetTransactionEvent struct {
	TimestampUnixMs   int64   `parquet:"name=timestamp_unix_ms, type=INT64"`
	TimestampRFC3339  string  `parquet:"name=timestamp_rfc3339, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	TestID            string  `parquet:"name=test_id, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	TestName          string  `parquet:"name=test_name, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	Group             string  `parquet:"name=group_name, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	ActionName        string  `parquet:"name=action_name, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	DurationMs        float64 `parquet:"name=duration_ms, type=DOUBLE"`
	DNSMs             float64 `parquet:"name=dns_ms, type=DOUBLE"`
	ConnectMs         float64 `parquet:"name=connect_ms, type=DOUBLE"`
	TLSMs             float64 `parquet:"name=tls_ms, type=DOUBLE"`
	TTFBMs            float64 `parquet:"name=ttfb_ms, type=DOUBLE"`
	BytesSent         int64   `parquet:"name=bytes_sent, type=INT64"`
	BytesReceived     int64   `parquet:"name=bytes_received, type=INT64"`
	StatusCode        int64   `parquet:"name=status_code, type=INT64"`
	Success           bool    `parquet:"name=success, type=BOOLEAN"`
	ErrorMessage      string  `parquet:"name=error_message, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	NetworkProfile    string  `parquet:"name=network_profile, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	ClientMetricsJSON string  `parquet:"name=client_metrics_json, type=BYTE_ARRAY, convertedtype=UTF8"`
	ExtractionsJSON   string  `parquet:"name=extractions_json, type=BYTE_ARRAY, convertedtype=UTF8"`
	SnapshotJSON      string  `parquet:"name=snapshot_json, type=BYTE_ARRAY, convertedtype=UTF8"`
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
	DiskIOTimePct                     float64 `parquet:"name=disk_io_time_pct, type=DOUBLE"`
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

func newArchiveRecorder(generatorID string, testID string, testName string) (*archiveRecorder, error) {
	if err := validateArchiveTestID(testID); err != nil {
		return nil, err
	}
	if generatorID != "" {
		if err := validateArchiveNamespace(sanitizeArchiveNamespace(generatorID)); err != nil {
			return nil, err
		}
	}
	namespace, err := prepareGeneratorArchiveNamespace(generatorID, true)
	if err != nil {
		return nil, err
	}
	recorder, err := newArchiveRecorderInNamespace(namespace, testID, testName)
	if err != nil {
		_ = namespace.close()
		return nil, err
	}
	recorder.ownsNamespace = true
	return recorder, nil
}

func newArchiveRecorderInNamespace(namespace *generatorArchiveNamespace, testID string, testName string) (*archiveRecorder, error) {
	if err := validateArchiveTestID(testID); err != nil {
		return nil, err
	}
	if namespace == nil || namespace.root == nil {
		return nil, fmt.Errorf("generator archive namespace is unavailable")
	}
	_, existingErr := namespace.root.Lstat(testID)
	createdRun := os.IsNotExist(existingErr)
	runRoot, runIdentity, err := openGeneratorArchiveRun(namespace, testID, true)
	if err != nil {
		return nil, fmt.Errorf("create archive dir: %w", err)
	}
	archiveDir := filepath.Join(namespace.path, testID)
	budget := &generatorArchiveByteBudget{
		root: archiveDir, maxBytes: configuredGeneratorArchiveMaxBytes(),
		minFreeBytes: configuredGeneratorArchiveMinFreeBytes(),
	}
	artifactNames := []string{
		"transactions.jsonl", "resources.jsonl", "interval_summaries.jsonl",
		"transactions.parquet", "resources.parquet", "interval_summaries.parquet",
		"manifest.json",
	}
	for _, name := range artifactNames {
		if _, err := inspectGeneratorArchiveFileTarget(runRoot, name, true); err != nil {
			_ = runRoot.Close()
			if createdRun {
				_ = removeGeneratorArchiveRun(namespace, testID, runIdentity)
			}
			return nil, fmt.Errorf("inspect archive target: %w", err)
		}
	}

	open := func(name string) (*os.File, *json.Encoder, error) {
		file, err := replaceGeneratorArchiveFile(runRoot, name, 0o644)
		if err != nil {
			return nil, nil, err
		}
		return file, json.NewEncoder(&generatorArchiveQuotaWriter{file: file, budget: budget}), nil
	}

	txFile, txEncoder, err := open("transactions.jsonl")
	if err != nil {
		_ = runRoot.Close()
		if createdRun {
			_ = removeGeneratorArchiveRun(namespace, testID, runIdentity)
		}
		return nil, fmt.Errorf("open transactions archive: %w", err)
	}
	resourceFile, resourceEncoder, err := open("resources.jsonl")
	if err != nil {
		_ = txFile.Close()
		_ = runRoot.Close()
		if createdRun {
			_ = removeGeneratorArchiveRun(namespace, testID, runIdentity)
		}
		return nil, fmt.Errorf("open resources archive: %w", err)
	}
	summaryFile, summaryEncoder, err := open("interval_summaries.jsonl")
	if err != nil {
		_ = txFile.Close()
		_ = resourceFile.Close()
		_ = runRoot.Close()
		if createdRun {
			_ = removeGeneratorArchiveRun(namespace, testID, runIdentity)
		}
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
		namespace:       namespace,
		runRoot:         runRoot,
		runIdentity:     runIdentity,
		runName:         testID,
		rootDir:         archiveDir,
		manifestPath:    filepath.Join(archiveDir, "manifest.json"),
		txFile:          txFile,
		resourceFile:    resourceFile,
		summaryFile:     summaryFile,
		txEncoder:       txEncoder,
		resourceEncoder: resourceEncoder,
		summaryEncoder:  summaryEncoder,
		byteBudget:      budget,
	}, nil
}

// Discard closes any files left open by a failed finalization and removes the
// incomplete archive directory. A failed archive must not become an untracked
// on-disk leak or expose a partial manifest to the controller.
type archiveRecorderCleanup struct {
	files         []*os.File
	namespace     *generatorArchiveNamespace
	runRoot       *os.Root
	runIdentity   os.FileInfo
	runName       string
	rootDir       string
	ownsNamespace bool
}

func (w *archiveRecorder) detachCleanupLocked() archiveRecorderCleanup {
	cleanup := archiveRecorderCleanup{
		files:         []*os.File{w.txFile, w.resourceFile, w.summaryFile},
		namespace:     w.namespace,
		runRoot:       w.runRoot,
		runIdentity:   w.runIdentity,
		runName:       w.runName,
		rootDir:       w.rootDir,
		ownsNamespace: w.ownsNamespace,
	}
	w.txFile = nil
	w.resourceFile = nil
	w.summaryFile = nil
	w.runRoot = nil
	w.closed = true
	return cleanup
}

func (c archiveRecorderCleanup) run() error {
	failures := make([]error, 0, 5)
	for _, file := range c.files {
		if file != nil {
			if err := file.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
				failures = append(failures, err)
			}
		}
	}
	if c.runRoot != nil {
		if err := c.runRoot.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			failures = append(failures, err)
		}
	}
	if strings.TrimSpace(c.rootDir) != "" {
		if err := removeGeneratorArchiveRun(c.namespace, c.runName, c.runIdentity); err != nil {
			failures = append(failures, err)
		}
	}
	if c.ownsNamespace && c.namespace != nil {
		if err := c.namespace.close(); err != nil && !errors.Is(err, os.ErrClosed) {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (w *archiveRecorder) Discard() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	cleanup := w.detachCleanupLocked()
	w.mu.Unlock()
	return cleanup.run()
}

func validateArchiveTestID(testID string) error {
	if testID == "" {
		return fmt.Errorf("test id is empty")
	}
	if testID != strings.TrimSpace(testID) || strings.ContainsRune(testID, 0) || strings.ContainsAny(testID, `/\\`) {
		return fmt.Errorf("test id contains a path separator")
	}
	if testID == "." || testID == ".." || filepath.IsAbs(testID) || filepath.VolumeName(testID) != "" {
		return fmt.Errorf("test id is not a safe archive namespace")
	}
	for _, r := range testID {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("test id contains a control character")
		}
	}
	return nil
}

func (w *archiveRecorder) RecordResult(result *protocol.Result) error {
	if result == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.txEncoder == nil {
		return fmt.Errorf("generator archive recorder is closed")
	}
	event := archiveTransactionEvent{
		Timestamp:      result.StartTime,
		TestID:         w.manifest.TestID,
		TestName:       w.manifest.TestName,
		Group:          result.Group,
		ActionName:     result.ActionName,
		DurationMs:     durationMillis(result.Duration),
		DNSMs:          durationMillis(result.DNSTime),
		ConnectMs:      durationMillis(result.ConnectTime),
		TLSMs:          durationMillis(result.TLSTime),
		TTFBMs:         durationMillis(result.TTFB),
		BytesSent:      result.BytesSent,
		BytesReceived:  result.BytesReceived,
		StatusCode:     result.StatusCode,
		Success:        result.Success,
		ErrorMessage:   result.ErrorMessage,
		NetworkProfile: result.NetworkProfile,
		ClientMetrics:  result.ClientMetrics,
		Extractions:    result.Extractions,
		Snapshot:       result.Snapshot,
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
	if w.closed || w.resourceEncoder == nil {
		return fmt.Errorf("generator archive recorder is closed")
	}
	record := archiveResourceSample{
		Timestamp:                         timestamp,
		TestID:                            w.manifest.TestID,
		TestName:                          w.manifest.TestName,
		ActiveVUsers:                      activeVUsers,
		CPUProcessPct:                     resources.CPUProcessPct,
		CPUFreePct:                        resources.CPUFreePct,
		MemProcessMB:                      resources.MemProcessMb,
		MemFreeMB:                         resources.MemFreeMb,
		NetSentKbps:                       resources.NetSentKbps,
		NetRecvKbps:                       resources.NetRecvKbps,
		DiskReadKbps:                      resources.DiskReadKbps,
		DiskWriteKbps:                     resources.DiskWriteKbps,
		CPUUserPct:                        resources.CPUUserPct,
		CPUSystemPct:                      resources.CPUSystemPct,
		CPUIOWaitPct:                      resources.CPUIOWaitPct,
		CPUStealPct:                       resources.CPUStealPct,
		ThreadCount:                       resources.ThreadCount,
		OpenFDCount:                       resources.OpenFDCount,
		GoroutineCount:                    resources.GoroutineCount,
		HeapAllocMB:                       resources.HeapAllocMB,
		HeapInUseMB:                       resources.HeapInUseMB,
		GCCountDelta:                      resources.GCCountDelta,
		GCPauseMs:                         resources.GCPauseMs,
		ContainerCPULimitCores:            resources.ContainerCPULimitCores,
		ContainerCPUUsagePct:              resources.ContainerCPUUsagePct,
		ContainerCPUThrottledPeriodsDelta: resources.ContainerCPUThrottledPeriodsDelta,
		ContainerCPUThrottledSecondsRate:  resources.ContainerCPUThrottledSecondsRate,
		ContainerMemoryWorkingSetMB:       resources.ContainerMemoryWorkingSetMB,
		ContainerMemoryLimitMB:            resources.ContainerMemoryLimitMB,
		ContainerMemoryUsagePct:           resources.ContainerMemoryUsagePct,
		ContainerMemoryFailCountDelta:     resources.ContainerMemoryFailCountDelta,
		ContainerOOMKillCountDelta:        resources.ContainerOOMKillCountDelta,
		NetDropInPerSec:                   resources.NetDropInPerSec,
		NetDropOutPerSec:                  resources.NetDropOutPerSec,
		NetErrorInPerSec:                  resources.NetErrorInPerSec,
		NetErrorOutPerSec:                 resources.NetErrorOutPerSec,
		TCPRetransmitsPerSec:              resources.TCPRetransmitsPerSec,
		TCPEstablished:                    resources.TCPEstablished,
		TCPTimeWait:                       resources.TCPTimeWait,
		DiskIOTimePct:                     resources.DiskIOTimePct,
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
	if w.closed || w.summaryEncoder == nil {
		return fmt.Errorf("generator archive recorder is closed")
	}
	summary.TestID = w.manifest.TestID
	summary.TestName = w.manifest.TestName
	if err := w.summaryEncoder.Encode(summary); err != nil {
		return err
	}
	w.manifest.IntervalSummaryCount++
	return nil
}

func (w *archiveRecorder) Finalize(status string, completedAt time.Time, errStr string) (record *storage.RunArchiveRecord, err error) {
	if w == nil {
		return nil, fmt.Errorf("generator archive recorder is unavailable")
	}
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil, fmt.Errorf("generator archive recorder is closed")
	}
	cleanupOnError := true
	defer func() {
		if !cleanupOnError {
			w.mu.Unlock()
			return
		}
		cleanup := w.detachCleanupLocked()
		w.mu.Unlock()
		if cleanupErr := cleanup.run(); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("discard failed generator archive: %w", cleanupErr))
		}
	}()
	w.manifest.Status = status
	w.manifest.CompletedAt = completedAt
	w.manifest.Error = errStr

	for _, file := range []*os.File{w.txFile, w.resourceFile, w.summaryFile} {
		if file == nil {
			continue
		}
		if syncErr := file.Sync(); syncErr != nil {
			return nil, syncErr
		}
		if closeErr := file.Close(); closeErr != nil {
			return nil, closeErr
		}
	}
	w.txFile = nil
	w.resourceFile = nil
	w.summaryFile = nil
	if w.runRoot == nil {
		return nil, fmt.Errorf("archive run root is unavailable")
	}
	if err := writeArchiveParquetFilesAtWithBudget(w.runRoot, &w.manifest, w.byteBudget); err != nil {
		return nil, err
	}
	if size, err := generatorArchiveRunSize(w.runRoot); err != nil {
		return nil, err
	} else if w.byteBudget != nil && size > w.byteBudget.maxBytes {
		return nil, fmt.Errorf("generator archive size %d exceeds byte limit %d", size, w.byteBudget.maxBytes)
	}
	data, err := json.MarshalIndent(w.manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := writeGeneratorArchiveFileDurableWithWriter(w.runRoot, "manifest.json", data, 0o644, func(file *os.File, content []byte) (int, error) {
		if w.byteBudget == nil {
			return file.Write(content)
		}
		return w.byteBudget.writeTo(file, content)
	}); err != nil {
		return nil, err
	}
	if size, err := generatorArchiveRunSize(w.runRoot); err != nil {
		return nil, err
	} else if w.byteBudget != nil && size > w.byteBudget.maxBytes {
		return nil, fmt.Errorf("generator archive size %d exceeds byte limit %d", size, w.byteBudget.maxBytes)
	}
	if err := verifyGeneratorArchiveRun(w.namespace, w.runName, w.runIdentity); err != nil {
		return nil, fmt.Errorf("verify finalized archive directory: %w", err)
	}
	record = &storage.RunArchiveRecord{
		TestID:               w.manifest.TestID,
		SchemaVersion:        w.manifest.SchemaVersion,
		ArchivePath:          w.rootDir,
		ManifestPath:         w.manifestPath,
		TransactionCount:     w.manifest.TransactionCount,
		ResourceSampleCount:  w.manifest.ResourceSampleCount,
		IntervalSummaryCount: w.manifest.IntervalSummaryCount,
		CreatedAt:            w.manifest.CreatedAt,
		CompletedAt:          w.manifest.CompletedAt,
	}
	if err := w.runRoot.Close(); err != nil {
		return nil, err
	}
	w.runRoot = nil
	if w.ownsNamespace {
		if err := w.namespace.close(); err != nil {
			// The archive has already been fully synced and verified. A directory
			// descriptor close error cannot be repaired by deleting that durable
			// result, and the descriptor is no longer usable after Close.
			w.closed = true
			cleanupOnError = false
			return record, nil
		}
	}
	w.closed = true
	cleanupOnError = false
	return record, nil
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
	return config.ArchiveDir()
}

func sanitizeArchiveNamespace(value string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "\x00", "_")
	cleaned := strings.TrimSpace(replacer.Replace(value))
	cleaned = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '_'
		}
		return r
	}, cleaned)
	if cleaned == "" || cleaned == "." || cleaned == ".." {
		return "default"
	}
	return cleaned
}

func durationMillis(duration time.Duration) float64 {
	if duration == 0 {
		return 0
	}
	return float64(duration) / float64(time.Millisecond)
}

func writeArchiveParquetFiles(rootDir string, manifest *archiveManifest) error {
	_, root, err := openCanonicalArchiveDirectory(rootDir, false)
	if err != nil {
		return err
	}
	defer root.Close()
	return writeArchiveParquetFilesAt(root, manifest)
}

func writeArchiveParquetFilesAt(root *os.Root, manifest *archiveManifest) error {
	return writeArchiveParquetFilesAtWithBudget(root, manifest, nil)
}

func writeArchiveParquetFilesAtWithBudget(root *os.Root, manifest *archiveManifest, budget *generatorArchiveByteBudget) error {
	if root == nil || manifest == nil {
		return fmt.Errorf("archive root and manifest are required")
	}
	if err := streamJSONLToParquetAtWithBudget(
		root,
		manifest.TransactionsFile,
		manifest.TransactionsParquetFile,
		budget,
		func(item archiveTransactionEvent) parquetTransactionEvent {
			return toParquetTransactions([]archiveTransactionEvent{item})[0]
		},
	); err != nil {
		return fmt.Errorf("write transaction parquet: %w", err)
	}
	if err := streamJSONLToParquetAtWithBudget(
		root,
		manifest.ResourceSamplesFile,
		manifest.ResourceSamplesParquetFile,
		budget,
		func(item archiveResourceSample) parquetResourceSample {
			return toParquetResources([]archiveResourceSample{item})[0]
		},
	); err != nil {
		return fmt.Errorf("write resource parquet: %w", err)
	}
	if err := streamJSONLToParquetAtWithBudget(
		root,
		manifest.IntervalSummariesFile,
		manifest.IntervalSummariesParquetFile,
		budget,
		func(item archiveIntervalSummary) parquetIntervalSummary {
			return toParquetSummaries([]archiveIntervalSummary{item})[0]
		},
	); err != nil {
		return fmt.Errorf("write summary parquet: %w", err)
	}
	return nil
}

func streamJSONLToParquet[In any, Out any](inputPath string, outputPath string, convert func(In) Out) error {
	inputParent, err := filepath.Abs(filepath.Dir(inputPath))
	if err != nil {
		return err
	}
	outputParent, err := filepath.Abs(filepath.Dir(outputPath))
	if err != nil {
		return err
	}
	if inputParent != outputParent {
		return fmt.Errorf("archive parquet input and output must share a directory")
	}
	_, root, err := openCanonicalArchiveDirectory(inputParent, false)
	if err != nil {
		return err
	}
	defer root.Close()
	return streamJSONLToParquetAt(root, filepath.Base(inputPath), filepath.Base(outputPath), convert)
}

func streamJSONLToParquetAt[In any, Out any](root *os.Root, inputName string, outputName string, convert func(In) Out) error {
	return streamJSONLToParquetAtWithBudget(root, inputName, outputName, nil, convert)
}

func streamJSONLToParquetAtWithBudget[In any, Out any](root *os.Root, inputName string, outputName string, budget *generatorArchiveByteBudget, convert func(In) Out) error {
	input, _, err := openGeneratorArchiveRegularFile(root, inputName)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := replaceGeneratorArchiveFile(root, outputName, 0o644)
	if err != nil {
		return err
	}
	var fw source.ParquetFile = &local.LocalFile{FilePath: outputName, File: output}
	if budget != nil {
		fw = &generatorArchiveQuotaParquetFile{ParquetFile: fw, budget: budget}
	}
	pw, err := writer.NewParquetWriter(fw, new(Out), 4)
	if err != nil {
		_ = fw.Close()
		return err
	}
	pw.CompressionType = parquet.CompressionCodec_SNAPPY
	decoder := json.NewDecoder(input)
	for {
		var item In
		if err := decoder.Decode(&item); err != nil {
			if err == io.EOF {
				break
			}
			_ = pw.WriteStop()
			_ = fw.Close()
			return err
		}
		if err := pw.Write(convert(item)); err != nil {
			_ = pw.WriteStop()
			_ = fw.Close()
			return err
		}
	}
	if err := pw.WriteStop(); err != nil {
		_ = fw.Close()
		return err
	}
	if err := output.Sync(); err != nil {
		_ = fw.Close()
		return err
	}
	return fw.Close()
}

func generatorArchiveRunSize(root *os.Root) (int64, error) {
	directory, err := root.Open(".")
	if err != nil {
		return 0, err
	}
	entries, readErr := directory.ReadDir(-1)
	closeErr := directory.Close()
	if readErr != nil {
		return 0, readErr
	}
	if closeErr != nil {
		return 0, closeErr
	}
	var total int64
	for _, entry := range entries {
		info, err := root.Lstat(entry.Name())
		if err != nil {
			return 0, err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return 0, fmt.Errorf("generator archive contains non-regular artifact %q", entry.Name())
		}
		if info.Size() > 0 && total > int64(^uint64(0)>>1)-info.Size() {
			return 0, fmt.Errorf("generator archive size overflow")
		}
		total += info.Size()
	}
	return total, nil
}

func toParquetTransactions(items []archiveTransactionEvent) []parquetTransactionEvent {
	rows := make([]parquetTransactionEvent, 0, len(items))
	for _, item := range items {
		rows = append(rows, parquetTransactionEvent{
			TimestampUnixMs:   item.Timestamp.UnixMilli(),
			TimestampRFC3339:  item.Timestamp.Format(time.RFC3339Nano),
			TestID:            item.TestID,
			TestName:          item.TestName,
			Group:             item.Group,
			ActionName:        item.ActionName,
			DurationMs:        item.DurationMs,
			DNSMs:             item.DNSMs,
			ConnectMs:         item.ConnectMs,
			TLSMs:             item.TLSMs,
			TTFBMs:            item.TTFBMs,
			BytesSent:         item.BytesSent,
			BytesReceived:     item.BytesReceived,
			StatusCode:        int64(item.StatusCode),
			Success:           item.Success,
			ErrorMessage:      item.ErrorMessage,
			NetworkProfile:    item.NetworkProfile,
			ClientMetricsJSON: mustJSON(item.ClientMetrics),
			ExtractionsJSON:   mustJSON(item.Extractions),
			SnapshotJSON:      mustJSON(item.Snapshot),
		})
	}
	return rows
}

func toParquetResources(items []archiveResourceSample) []parquetResourceSample {
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
			DiskIOTimePct:                     item.DiskIOTimePct,
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

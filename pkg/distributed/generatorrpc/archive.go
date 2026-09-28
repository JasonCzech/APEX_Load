package generatorrpc

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
	"github.com/jasonczech/apexload/pkg/internal/diskspace"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/scrub"
	"github.com/jasonczech/apexload/pkg/storage"
	local "github.com/xitongsys/parquet-go-source/local"
	"github.com/xitongsys/parquet-go/parquet"
	"github.com/xitongsys/parquet-go/source"
	"github.com/xitongsys/parquet-go/writer"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const archiveSchemaVersion = "apexload.archive.v4"

type archiveManifest struct {
	SourceIdentity               string                       `json:"source_identity,omitempty"`
	SchemaVersion                string                       `json:"schema_version"`
	TestID                       string                       `json:"test_id"`
	TestName                     string                       `json:"test_name"`
	CreatedAt                    time.Time                    `json:"created_at"`
	CompletedAt                  time.Time                    `json:"completed_at,omitempty"`
	Status                       string                       `json:"status,omitempty"`
	Error                        string                       `json:"error,omitempty"`
	ExecutionGeneration          uint64                       `json:"execution_generation,omitempty"`
	ExecutionGroups              []string                     `json:"execution_groups,omitempty"`
	TransactionsFile             string                       `json:"transactions_file"`
	TransactionsParquetFile      string                       `json:"transactions_parquet_file,omitempty"`
	ResourceSamplesFile          string                       `json:"resource_samples_file"`
	ResourceSamplesParquetFile   string                       `json:"resource_samples_parquet_file,omitempty"`
	IntervalSummariesFile        string                       `json:"interval_summaries_file"`
	IntervalSummariesParquetFile string                       `json:"interval_summaries_parquet_file,omitempty"`
	TransactionCount             int64                        `json:"transaction_count"`
	ResourceSampleCount          int64                        `json:"resource_sample_count"`
	IntervalSummaryCount         int64                        `json:"interval_summary_count"`
	TerminalMetrics              json.RawMessage              `json:"terminal_metrics,omitempty"`
	MessageDetailCapture         *archiveMessageDetailCapture `json:"message_detail_capture,omitempty"`
}

type archiveMessageDetailCapture struct {
	RetainedItems  int64            `json:"retained_items"`
	RetainedBytes  int64            `json:"retained_bytes"`
	TruncatedItems int64            `json:"truncated_items"`
	Dropped        map[string]int64 `json:"dropped,omitempty"`
}

type archiveTransactionEvent struct {
	WorkerDurationMs      float64                   `json:"worker_duration_ms,omitempty"`
	CSVExportIDs          []string                  `json:"csv_export_ids"`
	ExtractionsIncomplete bool                      `json:"extractions_incomplete,omitempty"`
	Timestamp             time.Time                 `json:"timestamp"`
	TestID                string                    `json:"test_id"`
	TestName              string                    `json:"test_name"`
	Group                 string                    `json:"group,omitempty"`
	Region                string                    `json:"region,omitempty"`
	Hierarchy             string                    `json:"hierarchy,omitempty"`
	PageName              string                    `json:"page_name,omitempty"`
	Phase                 string                    `json:"phase,omitempty"`
	HasDetail             bool                      `json:"has_detail"`
	MessageDetailPriority int                       `json:"message_detail_priority,omitempty"`
	ActionName            string                    `json:"action_name,omitempty"`
	Population            string                    `json:"population,omitempty"`
	BusinessTransaction   bool                      `json:"business_transaction,omitempty"`
	DurationMs            float64                   `json:"duration_ms"`
	DNSMs                 float64                   `json:"dns_ms,omitempty"`
	ConnectMs             float64                   `json:"connect_ms,omitempty"`
	TLSMs                 float64                   `json:"tls_ms,omitempty"`
	TTFBMs                float64                   `json:"ttfb_ms,omitempty"`
	BytesSent             int64                     `json:"bytes_sent,omitempty"`
	BytesReceived         int64                     `json:"bytes_received,omitempty"`
	StatusCode            int                       `json:"status_code,omitempty"`
	Success               bool                      `json:"success"`
	ErrorMessage          string                    `json:"error_message,omitempty"`
	NetworkProfile        string                    `json:"network_profile,omitempty"`
	CorrelationID         string                    `json:"correlation_id,omitempty"`
	DependencyType        string                    `json:"dependency_type,omitempty"`
	DependencyTarget      string                    `json:"dependency_target,omitempty"`
	SourceStepID          string                    `json:"source_step_id,omitempty"`
	SourceStepIndex       int                       `json:"source_step_index,omitempty"`
	SourceAction          string                    `json:"source_action,omitempty"`
	SourceActionName      string                    `json:"source_action_name,omitempty"`
	SourceTransaction     string                    `json:"source_transaction,omitempty"`
	SourceProfile         string                    `json:"source_profile,omitempty"`
	PageURL               string                    `json:"page_url,omitempty"`
	PageTitle             string                    `json:"page_title,omitempty"`
	ConsoleEntries        []protocol.ConsoleEntry   `json:"console_entries,omitempty"`
	NetworkEntries        []protocol.NetworkEntry   `json:"network_entries,omitempty"`
	DOMSummary            *protocol.DOMSummary      `json:"dom_summary,omitempty"`
	ClientMetrics         *protocol.ClientMetrics   `json:"client_metrics,omitempty"`
	ServerTelemetry       *protocol.ServerTelemetry `json:"server_telemetry,omitempty"`
	ProtocolEvidence      map[string]any            `json:"protocol_evidence,omitempty"`
	Extractions           map[string]string         `json:"extractions,omitempty"`
	Snapshot              *protocol.ErrorSnapshot   `json:"snapshot,omitempty"`
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
	CompletedIterations int64                                         `json:"completed_iterations,omitempty"`
	Timestamp           time.Time                                     `json:"timestamp"`
	TestID              string                                        `json:"test_id"`
	TestName            string                                        `json:"test_name"`
	ActiveVUsers        int64                                         `json:"active_vusers"`
	TPS                 float64                                       `json:"tps"`
	AvgResponseMs       float64                                       `json:"avg_response_ms"`
	ErrorPct            float64                                       `json:"error_pct"`
	TotalTx             int64                                         `json:"total_transactions"`
	TotalErrors         int64                                         `json:"total_errors"`
	P50Ms               float64                                       `json:"p50_ms"`
	P90Ms               float64                                       `json:"p90_ms"`
	P95Ms               float64                                       `json:"p95_ms"`
	P99Ms               float64                                       `json:"p99_ms"`
	DurationDigest      *engine.TDigestSnapshot                       `json:"duration_digest,omitempty"`
	Groups              map[string]*archiveGroupLiveMetrics           `json:"groups,omitempty"`
	Transactions        map[string]*archiveIntervalTransactionMetrics `json:"transactions,omitempty"`
	ByRegion            map[string]*archiveIntervalTransactionMetrics `json:"by_region,omitempty"`
	Browser             *archiveBrowserLiveMetrics                    `json:"browser,omitempty"`
	BrowserByDevice     map[string]*archiveBrowserDeviceMetric        `json:"browser_by_device,omitempty"`
	Telemetry           *archiveTelemetryHealth                       `json:"telemetry,omitempty"`
}

// archiveIntervalTransactionMetrics is the generator-side wire-compatible
// projection of api.IntervalTransactionMetrics. Count includes all attempts,
// Errors is a subset of Count, and TPS is the successful transaction rate.
type archiveIntervalTransactionMetrics struct {
	Name   string  `json:"name"`
	TPS    float64 `json:"tps"`
	Count  int64   `json:"count"`
	Errors int64   `json:"errors"`
	AvgMs  float64 `json:"avg_ms"`
}

const (
	maxArchiveIntervalSummaryTransactions     = 512
	archiveIntervalSummaryTransactionOverflow = "__other__"
)

func archiveIntervalTransactionName(name string) string {
	if name = strings.TrimSpace(scrub.ValueLabel(name, scrub.Policy{})); name != "" {
		return name
	}
	return "unnamed"
}

func archiveSummaryTransactionsFromBatch(values []*pb.MetricAggregate) map[string]*archiveIntervalTransactionMetrics {
	if len(values) == 0 {
		return nil
	}
	metrics := make(map[string]*archiveIntervalTransactionMetrics, len(values))
	for _, value := range values {
		if value == nil {
			continue
		}
		name := archiveIntervalTransactionName(value.GetName())
		mergeArchiveIntervalTransactionMetric(metrics, name, &archiveIntervalTransactionMetrics{
			Name:   name,
			TPS:    value.GetTps(),
			Count:  value.GetCount(),
			Errors: value.GetErrors(),
			AvgMs:  value.GetAvgMs(),
		})
	}
	return compactArchiveIntervalTransactions(metrics)
}

func mergeArchiveIntervalTransactionMetric(destination map[string]*archiveIntervalTransactionMetrics, name string, incoming *archiveIntervalTransactionMetrics) {
	if destination == nil || incoming == nil {
		return
	}
	name = archiveIntervalTransactionName(name)
	current := destination[name]
	if current == nil {
		copy := *incoming
		copy.Name = name
		destination[name] = &copy
		return
	}
	combinedCount := current.Count + incoming.Count
	if combinedCount > 0 {
		current.AvgMs = (current.AvgMs*float64(current.Count) + incoming.AvgMs*float64(incoming.Count)) / float64(combinedCount)
	}
	current.Name = name
	current.TPS += incoming.TPS
	current.Count = combinedCount
	current.Errors += incoming.Errors
}

func compactArchiveIntervalTransactions(metrics map[string]*archiveIntervalTransactionMetrics) map[string]*archiveIntervalTransactionMetrics {
	if len(metrics) == 0 {
		return nil
	}
	keys := make([]string, 0, len(metrics))
	for name, metric := range metrics {
		if metric != nil && name != archiveIntervalSummaryTransactionOverflow {
			keys = append(keys, name)
		}
	}
	sort.Strings(keys)
	overflow := metrics[archiveIntervalSummaryTransactionOverflow]
	entryCount := len(keys)
	if overflow != nil {
		entryCount++
	}
	if entryCount <= maxArchiveIntervalSummaryTransactions {
		if overflow != nil {
			overflow.Name = archiveIntervalSummaryTransactionOverflow
		}
		return metrics
	}

	compacted := make(map[string]*archiveIntervalTransactionMetrics, maxArchiveIntervalSummaryTransactions)
	keep := maxArchiveIntervalSummaryTransactions - 1
	for index, name := range keys {
		metric := metrics[name]
		if index < keep {
			copy := *metric
			copy.Name = name
			compacted[name] = &copy
			continue
		}
		mergeArchiveIntervalTransactionMetric(compacted, archiveIntervalSummaryTransactionOverflow, metric)
	}
	if overflow != nil {
		mergeArchiveIntervalTransactionMetric(compacted, archiveIntervalSummaryTransactionOverflow, overflow)
	}
	return compacted
}

type archiveGroupLiveMetrics struct {
	Name              string  `json:"name"`
	VUsers            int     `json:"vusers"`
	Transactions      int64   `json:"transactions"`
	Errors            int64   `json:"errors"`
	TPS               float64 `json:"tps"`
	AvgResponseMs     float64 `json:"avg_response_ms"`
	P50Ms             float64 `json:"p50_ms"`
	P90Ms             float64 `json:"p90_ms"`
	P95Ms             float64 `json:"p95_ms,omitempty"`
	P99Ms             float64 `json:"p99_ms"`
	PercentileQuality string  `json:"percentile_quality,omitempty"`
}

type archiveBrowserLiveMetrics struct {
	Samples              int64   `json:"samples"`
	AvgFCPMs             float64 `json:"avg_fcp_ms,omitempty"`
	AvgLCPMs             float64 `json:"avg_lcp_ms,omitempty"`
	AvgCLSScore          float64 `json:"avg_cls_score,omitempty"`
	AvgINPMs             float64 `json:"avg_inp_ms,omitempty"`
	AvgTTFBMs            float64 `json:"avg_ttfb_ms,omitempty"`
	AvgBlockingResources float64 `json:"avg_blocking_resources,omitempty"`
	AvgLongTaskCount     float64 `json:"avg_long_task_count,omitempty"`
	AvgJSHeapUsedMB      float64 `json:"avg_js_heap_used_mb,omitempty"`
	AvgBrowserProcessCPU float64 `json:"avg_browser_process_cpu_pct,omitempty"`
	AvgBrowserProcessMem float64 `json:"avg_browser_process_mem_mb,omitempty"`
}

type archiveBrowserDeviceMetric struct {
	DeviceProfile        string  `json:"device_profile,omitempty"`
	DevicePlatform       string  `json:"device_platform,omitempty"`
	DeviceTier           string  `json:"device_tier,omitempty"`
	BrowserEngine        string  `json:"browser_engine,omitempty"`
	Samples              int64   `json:"samples"`
	AvgFCPMs             float64 `json:"avg_fcp_ms,omitempty"`
	AvgLCPMs             float64 `json:"avg_lcp_ms,omitempty"`
	AvgCLSScore          float64 `json:"avg_cls_score,omitempty"`
	AvgINPMs             float64 `json:"avg_inp_ms,omitempty"`
	AvgTTFBMs            float64 `json:"avg_ttfb_ms,omitempty"`
	AvgBlockingResources float64 `json:"avg_blocking_resources,omitempty"`
	AvgLongTaskCount     float64 `json:"avg_long_task_count,omitempty"`
	AvgJSHeapUsedMB      float64 `json:"avg_js_heap_used_mb,omitempty"`
	AvgBrowserProcessCPU float64 `json:"avg_browser_process_cpu_pct,omitempty"`
	AvgBrowserProcessMem float64 `json:"avg_browser_process_mem_mb,omitempty"`
}

type archiveTelemetryHealth struct {
	DroppedResults           int64     `json:"dropped_results,omitempty"`
	SheddedArrivals          int64     `json:"shedded_arrivals,omitempty"`
	DroppedSubscriberBatches int64     `json:"dropped_subscriber_batches,omitempty"`
	GeneratorPublishedAt     time.Time `json:"generator_published_at,omitempty"`
	CollectedAt              time.Time `json:"collected_at,omitempty"`
	GeneratorProcessingLagMs float64   `json:"generator_processing_lag_ms,omitempty"`
	TransportLagMs           float64   `json:"transport_lag_ms,omitempty"`
	SampleAgeMs              float64   `json:"sample_age_ms,omitempty"`
}

type archiveRecorder struct {
	mu                  sync.Mutex
	manifest            archiveManifest
	namespace           *generatorArchiveNamespace
	ownsNamespace       bool
	runRoot             *os.Root
	runIdentity         os.FileInfo
	runName             string
	rootDir             string
	manifestPath        string
	txFile              *os.File
	resourceFile        *os.File
	summaryFile         *os.File
	txEncoder           *json.Encoder
	resourceEncoder     *json.Encoder
	summaryEncoder      *json.Encoder
	byteBudget          *generatorArchiveByteBudget
	recordErr           error
	failureSignaled     bool
	closed              bool
	detailItems         int64
	detailBytes         int64
	detailPriorityItems [4]int64
	detailPriorityBytes [4]int64
}

func (w *archiveRecorder) bindExecution(generation uint64, group, sourceIdentity string) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.manifest.ExecutionGeneration = generation
	if w.manifest.SourceIdentity == "" && validArchiveSourceIdentity(sourceIdentity) {
		w.manifest.SourceIdentity = sourceIdentity
	}
	group = strings.TrimSpace(group)
	if group == "" {
		return
	}
	for _, existing := range w.manifest.ExecutionGroups {
		if existing == group {
			return
		}
	}
	w.manifest.ExecutionGroups = append(w.manifest.ExecutionGroups, group)
	sort.Strings(w.manifest.ExecutionGroups)
}

// setTerminalMetrics retains the compact aggregate/SLA evidence needed to
// reconstruct a terminal stream tombstone after process restart. Raw
// transaction samples are already durable in transactions.jsonl/parquet and
// are intentionally omitted here to keep the manifest bounded.
func (w *archiveRecorder) setTerminalMetrics(batch *pb.MetricsBatch) error {
	if w == nil || batch == nil {
		return nil
	}
	terminal := proto.Clone(batch).(*pb.MetricsBatch)
	terminal.Transactions = nil
	data, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(terminal)
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return fmt.Errorf("generator archive recorder is closed")
	}
	if err != nil {
		w.recordErr = fmt.Errorf("encode terminal generator metrics: %w", err)
		return w.recordErr
	}
	w.manifest.TerminalMetrics = append(w.manifest.TerminalMetrics[:0], data...)
	return nil
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
	sharedFree    *generatorArchiveFreeBudget
}

type generatorArchiveFreeBudget struct {
	mu        sync.Mutex
	root      string
	reserved  int64
	freeBytes func(string) (int64, error)
}

func generatorArchiveFreeBytes(path string) (int64, error) {
	return diskspace.AvailablePath(path)
}

func (b *generatorArchiveFreeBudget) reserve(length int64, minFreeBytes int64) (func(), error) {
	if b == nil {
		return func() {}, nil
	}
	b.mu.Lock()
	freeBytes := b.freeBytes
	if freeBytes == nil {
		freeBytes = generatorArchiveFreeBytes
	}
	free, err := freeBytes(b.root)
	if err != nil {
		b.mu.Unlock()
		return nil, err
	}
	available := free - b.reserved
	if available < minFreeBytes || length > available-minFreeBytes {
		b.mu.Unlock()
		return nil, fmt.Errorf("generator archive free space would fall below reserve %d", minFreeBytes)
	}
	b.reserved += length
	b.mu.Unlock()
	return func() {
		b.mu.Lock()
		b.reserved -= length
		if b.reserved < 0 {
			b.reserved = 0
		}
		b.mu.Unlock()
	}, nil
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
	if b.sharedFree != nil {
		release, err := b.sharedFree.reserve(length, b.minFreeBytes)
		if err != nil {
			return 0, err
		}
		defer release()
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

type parquetTransactionEvent struct {
	WorkerDurationMs     float64 `parquet:"name=worker_duration_ms, type=DOUBLE"`
	TimestampUnixMs      int64   `parquet:"name=timestamp_unix_ms, type=INT64"`
	TimestampRFC3339     string  `parquet:"name=timestamp_rfc3339, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	TestID               string  `parquet:"name=test_id, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	TestName             string  `parquet:"name=test_name, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	Group                string  `parquet:"name=group_name, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	Region               string  `parquet:"name=region, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	Hierarchy            string  `parquet:"name=hierarchy, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	PageName             string  `parquet:"name=page_name, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	Phase                string  `parquet:"name=phase, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	HasDetail            bool    `parquet:"name=has_detail, type=BOOLEAN"`
	ActionName           string  `parquet:"name=action_name, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	Population           string  `parquet:"name=population, type=BYTE_ARRAY, convertedtype=UTF8"`
	BusinessTransaction  bool    `parquet:"name=business_transaction, type=BOOLEAN"`
	DurationMs           float64 `parquet:"name=duration_ms, type=DOUBLE"`
	DNSMs                float64 `parquet:"name=dns_ms, type=DOUBLE"`
	ConnectMs            float64 `parquet:"name=connect_ms, type=DOUBLE"`
	TLSMs                float64 `parquet:"name=tls_ms, type=DOUBLE"`
	TTFBMs               float64 `parquet:"name=ttfb_ms, type=DOUBLE"`
	BytesSent            int64   `parquet:"name=bytes_sent, type=INT64"`
	BytesReceived        int64   `parquet:"name=bytes_received, type=INT64"`
	StatusCode           int64   `parquet:"name=status_code, type=INT64"`
	Success              bool    `parquet:"name=success, type=BOOLEAN"`
	ErrorMessage         string  `parquet:"name=error_message, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	NetworkProfile       string  `parquet:"name=network_profile, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	SourceTransaction    string  `parquet:"name=source_transaction, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	SourceProfile        string  `parquet:"name=source_profile, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	NetworkEntriesJSON   string  `parquet:"name=network_entries_json, type=BYTE_ARRAY, convertedtype=UTF8"`
	ServerTelemetryJSON  string  `parquet:"name=server_telemetry_json, type=BYTE_ARRAY, convertedtype=UTF8"`
	ProtocolEvidenceJSON string  `parquet:"name=protocol_evidence_json, type=BYTE_ARRAY, convertedtype=UTF8"`
	ClientMetricsJSON    string  `parquet:"name=client_metrics_json, type=BYTE_ARRAY, convertedtype=UTF8"`
	ExtractionsJSON      string  `parquet:"name=extractions_json, type=BYTE_ARRAY, convertedtype=UTF8"`
	SnapshotJSON         string  `parquet:"name=snapshot_json, type=BYTE_ARRAY, convertedtype=UTF8"`
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
	TimestampUnixMs    int64   `parquet:"name=timestamp_unix_ms, type=INT64"`
	TimestampRFC3339   string  `parquet:"name=timestamp_rfc3339, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	TestID             string  `parquet:"name=test_id, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	TestName           string  `parquet:"name=test_name, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	ActiveVUsers       int64   `parquet:"name=active_vusers, type=INT64"`
	TPS                float64 `parquet:"name=tps, type=DOUBLE"`
	AvgResponseMs      float64 `parquet:"name=avg_response_ms, type=DOUBLE"`
	ErrorPct           float64 `parquet:"name=error_pct, type=DOUBLE"`
	TotalTx            int64   `parquet:"name=total_transactions, type=INT64"`
	TotalErrors        int64   `parquet:"name=total_errors, type=INT64"`
	P50Ms              float64 `parquet:"name=p50_ms, type=DOUBLE"`
	P90Ms              float64 `parquet:"name=p90_ms, type=DOUBLE"`
	P95Ms              float64 `parquet:"name=p95_ms, type=DOUBLE"`
	P99Ms              float64 `parquet:"name=p99_ms, type=DOUBLE"`
	DurationDigestJSON string  `parquet:"name=duration_digest_json, type=BYTE_ARRAY, convertedtype=UTF8"`
	GroupsJSON         string  `parquet:"name=groups_json, type=BYTE_ARRAY, convertedtype=UTF8"`
	TransactionsJSON   string  `parquet:"name=transactions_json, type=BYTE_ARRAY, convertedtype=UTF8"`
	ByRegionJSON       string  `parquet:"name=by_region_json, type=BYTE_ARRAY, convertedtype=UTF8"`
	BrowserJSON        string  `parquet:"name=browser_json, type=BYTE_ARRAY, convertedtype=UTF8"`
	BrowserDeviceJSON  string  `parquet:"name=browser_by_device_json, type=BYTE_ARRAY, convertedtype=UTF8"`
	TelemetryJSON      string  `parquet:"name=telemetry_json, type=BYTE_ARRAY, convertedtype=UTF8"`
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
	if existingErr == nil {
		// No indexed completed archive may reach recorder creation (AssignWork
		// rejects those IDs). What remains is a crash orphan. Remove the complete
		// identity-pinned directory before reuse so unknown stale artifacts cannot
		// survive into, or be downloaded from, the replacement run.
		staleRoot, staleIdentity, openErr := openGeneratorArchiveRun(namespace, testID, false)
		if openErr != nil {
			return nil, fmt.Errorf("open stale archive dir: %w", openErr)
		}
		_ = staleRoot.Close()
		if removeErr := removeGeneratorArchiveRun(namespace, testID, staleIdentity); removeErr != nil {
			return nil, fmt.Errorf("remove stale archive dir: %w", removeErr)
		}
	} else if !os.IsNotExist(existingErr) {
		return nil, fmt.Errorf("inspect archive dir: %w", existingErr)
	}
	createdRun := true
	runRoot, runIdentity, err := openGeneratorArchiveRun(namespace, testID, true)
	if err != nil {
		return nil, fmt.Errorf("create archive dir: %w", err)
	}
	archiveDir := filepath.Join(namespace.path, testID)
	budget := &generatorArchiveByteBudget{
		root: archiveDir, maxBytes: configuredGeneratorArchiveMaxBytes(),
		minFreeBytes: configuredGeneratorArchiveMinFreeBytes(),
		sharedFree:   namespace.freeBudget,
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
			TestName:                     truncateSnapshotField(testName, 512),
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

// removeFinalizedRun removes a recorder's identity-pinned run even after
// Finalize has closed its descriptors. It is used only when durable files were
// produced but registration in the service archive index failed; leaving that
// directory untracked would let a same-ID retry overwrite evidence in place.
func (w *archiveRecorder) removeFinalizedRun() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	namespace := w.namespace
	runName := w.runName
	runIdentity := w.runIdentity
	w.mu.Unlock()
	if namespace == nil || strings.TrimSpace(namespace.path) == "" || runName == "" || runIdentity == nil {
		return nil
	}
	if namespace.root != nil {
		err := removeGeneratorArchiveRun(namespace, runName, runIdentity)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	canonicalPath, root, err := openCanonicalArchiveDirectory(namespace.path, false)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer root.Close()
	if filepath.Clean(canonicalPath) != filepath.Clean(namespace.path) {
		return fmt.Errorf("generator archive namespace changed after finalization")
	}
	reopened := &generatorArchiveNamespace{path: canonicalPath, root: root}
	err = removeGeneratorArchiveRun(reopened, runName, runIdentity)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

const (
	maxGeneratorArchiveNamespaceBytes   = 255
	maxGeneratorArchiveArtifactBytes    = 4 << 10
	maxGeneratorArchiveJSONLRecordBytes = 64 << 20
)

func validateArchiveTestID(testID string) error {
	if testID == "" {
		return fmt.Errorf("test id is empty")
	}
	if len(testID) > maxGeneratorArchiveNamespaceBytes {
		return fmt.Errorf("test id exceeds %d bytes", maxGeneratorArchiveNamespaceBytes)
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
	if w.recordErr != nil {
		return w.recordErr
	}
	event := archiveTransactionEvent{
		CSVExportIDs:          result.CSVExportIDs,
		ExtractionsIncomplete: result.ExtractionsIncomplete,
		Timestamp:             result.StartTime,
		TestID:                w.manifest.TestID,
		TestName:              w.manifest.TestName,
		Group:                 truncateSnapshotField(result.Group, 512),
		Region:                truncateSnapshotField(result.Region, 512),
		Hierarchy:             truncateSnapshotField(firstNonEmptyString(result.Hierarchy, result.SourcePath), 2048),
		PageName:              truncateSnapshotField(result.PageName, 512),
		Phase:                 truncateSnapshotField(result.Phase, 64),
		MessageDetailPriority: result.MessageDetailPriority,
		ActionName:            truncateSnapshotField(result.ActionName, 512),
		BusinessTransaction:   result.BusinessTransaction,
		Population:            archiveResultPopulation(result.BusinessTransaction),
		DurationMs:            durationMillis(result.Duration),
		WorkerDurationMs:      durationMillis(result.WorkerDuration),
		DNSMs:                 durationMillis(result.DNSTime),
		ConnectMs:             durationMillis(result.ConnectTime),
		TLSMs:                 durationMillis(result.TLSTime),
		TTFBMs:                durationMillis(result.TTFB),
		BytesSent:             result.BytesSent,
		BytesReceived:         result.BytesReceived,
		StatusCode:            result.StatusCode,
		Success:               result.Success,
		ErrorMessage:          truncateSnapshotField(result.ErrorMessage, 16<<10),
		NetworkProfile:        truncateSnapshotField(result.NetworkProfile, 512),
		CorrelationID:         truncateSnapshotField(result.CorrelationID, 512),
		DependencyType:        truncateSnapshotField(result.DependencyType, 512),
		DependencyTarget:      truncateSnapshotField(result.DependencyTarget, 2048),
		SourceStepID:          truncateSnapshotField(result.SourceStepID, 512),
		SourceStepIndex:       result.SourceStepIndex,
		SourceAction:          truncateSnapshotField(result.SourceAction, 512),
		SourceActionName:      truncateSnapshotField(result.SourceActionName, 512),
		SourceTransaction:     truncateSnapshotField(result.SourceTransaction, 512),
		SourceProfile:         truncateSnapshotField(result.SourceProfile, 512),
		PageURL:               truncateSnapshotField(result.PageURL, 2048),
		PageTitle:             truncateSnapshotField(result.PageTitle, 512),
		ConsoleEntries:        result.ConsoleEntries,
		NetworkEntries:        result.NetworkEntries,
		DOMSummary:            result.DOMSummary,
		ClientMetrics:         cloneArchiveClientMetrics(result.ClientMetrics),
		ServerTelemetry:       result.ServerTelemetry,
		ProtocolEvidence:      result.ProtocolEvidence,
		Extractions:           boundedArchiveExtractions(result.Extractions),
		Snapshot:              engine.BoundErrorSnapshot(result.Snapshot, 1<<20),
	}
	if len(event.Extractions) != len(result.Extractions) {
		event.ExtractionsIncomplete = true
	}
	for k, v := range result.Extractions {
		if event.Extractions[k] != v {
			event.ExtractionsIncomplete = true
		}
	}
	event.HasDetail = event.Snapshot != nil || event.ClientMetrics != nil || event.ServerTelemetry != nil ||
		len(event.ProtocolEvidence) > 0 || len(event.NetworkEntries) > 0 || len(event.ConsoleEntries) > 0 || event.DOMSummary != nil
	w.applyMessageDetailBudget(&event, result)
	if err := w.txEncoder.Encode(event); err != nil {
		w.recordErr = fmt.Errorf("record transaction: %w", err)
		return w.recordErr
	}
	w.manifest.TransactionCount++
	return nil
}

func (w *archiveRecorder) applyMessageDetailBudget(event *archiveTransactionEvent, result *protocol.Result) {
	if w == nil || event == nil || result == nil || !result.Success || !result.MessageDetailRequested {
		return
	}
	if w.manifest.MessageDetailCapture == nil {
		w.manifest.MessageDetailCapture = &archiveMessageDetailCapture{Dropped: make(map[string]int64)}
	}
	stats := w.manifest.MessageDetailCapture
	drop := func(reason string) {
		stats.Dropped[reason]++
		event.Snapshot = nil
		result.Snapshot = nil
		result.MessageDetailRetained = false
		event.HasDetail = event.ClientMetrics != nil || event.ServerTelemetry != nil || len(event.ProtocolEvidence) > 0 || len(event.NetworkEntries) > 0 || len(event.ConsoleEntries) > 0 || event.DOMSummary != nil
	}
	if event.Snapshot == nil {
		drop("protocol_no_detail")
		return
	}
	maxItems := int64(result.MessageDetailMaxItems)
	maxItemBytes, maxTotalBytes := result.MessageDetailMaxItemBytes, result.MessageDetailMaxTotalBytes
	if maxItems <= 0 || maxItemBytes <= 0 || maxTotalBytes <= 0 {
		drop("invalid_budget")
		return
	}
	priority := 0
	priorityItemLimit, priorityByteLimit := maxItems, maxTotalBytes
	if strings.EqualFold(result.MessageDetailMode, "adaptive") {
		priority = min(max(result.MessageDetailPriority, 1), 3)
		priorityItemLimit = adaptiveMessageDetailPriorityLimit(maxItems, priority)
		priorityByteLimit = adaptiveMessageDetailPriorityLimit(maxTotalBytes, priority)
		if w.detailPriorityItems[priority] >= priorityItemLimit || priorityItemLimit == 0 {
			drop(fmt.Sprintf("priority_%d_item_limit", priority))
			return
		}
		if w.detailPriorityBytes[priority] >= priorityByteLimit || priorityByteLimit == 0 {
			drop(fmt.Sprintf("priority_%d_byte_limit", priority))
			return
		}
	}
	if w.detailItems >= maxItems {
		drop("item_limit")
		return
	}
	remaining := maxTotalBytes - w.detailBytes
	if priority > 0 {
		remaining = min(remaining, priorityByteLimit-w.detailPriorityBytes[priority])
	}
	if remaining <= 0 {
		drop("total_byte_limit")
		return
	}
	limit := min(maxItemBytes, remaining)
	before := generatorMessageDetailSnapshotBytes(event.Snapshot)
	if before > limit {
		event.Snapshot = engine.BoundErrorSnapshot(event.Snapshot, int(limit))
		stats.TruncatedItems++
	}
	retained := generatorMessageDetailSnapshotBytes(event.Snapshot)
	if event.Snapshot == nil || retained <= 0 || retained > limit {
		drop("total_byte_limit")
		return
	}
	w.detailItems++
	w.detailBytes += retained
	if priority > 0 {
		w.detailPriorityItems[priority]++
		w.detailPriorityBytes[priority] += retained
	}
	stats.RetainedItems++
	stats.RetainedBytes += retained
	result.Snapshot = event.Snapshot
	result.MessageDetailRetained = true
	event.HasDetail = true
}

func adaptiveMessageDetailPriorityLimit(total int64, priority int) int64 {
	if total <= 0 || priority < 1 || priority > 3 {
		return 0
	}
	high := (total + 1) / 2
	remaining := total - high
	low := remaining / 2
	middle := remaining - low
	switch priority {
	case 1:
		return low
	case 2:
		return middle
	default:
		return high
	}
}

func generatorMessageDetailSnapshotBytes(snapshot *protocol.ErrorSnapshot) int64 {
	if snapshot == nil {
		return 0
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return 0
	}
	return int64(len(encoded))
}

// RecordTransaction persists engine-synthesized business transactions. The
// engine routes those only through TransactionSink; adapter-supplied business
// transaction results continue through RecordResult and are not delivered here.
func (w *archiveRecorder) RecordTransaction(result *protocol.Result) error {
	return w.RecordResult(result)
}

func cloneArchiveClientMetrics(metrics *protocol.ClientMetrics) *protocol.ClientMetrics {
	if metrics == nil {
		return nil
	}
	cloned := *metrics
	cloned.DeviceProfile = truncateSnapshotField(cloned.DeviceProfile, 512)
	cloned.DevicePlatform = truncateSnapshotField(cloned.DevicePlatform, 512)
	cloned.DeviceTier = truncateSnapshotField(cloned.DeviceTier, 512)
	cloned.BrowserEngine = truncateSnapshotField(cloned.BrowserEngine, 512)
	return &cloned
}

func boundedArchiveExtractions(values map[string]string) map[string]string {
	const (
		maxEntries = 256
		maxBytes   = 64 << 10
	)
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, min(len(values), maxEntries))
	remaining := maxBytes
	for _, originalKey := range boundedtext.SmallestStringKeys(values, maxEntries) {
		if len(out) >= maxEntries || remaining <= 0 {
			break
		}
		key := originalKey
		value := values[originalKey]
		key = truncateSnapshotField(key, min(4<<10, remaining))
		remaining -= len(key)
		if key == "" || remaining <= 0 {
			continue
		}
		value = truncateSnapshotField(value, min(16<<10, remaining))
		remaining -= len(value)
		out[key] = value
	}
	return out
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
	if w.recordErr != nil {
		return w.recordErr
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
		w.recordErr = fmt.Errorf("record resource sample: %w", err)
		return w.recordErr
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
	if w.recordErr != nil {
		return w.recordErr
	}
	summary.TestID = w.manifest.TestID
	summary.TestName = w.manifest.TestName
	if err := w.summaryEncoder.Encode(summary); err != nil {
		w.recordErr = fmt.Errorf("record interval summary: %w", err)
		return w.recordErr
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
	if w.recordErr != nil {
		return nil, fmt.Errorf("generator archive recording failed: %w", w.recordErr)
	}
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

// claimRecordingFailure returns the first asynchronous write failure once.
// Result sinks are intentionally non-blocking and the engine does not consume
// their returned errors, so the metrics publisher uses this edge-trigger to
// fail and cancel the run instead of continuing to generate unarchived load.
func (w *archiveRecorder) claimRecordingFailure() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.recordErr == nil || w.failureSignaled {
		return nil
	}
	w.failureSignaled = true
	return w.recordErr
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
	// Bounded per-record scanning keeps a single oversized JSONL line from
	// being decoded wholesale into generator memory.
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 0, 64*1024), maxGeneratorArchiveJSONLRecordBytes)
	for scanner.Scan() {
		var item In
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
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
	if err := scanner.Err(); err != nil {
		_ = pw.WriteStop()
		_ = fw.Close()
		return err
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
			TimestampUnixMs:      item.Timestamp.UnixMilli(),
			TimestampRFC3339:     item.Timestamp.Format(time.RFC3339Nano),
			TestID:               item.TestID,
			TestName:             item.TestName,
			Group:                item.Group,
			Region:               item.Region,
			Hierarchy:            item.Hierarchy,
			PageName:             item.PageName,
			Phase:                item.Phase,
			HasDetail:            item.HasDetail,
			ActionName:           item.ActionName,
			BusinessTransaction:  item.BusinessTransaction,
			Population:           item.Population,
			DurationMs:           item.DurationMs,
			WorkerDurationMs:     item.WorkerDurationMs,
			DNSMs:                item.DNSMs,
			ConnectMs:            item.ConnectMs,
			TLSMs:                item.TLSMs,
			TTFBMs:               item.TTFBMs,
			BytesSent:            item.BytesSent,
			BytesReceived:        item.BytesReceived,
			StatusCode:           int64(item.StatusCode),
			Success:              item.Success,
			ErrorMessage:         item.ErrorMessage,
			NetworkProfile:       item.NetworkProfile,
			SourceTransaction:    item.SourceTransaction,
			SourceProfile:        item.SourceProfile,
			NetworkEntriesJSON:   mustJSON(item.NetworkEntries),
			ServerTelemetryJSON:  mustJSON(item.ServerTelemetry),
			ProtocolEvidenceJSON: mustJSON(item.ProtocolEvidence),
			ClientMetricsJSON:    mustJSON(item.ClientMetrics),
			ExtractionsJSON:      mustJSON(boundedArchiveExtractions(item.Extractions)),
			SnapshotJSON:         mustJSON(engine.BoundErrorSnapshot(item.Snapshot, 1<<20)),
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
			TimestampUnixMs:    item.Timestamp.UnixMilli(),
			TimestampRFC3339:   item.Timestamp.Format(time.RFC3339Nano),
			TestID:             item.TestID,
			TestName:           item.TestName,
			ActiveVUsers:       item.ActiveVUsers,
			TPS:                item.TPS,
			AvgResponseMs:      item.AvgResponseMs,
			ErrorPct:           item.ErrorPct,
			TotalTx:            item.TotalTx,
			TotalErrors:        item.TotalErrors,
			P50Ms:              item.P50Ms,
			P90Ms:              item.P90Ms,
			P95Ms:              item.P95Ms,
			P99Ms:              item.P99Ms,
			DurationDigestJSON: mustJSON(item.DurationDigest),
			GroupsJSON:         mustJSON(item.Groups),
			TransactionsJSON:   mustJSON(item.Transactions),
			ByRegionJSON:       mustJSON(item.ByRegion),
			BrowserJSON:        mustJSON(item.Browser),
			BrowserDeviceJSON:  mustJSON(item.BrowserByDevice),
			TelemetryJSON:      mustJSON(item.Telemetry),
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

func archiveResultPopulation(businessTransaction bool) string {
	if businessTransaction {
		return "transaction"
	}
	return "action"
}

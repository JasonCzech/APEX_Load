package api

import (
	"archive/zip"
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/stormrunner/pkg/engine"
	"github.com/jasonczech/stormrunner/pkg/protocol"
	"github.com/jasonczech/stormrunner/pkg/storage"
)

const archiveSchemaVersion = "stormrunner.archive.v1"
const browserDiagnosticsSchemaVersion = "stormrunner.browser_diagnostics.v1"

type RunArchiveManifest struct {
	SchemaVersion                string               `json:"schema_version"`
	TestID                       string               `json:"test_id"`
	TestName                     string               `json:"test_name"`
	CreatedAt                    time.Time            `json:"created_at"`
	CompletedAt                  time.Time            `json:"completed_at,omitempty"`
	Status                       string               `json:"status,omitempty"`
	Error                        string               `json:"error,omitempty"`
	Provenance                   RunArchiveProvenance `json:"provenance,omitempty"`
	TransactionsFile             string               `json:"transactions_file"`
	TransactionsParquetFile      string               `json:"transactions_parquet_file,omitempty"`
	ResourceSamplesFile          string               `json:"resource_samples_file"`
	ResourceSamplesParquetFile   string               `json:"resource_samples_parquet_file,omitempty"`
	IntervalSummariesFile        string               `json:"interval_summaries_file"`
	IntervalSummariesParquetFile string               `json:"interval_summaries_parquet_file,omitempty"`
	TransactionCount             int64                `json:"transaction_count"`
	ResourceSampleCount          int64                `json:"resource_sample_count"`
	IntervalSummaryCount         int64                `json:"interval_summary_count"`
	SLABreached                  bool                 `json:"sla_breached"`
	SLADetails                   []string             `json:"sla_details,omitempty"`
}

type RunArchiveExecutionSettingsSnapshot struct {
	General          GeneralSettings          `json:"general"`
	Thresholds       ThresholdSettings        `json:"thresholds"`
	CapacityPlanning CapacityPlanningSettings `json:"capacity_planning"`
}

type RunArchiveProvenance struct {
	ApplicationID      string                              `json:"application_id,omitempty"`
	ReleaseID          string                              `json:"release_id,omitempty"`
	ScenarioID         string                              `json:"scenario_id,omitempty"`
	ProjectID          string                              `json:"project_id,omitempty"`
	RequestedAt        time.Time                           `json:"requested_at,omitempty"`
	StartedAt          *time.Time                          `json:"started_at,omitempty"`
	ScenarioMode       string                              `json:"scenario_mode,omitempty"`
	ScriptShakeout     bool                                `json:"script_shakeout,omitempty"`
	ResourceBaseline   bool                                `json:"resource_baseline,omitempty"`
	StartDelay         string                              `json:"start_delay,omitempty"`
	TotalVUsers        int                                 `json:"total_vusers,omitempty"`
	Duration           string                              `json:"duration,omitempty"`
	Assignment         *GeneratorAssignmentSettings        `json:"assignment,omitempty"`
	GoalConfig         *GoalConfig                         `json:"goal_config,omitempty"`
	RuntimeSettings    *RuntimeSettingsOverride            `json:"runtime_settings,omitempty"`
	Groups             []TestGroup                         `json:"groups,omitempty"`
	BaselinePinned     bool                                `json:"baseline_pinned,omitempty"`
	BaselinePinnedAt   *time.Time                          `json:"baseline_pinned_at,omitempty"`
	ExecutionSettings  RunArchiveExecutionSettingsSnapshot `json:"execution_settings"`
	ControllerEnv      map[string]string                   `json:"controller_env,omitempty"`
	GeneratorInventory []GeneratorInventoryEntry           `json:"generator_inventory,omitempty"`
}

type TransactionEvent struct {
	Timestamp        time.Time                 `json:"timestamp"`
	TestID           string                    `json:"test_id"`
	TestName         string                    `json:"test_name"`
	Group            string                    `json:"group,omitempty"`
	ActionName       string                    `json:"action_name,omitempty"`
	CorrelationID    string                    `json:"correlation_id,omitempty"`
	TraceID          string                    `json:"trace_id,omitempty"`
	SpanID           string                    `json:"span_id,omitempty"`
	NetworkProfile   string                    `json:"network_profile,omitempty"`
	DependencyType   string                    `json:"dependency_type,omitempty"`
	DependencyTarget string                    `json:"dependency_target,omitempty"`
	DurationMs       float64                   `json:"duration_ms"`
	DNSMs            float64                   `json:"dns_ms,omitempty"`
	ConnectMs        float64                   `json:"connect_ms,omitempty"`
	TLSMs            float64                   `json:"tls_ms,omitempty"`
	TTFBMs           float64                   `json:"ttfb_ms,omitempty"`
	BytesSent        int64                     `json:"bytes_sent,omitempty"`
	BytesReceived    int64                     `json:"bytes_received,omitempty"`
	StatusCode       int                       `json:"status_code,omitempty"`
	Success          bool                      `json:"success"`
	ErrorMessage     string                    `json:"error_message,omitempty"`
	Extractions      map[string]string         `json:"extractions,omitempty"`
	PageURL          string                    `json:"page_url,omitempty"`
	PageTitle        string                    `json:"page_title,omitempty"`
	ConsoleEntries   []protocol.ConsoleEntry   `json:"console_entries,omitempty"`
	NetworkEntries   []protocol.NetworkEntry   `json:"network_entries,omitempty"`
	DOMSummary       *protocol.DOMSummary      `json:"dom_summary,omitempty"`
	ClientMetrics    *protocol.ClientMetrics   `json:"client_metrics,omitempty"`
	ServerTelemetry  *protocol.ServerTelemetry `json:"server_telemetry,omitempty"`
	Snapshot         *protocol.ErrorSnapshot   `json:"snapshot,omitempty"`
}

type ResourceSample struct {
	Timestamp     time.Time          `json:"timestamp"`
	TestID        string             `json:"test_id"`
	TestName      string             `json:"test_name"`
	ActiveVUsers  int64              `json:"active_vusers"`
	CPUProcessPct float64            `json:"cpu_process_pct"`
	CPUFreePct    float64            `json:"cpu_free_pct"`
	MemProcessMB  float64            `json:"mem_process_mb"`
	MemFreeMB     float64            `json:"mem_free_mb"`
	NetSentKbps   float64            `json:"net_sent_kbps"`
	NetRecvKbps   float64            `json:"net_recv_kbps"`
	DiskReadKbps  float64            `json:"disk_read_kbps"`
	DiskWriteKbps float64            `json:"disk_write_kbps"`
	External      map[string]float64 `json:"external_metrics,omitempty"`
}

type IntervalSummaryRecord struct {
	Timestamp              time.Time                            `json:"timestamp"`
	TestID                 string                               `json:"test_id"`
	TestName               string                               `json:"test_name"`
	ActiveVUsers           int64                                `json:"active_vusers"`
	TPS                    float64                              `json:"tps"`
	TargetTPS              float64                              `json:"target_tps,omitempty"`
	ThroughputGapPct       float64                              `json:"throughput_gap_pct,omitempty"`
	ThroughputTargetMissed bool                                 `json:"throughput_target_missed,omitempty"`
	AvgResponseMs          float64                              `json:"avg_response_ms"`
	ErrorPct               float64                              `json:"error_pct"`
	TotalTx                int64                                `json:"total_transactions"`
	TotalErrors            int64                                `json:"total_errors"`
	P50Ms                  float64                              `json:"p50_ms"`
	P90Ms                  float64                              `json:"p90_ms"`
	P95Ms                  float64                              `json:"p95_ms"`
	P99Ms                  float64                              `json:"p99_ms"`
	Browser                *BrowserLiveMetrics                  `json:"browser,omitempty"`
	BrowserByDevice        map[string]*BrowserDeviceLiveMetrics `json:"browser_by_device,omitempty"`
}

type archivePage struct {
	Limit  int
	Offset int
}

type transactionEventFilter struct {
	Action           string
	SOAPOperation    string
	Group            string
	CorrelationID    string
	TraceID          string
	DependencyType   string
	DependencyTarget string
	Success          *bool
}

type RunArchiveReport struct {
	Manifest            *RunArchiveManifest                    `json:"manifest"`
	Overview            RunArchiveOverview                     `json:"overview"`
	ErrorPatterns       []RunArchiveErrorPattern               `json:"error_patterns,omitempty"`
	SummaryTimeline     []RunArchiveSummaryPoint               `json:"summary_timeline,omitempty"`
	ByAction            map[string]RunArchiveActionStats       `json:"by_action"`
	BySOAPOperation     map[string]RunArchiveActionStats       `json:"by_soap_operation,omitempty"`
	ByNetworkProfile    map[string]RunArchiveActionStats       `json:"by_network_profile,omitempty"`
	BySOAPFaultCode     map[string]int64                       `json:"by_soap_fault_code,omitempty"`
	BySOAPFaultCategory map[string]int64                       `json:"by_soap_fault_category,omitempty"`
	SOAPFaultTimeline   []RunArchiveSOAPFaultTimelineBucket    `json:"soap_fault_timeline,omitempty"`
	SOAPPayloadTimeline []RunArchiveSOAPPayloadTimelineBucket  `json:"soap_payload_timeline,omitempty"`
	BrowserByEngine     map[string]*BrowserEngineLiveMetrics   `json:"browser_by_engine,omitempty"`
	BrowserByPlatform   map[string]*BrowserPlatformLiveMetrics `json:"browser_by_platform,omitempty"`
	BrowserByDevice     map[string]*BrowserDeviceLiveMetrics   `json:"browser_by_device,omitempty"`
	BrowserResources    []BrowserResourceTiming                `json:"browser_resources,omitempty"`
	ResourcePeaks       RunArchiveResourcePeaks                `json:"resource_peaks"`
}

type BrowserResourceTiming struct {
	Timestamp      time.Time `json:"timestamp"`
	ActionName     string    `json:"action_name,omitempty"`
	PageURL        string    `json:"page_url,omitempty"`
	URL            string    `json:"url,omitempty"`
	Method         string    `json:"method,omitempty"`
	StatusCode     int       `json:"status_code,omitempty"`
	ResourceType   string    `json:"resource_type,omitempty"`
	DurationMs     float64   `json:"duration_ms,omitempty"`
	BytesReceived  int64     `json:"bytes_received,omitempty"`
	DeviceProfile  string    `json:"device_profile,omitempty"`
	DevicePlatform string    `json:"device_platform,omitempty"`
	BrowserEngine  string    `json:"browser_engine,omitempty"`
}

type RunArchiveSummaryPoint struct {
	Timestamp              time.Time `json:"timestamp"`
	ActiveVUsers           int64     `json:"active_vusers"`
	TPS                    float64   `json:"tps"`
	TargetTPS              float64   `json:"target_tps,omitempty"`
	ThroughputGapPct       float64   `json:"throughput_gap_pct,omitempty"`
	ThroughputTargetMissed bool      `json:"throughput_target_missed,omitempty"`
	ErrorPct               float64   `json:"error_pct"`
	P95Ms                  float64   `json:"p95_ms"`
	TotalTx                int64     `json:"total_transactions"`
	TotalErrors            int64     `json:"total_errors"`
}

type RunArchiveErrorPattern struct {
	Signature  string                  `json:"signature"`
	Sample     string                  `json:"sample"`
	Count      int64                   `json:"count"`
	FirstSeen  time.Time               `json:"first_seen"`
	LatestSeen time.Time               `json:"latest_seen"`
	Actions    []string                `json:"actions,omitempty"`
	Snapshot   *protocol.ErrorSnapshot `json:"snapshot,omitempty"`
}

type RunArchiveSOAPFaultTimelineBucket struct {
	Timestamp  time.Time        `json:"timestamp"`
	Total      int64            `json:"total"`
	ByCode     map[string]int64 `json:"by_code,omitempty"`
	ByCategory map[string]int64 `json:"by_category,omitempty"`
}

type RunArchiveSOAPPayloadTimelineBucket struct {
	Timestamp             time.Time `json:"timestamp"`
	Samples               int64     `json:"samples"`
	AvgRequestPayloadKB   float64   `json:"avg_request_payload_kb,omitempty"`
	AvgResponsePayloadKB  float64   `json:"avg_response_payload_kb,omitempty"`
	AvgEnvelopeOverheadKB float64   `json:"avg_envelope_overhead_kb,omitempty"`
	MaxEnvelopeOverheadKB float64   `json:"max_envelope_overhead_kb,omitempty"`
}

type BrowserEngineLiveMetrics struct {
	BrowserEngine        string  `json:"browser_engine,omitempty"`
	Samples              int64   `json:"samples"`
	AvgFCPMs             float64 `json:"avg_fcp_ms,omitempty"`
	AvgLCPMs             float64 `json:"avg_lcp_ms,omitempty"`
	AvgCLSScore          float64 `json:"avg_cls_score,omitempty"`
	AvgINPMs             float64 `json:"avg_inp_ms,omitempty"`
	AvgBlockingResources float64 `json:"avg_blocking_resources,omitempty"`
	AvgLongTaskCount     float64 `json:"avg_long_task_count,omitempty"`
	AvgJSHeapUsedMB      float64 `json:"avg_js_heap_used_mb,omitempty"`
	AvgBrowserProcessCPU float64 `json:"avg_browser_process_cpu_pct,omitempty"`
	AvgBrowserProcessMem float64 `json:"avg_browser_process_mem_mb,omitempty"`
}

type BrowserPlatformLiveMetrics struct {
	DevicePlatform       string  `json:"device_platform,omitempty"`
	BrowserEngine        string  `json:"browser_engine,omitempty"`
	Samples              int64   `json:"samples"`
	AvgFCPMs             float64 `json:"avg_fcp_ms,omitempty"`
	AvgLCPMs             float64 `json:"avg_lcp_ms,omitempty"`
	AvgCLSScore          float64 `json:"avg_cls_score,omitempty"`
	AvgINPMs             float64 `json:"avg_inp_ms,omitempty"`
	AvgBlockingResources float64 `json:"avg_blocking_resources,omitempty"`
	AvgLongTaskCount     float64 `json:"avg_long_task_count,omitempty"`
	AvgJSHeapUsedMB      float64 `json:"avg_js_heap_used_mb,omitempty"`
	AvgBrowserProcessCPU float64 `json:"avg_browser_process_cpu_pct,omitempty"`
	AvgBrowserProcessMem float64 `json:"avg_browser_process_mem_mb,omitempty"`
}

type RunArchiveOverview struct {
	Transactions              int64               `json:"transactions"`
	Errors                    int64               `json:"errors"`
	ErrorPct                  float64             `json:"error_pct"`
	AvgResponseMs             float64             `json:"avg_response_ms"`
	MinResponseMs             float64             `json:"min_response_ms"`
	MaxResponseMs             float64             `json:"max_response_ms"`
	ResourceSamples           int64               `json:"resource_samples"`
	IntervalSummaries         int64               `json:"interval_summaries"`
	PeakTPS                   float64             `json:"peak_tps"`
	PeakTargetTPS             float64             `json:"peak_target_tps,omitempty"`
	MaxThroughputGapPct       float64             `json:"max_throughput_gap_pct,omitempty"`
	ThroughputMissedIntervals int64               `json:"throughput_missed_intervals,omitempty"`
	PeakActiveVUsers          int64               `json:"peak_active_vusers"`
	Browser                   *BrowserLiveMetrics `json:"browser,omitempty"`
}

type RunArchiveActionStats struct {
	Transactions  int64   `json:"transactions"`
	Errors        int64   `json:"errors"`
	ErrorPct      float64 `json:"error_pct"`
	AvgResponseMs float64 `json:"avg_response_ms"`
	MinResponseMs float64 `json:"min_response_ms"`
	MaxResponseMs float64 `json:"max_response_ms"`
}

type RunArchiveResourcePeaks struct {
	CPUProcessPct float64 `json:"cpu_process_pct"`
	MemProcessMB  float64 `json:"mem_process_mb"`
	NetSentKbps   float64 `json:"net_sent_kbps"`
	NetRecvKbps   float64 `json:"net_recv_kbps"`
	DiskReadKbps  float64 `json:"disk_read_kbps"`
	DiskWriteKbps float64 `json:"disk_write_kbps"`
}

type BrowserDiagnosticsBundle struct {
	SchemaVersion string                     `json:"schema_version"`
	GeneratedAt   time.Time                  `json:"generated_at"`
	Manifest      *RunArchiveManifest        `json:"manifest"`
	Overview      BrowserDiagnosticsOverview `json:"overview"`
	Events        []BrowserDiagnosticsEvent  `json:"events"`
}

type BrowserDiagnosticsOverview struct {
	BrowserTransactions       int64 `json:"browser_transactions"`
	FailedBrowserTransactions int64 `json:"failed_browser_transactions"`
	ClientMetricSamples       int64 `json:"client_metric_samples"`
	ScreenshotArtifacts       int64 `json:"screenshot_artifacts"`
	HTMLArtifacts             int64 `json:"html_artifacts"`
	ConsoleEntries            int64 `json:"console_entries"`
	NetworkEntries            int64 `json:"network_entries"`
	DOMSnapshots              int64 `json:"dom_snapshots"`
}

type BrowserDiagnosticsEvent struct {
	Index          int                        `json:"index"`
	Timestamp      time.Time                  `json:"timestamp"`
	Group          string                     `json:"group,omitempty"`
	ActionName     string                     `json:"action_name,omitempty"`
	DurationMs     float64                    `json:"duration_ms"`
	StatusCode     int                        `json:"status_code,omitempty"`
	Success        bool                       `json:"success"`
	ErrorMessage   string                     `json:"error_message,omitempty"`
	PageURL        string                     `json:"page_url,omitempty"`
	PageTitle      string                     `json:"page_title,omitempty"`
	ClientMetrics  *protocol.ClientMetrics    `json:"client_metrics,omitempty"`
	ConsoleEntries []protocol.ConsoleEntry    `json:"console_entries,omitempty"`
	NetworkEntries []protocol.NetworkEntry    `json:"network_entries,omitempty"`
	DOMSummary     *protocol.DOMSummary       `json:"dom_summary,omitempty"`
	Snapshot       *BrowserDiagnosticSnapshot `json:"snapshot,omitempty"`
	Artifacts      BrowserDiagnosticArtifacts `json:"artifacts,omitempty"`

	screenshotBase64 string
	html             string
}

type BrowserDiagnosticSnapshot struct {
	RequestMethod   string            `json:"request_method,omitempty"`
	RequestURL      string            `json:"request_url,omitempty"`
	RequestHeaders  map[string]string `json:"request_headers,omitempty"`
	RequestBody     string            `json:"request_body,omitempty"`
	ResponseStatus  int               `json:"response_status,omitempty"`
	ResponseHeaders map[string]string `json:"response_headers,omitempty"`
	ResponseBody    string            `json:"response_body,omitempty"`
}

type BrowserDiagnosticArtifacts struct {
	ScreenshotFile string `json:"screenshot_file,omitempty"`
	HTMLFile       string `json:"html_file,omitempty"`
}

var archiveErrorUUIDPattern = regexp.MustCompile(`\b[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}\b`)
var archiveErrorNumberPattern = regexp.MustCompile(`\b\d+\b`)
var archiveErrorWhitespacePattern = regexp.MustCompile(`\s+`)

type runArchiveWriter struct {
	mu              sync.Mutex
	manifest        RunArchiveManifest
	rootDir         string
	manifestPath    string
	txFile          *os.File
	resourceFile    *os.File
	summaryFile     *os.File
	txEncoder       *json.Encoder
	resourceEncoder *json.Encoder
	summaryEncoder  *json.Encoder
}

func newRunArchiveWriter(test *ManagedTest, provenance RunArchiveProvenance) (*runArchiveWriter, error) {
	rootDir := resolveArchiveRoot()
	archiveDir := filepath.Join(rootDir, test.ID)
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
		txFile.Close()
		return nil, fmt.Errorf("open resources archive: %w", err)
	}
	summaryFile, summaryEncoder, err := open("interval_summaries.jsonl")
	if err != nil {
		txFile.Close()
		resourceFile.Close()
		return nil, fmt.Errorf("open summaries archive: %w", err)
	}

	return &runArchiveWriter{
		manifest: RunArchiveManifest{
			SchemaVersion:                archiveSchemaVersion,
			TestID:                       test.ID,
			TestName:                     test.Name,
			CreatedAt:                    time.Now(),
			Provenance:                   provenance,
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

func cloneArchiveTestGroups(groups []TestGroup) []TestGroup {
	if len(groups) == 0 {
		return nil
	}
	cloned := make([]TestGroup, len(groups))
	copy(cloned, groups)
	return cloned
}

func cloneGeneratorInventoryEntries(entries []GeneratorInventoryEntry) []GeneratorInventoryEntry {
	if len(entries) == 0 {
		return nil
	}
	cloned := make([]GeneratorInventoryEntry, len(entries))
	copy(cloned, entries)
	return cloned
}

func captureStormrunnerEnv() map[string]string {
	env := make(map[string]string)
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || !strings.HasPrefix(key, "STORMRUNNER_") {
			continue
		}
		upper := strings.ToUpper(key)
		if strings.Contains(upper, "SECRET") || strings.Contains(upper, "TOKEN") || strings.Contains(upper, "PASSWORD") || strings.Contains(upper, "KEY") {
			continue
		}
		env[key] = value
	}
	if len(env) == 0 {
		return nil
	}
	return env
}

func resolveArchiveRoot() string {
	if dir := os.Getenv("STORMRUNNER_ARCHIVE_DIR"); dir != "" {
		return dir
	}
	return filepath.Join("artifacts", "archives")
}

func (w *runArchiveWriter) RecordResult(result *protocol.Result) error {
	if result == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	event := TransactionEvent{
		Timestamp:        result.StartTime,
		TestID:           w.manifest.TestID,
		TestName:         w.manifest.TestName,
		Group:            result.Group,
		ActionName:       result.ActionName,
		CorrelationID:    result.CorrelationID,
		TraceID:          traceIDFromCorrelationID(result.CorrelationID),
		SpanID:           spanIDFromCorrelationID(result.CorrelationID),
		NetworkProfile:   result.NetworkProfile,
		DependencyType:   result.DependencyType,
		DependencyTarget: result.DependencyTarget,
		DurationMs:       durationMillis(result.Duration),
		DNSMs:            durationMillis(result.DNSTime),
		ConnectMs:        durationMillis(result.ConnectTime),
		TLSMs:            durationMillis(result.TLSTime),
		TTFBMs:           durationMillis(result.TTFB),
		BytesSent:        result.BytesSent,
		BytesReceived:    result.BytesReceived,
		StatusCode:       result.StatusCode,
		Success:          result.Success,
		ErrorMessage:     result.ErrorMessage,
		Extractions:      result.Extractions,
		PageURL:          result.PageURL,
		PageTitle:        result.PageTitle,
		ConsoleEntries:   cloneProtocolConsoleEntries(result.ConsoleEntries),
		NetworkEntries:   cloneProtocolNetworkEntries(result.NetworkEntries),
		DOMSummary:       cloneProtocolDOMSummary(result.DOMSummary),
		ClientMetrics:    result.ClientMetrics,
		ServerTelemetry:  result.ServerTelemetry,
		Snapshot:         result.Snapshot,
	}
	if err := w.txEncoder.Encode(event); err != nil {
		return err
	}
	w.manifest.TransactionCount++
	return nil
}

func (w *runArchiveWriter) RecordResourceSample(timestamp time.Time, activeVUsers int64, resources *engine.SystemResources) error {
	if resources == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	record := ResourceSample{
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
		External:      cloneFloat64Map(resources.External),
	}
	if err := w.resourceEncoder.Encode(record); err != nil {
		return err
	}
	w.manifest.ResourceSampleCount++
	return nil
}

func (w *runArchiveWriter) RecordIntervalSummary(live *LiveMetrics) error {
	if live == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	record := IntervalSummaryRecord{
		Timestamp:              live.Timestamp,
		TestID:                 w.manifest.TestID,
		TestName:               w.manifest.TestName,
		ActiveVUsers:           live.ActiveVUsers,
		TPS:                    live.TPS,
		TargetTPS:              live.TargetTPS,
		ThroughputGapPct:       live.ThroughputGapPct,
		ThroughputTargetMissed: live.ThroughputTargetMissed,
		AvgResponseMs:          live.AvgResponseMs,
		ErrorPct:               live.ErrorPct,
		TotalTx:                live.TotalTx,
		TotalErrors:            live.TotalErrors,
		P50Ms:                  live.P50Ms,
		P90Ms:                  live.P90Ms,
		P95Ms:                  live.P95Ms,
		P99Ms:                  live.P99Ms,
		Browser:                cloneBrowserLiveMetrics(live.Browser),
		BrowserByDevice:        cloneBrowserByDeviceMetrics(live.BrowserByDevice),
	}
	if err := w.summaryEncoder.Encode(record); err != nil {
		return err
	}
	w.manifest.IntervalSummaryCount++
	return nil
}

func (w *runArchiveWriter) Finalize(status string, completedAt time.Time, errStr string, slaBreached bool, slaDetails []string) (*storage.RunArchiveRecord, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.manifest.Status = status
	w.manifest.CompletedAt = completedAt
	w.manifest.Error = errStr
	w.manifest.SLABreached = slaBreached
	w.manifest.SLADetails = slaDetails

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

func loadRunArchiveManifest(manifestPath string) (*RunArchiveManifest, error) {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, err
	}
	var manifest RunArchiveManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	return &manifest, nil
}

func archiveManifestArtifactNames(manifest *RunArchiveManifest) map[string]struct{} {
	artifacts := map[string]struct{}{}
	for _, name := range []string{
		manifest.TransactionsFile,
		manifest.TransactionsParquetFile,
		manifest.ResourceSamplesFile,
		manifest.ResourceSamplesParquetFile,
		manifest.IntervalSummariesFile,
		manifest.IntervalSummariesParquetFile,
		"manifest.json",
	} {
		if name == "" {
			continue
		}
		artifacts[name] = struct{}{}
	}
	return artifacts
}

func loadArchiveTransactions(record *storage.RunArchiveRecord, filter transactionEventFilter, page archivePage) ([]TransactionEvent, int, error) {
	manifest, err := loadRunArchiveManifest(record.ManifestPath)
	if err != nil {
		return nil, 0, err
	}
	path := filepath.Join(record.ArchivePath, manifest.TransactionsFile)
	return readJSONLPage(path, page, func(item *TransactionEvent) bool {
		if filter.Action != "" && !strings.EqualFold(item.ActionName, filter.Action) {
			return false
		}
		if filter.SOAPOperation != "" && !strings.EqualFold(strings.TrimSpace(item.Extractions["soap_operation"]), filter.SOAPOperation) {
			return false
		}
		if filter.Group != "" && !strings.EqualFold(item.Group, filter.Group) {
			return false
		}
		if filter.CorrelationID != "" && !strings.EqualFold(item.CorrelationID, filter.CorrelationID) {
			return false
		}
		if filter.TraceID != "" && !strings.EqualFold(item.TraceID, filter.TraceID) {
			return false
		}
		if filter.DependencyType != "" && !strings.EqualFold(item.DependencyType, filter.DependencyType) {
			return false
		}
		if filter.DependencyTarget != "" && !strings.EqualFold(item.DependencyTarget, filter.DependencyTarget) {
			return false
		}
		if filter.Success != nil && item.Success != *filter.Success {
			return false
		}
		return true
	})
}

func loadArchiveResourceSamples(record *storage.RunArchiveRecord, page archivePage) ([]ResourceSample, int, error) {
	manifest, err := loadRunArchiveManifest(record.ManifestPath)
	if err != nil {
		return nil, 0, err
	}
	path := filepath.Join(record.ArchivePath, manifest.ResourceSamplesFile)
	return readJSONLPage[ResourceSample](path, page, nil)
}

func loadArchiveIntervalSummaries(record *storage.RunArchiveRecord, page archivePage) ([]IntervalSummaryRecord, int, error) {
	manifest, err := loadRunArchiveManifest(record.ManifestPath)
	if err != nil {
		return nil, 0, err
	}
	path := filepath.Join(record.ArchivePath, manifest.IntervalSummariesFile)
	return readJSONLPage[IntervalSummaryRecord](path, page, nil)
}

func buildRunArchiveReport(record *storage.RunArchiveRecord) (*RunArchiveReport, error) {
	manifest, err := loadRunArchiveManifest(record.ManifestPath)
	if err != nil {
		return nil, err
	}
	transactions, err := readAllArchiveTransactions(record)
	if err != nil {
		return nil, err
	}
	resources, err := readAllArchiveResources(record)
	if err != nil {
		return nil, err
	}
	summaries, err := readAllArchiveSummaries(record)
	if err != nil {
		return nil, err
	}

	report := &RunArchiveReport{
		Manifest:            manifest,
		ByAction:            make(map[string]RunArchiveActionStats),
		BySOAPOperation:     make(map[string]RunArchiveActionStats),
		ByNetworkProfile:    make(map[string]RunArchiveActionStats),
		BySOAPFaultCode:     make(map[string]int64),
		BySOAPFaultCategory: make(map[string]int64),
		BrowserByEngine:     make(map[string]*BrowserEngineLiveMetrics),
		BrowserByPlatform:   make(map[string]*BrowserPlatformLiveMetrics),
		BrowserByDevice:     make(map[string]*BrowserDeviceLiveMetrics),
	}
	faultTimelineBuckets := make(map[time.Time]*RunArchiveSOAPFaultTimelineBucket)
	payloadTimelineBuckets := make(map[time.Time]*RunArchiveSOAPPayloadTimelineBucket)
	errorPatterns := make(map[string]*RunArchiveErrorPattern)
	errorPatternActions := make(map[string]map[string]struct{})

	var totalDuration float64
	for index, tx := range transactions {
		report.Overview.Transactions++
		totalDuration += tx.DurationMs
		if !tx.Success {
			report.Overview.Errors++
			mergeArchiveErrorPattern(errorPatterns, errorPatternActions, tx)
		}
		if index == 0 || tx.DurationMs < report.Overview.MinResponseMs {
			report.Overview.MinResponseMs = tx.DurationMs
		}
		if tx.DurationMs > report.Overview.MaxResponseMs {
			report.Overview.MaxResponseMs = tx.DurationMs
		}

		stats := report.ByAction[tx.ActionName]
		report.ByAction[tx.ActionName] = mergeRunArchiveActionStats(stats, tx)
		mergeArchiveSOAPFaultTimelineBucket(faultTimelineBuckets, tx)
		mergeArchiveSOAPPayloadTimelineBucket(payloadTimelineBuckets, tx)
		if operation := archiveSOAPOperation(tx); operation != "" {
			report.BySOAPOperation[operation] = mergeRunArchiveActionStats(report.BySOAPOperation[operation], tx)
		}
		if tx.NetworkProfile != "" {
			report.ByNetworkProfile[tx.NetworkProfile] = mergeRunArchiveActionStats(report.ByNetworkProfile[tx.NetworkProfile], tx)
		}
		incrementArchiveCount(report.BySOAPFaultCode, archiveSOAPFaultCode(tx))
		incrementArchiveCount(report.BySOAPFaultCategory, archiveSOAPFaultCategory(tx))
		if tx.ClientMetrics != nil {
			if report.Overview.Browser == nil {
				report.Overview.Browser = &BrowserLiveMetrics{}
			}
			mergeReportBrowserMetrics(report.Overview.Browser, tx.ClientMetrics)
			if engine := tx.ClientMetrics.BrowserEngine; engine != "" {
				engineMetrics, ok := report.BrowserByEngine[engine]
				if !ok {
					engineMetrics = &BrowserEngineLiveMetrics{BrowserEngine: engine}
					report.BrowserByEngine[engine] = engineMetrics
				}
				mergeReportBrowserEngineMetrics(engineMetrics, tx.ClientMetrics)
			}
			if platform := tx.ClientMetrics.DevicePlatform; platform != "" {
				platformMetrics, ok := report.BrowserByPlatform[platform]
				if !ok {
					platformMetrics = &BrowserPlatformLiveMetrics{
						DevicePlatform: platform,
						BrowserEngine:  tx.ClientMetrics.BrowserEngine,
					}
					report.BrowserByPlatform[platform] = platformMetrics
				}
				mergeReportBrowserPlatformMetrics(platformMetrics, tx.ClientMetrics)
			}
			if profile := tx.ClientMetrics.DeviceProfile; profile != "" {
				deviceMetrics, ok := report.BrowserByDevice[profile]
				if !ok {
					deviceMetrics = &BrowserDeviceLiveMetrics{
						DeviceProfile:  profile,
						DevicePlatform: tx.ClientMetrics.DevicePlatform,
						DeviceTier:     tx.ClientMetrics.DeviceTier,
						BrowserEngine:  tx.ClientMetrics.BrowserEngine,
					}
					report.BrowserByDevice[profile] = deviceMetrics
				}
				mergeReportBrowserDeviceMetrics(deviceMetrics, tx.ClientMetrics)
			}
		}
		report.BrowserResources = append(report.BrowserResources, browserResourceTimingsFromTransaction(tx)...)
	}
	if report.Overview.Transactions > 0 {
		report.Overview.AvgResponseMs = totalDuration / float64(report.Overview.Transactions)
		report.Overview.ErrorPct = float64(report.Overview.Errors) / float64(report.Overview.Transactions) * 100
	}
	if len(errorPatterns) > 0 {
		report.ErrorPatterns = make([]RunArchiveErrorPattern, 0, len(errorPatterns))
		for signature, pattern := range errorPatterns {
			if pattern == nil {
				continue
			}
			if actions := errorPatternActions[signature]; len(actions) > 0 {
				pattern.Actions = make([]string, 0, len(actions))
				for action := range actions {
					pattern.Actions = append(pattern.Actions, action)
				}
				sort.Strings(pattern.Actions)
			}
			report.ErrorPatterns = append(report.ErrorPatterns, *pattern)
		}
		sort.Slice(report.ErrorPatterns, func(left, right int) bool {
			if report.ErrorPatterns[left].Count == report.ErrorPatterns[right].Count {
				return report.ErrorPatterns[left].FirstSeen.Before(report.ErrorPatterns[right].FirstSeen)
			}
			return report.ErrorPatterns[left].Count > report.ErrorPatterns[right].Count
		})
	}
	finalizeReportBrowserMetrics(report.Overview.Browser)
	for _, metrics := range report.BrowserByEngine {
		finalizeReportBrowserEngineMetrics(metrics)
	}
	if len(report.BrowserByEngine) == 0 {
		report.BrowserByEngine = nil
	}
	for _, metrics := range report.BrowserByPlatform {
		finalizeReportBrowserPlatformMetrics(metrics)
	}
	if len(report.BrowserByPlatform) == 0 {
		report.BrowserByPlatform = nil
	}
	for _, metrics := range report.BrowserByDevice {
		finalizeReportBrowserDeviceMetrics(metrics)
	}
	if len(report.BrowserByDevice) == 0 {
		report.BrowserByDevice = nil
	}
	report.BrowserResources = finalizeBrowserResourceTimings(report.BrowserResources, 50)
	for action, stats := range report.ByAction {
		report.ByAction[action] = finalizeRunArchiveActionStats(stats)
	}
	for operation, stats := range report.BySOAPOperation {
		report.BySOAPOperation[operation] = finalizeRunArchiveActionStats(stats)
	}
	if len(report.BySOAPOperation) == 0 {
		report.BySOAPOperation = nil
	}
	for profile, stats := range report.ByNetworkProfile {
		report.ByNetworkProfile[profile] = finalizeRunArchiveActionStats(stats)
	}
	if len(report.ByNetworkProfile) == 0 {
		report.ByNetworkProfile = nil
	}
	if len(report.BySOAPFaultCode) == 0 {
		report.BySOAPFaultCode = nil
	}
	if len(report.BySOAPFaultCategory) == 0 {
		report.BySOAPFaultCategory = nil
	}
	if len(faultTimelineBuckets) > 0 {
		report.SOAPFaultTimeline = make([]RunArchiveSOAPFaultTimelineBucket, 0, len(faultTimelineBuckets))
		for _, bucket := range faultTimelineBuckets {
			if bucket == nil {
				continue
			}
			report.SOAPFaultTimeline = append(report.SOAPFaultTimeline, *bucket)
		}
		sort.Slice(report.SOAPFaultTimeline, func(left, right int) bool {
			return report.SOAPFaultTimeline[left].Timestamp.Before(report.SOAPFaultTimeline[right].Timestamp)
		})
	}
	if len(payloadTimelineBuckets) > 0 {
		report.SOAPPayloadTimeline = make([]RunArchiveSOAPPayloadTimelineBucket, 0, len(payloadTimelineBuckets))
		for _, bucket := range payloadTimelineBuckets {
			if bucket == nil {
				continue
			}
			report.SOAPPayloadTimeline = append(report.SOAPPayloadTimeline, finalizeArchiveSOAPPayloadTimelineBucket(*bucket))
		}
		sort.Slice(report.SOAPPayloadTimeline, func(left, right int) bool {
			return report.SOAPPayloadTimeline[left].Timestamp.Before(report.SOAPPayloadTimeline[right].Timestamp)
		})
	}

	report.Overview.ResourceSamples = int64(len(resources))
	for _, sample := range resources {
		if sample.CPUProcessPct > report.ResourcePeaks.CPUProcessPct {
			report.ResourcePeaks.CPUProcessPct = sample.CPUProcessPct
		}
		if sample.MemProcessMB > report.ResourcePeaks.MemProcessMB {
			report.ResourcePeaks.MemProcessMB = sample.MemProcessMB
		}
		if sample.NetSentKbps > report.ResourcePeaks.NetSentKbps {
			report.ResourcePeaks.NetSentKbps = sample.NetSentKbps
		}
		if sample.NetRecvKbps > report.ResourcePeaks.NetRecvKbps {
			report.ResourcePeaks.NetRecvKbps = sample.NetRecvKbps
		}
		if sample.DiskReadKbps > report.ResourcePeaks.DiskReadKbps {
			report.ResourcePeaks.DiskReadKbps = sample.DiskReadKbps
		}
		if sample.DiskWriteKbps > report.ResourcePeaks.DiskWriteKbps {
			report.ResourcePeaks.DiskWriteKbps = sample.DiskWriteKbps
		}
		if sample.ActiveVUsers > report.Overview.PeakActiveVUsers {
			report.Overview.PeakActiveVUsers = sample.ActiveVUsers
		}
	}

	report.Overview.IntervalSummaries = int64(len(summaries))
	if len(summaries) > 0 {
		report.SummaryTimeline = make([]RunArchiveSummaryPoint, 0, len(summaries))
	}
	for _, summary := range summaries {
		if summary.TPS > report.Overview.PeakTPS {
			report.Overview.PeakTPS = summary.TPS
		}
		if summary.TargetTPS > report.Overview.PeakTargetTPS {
			report.Overview.PeakTargetTPS = summary.TargetTPS
		}
		if summary.ThroughputGapPct > report.Overview.MaxThroughputGapPct {
			report.Overview.MaxThroughputGapPct = summary.ThroughputGapPct
		}
		if summary.ThroughputTargetMissed {
			report.Overview.ThroughputMissedIntervals++
		}
		if summary.ActiveVUsers > report.Overview.PeakActiveVUsers {
			report.Overview.PeakActiveVUsers = summary.ActiveVUsers
		}
		report.SummaryTimeline = append(report.SummaryTimeline, RunArchiveSummaryPoint{
			Timestamp:              summary.Timestamp,
			ActiveVUsers:           summary.ActiveVUsers,
			TPS:                    summary.TPS,
			TargetTPS:              summary.TargetTPS,
			ThroughputGapPct:       summary.ThroughputGapPct,
			ThroughputTargetMissed: summary.ThroughputTargetMissed,
			ErrorPct:               summary.ErrorPct,
			P95Ms:                  summary.P95Ms,
			TotalTx:                summary.TotalTx,
			TotalErrors:            summary.TotalErrors,
		})
	}

	return report, nil
}

func buildRunArchiveBrowserDiagnostics(record *storage.RunArchiveRecord) (*BrowserDiagnosticsBundle, error) {
	manifest, err := loadRunArchiveManifest(record.ManifestPath)
	if err != nil {
		return nil, err
	}
	transactions, err := readAllArchiveTransactions(record)
	if err != nil {
		return nil, err
	}
	bundle := &BrowserDiagnosticsBundle{
		SchemaVersion: browserDiagnosticsSchemaVersion,
		GeneratedAt:   time.Now().UTC(),
		Manifest:      manifest,
		Events:        make([]BrowserDiagnosticsEvent, 0),
	}
	for _, tx := range transactions {
		if !archiveTransactionHasBrowserDiagnostics(tx) {
			continue
		}
		event := BrowserDiagnosticsEvent{
			Index:         len(bundle.Events) + 1,
			Timestamp:     tx.Timestamp,
			Group:         tx.Group,
			ActionName:    tx.ActionName,
			DurationMs:    tx.DurationMs,
			StatusCode:    tx.StatusCode,
			Success:       tx.Success,
			ErrorMessage:  tx.ErrorMessage,
			ClientMetrics: tx.ClientMetrics,
		}
		if tx.ClientMetrics != nil {
			bundle.Overview.ClientMetricSamples++
		}
		if !tx.Success {
			bundle.Overview.FailedBrowserTransactions++
		}
		if snapshot := tx.Snapshot; snapshot != nil {
			event.PageURL = snapshot.PageURL
			event.PageTitle = snapshot.PageTitle
			event.ConsoleEntries = cloneProtocolConsoleEntries(snapshot.ConsoleEntries)
			event.NetworkEntries = cloneProtocolNetworkEntries(snapshot.NetworkEntries)
			event.DOMSummary = cloneProtocolDOMSummary(snapshot.DOMSummary)
			event.Snapshot = &BrowserDiagnosticSnapshot{
				RequestMethod:   snapshot.RequestMethod,
				RequestURL:      snapshot.RequestURL,
				RequestHeaders:  cloneStringMap(snapshot.RequestHeaders),
				RequestBody:     snapshot.RequestBody,
				ResponseStatus:  snapshot.ResponseStatus,
				ResponseHeaders: cloneStringMap(snapshot.ResponseHeaders),
				ResponseBody:    snapshot.ResponseBody,
			}
			event.screenshotBase64 = snapshot.ScreenshotBase64
			event.html = snapshot.HTML
		}
		if event.PageURL == "" {
			event.PageURL = tx.PageURL
		}
		if event.PageTitle == "" {
			event.PageTitle = tx.PageTitle
		}
		if len(event.ConsoleEntries) == 0 {
			event.ConsoleEntries = cloneProtocolConsoleEntries(tx.ConsoleEntries)
		}
		if len(event.NetworkEntries) == 0 {
			event.NetworkEntries = cloneProtocolNetworkEntries(tx.NetworkEntries)
		}
		if event.DOMSummary == nil {
			event.DOMSummary = cloneProtocolDOMSummary(tx.DOMSummary)
		}
		if event.screenshotBase64 != "" {
			bundle.Overview.ScreenshotArtifacts++
		}
		if event.html != "" {
			bundle.Overview.HTMLArtifacts++
		}
		if len(event.ConsoleEntries) > 0 {
			bundle.Overview.ConsoleEntries += int64(len(event.ConsoleEntries))
		}
		if len(event.NetworkEntries) > 0 {
			bundle.Overview.NetworkEntries += int64(len(event.NetworkEntries))
		}
		if event.DOMSummary != nil {
			bundle.Overview.DOMSnapshots++
		}
		bundle.Overview.BrowserTransactions++
		bundle.Events = append(bundle.Events, event)
	}
	return bundle, nil
}

func archiveTransactionHasBrowserDiagnostics(tx TransactionEvent) bool {
	if tx.ClientMetrics != nil {
		return true
	}
	if strings.HasPrefix(strings.ToLower(tx.ActionName), "browser_") {
		return true
	}
	if tx.PageURL != "" ||
		tx.PageTitle != "" ||
		len(tx.ConsoleEntries) > 0 ||
		len(tx.NetworkEntries) > 0 ||
		tx.DOMSummary != nil {
		return true
	}
	if tx.Snapshot == nil {
		return false
	}
	return tx.Snapshot.PageURL != "" ||
		tx.Snapshot.PageTitle != "" ||
		tx.Snapshot.HTML != "" ||
		tx.Snapshot.ScreenshotBase64 != "" ||
		len(tx.Snapshot.ConsoleEntries) > 0 ||
		len(tx.Snapshot.NetworkEntries) > 0 ||
		tx.Snapshot.DOMSummary != nil
}

func browserResourceTimingsFromTransaction(tx TransactionEvent) []BrowserResourceTiming {
	entries := tx.NetworkEntries
	if len(entries) == 0 && tx.Snapshot != nil {
		entries = tx.Snapshot.NetworkEntries
	}
	if len(entries) == 0 {
		return nil
	}
	timings := make([]BrowserResourceTiming, 0, len(entries))
	for _, entry := range entries {
		if strings.TrimSpace(entry.URL) == "" {
			continue
		}
		timing := BrowserResourceTiming{
			Timestamp:     tx.Timestamp,
			ActionName:    tx.ActionName,
			PageURL:       tx.PageURL,
			URL:           entry.URL,
			Method:        entry.Method,
			StatusCode:    entry.StatusCode,
			ResourceType:  entry.ResourceType,
			DurationMs:    entry.DurationMs,
			BytesReceived: entry.BytesReceived,
		}
		if timing.PageURL == "" && tx.Snapshot != nil {
			timing.PageURL = tx.Snapshot.PageURL
		}
		if tx.ClientMetrics != nil {
			timing.DeviceProfile = tx.ClientMetrics.DeviceProfile
			timing.DevicePlatform = tx.ClientMetrics.DevicePlatform
			timing.BrowserEngine = tx.ClientMetrics.BrowserEngine
		}
		timings = append(timings, timing)
	}
	return timings
}

func finalizeBrowserResourceTimings(entries []BrowserResourceTiming, limit int) []BrowserResourceTiming {
	if len(entries) == 0 {
		return nil
	}
	sort.SliceStable(entries, func(left, right int) bool {
		if entries[left].DurationMs == entries[right].DurationMs {
			return entries[left].URL < entries[right].URL
		}
		return entries[left].DurationMs > entries[right].DurationMs
	})
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	return entries
}

func traceIDFromCorrelationID(correlationID string) string {
	traceID, _ := traceparentIDs(correlationID)
	return traceID
}

func spanIDFromCorrelationID(correlationID string) string {
	_, spanID := traceparentIDs(correlationID)
	return spanID
}

func traceparentIDs(correlationID string) (string, string) {
	parts := strings.Split(strings.TrimSpace(correlationID), "-")
	if len(parts) != 4 {
		return "", ""
	}
	traceID := strings.ToLower(strings.TrimSpace(parts[1]))
	spanID := strings.ToLower(strings.TrimSpace(parts[2]))
	if len(traceID) != 32 || len(spanID) != 16 || !isLowerHex(traceID) || !isLowerHex(spanID) || isAllZeroHex(traceID) || isAllZeroHex(spanID) {
		return "", ""
	}
	return traceID, spanID
}

func isLowerHex(value string) bool {
	for _, ch := range value {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}

func isAllZeroHex(value string) bool {
	for _, ch := range value {
		if ch != '0' {
			return false
		}
	}
	return true
}

func writeRunArchiveBrowserDiagnosticsZip(bundle *BrowserDiagnosticsBundle, dst io.Writer) error {
	zipWriter := zip.NewWriter(dst)
	defer zipWriter.Close()

	for index := range bundle.Events {
		event := &bundle.Events[index]
		baseName := fmt.Sprintf("%03d-%s", event.Index, sanitizeFilename(event.ActionName))
		if event.screenshotBase64 != "" {
			event.Artifacts.ScreenshotFile = filepath.ToSlash(filepath.Join("screenshots", baseName+".b64"))
			if err := writeZipString(zipWriter, event.Artifacts.ScreenshotFile, event.screenshotBase64); err != nil {
				return err
			}
		}
		if event.html != "" {
			event.Artifacts.HTMLFile = filepath.ToSlash(filepath.Join("html", baseName+".html"))
			if err := writeZipString(zipWriter, event.Artifacts.HTMLFile, event.html); err != nil {
				return err
			}
		}
	}
	indexBytes, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return err
	}
	return writeZipBytes(zipWriter, "browser_diagnostics.json", indexBytes)
}

func writeZipString(zipWriter *zip.Writer, name string, value string) error {
	return writeZipBytes(zipWriter, name, []byte(value))
}

func writeZipBytes(zipWriter *zip.Writer, name string, value []byte) error {
	writer, err := zipWriter.Create(name)
	if err != nil {
		return err
	}
	_, err = writer.Write(value)
	return err
}

func cloneProtocolConsoleEntries(entries []protocol.ConsoleEntry) []protocol.ConsoleEntry {
	if len(entries) == 0 {
		return nil
	}
	cloned := make([]protocol.ConsoleEntry, len(entries))
	copy(cloned, entries)
	return cloned
}

func cloneProtocolNetworkEntries(entries []protocol.NetworkEntry) []protocol.NetworkEntry {
	if len(entries) == 0 {
		return nil
	}
	cloned := make([]protocol.NetworkEntry, len(entries))
	copy(cloned, entries)
	return cloned
}

func cloneProtocolDOMSummary(summary *protocol.DOMSummary) *protocol.DOMSummary {
	if summary == nil {
		return nil
	}
	cloned := *summary
	if len(summary.VisibleText) > 0 {
		cloned.VisibleText = append([]string(nil), summary.VisibleText...)
	}
	return &cloned
}

func normalizeArchiveErrorSignature(message string) string {
	normalized := strings.TrimSpace(strings.ToLower(message))
	if normalized == "" {
		return "request failed"
	}
	normalized = archiveErrorUUIDPattern.ReplaceAllString(normalized, "<id>")
	normalized = archiveErrorNumberPattern.ReplaceAllString(normalized, "#")
	normalized = archiveErrorWhitespacePattern.ReplaceAllString(normalized, " ")
	return strings.TrimSpace(normalized)
}

func archiveErrorPatternSource(tx TransactionEvent) string {
	if message := strings.TrimSpace(tx.ErrorMessage); message != "" {
		return message
	}
	if tx.StatusCode > 0 {
		return fmt.Sprintf("request failed with status %d", tx.StatusCode)
	}
	return "request failed"
}

func mergeArchiveErrorPattern(patterns map[string]*RunArchiveErrorPattern, actions map[string]map[string]struct{}, tx TransactionEvent) {
	source := archiveErrorPatternSource(tx)
	signature := normalizeArchiveErrorSignature(source)
	pattern, ok := patterns[signature]
	if !ok {
		pattern = &RunArchiveErrorPattern{
			Signature:  signature,
			Sample:     source,
			FirstSeen:  tx.Timestamp,
			LatestSeen: tx.Timestamp,
		}
		if tx.Snapshot != nil {
			pattern.Snapshot = tx.Snapshot
		}
		patterns[signature] = pattern
	}
	pattern.Count++
	if pattern.FirstSeen.IsZero() || (!tx.Timestamp.IsZero() && tx.Timestamp.Before(pattern.FirstSeen)) {
		pattern.FirstSeen = tx.Timestamp
	}
	if tx.Timestamp.After(pattern.LatestSeen) {
		pattern.LatestSeen = tx.Timestamp
	}
	if pattern.Snapshot == nil && tx.Snapshot != nil {
		pattern.Snapshot = tx.Snapshot
	}
	if tx.ActionName != "" {
		if actions[signature] == nil {
			actions[signature] = make(map[string]struct{})
		}
		actions[signature][tx.ActionName] = struct{}{}
	}
}

func archiveSOAPOperation(tx TransactionEvent) string {
	if len(tx.Extractions) == 0 {
		return ""
	}
	return strings.TrimSpace(tx.Extractions["soap_operation"])
}

func archiveSOAPFaultCode(tx TransactionEvent) string {
	if tx.ServerTelemetry == nil {
		return ""
	}
	return strings.TrimSpace(tx.ServerTelemetry.SOAPFaultCode)
}

func archiveSOAPFaultCategory(tx TransactionEvent) string {
	if tx.ServerTelemetry == nil {
		return ""
	}
	return strings.TrimSpace(tx.ServerTelemetry.SOAPFaultCategory)
}

func incrementArchiveCount(values map[string]int64, key string) {
	if values == nil || key == "" {
		return
	}
	values[key]++
}

func mergeArchiveSOAPFaultTimelineBucket(values map[time.Time]*RunArchiveSOAPFaultTimelineBucket, tx TransactionEvent) {
	if len(values) == 0 && values == nil {
		return
	}
	bucketTime := archiveSOAPFaultBucketTimestamp(tx.Timestamp)
	if bucketTime.IsZero() {
		return
	}
	faultCode := archiveSOAPFaultCode(tx)
	faultCategory := archiveSOAPFaultCategory(tx)
	if faultCode == "" && faultCategory == "" {
		return
	}
	bucket, ok := values[bucketTime]
	if !ok {
		bucket = &RunArchiveSOAPFaultTimelineBucket{
			Timestamp:  bucketTime,
			ByCode:     make(map[string]int64),
			ByCategory: make(map[string]int64),
		}
		values[bucketTime] = bucket
	}
	bucket.Total++
	incrementArchiveCount(bucket.ByCode, faultCode)
	incrementArchiveCount(bucket.ByCategory, faultCategory)
	if len(bucket.ByCode) == 0 {
		bucket.ByCode = nil
	}
	if len(bucket.ByCategory) == 0 {
		bucket.ByCategory = nil
	}
}

func archiveSOAPFaultBucketTimestamp(value time.Time) time.Time {
	if value.IsZero() {
		return time.Time{}
	}
	return value.Truncate(time.Minute)
}

func mergeArchiveSOAPPayloadTimelineBucket(values map[time.Time]*RunArchiveSOAPPayloadTimelineBucket, tx TransactionEvent) {
	if values == nil {
		return
	}
	bucketTime := archiveSOAPPayloadBucketTimestamp(tx.Timestamp)
	if bucketTime.IsZero() {
		return
	}
	requestPayloadKB, responsePayloadKB, envelopeOverheadKB := archiveSOAPPayloadTelemetry(tx)
	if requestPayloadKB == 0 && responsePayloadKB == 0 && envelopeOverheadKB == 0 {
		return
	}
	bucket, ok := values[bucketTime]
	if !ok {
		bucket = &RunArchiveSOAPPayloadTimelineBucket{Timestamp: bucketTime}
		values[bucketTime] = bucket
	}
	bucket.Samples++
	bucket.AvgRequestPayloadKB += requestPayloadKB
	bucket.AvgResponsePayloadKB += responsePayloadKB
	bucket.AvgEnvelopeOverheadKB += envelopeOverheadKB
	if envelopeOverheadKB > bucket.MaxEnvelopeOverheadKB {
		bucket.MaxEnvelopeOverheadKB = envelopeOverheadKB
	}
}

func archiveSOAPPayloadBucketTimestamp(value time.Time) time.Time {
	if value.IsZero() {
		return time.Time{}
	}
	return value.Truncate(time.Minute)
}

func archiveSOAPPayloadTelemetry(tx TransactionEvent) (float64, float64, float64) {
	if tx.ServerTelemetry == nil {
		return 0, 0, 0
	}
	return tx.ServerTelemetry.RequestPayloadKB, tx.ServerTelemetry.ResponsePayloadKB, tx.ServerTelemetry.EnvelopeOverheadKB
}

func finalizeArchiveSOAPPayloadTimelineBucket(bucket RunArchiveSOAPPayloadTimelineBucket) RunArchiveSOAPPayloadTimelineBucket {
	if bucket.Samples == 0 {
		return bucket
	}
	divisor := float64(bucket.Samples)
	bucket.AvgRequestPayloadKB /= divisor
	bucket.AvgResponsePayloadKB /= divisor
	bucket.AvgEnvelopeOverheadKB /= divisor
	return bucket
}

func mergeRunArchiveActionStats(stats RunArchiveActionStats, tx TransactionEvent) RunArchiveActionStats {
	stats.Transactions++
	stats.AvgResponseMs += tx.DurationMs
	if !tx.Success {
		stats.Errors++
	}
	if stats.Transactions == 1 || tx.DurationMs < stats.MinResponseMs {
		stats.MinResponseMs = tx.DurationMs
	}
	if tx.DurationMs > stats.MaxResponseMs {
		stats.MaxResponseMs = tx.DurationMs
	}
	return stats
}

func finalizeRunArchiveActionStats(stats RunArchiveActionStats) RunArchiveActionStats {
	if stats.Transactions > 0 {
		stats.ErrorPct = float64(stats.Errors) / float64(stats.Transactions) * 100
		stats.AvgResponseMs = stats.AvgResponseMs / float64(stats.Transactions)
	}
	return stats
}

func cloneBrowserLiveMetrics(metrics *BrowserLiveMetrics) *BrowserLiveMetrics {
	if metrics == nil {
		return nil
	}
	clone := *metrics
	return &clone
}

func cloneBrowserByDeviceMetrics(metrics map[string]*BrowserDeviceLiveMetrics) map[string]*BrowserDeviceLiveMetrics {
	if len(metrics) == 0 {
		return nil
	}
	clone := make(map[string]*BrowserDeviceLiveMetrics, len(metrics))
	for name, value := range metrics {
		if value == nil {
			continue
		}
		copied := *value
		clone[name] = &copied
	}
	if len(clone) == 0 {
		return nil
	}
	return clone
}

func mergeReportBrowserMetrics(summary *BrowserLiveMetrics, metrics *protocol.ClientMetrics) {
	if summary == nil || metrics == nil {
		return
	}
	summary.Samples++
	summary.AvgFCPMs += metrics.FCPMs
	summary.AvgLCPMs += metrics.LCPMs
	summary.AvgCLSScore += metrics.CLSScore
	summary.AvgINPMs += metrics.INPMs
	summary.AvgBlockingResources += float64(metrics.BlockingResourceCount)
	summary.AvgLongTaskCount += float64(metrics.LongTaskCount)
	summary.AvgJSHeapUsedMB += metrics.JSHeapUsedMB
	summary.AvgBrowserProcessCPU += metrics.BrowserProcessCPUPct
	summary.AvgBrowserProcessMem += metrics.BrowserProcessMemMB
}

func finalizeReportBrowserMetrics(summary *BrowserLiveMetrics) {
	if summary == nil || summary.Samples == 0 {
		return
	}
	divisor := float64(summary.Samples)
	summary.AvgFCPMs /= divisor
	summary.AvgLCPMs /= divisor
	summary.AvgCLSScore /= divisor
	summary.AvgINPMs /= divisor
	summary.AvgBlockingResources /= divisor
	summary.AvgLongTaskCount /= divisor
	summary.AvgJSHeapUsedMB /= divisor
	summary.AvgBrowserProcessCPU /= divisor
	summary.AvgBrowserProcessMem /= divisor
}

func mergeReportBrowserDeviceMetrics(summary *BrowserDeviceLiveMetrics, metrics *protocol.ClientMetrics) {
	if summary == nil || metrics == nil {
		return
	}
	if summary.BrowserEngine == "" {
		summary.BrowserEngine = metrics.BrowserEngine
	}
	if summary.DevicePlatform == "" {
		summary.DevicePlatform = metrics.DevicePlatform
	}
	summary.Samples++
	summary.AvgFCPMs += metrics.FCPMs
	summary.AvgLCPMs += metrics.LCPMs
	summary.AvgCLSScore += metrics.CLSScore
	summary.AvgINPMs += metrics.INPMs
	summary.AvgBlockingResources += float64(metrics.BlockingResourceCount)
	summary.AvgLongTaskCount += float64(metrics.LongTaskCount)
	summary.AvgJSHeapUsedMB += metrics.JSHeapUsedMB
	summary.AvgBrowserProcessCPU += metrics.BrowserProcessCPUPct
	summary.AvgBrowserProcessMem += metrics.BrowserProcessMemMB
}

func mergeReportBrowserPlatformMetrics(summary *BrowserPlatformLiveMetrics, metrics *protocol.ClientMetrics) {
	if summary == nil || metrics == nil {
		return
	}
	if summary.BrowserEngine == "" {
		summary.BrowserEngine = metrics.BrowserEngine
	} else if metrics.BrowserEngine != "" && summary.BrowserEngine != metrics.BrowserEngine {
		summary.BrowserEngine = "multiple"
	}
	summary.Samples++
	summary.AvgFCPMs += metrics.FCPMs
	summary.AvgLCPMs += metrics.LCPMs
	summary.AvgCLSScore += metrics.CLSScore
	summary.AvgINPMs += metrics.INPMs
	summary.AvgBlockingResources += float64(metrics.BlockingResourceCount)
	summary.AvgLongTaskCount += float64(metrics.LongTaskCount)
	summary.AvgJSHeapUsedMB += metrics.JSHeapUsedMB
	summary.AvgBrowserProcessCPU += metrics.BrowserProcessCPUPct
	summary.AvgBrowserProcessMem += metrics.BrowserProcessMemMB
}

func mergeReportBrowserEngineMetrics(summary *BrowserEngineLiveMetrics, metrics *protocol.ClientMetrics) {
	if summary == nil || metrics == nil {
		return
	}
	summary.Samples++
	summary.AvgFCPMs += metrics.FCPMs
	summary.AvgLCPMs += metrics.LCPMs
	summary.AvgCLSScore += metrics.CLSScore
	summary.AvgINPMs += metrics.INPMs
	summary.AvgBlockingResources += float64(metrics.BlockingResourceCount)
	summary.AvgLongTaskCount += float64(metrics.LongTaskCount)
	summary.AvgJSHeapUsedMB += metrics.JSHeapUsedMB
	summary.AvgBrowserProcessCPU += metrics.BrowserProcessCPUPct
	summary.AvgBrowserProcessMem += metrics.BrowserProcessMemMB
}

func finalizeReportBrowserEngineMetrics(summary *BrowserEngineLiveMetrics) {
	if summary == nil || summary.Samples == 0 {
		return
	}
	divisor := float64(summary.Samples)
	summary.AvgFCPMs /= divisor
	summary.AvgLCPMs /= divisor
	summary.AvgCLSScore /= divisor
	summary.AvgINPMs /= divisor
	summary.AvgBlockingResources /= divisor
	summary.AvgLongTaskCount /= divisor
	summary.AvgJSHeapUsedMB /= divisor
	summary.AvgBrowserProcessCPU /= divisor
	summary.AvgBrowserProcessMem /= divisor
}

func finalizeReportBrowserPlatformMetrics(summary *BrowserPlatformLiveMetrics) {
	if summary == nil || summary.Samples == 0 {
		return
	}
	divisor := float64(summary.Samples)
	summary.AvgFCPMs /= divisor
	summary.AvgLCPMs /= divisor
	summary.AvgCLSScore /= divisor
	summary.AvgINPMs /= divisor
	summary.AvgBlockingResources /= divisor
	summary.AvgLongTaskCount /= divisor
	summary.AvgJSHeapUsedMB /= divisor
	summary.AvgBrowserProcessCPU /= divisor
	summary.AvgBrowserProcessMem /= divisor
}

func finalizeReportBrowserDeviceMetrics(summary *BrowserDeviceLiveMetrics) {
	if summary == nil || summary.Samples == 0 {
		return
	}
	divisor := float64(summary.Samples)
	summary.AvgFCPMs /= divisor
	summary.AvgLCPMs /= divisor
	summary.AvgCLSScore /= divisor
	summary.AvgINPMs /= divisor
	summary.AvgBlockingResources /= divisor
	summary.AvgLongTaskCount /= divisor
	summary.AvgJSHeapUsedMB /= divisor
	summary.AvgBrowserProcessCPU /= divisor
	summary.AvgBrowserProcessMem /= divisor
}

func readAllArchiveTransactions(record *storage.RunArchiveRecord) ([]TransactionEvent, error) {
	return readAllArchiveRecords[TransactionEvent](record, func(manifest *RunArchiveManifest) string {
		return manifest.TransactionsFile
	})
}

func readAllArchiveResources(record *storage.RunArchiveRecord) ([]ResourceSample, error) {
	return readAllArchiveRecords[ResourceSample](record, func(manifest *RunArchiveManifest) string {
		return manifest.ResourceSamplesFile
	})
}

func readAllArchiveSummaries(record *storage.RunArchiveRecord) ([]IntervalSummaryRecord, error) {
	return readAllArchiveRecords[IntervalSummaryRecord](record, func(manifest *RunArchiveManifest) string {
		return manifest.IntervalSummariesFile
	})
}

func readAllArchiveRecords[T any](record *storage.RunArchiveRecord, pathSelector func(*RunArchiveManifest) string) ([]T, error) {
	manifest, err := loadRunArchiveManifest(record.ManifestPath)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(record.ArchivePath, pathSelector(manifest))
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

func readJSONLPage[T any](path string, page archivePage, predicate func(*T) bool) ([]T, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()

	limit := page.Limit
	if limit <= 0 {
		limit = 100
	}
	if page.Offset < 0 {
		page.Offset = 0
	}

	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 2*1024*1024)

	items := make([]T, 0, limit)
	matched := 0
	for scanner.Scan() {
		var item T
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
			return nil, 0, err
		}
		if predicate != nil && !predicate(&item) {
			continue
		}
		if matched >= page.Offset && len(items) < limit {
			items = append(items, item)
		}
		matched++
	}
	if err := scanner.Err(); err != nil {
		return nil, 0, err
	}
	return items, matched, nil
}

func zipDirectory(sourceDir string, dst io.Writer) error {
	zipWriter := zip.NewWriter(dst)
	defer zipWriter.Close()

	return filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		relPath, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		fileWriter, err := zipWriter.Create(filepath.ToSlash(relPath))
		if err != nil {
			return err
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		_, err = io.Copy(fileWriter, file)
		return err
	})
}

func durationMillis(duration time.Duration) float64 {
	return float64(duration.Microseconds()) / 1000.0
}

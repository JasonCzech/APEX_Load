package engine

import (
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type MetricsCollector struct {
	messagingProgress                    engineMessagingProgress
	timingObserver                       func(ResultTiming)
	initializationObserver               func(int)
	ch                                   chan *protocol.Result
	snapshots                            []MetricsSnapshot
	snapshotSizes                        []int
	snapshotBytes                        int
	snapshotsMu                          sync.RWMutex
	targetsMu                            sync.RWMutex
	current                              *liveCounters
	resCollector                         *ResourceCollector
	emitIdleSnapshots                    atomic.Bool
	activeGaugeDirty                     atomic.Bool
	done                                 chan struct{}
	aggregateDone                        chan struct{}
	startOnce                            sync.Once
	stopOnce                             sync.Once
	startedAt                            time.Time
	targetTPS                            float64
	groupTargetTPS                       map[string]float64
	groupActiveFn                        func() map[string]int
	transactionResponseTimeSLAThresholds map[string]float64
	transactionResponseTimeSLACumulative map[string]*TransactionResponseTimeSLACompliance
	groupRunTotals                       sync.Map // map[string]*groupRunCounters; lock-free hot-path attribution
}

// ResultTiming excludes request, response, parameter, and credential values.
// Observations are serialized by the metrics consumer and finish before Stop
// returns. Like other metrics they can be incomplete if the result queue drops.
type ResultTiming struct {
	StartTime           time.Time
	Duration            time.Duration
	Success             bool
	BusinessTransaction bool
}

const throughputGapAlertThresholdPct = 5.0

type liveCounters struct {
	completedIterations atomic.Int64
	totalTransactions   atomic.Int64
	totalErrors         atomic.Int64
	activeVUsers        atomic.Int64
	droppedResults      atomic.Int64
	sheddedArrivals     atomic.Int64
}

type groupRunCounters struct {
	completedIterations atomic.Int64
	totalTransactions   atomic.Int64
	totalErrors         atomic.Int64
}

type MetricsSnapshot struct {
	CompletedIterations    int64
	Timestamp              time.Time
	Window                 time.Duration
	Transactions           int64
	Errors                 int64
	ActiveVUsers           int64
	TPS                    float64
	TargetTPS              float64 `json:"target_tps,omitempty"`
	ThroughputGapPct       float64 `json:"throughput_gap_pct,omitempty"`
	ThroughputTargetMissed bool    `json:"throughput_target_missed,omitempty"`
	AvgDuration            time.Duration
	P50                    time.Duration
	P90                    time.Duration
	P95                    time.Duration
	P99                    time.Duration
	// Network-phase averages (ms) over business transactions, from real VU
	// traffic. Wait is the post-TTFB tail (Duration-TTFB), clamped at 0.
	AvgDNSMs     float64 `json:"avg_dns_ms,omitempty"`
	AvgConnectMs float64 `json:"avg_connect_ms,omitempty"`
	AvgTLSMs     float64 `json:"avg_tls_ms,omitempty"`
	AvgTTFBMs    float64 `json:"avg_ttfb_ms,omitempty"`
	AvgWaitMs    float64 `json:"avg_wait_ms,omitempty"`
	// ErrorsByClass counts failed business transactions by taxonomy class
	// (timeout/connection/http_4xx/http_5xx/assertion/other).
	ErrorsByClass              map[string]int64 `json:"errors_by_class,omitempty"`
	ByAction                   map[string]*ActionMetrics
	ByTransaction              map[string]*ActionMetrics
	ByDependencyTarget         map[string]*ActionMetrics `json:"by_dependency_target,omitempty"`
	BySOAPOperation            map[string]*ActionMetrics `json:"by_soap_operation,omitempty"`
	ByNetworkProfile           map[string]*ActionMetrics `json:"by_network_profile,omitempty"`
	ByDimensions               map[string]*ActionMetrics `json:"by_dimensions,omitempty"`
	ByActionProperty           map[string]*ActionMetrics `json:"by_action_property,omitempty"`
	ByProperty                 map[string]*ActionMetrics `json:"by_property,omitempty"`
	ByRegion                   map[string]*ActionMetrics `json:"by_region,omitempty"`
	ByGroup                    map[string]*GroupMetrics
	Browser                    *BrowserMetricsSummary                 `json:"browser,omitempty"`
	BrowserByDevice            map[string]*BrowserDeviceMetrics       `json:"browser_by_device,omitempty"`
	MessagingProgress          *protocol.MessagingProgressSnapshot    `json:"messaging_progress,omitempty"`
	Telemetry                  *TelemetryMetrics                      `json:"telemetry,omitempty"`
	Resources                  *SystemResources                       `json:"resources,omitempty"`
	TransactionResponseTimeSLA []TransactionResponseTimeSLACompliance `json:"transaction_response_time_sla,omitempty"`
}

// TransactionResponseTimeSLACompliance is a cumulative, successful-sample-only
// live counter for one named business transaction in one group.
type TransactionResponseTimeSLACompliance struct {
	Group                string
	Transaction          string
	ThresholdMs          float64
	TotalCount           int64
	EligibleSuccessCount int64
	ExcludedErrorCount   int64
	MissedCount          int64
}

type BrowserMetricsSummary struct {
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

type BrowserDeviceMetrics struct {
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

type ServerTelemetrySummary struct {
	Messaging                 *protocol.MessagingSummary `json:"messaging,omitempty"`
	ConsumerLagSamples        int64                      `json:"consumer_lag_samples,omitempty"`
	ConsumerOffsetLagSamples  int64                      `json:"consumer_offset_lag_samples,omitempty"`
	FirstEventSamples         int64                      `json:"first_event_samples,omitempty"`
	WebSocket                 *protocol.WebSocketSummary `json:"websocket,omitempty"`
	GRPC                      *protocol.GRPCSummary      `json:"grpc,omitempty"`
	Samples                   int64                      `json:"samples"`
	AvgServerProcessingMs     float64                    `json:"avg_server_processing_ms,omitempty"`
	AvgTransferMs             float64                    `json:"avg_transfer_ms,omitempty"`
	AvgRequestPayloadKB       float64                    `json:"avg_request_payload_kb,omitempty"`
	AvgResponsePayloadKB      float64                    `json:"avg_response_payload_kb,omitempty"`
	RequestPayloadHistogram   map[string]int64           `json:"request_payload_histogram,omitempty"`
	ResponsePayloadHistogram  map[string]int64           `json:"response_payload_histogram,omitempty"`
	AvgFirstEventMs           float64                    `json:"avg_first_event_ms,omitempty"`
	MaxFirstEventMs           float64                    `json:"max_first_event_ms,omitempty"`
	EventCount                int64                      `json:"event_count,omitempty"`
	AvgMessageLatencyMs       float64                    `json:"avg_message_latency_ms,omitempty"`
	MaxMessageLatencyMs       float64                    `json:"max_message_latency_ms,omitempty"`
	MessagesSent              int64                      `json:"messages_sent,omitempty"`
	MessagesReceived          int64                      `json:"messages_received,omitempty"`
	AvgMessagesSentPerSec     float64                    `json:"avg_messages_sent_per_sec,omitempty"`
	AvgMessagesReceivedPerSec float64                    `json:"avg_messages_received_per_sec,omitempty"`
	AvgConsumerLagMs          float64                    `json:"avg_consumer_lag_ms,omitempty"`
	MaxConsumerLagMs          float64                    `json:"max_consumer_lag_ms,omitempty"`
	AvgConsumerOffsetLag      float64                    `json:"avg_consumer_offset_lag,omitempty"`
	MaxConsumerOffsetLag      int64                      `json:"max_consumer_offset_lag,omitempty"`
	SendFailureCount          int64                      `json:"send_failure_count,omitempty"`
	RedeliveryCount           int64                      `json:"redelivery_count,omitempty"`
	DeadLetteredCount         int64                      `json:"dead_lettered_count,omitempty"`
	DuplicateCount            int64                      `json:"duplicate_count,omitempty"`
	OutOfOrderCount           int64                      `json:"out_of_order_count,omitempty"`
	CommitCount               int64                      `json:"commit_count,omitempty"`
	AbortCount                int64                      `json:"abort_count,omitempty"`
	// Derived reliability rates (P4.1): percent of received messages that were
	// redelivered / dead-lettered / duplicate. Computed at finalize from the
	// counts above ÷ MessagesReceived (post-merge, so distributed runs are pooled).
	RedeliveryRatePct float64 `json:"redelivery_rate_pct,omitempty"`
	DeadLetterRatePct float64 `json:"dead_letter_rate_pct,omitempty"`
	DuplicateRatePct  float64 `json:"duplicate_rate_pct,omitempty"`
	// Event Hubs / streaming ingestion aggregates (EH.4).
	TotalThrottledRequests  int64            `json:"total_throttled_requests,omitempty"`
	TotalServerBusyCount    int64            `json:"total_server_busy_count,omitempty"`
	TotalQuotaExceededCount int64            `json:"total_quota_exceeded_count,omitempty"`
	TotalEventTooLargeCount int64            `json:"total_event_too_large_count,omitempty"`
	AvgEndToEndLatencyMs    float64          `json:"avg_end_to_end_latency_ms,omitempty"`
	MaxEndToEndLatencyMs    float64          `json:"max_end_to_end_latency_ms,omitempty"`
	PartitionEvents         map[string]int64 `json:"partition_events,omitempty"`   // produce volume per partition (skew source)
	PartitionSkewPct        float64          `json:"partition_skew_pct,omitempty"` // coefficient of variation of PartitionEvents (× 100)
	RequestSizeClasses      map[string]int64 `json:"request_size_classes,omitempty"`
	ResponseSizeClasses     map[string]int64 `json:"response_size_classes,omitempty"`
	ConnectionReusedCount   int64            `json:"connection_reused_count,omitempty"`
	ConnectionReusedPct     float64          `json:"connection_reused_pct,omitempty"`
}

type TelemetryMetrics struct {
	DroppedResults  int64 `json:"dropped_results,omitempty"`
	SheddedArrivals int64 `json:"shedded_arrivals,omitempty"`
}

type SystemResources struct {
	CPUProcessPct                     float64            `json:"cpu_process_pct"`               // interval CPU used by vuser processes, normalized to allocated/available cores (fleet average in distributed runs)
	CPUProcessPctMax                  float64            `json:"cpu_process_pct_max,omitempty"` // worst-pod process CPU% across the fleet (distributed only)
	CPUFreePct                        float64            `json:"cpu_free_pct"`                  // CPU headroom
	MemProcessMb                      float64            `json:"mem_process_mb"`                // Memory used by vuser processes
	MemFreeMb                         float64            `json:"mem_free_mb"`                   // Total system memory free
	NetSentKbps                       float64            `json:"net_sent_kbps"`                 // Network egress
	NetRecvKbps                       float64            `json:"net_recv_kbps"`                 // Network ingress
	DiskReadKbps                      float64            `json:"disk_read_kbps"`                // Disk Read I/O
	DiskWriteKbps                     float64            `json:"disk_write_kbps"`               // Disk Write I/O
	CPUUserPct                        float64            `json:"cpu_user_pct,omitempty"`
	CPUSystemPct                      float64            `json:"cpu_system_pct,omitempty"`
	CPUIOWaitPct                      float64            `json:"cpu_iowait_pct,omitempty"`
	CPUStealPct                       float64            `json:"cpu_steal_pct,omitempty"`
	Load1                             float64            `json:"load1,omitempty"`
	Load5                             float64            `json:"load5,omitempty"`
	Load15                            float64            `json:"load15,omitempty"`
	ThreadCount                       int32              `json:"thread_count,omitempty"`
	OpenFDCount                       int32              `json:"open_fd_count,omitempty"`
	GoroutineCount                    int32              `json:"goroutine_count,omitempty"`
	HeapAllocMB                       float64            `json:"heap_alloc_mb,omitempty"`
	HeapInUseMB                       float64            `json:"heap_in_use_mb,omitempty"`
	GCCountDelta                      uint32             `json:"gc_count_delta,omitempty"`
	GCPauseMs                         float64            `json:"gc_pause_ms,omitempty"`
	ContainerCPULimitCores            float64            `json:"container_cpu_limit_cores,omitempty"`
	ContainerCPUUsagePct              float64            `json:"container_cpu_usage_pct,omitempty"`
	ContainerCPUThrottledPeriodsDelta uint64             `json:"container_cpu_throttled_periods_delta,omitempty"`
	ContainerCPUThrottledSecondsRate  float64            `json:"container_cpu_throttled_seconds_rate,omitempty"`
	ContainerMemoryWorkingSetMB       float64            `json:"container_memory_working_set_mb,omitempty"`
	ContainerMemoryLimitMB            float64            `json:"container_memory_limit_mb,omitempty"`
	ContainerMemoryUsagePct           float64            `json:"container_memory_usage_pct,omitempty"`
	ContainerMemoryFailCountDelta     uint64             `json:"container_memory_fail_count_delta,omitempty"`
	ContainerOOMKillCountDelta        uint64             `json:"container_oom_kill_count_delta,omitempty"`
	NetDropInPerSec                   float64            `json:"net_drop_in_per_sec,omitempty"`
	NetDropOutPerSec                  float64            `json:"net_drop_out_per_sec,omitempty"`
	NetErrorInPerSec                  float64            `json:"net_error_in_per_sec,omitempty"`
	NetErrorOutPerSec                 float64            `json:"net_error_out_per_sec,omitempty"`
	TCPRetransmitsPerSec              float64            `json:"tcp_retransmits_per_sec,omitempty"`
	TCPEstablished                    int                `json:"tcp_established,omitempty"`
	TCPTimeWait                       int                `json:"tcp_time_wait,omitempty"`
	EphemeralPortUtilizationPct       float64            `json:"ephemeral_port_utilization_pct,omitempty"`
	DiskIOTimePct                     float64            `json:"disk_io_time_pct,omitempty"`
	DiskWeightedIOTimePct             float64            `json:"disk_weighted_io_time_pct,omitempty"`
	DiskInProgress                    uint64             `json:"disk_in_progress,omitempty"`
	External                          map[string]float64 `json:"external_metrics,omitempty"` // Target-side imported metrics

	// ContainerCPUPeriodsDelta is the CFS periods elapsed since the previous
	// sample: the denominator of the share of periods throttled.
	ContainerCPUPeriodsDelta uint64 `json:"container_cpu_periods_delta,omitempty"`
}

type GroupMetrics struct {
	Name                   string
	Transactions           int64
	Errors                 int64
	ActiveVUsers           int64
	ActiveVUsersReported   bool `json:"active_vusers_reported,omitempty"`
	TPS                    float64
	TargetTPS              float64 `json:"target_tps,omitempty"`
	ThroughputGapPct       float64 `json:"throughput_gap_pct,omitempty"`
	ThroughputTargetMissed bool    `json:"throughput_target_missed,omitempty"`
	AvgDuration            time.Duration
	P50                    time.Duration
	P90                    time.Duration
	P95                    time.Duration
	P99                    time.Duration
	DurationDigest         *TDigestSnapshot `json:"-"`
}

type ActionMetrics struct {
	DurationDigest   *TDigestSnapshot `json:"-"`
	Name             string
	DependencyType   string
	DependencyTarget string
	LastErrorMessage string
	LastStatusCode   int
	LastErrorAt      time.Time
	ServerTelemetry  *ServerTelemetrySummary `json:"server_telemetry,omitempty"`
	AvgDNSMs         float64                 `json:"avg_dns_ms,omitempty"`
	AvgConnectMs     float64                 `json:"avg_connect_ms,omitempty"`
	AvgTLSMs         float64                 `json:"avg_tls_ms,omitempty"`
	AvgTTFBMs        float64                 `json:"avg_ttfb_ms,omitempty"`
	CorrelationIDs   []string
	CorrelationCount int64
	Count            int64
	Errors           int64
	AvgDuration      time.Duration
	MinDuration      time.Duration
	MaxDuration      time.Duration
	P50              time.Duration
	P90              time.Duration
	P95              time.Duration
	P99              time.Duration
	BytesSent        int64
	BytesRecv        int64
	// Exemplars are this window's requests whose trace context was sent.
	Exemplars          []TraceExemplar `json:"exemplars,omitempty"`
	exemplarCandidates int64
}

func NewMetricsCollector(bufferSize int) *MetricsCollector {
	if bufferSize <= 0 {
		bufferSize = 10000
	}
	return &MetricsCollector{
		ch:                                   make(chan *protocol.Result, bufferSize),
		current:                              &liveCounters{},
		resCollector:                         NewResourceCollector(),
		done:                                 make(chan struct{}),
		aggregateDone:                        make(chan struct{}),
		groupTargetTPS:                       make(map[string]float64),
		transactionResponseTimeSLAThresholds: make(map[string]float64),
		transactionResponseTimeSLACumulative: make(map[string]*TransactionResponseTimeSLACompliance),
	}
}

func (mc *MetricsCollector) SetTransactionResponseTimeSLAThresholds(thresholds map[string]float64) {
	mc.targetsMu.Lock()
	defer mc.targetsMu.Unlock()
	mc.transactionResponseTimeSLAThresholds = make(map[string]float64, len(thresholds))
	for group, threshold := range thresholds {
		if threshold > 0 && !math.IsNaN(threshold) && !math.IsInf(threshold, 0) {
			mc.transactionResponseTimeSLAThresholds[strings.TrimSpace(group)] = threshold
		}
	}
}

func (mc *MetricsCollector) SetTargetTPS(target float64) {
	mc.targetsMu.Lock()
	defer mc.targetsMu.Unlock()
	mc.targetTPS = target
}

// RecordSheddedArrival increments the count of open-workload arrivals dropped
// because the in-flight worker cap was saturated. Cumulative over the run and
// surfaced via the snapshot telemetry so operators can see when the offered TPS
// exceeds achievable throughput.
func (mc *MetricsCollector) RecordSheddedArrival() {
	mc.RecordSheddedArrivals(1)
}

// RecordSheddedArrivals adds arrivals that could not be dispatched. The bulk
// form is used by the open-workload deadline scheduler when it deliberately
// sheds an excessive catch-up backlog instead of creating an unbounded burst.
func (mc *MetricsCollector) RecordSheddedArrivals(count int64) {
	if mc == nil {
		return
	}
	if count > 0 {
		mc.current.sheddedArrivals.Add(count)
	}
}

func (mc *MetricsCollector) SetGroupTargetTPS(group string, target float64) {
	mc.targetsMu.Lock()
	defer mc.targetsMu.Unlock()
	if mc.groupTargetTPS == nil {
		mc.groupTargetTPS = make(map[string]float64)
	}
	if target <= 0 {
		delete(mc.groupTargetTPS, boundedMetricString(strings.TrimSpace(group), maxMetricLabelBytes))
		return
	}
	group = boundedDimensionKey(group, mc.groupTargetTPS)
	mc.groupTargetTPS[group] = target
}

func (mc *MetricsCollector) Record(r *protocol.Result) {
	metric := compactMetricsResult(r)
	if metric == nil {
		return
	}
	select {
	case mc.ch <- metric:
	default:
		mc.current.droppedResults.Add(1)
		// channel full — drop metric rather than blocking the hot path
	}
}

func (mc *MetricsCollector) SetActiveVUsers(n int64) {
	if mc.current.activeVUsers.Swap(n) != n {
		// A zero active gauge is still live execution evidence when the engine can
		// later scale up again. Remember the transition until the aggregator emits
		// it; unlike emitIdleSnapshots this produces one point per transition rather
		// than an unbounded stream of idle zero windows.
		mc.activeGaugeDirty.Store(true)
	}
}

// SetGroupActiveVUsersProvider registers a callback the collector invokes once
// per snapshot to obtain live per-group active VUser counts. The multi-group
// engine registers this so group-scoped metrics (and the load-impact x-axis when
// group-filtered) reflect the live ramp instead of static configured counts.
func (mc *MetricsCollector) SetGroupActiveVUsersProvider(fn func() map[string]int) {
	mc.targetsMu.Lock()
	mc.groupActiveFn = fn
	mc.targetsMu.Unlock()
}

func (mc *MetricsCollector) Start(interval time.Duration) {
	if interval <= 0 {
		interval = time.Second
	}
	mc.startOnce.Do(func() {
		mc.startedAt = time.Now()
		go mc.aggregate(interval)
	})
}

func (mc *MetricsCollector) EnableIdleSnapshots(enabled bool) {
	mc.emitIdleSnapshots.Store(enabled)
}

func (mc *MetricsCollector) Stop() {
	mc.stopOnce.Do(func() {
		notStarted := false
		mc.startOnce.Do(func() {
			notStarted = true
			close(mc.aggregateDone)
		})
		if notStarted {
			if mc.messagingProgress.sample(true) {
				mc.publishSnapshot(nil, time.Nanosecond)
			}
			return
		}
		close(mc.done)
		<-mc.aggregateDone
	})
}

func (mc *MetricsCollector) LatestSnapshot() (MetricsSnapshot, bool) {
	mc.snapshotsMu.RLock()
	defer mc.snapshotsMu.RUnlock()
	if len(mc.snapshots) == 0 {
		return MetricsSnapshot{}, false
	}
	snapshot := mc.snapshots[len(mc.snapshots)-1]
	snapshot.MessagingProgress = snapshot.MessagingProgress.Clone()
	return snapshot, true
}

func (mc *MetricsCollector) TotalTransactions() int64 {
	return mc.current.totalTransactions.Load()
}

func (mc *MetricsCollector) TotalErrors() int64 {
	return mc.current.totalErrors.Load()
}

// RecordTransaction records an automatic transaction as a synthetic result.
// This is used by the auto-transactions feature to track per-action or per-iteration timings.
func (mc *MetricsCollector) RecordTransaction(name string, duration time.Duration, success bool, group string) *protocol.Result {
	r := &protocol.Result{
		ActionName:          name,
		BusinessTransaction: true,
		Group:               group,
		StartTime:           time.Now().Add(-duration),
		Duration:            duration,
		Success:             success,
	}
	if !success {
		r.ErrorMessage = "transaction failed"
	}
	mc.RecordTransactionResult(r)
	return r
}

func (mc *MetricsCollector) RecordTransactionResult(r *protocol.Result) {
	if r == nil {
		return
	}
	r.BusinessTransaction = true
	if r.ActionName == "" {
		r.ActionName = "transaction"
	}
	if r.StartTime.IsZero() {
		r.StartTime = time.Now().Add(-r.Duration)
	}
	if r.Success {
		mc.current.totalTransactions.Add(1)
	} else {
		mc.current.totalErrors.Add(1)
	}
	group := strings.TrimSpace(r.Group)
	if group != "" {
		value, _ := mc.groupRunTotals.LoadOrStore(group, &groupRunCounters{})
		counters := value.(*groupRunCounters)
		if r.Success {
			counters.totalTransactions.Add(1)
		} else {
			counters.totalErrors.Add(1)
		}
	}
	metric := compactMetricsResult(r)
	select {
	case mc.ch <- metric:
	default:
		mc.current.droppedResults.Add(1)
	}
}

// GroupRunTotals returns cumulative business-transaction counters for a group.
// Unlike MetricsSnapshot.ByGroup, these values cover the complete run and are
// therefore suitable for the terminal MultiGroupRunResult contract.
func (mc *MetricsCollector) GroupRunTotals(group string) (transactions int64, errors int64) {
	if mc == nil {
		return 0, 0
	}
	value, ok := mc.groupRunTotals.Load(strings.TrimSpace(group))
	if !ok {
		return 0, 0
	}
	counters := value.(*groupRunCounters)
	return counters.totalTransactions.Load(), counters.totalErrors.Load()
}

func (mc *MetricsCollector) aggregate(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	defer close(mc.aggregateDone)

	var batch []*protocol.Result
	windowStart := mc.startedAt
	publishBatch := func(windowEnd time.Time, force bool) {
		if len(batch) == 0 && !force {
			return
		}
		window := windowEnd.Sub(windowStart)
		if window <= 0 {
			window = time.Nanosecond
		}
		mc.publishSnapshot(batch, window)
		clear(batch)
		batch = batch[:0]
		windowStart = windowEnd
	}

	for {
		select {
		case <-mc.done:
			for {
				select {
				case r := <-mc.ch:
					batch = append(batch, r)
					if len(batch) >= maxMetricsBatchSize {
						publishBatch(time.Now(), false)
					}
				default:
					progressChanged := mc.messagingProgress.sample(true)
					publishBatch(time.Now(), mc.activeGaugeDirty.Swap(false) || progressChanged)
					return
				}
			}
		case <-ticker.C:
			windowEnd := time.Now()
			// drain channel
			for len(batch) < maxMetricsBatchSize {
				select {
				case r := <-mc.ch:
					batch = append(batch, r)
				default:
					goto process
				}
			}
		process:
			progressChanged := mc.messagingProgress.sample(false)
			activeGaugeChanged := mc.activeGaugeDirty.Swap(false)
			// An empty window still gets a zero-valued, correctly-timestamped
			// snapshot while the run is live (active VUsers) or during a
			// configured baseline (emitIdleSnapshots) — so a stall, a long
			// think-time/pacing window, or an idle baseline renders as a flat
			// zero line in the runtime graphs instead of a gap. A transition to
			// zero also emits exactly once: scale-to-zero can be nonterminal, and
			// consumers must not keep projecting the previous nonzero gauge. Only
			// a truly idle, unchanged window is skipped, avoiding leading/trailing
			// zero noise before ramp-up starts or after ramp-down completes.
			if len(batch) == 0 && !activeGaugeChanged && !progressChanged && !mc.emitIdleSnapshots.Load() && mc.current.activeVUsers.Load() == 0 {
				windowStart = windowEnd
				continue
			}
			window := windowEnd.Sub(windowStart)
			if window <= 0 {
				window = interval
			}
			mc.publishSnapshot(batch, window)
			windowStart = windowEnd
			clear(batch)
			batch = batch[:0]
		}
	}
}

func (mc *MetricsCollector) publishSnapshot(batch []*protocol.Result, window time.Duration) {
	if mc.timingObserver != nil {
		for _, result := range batch {
			mc.timingObserver(ResultTiming{StartTime: result.StartTime, Duration: result.Duration,
				Success: result.Success, BusinessTransaction: result.BusinessTransaction})
		}
	}
	snap := mc.computeSnapshot(batch, window)
	snap.MessagingProgress = mc.messagingProgress.snapshot()
	if mc.resCollector != nil {
		snap.Resources = mc.resCollector.Collect()
		if snap.Resources != nil {
			snap.Resources.External = boundExternalMetrics(snap.Resources.External)
		}
	}
	snapshotSize := estimateMetricsSnapshotBytes(snap)
	mc.snapshotsMu.Lock()
	mc.snapshots = append(mc.snapshots, snap)
	mc.snapshotSizes = append(mc.snapshotSizes, snapshotSize)
	mc.snapshotBytes += snapshotSize
	for (len(mc.snapshots) > 3600 || mc.snapshotBytes > maxMetricsSnapshotBytes) && len(mc.snapshots) > 1 {
		mc.snapshotBytes -= mc.snapshotSizes[0]
		mc.snapshots[0] = MetricsSnapshot{}
		mc.snapshotSizes[0] = 0
		mc.snapshots = mc.snapshots[1:]
		mc.snapshotSizes = mc.snapshotSizes[1:]
	}
	mc.snapshotsMu.Unlock()
}

func (mc *MetricsCollector) computeSnapshot(results []*protocol.Result, window time.Duration) MetricsSnapshot {
	snap := MetricsSnapshot{
		CompletedIterations: mc.current.completedIterations.Load(),
		Timestamp:           time.Now(),
		Window:              window,
		ActiveVUsers:        mc.current.activeVUsers.Load(),
		ByAction:            make(map[string]*ActionMetrics),
		ByTransaction:       make(map[string]*ActionMetrics),
		ByDependencyTarget:  make(map[string]*ActionMetrics),
		BySOAPOperation:     make(map[string]*ActionMetrics),
		ByNetworkProfile:    make(map[string]*ActionMetrics),
		ByProperty:          make(map[string]*ActionMetrics),
		ByDimensions:        make(map[string]*ActionMetrics),
		ByActionProperty:    make(map[string]*ActionMetrics),
		ByRegion:            make(map[string]*ActionMetrics),
		ByGroup:             make(map[string]*GroupMetrics),
		ErrorsByClass:       make(map[string]int64),
		BrowserByDevice:     make(map[string]*BrowserDeviceMetrics),
		Telemetry: &TelemetryMetrics{
			DroppedResults:  mc.current.droppedResults.Load(),
			SheddedArrivals: mc.current.sheddedArrivals.Load(),
		},
	}
	mc.targetsMu.RLock()
	snap.TargetTPS = mc.targetTPS
	groupTargets := make(map[string]float64, len(mc.groupTargetTPS))
	for name, target := range mc.groupTargetTPS {
		groupTargets[name] = target
	}
	groupActiveFn := mc.groupActiveFn
	slaThresholds := make(map[string]float64, len(mc.transactionResponseTimeSLAThresholds))
	for group, threshold := range mc.transactionResponseTimeSLAThresholds {
		slaThresholds[group] = threshold
	}
	mc.targetsMu.RUnlock()

	var totalDuration time.Duration
	var totalDNSMs, totalConnectMs, totalTLSMs, totalTTFBMs, totalWaitMs float64
	td := NewTDigest(200)
	transactionDigests := make(map[string]*TDigest)
	transactionDurationSum := make(map[string]time.Duration)
	actionDigests := make(map[string]*TDigest)
	actionDurationSum := make(map[string]time.Duration)
	dependencyDigests := make(map[string]*TDigest)
	dependencyDurationSum := make(map[string]time.Duration)
	soapDigests := make(map[string]*TDigest)
	soapDurationSum := make(map[string]time.Duration)
	propertyDigests := make(map[string]*TDigest)
	propertyDurationSum := make(map[string]time.Duration)
	dimensionDigests := make(map[string]*TDigest)
	dimensionDurationSum := make(map[string]time.Duration)
	jointDigests := make(map[string]*TDigest)
	jointDurationSum := make(map[string]time.Duration)
	networkProfileDigests := make(map[string]*TDigest)
	networkProfileDurationSum := make(map[string]time.Duration)
	regionDigests := make(map[string]*TDigest)
	regionDurationSum := make(map[string]time.Duration)
	groupDigests := make(map[string]*TDigest)
	groupDurationSum := make(map[string]time.Duration)

	for _, r := range results {
		if r.BusinessTransaction {
			group := strings.TrimSpace(r.Group)
			transaction := strings.TrimSpace(r.ActionName)
			if threshold, configured := slaThresholds[group]; configured && transaction != "" {
				key := group + "\x00" + transaction
				compliance := mc.transactionResponseTimeSLACumulative[key]
				if compliance == nil && len(mc.transactionResponseTimeSLACumulative) < maxMetricDimensionEntries {
					compliance = &TransactionResponseTimeSLACompliance{Group: group, Transaction: transaction, ThresholdMs: threshold}
					mc.transactionResponseTimeSLACumulative[key] = compliance
				}
				if compliance != nil {
					compliance.TotalCount++
					if r.Success {
						compliance.EligibleSuccessCount++
						if float64(r.Duration)/float64(time.Millisecond) > threshold {
							compliance.MissedCount++
						}
					} else {
						compliance.ExcludedErrorCount++
					}
				}
			}
			if r.Success {
				snap.Transactions++
			} else {
				snap.Errors++
				if class := ClassifyError(r.StatusCode, r.ErrorMessage, false); class != "" {
					snap.ErrorsByClass[class]++
				}
			}
			totalDuration += r.Duration
			td.Add(float64(r.Duration))
			totalDNSMs += durationToMilliseconds(r.DNSTime)
			totalConnectMs += durationToMilliseconds(r.ConnectTime)
			totalTLSMs += durationToMilliseconds(r.TLSTime)
			totalTTFBMs += durationToMilliseconds(r.TTFB)
			if waitMs := durationToMilliseconds(r.Duration) - durationToMilliseconds(r.TTFB); waitMs > 0 {
				totalWaitMs += waitMs
			}

			transactionName := boundedDimensionKey(r.ActionName, snap.ByTransaction)
			tm, ok := snap.ByTransaction[transactionName]
			if !ok {
				tm = &ActionMetrics{
					Name:        transactionName,
					MinDuration: r.Duration,
					MaxDuration: r.Duration,
				}
				snap.ByTransaction[transactionName] = tm
			}
			mergeActionResult(tm, r)
			recordTraceExemplar(tm, r)
			recordDurationSample(transactionDigests, transactionDurationSum, transactionName, r.Duration)

			if region := strings.TrimSpace(r.Region); region != "" {
				region = boundedDimensionKey(region, snap.ByRegion)
				rm, ok := snap.ByRegion[region]
				if !ok {
					rm = &ActionMetrics{Name: region, MinDuration: r.Duration, MaxDuration: r.Duration}
					snap.ByRegion[region] = rm
				}
				mergeActionResult(rm, r)
				recordDurationSample(regionDigests, regionDurationSum, region, r.Duration)
			}

			if r.Group != "" {
				groupName := boundedDimensionKey(r.Group, snap.ByGroup)
				gm, ok := snap.ByGroup[groupName]
				if !ok {
					gm = &GroupMetrics{Name: groupName}
					snap.ByGroup[groupName] = gm
				}
				if r.Success {
					gm.Transactions++
				} else {
					gm.Errors++
				}
				groupDurationSum[groupName] += r.Duration
				if _, ok := groupDigests[groupName]; !ok {
					groupDigests[groupName] = NewTDigest(200)
				}
				groupDigests[groupName].Add(float64(r.Duration))
			}
		}

		if !r.BusinessTransaction {
			actionName := boundedDimensionKey(metricActionDimension(r), snap.ByAction)
			am, ok := snap.ByAction[actionName]
			if !ok {
				am = &ActionMetrics{
					Name:        actionName,
					MinDuration: r.Duration,
					MaxDuration: r.Duration,
				}
				snap.ByAction[actionName] = am
			}
			mergeActionResult(am, r)
			recordTraceExemplar(am, r)
			recordDurationSample(actionDigests, actionDurationSum, actionName, r.Duration)

			if dependencyTarget := strings.TrimSpace(r.DependencyTarget); dependencyTarget != "" {
				dependencyTarget = boundedDimensionKey(dependencyTarget, snap.ByDependencyTarget)
				dm, ok := snap.ByDependencyTarget[dependencyTarget]
				if !ok {
					dm = &ActionMetrics{
						Name:        dependencyTarget,
						MinDuration: r.Duration,
						MaxDuration: r.Duration,
					}
					snap.ByDependencyTarget[dependencyTarget] = dm
				}
				mergeActionResult(dm, r)
				recordDurationSample(dependencyDigests, dependencyDurationSum, dependencyTarget, r.Duration)
			}

			if operation := soapOperationFromResult(r); operation != "" {
				operation = boundedDimensionKey(operation, snap.BySOAPOperation)
				om, ok := snap.BySOAPOperation[operation]
				if !ok {
					om = &ActionMetrics{
						Name:        operation,
						MinDuration: r.Duration,
						MaxDuration: r.Duration,
					}
					snap.BySOAPOperation[operation] = om
				}
				mergeActionResult(om, r)
				recordDurationSample(soapDigests, soapDurationSum, operation, r.Duration)
			}

			if profile := strings.TrimSpace(r.NetworkProfile); profile != "" {
				profile = boundedDimensionKey(profile, snap.ByNetworkProfile)
				pm, ok := snap.ByNetworkProfile[profile]
				if !ok {
					pm = &ActionMetrics{
						Name:        profile,
						MinDuration: r.Duration,
						MaxDuration: r.Duration,
					}
					snap.ByNetworkProfile[profile] = pm
				}
				mergeActionResult(pm, r)
				recordDurationSample(networkProfileDigests, networkProfileDurationSum, profile, r.Duration)
			}
			dimension := boundedJointPropertyKey(ActionDimensionKey(actionName, r.NetworkProfile, r.Hierarchy, r.Phase), snap.ByDimensions)
			dm := snap.ByDimensions[dimension]
			if dm == nil {
				dm = &ActionMetrics{Name: dimension, MinDuration: r.Duration, MaxDuration: r.Duration}
				snap.ByDimensions[dimension] = dm
			}
			mergeActionResult(dm, r)
			recordDurationSample(dimensionDigests, dimensionDurationSum, dimension, r.Duration)
			for property, value := range r.PropertyDimensions {
				profile := property + "=" + value
				profile = boundedDimensionKey(profile, snap.ByProperty)
				pm, ok := snap.ByProperty[profile]
				if !ok {
					pm = &ActionMetrics{
						Name:        profile,
						MinDuration: r.Duration,
						MaxDuration: r.Duration,
					}
					snap.ByProperty[profile] = pm
				}
				mergeActionResult(pm, r)
				recordDurationSample(propertyDigests, propertyDurationSum, profile, r.Duration)
				joint := boundedJointPropertyKey(ActionPropertyKey(actionName, property, value), snap.ByActionProperty)
				jm := snap.ByActionProperty[joint]
				if jm == nil {
					jm = &ActionMetrics{Name: joint, MinDuration: r.Duration, MaxDuration: r.Duration}
					snap.ByActionProperty[joint] = jm
				}
				mergeActionResult(jm, r)
				recordDurationSample(jointDigests, jointDurationSum, joint, r.Duration)
			}

			if r.ClientMetrics != nil {
				if snap.Browser == nil {
					snap.Browser = &BrowserMetricsSummary{}
				}
				mergeBrowserMetricsSummary(snap.Browser, r.ClientMetrics)
				if profile := r.ClientMetrics.DeviceProfile; profile != "" {
					profile = boundedDimensionKey(profile, snap.BrowserByDevice)
					metrics, ok := snap.BrowserByDevice[profile]
					if !ok {
						metrics = &BrowserDeviceMetrics{
							DeviceProfile:  profile,
							DevicePlatform: r.ClientMetrics.DevicePlatform,
							DeviceTier:     r.ClientMetrics.DeviceTier,
							BrowserEngine:  r.ClientMetrics.BrowserEngine,
						}
						snap.BrowserByDevice[profile] = metrics
					}
					mergeBrowserDeviceMetrics(metrics, r.ClientMetrics)
				}
			}
		}
	}
	if len(slaThresholds) > 0 {
		keys := make([]string, 0, len(mc.transactionResponseTimeSLACumulative))
		for key := range mc.transactionResponseTimeSLACumulative {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		snap.TransactionResponseTimeSLA = make([]TransactionResponseTimeSLACompliance, 0, len(keys)+len(slaThresholds))
		observedGroups := make(map[string]bool, len(slaThresholds))
		for _, key := range keys {
			row := *mc.transactionResponseTimeSLACumulative[key]
			observedGroups[row.Group] = true
			snap.TransactionResponseTimeSLA = append(snap.TransactionResponseTimeSLA, row)
		}
		for group, threshold := range slaThresholds {
			if !observedGroups[group] {
				snap.TransactionResponseTimeSLA = append(snap.TransactionResponseTimeSLA, TransactionResponseTimeSLACompliance{Group: group, ThresholdMs: threshold})
			}
		}
		sort.Slice(snap.TransactionResponseTimeSLA, func(i, j int) bool {
			if snap.TransactionResponseTimeSLA[i].Group == snap.TransactionResponseTimeSLA[j].Group {
				return snap.TransactionResponseTimeSLA[i].Transaction < snap.TransactionResponseTimeSLA[j].Transaction
			}
			return snap.TransactionResponseTimeSLA[i].Group < snap.TransactionResponseTimeSLA[j].Group
		})
	}

	transactionAttempts := snap.Transactions + snap.Errors
	if transactionAttempts > 0 {
		attempts := float64(transactionAttempts)
		snap.AvgDuration = totalDuration / time.Duration(transactionAttempts)
		snap.TPS = float64(snap.Transactions) / window.Seconds()
		snap.AvgDNSMs = totalDNSMs / attempts
		snap.AvgConnectMs = totalConnectMs / attempts
		snap.AvgTLSMs = totalTLSMs / attempts
		snap.AvgTTFBMs = totalTTFBMs / attempts
		snap.AvgWaitMs = totalWaitMs / attempts

		snap.P50 = time.Duration(td.Quantile(0.50))
		snap.P90 = time.Duration(td.Quantile(0.90))
		snap.P95 = time.Duration(td.Quantile(0.95))
		snap.P99 = time.Duration(td.Quantile(0.99))
	}
	applyThroughputGap(&snap.TPS, snap.TargetTPS, &snap.ThroughputGapPct, &snap.ThroughputTargetMissed)

	// Attach live per-group active VUser counts before finalizing group metrics
	// so resultless groups still receive their configured target and throughput
	// gap. Keep explicit zeroes: zero is reported live state, not missing data.
	if groupActiveFn != nil {
		activeGroups := groupActiveFn()
		var totalActive int64
		for _, active := range activeGroups {
			totalActive += int64(active)
		}
		for _, name := range boundedtext.SmallestStringKeys(activeGroups, maxMetricDimensionEntries) {
			active := activeGroups[name]
			name = boundedDimensionKey(name, snap.ByGroup)
			gm, ok := snap.ByGroup[name]
			if !ok {
				gm = &GroupMetrics{Name: name}
				snap.ByGroup[name] = gm
			}
			gm.ActiveVUsers += int64(active)
			gm.ActiveVUsersReported = true
		}
		// The provider samples every group under the engine lock. Derive the
		// global gauge from that same sample so a ramp transition cannot publish
		// internally inconsistent global and group counts.
		snap.ActiveVUsers = totalActive
	}
	for name, gm := range snap.ByGroup {
		gm.TargetTPS = groupTargets[name]
		groupAttempts := gm.Transactions + gm.Errors
		if groupAttempts > 0 {
			gd := groupDigests[name]
			gm.AvgDuration = groupDurationSum[name] / time.Duration(groupAttempts)
			gm.TPS = float64(gm.Transactions) / window.Seconds()
			gm.P50 = time.Duration(gd.Quantile(0.50))
			gm.P90 = time.Duration(gd.Quantile(0.90))
			gm.P95 = time.Duration(gd.Quantile(0.95))
			gm.P99 = time.Duration(gd.Quantile(0.99))
			digestSnapshot := gd.Snapshot()
			gm.DurationDigest = &digestSnapshot
		}
		applyThroughputGap(&gm.TPS, gm.TargetTPS, &gm.ThroughputGapPct, &gm.ThroughputTargetMissed)
	}
	for name, am := range snap.ByAction {
		finalizeActionMetrics(am, actionDurationSum[name], actionDigests[name])
	}
	for name, am := range snap.ByTransaction {
		finalizeActionMetrics(am, transactionDurationSum[name], transactionDigests[name])
	}
	if len(snap.ByTransaction) == 0 {
		snap.ByTransaction = nil
	}
	for name, am := range snap.ByDependencyTarget {
		finalizeActionMetrics(am, dependencyDurationSum[name], dependencyDigests[name])
	}
	for name, am := range snap.BySOAPOperation {
		finalizeActionMetrics(am, soapDurationSum[name], soapDigests[name])
	}
	for name, am := range snap.ByNetworkProfile {
		finalizeActionMetrics(am, networkProfileDurationSum[name], networkProfileDigests[name])
	}
	if len(snap.ByNetworkProfile) == 0 {
		snap.ByNetworkProfile = nil
	}
	for name, am := range snap.ByDimensions {
		finalizeActionMetrics(am, dimensionDurationSum[name], dimensionDigests[name])
	}
	for name, am := range snap.ByActionProperty {
		finalizeActionMetrics(am, jointDurationSum[name], jointDigests[name])
	}
	for name, am := range snap.ByProperty {
		finalizeActionMetrics(am, propertyDurationSum[name], propertyDigests[name])
	}
	if len(snap.ByProperty) == 0 {
		snap.ByProperty = nil
	}
	for name, am := range snap.ByRegion {
		finalizeActionMetrics(am, regionDurationSum[name], regionDigests[name])
	}
	if len(snap.ByRegion) == 0 {
		snap.ByRegion = nil
	}
	finalizeBrowserMetricsSummary(snap.Browser)
	for _, metrics := range snap.BrowserByDevice {
		finalizeBrowserDeviceMetrics(metrics)
	}
	if len(snap.BrowserByDevice) == 0 {
		snap.BrowserByDevice = nil
	}

	return snap
}

func applyThroughputGap(actual *float64, target float64, gapPct *float64, missed *bool) {
	if actual == nil || gapPct == nil || missed == nil || target <= 0 {
		return
	}
	if *actual >= target {
		return
	}
	*gapPct = ((target - *actual) / target) * 100
	*missed = *gapPct >= throughputGapAlertThresholdPct
}

func soapOperationFromResult(result *protocol.Result) string {
	if result == nil || len(result.Extractions) == 0 {
		return ""
	}
	return strings.TrimSpace(result.Extractions["soap_operation"])
}

func mergeActionResult(metrics *ActionMetrics, result *protocol.Result) {
	if metrics == nil || result == nil {
		return
	}
	metrics.Count++
	if !result.Success {
		metrics.Errors++
		metrics.LastErrorMessage = strings.TrimSpace(result.ErrorMessage)
		metrics.LastStatusCode = result.StatusCode
		metrics.LastErrorAt = result.StartTime
		if metrics.LastErrorAt.IsZero() {
			metrics.LastErrorAt = time.Now()
		}
	}
	mergeActionMetadata(metrics, result)
	metrics.AvgDNSMs += durationToMilliseconds(result.DNSTime)
	metrics.AvgConnectMs += durationToMilliseconds(result.ConnectTime)
	metrics.AvgTLSMs += durationToMilliseconds(result.TLSTime)
	metrics.AvgTTFBMs += durationToMilliseconds(result.TTFB)
	if result.ServerTelemetry != nil {
		if metrics.ServerTelemetry == nil {
			metrics.ServerTelemetry = &ServerTelemetrySummary{}
		}
		mergeServerTelemetrySummary(metrics.ServerTelemetry, result.ServerTelemetry, result.DependencyTarget, result.DependencyType, result.Group)
	}
	metrics.BytesSent += result.BytesSent
	metrics.BytesRecv += result.BytesReceived
	if result.Duration < metrics.MinDuration {
		metrics.MinDuration = result.Duration
	}
	if result.Duration > metrics.MaxDuration {
		metrics.MaxDuration = result.Duration
	}
}

func recordDurationSample(digests map[string]*TDigest, totals map[string]time.Duration, key string, duration time.Duration) {
	if key == "" {
		return
	}
	totals[key] += duration
	if _, ok := digests[key]; !ok {
		digests[key] = NewTDigest(200)
	}
	digests[key].Add(float64(duration))
}

func finalizeActionMetrics(metrics *ActionMetrics, totalDuration time.Duration, td *TDigest) {
	if metrics == nil || metrics.Count == 0 {
		return
	}
	if totalDuration > 0 {
		metrics.AvgDuration = totalDuration / time.Duration(metrics.Count)
	}
	if td != nil {
		metrics.P50 = time.Duration(td.Quantile(0.50))
		metrics.P90 = time.Duration(td.Quantile(0.90))
		metrics.P95 = time.Duration(td.Quantile(0.95))
		metrics.P99 = time.Duration(td.Quantile(0.99))
		snapshot := td.Snapshot()
		metrics.DurationDigest = &snapshot
	}
	divisor := float64(metrics.Count)
	metrics.AvgDNSMs /= divisor
	metrics.AvgConnectMs /= divisor
	metrics.AvgTLSMs /= divisor
	metrics.AvgTTFBMs /= divisor
	finalizeServerTelemetrySummary(metrics.ServerTelemetry)
}

const maxCorrelationSamplesPerAction = 3

func mergeActionMetadata(metrics *ActionMetrics, result *protocol.Result) {
	if metrics == nil || result == nil {
		return
	}
	mergeStableActionMetadata(&metrics.DependencyType, result.DependencyType)
	mergeStableActionMetadata(&metrics.DependencyTarget, result.DependencyTarget)
	if result.CorrelationID == "" {
		return
	}
	metrics.CorrelationCount++
	for _, existing := range metrics.CorrelationIDs {
		if existing == result.CorrelationID {
			return
		}
	}
	if len(metrics.CorrelationIDs) < maxCorrelationSamplesPerAction {
		metrics.CorrelationIDs = append(metrics.CorrelationIDs, result.CorrelationID)
	}
}

func mergeStableActionMetadata(current *string, next string) {
	if current == nil || next == "" {
		return
	}
	if *current == "" {
		*current = next
		return
	}
	if *current != next {
		*current = "multiple"
	}
}

func mergeBrowserMetricsSummary(summary *BrowserMetricsSummary, metrics *protocol.ClientMetrics) {
	if summary == nil || metrics == nil {
		return
	}
	summary.Samples++
	summary.AvgFCPMs += metrics.FCPMs
	summary.AvgLCPMs += metrics.LCPMs
	summary.AvgCLSScore += metrics.CLSScore
	summary.AvgINPMs += metrics.INPMs
	summary.AvgTTFBMs += metrics.TTFBMs
	summary.AvgBlockingResources += float64(metrics.BlockingResourceCount)
	summary.AvgLongTaskCount += float64(metrics.LongTaskCount)
	summary.AvgJSHeapUsedMB += metrics.JSHeapUsedMB
	summary.AvgBrowserProcessCPU += metrics.BrowserProcessCPUPct
	summary.AvgBrowserProcessMem += metrics.BrowserProcessMemMB
}

func mergeServerTelemetrySummary(summary *ServerTelemetrySummary, telemetry *protocol.ServerTelemetry, sources ...string) {
	if summary == nil || telemetry == nil {
		return
	}
	source := ""
	if len(sources) > 0 {
		source = sources[0]
	}
	if len(sources) > 1 {
		protocol.MergeMessagingTelemetry(&summary.Messaging, telemetry, source, sources[1:]...)
	} else {
		protocol.MergeMessagingTelemetry(&summary.Messaging, telemetry, source)
	}
	protocol.MergeGRPCDiagnostic(&summary.GRPC, telemetry.GRPC)
	protocol.MergeWebSocketDiagnostic(&summary.WebSocket, telemetry.WebSocket)
	// Reliability counters are run totals and accumulate for every record,
	// including control-only ops (commit/rollback) that carry no measured sample.
	summary.SendFailureCount += int64(telemetry.SendFailureCount)
	summary.RedeliveryCount += int64(telemetry.RedeliveryCount)
	summary.DeadLetteredCount += int64(telemetry.DeadLetteredCount)
	summary.DuplicateCount += int64(telemetry.DuplicateCount)
	summary.OutOfOrderCount += int64(telemetry.OutOfOrderCount)
	summary.CommitCount += int64(telemetry.CommitCount)
	summary.AbortCount += int64(telemetry.AbortCount)
	// Streaming-ingestion counters are run totals too (EH.4).
	summary.TotalThrottledRequests += int64(telemetry.ThrottledRequests)
	summary.TotalServerBusyCount += int64(telemetry.ServerBusyCount)
	summary.TotalQuotaExceededCount += int64(telemetry.QuotaExceededCount)
	summary.TotalEventTooLargeCount += int64(telemetry.EventTooLargeCount)
	// Control-only ops must not contribute to the averaging divisor or they would
	// dilute the average payload/latency/throughput.
	if !telemetry.HasMeasuredSample() {
		return
	}
	summary.Samples++
	summary.AvgServerProcessingMs += telemetry.ServerProcessingMs
	summary.AvgTransferMs += telemetry.TransferMs
	summary.AvgRequestPayloadKB += telemetry.RequestPayloadKB
	summary.AvgResponsePayloadKB += telemetry.ResponsePayloadKB
	incrementTelemetryHistogramCount(&summary.RequestPayloadHistogram, telemetry.RequestPayloadKB)
	incrementTelemetryHistogramCount(&summary.ResponsePayloadHistogram, telemetry.ResponsePayloadKB)
	if (telemetry.GRPC == nil || telemetry.GRPC.FirstMessageMs != nil) && telemetry.WebSocket == nil {
		summary.FirstEventSamples++
		summary.AvgFirstEventMs += telemetry.FirstEventMs
	}
	if telemetry.FirstEventMs > summary.MaxFirstEventMs {
		summary.MaxFirstEventMs = telemetry.FirstEventMs
	}
	summary.EventCount += int64(telemetry.EventCount)
	summary.AvgMessageLatencyMs += telemetry.MessageLatencyMs
	if telemetry.MessageLatencyMs > summary.MaxMessageLatencyMs {
		summary.MaxMessageLatencyMs = telemetry.MessageLatencyMs
	}
	summary.MessagesSent += int64(telemetry.MessagesSent)
	summary.MessagesReceived += int64(telemetry.MessagesReceived)
	summary.AvgMessagesSentPerSec += telemetry.MessagesSentPerSec
	summary.AvgMessagesReceivedPerSec += telemetry.MessagesReceivedPerSec
	if telemetry.ConsumerLagObserved || telemetry.ConsumerLagMs > 0 {
		summary.ConsumerLagSamples++
		summary.AvgConsumerLagMs += telemetry.ConsumerLagMs
	}
	if telemetry.ConsumerLagMs > summary.MaxConsumerLagMs {
		summary.MaxConsumerLagMs = telemetry.ConsumerLagMs
	}
	if telemetry.ConsumerOffsetLagObserved || telemetry.ConsumerOffsetLag > 0 {
		summary.ConsumerOffsetLagSamples++
		summary.AvgConsumerOffsetLag += float64(telemetry.ConsumerOffsetLag)
	}
	if telemetry.ConsumerOffsetLag > summary.MaxConsumerOffsetLag {
		summary.MaxConsumerOffsetLag = telemetry.ConsumerOffsetLag
	}
	summary.AvgEndToEndLatencyMs += telemetry.EndToEndLatencyMs
	if telemetry.EndToEndLatencyMs > summary.MaxEndToEndLatencyMs {
		summary.MaxEndToEndLatencyMs = telemetry.EndToEndLatencyMs
	}
	// Bucket produce volume per partition (skew source). Producer-only: a consume
	// probe leaves MessagesSent == 0 and is excluded so it can't pollute skew.
	if telemetry.PartitionID != "" && telemetry.MessagesSent > 0 {
		if summary.PartitionEvents == nil {
			summary.PartitionEvents = make(map[string]int64)
		}
		partitionID := boundedDimensionKey(telemetry.PartitionID, summary.PartitionEvents)
		summary.PartitionEvents[partitionID] += int64(telemetry.MessagesSent)
	}
	incrementTelemetryClassCount(&summary.RequestSizeClasses, telemetry.RequestSizeClass)
	incrementTelemetryClassCount(&summary.ResponseSizeClasses, telemetry.ResponseSizeClass)
	if telemetry.ConnectionReused {
		summary.ConnectionReusedCount++
	}
}

func finalizeServerTelemetrySummary(summary *ServerTelemetrySummary) {
	if summary == nil || summary.Samples == 0 {
		return
	}
	divisor := float64(summary.Samples)
	summary.AvgServerProcessingMs /= divisor
	summary.AvgTransferMs /= divisor
	summary.AvgRequestPayloadKB /= divisor
	summary.AvgResponsePayloadKB /= divisor
	if summary.FirstEventSamples > 0 {
		summary.AvgFirstEventMs /= float64(summary.FirstEventSamples)
	}
	summary.AvgMessageLatencyMs /= divisor
	summary.AvgMessagesSentPerSec /= divisor
	summary.AvgMessagesReceivedPerSec /= divisor
	if summary.ConsumerLagSamples > 0 {
		summary.AvgConsumerLagMs /= float64(summary.ConsumerLagSamples)
	}
	if summary.ConsumerOffsetLagSamples > 0 {
		summary.AvgConsumerOffsetLag /= float64(summary.ConsumerOffsetLagSamples)
	}
	summary.AvgEndToEndLatencyMs /= divisor
	summary.PartitionSkewPct = ComputePartitionSkewPct(summary.PartitionEvents)
	// Reliability rates are derived from pooled counts ÷ messages received (not
	// averaged per-generator), so they stay correct under the distributed merge.
	summary.RedeliveryRatePct = MessagingRatePct(summary.RedeliveryCount, summary.MessagesReceived)
	summary.DeadLetterRatePct = MessagingRatePct(summary.DeadLetteredCount, summary.MessagesReceived)
	summary.DuplicateRatePct = MessagingRatePct(summary.DuplicateCount, summary.MessagesReceived)
	summary.ConnectionReusedPct = float64(summary.ConnectionReusedCount) / divisor * 100
	if len(summary.PartitionEvents) == 0 {
		summary.PartitionEvents = nil
	}
	if len(summary.RequestSizeClasses) == 0 {
		summary.RequestSizeClasses = nil
	}
	if len(summary.ResponseSizeClasses) == 0 {
		summary.ResponseSizeClasses = nil
	}
	if len(summary.RequestPayloadHistogram) == 0 {
		summary.RequestPayloadHistogram = nil
	}
	if len(summary.ResponsePayloadHistogram) == 0 {
		summary.ResponsePayloadHistogram = nil
	}
}

// MessagingRatePct returns count/received × 100, guarding divide-by-zero. Used
// for derived reliability rates (redelivery/dead-letter/duplicate). Exported so
// the controller's distributed merge recomputes rates from pooled counts rather
// than averaging per-generator rates.
func MessagingRatePct(count, received int64) float64 {
	if received <= 0 || count <= 0 {
		return 0
	}
	return float64(count) / float64(received) * 100
}

// ComputePartitionSkewPct returns the coefficient of variation (stddev/mean ×
// 100) of per-partition produce volume — a unit-free measure of how unevenly
// events are spread across partitions. It is 0 for fewer than two partitions (no
// skew possible) and 0 when the mean is zero. Exported so the controller's
// distributed merge recomputes skew from the pooled map (not by averaging
// per-generator skews).
func ComputePartitionSkewPct(events map[string]int64) float64 {
	n := len(events)
	if n < 2 {
		return 0
	}
	var sum float64
	for _, v := range events {
		sum += float64(v)
	}
	mean := sum / float64(n)
	if mean == 0 {
		return 0
	}
	var variance float64
	for _, v := range events {
		d := float64(v) - mean
		variance += d * d
	}
	variance /= float64(n)
	return math.Sqrt(variance) / mean * 100
}

func incrementTelemetryClassCount(values *map[string]int64, key string) {
	if values == nil || key == "" {
		return
	}
	if *values == nil {
		*values = make(map[string]int64)
	}
	key = boundedDimensionKey(key, *values)
	(*values)[key]++
}

func incrementTelemetryHistogramCount(values *map[string]int64, payloadKB float64) {
	if values == nil {
		return
	}
	key := payloadHistogramBucket(payloadKB)
	if key == "" {
		return
	}
	if *values == nil {
		*values = make(map[string]int64)
	}
	(*values)[key]++
}

func payloadHistogramBucket(payloadKB float64) string {
	switch {
	case payloadKB <= 0:
		return "0 KB"
	case payloadKB < 1:
		return "0-1 KB"
	case payloadKB < 4:
		return "1-4 KB"
	case payloadKB < 16:
		return "4-16 KB"
	case payloadKB < 64:
		return "16-64 KB"
	default:
		return "64+ KB"
	}
}

func durationToMilliseconds(duration time.Duration) float64 {
	if duration <= 0 {
		return 0
	}
	return float64(duration.Microseconds()) / 1000.0
}

func finalizeBrowserMetricsSummary(summary *BrowserMetricsSummary) {
	if summary == nil || summary.Samples == 0 {
		return
	}
	divisor := float64(summary.Samples)
	summary.AvgFCPMs /= divisor
	summary.AvgLCPMs /= divisor
	summary.AvgCLSScore /= divisor
	summary.AvgINPMs /= divisor
	summary.AvgTTFBMs /= divisor
	summary.AvgBlockingResources /= divisor
	summary.AvgLongTaskCount /= divisor
	summary.AvgJSHeapUsedMB /= divisor
	summary.AvgBrowserProcessCPU /= divisor
	summary.AvgBrowserProcessMem /= divisor
}

func mergeBrowserDeviceMetrics(summary *BrowserDeviceMetrics, metrics *protocol.ClientMetrics) {
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
	summary.AvgTTFBMs += metrics.TTFBMs
	summary.AvgBlockingResources += float64(metrics.BlockingResourceCount)
	summary.AvgLongTaskCount += float64(metrics.LongTaskCount)
	summary.AvgJSHeapUsedMB += metrics.JSHeapUsedMB
	summary.AvgBrowserProcessCPU += metrics.BrowserProcessCPUPct
	summary.AvgBrowserProcessMem += metrics.BrowserProcessMemMB
}

func finalizeBrowserDeviceMetrics(summary *BrowserDeviceMetrics) {
	if summary == nil || summary.Samples == 0 {
		return
	}
	divisor := float64(summary.Samples)
	summary.AvgFCPMs /= divisor
	summary.AvgLCPMs /= divisor
	summary.AvgCLSScore /= divisor
	summary.AvgINPMs /= divisor
	summary.AvgTTFBMs /= divisor
	summary.AvgBlockingResources /= divisor
	summary.AvgLongTaskCount /= divisor
	summary.AvgJSHeapUsedMB /= divisor
	summary.AvgBrowserProcessCPU /= divisor
	summary.AvgBrowserProcessMem /= divisor
}

func (mc *MetricsCollector) CompletedIterations() int64 { return mc.current.completedIterations.Load() }

func (mc *MetricsCollector) RecordCompletedIteration(group string) {
	mc.current.completedIterations.Add(1)
	if group != "" {
		value, _ := mc.groupRunTotals.LoadOrStore(group, &groupRunCounters{})
		value.(*groupRunCounters).completedIterations.Add(1)
	}
}
func (mc *MetricsCollector) GroupCompletedIterations(group string) int64 {
	if value, ok := mc.groupRunTotals.Load(group); ok {
		return value.(*groupRunCounters).completedIterations.Load()
	}
	return 0
}

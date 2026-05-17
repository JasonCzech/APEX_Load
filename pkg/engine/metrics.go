package engine

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jasonczech/stormrunner/pkg/protocol"
)

type MetricsCollector struct {
	ch                chan *protocol.Result
	snapshots         []MetricsSnapshot
	snapshotsMu       sync.RWMutex
	targetsMu         sync.RWMutex
	current           *liveCounters
	resCollector      *ResourceCollector
	emitIdleSnapshots bool
	done              chan struct{}
	targetTPS         float64
	groupTargetTPS    map[string]float64
}

const throughputGapAlertThresholdPct = 5.0

type liveCounters struct {
	totalTransactions atomic.Int64
	totalErrors       atomic.Int64
	activeVUsers      atomic.Int64
	droppedResults    atomic.Int64
}

type MetricsSnapshot struct {
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
	ByAction               map[string]*ActionMetrics
	ByDependencyTarget     map[string]*ActionMetrics `json:"by_dependency_target,omitempty"`
	BySOAPOperation        map[string]*ActionMetrics `json:"by_soap_operation,omitempty"`
	ByGroup                map[string]*GroupMetrics
	Browser                *BrowserMetricsSummary           `json:"browser,omitempty"`
	BrowserByDevice        map[string]*BrowserDeviceMetrics `json:"browser_by_device,omitempty"`
	Telemetry              *TelemetryMetrics                `json:"telemetry,omitempty"`
	Resources              *SystemResources                 `json:"resources,omitempty"`
}

type BrowserMetricsSummary struct {
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
	AvgBlockingResources float64 `json:"avg_blocking_resources,omitempty"`
	AvgLongTaskCount     float64 `json:"avg_long_task_count,omitempty"`
	AvgJSHeapUsedMB      float64 `json:"avg_js_heap_used_mb,omitempty"`
	AvgBrowserProcessCPU float64 `json:"avg_browser_process_cpu_pct,omitempty"`
	AvgBrowserProcessMem float64 `json:"avg_browser_process_mem_mb,omitempty"`
}

type ServerTelemetrySummary struct {
	Samples                   int64            `json:"samples"`
	AvgServerProcessingMs     float64          `json:"avg_server_processing_ms,omitempty"`
	AvgTransferMs             float64          `json:"avg_transfer_ms,omitempty"`
	AvgRequestPayloadKB       float64          `json:"avg_request_payload_kb,omitempty"`
	AvgResponsePayloadKB      float64          `json:"avg_response_payload_kb,omitempty"`
	RequestPayloadHistogram   map[string]int64 `json:"request_payload_histogram,omitempty"`
	ResponsePayloadHistogram  map[string]int64 `json:"response_payload_histogram,omitempty"`
	AvgMessageLatencyMs       float64          `json:"avg_message_latency_ms,omitempty"`
	MaxMessageLatencyMs       float64          `json:"max_message_latency_ms,omitempty"`
	MessagesSent              int64            `json:"messages_sent,omitempty"`
	MessagesReceived          int64            `json:"messages_received,omitempty"`
	AvgMessagesSentPerSec     float64          `json:"avg_messages_sent_per_sec,omitempty"`
	AvgMessagesReceivedPerSec float64          `json:"avg_messages_received_per_sec,omitempty"`
	AvgConsumerLagMs          float64          `json:"avg_consumer_lag_ms,omitempty"`
	MaxConsumerLagMs          float64          `json:"max_consumer_lag_ms,omitempty"`
	RequestSizeClasses        map[string]int64 `json:"request_size_classes,omitempty"`
	ResponseSizeClasses       map[string]int64 `json:"response_size_classes,omitempty"`
	ConnectionReusedCount     int64            `json:"connection_reused_count,omitempty"`
	ConnectionReusedPct       float64          `json:"connection_reused_pct,omitempty"`
}

type TelemetryMetrics struct {
	DroppedResults int64 `json:"dropped_results,omitempty"`
}

type SystemResources struct {
	CPUProcessPct float64            `json:"cpu_process_pct"`            // CPU used by vuser processes
	CPUFreePct    float64            `json:"cpu_free_pct"`               // CPU headroom
	MemProcessMb  float64            `json:"mem_process_mb"`             // Memory used by vuser processes
	MemFreeMb     float64            `json:"mem_free_mb"`                // Total system memory free
	NetSentKbps   float64            `json:"net_sent_kbps"`              // Network egress
	NetRecvKbps   float64            `json:"net_recv_kbps"`              // Network ingress
	DiskReadKbps  float64            `json:"disk_read_kbps"`             // Disk Read I/O
	DiskWriteKbps float64            `json:"disk_write_kbps"`            // Disk Write I/O
	External      map[string]float64 `json:"external_metrics,omitempty"` // Target-side imported metrics
}

type GroupMetrics struct {
	Name                   string
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
	P99                    time.Duration
}

type ActionMetrics struct {
	Name             string
	DependencyType   string
	DependencyTarget string
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
}

func NewMetricsCollector(bufferSize int) *MetricsCollector {
	if bufferSize <= 0 {
		bufferSize = 10000
	}
	return &MetricsCollector{
		ch:             make(chan *protocol.Result, bufferSize),
		current:        &liveCounters{},
		resCollector:   NewResourceCollector(),
		done:           make(chan struct{}),
		groupTargetTPS: make(map[string]float64),
	}
}

func (mc *MetricsCollector) SetTargetTPS(target float64) {
	mc.targetsMu.Lock()
	defer mc.targetsMu.Unlock()
	mc.targetTPS = target
}

func (mc *MetricsCollector) SetGroupTargetTPS(group string, target float64) {
	mc.targetsMu.Lock()
	defer mc.targetsMu.Unlock()
	if mc.groupTargetTPS == nil {
		mc.groupTargetTPS = make(map[string]float64)
	}
	if target <= 0 {
		delete(mc.groupTargetTPS, group)
		return
	}
	mc.groupTargetTPS[group] = target
}

func (mc *MetricsCollector) Record(r *protocol.Result) {
	mc.current.totalTransactions.Add(1)
	if !r.Success {
		mc.current.totalErrors.Add(1)
	}
	select {
	case mc.ch <- r:
	default:
		mc.current.droppedResults.Add(1)
		// channel full — drop metric rather than blocking the hot path
	}
}

func (mc *MetricsCollector) SetActiveVUsers(n int64) {
	mc.current.activeVUsers.Store(n)
}

func (mc *MetricsCollector) Start(interval time.Duration) {
	go mc.aggregate(interval)
}

func (mc *MetricsCollector) EnableIdleSnapshots(enabled bool) {
	mc.emitIdleSnapshots = enabled
}

func (mc *MetricsCollector) Stop() {
	close(mc.done)
}

func (mc *MetricsCollector) LatestSnapshot() (MetricsSnapshot, bool) {
	mc.snapshotsMu.RLock()
	defer mc.snapshotsMu.RUnlock()
	if len(mc.snapshots) == 0 {
		return MetricsSnapshot{}, false
	}
	return mc.snapshots[len(mc.snapshots)-1], true
}

func (mc *MetricsCollector) TotalTransactions() int64 {
	return mc.current.totalTransactions.Load()
}

func (mc *MetricsCollector) TotalErrors() int64 {
	return mc.current.totalErrors.Load()
}

// RecordTransaction records an automatic transaction as a synthetic result.
// This is used by the auto-transactions feature to track per-action or per-iteration timings.
func (mc *MetricsCollector) RecordTransaction(name string, duration time.Duration, success bool, group string) {
	r := &protocol.Result{
		ActionName: "tx:" + name,
		Group:      group,
		StartTime:  time.Now().Add(-duration),
		Duration:   duration,
		Success:    success,
	}
	if !success {
		r.ErrorMessage = "transaction failed"
	}
	// Don't count transactions in the main counters — they are synthetic
	select {
	case mc.ch <- r:
	default:
		mc.current.droppedResults.Add(1)
	}
}

func (mc *MetricsCollector) aggregate(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var batch []*protocol.Result

	for {
		select {
		case <-mc.done:
			return
		case <-ticker.C:
			// drain channel
			for {
				select {
				case r := <-mc.ch:
					batch = append(batch, r)
				default:
					goto process
				}
			}
		process:
			if len(batch) == 0 && (!mc.emitIdleSnapshots || mc.current.activeVUsers.Load() > 0) {
				continue
			}
			snap := mc.computeSnapshot(batch, interval)
			if mc.resCollector != nil {
				snap.Resources = &SystemResources{}
			}
			mc.snapshotsMu.Lock()
			mc.snapshots = append(mc.snapshots, snap)
			// keep last 3600 snapshots (1 hour at 1s interval)
			if len(mc.snapshots) > 3600 {
				mc.snapshots = mc.snapshots[len(mc.snapshots)-3600:]
			}
			snapshotIndex := len(mc.snapshots) - 1
			mc.snapshotsMu.Unlock()
			if mc.resCollector != nil {
				resources := mc.resCollector.Collect()
				if resources != nil {
					mc.snapshotsMu.Lock()
					if snapshotIndex >= 0 && snapshotIndex < len(mc.snapshots) {
						mc.snapshots[snapshotIndex].Resources = resources
					}
					mc.snapshotsMu.Unlock()
				}
			}
			batch = batch[:0]
		}
	}
}

func (mc *MetricsCollector) computeSnapshot(results []*protocol.Result, window time.Duration) MetricsSnapshot {
	snap := MetricsSnapshot{
		Timestamp:          time.Now(),
		Window:             window,
		ActiveVUsers:       mc.current.activeVUsers.Load(),
		ByAction:           make(map[string]*ActionMetrics),
		ByDependencyTarget: make(map[string]*ActionMetrics),
		BySOAPOperation:    make(map[string]*ActionMetrics),
		ByGroup:            make(map[string]*GroupMetrics),
		BrowserByDevice:    make(map[string]*BrowserDeviceMetrics),
		Telemetry: &TelemetryMetrics{
			DroppedResults: mc.current.droppedResults.Load(),
		},
	}
	mc.targetsMu.RLock()
	snap.TargetTPS = mc.targetTPS
	groupTargets := make(map[string]float64, len(mc.groupTargetTPS))
	for name, target := range mc.groupTargetTPS {
		groupTargets[name] = target
	}
	mc.targetsMu.RUnlock()

	var totalDuration time.Duration
	td := NewTDigest(200)
	actionDigests := make(map[string]*TDigest)
	actionDurationSum := make(map[string]time.Duration)
	dependencyDigests := make(map[string]*TDigest)
	dependencyDurationSum := make(map[string]time.Duration)
	soapDigests := make(map[string]*TDigest)
	soapDurationSum := make(map[string]time.Duration)
	groupDigests := make(map[string]*TDigest)
	groupDurationSum := make(map[string]time.Duration)

	for _, r := range results {
		snap.Transactions++
		if !r.Success {
			snap.Errors++
		}
		totalDuration += r.Duration
		td.Add(float64(r.Duration))

		am, ok := snap.ByAction[r.ActionName]
		if !ok {
			am = &ActionMetrics{
				Name:        r.ActionName,
				MinDuration: r.Duration,
				MaxDuration: r.Duration,
			}
			snap.ByAction[r.ActionName] = am
		}
		mergeActionResult(am, r)
		recordDurationSample(actionDigests, actionDurationSum, r.ActionName, r.Duration)

		if dependencyTarget := strings.TrimSpace(r.DependencyTarget); dependencyTarget != "" {
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

		if r.Group != "" {
			gm, ok := snap.ByGroup[r.Group]
			if !ok {
				gm = &GroupMetrics{Name: r.Group}
				snap.ByGroup[r.Group] = gm
			}
			gm.Transactions++
			if !r.Success {
				gm.Errors++
			}
			groupDurationSum[r.Group] += r.Duration
			if _, ok := groupDigests[r.Group]; !ok {
				groupDigests[r.Group] = NewTDigest(200)
			}
			groupDigests[r.Group].Add(float64(r.Duration))
		}
		if r.ClientMetrics != nil {
			if snap.Browser == nil {
				snap.Browser = &BrowserMetricsSummary{}
			}
			mergeBrowserMetricsSummary(snap.Browser, r.ClientMetrics)
			if profile := r.ClientMetrics.DeviceProfile; profile != "" {
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

	if snap.Transactions > 0 {
		snap.AvgDuration = totalDuration / time.Duration(snap.Transactions)
		snap.TPS = float64(snap.Transactions) / window.Seconds()

		snap.P50 = time.Duration(td.Quantile(0.50))
		snap.P90 = time.Duration(td.Quantile(0.90))
		snap.P95 = time.Duration(td.Quantile(0.95))
		snap.P99 = time.Duration(td.Quantile(0.99))
	}
	applyThroughputGap(&snap.TPS, snap.TargetTPS, &snap.ThroughputGapPct, &snap.ThroughputTargetMissed)

	for name, gm := range snap.ByGroup {
		gm.TargetTPS = groupTargets[name]
		if gm.Transactions > 0 {
			gd := groupDigests[name]
			gm.AvgDuration = groupDurationSum[name] / time.Duration(gm.Transactions)
			gm.TPS = float64(gm.Transactions) / window.Seconds()
			gm.P50 = time.Duration(gd.Quantile(0.50))
			gm.P90 = time.Duration(gd.Quantile(0.90))
			gm.P99 = time.Duration(gd.Quantile(0.99))
		}
		applyThroughputGap(&gm.TPS, gm.TargetTPS, &gm.ThroughputGapPct, &gm.ThroughputTargetMissed)
	}
	for name, am := range snap.ByAction {
		finalizeActionMetrics(am, actionDurationSum[name], actionDigests[name])
	}
	for name, am := range snap.ByDependencyTarget {
		finalizeActionMetrics(am, dependencyDurationSum[name], dependencyDigests[name])
	}
	for name, am := range snap.BySOAPOperation {
		finalizeActionMetrics(am, soapDurationSum[name], soapDigests[name])
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
		mergeServerTelemetrySummary(metrics.ServerTelemetry, result.ServerTelemetry)
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
	summary.AvgBlockingResources += float64(metrics.BlockingResourceCount)
	summary.AvgLongTaskCount += float64(metrics.LongTaskCount)
	summary.AvgJSHeapUsedMB += metrics.JSHeapUsedMB
	summary.AvgBrowserProcessCPU += metrics.BrowserProcessCPUPct
	summary.AvgBrowserProcessMem += metrics.BrowserProcessMemMB
}

func mergeServerTelemetrySummary(summary *ServerTelemetrySummary, telemetry *protocol.ServerTelemetry) {
	if summary == nil || telemetry == nil {
		return
	}
	summary.Samples++
	summary.AvgServerProcessingMs += telemetry.ServerProcessingMs
	summary.AvgTransferMs += telemetry.TransferMs
	summary.AvgRequestPayloadKB += telemetry.RequestPayloadKB
	summary.AvgResponsePayloadKB += telemetry.ResponsePayloadKB
	incrementTelemetryHistogramCount(&summary.RequestPayloadHistogram, telemetry.RequestPayloadKB)
	incrementTelemetryHistogramCount(&summary.ResponsePayloadHistogram, telemetry.ResponsePayloadKB)
	summary.AvgMessageLatencyMs += telemetry.MessageLatencyMs
	if telemetry.MessageLatencyMs > summary.MaxMessageLatencyMs {
		summary.MaxMessageLatencyMs = telemetry.MessageLatencyMs
	}
	summary.MessagesSent += int64(telemetry.MessagesSent)
	summary.MessagesReceived += int64(telemetry.MessagesReceived)
	summary.AvgMessagesSentPerSec += telemetry.MessagesSentPerSec
	summary.AvgMessagesReceivedPerSec += telemetry.MessagesReceivedPerSec
	summary.AvgConsumerLagMs += telemetry.ConsumerLagMs
	if telemetry.ConsumerLagMs > summary.MaxConsumerLagMs {
		summary.MaxConsumerLagMs = telemetry.ConsumerLagMs
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
	summary.AvgMessageLatencyMs /= divisor
	summary.AvgMessagesSentPerSec /= divisor
	summary.AvgMessagesReceivedPerSec /= divisor
	summary.AvgConsumerLagMs /= divisor
	summary.ConnectionReusedPct = float64(summary.ConnectionReusedCount) / divisor * 100
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

func incrementTelemetryClassCount(values *map[string]int64, key string) {
	if values == nil || key == "" {
		return
	}
	if *values == nil {
		*values = make(map[string]int64)
	}
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
		return ""
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
	summary.AvgBlockingResources /= divisor
	summary.AvgLongTaskCount /= divisor
	summary.AvgJSHeapUsedMB /= divisor
	summary.AvgBrowserProcessCPU /= divisor
	summary.AvgBrowserProcessMem /= divisor
}

package engine

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/jasonczech/stormrunner/pkg/protocol"
)

type MetricsCollector struct {
	ch                chan *protocol.Result
	snapshots         []MetricsSnapshot
	snapshotsMu       sync.RWMutex
	current           *liveCounters
	resCollector      *ResourceCollector
	emitIdleSnapshots bool
	done              chan struct{}
}

type liveCounters struct {
	totalTransactions atomic.Int64
	totalErrors       atomic.Int64
	activeVUsers      atomic.Int64
	droppedResults    atomic.Int64
}

type MetricsSnapshot struct {
	Timestamp    time.Time
	Window       time.Duration
	Transactions int64
	Errors       int64
	ActiveVUsers int64
	TPS          float64
	AvgDuration  time.Duration
	P50          time.Duration
	P90          time.Duration
	P95          time.Duration
	P99          time.Duration
	ByAction     map[string]*ActionMetrics
	ByGroup      map[string]*GroupMetrics
	Browser      *BrowserMetricsSummary               `json:"browser,omitempty"`
	BrowserByDevice map[string]*BrowserDeviceMetrics `json:"browser_by_device,omitempty"`
	Telemetry    *TelemetryMetrics `json:"telemetry,omitempty"`
	Resources    *SystemResources `json:"resources,omitempty"`
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
}

type TelemetryMetrics struct {
	DroppedResults int64 `json:"dropped_results,omitempty"`
}

type SystemResources struct {
	CPUProcessPct float64 `json:"cpu_process_pct"` // CPU used by vuser processes
	CPUFreePct    float64 `json:"cpu_free_pct"`    // CPU headroom
	MemProcessMb  float64 `json:"mem_process_mb"`  // Memory used by vuser processes
	MemFreeMb     float64 `json:"mem_free_mb"`     // Total system memory free
	NetSentKbps   float64 `json:"net_sent_kbps"`   // Network egress
	NetRecvKbps   float64 `json:"net_recv_kbps"`   // Network ingress
	DiskReadKbps  float64 `json:"disk_read_kbps"`  // Disk Read I/O
	DiskWriteKbps float64 `json:"disk_write_kbps"` // Disk Write I/O
}

type GroupMetrics struct {
	Name         string
	Transactions int64
	Errors       int64
	ActiveVUsers int64
	TPS          float64
	AvgDuration  time.Duration
	P50          time.Duration
	P90          time.Duration
	P99          time.Duration
}

type ActionMetrics struct {
	Name             string
	DependencyType   string
	DependencyTarget string
	CorrelationIDs   []string
	CorrelationCount int64
	Count            int64
	Errors           int64
	AvgDuration      time.Duration
	MinDuration      time.Duration
	MaxDuration      time.Duration
	P50              time.Duration
	P90              time.Duration
	P99              time.Duration
	BytesSent        int64
	BytesRecv        int64
}

func NewMetricsCollector(bufferSize int) *MetricsCollector {
	if bufferSize <= 0 {
		bufferSize = 10000
	}
	return &MetricsCollector{
		ch:           make(chan *protocol.Result, bufferSize),
		current:      &liveCounters{},
		resCollector: NewResourceCollector(),
		done:         make(chan struct{}),
	}
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
				snap.Resources = mc.resCollector.Collect()
			}
			mc.snapshotsMu.Lock()
			mc.snapshots = append(mc.snapshots, snap)
			// keep last 3600 snapshots (1 hour at 1s interval)
			if len(mc.snapshots) > 3600 {
				mc.snapshots = mc.snapshots[len(mc.snapshots)-3600:]
			}
			mc.snapshotsMu.Unlock()
			batch = batch[:0]
		}
	}
}

func (mc *MetricsCollector) computeSnapshot(results []*protocol.Result, window time.Duration) MetricsSnapshot {
	snap := MetricsSnapshot{
		Timestamp:    time.Now(),
		Window:       window,
		ActiveVUsers: mc.current.activeVUsers.Load(),
		ByAction:     make(map[string]*ActionMetrics),
		ByGroup:      make(map[string]*GroupMetrics),
		BrowserByDevice: make(map[string]*BrowserDeviceMetrics),
		Telemetry: &TelemetryMetrics{
			DroppedResults: mc.current.droppedResults.Load(),
		},
	}

	var totalDuration time.Duration
	td := NewTDigest(200)
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
		am.Count++
		if !r.Success {
			am.Errors++
		}
		mergeActionMetadata(am, r)
		am.BytesSent += r.BytesSent
		am.BytesRecv += r.BytesReceived
		if r.Duration < am.MinDuration {
			am.MinDuration = r.Duration
		}
		if r.Duration > am.MaxDuration {
			am.MaxDuration = r.Duration
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
						DeviceProfile: profile,
						DevicePlatform: r.ClientMetrics.DevicePlatform,
						DeviceTier:    r.ClientMetrics.DeviceTier,
						BrowserEngine: r.ClientMetrics.BrowserEngine,
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

	for name, gm := range snap.ByGroup {
		if gm.Transactions > 0 {
			gd := groupDigests[name]
			gm.AvgDuration = groupDurationSum[name] / time.Duration(gm.Transactions)
			gm.TPS = float64(gm.Transactions) / window.Seconds()
			gm.P50 = time.Duration(gd.Quantile(0.50))
			gm.P90 = time.Duration(gd.Quantile(0.90))
			gm.P99 = time.Duration(gd.Quantile(0.99))
		}
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
}

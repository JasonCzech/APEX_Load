package engine

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/jasonczech/stormrunner/pkg/protocol"
)

type MetricsCollector struct {
	ch          chan *protocol.Result
	snapshots   []MetricsSnapshot
	snapshotsMu sync.RWMutex
	current     *liveCounters
	resCollector *ResourceCollector
	done        chan struct{}
}

type liveCounters struct {
	totalTransactions atomic.Int64
	totalErrors       atomic.Int64
	activeVUsers      atomic.Int64
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
	Resources    *SystemResources `json:"resources,omitempty"`
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
	Name         string
	Count        int64
	Errors       int64
	AvgDuration  time.Duration
	MinDuration  time.Duration
	MaxDuration  time.Duration
	P50          time.Duration
	P90          time.Duration
	P99          time.Duration
	BytesSent    int64
	BytesRecv    int64
}

func NewMetricsCollector(bufferSize int) *MetricsCollector {
	if bufferSize <= 0 {
		bufferSize = 10000
	}
	return &MetricsCollector{
		ch:      make(chan *protocol.Result, bufferSize),
		current: &liveCounters{},
		resCollector: NewResourceCollector(),
		done:    make(chan struct{}),
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
		// channel full — drop oldest metric rather than blocking the hot path
	}
}

func (mc *MetricsCollector) SetActiveVUsers(n int64) {
	mc.current.activeVUsers.Store(n)
}

func (mc *MetricsCollector) Start(interval time.Duration) {
	go mc.aggregate(interval)
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
			if len(batch) == 0 {
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
	}

	var totalDuration time.Duration
	durations := make([]time.Duration, 0, len(results))
	groupDurations := make(map[string][]time.Duration)

	for _, r := range results {
		snap.Transactions++
		if !r.Success {
			snap.Errors++
		}
		totalDuration += r.Duration
		durations = append(durations, r.Duration)

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
			groupDurations[r.Group] = append(groupDurations[r.Group], r.Duration)
		}
	}

	if snap.Transactions > 0 {
		snap.AvgDuration = totalDuration / time.Duration(snap.Transactions)
		snap.TPS = float64(snap.Transactions) / window.Seconds()

		sortDurations(durations)
		snap.P50 = percentile(durations, 0.50)
		snap.P90 = percentile(durations, 0.90)
		snap.P95 = percentile(durations, 0.95)
		snap.P99 = percentile(durations, 0.99)
	}

	for name, gm := range snap.ByGroup {
		if gm.Transactions > 0 {
			gd := groupDurations[name]
			var sum time.Duration
			for _, d := range gd {
				sum += d
			}
			gm.AvgDuration = sum / time.Duration(gm.Transactions)
			gm.TPS = float64(gm.Transactions) / window.Seconds()
			sortDurations(gd)
			gm.P50 = percentile(gd, 0.50)
			gm.P90 = percentile(gd, 0.90)
			gm.P99 = percentile(gd, 0.99)
		}
	}

	return snap
}

func sortDurations(d []time.Duration) {
	// insertion sort — fast for typical batch sizes (<10k)
	for i := 1; i < len(d); i++ {
		key := d[i]
		j := i - 1
		for j >= 0 && d[j] > key {
			d[j+1] = d[j]
			j--
		}
		d[j+1] = key
	}
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(p * float64(len(sorted)-1))
	return sorted[idx]
}

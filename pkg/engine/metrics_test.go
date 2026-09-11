package engine

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestNewMetricsCollector(t *testing.T) {
	mc := NewMetricsCollector(100)
	if mc == nil {
		t.Fatal("NewMetricsCollector returned nil")
	}
	if cap(mc.ch) != 100 {
		t.Errorf("buffer capacity = %d, want 100", cap(mc.ch))
	}
}

func TestNewMetricsCollector_DefaultBuffer(t *testing.T) {
	mc := NewMetricsCollector(0)
	if cap(mc.ch) != 10000 {
		t.Errorf("buffer capacity = %d, want 10000", cap(mc.ch))
	}
}

func TestMetricsCollectorTransactionResponseTimeSLAIsCumulativeAndSuccessOnly(t *testing.T) {
	mc := NewMetricsCollector(10)
	mc.SetTransactionResponseTimeSLAThresholds(map[string]float64{"A": 3000, "B": 1000})
	first := mc.computeSnapshot([]*protocol.Result{
		{Group: "A", ActionName: "Checkout", BusinessTransaction: true, Success: true, Duration: 3000 * time.Millisecond},
		{Group: "A", ActionName: "Checkout", BusinessTransaction: true, Success: false, Duration: 9000 * time.Millisecond},
		{Group: "A", ActionName: "request", Success: true, Duration: 9000 * time.Millisecond},
		{Group: "B", ActionName: "Checkout", BusinessTransaction: true, Success: true, Duration: 1001 * time.Millisecond},
	}, time.Second)
	if len(first.TransactionResponseTimeSLA) != 2 {
		t.Fatalf("first SLA rows = %#v", first.TransactionResponseTimeSLA)
	}
	second := mc.computeSnapshot([]*protocol.Result{
		{Group: "A", ActionName: "Checkout", BusinessTransaction: true, Success: true, Duration: 3001 * time.Millisecond},
	}, time.Second)
	rows := make(map[string]TransactionResponseTimeSLACompliance)
	for _, row := range second.TransactionResponseTimeSLA {
		rows[row.Group] = row
	}
	if got := rows["A"]; got.TotalCount != 3 || got.EligibleSuccessCount != 2 || got.ExcludedErrorCount != 1 || got.MissedCount != 1 {
		t.Errorf("group A = %#v", got)
	}
	if got := rows["B"]; got.TotalCount != 1 || got.MissedCount != 1 || got.ThresholdMs != 1000 {
		t.Errorf("group B = %#v", got)
	}
}

func TestMetricsCollector_RecordAndCount(t *testing.T) {
	mc := NewMetricsCollector(100)

	mc.Record(&protocol.Result{Success: true, ActionName: "a"})
	mc.Record(&protocol.Result{Success: false, ActionName: "b"})
	mc.Record(&protocol.Result{Success: true, ActionName: "c"})

	if got := mc.TotalTransactions(); got != 0 {
		t.Errorf("TotalTransactions after raw actions = %d, want 0", got)
	}
	if got := mc.TotalErrors(); got != 0 {
		t.Errorf("TotalErrors after raw actions = %d, want 0", got)
	}

	mc.RecordTransaction("iteration", 10*time.Millisecond, true, "buyers")
	mc.RecordTransaction("iteration", 10*time.Millisecond, false, "buyers")

	if got := mc.TotalTransactions(); got != 1 {
		t.Errorf("TotalTransactions after business transactions = %d, want 1", got)
	}
	if got := mc.TotalErrors(); got != 1 {
		t.Errorf("TotalErrors after business transactions = %d, want 1", got)
	}
	transactions, errors := mc.GroupRunTotals("buyers")
	if transactions != 1 || errors != 1 {
		t.Fatalf("buyers cumulative totals = (%d, %d), want (1, 1)", transactions, errors)
	}
}

func TestMetricsCollector_RecordTracksDroppedResults(t *testing.T) {
	mc := NewMetricsCollector(1)
	mc.Record(&protocol.Result{Success: true, ActionName: "a"})
	mc.Record(&protocol.Result{Success: true, ActionName: "b"})

	snap := mc.computeSnapshot(nil, time.Second)
	if snap.Telemetry == nil {
		t.Fatal("expected telemetry snapshot")
	}
	if got := snap.Telemetry.DroppedResults; got != 1 {
		t.Fatalf("DroppedResults = %d, want 1", got)
	}
}

func TestMetricsCollector_SetActiveVUsers(t *testing.T) {
	mc := NewMetricsCollector(100)
	mc.SetActiveVUsers(42)
	if got := mc.current.activeVUsers.Load(); got != 42 {
		t.Errorf("activeVUsers = %d, want 42", got)
	}
}

func TestMetricsCollector_LatestSnapshot_Empty(t *testing.T) {
	mc := NewMetricsCollector(100)
	_, ok := mc.LatestSnapshot()
	if ok {
		t.Error("LatestSnapshot should return false when no snapshots exist")
	}
}

func TestMetricsCollectorStopFlushesTrailingPartialWindow(t *testing.T) {
	mc := NewMetricsCollector(8)
	mc.Start(time.Hour)
	mc.RecordTransaction("short", 5*time.Millisecond, true, "")
	mc.Stop()

	snap, ok := mc.LatestSnapshot()
	if !ok {
		t.Fatal("Stop did not synchronously publish the trailing window")
	}
	if snap.Transactions != 1 {
		t.Fatalf("transactions = %d, want 1", snap.Transactions)
	}
	if snap.Window <= 0 || snap.Window >= time.Hour {
		t.Fatalf("partial window = %v, want between zero and the tick interval", snap.Window)
	}
	mc.Stop()
}

func TestMetricsCollector_AggregateProducesSnapshot(t *testing.T) {
	mc := NewMetricsCollector(100)
	mc.Start(50 * time.Millisecond)
	defer mc.Stop()

	for i := 0; i < 10; i++ {
		mc.Record(&protocol.Result{
			ActionName: "test",
			Success:    true,
			Duration:   time.Duration(i+1) * time.Millisecond,
		})
	}

	time.Sleep(150 * time.Millisecond)

	snap, ok := mc.LatestSnapshot()
	if !ok {
		t.Fatal("expected a snapshot after aggregation")
	}
	if snap.Transactions != 0 {
		t.Errorf("transactions = %d, want 0 for raw action samples", snap.Transactions)
	}
	if snap.Errors != 0 {
		t.Errorf("errors = %d, want 0", snap.Errors)
	}
	if action := snap.ByAction["test"]; action == nil || action.Count != 10 {
		t.Fatalf("raw action count = %#v, want 10", action)
	}
}

func TestMetricsCollector_AggregateProducesIdleSnapshot(t *testing.T) {
	mc := NewMetricsCollector(100)
	mc.EnableIdleSnapshots(true)
	mc.SetActiveVUsers(0)
	mc.Start(50 * time.Millisecond)
	defer mc.Stop()

	time.Sleep(120 * time.Millisecond)

	snap, ok := mc.LatestSnapshot()
	if !ok {
		t.Fatal("expected an idle snapshot after aggregation")
	}
	if snap.Transactions != 0 {
		t.Errorf("transactions = %d, want 0", snap.Transactions)
	}
	if snap.ActiveVUsers != 0 {
		t.Errorf("active vusers = %d, want 0", snap.ActiveVUsers)
	}
	if snap.Resources == nil {
		t.Fatal("expected idle snapshot to include resources")
	}
}

func TestMetricsCollector_IdleSnapshotToggleIsConcurrentSafe(t *testing.T) {
	mc := NewMetricsCollector(100)
	mc.Start(time.Millisecond)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1_000; i++ {
			mc.EnableIdleSnapshots(i%2 == 0)
		}
	}()
	<-done
	mc.Stop()
}

// TestMetricsCollector_EmptyWindowSnapshotEmission pins down when an empty
// aggregation window (no completed transactions) still produces a zero-valued,
// timestamped snapshot. The key case is "active VUsers but nothing completing"
// — a stall, long think-time, or blocked downstream — which must emit so the
// runtime graphs draw a flat zero line rather than a multi-minute gap.
func TestMetricsCollector_EmptyWindowSnapshotEmission(t *testing.T) {
	tests := []struct {
		name          string
		idleSnapshots bool
		activeVUsers  int64
		wantSnapshot  bool
	}{
		{name: "active vusers with no completed transactions emits (stall)", idleSnapshots: false, activeVUsers: 8, wantSnapshot: true},
		{name: "active vusers with baseline configured also emits", idleSnapshots: true, activeVUsers: 8, wantSnapshot: true},
		{name: "baseline window with no vusers emits", idleSnapshots: true, activeVUsers: 0, wantSnapshot: true},
		{name: "idle window with no vusers and no baseline is skipped", idleSnapshots: false, activeVUsers: 0, wantSnapshot: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mc := NewMetricsCollector(100)
			mc.EnableIdleSnapshots(tt.idleSnapshots)
			mc.SetActiveVUsers(tt.activeVUsers)
			mc.Start(20 * time.Millisecond)
			defer mc.Stop()

			// Several ticks elapse with nothing recorded — every window is empty.
			time.Sleep(90 * time.Millisecond)

			snap, ok := mc.LatestSnapshot()
			if ok != tt.wantSnapshot {
				t.Fatalf("LatestSnapshot ok = %v, want %v", ok, tt.wantSnapshot)
			}
			if !tt.wantSnapshot {
				return
			}
			if snap.Transactions != 0 || snap.Errors != 0 {
				t.Errorf("empty-window snapshot tx/err = %d/%d, want 0/0", snap.Transactions, snap.Errors)
			}
			if snap.TPS != 0 {
				t.Errorf("empty-window TPS = %v, want 0", snap.TPS)
			}
			if snap.ActiveVUsers != tt.activeVUsers {
				t.Errorf("snapshot ActiveVUsers = %d, want %d", snap.ActiveVUsers, tt.activeVUsers)
			}
			if snap.Timestamp.IsZero() {
				t.Error("empty-window snapshot must carry a timestamp")
			}
		})
	}
}

func TestMetricsCollector_ActiveVUserTransitionsPublishZeroOnce(t *testing.T) {
	const interval = 20 * time.Millisecond
	mc := NewMetricsCollector(100)
	var groupActive atomic.Int64
	mc.SetGroupActiveVUsersProvider(func() map[string]int {
		return map[string]int{"buyers": int(groupActive.Load())}
	})
	mc.Start(interval)
	defer mc.Stop()

	waitForGauge := func(after time.Time, want int64) MetricsSnapshot {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			snapshot, ok := mc.LatestSnapshot()
			if ok && snapshot.Timestamp.After(after) && snapshot.ActiveVUsers == want {
				group := snapshot.ByGroup["buyers"]
				if group != nil && group.ActiveVUsersReported && group.ActiveVUsers == want {
					return snapshot
				}
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatalf("timed out waiting for active VUser gauge %d", want)
		return MetricsSnapshot{}
	}

	groupActive.Store(2)
	mc.SetActiveVUsers(2)
	nonzero := waitForGauge(time.Time{}, 2)

	// Scaling to zero is not necessarily terminal. Even with no transaction
	// result in the window, publish an authoritative zero so downstream local and
	// distributed projections cannot retain the previous nonzero gauge.
	groupActive.Store(0)
	mc.SetActiveVUsers(0)
	zero := waitForGauge(nonzero.Timestamp, 0)

	// A zero transition is emitted once, not on every idle tick. This retains the
	// existing suppression of unbounded leading/trailing zero snapshots.
	time.Sleep(4 * interval)
	mc.snapshotsMu.RLock()
	zeroSnapshots := 0
	for _, snapshot := range mc.snapshots {
		if !snapshot.Timestamp.Before(zero.Timestamp) && snapshot.ActiveVUsers == 0 {
			zeroSnapshots++
		}
	}
	mc.snapshotsMu.RUnlock()
	if zeroSnapshots != 1 {
		t.Fatalf("zero transition produced %d idle snapshots, want exactly 1", zeroSnapshots)
	}

	groupActive.Store(5)
	mc.SetActiveVUsers(5)
	waitForGauge(zero.Timestamp, 5)
}

// TestMetricsCollector_NonEmptyWindowUnaffected confirms a window that does
// contain completed transactions is still aggregated normally — the empty-window
// emission change must not alter the populated path.
func TestMetricsCollector_NonEmptyWindowUnaffected(t *testing.T) {
	mc := NewMetricsCollector(100)
	mc.SetActiveVUsers(4)

	// Buffer both results before starting the aggregator so they drain into the
	// same window deterministically.
	mc.RecordTransaction("checkout", 12*time.Millisecond, true, "buyers")
	mc.RecordTransaction("checkout", 8*time.Millisecond, false, "buyers")

	mc.Start(20 * time.Millisecond)
	// Stop synchronously drains the buffered results and publishes the final
	// populated window. Sleeping for a fixed number of ticker intervals made
	// this assertion depend on scheduler latency under a busy package run.
	mc.Stop()

	if got := mc.TotalTransactions(); got != 1 {
		t.Errorf("total transactions = %d, want 1", got)
	}
	if got := mc.TotalErrors(); got != 1 {
		t.Errorf("total errors = %d, want 1", got)
	}

	// The completed transactions must appear in some emitted window with the
	// real values (later empty windows now emit zeros too, so scan history).
	mc.snapshotsMu.RLock()
	defer mc.snapshotsMu.RUnlock()
	var found *MetricsSnapshot
	for i := range mc.snapshots {
		if mc.snapshots[i].Transactions == 1 && mc.snapshots[i].Errors == 1 {
			found = &mc.snapshots[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("no snapshot captured the completed transaction window; snapshots=%d", len(mc.snapshots))
	}
	if found.AvgDuration <= 0 {
		t.Errorf("populated-window AvgDuration = %v, want > 0", found.AvgDuration)
	}
	if buyers := found.ByGroup["buyers"]; buyers == nil || buyers.Transactions != 1 {
		t.Errorf("populated-window group 'buyers' = %#v, want 1 transaction", buyers)
	}
}

func TestMetricsCollector_ByAction(t *testing.T) {
	mc := NewMetricsCollector(100)
	mc.Start(50 * time.Millisecond)
	defer mc.Stop()

	mc.Record(&protocol.Result{ActionName: "login", Success: true, Duration: 10 * time.Millisecond, BytesSent: 100, BytesReceived: 500})
	errorAt := time.Date(2026, time.May, 18, 10, 30, 0, 0, time.UTC)
	mc.Record(&protocol.Result{ActionName: "login", Success: false, Duration: 20 * time.Millisecond, StartTime: errorAt, StatusCode: 503, ErrorMessage: "timeout"})
	mc.Record(&protocol.Result{ActionName: "checkout", Success: true, Duration: 5 * time.Millisecond})

	time.Sleep(150 * time.Millisecond)

	snap, ok := mc.LatestSnapshot()
	if !ok {
		t.Fatal("expected snapshot")
	}

	login, ok := snap.ByAction["login"]
	if !ok {
		t.Fatal("missing ByAction entry for 'login'")
	}
	if login.Count != 2 {
		t.Errorf("login count = %d, want 2", login.Count)
	}
	if login.Errors != 1 {
		t.Errorf("login errors = %d, want 1", login.Errors)
	}
	if login.LastErrorMessage != "timeout" || login.LastStatusCode != 503 || !login.LastErrorAt.Equal(errorAt) {
		t.Errorf("login last error = (%q, %d, %v), want timeout/503/%v", login.LastErrorMessage, login.LastStatusCode, login.LastErrorAt, errorAt)
	}

	checkout, ok := snap.ByAction["checkout"]
	if !ok {
		t.Fatal("missing ByAction entry for 'checkout'")
	}
	if checkout.Count != 1 {
		t.Errorf("checkout count = %d, want 1", checkout.Count)
	}
}

func TestMetricsCollector_ByGroup(t *testing.T) {
	mc := NewMetricsCollector(100)
	mc.Start(50 * time.Millisecond)
	defer mc.Stop()

	mc.RecordTransaction("checkout", 10*time.Millisecond, true, "buyers")
	mc.RecordTransaction("checkout", 20*time.Millisecond, true, "buyers")
	mc.RecordTransaction("listing", 5*time.Millisecond, true, "sellers")

	time.Sleep(150 * time.Millisecond)

	snap, _ := mc.LatestSnapshot()
	buyers, ok := snap.ByGroup["buyers"]
	if !ok {
		t.Fatal("missing group 'buyers'")
	}
	if buyers.Transactions != 2 {
		t.Errorf("buyers transactions = %d, want 2", buyers.Transactions)
	}
	if buyers.P95 <= 0 {
		t.Errorf("buyers P95 = %v, want populated group percentile", buyers.P95)
	}
	if buyers.DurationDigest == nil || buyers.DurationDigest.Count != 2 {
		t.Fatalf("buyers duration digest = %#v, want 2 samples", buyers.DurationDigest)
	}

	sellers, ok := snap.ByGroup["sellers"]
	if !ok {
		t.Fatal("missing group 'sellers'")
	}
	if sellers.Transactions != 1 {
		t.Errorf("sellers transactions = %d, want 1", sellers.Transactions)
	}
}

func TestMetricsCollector_TracksThroughputTargetGap(t *testing.T) {
	mc := NewMetricsCollector(100)
	mc.SetTargetTPS(8)
	mc.SetGroupTargetTPS("buyers", 6)

	snap := mc.computeSnapshot([]*protocol.Result{
		{ActionName: "a", BusinessTransaction: true, Group: "buyers", Success: true, Duration: 10 * time.Millisecond},
		{ActionName: "b", BusinessTransaction: true, Group: "buyers", Success: true, Duration: 12 * time.Millisecond},
		{ActionName: "c", BusinessTransaction: true, Group: "buyers", Success: true, Duration: 14 * time.Millisecond},
		{ActionName: "d", BusinessTransaction: true, Group: "buyers", Success: true, Duration: 16 * time.Millisecond},
	}, time.Second)

	if snap.TargetTPS != 8 {
		t.Fatalf("target TPS = %v, want 8", snap.TargetTPS)
	}
	if snap.TPS != 4 {
		t.Fatalf("actual TPS = %v, want 4", snap.TPS)
	}
	if !snap.ThroughputTargetMissed {
		t.Fatal("expected overall throughput target miss")
	}
	if snap.ThroughputGapPct < 49 || snap.ThroughputGapPct > 51 {
		t.Fatalf("overall throughput gap = %v, want about 50%%", snap.ThroughputGapPct)
	}

	buyers := snap.ByGroup["buyers"]
	if buyers == nil {
		t.Fatal("expected buyers group metrics")
	}
	if buyers.TargetTPS != 6 {
		t.Fatalf("buyers target TPS = %v, want 6", buyers.TargetTPS)
	}
	if !buyers.ThroughputTargetMissed {
		t.Fatal("expected buyers throughput target miss")
	}
	if buyers.ThroughputGapPct < 32 || buyers.ThroughputGapPct > 34 {
		t.Fatalf("buyers throughput gap = %v, want about 33%%", buyers.ThroughputGapPct)
	}
}

func TestMetricsCollector_BrowserMetricsSummary(t *testing.T) {
	mc := NewMetricsCollector(100)
	snap := mc.computeSnapshot([]*protocol.Result{
		{
			ActionName: "page-1",
			Success:    true,
			Duration:   10 * time.Millisecond,
			ClientMetrics: &protocol.ClientMetrics{
				DeviceProfile:         "iphone_se",
				DevicePlatform:        "ios",
				DeviceTier:            "low",
				BrowserEngine:         "webkit",
				FCPMs:                 120,
				LCPMs:                 220,
				CLSScore:              0.02,
				INPMs:                 45,
				BlockingResourceCount: 2,
				LongTaskCount:         1,
				JSHeapUsedMB:          12,
				BrowserProcessCPUPct:  10,
				BrowserProcessMemMB:   120,
			},
		},
		{
			ActionName: "page-2",
			Success:    true,
			Duration:   20 * time.Millisecond,
			ClientMetrics: &protocol.ClientMetrics{
				DeviceProfile:         "iphone_se",
				DevicePlatform:        "ios",
				DeviceTier:            "low",
				BrowserEngine:         "webkit",
				FCPMs:                 180,
				LCPMs:                 280,
				CLSScore:              0.04,
				INPMs:                 55,
				BlockingResourceCount: 4,
				LongTaskCount:         3,
				JSHeapUsedMB:          18,
				BrowserProcessCPUPct:  30,
				BrowserProcessMemMB:   180,
			},
		},
		{
			ActionName: "page-3",
			Success:    true,
			Duration:   15 * time.Millisecond,
			ClientMetrics: &protocol.ClientMetrics{
				DeviceProfile:         "pixel_8",
				DevicePlatform:        "android",
				DeviceTier:            "high",
				BrowserEngine:         "chromium",
				FCPMs:                 90,
				LCPMs:                 160,
				CLSScore:              0.01,
				INPMs:                 35,
				BlockingResourceCount: 1,
				LongTaskCount:         0,
				JSHeapUsedMB:          10,
				BrowserProcessCPUPct:  20,
				BrowserProcessMemMB:   100,
			},
		},
	}, time.Second)

	if snap.Browser == nil {
		t.Fatal("Browser = nil, want summary")
	}
	if snap.Browser.Samples != 3 {
		t.Fatalf("Browser.Samples = %d, want 3", snap.Browser.Samples)
	}
	if len(snap.BrowserByDevice) != 2 {
		t.Fatalf("len(BrowserByDevice) = %d, want 2", len(snap.BrowserByDevice))
	}
	iphone := snap.BrowserByDevice["iphone_se"]
	if iphone == nil {
		t.Fatal("missing iphone_se summary")
	}
	if iphone.DeviceTier != "low" {
		t.Fatalf("DeviceTier = %q, want low", iphone.DeviceTier)
	}
	if iphone.BrowserEngine != "webkit" {
		t.Fatalf("BrowserEngine = %q, want webkit", iphone.BrowserEngine)
	}
	if iphone.DevicePlatform != "ios" {
		t.Fatalf("DevicePlatform = %q, want ios", iphone.DevicePlatform)
	}
	if iphone.AvgFCPMs != 150 {
		t.Fatalf("AvgFCPMs = %v, want 150", iphone.AvgFCPMs)
	}
	if iphone.AvgBrowserProcessCPU != 20 {
		t.Fatalf("AvgBrowserProcessCPU = %v, want 20", iphone.AvgBrowserProcessCPU)
	}
	if iphone.AvgBrowserProcessMem != 150 {
		t.Fatalf("AvgBrowserProcessMem = %v, want 150", iphone.AvgBrowserProcessMem)
	}
	pixel := snap.BrowserByDevice["pixel_8"]
	if pixel == nil {
		t.Fatal("missing pixel_8 summary")
	}
	if pixel.BrowserEngine != "chromium" {
		t.Fatalf("pixel_8 BrowserEngine = %q, want chromium", pixel.BrowserEngine)
	}
	if pixel.DevicePlatform != "android" {
		t.Fatalf("pixel_8 DevicePlatform = %q, want android", pixel.DevicePlatform)
	}
}

func TestMetricsCollector_RecordTransaction(t *testing.T) {
	mc := NewMetricsCollector(100)
	mc.Start(50 * time.Millisecond)
	defer mc.Stop()

	mc.RecordTransaction("iteration_1", 100*time.Millisecond, true, "group-a")

	time.Sleep(150 * time.Millisecond)

	snap, ok := mc.LatestSnapshot()
	if !ok {
		t.Fatal("expected snapshot")
	}

	_, ok = snap.ByTransaction["iteration_1"]
	if !ok {
		t.Error("expected synthetic transaction in ByTransaction")
	}
}

func TestMetricsCollector_ByActionCapturesDependencyAndCorrelationMetadata(t *testing.T) {
	mc := NewMetricsCollector(100)
	snap := mc.computeSnapshot([]*protocol.Result{
		{ActionName: "login", Success: true, Duration: 10 * time.Millisecond, DependencyType: "http", DependencyTarget: "api.example.test", CorrelationID: "corr-1"},
		{ActionName: "login", Success: true, Duration: 20 * time.Millisecond, DependencyType: "http", DependencyTarget: "api.example.test", CorrelationID: "corr-2"},
		{ActionName: "login", Success: true, Duration: 25 * time.Millisecond, DependencyType: "http", DependencyTarget: "api.example.test", CorrelationID: "corr-2"},
	}, 50*time.Millisecond)
	login := snap.ByAction["login"]
	if login == nil {
		t.Fatal("missing action metadata for login")
	}
	if login.DependencyType != "http" || login.DependencyTarget != "api.example.test" {
		t.Fatalf("unexpected dependency metadata: %#v", login)
	}
	if login.CorrelationCount != 3 {
		t.Fatalf("CorrelationCount = %d, want 3", login.CorrelationCount)
	}
	if len(login.CorrelationIDs) != 2 {
		t.Fatalf("CorrelationIDs = %#v, want 2 unique ids", login.CorrelationIDs)
	}
}

func TestMetricsCollector_AggregatesByNetworkProfile(t *testing.T) {
	mc := NewMetricsCollector(100)
	snap := mc.computeSnapshot([]*protocol.Result{
		{ActionName: "home", Success: true, Duration: 10 * time.Millisecond, NetworkProfile: "preset:dsl"},
		{ActionName: "checkout", Success: false, Duration: 30 * time.Millisecond, NetworkProfile: "preset:dsl"},
		{ActionName: "home", Success: true, Duration: 20 * time.Millisecond, NetworkProfile: "custom:latency=200ms"},
	}, time.Second)

	dsl := snap.ByNetworkProfile["preset:dsl"]
	if dsl == nil {
		t.Fatal("missing preset:dsl network profile metrics")
	}
	if dsl.Count != 2 || dsl.Errors != 1 {
		t.Fatalf("preset:dsl metrics = %#v, want count 2 errors 1", dsl)
	}
	if dsl.AvgDuration != 20*time.Millisecond {
		t.Fatalf("preset:dsl avg = %v, want 20ms", dsl.AvgDuration)
	}
	custom := snap.ByNetworkProfile["custom:latency=200ms"]
	if custom == nil || custom.Count != 1 {
		t.Fatalf("custom metrics = %#v, want one sample", custom)
	}
}

func TestMetricsCollector_AggregatesBusinessTransactionsByApprovedRegion(t *testing.T) {
	mc := NewMetricsCollector(100)
	snap := mc.computeSnapshot([]*protocol.Result{
		{ActionName: "home", BusinessTransaction: true, Success: true, Duration: 10 * time.Millisecond, Region: "us-east"},
		{ActionName: "checkout", BusinessTransaction: true, Success: false, Duration: 30 * time.Millisecond, Region: "us-east"},
		{ActionName: "home", BusinessTransaction: true, Success: true, Duration: 20 * time.Millisecond, Region: "eu-west"},
		{ActionName: "unattributed", BusinessTransaction: true, Success: true, Duration: time.Millisecond},
	}, time.Second)

	east := snap.ByRegion["us-east"]
	if east == nil || east.Count != 2 || east.Errors != 1 || east.AvgDuration != 20*time.Millisecond {
		t.Fatalf("us-east metrics = %#v, want count 2 errors 1 avg 20ms", east)
	}
	west := snap.ByRegion["eu-west"]
	if west == nil || west.Count != 1 || west.Errors != 0 {
		t.Fatalf("eu-west metrics = %#v, want one successful transaction", west)
	}
	if _, exists := snap.ByRegion["unnamed"]; exists {
		t.Fatalf("blank regions must remain unattributed, got %#v", snap.ByRegion)
	}
}

func TestMetricsCollector_ByActionAggregatesServerTelemetry(t *testing.T) {
	mc := NewMetricsCollector(100)
	snap := mc.computeSnapshot([]*protocol.Result{
		{
			ActionName:  "login",
			Success:     true,
			Duration:    10 * time.Millisecond,
			DNSTime:     8 * time.Millisecond,
			ConnectTime: 12 * time.Millisecond,
			TLSTime:     5 * time.Millisecond,
			TTFB:        55 * time.Millisecond,
			ServerTelemetry: &protocol.ServerTelemetry{
				ServerProcessingMs: 60,
				TransferMs:         20,
				RequestPayloadKB:   1.5,
				RequestSizeClass:   "small",
				ResponsePayloadKB:  3.0,
				ResponseSizeClass:  "medium",
				MessageLatencyMs:   25,
				MessagesSent:       1,
				MessagesReceived:   0,
				MessagesSentPerSec: 100,
				ConsumerLagMs:      40,
				ConnectionReused:   false,
			},
		},
		{
			ActionName:  "login",
			Success:     true,
			Duration:    15 * time.Millisecond,
			DNSTime:     12 * time.Millisecond,
			ConnectTime: 18 * time.Millisecond,
			TTFB:        50 * time.Millisecond,
			ServerTelemetry: &protocol.ServerTelemetry{
				ServerProcessingMs:     40,
				TransferMs:             10,
				RequestPayloadKB:       2.5,
				RequestSizeClass:       "medium",
				ResponsePayloadKB:      5.0,
				ResponseSizeClass:      "large",
				MessageLatencyMs:       45,
				MessagesSent:           1,
				MessagesReceived:       1,
				MessagesSentPerSec:     50,
				MessagesReceivedPerSec: 50,
				ConsumerLagMs:          70,
				ConnectionReused:       true,
			},
		},
	}, time.Second)

	login := snap.ByAction["login"]
	if login == nil || login.ServerTelemetry == nil {
		t.Fatalf("login server telemetry = %#v, want populated summary", login)
	}
	if login.ServerTelemetry.Samples != 2 {
		t.Fatalf("Samples = %d, want 2", login.ServerTelemetry.Samples)
	}
	if login.ServerTelemetry.AvgServerProcessingMs != 50 {
		t.Fatalf("AvgServerProcessingMs = %v, want 50", login.ServerTelemetry.AvgServerProcessingMs)
	}
	if login.ServerTelemetry.AvgTransferMs != 15 {
		t.Fatalf("AvgTransferMs = %v, want 15", login.ServerTelemetry.AvgTransferMs)
	}
	if login.ServerTelemetry.AvgRequestPayloadKB != 2 {
		t.Fatalf("AvgRequestPayloadKB = %v, want 2", login.ServerTelemetry.AvgRequestPayloadKB)
	}
	if login.AvgDNSMs != 10 || login.AvgConnectMs != 15 || login.AvgTLSMs != 2.5 || login.AvgTTFBMs != 52.5 {
		t.Fatalf("HTTP timing averages = dns:%v connect:%v tls:%v ttfb:%v, want 10/15/2.5/52.5", login.AvgDNSMs, login.AvgConnectMs, login.AvgTLSMs, login.AvgTTFBMs)
	}
	if login.ServerTelemetry.AvgResponsePayloadKB != 4 {
		t.Fatalf("AvgResponsePayloadKB = %v, want 4", login.ServerTelemetry.AvgResponsePayloadKB)
	}
	if login.ServerTelemetry.RequestPayloadHistogram["1-4 KB"] != 2 {
		t.Fatalf("RequestPayloadHistogram = %#v, want 1-4 KB count 2", login.ServerTelemetry.RequestPayloadHistogram)
	}
	if login.ServerTelemetry.ResponsePayloadHistogram["1-4 KB"] != 1 || login.ServerTelemetry.ResponsePayloadHistogram["4-16 KB"] != 1 {
		t.Fatalf("ResponsePayloadHistogram = %#v, want 1-4 KB and 4-16 KB counts", login.ServerTelemetry.ResponsePayloadHistogram)
	}
	if login.ServerTelemetry.AvgMessageLatencyMs != 35 {
		t.Fatalf("AvgMessageLatencyMs = %v, want 35", login.ServerTelemetry.AvgMessageLatencyMs)
	}
	if login.ServerTelemetry.MaxMessageLatencyMs != 45 {
		t.Fatalf("MaxMessageLatencyMs = %v, want 45", login.ServerTelemetry.MaxMessageLatencyMs)
	}
	if login.ServerTelemetry.MessagesSent != 2 || login.ServerTelemetry.MessagesReceived != 1 {
		t.Fatalf("message totals = sent:%d received:%d, want 2 and 1", login.ServerTelemetry.MessagesSent, login.ServerTelemetry.MessagesReceived)
	}
	if login.ServerTelemetry.AvgMessagesSentPerSec != 75 {
		t.Fatalf("AvgMessagesSentPerSec = %v, want 75", login.ServerTelemetry.AvgMessagesSentPerSec)
	}
	if login.ServerTelemetry.AvgMessagesReceivedPerSec != 25 {
		t.Fatalf("AvgMessagesReceivedPerSec = %v, want 25", login.ServerTelemetry.AvgMessagesReceivedPerSec)
	}
	if login.ServerTelemetry.AvgConsumerLagMs != 55 {
		t.Fatalf("AvgConsumerLagMs = %v, want 55", login.ServerTelemetry.AvgConsumerLagMs)
	}
	if login.ServerTelemetry.MaxConsumerLagMs != 70 {
		t.Fatalf("MaxConsumerLagMs = %v, want 70", login.ServerTelemetry.MaxConsumerLagMs)
	}
	if login.ServerTelemetry.RequestSizeClasses["small"] != 1 || login.ServerTelemetry.RequestSizeClasses["medium"] != 1 {
		t.Fatalf("RequestSizeClasses = %#v, want small=1 medium=1", login.ServerTelemetry.RequestSizeClasses)
	}
	if login.ServerTelemetry.ResponseSizeClasses["medium"] != 1 || login.ServerTelemetry.ResponseSizeClasses["large"] != 1 {
		t.Fatalf("ResponseSizeClasses = %#v, want medium=1 large=1", login.ServerTelemetry.ResponseSizeClasses)
	}
	if login.ServerTelemetry.ConnectionReusedCount != 1 {
		t.Fatalf("ConnectionReusedCount = %d, want 1", login.ServerTelemetry.ConnectionReusedCount)
	}
	if login.ServerTelemetry.ConnectionReusedPct != 50 {
		t.Fatalf("ConnectionReusedPct = %v, want 50", login.ServerTelemetry.ConnectionReusedPct)
	}
}

func TestMetricsCollector_ByDependencyTargetAggregatesLatencyAndTelemetry(t *testing.T) {
	mc := NewMetricsCollector(100)
	snap := mc.computeSnapshot([]*protocol.Result{
		{
			ActionName:       "GET /",
			Success:          true,
			Duration:         50 * time.Millisecond,
			DependencyType:   "http",
			DependencyTarget: "api.example.test",
			DNSTime:          5 * time.Millisecond,
			ConnectTime:      10 * time.Millisecond,
			ServerTelemetry:  &protocol.ServerTelemetry{ServerProcessingMs: 20, TransferMs: 8},
		},
		{
			ActionName:       "GET /catalog",
			Success:          true,
			Duration:         100 * time.Millisecond,
			DependencyType:   "http",
			DependencyTarget: "api.example.test",
			DNSTime:          7 * time.Millisecond,
			ConnectTime:      14 * time.Millisecond,
			ServerTelemetry:  &protocol.ServerTelemetry{ServerProcessingMs: 40, TransferMs: 12},
		},
		{
			ActionName:       "POST /checkout",
			Success:          false,
			Duration:         150 * time.Millisecond,
			DependencyType:   "http",
			DependencyTarget: "api.example.test",
			DNSTime:          9 * time.Millisecond,
			ConnectTime:      16 * time.Millisecond,
			ServerTelemetry:  &protocol.ServerTelemetry{ServerProcessingMs: 60, TransferMs: 16},
		},
		{
			ActionName:       "GET /search",
			Success:          true,
			Duration:         80 * time.Millisecond,
			DependencyType:   "http",
			DependencyTarget: "search.example.test",
			ServerTelemetry:  &protocol.ServerTelemetry{ServerProcessingMs: 30, TransferMs: 10},
		},
	}, time.Second)

	dep := snap.ByDependencyTarget["api.example.test"]
	if dep == nil {
		t.Fatal("missing dependency-target aggregate for api.example.test")
	}
	if dep.DependencyType != "http" || dep.DependencyTarget != "api.example.test" {
		t.Fatalf("unexpected dependency aggregate metadata: %#v", dep)
	}
	if dep.Count != 3 || dep.Errors != 1 {
		t.Fatalf("dependency aggregate counts = %#v, want count 3 and errors 1", dep)
	}
	if dep.AvgDuration != 100*time.Millisecond {
		t.Fatalf("AvgDuration = %v, want 100ms", dep.AvgDuration)
	}
	assertWithinRange(t, "dependency P50", dep.P50, 70*time.Millisecond, 130*time.Millisecond)
	assertWithinRange(t, "dependency P95", dep.P95, 120*time.Millisecond, 180*time.Millisecond)
	assertWithinRange(t, "dependency P99", dep.P99, 130*time.Millisecond, 180*time.Millisecond)
	if dep.AvgDNSMs != 7 || dep.AvgConnectMs != 13.333333333333334 {
		t.Fatalf("dependency timing averages = dns:%v connect:%v, want 7 and 13.333...", dep.AvgDNSMs, dep.AvgConnectMs)
	}
	if dep.ServerTelemetry == nil || dep.ServerTelemetry.AvgServerProcessingMs != 40 || dep.ServerTelemetry.AvgTransferMs != 12 {
		t.Fatalf("dependency server telemetry = %#v, want avg server 40 and transfer 12", dep.ServerTelemetry)
	}
}

func TestMetricsCollector_BySOAPOperationAggregatesLiveMetrics(t *testing.T) {
	mc := NewMetricsCollector(100)
	snap := mc.computeSnapshot([]*protocol.Result{
		{
			ActionName: "SOAPRequest",
			Success:    true,
			Duration:   10 * time.Millisecond,
			Extractions: map[string]string{
				"soap_operation": "CreateOrder",
			},
			ServerTelemetry: &protocol.ServerTelemetry{ServerProcessingMs: 30, TransferMs: 10},
		},
		{
			ActionName: "SOAPRequest",
			Success:    false,
			Duration:   20 * time.Millisecond,
			Extractions: map[string]string{
				"soap_operation": "CreateOrder",
			},
			ServerTelemetry: &protocol.ServerTelemetry{ServerProcessingMs: 50, TransferMs: 20},
		},
	}, time.Second)

	operation := snap.BySOAPOperation["CreateOrder"]
	if operation == nil {
		t.Fatal("missing live SOAP operation metrics for CreateOrder")
	}
	if operation.Count != 2 || operation.Errors != 1 {
		t.Fatalf("operation metrics = %#v, want count=2 errors=1", operation)
	}
	if operation.ServerTelemetry == nil || operation.ServerTelemetry.AvgServerProcessingMs != 40 {
		t.Fatalf("operation server telemetry = %#v, want avg server processing 40", operation.ServerTelemetry)
	}
	if operation.ServerTelemetry.AvgTransferMs != 15 {
		t.Fatalf("AvgTransferMs = %v, want 15", operation.ServerTelemetry.AvgTransferMs)
	}
	if snap.ByAction["SOAPRequest"] == nil {
		t.Fatal("expected original action aggregation to remain available")
	}
}

func TestMetricsCollector_PercentilesViaTDigest(t *testing.T) {
	mc := NewMetricsCollector(100)
	mc.Start(50 * time.Millisecond)
	defer mc.Stop()

	// Feed 100 business transactions with durations 1ms..100ms
	for i := 1; i <= 100; i++ {
		mc.RecordTransaction("test", time.Duration(i)*time.Millisecond, true, "")
	}

	time.Sleep(150 * time.Millisecond)

	snap, ok := mc.LatestSnapshot()
	if !ok {
		t.Fatal("expected snapshot")
	}

	// T-digest estimates — allow reasonable tolerance
	assertWithinRange(t, "P50", snap.P50, 40*time.Millisecond, 60*time.Millisecond)
	assertWithinRange(t, "P90", snap.P90, 85*time.Millisecond, 95*time.Millisecond)
	assertWithinRange(t, "P99", snap.P99, 95*time.Millisecond, 105*time.Millisecond)
	if snap.P50 > snap.P90 || snap.P90 > snap.P99 {
		t.Errorf("percentiles not monotonic: P50=%v P90=%v P99=%v", snap.P50, snap.P90, snap.P99)
	}
}

func assertWithinRange(t *testing.T, name string, got, lo, hi time.Duration) {
	t.Helper()
	if got < lo || got > hi {
		t.Errorf("%s = %v, want between %v and %v", name, got, lo, hi)
	}
}

func TestComputeSnapshotPerGroupActiveVUsers(t *testing.T) {
	mc := NewMetricsCollector(100)
	mc.SetGroupTargetTPS("C", 4)
	// Group C is inactive and produced no results in this window. Its explicit
	// zero must still be surfaced rather than treated as missing telemetry, and
	// it must retain its configured throughput target and gap.
	mc.SetGroupActiveVUsersProvider(func() map[string]int {
		return map[string]int{"A": 7, "B": 3, "C": 0}
	})
	results := []*protocol.Result{
		{BusinessTransaction: true, Success: true, Group: "A", Duration: 10 * time.Millisecond},
		{BusinessTransaction: true, Success: true, Group: "B", Duration: 20 * time.Millisecond},
	}
	snap := mc.computeSnapshot(results, time.Second)
	if snap.ActiveVUsers != 10 {
		t.Fatalf("global active vusers = %d, want sum of authoritative group sample (10)", snap.ActiveVUsers)
	}

	if snap.ByGroup["A"] == nil || snap.ByGroup["A"].ActiveVUsers != 7 || !snap.ByGroup["A"].ActiveVUsersReported {
		t.Fatalf("group A active vusers = %#v, want 7", snap.ByGroup["A"])
	}
	if snap.ByGroup["B"] == nil || snap.ByGroup["B"].ActiveVUsers != 3 || !snap.ByGroup["B"].ActiveVUsersReported {
		t.Fatalf("group B active vusers = %#v, want 3", snap.ByGroup["B"])
	}
	if snap.ByGroup["C"] == nil || snap.ByGroup["C"].ActiveVUsers != 0 || !snap.ByGroup["C"].ActiveVUsersReported {
		t.Fatalf("group C active vusers = %#v, want reported zero", snap.ByGroup["C"])
	}
	if snap.ByGroup["C"].TargetTPS != 4 {
		t.Fatalf("group C target TPS = %v, want 4", snap.ByGroup["C"].TargetTPS)
	}
	if !snap.ByGroup["C"].ThroughputTargetMissed || snap.ByGroup["C"].ThroughputGapPct != 100 {
		t.Fatalf("group C throughput gap = (%v, %v), want missed at 100%%",
			snap.ByGroup["C"].ThroughputTargetMissed, snap.ByGroup["C"].ThroughputGapPct)
	}
}

func TestComputeSnapshotAggregatesCollidingGroupActiveVUsers(t *testing.T) {
	mc := NewMetricsCollector(10)
	prefix := strings.Repeat("g", maxMetricLabelBytes)
	mc.SetGroupActiveVUsersProvider(func() map[string]int {
		return map[string]int{prefix + "-one": 2, prefix + "-two": 3}
	})
	snap := mc.computeSnapshot(nil, time.Second)
	group := snap.ByGroup[prefix]
	if group == nil || !group.ActiveVUsersReported || group.ActiveVUsers != 5 {
		t.Fatalf("colliding group gauge = %#v, want reported aggregate 5", group)
	}
	if snap.ActiveVUsers != 5 {
		t.Fatalf("global active vusers = %d, want 5", snap.ActiveVUsers)
	}
}

func TestComputeSnapshotGlobalActiveVUsersIncludesGroupsBeyondDimensionLimit(t *testing.T) {
	mc := NewMetricsCollector(10)
	active := make(map[string]int, maxMetricDimensionEntries+1)
	for i := 0; i < maxMetricDimensionEntries+1; i++ {
		active[fmt.Sprintf("group-%04d", i)] = 1
	}
	mc.SetGroupActiveVUsersProvider(func() map[string]int { return active })

	snap := mc.computeSnapshot(nil, time.Second)
	if snap.ActiveVUsers != int64(len(active)) {
		t.Fatalf("global active vusers = %d, want all %d provider entries", snap.ActiveVUsers, len(active))
	}
	if len(snap.ByGroup) != maxMetricDimensionEntries {
		t.Fatalf("bounded group dimensions = %d, want %d", len(snap.ByGroup), maxMetricDimensionEntries)
	}
}

func TestActiveVUserCountByGroup(t *testing.T) {
	mge := &MultiGroupEngine{
		groups:       []GroupConfig{{Name: "A"}, {Name: "B"}, {Name: "C"}},
		vuserGroups:  map[int]string{1: "A", 2: "A", 3: "B"},
		vuserRunning: map[int]struct{}{1: {}, 2: {}, 3: {}},
	}
	counts := mge.activeVUserCountByGroup()
	if counts["A"] != 2 || counts["B"] != 1 || counts["C"] != 0 {
		t.Fatalf("per-group active counts = %#v, want A:2 B:1 C:0", counts)
	}
	if _, ok := counts["C"]; !ok {
		t.Fatalf("inactive configured group missing from counts: %#v", counts)
	}
}

func TestGroupMetricsReportedZeroSurvivesJSONRoundTrip(t *testing.T) {
	original := MetricsSnapshot{ByGroup: map[string]*GroupMetrics{
		"idle": {Name: "idle", ActiveVUsers: 0, ActiveVUsersReported: true},
	}}
	payload, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	var restored MetricsSnapshot
	if err := json.Unmarshal(payload, &restored); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	group := restored.ByGroup["idle"]
	if group == nil || group.ActiveVUsers != 0 || !group.ActiveVUsersReported {
		t.Fatalf("round-tripped idle group = %#v, want authoritative zero", group)
	}
}

func TestComputeSnapshotNetworkPhasesAndErrorClasses(t *testing.T) {
	mc := NewMetricsCollector(100)
	base := protocol.Result{
		BusinessTransaction: true,
		Duration:            150 * time.Millisecond,
		DNSTime:             12 * time.Millisecond,
		ConnectTime:         24 * time.Millisecond,
		TLSTime:             36 * time.Millisecond,
		TTFB:                90 * time.Millisecond,
	}
	success := base
	success.Success = true
	httpErr := base
	httpErr.StatusCode = 500
	httpErr.ErrorMessage = "boom"
	timeoutErr := base
	timeoutErr.ErrorMessage = "context deadline exceeded"

	snap := mc.computeSnapshot([]*protocol.Result{&success, &httpErr, &timeoutErr}, time.Second)

	if snap.Transactions != 1 || snap.Errors != 2 {
		t.Fatalf("transactions=%d errors=%d, want 1 and 2", snap.Transactions, snap.Errors)
	}
	if snap.AvgDNSMs != 12 || snap.AvgConnectMs != 24 || snap.AvgTLSMs != 36 || snap.AvgTTFBMs != 90 {
		t.Fatalf("phase averages dns=%v connect=%v tls=%v ttfb=%v", snap.AvgDNSMs, snap.AvgConnectMs, snap.AvgTLSMs, snap.AvgTTFBMs)
	}
	if snap.AvgWaitMs != 60 { // 150 - 90
		t.Fatalf("avg wait = %v, want 60", snap.AvgWaitMs)
	}
	if snap.ErrorsByClass[ErrorClassHTTP5xx] != 1 || snap.ErrorsByClass[ErrorClassTimeout] != 1 {
		t.Fatalf("errors by class = %#v, want one http_5xx and one timeout", snap.ErrorsByClass)
	}
}

package engine

import (
	"testing"
	"time"

	"github.com/jasonczech/stormrunner/pkg/protocol"
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

func TestMetricsCollector_RecordAndCount(t *testing.T) {
	mc := NewMetricsCollector(100)

	mc.Record(&protocol.Result{Success: true, ActionName: "a"})
	mc.Record(&protocol.Result{Success: false, ActionName: "b"})
	mc.Record(&protocol.Result{Success: true, ActionName: "c"})

	if got := mc.TotalTransactions(); got != 3 {
		t.Errorf("TotalTransactions = %d, want 3", got)
	}
	if got := mc.TotalErrors(); got != 1 {
		t.Errorf("TotalErrors = %d, want 1", got)
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
	if snap.Transactions != 10 {
		t.Errorf("transactions = %d, want 10", snap.Transactions)
	}
	if snap.Errors != 0 {
		t.Errorf("errors = %d, want 0", snap.Errors)
	}
	if snap.P50 == 0 {
		t.Error("P50 should be non-zero")
	}
	if snap.P90 == 0 {
		t.Error("P90 should be non-zero")
	}
	if snap.P99 == 0 {
		t.Error("P99 should be non-zero")
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

func TestMetricsCollector_ByAction(t *testing.T) {
	mc := NewMetricsCollector(100)
	mc.Start(50 * time.Millisecond)
	defer mc.Stop()

	mc.Record(&protocol.Result{ActionName: "login", Success: true, Duration: 10 * time.Millisecond, BytesSent: 100, BytesReceived: 500})
	mc.Record(&protocol.Result{ActionName: "login", Success: false, Duration: 20 * time.Millisecond, ErrorMessage: "timeout"})
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

	mc.Record(&protocol.Result{ActionName: "a", Group: "buyers", Success: true, Duration: 10 * time.Millisecond})
	mc.Record(&protocol.Result{ActionName: "b", Group: "buyers", Success: true, Duration: 20 * time.Millisecond})
	mc.Record(&protocol.Result{ActionName: "c", Group: "sellers", Success: true, Duration: 5 * time.Millisecond})

	time.Sleep(150 * time.Millisecond)

	snap, _ := mc.LatestSnapshot()
	buyers, ok := snap.ByGroup["buyers"]
	if !ok {
		t.Fatal("missing group 'buyers'")
	}
	if buyers.Transactions != 2 {
		t.Errorf("buyers transactions = %d, want 2", buyers.Transactions)
	}

	sellers, ok := snap.ByGroup["sellers"]
	if !ok {
		t.Fatal("missing group 'sellers'")
	}
	if sellers.Transactions != 1 {
		t.Errorf("sellers transactions = %d, want 1", sellers.Transactions)
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
				DeviceTier:            "low",
				FCPMs:                 120,
				LCPMs:                 220,
				CLSScore:              0.02,
				INPMs:                 45,
				BlockingResourceCount: 2,
				LongTaskCount:         1,
				JSHeapUsedMB:          12,
			},
		},
		{
			ActionName: "page-2",
			Success:    true,
			Duration:   20 * time.Millisecond,
			ClientMetrics: &protocol.ClientMetrics{
				DeviceProfile:         "iphone_se",
				DeviceTier:            "low",
				FCPMs:                 180,
				LCPMs:                 280,
				CLSScore:              0.04,
				INPMs:                 55,
				BlockingResourceCount: 4,
				LongTaskCount:         3,
				JSHeapUsedMB:          18,
			},
		},
		{
			ActionName: "page-3",
			Success:    true,
			Duration:   15 * time.Millisecond,
			ClientMetrics: &protocol.ClientMetrics{
				DeviceProfile:         "pixel_8",
				DeviceTier:            "high",
				FCPMs:                 90,
				LCPMs:                 160,
				CLSScore:              0.01,
				INPMs:                 35,
				BlockingResourceCount: 1,
				LongTaskCount:         0,
				JSHeapUsedMB:          10,
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
	if iphone.AvgFCPMs != 150 {
		t.Fatalf("AvgFCPMs = %v, want 150", iphone.AvgFCPMs)
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

	_, ok = snap.ByAction["tx:iteration_1"]
	if !ok {
		t.Error("expected synthetic transaction in ByAction")
	}
}

func TestMetricsCollector_ByActionCapturesDependencyAndCorrelationMetadata(t *testing.T) {
	mc := NewMetricsCollector(100)
	mc.Start(50 * time.Millisecond)
	defer mc.Stop()

	mc.Record(&protocol.Result{ActionName: "login", Success: true, Duration: 10 * time.Millisecond, DependencyType: "http", DependencyTarget: "api.example.test", CorrelationID: "corr-1"})
	mc.Record(&protocol.Result{ActionName: "login", Success: true, Duration: 20 * time.Millisecond, DependencyType: "http", DependencyTarget: "api.example.test", CorrelationID: "corr-2"})
	mc.Record(&protocol.Result{ActionName: "login", Success: true, Duration: 25 * time.Millisecond, DependencyType: "http", DependencyTarget: "api.example.test", CorrelationID: "corr-2"})

	time.Sleep(150 * time.Millisecond)

	snap, ok := mc.LatestSnapshot()
	if !ok {
		t.Fatal("expected snapshot")
	}
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

func TestMetricsCollector_PercentilesViaTDigest(t *testing.T) {
	mc := NewMetricsCollector(100)
	mc.Start(50 * time.Millisecond)
	defer mc.Stop()

	// Feed 100 results with durations 1ms..100ms
	for i := 1; i <= 100; i++ {
		mc.Record(&protocol.Result{
			ActionName: "test",
			Success:    true,
			Duration:   time.Duration(i) * time.Millisecond,
		})
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

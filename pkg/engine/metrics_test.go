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

func TestMetricsCollector_TracksThroughputTargetGap(t *testing.T) {
	mc := NewMetricsCollector(100)
	mc.SetTargetTPS(8)
	mc.SetGroupTargetTPS("buyers", 6)

	snap := mc.computeSnapshot([]*protocol.Result{
		{ActionName: "a", Group: "buyers", Success: true, Duration: 10 * time.Millisecond},
		{ActionName: "b", Group: "buyers", Success: true, Duration: 12 * time.Millisecond},
		{ActionName: "c", Group: "buyers", Success: true, Duration: 14 * time.Millisecond},
		{ActionName: "d", Group: "buyers", Success: true, Duration: 16 * time.Millisecond},
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

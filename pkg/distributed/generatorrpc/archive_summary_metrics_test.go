package generatorrpc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
)

func TestGeneratorArchiveIntervalSummaryPersistsNestedFleetEvidence(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	writer, err := newArchiveRecorder("summary-evidence-generator", "summary-evidence-run", "Summary evidence")
	if err != nil {
		t.Fatalf("newArchiveRecorder: %v", err)
	}
	collectedAt := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	publishedAt := collectedAt.Add(125 * time.Millisecond)
	batch := &pb.MetricsBatch{
		TestId:          "summary-evidence-run",
		TimestampUnixMs: publishedAt.UnixMilli(),
		WindowMs:        2_000,
		Stats:           &pb.GeneratorStats{ActiveVusers: 4},
		FleetMetrics:    &pb.MetricAggregate{Count: 8, Errors: 2, AvgMs: 175, P50Ms: 150, P90Ms: 240, P95Ms: 260, P99Ms: 300},
		Groups: []*pb.GroupMetric{{
			Name: "buyers", ActiveVusers: 4,
			Metrics: &pb.MetricAggregate{Count: 8, Errors: 2, AvgMs: 175, P50Ms: 150, P90Ms: 240, P95Ms: 260, P99Ms: 300, Tps: 3},
		}},
		BrowserMetrics: &pb.BrowserMetrics{Samples: 2, AvgLcpMs: 320, AvgInpMs: 75},
		BrowserDeviceMetrics: []*pb.BrowserDeviceMetrics{{
			DeviceProfile: "iphone_se", DevicePlatform: "ios", DeviceTier: "low", BrowserEngine: "webkit",
			Metrics: &pb.BrowserMetrics{Samples: 2, AvgLcpMs: 320, AvgInpMs: 75},
		}},
		Telemetry: &pb.MetricsBatchTelemetry{
			DroppedResults: 3, SheddedArrivals: 2, DroppedSubscriberBatches: 1,
			CollectedAtUnixMs: collectedAt.UnixMilli(), PublishedAtUnixMs: publishedAt.UnixMilli(),
			GeneratorProcessingLagMs: 125, TransportLagMs: 40, SampleAgeMs: 165,
		},
	}
	(&Service{}).recordMetricsBatch("summary-evidence-run", writer, batch)
	record, err := writer.Finalize("COMPLETE", publishedAt, "")
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(record.ArchivePath, "interval_summaries.jsonl"))
	if err != nil {
		t.Fatalf("read interval summaries: %v", err)
	}
	var summary archiveIntervalSummary
	if err := json.Unmarshal(data, &summary); err != nil {
		t.Fatalf("decode interval summary: %v", err)
	}
	buyers := summary.Groups["buyers"]
	if buyers == nil || buyers.VUsers != 4 || buyers.Transactions != 6 || buyers.Errors != 2 || buyers.TPS != 3 || buyers.P95Ms != 260 || buyers.PercentileQuality != "source_generator" {
		t.Fatalf("archived group metrics = %#v", summary.Groups)
	}
	if summary.Browser == nil || summary.Browser.Samples != 2 || summary.Browser.AvgLCPMs != 320 || summary.Browser.AvgINPMs != 75 {
		t.Fatalf("archived browser metrics = %#v", summary.Browser)
	}
	device := summary.BrowserByDevice["iphone_se"]
	if device == nil || device.DevicePlatform != "ios" || device.BrowserEngine != "webkit" || device.Samples != 2 || device.AvgLCPMs != 320 {
		t.Fatalf("archived browser device metrics = %#v", summary.BrowserByDevice)
	}
	if summary.Telemetry == nil || summary.Telemetry.DroppedResults != 3 || summary.Telemetry.SheddedArrivals != 2 ||
		summary.Telemetry.DroppedSubscriberBatches != 1 || !summary.Telemetry.CollectedAt.Equal(collectedAt) ||
		!summary.Telemetry.GeneratorPublishedAt.Equal(publishedAt) || summary.Telemetry.GeneratorProcessingLagMs != 125 {
		t.Fatalf("archived telemetry = %#v", summary.Telemetry)
	}
}

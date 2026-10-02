package api

import (
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/engine"
	"strings"
	"testing"
)

func TestBaselineTelemetryCoverageAndMissing(t *testing.T) {
	rows := []IntervalSummaryRecord{{TelemetryStatus: &TelemetryRunHealth{Status: "healthy"}}, {TelemetryStatus: &TelemetryRunHealth{Status: "breached"}, Telemetry: &TelemetryHealth{DroppedResults: 3, SampleAgeMs: 100}}, {TelemetryStatus: &TelemetryRunHealth{Status: "unknown"}}, {}}
	e := baselineTelemetryEvidence("run", rows)
	if e.HealthIntervals != 2 || *e.HealthCoveragePct != 50 || *e.BreachedIntervalPct != 50 || e.Summary.DroppedResults != 3 || len(e.Warnings) != 2 {
		t.Fatalf("%+v", e)
	}
	missing := baselineTelemetryEvidence("legacy", nil)
	if missing.Summary != nil || missing.BreachedIntervalPct != nil || missing.HealthCoveragePct != nil {
		t.Fatal("absence became healthy zero")
	}
	healthy := baselineTelemetryEvidence("healthy", rows[:1])
	if *healthy.BreachedIntervalPct != 0 {
		t.Fatal("observed zero lost")
	}
	if healthy.DroppedResults != nil || healthy.MaxSampleAgeMs != nil {
		t.Fatal("health-only evidence invented zero metrics")
	}
	raw := baselineTelemetryEvidence("raw", []IntervalSummaryRecord{{Telemetry: &TelemetryHealth{}}})
	if raw.Summary.Status != "unavailable" || raw.BreachedIntervalPct != nil || raw.DroppedResults != nil || raw.MaxSampleAgeMs != nil {
		t.Fatal("raw evidence availability lost")
	}
	if !strings.Contains(e.Warnings[0], "policies") {
		t.Fatal("missing policy qualification")
	}
}

func TestTelemetryCumulativeCountersDoNotDoubleCount(t *testing.T) {
	rows := []IntervalSummaryRecord{{Telemetry: &TelemetryHealth{DroppedResults: 2, WebsocketDroppedMessages: 3, WebsocketDroppedEvents: 4}}, {Telemetry: &TelemetryHealth{DroppedResults: 2, WebsocketDroppedMessages: 3, WebsocketDroppedEvents: 4}}, {Telemetry: &TelemetryHealth{DroppedResults: 1}}}
	e := baselineTelemetryEvidence("run", rows)
	if *e.DroppedResults != 2 || e.Summary.WebsocketDroppedMessages != 3 || e.Summary.WebsocketDroppedEvents != 4 || len(e.Warnings) != 3 {
		t.Fatalf("cumulative counter multiplied %+v", e)
	}
}

func TestBaselineTelemetryReportAvailability(t *testing.T) {
	zero := int64(0)
	value := 0.0
	e := BaselineTelemetryEvidence{RunID: "run", DroppedResults: &zero, BreachedIntervalPct: &value}
	lines := strings.Join(baselineTelemetryLines(e), "\n")
	if !strings.Contains(lines, "peak observed drop counter 0") || !strings.Contains(lines, "maximum sample age Unavailable") || !strings.Contains(lines, "breached classified intervals 0%") {
		t.Fatal(lines)
	}
}

func TestArchiveTelemetryMissingClassificationUnavailable(t *testing.T) {
	s := RunArchiveTelemetrySummary{}
	mergeRunArchiveTelemetrySummary(&s, IntervalSummaryRecord{Telemetry: &TelemetryHealth{DroppedResults: 2}})
	if finalizeRunArchiveTelemetrySummary(s).Status != "unavailable" {
		t.Fatal("raw counters inferred healthy status")
	}
}

func TestBaselineTelemetryLegacyMetadataCannotImplyZeroDrops(t *testing.T) {
	legacy := buildLiveMetricsFromDistributedBatch(&ManagedTest{}, generatorrpc.MetricsBatch{}, 0, nil)
	if legacy.Telemetry.DroppedResultsAvailable {
		t.Fatal("legacy peer acquired counter availability")
	}
	e := baselineTelemetryEvidence("legacy", []IntervalSummaryRecord{{Telemetry: legacy.Telemetry}})
	if e.DroppedResults != nil {
		t.Fatal("metadata-only counter became zero")
	}
	real := buildLiveMetricsFromDistributedBatch(&ManagedTest{}, generatorrpc.MetricsBatch{Telemetry: &generatorrpc.MetricsTelemetry{}}, 0, nil)
	e = baselineTelemetryEvidence("real", []IntervalSummaryRecord{{Telemetry: real.Telemetry}})
	if e.DroppedResults == nil || *e.DroppedResults != 0 {
		t.Fatal("measured zero lost")
	}
	local := buildLiveMetricsFromSnapshot(engine.MetricsSnapshot{Telemetry: &engine.TelemetryMetrics{}}, nil)
	if !local.Telemetry.DroppedResultsAvailable {
		t.Fatal("local source availability lost")
	}
}

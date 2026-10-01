package api

import (
	"encoding/json"
	"fmt"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMessagingAggregateSurvivesRawSheddingWithoutDoubleCounting(t *testing.T) {
	var s *protocol.MessagingSummary
	protocol.MergeMessagingTelemetry(&s, &protocol.ServerTelemetry{MessagesSent: 10, PartitionID: "0"}, "topic")
	m := txMetricsFromAggregate(generatorrpc.MetricAggregate{Name: "send", Count: 10, Messaging: s, MessagingVersion: 1})
	mergeDistributedAggregateTransactionDetails(m, generatorrpc.TransactionMetric{Name: "send", ServerTelemetry: &generatorrpc.ServerTelemetry{MessagesSent: 1, PartitionID: "0"}})
	if m.ServerTelemetry.Messaging.Dimensions[0].Sent != 10 {
		t.Fatal("retained raw sample counted twice")
	}
	legacy := txMetricsFromAggregate(generatorrpc.MetricAggregate{Name: "send", Count: 10})
	mergeDistributedAggregateTransactionDetails(legacy, generatorrpc.TransactionMetric{Name: "send", DependencyTarget: "topic", DependencyType: "kafka_topic", ServerTelemetry: &generatorrpc.ServerTelemetry{MessagesSent: 1, PartitionID: "0"}})
	if !legacy.ServerTelemetry.Messaging.Incomplete {
		t.Fatal("legacy sample coverage invented")
	}
	clone := cloneTxMetricsMap(map[string]*TxMetrics{"send": m})
	clone["send"].ServerTelemetry.Messaging.Dimensions[0].Sent = 999
	if m.ServerTelemetry.Messaging.Dimensions[0].Sent != 10 {
		t.Fatal("live snapshot alias")
	}
}
func TestMessagingQualifiedPartitionGoal(t *testing.T) {
	limit := 50.0
	var s *protocol.MessagingSummary
	protocol.MergeMessagingTelemetry(&s, &protocol.ServerTelemetry{MessagesSent: 9, PartitionID: "0"}, "topic")
	live := &LiveMetrics{Transactions: map[string]*TxMetrics{"send": {ServerTelemetry: &ServerTelemetrySummary{Messaging: s}}}}
	if hasMessagingPartitionSkewBreach(live, &limit) {
		t.Fatal("one partition scored")
	}
	protocol.MergeMessagingTelemetry(&s, &protocol.ServerTelemetry{MessagesSent: 1, PartitionID: "1"}, "topic")
	if !hasMessagingPartitionSkewBreach(live, &limit) {
		t.Fatal("qualified observed skew not detected")
	}
	s.Incomplete = true
	if hasMessagingPartitionSkewBreach(live, &limit) {
		t.Fatal("incomplete evidence scored")
	}
}
func TestMessagingArchiveReportHasSameObservedMoments(t *testing.T) {
	ack := 4.0
	var stats RunArchiveActionStats
	q := &archiveQuantileAccumulator{}
	stats = mergeRunArchiveActionStats(stats, q, TransactionEvent{ActionName: "receive", Success: true, DurationMs: 10, DependencyTarget: "topic", DependencyType: "kafka_topic", ServerTelemetry: &protocol.ServerTelemetry{MessagesReceived: 1, SubscriptionID: "<sub>", PartitionID: "0", ConsumerLagObserved: true, AcknowledgementMs: &ack}})
	report := &RunArchiveReport{ByAction: map[string]RunArchiveActionStats{"receive": stats}}
	text := strings.Join(protocolReportLines(report), "\n")
	if !strings.Contains(text, "subscription <sub>") || !strings.Contains(text, "mean 0.000") || !strings.Contains(text, "mean 4.000") {
		t.Fatal("report discarded observed zero or settlement", text)
	}
	html, err := protocolDiagnosticReportSection().HTML(&runArchiveHTMLReportData{Report: report})
	if err != nil || strings.Contains(string(html), "<sub>") {
		t.Fatal("report identity not escaped", err)
	}
}

func TestMessagingActionPopulationArchivesAndScopes(t *testing.T) {
	var s *protocol.MessagingSummary
	protocol.MergeMessagingTelemetry(&s, &protocol.ServerTelemetry{MessagesSent: 9, PartitionID: "0"}, "topic")
	snapshot := engine.MetricsSnapshot{ByAction: map[string]*engine.ActionMetrics{"produce": {Name: "produce", Count: 9, ServerTelemetry: &engine.ServerTelemetrySummary{Messaging: s}}}}
	live := buildLiveMetricsFromSnapshot(snapshot, nil)
	if live.Messaging["produce"] == nil {
		t.Fatal("business-transaction view hid action evidence")
	}
	clone := cloneLiveMetrics(live)
	clone.Messaging["produce"].Dimensions[0].Sent = 2
	if live.Messaging["produce"].Dimensions[0].Sent != 9 {
		t.Fatal("action map aliases")
	}
	if filtered := filterLiveMetricsForGroups(live, groupFilterQuery{Explicit: true}); filtered.Messaging != nil {
		t.Fatal("global evidence leaked into group-filtered view")
	}
	record := IntervalSummaryRecord{Messaging: live.Messaging}
	rows := toParquetSummaries([]IntervalSummaryRecord{record})
	if len(rows) != 1 || !strings.Contains(rows[0].MessagingJSON, `"produce"`) {
		t.Fatal("Parquet omitted messaging")
	}
	restored := intervalSummaryRecordToLiveMetrics(storage.IntervalSummaryRecord{Messaging: live.Messaging})
	if restored.Messaging["produce"].Dimensions[0].Sent != 9 {
		t.Fatal("restart lost action evidence")
	}
	limit := 50.0
	live.Messaging["other"] = &protocol.MessagingSummary{Dimensions: []protocol.MessagingDimension{{Source: "topic", Partition: "1", Operations: 1, Sent: 1}}}
	if !hasMessagingPartitionSkewBreach(live, &limit) {
		t.Fatal("action intersection not pooled by source")
	}
	live.Telemetry = &TelemetryHealth{MissingMetricWindows: 1}
	if hasMessagingPartitionSkewBreach(live, &limit) {
		t.Fatal("missing window scored")
	}
}

func TestMessagingArchiveMergeMarksLegacyCoverageIncomplete(t *testing.T) {
	root := t.TempDir()
	var s *protocol.MessagingSummary
	protocol.MergeMessagingTelemetry(&s, &protocol.ServerTelemetry{MessagesSent: 9, PartitionID: "0"}, "topic")
	bundles := []remoteArchiveBundle{}
	for i, messaging := range []map[string]*protocol.MessagingSummary{{"send": s}, nil} {
		path := filepath.Join(root, fmt.Sprintf("%d.jsonl", i))
		data, err := json.Marshal(IntervalSummaryRecord{Timestamp: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), TestID: "run", TotalTx: 1, Messaging: messaging})
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, append(data, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
		bundles = append(bundles, remoteArchiveBundle{target: fmt.Sprint(i), summariesPath: path, manifest: RunArchiveManifest{IntervalSummaryCount: 1}})
	}
	destination := filepath.Join(root, "merged.jsonl")
	if _, err := mergeRemoteArchiveIntervalSummaries(destination, bundles); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	var record IntervalSummaryRecord
	if err = json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	if got := record.Messaging["send"]; got == nil || !got.Incomplete || got.Dimensions[0].Sent != 9 {
		t.Fatalf("legacy fleet coverage: %+v", got)
	}
}

func TestMessagingGoalCapabilityAdmission(t *testing.T) {
	limit := 50.0
	for _, tc := range []struct {
		goal     *GoalConfig
		features []string
		bad      bool
	}{{nil, nil, false}, {&GoalConfig{MaxPartitionSkewPct: &limit}, nil, true}, {&GoalConfig{MaxPartitionSkewPct: &limit}, []string{generatorrpc.FeatureMessagingRollupV1}, false}} {
		if bad := messagingGoalCapabilityError(tc.goal, tc.features) != nil; bad != tc.bad {
			t.Fatalf("capability rejection %v want %v", bad, tc.bad)
		}
	}
}

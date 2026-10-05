package generatorrpc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func testMessagingProgress(t *testing.T, group string) *protocol.MessagingProgressSnapshot {
	t.Helper()
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	age := int64(0)
	position := protocol.MessagingProgressPosition{Sequence: "9007199254740993", ProviderOffset: "opaque:offset", State: "observed", ObservedAt: now, ReceivedAt: now, ObservationAgeMs: &age}
	s := &protocol.MessagingProgressSnapshot{Version: protocol.MessagingProgressVersion, CapturedAt: now, Lifecycle: "running", Rows: []protocol.MessagingProgressRow{{
		Group: group, Protocol: "eventhubs", Mode: "processor", SourceID: "source-id", Source: "hub", Subscription: "load", SessionID: "session", Partition: "0", Generation: "generation", Revision: "1", CaptureRevision: "1", UpdatedAt: now, CapturedAt: now, ExpectedPartitions: 1, InventoryComplete: true, Ownership: "owned", OwnershipVerifiedAt: now, OwnershipVerifiedAgeMs: &age, Acquired: 1, Delivered: 3,
		DeliveredPosition: position, CheckpointPosition: protocol.MessagingProgressPosition{State: "missing"}, TailPosition: protocol.MessagingProgressPosition{State: "disabled"}, Checkpoint: protocol.MessagingCheckpointProgress{Duration: protocol.MessagingDurationSummary{Metric: "checkpoint", Population: "checkpoint_operation"}},
	}}}
	if !s.Valid() {
		t.Fatal("invalid progress fixture")
	}
	return s
}

func TestMessagingProgressStandaloneWireAndArchiveRoundtrip(t *testing.T) {
	progress := testMessagingProgress(t, "processors")
	aggregates := metricsAggregateSet{MessagingProgress: progress}
	if !metricsAggregateSetHasEvidence(aggregates) {
		t.Fatal("zero-action progress has no evidence")
	}
	batch := (&Service{}).metricsBatchForMetrics("run", 7, GeneratorStats{}, nil, aggregates, false, nil, "paused", "", nil)
	if batch.GetMessagingProgressVersion() != 1 || batch.GetFleetMetrics() != nil || len(batch.GetTransactions()) != 0 {
		t.Fatal("progress coupled to action populations")
	}
	decoded, err := (&MetricsStream{stream: oneTimingBatchStream{batch: batch}}).Receive()
	if err != nil {
		t.Fatal(err)
	}
	if decoded.MessagingProgress.Rows[0].DeliveredPosition.Sequence != "9007199254740993" {
		t.Fatal("int64 sequence lost precision")
	}
	*decoded.MessagingProgress.Rows[0].DeliveredPosition.ObservationAgeMs = 5
	if *progress.Rows[0].DeliveredPosition.ObservationAgeMs != 0 {
		t.Fatal("wire clone aliases source")
	}
	summary := archiveIntervalSummary{MessagingProgress: messagingProgressFromJSON(batch.GetMessagingProgressJson(), 1)}
	raw, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	var restored archiveIntervalSummary
	if err = json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	parquet := toParquetSummaries([]archiveIntervalSummary{restored})
	if got := messagingProgressFromJSON(parquet[0].MessagingProgressJSON, 1); got == nil || !got.Valid() || got.Rows[0].Delivered != 3 {
		t.Fatal("archive progress lost")
	}
}

func TestMessagingProgressWireRejectsMalformedAndOldPeers(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		version   uint32
		reason    string
	}{
		{"legacy", "", 0, ""}, {"empty versioned", "", 1, "malformed"}, {"unsupported", "{}", 2, "unsupported_version"}, {"oversized", strings.Repeat("x", protocol.MaxMessagingProgressBytes+1), 1, "oversized"}, {"null", "null", 1, "malformed"}, {"missing rows", `{"version":1,"captured_at":"2026-10-03T12:00:00Z"}`, 1, "malformed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := messagingProgressFromJSON(tc.raw, tc.version)
			if tc.name == "legacy" {
				if s != nil {
					t.Fatal("legacy absence became observed")
				}
				return
			}
			if s == nil || !s.Valid() || !s.Incomplete || s.UnavailableReason != tc.reason || len(s.Rows) != 0 {
				t.Fatalf("invalid safe marker: %+v", s)
			}
		})
	}
	validEmpty := &protocol.MessagingProgressSnapshot{Version: 1, CapturedAt: time.Now().UTC(), Rows: []protocol.MessagingProgressRow{}}
	if s := messagingProgressFromJSON(messagingProgressJSON(validEmpty), 1); s == nil || !s.Valid() || s.Incomplete || s.Rows == nil {
		t.Fatal("observed empty inventory lost")
	}
}

func TestMessagingProgressMergeFiltersAndRetainsCumulativeGauge(t *testing.T) {
	a := testMessagingProgress(t, "a")
	b := testMessagingProgress(t, "b")
	b.Rows[0].SessionID = "other"
	merged := mergeMetricsAggregateSets(map[string]metricsAggregateSet{"a": {MessagingProgress: a}, "b": {MessagingProgress: b}, "http": {}})
	if len(merged.MessagingProgress.Rows) != 2 {
		t.Fatal("distinct groups lost")
	}
	filtered := (&sourcePlanExecution{processorGroups: map[string]bool{"b": true}}).groupSnapshot(engine.MetricsSnapshot{MessagingProgress: merged.MessagingProgress}, "b")
	if len(filtered.MessagingProgress.Rows) != 1 || filtered.MessagingProgress.Rows[0].Group != "b" {
		t.Fatal("source plan group evidence leaked")
	}
	if got := (&sourcePlanExecution{processorGroups: map[string]bool{"b": true}}).groupSnapshot(engine.MetricsSnapshot{MessagingProgress: merged.MessagingProgress}, "http"); got.MessagingProgress != nil {
		t.Fatal("unrelated source-plan group acquired processor evidence")
	}
	*filtered.MessagingProgress.Rows[0].DeliveredPosition.ObservationAgeMs = 8
	if *b.Rows[0].DeliveredPosition.ObservationAgeMs != 0 {
		t.Fatal("filtered pointer alias")
	}
	repeated := mergeMetricsAggregateSets(map[string]metricsAggregateSet{"a": {MessagingProgress: a}, "retry": {MessagingProgress: a}})
	if len(repeated.MessagingProgress.Rows) != 1 || repeated.MessagingProgress.Rows[0].Delivered != 3 {
		t.Fatal("cumulative retry was summed")
	}
	newer := a.Clone()
	newer.Rows[0].Revision = "2"
	newer.Rows[0].CaptureRevision = "2"
	newer.Rows[0].Delivered = 4
	repeated = mergeMetricsAggregateSets(map[string]metricsAggregateSet{"new": {MessagingProgress: newer}, "old": {MessagingProgress: a}})
	if repeated.MessagingProgress.Rows[0].Delivered != 4 {
		t.Fatal("older revision overwrote new evidence")
	}
	exec := &groupExecution{lastMetrics: &executionMetricsSnapshot{aggregates: metricsAggregateSet{MessagingProgress: a}}}
	got, ok := (&Service{publishInterval: time.Second}).collectExecutionMetrics(exec)
	if !ok || got.aggregates.MessagingProgress == nil || got.aggregates.MessagingProgress.Rows[0].Delivered != 3 {
		t.Fatal("idle progress disappeared")
	}
	storeExecutionMetrics(exec, got)
	*got.aggregates.MessagingProgress.Rows[0].DeliveredPosition.ObservationAgeMs = 9
	if *exec.lastMetrics.aggregates.MessagingProgress.Rows[0].DeliveredPosition.ObservationAgeMs != 0 {
		t.Fatal("stored metrics alias mutable progress")
	}
}

func TestMessagingProgressSameSnapshotTransactionWindowRetainsGauge(t *testing.T) {
	progress := testMessagingProgress(t, "processors")
	exec := &groupExecution{lastMetrics: &executionMetricsSnapshot{aggregates: metricsAggregateSet{MessagingProgress: progress}}, results: &transactionBuffer{transactions: []TransactionMetric{{Name: "read", Success: true}}, lastDrainAt: time.Now().Add(-time.Second)}}
	got, ok := (&Service{publishInterval: time.Second}).collectExecutionMetricsWithIdle(exec, false)
	if !ok || got.aggregates.MessagingProgress == nil || got.aggregates.MessagingProgress.Rows[0].Delivered != 3 || len(got.transactions) != 1 {
		t.Fatal("same-source result window erased cumulative progress")
	}
}

func TestMessagingProgressGeneratorRecorderPersistsIdleEvidence(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv(envGeneratorArchiveMinFree, "1")
	writer, err := newArchiveRecorder("generator", "progress-run", "Progress")
	if err != nil {
		t.Fatal(err)
	}
	progress := testMessagingProgress(t, "processors")
	batch := (&Service{}).metricsBatchForMetrics("progress-run", 7, GeneratorStats{}, nil, metricsAggregateSet{MessagingProgress: progress}, false, nil, "paused", "", nil)
	(&Service{}).recordMetricsBatch("progress-run", writer, batch)
	record, err := writer.Finalize("COMPLETE", time.Now().UTC(), "")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(record.ArchivePath, "interval_summaries.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var summary archiveIntervalSummary
	if err = json.Unmarshal(data, &summary); err != nil {
		t.Fatal(err)
	}
	if summary.TotalTx != 0 || summary.MessagingProgress == nil || summary.MessagingProgress.Rows[0].Delivered != 3 {
		t.Fatal("zero-action recorder omitted progress")
	}
}

func TestMessagingProgressWireCapabilityAdvertised(t *testing.T) {
	_, features := runtimeAdvertisement(context.Background(), false)
	if !containsProtocolName(features, FeatureMessagingProgressV1) {
		t.Fatal("available Event Hubs provider missing wire capability")
	}
}

func TestMessagingProgressSourcePlanUsesImmutableConsumerExpectation(t *testing.T) {
	processor := &dsl.Script{Protocol: "eventhubs", Config: dsl.ScriptConfig{EventHubs: dsl.EventHubsConfig{ConsumerMode: "processor", ProcessorMaxPartitions: 1}}, Actions: []dsl.Action{{Type: "eventhubs_consume"}}}
	producer := &dsl.Script{Protocol: "eventhubs", Config: processor.Config, Actions: []dsl.Action{{Type: "eventhubs_produce"}}}
	groups := map[string]*groupExecution{}
	for name, script := range map[string]*dsl.Script{"consume": processor, "produce": producer} {
		groups[name] = &groupExecution{stagedConfig: &engine.MultiGroupConfig{Duration: time.Second, RuntimePreflightValidated: true, Groups: []engine.GroupConfig{{Name: name, Script: script, VUsers: 1}}}}
	}
	owner, err := assembleSourcePlan(groups)
	if err != nil {
		t.Fatal(err)
	}
	if !owner.processorGroups["consume"] || owner.processorGroups["produce"] {
		t.Fatal("producer incorrectly expects processor progress")
	}
	empty := &protocol.MessagingProgressSnapshot{Version: 1, CapturedAt: time.Now().UTC(), Lifecycle: "pending", Rows: []protocol.MessagingProgressRow{}}
	if owner.groupSnapshot(engine.MetricsSnapshot{MessagingProgress: empty}, "consume").MessagingProgress == nil || owner.groupSnapshot(engine.MetricsSnapshot{MessagingProgress: empty}, "produce").MessagingProgress != nil {
		t.Fatal("pending empty inventory crossed population boundary")
	}
}

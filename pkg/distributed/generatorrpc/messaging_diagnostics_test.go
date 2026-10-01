package generatorrpc

import (
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"strings"
	"testing"
)

func TestMessagingAggregateRoundtripAndLegacyCoverage(t *testing.T) {
	var s *protocol.MessagingSummary
	protocol.MergeMessagingTelemetry(&s, &protocol.ServerTelemetry{MessagesSent: 9, PartitionID: "0"}, "topic")
	a := MetricAggregate{Name: "send", Count: 9, Messaging: s, MessagingVersion: 1}
	got := metricAggregateFromProto(metricAggregateToProto(&a))
	if got.MessagingVersion != 1 || got.Messaging == nil || got.Messaging.Dimensions[0].Sent != 9 {
		t.Fatalf("roundtrip: %+v", got)
	}
	merged := mergeMetricAggregates([]MetricAggregate{a, {Name: "send", Count: 1}})
	got = metricAggregateFromProto(metricAggregateToProto(merged))
	if got.Messaging == nil || !got.Messaging.Incomplete || got.Messaging.Dimensions[0].Sent != 9 {
		t.Fatal("old peer erased coverage or known observations", got)
	}
	if got := messagingSummaryFromJSON(strings.Repeat("x", maxMessagingSummaryBytes+1), 1); got == nil || !got.Incomplete {
		t.Fatal("oversized accepted")
	}
	if invalid := messagingSummaryFromJSON(`{"dimensions":[{"operations":-1}]}`, 1); !invalid.Incomplete || len(invalid.Dimensions) != 0 {
		t.Fatal("invalid counters accepted")
	}
}
func TestMessagingEngineProjectionAndAckClone(t *testing.T) {
	zero := 0.0
	raw := &protocol.ServerTelemetry{SubscriptionID: "sub", AcknowledgementMs: &zero}
	dto := cloneServerTelemetry(raw)
	*dto.AcknowledgementMs = 20
	if *raw.AcknowledgementMs != 0 {
		t.Fatal("ack pointer aliased")
	}
	batch := (&Service{}).metricsBatchForMetrics("run", 1, GeneratorStats{}, []TransactionMetric{{Name: "settle", ServerTelemetry: cloneServerTelemetry(raw)}}, metricsAggregateSet{}, false, nil, "running", "", nil)
	decoded, err := (&MetricsStream{stream: oneTimingBatchStream{batch: batch}}).Receive()
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Transactions[0].ServerTelemetry.AcknowledgementMs == nil || decoded.Transactions[0].ServerTelemetry.SubscriptionID != "sub" {
		t.Fatal("optional settlement measurement lost")
	}
	var s *protocol.MessagingSummary
	protocol.MergeMessagingTelemetry(&s, &protocol.ServerTelemetry{MessagesReceived: 1, ConsumerLagObserved: true}, "topic")
	aggregates := engineActionMetricsToAggregates(map[string]*engine.ActionMetrics{"receive": {Name: "receive", Count: 1, ServerTelemetry: &engine.ServerTelemetrySummary{Messaging: s}}})
	if len(aggregates) != 1 || aggregates[0].Messaging.Dimensions[0].Age.Count != 1 {
		t.Fatal("engine rollup discarded")
	}
}

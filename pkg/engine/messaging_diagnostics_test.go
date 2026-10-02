package engine

import (
	"github.com/jasonczech/apexload/pkg/protocol"
	"testing"
)

func TestMessagingLagUsesIndependentObservedDenominators(t *testing.T) {
	s := &ServerTelemetrySummary{}
	for _, sample := range []*protocol.ServerTelemetry{{MessagesSent: 1}, {MessagesReceived: 1, ConsumerLagObserved: true}, {MessagesReceived: 1, ConsumerLagObserved: true, ConsumerLagMs: 20, ConsumerOffsetLagObserved: true, ConsumerOffsetLag: 4}, {MessagesReceived: 1}} {
		mergeServerTelemetrySummary(s, sample, "topic")
	}
	finalizeServerTelemetrySummary(s)
	if s.ConsumerLagSamples != 2 || s.AvgConsumerLagMs != 10 || s.ConsumerOffsetLagSamples != 1 || s.AvgConsumerOffsetLag != 4 {
		t.Fatalf("diluted lag: %+v", s)
	}
	if !s.Messaging.Valid() {
		t.Fatal("invalid local rollup")
	}
}

func TestMessagingActionRollupRetainsProtocolAndExecutionGroup(t *testing.T) {
	metrics := &ActionMetrics{}
	for _, group := range []string{"first", "second"} {
		mergeActionResult(metrics, &protocol.Result{
			ActionName: "publish", Group: group, Success: true,
			DependencyType: "kafka_topic", DependencyTarget: "events",
			ServerTelemetry: &protocol.ServerTelemetry{MessagesSent: 1, PartitionID: "0"},
		})
	}
	summary := metrics.ServerTelemetry.Messaging
	if summary == nil || !summary.Valid() || summary.Incomplete || len(summary.Dimensions) != 2 {
		t.Fatalf("execution groups collapsed: %+v", summary)
	}
	for i, group := range []string{"first", "second"} {
		d := summary.Dimensions[i]
		if d.Group != group || d.Protocol != "kafka" || d.Source != "events" || d.Sent != 1 {
			t.Fatalf("rollup lost admitted scope: %+v", d)
		}
	}
}

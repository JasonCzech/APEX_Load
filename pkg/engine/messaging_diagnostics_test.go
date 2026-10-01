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

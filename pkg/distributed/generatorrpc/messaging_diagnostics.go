package generatorrpc

import (
	"encoding/json"
	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
)

const FeatureMessagingRollupV1 = "messaging_rollup_v1"
const maxMessagingSummaryBytes = 128 << 10

func cloneMessagingAck(v *float64) *float64 {
	if v == nil {
		return nil
	}
	c := *v
	return &c
}
func engineMessagingSummary(s *engine.ServerTelemetrySummary) *protocol.MessagingSummary {
	if s == nil {
		return nil
	}
	return s.Messaging.Clone()
}
func messagingSummaryJSON(s *protocol.MessagingSummary) string {
	if s == nil {
		return ""
	}
	if !s.Valid() {
		s = &protocol.MessagingSummary{Incomplete: true}
	}
	b, err := json.Marshal(s)
	if err != nil || len(b) > maxMessagingSummaryBytes {
		return `{"incomplete":true}`
	}
	return string(b)
}
func messagingSummaryFromJSON(raw string, version uint32) *protocol.MessagingSummary {
	if version != 1 || raw == "" {
		return nil
	}
	var s protocol.MessagingSummary
	if len(raw) > maxMessagingSummaryBytes || json.Unmarshal([]byte(raw), &s) != nil || !s.Valid() {
		return &protocol.MessagingSummary{Incomplete: true}
	}
	return &s
}

// MessagingTelemetry is a bounded-model adapter, not an inferred server timing.
func (t *ServerTelemetry) MessagingTelemetry() *protocol.ServerTelemetry {
	if t == nil {
		return nil
	}
	return &protocol.ServerTelemetry{SubscriptionID: t.SubscriptionID, AcknowledgementMs: cloneMessagingAck(t.AcknowledgementMs), PartitionID: t.PartitionID, MessagesSent: int(t.MessagesSent), MessagesReceived: int(t.MessagesReceived), ThrottledRequests: int(t.ThrottledRequests), RedeliveryCount: int(t.RedeliveryCount), ConsumerLagObserved: t.ConsumerLagObserved, ConsumerLagMs: t.ConsumerLagMs, ConsumerOffsetLagObserved: t.ConsumerOffsetLagObserved, ConsumerOffsetLag: t.ConsumerOffsetLag}
}

func archiveMessagingFromBatch(batch *pb.MetricsBatch) map[string]*protocol.MessagingSummary {
	out := map[string]*protocol.MessagingSummary{}
	for _, value := range batch.GetActionMetrics() {
		if s := messagingSummaryFromJSON(value.GetMessagingSummaryJson(), value.GetMessagingVersion()); s != nil {
			out[value.GetName()] = s
		}
	}
	return protocol.CloneMessagingSummaries(out)
}

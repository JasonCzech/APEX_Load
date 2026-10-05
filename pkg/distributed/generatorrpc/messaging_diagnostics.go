package generatorrpc

import (
	"encoding/json"
	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
)

const FeatureMessagingRollupV1 = "messaging_rollup_v1"
const FeatureMessagingScopeV1 = "messaging_scope_v1"
const FeatureMessagingEvidenceV2 = "messaging_evidence_v2"
const maxMessagingDiagnosticsBytes = 16 << 10
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
	if raw == "" {
		if version == 0 || version == 1 {
			return nil
		}
		return &protocol.MessagingSummary{Incomplete: true}
	}
	if !supportedMessagingSummaryVersion(version) {
		return &protocol.MessagingSummary{Incomplete: true}
	}
	var s protocol.MessagingSummary
	if len(raw) > maxMessagingSummaryBytes || json.Unmarshal([]byte(raw), &s) != nil || !s.Valid() || s.EvidenceVersion() != version {
		return &protocol.MessagingSummary{Incomplete: true}
	}
	return &s
}

// MessagingTelemetry is a bounded-model adapter, not an inferred server timing.
func (t *ServerTelemetry) MessagingTelemetry() *protocol.ServerTelemetry {
	if t == nil {
		return nil
	}
	return &protocol.ServerTelemetry{Messaging: t.Messaging.Clone(), SubscriptionID: t.SubscriptionID, AcknowledgementMs: cloneMessagingAck(t.AcknowledgementMs), PartitionID: t.PartitionID, MessagesSent: int(t.MessagesSent), MessagesReceived: int(t.MessagesReceived), ThrottledRequests: int(t.ThrottledRequests), RedeliveryCount: int(t.RedeliveryCount), ConsumerLagObserved: t.ConsumerLagObserved, ConsumerLagMs: t.ConsumerLagMs, ConsumerOffsetLagObserved: t.ConsumerOffsetLagObserved, ConsumerOffsetLag: t.ConsumerOffsetLag}
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

func supportedMessagingSummaryVersion(version uint32) bool { return version == 1 || version == 2 }

func messagingSummaryVersion(s *engine.ServerTelemetrySummary) uint32 {
	if s == nil || s.Messaging == nil {
		return 1
	}
	return s.Messaging.EvidenceVersion()
}

func messagingDiagnosticsVersion(d *protocol.MessagingDiagnostics) uint32 {
	if d == nil {
		return 0
	}
	return 2
}

func messagingDiagnosticsJSON(d *protocol.MessagingDiagnostics) string {
	if d == nil {
		return ""
	}
	if !d.Valid() {
		return `{ "version": 0 }`
	}
	b, err := json.Marshal(d)
	if err != nil || len(b) > maxMessagingDiagnosticsBytes {
		return `{ "version": 0 }`
	}
	return string(b)
}

func messagingDiagnosticsFromJSON(raw string, version uint32) *protocol.MessagingDiagnostics {
	if raw == "" && version == 0 {
		return nil
	}
	invalid := &protocol.MessagingDiagnostics{}
	if version != 2 || len(raw) > maxMessagingDiagnosticsBytes {
		return invalid
	}
	var d protocol.MessagingDiagnostics
	if json.Unmarshal([]byte(raw), &d) != nil || !d.Valid() {
		return invalid
	}
	return d.Clone()
}

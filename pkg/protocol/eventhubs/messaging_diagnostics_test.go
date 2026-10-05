package eventhubs

import (
	"context"
	"github.com/jasonczech/apexload/pkg/protocol"
	"testing"
	"time"
)

func TestMessagingEventHubsPartitionAndAcknowledgementAvailability(t *testing.T) {
	ack := 2.0
	r := &protocol.Result{StartTime: time.Now(), Duration: time.Millisecond, Extractions: map[string]string{}}
	got := finalizeTelemetry(r, event{PartitionID: "3", AcknowledgementMs: &ack}, true, event{}, false, nil)
	if got.PartitionID != "3" || got.AcknowledgementMs == nil || !got.Messaging.Valid() || got.Messaging.Durations[0].Metric != "producer_ack" || got.Messaging.Durations[0].ValueMs == nil {
		t.Fatal("ack lost", got)
	}
	got = finalizeTelemetry(r, event{}, false, event{PartitionID: "0"}, true, nil)
	if got.PartitionID != "0" || got.ConsumerLagObserved || got.AcknowledgementMs != nil || !got.Messaging.Valid() || got.Messaging.Durations[0].UnavailableReason != "not_observed" {
		t.Fatal("received partition or unavailable values wrong", got)
	}
}

func TestMessagingEventHubsBatchPartitionSkewUsesMessageVolume(t *testing.T) {
	e := newMockEventHubs(t, map[string]any{"eventhubs_event_hub": "events"})
	var summary *protocol.MessagingSummary
	for _, sample := range []struct {
		partition string
		bodies    []string
	}{{"0", []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}}, {"1", []string{"j"}}} {
		result, err := e.Execute(context.Background(), &protocol.Action{
			Type: "eventhubs_produce", Name: "publish",
			Params: map[string]any{"partition_id": sample.partition, "messages": sample.bodies},
		})
		if err != nil || !result.Success || result.ServerTelemetry == nil || result.ServerTelemetry.MessagesSent != len(sample.bodies) {
			t.Fatalf("batch volume lost: %v %+v", err, result)
		}
		protocol.MergeMessagingTelemetry(&summary, result.ServerTelemetry, result.DependencyTarget, result.DependencyType, "group")
	}
	if summary == nil || !summary.Valid() || summary.Incomplete {
		t.Fatalf("invalid batch evidence: %+v", summary)
	}
	if skew, ok := summary.ObservedPartitionSkew(); !ok || skew != 80 {
		t.Fatalf("skew %v available %v; wanted message-volume skew 80", skew, ok)
	}
	if summary.Dimensions[0].Operations != 1 || summary.Dimensions[0].Sent != 9 {
		t.Fatal("sampled operation count confused with messages", summary)
	}
}

type lastEventPartitionBatchClient struct{ eventHubsClient }

func (c lastEventPartitionBatchClient) SendBatch(ctx context.Context, hub string, events []event) (eventMeta, error) {
	meta, err := c.eventHubsClient.(eventHubsBatchClient).SendBatch(ctx, hub, events)
	// This metadata describes the last event, not all messages in the sample.
	meta.PartitionID = "last-event-partition"
	return meta, err
}

func TestMessagingEventHubsUnpinnedBatchDoesNotAttributeVolumeToLastPartition(t *testing.T) {
	e := newMockEventHubs(t, map[string]any{"eventhubs_event_hub": "events"})
	e.client = lastEventPartitionBatchClient{eventHubsClient: e.client}
	result, err := e.Execute(context.Background(), &protocol.Action{
		Type: "eventhubs_produce", Name: "publish",
		Params: map[string]any{"messages": []string{"first", "second"}},
	})
	if err != nil || !result.Success || result.ServerTelemetry == nil {
		t.Fatalf("batch failed: %v %+v", err, result)
	}
	tel := result.ServerTelemetry
	if tel.MessagesSent != 2 || tel.PartitionID != "" || result.Extractions["eventhubs_partition"] != "" {
		t.Fatalf("last-event metadata became sample-wide partition evidence: %+v", result)
	}
	var summary *protocol.MessagingSummary
	protocol.MergeMessagingTelemetry(&summary, tel, result.DependencyTarget, result.DependencyType)
	if _, ok := summary.ObservedPartitionSkew(); ok {
		t.Fatal("unresolved batch partition skew scored")
	}
}

func TestMessagingEventHubsDoesNotQualifyUncalibratedEnqueueClock(t *testing.T) {
	observed := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	result := &protocol.Result{StartTime: observed, Duration: time.Millisecond, Extractions: map[string]string{}}
	telemetry := finalizeTelemetry(result, event{}, false, event{PartitionID: "0", EnqueuedAt: observed}, true, nil)
	if !telemetry.ConsumerLagObserved || telemetry.ConsumerLagMs != 1 || !telemetry.Messaging.Valid() || telemetry.Messaging.Durations[0].ValueMs != nil || telemetry.Messaging.Durations[0].UnavailableReason != "clock_unqualified" {
		t.Fatalf("uncalibrated source clock promoted: %+v", telemetry)
	}
}

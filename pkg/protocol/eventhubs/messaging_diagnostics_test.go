package eventhubs

import (
	"github.com/jasonczech/apexload/pkg/protocol"
	"testing"
	"time"
)

func TestMessagingEventHubsPartitionAndAcknowledgementAvailability(t *testing.T) {
	ack := 2.0
	r := &protocol.Result{StartTime: time.Now(), Duration: time.Millisecond, Extractions: map[string]string{}}
	got := finalizeTelemetry(r, event{PartitionID: "3", AcknowledgementMs: &ack}, true, event{}, false, nil)
	if got.PartitionID != "3" || got.AcknowledgementMs == nil {
		t.Fatal("ack lost", got)
	}
	got = finalizeTelemetry(r, event{}, false, event{PartitionID: "0"}, true, nil)
	if got.PartitionID != "0" || got.ConsumerLagObserved || got.AcknowledgementMs != nil {
		t.Fatal("received partition or unavailable values wrong", got)
	}
}

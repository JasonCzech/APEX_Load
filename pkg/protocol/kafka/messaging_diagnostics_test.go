package kafka

import (
	"github.com/jasonczech/apexload/pkg/protocol"
	"testing"
	"time"
)

func TestMessagingKafkaPartitionAndAcknowledgementAvailability(t *testing.T) {
	ack := 2.0
	result := &protocol.Result{StartTime: time.Now(), Duration: time.Millisecond, Extractions: map[string]string{}}
	got := finalizeKafkaServerTelemetry(result, kafkaRecord{Partition: 3, AcknowledgementMs: &ack}, true, kafkaRecord{}, false)
	if got.PartitionID != "3" || got.AcknowledgementMs == nil || *got.AcknowledgementMs != 2 {
		t.Fatal("ack metadata discarded", got)
	}
	got = finalizeKafkaServerTelemetry(result, kafkaRecord{Partition: -1}, true, kafkaRecord{}, false)
	if got.PartitionID != "" || got.AcknowledgementMs != nil {
		t.Fatal("unknown partition or mock ack invented", got)
	}
	got = finalizeKafkaServerTelemetry(result, kafkaRecord{}, false, kafkaRecord{Partition: 0}, true)
	if got.PartitionID != "0" || got.ConsumerLagObserved {
		t.Fatal("received zero partition lost or lag invented", got)
	}
}

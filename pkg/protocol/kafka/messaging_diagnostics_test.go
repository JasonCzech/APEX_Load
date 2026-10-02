package kafka

import (
	"context"
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

// Wrap the real mock client only at the provider acknowledgement seam. This
// exercises Execute and requestReply propagation without a network broker.
type acknowledgedRequestReplyClient struct {
	kafkaClient
	ack float64
}

func (c acknowledgedRequestReplyClient) RequestReply(ctx context.Context, reqTopic, replyTopic string, request kafkaRecord, timeout time.Duration) (recordMeta, kafkaRecord, error) {
	meta, response, err := c.kafkaClient.RequestReply(ctx, reqTopic, replyTopic, request, timeout)
	meta.AcknowledgementMs = &c.ack
	meta.Partition = 2
	response.Partition = 3
	return meta, response, err
}

func TestMessagingKafkaRequestReplyPreservesAcknowledgementWithoutPartitionMisattribution(t *testing.T) {
	k := newMockKafka(t, map[string]any{"kafka_topic": "requests", "kafka_consumer_group": "reply-group"})
	k.client = acknowledgedRequestReplyClient{kafkaClient: k.client, ack: 4}
	result, err := k.Execute(context.Background(), &protocol.Action{
		Name: "rpc", Type: "kafka_request_reply", Body: []byte("request"),
		Params: map[string]any{"reply_to": "replies", "timeout_ms": 500},
	})
	if err != nil || !result.Success || result.ServerTelemetry == nil {
		t.Fatalf("request/reply failed: %v %+v", err, result)
	}
	tel := result.ServerTelemetry
	if tel.AcknowledgementMs == nil || *tel.AcknowledgementMs != 4 {
		t.Fatalf("request publish acknowledgement lost: %+v", tel)
	}
	if tel.PartitionID != "" || tel.MessagesSent != 1 || tel.MessagesReceived != 1 || tel.SubscriptionID != "reply-group" {
		t.Fatalf("request/reply direction evidence: %+v", tel)
	}
	var summary *protocol.MessagingSummary
	protocol.MergeMessagingTelemetry(&summary, tel, result.DependencyTarget, result.DependencyType)
	if summary == nil || !summary.Incomplete || summary.Dimensions[0].Source != "" || summary.Dimensions[0].Partition != "" {
		t.Fatalf("reply partition attributed to request topic: %+v", summary)
	}
}

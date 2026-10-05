package kafka

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

type outcomeKafkaClient struct {
	failSend bool
	attempts int
	kafkaClient
	failReply bool
	sends     int
}

func (c *outcomeKafkaClient) Produce(ctx context.Context, topic string, record kafkaRecord) (recordMeta, error) {
	c.attempts++
	if c.failSend {
		return recordMeta{}, errors.New("provider send failed")
	}
	meta, err := c.kafkaClient.Produce(ctx, topic, record)
	if err == nil {
		c.sends++
		ack := 4.0
		meta.AcknowledgementMs = &ack
	}
	return meta, err
}
func (c *outcomeKafkaClient) RequestReply(ctx context.Context, requestTopic, replyTopic string, record kafkaRecord, timeout time.Duration) (recordMeta, kafkaRecord, error) {
	if c.failReply {
		meta, err := c.Produce(ctx, requestTopic, record)
		if err != nil {
			return recordMeta{}, kafkaRecord{}, err
		}
		meta.sent = true
		return meta, kafkaRecord{}, errors.New("reply receive failed")
	}
	meta, response, err := c.kafkaClient.RequestReply(ctx, requestTopic, replyTopic, record, timeout)
	if err == nil {
		c.sends++
		ack := 4.0
		meta.AcknowledgementMs = &ack
	}
	return meta, response, err
}
func TestMessagingKafkaSuccessfulSendSurvivesActionFailure(t *testing.T) {
	for _, test := range []struct {
		name, action               string
		metadata, reply, extractor bool
	}{
		{name: "publish assertion", action: "kafka_produce"},
		{name: "publish extraction", action: "kafka_produce", metadata: true},
		{name: "reply assertion", action: "kafka_request_reply"},
		{name: "reply extraction", action: "kafka_request_reply", extractor: true},
		{name: "reply receive failure", action: "kafka_request_reply", reply: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			k := newMockKafka(t, map[string]any{"kafka_topic": "requests"})
			client := &outcomeKafkaClient{kafkaClient: k.client, failReply: test.reply}
			k.client = client
			action := &protocol.Action{Name: "send", Type: test.action, Body: []byte("published"), Params: map[string]any{"reply_to": "responses"}}
			if test.metadata {
				action.Params["key"] = strings.Repeat("x", 128<<10)
			}
			if test.extractor {
				action.Extractors = []protocol.Extractor{{Name: "missing", Source: "body", Type: "regex", Expression: "not-present", OnMiss: "fail"}}
			} else if !test.metadata && !test.reply {
				action.Assertions = []protocol.Assertion{{Type: "body_contains", Value: "not-present"}}
			}
			result, err := k.Execute(context.Background(), action)
			if err != nil || result.Success || client.sends != 1 || result.ServerTelemetry == nil {
				t.Fatalf("action failure/outcome: %v %+v sends=%d", err, result, client.sends)
			}
			telemetry := result.ServerTelemetry
			if telemetry.MessagesSent != 1 || telemetry.SendFailureCount != 0 || telemetry.AcknowledgementMs == nil || *telemetry.AcknowledgementMs != 4 || !telemetry.Messaging.Valid() {
				t.Fatalf("successful send discarded/misclassified: %+v", telemetry)
			}
			if test.action == "kafka_request_reply" && !test.reply && telemetry.MessagesReceived != 1 {
				t.Fatal("received reply discarded after action failure")
			}
			if test.reply && telemetry.MessagesReceived != 0 {
				t.Fatal("missing reply became received evidence")
			}
			var summary *protocol.MessagingSummary
			protocol.MergeMessagingTelemetry(&summary, telemetry, result.DependencyTarget, result.DependencyType)
			found := false
			for _, duration := range summary.Dimensions[0].Evidence.Durations {
				if duration.Metric == "producer_ack" {
					found = duration.Distribution.Count == 1 && duration.Distribution.Sum == 4
				}
			}
			if !found {
				t.Fatal("genuine acknowledgement missing from histogram")
			}
		})
	}
}

func TestMessagingKafkaFailedSendRetainsEligiblePopulation(t *testing.T) {
	k := newMockKafka(t, map[string]any{"kafka_topic": "events"})
	client := &outcomeKafkaClient{kafkaClient: k.client}
	k.client = client
	var summary *protocol.MessagingSummary
	for _, failed := range []bool{false, true} {
		client.failSend = failed
		result, err := k.Execute(context.Background(), &protocol.Action{Name: "send", Type: "kafka_produce", Body: []byte("event")})
		if err != nil || result.Success == failed || !result.ServerTelemetry.Messaging.Valid() {
			t.Fatalf("outcome: %v %+v", err, result)
		}
		protocol.MergeMessagingTelemetry(&summary, result.ServerTelemetry, result.DependencyTarget, result.DependencyType)
	}
	var eligible, observed, missing, counterEligible, failures int64
	for _, dimension := range summary.Dimensions {
		for _, duration := range dimension.Evidence.Durations {
			if duration.Metric == "producer_ack" {
				eligible += duration.Eligible
				observed += duration.Distribution.Count
				missing += duration.Unavailable["failed"]
			}
		}
		for _, counter := range dimension.Evidence.Counters {
			if counter.Metric == "send_failure" {
				counterEligible += counter.Eligible
				failures += counter.Count
			}
		}
	}
	if !summary.Valid() || summary.Incomplete || eligible != 2 || observed != 1 || missing != 1 || counterEligible != 2 || failures != 1 || client.attempts != 2 {
		t.Fatalf("eligible=%d observed=%d missing=%d denominator=%d failures=%d attempts=%d", eligible, observed, missing, counterEligible, failures, client.attempts)
	}
}

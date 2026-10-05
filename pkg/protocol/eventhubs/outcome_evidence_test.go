package eventhubs

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

type outcomeEventHubsClient struct {
	failSend bool
	attempts int
	eventHubsClient
	badMetadata bool
	sends       int
}

func (c *outcomeEventHubsClient) acknowledge(meta eventMeta, err error) (eventMeta, error) {
	if err == nil {
		c.sends++
		ack := 4.0
		meta.AcknowledgementMs = &ack
		if c.badMetadata {
			meta.PartitionID = strings.Repeat("x", 128<<10)
		}
	}
	return meta, err
}
func (c *outcomeEventHubsClient) Send(ctx context.Context, hub string, event event) (eventMeta, error) {
	c.attempts++
	if c.failSend {
		return eventMeta{}, errors.New("provider send failed")
	}
	return c.acknowledge(c.eventHubsClient.Send(ctx, hub, event))
}
func (c *outcomeEventHubsClient) SendBatch(ctx context.Context, hub string, events []event) (eventMeta, error) {
	return c.acknowledge(c.eventHubsClient.(eventHubsBatchClient).SendBatch(ctx, hub, events))
}
func TestMessagingEventHubsSuccessfulSendSurvivesActionFailure(t *testing.T) {
	for _, test := range []struct {
		name            string
		batch, metadata bool
	}{
		{name: "publish assertion"}, {name: "publish extraction", metadata: true},
		{name: "batch assertion", batch: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			e := newMockEventHubs(t, map[string]any{"eventhubs_event_hub": "events"})
			client := &outcomeEventHubsClient{eventHubsClient: e.client, badMetadata: test.metadata}
			e.client = client
			action := &protocol.Action{Name: "send", Type: "eventhubs_produce", Body: []byte("published"), Params: map[string]any{"partition_id": "0"}}
			wanted := 1
			if test.batch {
				action.Body = nil
				action.Params["messages"] = []string{"first", "second"}
				wanted = 2
			}
			if !test.metadata {
				action.Assertions = []protocol.Assertion{{Type: "body_contains", Value: "quota-exceeded-not-present"}}
			}
			result, err := e.Execute(context.Background(), action)
			if err != nil || result.Success || client.sends != 1 || result.ServerTelemetry == nil {
				t.Fatalf("action failure/outcome: %v %+v sends=%d", err, result, client.sends)
			}
			telemetry := result.ServerTelemetry
			if telemetry.MessagesSent != wanted || telemetry.SendFailureCount != 0 || telemetry.ThrottledRequests != 0 || telemetry.QuotaExceededCount != 0 || telemetry.AcknowledgementMs == nil || *telemetry.AcknowledgementMs != 4 || !telemetry.Messaging.Valid() {
				t.Fatalf("successful send discarded/misclassified: %+v", telemetry)
			}
		})
	}
}

func TestMessagingEventHubsFailedSendRetainsEligiblePopulation(t *testing.T) {
	e := newMockEventHubs(t, map[string]any{"eventhubs_event_hub": "events"})
	client := &outcomeEventHubsClient{eventHubsClient: e.client}
	e.client = client
	var summary *protocol.MessagingSummary
	for _, failed := range []bool{false, true} {
		client.failSend = failed
		result, err := e.Execute(context.Background(), &protocol.Action{Name: "send", Type: "eventhubs_produce", Body: []byte("event")})
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

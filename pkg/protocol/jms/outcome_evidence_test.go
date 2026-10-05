package jms

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

type outcomeJMSAdapter struct {
	failSend bool
	attempts int
	brokerAdapter
	badMetadata bool
	failReply   bool
	sends       int
}

func (a *outcomeJMSAdapter) Publish(ctx context.Context, destination string, request message) (message, error) {
	a.attempts++
	if a.failSend {
		return message{}, errors.New("provider send failed")
	}
	sent, err := a.brokerAdapter.Publish(ctx, destination, request)
	if err == nil {
		a.sends++
		if a.badMetadata {
			sent.ID = strings.Repeat("x", 128<<10)
		}
	}
	return sent, err
}
func (a *outcomeJMSAdapter) RequestReply(ctx context.Context, destination, reply string, request message, timeout time.Duration) (message, message, error) {
	if a.failReply {
		sent, err := a.Publish(ctx, destination, request)
		if err != nil {
			return message{}, message{}, err
		}
		sent.sent = true
		return sent, message{}, errors.New("reply receive failed")
	}
	sent, response, err := a.brokerAdapter.RequestReply(ctx, destination, reply, request, timeout)
	if err == nil {
		a.sends++
	}
	return sent, response, err
}
func TestMessagingJMSSuccessfulSendSurvivesActionFailure(t *testing.T) {
	for _, test := range []struct {
		name, action               string
		metadata, reply, extractor bool
	}{
		{name: "publish assertion", action: "jms_publish"}, {name: "publish extraction", action: "jms_publish", metadata: true},
		{name: "reply assertion", action: "jms_request_reply"}, {name: "reply extraction", action: "jms_request_reply", extractor: true},
		{name: "reply receive failure", action: "jms_request_reply", reply: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			j := newMockJMS(t)
			adapter := &outcomeJMSAdapter{brokerAdapter: j.adapter, badMetadata: test.metadata, failReply: test.reply}
			j.adapter = adapter
			action := &protocol.Action{Name: "send", Type: test.action, Target: "requests", Body: []byte("published"), Params: map[string]any{"destination": "requests", "reply_to": "responses"}}
			if test.extractor {
				action.Extractors = []protocol.Extractor{{Name: "missing", Source: "body", Type: "regex", Expression: "not-present", OnMiss: "fail"}}
			} else if !test.metadata && !test.reply {
				action.Assertions = []protocol.Assertion{{Type: "body_contains", Value: "not-present"}}
			}
			result, err := j.Execute(context.Background(), action)
			if err != nil || result.Success || adapter.sends != 1 || result.ServerTelemetry == nil {
				t.Fatalf("action failure/outcome: %v %+v sends=%d", err, result, adapter.sends)
			}
			telemetry := result.ServerTelemetry
			if telemetry.MessagesSent != 1 || telemetry.SendFailureCount != 0 || !telemetry.Messaging.Valid() {
				t.Fatalf("successful send discarded/misclassified: %+v", telemetry)
			}
			if test.action == "jms_request_reply" && !test.reply && telemetry.MessagesReceived != 1 {
				t.Fatal("received reply discarded after action failure")
			}
			if test.reply && telemetry.MessagesReceived != 0 {
				t.Fatal("missing reply became received evidence")
			}
		})
	}
}

func TestMessagingJMSFailedSendRetainsEligiblePopulation(t *testing.T) {
	j := newMockJMS(t)
	adapter := &outcomeJMSAdapter{brokerAdapter: j.adapter}
	j.adapter = adapter
	var summary *protocol.MessagingSummary
	for _, failed := range []bool{false, true} {
		adapter.failSend = failed
		result, err := j.Execute(context.Background(), &protocol.Action{Name: "send", Type: "jms_publish", Target: "events", Body: []byte("event")})
		if err != nil || result.Success == failed || !result.ServerTelemetry.Messaging.Valid() {
			t.Fatalf("outcome: %v %+v", err, result)
		}
		protocol.MergeMessagingTelemetry(&summary, result.ServerTelemetry, result.DependencyTarget, result.DependencyType)
	}
	var eligible, missing, counterEligible, failures int64
	for _, dimension := range summary.Dimensions {
		for _, duration := range dimension.Evidence.Durations {
			if duration.Metric == "producer_ack" {
				eligible += duration.Eligible
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
	if !summary.Valid() || summary.Incomplete || eligible != 2 || missing != 1 || counterEligible != 2 || failures != 1 || adapter.attempts != 2 {
		t.Fatalf("eligible=%d missing=%d denominator=%d failures=%d attempts=%d", eligible, missing, counterEligible, failures, adapter.attempts)
	}
}

type partialJMSReplyAdapter struct {
	brokerAdapter
	id   string
	body []byte
}

func (a partialJMSReplyAdapter) Consume(context.Context, string, time.Duration) (message, error) {
	return message{ID: a.id, Body: a.body, received: true}, errors.New("settlement failed")
}
func (a partialJMSReplyAdapter) RequestReply(_ context.Context, _, _ string, request message, _ time.Duration) (message, message, error) {
	request.sent = true
	return request, message{ID: a.id, Body: a.body, received: true}, errors.New("settlement failed")
}
func TestMessagingJMSObservedDeliverySurvivesSettlementFailure(t *testing.T) {
	for _, observation := range []struct {
		name, id string
		body     []byte
	}{
		{name: "body", id: "received", body: []byte("reply")},
		{name: "empty body", id: "received"},
		{name: "empty metadata", id: ""},
	} {
		for _, actionType := range []string{"jms_consume", "jms_request_reply"} {
			t.Run(observation.name+"/"+actionType, func(t *testing.T) {
				j := newMockJMS(t)
				j.adapter = partialJMSReplyAdapter{brokerAdapter: j.adapter, id: observation.id, body: observation.body}
				result, err := j.Execute(context.Background(), &protocol.Action{Name: "receive", Type: actionType, Target: "requests", Body: []byte("request"), Params: map[string]any{"reply_to": "replies"}})
				if err != nil || result.Success || result.ServerTelemetry == nil || result.ServerTelemetry.MessagesReceived != 1 || result.ServerTelemetry.SendFailureCount != 0 {
					t.Fatalf("partial delivery lost: %v %+v", err, result)
				}
				if actionType == "jms_request_reply" && result.ServerTelemetry.MessagesSent != 1 {
					t.Fatal("successful request lost")
				}
			})
		}
	}
}

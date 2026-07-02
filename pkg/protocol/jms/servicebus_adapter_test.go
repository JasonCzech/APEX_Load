//go:build jms_servicebus_real

package jms

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"
)

func TestServiceBusMessageMappingRoundTrip(t *testing.T) {
	createdAt := time.Now().UTC().Add(-time.Minute)
	contentType := "application/json"
	replyTo := "orders.reply"
	in := normalizeServiceBusOutgoingMessage("orders.queue", message{
		ID:            "ID:message-1",
		CorrelationID: "corr-1",
		Headers: map[string]string{
			"Content-Type": contentType,
			"reply_to":     replyTo,
			"traceparent":  "00-abc-123-01",
		},
		Properties: map[string]string{
			"customer": "retail",
		},
		Body:      []byte(`{"ok":true}`),
		CreatedAt: createdAt,
	})

	mapped := toServiceBusMessage("orders.queue", in)
	if mapped.MessageID == nil || *mapped.MessageID != in.ID {
		t.Fatalf("MessageID = %#v, want %q", mapped.MessageID, in.ID)
	}
	if mapped.CorrelationID == nil || *mapped.CorrelationID != in.CorrelationID {
		t.Fatalf("CorrelationID = %#v, want %q", mapped.CorrelationID, in.CorrelationID)
	}
	if mapped.ContentType == nil || *mapped.ContentType != contentType {
		t.Fatalf("ContentType = %#v, want %q", mapped.ContentType, contentType)
	}
	if mapped.ReplyTo == nil || *mapped.ReplyTo != replyTo {
		t.Fatalf("ReplyTo = %#v, want %q", mapped.ReplyTo, replyTo)
	}
	if mapped.ApplicationProperties["customer"] != "retail" || mapped.ApplicationProperties["traceparent"] != "00-abc-123-01" {
		t.Fatalf("ApplicationProperties = %#v, want customer and traceparent", mapped.ApplicationProperties)
	}
	if _, ok := mapped.ApplicationProperties["Content-Type"]; ok {
		t.Fatalf("ApplicationProperties includes reserved Content-Type header: %#v", mapped.ApplicationProperties)
	}

	out := fromServiceBusMessage("orders.queue", &azservicebus.ReceivedMessage{
		MessageID:             *mapped.MessageID,
		CorrelationID:         mapped.CorrelationID,
		ContentType:           mapped.ContentType,
		ReplyTo:               mapped.ReplyTo,
		ApplicationProperties: mapped.ApplicationProperties,
		Body:                  mapped.Body,
		DeliveryCount:         2,
		EnqueuedTime:          &createdAt,
	})
	if out.ID != in.ID || out.CorrelationID != in.CorrelationID || string(out.Body) != string(in.Body) {
		t.Fatalf("round-trip message = %#v, want id/correlation/body from input %#v", out, in)
	}
	if out.Headers["Content-Type"] != contentType || out.Headers["reply_to"] != replyTo {
		t.Fatalf("headers = %#v, want content type and reply_to", out.Headers)
	}
	if out.Properties["delivery_count"] != "2" || out.Properties["customer"] != "retail" {
		t.Fatalf("properties = %#v, want delivery_count and customer", out.Properties)
	}
	if !out.CreatedAt.Equal(createdAt) {
		t.Fatalf("CreatedAt = %s, want %s", out.CreatedAt, createdAt)
	}
}

func TestServiceBusClientPoolRefcountAcquireRelease(t *testing.T) {
	pool := newServiceBusClientPool()
	created := 0
	pool.newClient = func(serviceBusConn) (*azservicebus.Client, error) {
		created++
		return nil, nil
	}
	conn := serviceBusConn{
		FullyQualifiedNS: "orders.servicebus.windows.net",
		AuthMode:         "default_azure_credential",
	}
	key := serviceBusPoolKey(conn, 2)

	first, err := pool.acquire(conn, key)
	if err != nil {
		t.Fatalf("first acquire returned error: %v", err)
	}
	second, err := pool.acquire(conn, key)
	if err != nil {
		t.Fatalf("second acquire returned error: %v", err)
	}
	if first != second {
		t.Fatal("pool returned different entries for the same key")
	}
	if created != 1 {
		t.Fatalf("created clients = %d, want 1", created)
	}
	if first.refs != 2 {
		t.Fatalf("refs = %d, want 2", first.refs)
	}

	if err := pool.release(context.Background(), first); err != nil {
		t.Fatalf("first release returned error: %v", err)
	}
	if first.refs != 1 {
		t.Fatalf("refs after first release = %d, want 1", first.refs)
	}
	if err := pool.release(context.Background(), second); err != nil {
		t.Fatalf("second release returned error: %v", err)
	}
	pool.mu.Lock()
	_, stillPresent := pool.entries[key]
	pool.mu.Unlock()
	if stillPresent {
		t.Fatal("pool entry still present after final release")
	}
}

func TestServiceBusSubjectAndPriorityRoundTrip(t *testing.T) {
	pr := uint8(6)
	in := message{ID: "m1", Type: "map", Body: []byte(`{"a":1}`), Priority: &pr}
	mapped := toServiceBusMessage("orders.q", in)
	if mapped.Subject == nil || *mapped.Subject != "map" {
		t.Fatalf("Subject = %v, want map", mapped.Subject)
	}
	if v, ok := mapped.ApplicationProperties["JMSPriority"].(int64); !ok || v != 6 {
		t.Fatalf("JMSPriority application property = %v, want int64 6", mapped.ApplicationProperties["JMSPriority"])
	}
	subj := "map"
	out := fromServiceBusMessage("orders.q", &azservicebus.ReceivedMessage{Subject: &subj, Body: []byte(`{"a":1}`)})
	if out.Type != "map" {
		t.Fatalf("Type round-trip = %q, want map", out.Type)
	}
}

func TestServiceBusImplementsBrowserNotSelector(t *testing.T) {
	var a brokerAdapter = &serviceBusAdapter{}
	if _, ok := a.(messageBrowser); !ok {
		t.Fatal("Service Bus must implement messageBrowser (PeekMessages)")
	}
	// Selectors are admin-plane subscription rules on Service Bus, not a data-plane
	// per-consume filter — the adapter must NOT advertise selectiveConsumer so a
	// selector consume fails closed with an honest error.
	if _, ok := a.(selectiveConsumer); ok {
		t.Fatal("Service Bus must NOT implement selectiveConsumer")
	}
}

func TestServiceBusDoesNotImplementTopicAdapter(t *testing.T) {
	var a brokerAdapter = &serviceBusAdapter{}
	// Service Bus topic/subscription addressing is handled by the standard path,
	// not the fan-out capability, so the adapter must NOT implement topicAdapter.
	if _, ok := a.(topicAdapter); ok {
		t.Fatal("Service Bus must NOT implement topicAdapter")
	}
}

func TestServiceBusAwaitCorrelatedReplySettlesBatchRemainder(t *testing.T) {
	foreignCorrelation := "corr-other"
	matchCorrelation := "corr-match"
	seq := int64(3)
	receiver := &fakeServiceBusReplyReceiver{batches: [][]*azservicebus.ReceivedMessage{
		{
			{MessageID: "foreign-before", CorrelationID: &foreignCorrelation, Body: []byte("other-before")},
			{MessageID: "match", CorrelationID: &matchCorrelation, Body: []byte("ok")},
			{SequenceNumber: &seq, CorrelationID: &foreignCorrelation, Body: []byte("other-after")},
		},
	}}

	reply, err := serviceBusAwaitCorrelatedReply(context.Background(), receiver, "orders.reply", message{CorrelationID: matchCorrelation}, time.Second)
	if err != nil {
		t.Fatalf("serviceBusAwaitCorrelatedReply returned error: %v", err)
	}
	if string(reply.Body) != "ok" {
		t.Fatalf("reply body = %q, want ok", reply.Body)
	}
	if got := receiver.completeCount("match"); got != 1 {
		t.Fatalf("completed match count = %d, want 1", got)
	}
	if got := receiver.abandonCount("foreign-before"); got != 1 {
		t.Fatalf("abandoned foreign-before count = %d, want 1", got)
	}
	if got := receiver.abandonCount("sequence:3"); got != 1 {
		t.Fatalf("abandoned sequence fallback count = %d, want 1", got)
	}
}

func TestServiceBusAwaitCorrelatedReplyDoesNotRepeatedlyAbandonForeignReply(t *testing.T) {
	foreignCorrelation := "corr-other"
	receiver := &fakeServiceBusReplyReceiver{batches: [][]*azservicebus.ReceivedMessage{
		{{MessageID: "foreign", CorrelationID: &foreignCorrelation, Body: []byte("other")}},
		{{MessageID: "foreign", CorrelationID: &foreignCorrelation, Body: []byte("other")}},
	}}

	_, err := serviceBusAwaitCorrelatedReply(context.Background(), receiver, "orders.reply", message{CorrelationID: "corr-mine"}, 20*time.Millisecond)
	if err == nil {
		t.Fatal("serviceBusAwaitCorrelatedReply returned nil error without a correlated reply")
	}
	if got := receiver.abandonCount("foreign"); got != serviceBusForeignReplyAbandonLimit {
		t.Fatalf("abandoned foreign count = %d, want budget %d", got, serviceBusForeignReplyAbandonLimit)
	}
}

type fakeServiceBusReplyReceiver struct {
	batches   [][]*azservicebus.ReceivedMessage
	completed map[string]int
	abandoned map[string]int
}

func (r *fakeServiceBusReplyReceiver) ReceiveMessages(ctx context.Context, _ int, _ *azservicebus.ReceiveMessagesOptions) ([]*azservicebus.ReceivedMessage, error) {
	if len(r.batches) == 0 {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	batch := r.batches[0]
	r.batches = r.batches[1:]
	return batch, nil
}

func (r *fakeServiceBusReplyReceiver) CompleteMessage(_ context.Context, msg *azservicebus.ReceivedMessage, _ *azservicebus.CompleteMessageOptions) error {
	if msg == nil {
		return errors.New("message is required")
	}
	if r.completed == nil {
		r.completed = make(map[string]int)
	}
	r.completed[msg.MessageID]++
	return nil
}

func (r *fakeServiceBusReplyReceiver) AbandonMessage(_ context.Context, msg *azservicebus.ReceivedMessage, _ *azservicebus.AbandonMessageOptions) error {
	if msg == nil {
		return errors.New("message is required")
	}
	if r.abandoned == nil {
		r.abandoned = make(map[string]int)
	}
	r.abandoned[serviceBusTestReplyMessageKey(msg)]++
	return nil
}

func (r *fakeServiceBusReplyReceiver) completeCount(key string) int {
	return r.completed[key]
}

func (r *fakeServiceBusReplyReceiver) abandonCount(key string) int {
	return r.abandoned[key]
}

func serviceBusTestReplyMessageKey(msg *azservicebus.ReceivedMessage) string {
	if msg.MessageID != "" {
		return msg.MessageID
	}
	return serviceBusReplyMessageKey(msg)
}

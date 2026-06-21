//go:build jms_servicebus_real

package jms

import (
	"context"
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

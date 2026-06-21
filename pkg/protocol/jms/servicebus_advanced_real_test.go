//go:build jms_servicebus_real

package jms

import (
	"context"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"
)

func TestToServiceBusMessageMapsAdvancedSendFields(t *testing.T) {
	enqueueAt := time.Now().Add(5 * time.Second)
	mapped := toServiceBusMessage("orders.queue", message{
		ID:                 "dedup-1",
		Body:               []byte("x"),
		SessionID:          "sess-7",
		PartitionKey:       "pk-3",
		ScheduledEnqueueAt: enqueueAt,
		TimeToLive:         60 * time.Second,
	})
	if mapped.SessionID == nil || *mapped.SessionID != "sess-7" {
		t.Fatalf("SessionID = %#v, want sess-7", mapped.SessionID)
	}
	if mapped.PartitionKey == nil || *mapped.PartitionKey != "pk-3" {
		t.Fatalf("PartitionKey = %#v, want pk-3", mapped.PartitionKey)
	}
	if mapped.ScheduledEnqueueTime == nil || !mapped.ScheduledEnqueueTime.Equal(enqueueAt) {
		t.Fatalf("ScheduledEnqueueTime = %#v, want %v", mapped.ScheduledEnqueueTime, enqueueAt)
	}
	if mapped.TimeToLive == nil || *mapped.TimeToLive != 60*time.Second {
		t.Fatalf("TimeToLive = %#v, want 60s", mapped.TimeToLive)
	}
}

func TestToServiceBusMessageOmitsUnsetAdvancedFields(t *testing.T) {
	mapped := toServiceBusMessage("orders.queue", message{ID: "1", Body: []byte("x")})
	if mapped.SessionID != nil || mapped.PartitionKey != nil || mapped.ScheduledEnqueueTime != nil || mapped.TimeToLive != nil {
		t.Fatalf("unset advanced fields should be nil, got session=%v partition=%v enqueue=%v ttl=%v",
			mapped.SessionID, mapped.PartitionKey, mapped.ScheduledEnqueueTime, mapped.TimeToLive)
	}
}

func TestServiceBusSettleUnknownTokenFailsOffline(t *testing.T) {
	// Settle/RenewLock look up the in-flight token before touching the network, so
	// an unknown token errors deterministically without an Azure connection.
	a := &serviceBusAdapter{inflight: make(map[string]inflightMessage)}
	if err := a.Settle(context.Background(), "missing", "complete", settleOptions{}); err == nil {
		t.Fatal("Settle with unknown token should error")
	}
	if err := a.RenewLock(context.Background(), "missing"); err == nil {
		t.Fatal("RenewLock with unknown token should error")
	}
}

func TestServiceBusConfigErrorAdapterImplementsSettlement(t *testing.T) {
	// Both the real adapter and the config-error adapter must satisfy the full
	// settlementAdapter capability (settle + sessions + deferred + cancel).
	var _ settlementAdapter = serviceBusConfigErrorAdapter{}
	var _ settlementAdapter = (*serviceBusAdapter)(nil)
}

func TestServiceBusSessionStateUnknownTokenFailsOffline(t *testing.T) {
	a := &serviceBusAdapter{sessions: make(map[string]*azservicebus.SessionReceiver)}
	if _, err := a.SessionState(context.Background(), "missing", nil, false); err == nil {
		t.Fatal("SessionState with unknown token should error")
	}
}

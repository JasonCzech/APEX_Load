//go:build jms_servicebus_real

package jms

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"
	"github.com/jasonczech/apexload/pkg/identitypolicy"
)

func TestServiceBusResourceAdmissionBoundsHandlesAndSessions(t *testing.T) {
	adapter := &serviceBusAdapter{
		receivers: make(map[string]*azservicebus.Receiver),
		inflight:  make(map[string]inflightMessage),
		sessions:  make(map[string]*azservicebus.SessionReceiver),
	}
	for i := 0; i < maxServiceBusReceivers; i++ {
		adapter.receivers[fmt.Sprintf("receiver-%d", i)] = nil
	}
	if err := adapter.receiverAdmissionErrorLocked("overflow"); err == nil || !strings.Contains(err.Error(), "capacity") {
		t.Fatalf("receiver overflow error = %v", err)
	}
	for i := 0; i < maxServiceBusSessions; i++ {
		adapter.sessions[fmt.Sprintf("session-%d", i)] = nil
	}
	if _, _, err := adapter.AcceptSession(context.Background(), "orders", ""); err == nil || !strings.Contains(err.Error(), "capacity") {
		t.Fatalf("session overflow error = %v", err)
	}

	entry := &serviceBusPoolEntry{senders: make(map[string]*azservicebus.Sender)}
	for i := 0; i < maxServiceBusSenders; i++ {
		entry.senders[fmt.Sprintf("sender-%d", i)] = nil
	}
	if _, err := entry.sender("overflow"); err == nil || !strings.Contains(err.Error(), "capacity") {
		t.Fatalf("sender overflow error = %v", err)
	}
}

func TestServiceBusInflightAdmissionRejectsBeforeMutation(t *testing.T) {
	adapter := &serviceBusAdapter{
		inflight:      map[string]inflightMessage{"existing": {retained: maxServiceBusInflightBytes - 1}},
		inflightBytes: maxServiceBusInflightBytes - 1,
	}
	err := adapter.admitInflightBatchLocked(map[string]inflightMessage{
		"overflow": {retained: 2},
	}, time.Now())
	if err == nil {
		t.Fatal("inflight byte overflow unexpectedly succeeded")
	}
	if len(adapter.inflight) != 1 || adapter.inflightBytes != maxServiceBusInflightBytes-1 {
		t.Fatalf("inflight mutated on rejection: count=%d bytes=%d", len(adapter.inflight), adapter.inflightBytes)
	}
}

func TestServiceBusClientPoolAdmissionIsBounded(t *testing.T) {
	pool := newServiceBusClientPool()
	for i := 0; i < maxServiceBusPoolEntries; i++ {
		pool.entries[fmt.Sprintf("client-%d", i)] = nil
	}
	if _, err := pool.acquire(serviceBusConn{}, "overflow"); err == nil || !strings.Contains(err.Error(), "capacity") {
		t.Fatalf("client pool overflow error = %v", err)
	}
}

func TestServiceBusDefaultCredentialRequiresOperatorNamespaceBinding(t *testing.T) {
	t.Setenv(identitypolicy.AzureMessagingAllowedNamespacesEnv, "")
	_, err := newServiceBusClient(serviceBusConn{FullyQualifiedNS: "orders.servicebus.windows.net", AuthMode: "default_azure_credential"})
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("unbound default credential error = %v", err)
	}
	t.Setenv(identitypolicy.AzureMessagingAllowedNamespacesEnv, "audit.servicebus.windows.net")
	_, err = newServiceBusClient(serviceBusConn{FullyQualifiedNS: "orders.servicebus.windows.net", AuthMode: "default_azure_credential"})
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("cross-namespace default credential error = %v", err)
	}
}

func TestServiceBusCloseSessionRejectsUnknownToken(t *testing.T) {
	adapter := &serviceBusAdapter{sessions: make(map[string]*azservicebus.SessionReceiver), inflight: make(map[string]inflightMessage)}
	if err := adapter.CloseSession(context.Background(), "missing"); err == nil {
		t.Fatal("unknown session close unexpectedly succeeded")
	}
}

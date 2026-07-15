//go:build eventhubs_real

package eventhubs

import (
	"context"
	"fmt"
	"strings"
	"testing"

	azeventhubs "github.com/Azure/azure-sdk-for-go/sdk/messaging/azeventhubs/v2"
	"github.com/jasonczech/apexload/pkg/identitypolicy"
)

// TestNewRealClientType confirms the eventhubs_real build wires the azeventhubs
// adapter (not the fail-closed stub) into the non-mock path.
func TestNewRealClientType(t *testing.T) {
	c := newRealClient(config{Namespace: "ns.servicebus.windows.net", EventHub: "h"})
	if _, ok := c.(*realClient); !ok {
		t.Fatalf("newRealClient returned %T, want *realClient", c)
	}
}

func TestRealClientResourceMapsAreBoundedBeforeConstruction(t *testing.T) {
	t.Run("producers", func(t *testing.T) {
		client := &realClient{producers: make(map[string]*azeventhubs.ProducerClient), consumers: make(map[string]*azeventhubs.ConsumerClient), partitions: make(map[string]*azeventhubs.PartitionClient)}
		for i := 0; i < maxEventHubsProducers; i++ {
			client.producers[fmt.Sprintf("hub-%d", i)] = nil
		}
		if _, err := client.producer(context.Background(), "overflow"); err == nil || !strings.Contains(err.Error(), "capacity") {
			t.Fatalf("producer overflow error = %v", err)
		}
	})
	t.Run("consumers", func(t *testing.T) {
		client := &realClient{producers: make(map[string]*azeventhubs.ProducerClient), consumers: make(map[string]*azeventhubs.ConsumerClient), partitions: make(map[string]*azeventhubs.PartitionClient)}
		for i := 0; i < maxEventHubsConsumers; i++ {
			client.consumers[fmt.Sprintf("hub-%d\x00group", i)] = nil
		}
		if _, err := client.consumer("overflow", "group"); err == nil || !strings.Contains(err.Error(), "capacity") {
			t.Fatalf("consumer overflow error = %v", err)
		}
	})
	t.Run("partitions", func(t *testing.T) {
		client := &realClient{producers: make(map[string]*azeventhubs.ProducerClient), consumers: map[string]*azeventhubs.ConsumerClient{"hub\x00group": nil}, partitions: make(map[string]*azeventhubs.PartitionClient)}
		for i := 0; i < maxEventHubsPartitionClients; i++ {
			client.partitions[fmt.Sprintf("hub\x00group\x00%d", i)] = nil
		}
		if _, err := client.partitionClient("hub", "group", "overflow"); err == nil || !strings.Contains(err.Error(), "capacity") {
			t.Fatalf("partition overflow error = %v", err)
		}
	})
}

// TestRealClientSendRequiresEndpoint stays offline: with no namespace and no
// connection string, producer construction fails before any network call.
func TestRealClientSendRequiresEndpoint(t *testing.T) {
	c := newRealClient(config{})
	_, err := c.Send(context.Background(), "h", event{Body: []byte("x")})
	if err == nil {
		t.Fatal("Send succeeded without an endpoint, want error")
	}
	if !strings.Contains(err.Error(), "namespace") && !strings.Contains(err.Error(), "connection_string") {
		t.Fatalf("expected endpoint-required error, got: %v", err)
	}
}

func TestRealClientUseConnString(t *testing.T) {
	cases := []struct {
		name string
		cfg  config
		want bool
	}{
		{"auth_aad pins credential", config{AuthType: "aad", ConnectionString: "cs"}, false},
		{"auth_connection_string pins SAS", config{AuthType: "connection_string", Namespace: "ns"}, true},
		{"auto prefers connection string", config{ConnectionString: "cs"}, true},
		{"auto falls back to namespace", config{Namespace: "ns"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rc := &realClient{cfg: tc.cfg}
			if got := rc.useConnString(); got != tc.want {
				t.Fatalf("useConnString() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRealClientConnStringAuthMissingConnString(t *testing.T) {
	c := newRealClient(config{AuthType: "connection_string", Namespace: "ns"})
	_, err := c.Send(context.Background(), "h", event{Body: []byte("x")})
	if err == nil || !strings.Contains(err.Error(), "connection_string is required") {
		t.Fatalf("expected connection_string-required error, got: %v", err)
	}
}

func TestRealClientAADAuthRequiresOperatorNamespaceBinding(t *testing.T) {
	t.Setenv(identitypolicy.AzureMessagingAllowedNamespacesEnv, "")
	client := &realClient{cfg: config{AuthType: "aad", Namespace: "orders.servicebus.windows.net"}}
	if _, err := client.credentialLocked(); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("unbound AAD credential error = %v", err)
	}
	t.Setenv(identitypolicy.AzureMessagingAllowedNamespacesEnv, "audit.servicebus.windows.net")
	if _, err := client.credentialLocked(); err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("cross-namespace AAD credential error = %v", err)
	}
}

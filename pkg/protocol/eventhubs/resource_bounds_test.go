package eventhubs

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestEventHubsInitRejectsOversizedTimeout(t *testing.T) {
	for _, value := range []any{float64(MaxConfiguredTimeout.Milliseconds() + 1), math.Inf(1), 1.5} {
		instance := &EventHubs{name: eventHubsProtocolName}
		if err := instance.Init(context.Background(), map[string]any{"eventhubs_mock": true, "eventhubs_timeout_ms": value}); err == nil {
			t.Fatalf("Init accepted timeout %v", value)
		}
	}
}

func TestEventHubsRejectsOversizedDataDrivenHubName(t *testing.T) {
	instance := &EventHubs{name: eventHubsProtocolName}
	if err := instance.Init(context.Background(), map[string]any{"eventhubs_mock": true}); err != nil {
		t.Fatal(err)
	}
	defer instance.Teardown(context.Background())
	result, err := instance.Execute(context.Background(), &protocol.Action{
		Name: "produce", Type: "eventhubs_produce",
		Target: strings.Repeat("h", MaxEventHubNameBytes+1), Body: []byte("x"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Success || result.StatusCode != 400 || !strings.Contains(result.ErrorMessage, "name exceeds") {
		t.Fatalf("oversized hub result = %#v", result)
	}
}

func TestEventHubsMemoryBrokerHasAggregateRetentionBudget(t *testing.T) {
	broker := newMemoryBroker()
	broker.totalBytes = maxBrokerRetainedBytes
	if _, err := broker.send("orders", event{Body: []byte("x")}); err == nil || !strings.Contains(err.Error(), "aggregate") {
		t.Fatalf("send at aggregate cap error = %v", err)
	}
	if len(broker.logs) != 0 {
		t.Fatal("rejected aggregate-cap send mutated broker logs")
	}
}

func TestEventHubsHardEventCapIncludesProperties(t *testing.T) {
	instance := &EventHubs{client: mockClient{broker: newMemoryBroker()}, cfg: config{EventHub: "orders"}}
	result, err := instance.Execute(context.Background(), &protocol.Action{
		Name: "produce", Type: "eventhubs_produce", Body: make([]byte, MaxEventHubsEventBytes),
		Headers: map[string]string{"extra": "x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Success || !strings.Contains(result.ErrorMessage, "payload and properties") {
		t.Fatalf("oversized event result = %#v", result)
	}
}

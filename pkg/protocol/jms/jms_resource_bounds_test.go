package jms

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestJMSRealAdapterRejectsUnsupportedSettlementModes(t *testing.T) {
	p := &JMS{name: "jms-solace", provider: "solace"}
	err := p.Init(context.Background(), map[string]any{
		"broker_url": "amqps://broker.example:5671", "jms_mock": false, "jms_acknowledgement": "client",
	})
	if err == nil || !strings.Contains(err.Error(), "not supported by real adapters") {
		t.Fatalf("Init error = %v, want fail-closed settlement error", err)
	}
}

func TestJMSMemoryBrokerBoundsSubscriptionKeys(t *testing.T) {
	b := &memoryBroker{queues: map[string][]message{}, topics: map[string]map[string][]message{}, refs: map[string]int{}}
	for i := 0; i < maxJMSDestinationKeys*2; i++ {
		b.subscribe("events", "subscriber-"+strconv.Itoa(i))
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if got := b.resourceKeyCountLocked(); got > maxJMSDestinationKeys {
		t.Fatalf("broker resource keys = %d, cap = %d", got, maxJMSDestinationKeys)
	}
}

func TestJMSMockNamespacesDoNotShareMessagesAndCleanUp(t *testing.T) {
	ctx := context.Background()
	newProtocol := func(namespace string) *JMS {
		p := &JMS{name: "jms-solace", provider: "solace"}
		if err := p.Init(ctx, map[string]any{
			"jms_mock": true, "jms_destination": "orders", "jms_timeout_ms": 10,
			"runtime_mock_namespace": namespace,
		}); err != nil {
			t.Fatalf("Init(%s): %v", namespace, err)
		}
		return p
	}
	one := newProtocol("test-jms-one")
	two := newProtocol("test-jms-two")
	if result, _ := one.Execute(ctx, &protocol.Action{Name: "publish", Type: "jms_publish", Body: []byte("one")}); !result.Success {
		t.Fatalf("publish failed: %s", result.ErrorMessage)
	}
	if result, _ := two.Execute(ctx, &protocol.Action{Name: "consume", Type: "jms_consume"}); result.Success {
		t.Fatal("second mock namespace consumed the first namespace's message")
	}
	if result, _ := one.Execute(ctx, &protocol.Action{Name: "consume", Type: "jms_consume"}); !result.Success {
		t.Fatalf("own namespace consume failed: %s", result.ErrorMessage)
	}
	_ = one.Teardown(ctx)
	_ = two.Teardown(ctx)
	broker.mu.Lock()
	defer broker.mu.Unlock()
	for key := range broker.queues {
		if strings.HasPrefix(key, "test-jms-one\x00") || strings.HasPrefix(key, "test-jms-two\x00") {
			t.Fatalf("mock namespace key survived teardown: %q", key)
		}
	}
}

func TestJMSMemoryQueueIsCountAndByteBounded(t *testing.T) {
	b := &memoryBroker{queues: map[string][]message{}, topics: map[string]map[string][]message{}, refs: map[string]int{}}
	payload := make([]byte, 1<<20)
	for i := 0; i < maxJMSQueueDepth+1; i++ {
		b.publish("q", message{Body: payload, CreatedAt: time.Now()})
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.queues["q"]) > maxJMSQueueDepth {
		t.Fatalf("queue depth = %d, cap = %d", len(b.queues["q"]), maxJMSQueueDepth)
	}
	total := 0
	for _, msg := range b.queues["q"] {
		total += messageSize(msg)
	}
	if total > maxJMSQueueBytes {
		t.Fatalf("queue bytes = %d, cap = %d", total, maxJMSQueueBytes)
	}
}

func TestJMSMemoryPublishCapacityReturnsErrorWithoutDroppingLiveDestination(t *testing.T) {
	b := &memoryBroker{queues: map[string][]message{}, topics: map[string]map[string][]message{}, refs: map[string]int{}}
	liveDestination := mockJMSKey("live-run", "orders")
	b.acquire("live-run")
	defer b.release("live-run")

	b.mu.Lock()
	b.queues[liveDestination] = []message{{Body: []byte("live")}}
	for i := 1; i < maxJMSDestinationKeys; i++ {
		b.queues["occupied-"+strconv.Itoa(i)] = nil
	}
	b.mu.Unlock()

	adapter := memoryAdapter{broker: b, namespace: "new-run"}
	if _, err := adapter.Publish(context.Background(), "orders", message{Body: []byte("new")}); err == nil || !strings.Contains(err.Error(), "capacity") {
		t.Fatalf("Publish error = %v, want explicit capacity error", err)
	}
	protocolUnderTest := &JMS{
		name: "jms-solace", provider: "solace",
		cfg: config{Destination: "orders", Mock: true}, adapter: adapter,
	}
	result, err := protocolUnderTest.Execute(context.Background(), &protocol.Action{Name: "publish", Type: "jms_publish", Body: []byte("new")})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Success || !strings.Contains(result.ErrorMessage, "capacity") {
		t.Fatalf("Execute result = %#v, want failed publish with capacity error", result)
	}
	if result.ServerTelemetry == nil || result.ServerTelemetry.SendFailureCount != 1 {
		t.Fatalf("send failure telemetry = %#v, want one failed publish", result.ServerTelemetry)
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.queues[mockJMSKey("new-run", "orders")]; exists {
		t.Fatal("failed publish created the new destination")
	}
	if got := b.queues[liveDestination]; len(got) != 1 || string(got[0].Body) != "live" {
		t.Fatalf("live destination was changed or dropped: %#v", got)
	}
	if b.resourceKeyCountLocked() != maxJMSDestinationKeys {
		t.Fatalf("resource keys = %d, want unchanged cap %d", b.resourceKeyCountLocked(), maxJMSDestinationKeys)
	}
}

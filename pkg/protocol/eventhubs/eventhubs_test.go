package eventhubs

import (
	"context"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// newMockEventHubs builds a mock-backed protocol instance isolated from the
// package-level shared broker so tests don't leak events into each other.
func newMockEventHubs(t *testing.T, cfg map[string]any) *EventHubs {
	t.Helper()
	e := &EventHubs{name: eventHubsProtocolName}
	if cfg == nil {
		cfg = map[string]any{}
	}
	cfg["eventhubs_mock"] = true
	if err := e.Init(context.Background(), cfg); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	e.client = mockClient{broker: newMemoryBroker()}
	return e
}

func TestEventHubsProtocolIsRegistered(t *testing.T) {
	p, err := protocol.Get("eventhubs")
	if err != nil {
		t.Fatalf("protocol.Get(eventhubs) returned error: %v", err)
	}
	if p.Name() != "eventhubs" {
		t.Fatalf("Name() = %q, want eventhubs", p.Name())
	}
}

func TestEventHubsInitEnforcesResolvedConnectionTargetPolicy(t *testing.T) {
	e := &EventHubs{name: eventHubsProtocolName}
	err := e.Init(context.Background(), map[string]any{
		"eventhubs_connection_string_secret": "Endpoint=sb://attacker.servicebus.windows.net/;SharedAccessKey=secret",
		protocol.TargetPolicyConfigKey:       []string{"orders.servicebus.windows.net"},
	})
	if err == nil || !strings.Contains(err.Error(), "attacker.servicebus.windows.net") || strings.Contains(err.Error(), "SharedAccessKey") {
		t.Fatalf("Init policy error = %v, want sanitized Event Hubs target rejection", err)
	}
}

func TestEventHubsInitDefaults(t *testing.T) {
	e := newMockEventHubs(t, map[string]any{})
	if e.cfg.ConsumerGroup != "$Default" {
		t.Fatalf("ConsumerGroup = %q, want $Default", e.cfg.ConsumerGroup)
	}
	if e.cfg.Timeout != defaultTimeout {
		t.Fatalf("Timeout = %s, want %s", e.cfg.Timeout, defaultTimeout)
	}
	if !e.cfg.Mock {
		t.Fatal("Mock = false, want true")
	}
}

func TestEventHubsConnectionStringSecretFallback(t *testing.T) {
	e := &EventHubs{name: eventHubsProtocolName}
	if err := e.Init(context.Background(), map[string]any{
		"eventhubs_mock":                     true,
		"eventhubs_connection_string_secret": "Endpoint=sb://ns.servicebus.windows.net/;SharedAccessKey=xxx",
	}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	if !strings.Contains(e.cfg.ConnectionString, "servicebus.windows.net") {
		t.Fatalf("connection string secret not used as fallback: %q", e.cfg.ConnectionString)
	}
}

func TestEventHubsProduceConsumeRoundTrip(t *testing.T) {
	ctx := context.Background()
	e := newMockEventHubs(t, map[string]any{
		"eventhubs_event_hub":  "telemetry",
		"eventhubs_timeout_ms": float64(250),
	})

	produced, err := e.Execute(ctx, &protocol.Action{
		Name:    "publish-event",
		Type:    "eventhubs_produce",
		Body:    []byte(`{"deviceId":"d-1","temp":21.5}`),
		Headers: map[string]string{"source": "sensor"},
		Params:  map[string]any{"partition_key": "d-1"},
		Assertions: []protocol.Assertion{
			{Type: "message_received"},
			{Type: "body_contains", Value: "deviceId"},
		},
	})
	if err != nil {
		t.Fatalf("produce returned error: %v", err)
	}
	if !produced.Success {
		t.Fatalf("produce success = false: %s", produced.ErrorMessage)
	}
	if produced.BytesSent == 0 {
		t.Fatal("produce BytesSent = 0, want payload bytes")
	}
	if produced.Extractions["eventhubs_offset"] != "0" {
		t.Fatalf("eventhubs_offset = %q, want 0", produced.Extractions["eventhubs_offset"])
	}
	if produced.Extractions["eventhubs_operation"] != "produce" {
		t.Fatalf("eventhubs_operation = %q, want produce", produced.Extractions["eventhubs_operation"])
	}
	if produced.ServerTelemetry == nil || produced.ServerTelemetry.MessagesSent != 1 {
		t.Fatalf("MessagesSent telemetry not set: %+v", produced.ServerTelemetry)
	}

	consumed, err := e.Execute(ctx, &protocol.Action{
		Name:   "observe-event",
		Type:   "eventhubs_consume",
		Params: map[string]any{"event_hub": "telemetry"},
		Extractors: []protocol.Extractor{
			{Name: "device", Source: "property", Expression: "source"},
		},
		Assertions: []protocol.Assertion{
			{Type: "message_received"},
			{Type: "property_equals", Value: "source", Expected: "sensor"},
		},
	})
	if err != nil {
		t.Fatalf("consume returned error: %v", err)
	}
	if !consumed.Success {
		t.Fatalf("consume success = false: %s", consumed.ErrorMessage)
	}
	if consumed.BytesReceived == 0 {
		t.Fatal("consume BytesReceived = 0, want payload bytes")
	}
	if consumed.Extractions["device"] != "sensor" {
		t.Fatalf("property extractor device = %q, want sensor", consumed.Extractions["device"])
	}
	if consumed.Extractions["eventhubs_consumer_lag"] != "0" {
		t.Fatalf("eventhubs_consumer_lag = %q, want 0 (drained)", consumed.Extractions["eventhubs_consumer_lag"])
	}
	if consumed.ServerTelemetry == nil || consumed.ServerTelemetry.MessagesReceived != 1 {
		t.Fatalf("MessagesReceived telemetry not set: %+v", consumed.ServerTelemetry)
	}
}

func TestEventHubsConsumerLagReflectsBacklog(t *testing.T) {
	ctx := context.Background()
	e := newMockEventHubs(t, map[string]any{"eventhubs_event_hub": "stream", "eventhubs_timeout_ms": float64(250)})

	for i := 0; i < 3; i++ {
		if _, err := e.Execute(ctx, &protocol.Action{Name: "p", Type: "eventhubs_produce", Body: []byte("x")}); err != nil {
			t.Fatalf("produce %d error: %v", i, err)
		}
	}
	consumed, err := e.Execute(ctx, &protocol.Action{Name: "c", Type: "eventhubs_consume"})
	if err != nil {
		t.Fatalf("consume error: %v", err)
	}
	// 3 events produced, 1 consumed -> 2 remain behind the tail.
	if consumed.Extractions["eventhubs_consumer_lag"] != "2" {
		t.Fatalf("eventhubs_consumer_lag = %q, want 2", consumed.Extractions["eventhubs_consumer_lag"])
	}
	if consumed.ServerTelemetry == nil || consumed.ServerTelemetry.ConsumerOffsetLag != 2 {
		t.Fatalf("ConsumerOffsetLag telemetry not set to 2: %+v", consumed.ServerTelemetry)
	}
}

func TestEventHubsProduceRequiresHub(t *testing.T) {
	ctx := context.Background()
	e := newMockEventHubs(t, map[string]any{}) // no default event hub
	result, err := e.Execute(ctx, &protocol.Action{Name: "p", Type: "eventhubs_produce", Body: []byte("x")})
	if err != nil {
		t.Fatalf("Execute returned transport error: %v", err)
	}
	if result.Success {
		t.Fatal("produce succeeded without an event hub name, want failure")
	}
	if !strings.Contains(result.ErrorMessage, "event hub name is required") {
		t.Fatalf("unexpected error: %q", result.ErrorMessage)
	}
}

func TestEventHubsUnsupportedAction(t *testing.T) {
	ctx := context.Background()
	e := newMockEventHubs(t, map[string]any{"eventhubs_event_hub": "h"})
	result, err := e.Execute(ctx, &protocol.Action{Name: "bad", Type: "eventhubs_frobnicate", Body: []byte("x")})
	if err != nil {
		t.Fatalf("Execute returned transport error: %v", err)
	}
	if result.Success {
		t.Fatal("unsupported action succeeded, want failure")
	}
	if !strings.Contains(result.ErrorMessage, "unsupported eventhubs action") {
		t.Fatalf("unexpected error: %q", result.ErrorMessage)
	}
}

package jms

import (
	"context"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func newP23JMS(t *testing.T, cfg map[string]any) *JMS {
	t.Helper()
	p := &JMS{name: "jms-solace", provider: "solace"}
	base := map[string]any{"jms_destination": "p23.q", "jms_mock": true, "jms_timeout_ms": float64(250)}
	for k, v := range cfg {
		base[k] = v
	}
	if err := p.Init(context.Background(), base); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return p
}

func TestBuildMessageAppliesDeliveryModePriorityType(t *testing.T) {
	p := newP23JMS(t, map[string]any{"jms_delivery_mode": "persistent"})

	// delivery_mode falls back to the connection default (closing the dead config).
	msg := p.buildMessage(&protocol.Action{Type: "jms_publish", Params: map[string]any{"message_type": "text", "priority": 3}}, "p23.q")
	if msg.DeliveryMode != "persistent" {
		t.Fatalf("DeliveryMode = %q, want persistent (config fallback)", msg.DeliveryMode)
	}
	if msg.Type != "text" {
		t.Fatalf("Type = %q, want text", msg.Type)
	}
	if msg.Priority == nil || *msg.Priority != 3 {
		t.Fatalf("Priority = %v, want 3", msg.Priority)
	}

	// Per-action delivery_mode overrides the config default.
	over := p.buildMessage(&protocol.Action{Type: "jms_publish", Params: map[string]any{"delivery_mode": "non_persistent"}}, "p23.q")
	if over.DeliveryMode != "non_persistent" {
		t.Fatalf("override DeliveryMode = %q, want non_persistent", over.DeliveryMode)
	}
}

func TestBuildMessagePriorityClamped(t *testing.T) {
	p := newP23JMS(t, nil)
	for _, tc := range []struct {
		in   int
		want uint8
	}{{99, 9}, {-5, 0}, {0, 0}, {9, 9}, {4, 4}} {
		msg := p.buildMessage(&protocol.Action{Type: "jms_publish", Params: map[string]any{"priority": tc.in}}, "p23.q")
		if msg.Priority == nil || *msg.Priority != tc.want {
			t.Fatalf("priority %d clamped to %v, want %d", tc.in, msg.Priority, tc.want)
		}
	}
}

func TestBuildMessageMapBodyEncodedToJSON(t *testing.T) {
	p := newP23JMS(t, nil)
	msg := p.buildMessage(&protocol.Action{
		Type:   "jms_publish",
		Params: map[string]any{"message_type": "map", "map_body": map[string]any{"orderId": "A-1", "qty": 5}},
	}, "p23.q")
	if msg.Type != "map" {
		t.Fatalf("Type = %q, want map", msg.Type)
	}
	if msg.MapBody["orderId"] != "A-1" || msg.MapBody["qty"] != "5" {
		t.Fatalf("MapBody = %#v, want orderId=A-1 qty=5", msg.MapBody)
	}
	if len(msg.Body) == 0 {
		t.Fatal("Body should be the JSON encoding of the map")
	}
}

func TestMapMessageRoundTripThroughMock(t *testing.T) {
	ctx := context.Background()
	p := newP23JMS(t, nil)

	if _, err := p.Execute(ctx, &protocol.Action{
		Name:   "publish-map",
		Type:   "jms_publish",
		Params: map[string]any{"message_type": "map", "map_body": map[string]any{"orderId": "A-9"}},
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	consumed, err := p.Execute(ctx, &protocol.Action{
		Name: "consume-map",
		Type: "jms_consume",
		Extractors: []protocol.Extractor{{
			Name: "order_id", Source: "body", Type: "jsonpath", Expression: "$.orderId",
		}},
	})
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if !consumed.Success {
		t.Fatalf("consume failed: %s", consumed.ErrorMessage)
	}
	if consumed.Extractions["order_id"] != "A-9" {
		t.Fatalf("order_id = %q, want A-9 (map field via JSON body)", consumed.Extractions["order_id"])
	}
	if consumed.Extractions["jms_message_type"] != "map" {
		t.Fatalf("jms_message_type = %q, want map", consumed.Extractions["jms_message_type"])
	}
}

func TestStreamMessageEncodedToJSONArray(t *testing.T) {
	p := newP23JMS(t, nil)
	msg := p.buildMessage(&protocol.Action{
		Type:   "jms_publish",
		Params: map[string]any{"message_type": "stream", "stream_body": []any{"a", "b", 3}},
	}, "p23.q")
	if msg.Type != "stream" {
		t.Fatalf("Type = %q, want stream", msg.Type)
	}
	if len(msg.StreamBody) != 3 || msg.StreamBody[0] != "a" || msg.StreamBody[2] != "3" {
		t.Fatalf("StreamBody = %#v, want [a b 3]", msg.StreamBody)
	}
	if string(msg.Body) != `["a","b","3"]` {
		t.Fatalf("Body = %q, want JSON array", string(msg.Body))
	}
}

func TestExplicitBodyNotOverwrittenByTypedBody(t *testing.T) {
	p := newP23JMS(t, nil)
	// When an explicit body is supplied alongside map_body, the explicit body wins.
	msg := p.buildMessage(&protocol.Action{
		Type:   "jms_publish",
		Body:   []byte(`{"explicit":true}`),
		Params: map[string]any{"message_type": "map", "map_body": map[string]any{"a": "1"}},
	}, "p23.q")
	if string(msg.Body) != `{"explicit":true}` {
		t.Fatalf("Body = %q, want the explicit body preserved", string(msg.Body))
	}
	if msg.MapBody["a"] != "1" {
		t.Fatal("MapBody should still be populated for adapter fidelity")
	}
}

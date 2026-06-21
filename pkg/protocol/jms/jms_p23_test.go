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

func TestConsumeWithSelectorMock(t *testing.T) {
	ctx := context.Background()
	p := newP23JMS(t, map[string]any{"jms_destination": "sel.q"})
	for _, r := range []string{"us", "eu", "ap"} {
		if _, err := p.Execute(ctx, &protocol.Action{
			Name: "pub", Type: "jms_publish", Body: []byte(r),
			Headers: map[string]string{"region": r},
		}); err != nil {
			t.Fatalf("publish %s: %v", r, err)
		}
	}
	// Selector picks the matching message out of the middle of the queue.
	consumed, _ := p.Execute(ctx, &protocol.Action{
		Name: "consume", Type: "jms_consume",
		Params: map[string]any{"selector": "region = 'eu'"},
	})
	if !consumed.Success {
		t.Fatalf("selector consume failed: %s", consumed.ErrorMessage)
	}
	if consumed.Extractions["jms_body"] != "eu" {
		t.Fatalf("selected body = %q, want eu", consumed.Extractions["jms_body"])
	}
	// us and ap remain (non-matching messages were not consumed).
	remaining := map[string]bool{}
	for i := 0; i < 2; i++ {
		c, _ := p.Execute(ctx, &protocol.Action{Name: "c", Type: "jms_consume"})
		if !c.Success {
			t.Fatalf("expected remaining message, iter %d: %s", i, c.ErrorMessage)
		}
		remaining[c.Extractions["jms_body"]] = true
	}
	if !remaining["us"] || !remaining["ap"] {
		t.Fatalf("remaining = %v, want us+ap", remaining)
	}
}

func TestBrowseNonDestructiveMock(t *testing.T) {
	ctx := context.Background()
	p := newP23JMS(t, map[string]any{"jms_destination": "browse.q"})
	for _, b := range []string{"m1", "m2", "m3"} {
		if _, err := p.Execute(ctx, &protocol.Action{Name: "pub", Type: "jms_publish", Body: []byte(b)}); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	br, _ := p.Execute(ctx, &protocol.Action{Name: "browse", Type: "jms_browse"})
	if !br.Success {
		t.Fatalf("browse failed: %s", br.ErrorMessage)
	}
	if br.Extractions["jms_operation"] != "browse" {
		t.Fatalf("jms_operation = %q, want browse", br.Extractions["jms_operation"])
	}
	if br.Extractions["jms_browse_count"] != "3" {
		t.Fatalf("jms_browse_count = %q, want 3", br.Extractions["jms_browse_count"])
	}
	// All three messages must still be consumable (browse did not remove them).
	for i := 0; i < 3; i++ {
		c, _ := p.Execute(ctx, &protocol.Action{Name: "c", Type: "jms_consume"})
		if !c.Success {
			t.Fatalf("message %d missing after browse: %s", i, c.ErrorMessage)
		}
	}
}

func TestBrowseWithSelectorMock(t *testing.T) {
	ctx := context.Background()
	p := newP23JMS(t, map[string]any{"jms_destination": "bsel.q"})
	for _, r := range []string{"us", "eu", "eu", "ap"} {
		p.Execute(ctx, &protocol.Action{Name: "pub", Type: "jms_publish", Body: []byte(r), Headers: map[string]string{"region": r}})
	}
	br, _ := p.Execute(ctx, &protocol.Action{
		Name: "browse", Type: "jms_browse",
		Params: map[string]any{"selector": "region = 'eu'"},
	})
	if !br.Success {
		t.Fatalf("browse failed: %s", br.ErrorMessage)
	}
	if br.Extractions["jms_browse_count"] != "2" {
		t.Fatalf("selector browse count = %q, want 2", br.Extractions["jms_browse_count"])
	}
}

func TestConsumeSelectorInvalidFailsClosed(t *testing.T) {
	ctx := context.Background()
	p := newP23JMS(t, map[string]any{"jms_destination": "bad.q"})
	res, _ := p.Execute(ctx, &protocol.Action{
		Name: "c", Type: "jms_consume",
		Params: map[string]any{"selector": "region =="},
	})
	if res.Success {
		t.Fatal("invalid selector should fail the action")
	}
	if res.StatusCode != 400 {
		t.Fatalf("status = %d, want 400 for a malformed selector", res.StatusCode)
	}
}

func TestMockImplementsSelectorAndBrowser(t *testing.T) {
	var a brokerAdapter = memoryAdapter{broker: broker}
	if _, ok := a.(selectiveConsumer); !ok {
		t.Fatal("mock must implement selectiveConsumer")
	}
	if _, ok := a.(messageBrowser); !ok {
		t.Fatal("mock must implement messageBrowser")
	}
}

func TestConsumeSelectorRejectsAdvancedCombo(t *testing.T) {
	ctx := context.Background()
	p := newP23JMS(t, map[string]any{"jms_destination": "combo.q"})
	res, _ := p.Execute(ctx, &protocol.Action{
		Name: "c", Type: "jms_consume",
		Params: map[string]any{"selector": "region = 'eu'", "peek_lock": true},
	})
	if res.Success {
		t.Fatal("selector + peek_lock must be rejected, not silently mishandled")
	}
	if res.StatusCode != 400 {
		t.Fatalf("status = %d, want 400", res.StatusCode)
	}
}

func TestBrowseDoesNotAliasBrokerState(t *testing.T) {
	ctx := context.Background()
	p := newP23JMS(t, map[string]any{"jms_destination": "alias.q"})
	if _, err := p.Execute(ctx, &protocol.Action{
		Name: "pub", Type: "jms_publish", Body: []byte("original"),
		Headers: map[string]string{"k": "v"},
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	// Browse the queue and mutate the returned copy's reference fields.
	browsed := broker.browse("alias.q", nil, 10)
	if len(browsed) != 1 {
		t.Fatalf("expected 1 browsed message, got %d", len(browsed))
	}
	browsed[0].Headers["k"] = "mutated"
	if len(browsed[0].Body) > 0 {
		browsed[0].Body[0] = 'X'
	}
	// A subsequent destructive consume must see the untouched original.
	consumed, _ := p.Execute(ctx, &protocol.Action{Name: "c", Type: "jms_consume"})
	if !consumed.Success {
		t.Fatalf("consume failed: %s", consumed.ErrorMessage)
	}
	if consumed.Extractions["jms_body"] != "original" {
		t.Fatalf("browse mutated the queued body: got %q", consumed.Extractions["jms_body"])
	}
}

func TestTopicFanoutMock(t *testing.T) {
	ctx := context.Background()
	const topic = "fanout.topic.test"
	p := newP23JMS(t, map[string]any{"jms_destination": topic, "jms_destination_type": "topic"})
	// Register two subscriptions, then publish once: each must receive its own copy.
	broker.subscribe(topic, "subA")
	broker.subscribe(topic, "subB")
	if _, err := p.Execute(ctx, &protocol.Action{Name: "pub", Type: "jms_publish", Body: []byte("hello-topic")}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	for _, sub := range []string{"subA", "subB"} {
		c, _ := p.Execute(ctx, &protocol.Action{
			Name: "c", Type: "jms_consume",
			Params: map[string]any{"subscription": sub, "subscription_mode": "durable"},
		})
		if !c.Success {
			t.Fatalf("subscription %s received nothing: %s", sub, c.ErrorMessage)
		}
		if c.Extractions["jms_body"] != "hello-topic" {
			t.Fatalf("subscription %s body = %q, want hello-topic", sub, c.Extractions["jms_body"])
		}
	}
	// Cursors are independent: subA already consumed; its queue is now empty while
	// a re-publish reaches both again.
	if _, err := p.Execute(ctx, &protocol.Action{Name: "pub2", Type: "jms_publish", Body: []byte("second")}); err != nil {
		t.Fatalf("publish2: %v", err)
	}
	cA, _ := p.Execute(ctx, &protocol.Action{Name: "cA", Type: "jms_consume", Params: map[string]any{"subscription": "subA"}})
	if cA.Extractions["jms_body"] != "second" {
		t.Fatalf("subA second body = %q, want second", cA.Extractions["jms_body"])
	}
}

func TestTopicConsumeRegistersSubscription(t *testing.T) {
	ctx := context.Background()
	const topic = "register.topic.test"
	p := newP23JMS(t, map[string]any{"jms_destination": topic, "jms_destination_type": "topic", "jms_timeout_ms": float64(40)})
	// A consume registers the durable subscription even though it times out with
	// no message — so a later publish fans out to it.
	first, _ := p.Execute(ctx, &protocol.Action{Name: "c0", Type: "jms_consume", Params: map[string]any{"subscription": "late", "subscription_mode": "durable"}})
	if first.Success {
		t.Fatal("expected the first consume to time out (no message yet)")
	}
	if _, err := p.Execute(ctx, &protocol.Action{Name: "pub", Type: "jms_publish", Body: []byte("after")}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	got, _ := p.Execute(ctx, &protocol.Action{Name: "c1", Type: "jms_consume", Params: map[string]any{"subscription": "late"}})
	if !got.Success || got.Extractions["jms_body"] != "after" {
		t.Fatalf("durable subscription did not retain the message: success=%v body=%q", got.Success, got.Extractions["jms_body"])
	}
}

func TestTopicConsumeRequiresSubscription(t *testing.T) {
	ctx := context.Background()
	p := newP23JMS(t, map[string]any{"jms_destination": "nosub.topic.test", "jms_destination_type": "topic"})
	res, _ := p.Execute(ctx, &protocol.Action{Name: "c", Type: "jms_consume"})
	if res.Success {
		t.Fatal("topic consume without a subscription must fail")
	}
	if res.ErrorMessage == "" {
		t.Fatal("expected an error message about the missing subscription")
	}
}

func TestTopicBrowseMock(t *testing.T) {
	ctx := context.Background()
	const topic = "browse.topic.test"
	p := newP23JMS(t, map[string]any{"jms_destination": topic, "jms_destination_type": "topic"})
	broker.subscribe(topic, "bsub")
	for _, b := range []string{"x", "y"} {
		p.Execute(ctx, &protocol.Action{Name: "pub", Type: "jms_publish", Body: []byte(b)})
	}
	br, _ := p.Execute(ctx, &protocol.Action{Name: "browse", Type: "jms_browse", Params: map[string]any{"subscription": "bsub"}})
	if !br.Success || br.Extractions["jms_browse_count"] != "2" {
		t.Fatalf("topic browse count = %q (success=%v), want 2", br.Extractions["jms_browse_count"], br.Success)
	}
	// Non-destructive: the two messages are still consumable.
	for i := 0; i < 2; i++ {
		c, _ := p.Execute(ctx, &protocol.Action{Name: "c", Type: "jms_consume", Params: map[string]any{"subscription": "bsub"}})
		if !c.Success {
			t.Fatalf("message %d gone after topic browse", i)
		}
	}
}

func TestMockImplementsTopicAdapter(t *testing.T) {
	var a brokerAdapter = memoryAdapter{broker: broker}
	if _, ok := a.(topicAdapter); !ok {
		t.Fatal("mock must implement topicAdapter")
	}
}

func TestDurableSubscriptionPreRegisteredAtInit(t *testing.T) {
	ctx := context.Background()
	const topic = "preinit.topic.test"
	// A configured topic subscription is registered at Init, so a publish that
	// happens before any consume is still captured (durable semantics).
	pub := newP23JMS(t, map[string]any{"jms_destination": topic, "jms_destination_type": "topic", "jms_subscription": "dura"})
	if _, err := pub.Execute(ctx, &protocol.Action{Name: "pub", Type: "jms_publish", Body: []byte("early")}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	// Now consume the durable subscription — it must have retained the early message.
	got, _ := pub.Execute(ctx, &protocol.Action{Name: "c", Type: "jms_consume", Params: map[string]any{"subscription": "dura", "subscription_mode": "durable"}})
	if !got.Success || got.Extractions["jms_body"] != "early" {
		t.Fatalf("durable sub registered at Init did not retain the early publish: success=%v body=%q", got.Success, got.Extractions["jms_body"])
	}
}

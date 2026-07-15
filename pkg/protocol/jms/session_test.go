package jms

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestJMSPendingByteAdmissionRequeuesRejectedDelivery(t *testing.T) {
	ctx := context.Background()
	p := &JMS{name: "jms-solace", provider: "solace"}
	if err := p.Init(ctx, map[string]any{
		"jms_destination":     "orders.pending-bytes.test",
		"jms_mock":            true,
		"jms_acknowledgement": "client",
		"jms_timeout_ms":      float64(250),
	}); err != nil {
		t.Fatal(err)
	}
	defer p.Teardown(ctx)
	body := bytes.Repeat([]byte("x"), 5<<20)
	for i := 0; i < 2; i++ {
		result, err := p.Execute(ctx, &protocol.Action{Name: "publish", Type: "jms_publish", Body: body})
		if err != nil || !result.Success {
			t.Fatalf("publish %d = (%#v, %v)", i, result, err)
		}
	}
	first, _ := p.Execute(ctx, &protocol.Action{Name: "consume-1", Type: "jms_consume"})
	if !first.Success || p.session.Pending() != 1 {
		t.Fatalf("first consume success=%v pending=%d", first.Success, p.session.Pending())
	}
	overflow, _ := p.Execute(ctx, &protocol.Action{Name: "consume-overflow", Type: "jms_consume"})
	if overflow.Success || !strings.Contains(overflow.ErrorMessage, "capacity") || p.session.Pending() != 1 {
		t.Fatalf("overflow consume = success %v error %q pending %d", overflow.Success, overflow.ErrorMessage, p.session.Pending())
	}
	if ack, _ := p.Execute(ctx, &protocol.Action{Name: "ack", Type: "jms_ack"}); !ack.Success {
		t.Fatalf("ack failed: %s", ack.ErrorMessage)
	}
	retried, _ := p.Execute(ctx, &protocol.Action{Name: "consume-requeued", Type: "jms_consume"})
	if !retried.Success || retried.BytesReceived != int64(len(body)) {
		t.Fatalf("requeued consume = success %v bytes %d error %q", retried.Success, retried.BytesReceived, retried.ErrorMessage)
	}
}

// TestJMSTransactedRollbackRedelivery exercises the P1.2 session: a transacted
// consume that is rolled back requeues the message for redelivery (counted) and
// records an abort; a subsequent commit records a commit.
func TestJMSTransactedRollbackRedelivery(t *testing.T) {
	ctx := context.Background()
	p := &JMS{name: "jms-solace", provider: "solace"}
	if err := p.Init(ctx, map[string]any{
		"jms_destination":     "orders.tx.rollback.test",
		"jms_mock":            true,
		"jms_acknowledgement": "transacted",
		"jms_timeout_ms":      float64(250),
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	if _, err := p.Execute(ctx, &protocol.Action{Name: "pub", Type: "jms_publish", Body: []byte(`{"id":1}`)}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	first, err := p.Execute(ctx, &protocol.Action{Name: "c1", Type: "jms_consume"})
	if err != nil || !first.Success {
		t.Fatalf("first consume failed: err=%v msg=%s", err, first.ErrorMessage)
	}
	if first.ServerTelemetry != nil && first.ServerTelemetry.RedeliveryCount != 0 {
		t.Fatalf("first consume should not be a redelivery, got %d", first.ServerTelemetry.RedeliveryCount)
	}

	rb, err := p.Execute(ctx, &protocol.Action{Name: "rb", Type: "jms_rollback"})
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if rb.ServerTelemetry == nil || rb.ServerTelemetry.AbortCount != 1 {
		t.Fatalf("rollback AbortCount = %+v, want 1", rb.ServerTelemetry)
	}

	second, err := p.Execute(ctx, &protocol.Action{Name: "c2", Type: "jms_consume"})
	if err != nil || !second.Success {
		t.Fatalf("second consume failed: err=%v msg=%s", err, second.ErrorMessage)
	}
	if second.ServerTelemetry == nil || second.ServerTelemetry.RedeliveryCount != 1 {
		t.Fatalf("second consume RedeliveryCount = %+v, want 1", second.ServerTelemetry)
	}

	cm, err := p.Execute(ctx, &protocol.Action{Name: "cm", Type: "jms_commit"})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if cm.ServerTelemetry == nil || cm.ServerTelemetry.CommitCount != 1 {
		t.Fatalf("commit CommitCount = %+v, want 1", cm.ServerTelemetry)
	}
}

// TestJMSTransactedTopicRollbackRedelivery covers jms-1: a transacted message
// consumed from a topic subscription and then rolled back must be redelivered to
// that subscription. Before the fix it was requeued into the unrelated queue map
// (keyed by the topic name), so the topic consume path never saw it again.
func TestJMSTransactedTopicRollbackRedelivery(t *testing.T) {
	ctx := context.Background()
	p := &JMS{name: "jms-solace", provider: "solace"}
	if err := p.Init(ctx, map[string]any{
		"jms_destination":      "orders.topic.tx.test",
		"jms_destination_type": "topic",
		"jms_subscription":     "sub-1",
		"jms_mock":             true,
		"jms_acknowledgement":  "transacted",
		"jms_timeout_ms":       float64(250),
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	if _, err := p.Execute(ctx, &protocol.Action{Name: "pub", Type: "jms_publish", Body: []byte(`{"id":1}`)}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	first, err := p.Execute(ctx, &protocol.Action{Name: "c1", Type: "jms_consume"})
	if err != nil || !first.Success {
		t.Fatalf("first topic consume failed: err=%v msg=%s", err, first.ErrorMessage)
	}

	rb, err := p.Execute(ctx, &protocol.Action{Name: "rb", Type: "jms_rollback"})
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if rb.ServerTelemetry == nil || rb.ServerTelemetry.AbortCount != 1 {
		t.Fatalf("rollback AbortCount = %+v, want 1", rb.ServerTelemetry)
	}

	second, err := p.Execute(ctx, &protocol.Action{Name: "c2", Type: "jms_consume"})
	if err != nil {
		t.Fatalf("second consume err: %v", err)
	}
	if !second.Success {
		t.Fatalf("rolled-back topic message was not redelivered to the subscription: %s", second.ErrorMessage)
	}
	if second.ServerTelemetry == nil || second.ServerTelemetry.RedeliveryCount != 1 {
		t.Fatalf("second consume RedeliveryCount = %+v, want 1", second.ServerTelemetry)
	}
}

// TestJMSAutoModeDoesNotRetain confirms auto acknowledgement (the default) does
// not retain pending messages, so a rollback has nothing to redeliver.
func TestJMSAutoModeDoesNotRetain(t *testing.T) {
	ctx := context.Background()
	p := &JMS{name: "jms-solace", provider: "solace"}
	if err := p.Init(ctx, map[string]any{
		"jms_destination": "orders.auto.test",
		"jms_mock":        true,
		"jms_timeout_ms":  float64(250),
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := p.Execute(ctx, &protocol.Action{Name: "pub", Type: "jms_publish", Body: []byte(`{"id":1}`)}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if _, err := p.Execute(ctx, &protocol.Action{Name: "c1", Type: "jms_consume"}); err != nil {
		t.Fatalf("consume: %v", err)
	}
	if p.session.Pending() != 0 {
		t.Fatalf("auto mode retained %d pending messages, want 0", p.session.Pending())
	}
}

// TestJMSDeadLetterCountPopulated verifies a dead-letter action populates the
// DeadLetteredCount reliability counter (P1.5 makes the P1.1 counter live).
func TestJMSDeadLetterCountPopulated(t *testing.T) {
	ctx := context.Background()
	p := &JMS{name: "jms-solace", provider: "solace"}
	if err := p.Init(ctx, map[string]any{
		"jms_destination": "dlq.test",
		"jms_mock":        true,
		"jms_timeout_ms":  float64(250),
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	res, err := p.Execute(ctx, &protocol.Action{
		Name:   "pub-dlq",
		Type:   "jms_publish",
		Body:   []byte("x"),
		Params: map[string]any{"dead_letter": true},
	})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if res.ServerTelemetry == nil || res.ServerTelemetry.DeadLetteredCount != 1 {
		t.Fatalf("DeadLetteredCount = %+v, want 1", res.ServerTelemetry)
	}
}

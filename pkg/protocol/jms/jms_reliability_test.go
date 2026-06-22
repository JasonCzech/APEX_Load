package jms

import (
	"context"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// TestJMSConsumeReliabilityAndRedeliveryGuard covers messaging-shared-1 for JMS:
// distinct in-order consumes report no duplicate, and CRITICALLY a rollback
// redelivery (same broker sequence) is counted as a redelivery, NOT a duplicate.
func TestJMSConsumeReliabilityAndRedeliveryGuard(t *testing.T) {
	ctx := context.Background()
	p := &JMS{name: "jms-solace", provider: "solace"}
	if err := p.Init(ctx, map[string]any{
		"jms_destination":     "orders.reliability.test",
		"jms_mock":            true,
		"jms_acknowledgement": "transacted",
		"jms_timeout_ms":      float64(250),
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	pub := func() {
		if _, err := p.Execute(ctx, &protocol.Action{Name: "pub", Type: "jms_publish", Body: []byte(`{}`)}); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	consume := func(name string) *protocol.Result {
		res, err := p.Execute(ctx, &protocol.Action{Name: name, Type: "jms_consume"})
		if err != nil || !res.Success {
			t.Fatalf("%s: err=%v ok=%v msg=%s", name, err, res != nil && res.Success, res.ErrorMessage)
		}
		return res
	}

	// First message: consume + commit (a clean, settled delivery).
	pub()
	c1 := consume("c1")
	if c1.ServerTelemetry != nil && (c1.ServerTelemetry.DuplicateCount != 0 || c1.ServerTelemetry.OutOfOrderCount != 0) {
		t.Fatalf("first consume flagged dup/ooo: %+v", c1.ServerTelemetry)
	}
	if _, err := p.Execute(ctx, &protocol.Action{Name: "cm", Type: "jms_commit"}); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Second message: a distinct, higher-sequence delivery — not a duplicate.
	pub()
	c2 := consume("c2")
	if c2.ServerTelemetry != nil && c2.ServerTelemetry.DuplicateCount != 0 {
		t.Fatalf("distinct second consume flagged duplicate: %+v", c2.ServerTelemetry)
	}

	// Roll back the second message and re-consume the redelivery: same sequence, but
	// it must count as a redelivery, NOT a duplicate.
	if _, err := p.Execute(ctx, &protocol.Action{Name: "rb", Type: "jms_rollback"}); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	redelivered := consume("c3")
	if redelivered.ServerTelemetry == nil || redelivered.ServerTelemetry.RedeliveryCount != 1 {
		t.Fatalf("redelivery RedeliveryCount = %+v, want 1", redelivered.ServerTelemetry)
	}
	if redelivered.ServerTelemetry.DuplicateCount != 0 {
		t.Fatalf("redelivery wrongly counted as a duplicate: DuplicateCount = %d, want 0", redelivered.ServerTelemetry.DuplicateCount)
	}
}

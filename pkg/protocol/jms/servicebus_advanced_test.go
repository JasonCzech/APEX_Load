package jms

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// newMockJMS builds a mock-backed JMS protocol instance (memoryAdapter, which
// does NOT implement settlementAdapter).
func newMockJMS(t *testing.T) *JMS {
	t.Helper()
	p := &JMS{name: "jms-servicebus", provider: "servicebus"}
	if err := p.Init(context.Background(), map[string]any{
		"jms_destination": "orders.test",
		"jms_mock":        true,
		"jms_timeout_ms":  float64(200),
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return p
}

func TestJMSSettleRequiresServiceBusAdapter(t *testing.T) {
	ctx := context.Background()
	p := newMockJMS(t)
	res, err := p.Execute(ctx, &protocol.Action{
		Name:   "settle",
		Type:   "jms_settle",
		Params: map[string]any{"settlement_token": "abc", "disposition": "complete"},
	})
	if err != nil {
		t.Fatalf("Execute transport error: %v", err)
	}
	if res.Success {
		t.Fatal("jms_settle succeeded on the mock adapter, want fail-closed")
	}
	if !strings.Contains(res.ErrorMessage, "jms_servicebus_real") {
		t.Fatalf("expected build-tag guidance, got %q", res.ErrorMessage)
	}
}

func TestJMSRenewLockRequiresServiceBusAdapter(t *testing.T) {
	ctx := context.Background()
	p := newMockJMS(t)
	res, err := p.Execute(ctx, &protocol.Action{
		Name:   "renew",
		Type:   "jms_renew_lock",
		Params: map[string]any{"settlement_token": "abc"},
	})
	if err != nil {
		t.Fatalf("Execute transport error: %v", err)
	}
	if res.Success || !strings.Contains(res.ErrorMessage, "jms_servicebus_real") {
		t.Fatalf("expected fail-closed renew_lock, got success=%v msg=%q", res.Success, res.ErrorMessage)
	}
}

func TestJMSConsumePeekLockRequiresServiceBusAdapter(t *testing.T) {
	ctx := context.Background()
	p := newMockJMS(t)
	res, err := p.Execute(ctx, &protocol.Action{
		Name:   "consume",
		Type:   "jms_consume",
		Params: map[string]any{"peek_lock": true},
	})
	if err != nil {
		t.Fatalf("Execute transport error: %v", err)
	}
	if res.Success || !strings.Contains(res.ErrorMessage, "Service Bus adapter") {
		t.Fatalf("expected peek_lock to require the SB adapter, got success=%v msg=%q", res.Success, res.ErrorMessage)
	}
}

func TestJMSSettleMissingTokenFailsFast(t *testing.T) {
	ctx := context.Background()
	p := newMockJMS(t)
	// With the SB adapter absent the capability check fires first; assert the
	// fail-closed path rather than the token check (token check is exercised in
	// the validator test). This documents the runtime behavior.
	res, _ := p.Execute(ctx, &protocol.Action{Name: "settle", Type: "jms_settle", Params: map[string]any{}})
	if res.Success {
		t.Fatal("jms_settle with no token/adapter should not succeed")
	}
}

func TestJMSSessionVerbsRequireServiceBusAdapter(t *testing.T) {
	ctx := context.Background()
	p := newMockJMS(t)
	cases := []*protocol.Action{
		{Name: "accept", Type: "jms_session_accept", Target: "orders.test"},
		{Name: "state", Type: "jms_session_state", Params: map[string]any{"session_token": "sbsession-1"}},
		{Name: "deferred", Type: "jms_receive_deferred", Target: "orders.test", Params: map[string]any{"sequence_numbers": []any{float64(5)}}},
		{Name: "cancel", Type: "jms_cancel_scheduled", Target: "orders.test", Params: map[string]any{"sequence_numbers": []any{float64(5)}}},
	}
	for _, action := range cases {
		res, err := p.Execute(ctx, action)
		if err != nil {
			t.Fatalf("%s transport error: %v", action.Type, err)
		}
		if res.Success || !strings.Contains(res.ErrorMessage, "jms_servicebus_real") {
			t.Fatalf("%s should fail closed, got success=%v msg=%q", action.Type, res.Success, res.ErrorMessage)
		}
	}
}

func TestParseSequenceNumbers(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want []int64
	}{
		{"nil", nil, nil},
		{"float array", []any{float64(1), float64(2), float64(3)}, []int64{1, 2, 3}},
		{"csv string", "10, 20 ,30", []int64{10, 20, 30}},
		{"single number", float64(7), []int64{7}},
		{"int64 slice", []int64{4, 5}, []int64{4, 5}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseSequenceNumbers(c.in)
			if err != nil {
				t.Fatalf("parseSequenceNumbers(%v): %v", c.in, err)
			}
			if len(got) != len(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("got %v, want %v", got, c.want)
				}
			}
		})
	}
	if _, err := parseSequenceNumbers([]any{"not-a-number"}); err == nil {
		t.Fatal("expected error for non-numeric array entry")
	}
}

func TestBuildMessagePopulatesServiceBusSendFields(t *testing.T) {
	p := newMockJMS(t)
	action := &protocol.Action{
		Name: "publish",
		Type: "jms_publish",
		Body: []byte("payload"),
		Params: map[string]any{
			"message_id":           "dedup-1",
			"session_id":           "sess-7",
			"partition_key":        "pk-3",
			"time_to_live_ms":      float64(60000),
			"scheduled_enqueue_ms": float64(5000),
		},
	}
	msg := p.buildMessage(action, "orders.test")
	if msg.ID != "dedup-1" {
		t.Fatalf("ID = %q, want dedup-1 (message_id override)", msg.ID)
	}
	if msg.SessionID != "sess-7" || msg.PartitionKey != "pk-3" {
		t.Fatalf("session/partition not set: %+v", msg)
	}
	if msg.TimeToLive != 60*time.Second {
		t.Fatalf("TimeToLive = %s, want 60s", msg.TimeToLive)
	}
	if msg.ScheduledEnqueueAt.IsZero() || msg.ScheduledEnqueueAt.Before(msg.CreatedAt) {
		t.Fatalf("ScheduledEnqueueAt not set in the future: %v (created %v)", msg.ScheduledEnqueueAt, msg.CreatedAt)
	}
}

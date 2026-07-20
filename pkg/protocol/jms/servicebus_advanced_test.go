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

func TestServiceBusMockRejectsUnsupportedSendSemantics(t *testing.T) {
	for _, test := range []struct {
		name   string
		action *protocol.Action
		want   string
	}{
		{name: "publish session", action: &protocol.Action{Name: "publish", Type: "jms_publish", Params: map[string]any{"session_id": "session-1"}}, want: "params.session_id requires the real Azure Service Bus adapter"},
		{name: "publish partition", action: &protocol.Action{Name: "publish", Type: "jms_publish", Params: map[string]any{"partition_key": "tenant-1"}}, want: "params.partition_key requires the real Azure Service Bus adapter"},
		{name: "publish scheduled string", action: &protocol.Action{Name: "publish", Type: "jms_publish", Params: map[string]any{"scheduled_enqueue_ms": "100"}}, want: "params.scheduled_enqueue_ms requires the real Azure Service Bus adapter"},
		{name: "request session", action: &protocol.Action{Name: "request", Type: "jms_request_reply", Params: map[string]any{"session_id": "session-1"}}, want: "params.session_id requires the real Azure Service Bus adapter"},
		{name: "request partition", action: &protocol.Action{Name: "request", Type: "jms_request_reply", Params: map[string]any{"partition_key": "tenant-1"}}, want: "params.partition_key requires the real Azure Service Bus adapter"},
		{name: "request scheduled", action: &protocol.Action{Name: "request", Type: "jms_request_reply", Params: map[string]any{"scheduled_enqueue_ms": 100}}, want: "params.scheduled_enqueue_ms requires the real Azure Service Bus adapter"},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := newMockJMS(t)
			result, err := p.Execute(context.Background(), test.action)
			if err != nil {
				t.Fatalf("Execute returned transport error: %v", err)
			}
			if result.Success || result.StatusCode != 400 || !strings.Contains(result.ErrorMessage, test.want) {
				t.Fatalf("result = %#v, want 400 containing %q", result, test.want)
			}
		})
	}
}

func TestJMSRequestReplyRejectsIgnoredSelectorAtRuntime(t *testing.T) {
	p := newMockJMS(t)
	result, err := p.Execute(context.Background(), &protocol.Action{
		Name: "request", Type: "jms_request_reply", Params: map[string]any{"selector": "priority > 5"},
	})
	if err != nil {
		t.Fatalf("Execute returned transport error: %v", err)
	}
	if result.Success || result.StatusCode != 400 || !strings.Contains(result.ErrorMessage, "selector is not supported by jms_request_reply") {
		t.Fatalf("result = %#v, want request/reply selector rejection", result)
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
	for name, value := range map[string]any{
		"non-numeric array entry": []any{"not-a-number"},
		"zero scalar":             float64(0),
		"negative scalar":         int64(-1),
		"zero int64 slice":        []int64{1, 0},
		"negative CSV entry":      "1,-2",
		"fractional entry":        []any{float64(1.5)},
		"int64 overflow":          "9223372036854775808",
		"unexpanded template":     "{{var.sequence_number}}",
	} {
		t.Run("invalid/"+name, func(t *testing.T) {
			if _, err := parseSequenceNumbers(value); err == nil {
				t.Fatalf("parseSequenceNumbers(%v) unexpectedly succeeded", value)
			}
		})
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
			"time_to_live_ms":      "60000",
			"scheduled_enqueue_ms": "5000",
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

func TestJMSDurationParamsUseInterpolatedNumericStrings(t *testing.T) {
	p := newMockJMS(t)
	action := &protocol.Action{Name: "consume", Type: "jms_consume", Params: map[string]any{"timeout_ms": "125"}}
	if got := p.timeout(action); got != 125*time.Millisecond {
		t.Fatalf("timeout from numeric string = %s, want 125ms", got)
	}
	if err := p.validateActionBounds(&protocol.Action{
		Name: "publish", Type: "jms_publish", Params: map[string]any{"time_to_live_ms": "31536000001"},
	}); err == nil || !strings.Contains(err.Error(), "time_to_live_ms") {
		t.Fatalf("oversized numeric-string TTL error = %v", err)
	}
}

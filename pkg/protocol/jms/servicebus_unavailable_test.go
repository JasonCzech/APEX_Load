//go:build !jms_servicebus_real

package jms

import (
	"context"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestJMSRealServiceBusModeRequiresLinkedAdapter(t *testing.T) {
	ctx := context.Background()
	p := &JMS{name: "jms-servicebus", provider: "servicebus"}
	if err := p.Init(ctx, map[string]any{
		"broker_url":      "orders.servicebus.windows.net",
		"jms_destination": "orders.in",
		"jms_mock":        false,
	}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}

	if err := p.HealthCheck(ctx); err == nil {
		t.Fatal("HealthCheck returned nil error, want unavailable adapter error")
	}
	result, err := p.Execute(ctx, &protocol.Action{Name: "publish", Type: "jms_publish", Body: []byte("hello")})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Success {
		t.Fatal("Execute success = true, want false")
	}
	if result.ErrorMessage == "" {
		t.Fatal("ErrorMessage is empty, want unavailable adapter message")
	}
}

package jms

import (
	"context"
	"testing"
	"time"

	"github.com/jasonczech/stormrunner/pkg/protocol"
)

func TestJMSProtocolsAreRegistered(t *testing.T) {
	p, err := protocol.Get("jms-solace")
	if err != nil {
		t.Fatalf("protocol.Get(jms-solace) returned error: %v", err)
	}
	if p.Name() != "jms-solace" {
		t.Fatalf("Name() = %q, want jms-solace", p.Name())
	}
	if _, err := protocol.Get("jms-ibmmq"); err == nil {
		t.Fatal("protocol.Get(jms-ibmmq) returned nil error, want unregistered protocol")
	}
}

func TestJMSPublishConsumeWithExtractionAndAssertion(t *testing.T) {
	ctx := context.Background()
	p := &JMS{name: "jms-solace", provider: "solace"}
	if err := p.Init(ctx, map[string]any{
		"jms_destination": "orders.test",
		"jms_mock":        true,
		"jms_timeout_ms":  float64(250),
		"jms_properties":  map[string]string{"source": "checkout"},
	}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}

	published, err := p.Execute(ctx, &protocol.Action{
		Name: "publish-order",
		Type: "jms_publish",
		Body: []byte(`{"orderId":"A-100","status":"created"}`),
	})
	if err != nil {
		t.Fatalf("publish returned error: %v", err)
	}
	if !published.Success {
		t.Fatalf("publish success = false: %s", published.ErrorMessage)
	}

	consumed, err := p.Execute(ctx, &protocol.Action{
		Name: "consume-order",
		Type: "jms_consume",
		Extractors: []protocol.Extractor{{
			Name:       "order_id",
			Source:     "body",
			Type:       "jsonpath",
			Expression: "$.orderId",
		}},
		Assertions: []protocol.Assertion{
			{Type: "message_received"},
			{Type: "body_contains", Value: "created"},
			{Type: "property_equals", Value: "source", Expected: "checkout"},
		},
	})
	if err != nil {
		t.Fatalf("consume returned error: %v", err)
	}
	if !consumed.Success {
		t.Fatalf("consume success = false: %s", consumed.ErrorMessage)
	}
	if consumed.Extractions["order_id"] != "A-100" {
		t.Fatalf("order_id = %q, want A-100", consumed.Extractions["order_id"])
	}
	if consumed.BytesReceived == 0 {
		t.Fatal("BytesReceived = 0, want message bytes")
	}
	if consumed.ServerTelemetry == nil {
		t.Fatal("ServerTelemetry = nil, want JMS telemetry")
	}
	if consumed.ServerTelemetry.ResponsePayloadKB <= 0 {
		t.Fatalf("ResponsePayloadKB = %v, want > 0", consumed.ServerTelemetry.ResponsePayloadKB)
	}
	if consumed.ServerTelemetry.ResponseSizeClass != "small" {
		t.Fatalf("ResponseSizeClass = %q, want small", consumed.ServerTelemetry.ResponseSizeClass)
	}
	if consumed.ServerTelemetry.MessagesReceived != 1 {
		t.Fatalf("MessagesReceived = %d, want 1", consumed.ServerTelemetry.MessagesReceived)
	}
	if consumed.ServerTelemetry.ConsumerLagMs < 0 {
		t.Fatalf("ConsumerLagMs = %v, want non-negative", consumed.ServerTelemetry.ConsumerLagMs)
	}
}

func TestJMSConsumeTimeoutFailsResult(t *testing.T) {
	ctx := context.Background()
	p := &JMS{name: "jms-solace", provider: "solace"}
	if err := p.Init(ctx, map[string]any{
		"jms_destination": "empty.test",
		"jms_mock":        true,
		"jms_timeout_ms":  float64(20),
	}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}

	result, err := p.Execute(ctx, &protocol.Action{Name: "consume-empty", Type: "jms_consume"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Success {
		t.Fatal("consume success = true, want false")
	}
	if result.StatusCode != 204 {
		t.Fatalf("StatusCode = %d, want 204", result.StatusCode)
	}
}

func TestJMSHealthCheckSkipsBeforeInit(t *testing.T) {
	p := &JMS{name: "jms-solace", provider: "solace"}
	if err := p.HealthCheck(context.Background()); err != nil {
		t.Fatalf("HealthCheck before Init returned error: %v", err)
	}

	if err := p.Init(context.Background(), map[string]any{}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	if err := p.HealthCheck(context.Background()); err == nil {
		t.Fatal("HealthCheck after Init without broker/mock returned nil, want error")
	}
}

func TestJMSRealSolaceModeRequiresLinkedAdapter(t *testing.T) {
	ctx := context.Background()
	p := &JMS{name: "jms-solace", provider: "solace"}
	if err := p.Init(ctx, map[string]any{
		"broker_url":      "tcps://broker.example.com:55443",
		"jms_message_vpn": "perf",
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

func TestJMSRequestReplyEchoesResponse(t *testing.T) {
	ctx := context.Background()
	p := &JMS{name: "jms-solace", provider: "solace"}
	if err := p.Init(ctx, map[string]any{
		"jms_destination": "orders.request",
		"jms_reply_to":    "orders.reply",
		"jms_mock":        true,
		"jms_timeout_ms":  float64(250),
	}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}

	result, err := p.Execute(ctx, &protocol.Action{
		Name: "request-order",
		Type: "jms_request_reply",
		Body: []byte(`{"orderId":"A-200"}`),
		Assertions: []protocol.Assertion{
			{Type: "message_received"},
			{Type: "body_contains", Value: "A-200"},
			{Type: "response_time_ms", Max: int((500 * time.Millisecond).Milliseconds())},
		},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !result.Success {
		t.Fatalf("request-reply success = false: %s", result.ErrorMessage)
	}
	if result.Extractions["jms_request_message_id"] == "" {
		t.Fatal("jms_request_message_id was not extracted")
	}
	if result.Extractions["jms_correlation_id"] != result.Extractions["jms_request_message_id"] {
		t.Fatalf("correlation_id = %q, want request message id %q", result.Extractions["jms_correlation_id"], result.Extractions["jms_request_message_id"])
	}
	if result.CorrelationID == "" {
		t.Fatal("expected first-class correlation id on result")
	}
	if result.DependencyType != "jms_queue" {
		t.Fatalf("DependencyType = %q, want jms_queue", result.DependencyType)
	}
	if result.DependencyTarget != "orders.request" {
		t.Fatalf("DependencyTarget = %q, want orders.request", result.DependencyTarget)
	}
	if result.ServerTelemetry == nil {
		t.Fatal("ServerTelemetry = nil, want request/reply telemetry")
	}
	if result.ServerTelemetry.ServerProcessingMs < 0 {
		t.Fatalf("ServerProcessingMs = %v, want non-negative", result.ServerTelemetry.ServerProcessingMs)
	}
	if result.ServerTelemetry.TransferMs < 0 {
		t.Fatalf("TransferMs = %v, want non-negative", result.ServerTelemetry.TransferMs)
	}
	if result.ServerTelemetry.MessagesSent != 1 || result.ServerTelemetry.MessagesReceived != 1 {
		t.Fatalf("message counts = %#v, want one sent and one received", result.ServerTelemetry)
	}
	if result.ServerTelemetry.MessageLatencyMs < 0 {
		t.Fatalf("MessageLatencyMs = %v, want non-negative", result.ServerTelemetry.MessageLatencyMs)
	}
	if result.ServerTelemetry.RequestSizeClass != "small" {
		t.Fatalf("RequestSizeClass = %q, want small", result.ServerTelemetry.RequestSizeClass)
	}
	if result.ServerTelemetry.ResponseSizeClass != "small" {
		t.Fatalf("ResponseSizeClass = %q, want small", result.ServerTelemetry.ResponseSizeClass)
	}
	if result.ServerTelemetry.ConsumerLagMs < 0 {
		t.Fatalf("ConsumerLagMs = %v, want non-negative", result.ServerTelemetry.ConsumerLagMs)
	}
}

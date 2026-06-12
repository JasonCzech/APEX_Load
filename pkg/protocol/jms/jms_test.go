package jms

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestJMSProtocolsAreRegistered(t *testing.T) {
	for _, name := range []string{"jms-solace", "jms-ibmmq", "jms-servicebus"} {
		p, err := protocol.Get(name)
		if err != nil {
			t.Fatalf("protocol.Get(%s) returned error: %v", name, err)
		}
		if p.Name() != name {
			t.Fatalf("Name() = %q, want %s", p.Name(), name)
		}
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
	if consumed.ServerTelemetry.MessagesReceivedPerSec <= 0 {
		t.Fatalf("MessagesReceivedPerSec = %v, want > 0", consumed.ServerTelemetry.MessagesReceivedPerSec)
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

func TestJMSSolaceSidecarAdapterPublishesViaGovernedCommand(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	requestPath := filepath.Join(dir, "sidecar-request.json")
	sidecarPath := filepath.Join(dir, "sidecar.sh")
	script := `#!/bin/sh
cat > "$1"
printf '{}'
`
	if err := os.WriteFile(sidecarPath, []byte(script), 0o700); err != nil {
		t.Fatalf("WriteFile(sidecar) returned error: %v", err)
	}

	p := &JMS{name: "jms-solace", provider: "solace"}
	if err := p.Init(ctx, map[string]any{
		"broker_url":      "tcps://broker.example.com:55443",
		"jms_message_vpn": "perf",
		"jms_destination": "queue/orders",
		"jms_mock":        false,
		"jms_properties": map[string]string{
			"adapter":         "sidecar",
			"sidecar_command": sidecarPath + " " + requestPath,
		},
	}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}

	if err := p.HealthCheck(ctx); err != nil {
		t.Fatalf("HealthCheck returned error: %v", err)
	}

	result, err := p.Execute(ctx, &protocol.Action{
		Name:   "publish",
		Type:   "jms_publish",
		Target: "queue/orders",
		Body:   []byte("hello"),
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute success = false: %s", result.ErrorMessage)
	}
	if result.BytesSent != int64(len("hello")) {
		t.Fatalf("BytesSent = %d, want %d", result.BytesSent, len("hello"))
	}

	rawRequest, err := os.ReadFile(requestPath)
	if err != nil {
		t.Fatalf("ReadFile(sidecar request) returned error: %v", err)
	}
	var req sidecarRequest
	if err := json.Unmarshal(rawRequest, &req); err != nil {
		t.Fatalf("Unmarshal(sidecar request) returned error: %v", err)
	}
	if req.Operation != "publish" {
		t.Fatalf("Operation = %q, want publish", req.Operation)
	}
	if req.Destination != "queue/orders" {
		t.Fatalf("Destination = %q, want queue/orders", req.Destination)
	}
	if string(req.Message.Body) != "hello" {
		t.Fatalf("Message.Body = %q, want hello", string(req.Message.Body))
	}
	if req.Config.BrokerURL != "tcps://broker.example.com:55443" {
		t.Fatalf("Config.BrokerURL = %q, want configured broker", req.Config.BrokerURL)
	}
}

func TestJMSRealIbmMqModeRequiresLinkedAdapter(t *testing.T) {
	ctx := context.Background()
	p := &JMS{name: "jms-ibmmq", provider: "ibmmq"}
	if err := p.Init(ctx, map[string]any{
		"broker_url":      "tcp://broker.example.com:1414",
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
	if result.ServerTelemetry.MessagesSentPerSec <= 0 {
		t.Fatalf("MessagesSentPerSec = %v, want > 0", result.ServerTelemetry.MessagesSentPerSec)
	}
	if result.ServerTelemetry.MessagesReceivedPerSec <= 0 {
		t.Fatalf("MessagesReceivedPerSec = %v, want > 0", result.ServerTelemetry.MessagesReceivedPerSec)
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

func TestJMSServiceBusMockPublishConsumeRequestReply(t *testing.T) {
	ctx := context.Background()
	p := &JMS{name: "jms-servicebus", provider: "servicebus"}
	if err := p.Init(ctx, map[string]any{
		"jms_destination": "orders.sb.queue",
		"jms_reply_to":    "orders.sb.reply",
		"jms_provider":    "servicebus",
		"jms_mock":        true,
		"jms_timeout_ms":  float64(250),
	}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}

	published, err := p.Execute(ctx, &protocol.Action{
		Name: "publish-servicebus",
		Type: "jms_publish",
		Body: []byte(`{"orderId":"SB-100"}`),
	})
	if err != nil {
		t.Fatalf("publish returned error: %v", err)
	}
	if !published.Success {
		t.Fatalf("publish success = false: %s", published.ErrorMessage)
	}

	consumed, err := p.Execute(ctx, &protocol.Action{
		Name: "consume-servicebus",
		Type: "jms_consume",
		Assertions: []protocol.Assertion{
			{Type: "message_received"},
			{Type: "body_contains", Value: "SB-100"},
		},
	})
	if err != nil {
		t.Fatalf("consume returned error: %v", err)
	}
	if !consumed.Success {
		t.Fatalf("consume success = false: %s", consumed.ErrorMessage)
	}

	reply, err := p.Execute(ctx, &protocol.Action{
		Name: "request-reply-servicebus",
		Type: "jms_request_reply",
		Body: []byte(`{"orderId":"SB-200"}`),
	})
	if err != nil {
		t.Fatalf("request_reply returned error: %v", err)
	}
	if !reply.Success {
		t.Fatalf("request_reply success = false: %s", reply.ErrorMessage)
	}
	if reply.Extractions["jms_correlation_id"] == "" {
		t.Fatal("request_reply correlation extraction is empty")
	}
}

func TestJMSServiceBusMockConsumesTopicSubscriptionDestination(t *testing.T) {
	ctx := context.Background()
	topic := "orders.sb.topic"
	subscription := "perf-sub"
	composed := topic + "/subscriptions/" + subscription
	p := &JMS{name: "jms-servicebus", provider: "servicebus"}
	if err := p.Init(ctx, map[string]any{
		"jms_destination":      topic,
		"jms_destination_type": "topic",
		"jms_subscription":     subscription,
		"jms_provider":         "servicebus",
		"jms_mock":             true,
		"jms_timeout_ms":       float64(250),
	}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	broker.publish(composed, message{
		ID:          "msg-servicebus-topic",
		Destination: composed,
		Body:        []byte(`{"orderId":"SB-TOPIC"}`),
		CreatedAt:   time.Now(),
	})

	consumed, err := p.Execute(ctx, &protocol.Action{
		Name: "consume-servicebus-topic",
		Type: "jms_consume",
	})
	if err != nil {
		t.Fatalf("consume returned error: %v", err)
	}
	if !consumed.Success {
		t.Fatalf("consume success = false: %s", consumed.ErrorMessage)
	}
	if consumed.DependencyTarget != composed {
		t.Fatalf("DependencyTarget = %q, want %q", consumed.DependencyTarget, composed)
	}
	if consumed.Extractions["jms_destination"] != composed {
		t.Fatalf("jms_destination = %q, want %q", consumed.Extractions["jms_destination"], composed)
	}
}

func TestJMSConsumeClassifiesDeadLetterDestinations(t *testing.T) {
	ctx := context.Background()
	p := &JMS{name: "jms-solace", provider: "solace"}
	if err := p.Init(ctx, map[string]any{
		"jms_destination":              "orders.dlq",
		"jms_dead_letter_destinations": []string{"orders.dlq"},
		"jms_mock":                     true,
		"jms_timeout_ms":               float64(250),
	}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}

	_, err := p.Execute(ctx, &protocol.Action{
		Name:     "publish-dead-letter",
		Type:     "jms_publish",
		Body:     []byte(`{"orderId":"A-404"}`),
		Metadata: map[string]any{},
	})
	if err != nil {
		t.Fatalf("publish returned error: %v", err)
	}

	consumed, err := p.Execute(ctx, &protocol.Action{
		Name: "consume-dead-letter",
		Type: "jms_consume",
	})
	if err != nil {
		t.Fatalf("consume returned error: %v", err)
	}
	if consumed.DependencyType != "jms_dead_letter_queue" {
		t.Fatalf("DependencyType = %q, want jms_dead_letter_queue", consumed.DependencyType)
	}
	if consumed.Extractions["jms_dead_letter"] != "true" {
		t.Fatalf("jms_dead_letter extraction = %q, want true", consumed.Extractions["jms_dead_letter"])
	}
	if consumed.Extractions["jms_dead_letter_destination"] != "orders.dlq" {
		t.Fatalf("jms_dead_letter_destination = %q, want orders.dlq", consumed.Extractions["jms_dead_letter_destination"])
	}

	p2 := &JMS{name: "jms-solace", provider: "solace"}
	if err := p2.Init(ctx, map[string]any{
		"jms_destination": "explicit.dead.letter",
		"jms_mock":        true,
		"jms_timeout_ms":  float64(250),
	}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	result, err := p2.Execute(ctx, &protocol.Action{
		Name: "consume-explicit-dead-letter",
		Type: "jms_consume",
		Params: map[string]any{
			"dead_letter": true,
		},
	})
	if err != nil {
		t.Fatalf("explicit dead-letter execute returned error: %v", err)
	}
	if result.DependencyType != "jms_dead_letter_queue" {
		t.Fatalf("explicit dead-letter DependencyType = %q, want jms_dead_letter_queue", result.DependencyType)
	}
}

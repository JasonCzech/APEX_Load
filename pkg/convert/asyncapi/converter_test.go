package asyncapi

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

const asyncAPI3 = `
asyncapi: 3.0.0
info:
  title: Orders Service
  version: 1.2.0
servers:
  production:
    host: kafka.prod.example.com:9092
    protocol: kafka-secure
channels:
  orderCreated:
    address: orders.created
    messages:
      orderCreated:
        payload:
          type: object
          properties:
            orderId:
              type: string
            amount:
              type: number
            nested:
              type: object
              properties:
                sku:
                  type: string
  orderShipped:
    address: orders.shipped
    messages:
      orderShipped:
        payload:
          type: object
          properties:
            orderId:
              type: string
operations:
  publishOrderCreated:
    action: send
    channel:
      $ref: '#/channels/orderCreated'
  consumeOrderShipped:
    action: receive
    channel:
      $ref: '#/channels/orderShipped'
`

const asyncAPI2 = `
asyncapi: 2.6.0
info:
  title: Payments
  version: 0.1.0
servers:
  broker:
    url: kafka1:9092
    protocol: kafka
channels:
  payment/requested:
    bindings:
      kafka:
        topic: payments.requested
    publish:
      operationId: requestPayment
      message:
        payload:
          type: object
          properties:
            paymentId:
              type: string
    subscribe:
      operationId: paymentResult
      message:
        payload:
          type: object
          properties:
            paymentId:
              type: string
            status:
              type: string
`

func TestConvertAsyncAPI3(t *testing.T) {
	result, err := Convert(ImportRequest{Content: asyncAPI3})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Script.Protocol != "kafka" {
		t.Fatalf("protocol = %q, want kafka", result.Script.Protocol)
	}
	if got := result.Script.Config.Kafka.Brokers; len(got) != 1 || got[0] != "kafka.prod.example.com:9092" {
		t.Fatalf("brokers = %v, want [kafka.prod.example.com:9092]", got)
	}
	if result.Script.Config.Kafka.SecurityProtocol != "sasl_ssl" {
		t.Fatalf("security_protocol = %q, want sasl_ssl", result.Script.Config.Kafka.SecurityProtocol)
	}
	if !result.Script.Config.Kafka.Mock {
		t.Fatal("expected mock mode enabled by default")
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions = %d, want 2", len(result.Script.Actions))
	}

	var produce, consume *dsl.Action
	for i := range result.Script.Actions {
		switch result.Script.Actions[i].Type {
		case "kafka_produce":
			produce = &result.Script.Actions[i]
		case "kafka_consume":
			consume = &result.Script.Actions[i]
		}
	}
	if produce == nil || consume == nil {
		t.Fatalf("expected one produce and one consume action, got %+v", result.Script.Actions)
	}
	if produce.Target != "orders.created" {
		t.Fatalf("produce target = %q, want orders.created", produce.Target)
	}
	if !strings.Contains(produce.Body, "{{data.asyncapi.orderid}}") {
		t.Fatalf("produce body missing data placeholder: %s", produce.Body)
	}
	if consume.Target != "orders.shipped" {
		t.Fatalf("consume target = %q, want orders.shipped", consume.Target)
	}
	if result.Stats.ProduceActions != 1 || result.Stats.ConsumeActions != 1 {
		t.Fatalf("stats produce/consume = %d/%d, want 1/1", result.Stats.ProduceActions, result.Stats.ConsumeActions)
	}

	// The generated script must be valid DSL.
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("generated script failed validation: %v", issues)
	}
}

func TestConvertAsyncAPI2(t *testing.T) {
	result, err := Convert(ImportRequest{Content: asyncAPI2})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	topics := map[string]bool{}
	for _, a := range result.Script.Actions {
		topics[a.Target] = true
	}
	if !topics["payments.requested"] {
		t.Fatalf("expected topic payments.requested from kafka binding, got %v", topics)
	}
	if result.Stats.ProduceActions != 1 || result.Stats.ConsumeActions != 1 {
		t.Fatalf("produce/consume = %d/%d, want 1/1", result.Stats.ProduceActions, result.Stats.ConsumeActions)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("generated script failed validation: %v", issues)
	}
}

func TestConvertAsyncAPI3RequestReply(t *testing.T) {
	doc := `
asyncapi: 3.0.0
info:
  title: RPC
  version: 1.0.0
servers:
  s:
    host: kafka:9092
    protocol: kafka
channels:
  req:
    address: rpc.requests
    messages:
      m:
        payload:
          type: object
          properties:
            ping:
              type: boolean
  rep:
    address: rpc.replies
operations:
  doRpc:
    action: send
    channel:
      $ref: '#/channels/req'
    reply:
      channel:
        $ref: '#/channels/rep'
`
	result, err := Convert(ImportRequest{Content: doc})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Script.Actions) != 1 || result.Script.Actions[0].Type != "kafka_request_reply" {
		t.Fatalf("expected one kafka_request_reply action, got %+v", result.Script.Actions)
	}
	if result.Script.Actions[0].Params["reply_to"] != "rpc.replies" {
		t.Fatalf("reply_to = %v, want rpc.replies", result.Script.Actions[0].Params["reply_to"])
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("generated script failed validation: %v", issues)
	}
}

func TestConvertIgnoresNonKafkaServers(t *testing.T) {
	doc := `
asyncapi: 3.0.0
info:
  title: Mixed
  version: 1.0.0
servers:
  mqttBroker:
    host: mqtt:1883
    protocol: mqtt
  kafkaBroker:
    host: kafka:9092
    protocol: kafka
channels:
  c:
    address: events
    messages:
      m:
        payload:
          type: object
          properties:
            id:
              type: string
operations:
  send:
    action: send
    channel:
      $ref: '#/channels/c'
`
	result, err := Convert(ImportRequest{Content: doc})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if got := result.Script.Config.Kafka.Brokers; len(got) != 1 || got[0] != "kafka:9092" {
		t.Fatalf("brokers = %v, want [kafka:9092]", got)
	}
	found := false
	for _, warn := range result.Warnings {
		if strings.Contains(warn, "mqtt") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a warning about the mqtt server, got %v", result.Warnings)
	}
}

func TestConvertAMQPServerYieldsJMSScript(t *testing.T) {
	doc := `
asyncapi: 3.0.0
info:
  title: Orders AMQP
  version: 1.0.0
servers:
  broker:
    host: amqp.example.test:5671
    protocol: amqp
channels:
  orderCreated:
    address: orders.created
    messages:
      m:
        payload:
          type: object
          properties:
            orderId:
              type: string
operations:
  send:
    action: send
    channel:
      $ref: '#/channels/orderCreated'
  receive:
    action: receive
    channel:
      $ref: '#/channels/orderCreated'
`
	result, err := Convert(ImportRequest{Content: doc})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Script.Protocol != "jms-solace" {
		t.Fatalf("protocol = %q, want jms-solace", result.Script.Protocol)
	}
	if result.Script.Config.JMS.Provider != "solace" || !result.Script.Config.JMS.Mock {
		t.Fatalf("unexpected jms config: %+v", result.Script.Config.JMS)
	}
	if result.Script.Config.JMS.BrokerURL != "amqp.example.test:5671" {
		t.Fatalf("broker_url = %q, want amqp.example.test:5671", result.Script.Config.JMS.BrokerURL)
	}
	if result.Script.Config.Kafka.Brokers != nil {
		t.Fatalf("kafka config should be empty for a JMS target, got %+v", result.Script.Config.Kafka)
	}
	types := map[string]int{}
	for _, a := range result.Script.Actions {
		types[a.Type]++
	}
	if types["jms_publish"] == 0 || types["jms_consume"] == 0 {
		t.Fatalf("expected jms_publish + jms_consume actions, got %v", types)
	}
	for _, a := range result.Script.Actions {
		if a.Type == "kafka_produce" || a.Type == "kafka_consume" {
			t.Fatalf("JMS target must not emit kafka actions: %s", a.Type)
		}
	}
}

func TestConvertMQTTServerIsReviewOnly(t *testing.T) {
	doc := `
asyncapi: 3.0.0
info:
  title: Sensors
  version: 1.0.0
servers:
  broker:
    host: mqtt.example.test:8883
    protocol: mqtt
channels:
  reading:
    address: sensors/reading
    messages:
      m:
        payload:
          type: object
          properties:
            temp:
              type: number
operations:
  send:
    action: send
    channel:
      $ref: '#/channels/reading'
`
	result, err := Convert(ImportRequest{Content: doc})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Script.Protocol != "kafka" {
		t.Fatalf("protocol = %q, want skeleton kafka review script", result.Script.Protocol)
	}
	if len(result.Script.Actions) != 0 {
		t.Fatalf("MQTT import generated %d executable actions, want review-only skeleton", len(result.Script.Actions))
	}
	found := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "MQTT") && strings.Contains(w, "no native MQTT runtime") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an MQTT review warning, got %v", result.Warnings)
	}
}

func TestConvertDetectsEventHubsKafkaEndpoint(t *testing.T) {
	doc := `
asyncapi: 3.0.0
info:
  title: EH
  version: 1.0.0
servers:
  eh:
    host: myns.servicebus.windows.net:9093
    protocol: kafka-secure
channels:
  c:
    address: telemetry
    messages:
      m:
        payload:
          type: object
          properties:
            id:
              type: string
operations:
  send:
    action: send
    channel:
      $ref: '#/channels/c'
`
	result, err := Convert(ImportRequest{Content: doc})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Script.Config.Kafka.Profile != "eventhubs" {
		t.Fatalf("profile = %q, want eventhubs", result.Script.Config.Kafka.Profile)
	}
	if result.Script.Config.Kafka.SecurityProtocol != "sasl_ssl" {
		t.Fatalf("security_protocol = %q, want sasl_ssl", result.Script.Config.Kafka.SecurityProtocol)
	}
	found := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "Event Hubs") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an Event Hubs warning, got %v", result.Warnings)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("generated Event Hubs script failed validation: %v", issues)
	}
}

func TestConvertErrors(t *testing.T) {
	if _, err := Convert(ImportRequest{Content: ""}); err == nil {
		t.Fatal("expected error for empty content")
	}
	if _, err := Convert(ImportRequest{Content: "not: a: valid: doc"}); err == nil {
		t.Fatal("expected error for invalid document")
	}
	noChannels := "asyncapi: 3.0.0\ninfo:\n  title: x\n  version: 1.0.0\n"
	if _, err := Convert(ImportRequest{Content: noChannels}); err == nil {
		t.Fatal("expected error when there are no channels")
	}
}

func TestConvertChannelFilter(t *testing.T) {
	mock := true
	result, err := Convert(ImportRequest{
		Content: asyncAPI3,
		Options: ConvertOptions{Channels: []string{"orders.created"}, Mock: &mock},
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Script.Actions) != 1 || result.Script.Actions[0].Target != "orders.created" {
		t.Fatalf("expected only the orders.created action, got %+v", result.Script.Actions)
	}
}

func TestSupportMatrix(t *testing.T) {
	if len(GetSupportMatrix()) == 0 {
		t.Fatal("support matrix should not be empty")
	}
	if len(GetSupportMatrixNotes()) == 0 {
		t.Fatal("support matrix notes should not be empty")
	}
}

func TestConvertAsyncAPIWiresCorrelationIDs(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `{
  "asyncapi": "2.6.0",
  "info": {"title": "Orders", "version": "1.0.0"},
  "servers": {"prod": {"url": "broker:9092", "protocol": "kafka"}},
  "channels": {
    "order.requests": {
      "publish": {
        "operationId": "sendOrderRequest",
        "message": {
          "correlationId": {"location": "$message.payload#/correlationId"},
          "payload": {
            "type": "object",
            "properties": {
              "correlationId": {"type": "string"},
              "sku": {"type": "string"}
            }
          }
        }
      }
    },
    "order.replies": {
      "subscribe": {
        "operationId": "receiveOrderReply",
        "message": {
          "correlationId": {"location": "$message.payload#/correlationId"},
          "payload": {
            "type": "object",
            "properties": {
              "correlationId": {"type": "string"},
              "status": {"type": "string"}
            }
          }
        }
      }
    }
  }
}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}

	var produce, consume *dsl.Action
	for i := range result.Script.Actions {
		switch result.Script.Actions[i].Type {
		case "kafka_produce":
			produce = &result.Script.Actions[i]
		case "kafka_consume":
			consume = &result.Script.Actions[i]
		}
	}
	if produce == nil || consume == nil {
		t.Fatalf("expected produce + consume actions, got %+v", result.Script.Actions)
	}

	// Produce side: the declared correlation field sends a unique uuid, not a
	// shared CSV column.
	if !strings.Contains(produce.Body, `"correlationId": "{{fn.uuid()}}"`) {
		t.Fatalf("produce body missing fn.uuid correlation value:\n%s", produce.Body)
	}
	if strings.Contains(produce.Body, "{{data.asyncapi.correlationid}}") {
		t.Fatalf("produce correlation field still bound to data column:\n%s", produce.Body)
	}

	// Consume side: an extractor captures the correlation id for reuse.
	if len(consume.Extractors) != 1 {
		t.Fatalf("consume extractors = %+v, want 1", consume.Extractors)
	}
	extractor := consume.Extractors[0]
	if extractor.Source != "body" || extractor.Type != "jsonpath" || extractor.Expression != "$.correlationId" {
		t.Fatalf("consume extractor = %+v", extractor)
	}

	if result.Stats.MessagesWithCorrelation != 2 {
		t.Fatalf("messages_with_correlation = %d, want 2", result.Stats.MessagesWithCorrelation)
	}
}

func TestConvertAsyncAPIHeaderCorrelationGoesToActionHeaders(t *testing.T) {
	// A correlationId that points at a message header must be written to the
	// top-level action.Headers map (which both the Kafka and JMS runtimes send),
	// not action.Params["headers"] (which neither reads).
	result, err := Convert(ImportRequest{
		Content: `{
  "asyncapi": "2.6.0",
  "info": {"title": "Orders", "version": "1.0.0"},
  "servers": {"prod": {"url": "broker:9092", "protocol": "kafka"}},
  "channels": {
    "order.requests": {
      "publish": {
        "operationId": "sendOrderRequest",
        "message": {
          "correlationId": {"location": "$message.header#/correlation-id"},
          "payload": {"type": "object", "properties": {"sku": {"type": "string"}}}
        }
      }
    }
  }
}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	var produce *dsl.Action
	for i := range result.Script.Actions {
		if strings.HasSuffix(result.Script.Actions[i].Type, "_produce") || result.Script.Actions[i].Type == "jms_publish" {
			produce = &result.Script.Actions[i]
		}
	}
	if produce == nil {
		t.Fatalf("expected a produce action, got %+v", result.Script.Actions)
	}
	if got := produce.Headers["correlation-id"]; got != "{{fn.uuid()}}" {
		t.Fatalf("header correlation not written to action.Headers: headers=%v", produce.Headers)
	}
	if _, leaked := produce.Params["headers"]; leaked {
		t.Fatalf("correlation id was written to params[\"headers\"] (runtime-ignored): %v", produce.Params)
	}
}

func TestParseCorrelationLocation(t *testing.T) {
	tests := []struct {
		location string
		source   string
		path     string
		ok       bool
	}{
		{"$message.payload#/correlationId", "payload", "correlationId", true},
		{"$message.payload#/meta/requestId", "payload", "meta/requestId", true},
		{"$message.header#/x-correlation-id", "header", "x-correlation-id", true},
		{"$message.payload#/", "", "", false},
		{"unknown", "", "", false},
		{"", "", "", false},
	}
	for _, tt := range tests {
		source, path, ok := parseCorrelationLocation(tt.location)
		if source != tt.source || path != tt.path || ok != tt.ok {
			t.Errorf("parseCorrelationLocation(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tt.location, source, path, ok, tt.source, tt.path, tt.ok)
		}
	}
}

package asyncapi

import (
	"context"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/engine"
	_ "github.com/jasonczech/apexload/pkg/protocol/jms"
	_ "github.com/jasonczech/apexload/pkg/protocol/kafka"
)

func TestAsyncAPIImportReplaySmokeKafkaProduceConsume(t *testing.T) {
	result, err := Convert(ImportRequest{
		Name: "AsyncAPI Kafka Smoke",
		Options: ConvertOptions{
			IncludeAssertions: boolPtr(true),
		},
		Content: `
asyncapi: 3.0.0
info:
  title: Kafka Smoke
  version: 1.0.0
servers:
  broker:
    host: kafka:9092
    protocol: kafka
channels:
  orders:
    address: asyncapi.smoke.kafka.orders
    messages:
      orderCreated:
        payload:
          type: object
          properties:
            orderId:
              type: string
            status:
              type: string
operations:
  consumeOrder:
    action: receive
    channel:
      $ref: '#/channels/orders'
  publishOrder:
    action: send
    channel:
      $ref: '#/channels/orders'
`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Script.Config.Kafka.AutoOffsetReset != "earliest" {
		t.Fatalf("auto_offset_reset = %q, want earliest", result.Script.Config.Kafka.AutoOffsetReset)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions = %d, want 2", len(result.Script.Actions))
	}
	if result.Script.Actions[0].Type != "kafka_produce" || result.Script.Actions[1].Type != "kafka_consume" {
		t.Fatalf("actions not replay ordered: %+v", result.Script.Actions)
	}

	report, err := engine.ReplayScript(context.Background(), result.Script, engine.ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatalf("ReplayScript returned error: %v", err)
	}
	if !report.Success {
		t.Fatalf("replay failed: %s events=%#v", report.Error, report.Events)
	}
	if len(report.Events) != 2 {
		t.Fatalf("events = %d, want 2", len(report.Events))
	}
	if !strings.Contains(report.Events[1].Extractions["kafka_body"], "orderId") {
		t.Fatalf("consume kafka_body = %q, want generated order payload", report.Events[1].Extractions["kafka_body"])
	}
	assertNoAsyncAPIHeaders(t, report)
}

func TestAsyncAPIImportReplaySmokeKafkaRequestReply(t *testing.T) {
	result, err := Convert(ImportRequest{
		Name: "AsyncAPI RPC Smoke",
		Options: ConvertOptions{
			IncludeAssertions: boolPtr(true),
		},
		Content: `
asyncapi: 3.0.0
info:
  title: Kafka RPC Smoke
  version: 1.0.0
servers:
  broker:
    host: kafka:9092
    protocol: kafka
channels:
  request:
    address: asyncapi.smoke.kafka.rpc.request
    messages:
      rpcRequest:
        payload:
          type: object
          properties:
            ping:
              type: boolean
  reply:
    address: asyncapi.smoke.kafka.rpc.reply
operations:
  call:
    action: send
    channel:
      $ref: '#/channels/request'
    reply:
      channel:
        $ref: '#/channels/reply'
`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Script.Actions) != 1 || result.Script.Actions[0].Type != "kafka_request_reply" {
		t.Fatalf("expected one kafka_request_reply action, got %+v", result.Script.Actions)
	}
	report, err := engine.ReplayScript(context.Background(), result.Script, engine.ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatalf("ReplayScript returned error: %v", err)
	}
	if !report.Success {
		t.Fatalf("replay failed: %s events=%#v", report.Error, report.Events)
	}
	if got := report.Events[0].Extractions["kafka_correlation_id"]; got == "" {
		t.Fatalf("missing kafka_correlation_id extraction: %#v", report.Events[0].Extractions)
	}
	assertNoAsyncAPIHeaders(t, report)
}

func TestAsyncAPIImportReplaySmokeKafkaAvroStaticSchemaID(t *testing.T) {
	result, err := Convert(ImportRequest{
		Name: "AsyncAPI Kafka Avro Smoke",
		Options: ConvertOptions{
			IncludeAssertions: boolPtr(true),
			SchemaID:          7,
		},
		Content: `
asyncapi: 3.0.0
info:
  title: Kafka Avro Smoke
  version: 1.0.0
servers:
  broker:
    host: kafka:9092
    protocol: kafka
channels:
  orders:
    address: asyncapi.smoke.kafka.avro.orders
    messages:
      orderCreated:
        schemaFormat: application/vnd.apache.avro+json;version=1.9.0
        payload:
          type: record
          name: OrderCreated
          fields:
            - name: orderId
              type: string
            - name: amount
              type: double
            - name: status
              type:
                - "null"
                - string
              default: null
operations:
  publishOrder:
    action: send
    channel:
      $ref: '#/channels/orders'
  consumeOrder:
    action: receive
    channel:
      $ref: '#/channels/orders'
`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Script.Config.Kafka.Serialization != "avro" || result.Script.Config.Kafka.SchemaID != 7 || result.Script.Config.Kafka.Schema == "" {
		t.Fatalf("missing Avro serde config: %+v", result.Script.Config.Kafka)
	}

	report, err := engine.ReplayScript(context.Background(), result.Script, engine.ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatalf("ReplayScript returned error: %v", err)
	}
	if !report.Success {
		t.Fatalf("replay failed: %s events=%#v", report.Error, report.Events)
	}
	if !strings.Contains(report.Events[1].Extractions["kafka_body"], "orderId") {
		t.Fatalf("consume kafka_body = %q, want Avro-decoded order payload", report.Events[1].Extractions["kafka_body"])
	}
	assertNoAsyncAPIHeaders(t, report)
}

func TestAsyncAPIImportReplaySmokeJMSPublishConsume(t *testing.T) {
	result, err := Convert(ImportRequest{
		Name: "AsyncAPI JMS Smoke",
		Options: ConvertOptions{
			IncludeAssertions: boolPtr(true),
		},
		Content: `
asyncapi: 3.0.0
info:
  title: JMS Smoke
  version: 1.0.0
servers:
  broker:
    host: amqp.example.test:5671
    protocol: amqp
channels:
  shipments:
    address: asyncapi.smoke.jms.shipments
    bindings:
      jms:
        destinationType: topic
    messages:
      shipment:
        payload:
          type: object
          properties:
            shipmentId:
              type: string
            status:
              type: string
operations:
  receiveShipment:
    action: receive
    channel:
      $ref: '#/channels/shipments'
  publishShipment:
    action: send
    channel:
      $ref: '#/channels/shipments'
`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Script.Protocol != "jms-solace" {
		t.Fatalf("protocol = %q, want jms-solace", result.Script.Protocol)
	}
	if result.Script.Config.JMS.DestinationType != "topic" || result.Script.Config.JMS.Subscription == "" || result.Script.Config.JMS.Destination == "" {
		t.Fatalf("missing JMS topic subscription config: %+v", result.Script.Config.JMS)
	}
	if len(result.Script.Actions) != 2 || result.Script.Actions[0].Type != "jms_publish" || result.Script.Actions[1].Type != "jms_consume" {
		t.Fatalf("actions not replay ordered: %+v", result.Script.Actions)
	}

	report, err := engine.ReplayScript(context.Background(), result.Script, engine.ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatalf("ReplayScript returned error: %v", err)
	}
	if !report.Success {
		t.Fatalf("replay failed: %s events=%#v", report.Error, report.Events)
	}
	if !strings.Contains(report.Events[1].Extractions["jms_body"], "shipmentId") {
		t.Fatalf("consume jms_body = %q, want generated shipment payload", report.Events[1].Extractions["jms_body"])
	}
	assertNoAsyncAPIHeaders(t, report)
}

func TestAsyncAPIImportReplaySmokeJMSRequestReply(t *testing.T) {
	result, err := Convert(ImportRequest{
		Name: "AsyncAPI JMS RPC Smoke",
		Options: ConvertOptions{
			IncludeAssertions: boolPtr(true),
		},
		Content: `
asyncapi: 3.0.0
info:
  title: JMS RPC Smoke
  version: 1.0.0
servers:
  broker:
    host: amqp.example.test:5671
    protocol: amqp
channels:
  request:
    address: asyncapi.smoke.jms.rpc.request
    messages:
      rpcRequest:
        headers:
          type: object
          properties:
            correlation-id:
              type: string
        correlationId:
          location: $message.header#/correlation-id
        payload:
          type: object
          properties:
            ping:
              type: boolean
  reply:
    address: asyncapi.smoke.jms.rpc.reply
operations:
  call:
    action: send
    channel:
      $ref: '#/channels/request'
    reply:
      channel:
        $ref: '#/channels/reply'
`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Script.Actions) != 1 || result.Script.Actions[0].Type != "jms_request_reply" {
		t.Fatalf("expected one jms_request_reply action, got %+v", result.Script.Actions)
	}
	if got := result.Script.Actions[0].Params["correlation_id"]; got != "{{fn.uuid()}}" {
		t.Fatalf("correlation_id param = %v, want generated body/header correlation", got)
	}

	report, err := engine.ReplayScript(context.Background(), result.Script, engine.ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatalf("ReplayScript returned error: %v", err)
	}
	if !report.Success {
		t.Fatalf("replay failed: %s events=%#v", report.Error, report.Events)
	}
	if got := report.Events[0].Extractions["jms_correlation_id"]; got == "" {
		t.Fatalf("missing jms_correlation_id extraction: %#v", report.Events[0].Extractions)
	}
	assertNoAsyncAPIHeaders(t, report)
}

func TestAsyncAPIImportReplaySmokeMixedKafkaJMSSplit(t *testing.T) {
	result, err := Convert(ImportRequest{
		Name: "AsyncAPI Mixed Smoke",
		Options: ConvertOptions{
			IncludeAssertions: boolPtr(true),
		},
		Content: `
asyncapi: 3.0.0
info:
  title: Mixed Smoke
  version: 1.0.0
servers:
  kafkaBroker:
    host: kafka:9092
    protocol: kafka
  amqpBroker:
    host: amqp.example.test:5671
    protocol: amqp
channels:
  orders:
    address: asyncapi.smoke.mixed.orders
    servers:
      - $ref: '#/servers/kafkaBroker'
    messages:
      order:
        payload:
          type: object
          properties:
            orderId:
              type: string
  shipments:
    address: asyncapi.smoke.mixed.shipments
    servers:
      - $ref: '#/servers/amqpBroker'
    bindings:
      jms:
        destinationType: topic
    messages:
      shipment:
        payload:
          type: object
          properties:
            shipmentId:
              type: string
operations:
  publishOrder:
    action: send
    channel:
      $ref: '#/channels/orders'
  consumeOrder:
    action: receive
    channel:
      $ref: '#/channels/orders'
  publishShipment:
    action: send
    channel:
      $ref: '#/channels/shipments'
  consumeShipment:
    action: receive
    channel:
      $ref: '#/channels/shipments'
`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Scripts) != 2 {
		t.Fatalf("scripts = %d, want Kafka + JMS split", len(result.Scripts))
	}

	var kafkaScript, jmsScript bool
	for _, script := range result.Scripts {
		report, err := engine.ReplayScript(context.Background(), script, engine.ReplayOptions{IgnoreThinkTime: true})
		if err != nil {
			t.Fatalf("ReplayScript(%s) returned error: %v", script.Protocol, err)
		}
		if !report.Success {
			t.Fatalf("replay failed for %s: %s events=%#v", script.Protocol, report.Error, report.Events)
		}
		assertNoAsyncAPIHeaders(t, report)
		switch script.Protocol {
		case "kafka":
			kafkaScript = true
			if got := script.Config.Kafka.AutoOffsetReset; got != "earliest" {
				t.Fatalf("kafka auto_offset_reset = %q, want earliest", got)
			}
		case "jms-solace":
			jmsScript = true
			if got := script.Config.JMS.BrokerURL; got != "amqp.example.test:5671" {
				t.Fatalf("jms broker_url = %q, want amqp.example.test:5671", got)
			}
		}
	}
	if !kafkaScript || !jmsScript {
		t.Fatalf("split scripts missing Kafka/JMS: %+v", result.Scripts)
	}
}

func assertNoAsyncAPIHeaders(t *testing.T, report *engine.ReplayReport) {
	t.Helper()
	for _, event := range report.Events {
		for key := range event.Headers {
			if strings.HasPrefix(strings.ToLower(key), "asyncapi_") {
				t.Fatalf("event %q leaked AsyncAPI header %q", event.ActionName, key)
			}
		}
	}
}

func boolPtr(v bool) *bool {
	return &v
}

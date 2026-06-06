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

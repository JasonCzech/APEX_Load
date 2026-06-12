//go:build jms_servicebus_real && jms_servicebus_integration

package jms

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestServiceBusIntegrationQueueRoundTrip(t *testing.T) {
	cfg := serviceBusIntegrationJMSConfig(t)
	queue := requiredServiceBusIntegrationEnv(t, "SERVICEBUS_QUEUE")
	cfg["jms_destination"] = queue

	p := &JMS{name: "jms-servicebus", provider: "servicebus"}
	if err := p.Init(context.Background(), cfg); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	defer p.Teardown(context.Background())

	body := []byte("apexload-servicebus-queue-" + time.Now().Format(time.RFC3339Nano))
	published, err := p.Execute(context.Background(), &protocol.Action{Name: "publish", Type: "jms_publish", Body: body})
	if err != nil {
		t.Fatalf("publish returned error: %v", err)
	}
	if !published.Success {
		t.Fatalf("publish failed: %s", published.ErrorMessage)
	}
	consumed, err := p.Execute(context.Background(), &protocol.Action{
		Name: "consume",
		Type: "jms_consume",
		Assertions: []protocol.Assertion{{
			Type:  "body_contains",
			Value: string(body),
		}},
	})
	if err != nil {
		t.Fatalf("consume returned error: %v", err)
	}
	if !consumed.Success {
		t.Fatalf("consume failed: %s", consumed.ErrorMessage)
	}
}

func TestServiceBusIntegrationTopicSubscriptionRoundTrip(t *testing.T) {
	topic := os.Getenv("SERVICEBUS_TOPIC")
	subscription := os.Getenv("SERVICEBUS_SUBSCRIPTION")
	if strings.TrimSpace(topic) == "" || strings.TrimSpace(subscription) == "" {
		t.Skip("SERVICEBUS_TOPIC and SERVICEBUS_SUBSCRIPTION are required")
	}
	cfg := serviceBusIntegrationJMSConfig(t)
	cfg["jms_destination"] = topic
	cfg["jms_destination_type"] = "topic"
	cfg["jms_subscription"] = subscription

	p := &JMS{name: "jms-servicebus", provider: "servicebus"}
	if err := p.Init(context.Background(), cfg); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	defer p.Teardown(context.Background())

	body := []byte("apexload-servicebus-topic-" + time.Now().Format(time.RFC3339Nano))
	published, err := p.Execute(context.Background(), &protocol.Action{Name: "publish", Type: "jms_publish", Body: body})
	if err != nil {
		t.Fatalf("publish returned error: %v", err)
	}
	if !published.Success {
		t.Fatalf("publish failed: %s", published.ErrorMessage)
	}
	consumed, err := p.Execute(context.Background(), &protocol.Action{
		Name: "consume",
		Type: "jms_consume",
		Assertions: []protocol.Assertion{{
			Type:  "body_contains",
			Value: string(body),
		}},
	})
	if err != nil {
		t.Fatalf("consume returned error: %v", err)
	}
	if !consumed.Success {
		t.Fatalf("consume failed: %s", consumed.ErrorMessage)
	}
}

func TestServiceBusIntegrationRequestReplyCorrelation(t *testing.T) {
	requestQueue := requiredServiceBusIntegrationEnv(t, "SERVICEBUS_QUEUE")
	replyQueue := os.Getenv("SERVICEBUS_REPLY_QUEUE")
	if strings.TrimSpace(replyQueue) == "" {
		t.Skip("SERVICEBUS_REPLY_QUEUE is required")
	}
	cfg := serviceBusIntegrationJMSConfig(t)
	cfg["jms_destination"] = requestQueue
	cfg["jms_reply_to"] = replyQueue

	conn, err := resolveServiceBusConn(config{BrokerURL: cfg["broker_url"].(string)})
	if err != nil {
		t.Fatalf("resolve Service Bus connection: %v", err)
	}
	client, err := newServiceBusClient(conn)
	if err != nil {
		t.Fatalf("new Service Bus client: %v", err)
	}
	defer client.Close(context.Background())
	requestReceiver, err := client.NewReceiverForQueue(requestQueue, nil)
	if err != nil {
		t.Fatalf("new request receiver: %v", err)
	}
	defer requestReceiver.Close(context.Background())
	replySender, err := client.NewSender(replyQueue, nil)
	if err != nil {
		t.Fatalf("new reply sender: %v", err)
	}
	defer replySender.Close(context.Background())

	responderDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		messages, err := requestReceiver.ReceiveMessages(ctx, 1, nil)
		if err != nil {
			responderDone <- err
			return
		}
		if len(messages) == 0 {
			responderDone <- context.DeadlineExceeded
			return
		}
		req := messages[0]
		correlationID := req.MessageID
		if req.CorrelationID != nil && *req.CorrelationID != "" {
			correlationID = *req.CorrelationID
		}
		if err := requestReceiver.CompleteMessage(context.Background(), req, nil); err != nil {
			responderDone <- err
			return
		}
		responderDone <- replySender.SendMessage(context.Background(), &azservicebus.Message{
			Body:          []byte("apexload-servicebus-reply"),
			CorrelationID: &correlationID,
		}, nil)
	}()

	p := &JMS{name: "jms-servicebus", provider: "servicebus"}
	if err := p.Init(context.Background(), cfg); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	defer p.Teardown(context.Background())
	result, err := p.Execute(context.Background(), &protocol.Action{
		Name: "request",
		Type: "jms_request_reply",
		Body: []byte("apexload-servicebus-request"),
	})
	if err != nil {
		t.Fatalf("request_reply returned error: %v", err)
	}
	if responderErr := <-responderDone; responderErr != nil {
		t.Fatalf("responder returned error: %v", responderErr)
	}
	if !result.Success {
		t.Fatalf("request_reply failed: %s", result.ErrorMessage)
	}
	if result.Extractions["jms_correlation_id"] == "" {
		t.Fatal("jms_correlation_id is empty")
	}
}

func TestServiceBusIntegrationReceiveAndDelete(t *testing.T) {
	cfg := serviceBusIntegrationJMSConfig(t)
	queue := requiredServiceBusIntegrationEnv(t, "SERVICEBUS_QUEUE")
	cfg["jms_destination"] = queue
	cfg["jms_properties"] = map[string]string{"servicebus_receive_mode": "receive_and_delete"}

	p := &JMS{name: "jms-servicebus", provider: "servicebus"}
	if err := p.Init(context.Background(), cfg); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	defer p.Teardown(context.Background())

	body := []byte("apexload-servicebus-receive-delete-" + time.Now().Format(time.RFC3339Nano))
	if _, err := p.Execute(context.Background(), &protocol.Action{Name: "publish", Type: "jms_publish", Body: body}); err != nil {
		t.Fatalf("publish returned error: %v", err)
	}
	result, err := p.Execute(context.Background(), &protocol.Action{
		Name: "consume",
		Type: "jms_consume",
		Assertions: []protocol.Assertion{{
			Type:  "body_contains",
			Value: string(body),
		}},
	})
	if err != nil {
		t.Fatalf("consume returned error: %v", err)
	}
	if !result.Success {
		t.Fatalf("consume failed: %s", result.ErrorMessage)
	}
}

func serviceBusIntegrationJMSConfig(t *testing.T) map[string]any {
	t.Helper()
	brokerURL := os.Getenv("SERVICEBUS_CONNECTION_STRING")
	if strings.TrimSpace(brokerURL) == "" {
		brokerURL = os.Getenv("SERVICEBUS_NAMESPACE")
	}
	if strings.TrimSpace(brokerURL) == "" {
		t.Skip("SERVICEBUS_CONNECTION_STRING or SERVICEBUS_NAMESPACE is required")
	}
	return map[string]any{
		"broker_url":     brokerURL,
		"jms_provider":   "servicebus",
		"jms_mock":       false,
		"jms_timeout_ms": float64(30000),
	}
}

func requiredServiceBusIntegrationEnv(t *testing.T, key string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		t.Skip(key + " is required")
	}
	return value
}

//go:build jms_solace_real && jms_solace_integration

package jms

import (
	"context"
	"os"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// Live Solace AMQP smoke test. Run manually with:
//
//	SOLACE_AMQP_URL=amqps://broker.example.com:5671 \
//	SOLACE_USERNAME=user SOLACE_PASSWORD=pass SOLACE_VPN=vpn \
//	SOLACE_QUEUE=queue/perf/request \
//	go test -tags "jms_solace_real jms_solace_integration" ./pkg/protocol/jms
//
// A PubSub+ Standard broker with AMQP enabled is sufficient.
func TestSolaceAMQPIntegrationPublish(t *testing.T) {
	brokerURL := os.Getenv("SOLACE_AMQP_URL")
	queue := os.Getenv("SOLACE_QUEUE")
	if brokerURL == "" || queue == "" {
		t.Skip("set SOLACE_AMQP_URL and SOLACE_QUEUE to run the live Solace AMQP integration test")
	}
	p := &JMS{name: "jms-solace", provider: "solace"}
	if err := p.Init(context.Background(), map[string]any{
		"broker_url":      brokerURL,
		"jms_provider":    "solace",
		"jms_message_vpn": os.Getenv("SOLACE_VPN"),
		"jms_username":    os.Getenv("SOLACE_USERNAME"),
		"jms_password":    os.Getenv("SOLACE_PASSWORD"),
		"jms_destination": queue,
	}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	defer p.Teardown(context.Background())

	result, err := p.Execute(context.Background(), &protocol.Action{
		Name:   "publish",
		Type:   "jms_publish",
		Target: queue,
		Body:   []byte("apexload-solace-integration"),
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !result.Success {
		t.Fatalf("publish failed: %#v", result)
	}
}

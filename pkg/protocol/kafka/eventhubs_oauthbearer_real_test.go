//go:build kafka_real

package kafka

import (
	"context"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// TestEventHubsOAuthBearerFailsFastUnderTag verifies that an eventhubs+oauthbearer
// script fails fast with a clear "not yet supported (EH.2)" error under the real
// (kafka_real) adapter instead of silently downgrading to PLAIN and failing
// opaquely at the SASL handshake.
func TestEventHubsOAuthBearerFailsFastUnderTag(t *testing.T) {
	k := &Kafka{name: "kafka"}
	if err := k.Init(context.Background(), map[string]any{
		"kafka_profile":        "eventhubs",
		"kafka_brokers":        []string{"ns.servicebus.windows.net:9093"},
		"kafka_sasl_mechanism": "oauthbearer",
		"kafka_topic":          "events",
		"kafka_mock":           false,
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	res, err := k.Execute(context.Background(), &protocol.Action{Name: "p", Type: "kafka_produce", Body: []byte("x")})
	if err != nil {
		t.Fatalf("Execute returned transport error: %v", err)
	}
	if res.Success {
		t.Fatal("expected oauthbearer produce to fail fast under kafka_real")
	}
	if !strings.Contains(res.ErrorMessage, "oauthbearer") || !strings.Contains(res.ErrorMessage, "not yet supported") {
		t.Fatalf("error message %q should explain oauthbearer is not yet supported", res.ErrorMessage)
	}
}

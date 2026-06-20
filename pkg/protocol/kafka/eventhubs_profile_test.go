package kafka

import (
	"context"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// TestKafkaEventHubsProfileDefaults verifies the Azure Event Hubs Kafka-endpoint
// profile normalizes auth/transport to the values Event Hubs mandates.
func TestKafkaEventHubsProfileDefaults(t *testing.T) {
	k := &Kafka{name: "kafka"}
	if err := k.Init(context.Background(), map[string]any{
		"kafka_profile": "eventhubs",
		"kafka_brokers": []string{"ns.servicebus.windows.net:9093"},
		"kafka_topic":   "events",
		"kafka_mock":    true,
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if k.cfg.SecurityProtocol != "sasl_ssl" {
		t.Errorf("security_protocol = %q, want sasl_ssl", k.cfg.SecurityProtocol)
	}
	if k.cfg.SASLMechanism != "plain" {
		t.Errorf("sasl_mechanism = %q, want plain", k.cfg.SASLMechanism)
	}
	if k.cfg.SASLUsername != "$ConnectionString" {
		t.Errorf("sasl_username = %q, want $ConnectionString", k.cfg.SASLUsername)
	}
	if k.cfg.MaxMessageBytes != 1<<20 {
		t.Errorf("max_message_bytes = %d, want %d", k.cfg.MaxMessageBytes, 1<<20)
	}
}

// TestKafkaEventHubsProfilePreservesExplicitValues confirms the profile only
// DEFAULTS values — operator-supplied values are preserved, not clobbered. (The
// validator, not the runtime, rejects a conflicting explicit security_protocol.)
func TestKafkaEventHubsProfilePreservesExplicitValues(t *testing.T) {
	k := &Kafka{name: "kafka"}
	if err := k.Init(context.Background(), map[string]any{
		"kafka_profile":           "eventhubs",
		"kafka_sasl_username":     "customuser",
		"kafka_max_message_bytes": float64(2048),
		"kafka_security_protocol": "sasl_plaintext", // explicit value must be preserved, not clobbered
		"kafka_topic":             "events",
		"kafka_mock":              true,
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if k.cfg.SASLUsername != "customuser" {
		t.Errorf("sasl_username overwritten: %q", k.cfg.SASLUsername)
	}
	if k.cfg.MaxMessageBytes != 2048 {
		t.Errorf("max_message_bytes = %d, want 2048", k.cfg.MaxMessageBytes)
	}
	if k.cfg.SecurityProtocol != "sasl_plaintext" {
		t.Errorf("security_protocol = %q, want sasl_plaintext (explicit value preserved)", k.cfg.SecurityProtocol)
	}
}

// TestKafkaProducerConsumerKnobsFlowToConfig verifies the P2.1 idempotence and
// isolation-level knobs are parsed into the runtime config.
func TestKafkaProducerConsumerKnobsFlowToConfig(t *testing.T) {
	k := &Kafka{name: "kafka"}
	if err := k.Init(context.Background(), map[string]any{
		"kafka_topic":           "events",
		"kafka_mock":            true,
		"kafka_idempotence":     "disabled",
		"kafka_isolation_level": "read_committed",
		"kafka_oauth_scope":     "https://ns.servicebus.windows.net/.default",
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if k.cfg.Idempotence != "disabled" {
		t.Errorf("idempotence = %q, want disabled", k.cfg.Idempotence)
	}
	if k.cfg.IsolationLevel != "read_committed" {
		t.Errorf("isolation_level = %q, want read_committed", k.cfg.IsolationLevel)
	}
	if k.cfg.OAuthScope != "https://ns.servicebus.windows.net/.default" {
		t.Errorf("oauth_scope = %q, want the Event Hubs scope", k.cfg.OAuthScope)
	}
}

// TestKafkaMaxMessageBytesGuard verifies the producer-side size guard rejects
// records over the cap and admits records under it.
func TestKafkaMaxMessageBytesGuard(t *testing.T) {
	over := newMockKafka(t, map[string]any{
		"kafka_topic":             "events",
		"kafka_max_message_bytes": float64(8),
	})
	res, err := over.Execute(context.Background(), &protocol.Action{Name: "p", Type: "kafka_produce", Body: []byte("this is far more than eight bytes")})
	if err != nil {
		t.Fatalf("Execute returned transport error: %v", err)
	}
	if res.Success {
		t.Fatal("expected produce to fail when body exceeds max_message_bytes")
	}
	if !strings.Contains(res.ErrorMessage, "max_message_bytes") {
		t.Fatalf("error message %q should mention max_message_bytes", res.ErrorMessage)
	}

	under := newMockKafka(t, map[string]any{
		"kafka_topic":             "events",
		"kafka_max_message_bytes": float64(1024),
	})
	res2, err := under.Execute(context.Background(), &protocol.Action{Name: "p", Type: "kafka_produce", Body: []byte("small")})
	if err != nil {
		t.Fatalf("Execute returned transport error: %v", err)
	}
	if !res2.Success {
		t.Fatalf("small body should pass the guard, got error: %s", res2.ErrorMessage)
	}
}

package importreview

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestValidatePreservedImportCredentialEditsAllowsBrokerAndJavaEdits(t *testing.T) {
	prior := &dsl.Script{
		Name: "Kafka & Java edits", Version: "1", Protocol: "kafka",
		Config: dsl.ScriptConfig{
			Kafka: dsl.KafkaConfig{
				Brokers:       []string{"broker-1.example.test:9092"},
				SASLMechanism: "PLAIN",
				SASLUsername:  "svc",
				SASLPassword:  "synthetic-kafka-sasl-password",
			},
		},
		Actions: []dsl.Action{{
			Name: "produce", Type: "kafka_produce", Target: "orders", Body: `{"id":1}`,
		}},
	}
	MarkCredentialPreservingImportScript(prior)

	candidate := CloneScriptDefinition(prior)
	candidate.Config.Kafka.Brokers = []string{"broker-staging.example.test:9092"}
	candidate.Actions[0].Target = "orders-v2"

	if err := ValidatePreservedImportCredentialEdits(candidate, prior); err != nil {
		t.Fatalf("ValidatePreservedImportCredentialEdits returned unexpected error: %v", err)
	}
	saved, err := PreparePersistentLibraryScript(candidate, prior)
	if err != nil {
		t.Fatalf("PreparePersistentLibraryScript returned unexpected error: %v", err)
	}
	if saved.Config.Kafka.SASLPassword != "synthetic-kafka-sasl-password" {
		t.Fatalf("saved SASLPassword = %q, want literal preserved", saved.Config.Kafka.SASLPassword)
	}
}

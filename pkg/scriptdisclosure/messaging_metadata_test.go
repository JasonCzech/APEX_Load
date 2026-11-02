package scriptdisclosure

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// A Kafka or JMS send delivers import_action_uid as a record header or message
// property, so a known credential there must be masked like any other header
// value. On a step that never sends its metadata the UID stays verbatim.
func TestProjectMasksKnownCredentialInSentMessagingActionUID(t *testing.T) {
	const secret = "fixture-credential-Disclosure0123456789abcdef"
	script := &dsl.Script{
		ID: "kafka-uid", Name: "Kafka UID", Version: "1", Protocol: "kafka",
		Config: dsl.ScriptConfig{
			HTTP: dsl.HTTPConfig{
				Auth: dsl.AuthConfig{Type: "basic", Username: "operator", Password: secret},
			},
		},
		Actions: []dsl.Action{
			{
				Name: "produce", Type: "kafka_produce", Target: "orders",
				Metadata: map[string]any{dsl.ImportActionUIDMetadataKey: secret},
			},
			{
				Name: "publish", Type: "jms_publish", Target: "queue://orders",
				Metadata: map[string]any{dsl.ImportActionUIDMetadataKey: secret},
			},
			{
				Name: "consume", Type: "kafka_consume", Target: "orders",
				Metadata: map[string]any{dsl.ImportActionUIDMetadataKey: secret},
			},
		},
	}
	projected, err := ProjectWithCredentialSource(script, script)
	if err != nil {
		t.Fatalf("ProjectWithCredentialSource: %v", err)
	}
	for index := 0; index < 2; index++ {
		if got := projected.Actions[index].Metadata[dsl.ImportActionUIDMetadataKey]; got == secret {
			t.Fatalf("%s import_action_uid disclosed a known credential: %#v", script.Actions[index].Type, got)
		}
	}
	if got := projected.Actions[2].Metadata[dsl.ImportActionUIDMetadataKey]; got != secret {
		t.Fatalf("unsent import_action_uid = %#v, want it kept verbatim", got)
	}
}

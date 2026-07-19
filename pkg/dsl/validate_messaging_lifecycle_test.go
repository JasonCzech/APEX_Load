package dsl

import (
	"strings"
	"testing"
)

func TestValidateMessagingProtocolsRunCommonLifecycleAndNestedActionValidation(t *testing.T) {
	tests := []struct {
		name       string
		protocol   string
		validType  string
		configure  func(*Script)
		familyName string
	}{
		{
			name: "jms", protocol: "jms-solace", validType: "jms_publish", familyName: "JMS",
			configure: func(script *Script) {
				script.Config.JMS = JMSConfig{Provider: "solace", Destination: "orders", Mock: true}
			},
		},
		{
			name: "kafka", protocol: "kafka", validType: "kafka_produce", familyName: "Kafka",
			configure: func(script *Script) {
				script.Config.Kafka = KafkaConfig{Topic: "orders", Mock: true}
			},
		},
		{
			name: "event hubs", protocol: "eventhubs", validType: "eventhubs_produce", familyName: "Event Hubs",
			configure: func(script *Script) {
				script.Config.EventHubs = EventHubsConfig{EventHub: "orders", Mock: true}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			script := &Script{
				Name:     test.name + " lifecycle",
				Protocol: test.protocol,
				Init:     []Action{{Type: test.validType}},
				Actions: []Action{{
					Name: "branch", Type: "control_if",
					Control: &ControlFlow{Steps: []Action{{Name: "nested bad action", Type: "unsupported_messaging_action"}}},
				}},
				Teardown: []Action{{Name: "bad teardown", Type: "unsupported_messaging_action"}},
			}
			test.configure(script)
			issues := ValidateScriptIssues(script)
			for _, expected := range []string{
				"init[0]: missing name",
				"actions[0].control.steps[0] \"nested bad action\": unsupported " + test.familyName + " action type",
				"teardown[0] \"bad teardown\": unsupported " + test.familyName + " action type",
			} {
				if !containsValidationIssue(issues, expected) {
					t.Fatalf("issues = %v, want substring %q", issues, expected)
				}
			}
		})
	}
}

func containsValidationIssue(issues []string, expected string) bool {
	for _, issue := range issues {
		if strings.Contains(issue, expected) {
			return true
		}
	}
	return false
}

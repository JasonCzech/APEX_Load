package dsl

import (
	"strings"
	"testing"

	jmsprotocol "github.com/jasonczech/apexload/pkg/protocol/jms"
	kafkaprotocol "github.com/jasonczech/apexload/pkg/protocol/kafka"
)

func TestValidateMessagingSemanticsAcrossLifecycleAndControlTrees(t *testing.T) {
	tests := []struct {
		name   string
		script *Script
		want   string
	}{
		{
			name: "JMS init missing destination",
			script: &Script{
				Name: "jms-init", Protocol: "jms-solace",
				Config:  ScriptConfig{JMS: JMSConfig{Provider: "solace", Mock: true}},
				Init:    []Action{{Name: "consume init", Type: "jms_consume"}},
				Actions: []Action{{Name: "publish", Type: "jms_publish", Target: "orders"}},
			},
			want: `init[0] "consume init": missing destination`,
		},
		{
			name: "JMS teardown missing reply-to",
			script: &Script{
				Name: "jms-teardown", Protocol: "jms-solace",
				Config:   ScriptConfig{JMS: JMSConfig{Provider: "solace", Destination: "orders", Mock: true}},
				Actions:  []Action{{Name: "publish", Type: "jms_publish"}},
				Teardown: []Action{{Name: "request teardown", Type: "jms_request_reply"}},
			},
			want: `teardown[0] "request teardown": missing reply_to destination`,
		},
		{
			name: "Service Bus nested topic subscription",
			script: &Script{
				Name: "servicebus-nested", Protocol: "jms-servicebus",
				Config: ScriptConfig{JMS: JMSConfig{Provider: "servicebus", Destination: "orders", DestinationType: "topic", Mock: true}},
				Actions: []Action{{Name: "loop", Type: "control_loop", Control: &ControlFlow{Count: 1, Steps: []Action{{
					Name: "nested consume", Type: "jms_consume",
				}}}}},
			},
			want: `actions[0].control.steps[0] "nested consume": subscription required to consume from a Service Bus topic`,
		},
		{
			name: "JMS nested timeout bound",
			script: &Script{
				Name: "jms-bound", Protocol: "jms-solace",
				Config: ScriptConfig{JMS: JMSConfig{Provider: "solace", Destination: "orders", Mock: true}},
				Actions: []Action{{Name: "loop", Type: "control_loop", Control: &ControlFlow{Count: 1, Steps: []Action{{
					Name: "nested publish", Type: "jms_publish", Params: map[string]any{"timeout_ms": int(jmsprotocol.MaxConfiguredTimeout.Milliseconds()) + 1},
				}}}}},
			},
			want: `actions[0].control.steps[0] "nested publish": timeout_ms must be between`,
		},
		{
			name: "Kafka init missing topic",
			script: &Script{
				Name: "kafka-init", Protocol: "kafka",
				Config:  ScriptConfig{Kafka: KafkaConfig{Mock: true}},
				Init:    []Action{{Name: "consume init", Type: "kafka_consume"}},
				Actions: []Action{{Name: "produce", Type: "kafka_produce", Target: "orders"}},
			},
			want: `init[0] "consume init": missing topic`,
		},
		{
			name: "Kafka teardown missing reply-to",
			script: &Script{
				Name: "kafka-teardown", Protocol: "kafka",
				Config:   ScriptConfig{Kafka: KafkaConfig{Topic: "orders", Mock: true}},
				Actions:  []Action{{Name: "produce", Type: "kafka_produce"}},
				Teardown: []Action{{Name: "request teardown", Type: "kafka_request_reply"}},
			},
			want: `teardown[0] "request teardown": missing reply_to topic`,
		},
		{
			name: "Kafka nested partition bound in else",
			script: &Script{
				Name: "kafka-bound", Protocol: "kafka",
				Config: ScriptConfig{Kafka: KafkaConfig{Topic: "orders", Mock: true}},
				Actions: []Action{{Name: "branch", Type: "control_if", Control: &ControlFlow{
					Condition: &Condition{Left: "x", Op: "eq", Right: "y"},
					Steps:     []Action{{Name: "valid", Type: "kafka_produce"}},
					Else: []Action{{Name: "nested consume", Type: "kafka_consume", Params: map[string]any{
						"partition": int64(kafkaprotocol.MaxConfiguredPartitions) + 1<<32,
					}}},
				}}},
			},
			want: `actions[0].control.else[0] "nested consume": partition must be between`,
		},
		{
			name: "Kafka nested transaction configuration",
			script: &Script{
				Name: "kafka-transaction", Protocol: "kafka",
				Config: ScriptConfig{Kafka: KafkaConfig{Topic: "orders", Mock: true}},
				Actions: []Action{{Name: "loop", Type: "control_loop", Control: &ControlFlow{Count: 1, Steps: []Action{{
					Name: "begin", Type: "kafka_begin_transaction",
				}}}}},
			},
			want: `actions[0].control.steps[0] "begin": kafka_begin_transaction requires config.kafka.transactional_id`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			issues := ValidateScriptIssues(test.script)
			if !containsValidationIssue(issues, test.want) {
				t.Fatalf("issues = %v, want substring %q", issues, test.want)
			}
		})
	}
}

func TestValidateJMSProviderProtocolContract(t *testing.T) {
	tests := []struct {
		name     string
		protocol string
		provider string
		want     string
	}{
		{name: "unknown provider", protocol: "jms-solace", provider: "activemq", want: `config.jms.provider "activemq" is not supported`},
		{name: "mismatched provider", protocol: "jms-solace", provider: "servicebus", want: `config.jms.provider "servicebus" does not match protocol "jms-solace"`},
		{name: "generic protocol", protocol: "jms", provider: "solace", want: `protocol "jms" is not executable`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			script := &Script{
				Name: test.name, Protocol: test.protocol,
				Config:  ScriptConfig{JMS: JMSConfig{Provider: test.provider, Destination: "orders", Mock: true}},
				Actions: []Action{{Name: "publish", Type: "jms_publish"}},
			}
			issues := ValidateScriptIssues(script)
			if !containsValidationIssue(issues, test.want) {
				t.Fatalf("issues = %v, want substring %q", issues, test.want)
			}
		})
	}
}

func TestValidateJMSProviderMayBeInferredFromSpecificProtocol(t *testing.T) {
	script := &Script{
		Name: "inferred provider", Protocol: "jms-solace",
		Config:  ScriptConfig{JMS: JMSConfig{Destination: "orders", Mock: true}},
		Actions: []Action{{Name: "publish", Type: "jms_publish"}},
	}
	if issues := ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("omitted provider should be inferred from jms-solace, got issues %v", issues)
	}
}

func TestMessagingCommonActionIssuesAreNotDuplicated(t *testing.T) {
	for _, script := range []*Script{
		{Name: "jms", Protocol: "jms-solace", Config: ScriptConfig{JMS: JMSConfig{Provider: "solace", Destination: "orders", Mock: true}}, Actions: []Action{{Type: "jms_publish"}}},
		{Name: "kafka", Protocol: "kafka", Config: ScriptConfig{Kafka: KafkaConfig{Topic: "orders", Mock: true}}, Actions: []Action{{Type: "kafka_produce"}}},
	} {
		issues := ValidateScriptIssues(script)
		count := 0
		for _, issue := range issues {
			if strings.Contains(issue, "actions[0]: missing name") {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("protocol %s issues = %v, missing-name count = %d, want 1", script.Protocol, issues, count)
		}
	}
}

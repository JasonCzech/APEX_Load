package dsl

import (
	"strings"
	"testing"
)

func kafkaScript(mutate func(*Script)) *Script {
	s := &Script{
		Name:     "kafka-flow",
		Protocol: "kafka",
		Config: ScriptConfig{
			Kafka: KafkaConfig{
				Mock:  true,
				Topic: "orders.events",
			},
		},
		Actions: []Action{
			{Name: "produce", Type: "kafka_produce", Body: "{}"},
		},
	}
	if mutate != nil {
		mutate(s)
	}
	return s
}

func hasIssueContaining(issues []string, substr string) bool {
	for _, issue := range issues {
		if strings.Contains(issue, substr) {
			return true
		}
	}
	return false
}

func TestValidateKafkaScriptIssues(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*Script)
		wantClean bool
		wantHas   string
	}{
		{
			name:      "valid mock script",
			mutate:    nil,
			wantClean: true,
		},
		{
			name: "real mode without brokers",
			mutate: func(s *Script) {
				s.Config.Kafka.Mock = false
				s.Config.Kafka.Brokers = nil
			},
			wantHas: "config.kafka.brokers is required",
		},
		{
			name: "real mode is flagged unavailable",
			mutate: func(s *Script) {
				s.Config.Kafka.Mock = false
				s.Config.Kafka.Brokers = []string{"broker:9092"}
			},
			wantHas: "real Kafka network adapter is not available",
		},
		{
			name: "bad security protocol",
			mutate: func(s *Script) {
				s.Config.Kafka.SecurityProtocol = "carrier-pigeon"
			},
			wantHas: "security_protocol",
		},
		{
			name: "bad acks",
			mutate: func(s *Script) {
				s.Config.Kafka.Acks = "maybe"
			},
			wantHas: "config.kafka.acks",
		},
		{
			name: "bad compression",
			mutate: func(s *Script) {
				s.Config.Kafka.Compression = "brotli"
			},
			wantHas: "config.kafka.compression",
		},
		{
			name: "bad partition strategy",
			mutate: func(s *Script) {
				s.Config.Kafka.PartitionStrategy = "psychic"
			},
			wantHas: "partition_strategy",
		},
		{
			name: "unsupported action type",
			mutate: func(s *Script) {
				s.Actions[0].Type = "kafka_teleport"
			},
			wantHas: "unsupported Kafka action type",
		},
		{
			name: "missing topic everywhere",
			mutate: func(s *Script) {
				s.Config.Kafka.Topic = ""
				s.Actions[0].Target = ""
			},
			wantHas: "missing topic",
		},
		{
			name: "topic via action target is fine",
			mutate: func(s *Script) {
				s.Config.Kafka.Topic = ""
				s.Actions[0].Target = "orders.events"
			},
			wantClean: true,
		},
		{
			name: "request_reply needs reply_to",
			mutate: func(s *Script) {
				s.Actions[0] = Action{Name: "rpc", Type: "kafka_request_reply", Body: "{}"}
			},
			wantHas: "missing reply_to topic",
		},
		{
			name: "request_reply with reply_to is fine",
			mutate: func(s *Script) {
				s.Actions[0] = Action{Name: "rpc", Type: "kafka_request_reply", Body: "{}", Params: map[string]any{"reply_to": "orders.replies"}}
			},
			wantClean: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issues := ValidateScriptIssues(kafkaScript(tt.mutate))
			if tt.wantClean {
				if len(issues) != 0 {
					t.Fatalf("expected no issues, got: %v", issues)
				}
				return
			}
			if !hasIssueContaining(issues, tt.wantHas) {
				t.Fatalf("expected an issue containing %q, got: %v", tt.wantHas, issues)
			}
		})
	}
}

func TestKafkaProtocolAllowsEmptyBaseURL(t *testing.T) {
	if !protocolAllowsEmptyBaseURL("kafka") {
		t.Fatal("kafka should allow an empty base_url")
	}
}

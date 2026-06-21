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
			name: "bad profile value",
			mutate: func(s *Script) {
				s.Config.Kafka.Profile = "kinesis"
			},
			wantHas: "config.kafka.profile",
		},
		{
			name: "eventhubs profile on mock skips enforcement",
			mutate: func(s *Script) {
				s.Config.Kafka.Profile = "eventhubs"
			},
			wantClean: true,
		},
		{
			name: "eventhubs profile rejects plaintext",
			mutate: func(s *Script) {
				s.Config.Kafka.Mock = false
				s.Config.Kafka.Profile = "eventhubs"
				s.Config.Kafka.Brokers = []string{"ns.servicebus.windows.net:9093"}
				s.Config.Kafka.SecurityProtocol = "plaintext"
				s.Config.Kafka.SASLPasswordSecret = "eh_conn"
			},
			wantHas: "requires security_protocol sasl_ssl",
		},
		{
			name: "eventhubs profile requires credentials",
			mutate: func(s *Script) {
				s.Config.Kafka.Mock = false
				s.Config.Kafka.Profile = "eventhubs"
				s.Config.Kafka.Brokers = []string{"ns.servicebus.windows.net:9093"}
				s.Config.Kafka.SecurityProtocol = "sasl_ssl"
			},
			wantHas: "requires config.kafka.sasl_password",
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
			name: "bad idempotence",
			mutate: func(s *Script) {
				s.Config.Kafka.Idempotence = "sometimes"
			},
			wantHas: "config.kafka.idempotence",
		},
		{
			name: "idempotence enabled requires acks all",
			mutate: func(s *Script) {
				s.Config.Kafka.Idempotence = "enabled"
				s.Config.Kafka.Acks = "leader"
			},
			wantHas: "requires acks=all",
		},
		{
			name: "idempotence enabled with acks all is fine",
			mutate: func(s *Script) {
				s.Config.Kafka.Idempotence = "enabled"
				s.Config.Kafka.Acks = "all"
			},
			wantClean: true,
		},
		{
			name: "bad isolation level",
			mutate: func(s *Script) {
				s.Config.Kafka.IsolationLevel = "read_whatever"
			},
			wantHas: "config.kafka.isolation_level",
		},
		{
			name: "transactional_id requires acks all",
			mutate: func(s *Script) {
				s.Config.Kafka.TransactionalID = "tx-1"
				s.Config.Kafka.Acks = "leader"
			},
			wantHas: "transactional_id requires acks=all",
		},
		{
			name: "transactional_id with acks all is fine",
			mutate: func(s *Script) {
				s.Config.Kafka.TransactionalID = "tx-1"
				s.Config.Kafka.Acks = "all"
			},
			wantClean: true,
		},
		{
			name: "oauthbearer requires tls",
			mutate: func(s *Script) {
				s.Config.Kafka.SASLMechanism = "oauthbearer"
			},
			wantHas: "oauthbearer requires a TLS",
		},
		{
			name: "oauthbearer with sasl_ssl is fine",
			mutate: func(s *Script) {
				s.Config.Kafka.SASLMechanism = "oauthbearer"
				s.Config.Kafka.SecurityProtocol = "sasl_ssl"
			},
			wantClean: true,
		},
		{
			name: "eventhubs profile oauthbearer is fine",
			mutate: func(s *Script) {
				s.Config.Kafka.Profile = "eventhubs"
				s.Config.Kafka.SASLMechanism = "oauthbearer"
			},
			wantClean: true,
		},
		{
			name: "bad serialization",
			mutate: func(s *Script) {
				s.Config.Kafka.Serialization = "thrift"
			},
			wantHas: "config.kafka.serialization",
		},
		{
			name: "json_schema requires registry or id",
			mutate: func(s *Script) {
				s.Config.Kafka.Serialization = "json_schema"
			},
			wantHas: "requires config.kafka.schema_registry_url or schema_id",
		},
		{
			name: "json_schema with schema_id is fine",
			mutate: func(s *Script) {
				s.Config.Kafka.Serialization = "json_schema"
				s.Config.Kafka.SchemaID = 42
			},
			wantClean: true,
		},
		{
			name: "json_schema with registry url is fine",
			mutate: func(s *Script) {
				s.Config.Kafka.Serialization = "json_schema"
				s.Config.Kafka.SchemaRegistryURL = "http://registry:8081"
			},
			wantClean: true,
		},
		{
			name: "avro requires schema",
			mutate: func(s *Script) {
				s.Config.Kafka.Serialization = "avro"
				s.Config.Kafka.SchemaID = 7
			},
			wantHas: "avro requires config.kafka.schema",
		},
		{
			name: "avro with schema and id is fine",
			mutate: func(s *Script) {
				s.Config.Kafka.Serialization = "avro"
				s.Config.Kafka.SchemaID = 7
				s.Config.Kafka.Schema = `{"type":"record","name":"R","fields":[]}`
			},
			wantClean: true,
		},
		{
			name: "transaction actions are valid",
			mutate: func(s *Script) {
				s.Config.Kafka.TransactionalID = "tx-1"
				s.Actions = []Action{
					{Name: "b", Type: "kafka_begin_transaction"},
					{Name: "p", Type: "kafka_produce", Body: "{}"},
					{Name: "c", Type: "kafka_commit_transaction"},
				}
			},
			wantClean: true,
		},
		{
			name: "valid isolation read_committed",
			mutate: func(s *Script) {
				s.Config.Kafka.IsolationLevel = "read_committed"
			},
			wantClean: true,
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

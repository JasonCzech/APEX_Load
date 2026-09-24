package dsl

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/identitypolicy"
	kafkaprotocol "github.com/jasonczech/apexload/pkg/protocol/kafka"
)

func validateKafkaScriptIssues(script *Script) []string {
	var issues []string
	kafka := script.Config.Kafka
	if !kafka.Mock && !isBlockedJMeterMessagingDraft(script) {
		if len(kafka.Brokers) == 0 {
			issues = append(issues, "config.kafka.brokers is required when mock mode is disabled")
		}
		if !KafkaRealAdapterAvailable() {
			issues = append(issues, "real Kafka network adapter is not available in this build; enable config.kafka.mock for replay or build with -tags kafka_real")
		}
	}
	profile := strings.ToLower(strings.TrimSpace(kafka.Profile))
	if profile != "" && profile != "eventhubs" {
		issues = append(issues, fmt.Sprintf("config.kafka.profile %q is not supported (use \"eventhubs\")", kafka.Profile))
	}
	if profile == "eventhubs" && !kafka.Mock {
		// Azure Event Hubs requires TLS (sasl_ssl) and rejects plaintext transport.
		if secProto := strings.ToLower(strings.TrimSpace(kafka.SecurityProtocol)); secProto != "" && secProto != "sasl_ssl" {
			issues = append(issues, fmt.Sprintf("config.kafka.profile eventhubs requires security_protocol sasl_ssl, got %q", kafka.SecurityProtocol))
		}
		switch strings.ToLower(strings.TrimSpace(kafka.SASLMechanism)) {
		case "", "plain":
			if kafka.SASLPassword == "" && kafka.SASLPasswordSecret == "" {
				issues = append(issues, "config.kafka.profile eventhubs with PLAIN auth requires config.kafka.sasl_password or sasl_password_secret (the namespace connection string)")
			}
		case "oauthbearer":
			// Microsoft Entra ID / OAUTHBEARER auth (EH.2): credentials come from
			// the Azure environment (DefaultAzureCredential), so no SAS password
			// is required here.
		default:
			issues = append(issues, fmt.Sprintf("config.kafka.profile eventhubs supports sasl_mechanism plain or oauthbearer, got %q", kafka.SASLMechanism))
		}
	}
	if strings.EqualFold(strings.TrimSpace(kafka.SASLMechanism), "oauthbearer") {
		// OAUTHBEARER must run over TLS. The eventhubs profile forces sasl_ssl at
		// runtime, so accept it even when security_protocol is left unset.
		eventHubsProfile := strings.EqualFold(strings.TrimSpace(kafka.Profile), "eventhubs")
		if !eventHubsProfile && !strings.Contains(strings.ToLower(kafka.SecurityProtocol), "ssl") {
			issues = append(issues, "config.kafka.sasl_mechanism oauthbearer requires a TLS security_protocol (e.g. sasl_ssl)")
		}
		if !eventHubsProfile && !isBlockedJMeterMessagingDraft(script) {
			issues = append(issues, "config.kafka managed-identity oauthbearer requires profile eventhubs")
		}
		if kafka.TLS != nil && kafka.TLS.InsecureSkipVerify {
			issues = append(issues, "config.kafka managed-identity oauthbearer forbids tls.insecure_skip_verify")
		}
		if scope := strings.TrimSpace(kafka.OAuthScope); scope != "" && scope != identitypolicy.AzureEventHubsOAuthScope {
			issues = append(issues, fmt.Sprintf("config.kafka.oauth_scope must be %q for managed-identity oauthbearer", identitypolicy.AzureEventHubsOAuthScope))
		}
	}
	if kafka.SecurityProtocol != "" && !isAllowedValue(strings.ToLower(strings.TrimSpace(kafka.SecurityProtocol)), "plaintext", "ssl", "sasl_ssl", "sasl_plaintext") {
		issues = append(issues, fmt.Sprintf("config.kafka.security_protocol %q is not supported", kafka.SecurityProtocol))
	}
	if kafka.SASLMechanism != "" && !isAllowedValue(strings.ToLower(strings.TrimSpace(kafka.SASLMechanism)), "plain", "scram-sha-256", "scram-sha-512", "oauthbearer") {
		issues = append(issues, fmt.Sprintf("config.kafka.sasl_mechanism %q is not supported", kafka.SASLMechanism))
	}
	if kafka.Acks != "" && !isAllowedValue(strings.ToLower(strings.TrimSpace(kafka.Acks)), "none", "leader", "all") {
		issues = append(issues, fmt.Sprintf("config.kafka.acks %q is not supported", kafka.Acks))
	}
	if kafka.Compression != "" && !isAllowedValue(strings.ToLower(strings.TrimSpace(kafka.Compression)), "none", "gzip", "snappy", "lz4", "zstd") {
		issues = append(issues, fmt.Sprintf("config.kafka.compression %q is not supported", kafka.Compression))
	}
	if kafka.PartitionStrategy != "" && !isAllowedValue(strings.ToLower(strings.TrimSpace(kafka.PartitionStrategy)), "key-hash", "round-robin", "explicit") {
		issues = append(issues, fmt.Sprintf("config.kafka.partition_strategy %q is not supported", kafka.PartitionStrategy))
	}
	if kafka.AutoOffsetReset != "" && !isAllowedValue(strings.ToLower(strings.TrimSpace(kafka.AutoOffsetReset)), "earliest", "latest") {
		issues = append(issues, fmt.Sprintf("config.kafka.auto_offset_reset %q is not supported", kafka.AutoOffsetReset))
	}
	if kafka.Idempotence != "" {
		idem := strings.ToLower(strings.TrimSpace(kafka.Idempotence))
		if !isAllowedValue(idem, "enabled", "disabled") {
			issues = append(issues, fmt.Sprintf("config.kafka.idempotence %q is not supported (use enabled or disabled)", kafka.Idempotence))
		} else if idem == "enabled" {
			// Idempotent writes require acks=all (the default when acks is unset).
			if acks := strings.ToLower(strings.TrimSpace(kafka.Acks)); acks != "" && acks != "all" {
				issues = append(issues, fmt.Sprintf("config.kafka.idempotence enabled requires acks=all, got %q", kafka.Acks))
			}
		}
	}
	if kafka.IsolationLevel != "" && !isAllowedValue(strings.ToLower(strings.TrimSpace(kafka.IsolationLevel)), "read_uncommitted", "read_committed") {
		issues = append(issues, fmt.Sprintf("config.kafka.isolation_level %q is not supported", kafka.IsolationLevel))
	}
	if strings.TrimSpace(kafka.TransactionalID) != "" {
		// Transactional (EOS) producers require acks=all and idempotence.
		if acks := strings.ToLower(strings.TrimSpace(kafka.Acks)); acks != "" && acks != "all" {
			issues = append(issues, fmt.Sprintf("config.kafka.transactional_id requires acks=all, got %q", kafka.Acks))
		}
		if strings.ToLower(strings.TrimSpace(kafka.Idempotence)) == "disabled" {
			issues = append(issues, "config.kafka.transactional_id requires idempotence (remove idempotence: disabled)")
		}
	}
	if kafka.Serialization != "" {
		switch strings.ToLower(strings.TrimSpace(kafka.Serialization)) {
		case "raw":
		case "json_schema":
			if strings.TrimSpace(kafka.SchemaRegistryURL) == "" && kafka.SchemaID <= 0 {
				issues = append(issues, "config.kafka.serialization json_schema requires config.kafka.schema_registry_url or schema_id")
			}
		case "avro":
			if strings.TrimSpace(kafka.SchemaRegistryURL) == "" && kafka.SchemaID <= 0 {
				issues = append(issues, "config.kafka.serialization avro requires config.kafka.schema_registry_url or schema_id")
			}
			if strings.TrimSpace(kafka.Schema) == "" {
				issues = append(issues, "config.kafka.serialization avro requires config.kafka.schema (the Avro schema)")
			}
		default:
			issues = append(issues, fmt.Sprintf("config.kafka.serialization %q is not supported (raw, json_schema, or avro; protobuf in a later release)", kafka.Serialization))
		}
	}
	if kafka.TimeoutMs < 0 {
		issues = append(issues, "config.kafka.timeout_ms must be zero or greater")
	} else if time.Duration(kafka.TimeoutMs)*time.Millisecond > kafkaprotocol.MaxConfiguredTimeout {
		issues = append(issues, fmt.Sprintf("config.kafka.timeout_ms must not exceed %d", kafkaprotocol.MaxConfiguredTimeout/time.Millisecond))
	}
	if kafka.Partitions < 0 {
		issues = append(issues, "config.kafka.partitions must be zero or greater")
	} else if kafka.Partitions > kafkaprotocol.MaxConfiguredPartitions {
		issues = append(issues, fmt.Sprintf("config.kafka.partitions must not exceed %d", kafkaprotocol.MaxConfiguredPartitions))
	}
	if kafka.MaxMessageBytes < 0 {
		issues = append(issues, "config.kafka.max_message_bytes must be zero or greater")
	} else if kafka.MaxMessageBytes > kafkaprotocol.MaxConfiguredMessageBytes {
		issues = append(issues, fmt.Sprintf("config.kafka.max_message_bytes must not exceed %d", kafkaprotocol.MaxConfiguredMessageBytes))
	}
	if kafka.SchemaID < 0 || kafka.SchemaID > math.MaxInt32 {
		issues = append(issues, fmt.Sprintf("config.kafka.schema_id must be between 0 and %d", math.MaxInt32))
	}
	if len(kafka.Topic) > kafkaprotocol.MaxKafkaTopicBytes {
		issues = append(issues, fmt.Sprintf("config.kafka.topic must not exceed %d bytes", kafkaprotocol.MaxKafkaTopicBytes))
	}
	for name, value := range map[string]string{
		"client_id": kafka.ClientID, "consumer_group": kafka.ConsumerGroup,
		"transactional_id": kafka.TransactionalID, "oauth_scope": kafka.OAuthScope,
	} {
		if len(value) > kafkaprotocol.MaxKafkaIdentifierBytes {
			issues = append(issues, fmt.Sprintf("config.kafka.%s must not exceed %d bytes", name, kafkaprotocol.MaxKafkaIdentifierBytes))
		}
	}
	if len(kafka.Subject) > kafkaprotocol.MaxConfiguredSubjectBytes {
		issues = append(issues, fmt.Sprintf("config.kafka.subject must not exceed %d bytes", kafkaprotocol.MaxConfiguredSubjectBytes))
	}
	if len(kafka.Schema) > kafkaprotocol.MaxConfiguredSchemaBytes {
		issues = append(issues, fmt.Sprintf("config.kafka.schema must not exceed %d bytes", kafkaprotocol.MaxConfiguredSchemaBytes))
	}
	if len(kafka.SchemaRegistryURL) > kafkaprotocol.MaxConfiguredRegistryBytes {
		issues = append(issues, fmt.Sprintf("config.kafka.schema_registry_url must not exceed %d bytes", kafkaprotocol.MaxConfiguredRegistryBytes))
	}

	visitScriptActions(script, func(path string, action Action) {
		if actionIssues, ok := validateCatalogActionInScript("kafka", path, action, script); ok {
			issues = append(issues, actionIssues...)
		}
	})
	if !kafka.Mock {
		visitScriptActions(script, func(path string, action Action) {
			if strings.EqualFold(strings.TrimSpace(action.Type), "kafka_seek") {
				issues = append(issues, fmt.Sprintf("%s: kafka_seek is supported only with config.kafka.mock; the real Kafka adapter cannot reposition consumers", actionLabel(path, action)))
			}
		})
	}

	return issues
}

func validateKafkaCatalogActionIssues(path string, action Action, script *Script) []string {
	return validateKafkaActionSemanticIssues(path, action, script.Config.Kafka)
}

func validateKafkaActionSemanticIssues(path string, action Action, kafka KafkaConfig) []string {
	actionType := action.Type
	if isEngineActionType(actionType) {
		return nil
	}
	if _, ok := protocolActionSpec("kafka", actionType); !ok {
		// Common protocol validation owns missing/unsupported action-type issues.
		return nil
	}

	label := actionLabel(path, action)
	var issues []string
	actionTopic := actionParamString(action.Params, "topic")
	if actionTopic == "" {
		actionTopic = action.Target
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "topic", value: actionTopic},
		{name: "reply_to", value: actionParamString(action.Params, "reply_to")},
	} {
		if len(field.value) > kafkaprotocol.MaxKafkaTopicBytes {
			issues = append(issues, fmt.Sprintf("%s: %s must not exceed %d bytes", label, field.name, kafkaprotocol.MaxKafkaTopicBytes))
		}
	}
	if value := actionParamString(action.Params, "consumer_group"); len(value) > kafkaprotocol.MaxKafkaIdentifierBytes {
		issues = append(issues, fmt.Sprintf("%s: consumer_group must not exceed %d bytes", label, kafkaprotocol.MaxKafkaIdentifierBytes))
	}
	if value, present := actionParamIntValue(action.Params, "timeout_ms"); present && (value < 0 || value > int(kafkaprotocol.MaxConfiguredTimeout/time.Millisecond)) {
		issues = append(issues, fmt.Sprintf("%s: timeout_ms must be between 0 and %d", label, kafkaprotocol.MaxConfiguredTimeout/time.Millisecond))
	}
	if value, present := actionParamIntValue(action.Params, "partition"); present && (value < 0 || int64(value) > math.MaxInt32) {
		issues = append(issues, fmt.Sprintf("%s: partition must be between 0 and %d", label, math.MaxInt32))
	}
	if actionType == "kafka_produce" || actionType == "kafka_consume" || actionType == "kafka_request_reply" {
		if strings.TrimSpace(actionTopic) == "" && strings.TrimSpace(kafka.Topic) == "" {
			issues = append(issues, fmt.Sprintf("%s: missing topic", label))
		}
	}
	if actionType == "kafka_request_reply" {
		replyTo, _ := action.Params["reply_to"].(string)
		if strings.TrimSpace(replyTo) == "" {
			issues = append(issues, fmt.Sprintf("%s: missing reply_to topic", label))
		}
	}
	// EOS transaction verbs require config.kafka.transactional_id (the runtime
	// rejects kafka_begin_transaction without it). Gate on begin — the verb the
	// runtime fails first — so the contract is enforced at pre-flight, not run time.
	if actionType == "kafka_begin_transaction" && strings.TrimSpace(kafka.TransactionalID) == "" {
		issues = append(issues, fmt.Sprintf("%s: kafka_begin_transaction requires config.kafka.transactional_id", label))
	}
	return issues
}

package protocol

import (
	"encoding/json"
	"strings"
	"time"
)

func BuildProtocolEvidence(action *Action, result *Result) map[string]any {
	if result == nil {
		return nil
	}
	protocolName := inferEvidenceProtocol(action, result)
	if protocolName == "" {
		return nil
	}
	evidence := map[string]any{
		"protocol":  protocolName,
		"operation": evidenceOperation(action, result),
		"target":    evidenceTarget(action, result),
		"status":    evidenceStatus(result),
	}
	if timings := evidenceTimings(result); len(timings) > 0 {
		evidence["timings"] = timings
	}
	if messages := evidenceMessages(result); len(messages) > 0 {
		evidence["messages"] = messages
	}
	if metadata := evidenceMetadata(action, result); len(metadata) > 0 {
		evidence["metadata"] = metadata
	}
	payload := parseEvidencePayload(result.ResponseBody)
	if rows := evidenceRows(payload); len(rows) > 0 {
		evidence["rows"] = rows
	}
	if records := evidenceRecords(payload); len(records) > 0 {
		evidence["records"] = records
	}
	if transcript := evidenceTranscript(payload, result.ResponseBody); len(transcript) > 0 {
		evidence["transcript"] = transcript
	}
	return evidence
}

func inferEvidenceProtocol(action *Action, result *Result) string {
	dependency := strings.ToLower(strings.TrimSpace(result.DependencyType))
	actionType := ""
	if action != nil {
		actionType = strings.ToLower(strings.TrimSpace(action.Type))
	}
	switch {
	case strings.HasPrefix(dependency, "websocket") || strings.HasPrefix(actionType, "ws_"):
		return "websocket"
	case strings.HasPrefix(dependency, "grpc") || strings.HasPrefix(actionType, "grpc_"):
		return "grpc"
	case strings.HasPrefix(dependency, "database") || strings.HasPrefix(actionType, "db_") || strings.HasPrefix(actionType, "database_"):
		return "database"
	case strings.HasPrefix(dependency, "network_") || strings.HasPrefix(dependency, "network") || strings.HasPrefix(actionType, "dns_") || strings.HasPrefix(actionType, "tcp_") || strings.HasPrefix(actionType, "udp_") || strings.HasPrefix(actionType, "ftp_") || strings.HasPrefix(actionType, "smtp_") || strings.HasPrefix(actionType, "pop3_") || strings.HasPrefix(actionType, "imap_") || strings.HasPrefix(actionType, "ldap_") || strings.HasPrefix(actionType, "winsock_"):
		return "network"
	case strings.HasPrefix(dependency, "jms") || strings.HasPrefix(actionType, "jms_"):
		return "jms"
	case strings.HasPrefix(dependency, "kafka") || strings.HasPrefix(actionType, "kafka_"):
		return "kafka"
	default:
		return ""
	}
}

func evidenceOperation(action *Action, result *Result) string {
	if action != nil && strings.TrimSpace(action.Type) != "" {
		return strings.TrimSpace(action.Type)
	}
	return strings.TrimSpace(result.ActionName)
}

func evidenceTarget(action *Action, result *Result) string {
	if result.DependencyTarget != "" {
		return result.DependencyTarget
	}
	if action != nil {
		return strings.TrimSpace(action.Target)
	}
	return ""
}

func evidenceStatus(result *Result) string {
	if result.Success {
		return "passed"
	}
	if result.ErrorMessage != "" {
		return "failed"
	}
	return "unknown"
}

func evidenceTimings(result *Result) map[string]any {
	timings := make(map[string]any)
	addDuration := func(key string, value time.Duration) {
		if value > 0 {
			timings[key] = float64(value.Microseconds()) / 1000
		}
	}
	addDuration("duration_ms", result.Duration)
	addDuration("dns_ms", result.DNSTime)
	addDuration("connect_ms", result.ConnectTime)
	addDuration("tls_ms", result.TLSTime)
	addDuration("ttfb_ms", result.TTFB)
	return timings
}

func evidenceMessages(result *Result) map[string]any {
	messages := make(map[string]any)
	if result.BytesSent > 0 {
		messages["bytes_sent"] = result.BytesSent
	}
	if result.BytesReceived > 0 {
		messages["bytes_received"] = result.BytesReceived
	}
	if result.ServerTelemetry != nil {
		if result.ServerTelemetry.MessagesSent > 0 {
			messages["sent"] = result.ServerTelemetry.MessagesSent
		}
		if result.ServerTelemetry.MessagesReceived > 0 {
			messages["received"] = result.ServerTelemetry.MessagesReceived
		}
		if result.ServerTelemetry.ConsumerLagMs > 0 {
			messages["consumer_lag_ms"] = result.ServerTelemetry.ConsumerLagMs
		}
		if result.ServerTelemetry.ConsumerOffsetLag > 0 {
			messages["consumer_offset_lag"] = result.ServerTelemetry.ConsumerOffsetLag
		}
		if result.ServerTelemetry.MessageLatencyMs > 0 {
			messages["latency_ms"] = result.ServerTelemetry.MessageLatencyMs
		}
		if result.ServerTelemetry.EndToEndLatencyMs > 0 {
			messages["end_to_end_latency_ms"] = result.ServerTelemetry.EndToEndLatencyMs
		}
		if result.ServerTelemetry.PartitionID != "" {
			messages["partition_id"] = result.ServerTelemetry.PartitionID
		}
		if result.ServerTelemetry.ThrottledRequests > 0 {
			messages["throttled_requests"] = result.ServerTelemetry.ThrottledRequests
		}
		if result.ServerTelemetry.ServerBusyCount > 0 {
			messages["server_busy_count"] = result.ServerTelemetry.ServerBusyCount
		}
		if result.ServerTelemetry.QuotaExceededCount > 0 {
			messages["quota_exceeded_count"] = result.ServerTelemetry.QuotaExceededCount
		}
		if result.ServerTelemetry.EventTooLargeCount > 0 {
			messages["event_too_large_count"] = result.ServerTelemetry.EventTooLargeCount
		}
	}
	return messages
}

func evidenceMetadata(action *Action, result *Result) map[string]any {
	metadata := make(map[string]any)
	if result.CorrelationID != "" {
		metadata["correlation_id"] = result.CorrelationID
	}
	if result.DependencyType != "" {
		metadata["dependency_type"] = result.DependencyType
	}
	if result.StatusCode != 0 {
		metadata["status_code"] = result.StatusCode
	}
	if result.ErrorMessage != "" {
		metadata["error"] = result.ErrorMessage
	}
	if action != nil {
		if len(action.Headers) > 0 {
			metadata["headers"] = cloneEvidenceStringMap(action.Headers)
		}
		if len(action.Params) > 0 {
			metadata["params"] = cloneEvidenceAnyMap(action.Params)
		}
	}
	if len(result.Extractions) > 0 {
		metadata["extractions"] = cloneEvidenceStringMap(result.Extractions)
	}
	return metadata
}

func parseEvidencePayload(body string) map[string]any {
	if strings.TrimSpace(body) == "" {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return nil
	}
	return payload
}

func evidenceRows(payload map[string]any) map[string]any {
	if len(payload) == 0 {
		return nil
	}
	rows := make(map[string]any)
	for _, key := range []string{"columns", "row_count", "rows", "first_row", "scalar", "rows_affected", "last_insert_id", "transaction", "status"} {
		if value, ok := payload[key]; ok {
			rows[key] = value
		}
	}
	return rows
}

func evidenceRecords(payload map[string]any) map[string]any {
	if len(payload) == 0 {
		return nil
	}
	records := make(map[string]any)
	for _, key := range []string{"record_type", "record_count", "records", "entry_count", "entries", "protocol", "network", "target", "base_dn", "filter"} {
		if value, ok := payload[key]; ok {
			records[key] = value
		}
	}
	return records
}

func evidenceTranscript(payload map[string]any, body string) []any {
	if len(payload) > 0 {
		if responses, ok := payload["responses"].([]any); ok {
			return responses
		}
		if response, ok := payload["response"].(string); ok && response != "" {
			return []any{response}
		}
		if message, ok := payload["message"].(string); ok && message != "" {
			return []any{message}
		}
	}
	if trimmed := strings.TrimSpace(body); trimmed != "" && len(trimmed) < 4096 {
		return []any{trimmed}
	}
	return nil
}

func cloneEvidenceStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func cloneEvidenceAnyMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

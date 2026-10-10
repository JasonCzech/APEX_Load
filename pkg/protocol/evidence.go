package protocol

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
)

const (
	maxEvidencePayloadBytes = 64 << 10
	maxEvidenceBytes        = 128 << 10
	maxEvidenceStringBytes  = 8 << 10
	maxEvidenceEntries      = 1024
	maxEvidenceCollection   = 128
	maxEvidenceDepth        = 6
)

func BuildProtocolEvidence(action *Action, result *Result) map[string]any {
	if result == nil {
		return nil
	}
	protocolName := inferEvidenceProtocol(action, result)
	if protocolName == "" {
		if len(result.WarningCodes) > 0 {
			return BoundProtocolEvidence(map[string]any{"warnings": result.WarningCodes})
		}
		return nil
	}
	evidence := map[string]any{
		"protocol":  protocolName,
		"operation": evidenceOperation(action, result),
		"target":    evidenceTarget(action, result),
		"status":    evidenceStatus(result),
	}
	if result.HTTPVersionPolicy != "" {
		evidence["http_version_policy"] = result.HTTPVersionPolicy
	}
	if result.HTTPVersionNegotiated != "" {
		evidence["http_version_negotiated"] = result.HTTPVersionNegotiated
	}
	if result.HTTPVersionSource != "" {
		evidence["http_version_source"] = result.HTTPVersionSource
	}
	if result.HTTPAltSvcH3Advertised {
		evidence["alt_svc_h3_advertised"] = true
	}
	if result.HTTP3FailureCertainty != "" {
		evidence["http3_failure_certainty"] = result.HTTP3FailureCertainty
	}
	copyResponseContentReceipt(evidence, result.ProtocolEvidence)
	if len(result.WarningCodes) > 0 {
		evidence["warnings"] = result.WarningCodes
	}
	if result.ContentCheckErrorMatches+result.ContentCheckInfoMatches > 0 {
		evidence["content_check_matches"] = map[string]any{"errors": result.ContentCheckErrorMatches, "information": result.ContentCheckInfoMatches}
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
	return BoundProtocolEvidence(evidence)
}

func inferEvidenceProtocol(action *Action, result *Result) string {
	dependency := strings.ToLower(strings.TrimSpace(result.DependencyType))
	actionType := ""
	if action != nil {
		actionType = strings.ToLower(strings.TrimSpace(action.Type))
	}
	switch {
	case strings.HasPrefix(dependency, "soap") || (action != nil && action.SOAP != nil):
		return "soap"
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
	case strings.HasPrefix(dependency, "eventhubs") || strings.HasPrefix(actionType, "eventhubs_"):
		return "eventhubs"
	case strings.HasPrefix(dependency, "kafka") || strings.HasPrefix(actionType, "kafka_"):
		return "kafka"
	case strings.HasPrefix(dependency, "java_vuser") || strings.HasPrefix(actionType, "java_vuser_"):
		return "java_vuser"
	default:
		return ""
	}
}

func evidenceOperation(action *Action, result *Result) string {
	if action != nil && action.SOAP != nil {
		if operation := strings.TrimSpace(action.SOAP.Operation); operation != "" {
			return operation
		}
		if soapAction := strings.TrimSpace(action.SOAP.Action); soapAction != "" {
			return soapAction
		}
	}
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
		if strings.TrimSpace(action.RedactedTarget) != "" {
			return strings.TrimSpace(action.RedactedTarget)
		}
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
	addDuration("worker_duration_ms", result.WorkerDuration)
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
		if action.SOAP != nil || action.RedactedSOAP != nil {
			soapCfg := action.SOAP
			if action.RedactedSOAP != nil {
				soapCfg = action.RedactedSOAP
			}
			soapMetadata := make(map[string]any)
			if soapCfg != nil {
				if soapCfg.Action != "" {
					soapMetadata["action"] = soapCfg.Action
				}
				if soapCfg.Operation != "" {
					soapMetadata["operation"] = soapCfg.Operation
				}
				if soapCfg.BodyMode != "" {
					soapMetadata["body_mode"] = soapCfg.BodyMode
				}
			}
			if len(soapMetadata) > 0 {
				metadata["soap"] = soapMetadata
			}
		}
		if len(action.Headers) > 0 {
			headers := action.Headers
			if len(action.RedactedHeaders) > 0 {
				headers = action.RedactedHeaders
			}
			metadata["headers"] = cloneEvidenceStringMap(headers)
		}
		if len(action.Params) > 0 {
			params := action.Params
			if len(action.RedactedParams) > 0 {
				params = action.RedactedParams
			}
			metadata["params"] = cloneEvidenceAnyMap(params)
		}
	}
	if len(result.Extractions) > 0 {
		metadata["extractions"] = cloneEvidenceStringMap(result.Extractions)
	}
	return metadata
}

func parseEvidencePayload(body string) map[string]any {
	if len(body) > maxEvidencePayloadBytes || strings.TrimSpace(body) == "" {
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
	bounded := BoundProtocolEvidence(map[string]any{"values": input})
	values, _ := bounded["values"].(map[string]string)
	return values
}

func cloneEvidenceAnyMap(input map[string]any) map[string]any {
	return BoundProtocolEvidence(input)
}

// BoundProtocolEvidence returns a detached, recursively bounded projection of
// protocol evidence. Arbitrary adapter/action maps are user-controlled and may
// otherwise retain very large payload graphs in replay reports and archives.
func BoundProtocolEvidence(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	budget := evidenceBudget{bytes: maxEvidenceBytes, entries: maxEvidenceEntries}
	value, ok := boundEvidenceValue(input, &budget, 0)
	if !ok {
		return nil
	}
	out, _ := value.(map[string]any)
	return out
}

type evidenceBudget struct {
	bytes   int
	entries int
}

func boundEvidenceValue(value any, budget *evidenceBudget, depth int) (any, bool) {
	if budget == nil || budget.bytes <= 0 || budget.entries <= 0 || depth > maxEvidenceDepth {
		return nil, false
	}
	switch typed := value.(type) {
	case nil, bool, float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, json.Number:
		budget.entries--
		return typed, true
	case string:
		limit := min(maxEvidenceStringBytes, budget.bytes)
		if limit <= 0 {
			return nil, false
		}
		typed = boundedtext.TruncateUTF8(typed, limit)
		budget.bytes -= len(typed)
		budget.entries--
		return typed, true
	case map[string]string:
		keys := boundedtext.SmallestStringKeys(typed, maxEvidenceCollection)
		out := make(map[string]string, len(keys))
		for _, key := range keys {
			boundedKey, ok := boundEvidenceValue(key, budget, depth+1)
			if !ok {
				break
			}
			boundedValue, ok := boundEvidenceValue(typed[key], budget, depth+1)
			if !ok {
				continue
			}
			out[boundedKey.(string)] = boundedValue.(string)
		}
		return out, len(out) > 0
	case map[string]any:
		keys := boundedtext.SmallestStringKeys(typed, maxEvidenceCollection)
		out := make(map[string]any, len(keys))
		for _, key := range keys {
			if budget.entries <= 0 || budget.bytes <= 0 {
				break
			}
			boundedKey, ok := boundEvidenceValue(key, budget, depth+1)
			if !ok {
				break
			}
			boundedValue, ok := boundEvidenceValue(typed[key], budget, depth+1)
			if !ok {
				continue
			}
			out[boundedKey.(string)] = boundedValue
		}
		return out, len(out) > 0
	case []string:
		values := make([]any, min(len(typed), maxEvidenceCollection))
		for index := range values {
			values[index] = typed[index]
		}
		return boundEvidenceValue(values, budget, depth)
	case []any:
		limit := min(len(typed), maxEvidenceCollection)
		out := make([]any, 0, limit)
		for _, item := range typed[:limit] {
			bounded, ok := boundEvidenceValue(item, budget, depth+1)
			if !ok {
				continue
			}
			out = append(out, bounded)
		}
		return out, len(out) > 0
	default:
		return nil, false
	}
}

// Only closed response-content enums cross wrapper protocols. Arbitrary native
// evidence must not create a new generic payload projection or disclosure path.
func copyResponseContentReceipt(destination, source map[string]any) {
	if state, ok := source["response_content_state"].(string); ok {
		switch state {
		case "complete", "collection_window", "unavailable":
			destination["response_content_state"] = state
		}
	}
	if origin, ok := source["response_content_source"].(string); ok {
		switch origin {
		case "wire", "cache":
			destination["response_content_source"] = origin
		}
	}
	if basis, ok := source["response_content_basis"].(string); ok && destination["response_content_state"] != "unavailable" {
		switch basis {
		case "decoded", "identity":
			destination["response_content_basis"] = basis
		}
	}
}

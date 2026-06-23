package engine

import (
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func attachReplayDiagnosis(event *ReplayActionEvent) {
	if event == nil || event.Success {
		return
	}
	diagnosis := classifyReplayFailure(*event)
	event.Diagnosis = &diagnosis
	event.RepairSuggestions = replayRepairSuggestionsForDiagnosis(*event, diagnosis)
}

func attachSelfHealSuggestion(event *ReplayActionEvent) {
	if event == nil || !event.Success || len(event.ProtocolEvidence) == 0 {
		return
	}
	evidence, ok := event.ProtocolEvidence["selector_self_heal"].(map[string]any)
	if !ok || len(evidence) == 0 {
		return
	}
	healed := selfHealEvidenceString(evidence, "healed_selector")
	if healed == "" {
		return
	}
	previous := selfHealEvidenceString(evidence, "primary_selector")
	recorderSeq := selfHealEvidenceString(evidence, "recorder_step_seq")
	if recorderSeq == "" && event.SourceStepIndex > 0 {
		recorderSeq = fmt.Sprint(event.SourceStepIndex)
	}
	confidence := selfHealConfidence(evidence["confidence"])
	baseID := event.EventID
	if baseID == "" {
		baseID = fmt.Sprintf("%s-%d", event.Phase, event.EventIndex)
	}
	fields := map[string]string{
		"selector":          healed,
		"previous_selector": previous,
	}
	if recorderSeq != "" {
		fields["recorder_step_seq"] = recorderSeq
	}
	event.RepairSuggestions = append(event.RepairSuggestions, ReplayRepairSuggestion{
		ID:             baseID + ":promote-selector",
		Title:          "Promote self-healed selector",
		Description:    "Replay succeeded by using a fallback selector. Promote that selector so the next replay starts with the working locator.",
		DraftPatchType: "promote_selector",
		Action:         event.ActionName,
		Confidence:     confidence,
		Fields:         fields,
	})
}

func selfHealEvidenceString(evidence map[string]any, key string) string {
	value, ok := evidence[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func selfHealConfidence(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case string:
		var parsed float64
		if _, err := fmt.Sscanf(typed, "%f", &parsed); err == nil {
			return parsed
		}
	}
	return 0.8
}

func classifyReplayFailure(event ReplayActionEvent) ReplayDiagnosis {
	errorText := strings.TrimSpace(event.ErrorMessage)
	statusText := ""
	if event.StatusCode != 0 {
		statusText = fmt.Sprintf("HTTP status %d", event.StatusCode)
	}
	cause := errorText
	if cause == "" {
		cause = statusText
	}
	if cause == "" {
		cause = "Replay action failed without a protocol error message"
	}

	lower := strings.ToLower(strings.Join([]string{
		event.ActionType,
		event.Method,
		event.Target,
		event.Selector,
		event.Body,
		errorText,
		statusText,
	}, " "))
	isBrowserAction := strings.HasPrefix(strings.ToLower(event.ActionType), "browser_") || event.Selector != "" || event.PageURL != "" || event.HTML != ""

	category := "runtime_environment"
	signals := make([]string, 0, 3)
	addSignal := func(signal string) {
		if signal != "" {
			signals = append(signals, signal)
		}
	}

	switch {
	case isBrowserAction && containsAny(lower, "selector", "locator", "strict mode", "no element", "not found", "not visible", "not enabled", "detached", "element"):
		category = "selector"
		addSignal("browser_selector")
	case isBrowserAction && containsAny(lower, "timeout", "timed out", "wait", "navigation"):
		category = "wait_timing"
		addSignal("browser_wait")
	case strings.HasPrefix(strings.ToLower(event.ActionType), "ws_") && containsAny(lower, "handshake", "bad status", "upgrade"):
		category = "websocket_handshake"
		addSignal("websocket_handshake")
	case strings.HasPrefix(strings.ToLower(event.ActionType), "ws_") && containsAny(lower, "timeout", "deadline", "message does not contain"):
		category = "websocket_receive_timeout"
		addSignal("websocket_message")
	case strings.HasPrefix(strings.ToLower(event.ActionType), "grpc_") && containsAny(lower, "reflection", "proto", "descriptor", "method", "service"):
		category = "grpc_contract"
		addSignal("grpc_contract")
	case strings.HasPrefix(strings.ToLower(event.ActionType), "grpc_") && containsAny(lower, "code =", "status", "rpc error", "metadata", "authority", "deadline"):
		category = "grpc_status"
		addSignal("grpc_status")
	case strings.HasPrefix(strings.ToLower(event.ActionType), "db_") || strings.HasPrefix(strings.ToLower(event.ActionType), "database_"):
		switch {
		case containsAny(lower, "dsn", "driver", "connect", "ping", "secret"):
			category = "database_connection"
			addSignal("database_connection")
		case containsAny(lower, "read_only", "read only", "mutation"):
			category = "database_guard"
			addSignal("database_guard")
		case containsAny(lower, "syntax", "sql", "query", "exec", "constraint"):
			category = "database_sql"
			addSignal("database_sql")
		default:
			category = "database_runtime"
			addSignal("database")
		}
	case strings.HasPrefix(strings.ToLower(event.ActionType), "dns_") || strings.HasPrefix(strings.ToLower(event.ActionType), "tcp_") || strings.HasPrefix(strings.ToLower(event.ActionType), "udp_") || strings.HasPrefix(strings.ToLower(event.ActionType), "ftp_") || strings.HasPrefix(strings.ToLower(event.ActionType), "smtp_") || strings.HasPrefix(strings.ToLower(event.ActionType), "pop3_") || strings.HasPrefix(strings.ToLower(event.ActionType), "imap_") || strings.HasPrefix(strings.ToLower(event.ActionType), "ldap_") || strings.HasPrefix(strings.ToLower(event.ActionType), "winsock_"):
		switch {
		case containsAny(lower, "dns", "no such host", "lookup"):
			category = "network_dns_resolution"
			addSignal("dns")
		case containsAny(lower, "tls", "certificate", "auth", "login", "bind"):
			category = "network_auth_tls"
			addSignal("network_auth_tls")
		case containsAny(lower, "timeout", "deadline", "connection refused", "reset by peer"):
			category = "network_connection"
			addSignal("network_connection")
		default:
			category = "network_protocol"
			addSignal("network_protocol")
		}
	case strings.HasPrefix(strings.ToLower(event.ActionType), "jms_") && containsAny(lower, "broker", "destination", "message received", "timeout", "dead_letter", "correlation", "ack"):
		category = "jms_broker_message"
		addSignal("jms")
	case containsAny(lower, "assert", "expected", "mismatch", "check failed"):
		category = "assertion"
		addSignal("assertion")
	case containsAny(lower, "{{var.", "{{ param.", "{{param.", "extractor", "extract ", "jsonpath", "regexp", "regex", "correlat"):
		category = "correlation"
		addSignal("correlation")
	case containsAny(lower, "{{data.", "{{ data.", "{{param.", "data source", "parameter", "missing data", "no row"):
		category = "data"
		addSignal("data_binding")
	case event.StatusCode == 401 || event.StatusCode == 403 || containsAny(lower, "unauthorized", "forbidden", "csrf", "cookie", "session expired", "login", "auth"):
		category = "auth_session"
		addSignal("auth_session")
	case event.StatusCode >= 500 || containsAny(lower, "connection refused", "no such host", "dns", "tls", "certificate", "network", "reset by peer", "unreachable"):
		category = "network"
		addSignal("network")
	case event.StatusCode >= 300 && event.StatusCode < 400 || containsAny(lower, "redirect", "too many redirects"):
		category = "protocol"
		addSignal("redirect")
	case containsAny(lower, "unsupported", "protocol", "codec", "marshal", "unmarshal"):
		category = "protocol"
		addSignal("protocol")
	default:
		addSignal("runtime")
	}

	evidence := replayDiagnosisEvidence(event, category)
	action := replayDiagnosisAction(category)
	diagnosis := ReplayDiagnosis{
		Category: category,
		Severity: "error",
		Cause:    cause,
		Evidence: evidence,
		Action:   action,
		Signals:  signals,
	}
	if event.DOMSummary != nil && len(event.DOMSummary.VisibleText) > 0 {
		diagnosis.FrameContext = strings.Join(event.DOMSummary.VisibleText[:minInt(len(event.DOMSummary.VisibleText), 3)], " | ")
	}
	if category == "selector" && event.Selector != "" {
		diagnosis.SuggestedLocator = event.Selector
	}
	return diagnosis
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if needle != "" && strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func inferReplayDependencyType(action *protocol.Action) string {
	if action == nil {
		return ""
	}
	actionType := strings.ToLower(strings.TrimSpace(action.Type))
	switch {
	case strings.HasPrefix(actionType, "ws_"):
		return "websocket"
	case strings.HasPrefix(actionType, "grpc_"):
		return "grpc"
	case strings.HasPrefix(actionType, "db_") || strings.HasPrefix(actionType, "database_"):
		return "database"
	case strings.HasPrefix(actionType, "dns_"), strings.HasPrefix(actionType, "tcp_"), strings.HasPrefix(actionType, "udp_"), strings.HasPrefix(actionType, "ftp_"), strings.HasPrefix(actionType, "smtp_"), strings.HasPrefix(actionType, "pop3_"), strings.HasPrefix(actionType, "imap_"), strings.HasPrefix(actionType, "ldap_"), strings.HasPrefix(actionType, "winsock_"):
		return "network"
	case strings.HasPrefix(actionType, "jms_"):
		return "jms_queue"
	default:
		return ""
	}
}

func replayDiagnosisEvidence(event ReplayActionEvent, category string) string {
	switch category {
	case "selector":
		if event.Selector != "" {
			return fmt.Sprintf("Selector %q failed for %s", event.Selector, nonEmptyString(event.PageURL, event.Target))
		}
	case "wait_timing":
		return "The browser action did not reach the expected page or element state before timeout."
	case "correlation":
		return "The action references dynamic values or extractors that did not resolve cleanly during replay."
	case "data":
		return "The action depends on parameter or data-source values that were not available for this replay."
	case "auth_session":
		return "The target returned an authentication/session signal during replay."
	case "network":
		return "The protocol reported a transport or server-side availability failure."
	case "protocol":
		return "The protocol/runtime could not complete the action using the current script shape."
	case "websocket_handshake":
		return "The WebSocket connection did not complete the HTTP upgrade/handshake."
	case "websocket_receive_timeout":
		return "The WebSocket receive step timed out or received a message that did not match expectations."
	case "grpc_contract":
		return "The gRPC method, reflection data, or proto contract could not be resolved."
	case "grpc_status":
		return "The gRPC call returned a non-OK status or metadata/timeout failure."
	case "database_connection":
		return "The database adapter could not establish or validate the configured connection."
	case "database_sql":
		return "The SQL action failed during query, exec, constraint, or syntax processing."
	case "database_guard":
		return "A database safety guard blocked a mutating operation."
	case "database_runtime":
		return "The database action failed before producing approved result evidence."
	case "network_dns_resolution":
		return "The network adapter could not resolve the requested DNS target."
	case "network_connection":
		return "The network adapter could not establish or sustain the requested connection."
	case "network_auth_tls":
		return "The network adapter reported TLS, bind, login, or protocol authentication failure."
	case "network_protocol":
		return "The network protocol command failed or returned an unexpected response."
	case "jms_broker_message":
		return "The JMS adapter reported broker, destination, correlation, acknowledgement, timeout, or dead-letter evidence."
	}
	if event.StatusCode != 0 {
		return fmt.Sprintf("Protocol status: %d", event.StatusCode)
	}
	return ""
}

func replayDiagnosisAction(category string) string {
	switch category {
	case "selector":
		return "Review selector evidence, prefer stable role/text/test-id locators, and add an explicit wait if the element is rendered late."
	case "wait_timing":
		return "Add or tune a wait around the page state that proves the next action is safe."
	case "assertion":
		return "Compare expected and actual values, then draft a narrower assertion or update the expected value."
	case "correlation":
		return "Add or repair an extractor before the first unresolved use of the value."
	case "data":
		return "Bind the missing value to a data source row, parameter, or replay variable seed."
	case "auth_session":
		return "Refresh login/session setup, secret bindings, cookies, or CSRF extraction before rerun."
	case "network":
		return "Confirm target reachability, TLS, DNS, redirects, and environment-specific network controls."
	case "protocol":
		return "Inspect the protocol adapter and action fields for unsupported or malformed runtime input."
	case "websocket_handshake":
		return "Check endpoint URL, TLS, headers, origin, and subprotocol before rerunning the WebSocket connect step."
	case "websocket_receive_timeout":
		return "Tune receive timeout or match criteria, then verify the upstream event is published before this step waits."
	case "grpc_contract":
		return "Attach the correct proto files or enable reflection, then confirm the service/method path."
	case "grpc_status":
		return "Inspect gRPC status, metadata, authority, TLS/plaintext mode, and deadline settings."
	case "database_connection":
		return "Verify driver, DSN secret binding, network route, and fixture readiness before replay."
	case "database_sql":
		return "Review SQL text, bind values, fixture data, and row/affected-count assertions."
	case "database_guard":
		return "Move write-capable checks to an approved fixture or disable read-only mode only after review."
	case "database_runtime":
		return "Inspect query output, transaction state, and database evidence before drafting a repair."
	case "network_dns_resolution":
		return "Confirm DNS record type, resolver access, host spelling, and test-network policy."
	case "network_connection":
		return "Check endpoint, port, TLS mode, timeout, and local protocol fixture availability."
	case "network_auth_tls":
		return "Review TLS certificates, bind/login credentials, and protocol-specific authentication commands."
	case "network_protocol":
		return "Inspect the protocol transcript and adjust command, payload, or expected response."
	case "jms_broker_message":
		return "Verify broker settings, destination, reply-to, correlation ID, ack mode, and dead-letter routing."
	default:
		return "Inspect the raw log and action snapshot, then rerun with debug trace capture."
	}
}

func replayRepairSuggestionsForDiagnosis(event ReplayActionEvent, diagnosis ReplayDiagnosis) []ReplayRepairSuggestion {
	baseID := event.EventID
	if baseID == "" {
		baseID = fmt.Sprintf("%s-%d", event.Phase, event.EventIndex)
	}
	switch diagnosis.Category {
	case "selector":
		return []ReplayRepairSuggestion{
			{
				ID:             baseID + ":update-selector",
				Title:          "Draft stronger selector",
				Description:    "Create a draft edit that replaces the brittle selector with a stable locator from DOM evidence.",
				DraftPatchType: "update_selector",
				Action:         event.ActionName,
				Confidence:     0.72,
				Fields:         map[string]string{"selector": event.Selector, "target": event.Target},
			},
			{
				ID:             baseID + ":add-wait",
				Title:          "Add readiness wait",
				Description:    "Insert an explicit wait before this browser action so replay waits for the required element state.",
				DraftPatchType: "add_wait",
				Action:         event.ActionName,
				Confidence:     0.64,
				Fields:         map[string]string{"selector": event.Selector},
			},
		}
	case "wait_timing":
		return []ReplayRepairSuggestion{{
			ID:             baseID + ":tune-wait",
			Title:          "Tune wait condition",
			Description:    "Draft a wait condition that anchors on the page or element state seen immediately before the failure.",
			DraftPatchType: "add_wait",
			Action:         event.ActionName,
			Confidence:     0.68,
			Fields:         map[string]string{"selector": event.Selector, "target": event.Target},
		}}
	case "assertion":
		return []ReplayRepairSuggestion{{
			ID:             baseID + ":adjust-assertion",
			Title:          "Review assertion",
			Description:    "Draft a focused assertion update using actual replay response evidence.",
			DraftPatchType: "adjust_assertion",
			Action:         event.ActionName,
			Confidence:     0.62,
		}}
	case "correlation":
		return []ReplayRepairSuggestion{{
			ID:             baseID + ":add-extractor",
			Title:          "Add missing extractor",
			Description:    "Draft an extractor before the failed action so dynamic values are captured instead of hard-coded.",
			DraftPatchType: "add_extractor",
			Action:         event.ActionName,
			Confidence:     0.70,
			Fields:         map[string]string{"target": event.Target},
		}}
	case "data":
		return []ReplayRepairSuggestion{{
			ID:             baseID + ":mark-data",
			Title:          "Mark data requirement",
			Description:    "Draft a data binding requirement for values this replay could not seed.",
			DraftPatchType: "mark_data_requirement",
			Action:         event.ActionName,
			Confidence:     0.66,
		}}
	case "auth_session":
		return []ReplayRepairSuggestion{{
			ID:             baseID + ":repair-session",
			Title:          "Refresh session setup",
			Description:    "Draft a setup change for login, cookies, CSRF extraction, or required auth secrets.",
			DraftPatchType: "repair_auth_session",
			Action:         event.ActionName,
			Confidence:     0.58,
		}}
	case "websocket_handshake", "websocket_receive_timeout":
		return []ReplayRepairSuggestion{{
			ID:             baseID + ":tune-websocket",
			Title:          "Tune WebSocket evidence",
			Description:    "Draft a WebSocket repair for URL, headers, subprotocol, receive timeout, or message match criteria.",
			DraftPatchType: "repair_websocket",
			Action:         event.ActionName,
			Confidence:     0.61,
			Fields:         map[string]string{"target": event.Target},
		}}
	case "grpc_contract", "grpc_status":
		return []ReplayRepairSuggestion{{
			ID:             baseID + ":repair-grpc",
			Title:          "Repair gRPC call shape",
			Description:    "Draft a gRPC edit for proto/reflection, method target, metadata, TLS/plaintext mode, or deadline settings.",
			DraftPatchType: "repair_grpc",
			Action:         event.ActionName,
			Confidence:     0.60,
			Fields:         map[string]string{"target": event.Target},
		}}
	case "database_connection", "database_sql", "database_guard", "database_runtime":
		return []ReplayRepairSuggestion{{
			ID:             baseID + ":repair-database",
			Title:          "Repair database action",
			Description:    "Draft a database edit for DSN readiness, bind values, SQL text, read-only policy, or row assertions.",
			DraftPatchType: "repair_database",
			Action:         event.ActionName,
			Confidence:     0.63,
			Fields:         map[string]string{"target": event.Target},
		}}
	case "network_dns_resolution", "network_connection", "network_auth_tls", "network_protocol":
		return []ReplayRepairSuggestion{{
			ID:             baseID + ":repair-network",
			Title:          "Repair network protocol step",
			Description:    "Draft a network edit for endpoint, port, TLS/auth, protocol command, timeout, or response assertion.",
			DraftPatchType: "repair_network",
			Action:         event.ActionName,
			Confidence:     0.59,
			Fields:         map[string]string{"target": event.Target},
		}}
	case "jms_broker_message":
		return []ReplayRepairSuggestion{{
			ID:             baseID + ":repair-jms",
			Title:          "Repair JMS message flow",
			Description:    "Draft a JMS edit for broker settings, destination, reply-to, correlation ID, timeout, acknowledgement, or dead-letter routing.",
			DraftPatchType: "repair_jms",
			Action:         event.ActionName,
			Confidence:     0.60,
			Fields:         map[string]string{"target": event.Target},
		}}
	default:
		return []ReplayRepairSuggestion{{
			ID:             baseID + ":diagnostic-note",
			Title:          "Create diagnostic note",
			Description:    "Attach this failure evidence to a draft repair note for analyst review.",
			DraftPatchType: "diagnostic_note",
			Action:         event.ActionName,
			Confidence:     0.50,
		}}
	}
}

func buildReplayDiagnosisSummary(events []ReplayActionEvent) *ReplayDiagnosisSummary {
	summary := &ReplayDiagnosisSummary{Categories: map[string]int{}}
	for _, event := range events {
		if event.Success {
			continue
		}
		summary.FailureCount++
		category := "unknown"
		cause := strings.TrimSpace(event.ErrorMessage)
		if event.Diagnosis != nil {
			category = event.Diagnosis.Category
			cause = event.Diagnosis.Cause
		}
		summary.Categories[category]++
		if summary.FirstFailureEventID == "" {
			summary.FirstFailureEventID = event.EventID
			summary.FirstFailureAction = event.ActionName
			summary.FirstFailureCause = cause
		}
	}
	if summary.FailureCount == 0 {
		return nil
	}
	return summary
}

func collectReplayRepairSuggestions(events []ReplayActionEvent) []ReplayRepairSuggestion {
	var suggestions []ReplayRepairSuggestion
	seen := make(map[string]struct{})
	for _, event := range events {
		for _, suggestion := range event.RepairSuggestions {
			if suggestion.ID == "" {
				continue
			}
			if _, ok := seen[suggestion.ID]; ok {
				continue
			}
			seen[suggestion.ID] = struct{}{}
			suggestions = append(suggestions, cloneReplayRepairSuggestion(suggestion))
		}
	}
	if len(suggestions) == 0 {
		return nil
	}
	return suggestions
}

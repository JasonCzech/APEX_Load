package dsl

import (
	"fmt"
	"strings"
	"time"

	eventhubsprotocol "github.com/jasonczech/apexload/pkg/protocol/eventhubs"
)

// validateEventHubsScriptIssues validates the native Azure Event Hubs protocol
// (config.eventhubs + eventhubs_* actions). It is producer-first: eventhubs_produce
// is the primary action, eventhubs_consume is a read-only observer probe.
func validateEventHubsScriptIssues(script *Script) []string {
	var issues []string
	if strings.TrimSpace(script.Name) == "" {
		issues = append(issues, "script name is required")
	}
	if strings.TrimSpace(script.Protocol) == "" {
		issues = append(issues, "script protocol is required")
	}
	if len(script.Actions) == 0 {
		issues = append(issues, "script must have at least one action")
	}

	eh := script.Config.EventHubs
	if !eh.Mock && !isBlockedJMeterMessagingDraft(script) {
		// A non-mock run needs a reachable namespace and a real adapter. The
		// connection string may be supplied inline or via a secret reference.
		hasNamespace := strings.TrimSpace(eh.Namespace) != ""
		hasConnString := strings.TrimSpace(eh.ConnectionString) != "" || strings.TrimSpace(eh.ConnectionStringSecret) != ""
		if !hasNamespace && !hasConnString {
			issues = append(issues, "config.eventhubs requires namespace (AAD) or connection_string/connection_string_secret (SAS) when mock mode is disabled")
		}
		if !EventHubsRealAdapterAvailable() {
			issues = append(issues, "real Azure Event Hubs adapter is not available in this build; enable config.eventhubs.mock for shakeout or build with -tags eventhubs_real")
		}
	}
	if eh.AuthType != "" && !isAllowedValue(strings.ToLower(strings.TrimSpace(eh.AuthType)), "connection_string", "aad") {
		issues = append(issues, fmt.Sprintf("config.eventhubs.auth_type %q is not supported (use connection_string or aad)", eh.AuthType))
	}
	if eh.StartPosition != "" && !isAllowedValue(strings.ToLower(strings.TrimSpace(eh.StartPosition)), "latest", "earliest") {
		issues = append(issues, fmt.Sprintf("config.eventhubs.start_position %q is not supported (use latest or earliest)", eh.StartPosition))
	}
	if eh.TimeoutMs < 0 {
		issues = append(issues, "config.eventhubs.timeout_ms must be zero or greater")
	} else if time.Duration(eh.TimeoutMs)*time.Millisecond > eventhubsprotocol.MaxConfiguredTimeout {
		issues = append(issues, fmt.Sprintf("config.eventhubs.timeout_ms must not exceed %d", eventhubsprotocol.MaxConfiguredTimeout/time.Millisecond))
	}
	if len(eh.EventHub) > eventhubsprotocol.MaxEventHubNameBytes {
		issues = append(issues, fmt.Sprintf("config.eventhubs.event_hub must not exceed %d bytes", eventhubsprotocol.MaxEventHubNameBytes))
	}
	for name, value := range map[string]string{
		"namespace": eh.Namespace, "consumer_group": eh.ConsumerGroup,
		"partition_key": eh.PartitionKey, "partition_id": eh.PartitionID,
	} {
		if len(value) > eventhubsprotocol.MaxEventHubIdentifierBytes {
			issues = append(issues, fmt.Sprintf("config.eventhubs.%s must not exceed %d bytes", name, eventhubsprotocol.MaxEventHubIdentifierBytes))
		}
	}

	for i, action := range script.Actions {
		if strings.TrimSpace(action.Name) == "" {
			issues = append(issues, fmt.Sprintf("actions[%d]: missing name", i))
		}
		issues = append(issues, validateBodyFileIssues(fmt.Sprintf("actions[%d]", i), action)...)
		if action.Type == "" {
			issues = append(issues, fmt.Sprintf("actions[%d] %q: missing type", i, action.Name))
		} else if isEngineActionType(action.Type) {
			// Common lifecycle validation handles engine actions and recursively
			// validates their nested messaging steps.
		} else if _, ok := protocolActionSpec("eventhubs", action.Type); !ok {
			issues = append(issues, fmt.Sprintf("actions[%d] %q: unsupported Event Hubs action type %q", i, action.Name, action.Type))
		}
		path := fmt.Sprintf("actions[%d]", i)
		actionIssues, ok := validateCatalogActionInScript("eventhubs", path, action, script)
		if !ok {
			// Preserve residual bounds on malformed/unsupported actions while the
			// common validator owns their membership issue.
			actionIssues = validateEventHubsActionSemanticIssues(path, action, eh)
		}
		issues = append(issues, actionIssues...)
	}

	return issues
}

func validateEventHubsCatalogActionIssues(path string, action Action, script *Script) []string {
	return validateEventHubsActionSemanticIssues(path, action, script.Config.EventHubs)
}

func validateEventHubsActionSemanticIssues(path string, action Action, eh EventHubsConfig) []string {
	label := actionLabel(path, action)
	var issues []string
	if value, present := action.Params["messages"]; present {
		if action.Type != "eventhubs_produce" || action.Body != "" {
			issues = append(issues, label+": messages require a produce action without body")
		}
		if _, err := eventhubsprotocol.MessageBodies(value); err != nil {
			issues = append(issues, label+": "+err.Error())
		}
	}
	hub := actionParamString(action.Params, "event_hub")
	if hub == "" {
		hub = action.Target
	}
	if len(hub) > eventhubsprotocol.MaxEventHubNameBytes {
		issues = append(issues, fmt.Sprintf("%s: event hub must not exceed %d bytes", label, eventhubsprotocol.MaxEventHubNameBytes))
	}
	for name, value := range map[string]string{
		"consumer_group": actionParamString(action.Params, "consumer_group"),
		"partition_key":  actionParamString(action.Params, "partition_key"),
		"partition_id":   actionParamString(action.Params, "partition_id"),
	} {
		if len(value) > eventhubsprotocol.MaxEventHubIdentifierBytes {
			issues = append(issues, fmt.Sprintf("%s: %s must not exceed %d bytes", label, name, eventhubsprotocol.MaxEventHubIdentifierBytes))
		}
	}
	if value, present := actionParamIntValue(action.Params, "timeout_ms"); present && (value < 0 || value > int(eventhubsprotocol.MaxConfiguredTimeout/time.Millisecond)) {
		issues = append(issues, fmt.Sprintf("%s: timeout_ms must be between 0 and %d", label, eventhubsprotocol.MaxConfiguredTimeout/time.Millisecond))
	}
	if len(action.Body) > eventhubsprotocol.MaxEventHubsEventBytes {
		issues = append(issues, fmt.Sprintf("%s: body must not exceed %d bytes", label, eventhubsprotocol.MaxEventHubsEventBytes))
	}
	if action.Type == "eventhubs_produce" || action.Type == "eventhubs_consume" {
		eventHub, _ := action.Params["event_hub"].(string)
		if strings.TrimSpace(action.Target) == "" && strings.TrimSpace(eventHub) == "" && strings.TrimSpace(eh.EventHub) == "" {
			issues = append(issues, fmt.Sprintf("%s: missing event hub (set target, params.event_hub, or config.eventhubs.event_hub)", label))
		}
	}
	// partition_key and partition_id are mutually exclusive routing controls.
	if pk, _ := action.Params["partition_key"].(string); strings.TrimSpace(pk) != "" {
		if pid, _ := action.Params["partition_id"].(string); strings.TrimSpace(pid) != "" {
			issues = append(issues, fmt.Sprintf("%s: partition_key and partition_id are mutually exclusive", label))
		}
	}
	return issues
}

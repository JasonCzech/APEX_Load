package dsl

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	jmsprotocol "github.com/jasonczech/apexload/pkg/protocol/jms"
)

func stringMapByteSize(values map[string]string) int {
	total := 0
	for key, value := range values {
		total += len(key) + len(value)
	}
	return total
}

// validateJMSSequenceNumbersDraft mirrors the runtime parser for every static
// value while deferring template-bearing string entries until interpolation.
// Runtime parsing remains strict, so a template that resolves to a malformed,
// non-integral, out-of-range, or non-positive value still fails before the
// Service Bus operation is attempted.
func validateJMSSequenceNumbersDraft(raw any) (int, error) {
	hasTemplate := func(value string) bool {
		return strings.Contains(value, "{{") && strings.Contains(value, "}}")
	}
	switch value := raw.(type) {
	case string:
		if !hasTemplate(value) {
			return jmsprotocol.ValidateSequenceNumbers(value)
		}
		if len(value) > 64<<10 {
			return 0, fmt.Errorf("sequence_numbers string exceeds %d bytes", 64<<10)
		}
		count := 0
		for len(value) > 0 {
			part := value
			if before, after, found := strings.Cut(value, ","); found {
				part, value = before, after
			} else {
				value = ""
			}
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			count++
			if count > jmsprotocol.MaxJMSSequenceNumbers {
				return 0, fmt.Errorf("sequence_numbers must not exceed %d entries", jmsprotocol.MaxJMSSequenceNumbers)
			}
			if hasTemplate(part) {
				continue
			}
			if _, err := jmsprotocol.ValidateSequenceNumbers(part); err != nil {
				return 0, err
			}
		}
		return count, nil
	case []any:
		if len(value) > jmsprotocol.MaxJMSSequenceNumbers {
			return 0, fmt.Errorf("sequence_numbers must not exceed %d entries", jmsprotocol.MaxJMSSequenceNumbers)
		}
		for _, item := range value {
			if text, ok := item.(string); ok && hasTemplate(text) {
				continue
			}
			// Wrapping the item preserves the runtime array-item contract: a
			// comma-separated string is valid as the top-level form, but not as
			// one array element.
			if _, err := jmsprotocol.ValidateSequenceNumbers([]any{item}); err != nil {
				return 0, err
			}
		}
		return len(value), nil
	default:
		return jmsprotocol.ValidateSequenceNumbers(raw)
	}
}

// actionHasJMSDestination reports whether a JMS action carries a destination via
// its target or the catalog-documented params.destination alternative (the
// runtime resolves params.destination -> target -> config.jms.destination).
func actionHasJMSDestination(action Action) bool {
	if strings.TrimSpace(action.Target) != "" {
		return true
	}
	pd, ok := actionParamStringValue(action.Params, "destination")
	return ok && pd != ""
}

// jmsProviderCapability describes the provider-dependent part of one JMS
// action's runtime contract. Keep every catalogued JMS action in this matrix so
// adding a new verb cannot silently bypass provider validation.
type jmsProviderCapability struct {
	serviceBusOnly       bool
	serviceBusOnlyParams []string
}

var jmsProviderCapabilities = map[string]jmsProviderCapability{
	"jms_publish": {
		serviceBusOnlyParams: []string{"session_id", "partition_key", "scheduled_enqueue_ms"},
	},
	"jms_consume": {
		serviceBusOnlyParams: []string{"sub_queue", "peek_lock", "session_token"},
	},
	"jms_browse": {},
	"jms_request_reply": {
		// buildMessage applies the three advanced send fields to requests as
		// well as publishes. subscription composes a Service Bus topic reply
		// address and is otherwise ignored by the request/reply runtime.
		serviceBusOnlyParams: []string{"session_id", "partition_key", "scheduled_enqueue_ms", "subscription"},
	},
	"jms_ack":              {},
	"jms_commit":           {},
	"jms_rollback":         {},
	"jms_settle":           {serviceBusOnly: true},
	"jms_renew_lock":       {serviceBusOnly: true},
	"jms_session_accept":   {serviceBusOnly: true},
	"jms_session_state":    {serviceBusOnly: true},
	"jms_session_close":    {serviceBusOnly: true},
	"jms_receive_deferred": {serviceBusOnly: true},
	"jms_cancel_scheduled": {serviceBusOnly: true},
}

var serviceBusOnlyJMSConfigProperties = []string{
	"servicebus_max_clients",
	"servicebus_receive_mode",
}

func validateJMSScriptIssues(script *Script) []string {
	var issues []string
	jms := script.Config.JMS
	protocolName := strings.ToLower(strings.TrimSpace(script.Protocol))
	configuredProvider := strings.ToLower(strings.TrimSpace(jms.Provider))
	providerAllowed := configuredProvider == "" || isAllowedValue(configuredProvider, "solace", "ibmmq", "servicebus")
	if !providerAllowed {
		issues = append(issues, fmt.Sprintf("config.jms.provider %q is not supported; use solace, ibmmq, or servicebus", jms.Provider))
	}
	if protocolName == "jms" {
		issues = append(issues, "protocol \"jms\" is not executable; choose jms-solace, jms-ibmmq, or jms-servicebus and set the matching config.jms.provider")
	} else if expectedProvider, ok := expectedJMSProviderForProtocol(protocolName); ok {
		if configuredProvider != "" && providerAllowed && configuredProvider != expectedProvider {
			issues = append(issues, fmt.Sprintf("config.jms.provider %q does not match protocol %q; expected %q", jms.Provider, protocolName, expectedProvider))
		}
	} else if strings.HasPrefix(protocolName, "jms-") {
		issues = append(issues, fmt.Sprintf("protocol %q is not supported; choose jms-solace, jms-ibmmq, or jms-servicebus", script.Protocol))
	}

	solace := isSolaceJMSScript(script)
	serviceBus := isServiceBusJMSScript(script)
	ibmMQ := isIbmMqJMSScript(script)
	solaceSidecar := solace && strings.EqualFold(strings.TrimSpace(jms.Properties["adapter"]), "sidecar")
	if !jms.Mock {
		if jms.BrokerURL == "" {
			issues = append(issues, "config.jms.broker_url is required when mock mode is disabled")
		}
		if solace && !solaceSidecar && !SolaceJMSRealAdapterAvailable() {
			issues = append(issues, "real Solace JMS adapter is not available in this build; enable config.jms.mock for replay or build with -tags jms_solace_real")
		}
		if serviceBus && !ServiceBusJMSRealAdapterAvailable() {
			issues = append(issues, "real Azure Service Bus JMS adapter is not available in this build; enable config.jms.mock for replay or build with -tags jms_servicebus_real")
		}
		if ibmMQ && !IbmMqJMSRealAdapterAvailable() {
			issues = append(issues, "real IBM MQ JMS adapter is not available in this build; enable config.jms.mock for replay")
		}
		if ack := strings.ToLower(strings.TrimSpace(jms.Acknowledgement)); ack == "client" || ack == "transacted" {
			issues = append(issues, fmt.Sprintf("config.jms.acknowledgement %q is not supported by real adapters; use auto/dups_ok or a provider action with explicit settlement", jms.Acknowledgement))
		}
		visitScriptActions(script, func(path string, action Action) {
			switch strings.ToLower(strings.TrimSpace(action.Type)) {
			case "jms_ack", "jms_commit", "jms_rollback":
				issues = append(issues, fmt.Sprintf("%s: %s is supported only with config.jms.mock; real adapters do not implement generic JMS settlement", actionLabel(path, action), action.Type))
			}
		})
	}
	if jms.DeliveryMode != "" && !isAllowedValue(strings.ToLower(strings.TrimSpace(jms.DeliveryMode)), "persistent", "non_persistent") {
		issues = append(issues, fmt.Sprintf("config.jms.delivery_mode %q is not supported", jms.DeliveryMode))
	}
	if jms.Acknowledgement != "" && !isAllowedValue(strings.ToLower(strings.TrimSpace(jms.Acknowledgement)), "auto", "client", "dups_ok", "transacted") {
		issues = append(issues, fmt.Sprintf("config.jms.acknowledgement %q is not supported", jms.Acknowledgement))
	}
	if jms.DestinationType != "" && !isAllowedValue(strings.ToLower(strings.TrimSpace(jms.DestinationType)), "queue", "topic") {
		issues = append(issues, fmt.Sprintf("config.jms.destination_type %q is not supported", jms.DestinationType))
	}
	if jms.TimeoutMs < 0 || time.Duration(jms.TimeoutMs)*time.Millisecond > jmsprotocol.MaxConfiguredTimeout {
		issues = append(issues, fmt.Sprintf("config.jms.timeout_ms must be between 0 and %d", jmsprotocol.MaxConfiguredTimeout/time.Millisecond))
	}
	for name, value := range map[string]string{
		"destination": jms.Destination, "reply_to": jms.ReplyTo, "subscription": jms.Subscription,
	} {
		if len(value) > jmsprotocol.MaxJMSDestinationBytes {
			issues = append(issues, fmt.Sprintf("config.jms.%s must not exceed %d bytes", name, jmsprotocol.MaxJMSDestinationBytes))
		}
	}
	if len(jms.Properties) > 256 || stringMapByteSize(jms.Properties) > 1<<20 {
		issues = append(issues, "config.jms.properties must not exceed 256 entries or 1048576 bytes")
	}
	if !serviceBus {
		for _, property := range serviceBusOnlyJMSConfigProperties {
			if value, present := jms.Properties[property]; present && strings.TrimSpace(value) != "" {
				issues = append(issues, fmt.Sprintf("config.jms.properties.%s is supported only by Azure Service Bus; use protocol \"jms-servicebus\"", property))
			}
		}
	}
	visitScriptActions(script, func(path string, action Action) {
		if actionIssues, ok := validateCatalogActionInScript("jms", path, action, script); ok {
			issues = append(issues, actionIssues...)
		}
	})

	return issues
}

func validateJMSCatalogActionIssues(path string, action Action, script *Script) []string {
	jms := script.Config.JMS
	serviceBus := isServiceBusJMSScript(script)
	solaceSidecar := isSolaceJMSScript(script) && strings.EqualFold(strings.TrimSpace(jms.Properties["adapter"]), "sidecar") && !jms.Mock
	return validateJMSActionSemanticIssues(path, action, jms, serviceBus, solaceSidecar)
}

func validateJMSActionSemanticIssues(path string, action Action, jms JMSConfig, serviceBus, solaceSidecar bool) []string {
	actionType := action.Type
	if isEngineActionType(actionType) {
		return nil
	}
	if _, ok := protocolActionSpec("jms", actionType); !ok {
		// Common protocol validation owns missing/unsupported action-type issues.
		return nil
	}

	label := actionLabel(path, action)
	var issues []string
	if solaceSidecar {
		if actionType == "jms_browse" {
			issues = append(issues, fmt.Sprintf("%s: jms_browse is not supported by the Solace sidecar adapter", label))
		}
		if actionType == "jms_consume" && strings.TrimSpace(actionParamString(action.Params, "selector")) != "" {
			issues = append(issues, fmt.Sprintf("%s: params.selector is not supported by the Solace sidecar adapter", label))
		}
		for _, param := range []string{"destination_type", "subscription", "subscription_mode"} {
			if strings.TrimSpace(actionParamString(action.Params, param)) != "" {
				issues = append(issues, fmt.Sprintf("%s: action-level params.%s is not supported by the Solace sidecar adapter; configure the destination behavior in config.jms", label, param))
			}
		}
	}
	if capability, ok := jmsProviderCapabilities[actionType]; ok && !serviceBus {
		if capability.serviceBusOnly {
			issues = append(issues, fmt.Sprintf("%s: %s is supported only by Azure Service Bus; use protocol \"jms-servicebus\"", label, actionType))
		}
		for _, param := range capability.serviceBusOnlyParams {
			if jmsServiceBusOnlyParamIsEffective(action, param) {
				issues = append(issues, fmt.Sprintf("%s: params.%s is supported only by Azure Service Bus; use protocol \"jms-servicebus\"", label, param))
			}
		}
	}
	if capability, ok := jmsProviderCapabilities[actionType]; ok && serviceBus && jms.Mock {
		if capability.serviceBusOnly {
			issues = append(issues, fmt.Sprintf("%s: %s requires the real Azure Service Bus adapter and is not supported with config.jms.mock", label, actionType))
		}
		for _, param := range capability.serviceBusOnlyParams {
			// Action-level topic subscription is fully modeled by the Service Bus
			// mock request/reply path. The remaining provider-specific fields need
			// real broker scheduling, partitioning, sessions, or settlement.
			if actionType == "jms_request_reply" && param == "subscription" {
				continue
			}
			if jmsServiceBusOnlyParamIsEffective(action, param) {
				issues = append(issues, fmt.Sprintf("%s: params.%s requires the real Azure Service Bus adapter and is not supported with config.jms.mock", label, param))
			}
		}
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "target", value: action.Target},
		{name: "destination", value: actionParamString(action.Params, "destination")},
		{name: "reply_to", value: actionParamString(action.Params, "reply_to")},
		{name: "subscription", value: actionParamString(action.Params, "subscription")},
	} {
		if len(field.value) > jmsprotocol.MaxJMSDestinationBytes {
			issues = append(issues, fmt.Sprintf("%s: %s must not exceed %d bytes", label, field.name, jmsprotocol.MaxJMSDestinationBytes))
		}
	}
	if len(action.Body) > jmsprotocol.MaxJMSMessageBytes {
		issues = append(issues, fmt.Sprintf("%s: body must not exceed %d bytes", label, jmsprotocol.MaxJMSMessageBytes))
	}
	for _, bound := range []struct {
		name string
		max  int
	}{
		{name: "timeout_ms", max: int(jmsprotocol.MaxConfiguredTimeout / time.Millisecond)},
		{name: "scheduled_enqueue_ms", max: int(jmsprotocol.MaxConfiguredMessageDuration / time.Millisecond)},
		{name: "time_to_live_ms", max: int(jmsprotocol.MaxConfiguredMessageDuration / time.Millisecond)},
	} {
		if value, present := actionParamIntValue(action.Params, bound.name); present && (value < 0 || value > bound.max) {
			issues = append(issues, fmt.Sprintf("%s: %s must be between 0 and %d", label, bound.name, bound.max))
		}
	}
	if actionType == "jms_browse" {
		if value, present := actionParamIntValue(action.Params, "max"); present && (value < 0 || value > jmsprotocol.MaxJMSBrowseMessages) {
			issues = append(issues, fmt.Sprintf("%s: max must be between 0 and %d", label, jmsprotocol.MaxJMSBrowseMessages))
		}
	}
	if state := actionParamString(action.Params, "session_state"); len(state) > jmsprotocol.MaxJMSSessionStateBytes {
		issues = append(issues, fmt.Sprintf("%s: session_state must not exceed %d bytes", label, jmsprotocol.MaxJMSSessionStateBytes))
	}
	sequenceNumberCount := -1
	if actionType == "jms_receive_deferred" || actionType == "jms_cancel_scheduled" {
		count, err := validateJMSSequenceNumbersDraft(action.Params["sequence_numbers"])
		if err != nil {
			issues = append(issues, fmt.Sprintf("%s: %v", label, err))
		} else {
			sequenceNumberCount = count
		}
	}

	if jmsActionRequiresDestination(actionType) &&
		strings.TrimSpace(jms.Destination) == "" && !actionHasJMSDestination(action) {
		issues = append(issues, fmt.Sprintf("%s: missing destination", label))
	}
	if actionType == "jms_request_reply" && strings.TrimSpace(jms.ReplyTo) == "" {
		replyTo, _ := action.Params["reply_to"].(string)
		if strings.TrimSpace(replyTo) == "" {
			issues = append(issues, fmt.Sprintf("%s: missing reply_to destination", label))
		}
	}
	if serviceBus && jmsActionConsumesFromServiceBus(actionType) &&
		jmsActionUsesServiceBusTopicWithoutSubscription(jms, action) {
		issues = append(issues, fmt.Sprintf("%s: subscription required to consume from a Service Bus topic", label))
	}

	// JMS message-type model + delivery controls (P2.3) on send actions.
	if actionType == "jms_publish" || actionType == "jms_request_reply" {
		if mt, ok := actionParamStringValue(action.Params, "message_type"); ok &&
			!isAllowedValue(strings.ToLower(strings.TrimSpace(mt)), "text", "bytes", "map", "stream", "object") {
			issues = append(issues, fmt.Sprintf("%s: message_type %q is not supported", label, mt))
		}
		if dm, ok := actionParamStringValue(action.Params, "delivery_mode"); ok &&
			!isAllowedValue(strings.ToLower(strings.TrimSpace(dm)), "persistent", "non_persistent") {
			issues = append(issues, fmt.Sprintf("%s: delivery_mode %q is not supported", label, dm))
		}
		if _, present := action.Params["priority"]; present {
			if priority, ok := actionParamIntValue(action.Params, "priority"); !ok || priority < 0 || priority > 9 {
				issues = append(issues, fmt.Sprintf("%s: priority must be an integer 0-9", label))
			}
		}
	}

	// Service Bus advanced verbs (P2.4): validate enums + required tokens.
	if actionType == "jms_consume" {
		if subQueue, ok := actionParamStringValue(action.Params, "sub_queue"); ok &&
			!isAllowedValue(strings.ToLower(strings.TrimSpace(subQueue)), "none", "dead_letter", "transfer_dead_letter") {
			issues = append(issues, fmt.Sprintf("%s: sub_queue %q is not supported", label, subQueue))
		}
		if mode, ok := actionParamStringValue(action.Params, "subscription_mode"); ok &&
			!isAllowedValue(strings.ToLower(strings.TrimSpace(mode)), "non_durable", "durable", "shared") {
			issues = append(issues, fmt.Sprintf("%s: subscription_mode %q is not supported", label, mode))
		}
		if selector, ok := actionParamStringValue(action.Params, "selector"); ok && strings.TrimSpace(selector) != "" {
			if serviceBus && !jms.Mock {
				issues = append(issues, fmt.Sprintf("%s: selector is not supported by Azure Service Bus jms_consume; configure a subscription rule instead", label))
			}
			subQueue, _ := actionParamStringValue(action.Params, "sub_queue")
			hasSubQueue := strings.TrimSpace(subQueue) != "" && !strings.EqualFold(strings.TrimSpace(subQueue), "none")
			_, hasSession := actionParamStringValue(action.Params, "session_token")
			peekLock, _ := action.Params["peek_lock"].(bool)
			if hasSubQueue || hasSession || peekLock {
				issues = append(issues, fmt.Sprintf("%s: selector cannot be combined with peek_lock, sub_queue, or session_token", label))
			}
		}
	}
	if actionType == "jms_request_reply" {
		if selector, ok := actionParamStringValue(action.Params, "selector"); ok && strings.TrimSpace(selector) != "" {
			issues = append(issues, fmt.Sprintf("%s: selector is not supported by jms_request_reply", label))
		}
	}

	// Topic consume/browse on a non-Service-Bus provider requires a subscription.
	if (actionType == "jms_consume" || actionType == "jms_browse") && !serviceBus {
		destinationType := jms.DestinationType
		if value, ok := actionParamStringValue(action.Params, "destination_type"); ok {
			destinationType = value
		}
		if strings.EqualFold(strings.TrimSpace(destinationType), "topic") {
			subscription := jms.Subscription
			if value, ok := actionParamStringValue(action.Params, "subscription"); ok {
				subscription = value
			}
			if strings.TrimSpace(subscription) == "" {
				issues = append(issues, fmt.Sprintf("%s: consuming from a topic requires a subscription", label))
			}
		}
	}
	if actionType == "jms_settle" {
		if disposition, ok := actionParamStringValue(action.Params, "disposition"); ok &&
			!isAllowedValue(strings.ToLower(strings.TrimSpace(disposition)), "complete", "abandon", "dead_letter", "defer") {
			issues = append(issues, fmt.Sprintf("%s: disposition %q is not supported", label, disposition))
		}
		if token, _ := actionParamStringValue(action.Params, "settlement_token"); strings.TrimSpace(token) == "" {
			issues = append(issues, fmt.Sprintf("%s: settlement_token is required", label))
		}
	}
	if actionType == "jms_renew_lock" {
		if token, _ := actionParamStringValue(action.Params, "settlement_token"); strings.TrimSpace(token) == "" {
			issues = append(issues, fmt.Sprintf("%s: settlement_token is required", label))
		}
	}
	if actionType == "jms_session_state" || actionType == "jms_session_close" {
		if token, _ := actionParamStringValue(action.Params, "session_token"); strings.TrimSpace(token) == "" {
			issues = append(issues, fmt.Sprintf("%s: session_token is required", label))
		}
	}
	if actionType == "jms_receive_deferred" || actionType == "jms_cancel_scheduled" {
		if sequenceNumberCount == 0 {
			issues = append(issues, fmt.Sprintf("%s: sequence_numbers is required", label))
		}
	}
	return issues
}

// jmsServiceBusOnlyParamIsEffective mirrors the runtime's no-op defaults. This
// avoids rejecting drafts that retain harmless false/none/zero values while
// still failing fast whenever another provider would discard requested
// Service Bus behavior.
func jmsServiceBusOnlyParamIsEffective(action Action, param string) bool {
	raw, present := action.Params[param]
	if !present {
		return false
	}
	switch param {
	case "peek_lock":
		value, _ := raw.(bool)
		return value
	case "sub_queue":
		value, _ := actionParamStringValue(action.Params, param)
		value = strings.ToLower(strings.TrimSpace(value))
		return value != "" && value != "none"
	case "session_id", "partition_key", "session_token", "subscription":
		value, _ := actionParamStringValue(action.Params, param)
		return strings.TrimSpace(value) != ""
	case "scheduled_enqueue_ms":
		value, ok := actionParamIntValue(action.Params, param)
		if ok {
			return value > 0
		}
		// Runtime interpolation can leave a numeric value as text, and intParam
		// accepts numeric strings. Treat any non-empty/non-zero text (including a
		// template whose eventual value is unknown) as potentially effective.
		if text, ok := raw.(string); ok {
			text = strings.TrimSpace(text)
			if strings.Contains(text, "{{") {
				return true
			}
			value, err := strconv.Atoi(text)
			return err == nil && value > 0
		}
		return false
	default:
		return true
	}
}

func jmsActionRequiresDestination(actionType string) bool {
	switch actionType {
	case "jms_publish", "jms_consume", "jms_request_reply", "jms_browse",
		"jms_session_accept", "jms_receive_deferred", "jms_cancel_scheduled":
		return true
	default:
		return false
	}
}

func jmsActionConsumesFromServiceBus(actionType string) bool {
	switch actionType {
	case "jms_consume", "jms_request_reply", "jms_browse", "jms_session_accept", "jms_receive_deferred":
		return true
	default:
		return false
	}
}

func expectedJMSProviderForProtocol(protocolName string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(protocolName)) {
	case "jms-solace":
		return "solace", true
	case "jms-ibmmq":
		return "ibmmq", true
	case "jms-servicebus":
		return "servicebus", true
	default:
		return "", false
	}
}

func isServiceBusJMSScript(script *Script) bool {
	provider, ok := expectedJMSProviderForProtocol(script.Protocol)
	return ok && provider == "servicebus"
}

func isSolaceJMSScript(script *Script) bool {
	provider, ok := expectedJMSProviderForProtocol(script.Protocol)
	return ok && provider == "solace"
}

func isIbmMqJMSScript(script *Script) bool {
	provider, ok := expectedJMSProviderForProtocol(script.Protocol)
	return ok && provider == "ibmmq"
}

func jmsActionUsesServiceBusTopicWithoutSubscription(jms JMSConfig, action Action) bool {
	destinationType, ok := actionParamStringValue(action.Params, "destination_type")
	if !ok {
		destinationType = jms.DestinationType
	}
	if !strings.EqualFold(strings.TrimSpace(destinationType), "topic") {
		return false
	}
	destination := jmsActionConsumeDestination(jms, action)
	if jmsDestinationIncludesServiceBusSubscription(destination) {
		return false
	}
	subscription, ok := actionParamStringValue(action.Params, "subscription")
	if !ok {
		subscription = jms.Subscription
	}
	return strings.TrimSpace(subscription) == ""
}

func jmsActionConsumeDestination(jms JMSConfig, action Action) string {
	if action.Type == "jms_request_reply" {
		if replyTo, ok := actionParamStringValue(action.Params, "reply_to"); ok {
			return replyTo
		}
		return jms.ReplyTo
	}
	if destination, ok := actionParamStringValue(action.Params, "destination"); ok {
		return destination
	}
	if strings.TrimSpace(action.Target) != "" {
		return action.Target
	}
	return jms.Destination
}

func jmsDestinationIncludesServiceBusSubscription(destination string) bool {
	trimmed := strings.Trim(strings.TrimSpace(destination), "/")
	lower := strings.ToLower(trimmed)
	const marker = "/subscriptions/"
	idx := strings.Index(lower, marker)
	if idx < 0 {
		return false
	}
	return strings.Trim(trimmed[:idx], "/") != "" && strings.Trim(trimmed[idx+len(marker):], "/") != ""
}

// Package jms provides a provider-neutral JMS protocol implementation.
package jms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/messaging"
)

// sidecarCommandEnv is the operator-owned environment variable that supplies the
// JMS sidecar command line. The sidecar command is deliberately NOT sourced from
// script-supplied properties: a script is user-controlled JSON, so honoring a
// script-supplied command would allow arbitrary command execution on generators.
const (
	sidecarCommandEnv            = "APEXLOAD_JMS_SIDECAR_COMMAND"
	sidecarMaxProcessesEnv       = "APEXLOAD_JMS_SIDECAR_MAX_PROCESSES"
	defaultSidecarProcesses      = 25
	maxSidecarProcesses          = 1_024
	maxSidecarCaptureBytes       = 256 << 10
	MaxConfiguredTimeout         = time.Hour
	MaxConfiguredMessageDuration = 365 * 24 * time.Hour
	MaxJMSDestinationBytes       = 4 << 10
	MaxJMSIdentifierBytes        = 4 << 10
	MaxJMSBrowseMessages         = 250
	MaxJMSSequenceNumbers        = 100
	MaxJMSSessionStateBytes      = 256 << 10
	MaxJMSMessageBytes           = 64 << 20
	maxJMSSelectorBytes          = 64 << 10
	maxJMSPropertyEntries        = 256
	maxJMSPropertyBytes          = 1 << 20
)

type sidecarProcessLimiter struct {
	slots chan struct{}
}

func newSidecarProcessLimiter(limit int) *sidecarProcessLimiter {
	if limit <= 0 {
		limit = defaultSidecarProcesses
	}
	if limit > maxSidecarProcesses {
		limit = maxSidecarProcesses
	}
	return &sidecarProcessLimiter{slots: make(chan struct{}, limit)}
}

func configuredSidecarMaxProcesses() int {
	limit, err := strconv.Atoi(strings.TrimSpace(os.Getenv(sidecarMaxProcessesEnv)))
	if err != nil || limit <= 0 {
		return defaultSidecarProcesses
	}
	if limit > maxSidecarProcesses {
		return maxSidecarProcesses
	}
	return limit
}

func (l *sidecarProcessLimiter) acquire(ctx context.Context) (func(), error) {
	if l == nil {
		return func() {}, nil
	}
	select {
	case l.slots <- struct{}{}:
		return func() { <-l.slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

var globalSidecarProcessLimiter = newSidecarProcessLimiter(configuredSidecarMaxProcesses())

// redeliveredProperty marks a message that a transacted/client rollback requeued
// onto the in-memory mock broker, so a later consume can count it as a
// redelivery. Real brokers carry their own redelivery metadata.
const redeliveredProperty = "jms_redelivered"

func init() {
	protocol.Register("jms-solace", func() protocol.Protocol {
		return &JMS{name: "jms-solace", provider: "solace"}
	})
	protocol.Register("jms-ibmmq", func() protocol.Protocol {
		return &JMS{name: "jms-ibmmq", provider: "ibmmq"}
	})
	protocol.Register("jms-servicebus", func() protocol.Protocol {
		return &JMS{name: "jms-servicebus", provider: "servicebus"}
	})
	protocol.RegisterRuntimeNamespaceManager("jms", broker.acquire, broker.release)
}

type JMS struct {
	name               string
	provider           string
	cfg                config
	adapter            brokerAdapter
	session            *messaging.Session[message]
	initialized        bool
	mockNamespace      string
	mockRelease        *sync.Once
	allowedTargetHosts []string
	// traceCarrier writes and reads W3C context in message properties.
	traceCarrier bool
}

type config struct {
	BrokerURL              string            `json:"broker_url,omitempty"`
	MessageVPN             string            `json:"message_vpn,omitempty"`
	Username               string            `json:"username,omitempty"`
	Password               string            `json:"password,omitempty"`
	ClientID               string            `json:"client_id,omitempty"`
	Destination            string            `json:"destination,omitempty"`
	DestinationType        string            `json:"destination_type,omitempty"`
	Subscription           string            `json:"subscription,omitempty"`
	DeadLetterDestinations []string          `json:"dead_letter_destinations,omitempty"`
	ReplyTo                string            `json:"reply_to,omitempty"`
	Acknowledgement        string            `json:"acknowledgement,omitempty"`
	DeliveryMode           string            `json:"delivery_mode,omitempty"`
	Timeout                time.Duration     `json:"timeout,omitempty"`
	Properties             map[string]string `json:"properties,omitempty"`
	TLS                    jmsTLSConfig      `json:"tls,omitempty"`
	Mock                   bool              `json:"mock,omitempty"`
}

type jmsTLSConfig struct {
	CACertSecret       string `json:"ca_cert_secret,omitempty"`
	TrustStoreRef      string `json:"trust_store_ref,omitempty"`
	InsecureSkipVerify bool   `json:"insecure_skip_verify,omitempty"`
}

type message struct {
	// sent is internal adapter outcome evidence for a request whose reply later fails.
	sent          bool
	sendAttempted bool
	received      bool
	ID            string            `json:"id,omitempty"`
	CorrelationID string            `json:"correlation_id,omitempty"`
	Destination   string            `json:"destination,omitempty"`
	Headers       map[string]string `json:"headers,omitempty"`
	Properties    map[string]string `json:"properties,omitempty"`
	Body          []byte            `json:"body,omitempty"`
	CreatedAt     time.Time         `json:"created_at,omitempty"`
	// Azure Service Bus advanced fields (P2.4). Send-side: SessionID/PartitionKey
	// routing, ScheduledEnqueueAt deferred enqueue, TimeToLive expiry. Receive-side:
	// SettlementToken keys the in-flight peek-locked message for later Settle, and
	// SequenceNumber identifies a deferred message. Empty/zero on providers that
	// don't use them.
	SessionID          string        `json:"session_id,omitempty"`
	PartitionKey       string        `json:"partition_key,omitempty"`
	ScheduledEnqueueAt time.Time     `json:"scheduled_enqueue_at,omitempty"`
	TimeToLive         time.Duration `json:"time_to_live,omitempty"`
	SettlementToken    string        `json:"settlement_token,omitempty"`
	SequenceNumber     int64         `json:"sequence_number,omitempty"`
	// JMS message-type model + delivery controls (P2.3). Type discriminates the
	// JMS body kind (text/bytes/map/stream/object); MapBody/StreamBody carry the
	// structured payload for map/stream messages (also JSON-encoded into Body so
	// extractors and the mock see one wire form). DeliveryMode (persistent/
	// non_durable) and Priority (0-9) are applied on send by the AMQP/Solace
	// adapter; the mock carries them for fidelity. Empty/nil when unset.
	Type         string            `json:"type,omitempty"`
	MapBody      map[string]string `json:"map_body,omitempty"`
	StreamBody   []string          `json:"stream_body,omitempty"`
	DeliveryMode string            `json:"delivery_mode,omitempty"`
	Priority     *uint8            `json:"priority,omitempty"`
	// sourceTopic / sourceSubscription record a topic-subscription origin so a
	// transacted/client rollback requeues the message back to that subscription's
	// queue (not into the unrelated queue map). Unexported: mock-internal routing
	// state, never serialized; re-stamped by the topic consume path on redelivery.
	sourceTopic        string
	sourceSubscription string
}

func (j *JMS) Name() string {
	return j.name
}

func (j *JMS) Init(_ context.Context, cfg map[string]any) error {
	provider, err := resolveJMSProvider(j.name, j.provider, stringValue(cfg, "jms_provider"))
	if err != nil {
		return err
	}
	j.provider = provider
	j.allowedTargetHosts = protocol.TargetPolicyHosts(cfg)
	if j.traceCarrier, err = protocol.MessagingTraceCarrierEnabled(cfg); err != nil {
		return err
	}
	timeoutMS, err := boundedJMSInteger(cfg["jms_timeout_ms"], int((5*time.Second)/time.Millisecond), 0, int(MaxConfiguredTimeout/time.Millisecond))
	if err != nil {
		return fmt.Errorf("jms timeout_ms: %w", err)
	}
	j.cfg = config{
		BrokerURL:              stringValue(cfg, "broker_url"),
		MessageVPN:             stringValue(cfg, "jms_message_vpn"),
		Username:               stringValue(cfg, "jms_username"),
		Password:               stringValue(cfg, "jms_password"),
		ClientID:               stringValue(cfg, "jms_client_id"),
		Destination:            stringValue(cfg, "jms_destination"),
		DestinationType:        stringValue(cfg, "jms_destination_type"),
		Subscription:           stringValue(cfg, "jms_subscription"),
		DeadLetterDestinations: stringSliceValue(cfg, "jms_dead_letter_destinations"),
		ReplyTo:                stringValue(cfg, "jms_reply_to"),
		Acknowledgement:        stringValue(cfg, "jms_acknowledgement"),
		DeliveryMode:           stringValue(cfg, "jms_delivery_mode"),
		Timeout:                time.Duration(timeoutMS) * time.Millisecond,
		Properties:             stringMapValue(cfg, "jms_properties"),
		TLS: jmsTLSConfig{
			CACertSecret:       stringValue(cfg, "jms_tls_ca_cert_secret"),
			TrustStoreRef:      stringValue(cfg, "jms_tls_trust_store_ref"),
			InsecureSkipVerify: boolValue(cfg, "jms_tls_insecure_skip_verify"),
		},
		Mock: boolValue(cfg, "jms_mock"),
	}
	if j.cfg.DestinationType == "" {
		j.cfg.DestinationType = "queue"
	}
	if j.cfg.Acknowledgement == "" {
		j.cfg.Acknowledgement = "auto"
	}
	if j.cfg.DeliveryMode == "" {
		j.cfg.DeliveryMode = "persistent"
	}
	if j.cfg.Timeout <= 0 {
		j.cfg.Timeout = 5 * time.Second
	}
	if err := validateJMSConfigBounds(j.cfg); err != nil {
		return err
	}
	if !j.cfg.Mock {
		if err := protocol.ValidateTargetReference(j.cfg.BrokerURL, j.allowedTargetHosts); err != nil {
			return err
		}
	}
	j.session = messaging.New[message](messaging.ParseAckMode(j.cfg.Acknowledgement))
	if !j.cfg.Mock && j.session.NeedsSettlement() {
		return fmt.Errorf("jms acknowledgement mode %q is not supported by real adapters; use auto/dups_ok or a provider action with explicit settlement", j.cfg.Acknowledgement)
	}
	if j.cfg.Mock {
		j.mockNamespace = stringValue(cfg, "runtime_mock_namespace")
		j.mockRelease = &sync.Once{}
		broker.acquire(j.mockNamespace)
		j.adapter = memoryAdapter{broker: broker, namespace: j.mockNamespace}
		// Pre-register a configured topic subscription at connect time so a topic
		// publish that lands before the subscriber's first consume is still captured
		// (durable-subscription semantics). Without this, the mock only retains
		// messages published after the first consume registers the subscription.
		if strings.EqualFold(strings.TrimSpace(j.cfg.DestinationType), "topic") {
			topicName, subscription := j.mockTopicCoordinates(j.cfg.Destination, j.cfg.Subscription)
			if topicName != "" && subscription != "" {
				if err := broker.subscribe(mockJMSKey(j.mockNamespace, topicName), subscription); err != nil {
					j.mockRelease.Do(func() { broker.release(j.mockNamespace) })
					j.adapter = nil
					return err
				}
			}
		}
	} else {
		adapter, err := j.selectAdapter()
		if err != nil {
			return err
		}
		j.adapter = adapter
	}
	j.initialized = true
	return nil
}

func (j *JMS) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	start := time.Now()
	if action == nil {
		return nil, fmt.Errorf("JMS action is required")
	}
	if j.session == nil {
		j.session = messaging.New[message](messaging.ParseAckMode(j.cfg.Acknowledgement))
	}
	destination := j.destination(action)
	isDeadLetter := j.isDeadLetterAction(action, destination)
	result := &protocol.Result{
		ActionName:       action.Name,
		StartTime:        start,
		StatusCode:       200,
		Success:          true,
		Extractions:      make(map[string]string),
		DependencyType:   classifyJMSDependencyType(j.cfg.DestinationType, action, isDeadLetter),
		DependencyTarget: destination,
	}
	if err := j.validateActionBounds(action); err != nil {
		result.Success = false
		result.StatusCode = 400
		result.ErrorMessage = err.Error()
		result.Duration = time.Since(start)

		result.ProtocolEvidence = protocol.BuildProtocolEvidence(action, result)
		return result, nil
	}
	actionCtx, actionCancel := context.WithTimeout(ctx, j.timeout(action))
	defer actionCancel()
	ctx = actionCtx
	if isDeadLetter {
		result.Extractions["jms_dead_letter"] = "true"
		result.Extractions["jms_dead_letter_destination"] = destination
	}

	var err error
	var requestMsg message
	var responseMsg message
	hasRequestMsg := false
	hasResponseMsg := false
	sendAttempted := false
	switch strings.ToLower(action.Type) {
	case "jms_publish":
		requestMsg, err = j.publish(ctx, action, result, &hasRequestMsg, &sendAttempted)
	case "jms_consume":
		responseMsg, err = j.consume(ctx, action, result, &hasResponseMsg)
	case "jms_browse":
		responseMsg, hasResponseMsg, err = j.browse(ctx, action, result)
	case "jms_request_reply":
		requestMsg, responseMsg, err = j.requestReply(ctx, action, result, &hasRequestMsg, &hasResponseMsg, &sendAttempted)
	case "jms_ack", "jms_commit", "jms_rollback":
		if !j.cfg.Mock {
			err = fmt.Errorf("%s is supported only in mock mode; real adapters do not implement generic JMS settlement", action.Type)
		} else {
			err = j.settle(action, result)
		}
	case "jms_settle", "jms_renew_lock":
		err = j.settleAdvanced(ctx, action, result)
	case "jms_session_accept", "jms_session_state", "jms_session_close", "jms_receive_deferred", "jms_cancel_scheduled":
		responseMsg, hasResponseMsg, err = j.sessionAdvanced(ctx, action, result)
	default:
		err = fmt.Errorf("unsupported JMS action type %q", action.Type)
	}

	protocol.SettleMessagingTraceContext(result, sendAttempted)
	result.Duration = time.Since(start)
	if result.ServerTelemetry == nil {
		result.ServerTelemetry = finalizeJMSServerTelemetry(result, requestMsg, hasRequestMsg, responseMsg, hasResponseMsg)
	}
	if err != nil {
		result.Success = false
		if result.StatusCode == 0 || result.StatusCode == 200 {
			result.StatusCode = 500
		}
		result.ErrorMessage = err.Error()
		if isJMSSendAction(action.Type) && !hasRequestMsg {
			// Count a failed publish/send attempt so the messaging reliability
			// telemetry reflects send failures instead of always reading zero.
			if result.ServerTelemetry == nil {
				result.ServerTelemetry = &protocol.ServerTelemetry{}
			}
			result.ServerTelemetry.SendFailureCount++
		}
	}
	if result.ServerTelemetry != nil && hasResponseMsg {
		result.ServerTelemetry.SubscriptionID = responseMsg.sourceSubscription
		if result.ServerTelemetry.SubscriptionID == "" {
			result.ServerTelemetry.SubscriptionID = j.subscriptionName(action)
		}
	}
	if isJMSSendAction(action.Type) {
		if result.ServerTelemetry == nil {
			result.ServerTelemetry = &protocol.ServerTelemetry{}
		}
		protocol.AnnotateMessagingSendOutcome(result.ServerTelemetry, sendAttempted, hasRequestMsg, false)
	}
	result.ProtocolEvidence = protocol.BuildProtocolEvidence(action, result)
	return result, nil
}

// isJMSSendAction reports whether the action attempts a publish/send, so a
// failure can be counted as a failed publish attempt (SendFailureCount).
func isJMSSendAction(actionType string) bool {
	switch strings.ToLower(actionType) {
	case "jms_publish", "jms_request_reply":
		return true
	}
	return false
}

func (j *JMS) Teardown(ctx context.Context) error {
	if j.adapter == nil {
		return nil
	}
	err := j.adapter.Teardown(ctx)
	if j.cfg.Mock && j.mockRelease != nil {
		j.mockRelease.Do(func() { broker.release(j.mockNamespace) })
	}
	return err
}

func (j *JMS) HealthCheck(ctx context.Context) error {
	if !j.initialized {
		return nil
	}
	if j.cfg.BrokerURL == "" && !j.cfg.Mock {
		return fmt.Errorf("jms broker_url is not configured")
	}
	if j.adapter == nil {
		return fmt.Errorf("jms adapter is not initialized")
	}
	return j.adapter.HealthCheck(ctx)
}

func validateJMSConfigBounds(cfg config) error {
	if len(cfg.BrokerURL) > 8<<10 {
		return fmt.Errorf("JMS broker_url exceeds %d bytes", 8<<10)
	}
	for name, value := range map[string]string{
		"destination": cfg.Destination, "reply_to": cfg.ReplyTo,
		"subscription": cfg.Subscription,
	} {
		if len(value) > MaxJMSDestinationBytes {
			return fmt.Errorf("JMS %s exceeds %d bytes", name, MaxJMSDestinationBytes)
		}
	}
	for name, value := range map[string]string{
		"message_vpn": cfg.MessageVPN, "username": cfg.Username, "client_id": cfg.ClientID,
	} {
		if len(value) > MaxJMSIdentifierBytes {
			return fmt.Errorf("JMS %s exceeds %d bytes", name, MaxJMSIdentifierBytes)
		}
	}
	if len(cfg.Password) > 64<<10 {
		return fmt.Errorf("JMS password exceeds %d bytes", 64<<10)
	}
	for name, value := range map[string]string{"ca_cert_secret": cfg.TLS.CACertSecret, "trust_store_ref": cfg.TLS.TrustStoreRef} {
		if len(value) > 8<<10 {
			return fmt.Errorf("JMS %s exceeds %d bytes", name, 8<<10)
		}
	}
	if len(cfg.DeadLetterDestinations) > MaxJMSSequenceNumbers {
		return fmt.Errorf("JMS dead_letter_destinations exceed %d entries", MaxJMSSequenceNumbers)
	}
	for _, destination := range cfg.DeadLetterDestinations {
		if len(destination) > MaxJMSDestinationBytes {
			return fmt.Errorf("JMS dead-letter destination exceeds %d bytes", MaxJMSDestinationBytes)
		}
	}
	if len(cfg.Properties) > maxJMSPropertyEntries {
		return fmt.Errorf("JMS properties exceed %d entries", maxJMSPropertyEntries)
	}
	propertyBytes := 0
	for key, value := range cfg.Properties {
		propertyBytes += len(key) + len(value)
	}
	if propertyBytes > maxJMSPropertyBytes {
		return fmt.Errorf("JMS properties exceed %d bytes", maxJMSPropertyBytes)
	}
	return nil
}

func (j *JMS) validateActionBounds(action *protocol.Action) error {
	for name, value := range map[string]string{
		"destination":         j.destination(action),
		"consume destination": j.consumeDestination(action),
		"reply_to":            paramString(action.Params, "reply_to", j.cfg.ReplyTo),
		"subscription":        paramString(action.Params, "subscription", j.cfg.Subscription),
	} {
		if len(value) > MaxJMSDestinationBytes {
			return fmt.Errorf("JMS %s exceeds %d bytes", name, MaxJMSDestinationBytes)
		}
	}
	for _, field := range []string{
		"correlation_id", "message_id", "session_id", "partition_key",
		"settlement_token", "session_token", "dead_letter_reason", "dead_letter_description",
	} {
		if value := paramString(action.Params, field, ""); len(value) > MaxJMSIdentifierBytes {
			return fmt.Errorf("JMS %s exceeds %d bytes", field, MaxJMSIdentifierBytes)
		}
	}
	if selector := paramString(action.Params, "selector", ""); len(selector) > maxJMSSelectorBytes {
		return fmt.Errorf("JMS selector exceeds %d bytes", maxJMSSelectorBytes)
	}
	if state := paramString(action.Params, "session_state", ""); len(state) > MaxJMSSessionStateBytes {
		return fmt.Errorf("JMS session_state exceeds %d bytes", MaxJMSSessionStateBytes)
	}
	for _, limit := range []struct {
		key string
		max int
	}{
		{"timeout_ms", int(MaxConfiguredTimeout / time.Millisecond)},
		{"scheduled_enqueue_ms", int(MaxConfiguredMessageDuration / time.Millisecond)},
		{"time_to_live_ms", int(MaxConfiguredMessageDuration / time.Millisecond)},
	} {
		if raw, present := action.Params[limit.key]; present {
			if _, err := boundedJMSInteger(raw, 0, 0, limit.max); err != nil {
				return fmt.Errorf("JMS %s: %w", limit.key, err)
			}
		}
	}
	if strings.EqualFold(action.Type, "jms_request_reply") && strings.TrimSpace(paramString(action.Params, "selector", "")) != "" {
		return fmt.Errorf("JMS selector is not supported by jms_request_reply")
	}
	if _, sidecar := j.adapter.(sidecarAdapter); sidecar {
		if strings.EqualFold(action.Type, "jms_browse") {
			return fmt.Errorf("jms_browse is not supported by the Solace sidecar adapter")
		}
		if strings.EqualFold(action.Type, "jms_consume") && strings.TrimSpace(paramString(action.Params, "selector", "")) != "" {
			return fmt.Errorf("JMS params.selector is not supported by the Solace sidecar adapter")
		}
		for _, field := range []string{"destination_type", "subscription", "subscription_mode"} {
			if strings.TrimSpace(paramString(action.Params, field, "")) != "" {
				return fmt.Errorf("JMS action-level params.%s is not supported by the Solace sidecar adapter", field)
			}
		}
	}
	if j.cfg.Mock && strings.EqualFold(strings.TrimSpace(j.provider), "servicebus") &&
		(strings.EqualFold(action.Type, "jms_publish") || strings.EqualFold(action.Type, "jms_request_reply")) {
		for _, field := range []string{"session_id", "partition_key"} {
			if strings.TrimSpace(paramString(action.Params, field, "")) != "" {
				return fmt.Errorf("JMS params.%s requires the real Azure Service Bus adapter and is not supported in mock mode", field)
			}
		}
		if raw, present := action.Params["scheduled_enqueue_ms"]; present {
			delay, _ := boundedJMSInteger(raw, 0, 0, int(MaxConfiguredMessageDuration/time.Millisecond))
			if delay > 0 {
				return fmt.Errorf("JMS params.scheduled_enqueue_ms requires the real Azure Service Bus adapter and is not supported in mock mode")
			}
		}
	}
	if raw, present := action.Params["max"]; present {
		if _, err := boundedJMSInteger(raw, 0, 0, MaxJMSBrowseMessages); err != nil {
			return fmt.Errorf("JMS browse max: %w", err)
		}
	}
	if len(action.Headers) > maxJMSPropertyEntries {
		return fmt.Errorf("JMS headers exceed %d entries", maxJMSPropertyEntries)
	}
	if len(action.Metadata) > maxJMSPropertyEntries {
		return fmt.Errorf("JMS metadata properties exceed %d entries", maxJMSPropertyEntries)
	}
	if values, ok := action.Params["stream_body"].([]any); ok && len(values) > maxJMSPropertyEntries {
		return fmt.Errorf("JMS stream_body exceeds %d entries", maxJMSPropertyEntries)
	}
	if values, ok := action.Params["stream_body"].([]string); ok && len(values) > maxJMSPropertyEntries {
		return fmt.Errorf("JMS stream_body exceeds %d entries", maxJMSPropertyEntries)
	}
	if values, ok := action.Params["map_body"].(map[string]any); ok && len(values) > maxJMSPropertyEntries {
		return fmt.Errorf("JMS map_body exceeds %d entries", maxJMSPropertyEntries)
	}
	if values, ok := action.Params["map_body"].(map[string]string); ok && len(values) > maxJMSPropertyEntries {
		return fmt.Errorf("JMS map_body exceeds %d entries", maxJMSPropertyEntries)
	}
	headerBytes := 0
	for key, value := range action.Headers {
		headerBytes += len(key) + len(value)
	}
	for key, value := range action.Metadata {
		headerBytes += len(key)
		if text, ok := value.(string); ok {
			headerBytes += len(text)
		}
	}
	switch values := action.Params["stream_body"].(type) {
	case []any:
		for _, value := range values {
			size, ok := jmsScalarValueSize(value)
			if !ok {
				return fmt.Errorf("JMS stream_body entries must be scalar values")
			}
			headerBytes += size
		}
	case []string:
		for _, value := range values {
			headerBytes += len(value)
		}
	}
	switch values := action.Params["map_body"].(type) {
	case map[string]any:
		for key, value := range values {
			size, ok := jmsScalarValueSize(value)
			if !ok {
				return fmt.Errorf("JMS map_body values must be scalar values")
			}
			headerBytes += len(key) + size
		}
	case map[string]string:
		for key, value := range values {
			headerBytes += len(key) + len(value)
		}
	}
	if headerBytes > maxJMSPropertyBytes {
		return fmt.Errorf("JMS message properties exceed %d bytes", maxJMSPropertyBytes)
	}
	if len(action.Body) > MaxJMSMessageBytes {
		return fmt.Errorf("JMS body exceeds %d bytes", MaxJMSMessageBytes)
	}
	return nil
}

func jmsScalarValueSize(value any) (int, bool) {
	switch typed := value.(type) {
	case nil:
		return 0, true
	case string:
		return len(typed), true
	case []byte:
		return len(typed), true
	case json.Number:
		return len(typed), true
	case bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return 32, true
	default:
		return 0, false
	}
}

func validateJMSMessageBounds(msg message) error {
	if len(msg.Headers) > maxJMSPropertyEntries || len(msg.Properties) > maxJMSPropertyEntries || len(msg.MapBody) > maxJMSPropertyEntries {
		return fmt.Errorf("JMS message headers/properties/map fields exceed %d entries", maxJMSPropertyEntries)
	}
	propertyBytes := 0
	for key, value := range msg.Headers {
		propertyBytes += len(key) + len(value)
	}
	for key, value := range msg.Properties {
		propertyBytes += len(key) + len(value)
	}
	for key, value := range msg.MapBody {
		propertyBytes += len(key) + len(value)
	}
	if propertyBytes > maxJMSPropertyBytes {
		return fmt.Errorf("JMS message headers/properties exceed %d bytes", maxJMSPropertyBytes)
	}
	if size := messageSize(msg); size > MaxJMSMessageBytes {
		return fmt.Errorf("JMS message exceeds %d bytes", MaxJMSMessageBytes)
	}
	return nil
}

func (j *JMS) publish(ctx context.Context, action *protocol.Action, result *protocol.Result, accepted, attempted *bool) (observed message, operationErr error) {
	var sent message
	defer func() {
		if *accepted {
			observed = sent
		}
	}()
	destination := j.destination(action)
	if destination == "" {
		return message{}, fmt.Errorf("JMS destination is required")
	}
	msg := j.buildMessage(action, destination)
	j.injectTraceContext(ctx, &msg, result)
	if err := validateJMSMessageBounds(msg); err != nil {
		return message{}, err
	}
	var published message
	var err error
	*attempted = true
	if topic, ok := j.adapter.(serviceBusMockTopicAdapter); ok && j.usesMockServiceBusTopic(action) {
		// Service Bus subscriptions are durable broker entities. Retain a bounded
		// topic history so an action-level subscription declared by a later
		// consume still observes this publish.
		err = topic.PublishServiceBusTopic(ctx, j.topicNameForFanout(destination), msg)
		published = msg
	} else if topic, ok := j.adapter.(topicAdapter); ok && j.useTopicFanout(action) {
		// Topic publish fans out to every registered subscription.
		err = topic.PublishTopic(ctx, j.topicNameForFanout(destination), msg)
		published = msg
	} else {
		published, err = j.adapter.Publish(ctx, destination, msg)
	}
	if err != nil {
		return message{}, err
	}
	sent, *accepted = published, true
	result.BytesSent = int64(len(published.Body))
	if err := addMessageExtractions(result, published); err != nil {
		return message{}, err
	}
	result.Extractions["jms_operation"] = "publish"
	if published.SequenceNumber != 0 {
		// Scheduled (future-enqueue) send: surface the sequence so a later
		// jms_cancel_scheduled can reference it.
		result.Extractions["jms_scheduled_sequence"] = strconv.FormatInt(published.SequenceNumber, 10)
	}
	return published, evaluateAssertions(action.Assertions, result, published)
}

func (j *JMS) consume(ctx context.Context, action *protocol.Action, result *protocol.Result, received *bool) (observed message, operationErr error) {
	var delivery message
	defer func() {
		if *received {
			observed = delivery
		}
	}()
	destination := j.consumeDestination(action)
	if destination == "" {
		return message{}, fmt.Errorf("JMS destination is required")
	}
	result.DependencyTarget = destination
	// Advanced consume (dead-letter sub-queue or deferred settlement) requires a
	// settlementAdapter (Service Bus); fall back to the plain Consume otherwise.
	opts := consumeOptions{
		SubQueue:     normalizeSubQueue(paramString(action.Params, "sub_queue", "")),
		PeekLock:     boolParam(action.Params, "peek_lock", false),
		SessionToken: paramString(action.Params, "session_token", ""),
	}
	var (
		msg message
		err error
	)
	selectorExpr := paramString(action.Params, "selector", "")
	topic, isTopicCap := j.adapter.(topicAdapter)
	switch {
	case j.usesMockServiceBusTopic(action):
		serviceBusTopic, ok := j.adapter.(serviceBusMockTopicAdapter)
		if !ok {
			return message{}, fmt.Errorf("Service Bus mock topic adapter is unavailable")
		}
		sub := j.subscriptionName(action)
		if sub == "" {
			return message{}, fmt.Errorf("jms_consume from a topic requires a subscription (params.subscription or config.jms.subscription)")
		}
		if selectorExpr != "" {
			if _, cErr := compileSelector(selectorExpr); cErr != nil {
				result.StatusCode = 400
				return message{}, cErr
			}
		}
		topicName := j.topicNameForFanout(destination)
		msg, err = serviceBusTopic.ConsumeServiceBusTopic(ctx, topicName, sub, selectorExpr, j.timeout(action))
		if err == nil {
			msg.sourceTopic, msg.sourceSubscription = topicName, sub
		}
	case isTopicCap && j.useTopicFanout(action):
		// Topic fan-out: consume from a named subscription's independent queue.
		sub := j.subscriptionName(action)
		if sub == "" {
			return message{}, fmt.Errorf("jms_consume from a topic requires a subscription (params.subscription or config.jms.subscription)")
		}
		if selectorExpr != "" {
			if _, cErr := compileSelector(selectorExpr); cErr != nil {
				result.StatusCode = 400
				return message{}, cErr
			}
		}
		topicName := j.topicNameForFanout(destination)
		msg, err = topic.ConsumeTopic(ctx, topicName, sub, selectorExpr, j.isDurableSubscription(action), j.timeout(action))
		if err == nil {
			// Record the topic origin so a later rollback requeues to this
			// subscription rather than into the unrelated queue map.
			msg.sourceTopic, msg.sourceSubscription = topicName, sub
		}
	case selectorExpr != "":
		// Selector is incompatible with the Service Bus advanced-consume controls
		// (peek-lock settlement / sub-queue / session); reject the combo explicitly
		// rather than silently dropping one side.
		if opts.SubQueue != "" || opts.PeekLock || opts.SessionToken != "" {
			result.StatusCode = 400
			return message{}, fmt.Errorf("jms_consume selector cannot be combined with peek_lock, sub_queue, or session_token")
		}
		// A message selector requires a provider with data-plane selector support
		// (Solace/AMQP or the mock). Compile up front so a bad selector fails with a
		// clear 400 before any broker call.
		if _, cErr := compileSelector(selectorExpr); cErr != nil {
			result.StatusCode = 400
			return message{}, cErr
		}
		sc, ok := j.adapter.(selectiveConsumer)
		if !ok {
			return message{}, fmt.Errorf("jms_consume selector requires a provider with message-selector support (Solace/AMQP or the mock); Azure Service Bus uses subscription rules instead")
		}
		msg, err = sc.ConsumeSelective(ctx, destination, selectorExpr, j.timeout(action))
	case opts.SubQueue != "" || opts.PeekLock || opts.SessionToken != "":
		sb, ok := j.adapter.(settlementAdapter)
		if !ok {
			return message{}, fmt.Errorf("jms_consume sub_queue/peek_lock requires the Service Bus adapter (build -tags jms_servicebus_real)")
		}
		msg, err = sb.ConsumeAdvanced(ctx, destination, opts, j.timeout(action))
	default:
		msg, err = j.adapter.Consume(ctx, destination, j.timeout(action))
	}
	if err == nil || msg.received {
		delivery, *received = msg, true
		result.BytesReceived = int64(len(msg.Body))
	}
	if err != nil {
		result.StatusCode = 204
		return message{}, err
	}
	delivery, *received = msg, true
	result.BytesReceived = int64(len(msg.Body))
	if msg.Destination == "" || j.usesMockServiceBusTopic(action) {
		msg.Destination = destination
	}
	if err := addMessageExtractions(result, msg); err != nil {
		return message{}, err
	}
	result.Extractions["jms_operation"] = "consume"
	if msg.SettlementToken != "" {
		// Peek-locked: the script settles it later with jms_settle referencing this.
		if err := protocol.StoreExtraction(result, "jms_settlement_token", msg.SettlementToken); err != nil {
			result.Extractions = nil
			return message{}, fmt.Errorf("JMS settlement token extraction rejected: %w", err)
		}
	}
	if msg.SequenceNumber != 0 {
		result.Extractions["jms_sequence_number"] = strconv.FormatInt(msg.SequenceNumber, 10)
	}
	// The emitted RedeliveryCount telemetry is derived from the message marker in
	// finalizeJMSServerTelemetry; the session retains the message only for
	// settlement (ack/commit/rollback).
	if trackErr := j.session.TrackSized(msg, int64(messageSize(msg))); trackErr != nil {
		// The broker delivery has already been removed from the mock queue. Put it
		// back explicitly before returning the capacity error; silently forgetting
		// an unsettled delivery would turn a memory bound into message loss.
		if j.cfg.Mock {
			if requeueErr := j.requeueForRedelivery([]message{msg}); requeueErr != nil {
				trackErr = errors.Join(trackErr, fmt.Errorf("requeue rejected delivery: %w", requeueErr))
			}
		}
		result.StatusCode = 429
		return message{}, fmt.Errorf("retain JMS delivery for settlement: %w", trackErr)
	}
	// Reliability detection by broker sequence. A legitimate rollback-redelivery
	// carries the same sequence; it is tracked as a redelivery, NOT a broker
	// duplicate, so skip Observe for redelivered messages. Observe within the
	// subscription's own sequence space (empty for queue consumes): topic fan-out
	// delivers the SAME broker sequence to every subscription, so a single session
	// consuming two subscriptions of one topic would otherwise flag the second,
	// distinct delivery as a duplicate.
	j.linkConsumedTraceContext(msg, result)
	if msg.SequenceNumber > 0 && !isRedeliveredMessage(msg) {
		if dup, ooo := j.session.ObserveKeyed(msg.sourceSubscription, msg.SequenceNumber); dup || ooo {
			if dup {
				result.Extractions["jms_duplicate"] = "1"
			}
			if ooo {
				result.Extractions["jms_out_of_order"] = "1"
			}
		}
	}
	if err := runExtractors(action.Extractors, result, msg); err != nil {
		return message{}, err
	}
	return msg, evaluateAssertions(action.Assertions, result, msg)
}

// browse performs a non-destructive JMS QueueBrowser read (P2.3). It returns the
// first matching message (for extraction/assertion) plus a jms_browse_count of
// all matches, and never settles or tracks the messages.
func (j *JMS) browse(ctx context.Context, action *protocol.Action, result *protocol.Result) (message, bool, error) {
	destination := j.consumeDestination(action)
	result.DependencyTarget = destination
	selectorExpr := paramString(action.Params, "selector", "")
	if selectorExpr != "" {
		if _, cErr := compileSelector(selectorExpr); cErr != nil {
			result.StatusCode = 400
			return message{}, false, cErr
		}
	}
	max := 10
	if m, ok := intParam(action.Params, "max"); ok && m > 0 {
		max = m
	}
	if max > MaxJMSBrowseMessages {
		return message{}, false, fmt.Errorf("jms_browse max must not exceed %d", MaxJMSBrowseMessages)
	}
	var (
		msgs []message
		err  error
	)
	if j.usesMockServiceBusTopic(action) {
		topic, ok := j.adapter.(serviceBusMockTopicAdapter)
		if !ok {
			return message{}, false, fmt.Errorf("Service Bus mock topic adapter is unavailable")
		}
		sub := j.subscriptionName(action)
		if sub == "" {
			return message{}, false, fmt.Errorf("jms_browse on a topic requires a subscription (params.subscription or config.jms.subscription)")
		}
		msgs, err = topic.BrowseServiceBusTopic(ctx, j.topicNameForFanout(destination), sub, selectorExpr, max, j.timeout(action))
	} else if topic, ok := j.adapter.(topicAdapter); ok && j.useTopicFanout(action) {
		sub := j.subscriptionName(action)
		if sub == "" {
			return message{}, false, fmt.Errorf("jms_browse on a topic requires a subscription (params.subscription or config.jms.subscription)")
		}
		msgs, err = topic.BrowseTopic(ctx, j.topicNameForFanout(destination), sub, selectorExpr, max, j.timeout(action))
	} else {
		br, ok := j.adapter.(messageBrowser)
		if !ok {
			return message{}, false, fmt.Errorf("jms_browse requires a provider with browse support (Solace/AMQP, Azure Service Bus, or the mock)")
		}
		msgs, err = br.Browse(ctx, destination, selectorExpr, max, j.timeout(action))
	}
	if err != nil {
		result.StatusCode = 204
		return message{}, false, err
	}
	result.Extractions["jms_operation"] = "browse"
	result.Extractions["jms_browse_count"] = strconv.Itoa(len(msgs))
	if len(msgs) == 0 {
		return message{}, false, nil
	}
	first := msgs[0]
	if first.Destination == "" || j.usesMockServiceBusTopic(action) {
		first.Destination = destination
	}
	result.BytesReceived = int64(len(first.Body))
	if err := addMessageExtractions(result, first); err != nil {
		return message{}, false, err
	}
	// Non-destructive: do NOT Track for settlement.
	if err := runExtractors(action.Extractors, result, first); err != nil {
		return message{}, false, err
	}
	return first, true, evaluateAssertions(action.Assertions, result, first)
}

// settle handles jms_ack / jms_commit / jms_rollback against the per-VUser
// session, replacing the former no-ops. ack settles client-acknowledged
// messages; commit/rollback drive the transaction with commit/abort accounting,
// and rollback requeues pending messages for redelivery on the in-memory mock
// broker. Real-adapter broker settlement (Service Bus complete/abandon, Solace
// client-ack) is wired per provider in later work; the accounting is shared.
func (j *JMS) settle(action *protocol.Action, result *protocol.Result) error {
	switch strings.ToLower(action.Type) {
	case "jms_ack":
		settled := j.session.Ack()
		result.Extractions["jms_operation"] = "ack"
		result.Extractions["jms_settled"] = strconv.Itoa(len(settled))
	case "jms_commit":
		settled := j.session.Commit()
		result.Extractions["jms_operation"] = "commit"
		result.Extractions["jms_settled"] = strconv.Itoa(len(settled))
		// Control op: carry only the reliability counter so it does not dilute the
		// averaged transfer/latency telemetry (see ServerTelemetry.HasMeasuredSample).
		result.ServerTelemetry = &protocol.ServerTelemetry{CommitCount: 1}
	case "jms_rollback":
		toRedeliver := j.session.Rollback()
		if j.cfg.Mock {
			if err := j.requeueForRedelivery(toRedeliver); err != nil {
				return err
			}
		}
		result.Extractions["jms_operation"] = "rollback"
		result.Extractions["jms_redelivered_scheduled"] = strconv.Itoa(len(toRedeliver))
		result.ServerTelemetry = &protocol.ServerTelemetry{AbortCount: 1}
	}
	return nil
}

// settleAdvanced handles the Service Bus client-settlement verbs (jms_settle,
// jms_renew_lock). They require a settlementAdapter (only the real Service Bus
// adapter implements it), so on any other provider/build they fail closed with
// an actionable message rather than silently succeeding.
func (j *JMS) settleAdvanced(ctx context.Context, action *protocol.Action, result *protocol.Result) error {
	sb, ok := j.adapter.(settlementAdapter)
	if !ok {
		return fmt.Errorf("%s requires the Azure Service Bus adapter (build -tags jms_servicebus_real)", action.Type)
	}
	token := paramString(action.Params, "settlement_token", "")
	if token == "" {
		return fmt.Errorf("%s requires params.settlement_token (from a peek_lock jms_consume)", action.Type)
	}
	switch strings.ToLower(action.Type) {
	case "jms_renew_lock":
		if err := sb.RenewLock(ctx, token); err != nil {
			return err
		}
		result.Extractions["jms_operation"] = "renew_lock"
		return nil
	case "jms_settle":
		disposition := normalizeDisposition(paramString(action.Params, "disposition", "complete"))
		opts := settleOptions{
			DeadLetterReason:      paramString(action.Params, "dead_letter_reason", ""),
			DeadLetterDescription: paramString(action.Params, "dead_letter_description", ""),
		}
		started := time.Now()
		if err := sb.Settle(ctx, token, disposition, opts); err != nil {
			return err
		}
		elapsed := float64(time.Since(started)) / float64(time.Millisecond)
		result.ServerTelemetry = &protocol.ServerTelemetry{AcknowledgementMs: &elapsed, Messaging: &protocol.MessagingDiagnostics{Version: protocol.MessagingEvidenceVersion, Durations: []protocol.MessagingDurationObservation{{Metric: "settlement", ValueMs: &elapsed}}}}
		result.Extractions["jms_operation"] = "settle"
		result.Extractions["jms_disposition"] = disposition
		// Dead-lettering is a reliability event; carry the counter (control-op style).
		if disposition == "dead_letter" {
			result.ServerTelemetry.DeadLetteredCount = 1
		}
		return nil
	}
	return fmt.Errorf("unsupported settlement action %q", action.Type)
}

// normalizeSubQueue maps the DSL sub_queue enum to the canonical token; "" / "none"
// mean the main entity.
func normalizeSubQueue(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "dead_letter":
		return "dead_letter"
	case "transfer_dead_letter":
		return "transfer_dead_letter"
	default:
		return ""
	}
}

// normalizeDisposition canonicalizes the jms_settle disposition, defaulting to
// complete.
func normalizeDisposition(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "abandon":
		return "abandon"
	case "dead_letter":
		return "dead_letter"
	case "defer":
		return "defer"
	default:
		return "complete"
	}
}

// sessionAdvanced handles the Service Bus session, deferred-receive, and
// scheduled-cancel verbs. Like settleAdvanced it requires a settlementAdapter and
// fails closed elsewhere. It returns the received message (for jms_receive_deferred)
// so the standard telemetry/assertion path applies.
func (j *JMS) sessionAdvanced(ctx context.Context, action *protocol.Action, result *protocol.Result) (message, bool, error) {
	sb, ok := j.adapter.(settlementAdapter)
	if !ok {
		return message{}, false, fmt.Errorf("%s requires the Azure Service Bus adapter (build -tags jms_servicebus_real)", action.Type)
	}
	switch strings.ToLower(action.Type) {
	case "jms_session_accept":
		destination := j.consumeDestination(action)
		if destination == "" {
			return message{}, false, fmt.Errorf("jms_session_accept requires a destination")
		}
		token, resolved, err := sb.AcceptSession(ctx, destination, paramString(action.Params, "session_id", ""))
		if err != nil {
			return message{}, false, err
		}
		for key, value := range map[string]string{
			"jms_operation": "session_accept", "jms_session_token": token, "jms_session_id": resolved,
		} {
			if err := protocol.StoreExtraction(result, key, value); err != nil {
				result.Extractions = nil
				return message{}, false, fmt.Errorf("JMS session extraction %q rejected: %w", key, err)
			}
		}
		return message{}, false, nil
	case "jms_session_state":
		token := paramString(action.Params, "session_token", "")
		if token == "" {
			return message{}, false, fmt.Errorf("jms_session_state requires params.session_token")
		}
		var state []byte
		set := false
		if raw := paramString(action.Params, "session_state", ""); raw != "" {
			state = []byte(raw)
			set = true
		}
		got, err := sb.SessionState(ctx, token, state, set)
		if err != nil {
			return message{}, false, err
		}
		result.Extractions["jms_operation"] = "session_state"
		if set {
			result.Extractions["jms_session_state_set"] = "true"
		} else {
			if len(got) > protocol.MaxExtractionValueBytes {
				return message{}, false, fmt.Errorf("JMS session state extraction exceeds %d bytes", protocol.MaxExtractionValueBytes)
			}
			if err := protocol.StoreExtraction(result, "jms_session_state", string(got)); err != nil {
				result.Extractions = nil
				return message{}, false, fmt.Errorf("JMS session state extraction rejected: %w", err)
			}
		}
		return message{}, false, nil
	case "jms_session_close":
		token := paramString(action.Params, "session_token", "")
		if token == "" {
			return message{}, false, fmt.Errorf("jms_session_close requires params.session_token")
		}
		if err := sb.CloseSession(ctx, token); err != nil {
			return message{}, false, err
		}
		result.Extractions["jms_operation"] = "session_close"
		return message{}, false, nil
	case "jms_receive_deferred":
		destination := j.consumeDestination(action)
		seqs, err := parseSequenceNumbers(action.Params["sequence_numbers"])
		if err != nil {
			return message{}, false, err
		}
		if len(seqs) == 0 {
			return message{}, false, fmt.Errorf("jms_receive_deferred requires params.sequence_numbers")
		}
		msgs, err := sb.ReceiveDeferred(ctx, destination, seqs, j.timeout(action))
		if err != nil {
			return message{}, false, err
		}
		result.Extractions["jms_operation"] = "receive_deferred"
		result.Extractions["jms_deferred_received"] = strconv.Itoa(len(msgs))
		if len(msgs) == 0 {
			return message{}, false, nil
		}
		first := msgs[0]
		result.BytesReceived = int64(len(first.Body))
		if err := addMessageExtractions(result, first); err != nil {
			return message{}, false, err
		}
		if first.SettlementToken != "" {
			if err := protocol.StoreExtraction(result, "jms_settlement_token", first.SettlementToken); err != nil {
				result.Extractions = nil
				return message{}, false, fmt.Errorf("JMS settlement token extraction rejected: %w", err)
			}
		}
		return first, true, evaluateAssertions(action.Assertions, result, first)
	case "jms_cancel_scheduled":
		destination := j.destination(action)
		if destination == "" {
			return message{}, false, fmt.Errorf("jms_cancel_scheduled requires a destination")
		}
		seqs, err := parseSequenceNumbers(action.Params["sequence_numbers"])
		if err != nil {
			return message{}, false, err
		}
		if len(seqs) == 0 {
			return message{}, false, fmt.Errorf("jms_cancel_scheduled requires params.sequence_numbers")
		}
		if err := sb.CancelScheduled(ctx, destination, seqs); err != nil {
			return message{}, false, err
		}
		result.Extractions["jms_operation"] = "cancel_scheduled"
		result.Extractions["jms_cancelled"] = strconv.Itoa(len(seqs))
		return message{}, false, nil
	}
	return message{}, false, fmt.Errorf("unsupported session action %q", action.Type)
}

// parseSequenceNumbers parses a positive sequence-number list from a DSL param:
// a JSON array of numbers ([]any of float64/int/json.Number), one number, or a
// comma-separated string.
func parseSequenceNumbers(raw any) ([]int64, error) {
	if raw == nil {
		return nil, nil
	}
	var out []int64
	switch v := raw.(type) {
	case []int64:
		if len(v) > MaxJMSSequenceNumbers {
			return nil, fmt.Errorf("sequence_numbers must not exceed %d entries", MaxJMSSequenceNumbers)
		}
		for _, item := range v {
			if item <= 0 {
				return nil, fmt.Errorf("invalid sequence_numbers entry %d: must be greater than zero", item)
			}
		}
		return append([]int64(nil), v...), nil
	case []any:
		if len(v) > MaxJMSSequenceNumbers {
			return nil, fmt.Errorf("sequence_numbers must not exceed %d entries", MaxJMSSequenceNumbers)
		}
		for _, item := range v {
			n, err := positiveSequenceNumber(item)
			if err != nil {
				return nil, fmt.Errorf("invalid sequence_numbers entry %v: %w", item, err)
			}
			out = append(out, n)
		}
	case string:
		if len(v) > 64<<10 {
			return nil, fmt.Errorf("sequence_numbers string exceeds %d bytes", 64<<10)
		}
		for len(v) > 0 {
			part := v
			if before, after, found := strings.Cut(v, ","); found {
				part, v = before, after
			} else {
				v = ""
			}
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			n, err := positiveSequenceNumber(part)
			if err != nil {
				return nil, fmt.Errorf("invalid sequence_numbers entry %q: %w", part, err)
			}
			out = append(out, n)
			if len(out) > MaxJMSSequenceNumbers {
				return nil, fmt.Errorf("sequence_numbers must not exceed %d entries", MaxJMSSequenceNumbers)
			}
		}
	default:
		n, err := positiveSequenceNumber(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid sequence_numbers entry %v: %w", raw, err)
		}
		out = append(out, n)
	}
	return out, nil
}

func positiveSequenceNumber(value any) (int64, error) {
	n, err := toInt64(value)
	if err != nil {
		return 0, err
	}
	if n <= 0 {
		return 0, fmt.Errorf("must be greater than zero")
	}
	return n, nil
}

// ValidateSequenceNumbers exposes the runtime parser's exact acceptance
// contract to the DSL preflight validator without exporting the parsed values.
// Keeping one parser prevents drafts from passing validation only to fail when
// the Service Bus action executes.
func ValidateSequenceNumbers(raw any) (int, error) {
	values, err := parseSequenceNumbers(raw)
	return len(values), err
}

func toInt64(v any) (int64, error) {
	switch n := v.(type) {
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) || math.Trunc(n) != n || n < math.MinInt64 || n >= math.MaxInt64 {
			return 0, fmt.Errorf("not an integer")
		}
		return int64(n), nil
	case int:
		return int64(n), nil
	case int64:
		return n, nil
	case json.Number:
		return n.Int64()
	case string:
		return strconv.ParseInt(strings.TrimSpace(n), 10, 64)
	default:
		return 0, fmt.Errorf("not a number: %T", v)
	}
}

// requeueForRedelivery re-publishes rolled-back messages to the mock broker with
// the redelivered marker set so the next consume reports a redelivery.
func (j *JMS) requeueForRedelivery(msgs []message) error {
	for _, msg := range msgs {
		if msg.Destination == "" {
			continue
		}
		props := make(map[string]string, len(msg.Properties)+1)
		for k, v := range msg.Properties {
			props[k] = v
		}
		props[redeliveredProperty] = "true"
		msg.Properties = props
		if msg.sourceTopic != "" {
			// Topic-origin message: requeue to the originating subscription's queue.
			// broker.publish would land it in the unrelated queue map (keyed by the
			// topic name), where the topic consume path never looks, so it would
			// never be redelivered.
			if err := broker.requeueTopic(mockJMSKey(j.mockNamespace, msg.sourceTopic), msg.sourceSubscription, msg); err != nil {
				return err
			}
			continue
		}
		if err := broker.publish(mockJMSKey(j.mockNamespace, msg.Destination), msg); err != nil {
			return err
		}
	}
	return nil
}

func (j *JMS) requestReply(ctx context.Context, action *protocol.Action, result *protocol.Result, sentOK, receivedOK, attempted *bool) (observedRequest, observedResponse message, operationErr error) {
	var sentObservation, delivery message
	defer func() {
		if *sentOK {
			observedRequest = sentObservation
		}
		if *receivedOK {
			observedResponse = delivery
		}
	}()
	requestDestination := j.destination(action)
	if requestDestination == "" {
		return message{}, message{}, fmt.Errorf("JMS request destination is required")
	}
	replyDestination := paramString(action.Params, "reply_to", j.cfg.ReplyTo)
	replyDestination = j.serviceBusConsumeDestination(action, replyDestination)
	if replyDestination == "" {
		return message{}, message{}, fmt.Errorf("JMS reply_to destination is required")
	}

	request := j.buildMessage(action, requestDestination)
	if request.CorrelationID == "" {
		request.CorrelationID = request.ID
	}
	j.injectTraceContext(ctx, &request, result)
	if err := validateJMSMessageBounds(request); err != nil {
		return message{}, message{}, err
	}
	var sent, response message
	var err error
	if topic, ok := j.adapter.(topicRequestReplyAdapter); ok && j.usesMockServiceBusTopic(action) {
		subscription := paramString(action.Params, "subscription", j.cfg.Subscription)
		_, subscription = j.mockTopicCoordinates(replyDestination, subscription)
		if subscription == "" {
			return message{}, message{}, fmt.Errorf("jms_request_reply on a topic requires a subscription (params.subscription or config.jms.subscription)")
		}
		sent, response, err = topic.RequestReplyTopic(
			ctx,
			j.topicNameForFanout(requestDestination),
			j.topicNameForFanout(replyDestination),
			subscription,
			request,
			j.timeout(action),
		)
	} else {
		sent, response, err = j.adapter.RequestReply(ctx, requestDestination, replyDestination, request, j.timeout(action))
	}
	*attempted = err == nil || sent.sent || sent.sendAttempted
	if err == nil || sent.sent {
		sentObservation, *sentOK = sent, true
		result.BytesSent = int64(len(sent.Body))
	}
	if err == nil || response.received {
		delivery, *receivedOK = response, true
		result.BytesReceived = int64(len(response.Body))
	}
	if err != nil {
		result.StatusCode = 204
		return message{}, message{}, err
	}
	result.BytesSent = int64(len(sent.Body))
	result.BytesReceived = int64(len(response.Body))
	if err := addMessageExtractions(result, response); err != nil {
		return message{}, message{}, err
	}
	if err := protocol.StoreExtraction(result, "jms_request_message_id", sent.ID); err != nil {
		result.Extractions = nil
		return message{}, message{}, fmt.Errorf("JMS request ID extraction rejected: %w", err)
	}
	result.Extractions["jms_operation"] = "request_reply"
	if err := runExtractors(action.Extractors, result, response); err != nil {
		return message{}, message{}, err
	}
	return sent, response, evaluateAssertions(action.Assertions, result, response)
}

func (j *JMS) timeout(action *protocol.Action) time.Duration {
	timeout := durationParam(action.Params, "timeout_ms", j.cfg.Timeout, MaxConfiguredTimeout)
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return timeout
}

func (j *JMS) buildMessage(action *protocol.Action, destination string) message {
	correlationID := paramString(action.Params, "correlation_id", "")
	properties := cloneStringMap(j.cfg.Properties)
	// cloneStringMap returns nil for an empty config; writing action metadata into
	// a nil map panics and crashes the generator. Allocate when metadata is present
	// (mirrors kafka.buildRecord).
	if properties == nil && len(action.Metadata) > 0 {
		properties = make(map[string]string)
	}
	for k, v := range action.Metadata {
		if metadataKeyIsProvenance(k) {
			continue
		}
		if s, ok := v.(string); ok {
			properties[k] = s
		}
	}
	id := paramString(action.Params, "message_id", "")
	if id == "" {
		id = uuid.NewString()
	}
	msg := message{
		ID:            id,
		CorrelationID: correlationID,
		Destination:   destination,
		Headers:       cloneStringMap(action.Headers),
		Properties:    properties,
		Body:          action.Body,
		CreatedAt:     time.Now(),
		// Service Bus advanced send fields (P2.4); ignored by providers that don't
		// support them.
		SessionID:    paramString(action.Params, "session_id", ""),
		PartitionKey: paramString(action.Params, "partition_key", ""),
		TimeToLive:   durationParam(action.Params, "time_to_live_ms", 0, MaxConfiguredMessageDuration),
		// JMS message-type + delivery controls (P2.3). delivery_mode falls back to
		// the connection default (closing the previously-dead config.jms.delivery_mode).
		Type:         strings.ToLower(paramString(action.Params, "message_type", "")),
		DeliveryMode: strings.ToLower(paramString(action.Params, "delivery_mode", j.cfg.DeliveryMode)),
	}
	if p, ok := intParam(action.Params, "priority"); ok {
		if p < 0 {
			p = 0
		}
		if p > 9 {
			p = 9
		}
		pr := uint8(p)
		msg.Priority = &pr
	}
	applyTypedBody(&msg, action.Params)
	if delay := durationParam(action.Params, "scheduled_enqueue_ms", 0, MaxConfiguredMessageDuration); delay > 0 {
		msg.ScheduledEnqueueAt = msg.CreatedAt.Add(delay)
	}
	return msg
}

func metadataKeyIsProvenance(key string) bool {
	return messaging.MetadataKeyIsProvenance(messaging.FamilyJMS, key)
}

// applyTypedBody fills the structured map/stream payload for a MapMessage or
// StreamMessage and JSON-encodes it into Body (when an explicit body was not
// already supplied) so the mock broker, sidecar, and extractors all observe a
// single canonical wire form. Text/Bytes/Object messages keep the raw Body.
func applyTypedBody(msg *message, params map[string]any) {
	switch msg.Type {
	case "map":
		if mapBody := stringMapParam(params, "map_body"); len(mapBody) > 0 {
			msg.MapBody = mapBody
			if len(msg.Body) == 0 {
				if encoded, err := json.Marshal(mapBody); err == nil {
					msg.Body = encoded
				}
			}
		}
	case "stream":
		if streamBody := stringSliceParam(params, "stream_body"); len(streamBody) > 0 {
			msg.StreamBody = streamBody
			if len(msg.Body) == 0 {
				if encoded, err := json.Marshal(streamBody); err == nil {
					msg.Body = encoded
				}
			}
		}
	}
}

func (j *JMS) destination(action *protocol.Action) string {
	return paramString(action.Params, "destination", protocol.FirstNonEmpty(action.Target, j.cfg.Destination))
}

func (j *JMS) consumeDestination(action *protocol.Action) string {
	return j.serviceBusConsumeDestination(action, j.destination(action))
}

// useTopicFanout reports whether this action routes through the topic fan-out
// capability. Real Service Bus uses its native composed subscription address;
// Service Bus mock mode uses the in-memory fan-out path so publish/consume and
// browse exercise actual topic/subscription semantics instead of mismatched
// queue keys.
func (j *JMS) useTopicFanout(action *protocol.Action) bool {
	if strings.EqualFold(strings.TrimSpace(j.provider), "servicebus") && !j.cfg.Mock {
		return false
	}
	destType := paramString(action.Params, "destination_type", j.cfg.DestinationType)
	return strings.EqualFold(strings.TrimSpace(destType), "topic")
}

// subscriptionName resolves the topic subscription identifier (params.subscription
// then config.jms.subscription).
func (j *JMS) subscriptionName(action *protocol.Action) string {
	subscription := paramString(action.Params, "subscription", j.cfg.Subscription)
	if j.cfg.Mock && strings.EqualFold(strings.TrimSpace(j.provider), "servicebus") {
		_, subscription = j.mockTopicCoordinates(j.consumeDestination(action), subscription)
	}
	return subscription
}

// isDurableSubscription reports whether subscription_mode requests a durable
// subscription (applied by the AMQP/Solace adapter; the mock retains either way).
func (j *JMS) isDurableSubscription(action *protocol.Action) bool {
	return strings.EqualFold(strings.TrimSpace(paramString(action.Params, "subscription_mode", "")), "durable")
}

func (j *JMS) serviceBusConsumeDestination(action *protocol.Action, destination string) string {
	if !strings.EqualFold(strings.TrimSpace(j.provider), "servicebus") {
		return destination
	}
	destinationType := paramString(action.Params, "destination_type", j.cfg.DestinationType)
	subscription := paramString(action.Params, "subscription", j.cfg.Subscription)
	return serviceBusConsumeDestination(destination, destinationType, subscription)
}

func (j *JMS) usesMockServiceBusTopic(action *protocol.Action) bool {
	return j.cfg.Mock && strings.EqualFold(strings.TrimSpace(j.provider), "servicebus") && j.useTopicFanout(action)
}

// mockTopicCoordinates converts a Service Bus composed subscription address
// back into the base topic + subscription used by the in-memory broker. Other
// JMS providers already carry these as separate values.
func (j *JMS) mockTopicCoordinates(destination, subscription string) (string, string) {
	destination = strings.TrimSpace(destination)
	subscription = strings.TrimSpace(subscription)
	if j.cfg.Mock && strings.EqualFold(strings.TrimSpace(j.provider), "servicebus") {
		if topic, embeddedSubscription, ok := splitServiceBusSubscription(destination); ok {
			destination = topic
			// A composed Service Bus entity is authoritative. The real adapter
			// likewise ignores a separate subscription value once the destination
			// already names /subscriptions/{name}.
			subscription = embeddedSubscription
		}
	}
	return destination, subscription
}

func (j *JMS) topicNameForFanout(destination string) string {
	topic, _ := j.mockTopicCoordinates(destination, "")
	return topic
}

func addMessageExtractions(result *protocol.Result, msg message) error {
	store := func(key, value string) error {
		if err := protocol.StoreExtraction(result, key, value); err != nil {
			result.Extractions = nil
			return fmt.Errorf("JMS metadata extraction %q rejected: %w", key, err)
		}
		return nil
	}
	for _, entry := range []struct{ key, value string }{
		{"jms_message_id", msg.ID},
		{"jms_correlation_id", msg.CorrelationID},
		{"jms_destination", msg.Destination},
	} {
		if err := store(entry.key, entry.value); err != nil {
			return err
		}
	}
	body := msg.Body
	truncated := len(body) > protocol.MaxExtractionValueBytes
	if truncated {
		body = body[:protocol.MaxExtractionValueBytes]
	}
	if err := store("jms_body", string(body)); err != nil {
		return err
	}
	if truncated {
		if err := store("jms_body_truncated", "true"); err != nil {
			return err
		}
	}
	if msg.Type != "" {
		if err := store("jms_message_type", msg.Type); err != nil {
			return err
		}
	}
	if reason, ok := firstMatchingMapValue(msg.Properties, "dead_letter_reason", "dlq_reason", "x-dead-letter-reason"); ok {
		if err := store("jms_dead_letter_reason", reason); err != nil {
			return err
		}
	}
	if deliveryCount, ok := firstMatchingMapValue(msg.Properties, "delivery_count", "x-delivery-count", "JMSXDeliveryCount"); ok {
		if err := store("jms_delivery_count", deliveryCount); err != nil {
			return err
		}
	}
	result.CorrelationID = msg.CorrelationID
	return nil
}

// isRedeliveredMessage reports whether a consumed message is a redelivery — a
// rolled-back message re-consumed (redeliveredProperty) or a broker delivery_count
// greater than one — which must be counted as a redelivery, not a duplicate.
func isRedeliveredMessage(msg message) bool {
	if msg.Properties[redeliveredProperty] == "true" {
		return true
	}
	if dc, ok := firstMatchingMapValue(msg.Properties, "delivery_count", "x-delivery-count", "JMSXDeliveryCount"); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(dc)); err == nil && n > 1 {
			return true
		}
	}
	return false
}

func finalizeJMSServerTelemetry(result *protocol.Result, request message, hasRequest bool, response message, hasResponse bool) *protocol.ServerTelemetry {
	if result == nil || (!hasRequest && !hasResponse) {
		return nil
	}
	telemetry := &protocol.ServerTelemetry{
		TransferMs: durationToNonNegativeMilliseconds(result.Duration),
		Messaging:  &protocol.MessagingDiagnostics{Version: protocol.MessagingEvidenceVersion},
	}
	if hasRequest {
		telemetry.MessagesSent = 1
		telemetry.Messaging.Durations = append(telemetry.Messaging.Durations, protocol.MessagingDurationObservation{Metric: "producer_ack", UnavailableReason: "not_observed"})
	}
	if hasResponse {
		telemetry.MessagesReceived = 1
		observedAt := result.StartTime.Add(result.Duration)
		if !response.CreatedAt.IsZero() && !observedAt.Before(response.CreatedAt) {
			telemetry.ConsumerLagMs = durationToNonNegativeMilliseconds(observedAt.Sub(response.CreatedAt))
			telemetry.ConsumerLagObserved = true
		}
		ageReason := "not_observed"
		if telemetry.ConsumerLagObserved {
			ageReason = "clock_unqualified"
		}
		telemetry.Messaging.Durations = append(telemetry.Messaging.Durations, protocol.MessagingDurationObservation{Metric: "enqueue_age", UnavailableReason: ageReason})
		if response.Properties[redeliveredProperty] == "true" {
			telemetry.RedeliveryCount = 1
		}
		if result.Extractions["jms_duplicate"] == "1" {
			telemetry.DuplicateCount = 1
		}
		if result.Extractions["jms_out_of_order"] == "1" {
			telemetry.OutOfOrderCount = 1
		}
	}
	if hasRequest {
		telemetry.RequestPayloadKB = bytesToKilobytes(int64(len(request.Body)))
		telemetry.RequestSizeClass = protocol.ClassifyPayloadSize(int64(len(request.Body)))
	} else if result.BytesSent > 0 {
		telemetry.RequestPayloadKB = bytesToKilobytes(result.BytesSent)
		telemetry.RequestSizeClass = protocol.ClassifyPayloadSize(result.BytesSent)
	}
	if hasResponse {
		telemetry.ResponsePayloadKB = bytesToKilobytes(int64(len(response.Body)))
		telemetry.ResponseSizeClass = protocol.ClassifyPayloadSize(int64(len(response.Body)))
	} else if result.BytesReceived > 0 {
		telemetry.ResponsePayloadKB = bytesToKilobytes(result.BytesReceived)
		telemetry.ResponseSizeClass = protocol.ClassifyPayloadSize(result.BytesReceived)
	}
	if hasRequest && hasResponse && !request.CreatedAt.IsZero() && !response.CreatedAt.IsZero() && !response.CreatedAt.Before(request.CreatedAt) {
		serverProcessing := response.CreatedAt.Sub(request.CreatedAt)
		telemetry.ServerProcessingMs = durationToNonNegativeMilliseconds(serverProcessing)
		telemetry.MessageLatencyMs = durationToNonNegativeMilliseconds(serverProcessing)
		telemetry.TransferMs = durationToNonNegativeMilliseconds(result.Duration - serverProcessing)
	}
	telemetry.MessagesSentPerSec = messagesPerSecond(telemetry.MessagesSent, result.Duration)
	telemetry.MessagesReceivedPerSec = messagesPerSecond(telemetry.MessagesReceived, result.Duration)
	if (hasRequest || hasResponse) && result.Extractions["jms_dead_letter"] == "true" {
		telemetry.DeadLetteredCount = 1
	}
	return telemetry
}

func durationToNonNegativeMilliseconds(duration time.Duration) float64 {
	if duration <= 0 {
		return 0
	}
	return float64(duration.Microseconds()) / 1000.0
}

func bytesToKilobytes(bytes int64) float64 {
	if bytes <= 0 {
		return 0
	}
	return float64(bytes) / 1024.0
}

func messagesPerSecond(count int, duration time.Duration) float64 {
	if count <= 0 || duration <= 0 {
		return 0
	}
	return float64(count) / duration.Seconds()
}

func classifyJMSDependencyType(defaultDestinationType string, action *protocol.Action, deadLetter bool) string {
	destinationType := strings.TrimSpace(paramString(action.Params, "destination_type", defaultDestinationType))
	if destinationType == "" {
		destinationType = "queue"
	}
	if deadLetter {
		return "jms_dead_letter_" + strings.ToLower(destinationType)
	}
	return "jms_" + strings.ToLower(destinationType)
}

func (j *JMS) isDeadLetterAction(action *protocol.Action, destination string) bool {
	if boolParam(action.Params, "dead_letter", false) {
		return true
	}
	for _, candidate := range j.cfg.DeadLetterDestinations {
		if strings.EqualFold(strings.TrimSpace(candidate), strings.TrimSpace(destination)) {
			return true
		}
	}
	return false
}

func stringValue(cfg map[string]any, key string) string {
	value, _ := cfg[key].(string)
	return value
}

func boolValue(cfg map[string]any, key string) bool {
	value, _ := cfg[key].(bool)
	return value
}

func boundedJMSInteger(value any, fallback, minimum, maximum int) (int, error) {
	if value == nil {
		return fallback, nil
	}
	parsed, err := toInt64(value)
	if err != nil || parsed < int64(minimum) || parsed > int64(maximum) {
		return 0, fmt.Errorf("must be an integer between %d and %d", minimum, maximum)
	}
	return int(parsed), nil
}

func durationValue(cfg map[string]any, key string, fallback, maximum time.Duration) time.Duration {
	fallbackMillis := int(fallback / time.Millisecond)
	maximumMillis := int(maximum / time.Millisecond)
	value, err := boundedJMSInteger(cfg[key], fallbackMillis, 0, maximumMillis)
	if err != nil {
		return fallback
	}
	return time.Duration(value) * time.Millisecond
}

func stringMapValue(cfg map[string]any, key string) map[string]string {
	value, ok := cfg[key].(map[string]string)
	if !ok {
		return nil
	}
	return cloneStringMap(value)
}

func paramString(params map[string]any, key string, fallback string) string {
	if params == nil {
		return fallback
	}
	value, ok := params[key].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func durationParam(params map[string]any, key string, fallback, maximum time.Duration) time.Duration {
	if params == nil {
		return fallback
	}
	return durationValue(params, key, fallback, maximum)
}

func boolParam(params map[string]any, key string, fallback bool) bool {
	if params == nil {
		return fallback
	}
	value, ok := params[key].(bool)
	if !ok {
		return fallback
	}
	return value
}

// intParam coerces an int-like param (int/int64/float64, or a numeric string)
// and reports whether the key was present and parseable.
func intParam(params map[string]any, key string) (int, bool) {
	if params == nil {
		return 0, false
	}
	switch v := params[key].(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n, true
		}
	}
	return 0, false
}

// stringMapParam reads an object param (map[string]any) and stringifies its
// values for a JMS MapMessage body.
func stringMapParam(params map[string]any, key string) map[string]string {
	if params == nil {
		return nil
	}
	raw, ok := params[key].(map[string]any)
	if !ok || len(raw) == 0 {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		out[k] = anyToString(v)
	}
	return out
}

// stringSliceParam reads an array param ([]any or []string) and stringifies its
// elements for a JMS StreamMessage body.
func stringSliceParam(params map[string]any, key string) []string {
	if params == nil {
		return nil
	}
	switch raw := params[key].(type) {
	case []string:
		return raw
	case []any:
		out := make([]string, 0, len(raw))
		for _, v := range raw {
			out = append(out, anyToString(v))
		}
		return out
	}
	return nil
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func stringSliceValue(cfg map[string]any, key string) []string {
	raw, ok := cfg[key]
	if !ok {
		return nil
	}
	switch value := raw.(type) {
	case []string:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if trimmed := strings.TrimSpace(item); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if itemStr, ok := item.(string); ok {
				if trimmed := strings.TrimSpace(itemStr); trimmed != "" {
					out = append(out, trimmed)
				}
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case string:
		parts := strings.Split(value, ",")
		out := make([]string, 0, len(parts))
		for _, item := range parts {
			if trimmed := strings.TrimSpace(item); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	default:
		return nil
	}
}

func firstMatchingMapValue(values map[string]string, keys ...string) (string, bool) {
	for _, key := range keys {
		if value, ok := values[key]; ok && strings.TrimSpace(value) != "" {
			return value, true
		}
	}
	return "", false
}

func lookupString(key string, values map[string]string, label string) (string, error) {
	value, ok := values[key]
	if !ok {
		return "", fmt.Errorf("JMS %s %q not found", label, key)
	}
	return value, nil
}

// injectTraceContext writes the message's W3C properties when trace_carrier is
// on; Service Bus consumers built on the Azure SDK also read Diagnostic-Id.
func (j *JMS) injectTraceContext(ctx context.Context, msg *message, result *protocol.Result) {
	if !j.traceCarrier || msg == nil {
		return
	}
	if msg.Properties == nil {
		msg.Properties = make(map[string]string)
	}
	result.TraceContext = protocol.InjectMessageTraceContext(ctx, msg.Properties, strings.EqualFold(strings.TrimSpace(j.provider), "servicebus"))
}

// linkConsumedTraceContext archives a delivery's producer context, marking
// redeliveries with the broker's delivery count when it reports one.
func (j *JMS) linkConsumedTraceContext(msg message, result *protocol.Result) {
	if !j.traceCarrier {
		return
	}
	kind, count := protocol.TraceLinkConsumed, 0
	if isRedeliveredMessage(msg) {
		kind = protocol.TraceLinkRedelivery
		if dc, ok := firstMatchingMapValue(msg.Properties, "delivery_count", "x-delivery-count", "JMSXDeliveryCount"); ok {
			count, _ = strconv.Atoi(strings.TrimSpace(dc))
		}
	}
	props := msg.Properties
	if len(msg.Headers) > 0 {
		props = make(map[string]string, len(msg.Properties)+len(msg.Headers))
		for key, value := range msg.Headers {
			props[key] = value
		}
		for key, value := range msg.Properties {
			props[key] = value
		}
	}
	if link, ok := protocol.ExtractMessageTraceLink(props, kind, count); ok {
		protocol.AppendTraceLink(result, link)
	}
}

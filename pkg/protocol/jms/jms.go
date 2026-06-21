// Package jms provides a provider-neutral JMS protocol implementation.
package jms

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
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
const sidecarCommandEnv = "APEXLOAD_JMS_SIDECAR_COMMAND"

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
}

type JMS struct {
	name        string
	provider    string
	cfg         config
	adapter     brokerAdapter
	session     *messaging.Session[message]
	initialized bool
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

type brokerAdapter interface {
	Publish(ctx context.Context, destination string, msg message) (message, error)
	Consume(ctx context.Context, destination string, timeout time.Duration) (message, error)
	RequestReply(ctx context.Context, requestDestination string, replyDestination string, request message, timeout time.Duration) (message, message, error)
	HealthCheck(ctx context.Context) error
	Teardown(ctx context.Context) error
}

type message struct {
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
}

// settlementAdapter is an OPTIONAL capability some brokerAdapters implement for
// stateful, client-controlled message settlement and advanced consume modes
// (Azure Service Bus). It is type-asserted at dispatch time (like CookieController)
// so the core brokerAdapter interface — and every other adapter — stays
// untouched; providers that don't implement it report the verb as unsupported.
type settlementAdapter interface {
	// ConsumeAdvanced receives with deferred settlement and/or from a sub-queue
	// (dead-letter) or an accepted session, stashing the in-flight message under a
	// settlement token.
	ConsumeAdvanced(ctx context.Context, destination string, opts consumeOptions, timeout time.Duration) (message, error)
	// Settle resolves a previously consumed peek-locked message by token.
	Settle(ctx context.Context, token string, disposition string, opts settleOptions) error
	// RenewLock extends the broker-side lock on a peek-locked message.
	RenewLock(ctx context.Context, token string) error
	// AcceptSession opens a session receiver (specific sessionID, or the next
	// available when sessionID is empty) and returns a session token + the resolved
	// session id.
	AcceptSession(ctx context.Context, destination, sessionID string) (sessionToken string, resolvedSessionID string, err error)
	// SessionState gets (set=false) or sets (set=true) the session state for an
	// accepted session token.
	SessionState(ctx context.Context, sessionToken string, state []byte, set bool) ([]byte, error)
	// ReceiveDeferred fetches previously deferred messages by sequence number; the
	// returned messages are peek-locked and carry settlement tokens.
	ReceiveDeferred(ctx context.Context, destination string, sequenceNumbers []int64, timeout time.Duration) ([]message, error)
	// CancelScheduled cancels scheduled (future-enqueue) messages by sequence number.
	CancelScheduled(ctx context.Context, destination string, sequenceNumbers []int64) error
}

// consumeOptions carries Service Bus advanced consume controls.
type consumeOptions struct {
	SubQueue     string // "", "dead_letter", "transfer_dead_letter"
	PeekLock     bool   // defer settlement (stash token) instead of auto-completing
	SessionToken string // consume from a previously accepted session (jms_session_accept)
}

// settleOptions carries disposition detail for Settle (dead-letter reason etc.).
type settleOptions struct {
	DeadLetterReason      string
	DeadLetterDescription string
}

var broker = &memoryBroker{queues: make(map[string][]message)}

type memoryBroker struct {
	mu     sync.Mutex
	queues map[string][]message
}

func (b *memoryBroker) publish(destination string, msg message) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.queues[destination] = append(b.queues[destination], msg)
}

func (b *memoryBroker) consume(destination string) (message, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	queue := b.queues[destination]
	if len(queue) == 0 {
		return message{}, false
	}
	msg := queue[0]
	copy(queue, queue[1:])
	queue = queue[:len(queue)-1]
	b.queues[destination] = queue
	return msg, true
}

func (b *memoryBroker) hasMessages(destination string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.queues[destination]) > 0
}

func (j *JMS) Name() string {
	return j.name
}

func (j *JMS) Init(_ context.Context, cfg map[string]any) error {
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
		Timeout:                durationValue(cfg, "jms_timeout_ms", 5*time.Second),
		Properties:             stringMapValue(cfg, "jms_properties"),
		TLS: jmsTLSConfig{
			CACertSecret:       stringValue(cfg, "jms_tls_ca_cert_secret"),
			TrustStoreRef:      stringValue(cfg, "jms_tls_trust_store_ref"),
			InsecureSkipVerify: boolValue(cfg, "jms_tls_insecure_skip_verify"),
		},
		Mock: boolValue(cfg, "jms_mock"),
	}
	if provider := stringValue(cfg, "jms_provider"); provider != "" {
		j.provider = provider
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
	j.session = messaging.New[message](messaging.ParseAckMode(j.cfg.Acknowledgement))
	if j.cfg.Mock {
		j.adapter = memoryAdapter{broker: broker}
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

// selectAdapter chooses the non-mock broker adapter based on the configured
// provider and the operator-owned sidecar command. It enforces the security
// invariant that the sidecar command may ONLY come from the operator env var
// APEXLOAD_JMS_SIDECAR_COMMAND, never from script-supplied properties.
func (j *JMS) selectAdapter() (brokerAdapter, error) {
	// Reject any script-supplied sidecar command outright: a script is
	// user-controlled JSON, so honoring it would be arbitrary command execution.
	if scriptCommand := strings.TrimSpace(j.cfg.Properties["sidecar_command"]); scriptCommand != "" {
		return nil, fmt.Errorf("script-supplied sidecar_command is not allowed; configure %s on the runner", sidecarCommandEnv)
	}

	switch j.provider {
	case "ibmmq":
		return unavailableIbmMqAdapter{cfg: j.cfg}, nil
	case "servicebus":
		return newServiceBusAdapter(j.cfg), nil
	}

	if strings.EqualFold(strings.TrimSpace(j.cfg.Properties["adapter"]), "sidecar") {
		operatorCommand := strings.TrimSpace(os.Getenv(sidecarCommandEnv))
		if operatorCommand == "" {
			return nil, fmt.Errorf("jms adapter=sidecar requires the operator-owned %s to be set on the runner", sidecarCommandEnv)
		}
		return sidecarAdapter{command: operatorCommand, cfg: j.cfg}, nil
	}

	return newSolaceAdapter(j.cfg), nil
}

func (j *JMS) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	start := time.Now()
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
	if isDeadLetter {
		result.Extractions["jms_dead_letter"] = "true"
		result.Extractions["jms_dead_letter_destination"] = destination
	}

	var err error
	var requestMsg message
	var responseMsg message
	hasRequestMsg := false
	hasResponseMsg := false
	switch strings.ToLower(action.Type) {
	case "jms_publish":
		requestMsg, err = j.publish(ctx, action, result)
		hasRequestMsg = err == nil
	case "jms_consume":
		responseMsg, err = j.consume(ctx, action, result)
		hasResponseMsg = err == nil
	case "jms_request_reply":
		requestMsg, responseMsg, err = j.requestReply(ctx, action, result)
		hasRequestMsg = err == nil
		hasResponseMsg = err == nil
	case "jms_ack", "jms_commit", "jms_rollback":
		j.settle(action, result)
	case "jms_settle", "jms_renew_lock":
		err = j.settleAdvanced(ctx, action, result)
	case "jms_session_accept", "jms_session_state", "jms_receive_deferred", "jms_cancel_scheduled":
		responseMsg, hasResponseMsg, err = j.sessionAdvanced(ctx, action, result)
	default:
		err = fmt.Errorf("unsupported JMS action type %q", action.Type)
	}

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
	}
	result.ProtocolEvidence = protocol.BuildProtocolEvidence(action, result)
	return result, nil
}

func (j *JMS) Teardown(ctx context.Context) error {
	if j.adapter == nil {
		return nil
	}
	return j.adapter.Teardown(ctx)
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

func (j *JMS) publish(ctx context.Context, action *protocol.Action, result *protocol.Result) (message, error) {
	destination := j.destination(action)
	if destination == "" {
		return message{}, fmt.Errorf("JMS destination is required")
	}
	msg := j.buildMessage(action, destination)
	published, err := j.adapter.Publish(ctx, destination, msg)
	if err != nil {
		return message{}, err
	}
	result.BytesSent = int64(len(published.Body))
	addMessageExtractions(result, published)
	result.Extractions["jms_operation"] = "publish"
	if published.SequenceNumber != 0 {
		// Scheduled (future-enqueue) send: surface the sequence so a later
		// jms_cancel_scheduled can reference it.
		result.Extractions["jms_scheduled_sequence"] = strconv.FormatInt(published.SequenceNumber, 10)
	}
	return published, evaluateAssertions(action.Assertions, result, published)
}

func (j *JMS) consume(ctx context.Context, action *protocol.Action, result *protocol.Result) (message, error) {
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
	if opts.SubQueue != "" || opts.PeekLock || opts.SessionToken != "" {
		sb, ok := j.adapter.(settlementAdapter)
		if !ok {
			return message{}, fmt.Errorf("jms_consume sub_queue/peek_lock requires the Service Bus adapter (build -tags jms_servicebus_real)")
		}
		msg, err = sb.ConsumeAdvanced(ctx, destination, opts, j.timeout(action))
	} else {
		msg, err = j.adapter.Consume(ctx, destination, j.timeout(action))
	}
	if err != nil {
		result.StatusCode = 204
		return message{}, err
	}
	result.BytesReceived = int64(len(msg.Body))
	if msg.Destination == "" {
		msg.Destination = destination
	}
	addMessageExtractions(result, msg)
	result.Extractions["jms_operation"] = "consume"
	if msg.SettlementToken != "" {
		// Peek-locked: the script settles it later with jms_settle referencing this.
		result.Extractions["jms_settlement_token"] = msg.SettlementToken
	}
	if msg.SequenceNumber != 0 {
		result.Extractions["jms_sequence_number"] = strconv.FormatInt(msg.SequenceNumber, 10)
	}
	// The emitted RedeliveryCount telemetry is derived from the message marker in
	// finalizeJMSServerTelemetry; the session retains the message only for
	// settlement (ack/commit/rollback).
	j.session.Track(msg)
	if err := runExtractors(action.Extractors, result, msg); err != nil {
		return message{}, err
	}
	return msg, evaluateAssertions(action.Assertions, result, msg)
}

// settle handles jms_ack / jms_commit / jms_rollback against the per-VUser
// session, replacing the former no-ops. ack settles client-acknowledged
// messages; commit/rollback drive the transaction with commit/abort accounting,
// and rollback requeues pending messages for redelivery on the in-memory mock
// broker. Real-adapter broker settlement (Service Bus complete/abandon, Solace
// client-ack) is wired per provider in later work; the accounting is shared.
func (j *JMS) settle(action *protocol.Action, result *protocol.Result) {
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
			j.requeueForRedelivery(toRedeliver)
		}
		result.Extractions["jms_operation"] = "rollback"
		result.Extractions["jms_redelivered_scheduled"] = strconv.Itoa(len(toRedeliver))
		result.ServerTelemetry = &protocol.ServerTelemetry{AbortCount: 1}
	}
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
		if err := sb.Settle(ctx, token, disposition, opts); err != nil {
			return err
		}
		result.Extractions["jms_operation"] = "settle"
		result.Extractions["jms_disposition"] = disposition
		// Dead-lettering is a reliability event; carry the counter (control-op style).
		if disposition == "dead_letter" {
			result.ServerTelemetry = &protocol.ServerTelemetry{DeadLetteredCount: 1}
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
		result.Extractions["jms_operation"] = "session_accept"
		result.Extractions["jms_session_token"] = token
		result.Extractions["jms_session_id"] = resolved
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
			result.Extractions["jms_session_state"] = string(got)
		}
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
		addMessageExtractions(result, first)
		if first.SettlementToken != "" {
			result.Extractions["jms_settlement_token"] = first.SettlementToken
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

// parseSequenceNumbers parses a sequence-number list from a DSL param: a JSON
// array of numbers ([]any of float64/int/json.Number) or a comma-separated string.
func parseSequenceNumbers(raw any) ([]int64, error) {
	if raw == nil {
		return nil, nil
	}
	var out []int64
	switch v := raw.(type) {
	case []int64:
		return v, nil
	case []any:
		for _, item := range v {
			n, err := toInt64(item)
			if err != nil {
				return nil, fmt.Errorf("invalid sequence_numbers entry %v: %w", item, err)
			}
			out = append(out, n)
		}
	case string:
		for _, part := range strings.Split(v, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			n, err := strconv.ParseInt(part, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid sequence_numbers entry %q: %w", part, err)
			}
			out = append(out, n)
		}
	default:
		if n, err := toInt64(raw); err == nil {
			out = append(out, n)
		} else {
			return nil, fmt.Errorf("sequence_numbers must be a list of numbers")
		}
	}
	return out, nil
}

func toInt64(v any) (int64, error) {
	switch n := v.(type) {
	case float64:
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
func (j *JMS) requeueForRedelivery(msgs []message) {
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
		broker.publish(msg.Destination, msg)
	}
}

func (j *JMS) requestReply(ctx context.Context, action *protocol.Action, result *protocol.Result) (message, message, error) {
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
	sent, response, err := j.adapter.RequestReply(ctx, requestDestination, replyDestination, request, j.timeout(action))
	if err != nil {
		result.StatusCode = 204
		return message{}, message{}, err
	}
	result.BytesSent = int64(len(sent.Body))
	result.BytesReceived = int64(len(response.Body))
	addMessageExtractions(result, response)
	result.Extractions["jms_request_message_id"] = sent.ID
	result.Extractions["jms_operation"] = "request_reply"
	if err := runExtractors(action.Extractors, result, response); err != nil {
		return message{}, message{}, err
	}
	return sent, response, evaluateAssertions(action.Assertions, result, response)
}

func (j *JMS) timeout(action *protocol.Action) time.Duration {
	timeout := durationParam(action.Params, "timeout_ms", j.cfg.Timeout)
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return timeout
}

type memoryAdapter struct {
	broker *memoryBroker
}

func (a memoryAdapter) Publish(_ context.Context, destination string, msg message) (message, error) {
	a.broker.publish(destination, msg)
	return msg, nil
}

func (a memoryAdapter) Consume(ctx context.Context, destination string, timeout time.Duration) (message, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()

	for {
		if msg, ok := a.broker.consume(destination); ok {
			return msg, nil
		}
		select {
		case <-ctx.Done():
			return message{}, ctx.Err()
		case <-deadline.C:
			return message{}, fmt.Errorf("no JMS message received from %q within %s", destination, timeout)
		case <-tick.C:
		}
	}
}

func (a memoryAdapter) RequestReply(ctx context.Context, requestDestination string, replyDestination string, request message, timeout time.Duration) (message, message, error) {
	a.broker.publish(requestDestination, request)

	if !a.broker.hasMessages(replyDestination) {
		a.broker.publish(replyDestination, message{
			ID:            uuid.NewString(),
			CorrelationID: request.CorrelationID,
			Destination:   replyDestination,
			Headers:       cloneStringMap(request.Headers),
			Properties:    cloneStringMap(request.Properties),
			Body:          request.Body,
			CreatedAt:     time.Now(),
		})
	}

	response, err := a.Consume(ctx, replyDestination, timeout)
	return request, response, err
}

func (a memoryAdapter) HealthCheck(context.Context) error {
	if a.broker == nil {
		return fmt.Errorf("memory JMS broker is not initialized")
	}
	return nil
}

func (a memoryAdapter) Teardown(context.Context) error {
	return nil
}

type sidecarAdapter struct {
	command string
	cfg     config
}

type sidecarRequest struct {
	Operation        string         `json:"operation"`
	Provider         string         `json:"provider,omitempty"`
	Config           config         `json:"config,omitempty"`
	Destination      string         `json:"destination,omitempty"`
	ReplyDestination string         `json:"reply_destination,omitempty"`
	TimeoutMs        int64          `json:"timeout_ms,omitempty"`
	Message          message        `json:"message,omitempty"`
	Messages         []message      `json:"messages,omitempty"`
	Metadata         map[string]any `json:"metadata,omitempty"`
}

type sidecarResponse struct {
	Message  message `json:"message,omitempty"`
	Request  message `json:"request,omitempty"`
	Response message `json:"response,omitempty"`
	Error    string  `json:"error,omitempty"`
}

func (a sidecarAdapter) Publish(ctx context.Context, destination string, msg message) (message, error) {
	resp, err := a.run(ctx, sidecarRequest{
		Operation:   "publish",
		Provider:    "solace",
		Config:      a.cfg,
		Destination: destination,
		TimeoutMs:   durationMilliseconds(a.cfg.Timeout),
		Message:     msg,
	}, a.cfg.Timeout)
	if err != nil {
		return message{}, err
	}
	if resp.Message.ID == "" && len(resp.Message.Body) == 0 {
		return msg, nil
	}
	return normalizeSidecarMessage(resp.Message, msg), nil
}

func (a sidecarAdapter) Consume(ctx context.Context, destination string, timeout time.Duration) (message, error) {
	resp, err := a.run(ctx, sidecarRequest{
		Operation:   "consume",
		Provider:    "solace",
		Config:      a.cfg,
		Destination: destination,
		TimeoutMs:   durationMilliseconds(timeout),
	}, timeout)
	if err != nil {
		return message{}, err
	}
	if resp.Message.ID == "" && len(resp.Message.Body) == 0 {
		return message{}, fmt.Errorf("jms sidecar consume returned no message")
	}
	return normalizeSidecarMessage(resp.Message, message{Destination: destination, CreatedAt: time.Now()}), nil
}

func (a sidecarAdapter) RequestReply(ctx context.Context, requestDestination string, replyDestination string, request message, timeout time.Duration) (message, message, error) {
	resp, err := a.run(ctx, sidecarRequest{
		Operation:        "request_reply",
		Provider:         "solace",
		Config:           a.cfg,
		Destination:      requestDestination,
		ReplyDestination: replyDestination,
		TimeoutMs:        durationMilliseconds(timeout),
		Message:          request,
	}, timeout)
	if err != nil {
		return message{}, message{}, err
	}
	sent := resp.Request
	if sent.ID == "" && len(sent.Body) == 0 {
		sent = request
	}
	response := resp.Response
	if response.ID == "" && len(response.Body) == 0 {
		response = resp.Message
	}
	if response.ID == "" && len(response.Body) == 0 {
		return message{}, message{}, fmt.Errorf("jms sidecar request_reply returned no response message")
	}
	return normalizeSidecarMessage(sent, request), normalizeSidecarMessage(response, message{
		CorrelationID: request.CorrelationID,
		Destination:   replyDestination,
		CreatedAt:     time.Now(),
	}), nil
}

func (a sidecarAdapter) HealthCheck(ctx context.Context) error {
	_, err := a.run(ctx, sidecarRequest{
		Operation: "health",
		Provider:  "solace",
		Config:    a.cfg,
		TimeoutMs: durationMilliseconds(a.cfg.Timeout),
	}, a.cfg.Timeout)
	return err
}

func (a sidecarAdapter) Teardown(ctx context.Context) error {
	_, err := a.run(ctx, sidecarRequest{
		Operation: "teardown",
		Provider:  "solace",
		Config:    a.cfg,
		TimeoutMs: durationMilliseconds(a.cfg.Timeout),
	}, a.cfg.Timeout)
	return err
}

func (a sidecarAdapter) run(ctx context.Context, req sidecarRequest, timeout time.Duration) (sidecarResponse, error) {
	fields := strings.Fields(a.command)
	if len(fields) == 0 {
		return sidecarResponse{}, fmt.Errorf("jms sidecar command is not configured")
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout+time.Second)
	defer cancel()

	payload, err := json.Marshal(req)
	if err != nil {
		return sidecarResponse{}, fmt.Errorf("marshal jms sidecar request: %w", err)
	}
	cmd := exec.CommandContext(runCtx, fields[0], fields[1:]...)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if runCtx.Err() != nil {
			return sidecarResponse{}, fmt.Errorf("jms sidecar %s timed out: %w", req.Operation, runCtx.Err())
		}
		return sidecarResponse{}, fmt.Errorf("jms sidecar %s failed: %w", req.Operation, err)
	}
	if len(bytes.TrimSpace(out)) == 0 {
		return sidecarResponse{}, nil
	}
	var resp sidecarResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return sidecarResponse{}, fmt.Errorf("decode jms sidecar %s response: %w", req.Operation, err)
	}
	if strings.TrimSpace(resp.Error) != "" {
		return sidecarResponse{}, fmt.Errorf("jms sidecar %s rejected request: %s", req.Operation, resp.Error)
	}
	return resp, nil
}

func normalizeSidecarMessage(msg message, fallback message) message {
	if msg.ID == "" {
		msg.ID = fallback.ID
	}
	if msg.CorrelationID == "" {
		msg.CorrelationID = fallback.CorrelationID
	}
	if msg.Destination == "" {
		msg.Destination = fallback.Destination
	}
	if msg.Headers == nil {
		msg.Headers = cloneStringMap(fallback.Headers)
	}
	if msg.Properties == nil {
		msg.Properties = cloneStringMap(fallback.Properties)
	}
	if len(msg.Body) == 0 {
		msg.Body = fallback.Body
	}
	if msg.CreatedAt.IsZero() {
		if fallback.CreatedAt.IsZero() {
			msg.CreatedAt = time.Now()
		} else {
			msg.CreatedAt = fallback.CreatedAt
		}
	}
	return msg
}

func durationMilliseconds(duration time.Duration) int64 {
	if duration <= 0 {
		return 0
	}
	return duration.Milliseconds()
}

type unavailableSolaceAdapter struct {
	cfg config
}

func (a unavailableSolaceAdapter) Publish(context.Context, string, message) (message, error) {
	return message{}, a.err()
}

func (a unavailableSolaceAdapter) Consume(context.Context, string, time.Duration) (message, error) {
	return message{}, a.err()
}

func (a unavailableSolaceAdapter) RequestReply(context.Context, string, string, message, time.Duration) (message, message, error) {
	return message{}, message{}, a.err()
}

func (a unavailableSolaceAdapter) HealthCheck(context.Context) error {
	if a.cfg.BrokerURL == "" {
		return fmt.Errorf("jms broker_url is not configured")
	}
	return a.err()
}

func (a unavailableSolaceAdapter) Teardown(context.Context) error {
	return nil
}

func (a unavailableSolaceAdapter) err() error {
	return fmt.Errorf("real Solace JMS adapter is not available in this build; enable mock mode for local replay")
}

type unavailableIbmMqAdapter struct {
	cfg config
}

func (a unavailableIbmMqAdapter) Publish(context.Context, string, message) (message, error) {
	return message{}, a.err()
}

func (a unavailableIbmMqAdapter) Consume(context.Context, string, time.Duration) (message, error) {
	return message{}, a.err()
}

func (a unavailableIbmMqAdapter) RequestReply(context.Context, string, string, message, time.Duration) (message, message, error) {
	return message{}, message{}, a.err()
}

func (a unavailableIbmMqAdapter) HealthCheck(context.Context) error {
	if a.cfg.BrokerURL == "" {
		return fmt.Errorf("jms broker_url is not configured")
	}
	return a.err()
}

func (a unavailableIbmMqAdapter) Teardown(context.Context) error {
	return nil
}

func (a unavailableIbmMqAdapter) err() error {
	return fmt.Errorf("real IBM MQ adapter is not available in this build; enable mock mode for local replay")
}

type unavailableServiceBusAdapter struct {
	cfg config
}

func (a unavailableServiceBusAdapter) Publish(context.Context, string, message) (message, error) {
	return message{}, a.err()
}

func (a unavailableServiceBusAdapter) Consume(context.Context, string, time.Duration) (message, error) {
	return message{}, a.err()
}

func (a unavailableServiceBusAdapter) RequestReply(context.Context, string, string, message, time.Duration) (message, message, error) {
	return message{}, message{}, a.err()
}

func (a unavailableServiceBusAdapter) HealthCheck(context.Context) error {
	if a.cfg.BrokerURL == "" {
		return fmt.Errorf("jms broker_url is not configured")
	}
	return a.err()
}

func (a unavailableServiceBusAdapter) Teardown(context.Context) error {
	return nil
}

func (a unavailableServiceBusAdapter) err() error {
	return fmt.Errorf("real Azure Service Bus adapter is not available in this build; enable mock mode or build with -tags jms_servicebus_real")
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
		TimeToLive:   durationParam(action.Params, "time_to_live_ms", 0),
	}
	if delay := durationParam(action.Params, "scheduled_enqueue_ms", 0); delay > 0 {
		msg.ScheduledEnqueueAt = msg.CreatedAt.Add(delay)
	}
	return msg
}

func (j *JMS) destination(action *protocol.Action) string {
	return paramString(action.Params, "destination", protocol.FirstNonEmpty(action.Target, j.cfg.Destination))
}

func (j *JMS) consumeDestination(action *protocol.Action) string {
	return j.serviceBusConsumeDestination(action, j.destination(action))
}

func (j *JMS) serviceBusConsumeDestination(action *protocol.Action, destination string) string {
	if !strings.EqualFold(strings.TrimSpace(j.provider), "servicebus") {
		return destination
	}
	destinationType := paramString(action.Params, "destination_type", j.cfg.DestinationType)
	subscription := paramString(action.Params, "subscription", j.cfg.Subscription)
	return serviceBusConsumeDestination(destination, destinationType, subscription)
}

func addMessageExtractions(result *protocol.Result, msg message) {
	result.CorrelationID = msg.CorrelationID
	result.Extractions["jms_message_id"] = msg.ID
	result.Extractions["jms_correlation_id"] = msg.CorrelationID
	result.Extractions["jms_destination"] = msg.Destination
	result.Extractions["jms_body"] = string(msg.Body)
	if reason, ok := firstMatchingMapValue(msg.Properties, "dead_letter_reason", "dlq_reason", "x-dead-letter-reason"); ok {
		result.Extractions["jms_dead_letter_reason"] = reason
	}
	if deliveryCount, ok := firstMatchingMapValue(msg.Properties, "delivery_count", "x-delivery-count", "JMSXDeliveryCount"); ok {
		result.Extractions["jms_delivery_count"] = deliveryCount
	}
}

func finalizeJMSServerTelemetry(result *protocol.Result, request message, hasRequest bool, response message, hasResponse bool) *protocol.ServerTelemetry {
	if result == nil || (!hasRequest && !hasResponse) {
		return nil
	}
	telemetry := &protocol.ServerTelemetry{
		TransferMs: durationToNonNegativeMilliseconds(result.Duration),
	}
	if hasRequest {
		telemetry.MessagesSent = 1
	}
	if hasResponse {
		telemetry.MessagesReceived = 1
		observedAt := result.StartTime.Add(result.Duration)
		if !response.CreatedAt.IsZero() && !observedAt.Before(response.CreatedAt) {
			telemetry.ConsumerLagMs = durationToNonNegativeMilliseconds(observedAt.Sub(response.CreatedAt))
		}
		if response.Properties[redeliveredProperty] == "true" {
			telemetry.RedeliveryCount = 1
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

func runExtractors(extractors []protocol.Extractor, result *protocol.Result, msg message) error {
	for _, extractor := range extractors {
		value, err := extract(extractor, msg)
		if err != nil {
			return err
		}
		result.Extractions[extractor.Name] = value
	}
	return nil
}

func extract(ext protocol.Extractor, msg message) (string, error) {
	switch ext.Source {
	case "body":
		switch ext.Type {
		case "regex":
			re, err := regexp.Compile(ext.Expression)
			if err != nil {
				return "", fmt.Errorf("invalid regex: %w", err)
			}
			matches := re.FindSubmatch(msg.Body)
			if len(matches) < 2 {
				return "", fmt.Errorf("no match for regex %q", ext.Expression)
			}
			return string(matches[1]), nil
		case "jsonpath":
			return extractJSONPath(ext.Expression, msg.Body)
		default:
			return "", fmt.Errorf("unsupported JMS body extraction type: %s", ext.Type)
		}
	case "header":
		return lookupString(ext.Expression, msg.Headers, "header")
	case "property":
		return lookupString(ext.Expression, msg.Properties, "property")
	case "message_id":
		return msg.ID, nil
	case "correlation_id":
		return msg.CorrelationID, nil
	default:
		return "", fmt.Errorf("unsupported JMS extraction source: %s", ext.Source)
	}
}

func evaluateAssertions(assertions []protocol.Assertion, result *protocol.Result, msg message) error {
	for _, assertion := range assertions {
		switch assertion.Type {
		case "message_received":
			if msg.ID == "" {
				return fmt.Errorf("expected a JMS message")
			}
		case "body_contains":
			if !strings.Contains(string(msg.Body), assertion.Value) {
				return fmt.Errorf("JMS message body does not contain %q", assertion.Value)
			}
		case "property_equals":
			if msg.Properties[assertion.Value] != fmt.Sprint(assertion.Expected) {
				return fmt.Errorf("JMS property %q = %q, want %q", assertion.Value, msg.Properties[assertion.Value], assertion.Expected)
			}
		case "header_equals":
			if msg.Headers[assertion.Value] != fmt.Sprint(assertion.Expected) {
				return fmt.Errorf("JMS header %q = %q, want %q", assertion.Value, msg.Headers[assertion.Value], assertion.Expected)
			}
		case "correlation_match":
			if msg.CorrelationID != assertion.Value {
				return fmt.Errorf("JMS correlation_id = %q, want %q", msg.CorrelationID, assertion.Value)
			}
		case "response_time_ms":
			if assertion.Max > 0 && result.Duration.Milliseconds() > int64(assertion.Max) {
				return fmt.Errorf("JMS response time %dms exceeded max %dms", result.Duration.Milliseconds(), assertion.Max)
			}
		default:
			return fmt.Errorf("unsupported JMS assertion type: %s", assertion.Type)
		}
	}
	return nil
}

func extractJSONPath(expr string, body []byte) (string, error) {
	expr = strings.TrimPrefix(strings.TrimSpace(expr), "$.")
	if expr == "" {
		return string(body), nil
	}
	parts := strings.Split(expr, ".")
	var current any
	if err := json.Unmarshal(body, &current); err != nil {
		return "", fmt.Errorf("invalid JSON body: %w", err)
	}
	for _, part := range parts {
		obj, ok := current.(map[string]any)
		if !ok {
			return "", fmt.Errorf("expected JSON object at %q", part)
		}
		current, ok = obj[part]
		if !ok {
			return "", fmt.Errorf("JSON field %q not found", part)
		}
	}
	return fmt.Sprint(current), nil
}

func stringValue(cfg map[string]any, key string) string {
	value, _ := cfg[key].(string)
	return value
}

func boolValue(cfg map[string]any, key string) bool {
	value, _ := cfg[key].(bool)
	return value
}

func durationValue(cfg map[string]any, key string, fallback time.Duration) time.Duration {
	switch value := cfg[key].(type) {
	case int:
		return time.Duration(value) * time.Millisecond
	case int64:
		return time.Duration(value) * time.Millisecond
	case float64:
		return time.Duration(value) * time.Millisecond
	default:
		return fallback
	}
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

func durationParam(params map[string]any, key string, fallback time.Duration) time.Duration {
	if params == nil {
		return fallback
	}
	return durationValue(params, key, fallback)
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

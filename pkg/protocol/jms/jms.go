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

// selectiveConsumer is an OPTIONAL capability for JMS message selectors on
// consume (P2.3). Implemented by the in-memory mock and the AMQP/Solace adapter
// (server-side selector-filter link). Azure Service Bus does NOT implement it —
// Service Bus selectors are admin-plane subscription rules, not a data-plane
// per-consume filter — so a selector consume against Service Bus fails closed.
type selectiveConsumer interface {
	ConsumeSelective(ctx context.Context, destination, selector string, timeout time.Duration) (message, error)
}

// messageBrowser is an OPTIONAL capability for non-destructive queue browse
// (JMS QueueBrowser, P2.3). Implemented by the in-memory mock, the AMQP/Solace
// adapter (copy-distribution receiver), and Azure Service Bus (PeekMessages).
type messageBrowser interface {
	Browse(ctx context.Context, destination, selector string, max int, timeout time.Duration) ([]message, error)
}

// topicAdapter is an OPTIONAL capability for JMS topic fan-out + durable/shared
// subscriptions (P2.3). A topic publish reaches every registered subscription;
// each subscription consumes with an independent cursor. Implemented by the mock
// and the AMQP/Solace adapter (durable receiver). Azure Service Bus is NOT routed
// here — its topic/subscription addressing is handled by the standard path — so
// the Service Bus adapter does not implement it.
type topicAdapter interface {
	PublishTopic(ctx context.Context, topic string, msg message) error
	ConsumeTopic(ctx context.Context, topic, subscription, selector string, durable bool, timeout time.Duration) (message, error)
	BrowseTopic(ctx context.Context, topic, subscription, selector string, max int, timeout time.Duration) ([]message, error)
}

var broker = &memoryBroker{
	queues: make(map[string][]message),
	topics: make(map[string]map[string][]message),
}

type memoryBroker struct {
	mu     sync.Mutex
	queues map[string][]message
	// topics models JMS topic fan-out: topic name -> subscription name -> that
	// subscription's independent message queue. A topic publish appends a copy to
	// every registered subscription; each subscription consumes its own copy with
	// an independent cursor.
	topics map[string]map[string][]message
	// seqLast is a monotonic broker sequence assigned to published messages that
	// carry none, so consumers can detect duplicate / out-of-order delivery (real
	// brokers assign sequence numbers; the mock mirrors that for fidelity). A
	// redelivered message keeps its original sequence (it is non-zero), so a
	// rollback redelivery is not mistaken for a new message.
	seqLast int64
}

// assignSequenceLocked stamps a monotonic sequence on a message that doesn't
// already carry one. The caller must hold b.mu.
func (b *memoryBroker) assignSequenceLocked(msg *message) {
	if msg.SequenceNumber == 0 {
		b.seqLast++
		msg.SequenceNumber = b.seqLast
	}
}

// cloneMessageValue returns a copy of msg with its reference fields (maps, body)
// deep-copied so callers cannot mutate shared broker state.
func cloneMessageValue(msg message) message {
	out := msg
	out.Headers = cloneStringMap(msg.Headers)
	out.Properties = cloneStringMap(msg.Properties)
	if msg.MapBody != nil {
		out.MapBody = cloneStringMap(msg.MapBody)
	}
	if msg.StreamBody != nil {
		out.StreamBody = append([]string(nil), msg.StreamBody...)
	}
	if msg.Body != nil {
		out.Body = append([]byte(nil), msg.Body...)
	}
	return out
}

func (b *memoryBroker) publish(destination string, msg message) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.assignSequenceLocked(&msg)
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

// consumeMatching pops the first message satisfying the selector (a nil selector
// matches the head, preserving plain-FIFO behavior). Non-matching messages stay
// in the queue for other consumers.
func (b *memoryBroker) consumeMatching(destination string, sel *selector) (message, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	queue := b.queues[destination]
	for i, msg := range queue {
		if sel.matches(msg.Headers, msg.Properties) {
			// Three-index slice on the head keeps the backing array intact for the
			// retained tail.
			b.queues[destination] = append(queue[:i:i], queue[i+1:]...)
			return msg, true
		}
	}
	return message{}, false
}

// browse returns copies of the messages matching the selector without removing
// them (JMS QueueBrowser semantics). max <= 0 means no limit.
func (b *memoryBroker) browse(destination string, sel *selector, max int) []message {
	b.mu.Lock()
	defer b.mu.Unlock()
	return browseMatching(b.queues[destination], sel, max)
}

// browseMatching returns deep copies of the messages in queue matching sel, so a
// browsed message can never alias (and thus mutate) broker state. max <= 0 means
// no limit.
func browseMatching(queue []message, sel *selector, max int) []message {
	var out []message
	for _, msg := range queue {
		if sel.matches(msg.Headers, msg.Properties) {
			out = append(out, cloneMessageValue(msg))
			if max > 0 && len(out) >= max {
				break
			}
		}
	}
	return out
}

// subscribe registers a topic subscription (idempotently) so subsequent topic
// publishes fan out to it. A subscription registered at connect time (Init, when
// config.jms.subscription is set) collects every later publish; one registered
// lazily on first consume only collects publishes after that point. The mock has
// no long-lived connection, so it does not model a durable subscriber collecting
// messages published before it ever registered.
func (b *memoryBroker) subscribe(topic, subscription string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.topics[topic] == nil {
		b.topics[topic] = make(map[string][]message)
	}
	if _, ok := b.topics[topic][subscription]; !ok {
		b.topics[topic][subscription] = nil
	}
}

// publishTopic fans a message out to every registered subscription on the topic,
// each receiving its own deep copy. A topic with no subscriptions drops the
// message (standard topic semantics — only subscribers receive).
func (b *memoryBroker) publishTopic(topic string, msg message) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.assignSequenceLocked(&msg)
	for sub := range b.topics[topic] {
		b.topics[topic][sub] = append(b.topics[topic][sub], cloneMessageValue(msg))
	}
}

// requeueTopic re-appends a rolled-back message to a specific subscription's
// queue for redelivery. Unlike publishTopic it targets only the subscription the
// message was consumed from, so a transacted/client rollback redelivers to that
// subscription rather than fanning out to every subscriber.
func (b *memoryBroker) requeueTopic(topic, subscription string, msg message) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.topics[topic] == nil {
		b.topics[topic] = make(map[string][]message)
	}
	b.topics[topic][subscription] = append(b.topics[topic][subscription], cloneMessageValue(msg))
}

// consumeTopic pops the first message matching the selector from a subscription's
// independent queue.
func (b *memoryBroker) consumeTopic(topic, subscription string, sel *selector) (message, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.topics[topic] == nil {
		return message{}, false
	}
	queue := b.topics[topic][subscription]
	for i, m := range queue {
		if sel.matches(m.Headers, m.Properties) {
			b.topics[topic][subscription] = append(queue[:i:i], queue[i+1:]...)
			return m, true
		}
	}
	return message{}, false
}

// browseTopic returns deep copies of a subscription's matching messages without
// consuming them.
func (b *memoryBroker) browseTopic(topic, subscription string, sel *selector, max int) []message {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.topics[topic] == nil {
		return nil
	}
	return browseMatching(b.topics[topic][subscription], sel, max)
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
		// Pre-register a configured topic subscription at connect time so a topic
		// publish that lands before the subscriber's first consume is still captured
		// (durable-subscription semantics). Without this, the mock only retains
		// messages published after the first consume registers the subscription.
		if !strings.EqualFold(strings.TrimSpace(j.provider), "servicebus") &&
			strings.EqualFold(strings.TrimSpace(j.cfg.DestinationType), "topic") &&
			strings.TrimSpace(j.cfg.Subscription) != "" && strings.TrimSpace(j.cfg.Destination) != "" {
			broker.subscribe(j.cfg.Destination, j.cfg.Subscription)
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
	case "jms_browse":
		responseMsg, hasResponseMsg, err = j.browse(ctx, action, result)
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
		if isJMSSendAction(action.Type) {
			// Count a failed publish/send attempt so the messaging reliability
			// telemetry reflects send failures instead of always reading zero.
			if result.ServerTelemetry == nil {
				result.ServerTelemetry = &protocol.ServerTelemetry{}
			}
			result.ServerTelemetry.SendFailureCount++
		}
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
	var published message
	var err error
	if topic, ok := j.adapter.(topicAdapter); ok && j.useTopicFanout(action) {
		// Topic publish fans out to every registered subscription.
		err = topic.PublishTopic(ctx, destination, msg)
		published = msg
	} else {
		published, err = j.adapter.Publish(ctx, destination, msg)
	}
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
	selectorExpr := paramString(action.Params, "selector", "")
	topic, isTopicCap := j.adapter.(topicAdapter)
	switch {
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
		msg, err = topic.ConsumeTopic(ctx, destination, sub, selectorExpr, j.isDurableSubscription(action), j.timeout(action))
		if err == nil {
			// Record the topic origin so a later rollback requeues to this
			// subscription rather than into the unrelated queue map.
			msg.sourceTopic, msg.sourceSubscription = destination, sub
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
	// Reliability detection by broker sequence. A legitimate rollback-redelivery
	// carries the same sequence; it is tracked as a redelivery, NOT a broker
	// duplicate, so skip Observe for redelivered messages. Observe within the
	// subscription's own sequence space (empty for queue consumes): topic fan-out
	// delivers the SAME broker sequence to every subscription, so a single session
	// consuming two subscriptions of one topic would otherwise flag the second,
	// distinct delivery as a duplicate.
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
	var (
		msgs []message
		err  error
	)
	if topic, ok := j.adapter.(topicAdapter); ok && j.useTopicFanout(action) {
		sub := j.subscriptionName(action)
		if sub == "" {
			return message{}, false, fmt.Errorf("jms_browse on a topic requires a subscription (params.subscription or config.jms.subscription)")
		}
		msgs, err = topic.BrowseTopic(ctx, destination, sub, selectorExpr, max, j.timeout(action))
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
	if first.Destination == "" {
		first.Destination = destination
	}
	result.BytesReceived = int64(len(first.Body))
	addMessageExtractions(result, first)
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
		if msg.sourceTopic != "" {
			// Topic-origin message: requeue to the originating subscription's queue.
			// broker.publish would land it in the unrelated queue map (keyed by the
			// topic name), where the topic consume path never looks, so it would
			// never be redelivered.
			broker.requeueTopic(msg.sourceTopic, msg.sourceSubscription, msg)
			continue
		}
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

// ConsumeSelective polls for the first message matching the selector (P2.3).
func (a memoryAdapter) ConsumeSelective(ctx context.Context, destination, selectorExpr string, timeout time.Duration) (message, error) {
	sel, err := compileSelector(selectorExpr)
	if err != nil {
		return message{}, err
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if msg, ok := a.broker.consumeMatching(destination, sel); ok {
			return msg, nil
		}
		select {
		case <-ctx.Done():
			return message{}, ctx.Err()
		case <-deadline.C:
			return message{}, fmt.Errorf("no JMS message matching the selector received from %q within %s", destination, timeout)
		case <-tick.C:
		}
	}
}

// Browse returns matching messages without consuming them (P2.3).
func (a memoryAdapter) Browse(_ context.Context, destination, selectorExpr string, max int, _ time.Duration) ([]message, error) {
	sel, err := compileSelector(selectorExpr)
	if err != nil {
		return nil, err
	}
	return a.broker.browse(destination, sel, max), nil
}

// PublishTopic fans a message out to every registered subscription (P2.3).
func (a memoryAdapter) PublishTopic(_ context.Context, topic string, msg message) error {
	a.broker.publishTopic(topic, msg)
	return nil
}

// ConsumeTopic registers the subscription (so future publishes fan out to it,
// even across timed-out polls) then consumes its next matching message (P2.3).
// The mock retains messages for durable and non-durable subscriptions alike (it
// has no long-lived consumer connection to model a non-durable drop).
func (a memoryAdapter) ConsumeTopic(ctx context.Context, topic, subscription, selectorExpr string, _ bool, timeout time.Duration) (message, error) {
	a.broker.subscribe(topic, subscription)
	sel, err := compileSelector(selectorExpr)
	if err != nil {
		return message{}, err
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if msg, ok := a.broker.consumeTopic(topic, subscription, sel); ok {
			return msg, nil
		}
		select {
		case <-ctx.Done():
			return message{}, ctx.Err()
		case <-deadline.C:
			return message{}, fmt.Errorf("no JMS message received on topic %q subscription %q within %s", topic, subscription, timeout)
		case <-tick.C:
		}
	}
}

// BrowseTopic registers the subscription then non-destructively reads its
// matching messages (P2.3).
func (a memoryAdapter) BrowseTopic(_ context.Context, topic, subscription, selectorExpr string, max int, _ time.Duration) ([]message, error) {
	a.broker.subscribe(topic, subscription)
	sel, err := compileSelector(selectorExpr)
	if err != nil {
		return nil, err
	}
	return a.broker.browseTopic(topic, subscription, sel, max), nil
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
		TimeToLive:   durationParam(action.Params, "time_to_live_ms", 0),
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
	if delay := durationParam(action.Params, "scheduled_enqueue_ms", 0); delay > 0 {
		msg.ScheduledEnqueueAt = msg.CreatedAt.Add(delay)
	}
	return msg
}

func metadataKeyIsProvenance(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	return strings.HasPrefix(key, "asyncapi_")
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
// capability: destination_type=topic on a non-Service-Bus provider. Service Bus
// encodes topic/subscription in the destination string and uses the standard
// path, so it is excluded.
func (j *JMS) useTopicFanout(action *protocol.Action) bool {
	if strings.EqualFold(strings.TrimSpace(j.provider), "servicebus") {
		return false
	}
	destType := paramString(action.Params, "destination_type", j.cfg.DestinationType)
	return strings.EqualFold(strings.TrimSpace(destType), "topic")
}

// subscriptionName resolves the topic subscription identifier (params.subscription
// then config.jms.subscription).
func (j *JMS) subscriptionName(action *protocol.Action) string {
	return paramString(action.Params, "subscription", j.cfg.Subscription)
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

func addMessageExtractions(result *protocol.Result, msg message) {
	result.CorrelationID = msg.CorrelationID
	result.Extractions["jms_message_id"] = msg.ID
	result.Extractions["jms_correlation_id"] = msg.CorrelationID
	result.Extractions["jms_destination"] = msg.Destination
	result.Extractions["jms_body"] = string(msg.Body)
	if msg.Type != "" {
		result.Extractions["jms_message_type"] = msg.Type
	}
	if reason, ok := firstMatchingMapValue(msg.Properties, "dead_letter_reason", "dlq_reason", "x-dead-letter-reason"); ok {
		result.Extractions["jms_dead_letter_reason"] = reason
	}
	if deliveryCount, ok := firstMatchingMapValue(msg.Properties, "delivery_count", "x-delivery-count", "JMSXDeliveryCount"); ok {
		result.Extractions["jms_delivery_count"] = deliveryCount
	}
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

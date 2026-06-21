//go:build jms_servicebus_real

package jms

import (
	"context"
	"encoding/hex"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/messaging"
)

var (
	serviceBusClients      = newServiceBusClientPool()
	serviceBusShardCounter atomic.Uint64
)

func newServiceBusAdapter(cfg config) brokerAdapter {
	conn, err := resolveServiceBusConn(cfg)
	if err != nil {
		return serviceBusConfigErrorAdapter{err: err}
	}
	shards := serviceBusShardCount(cfg.Properties)
	shard := int(serviceBusShardCounter.Add(1)-1) % shards
	handle, err := serviceBusClients.acquire(conn, serviceBusPoolKey(conn, shard))
	if err != nil {
		return serviceBusConfigErrorAdapter{err: err}
	}
	return &serviceBusAdapter{
		cfg:       cfg,
		conn:      conn,
		handle:    handle,
		receivers: make(map[string]*azservicebus.Receiver),
		inflight:  make(map[string]inflightMessage),
	}
}

type serviceBusAdapter struct {
	cfg       config
	conn      serviceBusConn
	handle    *serviceBusPoolEntry
	mu        sync.Mutex
	receivers map[string]*azservicebus.Receiver
	// inflight holds peek-locked messages awaiting explicit settlement (jms_settle
	// / jms_renew_lock), keyed by a per-VUser settlement token. The SDK settles via
	// the *ReceivedMessage on the SAME receiver it came from, so both are stored.
	inflight map[string]inflightMessage
}

type inflightMessage struct {
	received *azservicebus.ReceivedMessage
	receiver *azservicebus.Receiver
}

func (a *serviceBusAdapter) Publish(ctx context.Context, destination string, msg message) (message, error) {
	sender, err := a.handle.sender(destination)
	if err != nil {
		return message{}, err
	}
	outgoing := normalizeServiceBusOutgoingMessage(destination, msg)
	if err := sender.SendMessage(ctx, toServiceBusMessage(destination, outgoing), nil); err != nil {
		return message{}, err
	}
	return outgoing, nil
}

func (a *serviceBusAdapter) Consume(ctx context.Context, destination string, timeout time.Duration) (message, error) {
	receiver, err := a.receiver(destination)
	if err != nil {
		return message{}, err
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	receiveCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	messages, err := receiver.ReceiveMessages(receiveCtx, 1, nil)
	if err != nil {
		return message{}, fmt.Errorf("no JMS message received from %q within %s: %w", destination, timeout, err)
	}
	if len(messages) == 0 {
		return message{}, fmt.Errorf("no JMS message received from %q within %s", destination, timeout)
	}
	converted := fromServiceBusMessage(destination, messages[0])
	if serviceBusReceiveMode(a.cfg.Acknowledgement, a.cfg.Properties) != "receive_and_delete" {
		if err := receiver.CompleteMessage(ctx, messages[0], nil); err != nil {
			return converted, fmt.Errorf("complete Service Bus message from %q: %w", destination, err)
		}
	}
	return converted, nil
}

func (a *serviceBusAdapter) RequestReply(ctx context.Context, requestDestination string, replyDestination string, request message, timeout time.Duration) (message, message, error) {
	if request.CorrelationID == "" {
		request.CorrelationID = protocol.FirstNonEmpty(request.ID, newJMSMessageID())
	}
	if request.Headers == nil {
		request.Headers = make(map[string]string)
	}
	request.Headers["reply_to"] = serviceBusReplyToDestination(replyDestination)
	sent, err := a.Publish(ctx, requestDestination, request)
	if err != nil {
		return message{}, message{}, err
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		reply, err := a.Consume(ctx, replyDestination, remaining)
		if err != nil {
			return sent, message{}, err
		}
		if messaging.CorrelationMatches(request.CorrelationID, reply.CorrelationID) {
			return sent, reply, nil
		}
		// NOTE: an uncorrelated reply has already been completed by Consume
		// (peek-lock auto-complete) and is therefore lost rather than requeued.
		// Requeue-safety needs client-controlled settlement (abandon), which lands
		// with the Service Bus settlement work (P2.4).
	}
	return sent, message{}, fmt.Errorf("no correlated JMS reply received from %q within %s", replyDestination, timeout)
}

func (a *serviceBusAdapter) HealthCheck(ctx context.Context) error {
	if a.handle == nil {
		return fmt.Errorf("Service Bus client is not initialized")
	}
	if a.cfg.Destination != "" {
		_, err := a.handle.sender(a.cfg.Destination)
		return err
	}
	return nil
}

func (a *serviceBusAdapter) Teardown(ctx context.Context) error {
	a.mu.Lock()
	receivers := a.receivers
	a.receivers = make(map[string]*azservicebus.Receiver)
	a.inflight = make(map[string]inflightMessage)
	handle := a.handle
	a.handle = nil
	a.mu.Unlock()

	var firstErr error
	for _, receiver := range receivers {
		if err := receiver.Close(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if err := serviceBusClients.release(ctx, handle); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func (a *serviceBusAdapter) receiver(destination string) (*azservicebus.Receiver, error) {
	return a.receiverFor(destination, "")
}

// receiverFor returns a cached receiver for a destination + optional sub-queue
// (dead_letter / transfer_dead_letter). Receivers are cached per (destination,
// subQueue) so Teardown closes them all.
func (a *serviceBusAdapter) receiverFor(destination, subQueue string) (*azservicebus.Receiver, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	key := destination
	if subQueue != "" {
		key = destination + "|" + subQueue
	}
	if receiver := a.receivers[key]; receiver != nil {
		return receiver, nil
	}
	options := &azservicebus.ReceiverOptions{ReceiveMode: serviceBusAZReceiveMode(a.cfg.Acknowledgement, a.cfg.Properties)}
	switch subQueue {
	case "dead_letter":
		options.SubQueue = azservicebus.SubQueueDeadLetter
	case "transfer_dead_letter":
		options.SubQueue = azservicebus.SubQueueTransfer
	}
	var (
		receiver *azservicebus.Receiver
		err      error
	)
	if topic, subscription, ok := splitServiceBusSubscription(destination); ok {
		receiver, err = a.handle.client.NewReceiverForSubscription(topic, subscription, options)
	} else {
		receiver, err = a.handle.client.NewReceiverForQueue(destination, options)
	}
	if err != nil {
		return nil, err
	}
	a.receivers[key] = receiver
	return receiver, nil
}

// ConsumeAdvanced implements settlementAdapter: receive from an optional
// dead-letter sub-queue and, when PeekLock is set, defer settlement by stashing
// the message under a returned token instead of auto-completing.
func (a *serviceBusAdapter) ConsumeAdvanced(ctx context.Context, destination string, opts consumeOptions, timeout time.Duration) (message, error) {
	receiver, err := a.receiverFor(destination, opts.SubQueue)
	if err != nil {
		return message{}, err
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	receiveCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	messages, err := receiver.ReceiveMessages(receiveCtx, 1, nil)
	if err != nil {
		return message{}, fmt.Errorf("no JMS message received from %q within %s: %w", destination, timeout, err)
	}
	if len(messages) == 0 {
		return message{}, fmt.Errorf("no JMS message received from %q within %s", destination, timeout)
	}
	converted := fromServiceBusMessage(destination, messages[0])
	receiveAndDelete := serviceBusReceiveMode(a.cfg.Acknowledgement, a.cfg.Properties) == "receive_and_delete"
	if opts.PeekLock && !receiveAndDelete {
		// Defer settlement: hand the caller a token to settle later.
		token := hex.EncodeToString(messages[0].LockToken[:])
		a.mu.Lock()
		a.inflight[token] = inflightMessage{received: messages[0], receiver: receiver}
		a.mu.Unlock()
		converted.SettlementToken = token
		return converted, nil
	}
	if !receiveAndDelete {
		if err := receiver.CompleteMessage(ctx, messages[0], nil); err != nil {
			return converted, fmt.Errorf("complete Service Bus message from %q: %w", destination, err)
		}
	}
	return converted, nil
}

// Settle implements settlementAdapter: resolve a peek-locked message by token.
func (a *serviceBusAdapter) Settle(ctx context.Context, token string, disposition string, opts settleOptions) error {
	a.mu.Lock()
	entry, ok := a.inflight[token]
	a.mu.Unlock()
	if !ok {
		return fmt.Errorf("unknown settlement_token %q (already settled or never peek-locked)", token)
	}
	var err error
	switch disposition {
	case "abandon":
		err = entry.receiver.AbandonMessage(ctx, entry.received, nil)
	case "dead_letter":
		dlOpts := &azservicebus.DeadLetterOptions{}
		if opts.DeadLetterReason != "" {
			dlOpts.Reason = stringPtr(opts.DeadLetterReason)
		}
		if opts.DeadLetterDescription != "" {
			dlOpts.ErrorDescription = stringPtr(opts.DeadLetterDescription)
		}
		err = entry.receiver.DeadLetterMessage(ctx, entry.received, dlOpts)
	case "defer":
		err = entry.receiver.DeferMessage(ctx, entry.received, nil)
	case "complete":
		err = entry.receiver.CompleteMessage(ctx, entry.received, nil)
	default:
		// Defensive: never silently complete (acknowledge+remove) on an unknown
		// disposition. The caller normalizes, so this is unreachable in practice.
		return fmt.Errorf("unknown settle disposition %q", disposition)
	}
	if err != nil {
		return fmt.Errorf("settle Service Bus message (%s): %w", disposition, err)
	}
	// Settled (or deferred): the token is no longer live.
	a.mu.Lock()
	delete(a.inflight, token)
	a.mu.Unlock()
	return nil
}

// RenewLock implements settlementAdapter: extend the broker lock on a peek-locked
// message so a long-running settlement decision doesn't lose it.
func (a *serviceBusAdapter) RenewLock(ctx context.Context, token string) error {
	a.mu.Lock()
	entry, ok := a.inflight[token]
	a.mu.Unlock()
	if !ok {
		return fmt.Errorf("unknown settlement_token %q (already settled or never peek-locked)", token)
	}
	if err := entry.receiver.RenewMessageLock(ctx, entry.received, nil); err != nil {
		return fmt.Errorf("renew Service Bus message lock: %w", err)
	}
	return nil
}

type serviceBusConfigErrorAdapter struct {
	err error
}

func (a serviceBusConfigErrorAdapter) Publish(context.Context, string, message) (message, error) {
	return message{}, a.err
}

func (a serviceBusConfigErrorAdapter) Consume(context.Context, string, time.Duration) (message, error) {
	return message{}, a.err
}

func (a serviceBusConfigErrorAdapter) RequestReply(context.Context, string, string, message, time.Duration) (message, message, error) {
	return message{}, message{}, a.err
}

func (a serviceBusConfigErrorAdapter) HealthCheck(context.Context) error {
	return a.err
}

func (a serviceBusConfigErrorAdapter) Teardown(context.Context) error {
	return nil
}

// settlementAdapter capability: surface the underlying config error rather than a
// misleading "requires build tag" message (this IS the jms_servicebus_real build).
func (a serviceBusConfigErrorAdapter) ConsumeAdvanced(context.Context, string, consumeOptions, time.Duration) (message, error) {
	return message{}, a.err
}

func (a serviceBusConfigErrorAdapter) Settle(context.Context, string, string, settleOptions) error {
	return a.err
}

func (a serviceBusConfigErrorAdapter) RenewLock(context.Context, string) error {
	return a.err
}

type serviceBusClientPool struct {
	mu        sync.Mutex
	entries   map[string]*serviceBusPoolEntry
	newClient func(serviceBusConn) (*azservicebus.Client, error)
}

type serviceBusPoolEntry struct {
	key     string
	client  *azservicebus.Client
	refs    int
	mu      sync.Mutex
	senders map[string]*azservicebus.Sender
}

func newServiceBusClientPool() *serviceBusClientPool {
	return &serviceBusClientPool{
		entries:   make(map[string]*serviceBusPoolEntry),
		newClient: newServiceBusClient,
	}
}

func (p *serviceBusClientPool) acquire(conn serviceBusConn, key string) (*serviceBusPoolEntry, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if entry := p.entries[key]; entry != nil {
		entry.refs++
		return entry, nil
	}
	factory := p.newClient
	if factory == nil {
		factory = newServiceBusClient
	}
	client, err := factory(conn)
	if err != nil {
		return nil, err
	}
	entry := &serviceBusPoolEntry{
		key:     key,
		client:  client,
		refs:    1,
		senders: make(map[string]*azservicebus.Sender),
	}
	p.entries[key] = entry
	return entry, nil
}

func (p *serviceBusClientPool) release(ctx context.Context, entry *serviceBusPoolEntry) error {
	if entry == nil {
		return nil
	}
	p.mu.Lock()
	current := p.entries[entry.key]
	if current == nil {
		p.mu.Unlock()
		return nil
	}
	current.refs--
	if current.refs > 0 {
		p.mu.Unlock()
		return nil
	}
	delete(p.entries, entry.key)
	p.mu.Unlock()
	return current.close(ctx)
}

func (e *serviceBusPoolEntry) sender(destination string) (*azservicebus.Sender, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if sender := e.senders[destination]; sender != nil {
		return sender, nil
	}
	if e.client == nil {
		return nil, fmt.Errorf("Service Bus client is not initialized")
	}
	sender, err := e.client.NewSender(destination, nil)
	if err != nil {
		return nil, err
	}
	e.senders[destination] = sender
	return sender, nil
}

func (e *serviceBusPoolEntry) close(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	var firstErr error
	for _, sender := range e.senders {
		if err := sender.Close(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	e.senders = make(map[string]*azservicebus.Sender)
	if e.client != nil {
		if err := e.client.Close(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func newServiceBusClient(conn serviceBusConn) (*azservicebus.Client, error) {
	if conn.ConnectionString != "" {
		return azservicebus.NewClientFromConnectionString(conn.ConnectionString, nil)
	}
	credential, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, err
	}
	return azservicebus.NewClient(conn.FullyQualifiedNS, credential, nil)
}

func serviceBusAZReceiveMode(ack string, props map[string]string) azservicebus.ReceiveMode {
	if serviceBusReceiveMode(ack, props) == "receive_and_delete" {
		return azservicebus.ReceiveModeReceiveAndDelete
	}
	return azservicebus.ReceiveModePeekLock
}

func normalizeServiceBusOutgoingMessage(destination string, msg message) message {
	if msg.ID == "" {
		msg.ID = newJMSMessageID()
	}
	if msg.Destination == "" {
		msg.Destination = destination
	}
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now()
	}
	return msg
}

func toServiceBusMessage(destination string, msg message) *azservicebus.Message {
	appProps := make(map[string]any, len(msg.Properties)+len(msg.Headers))
	for k, v := range msg.Properties {
		appProps[k] = v
	}
	for k, v := range msg.Headers {
		if isServiceBusReservedHeader(k) {
			continue
		}
		appProps[k] = v
	}
	out := &azservicebus.Message{
		ApplicationProperties: appProps,
		Body:                  msg.Body,
		MessageID:             stringPtr(msg.ID),
		CorrelationID:         stringPtr(msg.CorrelationID),
		ReplyTo:               stringPtr(firstMatchingString(msg.Headers, "reply_to", "ReplyTo")),
		To:                    stringPtr(destination),
	}
	if contentType := protocol.FirstNonEmpty(
		firstMatchingString(msg.Headers, "Content-Type", "content-type", "content_type"),
		firstMatchingString(msg.Properties, "Content-Type", "content-type", "content_type"),
	); contentType != "" {
		out.ContentType = &contentType
	}
	// P2.4 advanced send fields: session routing, partition co-location, scheduled
	// enqueue, and TTL.
	if msg.SessionID != "" {
		out.SessionID = stringPtr(msg.SessionID)
	}
	if msg.PartitionKey != "" {
		out.PartitionKey = stringPtr(msg.PartitionKey)
	}
	if !msg.ScheduledEnqueueAt.IsZero() {
		enqueue := msg.ScheduledEnqueueAt
		out.ScheduledEnqueueTime = &enqueue
	}
	if msg.TimeToLive > 0 {
		ttl := msg.TimeToLive
		out.TimeToLive = &ttl
	}
	return out
}

func fromServiceBusMessage(destination string, msg *azservicebus.ReceivedMessage) message {
	out := message{
		ID:          msg.MessageID,
		Destination: destination,
		Headers:     make(map[string]string),
		Properties:  make(map[string]string),
		Body:        msg.Body,
		CreatedAt:   time.Now(),
	}
	if msg.CorrelationID != nil {
		out.CorrelationID = *msg.CorrelationID
	}
	if msg.To != nil && *msg.To != "" {
		out.Destination = *msg.To
	}
	if msg.ContentType != nil {
		out.Headers["Content-Type"] = *msg.ContentType
	}
	if msg.ReplyTo != nil {
		out.Headers["reply_to"] = *msg.ReplyTo
	}
	if msg.EnqueuedTime != nil {
		out.CreatedAt = *msg.EnqueuedTime
	}
	if msg.SessionID != nil {
		out.SessionID = *msg.SessionID
	}
	if msg.PartitionKey != nil {
		out.PartitionKey = *msg.PartitionKey
	}
	if msg.SequenceNumber != nil {
		out.SequenceNumber = *msg.SequenceNumber
	}
	for k, v := range msg.ApplicationProperties {
		out.Properties[k] = anyToString(v)
	}
	if msg.DeliveryCount > 0 {
		out.Properties["delivery_count"] = strconv.FormatUint(uint64(msg.DeliveryCount), 10)
	}
	if msg.DeadLetterReason != nil {
		out.Properties["dead_letter_reason"] = *msg.DeadLetterReason
	}
	if msg.DeadLetterErrorDescription != nil {
		out.Properties["dead_letter_error_description"] = *msg.DeadLetterErrorDescription
	}
	return out
}

func isServiceBusReservedHeader(key string) bool {
	return firstMatchingString(map[string]string{key: "x"}, "Content-Type", "content-type", "content_type", "reply_to", "ReplyTo") != ""
}

func stringPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

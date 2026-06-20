//go:build jms_servicebus_real

package jms

import (
	"context"
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
	}
}

type serviceBusAdapter struct {
	cfg       config
	conn      serviceBusConn
	handle    *serviceBusPoolEntry
	mu        sync.Mutex
	receivers map[string]*azservicebus.Receiver
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
	a.mu.Lock()
	defer a.mu.Unlock()
	if receiver := a.receivers[destination]; receiver != nil {
		return receiver, nil
	}
	options := &azservicebus.ReceiverOptions{ReceiveMode: serviceBusAZReceiveMode(a.cfg.Acknowledgement, a.cfg.Properties)}
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
	a.receivers[destination] = receiver
	return receiver, nil
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

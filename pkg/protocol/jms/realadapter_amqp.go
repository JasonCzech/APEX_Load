//go:build jms_solace_real

package jms

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	amqp "github.com/Azure/go-amqp"
	"github.com/google/uuid"
)

func newSolaceAdapter(cfg config) brokerAdapter {
	return &solaceAMQPAdapter{
		cfg:       cfg,
		senders:   make(map[string]*amqp.Sender),
		receivers: make(map[string]*amqp.Receiver),
	}
}

type solaceAMQPAdapter struct {
	cfg       config
	mu        sync.Mutex
	conn      *amqp.Conn
	session   *amqp.Session
	senders   map[string]*amqp.Sender
	receivers map[string]*amqp.Receiver
}

func (a *solaceAMQPAdapter) Publish(ctx context.Context, destination string, msg message) (message, error) {
	sender, err := a.sender(ctx, destination)
	if err != nil {
		return message{}, err
	}
	if err := sender.Send(ctx, toAMQPMessage(destination, msg), nil); err != nil {
		return message{}, err
	}
	if msg.ID == "" {
		msg.ID = newJMSMessageID()
	}
	if msg.Destination == "" {
		msg.Destination = destination
	}
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now()
	}
	return msg, nil
}

func (a *solaceAMQPAdapter) Consume(ctx context.Context, destination string, timeout time.Duration) (message, error) {
	receiver, err := a.receiver(ctx, destination)
	if err != nil {
		return message{}, err
	}
	receiveCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	msg, err := receiver.Receive(receiveCtx, nil)
	if err != nil {
		return message{}, fmt.Errorf("no JMS message received from %q within %s: %w", destination, timeout, err)
	}
	converted := fromAMQPMessage(destination, msg)
	if err := receiver.AcceptMessage(ctx, msg); err != nil {
		return converted, fmt.Errorf("accept JMS message from %q: %w", destination, err)
	}
	return converted, nil
}

func (a *solaceAMQPAdapter) RequestReply(ctx context.Context, requestDestination string, replyDestination string, request message, timeout time.Duration) (message, message, error) {
	if request.CorrelationID == "" {
		request.CorrelationID = firstNonEmpty(request.ID, newJMSMessageID())
	}
	if request.Headers == nil {
		request.Headers = make(map[string]string)
	}
	request.Headers["reply_to"] = replyDestination
	sent, err := a.Publish(ctx, requestDestination, request)
	if err != nil {
		return message{}, message{}, err
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
		if request.CorrelationID == "" || reply.CorrelationID == "" || reply.CorrelationID == request.CorrelationID {
			return sent, reply, nil
		}
	}
	return sent, message{}, fmt.Errorf("no correlated JMS reply received from %q within %s", replyDestination, timeout)
}

func (a *solaceAMQPAdapter) HealthCheck(ctx context.Context) error {
	if a.cfg.BrokerURL == "" {
		return fmt.Errorf("jms broker_url is not configured")
	}
	_, err := a.handle(ctx)
	return err
}

func (a *solaceAMQPAdapter) Teardown(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	var firstErr error
	for _, sender := range a.senders {
		if err := sender.Close(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	for _, receiver := range a.receivers {
		if err := receiver.Close(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if a.session != nil {
		if err := a.session.Close(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if a.conn != nil {
		if err := a.conn.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	a.conn = nil
	a.session = nil
	a.senders = make(map[string]*amqp.Sender)
	a.receivers = make(map[string]*amqp.Receiver)
	return firstErr
}

func (a *solaceAMQPAdapter) sender(ctx context.Context, destination string) (*amqp.Sender, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if sender := a.senders[destination]; sender != nil {
		return sender, nil
	}
	session, err := a.handleLocked(ctx)
	if err != nil {
		return nil, err
	}
	sender, err := session.NewSender(ctx, destination, nil)
	if err != nil {
		return nil, err
	}
	a.senders[destination] = sender
	return sender, nil
}

func (a *solaceAMQPAdapter) receiver(ctx context.Context, destination string) (*amqp.Receiver, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if receiver := a.receivers[destination]; receiver != nil {
		return receiver, nil
	}
	session, err := a.handleLocked(ctx)
	if err != nil {
		return nil, err
	}
	receiver, err := session.NewReceiver(ctx, destination, nil)
	if err != nil {
		return nil, err
	}
	a.receivers[destination] = receiver
	return receiver, nil
}

func (a *solaceAMQPAdapter) handle(ctx context.Context) (*amqp.Session, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.handleLocked(ctx)
}

func (a *solaceAMQPAdapter) handleLocked(ctx context.Context) (*amqp.Session, error) {
	if a.session != nil {
		return a.session, nil
	}
	brokerURL, err := a.normalizedBrokerURL()
	if err != nil {
		return nil, err
	}
	opts := &amqp.ConnOptions{}
	if a.cfg.ClientID != "" {
		opts.ContainerID = a.cfg.ClientID
	}
	if strings.EqualFold(brokerURL.Scheme, "amqps") || strings.EqualFold(brokerURL.Scheme, "amqp+ssl") || a.cfg.TLS.InsecureSkipVerify {
		opts.TLSConfig = &tls.Config{
			ServerName:         brokerURL.Hostname(),
			InsecureSkipVerify: a.cfg.TLS.InsecureSkipVerify, //nolint:gosec // operator-controlled adapter knob
		}
	}
	username := strings.TrimSpace(a.cfg.Username)
	if username != "" && a.cfg.MessageVPN != "" && !strings.Contains(username, "@") {
		username += "@" + a.cfg.MessageVPN
	}
	if username != "" || a.cfg.Password != "" {
		opts.SASLType = amqp.SASLTypePlain(username, a.cfg.Password)
	}
	conn, err := amqp.Dial(ctx, brokerURL.String(), opts)
	if err != nil {
		return nil, err
	}
	session, err := conn.NewSession(ctx, nil)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	a.conn = conn
	a.session = session
	return session, nil
}

func (a *solaceAMQPAdapter) normalizedBrokerURL() (*url.URL, error) {
	raw := strings.TrimSpace(a.cfg.BrokerURL)
	if raw == "" {
		return nil, fmt.Errorf("jms broker_url is not configured")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid JMS broker_url %q: %w", raw, err)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "amqp", "amqps", "amqp+ssl":
	case "smf", "smfs", "tcp", "tcps":
		return nil, fmt.Errorf("Solace JMS real adapter uses AMQP 1.0; convert broker_url %q to amqp://host:5672 or amqps://host:5671", raw)
	default:
		return nil, fmt.Errorf("Solace JMS real adapter requires amqp:// or amqps:// broker_url, got %q", raw)
	}
	return parsed, nil
}

func toAMQPMessage(destination string, msg message) *amqp.Message {
	id := firstNonEmpty(msg.ID, newJMSMessageID())
	createdAt := msg.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	properties := &amqp.MessageProperties{
		MessageID:    id,
		To:           &destination,
		CreationTime: &createdAt,
	}
	if msg.CorrelationID != "" {
		properties.CorrelationID = msg.CorrelationID
	}
	if contentType := firstMatchingString(msg.Headers, "Content-Type", "content-type"); contentType != "" {
		properties.ContentType = &contentType
	}
	appProps := make(map[string]any, len(msg.Properties)+len(msg.Headers))
	for k, v := range msg.Properties {
		appProps[k] = v
	}
	for k, v := range msg.Headers {
		if strings.EqualFold(k, "Content-Type") {
			continue
		}
		appProps[k] = v
	}
	return &amqp.Message{
		Properties:            properties,
		ApplicationProperties: appProps,
		Data:                  [][]byte{msg.Body},
	}
}

func fromAMQPMessage(destination string, msg *amqp.Message) message {
	out := message{
		Destination: destination,
		Properties:  make(map[string]string),
		Headers:     make(map[string]string),
		Body:        msg.GetData(),
		CreatedAt:   time.Now(),
	}
	if msg.Properties != nil {
		out.ID = anyToString(msg.Properties.MessageID)
		out.CorrelationID = anyToString(msg.Properties.CorrelationID)
		if msg.Properties.To != nil && *msg.Properties.To != "" {
			out.Destination = *msg.Properties.To
		}
		if msg.Properties.CreationTime != nil {
			out.CreatedAt = *msg.Properties.CreationTime
		}
		if msg.Properties.ContentType != nil {
			out.Headers["Content-Type"] = *msg.Properties.ContentType
		}
	}
	for k, v := range msg.ApplicationProperties {
		out.Properties[k] = anyToString(v)
	}
	if len(out.Body) == 0 {
		switch value := msg.Value.(type) {
		case string:
			out.Body = []byte(value)
		case []byte:
			out.Body = value
		}
	}
	return out
}

func firstMatchingString(values map[string]string, names ...string) string {
	for _, name := range names {
		for key, value := range values {
			if strings.EqualFold(key, name) && strings.TrimSpace(value) != "" {
				return value
			}
		}
	}
	return ""
}

func anyToString(value any) string {
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		return fmt.Sprint(typed)
	}
}

func newJMSMessageID() string {
	return "ID:" + uuid.NewString()
}

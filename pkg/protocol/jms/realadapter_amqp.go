//go:build jms_solace_real

package jms

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	amqp "github.com/Azure/go-amqp"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/messaging"
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
		request.CorrelationID = protocol.FirstNonEmpty(request.ID, newJMSMessageID())
	}
	if request.Headers == nil {
		request.Headers = make(map[string]string)
	}
	request.Headers["reply_to"] = replyDestination
	sent, err := a.Publish(ctx, requestDestination, request)
	if err != nil {
		return message{}, message{}, err
	}

	receiver, err := a.receiver(ctx, replyDestination)
	if err != nil {
		return sent, message{}, err
	}

	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		receiveCtx, cancel := context.WithTimeout(ctx, remaining)
		amqpMsg, err := receiver.Receive(receiveCtx, nil)
		cancel()
		if err != nil {
			// Timeout/cancellation: stop waiting and report no correlated reply.
			if ctx.Err() != nil {
				return sent, message{}, ctx.Err()
			}
			break
		}
		reply := fromAMQPMessage(replyDestination, amqpMsg)
		if correlationMatches(request.CorrelationID, reply.CorrelationID) {
			if err := receiver.AcceptMessage(ctx, amqpMsg); err != nil {
				return sent, reply, fmt.Errorf("accept JMS reply from %q: %w", replyDestination, err)
			}
			return sent, reply, nil
		}
		// Not ours: requeue so the owning VUser can claim it, then keep waiting.
		if err := receiver.ReleaseMessage(ctx, amqpMsg); err != nil {
			return sent, message{}, fmt.Errorf("release uncorrelated JMS reply from %q: %w", replyDestination, err)
		}
	}
	return sent, message{}, fmt.Errorf("no correlated JMS reply received from %q within %s", replyDestination, timeout)
}

// correlationMatches reports whether an observed reply correlation id satisfies a
// request correlation id, delegating to the shared messaging matcher (permissive
// when either side is empty).
func correlationMatches(requestCorrelationID, replyCorrelationID string) bool {
	return messaging.CorrelationMatches(requestCorrelationID, replyCorrelationID)
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
		tlsConfig := &tls.Config{
			ServerName:         brokerURL.Hostname(),
			InsecureSkipVerify: a.cfg.TLS.InsecureSkipVerify, //nolint:gosec // operator-controlled adapter knob
		}
		rootCAs, err := buildTLSRootCAs(a.cfg.TLS.CACertSecret, a.cfg.TLS.TrustStoreRef)
		if err != nil {
			return nil, err
		}
		if rootCAs != nil {
			tlsConfig.RootCAs = rootCAs
		}
		opts.TLSConfig = tlsConfig
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

// buildTLSRootCAs maps the operator-plumbed TLS trust configuration into a
// custom root CA pool for the embedded AMQP adapter. It is pure with respect to
// caCertSecret content (PEM string) but may read the filesystem when caCertSecret
// is a path.
//
//   - caCertSecret containing "BEGIN CERTIFICATE" is treated as inline PEM content.
//   - otherwise a non-empty caCertSecret is treated as a filesystem path to a PEM file.
//   - if only trustStoreRef (a JKS reference) is set, a clear error is returned:
//     JKS truststores are unsupported by the embedded AMQP adapter.
//   - if neither is set, (nil, nil) is returned so the platform default roots apply.
func buildTLSRootCAs(caCertSecret string, trustStoreRef string) (*x509.CertPool, error) {
	caCertSecret = strings.TrimSpace(caCertSecret)
	if caCertSecret == "" {
		if strings.TrimSpace(trustStoreRef) != "" {
			return nil, fmt.Errorf("JKS truststores (jms_tls_trust_store_ref) are unsupported by the embedded AMQP adapter; supply a PEM CA via jms_tls_ca_cert_secret instead")
		}
		return nil, nil
	}

	var pemBytes []byte
	if strings.Contains(caCertSecret, "BEGIN CERTIFICATE") {
		pemBytes = []byte(caCertSecret)
	} else {
		raw, err := os.ReadFile(caCertSecret)
		if err != nil {
			return nil, fmt.Errorf("read JMS TLS CA cert file %q: %w", caCertSecret, err)
		}
		pemBytes = raw
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		return nil, fmt.Errorf("JMS TLS CA cert (jms_tls_ca_cert_secret) contains no parseable PEM certificates")
	}
	return pool, nil
}

func toAMQPMessage(destination string, msg message) *amqp.Message {
	id := protocol.FirstNonEmpty(msg.ID, newJMSMessageID())
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
	// JMS message type (JMSType) maps to the AMQP Subject property (P2.3).
	if msg.Type != "" {
		subject := msg.Type
		properties.Subject = &subject
	}
	// Mirror any reply destination into the protocol-level ReplyTo property so
	// brokers and standards-compliant consumers route replies correctly. The
	// reply destination may arrive via headers or application properties; keep
	// the application property too for adapters that only read app properties.
	if replyTo := firstMatchingString(msg.Headers, "reply_to", "ReplyTo"); replyTo != "" {
		properties.ReplyTo = &replyTo
	} else if replyTo := firstMatchingString(msg.Properties, "reply_to", "ReplyTo"); replyTo != "" {
		properties.ReplyTo = &replyTo
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
	// Apply JMS delivery mode (durable), priority, and TTL via the AMQP message
	// header (P2.3). delivery_mode defaults to persistent, so Durable is true
	// unless explicitly non_persistent. Priority/TTL are set only when supplied.
	header := &amqp.MessageHeader{Durable: !strings.EqualFold(msg.DeliveryMode, "non_persistent")}
	if msg.Priority != nil {
		header.Priority = *msg.Priority
	}
	if msg.TimeToLive > 0 {
		header.TTL = msg.TimeToLive
	}
	out := &amqp.Message{
		Header:                header,
		Properties:            properties,
		ApplicationProperties: appProps,
	}
	// A JMS TextMessage rides the AMQP value body as a string; bytes/map/stream/
	// object (and the untyped default) use the data section.
	if strings.EqualFold(msg.Type, "text") {
		out.Value = string(msg.Body)
	} else {
		out.Data = [][]byte{msg.Body}
	}
	return out
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
		// Restore the JMS message type from the AMQP Subject so the type survives
		// a publish->consume / request->reply round-trip (P2.3).
		if msg.Properties.Subject != nil && *msg.Properties.Subject != "" {
			out.Type = *msg.Properties.Subject
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

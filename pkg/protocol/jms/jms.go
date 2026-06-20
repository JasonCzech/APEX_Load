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
	return published, evaluateAssertions(action.Assertions, result, published)
}

func (j *JMS) consume(ctx context.Context, action *protocol.Action, result *protocol.Result) (message, error) {
	destination := j.consumeDestination(action)
	if destination == "" {
		return message{}, fmt.Errorf("JMS destination is required")
	}
	result.DependencyTarget = destination
	msg, err := j.adapter.Consume(ctx, destination, j.timeout(action))
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
	return message{
		ID:            uuid.NewString(),
		CorrelationID: correlationID,
		Destination:   destination,
		Headers:       cloneStringMap(action.Headers),
		Properties:    properties,
		Body:          action.Body,
		CreatedAt:     time.Now(),
	}
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

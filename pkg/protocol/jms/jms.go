// Package jms provides a provider-neutral JMS protocol implementation.
package jms

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/stormrunner/pkg/protocol"
)

func init() {
	protocol.Register("jms-solace", func() protocol.Protocol {
		return &JMS{name: "jms-solace", provider: "solace"}
	})
}

type JMS struct {
	name        string
	provider    string
	cfg         config
	adapter     brokerAdapter
	initialized bool
}

type config struct {
	BrokerURL       string
	MessageVPN      string
	Username        string
	Password        string
	ClientID        string
	Destination     string
	DestinationType string
	ReplyTo         string
	Acknowledgement string
	DeliveryMode    string
	Timeout         time.Duration
	Properties      map[string]string
	Mock            bool
}

type brokerAdapter interface {
	Publish(ctx context.Context, destination string, msg message) (message, error)
	Consume(ctx context.Context, destination string, timeout time.Duration) (message, error)
	RequestReply(ctx context.Context, requestDestination string, replyDestination string, request message, timeout time.Duration) (message, message, error)
	HealthCheck(ctx context.Context) error
	Teardown(ctx context.Context) error
}

type message struct {
	ID            string
	CorrelationID string
	Destination   string
	Headers       map[string]string
	Properties    map[string]string
	Body          []byte
	CreatedAt     time.Time
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
		BrokerURL:       stringValue(cfg, "broker_url"),
		MessageVPN:      stringValue(cfg, "jms_message_vpn"),
		Username:        stringValue(cfg, "jms_username"),
		Password:        stringValue(cfg, "jms_password"),
		ClientID:        stringValue(cfg, "jms_client_id"),
		Destination:     stringValue(cfg, "jms_destination"),
		DestinationType: stringValue(cfg, "jms_destination_type"),
		ReplyTo:         stringValue(cfg, "jms_reply_to"),
		Acknowledgement: stringValue(cfg, "jms_acknowledgement"),
		DeliveryMode:    stringValue(cfg, "jms_delivery_mode"),
		Timeout:         durationValue(cfg, "jms_timeout_ms", 5*time.Second),
		Properties:      stringMapValue(cfg, "jms_properties"),
		Mock:            boolValue(cfg, "jms_mock"),
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
	if j.cfg.Mock {
		j.adapter = memoryAdapter{broker: broker}
	} else {
		j.adapter = unavailableSolaceAdapter{cfg: j.cfg}
	}
	j.initialized = true
	return nil
}

func (j *JMS) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	start := time.Now()
	result := &protocol.Result{
		ActionName:       action.Name,
		StartTime:        start,
		StatusCode:       200,
		Success:          true,
		Extractions:      make(map[string]string),
		DependencyType:   classifyJMSDependencyType(j.cfg.DestinationType, action),
		DependencyTarget: j.destination(action),
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
		result.Extractions["jms_operation"] = action.Type
	default:
		err = fmt.Errorf("unsupported JMS action type %q", action.Type)
	}

	result.Duration = time.Since(start)
	result.ServerTelemetry = finalizeJMSServerTelemetry(result, requestMsg, hasRequestMsg, responseMsg, hasResponseMsg)
	if err != nil {
		result.Success = false
		if result.StatusCode == 0 || result.StatusCode == 200 {
			result.StatusCode = 500
		}
		result.ErrorMessage = err.Error()
	}
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
	destination := j.destination(action)
	if destination == "" {
		return message{}, fmt.Errorf("JMS destination is required")
	}
	msg, err := j.adapter.Consume(ctx, destination, j.timeout(action))
	if err != nil {
		result.StatusCode = 204
		return message{}, err
	}
	result.BytesReceived = int64(len(msg.Body))
	addMessageExtractions(result, msg)
	result.Extractions["jms_operation"] = "consume"
	if err := runExtractors(action.Extractors, result, msg); err != nil {
		return message{}, err
	}
	return msg, evaluateAssertions(action.Assertions, result, msg)
}

func (j *JMS) requestReply(ctx context.Context, action *protocol.Action, result *protocol.Result) (message, message, error) {
	requestDestination := j.destination(action)
	if requestDestination == "" {
		return message{}, message{}, fmt.Errorf("JMS request destination is required")
	}
	replyDestination := paramString(action.Params, "reply_to", j.cfg.ReplyTo)
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

func (j *JMS) buildMessage(action *protocol.Action, destination string) message {
	correlationID := paramString(action.Params, "correlation_id", "")
	properties := cloneStringMap(j.cfg.Properties)
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
	return paramString(action.Params, "destination", firstNonEmpty(action.Target, j.cfg.Destination))
}

func addMessageExtractions(result *protocol.Result, msg message) {
	result.CorrelationID = msg.CorrelationID
	result.Extractions["jms_message_id"] = msg.ID
	result.Extractions["jms_correlation_id"] = msg.CorrelationID
	result.Extractions["jms_destination"] = msg.Destination
	result.Extractions["jms_body"] = string(msg.Body)
}

func finalizeJMSServerTelemetry(result *protocol.Result, request message, hasRequest bool, response message, hasResponse bool) *protocol.ServerTelemetry {
	if result == nil || (!hasRequest && !hasResponse) {
		return nil
	}
	telemetry := &protocol.ServerTelemetry{
		TransferMs: durationToNonNegativeMilliseconds(result.Duration),
	}
	if hasRequest {
		telemetry.RequestPayloadKB = bytesToKilobytes(int64(len(request.Body)))
	} else if result.BytesSent > 0 {
		telemetry.RequestPayloadKB = bytesToKilobytes(result.BytesSent)
	}
	if hasResponse {
		telemetry.ResponsePayloadKB = bytesToKilobytes(int64(len(response.Body)))
	} else if result.BytesReceived > 0 {
		telemetry.ResponsePayloadKB = bytesToKilobytes(result.BytesReceived)
	}
	if hasRequest && hasResponse && !request.CreatedAt.IsZero() && !response.CreatedAt.IsZero() && !response.CreatedAt.Before(request.CreatedAt) {
		serverProcessing := response.CreatedAt.Sub(request.CreatedAt)
		telemetry.ServerProcessingMs = durationToNonNegativeMilliseconds(serverProcessing)
		telemetry.TransferMs = durationToNonNegativeMilliseconds(result.Duration - serverProcessing)
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

func classifyJMSDependencyType(defaultDestinationType string, action *protocol.Action) string {
	destinationType := strings.TrimSpace(paramString(action.Params, "destination_type", defaultDestinationType))
	if destinationType == "" {
		destinationType = "queue"
	}
	return "jms_" + strings.ToLower(destinationType)
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

func lookupString(key string, values map[string]string, label string) (string, error) {
	value, ok := values[key]
	if !ok {
		return "", fmt.Errorf("JMS %s %q not found", label, key)
	}
	return value, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

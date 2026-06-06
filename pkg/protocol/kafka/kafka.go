// Package kafka provides a provider-neutral Apache Kafka protocol implementation.
//
// Kafka is a direct-execution protocol (it has no recorded source artifact), so
// it is authored directly in the DSL. Two transports are supported behind the
// kafkaClient interface: an in-memory mock broker (default for local shakeout
// and CI) and a real network adapter. The real adapter is a stub in this build;
// enable config.kafka.mock to exercise produce/consume/request-reply flows.
package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func init() {
	protocol.Register("kafka", func() protocol.Protocol {
		return &Kafka{name: "kafka"}
	})
}

const defaultMockPartitions = 4

// Kafka implements protocol.Protocol for Apache Kafka messaging.
type Kafka struct {
	name        string
	cfg         config
	client      kafkaClient
	rrCounter   uint64 // round-robin partition counter
	initialized bool
}

type config struct {
	Brokers           []string
	SecurityProtocol  string
	SASLMechanism     string
	SASLUsername      string
	SASLPassword      string
	ClientID          string
	ConsumerGroup     string
	Topic             string
	Acks              string
	Compression       string
	PartitionStrategy string
	AutoOffsetReset   string
	Partitions        int32
	Timeout           time.Duration
	Properties        map[string]string
	Mock              bool
}

func (k *Kafka) Name() string { return k.name }

func (k *Kafka) Init(_ context.Context, cfg map[string]any) error {
	k.cfg = config{
		Brokers:           stringSliceValue(cfg, "kafka_brokers"),
		SecurityProtocol:  stringValue(cfg, "kafka_security_protocol"),
		SASLMechanism:     stringValue(cfg, "kafka_sasl_mechanism"),
		SASLUsername:      stringValue(cfg, "kafka_sasl_username"),
		SASLPassword:      stringValue(cfg, "kafka_sasl_password"),
		ClientID:          stringValue(cfg, "kafka_client_id"),
		ConsumerGroup:     stringValue(cfg, "kafka_consumer_group"),
		Topic:             stringValue(cfg, "kafka_topic"),
		Acks:              stringValue(cfg, "kafka_acks"),
		Compression:       stringValue(cfg, "kafka_compression"),
		PartitionStrategy: stringValue(cfg, "kafka_partition_strategy"),
		AutoOffsetReset:   stringValue(cfg, "kafka_auto_offset_reset"),
		Partitions:        int32(intValue(cfg, "kafka_partitions", defaultMockPartitions)),
		Timeout:           durationValue(cfg, "kafka_timeout_ms", 5*time.Second),
		Properties:        stringMapValue(cfg, "kafka_properties"),
		Mock:              boolValue(cfg, "kafka_mock"),
	}
	// Secret references in either field are already interpolated by the engine
	// (`{{secret.NAME}}`); honor the dedicated secret field as a fallback.
	if k.cfg.SASLPassword == "" {
		k.cfg.SASLPassword = stringValue(cfg, "kafka_sasl_password_secret")
	}
	if k.cfg.SecurityProtocol == "" {
		k.cfg.SecurityProtocol = "plaintext"
	}
	if k.cfg.Acks == "" {
		k.cfg.Acks = "all"
	}
	if k.cfg.Compression == "" {
		k.cfg.Compression = "none"
	}
	if k.cfg.PartitionStrategy == "" {
		k.cfg.PartitionStrategy = "key-hash"
	}
	if k.cfg.AutoOffsetReset == "" {
		k.cfg.AutoOffsetReset = "latest"
	}
	if k.cfg.Partitions <= 0 {
		k.cfg.Partitions = defaultMockPartitions
	}
	if k.cfg.Timeout <= 0 {
		k.cfg.Timeout = 5 * time.Second
	}
	if k.cfg.Mock {
		k.client = mockClient{broker: sharedBroker}
	} else {
		k.client = unavailableClient{brokers: k.cfg.Brokers}
	}
	k.initialized = true
	return nil
}

func (k *Kafka) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	start := time.Now()
	topic := k.topic(action)
	result := &protocol.Result{
		ActionName:       action.Name,
		StartTime:        start,
		StatusCode:       200,
		Success:          true,
		Extractions:      make(map[string]string),
		DependencyType:   classifyKafkaDependencyType(action, k.cfg.ConsumerGroup),
		DependencyTarget: topic,
	}

	var err error
	var requestRec kafkaRecord
	var responseRec kafkaRecord
	hasRequest := false
	hasResponse := false

	switch strings.ToLower(action.Type) {
	case "kafka_produce":
		requestRec, err = k.produce(ctx, action, result)
		hasRequest = err == nil
	case "kafka_consume":
		responseRec, err = k.consume(ctx, action, result)
		hasResponse = err == nil
	case "kafka_request_reply":
		requestRec, responseRec, err = k.requestReply(ctx, action, result)
		hasRequest = err == nil
		hasResponse = err == nil
	case "kafka_commit", "kafka_seek":
		result.Extractions["kafka_operation"] = action.Type
	default:
		err = fmt.Errorf("unsupported kafka action type %q", action.Type)
	}

	result.Duration = time.Since(start)
	result.ServerTelemetry = finalizeKafkaServerTelemetry(result, requestRec, hasRequest, responseRec, hasResponse)
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

func (k *Kafka) Teardown(ctx context.Context) error {
	if k.client == nil {
		return nil
	}
	return k.client.Teardown(ctx)
}

func (k *Kafka) HealthCheck(ctx context.Context) error {
	if !k.initialized {
		return nil
	}
	if len(k.cfg.Brokers) == 0 && !k.cfg.Mock {
		return fmt.Errorf("kafka brokers are not configured")
	}
	if k.client == nil {
		return fmt.Errorf("kafka client is not initialized")
	}
	return k.client.HealthCheck(ctx)
}

func (k *Kafka) produce(ctx context.Context, action *protocol.Action, result *protocol.Result) (kafkaRecord, error) {
	topic := k.topic(action)
	if topic == "" {
		return kafkaRecord{}, fmt.Errorf("kafka topic is required")
	}
	rec := k.buildRecord(action, topic)
	meta, err := k.client.Produce(ctx, topic, rec)
	if err != nil {
		return kafkaRecord{}, err
	}
	rec.Partition = meta.Partition
	rec.Offset = meta.Offset
	rec.Timestamp = meta.Timestamp
	result.BytesSent = int64(len(rec.Value))
	result.CorrelationID = rec.CorrelationID
	addRecordExtractions(result, rec)
	result.Extractions["kafka_operation"] = "produce"
	return rec, evaluateAssertions(action.Assertions, result, rec)
}

func (k *Kafka) consume(ctx context.Context, action *protocol.Action, result *protocol.Result) (kafkaRecord, error) {
	topic := k.topic(action)
	if topic == "" {
		return kafkaRecord{}, fmt.Errorf("kafka topic is required")
	}
	rec, lag, err := k.client.Consume(ctx, topic, k.cfg.ConsumerGroup, k.timeout(action))
	if err != nil {
		result.StatusCode = 204
		return kafkaRecord{}, err
	}
	result.BytesReceived = int64(len(rec.Value))
	addRecordExtractions(result, rec)
	result.Extractions["kafka_operation"] = "consume"
	result.Extractions["kafka_consumer_lag"] = strconv.FormatInt(lag, 10)
	if err := runExtractors(action.Extractors, result, rec); err != nil {
		return kafkaRecord{}, err
	}
	return rec, evaluateAssertions(action.Assertions, result, rec)
}

func (k *Kafka) requestReply(ctx context.Context, action *protocol.Action, result *protocol.Result) (kafkaRecord, kafkaRecord, error) {
	reqTopic := k.topic(action)
	if reqTopic == "" {
		return kafkaRecord{}, kafkaRecord{}, fmt.Errorf("kafka request topic is required")
	}
	replyTopic := paramString(action.Params, "reply_to", "")
	if replyTopic == "" {
		return kafkaRecord{}, kafkaRecord{}, fmt.Errorf("kafka reply_to topic is required")
	}
	request := k.buildRecord(action, reqTopic)
	if request.CorrelationID == "" {
		request.CorrelationID = newCorrelationID()
	}
	if request.Headers == nil {
		request.Headers = make(map[string]string)
	}
	request.Headers["kafka_correlation_id"] = request.CorrelationID

	meta, response, err := k.client.RequestReply(ctx, reqTopic, replyTopic, request, k.timeout(action))
	if err != nil {
		result.StatusCode = 204
		return kafkaRecord{}, kafkaRecord{}, err
	}
	request.Partition = meta.Partition
	request.Offset = meta.Offset
	result.BytesSent = int64(len(request.Value))
	result.BytesReceived = int64(len(response.Value))
	addRecordExtractions(result, response)
	result.CorrelationID = response.CorrelationID
	result.Extractions["kafka_request_offset"] = strconv.FormatInt(meta.Offset, 10)
	result.Extractions["kafka_operation"] = "request_reply"
	if err := runExtractors(action.Extractors, result, response); err != nil {
		return kafkaRecord{}, kafkaRecord{}, err
	}
	return request, response, evaluateAssertions(action.Assertions, result, response)
}

func (k *Kafka) buildRecord(action *protocol.Action, topic string) kafkaRecord {
	key := paramString(action.Params, "key", "")
	correlationID := paramString(action.Params, "correlation_id", "")
	headers := cloneStringMap(action.Headers)
	if headers == nil && len(action.Metadata) > 0 {
		headers = make(map[string]string)
	}
	for mk, mv := range action.Metadata {
		if s, ok := mv.(string); ok {
			headers[mk] = s
		}
	}
	return kafkaRecord{
		Key:           key,
		Value:         action.Body,
		Headers:       headers,
		Topic:         topic,
		Partition:     k.resolvePartition(action, key),
		CorrelationID: correlationID,
		Timestamp:     time.Now(),
	}
}

// resolvePartition mirrors a producer's partitioner: an explicit partition param
// wins, then the configured strategy (key-hash / round-robin / explicit).
func (k *Kafka) resolvePartition(action *protocol.Action, key string) int32 {
	if p, ok := intParamOK(action.Params, "partition"); ok {
		return int32(p)
	}
	partitions := k.cfg.Partitions
	if partitions <= 0 {
		partitions = defaultMockPartitions
	}
	switch strings.ToLower(k.cfg.PartitionStrategy) {
	case "round-robin":
		n := atomic.AddUint64(&k.rrCounter, 1) - 1
		return int32(n % uint64(partitions))
	case "explicit":
		return 0
	default: // key-hash
		if key == "" {
			return 0
		}
		h := fnv.New32a()
		_, _ = h.Write([]byte(key))
		return int32(h.Sum32() % uint32(partitions))
	}
}

func (k *Kafka) topic(action *protocol.Action) string {
	return paramString(action.Params, "topic", firstNonEmpty(action.Target, k.cfg.Topic))
}

func (k *Kafka) timeout(action *protocol.Action) time.Duration {
	timeout := durationParam(action.Params, "timeout_ms", k.cfg.Timeout)
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return timeout
}

func classifyKafkaDependencyType(action *protocol.Action, defaultGroup string) string {
	group := paramString(action.Params, "consumer_group", defaultGroup)
	switch strings.ToLower(action.Type) {
	case "kafka_consume", "kafka_commit", "kafka_seek":
		if strings.TrimSpace(group) != "" {
			return "kafka_consumer_group"
		}
	}
	return "kafka_topic"
}

func addRecordExtractions(result *protocol.Result, rec kafkaRecord) {
	if rec.CorrelationID != "" {
		result.CorrelationID = rec.CorrelationID
	}
	result.Extractions["kafka_key"] = rec.Key
	result.Extractions["kafka_topic"] = rec.Topic
	result.Extractions["kafka_partition"] = strconv.FormatInt(int64(rec.Partition), 10)
	result.Extractions["kafka_offset"] = strconv.FormatInt(rec.Offset, 10)
	result.Extractions["kafka_body"] = string(rec.Value)
	if !rec.Timestamp.IsZero() {
		result.Extractions["kafka_timestamp"] = rec.Timestamp.UTC().Format(time.RFC3339Nano)
	}
	if rec.CorrelationID != "" {
		result.Extractions["kafka_correlation_id"] = rec.CorrelationID
	}
}

func finalizeKafkaServerTelemetry(result *protocol.Result, request kafkaRecord, hasRequest bool, response kafkaRecord, hasResponse bool) *protocol.ServerTelemetry {
	if result == nil || (!hasRequest && !hasResponse) {
		return nil
	}
	telemetry := &protocol.ServerTelemetry{
		TransferMs: durationToNonNegativeMilliseconds(result.Duration),
	}
	if hasRequest {
		telemetry.MessagesSent = 1
		telemetry.RequestPayloadKB = bytesToKilobytes(int64(len(request.Value)))
		telemetry.RequestSizeClass = protocol.ClassifyPayloadSize(int64(len(request.Value)))
	}
	if hasResponse {
		telemetry.MessagesReceived = 1
		telemetry.ResponsePayloadKB = bytesToKilobytes(int64(len(response.Value)))
		telemetry.ResponseSizeClass = protocol.ClassifyPayloadSize(int64(len(response.Value)))
		observedAt := result.StartTime.Add(result.Duration)
		if !response.Timestamp.IsZero() && !observedAt.Before(response.Timestamp) {
			telemetry.ConsumerLagMs = durationToNonNegativeMilliseconds(observedAt.Sub(response.Timestamp))
		}
	}
	if hasRequest && hasResponse && !request.Timestamp.IsZero() && !response.Timestamp.IsZero() && !response.Timestamp.Before(request.Timestamp) {
		latency := response.Timestamp.Sub(request.Timestamp)
		telemetry.ServerProcessingMs = durationToNonNegativeMilliseconds(latency)
		telemetry.MessageLatencyMs = durationToNonNegativeMilliseconds(latency)
	}
	telemetry.MessagesSentPerSec = messagesPerSecond(telemetry.MessagesSent, result.Duration)
	telemetry.MessagesReceivedPerSec = messagesPerSecond(telemetry.MessagesReceived, result.Duration)
	return telemetry
}

func runExtractors(extractors []protocol.Extractor, result *protocol.Result, rec kafkaRecord) error {
	for _, extractor := range extractors {
		value, err := extract(extractor, rec)
		if err != nil {
			return err
		}
		result.Extractions[extractor.Name] = value
	}
	return nil
}

func extract(ext protocol.Extractor, rec kafkaRecord) (string, error) {
	switch ext.Source {
	case "body":
		switch ext.Type {
		case "regex":
			re, err := regexp.Compile(ext.Expression)
			if err != nil {
				return "", fmt.Errorf("invalid regex: %w", err)
			}
			matches := re.FindSubmatch(rec.Value)
			if len(matches) < 2 {
				return "", fmt.Errorf("no match for regex %q", ext.Expression)
			}
			return string(matches[1]), nil
		case "jsonpath":
			return extractJSONPath(ext.Expression, rec.Value)
		default:
			return "", fmt.Errorf("unsupported kafka body extraction type: %s", ext.Type)
		}
	case "header":
		return lookupString(ext.Expression, rec.Headers, "header")
	case "key":
		return rec.Key, nil
	case "partition":
		return strconv.FormatInt(int64(rec.Partition), 10), nil
	case "offset":
		return strconv.FormatInt(rec.Offset, 10), nil
	case "correlation_id":
		return rec.CorrelationID, nil
	default:
		return "", fmt.Errorf("unsupported kafka extraction source: %s", ext.Source)
	}
}

func evaluateAssertions(assertions []protocol.Assertion, result *protocol.Result, rec kafkaRecord) error {
	for _, assertion := range assertions {
		switch assertion.Type {
		case "message_received":
			if len(rec.Value) == 0 && rec.Key == "" {
				return fmt.Errorf("expected a kafka record")
			}
		case "body_contains":
			if !strings.Contains(string(rec.Value), assertion.Value) {
				return fmt.Errorf("kafka record body does not contain %q", assertion.Value)
			}
		case "header_equals":
			if rec.Headers[assertion.Value] != fmt.Sprint(assertion.Expected) {
				return fmt.Errorf("kafka header %q = %q, want %q", assertion.Value, rec.Headers[assertion.Value], assertion.Expected)
			}
		case "key_equals":
			if rec.Key != fmt.Sprint(assertion.Expected) {
				return fmt.Errorf("kafka key = %q, want %q", rec.Key, assertion.Expected)
			}
		case "partition_equals":
			want := fmt.Sprint(assertion.Expected)
			if got := strconv.FormatInt(int64(rec.Partition), 10); got != want {
				return fmt.Errorf("kafka partition = %s, want %s", got, want)
			}
		case "offset_advanced":
			if rec.Offset < 0 {
				return fmt.Errorf("kafka offset %d did not advance", rec.Offset)
			}
		case "correlation_match":
			if rec.CorrelationID != assertion.Value {
				return fmt.Errorf("kafka correlation_id = %q, want %q", rec.CorrelationID, assertion.Value)
			}
		case "consumer_lag_below":
			if assertion.Max > 0 {
				lag, _ := strconv.Atoi(result.Extractions["kafka_consumer_lag"])
				if lag > assertion.Max {
					return fmt.Errorf("kafka consumer lag %d exceeded max %d", lag, assertion.Max)
				}
			}
		case "response_time_ms":
			if assertion.Max > 0 && result.Duration.Milliseconds() > int64(assertion.Max) {
				return fmt.Errorf("kafka response time %dms exceeded max %dms", result.Duration.Milliseconds(), assertion.Max)
			}
		default:
			return fmt.Errorf("unsupported kafka assertion type: %s", assertion.Type)
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

// --- config/param helpers (kept package-local for protocol isolation) ---

func stringValue(cfg map[string]any, key string) string {
	value, _ := cfg[key].(string)
	return value
}

func boolValue(cfg map[string]any, key string) bool {
	value, _ := cfg[key].(bool)
	return value
}

func intValue(cfg map[string]any, key string, fallback int) int {
	switch value := cfg[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	default:
		return fallback
	}
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

func intParamOK(params map[string]any, key string) (int, bool) {
	if params == nil {
		return 0, false
	}
	switch value := params[key].(type) {
	case int:
		return value, true
	case int64:
		return int(value), true
	case float64:
		return int(value), true
	default:
		return 0, false
	}
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
		return trimNonEmpty(value)
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if itemStr, ok := item.(string); ok {
				out = append(out, itemStr)
			}
		}
		return trimNonEmpty(out)
	case string:
		return trimNonEmpty(strings.Split(value, ","))
	default:
		return nil
	}
}

func trimNonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, item := range values {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func lookupString(key string, values map[string]string, label string) (string, error) {
	value, ok := values[key]
	if !ok {
		return "", fmt.Errorf("kafka %s %q not found", label, key)
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

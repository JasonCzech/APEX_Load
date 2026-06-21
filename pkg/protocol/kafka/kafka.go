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
	"github.com/jasonczech/apexload/pkg/protocol/messaging"
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
	session     *messaging.Session[string]
	valueSerde  serde            // record-value (de)serializer; rawSerde by default
	txOpen      bool             // a transaction is in progress (per-VUser)
	txBuffer    []pendingProduce // records produced within the open transaction
	rrCounter   uint64           // round-robin partition counter
	initialized bool
}

// pendingProduce is a record buffered during an open transaction; it is flushed
// to the broker on commit and discarded on abort.
type pendingProduce struct {
	topic string
	rec   kafkaRecord
}

type config struct {
	Brokers           []string
	Profile           string
	MaxMessageBytes   int
	SecurityProtocol  string
	SASLMechanism     string
	SASLUsername      string
	SASLPassword      string
	OAuthScope        string
	ClientID          string
	ConsumerGroup     string
	Topic             string
	Acks              string
	Idempotence       string
	TransactionalID   string
	Compression       string
	PartitionStrategy string
	AutoOffsetReset   string
	IsolationLevel    string
	Partitions        int32
	Timeout           time.Duration
	Serialization     string
	SchemaRegistryURL string
	Subject           string
	Schema            string
	SchemaID          int
	Properties        map[string]string
	Mock              bool

	// TLS material (consumed by the franz-go adapter under the kafka_real build tag).
	TLSInsecureSkipVerify bool
	TLSClientCertFile     string
	TLSClientKeyFile      string
	TLSCACertFile         string
}

// tlsConfigured reports whether any TLS material was supplied.
func (c config) tlsConfigured() bool {
	return c.TLSInsecureSkipVerify || c.TLSClientCertFile != "" || c.TLSClientKeyFile != "" || c.TLSCACertFile != ""
}

func (k *Kafka) Name() string { return k.name }

func (k *Kafka) Init(_ context.Context, cfg map[string]any) error {
	k.cfg = config{
		Brokers:           stringSliceValue(cfg, "kafka_brokers"),
		Profile:           strings.ToLower(strings.TrimSpace(stringValue(cfg, "kafka_profile"))),
		MaxMessageBytes:   intValue(cfg, "kafka_max_message_bytes", 0),
		SecurityProtocol:  stringValue(cfg, "kafka_security_protocol"),
		SASLMechanism:     stringValue(cfg, "kafka_sasl_mechanism"),
		SASLUsername:      stringValue(cfg, "kafka_sasl_username"),
		SASLPassword:      stringValue(cfg, "kafka_sasl_password"),
		OAuthScope:        stringValue(cfg, "kafka_oauth_scope"),
		ClientID:          stringValue(cfg, "kafka_client_id"),
		ConsumerGroup:     stringValue(cfg, "kafka_consumer_group"),
		Topic:             stringValue(cfg, "kafka_topic"),
		Acks:              stringValue(cfg, "kafka_acks"),
		Idempotence:       strings.ToLower(strings.TrimSpace(stringValue(cfg, "kafka_idempotence"))),
		TransactionalID:   stringValue(cfg, "kafka_transactional_id"),
		Compression:       stringValue(cfg, "kafka_compression"),
		PartitionStrategy: stringValue(cfg, "kafka_partition_strategy"),
		AutoOffsetReset:   stringValue(cfg, "kafka_auto_offset_reset"),
		IsolationLevel:    strings.ToLower(strings.TrimSpace(stringValue(cfg, "kafka_isolation_level"))),
		Partitions:        int32(intValue(cfg, "kafka_partitions", defaultMockPartitions)),
		Timeout:           durationValue(cfg, "kafka_timeout_ms", 5*time.Second),
		Serialization:     strings.ToLower(strings.TrimSpace(stringValue(cfg, "kafka_serialization"))),
		SchemaRegistryURL: stringValue(cfg, "kafka_schema_registry_url"),
		Subject:           stringValue(cfg, "kafka_subject"),
		Schema:            stringValue(cfg, "kafka_schema"),
		SchemaID:          intValue(cfg, "kafka_schema_id", 0),
		Properties:        stringMapValue(cfg, "kafka_properties"),
		Mock:              boolValue(cfg, "kafka_mock"),

		TLSInsecureSkipVerify: boolValue(cfg, "kafka_tls_insecure_skip_verify"),
		TLSClientCertFile:     stringValue(cfg, "kafka_tls_client_cert_file"),
		TLSClientKeyFile:      stringValue(cfg, "kafka_tls_client_key_file"),
		TLSCACertFile:         stringValue(cfg, "kafka_tls_ca_cert_file"),
	}
	// Secret references in either field are already interpolated by the engine
	// (`{{secret.NAME}}`); honor the dedicated secret field as a fallback.
	if k.cfg.SASLPassword == "" {
		k.cfg.SASLPassword = stringValue(cfg, "kafka_sasl_password_secret")
	}
	if k.cfg.Profile == "eventhubs" {
		// Azure Event Hubs Kafka endpoint: TLS is mandatory and SAS auth uses
		// PLAIN with the literal username "$ConnectionString" and the namespace
		// connection string as the password. (OAUTHBEARER/Entra ID lands in EH.2.)
		// These are defaults only — an explicit operator value is preserved and
		// the validator (not the runtime) rejects conflicting choices.
		if k.cfg.SecurityProtocol == "" {
			k.cfg.SecurityProtocol = "sasl_ssl"
		}
		if k.cfg.SASLMechanism == "" {
			k.cfg.SASLMechanism = "plain"
		}
		if k.cfg.SASLMechanism == "plain" && k.cfg.SASLUsername == "" {
			k.cfg.SASLUsername = "$ConnectionString"
		}
		if k.cfg.MaxMessageBytes <= 0 {
			k.cfg.MaxMessageBytes = 1 << 20 // 1 MiB (Standard/Premium tier cap)
		}
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
		// The real network adapter is provided by the build-tagged seam
		// (newRealClient). Default builds return an unavailable stub; build with
		// -tags kafka_real to compile in the franz-go-backed client.
		k.client = newRealClient(k.cfg)
	}
	valueSerde, err := buildSerde(k.cfg)
	if err != nil {
		return err
	}
	k.valueSerde = valueSerde
	k.session = messaging.New[string](messaging.ParseAckMode(""))
	k.initialized = true
	return nil
}

// buildSerde constructs the record-value serde from config. Default is raw
// (passthrough). json_schema frames the value (treated as serialized JSON bytes)
// in the Confluent wire format using a schema id from either a Schema Registry
// (schema_registry_url) or a static schema_id. Avro/Protobuf land in P2.2 part 2.
func buildSerde(cfg config) (serde, error) {
	if cfg.Serialization == "" || cfg.Serialization == "raw" {
		return rawSerde{}, nil
	}
	subjectFor := func(topic string) string {
		if cfg.Subject != "" {
			return cfg.Subject
		}
		return topicNameSubject(topic)
	}
	resolver, err := schemaResolver(cfg, cfg.Serialization)
	if err != nil {
		return nil, err
	}
	switch cfg.Serialization {
	case "json_schema":
		return confluentSerde{resolver: resolver, subjectForTopic: subjectFor}, nil
	case "avro":
		if cfg.Schema == "" {
			return nil, fmt.Errorf("kafka serialization avro requires config.kafka.schema (the Avro schema)")
		}
		return newAvroSerde(cfg.Schema, resolver, subjectFor)
	default:
		return nil, fmt.Errorf("kafka serialization %q is not supported", cfg.Serialization)
	}
}

// schemaResolver picks the schema id source for a registry serde: a Schema
// Registry client (schema_registry_url) or a static schema_id.
func schemaResolver(cfg config, format string) (schemaIDResolver, error) {
	registryType := "JSON"
	if format == "avro" {
		registryType = "AVRO"
	}
	switch {
	case cfg.SchemaRegistryURL != "":
		return newSchemaRegistryClient(cfg.SchemaRegistryURL, cfg.Schema, registryType), nil
	case cfg.SchemaID > 0:
		return staticSchemaID(cfg.SchemaID), nil
	default:
		return nil, fmt.Errorf("kafka serialization %s requires config.kafka.schema_registry_url or schema_id", format)
	}
}

func (k *Kafka) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	start := time.Now()
	if k.session == nil {
		k.session = messaging.New[string](messaging.ParseAckMode(""))
	}
	if k.valueSerde == nil {
		k.valueSerde = rawSerde{}
	}
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
	case "kafka_commit":
		// Offset commit: record a commit on the shared session so it feeds the
		// unified commit accounting (no redelivery semantics for Kafka offsets).
		k.session.Commit()
		result.Extractions["kafka_operation"] = "kafka_commit"
		result.ServerTelemetry = &protocol.ServerTelemetry{CommitCount: 1}
	case "kafka_seek":
		target := seekTarget(action.Params)
		if seekErr := k.client.Seek(ctx, k.topic(action), k.consumerGroup(action), target); seekErr != nil {
			err = seekErr
		} else {
			result.Extractions["kafka_operation"] = "kafka_seek"
			result.Extractions["kafka_seek_offset"] = target
		}
	case "kafka_begin_transaction":
		err = k.beginTransaction(ctx, result)
	case "kafka_commit_transaction":
		err = k.commitTransaction(ctx, result)
	case "kafka_abort_transaction":
		err = k.abortTransaction(ctx, result)
	default:
		err = fmt.Errorf("unsupported kafka action type %q", action.Type)
	}

	result.Duration = time.Since(start)
	if result.ServerTelemetry == nil {
		result.ServerTelemetry = finalizeKafkaServerTelemetry(result, requestRec, hasRequest, responseRec, hasResponse)
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

func (k *Kafka) Teardown(ctx context.Context) error {
	if k.client == nil {
		return nil
	}
	// Abort a transaction left open by the script so buffered records are not
	// silently dropped and the rollback is accounted for.
	if k.txOpen {
		_ = k.client.AbortTransaction(ctx)
		k.txOpen, k.txBuffer = false, nil
		if k.session != nil {
			k.session.Rollback()
		}
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

// seekTarget resolves the kafka_seek "offset" param (a number, "earliest", or
// "latest") to a string the client Seek understands; defaults to earliest.
func seekTarget(params map[string]any) string {
	v, ok := params["offset"]
	if !ok {
		return "earliest"
	}
	switch t := v.(type) {
	case string:
		if strings.TrimSpace(t) == "" {
			return "earliest"
		}
		return t
	case float64:
		return strconv.FormatInt(int64(t), 10)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	default:
		return "earliest"
	}
}

// checkRecordSize is a conservative pre-flight guard for the broker message-size
// cap (config.kafka.max_message_bytes; defaulted to 1 MiB by the eventhubs
// profile). It counts value + key + header bytes — an approximation of the
// encoded record size, not the exact wire size, so it errs toward catching
// oversized records early. The franz adapter additionally enforces the cap
// authoritatively via kgo.ProducerBatchMaxBytes on the real network path.
func (k *Kafka) checkRecordSize(rec kafkaRecord) error {
	if k.cfg.MaxMessageBytes <= 0 {
		return nil
	}
	size := len(rec.Value) + len(rec.Key)
	for hk, hv := range rec.Headers {
		size += len(hk) + len(hv)
	}
	if size > k.cfg.MaxMessageBytes {
		return fmt.Errorf("record %d bytes (value+key+headers) exceeds max_message_bytes %d (broker message-size cap)", size, k.cfg.MaxMessageBytes)
	}
	return nil
}

func (k *Kafka) produce(ctx context.Context, action *protocol.Action, result *protocol.Result) (kafkaRecord, error) {
	topic := k.topic(action)
	if topic == "" {
		return kafkaRecord{}, fmt.Errorf("kafka topic is required")
	}
	rec := k.buildRecord(action, topic)
	framed, serr := k.valueSerde.Serialize(ctx, topic, rec.Value)
	if serr != nil {
		return kafkaRecord{}, serr
	}
	rec.Value = framed
	if err := k.checkRecordSize(rec); err != nil {
		return kafkaRecord{}, err
	}
	if k.txOpen {
		// Transactional produce: buffer until commit so consumers (read_committed)
		// never see uncommitted records, and an abort discards them atomically.
		k.txBuffer = append(k.txBuffer, pendingProduce{topic: topic, rec: rec})
		result.BytesSent = int64(len(rec.Value))
		result.CorrelationID = rec.CorrelationID
		addRecordExtractions(result, rec)
		result.Extractions["kafka_operation"] = "produce"
		result.Extractions["kafka_transactional"] = "true"
		return rec, evaluateAssertions(action.Assertions, result, rec)
	}
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

// beginTransaction opens a producer transaction (transactional.id required). The
// franz adapter starts a real Kafka transaction; the mock relies on per-VUser
// produce buffering for commit/abort visibility.
func (k *Kafka) beginTransaction(ctx context.Context, result *protocol.Result) error {
	if k.cfg.TransactionalID == "" {
		return fmt.Errorf("kafka_begin_transaction requires config.kafka.transactional_id")
	}
	if k.txOpen {
		return fmt.Errorf("a kafka transaction is already open")
	}
	if err := k.client.BeginTransaction(ctx); err != nil {
		return err
	}
	k.txOpen = true
	k.txBuffer = nil
	result.Extractions["kafka_operation"] = "begin_transaction"
	return nil
}

// commitTransaction flushes buffered records to the broker and commits, making
// them atomically visible; it records a commit on the shared session.
func (k *Kafka) commitTransaction(ctx context.Context, result *protocol.Result) error {
	if !k.txOpen {
		return fmt.Errorf("no open kafka transaction to commit")
	}
	for _, p := range k.txBuffer {
		if _, err := k.client.Produce(ctx, p.topic, p.rec); err != nil {
			_ = k.client.AbortTransaction(ctx)
			k.txOpen, k.txBuffer = false, nil
			return err
		}
	}
	if err := k.client.CommitTransaction(ctx); err != nil {
		k.txOpen, k.txBuffer = false, nil
		return err
	}
	n := len(k.txBuffer)
	k.txOpen, k.txBuffer = false, nil
	k.session.Commit()
	result.Extractions["kafka_operation"] = "commit_transaction"
	result.Extractions["kafka_tx_records"] = strconv.Itoa(n)
	result.ServerTelemetry = &protocol.ServerTelemetry{CommitCount: 1}
	return nil
}

// abortTransaction discards buffered records (never sent) and aborts; it records
// a rollback on the shared session.
func (k *Kafka) abortTransaction(ctx context.Context, result *protocol.Result) error {
	if !k.txOpen {
		return fmt.Errorf("no open kafka transaction to abort")
	}
	n := len(k.txBuffer)
	if err := k.client.AbortTransaction(ctx); err != nil {
		k.txOpen, k.txBuffer = false, nil
		return err
	}
	k.txOpen, k.txBuffer = false, nil
	k.session.Rollback()
	result.Extractions["kafka_operation"] = "abort_transaction"
	result.Extractions["kafka_tx_records_discarded"] = strconv.Itoa(n)
	result.ServerTelemetry = &protocol.ServerTelemetry{AbortCount: 1}
	return nil
}

// consumerGroup resolves the effective consumer group for an action: the
// per-action consumer_group param falls back to config.kafka.consumer_group.
// Using it consistently keeps the broker cursor key aligned with the dependency
// type classified in classifyKafkaDependencyType.
func (k *Kafka) consumerGroup(action *protocol.Action) string {
	return paramString(action.Params, "consumer_group", k.cfg.ConsumerGroup)
}

func (k *Kafka) consume(ctx context.Context, action *protocol.Action, result *protocol.Result) (kafkaRecord, error) {
	topic := k.topic(action)
	if topic == "" {
		return kafkaRecord{}, fmt.Errorf("kafka topic is required")
	}
	rec, lag, err := k.client.Consume(ctx, topic, k.consumerGroup(action), k.timeout(action))
	if err != nil {
		result.StatusCode = 204
		return kafkaRecord{}, err
	}
	payload, derr := k.valueSerde.Deserialize(ctx, rec.Value)
	if derr != nil {
		result.StatusCode = 422
		return kafkaRecord{}, derr
	}
	rec.Value = payload
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
	if k.txOpen {
		// request_reply produces + consumes inline, which cannot honor the
		// transaction buffer's read_committed semantics; disallow it inside a tx.
		return kafkaRecord{}, kafkaRecord{}, fmt.Errorf("kafka_request_reply is not supported inside an open transaction; commit or abort first")
	}
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
	if err := k.checkRecordSize(request); err != nil {
		return kafkaRecord{}, kafkaRecord{}, err
	}

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
	return paramString(action.Params, "topic", protocol.FirstNonEmpty(action.Target, k.cfg.Topic))
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
		// Surface the broker offset backlog (highWatermark - offset - 1) that the
		// consume path records, instead of discarding it to a string extraction.
		if lag, err := strconv.ParseInt(result.Extractions["kafka_consumer_lag"], 10, 64); err == nil && lag > 0 {
			telemetry.ConsumerOffsetLag = lag
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

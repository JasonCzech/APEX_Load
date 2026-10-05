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
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/boundedcache"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/messaging"
)

var extractorRegexCache = boundedcache.NewStringCache[*regexp.Regexp](0, 0, 0)

func compileExtractorRegex(expr string) (*regexp.Regexp, error) {
	if cached, ok := extractorRegexCache.Load(expr); ok {
		return cached, nil
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, err
	}
	actual, _ := extractorRegexCache.LoadOrStore(expr, re)
	return actual, nil
}

func init() {
	protocol.Register("kafka", func() protocol.Protocol {
		return &Kafka{name: "kafka"}
	})
	protocol.RegisterRuntimeNamespaceManager("kafka", sharedBroker.acquire, sharedBroker.release)
}

const defaultMockPartitions = 4
const kafkaExtractionBodyLimit = protocol.MaxExtractionValueBytes
const kafkaMaxTransactionRecords = 10_000
const kafkaMaxTransactionBytes = 64 << 20
const kafkaTransactionAbortTimeout = 5 * time.Second

// Runtime configuration hard limits are exported so DSL validation and the
// protocol boundary enforce the same contract. They remain deliberately above
// normal Kafka values while preventing integer casts and data-driven identifiers
// from becoming generator-sized allocations or long-lived map keys.
const (
	MaxConfiguredTimeout       = time.Hour
	MaxConfiguredPartitions    = 1_000_000
	MaxConfiguredMessageBytes  = 64 << 20
	MaxConfiguredSchemaBytes   = 1 << 20
	MaxConfiguredSubjectBytes  = 4 << 10
	MaxConfiguredRegistryBytes = 8 << 10
	MaxKafkaTopicBytes         = 249
	MaxKafkaIdentifierBytes    = 4 << 10
	maxKafkaBrokers            = 256
	maxKafkaProperties         = 256
	maxKafkaConfigMapBytes     = 1 << 20
)

// Kafka implements protocol.Protocol for Apache Kafka messaging.
type Kafka struct {
	name           string
	cfg            config
	client         kafkaClient
	session        *messaging.Session[string]
	valueSerde     serde            // record-value (de)serializer; rawSerde by default
	txOpen         bool             // a transaction is in progress (per-VUser)
	txBuffer       []pendingProduce // records produced within the open transaction
	txBytes        int64
	txPoisoned     bool   // broker transaction outcome could not be confirmed
	rrCounter      uint64 // round-robin partition counter
	initialized    bool
	sharedProducer bool
}

// pendingProduce is a record buffered during an open transaction; it is flushed
// to the broker on commit and discarded on abort.
type pendingProduce struct {
	topic string
	rec   kafkaRecord
}

// atomicTransactionCommitter is implemented by the in-memory mock, whose
// no-op transaction lifecycle otherwise cannot roll back records already
// appended during a failed commit flush. Real clients rely on broker-native
// transaction isolation and intentionally do not implement this interface.
var errKafkaTransactionAborted = errors.New("Kafka transaction was not committed; retry from recovered offsets")

type atomicTransactionCommitter interface {
	CommitBufferedTransaction(ctx context.Context, pending []pendingProduce) error
}

type config struct {
	ConsumerTransactions                                                                                    bool
	Producer                                                                                                producerTuning
	TLSMinVersion, TLSMaxVersion                                                                            string
	OAuthMode, OAuthTokenURL, OAuthClientID, OAuthClientSecret, OAuthClientScope, OAuthAuthenticationMethod string
	Brokers                                                                                                 []string
	Profile                                                                                                 string
	MaxMessageBytes                                                                                         int
	SecurityProtocol                                                                                        string
	SASLMechanism                                                                                           string
	SASLUsername                                                                                            string
	SASLPassword                                                                                            string
	OAuthScope                                                                                              string
	ClientID                                                                                                string
	ConsumerGroup                                                                                           string
	Topic                                                                                                   string
	Acks                                                                                                    string
	Idempotence                                                                                             string
	TransactionalID                                                                                         string
	Compression                                                                                             string
	PartitionStrategy                                                                                       string
	AutoOffsetReset                                                                                         string
	IsolationLevel                                                                                          string
	Partitions                                                                                              int32
	Timeout                                                                                                 time.Duration
	Serialization                                                                                           string
	SchemaRegistryURL                                                                                       string
	Subject                                                                                                 string
	Schema                                                                                                  string
	SchemaID                                                                                                int
	Properties                                                                                              map[string]string
	Mock                                                                                                    bool
	AllowedTargetHosts                                                                                      []string

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

func (k *Kafka) Init(ctx context.Context, cfg map[string]any) error {
	partitions, err := boundedConfigInt(cfg, "kafka_partitions", defaultMockPartitions, 0, MaxConfiguredPartitions)
	if err != nil {
		return err
	}
	maxMessageBytes, err := boundedConfigInt(cfg, "kafka_max_message_bytes", 0, 0, MaxConfiguredMessageBytes)
	if err != nil {
		return err
	}
	timeoutMS, err := boundedConfigInt(cfg, "kafka_timeout_ms", int((5*time.Second)/time.Millisecond), 0, int(MaxConfiguredTimeout/time.Millisecond))
	if err != nil {
		return err
	}
	schemaID, err := boundedConfigInt(cfg, "kafka_schema_id", 0, 0, math.MaxInt32)
	if err != nil {
		return err
	}
	tuning, err := parseProducerTuning(cfg)
	if err != nil {
		return err
	}
	k.cfg = config{
		Producer:      tuning,
		TLSMinVersion: stringValue(cfg, "kafka_tls_min_version"), TLSMaxVersion: stringValue(cfg, "kafka_tls_max_version"),
		OAuthMode:                 stringValue(cfg, "kafka_oauth_mode"),
		OAuthTokenURL:             stringValue(cfg, "kafka_oauth_token_url"),
		OAuthClientID:             stringValue(cfg, "kafka_oauth_client_id"),
		OAuthClientSecret:         stringValue(cfg, "kafka_oauth_client_secret"),
		OAuthClientScope:          stringValue(cfg, "kafka_oauth_client_scope"),
		OAuthAuthenticationMethod: stringValue(cfg, "kafka_oauth_authentication_method"),
		Brokers:                   stringSliceValue(cfg, "kafka_brokers"),
		Profile:                   strings.ToLower(strings.TrimSpace(stringValue(cfg, "kafka_profile"))),
		MaxMessageBytes:           maxMessageBytes,
		SecurityProtocol:          stringValue(cfg, "kafka_security_protocol"),
		SASLMechanism:             stringValue(cfg, "kafka_sasl_mechanism"),
		SASLUsername:              stringValue(cfg, "kafka_sasl_username"),
		SASLPassword:              stringValue(cfg, "kafka_sasl_password"),
		OAuthScope:                stringValue(cfg, "kafka_oauth_scope"),
		ClientID:                  stringValue(cfg, "kafka_client_id"),
		ConsumerGroup:             stringValue(cfg, "kafka_consumer_group"),
		Topic:                     stringValue(cfg, "kafka_topic"),
		Acks:                      stringValue(cfg, "kafka_acks"),
		Idempotence:               strings.ToLower(strings.TrimSpace(stringValue(cfg, "kafka_idempotence"))),
		TransactionalID:           stringValue(cfg, "kafka_transactional_id"),
		ConsumerTransactions:      boolValue(cfg, "kafka_consumer_transactions"),
		Compression:               stringValue(cfg, "kafka_compression"),
		PartitionStrategy:         stringValue(cfg, "kafka_partition_strategy"),
		AutoOffsetReset:           stringValue(cfg, "kafka_auto_offset_reset"),
		IsolationLevel:            strings.ToLower(strings.TrimSpace(stringValue(cfg, "kafka_isolation_level"))),
		Partitions:                int32(partitions),
		Timeout:                   time.Duration(timeoutMS) * time.Millisecond,
		Serialization:             strings.ToLower(strings.TrimSpace(stringValue(cfg, "kafka_serialization"))),
		SchemaRegistryURL:         stringValue(cfg, "kafka_schema_registry_url"),
		Subject:                   stringValue(cfg, "kafka_subject"),
		Schema:                    stringValue(cfg, "kafka_schema"),
		SchemaID:                  schemaID,
		Properties:                stringMapValue(cfg, "kafka_properties"),
		Mock:                      boolValue(cfg, "kafka_mock"),
		AllowedTargetHosts:        protocol.TargetPolicyHosts(cfg),

		TLSInsecureSkipVerify: boolValue(cfg, "kafka_tls_insecure_skip_verify"),
		TLSClientCertFile:     stringValue(cfg, "kafka_tls_client_cert_file"),
		TLSClientKeyFile:      stringValue(cfg, "kafka_tls_client_key_file"),
		TLSCACertFile:         stringValue(cfg, "kafka_tls_ca_cert_file"),
	}
	if k.cfg.ConsumerTransactions && (k.cfg.Mock || !realKafkaAvailable || k.cfg.TransactionalID == "" || k.cfg.ConsumerGroup == "" || k.cfg.IsolationLevel != "read_committed") {
		return fmt.Errorf("Kafka consumer_transactions requires kafka_real, a non-mock consumer_group, transactional_id and read_committed isolation")
	}

	if err := validateKafkaConfigBounds(k.cfg); err != nil {
		return err
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
	if !k.cfg.Mock {
		for _, broker := range k.cfg.Brokers {
			if err := protocol.ValidateTargetAddress(broker, k.cfg.AllowedTargetHosts); err != nil {
				return err
			}
		}
	}
	if k.cfg.SchemaRegistryURL != "" {
		if err := protocol.ValidateTargetURL(k.cfg.SchemaRegistryURL, k.cfg.AllowedTargetHosts); err != nil {
			return err
		}
	}
	if len(k.cfg.Properties) != 0 {
		return fmt.Errorf("Kafka generic properties are not executable; use validated typed configuration")
	}
	if hasExplicitOAuth(k.cfg) {
		if err := validateExplicitOAuth(k.cfg); err != nil {
			return err
		}
	}
	if _, _, err := producerTLSVersions(k.cfg); err != nil {
		return err
	}
	valueSerde, err := buildSerde(k.cfg)
	if err != nil {
		return err
	}
	namespace := stringValue(cfg, "runtime_mock_namespace")
	create := func() kafkaClient {
		if k.cfg.Mock {
			sharedBroker.acquire(namespace)
			return mockClient{broker: sharedBroker, namespace: namespace, release: &sync.Once{}}
		}
		return newRealClient(k.cfg)
	}
	k.sharedProducer = false
	if lifecycle := stringValue(cfg, "kafka_producer_lifecycle"); lifecycle != "" {
		if lifecycle != "source_engine" || k.cfg.TransactionalID != "" {
			return fmt.Errorf("unsupported shared Kafka producer lifecycle")
		}
		owner, ok := cfg["kafka_producer_session"].(*ProducerSession)
		if !ok || owner == nil {
			return fmt.Errorf("Kafka source producer session is required")
		}
		k.client, err = owner.acquire(ctx, stringValue(cfg, "kafka_source_configuration"), k.cfg, namespace, create)
		if err != nil {
			return err
		}
		k.sharedProducer = true
	} else {
		k.client = create()
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
		return newSchemaRegistryClient(cfg.SchemaRegistryURL, cfg.Schema, registryType, cfg.AllowedTargetHosts), nil
	case cfg.SchemaID > 0:
		return staticSchemaID(cfg.SchemaID), nil
	default:
		return nil, fmt.Errorf("kafka serialization %s requires config.kafka.schema_registry_url or schema_id", format)
	}
}

func (k *Kafka) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	start := time.Now()
	if k.sharedProducer && (action == nil || action.Type != "kafka_produce") {
		return nil, fmt.Errorf("source-shared Kafka session only supports produce actions")
	}
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
	if err := k.validateActionBounds(action); err != nil {
		result.Success = false
		result.StatusCode = 400
		result.ErrorMessage = err.Error()
		result.Duration = time.Since(start)
		result.ProtocolEvidence = protocol.BuildProtocolEvidence(action, result)
		return result, nil
	}
	var actionCtx context.Context
	var actionCancel context.CancelFunc
	if k.sharedProducer {
		if _, explicit := action.Params["timeout_ms"]; !explicit {
			actionCtx, actionCancel = context.WithCancel(ctx)
		} else {
			actionCtx, actionCancel = context.WithTimeout(ctx, k.timeout(action))
		}
	} else {
		actionCtx, actionCancel = context.WithTimeout(ctx, k.timeout(action))
	}
	defer actionCancel()
	ctx = actionCtx

	var err error
	var requestRec kafkaRecord
	var responseRec kafkaRecord
	hasRequest := false
	hasResponse := false
	sendAttempted := false
	transactionAttempt := false

	if k.txPoisoned {
		err = fmt.Errorf("kafka client transaction state is unresolved; recreate the VUser")
	} else {
		switch strings.ToLower(action.Type) {
		case "kafka_produce":
			requestRec, err = k.produce(ctx, action, result, &hasRequest, &sendAttempted)
		case "kafka_consume":
			responseRec, err = k.consume(ctx, action, result, &hasResponse)
		case "kafka_request_reply":
			requestRec, responseRec, err = k.requestReply(ctx, action, result, &hasRequest, &hasResponse, &sendAttempted)
		case "kafka_commit":
			// Synchronously commit consumer offsets on the broker, then record the
			// commit on the shared session for unified accounting. The mock Commit is a
			// no-op; the real adapter flushes + commits offsets and surfaces failures.
			if cerr := k.client.Commit(ctx); cerr != nil {
				err = cerr
			} else {
				k.session.Commit()
				result.Extractions["kafka_operation"] = "kafka_commit"
				result.ServerTelemetry = &protocol.ServerTelemetry{CommitCount: 1}
			}
		case "kafka_seek":
			target, targetErr := seekTarget(action.Params)
			if targetErr != nil {
				err = targetErr
				break
			}
			if !k.cfg.Mock {
				if _, requested := action.Params["partition"]; requested {
					err = fmt.Errorf("real Kafka seek repositions all owned partitions; partition selection is unavailable")
					break
				}
			}
			if seekErr := k.client.Seek(ctx, k.topic(action), k.consumerGroup(action), target); seekErr != nil {
				err = seekErr
			} else {
				result.Extractions["kafka_operation"] = "kafka_seek"
				result.Extractions["kafka_seek_offset"] = target
			}
		case "kafka_begin_transaction":
			err = k.beginTransaction(ctx, result)
		case "kafka_commit_transaction":
			transactionAttempt = k.txOpen
			err = k.commitTransaction(ctx, result)
		case "kafka_abort_transaction":
			err = k.abortTransaction(ctx, result)
		default:
			err = fmt.Errorf("unsupported kafka action type %q", action.Type)
		}
	}

	result.Duration = time.Since(result.StartTime)
	if k.sharedProducer {
		result.WorkerDuration = time.Since(start)
	}
	if result.ServerTelemetry == nil {
		result.ServerTelemetry = finalizeKafkaServerTelemetry(result, requestRec, hasRequest, responseRec, hasResponse)
	}
	if result.ServerTelemetry != nil && hasResponse {
		result.ServerTelemetry.SubscriptionID = k.consumerGroup(action)
	}
	if err != nil {
		result.Success = false
		if result.StatusCode == 0 || result.StatusCode == 200 {
			result.StatusCode = 500
		}
		result.ErrorMessage = err.Error()
		if isKafkaSendAction(action.Type) && !hasRequest {
			// Count a failed produce/publish attempt so the messaging reliability
			// telemetry reflects send failures instead of always reading zero.
			if result.ServerTelemetry == nil {
				result.ServerTelemetry = &protocol.ServerTelemetry{}
			}
			result.ServerTelemetry.SendFailureCount++
		}
	}
	if isKafkaSendAction(action.Type) {
		if result.ServerTelemetry == nil {
			result.ServerTelemetry = &protocol.ServerTelemetry{}
		}
		isTransaction := strings.EqualFold(action.Type, "kafka_commit_transaction")
		protocol.AnnotateMessagingSendOutcome(result.ServerTelemetry, sendAttempted || transactionAttempt, hasRequest || isTransaction && err == nil, isTransaction)
	}
	result.ProtocolEvidence = protocol.BuildProtocolEvidence(action, result)
	return result, nil
}

// isKafkaSendAction reports whether the action attempts a produce/publish, so a
// failure is counted as a failed produce attempt (SendFailureCount). A failed
// transactional commit also fails the buffered-record flush, i.e. a send.
func isKafkaSendAction(actionType string) bool {
	switch strings.ToLower(actionType) {
	case "kafka_produce", "kafka_request_reply", "kafka_commit_transaction":
		return true
	}
	return false
}

func (k *Kafka) Teardown(ctx context.Context) error {
	if k.client == nil {
		return nil
	}
	// Abort a transaction left open by the script so buffered records are not
	// silently dropped and the rollback is accounted for.
	if k.txOpen {
		_ = k.client.AbortTransaction(ctx)
		k.txOpen, k.txBuffer, k.txBytes = false, nil, 0
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
func seekTarget(params map[string]any) (string, error) {
	v, ok := params["offset"]
	if !ok || v == "" {
		return "earliest", nil
	}
	switch t := v.(type) {
	case string:
		if t == "earliest" || t == "latest" {
			return t, nil
		}
		offset, err := strconv.ParseInt(t, 10, 64)
		if err == nil && offset >= 0 {
			return t, nil
		}
	case float64:
		if !math.IsNaN(t) && !math.IsInf(t, 0) && t >= 0 && t <= 9007199254740991 && t == math.Trunc(t) {
			return strconv.FormatInt(int64(t), 10), nil
		}
	case int:
		if t >= 0 {
			return strconv.Itoa(t), nil
		}
	case int64:
		if t >= 0 {
			return strconv.FormatInt(t, 10), nil
		}
	}
	return "", fmt.Errorf("Kafka seek offset must be earliest, latest, or an exact nonnegative integer; use a string for large offsets")
}

func (k *Kafka) checkRecordSize(rec kafkaRecord) error {
	limit := k.cfg.MaxMessageBytes
	if limit <= 0 || limit > MaxConfiguredMessageBytes {
		limit = MaxConfiguredMessageBytes
	}
	size := int64(len(rec.Value)) + int64(len(rec.Key))
	for hk, hv := range rec.Headers {
		size += int64(len(hk)) + int64(len(hv))
	}
	if size > int64(limit) {
		return fmt.Errorf("record %d bytes (value+key+headers) exceeds max_message_bytes %d (broker message-size cap)", size, limit)
	}
	return nil
}

func validateKafkaConfigBounds(cfg config) error {
	if len(cfg.Brokers) > maxKafkaBrokers {
		return fmt.Errorf("kafka brokers exceed %d entries", maxKafkaBrokers)
	}
	configBytes := 0
	for _, broker := range cfg.Brokers {
		if len(broker) > MaxKafkaIdentifierBytes {
			return fmt.Errorf("kafka broker address exceeds %d bytes", MaxKafkaIdentifierBytes)
		}
		configBytes += len(broker)
	}
	if len(cfg.Properties) > maxKafkaProperties {
		return fmt.Errorf("kafka properties exceed %d entries", maxKafkaProperties)
	}
	for key, value := range cfg.Properties {
		configBytes += len(key) + len(value)
	}
	if configBytes > maxKafkaConfigMapBytes {
		return fmt.Errorf("kafka broker/property configuration exceeds %d bytes", maxKafkaConfigMapBytes)
	}
	for name, value := range map[string]string{
		"client_id": cfg.ClientID, "consumer_group": cfg.ConsumerGroup,
		"transactional_id": cfg.TransactionalID, "oauth_scope": cfg.OAuthScope,
	} {
		if len(value) > MaxKafkaIdentifierBytes {
			return fmt.Errorf("kafka %s exceeds %d bytes", name, MaxKafkaIdentifierBytes)
		}
	}
	if len(cfg.Topic) > MaxKafkaTopicBytes {
		return fmt.Errorf("kafka topic exceeds %d bytes", MaxKafkaTopicBytes)
	}
	if len(cfg.Subject) > MaxConfiguredSubjectBytes {
		return fmt.Errorf("kafka schema subject exceeds %d bytes", MaxConfiguredSubjectBytes)
	}
	if len(cfg.Schema) > MaxConfiguredSchemaBytes {
		return fmt.Errorf("kafka schema exceeds %d bytes", MaxConfiguredSchemaBytes)
	}
	if len(cfg.SchemaRegistryURL) > MaxConfiguredRegistryBytes {
		return fmt.Errorf("kafka schema_registry_url exceeds %d bytes", MaxConfiguredRegistryBytes)
	}
	if cfg.SchemaRegistryURL != "" {
		parsed, err := url.Parse(cfg.SchemaRegistryURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return fmt.Errorf("kafka schema_registry_url must be an absolute http(s) URL")
		}
	}
	return nil
}

func (k *Kafka) validateActionBounds(action *protocol.Action) error {
	if action == nil {
		return fmt.Errorf("kafka action is required")
	}
	for name, value := range map[string]string{
		"topic":    k.topic(action),
		"reply_to": paramString(action.Params, "reply_to", ""),
	} {
		if len(value) > MaxKafkaTopicBytes {
			return fmt.Errorf("kafka %s exceeds %d bytes", name, MaxKafkaTopicBytes)
		}
	}
	for name, value := range map[string]string{
		"consumer_group": paramString(action.Params, "consumer_group", k.cfg.ConsumerGroup),
		"correlation_id": paramString(action.Params, "correlation_id", ""),
	} {
		if len(value) > MaxKafkaIdentifierBytes {
			return fmt.Errorf("kafka %s exceeds %d bytes", name, MaxKafkaIdentifierBytes)
		}
	}
	if _, _, err := optionalBoundedParamInt(action.Params, "timeout_ms", 0, int(MaxConfiguredTimeout/time.Millisecond)); err != nil {
		return err
	}
	if _, _, err := optionalBoundedParamInt(action.Params, "partition", 0, math.MaxInt32); err != nil {
		return err
	}
	return nil
}

func (k *Kafka) produce(ctx context.Context, action *protocol.Action, result *protocol.Result, accepted, attempted *bool) (observed kafkaRecord, operationErr error) {
	var sent kafkaRecord
	defer func() {
		if *accepted {
			observed = sent
		}
	}()
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
		recordBytes := int64(len(rec.Value) + len(rec.Key))
		for key, value := range rec.Headers {
			recordBytes += int64(len(key) + len(value))
		}
		if len(k.txBuffer) >= kafkaMaxTransactionRecords || k.txBytes+recordBytes > kafkaMaxTransactionBytes {
			limitErr := fmt.Errorf("kafka transaction buffer limit exceeded (records=%d bytes=%d)", len(k.txBuffer), k.txBytes)
			abortBase := ctx
			if abortBase == nil {
				abortBase = context.Background()
			}
			abortCtx, cancelAbort := context.WithTimeout(context.WithoutCancel(abortBase), kafkaTransactionAbortTimeout)
			abortErr := k.client.AbortTransaction(abortCtx)
			cancelAbort()
			k.txOpen, k.txBuffer, k.txBytes = false, nil, 0
			k.session.Rollback()
			if abortErr != nil {
				k.txPoisoned = true
				return kafkaRecord{}, fmt.Errorf("%w; abort transaction: %v", limitErr, abortErr)
			}
			return kafkaRecord{}, limitErr
		}
		k.txBuffer = append(k.txBuffer, pendingProduce{topic: topic, rec: rec})
		k.txBytes += recordBytes
		sent, *accepted = rec, true
		result.BytesSent = int64(len(rec.Value))
		result.CorrelationID = rec.CorrelationID
		if err := addRecordExtractions(result, rec); err != nil {
			return kafkaRecord{}, err
		}
		result.Extractions["kafka_operation"] = "produce"
		result.Extractions["kafka_transactional"] = "true"
		return rec, evaluateAssertions(action.Assertions, result, rec)
	}
	if k.sharedProducer {
		result.StartTime = time.Now()
	}
	*attempted = true
	meta, err := k.client.Produce(ctx, topic, rec)
	if err != nil {
		return kafkaRecord{}, err
	}
	rec.Partition = meta.Partition
	rec.Offset = meta.Offset
	rec.Timestamp = meta.Timestamp
	rec.AcknowledgementMs = meta.AcknowledgementMs
	sent, *accepted = rec, true
	result.BytesSent = int64(len(rec.Value))
	result.CorrelationID = rec.CorrelationID
	if err := addRecordExtractions(result, rec); err != nil {
		return kafkaRecord{}, err
	}
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
	k.txBytes = 0
	result.Extractions["kafka_operation"] = "begin_transaction"
	return nil
}

// commitTransaction flushes buffered records to the broker and commits, making
// them atomically visible; it records a commit on the shared session.
func (k *Kafka) commitTransaction(ctx context.Context, result *protocol.Result) error {
	if !k.txOpen {
		return fmt.Errorf("no open kafka transaction to commit")
	}
	if committer, ok := k.client.(atomicTransactionCommitter); ok {
		if err := committer.CommitBufferedTransaction(ctx, k.txBuffer); err != nil {
			abortErr := k.abortAfterTransactionFailure(ctx)
			k.txOpen, k.txBuffer, k.txBytes = false, nil, 0
			k.txPoisoned = true
			return errors.Join(err, abortErr)
		}
	} else {
		for _, p := range k.txBuffer {
			if _, err := k.client.Produce(ctx, p.topic, p.rec); err != nil {
				abortErr := k.abortAfterTransactionFailure(ctx)
				k.txOpen, k.txBuffer, k.txBytes = false, nil, 0
				if abortErr != nil {
					k.txPoisoned = true
				}
				return errors.Join(err, abortErr)
			}
		}
		if err := k.client.CommitTransaction(ctx); err != nil {
			if errors.Is(err, errKafkaTransactionAborted) {
				k.txOpen, k.txBuffer, k.txBytes = false, nil, 0
				k.session.Rollback()
				return err
			}
			abortErr := k.abortAfterTransactionFailure(ctx)
			k.txOpen, k.txBuffer, k.txBytes = false, nil, 0
			k.txPoisoned = true
			return errors.Join(err, abortErr)
		}
	}
	n := len(k.txBuffer)
	k.txOpen, k.txBuffer, k.txBytes = false, nil, 0
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
		k.txOpen, k.txBuffer, k.txBytes = false, nil, 0
		k.txPoisoned = true
		return err
	}
	k.txOpen, k.txBuffer, k.txBytes = false, nil, 0
	k.session.Rollback()
	result.Extractions["kafka_operation"] = "abort_transaction"
	result.Extractions["kafka_tx_records_discarded"] = strconv.Itoa(n)
	result.ServerTelemetry = &protocol.ServerTelemetry{AbortCount: 1}
	return nil
}

func (k *Kafka) abortAfterTransactionFailure(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	abortCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), kafkaTransactionAbortTimeout)
	defer cancel()
	return k.client.AbortTransaction(abortCtx)
}

// consumerGroup resolves the effective consumer group for an action: the
// per-action consumer_group param falls back to config.kafka.consumer_group.
// Using it consistently keeps the broker cursor key aligned with the dependency
// type classified in classifyKafkaDependencyType.
func (k *Kafka) consumerGroup(action *protocol.Action) string {
	return paramString(action.Params, "consumer_group", k.cfg.ConsumerGroup)
}

func (k *Kafka) consume(ctx context.Context, action *protocol.Action, result *protocol.Result, received *bool) (observed kafkaRecord, operationErr error) {
	var delivery kafkaRecord
	defer func() {
		if *received {
			observed = delivery
		}
	}()
	topic := k.topic(action)
	if topic == "" {
		return kafkaRecord{}, fmt.Errorf("kafka topic is required")
	}
	rec, lag, err := k.client.Consume(ctx, topic, k.consumerGroup(action), k.timeout(action))
	if err != nil {
		result.StatusCode = 204
		return kafkaRecord{}, err
	}
	delivery, *received = rec, true
	result.BytesReceived = int64(len(rec.Value))
	payload, derr := k.valueSerde.Deserialize(ctx, rec.Value)
	if derr != nil {
		result.StatusCode = 422
		return kafkaRecord{}, derr
	}
	rec.Value = payload
	delivery = rec
	result.BytesReceived = int64(len(rec.Value))
	if err := addRecordExtractions(result, rec); err != nil {
		return kafkaRecord{}, err
	}
	result.Extractions["kafka_operation"] = "consume"
	result.Extractions["kafka_consumer_lag"] = strconv.FormatInt(lag, 10)
	// Reliability detection: flag a duplicate / out-of-order delivery by broker
	// offset. Kafka offsets are per-(topic,partition), so the offset is observed
	// within that partition's own sequence space — otherwise consuming across
	// multiple partitions of a topic (the normal topology) would interleave
	// independent offset streams and read as spurious duplicates/out-of-order.
	// Kafka uses auto-ack with no rollback model, so every consume is a fresh
	// delivery (there is no redelivery to exclude).
	streamKey := topic + "/" + strconv.FormatInt(int64(rec.Partition), 10)
	if dup, ooo := k.session.ObserveKeyed(streamKey, rec.Offset); dup || ooo {
		if dup {
			result.Extractions["kafka_duplicate"] = "1"
		}
		if ooo {
			result.Extractions["kafka_out_of_order"] = "1"
		}
	}
	if err := runExtractors(action.Extractors, result, rec); err != nil {
		return kafkaRecord{}, err
	}
	return rec, evaluateAssertions(action.Assertions, result, rec)
}

func (k *Kafka) requestReply(ctx context.Context, action *protocol.Action, result *protocol.Result, sentOK, receivedOK, attempted *bool) (observedRequest, observedResponse kafkaRecord, operationErr error) {
	var sent, delivery kafkaRecord
	defer func() {
		if *sentOK {
			observedRequest = sent
		}
		if *receivedOK {
			observedResponse = delivery
		}
	}()
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
	// Frame the request value with the configured serde (Confluent/Avro), the same
	// as produce — otherwise a schema-registry consumer rejects the unframed
	// request and the on-wire encoding diverges from kafka_produce.
	framed, serr := k.valueSerde.Serialize(ctx, reqTopic, request.Value)
	if serr != nil {
		return kafkaRecord{}, kafkaRecord{}, serr
	}
	request.Value = framed
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
	*attempted = err == nil || meta.sent || meta.sendAttempted
	if err == nil || meta.sent {
		request.Partition, request.Offset, request.AcknowledgementMs = meta.Partition, meta.Offset, meta.AcknowledgementMs
		sent, *sentOK = request, true
		result.BytesSent = int64(len(request.Value))
	}
	if err == nil {
		delivery, *receivedOK = response, true
		result.BytesReceived = int64(len(response.Value))
	}
	if err != nil {
		result.StatusCode = 204
		return kafkaRecord{}, kafkaRecord{}, err
	}
	// Deframe the reply with the configured serde, the same as consume, so
	// extractors see clean decoded bytes rather than the Confluent envelope.
	payload, derr := k.valueSerde.Deserialize(ctx, response.Value)
	if derr != nil {
		result.StatusCode = 422
		return kafkaRecord{}, kafkaRecord{}, derr
	}
	response.Value = payload
	delivery = response
	request.Partition = meta.Partition
	request.Offset = meta.Offset
	request.AcknowledgementMs = meta.AcknowledgementMs
	result.BytesSent = int64(len(request.Value))
	result.BytesReceived = int64(len(response.Value))
	if err := addRecordExtractions(result, response); err != nil {
		return kafkaRecord{}, kafkaRecord{}, err
	}
	result.CorrelationID = response.CorrelationID
	result.Extractions["kafka_request_offset"] = strconv.FormatInt(meta.Offset, 10)
	result.Extractions["kafka_operation"] = "request_reply"
	if err := runExtractors(action.Extractors, result, response); err != nil {
		return kafkaRecord{}, kafkaRecord{}, err
	}
	return request, response, evaluateAssertions(action.Assertions, result, response)
}

func (k *Kafka) buildRecord(action *protocol.Action, topic string) kafkaRecord {
	key, _ := action.Params["key"].(string)
	_, keyPresent := action.Params["key"]
	if k.sharedProducer && key == "" {
		keyPresent = false
	} // DI KafkaMeter maps empty strings to null.
	correlationID := paramString(action.Params, "correlation_id", "")
	_, actionPartition := intParamOK(action.Params, "partition")
	manualPartition := actionPartition || strings.EqualFold(strings.TrimSpace(k.cfg.PartitionStrategy), "explicit")
	headers := cloneStringMap(action.Headers)
	if headers == nil && len(action.Metadata) > 0 {
		headers = make(map[string]string)
	}
	for mk, mv := range action.Metadata {
		if metadataKeyIsProvenance(mk) {
			continue
		}
		if s, ok := mv.(string); ok {
			headers[mk] = s
		}
	}
	return kafkaRecord{
		Key:             key,
		KeyPresent:      keyPresent,
		Value:           action.Body,
		Headers:         headers,
		Topic:           topic,
		Partition:       k.resolvePartition(action, key),
		ManualPartition: manualPartition,
		CorrelationID:   correlationID,
		Timestamp:       time.Now(),
	}
}

func metadataKeyIsProvenance(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	switch key {
	case "original_url", "source_path", "source_step_id", "source_profile", "source_uid", "source_node_id":
		return true
	default:
		return strings.HasPrefix(key, "asyncapi_") || strings.HasPrefix(key, "jmeter_")
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

func addRecordExtractions(result *protocol.Result, rec kafkaRecord) error {
	if rec.CorrelationID != "" {
		result.CorrelationID = rec.CorrelationID
	}
	store := func(key, value string) error {
		if err := protocol.StoreExtraction(result, key, value); err != nil {
			result.Extractions = nil
			return fmt.Errorf("kafka metadata extraction %q rejected: %w", key, err)
		}
		return nil
	}
	for key, value := range map[string]string{
		"kafka_key": rec.Key, "kafka_topic": rec.Topic,
		"kafka_partition": strconv.FormatInt(int64(rec.Partition), 10),
		"kafka_offset":    strconv.FormatInt(rec.Offset, 10),
	} {
		if err := store(key, value); err != nil {
			return err
		}
	}
	if len(rec.Value) > kafkaExtractionBodyLimit {
		if err := store("kafka_body", string(rec.Value[:kafkaExtractionBodyLimit])); err != nil {
			return err
		}
		if err := store("kafka_body_truncated", "true"); err != nil {
			return err
		}
	} else {
		if err := store("kafka_body", string(rec.Value)); err != nil {
			return err
		}
	}
	if !rec.Timestamp.IsZero() {
		if err := store("kafka_timestamp", rec.Timestamp.UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	if rec.CorrelationID != "" {
		if err := store("kafka_correlation_id", rec.CorrelationID); err != nil {
			return err
		}
	}
	return nil
}

func finalizeKafkaServerTelemetry(result *protocol.Result, request kafkaRecord, hasRequest bool, response kafkaRecord, hasResponse bool) *protocol.ServerTelemetry {
	if result == nil || (!hasRequest && !hasResponse) {
		return nil
	}
	telemetry := &protocol.ServerTelemetry{
		TransferMs: durationToNonNegativeMilliseconds(result.Duration),
		Messaging:  &protocol.MessagingDiagnostics{Version: protocol.MessagingEvidenceVersion},
	}
	if hasRequest {
		if request.Partition >= 0 && result.Extractions["kafka_transactional"] != "true" {
			telemetry.PartitionID = strconv.Itoa(int(request.Partition))
		}
		telemetry.AcknowledgementMs = request.AcknowledgementMs
		observation := protocol.MessagingDurationObservation{Metric: "producer_ack", ValueMs: request.AcknowledgementMs}
		if observation.ValueMs == nil {
			observation.UnavailableReason = "not_observed"
		}
		telemetry.Messaging.Durations = append(telemetry.Messaging.Durations, observation)
		telemetry.MessagesSent = 1
		telemetry.RequestPayloadKB = bytesToKilobytes(int64(len(request.Value)))
		telemetry.RequestSizeClass = protocol.ClassifyPayloadSize(int64(len(request.Value)))
	}
	if hasResponse {
		if !hasRequest && response.Partition >= 0 {
			telemetry.PartitionID = strconv.Itoa(int(response.Partition))
		} else if hasRequest {
			// Request and reply partitions belong to different topic populations.
			// A single partition label cannot truthfully represent both directions.
			telemetry.PartitionID = ""
		}
		telemetry.MessagesReceived = 1
		telemetry.ResponsePayloadKB = bytesToKilobytes(int64(len(response.Value)))
		telemetry.ResponseSizeClass = protocol.ClassifyPayloadSize(int64(len(response.Value)))
		observedAt := result.StartTime.Add(result.Duration)
		if !response.Timestamp.IsZero() && !observedAt.Before(response.Timestamp) {
			telemetry.ConsumerLagMs = durationToNonNegativeMilliseconds(observedAt.Sub(response.Timestamp))
			telemetry.ConsumerLagObserved = true
		}
		ageReason := "not_observed"
		if telemetry.ConsumerLagObserved {
			ageReason = "clock_unqualified"
		}
		telemetry.Messaging.Durations = append(telemetry.Messaging.Durations, protocol.MessagingDurationObservation{Metric: "enqueue_age", UnavailableReason: ageReason})
		// Surface the broker offset backlog (highWatermark - offset - 1) that the
		// consume path records, instead of discarding it to a string extraction.
		if lag, err := strconv.ParseInt(result.Extractions["kafka_consumer_lag"], 10, 64); err == nil && lag >= 0 {
			telemetry.ConsumerOffsetLag = lag
			telemetry.ConsumerOffsetLagObserved = true
		}
		if result.Extractions["kafka_duplicate"] == "1" {
			telemetry.DuplicateCount = 1
		}
		if result.Extractions["kafka_out_of_order"] == "1" {
			telemetry.OutOfOrderCount = 1
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
			switch strings.ToLower(strings.TrimSpace(extractor.OnMiss)) {
			case "", "ignore":
				continue
			case "empty":
				value = ""
			default:
				return err
			}
		}
		if err := protocol.StoreExtraction(result, extractor.Name, value); err != nil {
			result.Extractions = nil
			return fmt.Errorf("kafka extractor %q output rejected: %w", extractor.Name, err)
		}
	}
	return nil
}

func extract(ext protocol.Extractor, rec kafkaRecord) (string, error) {
	switch ext.Source {
	case "body":
		switch ext.Type {
		case "regex":
			re, err := compileExtractorRegex(ext.Expression)
			if err != nil {
				return "", fmt.Errorf("invalid regex: %w", err)
			}
			matches := re.FindSubmatch(rec.Value)
			if len(matches) == 0 {
				return "", fmt.Errorf("no match for regex %q", ext.Expression)
			}
			if len(matches) == 1 {
				return string(matches[0]), nil
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
	expr = strings.TrimSpace(expr)
	if expr == "$" || expr == "" {
		return string(body), nil
	}
	parts, err := parseJSONPathSegments(expr)
	if err != nil {
		return "", err
	}
	var current any
	if err := json.Unmarshal(body, &current); err != nil {
		return "", fmt.Errorf("invalid JSON body: %w", err)
	}
	for _, part := range parts {
		// Treat an all-digit segment as an array index ONLY when the current value
		// is actually an array. Against an object, an all-digit segment is a
		// (numeric-string) key — otherwise a key like "123" could never be reached.
		if arr, ok := current.([]any); ok {
			idx, ok := parseArrayIndex(part)
			if !ok {
				return "", fmt.Errorf("expected JSON array index, got %q", part)
			}
			if idx < 0 || idx >= len(arr) {
				return "", fmt.Errorf("JSON array index %d out of range", idx)
			}
			current = arr[idx]
			continue
		}
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

func parseJSONPathSegments(expr string) ([]string, error) {
	expr = strings.TrimSpace(expr)
	expr = strings.TrimPrefix(expr, "$")
	expr = strings.TrimPrefix(expr, ".")
	if expr == "" {
		return nil, nil
	}
	var parts []string
	for len(expr) > 0 {
		if expr[0] == '.' {
			expr = expr[1:]
			continue
		}
		if expr[0] == '[' {
			end := strings.IndexByte(expr, ']')
			if end < 0 {
				return nil, fmt.Errorf("invalid JSONPath %q", expr)
			}
			token := expr[1:end]
			if len(token) >= 2 && ((token[0] == '"' && token[len(token)-1] == '"') || (token[0] == '\'' && token[len(token)-1] == '\'')) {
				var decoded string
				if token[0] == '"' {
					if err := json.Unmarshal([]byte(token), &decoded); err != nil {
						return nil, err
					}
				} else {
					decoded = strings.ReplaceAll(token[1:len(token)-1], `\'`, `'`)
				}
				parts = append(parts, decoded)
			} else {
				parts = append(parts, token)
			}
			expr = expr[end+1:]
			continue
		}
		nextDot := strings.IndexAny(expr, ".[")
		if nextDot < 0 {
			parts = append(parts, expr)
			break
		}
		parts = append(parts, expr[:nextDot])
		expr = expr[nextDot:]
	}
	return parts, nil
}

func parseArrayIndex(value string) (int, bool) {
	if value == "" {
		return 0, false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	idx, err := strconv.Atoi(value)
	return idx, err == nil
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

func boundedConfigInt(cfg map[string]any, key string, fallback, minimum, maximum int) (int, error) {
	value, exists := cfg[key]
	if !exists {
		return fallback, nil
	}
	parsed, err := numericInt64(value)
	if err != nil || parsed < int64(minimum) || parsed > int64(maximum) {
		return 0, fmt.Errorf("kafka %s must be an integer between %d and %d", strings.TrimPrefix(key, "kafka_"), minimum, maximum)
	}
	return int(parsed), nil
}

func optionalBoundedParamInt(params map[string]any, key string, minimum, maximum int) (int, bool, error) {
	if params == nil {
		return 0, false, nil
	}
	value, exists := params[key]
	if !exists {
		return 0, false, nil
	}
	parsed, err := numericInt64(value)
	if err != nil || parsed < int64(minimum) || parsed > int64(maximum) {
		return 0, true, fmt.Errorf("kafka %s must be an integer between %d and %d", key, minimum, maximum)
	}
	return int(parsed), true, nil
}

func numericInt64(value any) (int64, error) {
	switch typed := value.(type) {
	case int:
		return int64(typed), nil
	case int64:
		return typed, nil
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || math.Trunc(typed) != typed || typed < math.MinInt64 || typed > math.MaxInt64 {
			return 0, fmt.Errorf("not an integer")
		}
		return int64(typed), nil
	case string:
		return strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
	default:
		return 0, fmt.Errorf("not an integer")
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

// Package asyncapi converts AsyncAPI 2.x and 3.x documents into APEX Load Kafka
// DSL scripts. It is the event-streaming analog of the OpenAPI converter: servers
// become brokers, channels become topics, and operations become kafka_produce /
// kafka_consume / kafka_request_reply actions.
//
// Only the Kafka protocol binding is converted. Channels served exclusively over
// other protocols (MQTT, AMQP, WebSocket, ...) are reported as review-only.
package asyncapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/convert/srcparse"
	"github.com/jasonczech/apexload/pkg/dsl"
	"gopkg.in/yaml.v3"
)

// ImportRequest is the converter entry payload.
type ImportRequest struct {
	Name      string         `json:"name,omitempty"`
	ProjectID string         `json:"project_id,omitempty"`
	Content   string         `json:"content"`
	Options   ConvertOptions `json:"options,omitempty"`
}

// ConvertOptions tunes the generated script.
type ConvertOptions struct {
	ScriptName         string   `json:"script_name,omitempty"`
	Brokers            []string `json:"brokers,omitempty"`  // override servers
	Channels           []string `json:"channels,omitempty"` // filter by channel/topic name
	IncludeAssertions  *bool    `json:"include_assertions,omitempty"`
	DefaultThinkTimeMs int      `json:"default_think_time_ms,omitempty"`
	Mock               *bool    `json:"mock,omitempty"` // default true (real adapter is stubbed)
	SchemaRegistryURL  string   `json:"schema_registry_url,omitempty"`
	SchemaID           int      `json:"schema_id,omitempty"`
	SchemaSubject      string   `json:"schema_subject,omitempty"`
	Serialization      string   `json:"serialization,omitempty"`
}

// ConvertStats summarizes what the converter saw and produced.
type ConvertStats struct {
	Format                  string   `json:"format"`
	AsyncAPIVersion         string   `json:"asyncapi_version,omitempty"`
	TotalChannels           int      `json:"total_channels"`
	TotalOperations         int      `json:"total_operations"`
	ConvertedActions        int      `json:"converted_actions"`
	ProduceActions          int      `json:"produce_actions"`
	ConsumeActions          int      `json:"consume_actions"`
	RequestReplyActions     int      `json:"request_reply_actions"`
	MessagesWithCorrelation int      `json:"messages_with_correlation"`
	ServerProtocols         []string `json:"server_protocols,omitempty"`
	Brokers                 []string `json:"brokers,omitempty"`
	SchemaFormatsDetected   []string `json:"schema_formats_detected,omitempty"`
	SkippedOperations       []string `json:"skipped_operations,omitempty"`
}

// ReviewSummary feeds the import-review UI.
type ReviewSummary struct {
	Title            string   `json:"title,omitempty"`
	Version          string   `json:"version,omitempty"`
	Brokers          []string `json:"brokers,omitempty"`
	SecurityProtocol string   `json:"security_protocol,omitempty"`
	Topics           []string `json:"topics,omitempty"`
	OperationNames   []string `json:"operation_names,omitempty"`
	DataColumns      []string `json:"data_columns,omitempty"`
}

// ConvertResult is the converter output.
type ConvertResult struct {
	Script       *dsl.Script        `json:"script"`
	Scripts      []*dsl.Script      `json:"scripts,omitempty"`
	Warnings     []string           `json:"warnings,omitempty"`
	Unsupported  diag.Diagnostics   `json:"unsupported,omitempty"`
	Stats        ConvertStats       `json:"stats"`
	Summary      ReviewSummary      `json:"summary"`
	SourceReview *sourceview.Review `json:"source_review,omitempty"`
}

// --- AsyncAPI document model (2.x + 3.x) ---

type document struct {
	AsyncAPI   string               `json:"asyncapi,omitempty" yaml:"asyncapi,omitempty"`
	Info       info                 `json:"info,omitempty" yaml:"info,omitempty"`
	Servers    map[string]server    `json:"servers,omitempty" yaml:"servers,omitempty"`
	Channels   map[string]channel   `json:"channels,omitempty" yaml:"channels,omitempty"`
	Operations map[string]operation `json:"operations,omitempty" yaml:"operations,omitempty"` // 3.x only
	Components components           `json:"components,omitempty" yaml:"components,omitempty"`
}

type info struct {
	Title   string `json:"title,omitempty" yaml:"title,omitempty"`
	Version string `json:"version,omitempty" yaml:"version,omitempty"`
}

type server struct {
	URL       string                    `json:"url,omitempty" yaml:"url,omitempty"`           // 2.x
	Host      string                    `json:"host,omitempty" yaml:"host,omitempty"`         // 3.x
	Pathname  string                    `json:"pathname,omitempty" yaml:"pathname,omitempty"` // 3.x
	Protocol  string                    `json:"protocol,omitempty" yaml:"protocol,omitempty"`
	Variables map[string]serverVariable `json:"variables,omitempty" yaml:"variables,omitempty"`
	Security  []map[string][]string     `json:"security,omitempty" yaml:"security,omitempty"`
}

type serverVariable struct {
	Default any   `json:"default,omitempty" yaml:"default,omitempty"`
	Enum    []any `json:"enum,omitempty" yaml:"enum,omitempty"`
}

type channel struct {
	Ref        string                `json:"$ref,omitempty" yaml:"$ref,omitempty"`
	Address    string                `json:"address,omitempty" yaml:"address,omitempty"` // 3.x
	Servers    []ref                 `json:"servers,omitempty" yaml:"servers,omitempty"` // 3.x
	Parameters map[string]parameter  `json:"parameters,omitempty" yaml:"parameters,omitempty"`
	Publish    *channelOperation     `json:"publish,omitempty" yaml:"publish,omitempty"`     // 2.x
	Subscribe  *channelOperation     `json:"subscribe,omitempty" yaml:"subscribe,omitempty"` // 2.x
	Messages   map[string]messageDef `json:"messages,omitempty" yaml:"messages,omitempty"`   // 3.x
	Bindings   bindings              `json:"bindings,omitempty" yaml:"bindings,omitempty"`
}

type parameter struct {
	Default  any   `json:"default,omitempty" yaml:"default,omitempty"`
	Example  any   `json:"example,omitempty" yaml:"example,omitempty"`
	Examples []any `json:"examples,omitempty" yaml:"examples,omitempty"`
	Enum     []any `json:"enum,omitempty" yaml:"enum,omitempty"`
}

type channelOperation struct {
	OperationID string      `json:"operationId,omitempty" yaml:"operationId,omitempty"`
	Summary     string      `json:"summary,omitempty" yaml:"summary,omitempty"`
	Message     *messageDef `json:"message,omitempty" yaml:"message,omitempty"`
	Bindings    bindings    `json:"bindings,omitempty" yaml:"bindings,omitempty"`
}

type operation struct {
	Action   string          `json:"action,omitempty" yaml:"action,omitempty"` // send|receive
	Channel  ref             `json:"channel,omitempty" yaml:"channel,omitempty"`
	Messages []ref           `json:"messages,omitempty" yaml:"messages,omitempty"`
	Servers  []ref           `json:"servers,omitempty" yaml:"servers,omitempty"`
	Reply    *operationReply `json:"reply,omitempty" yaml:"reply,omitempty"`
	Title    string          `json:"title,omitempty" yaml:"title,omitempty"`
	Summary  string          `json:"summary,omitempty" yaml:"summary,omitempty"`
	Bindings bindings        `json:"bindings,omitempty" yaml:"bindings,omitempty"`
}

type operationReply struct {
	Channel ref `json:"channel,omitempty" yaml:"channel,omitempty"`
}

type ref struct {
	Ref string `json:"$ref,omitempty" yaml:"$ref,omitempty"`
}

type bindings struct {
	Kafka  *kafkaBinding  `json:"kafka,omitempty" yaml:"kafka,omitempty"`
	AMQP   map[string]any `json:"amqp,omitempty" yaml:"amqp,omitempty"`
	JMS    map[string]any `json:"jms,omitempty" yaml:"jms,omitempty"`
	Solace map[string]any `json:"solace,omitempty" yaml:"solace,omitempty"`
	MQTT   map[string]any `json:"mqtt,omitempty" yaml:"mqtt,omitempty"`
}

type kafkaBinding struct {
	Topic      string `json:"topic,omitempty" yaml:"topic,omitempty"`
	Key        string `json:"key,omitempty" yaml:"key,omitempty"`
	GroupID    string `json:"groupId,omitempty" yaml:"groupId,omitempty"`
	ClientID   string `json:"clientId,omitempty" yaml:"clientId,omitempty"`
	Partitions int    `json:"partitions,omitempty" yaml:"partitions,omitempty"`
	// Partition is a pointer so an explicit `partition: 0` (a valid, pinnable
	// partition index) is distinguishable from absence and not silently dropped.
	Partition *int `json:"partition,omitempty" yaml:"partition,omitempty"`
}

type messageDef struct {
	Ref           string         `json:"$ref,omitempty" yaml:"$ref,omitempty"`
	Name          string         `json:"name,omitempty" yaml:"name,omitempty"`
	Title         string         `json:"title,omitempty" yaml:"title,omitempty"`
	Headers       schema         `json:"headers,omitempty" yaml:"headers,omitempty"`
	Payload       schema         `json:"payload,omitempty" yaml:"payload,omitempty"`
	CorrelationID *correlationID `json:"correlationId,omitempty" yaml:"correlationId,omitempty"`
	SchemaFormat  string         `json:"schemaFormat,omitempty" yaml:"schemaFormat,omitempty"`
	ContentType   string         `json:"contentType,omitempty" yaml:"contentType,omitempty"`
	OneOf         []messageDef   `json:"oneOf,omitempty" yaml:"oneOf,omitempty"`
}

type correlationID struct {
	Location    string `json:"location,omitempty" yaml:"location,omitempty"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

type components struct {
	Messages        map[string]messageDef     `json:"messages,omitempty" yaml:"messages,omitempty"`
	Schemas         map[string]schema         `json:"schemas,omitempty" yaml:"schemas,omitempty"`
	Channels        map[string]channel        `json:"channels,omitempty" yaml:"channels,omitempty"`
	SecuritySchemes map[string]securityScheme `json:"securitySchemes,omitempty" yaml:"securitySchemes,omitempty"`
}

type securityScheme struct {
	Type         string `json:"type,omitempty" yaml:"type,omitempty"`
	Scheme       string `json:"scheme,omitempty" yaml:"scheme,omitempty"`
	BearerFormat string `json:"bearerFormat,omitempty" yaml:"bearerFormat,omitempty"`
}

type schema struct {
	Ref        string            `json:"$ref,omitempty" yaml:"$ref,omitempty"`
	Type       any               `json:"type,omitempty" yaml:"type,omitempty"`
	Format     string            `json:"format,omitempty" yaml:"format,omitempty"`
	Properties map[string]schema `json:"properties,omitempty" yaml:"properties,omitempty"`
	Items      *schema           `json:"items,omitempty" yaml:"items,omitempty"`
	Values     *schema           `json:"values,omitempty" yaml:"values,omitempty"`
	Fields     []avroField       `json:"fields,omitempty" yaml:"fields,omitempty"`
	Example    any               `json:"example,omitempty" yaml:"example,omitempty"`
	Examples   []any             `json:"examples,omitempty" yaml:"examples,omitempty"`
	Enum       []any             `json:"enum,omitempty" yaml:"enum,omitempty"`
	Default    any               `json:"default,omitempty" yaml:"default,omitempty"`
	Name       string            `json:"name,omitempty" yaml:"name,omitempty"`
	Namespace  string            `json:"namespace,omitempty" yaml:"namespace,omitempty"`
}

type avroField struct {
	Name    string `json:"name,omitempty" yaml:"name,omitempty"`
	Type    any    `json:"type,omitempty" yaml:"type,omitempty"`
	Default any    `json:"default,omitempty" yaml:"default,omitempty"`
	Doc     string `json:"doc,omitempty" yaml:"doc,omitempty"`
}

type opTarget string

const (
	targetKafka  opTarget = "kafka"
	targetJMS    opTarget = "jms"
	targetReview opTarget = "review"
)

// kafkaOp is the unified, version-neutral operation the generator consumes.
type kafkaOp struct {
	name           string
	kind           string // produce | consume | request_reply
	targetProtocol opTarget
	topic          string
	replyTopic     string
	payload        schema
	headers        schema
	hasCorrelation bool
	// correlationLocation is the AsyncAPI runtime expression for the message's
	// correlation id (e.g. $message.payload#/correlationId), used to wire
	// produce-side uniqueness and consume-side extraction automatically.
	correlationLocation string
	schemaFormat        string
	schemaDocument      string
	key                 string
	consumerGroup       string
	clientID            string
	partition           *int
	partitions          int
	destinationType     string
	subscription        string
	channelName         string
}

func correlationLocationOf(msg messageDef) string {
	if msg.CorrelationID == nil {
		return ""
	}
	return strings.TrimSpace(msg.CorrelationID.Location)
}

func Convert(req ImportRequest) (*ConvertResult, error) {
	content := strings.TrimSpace(req.Content)
	if content == "" {
		return nil, errors.New("content is required")
	}
	doc, sourceFindings, err := decodeDocument([]byte(content))
	if err != nil {
		return nil, err
	}
	if len(doc.Channels) == 0 {
		return nil, errors.New("AsyncAPI document contains no channels")
	}

	var diagnostics diag.Collector
	for _, finding := range sourceFindings {
		diagnostics.Add(diag.Warning, "source document", finding.Location, finding.Detail)
	}
	sourceFile := sourceFileName(content)
	mappings := make([]sourceview.Mapping, 0)

	brokers, protocols, securityProtocol, brokerWarnings := deriveBrokers(doc, req.Options.Brokers)
	kafkaSASLMechanism := deriveKafkaSASLMechanism(doc)

	// Azure Event Hubs exposes a Kafka-protocol endpoint; when the bootstrap host
	// is *.servicebus.windows.net, pre-set the Event Hubs profile and its
	// mandatory sasl_ssl transport so the generated script targets it once mock is
	// disabled and credentials ($ConnectionString / Entra ID) are supplied.
	kafkaProfile := ""
	if detectEventHubsProfile(brokers) {
		kafkaProfile = "eventhubs"
		securityProtocol = "sasl_ssl"
	}

	// When the servers are AMQP/JMS (and not Kafka), target the JMS family instead
	// of Kafka so the import is executable (jms-solace) rather than review-only.
	jmsAvailable, jmsBroker, jmsWarnings := detectJMSTarget(doc)
	useJMS := len(brokers) == 0 && jmsAvailable

	defaultTarget := targetKafka
	if useJMS {
		defaultTarget = targetJMS
	}
	ops, schemaFormats, opWarnings, skipped := collectOperations(doc, req.Options, defaultTarget, &diagnostics, &mappings, sourceFile)
	if len(ops) == 0 {
		diagnostics.Add(diag.Warning, "AsyncAPI import produced no executable actions", "", "all detected operations were review-only, filtered, or unsupported")
	}
	ops = orderReplayOps(ops)

	mock := true
	if req.Options.Mock != nil {
		mock = *req.Options.Mock
	}

	scriptName := convertutil.FirstNonEmpty(req.Options.ScriptName, req.Name, doc.Info.Title, "Imported_AsyncAPI_Script")

	result := &ConvertResult{
		Stats: ConvertStats{
			Format:            "asyncapi",
			AsyncAPIVersion:   doc.AsyncAPI,
			TotalChannels:     len(doc.Channels),
			ServerProtocols:   protocols,
			Brokers:           brokers,
			SkippedOperations: skipped,
		},
		Summary: ReviewSummary{
			Title:            doc.Info.Title,
			Version:          doc.Info.Version,
			Brokers:          brokers,
			SecurityProtocol: securityProtocol,
		},
	}
	result.Warnings = append(result.Warnings, brokerWarnings...)
	if kafkaProfile == "eventhubs" {
		result.Warnings = append(result.Warnings, "an Azure Event Hubs Kafka endpoint was detected; config.kafka.profile was set to eventhubs (sasl_ssl). Supply sasl_username \"$ConnectionString\" and sasl_password_secret (the namespace connection string), or use Entra ID, before running against the live namespace")
	}
	result.Warnings = append(result.Warnings, opWarnings...)
	result.Warnings = append(result.Warnings, jmsWarnings...)
	if useJMS {
		result.Warnings = append(result.Warnings, "AMQP/JMS servers were detected; the import targets the jms-solace adapter (executable with -tags jms_solace_real). Confirm the destination type (queue/topic) and credentials")
	}
	if mock {
		if hasTarget(ops, targetKafka) {
			result.Warnings = append(result.Warnings, "config.kafka.mock is enabled so the generated script runs against the in-memory broker; disable it once a real Kafka adapter is configured")
		}
		if hasTarget(ops, targetJMS) {
			result.Warnings = append(result.Warnings, "config.jms.mock is enabled so the generated script runs against the in-memory broker; disable it once a real JMS adapter is configured")
		}
	}

	includeAssertions := convertutil.BoolOption(req.Options.IncludeAssertions, true)
	columns := map[string]struct{}{}
	topics := map[string]struct{}{}
	operationNames := []string{}

	grouped := groupOps(ops)
	for _, target := range []opTarget{targetKafka, targetJMS} {
		groupOps := grouped[target]
		if len(groupOps) == 0 {
			continue
		}
		script := buildScriptForOps(scriptName, target, groupOps, req.Options, brokers, kafkaProfile, securityProtocol, kafkaSASLMechanism, jmsBroker, mock, len(grouped) > 1)
		if target == targetKafka && script.Config.Kafka.AutoOffsetReset == "earliest" {
			result.Warnings = append(result.Warnings, "config.kafka.auto_offset_reset was set to earliest because the imported script produces and consumes records; this lets real-broker replay observe records produced earlier in the same run")
		}
		for i, op := range groupOps {
			var action dsl.Action
			var bodyColumns []string
			if target == targetJMS {
				action, bodyColumns = buildJMSAction(op, includeAssertions, doc)
			} else {
				action, bodyColumns = buildAction(op, includeAssertions, doc)
			}
			idx := len(script.Actions)
			script.Actions = append(script.Actions, action)
			mappings = append(mappings, sourceview.CoarseMapping(sourceFile, op.channelName+" ("+op.kind+")", fmt.Sprintf("%s.actions[%d]", script.Protocol, idx), action.Name, "action", "converted", "AsyncAPI operation was converted into a generated APEX Load action."))
			result.Stats.ConvertedActions++
			switch op.kind {
			case "produce":
				result.Stats.ProduceActions++
			case "consume":
				result.Stats.ConsumeActions++
			case "request_reply":
				result.Stats.RequestReplyActions++
			}
			if op.hasCorrelation {
				result.Stats.MessagesWithCorrelation++
			}
			topics[op.topic] = struct{}{}
			operationNames = append(operationNames, action.Name)
			for _, c := range bodyColumns {
				columns[c] = struct{}{}
			}
			_ = i
		}
		result.Scripts = append(result.Scripts, script)
	}
	if len(result.Scripts) > 0 {
		result.Script = result.Scripts[0]
	} else {
		result.Script = buildScriptForOps(scriptName, defaultTarget, nil, req.Options, brokers, kafkaProfile, securityProtocol, kafkaSASLMechanism, jmsBroker, mock, false)
		result.Scripts = []*dsl.Script{result.Script}
	}

	result.Stats.TotalOperations = len(ops)
	result.Stats.SchemaFormatsDetected = schemaFormats
	result.Summary.OperationNames = operationNames
	result.Summary.Topics = sortedKeys(topics)
	dataColumns := sortedKeys(columns)
	result.Summary.DataColumns = dataColumns
	if len(dataColumns) > 0 {
		for _, script := range result.Scripts {
			script.DataSources = []dsl.DataSource{dataSource(dataColumns)}
		}
	}
	if hasTarget(ops, targetJMS) {
		if jmsBroker == "" {
			result.Warnings = append(result.Warnings, "no broker URL was derived from servers; set config.jms.broker_url before running against a real broker")
		}
	}
	if hasTarget(ops, targetKafka) && len(brokers) == 0 {
		result.Warnings = append(result.Warnings, "no Kafka brokers were derived from servers; set config.kafka.brokers before running against a real cluster")
	}

	result.Unsupported = diagnostics.Items()
	result.SourceReview = sourceview.Build(map[string]string{sourceFile: content}, mappings)
	return result, nil
}

// decodeDocument parses an AsyncAPI document and reports structural
// ambiguities alongside it. Duplicate and non-scalar mapping keys make the
// document mean different things to different readers and are rejected; merge
// keys are idiomatic in hand-authored specs and are returned for review instead.
func decodeDocument(data []byte) (*document, []srcparse.Finding, error) {
	var doc document
	if err := json.Unmarshal(data, &doc); err == nil && doc.AsyncAPI != "" {
		if validateErr := srcparse.ValidateJSON(data, srcparse.Limits{}); validateErr != nil {
			return nil, nil, fmt.Errorf("invalid AsyncAPI document: %w", validateErr)
		}
		return &doc, nil, nil
	}
	findings, err := srcparse.ValidateYAMLMappingKeys(data, srcparse.SpecYAMLPolicy(), srcparse.Limits{})
	if err != nil {
		return nil, nil, fmt.Errorf("invalid AsyncAPI document: %w", err)
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, nil, fmt.Errorf("invalid AsyncAPI document: %w", err)
	}
	if doc.AsyncAPI == "" {
		return nil, nil, errors.New("invalid AsyncAPI document: missing asyncapi version")
	}
	return &doc, findings, nil
}

func buildScriptForOps(scriptName string, target opTarget, ops []kafkaOp, opts ConvertOptions, brokers []string, kafkaProfile, securityProtocol, kafkaSASLMechanism, jmsBroker string, mock bool, suffix bool) *dsl.Script {
	name := sanitizeName(scriptName)
	protocolName := "kafka"
	if target == targetJMS {
		protocolName = "jms-solace"
	}
	if suffix {
		name = sanitizeName(name + "_" + protocolName)
	}
	cfg := baseScriptConfig(opts)
	if target == targetJMS {
		cfg.JMS = dsl.JMSConfig{
			BrokerURL: jmsBroker,
			Provider:  "solace",
			Mock:      mock,
		}
		if opNeedsTopicSubscription(ops) {
			cfg.JMS.DestinationType = "topic"
			cfg.JMS.Subscription = "asyncapi-replay"
			cfg.JMS.Destination = firstOpTopic(ops)
		}
	} else {
		cfg.Kafka = dsl.KafkaConfig{
			Brokers:          brokers,
			Profile:          kafkaProfile,
			SecurityProtocol: securityProtocol,
			Acks:             "all",
			Partitions:       maxKafkaPartitions(ops),
			Mock:             mock,
		}
		if kafkaProfile == "eventhubs" {
			cfg.Kafka.SASLMechanism = "plain"
			cfg.Kafka.SASLUsername = "$ConnectionString"
			cfg.Kafka.SASLPasswordSecret = "eventhubs_connection_string"
		} else if kafkaSASLMechanism != "" {
			cfg.Kafka.SASLMechanism = kafkaSASLMechanism
			if cfg.Kafka.SecurityProtocol == "" || cfg.Kafka.SecurityProtocol == "plaintext" {
				cfg.Kafka.SecurityProtocol = "sasl_plaintext"
			}
		}
		if opsNeedReplayFromStart(ops) {
			cfg.Kafka.AutoOffsetReset = "earliest"
		}
		applyKafkaSerdeConfig(&cfg.Kafka, ops, opts)
	}
	return &dsl.Script{
		Name:          name,
		Version:       "1.0.0",
		Protocol:      protocolName,
		GeneratedBy:   "apexload-asyncapi-converter",
		GeneratedFrom: "asyncapi",
		CreatedAt:     time.Now(),
		Config:        cfg,
	}
}

func baseScriptConfig(opts ConvertOptions) dsl.ScriptConfig {
	return dsl.ScriptConfig{
		ThinkTime: dsl.ThinkTimeConfig{
			Type:   "fixed",
			MeanMs: float64(firstPositive(opts.DefaultThinkTimeMs, 1000)),
		},
		Pacing:        dsl.PacingConfig{Type: "none"},
		RunLogic:      dsl.RunLogicConfig{Iterations: 0, AutoTransactions: "none"},
		ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: false},
		Log:           dsl.LogConfig{Level: "standard"},
	}
}

func groupOps(ops []kafkaOp) map[opTarget][]kafkaOp {
	grouped := map[opTarget][]kafkaOp{}
	for _, op := range ops {
		target := op.targetProtocol
		if target == "" {
			target = targetKafka
		}
		if target == targetReview {
			continue
		}
		grouped[target] = append(grouped[target], op)
	}
	return grouped
}

func hasTarget(ops []kafkaOp, target opTarget) bool {
	for _, op := range ops {
		if op.targetProtocol == target {
			return true
		}
	}
	return false
}

func opsNeedReplayFromStart(ops []kafkaOp) bool {
	hasProducer := false
	hasConsumer := false
	for _, op := range ops {
		switch op.kind {
		case "produce", "request_reply":
			hasProducer = true
		case "consume":
			hasConsumer = true
		}
	}
	return hasProducer && hasConsumer
}

func maxKafkaPartitions(ops []kafkaOp) int {
	maxPartitions := 0
	for _, op := range ops {
		if op.partitions > maxPartitions {
			maxPartitions = op.partitions
		}
	}
	return maxPartitions
}

func opNeedsTopicSubscription(ops []kafkaOp) bool {
	for _, op := range ops {
		if op.destinationType == "topic" && op.kind == "consume" {
			return true
		}
	}
	return false
}

func firstOpTopic(ops []kafkaOp) string {
	for _, op := range ops {
		if strings.TrimSpace(op.topic) != "" {
			return op.topic
		}
	}
	return ""
}

func applyKafkaSerdeConfig(cfg *dsl.KafkaConfig, ops []kafkaOp, opts ConvertOptions) {
	serialization := strings.ToLower(strings.TrimSpace(opts.Serialization))
	schemaDoc := ""
	for _, op := range ops {
		if serialization == "" {
			serialization = serializationForSchemaFormat(op.schemaFormat)
		}
		if schemaDoc == "" {
			schemaDoc = op.schemaDocument
		}
	}
	if serialization == "" || serialization == "raw" {
		return
	}
	cfg.Serialization = serialization
	cfg.SchemaRegistryURL = strings.TrimSpace(opts.SchemaRegistryURL)
	cfg.SchemaID = opts.SchemaID
	cfg.Subject = strings.TrimSpace(opts.SchemaSubject)
	if schemaDoc != "" {
		cfg.Schema = schemaDoc
	}
}

func serializationForSchemaFormat(format string) string {
	f := strings.ToLower(strings.TrimSpace(format))
	switch {
	case strings.Contains(f, "avro"):
		return "avro"
	case strings.Contains(f, "jsonschema"), strings.Contains(f, "json-schema"):
		return "json_schema"
	default:
		return ""
	}
}

func orderReplayOps(ops []kafkaOp) []kafkaOp {
	out := append([]kafkaOp(nil), ops...)
	kindRank := func(kind string) int {
		switch kind {
		case "produce", "request_reply":
			return 0
		case "consume":
			return 1
		default:
			return 2
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.targetProtocol != b.targetProtocol {
			return a.targetProtocol < b.targetProtocol
		}
		// The importer can only infer replay dependency for the same messaging
		// destination. Cross-topic request/reply choreography remains in source
		// order because AsyncAPI does not generally declare a causal graph.
		if a.topic == b.topic {
			return kindRank(a.kind) < kindRank(b.kind)
		}
		return false
	})
	return out
}

func deriveBrokers(doc *document, override []string) (brokers []string, protocols []string, securityProtocol string, warnings []string) {
	if len(override) > 0 {
		brokers = trimNonEmpty(override)
	}
	protoSet := map[string]struct{}{}
	secure := false
	plain := false
	for _, name := range sortedServerKeys(doc.Servers) {
		srv := doc.Servers[name]
		proto := strings.ToLower(strings.TrimSpace(srv.Protocol))
		if proto != "" {
			protoSet[proto] = struct{}{}
		}
		if !strings.HasPrefix(proto, "kafka") {
			if proto != "" {
				warnings = append(warnings, fmt.Sprintf("server %q uses non-Kafka protocol %q and was ignored", name, srv.Protocol))
			}
			continue
		}
		if proto == "kafka-secure" {
			secure = true
		}
		if proto == "kafka" {
			plain = true
		}
		host := serverEndpoint(srv)
		if srv.Host != "" && srv.Pathname != "" {
			host = srv.Host + srv.Pathname
		}
		if host != "" && len(override) == 0 {
			brokers = append(brokers, host)
		}
	}
	protocols = sortedKeysFromSet(protoSet)
	if secure {
		securityProtocol = "sasl_ssl"
		warnings = append(warnings, "a kafka-secure server was detected; security_protocol defaulted to sasl_ssl — confirm the mechanism and credentials")
	} else {
		securityProtocol = "plaintext"
	}
	if secure && plain {
		warnings = append(warnings, "both kafka and kafka-secure servers were detected; generated Kafka config uses one security_protocol, so split or review mixed-security brokers before live replay")
	}
	return brokers, protocols, securityProtocol, warnings
}

func deriveKafkaSASLMechanism(doc *document) string {
	if doc == nil || len(doc.Components.SecuritySchemes) == 0 {
		return ""
	}
	referenced := map[string]struct{}{}
	for _, srv := range doc.Servers {
		if serverProtocolFamily(srv.Protocol) != targetKafka {
			continue
		}
		for _, requirement := range srv.Security {
			for name := range requirement {
				referenced[name] = struct{}{}
			}
		}
	}
	if len(referenced) > 0 {
		for _, name := range sortedKeysFromSet(referenced) {
			if mechanism := saslMechanismForScheme(doc.Components.SecuritySchemes[name]); mechanism != "" {
				return mechanism
			}
		}
	}
	for _, name := range sortedSecuritySchemeKeys(doc.Components.SecuritySchemes) {
		if mechanism := saslMechanismForScheme(doc.Components.SecuritySchemes[name]); mechanism != "" {
			return mechanism
		}
	}
	return ""
}

func saslMechanismForScheme(s securityScheme) string {
	raw := strings.ToLower(strings.TrimSpace(convertutil.FirstNonEmpty(s.Scheme, s.Type, s.BearerFormat)))
	raw = strings.NewReplacer("_", "-", " ", "-", ".", "-").Replace(raw)
	switch raw {
	case "plain", "user-password", "api-key":
		return "plain"
	case "scramsha256", "scram-sha256", "scram-sha-256":
		return "scram-sha-256"
	case "scramsha512", "scram-sha512", "scram-sha-512":
		return "scram-sha-512"
	case "oauth2", "oauthbearer", "oauth-bearer", "bearer", "jwt":
		return "oauthbearer"
	default:
		return ""
	}
}

// detectEventHubsProfile reports whether any bootstrap broker points at an Azure
// Event Hubs Kafka endpoint (*.servicebus.windows.net), so the importer can
// pre-set the Event Hubs profile and its mandatory sasl_ssl transport.
func detectEventHubsProfile(brokers []string) bool {
	for _, b := range brokers {
		host := strings.ToLower(strings.TrimSpace(b))
		port := ""
		if i := strings.LastIndexByte(host, ':'); i >= 0 {
			port = strings.TrimSpace(host[i+1:])
			host = host[:i]
		}
		if strings.HasSuffix(host, ".servicebus.windows.net") && port == "9093" {
			return true
		}
	}
	return false
}

func serverEndpoint(srv server) string {
	host := convertutil.FirstNonEmpty(srv.Host, srv.URL)
	if srv.Host != "" && srv.Pathname != "" {
		host = srv.Host + srv.Pathname
	}
	return applyServerVariables(host, srv.Variables)
}

func applyServerVariables(value string, variables map[string]serverVariable) string {
	out := value
	for name, variable := range variables {
		replacement := fmt.Sprint(firstNonNil(variable.Default, firstAny(variable.Enum), ""))
		if replacement == "" {
			continue
		}
		out = strings.ReplaceAll(out, "{"+name+"}", replacement)
	}
	return out
}

// detectJMSTarget reports whether any document server indicates a JMS/AMQP
// target. Kafka still takes precedence as the default target in Convert when
// Kafka brokers are present, but mixed Kafka+JMS documents need the JMS broker
// URL for the generated JMS sub-script. MQTT stays review-only: APEX Load has no
// native MQTT runtime and must not silently emit executable JMS actions that
// cannot preserve MQTT semantics.
func detectJMSTarget(doc *document) (isJMS bool, brokerURL string, warnings []string) {
	for _, name := range sortedServerKeys(doc.Servers) {
		srv := doc.Servers[name]
		proto := strings.ToLower(strings.TrimSpace(srv.Protocol))
		host := serverEndpoint(srv)
		switch proto {
		case "amqp", "amqps", "jms", "solace":
			return true, host, nil
		case "mqtt", "mqtts", "secure-mqtt":
			return false, "", []string{fmt.Sprintf("server %q uses MQTT; APEX Load has no native MQTT runtime, so MQTT operations are review-only and no executable JMS actions are emitted", name)}
		}
	}
	return false, "", nil
}

// buildJMSAction mirrors buildAction for the JMS protocol family: produce ->
// jms_publish, consume -> jms_consume, request_reply -> jms_request_reply.
func buildJMSAction(op kafkaOp, includeAssertion bool, doc *document) (dsl.Action, []string) {
	action := dsl.Action{
		Name:   sanitizeName(op.name),
		Target: op.topic,
		Metadata: map[string]any{
			"asyncapi_channel": op.channelName,
		},
	}
	params := map[string]any{}
	if op.destinationType != "" {
		params["destination_type"] = op.destinationType
	}
	if op.subscription != "" {
		params["subscription"] = op.subscription
	}
	var columns []string
	switch op.kind {
	case "produce":
		action.Type = "jms_publish"
		action.Body, columns = sampleBody(doc, op.payload, op.schemaFormat)
	case "request_reply":
		action.Type = "jms_request_reply"
		action.Body, columns = sampleBody(doc, op.payload, op.schemaFormat)
		params["reply_to"] = op.replyTopic
	default:
		action.Type = "jms_consume"
	}
	columns = append(columns, applyDeclaredHeaders(&action, op, doc)...)
	if len(params) > 0 {
		action.Params = params
	}
	columns = applyCorrelationID(&action, op, columns)
	if includeAssertion {
		action.Assertions = []dsl.Assertion{{Type: "message_received"}}
	}
	return action, columns
}

func collectOperations(doc *document, opts ConvertOptions, defaultTarget opTarget, diagnostics *diag.Collector, mappings *[]sourceview.Mapping, sourceFile string) ([]kafkaOp, []string, []string, []string) {
	var ops []kafkaOp
	var warnings []string
	var skipped []string
	schemaFormats := map[string]struct{}{}
	allowed := filterSet(opts.Channels)

	recordFormat := func(m messageDef) {
		if f := strings.TrimSpace(m.SchemaFormat); f != "" {
			schemaFormats[f] = struct{}{}
			switch {
			case isJSONSchemaFormat(f), isAvroSchemaFormat(f):
			case strings.Contains(strings.ToLower(f), "protobuf"), strings.Contains(strings.ToLower(f), "proto"):
				warnings = append(warnings, fmt.Sprintf("message schemaFormat %q is Protobuf; Protobuf import is review-only until runtime serde is available", f))
			default:
				warnings = append(warnings, fmt.Sprintf("message schemaFormat %q is not JSON Schema or Avro; the generated payload is review-only unless manually adapted", f))
			}
		}
	}

	if len(doc.Operations) > 0 {
		// AsyncAPI 3.x: top-level operations with explicit send/receive actions.
		for _, opName := range sortedOperationKeys(doc.Operations) {
			op := doc.Operations[opName]
			chName, ch, ok := resolveChannel(doc, op.Channel.Ref)
			if !ok {
				skipped = append(skipped, opName)
				diagnostics.Add(diag.Warning, "AsyncAPI operation skipped", opName, "operation channel could not be resolved")
				continue
			}
			if !channelAllowed(allowed, chName, channelTopic(chName, ch)) {
				continue
			}
			topic := channelTopic(chName, ch)
			kind := "produce"
			if strings.EqualFold(op.Action, "receive") {
				kind = "consume"
			}
			replyTopic := ""
			if op.Reply != nil {
				if rName, rCh, ok := resolveChannel(doc, op.Reply.Channel.Ref); ok {
					replyTopic = channelTopic(rName, rCh)
					kind = "request_reply"
				}
			}
			target, targetWarnings := operationTarget(doc, op, ch, defaultTarget)
			warnings = append(warnings, targetWarnings...)
			if target == targetReview {
				skipped = append(skipped, opName)
				diagnostics.Add(diag.Warning, "AsyncAPI operation review-only", opName, "operation targets a protocol without an executable APEX Load runtime")
				*mappings = append(*mappings, sourceview.CoarseMapping(sourceFile, chName, "", opName, "operation", "review_only", "Operation was not emitted because its protocol is review-only."))
				continue
			}
			messages := operationMessages(doc, op, ch)
			if len(messages) == 0 {
				messages = []namedMessage{{name: "", msg: messageDef{}}}
			}
			for _, named := range messages {
				warnDroppedOneOf(doc, diagnostics, &warnings, convertutil.FirstNonEmpty(named.name, opName), named.msg)
				msg := derefMessage(doc, named.msg)
				recordFormat(msg)
				if isReviewOnlySchemaFormat(msg.SchemaFormat) {
					skippedName := convertutil.FirstNonEmpty(named.name, opName)
					skipped = append(skipped, skippedName)
					diagnostics.Add(diag.Warning, "AsyncAPI message review-only", skippedName, "message schema format is not executable by the current Kafka/JMS runtime")
					continue
				}
				opDisplayName := convertutil.FirstNonEmpty(opName, op.Title, op.Summary)
				if named.name != "" && len(messages) > 1 {
					opDisplayName += "_" + named.name
				}
				kop := kafkaOp{
					name:                opDisplayName,
					kind:                kind,
					targetProtocol:      target,
					topic:               topic,
					replyTopic:          replyTopic,
					payload:             resolvePayload(doc, msg),
					headers:             resolveHeaders(doc, msg),
					hasCorrelation:      msg.CorrelationID != nil,
					correlationLocation: correlationLocationOf(msg),
					schemaFormat:        msg.SchemaFormat,
					schemaDocument:      schemaDocument(resolvePayload(doc, msg)),
					channelName:         chName,
				}
				applyBindingsToOp(&kop, ch.Bindings, op.Bindings)
				ops = append(ops, kop)
			}
		}
		return ops, sortedKeysFromSet(schemaFormats), warnings, uniqueSorted(skipped)
	}

	// AsyncAPI 2.x: channel-scoped publish/subscribe operations.
	for _, chName := range sortedChannelKeys(doc.Channels) {
		ch := resolveChannelValue(doc, doc.Channels[chName], map[string]bool{})
		topic := channelTopic(chName, ch)
		if !channelAllowed(allowed, chName, topic) {
			continue
		}
		target, targetWarnings := channelTarget(doc, ch, defaultTarget)
		warnings = append(warnings, targetWarnings...)
		if target == targetReview {
			skipped = append(skipped, chName)
			diagnostics.Add(diag.Warning, "AsyncAPI channel review-only", chName, "channel targets a protocol without an executable APEX Load runtime")
			*mappings = append(*mappings, sourceview.CoarseMapping(sourceFile, chName, "", chName, "channel", "review_only", "Channel protocol is review-only; no executable action emitted."))
			continue
		}
		// 2.x perspective: an external client publishes to `publish` channels
		// (produce) and consumes from `subscribe` channels (consume).
		if ch.Publish != nil {
			rawMsg := messageOf(ch.Publish)
			warnDroppedOneOf(doc, diagnostics, &warnings, convertutil.FirstNonEmpty(ch.Publish.OperationID, chName+"_publish"), rawMsg)
			msg := derefMessage(doc, rawMsg)
			recordFormat(msg)
			if isReviewOnlySchemaFormat(msg.SchemaFormat) {
				skipped = append(skipped, convertutil.FirstNonEmpty(ch.Publish.OperationID, chName+"_publish"))
				diagnostics.Add(diag.Warning, "AsyncAPI message review-only", chName, "message schema format is not executable by the current Kafka/JMS runtime")
			} else {
				kop := kafkaOp{
					name:                convertutil.FirstNonEmpty(ch.Publish.OperationID, ch.Publish.Summary, chName+"_produce"),
					kind:                "produce",
					targetProtocol:      target,
					topic:               topicWithBinding(topic, ch.Publish.Bindings),
					payload:             resolvePayload(doc, msg),
					headers:             resolveHeaders(doc, msg),
					hasCorrelation:      msg.CorrelationID != nil,
					correlationLocation: correlationLocationOf(msg),
					schemaFormat:        msg.SchemaFormat,
					schemaDocument:      schemaDocument(resolvePayload(doc, msg)),
					channelName:         chName,
				}
				applyBindingsToOp(&kop, ch.Bindings, ch.Publish.Bindings)
				ops = append(ops, kop)
			}
		}
		if ch.Subscribe != nil {
			rawMsg := messageOf(ch.Subscribe)
			warnDroppedOneOf(doc, diagnostics, &warnings, convertutil.FirstNonEmpty(ch.Subscribe.OperationID, chName+"_subscribe"), rawMsg)
			msg := derefMessage(doc, rawMsg)
			recordFormat(msg)
			if isReviewOnlySchemaFormat(msg.SchemaFormat) {
				skipped = append(skipped, convertutil.FirstNonEmpty(ch.Subscribe.OperationID, chName+"_subscribe"))
				diagnostics.Add(diag.Warning, "AsyncAPI message review-only", chName, "message schema format is not executable by the current Kafka/JMS runtime")
			} else {
				kop := kafkaOp{
					name:                convertutil.FirstNonEmpty(ch.Subscribe.OperationID, ch.Subscribe.Summary, chName+"_consume"),
					kind:                "consume",
					targetProtocol:      target,
					topic:               topicWithBinding(topic, ch.Subscribe.Bindings),
					payload:             resolvePayload(doc, msg),
					headers:             resolveHeaders(doc, msg),
					hasCorrelation:      msg.CorrelationID != nil,
					correlationLocation: correlationLocationOf(msg),
					schemaFormat:        msg.SchemaFormat,
					schemaDocument:      schemaDocument(resolvePayload(doc, msg)),
					channelName:         chName,
				}
				applyBindingsToOp(&kop, ch.Bindings, ch.Subscribe.Bindings)
				ops = append(ops, kop)
			}
		}
		if ch.Publish == nil && ch.Subscribe == nil {
			skipped = append(skipped, chName)
			diagnostics.Add(diag.Warning, "AsyncAPI channel skipped", chName, "channel declares neither publish nor subscribe operations")
			*mappings = append(*mappings, sourceview.CoarseMapping(sourceFile, chName, "", chName, "channel", "review_only", "Channel had no publish/subscribe operation to convert."))
		}
	}
	return ops, sortedKeysFromSet(schemaFormats), warnings, uniqueSorted(skipped)
}

type namedMessage struct {
	name string
	msg  messageDef
}

func operationMessages(doc *document, op operation, ch channel) []namedMessage {
	var out []namedMessage
	for _, r := range op.Messages {
		name, msg, ok := resolveOperationMessage(doc, ch, r.Ref)
		if ok {
			out = append(out, namedMessage{name: name, msg: msg})
		}
	}
	if len(out) > 0 {
		return out
	}
	for _, name := range sortedMessageKeys(ch.Messages) {
		out = append(out, namedMessage{name: name, msg: resolveMessageRef(doc, ch.Messages[name])})
	}
	if len(out) > 0 {
		return []namedMessage{out[0]}
	}
	return nil
}

func resolveOperationMessage(doc *document, ch channel, refStr string) (string, messageDef, bool) {
	// Resolve a components ref from components FIRST. A "#/components/messages/Foo"
	// ref also contains the "/messages/" substring, so the channel-message lookup
	// would otherwise win and a same-named channel message would shadow the
	// intended component message.
	if name := refName(refStr, "/components/messages/"); name != "" {
		if msg, ok := doc.Components.Messages[name]; ok {
			return name, resolveMessageRef(doc, msg), true
		}
	}
	if name := refName(refStr, "/messages/"); name != "" {
		if msg, ok := ch.Messages[name]; ok {
			return name, resolveMessageRef(doc, msg), true
		}
	}
	if strings.TrimSpace(refStr) == "" {
		return "", firstChannelMessage(doc, ch), true
	}
	return "", messageDef{}, false
}

func operationTarget(doc *document, op operation, ch channel, fallback opTarget) (opTarget, []string) {
	if target, warnings, ok := targetFromServerRefs(doc, op.Servers); ok {
		return target, warnings
	}
	return channelTarget(doc, ch, fallback)
}

func channelTarget(doc *document, ch channel, fallback opTarget) (opTarget, []string) {
	if ch.Bindings.Kafka != nil {
		return targetKafka, nil
	}
	if hasJMSBinding(ch.Bindings) {
		return targetJMS, nil
	}
	if ch.Bindings.MQTT != nil {
		return targetReview, []string{"MQTT bindings are review-only; APEX Load has no native MQTT runtime"}
	}
	if target, warnings, ok := targetFromServerRefs(doc, ch.Servers); ok {
		return target, warnings
	}
	if hasOnlyMQTTServers(doc) {
		return targetReview, []string{"MQTT servers are review-only; APEX Load has no native MQTT runtime"}
	}
	return fallback, nil
}

func targetFromServerRefs(doc *document, refs []ref) (opTarget, []string, bool) {
	if len(refs) == 0 {
		return "", nil, false
	}
	var warnings []string
	for _, r := range refs {
		name := refName(r.Ref, "/servers/")
		if name == "" {
			continue
		}
		srv, ok := doc.Servers[name]
		if !ok {
			continue
		}
		switch serverProtocolFamily(srv.Protocol) {
		case targetKafka:
			return targetKafka, warnings, true
		case targetJMS:
			return targetJMS, warnings, true
		case targetReview:
			warnings = append(warnings, fmt.Sprintf("server %q uses review-only protocol %q", name, srv.Protocol))
			return targetReview, warnings, true
		}
	}
	return "", warnings, false
}

func serverProtocolFamily(protocol string) opTarget {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "kafka", "kafka-secure":
		return targetKafka
	case "amqp", "amqps", "jms", "solace":
		return targetJMS
	case "mqtt", "mqtts", "secure-mqtt", "ws", "wss", "http", "https":
		return targetReview
	default:
		return ""
	}
}

func hasOnlyMQTTServers(doc *document) bool {
	if len(doc.Servers) == 0 {
		return false
	}
	seenMQTT := false
	for _, srv := range doc.Servers {
		family := serverProtocolFamily(srv.Protocol)
		if family == targetKafka || family == targetJMS {
			return false
		}
		if family == targetReview && strings.Contains(strings.ToLower(srv.Protocol), "mqtt") {
			seenMQTT = true
		}
	}
	return seenMQTT
}

func hasJMSBinding(b bindings) bool {
	return b.AMQP != nil || b.JMS != nil || b.Solace != nil
}

func applyBindingsToOp(op *kafkaOp, channelBindings bindings, operationBindings bindings) {
	for _, b := range []bindings{channelBindings, operationBindings} {
		if b.Kafka != nil {
			if strings.TrimSpace(b.Kafka.Topic) != "" {
				op.topic = b.Kafka.Topic
			}
			if strings.TrimSpace(b.Kafka.Key) != "" {
				op.key = b.Kafka.Key
			}
			if strings.TrimSpace(b.Kafka.GroupID) != "" {
				op.consumerGroup = b.Kafka.GroupID
			}
			if strings.TrimSpace(b.Kafka.ClientID) != "" {
				op.clientID = b.Kafka.ClientID
			}
			if b.Kafka.Partition != nil {
				p := *b.Kafka.Partition
				op.partition = &p
			}
			if b.Kafka.Partitions > 0 {
				op.partitions = b.Kafka.Partitions
			}
		}
		if hasJMSBinding(b) && op.targetProtocol == targetJMS {
			if op.destinationType == "" {
				op.destinationType = "topic"
			}
			if op.subscription == "" && op.kind == "consume" {
				op.subscription = "asyncapi-replay"
			}
		}
	}
}

func ensureKafkaActionParams(action *dsl.Action, op kafkaOp) {
	if op.key == "" && op.consumerGroup == "" && op.partition == nil && op.clientID == "" {
		return
	}
	if action.Params == nil {
		action.Params = map[string]any{}
	}
	if op.key != "" {
		action.Params["key"] = op.key
	}
	if op.consumerGroup != "" {
		action.Params["consumer_group"] = op.consumerGroup
	}
	if op.partition != nil {
		action.Params["partition"] = *op.partition
	}
	if op.clientID != "" {
		action.Metadata["asyncapi_kafka_client_id"] = op.clientID
	}
}

func schemaDocument(s schema) string {
	if s.Ref != "" {
		return ""
	}
	encoded, err := json.Marshal(s)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func isReviewOnlySchemaFormat(format string) bool {
	f := strings.ToLower(strings.TrimSpace(format))
	return strings.Contains(f, "protobuf") || strings.Contains(f, "proto3") || strings.Contains(f, "proto2")
}

func buildAction(op kafkaOp, includeAssertion bool, doc *document) (dsl.Action, []string) {
	action := dsl.Action{
		Name:   sanitizeName(op.name),
		Target: op.topic,
		Metadata: map[string]any{
			"asyncapi_channel": op.channelName,
		},
	}
	var columns []string
	switch op.kind {
	case "produce":
		action.Type = "kafka_produce"
		action.Body, columns = sampleBody(doc, op.payload, op.schemaFormat)
	case "request_reply":
		action.Type = "kafka_request_reply"
		action.Body, columns = sampleBody(doc, op.payload, op.schemaFormat)
		action.Params = map[string]any{"reply_to": op.replyTopic}
	default:
		action.Type = "kafka_consume"
	}
	columns = append(columns, applyDeclaredHeaders(&action, op, doc)...)
	ensureKafkaActionParams(&action, op)
	columns = applyCorrelationID(&action, op, columns)
	if includeAssertion {
		switch op.kind {
		case "produce":
			action.Assertions = []dsl.Assertion{{Type: "offset_advanced"}}
		default:
			action.Assertions = []dsl.Assertion{{Type: "message_received"}}
		}
	}
	return action, columns
}

func applyDeclaredHeaders(action *dsl.Action, op kafkaOp, doc *document) []string {
	if op.kind == "consume" || len(op.headers.Properties) == 0 {
		return nil
	}
	value, columns := sampleForSchema(doc, op.headers, map[string]bool{}, "header")
	headers, ok := value.(map[string]any)
	if !ok || len(headers) == 0 {
		return columns
	}
	if action.Headers == nil {
		action.Headers = map[string]string{}
	}
	for _, name := range sortedAnyKeys(headers) {
		action.Headers[name] = fmt.Sprint(headers[name])
	}
	return columns
}

// applyCorrelationID wires the message's declared correlation id into the
// generated action: produced messages send a unique {{fn.uuid()}} value at the
// declared location, and consumed messages extract the value into
// {{var.<name>}} so later actions can reuse it. Returns the data columns with
// the correlation field removed (it must not come from a shared CSV row).
func applyCorrelationID(action *dsl.Action, op kafkaOp, columns []string) []string {
	location := op.correlationLocation
	if location == "" {
		return columns
	}
	source, path, ok := parseCorrelationLocation(location)
	if !ok {
		return columns
	}
	correlationTemplate := "{{fn.uuid()}}"
	switch op.kind {
	case "produce", "request_reply":
		if source == "payload" && action.Body != "" {
			if updated, ok := setJSONPointerValue(action.Body, path, correlationTemplate); ok {
				action.Body = updated
				action.Metadata["asyncapi_correlation_id"] = location
				if op.kind == "request_reply" {
					if action.Params == nil {
						action.Params = map[string]any{}
					}
					action.Params["correlation_id"] = correlationTemplate
				}
				return removeColumn(columns, sanitizeColumnName(lastPathSegment(path)))
			}
		}
		if source == "header" {
			// Outgoing message headers are sourced from the top-level
			// action.Headers map by both the Kafka (buildRecord) and JMS
			// (buildMessage) runtimes; params["headers"] is read by neither, so a
			// correlation id written there would be silently dropped on send.
			if action.Headers == nil {
				action.Headers = map[string]string{}
			}
			action.Headers[lastPathSegment(path)] = correlationTemplate
			action.Metadata["asyncapi_correlation_id"] = location
			if op.kind == "request_reply" {
				if action.Params == nil {
					action.Params = map[string]any{}
				}
				action.Params["correlation_id"] = correlationTemplate
			}
			return removeColumn(columns, sanitizeColumnName(columnName("header", lastPathSegment(path))))
		}
	case "consume":
		extractorName := sanitizeName(op.name) + "_correlation_id"
		extractor := dsl.Extractor{Name: extractorName, OnMiss: "ignore"}
		if source == "payload" {
			extractor.Source = "body"
			extractor.Type = "jsonpath"
			extractor.Expression = jsonPathForPointer(path)
		} else {
			extractor.Source = "header"
			extractor.Type = "header"
			extractor.Expression = lastPathSegment(path)
		}
		action.Extractors = append(action.Extractors, extractor)
		action.Metadata["asyncapi_correlation_id"] = location
	}
	return columns
}

// parseCorrelationLocation decodes AsyncAPI runtime expressions of the form
// $message.payload#/correlationId or $message.header#/correlationId.
func parseCorrelationLocation(location string) (source string, path string, ok bool) {
	trimmed := strings.TrimSpace(location)
	for _, candidate := range []string{"$message.payload#/", "$message.header#/"} {
		if strings.HasPrefix(trimmed, candidate) {
			path = strings.TrimPrefix(trimmed, candidate)
			if path == "" {
				return "", "", false
			}
			if strings.Contains(candidate, "payload") {
				return "payload", path, true
			}
			return "header", path, true
		}
	}
	return "", "", false
}

func splitPointerPath(path string) []string {
	segments := strings.Split(path, "/")
	out := make([]string, 0, len(segments))
	for _, segment := range segments {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			continue
		}
		out = append(out, unescapeJSONPointerSegment(segment))
	}
	return out
}

func unescapeJSONPointerSegment(segment string) string {
	segment = strings.ReplaceAll(segment, "~1", "/")
	segment = strings.ReplaceAll(segment, "~0", "~")
	return segment
}

func jsonPathForPointer(path string) string {
	segments := splitPointerPath(path)
	if len(segments) == 0 {
		return "$"
	}
	var b strings.Builder
	b.WriteString("$")
	for _, segment := range segments {
		if isIdentifier(segment) {
			b.WriteByte('.')
			b.WriteString(segment)
			continue
		}
		if isDigits(segment) {
			b.WriteByte('[')
			b.WriteString(segment)
			b.WriteByte(']')
			continue
		}
		encoded, _ := json.Marshal(segment)
		b.WriteByte('[')
		b.WriteString(string(encoded))
		b.WriteByte(']')
	}
	return b.String()
}

func lastPathSegment(path string) string {
	segments := splitPointerPath(path)
	if len(segments) == 0 {
		return path
	}
	return segments[len(segments)-1]
}

// setJSONPointerValue sets a nested field of a JSON document to value,
// following the given slash-separated pointer path. Returns ok=false when the
// document is not an object or the path's parents are missing.
func setJSONPointerValue(document string, path string, value string) (string, bool) {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(document), &parsed); err != nil {
		return document, false
	}
	segments := splitPointerPath(path)
	if len(segments) == 0 {
		return document, false
	}
	current := parsed
	for _, segment := range segments[:len(segments)-1] {
		next, ok := current[segment].(map[string]any)
		if !ok {
			return document, false
		}
		current = next
	}
	// Overwrites the sampled value, or adds the field when the schema sample
	// omitted it, so the produced message always carries the correlation id.
	current[segments[len(segments)-1]] = value
	encoded, err := json.MarshalIndent(parsed, "", "  ")
	if err != nil {
		return document, false
	}
	return string(encoded), true
}

func removeColumn(columns []string, name string) []string {
	out := make([]string, 0, len(columns))
	for _, column := range columns {
		if column == name {
			continue
		}
		out = append(out, column)
	}
	return out
}

// --- schema sampling ---

func sampleBody(doc *document, s schema, schemaFormat string) (string, []string) {
	var value any
	var columns []string
	if isAvroSchemaFormat(schemaFormat) || strings.EqualFold(schemaType(s), "record") {
		value, columns = sampleAvroSchema(doc, s, map[string]bool{}, "", 0)
	} else {
		value, columns = sampleForSchema(doc, s, map[string]bool{}, "")
	}
	if value == nil {
		return "", columns
	}
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", columns
	}
	return string(encoded), columns
}

func sampleForSchema(doc *document, s schema, seen map[string]bool, prefix string) (any, []string) {
	if s.Ref != "" {
		if seen[s.Ref] {
			return map[string]any{}, nil
		}
		seen[s.Ref] = true
		s = resolveSchemaRef(doc, s)
	}
	if s.Example != nil {
		return s.Example, nil
	}
	if len(s.Examples) > 0 {
		return s.Examples[0], nil
	}
	if len(s.Enum) > 0 {
		return s.Enum[0], nil
	}
	if len(s.Properties) > 0 || strings.EqualFold(schemaType(s), "object") {
		result := map[string]any{}
		var columns []string
		for _, name := range sortedSchemaKeys(s.Properties) {
			prop := s.Properties[name]
			if scalarSchema(prop) {
				column := columnName(prefix, name)
				result[name] = "{{data.asyncapi." + column + "}}"
				columns = append(columns, column)
				continue
			}
			value, nested := sampleForSchema(doc, prop, copySeen(seen), columnName(prefix, name))
			result[name] = value
			columns = append(columns, nested...)
		}
		return result, uniqueSorted(columns)
	}
	if s.Items != nil || strings.EqualFold(schemaType(s), "array") {
		value, columns := sampleForSchema(doc, valueOrEmpty(s.Items), copySeen(seen), prefix)
		return []any{value}, columns
	}
	switch strings.ToLower(schemaType(s)) {
	case "integer":
		return 0, nil
	case "number":
		return 0.0, nil
	case "boolean":
		return false, nil
	case "string":
		return "string", nil
	default:
		return map[string]any{}, nil
	}
}

func scalarSchema(s schema) bool {
	if s.Ref != "" || len(s.Properties) > 0 || s.Items != nil {
		return false
	}
	switch strings.ToLower(schemaType(s)) {
	case "string", "integer", "number", "boolean":
		return true
	default:
		return false
	}
}

// maxAvroSampleDepth bounds Avro schema-sampling recursion as defense-in-depth.
// The seen-set keyed on named-type identity (and on $ref) is the primary
// cycle-breaker; this cap guards against any unforeseen recursive shape so a
// hostile spec cannot stack-overflow (an uncatchable, process-fatal panic) the
// controller via the authenticated AsyncAPI import endpoint.
const maxAvroSampleDepth = 64

func sampleAvroSchema(doc *document, s schema, seen map[string]bool, prefix string, depth int) (any, []string) {
	if depth > maxAvroSampleDepth {
		column := columnName(prefix, "value")
		return "{{data.asyncapi." + column + "}}", []string{column}
	}
	if s.Ref != "" {
		if seen[s.Ref] {
			return map[string]any{}, nil
		}
		seen[s.Ref] = true
		s = resolveSchemaRef(doc, s)
	}
	if s.Default != nil {
		return s.Default, nil
	}
	if len(s.Enum) > 0 {
		return s.Enum[0], nil
	}
	typ := s.Type
	if typ == nil {
		typ = schemaType(s)
	}
	if union, ok := typ.([]any); ok {
		return sampleAvroUnion(doc, union, seen, prefix, depth+1)
	}
	if len(s.Fields) > 0 || strings.EqualFold(schemaType(s), "record") {
		result := map[string]any{}
		var columns []string
		for _, field := range s.Fields {
			if strings.TrimSpace(field.Name) == "" {
				continue
			}
			value, nested := sampleAvroType(doc, field.Type, field.Default, copySeen(seen), columnName(prefix, field.Name), depth+1)
			result[field.Name] = value
			columns = append(columns, nested...)
		}
		return result, uniqueSorted(columns)
	}
	switch strings.ToLower(schemaType(s)) {
	case "array":
		value, columns := sampleAvroSchema(doc, valueOrEmpty(s.Items), copySeen(seen), prefix, depth+1)
		return []any{value}, columns
	case "map":
		value, columns := sampleAvroSchema(doc, valueOrEmpty(s.Values), copySeen(seen), prefix, depth+1)
		return map[string]any{"key": value}, columns
	case "enum":
		if len(s.Enum) > 0 {
			return s.Enum[0], nil
		}
		return "ENUM_VALUE", nil
	case "fixed", "bytes":
		return "bytes", nil
	case "int", "long", "integer":
		return 0, nil
	case "float", "double", "number":
		return 0.0, nil
	case "boolean":
		return false, nil
	case "null":
		return nil, nil
	case "string":
		column := columnName(prefix, "value")
		return "{{data.asyncapi." + column + "}}", []string{column}
	default:
		// Avro references a named type via the `type` field (not a JSON $ref), so
		// the $ref seen-guard above never fires for it. Track the resolved
		// named-type identity here to break cyclic/mutually-recursive Avro records.
		if name := schemaType(s); isLikelyNamedAvroRef(name) {
			avroKey := "avro:" + name
			if seen[avroKey] {
				return map[string]any{}, nil
			}
			if resolved, ok := resolveNamedAvroSchema(doc, name); ok {
				next := copySeen(seen)
				next[avroKey] = true
				return sampleAvroSchema(doc, resolved, next, prefix, depth+1)
			}
		}
		column := columnName(prefix, "value")
		return "{{data.asyncapi." + column + "}}", []string{column}
	}
}

func sampleAvroType(doc *document, typ any, defaultValue any, seen map[string]bool, prefix string, depth int) (any, []string) {
	if defaultValue != nil {
		return defaultValue, nil
	}
	switch t := typ.(type) {
	case string:
		return sampleAvroSchema(doc, schema{Type: t}, seen, prefix, depth)
	case []any:
		return sampleAvroUnion(doc, t, seen, prefix, depth)
	case map[string]any:
		return sampleAvroSchema(doc, schemaFromMap(t), seen, prefix, depth)
	case map[any]any:
		converted := map[string]any{}
		for k, v := range t {
			converted[fmt.Sprint(k)] = v
		}
		return sampleAvroSchema(doc, schemaFromMap(converted), seen, prefix, depth)
	default:
		return sampleAvroSchema(doc, schema{}, seen, prefix, depth)
	}
}

func sampleAvroUnion(doc *document, union []any, seen map[string]bool, prefix string, depth int) (any, []string) {
	for _, branch := range union {
		if s, ok := branch.(string); ok && strings.EqualFold(s, "null") {
			continue
		}
		return sampleAvroType(doc, branch, nil, seen, prefix, depth)
	}
	return nil, nil
}

func schemaFromMap(m map[string]any) schema {
	var s schema
	data, err := json.Marshal(m)
	if err != nil {
		return s
	}
	_ = json.Unmarshal(data, &s)
	return s
}

func resolveNamedAvroSchema(doc *document, name string) (schema, bool) {
	if doc == nil {
		return schema{}, false
	}
	for key, candidate := range doc.Components.Schemas {
		if key == name || candidate.Name == name || candidate.Namespace+"."+candidate.Name == name {
			return candidate, true
		}
	}
	return schema{}, false
}

// --- message / channel resolution ---

func messageOf(op *channelOperation) messageDef {
	if op == nil || op.Message == nil {
		return messageDef{}
	}
	return *op.Message
}

func derefMessage(doc *document, m messageDef) messageDef {
	if len(m.OneOf) > 0 {
		m = m.OneOf[0]
	}
	return resolveMessageRef(doc, m)
}

func warnDroppedOneOf(doc *document, diagnostics *diag.Collector, warnings *[]string, label string, m messageDef) {
	resolved := resolveMessageRef(doc, m)
	if len(resolved.OneOf) <= 1 {
		return
	}
	message := fmt.Sprintf("message oneOf declares %d variants; the importer sampled the first variant and left %d variant(s) for review", len(resolved.OneOf), len(resolved.OneOf)-1)
	*warnings = append(*warnings, fmt.Sprintf("%s: %s", label, message))
	if diagnostics != nil {
		diagnostics.Add(diag.Warning, "AsyncAPI message oneOf review", label, message)
	}
}

func resolveMessageRef(doc *document, m messageDef) messageDef {
	if m.Ref == "" {
		return m
	}
	name := refName(m.Ref, "/components/messages/")
	if name == "" {
		return m
	}
	if resolved, ok := doc.Components.Messages[name]; ok {
		return resolved
	}
	return m
}

func resolvePayload(doc *document, m messageDef) schema {
	m = resolveMessageRef(doc, m)
	return resolveSchemaDeep(doc, m.Payload, map[string]bool{})
}

func resolveHeaders(doc *document, m messageDef) schema {
	m = resolveMessageRef(doc, m)
	return resolveSchemaDeep(doc, m.Headers, map[string]bool{})
}

func resolveSchemaRef(doc *document, s schema) schema {
	if s.Ref == "" {
		return s
	}
	name := refName(s.Ref, "/components/schemas/")
	if name == "" {
		return s
	}
	if resolved, ok := doc.Components.Schemas[name]; ok {
		return resolved
	}
	return s
}

func resolveSchemaDeep(doc *document, s schema, seen map[string]bool) schema {
	if s.Ref != "" {
		if seen[s.Ref] {
			return schema{}
		}
		seen[s.Ref] = true
		s = resolveSchemaRef(doc, s)
	}
	out := s
	if s.Properties != nil {
		out.Properties = make(map[string]schema, len(s.Properties))
		for name, prop := range s.Properties {
			out.Properties[name] = resolveSchemaDeep(doc, prop, copySeen(seen))
		}
	}
	if s.Items != nil {
		item := resolveSchemaDeep(doc, *s.Items, copySeen(seen))
		out.Items = &item
	}
	if s.Values != nil {
		value := resolveSchemaDeep(doc, *s.Values, copySeen(seen))
		out.Values = &value
	}
	return out
}

func resolveChannel(doc *document, channelRef string) (string, channel, bool) {
	name := refName(channelRef, "/channels/")
	if name == "" {
		return "", channel{}, false
	}
	ch, ok := doc.Channels[name]
	if !ok {
		ch, ok = doc.Components.Channels[name]
	}
	if ok {
		ch = resolveChannelValue(doc, ch, map[string]bool{})
	}
	return name, ch, ok
}

func resolveChannelValue(doc *document, ch channel, seen map[string]bool) channel {
	if ch.Ref == "" {
		return ch
	}
	if seen[ch.Ref] {
		return channel{}
	}
	seen[ch.Ref] = true
	name := refName(ch.Ref, "/channels/")
	if name == "" {
		return ch
	}
	if resolved, ok := doc.Channels[name]; ok {
		return resolveChannelValue(doc, resolved, seen)
	}
	if resolved, ok := doc.Components.Channels[name]; ok {
		return resolveChannelValue(doc, resolved, seen)
	}
	return ch
}

func firstChannelMessage(doc *document, ch channel) messageDef {
	for _, name := range sortedMessageKeys(ch.Messages) {
		return resolveMessageRef(doc, ch.Messages[name])
	}
	return messageDef{}
}

func channelTopic(name string, ch channel) string {
	topic := ""
	if ch.Bindings.Kafka != nil && strings.TrimSpace(ch.Bindings.Kafka.Topic) != "" {
		topic = ch.Bindings.Kafka.Topic
	} else if strings.TrimSpace(ch.Address) != "" {
		topic = ch.Address
	} else {
		topic = name
	}
	return applyChannelParameters(topic, ch.Parameters)
}

func topicWithBinding(topic string, b bindings) string {
	if b.Kafka != nil && strings.TrimSpace(b.Kafka.Topic) != "" {
		return b.Kafka.Topic
	}
	return topic
}

func applyChannelParameters(topic string, params map[string]parameter) string {
	out := topic
	for name, param := range params {
		replacement := fmt.Sprint(firstNonNil(param.Default, param.Example, firstAny(param.Examples), firstAny(param.Enum), ""))
		if replacement == "" {
			continue
		}
		out = strings.ReplaceAll(out, "{"+name+"}", replacement)
	}
	return out
}

func channelAllowed(allowed map[string]struct{}, name, topic string) bool {
	if allowed == nil {
		return true
	}
	if _, ok := allowed[name]; ok {
		return true
	}
	_, ok := allowed[topic]
	return ok
}

func refName(refStr, prefix string) string {
	refStr = strings.TrimSpace(refStr)
	idx := strings.Index(refStr, prefix)
	if idx < 0 {
		return ""
	}
	return refStr[idx+len(prefix):]
}

func dataSource(columns []string) dsl.DataSource {
	header := strings.Join(columns, ",")
	row := make([]string, len(columns))
	for i, c := range columns {
		row[i] = "sample_" + c
	}
	return dsl.DataSource{
		Name:           "asyncapi",
		Type:           "csv",
		Strategy:       "sequential",
		Delimiter:      ",",
		FirstRowHeader: true,
		Columns:        columns,
		InlineData:     header + "\n" + strings.Join(row, ","),
	}
}

func isJSONSchemaFormat(format string) bool {
	f := strings.ToLower(format)
	return strings.Contains(f, "jsonschema") || strings.Contains(f, "json-schema") || strings.Contains(f, "openapi")
}

func isAvroSchemaFormat(format string) bool {
	return strings.Contains(strings.ToLower(format), "avro")
}

func schemaType(s schema) string {
	switch t := s.Type.(type) {
	case string:
		return strings.TrimSpace(t)
	case []any:
		for _, item := range t {
			if str, ok := item.(string); ok && !strings.EqualFold(str, "null") {
				return strings.TrimSpace(str)
			}
		}
	case map[string]any:
		if str, ok := t["type"].(string); ok {
			return strings.TrimSpace(str)
		}
	case map[any]any:
		if v, ok := t["type"]; ok {
			return strings.TrimSpace(fmt.Sprint(v))
		}
	}
	return ""
}

func isLikelyNamedAvroRef(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	switch strings.ToLower(value) {
	case "record", "array", "map", "enum", "fixed", "string", "bytes", "int", "long", "float", "double", "boolean", "null":
		return false
	default:
		return true
	}
}

func columnName(prefix, name string) string {
	if prefix == "" {
		return sanitizeColumnName(name)
	}
	if name == "" || name == "value" {
		return sanitizeColumnName(prefix)
	}
	return sanitizeColumnName(prefix + "_" + name)
}

// --- small helpers ---

func sourceFileName(content string) string {
	if strings.HasPrefix(strings.TrimSpace(content), "{") {
		return "asyncapi.json"
	}
	return "asyncapi.yaml"
}

func valueOrEmpty(s *schema) schema {
	if s == nil {
		return schema{}
	}
	return *s
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func firstAny(values []any) any {
	if len(values) == 0 {
		return nil
	}
	return values[0]
}

func isIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for i, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}

func isDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func copySeen(seen map[string]bool) map[string]bool {
	out := make(map[string]bool, len(seen))
	for k, v := range seen {
		out[k] = v
	}
	return out
}

func filterSet(values []string) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(values))
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			out[trimmed] = struct{}{}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func firstPositive(values ...int) int {
	for _, v := range values {
		if v > 0 {
			return v
		}
	}
	return 0
}

func trimNonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func uniqueSorted(values []string) []string {
	set := map[string]struct{}{}
	for _, v := range values {
		if v != "" {
			set[v] = struct{}{}
		}
	}
	return sortedKeys(set)
}

func sortedKeys(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedKeysFromSet(set map[string]struct{}) []string {
	if len(set) == 0 {
		return nil
	}
	return sortedKeys(set)
}

func sortedServerKeys(m map[string]server) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedChannelKeys(m map[string]channel) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedOperationKeys(m map[string]operation) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedMessageKeys(m map[string]messageDef) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedSchemaKeys(m map[string]schema) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedAnyKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedSecuritySchemeKeys(m map[string]securityScheme) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sanitizeName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "AsyncAPI_Script"
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

func sanitizeColumnName(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

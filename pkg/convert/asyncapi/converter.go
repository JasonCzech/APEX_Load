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
	URL      string `json:"url,omitempty" yaml:"url,omitempty"`           // 2.x
	Host     string `json:"host,omitempty" yaml:"host,omitempty"`         // 3.x
	Pathname string `json:"pathname,omitempty" yaml:"pathname,omitempty"` // 3.x
	Protocol string `json:"protocol,omitempty" yaml:"protocol,omitempty"`
}

type channel struct {
	Ref       string                `json:"$ref,omitempty" yaml:"$ref,omitempty"`
	Address   string                `json:"address,omitempty" yaml:"address,omitempty"`     // 3.x
	Publish   *channelOperation     `json:"publish,omitempty" yaml:"publish,omitempty"`     // 2.x
	Subscribe *channelOperation     `json:"subscribe,omitempty" yaml:"subscribe,omitempty"` // 2.x
	Messages  map[string]messageDef `json:"messages,omitempty" yaml:"messages,omitempty"`   // 3.x
	Bindings  bindings              `json:"bindings,omitempty" yaml:"bindings,omitempty"`
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
	Kafka *kafkaBinding `json:"kafka,omitempty" yaml:"kafka,omitempty"`
}

type kafkaBinding struct {
	Topic string `json:"topic,omitempty" yaml:"topic,omitempty"`
}

type messageDef struct {
	Ref           string         `json:"$ref,omitempty" yaml:"$ref,omitempty"`
	Name          string         `json:"name,omitempty" yaml:"name,omitempty"`
	Title         string         `json:"title,omitempty" yaml:"title,omitempty"`
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
	Messages map[string]messageDef `json:"messages,omitempty" yaml:"messages,omitempty"`
	Schemas  map[string]schema     `json:"schemas,omitempty" yaml:"schemas,omitempty"`
}

type schema struct {
	Ref        string            `json:"$ref,omitempty" yaml:"$ref,omitempty"`
	Type       string            `json:"type,omitempty" yaml:"type,omitempty"`
	Format     string            `json:"format,omitempty" yaml:"format,omitempty"`
	Properties map[string]schema `json:"properties,omitempty" yaml:"properties,omitempty"`
	Items      *schema           `json:"items,omitempty" yaml:"items,omitempty"`
	Example    any               `json:"example,omitempty" yaml:"example,omitempty"`
	Examples   []any             `json:"examples,omitempty" yaml:"examples,omitempty"`
	Enum       []any             `json:"enum,omitempty" yaml:"enum,omitempty"`
}

// kafkaOp is the unified, version-neutral operation the generator consumes.
type kafkaOp struct {
	name           string
	kind           string // produce | consume | request_reply
	topic          string
	replyTopic     string
	payload        schema
	hasCorrelation bool
	// correlationLocation is the AsyncAPI runtime expression for the message's
	// correlation id (e.g. $message.payload#/correlationId), used to wire
	// produce-side uniqueness and consume-side extraction automatically.
	correlationLocation string
	schemaFormat        string
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
	doc, err := decodeDocument([]byte(content))
	if err != nil {
		return nil, err
	}
	if len(doc.Channels) == 0 {
		return nil, errors.New("AsyncAPI document contains no channels")
	}

	var diagnostics diag.Collector
	sourceFile := sourceFileName(content)
	mappings := make([]sourceview.Mapping, 0)

	brokers, protocols, securityProtocol, brokerWarnings := deriveBrokers(doc, req.Options.Brokers)

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
	useJMS, jmsBroker, jmsWarnings := false, "", []string(nil)
	if len(brokers) == 0 {
		useJMS, jmsBroker, jmsWarnings = detectJMSTarget(doc)
	}

	ops, schemaFormats, opWarnings := collectOperations(doc, req.Options, &diagnostics, &mappings, sourceFile)
	if len(ops) == 0 {
		return nil, errors.New("no supported operations found in AsyncAPI document")
	}

	mock := true
	if req.Options.Mock != nil {
		mock = *req.Options.Mock
	}

	scriptName := convertutil.FirstNonEmpty(req.Options.ScriptName, req.Name, doc.Info.Title, "Imported_AsyncAPI_Script")
	cfg := dsl.ScriptConfig{
		ThinkTime: dsl.ThinkTimeConfig{
			Type:   "fixed",
			MeanMs: float64(firstPositive(req.Options.DefaultThinkTimeMs, 1000)),
		},
		Pacing:        dsl.PacingConfig{Type: "none"},
		RunLogic:      dsl.RunLogicConfig{Iterations: 0, AutoTransactions: "none"},
		ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: false},
		Log:           dsl.LogConfig{Level: "standard"},
	}
	protocol := "kafka"
	if useJMS {
		protocol = "jms-solace"
		cfg.JMS = dsl.JMSConfig{
			BrokerURL: jmsBroker,
			Provider:  "solace",
			Mock:      mock,
		}
	} else {
		cfg.Kafka = dsl.KafkaConfig{
			Brokers:          brokers,
			Profile:          kafkaProfile,
			SecurityProtocol: securityProtocol,
			Acks:             "all",
			Mock:             mock,
		}
	}
	result := &ConvertResult{
		Script: &dsl.Script{
			Name:          sanitizeName(scriptName),
			Version:       "1.0.0",
			Protocol:      protocol,
			GeneratedBy:   "apexload-asyncapi-converter",
			GeneratedFrom: "asyncapi",
			CreatedAt:     time.Now(),
			Config:        cfg,
		},
		Stats: ConvertStats{
			Format:          "asyncapi",
			AsyncAPIVersion: doc.AsyncAPI,
			TotalChannels:   len(doc.Channels),
			ServerProtocols: protocols,
			Brokers:         brokers,
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
		if useJMS {
			result.Warnings = append(result.Warnings, "config.jms.mock is enabled so the generated script runs against the in-memory broker; disable it once a real JMS adapter is configured")
		} else {
			result.Warnings = append(result.Warnings, "config.kafka.mock is enabled so the generated script runs against the in-memory broker; disable it once a real Kafka adapter is configured")
		}
	}

	includeAssertions := convertutil.BoolOption(req.Options.IncludeAssertions, true)
	columns := map[string]struct{}{}
	topics := map[string]struct{}{}
	operationNames := []string{}

	for i, op := range ops {
		var action dsl.Action
		var bodyColumns []string
		if useJMS {
			action, bodyColumns = buildJMSAction(op, includeAssertions)
		} else {
			action, bodyColumns = buildAction(op, includeAssertions)
		}
		idx := len(result.Script.Actions)
		result.Script.Actions = append(result.Script.Actions, action)
		mappings = append(mappings, sourceview.CoarseMapping(sourceFile, op.channelName+" ("+op.kind+")", fmt.Sprintf("actions[%d]", idx), action.Name, "action", "converted", "AsyncAPI operation was converted into a generated APEX Load action."))
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

	result.Stats.TotalOperations = len(ops)
	result.Stats.SchemaFormatsDetected = schemaFormats
	result.Summary.OperationNames = operationNames
	result.Summary.Topics = sortedKeys(topics)
	dataColumns := sortedKeys(columns)
	result.Summary.DataColumns = dataColumns
	if len(dataColumns) > 0 {
		result.Script.DataSources = []dsl.DataSource{dataSource(dataColumns)}
	}
	if useJMS {
		if jmsBroker == "" {
			result.Warnings = append(result.Warnings, "no broker URL was derived from servers; set config.jms.broker_url before running against a real broker")
		}
	} else if len(brokers) == 0 {
		result.Warnings = append(result.Warnings, "no Kafka brokers were derived from servers; set config.kafka.brokers before running against a real cluster")
	}

	result.Unsupported = diagnostics.Items()
	result.SourceReview = sourceview.Build(map[string]string{sourceFile: content}, mappings)
	return result, nil
}

func decodeDocument(data []byte) (*document, error) {
	var doc document
	if err := json.Unmarshal(data, &doc); err == nil && doc.AsyncAPI != "" {
		return &doc, nil
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("invalid AsyncAPI document: %w", err)
	}
	if doc.AsyncAPI == "" {
		return nil, errors.New("invalid AsyncAPI document: missing asyncapi version")
	}
	return &doc, nil
}

func deriveBrokers(doc *document, override []string) (brokers []string, protocols []string, securityProtocol string, warnings []string) {
	if len(override) > 0 {
		brokers = trimNonEmpty(override)
	}
	protoSet := map[string]struct{}{}
	secure := false
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
		host := convertutil.FirstNonEmpty(srv.Host, srv.URL)
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
	return brokers, protocols, securityProtocol, warnings
}

// detectEventHubsProfile reports whether any bootstrap broker points at an Azure
// Event Hubs Kafka endpoint (*.servicebus.windows.net), so the importer can
// pre-set the Event Hubs profile and its mandatory sasl_ssl transport.
func detectEventHubsProfile(brokers []string) bool {
	for _, b := range brokers {
		host := strings.ToLower(strings.TrimSpace(b))
		if i := strings.IndexByte(host, ':'); i >= 0 {
			host = host[:i]
		}
		if strings.HasSuffix(host, ".servicebus.windows.net") {
			return true
		}
	}
	return false
}

// detectJMSTarget reports whether the document's servers indicate a JMS/AMQP
// target (and no Kafka — Kafka takes precedence to preserve existing behavior).
// AMQP/JMS map to the executable jms-solace adapter; MQTT is accepted best-effort
// with a review note (APEX Load has no native MQTT runtime).
func detectJMSTarget(doc *document) (isJMS bool, brokerURL string, warnings []string) {
	for _, name := range sortedServerKeys(doc.Servers) {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(doc.Servers[name].Protocol)), "kafka") {
			return false, "", nil
		}
	}
	for _, name := range sortedServerKeys(doc.Servers) {
		srv := doc.Servers[name]
		proto := strings.ToLower(strings.TrimSpace(srv.Protocol))
		host := convertutil.FirstNonEmpty(srv.URL, srv.Host)
		if srv.Host != "" && srv.Pathname != "" {
			host = srv.Host + srv.Pathname
		}
		switch proto {
		case "amqp", "amqps", "jms", "solace":
			return true, host, nil
		case "mqtt", "mqtts", "secure-mqtt":
			return true, host, []string{fmt.Sprintf("server %q uses MQTT; APEX Load has no native MQTT runtime — the import targets the JMS-over-AMQP adapter as a best-effort starting point and needs review", name)}
		}
	}
	return false, "", nil
}

// buildJMSAction mirrors buildAction for the JMS protocol family: produce ->
// jms_publish, consume -> jms_consume, request_reply -> jms_request_reply.
func buildJMSAction(op kafkaOp, includeAssertion bool) (dsl.Action, []string) {
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
		action.Type = "jms_publish"
		action.Body, columns = sampleBody(op.payload)
	case "request_reply":
		action.Type = "jms_request_reply"
		action.Body, columns = sampleBody(op.payload)
		action.Params = map[string]any{"reply_to": op.replyTopic}
	default:
		action.Type = "jms_consume"
	}
	columns = applyCorrelationID(&action, op, columns)
	if includeAssertion {
		action.Assertions = []dsl.Assertion{{Type: "message_received"}}
	}
	return action, columns
}

func collectOperations(doc *document, opts ConvertOptions, diagnostics *diag.Collector, mappings *[]sourceview.Mapping, sourceFile string) ([]kafkaOp, []string, []string) {
	var ops []kafkaOp
	var warnings []string
	schemaFormats := map[string]struct{}{}
	allowed := filterSet(opts.Channels)

	recordFormat := func(m messageDef) {
		if f := strings.TrimSpace(m.SchemaFormat); f != "" {
			schemaFormats[f] = struct{}{}
			if !isJSONSchemaFormat(f) {
				warnings = append(warnings, fmt.Sprintf("message schemaFormat %q is not JSON Schema; the generated payload is a best-effort placeholder", f))
			}
		}
	}

	if len(doc.Operations) > 0 {
		// AsyncAPI 3.x: top-level operations with explicit send/receive actions.
		for _, opName := range sortedOperationKeys(doc.Operations) {
			op := doc.Operations[opName]
			chName, ch, ok := resolveChannel(doc, op.Channel.Ref)
			if !ok {
				continue
			}
			if !channelAllowed(allowed, chName, channelTopic(chName, ch)) {
				continue
			}
			topic := channelTopic(chName, ch)
			msg := firstChannelMessage(doc, ch)
			recordFormat(msg)
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
			ops = append(ops, kafkaOp{
				name:                convertutil.FirstNonEmpty(opName, op.Title, op.Summary),
				kind:                kind,
				topic:               topic,
				replyTopic:          replyTopic,
				payload:             resolvePayload(doc, msg),
				hasCorrelation:      msg.CorrelationID != nil,
				correlationLocation: correlationLocationOf(msg),
				schemaFormat:        msg.SchemaFormat,
				channelName:         chName,
			})
		}
		return ops, sortedKeysFromSet(schemaFormats), warnings
	}

	// AsyncAPI 2.x: channel-scoped publish/subscribe operations.
	for _, chName := range sortedChannelKeys(doc.Channels) {
		ch := doc.Channels[chName]
		topic := channelTopic(chName, ch)
		if !channelAllowed(allowed, chName, topic) {
			continue
		}
		// 2.x perspective: an external client publishes to `publish` channels
		// (produce) and consumes from `subscribe` channels (consume).
		if ch.Publish != nil {
			msg := derefMessage(doc, messageOf(ch.Publish))
			recordFormat(msg)
			ops = append(ops, kafkaOp{
				name:                convertutil.FirstNonEmpty(ch.Publish.OperationID, ch.Publish.Summary, chName+"_produce"),
				kind:                "produce",
				topic:               topicWithBinding(topic, ch.Publish.Bindings),
				payload:             resolvePayload(doc, msg),
				hasCorrelation:      msg.CorrelationID != nil,
				correlationLocation: correlationLocationOf(msg),
				schemaFormat:        msg.SchemaFormat,
				channelName:         chName,
			})
		}
		if ch.Subscribe != nil {
			msg := derefMessage(doc, messageOf(ch.Subscribe))
			recordFormat(msg)
			ops = append(ops, kafkaOp{
				name:                convertutil.FirstNonEmpty(ch.Subscribe.OperationID, ch.Subscribe.Summary, chName+"_consume"),
				kind:                "consume",
				topic:               topicWithBinding(topic, ch.Subscribe.Bindings),
				payload:             resolvePayload(doc, msg),
				hasCorrelation:      msg.CorrelationID != nil,
				correlationLocation: correlationLocationOf(msg),
				schemaFormat:        msg.SchemaFormat,
				channelName:         chName,
			})
		}
		if ch.Publish == nil && ch.Subscribe == nil {
			diagnostics.Add(diag.Warning, "AsyncAPI channel skipped", chName, "channel declares neither publish nor subscribe operations")
			*mappings = append(*mappings, sourceview.CoarseMapping(sourceFile, chName, "", chName, "channel", "review_only", "Channel had no publish/subscribe operation to convert."))
		}
	}
	return ops, sortedKeysFromSet(schemaFormats), warnings
}

func buildAction(op kafkaOp, includeAssertion bool) (dsl.Action, []string) {
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
		action.Body, columns = sampleBody(op.payload)
	case "request_reply":
		action.Type = "kafka_request_reply"
		action.Body, columns = sampleBody(op.payload)
		action.Params = map[string]any{"reply_to": op.replyTopic}
	default:
		action.Type = "kafka_consume"
	}
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
	switch op.kind {
	case "produce", "request_reply":
		if source == "payload" && action.Body != "" {
			if updated, ok := setJSONPointerValue(action.Body, path, "{{fn.uuid()}}"); ok {
				action.Body = updated
				action.Metadata["asyncapi_correlation_id"] = location
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
			action.Headers[lastPathSegment(path)] = "{{fn.uuid()}}"
			action.Metadata["asyncapi_correlation_id"] = location
		}
	case "consume":
		extractorName := sanitizeName(op.name) + "_correlation_id"
		extractor := dsl.Extractor{Name: extractorName}
		if source == "payload" {
			extractor.Source = "body"
			extractor.Type = "jsonpath"
			extractor.Expression = "$." + strings.Join(splitPointerPath(path), ".")
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
		out = append(out, segment)
	}
	return out
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

func sampleBody(s schema) (string, []string) {
	value, columns := sampleForSchema(s, map[string]bool{})
	if value == nil {
		return "", columns
	}
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", columns
	}
	return string(encoded), columns
}

func sampleForSchema(s schema, seen map[string]bool) (any, []string) {
	if s.Ref != "" {
		if seen[s.Ref] {
			return map[string]any{}, nil
		}
		seen[s.Ref] = true
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
	if len(s.Properties) > 0 || strings.EqualFold(s.Type, "object") {
		result := map[string]any{}
		var columns []string
		for _, name := range sortedSchemaKeys(s.Properties) {
			prop := s.Properties[name]
			if scalarSchema(prop) {
				column := sanitizeColumnName(name)
				result[name] = "{{data.asyncapi." + column + "}}"
				columns = append(columns, column)
				continue
			}
			value, nested := sampleForSchema(prop, copySeen(seen))
			result[name] = value
			columns = append(columns, nested...)
		}
		return result, uniqueSorted(columns)
	}
	if s.Items != nil || strings.EqualFold(s.Type, "array") {
		value, columns := sampleForSchema(valueOrEmpty(s.Items), copySeen(seen))
		return []any{value}, columns
	}
	switch strings.ToLower(s.Type) {
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
	switch strings.ToLower(s.Type) {
	case "string", "integer", "number", "boolean":
		return true
	default:
		return false
	}
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
	return resolveSchemaRef(doc, m.Payload)
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

func resolveChannel(doc *document, channelRef string) (string, channel, bool) {
	name := refName(channelRef, "/channels/")
	if name == "" {
		return "", channel{}, false
	}
	ch, ok := doc.Channels[name]
	return name, ch, ok
}

func firstChannelMessage(doc *document, ch channel) messageDef {
	for _, name := range sortedMessageKeys(ch.Messages) {
		return resolveMessageRef(doc, ch.Messages[name])
	}
	return messageDef{}
}

func channelTopic(name string, ch channel) string {
	if ch.Bindings.Kafka != nil && strings.TrimSpace(ch.Bindings.Kafka.Topic) != "" {
		return ch.Bindings.Kafka.Topic
	}
	if strings.TrimSpace(ch.Address) != "" {
		return ch.Address
	}
	return name
}

func topicWithBinding(topic string, b bindings) string {
	if b.Kafka != nil && strings.TrimSpace(b.Kafka.Topic) != "" {
		return b.Kafka.Topic
	}
	return topic
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

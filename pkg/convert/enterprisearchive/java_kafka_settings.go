package enterprisearchive

// Kafka properties inside Java source previously appeared only as generic Java
// statements. This inventory adds source-linked migration evidence without
// changing the combined Java runner's execution or claiming native equivalence.

import (
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	kafkaprotocol "github.com/jasonczech/apexload/pkg/protocol/kafka"
	"github.com/jasonczech/apexload/pkg/protocol/kafkacontract"
)

const JavaKafkaSettingsMetadataKey = "java_kafka_settings"
const maxJavaKafkaSettingEvidence = 256

type JavaKafkaSetting struct {
	Name       string   `json:"name"`
	Value      string   `json:"value"`
	SourceFile string   `json:"source_file"`
	StartLine  int      `json:"start_line"`
	EndLine    int      `json:"end_line"`
	GoSupport  string   `json:"go_support"`
	GoFields   []string `json:"go_fields,omitempty"`
	Notes      string   `json:"notes"`
}

type JavaKafkaSettingsReport struct {
	Settings                []JavaKafkaSetting `json:"settings"`
	StatusCounts            map[string]int     `json:"status_counts"`
	SettingsTruncated       bool               `json:"settings_truncated,omitempty"`
	NativeMigrationReady    bool               `json:"native_migration_ready"`
	NativeMigrationBlockers []string           `json:"native_migration_blockers"`
	Summary                 string             `json:"summary"`
}

var (
	javaKafkaProducerCreationRe = regexp.MustCompile(`\bnew\s+(?:org\.apache\.kafka\.clients\.producer\.)?KafkaProducer\s*(?:<[^;{}()]*>)?\s*\(\s*([A-Za-z_$][A-Za-z0-9_$]*)\s*\)`)
	javaKafkaPropertyCallRe     = regexp.MustCompile(`^\s*([A-Za-z_$][A-Za-z0-9_$]*)\s*\.\s*(?:put|setProperty)\s*\((.*)\)\s*;?\s*$`)
)

func attachJavaKafkaSettingsReview(result *ConvertResult, program javaProgram) {
	if result == nil || result.ReviewArtifact == nil {
		return
	}
	report := buildJavaKafkaSettingsReport(program)
	if report == nil {
		return
	}
	if result.ReviewArtifact.Metadata == nil {
		result.ReviewArtifact.Metadata = map[string]any{}
	}
	result.ReviewArtifact.Metadata[JavaKafkaSettingsMetadataKey] = report
	if result.Script != nil {
		if result.Script.Metadata == nil {
			result.Script.Metadata = &dsl.ScriptMetadata{}
		}
		if result.Script.Metadata.Import == nil {
			result.Script.Metadata.Import = &dsl.ImportMetadata{Source: "enterprise_script_archive"}
		}
		if result.Script.Metadata.Import.Preserved == nil {
			result.Script.Metadata.Import.Preserved = map[string]any{}
		}
		result.Script.Metadata.Import.Preserved[JavaKafkaSettingsMetadataKey] = report
	}
	result.ReviewArtifact.NextActions = appendUnique(result.ReviewArtifact.NextActions,
		"Review the source-linked Kafka property inventory; the Java source retains these settings, while Go migration requires resolving the listed producer semantics and credential bindings.")
	result.Warnings = append(result.Warnings, report.Summary)
	// Enrich existing mappings instead of adding duplicate statement rows, which
	// would inflate the function-conservation counters and clutter source review.
	if result.SourceReview != nil {
		type location struct {
			file string
			line int
		}
		settingsByLocation := map[location][]JavaKafkaSetting{}
		for _, setting := range report.Settings {
			key := location{setting.SourceFile, setting.StartLine}
			settingsByLocation[key] = append(settingsByLocation[key], setting)
		}
		for index := range result.SourceReview.Mappings {
			mapping := &result.SourceReview.Mappings[index]
			for _, setting := range settingsByLocation[location{mapping.SourceFile, mapping.StartLine}] {
				mapping.Notes += " Kafka property " + setting.Name + ": " + setting.Notes
			}
		}
	}
}

func buildJavaKafkaSettingsReport(program javaProgram) *JavaKafkaSettingsReport {
	report := &JavaKafkaSettingsReport{StatusCounts: map[string]int{}}
	producerFound := false
	for _, class := range program.Classes {
		methods := make([]javaMethod, 0, len(class.Methods))
		for _, method := range class.Methods {
			methods = append(methods, method)
		}
		sort.Slice(methods, func(i, j int) bool { return methods[i].StartLine < methods[j].StartLine })
		for _, method := range methods {
			// Only associate local Properties receivers passed to a KafkaProducer
			// in this method. Other .put calls are unrelated source evidence.
			properties := map[string]bool{}
			for _, match := range javaKafkaProducerCreationRe.FindAllStringSubmatch(javaCodeOnly(method.Body), -1) {
				properties[match[1]] = true
				producerFound = true
			}
			if len(properties) == 0 {
				continue
			}
			env := newJavaStringEnv()
			methodStart := len(report.Settings)
			// Each Properties receiver is its own producer configuration, so
			// a row is judged against the receiver it was put on.
			producers := make(map[string]*javaKafkaProducerIdentity, len(properties))
			for receiver := range properties {
				producers[receiver] = &javaKafkaProducerIdentity{resolved: true}
			}
			var receivers []string
			var visit func([]javaStatement, bool)
			visit = func(statements []javaStatement, conditional bool) {
				for _, statement := range statements {
					if len(statement.Children) > 0 {
						// Conditional/loop assignments cannot establish an effective
						// Properties map. Inventory their literal rows for review.
						visit(statement.Children, conditional || strings.TrimSpace(javaCodeOnly(statement.Header)) != "")
						continue
					}
					match := javaKafkaPropertyCallRe.FindStringSubmatch(strings.TrimSpace(statement.Text))
					if len(match) == 3 && properties[match[1]] {
						if len(report.Settings) == maxJavaKafkaSettingEvidence {
							report.SettingsTruncated = true
							continue
						}
						args := splitJavaArgs(match[2])
						name, value, knownName, knownValue := "[dynamic property]", "", false, false
						if len(args) == 2 {
							name, knownName = env.eval(args[0], nil)
							value, knownValue = env.eval(args[1], nil)
							if !knownValue && javaConstantNumberRe.MatchString(strings.TrimSpace(args[1])) {
								value, knownValue = strings.TrimSpace(args[1]), true
							}
							if !knownValue && (strings.TrimSpace(args[1]) == "true" || strings.TrimSpace(args[1]) == "false") {
								value, knownValue = strings.TrimSpace(args[1]), true
							}
						}
						if !knownName {
							name = "[dynamic property]"
						}
						producer := producers[match[1]]
						if !knownName {
							// A dynamic property name may be acks or enable.idempotence.
							producer.resolved = false
						} else if name == "acks" || name == "enable.idempotence" {
							if knownValue && !conditional {
								producer.observe(name, value)
							} else {
								producer.resolved = false
							}
						} else if name == "max.in.flight.requests.per.connection" || name == "retries" {
							// These can turn an unset idempotence off (see
							// javaKafkaImplicitIdempotenceConflict).
							if knownValue && !conditional {
								producer.observe(name, value)
							} else {
								producer.conflictUnresolved = true
								producer.retriesUnresolved = producer.retriesUnresolved || name == "retries"
							}
						}
						setting := classifyJavaKafkaSetting(name, value, knownName && knownValue && !conditional)
						setting.SourceFile, setting.StartLine, setting.EndLine = statement.File, statement.StartLine, statement.EndLine
						if conditional {
							setting.Notes += " Conditional or looped assignment; an effective property value was not inferred."
						}
						report.Settings = append(report.Settings, setting)
						receivers = append(receivers, match[1])
						report.StatusCounts[setting.GoSupport]++
					}
					if !conditional {
						if assignment := javaStringAssignRe.FindStringSubmatch(strings.TrimSpace(statement.Text)); len(assignment) == 3 {
							delete(env.Vars, assignment[1])
							env.assignString(statement.Text, nil)
						}
					} else if assignment := javaStringAssignRe.FindStringSubmatch(strings.TrimSpace(statement.Text)); len(assignment) == 3 {
						delete(env.Vars, assignment[1])
					}
				}
			}
			visit(method.Statements, false)
			for offset, receiver := range receivers {
				row := methodStart + offset
				producer := producers[receiver]
				refineJavaKafkaInFlightSettings(report.Settings[row:row+1], report.StatusCounts, *producer, producer.resolved)
			}
		}
	}
	if !producerFound {
		return nil
	}
	report.NativeMigrationBlockers = []string{
		"Java source is retained in the governed Java Vuser runner; this inventory does not generate or enable a native Kafka script.",
		"Go Kafka replay requires a kafka_real build and reachable, approved brokers; the mock broker does not verify network fidelity.",
		"Producer lifetime, asynchronous send/close error handling, partition routing, and implicit Java client defaults need source-specific verification before Go migration.",
	}
	if report.SettingsTruncated {
		report.NativeMigrationBlockers = append(report.NativeMigrationBlockers, "Kafka property evidence exceeded the bounded inventory; review the preserved Java source for remaining settings.")
	}
	report.Summary = fmt.Sprintf("Kafka source inventory accounts for %d explicit property assignments. Java replay retains source settings; Go support is migration evidence and does not establish native replay fidelity.", len(report.Settings))
	return report
}

func classifyJavaKafkaSetting(name, value string, resolved bool) JavaKafkaSetting {
	setting := JavaKafkaSetting{Name: boundedRuntimeReviewEvidence(name), Value: boundedRuntimeReviewEvidence(value), GoSupport: "unsupported", Notes: "No verified typed Go mapping; the setting remains in the preserved Java source."}
	if !resolved || strings.Contains(name, "{{") || strings.Contains(value, "{{") {
		setting.GoSupport = "unresolved"
		setting.Notes = "Dynamic or conditional source setting; the effective value requires source review."
		return setting
	}
	field := func(fields ...string) { setting.GoFields = fields; setting.GoSupport = "supported" }
	switch name {
	case "group", "session.timeout.ms", "heartbeat.interval.ms":
		setting.GoSupport = "non_applicable"
		setting.Notes = "This property does not affect KafkaProducer; consumer grouping and heartbeat settings were not promoted to Go producer behavior."
	case "bootstrap.servers":
		if javaKafkaBrokerListValid(value) {
			field("config.kafka.brokers")
		}
	case "key.serializer", "value.serializer":
		if value == "org.apache.kafka.common.serialization.StringSerializer" {
			if name == "key.serializer" {
				field("actions[].params.key")
			} else {
				field("actions[].params.value")
			}
			setting.Notes = "StringSerializer uses UTF-8 bytes; Go raw Kafka records can carry the same bytes. Null/empty keys and source partition routing still require verification."
		}
	case "sasl.jaas.config":
		setting.GoSupport = "requires_binding"
		setting.GoFields = []string{"config.kafka.sasl_username", "config.kafka.sasl_password_secret"}
		setting.Notes = "JAAS stays in preserved Java source. A reviewed PLAIN login module can use Go SASL username and password configuration; arbitrary JAAS modules/options are not executed by Go."
	case "security.protocol":
		if oneOfFold(value, "PLAINTEXT", "SSL", "SASL_SSL", "SASL_PLAINTEXT") {
			field("config.kafka.security_protocol")
		}
	case "sasl.mechanism":
		if oneOfFold(value, "PLAIN", "SCRAM-SHA-256", "SCRAM-SHA-512") {
			field("config.kafka.sasl_mechanism")
		}
	case "acks":
		if oneOfFold(value, "0", "1", "-1", "all") {
			field("config.kafka.acks")
		}
	case "enable.idempotence":
		if oneOfFold(value, "true", "false") {
			field("config.kafka.idempotence")
		}
	case "ssl.enabled.protocols":
		versions := map[string]bool{}
		for _, version := range strings.Split(value, ",") {
			versions[strings.TrimSpace(version)] = true
		}
		if (len(versions) == 1 && (versions["TLSv1.2"] || versions["TLSv1.3"])) || (len(versions) == 2 && versions["TLSv1.2"] && versions["TLSv1.3"]) {
			field("config.kafka.tls.min_version", "config.kafka.tls.max_version")
			setting.Notes = "Use both Go TLS minimum and maximum versions; TLSv1.2 alone must not silently allow TLSv1.3."
		}
	case "batch.size":
		if size, err := strconv.Atoi(value); err == nil && size >= 0 && size <= kafkaprotocol.MaxConfiguredMessageBytes {
			setting.GoSupport = "partial"
			setting.GoFields = []string{"config.kafka.producer.batch_bytes"}
			setting.Notes = "Java batch.size is an allocation target and permits larger singleton records; Go/franz-go batch_bytes is a maximum. Direct copying can reject records the Java producer accepts."
		}
	case "max.in.flight.requests.per.connection":
		// Classified against the producer's idempotence once the whole method is
		// read (refineJavaKafkaInFlightSettings); alone it is only a candidate.
		if parsed, err := strconv.Atoi(value); err == nil && kafkaprotocol.ValidateProducerSettings(map[string]any{"kafka_producer_max_in_flight_requests": parsed}) == nil {
			setting.GoSupport = "partial"
			setting.GoFields = []string{"config.kafka.producer.max_in_flight_requests"}
			setting.Notes = javaKafkaInFlightIdempotentNote
		}
	case "retries", "linger.ms", "buffer.memory", "max.block.ms", "delivery.timeout.ms", "request.timeout.ms":
		native := map[string]string{"retries": "retries", "linger.ms": "linger_ms", "buffer.memory": "buffer_bytes", "max.block.ms": "admission_timeout_ms", "delivery.timeout.ms": "delivery_timeout_ms", "request.timeout.ms": "request_timeout_ms"}[name]
		parsed, err := strconv.Atoi(value)
		if err == nil && kafkaprotocol.ValidateProducerSettings(map[string]any{"kafka_producer_" + native: parsed}) == nil {
			field("config.kafka.producer." + native)
		}
	}
	if setting.GoSupport == "supported" && setting.Notes == "No verified typed Go mapping; the setting remains in the preserved Java source." {
		setting.Notes = "A typed Go setting supports this explicit value; the importer retains the Java source rather than selecting a native execution lane."
	}
	return setting
}

const javaKafkaInFlightIdempotentNote = "Maps to config.kafka.producer.max_in_flight_requests only for a non-idempotent producer (acks 0 or 1, or enable.idempotence=false). An idempotent producer (acks all with idempotence not disabled, the Java 3.x default) does not apply it in Go: franz pipelines up to 5 idempotent requests itself and refuses more than 5."

// javaKafkaProducerIdentity is the acks/idempotence pair the Go runtime uses
// to decide whether a producer is idempotent (kafkacontract.IdempotentProducer),
// plus the effective in-flight and retries values that make the Java client
// turn an unset idempotence off. conflictUnresolved records a dynamic or
// conditional assignment of either, whose effective value is unknown, and
// retriesUnresolved one of retries. resolved is false when acks or
// enable.idempotence was assigned dynamically or conditionally.
type javaKafkaProducerIdentity struct {
	acks, idempotence  string
	inFlight, retries  string
	conflictUnresolved bool
	retriesUnresolved  bool
	resolved           bool
}

func (p *javaKafkaProducerIdentity) observe(name, value string) {
	switch name {
	case "acks":
		p.acks = map[string]string{"0": "none", "1": "leader", "-1": "all", "all": "all"}[strings.ToLower(strings.TrimSpace(value))]
	case "enable.idempotence":
		p.idempotence = map[string]string{"true": "enabled", "false": "disabled"}[strings.ToLower(strings.TrimSpace(value))]
	case "max.in.flight.requests.per.connection":
		p.inFlight = strings.TrimSpace(value)
	case "retries":
		p.retries = strings.TrimSpace(value)
	}
}

// javaKafkaImplicitIdempotenceConflict names the effective setting that makes
// the Java producer turn its default idempotence off (more than five requests
// in flight, or retries=0), or returns "" when none does. It mirrors the
// JMeter converter's helper of the same name: Kafka 3.x (KAFKA-13598) turns an
// unset enable.idempotence off instead of failing on these settings; only an
// explicit enable.idempotence=true makes them invalid.
func javaKafkaImplicitIdempotenceConflict(producer javaKafkaProducerIdentity) string {
	if value, err := strconv.Atoi(producer.inFlight); err == nil && value > kafkacontract.MaxIdempotentInFlightRequests {
		return "max.in.flight.requests.per.connection " + producer.inFlight
	}
	if value, err := strconv.Atoi(producer.retries); err == nil && value == 0 {
		return "retries 0"
	}
	return ""
}

// refineJavaKafkaInFlightSettings applies the idempotence rule to the retries and
// max.in.flight rows of one producer's Properties receiver. resolved is false
// when acks or enable.idempotence was assigned dynamically or conditionally,
// so the rule cannot be decided and the row stays partial. With idempotence
// unset, a conflicting in-flight or retries value turns it off as the Java
// client does; the rows then also map to config.kafka.idempotence: disabled.
func refineJavaKafkaInFlightSettings(settings []JavaKafkaSetting, counts map[string]int, producer javaKafkaProducerIdentity, resolved bool) {
	if !resolved {
		return
	}
	idempotence, implicit := producer.idempotence, ""
	if idempotence == "" && kafkacontract.IdempotentProducer(producer.acks, idempotence) {
		if producer.conflictUnresolved {
			// The Java client may or may not have turned idempotence off.
			return
		}
		if implicit = javaKafkaImplicitIdempotenceConflict(producer); implicit != "" {
			idempotence = "disabled"
		}
	}
	implicitNote := ""
	if implicit != "" {
		implicitNote = " enable.idempotence is unset and the producer sets " + implicit + ", so the Java client turns idempotence off rather than failing; Go matches it with config.kafka.idempotence: disabled."
	}
	for index := range settings {
		setting := &settings[index]
		if value, err := strconv.Atoi(setting.Value); err == nil && value == 0 && setting.Name == "retries" && producer.idempotence == "enabled" && setting.GoSupport == "supported" {
			// An explicit enable.idempotence=true makes the Java client refuse
			// an effective retries=0; a later assignment can replace this one.
			if producer.retriesUnresolved {
				setting.Notes += " enable.idempotence=true is set explicitly, so the Java client refuses retries=0, but a conditional or dynamic assignment may replace this value; the effective value requires source review."
				continue
			}
			if effective, err := strconv.Atoi(producer.retries); err != nil || effective != 0 {
				setting.Notes += " A later assignment replaces this value, so the Java client's refusal of retries=0 for an explicitly idempotent producer does not apply."
				continue
			}
			setting.GoSupport, setting.GoFields = "unsupported", nil
			setting.Notes = "enable.idempotence=true is set explicitly, and the Java client refuses retries=0 for an idempotent producer. Set enable.idempotence=false to keep the value."
			if counts["supported"]--; counts["supported"] <= 0 {
				delete(counts, "supported")
			}
			counts["unsupported"]++
			continue
		}
		if setting.Name == "retries" && implicit == "retries 0" && setting.GoSupport == "supported" {
			setting.GoFields = append(setting.GoFields, "config.kafka.idempotence")
			setting.Notes += implicitNote
			continue
		}
		if setting.Name != "max.in.flight.requests.per.connection" || setting.GoSupport != "partial" {
			continue
		}
		previous := setting.GoSupport
		if !kafkacontract.IdempotentProducer(producer.acks, idempotence) {
			setting.GoSupport = "supported"
			setting.Notes = "The producer is not idempotent (acks 0 or 1, or enable.idempotence=false), so the value maps to config.kafka.producer.max_in_flight_requests as Java applies it."
			if implicit != "" {
				setting.GoFields = append(setting.GoFields, "config.kafka.idempotence")
				setting.Notes = "The value maps to config.kafka.producer.max_in_flight_requests as Java applies it." + implicitNote
			}
		} else if value, err := strconv.Atoi(setting.Value); err == nil && value > kafkacontract.MaxIdempotentInFlightRequests {
			setting.GoSupport, setting.GoFields = "unsupported", nil
			setting.Notes = "The effective producer is idempotent (enable.idempotence=true set explicitly, or this value is replaced by a later assignment), and both the Java client and Go refuse more than 5 requests in flight for an idempotent producer. Set enable.idempotence=false to keep the value."
		}
		if setting.GoSupport != previous {
			if counts[previous]--; counts[previous] <= 0 {
				delete(counts, previous)
			}
			counts[setting.GoSupport]++
		}
	}
}

func oneOfFold(value string, options ...string) bool {
	for _, option := range options {
		if strings.EqualFold(value, option) {
			return true
		}
	}
	return false
}

func javaKafkaBrokerListValid(value string) bool {
	brokers := strings.Split(value, ",")
	if len(brokers) > 256 {
		return false
	}
	for _, broker := range brokers {
		host, port, err := net.SplitHostPort(strings.TrimSpace(broker))
		if err != nil || strings.TrimSpace(host) == "" {
			return false
		}
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return false
		}
	}
	return true
}

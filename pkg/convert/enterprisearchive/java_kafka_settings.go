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
						setting := classifyJavaKafkaSetting(name, value, knownName && knownValue && !conditional)
						setting.SourceFile, setting.StartLine, setting.EndLine = statement.File, statement.StartLine, statement.EndLine
						if conditional {
							setting.Notes += " Conditional or looped assignment; an effective property value was not inferred."
						}
						report.Settings = append(report.Settings, setting)
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
	setting := JavaKafkaSetting{Name: boundedRuntimeReviewEvidence(name), Value: "[value withheld]", GoSupport: "unsupported", Notes: "No verified typed Go mapping; the setting remains in the preserved Java source."}
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
		setting.Value = "[credential options withheld]"
		setting.Notes = "JAAS stays in protected Java source. A reviewed PLAIN login module can use Go SASL username and a managed secret binding; arbitrary JAAS modules/options are not executed by Go."
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
	// Only known transport/tuning values are exposed. Unknown properties and all
	// credential composites remain withheld even if their names look harmless.
	if setting.GoSupport == "supported" || setting.GoSupport == "partial" || setting.GoSupport == "non_applicable" {
		setting.Value = boundedRuntimeReviewEvidence(value)
	}
	return setting
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

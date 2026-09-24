package jmeter

// The adapters preserve messaging drafts even when runtime compatibility or
// credentials are unavailable. Preservation is deliberately distinct from
// execution readiness; every draft retains a durable compatibility gate.
import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

const diKafkaSampler = "com.di.jmeter.kafka.sampler.KafkaProducerSampler"
const diKafkaConfig = "com.di.jmeter.kafka.config.KafkaProducerConfig"
const pnopEventHubsSampler = "jp.co.pnop.jmeter.protocol.azureeventhubs.sampler.AzEventHubsSampler"
const throughputShapingTimer = "kg.apc.jmeter.timers.VariableThroughputTimer"

func messagingNodeID(plan string, e *xmlNode) string {
	return fmt.Sprintf("%s:%d:%s", plan, e.StartOffset, e.Name)
}
func sourcePlanID(data []byte) string { return fmt.Sprintf("jmx:%x", sha256.Sum256(data)) }

func messagingAncestorNodes(nodes []*treeNode, target *treeNode) ([]*treeNode, bool) {
	for _, n := range nodes {
		if n == nil || n.Element == nil || !jmeterElementEnabled(n.Element) {
			continue
		}
		if n == target {
			return nil, true
		}
		if descendants, ok := messagingAncestorNodes(n.Children, target); ok {
			var out []*treeNode
			for _, sibling := range n.Children {
				if sibling != target && sibling.Element != nil && isMessagingContributor(sibling.Element.Name) {
					out = append(out, sibling)
				}
			}
			return append(out, descendants...), true
		}
	}
	return nil, false
}
func isMessagingContributor(name string) bool {
	return name == diKafkaConfig || name == "CounterConfig" || name == "RandomVariableConfig" || name == throughputShapingTimer
}

// rawMessagingProp preserves message bytes (prop intentionally trims ordinary
// configuration values). Empty and absent Kafka keys remain distinguishable.
func rawMessagingProp(n *xmlNode, key string) (string, bool) {
	if n.Attrs["name"] == key {
		return n.Text, true
	}
	for _, c := range n.Children {
		if v, ok := rawMessagingProp(c, key); ok {
			return v, true
		}
	}
	return "", false
}

func handleJMeterMessagingSampler(node *treeNode, ctx *convertContext, script *dsl.Script, result *ConvertResult, diagnostics *diag.Collector, file string, mappings *[]sourceview.Mapping) {
	e := node.Element
	a := dsl.Action{Name: attrOr(e, "testname", "Messaging producer"), Type: "kafka_produce", Params: map[string]interface{}{}, Metadata: map[string]interface{}{"source_node_id": messagingNodeID(script.Metadata.Import.Preserved["source_plan_id"].(string), e)}}
	expected := "kafka"
	if e.Name == pnopEventHubsSampler {
		expected = "eventhubs"
		a.Type = "eventhubs_produce"
	}
	block := func(reason string) {
		gateJMeterAction(&a, reason, jmeterElementLocation(e))
		diagnostics.Add(diag.Blocker, "JMeter "+e.Name, jmeterElementLocation(e), reason)
	}
	if script.Protocol != expected {
		block("jmeter_mixed_messaging_protocol")
	}
	plan := script.Metadata.Import.Preserved["source_plan_id"].(string)
	if script.Config.JMeter == nil {
		script.Config.JMeter = &dsl.JMeterCompatibility{Profile: dsl.JMeterMessagingProfile, SourcePlanID: plan, SourceGroupID: script.Metadata.Import.Preserved["source_group_id"].(string)}
	}
	// Runtime support is not yet certified. This gate cannot be cleared by warning acknowledgement.
	block("jmeter_messaging_compatibility_unverified")
	block("jmeter_reference_context_confirmation_required")
	scope := append(append([]*treeNode(nil), ctx.contributors...), node.Children...)
	if expected == "kafka" {
		previous := script.Config.Kafka
		convertDIKafka(e, scope, ctx, script, &a, file, block)
		if meta, ok := a.Metadata["jmeter_kafka"].(map[string]interface{}); ok {
			meta["native_config"] = script.Config.Kafka
			// Topics are already sampler-local. Other differing producer settings
			// cannot be represented by a single script-level configuration.
			comparison := script.Config.Kafka
			comparison.Topic = previous.Topic
			if len(previous.Brokers) > 0 && !reflect.DeepEqual(previous, comparison) {
				block("jmeter_multiple_producer_configurations_unsupported")
				script.Config.Kafka = previous
			}
		}
	} else {
		previous := script.Config.EventHubs
		convertPNOPEventHubs(e, ctx, script, &a, file, block)
		if meta, ok := a.Metadata["jmeter_eventhubs"].(map[string]interface{}); ok {
			meta["native_config"] = script.Config.EventHubs
			if previous.ConnectionString != "" {
				block("jmeter_multiple_producer_configurations_unsupported")
				script.Config.EventHubs = previous
			}
		}
	}
	seen := map[*xmlNode]bool{}
	for _, n := range scope {
		if n == nil || n.Element == nil || !jmeterElementEnabled(n.Element) || seen[n.Element] {
			continue
		}
		seen[n.Element] = true
		c := n.Element
		switch c.Name {
		case "CounterConfig", "RandomVariableConfig":
			p, err := messagingParameter(c, plan)
			if err != nil {
				block("jmeter_parameter_semantics_unsupported")
				continue
			}
			duplicate := false
			for _, old := range script.Parameters {
				if old.Name == p.Name {
					duplicate = true
					if old.ScopeID != p.ScopeID {
						block("jmeter_parameter_scope_ambiguous")
					}
				}
			}
			if !duplicate {
				script.Parameters = append(script.Parameters, p)
			}
			// Messaging variables are evaluated by JMeter before sampler execution.
			token := "{{var." + p.Name + "}}"
			replacement := "{{param." + p.Name + "}}"
			a.Body = strings.ReplaceAll(a.Body, token, replacement)
			for key, v := range a.Params {
				if s, ok := v.(string); ok {
					a.Params[key] = strings.ReplaceAll(s, token, replacement)
				}
				if ss, ok := v.([]string); ok {
					for i := range ss {
						ss[i] = strings.ReplaceAll(ss[i], token, replacement)
					}
				}
			}
		case throughputShapingTimer:
			schedule, err := messagingSchedule(c, plan)
			if err != nil {
				block("jmeter_throughput_schedule_invalid")
				continue
			}
			exists := false
			for _, s := range script.Config.SamplerSchedules {
				if s.ID == schedule.ID {
					exists = true
				}
			}
			if !exists {
				script.Config.SamplerSchedules = append(script.Config.SamplerSchedules, schedule)
				result.Stats.Timers++
			}
			a.PacingIDs = append(a.PacingIDs, schedule.ID)
		}
		if isMessagingContributor(c.Name) {
			*mappings = append(*mappings, jmeterSourceMapping(file, c, c.Name, "", messagingNodeID(plan, c), "contributor", "converted", "Source configuration retained; runtime compatibility remains gated."))
		}
	}
	// Unknown functions/variables must never survive as a runnable literal.
	check := func(s string) {
		if strings.Contains(s, "{{var.") || strings.Contains(s, "${") {
			block("jmeter_messaging_variable_unresolved")
		}
	}
	check(a.Body)
	for _, v := range a.Params {
		if s, ok := v.(string); ok {
			check(s)
		}
		if ss, ok := v.([]string); ok {
			for _, s := range ss {
				check(s)
			}
		}
	}
	for _, n := range node.Children {
		if n.Element == nil {
			continue
		}
		if !jmeterElementEnabled(n.Element) {
			appendJMeterDisabledMapping(mappings, file, n.Element)
			continue
		}
		if n.Element.Name == "HeaderManager" && expected == "eventhubs" {
			diagnostics.Add(diag.Warning, "JMeter HeaderManager", jmeterElementLocation(n.Element), "PNOP Event Hubs does not consume HTTP headers; intended application properties require an explicit repair")
			*mappings = append(*mappings, jmeterSourceMapping(file, n.Element, "HeaderManager", "", a.Name, "contributor", "ignored", "HTTP headers are ineffective for PNOP Event Hubs and are not sent as application properties."))
		} else if !isMessagingContributor(n.Element.Name) {
			block("jmeter_messaging_child_unsupported")
		}
	}
	index := len(script.Actions)
	script.Actions = append(script.Actions, a)
	result.Stats.Samplers++
	result.Stats.ConvertedActions++
	if result.Stats.SkippedSamplers > 0 {
		result.Stats.SkippedSamplers--
	}
	*mappings = append(*mappings, jmeterSourceMapping(file, e, e.Name, fmt.Sprintf("actions[%d]", index), a.Name, "sampler", "review_only", "Native messaging draft preserved; execution requires compatibility and source blockers to be resolved."))
}

func messagingParameter(e *xmlNode, plan string) (dsl.Parameter, error) {
	p := dsl.Parameter{Scope: "source_engine", ScopeID: messagingNodeID(plan, e), Update: "each_iteration", OnExhaustion: "wrap"}
	integer := func(key string) (int, error) { return strconv.Atoi(prop(e, key)) }
	var err error
	if e.Name == "CounterConfig" {
		p.Name = prop(e, "CounterConfig.name")
		p.Type = "counter"
		if p.Start, err = integer("CounterConfig.start"); err != nil {
			return p, err
		}
		if p.End, err = integer("CounterConfig.end"); err != nil {
			return p, err
		}
		if p.Step, err = integer("CounterConfig.incr"); err != nil || p.Step == 0 {
			return p, fmt.Errorf("invalid counter increment")
		}
		p.Format = prop(e, "CounterConfig.format")
		if prop(e, "CounterConfig.per_user") == "true" {
			p.Scope = "vuser"
		}
		if prop(e, "CounterConfig.reset_on_tg_iteration") == "true" {
			return p, fmt.Errorf("counter reset mode requires compatibility")
		}
	} else {
		p.Name = prop(e, "variableName")
		p.Type = "random_number"
		p.RandomAlgorithm = "java48"
		if p.Min, err = integer("minimumValue"); err != nil {
			return p, err
		}
		if p.Max, err = integer("maximumValue"); err != nil {
			return p, err
		}
		p.Format = prop(e, "outputFormat")
		if prop(e, "perThread") == "true" {
			p.Scope = "vuser"
		}
		if seed := prop(e, "randomSeed"); seed != "" {
			v, e := strconv.ParseInt(seed, 10, 64)
			if e != nil {
				return p, e
			}
			p.Seed = &v
		}
	}
	if p.Name == "" || p.Format != "" {
		return p, fmt.Errorf("unsupported variable format")
	}
	return p, nil
}
func messagingSchedule(e *xmlNode, plan string) (dsl.SamplerSchedule, error) {
	s := dsl.SamplerSchedule{ID: messagingNodeID(plan, e)}
	for _, collection := range childProps(e, "load_profile") {
		for _, row := range collection.Children {
			if len(row.Children) != 3 {
				return s, fmt.Errorf("schedule requires three columns")
			}
			start, e1 := strconv.ParseFloat(strings.TrimSpace(row.Children[0].Text), 64)
			end, e2 := strconv.ParseFloat(strings.TrimSpace(row.Children[1].Text), 64)
			duration, e3 := strconv.Atoi(strings.TrimSpace(row.Children[2].Text))
			if e1 != nil || e2 != nil || e3 != nil {
				return s, fmt.Errorf("invalid schedule")
			}
			s.Segments = append(s.Segments, dsl.SamplerRateSegment{StartRPS: start, EndRPS: end, DurationSeconds: duration})
		}
	}
	if issues := dsl.ValidateSamplerSchedule(s); len(issues) > 0 {
		return s, fmt.Errorf("invalid schedule: %s", issues[0])
	}
	return s, nil
}

func convertDIKafka(e *xmlNode, scope []*treeNode, ctx *convertContext, script *dsl.Script, a *dsl.Action, file string, block func(string)) {
	identity := prop(e, "kafkaProducerClientVariableName")
	var configs []*xmlNode
	seen := map[*xmlNode]bool{}
	for _, n := range scope {
		if n != nil && n.Element != nil && n.Element.Name == diKafkaConfig && jmeterElementEnabled(n.Element) && prop(n.Element, "kafkaProducerClientVariableName") == identity && !seen[n.Element] {
			seen[n.Element] = true
			configs = append(configs, n.Element)
		}
	}
	a.Params["topic"] = convertVariables(prop(e, "kafkaTopic"))
	if key, present := rawMessagingProp(e, "kafkaMessageKey"); present {
		a.Params["key"] = convertVariables(key)
	}
	if partition := prop(e, "partitionString"); partition != "" {
		a.Params["partition"] = convertVariables(partition)
	}
	body, _ := rawMessagingProp(e, "kafkaMessage")
	a.Body = convertVariables(body)
	if len(configs) != 1 {
		block("jmeter_kafka_configuration_missing_or_ambiguous")
		return
	}
	c := configs[0]
	effective := map[string]string{"bootstrap.servers": prop(c, "kafkaBrokers"), "batch.size": prop(c, "batchSize"), "client.id": prop(c, "clientId"), "security.protocol": strings.TrimPrefix(prop(c, "securityType"), "securityType."), "key.serializer": prop(c, "serializerKey"), "value.serializer": prop(c, "serializerValue")}
	base := map[string]string{}
	for k, v := range effective {
		base[k] = v
	}
	for _, collection := range childProps(c, "extraConfigs") {
		for _, row := range collection.Children {
			effective[prop(row, "configKey")] = prop(row, "configValue")
		}
	}
	cfg := &script.Config.Kafka
	cfg.Brokers = strings.Split(effective["bootstrap.servers"], ",")
	cfg.ClientID = effective["client.id"]
	cfg.SecurityProtocol = strings.ToLower(effective["security.protocol"])
	cfg.SASLMechanism = strings.ToLower(effective["sasl.mechanism"])
	cfg.Acks = effective["acks"]
	switch cfg.Acks {
	case "-1":
		cfg.Acks = "all"
	case "1":
		cfg.Acks = "leader"
	case "0":
		cfg.Acks = "none"
	}
	if effective["enable.idempotence"] == "false" {
		cfg.Idempotence = "disabled"
	}
	if effective["enable.idempotence"] == "true" {
		cfg.Idempotence = "enabled"
	}
	cfg.Topic = prop(e, "kafkaTopic")
	if _, explicit := a.Params["partition"]; explicit {
		cfg.PartitionStrategy = "explicit"
	}
	safe := map[string]string{}
	ignored := []string{}
	oauthSource := map[string]interface{}{}
	allowed := map[string]bool{}
	for _, key := range []string{"bootstrap.servers", "batch.size", "client.id", "security.protocol", "key.serializer", "value.serializer", "acks", "retries", "linger.ms", "max.block.ms", "buffer.memory", "enable.idempotence", "ssl.enabled.protocols", "sasl.mechanism", "sasl.oauthbearer.token.endpoint.url", "sasl.login.callback.handler.class"} {
		allowed[key] = true
	}
	for key, value := range effective {
		switch key {
		case "sasl.jaas.config":
			safe[key] = "[credential options withheld]"
			options, err := parseMessagingOAuthJAAS(value)
			if err != nil {
				block("jmeter_kafka_jaas_unsupported_or_malformed")
			}
			if err == nil {
				names := make([]string, 0, len(options))
				for name := range options {
					names = append(names, name)
				}
				sort.Strings(names)
				oauthSource["option_names"] = names
				oauthSource["scope"] = options["scope"]
				oauthSource["credentials"] = "protected binding required"
			}
			_, legacyID := options["cid"]
			_, legacySecret := options["cs"]
			if legacyID || legacySecret {
				block("jmeter_kafka_nonstandard_oauth_options")
			}
			block("jmeter_kafka_oauth_credentials_required")
		case "group", "session.timeout.ms", "heartbeat.interval.ms":
			ignored = append(ignored, key)
		default:
			if allowed[key] {
				safe[key] = value
			} else {
				block("jmeter_kafka_effective_property_unsupported")
			}
		}
	}
	for _, key := range []string{"key.serializer", "value.serializer"} {
		if effective[key] != "org.apache.kafka.common.serialization.StringSerializer" {
			block("jmeter_kafka_serializer_unsupported")
		}
	}
	if callback := effective["sasl.login.callback.handler.class"]; callback != "" && callback != "org.apache.kafka.common.security.oauthbearer.secured.OAuthBearerLoginCallbackHandler" {
		block("jmeter_kafka_callback_unsupported")
	}
	for _, headers := range childProps(e, "messageHeaders") {
		if len(headers.Children) > 0 {
			block("jmeter_kafka_headers_unsupported")
		}
	}
	for _, key := range []string{"kafkaSslKeystore", "kafkaSslTruststore", "kafkaSslKeystorePassword", "kafkaSslTruststorePassword", "kafkaSslPrivateKeyPass"} {
		if prop(c, key) != "" {
			block("jmeter_kafka_keystore_unsupported")
		}
	}
	sort.Strings(ignored)
	a.Metadata["jmeter_kafka"] = map[string]interface{}{"source_node_id": messagingNodeID(script.Config.JMeter.SourcePlanID, c), "base_settings": base, "effective_settings": safe, "ignored_producer_settings": ignored, "producer_variable": identity, "oauth_source": oauthSource, "producer_lifecycle": "source_engine", "sample_timing": "send_acknowledgement"}
}

// Collection properties carry their values in children, not Text. Looking only
// at prop() would silently discard structured message options.
func messagingPropertyHasValue(n *xmlNode, key string) bool {
	if n.Attrs["name"] == key {
		return strings.TrimSpace(n.Text) != "" || len(n.Children) > 0
	}
	for _, child := range n.Children {
		if messagingPropertyHasValue(child, key) {
			return true
		}
	}
	return false
}

func convertPNOPEventHubs(e *xmlNode, ctx *convertContext, script *dsl.Script, a *dsl.Action, file string, block func(string)) {
	cfg := &script.Config.EventHubs
	target := credentialpolicy.TargetRef{RequestKey: script.Name, Field: "config.eventhubs.connection_string"}
	if cfg.ConnectionString != "" {
		target = credentialpolicy.TargetRef{RequestKey: a.Name, Field: "metadata.jmeter_eventhubs.native_config.connection_string"}
	}
	cfg.Namespace = prop(e, "namespaceName")
	cfg.EventHub = prop(e, "eventHubName")
	cfg.AuthType = "connection_string"
	a.Params["event_hub"] = cfg.EventHub
	if prop(e, "partitionType") == "ID" {
		a.Params["partition_id"] = convertVariables(prop(e, "partitionValue"))
	} else if prop(e, "partitionType") != "" {
		block("jmeter_eventhubs_partition_mode_unsupported")
	}
	var messages []string
	for _, list := range childProps(e, "Messages.messages") {
		for _, m := range list.Children {
			if m.Attrs["elementType"] != "jp.co.pnop.jmeter.protocol.amqp.sampler.AzAmqpMessage" || prop(m, "Message.messageType") != "String" {
				block("jmeter_eventhubs_message_encoding_unsupported")
			}
			body, _ := rawMessagingProp(m, "Message.message")
			messages = append(messages, convertVariables(body))
			for _, key := range []string{"Message.partitionKey", "Message.messageId", "Message.groupId", "Message.customProperties", "Message.contentType", "Message.label", "Message.standardProperties"} {
				if messagingPropertyHasValue(m, key) {
					block("jmeter_eventhubs_message_option_unsupported")
				}
			}
		}
	}
	if len(messages) == 1 {
		a.Body = messages[0]
	} else {
		a.Params["messages"] = messages
		if len(messages) == 0 {
			block("jmeter_eventhubs_messages_missing")
		} else {
			block("jmeter_eventhubs_batch_runtime_required")
		}
	}
	a.Metadata["jmeter_eventhubs"] = map[string]interface{}{"message_count": len(messages), "producer_lifecycle": "per_sample", "sample_timing": "create_send_excludes_close", "shared_access_key_name": prop(e, "sharedAccessKeyName")}
	if prop(e, "authType") != "Shared access signature" {
		block("jmeter_eventhubs_authentication_unsupported")
	}
	sourceCredential := "***"
	if key := prop(e, "sharedAccessKey"); credentialpolicy.ClassifySourceCredentialValue(key) == credentialpolicy.SourceCredentialValueAvailable {
		sourceCredential = "Endpoint=sb://" + cfg.Namespace + "/;SharedAccessKeyName=" + prop(e, "sharedAccessKeyName") + ";SharedAccessKey=" + key + ";EntityPath=" + cfg.EventHub
	}
	requirement, replacement, err := credentialpolicy.NewRequirement(credentialpolicy.RequirementSpec{Key: "eventhubs_connection_string", Label: "Event Hubs connection for " + script.Name, NameHint: "JMETER_" + strings.ToUpper(sanitizeName(script.Name)) + "_EVENTHUBS", Source: credentialpolicy.SourceRef{Format: "jmeter", Path: file, Pointer: messagingNodeID(script.Config.JMeter.SourcePlanID, e), ItemID: script.Name}, Targets: []credentialpolicy.TargetRef{target}, Value: sourceCredential, Mode: credentialpolicy.SecretReference, Registry: ctx.credentialNames})
	if err != nil {
		if ctx.importErr != nil {
			*ctx.importErr = err
		}
		block("jmeter_eventhubs_credentials_required")
		return
	}
	cfg.ConnectionString = replacement
	ctx.credentialRequirements = append(ctx.credentialRequirements, requirement)
	block("jmeter_eventhubs_credentials_required")
}

func messagingAdvisories(script *dsl.Script, result *ConvertResult) {
	if script.Config.JMeter == nil {
		return
	}
	result.Warnings = append(result.Warnings,
		"Messaging payloads retain static source data; parameterize only through an explicit reviewed correction.",
		"Composite timestamps retain separate evaluations; the literal Z requires a confirmed source timezone and can span a date boundary.",
		"Partition identifiers must exist on the destination; import cannot verify broker or Event Hubs partition availability.",
		"Producer acknowledgement does not verify downstream business processing; this source has no imported downstream verification.")
	raw, _ := json.Marshal(script.Actions)
	for _, p := range script.Parameters {
		if p.Type == "counter" && !strings.Contains(string(raw), "{{param."+p.Name+"}}") {
			result.Warnings = append(result.Warnings, "Counter "+p.Name+" is retained but unused by the messaging actions.")
		}
	}
}

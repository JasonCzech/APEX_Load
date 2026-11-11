package jmeter

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func messagingFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, e := os.ReadFile("testdata/messaging_" + name + ".jmx")
	if e != nil {
		t.Fatal(e)
	}
	return b
}

func TestMessagingReviewIdentityAndUnmappedContributors(t *testing.T) {
	data := messagingFixture(t, "original")
	first, err := ConvertJMX(data, ConvertOptions{FileName: "plan.jmx", ScenarioDraft: true})
	if err != nil {
		t.Fatal(err)
	}
	repeat, err := ConvertJMX(data, ConvertOptions{FileName: "plan.jmx", ScenarioDraft: true})
	if err != nil {
		t.Fatal(err)
	}
	changed, err := ConvertJMX([]byte(strings.Replace(string(data), "fixture.events", "fixture.other", 1)), ConvertOptions{FileName: "plan.jmx", ScenarioDraft: true})
	if err != nil {
		t.Fatal(err)
	}
	for i, mapping := range first.SourceReview.Mappings {
		if mapping.ID != repeat.SourceReview.Mappings[i].ID {
			t.Fatal("unstable source identity")
		}
		if strings.HasSuffix(mapping.TargetPath, ".") {
			t.Fatalf("unmapped contributor assigned to script: %s", mapping.TargetPath)
		}
		for _, other := range changed.SourceReview.Mappings {
			if mapping.ID == other.ID {
				t.Fatal("different source plans share a review identity")
			}
		}
	}
}
func TestMessagingPlanPreservesThreeBlockedNativeDrafts(t *testing.T) {
	for _, name := range []string{"original", "repaired"} {
		t.Run(name, func(t *testing.T) {
			r, e := ConvertJMX(messagingFixture(t, name), ConvertOptions{FileName: "messaging.jmx", ScenarioDraft: true})
			if e != nil {
				t.Fatal(e)
			}
			if len(r.Scripts) != 3 || r.ScenarioDraft == nil || len(r.ScenarioDraft.Groups) != 3 {
				t.Fatalf("incomplete conversion: %+v", r.Stats)
			}
			if r.Stats.Samplers != 3 || r.Stats.ConvertedActions != 3 {
				t.Fatalf("counts: %+v", r.Stats)
			}
			d := r.ScenarioDraft
			if d.Duration != "1h5m0s" || d.GeneratorAffinity != "single_generator" {
				t.Fatalf("scenario: %+v", d)
			}
			total := 0.0
			for i, s := range r.Scripts {
				want := []string{"kafka", "eventhubs", "eventhubs"}[i]
				if s.Protocol != want || len(s.Actions) != 1 {
					t.Fatalf("script %d: %s %d actions", i, s.Protocol, len(s.Actions))
				}
				g := d.Groups[i]
				if g.VUsers != []int{100, 50, 50}[i] || g.RampUp != "1s" || g.SourceGroupID != s.Config.JMeter.SourceGroupID {
					t.Fatalf("group: %+v", g)
				}
				if len(s.Config.SamplerSchedules) != 1 || len(s.Actions[0].PacingIDs) != 1 {
					t.Fatal("lost sampler schedule")
				}
				schedule := s.Config.SamplerSchedules[0]
				if schedule.Duration() != 3900*time.Second || schedule.Segments[0].StartRPS != 1 || schedule.Segments[1].EndRPS != []float64{84, 42, 42}[i] {
					t.Fatalf("schedule: %+v", schedule)
				}
				total += schedule.PlannedSamples()
				if s.Actions[0].Metadata["execution_blocker"] == nil {
					t.Fatal("uncertified import marked ready")
				}
				if strings.Contains(s.Actions[0].Body, "${") || strings.Contains(s.Actions[0].Body, "{{var.") {
					t.Fatal("unresolved body")
				}
				if issues := dsl.ValidateScriptIssues(s); len(issues) > 0 {
					t.Fatalf("blocked draft is not saveable: %v", issues)
				}
				var payload any
				if e := json.Unmarshal([]byte(s.Actions[0].Body), &payload); e != nil {
					t.Fatal(e)
				}
				var formatted bool
				for _, p := range s.Parameters {
					if p.Format == "HH:mm:ss.S" && p.FormatDialect == "java_simple_date_format" {
						formatted = true
					}
				}
				if !formatted {
					t.Fatal("lost single-S millisecond semantics")
				}
			}
			if total != 630450 {
				t.Fatalf("planned samples=%v", total)
			}
			kafka := r.Scripts[0]
			if key, ok := kafka.Actions[0].Params["key"]; !ok || key != "" {
				t.Fatal("empty key lost")
			}
			settings := kafka.Actions[0].Metadata["jmeter_kafka"].(map[string]interface{})
			if settings["base_settings"].(map[string]string)["batch.size"] != "9999999" || settings["effective_settings"].(map[string]string)["batch.size"] != "16384" {
				t.Fatal("property precedence lost")
			}
			if kafka.Config.Kafka.SecurityProtocol != "sasl_ssl" || kafka.Config.Kafka.Idempotence != "disabled" || kafka.Config.Kafka.Acks != "all" {
				t.Fatal("effective config lost")
			}
			if r.Scripts[1].Config.EventHubs.ConnectionString == r.Scripts[2].Config.EventHubs.ConnectionString {
				t.Fatal("east/west credentials conflated")
			}
			if len(r.Scripts[1].Actions[0].Headers) != 0 {
				t.Fatal("HTTP headers became messaging properties")
			}
		})
	}
}
func TestMessagingMissingConfigurationRetainsSampler(t *testing.T) {
	input := `<jmeterTestPlan><hashTree><TestPlan/><hashTree><ThreadGroup/><hashTree><com.di.jmeter.kafka.sampler.KafkaProducerSampler testname="send"><stringProp name="kafkaProducerClientVariableName">missing</stringProp><stringProp name="kafkaMessage">body</stringProp></com.di.jmeter.kafka.sampler.KafkaProducerSampler><hashTree/></hashTree></hashTree></hashTree></jmeterTestPlan>`
	r, e := ConvertJMX([]byte(input), ConvertOptions{})
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(r.Scripts[0].Actions[0])
	if !strings.Contains(string(b), "jmeter_kafka_configuration_missing_or_ambiguous") {
		t.Fatal("missing configuration not blocked")
	}
}
func TestScenarioAssociationUsesSourceIdentityAfterOmittedGroup(t *testing.T) {
	input := `<jmeterTestPlan><hashTree><TestPlan/><hashTree><ThreadGroup testname="Empty"><stringProp name="ThreadGroup.num_threads">99</stringProp></ThreadGroup><hashTree/><ThreadGroup testname="HTTP"><stringProp name="ThreadGroup.num_threads">7</stringProp><stringProp name="ThreadGroup.ramp_time">3</stringProp></ThreadGroup><hashTree><HTTPSamplerProxy testname="get"><stringProp name="HTTPSampler.domain">example.test</stringProp><stringProp name="HTTPSampler.path">/</stringProp></HTTPSamplerProxy><hashTree/></hashTree></hashTree></hashTree></jmeterTestPlan>`
	r, e := ConvertJMX([]byte(input), ConvertOptions{ScenarioDraft: true})
	if e != nil {
		t.Fatal(e)
	}
	if len(r.Scripts) != 1 || r.ScenarioDraft.Groups[0].VUsers != 7 || r.ScenarioDraft.Groups[0].RampUp != "3s" {
		t.Fatalf("shifted group settings: %+v", r.ScenarioDraft)
	}
}
func TestMessagingScheduleRejectsInvalidRows(t *testing.T) {
	// "0" was rejected here before: TST accepts 0 requests/s (see
	// TestMessagingScheduleAcceptsZeroRateWarmUp), so it is no longer invalid.
	for _, row := range []string{"NaN", "-1", "+Inf", "10000001"} {
		n := &xmlNode{Name: throughputShapingTimer, Children: []*xmlNode{{Attrs: map[string]string{"name": "load_profile"}, Children: []*xmlNode{{Children: []*xmlNode{{Text: row}, {Text: "42"}, {Text: "300"}}}}}}}
		if _, e := messagingSchedule(n, "plan"); e == nil {
			t.Fatalf("accepted %s", row)
		}
	}
}

func shapingTimerNode(rows ...[3]string) *xmlNode {
	collection := &xmlNode{Attrs: map[string]string{"name": "load_profile"}}
	for _, row := range rows {
		collection.Children = append(collection.Children, &xmlNode{Children: []*xmlNode{{Text: row[0]}, {Text: row[1]}, {Text: row[2]}}})
	}
	return &xmlNode{Name: throughputShapingTimer, Children: []*xmlNode{collection}}
}

func TestMessagingScheduleAcceptsZeroRateWarmUp(t *testing.T) {
	schedule, err := messagingSchedule(shapingTimerNode([3]string{"0", "18", "60"}, [3]string{"18", "18", "3600"}), "plan")
	if err != nil {
		t.Fatalf("0 -> 18 rps warm-up rejected: %v", err)
	}
	if len(schedule.Segments) != 2 || schedule.Segments[0].StartRPS != 0 || schedule.Segments[0].EndRPS != 18 {
		t.Fatalf("schedule = %+v", schedule)
	}
	if _, err := messagingSchedule(shapingTimerNode([3]string{"0", "0", "60"}), "plan"); err == nil {
		t.Fatal("accepted an all-zero schedule, which is not supported")
	}
}

func TestMessagingSourcePreservesCredentialsVerbatim(t *testing.T) {
	source := string(messagingFixture(t, "original"))
	source = strings.Replace(source, `name="fixture message"`, `name="echo-credential-canary"`, 1)
	source = strings.Replace(source, `<stringProp name="sharedAccessKey">***</stringProp>`, `<stringProp name="sharedAccessKey">echo-credential-canary</stringProp>`, 1)
	r, e := ConvertJMX([]byte(source), ConvertOptions{})
	if e != nil {
		t.Fatal(e)
	}
	if r.SourceReview.SourceFiles[0].Content != source || r.SourceReview.SourceFiles[0].Redacted {
		t.Fatal("source review did not preserve raw JMX verbatim")
	}
	if !strings.Contains(r.Scripts[1].Config.EventHubs.ConnectionString, "SharedAccessKey=echo-credential-canary") {
		t.Fatalf("Event Hubs connection string lost literal key: %q", r.Scripts[1].Config.EventHubs.ConnectionString)
	}
}

func TestMessagingSourcePreservesExtraPropertyCredentialsAndKafkaJAASOptions(t *testing.T) {
	for _, key := range []string{"ssl.key.password", "sasl.password", "clientSecret"} {
		source := strings.Replace(string(messagingFixture(t, "original")), `<collectionProp name="extraConfigs">`, `<collectionProp name="extraConfigs"><elementProp name="secret-row"><stringProp name="configKey">`+key+`</stringProp><stringProp name="configValue">unsupported-secret-canary</stringProp></elementProp>`, 1)
		r, err := ConvertJMX([]byte(source), ConvertOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(r.SourceReview.SourceFiles[0].Content, "unsupported-secret-canary") || r.SourceReview.SourceFiles[0].Redacted {
			t.Fatalf("%s was scrubbed from source review", key)
		}
	}
	repaired, err := ConvertJMX(messagingFixture(t, "repaired"), ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	kafka := repaired.Scripts[0]
	if kafka.Config.Kafka.OAuth == nil || kafka.Config.Kafka.OAuth.ClientID != "***" || kafka.Config.Kafka.OAuth.ClientSecret != "***" {
		t.Fatalf("Kafka OAuth literal options were rewritten: %#v", kafka.Config.Kafka.OAuth)
	}
	if hasJMeterReviewReason(kafka.Actions, "jmeter_kafka_oauth_credentials_required") {
		t.Fatal("literal Kafka OAuth options should not emit jmeter_kafka_oauth_credentials_required")
	}
	meta := kafka.Actions[0].Metadata["jmeter_kafka"].(map[string]interface{})
	effective := meta["effective_settings"].(map[string]string)
	if effective["sasl.jaas.config"] == "[credential options withheld]" || !strings.Contains(effective["sasl.jaas.config"], "OAuthBearerLoginModule") {
		t.Fatalf("sasl.jaas.config was withheld: %q", effective["sasl.jaas.config"])
	}
	oauthSource := meta["oauth_source"].(map[string]interface{})
	if _, withheld := oauthSource["credentials"]; withheld {
		t.Fatalf("oauth_source retained protected binding sentinel: %#v", oauthSource)
	}
}

func TestMessagingNumericKafkaAcknowledgements(t *testing.T) {
	for source, want := range map[string]string{"-1": "all", "1": "leader", "0": "none"} {
		data := strings.ReplaceAll(string(messagingFixture(t, "original")), `<stringProp name="configValue">all</stringProp>`, `<stringProp name="configValue">`+source+`</stringProp>`)
		r, err := ConvertJMX([]byte(data), ConvertOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if r.Scripts[0].Config.Kafka.Acks != want {
			t.Fatalf("acks=%s was not normalized", source)
		}
		meta := r.Scripts[0].Actions[0].Metadata["jmeter_kafka"].(map[string]interface{})
		if meta["effective_settings"].(map[string]string)["acks"] != source {
			t.Fatal("lost authored acknowledgement value")
		}
	}
}

func TestMessagingStructuredEventHubsOptionsAreBlocked(t *testing.T) {
	data := strings.Replace(string(messagingFixture(t, "original")), `<stringProp name="Message.messageType">String</stringProp>`, `<stringProp name="Message.messageType">String</stringProp><collectionProp name="Message.customProperties"><stringProp name="trace">value</stringProp></collectionProp>`, 1)
	r, err := ConvertJMX([]byte(data), ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(r.Scripts[1].Actions)
	if !strings.Contains(string(b), "jmeter_eventhubs_message_option_unsupported") {
		t.Fatal("structured message properties silently dropped")
	}
}

func TestMessagingUnparseableJAASPreservesSourceReviewAndBlocksDraft(t *testing.T) {
	data := strings.Replace(string(messagingFixture(t, "original")), `cid="***" cs="***"`, `cid="id" cs="line\nsecret"`, 1)
	if !strings.Contains(data, `line\nsecret`) {
		t.Fatal("JAAS fixture replacement did not apply")
	}
	r, err := ConvertJMX([]byte(data), ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if r.SourceReview.SourceFiles[0].Content != data || r.SourceReview.SourceFiles[0].Redacted {
		t.Fatal("unparseable JAAS source was scrubbed or omitted")
	}
	if !hasJMeterReviewReason(r.Scripts[0].Actions, "jmeter_kafka_jaas_unsupported_or_malformed") {
		t.Fatal("malformed JAAS was not blocked")
	}
}

func TestMessagingMultipleEventHubsConfigurationsRemainDistinct(t *testing.T) {
	// Duplicate a sampler inside the same group, changing only its namespace.
	source := string(messagingFixture(t, "original"))
	tag := "<" + pnopEventHubsSampler + " "
	start := strings.Index(source, tag)
	end := strings.Index(source[start:], "</"+pnopEventHubsSampler+">") + start + len("</"+pnopEventHubsSampler+">")
	if start < 0 || end < start {
		t.Fatal("missing sampler fixture")
	}
	duplicate := strings.ReplaceAll(source[start:end], "fixture-east", "fixture-other") + "<hashTree/>"
	source = source[:start] + duplicate + source[start:]
	r, err := ConvertJMX([]byte(source), ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s := r.Scripts[1]
	if len(s.Actions) != 2 {
		t.Fatalf("actions=%d", len(s.Actions))
	}
	if s.Config.EventHubs.Namespace != "fixture-other.servicebus.windows.net" {
		t.Fatal("later sampler overwrote first connection")
	}
	meta := s.Actions[1].Metadata["jmeter_eventhubs"].(map[string]interface{})
	config := meta["native_config"].(dsl.EventHubsConfig)
	if config.Namespace != "fixture-east.servicebus.windows.net" || config.ConnectionString == s.Config.EventHubs.ConnectionString {
		t.Fatal("second sampler connection lost")
	}
	action, _ := json.Marshal(s.Actions[1])
	if !strings.Contains(string(action), "jmeter_multiple_producer_configurations_unsupported") {
		t.Fatal("per-sampler connection not specifically blocked")
	}
}

func TestMessagingMultipleKafkaConfigurationsDoNotOverwrite(t *testing.T) {
	config := func(id, broker string) string {
		return fmt.Sprintf(`<%s><stringProp name="kafkaProducerClientVariableName">%s</stringProp><stringProp name="kafkaBrokers">%s:9092</stringProp><stringProp name="securityType">securityType.plaintext</stringProp><stringProp name="serializerKey">org.apache.kafka.common.serialization.StringSerializer</stringProp><stringProp name="serializerValue">org.apache.kafka.common.serialization.StringSerializer</stringProp></%s><hashTree/>`, diKafkaConfig, id, broker, diKafkaConfig)
	}
	sampler := func(id string) string {
		return fmt.Sprintf(`<%s testname="%s"><stringProp name="kafkaProducerClientVariableName">%s</stringProp><stringProp name="kafkaTopic">events</stringProp><stringProp name="kafkaMessage">payload</stringProp></%s><hashTree/>`, diKafkaSampler, id, id, diKafkaSampler)
	}
	first := strings.Replace(strings.Replace(config("one", "first.test"), "securityType.plaintext", "securityType.sasl_ssl", 1), "</"+diKafkaConfig+">", `<collectionProp name="extraConfigs"><elementProp name="mech"><stringProp name="configKey">sasl.mechanism</stringProp><stringProp name="configValue">OAUTHBEARER</stringProp></elementProp><elementProp name="tls"><stringProp name="configKey">ssl.enabled.protocols</stringProp><stringProp name="configValue">TLSv1.2</stringProp></elementProp><elementProp name="oauth"><stringProp name="configKey">sasl.jaas.config</stringProp><stringProp name="configValue">org.apache.kafka.common.security.oauthbearer.OAuthBearerLoginModule required clientId="synthetic" clientSecret="***";</stringProp></elementProp></collectionProp></`+diKafkaConfig+">", 1)
	input := `<jmeterTestPlan><hashTree><TestPlan/><hashTree><ThreadGroup/><hashTree>` + first + config("two", "second.test") + sampler("one") + sampler("two") + `</hashTree></hashTree></hashTree></jmeterTestPlan>`
	r, err := ConvertJMX([]byte(input), ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s := r.Scripts[0]
	if s.Config.Kafka.Brokers[0] != "first.test:9092" {
		t.Fatal("second producer overwrote first")
	}
	if s.Config.JMeter.ConfigurationID != s.Actions[0].Metadata["jmeter_kafka"].(map[string]interface{})["source_node_id"] {
		t.Fatal("second producer overwrote first configuration identity")
	}
	meta := s.Actions[1].Metadata["jmeter_kafka"].(map[string]interface{})
	native := meta["native_config"].(dsl.KafkaConfig)
	if native.OAuth != nil || native.TLS != nil {
		t.Fatal("second producer inherited first credentials or TLS")
	}
	if s.Config.Kafka.OAuth == nil || s.Config.Kafka.TLS == nil {
		t.Fatal("first producer lost optional settings")
	}
	if meta["native_config"].(dsl.KafkaConfig).Brokers[0] != "second.test:9092" {
		t.Fatal("second producer config lost")
	}
	action, _ := json.Marshal(s.Actions[1])
	if !strings.Contains(string(action), "jmeter_multiple_producer_configurations_unsupported") {
		t.Fatal("distinct producer configs not blocked")
	}

	identical := `<jmeterTestPlan><hashTree><TestPlan/><hashTree><ThreadGroup/><hashTree>` + config("one", "same.test") + config("two", "same.test") + sampler("one") + sampler("two") + `</hashTree></hashTree></hashTree></jmeterTestPlan>`
	same, err := ConvertJMX([]byte(identical), ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	firstID := same.Scripts[0].Actions[0].Metadata["jmeter_kafka"].(map[string]interface{})["source_node_id"]
	if same.Scripts[0].Config.JMeter.ConfigurationID != firstID {
		t.Fatal("identical settings collapsed distinct producer identities")
	}
	action, _ = json.Marshal(same.Scripts[0].Actions[1])
	if !strings.Contains(string(action), "jmeter_multiple_producer_configurations_unsupported") {
		t.Fatal("separate producer lifetimes silently combined")
	}
}

func TestMessagingScenarioUsesLongestSchedule(t *testing.T) {
	source := string(messagingFixture(t, "original"))
	index := strings.LastIndex(source, ">3600<")
	if index < 0 {
		t.Fatal("missing duration")
	}
	source = source[:index] + strings.Replace(source[index:], ">3600<", ">1800<", 1)
	r, err := ConvertJMX([]byte(source), ConvertOptions{ScenarioDraft: true})
	if err != nil {
		t.Fatal(err)
	}
	if r.ScenarioDraft.Duration != "1h5m0s" || r.ScenarioDraft.Groups[2].SamplerSchedules[0].Duration() != 35*time.Minute {
		t.Fatal("later group truncated source plan")
	}
}

func TestMessagingWithoutScheduleDoesNotInventFiveMinutes(t *testing.T) {
	source := `<jmeterTestPlan><hashTree><TestPlan/><hashTree><ThreadGroup/><hashTree><com.di.jmeter.kafka.sampler.KafkaProducerSampler testname="send"/><hashTree/></hashTree></hashTree></hashTree></jmeterTestPlan>`
	r, err := ConvertJMX([]byte(source), ConvertOptions{ScenarioDraft: true})
	if err != nil {
		t.Fatal(err)
	}
	if r.ScenarioDraft.Duration != "" {
		t.Fatalf("invented duration %q", r.ScenarioDraft.Duration)
	}
}
func TestMessagingWorkloadAccountingSeparatesDisabledAndReporting(t *testing.T) {
	r, e := ConvertJMX(messagingFixture(t, "original"), ConvertOptions{})
	if e != nil {
		t.Fatal(e)
	}
	s := r.Stats
	if s.EnabledWorkloadSamplers != 3 || s.DisabledWorkloadSamplers != 2 || s.ConvertedWorkloadSamplers != 3 || s.BlockedWorkloadSamplers != 3 || s.IgnoredReportingElements == 0 || s.ConfigurationContributors < 9 {
		t.Fatalf("accounting: %+v", s)
	}
}
func TestMessagingSamplerWithinControllerIsPreserved(t *testing.T) {
	input := `<jmeterTestPlan><hashTree><TestPlan/><hashTree><ThreadGroup/><hashTree><LoopController testname="loop"><stringProp name="LoopController.loops">2</stringProp></LoopController><hashTree><com.di.jmeter.kafka.sampler.KafkaProducerSampler testname="send"><stringProp name="kafkaTopic">events</stringProp><stringProp name="kafkaMessage">payload</stringProp></com.di.jmeter.kafka.sampler.KafkaProducerSampler><hashTree/></hashTree></hashTree></hashTree></hashTree></jmeterTestPlan>`
	r, e := ConvertJMX([]byte(input), ConvertOptions{})
	if e != nil {
		t.Fatal(e)
	}
	if len(r.Scripts) != 1 || r.Scripts[0].Protocol != "kafka" || r.Scripts[0].Config.JMeter == nil {
		t.Fatal("nested messaging draft lost")
	}
	b, _ := json.Marshal(r.Scripts[0])
	if !strings.Contains(string(b), "kafka_produce") || !strings.Contains(string(b), "payload") {
		t.Fatal("nested sampler lost")
	}
}

func TestMessagingJAASParserFailsClosed(t *testing.T) {
	const prefix = "org.apache.kafka.common.security.oauthbearer.OAuthBearerLoginModule required "
	for _, bad := range []string{"", prefix + `clientId="x" clientId="y";`, strings.TrimSpace(prefix) + `clientId="x";`, prefix + `clientId="x" unsupported="y";`, prefix + `clientId="x"clientSecret="y";`, prefix + `clientSecret="unfinished;`, prefix + `clientId="x"`, "uploaded.CustomLoginModule required clientId=\"x\";"} {
		if _, err := parseMessagingOAuthJAAS(bad); err == nil {
			t.Fatalf("accepted malformed JAAS")
		}
	}
	for _, source := range []string{prefix + `cid="***" cs="***" scope="app/.default";`, prefix + `clientId="client" clientSecret="value" scope="app/.default";`} {
		options, e := parseMessagingOAuthJAAS(source)
		if e != nil || options["scope"] != "app/.default" {
			t.Fatalf("supported JAAS options rejected: %v", e)
		}
	}
}
func TestUnsupportedPluginSamplerRetainsBlockedGroup(t *testing.T) {
	source := `<jmeterTestPlan><hashTree><TestPlan/><hashTree><ThreadGroup testname="unknown"/><hashTree><unknown.PluginSampler testname="unsupported"/><hashTree/></hashTree></hashTree></hashTree></jmeterTestPlan>`
	r, e := ConvertJMX([]byte(source), ConvertOptions{ScenarioDraft: true})
	if e != nil {
		t.Fatal(e)
	}
	if len(r.Scripts) != 1 || len(r.Scripts[0].Actions) != 1 || r.Stats.ConvertedWorkloadSamplers != 0 || r.Stats.BlockedWorkloadSamplers != 1 {
		t.Fatalf("unsupported source silently lost: %+v", r.Stats)
	}
	if r.Scripts[0].Actions[0].Metadata["execution_blocker"] == nil {
		t.Fatal("unsupported plugin is executable")
	}
}

func TestMessagingInvalidProducerTuningRetainsSavableDraft(t *testing.T) {
	source := string(messagingFixture(t, "original"))
	for _, value := range []string{"-1", "999999999999999999999999999", "broken"} {
		t.Run(value, func(t *testing.T) {
			input := strings.ReplaceAll(source, "<stringProp name=\"configValue\">16384</stringProp>", "<stringProp name=\"configValue\">"+value+"</stringProp>")
			if input == source {
				t.Fatal("fixture no longer has batch override")
			}
			result, err := ConvertJMX([]byte(input), ConvertOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Scripts) != 3 {
				t.Fatal("invalid config dropped source groups")
			}
			serialized, _ := json.Marshal(result.Scripts[0])
			if !strings.Contains(string(serialized), "jmeter_kafka_producer_setting_invalid") {
				t.Fatal("invalid source setting not blocked")
			}
			if result.Scripts[0].Config.Kafka.Producer != nil {
				t.Fatal("invalid tuning persisted as runnable native configuration")
			}
		})
	}
}

func TestMessagingInvalidOAuthEndpointRemainsSavableBlockedDraft(t *testing.T) {
	source := string(messagingFixture(t, "original"))
	old := "https://login.microsoftonline.com/00000000-0000-0000-0000-000000000001/oauth2/v2.0/token"
	for _, endpoint := range []string{"", "http://identity.example.test/token", "https://"} {
		result, err := ConvertJMX([]byte(strings.ReplaceAll(source, old, endpoint)), ConvertOptions{})
		if err != nil {
			t.Fatal(err)
		}
		script := result.Scripts[0]
		serialized, _ := json.Marshal(script)
		if !strings.Contains(string(serialized), "jmeter_kafka_oauth_endpoint_invalid") {
			t.Fatal("missing endpoint blocker")
		}
		if issues := dsl.ValidateScriptIssues(script); len(issues) > 0 {
			t.Fatal("source defect prevents blocked draft preservation", issues)
		}
	}
}

func TestMissingEventHubsMessagesRemainSaveableBlockedDrafts(t *testing.T) {
	source := string(messagingFixture(t, "original"))
	missing := strings.ReplaceAll(source, `name="Messages.messages"`, `name="unused.messages"`)
	if missing == source {
		t.Fatal("fixture message list not found")
	}
	result, err := ConvertJMX([]byte(missing), ConvertOptions{ScenarioDraft: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Scripts) != 3 || result.ScenarioDraft == nil {
		t.Fatal("missing messages dropped workload groups")
	}
	for _, script := range result.Scripts[1:] {
		if issues := dsl.ValidateScriptIssues(script); len(issues) > 0 {
			t.Fatalf("blocked draft cannot be saved: %v", issues)
		}
		action, _ := json.Marshal(script.Actions[0])
		if !strings.Contains(string(action), "jmeter_eventhubs_messages_missing") {
			t.Fatal("missing execution blocker")
		}
		if count := script.Actions[0].Metadata["jmeter_eventhubs"].(map[string]interface{})["message_count"]; count != 0 {
			t.Fatal("missing source message count", count)
		}
	}
}

// An all-zero messaging profile is gated, and the import says why instead of
// showing only the gate code.
func TestMessagingInvalidScheduleExplainsTheCause(t *testing.T) {
	source := string(messagingFixture(t, "original"))
	const rows = `<stringProp name="49">1</stringProp>
                <stringProp name="1788">84</stringProp>
                <stringProp name="50547">300</stringProp>
              </collectionProp>
              <collectionProp name="451796357">
                <stringProp name="1788">84</stringProp>
                <stringProp name="1788">84</stringProp>`
	if !strings.Contains(source, rows) {
		t.Fatal("fixture has no shaping profile")
	}
	source = strings.Replace(source, rows, strings.ReplaceAll(strings.ReplaceAll(rows, ">84<", ">0<"), ">1<", ">0<"), 1)
	r, err := ConvertJMX([]byte(source), ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	gated, explained := false, false
	for _, script := range r.Scripts {
		gated = gated || hasJMeterReviewReason(script.Actions, "jmeter_throughput_schedule_invalid")
	}
	for _, d := range r.Diagnostics {
		explained = explained || (d.Severity == diag.Blocker && strings.Contains(d.Detail, "Throughput Shaping Timer") && strings.Contains(d.Detail, "at least one positive rate"))
	}
	if !gated || !explained {
		t.Fatalf("gated=%v explained=%v diagnostics=%+v", gated, explained, r.Diagnostics)
	}
}

func TestMessagingDIKafkaPlainScramAndLiteralTildeOAuth(t *testing.T) {
	makePlan := func(brokers, securityType, mechanism, jaas, extraRows string) []byte {
		return []byte(fmt.Sprintf(`<jmeterTestPlan><hashTree><TestPlan/><hashTree><ThreadGroup testname="kafka"/><hashTree>`+
			`<%s><stringProp name="kafkaProducerClientVariableName">prod</stringProp>`+
			`<stringProp name="kafkaBrokers">%s</stringProp>`+
			`<stringProp name="securityType">%s</stringProp>`+
			`<stringProp name="serializerKey">org.apache.kafka.common.serialization.StringSerializer</stringProp>`+
			`<stringProp name="serializerValue">org.apache.kafka.common.serialization.StringSerializer</stringProp>`+
			`<collectionProp name="extraConfigs">`+
			`<elementProp name="mech"><stringProp name="configKey">sasl.mechanism</stringProp><stringProp name="configValue">%s</stringProp></elementProp>`+
			`<elementProp name="jaas"><stringProp name="configKey">sasl.jaas.config</stringProp><stringProp name="configValue">%s</stringProp></elementProp>`+
			`%s</collectionProp></%s><hashTree/>`+
			`<%s testname="publish"><stringProp name="kafkaProducerClientVariableName">prod</stringProp><stringProp name="kafkaTopic">orders</stringProp><stringProp name="kafkaMessage">{"ok":true}</stringProp></%s><hashTree/>`+
			`</hashTree></hashTree></hashTree></jmeterTestPlan>`,
			diKafkaConfig, brokers, securityType, mechanism, jaas, extraRows, diKafkaConfig, diKafkaSampler, diKafkaSampler))
	}

	t.Run("eventhubs_sasl_plain", func(t *testing.T) {
		connStr := "Endpoint=sb://synthetic-ns.servicebus.windows.net/;SharedAccessKeyName=SendRule;SharedAccessKey=abc123XYZ+789/0="
		jaas := `org.apache.kafka.common.security.plain.PlainLoginModule required username="$ConnectionString" password="` + connStr + `";`
		res, err := ConvertJMX(makePlan("synthetic-ns.servicebus.windows.net:9093", "securityType.sasl_ssl", "PLAIN", jaas, ""), ConvertOptions{})
		if err != nil {
			t.Fatal(err)
		}
		s := res.Scripts[0]
		if s.Config.Kafka.Profile != "eventhubs" || s.Config.Kafka.SASLMechanism != "plain" || s.Config.Kafka.SASLUsername != "$ConnectionString" || s.Config.Kafka.SASLPassword != connStr {
			t.Fatalf("unexpected Kafka SASL/PLAIN config: %+v", s.Config.Kafka)
		}
		if s.Config.Kafka.OAuth != nil {
			t.Fatalf("unexpected OAuth config on SASL/PLAIN: %+v", s.Config.Kafka.OAuth)
		}
		if hasJMeterReviewReason(s.Actions, "jmeter_kafka_jaas_unsupported_or_malformed") || hasJMeterReviewReason(s.Actions, "jmeter_kafka_oauth_credentials_required") {
			t.Fatalf("unexpected JAAS/OAuth blocker on SASL/PLAIN: %#v", s.Actions[0].Metadata)
		}
		if issues := dsl.ValidateScriptIssues(s); len(issues) > 0 {
			t.Fatalf("ValidateScriptIssues failed: %v", issues)
		}
	})

	t.Run("kafka_scram_sha_256", func(t *testing.T) {
		jaas := `org.apache.kafka.common.security.scram.ScramLoginModule required username='scram-user' password='scram-pass-value';`
		res, err := ConvertJMX(makePlan("broker.example.test:9093", "securityType.sasl_ssl", "SCRAM-SHA-256", jaas, ""), ConvertOptions{})
		if err != nil {
			t.Fatal(err)
		}
		s := res.Scripts[0]
		if s.Config.Kafka.Profile != "" || s.Config.Kafka.SASLMechanism != "scram-sha-256" || s.Config.Kafka.SASLUsername != "scram-user" || s.Config.Kafka.SASLPassword != "scram-pass-value" {
			t.Fatalf("unexpected Kafka SCRAM config: %+v", s.Config.Kafka)
		}
		if hasJMeterReviewReason(s.Actions, "jmeter_kafka_jaas_unsupported_or_malformed") || hasJMeterReviewReason(s.Actions, "jmeter_kafka_oauth_credentials_required") {
			t.Fatalf("unexpected JAAS/OAuth blocker on SCRAM: %#v", s.Actions[0].Metadata)
		}
		if issues := dsl.ValidateScriptIssues(s); len(issues) > 0 {
			t.Fatalf("ValidateScriptIssues failed: %v", issues)
		}
	})

	t.Run("oauthbearer_literal_tilde_secret", func(t *testing.T) {
		tildeSecret := "synth8Q~0123456789abcdefABCDEF_tilde-sec"
		jaas := `org.apache.kafka.common.security.oauthbearer.OAuthBearerLoginModule required clientId="00000000-0000-0000-0000-000000000099" clientSecret="` + tildeSecret + `" scope="api://synthetic/.default";`
		extra := `<elementProp name="ep"><stringProp name="configKey">sasl.oauthbearer.token.endpoint.url</stringProp><stringProp name="configValue">https://login.microsoftonline.com/00000000-0000-0000-0000-000000000001/oauth2/v2.0/token</stringProp></elementProp>` +
			`<elementProp name="cb"><stringProp name="configKey">sasl.login.callback.handler.class</stringProp><stringProp name="configValue">org.apache.kafka.common.security.oauthbearer.secured.OAuthBearerLoginCallbackHandler</stringProp></elementProp>`
		res, err := ConvertJMX(makePlan("broker.example.test:9093", "securityType.sasl_ssl", "OAUTHBEARER", jaas, extra), ConvertOptions{})
		if err != nil {
			t.Fatal(err)
		}
		s := res.Scripts[0]
		if s.Config.Kafka.OAuth == nil || s.Config.Kafka.OAuth.ClientSecret != tildeSecret {
			t.Fatalf("expected literal tilde secret in ClientSecret, got %+v", s.Config.Kafka.OAuth)
		}
		if len(res.SecretRequirements) != 0 {
			t.Fatalf("expected zero SecretRequirements for literal OAuth secret, got %+v", res.SecretRequirements)
		}
		if hasJMeterReviewReason(s.Actions, "jmeter_kafka_jaas_unsupported_or_malformed") || hasJMeterReviewReason(s.Actions, "jmeter_kafka_oauth_credentials_required") {
			t.Fatalf("unexpected JAAS/OAuth blocker on literal OAuth: %#v", s.Actions[0].Metadata)
		}
		if issues := dsl.ValidateScriptIssues(s); len(issues) > 0 {
			t.Fatalf("ValidateScriptIssues failed: %v", issues)
		}
	})

	t.Run("scram_tokenauth_true_is_gated", func(t *testing.T) {
		jaas := `org.apache.kafka.common.security.scram.ScramLoginModule required username="token-id" password="token-hmac" tokenauth=true;`
		res, err := ConvertJMX(makePlan("broker.example.test:9093", "securityType.sasl_ssl", "SCRAM-SHA-256", jaas, ""), ConvertOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if !hasJMeterReviewReason(res.Scripts[0].Actions, "jmeter_kafka_jaas_unsupported_or_malformed") {
			t.Fatalf("expected tokenauth=true SCRAM to be gated with jmeter_kafka_jaas_unsupported_or_malformed, got %#v", res.Scripts[0].Actions[0].Metadata)
		}
	})

	t.Run("unresolved_jaas_variables_are_gated", func(t *testing.T) {
		jaas := `org.apache.kafka.common.security.scram.ScramLoginModule required username="${KAFKA_USER}" password="${KAFKA_PASS}" tokenauth=false;`
		res, err := ConvertJMX(makePlan("broker.example.test:9093", "securityType.sasl_ssl", "SCRAM-SHA-256", jaas, ""), ConvertOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if !hasJMeterReviewReason(res.Scripts[0].Actions, "jmeter_messaging_variable_unresolved") {
			t.Fatalf("expected unresolved JAAS variable to be gated with jmeter_messaging_variable_unresolved, got %#v", res.Scripts[0].Actions[0].Metadata)
		}
		if issues := dsl.ValidateScriptIssues(res.Scripts[0]); len(issues) > 0 {
			t.Fatalf("blocked draft must remain savable: %v", issues)
		}
	})

	t.Run("eventhubs_semicolon_injection_and_unresolved_variables_and_sb_prefix", func(t *testing.T) {
		makeEHPlan := func(ns, keyName, key string) []byte {
			return []byte(fmt.Sprintf(`<jmeterTestPlan><hashTree><TestPlan/><hashTree><ThreadGroup testname="eh"/><hashTree>`+
				`<%s testname="send"><stringProp name="namespaceName">%s</stringProp><stringProp name="eventHubName">orders</stringProp>`+
				`<stringProp name="authType">Shared access signature</stringProp><stringProp name="sharedAccessKeyName">%s</stringProp><stringProp name="sharedAccessKey">%s</stringProp>`+
				`<collectionProp name="Messages.messages"><elementProp name="m" elementType="jp.co.pnop.jmeter.protocol.amqp.sampler.AzAmqpMessage"><stringProp name="Message.messageType">String</stringProp><stringProp name="Message.message">{"ok":true}</stringProp></elementProp></collectionProp>`+
				`</%s><hashTree/></hashTree></hashTree></hashTree></jmeterTestPlan>`,
				pnopEventHubsSampler, ns, keyName, key, pnopEventHubsSampler))
		}

		// sb:// prefix and trailing slash normalize cleanly when valid.
		cleanRes, err := ConvertJMX(makeEHPlan("sb://synthetic-ns.servicebus.windows.net/", "SendRule", "abc123XYZ+789/0="), ConvertOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if cleanRes.Scripts[0].Config.EventHubs.ConnectionString != "Endpoint=sb://synthetic-ns.servicebus.windows.net/;SharedAccessKeyName=SendRule;SharedAccessKey=abc123XYZ+789/0=" {
			t.Fatalf("unexpected normalized EventHubs connection string: %q", cleanRes.Scripts[0].Config.EventHubs.ConnectionString)
		}
		if hasJMeterReviewReason(cleanRes.Scripts[0].Actions, "jmeter_eventhubs_credentials_required") {
			t.Fatalf("unexpected credential gate on valid sb:// namespace: %#v", cleanRes.Scripts[0].Actions[0].Metadata)
		}

		// Semicolons in keyName or key must not inject connection-string segments.
		injRes, err := ConvertJMX(makeEHPlan("synthetic-ns.servicebus.windows.net", "SendRule;EntityPath=orders", "abc;foo=bar"), ConvertOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if !hasJMeterReviewReason(injRes.Scripts[0].Actions, "jmeter_eventhubs_credentials_required") ||
			!strings.HasPrefix(injRes.Scripts[0].Config.EventHubs.ConnectionString, "{{secret.") {
			t.Fatalf("semicolon-bearing EventHubs SAS fields bypassed credential gate: conn=%q meta=%#v",
				injRes.Scripts[0].Config.EventHubs.ConnectionString, injRes.Scripts[0].Actions[0].Metadata)
		}

		// Unresolved ${VAR} in sharedAccessKey is gated with jmeter_messaging_variable_unresolved.
		varRes, err := ConvertJMX(makeEHPlan("synthetic-ns.servicebus.windows.net", "SendRule", "${EH_SAS_KEY}"), ConvertOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if !hasJMeterReviewReason(varRes.Scripts[0].Actions, "jmeter_messaging_variable_unresolved") {
			t.Fatalf("expected jmeter_messaging_variable_unresolved for ${EH_SAS_KEY}, got %#v", varRes.Scripts[0].Actions[0].Metadata)
		}
		if issues := dsl.ValidateScriptIssues(varRes.Scripts[0]); len(issues) > 0 {
			t.Fatalf("blocked EventHubs draft must remain savable: %v", issues)
		}
	})

	t.Run("malformed_eventhubs_plain_and_non_ssl_oauth_and_templated_enums_stay_savable", func(t *testing.T) {
		emptyPassJAAS := `org.apache.kafka.common.security.plain.PlainLoginModule required username="$ConnectionString" password="";`
		emptyPassRes, err := ConvertJMX(makePlan("synthetic-ns.servicebus.windows.net:9093", "securityType.sasl_ssl", "PLAIN", emptyPassJAAS, ""), ConvertOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if emptyPassRes.Scripts[0].Config.Kafka.Profile != "" {
			t.Fatalf("empty-password Event Hubs PLAIN should not set profile=eventhubs: %+v", emptyPassRes.Scripts[0].Config.Kafka)
		}
		if !hasJMeterReviewReason(emptyPassRes.Scripts[0].Actions, "jmeter_kafka_jaas_unsupported_or_malformed") {
			t.Fatalf("expected empty-password JAAS blocker, got %#v", emptyPassRes.Scripts[0].Actions[0].Metadata)
		}
		if issues := dsl.ValidateScriptIssues(emptyPassRes.Scripts[0]); len(issues) > 0 {
			t.Fatalf("empty-password Event Hubs PLAIN draft must remain savable: %v", issues)
		}

		nonSSLOAuthJAAS := `org.apache.kafka.common.security.oauthbearer.OAuthBearerLoginModule required clientId="cid-1" clientSecret="csec-1" scope="api://synthetic/.default";`
		nonSSLExtra := `<elementProp name="ep"><stringProp name="configKey">sasl.oauthbearer.token.endpoint.url</stringProp><stringProp name="configValue">https://login.microsoftonline.com/00000000-0000-0000-0000-000000000001/oauth2/v2.0/token</stringProp></elementProp>`
		nonSSLRes, err := ConvertJMX(makePlan("broker.example.test:9092", "securityType.sasl_plaintext", "OAUTHBEARER", nonSSLOAuthJAAS, nonSSLExtra), ConvertOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if nonSSLRes.Scripts[0].Config.Kafka.OAuth != nil {
			t.Fatalf("expected non-SSL OAuth native config to stay nil on blocked draft, got %+v", nonSSLRes.Scripts[0].Config.Kafka.OAuth)
		}
		if !hasJMeterReviewReason(nonSSLRes.Scripts[0].Actions, "jmeter_kafka_jaas_unsupported_or_malformed") {
			t.Fatalf("expected jmeter_kafka_jaas_unsupported_or_malformed on non-SSL OAuth, got %#v", nonSSLRes.Scripts[0].Actions[0].Metadata)
		}
		if issues := dsl.ValidateScriptIssues(nonSSLRes.Scripts[0]); len(issues) > 0 {
			t.Fatalf("non-SSL OAuth blocked draft must remain savable: %v", issues)
		}

		templatedEnumsExtra := `<elementProp name="acks"><stringProp name="configKey">acks</stringProp><stringProp name="configValue">${KAFKA_ACKS}</stringProp></elementProp>`
		templatedRes, err := ConvertJMX(makePlan("broker.example.test:9093", "${SEC_PROTO}", "${SASL_MECH}", emptyPassJAAS, templatedEnumsExtra), ConvertOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if !hasJMeterReviewReason(templatedRes.Scripts[0].Actions, "jmeter_messaging_variable_unresolved") {
			t.Fatalf("expected jmeter_messaging_variable_unresolved for templated enums, got %#v", templatedRes.Scripts[0].Actions[0].Metadata)
		}
		if issues := dsl.ValidateScriptIssues(templatedRes.Scripts[0]); len(issues) > 0 {
			t.Fatalf("templated-enum blocked draft must remain savable: %v", issues)
		}

		badTokenURLRes, err := ConvertJMX(makePlan("broker.example.test:9093", "securityType.sasl_ssl", "OAUTHBEARER", nonSSLOAuthJAAS, `<elementProp name="ep"><stringProp name="configKey">sasl.oauthbearer.token.endpoint.url</stringProp><stringProp name="configValue">http://idp.example.test/token</stringProp></elementProp>`), ConvertOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if !hasJMeterReviewReason(badTokenURLRes.Scripts[0].Actions, "jmeter_kafka_oauth_endpoint_invalid") {
			t.Fatalf("expected jmeter_kafka_oauth_endpoint_invalid, got %#v", badTokenURLRes.Scripts[0].Actions[0].Metadata)
		}
		if issues := dsl.ValidateScriptIssues(badTokenURLRes.Scripts[0]); len(issues) > 0 {
			t.Fatalf("invalid-token-url OAuth over sasl_ssl blocked draft must remain savable: %v", issues)
		}

		for _, badEscapeJAAS := range []string{
			`org.apache.kafka.common.security.plain.PlainLoginModule required username="svc" password="line1\nline2";`,
			`org.apache.kafka.common.security.scram.ScramLoginModule required username='svc\tuser' password='secret';`,
		} {
			badEscRes, err := ConvertJMX(makePlan("broker.example.test:9093", "securityType.sasl_ssl", "PLAIN", badEscapeJAAS, ""), ConvertOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if !hasJMeterReviewReason(badEscRes.Scripts[0].Actions, "jmeter_kafka_jaas_unsupported_or_malformed") {
				t.Fatalf("expected unsupported escape JAAS %q to fail closed, got %#v", badEscapeJAAS, badEscRes.Scripts[0].Actions[0].Metadata)
			}
			if issues := dsl.ValidateScriptIssues(badEscRes.Scripts[0]); len(issues) > 0 {
				t.Fatalf("unsupported escape JAAS draft must remain savable: %v", issues)
			}
		}

		escapedQuoteJAAS := `org.apache.kafka.common.security.plain.PlainLoginModule required username="svc" password="p\\\"q";`
		escapedQuoteRes, err := ConvertJMX(makePlan("broker.example.test:9093", "securityType.sasl_ssl", "PLAIN", escapedQuoteJAAS, ""), ConvertOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if got := escapedQuoteRes.Scripts[0].Config.Kafka.SASLPassword; got != `p\"q` {
			t.Fatalf("SASLPassword single-pass unescape = %q, want %q", got, `p\"q`)
		}
	})
}

package jmeter

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

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
	for _, row := range []string{"NaN", "0", "-1", "+Inf", "10000001"} {
		n := &xmlNode{Name: throughputShapingTimer, Children: []*xmlNode{{Attrs: map[string]string{"name": "load_profile"}, Children: []*xmlNode{{Children: []*xmlNode{{Text: row}, {Text: "42"}, {Text: "300"}}}}}}}
		if _, e := messagingSchedule(n, "plan"); e == nil {
			t.Fatalf("accepted %s", row)
		}
	}
}

func TestMessagingSourceRedactsCredentialDuplicatedInXMLAttribute(t *testing.T) {
	source := string(messagingFixture(t, "original"))
	source = strings.Replace(source, `name="fixture message"`, `name="echo-credential-canary"`, 1)
	source = strings.Replace(source, `<stringProp name="sharedAccessKey">***</stringProp>`, `<stringProp name="sharedAccessKey">echo-credential-canary</stringProp>`, 1)
	r, e := ConvertJMX([]byte(source), ConvertOptions{})
	if e != nil {
		t.Fatal(e)
	}
	public, _ := json.Marshal(r)
	if strings.Contains(string(public), "echo-credential-canary") {
		t.Fatal("credential leaked into public draft or nested source attribute")
	}
	if !r.SourceReview.SourceFiles[0].Redacted {
		t.Fatal("source redaction not disclosed")
	}
	source = strings.Replace(source, `name="echo-credential-canary"`, `name="echo&#45;credential&#45;canary"`, 1)
	r, e = ConvertJMX([]byte(source), ConvertOptions{})
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(r.SourceReview.SourceFiles[0].Content, "SOURCE OMITTED") {
		t.Fatal("entity-obscured duplicate not caught")
	}
}

func TestMessagingSourceRedactsUnsupportedExtraPropertyCredentials(t *testing.T) {
	for _, key := range []string{"ssl.key.password", "sasl.password", "clientSecret"} {
		source := strings.Replace(string(messagingFixture(t, "original")), `<collectionProp name="extraConfigs">`, `<collectionProp name="extraConfigs"><elementProp name="secret-row"><stringProp name="configKey">`+key+`</stringProp><stringProp name="configValue">unsupported-secret-canary</stringProp></elementProp>`, 1)
		r, err := ConvertJMX([]byte(source), ConvertOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(r.SourceReview.SourceFiles[0].Content, "unsupported-secret-canary") {
			t.Fatalf("%s leaked through source review", key)
		}
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

func TestMessagingUnparseableJAASCannotExposeDecodedSecretCopies(t *testing.T) {
	data := strings.Replace(string(messagingFixture(t, "original")), `cid="***" cs="***"`, `cid="id" cs="line\nsecret"`, 1)
	// Verify the test reached the source spelling it intends to exercise.
	if !strings.Contains(data, `line\nsecret`) {
		t.Fatal("JAAS fixture replacement did not apply")
	}
	r, err := ConvertJMX([]byte(data), ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.SourceReview.SourceFiles[0].Content, "SOURCE OMITTED") {
		t.Fatal("unparseable JAAS source was disclosed")
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
	first := strings.Replace(config("one", "first.test"), "</"+diKafkaConfig+">", `<collectionProp name="extraConfigs"><elementProp name="tls"><stringProp name="configKey">ssl.enabled.protocols</stringProp><stringProp name="configValue">TLSv1.2</stringProp></elementProp><elementProp name="oauth"><stringProp name="configKey">sasl.jaas.config</stringProp><stringProp name="configValue">org.apache.kafka.common.security.oauthbearer.OAuthBearerLoginModule required clientId="synthetic" clientSecret="***";</stringProp></elementProp></collectionProp></`+diKafkaConfig+">", 1)
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

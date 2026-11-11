package jmeter

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func convertMessagingVariant(t *testing.T, edit func(string) string) *ConvertResult {
	t.Helper()
	source := string(messagingFixture(t, "original"))
	if edit != nil {
		source = edit(source)
	}
	result, err := ConvertJMX([]byte(source), ConvertOptions{FileName: "messaging.jmx", ScenarioDraft: true})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// replaceKafkaSetting rewrites one extraConfigs value of the enabled Kafka
// Producer Config (the second one in the fixture; the first is disabled).
func replaceKafkaSetting(source, key, from, to string) string {
	enabled := strings.Index(source, `testname="Kafka Producer Config" enabled="true"`)
	old := `<stringProp name="configKey">` + key + `</stringProp>
              <stringProp name="configValue">` + from + `</stringProp>`
	index := strings.Index(source[enabled:], old)
	if enabled < 0 || index < 0 {
		panic("fixture setting not found: " + key)
	}
	at := enabled + index
	return source[:at] + strings.Replace(old, ">"+from+"<", ">"+to+"<", 1) + source[at+len(old):]
}

func kafkaReviewCodes(script *dsl.Script) string {
	raw, _ := json.Marshal(script.Actions[0].Metadata["import_review"])
	return string(raw)
}

func diagnosticDetails(result *ConvertResult, severity diag.Severity) []string {
	var out []string
	for _, item := range result.Unsupported {
		if item.Severity == severity {
			out = append(out, item.Detail)
		}
	}
	return out
}

func TestMessagingKafkaEmitsJavaProducerDefaults(t *testing.T) {
	result := convertMessagingVariant(t, nil)
	kafka := result.Scripts[0].Config.Kafka
	if kafka.Producer == nil || kafka.Producer.MaxInFlightRequests == nil || *kafka.Producer.MaxInFlightRequests != 5 {
		t.Fatalf("non-idempotent producer must carry Java's default 5 in flight: %+v", kafka.Producer)
	}
	if kafka.Producer.BatchBytes != nil {
		t.Fatal("batch.size must not become franz's hard batch ceiling")
	}
	if kafka.Compression != "none" {
		t.Fatalf("compression = %q, want Java's default none", kafka.Compression)
	}
	settings := result.Scripts[0].Actions[0].Metadata["jmeter_kafka"].(map[string]interface{})["effective_settings"].(map[string]string)
	for key, want := range map[string]string{"compression.type": "none", "max.in.flight.requests.per.connection": "5", "batch.size": "16384"} {
		if settings[key] != want {
			t.Fatalf("effective %s = %q, want %q", key, settings[key], want)
		}
	}
	if !strings.Contains(kafkaReviewCodes(result.Scripts[0]), "jmeter_kafka_batch_allocation_parity_unverified") {
		t.Fatal("batching parity gate must stay")
	}
	notes := strings.Join(diagnosticDetails(result, diag.Info), "\n")
	if !strings.Contains(notes, "batch.size 16384 is kept in the effective settings but not applied") {
		t.Fatal("batch.size disclosure missing")
	}
	if !strings.Contains(notes, "client.id is empty, so the Java client names each producer producer-1") {
		t.Fatal("client.id disclosure missing")
	}
}

func TestMessagingKafkaIdempotentProducerKeepsClientPipelining(t *testing.T) {
	result := convertMessagingVariant(t, func(source string) string {
		return replaceKafkaSetting(source, "enable.idempotence", "false", "true")
	})
	producer := result.Scripts[0].Config.Kafka.Producer
	if producer == nil || producer.MaxInFlightRequests != nil {
		t.Fatalf("idempotent producer must not emit max_in_flight_requests: %+v", producer)
	}
	if strings.Contains(kafkaReviewCodes(result.Scripts[0]), "jmeter_kafka_producer_setting_invalid") {
		t.Fatal("Java's default in-flight value is valid for an idempotent producer")
	}

	tooMany := convertMessagingVariant(t, func(source string) string {
		source = replaceKafkaSetting(source, "enable.idempotence", "false", "true")
		return replaceKafkaSetting(source, "linger.ms", "1", "1</stringProp>\n            </elementProp>\n            <elementProp name=\"\" elementType=\"com.di.jmeter.kafka.utils.VariableSettings\">\n              <stringProp name=\"configKey\">max.in.flight.requests.per.connection</stringProp>\n              <stringProp name=\"configValue\">6")
	})
	if !strings.Contains(kafkaReviewCodes(tooMany.Scripts[0]), "jmeter_kafka_producer_setting_invalid") {
		t.Fatal("an idempotent producer with more than 5 in flight must be gated")
	}
}

// replaceKafkaSettingRow swaps one extraConfigs row (key and value) of the
// enabled Kafka Producer Config for another.
func replaceKafkaSettingRow(source, key, value, newKey, newValue string) string {
	enabled := strings.Index(source, `testname="Kafka Producer Config" enabled="true"`)
	old := `<stringProp name="configKey">` + key + `</stringProp>
              <stringProp name="configValue">` + value + `</stringProp>`
	index := strings.Index(source[enabled:], old)
	if enabled < 0 || index < 0 {
		panic("fixture setting not found: " + key)
	}
	at := enabled + index
	return source[:at] + `<stringProp name="configKey">` + newKey + `</stringProp>
              <stringProp name="configValue">` + newValue + `</stringProp>` + source[at+len(old):]
}

func withKafkaInFlight(source, value string) string {
	return replaceKafkaSetting(source, "linger.ms", "1", "1</stringProp>\n            </elementProp>\n            <elementProp name=\"\" elementType=\"com.di.jmeter.kafka.utils.VariableSettings\">\n              <stringProp name=\"configKey\">max.in.flight.requests.per.connection</stringProp>\n              <stringProp name=\"configValue\">"+value)
}

func TestMessagingKafkaImplicitIdempotenceTurnsOffLikeJava(t *testing.T) {
	// enable.idempotence unset (its row becomes a duplicate acks=all), acks=all.
	unset := func(source string) string {
		return replaceKafkaSettingRow(source, "enable.idempotence", "false", "acks", "all")
	}
	for _, tc := range []struct {
		name     string
		edit     func(string) string
		inFlight int
	}{
		{"in_flight_above_five", func(source string) string { return withKafkaInFlight(unset(source), "10") }, 10},
		{"retries_zero", func(source string) string { return replaceKafkaSetting(unset(source), "retries", "1", "0") }, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := convertMessagingVariant(t, tc.edit)
			kafka := result.Scripts[0].Config.Kafka
			codes := kafkaReviewCodes(result.Scripts[0])
			if kafka.Idempotence != "disabled" {
				t.Fatalf("Java turns default idempotence off for this plan; got idempotence=%q", kafka.Idempotence)
			}
			if strings.Contains(codes, "jmeter_kafka_producer_setting_invalid") || !strings.Contains(codes, "jmeter_kafka_batch_allocation_parity_unverified") {
				t.Fatalf("a valid Java plan must keep its producer tuning gate, not setting_invalid: %s", codes)
			}
			if kafka.Producer == nil || kafka.Producer.MaxInFlightRequests == nil || *kafka.Producer.MaxInFlightRequests != tc.inFlight || kafka.Producer.Retries == nil {
				t.Fatalf("producer tuning must be kept with %d in flight: %+v", tc.inFlight, kafka.Producer)
			}
			if !strings.Contains(strings.Join(diagnosticDetails(result, diag.Info), "\n"), "leaves enable.idempotence unset") {
				t.Fatal("the implicit idempotence change must be disclosed")
			}
		})
	}

	// An explicit enable.idempotence=true with more than five is still invalid
	// (covered above); acks=all with the default in-flight stays idempotent.
	plain := convertMessagingVariant(t, unset)
	if plain.Scripts[0].Config.Kafka.Idempotence != "" {
		t.Fatalf("no conflicting setting: idempotence must stay at the default, got %q", plain.Scripts[0].Config.Kafka.Idempotence)
	}
}

func TestMessagingKafkaIdempotentInFlightBelowFiveIsDisclosed(t *testing.T) {
	result := convertMessagingVariant(t, func(source string) string {
		return withKafkaInFlight(replaceKafkaSetting(source, "enable.idempotence", "false", "true"), "1")
	})
	if producer := result.Scripts[0].Config.Kafka.Producer; producer == nil || producer.MaxInFlightRequests != nil {
		t.Fatalf("idempotent producer must not emit max_in_flight_requests: %+v", producer)
	}
	if !strings.Contains(strings.Join(diagnosticDetails(result, diag.Info), "\n"), "max.in.flight.requests.per.connection 1 is kept in the effective settings but not applied") {
		t.Fatal("an idempotent in-flight value below five must be disclosed as not applied")
	}
}

func TestMessagingKafkaCompressionAndSmallBatchSize(t *testing.T) {
	result := convertMessagingVariant(t, func(source string) string {
		// 0 disables batching in Java; franz's 512-byte floor must not reject it.
		source = replaceKafkaSetting(source, "batch.size", "16384", "0")
		return replaceKafkaSetting(source, "linger.ms", "1", "1</stringProp>\n            </elementProp>\n            <elementProp name=\"\" elementType=\"com.di.jmeter.kafka.utils.VariableSettings\">\n              <stringProp name=\"configKey\">compression.type</stringProp>\n              <stringProp name=\"configValue\">gzip")
	})
	kafka := result.Scripts[0].Config.Kafka
	if kafka.Compression != "gzip" {
		t.Fatalf("compression = %q, want gzip", kafka.Compression)
	}
	codes := kafkaReviewCodes(result.Scripts[0])
	if strings.Contains(codes, "jmeter_kafka_producer_setting_invalid") || strings.Contains(codes, "jmeter_kafka_effective_property_unsupported") {
		t.Fatalf("valid Java settings were gated: %s", codes)
	}
	if kafka.Producer == nil || kafka.Producer.BatchBytes != nil {
		t.Fatalf("producer tuning lost or batch.size applied: %+v", kafka.Producer)
	}
}

func TestMessagingEventHubsPreservesLiteralConnectionStringAndRequiresSecretOnlyWhenMissing(t *testing.T) {
	preserved := convertMessagingVariant(t, func(source string) string {
		return strings.Replace(source, `<stringProp name="sharedAccessKey">***</stringProp>`, `<stringProp name="sharedAccessKey">eh-key-canary</stringProp>`, 1)
	})
	if got := preserved.Scripts[1].Config.EventHubs.ConnectionString; got != "Endpoint=sb://fixture-east.servicebus.windows.net/;SharedAccessKeyName=write;SharedAccessKey=eh-key-canary" {
		t.Fatalf("east connection string = %q, want literal SAS connection string", got)
	}
	if got := preserved.Scripts[2].Config.EventHubs.ConnectionString; got != "Endpoint=sb://fixture-west.servicebus.windows.net/;SharedAccessKeyName=write;SharedAccessKey=***" {
		t.Fatalf("west connection string = %q, want literal SAS connection string", got)
	}
	if hasJMeterReviewReason(preserved.Scripts[1].Actions, "jmeter_eventhubs_credentials_required") {
		t.Fatal("literal Event Hubs sharedAccessKey should not emit jmeter_eventhubs_credentials_required")
	}

	missing := convertMessagingVariant(t, func(source string) string {
		return strings.ReplaceAll(source, `<stringProp name="sharedAccessKey">***</stringProp>`, `<stringProp name="sharedAccessKey"></stringProp>`)
	})
	found := 0
	for _, requirement := range missing.SecretRequirements {
		if requirement.Key != "eventhubs_connection_string" {
			continue
		}
		found++
		for _, want := range []string{"Endpoint=sb://<namespace>.servicebus.windows.net/;SharedAccessKeyName=<policy>;SharedAccessKey=<key>", ";EntityPath=<event hub>", `"fixture-hub"`, "trimmed"} {
			if !strings.Contains(requirement.Context, want) {
				t.Fatalf("context %q lacks %q", requirement.Context, want)
			}
		}
		if requirement.Value != "" {
			t.Fatal("missing-source requirement unexpectedly carries a value")
		}
	}
	if found != 2 {
		t.Fatalf("Event Hubs missing-key requirements = %d, want 2", found)
	}
	if !hasJMeterReviewReason(missing.Scripts[1].Actions, "jmeter_eventhubs_credentials_required") {
		t.Fatal("missing Event Hubs sharedAccessKey must emit jmeter_eventhubs_credentials_required")
	}
}

func TestMessagingKafkaTokenURLIsNotACredentialRequirement(t *testing.T) {
	result := convertMessagingVariant(t, nil)
	for _, requirement := range result.SecretRequirements {
		for _, target := range requirement.Targets {
			if strings.Contains(target.Field, "token_url") {
				t.Fatalf("token endpoint emitted as a secret requirement: %+v", requirement)
			}
		}
	}
	if result.Scripts[0].Config.Kafka.OAuth == nil || !strings.HasPrefix(result.Scripts[0].Config.Kafka.OAuth.TokenURL, "https://") {
		t.Fatal("token endpoint must stay plain configuration")
	}
}

func TestMessagingDisabledPlainJAASPreservesUnredactedSourceReview(t *testing.T) {
	result := convertMessagingVariant(t, func(source string) string {
		return strings.Replace(source, `username='fixture' password=***;;`, `username='jaas-user-canary' password="jaas-password-canary";`, 1)
	})
	content := result.SourceReview.SourceFiles[0].Content
	if strings.Contains(content, "JMETER SOURCE OMITTED") {
		t.Fatal("source review was omitted")
	}
	if !strings.Contains(content, `testname="Event Hubs east"`) || !strings.Contains(content, "jaas-password-canary") {
		t.Fatal("source review did not preserve raw JMX verbatim")
	}
	if result.SourceReview.SourceFiles[0].Redacted {
		t.Fatal("source review marked redacted despite preserving raw JMX")
	}

	enabled := convertMessagingVariant(t, func(source string) string {
		return strings.Replace(source, `OAuthBearerLoginModule required cid="***" cs="***" scope="00000000-0000-0000-0000-000000000001/.default";`, `org.apache.kafka.common.security.plain.PlainLoginModule required username='u' password="enabled-canary";`, 1)
	})
	if !strings.Contains(enabled.SourceReview.SourceFiles[0].Content, "enabled-canary") || enabled.SourceReview.SourceFiles[0].Redacted {
		t.Fatal("enabled unsupported JAAS must still preserve raw source in SourceReview")
	}
}

func TestMessagingScriptNamesCommentsAndScenarioName(t *testing.T) {
	result := convertMessagingVariant(t, nil)
	want := []string{"Kafka_payments", "Event_Hubs_east", "Event_Hubs_west"}
	for i, script := range result.Scripts {
		if script.Name != want[i] {
			t.Fatalf("script %d name = %q, want %q (no positional suffix without a collision)", i, script.Name, want[i])
		}
	}
	if result.Scripts[0].Metadata.Import.Preserved["source_comments"] != "100k" || result.Scripts[1].Metadata.Import.Preserved["source_comments"] != "Billing System = TLG" {
		t.Fatal("thread group comments were dropped")
	}
	if result.ScenarioDraft.Name != "Azure Event Hubs Test Plan" {
		t.Fatalf("scenario name = %q, want the Test Plan name", result.ScenarioDraft.Name)
	}
	names := func(r *ConvertResult) []string {
		var out []string
		for _, requirement := range r.SecretRequirements {
			if requirement.Key == "eventhubs_connection_string" {
				out = append(out, requirement.SuggestedName)
			}
		}
		return out
	}
	// Disabling the Kafka group must not rename the Event Hubs secrets.
	withoutKafka := convertMessagingVariant(t, func(source string) string {
		return strings.Replace(source, `testname="Kafka payments" enabled="true"`, `testname="Kafka payments" enabled="false"`, 1)
	})
	if strings.Join(names(result), ",") != strings.Join(names(withoutKafka), ",") {
		t.Fatalf("secret names depend on group order: %v vs %v", names(result), names(withoutKafka))
	}
}

func TestJMeterScriptNamesSuffixOnlyCollisions(t *testing.T) {
	group := func(name string) *treeNode {
		return &treeNode{Element: &xmlNode{Name: "ThreadGroup", Attrs: map[string]string{"testname": name}}}
	}
	got := jmeterScriptNames([]*treeNode{group("Orders"), group("Login"), group("orders"), group("Orders_1")}, "")
	if strings.Join(got, ",") != "Orders_2,Login,orders_3,Orders_1" {
		t.Fatalf("names = %v", got)
	}
	if got := jmeterScriptNames([]*treeNode{group("A"), group("B")}, "Named"); strings.Join(got, ",") != "Named_1,Named_2" {
		t.Fatalf("requested name across groups = %v", got)
	}
	if got := jmeterScriptNames([]*treeNode{group("A")}, "Named"); got[0] != "Named" {
		t.Fatalf("single requested name = %v", got)
	}
}

func TestJMeterGenericPlanNameKeepsDefaultScenarioName(t *testing.T) {
	input := `<jmeterTestPlan><hashTree><TestPlan testname="Test Plan"><stringProp name="TestPlan.comments">nightly smoke</stringProp></TestPlan><hashTree><ThreadGroup testname="HTTP"/><hashTree><HTTPSamplerProxy testname="get"><stringProp name="HTTPSampler.domain">example.test</stringProp><stringProp name="HTTPSampler.path">/</stringProp></HTTPSamplerProxy><hashTree/></hashTree></hashTree></hashTree></jmeterTestPlan>`
	result, err := ConvertJMX([]byte(input), ConvertOptions{ScenarioDraft: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.ScenarioDraft.Name != "Imported JMeter Scenario" {
		t.Fatalf("generic plan name used for the scenario: %q", result.ScenarioDraft.Name)
	}
	if result.ScenarioDraft.Metadata["source_comments"] != "nightly smoke" {
		t.Fatalf("test plan comment dropped: %+v", result.ScenarioDraft.Metadata)
	}
	if _, inherited := result.Scripts[0].Metadata.Import.Preserved["source_comments"]; inherited {
		t.Fatal("the plan's comment was attributed to its thread group")
	}
}

func TestMessagingScriptWarningsStayWithTheirScript(t *testing.T) {
	result := convertMessagingVariant(t, nil)
	if len(result.ScriptWarnings) != len(result.Scripts) {
		t.Fatalf("script warnings for %d of %d scripts", len(result.ScriptWarnings), len(result.Scripts))
	}
	unusedCounter := false
	for _, script := range result.Scripts {
		declared := map[string]bool{}
		for _, parameter := range script.Parameters {
			declared[parameter.Name] = true
		}
		warnings := result.ScriptWarnings[script]
		schedule := false
		for _, warning := range warnings {
			if name, ok := strings.CutPrefix(warning, "Counter "); ok {
				name, _, _ = strings.Cut(name, " ")
				if !declared[name] {
					t.Fatalf("%s lists %q from another thread group", script.Name, warning)
				}
				unusedCounter = true
			}
			schedule = schedule || warning == jmeterSamplerScheduleUnavailable
		}
		if !schedule {
			t.Fatalf("%s lacks the sampler-schedule statement", script.Name)
		}
		if script.Protocol == "eventhubs" && !strings.Contains(strings.Join(warnings, "\n"), "PNOP Event Hubs does not consume HTTP headers") {
			t.Fatalf("%s lost its own de-duplicated header warning", script.Name)
		}
	}
	if !unusedCounter {
		t.Fatal("fixture should report an unused counter on the Kafka script")
	}
	count := 0
	for _, warning := range result.Warnings {
		if warning == jmeterSamplerScheduleUnavailable {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("sampler-schedule statement appears %d times, want once", count)
	}
}

func TestMessagingProductGatesSayTheyCannotBeCleared(t *testing.T) {
	result := convertMessagingVariant(t, nil)
	for _, script := range result.Scripts {
		review := script.Actions[0].Metadata["import_review"].(map[string]any)
		if review["resolution"] != jmeterReviewResolutionAwaitingRuntime {
			t.Fatalf("%s resolution = %v", script.Name, review["resolution"])
		}
	}
	action := &dsl.Action{}
	applyJMeterSharedReviewGate(action, []string{"unsupported_jmeter_processor"}, "src")
	if action.Metadata["import_review"].(map[string]any)["resolution"] != "replace_behavior_and_remove_gate" {
		t.Fatal("clearable gates keep their resolution")
	}
}

func TestMessagingStatsSeparateReadyGatedAndCountVariables(t *testing.T) {
	result := convertMessagingVariant(t, nil)
	stats := result.Stats
	if stats.ReplayReadyWorkloadSamplers+stats.ReviewGatedWorkloadSamplers+stats.UnconvertedWorkloadSamplers != stats.EnabledWorkloadSamplers {
		t.Fatalf("disjoint sampler counts do not sum: %+v", stats)
	}
	if stats.ConvertedWorkloadSamplers != stats.ReplayReadyWorkloadSamplers+stats.ReviewGatedWorkloadSamplers || stats.BlockedWorkloadSamplers != stats.ReviewGatedWorkloadSamplers+stats.UnconvertedWorkloadSamplers {
		t.Fatalf("documented overlap does not hold: %+v", stats)
	}
	generated := 0
	for _, script := range result.Scripts {
		for _, parameter := range script.Parameters {
			switch parameter.Type {
			case "counter", "random_number", "date_time":
				generated++
			}
		}
	}
	if generated == 0 || stats.GeneratedParameters != generated || stats.Variables < generated {
		t.Fatalf("variables %d / generated %d, want at least %d", stats.Variables, stats.GeneratedParameters, generated)
	}
}

func TestSplitPayStatsDoNotCountExportAsMapped(t *testing.T) {
	result, err := ConvertJMX(splitPayFixture(t, "original"), ConvertOptions{FileName: "split.jmx"})
	if err != nil {
		t.Fatal(err)
	}
	exports := 0
	for _, mapping := range result.SourceReview.Mappings {
		if mapping.TargetKind == "export" && mapping.Status == "converted" {
			exports++
		}
	}
	if exports == 0 {
		t.Fatal("fixture should contain the JSR223 CSV export")
	}
	if result.Stats.Mapped != 0 {
		t.Fatalf("mapped = %d; the review-gated sampler's CSV export is a contributor, not mapped workload", result.Stats.Mapped)
	}
}

// A Test Plan-scope Counter is attached to every thread group's script but is
// one source variable, so the plan counts it once.
func TestAncestorCounterIsCountedOncePerPlan(t *testing.T) {
	group := func(name string) string {
		return `<ThreadGroup testname="` + name + `" enabled="true"/>
      <hashTree>
        <HTTPSamplerProxy testname="Get" enabled="true">
          <stringProp name="HTTPSampler.domain">api.example.test</stringProp>
          <stringProp name="HTTPSampler.path">/items/${ctr}</stringProp>
          <stringProp name="HTTPSampler.method">GET</stringProp>
        </HTTPSamplerProxy>
        <hashTree/>
      </hashTree>`
	}
	plan := `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2">
  <hashTree>
    <TestPlan testname="Plan" enabled="true"/>
    <hashTree>
      <CounterConfig testname="Counter" enabled="true">
        <stringProp name="CounterConfig.start">1</stringProp>
        <stringProp name="CounterConfig.end">100</stringProp>
        <stringProp name="CounterConfig.incr">1</stringProp>
        <stringProp name="CounterConfig.name">ctr</stringProp>
      </CounterConfig>
      <hashTree/>
      ` + group("A") + group("B") + group("C") + `
    </hashTree>
  </hashTree>
</jmeterTestPlan>`
	result, err := ConvertJMX([]byte(plan), ConvertOptions{FileName: "counter.jmx"})
	if err != nil {
		t.Fatal(err)
	}
	carriers := 0
	for _, script := range result.Scripts {
		for _, parameter := range script.Parameters {
			if parameter.Name == "ctr" && parameter.Type == "counter" {
				carriers++
			}
		}
	}
	if carriers != 3 {
		t.Fatalf("fixture: the counter is carried by %d of 3 scripts", carriers)
	}
	if result.Stats.GeneratedParameters != 1 || result.Stats.Variables != 1 {
		t.Fatalf("generated = %d variables = %d, want one counted variable", result.Stats.GeneratedParameters, result.Stats.Variables)
	}
}

// jmeterTwoGroupPlan wraps plan-scope elements and two thread groups, A and
// B, each with one GET that uses ${user}; groupExtra goes inside each group.
func jmeterTwoGroupPlan(planScope string, groupExtra func(name string) string) []byte {
	group := func(name string) string {
		return `<ThreadGroup testname="` + name + `" enabled="true"/>
      <hashTree>` + groupExtra(name) + `
        <HTTPSamplerProxy testname="Get ` + name + `" enabled="true">
          <stringProp name="HTTPSampler.domain">api.example.test</stringProp>
          <stringProp name="HTTPSampler.path">/items/${user}/${id}</stringProp>
          <stringProp name="HTTPSampler.method">GET</stringProp>
        </HTTPSamplerProxy>
        <hashTree/>
      </hashTree>`
	}
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2">
  <hashTree>
    <TestPlan testname="Plan" enabled="true"/>
    <hashTree>` + planScope + group("A") + group("B") + `
    </hashTree>
  </hashTree>
</jmeterTestPlan>`)
}

// A warning raised by a plan-scope config element belongs to every script the
// element covers, not only to the first thread group that applied it.
func TestAncestorConfigWarningReachesEveryCoveredScript(t *testing.T) {
	const shareWarning = "CSV shareMode has no equivalent and requires review"
	plan := jmeterTwoGroupPlan(`
      <CSVDataSet testname="Users" enabled="true">
        <stringProp name="filename">users.csv</stringProp>
        <stringProp name="variableNames">user</stringProp>
        <stringProp name="shareMode">shareMode.group</stringProp>
      </CSVDataSet>
      <hashTree/>`, func(string) string { return "" })
	result, err := ConvertJMX(plan, ConvertOptions{FileName: "share.jmx"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Scripts) != 2 {
		t.Fatalf("scripts = %d, want 2", len(result.Scripts))
	}
	for _, script := range result.Scripts {
		if len(script.DataSources) != 1 {
			t.Fatalf("fixture: %s carries %d data sources", script.Name, len(script.DataSources))
		}
		if !slices.Contains(result.ScriptWarnings[script], shareWarning) {
			t.Fatalf("%s lacks the plan-scope CSV shareMode warning: %q", script.Name, result.ScriptWarnings[script])
		}
	}
	if result.Stats.CSVSources != 1 {
		t.Fatalf("csv sources = %d, want the plan-scope source counted once", result.Stats.CSVSources)
	}
}

// Sibling thread groups' own Counters that share a name are two source
// variables, while one plan-scope Counter stays one (see above).
func TestSiblingSameNamedCountersCountSeparately(t *testing.T) {
	plan := jmeterTwoGroupPlan(`
      <Arguments testname="Vars" enabled="true">
        <collectionProp name="Arguments.arguments">
          <elementProp name="user" elementType="Argument">
            <stringProp name="Argument.name">user</stringProp>
            <stringProp name="Argument.value">u</stringProp>
          </elementProp>
        </collectionProp>
      </Arguments>
      <hashTree/>`, func(name string) string {
		start := "1"
		if name == "B" {
			start = "100"
		}
		return `
        <CounterConfig testname="Counter ` + name + `" enabled="true">
          <stringProp name="CounterConfig.start">` + start + `</stringProp>
          <stringProp name="CounterConfig.incr">1</stringProp>
          <stringProp name="CounterConfig.name">id</stringProp>
          <boolProp name="CounterConfig.per_user">true</boolProp>
        </CounterConfig>
        <hashTree/>`
	})
	result, err := ConvertJMX(plan, ConvertOptions{FileName: "counters.jmx"})
	if err != nil {
		t.Fatal(err)
	}
	carriers := 0
	for _, script := range result.Scripts {
		for _, parameter := range script.Parameters {
			if parameter.Name == "id" && parameter.Type == "counter" {
				carriers++
			}
		}
	}
	if carriers != 2 {
		t.Fatalf("fixture: the counter is carried by %d of 2 scripts", carriers)
	}
	if result.Stats.GeneratedParameters != 2 {
		t.Fatalf("generated = %d, want each thread group's own counter counted", result.Stats.GeneratedParameters)
	}
}

func TestMessagingEntityEncodedEscapedJAASValuesPreservedInSourceReview(t *testing.T) {
	for name, edit := range map[string]func(string) string{
		"disabled_escaped_quote": func(source string) string {
			return strings.Replace(source, `username='fixture' password=***;;`, `username=&quot;jaas-user-canary&quot; password=&quot;pw\&quot;SECRETPART&quot;;`, 1)
		},
		"disabled_backslash": func(source string) string {
			return strings.Replace(source, `username='fixture' password=***;;`, `username=&quot;jaas-user-canary&quot; password=&quot;pw\\SECRETPART&quot;;`, 1)
		},
		"disabled_apostrophes": func(source string) string {
			return strings.Replace(source, `username='fixture' password=***;;`, `username=&apos;jaas-user-canary&apos; password=&apos;pw\&apos;SECRETPART&apos;;`, 1)
		},
		"enabled_oauth_escaped_quote": func(source string) string {
			return strings.Replace(source, `cs="***"`, `cs=&quot;pw\&quot;SECRETPART&quot;`, 1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			source := string(messagingFixture(t, "original"))
			edited := edit(source)
			if edited == source {
				t.Fatal("fixture edit did not apply")
			}
			result := convertMessagingVariant(t, edit)
			content := result.SourceReview.SourceFiles[0].Content
			if content != edited {
				t.Fatal("source review did not preserve raw JMX content verbatim")
			}
			if result.SourceReview.SourceFiles[0].Redacted {
				t.Fatal("source review should not be marked redacted")
			}
		})
	}
}

// The Java client reads enable.idempotence trimmed and ignoring case, refuses
// any other value, and refuses fewer than one request in flight or, with an
// explicit enable.idempotence=true, retries=0.
func TestMessagingKafkaIdempotenceParsedLikeJava(t *testing.T) {
	unset := func(source string) string {
		return replaceKafkaSettingRow(source, "enable.idempotence", "false", "acks", "all")
	}
	setIdempotence := func(value string) func(string) string {
		return func(source string) string { return replaceKafkaSetting(source, "enable.idempotence", "false", value) }
	}
	for _, tc := range []struct {
		name        string
		edit        func(string) string
		invalid     bool
		idempotence string
	}{
		{"upper_true_above_five", func(source string) string { return withKafkaInFlight(setIdempotence(" TRUE ")(source), "10") }, true, "enabled"},
		{"mixed_false_three", func(source string) string { return withKafkaInFlight(setIdempotence("False")(source), "3") }, false, "disabled"},
		{"neither_true_nor_false", setIdempotence("yes"), true, ""},
		{"unset_zero_in_flight", func(source string) string { return withKafkaInFlight(unset(source), "0") }, true, ""},
		{"explicit_true_zero_in_flight", func(source string) string { return withKafkaInFlight(setIdempotence("true")(source), "0") }, true, "enabled"},
		{"explicit_true_retries_zero", func(source string) string {
			return replaceKafkaSetting(setIdempotence("true")(source), "retries", "1", "0")
		}, true, "enabled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := convertMessagingVariant(t, tc.edit)
			kafka := result.Scripts[0].Config.Kafka
			codes := kafkaReviewCodes(result.Scripts[0])
			if got := strings.Contains(codes, "jmeter_kafka_producer_setting_invalid"); got != tc.invalid {
				t.Fatalf("setting_invalid gate = %v, want %v: %s", got, tc.invalid, codes)
			}
			if kafka.Idempotence != tc.idempotence {
				t.Fatalf("idempotence = %q, want %q", kafka.Idempotence, tc.idempotence)
			}
			if strings.Contains(strings.Join(diagnosticDetails(result, diag.Info), "\n"), "leaves enable.idempotence unset") && tc.idempotence != "" {
				t.Fatal("a set enable.idempotence was reported as unset")
			}
			settings := result.Scripts[0].Actions[0].Metadata["jmeter_kafka"].(map[string]interface{})["effective_settings"].(map[string]string)
			if tc.idempotence == "enabled" && settings["enable.idempotence"] == "false" {
				t.Fatalf("evidence contradicts the source: %v", settings["enable.idempotence"])
			}
			if tc.name == "mixed_false_three" && (kafka.Producer == nil || kafka.Producer.MaxInFlightRequests == nil || *kafka.Producer.MaxInFlightRequests != 3) {
				t.Fatalf("a non-idempotent producer applies its in-flight value: %+v", kafka.Producer)
			}
		})
	}
}

func TestMessagingDisabledJAASPreservesSourceReviewAndReadableHelper(t *testing.T) {
	for name, jaas := range map[string]string{
		"variable_reference": `${kafka_jaas}`,
		"comment_no_space":   `org.apache.kafka.common.security.plain.PlainLoginModule required username="svc-user-1"/**/password=&apos;cmt-password-canary&apos;;`,
	} {
		t.Run(name, func(t *testing.T) {
			result := convertMessagingVariant(t, func(source string) string {
				source = strings.Replace(source, `org.apache.kafka.common.security.plain.PlainLoginModule required username='fixture' password=***;;`, jaas, 1)
				return strings.Replace(source, `<collectionProp name="Arguments.arguments" />`, `<collectionProp name="Arguments.arguments"><elementProp name="kafka_jaas" elementType="Argument"><stringProp name="Argument.name">kafka_jaas</stringProp><stringProp name="Argument.value">org.apache.kafka.common.security.plain.PlainLoginModule required username="svc-user" password="udv-password-canary";</stringProp></elementProp></collectionProp>`, 1)
			})
			content := result.SourceReview.SourceFiles[0].Content
			if !strings.Contains(content, "udv-password-canary") || result.SourceReview.SourceFiles[0].Redacted {
				t.Fatal("source review did not preserve raw JMX verbatim")
			}
		})
	}
	if !messagingJAASOptionsReadable(`org.apache.kafka.common.security.plain.PlainLoginModule required username='u' password="p";`) {
		t.Fatal("an ordinary PLAIN JAAS must stay readable")
	}
}

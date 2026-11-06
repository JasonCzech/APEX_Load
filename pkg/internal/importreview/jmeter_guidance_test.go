package importreview

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestJMeterReviewGuidanceCoversEveryGateCode(t *testing.T) {
	for _, code := range []string{
		"jmeter_eventhubs_credentials_required", "jmeter_kafka_oauth_credentials_required",
		"jmeter_kafka_nonstandard_oauth_options", "jmeter_reference_context_confirmation_required",
		"jmeter_messaging_compatibility_unverified", "jmeter_kafka_batch_allocation_parity_unverified",
		"jmeter_timestamp_timezone_required",
	} {
		guidance, ok := JMeterReviewCodeGuidance(code)
		if !ok || guidance.Code != code || guidance.Title == "" || guidance.Detail == "" || guidance.Resolution == "" {
			t.Fatalf("%s guidance = %+v", code, guidance)
		}
		if strings.Contains(guidance.Detail+guidance.Resolution, code) {
			t.Fatalf("%s guidance repeats the raw code instead of explaining it", code)
		}
		if !guidance.Clearable && !strings.Contains(guidance.Resolution, "cannot") && !strings.Contains(guidance.Resolution, "Nothing") {
			t.Fatalf("%s cannot be cleared but its resolution does not say so: %q", code, guidance.Resolution)
		}
		if guidance.Clearable && guidance.BatchOperation == "" && len(guidance.Reinspect) == 0 {
			t.Fatalf("%s is clearable but names no repair or re-conversion", code)
		}
		// The resolution speaks in the batch form's labels; API clients read
		// the operation and option names from BatchOperation and Reinspect.
		for _, wire := range []string{"confirm_reference_context", "bind_kafka_credentials", "normalize_oauth_options", "bind_eventhubs_credentials", "client_id_secret", "secret_name", "time_mode", `"utc"`, `"source"`} {
			if strings.Contains(guidance.Resolution, wire) {
				t.Fatalf("%s resolution names the wire identifier %s instead of the form's label: %q", code, wire, guidance.Resolution)
			}
		}
	}
	for code, labels := range map[string][]string{
		"jmeter_timestamp_timezone_required":             {`"Normalize to UTC with milliseconds"`, `"Preserve wall clock and acknowledge literal Z suffix"`},
		"jmeter_reference_context_confirmation_required": {`"Confirm the reference timezone and no property overrides"`},
		"jmeter_kafka_oauth_credentials_required":        {`"Bind Kafka credential references"`, `"Client ID secret reference"`, `"Client secret reference"`},
		"jmeter_kafka_nonstandard_oauth_options":         {`"Normalize Kafka OAuth option names"`},
		"jmeter_eventhubs_credentials_required":          {`"Bind Event Hubs credential reference"`, `"Connection string secret reference"`},
	} {
		guidance, _ := JMeterReviewCodeGuidance(code)
		for _, label := range labels {
			if !strings.Contains(guidance.Resolution, label) {
				t.Fatalf("%s resolution does not name the form label %s: %q", code, label, guidance.Resolution)
			}
		}
	}
	if guidance, _ := JMeterReviewCodeGuidance("jmeter_eventhubs_credentials_required"); !strings.Contains(guidance.Resolution, EventHubsConnectionStringFormat) {
		t.Fatal("Event Hubs guidance omits the connection-string format")
	}
}

func TestJMeterReviewRepairSuggestionsPerCodeAndGroup(t *testing.T) {
	gated := func(name string, codes ...string) dsl.Action {
		reasons := make([]any, len(codes))
		for i, code := range codes {
			reasons[i] = code
		}
		return dsl.Action{Name: name, Type: "kafka_produce", Metadata: map[string]any{
			"execution_blocker": "import_review_required",
			"import_review":     map[string]any{"reason_codes": reasons},
		}}
	}
	group := func(id string, actions ...dsl.Action) *dsl.Script {
		return &dsl.Script{Config: dsl.ScriptConfig{JMeter: &dsl.JMeterCompatibility{SourceGroupID: id}}, Actions: actions}
	}
	scripts := []*dsl.Script{
		group("g-kafka", gated("send", "jmeter_kafka_nonstandard_oauth_options", "jmeter_kafka_oauth_credentials_required",
			"jmeter_reference_context_confirmation_required", "jmeter_messaging_compatibility_unverified", "unrelated_code")),
		group("g-east", gated("east", "jmeter_eventhubs_credentials_required", "jmeter_reference_context_confirmation_required", "jmeter_messaging_compatibility_unverified")),
		group("g-west", gated("west", "jmeter_eventhubs_credentials_required", "jmeter_messaging_compatibility_unverified")),
		{Actions: []dsl.Action{gated("BALANCE", "jmeter_timestamp_timezone_required")}},
	}
	byID := map[string]ScriptImportRepairSuggestion{}
	for _, suggestion := range JMeterReviewRepairSuggestions(scripts) {
		if _, duplicate := byID[suggestion.ID]; duplicate {
			t.Fatalf("duplicate suggestion %s", suggestion.ID)
		}
		byID[suggestion.ID] = suggestion
	}
	if len(byID) != 7 {
		t.Fatalf("suggestions = %v, want 7", byID)
	}
	normalize := byID["jmeter:jmeter_kafka_nonstandard_oauth_options:g-kafka"]
	if normalize.BatchOperation != "normalize_oauth_options" || normalize.SourceGroupID != "g-kafka" || normalize.TargetAction != "send" || normalize.Clearable == nil || !*normalize.Clearable {
		t.Fatalf("normalize suggestion = %+v", normalize)
	}
	if byID["jmeter:jmeter_eventhubs_credentials_required:g-west"].BatchOperation != "bind_eventhubs_credentials" {
		t.Fatal("each Event Hubs group needs its own bind suggestion")
	}
	confirm := byID["jmeter:jmeter_reference_context_confirmation_required"]
	if confirm.BatchOperation != "confirm_reference_context" || confirm.SourceGroupID != "" {
		t.Fatalf("confirmation covers every group: %+v", confirm)
	}
	unverified := byID["jmeter:jmeter_messaging_compatibility_unverified"]
	if unverified.Clearable == nil || *unverified.Clearable || unverified.BatchOperation != "" {
		t.Fatalf("unverified messaging gate must say it cannot be cleared: %+v", unverified)
	}
	timestamp := byID["jmeter:jmeter_timestamp_timezone_required"]
	if len(timestamp.Reinspect) != 2 || timestamp.Reinspect[0].Field != "time_mode" || strings.Join(timestamp.Reinspect[0].Values, ",") != "utc,source" || timestamp.Reinspect[1].Field != "timezone" {
		t.Fatalf("timestamp suggestion = %+v", timestamp)
	}
}

// The explanation and the resolution travel as separate diagnostic fields:
// detail keeps only the meaning. A detail the converter wrote itself, and a
// code the table does not know, are left alone.
func TestExplainJMeterReviewDiagnosticsSplitsResolution(t *testing.T) {
	const code = "jmeter_eventhubs_credentials_required"
	diagnostics := diag.Diagnostics{
		{Code: code, Detail: jmeterGenericReviewDetail + code, Severity: diag.Blocker},
		{Code: code, Detail: "The converter's own sentence.", Severity: diag.Blocker},
		{Code: "unknown_code", Detail: jmeterGenericReviewDetail + "unknown_code", Severity: diag.Blocker},
	}
	ExplainJMeterReviewDiagnostics(diagnostics)
	guidance, _ := JMeterReviewCodeGuidance(code)
	if diagnostics[0].Detail != guidance.Detail || diagnostics[0].Resolution != guidance.Resolution {
		t.Fatalf("explained diagnostic = %+v", diagnostics[0])
	}
	if strings.Contains(diagnostics[0].Detail, guidance.Resolution) {
		t.Fatal("detail still carries the resolution")
	}
	if diagnostics[1].Resolution != "" || diagnostics[1].Detail != "The converter's own sentence." {
		t.Fatalf("converter-authored detail changed: %+v", diagnostics[1])
	}
	if diagnostics[2].Resolution != "" || diagnostics[2].Detail != jmeterGenericReviewDetail+"unknown_code" {
		t.Fatalf("unknown code changed: %+v", diagnostics[2])
	}
	if text := diagnostics[0].String(); !strings.Contains(text, guidance.Detail+" "+guidance.Resolution) {
		t.Fatalf("text form lost the resolution: %q", text)
	}
	encoded, _ := json.Marshal(diagnostics[0])
	if !strings.Contains(string(encoded), `"resolution":`) {
		t.Fatalf("resolution not serialized: %s", encoded)
	}
	encoded, _ = json.Marshal(diagnostics[1])
	if strings.Contains(string(encoded), `"resolution"`) {
		t.Fatalf("empty resolution serialized: %s", encoded)
	}
}

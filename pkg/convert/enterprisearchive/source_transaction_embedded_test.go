package enterprisearchive

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
)

func TestASTParserEnumeratesEmbeddedUnsupportedTransactionCalls(t *testing.T) {
	source := `int Action()
{
	double duration = lr_get_transaction_duration("DECLARATION_CANARY");
	status = lr_get_transaction_status("ASSIGNMENT_CANARY");
	if (lr_get_transaction_think_time("CONDITION_CANARY") > 0) {
		lr_log_message("condition body");
	}
	return lr_wasted_time(250);
	int instance = lr_start_transaction_instance("OWNERSHIP_CANARY", 0);
}`

	parsed, err := ParseActionFileAST("Action.c", source)
	if err != nil {
		t.Fatalf("ParseActionFileAST: %v", err)
	}

	var got []string
	for _, fn := range parsed.Functions {
		if nativeSourceTransactionReviewUnsupported(fn.Name) {
			got = append(got, fn.Name)
			if fn.Filename != "Action.c" || fn.LineNumber <= 0 || fn.EndLine < fn.LineNumber {
				t.Fatalf("%s provenance = %#v, want file and exact line range", fn.Name, fn)
			}
			if fn.RawBlock == "" || !strings.Contains(fn.RawBlock, fn.Name) {
				t.Fatalf("%s RawBlock = %q, want internal source evidence", fn.Name, fn.RawBlock)
			}
		}
	}
	want := []string{
		"lr_get_transaction_duration",
		"lr_get_transaction_status",
		"lr_get_transaction_think_time",
		"lr_wasted_time",
		"lr_start_transaction_instance",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unsupported transaction expressions = %v, want %v", got, want)
	}
}

func TestNestedUnsupportedTransactionCallsPrecedeConsumersAndGateEachConsumer(t *testing.T) {
	source := `int Action()
{
	lr_log_message("before");
	lr_output_message("duration %f", lr_get_transaction_duration("DURATION_CANARY"));
	lr_output_message("wasted %d", lr_wasted_time(250));
	lr_output_message("two %f %d",
		lr_get_transaction_duration("MULTI_DURATION_CANARY"),
		lr_get_transaction_status("MULTI_STATUS_CANARY"));
	return 0;
}`

	parsed, err := ParseActionFileAST("Action.c", source)
	if err != nil {
		t.Fatalf("ParseActionFileAST: %v", err)
	}
	var parsedOrder []string
	for _, fn := range parsed.Functions {
		parsedOrder = append(parsedOrder, fn.Name)
	}
	wantOrder := []string{
		"lr_log_message",
		"lr_get_transaction_duration",
		"lr_output_message",
		"lr_wasted_time",
		"lr_output_message",
		"lr_get_transaction_duration",
		"lr_get_transaction_status",
		"lr_output_message",
	}
	if !reflect.DeepEqual(parsedOrder, wantOrder) {
		t.Fatalf("parse order = %v, want argument evaluation before each enclosing call: %v", parsedOrder, wantOrder)
	}

	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{ScriptName: "embedded-transaction"})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	actions := flattenImportedSourceActions(result.Script.Actions)
	if len(actions) != len(wantOrder) {
		t.Fatalf("actions = %#v, want %d source occurrences", actions, len(wantOrder))
	}
	for index, wantFunction := range wantOrder {
		if got := importedSourceFunction(actions[index]); got != wantFunction {
			t.Fatalf("actions[%d] function = %q, want %q", index, got, wantFunction)
		}
	}

	innerIndexes := []int{1, 3, 5, 6}
	for _, index := range innerIndexes {
		action := actions[index]
		if action.Type != "source_review" {
			t.Fatalf("actions[%d] = %#v, unsupported return-value call must be review-only", index, action)
		}
		if action.Metadata["execution_blocker"] != unsupportedNativeSourceTransactionValueCode {
			t.Fatalf("actions[%d] blocker = %#v, want %s", index, action.Metadata, unsupportedNativeSourceTransactionValueCode)
		}
	}

	consumerIndexes := []int{2, 4, 7}
	for _, index := range consumerIndexes {
		action := actions[index]
		if action.Type != "source_review" {
			t.Fatalf("actions[%d] type = %q, supported consumer should remain as a review action", index, action.Type)
		}
		if action.Params["support_mode"] != string(SourceFunctionSupportReviewOnly) ||
			action.Metadata["support_mode"] != string(SourceFunctionSupportReviewOnly) {
			t.Fatalf("actions[%d] support mode = %#v / %#v, want review_only", index, action.Params, action.Metadata)
		}
		if action.Metadata["execution_blocker"] != unsupportedEmbeddedSourceTransactionConsumerCode {
			t.Fatalf("actions[%d] blocker = %#v, want independent consumer gate", index, action.Metadata)
		}
		if action.Metadata["embedded_unsupported_transaction_call"] != true {
			t.Fatalf("actions[%d] metadata = %#v, want embedded-call provenance", index, action.Metadata)
		}
	}

	// Simulate a reviewer deleting only the inner getter placeholder. The
	// enclosing consumer must retain its own engine-enforced import gate.
	result.Script.Actions = []dsl.Action{actions[2]}
	if _, err := engine.New(engine.Config{
		Script:   result.Script,
		VUsers:   1,
		Duration: time.Millisecond,
	}); err == nil || !strings.Contains(err.Error(), unsupportedEmbeddedSourceTransactionConsumerCode) {
		t.Fatalf("engine.New after deleting inner placeholder error = %v, want independent consumer blocker", err)
	}
}

func TestEmbeddedUnsupportedTransactionCallsPreserveLifecyclePhase(t *testing.T) {
	fixtures := []struct {
		filename string
		phase    string
		source   string
		function string
	}{
		{
			filename: "vuser_init.c",
			phase:    "init",
			source: `int vuser_init()
{
	double value = lr_get_transaction_duration("INIT_CANARY");
	return 0;
}`,
			function: "lr_get_transaction_duration",
		},
		{
			filename: "Action.c",
			phase:    "action",
			source: `int Action()
{
	status = lr_get_transaction_status("ACTION_CANARY");
	return 0;
}`,
			function: "lr_get_transaction_status",
		},
		{
			filename: "vuser_end.c",
			phase:    "teardown",
			source: `int vuser_end()
{
	int instance = lr_start_transaction_instance("TEARDOWN_CANARY", 0);
	return 0;
}`,
			function: "lr_start_transaction_instance",
		},
	}

	parsedActions := make([]*ParsedAction, 0, len(fixtures))
	for _, fixture := range fixtures {
		parsed, err := ParseActionFileAST(fixture.filename, fixture.source)
		if err != nil {
			t.Fatalf("ParseActionFileAST(%s): %v", fixture.filename, err)
		}
		parsed.Phase = fixture.phase
		parsedActions = append(parsedActions, parsed)
	}

	result, err := Convert(parsedActions, ConvertOptions{ScriptName: "phases"})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	phaseActions := [][]dsl.Action{result.Script.Init, result.Script.Actions, result.Script.Teardown}
	if len(phaseActions[0]) != 1 || len(phaseActions[1]) != 1 || len(phaseActions[2]) != 1 {
		t.Fatalf("phase actions = init:%#v action:%#v teardown:%#v", phaseActions[0], phaseActions[1], phaseActions[2])
	}
	for index, fixture := range fixtures {
		action := phaseActions[index][0]
		if action.Type != "source_review" || importedSourceFunction(action) != fixture.function {
			t.Fatalf("%s action = %#v, want gated %s", fixture.phase, action, fixture.function)
		}
		if got := fmt.Sprint(action.Metadata["phase"]); got != fixture.phase {
			t.Fatalf("%s phase metadata = %q", fixture.function, got)
		}
		foundMapping := false
		for _, mapping := range result.SourceReview.Mappings {
			if mapping.SourceFile == fixture.filename &&
				mapping.Function == fixture.function &&
				mapping.StartLine > 0 &&
				mapping.Status == "review_only" {
				foundMapping = true
				break
			}
		}
		if !foundMapping {
			t.Fatalf("mappings = %#v, want %s/%s lifecycle provenance", result.SourceReview.Mappings, fixture.filename, fixture.function)
		}
	}
}

func TestMalformedCFallbackRecoversEmbeddedTransactionCallsWithoutFalsePositives(t *testing.T) {
	source := `Action()
{
	lr_output_message("fallback %f", lr_get_transaction_duration("FALLBACK_CANARY"));
	lr_get_transaction_status("STANDALONE_CANARY");
	// lr_get_transaction_think_time("COMMENT_CANARY");
	/* lr_wasted_time(999); */
	char *literal = "lr_get_transaction_duration(\"STRING_CANARY\")";
#define FAKE_TRANSACTION() lr_get_transaction_status("MACRO_CANARY") \
	+ lr_wasted_time(777)
	int broken = ;
	return 0;
}`
	if _, err := ParseActionFileAST("Action.c", source); err == nil {
		t.Fatal("fixture must reject strict AST parsing")
	}

	parsed, err := ParseActionFile("Action.c", source)
	if err != nil {
		t.Fatalf("ParseActionFile fallback: %v", err)
	}
	counts := make(map[string]int)
	for _, fn := range parsed.Functions {
		counts[fn.Name]++
	}
	if counts["lr_output_message"] != 1 ||
		counts["lr_get_transaction_duration"] != 1 ||
		counts["lr_get_transaction_status"] != 1 {
		t.Fatalf("fallback counts = %#v, want outer + embedded + standalone exactly once", counts)
	}
	if counts["lr_get_transaction_think_time"] != 0 || counts["lr_wasted_time"] != 0 {
		t.Fatalf("fallback counts = %#v, comments/strings/macros must not become calls", counts)
	}

	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{ScriptName: "malformed-fallback"})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	actions := flattenImportedSourceActions(result.Script.Actions)
	var gatedInner int
	var gatedConsumer int
	for _, action := range actions {
		switch action.Metadata["execution_blocker"] {
		case unsupportedNativeSourceTransactionValueCode:
			gatedInner++
		case unsupportedEmbeddedSourceTransactionConsumerCode:
			gatedConsumer++
		}
	}
	if gatedInner != 2 || gatedConsumer != 1 {
		t.Fatalf("actions = %#v, want two unsupported occurrences and independently gated consumer", actions)
	}
}

func TestMalformedCFallbackConservativelyGatesTransactionControlledConsumers(t *testing.T) {
	source := `Action()
{
	if (lr_get_transaction_status("MALFORMED_COND_CANARY_6c3e") == LR_FAIL) {
		web_url("MALFORMED_BRANCH_CANARY_814f",
			"URL=https://malformed-branch-canary.invalid/private",
			LAST);
	}
	int broken = ;
	return 0;
}`
	if _, err := ParseActionFileAST("Action.c", source); err == nil {
		t.Fatal("fixture must reject strict AST parsing")
	}
	parsed, err := ParseActionFile("Action.c", source)
	if err != nil {
		t.Fatalf("ParseActionFile fallback: %v", err)
	}
	if len(parsed.Functions) != 2 ||
		parsed.Functions[0].Name != "lr_get_transaction_status" ||
		parsed.Functions[1].Name != "web_url" ||
		!parsed.Functions[1].dependsOnUnsupportedTransactionFlow {
		t.Fatalf("fallback functions = %#v, want getter plus conservatively gated branch consumer", parsed.Functions)
	}

	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{ScriptName: "malformed-condition"})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	serialized, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	for _, canary := range []string{
		"MALFORMED_COND_CANARY_6c3e",
		"MALFORMED_BRANCH_CANARY_814f",
		"malformed-branch-canary.invalid",
	} {
		if !strings.Contains(string(serialized), canary) {
			t.Fatalf("serialized malformed fallback lost %q: %s", canary, serialized)
		}
	}
	actions := flattenImportedSourceActions(result.Script.Actions)
	if len(actions) != 3 ||
		actions[1].Type != "source_review" ||
		actions[1].Metadata["execution_blocker"] != unsupportedEmbeddedSourceTransactionConsumerCode ||
		actions[2].Metadata["execution_blocker"] != "source_c_parse_failure" {
		t.Fatalf("fallback actions = %#v, want independent branch-consumer and malformed-source gates", actions)
	}

	result.Script.Actions = []dsl.Action{actions[1]}
	if _, err := engine.New(engine.Config{
		Script:   result.Script,
		VUsers:   1,
		Duration: time.Millisecond,
	}); err == nil || !strings.Contains(err.Error(), unsupportedEmbeddedSourceTransactionConsumerCode) {
		t.Fatalf("engine.New after deleting malformed getter error = %v, want fallback consumer blocker", err)
	}
}

func TestEmbeddedTransactionReviewEvidencePreservesSensitiveSource(t *testing.T) {
	source := `int Action()
{
	if (strcmp("FLOW_CANARY_8f81", "expected") == 0) {
		lr_output_message("OUTER_CANARY_71a2 %f",
			lr_get_transaction_duration("TX_CANARY_11c9"));
	}
	return 0;
}`
	result := convertSourceExecutionFixture(t, source)
	serialized, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	for _, canary := range []string{
		"FLOW_CANARY_8f81",
		"OUTER_CANARY_71a2",
		"TX_CANARY_11c9",
	} {
		if !strings.Contains(string(serialized), canary) {
			t.Fatalf("serialized conversion result lost %q: %s", canary, serialized)
		}
	}

	actions := flattenImportedSourceActions(result.Script.Actions)
	if len(actions) != 2 {
		t.Fatalf("actions = %#v, want inner review then outer consumer", actions)
	}
	for _, action := range actions {
		shape := fmt.Sprint(action.Metadata["source_call_shape"])
		if strings.Contains(shape, credential.Redacted) {
			t.Fatalf("source_call_shape = %q, contains a manufactured redaction", shape)
		}
		if _, retained := action.Metadata["source_call"]; retained {
			t.Fatalf("action retained raw source_call: %#v", action.Metadata)
		}
	}
}

func TestUnsupportedTransactionConditionGatesThenAndElseConsumersWithSourcePreserved(t *testing.T) {
	source := `int Action()
{
	if (lr_get_transaction_status("COND_SECRET_CANARY_92f4") == LR_FAIL) {
		web_url("THEN_STEP_CANARY_51bd", "URL=https://then-canary.invalid/private", LAST);
	} else {
		web_url("ELSE_STEP_CANARY_72ac", "URL=https://else-canary.invalid/private", LAST);
	}
	return 0;
}`
	parsed, err := ParseActionFileAST("Action.c", source)
	if err != nil {
		t.Fatalf("ParseActionFileAST: %v", err)
	}
	if len(parsed.Functions) != 3 {
		t.Fatalf("functions = %#v, want condition getter plus then/else consumers", parsed.Functions)
	}
	if parsed.Functions[0].Name != "lr_get_transaction_status" ||
		!parsed.Functions[1].dependsOnUnsupportedTransactionFlow ||
		!parsed.Functions[2].dependsOnUnsupportedTransactionFlow {
		t.Fatalf("functions = %#v, want both branches marked transaction-dependent", parsed.Functions)
	}

	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{ScriptName: "condition-gate"})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	serialized, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	for _, canary := range []string{
		"COND_SECRET_CANARY_92f4",
		"THEN_STEP_CANARY_51bd",
		"ELSE_STEP_CANARY_72ac",
		"then-canary.invalid",
		"else-canary.invalid",
	} {
		if !strings.Contains(string(serialized), canary) {
			t.Fatalf("serialized condition import lost %q: %s", canary, serialized)
		}
	}

	actions := flattenImportedSourceActions(result.Script.Actions)
	if len(actions) != 3 {
		t.Fatalf("actions = %#v, want getter plus then/else review consumers", actions)
	}
	for index := 1; index < len(actions); index++ {
		action := actions[index]
		if action.Type != "source_review" ||
			action.Metadata["execution_blocker"] != unsupportedEmbeddedSourceTransactionConsumerCode ||
			action.Metadata["unsupported_transaction_flow_dependency"] != true {
			t.Fatalf("branch action[%d] = %#v, want independent flow-consumer gate", index, action)
		}
		if condition := fmt.Sprint(action.Metadata[sourceFlowConditionKey]); !strings.Contains(condition, "COND_SECRET") {
			t.Fatalf("branch action[%d] condition = %q, want exact flow evidence", index, condition)
		}
	}

	// Removing only the condition getter must still leave every affected branch
	// non-executable.
	result.Script.Actions = append([]dsl.Action(nil), actions[1:]...)
	if _, err := engine.New(engine.Config{
		Script:   result.Script,
		VUsers:   1,
		Duration: time.Millisecond,
	}); err == nil || !strings.Contains(err.Error(), unsupportedEmbeddedSourceTransactionConsumerCode) {
		t.Fatalf("engine.New after deleting condition getter error = %v, want branch consumer blocker", err)
	}
}

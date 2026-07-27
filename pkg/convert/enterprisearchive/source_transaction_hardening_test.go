package enterprisearchive

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
)

func TestCTransactionCalleeNormalizationCoversWrappersMacrosAndAliases(t *testing.T) {
	source := `
int lr_get_transaction_status(const char *);
typedef int (*status_fn)(const char *);
#define TX_STATUS_OBJECT lr_get_transaction_status
#define TX_STATUS_FUNCTION(value) lr_get_transaction_status(value)
int Action()
{
	status_fn first = &lr_get_transaction_status;
	status_fn second = first;
	(lr_get_transaction_status)("direct_parenthesized");
	(&lr_get_transaction_status)("direct_address");
	(*lr_get_transaction_status)("direct_deref");
	TX_STATUS_OBJECT("object_macro");
	TX_STATUS_FUNCTION("function_macro");
	first("pointer_alias");
	lr_output_message("nested %d", second("copied_alias"));
	if (second("condition_alias") == 0) {
		lr_log_message("condition consumer");
	}
	return 0;
}`
	parsed, err := ParseActionFileAST("Action.c", source)
	if err != nil {
		t.Fatalf("ParseActionFileAST: %v", err)
	}
	if len(parsed.ParseFailures) != 0 {
		t.Fatalf("parse failures = %#v, want exact clean analysis", parsed.ParseFailures)
	}

	var transactionCalls []ParsedFunction
	var outer ParsedFunction
	var branchConsumer ParsedFunction
	for _, fn := range parsed.Functions {
		switch fn.Name {
		case "lr_get_transaction_status":
			transactionCalls = append(transactionCalls, fn)
		case "lr_output_message":
			outer = fn
		case "lr_log_message":
			branchConsumer = fn
		}
	}
	if len(transactionCalls) != 8 {
		t.Fatalf("transaction calls = %#v, want 8 normalized occurrences", transactionCalls)
	}
	for _, fn := range transactionCalls {
		if len(fn.CallArgs) != 1 || fn.RawArgs[0] != "[REDACTED]" {
			t.Fatalf("normalized call = %#v, want value-free one-argument placeholder", fn)
		}
	}
	if !outer.containsUnsupportedTransactionCall {
		t.Fatalf("nested consumer = %#v, want embedded transaction dependency", outer)
	}
	if !branchConsumer.dependsOnUnsupportedTransactionFlow {
		t.Fatalf("branch consumer = %#v, want transaction-controlled flow dependency", branchConsumer)
	}
}

func TestMalformedTransactionMacroFailsClosedWithoutSourceDisclosure(t *testing.T) {
	const source = `
#define TX_STATUS(value) lr_get_transaction_status(
Action()
{
	TX_STATUS("MACRO_VALUE_CANARY_61bb");
	web_url("MACRO_STEP_CANARY_b4f2",
		"URL=https://macro-value-canary.invalid/private",
		LAST);
	int broken = ;
	return 0;
}`
	parsed, err := ParseActionFile("Action.c", source)
	if err != nil {
		t.Fatalf("ParseActionFile recovered review evidence: %v", err)
	}
	if len(parsed.ParseFailures) == 0 {
		t.Fatal("malformed transaction macro did not preserve a parse failure")
	}
	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{ScriptName: "macro-fail-closed"})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}

	gate := findCSourceParseFailureGate(result.Script)
	if gate == nil || gate.Metadata["execution_blocker"] != cSourceParseFailureCode {
		t.Fatalf("parse gate = %#v, want durable %s blocker", gate, cSourceParseFailureCode)
	}
	serialized, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	for _, canary := range []string{
		"MACRO_VALUE_CANARY_61bb",
		"MACRO_STEP_CANARY_b4f2",
		"macro-value-canary.invalid",
		"int broken",
	} {
		if strings.Contains(string(serialized), canary) {
			t.Fatalf("serialized result leaked %q: %s", canary, serialized)
		}
	}
	if _, err := engine.New(engine.Config{
		Script:   result.Script,
		VUsers:   1,
		Duration: time.Millisecond,
	}); err == nil || !strings.Contains(err.Error(), cSourceParseFailureCode) {
		t.Fatalf("engine.New error = %v, want durable parse gate", err)
	}
}

func TestUnresolvedTransactionIdentityFailsClosed(t *testing.T) {
	parsed, err := ParseActionFile("Action.c", `
int Action()
{
	lr_get_transaction_unknown("UNRESOLVED_IDENTITY_CANARY_247a");
	return 0;
}`)
	if err != nil {
		t.Fatalf("ParseActionFile: %v", err)
	}
	foundFailure := false
	for _, failure := range parsed.ParseFailures {
		if failure.Kind == "unresolved_transaction_identity" {
			foundFailure = true
			break
		}
	}
	if !foundFailure {
		t.Fatalf("parse failures = %#v, want unresolved transaction identity", parsed.ParseFailures)
	}
	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{ScriptName: "unresolved-identity"})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	gate := findCSourceParseFailureGate(result.Script)
	if gate == nil || gate.Metadata["execution_blocker"] != cSourceParseFailureCode {
		t.Fatalf("parse gate = %#v, want unresolved identity execution gate", gate)
	}
	serialized, err := json.Marshal(gate)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if strings.Contains(string(serialized), "UNRESOLVED_IDENTITY_CANARY_247a") {
		t.Fatalf("durable gate leaked unresolved call value: %s", serialized)
	}
}

func TestCSourceParseFailuresSurviveSiblingFilesAndLifecyclePhases(t *testing.T) {
	fixtures := []struct {
		filename string
		phase    string
		source   string
	}{
		{
			filename: "vuser_init.c",
			phase:    "init",
			source:   "int vuser_init() {\n lr_get_transaction_status(\"INIT_PARSE_CANARY_11\");\n int bad = ;\n",
		},
		{
			filename: "BrokenAction.c",
			phase:    "action",
			source:   "int BrokenAction() {\n lr_get_transaction_status(\n",
		},
		{
			filename: "vuser_end.c",
			phase:    "teardown",
			source:   "int vuser_end() {\n lr_get_transaction_status(\"END_PARSE_CANARY_33\");\n int bad = ;\n",
		},
	}
	actions := make([]*ParsedAction, 0, len(fixtures)+1)
	for _, fixture := range fixtures {
		parsed, _ := ParseActionFile(fixture.filename, fixture.source)
		if parsed == nil || len(parsed.ParseFailures) == 0 {
			t.Fatalf("%s parse result = %#v, want durable failure evidence", fixture.filename, parsed)
		}
		parsed.Phase = fixture.phase
		for index := range parsed.Functions {
			parsed.Functions[index].Phase = fixture.phase
		}
		for index := range parsed.ParseFailures {
			parsed.ParseFailures[index].Phase = fixture.phase
		}
		actions = append(actions, parsed)
	}
	valid, err := ParseActionFile("Action.c", `
int Action()
{
	web_url("healthy", "URL=https://example.test/", LAST);
	return 0;
}`)
	if err != nil {
		t.Fatalf("valid sibling: %v", err)
	}
	valid.Phase = "action"
	actions = append(actions, valid)

	result, err := Convert(actions, ConvertOptions{ScriptName: "lifecycle-parse-gates"})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	gotFiles := make(map[string]string)
	for phase, phaseActions := range map[string][]dsl.Action{
		"init":     result.Script.Init,
		"action":   result.Script.Actions,
		"teardown": result.Script.Teardown,
	} {
		for index := range phaseActions {
			action := &phaseActions[index]
			if action.Metadata["execution_blocker"] == cSourceParseFailureCode {
				gotFiles[fmt.Sprint(action.Metadata["source_file"])] = phase
			}
		}
	}
	wantFiles := map[string]string{
		"vuser_init.c":   "init",
		"BrokenAction.c": "action",
		"vuser_end.c":    "teardown",
	}
	if !reflect.DeepEqual(gotFiles, wantFiles) {
		t.Fatalf("parse gates = %#v, want %#v", gotFiles, wantFiles)
	}
	if _, err := engine.New(engine.Config{
		Script:   result.Script,
		VUsers:   1,
		Duration: time.Millisecond,
	}); err == nil || !strings.Contains(err.Error(), cSourceParseFailureCode) {
		t.Fatalf("engine.New error = %v, want parse failure gate despite healthy sibling", err)
	}
}

func TestConvertZipPreservesMalformedSiblingAsDurableScriptGate(t *testing.T) {
	archive := buildSourceZip(t, map[string]string{
		"SiblingGate.usr": `[General]
Type=Web
[Actions]
Action=Action.c
Broken=Broken.c
`,
		"Action.c": `int Action()
{
	web_url("healthy", "URL=https://example.test/", LAST);
	return 0;
}`,
		"Broken.c": `int Broken()
{
	lr_get_transaction_status("ZIP_PARSE_CANARY_96c1");
	int broken = ;
`,
	})
	result, err := ConvertZip(archive, ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	if result.Script == nil {
		t.Fatalf("result = %#v, want valid sibling plus gated malformed file", result)
	}
	gate := findCSourceParseFailureGate(result.Script)
	if gate == nil ||
		gate.Metadata["execution_blocker"] != cSourceParseFailureCode ||
		gate.Metadata["source_file"] != "Broken.c" {
		t.Fatalf("parse gate = %#v, want Broken.c durable blocker", gate)
	}
	if result.SourceReview == nil {
		t.Fatal("source review missing")
	}
	foundMapping := false
	for _, mapping := range result.SourceReview.Mappings {
		if mapping.SourceFile == "Broken.c" &&
			mapping.Function == "c_source_parse_failure" &&
			mapping.Status == "review_only" {
			foundMapping = true
			break
		}
	}
	if !foundMapping {
		t.Fatalf("source mappings = %#v, want malformed sibling review mapping", result.SourceReview.Mappings)
	}
	serialized, err := json.Marshal(gate)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if strings.Contains(string(serialized), "ZIP_PARSE_CANARY_96c1") ||
		strings.Contains(string(serialized), "int broken") {
		t.Fatalf("serialized durable gate leaked source values: %s", serialized)
	}
}

func TestMalformedTransactionFallbackWorkAndEvidenceAreBounded(t *testing.T) {
	var source strings.Builder
	source.WriteString("Action()\n{\n")
	const calls = 5000
	for index := 0; index < calls; index++ {
		fmt.Fprintf(&source, "lr_get_transaction_status(\"CANARY_%d\"(\n", index)
	}
	source.WriteString("}\n")

	parsed, err := ParseActionFile("Action.c", source.String())
	if err == nil {
		t.Fatal("adversarial malformed source unexpectedly parsed cleanly")
	}
	if parsed == nil {
		t.Fatal("malformed source lost its value-free parse evidence")
	}
	if len(parsed.ParseFailures) == 0 ||
		len(parsed.ParseFailures) > maxCTransactionFailureEvidence+2 {
		t.Fatalf("parse failures = %d, want bounded non-zero evidence", len(parsed.ParseFailures))
	}
	if len(parsed.Functions) != 0 {
		t.Fatalf("functions = %d, unmatched calls must not be fabricated", len(parsed.Functions))
	}
}

func TestASTParserLargeOrdinaryCallFileMaintainsSourceOrder(t *testing.T) {
	var source strings.Builder
	source.WriteString("int Action()\n{\n")
	const calls = 3000
	for index := 0; index < calls; index++ {
		fmt.Fprintf(&source, "\tlr_log_message(\"message-%d\");\n", index)
	}
	source.WriteString("\treturn 0;\n}\n")

	parsed, err := ParseActionFileAST("Action.c", source.String())
	if err != nil {
		t.Fatalf("ParseActionFileAST: %v", err)
	}
	if len(parsed.Functions) != calls {
		t.Fatalf("functions = %d, want %d", len(parsed.Functions), calls)
	}
	previousLine := 0
	for index, fn := range parsed.Functions {
		if fn.Name != "lr_log_message" || fn.LineNumber <= previousLine {
			t.Fatalf("functions[%d] = %#v, want stable source order", index, fn)
		}
		previousLine = fn.LineNumber
	}
}

func findCSourceParseFailureGate(script *dsl.Script) *dsl.Action {
	if script == nil {
		return nil
	}
	for _, actions := range [][]dsl.Action{script.Init, script.Actions, script.Teardown} {
		for index := range actions {
			if actions[index].Metadata["execution_blocker"] == cSourceParseFailureCode {
				return &actions[index]
			}
		}
	}
	return nil
}

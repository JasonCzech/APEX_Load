package enterprisearchive

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func flattenImportedSourceActions(actions []dsl.Action) []dsl.Action {
	var flattened []dsl.Action
	for _, action := range actions {
		flattened = append(flattened, action)
		if action.Control != nil {
			flattened = append(flattened, flattenImportedSourceActions(action.Control.Steps)...)
			flattened = append(flattened, flattenImportedSourceActions(action.Control.Else)...)
		}
	}
	return flattened
}

func importedSourceFunction(action dsl.Action) string {
	if action.Params == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(fmt.Sprint(action.Params["function"])))
}

func importedSourceArgs(action dsl.Action) []any {
	if action.Params == nil {
		return nil
	}
	args, _ := action.Params["args"].([]any)
	return args
}

func TestCTransactionRuntimeUtilitiesAndSubtransactionsEmitExecutableActions(t *testing.T) {
	result := convertSourceExecutionFixture(t, `
Action()
{
	lr_start_transaction("Parent");
	lr_start_sub_transaction("Child", "Parent");
	lr_set_transaction_status_by_name(LR_FAIL, "Child");
	lr_stop_transaction("Parent");
	lr_resume_transaction("Parent");
	lr_fail_trans_with_error("checkout %s failed", "request");
	lr_end_sub_transaction("Child", LR_AUTO);
	lr_end_transaction("Parent", LR_AUTO);
	lr_set_transaction("Manual", 1.25, LR_STOP);
	return 0;
}`)

	actions := flattenImportedSourceActions(result.Script.Actions)
	wantFunctions := []string{
		"lr_start_transaction",
		"lr_start_sub_transaction",
		"lr_set_transaction_status_by_name",
		"lr_stop_transaction",
		"lr_resume_transaction",
		"lr_fail_trans_with_error",
		"lr_end_sub_transaction",
		"lr_end_transaction",
		"lr_set_transaction",
	}
	if len(actions) != len(wantFunctions) {
		t.Fatalf("actions = %#v, want %d positional transaction calls", actions, len(wantFunctions))
	}
	for index, wantFunction := range wantFunctions {
		action := actions[index]
		if action.Type != "source_transaction" || importedSourceFunction(action) != wantFunction {
			t.Fatalf("actions[%d] = %#v, want executable %s", index, action, wantFunction)
		}
		if action.Params["support_mode"] != string(SourceFunctionSupportExecutable) ||
			action.Metadata["support_mode"] != string(SourceFunctionSupportExecutable) {
			t.Fatalf("actions[%d] support metadata = %#v / %#v", index, action.Params, action.Metadata)
		}
		assertSourceReviewSupportMode(t, result.SourceReview.Mappings, wantFunction, SourceFunctionSupportExecutable)
	}

	if got := importedSourceArgs(actions[1]); !reflect.DeepEqual(got, []any{"Child", "Parent"}) {
		t.Fatalf("subtransaction args = %#v, want child then parent", got)
	}
	if got := importedSourceArgs(actions[2]); !reflect.DeepEqual(got, []any{"LR_FAIL", "Child"}) {
		t.Fatalf("status-by-name args = %#v, want status then transaction name", got)
	}
	if got := importedSourceArgs(actions[8]); !reflect.DeepEqual(got, []any{"Manual", "1.25", "LR_STOP"}) {
		t.Fatalf("set-transaction args = %#v, want exact name/duration/status", got)
	}
	if result.Stats.Transactions != 2 || result.Stats.TransactionMarkers != len(wantFunctions) {
		t.Fatalf("stats = %#v, want parent + immediate transaction and all positional markers", result.Stats)
	}
}

func TestNativeCTransactionValueCallsAreReviewOnlyAndDurablyGated(t *testing.T) {
	tests := []struct {
		function string
		call     string
	}{
		{function: "lr_get_transaction_duration", call: `lr_get_transaction_duration("Checkout");`},
		{function: "lr_get_transaction_status", call: `lr_get_transaction_status("Checkout");`},
		{function: "lr_get_transaction_think_time", call: `lr_get_transaction_think_time("Checkout");`},
		{function: "lr_wasted_time", call: `lr_wasted_time(250);`},
	}

	for _, test := range tests {
		t.Run(test.function, func(t *testing.T) {
			result := convertSourceExecutionFixture(t, "Action()\n{\n\t"+test.call+"\n\treturn 0;\n}\n")
			actions := flattenImportedSourceActions(result.Script.Actions)
			if len(actions) != 1 {
				t.Fatalf("actions = %#v, want one review placeholder", actions)
			}
			action := actions[0]
			if action.Type != "source_review" || importedSourceFunction(action) != test.function {
				t.Fatalf("action = %#v, want review-only %s", action, test.function)
			}
			if action.Params["support_mode"] != string(SourceFunctionSupportReviewOnly) ||
				action.Metadata["support_mode"] != string(SourceFunctionSupportReviewOnly) {
				t.Fatalf("support metadata = %#v / %#v", action.Params, action.Metadata)
			}
			if action.Metadata["execution_blocker"] != unsupportedNativeSourceTransactionValueCode {
				t.Fatalf("metadata = %#v, want durable execution blocker", action.Metadata)
			}
			if sourceFile := fmt.Sprint(action.Metadata["source_file"]); !strings.Contains(sourceFile, "Action.c") {
				t.Fatalf("source_file = %q, want Action.c evidence", sourceFile)
			}
			if _, retained := action.Metadata["source_call"]; retained {
				t.Fatalf("metadata retained raw source_call: %#v", action.Metadata)
			}
			wantArg := "Checkout"
			if test.function == "lr_wasted_time" {
				wantArg = "250"
			}
			if got := fmt.Sprint(action.Metadata["source_call_shape"]); got != test.function+"("+wantArg+")" {
				t.Fatalf("source_call_shape = %q, want exact imported call evidence", got)
			}
			if got := action.Metadata["source_argument_count"]; got != 1 {
				t.Fatalf("source_argument_count = %#v, want 1", got)
			}
			args, _ := action.Params["args"].([]any)
			if len(args) == 0 {
				t.Fatalf("args = %#v, want disclosure-safe argument shape", action.Params["args"])
			}
			for _, arg := range args {
				if fmt.Sprint(arg) != wantArg {
					t.Fatalf("args = %#v, want exact imported value %q", args, wantArg)
				}
			}
			review, _ := action.Metadata["import_review"].(map[string]any)
			if review["source"] != "enterprise_script_archive" ||
				!reflect.DeepEqual(review["reason_codes"], []string{unsupportedNativeSourceTransactionValueCode}) {
				t.Fatalf("import review = %#v, want transaction-value finding", review)
			}
			if result.Stats.ConvertedActions != 1 ||
				result.Stats.TransactionMarkers != 1 ||
				result.Stats.ReviewOnlyFunctions != 1 ||
				result.Stats.NativeFunctions != 0 {
				t.Fatalf("stats = %#v, want one review-only transaction marker/action", result.Stats)
			}
			assertSourceReviewSupportMode(t, result.SourceReview.Mappings, test.function, SourceFunctionSupportReviewOnly)
			if !strings.Contains(strings.Join(result.Warnings, "\n"), "not executed") {
				t.Fatalf("warnings = %#v, want explicit non-execution warning", result.Warnings)
			}
			if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
				t.Fatalf("validation issues = %#v", issues)
			}
		})
	}
}

func TestUnsupportedTransactionOwnershipCallsAreReviewOnlyAndDurablyGated(t *testing.T) {
	functions := []string{
		"lr_start_cross_vuser_transaction",
		"lr_end_cross_vuser_transaction",
		"lr_start_transaction_instance",
		"lr_end_transaction_instance",
		"lr_stop_transaction_instance",
		"lr_resume_transaction_instance",
		"lr_set_transaction_instance_status",
		"lr_get_trans_instance_duration",
		"lr_get_trans_instance_status",
		"lr_get_trans_instance_think_time",
		"lr_get_trans_instance_wasted_time",
	}
	var source strings.Builder
	source.WriteString("Action()\n{\n")
	for _, function := range functions {
		source.WriteString("\t")
		source.WriteString(function)
		source.WriteString("(\"Checkout\", 1, LR_PASS);\n")
	}
	source.WriteString("\treturn 0;\n}\n")

	result := convertSourceExecutionFixture(t, source.String())
	actions := flattenImportedSourceActions(result.Script.Actions)
	if len(actions) != len(functions) {
		t.Fatalf("actions = %#v, want one review placeholder per unsupported ownership call", actions)
	}
	if result.Stats.ReviewOnlyFunctions != len(functions) || result.Stats.NativeFunctions != 0 {
		t.Fatalf("stats = %#v, unsupported ownership calls must not count as native", result.Stats)
	}

	for index, function := range functions {
		action := actions[index]
		if action.Type != "source_review" || importedSourceFunction(action) != function {
			t.Fatalf("actions[%d] = %#v, want review-only %s", index, action, function)
		}
		if action.Metadata["execution_blocker"] != unsupportedSourceTransactionOwnershipCode {
			t.Fatalf("actions[%d] metadata = %#v, want durable execution blocker", index, action.Metadata)
		}
		blockers, _ := action.Metadata["execution_blockers"].([]string)
		if !reflect.DeepEqual(blockers, []string{unsupportedSourceTransactionOwnershipCode}) {
			t.Fatalf("actions[%d] blockers = %#v", index, blockers)
		}
		review, _ := action.Metadata["import_review"].(map[string]any)
		sourceRefs, _ := review["source_refs"].([]string)
		if review["source"] != "enterprise_script_archive" ||
			!reflect.DeepEqual(review["reason_codes"], []string{unsupportedSourceTransactionOwnershipCode}) ||
			len(sourceRefs) != 1 || !strings.Contains(sourceRefs[0], "Action.c:") {
			t.Fatalf("actions[%d] review evidence = %#v", index, review)
		}
		assertSourceReviewSupportMode(t, result.SourceReview.Mappings, function, SourceFunctionSupportReviewOnly)
	}
	if !strings.Contains(strings.Join(result.Warnings, "\n"), "not executed") {
		t.Fatalf("warnings = %#v, want explicit non-execution evidence", result.Warnings)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("validation issues = %#v", issues)
	}
}

func TestJavaUnsupportedTransactionOwnershipCallIsReviewOnlyAndDurablyGated(t *testing.T) {
	result := convertJavaLRBody(t, `lr.start_transaction_instance("Checkout");
    lr.get_trans_instance_status("Checkout");`)
	actions := flattenImportedSourceActions(result.Script.Actions)
	if len(actions) != 2 {
		t.Fatalf("actions = %#v, want two review placeholders", actions)
	}
	for _, action := range actions {
		if action.Type != "source_review" ||
			action.Metadata["execution_blocker"] != unsupportedSourceTransactionOwnershipCode {
			t.Fatalf("action = %#v, Java ownership call must be review-only and non-executable", action)
		}
		if metadataFile := fmt.Sprint(action.Metadata["source_file"]); !strings.Contains(metadataFile, "Actions.java") {
			t.Fatalf("action metadata = %#v, want Java source evidence", action.Metadata)
		}
	}
	if result.Stats.ReviewOnlyFunctions != 2 {
		t.Fatalf("stats = %#v, want two Java review-only functions", result.Stats)
	}
}

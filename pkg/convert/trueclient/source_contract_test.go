package trueclient

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestTruClientSourceArgumentsPreserveParameters(t *testing.T) {
	var value truArgument
	if err := json.Unmarshal([]byte(`{"value":"AccountID","param":true}`), &value); err != nil {
		t.Fatal(err)
	}
	if value.Value != "{{param.accountid}}" || !value.Param {
		t.Fatalf("parameter argument=%+v", value)
	}
	if err := json.Unmarshal([]byte(`{"value":"not a parameter","param":true}`), &value); err == nil {
		t.Fatal("invalid parameter name accepted")
	}
}

func TestTruClientSelectOrdinalContract(t *testing.T) {
	for _, test := range []struct {
		name, args string
		index      int
		review     bool
	}{
		{"first", `{"Selector":{"value":"#choice"},"Ordinal":{"value":1}}`, 0, false},
		{"third", `{"Selector":{"value":"#choice"},"Ordinal":{"value":3}}`, 2, false},
		{"default", `{"Selector":{"value":"#choice"}}`, 0, false},
		{"random", `{"Selector":{"value":"#choice"},"Ordinal":{"value":0}}`, -1, true},
		{"fractional", `{"Selector":{"value":"#choice"},"Ordinal":{"value":1.5}}`, -1, true},
		{"mixed", `{"Selector":{"value":"#choice"},"Text":{"value":"Label"},"Ordinal":{"value":2}}`, -1, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			actions, _, _, err := convertStep(truXMLStep{Action: "Select", Arguments: test.args}, nil)
			if err != nil {
				t.Fatal(err)
			}
			action := actions[0]
			if test.index >= 0 {
				if action.Type != "browser_select" || action.Params["option_index"] != test.index {
					t.Fatalf("action=%+v", action)
				}
			}
			if reviewRequired(action.Metadata) != test.review {
				t.Fatalf("review=%v want%v", action.Metadata, test.review)
			}
		})
	}
}

func TestTruClientIntervalAndTypeContracts(t *testing.T) {
	for _, test := range []struct {
		unit, value string
		want        string
	}{{"", "2.5", "2.5"}, {"milliseconds", "250", "250ms"}, {"seconds", "2", "2"}} {
		args, _ := json.Marshal(map[string]any{"Interval": map[string]any{"value": test.value}, "Unit": map[string]any{"value": test.unit}})
		action, ok, err := convertWaitStep(truXMLStep{Action: "Wait", Arguments: string(args)}, nil)
		if err != nil || !ok || action.WaitFor.Value != test.want {
			t.Fatalf("interval action=%+v ok=%v err=%v", action, ok, err)
		}
	}
	if _, _, err := convertWaitStep(truXMLStep{Action: "Wait", Arguments: `{"Interval":{"value":2},"Unit":{"value":"frames"}}`}, nil); err == nil {
		t.Fatal("unknown wait unit accepted")
	}
	actions, _, _, err := convertStep(truXMLStep{Action: "Type", Arguments: `{"Selector":{"value":"#account"},"Value":{"value":"AccountID","param":true},"Typing Interval":{"value":20}}`}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if actions[0].Value != "{{param.accountid}}" || actions[0].Params["input_mode"] != "type" || actions[0].Params["typing_delay_ms"] != 20 || !reviewRequired(actions[0].Metadata) {
		t.Fatalf("typed action=%+v", actions[0])
	}
}

func TestTruClientDisabledAndUnrepresentableStepsRemainAccounted(t *testing.T) {
	result, err := ConvertArchive(map[string]string{"default.xml": `<truScript><step action="action" actionName="Action"><step action="Navigate" stepId="1" arguments='{&quot;Location&quot;:{&quot;value&quot;:&quot;https://example.invalid/&quot;}}'/><step action="Click" disabled="true" stepId="2"/><step action="Wait" stepId="3"/><step action="Mystery" stepId="4"/></step></truScript>`}, ConvertOptions{ScriptName: "Source accounting", PreserveUnsupportedSteps: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stats.TotalSteps != 4 || result.Stats.Skipped != 1 || result.Stats.ReviewOnlySteps != 2 || len(result.Script.Actions) != 3 {
		t.Fatalf("stats=%+v actions=%d", result.Stats, len(result.Script.Actions))
	}
	for _, action := range result.Script.Actions {
		if action.Metadata["source_step_id"] == "2" {
			t.Fatal("disabled step emitted")
		}
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatal(issues)
	}
	control := truXMLStep{Action: "If", Disabled: true, Steps: []truXMLStep{{Action: "Click"}, {Action: "Wait"}}}
	if countDisabledExecutableSteps(control) != 3 {
		t.Fatal("disabled control descendants not counted")
	}
}

func TestTruClientSourceCompletionAndOptionalGates(t *testing.T) {
	action := dsl.Action{Type: "browser_click", Metadata: map[string]any{}}
	step := truXMLStep{Action: "Click", Optional: true, Unfinalized: true, EndEvent: "Step network completed", ManualDuration: "0", RecordedDuration: "2.5"}
	attachSourceStepMetadata(&action, step, "", nil)
	if !reviewRequired(action.Metadata) || action.Metadata["source_optional"] != true || action.Metadata["source_recorded_duration"] != "2.5" {
		t.Fatalf("metadata=%v", action.Metadata)
	}
	if strings.Contains(action.Metadata["execution_blocker"].(string), "manual_duration") {
		t.Fatal("zero manual duration falsely gated")
	}
	missing := dsl.Action{Type: "browser_click"}
	applyTruClientCompletionContract(&missing, truXMLStep{EndEvent: "Object Exists", EndEventArgs: `{"objectId":"missing"}`}, nil)
	if !reviewRequired(missing.Metadata) || missing.Sync != nil {
		t.Fatal("missing completion object guessed")
	}
	load := dsl.Action{Type: "browser_click"}
	applyTruClientCompletionContract(&load, truXMLStep{EndEvent: "Document Loaded"}, nil)
	if load.Sync == nil || load.Sync.Type != "load" {
		t.Fatal("load completion lost")
	}
	visible := dsl.Action{Type: "browser_click"}
	applyTruClientCompletionContract(&visible, truXMLStep{EndEvent: "Object Exists", EndEventArgs: `{"objectId":"ready"}`}, []ObjectDescriptor{{ID: "ready", SelectorCandidates: []SelectorCandidate{{Kind: "css", Value: "#ready", Confidence: 90}}}})
	if visible.Sync == nil || visible.Sync.Type != "element_visible" || visible.Sync.Value != "#ready" {
		t.Fatalf("completion visibility=%+v", visible.Sync)
	}
}

func TestTruClientAlternativeGraphIsPreservedWithoutSequentialExecution(t *testing.T) {
	result, err := ConvertArchive(map[string]string{"default.xml": `<truScript><step action="action" actionName="Action"><step type="alternative" action="Select" stepId="parent" activeStep="1"><step action="Click" stepId="child1"/><step action="Select" stepId="child2"/></step><step type="alternative" action="Click" disabled="true" stepId="disabled"><step action="Click"/></step></step></truScript>`}, ConvertOptions{ScriptName: "Alternatives", PreserveUnsupportedSteps: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Script.Actions) != 1 || result.Script.Actions[0].Type != "source_review" || result.Script.Actions[0].Metadata["source_step_graph"] == nil || !reviewRequired(result.Script.Actions[0].Metadata) || result.Stats.TotalSteps != 5 || result.Stats.Skipped != 2 || result.Stats.ReviewOnlySteps != 3 {
		t.Fatalf("alternative accounting=%+v actions=%v", result.Stats, result.Script.Actions)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) > 0 {
		t.Fatal(issues)
	}
}

func TestTruClientPreservedVerificationAndControlsCannotBypassSourceContract(t *testing.T) {
	for _, body := range []string{`<step action="Verify" stepId="verify"/>`, `<step action="Verify" stepId="verify" arguments='{&quot;Comparison&quot;:{&quot;value&quot;:&quot;unsupported&quot;}}'/>`, `<step action="Navigate" arguments='{&quot;Location&quot;:{&quot;value&quot;:&quot;https://example.invalid&quot;}}'/><step action="Verify" stepId="verify" optional="true" unfinalized="true" arguments='{&quot;Text&quot;:{&quot;value&quot;:&quot;ready&quot;}}'/>`} {
		result, err := ConvertArchive(map[string]string{"default.xml": "<truScript><step action=\"action\" actionName=\"Action\">" + body + "</step></truScript>"}, ConvertOptions{ScriptName: "Verify evidence", PreserveUnsupportedSteps: true})
		if err != nil {
			t.Fatal(err)
		}
		action := result.Script.Actions[len(result.Script.Actions)-1]
		if action.Type != "source_review" || action.Metadata["source_step_id"] != "verify" || !reviewRequired(action.Metadata) || result.Stats.ReviewOnlySteps != 1 {
			t.Fatalf("verification dropped or ungated: %+v", action)
		}
	}
	result, err := ConvertArchive(map[string]string{"default.xml": `<truScript><step action="action" actionName="Action"><step action="If" stepId="if" optional="true"/><step action="Else" disabled="true"><step action="Click" arguments='{&quot;Selector&quot;:{&quot;value&quot;:&quot;#forbidden&quot;}}'/></step><step action="Loop" unfinalized="true"><step action="Break" optional="true"/></step></step></truScript>`}, ConvertOptions{ScriptName: "Control evidence", PreserveUnsupportedSteps: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Script.Actions) != 2 || !reviewRequired(result.Script.Actions[0].Metadata) || !reviewRequired(result.Script.Actions[1].Metadata) || result.Stats.Skipped != 2 || len(result.Script.Actions[0].Control.Else) != 0 || !reviewRequired(result.Script.Actions[1].Control.Steps[0].Metadata) {
		t.Fatalf("control source flags or disabledElse lost: %+v", result)
	}
}

func TestTruClientCompletionContextMustMatchTrigger(t *testing.T) {
	for _, test := range []struct {
		frame, window string
		gated         bool
	}{{"child", "tab", false}, {"other", "tab", true}, {"child", "other", true}} {
		action := dsl.Action{Metadata: map[string]any{"source_frame_context": "child", "source_window_context": "tab"}}
		applyTruClientCompletionContract(&action, truXMLStep{EndEvent: "Object Exists", EndEventArgs: `{"objectId":"ready"}`}, []ObjectDescriptor{{ID: "ready", FrameContext: test.frame, WindowContext: test.window, SelectorCandidates: []SelectorCandidate{{Kind: "css", Value: "#ready", Confidence: 90}}}})
		if reviewRequired(action.Metadata) != test.gated || (action.Sync == nil) == !test.gated {
			t.Fatalf("completion context frame%q/window%q=%+v", test.frame, test.window, action)
		}
	}
}

func TestTruClientXMLTransactionAnchorsPreserveOrderingAndGateAmbiguity(t *testing.T) {
	marker := func(id string) dsl.Action {
		return dsl.Action{Name: id, Type: "browser_click", Selector: "#button", Metadata: map[string]any{"source_step_id": id}}
	}
	transaction := func(name, start, end string) truXMLTransaction {
		return truXMLTransaction{Name: name, Vertices: []truXMLTransactionVertex{{Type: "start", ProgressType: "Step started", Step: start}, {Type: "end", ProgressType: "After step ended", Step: end}}}
	}
	result := &ConvertResult{Script: &dsl.Script{Name: "Anchors", Protocol: "browser", Actions: []dsl.Action{marker("1"), marker("2"), marker("3")}}}
	applyXMLAnchoredTransactions(result, []truXMLTransaction{transaction("outer", "1", "3"), transaction("inner", "2", "2")}, true)
	if result.Stats.Transactions != 2 || len(result.Script.Actions) != 7 {
		t.Fatalf("stats=%+v actions=%v", result.Stats, result.Script.Actions)
	}
	want := []string{"lr_start_transaction", "1", "lr_start_transaction", "2", "lr_end_transaction", "3", "lr_end_transaction"}
	for i, action := range result.Script.Actions {
		got := action.Name
		if action.Type == "source_transaction" {
			got = action.Params["function"].(string)
			if action.Metadata["source_ref"] == nil || action.Metadata["source_anchor_step_id"] == nil {
				t.Fatal("marker provenance lost")
			}
		}
		if got != want[i] {
			t.Fatalf("order[%d]=%s want%s", i, got, want[i])
		}
	}
	for _, transactions := range [][]truXMLTransaction{{transaction("missing", "1", "disabled")}, {transaction("reversed", "3", "1")}, {transaction("duplicate", "1", "2"), transaction("duplicate", "2", "3")}, {{Name: "unsupported", Vertices: []truXMLTransactionVertex{{Type: "start", ProgressType: "After step ended", Step: "1"}, {Type: "end", ProgressType: "After step ended", Step: "2"}}}}} {
		gated := &ConvertResult{Script: &dsl.Script{Actions: []dsl.Action{marker("1"), marker("2"), marker("3")}}}
		applyXMLAnchoredTransactions(gated, transactions, true)
		if gated.Stats.Transactions != 0 || gated.Stats.ReviewOnlySteps != len(transactions) || gated.Script.Actions[0].Type != "source_review" || !reviewRequired(gated.Script.Actions[0].Metadata) {
			t.Fatalf("ambiguous anchors=%+v", gated)
		}
	}
}

package salesforce

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol/aura"
	"github.com/jasonczech/apexload/pkg/scriptdisclosure"
)

const syntheticAuraRequest = `{"actions":[{"id":"first;a","descriptor":"apex://Example/ACTION$first"},{"id":"second;a","descriptor":"apex://Example/ACTION$second"}]}`

func auraTestScript(body string) *dsl.Script {
	return &dsl.Script{Name: "Synthetic Aura", Protocol: "http", Actions: []dsl.Action{{
		Type: "POST", Method: "POST", Target: "https://example.lightning.force.com/aura", Body: body,
	}}}
}

func auraTestHAR(t *testing.T, request, response string) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{"log": map[string]any{"entries": []any{
		map[string]any{"request": map[string]any{
			"method": "POST", "url": "https://example.lightning.force.com/aura",
			"postData": map[string]any{"text": request, "mimeType": "application/x-www-form-urlencoded"},
		}, "response": map[string]any{"status": 200, "content": map[string]any{"text": response}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestAuraBaselineReportsReorderedKnownOutcomesAndAttachesNativeAssertion(t *testing.T) {
	body := "message=" + url.QueryEscape(syntheticAuraRequest)
	script := auraTestScript(body)
	// Nested application payload strings must not be mistaken for action state.
	response := `{"actions":[{"id":"second;a","state":"ERROR","error":[{"message":"PRIVATE_ERROR"}]},{"id":"first;a","state":"SUCCESS","returnValue":{"state":"ERROR"}}]}`
	review, err := Analyze(script, AnalyzeOptions{BuildNative: true, HARData: auraTestHAR(t, body, response)})
	if err != nil {
		t.Fatal(err)
	}
	outcomes := review.AuraOutcomes
	if outcomes == nil || !outcomes.Complete || outcomes.CompleteBatches != 1 || outcomes.IncompleteBatches != 0 || outcomes.RequestActions != 2 || outcomes.ResponseActions != 2 || outcomes.MatchedActions != 2 {
		t.Fatalf("unexpected aggregate: %#v", outcomes)
	}
	if outcomes.StateCounts[aura.Success] != 1 || outcomes.StateCounts[aura.Error] != 1 || len(review.Warnings) == 0 {
		t.Fatalf("baseline failure omitted: %#v warnings=%v", outcomes, review.Warnings)
	}
	batch := outcomes.Batches[0]
	if batch.HAREntryID != 1 || batch.ActionPath != "actions[0]" || batch.Analysis.Success || !batch.Analysis.HasResponse || !batch.RequestValid {
		t.Fatalf("unexpected batch: %#v", batch)
	}
	want := []aura.Outcome{{Ordinal: 1, State: aura.Success}, {Ordinal: 2, State: aura.Error}}
	if !reflect.DeepEqual(batch.Analysis.Outcomes, want) {
		t.Fatalf("outcomes = %#v, want %#v", batch.Analysis.Outcomes, want)
	}
	// The action that failed in the recording becomes the replay baseline, so
	// a repeat failure is labelled rather than chased as a correlation fault.
	assertions := review.NativePreview.Script.Actions[0].Assertions
	wantBaseline := aura.Baseline{Recorded: []aura.RecordedOutcome{{Ordinal: 2, State: aura.Error}}}
	if len(assertions) != 1 || assertions[0].Type != "aura_actions_success" || assertions[0].Value != "" || !reflect.DeepEqual(assertions[0].Expected, wantBaseline) {
		t.Fatalf("native assertions = %#v", assertions)
	}
	if outcomes.RecordedFailureActions != 1 || outcomes.RecordedFailureBatches != 1 || !strings.Contains(strings.Join(review.Warnings, "\n"), "1 Aura action(s) in 1 request(s) already failed in the recording") {
		t.Fatalf("recorded failure decision missing: actions=%d batches=%d warnings=%v", outcomes.RecordedFailureActions, outcomes.RecordedFailureBatches, review.Warnings)
	}
	if len(script.Actions[0].Assertions) != 0 || script.Actions[0].Metadata != nil {
		t.Fatal("native analysis mutated source script")
	}
	assertAuraReportValueFree(t, outcomes, "first;a", "second;a", "apex://Example", "PRIVATE_ERROR", "example.lightning.force.com")
}

func TestAuraBaselineMissingResponseDoesNotInventSuccess(t *testing.T) {
	for _, withHAR := range []bool{false, true} {
		t.Run(fmt.Sprint(withHAR), func(t *testing.T) {
			var data []byte
			if withHAR {
				data = auraTestHAR(t, syntheticAuraRequest, "")
			}
			review, err := Analyze(auraTestScript(syntheticAuraRequest), AnalyzeOptions{BuildNative: true, HARData: data})
			if err != nil {
				t.Fatal(err)
			}
			outcomes := review.AuraOutcomes
			if outcomes.Complete || outcomes.MissingResponses != 1 || outcomes.CompleteBatches != 0 || outcomes.IncompleteBatches != 1 || outcomes.MatchedActions != 0 || len(outcomes.StateCounts) != 0 {
				t.Fatalf("request-only baseline presented as success: %#v", outcomes)
			}
			batch := outcomes.Batches[0]
			if !batch.RequestValid || batch.Analysis.HasResponse || !batch.Analysis.Success || len(review.Warnings) == 0 {
				t.Fatalf("missing response evidence lost: %#v", batch)
			}
			if !hasAuraAssertion(review.NativePreview.Script.Actions[0]) {
				t.Fatal("valid request without response needs actual runtime assertion")
			}
		})
	}
}

func TestAuraBaselineIncompleteResponsesRemainExplicit(t *testing.T) {
	tests := []struct {
		name, response string
		code           aura.Code
		state          aura.State
		complete       bool
	}{
		{"missing action", `{"actions":[{"id":"first;a","state":"SUCCESS"}]}`, aura.MissingResponse, aura.Success, false},
		{"extra action", `{"actions":[{"id":"first;a","state":"SUCCESS"},{"id":"second;a","state":"SUCCESS"},{"id":"extra;a","state":"SUCCESS"}]}`, aura.UnexpectedResponse, aura.Success, false},
		{"duplicate id", `{"actions":[{"id":"first;a","state":"SUCCESS"},{"id":"first;a","state":"SUCCESS"}]}`, aura.DuplicateID, "", false},
		{"malformed", `{"actions":`, aura.InvalidJSON, "", false},
		{"unknown state", `{"actions":[{"id":"first;a","state":"PRIVATE_STATE"},{"id":"second;a","state":"SUCCESS"}]}`, aura.UnknownState, aura.Unknown, false},
		{"missing state", `{"actions":[{"id":"first;a"},{"id":"second;a","state":"SUCCESS"}]}`, aura.MissingState, "", false},
		{"incomplete", `{"actions":[{"id":"first;a","state":"INCOMPLETE"},{"id":"second;a","state":"SUCCESS"}]}`, aura.NonSuccessState, aura.Incomplete, true},
		{"aborted", `{"actions":[{"id":"first;a","state":"ABORTED"},{"id":"second;a","state":"SUCCESS"}]}`, aura.NonSuccessState, aura.Aborted, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			review, err := Analyze(auraTestScript(syntheticAuraRequest), AnalyzeOptions{BuildNative: true, HARData: auraTestHAR(t, syntheticAuraRequest, tc.response)})
			if err != nil {
				t.Fatal(err)
			}
			outcomes := review.AuraOutcomes
			if outcomes.Complete != tc.complete || outcomes.IssueCounts[tc.code] == 0 || outcomes.Batches[0].Analysis.Success {
				t.Fatalf("ambiguous/failed baseline not explicit: %#v", outcomes)
			}
			if tc.state != "" && outcomes.StateCounts[tc.state] == 0 {
				t.Fatalf("state %s omitted: %#v", tc.state, outcomes)
			}
			if !hasAuraAssertion(review.NativePreview.Script.Actions[0]) {
				t.Fatal("valid request still requires runtime assertion")
			}
			assertAuraReportValueFree(t, outcomes, "first;a", "second;a", "extra;a", "PRIVATE_STATE")
		})
	}
}

func TestAuraNativeMalformedRequestHasDurableGate(t *testing.T) {
	tests := []struct {
		name, body string
		code       aura.Code
	}{
		{"empty", "", aura.MissingMessage},
		{"invalid id", `{"actions":[{"descriptor":"apex://Example/ACTION$get"}]}`, aura.InvalidID},
		{"duplicate id", `{"actions":[{"id":"same","descriptor":"first"},{"id":"same","descriptor":"second"}]}`, aura.DuplicateID},
		{"duplicate message", "message=" + url.QueryEscape(syntheticAuraRequest) + "&message=" + url.QueryEscape(syntheticAuraRequest), aura.DuplicateMessage},
		{"byte limit", strings.Repeat("x", aura.MaxEnvelopeBytes+1), aura.ByteLimit},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			script := auraTestScript(tc.body)
			script.Actions[0].Metadata = map[string]any{"execution_blocker": "existing_gate"}
			review, err := Analyze(script, AnalyzeOptions{BuildNative: true})
			if err != nil {
				t.Fatal(err)
			}
			outcomes := review.AuraOutcomes
			if outcomes.Complete || outcomes.InvalidRequests != 1 || outcomes.Batches[0].RequestValid || outcomes.Batches[0].RequestIssues[0].Code != tc.code {
				t.Fatalf("invalid request omitted: %#v", outcomes)
			}
			native := review.NativePreview.Script
			if hasAuraAssertion(native.Actions[0]) {
				t.Fatal("malformed request received blind assertion coverage")
			}
			data, err := json.Marshal(native)
			if err != nil {
				t.Fatal(err)
			}
			var reopened dsl.Script
			if err := json.Unmarshal(data, &reopened); err != nil {
				t.Fatal(err)
			}
			findings, err := importreview.FindingsFromScript(&reopened)
			if err != nil {
				t.Fatal(err)
			}
			codes := map[string]bool{}
			for _, finding := range findings {
				if finding.Gate == importreview.GateExecution {
					codes[finding.Code] = true
				}
			}
			if !codes["existing_gate"] || !codes["salesforce_aura_request_requires_review"] {
				t.Fatalf("durable gates lost after reopen: %#v", findings)
			}
			if native.Actions[0].Metadata["import_review"] == nil {
				t.Fatal("durable provenance missing")
			}
			if script.Actions[0].Metadata["import_review"] != nil {
				t.Fatal("native gate mutated original source")
			}
		})
	}
}

func TestAuraBaselineUsesCapturedIDsBeforeExecutableRewrites(t *testing.T) {
	captured := syntheticAuraRequest
	executable := strings.ReplaceAll(captured, "first;a", "{{var.runtime_id}}")
	response := `{"actions":[{"id":"first;a","state":"SUCCESS"},{"id":"second;a","state":"SUCCESS"}]}`
	review, err := Analyze(auraTestScript(executable), AnalyzeOptions{HARData: auraTestHAR(t, captured, response)})
	if err != nil {
		t.Fatal(err)
	}
	if !review.AuraOutcomes.Complete || review.AuraOutcomes.MatchedActions != 2 {
		t.Fatalf("baseline incorrectly matched rewritten IDs: %#v", review.AuraOutcomes)
	}
}

func TestAuraBaselineReviewLimitsAreExplicit(t *testing.T) {
	script := auraTestScript(syntheticAuraRequest)
	action := script.Actions[0]
	for len(script.Actions) <= maxAuraReviewBatches {
		script.Actions = append(script.Actions, action)
	}
	outcomes := inspectAuraOutcomes(flattenRequestActions(script), nil)
	if outcomes.Complete || outcomes.TotalBatches != 513 || outcomes.InspectedBatches != 512 || outcomes.OmittedBatches != 1 || len(outcomes.Batches) != 512 || outcomes.IncompleteBatches != 512 {
		t.Fatalf("batch cap silently claimed completeness: %#v", outcomes)
	}
	// A large non-action payload consumes aggregate parser work, but not source
	// values in the report; omission must remain explicit at the byte budget.
	large := `{"actions":[{"id":"one","descriptor":"one","params":{"data":"` + strings.Repeat("x", 7<<20) + `"}}]}`
	refs := make([]actionRef, 6)
	for i := range refs {
		refs[i] = actionRef{Kind: "aura", Method: "POST", Sequence: i + 1, Action: &dsl.Action{Body: large}}
	}
	outcomes = inspectAuraOutcomes(refs, nil)
	if outcomes.OmittedBatches == 0 || outcomes.Complete || outcomes.InspectedBatches+outcomes.OmittedBatches != outcomes.TotalBatches {
		t.Fatal("aggregate byte cap omitted without accounting")
	}
}

func TestAuraSourceEntryAndSequenceChildPathsAreNotInferredFromRequestOrdinal(t *testing.T) {
	script := auraTestScript(syntheticAuraRequest)
	body := string(auraTestHAR(t, syntheticAuraRequest, `{"actions":[{"id":"first;a","state":"SUCCESS"},{"id":"second;a","state":"SUCCESS"}]}`))
	body = strings.Replace(body, `"entries":[`, `"entries":[{"request":{"method":"CONNECT","url":"https://example.test/tunnel"},"response":{}},{"request":{"method":"GET","url":"https://example.test/image.png"},"response":{}},`, 1)
	review, err := Analyze(script, AnalyzeOptions{HARData: []byte(body)})
	if err != nil {
		t.Fatal(err)
	}
	batch := review.AuraOutcomes.Batches[0]
	if batch.HAREntryID != 3 || batch.ActionPath != "actions[0]" || batch.ActionSequence != 1 {
		t.Fatalf("source entry indexing lost: %#v", batch)
	}
	sequence := &dsl.Script{Name: "Sequence", Protocol: "http", Actions: []dsl.Action{{Type: "http_sequence", Params: map[string]any{"requests": []dsl.Action{script.Actions[0]}}}}}
	review, err = Analyze(sequence, AnalyzeOptions{BuildNative: true})
	if err != nil {
		t.Fatal(err)
	}
	if review.AuraOutcomes.Batches[0].ActionPath != "actions[0].params.requests[0]" {
		t.Fatalf("incorrect exact path: %#v", review.AuraOutcomes.Batches[0])
	}
	children := salesforceSequenceChildren(review.NativePreview.Script.Actions[0])
	if len(children) != 1 || !hasAuraAssertion(children[0]) {
		t.Fatal("native assertion on sequence child did not persist")
	}
}

func TestAuraGenericAnalysisPreservesScriptAndNativeReplacesOnlyLegacyGuards(t *testing.T) {
	script := auraTestScript(syntheticAuraRequest)
	script.Actions[0].Assertions = []dsl.Assertion{
		{Type: "body_not_contains", Value: `"state":"ERROR"`},
		{Type: "body_not_contains", Value: `"state":"INCOMPLETE"`},
		{Type: "body_not_contains", Value: "custom failure"},
		{Type: "body_not_contains", Value: `"state":"ERROR"`, LegacyHTMLCheck: true},
	}
	before, _ := json.Marshal(script)
	review, err := Analyze(script, AnalyzeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(script)
	if string(before) != string(after) || review.NativePreview != nil {
		t.Fatal("generic analysis rewrote the executable draft")
	}
	review, err = Analyze(script, AnalyzeOptions{BuildNative: true})
	if err != nil {
		t.Fatal(err)
	}
	assertions := review.NativePreview.Script.Actions[0].Assertions
	if len(assertions) != 3 || assertions[0].Value != "custom failure" || !assertions[1].LegacyHTMLCheck || assertions[2].Type != "aura_actions_success" {
		t.Fatalf("custom assertions changed: %#v", assertions)
	}
}

func TestAuraBootstrapAndNonAuraRequestsDoNotGetBatchAssertion(t *testing.T) {
	script := &dsl.Script{Name: "Bootstrap", Protocol: "http", Actions: []dsl.Action{
		{Type: "GET", Method: "GET", Target: "https://example.lightning.force.com/auraFW/javascript.js"},
		{Type: "POST", Method: "POST", Target: "https://example.my.salesforce.com/services/data/v60.0/sobjects/Account", Body: `{"Name":"Example"}`},
	}}
	review, err := Analyze(script, AnalyzeOptions{BuildNative: true})
	if err != nil {
		t.Fatal(err)
	}
	if review.AuraOutcomes != nil {
		t.Fatalf("non-batch traffic inspected: %#v", review.AuraOutcomes)
	}
	for _, action := range review.NativePreview.Script.Actions {
		if hasAuraAssertion(action) || action.Metadata["execution_blocker"] != nil {
			t.Fatal("non-Aura request gated by Aura analyzer")
		}
	}
}

func hasAuraAssertion(action dsl.Action) bool {
	for _, assertion := range action.Assertions {
		if assertion.Type == "aura_actions_success" {
			return true
		}
	}
	return false
}

func assertAuraReportValueFree(t *testing.T, report *AuraOutcomeReview, privateValues ...string) {
	t.Helper()
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range privateValues {
		if strings.Contains(string(data), value) {
			t.Fatalf("structural report disclosed source value %q", value)
		}
	}
}

func TestAuraGenericSequenceReviewPreservesMetadataAndOmitsUncertainPaths(t *testing.T) {
	child := auraTestScript(syntheticAuraRequest).Actions[0]
	child.Metadata = map[string]any{"existing": "retained"}
	script := &dsl.Script{Name: "Sequence", Protocol: "http", Actions: []dsl.Action{{Type: "http_sequence", Metadata: map[string]any{"source_transaction": "Example"}, Params: map[string]any{"requests": []dsl.Action{child}}}}}
	before, _ := json.Marshal(script)
	if _, err := Analyze(script, AnalyzeOptions{}); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(script)
	if string(before) != string(after) {
		t.Fatal("generic sequence analysis mutated child metadata")
	}
	script.Actions[0].Params["requests"] = []any{nil, child}
	review, err := Analyze(script, AnalyzeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if review.AuraOutcomes.Batches[0].ActionPath != "" {
		t.Fatal("source action path guessed after invalid request filtering")
	}
}

func TestAuraBaselineResponseLimitsRetainKnownRequestCounts(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		code           aura.Code
	}{
		{"byte limit", strings.Repeat("x", aura.MaxEnvelopeBytes+1), aura.ByteLimit},
		{"depth limit", `{"actions":[{"id":"first;a","state":"SUCCESS","returnValue":` + strings.Repeat("[", aura.MaxDepth+1) + `0` + strings.Repeat("]", aura.MaxDepth+1) + `},{"id":"second;a","state":"SUCCESS"}]}`, aura.DepthLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			refs := flattenRequestActions(auraTestScript(syntheticAuraRequest))
			refs[0].HARID = 1
			review := inspectAuraOutcomes(refs, []harEntry{{ID: 1, RequestBody: syntheticAuraRequest, ResponseBody: tc.response}})
			if review.Complete || review.RequestActions != 2 || review.IncompleteBatches != 1 || review.MissingResponses != 0 || review.IssueCounts[tc.code] != 1 || !review.Batches[0].Analysis.HasResponse {
				t.Fatalf("response limit silently discarded known evidence: %#v", review)
			}
		})
	}
}

func TestAuraNestedControlRequestsHaveAssertionsAndDurableGates(t *testing.T) {
	valid := auraTestScript(syntheticAuraRequest).Actions[0]
	invalid := auraTestScript(`{"actions":[{"descriptor":"example"}]}`).Actions[0]
	for _, tc := range []struct {
		kind                   string
		control                *dsl.ControlFlow
		validPath, invalidPath string
	}{
		{"control_loop", &dsl.ControlFlow{Count: 1, Steps: []dsl.Action{valid, invalid}}, "steps[0]", "steps[1]"},
		{"control_if", &dsl.ControlFlow{Steps: []dsl.Action{valid}, Else: []dsl.Action{invalid}}, "steps[0]", "else[0]"},
		{"control_switch", &dsl.ControlFlow{Cases: []dsl.SwitchCase{{Steps: []dsl.Action{valid}}}, Default: []dsl.Action{invalid}}, "cases[0].steps[0]", "default[0]"},
		{"control_parallel", &dsl.ControlFlow{Branches: []dsl.ParallelBranch{{Name: "first", Steps: []dsl.Action{valid}}, {Name: "second", Steps: []dsl.Action{invalid}}}}, "branches[0].steps[0]", "branches[1].steps[0]"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			script := &dsl.Script{Name: "Nested Aura", Protocol: "http", Actions: []dsl.Action{{Type: tc.kind, Control: tc.control, Metadata: map[string]any{"source_transaction": "Nested example"}}}}
			before, _ := json.Marshal(script)
			response := `{"actions":[{"id":"first;a","state":"SUCCESS"},{"id":"second;a","state":"SUCCESS"}]}`
			review, err := Analyze(script, AnalyzeOptions{BuildNative: true, HARData: auraTestHAR(t, syntheticAuraRequest, response)})
			if err != nil {
				t.Fatal(err)
			}
			outcomes := review.AuraOutcomes
			if outcomes.TotalBatches != 2 || outcomes.Complete || outcomes.InvalidRequests != 1 || outcomes.Batches[0].ActionPath != "actions[0].control."+tc.validPath || outcomes.Batches[1].ActionPath != "actions[0].control."+tc.invalidPath {
				t.Fatalf("nested Aura review incomplete: %#v", outcomes)
			}
			after, _ := json.Marshal(script)
			if string(before) != string(after) {
				t.Fatal("nested generic inspection/native cloning mutated source")
			}
			encoded, err := json.Marshal(review.NativePreview.Script)
			if err != nil {
				t.Fatal(err)
			}
			var reopened dsl.Script
			if err := json.Unmarshal(encoded, &reopened); err != nil {
				t.Fatal(err)
			}
			refs := flattenRequestActions(&reopened)
			if len(refs) != 2 || !hasAuraAssertion(*refs[0].Action) || hasAuraAssertion(*refs[1].Action) || refs[0].Transaction != "Nested example" {
				t.Fatal("nested assertion/inherited transaction did not survive reopen")
			}
			findings, err := importreview.FindingsFromScript(&reopened)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, finding := range findings {
				if finding.Code == "salesforce_aura_request_requires_review" && finding.ActionPath == "actions[0].control."+tc.invalidPath && finding.Gate == importreview.GateExecution {
					found = true
				}
			}
			if !found {
				t.Fatalf("nested durable malformed request gate missing: %#v", findings)
			}
		})
	}
}

func TestAuraNestedHTTPSequenceRequestsPreserveExactRewritePaths(t *testing.T) {
	valid := auraTestScript(syntheticAuraRequest).Actions[0]
	invalid := auraTestScript(`{"actions":[{"descriptor":"example"}]}`).Actions[0]
	script := &dsl.Script{Name: "Nested sequence", Protocol: "http", Actions: []dsl.Action{{Type: "control_loop", Control: &dsl.ControlFlow{Count: 1, Steps: []dsl.Action{{Type: "http_sequence", Params: map[string]any{"requests": []any{valid, invalid}}}}}}}}
	review, err := Analyze(script, AnalyzeOptions{BuildNative: true})
	if err != nil {
		t.Fatal(err)
	}
	if review.AuraOutcomes.TotalBatches != 2 || review.AuraOutcomes.Batches[1].ActionPath != "actions[0].control.steps[0].params.requests[1]" {
		t.Fatalf("nested sequence request paths lost: %#v", review.AuraOutcomes)
	}
	encoded, err := json.Marshal(review.NativePreview.Script)
	if err != nil {
		t.Fatal(err)
	}
	var reopened dsl.Script
	if err := json.Unmarshal(encoded, &reopened); err != nil {
		t.Fatal(err)
	}
	refs := flattenRequestActions(&reopened)
	if len(refs) != 2 || !hasAuraAssertion(*refs[0].Action) || refs[1].Action.Metadata["import_review"] == nil {
		t.Fatal("nested sequence mutations detached from native output")
	}
}

func TestAuraRequestTraversalBoundsFailClosed(t *testing.T) {
	leaf := auraTestScript(syntheticAuraRequest).Actions[0]
	deep := leaf
	for i := 0; i < dsl.MaxScriptActionDepth; i++ {
		deep = dsl.Action{Type: "control_loop", Control: &dsl.ControlFlow{Steps: []dsl.Action{deep}}}
	}
	for _, script := range []*dsl.Script{
		{Name: "Deep", Protocol: "http", Actions: []dsl.Action{deep}},
		{Name: "Too many", Protocol: "http", Actions: make([]dsl.Action, dsl.MaxScriptActionDefinitions+1)},
		{Name: "Too many sequence requests", Protocol: "http", Actions: []dsl.Action{{Type: "http_sequence", Params: map[string]any{"requests": make([]any, dsl.MaxScriptActionDefinitions)}}}},
	} {
		if review, err := Analyze(script, AnalyzeOptions{BuildNative: true}); err == nil || review != nil {
			t.Fatal("partial traversal silently claimed complete review")
		}
	}
}

func TestNonAuraDescriptorPayloadsUseConcreteAPIRoutesAndDoNotGate(t *testing.T) {
	for _, tc := range []struct{ path, kind string }{
		{"/services/data/v60.0/sobjects/Account", "rest_api"},
		{"/services/data/v60.0/ui-api/records/one", "ui_api"},
		{"/services/apexrest/AuraExample", "apex_rest"},
		{"/services/data/v60.0/composite", "composite"},
		{"/services/data/v60.0/graphql", "graphql"},
		{"/business", "generic"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			body := `{"Name":"Example","descriptor":"legitimate business field","aura.context":"ordinary value"}`
			script := auraTestScript(body)
			script.Actions[0].Target = "https://example.my.salesforce.com" + tc.path
			if kind := classifySalesforceAction(&script.Actions[0], script.Actions[0].Target); kind != tc.kind {
				t.Fatalf("classification=%s, want%s", kind, tc.kind)
			}
			review, err := Analyze(script, AnalyzeOptions{BuildNative: true})
			if err != nil {
				t.Fatal(err)
			}
			if review.AuraOutcomes != nil || hasAuraAssertion(review.NativePreview.Script.Actions[0]) || review.NativePreview.Script.Actions[0].Metadata["execution_blocker"] != nil {
				t.Fatal("ordinary descriptor payload acquired an Aura gate or assertion")
			}
		})
	}
	for _, body := range []string{syntheticAuraRequest, "message=" + url.QueryEscape(syntheticAuraRequest), "aura.context=%7B%7D&message=%7Bbad"} {
		if !hasAuraEnvelopeEvidence(body) {
			t.Fatal("specific Aura envelope/form evidence no longer recognized")
		}
	}
}

func TestAuraGlobalDetailRetentionPreservesCountsLateFailuresAndDisclosure(t *testing.T) {
	for _, allErrors := range []bool{false, true} {
		t.Run(fmt.Sprint(allErrors), func(t *testing.T) {
			requests := make([]map[string]any, aura.MaxActions)
			responses := make([]map[string]any, aura.MaxActions)
			for i := range requests {
				id := fmt.Sprintf("synthetic-%d", i)
				requests[i] = map[string]any{"id": id, "descriptor": "synthetic"}
				responses[i] = map[string]any{"id": id, "state": "SUCCESS"}
			}
			request, _ := json.Marshal(map[string]any{"actions": requests})
			refs := make([]actionRef, maxAuraReviewBatches)
			entries := make([]harEntry, maxAuraReviewBatches)
			script := &dsl.Script{Name: "Bounded outcome review", Protocol: "http", Actions: make([]dsl.Action, maxAuraReviewBatches)}
			for i := range refs {
				for j := range responses {
					state := "SUCCESS"
					if allErrors || (i == len(refs)-1 && j == len(responses)-1) {
						state = "ERROR"
					}
					responses[j]["state"] = state
				}
				response, _ := json.Marshal(map[string]any{"actions": responses})
				script.Actions[i] = auraTestScript(string(request)).Actions[0]
				refs[i] = actionRef{Kind: "aura", Method: "POST", HARID: i + 1, Sequence: i + 1, Action: &script.Actions[i], ActionPath: fmt.Sprintf("actions[%d]", i)}
				entries[i] = harEntry{ID: i + 1, RequestBody: string(request), ResponseBody: string(response)}
			}
			outcomes := inspectAuraOutcomes(refs, entries)
			total := maxAuraReviewBatches * aura.MaxActions
			if !outcomes.Complete || outcomes.DetailsComplete || outcomes.MatchedActions != total || outcomes.OutcomesOmitted != total-maxAuraOutcomeDetails || outcomes.OmittedBatches != 0 {
				t.Fatalf("full counts/detail omissions lost: %#v", outcomes)
			}
			retainedOutcomes, retainedIssues := 0, 0
			for _, batch := range outcomes.Batches {
				retainedOutcomes += len(batch.Analysis.Outcomes)
				retainedIssues += len(batch.Analysis.Issues) + len(batch.RequestIssues)
			}
			if retainedOutcomes != maxAuraOutcomeDetails || retainedIssues > maxAuraIssueDetails {
				t.Fatal("global retained detail budget exceeded")
			}
			if !allErrors {
				last := outcomes.Batches[len(outcomes.Batches)-1]
				if len(last.Analysis.Outcomes) != 1 || last.Analysis.Outcomes[0].Ordinal != aura.MaxActions || last.Analysis.Outcomes[0].State != aura.Error || outcomes.StateCounts[aura.Error] != 1 {
					t.Fatal("late failure displaced by earlier successful detail")
				}
			} else if outcomes.StateCounts[aura.Error] != total || outcomes.IssuesOmitted != total-maxAuraIssueDetails || outcomes.IssueCounts[aura.NonSuccessState] != total {
				t.Fatal("failed aggregate counts did not exceed retained detail correctly")
			}
			script.Metadata = &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Source: "salesforce", Preserved: map[string]any{"salesforce_review": &Review{Detected: true, AuraOutcomes: outcomes}}}}
			projected, err := scriptdisclosure.Project(script)
			if err != nil || projected == nil {
				t.Fatalf("otherwise supported import exceeds real disclosure budget: %v", err)
			}
			encoded, err := json.Marshal(projected.Metadata.Import.Preserved["salesforce_review"])
			if err != nil {
				t.Fatal(err)
			}
			var reopened Review
			if err := json.Unmarshal(encoded, &reopened); err != nil {
				t.Fatal(err)
			}
			if reopened.AuraOutcomes.MatchedActions != total || reopened.AuraOutcomes.OutcomesOmitted != outcomes.OutcomesOmitted {
				t.Fatal("aggregate counts/omissions lost through disclosure/save projection")
			}
		})
	}
}

func TestAuraGlobalIssueRetentionAlsoBoundsExecutableRequestIssues(t *testing.T) {
	review := &AuraOutcomeReview{Batches: make([]AuraOutcomeBatch, 32)}
	for i := range review.Batches {
		review.Batches[i].RequestIssues = make([]aura.Issue, 256)
		review.Batches[i].Analysis.Issues = make([]aura.Issue, 256)
	}
	retainAuraReviewDetails(review)
	retained := 0
	for _, batch := range review.Batches {
		retained += len(batch.RequestIssues) + len(batch.Analysis.Issues)
	}
	if retained != maxAuraIssueDetails || review.DetailsComplete || review.RequestIssuesOmitted != 8192-maxAuraIssueDetails || review.IssuesOmitted != 8192 {
		t.Fatal("executable and captured issue lists exceeded shared retained issue budget")
	}
}

func TestAuraEscapedEndpointPathStillGatesMalformedRequest(t *testing.T) {
	script := auraTestScript(`{"actions":[{"descriptor":"synthetic"}]}`)
	script.Actions[0].Target = "https://example.lightning.force.com/%61ura"
	review, err := Analyze(script, AnalyzeOptions{BuildNative: true})
	if err != nil {
		t.Fatal(err)
	}
	if review.AuraOutcomes == nil || review.AuraOutcomes.InvalidRequests != 1 || review.NativePreview.Script.Actions[0].Metadata["execution_blocker"] == nil {
		t.Fatal("escaped endpoint path silently bypassed Aura review")
	}
}

func TestAuraEncodedWireBodiesHaveNativeAssertionsWithoutChangingSource(t *testing.T) {
	for _, encoding := range []string{"base64", " BASE64 ", "windows-1252"} {
		for _, withHAR := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/har=%t", encoding, withHAR), func(t *testing.T) {
				body := syntheticAuraRequest
				if strings.EqualFold(strings.TrimSpace(encoding), "base64") {
					body = " " + base64.StdEncoding.EncodeToString([]byte(body)) + "\n"
				}
				script := auraTestScript(body)
				script.Actions[0].BodyEncoding = encoding
				var source []byte
				if withHAR {
					source = auraTestHAR(t, syntheticAuraRequest, `{"actions":[{"id":"second;a","state":"SUCCESS"},{"id":"first;a","state":"SUCCESS"}]}`)
				}
				review, err := Analyze(script, AnalyzeOptions{BuildNative: true, HARData: source})
				if err != nil {
					t.Fatal(err)
				}
				outcomes := review.AuraOutcomes
				if outcomes == nil || outcomes.InvalidRequests != 0 || !outcomes.Batches[0].RequestValid || outcomes.RequestActions != 2 || outcomes.Complete != withHAR || outcomes.Batches[0].Analysis.HasResponse != withHAR {
					t.Fatalf("wire request admission differs from replay: %#v", outcomes)
				}
				native := review.NativePreview.Script.Actions[0]
				if !hasAuraAssertion(native) || native.Body != body || native.BodyEncoding != encoding || script.Actions[0].Body != body || script.Actions[0].BodyEncoding != encoding {
					t.Fatal("wire assertion or authored body encoding was lost")
				}
				findings, err := importreview.FindingsFromScript(review.NativePreview.Script)
				if err != nil {
					t.Fatal(err)
				}
				for _, finding := range findings {
					if finding.Code == "salesforce_aura_request_requires_review" {
						t.Fatal("valid encoded wire request received a malformed-request gate")
					}
				}
			})
		}
	}
}

func TestAuraEncodedEnvelopeEvidenceRespectsConcreteAPIClassification(t *testing.T) {
	body := base64.StdEncoding.EncodeToString([]byte(syntheticAuraRequest))
	for _, target := range []struct{ path, kind string }{
		{"/custom", "aura"},
		{"/services/data/v60.0/sobjects/Account", "rest_api"},
	} {
		action := &dsl.Action{Body: body, BodyEncoding: "base64"}
		if got := classifySalesforceAction(action, "https://example.test"+target.path); got != target.kind {
			t.Fatalf("%s classification = %s, want %s", target.path, got, target.kind)
		}
	}
}

func TestAuraEncodedWireBodyFailuresAreSafeAndDurablyGated(t *testing.T) {
	for _, tc := range []struct {
		name, body, encoding string
		code                 aura.Code
	}{
		{"invalid base64", "PRIVATE_BODY%", "base64", aura.InvalidEnvelope},
		{"encoded byte limit", strings.Repeat("A", base64.StdEncoding.EncodedLen(aura.MaxEnvelopeBytes)+1), "base64", aura.ByteLimit},
		{"windows non-UTF8 wire JSON", strings.Replace(syntheticAuraRequest, "first;a", "caf\u00e9;a", 1), "windows-1252", aura.InvalidJSON},
		{"windows unrepresentable", strings.Replace(syntheticAuraRequest, "first;a", "first\U0001f600;a", 1), "windows-1252", aura.InvalidEnvelope},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := auraTestScript(tc.body)
			script.Actions[0].BodyEncoding = tc.encoding
			review, err := Analyze(script, AnalyzeOptions{BuildNative: true})
			if err != nil {
				t.Fatal(err)
			}
			batch := review.AuraOutcomes.Batches[0]
			if review.AuraOutcomes.InvalidRequests != 1 || batch.RequestValid || len(batch.RequestIssues) != 1 || batch.RequestIssues[0].Code != tc.code || batch.Analysis.Success || len(batch.Analysis.Issues) != 1 || batch.Analysis.Issues[0].Code != tc.code {
				t.Fatalf("unsafe or mismatched encoded request diagnostics: %#v", batch)
			}
			assertAuraReportValueFree(t, review.AuraOutcomes, "PRIVATE_BODY", "caf\u00e9", "first\U0001f600")
			data, err := json.Marshal(review.NativePreview.Script)
			if err != nil {
				t.Fatal(err)
			}
			var reopened dsl.Script
			if err := json.Unmarshal(data, &reopened); err != nil {
				t.Fatal(err)
			}
			findings, err := importreview.FindingsFromScript(&reopened)
			if err != nil {
				t.Fatal(err)
			}
			gated := false
			for _, finding := range findings {
				gated = gated || finding.Code == "salesforce_aura_request_requires_review" && finding.Gate == importreview.GateExecution
			}
			if !gated || hasAuraAssertion(reopened.Actions[0]) || reopened.Actions[0].Body != tc.body || reopened.Actions[0].BodyEncoding != tc.encoding {
				t.Fatal("failed encoded body lost its durable gate or source representation")
			}
		})
	}
}

func TestAuraWireBodyDecoderBoundsDecodedEnvelopeSeparately(t *testing.T) {
	for _, size := range []int{aura.MaxEnvelopeBytes, aura.MaxEnvelopeBytes + 1} {
		encoded := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", size)))
		body, code := auraActionRequestBody(&dsl.Action{Body: encoded, BodyEncoding: "base64"})
		if size == aura.MaxEnvelopeBytes {
			if code != "" || len(body) != size {
				t.Fatal("bounded decoded envelope was rejected by authored encoded length")
			}
		} else if code != aura.ByteLimit || body != "" {
			t.Fatal("oversized decoded envelope was admitted")
		}
	}
	large := &dsl.Action{Body: strings.Repeat("A", base64.StdEncoding.EncodedLen(aura.MaxEnvelopeBytes)), BodyEncoding: "base64"}
	if boundedAuraActionWorkBytes(large) <= len(large.Body) {
		t.Fatal("wire-body decode work was not charged in the review budget")
	}
}

// A recorded framework error is a known failure of the whole batch: the batch
// counts as complete evidence, and the review says the recording failed there.
func TestAuraBaselineReportsRecordedFrameworkErrors(t *testing.T) {
	body := "message=" + url.QueryEscape(syntheticAuraRequest)
	response := `*/{"exceptionEvent":true,"event":{"descriptor":"markup://aura:invalidSession"}}/*ERROR*/`
	review, err := Analyze(auraTestScript(body), AnalyzeOptions{BuildNative: true, HARData: auraTestHAR(t, body, response)})
	if err != nil {
		t.Fatal(err)
	}
	outcomes := review.AuraOutcomes
	if outcomes == nil || outcomes.CompleteBatches != 1 || outcomes.IssueCounts[aura.InvalidSession] != 1 || outcomes.RecordedFailureActions != 0 {
		t.Fatalf("outcomes = %#v", outcomes)
	}
	if !strings.Contains(strings.Join(review.Warnings, "\n"), "1 captured Aura response(s) were framework errors (invalid session: 1") {
		t.Fatalf("warnings = %v", review.Warnings)
	}
}

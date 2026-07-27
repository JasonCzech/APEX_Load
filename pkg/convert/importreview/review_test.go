package importreview

import (
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestEvaluateReadinessStates(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input EvaluationInput
		state State
		draft bool
	}{
		{name: "ready", state: Ready, draft: true},
		{
			name: "review",
			input: EvaluationInput{Findings: []Finding{{
				Code: "wire_review", Gate: GatePromotion, AcknowledgementRequired: true,
			}}},
			state: ReviewRequired,
			draft: true,
		},
		{
			name: "execution blocker",
			input: EvaluationInput{Findings: []Finding{{
				Code: "missing_variable", Gate: GateExecution,
			}}},
			state: Blocked,
			draft: true,
		},
		{
			name:  "validation blocker",
			input: EvaluationInput{ValidationIssues: []string{"script is invalid"}},
			state: Blocked,
			draft: false,
		},
		{
			name:  "binding blocker",
			input: EvaluationInput{UnresolvedBindings: 2},
			state: Blocked,
			draft: true,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := Evaluate(test.input)
			if got.State != test.state || got.DraftValid != test.draft {
				t.Fatalf("Evaluate() = %#v, want state=%q draft=%t", got, test.state, test.draft)
			}
			if got.State == ReviewRequired && (!got.ReplayReady || got.PromotionReady) {
				t.Fatalf("promotion-only review must remain replayable: %#v", got)
			}
			if got.State == Blocked && (got.ReplayReady || got.PromotionReady) {
				t.Fatalf("blocked result advertised replay/promotion readiness: %#v", got)
			}
		})
	}
}

func TestBlockerSeverityFailsClosedWithoutExplicitGate(t *testing.T) {
	t.Parallel()
	finding := Finding{Code: "unsafe", Severity: diag.Blocker}
	got := Evaluate(EvaluationInput{Findings: []Finding{finding}})
	if got.State != Blocked || !reflect.DeepEqual(got.BlockingCodes, []string{"unsafe"}) {
		t.Fatalf("readiness = %#v", got)
	}
	action := &dsl.Action{}
	ApplyToAction(action, []Finding{finding})
	if action.Metadata["execution_blocker"] != "unsafe" {
		t.Fatalf("blocker severity was not persisted as an execution gate: %#v", action.Metadata)
	}
	review, ok := action.Metadata["import_review"].(map[string]any)
	if !ok ||
		review["version"] != 1 ||
		review["source"] != "import" ||
		!reflect.DeepEqual(review["reason_codes"], []string{"unsafe"}) ||
		review["resolution"] != "replace_behavior_and_remove_gate" {
		t.Fatalf("durable import review = %#v", action.Metadata["import_review"])
	}
}

func TestApplyToActionWritesCompatibilityMetadataAndPreservesExistingValues(t *testing.T) {
	t.Parallel()
	action := &dsl.Action{Metadata: map[string]any{"source_profile": "fixture", "execution_blocker": "existing"}}
	ApplyToAction(action, []Finding{
		{Code: "review_two", Gate: GatePromotion},
		{Code: "review_one", Gate: GatePromotion},
		{Code: "new_execution", Gate: GateExecution},
	})
	if action.Metadata["source_profile"] != "fixture" {
		t.Fatalf("unrelated metadata was changed: %#v", action.Metadata)
	}
	if action.Metadata["execution_blocker"] != "existing" {
		t.Fatalf("first deterministic execution blocker = %#v", action.Metadata["execution_blocker"])
	}
	if action.Metadata["review_gate"] != "review_one" ||
		action.Metadata["review_required"] != true ||
		action.Metadata["promotion_blocker"] != true {
		t.Fatalf("review metadata = %#v", action.Metadata)
	}
}

func TestApplyToScriptAndFindingsFromScriptTraverseNestedActions(t *testing.T) {
	t.Parallel()
	script := &dsl.Script{
		Actions: []dsl.Action{{
			Name: "parent",
			Control: &dsl.ControlFlow{
				Steps: []dsl.Action{{Name: "nested"}},
			},
		}},
	}
	path := "actions[0].control.steps[0]"
	err := ApplyToScript(script, []Finding{{
		Code:       "nested_missing",
		ActionPath: path,
		Gate:       GateExecution,
	}})
	if err != nil {
		t.Fatal(err)
	}
	nested := script.Actions[0].Control.Steps[0]
	if nested.Metadata["execution_blocker"] != "nested_missing" {
		t.Fatalf("nested metadata = %#v", nested.Metadata)
	}
	findings, err := FindingsFromScript(script)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].ActionPath != path || findings[0].Gate != GateExecution {
		t.Fatalf("findings = %#v", findings)
	}
}

func TestApplyToScriptAndFindingsFromScriptTraverseHTTPSequenceChildren(t *testing.T) {
	t.Parallel()
	script := &dsl.Script{Actions: []dsl.Action{{
		Name: "sequence",
		Type: "http_sequence",
		Params: map[string]any{
			"requests": []any{map[string]any{
				"name": "child request",
			}},
		},
	}}}
	path := "actions[0].params.requests[0]"
	err := ApplyToScript(script, []Finding{{
		Code:       "child_review",
		ActionPath: path,
		Source:     "fixture",
		ReasonCode: "unsupported_child_behavior",
		SourceRef:  "fixture.json#/requests/0",
		Gate:       GateExecution,
	}})
	if err != nil {
		t.Fatal(err)
	}
	child := script.Actions[0].Params["requests"].([]any)[0].(map[string]any)
	metadata, _ := child["metadata"].(map[string]any)
	if metadata["execution_blocker"] != "child_review" {
		t.Fatalf("sequence child metadata = %#v", metadata)
	}
	findings, err := FindingsFromScript(script)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 ||
		findings[0].ActionPath != path ||
		findings[0].ReasonCode != "unsupported_child_behavior" ||
		findings[0].Gate != GateExecution {
		t.Fatalf("findings = %#v", findings)
	}
}

func TestApplyToActionWritesAndMergesDurableReviewContract(t *testing.T) {
	t.Parallel()
	action := &dsl.Action{
		Name: "request",
		Metadata: map[string]any{
			"source_profile": "postman",
			"import_review": map[string]any{
				"version":      1,
				"source":       "postman",
				"reason_codes": []any{"unsupported_source_script"},
				"source_refs":  []string{"collection.json#/item/0/event/0"},
				"resolution":   "replace_behavior_and_remove_gate",
			},
		},
	}
	ApplyToAction(action, []Finding{{
		Code:       "import_review_required",
		Severity:   diag.Blocker,
		Source:     "postman",
		ReasonCode: "unsupported_auth_scheme",
		SourceRef:  "collection.json#/item/0/request/auth",
		Gate:       GateExecution,
	}})

	review, ok := action.Metadata["import_review"].(map[string]any)
	if !ok {
		t.Fatalf("import_review = %#v", action.Metadata["import_review"])
	}
	if review["version"] != 1 || review["source"] != "postman" ||
		review["resolution"] != "replace_behavior_and_remove_gate" {
		t.Fatalf("import_review envelope = %#v", review)
	}
	if !reflect.DeepEqual(review["reason_codes"], []string{
		"unsupported_auth_scheme",
		"unsupported_source_script",
	}) {
		t.Fatalf("reason_codes = %#v", review["reason_codes"])
	}
	if !reflect.DeepEqual(review["source_refs"], []string{
		"collection.json#/item/0/event/0",
		"collection.json#/item/0/request/auth",
	}) {
		t.Fatalf("source_refs = %#v", review["source_refs"])
	}
}

func TestFindingsFromScriptRecognizesLegacyStringFlags(t *testing.T) {
	t.Parallel()
	script := &dsl.Script{Init: []dsl.Action{{
		Name: "review",
		Metadata: map[string]any{
			"promotion_blocker": "true",
			"review_gate":       "legacy_review",
		},
	}}}
	findings, err := FindingsFromScript(script)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Code != "legacy_review" || findings[0].Gate != GatePromotion {
		t.Fatalf("findings = %#v", findings)
	}
}

func TestNormalizeFindingsAndDiagnosticsAreDeterministic(t *testing.T) {
	t.Parallel()
	input := []Finding{
		{Code: "b", Detail: " second ", Severity: diag.Warning, Location: "z"},
		{Code: "a", Detail: "first", Severity: diag.Info, Location: "a"},
		{Code: "a", Detail: "first", Severity: diag.Info, Location: "a"},
	}
	normalized := NormalizeFindings(input)
	if len(normalized) != 2 || normalized[0].Code != "a" || normalized[1].Detail != "second" {
		t.Fatalf("normalized = %#v", normalized)
	}
	diagnostics := Diagnostics(input)
	if len(diagnostics) != 2 || diagnostics[0].Element != "a" || diagnostics[1].Element != "b" {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}

func TestNormalizeFindingsUsesTotalDeterministicOrder(t *testing.T) {
	t.Parallel()
	input := []Finding{
		{Code: "same", Detail: "same", Severity: diag.Warning, Gate: GatePromotion},
		{Code: "same", Detail: "same", Severity: diag.Blocker, Gate: GateExecution},
	}
	forward := NormalizeFindings(input)
	reverse := NormalizeFindings([]Finding{input[1], input[0]})
	if !reflect.DeepEqual(forward, reverse) {
		t.Fatalf("normalized order depends on input order:\nforward %#v\nreverse %#v", forward, reverse)
	}
}

func TestEvaluateScriptMergesExplicitAndDurableGates(t *testing.T) {
	t.Parallel()
	script := &dsl.Script{Actions: []dsl.Action{{
		Name: "request",
		Metadata: map[string]any{
			"execution_blocker": "durable",
		},
	}}}
	readiness, err := EvaluateScript(script, EvaluationInput{Findings: []Finding{{
		Code: "review", Gate: GatePromotion,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if readiness.State != Blocked ||
		!reflect.DeepEqual(readiness.BlockingCodes, []string{"durable"}) ||
		!reflect.DeepEqual(readiness.ReviewCodes, []string{"review"}) {
		t.Fatalf("readiness = %#v", readiness)
	}
}

func TestFindingsFromScriptPreservesEveryDurableReason(t *testing.T) {
	t.Parallel()
	script := &dsl.Script{Actions: []dsl.Action{{
		Name: "request",
		Metadata: map[string]any{
			"execution_blocker":  "exec_b",
			"execution_blockers": []any{"exec_a", "exec_b"},
			"review_gate":        "review_b",
			"review_reasons":     []string{"review_a", "review_b"},
		},
	}}}
	findings, err := FindingsFromScript(script)
	if err != nil {
		t.Fatal(err)
	}
	var executionCodes, reviewCodes []string
	for _, finding := range findings {
		switch finding.Gate {
		case GateExecution:
			executionCodes = append(executionCodes, finding.Code)
		case GatePromotion:
			reviewCodes = append(reviewCodes, finding.Code)
		}
	}
	if !reflect.DeepEqual(executionCodes, []string{"exec_a", "exec_b"}) {
		t.Fatalf("execution codes = %#v", executionCodes)
	}
	if !reflect.DeepEqual(reviewCodes, []string{"review_a", "review_b"}) {
		t.Fatalf("review codes = %#v", reviewCodes)
	}
}

func TestApplyToScriptRejectsUnknownActionPaths(t *testing.T) {
	t.Parallel()
	script := &dsl.Script{Actions: []dsl.Action{{Name: "only"}}}
	err := ApplyToScript(script, []Finding{{
		Code:       "misaddressed",
		ActionPath: "actions[9]",
		Gate:       GateExecution,
	}})
	if err == nil || !strings.Contains(err.Error(), "actions[9]") {
		t.Fatalf("ApplyToScript() error = %v", err)
	}
}

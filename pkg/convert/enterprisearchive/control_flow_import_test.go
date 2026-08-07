package enterprisearchive

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestTranslateSourceConditionToCEL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{"atoi greater", `atoi(lr_eval_string("{Error_Count}")) > 0`, `(has(vars.error_count) && var.error_count.matches("^-?[0-9]+$") ? int(var.error_count) : 0) > 0`, true},
		{"atoi greater no spaces", `atoi(lr_eval_string("{SuccessCount}"))>0`, `(has(vars.successcount) && var.successcount.matches("^-?[0-9]+$") ? int(var.successcount) : 0) > 0`, true},
		{"atoi parenthesized", `(atoi(lr_eval_string("{Count}"))) <= 5`, `(has(vars.count) && var.count.matches("^-?[0-9]+$") ? int(var.count) : 0) <= 5`, true},
		{"atoi reversed", `0 < atoi(lr_eval_string("{Count}"))`, `0 < (has(vars.count) && var.count.matches("^-?[0-9]+$") ? int(var.count) : 0)`, true},
		{"atoi truthy", `atoi(lr_eval_string("{Flag}"))`, `(has(vars.flag) && var.flag.matches("^-?[0-9]+$") ? int(var.flag) : 0) != 0`, true},
		{"negated", `!(atoi(lr_eval_string("{Count}")) > 0)`, `!((has(vars.count) && var.count.matches("^-?[0-9]+$") ? int(var.count) : 0) > 0)`, true},
		{"strcmp equal", `strcmp(lr_eval_string("{role}"), "admin") == 0`, `(has(vars.role) ? var.role : "{role}") == "admin"`, true},
		{"strcmp not equal", `strcmp(lr_eval_string("{role}"), "admin") != 0`, `(has(vars.role) ? var.role : "{role}") != "admin"`, true},
		{"strcmp reversed args", `strcmp("admin", lr_eval_string("{role}")) == 0`, `(has(vars.role) ? var.role : "{role}") == "admin"`, true},
		{"strstr found", `strstr(lr_eval_string("{body}"), "OK") != NULL`, `(has(vars.body) ? var.body : "{body}").contains("OK")`, true},
		{"strstr missing", `strstr(lr_eval_string("{body}"), "OK") == NULL`, `!((has(vars.body) ? var.body : "{body}").contains("OK"))`, true},
		{"unrecognized C variable", `HTTP_rc == 200`, ``, false},
		{"unrecognized call", `my_helper() > 0`, ``, false},
		{"empty", ``, ``, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := translateSourceConditionToCEL(tc.in)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v (got %q)", ok, tc.ok, got)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
			if ok {
				if err := dsl.ValidateConditionExpression(got); err != nil {
					t.Errorf("translated expression does not validate: %v", err)
				}
			}
		})
	}
}

func TestTranslateSourceLoopHeader(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		wantCount int
		wantExpr  string
		ok        bool
	}{
		{"count from param 1..<=N", `for (ctn_count=1; ctn_count<=(atoi(lr_eval_string("{Req_CTN}")));ctn_count++)`, 0, `int(var.loop_index) <= (has(vars.req_ctn) && var.req_ctn.matches("^-?[0-9]+$") ? int(var.req_ctn) : 0)`, true},
		{"count from param 0..<N", `for (i = 0; i < atoi(lr_eval_string("{Total}")); i++)`, 0, `int(var.loop_index) <= (has(vars.total) && var.total.matches("^-?[0-9]+$") ? int(var.total) : 0)`, true},
		{"count from param 0..<=N", `for (i = 0; i <= atoi(lr_eval_string("{Total}")); i++)`, 0, `int(var.loop_index) <= (has(vars.total) && var.total.matches("^-?[0-9]+$") ? int(var.total) : 0) + 1`, true},
		{"numeric bound", `for (i = 0; i < 5; i++)`, 5, ``, true},
		{"numeric bound inclusive", `for (i = 1; i <= 3; i++)`, 3, ``, true},
		{"while recognized", `while (atoi(lr_eval_string("{More}")) > 0)`, 0, `(has(vars.more) && var.more.matches("^-?[0-9]+$") ? int(var.more) : 0) > 0`, true},
		{"while unrecognized", `while (keep_going)`, 0, ``, false},
		{"mismatched loop var", `for (i = 0; j < 5; i++)`, 0, ``, false},
		{"decrementing", `for (i = 5; i > 0; i--)`, 0, ``, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := translateSourceLoopHeader(tc.in)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v (got %+v)", ok, tc.ok, got)
			}
			if !ok {
				return
			}
			if got.Count != tc.wantCount || got.ConditionExpr != tc.wantExpr {
				t.Errorf("got %+v, want count %d expr %q", got, tc.wantCount, tc.wantExpr)
			}
		})
	}
}

func TestASTParserCapturesFlowContext(t *testing.T) {
	source := `int Action()
{
	web_url("always", "URL=https://example.test/a", LAST);
	if (atoi(lr_eval_string("{SuccessCount}")) > 0)
	{
		lr_log_message("ok");
	}
	else
	{
		lr_error_message("bad %s", "state");
	}
	for (i=1; i<=(atoi(lr_eval_string("{Req_CTN}")));i++)
	{
		web_url("looped", "URL=https://example.test/loop", LAST);
		if (atoi(lr_eval_string("{Error_Count}")) > 0)
		{
			lr_log_message("loop error");
		}
	}
	return 0;
}`
	parsed, err := ParseActionFileAST("Action.c", source)
	if err != nil {
		t.Fatalf("ParseActionFileAST: %v", err)
	}

	byName := map[string]ParsedFunction{}
	for _, fn := range parsed.Functions {
		byName[fn.StepName] = fn
	}

	always := byName["always"]
	if len(always.Conditions) != 0 || len(always.Loops) != 0 {
		t.Errorf("top-level call must have no flow context: %+v / %+v", always.Conditions, always.Loops)
	}

	ok := byName["ok"]
	if len(ok.Conditions) != 1 || !strings.Contains(ok.Conditions[0], "SuccessCount") {
		t.Errorf("then-branch conditions = %v", ok.Conditions)
	}

	var bad ParsedFunction
	for _, fn := range parsed.Functions {
		if fn.Name == "lr_error_message" {
			bad = fn
		}
	}
	if len(bad.Conditions) != 1 || !strings.HasPrefix(bad.Conditions[0], "!(") {
		t.Errorf("else-branch condition must be pre-negated: %v", bad.Conditions)
	}

	looped := byName["looped"]
	if len(looped.Loops) != 1 || !strings.Contains(looped.Loops[0], "Req_CTN") {
		t.Errorf("loop body call loops = %v", looped.Loops)
	}
	if len(looped.Conditions) != 0 {
		t.Errorf("loop body call without if must have no conditions: %v", looped.Conditions)
	}

	var loopError ParsedFunction
	for _, fn := range parsed.Functions {
		if fn.Name == "lr_log_message" && fn.StepName == "loop error" {
			loopError = fn
		}
	}
	if len(loopError.Loops) != 1 || len(loopError.Conditions) != 1 {
		t.Errorf("nested call must carry both loop and condition: loops=%v conds=%v", loopError.Loops, loopError.Conditions)
	}
}

func convertFlowSource(t *testing.T, source string) *ConvertResult {
	t.Helper()
	parsed, err := ParseActionFile("Action.c", source)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{ScriptName: "Flow"})
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}
	return result
}

func TestConvertStructuresIfElse(t *testing.T) {
	result := convertFlowSource(t, `int Action()
{
	web_url("first", "URL=https://example.test/a", LAST);
	if (atoi(lr_eval_string("{SuccessCount}")) > 0)
	{
		web_url("when_ok", "URL=https://example.test/ok", LAST);
	}
	else
	{
		web_url("when_bad", "URL=https://example.test/bad", LAST);
	}
	web_url("last", "URL=https://example.test/z", LAST);
	return 0;
}`)

	actions := result.Script.Actions
	if len(actions) != 3 {
		names := make([]string, 0, len(actions))
		for _, a := range actions {
			names = append(names, a.Name+"/"+a.Type)
		}
		t.Fatalf("actions = %v, want [first, control_if, last]", names)
	}
	wrapper := actions[1]
	if wrapper.Type != "control_if" || wrapper.Control == nil {
		t.Fatalf("middle action = %+v, want control_if", wrapper)
	}
	if wrapper.Control.Condition == nil || wrapper.Control.Condition.Expr != `(has(vars.successcount) && var.successcount.matches("^-?[0-9]+$") ? int(var.successcount) : 0) > 0` {
		t.Errorf("condition = %+v", wrapper.Control.Condition)
	}
	if len(wrapper.Control.Steps) != 1 || wrapper.Control.Steps[0].Name != "when_ok" {
		t.Errorf("steps = %+v", wrapper.Control.Steps)
	}
	if len(wrapper.Control.Else) != 1 || wrapper.Control.Else[0].Name != "when_bad" {
		t.Errorf("else = %+v", wrapper.Control.Else)
	}
	if metadataString(wrapper.Metadata, sourceFlowConditionKey) == "" {
		t.Error("wrapper must keep the original condition text for review")
	}
	for _, step := range wrapper.Control.Steps {
		if metadataString(step.Metadata, sourceFlowConditionExprKey) != "" {
			t.Error("internal flow tags must not leak into the final script")
		}
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) > 0 {
		t.Errorf("structured script does not validate: %v", issues)
	}
}

func TestConvertStructuresParamBoundedLoop(t *testing.T) {
	result := convertFlowSource(t, `int Action()
{
	for (ctn=1; ctn<=(atoi(lr_eval_string("{Req_CTN}")));ctn++)
	{
		lr_start_transaction("T1");
		web_url("loop_a", "URL=https://example.test/a", LAST);
		web_url("loop_b", "URL=https://example.test/b", LAST);
		lr_end_transaction("T1", LR_AUTO);
		if (atoi(lr_eval_string("{Error_Count}")) > 0)
		{
			lr_error_message("err %s", lr_eval_string("{Error_Count}"));
		}
	}
	web_url("after", "URL=https://example.test/done", LAST);
	return 0;
}`)

	actions := result.Script.Actions
	if len(actions) != 2 {
		names := make([]string, 0, len(actions))
		for _, a := range actions {
			names = append(names, a.Name+"/"+a.Type)
		}
		t.Fatalf("actions = %v, want [control_loop, after]", names)
	}
	loop := actions[0]
	if loop.Type != "control_loop" || loop.Control == nil {
		t.Fatalf("first action = %+v, want control_loop", loop)
	}
	if loop.Control.Condition == nil || loop.Control.Condition.Expr != `int(var.loop_index) <= (has(vars.req_ctn) && var.req_ctn.matches("^-?[0-9]+$") ? int(var.req_ctn) : 0)` {
		t.Errorf("loop condition = %+v", loop.Control.Condition)
	}

	// Inside the loop: the transaction-grouped requests and a nested control_if.
	var nestedIf *dsl.Action
	requestSteps := 0
	for i := range loop.Control.Steps {
		step := loop.Control.Steps[i]
		if step.Type == "control_if" {
			nestedIf = &loop.Control.Steps[i]
		}
		if step.Type == "http_sequence" || isHTTPProtocolAction(step) {
			requestSteps++
		}
	}
	if requestSteps == 0 {
		t.Errorf("loop body lost its requests: %+v", loop.Control.Steps)
	}
	if nestedIf == nil {
		t.Fatalf("nested condition not structured: %+v", loop.Control.Steps)
	}
	if nestedIf.Control.Condition == nil || nestedIf.Control.Condition.Expr != `(has(vars.error_count) && var.error_count.matches("^-?[0-9]+$") ? int(var.error_count) : 0) > 0` {
		t.Errorf("nested condition = %+v", nestedIf.Control.Condition)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) > 0 {
		t.Errorf("structured script does not validate: %v", issues)
	}
}

func TestConvertUnrecognizedConditionStaysLinearWithWarning(t *testing.T) {
	result := convertFlowSource(t, `int Action()
{
	if (HTTP_rc == 200)
	{
		web_url("guarded", "URL=https://example.test/g", LAST);
	}
	return 0;
}`)

	actions := result.Script.Actions
	if len(actions) != 1 || actions[0].Type == "control_if" {
		t.Fatalf("unrecognized condition must stay linear: %+v", actions)
	}
	if metadataString(actions[0].Metadata, sourceFlowConditionKey) == "" {
		t.Error("untranslated condition must stay visible in metadata for review")
	}
	found := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "HTTP_rc == 200") && strings.Contains(w, "could not be translated") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected untranslated-condition warning, got %v", result.Warnings)
	}
}

func TestConvertNumericLoopUsesFixedCount(t *testing.T) {
	result := convertFlowSource(t, `int Action()
{
	for (i = 0; i < 3; i++)
	{
		web_url("ping", "URL=https://example.test/ping", LAST);
	}
	return 0;
}`)

	actions := result.Script.Actions
	if len(actions) != 1 || actions[0].Type != "control_loop" {
		t.Fatalf("actions = %+v, want one control_loop", actions)
	}
	if actions[0].Control.Count != 3 {
		t.Errorf("count = %d, want 3", actions[0].Control.Count)
	}
	if actions[0].Control.Condition != nil {
		t.Errorf("fixed-count loop must not carry a condition: %+v", actions[0].Control.Condition)
	}
}

func TestConvertConditionSourceReviewPathsFollowStructure(t *testing.T) {
	result := convertFlowSource(t, `int Action()
{
	if (atoi(lr_eval_string("{Count}")) > 0)
	{
		web_url("guarded", "URL=https://example.test/g", LAST);
	}
	return 0;
}`)

	if result.SourceReview == nil {
		t.Fatal("expected source review")
	}
	found := false
	for _, mapping := range result.SourceReview.Mappings {
		if mapping.Function == "web_url" && strings.Contains(mapping.TargetPath, "control.steps[0]") {
			found = true
		}
	}
	if !found {
		paths := make([]string, 0)
		for _, m := range result.SourceReview.Mappings {
			paths = append(paths, m.Function+" -> "+m.TargetPath)
		}
		t.Errorf("guarded request mapping must point inside the control block; got:\n%s", strings.Join(paths, "\n"))
	}
}

func TestConvertAdjacentIdenticalLoopsStaySeparate(t *testing.T) {
	result := convertFlowSource(t, `int Action()
{
	for (i = 0; i < 3; i++)
	{
		web_url("first_loop", "URL=https://example.test/a", LAST);
	}
	for (i = 0; i < 3; i++)
	{
		web_url("second_loop", "URL=https://example.test/b", LAST);
	}
	return 0;
}`)

	actions := result.Script.Actions
	if len(actions) != 2 || actions[0].Type != "control_loop" || actions[1].Type != "control_loop" {
		names := make([]string, 0, len(actions))
		for _, a := range actions {
			names = append(names, a.Name+"/"+a.Type)
		}
		t.Fatalf("actions = %v, want two separate control_loops (A,A,A,B,B,B order, not A,B interleaved)", names)
	}
	if len(actions[0].Control.Steps) != 1 || len(actions[1].Control.Steps) != 1 {
		t.Errorf("each loop must own exactly its body: %d / %d steps", len(actions[0].Control.Steps), len(actions[1].Control.Steps))
	}
}

func TestConvertAdjacentIdenticalIfsStaySeparate(t *testing.T) {
	result := convertFlowSource(t, `int Action()
{
	if (atoi(lr_eval_string("{Count}")) > 0)
	{
		web_url("first_if", "URL=https://example.test/a", LAST);
	}
	if (atoi(lr_eval_string("{Count}")) > 0)
	{
		web_url("second_if", "URL=https://example.test/b", LAST);
	}
	return 0;
}`)

	actions := result.Script.Actions
	if len(actions) != 2 || actions[0].Type != "control_if" || actions[1].Type != "control_if" {
		names := make([]string, 0, len(actions))
		for _, a := range actions {
			names = append(names, a.Name+"/"+a.Type)
		}
		t.Fatalf("actions = %v, want two separate control_ifs (condition re-evaluated between them)", names)
	}
}

func TestConvertZeroPassLoopDroppedWithWarning(t *testing.T) {
	result := convertFlowSource(t, `int Action()
{
	web_url("before", "URL=https://example.test/before", LAST);
	for (i = 0; i < 0; i++)
	{
		web_url("disabled", "URL=https://example.test/disabled", LAST);
	}
	web_url("after", "URL=https://example.test/after", LAST);
	return 0;
}`)

	for _, a := range result.Script.Actions {
		if a.Type == "control_loop" {
			t.Fatalf("zero-pass loop must be dropped, found %+v", a)
		}
		if strings.Contains(a.Name, "disabled") {
			t.Fatalf("zero-pass loop body must not survive: %+v", a)
		}
	}
	if len(result.Script.Actions) != 2 {
		t.Errorf("surrounding actions must survive: %d actions", len(result.Script.Actions))
	}
	found := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "never executes") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected zero-pass warning, got %v", result.Warnings)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) > 0 {
		t.Errorf("script does not validate: %v", issues)
	}
}

func TestConvertLoopWithBreakStaysLinearWithWarning(t *testing.T) {
	result := convertFlowSource(t, `int Action()
{
	for (i = 0; i < 5; i++)
	{
		web_url("guarded", "URL=https://example.test/g", LAST);
		if (atoi(lr_eval_string("{Done}")) > 0)
		{
			break;
		}
	}
	return 0;
}`)

	for _, a := range result.Script.Actions {
		if a.Type == "control_loop" {
			t.Fatalf("a loop whose body breaks cannot translate to a fixed count; found %+v", a)
		}
	}
	found := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "break/continue/goto/return") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected jump warning, got %v", result.Warnings)
	}
}

func TestConvertForDeclLoopTranslates(t *testing.T) {
	result := convertFlowSource(t, `int Action()
{
	for (int i = 0; i < 4; i++)
	{
		web_url("declared", "URL=https://example.test/d", LAST);
	}
	return 0;
}`)

	actions := result.Script.Actions
	if len(actions) != 1 || actions[0].Type != "control_loop" {
		t.Fatalf("actions = %+v, want one control_loop from C99 for-declaration", actions)
	}
	if actions[0].Control.Count != 4 {
		t.Errorf("count = %d, want 4", actions[0].Control.Count)
	}
}

func TestConvertLargeFixedLoopSetsMaxIterations(t *testing.T) {
	result := convertFlowSource(t, `int Action()
{
	for (i = 0; i < 5000; i++)
	{
		web_url("many", "URL=https://example.test/m", LAST);
	}
	return 0;
}`)

	actions := result.Script.Actions
	if len(actions) != 1 || actions[0].Type != "control_loop" {
		t.Fatalf("actions = %+v, want one control_loop", actions)
	}
	if actions[0].Control.Count != 5000 || actions[0].Control.MaxIterations != 5000 {
		t.Errorf("count/max = %d/%d, want 5000/5000 (engine default cap must not truncate)", actions[0].Control.Count, actions[0].Control.MaxIterations)
	}
}

func TestConvertIfAroundLoopWarnsAboutNestingApproximation(t *testing.T) {
	result := convertFlowSource(t, `int Action()
{
	if (atoi(lr_eval_string("{Enabled}")) > 0)
	{
		for (i = 0; i < 2; i++)
		{
			web_url("inner", "URL=https://example.test/i", LAST);
		}
	}
	return 0;
}`)

	found := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "encloses loop") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected nesting-approximation warning, got %v", result.Warnings)
	}
}

// A translated outer loop whose body OPENS with an untranslated nested loop must
// stay one block: splitting it would run the nested calls once, before the loop,
// instead of once per outer pass.
func TestConvertLoopBodyOpeningWithNestedLoopStaysOneBlock(t *testing.T) {
	result := convertFlowSource(t, `int Action()
{
	for (i = 0; i < 3; i++)
	{
		for (j = 0; j < atoi(lr_eval_string("{Inner}")); j++)
		{
			web_url("inner", "URL=https://example.test/i", LAST);
		}
		web_url("tail", "URL=https://example.test/t", LAST);
	}
	return 0;
}`)

	actions := result.Script.Actions
	if len(actions) != 1 || actions[0].Type != "control_loop" {
		names := make([]string, 0, len(actions))
		for _, action := range actions {
			names = append(names, action.Name+"/"+action.Type)
		}
		t.Fatalf("actions = %v, want a single control_loop covering the whole outer body", names)
	}
	loop := actions[0]
	if loop.Control == nil || loop.Control.Count != 3 {
		t.Fatalf("loop control = %+v, want the outer loop's three passes", loop.Control)
	}
	targets := make([]string, 0, len(loop.Control.Steps))
	for _, step := range loop.Control.Steps {
		targets = append(targets, step.Name)
	}
	if len(loop.Control.Steps) != 2 {
		t.Fatalf("loop steps = %v, want the nested call and the outer-only call", targets)
	}
	// The nested loop is untranslated, so its calls run once per outer pass; the
	// warning tells operators to review metadata.source_loop, which must survive.
	if display := metadataString(loop.Control.Steps[0].Metadata, sourceFlowLoopKey); !strings.Contains(display, ">>") {
		t.Errorf("nested step loop context = %q, want the nested loop chain retained for review", display)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) > 0 {
		t.Errorf("structured script does not validate: %v", issues)
	}
}

// The review's scenario: outer body [A, nested loop, B]. The wrapper covers all
// three, and the nested call keeps its loop chain in metadata so the
// untranslated-loop warning has something to point at.
func TestConvertLoopBodyAroundNestedLoopKeepsNestedContext(t *testing.T) {
	result := convertFlowSource(t, `int Action()
{
	for (i = 0; i < 3; i++)
	{
		web_url("head", "URL=https://example.test/a", LAST);
		for (j = 0; j < atoi(lr_eval_string("{Inner}")); j++)
		{
			web_url("inner", "URL=https://example.test/i", LAST);
		}
		web_url("tail", "URL=https://example.test/t", LAST);
	}
	return 0;
}`)

	actions := result.Script.Actions
	if len(actions) != 1 || actions[0].Type != "control_loop" {
		t.Fatalf("actions = %+v, want a single control_loop", actions)
	}
	steps := actions[0].Control.Steps
	if len(steps) != 3 {
		t.Fatalf("loop steps = %+v, want head, inner and tail in one block", steps)
	}
	nestedContext := metadataString(steps[1].Metadata, sourceFlowLoopKey)
	if !strings.Contains(nestedContext, ">>") {
		t.Errorf("nested step loop context = %q, want the nested loop chain retained for review", nestedContext)
	}
	for _, index := range []int{0, 2} {
		if display := metadataString(steps[index].Metadata, sourceFlowLoopKey); display != "" {
			t.Errorf("step %d loop context = %q, want the wrapper to own the outer loop display", index, display)
		}
	}
}

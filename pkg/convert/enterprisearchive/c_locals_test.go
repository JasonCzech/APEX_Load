package enterprisearchive

import (
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestCLocalCandidatesRequireLiteralWrites(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		want         []string
	}{
		{"polling flag", "int a(){ int done = 0; while(!done){ done = 1; } }", []string{"done"}},
		{"comments and strings ignored", "int a(){ int done = 0; /* done = f(); */ lr_output_message(\"done++\"); done = 1; }", []string{"done"}},
		{"increment", "int a(){ int done = 0; done++; }", nil},
		{"compound", "int a(){ int done = 0; done += 2; }", nil},
		{"computed value", "int a(){ int done = 0; done = atoi(lr_eval_string(\"{p}\")); }", nil},
		{"address taken", "int a(){ int done = 0; f(&done); }", nil},
		{"declared twice", "int a(){ int done = 0; } int b(){ int done = 1; }", nil},
		{"for loop counter", "int a(){ int i = 0; for (i = 0; i < 3; i++) {} }", nil},
		{"comparison is not a write", "int a(){ int done = 0; if (done == 1 && done != 2 && done <= 3) {} }", []string{"done"}},
		{"guarded and else statements", "int a(){ int done = 0; if (x) done = 1; else done = 2; }", []string{"done"}},
		{"write inside a condition", "int a(){ int done = 0; if ((done = 1)) {} }", nil},
		{"write as a call argument", "int a(){ int done = 0; f(done = 1); }", nil},
		{"write in a for header", "int a(){ int done = 0; for (done = 0; x; ) {} }", nil},
		{"chained write", "int a(){ int done = 0; int b = 0; b = done = 1; }", nil},
		{"for-init declaration", "int a(){ for (int done = 0; !done; ) { done = 1; } }", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for name := range cLocalCandidates(tc.source) {
				got = append(got, name)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("candidates = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestExpandCLocalReferences(t *testing.T) {
	locals := map[string]bool{"done": true}
	for in, want := range map[string]string{
		"!done":                    `!(atoi(lr_eval_string("{clocal_done}")))`,
		"while (done == 0)":        `while ((atoi(lr_eval_string("{clocal_done}"))) == 0)`,
		`strcmp("done", x) == 0`:   `strcmp("done", x) == 0`,
		"done(1) && s.done && !d2": "done(1) && s.done && !d2",
	} {
		if got := expandCLocalReferences(in, locals); got != want {
			t.Errorf("expand(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTranslateStrlenConditions(t *testing.T) {
	for in, want := range map[string]string{
		`strlen(lr_eval_string("{c_id}")) != 0`:    `size((has(vars.c_id) ? var.c_id : "{c_id}")) != 0`,
		`strlen(lr_eval_string("{c_id}")) > 3`:     `size((has(vars.c_id) ? var.c_id : "{c_id}")) > 3`,
		`0 == strlen(lr_eval_string("{c_id}"))`:    `0 == size((has(vars.c_id) ? var.c_id : "{c_id}"))`,
		`strlen(lr_eval_string("{c_id}"))`:         `size((has(vars.c_id) ? var.c_id : "{c_id}")) != 0`,
		`!(strlen(lr_eval_string("{c_id}")) != 0)`: `!(size((has(vars.c_id) ? var.c_id : "{c_id}")) != 0)`,
	} {
		got, ok := translateSourceConditionToCEL(in)
		if !ok || got != want {
			t.Errorf("translate(%q) = %q, %v; want %q", in, got, ok, want)
			continue
		}
		if err := dsl.ValidateConditionExpression(got); err != nil {
			t.Errorf("translate(%q) produced invalid CEL: %v", in, err)
		}
	}
}

// The iPad store check-in script polls until an Aura response carries a
// check-in id. Before this translation every step ran once and both branches
// of the found/not-found test ran.
func TestPollingLoopOverCLocalImportsAsBoundedConditionLoop(t *testing.T) {
	source := "Action()\n{\n" +
		"\tint assertionPassed= 0;\n" +
		"\twhile (!assertionPassed){\n" +
		"\t\tweb_reg_save_param(\"c_checkinId02\",\"LB=\\\"checkinId\\\":\\\"\",\"RB=\\\"\",\"NotFound=warning\",LAST);\n" +
		"\t\tweb_url(\"poll\",\"URL=https://example.com/poll\",LAST);\n" +
		"\t\tif (strlen(lr_eval_string(\"{c_checkinId02}\")) != 0) {\n" +
		"\t\t\tassertionPassed = 1;\n" +
		"\t\t\tlr_output_message(\"Assertion passed, existing loop.\");\n" +
		"\t\t}\n" +
		"\t\telse{\n" +
		"\t\t\tlr_output_message(\"Assertion failed, continuing loop.\");\n" +
		"\t\t}\n" +
		"\t}\n" +
		"\tweb_url(\"after\",\"URL=https://example.com/after\",LAST);\n" +
		"\treturn 0;\n}\n"
	parsed, err := ParseActionFile("Action.c", source)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{ScriptName: "polling"})
	if err != nil {
		t.Fatal(err)
	}
	actions := result.Script.Actions
	if len(actions) != 3 {
		t.Fatalf("top-level actions = %d (%v), want init, loop, after", len(actions), actionTypes(actions))
	}
	init := actions[0]
	if init.Type != "source_parameter" || !strings.EqualFold(cLocalTestParam(init, "save_param"), "clocal_assertionPassed") || init.Params["source_string"] != "0" {
		t.Fatalf("initializer = %s %v", init.Type, init.Params)
	}
	loop := actions[1]
	if loop.Type != "control_loop" || loop.Control == nil || loop.Control.Condition == nil ||
		!strings.Contains(strings.ToLower(loop.Control.Condition.Expr), "var.clocal_assertionpassed") || loop.Control.MaxIterations != cLocalLoopMaxIterations {
		t.Fatalf("loop = %s expr=%q max=%d", loop.Type, func() string {
			if loop.Control != nil && loop.Control.Condition != nil {
				return loop.Control.Condition.Expr
			}
			return ""
		}(), loop.Control.MaxIterations)
	}
	if actions[2].Type != "GET" && actions[2].Method != "GET" {
		t.Fatalf("post-loop request = %+v", actions[2])
	}
	var found, notFound bool
	for _, step := range loop.Control.Steps {
		if step.Type != "control_if" || step.Control == nil || step.Control.Condition == nil {
			continue
		}
		expr := step.Control.Condition.Expr
		if !strings.Contains(expr, "size(") {
			continue
		}
		for _, inner := range append(append([]dsl.Action(nil), step.Control.Steps...), step.Control.Else...) {
			if strings.EqualFold(cLocalTestParam(inner, "save_param"), "clocal_assertionPassed") && inner.Params["source_string"] == "1" {
				found = true
			}
			if strings.Contains(inner.Value, "continuing loop") || strings.Contains(cLocalTestParam(inner, "message"), "continuing loop") {
				notFound = true
			}
		}
	}
	if !found || !notFound {
		t.Fatalf("loop steps lack the guarded flag write (%v) or the else branch (%v): %v", found, notFound, actionTypes(loop.Control.Steps))
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("invalid script: %v", issues)
	}
	warned := false
	for _, warning := range result.Warnings {
		if strings.Contains(warning, "has no bound in the source") {
			warned = true
		}
		if strings.Contains(warning, "could not be translated") || strings.Contains(warning, "not a recognized count or condition loop") {
			t.Errorf("untranslated flow warning: %s", warning)
		}
	}
	if !warned {
		t.Errorf("missing unbounded-loop warning: %v", result.Warnings)
	}
}

func cLocalTestParam(action dsl.Action, key string) string {
	value, _ := action.Params[key].(string)
	return value
}

// A local qualifies only inside the function that declares it: another
// function reading a same-named global keeps its untranslated condition.
func TestCLocalsAreScopedToTheirFunction(t *testing.T) {
	source := "Action()\n{\n\tint flag = 0;\n\tflag = 1;\n\treturn 0;\n}\n" +
		"int Other()\n{\n\tif (flag) {\n\t\tweb_url(\"x\",\"URL=https://example.com/x\",LAST);\n\t}\n\treturn 0;\n}\n"
	parsed, err := ParseActionFile("Action.c", source)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, fn := range parsed.Functions {
		seen[fn.sourceFunction] = true
		switch fn.sourceFunction {
		case "Action":
			if !fn.cLocals["flag"] {
				t.Fatalf("%s in Action lacks its local", fn.Name)
			}
		case "Other":
			if fn.cLocals["flag"] {
				t.Fatalf("%s in Other sees Action's local", fn.Name)
			}
		}
	}
	if !seen["Action"] || !seen["Other"] {
		t.Fatalf("parsed functions cover %v, want Action and Other", seen)
	}
}

// A for loop whose bound is a qualified local keeps that bound: no safety cap
// and no "unbounded" warning.
func TestForLoopBoundedByCLocalIsNotCapped(t *testing.T) {
	source := "Action()\n{\n\tint limit = 5000;\n\tint i;\n" +
		"\tfor (i = 1; i <= limit; i++) {\n\t\tweb_url(\"step\",\"URL=https://example.com/step\",LAST);\n\t}\n\treturn 0;\n}\n"
	parsed, err := ParseActionFile("Action.c", source)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{ScriptName: "bounded"})
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range result.Script.Actions {
		if action.Type == "control_loop" && action.Control != nil && action.Control.MaxIterations == cLocalLoopMaxIterations && action.Control.Condition != nil {
			t.Fatalf("for loop over a local bound was capped: %+v", action.Control)
		}
	}
	for _, warning := range result.Warnings {
		if strings.Contains(warning, "has no bound in the source") {
			t.Fatalf("bounded for loop warned as unbounded: %s", warning)
		}
	}
}

package enterprisearchive

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func helperArchive(t *testing.T, files map[string]string) *ConvertResult {
	t.Helper()
	all := map[string]string{
		"Steps/Steps.usr": "[General]\nType=Multi\nActiveTypes=QTWeb\n[Actions]\nvuser_init=vuser_init.c\nAction=Action.c\nvuser_end=vuser_end.c\n",
		"Steps/vuser_end.c": "vuser_end()\n{\n\treturn 0;\n}\n",
	}
	for name, content := range files {
		all["Steps/"+name] = content
	}
	result, err := ConvertZip(zipFilesForTest(t, all), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	return result
}

func flatTargets(actions []dsl.Action) []string {
	var out []string
	for _, action := range actions {
		if action.Target != "" && action.Type != "custom_call" {
			out = append(out, action.Target)
		}
		if action.Control != nil {
			out = append(out, flatTargets(action.Control.Steps)...)
			out = append(out, flatTargets(action.Control.Else)...)
		}
	}
	return out
}

func countType(actions []dsl.Action, actionType string) int {
	count := 0
	for _, action := range actions {
		if action.Type == actionType {
			count++
		}
		if action.Control != nil {
			count += countType(action.Control.Steps, actionType) + countType(action.Control.Else, actionType)
		}
	}
	return count
}

// Business steps defined as C functions in the action file run where they are
// called: init-only steps once in init, and the rest in call order inside the
// transactions around them.
func TestConvertZipInlinesHelperFunctionsAtTheirCallSites(t *testing.T) {
	result := helperArchive(t, map[string]string{
		"vuser_init.c": "void Login();\nvuser_init()\n{\n\tlr_start_transaction(\"01_Login\");\n\tLogin();\n\tlr_end_transaction(\"01_Login\", LR_AUTO);\n\treturn 0;\n}\n",
		"Action.c": `Action()
{
	lr_start_transaction("02_Search");
	Search();
	lr_end_transaction("02_Search", LR_AUTO);
	Checkout();
	return 0;
}

void Login()
{
	web_url("login", "URL=https://example.test/login", LAST);
}

void Checkout()
{
	web_url("cart", "URL=https://example.test/cart", LAST);
	Pay();
}

void Search()
{
	web_url("search", "URL=https://example.test/search", LAST);
}

void Pay()
{
	web_url("pay", "URL=https://example.test/pay", LAST);
}

void Unused()
{
	web_url("unused", "URL=https://example.test/unused", LAST);
}
`,
	})
	script := result.Script
	if got := strings.Join(flatTargets(script.Init), " "); got != "/login" {
		t.Fatalf("init targets = %q, want only the login step", got)
	}
	want := "/search /cart /pay"
	if got := strings.Join(flatTargets(script.Actions), " "); got != want {
		t.Fatalf("action targets = %q, want %q (call order, no uncalled or init-only steps)", got, want)
	}
	if n := countType(script.Init, "custom_call") + countType(script.Actions, "custom_call"); n != 0 {
		t.Fatalf("custom_call actions = %d, want every helper call inlined", n)
	}
}

func TestConvertZipInlinesEachCallOfAHelperUnderItsOwnCondition(t *testing.T) {
	result := helperArchive(t, map[string]string{
		"vuser_init.c": "vuser_init()\n{\n\treturn 0;\n}\n",
		"Action.c": `Action()
{
	if (atoi(lr_eval_string("{retry}")) > 0) {
		Ping();
	}
	Ping();
	return 0;
}

void Ping()
{
	web_url("ping", "URL=https://example.test/ping", LAST);
}
`,
	})
	actions := result.Script.Actions
	if got := len(flatTargets(actions)); got != 2 {
		t.Fatalf("ping requests = %d, want one per call", got)
	}
	conditional := 0
	for _, action := range actions {
		if action.Type == "control_if" && len(flatTargets(action.Control.Steps)) == 1 {
			conditional++
		}
	}
	if conditional != 1 || len(flatTargets(actions[1:])) == 0 {
		t.Fatalf("actions = %#v, want one conditional ping and one unconditional ping", actions)
	}
}

func TestConvertZipKeepsRecursiveHelperCallsForReview(t *testing.T) {
	result := helperArchive(t, map[string]string{
		"vuser_init.c": "vuser_init()\n{\n\treturn 0;\n}\n",
		"Action.c": `Action()
{
	Again();
	return 0;
}

void Again()
{
	web_url("again", "URL=https://example.test/again", LAST);
	Again();
}
`,
	})
	if got := len(flatTargets(result.Script.Actions)); got != 1 {
		t.Fatalf("requests = %d, want the body inlined once", got)
	}
	if countType(result.Script.Actions, "custom_call") != 1 || !strings.Contains(strings.Join(result.Warnings, "\n"), "calls itself") {
		t.Fatalf("warnings = %#v, want the recursive call kept with a warning", result.Warnings)
	}
}

// An action file the run logic never runs can still define functions that a
// scheduled action calls.
func TestConvertZipInlinesHelperFromUnscheduledActionFile(t *testing.T) {
	archive := zipFilesForTest(t, map[string]string{
		"Lib/Lib.usr": "[General]\nType=Multi\nActiveTypes=QTWeb\nDefaultCfg=default.cfg\n[Actions]\nvuser_init=vuser_init.c\nAction=Action.c\nLibrary=Library.c\nvuser_end=vuser_end.c\n[CfgFiles]\nDefault Profile=default.cfg\n[RunLogicFiles]\nDefault Profile=default.usp\n",
		"Lib/default.cfg":  "[General]\nDefaultRunLogic=default.usp\n",
		"Lib/default.usp":  "[RunLogicRunRoot]\nName=\"Run\"\nRunLogicActionOrder=\"Action\"\nRunLogicObjectKind=\"Group\"\n[RunLogicRunRoot:Action]\nName=\"Action\"\nRunLogicObjectKind=\"Action\"\n",
		"Lib/vuser_init.c": "vuser_init()\n{\n\treturn 0;\n}\n",
		"Lib/Action.c":     "Action()\n{\n\tShared();\n\treturn 0;\n}\n",
		"Lib/Library.c":    "Library()\n{\n\treturn 0;\n}\n\nvoid Shared()\n{\n\tweb_url(\"shared\", \"URL=https://example.test/shared\", LAST);\n}\n",
		"Lib/vuser_end.c":  "vuser_end()\n{\n\treturn 0;\n}\n",
	})
	result, err := ConvertZip(archive, ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	if got := strings.Join(flatTargets(result.Script.Actions), " "); got != "/shared" {
		t.Fatalf("action targets = %q, want the helper from the unscheduled file", got)
	}
}

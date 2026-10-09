package enterprisearchive

import (
	"strings"
	"testing"
)

func TestTranslateJavaScriptToC(t *testing.T) {
	source := `function Action()
{
	web.regSaveParamEx({ paramName : 'Token', lb : 'token=', rb : ';', scope : 'Body' });
	web.url(
		{
			name : 'home',
			url : 'http://shop.test/home',
			resource : 0,
			recContentType : 'text/html',
			mode : 'HTML'
		}
	);
	lr.startTransaction('login');
	web.submitData({
		name : 'login',
		action : 'http://shop.test/login',
		method : 'POST',
		itemData : [ { name : 'user', value : '{User}' }, { name : 'pw', value : 'x"y' } ]
	});
	lr.endTransaction('login', lr.AUTO);
	if (lr.evalString('{Mode}') === 'full') {
		web.url({ name : 'full', url : 'http://shop.test/full' });
	} else {
		lr.thinkTime(2);
	}
	for (var i = 0; i < 3; i++) {
		web.url({ name : 'page', url : 'http://shop.test/page' });
	}
	while (parseInt(lr.evalString('{Count}')) > 0) {
		lr.outputMessage('waiting');
	}
	var total = compute();
	web.url({ name : 'computed', url : 'http://shop.test/' + lr.evalString('{Path}') });
	return 0;
}
`
	got, notes, err := translateJavaScriptToC("Action.js", source)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(got, "\n")
	expect := map[int]string{
		1:  "Action()",
		3:  `web_reg_save_param_ex("ParamName=Token", "LB=token=", "RB=;", "Scope=Body", LAST);`,
		4:  `web_url("home", "URL=http://shop.test/home", "Resource=0", "RecContentType=text/html", "Mode=HTML", LAST);`,
		13: `lr_start_transaction("login");`,
		14: `web_submit_data("login", "Action=http://shop.test/login", "Method=POST", ITEMDATA, "Name=user", "Value={User}", ENDITEM, "Name=pw", "Value=x\"y", ENDITEM, LAST);`,
		20: `lr_end_transaction("login", LR_AUTO);`,
		21: `if (strcmp(lr_eval_string("{Mode}"), "full") == 0) {`,
		26: `for (i = 0; i < 3; i++) {`,
		29: `while (atoi(lr_eval_string("{Count}")) > 0) {`,
		32: `js_unsupported_statement("line 32: a statement the JavaScript import does not translate");`,
		33: `js_unsupported_statement("line 33: a computed attribute value ('http://shop.test/' + lr.evalString('{Path}'))");`,
	}
	for line, want := range expect {
		if line > len(lines) || !strings.Contains(lines[line-1], want) {
			t.Errorf("line %d = %q, want it to contain %q\n%s", line, lines[line-1], want, got)
		}
	}
	if !strings.Contains(lines[1], "int i;") {
		t.Errorf("loop variables are declared at the top of the function: %q", lines[1])
	}
	if len(notes) != 2 {
		t.Errorf("notes = %v", notes)
	}
}

func TestTranslateJavaScriptToCRejectsSyntaxErrors(t *testing.T) {
	if _, _, err := translateJavaScriptToC("Action.js", "function Action() { web.url({ name: 'x', }"); err == nil {
		t.Fatal("a JavaScript syntax error must fail the translation")
	}
}

func TestJavaScriptControlFlowImportsAsControlBlocks(t *testing.T) {
	archive := zipFilesForTest(t, map[string]string{
		"S.usr": "[General]\nType=Multi\nScriptLanguage=JavaScript\n[Actions]\nAction=Action.js\n",
		"Action.js": `function Action()
{
	if (lr.evalString('{Mode}') == 'full') {
		web.url({ name : 'full', url : 'http://shop.test/full' });
	}
	for (var i = 0; i < 3; i++) {
		web.url({ name : 'page', url : 'http://shop.test/page' });
	}
	return 0;
}
`,
	})
	result, err := ConvertZip(archive, ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Script == nil || len(result.Script.Actions) != 2 {
		t.Fatalf("script = %+v warnings %v", result.Script, result.Warnings)
	}
	if branch := result.Script.Actions[0]; branch.Type != "control_if" || branch.Control == nil || len(branch.Control.Steps) != 1 {
		t.Fatalf("the if statement must become control_if: %+v", branch)
	}
	if loop := result.Script.Actions[1]; loop.Type != "control_loop" || loop.Control == nil || loop.Control.Count != 3 {
		t.Fatalf("the for loop must become a 3-pass control_loop: %+v", loop)
	}
}

// A statement outside a function becomes a review step at the start of the
// next function, since C has no top-level statements.
func TestTranslateJavaScriptTopLevelStatements(t *testing.T) {
	source, notes, err := translateJavaScriptToC("Action.js", "var shared = compute();\nfunction Action()\n{\n\tweb.url({ name : 'home', url : 'http://shop.test/home' });\n\treturn 0;\n}\n")
	if err != nil || len(notes) != 1 || !strings.Contains(notes[0], "line 1: a top-level statement") {
		t.Fatalf("notes = %v, %v", notes, err)
	}
	parsed, err := ParseActionFile("Action.c", source)
	if err != nil || len(parsed.ParseFailures) != 0 {
		t.Fatalf("the C twin must parse: %v %+v\n%s", err, parsed.ParseFailures, source)
	}
	if len(parsed.Functions) != 2 || parsed.Functions[0].Name != jsUnsupportedFunction || parsed.Functions[1].Name != "web_url" || parsed.Functions[1].LineNumber != 4 {
		t.Fatalf("functions = %+v", parsed.Functions)
	}
}

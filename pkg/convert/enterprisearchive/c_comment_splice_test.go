package enterprisearchive

import (
	"reflect"
	"strings"
	"testing"
)

func TestFindSplicedCommentLines(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		want         []CSplicedCommentLine
	}{
		{"continued comment swallows a call", "//\tsnippet\\\"}},{\\\"relationshipType\\\r\n\tweb_reg_save_param(\"c_x\",LAST);\r\nweb_url(\"y\",LAST);\r\n",
			[]CSplicedCommentLine{{LineNumber: 2, CommentLine: 1, Function: "web_reg_save_param"}}},
		{"chained continuation", "// a\\\nfirst(1);\\\nsecond(2);\nthird(3);\n",
			[]CSplicedCommentLine{{LineNumber: 2, CommentLine: 1, Function: "first"}, {LineNumber: 3, CommentLine: 1, Function: "second"}}},
		{"non-call code", "x = 1; // note\\\ny = 2;\n", []CSplicedCommentLine{{LineNumber: 2, CommentLine: 1}}},
		{"swallowed comment and blank lines are not reported", "// a\\\n// b\n// c\\\n\nf();\n", nil},
		{"comment without a trailing backslash", "// a\nf();\n", nil},
		{"slashes inside a string are not a comment", "web_url(\"u\", \"URL=https://example.com/\\\\\",\n\tLAST);\n", nil},
		{"line comment inside a block comment", "/* // a\\\n*/ f();\n", nil},
		{"block comment closed before a line comment", "/* a */ g(); // b\\\nf();\n", []CSplicedCommentLine{{LineNumber: 2, CommentLine: 1, Function: "f"}}},
		{"backslash followed by space does not continue", "// a\\ \nf();\n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := findSplicedCommentLines(tc.source); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("spliced lines = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// A recorded script registered a parameter on the line after a pasted
// response snippet whose // comment ends in a backslash. C
// splices the two lines, so LoadRunner never registers the parameter; the
// import must agree on both parser paths and say why.
const splicedRegistrationSource = "Action()\r\n{\r\n" +
	"\tweb_reg_save_param(\"c_ref3\",\"LB=a\",\"RB=b\",LAST);\r\n" +
	"//\tproduct\\\":{\\\"referenceId\\\":\\\"62e4\\\"}},{\\\"relationshipType\\\r\n" +
	"\tweb_reg_save_param(\"c_ref4\",\"LB=a\",\"RB=b\",\"ORD=5\",LAST);\r\n" +
	"\tweb_reg_save_param(\"c_ref5\",\"LB=c\",\"RB=d\",LAST);\r\n" +
	"\tweb_url(\"cart\",\"URL=https://example.com/cart\",LAST);\r\n" +
	"\treturn 0;\r\n}\r\n"

func TestSplicedCommentRegistrationIsSkippedAndReported(t *testing.T) {
	parsed, err := ParseActionFile("Action.c", splicedRegistrationSource)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := parseActionFileLegacy("Action.c", splicedRegistrationSource, splicedCommentLineSet(parsed.SplicedCommentLines))
	if err != nil {
		t.Fatal(err)
	}
	for path, action := range map[string]*ParsedAction{"ast": parsed, "legacy": legacy} {
		var registered []string
		for _, fn := range action.Functions {
			if fn.Name == "web_reg_save_param" {
				registered = append(registered, fn.StepName)
			}
		}
		if want := []string{"c_ref3", "c_ref5"}; !reflect.DeepEqual(registered, want) {
			t.Errorf("%s registrations = %v, want %v", path, registered, want)
		}
	}

	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{ScriptName: "spliced"})
	if err != nil {
		t.Fatal(err)
	}
	var extractors []string
	for _, action := range result.Script.Actions {
		for _, extractor := range action.Extractors {
			extractors = append(extractors, strings.ToLower(extractor.Name))
		}
	}
	if strings.Contains(strings.Join(extractors, ","), "c_ref4") {
		t.Fatalf("spliced registration became an extractor: %v", extractors)
	}
	warned := false
	for _, warning := range result.Warnings {
		if strings.Contains(warning, "Action.c:5: web_reg_save_param does not run") && strings.Contains(warning, "line 4 ends in a backslash") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("missing spliced-comment warning: %v", result.Warnings)
	}
}

// ConvertZip reports swallowed lines for every protocol, once.
func TestConvertZipReportsSplicedCommentsForEveryProtocol(t *testing.T) {
	for scriptType, call := range map[string]string{
		"ODBC": `lrd_stmt("select id from customers", LAST);`,
		"Web":  `web_url("Home", "URL=https://example.test/", LAST);`,
	} {
		source := "Action()\n{\n\t// pasted snippet ends in a backslash \\\n\t" + call + "\n\t" + call + "\n\treturn 0;\n}\n"
		result, err := ConvertZip(buildSourceProtocolZip(t, scriptType, source), ConvertOptions{})
		if err != nil {
			t.Fatalf("%s: %v", scriptType, err)
		}
		warnings := 0
		for _, warning := range result.Warnings {
			if strings.Contains(warning, "does not run: the // comment on line 3 ends in a backslash") {
				warnings++
			}
		}
		if warnings != 1 {
			t.Errorf("%s: %d spliced-comment warnings in %v, want 1", scriptType, warnings, result.Warnings)
		}
	}
}

// One source file backing two lifecycle phases still reports a line once
// (archive warnings are de-duplicated).
func TestSplicedCommentWarningOncePerSharedFile(t *testing.T) {
	files := map[string]string{
		"Shared.usr": "[General]\nType=Web\n\n[Actions]\nvuser_init=Action.c\nAction=Action.c\n",
		"Action.c": "vuser_init()\n{\n\tweb_url(\"Init\", \"URL=https://example.test/init\", LAST);\n\treturn 0;\n}\n" +
			"Action()\n{\n\t// snippet \\\n\tweb_url(\"Gone\", \"URL=https://example.test/gone\", LAST);\n\tweb_url(\"Home\", \"URL=https://example.test/\", LAST);\n\treturn 0;\n}\n",
	}
	result, err := ConvertZip(zipFilesForTest(t, files), ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	warnings := 0
	for _, warning := range result.Warnings {
		if strings.Contains(warning, "Action.c:9: web_url does not run") {
			warnings++
		}
	}
	if warnings != 1 {
		t.Fatalf("%d spliced-comment warnings in %v, want 1", warnings, result.Warnings)
	}
}

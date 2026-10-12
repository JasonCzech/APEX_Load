package enterprisearchive

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/importreview"
)

func TestVTSReferencesCannotDisappearFromImportedExpressions(t *testing.T) {
	for _, body := range []string{
		`lrvtc_retrieve_row();`,
		`int rc = lrvtc_update_message_ifequals("secret-column", 1, "secret-value", "old");`,
		`if (lrvtc_search_row("secret-column", "secret-value", ";") == 1) { lr_think_time(1); }`,
		`int (*getrow)() = &lrvtc_retrieve_row; getrow();`,
		`vtc_future_extension(0);`,
		"#define GET_ROW lrvtc_retrieve_row\n GET_ROW();",
	} {
		t.Run(body, func(t *testing.T) {
			parsed, _ := ParseActionFile("Action.c", "Action() {\n"+body+"\nweb_url(\"home\", \"URL=https://example.test\", LAST);\nreturn 0;\n}")
			result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{})
			if err != nil {
				t.Fatal(err)
			}
			assertVTSGate(t, result)
			// Finalization and repeated checks do not multiply the gate.
			before := len(result.Script.Init)
			appendVTSSourceGates(result, []*ParsedAction{parsed}, nil)
			if len(result.Script.Init) != before {
				t.Fatal("duplicate VTS gate")
			}
		})
	}
}

func assertVTSGate(t *testing.T, result *ConvertResult) {
	t.Helper()
	findings, err := importreview.FindingsFromScript(result.Script)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, finding := range findings {
		if finding.Code == vtsSourceReviewCode && finding.Gate == importreview.GateExecution {
			found = true
		}
	}
	if !found || importreview.Evaluate(importreview.EvaluationInput{Findings: findings}).ReplayReady {
		t.Fatalf("VTS dependency was not blocked: %+v", findings)
	}
	for _, action := range result.Script.Init {
		if action.Metadata["source_vts_gate"] == nil {
			continue
		}
		encoded, _ := json.Marshal(action)
		if strings.Contains(string(encoded), "secret-value") || strings.Contains(string(encoded), "secret-column") {
			t.Fatal("raw source arguments leaked into VTS gate")
		}
	}
}

func TestVTSScannerIgnoresCommentsAndQuotedValues(t *testing.T) {
	source := "// lrvtc_connect(\"secret\");\n/* vtc_retrieve_row(0); */\nchar *text=\"lrvtc_retrieve_row() vts.connect()\";\nchar c='x';"
	if refs := scanVTSSourceReferences(source); len(refs) != 0 {
		t.Fatalf("false references: %+v", refs)
	}
	for _, source := range []string{"Lrvtc.retrieve_row();", "vts.retrieve_row();", "vts_multi.retrieve_row(handle);", "lrvtc.retrieveRow();", "vtc.retrieveRow(handle);", "var api = lrvtc;"} {
		if refs := scanVTSSourceReferences(source); len(refs) != 1 {
			t.Fatalf("%q references: %+v", source, refs)
		}
	}
	var sourceMany strings.Builder
	for i := 0; i < 1000; i++ {
		fmt.Fprintf(&sourceMany, "vtc_operation_%d();\n", i)
	}
	if refs := scanVTSSourceReferences(sourceMany.String()); len(refs) != maxVTSSourceReferences {
		t.Fatalf("unbounded evidence: %d", len(refs))
	}
}

func TestVTSJavaScriptAndCArchivesStayExecutionBlocked(t *testing.T) {
	for _, tc := range []struct{ language, file, content string }{
		{"C", "Action.c", "Action() { lrvtc_retrieve_row(); web_url(\"home\", \"URL=https://example.test\", LAST); return 0; }"},
		{"JavaScript", "Action.js", "function Action() { var rc = lrvtc.retrieveRow(); web.url({name:'home',url:'https://example.test'}); return 0; }"},
	} {
		t.Run(tc.language, func(t *testing.T) {
			archive := zipFilesForTest(t, map[string]string{
				"S.usr": "[General]\nType=Multi\nScriptLanguage=" + tc.language + "\n[Actions]\nAction=" + tc.file + "\n",
				tc.file: tc.content,
			})
			result, err := ConvertZip(archive, ConvertOptions{})
			if err != nil {
				t.Fatal(err)
			}
			assertVTSGate(t, result)
		})
	}
}

func TestVTSDependencyInExecutedHelperFromUnscheduledFile(t *testing.T) {
	archive := zipFilesForTest(t, map[string]string{
		"S.usr":       "[General]\nType=Multi\nActiveTypes=QTWeb\nDefaultCfg=default.cfg\n[Actions]\nAction=Action.c\nOptional=Optional.c\n[CfgFiles]\nDefault Profile=default.cfg\n[RunLogicFiles]\nDefault Profile=default.usp\n",
		"default.cfg": "[General]\nDefaultRunLogic=default.usp\n",
		"default.usp": "[RunLogicRunRoot]\nName=\"Run\"\nRunLogicActionOrder=\"Action\"\nRunLogicObjectKind=\"Group\"\n[RunLogicRunRoot:Action]\nName=\"Action\"\nRunLogicObjectKind=\"Action\"\n",
		"Action.c":    "Action() {\nhelper();\nweb_url(\"home\", \"URL=https://example.test\", LAST);\nreturn 0;\n}",
		"Optional.c":  "Optional() { return 0; }\nvoid helper() {\nint rc = lrvtc_retrieve_row();\nlr_think_time(0);\n}",
	})
	result, err := ConvertZip(archive, ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	assertVTSGate(t, result)
}

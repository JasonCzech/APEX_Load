package enterprisearchive

import (
	"strings"
	"testing"
)

func TestConvertZipCarriesCustomParamBracesToJavaVuser(t *testing.T) {
	archive := zipFilesForTest(t, map[string]string{
		"Braced/Braced.usr": "[General]\nType=Java_protocols\nScriptLanguage=JAVA\nRunType=Java-Interpreter\nParameterFile=Braced.prm\nParamLeftBrace=<\nParamRightBrace=>\n[Actions]\nActions=Actions.java\n",
		"Braced/Actions.java": `import lrapi.lr;
public class Actions {
  public int init() { return 0; }
  public int action() {
    lr.start_transaction("Lookup");
    lr.output_message(lr.eval_string("<dbUrl>"));
    lr.end_transaction("Lookup", lr.AUTO);
    return 0;
  }
  public int end() { return 0; }
}`,
		"Braced/Braced.prm": "[parameter:dbUrl]\nColumnName=\"dbUrl\"\nDelimiter=\"\t\"\nGenerateNewVal=\"Once\"\nParamName=\"dbUrl\"\nSelectNextRow=\"Sequential\"\nTable=\"URL.dat\"\nType=\"Table\"\n",
		"Braced/URL.dat":    "dbUrl\tdbUser\njdbc:test:one\tuser\n",
	})
	result, err := ConvertZip(archive, ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	if result.Script == nil || result.Script.Protocol != "java_vuser" {
		t.Fatalf("script = %#v, want a java_vuser draft", result.Script)
	}
	if cfg := result.Script.Config.JavaVuser; cfg.ParamLeftBrace != "<" || cfg.ParamRightBrace != ">" {
		t.Fatalf("java_vuser braces = %q %q, want < >", cfg.ParamLeftBrace, cfg.ParamRightBrace)
	}
	if len(result.Script.DataSources) != 1 || result.Script.DataSources[0].Delimiter != "\t" {
		t.Fatalf("data sources = %#v, want the tab-delimited URL.dat", result.Script.DataSources)
	}
}

func TestConvertZipWarnsWhenCScriptUsesCustomParamBraces(t *testing.T) {
	archive := zipFilesForTest(t, map[string]string{
		"Web/Web.usr":      "[General]\nType=Multi\nActiveTypes=QTWeb\nParamLeftBrace=<\nParamRightBrace=>\n[Actions]\nvuser_init=vuser_init.c\nAction=Action.c\nvuser_end=vuser_end.c\n",
		"Web/Action.c":     "Action()\n{\n\tweb_url(\"home\", \"URL=https://example.test/<path>\", LAST);\n\treturn 0;\n}\n",
		"Web/vuser_init.c": "vuser_init()\n{\n\treturn 0;\n}\n",
		"Web/vuser_end.c":  "vuser_end()\n{\n\treturn 0;\n}\n",
	})
	result, err := ConvertZip(archive, ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	if !strings.Contains(strings.Join(result.Warnings, "\n"), "C conversion recognizes only {name} references") {
		t.Fatalf("warnings = %#v, want the custom-delimiter warning", result.Warnings)
	}
}

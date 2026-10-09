package enterprisearchive

import (
	"reflect"
	"strings"
	"testing"
)

func TestScriptLanguageClassification(t *testing.T) {
	tests := []struct {
		value      string
		java       bool
		javaScript bool
	}{
		{"JAVA", true, false},
		{"Java-Interpreter", true, false},
		{"Java Vuser", true, false},
		{"JavaScript", false, true},
		{"javascript", false, true},
		{"Java Script", false, true},
		{"JS", false, true},
		{"C", false, false},
		{"", false, false},
	}
	for _, tt := range tests {
		if got := scriptLanguageIsJava(tt.value); got != tt.java {
			t.Errorf("scriptLanguageIsJava(%q) = %v, want %v", tt.value, got, tt.java)
		}
		if got := scriptLanguageIsJavaScript(tt.value); got != tt.javaScript {
			t.Errorf("scriptLanguageIsJavaScript(%q) = %v, want %v", tt.value, got, tt.javaScript)
		}
	}
}

func TestJavaScriptWebActionFiles(t *testing.T) {
	const webJS = "function Action()\n{\n\tweb.url({ name: 'home', url: 'http://x.test/' });\n\treturn 0;\n}\n"
	tests := []struct {
		name  string
		files map[string]string
		want  []string
		ok    bool
	}{
		{
			name: "javascript web project declares js action files",
			files: map[string]string{
				"S.usr":         "[General]\nType=Multi\nScriptLanguage=JavaScript\n[Actions]\nvuser_init=vuser_init.js\nAction=Action.js\n",
				"Action.js":     webJS,
				"vuser_init.js": "function vuser_init() { return 0; }",
			},
			want: []string{"Action.js", "vuser_init.js"},
			ok:   true,
		},
		{
			name: "js action files without a language line",
			files: map[string]string{
				"S.usr":     "[General]\nType=QTWeb\n[Actions]\nAction=Action.js\n",
				"Action.js": webJS,
			},
			want: []string{"Action.js"},
			ok:   true,
		},
		{
			name:  "no project file but web API calls in a js file",
			files: map[string]string{"Action.js": webJS},
			want:  []string{"Action.js"},
			ok:    true,
		},
		{
			name: "truclient also uses JavaScript but has its own importer",
			files: map[string]string{
				"S.usr":     "[General]\nType=TruClient\nScriptLanguage=JavaScript\n[Actions]\nAction=Action.js\n",
				"Action.js": webJS,
			},
		},
		{
			name: "devweb main.js is not a VuGen web script",
			files: map[string]string{
				"main.js": "load.initialize(async function () {});",
			},
		},
		{
			name: "c web script",
			files: map[string]string{
				"S.usr":    "[General]\nType=QTWeb\nScriptLanguage=C\n[Actions]\nAction=Action.c\n",
				"Action.c": "Action() { web_url(\"x\", \"URL=http://x.test/\", LAST); return 0; }",
			},
		},
		{
			name: "explicit C language wins over a helper js file",
			files: map[string]string{
				"S.usr":     "[General]\nType=QTWeb\nScriptLanguage=C\n",
				"helper.js": webJS,
			},
		},
		{
			name: "c action file beside a js helper without a project file",
			files: map[string]string{
				"Action.c":  "Action() { return 0; }",
				"helper.js": webJS,
			},
		},
		{
			name: "java vuser",
			files: map[string]string{
				"S.usr":        "[General]\nType=Java Vuser\nScriptLanguage=JAVA\n[Actions]\nActions=Actions.java\n",
				"Actions.java": "public class Actions {}",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg *ProjectConfig
			var sections map[string]map[string]string
			if usr, ok := tt.files["S.usr"]; ok {
				parsed, err := ParseUSRFile(usr)
				if err != nil {
					t.Fatal(err)
				}
				cfg = parsed
				sections = ParseConfigSections(usr)
			}
			got, ok := javaScriptWebActionFiles(tt.files, cfg, sections)
			if ok != tt.ok || (tt.ok && !reflect.DeepEqual(got, tt.want)) {
				t.Fatalf("javaScriptWebActionFiles = %v, %v; want %v, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestJavaScriptWebArchiveIsReviewOnlyNotJavaVuser(t *testing.T) {
	archive := zipFilesForTest(t, map[string]string{
		"S.usr":     "[General]\nType=Multi\nScriptLanguage=JavaScript\n[Actions]\nAction=Action.js\n",
		"Action.js": "function Action()\n{\n\tweb.url({ name: 'home', url: 'http://x.test/' });\n\treturn 0;\n}\n",
	})
	result, err := ConvertZip(archive, ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Script != nil {
		t.Fatalf("a JavaScript web script must not produce a script, got protocol %q", result.Script.Protocol)
	}
	if result.DetectedProtocol == nil || result.DetectedProtocol.ImportMode != ImportModeReviewOnly || result.DetectedProtocol.APEXLoadProtocol != "http" {
		t.Fatalf("detected protocol = %+v", result.DetectedProtocol)
	}
	if result.ReviewArtifact == nil || !reflect.DeepEqual(result.ReviewArtifact.Metadata["reason_codes"], []string{javaScriptWebReasonCode}) {
		t.Fatalf("review artifact = %+v", result.ReviewArtifact)
	}
	if files, _ := result.ReviewArtifact.Metadata["javascript_action_files"].([]string); !reflect.DeepEqual(files, []string{"Action.js"}) {
		t.Fatalf("javascript_action_files = %v", result.ReviewArtifact.Metadata["javascript_action_files"])
	}
	if !strings.Contains(strings.Join(result.Warnings, "\n"), "written in JavaScript") {
		t.Fatalf("warnings = %v", result.Warnings)
	}
}

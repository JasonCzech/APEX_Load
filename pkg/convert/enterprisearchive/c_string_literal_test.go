package enterprisearchive

import (
	"encoding/base64"
	"regexp"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestDecodeCStringContent(t *testing.T) {
	for _, tt := range []struct {
		in, want string
		ok       bool
	}{
		{`plain`, `plain`, true},
		{`a\\b`, `a\b`, true},
		{`say \"hi\"`, `say "hi"`, true},
		{`O\'Brien\?`, `O'Brien?`, true}, // valid C, rejected by strconv.Unquote
		{`l1\nl2\tx`, "l1\nl2\tx", true},
		{`\x41\101\0`, "AA\x00", true},
		{`\x81\xff`, "\x81\xff", true},
		{`caf\u00e9`, "caf\u00e9", true},
		// All or nothing: one unknown escape keeps the whole text verbatim.
		{`\xZ`, `\xZ`, false},
		{`\d+\.`, `\d+\.`, false},
		{`C:\data\file.dat`, `C:\data\file.dat`, false},
		{`dangling\`, `dangling\`, false},
	} {
		got, ok := decodeCStringContent(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("decodeCStringContent(%q) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

// Real VuGen script (iPad ACI store check-in): the response carries escaped
// JSON inside a JSON string, so the boundaries keep their backslashes. A
// second C decode used to strip them and the extractor never matched.
// Quoted configuration values pass through the same literal cleaner. A
// Windows path keeps every backslash when any escape in it is not C.
func TestQuotedConfigValuesKeepUnknownEscapesVerbatim(t *testing.T) {
	for in, want := range map[string]string{
		`"C:\data\file.dat"`: `C:\data\file.dat`,
		`"say \"hi\""`:       `say "hi"`,
		`"O\'Brien"`:         `O'Brien`,
	} {
		if got := cleanConfigValue(in); got != want {
			t.Errorf("cleanConfigValue(%s) = %q, want %q", in, got, want)
		}
	}
}

func TestEscapedJSONBoundariesAreDecodedOnce(t *testing.T) {
	ext, _ := convertSingleExtractor(t, `web_reg_save_param("c_BAN","LB=userAccountNumber\\\":\\\"","RB=\\\",\\\"individualsFound","NotFound=warning",LAST);`)
	if ext == nil {
		t.Fatal("extractor not converted")
	}
	want := regexp.QuoteMeta(`userAccountNumber\":\"`) + "(.+?)" + regexp.QuoteMeta(`\",\"individualsFound`)
	if ext.Expression != want {
		t.Fatalf("expression = %q, want %q", ext.Expression, want)
	}
	response := `{"returnValue":"{\"userAccountNumber\":\"287654321\",\"individualsFound\":1}"}`
	match := regexp.MustCompile(ext.Expression).FindStringSubmatch(response)
	if len(match) != 2 || match[1] != "287654321" {
		t.Fatalf("extractor does not match the recorded response: %v", match)
	}
}

func convertSingleAction(t *testing.T, call string, opts ConvertOptions) dsl.Action {
	t.Helper()
	parsed, err := ParseActionFile("Action.c", "Action()\n{\n\t"+call+"\n\treturn 0;\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	opts.ScriptName = "Decode"
	result, err := Convert([]*ParsedAction{parsed}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Script.Actions) == 0 {
		t.Fatal("no actions converted")
	}
	return result.Script.Actions[0]
}

func TestRequestBodiesAreDecodedOnce(t *testing.T) {
	for _, tt := range []struct{ name, body, want string }{
		{"literal backslash-n", `a\\nb`, `a\nb`},
		{"escaped JSON in JSON", `{\"m\":\"{\\\"a\\\":1}\"}`, `{"m":"{\"a\":1}"}`},
		{"apostrophe escape", `O\'Brien`, `O'Brien`},
		{"real newline", `l1\nl2`, "l1\nl2"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			action := convertSingleAction(t, `web_custom_request("Post", "URL=https://example.com/api", "Method=POST", "Body=`+tt.body+`", LAST);`, ConvertOptions{})
			if action.Body != tt.want {
				t.Fatalf("body = %q, want %q", action.Body, tt.want)
			}
		})
	}
}

func TestParserKeepsRawBodyForCodePageEncoding(t *testing.T) {
	parsed, err := ParseActionFile("Action.c", "Action()\n{\n\tweb_custom_request(\"Bin\", \"URL=https://example.com/b\", \"Method=POST\", \"Body=\\x81\\x00ok\", LAST);\n\treturn 0;\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	fn := parsed.Functions[0]
	if fn.Body != "\x81\x00ok" || fn.RawBody != `\x81\x00ok` {
		t.Fatalf("Body=%q RawBody=%q", fn.Body, fn.RawBody)
	}
	action := &dsl.Action{}
	if err := encodeImportedBody(action, fn, "windows-1252"); err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(action.Body)
	if err != nil || string(raw) != "\x81\x00ok" || action.BodyEncoding != "base64" {
		t.Fatalf("encoded body = %q (%s), want exact Windows-1252 bytes", action.Body, action.BodyEncoding)
	}
}

func TestTextCheckIsDecodedOnce(t *testing.T) {
	action := convertSingleAction(t, `web_reg_find("Text=\\\"ok\\\"", LAST);`+"\n\t"+`web_url("Landing", "URL=https://example.com/landing", LAST);`, ConvertOptions{})
	for _, assertion := range action.Assertions {
		if assertion.Type == "body_contains" {
			if got := assertion.Value; got != `\"ok\"` {
				t.Fatalf("text check = %q, want %q", got, `\"ok\"`)
			}
			return
		}
	}
	t.Fatalf("no body_contains assertion: %+v", action.Assertions)
}

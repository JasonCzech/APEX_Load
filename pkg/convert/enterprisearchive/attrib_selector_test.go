package enterprisearchive

import (
	"strings"
	"testing"
)

// VuGen records web_reg_save_param_attrib with TagName= and Extract= plus the
// element's own attributes, then search filters. The registration used to be
// held for review because only Tag=/Attribute= were understood.
func TestWebRegSaveParamAttribUsesVuGenArguments(t *testing.T) {
	result := convertSingleWebSource(t, `
	web_reg_save_param_attrib(
		"ParamName=c_sid",
		"TagName=input",
		"Extract=value",
		"Name=sid",
		"Id=sid",
		"Type=hidden",
		SEARCH_FILTERS,
		"IgnoreRedirections=Yes",
		"RequestUrl=*/session*",
		LAST);
	web_url("Session", "URL=https://example.test/session", LAST);`)
	action := result.Script.Actions[0]
	if len(action.Extractors) != 1 {
		t.Fatalf("extractors = %#v, want the attrib extractor (warnings %v)", action.Extractors, result.Warnings)
	}
	if got, want := action.Extractors[0].Expression, "//input[@name='sid'][@id='sid'][@type='hidden']/@value"; action.Extractors[0].Type != "xpath" || got != want {
		t.Fatalf("extractor = %s %q, want xpath %q", action.Extractors[0].Type, got, want)
	}
	for _, warning := range result.Warnings {
		if strings.Contains(warning, "web_reg_save_param_attrib") {
			t.Fatalf("unexpected attrib warning: %s", warning)
		}
	}
}

func TestSourceAttribSelector(t *testing.T) {
	for _, tc := range []struct {
		name, call, want, warning string
	}{
		{"ordinal", `web_reg_save_param_attrib("ParamName=p", "TagName=A", "Extract=HREF", "Class=next", "Ordinal=2", LAST);`, "(//a[@class='next']/@href)[2]", ""},
		{"no tag", `web_reg_save_param_attrib("ParamName=p", "Extract=content", "Name=csrf-token", LAST);`, "//*[@name='csrf-token']/@content", ""},
		{"apostrophe", `web_reg_save_param_attrib("ParamName=p", "TagName=input", "Extract=value", "Title=it's", LAST);`, `//input[@title="it's"]/@value`, ""},
		{"both quotes", `web_reg_save_param_attrib("ParamName=p", "TagName=input", "Extract=value", "Title=it's \"x\"", LAST);`, `//input[@title=concat('it', "'", 's "x"')]/@value`, ""},
		{"parameter filter", `web_reg_save_param_attrib("ParamName=p", "TagName=input", "Extract=value", "Name={p_field}", LAST);`, "//input/@value", "uses a parameter"},
		{"legacy names", `web_reg_save_param_attrib("ParamName=p", "Tag=a", "Attribute=href", LAST);`, "//a/@href", ""},
		{"search filters without the marker", `web_reg_save_param_attrib("ParamName=p", "TagName=input", "Extract=value", "Name=token", "Scope=Body", "RequestUrl=*/login*", LAST);`, "//input[@name='token']/@value", ""},
		{"scope as an element attribute", `web_reg_save_param_attrib("ParamName=p", "TagName=th", "Extract=id", "Scope=col", LAST);`, "//th[@scope='col']/@id", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := ParseActionFile("Action.c", "Action()\n{\n\t"+tc.call+"\n\treturn 0;\n}\n")
			if err != nil || len(parsed.Functions) != 1 {
				t.Fatalf("parse: %v %d", err, len(parsed.Functions))
			}
			ordinal, saveAll := sourceExtractorOrdinal(parsed.Functions[0])
			mode := ""
			if saveAll {
				mode = "all"
			}
			got, warnings, ok := sourceAttribSelector(parsed.Functions[0], "p", mode, ordinal)
			if !ok || got != tc.want {
				t.Fatalf("selector = %q (ok %v, warnings %v), want %q", got, ok, warnings, tc.want)
			}
			if joined := strings.Join(warnings, "\n"); (tc.warning == "") != (joined == "") || !strings.Contains(joined, tc.warning) {
				t.Fatalf("warnings = %v, want %q", warnings, tc.warning)
			}
		})
	}

	parsed, err := ParseActionFile("Action.c", "Action()\n{\n\tweb_reg_save_param_attrib(\"ParamName=p\", \"TagName=input\", LAST);\n\treturn 0;\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, warnings, ok := sourceAttribSelector(parsed.Functions[0], "p", "", 0); ok || !strings.Contains(strings.Join(warnings, ""), "no Extract=") {
		t.Fatalf("missing Extract= accepted: ok=%v warnings=%v", ok, warnings)
	}
}

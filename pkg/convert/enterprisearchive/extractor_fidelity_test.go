package enterprisearchive

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func convertSingleExtractor(t *testing.T, registration string) (*dsl.Extractor, []string) {
	t.Helper()
	source := "Action()\n{\n\t" + registration + "\n\tweb_url(\"Landing\", \"URL=https://example.com/landing\", LAST);\n\treturn 0;\n}\n"
	parsed, err := ParseActionFile("Action.c", source)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{ScriptName: "Fidelity"})
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}
	if len(result.Script.Actions) == 0 {
		t.Fatal("no actions converted")
	}
	action := result.Script.Actions[0]
	if len(action.Extractors) == 0 {
		return nil, result.Warnings
	}
	if len(action.Extractors) > 1 {
		t.Fatalf("expected at most one extractor, got %#v", action.Extractors)
	}
	return &action.Extractors[0], result.Warnings
}

func decodeBoundaryConfigExpr(t *testing.T, expr string) boundaryExpressionConfig {
	t.Helper()
	if !strings.HasPrefix(expr, boundaryExpressionConfigPrefix) {
		t.Fatalf("expected encoded boundary config, got %q", expr)
	}
	var config boundaryExpressionConfig
	if err := json.Unmarshal([]byte(strings.TrimPrefix(expr, boundaryExpressionConfigPrefix)), &config); err != nil {
		t.Fatalf("decode boundary config: %v", err)
	}
	return config
}

func TestConvertExtractorBoundaryOrdinal(t *testing.T) {
	ext, warnings := convertSingleExtractor(t, `web_reg_save_param("OfferID", "LB=offer=", "RB=;", "ORD=5", LAST);`)
	if ext == nil {
		t.Fatal("extractor not converted")
	}
	if ext.Type != "boundary" {
		t.Fatalf("type = %q, want boundary", ext.Type)
	}
	config := decodeBoundaryConfigExpr(t, ext.Expression)
	if config.Left != "offer=" || config.Right != ";" || config.Ordinal != 5 {
		t.Errorf("config = %+v, want offer=/;/ordinal 5", config)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
}

func TestConvertExtractorBoundaryOrdinalOneStaysSimple(t *testing.T) {
	ext, _ := convertSingleExtractor(t, `web_reg_save_param("GroupID", "LB=group=", "RB=;", "ORD=1", LAST);`)
	if ext == nil {
		t.Fatal("extractor not converted")
	}
	if ext.Expression != "group=|||;" {
		t.Errorf("expression = %q, want simple left|||right form", ext.Expression)
	}
}

func TestConvertExtractorQuotedBoundaryOrdinalUsesRegexOrdinal(t *testing.T) {
	ext, _ := convertSingleExtractor(t, `web_reg_save_param("RefID", "LB=ref\":\"", "RB=\"}", "ORD=3", LAST);`)
	if ext == nil {
		t.Fatal("extractor not converted")
	}
	if ext.Type != "regex_ordinal" {
		t.Fatalf("type = %q, want regex_ordinal", ext.Type)
	}
	if !strings.HasPrefix(ext.Expression, regexOrdinalExpressionConfigPrefix) {
		t.Fatalf("expression = %q, want encoded regex ordinal", ext.Expression)
	}
	var config regexOrdinalExpressionConfig
	if err := json.Unmarshal([]byte(strings.TrimPrefix(ext.Expression, regexOrdinalExpressionConfigPrefix)), &config); err != nil {
		t.Fatalf("decode regex ordinal config: %v", err)
	}
	if config.Ordinal != 3 {
		t.Errorf("ordinal = %d, want 3", config.Ordinal)
	}
}

func TestConvertExtractorSaveAllOrdinal(t *testing.T) {
	ext, warnings := convertSingleExtractor(t, `web_reg_save_param("ItemIDs", "LB=item=", "RB=;", "ORD=All", LAST);`)
	if ext == nil {
		t.Fatal("extractor not converted")
	}
	if ext.Mode != "all" {
		t.Errorf("mode = %q, want all", ext.Mode)
	}
	if ext.Type != "boundary" || ext.Expression != "item=|||;" {
		t.Errorf("extractor = %+v, want plain boundary with mode all", ext)
	}
	if len(warnings) != 0 {
		t.Errorf("save-all is supported now; unexpected warnings: %v", warnings)
	}
}

func TestConvertExtractorJSONSelectAll(t *testing.T) {
	ext, warnings := convertSingleExtractor(t, `web_reg_save_param_json("ParamName=Items", "QueryString=$.items", "SelectAll=Yes", SEARCH_FILTERS, LAST);`)
	if ext == nil {
		t.Fatal("extractor not converted")
	}
	if ext.Mode != "all" || ext.Type != "jsonpath" {
		t.Errorf("extractor = %+v, want jsonpath with mode all", ext)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
}

func TestConvertExtractorRegexpOrdinalAllStaysPlainRegex(t *testing.T) {
	ext, _ := convertSingleExtractor(t, `web_reg_save_param_regexp("ParamName=ids", "RegExp=id=(\\d+)", "Ordinal=All", SEARCH_FILTERS, LAST);`)
	if ext == nil {
		t.Fatal("extractor not converted")
	}
	if ext.Mode != "all" || ext.Type != "regex" {
		t.Errorf("extractor = %+v, want plain regex with mode all (no ordinal encoding)", ext)
	}
	if strings.HasPrefix(ext.Expression, regexOrdinalExpressionConfigPrefix) {
		t.Errorf("save-all must not encode an ordinal: %q", ext.Expression)
	}
}

func TestConvertExtractorCaseInsensitiveBoundaryFlag(t *testing.T) {
	ext, warnings := convertSingleExtractor(t, `web_reg_save_param_ex("ParamName=SessionID", "LB/IC=sessionid=", "RB=;", SEARCH_FILTERS, "Scope=Body", LAST);`)
	if ext == nil {
		t.Fatal("extractor not converted")
	}
	config := decodeBoundaryConfigExpr(t, ext.Expression)
	if !config.CaseInsensitive {
		t.Errorf("config = %+v, want case_insensitive", config)
	}
	if config.Left != "sessionid=" || config.Right != ";" {
		t.Errorf("boundaries = %q / %q", config.Left, config.Right)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
}

func TestConvertExtractorUnsupportedBoundaryFlagWarns(t *testing.T) {
	ext, warnings := convertSingleExtractor(t, `web_reg_save_param_ex("ParamName=Blob", "LB/BIN=\\x01", "RB/BIN=\\x02", SEARCH_FILTERS, LAST);`)
	if ext == nil {
		t.Fatal("extractor not converted")
	}
	found := 0
	for _, w := range warnings {
		if strings.Contains(w, "boundary modifier") && strings.Contains(w, "/BIN") {
			found++
		}
	}
	if found != 2 {
		t.Errorf("expected LB and RB modifier warnings, got %v", warnings)
	}
}

func TestConvertExtractorBoundarySeparatorCollisionUsesConfig(t *testing.T) {
	ext, _ := convertSingleExtractor(t, `web_reg_save_param("Odd", "LB=a|||b", "RB=;", LAST);`)
	if ext == nil {
		t.Fatal("extractor not converted")
	}
	config := decodeBoundaryConfigExpr(t, ext.Expression)
	if config.Left != "a|||b" || config.Right != ";" {
		t.Errorf("config = %+v", config)
	}
}

func TestConvertExtractorPostProcessingMapping(t *testing.T) {
	ext, warnings := convertSingleExtractor(t, `web_reg_save_param("Token", "LB=t=", "RB=;", "SaveOffset=2", "SaveLen=8", "Convert=HTML_TO_URL", LAST);`)
	if ext == nil {
		t.Fatal("extractor not converted")
	}
	if ext.SaveOffset != 2 || ext.SaveLength != 8 || ext.Convert != "html_url_encode" {
		t.Errorf("post-processing = offset %d len %d convert %q, want 2/8/html_url_encode", ext.SaveOffset, ext.SaveLength, ext.Convert)
	}
	if len(warnings) != 0 {
		t.Errorf("supported modifiers must not warn: %v", warnings)
	}
}

func TestConvertExtractorHTMLToTextMapsToHTMLDecode(t *testing.T) {
	ext, _ := convertSingleExtractor(t, `web_reg_save_param("Title", "LB=<title>", "RB=</title>", "Convert=HTML_TO_TEXT", LAST);`)
	if ext == nil {
		t.Fatal("extractor not converted")
	}
	if ext.Convert != "html_decode" {
		t.Errorf("convert = %q, want html_decode", ext.Convert)
	}
}

func TestConvertExtractorUnknownConvertWarns(t *testing.T) {
	ext, warnings := convertSingleExtractor(t, `web_reg_save_param("Token", "LB=t=", "RB=;", "Convert=SOMETHING_ELSE", LAST);`)
	if ext == nil {
		t.Fatal("extractor not converted")
	}
	if ext.Convert != "" {
		t.Errorf("unknown convert must not map, got %q", ext.Convert)
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "Convert=SOMETHING_ELSE") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected unknown-convert warning, got %v", warnings)
	}
}

func TestConvertExtractorRelFrameIdStillWarns(t *testing.T) {
	_, warnings := convertSingleExtractor(t, `web_reg_save_param("Token", "LB=t=", "RB=;", "RelFrameId=1", LAST);`)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "RelFrameId=1") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected RelFrameId warning, got %v", warnings)
	}
}

func TestConvertFindAssertionSaveCountCaseInsensitive(t *testing.T) {
	source := `Action()
{
	web_reg_find("Text=Welcome", "Search=Body", "Savecount=welcome_count", LAST);
	web_url("Landing", "URL=https://example.com/landing", LAST);
	return 0;
}
`
	parsed, err := ParseActionFile("Action.c", source)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{ScriptName: "SaveCount"})
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}
	action := result.Script.Actions[0]
	if len(action.Extractors) != 1 {
		t.Fatalf("extractors = %#v, want the Savecount extractor", action.Extractors)
	}
	if action.Extractors[0].Type != "regex_count" || action.Extractors[0].Name != "welcome_count" {
		t.Errorf("extractor = %+v", action.Extractors[0])
	}
}

func TestConvertExtractorRegexpOrdinalAcceptsORDKey(t *testing.T) {
	ext, _ := convertSingleExtractor(t, `web_reg_save_param_regexp("ParamName=banNumber", "RegExp=/TD><TD>(.*?)</TD", "ORD=2", SEARCH_FILTERS, "Scope=Body", LAST);`)
	if ext == nil {
		t.Fatal("extractor not converted")
	}
	if ext.Type != "regex_ordinal" {
		t.Fatalf("type = %q, want regex_ordinal", ext.Type)
	}
}

func TestConvertExtractorOnMissMapping(t *testing.T) {
	tests := []struct {
		name         string
		registration string
		wantOnMiss   string
	}{
		{"default fails like the source tool", `web_reg_save_param("Token", "LB=t=", "RB=;", LAST);`, "fail"},
		{"explicit error fails", `web_reg_save_param("Token", "LB=t=", "RB=;", "NotFound=Error", LAST);`, "fail"},
		{"warning assigns empty", `web_reg_save_param("Token", "LB=t=", "RB=;", "NotFound=Warning", LAST);`, "empty"},
		{"lowercase warning assigns empty", `web_reg_save_param("Token", "LB=t=", "RB=;", "NotFound=warning", LAST);`, "empty"},
		{"json variant honors NotFound", `web_reg_save_param_json("ParamName=Tok", "QueryString=$.tok", "NotFound=Warning", SEARCH_FILTERS, LAST);`, "empty"},
		{"json variant defaults to fail", `web_reg_save_param_json("ParamName=Tok", "QueryString=$.tok", SEARCH_FILTERS, LAST);`, "fail"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ext, _ := convertSingleExtractor(t, tc.registration)
			if ext == nil {
				t.Fatal("extractor not converted")
			}
			if ext.OnMiss != tc.wantOnMiss {
				t.Errorf("on_miss = %q, want %q", ext.OnMiss, tc.wantOnMiss)
			}
		})
	}
}

func TestConvertFindSaveCountExtractorNeverFails(t *testing.T) {
	source := `Action()
{
	web_reg_find("Text=Welcome", "SaveCount=welcome_count", LAST);
	web_url("Landing", "URL=https://example.com/landing", LAST);
	return 0;
}
`
	parsed, err := ParseActionFile("Action.c", source)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{ScriptName: "Counts"})
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}
	action := result.Script.Actions[0]
	if len(action.Extractors) != 1 {
		t.Fatalf("extractors = %#v", action.Extractors)
	}
	if action.Extractors[0].OnMiss != "" {
		t.Errorf("a zero count is a valid result; on_miss = %q, want lenient default", action.Extractors[0].OnMiss)
	}
}

func TestConvertExtractorEmptyBoundariesPrunesUnusedWholeBody(t *testing.T) {
	ext, _ := convertSingleExtractor(t, `web_reg_save_param("get_body","LB=","RB=","Search=Body","NotFound=Warning",LAST);`)
	if ext != nil {
		t.Fatalf("extractor = %+v, want unused whole-body capture pruned", ext)
	}
}

func TestPruneUnusedWholeBodyExtractorsRetainsReferencedAndTargeted(t *testing.T) {
	script := &dsl.Script{Actions: []dsl.Action{
		{Name: "source", Extractors: []dsl.Extractor{
			{Name: "unused", Type: "regex", Expression: "(?s)^(.*)$"},
			{Name: "used", Type: "regex", Expression: "(?s)^(.*)$"},
			{Name: "token", Type: "regex", Expression: `token=(.+)`},
		}},
		{Name: "consumer", Body: `{{var.used}}`},
	}}
	if removed := pruneUnusedWholeBodyExtractors(script); len(removed) != 1 {
		t.Fatalf("removed = %v, want 1", removed)
	}
	if got := script.Actions[0].Extractors; len(got) != 2 || got[0].Name != "used" || got[1].Name != "token" {
		t.Fatalf("extractors = %#v, want referenced whole-body and targeted extractors", got)
	}
}

func TestPruneUnusedWholeBodyExtractors(t *testing.T) {
	newScript := func(body string) *dsl.Script {
		return &dsl.Script{Actions: []dsl.Action{
			{Name: "request", Type: "request", Extractors: []dsl.Extractor{{Name: "get_body", Type: "regex", Expression: "(?s)^(.*)$"}}},
			{Name: "next", Type: "request", Body: body},
		}}
	}

	unused := newScript("ordinary body")
	if removed := pruneUnusedWholeBodyExtractors(unused); len(removed) != 1 || removed[0] != "get_body" || len(unused.Actions[0].Extractors) != 0 {
		t.Fatalf("unused prune = %#v actions=%#v", removed, unused.Actions)
	}

	referenced := newScript("response={{var.get_body}}")
	if removed := pruneUnusedWholeBodyExtractors(referenced); len(removed) != 0 || len(referenced.Actions[0].Extractors) != 1 {
		t.Fatalf("referenced prune = %#v actions=%#v", removed, referenced.Actions)
	}
}

// VuGen's Text/IC= is a case-insensitive web_reg_find.
func TestConvertFindAssertionHonorsTextFlags(t *testing.T) {
	source := `Action()
{
	web_reg_find("Search=Body", "SaveCount=error_count", "Text/IC=Tuxedo", LAST);
	web_url("count", "URL=https://example.com/count", LAST);
	web_reg_find("Fail=NotFound", "Search=Body", "Text/IC=Shipping", LAST);
	web_url("check", "URL=https://example.com/check", LAST);
	web_reg_find("Text/BIN=\\x00", LAST);
	web_url("binary", "URL=https://example.com/binary", LAST);
	return 0;
}
`
	parsed, err := ParseActionFile("Action.c", source)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{ScriptName: "Flags"})
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}
	actions := result.Script.Actions
	if got := actions[0].Extractors; len(got) != 1 || got[0].Expression != "(?i)Tuxedo" {
		t.Fatalf("count extractor = %+v, want a case-insensitive count", got)
	}
	if got := actions[1].Assertions; len(got) != 1 || got[0].Type != "body_contains" || got[0].Value != "Shipping" || !got[0].CaseInsensitive {
		t.Fatalf("assertion = %+v, want case-insensitive body_contains", got)
	}
	// Binary text becomes a pattern of its decoded bytes.
	if got := actions[2].Assertions; len(got) != 1 || got[0].Type != "text_matches" || got[0].Value != `\x00` {
		t.Fatalf("binary assertions = %+v, want a text_matches check for the NUL byte", got)
	}
}

// lr_force_think_time waits whatever the think-time settings say.
func TestConvertForceThinkTimeKeepsAForcedDelay(t *testing.T) {
	source := `Action()
{
	web_url("home", "URL=https://example.com/", LAST);
	lr_force_think_time(2);
	return 0;
}
`
	parsed, err := ParseActionFile("Action.c", source)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{ScriptName: "Forced"})
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}
	actions := result.Script.Actions
	if len(actions) != 2 || actions[0].ThinkTimeAfter {
		t.Fatalf("actions = %#v, want the forced wait kept as its own action", actions)
	}
	if got := actions[1].ThinkTime; !actions[1].ThinkTimeAfter || got.Type != "fixed" || got.MeanMs != 2000 || !got.Force {
		t.Fatalf("think time = %+v, want a forced 2000 ms fixed delay", got)
	}
}

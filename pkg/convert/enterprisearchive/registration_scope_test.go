package enterprisearchive

import (
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func parseSingleCall(t *testing.T, call string) ParsedFunction {
	t.Helper()
	parsed, err := ParseActionFile("Action.c", "Action()\n{\n\t"+call+"\n\treturn 0;\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Functions) == 0 {
		t.Fatalf("nothing parsed from %s", call)
	}
	return parsed.Functions[0]
}

func findingCodes(conversion registrationConversion) []string {
	codes := make([]string, 0, len(conversion.findings))
	for _, finding := range conversion.findings {
		codes = append(codes, finding.Code)
	}
	return codes
}

func TestRegistrationScopeMapping(t *testing.T) {
	tests := []struct {
		name   string
		call   string
		source string
		codes  string
	}{
		{"legacy headers", `web_reg_save_param("p", "LB=a", "RB=b", "Search=Headers", LAST);`, "header", ""},
		{"legacy default searches headers and body", `web_reg_save_param("p", "LB=a", "RB=b", LAST);`, "all", ""},
		{"explicit ALL", `web_reg_save_param("p", "LB=a", "RB=b", "Search=ALL", LAST);`, "all", ""},
		{"noresource is the main body", `web_reg_save_param("p", "LB=a", "RB=b", "Search=Noresource", LAST);`, "body", ""},
		{"ex body", `web_reg_save_param_ex("ParamName=p", "LB=a", "RB=b", SEARCH_FILTERS, "Scope=Body", LAST);`, "body", ""},
		{"ex cookies searches Set-Cookie lines", `web_reg_save_param_ex("ParamName=p", "LB=a", "RB=b", SEARCH_FILTERS, "Scope=Cookies", LAST);`, "cookies", ""},
		{"json over cookies is narrowed", `web_reg_save_param_json("ParamName=p", "QueryString=$.id", SEARCH_FILTERS, "Scope=Cookies", LAST);`, "body", registrationScopeNarrowedReasonCode},
		{"regexp default", `web_reg_save_param_regexp("ParamName=p", "RegExp=id=(\\d+)", LAST);`, "all", ""},
		{"json reads the body", `web_reg_save_param_json("ParamName=p", "QueryString=$.id", LAST);`, "body", ""},
		{"dfes gate", `web_reg_save_param_ex("ParamName=p", "LB=a", "RB=b", "DFEs=Base64Decode", LAST);`, "all", registrationDFEReasonCode},
		{"ignore redirections yes is exact", `web_reg_save_param_ex("ParamName=p", "LB=a", "RB=b", SEARCH_FILTERS, "IgnoreRedirections=Yes", LAST);`, "all", ""},
		{"ignore redirections no searches redirect hops", `web_reg_save_param_ex("ParamName=p", "LB=a", "RB=b", SEARCH_FILTERS, "IgnoreRedirections=No", LAST);`, "all", ""},
		{"header names narrow the header search", `web_reg_save_param_ex("ParamName=p", "LB=a", "RB=b", SEARCH_FILTERS, "Scope=Headers", "HeaderNames=Location", LAST);`, "header", ""},
		{"header names on a body search have no effect", `web_reg_save_param_ex("ParamName=p", "LB=a", "RB=b", SEARCH_FILTERS, "Scope=Body", "HeaderNames=Location", LAST);`, "body", registrationFilterReasonCode},
		{"frames", `web_reg_save_param_ex("ParamName=p", "LB=a", "RB=b", "RelFrameId=1.2", LAST);`, "all", registrationFilterReasonCode},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conversion := convertRegistration(parseSingleCall(t, tt.call))
			if conversion.extractor == nil {
				t.Fatal("no extractor")
			}
			if conversion.extractor.Source != tt.source {
				t.Errorf("source = %q, want %q", conversion.extractor.Source, tt.source)
			}
			if got := strings.Join(findingCodes(conversion), ","); got != tt.codes {
				t.Errorf("findings = %q, want %q", got, tt.codes)
			}
		})
	}
}

func TestBoundaryWhitespaceIsPreserved(t *testing.T) {
	all := convertRegistration(parseSingleCall(t, `web_reg_save_param("p", "LB=Order-Ref: ", "RB=\r\n", LAST);`))
	if all.extractor.Expression != "Order-Ref: |||\r\n" {
		t.Fatalf("default-scope boundary = %q, want the recorded whitespace and CRLF", all.extractor.Expression)
	}
	header := convertRegistration(parseSingleCall(t, `web_reg_save_param("p", "LB=X-Ref: ", "RB=\r\n", "Search=Headers", LAST);`))
	if header.extractor.Expression != "X-Ref: |||\n" {
		t.Fatalf("header boundary = %q, want CRLF normalized to the LF header surface", header.extractor.Expression)
	}
	regex := convertRegistration(parseSingleCall(t, `web_reg_save_param_regexp("ParamName=p", "RegExp= id=(\\d+) ", LAST);`))
	if regex.extractor.Expression != ` id=(\d+) ` {
		t.Fatalf("regexp = %q, want surrounding spaces kept", regex.extractor.Expression)
	}
}

func TestRegistrationFiltersAreJudgedAgainstTheAttachedRequest(t *testing.T) {
	_, filter := sourceRegistrationFilters(parseSingleCall(t, `web_reg_save_param_ex("ParamName=p", "LB=a", "RB=b", SEARCH_FILTERS, "RequestUrl=*orders*", "ContentType=text/html", LAST);`), "p", "all")
	if filter == nil {
		t.Fatal("RequestUrl/ContentType must produce a pending filter")
	}
	matching := &dsl.Action{Target: "/app/orders", Metadata: map[string]any{"original_url": "https://shop.test/app/orders?id=1"}}
	if findings := filter.findingsForAttachedRequest(matching, "text/html"); len(findings) != 0 {
		t.Fatalf("filters matching the main request must be exact, got %v", findings)
	}
	other := &dsl.Action{Target: "/app/home", Metadata: map[string]any{"original_url": "https://shop.test/app/home"}}
	if findings := filter.findingsForAttachedRequest(other, "application/json"); len(findings) != 2 {
		t.Fatalf("a filter matching neither URL nor content type must produce two findings, got %v", findings)
	}
}

func TestLoadRunnerGlobMatch(t *testing.T) {
	tests := []struct {
		pattern, value string
		want           bool
	}{
		{"*orders*", "https://shop.test/app/orders?id=1", true},
		{"*ORDERS*", "https://shop.test/app/orders", true},
		{"https://shop.test/*", "https://shop.test/a/b/c", true},
		{"*app.js*", "https://shop.test/app/home", false},
		{"text/*", "text/html; charset=utf-8", true},
		{"orders", "https://shop.test/app/orders", true},
		{"", "anything", true},
	}
	for _, tt := range tests {
		if got := loadRunnerGlobMatch(tt.pattern, tt.value); got != tt.want {
			t.Errorf("loadRunnerGlobMatch(%q, %q) = %v, want %v", tt.pattern, tt.value, got, tt.want)
		}
	}
}

func TestTextCheckOnHeadersIsNotRunAgainstTheBody(t *testing.T) {
	result := convertSingleWebSource(t, `
	web_reg_find("Text=X-Status: ok", "Search=Headers", LAST);
	web_url("status", "URL=https://shop.test/status", LAST);`)
	action := result.Script.Actions[0]
	for _, assertion := range action.Assertions {
		if assertion.Type == "body_contains" {
			t.Fatalf("a Search=Headers check must not become a body assertion: %+v", action.Assertions)
		}
	}
	reasons, _ := action.Metadata["review_reasons"].([]string)
	if len(reasons) != 1 || reasons[0] != textCheckScopeReasonCode {
		t.Fatalf("review_reasons = %v, want %s", action.Metadata["review_reasons"], textCheckScopeReasonCode)
	}
}

func TestRegistrationExtractorScope(t *testing.T) {
	tests := []struct {
		call string
		want dsl.ExtractorScope
	}{
		{`web_reg_save_param("p", "LB=a", "RB=b", LAST);`, dsl.ExtractorScope{Redirects: "include", Resources: "include"}},
		{`web_reg_save_param_ex("ParamName=p", "LB=a", "RB=b", SEARCH_FILTERS, "IgnoreRedirections=Yes", "RequestUrl=*token*", "ContentType=text/*", LAST);`,
			dsl.ExtractorScope{Redirects: "exclude", Resources: "include", RequestURL: "*token*", ContentType: "text/*"}},
		{`web_reg_save_param_ex("ParamName=p", "LB=a", "RB=b", SEARCH_FILTERS, "Scope=Headers", "HeaderNames=Location, Set-Cookie", LAST);`,
			dsl.ExtractorScope{Redirects: "include", Resources: "include", HeaderNames: []string{"Location", "Set-Cookie"}}},
	}
	for _, tt := range tests {
		conversion := convertRegistration(parseSingleCall(t, tt.call))
		if conversion.extractor == nil || conversion.extractor.Scope == nil || !reflect.DeepEqual(*conversion.extractor.Scope, tt.want) {
			t.Errorf("%s: scope = %+v, want %+v", tt.call, conversion.extractor.Scope, tt.want)
		}
	}
}

func TestResourceFilterIsExactWhenAnExplicitResourceMatches(t *testing.T) {
	_, filter := sourceRegistrationFilters(parseSingleCall(t, `web_reg_save_param_ex("ParamName=p", "LB=a", "RB=b", SEARCH_FILTERS, "RequestUrl=*token*", LAST);`), "p", "all")
	page := &dsl.Action{Target: "/app/home", Metadata: map[string]any{"original_url": "https://shop.test/app/home"}}
	if findings := filter.findingsForAttachedRequest(page, "text/html"); len(findings) != 1 {
		t.Fatalf("a filter only an unlisted resource matches must keep its finding, got %v", findings)
	}
	page.Params = map[string]any{"resources": []any{map[string]any{"url": "https://shop.test/static/token.js"}}}
	if findings := filter.findingsForAttachedRequest(page, "text/html"); len(findings) != 0 {
		t.Fatalf("an explicit resource matching the filter makes it exact, got %v", findings)
	}
}

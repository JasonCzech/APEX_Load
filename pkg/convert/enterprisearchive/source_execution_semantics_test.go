package enterprisearchive

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func convertSourceExecutionFixture(t *testing.T, source string) *ConvertResult {
	t.Helper()
	parsed, err := ParseActionFile("Action.c", source)
	if err != nil {
		t.Fatalf("ParseActionFile: %v", err)
	}
	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{ScriptName: "source-semantics"})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	return result
}

func TestWebRegFindSaveCountWithoutFailDoesNotAddAssertion(t *testing.T) {
	result := convertSourceExecutionFixture(t, `
Action()
{
	web_reg_find("SaveCount=SuccessCount", "Text=Success", "Search=Body", LAST);
	web_url("request", "URL=https://example.test/", LAST);
	return 0;
}`)

	if len(result.Script.Actions) != 1 {
		t.Fatalf("actions = %#v, want one request", result.Script.Actions)
	}
	request := result.Script.Actions[0]
	if len(request.Assertions) != 0 {
		t.Fatalf("assertions = %#v, SaveCount without Fail must not fail the request", request.Assertions)
	}
	if len(request.Extractors) != 1 || request.Extractors[0].Name != "successcount" || request.Extractors[0].Type != "regex_count" {
		t.Fatalf("extractors = %#v, want SuccessCount regex_count", request.Extractors)
	}
}

func TestWebRegFindSearchAllIsExplicitlyPartial(t *testing.T) {
	result := convertSourceExecutionFixture(t, `
Action()
{
	web_reg_find("SaveCount=SuccessCount", "Text=Success", "Search=ALL", LAST);
	web_url("request", "URL=https://example.test/", LAST);
	return 0;
}`)

	request := result.Script.Actions[0]
	if len(request.Assertions) != 0 {
		t.Fatalf("assertions = %#v, SaveCount without Fail must remain non-failing", request.Assertions)
	}
	if len(request.Extractors) != 1 || request.Extractors[0].Source != "all" {
		t.Fatalf("extractors = %#v, want the headers-and-body portion of Search=ALL", request.Extractors)
	}
	if !strings.Contains(strings.Join(result.Warnings, "\n"), "Search=ALL") {
		t.Fatalf("warnings = %v, want Search=ALL scope warning", result.Warnings)
	}
	assertSourceReviewSupportMode(t, result.SourceReview.Mappings, "web_reg_find", SourceFunctionSupportReviewOnly)
}

func TestSingleRequestTransactionEmitsExecutableMarkersWithBranchStatus(t *testing.T) {
	result := convertSourceExecutionFixture(t, `
Action()
{
	lr_start_transaction("ACME_ISBA");
	web_url("request", "URL=https://example.test/", LAST);
	if (atoi(lr_eval_string("{SuccessCount}")) > 0) {
		lr_end_transaction("ACME_ISBA", LR_PASS);
	} else {
		lr_end_transaction("ACME_ISBA", LR_FAIL);
	}
	return 0;
}`)

	if len(result.Script.Actions) != 3 {
		t.Fatalf("actions = %#v, want start marker, request, and status branch", result.Script.Actions)
	}
	if result.Script.Actions[0].Type != "source_transaction" {
		t.Fatalf("first action = %#v, want source_transaction start", result.Script.Actions[0])
	}
	if got := result.Script.Actions[0].Name; got != "Start_ACME_ISBA_transaction" {
		t.Fatalf("start marker name = %q, want distinct display label", got)
	}
	if result.Script.Actions[1].Type == "http_sequence" {
		t.Fatalf("single request transaction was unnecessarily compacted: %#v", result.Script.Actions[1])
	}
	branch := result.Script.Actions[2]
	if branch.Type != "control_if" || branch.Control == nil || len(branch.Control.Steps) != 1 || len(branch.Control.Else) != 1 {
		t.Fatalf("branch = %#v, want one transaction end in each branch", branch)
	}
	for label, end := range map[string]string{
		"pass": sourceActionArgsForTest(branch.Control.Steps[0]),
		"fail": sourceActionArgsForTest(branch.Control.Else[0]),
	} {
		if !strings.Contains(end, "LR_"+strings.ToUpper(label)) {
			t.Fatalf("%s end args = %q", label, end)
		}
	}
	for _, end := range []dsl.Action{branch.Control.Steps[0], branch.Control.Else[0]} {
		if !strings.HasPrefix(end.Name, "End_ACME_ISBA_transaction") {
			t.Fatalf("end marker name = %q, want distinct display label", end.Name)
		}
	}
}

func sourceActionArgsForTest(action dsl.Action) string {
	return fmt.Sprint(action.Params["args"])
}

func TestCacheCleanupAndTLSVersionAreExecutable(t *testing.T) {
	result := convertSourceExecutionFixture(t, `
Action()
{
	web_set_sockets_option("SSL_VERSION", "TLS1.2");
	web_cache_cleanup();
	web_url("request", "URL=https://example.test/", LAST);
	return 0;
}`)

	if got := result.Script.Config.HTTP.TLS.MinVersion; got != "1.2" {
		t.Fatalf("TLS min version = %q, want 1.2", got)
	}
	if got := result.Script.Config.HTTP.TLS.MaxVersion; got != "1.2" {
		t.Fatalf("TLS max version = %q, want exact 1.2", got)
	}
	if len(result.Script.Actions) < 2 || result.Script.Actions[0].Type != "source_runtime" {
		t.Fatalf("actions = %#v, want positional source_runtime cache clear", result.Script.Actions)
	}
	if function := firstNonEmptyActionParam(result.Script.Actions[0].Params, "function"); function != "web_cache_cleanup" {
		t.Fatalf("cache action function = %q", function)
	}
	assertSourceReviewSupportMode(t, result.SourceReview.Mappings, "web_cache_cleanup", SourceFunctionSupportExecutable)
}

func TestUnknownSourceTLSVersionUsesDefaultsAndRequiresReview(t *testing.T) {
	result := convertSourceExecutionFixture(t, `
Action()
{
	web_set_sockets_option("SSL_VERSION", "SSL3");
	web_url("request", "URL=https://example.test/", LAST);
	return 0;
}`)

	if result.Script.Config.HTTP.TLS.MinVersion != "" || result.Script.Config.HTTP.TLS.MaxVersion != "" {
		t.Fatalf("TLS config = %#v, want defaults for unsupported SSL3", result.Script.Config.HTTP.TLS)
	}
	assertSourceReviewSupportMode(t, result.SourceReview.Mappings, "web_set_sockets_option", SourceFunctionSupportReviewOnly)
	if !strings.Contains(strings.Join(result.Warnings, "\n"), "SSL3") {
		t.Fatalf("warnings = %#v, want unsupported SSL3 review evidence", result.Warnings)
	}

	key := "web_set_sockets_option.SSL_VERSION"
	if runtimeAdditionalAttributeIsNativelyMapped(result.Script.Config, key, "SSL3") {
		t.Fatal("unsupported SSL3 compatibility attribute was classified as natively executed")
	}
}

func TestThinkTimeAfterTransactionEndAttachesToEndMarker(t *testing.T) {
	result := convertSourceExecutionFixture(t, `
Action()
{
	lr_start_transaction("ACME_ISBA");
	web_url("inside", "URL=https://example.test/inside", LAST);
	lr_end_transaction("ACME_ISBA", LR_PASS);
	lr_think_time(1);
	web_url("outside", "URL=https://example.test/outside", LAST);
	return 0;
}`)

	if len(result.Script.Actions) != 4 {
		t.Fatalf("actions = %#v, want start, request, end, request", result.Script.Actions)
	}
	end := result.Script.Actions[2]
	if end.Type != "source_transaction" || !end.ThinkTimeAfter || end.ThinkTime.Type != "fixed" || end.ThinkTime.MeanMs != 1000 {
		t.Fatalf("transaction end = %#v, want 1s post-end think time", end)
	}
	if result.Script.Actions[0].Name != "Start_ACME_ISBA_transaction" || end.Name != "End_ACME_ISBA_transaction" {
		t.Fatalf("marker names = %q / %q, want distinct start/end labels", result.Script.Actions[0].Name, end.Name)
	}
	if result.Script.Actions[1].ThinkTimeAfter {
		t.Fatalf("inside request = %#v, post-end wait must stay outside transaction", result.Script.Actions[1])
	}
	if strings.Contains(strings.Join(result.Warnings, "\n"), "could not be placed") {
		t.Fatalf("warnings = %v, post-end wait should be exactly placed", result.Warnings)
	}
	assertSourceReviewMapping(t, result.SourceReview.Mappings, "lr_think_time", "actions[2].think_time", "converted")
}

func firstNonEmptyActionParam(params map[string]any, key string) string {
	if value, ok := params[key].(string); ok {
		return value
	}
	return ""
}

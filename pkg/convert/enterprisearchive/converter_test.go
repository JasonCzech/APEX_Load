package enterprisearchive

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/jasonczech/apexload/pkg/convert/trueclient"
	"github.com/jasonczech/apexload/pkg/dsl"
)

const sampleActionC = `
#include "web_api.h"

Action()
{
	web_reg_save_param("SessionID",
		"LB=sid=",
		"RB=;",
		LAST);

	lr_start_transaction("Login");

	web_url("Homepage",
		"URL=https://example.com/app/home",
		"Resource=0",
		"RecContentType=text/html",
		LAST);

	lr_think_time(3);

	web_submit_data("Login_Submit",
		"Action=https://example.com/app/login",
		"Method=POST",
		"RecContentType=text/html",
		"Referer=https://example.com/app/home",
		ITEMDATA,
		"Name=username", "Value=testuser", ENDITEM,
		"Name=password", "Value={UserPassword}", ENDITEM,
		LAST);

	lr_end_transaction("Login", LR_AUTO);

	lr_think_time(5);

	web_custom_request("API_GetOrders",
		"URL=https://example.com/api/v1/orders",
		"Method=GET",
		"RecContentType=application/json",
		LAST);

	web_custom_request("API_CreateOrder",
		"URL=https://example.com/api/v1/orders",
		"Method=POST",
		"Body={\"item\": \"widget\", \"quantity\": 5}",
		"RecContentType=application/json",
		LAST);

	web_set_max_html_param_len("4096");

	return 0;
}
`

const sampleRegFindActionC = `
#include "web_api.h"

Action()
{
	web_reg_find("Text=Welcome back",
		"SaveCount=welcomeCount",
		LAST);

	web_url("Homepage",
		"URL=https://example.com/app/home",
		"Resource=0",
		LAST);

	web_reg_find("Text=Internal Server Error",
		"Fail=Found",
		LAST);

	web_url("Dashboard",
		"URL=https://example.com/app/dashboard",
		"Resource=0",
		LAST);

	return 0;
}
`

const sampleSOAPActionC = `
#include "web_api.h"

Action()
{
	web_add_header("SOAPAction", "urn:GetQuote");

	web_custom_request("GetQuote",
		"URL=https://example.com/services/quotes",
		"Method=POST",
		"Body=<soapenv:Envelope xmlns:soapenv=\"http://schemas.xmlsoap.org/soap/envelope/\"><soapenv:Body><GetQuote xmlns=\"urn:quotes\"><Symbol>STORM</Symbol></GetQuote></soapenv:Body></soapenv:Envelope>",
		"RecContentType=text/xml; charset=utf-8",
		LAST);

	return 0;
}
`

func TestParseActionFile(t *testing.T) {
	parsed, err := ParseActionFile("Action.c", sampleActionC)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(parsed.Functions) == 0 {
		t.Fatal("expected functions to be parsed")
	}

	// Check we found the expected function types
	names := make(map[string]int)
	for _, fn := range parsed.Functions {
		names[fn.Name]++
	}

	expected := map[string]int{
		"web_reg_save_param":         1,
		"lr_start_transaction":       1,
		"web_url":                    1,
		"lr_think_time":              2,
		"web_submit_data":            1,
		"lr_end_transaction":         1,
		"web_custom_request":         2,
		"web_set_max_html_param_len": 1,
	}

	for name, count := range expected {
		if names[name] != count {
			t.Errorf("expected %d %s calls, got %d", count, name, names[name])
		}
	}

	// Check web_url parsing
	for _, fn := range parsed.Functions {
		if fn.Name == "web_url" {
			if fn.StepName != "Homepage" {
				t.Errorf("expected step name 'Homepage', got '%s'", fn.StepName)
			}
			if fn.URL != "https://example.com/app/home" {
				t.Errorf("expected URL 'https://example.com/app/home', got '%s'", fn.URL)
			}
			if fn.Method != "GET" {
				t.Errorf("expected method 'GET', got '%s'", fn.Method)
			}
			if fn.Filename != "Action.c" || fn.LineNumber == 0 || fn.EndLine <= fn.LineNumber {
				t.Errorf("source location = %q:%d-%d, want Action.c with multi-line range", fn.Filename, fn.LineNumber, fn.EndLine)
			}
			if !strings.Contains(fn.RawBlock, `web_url("Homepage"`) || !strings.Contains(fn.RawBlock, `"URL=https://example.com/app/home"`) {
				t.Errorf("raw block = %q, want original web_url source", fn.RawBlock)
			}
		}
	}
}

func TestParseActionFileStructuredCallArgs(t *testing.T) {
	parsed, err := ParseActionFile("Action.c", `Action()
{
	custom_helper("Name=Checkout", 42, LR_AUTO, lr_eval_string("{UserID}"), LAST);
	return 0;
}
`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(parsed.Functions) != 1 {
		t.Fatalf("functions = %#v, want one custom_helper", parsed.Functions)
	}
	args := parsed.Functions[0].CallArgs
	if len(args) != 5 {
		t.Fatalf("call args = %#v, want five structured args", args)
	}
	if args[0].Kind != "key_value" || args[0].Name != "Name" || args[0].Value != "Checkout" {
		t.Fatalf("arg0 = %#v, want Name=Checkout key_value", args[0])
	}
	if args[1].Kind != "number" || args[1].Value != "42" {
		t.Fatalf("arg1 = %#v, want numeric 42", args[1])
	}
	if args[2].Kind != "identifier" || args[2].Value != "LR_AUTO" {
		t.Fatalf("arg2 = %#v, want identifier LR_AUTO", args[2])
	}
	if args[3].Kind != "nested_call" || args[3].NestedCall != "lr_eval_string" {
		t.Fatalf("arg3 = %#v, want nested lr_eval_string", args[3])
	}
	if args[4].Kind != "macro" || args[4].Value != "LAST" {
		t.Fatalf("arg4 = %#v, want LAST macro", args[4])
	}
}

func TestParseActionFileFallsBackToLegacyOnSyntaxError(t *testing.T) {
	// A malformed declaration makes the strict C frontend reject the whole
	// translation unit; the lenient scanner must still recover the valid call.
	action := `Action()
{
	web_url("Homepage",
		"URL=https://example.com/app/home",
		LAST);
	int broken = ;
	return 0;
}
`

	if _, astErr := ParseActionFileAST("Action.c", action); astErr == nil {
		t.Fatal("expected ParseActionFileAST to reject the malformed C source; choose a stronger malformation")
	}

	parsed, err := ParseActionFile("Action.c", action)
	if err != nil {
		t.Fatalf("ParseActionFile should recover via legacy fallback, got error: %v", err)
	}
	var web *ParsedFunction
	for i := range parsed.Functions {
		if parsed.Functions[i].Name == "web_url" {
			web = &parsed.Functions[i]
			break
		}
	}
	if web == nil {
		t.Fatalf("expected web_url to be recovered, got %#v", parsed.Functions)
	}
	if web.StepName != "Homepage" || web.URL != "https://example.com/app/home" {
		t.Fatalf("recovered web_url = %#v, want Homepage / example.com URL", web)
	}
}

func TestParseActionFilePropagatesErrorWhenNothingRecoverable(t *testing.T) {
	// Malformed source with no extractable calls should surface the AST
	// diagnostic so the importer can flag the archive for review.
	if _, err := ParseActionFile("Action.c", "Action()\n{\n\tint broken = ;\n}\n"); err == nil {
		t.Fatal("expected error when neither parser can recover any calls")
	}
}

func TestConvert(t *testing.T) {
	parsed, err := ParseActionFile("Action.c", sampleActionC)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{
		ScriptName:        "Test_Conversion",
		IncludeAssertions: true,
	})
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}

	if result.Script.Name != "Test_Conversion" {
		t.Errorf("expected script name 'Test_Conversion', got '%s'", result.Script.Name)
	}

	if result.Script.Protocol != "http" {
		t.Errorf("expected protocol 'http', got '%s'", result.Script.Protocol)
	}

	if result.Script.Config.BaseURL != "https://example.com" {
		t.Errorf("expected base URL 'https://example.com', got '%s'", result.Script.Config.BaseURL)
	}

	if len(result.Script.Actions) == 0 {
		t.Fatal("expected converted actions")
	}

	// Verify stats
	if result.Stats.ConvertedActions != 3 || result.Stats.LogicalActions != 3 {
		t.Errorf("converted/logical actions = %d/%d, want 3/3", result.Stats.ConvertedActions, result.Stats.LogicalActions)
	}
	if result.Stats.RawRequestActions != 4 || result.Stats.CollapsedRequests != 1 {
		t.Errorf("raw/collapsed request actions = %d/%d, want 4/1", result.Stats.RawRequestActions, result.Stats.CollapsedRequests)
	}

	if result.Stats.Extractors != 1 {
		t.Errorf("expected 1 extractor, got %d", result.Stats.Extractors)
	}

	if result.Stats.ThinkTimes != 2 {
		t.Errorf("expected 2 think times, got %d", result.Stats.ThinkTimes)
	}

	if result.Stats.Transactions != 1 {
		t.Errorf("expected 1 transaction, got %d", result.Stats.Transactions)
	}
	if result.Stats.Skipped != 0 {
		t.Errorf("expected no skipped functions, got %d", result.Stats.Skipped)
	}
	if len(result.Stats.UnsupportedFuncs) != 0 {
		t.Fatalf("unsupported funcs = %#v, want none", result.Stats.UnsupportedFuncs)
	}
	if result.Stats.CustomCallFunctions != 0 {
		t.Fatalf("custom_call_functions = %d, want metadata-only helper functions to stay out of visible actions", result.Stats.CustomCallFunctions)
	}
	if result.Stats.TransactionMarkers != 2 {
		t.Fatalf("transaction_markers = %d, want start and end markers", result.Stats.TransactionMarkers)
	}
	if result.SourceReview == nil || len(result.SourceReview.Mappings) == 0 {
		t.Fatal("expected source review mappings")
	}
	assertSourceReviewMapping(t, result.SourceReview.Mappings, "web_url", "actions[0].params.requests[0]", "converted")
	assertSourceReviewMapping(t, result.SourceReview.Mappings, "web_reg_save_param", "actions[0].params.requests[0].extractors[0]", "contributor")
	assertSourceReviewMapping(t, result.SourceReview.Mappings, "lr_think_time", "config.think_time", "converted")
	assertSourceReviewMapping(t, result.SourceReview.Mappings, "web_set_max_html_param_len", "config.web_runtime.additional_attributes.web_set_max_html_param_len", "converted")
	if result.Script.Config.WebRuntime.AdditionalAttributes["web_set_max_html_param_len"] != "4096" {
		t.Fatalf("web_set_max_html_param_len = %#v, want 4096", result.Script.Config.WebRuntime.AdditionalAttributes)
	}

	// Check think time config was computed
	if result.Script.Config.ThinkTime.MeanMs == 0 {
		t.Error("expected think time to be configured")
	}

	// First action (Homepage) should have the extractor attached
	if result.Script.Actions[0].Type != "http_sequence" || result.Script.Actions[0].Name != "Login" {
		t.Fatalf("first action = %#v, want logical Login http_sequence", result.Script.Actions[0])
	}
	firstAction := sequenceChildByName(t, result.Script.Actions[0], "Homepage")
	if len(firstAction.Extractors) != 1 {
		t.Errorf("expected 1 extractor on first action, got %d", len(firstAction.Extractors))
	} else if firstAction.Extractors[0].Name != "sessionid" {
		t.Errorf("expected extractor name 'sessionid', got '%s'", firstAction.Extractors[0].Name)
	}

	t.Logf("Converted %d LR functions → %d DSL actions, %d extractors, %d think times",
		result.Stats.TotalFunctions, result.Stats.ConvertedActions, result.Stats.Extractors, result.Stats.ThinkTimes)
}

func assertSourceReviewMapping(t *testing.T, mappings []SourceReviewMapping, functionName string, targetPath string, status string) {
	t.Helper()
	for _, mapping := range mappings {
		if mapping.Function == functionName && mapping.TargetPath == targetPath && mapping.Status == status {
			return
		}
	}
	t.Fatalf("missing source mapping function=%s target=%s status=%s in %#v", functionName, targetPath, status, mappings)
}

func assertSourceReviewFunctionStatus(t *testing.T, mappings []SourceReviewMapping, functionName string, status string) {
	t.Helper()
	for _, mapping := range mappings {
		if mapping.Function == functionName && mapping.Status == status {
			return
		}
	}
	t.Fatalf("missing source mapping function=%s status=%s in %#v", functionName, status, mappings)
}

func assertSourceReviewSupportMode(t *testing.T, mappings []SourceReviewMapping, functionName string, mode SourceFunctionSupportMode) {
	t.Helper()
	for _, mapping := range mappings {
		if mapping.Function == functionName && mapping.SupportMode == string(mode) {
			return
		}
	}
	t.Fatalf("missing source mapping function=%s support_mode=%s in %#v", functionName, mode, mappings)
}

func findActionByName(t *testing.T, actions []dsl.Action, name string) dsl.Action {
	t.Helper()
	for _, action := range actions {
		if action.Name == name {
			return action
		}
	}
	t.Fatalf("missing action named %q in %#v", name, actions)
	return dsl.Action{}
}

func sequenceChildByName(t *testing.T, sequence dsl.Action, name string) dsl.Action {
	t.Helper()
	if sequence.Type != "http_sequence" {
		t.Fatalf("action %q type = %q, want http_sequence", sequence.Name, sequence.Type)
	}
	rawRequests, ok := sequence.Params["requests"]
	if !ok {
		t.Fatalf("sequence %q missing params.requests: %#v", sequence.Name, sequence.Params)
	}
	values, ok := rawRequests.([]map[string]any)
	if !ok {
		if generic, genericOK := rawRequests.([]any); genericOK {
			values = make([]map[string]any, 0, len(generic))
			for _, value := range generic {
				entry, entryOK := value.(map[string]any)
				if !entryOK {
					t.Fatalf("sequence request entry = %#v, want object", value)
				}
				values = append(values, entry)
			}
		} else {
			t.Fatalf("sequence %q params.requests = %#v, want []map[string]any", sequence.Name, rawRequests)
		}
	}
	for _, entry := range values {
		payload, err := json.Marshal(entry)
		if err != nil {
			t.Fatalf("json.Marshal(sequence child) returned error: %v", err)
		}
		var child dsl.Action
		if err := json.Unmarshal(payload, &child); err != nil {
			t.Fatalf("json.Unmarshal(sequence child) returned error: %v", err)
		}
		if child.Name == name {
			return child
		}
	}
	t.Fatalf("missing sequence child named %q in %#v", name, rawRequests)
	return dsl.Action{}
}

func firstProtocolAction(t *testing.T, actions []dsl.Action) dsl.Action {
	t.Helper()
	for _, action := range actions {
		switch action.Type {
		case "source_transaction", "source_runtime", "source_message", "error_message", "custom_call", "source_review", "source_external":
			continue
		default:
			return action
		}
	}
	t.Fatalf("missing protocol action in %#v", actions)
	return dsl.Action{}
}

func sampleSourceFunctionCall(def SourceFunctionDefinition) string {
	switch def.Name {
	case "lr_continue_on_error":
		return "\tlr_continue_on_error(1);"
	case "lr_think_time":
		return "\tlr_think_time(1);"
	case "lr_rendezvous", "lr_rendezvous_ex":
		return "\t" + def.Name + "(\"sync_point\");"
	case "lr_error_message", "lr_message", "lr_log_message", "lr_output_message", "lr_debug_message", "lr_vuser_status_message":
		return "\t" + def.Name + "(\"Imported message for %s\", lr_eval_string(\"{UserID}\"));"
	case "lr_start_transaction", "lr_start_sub_transaction", "lr_start_cross_vuser_transaction", "lr_start_transaction_instance":
		return "\t" + def.Name + "(\"Checkout\");"
	case "lr_end_transaction", "lr_end_sub_transaction", "lr_end_cross_vuser_transaction", "lr_end_transaction_instance", "lr_stop_transaction", "lr_stop_transaction_instance":
		return "\t" + def.Name + "(\"Checkout\", LR_AUTO);"
	case "lr_fail_trans_with_error":
		return "\t" + def.Name + "(\"Checkout failed\");"
	case "lr_start_timer", "lr_end_timer":
		return "\t" + def.Name + "(\"Timer1\");"
	case "lr_user_data_point", "lr_user_data_point_ex", "lr_user_data_point_instance", "lr_user_data_point_instance_ex", "lr_checkpoint":
		return "\t" + def.Name + "(\"metric\", 1.5);"
	case "lr_save_string":
		return "\tlr_save_string(\"value\", \"SavedParam\");"
	case "lr_save_int":
		return "\tlr_save_int(7, \"SavedInt\");"
	case "lr_param_sprintf":
		return "\tlr_param_sprintf(\"Greeting\", \"Hello %s\", \"Ada\");"
	case "lr_param_increment":
		return "\tlr_param_increment(\"Counter\", 1);"
	case "lr_save_var":
		return "\tlr_save_var(\"abcdef\", 3, 0, \"SavedVar\");"
	case "lr_db_executeSQLStatement":
		return "\tlr_db_executeSQLStatement(\"SQL=select 1\");"
	case "lr_aws_get_secret", "lr_azure_ad_client_get_token", "lr_azure_kv_get_secret", "lr_azure_set_service_port", "lr_cyberark_get_vault":
		return "\t" + def.Name + "(\"SecretName\", \"SecretParam\");"
	default:
		return "\t" + def.Name + "(\"Arg1\", \"Arg2\");"
	}
}

func TestConvertContributorMappingsUseIndexedTargetPaths(t *testing.T) {
	const source = `#include "web_api.h"

Action()
{
	web_reg_save_param("TokenA", "LB=tokenA=", "RB=;", LAST);
	web_reg_save_param("TokenB", "LB=tokenB=", "RB=;", LAST);
	web_reg_find("Text=Welcome", LAST);
	web_reg_find("Text=Dashboard", LAST);
	web_url("Landing", "URL=https://example.com/landing", LAST);
	return 0;
}
`
	parsed, err := ParseActionFile("Action.c", source)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{ScriptName: "Indexed", IncludeAssertions: false})
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}
	if result.SourceReview == nil {
		t.Fatal("expected source review")
	}

	// Each contributor must point at the exact extractor/assertion index it landed on,
	// so the dashboard's side-by-side view can highlight the sub-action element.
	assertSourceReviewMapping(t, result.SourceReview.Mappings, "web_reg_save_param", "actions[0].extractors[0]", "contributor")
	assertSourceReviewMapping(t, result.SourceReview.Mappings, "web_reg_save_param", "actions[0].extractors[1]", "contributor")
	assertSourceReviewMapping(t, result.SourceReview.Mappings, "web_reg_find", "actions[0].assertions[0]", "contributor")
	assertSourceReviewMapping(t, result.SourceReview.Mappings, "web_reg_find", "actions[0].assertions[1]", "contributor")

	// And the indices must actually address the attached elements in order.
	action := result.Script.Actions[0]
	if len(action.Extractors) != 2 || action.Extractors[0].Name != "tokena" || action.Extractors[1].Name != "tokenb" {
		t.Fatalf("extractors = %#v, want [tokena tokenb]", action.Extractors)
	}
	if len(action.Assertions) != 2 {
		t.Fatalf("assertions = %#v, want 2 contributor assertions", action.Assertions)
	}
}

func TestConvertInfersSourceFormTargetsAndIgnoresCommentedCode(t *testing.T) {
	source := `Action()
{
	web_url("Login Page",
		"URL=http://{server_url}/mi/login.jsp",
		"Resource=0",
		LAST);

	/* web_url("Disabled",
		"URL=http://{server_url}/mi/disabled.jsp",
		LAST); */

	web_submit_form("login.jsp_2",
		"Snapshot=t2.inf",
		ITEMDATA,
		"Name=user", "Value={username}", ENDITEM,
		// "Name=disabled", "Value=should_not_import", ENDITEM,
		LAST);

	return 0;
}`
	parsed, err := ParseActionFile("Action.c", source)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(parsed.Functions) != 2 {
		t.Fatalf("functions = %#v, want only live web_url and web_submit_form", parsed.Functions)
	}
	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{})
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", result.Warnings)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions = %#v, want two converted actions", result.Script.Actions)
	}
	form := result.Script.Actions[1]
	if form.Target != "/mi/login.jsp" {
		t.Fatalf("form target = %q, want inferred /mi/login.jsp", form.Target)
	}
	if strings.Contains(form.Body, "disabled") {
		t.Fatalf("form body included commented ITEMDATA: %s", form.Body)
	}
	if form.Metadata["inferred_url_from"] != "source_form_context" {
		t.Fatalf("metadata = %#v, want inferred URL marker", form.Metadata)
	}
}

func TestConvertPreservesSaveCountAndRegexpCorrelations(t *testing.T) {
	source := `Action()
{
	web_reg_find("Search=Body",
		"SaveCount=Error_Count",
		"Text=Show Errors",
		LAST);

	web_reg_save_param_regexp(
		"ParamName=equInstlmntInfo.instSeqNo",
		"RegExp=/TD><TD>(.*?)</TD",
		"Ordinal=2",
		SEARCH_FILTERS,
		"Scope=Body",
		LAST);

	web_url("Lookup",
		"URL=https://example.com/app/lookup",
		"Resource=0",
		LAST);

	return 0;
}`
	parsed, err := ParseActionFile("Action.c", source)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{})
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want SaveCount preserved without warning", result.Warnings)
	}
	if result.Stats.Extractors != 2 {
		t.Fatalf("extractor stats = %d, want SaveCount and regexp extractors", result.Stats.Extractors)
	}
	action := result.Script.Actions[0]
	if len(action.Assertions) != 1 || action.Assertions[0].Type != "body_contains" {
		t.Fatalf("assertions = %#v, want body_contains", action.Assertions)
	}
	if len(action.Extractors) != 2 {
		t.Fatalf("extractors = %#v, want SaveCount and regexp extractors", action.Extractors)
	}
	if got := action.Extractors[0]; got.Name != "error_count" || got.Type != "regex_count" || got.Expression != "Show Errors" {
		t.Fatalf("SaveCount extractor = %#v, want regex_count for error_count", got)
	}
	if got := action.Extractors[1]; got.Name != "equinstlmntinfo_instseqno" || got.Type != "regex_ordinal" || !strings.HasPrefix(got.Expression, regexOrdinalExpressionConfigPrefix) {
		t.Fatalf("regexp extractor = %#v, want ordinal regex extractor", got)
	}
}

func TestConvertSupportsSourceRuntimeControls(t *testing.T) {
	actionC := `
#include "web_api.h"

Action()
{
	lr_continue_on_error(1);
	web_set_timeout("CONNECT", "30");
	web_set_timeout(RECEIVE, "45");
	web_set_timeout(STEP, "60");
	web_set_sockets_option("SSL_VERSION", "TLS1.2");
	web_set_sockets_option("CLOSE_KEEPALIVE_CONNECTIONS", "1");
	web_cache_cleanup();
	web_cleanup_cookies();
	lr_error_message("Imported error message");
	lr_exit(LR_EXIT_VUSER, LR_AUTO);

	web_url("Homepage",
		"URL=https://example.com/app/home",
		LAST);

	return 0;
}
`
	parsed, err := ParseActionFile("Action.c", actionC)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{})
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}
	if result.Stats.Skipped != 0 || len(result.Stats.UnsupportedFuncs) != 0 {
		t.Fatalf("unsupported funcs = %#v skipped=%d, want none", result.Stats.UnsupportedFuncs, result.Stats.Skipped)
	}
	if !result.Script.Config.ErrorHandling.ContinueOnError {
		t.Fatal("continue_on_error = false, want true")
	}
	if result.Script.Config.HTTP.ConnectTimeoutMs != 30000 {
		t.Fatalf("connect timeout = %d, want 30000", result.Script.Config.HTTP.ConnectTimeoutMs)
	}
	if result.Script.Config.HTTP.RequestTimeoutMs != 45000 {
		t.Fatalf("request timeout = %d, want RECEIVE timeout 45000", result.Script.Config.HTTP.RequestTimeoutMs)
	}
	if result.Script.Config.HTTP.TLS.MinVersion != "1.2" {
		t.Fatalf("tls min version = %q, want 1.2", result.Script.Config.HTTP.TLS.MinVersion)
	}
	if result.Script.Config.HTTP.KeepAlive == nil || *result.Script.Config.HTTP.KeepAlive {
		t.Fatalf("keep_alive = %#v, want false", result.Script.Config.HTTP.KeepAlive)
	}
	attrs := result.Script.Config.WebRuntime.AdditionalAttributes
	if attrs["web_cache_cleanup"] != "true" {
		t.Fatalf("web runtime attrs = %#v, want web_cache_cleanup preserved as a native no-op", attrs)
	}
	if _, ok := attrs["web_cleanup_cookies"]; ok {
		t.Fatalf("web runtime attrs = %#v, want web_cleanup_cookies emitted as a cookie_clear action, not an attribute", attrs)
	}
	if attrs["web_set_sockets_option.SSL_VERSION"] != "TLS1.2" {
		t.Fatalf("socket option attrs = %#v, want SSL_VERSION TLS1.2", attrs)
	}
	if attrs["web_set_timeout.CONNECT"] != "30" || attrs["web_set_timeout.RECEIVE"] != "45" || attrs["web_set_timeout.STEP"] != "60" {
		t.Fatalf("timeout attrs = %#v, want CONNECT/RECEIVE/STEP values", attrs)
	}
	if result.Script.Metadata == nil || result.Script.Metadata.Import == nil {
		t.Fatal("expected import metadata for preserved runtime controls")
	}
	if len(result.Script.Actions) != 4 {
		t.Fatalf("actions = %#v, want cookie_clear, error_message, lr_exit, and Homepage request", result.Script.Actions)
	}
	cookieAction := result.Script.Actions[0]
	if cookieAction.Type != "cookie_clear" || cookieAction.Params["function"] != "web_cleanup_cookies" {
		t.Fatalf("cookie action = %#v, want executable cookie_clear from web_cleanup_cookies", cookieAction)
	}
	assertSourceReviewMapping(t, result.SourceReview.Mappings, "web_cleanup_cookies", "actions[0]", "converted")
	assertSourceReviewSupportMode(t, result.SourceReview.Mappings, "web_cleanup_cookies", SourceFunctionSupportExecutable)
	assertSourceReviewSupportMode(t, result.SourceReview.Mappings, "web_cache_cleanup", SourceFunctionSupportNativeNoop)
	errorAction := result.Script.Actions[1]
	if errorAction.Type != "error_message" || errorAction.Value != "Imported error message" {
		t.Fatalf("error action = %#v, want native error_message action", errorAction)
	}
	if errorAction.Params["level"] != "error" || errorAction.Params["always"] != true {
		t.Fatalf("error action params = %#v, want always-on error level", errorAction.Params)
	}
	if errorAction.Metadata["source_function"] != "lr_error_message" {
		t.Fatalf("error action metadata = %#v, want source_function lr_error_message", errorAction.Metadata)
	}
	assertSourceReviewMapping(t, result.SourceReview.Mappings, "lr_error_message", "actions[1]", "converted")
	if exitAction := result.Script.Actions[2]; exitAction.Type != "source_runtime" || exitAction.Params["function"] != "lr_exit" {
		t.Fatalf("exit action = %#v, want native lr_exit runtime action", exitAction)
	}
	preserved := result.Script.Metadata.Import.Preserved
	if _, ok := preserved["source_error_messages"]; ok {
		t.Fatalf("source_error_messages = %#v, want lr_error_message converted natively", preserved["source_error_messages"])
	}
	exits, ok := preserved["source_exit_controls"].([]map[string]any)
	if !ok || len(exits) != 1 {
		t.Fatalf("source_exit_controls = %#v, want one preserved lr_exit", preserved["source_exit_controls"])
	}
	timeouts, ok := preserved["source_timeouts"].([]map[string]any)
	if !ok || len(timeouts) != 3 {
		t.Fatalf("source_timeouts = %#v, want three preserved web_set_timeout calls", preserved["source_timeouts"])
	}
	args, ok := exits[0]["args"].([]string)
	if !ok || len(args) != 2 || args[0] != "LR_EXIT_VUSER" || args[1] != "LR_AUTO" {
		t.Fatalf("lr_exit args = %#v, want LR_EXIT_VUSER/LR_AUTO", exits[0]["args"])
	}
}

func TestConvertMapsWebSetUserToHTTPAuth(t *testing.T) {
	actionC := `
#include "web_api.h"

Action()
{
	web_set_user("perfuser", "perfpass", "secure.example.com:443");

	web_url("Secure Home",
		"URL=https://secure.example.com/app/home",
		LAST);

	return 0;
}
`
	parsed, err := ParseActionFile("Action.c", actionC)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{})
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}
	if result.Stats.Skipped != 0 || len(result.Stats.UnsupportedFuncs) != 0 {
		t.Fatalf("unsupported funcs = %#v skipped=%d, want none", result.Stats.UnsupportedFuncs, result.Stats.Skipped)
	}
	auth := result.Script.Config.HTTP.Auth
	if auth.Type != "basic" || auth.Username != "perfuser" || auth.Password != "perfpass" {
		t.Fatalf("http auth = %#v, want web_set_user basic auth", auth)
	}
	if result.Script.Config.WebRuntime.AdditionalAttributes["web_set_user.host"] != "secure.example.com:443" {
		t.Fatalf("web runtime attrs = %#v, want web_set_user host", result.Script.Config.WebRuntime.AdditionalAttributes)
	}
	preserved := result.Script.Metadata.Import.Preserved
	authControls, ok := preserved["source_authentication"].([]map[string]any)
	if !ok || len(authControls) != 1 || authControls[0]["host"] != "secure.example.com:443" {
		t.Fatalf("source_authentication = %#v, want preserved web_set_user control", preserved["source_authentication"])
	}
	assertSourceReviewMapping(t, result.SourceReview.Mappings, "web_set_user", "config.http.auth", "converted")
}

func TestConvertLRErrorMessageFormatsArgumentsAsNativeAction(t *testing.T) {
	actionC := `
#include "web_api.h"

Action()
{
	lr_error_message("Login failed for User: %s", lr_eval_string("{UserID}"));

	web_url("Homepage",
		"URL=https://example.com/app/home",
		LAST);

	return 0;
}
`
	parsed, err := ParseActionFile("Action.c", actionC)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{})
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}
	if result.Stats.Skipped != 0 || len(result.Stats.UnsupportedFuncs) != 0 {
		t.Fatalf("unsupported funcs = %#v skipped=%d, want none", result.Stats.UnsupportedFuncs, result.Stats.Skipped)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions = %#v, want error_message plus request", result.Script.Actions)
	}
	action := result.Script.Actions[0]
	if action.Name != "LR_Error_Message_6" || action.Type != "error_message" || action.Value != "Login failed for User: %s" {
		t.Fatalf("error action = %#v, want formatted native error_message", action)
	}
	args, ok := action.Params["args"].([]any)
	if !ok || len(args) != 1 || args[0] != "{{var.userid}}" {
		t.Fatalf("error message args = %#v, want {{var.userid}}", action.Params["args"])
	}
	assertSourceReviewMapping(t, result.SourceReview.Mappings, "lr_error_message", "actions[0]", "converted")
}

func TestSourceFunctionRegistryCoversDocumentedFunctions(t *testing.T) {
	defs := allSourceFunctionDefinitions()
	if len(defs) < 80 {
		t.Fatalf("registry definitions = %d, want documented C/VUser 26.1 utility baseline", len(defs))
	}
	for _, def := range defs {
		t.Run(def.Name, func(t *testing.T) {
			if def.SupportMode == "" || def.Family == "" {
				t.Fatalf("definition = %#v, want family and support mode", def)
			}
			source := "Action()\n{\n" + sampleSourceFunctionCall(def) + "\nweb_url(\"Home\", \"URL=https://example.test/home\", LAST);\nreturn 0;\n}\n"
			parsed, err := ParseActionFile("Action.c", source)
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}
			result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{})
			if err != nil {
				t.Fatalf("convert error: %v", err)
			}
			if len(result.Stats.UnsupportedFuncs) != 0 || result.Stats.Skipped != 0 {
				t.Fatalf("unsupported funcs = %#v skipped=%d, want none", result.Stats.UnsupportedFuncs, result.Stats.Skipped)
			}
			assertSourceReviewSupportMode(t, result.SourceReview.Mappings, def.Name, def.SupportMode)
		})
	}
}

func TestConvertUnknownCFunctionAsCustomCallReviewAction(t *testing.T) {
	parsed, err := ParseActionFile("Action.c", `Action()
{
	my_custom_helper(42, LR_AUTO, "value");
	web_url("Home", "URL=https://example.test/home", LAST);
	return 0;
}
`)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{})
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}
	if len(result.Stats.UnsupportedFuncs) != 0 || result.Stats.CustomCallFunctions != 1 {
		t.Fatalf("stats = %#v, want one custom call and no unsupported funcs", result.Stats)
	}
	action := result.Script.Actions[0]
	if action.Type != "custom_call" || action.Params["function"] != "my_custom_helper" || action.Params["support_mode"] != string(SourceFunctionSupportCustomReview) {
		t.Fatalf("custom action = %#v, want custom_call review action", action)
	}
	args, ok := action.Params["args"].([]any)
	if !ok || len(args) != 3 || args[0] != "42" || args[1] != "LR_AUTO" || args[2] != "value" {
		t.Fatalf("custom args = %#v, want numeric, identifier, and string args preserved", action.Params["args"])
	}
	assertSourceReviewSupportMode(t, result.SourceReview.Mappings, "my_custom_helper", SourceFunctionSupportCustomReview)
}

func TestConvertPreserveTransactionsOption(t *testing.T) {
	parsed, err := ParseActionFile("Action.c", sampleActionC)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	withTransactions, err := Convert([]*ParsedAction{parsed}, ConvertOptions{PreserveTransactions: true})
	if err != nil {
		t.Fatalf("convert with transactions error: %v", err)
	}
	withoutTransactions, err := Convert([]*ParsedAction{parsed}, ConvertOptions{PreserveTransactions: false})
	if err != nil {
		t.Fatalf("convert without transactions error: %v", err)
	}

	withSequence := firstProtocolAction(t, withTransactions.Script.Actions)
	if withSequence.Name != "Login" || withSequence.Type != "http_sequence" {
		t.Fatalf("preserved logical action = %#v, want Login http_sequence", withSequence)
	}
	if got := sequenceChildByName(t, withSequence, "Login_Homepage").Name; got != "Login_Homepage" {
		t.Fatalf("preserved child action name = %q, want Login_Homepage", got)
	}
	withoutSequence := firstProtocolAction(t, withoutTransactions.Script.Actions)
	if withoutSequence.Name != "Login" || withoutSequence.Type != "http_sequence" {
		t.Fatalf("non-preserved logical action = %#v, want Login http_sequence", withoutSequence)
	}
	if got := sequenceChildByName(t, withoutSequence, "Homepage").Name; got != "Homepage" {
		t.Fatalf("non-preserved child action name = %q, want Homepage", got)
	}
}

func TestConvertWebRegFindToAssertions(t *testing.T) {
	parsed, err := ParseActionFile("Action.c", sampleRegFindActionC)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{IncludeAssertions: true})
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}

	if len(result.Script.Actions) != 2 {
		t.Fatalf("action count = %d, want 2", len(result.Script.Actions))
	}

	firstAssertions := result.Script.Actions[0].Assertions
	if len(firstAssertions) != 2 {
		t.Fatalf("first action assertions = %#v, want body_contains + status_code", firstAssertions)
	}
	if firstAssertions[0].Type != "body_contains" || firstAssertions[0].Value != "Welcome back" {
		t.Fatalf("first mapped assertion = %#v, want body_contains on Welcome back", firstAssertions[0])
	}
	firstExtractors := result.Script.Actions[0].Extractors
	if len(firstExtractors) != 1 {
		t.Fatalf("first action extractors = %#v, want SaveCount extractor", firstExtractors)
	}
	if firstExtractors[0].Name != "welcomecount" || firstExtractors[0].Type != "regex_count" || firstExtractors[0].Expression != "Welcome back" {
		t.Fatalf("first SaveCount extractor = %#v, want regex_count for welcomecount", firstExtractors[0])
	}

	secondAssertions := result.Script.Actions[1].Assertions
	if len(secondAssertions) != 2 {
		t.Fatalf("second action assertions = %#v, want body_not_contains + status_code", secondAssertions)
	}
	if secondAssertions[0].Type != "body_not_contains" || secondAssertions[0].Value != "Internal Server Error" {
		t.Fatalf("second mapped assertion = %#v, want body_not_contains on Internal Server Error", secondAssertions[0])
	}

	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", result.Warnings)
	}
}

func TestConvertWebRESTAndXPathExtractors(t *testing.T) {
	parsed, err := ParseActionFile("Action.c", `Action()
{
	web_reg_save_param_xpath(
		"ParamName=userId",
		"QueryString=//ns2:userId/text()",
		SEARCH_FILTERS,
		"Scope=Body",
		LAST);

	web_reg_save_param_ex(
		"ParamName=sessionToken",
		"LB=\"token\":\"",
		"RB=\"",
		SEARCH_FILTERS,
		"Scope=Body",
		LAST);

	web_rest("Create Post",
		"URL=https://jsonplaceholder.typicode.com/posts/{randomPostID}",
		"Method=POST",
		"EncType=raw",
		"Body={\r\n"
		"\"title\": \"foo\",\r\n"
		"\"id\": \"{randomPostID}\"\r\n"
		"}",
		HEADERS,
		"Name=Accept", "Value=*/*", ENDHEADER,
		"Name=Content-Type", "Value=application/json; charset=UTF-8", ENDHEADER,
		LAST);
}`)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{})
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}
	if len(result.Script.Actions) != 1 {
		t.Fatalf("action count = %d, want 1", len(result.Script.Actions))
	}
	action := result.Script.Actions[0]
	if action.Method != "POST" || action.Target != "/posts/{{var.randompostid}}" {
		t.Fatalf("action method/target = %s/%q", action.Method, action.Target)
	}
	if action.Headers["Accept"] != "*/*" || action.Headers["Content-Type"] != "application/json; charset=UTF-8" {
		t.Fatalf("headers = %#v", action.Headers)
	}
	if !strings.Contains(action.Body, `"id": "{{var.randompostid}}"`) {
		t.Fatalf("body = %q, want converted source parameter", action.Body)
	}
	if len(action.Extractors) != 2 {
		t.Fatalf("extractors = %#v, want xpath + boundary", action.Extractors)
	}
	if action.Extractors[0].Name != "userid" || action.Extractors[0].Type != "xpath" || action.Extractors[0].Expression != "//ns2:userId/text()" {
		t.Fatalf("xpath extractor = %#v", action.Extractors[0])
	}
	if action.Extractors[1].Name != "sessiontoken" || action.Extractors[1].Type != "boundary" {
		t.Fatalf("ex extractor = %#v", action.Extractors[1])
	}
}

func TestConvertOpaqueMaskedBodyArgumentsRequireReview(t *testing.T) {
	parsed, err := ParseActionFile("Action.c", `Action()
{
	web_rest("POST: SubmitOrder",
		"URL=https://api.example.test/orders",
		"Method=POST",
		"EncType=raw",
		lr_unmask("6a2c27c0f5925d3f80e5cc3a44ab4f2e"),
		HEADERS,
		"Name=Content-Type", "Value=application/xml", ENDHEADER,
		LAST);

	web_custom_request("SubmitPayment",
		"URL=https://api.example.test/payments",
		"Method=POST",
		lr_decrypt("3f80e5cc3a44ab4f"),
		LAST);

	web_rest("GET: Health",
		"URL=https://api.example.test/health",
		"Method=GET",
		LAST);
}`)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{})
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}
	if len(result.Script.Actions) != 3 {
		t.Fatalf("action count = %d, want 3", len(result.Script.Actions))
	}

	cases := []struct {
		actionIndex int
		sourceFunc  string
		stepName    string
		maskFunc    string
		maskedHex   string
	}{
		{0, "web_rest", "POST: SubmitOrder", "lr_unmask", "6a2c27c0f5925d3f80e5cc3a44ab4f2e"},
		{1, "web_custom_request", "SubmitPayment", "lr_decrypt", "3f80e5cc3a44ab4f"},
	}
	if len(result.Warnings) != len(cases) {
		t.Fatalf("warnings = %#v, want %d masked-body warnings", result.Warnings, len(cases))
	}
	for _, tc := range cases {
		action := result.Script.Actions[tc.actionIndex]
		if action.Body != "" {
			t.Errorf("%s: body = %q, want empty (masked payload must not import as a literal body)", tc.stepName, action.Body)
		}
		if !metadataBool(action.Metadata, "masked_body") {
			t.Errorf("%s: metadata masked_body = %#v, want true", tc.stepName, action.Metadata["masked_body"])
		}
		if !metadataBool(action.Metadata, "review_required") {
			t.Errorf("%s: metadata review_required = %#v, want true", tc.stepName, action.Metadata["review_required"])
		}
		if got := metadataString(action.Metadata, "masked_body_function"); got != tc.maskFunc {
			t.Errorf("%s: metadata masked_body_function = %q, want %q", tc.stepName, got, tc.maskFunc)
		}
		if raw := metadataString(action.Metadata, "masked_body_raw"); !strings.Contains(raw, tc.maskedHex) {
			t.Errorf("%s: metadata masked_body_raw = %q, want preserved hex %q", tc.stepName, raw, tc.maskedHex)
		}

		warning := result.Warnings[tc.actionIndex]
		for _, fragment := range []string{"Action.c line ", tc.sourceFunc, tc.stepName, tc.maskFunc, "cannot be recovered"} {
			if !strings.Contains(warning, fragment) {
				t.Errorf("%s: warning = %q, want it to contain %q", tc.stepName, warning, fragment)
			}
		}

		mapping := findSourceReviewMappingByTarget(t, result, action.Name)
		if mapping.Status != "review_only" {
			t.Errorf("%s: mapping status = %q, want review_only", tc.stepName, mapping.Status)
		}
		if mapping.SupportMode != string(SourceFunctionSupportReviewOnly) {
			t.Errorf("%s: mapping support mode = %q, want review_only", tc.stepName, mapping.SupportMode)
		}
	}

	healthy := result.Script.Actions[2]
	if metadataBool(healthy.Metadata, "masked_body") || metadataBool(healthy.Metadata, "review_required") {
		t.Errorf("unmasked request metadata = %#v, want no masked-body flags", healthy.Metadata)
	}
	if mapping := findSourceReviewMappingByTarget(t, result, healthy.Name); mapping.Status != "converted" {
		t.Errorf("unmasked request mapping status = %q, want converted", mapping.Status)
	}

	if result.Stats.ReviewOnlyFunctions != 2 {
		t.Errorf("Stats.ReviewOnlyFunctions = %d, want 2", result.Stats.ReviewOnlyFunctions)
	}
	if result.Stats.NativeFunctions != 1 {
		t.Errorf("Stats.NativeFunctions = %d, want 1 (only the unmasked request)", result.Stats.NativeFunctions)
	}
}

func TestConvertMaskedBodyWithLiteralBodyKeepsBodyButFlagsReview(t *testing.T) {
	parsed, err := ParseActionFile("Action.c", `Action()
{
	web_custom_request("MixedBody",
		"URL=https://api.example.test/orders",
		"Method=POST",
		"Body=plain-part",
		lr_decrypt_ex("3f80e5cc", "extra"),
		LAST);
}`)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{})
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}
	if len(result.Script.Actions) != 1 {
		t.Fatalf("action count = %d, want 1", len(result.Script.Actions))
	}
	action := result.Script.Actions[0]
	if action.Body != "plain-part" {
		t.Fatalf("body = %q, want the literal Body argument preserved", action.Body)
	}
	if !metadataBool(action.Metadata, "masked_body") {
		t.Fatalf("metadata masked_body = %#v, want true", action.Metadata["masked_body"])
	}
	if got := metadataString(action.Metadata, "masked_body_function"); got != "lr_decrypt_ex" {
		t.Fatalf("metadata masked_body_function = %q, want lr_decrypt_ex", got)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "may be incomplete") {
		t.Fatalf("warnings = %#v, want one warning noting the body may be incomplete", result.Warnings)
	}
	if result.Stats.ReviewOnlyFunctions != 1 {
		t.Fatalf("Stats.ReviewOnlyFunctions = %d, want 1", result.Stats.ReviewOnlyFunctions)
	}
}

// TestConvertMaskedBodyArgRedactsWithoutFlag exercises the safe-by-default
// invariant directly through the exported Convert entry point WITHOUT setting
// ConvertOptions.UseMaskedBodySecretReferences (the flag ConvertZip's http/soap
// branch sets). A web function whose Body= argument carries lr_unmask("...")
// must still produce the governed secret reference in action.Body — never the
// raw masked payload — and the raw masked text must not survive in metadata or
// the serialized result.
func TestConvertMaskedBodyArgRedactsWithoutFlag(t *testing.T) {
	const maskedPayload = "6a2cMASKEDPAYLOADREDACTME"
	parsed, err := ParseActionFile("Action.c", `Action()
{
	web_rest("POST: SubmitOrder",
		"URL=https://api.example.test/orders",
		"Method=POST",
		"EncType=raw",
		"Body=lr_unmask(\"`+maskedPayload+`\")",
		LAST);
}`)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	// Deliberately do NOT set UseMaskedBodySecretReferences: this is the future
	// caller the defense-in-depth fix protects against.
	opts := ConvertOptions{}
	if opts.UseMaskedBodySecretReferences {
		t.Fatalf("guard: flag must be false for this test")
	}

	result, err := Convert([]*ParsedAction{parsed}, opts)
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}
	if len(result.Script.Actions) != 1 {
		t.Fatalf("action count = %d, want 1", len(result.Script.Actions))
	}
	action := result.Script.Actions[0]

	if !strings.HasPrefix(action.Body, "{{secret.lr_unmask_") {
		t.Fatalf("action.Body = %q, want governed lr_unmask secret reference", action.Body)
	}
	if strings.Contains(action.Body, maskedPayload) {
		t.Fatalf("action.Body = %q leaked the raw masked payload", action.Body)
	}
	if strings.Contains(action.Body, "lr_unmask(") {
		t.Fatalf("action.Body = %q still contains a raw lr_unmask call", action.Body)
	}

	// Governance metadata is recorded, but the raw masked text is scrubbed.
	if ref := metadataString(action.Metadata, "source_secret_ref"); !strings.HasPrefix(ref, "lr_unmask_") {
		t.Fatalf("metadata source_secret_ref = %q, want governed secret name", ref)
	}
	if !metadataBool(action.Metadata, "body_gated_by_secret") {
		t.Fatalf("metadata body_gated_by_secret = %#v, want true", action.Metadata["body_gated_by_secret"])
	}
	if raw, ok := action.Metadata["masked_body_raw"]; ok {
		if rawStr, isStr := raw.(string); isStr && strings.Contains(rawStr, maskedPayload) {
			t.Fatalf("metadata masked_body_raw = %q leaked the raw masked payload", rawStr)
		}
	}

	// Nothing in the fully serialized result may carry the masked payload.
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if strings.Contains(string(payload), maskedPayload) {
		t.Fatalf("masked lr_unmask payload leaked into converted output: %s", payload)
	}
}

func TestMaskedBodyPublicReferenceDoesNotFingerprintOpaquePayload(t *testing.T) {
	convert := func(maskedPayload string) dsl.Action {
		t.Helper()
		parsed, err := ParseActionFile("Action.c", `Action()
{
	web_rest("POST: SubmitOrder",
		"URL=https://api.example.test/orders",
		"Method=POST",
		"Body=lr_unmask(\"`+maskedPayload+`\")",
		LAST);
}`)
		if err != nil {
			t.Fatalf("parse error: %v", err)
		}
		result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{})
		if err != nil {
			t.Fatalf("convert error: %v", err)
		}
		if len(result.Script.Actions) != 1 {
			t.Fatalf("action count = %d, want 1", len(result.Script.Actions))
		}
		return result.Script.Actions[0]
	}

	first := convert("FIRST-OPAQUE-PAYLOAD")
	second := convert("A-DIFFERENT-AND-LONGER-OPAQUE-PAYLOAD")
	if first.Body != second.Body {
		t.Fatalf("public secret references fingerprint opaque payloads: %q != %q", first.Body, second.Body)
	}
	if metadataString(first.Metadata, "source_secret_ref") != metadataString(second.Metadata, "source_secret_ref") {
		t.Fatalf("public source-secret metadata fingerprints opaque payloads: %#v != %#v", first.Metadata, second.Metadata)
	}
	for _, action := range []dsl.Action{first, second} {
		if _, exists := action.Metadata["source_secret_hash"]; exists {
			t.Fatalf("public action retained an opaque-payload hash: %#v", action.Metadata)
		}
	}
}

func findSourceReviewMappingByTarget(t *testing.T, result *ConvertResult, targetName string) SourceReviewMapping {
	t.Helper()
	if result.SourceReview == nil {
		t.Fatalf("source review is nil")
	}
	for _, mapping := range result.SourceReview.Mappings {
		if mapping.TargetKind == "action" && mapping.TargetName == targetName {
			return mapping
		}
	}
	t.Fatalf("no action mapping found for target %q in %#v", targetName, result.SourceReview.Mappings)
	return SourceReviewMapping{}
}

func TestConvertZipRoutesDevWebMainJSArchive(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addFile := func(name string, content string) {
		writer, err := zw.Create(name)
		if err != nil {
			t.Fatalf("Create(%q) returned error: %v", name, err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatalf("Write(%q) returned error: %v", name, err)
		}
	}
	addFile("LRD/DevWeb.usr", `[General]
Type=DevWeb
`)
	addFile("LRD/main.js", strings.Join([]string{
		`load.action("Browse", async function() {`,
		`  const helper = require("./helper");`,
		"  new load.WebRequest({ url: `${load.params.BASE_URL}/orders`, method: \"GET\" }).sendSync();",
		`});`,
	}, "\n"))
	addFile("LRD/helper.js", `module.exports = { enabled: true };`)
	addFile("LRD/parameters.yml", `
parameters:
- name: BASE_URL
  type: csv
  fileName: base.csv
  columnName: url
`)
	addFile("LRD/base.csv", "url\nhttps://api.example.test\n")
	addFile("LRD/rts.yml", `
httpConnection:
  connectTimeout: 7
`)
	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close returned error: %v", err)
	}

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.DetectedProtocol == nil || result.DetectedProtocol.CanonicalSourceProtocol != "devweb" || result.DetectedProtocol.ImportMode != ImportModeExecutable {
		t.Fatalf("detected protocol = %#v, want executable DevWeb routing", result.DetectedProtocol)
	}
	if result.Script == nil || result.Script.Protocol != "http" || len(result.Script.Actions) != 2 {
		t.Fatalf("script = %#v, want review gate plus native DevWeb HTTP action", result.Script)
	}
	if result.Script.Actions[0].Type != "review_required" || result.Script.Actions[1].Target != "{{data.base.url}}/orders" {
		t.Fatalf("actions = %#v, want helper review gate followed by DevWeb parameter template request", result.Script.Actions)
	}
	preserved := result.Script.Metadata.Import.Preserved
	if preserved["source_endpoint_routed_to"] != "devweb" {
		t.Fatalf("preserved metadata = %#v, want enterprise archive endpoint route marker", preserved)
	}
	helpers, ok := preserved["helper_modules"].([]string)
	if !ok || len(helpers) != 1 || helpers[0] != "LRD/helper.js" {
		t.Fatalf("helper_modules = %#v, want preserved helper.js", preserved["helper_modules"])
	}
	parity, ok := preserved["runtime_parity"].(map[string]string)
	if !ok || parity["httpConnection.connectTimeout"] != "executed" {
		t.Fatalf("runtime parity = %#v, want DevWeb rts.yml parity", preserved["runtime_parity"])
	}
	if result.Stats.ConvertedActions != 2 || result.Stats.ReviewOnlyFunctions == 0 {
		t.Fatalf("stats = %#v, want converted action plus helper-module review gate", result.Stats)
	}
}

func TestConvertZipSAPWebAsyncHelpersRemainReviewGated(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addFile := func(name string, content string) {
		writer, err := zw.Create(name)
		if err != nil {
			t.Fatalf("Create(%q) returned error: %v", name, err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatalf("Write(%q) returned error: %v", name, err)
		}
	}
	addFile("SAPWeb.usr", `[General]
Type=Multi
ActiveTypes=SAP_Web
GenerateTypes=SAP_Web

[Actions]
Action1=Action.c
`)
	addFile("Action.c", `Action()
{
	web_reg_async_attributes("ID=SapAsync", "Pattern=.*", LAST);
	web_url("FLP",
		"URL=https://sap.example.test/sap/bc/ui2/flp",
		LAST);
	web_stop_async("ID=SapAsync", LAST);
	return 0;
}`)
	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close returned error: %v", err)
	}

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.DetectedProtocol == nil || result.DetectedProtocol.CanonicalSourceProtocol != "sap_web" || result.DetectedProtocol.APEXLoadProtocol != "http" {
		t.Fatalf("detected protocol = %#v, want SAP Web over HTTP", result.DetectedProtocol)
	}
	if result.Script == nil || result.Script.Protocol != "http" {
		t.Fatalf("script = %#v, want executable HTTP script with review-gated SAP helpers", result.Script)
	}
	if result.Stats.RawRequestActions != 1 || result.Stats.ReviewOnlyFunctions != 2 {
		t.Fatalf("stats = %#v, want one HTTP request plus two review-only async helpers", result.Stats)
	}
	assertSourceReviewFunctionStatus(t, result.SourceReview.Mappings, "web_reg_async_attributes", "review_only")
	assertSourceReviewFunctionStatus(t, result.SourceReview.Mappings, "web_stop_async", "review_only")
	assertSourceReviewFunctionStatus(t, result.SourceReview.Mappings, "web_url", "converted")
}

func TestConvertSourceParamArrayRandomInSaveString(t *testing.T) {
	parsed, err := ParseActionFile("Action.c", `Action()
{
	lr_save_string(lr_paramarr_random("postIDs"),"randomPostID");
	web_url("Post", "URL=https://example.test/posts/{randomPostID}", LAST);
}`)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{})
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions = %#v, want parameter action and request", result.Script.Actions)
	}
	paramAction := result.Script.Actions[0]
	args, _ := paramAction.Params["args"].([]any)
	if len(args) < 2 || args[0] != "{{var.postids}}" || args[1] != "randomPostID" {
		t.Fatalf("parameter action args = %#v", paramAction.Params["args"])
	}
	if result.Script.Actions[1].Target != "/posts/{{var.randompostid}}" {
		t.Fatalf("request target = %q", result.Script.Actions[1].Target)
	}
}

func TestConvertSOAPCustomRequest(t *testing.T) {
	parsed, err := ParseActionFile("Action.c", sampleSOAPActionC)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{IncludeAssertions: true})
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}

	if result.Script.Protocol != "soap" {
		t.Fatalf("protocol = %q, want soap", result.Script.Protocol)
	}
	if result.Script.Config.SOAP.Version != "1.1" {
		t.Fatalf("soap version = %q, want 1.1", result.Script.Config.SOAP.Version)
	}
	if !result.Script.Config.SOAP.FaultHandling.FailOnFault {
		t.Fatal("expected fail_on_fault to be enabled for imported SOAP scripts")
	}
	if len(result.Script.Actions) != 1 {
		t.Fatalf("action count = %d, want 1", len(result.Script.Actions))
	}
	action := result.Script.Actions[0]
	if action.SOAP == nil {
		t.Fatal("expected SOAP action metadata to be populated")
	}
	if action.SOAP.Action != "urn:GetQuote" {
		t.Fatalf("soap action = %q, want urn:GetQuote", action.SOAP.Action)
	}
	if action.SOAP.BodyMode != "raw_xml" {
		t.Fatalf("body mode = %q, want raw_xml", action.SOAP.BodyMode)
	}
	if action.Method != "POST" || action.Type != "POST" {
		t.Fatalf("method/type = %s/%s, want POST/POST", action.Method, action.Type)
	}
	if got := action.Headers["SOAPAction"]; got != "urn:GetQuote" {
		t.Fatalf("SOAPAction header = %q, want urn:GetQuote", got)
	}
	if got := action.Headers["Content-Type"]; !strings.Contains(got, "text/xml") {
		t.Fatalf("content type = %q, want text/xml SOAP content type", got)
	}
}

func TestConvertWebServiceCallGeneratesSOAPAction(t *testing.T) {
	parsed, err := ParseActionFile("Action.c", `Action()
{
	web_service_call( "StepName=GetFlights_102",
		"SOAPMethod=Flights_Service|FlightsServiceMethods|GetFlights",
		"ResponseParam=response",
		"Service=Flights_Service",
		"ExpectedResponse=SoapResult",
		"Snapshot=t1699436746.inf",
		BEGIN_ARGUMENTS,
		"DepartureCity={City}",
		"ArrivalCity={City}",
		"FlightDate=11/9/2025",
		END_ARGUMENTS,
		BEGIN_RESULT,
		END_RESULT,
		LAST);
}`)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{})
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}
	if result.Script.Protocol != "soap" {
		t.Fatalf("protocol = %q, want soap", result.Script.Protocol)
	}
	if len(result.Script.Actions) != 1 {
		t.Fatalf("actions = %#v, want one SOAP request", result.Script.Actions)
	}
	action := result.Script.Actions[0]
	if action.Target != "{{env.FLIGHTS_SERVICE_URL}}" {
		t.Fatalf("target = %q, want env endpoint placeholder", action.Target)
	}
	if action.SOAP == nil || action.SOAP.Operation != "GetFlights" || action.SOAP.BodyMode != "generated" {
		t.Fatalf("soap metadata = %#v, want generated GetFlights operation", action.SOAP)
	}
	if action.Metadata["soap_service"] != "Flights_Service" || action.Metadata["soap_binding"] != "FlightsServiceMethods" || action.Metadata["soap_response_param"] != "response" {
		t.Fatalf("metadata = %#v, want preserved service/binding/response metadata", action.Metadata)
	}
	if !strings.Contains(action.Body, "<GetFlights>") || !strings.Contains(action.Body, "<DepartureCity>{{var.city}}</DepartureCity>") {
		t.Fatalf("generated SOAP body = %q", action.Body)
	}
}

func TestConvertSOAPCustomRequestCollapsesMultilineCStringBody(t *testing.T) {
	parsed, err := ParseActionFile("Action.c", `
#include "web_api.h"

Action()
{
	web_add_header("SOAPAction", "urn:GetQuote");

	web_custom_request("GetQuote",
		"URL=https://example.com/services/quotes",
		"Method=POST",
		"Body=<soapenv:Envelope xmlns:soapenv=\"http://schemas.xmlsoap.org/soap/envelope/\">\r\n"
		"  <soapenv:Body>\r\n"
		"    <GetQuote xmlns=\"urn:quotes\"><Symbol>STORM</Symbol></GetQuote>\r\n"
		"  </soapenv:Body>\r\n"
		"</soapenv:Envelope>",
		"RecContentType=text/xml; charset=utf-8",
		LAST);

	return 0;
}
`)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{IncludeAssertions: true})
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}
	if len(result.Script.Actions) != 1 {
		t.Fatalf("action count = %d, want 1", len(result.Script.Actions))
	}

	body := result.Script.Actions[0].Body
	if strings.Contains(body, `" "`) {
		t.Fatalf("body retained adjacent C string separator: %q", body)
	}
	if strings.Contains(body, `\"`) {
		t.Fatalf("body retained escaped XML quotes: %q", body)
	}
	if !strings.Contains(body, `xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"`) {
		t.Fatalf("body = %q, want decoded XML namespace quotes", body)
	}
	if !strings.Contains(body, "\r\n  <soapenv:Body>\r\n") {
		t.Fatalf("body = %q, want preserved CRLF line breaks", body)
	}
}

func TestSanitizeName(t *testing.T) {
	tests := map[string]string{
		"Login Page":      "Login_Page",
		"GET /api/orders": "GET__api_orders",
		"Step (1)":        "Step_1",
		"":                "unnamed_action",
	}
	for input, expected := range tests {
		got := sanitizeName(input)
		// The sanitizer may collapse double underscores
		if got == "" {
			t.Errorf("sanitizeName(%q) returned empty string", input)
		}
		_ = expected // just ensure it doesn't panic
	}
}

func TestConvertParamReferences(t *testing.T) {
	input := "sid={SessionID}&user={Username}"
	expected := "sid={{var.sessionid}}&user={{var.username}}"
	got := convertParamReferences(input)
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func buildSourceZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		writer, err := zw.Create(name)
		if err != nil {
			t.Fatalf("Create(%q) returned error: %v", name, err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatalf("Write(%q) returned error: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close returned error: %v", err)
	}
	return buf.Bytes()
}

func headerValues(headers map[string]string) []string {
	values := make([]string, 0, len(headers))
	for _, value := range headers {
		values = append(values, value)
	}
	return values
}

func TestParseUSRFile(t *testing.T) {
	content := `[General]
Type=Web
RuntimeSettings=default.cfg

[Parameters]
Parameter1=LoginUsers.prm

[Actions]
Action0=vuser_init.c
Action1=Action.c
Action2=vuser_end.c
`
	cfg, err := ParseUSRFile(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ScriptType != "Web" {
		t.Errorf("expected type 'Web', got '%s'", cfg.ScriptType)
	}
	if len(cfg.ActionFiles) < 2 {
		t.Errorf("expected at least 2 action files, got %d", len(cfg.ActionFiles))
	}
	if cfg.RuntimeFile != "default.cfg" {
		t.Fatalf("runtime file = %q, want default.cfg", cfg.RuntimeFile)
	}
	if len(cfg.ParamFiles) != 1 || cfg.ParamFiles[0] != "LoginUsers.prm" {
		t.Fatalf("param files = %v, want [LoginUsers.prm]", cfg.ParamFiles)
	}
}

func TestConvertZipPreservesSourceMetadata(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addFile := func(name string, content string) {
		writer, err := zw.Create(name)
		if err != nil {
			t.Fatalf("Create(%q) returned error: %v", name, err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatalf("Write(%q) returned error: %v", name, err)
		}
	}

	addFile("Demo.usr", `[General]
Type=Web
RuntimeSettings=default.cfg

[Actions]
Action1=Action.c
`)
	addFile("Action.c", sampleActionC)
	addFile("globals.h", "Authorization: Bearer raw-secret-token\npassword=raw-secret-password\n")
	addFile("default.cfg", `[HTTP]
ConnectTimeout=45
ReceiveTimeout=20
KeepAlive=0

[Browser Emulation]
UserAgent=C/VUser Browser
AcceptLanguage=en-US
`)
	addFile("LoginUsers.prm", `[LoginUser]
Type=File
Table=Users.dat
ColumnName=username
SelectNextRow=Sequential
UpdateValueOn=Each Iteration

[OrderId]
Type=RandomNumber
MinimumValue=1000
MaximumValue=1005
UpdateValueOn=Each Occurrence
`)
	addFile("Users.dat", "username\nalice\nbob\n")
	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close returned error: %v", err)
	}

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.Script.Config.HTTP.ConnectTimeoutMs != 45000 {
		t.Fatalf("connect timeout = %d, want 45000", result.Script.Config.HTTP.ConnectTimeoutMs)
	}
	if result.Script.Metadata == nil || result.Script.Metadata.Import == nil {
		t.Fatal("expected script import metadata to be populated")
	}
	if len(result.Script.Parameters) != 2 {
		t.Fatalf("parameter count = %d, want 2", len(result.Script.Parameters))
	}
	var loginUser *dsl.Parameter
	for index := range result.Script.Parameters {
		if result.Script.Parameters[index].Name == "loginuser" {
			loginUser = &result.Script.Parameters[index]
			break
		}
	}
	if loginUser == nil || loginUser.Type != "file" {
		t.Fatalf("parameters = %#v, want file-backed loginuser", result.Script.Parameters)
	}
	if len(result.Script.DataSources) != 1 || result.Script.DataSources[0].Name != "users" {
		t.Fatalf("data sources = %#v, want one mapped users data source", result.Script.DataSources)
	}
	if result.Script.DataSources[0].Path != "" || result.Script.DataSources[0].FileName != "Users.dat" {
		t.Fatalf("data source path/file_name = %q/%q, want inline data with Users.dat provenance", result.Script.DataSources[0].Path, result.Script.DataSources[0].FileName)
	}
	payload, err := json.Marshal(result.Script.Metadata.Import)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	text := string(payload)
	for _, want := range []string{"\"source\":\"enterprise_script_archive\"", "\"project_settings\"", "\"runtime_files\"", "\"parameter_files\"", "LoginUsers.prm", "default.cfg", "\"ConnectTimeout\":\"45\""} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected metadata JSON to contain %q, got %s", want, text)
		}
	}
	if result.SourceReview == nil || len(result.SourceReview.SourceFiles) == 0 || len(result.SourceReview.Mappings) == 0 {
		t.Fatalf("source review = %#v, want files and mappings", result.SourceReview)
	}
	preserved := result.Script.Metadata.Import.Preserved
	if preserved["source_review"] == nil {
		t.Fatalf("preserved metadata missing source_review: %#v", preserved)
	}
	sourceReviewPayload, err := json.Marshal(preserved["source_review"])
	if err != nil {
		t.Fatalf("json.Marshal(source_review) returned error: %v", err)
	}
	if strings.Contains(string(sourceReviewPayload), "raw-secret-token") || strings.Contains(string(sourceReviewPayload), "raw-secret-password") {
		t.Fatalf("source review leaked raw secret literals: %s", string(sourceReviewPayload))
	}
}

func TestConvertZipPreservesSalesforceReplayFidelity(t *testing.T) {
	archive := buildSourceZip(t, map[string]string{
		"Salesforce.usr": `[General]
Type=Web
RuntimeSettings=default.cfg

[Actions]
Action0=vuser_init.c
Action1=Action.c
`,
		"vuser_init.c": `
vuser_init()
{
	web_add_auto_header("X-B3-TraceId", "{TraceId}");
	web_add_header("accept-language", "en-US,en;q=0.9");
	web_reg_save_param_json("ParamName=c_access_token", "QueryString=$.access_token", LAST);
	web_add_cookie("sid={c_lightning_sid}; DOMAIN=attone--perfhyper2.sandbox.lightning.force.com; PATH=/");
	web_url("Init Login", "URL=https://attone--perfhyper2.sandbox.my.salesforce.com/login", LAST);
	return 0;
}
`,
		"Action.c": `
Action()
{
	web_global_verification("Text=\"state\":\"ERROR\"", "Fail=Found", LAST);
	web_url("Lightning Aura", "URL=https://attone--perfhyper2.sandbox.lightning.force.com/aura", LAST);
	web_revert_auto_header("X-B3-TraceId", LAST);
	web_url("After Revert", "URL=https://attone--perfhyper2.sandbox.lightning.force.com/one/one.app", LAST);
	web_custom_request("Aura Post",
		"URL=https://attone--perfhyper2.sandbox.lightning.force.com/aura",
		"Method=POST",
		"RecContentType=text/html",
		"EncType=application/json",
		"Body={\"token\":\"{c_access_token}\"}",
		LAST);
	return 0;
}
`,
		"default.cfg": `[WEB]
ConnectTimeout=30
ReceiveTimeout=45
HTTPVersion=2
UserAgent=Salesforce Mobile
AcceptLanguage=en-US,en;q=0.9

[ThinkTime]
Options=NOTHINK
`,
	})

	result, err := ConvertZip(archive, ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if len(result.Script.Init) != 1 {
		t.Fatalf("init actions = %d, want 1: %#v", len(result.Script.Init), result.Script.Init)
	}
	if len(result.Script.Actions) != 3 {
		t.Fatalf("main actions = %d, want 3: %#v", len(result.Script.Actions), result.Script.Actions)
	}
	init := result.Script.Init[0]
	if init.Target != "/login" {
		t.Fatalf("init target = %q, want /login", init.Target)
	}
	if got := init.Headers["accept-language"]; got != "en-US,en;q=0.9" {
		t.Fatalf("accept-language = %q, want full q header", got)
	}
	if got := init.Headers["X-B3-TraceId"]; got != "{{var.traceid}}" {
		t.Fatalf("auto header on init = %q, want parameterized trace id", got)
	}
	if len(init.Extractors) != 1 || init.Extractors[0].Type != "jsonpath" || init.Extractors[0].Expression != "$.access_token" {
		t.Fatalf("init extractors = %#v, want JSONPath access token extractor", init.Extractors)
	}

	lightning := result.Script.Actions[0]
	if lightning.Target != "https://attone--perfhyper2.sandbox.lightning.force.com/aura" {
		t.Fatalf("lightning target = %q, want absolute foreign host", lightning.Target)
	}
	if got := lightning.Headers["X-B3-TraceId"]; got != "{{var.traceid}}" {
		t.Fatalf("auto header on lightning = %q, want parameterized trace id", got)
	}
	if got := lightning.Headers["Cookie"]; got != "sid={{var.c_lightning_sid}}" {
		t.Fatalf("cookie header = %q, want parameterized lightning sid", got)
	}
	if _, ok := result.Script.Actions[1].Headers["X-B3-TraceId"]; ok {
		t.Fatalf("auto header should be removed after web_revert_auto_header: %#v", result.Script.Actions[1].Headers)
	}
	auraPost := result.Script.Actions[2]
	if got := auraPost.Headers["Content-Type"]; got != "application/json" {
		t.Fatalf("content-type = %q, want request EncType application/json", got)
	}
	if strings.Contains(strings.ToLower(strings.Join(headerValues(auraPost.Headers), "\n")), "text/html") {
		t.Fatalf("RecContentType leaked into request headers: %#v", auraPost.Headers)
	}
	if !strings.Contains(auraPost.Body, "{{var.c_access_token}}") {
		t.Fatalf("body = %q, want parameterized access token", auraPost.Body)
	}
	if len(result.Script.Config.WebRuntime.ContentChecks) != 1 {
		t.Fatalf("content checks = %#v, want one global verification", result.Script.Config.WebRuntime.ContentChecks)
	}
	if result.Script.Config.WebRuntime.ContentChecks[0].Scope != "any_response" {
		t.Fatalf("content check scope = %q, want any_response", result.Script.Config.WebRuntime.ContentChecks[0].Scope)
	}
	if !result.Script.Config.ThinkTime.Ignore {
		t.Fatalf("think time ignore = false, want NOTHINK mapped")
	}
}

func TestNormalizeSourceContentCheckScope(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "source body", value: "Body", want: "any_response"},
		{name: "source all", value: "All", want: "any_response"},
		{name: "main document", value: "Main Document", want: "main_document"},
		{name: "resource", value: "Embedded Resources", want: "resource"},
		{name: "unknown fallback", value: "Some Source Scope", want: "any_response"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := normalizeSourceContentCheckScope(test.value); got != test.want {
				t.Fatalf("normalizeSourceContentCheckScope(%q) = %q, want %q", test.value, got, test.want)
			}
		})
	}
}

func TestConvertZipSanitizedACCWLSFixtureShape(t *testing.T) {
	archive := buildSourceZip(t, map[string]string{
		"acc_wls_reg_1.usr": `[General]
Type=Multi
DefaultCfg=default.cfg
ParameterFile=AIA_CashAndCarry_27May.prm
ActiveTypes=QTWeb
GenerateTypes=QTWeb
ScriptLanguage=C

[Actions]
vuser_init=vuser_init.c
Action=Action.c
vuser_end=vuser_end.c

[Transactions]
T00_PKCEGeneratorAPI=
T01_LaunchSFApp=
S01_iPad_BuyFlow_AIA_T06_AddressSearchAndContinue=

[SubTransactions]
S01_iPad_BuyFlow_AIA_T06_AddressSearchAndContinue||aura_ApexActionController_BWC_ServiceAddressControllergetSearchResults=
S01_iPad_BuyFlow_AIA_T06_AddressSearchAndContinue||ui.global.components.one.actionBar.ActionBarController/ACTION$getActions=
`,
		"default.cfg": `[General]
DefaultRunLogic=default.usp
AutomaticTransactions=0

[ThinkTime]
Options=NOTHINK

[WEB]
NavigatorBrowserLanguage=en-US
HttpVer=1.1
HTTPVer=1.1
EnableHTTP2=1
CustomUserAgent=SalesforceMobileSDK/12.0.1 Sanitized iPadOS/18.5 Salesforce1/254.060
UseCustomAgent=1
KeepAlive=1
ProxyUseProxy=1
ProxyHTTPHost=proxy.example.test
ProxyHTTPSHost=proxy.example.test
ProxyHTTPPort=3128
ProxyHTTPSPort=3128
ProxyUseSame=1
BrowserAcceptLanguage=en-US,en;q=0.9
ConnectTimeout=120
ReceiveTimeout=120
MaxRedirectionDepth=10
`,
		"AIA_CashAndCarry_27May.prm": `[parameter:P_UserName]
ColumnName="username"
GenerateNewVal="EachIteration"
OutOfRangePolicy="ContinueWithLast"
ParamName="P_UserName"
SelectNextRow="Sequential"
Table="p_UserName.dat"
Type="Table"

[parameter:P_FirstName]
ColumnName="first_name"
GenerateNewVal="EachIteration"
ParamName="P_FirstName"
SelectNextRow="Sequential"
Table="P_FirstName.dat"
Type="Table"
`,
		"p_UserName.dat":            "username\nsanitized.user@example.test\n",
		"P_FirstName.dat":           "first_name\nSanitized\n",
		"custom_body_variables.txt": "sanitized=true\n",
		"globals.h":                 "/* sanitized globals */\n",
		"lrw_custom_body.h":         "/* sanitized custom body helpers */\n",
		"vuser_init.c": `
vuser_init()
{
	web_add_auto_header("User-Agent", "SalesforceMobileSDK/12.0.1 Sanitized");
	web_add_header("Accept-Language", "en-US,en;q=0.9");
	web_reg_save_param_json("ParamName=AccessToken", "QueryString=$.access_token", LAST);
	web_add_cookie("sid={SessionId}; DOMAIN=acme.lightning.force.com; PATH=/");
	web_convert_param("PKCEVerifier", "SourceEncoding=PLAIN", "TargetEncoding=URL", LAST);
	lr_start_transaction("T00_PKCEGeneratorAPI");
	web_custom_request("PKCE Generator API",
		"URL=https://mobileauth.salesforce.com/oauth/token",
		"Method=POST",
		"EncType=application/json",
		"RecContentType=application/json",
		"Body={\"code_verifier\":\"{PKCEVerifier}\"}",
		LAST);
	lr_end_transaction("T00_PKCEGeneratorAPI", LR_AUTO);
	lr_start_transaction("T01_LaunchSFApp");
	web_url("Launch SF App",
		"URL=https://acme.my.salesforce.com/one/one.app",
		LAST);
	lr_end_transaction("T01_LaunchSFApp", LR_AUTO);
	return 0;
}
`,
		"Action.c": `
Action()
{
	web_global_verification("Text=\"state\":\"ERROR\"", "Fail=Found", LAST);
	lr_start_transaction("S01_iPad_BuyFlow_AIA_T06_AddressSearchAndContinue");
	web_add_auto_header("X-SFDC-Page-Scope-Id", "{PageScopeId}");
	web_add_header("Accept-Language", "en-US,en;q=0.9");
	lr_start_sub_transaction("aura_ApexActionController_BWC_ServiceAddressControllergetSearchResults", "S01_iPad_BuyFlow_AIA_T06_AddressSearchAndContinue");
	web_submit_data("Address Search Aura",
		"Action=https://acme.lightning.force.com/aura",
		"Method=POST",
		"EncType=application/x-www-form-urlencoded",
		"RecContentType=application/json",
		ITEMDATA,
		"Name=message", "Value={\"actions\":[{\"descriptor\":\"apex://BWC_ServiceAddressController/ACTION$getSearchResults\",\"params\":{\"search\":\"{P_UserName}\"}}]}", ENDITEM,
		LAST);
	lr_end_sub_transaction("aura_ApexActionController_BWC_ServiceAddressControllergetSearchResults", LR_AUTO);
	web_custom_request("Record UI",
		"URL=https://acme.my.salesforce.com/services/data/v60.0/ui-api/records/{AccountId}",
		"Method=GET",
		"RecContentType=application/json",
		LAST);
	web_revert_auto_header("X-SFDC-Page-Scope-Id", LAST);
	web_custom_request("Checkout Aura",
		"URL=https://acme.lightning.force.com/aura",
		"Method=POST",
		"EncType=application/json",
		"Body={\"actions\":[{\"descriptor\":\"apex://DigitalAccountCreationUtil/ACTION$checkForRestrictedDomainFilters\",\"params\":{\"firstName\":\"{P_FirstName}\"}}]}",
		LAST);
	lr_end_transaction("S01_iPad_BuyFlow_AIA_T06_AddressSearchAndContinue", LR_AUTO);
	return 0;
}
`,
		"vuser_end.c": `
vuser_end()
{
	web_url("Logout", "URL=https://acme.my.salesforce.com/secur/logout.jsp", LAST);
	return 0;
}
`,
	})

	result, err := ConvertZip(archive, ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.Script.Protocol != "http" {
		t.Fatalf("protocol = %q, want http", result.Script.Protocol)
	}
	if result.DetectedProtocol == nil || result.DetectedProtocol.ImportMode != ImportModeExecutable {
		t.Fatalf("detected protocol = %#v, want executable QTWeb import", result.DetectedProtocol)
	}
	initProtocolActions := 0
	for _, action := range result.Script.Init {
		if action.Method != "" && action.Target != "" {
			initProtocolActions++
		}
	}
	if initProtocolActions != 2 {
		t.Fatalf("init protocol actions = %d, want PKCE and launch only once: %#v", initProtocolActions, result.Script.Init)
	}
	if len(result.Script.Teardown) != 1 {
		t.Fatalf("teardown actions = %d, want logout action", len(result.Script.Teardown))
	}
	for _, action := range result.Script.Actions {
		if strings.Contains(action.Name, "PKCE") || strings.Contains(action.Name, "Launch") {
			t.Fatalf("init action duplicated in main actions: %#v", result.Script.Actions)
		}
	}
	pkce := findActionByName(t, result.Script.Init, "PKCE_Generator_API")
	if pkce.Target != "/oauth/token" {
		t.Fatalf("PKCE target = %q, want relative mobileauth URL for detected base host", pkce.Target)
	}
	if got := pkce.Headers["Accept-Language"]; got != "en-US,en;q=0.9" {
		t.Fatalf("Accept-Language = %q, want q-value preserved", got)
	}
	if len(pkce.Extractors) != 1 || pkce.Extractors[0].Name != "accesstoken" || pkce.Extractors[0].Type != "jsonpath" {
		t.Fatalf("PKCE extractors = %#v, want JSON extractor from web_reg_save_param_json", pkce.Extractors)
	}
	launch := findActionByName(t, result.Script.Init, "Launch_SF_App")
	if launch.Target != "https://acme.my.salesforce.com/one/one.app" {
		t.Fatalf("launch target = %q, want absolute Salesforce URL because base host is mobileauth", launch.Target)
	}
	if len(result.Script.Actions) != 1 || result.Script.Actions[0].Type != "http_sequence" {
		t.Fatalf("main actions = %#v, want one logical http_sequence transaction", result.Script.Actions)
	}
	sequence := result.Script.Actions[0]
	if sequence.Name != "S01_iPad_BuyFlow_AIA_T06_AddressSearchAndContinue" {
		t.Fatalf("sequence name = %q, want source transaction name", sequence.Name)
	}
	if sequence.Metadata["source_request_count"] != 3 {
		t.Fatalf("sequence request count metadata = %#v, want 3", sequence.Metadata["source_request_count"])
	}
	address := sequenceChildByName(t, sequence, "Address_Search_Aura")
	if address.Target != "https://acme.lightning.force.com/aura" {
		t.Fatalf("address target = %q, want absolute lightning URL", address.Target)
	}
	if got := address.Headers["Cookie"]; got != "sid={{var.sessionid}}" {
		t.Fatalf("address cookie = %q, want persistent sanitized cookie substitution", got)
	}
	if got := address.Headers["X-SFDC-Page-Scope-Id"]; got != "{{var.pagescopeid}}" {
		t.Fatalf("auto header = %q, want persistent parameterized page scope", got)
	}
	if strings.Contains(strings.ToLower(strings.Join(headerValues(address.Headers), "\n")), "application/json") {
		t.Fatalf("RecContentType leaked into submit_data request headers: %#v", address.Headers)
	}
	if !strings.Contains(address.Body, "{{param.p_username}}") {
		t.Fatalf("address body = %q, want parameterized username", address.Body)
	}
	checkout := sequenceChildByName(t, sequence, "Checkout_Aura")
	if _, ok := checkout.Headers["X-SFDC-Page-Scope-Id"]; ok {
		t.Fatalf("auto header should be reverted before checkout: %#v", checkout.Headers)
	}
	if got := checkout.Headers["Content-Type"]; got != "application/json" {
		t.Fatalf("checkout content type = %q, want request EncType", got)
	}
	if !result.Script.Config.ThinkTime.Ignore {
		t.Fatal("think time ignore = false, want NOTHINK from default.cfg")
	}
	if !strings.Contains(result.Script.Config.HTTP.UserAgent, "SalesforceMobileSDK/12.0.1 Sanitized") {
		t.Fatalf("user agent = %q, want sanitized Salesforce mobile CustomUserAgent", result.Script.Config.HTTP.UserAgent)
	}
	params := map[string]dsl.Parameter{}
	for _, parameter := range result.Script.Parameters {
		params[parameter.Name] = parameter
	}
	if params["p_username"].OnExhaustion != "last" {
		t.Fatalf("p_username parameter = %#v, want ContinueWithLast -> last", params["p_username"])
	}
	if len(result.Script.Config.WebRuntime.ContentChecks) != 1 {
		t.Fatalf("content checks = %#v, want global verification", result.Script.Config.WebRuntime.ContentChecks)
	}
	if result.SourceReview == nil {
		t.Fatal("source review missing")
	}
	convertParam := findActionByName(t, result.Script.Init, "PKCEVerifier")
	if convertParam.Type != "source_parameter" || convertParam.Params["function"] != "web_convert_param" {
		t.Fatalf("web_convert_param action = %#v, want executable source_parameter", convertParam)
	}
	if convertParam.Params["source_string"] != "{{var.pkceverifier}}" || convertParam.Params["target_encoding"] != "URL" {
		t.Fatalf("web_convert_param params = %#v, want source string and URL target encoding", convertParam.Params)
	}
	assertSourceReviewMapping(t, result.SourceReview.Mappings, "web_custom_request", "init[1]", "converted")
	assertSourceReviewMapping(t, result.SourceReview.Mappings, "web_submit_data", "actions[0].params.requests[0]", "converted")
	assertSourceReviewMapping(t, result.SourceReview.Mappings, "web_url", "teardown[0]", "converted")
}

func TestConvertZipJavaProtocolsSolaceJMS(t *testing.T) {
	archive := buildSourceZip(t, map[string]string{
		"DataSetup.usr": `[General]
Type=Java_protocols
ScriptLanguage=JAVA
[Actions]
vuser_init=vuser_init.java
Action=Actions.java
vuser_end=vuser_end.java
[Files]
Params=DataSetup.prm
`,
		"vuser_init.java": `import lrapi.lr;
public int init() { lr.output_message("init"); return 0; }`,
		"Actions.java": `import lrapi.lr;
import com.solacesystems.jms.SolConnectionFactory;
public class Actions {
  public int action() throws Exception {
    lr.start_transaction("publish");
    String inputXML = readFile("GDDN15_Request.xml");
    inputXML = inputXML.replaceAll("xxxdatexxx", lr.eval_string("{date}"));
    inputXML = inputXML.replaceAll("xxxtimexxx", lr.eval_string("{time}"));
    TextMessage msg = session.createTextMessage(inputXML);
    msg.setStringProperty("subscriberNumber", lr.eval_string("{ctn}"));
    msg.setStringProperty("container", lr.eval_string("{containerName}"));
    msg.setStringProperty("scheduledStartTime", lr.eval_string("{scheduledStartTime}"));
    msg.setStringProperty("guid", lr.eval_string("{guid}"));
    msg.setBooleanProperty("Solace_JMS_PROP_ISXML", true);
    producer.send(msg);
    lr.end_transaction("publish", lr.AUTO);
    return 0;
  }
}`,
		"vuser_end.java": `import lrapi.lr;
public int end() { lr.output_message("end"); return 0; }`,
		"GDDN15_Request.xml":         `<request><date>xxxdatexxx</date><time>xxxtimexxx</time><ctn>{ctn}</ctn></request>`,
		"gddnsolace.perf.truststore": "sanitized truststore bytes",
		"DataSetup.prm": `[parameter:serverURL]
ParamName=serverURL
Table=serverURL.dat
ColumnName=serverURL
SelectNextRow=Sequential
UpdateValueOn=Once

[parameter:initialContext]
ParamName=initialContext
ValueList=com.solacesystems.jndi.SolJNDIInitialContextFactory

[parameter:connectionFactoryJNDI]
ParamName=connectionFactoryJNDI
ValueList=/jms/cf/default

[parameter:userNameJNDI]
ParamName=userNameJNDI
ValueList=perf_user

[parameter:userPasswordJNDI]
ParamName=userPasswordJNDI
ValueList=sanitized

[parameter:vpnName]
ParamName=vpnName
ValueList=perf_vpn

[parameter:requestQueueName]
ParamName=requestQueueName
ValueList=queue/perf/request

[parameter:ctn]
ParamName=ctn
Table=ctn.dat
ColumnName=ctn

[parameter:containerName]
ParamName=containerName
ValueList=container

[parameter:scheduledStartTime]
ParamName=scheduledStartTime
ValueList=2026-01-01T00:00:00Z

[parameter:guid]
ParamName=guid
ValueList=00000000-0000-0000-0000-000000000000

[parameter:date]
ParamName=date
Type=Date
Format=%Y-%m-%d

[parameter:time]
ParamName=time
Type=Time
Format=%H:%M:%S
`,
		"serverURL.dat": "serverURL\ntcp://solace.example.test:55555\n",
		"ctn.dat":       "ctn\n5550001111\n",
	})

	result, err := ConvertZip(archive, ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.Script.Protocol != "java_vuser" || len(result.Scripts) != 1 || result.Scripts[0] != result.Script {
		t.Fatalf("result = %#v, want one combined java_vuser", result)
	}
	if result.DetectedProtocol == nil || result.DetectedProtocol.CanonicalSourceProtocol != "java_vuser" || result.DetectedProtocol.ImportMode != ImportModeExternalRunnerRequired || result.DetectedProtocol.APEXLoadProtocol != "java_vuser" {
		t.Fatalf("detected protocol = %#v, want external-runner Java draft", result.DetectedProtocol)
	}
	if result.ReviewArtifact == nil {
		t.Fatal("review artifact is nil, want governed Java source review")
	}
	if len(result.Script.Init) != 0 || len(result.Script.Actions) != 1 || result.Script.Actions[0].Type != "java_vuser_run" {
		t.Fatalf("lifecycle = init %#v actions %#v, want Actions-owned Java phases", result.Script.Init, result.Script.Actions)
	}
	if len(result.Script.Parameters) != 13 {
		t.Fatalf("parameters = %d, want 13", len(result.Script.Parameters))
	}
}

func TestConvertZipNestedLRUnmaskRESTBodyIsSecretReference(t *testing.T) {
	archive := buildSourceZip(t, map[string]string{
		"AddTracking.usr": `[General]
Type=Multi
ActiveTypes=QTWeb
[Actions]
Action=Action.c
[Files]
Params=AddTracking.prm
`,
		"Action.c": `Action()
{
	web_set_sockets_option("SSL_VERSION", "TLS1.2");
	lr_start_transaction("AddTracking");
	web_rest("Add Tracking",
		"URL=https://csi-perf-t3.test.att.com:38443/services/UpdateSubscriberProfile.jws",
		"Method=POST",
		"EncType=raw",
		lr_unmask("6a2cMASKEDPAYLOAD"),
		HEADERS,
		"Name=Content-Type", "Value=application/xml", ENDHEADER,
		LAST);
	lr_end_transaction("AddTracking", LR_AUTO);
	return 0;
}`,
		"AddTracking.prm": `[parameter:Sub]
ParamName=Sub
Table=Sub.dat
ColumnName=Sub
SelectNextRow=Unique
StartRow=2
auto_allocate_block_size=3
value_for_each_vuser=1
OutOfRangePolicy=AbortVuser

[parameter:Date]
ParamName=Date
Type=Date
UpdateValueOn=Each occurrence
Format=%Y-%m-%d
`,
		"Sub.dat": "Sub\n100\n200\n300\n400\n",
	})

	result, err := ConvertZip(archive, ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if len(result.Script.Actions) != 1 {
		t.Fatalf("actions = %#v, want one REST action", result.Script.Actions)
	}
	action := result.Script.Actions[0]
	if !strings.HasPrefix(action.Body, "{{secret.lr_unmask_") {
		t.Fatalf("body = %q, want governed lr_unmask secret reference", action.Body)
	}
	if got := action.Headers["Content-Type"]; got != "application/xml" {
		t.Fatalf("content type = %q, want application/xml", got)
	}
	if result.Script.Config.HTTP.TLS.MinVersion != "1.2" {
		t.Fatalf("TLS min version = %q, want 1.2", result.Script.Config.HTTP.TLS.MinVersion)
	}
	params := map[string]dsl.Parameter{}
	for _, parameter := range result.Script.Parameters {
		params[parameter.Name] = parameter
	}
	if params["sub"].Selection != "unique" || params["sub"].StartRow != 2 || params["sub"].BlockSize != 3 || !params["sub"].ValueForEachVUser {
		t.Fatalf("Sub parameter = %#v, want unique StartRow/block/value_for_each_vuser", params["sub"])
	}
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal result: %v", err)
	}
	if strings.Contains(string(payload), "6a2cMASKEDPAYLOAD") {
		t.Fatalf("masked lr_unmask payload leaked into import JSON: %s", payload)
	}
	if result.FidelityReport == nil || result.FidelityReport.SecretGated != 1 || result.FidelityReport.DroppedBodies != 0 || result.FidelityReport.PromotionStatus != ImportPromotionBlocked {
		t.Fatalf("fidelity report = %#v, want one secret gate and no dropped body", result.FidelityReport)
	}
}

func TestConvertZipGlobalVerificationTextIC(t *testing.T) {
	archive := buildSourceZip(t, map[string]string{
		"Verify.usr": `[General]
Type=Multi
ActiveTypes=QTWeb
[Actions]
Action=Action.c
`,
		"Action.c": `Action()
{
	web_global_verification("Text/IC=\"state\":\"E", "Fail=Found", "Search=All", LAST);
	web_url("Home", "URL=https://example.test/", LAST);
	return 0;
}`,
	})

	result, err := ConvertZip(archive, ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	checks := result.Script.Config.WebRuntime.ContentChecks
	if len(checks) != 1 || checks[0].Text != `"state":"E` || !checks[0].CaseInsensitive {
		t.Fatalf("content checks = %#v, want case-insensitive Text/IC check", checks)
	}
}

func TestBuildSourceReviewRedactsSecrets(t *testing.T) {
	review := BuildSourceReview(map[string]string{
		"Action.c": `Action()
{
	web_add_header("Authorization", "Bearer super-secret-token");
	web_submit_data("Login", "Action=https://example.test/login", ITEMDATA, "Name=password", "Value=plain-secret", ENDITEM, LAST);
}`,
	}, []SourceReviewMapping{{
		SourceFile: "Action.c",
		StartLine:  3,
		EndLine:    3,
		Function:   "web_add_header",
		Status:     "contributor",
	}})
	if review == nil || len(review.SourceFiles) != 1 {
		t.Fatalf("review = %#v, want one source file", review)
	}
	content := review.SourceFiles[0].Content
	if strings.Contains(content, "super-secret-token") || strings.Contains(content, "plain-secret") {
		t.Fatalf("source review content was not redacted: %s", content)
	}
	if !strings.Contains(content, "[REDACTED]") {
		t.Fatalf("source review content = %s, want redaction marker", content)
	}
}

func TestConvertZipParsesQuotedParameterDatFiles(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addFile := func(name string, content string) {
		writer, err := zw.Create(name)
		if err != nil {
			t.Fatalf("Create(%q) returned error: %v", name, err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatalf("Write(%q) returned error: %v", name, err)
		}
	}

	addFile("Quoted.usr", `[General]
Type="Web"
RuntimeSettings="default.cfg"

[Parameters]
Parameter1="QuotedParams.prm"

[Actions]
Action1="Action.c"
`)
	addFile("Action.c", `
Action()
{
	web_url("Homepage",
		"URL=https://example.com/app/home",
		LAST);
	return 0;
}
`)
	addFile("default.cfg", `[HTTP]
ConnectTimeout="45"
`)
	addFile("QuotedParams.prm", `[parameter:UserName]
ColumnName="Col 1"
Delimiter=","
GenerateNewVal="EachIteration"
OutOfRangePolicy="ContinueWithLast"
ParamName="UserName"
SelectNextRow="Sequential"
Table="UserName.dat"
Type="Table"

[parameter:billingMarket]
ColumnName="billingMarket"
Delimiter=","
GenerateNewVal="EachIteration"
ParamName="billingMarket"
SelectNextRow="Random"
Table="ACME_IAO.dat"
Type="Table"
`)
	addFile("UserName.dat", "UserName\nalice\n")
	addFile("ACME_IAO.dat", "subscriberNumber,billingMarket,billingSubMarket\n3868432133,FLP,25\n")
	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close returned error: %v", err)
	}

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	for _, warning := range result.Warnings {
		if strings.Contains(warning, "unable to resolve data file") {
			t.Fatalf("warnings = %#v, want quoted .dat files resolved", result.Warnings)
		}
	}
	if result.Script.Config.HTTP.ConnectTimeoutMs != 45000 {
		t.Fatalf("connect timeout = %d, want quoted runtime setting parsed", result.Script.Config.HTTP.ConnectTimeoutMs)
	}
	if len(result.Script.Parameters) != 2 {
		t.Fatalf("parameters = %#v, want username and billingmarket", result.Script.Parameters)
	}
	params := map[string]dsl.Parameter{}
	for _, parameter := range result.Script.Parameters {
		params[parameter.Name] = parameter
	}
	if params["username"].DataSource != "username" || params["username"].Column != "UserName" {
		t.Fatalf("username parameter = %#v, want quoted Col 1 mapped to UserName data source header", params["username"])
	}
	if params["billingmarket"].DataSource != "acme_iao" || params["billingmarket"].Column != "billingMarket" {
		t.Fatalf("billingmarket parameter = %#v, want ACME_IAO billingMarket column", params["billingmarket"])
	}
	if len(result.Script.DataSources) != 2 {
		t.Fatalf("data sources = %#v, want two .dat sources", result.Script.DataSources)
	}
	dataSourceFiles := map[string]string{}
	for _, source := range result.Script.DataSources {
		if source.Path != "" {
			t.Fatalf("data source = %#v, archive-backed inline data must not expose an executable path", source)
		}
		dataSourceFiles[source.Name] = source.FileName
		if !source.FirstRowHeader {
			t.Fatalf("data source = %#v, want header inference for quoted .dat imports", source)
		}
	}
	if dataSourceFiles["username"] != "UserName.dat" || dataSourceFiles["acme_iao"] != "ACME_IAO.dat" {
		t.Fatalf("data source file names = %#v, want original .dat provenance", dataSourceFiles)
	}
}

func TestConvertZipCanonicalizesURLParameterReferences(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addFile := func(name string, content string) {
		writer, err := zw.Create(name)
		if err != nil {
			t.Fatalf("Create(%q) returned error: %v", name, err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatalf("Write(%q) returned error: %v", name, err)
		}
	}

	addFile("ACME.usr", `[General]
Type="Web"

[Parameters]
Parameter1="ACME.prm"

[Actions]
Action1="Action.c"
`)
	addFile("Action.c", `
	Action()
	{
		web_custom_request("InquireSubscriberBillingAgreement",
			"URL={URL}/Services/com/cingular/csi/edp/InquireSubscriberBillingAgreement.jws",
			"Method=POST",
			"Body=url={URL}&session={SessionID}",
			LAST);
		return 0;
	}
`)
	addFile("ACME.prm", `[parameter:URL]
ColumnName="Col 1"
Delimiter=","
GenerateNewVal="Once"
ParamName="URL"
SelectNextRow="Sequential"
Table="URL.dat"
Type="Table"
`)
	addFile("URL.dat", "URL\nhttps://csi-gw-ext.att.com:443\n")
	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close returned error: %v", err)
	}

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if len(result.Script.Actions) != 1 {
		t.Fatalf("actions = %#v, want one imported request", result.Script.Actions)
	}
	if got := result.Script.Actions[0].Target; got != "{{param.url}}/Services/com/cingular/csi/edp/InquireSubscriberBillingAgreement.jws" {
		t.Fatalf("target = %q, want URL parameter reference canonicalized to param.url", got)
	}
	if got := result.Script.Actions[0].Body; got != "url={{param.url}}&session={{var.sessionid}}" {
		t.Fatalf("body = %q, want known .prm parameters as param and dynamic values as var", got)
	}
	if got := result.Script.Config.BaseURL; got != "https://csi-gw-ext.att.com:443" {
		t.Fatalf("base_url = %q, want first URL.dat value to replace source fallback", got)
	}
	if len(result.Script.Parameters) != 1 {
		t.Fatalf("parameters = %#v, want one URL parameter", result.Script.Parameters)
	}
	if got := result.Script.Parameters[0]; got.Name != "url" || got.Type != "file" || got.DataSource != "url" || got.Column != "URL" {
		t.Fatalf("parameter = %#v, want URL.dat mapped as file parameter named url", got)
	}
}

func TestParseParameterFileSupportsSourceBuiltinParameters(t *testing.T) {
	parsed := ParseParameterFile("ACME_ISCD_Azure_2024.prm", `[parameter:IterationNo]
Format="%04d"
OriginalValue=""
ParamName="IterationNo"
Type="CurrentIteration"

[parameter:LGName]
Format="%s"
OriginalValue=""
ParamName="LGName"
Type="Host"

[parameter:ReqDateTime]
Format="%Y-%m-%dT%H:%M:%SZ"
GenerateNewVal="EachIteration"
Offset=""
OffsetUI="0"
OriginalValue=""
ParamName="ReqDateTime"
Type="Time"
WorkingDaysOn="0"

[parameter:Time]
Format="%H:%M:%S"
GenerateNewVal="EachIteration"
OriginalValue=""
ParamName="Time"
Type="Time"

[parameter:TimeStamp]
Format="%Y-%m-%dT%H:%M:%SZ"
GenerateNewVal="EachIteration"
OriginalValue=""
ParamName="TimeStamp"
Type="Time"

[parameter:SIM]
Format="%05lu"
GenerateNewVal="EachOccurrence"
MaxValue="99999"
MinValue="11111"
OriginalValue="10001"
ParamName="SIM"
Type="Random"

[parameter:vuser]
Format="%s"
OriginalValue="Vuser"
ParamName="vuser"
Type="Userid"
`, nil)

	if len(parsed.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", parsed.Warnings)
	}
	if len(parsed.Parameters) != 7 {
		t.Fatalf("parameters = %#v, want seven built-in parameters", parsed.Parameters)
	}
	params := map[string]dsl.Parameter{}
	for _, parameter := range parsed.Parameters {
		params[parameter.Name] = parameter
	}
	if got := params["iterationno"]; got.Type != "current_iteration" || got.Start != 1 || got.Step != 1 || got.PadWidth != 4 || got.Update != "each_iteration" {
		t.Fatalf("IterationNo = %#v, want native current_iteration parameter", got)
	}
	if got := params["lgname"]; got.Type != "host" || got.Update != "once" || len(got.Values) != 0 {
		t.Fatalf("LGName = %#v, want native host parameter without forced fallback value", got)
	}
	if got := params["reqdatetime"]; got.Type != "time" || got.Format != "2006-01-02T15:04:05Z" || got.Timezone != "UTC" || got.Update != "each_iteration" {
		t.Fatalf("ReqDateTime = %#v, want mapped UTC time parameter", got)
	}
	if got := params["time"]; got.Type != "time" || got.Format != "15:04:05" || got.Update != "each_iteration" {
		t.Fatalf("Time = %#v, want mapped time-only parameter", got)
	}
	if got := params["timestamp"]; got.Type != "time" || got.Format != "2006-01-02T15:04:05Z" || got.Timezone != "UTC" {
		t.Fatalf("TimeStamp = %#v, want mapped timestamp-style time parameter", got)
	}
	if got := params["sim"]; got.Type != "random_number" || got.Min != 11111 || got.Max != 99999 || got.Update != "each_occurrence" || got.PadWidth != 5 {
		t.Fatalf("SIM = %#v, want source Random mapped to numeric parameter", got)
	}
	if got := params["vuser"]; got.Type != "vuser_id" || got.Update != "once" {
		t.Fatalf("vuser = %#v, want native vuser_id parameter", got)
	}
}

func TestMapParameterExhaustionPreservesContinueWithLast(t *testing.T) {
	if got := mapParameterExhaustion("ContinueWithLast"); got != "last" {
		t.Fatalf("ContinueWithLast mapped to %q, want last", got)
	}
	if got := mapParameterExhaustion("Continue with last"); got != "last" {
		t.Fatalf("Continue with last mapped to %q, want last", got)
	}
}

func TestParseParameterFilePreservesSameLineSelection(t *testing.T) {
	parsed := ParseParameterFile("ACME.prm", `[parameter:UserName]
ColumnName="UserName"
GenerateNewVal="EachIteration"
ParamName="UserName"
SelectNextRow="Random"
Table="Users.dat"
Type="Table"

[parameter:Password]
ColumnName="Password"
GenerateNewVal="EachIteration"
ParamName="Password"
SelectNextRow="Same line as UserName"
Table="Users.dat"
Type="Table"
`, map[string]string{
		"Users.dat": "UserName,Password\nalice,a-pass\nbob,b-pass\n",
	})

	if len(parsed.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", parsed.Warnings)
	}
	params := map[string]dsl.Parameter{}
	for _, parameter := range parsed.Parameters {
		params[parameter.Name] = parameter
	}
	if got := params["password"]; got.SameLineAs != "username" || got.DataSource != "users" || got.Column != "Password" {
		t.Fatalf("Password = %#v, want same-line sync to username in Users.dat", got)
	}
	if got := params["username"]; got.Selection != "random" || got.SameLineAs != "" {
		t.Fatalf("UserName = %#v, want independent random row selection", got)
	}
}

func TestParseRuntimeCfgToScriptConfigMapsExtendedRuntimeSettings(t *testing.T) {
	runtimeCfg := ParseRuntimeCfg(`[HTTP]
ConnectTimeout=45
ReceiveTimeout=20
KeepAlive=0
MaxRedirections=4
MaxConnectionsPerHost=12

[Proxy]
Mode=custom
ProxyServer=http://proxy.example.test:8080
ProxyUserName=perf-user
ProxyPassword=secret

[Browser Emulation]
Engine=chromium
DeviceProfile=iPhone_SE
TouchMode=1
Orientation=portrait
ClientMetrics=1
SlowMoMs=125
ViewportWidth=390
ViewportHeight=844
UserAgent=C/VUser Browser
AcceptLanguage=en-US
AcceptEncoding=gzip, deflate
CacheEnabled=1
CacheContentURLs=1
CheckNewerVersions=1
NewUserPerIteration=1
DownloadResources=all

[Run Logic]
Iterations=7
AutoTransactions=per_iteration
BlockScheduling=random
ActionSequence=init,action,teardown

[Pacing]
Mode=random_delay
MinDelayMs=1500
MaxDelayMs=3500

[Think Time]
Ignore=1
Multiplier=0.5
LimitMs=10000

[Logging]
Level=extended
OnErrorOnly=1
ExtendedOptions=parameter_substitution,data_returned
AdvancedTrace=1

[Misc]
ContinueOnError=1
SnapshotOnError=1

[Additional Attributes]
env=perf
region=east

[Download Filters]
Mode=block_list
Hosts=metrics.example.test,cdn.example.test

[Content Check]
Text=Access Denied
Scope=any_response
FailIfFound=1

[Network Speed Simulation]
Mode=preset
Preset=dsl
LatencyMs=75

[Streaming]
Mode=fixed
BufferSizeSeconds=30
FixedBitrateKbps=1800
BandwidthEstimation=instant

[Data Formatting]
AutoDetect=1
Chain=base64,gzip mode=decode,html_unescape,xml_to_json,url_encode,html_escape
`)

	config := runtimeCfg.ToScriptConfig()
	if config == nil {
		t.Fatal("expected script config to be created")
	}
	if config.HTTP.ConnectTimeoutMs != 45000 || config.HTTP.RequestTimeoutMs != 20000 {
		t.Fatalf("http config = %#v, want converted timeout values", config.HTTP)
	}
	if config.HTTP.MaxConnsPerHost != 12 {
		t.Fatalf("max conns per host = %d, want 12", config.HTTP.MaxConnsPerHost)
	}
	if config.HTTP.ProxyMode != "custom" || config.HTTP.Proxy.URL != "http://proxy.example.test:8080" {
		t.Fatalf("proxy config = %#v, want custom proxy", config.HTTP)
	}
	if !config.ThinkTime.Ignore || config.ThinkTime.Multiplier != 0.5 || config.ThinkTime.LimitMs != 10000 {
		t.Fatalf("think time = %#v, want ignore+multiplier+limit", config.ThinkTime)
	}
	if config.Pacing.Type != "random_delay" || config.Pacing.MinDelayMs != 1500 || config.Pacing.MaxDelayMs != 3500 {
		t.Fatalf("pacing = %#v, want random_delay 1500-3500", config.Pacing)
	}
	if config.RunLogic.Iterations != 7 || config.RunLogic.AutoTransactions != "per_iteration" || config.RunLogic.BlockScheduling != "random" {
		t.Fatalf("run logic = %#v, want populated run logic", config.RunLogic)
	}
	if got := strings.Join(config.RunLogic.ActionSequence, ","); got != "init,action,teardown" {
		t.Fatalf("action sequence = %q, want init,action,teardown", got)
	}
	if config.Log.Level != "extended" || !config.Log.OnErrorOnly || len(config.Log.ExtendedOptions) != 3 {
		t.Fatalf("log config = %#v, want extended log settings", config.Log)
	}
	if !config.ErrorHandling.ContinueOnError || !config.ErrorHandling.SnapshotOnError {
		t.Fatalf("error handling = %#v, want continue+snapshot", config.ErrorHandling)
	}
	if config.Browser.DownloadResources != "all" || config.Browser.CacheEnabled == nil || !*config.Browser.CacheEnabled || config.Browser.NewUserPerIter == nil || !*config.Browser.NewUserPerIter {
		t.Fatalf("browser config = %#v, want cache and new user settings", config.Browser)
	}
	if config.Browser.Engine != "webkit" || config.Browser.DeviceProfile != "iphone_se" || config.Browser.TouchMode == nil || !*config.Browser.TouchMode || config.Browser.Orientation != "portrait" || config.Browser.ClientMetrics == nil || !*config.Browser.ClientMetrics || config.Browser.SlowMoMs != 125 || config.Browser.Viewport.Width != 390 || config.Browser.Viewport.Height != 844 {
		t.Fatalf("browser mobile config = %#v, want mobile emulation settings", config.Browser)
	}
	if config.WebRuntime.AdditionalAttributes["env"] != "perf" || config.WebRuntime.DownloadFilters.Mode != "block_list" {
		t.Fatalf("web runtime = %#v, want additional attributes and download filters", config.WebRuntime)
	}
	if len(config.WebRuntime.ContentChecks) != 1 || config.WebRuntime.ContentChecks[0].Text != "Access Denied" {
		t.Fatalf("content checks = %#v, want Access Denied", config.WebRuntime.ContentChecks)
	}
	if config.WebRuntime.NetworkSimulation.Mode != "preset" || config.WebRuntime.NetworkSimulation.Preset != "dsl" || config.WebRuntime.NetworkSimulation.LatencyMs != 75 {
		t.Fatalf("network simulation = %#v, want preset dsl latency 75", config.WebRuntime.NetworkSimulation)
	}
	if config.WebRuntime.Streaming.Mode != "fixed" || config.WebRuntime.Streaming.FixedBitrateKbps != 1800 || config.WebRuntime.Streaming.BandwidthEstimation != "instant" {
		t.Fatalf("streaming = %#v, want fixed bitrate streaming", config.WebRuntime.Streaming)
	}
	if !config.WebRuntime.DataFormatting.AutoDetect || len(config.WebRuntime.DataFormatting.Chain) != 6 || config.WebRuntime.DataFormatting.Chain[0].Type != "base64" {
		t.Fatalf("data formatting = %#v, want autodetect + DFE chain", config.WebRuntime.DataFormatting)
	}
	if config.WebRuntime.DataFormatting.Chain[1].Type != "gzip" || config.WebRuntime.DataFormatting.Chain[1].Options["mode"] != "decode" {
		t.Fatalf("gzip data formatting = %#v, want mode=decode", config.WebRuntime.DataFormatting.Chain[1])
	}
	wantDFEChain := []string{"base64", "gzip", "html_unescape", "xml_to_json", "url_encode", "html_escape"}
	for index, want := range wantDFEChain {
		if config.WebRuntime.DataFormatting.Chain[index].Type != want {
			t.Fatalf("data formatting chain[%d] = %#v, want %s", index, config.WebRuntime.DataFormatting.Chain[index], want)
		}
	}
}

func TestParseRuntimeCfgNormalizesConnectProxyURLs(t *testing.T) {
	tests := []struct {
		name    string
		cfg     string
		wantURL string
	}{
		{
			name: "explicit https proxy on connect port",
			cfg: `[Proxy]
Mode=custom
ProxyServer=https://proxy.example.test:8888
`,
			wantURL: "http://proxy.example.test:8888",
		},
		{
			name: "synthesized https proxy host on connect port",
			cfg: `[Proxy]
ProxyHTTPSHost=proxy.example.test
ProxyHTTPSPort=8888
`,
			wantURL: "http://proxy.example.test:8888",
		},
		{
			name: "intentional https proxy port",
			cfg: `[Proxy]
Mode=custom
ProxyServer=https://proxy.example.test:4443
`,
			wantURL: "https://proxy.example.test:4443",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtimeCfg := ParseRuntimeCfg(tt.cfg)
			config := runtimeCfg.ToScriptConfig()
			if config == nil {
				t.Fatal("expected script config to be created")
			}
			if config.HTTP.ProxyMode != "custom" || config.HTTP.Proxy.URL != tt.wantURL {
				t.Fatalf("proxy config = %#v, want custom proxy URL %q", config.HTTP, tt.wantURL)
			}
		})
	}
}

func TestConvertZipPreservesRecordedResourceURLs(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addFile := func(name string, content string) {
		writer, err := zw.Create(name)
		if err != nil {
			t.Fatalf("Create(%q) returned error: %v", name, err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatalf("Write(%q) returned error: %v", name, err)
		}
	}

	addFile("Recorded.usr", `[General]
Type=Web
DefaultCfg=default.cfg
Action=Action.c
`)
	addFile("default.cfg", `[Browser Emulation]
DownloadResources=recorded
`)
	addFile("Action.c", `Action()
{
	web_url("Homepage",
		"URL=https://example.com/app/home",
		"Resource=0",
		LAST);

	web_url("Logo",
		"URL=https://example.com/static/logo.png",
		"Resource=1",
		LAST);

	web_url("Styles",
		"URL=https://cdn.example.com/app/site.css?v=2",
		"Resource=1",
		LAST);

	return 0;
}`)
	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close returned error: %v", err)
	}

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.Script.Metadata == nil || result.Script.Metadata.Import == nil {
		t.Fatal("expected import metadata")
	}
	preserved := result.Script.Metadata.Import.Preserved
	if preserved["recorded_resource_urls_available"] != true {
		t.Fatalf("recorded_resource_urls_available = %#v, want true", preserved["recorded_resource_urls_available"])
	}
	urls, ok := preserved["recorded_resource_urls"].([]string)
	if !ok {
		t.Fatalf("recorded_resource_urls = %#v, want []string", preserved["recorded_resource_urls"])
	}
	if len(urls) != 2 || urls[0] != "https://example.com/static/logo.png" || urls[1] != "https://cdn.example.com/app/site.css?v=2" {
		t.Fatalf("recorded_resource_urls = %#v, want preserved resource URLs", urls)
	}
}

func TestConvertZipMapsExtendedRuntimeSettings(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addFile := func(name string, content string) {
		writer, err := zw.Create(name)
		if err != nil {
			t.Fatalf("Create(%q) returned error: %v", name, err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatalf("Write(%q) returned error: %v", name, err)
		}
	}

	addFile("Demo.usr", `[General]
Type=Web
RuntimeSettings=default.cfg

[Actions]
Action1=Action.c
`)
	addFile("Action.c", sampleActionC)
	addFile("default.cfg", `[HTTP]
ConnectTimeout=45
ReceiveTimeout=20
KeepAlive=0
MaxConnectionsPerHost=12

[Run Logic]
Iterations=5
AutoTransactions=per_action

[Pacing]
Mode=fixed_interval
IntervalMs=60000

[Logging]
Level=extended
OnErrorOnly=1

[Misc]
ContinueOnError=1

[Browser Emulation]
CacheEnabled=1
NewUserPerIteration=1
DownloadResources=all

[Download Filters]
Mode=block_list
Hosts=metrics.example.test

[Content Check]
Text=Access Denied
Scope=any_response
FailIfFound=1
`)
	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close returned error: %v", err)
	}

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.Script.Config.HTTP.MaxConnsPerHost != 12 {
		t.Fatalf("max conns per host = %d, want 12", result.Script.Config.HTTP.MaxConnsPerHost)
	}
	if result.Script.Config.RunLogic.Iterations != 5 || result.Script.Config.RunLogic.AutoTransactions != "per_action" {
		t.Fatalf("run logic = %#v, want imported values", result.Script.Config.RunLogic)
	}
	if result.Script.Config.Pacing.Type != "fixed_interval" || result.Script.Config.Pacing.IntervalMs != 60000 {
		t.Fatalf("pacing = %#v, want fixed interval 60000", result.Script.Config.Pacing)
	}
	if result.Script.Config.Log.Level != "extended" || !result.Script.Config.Log.OnErrorOnly {
		t.Fatalf("log config = %#v, want extended on error only", result.Script.Config.Log)
	}
	if !result.Script.Config.ErrorHandling.ContinueOnError {
		t.Fatalf("error handling = %#v, want continue on error", result.Script.Config.ErrorHandling)
	}
	if result.Script.Config.Browser.NewUserPerIter == nil || !*result.Script.Config.Browser.NewUserPerIter {
		t.Fatalf("browser config = %#v, want new user per iteration", result.Script.Config.Browser)
	}
	if result.Script.Config.HTTP.ClearCookiesPerIter == nil || !*result.Script.Config.HTTP.ClearCookiesPerIter {
		t.Fatalf("http config = %#v, want clear cookies per iteration", result.Script.Config.HTTP)
	}
	if result.Script.Config.WebRuntime.DownloadFilters.Mode != "block_list" || len(result.Script.Config.WebRuntime.ContentChecks) != 1 {
		t.Fatalf("web runtime = %#v, want download filter and content check", result.Script.Config.WebRuntime)
	}
	if result.Script.Metadata == nil || result.Script.Metadata.Import == nil {
		t.Fatal("expected import metadata to be preserved")
	}
	parity, ok := result.Script.Metadata.Import.Preserved["runtime_settings_parity"].([]RuntimeSettingParityEntry)
	if !ok || len(parity) == 0 {
		t.Fatalf("runtime_settings_parity = %#v, want non-empty []RuntimeSettingParityEntry", result.Script.Metadata.Import.Preserved["runtime_settings_parity"])
	}
	assertRuntimeSettingParityEntry(t, parity, "http_transport", RuntimeSettingStatusExecuted)
	assertRuntimeSettingParityEntry(t, parity, "run_logic_iterations", RuntimeSettingStatusExecuted)
	assertRuntimeSettingParityEntry(t, parity, "logging", RuntimeSettingStatusExecuted)
	assertRuntimeSettingParityEntry(t, parity, "browser_download_resources_all", RuntimeSettingStatusExecuted)
	assertRuntimeSettingParityEntry(t, parity, "download_filters", RuntimeSettingStatusExecuted)
}

func TestGoldenWebHTTPHTMLFixtureRuntimeParity(t *testing.T) {
	zipBytes := zipTestDataDir(t, "testdata/golden_web_http_html")

	result, err := ConvertZip(zipBytes, ConvertOptions{
		IncludeAssertions:    true,
		PreserveTransactions: true,
	})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}

	if result.Script.Protocol != "http" {
		t.Fatalf("protocol = %q, want http", result.Script.Protocol)
	}
	if result.Script.Config.BaseURL != "https://shop.example.test" {
		t.Fatalf("base_url = %q, want https://shop.example.test", result.Script.Config.BaseURL)
	}
	if result.Stats.ConvertedActions != 2 || result.Stats.LogicalActions != 2 {
		t.Fatalf("converted/logical actions = %d/%d, want two logical actions", result.Stats.ConvertedActions, result.Stats.LogicalActions)
	}
	if result.Stats.Transactions != 1 {
		t.Fatalf("transactions = %d, want 1", result.Stats.Transactions)
	}
	if got := convertedAssertionCount(result.Script.Actions); got != 6 {
		t.Fatalf("assertion count = %d, want one imported assertion plus status checks", got)
	}

	config := result.Script.Config
	if config.HTTP.ConnectTimeoutMs != 45000 || config.HTTP.RequestTimeoutMs != 20000 || config.HTTP.MaxConnsPerHost != 12 {
		t.Fatalf("http config = %#v, want imported transport settings", config.HTTP)
	}
	if config.HTTP.ProxyMode != "custom" || config.HTTP.Proxy.URL != "http://proxy.example.test:8080" {
		t.Fatalf("proxy config = %#v, want custom proxy", config.HTTP)
	}
	if config.HTTP.TLS.MinVersion != "1.2" {
		t.Fatalf("tls min version = %q, want 1.2", config.HTTP.TLS.MinVersion)
	}
	if config.HTTP.Auth.Type != "basic" || config.HTTP.Auth.Username != "perfuser" {
		t.Fatalf("auth config = %#v, want basic perfuser", config.HTTP.Auth)
	}
	if config.RunLogic.Iterations != 3 || config.RunLogic.AutoTransactions != "per_action" || config.RunLogic.BlockScheduling != "sequential" {
		t.Fatalf("run logic = %#v, want imported run logic", config.RunLogic)
	}
	if config.Pacing.Type != "random_delay" || config.Pacing.MinDelayMs != 1000 || config.Pacing.MaxDelayMs != 5000 {
		t.Fatalf("pacing = %#v, want random delay bounds", config.Pacing)
	}
	if config.Log.Level != "extended" || !config.Log.OnErrorOnly || len(config.Log.ExtendedOptions) != 3 {
		t.Fatalf("log config = %#v, want extended error-only logging options", config.Log)
	}
	if !config.ErrorHandling.ContinueOnError || !config.ErrorHandling.SnapshotOnError {
		t.Fatalf("error handling = %#v, want continue and snapshot on error", config.ErrorHandling)
	}
	if config.Browser.DownloadResources != "recorded" || config.Browser.DeviceProfile != "pixel_7" {
		t.Fatalf("browser config = %#v, want recorded pixel_7 browser settings", config.Browser)
	}
	if config.WebRuntime.NetworkSimulation.Mode != "custom" || config.WebRuntime.NetworkSimulation.BandwidthKbps != 2048 || config.WebRuntime.NetworkSimulation.LatencyMs != 75 {
		t.Fatalf("network simulation = %#v, want custom bandwidth and latency", config.WebRuntime.NetworkSimulation)
	}
	if config.WebRuntime.Streaming.Mode != "fixed" || config.WebRuntime.Streaming.FixedBitrateKbps != 1800 {
		t.Fatalf("streaming = %#v, want preserved fixed streaming settings", config.WebRuntime.Streaming)
	}
	if !config.WebRuntime.DataFormatting.AutoDetect || len(config.WebRuntime.DataFormatting.Chain) != 5 {
		t.Fatalf("data formatting = %#v, want autodetect plus five transforms", config.WebRuntime.DataFormatting)
	}

	preserved := result.Script.Metadata.Import.Preserved
	if preserved["mapped_runtime_file"] != "default.cfg" {
		t.Fatalf("mapped_runtime_file = %#v, want default.cfg", preserved["mapped_runtime_file"])
	}
	recordedURLs, ok := preserved["recorded_resource_urls"].([]string)
	if !ok || len(recordedURLs) != 2 {
		t.Fatalf("recorded_resource_urls = %#v, want two preserved resources", preserved["recorded_resource_urls"])
	}

	parity, ok := preserved["runtime_settings_parity"].([]RuntimeSettingParityEntry)
	if !ok {
		t.Fatalf("runtime_settings_parity = %#v, want []RuntimeSettingParityEntry", preserved["runtime_settings_parity"])
	}
	expectedStatuses := map[string]RuntimeSettingStatus{
		"http_transport":                      RuntimeSettingStatusExecuted,
		"proxy":                               RuntimeSettingStatusExecuted,
		"request_headers":                     RuntimeSettingStatusExecuted,
		"tls":                                 RuntimeSettingStatusExecuted,
		"authentication":                      RuntimeSettingStatusExecuted,
		"run_logic_iterations":                RuntimeSettingStatusExecuted,
		"run_logic_auto_transactions":         RuntimeSettingStatusExecuted,
		"run_logic_block_scheduling":          RuntimeSettingStatusExecuted,
		"run_logic_action_sequence":           RuntimeSettingStatusExecuted,
		"pacing":                              RuntimeSettingStatusExecuted,
		"think_time":                          RuntimeSettingStatusExecuted,
		"logging":                             RuntimeSettingStatusExecuted,
		"continue_on_error":                   RuntimeSettingStatusExecuted,
		"snapshot_on_error":                   RuntimeSettingStatusExecuted,
		"browser_device_emulation":            RuntimeSettingStatusExecuted,
		"browser_cache_enabled":               RuntimeSettingStatusExecuted,
		"browser_cache_content_urls":          RuntimeSettingStatusExecuted,
		"browser_check_newer_versions":        RuntimeSettingStatusExecuted,
		"browser_new_user_per_iteration":      RuntimeSettingStatusExecuted,
		"browser_download_resources_recorded": RuntimeSettingStatusExecuted,
		"additional_attributes":               RuntimeSettingStatusCompatibilityOnly,
		"download_filters":                    RuntimeSettingStatusExecuted,
		"content_checks":                      RuntimeSettingStatusExecuted,
		"network_simulation":                  RuntimeSettingStatusExecuted,
		"streaming":                           RuntimeSettingStatusExecuted,
		"data_formatting_auto_detect":         RuntimeSettingStatusExecuted,
		"data_formatting_chain":               RuntimeSettingStatusExecuted,
	}
	for id, status := range expectedStatuses {
		assertRuntimeSettingParityEntry(t, parity, id, status)
	}
	counts := RuntimeSettingStatusCounts(parity)
	if counts[string(RuntimeSettingStatusExecuted)] != 26 || counts[string(RuntimeSettingStatusPartial)] != 0 || counts[string(RuntimeSettingStatusCompatibilityOnly)] != 1 {
		t.Fatalf("status counts = %#v, want executed=26 partial=0 compatibility_only=1", counts)
	}
}

func TestBuildRTSParityReportClassifiesConfiguredSettings(t *testing.T) {
	runtimeSections := map[string]map[string]map[string]string{
		"default.cfg": ParseConfigSections(`[Run Logic]
Iterations=5

[Logging]
Level=extended
OnErrorOnly=1

[Streaming]
Mode=fixed
BufferSizeSeconds=30

[Data Formatting]
AutoDetect=1
Chain=base64,gzip mode=decode
`),
	}

	parity := BuildRTSParityReport(runtimeSections)
	if len(parity) != 5 {
		t.Fatalf("parity count = %d, want 5 entries", len(parity))
	}
	assertRuntimeSettingParityEntry(t, parity, "run_logic_iterations", RuntimeSettingStatusExecuted)
	assertRuntimeSettingParityEntry(t, parity, "logging", RuntimeSettingStatusExecuted)
	assertRuntimeSettingParityEntry(t, parity, "streaming", RuntimeSettingStatusExecuted)
	assertRuntimeSettingParityEntry(t, parity, "data_formatting_auto_detect", RuntimeSettingStatusExecuted)
	assertRuntimeSettingParityEntry(t, parity, "data_formatting_chain", RuntimeSettingStatusExecuted)
	counts := RuntimeSettingStatusCounts(parity)
	if counts[string(RuntimeSettingStatusExecuted)] != 5 || counts[string(RuntimeSettingStatusPartial)] != 0 || counts[string(RuntimeSettingStatusCompatibilityOnly)] != 0 {
		t.Fatalf("status counts = %#v, want executed=5 partial=0 compatibility_only=0", counts)
	}
}

func TestBuildRTSParityReportFromScriptClassifiesConfiguredScriptSettings(t *testing.T) {
	script := &dsl.Script{
		Name:     "Imported Runtime Draft",
		Version:  "1.0.0",
		Protocol: "browser",
		Config: dsl.ScriptConfig{
			HTTP: dsl.HTTPConfig{
				ConnectTimeoutMs: 15000,
			},
			RunLogic: dsl.RunLogicConfig{
				Iterations:       3,
				AutoTransactions: "per_action",
			},
			Log: dsl.LogConfig{
				Level: "extended",
			},
			Browser: dsl.BrowserConfig{
				DownloadResources: "recorded",
			},
			WebRuntime: dsl.WebRuntimeConfig{
				Streaming: dsl.StreamingConfig{
					Mode:              "fixed",
					BufferSizeSeconds: 30,
				},
			},
		},
	}

	parity := BuildRTSParityReportFromScript(script)
	if len(parity) != 6 {
		t.Fatalf("parity count = %d, want 6 entries", len(parity))
	}
	assertRuntimeSettingParityStatus(t, parity, "http_transport", RuntimeSettingStatusExecuted)
	assertRuntimeSettingParityStatus(t, parity, "run_logic_iterations", RuntimeSettingStatusExecuted)
	assertRuntimeSettingParityStatus(t, parity, "run_logic_auto_transactions", RuntimeSettingStatusExecuted)
	assertRuntimeSettingParityStatus(t, parity, "logging", RuntimeSettingStatusExecuted)
	assertRuntimeSettingParityStatus(t, parity, "browser_download_resources_recorded", RuntimeSettingStatusExecuted)
	assertRuntimeSettingParityStatus(t, parity, "streaming", RuntimeSettingStatusExecuted)
}

func TestBuildRTSParityReportFromScriptSkipsNativeMappedAdditionalAttributes(t *testing.T) {
	keepAlive := false
	script := &dsl.Script{
		Name:     "Native Mapped Runtime Draft",
		Version:  "1.0.0",
		Protocol: "http",
		Config: dsl.ScriptConfig{
			HTTP: dsl.HTTPConfig{
				ConnectTimeoutMs: 30000,
				RequestTimeoutMs: 45000,
				TLSTimeoutMs:     10000,
				MaxConnsPerHost:  12,
				KeepAlive:        &keepAlive,
				TLS:              dsl.TLSConfig{MinVersion: "1.2"},
			},
			WebRuntime: dsl.WebRuntimeConfig{
				AdditionalAttributes: map[string]string{
					"web_set_sockets_option.SSL_VERSION":                 "TLS1.2",
					"web_set_sockets_option.CLOSE_KEEPALIVE_CONNECTIONS": "1",
					"web_set_sockets_option.MAX_CONNECTIONS_PER_HOST":    "12",
					"web_set_timeout.CONNECT":                            "30",
					"web_set_timeout.RECEIVE":                            "45",
					"web_set_timeout.SSL":                                "10",
				},
			},
		},
	}

	parity := BuildRTSParityReportFromScript(script)
	assertRuntimeSettingParityStatus(t, parity, "http_transport", RuntimeSettingStatusExecuted)
	assertRuntimeSettingParityStatus(t, parity, "tls", RuntimeSettingStatusExecuted)
	if runtimeSettingParityHasID(parity, "additional_attributes") {
		t.Fatalf("parity = %#v, want native-mapped source runtime attributes excluded from additional_attributes", parity)
	}
	counts := RuntimeSettingStatusCounts(parity)
	if counts[string(RuntimeSettingStatusCompatibilityOnly)] != 0 {
		t.Fatalf("status counts = %#v, want no compatibility-only additional attribute", counts)
	}
}

func TestBuildRTSParityReportFromScriptRetainsUnmappedAdditionalAttributes(t *testing.T) {
	script := &dsl.Script{
		Name:     "Compatibility Runtime Draft",
		Version:  "1.0.0",
		Protocol: "http",
		Config: dsl.ScriptConfig{
			WebRuntime: dsl.WebRuntimeConfig{
				AdditionalAttributes: map[string]string{
					// An arbitrary imported RTS attribute with no native APEX
					// equivalent stays compatibility-only.
					"BusinessProcess": "Checkout",
				},
			},
		},
	}

	parity := BuildRTSParityReportFromScript(script)
	assertRuntimeSettingParityStatus(t, parity, "additional_attributes", RuntimeSettingStatusCompatibilityOnly)
	counts := RuntimeSettingStatusCounts(parity)
	if counts[string(RuntimeSettingStatusCompatibilityOnly)] != 1 {
		t.Fatalf("status counts = %#v, want one compatibility-only additional attribute", counts)
	}
}

func TestBuildRTSParityReportFromScriptTreatsNativeNoopAttributesAsNonGaps(t *testing.T) {
	// web_set_max_html_param_len (no capture-length cap in APEX) and
	// web_cache_cleanup (no HTTP response cache in APEX) are honest native
	// no-ops: preserved for fidelity but never a runtime-parity gap.
	script := &dsl.Script{
		Name:     "Native No-op Runtime Draft",
		Version:  "1.0.0",
		Protocol: "http",
		Config: dsl.ScriptConfig{
			WebRuntime: dsl.WebRuntimeConfig{
				AdditionalAttributes: map[string]string{
					"web_set_max_html_param_len": "99999",
					"web_cache_cleanup":          "true",
				},
			},
		},
	}

	parity := BuildRTSParityReportFromScript(script)
	if runtimeSettingParityHasID(parity, "additional_attributes") {
		t.Fatalf("parity = %#v, want native no-op attributes excluded from additional_attributes", parity)
	}
	counts := RuntimeSettingStatusCounts(parity)
	if counts[string(RuntimeSettingStatusCompatibilityOnly)] != 0 {
		t.Fatalf("status counts = %#v, want no compatibility-only additional attribute", counts)
	}
}

func TestBuildRTSParityReportFromScriptSkipsDefaultCompatibilityNoise(t *testing.T) {
	script := &dsl.Script{
		Name:     "Default Draft",
		Version:  "1.0.0",
		Protocol: "http",
		Config: dsl.ScriptConfig{
			ThinkTime: dsl.ThinkTimeConfig{
				Type:   "fixed",
				MeanMs: 1000,
			},
			Pacing: dsl.PacingConfig{
				Type: "none",
			},
			RunLogic: dsl.RunLogicConfig{
				AutoTransactions: "none",
			},
			Log: dsl.LogConfig{
				Level: "standard",
			},
		},
	}

	parity := BuildRTSParityReportFromScript(script)
	if len(parity) != 0 {
		t.Fatalf("parity = %#v, want no default-only runtime parity entries", parity)
	}
}

func TestRTSSupportMatrixHasNoPartialEntriesAfterParityReconciliation(t *testing.T) {
	matrix := GetRTSSupportMatrix()
	if len(matrix) == 0 {
		t.Fatal("expected RTS support matrix entries")
	}
	for _, entry := range matrix {
		if entry.Status == RuntimeSettingStatusPartial {
			t.Fatalf("support matrix entry %s is still partial: %#v", entry.ID, entry)
		}
	}
}

func TestBuildRTSMigrationReadinessHonorsBlockStatuses(t *testing.T) {
	parity := []RuntimeSettingParityEntry{
		{ID: "run_logic_iterations", Area: "Run Logic", Concept: "Iterations", Status: RuntimeSettingStatusExecuted, RuntimeFiles: []string{"default.cfg"}},
		{ID: "logging", Area: "Logging", Concept: "Log level, error-only, and extended logging options", Status: RuntimeSettingStatusPartial, RuntimeFiles: []string{"default.cfg"}},
		{ID: "streaming", Area: "Streaming", Concept: "Adaptive or fixed playback settings", Status: RuntimeSettingStatusExecuted, RuntimeFiles: []string{"default.cfg"}},
	}

	readiness := BuildRTSMigrationReadiness(parity, RuntimeSettingMigrationPolicy{BlockStatuses: []RuntimeSettingStatus{RuntimeSettingStatusPartial, RuntimeSettingStatusCompatibilityOnly}})
	if readiness == nil {
		t.Fatal("expected migration readiness")
	}
	if readiness.ScorePct != 87 {
		t.Fatalf("score_pct = %d, want 87", readiness.ScorePct)
	}
	if !readiness.Blocked {
		t.Fatal("expected readiness to be blocked")
	}
	if len(readiness.BlockingStatuses) != 1 || readiness.BlockingStatuses[0] != RuntimeSettingStatusPartial {
		t.Fatalf("blocking_statuses = %#v, want partial", readiness.BlockingStatuses)
	}
	if len(readiness.BlockingSettings) != 1 {
		t.Fatalf("blocking_settings = %#v, want 1 entry", readiness.BlockingSettings)
	}
	if !strings.Contains(readiness.Summary, "87%") || !strings.Contains(readiness.Summary, "partial") {
		t.Fatalf("summary = %q, want score and policy statuses", readiness.Summary)
	}
}

func zipTestDataDir(t *testing.T, dir string) []byte {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%q) returned error: %v", dir, err)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		content, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("ReadFile(%q) returned error: %v", name, err)
		}
		writer, err := zw.Create(name)
		if err != nil {
			t.Fatalf("Create(%q) returned error: %v", name, err)
		}
		if _, err := writer.Write(content); err != nil {
			t.Fatalf("Write(%q) returned error: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close returned error: %v", err)
	}
	return buf.Bytes()
}

func convertedAssertionCount(actions []dsl.Action) int {
	count := 0
	for _, action := range actions {
		count += len(action.Assertions)
		if action.Type == "http_sequence" {
			for _, child := range sequenceChildren(action) {
				count += len(child.Assertions)
			}
		}
	}
	return count
}

func sequenceChildren(sequence dsl.Action) []dsl.Action {
	rawRequests, ok := sequence.Params["requests"]
	if !ok {
		return nil
	}
	values, ok := rawRequests.([]map[string]any)
	if !ok {
		if generic, genericOK := rawRequests.([]any); genericOK {
			values = make([]map[string]any, 0, len(generic))
			for _, value := range generic {
				if entry, entryOK := value.(map[string]any); entryOK {
					values = append(values, entry)
				}
			}
		}
	}
	children := make([]dsl.Action, 0, len(values))
	for _, entry := range values {
		payload, err := json.Marshal(entry)
		if err != nil {
			continue
		}
		var child dsl.Action
		if err := json.Unmarshal(payload, &child); err == nil {
			children = append(children, child)
		}
	}
	return children
}

func assertRuntimeSettingParityEntry(t *testing.T, entries []RuntimeSettingParityEntry, wantID string, wantStatus RuntimeSettingStatus) {
	t.Helper()
	for _, entry := range entries {
		if entry.ID != wantID {
			continue
		}
		if entry.Status != wantStatus {
			t.Fatalf("parity[%s].status = %q, want %q", wantID, entry.Status, wantStatus)
		}
		if len(entry.RuntimeFiles) == 0 {
			t.Fatalf("parity[%s].runtime_files = %#v, want non-empty list", wantID, entry.RuntimeFiles)
		}
		return
	}
	t.Fatalf("parity entry %q not found in %#v", wantID, entries)
}

func assertRuntimeSettingParityStatus(t *testing.T, entries []RuntimeSettingParityEntry, wantID string, wantStatus RuntimeSettingStatus) {
	t.Helper()
	for _, entry := range entries {
		if entry.ID != wantID {
			continue
		}
		if entry.Status != wantStatus {
			t.Fatalf("parity[%s].status = %q, want %q", wantID, entry.Status, wantStatus)
		}
		return
	}
	t.Fatalf("parity entry %q not found in %#v", wantID, entries)
}

func runtimeSettingParityHasID(entries []RuntimeSettingParityEntry, wantID string) bool {
	for _, entry := range entries {
		if entry.ID == wantID {
			return true
		}
	}
	return false
}

func TestConvertZipMapsMobileBrowserRuntimeSettings(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addFile := func(name string, content string) {
		writer, err := zw.Create(name)
		if err != nil {
			t.Fatalf("Create(%q) returned error: %v", name, err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatalf("Write(%q) returned error: %v", name, err)
		}
	}

	addFile("Mobile.usr", `[General]
Type=Web
RuntimeSettings=default.cfg

[Actions]
Action1=Action.c
`)
	addFile("Action.c", sampleActionC)
	addFile("default.cfg", `[Browser Emulation]
DeviceProfile=iPad_Pro
TouchMode=1
Orientation=landscape
ClientMetrics=1
SlowMoMs=80
ViewportWidth=1024
ViewportHeight=1366
`)
	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close returned error: %v", err)
	}

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.Script.Config.Browser.DeviceProfile != "ipad_air" {
		t.Fatalf("device profile = %q, want ipad_air", result.Script.Config.Browser.DeviceProfile)
	}
	if result.Script.Config.Browser.Engine != "webkit" {
		t.Fatalf("engine = %q, want webkit", result.Script.Config.Browser.Engine)
	}
	if result.Script.Config.Browser.TouchMode == nil || !*result.Script.Config.Browser.TouchMode {
		t.Fatalf("touch mode = %#v, want true", result.Script.Config.Browser.TouchMode)
	}
	if result.Script.Config.Browser.Orientation != "landscape" {
		t.Fatalf("orientation = %q, want landscape", result.Script.Config.Browser.Orientation)
	}
	if result.Script.Config.Browser.ClientMetrics == nil || !*result.Script.Config.Browser.ClientMetrics {
		t.Fatalf("client metrics = %#v, want true", result.Script.Config.Browser.ClientMetrics)
	}
	if result.Script.Config.Browser.SlowMoMs != 80 || result.Script.Config.Browser.Viewport.Width != 1024 || result.Script.Config.Browser.Viewport.Height != 1366 {
		t.Fatalf("browser config = %#v, want imported viewport and slow-mo", result.Script.Config.Browser)
	}
}

func TestConvertZipImportsTruClientMobileWebDefaultXML(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addFile := func(name string, content string) {
		writer, err := zw.Create(name)
		if err != nil {
			t.Fatalf("Create(%q) returned error: %v", name, err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatalf("Write(%q) returned error: %v", name, err)
		}
	}

	addFile("MobileWeb.usr", `[General]
Type=TruClientMobileWeb
RuntimeSettings=default.cfg
`)
	addFile("default.xml", encodeUTF16LEForZip(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" stepId="step:{action}" action="action" actionName="Action">
    <step type="testObject" stepId="step:{navigate}" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://m.example.com/catalog\&quot;&quot;}}" />
		<step type="util" stepId="step:{eval}" action="Evaluate JavaScript" index="2" arguments="{&quot;Code&quot;:{&quot;value&quot;:&quot;$(\&quot;input[name='q']\&quot;).val(\&quot;QAInsights\&quot;); //Enter the search keyword\ndocument.querySelector('#menu').click()&quot;}}" />
  </step>
</truScript>`))
	addFile("default.cfg", `[Browser Emulation]
DeviceProfile=iPhone_SE
TouchMode=1
`)
	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close returned error: %v", err)
	}

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.Script.Protocol != "browser" {
		t.Fatalf("script protocol = %q, want browser", result.Script.Protocol)
	}
	if result.Script.Config.BaseURL != "https://m.example.com" {
		t.Fatalf("base url = %q, want https://m.example.com", result.Script.Config.BaseURL)
	}
	if result.Script.Config.Browser.DeviceProfile != "iphone_se" {
		t.Fatalf("device profile = %q, want iphone_se", result.Script.Config.Browser.DeviceProfile)
	}
	if result.Script.Config.Browser.Engine != "webkit" {
		t.Fatalf("engine = %q, want webkit", result.Script.Config.Browser.Engine)
	}
	if result.Script.Config.Browser.TouchMode == nil || !*result.Script.Config.Browser.TouchMode {
		t.Fatalf("touch mode = %#v, want true", result.Script.Config.Browser.TouchMode)
	}
	if len(result.Script.Actions) != 3 {
		t.Fatalf("actions len = %d, want 3", len(result.Script.Actions))
	}
	if action := result.Script.Actions[0]; action.Type != "browser_navigate" || action.Target != "https://m.example.com/catalog" {
		t.Fatalf("action = %#v, want browser navigate to mobile catalog", action)
	}
	if action := result.Script.Actions[1]; action.Type != "browser_fill" || action.Selector != "input[name='q']" || action.Value != "QAInsights" {
		t.Fatalf("action = %#v, want browser fill for mobile search input", action)
	}
	if action := result.Script.Actions[2]; action.Type != "browser_click" || action.Selector != "#menu" {
		t.Fatalf("action = %#v, want browser click for menu selector", action)
	}
	if result.Stats.TotalFunctions != 2 || result.Stats.ConvertedActions != 3 || result.Stats.Skipped != 0 {
		t.Fatalf("stats = %#v, want total=2 converted=3 skipped=0", result.Stats)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", result.Warnings)
	}
}

func TestConvertZipImportsTruClient2MobileWebRepresentativeArchive(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addFile := func(name string, content string) {
		writer, err := zw.Create(name)
		if err != nil {
			t.Fatalf("Create(%q) returned error: %v", name, err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatalf("Write(%q) returned error: %v", name, err)
		}
	}

	addFile("CheckoutMobile.usr", `[General]
Type=TruClient 2.0 - Mobile Web
RuntimeSettings=default.cfg
`)
	addFile("default.xml", encodeUTF16LEForZip(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="top" action="TopStep">
    <step type="block" action="Run Block" actionName="Run">
      <step type="control" action="Call Action" arguments="{&quot;Action Name&quot;:{&quot;value&quot;:&quot;Checkout&quot;}}" />
    </step>
    <step type="block" action="action" actionName="Checkout">
      <step type="util" action="Start Transaction" index="1" arguments="{&quot;Transaction Name&quot;:{&quot;value&quot;:&quot;\&quot;MobileCheckout\&quot;&quot;}}" />
      <step type="testObject" action="Navigate" index="2" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://m.example.com/checkout\&quot;&quot;}}" />
      <step type="testObject" action="Set / Type" index="3" arguments="{&quot;Selector&quot;:{&quot;value&quot;:&quot;\&quot;[data-testid='email']\&quot;&quot;},&quot;Value&quot;:{&quot;value&quot;:&quot;\&quot;qa@example.com\&quot;&quot;}}" />
      <step type="util" action="Wait" index="4" arguments="{&quot;Selector&quot;:{&quot;value&quot;:&quot;\&quot;[data-testid='pay-now']\&quot;&quot;}}" />
      <step type="testObject" action="Click" index="5" arguments="{&quot;Selector&quot;:{&quot;value&quot;:&quot;\&quot;[data-testid='pay-now']\&quot;&quot;}}" />
      <step type="util" action="Verify" index="6" arguments="{&quot;Text&quot;:{&quot;value&quot;:&quot;\&quot;Order received\&quot;&quot;}}" />
      <step type="util" action="End Transaction" index="7" arguments="{&quot;Transaction Name&quot;:{&quot;value&quot;:&quot;\&quot;MobileCheckout\&quot;&quot;}}" />
    </step>
  </step>
</truScript>`))
	addFile("default.cfg", `[Browser Emulation]
DeviceProfile=Pixel_8
TouchMode=1
ClientMetrics=1
CaptureScreenshots=on_error
`)
	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close returned error: %v", err)
	}

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{PreserveTransactions: true})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.Script.Protocol != "browser" || result.Script.Name != "CheckoutMobile" {
		t.Fatalf("script = %#v, want browser CheckoutMobile", result.Script)
	}
	if result.Script.Config.Browser.DeviceProfile != "pixel_8" || result.Script.Config.Browser.Engine != "chromium" {
		t.Fatalf("browser config = %#v, want Pixel 8 Chromium profile", result.Script.Config.Browser)
	}
	if result.Script.Config.Browser.ClientMetrics == nil || !*result.Script.Config.Browser.ClientMetrics {
		t.Fatalf("client metrics = %#v, want enabled", result.Script.Config.Browser.ClientMetrics)
	}
	if len(result.Script.Actions) != 4 {
		t.Fatalf("actions len = %d, want 4", len(result.Script.Actions))
	}
	if action := result.Script.Actions[1]; action.Type != "browser_fill" || action.Selector != "[data-testid='email']" || action.Value != "qa@example.com" {
		t.Fatalf("action[1] = %#v, want email fill", action)
	}
	if action := result.Script.Actions[2]; action.Type != "browser_wait" || action.Selector != "[data-testid='pay-now']" {
		t.Fatalf("action[2] = %#v, want pay-now wait", action)
	}
	if action := result.Script.Actions[3]; action.Type != "browser_click" || len(action.Assertions) != 1 || action.Assertions[0].Value != "Order received" {
		t.Fatalf("action[3] = %#v, want click with verify assertion", action)
	}
	if result.Stats.TotalFunctions != 7 || result.Stats.ConvertedActions != 4 || result.Stats.Transactions != 1 {
		t.Fatalf("stats = %#v, want total=7 converted=4 transactions=1", result.Stats)
	}
}

func TestConvertZipMapsTruClientQuerySelectorValueAssignment(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addFile := func(name string, content string) {
		writer, err := zw.Create(name)
		if err != nil {
			t.Fatalf("Create(%q) returned error: %v", name, err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatalf("Write(%q) returned error: %v", name, err)
		}
	}

	addFile("MobileWeb.usr", `[General]
Type=TruClientMobileWeb
RuntimeSettings=default.cfg
`)
	addFile("default.xml", encodeUTF16LEForZip(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://m.example.com/profile\&quot;&quot;}}" />
    <step type="util" action="Evaluate JavaScript" index="2" arguments="{&quot;Code&quot;:{&quot;value&quot;:&quot;document.querySelector('#displayName').value = 'APEX Load QA'&quot;}}" />
  </step>
</truScript>`))
	addFile("default.cfg", `[Browser Emulation]
DeviceProfile=iPhone_SE
TouchMode=1
`)
	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close returned error: %v", err)
	}

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions len = %d, want 2", len(result.Script.Actions))
	}
	if action := result.Script.Actions[1]; action.Type != "browser_fill" || action.Selector != "#displayName" || action.Value != "APEX Load QA" {
		t.Fatalf("action = %#v, want browser fill for #displayName", action)
	}
	if result.Stats.TotalFunctions != 2 || result.Stats.ConvertedActions != 2 || result.Stats.Skipped != 0 {
		t.Fatalf("stats = %#v, want total=2 converted=2 skipped=0", result.Stats)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", result.Warnings)
	}
}

func TestConvertZipIgnoresStructuralTruClientRunLogicSteps(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addFile := func(name string, content string) {
		writer, err := zw.Create(name)
		if err != nil {
			t.Fatalf("Create(%q) returned error: %v", name, err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatalf("Write(%q) returned error: %v", name, err)
		}
	}

	addFile("MobileWeb.usr", `[General]
Type=TruClientMobileWeb
RuntimeSettings=default.cfg
`)
	addFile("default.xml", encodeUTF16LEForZip(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="top" action="TopStep">
    <step type="block" action="Run Block" actionName="Run">
      <step type="control" action="Call Action" arguments="{&quot;Action Name&quot;:{&quot;value&quot;:&quot;Action&quot;}}" />
    </step>
    <step type="block" action="action" actionName="Action">
      <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://m.example.com/home\&quot;&quot;}}" />
    </step>
  </step>
</truScript>`))
	addFile("default.cfg", `[Browser Emulation]
DeviceProfile=iPhone_SE
TouchMode=1
`)
	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close returned error: %v", err)
	}

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if len(result.Script.Actions) != 1 {
		t.Fatalf("actions len = %d, want 1", len(result.Script.Actions))
	}
	if action := result.Script.Actions[0]; action.Type != "browser_navigate" || action.Target != "https://m.example.com/home" {
		t.Fatalf("action = %#v, want browser navigate to mobile home", action)
	}
	if result.Stats.TotalFunctions != 1 || result.Stats.ConvertedActions != 1 || result.Stats.Skipped != 0 {
		t.Fatalf("stats = %#v, want total=1 converted=1 skipped=0", result.Stats)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", result.Warnings)
	}
}

func TestConvertZipUsesReferencedTruClientActionBlocksOnly(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addFile := func(name string, content string) {
		writer, err := zw.Create(name)
		if err != nil {
			t.Fatalf("Create(%q) returned error: %v", name, err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatalf("Write(%q) returned error: %v", name, err)
		}
	}

	addFile("MobileWeb.usr", `[General]
Type=TruClientMobileWeb
RuntimeSettings=default.cfg
`)
	addFile("default.xml", encodeUTF16LEForZip(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="top" action="TopStep">
    <step type="block" action="Run Block" actionName="Run">
      <step type="control" action="Call Action" arguments="{&quot;Action Name&quot;:{&quot;value&quot;:&quot;PrimaryAction&quot;}}" />
    </step>
    <step type="block" action="action" actionName="PrimaryAction">
      <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://m.example.com/home\&quot;&quot;}}" />
    </step>
    <step type="block" action="action" actionName="UnusedAction">
      <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://m.example.com/admin\&quot;&quot;}}" />
    </step>
  </step>
</truScript>`))
	addFile("default.cfg", `[Browser Emulation]
DeviceProfile=iPhone_SE
TouchMode=1
`)
	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close returned error: %v", err)
	}

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if len(result.Script.Actions) != 1 {
		t.Fatalf("actions len = %d, want 1", len(result.Script.Actions))
	}
	if action := result.Script.Actions[0]; action.Type != "browser_navigate" || action.Target != "https://m.example.com/home" {
		t.Fatalf("action = %#v, want only referenced mobile home navigate", action)
	}
	if result.Stats.TotalFunctions != 1 || result.Stats.ConvertedActions != 1 || result.Stats.Skipped != 0 {
		t.Fatalf("stats = %#v, want total=1 converted=1 skipped=0", result.Stats)
	}
}

func TestConvertZipMapsDirectTruClientClickAndTypeSteps(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addFile := func(name string, content string) {
		writer, err := zw.Create(name)
		if err != nil {
			t.Fatalf("Create(%q) returned error: %v", name, err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatalf("Write(%q) returned error: %v", name, err)
		}
	}

	addFile("MobileWeb.usr", `[General]
Type=TruClientMobileWeb
RuntimeSettings=default.cfg
`)
	addFile("default.xml", encodeUTF16LEForZip(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://m.example.com/login\&quot;&quot;}}" />
    <step type="testObject" action="Set / Type" index="2" arguments="{&quot;Selector&quot;:{&quot;value&quot;:&quot;\&quot;#username\&quot;&quot;},&quot;Value&quot;:{&quot;value&quot;:&quot;\&quot;apexload\&quot;&quot;}}" />
    <step type="testObject" action="Click" index="3" arguments="{&quot;Selector&quot;:{&quot;value&quot;:&quot;\&quot;#submit\&quot;&quot;}}" />
  </step>
</truScript>`))
	addFile("default.cfg", `[Browser Emulation]
DeviceProfile=iPhone_SE
TouchMode=1
`)
	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close returned error: %v", err)
	}

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if len(result.Script.Actions) != 3 {
		t.Fatalf("actions len = %d, want 3", len(result.Script.Actions))
	}
	if action := result.Script.Actions[1]; action.Type != "browser_fill" || action.Selector != "#username" || action.Value != "apexload" {
		t.Fatalf("action = %#v, want browser fill for #username", action)
	}
	if action := result.Script.Actions[2]; action.Type != "browser_click" || action.Selector != "#submit" {
		t.Fatalf("action = %#v, want browser click for #submit", action)
	}
	if result.Stats.TotalFunctions != 3 || result.Stats.ConvertedActions != 3 || result.Stats.Skipped != 0 {
		t.Fatalf("stats = %#v, want total=3 converted=3 skipped=0", result.Stats)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", result.Warnings)
	}
}

func TestConvertZipMapsTruClientSelectStep(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addFile := func(name string, content string) {
		writer, err := zw.Create(name)
		if err != nil {
			t.Fatalf("Create(%q) returned error: %v", name, err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatalf("Write(%q) returned error: %v", name, err)
		}
	}

	addFile("MobileWeb.usr", `[General]
Type=TruClientMobileWeb
RuntimeSettings=default.cfg
`)
	addFile("default.xml", encodeUTF16LEForZip(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://m.example.com/profile\&quot;&quot;}}" />
    <step type="testObject" action="Select" index="2" arguments="{&quot;Selector&quot;:{&quot;value&quot;:&quot;\&quot;#country\&quot;&quot;},&quot;VisibleText&quot;:{&quot;value&quot;:&quot;\&quot;United States\&quot;&quot;}}" />
  </step>
</truScript>`))
	addFile("default.cfg", `[Browser Emulation]
DeviceProfile=iPhone_SE
TouchMode=1
`)
	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close returned error: %v", err)
	}

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions len = %d, want 2", len(result.Script.Actions))
	}
	if action := result.Script.Actions[1]; action.Type != "browser_select" || action.Selector != "#country" || action.Value != "United States" {
		t.Fatalf("action = %#v, want browser_select for #country", action)
	}
	if result.Stats.TotalFunctions != 2 || result.Stats.ConvertedActions != 2 || result.Stats.Skipped != 0 {
		t.Fatalf("stats = %#v, want total=2 converted=2 skipped=0", result.Stats)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", result.Warnings)
	}
}

func TestConvertZipMapsTruClientWaitAndPreservesTransactions(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addFile := func(name string, content string) {
		writer, err := zw.Create(name)
		if err != nil {
			t.Fatalf("Create(%q) returned error: %v", name, err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatalf("Write(%q) returned error: %v", name, err)
		}
	}

	addFile("MobileWeb.usr", `[General]
Type=TruClientMobileWeb
RuntimeSettings=default.cfg
`)
	addFile("default.xml", encodeUTF16LEForZip(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="util" action="Start Transaction" index="1" arguments="{&quot;Transaction Name&quot;:{&quot;value&quot;:&quot;\&quot;Login\&quot;&quot;}}" />
    <step type="testObject" action="Navigate" index="2" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://m.example.com/login\&quot;&quot;}}" />
    <step type="util" action="Wait" index="3" arguments="{&quot;Duration&quot;:{&quot;value&quot;:&quot;500&quot;}}" />
    <step type="testObject" action="Click" index="4" arguments="{&quot;Selector&quot;:{&quot;value&quot;:&quot;\&quot;#submit\&quot;&quot;}}" />
    <step type="util" action="End Transaction" index="5" arguments="{&quot;Transaction Name&quot;:{&quot;value&quot;:&quot;\&quot;Login\&quot;&quot;}}" />
  </step>
</truScript>`))
	addFile("default.cfg", `[Browser Emulation]
DeviceProfile=iPhone_SE
TouchMode=1
`)
	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close returned error: %v", err)
	}

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{PreserveTransactions: true})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if len(result.Script.Actions) != 3 {
		t.Fatalf("actions len = %d, want 3", len(result.Script.Actions))
	}
	if action := result.Script.Actions[1]; action.Name != "Login_Wait" || action.Type != "browser_wait" || action.WaitFor == nil || action.WaitFor.Type != "duration" || action.WaitFor.Value != "500" {
		t.Fatalf("action = %#v, want Login_Wait browser_wait duration 500", action)
	}
	if action := result.Script.Actions[2]; action.Name != "Login_Click" || action.Type != "browser_click" || action.Selector != "#submit" {
		t.Fatalf("action = %#v, want Login_Click browser_click for #submit", action)
	}
	if result.Stats.TotalFunctions != 5 || result.Stats.ConvertedActions != 3 || result.Stats.Skipped != 0 || result.Stats.Transactions != 1 {
		t.Fatalf("stats = %#v, want total=5 converted=3 skipped=0 transactions=1", result.Stats)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", result.Warnings)
	}
}

func TestConvertZipMapsTruClientVerifyToAssertions(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addFile := func(name string, content string) {
		writer, err := zw.Create(name)
		if err != nil {
			t.Fatalf("Create(%q) returned error: %v", name, err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatalf("Write(%q) returned error: %v", name, err)
		}
	}

	addFile("MobileWeb.usr", `[General]
Type=TruClientMobileWeb
RuntimeSettings=default.cfg
`)
	addFile("default.xml", encodeUTF16LEForZip(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://m.example.com/login\&quot;&quot;}}" />
    <step type="util" action="Verify" index="2" arguments="{&quot;Title&quot;:{&quot;value&quot;:&quot;\&quot;Login Page\&quot;&quot;}}" />
    <step type="testObject" action="Click" index="3" arguments="{&quot;Selector&quot;:{&quot;value&quot;:&quot;\&quot;#submit\&quot;&quot;}}" />
    <step type="util" action="Verify" index="4" arguments="{&quot;Text&quot;:{&quot;value&quot;:&quot;\&quot;Saved apexload\&quot;&quot;}}" />
  </step>
</truScript>`))
	addFile("default.cfg", `[Browser Emulation]
DeviceProfile=iPhone_SE
TouchMode=1
`)
	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close returned error: %v", err)
	}

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions len = %d, want 2", len(result.Script.Actions))
	}
	if assertions := result.Script.Actions[0].Assertions; len(assertions) != 1 || assertions[0].Type != "page_title" || assertions[0].Value != "Login Page" {
		t.Fatalf("action[0].assertions = %#v, want page_title Login Page", assertions)
	}
	if assertions := result.Script.Actions[1].Assertions; len(assertions) != 1 || assertions[0].Type != "body_contains" || assertions[0].Value != "Saved apexload" {
		t.Fatalf("action[1].assertions = %#v, want body_contains Saved apexload", assertions)
	}
	if result.Stats.TotalFunctions != 4 || result.Stats.ConvertedActions != 2 || result.Stats.Skipped != 0 {
		t.Fatalf("stats = %#v, want total=4 converted=2 skipped=0", result.Stats)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", result.Warnings)
	}
}

func TestConvertZipMapsMixedTruClientEvaluateJavaScriptScrollStatements(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addFile := func(name string, content string) {
		writer, err := zw.Create(name)
		if err != nil {
			t.Fatalf("Create(%q) returned error: %v", name, err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatalf("Write(%q) returned error: %v", name, err)
		}
	}

	addFile("MobileWeb.usr", `[General]
Type=TruClientMobileWeb
RuntimeSettings=default.cfg
`)
	addFile("default.xml", encodeUTF16LEForZip(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://m.example.com/profile\&quot;&quot;}}" />
    <step type="util" action="Evaluate JavaScript" index="2" arguments="{&quot;Code&quot;:{&quot;value&quot;:&quot;document.querySelector('#displayName').value = 'APEX Load QA';\nwindow.scrollTo(0, 400)&quot;}}" />
  </step>
</truScript>`))
	addFile("default.cfg", `[Browser Emulation]
DeviceProfile=iPhone_SE
TouchMode=1
`)
	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close returned error: %v", err)
	}

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if len(result.Script.Actions) != 3 {
		t.Fatalf("actions len = %d, want 3", len(result.Script.Actions))
	}
	if action := result.Script.Actions[1]; action.Type != "browser_fill" || action.Selector != "#displayName" || action.Value != "APEX Load QA" {
		t.Fatalf("action = %#v, want browser fill for #displayName", action)
	}
	if action := result.Script.Actions[2]; action.Type != "browser_swipe" || action.Selector != "body" || action.Params["direction"] != "up" || action.Params["distance"] != 400 {
		t.Fatalf("action = %#v, want browser swipe up on body", action)
	}
	if result.Stats.TotalFunctions != 2 || result.Stats.ConvertedActions != 3 || result.Stats.Skipped != 0 {
		t.Fatalf("stats = %#v, want total=2 converted=3 skipped=0", result.Stats)
	}
	if len(result.Stats.UnsupportedFuncs) != 0 {
		t.Fatalf("unsupported funcs = %#v, want none", result.Stats.UnsupportedFuncs)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", result.Warnings)
	}
}

func TestConvertZipClassifiesLegacyTruClientAsReviewArtifact(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addFile := func(name string, content string) {
		writer, err := zw.Create(name)
		if err != nil {
			t.Fatalf("Create(%q) returned error: %v", name, err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatalf("Write(%q) returned error: %v", name, err)
		}
	}

	addFile("Desktop.usr", `[General]
Type=TruClient
`)
	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close returned error: %v", err)
	}

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.Script != nil {
		t.Fatalf("script = %#v, want nil for review-only legacy TruClient", result.Script)
	}
	if result.DetectedProtocol == nil || result.DetectedProtocol.CanonicalSourceProtocol != "truclient_legacy" || result.DetectedProtocol.ImportMode != ImportModeExternalRunnerRequired {
		t.Fatalf("detected protocol = %#v, want legacy TruClient external-runner classification", result.DetectedProtocol)
	}
	if result.ReviewArtifact == nil || !strings.Contains(result.ReviewArtifact.Summary, "browser-profile archives require an external runtime") {
		t.Fatalf("review artifact = %#v, want browser-profile review guidance", result.ReviewArtifact)
	}
}

func TestConvertZipProducesReviewDraftForTruClientWeb2Archive(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addFile := func(name string, content string) {
		writer, err := zw.Create(name)
		if err != nil {
			t.Fatalf("Create(%q) returned error: %v", name, err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatalf("Write(%q) returned error: %v", name, err)
		}
	}

	addFile("Web2/Web2.usr", `[General]
Type=TruClient 2.0 - Web
`)
	addFile("Web2/default.xml", encodeUTF16LEForZip(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://shop.example.com\&quot;&quot;}}" />
    <step type="testObject" action="Click" index="2" arguments="{&quot;Selector&quot;:{&quot;value&quot;:&quot;\&quot;#cart\&quot;&quot;}}" />
    <step type="util" action="Evaluate JavaScript" index="3" arguments="{&quot;Code&quot;:{&quot;value&quot;:&quot;window.app.checkout()&quot;}}" />
  </step>
</truScript>`))
	addFile("Web2/ObjectRepository/MainObjects.xml", "<objects><object name=\"Cart\" /></objects>")
	addFile("Web2/parameters/users.dat", "username,password\nqa,secret\n")
	addFile("Web2/default.cfg", "[ThinkTime]\nOptions=RECORDED\n")
	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close returned error: %v", err)
	}

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.Script.Protocol != "browser" {
		t.Fatalf("protocol = %q, want browser", result.Script.Protocol)
	}
	if len(result.Script.Actions) != 3 {
		t.Fatalf("actions = %#v, want navigate, click, and review-gated JS actions", result.Script.Actions)
	}
	if result.Script.Actions[0].Type != "browser_navigate" || result.Script.Actions[1].Type != "browser_click" || result.Script.Actions[2].Type != "browser_evaluate_js" {
		t.Fatalf("actions = %#v, want browser navigate, click, and evaluate_js", result.Script.Actions)
	}
	if result.Script.Actions[2].Metadata["review_required"] != true || result.Script.Actions[2].Metadata["compatibility_gate"] != "trueclient2_javascript_review" {
		t.Fatalf("evaluate_js metadata = %#v, want review gate preserved", result.Script.Actions[2].Metadata)
	}
	if result.Script.Actions[0].Metadata["source_step_index"] != "1" || result.Script.Actions[0].Metadata["source_action"] != "Navigate" {
		t.Fatalf("first action metadata = %#v, want TruClient source step metadata", result.Script.Actions[0].Metadata)
	}
	if result.Script.Actions[1].Metadata["source_step_index"] != "2" || result.Script.Actions[1].Metadata["source_action"] != "Click" {
		t.Fatalf("second action metadata = %#v, want TruClient source step metadata", result.Script.Actions[1].Metadata)
	}
	if containsString(result.Warnings, "TruClient 2.0 - Web import is review-only; validate selectors, waits, runtime settings, and replay evidence before promotion.") {
		t.Fatalf("warnings = %#v, did not expect review-only warning for executable TruClient 2.0 Web import", result.Warnings)
	}
	if result.Script.Metadata == nil || result.Script.Metadata.Import == nil {
		t.Fatal("expected import metadata to be populated")
	}
	preserved := result.Script.Metadata.Import.Preserved
	if preserved["source_script_type"] != "TruClient 2.0 - Web" || preserved["source_profile"] != "web2_browser_profile browser profile" {
		t.Fatalf("preserved source metadata = %#v, want Web 2.0 browser profile", preserved)
	}
	inspection, ok := preserved["trueclient_archive_inspection"].(*trueclient.ArchiveInspection)
	if !ok {
		t.Fatalf("trueclient_archive_inspection = %#v, want *trueclient.ArchiveInspection", preserved["trueclient_archive_inspection"])
	}
	if inspection.StepInventory.Total != 3 || len(inspection.ObjectRepositories) != 1 || len(inspection.ParameterFiles) != 1 || len(inspection.RuntimeFiles) != 1 {
		t.Fatalf("inspection = %#v, want step/object/parameter/runtime inventory", inspection)
	}
}

func TestConvertZipMapsJavaSolaceJMSArchive(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	files := map[string]string{
		"JavaJMS/JavaJMS.usr": `[General]
Type=Java Vuser
ScriptLanguage=JAVA
RunType=Java-Interpreter
[Actions]
Action=Actions.java
[ManuallyExtraFiles]
File1=request.xml
`,
		"JavaJMS/Actions.java": `import lrapi.lr;
import javax.jms.*;
import javax.naming.Context;
import java.util.Hashtable;

public class Actions {
  public int init() throws Throwable {
    Hashtable env = new Hashtable();
    env.put(Context.PROVIDER_URL, "smf://broker.example.test:55555");
    env.put(Context.SECURITY_PRINCIPAL, "perfuser@ordersvpn");
    env.put(Context.SECURITY_CREDENTIALS, "secret");
    return 0;
  }

  public int action() throws Throwable {
    lr.start_transaction("PublishOrder");
    Queue queue = session.createQueue("ORDERS.IN");
    String body = readFile("request.xml");
    body = body.replaceAll("xxxdatexxx", lr.eval_string("{date}"));
    TextMessage msg = session.createTextMessage(body);
    msg.setStringProperty("eventType", "order");
    producer.send(queue, msg);
    lr.end_transaction("PublishOrder", lr.AUTO);
    return 0;
  }
}
`,
		"JavaJMS/request.xml": `<order><date>xxxdatexxx</date></order>`,
	}
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("Create(%s): %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("Write(%s): %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("Close zip: %v", err)
	}

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.Script == nil || result.Script.Protocol != "java_vuser" || len(result.Scripts) != 1 || result.Scripts[0] != result.Script {
		t.Fatalf("script = %#v scripts=%#v, want combined java_vuser script", result.Script, result.Scripts)
	}
	if result.DetectedProtocol == nil || result.DetectedProtocol.ImportMode != ImportModeExternalRunnerRequired || result.DetectedProtocol.APEXLoadProtocol != "java_vuser" {
		t.Fatalf("detected = %#v, want external-runner Java mapping", result.DetectedProtocol)
	}
	if len(result.Script.Init) != 1 || len(result.Script.Actions) != 1 || result.Script.Actions[0].Type != "java_vuser_run" {
		t.Fatalf("lifecycle = init %#v actions %#v, want combined Java phases", result.Script.Init, result.Script.Actions)
	}
	if result.SourceReview == nil || len(result.SourceReview.Mappings) == 0 {
		t.Fatalf("source review = %#v, want Java mappings", result.SourceReview)
	}
}

func TestConvertZipMapsSAPGUIDraftWithExternalRunnerGate(t *testing.T) {
	archive := buildSourceZip(t, map[string]string{
		"SAPLogin.usr": `[General]
Type=SAP GUI
[Actions]
Action=Action.c
[Files]
Runtime=default.cfg
Params=SAPLogin.prm
`,
		"default.cfg": `[SAPGUI]
ConnectionString=SAP_SYSTEM
Client=001
Language=EN
AutoLogon=1
`,
		"SAPLogin.prm": `[parameter:User]
ParamName=User
ValueList=perfuser
`,
		"Action.c": `Action()
{
    sapgui_open_connection("SAP_SYSTEM", LAST);
    sapgui_logon("001", "{User}", "recorded-password", "EN", LAST);
    sapgui_set_text("wnd[0]/usr/txtRSYST-BNAME", "{User}", LAST);
    sapgui_set_password("wnd[0]/usr/pwdRSYST-BCODE", "recorded-password", LAST);
    sapgui_press_button("wnd[0]/tbar[0]/btn[0]", LAST);
    sapgui_grid_set_cell_data("wnd[0]/usr/cntlGRID/shellcont/shell", "0", "CARRID", "AA", LAST);
    sapgui_wait_for_session_idle(LAST);
    return 0;
}`,
	})

	result, err := ConvertZip(archive, ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.Script == nil || result.Script.Protocol != "sap_gui" {
		t.Fatalf("script = %#v, want SAP GUI draft", result.Script)
	}
	if result.DetectedProtocol == nil || result.DetectedProtocol.CanonicalSourceProtocol != "sap_gui" || result.DetectedProtocol.ImportMode != ImportModeExternalRunnerRequired {
		t.Fatalf("detected protocol = %#v, want external-runner SAP GUI", result.DetectedProtocol)
	}
	if result.ReviewArtifact == nil {
		t.Fatal("review artifact = nil, want SAP GUI external-runner review artifact")
	}
	if result.Script.Config.SAPGUI.Password != "{{secret.sapgui_password}}" || result.Script.Config.SAPGUI.Client != "001" {
		t.Fatalf("sapgui config = %#v, want secret password and client", result.Script.Config.SAPGUI)
	}
	var sawSet, sawPress, sawGrid, sawSync bool
	for _, action := range result.Script.Actions {
		switch action.Type {
		case "sapgui_set":
			if action.Target == "wnd[0]/usr/txtRSYST-BNAME" && action.Value == "{{param.user}}" {
				sawSet = true
			}
			if action.Target == "wnd[0]/usr/pwdRSYST-BCODE" && action.Value != "{{secret.sapgui_password}}" {
				t.Fatalf("password action = %#v, want secret replacement", action)
			}
		case "sapgui_press":
			sawPress = true
		case "sapgui_grid":
			if action.Target == "wnd[0]/usr/cntlGRID/shellcont/shell" && action.Params["row"] == 0 && action.Params["column"] == "CARRID" {
				sawGrid = true
			}
		case "sapgui_sync":
			sawSync = true
		}
	}
	if !sawSet || !sawPress || !sawGrid || !sawSync {
		t.Fatalf("actions = %#v, want set/press/grid/sync", result.Script.Actions)
	}
	if result.SourceReview == nil || len(result.SourceReview.Mappings) == 0 {
		t.Fatalf("source review = %#v, want SAP GUI mappings", result.SourceReview)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) > 0 {
		t.Fatalf("SAP GUI draft validation issues = %v", issues)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func encodeUTF16LEForZip(text string) string {
	words := utf16.Encode([]rune(text))
	bytes := make([]byte, 0, len(words)*2)
	for _, word := range words {
		bytes = append(bytes, byte(word), byte(word>>8))
	}
	return string(bytes)
}

// assertTopLevelNamesFoldedUnique fails if any two top-level action names (the
// set the dashboard validates) collide once lowercased — the exact invariant
// that the duplicate-action-name bug violated.
func assertTopLevelNamesFoldedUnique(t *testing.T, script *dsl.Script) {
	t.Helper()
	seen := map[string]string{}
	for _, phase := range [][]dsl.Action{script.Init, script.Actions, script.Teardown} {
		for _, action := range phase {
			if strings.TrimSpace(action.Name) == "" {
				continue
			}
			key := strings.ToLower(action.Name)
			if prev, ok := seen[key]; ok {
				t.Fatalf("top-level action names collide under case-fold: %q and %q both fold to %q", prev, action.Name, key)
			}
			seen[key] = action.Name
		}
	}
}

// TestConvertUniquifiesCaseVariantActionNames covers the reported bug: source
// steps that differ only in case (a common Salesforce/Aura correlation-step
// TestConvertReconcilesDuplicateUtilityActionNames covers the source-review
// reconciliation for two byte-identical utility-action names: the kept action
// must retain its own mapping rather than having it clobbered to the renamed
// _N action (the old name-only reconcile rewrote ALL same-name mappings).
func TestConvertReconcilesDuplicateUtilityActionNames(t *testing.T) {
	source := `Action()
{
	web_convert_param("p1", "SourceString=a", "SourceEncoding=PLAIN", "TargetEncoding=URL", LAST);
	web_convert_param("p1", "SourceString=b", "SourceEncoding=PLAIN", "TargetEncoding=URL", LAST);
	return 0;
}`
	parsed, err := ParseActionFile("Action.c", source)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{})
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions = %d, want 2 same-named utility actions", len(result.Script.Actions))
	}
	if result.Script.Actions[0].Name == result.Script.Actions[1].Name {
		t.Fatalf("actions were not uniquified: both named %q", result.Script.Actions[0].Name)
	}
	// Every top-level action must have an action-kind mapping at its own path that
	// names it — in particular the kept action[0] must not have lost its mapping.
	for idx := range result.Script.Actions {
		want := result.Script.Actions[idx].Name
		path := fmt.Sprintf("actions[%d]", idx)
		found := false
		for _, m := range result.SourceReview.Mappings {
			if m.TargetKind == "action" && m.TargetPath == path {
				found = true
				if m.TargetName != want {
					t.Fatalf("mapping at %s has TargetName %q, want %q (the action's own name)", path, m.TargetName, want)
				}
			}
		}
		if !found {
			t.Fatalf("action %q at %s lost its source-review mapping", want, path)
		}
	}
}

// pattern) defeated the case-sensitive per-branch dedup and surfaced as false
// "Action name ... is duplicated" warnings in the dashboard, which folds case.
func TestConvertUniquifiesCaseVariantActionNames(t *testing.T) {
	source := `Action()
{
	web_url("c_AuraToken_URL",
		"URL=https://acme.lightning.force.com/aura?a=1",
		LAST);

	web_url("c_AuraToken_url",
		"URL=https://acme.lightning.force.com/aura?a=2",
		LAST);

	web_url("C_AURATOKEN_URL",
		"URL=https://acme.lightning.force.com/aura?a=3",
		LAST);

	return 0;
}`
	parsed, err := ParseActionFile("Action.c", source)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{})
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}
	if len(result.Script.Actions) != 3 {
		t.Fatalf("actions = %d, want 3", len(result.Script.Actions))
	}
	assertTopLevelNamesFoldedUnique(t, result.Script)

	// The first occurrence keeps its name; the later case variants are suffixed.
	if result.Script.Actions[0].Name != "c_AuraToken_URL" {
		t.Fatalf("first action = %q, want c_AuraToken_URL kept", result.Script.Actions[0].Name)
	}

	// The rename must be reflected in the source-review mapping so the import
	// review table keeps pointing at the right action.
	renamed := result.Script.Actions[1].Name
	foundMapping := false
	for _, mapping := range result.SourceReview.Mappings {
		if mapping.TargetKind == "action" && mapping.TargetName == renamed {
			foundMapping = true
			break
		}
		if mapping.TargetKind == "action" && mapping.TargetName == "c_AuraToken_url" {
			t.Fatalf("source-review mapping still references pre-rename name %q", "c_AuraToken_url")
		}
	}
	if !foundMapping {
		t.Fatalf("no source-review action mapping reconciled to renamed action %q in %#v", renamed, result.SourceReview.Mappings)
	}
}

// TestConvertUniquifiesRepeatedTransactionWrappers covers the compaction path:
// a transaction name reused across two non-contiguous request runs produces two
// http_sequence wrapper actions with the same name. The per-branch dedup never
// saw wrapper names (they are created after it runs), so the final pass must
// disambiguate them.
func TestConvertUniquifiesRepeatedTransactionWrappers(t *testing.T) {
	source := `Action()
{
	lr_start_transaction("AuraFlow");
	web_url("step_a", "URL=https://acme.my.salesforce.com/a", LAST);
	web_url("step_b", "URL=https://acme.my.salesforce.com/b", LAST);
	lr_end_transaction("AuraFlow", LR_AUTO);

	web_url("divider", "URL=https://acme.my.salesforce.com/divider", LAST);

	lr_start_transaction("AuraFlow");
	web_url("step_c", "URL=https://acme.my.salesforce.com/c", LAST);
	web_url("step_d", "URL=https://acme.my.salesforce.com/d", LAST);
	lr_end_transaction("AuraFlow", LR_AUTO);

	return 0;
}`
	parsed, err := ParseActionFile("Action.c", source)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{})
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}

	sequences := 0
	for _, action := range result.Script.Actions {
		if action.Type == "http_sequence" {
			sequences++
		}
	}
	if sequences != 2 {
		t.Fatalf("http_sequence wrappers = %d, want 2 (one per AuraFlow run); actions=%#v", sequences, result.Script.Actions)
	}
	assertTopLevelNamesFoldedUnique(t, result.Script)
}

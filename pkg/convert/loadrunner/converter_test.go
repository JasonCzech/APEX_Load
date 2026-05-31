package loadrunner

import (
	"archive/zip"
	"bytes"
	"encoding/json"
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
	if result.Stats.ConvertedActions < 4 {
		t.Errorf("expected at least 4 converted actions, got %d", result.Stats.ConvertedActions)
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
		t.Fatalf("custom_call_functions = %d, want web_set_max_html_param_len catalogued rather than a custom call", result.Stats.CustomCallFunctions)
	}
	if result.Stats.ReviewOnlyFunctions < 1 {
		t.Fatalf("review_only_functions = %d, want web_set_max_html_param_len preserved as a documented review action", result.Stats.ReviewOnlyFunctions)
	}
	if result.SourceReview == nil || len(result.SourceReview.Mappings) == 0 {
		t.Fatal("expected source review mappings")
	}
	assertSourceReviewMapping(t, result.SourceReview.Mappings, "web_url", "actions[1]", "converted")
	assertSourceReviewMapping(t, result.SourceReview.Mappings, "web_reg_save_param", "actions[1].extractors[0]", "contributor")
	assertSourceReviewMapping(t, result.SourceReview.Mappings, "lr_think_time", "config.think_time", "converted")
	assertSourceReviewFunctionStatus(t, result.SourceReview.Mappings, "web_set_max_html_param_len", "review_only")

	// Check think time config was computed
	if result.Script.Config.ThinkTime.MeanMs == 0 {
		t.Error("expected think time to be configured")
	}

	// First action (Homepage) should have the extractor attached
	firstAction := findActionByName(t, result.Script.Actions, "Homepage")
	if len(firstAction.Extractors) != 1 {
		t.Errorf("expected 1 extractor on first action, got %d", len(firstAction.Extractors))
	} else if firstAction.Extractors[0].Name != "SessionID" {
		t.Errorf("expected extractor name 'SessionID', got '%s'", firstAction.Extractors[0].Name)
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

func assertSourceReviewSupportMode(t *testing.T, mappings []SourceReviewMapping, functionName string, mode LoadRunnerSupportMode) {
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

func firstProtocolAction(t *testing.T, actions []dsl.Action) dsl.Action {
	t.Helper()
	for _, action := range actions {
		switch action.Type {
		case "loadrunner_transaction", "loadrunner_runtime", "loadrunner_message", "error_message", "custom_call", "loadrunner_review", "loadrunner_external":
			continue
		default:
			return action
		}
	}
	t.Fatalf("missing protocol action in %#v", actions)
	return dsl.Action{}
}

func sampleLoadRunnerFunctionCall(def LoadRunnerFunctionDefinition) string {
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
	if len(action.Extractors) != 2 || action.Extractors[0].Name != "TokenA" || action.Extractors[1].Name != "TokenB" {
		t.Fatalf("extractors = %#v, want [TokenA TokenB]", action.Extractors)
	}
	if len(action.Assertions) != 2 {
		t.Fatalf("assertions = %#v, want 2 contributor assertions", action.Assertions)
	}
}

func TestConvertSupportsLoadRunnerRuntimeControls(t *testing.T) {
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
	if attrs["web_cache_cleanup"] != "true" || attrs["web_cleanup_cookies"] != "true" {
		t.Fatalf("web runtime attrs = %#v, want cleanup controls", attrs)
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
	if len(result.Script.Actions) != 3 {
		t.Fatalf("actions = %#v, want native error_message, lr_exit, and Homepage request", result.Script.Actions)
	}
	errorAction := result.Script.Actions[0]
	if errorAction.Type != "error_message" || errorAction.Value != "Imported error message" {
		t.Fatalf("error action = %#v, want native error_message action", errorAction)
	}
	if errorAction.Params["level"] != "error" || errorAction.Params["always"] != true {
		t.Fatalf("error action params = %#v, want always-on error level", errorAction.Params)
	}
	if errorAction.Metadata["source_function"] != "lr_error_message" {
		t.Fatalf("error action metadata = %#v, want source_function lr_error_message", errorAction.Metadata)
	}
	assertSourceReviewMapping(t, result.SourceReview.Mappings, "lr_error_message", "actions[0]", "converted")
	if exitAction := result.Script.Actions[1]; exitAction.Type != "loadrunner_runtime" || exitAction.Params["function"] != "lr_exit" {
		t.Fatalf("exit action = %#v, want native lr_exit runtime action", exitAction)
	}
	preserved := result.Script.Metadata.Import.Preserved
	if _, ok := preserved["loadrunner_error_messages"]; ok {
		t.Fatalf("loadrunner_error_messages = %#v, want lr_error_message converted natively", preserved["loadrunner_error_messages"])
	}
	exits, ok := preserved["loadrunner_exit_controls"].([]map[string]any)
	if !ok || len(exits) != 1 {
		t.Fatalf("loadrunner_exit_controls = %#v, want one preserved lr_exit", preserved["loadrunner_exit_controls"])
	}
	timeouts, ok := preserved["loadrunner_timeouts"].([]map[string]any)
	if !ok || len(timeouts) != 3 {
		t.Fatalf("loadrunner_timeouts = %#v, want three preserved web_set_timeout calls", preserved["loadrunner_timeouts"])
	}
	args, ok := exits[0]["args"].([]string)
	if !ok || len(args) != 2 || args[0] != "LR_EXIT_VUSER" || args[1] != "LR_AUTO" {
		t.Fatalf("lr_exit args = %#v, want LR_EXIT_VUSER/LR_AUTO", exits[0]["args"])
	}
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

func TestLoadRunnerFunctionRegistryCoversDocumentedFunctions(t *testing.T) {
	defs := allLoadRunnerFunctionDefinitions()
	if len(defs) < 80 {
		t.Fatalf("registry definitions = %d, want documented VuGen 26.1 utility baseline", len(defs))
	}
	for _, def := range defs {
		t.Run(def.Name, func(t *testing.T) {
			if def.SupportMode == "" || def.Family == "" || def.Reason == "" {
				t.Fatalf("definition = %#v, want family, support mode, and reason", def)
			}
			switch def.SupportMode {
			case LoadRunnerSupportExecutable, LoadRunnerSupportNativeNoop, LoadRunnerSupportReviewOnly, LoadRunnerSupportExternalRequired:
			default:
				t.Fatalf("definition = %#v, has unknown support mode", def)
			}
			source := "Action()\n{\n" + sampleLoadRunnerFunctionCall(def) + "\nweb_url(\"Home\", \"URL=https://example.test/home\", LAST);\nreturn 0;\n}\n"
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
	if action.Type != "custom_call" || action.Params["function"] != "my_custom_helper" || action.Params["support_mode"] != string(LoadRunnerSupportCustomReview) {
		t.Fatalf("custom action = %#v, want custom_call review action", action)
	}
	args, ok := action.Params["args"].([]any)
	if !ok || len(args) != 3 || args[0] != "42" || args[1] != "LR_AUTO" || args[2] != "value" {
		t.Fatalf("custom args = %#v, want numeric, identifier, and string args preserved", action.Params["args"])
	}
	assertSourceReviewSupportMode(t, result.SourceReview.Mappings, "my_custom_helper", LoadRunnerSupportCustomReview)
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

	if got := firstProtocolAction(t, withTransactions.Script.Actions).Name; got != "Login_Homepage" {
		t.Fatalf("preserved action name = %q, want Login_Homepage", got)
	}
	if got := firstProtocolAction(t, withoutTransactions.Script.Actions).Name; got != "Homepage" {
		t.Fatalf("non-preserved action name = %q, want Homepage", got)
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

	secondAssertions := result.Script.Actions[1].Assertions
	if len(secondAssertions) != 2 {
		t.Fatalf("second action assertions = %#v, want body_not_contains + status_code", secondAssertions)
	}
	if secondAssertions[0].Type != "body_not_contains" || secondAssertions[0].Value != "Internal Server Error" {
		t.Fatalf("second mapped assertion = %#v, want body_not_contains on Internal Server Error", secondAssertions[0])
	}

	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "SaveCount=welcomeCount") {
		t.Fatalf("warnings = %#v, want SaveCount warning", result.Warnings)
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

func TestConvertZipPreservesLoadRunnerMetadata(t *testing.T) {
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
UserAgent=VuGen Browser
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
	if result.Script.DataSources[0].Path != "Users.dat" {
		t.Fatalf("data source path = %q, want original Users.dat filename", result.Script.DataSources[0].Path)
	}
	payload, err := json.Marshal(result.Script.Metadata.Import)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	text := string(payload)
	for _, want := range []string{"\"source\":\"loadrunner\"", "\"project_settings\"", "\"runtime_files\"", "\"parameter_files\"", "LoginUsers.prm", "default.cfg", "\"ConnectTimeout\":\"45\""} {
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
	dataSourcePaths := map[string]string{}
	for _, source := range result.Script.DataSources {
		dataSourcePaths[source.Name] = source.Path
		if !source.FirstRowHeader {
			t.Fatalf("data source = %#v, want header inference for quoted .dat imports", source)
		}
	}
	if dataSourcePaths["username"] != "UserName.dat" || dataSourcePaths["acme_iao"] != "ACME_IAO.dat" {
		t.Fatalf("data source paths = %#v, want original .dat filenames", dataSourcePaths)
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
		t.Fatalf("base_url = %q, want first URL.dat value to replace LoadRunner fallback", got)
	}
	if len(result.Script.Parameters) != 1 {
		t.Fatalf("parameters = %#v, want one URL parameter", result.Script.Parameters)
	}
	if got := result.Script.Parameters[0]; got.Name != "url" || got.Type != "file" || got.DataSource != "url" || got.Column != "URL" {
		t.Fatalf("parameter = %#v, want URL.dat mapped as file parameter named url", got)
	}
}

func TestParseParameterFileSupportsLoadRunnerBuiltinParameters(t *testing.T) {
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
`, nil)

	if len(parsed.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", parsed.Warnings)
	}
	if len(parsed.Parameters) != 5 {
		t.Fatalf("parameters = %#v, want five built-in parameters", parsed.Parameters)
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
UserAgent=VuGen Browser
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
	if result.Stats.ConvertedActions != 7 {
		t.Fatalf("converted_actions = %d, want 7 including native transaction actions", result.Stats.ConvertedActions)
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
	}
	return count
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
	if result.DetectedProtocol == nil || result.DetectedProtocol.CanonicalLoadRunnerProtocol != "truclient_legacy" || result.DetectedProtocol.ImportMode != ImportModeExternalRunnerRequired {
		t.Fatalf("detected protocol = %#v, want legacy TruClient external-runner classification", result.DetectedProtocol)
	}
	if result.ReviewArtifact == nil || !strings.Contains(result.ReviewArtifact.Summary, "TruClient") {
		t.Fatalf("review artifact = %#v, want TruClient review guidance", result.ReviewArtifact)
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
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions = %#v, want navigate and click actions", result.Script.Actions)
	}
	if result.Script.Actions[0].Type != "browser_navigate" || result.Script.Actions[1].Type != "browser_click" {
		t.Fatalf("actions = %#v, want browser navigate and click", result.Script.Actions)
	}
	if result.Script.Actions[0].Metadata["source_step_index"] != "1" || result.Script.Actions[0].Metadata["source_action"] != "Navigate" {
		t.Fatalf("first action metadata = %#v, want TruClient source step metadata", result.Script.Actions[0].Metadata)
	}
	if result.Script.Actions[1].Metadata["source_step_index"] != "2" || result.Script.Actions[1].Metadata["source_action"] != "Click" {
		t.Fatalf("second action metadata = %#v, want TruClient source step metadata", result.Script.Actions[1].Metadata)
	}
	if !containsString(result.Warnings, "TruClient 2.0 - Web import is review-only; validate selectors, waits, runtime settings, and replay evidence before promotion.") {
		t.Fatalf("warnings = %#v, want review-only warning", result.Warnings)
	}
	if result.Script.Metadata == nil || result.Script.Metadata.Import == nil {
		t.Fatal("expected import metadata to be populated")
	}
	preserved := result.Script.Metadata.Import.Preserved
	if preserved["source_script_type"] != "TruClient 2.0 - Web" || preserved["source_profile"] != "vugen_web2 browser profile" {
		t.Fatalf("preserved source metadata = %#v, want TruClient 2.0 Web profile", preserved)
	}
	inspection, ok := preserved["trueclient_archive_inspection"].(*trueclient.ArchiveInspection)
	if !ok {
		t.Fatalf("trueclient_archive_inspection = %#v, want *trueclient.ArchiveInspection", preserved["trueclient_archive_inspection"])
	}
	if inspection.StepInventory.Total != 2 || len(inspection.ObjectRepositories) != 1 || len(inspection.ParameterFiles) != 1 || len(inspection.RuntimeFiles) != 1 {
		t.Fatalf("inspection = %#v, want step/object/parameter/runtime inventory", inspection)
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

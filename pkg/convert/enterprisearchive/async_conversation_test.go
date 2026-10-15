package enterprisearchive

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func convertAsyncSource(t *testing.T, body string) *ConvertResult {
	t.Helper()
	parsed, err := ParseActionFile("Action.c", "Action()\n{\n"+body+"\n\treturn 0;\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestConvertAsyncConversation(t *testing.T) {
	result := convertAsyncSource(t, `
	web_reg_async_attributes("ID=Poll_0", "Pattern=Poll", "URL=https://example.test/api/status", "PollIntervalMs=2000",
		"RequestCB=Poll_0_RequestCB", "ResponseCB=Poll_0_ResponseCB", LAST);
	web_reg_save_param_json("ParamName=JobStatus", "QueryString=$.status", SEARCH_FILTERS, "Scope=BODY", LAST);
	web_url("status", "URL=https://example.test/api/status", "Mode=HTTP", LAST);
	web_sync("ParamValueEquals=JobStatus={Wanted}", "Timeout=30", LAST);
	web_stop_async("ID=Poll_0", LAST);`)
	actions := result.Script.Actions
	if len(actions) != 3 {
		t.Fatalf("actions = %+v", actions)
	}
	start := actions[0]
	if start.Type != "http_async_start" || start.Params["id"] != "Poll_0" || start.Params["pattern"] != "poll" || start.Params["interval_ms"] != 2000 {
		t.Fatalf("start = %+v", start)
	}
	request, _ := start.Params["request"].(map[string]any)
	if request["target"] != "/api/status" || request["method"] != "GET" {
		t.Fatalf("request = %+v", request)
	}
	if extractors, _ := request["extractors"].([]any); len(extractors) != 1 {
		t.Fatalf("the registration must apply to the async request: %+v", request["extractors"])
	}
	if callbacks, _ := start.Params["source_callbacks"].([]string); len(callbacks) != 2 {
		t.Fatalf("callbacks = %+v", start.Params["source_callbacks"])
	}
	raw, _ := json.Marshal(start.Metadata)
	if !strings.Contains(string(raw), asyncCallbacksReasonCode) {
		t.Fatalf("the callbacks need a promotion finding: %s", raw)
	}
	if sync := actions[1]; sync.Type != "http_sync" || sync.Params["variable"] != "jobstatus" || sync.Params["value"] != "{{var.wanted}}" || sync.Params["timeout_ms"] != 30000 {
		t.Fatalf("sync = %+v", sync)
	}
	if stop := actions[2]; stop.Type != "http_async_stop" || stop.Params["id"] != "Poll_0" {
		t.Fatalf("stop = %+v", stop)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("the converted script must validate: %v", issues)
	}
	paths := map[string]string{}
	for _, mapping := range result.SourceReview.Mappings {
		paths[mapping.Function] = mapping.TargetPath
	}
	if paths["web_reg_async_attributes"] != "actions[0]" || paths["web_reg_save_param_json"] != "actions[0].params.request.extractors[0]" {
		t.Fatalf("mapping paths = %v", paths)
	}
}

func TestConvertAsyncConversationReviewCases(t *testing.T) {
	cases := map[string]struct {
		body, function, notes string
	}{
		"unknown pattern": {
			body:     `web_reg_async_attributes("ID=A", "Pattern=.*", LAST); web_url("x", "URL=https://example.test/x", LAST);`,
			function: "web_reg_async_attributes", notes: "not Poll, LongPoll or Push",
		},
		"resource URL": {
			body:     `web_reg_async_attributes("ID=A", "Pattern=LongPoll", "URL=https://example.test/push", LAST); web_url("page", "URL=https://example.test/page", LAST);`,
			function: "web_reg_async_attributes", notes: "request inside the step",
		},
		"no step": {
			body:     `web_reg_async_attributes("ID=A", "Pattern=Push", LAST);`,
			function: "web_reg_async_attributes", notes: "no step follows",
		},
		"stop without start": {
			body:     `web_stop_async("ID=A", LAST);`,
			function: "web_stop_async", notes: "nothing to stop",
		},
		"sync with an unknown argument": {
			body:     `web_sync("RetVal=1", LAST);`,
			function: "web_sync", notes: "Only web_sync ParamExists=",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			result := convertAsyncSource(t, "\t"+tc.body)
			for _, action := range result.Script.Actions {
				if strings.HasPrefix(action.Type, "http_async") || action.Type == "http_sync" {
					t.Fatalf("a construct that cannot run must not convert: %+v", action)
				}
			}
			found := false
			for _, mapping := range result.SourceReview.Mappings {
				if mapping.Function == tc.function {
					found = true
					if mapping.Status != "review_only" || !strings.Contains(mapping.Notes, tc.notes) {
						t.Fatalf("mapping = %+v", mapping)
					}
				}
			}
			if !found {
				t.Fatalf("no mapping for %s: %+v", tc.function, result.SourceReview.Mappings)
			}
		})
	}
}

func TestTranslateAsyncCallbacks(t *testing.T) {
	files := map[string]string{"AsyncCallbacks.c": `
int Stub_RequestCB()
{
	//enter your implementation for RequestCB() here

	return WEB_ASYNC_CB_RC_OK;
}
int Save_ResponseCB(const char * aResponseHeadersStr, int aResponseHeadersLen, const char * aResponseBodyStr, int aResponseBodyLen, int aHttpStatusCode)
{
	lr_save_string("yes", "Seen");
	lr_output_message("poll response");
	return WEB_ASYNC_CB_RC_OK;
}
int Args_ResponseCB(const char * aResponseHeadersStr, int aResponseHeadersLen, const char * aResponseBodyStr, int aResponseBodyLen, int aHttpStatusCode)
{
	lr_save_string(aResponseBodyStr, "Body");
	return WEB_ASYNC_CB_RC_OK;
}
int Status_ResponseCB(const char * aResponseHeadersStr, int aResponseHeadersLen, const char * aResponseBodyStr, int aResponseBodyLen, int aHttpStatusCode)
{
	lr_save_int(aHttpStatusCode, "Status");
	return WEB_ASYNC_CB_RC_OK;
}
int Log_ResponseCB(const char * aResponseHeadersStr, int aResponseHeadersLen, const char * aResponseBodyStr, int aResponseBodyLen, int aHttpStatusCode)
{
	lr_output_message("status %d, body %s", aHttpStatusCode, aResponseBodyStr);
	return WEB_ASYNC_CB_RC_OK;
}
int Abort_ResponseCB(const char * aResponseHeadersStr, int aResponseHeadersLen, const char * aResponseBodyStr, int aResponseBodyLen, int aHttpStatusCode)
{
	return WEB_ASYNC_CB_RC_ABORT_ASYNC_NOT_ERROR;
}
int Flow_ResponseCB(const char * aResponseHeadersStr, int aResponseHeadersLen, const char * aResponseBodyStr, int aResponseBodyLen, int aHttpStatusCode)
{
	if (aHttpStatusCode == 200) { lr_save_string("ok", "Status"); }
	return WEB_ASYNC_CB_RC_OK;
}
int Var_ResponseCB(const char * aResponseHeadersStr, int aResponseHeadersLen, const char * aResponseBodyStr, int aResponseBodyLen, int aHttpStatusCode)
{
	int seen = 1;
	return WEB_ASYNC_CB_RC_OK;
}
int Headers_ResponseHeadersCB(int aHttpStatusCode, const char * aAccumulatedHeadersStr, int aAccumulatedHeadersLen)
{
	lr_save_string("yes", "Headers");
	return WEB_ASYNC_CB_RC_OK;
}
`}
	async := &sourceAsync{id: "A", callbacks: []string{
		"RequestCB=Stub_RequestCB", "ResponseCB=Save_ResponseCB", "ResponseCB=Args_ResponseCB", "ResponseCB=Status_ResponseCB", "ResponseCB=Log_ResponseCB", "ResponseCB=Abort_ResponseCB",
		"ResponseCB=Flow_ResponseCB", "ResponseCB=Var_ResponseCB", "ResponseHeadersCB=Headers_ResponseHeadersCB", "ResponseCB=Missing_ResponseCB",
	}}
	got := translateAsyncCallbacks(async, files)
	want := []struct {
		trivial bool
		steps   int
		dropped int
		reason  string
	}{
		{trivial: true},
		{steps: 2},
		// The response body stands for {async_response_body}.
		{steps: 1},
		{reason: "reads the callback argument aHttpStatusCode"},
		{trivial: true, dropped: 1},
		{reason: "returns WEB_ASYNC_CB_RC_ABORT_ASYNC_NOT_ERROR"},
		{reason: "C control flow"},
		{reason: "C variables"},
		{reason: "no ResponseHeadersCB callback point"},
		{reason: "not found"},
	}
	for i, translation := range got {
		w := want[i]
		if translation.trivial != w.trivial || len(translation.steps) != w.steps || translation.droppedLogs != w.dropped || (w.reason == "") != (translation.reason == "") || !strings.Contains(translation.reason, w.reason) {
			t.Errorf("%s: trivial=%v steps=%d dropped=%d reason=%q, want %+v", async.callbacks[i], translation.trivial, len(translation.steps), translation.droppedLogs, translation.reason, w)
		}
	}
	if got[1].branch != "on_response" || got[1].steps[0].Type != "source_parameter" {
		t.Fatalf("ResponseCB steps = %+v", got[1])
	}
	if raw, _ := json.Marshal(got[2].steps); !strings.Contains(string(raw), "{{var.async_response_body}}") {
		t.Fatalf("the response body argument must read var.async_response_body: %s", raw)
	}
}

func TestAsyncRegistrationAppliesToItsOwnURL(t *testing.T) {
	for _, tt := range []struct {
		url, target string
		want        bool
	}{
		{"https://shop.test/poll?x=1", "/poll?x=1", true},
		{"https://{{var.host}}/poll/{{var.id}}", "/poll/{{var.id}}", true},
		{"https://shop.test/poll/", "/", false},
		{"https://shop.test", "/", true},
		{"https://shop.test/other", "/poll", false},
	} {
		async := &sourceAsync{url: tt.url}
		if got := async.appliesTo(ParsedFunction{}, &dsl.Action{Target: tt.target}); got != tt.want {
			t.Errorf("URL=%s on %s = %v, want %v", tt.url, tt.target, got, tt.want)
		}
	}
}

// VuGen's async scan can name a request the app fired while a step loaded,
// left in that step's EXTRARES: the resource becomes the conversation's
// request, sent with the auto headers, and leaves the step — even when the
// step itself is gated for review.
func TestConvertAsyncConversationOfAStepResource(t *testing.T) {
	for name, encoding := range map[string]string{"executable step": "", "gated step": `"ExtraResBaseDir=https://cdn.test/", `} {
		t.Run(name, func(t *testing.T) {
			result := convertAsyncSource(t, `
	web_add_auto_header("Authorization", "Bearer {Token}");
	web_reg_async_attributes("ID=Poll_0", "Pattern=Poll", "URL=https://example.test/services/apexrest/mobile/managerOverride/requests?manager=ab1",
		"PollIntervalMs=5500", LAST);
	web_custom_request("search", "URL=https://example.test/services/apexrest/mobile/search", "Method=POST", `+encoding+`"Body={}",
		EXTRARES, "Url=managerOverride/requests?manager=ab1", "Referer=", ENDITEM, "Url=/img/logo.png", ENDITEM, LAST);
	web_stop_async("ID=Poll_0", LAST);`)
			actions := result.Script.Actions
			if len(actions) != 3 || actions[1].Type != "http_async_start" || actions[2].Type != "http_async_stop" {
				t.Fatalf("actions = %+v", actions)
			}
			resources, _ := actions[0].Params["resources"].([]any)
			if len(resources) != 1 || resources[0].(map[string]any)["url"] != "/img/logo.png" {
				t.Fatalf("step resources = %+v; the conversation's request must leave the step", actions[0].Params["resources"])
			}
			start := actions[1]
			request, _ := start.Params["request"].(map[string]any)
			encoded, _ := json.Marshal(request["headers"])
			if start.Params["id"] != "Poll_0" || start.Params["pattern"] != "poll" || start.Params["interval_ms"] != 5500 ||
				request["method"] != "GET" || request["target"] != "/services/apexrest/mobile/managerOverride/requests?manager=ab1" ||
				string(encoded) != `{"Authorization":"Bearer {{var.token}}"}` {
				t.Fatalf("start = %+v, request = %+v", start.Params, request)
			}
			if stop := actions[2]; stop.Params["id"] != "Poll_0" {
				t.Fatalf("stop = %+v", stop)
			}
			for _, mapping := range result.SourceReview.Mappings {
				if mapping.Function == "web_reg_async_attributes" && (mapping.Status != "converted" || mapping.TargetPath != "actions[1]") {
					t.Fatalf("registration mapping = %+v", mapping)
				}
			}
			if encoding == "" {
				if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
					t.Fatalf("the converted script must validate: %v", issues)
				}
			}
		})
	}
}

// A step whose URL matches a background request pattern (an app's logging and
// analytics calls) is sent as a background request, outside transaction
// times; a gated step, one with an extractor, or one in a concurrent group
// stays in line, and an empty pattern list keeps every step in line.
func TestConvertBackgroundRequestSteps(t *testing.T) {
	source := `
	lr_start_transaction("Search");
	web_custom_request("search", "URL=https://example.test/services/apexrest/mobile/search", "Method=POST", "Body={}", LAST);
	web_custom_request("nebulaLogger_1", "URL=https://example.test/services/apexrest/mobile/nebulaLogger", "Method=POST", "Body={}", LAST);
	web_reg_save_param_json("ParamName=EventId", "QueryString=$.id", SEARCH_FILTERS, "Scope=BODY", LAST);
	web_custom_request("analyticsEvent_1", "URL=https://example.test/services/apexrest/mobile/analyticsEvent", "Method=POST", "Body={}", LAST);
	web_custom_request("nebulaLogger_2", "URL=https://example.test/services/apexrest/mobile/nebulaLogger", "Method=POST", "ExtraResBaseDir=https://cdn.test/", "Body={}", LAST);
	web_concurrent_start(NULL);
	web_custom_request("nebulaLogger_3", "URL=https://example.test/services/apexrest/mobile/nebulaLogger", "Method=POST", "Body={}", LAST);
	web_custom_request("other", "URL=https://example.test/services/apexrest/mobile/other", "Method=POST", "Body={}", LAST);
	web_concurrent_end(NULL);
	lr_end_transaction("Search", LR_AUTO);`
	backgroundNames := func(result *ConvertResult) []string {
		var names []string
		var walk func([]dsl.Action)
		walk = func(actions []dsl.Action) {
			for _, action := range actions {
				if action.Type == "http_async_start" && action.Params["pattern"] == "once" {
					request, _ := action.Params["request"].(map[string]any)
					names = append(names, request["name"].(string))
				}
				if action.Control != nil {
					walk(action.Control.Steps)
				}
			}
		}
		walk(result.Script.Actions)
		return names
	}

	result := convertAsyncSource(t, source)
	if names := backgroundNames(result); strings.Join(names, ",") != "nebulaLogger_1" {
		t.Fatalf("background requests = %v, want only the executable logger call outside a concurrent group", names)
	}
	if !strings.Contains(strings.Join(result.Warnings, "\n"), `1 step(s) matching background request pattern "nebulaLogger" are sent as background requests`) {
		t.Fatalf("warnings = %q", result.Warnings)
	}
	for _, action := range result.Script.Actions {
		if action.Type == "http_async_start" {
			request, _ := action.Params["request"].(map[string]any)
			requestMetadata, _ := request["metadata"].(map[string]any)
			if action.Params["id"] != "background_1" || action.Metadata["source_background_request"] != "nebulaLogger" ||
				action.Metadata["source_transaction"] != nil || requestMetadata["source_transaction"] != nil {
				t.Fatalf("background start = %+v; it must not belong to the transaction around it", action)
			}
		}
	}

	parsed, err := ParseActionFile("Action.c", "Action()\n{\n"+source+"\n\treturn 0;\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	none := []string{}
	inline, err := Convert([]*ParsedAction{parsed}, ConvertOptions{BackgroundRequests: &none})
	if err != nil {
		t.Fatal(err)
	}
	if names := backgroundNames(inline); len(names) != 0 {
		t.Fatalf("an empty pattern list must keep every step in line: %v", names)
	}
}

// The Mobile SDK compresses its analytics uploads (ContentEncoding=gzip): the
// step is sent in the background with its body compressed, not gated.
func TestConvertCompressedBackgroundRequest(t *testing.T) {
	result := convertAsyncSource(t, `
	web_custom_request("app-analytics-logging_1", "URL=https://example.test/services/data/v66.0/connect/proxy/app-analytics-logging", "Method=POST", "ContentEncoding=gzip", "EncType=application/json", "Body={\"events\":[]}", LAST);`)
	if len(result.Script.Actions) != 1 || result.Script.Actions[0].Type != "http_async_start" {
		t.Fatalf("actions = %+v", result.Script.Actions)
	}
	start := result.Script.Actions[0]
	request, _ := start.Params["request"].(map[string]any)
	params, _ := request["params"].(map[string]any)
	if start.Params["pattern"] != "once" || params["content_encoding"] != "gzip" || start.Metadata["execution_blockers"] != nil {
		t.Fatalf("start = %+v, request = %+v", start, request)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("the converted script must validate: %v", issues)
	}
}

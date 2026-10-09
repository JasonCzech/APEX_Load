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
		"RequestCB=Stub_RequestCB", "ResponseCB=Save_ResponseCB", "ResponseCB=Args_ResponseCB", "ResponseCB=Abort_ResponseCB",
		"ResponseCB=Flow_ResponseCB", "ResponseCB=Var_ResponseCB", "ResponseHeadersCB=Headers_ResponseHeadersCB", "ResponseCB=Missing_ResponseCB",
	}}
	got := translateAsyncCallbacks(async, files)
	want := []struct {
		trivial bool
		steps   int
		reason  string
	}{
		{trivial: true},
		{steps: 2},
		{reason: "reads the callback argument aResponseBodyStr"},
		{reason: "returns WEB_ASYNC_CB_RC_ABORT_ASYNC_NOT_ERROR"},
		{reason: "C control flow"},
		{reason: "C variables"},
		{reason: "no ResponseHeadersCB callback point"},
		{reason: "not found"},
	}
	for i, translation := range got {
		w := want[i]
		if translation.trivial != w.trivial || len(translation.steps) != w.steps || (w.reason == "") != (translation.reason == "") || !strings.Contains(translation.reason, w.reason) {
			t.Errorf("%s: trivial=%v steps=%d reason=%q, want %+v", async.callbacks[i], translation.trivial, len(translation.steps), translation.reason, w)
		}
	}
	if got[1].branch != "on_response" || got[1].steps[0].Type != "source_parameter" {
		t.Fatalf("ResponseCB steps = %+v", got[1])
	}
}

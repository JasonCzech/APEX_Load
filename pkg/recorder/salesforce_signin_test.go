package recorder

import (
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// A recorded Mobile SDK sign-in runs in init; its steps get the same clean-up
// and JSON normalization as the iteration's, and the baseline names only the
// iteration's steps.
func TestConvertSessionMovesSalesforceSignInToInit(t *testing.T) {
	base := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	host := "https://acme.my.salesforce.com"
	form := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
	request := func(seq int, method, path, body string, status int, responseBody string) RecordedRequest {
		r := RecordedRequest{Seq: seq, Timestamp: base.Add(time.Duration(seq) * time.Second), Method: method, URL: host + path, Path: path,
			Host: "acme.my.salesforce.com", StatusCode: status, ResponseBody: responseBody, ResponseHeaders: map[string]string{"Content-Type": "application/json"}}
		if body != "" {
			r.Body, r.ContentType, r.Headers = body, "application/x-www-form-urlencoded", form
		}
		return r
	}
	failed := request(1, "POST", "/", "un=perf.user%40example.com&pw=wrong&lt=standard", 200, "")
	login := request(2, "POST", "/", "un=perf.user%40example.com&pw=Secr3t&lt=standard", 302, "")
	login.ResponseHeaders = map[string]string{"Location": host + "/secur/frontdoor.jsp"}
	token := request(3, "POST", "/services/oauth2/token",
		"format=json&redirect_uri=testsfdc%3A%2F%2F%2Fmobilesdk%2Fdetect%2Foauth%2Fdone&client_id=3MVGexample&grant_type=hybrid_auth_code&code=aPrxAbCdEf123456",
		200, `{"access_token":"00D5g000004ExAmPLE!AQ0AQexampleToken","instance_url":"`+host+`","id":"https://login.salesforce.com/id/00D5g000004ExAmPLE/0055g00000ABCDeAAH"}`)
	session := &Session{ID: "sf", Name: "Mobile sign-in", BaseURL: host, StartedAt: base, Requests: []RecordedRequest{
		failed, login, token, request(4, "GET", "/services/data/v66.0/limits", "", 200, `{}`),
	}}

	result := ConvertSession(session, "")
	if result.Script == nil || len(result.Script.Init) != 3 || len(result.Script.Actions) != 1 {
		t.Fatalf("init = %d, actions = %d; want the sign-in in init", len(result.Script.Init), len(result.Script.Actions))
	}
	for _, action := range result.Script.Init {
		for _, key := range []string{"har.pageref", "har.timings"} {
			if _, ok := action.Metadata[key]; ok {
				t.Fatalf("init step %s %s kept %s", action.Method, action.Target, key)
			}
		}
	}
	// The failed attempt's gate is in its JSON form, as a saved draft returns it.
	if blockers, ok := result.Script.Init[0].Metadata["execution_blockers"].([]any); !ok || len(blockers) == 0 {
		t.Fatalf("failed attempt gates = %#v, want JSON-normalized execution_blockers", result.Script.Init[0].Metadata["execution_blockers"])
	}
	if len(result.Baseline) != 1 || result.Baseline[0].ID != 0 || result.Baseline[0].RequestURL != host+"/services/data/v66.0/limits" {
		t.Fatalf("baseline = %+v, want only the iteration's request", result.Baseline)
	}
}

// Recorded inputs are parameterized in every phase: a sign-in may run in init.
func TestParameterizeInputsCoversInit(t *testing.T) {
	script := &dsl.Script{
		Init:    []dsl.Action{{Type: "http", Method: "POST", Target: "/login", Body: "username=alice&lt=standard"}},
		Actions: []dsl.Action{{Type: "http", Method: "GET", Target: "/search?q=shoes"}},
	}
	parameterizeInputs(script, "recording")
	if script.Init[0].Body != "username={{data."+parameterizedDataSourceName+".username}}&lt=standard" ||
		script.Actions[0].Target != "/search?q={{data."+parameterizedDataSourceName+".q}}" {
		t.Fatalf("init body = %q, action target = %q", script.Init[0].Body, script.Actions[0].Target)
	}
}

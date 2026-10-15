package scriptdisclosure

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// Cookies and session, anti-forgery and tracing headers are state the source
// recorded, not credentials, so a script view shows them as recorded. An
// operator credential beside them stays masked, and so does a recorded value
// that repeats one.
func TestProjectShowsRecordedStateHeadersAsRecorded(t *testing.T) {
	const bearer = "operator-bearer-7c1d"
	script := &dsl.Script{
		Name: "recorded state", Protocol: "http",
		Config: dsl.ScriptConfig{HTTP: dsl.HTTPConfig{DefaultHeaders: map[string]string{
			"Cookie":       "JSESSIONID=default-session-41",
			"X-Request-Id": "req-1",
		}}},
		Actions: []dsl.Action{{
			Name: "login", Type: "request", Method: "GET", Target: "https://example.test/home",
			HeaderList: dsl.HeaderList{
				{Name: "Cookie", Value: "BIGipServer=pool-9; sid=recorded-sid-52"},
				{Name: "X-CSRF-Token", Value: "recorded-csrf-63"},
				{Name: "traceparent", Value: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"},
				{Name: "idpctx-session-id", Value: "recorded-session-74"},
				{Name: "Authorization", Value: "Bearer " + bearer},
				{Name: "Ocp-Apim-Subscription-Key", Value: "subscription-key-85"},
				{Name: "X-Session-Echo", Value: "token=" + bearer},
			},
			Headers: map[string]string{"X-XSRF-TOKEN": "recorded-xsrf-96"},
		}},
	}
	projected, err := Project(script)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	payload, err := json.Marshal(projected)
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	for _, shown := range []string{
		"JSESSIONID=default-session-41",
		"BIGipServer=pool-9; sid=recorded-sid-52",
		"recorded-csrf-63",
		"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		"recorded-session-74",
		"recorded-xsrf-96",
	} {
		if !strings.Contains(text, shown) {
			t.Errorf("script view hid recorded state %q: %s", shown, text)
		}
	}
	for _, hidden := range []string{bearer, "subscription-key-85"} {
		if strings.Contains(text, hidden) {
			t.Errorf("script view showed credential %q: %s", hidden, text)
		}
	}
	if !strings.Contains(text, `"X-Session-Echo","value":"token=[REDACTED]"`) {
		t.Errorf("a recorded header repeating the bearer token was not masked: %s", text)
	}
	if script.Actions[0].HeaderList[4].Value != "Bearer "+bearer {
		t.Fatal("projection mutated the source script")
	}
}

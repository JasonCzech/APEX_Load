package browser

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/selector"
)

func TestSalesforceAuthURL(t *testing.T) {
	for raw, want := range map[string]bool{
		"https://login.salesforce.com/?locale=us":                                         true,
		"https://test.salesforce.com/":                                                    true,
		"https://acme.my.salesforce.com/":                                                 true,
		"https://acme.my.salesforce.com/?ec=302&startURL=%2Flightning%2Fpage%2Fhome":      true,
		"https://acme.my.salesforce.com/_ui/identity/verification/method/SmsVerification": true,
		"https://acme.my.salesforce.com/lightning/page/home":                              false,
		"https://acme.lightning.force.com/lightning/page/home":                            false,
		"https://example.com/_ui/identity/verification/x":                                 false,
		"": false,
	} {
		if got := salesforceAuthURL(raw); got != want {
			t.Errorf("salesforceAuthURL(%q) = %v, want %v", raw, got, want)
		}
	}
}

// A browser recording that signs in to Salesforce first converts with the
// sign-in in init, tagged, and its one-time verification code masked and
// flagged; the journey stays in the iteration.
func TestConvertPutsTheSalesforceSignInInInit(t *testing.T) {
	login := "https://acme.my.salesforce.com/"
	verify := "https://acme.my.salesforce.com/_ui/identity/verification/method/SmsVerificationFinish"
	app := "https://acme.lightning.force.com/lightning/page/home"
	candidate := func(value string) []selector.Candidate {
		return []selector.Candidate{{Kind: "css", Value: value, Confidence: 90}}
	}
	session := &Session{
		StartURL: login,
		Steps: []Step{
			{Seq: 1, Kind: StepFill, Value: "perf.user@acme.test", URL: login, Timestamp: ts(1000), Candidates: candidate("#username")},
			{Seq: 2, Kind: StepFill, Masked: true, URL: login, Timestamp: ts(2000), Candidates: candidate("#password"), Element: &selector.ElementMetadata{Tag: "input", ID: "password", Type: "password"}},
			{Seq: 3, Kind: StepClick, URL: login, Timestamp: ts(3000), Candidates: candidate("#Login")},
			{Seq: 4, Kind: StepFill, Masked: true, URL: verify, Timestamp: ts(9000), Candidates: candidate("#smc"), Element: &selector.ElementMetadata{Tag: "input", ID: "smc", Attributes: map[string]string{"autocomplete": "one-time-code"}}},
			{Seq: 5, Kind: StepClick, URL: verify, Timestamp: ts(10000), Candidates: candidate("#save")},
			{Seq: 6, Kind: StepClick, URL: app, Timestamp: ts(20000), Candidates: candidate("a[title=Accounts]")},
		},
	}
	result := Convert(session, ConvertOptions{})
	script := result.Script
	if len(script.Init) != 6 || len(script.Actions) != 1 {
		t.Fatalf("init %d, actions %d: %+v", len(script.Init), len(script.Actions), script)
	}
	if script.Init[0].Type != "browser_navigate" || script.Actions[0].Target != "a[title=Accounts]" {
		t.Fatalf("init[0] %+v, actions[0] %+v", script.Init[0], script.Actions[0])
	}
	for _, action := range script.Init[1:] {
		if action.Metadata["salesforce_auth"] != true {
			t.Fatalf("sign-in step not tagged: %+v", action)
		}
	}
	code := script.Init[4]
	if code.Metadata["source_one_time_code"] != true || !strings.HasPrefix(code.Value, "{{env.") {
		t.Fatalf("verification code step = %+v", code)
	}
	joined := strings.Join(result.Warnings, "\n")
	if !strings.Contains(joined, "Salesforce sign-in (5 step(s)) runs in init") || !strings.Contains(joined, "one-time verification code") {
		t.Fatalf("warnings = %s", joined)
	}
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("invalid script: %v", issues)
	}

	// Only a sign-in: it is the journey.
	only := &Session{StartURL: login, Steps: session.Steps[:3]}
	if got := Convert(only, ConvertOptions{}).Script; len(got.Init) != 0 {
		t.Fatalf("a sign-in-only recording moved to init: %+v", got.Init)
	}

	// A transaction marked around the sign-in moves with it.
	wrapped := &Session{StartURL: login, Steps: session.Steps, Markers: []Marker{
		{Kind: MarkerTransactionStart, Name: "Login", Timestamp: ts(500)},
		{Kind: MarkerTransactionEnd, Name: "Login", Timestamp: ts(12000)},
	}}
	if got := Convert(wrapped, ConvertOptions{}).Script; len(got.Init) != 8 || got.Init[1].Type != "source_transaction" || got.Init[7].Type != "source_transaction" || len(got.Actions) != 1 {
		t.Fatalf("a Login transaction around the sign-in: init %+v, actions %+v", got.Init, got.Actions)
	}

	// A transaction that starts during the sign-in and ends after it keeps
	// the sign-in in the iteration.
	spanning := &Session{StartURL: login, Steps: session.Steps, Markers: []Marker{
		{Kind: MarkerTransactionStart, Name: "Open app", Timestamp: ts(2500)},
		{Kind: MarkerTransactionEnd, Name: "Open app", Timestamp: ts(21000)},
	}}
	spanned := Convert(spanning, ConvertOptions{})
	if len(spanned.Script.Init) != 0 || !strings.Contains(strings.Join(spanned.Warnings, "\n"), `transaction "Open app" starts during it`) {
		t.Fatalf("spanning transaction: init %+v, warnings %v", spanned.Script.Init, spanned.Warnings)
	}
}

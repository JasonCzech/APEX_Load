package browser

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/selector"
)

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
	// So is a converted range that holds only the sign-in.
	if got := Convert(session, ConvertOptions{StepRange: StepRange{StartSeq: 1, EndSeq: 5}}).Script; len(got.Init) != 0 || len(got.Actions) == 0 {
		t.Fatalf("a sign-in-only range: init %+v, actions %+v", got.Init, got.Actions)
	} else if issues := dsl.ValidateScriptIssues(got); len(issues) != 0 {
		t.Fatalf("a sign-in-only range is invalid: %v", issues)
	}

	// A transaction marked around the sign-in moves with it.
	wrapped := &Session{StartURL: login, Steps: session.Steps, Markers: []Marker{
		{Kind: MarkerTransactionStart, Name: "Login", Timestamp: ts(500)},
		{Kind: MarkerTransactionEnd, Name: "Login", Timestamp: ts(12000)},
	}}
	if got := Convert(wrapped, ConvertOptions{}).Script; len(got.Init) != 8 || got.Init[1].Type != "source_transaction" || got.Init[7].Type != "source_transaction" || len(got.Actions) != 1 {
		t.Fatalf("a Login transaction around the sign-in: init %+v, actions %+v", got.Init, got.Actions)
	}
	// A script split at transactions is one transaction, so the Login
	// transaction's sign-in stays in its iteration.
	split := Convert(wrapped, ConvertOptions{SplitAtTransactions: true})
	if len(split.Scripts) != 1 || len(split.Scripts[0].Init) != 0 || len(split.Scripts[0].Actions) != 6 || strings.Contains(strings.Join(split.Warnings, "\n"), "runs in init") {
		t.Fatalf("split at transactions: scripts %+v, warnings %v", split.Scripts, split.Warnings)
	}
	if split.Scripts[0].Actions[1].Metadata["salesforce_auth"] != true {
		t.Fatalf("split sign-in step not tagged: %+v", split.Scripts[0].Actions[1])
	}
	if issues := dsl.ValidateScriptIssues(split.Scripts[0]); len(issues) != 0 {
		t.Fatalf("split Login script is invalid: %v", issues)
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

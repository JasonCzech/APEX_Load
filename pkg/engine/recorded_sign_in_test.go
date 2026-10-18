package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	recorderbrowser "github.com/jasonczech/apexload/pkg/recorder/browser"
	"github.com/jasonczech/apexload/pkg/selector"
)

const (
	signInPage  = "https://acme.my.salesforce.com/"
	landingPage = "https://acme.lightning.force.com/lightning/page/home"
)

// recordedSignInScript is a converted recording: its sign-in in init, tagged
// and inside a Login transaction, after a navigation to the sign-in page.
func recordedSignInScript(strategy string) *dsl.Script {
	script := sessionGroupScript(strategy, 1)
	signIn := func(action dsl.Action) dsl.Action {
		action.Metadata = map[string]any{"salesforce_auth": true, "source_url": signInPage}
		return action
	}
	script.Config.BaseURL = "https://acme.my.salesforce.com"
	script.Init = []dsl.Action{
		{Name: "Navigate to sign-in", Type: "browser_navigate", Target: "/"},
		dsl.TransactionMarker("Login", true),
		signIn(dsl.Action{Name: "User", Type: "browser_fill", Selector: "#username", Value: "{{identity.username}}"}),
		signIn(dsl.Action{Name: "Password", Type: "browser_fill", Selector: "#password", Value: "{{identity.password}}"}),
		signIn(dsl.Action{Name: "Log in", Type: "browser_click", Selector: "#Login"}),
		dsl.TransactionMarker("Login", false),
	}
	script.Actions = []dsl.Action{{Name: "Accounts", Type: "browser_click", Selector: "a[title=Accounts]", Metadata: map[string]any{"source_url": landingPage}}}
	return script
}

// A seeded observer skips the tagged sign-in and the Login transaction
// around only it, and opens the page the recording reached after signing in
// instead of the sign-in page; other observers run the recording as is.
func TestRecordedSignInPlan(t *testing.T) {
	plan := newRecordedSignInPlan(recordedSignInScript(dsl.BrowserSessionStorageStatePool))
	if plan == nil || plan.steps != 3 || plan.landing != landingPage || plan.strategy != dsl.BrowserSessionStorageStatePool {
		t.Fatalf("plan = %+v", plan)
	}
	for index, want := range []bool{false, true, true, true, true, true} {
		if plan.skips(index) != want {
			t.Errorf("init[%d] skipped = %v, want %v", index, plan.skips(index), want)
		}
	}
	if got := plan.action(dsl.Action{Type: "browser_navigate", Target: "/"}); got.Target != landingPage {
		t.Fatalf("sign-in navigation opens %q", got.Target)
	}
	for _, target := range []string{"https://acme.lightning.force.com/lightning/o/Account/list", "{{start_url}}"} {
		if got := plan.action(dsl.Action{Type: "browser_navigate", Target: target}); got.Target != target {
			t.Fatalf("navigation to %q was rewritten to %q", target, got.Target)
		}
	}

	// actionRef, for callers holding the script's own action, copies only a
	// rewritten navigation and never changes the script.
	script := recordedSignInScript(dsl.BrowserSessionStorageStatePool)
	navigation := &script.Init[0]
	if got := plan.actionRef(navigation); got == navigation || got.Target != landingPage || navigation.Target != "/" {
		t.Fatalf("actionRef(sign-in navigation) = %p %q, script action %p %q", got, got.Target, navigation, navigation.Target)
	}
	if fill := &script.Init[2]; plan.actionRef(fill) != fill {
		t.Fatal("actionRef copied an action it does not rewrite")
	}
	var none *recordedSignInPlan
	if none.actionRef(navigation) != navigation || none.opensSignIn(navigation) {
		t.Fatal("a nil plan rewrote the navigation")
	}

	// A transaction around more than the sign-in still measures the rest.
	wider := recordedSignInScript(dsl.BrowserSessionSalesforceJWT)
	wider.Init = append(wider.Init[:5:5], dsl.Action{Name: "Home", Type: "browser_wait", Selector: "one-app-nav-bar"}, wider.Init[5])
	if plan := newRecordedSignInPlan(wider); plan == nil || plan.skips(1) || plan.skips(6) || plan.skips(5) {
		t.Fatalf("a wider transaction's markers were skipped: %+v", plan)
	}

	// The landing page is unknown without the next step's recorded page.
	unknown := recordedSignInScript(dsl.BrowserSessionStorageStatePool)
	unknown.Actions[0].Metadata = nil
	if plan := newRecordedSignInPlan(unknown); plan == nil || plan.landing != "" || plan.action(dsl.Action{Type: "browser_navigate", Target: "/"}).Target != "/" {
		t.Fatalf("an unknown landing page rewrote the navigation: %+v", plan)
	}

	for name, script := range map[string]*dsl.Script{
		"login_steps": recordedSignInScript(dsl.BrowserSessionLoginSteps),
		"no session": func() *dsl.Script {
			s := recordedSignInScript(dsl.BrowserSessionStorageStatePool)
			s.Config.Browser.Session = nil
			return s
		}(),
		"untagged": func() *dsl.Script {
			s := recordedSignInScript(dsl.BrowserSessionStorageStatePool)
			s.Init = s.Init[:1]
			return s
		}(),
	} {
		if plan := newRecordedSignInPlan(script); plan != nil {
			t.Errorf("%s: plan = %+v, want the recording run as is", name, plan)
		}
	}
}

// A run's observers and Verify skip the recorded sign-in under a seeded
// session and open the landing page; a login_steps observer signs in.
func TestSeededObserversSkipTheRecordedSignIn(t *testing.T) {
	useSessionFake()
	reset := func() {
		sessionFake.mu.Lock()
		sessionFake.configs, sessionFake.executed, sessionFake.targets, sessionFake.signedOut, sessionFake.refused, sessionFake.started = nil, nil, nil, nil, nil, map[string]time.Time{}
		sessionFake.mu.Unlock()
	}
	ran := func() (string, string) {
		sessionFake.mu.Lock()
		defer sessionFake.mu.Unlock()
		return strings.Join(sessionFake.executed, ","), strings.Join(sessionFake.targets, ",")
	}
	for strategy, want := range map[string]struct{ executed, targets string }{
		dsl.BrowserSessionStorageStatePool: {"perf1@acme.test|Navigate to sign-in,perf1@acme.test|Accounts", "perf1@acme.test|" + landingPage},
		dsl.BrowserSessionLoginSteps: {"perf1@acme.test|Navigate to sign-in,perf1@acme.test|User,perf1@acme.test|Password,perf1@acme.test|Log in,perf1@acme.test|Accounts",
			"perf1@acme.test|/"},
	} {
		reset()
		mge, err := NewMultiGroup(MultiGroupConfig{Groups: []GroupConfig{{Name: "observers", Script: recordedSignInScript(strategy), VUsers: 1, SecretResolver: sessionResolver()}}, Duration: 30 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := mge.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if executed, targets := ran(); executed != want.executed || targets != want.targets {
			t.Errorf("%s run: executed %s, navigated %s", strategy, executed, targets)
		}

		reset()
		report, err := ReplayScript(context.Background(), recordedSignInScript(strategy), ReplayOptions{IgnoreThinkTime: true, SecretResolver: sessionResolver()})
		if err != nil || !report.Success {
			t.Fatalf("%s Verify: %v %+v", strategy, err, report)
		}
		if executed, targets := ran(); executed != want.executed || targets != want.targets {
			t.Errorf("%s Verify: executed %s, navigated %s", strategy, executed, targets)
		}
	}
}

// A renewed salesforce_jwt session runs init again, its recorded sign-in
// skipped, so the next iteration starts on the landing page, not a blank one.
func TestRenewedSessionReopensTheLandingPage(t *testing.T) {
	script := recordedSignInScript(dsl.BrowserSessionSalesforceJWT)
	script.Config.RunLogic.Iterations = 2
	script.Config.Browser.Session.Identities[0].StorageState, script.Config.Browser.Session.Identities[0].Password = "", ""
	script.Config.Browser.Session.JWT = &dsl.BrowserSessionJWT{LoginURL: "https://login.salesforce.com", ClientID: "consumer-key", PrivateKey: "{{secret.sf_jwt_key}}"}
	script.Actions = []dsl.Action{{Name: "Open record", Type: "browser_navigate", Target: "https://login.salesforce.com/", Metadata: map[string]any{"source_url": landingPage}}}
	resolver := SecretResolverFunc(func(name string) (string, bool) {
		if name == "sf_jwt_key" {
			return "-----BEGIN RSA PRIVATE KEY-----\nkey\n-----END RSA PRIVATE KEY-----", true
		}
		return sessionResolver().Resolve(name)
	})
	useSessionFake()
	sessionFake.mu.Lock()
	sessionFake.configs, sessionFake.executed, sessionFake.targets, sessionFake.refused, sessionFake.started = nil, nil, nil, nil, map[string]time.Time{}
	// The iteration's navigation lands on the sign-in page: the session is lost.
	sessionFake.signedOut = map[string]bool{"perf1@acme.test": true}
	sessionFake.mu.Unlock()
	sink := &allResultsSink{}
	mge, err := NewMultiGroup(MultiGroupConfig{Groups: []GroupConfig{{Name: "observers", Script: script, VUsers: 1, SecretResolver: resolver, ResultSink: sink}}, Duration: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mge.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	sessionFake.mu.Lock()
	defer sessionFake.mu.Unlock()
	if got := strings.Join(sessionFake.executed, ","); got != "perf1@acme.test|Navigate to sign-in,perf1@acme.test|Open record,perf1@acme.test|Navigate to sign-in,perf1@acme.test|Open record" {
		t.Fatalf("executed %s: want init again after the renewal", got)
	}
	if len(sessionFake.configs) != 2 {
		t.Fatalf("%d browser inits, want a new context for the renewal", len(sessionFake.configs))
	}
	var lost int
	for _, result := range sink.results {
		if strings.HasPrefix(result.ErrorMessage, sessionLostPrefix) {
			lost++
		}
	}
	if lost != 2 {
		t.Fatalf("%d lost sessions, want one per iteration", lost)
	}
}

// The converter's sign-in tags and recorded pages are what the plan reads: a
// converted Salesforce recording with identity verification, in a Login
// transaction, skips its whole sign-in and opens the app page it reached.
func TestRecordedSignInPlanReadsConvertedRecordings(t *testing.T) {
	login, verify, app := "https://acme.my.salesforce.com/", "https://acme.my.salesforce.com/_ui/identity/verification/method/SmsVerificationFinish", "https://acme.lightning.force.com/lightning/page/home"
	at := func(ms int) time.Time { return time.Unix(1_700_000_000, 0).Add(time.Duration(ms) * time.Millisecond) }
	candidate := func(value string) []selector.Candidate {
		return []selector.Candidate{{Kind: "css", Value: value, Confidence: 90}}
	}
	session := &recorderbrowser.Session{
		StartURL: login,
		Steps: []recorderbrowser.Step{
			{Seq: 1, Kind: recorderbrowser.StepFill, Value: "perf.user@acme.test", URL: login, Timestamp: at(1000), Candidates: candidate("#username")},
			{Seq: 2, Kind: recorderbrowser.StepFill, Masked: true, URL: login, Timestamp: at(2000), Candidates: candidate("#password"), Element: &selector.ElementMetadata{Tag: "input", ID: "password", Type: "password"}},
			{Seq: 3, Kind: recorderbrowser.StepClick, URL: login, Timestamp: at(3000), Candidates: candidate("#Login")},
			{Seq: 4, Kind: recorderbrowser.StepFill, Masked: true, URL: verify, Timestamp: at(9000), Candidates: candidate("#smc"), Element: &selector.ElementMetadata{Tag: "input", ID: "smc", Attributes: map[string]string{"autocomplete": "one-time-code"}}},
			{Seq: 5, Kind: recorderbrowser.StepClick, URL: verify, Timestamp: at(10000), Candidates: candidate("#save")},
			{Seq: 6, Kind: recorderbrowser.StepClick, URL: app, Timestamp: at(20000), Candidates: candidate("a[title=Accounts]")},
		},
		Markers: []recorderbrowser.Marker{
			{Kind: recorderbrowser.MarkerTransactionStart, Name: "Login", Timestamp: at(500)},
			{Kind: recorderbrowser.MarkerTransactionEnd, Name: "Login", Timestamp: at(12000)},
		},
	}
	script := recorderbrowser.Convert(session, recorderbrowser.ConvertOptions{}).Script
	script.Config.Browser.Session = &dsl.BrowserSessionConfig{Strategy: dsl.BrowserSessionStorageStatePool, Identities: []dsl.BrowserIdentity{{Username: "perf1@acme.test", StorageState: "{{secret.state}}"}}}
	plan := newRecordedSignInPlan(script)
	if plan == nil || plan.steps != 5 || plan.landing != app {
		t.Fatalf("plan = %+v for init %+v", plan, script.Init)
	}
	var ran []string
	for index, action := range script.Init {
		if !plan.skips(index) {
			ran = append(ran, plan.action(action).Type+" "+plan.action(action).Target)
		}
	}
	if strings.Join(ran, ",") != "browser_navigate "+app {
		t.Fatalf("a seeded observer's init runs %v", ran)
	}
}

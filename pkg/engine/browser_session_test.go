package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

const sessionFakeProtocol = "browser_session_fake"

var registerSessionFake sync.Once

func useSessionFake() {
	registerSessionFake.Do(func() {
		protocol.Register(sessionFakeProtocol, func() protocol.Protocol { return &sessionFakeBrowser{} })
	})
}

type sessionFakeRecord struct {
	mu      sync.Mutex
	configs []map[string]any
	fills   []string
	// signedOut lists the users the target sends to sign in; started is when
	// each user's first step ran; executed lists user|action per step.
	signedOut map[string]bool
	refused   map[string]bool
	started   map[string]time.Time
	executed  []string
}

var sessionFake = &sessionFakeRecord{}

type sessionFakeBrowser struct{ username string }

func (*sessionFakeBrowser) Name() string { return sessionFakeProtocol }
func (b *sessionFakeBrowser) Init(_ context.Context, config map[string]any) error {
	sessionFake.mu.Lock()
	defer sessionFake.mu.Unlock()
	sessionFake.configs = append(sessionFake.configs, config)
	b.username, _ = config["browser_identity_username"].(string)
	return nil
}
func (b *sessionFakeBrowser) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	sessionFake.mu.Lock()
	defer sessionFake.mu.Unlock()
	if action.Type == "browser_fill" {
		sessionFake.fills = append(sessionFake.fills, action.Value)
	}
	if _, seen := sessionFake.started[b.username]; !seen && sessionFake.started != nil {
		sessionFake.started[b.username] = time.Now()
	}
	sessionFake.executed = append(sessionFake.executed, b.username+"|"+action.Name)
	if action.Type == "browser_navigate" && sessionFake.refused[b.username] {
		return nil, fmt.Errorf("net::ERR_CONNECTION_REFUSED at %s", action.Target)
	}
	result := &protocol.Result{ActionName: action.Name, Success: true, Duration: time.Millisecond}
	if action.Type == "browser_navigate" {
		result.PageURL = "https://acme.lightning.force.com/lightning/page/home"
		if sessionFake.signedOut[b.username] {
			result.PageURL = "https://login.salesforce.com/?ec=302&startURL=%2Flightning%2Fpage%2Fhome"
		}
	}
	return result, nil
}
func (*sessionFakeBrowser) Teardown(context.Context) error    { return nil }
func (*sessionFakeBrowser) HealthCheck(context.Context) error { return nil }

func storageStateFor(user string) string {
	return fmt.Sprintf(`{"cookies":[{"name":"sid","value":"session-of-%s","domain":"acme.my.salesforce.com","path":"/","expires":-1,"httpOnly":true,"secure":true,"sameSite":"Lax"}],"origins":[]}`, user)
}

func sessionResolver() SecretResolver {
	return SecretResolverFunc(func(name string) (string, bool) {
		switch {
		case strings.HasPrefix(name, "sf_state_"):
			return storageStateFor(strings.TrimPrefix(name, "sf_state_")), true
		case strings.HasPrefix(name, "sf_pw_"):
			return "password-of-" + strings.TrimPrefix(name, "sf_pw_"), true
		}
		return "", false
	})
}

func sessionGroupScript(strategy string, users int) *dsl.Script {
	identities := make([]dsl.BrowserIdentity, users)
	for i := range identities {
		identities[i] = dsl.BrowserIdentity{Username: fmt.Sprintf("perf%d@acme.test", i+1), StorageState: fmt.Sprintf("{{secret.sf_state_%d}}", i+1), Password: fmt.Sprintf("{{secret.sf_pw_%d}}", i+1)}
	}
	return &dsl.Script{
		Name: "observer", Protocol: sessionFakeProtocol,
		Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}, Browser: dsl.BrowserConfig{Session: &dsl.BrowserSessionConfig{Strategy: strategy, Identities: identities}}},
		Init: []dsl.Action{
			{Name: "User", Type: "browser_fill", Selector: "#username", Value: "{{identity.username}}"},
			{Name: "Password", Type: "browser_fill", Selector: "#password", Value: "{{identity.password}}"},
		},
		Actions: []dsl.Action{{Name: "Open", Type: "browser_navigate", Target: "/"}},
	}
}

func runSessionGroup(t *testing.T, script *dsl.Script, vusers int, signedOut ...string) error {
	t.Helper()
	sessionFake.mu.Lock()
	sessionFake.signedOut, sessionFake.refused = map[string]bool{}, map[string]bool{}
	for _, user := range signedOut {
		sessionFake.signedOut[user] = true
	}
	sessionFake.mu.Unlock()
	useSessionFake()
	sessionFake.mu.Lock()
	sessionFake.configs, sessionFake.fills, sessionFake.executed = nil, nil, nil
	sessionFake.started = map[string]time.Time{}
	sessionFake.mu.Unlock()
	mge, err := NewMultiGroup(MultiGroupConfig{Groups: []GroupConfig{{Name: "observers", Script: script, VUsers: vusers, SecretResolver: sessionResolver()}}, Duration: 30 * time.Second})
	if err != nil {
		return err
	}
	_, err = mge.Run(context.Background())
	return err
}

// Each observer runs as its own identity for its life: a storage_state_pool
// observer's browser gets its own storage state, and a login observer signs in
// with its own username and password.
func TestObserversRunAsTheirOwnIdentities(t *testing.T) {
	if err := runSessionGroup(t, sessionGroupScript(dsl.BrowserSessionStorageStatePool, 3), 3); err != nil {
		t.Fatal(err)
	}
	sessionFake.mu.Lock()
	var seen []string
	for _, config := range sessionFake.configs {
		if _, leaked := config[browserSessionStorageStatesKey]; leaked {
			t.Fatal("an observer was given every identity's storage state")
		}
		seen = append(seen, fmt.Sprintf("%v|%v|%v", config["browser_identity_index"], config["browser_identity_username"], strings.Contains(fmt.Sprint(config["browser_storage_state"]), fmt.Sprintf("session-of-%v", config["browser_identity_index"]))))
	}
	sort.Strings(seen)
	fills := append([]string(nil), sessionFake.fills...)
	sessionFake.mu.Unlock()
	if strings.Join(seen, ",") != "1|perf1@acme.test|true,2|perf2@acme.test|true,3|perf3@acme.test|true" {
		t.Fatalf("observer configs = %v", seen)
	}
	sort.Strings(fills)
	want := []string{"password-of-1", "password-of-2", "password-of-3", "perf1@acme.test", "perf2@acme.test", "perf3@acme.test"}
	if strings.Join(fills, ",") != strings.Join(want, ",") {
		t.Fatalf("filled values = %v, want each observer's own username and password", fills)
	}
}

// A run with more observers than identities fails before it starts.
func TestMoreObserversThanIdentitiesFailsTheRun(t *testing.T) {
	err := runSessionGroup(t, sessionGroupScript(dsl.BrowserSessionLoginSteps, 2), 3)
	if err == nil || !strings.Contains(err.Error(), "3 observers need 3 identities") {
		t.Fatalf("err = %v", err)
	}
}

// Every identity's storage state is redacted, down to its cookie values, and
// session scripts take disjoint ordinals across groups.
func TestSessionIdentitiesAreRedactedAndAllocated(t *testing.T) {
	script := sessionGroupScript(dsl.BrowserSessionStorageStatePool, 2)
	values := strings.Join(runtimeProtocolAuthCompositeValues(script, sessionResolver()), "\n")
	for _, user := range []string{"1", "2"} {
		if !strings.Contains(values, "session-of-"+user) {
			t.Fatalf("identity %s's session cookie is not redacted: %q", user, values)
		}
	}
	if !HasAllocatedParameters(script) {
		t.Fatal("a session script does not allocate ordinals")
	}
	allocations, err := PlanScenarioParameterAllocations([]*dsl.Script{script, script}, []int{1, 1})
	if err != nil || allocations[0].Offset == allocations[1].Offset || allocations[0].Population != 2 {
		t.Fatalf("allocations = %+v, %v", allocations, err)
	}
	if _, err := PlanScenarioParameterAllocations([]*dsl.Script{script, script}, []int{2, 1}); err == nil || !strings.Contains(err.Error(), "3 observers need 3 identities") {
		t.Fatalf("two groups sharing 2 identities among 3 observers planned: %v", err)
	}
}

// Identity secrets are named in the script, so snapshots, generator secret
// bundles and the launch check find them; a generator running a later slice
// of the observers binds the identities at its offset.
func TestSessionIdentitiesAcrossGenerators(t *testing.T) {
	script := sessionGroupScript(dsl.BrowserSessionStorageStatePool, 3)
	names, err := ReferencedSecretNames(script)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(names, ","); got != "sf_pw_1,sf_pw_2,sf_pw_3,sf_state_1,sf_state_2,sf_state_3" {
		t.Fatalf("referenced secrets = %s", got)
	}
	useSessionFake()
	sessionFake.mu.Lock()
	sessionFake.configs = nil
	sessionFake.mu.Unlock()
	mge, err := NewMultiGroup(MultiGroupConfig{Groups: []GroupConfig{{
		Name: "observers", Script: script, VUsers: 1, SecretResolver: sessionResolver(),
		ParameterAllocation: ParameterAllocation{Population: 3, Offset: 2, Iterations: 1},
	}}, Duration: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mge.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	sessionFake.mu.Lock()
	defer sessionFake.mu.Unlock()
	if len(sessionFake.configs) != 1 || sessionFake.configs[0]["browser_identity_username"] != "perf3@acme.test" {
		t.Fatalf("the generator's observer = %v", sessionFake.configs)
	}
}

// Before the run starts, each pooled observer opens its session on the
// preflight page; one sent to sign in fails the run, named, with no secret.
func TestSessionPreflightFailsTheRunNamingExpiredSessions(t *testing.T) {
	script := sessionGroupScript(dsl.BrowserSessionStorageStatePool, 3)
	script.Init = nil
	script.Config.Browser.Session.PreflightURL = "/lightning/page/home"
	err := runSessionGroup(t, script, 3, "perf2@acme.test")
	if err == nil || !strings.Contains(err.Error(), "observer 2 (perf2@acme.test) was sent to sign in at login.salesforce.com") || strings.Contains(err.Error(), "observer 1") || strings.Contains(err.Error(), "session-of") {
		t.Fatalf("err = %v", err)
	}
	sessionFake.mu.Lock()
	executed := append([]string(nil), sessionFake.executed...)
	configs := append([]map[string]any(nil), sessionFake.configs...)
	sessionFake.mu.Unlock()
	for _, step := range executed {
		if !strings.HasSuffix(step, "|Session preflight") {
			t.Fatalf("a step ran although the preflight failed: %v", executed)
		}
	}
	// A preflight page is not run evidence: no HAR, no replay-mode healing.
	for _, config := range configs {
		if config["browser_har_capture"] != "off" || config["apexload_run_mode"] != "load" {
			t.Fatalf("preflight config = har %v, mode %v", config["browser_har_capture"], config["apexload_run_mode"])
		}
	}
	if err := runSessionGroup(t, script, 3); err != nil {
		t.Fatalf("valid sessions failed the preflight: %v", err)
	}
	// An observer that cannot reach the page is named with the cause.
	useSessionFake()
	sessionFake.mu.Lock()
	sessionFake.signedOut, sessionFake.refused = map[string]bool{}, map[string]bool{"perf3@acme.test": true}
	sessionFake.mu.Unlock()
	mge, err := NewMultiGroup(MultiGroupConfig{Groups: []GroupConfig{{Name: "observers", Script: script, VUsers: 3, SecretResolver: sessionResolver()}}, Duration: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := mge.PreflightBrowserSessions(context.Background()); err == nil || !strings.Contains(err.Error(), "observer 3 (perf3@acme.test) could not open the preflight page (net::ERR_CONNECTION_REFUSED at /lightning/page/home)") {
		t.Fatalf("refused preflight = %v", err)
	}
	// A stopped run reports its cancellation, not the observers it stopped.
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	stopped, err := NewMultiGroup(MultiGroupConfig{Groups: []GroupConfig{{Name: "observers", Script: script, VUsers: 3, SecretResolver: sessionResolver()}}, Duration: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := stopped.PreflightBrowserSessions(cancelled); err != context.Canceled {
		t.Fatalf("cancelled preflight = %v", err)
	}
}

// An observer sent to sign in mid-iteration fails as session_lost and ends
// the iteration, whatever its continue-on-error setting.
func TestSessionLostEndsTheIteration(t *testing.T) {
	script := sessionGroupScript(dsl.BrowserSessionStorageStatePool, 1)
	script.Init = nil
	continueOnError := true
	script.Config.ErrorHandling.ContinueOnError = &continueOnError
	script.Actions = append(script.Actions, dsl.Action{Name: "Accounts", Type: "browser_click", Selector: "a[title=Accounts]"})
	sink := &allResultsSink{}
	useSessionFake()
	sessionFake.mu.Lock()
	sessionFake.signedOut, sessionFake.executed, sessionFake.started = map[string]bool{"perf1@acme.test": true}, nil, map[string]time.Time{}
	sessionFake.mu.Unlock()
	mge, err := NewMultiGroup(MultiGroupConfig{Groups: []GroupConfig{{Name: "observers", Script: script, VUsers: 1, SecretResolver: sessionResolver(), ResultSink: sink}}, Duration: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mge.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	var lost *protocol.Result
	for _, result := range sink.results {
		if result.ActionName == "Open" {
			lost = result
		}
	}
	if lost == nil || lost.Success || !strings.HasPrefix(lost.ErrorMessage, "session_lost: observer 1 (perf1@acme.test) was sent to sign in") || ClassifyError(0, lost.ErrorMessage, false) != ErrorClassSessionLost {
		t.Fatalf("lost step = %+v", lost)
	}
	sessionFake.mu.Lock()
	defer sessionFake.mu.Unlock()
	for _, step := range sessionFake.executed {
		if strings.HasSuffix(step, "|Accounts") {
			t.Fatalf("the iteration went on after its session was lost: %v", sessionFake.executed)
		}
	}
}

// Observers start no closer together than their start gap, in ordinal order.
func TestObserversStartApartByTheirGap(t *testing.T) {
	script := sessionGroupScript(dsl.BrowserSessionLoginSteps, 3)
	script.Config.Browser.Session.StartGapMs = 300
	before := time.Now()
	if err := runSessionGroup(t, script, 3); err != nil {
		t.Fatal(err)
	}
	sessionFake.mu.Lock()
	defer sessionFake.mu.Unlock()
	first, second, third := sessionFake.started["perf1@acme.test"], sessionFake.started["perf2@acme.test"], sessionFake.started["perf3@acme.test"]
	// The run started after before, so observer n starts no earlier than
	// before plus n-1 gaps; the first is not held back.
	if first.IsZero() || !first.Before(second) || second.Sub(before) < 300*time.Millisecond || third.Sub(before) < 600*time.Millisecond {
		t.Fatalf("starts after %v: %v, %v, %v; want observer n at least n-1 gaps after the run started", before, first, second, third)
	}
}

// A coordinated start's lead is too short to open browsers: staging checks
// the sessions once, its result is kept, and the run itself opens no
// preflight page.
func TestCoordinatedStartLeavesSessionPreflightToStaging(t *testing.T) {
	script := sessionGroupScript(dsl.BrowserSessionStorageStatePool, 2)
	script.Init = nil
	script.Config.Browser.Session.PreflightURL = "/lightning/page/home"
	useSessionFake()
	newEngine := func() *MultiGroupEngine {
		mge, err := NewMultiGroup(MultiGroupConfig{Groups: []GroupConfig{{Name: "observers", Script: script, VUsers: 2, SecretResolver: sessionResolver()}}, Duration: 30 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		return mge
	}
	executed := func() []string {
		sessionFake.mu.Lock()
		defer sessionFake.mu.Unlock()
		return append([]string(nil), sessionFake.executed...)
	}
	sessionFake.mu.Lock()
	sessionFake.signedOut, sessionFake.executed, sessionFake.started = map[string]bool{"perf2@acme.test": true}, nil, map[string]time.Time{}
	sessionFake.mu.Unlock()

	staged := newEngine()
	if err := staged.PreflightBrowserSessions(context.Background()); err == nil || !strings.Contains(err.Error(), "observer 2 (perf2@acme.test)") {
		t.Fatalf("staging preflight = %v", err)
	}
	if got := len(executed()); got != 2 {
		t.Fatalf("staging opened %d preflight pages, want 2", got)
	}
	if err := staged.PreflightBrowserSessions(context.Background()); err == nil || len(executed()) != 2 {
		t.Fatalf("a second preflight = %v after %v: want the first result, no new pages", err, executed())
	}

	sessionFake.mu.Lock()
	sessionFake.executed = nil
	sessionFake.mu.Unlock()
	if _, err := newEngine().RunAt(context.Background(), time.Now().Add(200*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	for _, step := range executed() {
		if strings.HasSuffix(step, "|Session preflight") {
			t.Fatalf("a coordinated start opened a preflight page: %v", executed())
		}
	}
}

// A step loses its session when it lands on Salesforce sign-in or its Aura
// requests come back aura:invalidSession; other Aura failures, other pages and
// observers without an identity do not.
func TestBrowserSessionLoss(t *testing.T) {
	aura := func(state string) map[string]any {
		return map[string]any{"aura_actions": map[string]any{"requests": 1, "actions": []any{map[string]any{"name": "RecordUiController.getRecordWithFields", "state": state}}}}
	}
	for name, tc := range map[string]struct {
		result *protocol.Result
		want   string
	}{
		"sign-in page":      {&protocol.Result{Success: true, PageURL: "https://acme.my.salesforce.com/?ec=302"}, "was sent to sign in"},
		"verification page": {&protocol.Result{Success: true, PageURL: "https://acme.my.salesforce.com/_ui/identity/verification/method/SmsVerification"}, "was sent to sign in"},
		"invalid session":   {&protocol.Result{Success: true, PageURL: "https://acme.lightning.force.com/lightning/page/home", ProtocolEvidence: aura("invalid_session")}, "invalid_session"},
		"framework error":   {&protocol.Result{Success: true, ProtocolEvidence: aura("framework_error")}, ""},
		"app page":          {&protocol.Result{Success: true, PageURL: "https://acme.lightning.force.com/lightning/page/home"}, ""},
	} {
		if got := browserSessionLossReason(tc.result, false); !strings.Contains(got, tc.want) || (tc.want == "") != (got == "") {
			t.Errorf("%s: reason %q, want %q", name, got, tc.want)
		}
	}
	// An iteration that signs in itself reaches the sign-in page on purpose,
	// but a rejected Aura session is still lost.
	if got := browserSessionLossReason(&protocol.Result{PageURL: "https://login.salesforce.com/"}, true); got != "" {
		t.Errorf("a per-iteration sign-in lost its session: %q", got)
	}
	if got := browserSessionLossReason(&protocol.Result{ProtocolEvidence: aura("invalid_session")}, true); !strings.Contains(got, "invalid_session") {
		t.Errorf("a per-iteration sign-in kept a rejected Aura session: %q", got)
	}
	anonymous := &VUser{}
	result := &protocol.Result{Success: true, PageURL: "https://login.salesforce.com/"}
	anonymous.markBrowserSessionLost(result)
	if !result.Success || result.LifecycleControl != nil {
		t.Fatalf("an observer without an identity lost its session: %+v", result)
	}
}

// A login_steps observer whose iteration signs in itself reaches the sign-in
// page every iteration on purpose: it is not a lost session.
func TestPerIterationSignInIsNotASessionLoss(t *testing.T) {
	script := sessionGroupScript(dsl.BrowserSessionLoginSteps, 1)
	script.Actions = append([]dsl.Action{{Name: "Sign in page", Type: "browser_navigate", Target: "/"}}, script.Init...)
	script.Init = nil
	sink := &allResultsSink{}
	useSessionFake()
	sessionFake.mu.Lock()
	sessionFake.signedOut, sessionFake.refused, sessionFake.executed, sessionFake.started = map[string]bool{"perf1@acme.test": true}, nil, nil, map[string]time.Time{}
	sessionFake.mu.Unlock()
	mge, err := NewMultiGroup(MultiGroupConfig{Groups: []GroupConfig{{Name: "observers", Script: script, VUsers: 1, SecretResolver: sessionResolver(), ResultSink: sink}}, Duration: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mge.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, result := range sink.results {
		if !result.Success {
			t.Fatalf("step %q failed: %s", result.ActionName, result.ErrorMessage)
		}
		names = append(names, result.ActionName)
	}
	if !strings.Contains(strings.Join(names, ","), "Sign in page,User,Password") {
		t.Fatalf("iteration steps = %v", names)
	}
}

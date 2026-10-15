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

type sessionFakeRecord struct {
	mu      sync.Mutex
	configs []map[string]any
	fills   []string
}

var sessionFake = &sessionFakeRecord{}

type sessionFakeBrowser struct{}

func (*sessionFakeBrowser) Name() string { return sessionFakeProtocol }
func (*sessionFakeBrowser) Init(_ context.Context, config map[string]any) error {
	sessionFake.mu.Lock()
	defer sessionFake.mu.Unlock()
	sessionFake.configs = append(sessionFake.configs, config)
	return nil
}
func (*sessionFakeBrowser) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	if action.Type == "browser_fill" {
		sessionFake.mu.Lock()
		sessionFake.fills = append(sessionFake.fills, action.Value)
		sessionFake.mu.Unlock()
	}
	return &protocol.Result{ActionName: action.Name, Success: true, Duration: time.Millisecond}, nil
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

func runSessionGroup(t *testing.T, script *dsl.Script, vusers int) error {
	t.Helper()
	registerSessionFake.Do(func() { protocol.Register(sessionFakeProtocol, func() protocol.Protocol { return &sessionFakeBrowser{} }) })
	sessionFake.mu.Lock()
	sessionFake.configs, sessionFake.fills = nil, nil
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
	registerSessionFake.Do(func() { protocol.Register(sessionFakeProtocol, func() protocol.Protocol { return &sessionFakeBrowser{} }) })
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

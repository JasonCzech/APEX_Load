package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type rejectingPreflightProtocol struct {
	initCalls      *atomic.Int32
	preflightCalls *atomic.Int32
	expectedConfig string
	panicValue     string
}

func (p *rejectingPreflightProtocol) Name() string { return "runtime_preflight_rejection_test" }
func (p *rejectingPreflightProtocol) Init(context.Context, map[string]any) error {
	p.initCalls.Add(1)
	return nil
}
func (*rejectingPreflightProtocol) Execute(context.Context, *protocol.Action) (*protocol.Result, error) {
	return &protocol.Result{Success: true}, nil
}
func (*rejectingPreflightProtocol) Teardown(context.Context) error    { return nil }
func (*rejectingPreflightProtocol) HealthCheck(context.Context) error { return nil }
func (p *rejectingPreflightProtocol) PreflightRuntime(_ context.Context, request protocol.RuntimePreflightRequest) (protocol.RuntimePreflightResult, error) {
	if p.preflightCalls != nil {
		p.preflightCalls.Add(1)
	}
	if p.panicValue != "" {
		panic(p.panicValue)
	}
	if p.expectedConfig != "" {
		resolved := fmt.Sprint(request.Config["base_url"])
		if resolved != p.expectedConfig {
			return protocol.RuntimePreflightResult{}, errors.New("fixture received unresolved configuration")
		}
		return protocol.RuntimePreflightResult{}, fmt.Errorf("fixture rejected resolved value %s", resolved)
	}
	return protocol.RuntimePreflightResult{}, errors.New("fixture runtime is incompatible")
}

func TestRuntimePreflightTargetsCollectsUniqueNestedJavaTargets(t *testing.T) {
	script := &dsl.Script{
		Protocol: "java_vuser",
		Init:     []dsl.Action{{Type: "java_vuser_run", Target: "Actions", Params: map[string]any{"method": "init"}}},
		Actions: []dsl.Action{
			{Type: "java_vuser_run", Target: "Actions", Params: map[string]any{"method": "action"}},
			{Type: "java_vuser_run", Target: "Actions", Params: map[string]any{"method": "action"}},
			{Type: "control_if", Control: &dsl.ControlFlow{
				Steps: []dsl.Action{{Type: "java_vuser_run", Target: "Nested"}},
			}},
		},
	}
	targets, err := runtimePreflightTargets(context.Background(), script, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 3 {
		t.Fatalf("targets = %#v, want three unique targets", targets)
	}
	if targets[0].ClassName != "Actions" || targets[0].Method != "init" {
		t.Fatalf("first target = %#v", targets[0])
	}
	if targets[2].ClassName != "Nested" || targets[2].Method != "full_lifecycle" {
		t.Fatalf("nested target = %#v", targets[2])
	}
}

func TestRuntimePreflightTargetsIgnoresOtherProtocols(t *testing.T) {
	script := &dsl.Script{Protocol: "http", Actions: []dsl.Action{{Type: "java_vuser_run", Target: "Actions"}}}
	if targets, err := runtimePreflightTargets(context.Background(), script, nil); err != nil || len(targets) != 0 {
		t.Fatalf("targets = %#v, want none", targets)
	}
}

func TestRuntimePreflightTargetsResolveOnlyStaticSecretAndEnvTemplates(t *testing.T) {
	t.Setenv(EnvScriptEnvAllowlist, "JAVA_ACTION_METHOD")
	t.Setenv("JAVA_ACTION_METHOD", "action")
	resolver := SecretResolverFunc(func(name string) (string, bool) {
		if name == "JAVA_ACTION_CLASS" {
			return "com.example.Actions", true
		}
		return "", false
	})
	script := &dsl.Script{
		Protocol: "java_vuser",
		Actions: []dsl.Action{
			{Type: "java_vuser_run", Target: "{{secret.JAVA_ACTION_CLASS}}", Params: map[string]any{"method": "{{env.JAVA_ACTION_METHOD}}"}},
			{Type: "java_vuser_run", Target: "com.example.Actions", Params: map[string]any{"method": "action"}},
		},
	}
	targets, err := runtimePreflightTargets(context.Background(), script, resolver)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].ClassName != "com.example.Actions" || targets[0].Method != "action" {
		t.Fatalf("targets = %#v, want one resolved unique target", targets)
	}
}

func TestRuntimePreflightTargetsRejectDynamicAndUnresolvedTemplatesWithoutValues(t *testing.T) {
	tests := []struct {
		name   string
		target string
		method string
		want   string
		avoid  string
	}{
		{name: "dynamic class", target: "{{data.users.class_name}}", method: "action", want: "dynamic template", avoid: "users.class_name"},
		{name: "dynamic method", target: "Actions", method: "{{var.phase}}", want: "dynamic template", avoid: "var.phase"},
		{name: "missing secret", target: "{{secret.SENSITIVE_CLASS_NAME}}", method: "action", want: "unresolved secret", avoid: "SENSITIVE_CLASS_NAME"},
		{name: "blocked env", target: "{{env.AZURE_CLIENT_SECRET}}", method: "action", want: "unresolved environment", avoid: "AZURE_CLIENT_SECRET"},
		{name: "malformed", target: "{{secret.BROKEN", method: "action", want: "invalid template", avoid: "BROKEN"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			script := &dsl.Script{Protocol: "java_vuser", Actions: []dsl.Action{{
				Type: "java_vuser_run", Target: test.target, Params: map[string]any{"method": test.method},
			}}}
			_, err := runtimePreflightTargets(context.Background(), script, SecretResolverFunc(func(string) (string, bool) {
				return "resolved-super-secret", false
			}))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want category %q", err, test.want)
			}
			for _, forbidden := range []string{test.avoid, "resolved-super-secret"} {
				if forbidden != "" && strings.Contains(err.Error(), forbidden) {
					t.Fatalf("error leaked %q: %v", forbidden, err)
				}
			}
		})
	}
}

func TestRuntimePreflightTargetsRejectsEmptyClass(t *testing.T) {
	script := &dsl.Script{Protocol: "java_vuser", Actions: []dsl.Action{{
		Type:   "java_vuser_run",
		Target: "",
		Params: map[string]any{"method": "action"},
	}}}
	_, err := runtimePreflightTargets(context.Background(), script, nil)
	if err == nil || !strings.Contains(err.Error(), "resolves to an empty value") {
		t.Fatalf("error = %v, want empty class category", err)
	}
}

func TestSafeRuntimeWarningCodeRejectsSecretBearingText(t *testing.T) {
	if got := safeRuntimeWarningCode("java_vuser_xbootclasspath_modern_fallback"); got != "java_vuser_xbootclasspath_modern_fallback" {
		t.Fatalf("safe warning = %q", got)
	}
	if got := safeRuntimeWarningCode("token=super-secret"); got != "runtime_preflight_warning" {
		t.Fatalf("unsafe warning = %q", got)
	}
}

func TestEngineRuntimePreflightFailsBeforeVUserInitialization(t *testing.T) {
	var initCalls atomic.Int32
	protocol.Register("runtime_preflight_rejection_test", func() protocol.Protocol {
		return &rejectingPreflightProtocol{initCalls: &initCalls}
	})
	engine, err := New(Config{
		Script:   &dsl.Script{Protocol: "runtime_preflight_rejection_test"},
		VUsers:   2,
		Duration: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "protocol runtime preflight") {
		t.Fatalf("Run error = %v", err)
	}
	if got := initCalls.Load(); got != 0 {
		t.Fatalf("protocol Init calls = %d, want zero", got)
	}
}

func TestEngineRuntimePreflightContainsAdapterPanicWithoutLeakingValue(t *testing.T) {
	var initCalls atomic.Int32
	const panicValue = "resolved-preflight-secret"
	protocol.Register("runtime_preflight_panic_test", func() protocol.Protocol {
		return &rejectingPreflightProtocol{initCalls: &initCalls, panicValue: panicValue}
	})
	engine, err := New(Config{
		Script:   &dsl.Script{Protocol: "runtime_preflight_panic_test"},
		VUsers:   1,
		Duration: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "runtime preflight provider panicked") {
		t.Fatalf("Run error = %v, want recovered preflight panic", err)
	} else if strings.Contains(err.Error(), panicValue) {
		t.Fatalf("Run error leaked panic value: %v", err)
	}
	if got := initCalls.Load(); got != 0 {
		t.Fatalf("protocol Init calls = %d, want zero", got)
	}
}

func TestMultiGroupRuntimePreflightFailsBeforeVUserInitialization(t *testing.T) {
	var initCalls atomic.Int32
	var preflightCalls atomic.Int32
	const resolvedSecret = "https://resolved-runtime-secret.example.test"
	protocol.Register("runtime_preflight_multigroup_rejection_test", func() protocol.Protocol {
		return &rejectingPreflightProtocol{
			initCalls:      &initCalls,
			preflightCalls: &preflightCalls,
			expectedConfig: resolvedSecret,
		}
	})
	script := &dsl.Script{
		Name:     "preflight",
		Protocol: "runtime_preflight_multigroup_rejection_test",
		Config:   dsl.ScriptConfig{BaseURL: "{{secret.RUNTIME_URL}}"},
		Actions:  []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
	}
	mge, err := NewMultiGroup(MultiGroupConfig{
		Duration: time.Second,
		Groups: []GroupConfig{{
			Name: "java-like", Script: script, VUsers: 3,
			SecretResolver: SecretResolverFunc(func(name string) (string, bool) {
				return resolvedSecret, name == "RUNTIME_URL"
			}),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mge.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "protocol runtime preflight") {
		t.Fatalf("Run error = %v", err)
	} else if strings.Contains(err.Error(), resolvedSecret) {
		t.Fatalf("Run error leaked resolved secret: %v", err)
	}
	if got := preflightCalls.Load(); got != 1 {
		t.Fatalf("preflight calls = %d, want one", got)
	}
	if got := initCalls.Load(); got != 0 {
		t.Fatalf("protocol Init calls = %d, want zero", got)
	}
	mge.mu.Lock()
	allocated := len(mge.vuserIDsInUse)
	nextID := mge.nextVUserID
	mge.mu.Unlock()
	if allocated != 0 || nextID != 0 {
		t.Fatalf("VUser allocation state = %d in use, next=%d; want untouched", allocated, nextID)
	}
	if err := mge.PreflightRuntime(context.Background()); err == nil {
		t.Fatal("cached failed preflight unexpectedly succeeded")
	}
	if got := preflightCalls.Load(); got != 1 {
		t.Fatalf("cached preflight calls = %d, want one", got)
	}
}

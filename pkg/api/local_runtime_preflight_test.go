package api

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

type localManagedPreflightRejectingProtocol struct {
	preflightCalls *atomic.Int32
	initCalls      *atomic.Int32
}

func (*localManagedPreflightRejectingProtocol) Name() string {
	return "local_managed_runtime_preflight_rejection_test"
}
func (p *localManagedPreflightRejectingProtocol) Init(context.Context, map[string]any) error {
	p.initCalls.Add(1)
	return nil
}
func (*localManagedPreflightRejectingProtocol) Execute(context.Context, *protocol.Action) (*protocol.Result, error) {
	return &protocol.Result{Success: true}, nil
}
func (*localManagedPreflightRejectingProtocol) Teardown(context.Context) error    { return nil }
func (*localManagedPreflightRejectingProtocol) HealthCheck(context.Context) error { return nil }
func (p *localManagedPreflightRejectingProtocol) PreflightRuntime(context.Context, protocol.RuntimePreflightRequest) (protocol.RuntimePreflightResult, error) {
	p.preflightCalls.Add(1)
	return protocol.RuntimePreflightResult{}, errors.New("fixture local runtime is incompatible")
}

func TestStartManagedTestRunPreflightsBeforeRunningTransition(t *testing.T) {
	var preflightCalls atomic.Int32
	var initCalls atomic.Int32
	protocol.Register("local_managed_runtime_preflight_rejection_test", func() protocol.Protocol {
		return &localManagedPreflightRejectingProtocol{preflightCalls: &preflightCalls, initCalls: &initCalls}
	})

	runner := NewTestRunner(2, NewHub(), nil)
	t.Cleanup(runner.beginShutdown)
	script := &dsl.Script{
		Name:     "preflight",
		Protocol: "local_managed_runtime_preflight_rejection_test",
		Actions:  []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
	}
	testRun := &ManagedTest{
		ID:          "local-runtime-preflight-reject",
		Name:        "Local runtime preflight reject",
		ProjectID:   storage.DefaultProjectID,
		Status:      "INITIALIZING",
		Duration:    time.Second,
		DurationStr: time.Second.String(),
		Groups:      []TestGroup{{Name: "default", ScriptName: script.Name, VUsers: 2}},
		TotalVUsers: 2,
		groupSpecs: []engine.GroupConfig{{
			Name: "default", Script: script, VUsers: 2,
		}},
	}
	runner.tests[testRun.ID] = testRun
	mge, err := runner.buildRunEngine(testRun, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, metricsDone, metricsStopped, started, err := runner.startManagedTestRun(testRun, mge, nil)
	if err == nil || !strings.Contains(err.Error(), "local runtime preflight") {
		t.Fatalf("startManagedTestRun error = %v", err)
	}
	if started || metricsDone != nil || metricsStopped != nil {
		t.Fatalf("start result = started %v metrics %v/%v; want no RUNNING side effects", started, metricsDone, metricsStopped)
	}
	if testRun.Status != "INITIALIZING" || testRun.StartedAt != nil || testRun.multiEngine != nil {
		t.Fatalf("managed state = status %q started %v engine %p; want INITIALIZING and unallocated", testRun.Status, testRun.StartedAt, testRun.multiEngine)
	}
	if testRun.executionCtx != nil || testRun.cancel != nil {
		t.Fatal("failed preflight retained an execution context")
	}
	if got := preflightCalls.Load(); got != 1 {
		t.Fatalf("preflight calls = %d, want one", got)
	}
	if got := initCalls.Load(); got != 0 {
		t.Fatalf("protocol Init calls = %d, want zero", got)
	}
}

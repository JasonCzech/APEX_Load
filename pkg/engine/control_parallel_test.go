package engine

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

const parallelTestProtocolName = "control-parallel-test"

type parallelTestProtocol struct{}

var parallelTeardownFails atomic.Bool

func (*parallelTestProtocol) Name() string                               { return parallelTestProtocolName }
func (*parallelTestProtocol) ParallelSafe() bool                         { return true }
func (*parallelTestProtocol) Init(context.Context, map[string]any) error { return nil }
func (*parallelTestProtocol) Teardown(context.Context) error {
	if parallelTeardownFails.Load() {
		return errors.New("injected teardown failure")
	}
	return nil
}
func (*parallelTestProtocol) HealthCheck(context.Context) error { return nil }
func (*parallelTestProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	if action.Name == "transport-error" {
		return nil, errors.New("injected branch failure")
	}
	return &protocol.Result{
		ActionName:  action.Name,
		StartTime:   time.Now(),
		Duration:    time.Millisecond,
		Success:     true,
		Extractions: map[string]string{"observed": action.Target},
	}, nil
}

func init() {
	protocol.Register(parallelTestProtocolName, func() protocol.Protocol { return &parallelTestProtocol{} })
}

func TestParallelBranchesMergeExtractionsInDeclarationNamespaces(t *testing.T) {
	vu := &VUser{
		ID: 3, Group: "parallel", Proto: &parallelTestProtocol{},
		Variables: map[string]string{"seed": "snapshot"},
		Metrics:   NewMetricsCollector(32),
		Script:    &dsl.Script{Protocol: parallelTestProtocolName, Config: dsl.ScriptConfig{}, Actions: []dsl.Action{{Name: "unused", Type: "GET", Target: "/"}}},
	}
	control := &dsl.ControlFlow{Concurrency: 2, Branches: []dsl.ParallelBranch{
		{Name: "catalog", Steps: []dsl.Action{{Name: "catalog-call", Type: "GET", Target: "/{{var.seed}}/catalog"}}},
		{Name: "cart", Steps: []dsl.Action{{Name: "cart-call", Type: "GET", Target: "/{{var.seed}}/cart"}}},
	}}
	if !vu.runParallelBranches(context.Background(), control, "action") {
		t.Fatal("parallel branches failed")
	}
	if got := vu.Variables["parallel.catalog.observed"]; got != "/snapshot/catalog" {
		t.Fatalf("catalog extraction = %q", got)
	}
	if got := vu.Variables["parallel.cart.observed"]; got != "/snapshot/cart" {
		t.Fatalf("cart extraction = %q", got)
	}
	if got := vu.Variables["observed"]; got != "" {
		t.Fatalf("branch extraction leaked into parent namespace: %q", got)
	}
}

func TestParallelRuntimeSafetyRejectsNestedParallel(t *testing.T) {
	branches := []dsl.ParallelBranch{{Name: "unsafe", Steps: []dsl.Action{{
		Name: "nested", Type: "control_parallel", Control: &dsl.ControlFlow{},
	}}}}
	if parallelActionsRuntimeSafe(branches) {
		t.Fatal("nested parallel block was accepted")
	}
}

func TestParallelBranchTeardownFailureFailsContainer(t *testing.T) {
	parallelTeardownFails.Store(true)
	t.Cleanup(func() { parallelTeardownFails.Store(false) })
	vu := &VUser{
		ID: 1, Group: "parallel", Proto: &parallelTestProtocol{}, Variables: map[string]string{},
		Metrics: NewMetricsCollector(8), Script: &dsl.Script{Protocol: parallelTestProtocolName},
	}
	control := &dsl.ControlFlow{Branches: []dsl.ParallelBranch{
		{Name: "one", Steps: []dsl.Action{{Name: "one", Type: "GET", Target: "/one"}}},
		{Name: "two", Steps: []dsl.Action{{Name: "two", Type: "GET", Target: "/two"}}},
	}}
	if vu.runParallelBranches(context.Background(), control, "action") {
		t.Fatal("parallel container succeeded after branch protocol teardown failed")
	}
}

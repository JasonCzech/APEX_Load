package engine

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func processorResourceScript() *dsl.Script {
	return &dsl.Script{Protocol: "eventhubs", Config: dsl.ScriptConfig{EventHubs: dsl.EventHubsConfig{ConsumerMode: "processor", ProcessorMaxPartitions: 2}}, Actions: []dsl.Action{{Type: "eventhubs_consume"}}}
}

func TestRuntimeResourcePlanPhaseAndPeakDemand(t *testing.T) {
	tests := []struct {
		name, strategy  string
		users           int
		init, main, end bool
		expected        int64
	}{
		{"runtime all phases", "at_runtime", 3, true, true, true, 3},
		{"runtime teardown only", "at_runtime", 3, false, false, true, 3},
		{"once all phases", "at_once", 3, true, true, true, 3},
		{"once lifecycle only", "at_once", 9, true, false, true, 1},
		{"once zero init", "at_once", 0, true, false, false, 1},
		{"once zero teardown", "at_once", 0, false, false, true, 1},
		{"zero runtime main", "at_runtime", 0, false, true, false, 0},
		{"producer only", "at_runtime", 99, false, false, false, 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			script := processorResourceScript()
			script.Init = nil
			script.Actions = nil
			script.Teardown = nil
			if test.init {
				script.Init = []dsl.Action{{Type: "eventhubs_consume"}}
			}
			if test.main {
				script.Actions = []dsl.Action{{Type: "eventhubs_consume"}}
			}
			if test.end {
				script.Teardown = []dsl.Action{{Type: "eventhubs_consume"}}
			}
			plan, err := NewRuntimeResourcePlan(script, test.strategy)
			if err != nil {
				t.Fatal(err)
			}
			if plan.RequiresReservation() != (test.init || test.main || test.end) {
				t.Fatal("consume presence mismatched")
			}
			demand, err := plan.Demand(test.users)
			if err != nil {
				t.Fatal(err)
			}
			if demand.Processors != test.expected || demand.Workers != 2*test.expected {
				t.Fatalf("demand=%+v want processors=%d", demand, test.expected)
			}
			if !demand.Valid() {
				t.Fatalf("invalid demand %+v", demand)
			}
		})
	}
}

func TestRuntimeResourcePlanOpenWorkersAndFiniteArrivals(t *testing.T) {
	tests := []struct {
		name                                 string
		maxFlight, iterations, global, users int
		tps                                  float64
		want                                 int64
	}{
		{"explicit in-flight", 3, 0, 0, 100, 20, 3},
		{"derived in-flight", 0, 0, 0, 1, 2.5, 180},
		{"finite per-user", 20, 2, 0, 3, 10, 6},
		{"global cap", 20, 2, 4, 3, 10, 4},
		{"global-only cap", 20, 0, 4, 3, 10, 4},
		{"zero population", 20, 2, 4, 0, 10, 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			script := processorResourceScript()
			script.Config.RunLogic.WorkloadModel = "open"
			script.Config.Pacing.Type = "tps"
			script.Config.Pacing.TPS = test.tps
			script.Config.RunLogic.MaxInFlight = test.maxFlight
			script.Config.RunLogic.Iterations = test.iterations
			script.Config.RunLogic.GlobalIterations = test.global
			plan, err := NewRuntimeResourcePlan(script, "")
			if err != nil {
				t.Fatal(err)
			}
			demand, err := plan.Demand(test.users)
			if err != nil {
				t.Fatal(err)
			}
			if demand.Processors != test.want {
				t.Fatalf("demand=%+v want %d", demand, test.want)
			}
		})
	}
}

func TestRuntimeResourcePlanBoundsAndNestedConsume(t *testing.T) {
	script := processorResourceScript()
	script.Config.EventHubs.ProcessorMaxPartitions = 0
	script.Actions = []dsl.Action{{Type: "control_if", Control: &dsl.ControlFlow{Else: []dsl.Action{{Type: "control_loop", Control: &dsl.ControlFlow{Steps: []dsl.Action{{Type: "eventhubs_consume"}}}}}}}}
	plan, err := NewRuntimeResourcePlan(script, "at_once")
	if err != nil {
		t.Fatal(err)
	}
	demand, err := plan.Demand(1)
	if err != nil || demand.Workers != 32 {
		t.Fatalf("default/nested demand=%+v err=%v", demand, err)
	}
	if _, err := plan.Demand(-1); err == nil {
		t.Fatal("accepted negative peak")
	}
	if int64(int(math.MaxInt64)) == math.MaxInt64 {
		if _, err := plan.Demand(int(math.MaxInt64)); err == nil {
			t.Fatal("accepted multiplication overflow")
		}
	}
	for _, ceiling := range []int{-1, 33} {
		script.Config.EventHubs.ProcessorMaxPartitions = ceiling
		if _, err := NewRuntimeResourcePlan(script, ""); err == nil {
			t.Fatalf("accepted ceiling %d", ceiling)
		}
	}
	script.Config.EventHubs.ProcessorMaxPartitions = 1
	if _, err := NewRuntimeResourcePlan(script, "invalid"); err == nil {
		t.Fatal("accepted invalid strategy")
	}
	script.Config.EventHubs.ConsumerMode = "{{var.mode}}"
	if _, err := NewRuntimeResourcePlan(script, ""); err == nil {
		t.Fatal("accepted unresolved resource mode")
	}
	for _, mode := range []string{"observer", "exclusive", ""} {
		script.Config.EventHubs.ConsumerMode = mode
		plan, err := NewRuntimeResourcePlan(script, "")
		if err != nil || plan.RequiresReservation() {
			t.Fatalf("mode %s incorrectly needs processor: %+v %v", mode, plan, err)
		}
	}
	script.Config.EventHubs.ConsumerMode = "processor"
	script.Actions = nil
	script.Init = []dsl.Action{{Type: "eventhubs_produce"}}
	script.Teardown = []dsl.Action{{Type: "eventhubs_produce"}}
	plan, err = NewRuntimeResourcePlan(script, "at_once")
	if err != nil || plan.RequiresReservation() {
		t.Fatalf("producer lifecycle needs reservation: %+v %v", plan, err)
	}
}

// A protocol boundary fixture lets tests observe trusted context transfer without
// an external broker, network connection, credential fetch, or background worker.
type resourceLifecycleFixture struct {
	name                    string
	lease                   *resourceReservationFixture
	inits, executes, closes *atomic.Int64
	failInit                bool
}

func (p *resourceLifecycleFixture) Name() string { return p.name }
func (p *resourceLifecycleFixture) Init(ctx context.Context, _ map[string]any) error {
	got, present := protocol.RuntimeResourceReservationFromContext(ctx, p.name)
	if !present || got != p.lease {
		return errors.New("runtime lease context missing")
	}
	p.inits.Add(1)
	if p.failInit {
		return errors.New("fixture init failed")
	}
	return nil
}
func (p *resourceLifecycleFixture) Execute(ctx context.Context, _ *protocol.Action) (*protocol.Result, error) {
	p.executes.Add(1)
	return &protocol.Result{Success: true, Duration: time.Microsecond}, ctx.Err()
}
func (p *resourceLifecycleFixture) Teardown(context.Context) error    { p.closes.Add(1); return nil }
func (p *resourceLifecycleFixture) HealthCheck(context.Context) error { return nil }

type resourceReservationFixture struct {
	mu                        sync.Mutex
	name                      string
	demand                    protocol.RuntimeResourceDemand
	adopted, closed           bool
	adopts, releases, resizes int
	resizeErr                 error
	strictCeiling             bool
	adoptedSignal             chan struct{}
}

func (r *resourceReservationFixture) ResourceProtocol() string { return r.name }
func (r *resourceReservationFixture) Demand() protocol.RuntimeResourceDemand {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.demand
}
func (r *resourceReservationFixture) Current() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.closed
}
func (r *resourceReservationFixture) Adopt(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.adopted {
		return protocol.ErrRuntimeResourceAlreadyAdopted
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.closed {
		return errors.New("closed")
	}
	r.adopted = true
	r.adopts++
	if r.adoptedSignal != nil {
		close(r.adoptedSignal)
	}
	return nil
}
func (r *resourceReservationFixture) Resize(_ context.Context, demand protocol.RuntimeResourceDemand) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.resizeErr != nil {
		return r.resizeErr
	}
	if r.strictCeiling && demand.MaxPartitionsPerProcessor != r.demand.MaxPartitionsPerProcessor {
		return errors.New("immutable partition ceiling mismatch")
	}
	r.demand = demand
	r.resizes++
	return nil
}
func (r *resourceReservationFixture) ReleaseUnadopted() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.adopted {
		return false
	}
	if !r.closed {
		r.closed = true
		r.releases++
	}
	return true
}

func (r *resourceReservationFixture) Release() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.closed {
		r.closed = true
		r.releases++
	}
}

func TestRuntimeResourceInheritedPreparationAndCancellation(t *testing.T) {
	script := processorResourceScript()
	plan, _ := NewRuntimeResourcePlan(script, "")
	demand, _ := plan.Demand(3)
	lease := &resourceReservationFixture{name: "eventhubs", demand: demand}
	resources, err := newEngineRuntimeResources([]GroupConfig{{Name: "group", Script: script, VUsers: 0, RuntimeResourcePeakVUsers: 3, RuntimeResourcePeakFrozen: true, RuntimeResourceReservation: lease}})
	if err != nil {
		t.Fatal(err)
	}
	if err := resources.reserve(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := resources.reserve(context.Background()); err != nil {
		t.Fatal(err)
	}
	if lease.adopts != 0 || lease.releases != 0 {
		t.Fatal("preparation executed or released lease")
	}
	if !resources.current() {
		t.Fatal("prepared lease unavailable")
	}
	if err := resources.resize(context.Background(), "group", 4); err == nil {
		t.Fatal("frozen peak exceeded")
	}
	resources.release()
	resources.release()
	if lease.releases != 1 || resources.current() {
		t.Fatal("preparation cleanup was not once-only")
	}
}

func TestRuntimeResourceZeroFrozenAndChangedScript(t *testing.T) {
	script := processorResourceScript()
	resources, err := newEngineRuntimeResources([]GroupConfig{{Name: "group", Script: script, VUsers: 0, RuntimeResourcePeakFrozen: true}})
	if err != nil {
		t.Fatal(err)
	}
	if err := resources.reserve(context.Background()); err != nil {
		t.Fatal(err)
	}
	bound := resources.bind(context.Background(), "group")
	lease, present := protocol.RuntimeResourceReservationFromContext(bound, "eventhubs")
	if !present || lease != nil {
		t.Fatal("zero binding permitted unmanaged fallback")
	}
	if err := resources.resize(context.Background(), "group", 1); err == nil {
		t.Fatal("frozen zero grew")
	}
	script.Actions = nil
	if err := resources.adopt(context.Background()); err == nil {
		t.Fatal("mutated plan was adopted")
	}
}

func TestRuntimeResourceAdoptionAndResizeFailure(t *testing.T) {
	script := processorResourceScript()
	plan, _ := NewRuntimeResourcePlan(script, "")
	demand, _ := plan.Demand(1)
	lease := &resourceReservationFixture{name: "eventhubs", demand: demand, resizeErr: errors.New("capacity unavailable")}
	resources, err := newEngineRuntimeResources([]GroupConfig{{Name: "group", Script: script, VUsers: 1, RuntimeResourceReservation: lease}})
	if err != nil {
		t.Fatal(err)
	}
	if err := resources.adopt(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := resources.resize(context.Background(), "group", 2); err == nil {
		t.Fatal("failed resize accepted")
	}
	if resources.demands()["group"] != demand {
		t.Fatal("failed resize changed admitted plan")
	}
	if err := resources.adopt(context.Background()); err == nil {
		t.Fatal("repeated execution adopted")
	}
	resources.release()
	if lease.adopts != 1 || lease.releases != 1 {
		t.Fatalf("lease lifecycle=%+v", lease)
	}
}

func TestRuntimeResourceRunContextIncludesAtOnceFreshTeardown(t *testing.T) {
	var inits, executes, closes atomic.Int64
	name := "resource_context_" + strings.ToLower(t.Name())
	lease := &resourceReservationFixture{name: name}
	protocol.Register(name, func() protocol.Protocol {
		return &resourceLifecycleFixture{name: name, lease: lease, inits: &inits, executes: &executes, closes: &closes}
	})
	script := &dsl.Script{Protocol: name, Init: []dsl.Action{{Type: "init"}}, Actions: []dsl.Action{{Type: "action"}}, Teardown: []dsl.Action{{Type: "teardown"}}}
	script.Config.RunLogic.Iterations = 1
	mge, err := NewMultiGroup(MultiGroupConfig{TerminationMode: dsl.TerminationIterations, Groups: []GroupConfig{{Name: "group", Script: script, VUsers: 1, InitStrategy: "at_once", RuntimeResourceReservation: lease}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := mge.ReserveRuntimeResources(context.Background()); err != nil {
		t.Fatal(err)
	}
	if inits.Load() != 0 || lease.adopts != 0 {
		t.Fatal("target activity during preparation")
	}
	if _, err := mge.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if inits.Load() != 3 || executes.Load() != 3 || closes.Load() != 3 {
		t.Fatalf("phase clients init=%d execute=%d close=%d", inits.Load(), executes.Load(), closes.Load())
	}
	if lease.adopts != 1 || lease.releases != 1 {
		t.Fatal("phase clients adopted/released shared parent more than once")
	}
}

func TestRuntimeResourceDirectRunContext(t *testing.T) {
	var inits, executes, closes atomic.Int64
	name := fmt.Sprintf("resource_direct_%s", strings.ToLower(t.Name()))
	lease := &resourceReservationFixture{name: name}
	protocol.Register(name, func() protocol.Protocol {
		return &resourceLifecycleFixture{name: name, lease: lease, inits: &inits, executes: &executes, closes: &closes}
	})
	script := &dsl.Script{Protocol: name, Actions: []dsl.Action{{Type: "action"}}}
	script.Config.RunLogic.Iterations = 1
	load, err := New(Config{Script: script, VUsers: 1, TerminationMode: dsl.TerminationIterations, RuntimeResourceReservation: lease})
	if err != nil {
		t.Fatal(err)
	}
	if err := load.ReserveRuntimeResources(context.Background()); err != nil {
		t.Fatal(err)
	}
	if inits.Load() != 0 || lease.adopts != 0 {
		t.Fatal("direct preparation contacted target")
	}
	if _, err := load.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if inits.Load() != 1 || executes.Load() != 1 || closes.Load() != 1 || lease.adopts != 1 || lease.releases != 1 {
		t.Fatalf("direct lifecycle init=%d execute=%d close=%d lease=%+v", inits.Load(), executes.Load(), closes.Load(), lease)
	}
}

func TestRuntimeResourceDuplicateEngineDoesNotReleaseOriginalOwner(t *testing.T) {
	script := processorResourceScript()
	plan, _ := NewRuntimeResourcePlan(script, "")
	demand, _ := plan.Demand(1)
	lease := &resourceReservationFixture{name: "eventhubs", demand: demand}
	group := GroupConfig{Name: "group", Script: script, VUsers: 1, RuntimeResourceReservation: lease}
	first, _ := newEngineRuntimeResources([]GroupConfig{group})
	second, _ := newEngineRuntimeResources([]GroupConfig{group})
	if err := first.adopt(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := second.adopt(context.Background()); !errors.Is(err, protocol.ErrRuntimeResourceAlreadyAdopted) {
		t.Fatalf("duplicate error=%v", err)
	}
	second.release()
	if !lease.Current() || lease.releases != 0 {
		t.Fatal("rejected duplicate released original owner")
	}
	first.release()
	if lease.releases != 1 {
		t.Fatal("original owner did not release")
	}
}

func TestRuntimeResourceFailedInitAndCancelledPreparationRelease(t *testing.T) {
	var inits, executes, closes atomic.Int64
	name := "resource_failed_init_" + strings.ToLower(t.Name())
	lease := &resourceReservationFixture{name: name}
	protocol.Register(name, func() protocol.Protocol {
		return &resourceLifecycleFixture{name: name, lease: lease, inits: &inits, executes: &executes, closes: &closes, failInit: true}
	})
	script := &dsl.Script{Protocol: name, Actions: []dsl.Action{{Type: "action"}}}
	script.Config.RunLogic.Iterations = 1
	load, err := New(Config{Script: script, VUsers: 1, TerminationMode: dsl.TerminationIterations, RuntimeResourceReservation: lease})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := load.Run(context.Background()); err == nil {
		t.Fatal("failed init reported success")
	}
	if inits.Load() != 1 || executes.Load() != 0 || closes.Load() != 1 || lease.releases != 1 {
		t.Fatalf("failed init lifecycle init=%d execute=%d close=%d release=%d", inits.Load(), executes.Load(), closes.Load(), lease.releases)
	}
	lease2 := &resourceReservationFixture{name: name}
	load2, err := New(Config{Script: script, VUsers: 1, TerminationMode: dsl.TerminationIterations, RuntimeResourceReservation: lease2})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := load2.ReserveRuntimeResources(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled preparation error=%v", err)
	}
	if lease2.releases != 1 || lease2.adopts != 0 {
		t.Fatal("cancelled preparation retained/consumed lease")
	}
}

func TestRuntimeResourceManualResizeReconcilesUnusedExtension(t *testing.T) {
	script := processorResourceScript()
	plan, _ := NewRuntimeResourcePlan(script, "")
	demand, _ := plan.Demand(1)
	lease := &resourceReservationFixture{name: "eventhubs", demand: demand}
	resources, err := newEngineRuntimeResources([]GroupConfig{{Name: "group", Script: script, VUsers: 1, RuntimeResourceReservation: lease}})
	if err != nil {
		t.Fatal(err)
	}
	if err := resources.adopt(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := resources.resize(context.Background(), "group", 3); err != nil {
		t.Fatal(err)
	}
	if err := resources.reconcileDesired(context.Background(), "group", 1, 2); err != nil {
		t.Fatal(err)
	}
	if lease.Demand().Processors != 2 {
		t.Fatal("retiring VUser entitlement was released")
	}
	if err := resources.reconcileDesired(context.Background(), "group", 1, 1); err != nil {
		t.Fatal(err)
	}
	if lease.Demand() != demand {
		t.Fatalf("unused extension retained: %+v", lease.Demand())
	}
	resources.release()
}

func TestRuntimeResourcePlannerAllControlBranchesAndStructuralLimits(t *testing.T) {
	branches := []*dsl.ControlFlow{
		{Steps: []dsl.Action{{Type: "eventhubs_consume"}}},
		{Else: []dsl.Action{{Type: "eventhubs_consume"}}},
		{Cases: []dsl.SwitchCase{{Steps: []dsl.Action{{Type: "eventhubs_consume"}}}}},
		{Default: []dsl.Action{{Type: "eventhubs_consume"}}},
		{Branches: []dsl.ParallelBranch{{Name: "consumer", Steps: []dsl.Action{{Type: "eventhubs_consume"}}}}},
	}
	for i, branch := range branches {
		script := processorResourceScript()
		script.Actions = []dsl.Action{{Type: "control", Control: branch}}
		plan, err := NewRuntimeResourcePlan(script, "")
		if err != nil || !plan.RequiresReservation() {
			t.Fatalf("branch%d omitted: %v", i, err)
		}
	}
	script := processorResourceScript()
	action := dsl.Action{Type: "eventhubs_consume"}
	for i := 0; i <= dsl.MaxScriptActionDepth; i++ {
		action = dsl.Action{Type: "control", Control: &dsl.ControlFlow{Steps: []dsl.Action{action}}}
	}
	script.Actions = []dsl.Action{action}
	if _, err := NewRuntimeResourcePlan(script, ""); err == nil {
		t.Fatal("unbounded walker accepted excessive depth")
	}
	for _, script := range []*dsl.Script{nil, {Protocol: "http"}} {
		plan, err := NewRuntimeResourcePlan(script, "")
		if err != nil || plan.RequiresReservation() {
			t.Fatalf("non-EH plan=%+v err=%v", plan, err)
		}
	}
}

func TestRuntimeResourceLaunchGateFailureReleasesStagedLease(t *testing.T) {
	for _, multi := range []bool{false, true} {
		t.Run(fmt.Sprint(multi), func(t *testing.T) {
			script := processorResourceScript()
			plan, _ := NewRuntimeResourcePlan(script, "")
			demand, _ := plan.Demand(1)
			lease := &resourceReservationFixture{name: "eventhubs", demand: demand}
			if multi {
				load, err := NewMultiGroup(MultiGroupConfig{Duration: time.Second, Groups: []GroupConfig{{Name: "group", Script: script, VUsers: 1, RuntimeResourceReservation: lease}}})
				if err != nil {
					t.Fatal(err)
				}
				if err := load.ReserveRuntimeResources(context.Background()); err != nil {
					t.Fatal(err)
				}
				script.Actions[0].Metadata = map[string]any{"execution_blocker": "import_review_required"}
				if _, err := load.Run(context.Background()); err == nil {
					t.Fatal("blocked group launched")
				}
			} else {
				load, err := New(Config{Duration: time.Second, Script: script, VUsers: 1, RuntimeResourceReservation: lease})
				if err != nil {
					t.Fatal(err)
				}
				if err := load.ReserveRuntimeResources(context.Background()); err != nil {
					t.Fatal(err)
				}
				script.Actions[0].Metadata = map[string]any{"execution_blocker": "import_review_required"}
				if _, err := load.Run(context.Background()); err == nil {
					t.Fatal("blocked engine launched")
				}
			}
			if lease.adopts != 0 || lease.releases != 1 {
				t.Fatalf("failed gate lifecycle adopts=%d releases=%d", lease.adopts, lease.releases)
			}
		})
	}
}

func TestRuntimeResourceDuplicateRunCannotAcquireCleanupOwnership(t *testing.T) {
	script := processorResourceScript()
	plan, _ := NewRuntimeResourcePlan(script, "")
	demand, _ := plan.Demand(1)
	lease := &resourceReservationFixture{name: "eventhubs", demand: demand}
	resources, err := newEngineRuntimeResources([]GroupConfig{{Name: "group", Script: script, VUsers: 1, RuntimeResourceReservation: lease}})
	if err != nil {
		t.Fatal(err)
	}
	if err := resources.beginRun(); err != nil {
		t.Fatal(err)
	}
	if err := resources.adopt(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := resources.beginRun(); err == nil {
		t.Fatal("duplicate Run installed cleanup ownership")
	}
	if !lease.Current() || lease.releases != 0 {
		t.Fatal("duplicate Run closed first execution")
	}
	resources.release()
}

func TestRuntimeResourceRejectedInheritedHandleCannotReleaseAdoptedOwner(t *testing.T) {
	for _, failure := range []string{"different demand", "cancelled preparation", "explicit cleanup", "launch gate"} {
		t.Run(failure, func(t *testing.T) {
			script := processorResourceScript()
			plan, _ := NewRuntimeResourcePlan(script, "")
			demand, _ := plan.Demand(1)
			lease := &resourceReservationFixture{name: "eventhubs", demand: demand}
			first, _ := newEngineRuntimeResources([]GroupConfig{{Name: "group", Script: script, VUsers: 1, RuntimeResourceReservation: lease}})
			if err := first.adopt(context.Background()); err != nil {
				t.Fatal(err)
			}
			secondScript := processorResourceScript()
			count := 1
			if failure == "different demand" {
				count = 2
			}
			second, err := NewMultiGroup(MultiGroupConfig{Duration: time.Second, Groups: []GroupConfig{{Name: "group", Script: secondScript, VUsers: count, RuntimeResourceReservation: lease}}})
			if err != nil {
				t.Fatal(err)
			}
			switch failure {
			case "different demand":
				if err := second.ReserveRuntimeResources(context.Background()); err == nil {
					t.Fatal("mismatched inherited demand accepted")
				}
			case "cancelled preparation":
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				if err := second.ReserveRuntimeResources(ctx); !errors.Is(err, context.Canceled) {
					t.Fatalf("cancel error=%v", err)
				}
			case "explicit cleanup":
				_ = second.ClosePreparedRuntime()
			case "launch gate":
				secondScript.Actions[0].Metadata = map[string]any{"execution_blocker": "import_review_required"}
				if _, err := second.Run(context.Background()); err == nil {
					t.Fatal("blocked second execution launched")
				}
			}
			_ = second.ClosePreparedRuntime()
			if !lease.Current() || lease.releases != 0 {
				t.Fatal("failed inherited owner released adopted first execution")
			}
			first.release()
			if lease.Current() || lease.releases != 1 {
				t.Fatal("original owner did not release")
			}
		})
	}
}

func TestRuntimeResourceProducerOnlyProcessorLeavesContextUnbound(t *testing.T) {
	script := processorResourceScript()
	script.Actions = []dsl.Action{{Type: "eventhubs_produce"}}
	for _, strategy := range []string{"at_runtime", "at_once"} {
		script.Init = []dsl.Action{{Type: "eventhubs_produce"}}
		script.Teardown = []dsl.Action{{Type: "eventhubs_produce"}}
		resources, err := newEngineRuntimeResources([]GroupConfig{{Name: "group", Script: script, VUsers: 5, InitStrategy: strategy}})
		if err != nil {
			t.Fatal(err)
		}
		if err := resources.reserve(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := resources.adopt(context.Background()); err != nil {
			t.Fatal(err)
		}
		// The real adapter rejects a present nil binding. This fixture uses the same
		// authoritative context rule without acquiring credentials or target traffic.
		client := resourceUnboundContextFixture{name: "eventhubs"}
		if err := client.Init(resources.bind(context.Background(), "group"), nil); err != nil {
			t.Fatal(err)
		}
		if len(resources.demands()) != 0 {
			t.Fatal("producer-only engine retained a processor binding")
		}
		resources.release()
	}
}

type resourceUnboundContextFixture struct{ name string }

func (p resourceUnboundContextFixture) Init(ctx context.Context, _ map[string]any) error {
	if _, present := protocol.RuntimeResourceReservationFromContext(ctx, p.name); present {
		return errors.New("producer-only runtime received invalid resource binding")
	}
	return nil
}

func TestRuntimeResourceZeroFloorReconciliationPreservesPartitionCeiling(t *testing.T) {
	script := processorResourceScript()
	resources, err := newEngineRuntimeResources([]GroupConfig{{Name: "group", Script: script, VUsers: 0}})
	if err != nil {
		t.Fatal(err)
	}
	if err := resources.adopt(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The admitted manual extension owns three clients. The provider is the
	// authority for actual draining children; the engine forwards only desired
	// demand and must not change their immutable per-processor ceiling.
	binding := resources.bindings["group"]
	demand, _ := binding.plan.Demand(3)
	lease := &resourceReservationFixture{name: "eventhubs", demand: demand, adopted: true, strictCeiling: true}
	binding.reservation, binding.cleanupOwned, binding.peak = lease, true, 3
	if err := resources.reconcileDesired(context.Background(), "group", 0, 1); err != nil {
		t.Fatal(err)
	}
	if lease.Demand().Processors != 1 {
		t.Fatal("retiring client desired entitlement released too early")
	}
	if err := resources.reconcileDesired(context.Background(), "group", 0, 0); err != nil {
		t.Fatal(err)
	}
	if got := lease.Demand(); !got.IsZero() || got.MaxPartitionsPerProcessor != 2 {
		t.Fatalf("zero reduction changed immutable ceiling: %+v", got)
	}
	if lease.resizes != 2 || binding.peak != 0 || binding.reservation != nil || lease.releases != 1 {
		t.Fatal("manual extension did not discard its empty parent")
	}
	resources.release()
}

func TestRuntimeResourceDuplicateRunCannotPublishOriginalReady(t *testing.T) {
	name := "resource_waiting_" + strings.ToLower(t.Name())
	lease := &resourceReservationFixture{name: name, adoptedSignal: make(chan struct{})}
	var inits, executes, closes atomic.Int64
	protocol.Register(name, func() protocol.Protocol {
		return &resourceLifecycleFixture{name: name, lease: lease, inits: &inits, executes: &executes, closes: &closes}
	})
	script := &dsl.Script{Protocol: name, Actions: []dsl.Action{{Type: "action"}}}
	load, err := NewMultiGroup(MultiGroupConfig{Duration: time.Second, PreBaseline: time.Hour, Groups: []GroupConfig{{Name: "group", Script: script, VUsers: 1, RuntimeResourceReservation: lease}}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := load.Run(ctx); done <- err }()
	select {
	case <-lease.adoptedSignal:
	case <-time.After(time.Second):
		t.Fatal("first execution did not adopt")
	}
	if _, err := load.Run(context.Background()); !errors.Is(err, errRuntimeResourceExecutionConsumed) {
		t.Fatalf("duplicate error=%v", err)
	}
	select {
	case <-load.Ready():
		t.Fatal("duplicate published first execution readiness during baseline")
	default:
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("first execution did not finish")
	}
	select {
	case <-load.Ready():
	default:
		t.Fatal("first terminal execution did not publish readiness")
	}
	if inits.Load() != 0 || executes.Load() != 0 {
		t.Fatal("baseline contacted target")
	}
}

func TestRuntimeResourceCancelledPreparationRunPublishesTerminalReady(t *testing.T) {
	script := processorResourceScript()
	load, err := NewMultiGroup(MultiGroupConfig{Duration: time.Second, Groups: []GroupConfig{{Name: "group", Script: script, VUsers: 0, AllowZeroVUsers: true}}})
	if err != nil {
		t.Fatal(err)
	}
	_ = load.ClosePreparedRuntime()
	if _, err := load.Run(context.Background()); err == nil {
		t.Fatal("closed preparation launched")
	}
	select {
	case <-load.Ready():
	default:
		t.Fatal("cancelled first execution left readiness blocked")
	}
}

type resourceProviderFixture struct {
	resourceLifecycleFixture
	leases *[]*resourceReservationFixture
}

func (p *resourceProviderFixture) RuntimeResourceCapacity() protocol.RuntimeResourceCapacity {
	return protocol.RuntimeResourceCapacity{Available: true, MaxProcessors: 4, AvailableProcessors: 4, MaxWorkers: 32, AvailableWorkers: 32, MaxPartitionsPerProcessor: 32}
}
func (p *resourceProviderFixture) ReserveRuntimeResources(_ context.Context, demand protocol.RuntimeResourceDemand) (protocol.RuntimeResourceReservation, error) {
	lease := &resourceReservationFixture{name: p.name, demand: demand, strictCeiling: true}
	*p.leases = append(*p.leases, lease)
	return lease, nil
}
func TestRuntimeResourceRepeatedManualZeroGrowthUsesFreshParents(t *testing.T) {
	name := "resource_zero_regrow_" + strings.ToLower(t.Name())
	var leases []*resourceReservationFixture
	var inits, executes, closes atomic.Int64
	protocol.Register(name, func() protocol.Protocol {
		return &resourceProviderFixture{resourceLifecycleFixture: resourceLifecycleFixture{name: name, inits: &inits, executes: &executes, closes: &closes}, leases: &leases}
	})
	resources := &engineRuntimeResources{adopted: true, bindings: map[string]*runtimeResourceBinding{"group": {plan: RuntimeResourcePlan{protocolName: name, mainConsume: true, partitions: 2}}}}
	for i := 0; i < 3; i++ {
		if err := resources.resize(context.Background(), "group", 3); err != nil {
			t.Fatal(err)
		}
		current := resources.bindings["group"].reservation
		if len(leases) != i+1 || current != leases[i] || leases[i].adopts != 1 || !leases[i].Current() {
			t.Fatal("manual growth reused a released parent")
		}
		if err := resources.reconcileDesired(context.Background(), "group", 0, 0); err != nil {
			t.Fatal(err)
		}
		if resources.bindings["group"].reservation != nil || !resources.current() || leases[i].Current() || leases[i].releases != 1 {
			t.Fatal("empty desired parent remains registered")
		}
		if got, present := protocol.RuntimeResourceReservationFromContext(resources.bind(context.Background(), "group"), name); !present || got != nil {
			t.Fatal("zero demand enabled unmanaged fallback")
		}
	}
	resources.release()
	if inits.Load() != 0 || executes.Load() != 0 {
		t.Fatal("reservation operation initialized protocol or contacted target")
	}
	for _, lease := range leases {
		if lease.releases != 1 {
			t.Fatal("empty parent released more than once")
		}
	}
}

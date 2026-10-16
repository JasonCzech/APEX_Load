package eventhubs

import (
	"context"
	"errors"
	"github.com/jasonczech/apexload/pkg/protocol"
	"math"
	"strings"
	"sync"
	"testing"
)

func testProcessorPool(t *testing.T, limits processorLimits) *processorPool {
	t.Helper()
	p, err := newProcessorPool(limits)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func reserveProcessorTest(t *testing.T, p *processorPool, n, ceiling int64) *ProcessorReservation {
	t.Helper()
	r, err := p.reserve(context.Background(), protocol.RuntimeResourceDemand{Processors: n, Workers: n * ceiling, MaxPartitionsPerProcessor: ceiling})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Release)
	return r
}
func TestProcessorLimitsOnlyLowerAtStartup(t *testing.T) {
	keys := []string{"APEXLOAD_EVENTHUBS_MAX_PROCESSORS", "APEXLOAD_EVENTHUBS_MAX_WORKERS", "APEXLOAD_EVENTHUBS_MAX_PARTITIONS_PER_PROCESSOR"}
	for _, k := range keys {
		t.Setenv(k, "")
	}
	d, err := processorLimitsFromEnvironment()
	if err != nil || d != (processorLimits{4, 32, 32}) {
		t.Fatal(d, err)
	}
	for i, k := range keys {
		t.Setenv(k, "1")
		d, err = processorLimitsFromEnvironment()
		if err != nil {
			t.Fatal(err)
		}
		if []int64{d.processors, d.workers, d.partitions}[i] != 1 {
			t.Fatal(d)
		}
		for _, value := range []string{"0", "-1", "33", "1.5", "secret"} {
			t.Setenv(k, value)
			if _, err = processorLimitsFromEnvironment(); err == nil {
				t.Fatalf("%s accepted %q", k, value)
			}
		}
		t.Setenv(k, "")
	}
	for _, bad := range []processorLimits{{0, 32, 32}, {5, 32, 32}, {4, 33, 32}, {4, 32, 33}} {
		if _, err := newProcessorPool(bad); err == nil {
			t.Fatal(bad)
		}
	}
}
func TestProcessorPoolAtomicallyReservesBothResourceAxes(t *testing.T) {
	p := testProcessorPool(t, processorLimits{4, 32, 32})
	for _, bad := range []protocol.RuntimeResourceDemand{demand(1, 32, 33), demand(math.MaxInt64, 1, 2), demand(1, 1, 2), demand(-1, 0, 0)} {
		if _, err := p.reserve(context.Background(), bad); err == nil {
			t.Fatal("invalid demand accepted", bad)
		}
	}
	if r, err := p.reserve(context.Background(), protocol.RuntimeResourceDemand{}); err != nil || r != nil || len(p.reservations) != 0 {
		t.Fatal("zero reservation retained", r, err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var accepted []*ProcessorReservation
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := p.reserve(context.Background(), demand(1, 8, 8))
			if err == nil {
				mu.Lock()
				accepted = append(accepted, r)
				mu.Unlock()
			} else if !errors.Is(err, ErrProcessorResourceCapacity) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if len(accepted) != 4 {
		t.Fatalf("accepted=%d", len(accepted))
	}
	c := p.snapshot()
	if !c.Valid() || c.ReservedProcessors != 4 || c.ReservedWorkers != 32 || c.AvailableWorkers != 0 {
		t.Fatal(c)
	}
	for _, r := range accepted {
		r.Release()
	}
	if c = p.snapshot(); c.ReservedProcessors != 0 || c.ReservedWorkers != 0 {
		t.Fatal(c)
	}
	p = testProcessorPool(t, processorLimits{4, 8, 32})
	r := reserveProcessorTest(t, p, 1, 8)
	if _, err := p.reserve(context.Background(), demand(1, 1, 1)); !errors.Is(err, ErrProcessorResourceCapacity) {
		t.Fatal("worker axis not enforced", err)
	}
	r.Release()
	p = testProcessorPool(t, processorLimits{1, 32, 32})
	reserveProcessorTest(t, p, 1, 1)
	if _, err := p.reserve(context.Background(), demand(1, 1, 1)); !errors.Is(err, ErrProcessorResourceCapacity) {
		t.Fatal("processor axis not enforced", err)
	}
}
func TestProcessorReservationOwnershipAndOperationContexts(t *testing.T) {
	p := testProcessorPool(t, processorLimits{4, 32, 32})
	ctx, cancel := context.WithCancel(context.Background())
	r, err := p.reserve(ctx, demand(1, 8, 8))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Release()
	cancel()
	if !r.Current() {
		t.Fatal("RPC cancellation ended reservation")
	}
	if _, err := r.acquireProcessor(context.Background()); err == nil {
		t.Fatal("staged reservation activated")
	}
	if err = r.Adopt(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err = r.Adopt(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = r.Adopt(ctx); !errors.Is(err, protocol.ErrRuntimeResourceAlreadyAdopted) {
		t.Fatal("duplicate owner cancellation obscured ownership", err)
	}
	child, err := r.acquireProcessor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.acquireProcessor(context.Background()); err == nil {
		t.Fatal("parent double spent")
	}
	r.Release()
	r.Release()
	if r.Current() {
		t.Fatal("released parent current")
	}
	if _, err = child.acquireWorker("0"); err == nil {
		t.Fatal("released parent started new worker")
	}
	if c := p.snapshot(); !c.Valid() || c.ReservedProcessors != 1 || c.ReservedWorkers != 8 || c.DrainingProcessors != 1 {
		t.Fatal(c)
	}
	child.release()
	if c := p.snapshot(); c.ReservedProcessors != 0 {
		t.Fatal(c)
	}
}
func TestProcessorResizeAndReleaseRetainLiveWorkers(t *testing.T) {
	p := testProcessorPool(t, processorLimits{4, 32, 32})
	r := reserveProcessorTest(t, p, 2, 8)
	if err := r.Adopt(context.Background()); err != nil {
		t.Fatal(err)
	}
	first, _ := r.acquireProcessor(context.Background())
	second, _ := r.acquireProcessor(context.Background())
	worker, err := first.acquireWorker("0")
	if err != nil {
		t.Fatal(err)
	}
	if c := p.snapshot(); c.ReservedWorkers != 16 || c.ActiveWorkers != 1 {
		t.Fatal("child double charged", c)
	}
	if err = r.Resize(context.Background(), demand(1, 8, 8)); err != nil {
		t.Fatal(err)
	}
	if c := p.snapshot(); c.ReservedProcessors != 2 || c.DrainingProcessors != 1 || c.AvailableWorkers != 16 {
		t.Fatal(c)
	}
	second.release()
	if c := p.snapshot(); c.ReservedProcessors != 1 {
		t.Fatal(c)
	}
	if err = r.Resize(context.Background(), demand(0, 0, 8)); err != nil {
		t.Fatal(err)
	}
	first.release()
	r.Release()
	if c := p.snapshot(); c.ReservedProcessors != 1 || c.DrainingWorkers != 8 {
		t.Fatal("live worker freed early", c)
	}
	worker.release()
	worker.release()
	if c := p.snapshot(); c.ReservedWorkers != 0 || c.ActiveWorkers != 0 {
		t.Fatal(c)
	}
	if err = r.Resize(context.Background(), demand(1, 8, 8)); err == nil {
		t.Fatal("released reservation resized")
	}
}
func TestProcessorResizeIsAtomicAndCeilingImmutable(t *testing.T) {
	p := testProcessorPool(t, processorLimits{4, 16, 32})
	r := reserveProcessorTest(t, p, 1, 8)
	other := reserveProcessorTest(t, p, 1, 8)
	if err := r.Resize(context.Background(), demand(2, 16, 8)); !errors.Is(err, ErrProcessorResourceCapacity) {
		t.Fatal(err)
	}
	if r.Demand().Processors != 1 {
		t.Fatal("failed resize mutated demand")
	}
	if err := r.Resize(context.Background(), demand(1, 4, 4)); err == nil {
		t.Fatal("ceiling changed")
	}
	other.Release()
	if err := r.Resize(context.Background(), demand(2, 16, 8)); err != nil {
		t.Fatal(err)
	}
}
func TestProcessorWorkersBoundPartitionIdentityAndQuarantine(t *testing.T) {
	p := testProcessorPool(t, processorLimits{4, 32, 32})
	r := reserveProcessorTest(t, p, 1, 2)
	_ = r.Adopt(context.Background())
	child, _ := r.acquireProcessor(context.Background())
	for _, id := range []string{"", string(make([]byte, 65))} {
		if _, err := child.acquireWorker(id); err == nil {
			t.Fatal("invalid partition accepted")
		}
	}
	a, _ := child.acquireWorker("0")
	if _, err := child.acquireWorker("0"); err == nil {
		t.Fatal("duplicate worker")
	}
	b, _ := child.acquireWorker("1")
	if _, err := child.acquireWorker("2"); err == nil {
		t.Fatal("ceiling exceeded")
	}
	a.release()
	replacement, _ := child.acquireWorker("0")
	a.release()
	if child.workers["0"] != replacement {
		t.Fatal("stale release erased replacement")
	}
	b.release()
	replacement.release()
	child.quarantine()
	child.release()
	r.Release()
	if c := p.snapshot(); !c.Valid() || c.ReservedProcessors != 1 || c.DrainingProcessors != 1 || c.ReservedWorkers != 2 {
		t.Fatal("unproven SDK close freed capacity", c)
	}
}
func TestProcessorModeParsingRejectsUnresolvedOrInvalidValues(t *testing.T) {
	for _, cfg := range []map[string]any{
		{"eventhubs_consumer_mode": 42},
		{"eventhubs_consumer_mode": "{{var.mode}}"},
		{"eventhubs_processor_max_partitions": nil},
		{"eventhubs_processor_max_partitions": 1.5},
		{"eventhubs_processor_max_partitions": 33},
		{"eventhubs_processor_max_partitions": -1},
		{"eventhubs_processor_max_partitions": 1},
	} {
		if err := (&EventHubs{}).Init(context.Background(), cfg); err == nil {
			t.Fatalf("invalid config accepted: %+v", cfg)
		}
	}
	for _, value := range []any{0, int64(0), float64(0), "0"} {
		e := &EventHubs{}
		if err := e.Init(context.Background(), map[string]any{"eventhubs_mock": true, "eventhubs_processor_max_partitions": value}); err != nil {
			t.Fatal(err)
		}
		if e.cfg.ProcessorMaxPartitions != 32 {
			t.Fatal(e.cfg.ProcessorMaxPartitions)
		}
		_ = e.Teardown(context.Background())
	}
}
func TestProcessorReservationCapabilityMatchesBuild(t *testing.T) {
	e := &EventHubs{}
	c := e.RuntimeResourceCapacity()
	if !c.Valid() {
		t.Fatal(c)
	}
	advertised := false
	for _, f := range e.RuntimeCapabilities().Features {
		advertised = advertised || f == FeatureProcessorReservationsV1
	}
	if c.Available != realEventHubsAvailable || advertised != c.Available {
		t.Fatal(c, advertised)
	}
	if !realEventHubsAvailable {
		if _, err := e.ReserveRuntimeResources(context.Background(), demand(1, 1, 1)); err == nil {
			t.Fatal("unsupported provider admitted")
		}
	}
}

func TestProcessorInheritedCleanupCannotReleaseAdoptedOwner(t *testing.T) {
	p := testProcessorPool(t, processorLimits{4, 32, 32})
	r := reserveProcessorTest(t, p, 1, 8)
	if !protocol.ReleaseUnadoptedRuntimeResourceReservation(r) || r.Current() {
		t.Fatal("staged cleanup failed")
	}
	r = reserveProcessorTest(t, p, 1, 8)
	if err := r.Adopt(context.Background()); err != nil {
		t.Fatal(err)
	}
	if protocol.ReleaseUnadoptedRuntimeResourceReservation(r) || !r.Current() {
		t.Fatal("inherited cleanup released adopted foreign owner")
	}
	// Both state transitions use the same pool lock; exactly one can win.
	for i := 0; i < 50; i++ {
		candidate := reserveProcessorTest(t, p, 1, 1)
		var wg sync.WaitGroup
		wg.Add(2)
		var adopted bool
		var released bool
		go func() { defer wg.Done(); adopted = candidate.Adopt(context.Background()) == nil }()
		go func() { defer wg.Done(); released = candidate.ReleaseUnadopted() }()
		wg.Wait()
		if adopted == released {
			t.Fatal("adopt and staged release were not exclusive", adopted, released)
		}
		if adopted && !candidate.Current() {
			t.Fatal("adopted owner released concurrently")
		}
		candidate.Release()
	}
}

func TestProcessorZeroResizeIsTerminalAndCannotRetainDormantRegistry(t *testing.T) {
	p := testProcessorPool(t, processorLimits{4, 32, 32})
	for i := 0; i < 100; i++ {
		r := reserveProcessorTest(t, p, 1, 2)
		if err := r.Resize(context.Background(), protocol.RuntimeResourceDemand{MaxPartitionsPerProcessor: 2}); err != nil {
			t.Fatal(err)
		}
		if r.Current() || len(p.reservations) != 0 {
			t.Fatal("zero target retained dormant authority")
		}
		if err := r.Adopt(context.Background()); err == nil {
			t.Fatal("zero parent reactivated")
		}
		if err := r.Resize(context.Background(), demand(1, 2, 2)); err == nil {
			t.Fatal("zero parent regrew")
		}
	}
	r := reserveProcessorTest(t, p, 1, 2)
	_ = r.Adopt(context.Background())
	child, _ := r.acquireProcessor(context.Background())
	worker, _ := child.acquireWorker("0")
	if err := r.Resize(context.Background(), protocol.RuntimeResourceDemand{MaxPartitionsPerProcessor: 2}); err != nil {
		t.Fatal(err)
	}
	child.release()
	if r.Current() {
		t.Fatal("draining zero parent remains executable")
	}
	if c := p.snapshot(); c.ReservedProcessors != 1 || c.DrainingProcessors != 1 || len(p.reservations) != 1 {
		t.Fatal("live child released prematurely", c)
	}
	worker.release()
	if c := p.snapshot(); c.ReservedWorkers != 0 || len(p.reservations) != 0 {
		t.Fatal("drained zero parent retained", c)
	}
}

func TestProcessorActivationReportsRetainedDrainingEntitlement(t *testing.T) {
	p := testProcessorPool(t, processorLimits{4, 32, 32})
	r := reserveProcessorTest(t, p, 1, 2)
	_ = r.Adopt(context.Background())
	child, _ := r.acquireProcessor(context.Background())
	worker, _ := child.acquireWorker("0")
	child.release()
	if _, err := r.acquireProcessor(context.Background()); err == nil || !strings.Contains(err.Error(), "current=true") || !strings.Contains(err.Error(), "active=1") || !strings.Contains(err.Error(), "draining=1") {
		t.Fatal("activation silently hid draining entitlement", err)
	}
	worker.release()
	replacement, err := r.acquireProcessor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	replacement.release()
}

func demand(processors, workers, maxPartitionsPerProcessor int64) protocol.RuntimeResourceDemand {
	return protocol.RuntimeResourceDemand{Processors: processors, Workers: workers, MaxPartitionsPerProcessor: maxPartitionsPerProcessor}
}

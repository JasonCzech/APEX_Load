package protocol

import (
	"context"
	"math"
	"strings"
	"testing"
)

func TestRuntimeResourceDemandBounds(t *testing.T) {
	for _, tt := range []struct {
		name  string
		d     RuntimeResourceDemand
		valid bool
	}{
		{"zero", RuntimeResourceDemand{}, true},
		{"zero with ceiling", RuntimeResourceDemand{MaxPartitionsPerProcessor: 32}, true},
		{"peak", RuntimeResourceDemand{4, 32, 8}, true},
		{"negative", RuntimeResourceDemand{-1, 0, 0}, false},
		{"workers without processor", RuntimeResourceDemand{0, 1, 32}, false},
		{"zero ceiling", RuntimeResourceDemand{1, 0, 0}, false},
		{"mismatched workers", RuntimeResourceDemand{2, 2, 2}, false},
		{"overflow", RuntimeResourceDemand{math.MaxInt64, 1, 2}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.d.Valid(); got != tt.valid {
				t.Fatalf("Valid=%v", got)
			}
		})
	}
}
func TestRuntimeResourceCapacityUsesTotalChargeOnce(t *testing.T) {
	c := RuntimeResourceCapacity{Available: true, MaxProcessors: 4, ReservedProcessors: 2, AvailableProcessors: 2, ActiveProcessors: 2, DrainingProcessors: 1, MaxWorkers: 32, ReservedWorkers: 16, AvailableWorkers: 16, ActiveWorkers: 4, DrainingWorkers: 8, MaxPartitionsPerProcessor: 8}
	if !c.Valid() {
		t.Fatal("valid charged subsets rejected")
	}
	for _, bad := range []RuntimeResourceCapacity{
		{Available: true},
		{AvailableProcessors: 1},
		{MaxProcessors: 1, ReservedProcessors: 2},
		{MaxWorkers: 2, ReservedWorkers: 1, ActiveWorkers: 2},
	} {
		if bad.Valid() {
			t.Fatalf("invalid capacity accepted: %+v", bad)
		}
	}
	c.AvailableWorkers = 32 - c.ReservedWorkers - c.DrainingWorkers
	if c.Valid() {
		t.Fatal("draining subtracted twice")
	}
}

type resourceReservationFixture struct{ d RuntimeResourceDemand }

func (*resourceReservationFixture) ResourceProtocol() string        { return "resources_test" }
func (r *resourceReservationFixture) Demand() RuntimeResourceDemand { return r.d }
func (*resourceReservationFixture) Current() bool                   { return true }
func (*resourceReservationFixture) Adopt(context.Context) error     { return nil }
func (r *resourceReservationFixture) Resize(_ context.Context, d RuntimeResourceDemand) error {
	r.d = d
	return nil
}
func (*resourceReservationFixture) Release() {}

type resourceProviderFixture struct {
	panickingRuntimeExtensionProtocol
	calls                       int
	panicOnRead, panicOnReserve bool
	capacity                    RuntimeResourceCapacity
}

func (*resourceProviderFixture) Name() string { return "resources_test" }
func (p *resourceProviderFixture) RuntimeResourceCapacity() RuntimeResourceCapacity {
	if p.panicOnRead {
		panic("secret")
	}
	return p.capacity
}
func (p *resourceProviderFixture) ReserveRuntimeResources(_ context.Context, d RuntimeResourceDemand) (RuntimeResourceReservation, error) {
	p.calls++
	if p.panicOnReserve {
		panic("secret")
	}
	return &resourceReservationFixture{d}, nil
}
func TestRuntimeResourceProviderIsOptionalAndContainsPanics(t *testing.T) {
	plain := &panickingRuntimeExtensionProtocol{}
	if _, supported, err := ReadRuntimeResourceCapacity(plain); supported || err != nil {
		t.Fatal(supported, err)
	}
	if _, err := ReserveRuntimeResources(context.Background(), plain, RuntimeResourceDemand{1, 1, 1}); err == nil {
		t.Fatal("unsupported reserve accepted")
	}
	p := &resourceProviderFixture{}
	if _, err := ReserveRuntimeResources(context.Background(), p, RuntimeResourceDemand{}); err != nil || p.calls != 0 {
		t.Fatal("zero demand called provider", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ReserveRuntimeResources(cancelled, p, RuntimeResourceDemand{1, 1, 1}); err == nil || p.calls != 0 {
		t.Fatal("cancelled admission called provider")
	}
	p.panicOnReserve = true
	if _, err := ReserveRuntimeResources(context.Background(), p, RuntimeResourceDemand{1, 1, 1}); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal(err)
	}
	p.panicOnRead = true
	if _, supported, err := ReadRuntimeResourceCapacity(p); !supported || err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal(supported, err)
	}
	p.panicOnRead = false
	p.capacity = RuntimeResourceCapacity{Available: true}
	if _, _, err := ReadRuntimeResourceCapacity(p); err == nil {
		t.Fatal("invalid provider capacity accepted")
	}
	p.panicOnReserve = false
	r, err := ReserveRuntimeResources(nil, p, RuntimeResourceDemand{1, 1, 1})
	if err != nil || r == nil {
		t.Fatal(r, err)
	}
}
func TestRuntimeResourceContextPreservesInvalidBindings(t *testing.T) {
	r := &resourceReservationFixture{RuntimeResourceDemand{1, 1, 1}}
	ctx := WithRuntimeResourceReservation(nil, " Resources_Test ", r)
	for _, c := range []context.Context{ctx, context.WithoutCancel(ctx)} {
		if got, present := RuntimeResourceReservationFromContext(c, "resources_test"); !present || got != r {
			t.Fatal("trusted binding lost")
		}
	}
	if got, present := RuntimeResourceReservationFromContext(ctx, "other"); !present || got != nil {
		t.Fatal("mismatched binding became absent")
	}
	ctx = WithRuntimeResourceReservation(ctx, "resources_test", nil)
	if got, present := RuntimeResourceReservationFromContext(ctx, "resources_test"); !present || got != nil {
		t.Fatal("nil binding became absent")
	}
	if _, present := RuntimeResourceReservationFromContext(nil, "resources_test"); present {
		t.Fatal("nil context contains binding")
	}
}

func TestUnknownStagedReleaseNeverFallsBackToRelease(t *testing.T) {
	if ReleaseUnadoptedRuntimeResourceReservation(&resourceReservationFixture{}) {
		t.Fatal("unsupported staged releaser accepted")
	}
	if ReleaseUnadoptedRuntimeResourceReservation(nil) {
		t.Fatal("nil staged releaser accepted")
	}
}

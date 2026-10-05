package protocol

import (
	"context"
	"errors"
	"math"
	"strings"
)

// ErrRuntimeResourceAlreadyAdopted lets a rejected duplicate owner forget a
// foreign handle without releasing the current owner's reservation.
var ErrRuntimeResourceAlreadyAdopted = errors.New("runtime resource reservation already adopted")

// RuntimeResourceDemand is local process admission, independent of VUser count.
// Each processor can own every declared partition; its worker entitlement is
// Processors*MaxPartitionsPerProcessor. The ceiling is not additive across runs.
type RuntimeResourceDemand struct {
	Processors                int64 `json:"processors"`
	Workers                   int64 `json:"workers"`
	MaxPartitionsPerProcessor int64 `json:"max_partitions_per_processor"`
}

func (d RuntimeResourceDemand) Valid() bool {
	if d.Processors < 0 || d.Workers < 0 || d.MaxPartitionsPerProcessor < 0 {
		return false
	}
	if d.Processors == 0 {
		return d.Workers == 0
	}
	return d.MaxPartitionsPerProcessor > 0 && d.Processors <= math.MaxInt64/d.MaxPartitionsPerProcessor && d.Workers == d.Processors*d.MaxPartitionsPerProcessor
}

func (d RuntimeResourceDemand) IsZero() bool { return d.Processors == 0 && d.Workers == 0 }

// Reserved is the total charged entitlement, including staged, active and
// draining work. Active/Draining are informative subsets, not extra deductions.
type RuntimeResourceCapacity struct {
	Available                 bool  `json:"available"`
	MaxProcessors             int64 `json:"max_processors"`
	AvailableProcessors       int64 `json:"available_processors"`
	ReservedProcessors        int64 `json:"reserved_processors"`
	ActiveProcessors          int64 `json:"active_processors"`
	DrainingProcessors        int64 `json:"draining_processors"`
	MaxWorkers                int64 `json:"max_workers"`
	AvailableWorkers          int64 `json:"available_workers"`
	ReservedWorkers           int64 `json:"reserved_workers"`
	ActiveWorkers             int64 `json:"active_workers"`
	DrainingWorkers           int64 `json:"draining_workers"`
	MaxPartitionsPerProcessor int64 `json:"max_partitions_per_processor"`
}

func (c RuntimeResourceCapacity) Valid() bool {
	if c.MaxProcessors < 0 || c.MaxWorkers < 0 || c.MaxPartitionsPerProcessor < 0 || c.ReservedProcessors < 0 || c.ReservedWorkers < 0 || c.ReservedProcessors > c.MaxProcessors || c.ReservedWorkers > c.MaxWorkers {
		return false
	}
	if c.Available && (c.MaxProcessors == 0 || c.MaxWorkers == 0 || c.MaxPartitionsPerProcessor == 0) {
		return false
	}
	if c.ActiveProcessors < 0 || c.DrainingProcessors < 0 || c.ActiveWorkers < 0 || c.DrainingWorkers < 0 || c.ActiveProcessors > c.ReservedProcessors || c.DrainingProcessors > c.ReservedProcessors || c.ActiveWorkers > c.ReservedWorkers || c.DrainingWorkers > c.ReservedWorkers {
		return false
	}
	if !c.Available {
		return c.AvailableProcessors == 0 && c.AvailableWorkers == 0
	}
	return c.AvailableProcessors == c.MaxProcessors-c.ReservedProcessors && c.AvailableWorkers == c.MaxWorkers-c.ReservedWorkers
}

// RuntimeResourceProvider inspects and reserves only local state. It must not
// initialize a protocol, contact its target, fetch credentials or execute code.
type RuntimeResourceProvider interface {
	RuntimeResourceCapacity() RuntimeResourceCapacity
	ReserveRuntimeResources(context.Context, RuntimeResourceDemand) (RuntimeResourceReservation, error)
}

// RuntimeResourceReservation is an opaque process-issued capability. Adopt is
// single-use at execution commit. Reductions and Release retain live children
// until their actual teardown completes; callers never serialize this handle.
type RuntimeResourceReservation interface {
	ResourceProtocol() string
	Demand() RuntimeResourceDemand
	Current() bool
	Adopt(context.Context) error
	Resize(context.Context, RuntimeResourceDemand) error
	Release()
}

// RuntimeResourceStagedReleaser is an optional atomic cleanup boundary for a
// rejected inherited handle. It may close only a never-adopted reservation.
type RuntimeResourceStagedReleaser interface{ ReleaseUnadopted() bool }

func ReleaseUnadoptedRuntimeResourceReservation(reservation RuntimeResourceReservation) (released bool) {
	staged, supported := reservation.(RuntimeResourceStagedReleaser)
	if !supported {
		return false
	}
	defer func() {
		if recover() != nil {
			released = false
		}
	}()
	return staged.ReleaseUnadopted()
}

func ReadRuntimeResourceCapacity(instance Protocol) (capacity RuntimeResourceCapacity, supported bool, err error) {
	provider, supported := instance.(RuntimeResourceProvider)
	if !supported {
		return RuntimeResourceCapacity{}, false, nil
	}
	defer func() {
		if recover() != nil {
			capacity = RuntimeResourceCapacity{}
			err = errors.New("runtime resource provider panicked")
		}
	}()
	capacity = provider.RuntimeResourceCapacity()
	if !capacity.Valid() {
		return RuntimeResourceCapacity{}, true, errors.New("invalid runtime resource capacity")
	}
	return capacity, true, nil
}

func ReserveRuntimeResources(ctx context.Context, instance Protocol, demand RuntimeResourceDemand) (reservation RuntimeResourceReservation, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !demand.Valid() {
		return nil, errors.New("invalid runtime resource demand")
	}
	if demand.IsZero() {
		return nil, nil
	}
	provider, supported := instance.(RuntimeResourceProvider)
	if !supported {
		return nil, errors.New("protocol does not support runtime resource reservation")
	}
	defer func() {
		if recover() != nil {
			reservation = nil
			err = errors.New("runtime resource reservation provider panicked")
		}
	}()
	return provider.ReserveRuntimeResources(ctx, demand)
}

type runtimeResourceContextKey struct{}
type runtimeResourceBinding struct {
	protocol    string
	reservation RuntimeResourceReservation
}

// The private key and boxed value preserve a present nil/mismatched binding as
// invalid. An adapter must never turn that into a fresh unmanaged reservation.
func WithRuntimeResourceReservation(ctx context.Context, name string, reservation RuntimeResourceReservation) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, runtimeResourceContextKey{}, runtimeResourceBinding{protocol: strings.ToLower(strings.TrimSpace(name)), reservation: reservation})
}

func RuntimeResourceReservationFromContext(ctx context.Context, name string) (RuntimeResourceReservation, bool) {
	if ctx == nil {
		return nil, false
	}
	binding, present := ctx.Value(runtimeResourceContextKey{}).(runtimeResourceBinding)
	if !present {
		return nil, false
	}
	if binding.protocol != strings.ToLower(strings.TrimSpace(name)) {
		return nil, true
	}
	return binding.reservation, true
}

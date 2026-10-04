package duckdb

import (
	"context"
	"errors"
	"strconv"
	"time"
)

var ErrResourceConfiguration = errors.New("invalid native analytics resource configuration")

// NativeResourceProfile is a bounded immutable administrator-requested memory
// and spill snapshot. It cannot be chosen by a mapping, dataset or child frame.
// Zero preserves the2GiB/8GiB production defaults. Actual admission still checks
// headroom and the worker uses the exact same limits; no admission bypass exists.
type NativeResourceProfile struct {
	version       uint8
	memory, spill int64
}
type NativeResourceLimits struct {
	MemoryBytes int64
	SpillBytes  int64
}

func workerParseBudget(text string, maximum int64) (int64, error) {
	scales := []struct {
		suffix string
		scale  int64
	}{{"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}, {"B", 1}}
	for _, unit := range scales {
		if len(text) > len(unit.suffix) && text[len(text)-len(unit.suffix):] == unit.suffix {
			value, err := strconv.ParseInt(text[:len(text)-len(unit.suffix)], 10, 64)
			if err != nil || value < 1 || value > maximum/unit.scale {
				return 0, errWorkerResourceLimit
			}
			return value * unit.scale, nil
		}
	}
	return 0, errWorkerResourceLimit
}

func ApproveNativeResourceProfile(memoryLimit, spillLimit string) (NativeResourceProfile, error) {
	if len(memoryLimit) > 32 || len(spillLimit) > 32 {
		return NativeResourceProfile{}, ErrResourceConfiguration
	}
	if memoryLimit == "" {
		memoryLimit = "2GiB"
	}
	if spillLimit == "" {
		spillLimit = "8GiB"
	}
	memory, err := workerParseBudget(memoryLimit, 2<<30)
	if err != nil {
		return NativeResourceProfile{}, ErrResourceConfiguration
	}
	spill, err := workerParseBudget(spillLimit, 8<<30)
	if err != nil {
		return NativeResourceProfile{}, ErrResourceConfiguration
	}
	return NativeResourceProfile{version: 1, memory: memory, spill: spill}, nil
}
func (profile NativeResourceProfile) Limits() (NativeResourceLimits, error) {
	if profile == (NativeResourceProfile{}) {
		profile = NativeResourceProfile{version: 1, memory: 2 << 30, spill: 8 << 30}
	}
	if profile.version != 1 || profile.memory < 1 || profile.memory > 2<<30 || profile.spill < 1 || profile.spill > 8<<30 {
		return NativeResourceLimits{}, ErrResourceConfiguration
	}
	return NativeResourceLimits{MemoryBytes: profile.memory, SpillBytes: profile.spill}, nil
}

// NewForNativePipeline must be used for the controller's borrowed-parent work.
// The constructor and subsequent pre-hash check pin the exact same parent and
// immutable resource limits; ambient changes affect only later reservations.
func NewForNativePipeline(ctx context.Context, root string) (*Engine, error) {
	return newForNativePipeline(ctx, root, New)
}
func newForNativePipeline(ctx context.Context, root string, constructor func(string) (*Engine, error)) (*Engine, error) {
	if constructor == nil {
		return nil, errWorkerOwnership
	}
	if err := workerContextError(ctx); err != nil {
		return nil, err
	}
	p := workerPipelineForContext(ctx)
	if p == nil {
		return nil, errWorkerOwnership
	}
	p.mu.Lock()
	if p.released || p.writersSealed || workerContextError(p.ctx) != nil {
		p.mu.Unlock()
		return nil, errWorkerOwnership
	}
	limits := p.limits
	p.mu.Unlock()
	if limits.validate() != nil {
		return nil, errWorkerOwnership
	}
	lease, err := p.AcquireIO()
	if err != nil {
		return nil, err
	}
	defer lease.Joined()
	engine, err := constructor(root)
	if err != nil {
		return nil, err
	}
	if err = workerContextError(ctx); err != nil {
		return nil, err
	}
	if engine == nil {
		return nil, errWorkerOwnership
	}
	engine.MemoryLimit = strconv.FormatInt(limits.MemoryBytes, 10) + "B"
	engine.SpillLimit = strconv.FormatInt(limits.SpillBytes, 10) + "B"
	engine.Timeout = time.Duration(limits.RuntimeMS) * time.Millisecond
	engine.borrowedParent = p
	if err = p.admitEngineResourceLimits(engine, limits.MemoryBytes, limits.SpillBytes); err != nil {
		return nil, err
	}
	if err = workerContextError(ctx); err != nil {
		return nil, err
	}
	return engine, nil
}
func (p *NativePipeline) admitEngineResourceLimits(engine *Engine, memory, spill int64) error {
	if p == nil || engine == nil {
		return errWorkerOwnership
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.released || p.writersSealed || workerContextError(p.ctx) != nil || engine.borrowedParent != p {
		return errWorkerOwnership
	}
	if memory != p.limits.MemoryBytes || spill != p.limits.SpillBytes || engine.Timeout != time.Duration(p.limits.RuntimeMS)*time.Millisecond {
		return errWorkerResourceLimit
	}
	return nil
}

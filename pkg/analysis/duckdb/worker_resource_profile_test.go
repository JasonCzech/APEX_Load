package duckdb

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNativeResourceProfileBoundsAndProductionDefaults(t *testing.T) {
	for _, tc := range []struct {
		name, memory, spill   string
		wantMemory, wantSpill int64
		bad                   bool
	}{
		{name: "defaults", wantMemory: 2 << 30, wantSpill: 8 << 30},
		{name: "explicit-smaller", memory: "256MiB", spill: "128MiB", wantMemory: 256 << 20, wantSpill: 128 << 20},
		{name: "minimum", memory: "1B", spill: "1B", wantMemory: 1, wantSpill: 1},
		{name: "zero", memory: "0B", bad: true}, {name: "negative", spill: "-1B", bad: true}, {name: "expanded-memory", memory: "2147483649B", bad: true}, {name: "expanded-spill", spill: "8589934593B", bad: true},
		{name: "implicit-unit", memory: "128", bad: true}, {name: "unsupported-unit", memory: "128MB", bad: true}, {name: "overflow", memory: "99999999999999999999999GiB", bad: true}, {name: "oversized", memory: strings.Repeat("1", 4<<20), bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profile, err := ApproveNativeResourceProfile(tc.memory, tc.spill)
			if tc.bad {
				if !errors.Is(err, ErrResourceConfiguration) {
					t.Fatal("invalid configuration accepted", err)
				}
				return
			}
			limits, err := profile.Limits()
			if err != nil || limits.MemoryBytes != tc.wantMemory || limits.SpillBytes != tc.wantSpill {
				t.Fatal("profile changed", limits, err)
			}
		})
	}
	limits, err := (NativeResourceProfile{}).Limits()
	if err != nil || limits.MemoryBytes != 2<<30 || limits.SpillBytes != 8<<30 {
		t.Fatal("zero profile changed defaults", limits, err)
	}
	if _, err := (NativeResourceProfile{version: 2, memory: 1, spill: 1}).Limits(); !errors.Is(err, ErrResourceConfiguration) {
		t.Fatal("unknown profile version admitted")
	}
}
func nativeResourceProfileTestPipeline(t *testing.T) (context.Context, *NativePipeline) {
	t.Helper()
	p, _ := workerPipelineTestFixture(t)
	p.limits = workerLimits{MemoryBytes: 256 << 20, SpillBytes: 128 << 20, RuntimeMS: 1000, Threads: 2, InputBytes: 12, OutputBytes: 12}
	return context.WithValue(p.ctx, nativePipelineKey{}, p), p
}
func TestNativeBorrowedConstructorPinsExactApprovedResourcesAndUnsealedParent(t *testing.T) {
	ctx, p := nativeResourceProfileTestPipeline(t)
	defer p.Release()
	engine, err := newForNativePipeline(ctx, "unused", func(string) (*Engine, error) { return &Engine{}, nil })
	if err != nil || engine.MemoryLimit != "268435456B" || engine.SpillLimit != "134217728B" || engine.Timeout != time.Second || engine.borrowedParent != p {
		t.Fatal("constructor did not pin parent profile", engine, err)
	}
	p.mu.Lock()
	joined := len(p.parentIO) == 0
	p.mu.Unlock()
	if !joined {
		t.Fatal("completed constructor did not join IO")
	}
	other, _ := workerPipelineTestFixture(t)
	defer other.Release()
	other.limits = p.limits
	if err := other.admitEngineResourceLimits(engine, 256<<20, 128<<20); !errors.Is(err, errWorkerOwnership) {
		t.Fatal("equal limits substituted a foreign parent", err)
	}
	if err := p.SealWriters(); err != nil {
		t.Fatal(err)
	}
	if e, err := newForNativePipeline(ctx, "unused", func(string) (*Engine, error) { t.Fatal("sealed parent started constructor"); return nil, nil }); e != nil || !errors.Is(err, errWorkerOwnership) {
		t.Fatal("sealed parent admitted engine", err)
	}
}
func TestNativeBorrowedConstructorChecksActualCallerDeadlineAfterOwnedWork(t *testing.T) {
	ctx, p := nativeResourceProfileTestPipeline(t)
	defer p.Release()
	caller := workerLaggingDeadlineContext{Context: ctx, deadline: time.Now().Add(time.Second)}
	entered, resume, joined := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(resume) }) }
	defer unblock()
	result := make(chan error, 1)
	go func() {
		defer close(joined)
		engine, err := newForNativePipeline(caller, "unused", func(string) (*Engine, error) { close(entered); <-resume; return &Engine{}, nil })
		if engine != nil {
			err = errWorkerProtocol
		}
		result <- err
	}()
	t.Cleanup(func() {
		unblock()
		select {
		case <-joined:
		case <-time.After(time.Second):
			t.Error("constructor did not join")
		}
	})
	select {
	case <-entered:
	case err := <-result:
		t.Fatal("constructor did not enter under live fixture deadline", err)
	case <-time.After(2 * time.Second):
		t.Fatal("fixture constructor did not enter")
	}
	timer := time.NewTimer(max(0, time.Until(caller.deadline)) + time.Millisecond)
	<-timer.C
	timer.Stop()
	unblock()
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("caller ended while parent remained live", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("owned constructor did not return after actual callback join")
	}
	if workerContextError(p.ctx) != nil {
		t.Fatal("fixture parent did not remain independently live")
	}
	p.mu.Lock()
	pending := len(p.parentIO)
	p.mu.Unlock()
	if pending != 0 {
		t.Fatal("actual constructor return did not join resource owner")
	}
}

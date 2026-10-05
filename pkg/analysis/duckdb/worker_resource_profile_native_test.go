//go:build analytics_duckdb && cgo

package duckdb

import (
	"errors"
	"testing"
	"time"
)

func TestNativeBorrowedEngineRefusesUnboundAndResourceDriftBeforeHash(t *testing.T) {
	ctx, p := nativeResourceProfileTestPipeline(t)
	defer p.Release()
	engine, err := newForNativePipeline(ctx, "unused", func(string) (*Engine, error) { return &Engine{}, nil })
	if err != nil {
		t.Fatal(err)
	}
	// Matching mutable values cannot substitute the constructor's immutable
	// exact-parent admission. An unbound generic engine must fail before I/O.
	unbound := &Engine{MemoryLimit: "256MiB", SpillLimit: "128MiB", Timeout: time.Second}
	unboundHashes := 0
	unbound.workerBeforeHash = func() { unboundHashes++ }
	if a, _, err := unbound.prepareWorker(ctx, []string{"not-opened"}, nil, nil); a != nil || !errors.Is(err, errWorkerOwnership) || unboundHashes != 0 {
		t.Fatal("matching mutable limits bypassed exact parent constructor", a, err)
	}
	engine.MemoryLimit = "128MiB"
	hashes := 0
	engine.workerBeforeHash = func() { hashes++ }
	if a, _, err := engine.prepareWorker(ctx, []string{"not-opened"}, nil, nil); a != nil || !errors.Is(err, errWorkerResourceLimit) || hashes != 0 {
		t.Fatal("drift reached source/SQL work", a, err)
	}
}

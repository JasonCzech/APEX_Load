package api

import (
	"context"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

type dynatraceRunTraceStore interface {
	RecordDynatraceRunTraceIDsFencedContext(context.Context, string, string, time.Time, []string, string, string, uint64) (bool, error)
}

// Caller holds monitorMu, so worker admission cannot race shutdown's Wait.
func (a *runArtifacts) startNativeDynatraceTraceWorker(ctx context.Context, project, run string, started time.Time, term executionLeaseTerm) {
	if a.runner.durableExecutionFence && !term.valid() {
		return
	}
	a.monitorWorkers.Add(1)
	go func() {
		defer a.monitorWorkers.Done()
		defer func() {
			flush, stop := context.WithTimeout(context.Background(), 2*time.Second)
			defer stop()
			a.persistNativeDynatraceTraceIDs(flush, project, run, started, term)
		}()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				flush, stop := context.WithTimeout(ctx, 2*time.Second)
				a.persistNativeDynatraceTraceIDs(flush, project, run, started, term)
				stop()
			}
		}
	}()
}

func (a *runArtifacts) persistNativeDynatraceTraceIDs(ctx context.Context, project, run string, started time.Time, term executionLeaseTerm) {
	if a == nil || a.runner == nil || ctx.Err() != nil || !a.nativeDynatrace.Load() {
		return
	}
	store, ok := a.runner.testStore.(dynatraceRunTraceStore)
	if !ok {
		return
	}
	a.dynatraceMu.Lock()
	window := a.nativeTraceLookups.take()
	if len(a.nativeTraceStored) >= storage.MaxDynatraceRunTraceIDs {
		// Storage keeps the first 64 proofs per job; it can accept no more.
		a.dynatraceMu.Unlock()
		return
	}
	ids := make([]string, 0, len(window))
	for _, id := range window {
		if !a.nativeTraceStored[id] {
			ids = append(ids, id)
		}
	}
	a.dynatraceMu.Unlock()
	if len(ids) == 0 {
		return
	}
	if ctx.Err() != nil {
		a.dynatraceMu.Lock()
		a.nativeTraceLookups.restore(ids)
		a.dynatraceMu.Unlock()
		return
	}
	leaseName, owner, generation := "", "", uint64(0)
	if a.runner.durableExecutionFence {
		leaseName, owner, generation = executionLeaseName, term.ownerID, term.generation
	}
	var accepted bool
	err := a.runner.trackDurableWrite(func() error {
		var writeErr error
		accepted, writeErr = store.RecordDynatraceRunTraceIDsFencedContext(ctx, project, run, started, ids, leaseName, owner, generation)
		return writeErr
	})
	a.dynatraceMu.Lock()
	defer a.dynatraceMu.Unlock()
	if err != nil || !accepted {
		// A rejected or failed write keeps its IDs for the next attempt.
		a.nativeTraceLookups.restore(ids)
		return
	}
	if a.nativeTraceStored == nil {
		a.nativeTraceStored = map[string]bool{}
	}
	for _, id := range ids {
		a.nativeTraceStored[id] = true
	}
}

package api

import (
	"context"
	"sort"
	"strings"
	"time"
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
	ids := make([]string, 0, len(a.dynatraceTraceIDs))
	for id := range a.dynatraceTraceIDs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	fingerprint := strings.Join(ids, ",")
	unchanged := fingerprint == a.nativeTraceWritten
	a.dynatraceMu.Unlock()
	if len(ids) == 0 || unchanged || ctx.Err() != nil {
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
	if err == nil && accepted {
		a.dynatraceMu.Lock()
		a.nativeTraceWritten = fingerprint
		a.dynatraceMu.Unlock()
	}
}

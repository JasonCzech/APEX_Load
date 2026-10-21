package api

import (
	"context"
	"time"
)

func (tr *TestRunner) finishExecutionWorker() {
	tr.executionWorkerMu.Lock()
	tr.executionWorkers--
	close(tr.executionWorkerChanged)
	tr.executionWorkerChanged = make(chan struct{})
	tr.executionWorkerMu.Unlock()
}

// waitExecutionWorkers runs after shutdown has closed admission and requested
// stops. Terminal status alone is not proof that archive/storage writes joined.
// A deadline returns its error and leaves physical ownership intact.
func (tr *TestRunner) waitExecutionWorkers(ctx context.Context) error {
	if tr == nil {
		return nil
	}
	for {
		tr.executionWorkerMu.Lock()
		count, changed := tr.executionWorkers, tr.executionWorkerChanged
		tr.executionWorkerMu.Unlock()
		if count == 0 {
			break
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	// A run driver may have handed a failed terminal write to the existing
	// retry owner. Shutdown disables new retry work; join any operation already
	// entered before dependent stores and the event hub are torn down.
	for {
		tr.terminalRetryMu.Lock()
		pending := len(tr.terminalRetrying)
		tr.terminalRetryMu.Unlock()
		if pending == 0 {
			return nil
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
}

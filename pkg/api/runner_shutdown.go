package api

import (
	"context"
	"errors"
	"fmt"
)

var errRunnerShuttingDown = errors.New("controller is shutting down")

// beginShutdown closes execution admission under the same lock used for every
// start transition. An in-flight start therefore either becomes visible to the
// subsequent active-test snapshot or observes this gate and declines to launch.
func (tr *TestRunner) beginShutdown() {
	if tr == nil {
		return
	}
	tr.mu.Lock()
	tr.shuttingDown = true
	tr.mu.Unlock()
}

func (tr *TestRunner) shutdownErrorLocked() error {
	if tr != nil && tr.shuttingDown {
		return errRunnerShuttingDown
	}
	return nil
}

func (tr *TestRunner) stopActiveTests(ctx context.Context) error {
	if tr == nil {
		return nil
	}
	tr.activeStopMu.Lock()
	defer tr.activeStopMu.Unlock()
	tr.mu.Lock()
	ids := make([]string, 0)
	for id, test := range tr.tests {
		if test == nil {
			continue
		}
		switch test.Status {
		case "INITIALIZING", "RUNNING", "PAUSED", "STOPPING", "SHAKEOUT":
			ids = append(ids, id)
		}
	}
	tr.mu.Unlock()

	type stopResult struct {
		id  string
		err error
	}
	results := make(chan stopResult, len(ids))
	for _, id := range ids {
		id := id
		go func() {
			results <- stopResult{id: id, err: tr.stopTestWithContext(ctx, id, false)}
		}()
	}
	var errs []error
	for range ids {
		select {
		case <-ctx.Done():
			// A legacy backend may ignore context after stopTestWithContext has
			// already persisted STOPPING. Cancel only those durably transitioned
			// runs; never kill work whose persistence failed and rolled back.
			tr.cancelDurablyStoppingTests(ids)
			errs = append(errs, ctx.Err())
			return errors.Join(errs...)
		case result := <-results:
			if result.err != nil {
				errs = append(errs, fmt.Errorf("stop active test %s: %w", result.id, result.err))
			}
		}
	}
	return errors.Join(errs...)
}

func (tr *TestRunner) cancelDurablyStoppingTests(ids []string) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	for _, id := range ids {
		test := tr.tests[id]
		if test != nil && test.Status == "STOPPING" && test.cancel != nil {
			test.cancel()
		}
	}
}

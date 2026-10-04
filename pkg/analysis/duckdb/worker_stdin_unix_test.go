//go:build linux || darwin

package duckdb

import (
	"context"
	"os"
	"testing"
)

func TestWorkerActualOwnedStandardInputUsesExactCommandPipe(t *testing.T) {
	a, admission := workerRuntimeTestAttempt(t, context.Background())
	launch := workerRuntimeTestLaunch(t, a, "stdin-owned")
	request := workerRequest{Preview: &workerPreviewOperation{InputHandle: a.bootstrap.Handles[0].Handle}}
	result, err := runWorkerParent(a, request, workerRunOptions{probe: func(*workerAttempt, *os.Process) error { return nil }, testLaunch: launch})
	if err != nil || result == nil {
		t.Fatal("actual child stdin did not match the owned command pipe", err)
	}
	a.mu.Lock()
	reaped, pipesJoined := a.reaped, a.pipesJoined
	a.mu.Unlock()
	if !reaped || !pipesJoined || admission.releases.Load() != 0 {
		t.Fatal("stdin binding changed sole Wait/pipe join/publication ownership")
	}
	if err := result.release(); err != nil || admission.releases.Load() != 1 {
		t.Fatal("actual joined child failed its exact single release", err)
	}
}

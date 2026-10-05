package duckdb

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Err/Done deliberately lag the advertised absolute deadline, as a timer
// callback may. Native admission must use the actual wall-clock bound too.
type workerLaggingDeadlineContext struct {
	context.Context
	deadline time.Time
}

func (c workerLaggingDeadlineContext) Deadline() (time.Time, bool) { return c.deadline, true }
func (c workerLaggingDeadlineContext) Err() error                  { return nil }
func (c workerLaggingDeadlineContext) Done() <-chan struct{}       { return nil }

func TestWorkerPastActualDeadlineDoesNotProbeAllocateStageOrStart(t *testing.T) {
	ctx := workerLaggingDeadlineContext{Context: context.Background(), deadline: time.Now().Add(-time.Second)}
	probes := 0
	controller := newWorkerAdmissionController(func(workerBootstrap) error { probes++; return nil })
	if release, err := controller.reserve(ctx, workerRuntimeTestBootstrap(t)); release != nil || !errors.Is(err, context.DeadlineExceeded) || probes != 0 {
		t.Fatal("expired actual deadline admitted resource probe")
	}
	parent := t.TempDir()
	admission := &workerTestAdmission{}
	if a, err := newWorkerAttempt(ctx, parent, workerRuntimeTestBootstrap(t), admission); a != nil || !errors.Is(err, context.DeadlineExceeded) || admission.reservations.Load() != 0 {
		t.Fatal("expired actual deadline allocated scratch")
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatal("expired admission created files")
	}
	a, _ := workerRuntimeTestAttempt(t, context.Background())
	a.ctx = ctx
	file, err := os.CreateTemp(t.TempDir(), "source")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	file.Write(make([]byte, 100))
	if a.stageInput(a.bootstrap.Handles[0].Handle, file) == nil {
		t.Fatal("past actual deadline staged input")
	}
	probeCount := atomic.Int32{}
	if result, err := runWorkerParent(a, workerRequest{Preview: &workerPreviewOperation{InputHandle: a.bootstrap.Handles[0].Handle}}, workerRunOptions{probe: func(*workerAttempt, *os.Process) error { probeCount.Add(1); return nil }}); result != nil || !errors.Is(err, context.DeadlineExceeded) || a.started || probeCount.Load() != 0 {
		t.Fatal("past actual deadline started worker", err)
	}
}
func TestWorkerPinnedExecutableIdentityDoesNotFollowLaterPathReplacement(t *testing.T) {
	approval := workerExecutableApproval{}
	calls := 0
	digest := strings.Repeat("a", 64)
	read := func() (string, error) { calls++; return digest, nil }
	approval.pin(read)
	digest = strings.Repeat("b", 64)
	approval.pin(read)
	if calls != 1 || approval.digest != strings.Repeat("a", 64) {
		t.Fatal("transport build approval silently changed")
	}
}
func TestWorkerFrozenRequestDoesNotShareCallerMapping(t *testing.T) {
	b := workerTestBootstrap(t)
	request := workerTestRequest(b)
	frozen, err := (workerFrame{Identity: b.Identity, Kind: "request", Request: &request}).clone()
	if err != nil {
		t.Fatal(err)
	}
	request.Normalize.Mapping.Timestamp[0] = "changed"
	request.Normalize.InputHandles[0] = "changed"
	if frozen.Request.Normalize.Mapping.Timestamp[0] != "at" || frozen.Request.Normalize.InputHandles[0] != b.Handles[0].Handle {
		t.Fatal("request approval shared caller-owned slices")
	}
}

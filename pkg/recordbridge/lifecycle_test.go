package recordbridge

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type observedWaitContext struct {
	context.Context
	once    sync.Once
	waiting chan struct{}
}

func (ctx *observedWaitContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.waiting) })
	return ctx.Context.Done()
}

func TestManagerBackgroundLifecycle(t *testing.T) {
	for name, manager := range map[string]interface {
		BackgroundRunning() bool
		StartJanitor(context.Context)
		Close()
	}{
		"recording": NewBrowserRecordingManager(nil),
		"replay":    NewBrowserReplayManager(nil),
	} {
		t.Run(name, func(t *testing.T) {
			if manager.BackgroundRunning() {
				t.Fatal("constructor started background work")
			}
			manager.StartJanitor(context.Background())
			if !manager.BackgroundRunning() {
				t.Fatal("startup did not own a retention worker")
			}
			manager.Close()
			if manager.BackgroundRunning() {
				t.Fatal("shutdown retained its background worker")
			}
			// Replay owns a permanent close fence. Recording historically allows
			// the host to restart its janitor after a stop; preserve both contracts.
			manager.StartJanitor(context.Background())
			if name == "replay" && manager.BackgroundRunning() {
				t.Fatal("startup resurrected a closed replay manager")
			}
			if name == "recording" && !manager.BackgroundRunning() {
				t.Fatal("recording manager did not preserve janitor restartability")
			}
			manager.Close()
		})
	}
}

func TestReplayWaitUsesCapturedWorkerWithoutHoldingManagerLock(t *testing.T) {
	manager := NewBrowserReplayManager(nil)
	if found, err := manager.Wait(context.Background(), "missing"); found || err != nil {
		t.Fatalf("missing = %v, %v", found, err)
	}
	oldDone := make(chan struct{})
	manager.sessions["id"] = &BrowserReplaySession{workerDone: oldDone}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if found, err := manager.Wait(ctx, "id"); !found || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled wait = %v, %v", found, err)
	}
	// Deadline keeps this regression bounded if Wait accidentally retains mu.
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	observed := &observedWaitContext{Context: ctx, waiting: make(chan struct{})}
	returned := make(chan error, 1)
	go func() { _, err := manager.Wait(observed, "id"); returned <- err }()
	select {
	case <-observed.waiting:
	case <-ctx.Done():
		t.Fatal("wait did not capture the worker")
	}
	// Closing the old channel must release an in-flight waiter, even if a new
	// worker is admitted under the same ID after this one has retired.
	replacement := make(chan struct{})
	manager.mu.Lock()
	manager.sessions["id"] = &BrowserReplaySession{workerDone: replacement}
	close(oldDone)
	manager.mu.Unlock()
	if err := <-returned; err != nil {
		t.Fatal(err)
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	cancel2()
	if found, err := manager.Wait(ctx2, "id"); !found || !errors.Is(err, context.Canceled) {
		t.Fatalf("replacement wait = %v, %v", found, err)
	}
	close(replacement)
	if found, err := manager.Wait(context.Background(), "id"); !found || err != nil {
		t.Fatalf("finished wait = %v, %v", found, err)
	}
}

// Preserve the manager-level assertions formerly made through API-private
// session fields. The API integration additionally exercises idle expiry.
func TestReplayForeignProjectOperationsLeaveSessionUnchanged(t *testing.T) {
	manager := NewBrowserReplayManager(nil)
	now := time.Now().UTC()
	session := &BrowserReplaySession{ID: "session", ProjectID: "owner", Status: BrowserReplayPaused, lastActivityAt: now}
	manager.sessions[session.ID] = session
	if got, ok := manager.GetForProject(session.ID, "other"); ok || got != nil {
		t.Fatal("foreign get succeeded")
	}
	if got, ok, changed := manager.PauseForProject(session.ID, "other"); ok || changed || got != nil {
		t.Fatal("foreign pause succeeded")
	}
	if got, ok, changed := manager.ResumeForProject(session.ID, "other"); ok || changed || got != nil {
		t.Fatal("foreign resume succeeded")
	}
	if got, ok := manager.StopForProject(session.ID, "other"); ok || got != nil {
		t.Fatal("foreign stop succeeded")
	}
	if session.Status != BrowserReplayPaused || !session.lastActivityAt.Equal(now) {
		t.Fatal("foreign operation changed status or activity")
	}
}

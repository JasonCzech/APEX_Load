package contentmutation

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestPublicationRejectsReadsAcrossEveryMutationOutcome(t *testing.T) {
	for _, outcome := range []string{"committed", "rejected", "unknown"} {
		t.Run(outcome, func(t *testing.T) {
			var owner Coordinator
			before := owner.Observe()
			if !owner.CanPublish(before) {
				t.Fatal("fresh read rejected")
			}
			owner.Lock()
			during := owner.Observe()
			if owner.CanPublish(before) || owner.CanPublish(during) {
				t.Fatal("overlapping read accepted")
			}
			owner.Unlock()
			if owner.CanPublish(before) || owner.CanPublish(during) {
				t.Fatal("stale read accepted after completion")
			}
			if !owner.CanPublish(owner.Observe()) {
				t.Fatal("new read rejected after completion")
			}
		})
	}
}

func TestBlockedRepositoryKeepsLiveReadsAvailableAndSerializesWriters(t *testing.T) {
	var owner Coordinator
	var live sync.Mutex
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		owner.Lock()
		defer owner.Unlock()
		live.Lock()
		defer live.Unlock()
		_, _ = OutsideLock(&live, func() (int, error) { close(entered); <-release; return 1, nil })
	}()
	<-entered
	if !live.TryLock() {
		close(release)
		<-finished
		t.Fatal("repository I/O held live-state mutex")
	}
	live.Unlock()
	second := make(chan struct{})
	go func() { owner.Lock(); defer owner.Unlock(); close(second) }()
	select {
	case <-second:
		close(release)
		<-finished
		t.Fatal("second writer crossed unfinished mutation")
	case <-time.After(10 * time.Millisecond):
	}
	close(release)
	<-finished
	select {
	case <-second:
	case <-time.After(time.Second):
		t.Fatal("second writer did not resume")
	}
}

func TestRepositoryFailureAndPanicRestoreLiveLock(t *testing.T) {
	var live sync.Mutex
	live.Lock()
	want := errors.New("uncertain commit")
	if err := WriteOutsideLock(&live, func() error { return want }); !errors.Is(err, want) {
		t.Fatal(err)
	}
	if live.TryLock() {
		live.Unlock()
		t.Fatal("error path did not reacquire live lock")
	}
	func() {
		defer func() {
			if recover() != "storage panic" {
				t.Fatal("panic was not preserved")
			}
		}()
		_ = WriteOutsideLock(&live, func() error { panic("storage panic") })
	}()
	if live.TryLock() {
		live.Unlock()
		t.Fatal("panic path did not reacquire live lock")
	}
	live.Unlock()
}

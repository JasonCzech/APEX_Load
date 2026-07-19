package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fakeDesktopProcessLock struct{}

func (fakeDesktopProcessLock) Close() error { return nil }

func TestDesktopProcessLockRejectsConcurrentOwner(t *testing.T) {
	directory := t.TempDir()
	first, err := acquireDesktopProcessLock(directory)
	if err != nil {
		t.Fatal(err)
	}
	second, err := acquireDesktopProcessLock(directory)
	if err == nil {
		_ = second.Close()
		t.Fatal("second desktop process acquired the same app-data lock")
	}
	if !errors.Is(err, errDesktopProcessLockHeld) {
		t.Fatalf("second lock error = %v, want held sentinel", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := acquireDesktopProcessLock(directory)
	if err != nil {
		t.Fatalf("desktop process lock was not reusable after release: %v", err)
	}
	t.Cleanup(func() { _ = third.Close() })

	contents, err := os.ReadFile(filepath.Join(directory, desktopProcessLockFile))
	if err != nil {
		t.Fatal(err)
	}
	if len(contents) == 0 {
		t.Fatal("desktop process lock does not identify its owning process")
	}
}

func TestDesktopProcessLockRetryBridgesTransientSingleInstanceRace(t *testing.T) {
	attempts := 0
	lock, err := acquireDesktopProcessLockWithRetry("unused", 100*time.Millisecond, time.Millisecond, func(string) (desktopProcessLock, error) {
		attempts++
		if attempts < 3 {
			return nil, fmt.Errorf("secondary instance still exiting: %w", errDesktopProcessLockHeld)
		}
		return fakeDesktopProcessLock{}, nil
	})
	if err != nil {
		t.Fatalf("transient lock conflict was not retried: %v", err)
	}
	if lock == nil || attempts != 3 {
		t.Fatalf("lock = %v, attempts = %d; want a lock after 3 attempts", lock, attempts)
	}
}

func TestDesktopProcessLockRetryRemainsBoundedForLiveOwner(t *testing.T) {
	attempts := 0
	_, err := acquireDesktopProcessLockWithRetry("unused", 10*time.Millisecond, time.Millisecond, func(string) (desktopProcessLock, error) {
		attempts++
		return nil, errDesktopProcessLockHeld
	})
	if !errors.Is(err, errDesktopProcessLockHeld) {
		t.Fatalf("persistent owner error = %v, want held sentinel", err)
	}
	if attempts < 2 {
		t.Fatalf("persistent owner was not retried: %d attempt(s)", attempts)
	}

	nonLockErr := errors.New("permission denied")
	attempts = 0
	_, err = acquireDesktopProcessLockWithRetry("unused", time.Second, time.Millisecond, func(string) (desktopProcessLock, error) {
		attempts++
		return nil, nonLockErr
	})
	if !errors.Is(err, nonLockErr) || attempts != 1 {
		t.Fatalf("non-contention error = %v after %d attempts; want immediate failure", err, attempts)
	}
}

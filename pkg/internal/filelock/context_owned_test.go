package filelock

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

type ownedDetachedDeadline struct {
	context.Context
	at time.Time
}

func (c ownedDetachedDeadline) Deadline() (time.Time, bool) { return c.at, true }
func (c ownedDetachedDeadline) Done() <-chan struct{}       { return nil }
func (c ownedDetachedDeadline) Err() error                  { return nil }

func ownedLockTestFile(t *testing.T) *os.File {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "owned-lock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func TestLockContextOwnedReturnsLateAcquiredRelease(t *testing.T) {
	f := ownedLockTestFile(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var releases atomic.Int32
	unlock, err := lockContextOwned(ctx, f, func(actual *os.File) (func() error, bool, error) {
		if actual != f {
			t.Error("foreign lock descriptor")
		}
		cancel()
		return func() error { releases.Add(1); return nil }, true, nil
	})
	if !errors.Is(err, context.Canceled) || unlock == nil || releases.Load() != 0 {
		t.Fatal("lost or prematurely invoked acquired release", err)
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	if err := unlock(); err != nil || releases.Load() != 1 {
		t.Fatal("release not stable exactly once", err)
	}
	if _, err := f.Stat(); err != nil {
		t.Fatal("owned release closed caller descriptor", err)
	}
}
func TestLockContextOwnedReleaseFailureAndPanicCannotBecomeNil(t *testing.T) {
	for _, panics := range []bool{false, true} {
		t.Run(map[bool]string{false: "error", true: "panic"}[panics], func(t *testing.T) {
			fault := errors.New("controlled platform release failure")
			var calls atomic.Int32
			release := onceLockRelease(func() error {
				calls.Add(1)
				if panics {
					panic("controlled platform panic")
				}
				return fault
			})
			first, second := release(), release()
			want := fault
			if panics {
				want = ErrReleaseUnproven
			}
			if !errors.Is(first, want) || !errors.Is(second, want) || calls.Load() != 1 {
				t.Fatal("forgot first release result or retried", first, second, calls.Load())
			}
		})
	}
}
func TestLockContextOwnedPreservesAcquiredErrorAuthority(t *testing.T) {
	f := ownedLockTestFile(t)
	fault := errors.New("controlled acquired error")
	var releases atomic.Int32
	release, err := lockContextOwned(context.Background(), f, func(*os.File) (func() error, bool, error) {
		return func() error { releases.Add(1); return nil }, true, fault
	})
	if !errors.Is(err, fault) || release == nil || releases.Load() != 0 {
		t.Fatal("acquired error lost exact authority", err)
	}
	if err := release(); err != nil || releases.Load() != 1 {
		t.Fatal("owned release failed", err)
	}
}
func TestLockContextOwnedUsesActualDetachedDeadline(t *testing.T) {
	f := ownedLockTestFile(t)
	for _, expired := range []bool{true, false} {
		t.Run(map[bool]string{true: "expired", false: "future"}[expired], func(t *testing.T) {
			at := time.Now().Add(25 * time.Millisecond)
			if expired {
				at = time.Now().Add(-time.Second)
			}
			ctx := ownedDetachedDeadline{Context: context.Background(), at: at}
			var attempts atomic.Int32
			start := time.Now()
			release, err := lockContextOwned(ctx, f, func(*os.File) (func() error, bool, error) { attempts.Add(1); return nil, false, nil })
			if release != nil || !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
				t.Fatal("actual detached deadline did not bound acquisition", err)
			}
			if expired && attempts.Load() != 0 {
				t.Fatal("expired deadline entered kernel lock")
			}
		})
	}
}

package generatorrpc

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// testClock is a controllable time source. Execution-epoch leases are
// expiry-driven, so a test that wants a lapsed epoch otherwise has to pick a TTL
// short enough to elapse and then sleep past it — which makes the assertion a
// race against the scheduler rather than a statement about behavior.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock(start time.Time) *testClock {
	return &testClock{now: start}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// newClockedEpochTestService mirrors newEpochTestService but drives the service
// from a controlled clock.
func newClockedEpochTestService(t *testing.T, clock *testClock) *Service {
	t.Helper()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	statePath := filepath.Join(t.TempDir(), "epoch.json")
	service := NewService("epoch-clock-test", 10,
		WithExecutionEpochStatePath(statePath),
		WithClock(clock.Now),
	)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := service.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})
	return service
}

func (s *Service) mutationErrorForTest(generation uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.validateMutationGenerationLocked(generation)
}

// TestWithClockDrivesExecutionEpochExpiry proves the seam controls epoch
// lifetime end to end: a lease claimed under a held clock stays valid however
// much real time passes, and lapses the instant the clock is advanced past its
// TTL. Nothing sleeps — expiry is caused, not awaited.
//
// This is the deterministic counterpart to FLAKE-1 (docs/qa/FLAKE_LOG.md), which
// failed 2 runs in 5 because a 30 ms TTL could elapse before the claim that
// installed it was even observed on a loaded machine.
func TestWithClockDrivesExecutionEpochExpiry(t *testing.T) {
	start := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	clock := newTestClock(start)
	service := newClockedEpochTestService(t, clock)

	if got := service.nowTime(); !got.Equal(start) {
		t.Fatalf("service time = %v, want the injected %v", got, start)
	}

	const generation = uint64(1)
	const ttl = time.Minute

	if ack := claimEpoch(t, service, generation, ttl); !ack.GetAccepted() {
		t.Fatalf("claim rejected: %s", ack.GetErrorMessage())
	}

	// Held still, the lease must not lapse. A sleep-based test cannot assert
	// this at all: it can only wait and hope the window is still open.
	if err := service.mutationErrorForTest(generation); err != nil {
		t.Fatalf("mutation rejected while the clock is held before expiry: %v", err)
	}

	clock.Advance(ttl + time.Second)

	err := service.mutationErrorForTest(generation)
	if err == nil {
		t.Fatal("mutation accepted after the clock advanced past the TTL, want an expiry rejection")
	}
	if !strings.Contains(err.Error(), ExecutionGenerationSessionExpiredMessage) {
		t.Fatalf("mutation error = %v, want the session-expired rejection", err)
	}
}

// TestClockHeldStillKeepsLeaseValid isolates the property that makes the seam
// worth having: real elapsed time must not affect a lease when the clock is
// controlled. Under the old wall-clock path a short TTL would have expired here.
func TestClockHeldStillKeepsLeaseValid(t *testing.T) {
	clock := newTestClock(time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC))
	service := newClockedEpochTestService(t, clock)

	const generation = uint64(1)
	if ack := claimEpoch(t, service, generation, 30*time.Millisecond); !ack.GetAccepted() {
		t.Fatalf("claim rejected: %s", ack.GetErrorMessage())
	}

	// Well past the 30 ms TTL in wall-clock terms, and deliberately the same TTL
	// FLAKE-1 used. The clock has not moved, so the lease has not lapsed.
	time.Sleep(100 * time.Millisecond)

	if err := service.mutationErrorForTest(generation); err != nil {
		t.Fatalf("lease lapsed on wall-clock time despite a held clock: %v", err)
	}
}

// TestNowTimeFallsBackToWallClock pins the production default. Options run after
// the struct literal, so WithClock(nil) can leave the field unset; no production
// path may depend on the option having been supplied.
func TestNowTimeFallsBackToWallClock(t *testing.T) {
	cases := map[string]*Service{
		"unset":      NewService("gen-default", 4, WithExecution()),
		"nil option": NewService("gen-nil", 4, WithExecution(), WithClock(nil)),
	}
	for name, service := range cases {
		t.Run(name, func(t *testing.T) {
			before := time.Now()
			got := service.nowTime()
			after := time.Now()
			if got.Before(before) || got.After(after) {
				t.Fatalf("nowTime() = %v, want a wall-clock reading between %v and %v", got, before, after)
			}
		})
	}
}

// TestNilServiceNowTimeDoesNotPanic guards the nil receiver: nowTime is reached
// from expiry paths that can run during teardown.
func TestNilServiceNowTimeDoesNotPanic(t *testing.T) {
	var service *Service
	if got := service.nowTime(); got.IsZero() {
		t.Fatal("nowTime() on a nil service returned the zero time, want a wall-clock reading")
	}
}

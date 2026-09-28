package javarunner

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testHostCapacity(t *testing.T, budget HostBudget) *HostCapacity {
	t.Helper()
	c, err := NewHostCapacity(budget)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func sharedLedger(t *testing.T, c *HostCapacity, epoch string) *HostLedger {
	t.Helper()
	l, err := NewHostLedgerWithCapacity(HostBinding{OwnerID: "owner", OwnerEpoch: epoch, ExecutionID: "run", GroupID: "group", ConfigurationSHA256: strings.Repeat("a", 64)}, HostBudget{100, 1000, math.MaxInt64}, time.Minute, c)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func assertSharedUsage(t *testing.T, c *HostCapacity, want HostUsage) {
	t.Helper()
	if got := c.Snapshot(); got != want {
		t.Fatalf("shared usage=%+v, want %+v", got, want)
	}
}

func TestHostCapacityMixedAdmissionAndDraining(t *testing.T) {
	c := testHostCapacity(t, HostBudget{3, 5, 300})
	a, err := NewDedicatedHostAdmission(c, 100)
	if err != nil {
		t.Fatal(err)
	}
	release, err := a.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	l := sharedLedger(t, c, "epoch-1")
	h, err := l.Reserve("epoch-1", 4, HostProfile{2, 100}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	assertSharedUsage(t, c, HostUsage{3, 5, 300})
	readyHosts(t, l, h)
	if err := l.Commit("epoch-1", h[0].ReservationID); err != nil {
		t.Fatal(err)
	}
	for _, slot := range h[0].SlotIDs {
		for i := 0; i < 2; i++ {
			if err := l.CloseSlot("epoch-1", h[0].ID, slot, true); err != nil {
				t.Fatal(err)
			}
		}
	}
	assertSharedUsage(t, c, HostUsage{3, 3, 300})
	other := sharedLedger(t, c, "epoch-2")
	if _, err := other.Reserve("epoch-2", 1, HostProfile{2, 100}, time.Second); !errors.Is(err, ErrHostCapacity) {
		t.Fatalf("fragmented/draining host lost charge: %v", err)
	}
	if err := l.ConfirmTermination(h[0].ID, "wrong", true); !errors.Is(err, ErrHostIdentity) {
		t.Fatal(err)
	}
	assertSharedUsage(t, c, HostUsage{3, 3, 300})
	for i := 0; i < 2; i++ {
		if err := l.ConfirmTermination(h[0].ID, "container-"+h[0].ID, true); err != nil {
			t.Fatal(err)
		}
	}
	assertSharedUsage(t, c, HostUsage{2, 3, 200})
	if _, err := other.Reserve("epoch-2", 2, HostProfile{2, 100}, time.Second); err != nil {
		t.Fatal(err)
	}
	// Retirement fences an owner; it cannot release a surviving JVM's charge.
	if err := l.Retire("epoch-1"); err != nil {
		t.Fatal(err)
	}
	assertSharedUsage(t, c, HostUsage{3, 5, 300})
	if err := l.ConfirmTermination(h[1].ID, "container-"+h[1].ID, true); err != nil {
		t.Fatal(err)
	}
	release()
	release()
	assertSharedUsage(t, c, HostUsage{1, 2, 100})
	if err := other.Retire("epoch-2"); err != nil {
		t.Fatal(err)
	}
	assertSharedUsage(t, c, HostUsage{})
}

func TestHostCapacityPreparationReleaseEvidence(t *testing.T) {
	for _, stage := range []string{"reserved", "preparing", "ready", "expired_reserved", "expired_preparing"} {
		t.Run(stage, func(t *testing.T) {
			c := testHostCapacity(t, HostBudget{1, 2, 100})
			l := sharedLedger(t, c, "epoch-1")
			now := time.Now()
			l.now = func() time.Time { return now }
			h, err := l.Reserve("epoch-1", 2, HostProfile{2, 100}, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if stage == "ready" {
				readyHosts(t, l, h)
			} else if strings.Contains(stage, "preparing") {
				if err := l.BeginPrepare("epoch-1", h[0].ID); err != nil {
					t.Fatal(err)
				}
			}
			if strings.HasPrefix(stage, "expired") {
				now = now.Add(2 * time.Second)
				l.Snapshot()
			} else if err := l.Abort("epoch-1", h[0].ReservationID); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(stage, "reserved") {
				assertSharedUsage(t, c, HostUsage{})
				return
			}
			assertSharedUsage(t, c, HostUsage{1, 2, 100})
			if err := l.ConfirmNotStarted(h[0].ID, false); err == nil {
				t.Fatal("unproven no-start accepted")
			}
			assertSharedUsage(t, c, HostUsage{1, 2, 100})
			if stage == "ready" {
				err = l.ConfirmTermination(h[0].ID, "container-"+h[0].ID, true)
			} else {
				err = l.ConfirmNotStarted(h[0].ID, true)
			}
			if err != nil {
				t.Fatal(err)
			}
			assertSharedUsage(t, c, HostUsage{})
		})
	}
}

func TestHostCapacityAtomicConcurrentGroups(t *testing.T) {
	for _, budget := range []HostBudget{{2, 100, 10000}, {100, 4, 10000}, {100, 100, 200}} {
		c := testHostCapacity(t, budget)
		var successes atomic.Int32
		var wg sync.WaitGroup
		ledgers := make([]*HostLedger, 32)
		for i := range ledgers {
			ledgers[i] = sharedLedger(t, c, "epoch-1")
		}
		for _, l := range ledgers {
			wg.Go(func() {
				if _, err := l.Reserve("epoch-1", 4, HostProfile{2, 100}, time.Second); err == nil {
					successes.Add(1)
				} else if !errors.Is(err, ErrHostCapacity) {
					t.Error(err)
				}
			})
		}
		wg.Wait()
		if successes.Load() != 1 {
			t.Fatalf("budget %+v admitted %d batches", budget, successes.Load())
		}
		assertSharedUsage(t, c, HostUsage{2, 4, 200})
		for _, l := range ledgers {
			if err := l.Retire("epoch-1"); err != nil {
				t.Fatal(err)
			}
		}
		assertSharedUsage(t, c, HostUsage{})
	}
}

func TestDedicatedAdmissionWaitCancellationAndFence(t *testing.T) {
	for _, outcome := range []string{"release", "cancel", "close"} {
		t.Run(outcome, func(t *testing.T) {
			c := testHostCapacity(t, HostBudget{1, 1, 100})
			a, _ := NewDedicatedHostAdmission(c, 100)
			release, err := a.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() {
				r, err := a.Acquire(ctx)
				if r != nil {
					r()
				}
				result <- err
			}()
			switch outcome {
			case "release":
				release()
			case "cancel":
				cancel()
			case "close":
				c.CloseAdmission()
				c.CloseAdmission()
			}
			err = <-result
			if outcome == "release" && err != nil || outcome == "cancel" && !errors.Is(err, context.Canceled) || outcome == "close" && !errors.Is(err, ErrHostCapacityClosed) {
				t.Fatalf("%s: %v", outcome, err)
			}
			if outcome != "release" {
				assertSharedUsage(t, c, HostUsage{1, 1, 100})
			}
			release()
			assertSharedUsage(t, c, HostUsage{})
			if outcome == "close" {
				if _, err := sharedLedger(t, c, "new-owner").Reserve("new-owner", 1, HostProfile{1, 100}, time.Second); !errors.Is(err, ErrHostCapacityClosed) {
					t.Fatalf("new owner reopened fenced capacity: %v", err)
				}
			}
		})
	}
}

func TestHostCapacityValidationAndOverflow(t *testing.T) {
	for _, b := range []HostBudget{{}, {1, 1, 0}, {1, 0, 1}, {0, 1, 1}, {10001, 1, 1}, {1, 100001, 1}} {
		if _, err := NewHostCapacity(b); !errors.Is(err, ErrHostState) {
			t.Fatalf("invalid budget %+v: %v", b, err)
		}
	}
	c := testHostCapacity(t, HostBudget{10, 10, math.MaxInt64})
	for _, memory := range []int64{-1, 0} {
		if _, err := NewDedicatedHostAdmission(c, memory); !errors.Is(err, ErrHostState) {
			t.Fatal(err)
		}
	}
	if _, err := NewDedicatedHostAdmission(nil, 1); !errors.Is(err, ErrHostState) {
		t.Fatal(err)
	}
	if _, err := (&DedicatedHostAdmission{}).Acquire(context.Background()); !errors.Is(err, ErrHostState) {
		t.Fatal(err)
	}
	l := sharedLedger(t, c, "epoch-1")
	if _, err := l.Reserve("epoch-1", 2, HostProfile{1, math.MaxInt64}, time.Second); !errors.Is(err, ErrHostCapacity) {
		t.Fatal("memory multiplication overflow", err)
	}
	assertSharedUsage(t, c, HostUsage{})
	// A failed batch must not freeze its profile or consume any host identity.
	if l.profile != nil || l.sequence != 0 {
		t.Fatal("failed reservation mutated ledger")
	}
	if _, err := l.Reserve("epoch-1", 2, HostProfile{2, 100}, time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestHostCapacityRejectedBatchDoesNotFreezeProfile(t *testing.T) {
	c := testHostCapacity(t, HostBudget{2, 4, 100})
	l := sharedLedger(t, c, "epoch-1")
	if _, err := l.Reserve("epoch-1", 4, HostProfile{2, 100}, time.Second); !errors.Is(err, ErrHostCapacity) {
		t.Fatal(err)
	}
	if l.profile != nil || l.sequence != 0 || l.admissions != 0 {
		t.Fatal("shared budget rejection mutated the run ledger")
	}
	assertSharedUsage(t, c, HostUsage{})
	if _, err := l.Reserve("epoch-1", 4, HostProfile{4, 100}, time.Second); err != nil {
		t.Fatal(err)
	}
	assertSharedUsage(t, c, HostUsage{1, 4, 100})
}

func TestHostCapacitySupervisorCleanupAcrossOwnerEpochs(t *testing.T) {
	for _, confirmed := range []bool{false, true} {
		t.Run(map[bool]string{false: "uncertain", true: "confirmed"}[confirmed], func(t *testing.T) {
			c := testHostCapacity(t, HostBudget{1, 2, 100})
			l := sharedLedger(t, c, "epoch-1")
			h, err := l.Reserve("epoch-1", 2, HostProfile{2, 100}, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			runtime := newTestManagedHost("container")
			if !confirmed {
				runtime.terminationErr = ErrProcessContainmentFailed
			}
			s, err := NewHostSupervisor(l, provisionFunc(func(context.Context, HostAllocation) (ManagedHost, error) { return runtime, nil }), true)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			contexts := map[string]map[string]string{}
			for _, slot := range h[0].SlotIDs {
				contexts[slot] = map[string]string{}
			}
			if err := s.Prepare(ctx, "epoch-1", h[0].ReservationID, contexts); err != nil {
				t.Fatal(err)
			}
			err = s.Close(ctx)
			if confirmed && err != nil || !confirmed && !errors.Is(err, ErrProcessContainmentFailed) {
				t.Fatalf("cleanup confirmation=%v: %v", confirmed, err)
			}
			next := sharedLedger(t, c, "epoch-2")
			_, err = next.Reserve("epoch-2", 2, HostProfile{2, 100}, time.Minute)
			if confirmed && err != nil || !confirmed && !errors.Is(err, ErrHostCapacity) {
				t.Fatalf("replacement owner admission after cleanup confirmation=%v: %v", confirmed, err)
			}
			assertSharedUsage(t, c, HostUsage{1, 2, 100})
			if runtime.terminations.Load() != 1 {
				t.Fatalf("termination called %d times", runtime.terminations.Load())
			}
		})
	}
}

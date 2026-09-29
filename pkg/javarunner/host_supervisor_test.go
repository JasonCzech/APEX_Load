package javarunner

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type provisionFunc func(context.Context, HostAllocation) (ManagedHost, error)

func (f provisionFunc) Provision(ctx context.Context, a HostAllocation) (ManagedHost, error) {
	return f(ctx, a)
}

type testManagedHost struct {
	id             string
	done           chan struct{}
	stopped        chan struct{}
	once           sync.Once
	ready          atomic.Int32
	terminations   atomic.Int32
	terminationErr error
	terminate      func(context.Context) error
	commit         func(context.Context) error
	invoke         func(context.Context) (PooledFrame, bool, error)
}

func newTestManagedHost(id string) *testManagedHost {
	return &testManagedHost{id: id, done: make(chan struct{}), stopped: make(chan struct{})}
}
func (h *testManagedHost) Identity() string      { return h.id }
func (h *testManagedHost) Done() <-chan struct{} { return h.done }
func (h *testManagedHost) Ready(context.Context, map[string]map[string]string) error {
	h.ready.Add(1)
	return nil
}
func (h *testManagedHost) Commit(ctx context.Context) error {
	if h.commit != nil {
		return h.commit(ctx)
	}
	return nil
}
func (h *testManagedHost) Invoke(ctx context.Context, _, _ string, _ map[string]string) (PooledFrame, bool, error) {
	if h.invoke != nil {
		return h.invoke(ctx)
	}
	return PooledFrame{}, false, nil
}
func (h *testManagedHost) CloseSlot(context.Context, string) error { return nil }
func (h *testManagedHost) Terminate(ctx context.Context) error {
	h.terminations.Add(1)
	h.once.Do(func() { close(h.done); close(h.stopped) })
	if h.terminate != nil {
		return h.terminate(ctx)
	}
	return h.terminationErr
}
func supervisorLedger(t *testing.T, vusers int, lease time.Duration) (*HostLedger, []HostAllocation, map[string]map[string]string) {
	t.Helper()
	l, err := NewHostLedger(HostBinding{"owner", "epoch", "run", "group", strings.Repeat("a", 64)}, HostBudget{vusers, vusers, int64(vusers) * 100}, lease)
	if err != nil {
		t.Fatal(err)
	}
	a, err := l.Reserve("epoch", vusers, HostProfile{1, 100}, lease)
	if err != nil {
		t.Fatal(err)
	}
	contexts := make(map[string]map[string]string)
	for _, h := range a {
		for _, slot := range h.SlotIDs {
			contexts[slot] = map[string]string{}
		}
	}
	return l, a, contexts
}
func closeSupervisor(t *testing.T, s *HostSupervisor) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
}
func awaitUsage(t *testing.T, l *HostLedger, want HostUsage) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		_, got := l.Snapshot()
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("usage %+v, want %+v", got, want)
		}
		time.Sleep(time.Millisecond)
	}
}
func TestHostSupervisorLeaseIndependentOfInvocation(t *testing.T) {
	l, a, contexts := supervisorLedger(t, 1, 150*time.Millisecond)
	h := newTestManagedHost("boundary")
	h.invoke = func(context.Context) (PooledFrame, bool, error) {
		<-h.stopped
		return PooledFrame{}, false, ErrPooledHostReaped
	}
	s, _ := NewHostSupervisor(l, provisionFunc(func(context.Context, HostAllocation) (ManagedHost, error) { return h, nil }), true)
	defer closeSupervisor(t, s)
	if err := s.Prepare(context.Background(), "epoch", a[0].ReservationID, contexts); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(context.Background(), "epoch", a[0].ReservationID); err != nil {
		t.Fatal(err)
	}
	invoke := make(chan error, 1)
	go func() {
		_, err := s.Invoke(context.Background(), "epoch", a[0].ID, a[0].SlotIDs[0], "action", nil)
		invoke <- err
	}()
	select {
	case err := <-invoke:
		if !errors.Is(err, ErrPooledHostReaped) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("lease cleanup waited for invocation")
	}
	awaitUsage(t, l, HostUsage{})
	if h.terminations.Load() != 1 {
		t.Fatal("termination was not exactly once")
	}
	if err := l.Renew("epoch", time.Second); !errors.Is(err, ErrHostFence) {
		t.Fatal("expired lease revived", err)
	}
}
func TestHostSupervisorCommitBarrierAcrossHosts(t *testing.T) {
	l, a, contexts := supervisorLedger(t, 2, time.Minute)
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	backend := provisionFunc(func(_ context.Context, allocation HostAllocation) (ManagedHost, error) {
		h := newTestManagedHost(allocation.ID)
		h.commit = func(ctx context.Context) error {
			if calls.Add(1) == 2 {
				close(entered)
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return nil
		}
		return h, nil
	})
	s, _ := NewHostSupervisor(l, backend, true)
	defer closeSupervisor(t, s)
	if err := s.Prepare(context.Background(), "epoch", a[0].ReservationID, contexts); err != nil {
		t.Fatal(err)
	}
	committed := make(chan error, 1)
	go func() { committed <- s.Commit(context.Background(), "epoch", a[0].ReservationID) }()
	<-entered
	for _, h := range a {
		if _, err := s.Invoke(context.Background(), "epoch", h.ID, h.SlotIDs[0], "init", nil); err == nil {
			t.Fatal("initialization before all hosts committed")
		}
	}
	if err := s.Commit(context.Background(), "epoch", a[0].ReservationID); !errors.Is(err, ErrHostState) {
		t.Fatal("concurrent commit admitted", err)
	}
	close(release)
	if err := <-committed; err != nil {
		t.Fatal(err)
	}
	if _, err := s.Invoke(context.Background(), "epoch", a[0].ID, a[0].SlotIDs[0], "init", nil); err != nil {
		t.Fatal(err)
	}
}
func TestHostSupervisorDelayedCreationAfterRetirement(t *testing.T) {
	l, a, contexts := supervisorLedger(t, 1, time.Minute)
	h := newTestManagedHost("late-boundary")
	entered := make(chan struct{})
	release := make(chan struct{})
	s, _ := NewHostSupervisor(l, provisionFunc(func(context.Context, HostAllocation) (ManagedHost, error) { close(entered); <-release; return h, nil }), true)
	prepared := make(chan error, 1)
	go func() { prepared <- s.Prepare(context.Background(), "epoch", a[0].ReservationID, contexts) }()
	<-entered
	if err := s.Prepare(context.Background(), "epoch", a[0].ReservationID, contexts); !errors.Is(err, ErrHostState) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := s.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("close did not retain pending creation", err)
	}
	_, usage := l.Snapshot()
	if usage != (HostUsage{1, 1, 100}) {
		t.Fatal(usage)
	}
	if _, err := l.Reserve("epoch", 1, HostProfile{1, 100}, time.Second); !errors.Is(err, ErrHostFence) {
		t.Fatal("shutdown allowed new reservation", err)
	}
	close(release)
	if err := <-prepared; err == nil {
		t.Fatal("late prepare succeeded")
	}
	closeSupervisor(t, s)
	if h.ready.Load() != 0 || h.terminations.Load() != 1 {
		t.Fatalf("late ready=%d terminations=%d", h.ready.Load(), h.terminations.Load())
	}
}
func TestHostSupervisorCleanupUncertaintyRetainsCapacity(t *testing.T) {
	for _, kind := range []string{"unknown-create", "known-cleanup", "never-started"} {
		t.Run(kind, func(t *testing.T) {
			l, a, contexts := supervisorLedger(t, 1, time.Minute)
			h := newTestManagedHost("boundary")
			h.terminationErr = ErrProcessContainmentFailed
			backend := provisionFunc(func(context.Context, HostAllocation) (ManagedHost, error) {
				switch kind {
				case "unknown-create":
					return nil, errors.New("uncertain")
				case "never-started":
					return nil, ErrHostNotStarted
				}
				return h, nil
			})
			s, _ := NewHostSupervisor(l, backend, true)
			err := s.Prepare(context.Background(), "epoch", a[0].ReservationID, contexts)
			if kind != "known-cleanup" && err == nil {
				t.Fatal("failed creation succeeded")
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err = s.Close(ctx)
			if kind == "never-started" {
				if err != nil {
					t.Fatal(err)
				}
				awaitUsage(t, l, HostUsage{})
			} else {
				if !errors.Is(err, ErrProcessContainmentFailed) {
					t.Fatal("uncertainty not reported", err)
				}
				_, usage := l.Snapshot()
				if usage != (HostUsage{1, 1, 100}) {
					t.Fatal("uncertainty released reservation", usage)
				}
			}
		})
	}
}
func TestHostSupervisorCancelledSlotAccounting(t *testing.T) {
	l, a, contexts := supervisorLedger(t, 1, time.Minute)
	h := newTestManagedHost("boundary")
	h.invoke = func(ctx context.Context) (PooledFrame, bool, error) {
		<-ctx.Done()
		return PooledFrame{}, true, ctx.Err()
	}
	s, _ := NewHostSupervisor(l, provisionFunc(func(context.Context, HostAllocation) (ManagedHost, error) { return h, nil }), true)
	defer closeSupervisor(t, s)
	if err := s.Prepare(context.Background(), "epoch", a[0].ReservationID, contexts); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(context.Background(), "epoch", a[0].ReservationID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := s.Invoke(ctx, "epoch", a[0].ID, a[0].SlotIDs[0], "action", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	awaitUsage(t, l, HostUsage{})
}
func TestHostSupervisorRejectsInvalidContextBeforeProvisioning(t *testing.T) {
	l, a, contexts := supervisorLedger(t, 1, time.Minute)
	var calls atomic.Int32
	s, _ := NewHostSupervisor(l, provisionFunc(func(context.Context, HostAllocation) (ManagedHost, error) {
		calls.Add(1)
		return nil, ErrHostNotStarted
	}), true)
	defer closeSupervisor(t, s)
	contexts[a[0].SlotIDs[0]]["UNAPPROVED_ENV"] = "value"
	if err := s.Prepare(context.Background(), "epoch", a[0].ReservationID, contexts); !errors.Is(err, ErrHostIdentity) {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("invalid context provisioned a host")
	}
}

func TestHostSupervisorExpiryCancelsPendingProvision(t *testing.T) {
	for _, leaseExpiry := range []bool{true, false} {
		t.Run(strconv.FormatBool(leaseExpiry), func(t *testing.T) {
			l, err := NewHostLedger(HostBinding{"owner", "epoch", "run", "group", strings.Repeat("a", 64)}, HostBudget{1, 1, 100}, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			prepareFor := time.Minute
			if !leaseExpiry {
				prepareFor = 75 * time.Millisecond
			}
			a, err := l.Reserve("epoch", 1, HostProfile{1, 100}, prepareFor)
			if err != nil {
				t.Fatal(err)
			}
			if leaseExpiry {
				if err = l.Renew("epoch", 75*time.Millisecond); err != nil {
					t.Fatal(err)
				}
			}
			s, _ := NewHostSupervisor(l, provisionFunc(func(ctx context.Context, _ HostAllocation) (ManagedHost, error) {
				<-ctx.Done()
				return nil, errors.Join(ErrHostNotStarted, ctx.Err())
			}), true)
			defer closeSupervisor(t, s)
			done := make(chan error, 1)
			go func() {
				done <- s.Prepare(context.Background(), "epoch", a[0].ReservationID, map[string]map[string]string{a[0].SlotIDs[0]: {}})
			}()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("expired prepare succeeded")
				}
			case <-time.After(time.Second):
				t.Fatal("expiry did not cancel pending creation")
			}
			awaitUsage(t, l, HostUsage{})
			if !leaseExpiry {
				if err = l.Renew("epoch", time.Minute); err != nil {
					t.Fatal("preparation expiry retired entire owner", err)
				}
			}
		})
	}
}
func TestHostSupervisorReleasesCompletedRuntimeBuffers(t *testing.T) {
	l, a, contexts := supervisorLedger(t, 1, time.Minute)
	h := newTestManagedHost("boundary")
	s, _ := NewHostSupervisor(l, provisionFunc(func(context.Context, HostAllocation) (ManagedHost, error) { return h, nil }), true)
	if err := s.Prepare(context.Background(), "epoch", a[0].ReservationID, contexts); err != nil {
		t.Fatal(err)
	}
	closeSupervisor(t, s)
	s.mu.Lock()
	retained := len(s.hosts)
	s.mu.Unlock()
	if retained != 0 {
		t.Fatal("completed runtime retained", retained)
	}
}

func TestHostSupervisorReapedPeerHasStableTerminalError(t *testing.T) {
	l, a, contexts := supervisorLedger(t, 1, time.Minute)
	h := newTestManagedHost("boundary")
	s, _ := NewHostSupervisor(l, provisionFunc(func(context.Context, HostAllocation) (ManagedHost, error) { return h, nil }), true)
	defer closeSupervisor(t, s)
	if err := s.Prepare(context.Background(), "epoch", a[0].ReservationID, contexts); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(context.Background(), "epoch", a[0].ReservationID); err != nil {
		t.Fatal(err)
	}
	h.once.Do(func() { close(h.done); close(h.stopped) })
	awaitUsage(t, l, HostUsage{})
	if _, err := s.Invoke(context.Background(), "epoch", a[0].ID, a[0].SlotIDs[0], "action", nil); !errors.Is(err, ErrPooledHostReaped) {
		t.Fatal("lost terminal host error", err)
	}
}

func TestHostSupervisorChangedIdentityRetainsQuarantine(t *testing.T) {
	for _, during := range []bool{false, true} {
		t.Run(strconv.FormatBool(during), func(t *testing.T) {
			l, a, contexts := supervisorLedger(t, 1, time.Minute)
			h := newTestManagedHost("original-boundary")
			if during {
				h.terminate = func(context.Context) error { h.id = "replacement-boundary"; return nil }
			}
			s, _ := NewHostSupervisor(l, provisionFunc(func(context.Context, HostAllocation) (ManagedHost, error) { return h, nil }), true)
			if err := s.Prepare(context.Background(), "epoch", a[0].ReservationID, contexts); err != nil {
				t.Fatal(err)
			}
			if !during {
				h.id = "replacement-boundary"
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := s.Close(ctx); !errors.Is(err, ErrProcessContainmentFailed) {
				t.Fatal("changed identity released capacity", err)
			}
			hosts, usage := l.Snapshot()
			if usage != (HostUsage{1, 1, 100}) || hosts[0].ContainmentID != "original-boundary" {
				t.Fatal(hosts, usage)
			}
			if !during && h.terminations.Load() != 0 {
				t.Fatal("terminated replacement boundary")
			}
		})
	}
}
func TestHostSupervisorBlockedCleanupCannotReleaseCapacity(t *testing.T) {
	l, a, contexts := supervisorLedger(t, 1, time.Minute)
	h := newTestManagedHost("boundary")
	release := make(chan struct{})
	returned := make(chan struct{})
	h.terminate = func(context.Context) error { <-release; close(returned); return nil }
	s, _ := NewHostSupervisor(l, provisionFunc(func(context.Context, HostAllocation) (ManagedHost, error) { return h, nil }), true)
	// Set before publishing any runtime handle to the monitor.
	s.cleanupTimeout = 20 * time.Millisecond
	if err := s.Prepare(context.Background(), "epoch", a[0].ReservationID, contexts); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Close(ctx); !errors.Is(err, ErrProcessContainmentFailed) {
		t.Fatal("cleanup timeout not quarantined", err)
	}
	close(release)
	<-returned
	hosts, usage := l.Snapshot()
	if hosts[0].State != HostQuarantined || usage.Hosts != 1 {
		t.Fatal("late result released unresolved reservation", hosts, usage)
	}
}

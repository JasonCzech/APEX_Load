package javarunner

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func testHostLedger(t *testing.T, budget HostBudget) (*HostLedger, *time.Time) {
	t.Helper()
	now := time.Now()
	l, err := newHostLedger(HostBinding{OwnerID: "generator", OwnerEpoch: "epoch-1", ExecutionID: "run-1", GroupID: "group-1", ConfigurationSHA256: strings.Repeat("a", 64)}, budget, 5*time.Second, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	return l, &now
}
func readyHosts(t *testing.T, l *HostLedger, hosts []HostAllocation) {
	t.Helper()
	for _, h := range hosts {
		if err := l.BeginPrepare("epoch-1", h.ID); err != nil {
			t.Fatal(err)
		}
		if err := l.BindContainment(h.ID, "container-"+h.ID); err != nil {
			t.Fatal(err)
		}
		if err := l.MarkReady("epoch-1", h.ID); err != nil {
			t.Fatal(err)
		}
	}
}

func TestHostLedgerInvocationFailureReasons(t *testing.T) {
	for _, code := range []string{"pooled_event_capture_invalid", "pooled_invocation_failed", "pooled_invocation_cancelled", "pooled_frame_invalid"} {
		t.Run(code, func(t *testing.T) {
			l, _ := testHostLedger(t, HostBudget{MaxHosts: 1, MaxVUsers: 2, MaxMemoryBytes: 100})
			h, err := l.Reserve("epoch-1", 2, HostProfile{2, 100}, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			readyHosts(t, l, h)
			if err := l.FailHost(h[0].ID, code); err != nil {
				t.Fatal(err)
			}
			if err := l.FailHost(h[0].ID, "pooled_cleanup_uncertain"); err != nil {
				t.Fatal(err)
			}
			hosts, usage := l.Snapshot()
			if hosts[0].State != HostQuarantined || !hosts[0].CleanupRequired || hosts[0].FailureCode != code || usage.Hosts != 1 || usage.VUsers != 2 || usage.MemoryBytes != 100 {
				t.Fatalf("failure lost or capacity released: %+v %+v", hosts, usage)
			}
		})
	}
}

func TestHostLedgerBarrierAndFragmentation(t *testing.T) {
	l, _ := testHostLedger(t, HostBudget{MaxHosts: 2, MaxVUsers: 4, MaxMemoryBytes: 200})
	h, err := l.Reserve("epoch-1", 3, HostProfile{2, 100}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(h) != 2 || len(h[0].SlotIDs) != 2 || len(h[1].SlotIDs) != 1 {
		t.Fatalf("hosts=%+v", h)
	}
	if err := l.CanInvoke("epoch-1", h[0].ID, h[0].SlotIDs[0]); err == nil {
		t.Fatal("invocation before commit")
	}
	readyHosts(t, l, h[:1])
	if err := l.Commit("epoch-1", h[0].ReservationID); err == nil {
		t.Fatal("partial ready commit")
	}
	readyHosts(t, l, h[1:])
	if err := l.Commit("epoch-1", h[0].ReservationID); err != nil {
		t.Fatal(err)
	}
	if err := l.CloseSlot("epoch-1", h[0].ID, h[0].SlotIDs[0], true); err != nil {
		t.Fatal(err)
	}
	if err := l.CanInvoke("epoch-1", h[0].ID, h[0].SlotIDs[0]); err == nil {
		t.Fatal("closed slot reused")
	}
	if err := l.CanInvoke("epoch-1", h[0].ID, h[0].SlotIDs[1]); err != nil {
		t.Fatal("survivor blocked", err)
	}
	if _, err := l.Reserve("epoch-1", 1, HostProfile{2, 100}, time.Second); !errors.Is(err, ErrHostCapacity) {
		t.Fatal("fragmented host freed", err)
	}
	if err := l.CloseSlot("epoch-1", h[0].ID, h[0].SlotIDs[0], true); err != nil {
		t.Fatal(err)
	}
	_, u := l.Snapshot()
	if u != (HostUsage{2, 2, 200}) {
		t.Fatalf("double release: %+v", u)
	}
	if err := l.CloseSlot("epoch-1", h[0].ID, h[0].SlotIDs[1], true); err != nil {
		t.Fatal(err)
	}
	_, u = l.Snapshot()
	if u != (HostUsage{2, 1, 200}) {
		t.Fatalf("memory released before reap: %+v", u)
	}
	if err := l.ConfirmTermination(h[0].ID, "container-"+h[0].ID, false); err == nil {
		t.Fatal("delete acknowledgment accepted")
	}
	if err := l.ConfirmTermination(h[0].ID, "wrong", true); err == nil {
		t.Fatal("changed identity accepted")
	}
	for i := 0; i < 2; i++ {
		if err := l.ConfirmTermination(h[0].ID, "container-"+h[0].ID, true); err != nil {
			t.Fatal(err)
		}
	}
	_, u = l.Snapshot()
	if u != (HostUsage{1, 1, 100}) {
		t.Fatalf("reap accounting: %+v", u)
	}
	next, err := l.Reserve("epoch-1", 1, HostProfile{2, 100}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if next[0].ID == h[0].ID || next[0].SlotIDs[0] == h[0].SlotIDs[0] {
		t.Fatal("identity reused")
	}
	if _, err := l.Reserve("epoch-1", 1, HostProfile{1, 1}, time.Second); !errors.Is(err, ErrHostIdentity) {
		t.Fatal("profile changed within configuration", err)
	}
}

func TestHostLedgerExpiryAndDelayedPreparation(t *testing.T) {
	for _, runExpiry := range []bool{false, true} {
		l, now := testHostLedger(t, HostBudget{2, 4, 200})
		h, err := l.Reserve("epoch-1", 3, HostProfile{2, 100}, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if err = l.BeginPrepare("epoch-1", h[0].ID); err != nil {
			t.Fatal(err)
		}
		*now = now.Add(2 * time.Second)
		if runExpiry {
			*now = now.Add(5 * time.Second)
		}
		if err = l.BindContainment(h[0].ID, "delayed-pod-uid"); err != nil {
			t.Fatal(err)
		}
		if err = l.MarkReady("epoch-1", h[0].ID); err == nil {
			t.Fatal("late ready after expiry")
		}
		if err = l.Commit("epoch-1", h[0].ReservationID); err == nil {
			t.Fatal("late commit after expiry")
		}
		_, u := l.Snapshot()
		if u != (HostUsage{1, 2, 100}) {
			t.Fatalf("uncertain preparation freed: %+v", u)
		}
		if err = l.ConfirmNotStarted(h[0].ID, true); err == nil {
			t.Fatal("bound pod classified not started")
		}
		if err = l.ConfirmTermination(h[0].ID, "delayed-pod-uid", true); err != nil {
			t.Fatal(err)
		}
		if runExpiry {
			if err = l.Renew("epoch-1", time.Second); !errors.Is(err, ErrHostFence) {
				t.Fatal("expired owner revived", err)
			}
		}
	}
}

func TestHostLedgerUnknownCleanupAndOwnerFence(t *testing.T) {
	l, now := testHostLedger(t, HostBudget{1, 2, 100})
	h, err := l.Reserve("epoch-1", 2, HostProfile{2, 100}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	readyHosts(t, l, h)
	if err = l.Commit("new-epoch", h[0].ReservationID); !errors.Is(err, ErrHostFence) {
		t.Fatal(err)
	}
	if err = l.Commit("epoch-1", h[0].ReservationID); err != nil {
		t.Fatal(err)
	}
	if err = l.CloseSlot("epoch-1", h[0].ID, h[0].SlotIDs[0], false); err != nil {
		t.Fatal(err)
	}
	if err = l.CanInvoke("epoch-1", h[0].ID, h[0].SlotIDs[1]); err == nil {
		t.Fatal("peer invoked after quarantine")
	}
	*now = now.Add(10 * time.Second)
	s, u := l.Snapshot()
	if u != (HostUsage{1, 2, 100}) || s[0].FailureCode != "pooled_cleanup_uncertain" || !s[0].CleanupRequired {
		t.Fatalf("lost failure/reservation: %+v %+v", s, u)
	}
}

func TestHostLedgerConcurrentReservationLimit(t *testing.T) {
	l, _ := testHostLedger(t, HostBudget{2, 4, 200})
	var wg sync.WaitGroup
	var mu sync.Mutex
	success := 0
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := l.Reserve("epoch-1", 2, HostProfile{2, 100}, time.Second)
			if err == nil {
				mu.Lock()
				success++
				mu.Unlock()
			} else if !errors.Is(err, ErrHostCapacity) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	_, u := l.Snapshot()
	if success != 2 || u != (HostUsage{2, 4, 200}) {
		t.Fatalf("success=%d usage=%+v", success, u)
	}
}

func TestHostLedgerAbortBeforeAndDuringProvisioning(t *testing.T) {
	l, _ := testHostLedger(t, HostBudget{2, 2, 200})
	h, err := l.Reserve("epoch-1", 2, HostProfile{1, 100}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err = l.BeginPrepare("epoch-1", h[0].ID); err != nil {
		t.Fatal(err)
	}
	if err = l.Abort("epoch-1", h[0].ReservationID); err != nil {
		t.Fatal(err)
	}
	if err = l.BeginPrepare("epoch-1", h[1].ID); err == nil {
		t.Fatal("aborted reservation provisioned")
	}
	_, u := l.Snapshot()
	if u != (HostUsage{1, 1, 100}) {
		t.Fatal(u)
	}
	if err = l.ConfirmNotStarted(h[0].ID, false); err == nil {
		t.Fatal("uncertain creation released")
	}
	if err = l.ConfirmNotStarted(h[0].ID, true); err != nil {
		t.Fatal(err)
	}
	_, u = l.Snapshot()
	if u != (HostUsage{}) {
		t.Fatal(u)
	}
}

func TestHostLedgerFailureAffectsOnlyItsHost(t *testing.T) {
	l, _ := testHostLedger(t, HostBudget{2, 2, 200})
	h, err := l.Reserve("epoch-1", 2, HostProfile{1, 100}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	readyHosts(t, l, h)
	if err = l.Commit("epoch-1", h[0].ReservationID); err != nil {
		t.Fatal(err)
	}
	if err = l.FailHost(h[0].ID, "secret-bearing arbitrary message"); err == nil {
		t.Fatal("unbounded failure reason accepted")
	}
	if err = l.FailHost(h[0].ID, "pooled_host_crashed"); err != nil {
		t.Fatal(err)
	}
	if err = l.FailHost(h[0].ID, "pooled_host_transport_lost"); err != nil {
		t.Fatal(err)
	}
	if err = l.CanInvoke("epoch-1", h[0].ID, h[0].SlotIDs[0]); err == nil {
		t.Fatal("failed host accepted invocation")
	}
	if err = l.CanInvoke("epoch-1", h[1].ID, h[1].SlotIDs[0]); err != nil {
		t.Fatal("unrelated host failed", err)
	}
	s, u := l.Snapshot()
	if u != (HostUsage{2, 2, 200}) {
		t.Fatal(u)
	}
	for _, a := range s {
		if a.ID == h[0].ID && a.FailureCode != "pooled_host_crashed" {
			t.Fatal("triggering failure overwritten", a)
		}
	}
}

func TestHostLedgerRejectedReservationDoesNotMutate(t *testing.T) {
	l, _ := testHostLedger(t, HostBudget{2, 4, 200})
	for _, profile := range []HostProfile{{0, 100}, {2, 0}, {2, 201}} {
		if _, err := l.Reserve("epoch-1", 2, profile, time.Second); err == nil {
			t.Fatal("invalid profile admitted")
		}
		_, u := l.Snapshot()
		if u != (HostUsage{}) || l.sequence != 0 || l.profile != nil {
			t.Fatalf("rejected reservation mutated ledger: %+v", u)
		}
	}
	h, err := l.Reserve("epoch-1", 2, HostProfile{2, 100}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	original := h[0].SlotIDs[0]
	h[0].SlotIDs[0] = "mutated"
	s, _ := l.Snapshot()
	if s[0].SlotIDs[0] != original {
		t.Fatal("caller mutated authoritative slot identity")
	}
	l.admissions = 1_000_000
	if _, err = l.Reserve("epoch-1", 1, HostProfile{2, 100}, time.Second); !errors.Is(err, ErrHostCapacity) {
		t.Fatal("lifetime records unbounded", err)
	}
}

func TestHostLedgerMatchesJavaAdmissionCeiling(t *testing.T) {
	if MaxPooledSlotsPerHost != 1024 {
		t.Fatalf("Go ceiling %d drifted from PooledHost.MAX_SLOTS", MaxPooledSlotsPerHost)
	}
	over := MaxPooledSlotsPerHost + 1
	l, _ := testHostLedger(t, HostBudget{2, 4000, 200})
	if _, err := l.Reserve("epoch-1", over, HostProfile{over, 100}, time.Second); !errors.Is(err, ErrHostState) {
		t.Fatalf("unsupported Java capacity accepted: %v", err)
	}
	_, usage := l.Snapshot()
	if usage != (HostUsage{}) {
		t.Fatalf("rejected capacity charged: %+v", usage)
	}
	h, err := l.Reserve("epoch-1", over, HostProfile{MaxPooledSlotsPerHost, 100}, time.Second)
	if err != nil || len(h) != 2 || len(h[0].SlotIDs) != MaxPooledSlotsPerHost || len(h[1].SlotIDs) != 1 {
		t.Fatalf("ceiling partition: %v %+v", err, h)
	}
}

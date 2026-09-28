package javasidecar

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/javarunner"
)

func sharedAdmissionServer(t *testing.T, c *javarunner.HostCapacity) *Server {
	t.Helper()
	a, err := javarunner.NewDedicatedHostAdmission(c, 100)
	if err != nil {
		t.Fatal(err)
	}
	return NewServer(ServerOptions{Operator: config.JavaVuserOperatorConfig{MaxProcesses: 2}, HostAdmission: a})
}

func TestSidecarSharesAdmissionWithPooledReservations(t *testing.T) {
	c, err := javarunner.NewHostCapacity(javarunner.HostBudget{MaxHosts: 1, MaxVUsers: 2, MaxMemoryBytes: 100})
	if err != nil {
		t.Fatal(err)
	}
	s := sharedAdmissionServer(t, c)
	l, err := javarunner.NewHostLedgerWithCapacity(javarunner.HostBinding{OwnerID: "owner", OwnerEpoch: "epoch", ExecutionID: "run", GroupID: "group", ConfigurationSHA256: strings.Repeat("a", 64)}, javarunner.HostBudget{MaxHosts: 1, MaxVUsers: 2, MaxMemoryBytes: 100}, time.Minute, c)
	if err != nil {
		t.Fatal(err)
	}
	h, err := l.Reserve("epoch", 2, javarunner.HostProfile{SlotsPerHost: 2, MemoryBytesPerHost: 100}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if release, failure := s.acquireProcessSlot(ctx); release != nil || failure == nil || failure.code != "execution_timeout" {
		t.Fatalf("dedicated session bypassed shared quota: %v", failure)
	}
	if len(s.slots) != 0 || c.Snapshot() != (javarunner.HostUsage{Hosts: 1, VUsers: 2, MemoryBytes: 100}) {
		t.Fatalf("cancelled waiter changed charges: local=%d shared=%+v", len(s.slots), c.Snapshot())
	}
	if err := l.Abort("epoch", h[0].ReservationID); err != nil {
		t.Fatal(err)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	release, failure := s.acquireProcessSlot(ctx2)
	if failure != nil {
		t.Fatal(failure)
	}
	if c.Snapshot() != (javarunner.HostUsage{Hosts: 1, VUsers: 1, MemoryBytes: 100}) {
		t.Fatal("dedicated JVM was not charged", c.Snapshot())
	}
	for i := 0; i < 2; i++ {
		s.finishProcessCapacity(release, nil)
	}
	if len(s.slots) != 0 || c.Snapshot() != (javarunner.HostUsage{}) {
		t.Fatal("capacity not released exactly once", c.Snapshot())
	}
}

func TestSidecarSharedAdmissionRetainsQuarantineAndWakesWaiters(t *testing.T) {
	c, err := javarunner.NewHostCapacity(javarunner.HostBudget{MaxHosts: 1, MaxVUsers: 1, MaxMemoryBytes: 100})
	if err != nil {
		t.Fatal(err)
	}
	s, peer := sharedAdmissionServer(t, c), sharedAdmissionServer(t, c)
	release, failure := s.acquireProcessSlot(context.Background())
	if failure != nil {
		t.Fatal(failure)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := make(chan *safeFailure, 1)
	go func() {
		r, f := peer.acquireProcessSlot(ctx)
		if r != nil {
			r()
		}
		result <- f
	}()
	s.finishProcessCapacity(release, javarunner.ErrProcessContainmentFailed)
	if f := <-result; f == nil || f.code != "process_containment_unavailable" {
		t.Fatalf("shared waiter was not fenced: %v", f)
	}
	if len(s.slots) != 1 || len(peer.slots) != 0 || c.Snapshot() != (javarunner.HostUsage{Hosts: 1, VUsers: 1, MemoryBytes: 100}) {
		t.Fatalf("quarantine charge lost: %d/%d %+v", len(s.slots), len(peer.slots), c.Snapshot())
	}
	if !peer.containmentUnavailable() {
		t.Fatal("peer still advertises containment readiness")
	}
}

func TestSidecarSharedFenceWakesLocalLimitWaiter(t *testing.T) {
	c, _ := javarunner.NewHostCapacity(javarunner.HostBudget{MaxHosts: 2, MaxVUsers: 2, MaxMemoryBytes: 200})
	s := sharedAdmissionServer(t, c)
	for i := 0; i < 2; i++ {
		r, f := s.acquireProcessSlot(context.Background())
		if f != nil {
			t.Fatal(f)
		}
		defer r()
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := make(chan *safeFailure, 1)
	go func() {
		r, f := s.acquireProcessSlot(ctx)
		if r != nil {
			r()
		}
		result <- f
	}()
	c.CloseAdmission()
	if f := <-result; f == nil || f.code != "process_containment_unavailable" {
		t.Fatalf("local semaphore waiter missed global fence: %v", f)
	}
}

func TestSidecarSharedFenceWakesProbeWaiter(t *testing.T) {
	c, _ := javarunner.NewHostCapacity(javarunner.HostBudget{MaxHosts: 1, MaxVUsers: 1, MaxMemoryBytes: 100})
	s := sharedAdmissionServer(t, c)
	release, failure := s.acquireProbeSlot(context.Background())
	if failure != nil {
		t.Fatal(failure)
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := make(chan *safeFailure, 1)
	go func() {
		r, f := s.acquireProbeSlot(ctx)
		if r != nil {
			r()
		}
		result <- f
	}()
	c.CloseAdmission()
	if f := <-result; f == nil || f.code != "process_containment_unavailable" {
		t.Fatalf("probe waiter missed shared admission fence: %v", f)
	}
	if c.Snapshot() != (javarunner.HostUsage{}) {
		t.Fatal("probe consumed execution budget")
	}
}

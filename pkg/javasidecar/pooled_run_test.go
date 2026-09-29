package javasidecar

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/javarunner"
)

type pooledProvisionFunc func(context.Context, javarunner.HostAllocation) (javarunner.ManagedHost, error)

func (f pooledProvisionFunc) Provision(ctx context.Context, a javarunner.HostAllocation) (javarunner.ManagedHost, error) {
	return f(ctx, a)
}

type ownedTestHost struct {
	done        chan struct{}
	once        sync.Once
	stops       atomic.Int32
	err         error
	invoke      func(context.Context) (javarunner.PooledFrame, bool, error)
	onTerminate func()
}

func (h *ownedTestHost) Identity() string                                          { return "exact-container" }
func (h *ownedTestHost) Done() <-chan struct{}                                     { return h.done }
func (h *ownedTestHost) Ready(context.Context, map[string]map[string]string) error { return nil }
func (h *ownedTestHost) Commit(context.Context) error                              { return nil }
func (h *ownedTestHost) CloseSlot(context.Context, string) error                   { return nil }
func (h *ownedTestHost) Invoke(ctx context.Context, _, _ string, _ map[string]string) (javarunner.PooledFrame, bool, error) {
	if h.invoke != nil {
		return h.invoke(ctx)
	}
	return javarunner.PooledFrame{}, false, nil
}
func (h *ownedTestHost) Terminate(context.Context) error {
	h.stops.Add(1)
	if h.onTerminate != nil {
		h.onTerminate()
	}
	h.once.Do(func() { close(h.done) })
	return h.err
}

func pooledOwnerServer(t *testing.T) (*Server, *javarunner.HostCapacity) {
	t.Helper()
	c, err := javarunner.NewHostCapacity(javarunner.HostBudget{MaxHosts: 2, MaxVUsers: 4, MaxMemoryBytes: 200})
	if err != nil {
		t.Fatal(err)
	}
	a, err := javarunner.NewDedicatedHostAdmission(c, 100)
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(ServerOptions{Operator: config.JavaVuserOperatorConfig{Enabled: true}, HostAdmission: a})
	s.owner = &ownerGeneration{ownerID: "owner", epoch: "epoch", ready: true, leaseDeadline: time.Now().Add(time.Minute)}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	})
	return s, c
}

func openOwnedRun(t *testing.T, s *Server, group string, backend javarunner.HostProvisioner) *PooledRun {
	t.Helper()
	r, err := s.openPooledRun(context.Background(), "owner", "epoch", "execution", group, javarunner.HostBudget{MaxHosts: 2, MaxVUsers: 4, MaxMemoryBytes: 200}, strings.Repeat("a", 64), javarunner.HostProfile{SlotsPerHost: 2, MemoryBytesPerHost: 100}, backend)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func prepareOwnedRun(t *testing.T, r *PooledRun) javarunner.HostAllocation {
	t.Helper()
	a, err := r.Reserve(context.Background(), 2, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]map[string]string{}
	for _, slot := range a[0].SlotIDs {
		values[slot] = map[string]string{}
	}
	if err := r.Prepare(context.Background(), a[0].ReservationID, values); err != nil {
		t.Fatal(err)
	}
	if err := r.Commit(context.Background(), a[0].ReservationID); err != nil {
		t.Fatal(err)
	}
	return a[0]
}

func claimReplacement(s *Server) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/owner", strings.NewReader(`{"contract":"`+ContractVersion+`","owner_id":"replacement"}`))
	s.handleClaimOwner(w, r)
	return w
}

func TestPooledRunTakeoverCancelsBeforeWaiting(t *testing.T) {
	s, c := pooledOwnerServer(t)
	started := make(chan struct{})
	h := &ownedTestHost{done: make(chan struct{}), invoke: func(ctx context.Context) (javarunner.PooledFrame, bool, error) {
		close(started)
		<-ctx.Done()
		return javarunner.PooledFrame{}, false, ctx.Err()
	}}
	r := openOwnedRun(t, s, "group", pooledProvisionFunc(func(context.Context, javarunner.HostAllocation) (javarunner.ManagedHost, error) { return h, nil }))
	a := prepareOwnedRun(t, r)
	invoked := make(chan error, 1)
	go func() { _, err := r.Invoke(context.Background(), a.ID, a.SlotIDs[0], "action", nil); invoked <- err }()
	<-started
	w := claimReplacement(s)
	if w.Code != http.StatusOK {
		t.Fatalf("takeover: %d %s", w.Code, w.Body.String())
	}
	if err := <-invoked; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if c.Snapshot() != (javarunner.HostUsage{}) || h.stops.Load() != 1 {
		t.Fatalf("cleanup: %+v stops=%d", c.Snapshot(), h.stops.Load())
	}
	if _, err := r.Reserve(context.Background(), 1, time.Second); !errors.Is(err, javarunner.ErrHostFence) {
		t.Fatal("stale owner admitted", err)
	}
}

func TestPooledRunLeaseExpiryDoesNotReapPeerGroup(t *testing.T) {
	s, c := pooledOwnerServer(t)
	hosts := []*ownedTestHost{{done: make(chan struct{})}, {done: make(chan struct{})}}
	runs := make([]*PooledRun, 2)
	for i, group := range []string{"first", "second"} {
		h := hosts[i]
		runs[i] = openOwnedRun(t, s, group, pooledProvisionFunc(func(context.Context, javarunner.HostAllocation) (javarunner.ManagedHost, error) { return h, nil }))
		prepareOwnedRun(t, runs[i])
	}
	r := runs[0]
	r.mu.Lock()
	r.deadline = time.Now().Add(-time.Second)
	r.armLocked()
	r.mu.Unlock()
	select {
	case <-r.closed:
	case <-time.After(time.Second):
		t.Fatal("lease expiry did not clean up")
	}
	if r.cleanupErr != nil || c.Snapshot() != (javarunner.HostUsage{Hosts: 1, VUsers: 2, MemoryBytes: 100}) || hosts[1].stops.Load() != 0 {
		t.Fatalf("peer cleanup: %v %+v", r.cleanupErr, c.Snapshot())
	}
	if err := runs[1].Renew(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := r.Renew(context.Background()); !errors.Is(err, javarunner.ErrHostFence) {
		t.Fatal("expired lease renewed", err)
	}
}

func TestPooledOnlyOwnerExpiryStartsShutdown(t *testing.T) {
	s, c := pooledOwnerServer(t)
	h := &ownedTestHost{done: make(chan struct{})}
	r := openOwnedRun(t, s, "group", pooledProvisionFunc(func(context.Context, javarunner.HostAllocation) (javarunner.ManagedHost, error) { return h, nil }))
	prepareOwnedRun(t, r)
	s.ownerMu.Lock()
	s.owner.leaseDeadline = time.Now().Add(-time.Second)
	s.ownerMu.Unlock()
	if !s.expireOwnerIfPastDeadline(time.Now()) {
		t.Fatal("owner did not expire")
	}
	select {
	case <-s.OwnerLeaseExpired():
	case <-time.After(time.Second):
		t.Fatal("pooled owner considered idle")
	}
	select {
	case <-r.closed:
	case <-time.After(time.Second):
		t.Fatal("no pooled cleanup")
	}
	if c.Snapshot() != (javarunner.HostUsage{}) || !s.draining.Load() {
		t.Fatalf("cleanup=%+v draining=%v", c.Snapshot(), s.draining.Load())
	}
}

func TestPooledRunUncertainCleanupBlocksReplacement(t *testing.T) {
	s, c := pooledOwnerServer(t)
	h := &ownedTestHost{done: make(chan struct{}), err: javarunner.ErrProcessContainmentFailed}
	r := openOwnedRun(t, s, "group", pooledProvisionFunc(func(context.Context, javarunner.HostAllocation) (javarunner.ManagedHost, error) { return h, nil }))
	prepareOwnedRun(t, r)
	w := claimReplacement(s)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("replacement accepted: %d", w.Code)
	}
	if c.Snapshot() != (javarunner.HostUsage{Hosts: 1, VUsers: 2, MemoryBytes: 100}) || !s.containmentUnavailable() {
		t.Fatal("quarantine lost", c.Snapshot())
	}
	if len(s.selectedPooledRuns(r.owner)) != 1 {
		t.Fatal("quarantine owner forgotten")
	}
}

func TestPooledRunNoReplayAndCancelledClose(t *testing.T) {
	s, c := pooledOwnerServer(t)
	backend := pooledProvisionFunc(func(context.Context, javarunner.HostAllocation) (javarunner.ManagedHost, error) {
		t.Error("unprepared reservation provisioned")
		return nil, javarunner.ErrHostNotStarted
	})
	r := openOwnedRun(t, s, "group", backend)
	if _, err := r.Reserve(context.Background(), 2, time.Second); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = r.Close(ctx)
	if err := r.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.Snapshot() != (javarunner.HostUsage{}) {
		t.Fatal(c.Snapshot())
	}
	_, err := s.openPooledRun(context.Background(), "owner", "epoch", "execution", "group", javarunner.HostBudget{MaxHosts: 2, MaxVUsers: 4, MaxMemoryBytes: 200}, strings.Repeat("a", 64), javarunner.HostProfile{SlotsPerHost: 2, MemoryBytesPerHost: 100}, backend)
	if !errors.Is(err, javarunner.ErrHostIdentity) {
		t.Fatal("initialization replay allowed", err)
	}
	s.ownerMu.Lock()
	s.owner.leaseDeadline = time.Now().Add(-time.Second)
	s.ownerMu.Unlock()
	s.expireOwnerIfPastDeadline(time.Now())
	if len(s.selectedPooledRuns(nil)) != 0 || s.draining.Load() {
		t.Fatal("completed epoch was not retired as idle")
	}
}

func TestPooledRunQualificationAndAbort(t *testing.T) {
	s, c := pooledOwnerServer(t)
	if _, err := s.OpenPooledRun(context.Background(), "owner", "epoch", "execution", "group", javarunner.HostBudget{}, javarunner.PooledSelection{}); err == nil {
		t.Fatal("unqualified selection admitted")
	}
	r := openOwnedRun(t, s, "group", pooledProvisionFunc(func(context.Context, javarunner.HostAllocation) (javarunner.ManagedHost, error) {
		return nil, javarunner.ErrHostNotStarted
	}))
	a, err := r.Reserve(context.Background(), 2, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Abort(context.Background(), a[0].ReservationID); err != nil {
		t.Fatal(err)
	}
	if c.Snapshot() != (javarunner.HostUsage{}) {
		t.Fatal("aborted reservation charged", c.Snapshot())
	}
}

func TestPooledRunDelayedCreationRemainsOwnedDuringTakeover(t *testing.T) {
	s, c := pooledOwnerServer(t)
	entered, cancelled, proceed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(proceed) })
	h := &ownedTestHost{done: make(chan struct{})}
	r := openOwnedRun(t, s, "group", pooledProvisionFunc(func(ctx context.Context, _ javarunner.HostAllocation) (javarunner.ManagedHost, error) {
		close(entered)
		<-ctx.Done()
		close(cancelled)
		<-proceed
		return h, nil
	}))
	a, err := r.Reserve(context.Background(), 2, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]map[string]string{}
	for _, slot := range a[0].SlotIDs {
		values[slot] = map[string]string{}
	}
	prepared := make(chan error, 1)
	go func() { prepared <- r.Prepare(context.Background(), a[0].ReservationID, values) }()
	<-entered
	claimed := make(chan *httptest.ResponseRecorder, 1)
	go func() { claimed <- claimReplacement(s) }()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("takeover waited before cancelling provisioning")
	}
	if c.Snapshot() != (javarunner.HostUsage{Hosts: 1, VUsers: 2, MemoryBytes: 100}) {
		t.Fatal("in-flight creation released", c.Snapshot())
	}
	select {
	case <-claimed:
		t.Fatal("takeover completed before creation resolved")
	default:
	}
	once.Do(func() { close(proceed) })
	if err := <-prepared; err == nil {
		t.Fatal("late preparation succeeded")
	}
	w := <-claimed
	if w.Code != http.StatusOK || h.stops.Load() != 1 || c.Snapshot() != (javarunner.HostUsage{}) {
		t.Fatalf("late host leaked: response=%d stops=%d usage=%+v", w.Code, h.stops.Load(), c.Snapshot())
	}
}

func TestPooledRunPublicationAndShutdownAreAtomic(t *testing.T) {
	for range 50 {
		s, _ := pooledOwnerServer(t)
		start := make(chan struct{})
		var run *PooledRun
		var opened, closed error
		var wg sync.WaitGroup
		wg.Go(func() {
			<-start
			run, opened = s.openPooledRun(context.Background(), "owner", "epoch", "execution", "group", javarunner.HostBudget{MaxHosts: 2, MaxVUsers: 4, MaxMemoryBytes: 200}, strings.Repeat("a", 64), javarunner.HostProfile{SlotsPerHost: 2, MemoryBytesPerHost: 100}, pooledProvisionFunc(func(context.Context, javarunner.HostAllocation) (javarunner.ManagedHost, error) {
				return nil, javarunner.ErrHostNotStarted
			}))
		})
		wg.Go(func() {
			<-start
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			closed = s.Shutdown(ctx)
		})
		close(start)
		wg.Wait()
		if closed != nil {
			t.Fatal(closed)
		}
		if opened != nil && !errors.Is(opened, javarunner.ErrHostFence) {
			t.Fatal(opened)
		}
		if run != nil {
			select {
			case <-run.closed:
			default:
				_ = run.Close(context.Background())
				t.Fatal("shutdown missed published run")
			}
		}
	}
}

func TestPooledRunConcurrentClaimsHaveOneWinner(t *testing.T) {
	s, _ := pooledOwnerServer(t)
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			_, err := s.openPooledRun(context.Background(), "owner", "epoch", "execution", "group", javarunner.HostBudget{MaxHosts: 2, MaxVUsers: 4, MaxMemoryBytes: 200}, strings.Repeat("a", 64), javarunner.HostProfile{SlotsPerHost: 2, MemoryBytesPerHost: 100}, pooledProvisionFunc(func(context.Context, javarunner.HostAllocation) (javarunner.ManagedHost, error) {
				return nil, javarunner.ErrHostNotStarted
			}))
			if err == nil {
				wins.Add(1)
			} else if !errors.Is(err, javarunner.ErrHostIdentity) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("claims admitted %d copies", wins.Load())
	}
}

func TestPooledRunCloseSlotReleasesOnlyOccupancy(t *testing.T) {
	s, c := pooledOwnerServer(t)
	h := &ownedTestHost{done: make(chan struct{})}
	r := openOwnedRun(t, s, "group", pooledProvisionFunc(func(context.Context, javarunner.HostAllocation) (javarunner.ManagedHost, error) { return h, nil }))
	a := prepareOwnedRun(t, r)
	if err := r.CloseSlot(context.Background(), a.ID, a.SlotIDs[0]); err != nil {
		t.Fatal(err)
	}
	if c.Snapshot() != (javarunner.HostUsage{Hosts: 1, VUsers: 1, MemoryBytes: 100}) {
		t.Fatal(c.Snapshot())
	}
	if _, err := r.Invoke(context.Background(), a.ID, a.SlotIDs[1], "action", nil); err != nil {
		t.Fatal("peer interrupted", err)
	}
}

func TestPooledRunTakeoverCannotPublishDuringShutdown(t *testing.T) {
	s, _ := pooledOwnerServer(t)
	h := &ownedTestHost{done: make(chan struct{}), onTerminate: s.startShutdown}
	r := openOwnedRun(t, s, "group", pooledProvisionFunc(func(context.Context, javarunner.HostAllocation) (javarunner.ManagedHost, error) { return h, nil }))
	prepareOwnedRun(t, r)
	w := claimReplacement(s)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("shutdown takeover acknowledged: %d %s", w.Code, w.Body.String())
	}
	s.ownerMu.Lock()
	ready := s.owner.ready
	s.ownerMu.Unlock()
	if ready {
		t.Fatal("replacement owner marked ready during shutdown")
	}
}

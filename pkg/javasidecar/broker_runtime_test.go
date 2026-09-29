package javasidecar

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
	"github.com/jasonczech/apexload/pkg/javarunner/broker"
)

func managedBrokerFixture(t *testing.T) (*BrokerRuntime, *BrokerFacade, *facadeRuntime, *broker.Store, string) {
	t.Helper()
	return managedBrokerFixtureBlocked(t, false)
}
func managedBrokerFixtureBlocked(t *testing.T, block bool) (*BrokerRuntime, *BrokerFacade, *facadeRuntime, *broker.Store, string) {
	t.Helper()
	remote, runtime, store, epoch := facadeBrokerFixture(t, block)
	token := strings.Repeat("x", 32)
	facade, err := NewBrokerFacade(t.Context(), remote, token)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(facade)
	client, err := NewBrokerFacadeClient(server.URL, token, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewBrokerRuntime(t.Context(), client)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close(); server.Close(); facade.Close() })
	return manager, facade, runtime, store, epoch
}
func managedPreparation(r *BrokerRuntime, group string) BrokerPreparation {
	return BrokerPreparation{ExecutionID: "execution", GroupID: group, Profile: r.Capabilities().Profiles[0], PreparationTimeout: 5 * time.Second, RunLifetime: time.Minute, Sessions: []javarunner.DedicatedHostContext{{Contract: javarunner.DedicatedHostContextContract, SessionID: "session", VUserID: 1, GroupName: group, ScenarioID: "execution"}}}
}
func TestBrokerRuntimeIndependentGroups(t *testing.T) {
	r, _, runtime, store, epoch := managedBrokerFixture(t)
	var groups [2]*PreparedBrokerRun
	var failures [2]error
	var wg sync.WaitGroup
	for i, group := range []string{"first", "second"} {
		wg.Add(1)
		go func() { defer wg.Done(); groups[i], failures[i] = r.Prepare(t.Context(), managedPreparation(r, group)) }()
	}
	wg.Wait()
	for _, err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	if runtime.calls.Load() != 0 {
		t.Fatal("preparation executed user code")
	}
	if _, err := groups[0].Activate(t.Context(), "session"); err == nil {
		t.Fatal("activated without commit")
	}
	if err := groups[0].Close(); err != nil {
		t.Fatal(err)
	}
	if err := groups[1].Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	session, err := groups[1].Activate(t.Context(), "session")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = session.Execute(t.Context(), "init", nil); err != nil {
		t.Fatal("peer group invalidated", err)
	}
	if r.Err() != nil {
		t.Fatal("group close released owner", r.Err())
	}
	if err := groups[1].Close(); err != nil {
		t.Fatal(err)
	}
	awaitManagedQuarantine(t, store, epoch, 2)
	// Quarantine remains authoritative even after group close.
	if _, err = r.Prepare(t.Context(), managedPreparation(r, "third")); err == nil {
		t.Fatal("quarantine capacity reused")
	}
}
func TestBrokerRuntimePreparedDeadlineCannotBeRevived(t *testing.T) {
	r, _, runtime, _, _ := managedBrokerFixture(t)
	o := managedPreparation(r, "group")
	o.PreparationTimeout = 150 * time.Millisecond
	group, err := r.Prepare(t.Context(), o)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-group.Done():
	case <-time.After(time.Second):
		t.Fatal("preparation did not expire")
	}
	if !errors.Is(group.Err(), context.DeadlineExceeded) {
		t.Fatal(group.Err())
	}
	if err = group.Commit(t.Context()); err == nil {
		t.Fatal("committed expired group")
	}
	if _, err = group.Activate(t.Context(), "session"); err == nil {
		t.Fatal("activated expired group")
	}
	if runtime.calls.Load() != 0 {
		t.Fatal("expired preparation ran code")
	}
	if r.Err() != nil {
		t.Fatal("group deadline stopped owner")
	}
}
func TestBrokerRuntimeCommitReplacesOnlyPreparationDeadline(t *testing.T) {
	r, _, _, _, _ := managedBrokerFixture(t)
	o := managedPreparation(r, "group")
	o.PreparationTimeout = 200 * time.Millisecond
	o.RunLifetime = 500 * time.Millisecond
	group, err := r.Prepare(t.Context(), o)
	if err != nil {
		t.Fatal(err)
	}
	if err = group.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = group.Commit(t.Context()); err == nil {
		t.Fatal("commit replay extended lifetime")
	}
	time.Sleep(250 * time.Millisecond)
	if err = group.Err(); err != nil {
		t.Fatal("stale preparation timer", err)
	}
	select {
	case <-group.Done():
	case <-time.After(time.Second):
		t.Fatal("execution deadline not enforced")
	}
	if !errors.Is(group.Err(), context.DeadlineExceeded) {
		t.Fatal(group.Err())
	}
}
func TestBrokerRuntimeOwnerCloseFencesAllGroups(t *testing.T) {
	r, _, _, store, epoch := managedBrokerFixture(t)
	first, err := r.Prepare(t.Context(), managedPreparation(r, "first"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.Prepare(t.Context(), managedPreparation(r, "second"))
	if err != nil {
		t.Fatal(err)
	}
	if err = first.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	session, err := first.Activate(t.Context(), "session")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := r.Close(); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if first.Err() == nil || second.Err() == nil {
		t.Fatal("owner shutdown left live groups")
	}
	if _, err = session.Execute(t.Context(), "init", nil); err == nil {
		t.Fatal("session survived owner")
	}
	if _, err = r.Prepare(t.Context(), managedPreparation(r, "third")); err == nil {
		t.Fatal("admission after close")
	}
	awaitManagedQuarantine(t, store, epoch, 2)
}
func TestBrokerRuntimeFrozenProfileAndCancelledPreparation(t *testing.T) {
	for _, kind := range []string{"drift", "cancelled", "ceiling"} {
		t.Run(kind, func(t *testing.T) {
			r, _, runtime, store, epoch := managedBrokerFixture(t)
			o := managedPreparation(r, "group")
			caps := r.Capabilities()
			caps.Profiles[0].PolicySHA256 = "mutated"
			if r.Capabilities().Profiles[0].PolicySHA256 == "mutated" {
				t.Fatal("mutable capabilities")
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch kind {
			case "drift":
				o.Profile.PolicySHA256 = strings.Repeat("b", 64)
			case "cancelled":
				cancel()
			case "ceiling":
				o.Sessions = append(o.Sessions, o.Sessions[0], o.Sessions[0])
			}
			if _, err := r.Prepare(ctx, o); err == nil {
				t.Fatal("invalid preparation accepted")
			}
			records, _, err := store.Snapshot(t.Context(), epoch)
			if err != nil || len(records) != 0 || runtime.calls.Load() != 0 {
				t.Fatal(records, err)
			}
		})
	}
}
func TestBrokerRuntimeDoesNotRenewAbandonedActiveVuser(t *testing.T) {
	r, f, _, _, _ := managedBrokerFixture(t)
	group, err := r.Prepare(t.Context(), managedPreparation(r, "group"))
	if err != nil {
		t.Fatal(err)
	}
	if err = group.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	session, err := group.Activate(t.Context(), "session")
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.runs[group.run.id].sessions[session.id].leaseUntil = time.Now().Add(-time.Second)
	f.mu.Unlock()
	if err = group.run.Renew(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err = session.Execute(t.Context(), "init", nil); err == nil {
		t.Fatal("shared owner renewed abandoned Vuser")
	}
}

func awaitManagedQuarantine(t *testing.T, store *broker.Store, epoch string, count int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		records, _, err := store.Snapshot(t.Context(), epoch)
		if err != nil || len(records) != count {
			t.Fatal(records, err)
		}
		ready := true
		for _, record := range records {
			if record.State == javarunner.HostReaped {
				t.Fatal("cleanup falsely released reservation")
			}
			ready = ready && record.State == javarunner.HostQuarantined
		}
		if ready {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("cleanup did not quarantine", records)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestBrokerRuntimeRenewsPreparedGroupsIndependently(t *testing.T) {
	r, f, _, _, _ := managedBrokerFixture(t)
	first, err := r.Prepare(t.Context(), managedPreparation(r, "first"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.Prepare(t.Context(), managedPreparation(r, "second"))
	if err != nil {
		t.Fatal(err)
	}
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
	// Wait for real owner and pending-host renewal, then verify the peer still
	// commits. Closing the first group must not cancel either independent lane.
	deadline := time.Now().Add(4 * time.Second)
	for {
		f.mu.Lock()
		owner := f.ownerSequence
		run := f.runs[second.run.id].renewSequence
		f.mu.Unlock()
		if owner > 0 && run > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("renewal did not progress", owner, run)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = second.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestBrokerRuntimeCancelledInvocationDoesNotFencePeerGroup(t *testing.T) {
	r, _, runtime, _, _ := managedBrokerFixtureBlocked(t, true)
	first, err := r.Prepare(t.Context(), managedPreparation(r, "first"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.Prepare(t.Context(), managedPreparation(r, "second"))
	if err != nil {
		t.Fatal(err)
	}
	if err = first.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = second.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	a, err := first.Activate(t.Context(), "session")
	if err != nil {
		t.Fatal(err)
	}
	b, err := second.Activate(t.Context(), "session")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Execute(t.Context(), "init", nil); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Execute(t.Context(), "init", nil); err != nil {
		t.Fatal(err)
	}
	call, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, e := a.Execute(call, "action", nil); done <- e }()
	select {
	case <-runtime.entered:
	case <-time.After(time.Second):
		t.Fatal("invocation not entered")
	}
	cancel()
	select {
	case err = <-done:
		if err == nil {
			t.Fatal("cancelled result accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("cancel blocked")
	}
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Execute(t.Context(), "end", nil); err != nil {
		t.Fatal("cancelled invocation fenced peer", err)
	}
	if err = r.client.Renew(t.Context()); err != nil {
		t.Fatal("owner unusable", err)
	}
}

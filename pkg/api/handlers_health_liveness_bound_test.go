package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestHealthBypassBudgetIsDerivedFromTheBackstopBudget pins the pairing the
// run-driver panic backstop documents: the liveness bypass exists so the kubelet
// cannot restart the replica before the backstop has finished riding out a
// stalled store, so it must tolerate exactly as long as the backstop does and no
// longer. If either side is retuned, both move together.
func TestHealthBypassBudgetIsDerivedFromTheBackstopBudget(t *testing.T) {
	if got, want := healthCheckDurableWriteBypassBudget(), 4*time.Minute+30*time.Second; got != want {
		t.Fatalf("shipped liveness bypass budget = %s, want %s ((1+8)*30s, the backstop's own tolerance)", got, want)
	}
	// The whole point of the bypass is to outlast the kubelet's ~43s
	// (3 x periodSeconds 20 + timeoutSeconds 3) restart math.
	if got := healthCheckDurableWriteBypassBudget(); got <= 43*time.Second {
		t.Fatalf("liveness bypass budget = %s, want longer than the kubelet's ~43s restart window", got)
	}

	previousTimeout := runTestPanicRecoveryLockTimeout
	previousExtensions := runTestPanicRecoveryDurableWriteExtensions
	t.Cleanup(func() {
		runTestPanicRecoveryLockTimeout = previousTimeout
		runTestPanicRecoveryDurableWriteExtensions = previousExtensions
	})
	runTestPanicRecoveryLockTimeout = 10 * time.Second
	runTestPanicRecoveryDurableWriteExtensions = 2
	if got, want := healthCheckDurableWriteBypassBudget(), 30*time.Second; got != want {
		t.Fatalf("liveness bypass budget after retuning the backstop = %s, want %s; the two must stay in step", got, want)
	}
}

// TestHealthProbeStopsServingTheCachedSnapshotOnceTheBypassBudgetExpires is the
// bound the bypass shipped without.
//
// The bypass rides out a stalled durable write, and a failover eventually
// returns. A black-holed connection does not: the runner's durable writes take
// no context and the SQL stores set no statement timeout, so the write never
// returns, the in-flight gauge stays above zero for the life of the process and
// tr.mu is never released. Every start, stop, list and scale is then wedged
// forever while an unbounded bypass keeps answering 200 from a snapshot
// published before the stall, so the kubelet never restarts the container and
// the process never gets a fresh connection. Past the budget the handler must go
// back to the live read - it blocks, the probe fails, the container restarts -
// which is exactly what happened before the bypass existed.
func TestHealthProbeStopsServingTheCachedSnapshotOnceTheBypassBudgetExpires(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	store := newSlowRecordPersistence()
	t.Cleanup(store.releaseAll)
	server.runner.testStore = store

	if warm := probeHealth(t, server); warm.Code != http.StatusOK {
		t.Fatalf("warm-up probe status = %d, want %d body=%s", warm.Code, http.StatusOK, warm.Body.String())
	}
	published := server.healthSnapshot.Load()
	if published == nil {
		t.Fatal("the warm-up probe published no snapshot to age")
	}
	// Age the published copy past the budget instead of waiting minutes for it.
	// This is the state a controller reaches when the store call never returns:
	// no probe has read live state since the stall began.
	aged := *published
	aged.publishedAt = time.Now().Add(-healthCheckDurableWriteBypassBudget() - time.Minute)
	server.healthSnapshot.Store(&aged)

	unrelated := &ManagedTest{ID: "queued-run", Name: "queued-run", Status: "QUEUED"}
	server.runner.mu.Lock()
	server.runner.tests[unrelated.ID] = unrelated
	server.runner.mu.Unlock()
	stopped := make(chan error, 1)
	go func() { stopped <- server.runner.stopTest(unrelated.ID, false) }()
	select {
	case <-store.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the pre-start abort never reached the storage driver")
	}

	if snapshot, cached := server.healthCheckSnapshotWithoutRunnerLock(); cached {
		t.Fatalf("the bypass still served a snapshot published %s ago (budget %s) while a durable write that never returns held tr.mu; the wedged controller would report healthy for the life of the process and never be restarted",
			time.Since(snapshot.publishedAt).Round(time.Second), healthCheckDurableWriteBypassBudget())
	}

	probed := make(chan int, 1)
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/system/health", nil)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		probed <- resp.Code
	}()
	select {
	case code := <-probed:
		t.Fatalf("liveness probe answered %d from a snapshot older than the bypass budget; the kubelet would never restart the permanently wedged replica", code)
	case <-time.After(2 * time.Second):
		// Blocked on the live read behind tr.mu, which is what fails the probe and
		// gets the container restarted onto a fresh connection.
	}

	// A store that does drain must still be served normally afterwards.
	store.releaseAll()
	select {
	case code := <-probed:
		if code != http.StatusOK {
			t.Fatalf("liveness probe status after the store drained = %d, want %d", code, http.StatusOK)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the liveness probe never completed after the stalled write returned")
	}
	if err := <-stopped; err != nil {
		t.Fatalf("the stalled pre-start abort failed: %v", err)
	}
}

// TestHealthSnapshotIsRepublishedOnEveryLiveProbe keeps the age the bypass bounds
// meaningful: the stamp must be refreshed whenever the handler reads live runner
// state, or a healthy controller would drift into the expired branch and start
// blocking on tr.mu for no reason.
func TestHealthSnapshotIsRepublishedOnEveryLiveProbe(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	if warm := probeHealth(t, server); warm.Code != http.StatusOK {
		t.Fatalf("warm-up probe status = %d, want %d", warm.Code, http.StatusOK)
	}
	first := server.healthSnapshot.Load()
	if first == nil {
		t.Fatal("the warm-up probe published no snapshot")
	}
	if first.publishedAt.IsZero() {
		t.Fatal("the published snapshot carries no publication time, so its age cannot be bounded")
	}

	// Backdate the stamp; the next live probe must replace it with a fresh one.
	aged := *first
	aged.publishedAt = time.Now().Add(-time.Hour)
	server.healthSnapshot.Store(&aged)
	if next := probeHealth(t, server); next.Code != http.StatusOK {
		t.Fatalf("second probe status = %d, want %d", next.Code, http.StatusOK)
	}
	republished := server.healthSnapshot.Load()
	if republished == nil {
		t.Fatal("the second probe published no snapshot")
	}
	if age := time.Since(republished.publishedAt); age > time.Minute {
		t.Fatalf("snapshot age after a live probe = %s, want it republished; a stamp that is never refreshed expires the bypass on a healthy controller", age)
	}
}

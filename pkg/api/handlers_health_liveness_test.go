package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func probeHealth(t *testing.T, server *Server) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/health", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	return resp
}

// TestHealthProbeAnswersWhileADurableWriteHoldsTheRunnerLock proves the
// liveness endpoint does not queue behind the runner lock during a store
// failover.
//
// /api/v1/system/health is the livenessProbe in every shipped deployment, with
// periodSeconds 20, timeoutSeconds 3 and the default failureThreshold of 3, so
// a handler that blocks gets the container killed ~43s later. Both of its
// runner reads (GetSystemSettings, TestCount) take tr.mu, and the runner holds
// tr.mu across durable writes that take no context. Without this the run-driver
// panic backstop's (1+8)*30s durable-write extension - written to ride out a
// managed-database failover - could never be reached: the pod is restarted into
// the same unreachable store first, which is the crash-loop the extension
// exists to prevent.
func TestHealthProbeAnswersWhileADurableWriteHoldsTheRunnerLock(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	store := newSlowRecordPersistence()
	t.Cleanup(store.releaseAll)
	server.runner.testStore = store

	// A probe against a healthy controller publishes the snapshot the stalled
	// window is answered from. The kubelet issues one every 20s, so a controller
	// reaches this state long before it can stall a durable write.
	if warm := probeHealth(t, server); warm.Code != http.StatusOK {
		t.Fatalf("warm-up probe status = %d, want %d body=%s", warm.Code, http.StatusOK, warm.Body.String())
	}

	// An unrelated, perfectly healthy run is aborted before it started. Its
	// durable record write stalls inside the storage driver with tr.mu held -
	// exactly what a managed-Postgres failover does to a live controller.
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

	probed := make(chan int, 1)
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/system/health", nil)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		probed <- resp.Code
	}()
	select {
	case code := <-probed:
		if code != http.StatusOK {
			t.Fatalf("liveness probe status while a durable write held the runner lock = %d, want %d", code, http.StatusOK)
		}
	case <-time.After(3 * time.Second):
		// 3s is the shipped livenessProbe timeoutSeconds; three of these in a row
		// and the kubelet kills the container.
		t.Fatal("liveness probe blocked past its 3s timeout while a durable write held the runner lock; kubernetes would restart the replica mid-failover")
	}

	store.releaseAll()
	if err := <-stopped; err != nil {
		t.Fatalf("the stalled pre-start abort failed: %v", err)
	}

	// Once the store drains, the probe goes back to reading live runner state.
	live := probeHealth(t, server)
	if live.Code != http.StatusOK {
		t.Fatalf("post-failover probe status = %d, want %d", live.Code, http.StatusOK)
	}
	var payload struct {
		Status               string `json:"status"`
		TestCount            int    `json:"test_count"`
		CapacityAvailability struct {
			Source string `json:"source"`
		} `json:"capacity_availability"`
	}
	if err := json.Unmarshal(live.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal health payload: %v", err)
	}
	if payload.Status != "healthy" {
		t.Fatalf("health status = %q, want healthy", payload.Status)
	}
	if payload.TestCount != 1 {
		t.Fatalf("test_count once the lock is free = %d, want the live count 1", payload.TestCount)
	}
	if payload.CapacityAvailability.Source == "" {
		t.Fatalf("health payload lost its capacity availability block: %s", live.Body.String())
	}
}

// TestHealthProbeReadsLiveStateWithNoDurableWriteInFlight pins the other half of
// the contract: the cached answer is only for the stalled-store window. With no
// durable write in flight the handler still reads live runner state, so a mutex
// a panic genuinely orphaned still hangs the probe and still gets the replica
// restarted - the backstop's own fail-fast depends on that.
func TestHealthProbeReadsLiveStateWithNoDurableWriteInFlight(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	if warm := probeHealth(t, server); warm.Code != http.StatusOK {
		t.Fatalf("warm-up probe status = %d, want %d", warm.Code, http.StatusOK)
	}

	server.runner.mu.Lock()
	server.runner.tests["later-run"] = &ManagedTest{ID: "later-run", Status: "QUEUED"}
	server.runner.mu.Unlock()

	resp := probeHealth(t, server)
	var payload struct {
		TestCount int `json:"test_count"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal health payload: %v", err)
	}
	if payload.TestCount != 1 {
		t.Fatalf("test_count = %d, want the live count 1; the probe served a stale snapshot with no durable write in flight", payload.TestCount)
	}
}

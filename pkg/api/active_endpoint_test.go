package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

type fakeExecutionEndpointPublisher struct {
	mu           sync.Mutex
	publishCalls int
	clearCalls   int
	publishErr   error
	clearErr     error
	publishOwner string
	publishGen   uint64
	clearOwner   string
	clearGen     uint64
}

func (p *fakeExecutionEndpointPublisher) Publish(_ context.Context, ownerID string, generation uint64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.publishCalls++
	p.publishOwner = ownerID
	p.publishGen = generation
	return p.publishErr
}

func (p *fakeExecutionEndpointPublisher) Clear(_ context.Context, ownerID string, generation uint64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clearCalls++
	p.clearOwner = ownerID
	p.clearGen = generation
	return p.clearErr
}

func (p *fakeExecutionEndpointPublisher) state() (publishes, clears int, publishOwner string, publishGen uint64, clearOwner string, clearGen uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.publishCalls, p.clearCalls, p.publishOwner, p.publishGen, p.clearOwner, p.clearGen
}

func TestExecutionReadinessIsReadyInSingleControllerMode(t *testing.T) {
	server := NewServer(ServerConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/execution-readiness", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", resp.Code, resp.Body.String())
	}
}

func TestExecutionReadinessRejectsFollowerWithoutAffectingOrdinaryReadiness(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	publisher := &fakeExecutionEndpointPublisher{}
	server := NewServer(ServerConfig{
		Store:                   store,
		HAExecutionLeadership:   true,
		ActiveEndpointPublisher: publisher,
	})

	executionReq := httptest.NewRequest(http.MethodGet, "/api/v1/system/execution-readiness", nil)
	executionResp := httptest.NewRecorder()
	server.mux.ServeHTTP(executionResp, executionReq)
	if executionResp.Code != http.StatusServiceUnavailable {
		t.Fatalf("execution readiness status = %d, want 503 body=%s", executionResp.Code, executionResp.Body.String())
	}

	// The ordinary readiness contract intentionally remains independent from
	// execution leadership so followers can continue serving read traffic.
	if got := server.schedulerReadiness().Status; got == readinessStatusNotReady {
		t.Fatalf("ordinary scheduler readiness became not_ready on follower")
	}
}

func TestExecutionLeaderPublishesEndpointBeforeExecutionReadinessPasses(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	publisher := &fakeExecutionEndpointPublisher{}
	server := NewServer(ServerConfig{
		Store:                   store,
		HAExecutionLeadership:   true,
		ActiveEndpointPublisher: publisher,
	})
	server.setBoundAddr("127.0.0.1:8080")

	server.leadership.tick()
	deadline := time.Now().Add(3 * time.Second)
	for {
		publishes, _, _, _, _, _ := publisher.state()
		if server.isExecutionLeader() && publishes > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("leader was not admitted/published: leader=%v publishes=%d", server.isExecutionLeader(), publishes)
		}
		time.Sleep(10 * time.Millisecond)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/execution-readiness", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", resp.Code, resp.Body.String())
	}

	server.leadership.relinquish()
	_, clears, publishOwner, publishGen, clearOwner, clearGen := publisher.state()
	if clears == 0 {
		t.Fatal("leadership loss did not clear the active endpoint")
	}
	if publishOwner == "" || publishGen == 0 || clearOwner != publishOwner || clearGen != publishGen {
		t.Fatalf("published term %q/%d, cleared term %q/%d", publishOwner, publishGen, clearOwner, clearGen)
	}
}

func TestExecutionReadinessFailsClosedWhenEndpointPublishFails(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	publisher := &fakeExecutionEndpointPublisher{publishErr: errors.New("Kubernetes API unavailable")}
	server := NewServer(ServerConfig{
		Store:                   store,
		HAExecutionLeadership:   true,
		ActiveEndpointPublisher: publisher,
	})
	server.setBoundAddr("127.0.0.1:8080")

	server.leadership.tick()
	deadline := time.Now().Add(3 * time.Second)
	for {
		publishes, _, _, _, _, _ := publisher.state()
		if server.isExecutionLeader() && publishes > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("leader admission/publish attempt did not complete: leader=%v publishes=%d", server.isExecutionLeader(), publishes)
		}
		time.Sleep(10 * time.Millisecond)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/execution-readiness", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 body=%s", resp.Code, resp.Body.String())
	}
	server.leadership.relinquish()
}

func TestExecutionEndpointIsNotPublishedBeforeHTTPListenerBinds(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	publisher := &fakeExecutionEndpointPublisher{}
	server := NewServer(ServerConfig{
		Store:                   store,
		HAExecutionLeadership:   true,
		ActiveEndpointPublisher: publisher,
	})

	server.leadership.tick()
	deadline := time.Now().Add(3 * time.Second)
	for !server.isExecutionLeader() {
		if time.Now().After(deadline) {
			t.Fatal("leader was not admitted")
		}
		time.Sleep(10 * time.Millisecond)
	}
	publishes, _, _, _, _, _ := publisher.state()
	if publishes != 0 {
		t.Fatalf("publish calls before listener bind = %d, want 0", publishes)
	}

	server.setBoundAddr("127.0.0.1:8080")
	server.publishActiveExecutionEndpoint()
	publishes, _, ownerID, generation, _, _ := publisher.state()
	if publishes != 1 || ownerID == "" || generation == 0 {
		t.Fatalf("publish after bind = calls %d term %q/%d", publishes, ownerID, generation)
	}
	server.leadership.relinquish()
}

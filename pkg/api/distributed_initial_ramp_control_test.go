package api

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

type initialRampRejectingGeneratorClient struct {
	raceFakeGeneratorClient
	calls int
}

func (client *initialRampRejectingGeneratorClient) AdjustVUsers(context.Context, generatorrpc.VUserAdjustment) (generatorrpc.AdjustmentResult, error) {
	client.calls++
	return generatorrpc.AdjustmentResult{}, fmt.Errorf("generator scale rejected: %w", engine.ErrInitialRampInProgress)
}

func TestDistributedInitialRampRejectionRemainsConflictWithoutCompensation(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	backend := newDistributedExecutionBackend(nil, 50*time.Millisecond, nil)
	client := &initialRampRejectingGeneratorClient{}
	startedAt := time.Now().UTC().Add(-time.Second)
	testRun := &ManagedTest{
		ID:                      "distributed-initial-ramp",
		Name:                    "distributed initial ramp",
		Status:                  "RUNNING",
		ProjectID:               storage.DefaultProjectID,
		Duration:                time.Minute,
		DurationStr:             time.Minute.String(),
		Groups:                  []TestGroup{{Name: "buyers", VUsers: 1}},
		TotalVUsers:             1,
		StartedAt:               &startedAt,
		executionBackend:        backend,
		remoteActivated:         true,
		remoteClient:            client,
		remoteTarget:            "generator-a",
		remoteControlGeneration: 1,
	}
	server.runner.tests[testRun.ID] = testRun

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/tests/"+testRun.ID+"/vusers", bytes.NewBufferString(`{"group":"buyers","target_vusers":2}`))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, req)

	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d body=%s", response.Code, http.StatusConflict, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), engine.ErrInitialRampInProgress.Error()) {
		t.Fatalf("body = %q, want initial-ramp retry signal", response.Body.String())
	}
	if client.calls != 1 {
		t.Fatalf("AdjustVUsers calls = %d, want one rejected forward call and no compensation", client.calls)
	}
	if testRun.Status != "RUNNING" || testRun.TotalVUsers != 1 || testRun.Groups[0].VUsers != 1 {
		t.Fatalf("rejected scale mutated run: status=%s total=%d groups=%#v", testRun.Status, testRun.TotalVUsers, testRun.Groups)
	}
}

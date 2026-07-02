package api

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
)

type evacuationFakeGeneratorClient struct {
	target string
	mu     sync.Mutex
	adjust map[string]int32
	stops  int
}

func newEvacuationFakeGeneratorClient(target string) *evacuationFakeGeneratorClient {
	return &evacuationFakeGeneratorClient{target: target, adjust: make(map[string]int32)}
}

func (c *evacuationFakeGeneratorClient) DistributeScript(context.Context, generatorrpc.Script) (generatorrpc.ScriptResult, error) {
	return generatorrpc.ScriptResult{}, nil
}
func (c *evacuationFakeGeneratorClient) AssignWork(context.Context, generatorrpc.Assignment) (generatorrpc.AssignmentResult, error) {
	return generatorrpc.AssignmentResult{Accepted: true}, nil
}
func (c *evacuationFakeGeneratorClient) AdjustVUsers(_ context.Context, adjustment generatorrpc.VUserAdjustment) (generatorrpc.AdjustmentResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.adjust[adjustment.GroupName] = adjustment.TargetVUsers
	return generatorrpc.AdjustmentResult{Accepted: true, CurrentVUsers: adjustment.TargetVUsers}, nil
}
func (c *evacuationFakeGeneratorClient) AdjustDuration(context.Context, generatorrpc.DurationAdjustment) (generatorrpc.DurationResult, error) {
	return generatorrpc.DurationResult{Accepted: true}, nil
}
func (c *evacuationFakeGeneratorClient) OpenMetricsStream(context.Context) (*generatorrpc.MetricsStream, error) {
	return nil, errors.New("metrics stream unavailable in test")
}
func (c *evacuationFakeGeneratorClient) OpenHeartbeat(context.Context) (*generatorrpc.HeartbeatStream, error) {
	return nil, errors.New("heartbeat unavailable in test")
}
func (c *evacuationFakeGeneratorClient) StopTest(context.Context, string, bool) (generatorrpc.StopResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stops++
	return generatorrpc.StopResult{Acknowledged: true}, nil
}
func (c *evacuationFakeGeneratorClient) GetArchiveArtifact(context.Context, generatorrpc.ArchiveArtifactRequest) ([]byte, error) {
	return nil, nil
}
func (c *evacuationFakeGeneratorClient) Close() error { return nil }

func (c *evacuationFakeGeneratorClient) adjusted(group string) int32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.adjust[group]
}

func (c *evacuationFakeGeneratorClient) stopCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stops
}

func TestDistributedEvacuateGeneratorReshardsStatelessGroups(t *testing.T) {
	backend := newDistributedExecutionBackend([]string{"gen-a:9090", "gen-b:9090", "gen-c:9090"}, time.Second, nil)
	clients := map[string]*evacuationFakeGeneratorClient{
		"gen-a:9090": newEvacuationFakeGeneratorClient("gen-a:9090"),
		"gen-b:9090": newEvacuationFakeGeneratorClient("gen-b:9090"),
		"gen-c:9090": newEvacuationFakeGeneratorClient("gen-c:9090"),
	}
	test := &ManagedTest{
		ID:         "run-1",
		Name:       "Checkout",
		Status:     "RUNNING",
		Assignment: &GeneratorAssignmentSettings{TolerateGeneratorLoss: true},
		Groups:     []TestGroup{{Name: "buyers", Protocol: "http", VUsers: 9}},
		remoteGroupShards: map[string][]GeneratorShardSummary{
			"buyers": {
				{Group: "buyers", Target: "gen-a:9090", VUsers: 3},
				{Group: "buyers", Target: "gen-b:9090", VUsers: 3},
				{Group: "buyers", Target: "gen-c:9090", VUsers: 3},
			},
		},
		remoteClientsByTarget: map[string]generatorClient{
			"gen-a:9090": clients["gen-a:9090"],
			"gen-b:9090": clients["gen-b:9090"],
			"gen-c:9090": clients["gen-c:9090"],
		},
		remoteTargetStates: map[string]string{
			"gen-a:9090": "RUNNING",
			"gen-b:9090": "RUNNING",
			"gen-c:9090": "RUNNING",
		},
	}
	runner := &TestRunner{tests: map[string]*ManagedTest{test.ID: test}}

	dryRun, err := backend.EvacuateGenerator(runner, test, "gen-a:9090", true, "dry_run")
	if err != nil {
		t.Fatalf("dry-run evacuate returned error: %v", err)
	}
	if len(dryRun) != 1 || dryRun[0].VUsers != 3 || len(dryRun[0].ToShards) != 2 {
		t.Fatalf("dry-run impacts = %+v, want one redistributed buyers shard", dryRun)
	}
	if got := clients["gen-b:9090"].adjusted("buyers") + clients["gen-c:9090"].adjusted("buyers"); got != 0 {
		t.Fatalf("dry-run adjusted survivors, got %d total", got)
	}

	impacts, err := backend.EvacuateGenerator(runner, test, "gen-a:9090", false, "operator_evacuate")
	if err != nil {
		t.Fatalf("apply evacuate returned error: %v", err)
	}
	if len(impacts) != 1 {
		t.Fatalf("impacts = %+v, want one", impacts)
	}
	if got := clients["gen-b:9090"].adjusted("buyers") + clients["gen-c:9090"].adjusted("buyers"); got != 9 {
		t.Fatalf("survivor adjusted VUsers = %d, want total 9", got)
	}
	if clients["gen-a:9090"].stopCount() != 1 {
		t.Fatalf("evacuated target stop count = %d, want 1", clients["gen-a:9090"].stopCount())
	}
	for _, shard := range test.remoteGroupShards["buyers"] {
		if shard.Target == "gen-a:9090" {
			t.Fatalf("evacuated target remains in shards: %+v", test.remoteGroupShards["buyers"])
		}
	}
	if _, ok := test.remoteClientsByTarget["gen-a:9090"]; ok {
		t.Fatalf("evacuated target remains in remote client map")
	}
}

func TestDistributedEvacuateGeneratorRefusesBrowserGroups(t *testing.T) {
	backend := newDistributedExecutionBackend([]string{"gen-a:9090", "gen-b:9090"}, time.Second, nil)
	clientA := newEvacuationFakeGeneratorClient("gen-a:9090")
	clientB := newEvacuationFakeGeneratorClient("gen-b:9090")
	test := &ManagedTest{
		ID:         "run-browser",
		Status:     "RUNNING",
		Assignment: &GeneratorAssignmentSettings{TolerateGeneratorLoss: true},
		Groups:     []TestGroup{{Name: "browser", Protocol: "browser", VUsers: 2}},
		remoteGroupShards: map[string][]GeneratorShardSummary{
			"browser": {
				{Group: "browser", Target: "gen-a:9090", VUsers: 1},
				{Group: "browser", Target: "gen-b:9090", VUsers: 1},
			},
		},
		remoteClientsByTarget: map[string]generatorClient{"gen-a:9090": clientA, "gen-b:9090": clientB},
		remoteTargetStates:    map[string]string{"gen-a:9090": "RUNNING", "gen-b:9090": "RUNNING"},
	}
	runner := &TestRunner{tests: map[string]*ManagedTest{test.ID: test}}

	impacts, err := backend.EvacuateGenerator(runner, test, "gen-a:9090", false, "operator_evacuate")
	if err == nil {
		t.Fatal("expected browser evacuation to be refused")
	}
	if len(impacts) != 1 || !impacts[0].Refused || impacts[0].RefusalReason == "" {
		t.Fatalf("impacts = %+v, want refused browser impact", impacts)
	}
	if got := clientB.adjusted("browser"); got != 0 {
		t.Fatalf("browser survivor adjusted = %d, want 0", got)
	}
}

func TestFailDistributedTestOnStreamErrorIgnoresEvacuatedTarget(t *testing.T) {
	backend := newDistributedExecutionBackend([]string{"gen-a:9090", "gen-b:9090"}, time.Second, nil)
	test := &ManagedTest{
		ID:                 "run-evacuated-stream",
		Status:             "RUNNING",
		remoteTargetStates: map[string]string{"gen-a:9090": "operator_evacuate", "gen-b:9090": "RUNNING"},
		remoteClientsByTarget: map[string]generatorClient{
			"gen-b:9090": newEvacuationFakeGeneratorClient("gen-b:9090"),
		},
	}
	runner := &TestRunner{tests: map[string]*ManagedTest{test.ID: test}}

	backend.failDistributedTestOnStreamError(runner, test, "gen-a:9090", errors.New("stream closed after evacuation"))

	if test.Status != "RUNNING" {
		t.Fatalf("status = %q, want RUNNING after evacuated target stream closes", test.Status)
	}
	if test.remoteFinalizing {
		t.Fatal("evacuated target stream close must not mark the test finalizing")
	}
	if test.remoteTargetStates["gen-a:9090"] != "operator_evacuate" {
		t.Fatalf("evacuated state overwritten: %#v", test.remoteTargetStates)
	}
}

package api

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
)

// raceFakeGeneratorClient is a no-op generatorClient for race tests; control calls
// succeed immediately so the test exercises the locking, not the gRPC behavior.
type raceFakeGeneratorClient struct{}

func (raceFakeGeneratorClient) DistributeScript(context.Context, generatorrpc.Script) (generatorrpc.ScriptResult, error) {
	return generatorrpc.ScriptResult{}, nil
}
func (raceFakeGeneratorClient) AssignWork(context.Context, generatorrpc.Assignment) (generatorrpc.AssignmentResult, error) {
	return generatorrpc.AssignmentResult{}, nil
}
func (raceFakeGeneratorClient) AdjustVUsers(context.Context, generatorrpc.VUserAdjustment) (generatorrpc.AdjustmentResult, error) {
	return generatorrpc.AdjustmentResult{Accepted: true}, nil
}
func (raceFakeGeneratorClient) AdjustDuration(context.Context, generatorrpc.DurationAdjustment) (generatorrpc.DurationResult, error) {
	return generatorrpc.DurationResult{Accepted: true}, nil
}
func (raceFakeGeneratorClient) OpenMetricsStream(context.Context) (*generatorrpc.MetricsStream, error) {
	return nil, nil
}
func (raceFakeGeneratorClient) OpenHeartbeat(context.Context) (*generatorrpc.HeartbeatStream, error) {
	return nil, nil
}
func (raceFakeGeneratorClient) StopTest(context.Context, string, bool) (generatorrpc.StopResult, error) {
	return generatorrpc.StopResult{Acknowledged: true}, nil
}
func (raceFakeGeneratorClient) GetArchiveArtifact(context.Context, generatorrpc.ArchiveArtifactRequest) ([]byte, error) {
	return nil, nil
}
func (raceFakeGeneratorClient) Close() error { return nil }

// TestDistributedControlMethodsRaceTeardown drives the distributed backend's control
// methods (which read the remote* maps) concurrently with closeRemoteResources (which
// nils those maps under tr.mu). Before the snapshot-under-lock fix, -race reports a
// data race on remoteClientsByTarget; after, every read is taken under tr.mu.
func TestDistributedControlMethodsRaceTeardown(t *testing.T) {
	fake := raceFakeGeneratorClient{}
	b := newDistributedExecutionBackend([]string{"gen-1"}, time.Second, func(context.Context, string) (generatorClient, error) {
		return fake, nil
	})
	tr := &TestRunner{}
	test := &ManagedTest{
		ID:     "race-test",
		Status: "RUNNING",
		Groups: []TestGroup{{Name: "g", VUsers: 1}},
	}
	populateLocked := func() {
		test.remoteClientsByTarget = map[string]generatorClient{"gen-1": fake}
		test.remoteGroupTargets = map[string]string{"g": "gen-1"}
		test.remoteGroupShards = map[string][]GeneratorShardSummary{"g": {{Group: "g", Target: "gen-1", VUsers: 1}}}
		test.remoteTargetStates = map[string]string{"gen-1": "RUNNING"}
		test.remoteTarget = "gen-1"
	}
	tr.mu.Lock()
	populateLocked()
	tr.mu.Unlock()

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// -race alone only proves the reads are synchronised; it says nothing about the
	// value each control call resolved. Every generator call in this test is wired to
	// succeed (raceFakeGeneratorClient) and the dialer always returns that client, so a
	// control call that loses the remote* maps to a concurrent teardown must still
	// resolve a client through remoteTarget and return nil. A non-nil error here means
	// the snapshot produced an unusable view of the topology — the wrong-result failure
	// mode that a data-race detector cannot see.
	var resultMu sync.Mutex
	calls := 0
	failures := map[string]error{}
	record := func(name string, err error) {
		resultMu.Lock()
		defer resultMu.Unlock()
		calls++
		if err != nil {
			if _, seen := failures[name]; !seen {
				failures[name] = err
			}
		}
	}

	// StopTest/AdjustDuration/stopRemainingRemoteTargets all read the remote* maps via
	// the snapshot helpers (clientsFromSnapshot + the remoteTargetStates copy). ScaleVUsers
	// and Pause/Resume use the identical snapshot pattern but need a live heartbeat/metrics
	// stream to reach their gRPC bodies, so they are exercised by the integration suites.
	readers := map[string]func() error{
		"StopTest":                   func() error { return b.StopTest(tr, test) },
		"AdjustDuration":             func() error { return b.AdjustDuration(tr, test, time.Second) },
		"stopRemainingRemoteTargets": func() error { return b.stopRemainingRemoteTargets(tr, test, true) },
	}
	for name, fn := range readers {
		name, fn := name, fn
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					record(name, fn())
				}
			}
		}()
	}

	// Mutator: closeRemoteResources nils the remote* maps under tr.mu, then repopulate
	// so the readers keep finding state to race against.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				b.closeRemoteResources(tr, test)
				tr.mu.Lock()
				populateLocked()
				tr.mu.Unlock()
			}
		}
	}()

	time.Sleep(300 * time.Millisecond)
	close(stop)
	wg.Wait()

	resultMu.Lock()
	defer resultMu.Unlock()
	if calls == 0 {
		t.Fatal("no distributed control calls ran against the concurrent teardown")
	}
	for name, err := range failures {
		t.Errorf("%s failed while the remote topology was being torn down and repopulated: %v", name, err)
	}
}

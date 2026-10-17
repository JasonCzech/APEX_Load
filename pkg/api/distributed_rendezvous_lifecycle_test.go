package api

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/distributed/rendezvous"
	"github.com/jasonczech/apexload/pkg/storage"
)

type rendezvousLifecycleClient struct {
	*generatorrpc.Client
	handler rendezvous.Client
	openErr error
	stream  *generatorrpc.MetricsStream
}

// Placement probes and the run share this fixture's connection; the real
// client's cleanup owns closing it after the run has released its resources.
func (c *rendezvousLifecycleClient) Close() error { return nil }

func (c *rendezvousLifecycleClient) OpenRendezvous(_ context.Context, _, _ string, _ uint64, _ []byte, handler rendezvous.Client) error {
	c.handler = handler
	return c.openErr
}

func (c *rendezvousLifecycleClient) OpenMetricsStreamForTestGeneration(ctx context.Context, id string, generation uint64) (*generatorrpc.MetricsStream, error) {
	if c.stream != nil {
		return c.stream, nil
	}
	return c.Client.OpenMetricsStreamForTestGeneration(ctx, id, generation)
}

func (c *rendezvousLifecycleClient) OpenMetricsStreamWithSetupTimeoutForTestGeneration(ctx context.Context, timeout time.Duration, id string, generation uint64) (*generatorrpc.MetricsStream, error) {
	if c.stream != nil {
		return c.stream, nil
	}
	return c.Client.OpenMetricsStreamWithSetupTimeoutForTestGeneration(ctx, timeout, id, generation)
}

func awaitRendezvousClosed(t *testing.T, runner *TestRunner, runID, group string, rt *runGroupRendezvous) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !rt.closed.Load() || runner.rendezvousRuntime(runID, group) == rt {
		if time.Now().After(deadline) {
			t.Fatal("run lifetime ended without releasing its rendezvous coordinator")
		}
		time.Sleep(time.Millisecond)
	}
}

func assertRendezvousServing(t *testing.T, client rendezvous.Client) {
	t.Helper()
	if client == nil {
		t.Fatal("rendezvous handler was not attached")
	}
	out, err := client.Exchange(context.Background(), rendezvous.Batch{Key: "checkout", Round: 1, Arrivals: []uint64{0}})
	if err != nil || out.State != "waiting" {
		t.Fatalf("active run rendezvous = %+v, %v; want waiting", out, err)
	}
}

func TestDistributedStartupRendezvousLifetime(t *testing.T) {
	for _, failChannel := range []bool{false, true} {
		name := "success"
		if failChannel {
			name = "channel_failure"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv(config.EnvArchiveDir, t.TempDir())
			store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "run.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			runner := NewTestRunner(1, NewHub(), store)
			base, _ := newTestGeneratorRPCClient(t, "rendezvous-generator", 10)
			client := &rendezvousLifecycleClient{Client: base}
			if failChannel {
				client.openErr = errors.New("channel refused")
			}
			backend := newDistributedExecutionBackend([]string{"rendezvous-generator"}, time.Second, func(context.Context, string) (generatorClient, error) { return client, nil })
			runner.setExecutionBackend(backend)
			group := rendezvousTestGroup()
			group.script.ID, group.script.Name, group.script.Protocol = "checkout-script", "Checkout", "http"
			group.ScriptID, group.ScriptName, group.Protocol = group.script.ID, group.script.Name, "http"
			ctx, cancel := context.WithCancel(context.Background())
			now := time.Now().UTC()
			run := &ManagedTest{ID: "startup-rendezvous-" + name, ProjectID: storage.DefaultProjectID, Name: "Startup", Status: "INITIALIZING", Groups: []TestGroup{group}, TotalVUsers: 4, Duration: time.Minute, DurationStr: "1m", CreatedAt: now, executionCtx: ctx, cancel: cancel}
			if err := store.SaveTest(&storage.TestRecord{ID: run.ID, ProjectID: run.ProjectID, Name: run.Name, Status: run.Status, StartTime: now, Groups: []any{}}); err != nil {
				t.Fatal(err)
			}
			runner.mu.Lock()
			runner.tests[run.ID] = run
			runner.mu.Unlock()
			t.Cleanup(func() {
				runner.mu.Lock()
				run.Status = "COMPLETE"
				runner.mu.Unlock()
				cancel()
				backend.closeRemoteResources(runner, run)
				runner.beginShutdown()
			})
			backend.Run(runner, run)
			if failChannel {
				if client.handler == nil {
					t.Fatalf("startup did not reach rendezvous channel: %s", run.Error)
				}
				if runner.rendezvousRuntime(run.ID, group.Name) != nil {
					t.Fatal("failed startup retained its coordinator")
				}
				return
			}
			runner.mu.RLock()
			active, message := run.Status == "RUNNING" && run.remoteActivated, run.Error
			runner.mu.RUnlock()
			if !active {
				t.Fatalf("startup did not activate: %s", message)
			}
			assertRendezvousServing(t, client.handler)
			rt := runner.rendezvousRuntime(run.ID, group.Name)
			// Graceful Stop cancels execution before terminal metrics arrive.
			cancel()
			assertRendezvousServing(t, client.handler)
			runner.mu.Lock()
			run.Status = "COMPLETE"
			runner.mu.Unlock()
			backend.closeRemoteResources(runner, run)
			awaitRendezvousClosed(t, runner, run.ID, group.Name, rt)
		})
	}
}

func TestAdoptedRendezvousLifetime(t *testing.T) {
	for _, failChannel := range []bool{false, true} {
		name := "success"
		if failChannel {
			name = "channel_failure"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv(config.EnvArchiveDir, t.TempDir())
			runner, store, _ := newAdoptionTestRunner(t)
			term, err := runner.captureExecutionLeaseTerm()
			if err != nil {
				t.Fatal(err)
			}
			group := rendezvousTestGroup()
			started := time.Now().UTC()
			runCtx, cancel := context.WithCancel(context.Background())
			run := &ManagedTest{ID: "adopted-rendezvous-" + name, Name: "Adopted", ProjectID: storage.DefaultProjectID, Status: "RUNNING", Groups: []TestGroup{group}, StartedAt: &started, Duration: time.Minute, executionCtx: runCtx, cancel: cancel, executionGeneration: term.generation}
			if err := store.SaveTest(&storage.TestRecord{ID: run.ID, ProjectID: run.ProjectID, Name: run.Name, Status: run.Status, StartTime: started, Groups: []any{}}); err != nil {
				t.Fatal(err)
			}
			shards := []GeneratorShardSummary{{Group: group.Name, Target: "gen-a:9091", VUsers: 4}}
			run.remoteGroupShards = map[string][]GeneratorShardSummary{group.Name: shards}
			runner.mu.Lock()
			runner.tests[run.ID] = run
			runner.mu.Unlock()
			backend := runner.execution.(*distributedExecutionBackend)
			t.Cleanup(cancel)
			if err := runner.prepareGroupRendezvous(context.Background(), run, group, shards, false, term); err != nil {
				t.Fatal(err)
			}
			_, stream := openScriptedMetricsStream(t, run.ID, term.generation)
			base, _ := newTestGeneratorRPCClient(t, "adopted-generator", 10)
			client := &rendezvousLifecycleClient{Client: base, stream: stream}
			if failChannel {
				client.openErr = errors.New("channel refused")
			}
			t.Cleanup(func() {
				runner.mu.Lock()
				run.Status = "COMPLETE"
				runner.mu.Unlock()
				cancel()
				backend.closeRemoteResources(runner, run)
			})
			run.adoptedResume = &adoptedRunResume{generation: term.generation, ownerID: term.ownerID, secured: true, adopted: map[string]generatorClient{"gen-a:9091": client}}
			runner.resumeAdoptedRun(run)
			if failChannel {
				if runner.rendezvousRuntime(run.ID, group.Name) != nil {
					t.Fatal("failed adoption retained its coordinator")
				}
				return
			}
			assertRendezvousServing(t, client.handler)
			rt := runner.rendezvousRuntime(run.ID, group.Name)
			runner.mu.Lock()
			run.Status = "COMPLETE"
			runner.mu.Unlock()
			backend.closeRemoteResources(runner, run)
			awaitRendezvousClosed(t, runner, run.ID, group.Name, rt)
		})
	}
}

func TestRendezvousLifetimeLossAndReplacement(t *testing.T) {
	runner, store, leadership := newAdoptionTestRunner(t)
	term, err := runner.captureExecutionLeaseTerm()
	if err != nil {
		t.Fatal(err)
	}
	group := rendezvousTestGroup()
	run := &ManagedTest{ID: "lifetime-rendezvous", ProjectID: storage.DefaultProjectID, Groups: []TestGroup{group}}
	if err := store.SaveTest(&storage.TestRecord{ID: run.ID, ProjectID: run.ProjectID, Name: "Lifetime", Status: "RUNNING", StartTime: time.Now().UTC(), Groups: []any{}}); err != nil {
		t.Fatal(err)
	}
	shards := []GeneratorShardSummary{{Group: group.Name, VUsers: 4}}
	if err := runner.prepareGroupRendezvous(context.Background(), run, group, shards, false, term); err != nil {
		t.Fatal(err)
	}
	old := runner.rendezvousRuntime(run.ID, group.Name)
	oldCtx, cancelOld := context.WithCancel(context.Background())
	defer cancelOld()
	runner.retainRunRendezvousUntil(oldCtx, run.ID, term)
	// Replace the registry entry before the old lifetime ends, so the old
	// callback must close its captured instance without touching the replacement.
	runner.rendezvousMu.Lock()
	delete(runner.rendezvousGroups, rendezvousRuntimeKey(run.ID, group.Name))
	runner.rendezvousMu.Unlock()
	if err := runner.restoreRendezvous(context.Background(), run, map[string][]GeneratorShardSummary{group.Name: shards}, term); err != nil {
		t.Fatal(err)
	}
	replacement := runner.rendezvousRuntime(run.ID, group.Name)
	ctx, cancel := runner.executionOperationContext(context.Background())
	defer cancel()
	runner.retainRunRendezvousUntil(ctx, run.ID, term)
	cancelOld()
	awaitRendezvousClosed(t, runner, run.ID, group.Name, old)
	assertRendezvousServing(t, runner.localRendezvousClient(run.ID, group.Name))
	leadership.relinquish()
	awaitRendezvousClosed(t, runner, run.ID, group.Name, replacement)
}

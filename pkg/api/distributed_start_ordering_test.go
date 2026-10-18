package api

import (
	"context"
	"net"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

type delayedFirstRunningStore struct {
	SQLiteStore *storage.SQLiteStore
	delay       time.Duration
	once        sync.Once
	endedAt     atomic.Int64
}

type delayedBarrierPersistenceStore struct {
	SQLiteStore  *storage.SQLiteStore
	mu           sync.Mutex
	runningSaves int
	delayCall    int
	delay        time.Duration
	delayedEnd   atomic.Int64
}

func (s *delayedBarrierPersistenceStore) SaveTest(record *storage.TestRecord) error {
	if record != nil && record.Status == "RUNNING" {
		s.mu.Lock()
		s.runningSaves++
		call := s.runningSaves
		s.mu.Unlock()
		if call == s.delayCall {
			time.Sleep(s.delay)
			s.delayedEnd.Store(time.Now().UnixNano())
		}
	}
	return s.SQLiteStore.SaveTest(record)
}

func (s *delayedFirstRunningStore) SaveTest(record *storage.TestRecord) error {
	if record != nil && record.Status == "RUNNING" {
		s.once.Do(func() {
			time.Sleep(s.delay)
			s.endedAt.Store(time.Now().UnixNano())
		})
	}
	return s.SQLiteStore.SaveTest(record)
}

type delayedSetupGeneratorClient struct {
	*generatorrpc.Client
	delay time.Duration
}

func (c *delayedSetupGeneratorClient) wait(ctx context.Context) error {
	timer := time.NewTimer(c.delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c *delayedSetupGeneratorClient) DistributeScript(ctx context.Context, script generatorrpc.Script) (generatorrpc.ScriptResult, error) {
	if err := c.wait(ctx); err != nil {
		return generatorrpc.ScriptResult{}, err
	}
	return c.Client.DistributeScript(ctx, script)
}

func (c *delayedSetupGeneratorClient) AssignWork(ctx context.Context, assignment generatorrpc.Assignment) (generatorrpc.AssignmentResult, error) {
	if err := c.wait(ctx); err != nil {
		return generatorrpc.AssignmentResult{}, err
	}
	return c.Client.AssignWork(ctx, assignment)
}

// A fixture owns the generator, transport, hub and asynchronous run driver.
// Its store remains caller-owned and must outlive all of these barriers.
type distributedOrderingHarness struct {
	runner        *TestRunner
	test          *ManagedTest
	service       *generatorrpc.Service
	server        *grpc.Server
	listener      *bufconn.Listener
	hub           *Hub
	runDone       chan struct{}
	serveDone     chan struct{}
	hubDone       chan struct{}
	generatorDone chan struct{}
	quiesceOnce   sync.Once
}

func (h *distributedOrderingHarness) close(t *testing.T) {
	t.Helper()
	h.quiesce(t)
}

func (h *distributedOrderingHarness) quiesce(t *testing.T) {
	t.Helper()
	h.quiesceOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if h.runner != nil {
			h.runner.beginShutdown()
			if h.test != nil {
				if err := h.runner.stopTestWithContextAndCIReasonAndActorMode(ctx, h.test.ID, false, "", testCreationActor{}, true); err != nil {
					t.Errorf("stop owned ordering run: %v", err)
				}
			}
		}
		waitOwnedOrderingBarrier(t, h.runDone, "run driver")
		if h.runner != nil && h.test != nil {
			// Terminal status precedes asynchronous archive teardown. Wait for
			// its retained transport ownership to retire, then join the exact
			// archive fence before the caller is allowed to close SQLite.
			waitOwnedOrderingCondition(t, func() bool {
				h.runner.mu.RLock()
				defer h.runner.mu.RUnlock()
				return isTerminalTestStatus(h.test.Status) && h.test.remoteLifecycleCtx == nil && len(h.test.remoteClientsByTarget) == 0 && h.test.remoteClient == nil
			}, "controller terminal cleanup")
			unlock := h.runner.lockArchiveCommitFence(h.test.ID)
			unlock()
		}
		if h.runner != nil {
			if err := h.runner.waitRuntimeOverrideSecretAttemptReconciliation(context.Background()); err != nil {
				t.Errorf("join owned ordering reconciliation: %v", err)
			}
			if h.runner.thresholdJournal != nil {
				if err := h.runner.thresholdJournal.shutdown(context.Background()); err != nil {
					t.Errorf("join owned ordering threshold journal: %v", err)
				}
			}
		}
		if err := h.service.Shutdown(context.Background()); err != nil {
			t.Errorf("join owned ordering generator: %v", err)
		}
		close(h.generatorDone)
		h.server.Stop()
		_ = h.listener.Close()
		waitOwnedOrderingBarrier(t, h.serveDone, "gRPC server")
		h.hub.Stop()
		waitOwnedOrderingBarrier(t, h.hubDone, "hub")
	})
}

func waitOwnedOrderingBarrier(t *testing.T, done <-chan struct{}, name string) {
	t.Helper()
	if done == nil {
		return
	}
	select {
	case <-done:
		return
	case <-time.After(10 * time.Second):
		stack := make([]byte, 1<<20)
		t.Errorf("owned ordering %s has not joined:\n%s", name, stack[:runtime.Stack(stack, true)])
	}
	// A diagnostic deadline is not permission to close the fixture's store
	// or transport while its worker still holds them.
	<-done
}

func waitOwnedOrderingCondition(t *testing.T, condition func() bool, name string) {
	t.Helper()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	timeout := timer.C
	for !condition() {
		select {
		case <-timeout:
			stack := make([]byte, 1<<20)
			t.Errorf("owned ordering %s has not retired:\n%s", name, stack[:runtime.Stack(stack, true)])
			timeout = nil
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func newDistributedOrderingHarness(t *testing.T, timeout time.Duration, store storage.Store, wrap func(*generatorrpc.Client) generatorClient) (*TestRunner, *ManagedTest) {
	t.Helper()
	harness := newOwnedDistributedOrderingHarness(t, timeout, store, wrap)
	return harness.runner, harness.test
}

func newOwnedDistributedOrderingHarness(t *testing.T, timeout time.Duration, store storage.Store, wrap func(*generatorrpc.Client) generatorClient) *distributedOrderingHarness {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	service := generatorrpc.NewService("ordering-generator", 20)
	pb.RegisterGeneratorControlServer(server, service)
	hub := NewHub()
	harness := &distributedOrderingHarness{service: service, server: server, listener: listener, hub: hub, serveDone: make(chan struct{}), hubDone: make(chan struct{}), generatorDone: make(chan struct{})}
	t.Cleanup(func() { harness.close(t) })
	go func() { defer close(harness.serveDone); _ = server.Serve(listener) }()
	go func() { defer close(harness.hubDone); hub.Run() }()

	runner := NewTestRunner(1, hub, store)
	harness.runner = runner
	runner.setExecutionBackend(newDistributedExecutionBackend([]string{"bufnet"}, timeout, func(ctx context.Context, target string) (generatorClient, error) {
		client, err := generatorrpc.Dial(ctx, target,
			grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		if err != nil {
			return nil, err
		}
		if wrap != nil {
			return wrap(client), nil
		}
		return client, nil
	}))
	runner.StoreScript(&dsl.Script{
		ID:       "ordering-script",
		Name:     "ordering-script",
		Protocol: "http",
		Config: dsl.ScriptConfig{
			BaseURL:  "http://127.0.0.1",
			RunLogic: dsl.RunLogicConfig{Iterations: 1},
		},
		Actions: []dsl.Action{{Name: "request", Type: "GET", Target: "/"}},
	})
	test, err := runner.CreateTest(CreateTestRequest{
		Name: "ordering-run", Duration: "2s",
		Groups: []CreateGroupEntry{{Name: "group", ScriptID: "ordering-script", VUsers: 1}},
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	runner.mu.RLock()
	harness.test = runner.tests[test.ID]
	runner.mu.RUnlock()
	// Use the same admission/driver as StartTest while owning its exact
	// goroutine barrier; keep the concrete distributed backend intact.
	prepared, err := runner.prepareTestForExecutionWithOptionsAndExpectedTerm(test.ID, StartTestOptions{}, nil)
	if err != nil || prepared == nil {
		t.Fatalf("prepare StartTest: prepared=%v error=%v", prepared != nil, err)
	}
	harness.test = prepared
	harness.runDone = make(chan struct{})
	go func() { defer close(harness.runDone); runner.runTest(prepared) }()
	return harness
}

func waitDistributedActivation(t *testing.T, runner *TestRunner, testID string) *ManagedTest {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		runner.mu.RLock()
		test := runner.tests[testID]
		activated := test != nil && test.remoteActivated
		terminal := test != nil && isTerminalTestStatus(test.Status)
		runner.mu.RUnlock()
		if activated {
			return test
		}
		if terminal {
			t.Fatalf("distributed startup became terminal: status=%s error=%s", test.Status, test.Error)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("distributed startup did not activate")
	return nil
}

func TestDistributedCommitBarrierIsChosenAfterSlowRunningPersistence(t *testing.T) {
	timeout := 400 * time.Millisecond
	baseStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = baseStore.Close() })
	store := &delayedFirstRunningStore{SQLiteStore: baseStore, delay: 800 * time.Millisecond}
	runner, test := newDistributedOrderingHarness(t, timeout, sqliteOverlay(store), nil)
	started := waitDistributedActivation(t, runner, test.ID)
	persistedAt := time.Unix(0, store.endedAt.Load())
	if persistedAt.IsZero() || started.StartedAt == nil {
		t.Fatalf("missing persistence/start timestamps: persisted=%s started=%v", persistedAt, started.StartedAt)
	}
	minimum := persistedAt.Add(distributedCommitStartLead(timeout) - 50*time.Millisecond)
	if started.StartedAt.Before(minimum) {
		t.Fatalf("coordinated start %s was chosen before slow RUNNING persistence ended at %s", started.StartedAt, persistedAt)
	}
}

func TestDistributedSetupRPCsReceiveIndependentTimeoutBudgets(t *testing.T) {
	// Keep each delayed RPC comfortably within its own budget while making the
	// two delays exceed one shared budget. The larger absolute margin keeps the
	// assertion meaningful under the race detector on loaded hosted runners.
	timeout := 1500 * time.Millisecond
	runner, test := newDistributedOrderingHarness(t, timeout, nil, func(client *generatorrpc.Client) generatorClient {
		return &delayedSetupGeneratorClient{Client: client, delay: 900 * time.Millisecond}
	})
	waitDistributedActivation(t, runner, test.ID)
}

func TestDistributedCommitBarrierIsRepersistedWhenSaveConsumesLead(t *testing.T) {
	timeout := 400 * time.Millisecond
	baseStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = baseStore.Close() })
	store := &delayedBarrierPersistenceStore{
		SQLiteStore: baseStore, delayCall: 2, delay: 700 * time.Millisecond,
	}
	runner, test := newDistributedOrderingHarness(t, timeout, sqliteOverlay(store), nil)
	started := waitDistributedActivation(t, runner, test.ID)
	store.mu.Lock()
	runningSaves := store.runningSaves
	store.mu.Unlock()
	if runningSaves < 3 {
		t.Fatalf("RUNNING saves = %d, want a refreshed barrier persistence", runningSaves)
	}
	delayedEnd := time.Unix(0, store.delayedEnd.Load())
	minimum := delayedEnd.Add(distributedCommitStartLead(timeout) - 50*time.Millisecond)
	if started.StartedAt == nil || started.StartedAt.Before(minimum) {
		t.Fatalf("refreshed start = %v, want after %s", started.StartedAt, minimum)
	}
}

type cleanupGatedOrderingClient struct {
	*generatorrpc.Client
	entered chan struct{}
}

func (c *cleanupGatedOrderingClient) DistributeScript(ctx context.Context, _ generatorrpc.Script) (generatorrpc.ScriptResult, error) {
	close(c.entered)
	<-ctx.Done()
	return generatorrpc.ScriptResult{}, ctx.Err()
}

func TestDistributedOrderingHarnessCleanupBeforeActivationJoinsDriver(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	entered := make(chan struct{})
	h := newOwnedDistributedOrderingHarness(t, 2*time.Second, store, func(client *generatorrpc.Client) generatorClient {
		return &cleanupGatedOrderingClient{Client: client, entered: entered}
	})
	// Explicit and automatic fixture cleanup share the same owned barriers;
	// repeated cleanup must not close the caller-owned store.
	t.Cleanup(func() { h.quiesce(t) })
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("owned setup gate was not entered")
	}
	h.close(t)
	select {
	case <-h.runDone:
	default:
		t.Error("cleanup returned while the owned run driver was still active")
	}
	select {
	case <-h.generatorDone:
	default:
		t.Error("cleanup returned without joining its owned generator")
	}
}

func TestDistributedOrderingHarnessCleanupAfterActivationRetiresCapacityAndStorage(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	h := newOwnedDistributedOrderingHarness(t, 400*time.Millisecond, store, nil)
	t.Cleanup(func() { h.quiesce(t) })
	waitDistributedActivation(t, h.runner, h.test.ID)
	if h.service.ActiveVUsers() != 1 {
		t.Fatal("fixture had no allocated generator capacity before cleanup")
	}
	h.close(t)
	if h.service.ActiveVUsers() != 0 {
		t.Error("cleanup returned with retained generator capacity")
	}
	select {
	case <-h.generatorDone:
	default:
		t.Error("cleanup returned before generator shutdown joined")
	}
	h.runner.mu.RLock()
	terminal := isTerminalTestStatus(h.test.Status)
	remoteRetained := h.test.remoteLifecycleCtx != nil || h.test.remoteClient != nil || len(h.test.remoteClientsByTarget) != 0
	h.runner.mu.RUnlock()
	if !terminal || remoteRetained {
		t.Error("cleanup returned before controller terminal/archive ownership retired")
	}
	// The real caller-owned SQLite remains available until after cleanup.
	if _, err := store.GetTest(h.test.ID); err != nil {
		t.Errorf("cleanup closed its caller-owned store: %v", err)
	}
}

package api

import (
	"context"
	"net"
	"path/filepath"
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
	*storage.SQLiteStore
	delay   time.Duration
	once    sync.Once
	endedAt atomic.Int64
}

type delayedBarrierPersistenceStore struct {
	*storage.SQLiteStore
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

func newDistributedOrderingHarness(t *testing.T, timeout time.Duration, store storage.Store, wrap func(*generatorrpc.Client) generatorClient) (*TestRunner, *ManagedTest) {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	pb.RegisterGeneratorControlServer(server, generatorrpc.NewService("ordering-generator", 20))
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	runner := NewTestRunner(1, NewHub(), store)
	runner.setExecutionBackend(newDistributedExecutionBackend([]string{"bufnet"}, timeout, func(ctx context.Context, target string) (generatorClient, error) {
		client, err := generatorrpc.Dial(ctx, target,
			grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithBlock(),
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
	if err := runner.StartTest(test.ID); err != nil {
		t.Fatalf("StartTest: %v", err)
	}
	return runner, test
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
	runner, test := newDistributedOrderingHarness(t, timeout, store, nil)
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
	timeout := 120 * time.Millisecond
	runner, test := newDistributedOrderingHarness(t, timeout, nil, func(client *generatorrpc.Client) generatorClient {
		return &delayedSetupGeneratorClient{Client: client, delay: 80 * time.Millisecond}
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
	runner, test := newDistributedOrderingHarness(t, timeout, store, nil)
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

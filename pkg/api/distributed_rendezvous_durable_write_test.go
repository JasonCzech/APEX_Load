package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/rendezvous"
	"github.com/jasonczech/apexload/pkg/storage"
)

type rendezvousGaugeStore struct {
	SQLiteStore *storage.SQLiteStore
	runner      *TestRunner
	operation   string
	failure     error
	gauge       int64
	calls       int
}

func (s *rendezvousGaugeStore) observe(operation string) error {
	if s.operation != operation {
		return nil
	}
	s.gauge = s.runner.durableWritesInFlight.Load()
	s.calls++
	return s.failure
}

func (s *rendezvousGaugeStore) InitializeRendezvous(ctx context.Context, record storage.RendezvousRecord, term storage.LiveTableExecutionTerm) error {
	if err := s.observe("initialize"); err != nil {
		return err
	}
	return s.SQLiteStore.InitializeRendezvous(ctx, record, term)
}

func (s *rendezvousGaugeStore) ClaimRendezvous(ctx context.Context, run, group, project string, term storage.LiveTableExecutionTerm) (*storage.RendezvousRecord, error) {
	if err := s.observe("claim"); err != nil {
		return nil, err
	}
	return s.SQLiteStore.ClaimRendezvous(ctx, run, group, project, term)
}

func (s *rendezvousGaugeStore) SaveRendezvous(ctx context.Context, record storage.RendezvousRecord, previous uint64, term storage.LiveTableExecutionTerm) error {
	if err := s.observe("save"); err != nil {
		return err
	}
	return s.SQLiteStore.SaveRendezvous(ctx, record, previous, term)
}

func TestDistributedRendezvousDurableWritesPublishAndReleaseGauge(t *testing.T) {
	for _, operation := range []string{"initialize", "claim", "save"} {
		for _, fail := range []bool{false, true} {
			name := operation + "/success"
			if fail {
				name = operation + "/failure"
			}
			t.Run(name, func(t *testing.T) {
				runner, store, _ := newAdoptionTestRunner(t)
				ctx := context.Background()
				group := rendezvousTestGroup()
				test := &ManagedTest{ID: "gauge-rendezvous", ProjectID: storage.DefaultProjectID, Name: "Rendezvous", Status: "INITIALIZING", Groups: []TestGroup{group}}
				if err := store.SaveTest(&storage.TestRecord{ID: test.ID, ProjectID: test.ProjectID, Name: test.Name, Status: test.Status, StartTime: time.Now(), Groups: []any{}}); err != nil {
					t.Fatal(err)
				}
				term, err := runner.captureExecutionLeaseTerm()
				if err != nil {
					t.Fatal(err)
				}
				observed := &rendezvousGaugeStore{SQLiteStore: store, runner: runner}
				runner.testStore = sqliteOverlay(observed)
				shards := []GeneratorShardSummary{{Group: group.Name, VUsers: group.VUsers}}
				if operation != "initialize" {
					if err := runner.prepareGroupRendezvous(ctx, test, group, shards, false, term); err != nil {
						t.Fatal(err)
					}
				}
				observed.operation = operation
				if fail {
					observed.failure = errors.New("injected rendezvous storage failure")
				}
				switch operation {
				case "initialize":
					err = runner.prepareGroupRendezvous(ctx, test, group, shards, false, term)
				case "claim":
					runner.dropRunRendezvous(test.ID, term)
					err = runner.restoreRendezvous(ctx, test, map[string][]GeneratorShardSummary{group.Name: shards}, term)
				case "save":
					_, err = runner.localRendezvousClient(test.ID, group.Name).Exchange(ctx, rendezvous.Batch{Key: "checkout", Round: 1, Arrivals: []uint64{0}})
				}
				if fail != (err != nil) {
					t.Fatalf("write outcome = %v, failure wanted = %v", err, fail)
				}
				if fail && operation != "save" && !errors.Is(err, observed.failure) {
					t.Fatalf("storage error changed: %v", err)
				}
				if fail && operation == "save" && !errors.Is(err, rendezvous.ErrUnavailable) {
					t.Fatalf("uncertain save must fence the coordinator: %v", err)
				}
				if observed.calls != 1 || observed.gauge != 1 {
					t.Fatalf("storage observed calls=%d in-flight=%d, want 1 each", observed.calls, observed.gauge)
				}
				if got := runner.durableWritesInFlight.Load(); got != 0 {
					t.Fatalf("write completion leaked in-flight gauge: %d", got)
				}
			})
		}
	}
}

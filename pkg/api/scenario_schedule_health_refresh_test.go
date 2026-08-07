package api

import (
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

// scheduleHealthVersionStore is a scheduler-content-versioned store that counts
// the library reads a schedule-health poll performs.
type scheduleHealthVersionStore struct {
	storage.Store
	mu            sync.Mutex
	version       string
	scriptLists   int
	scenarioLists int
	statusCounts  int
}

func (s *scheduleHealthVersionStore) SchedulerContentVersion() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.version, nil
}

func (s *scheduleHealthVersionStore) ListScripts(string, *string) ([]*dsl.Script, error) {
	s.mu.Lock()
	s.scriptLists++
	s.mu.Unlock()
	return nil, nil
}

func (s *scheduleHealthVersionStore) ListScenarios() ([]*storage.Scenario, error) {
	s.mu.Lock()
	s.scenarioLists++
	s.mu.Unlock()
	return nil, nil
}

func (s *scheduleHealthVersionStore) CountScenarioScheduleOccurrencesByStatus() (map[string]int, error) {
	s.mu.Lock()
	s.statusCounts++
	s.mu.Unlock()
	return map[string]int{}, nil
}

func (s *scheduleHealthVersionStore) counts() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scriptLists, s.scenarioLists
}

func TestScenarioScheduleHealthPollSkipsUnchangedLibraryReload(t *testing.T) {
	store := &scheduleHealthVersionStore{version: "v1"}
	runner := &TestRunner{
		scripts:            map[string]*StoredScript{},
		scenarios:          map[string]*StoredScenario{},
		tests:              map[string]*ManagedTest{},
		refKeyIndex:        map[string]string{},
		privateCISnapshots: map[string]int{},
		scriptStore:        store,
		scenarioStore:      store,
		leadership:         &executionLeadership{},
	}

	now := time.Now()
	for range 5 {
		runner.ScenarioScheduleHealth(now)
	}
	if scripts, scenarios := store.counts(); scripts != 1 || scenarios != 1 {
		t.Fatalf("unchanged-library polls = scripts:%d scenarios:%d, want 1 each", scripts, scenarios)
	}

	// A schedule committed by another replica bumps the content revision, and the
	// next poll must observe it.
	store.mu.Lock()
	store.version = "v2"
	store.mu.Unlock()
	runner.ScenarioScheduleHealth(now)
	if scripts, scenarios := store.counts(); scripts != 2 || scenarios != 2 {
		t.Fatalf("polls after a cross-replica change = scripts:%d scenarios:%d, want 2 each", scripts, scenarios)
	}
}

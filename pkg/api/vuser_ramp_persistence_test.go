package api

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// countingRampTestPersistence counts the full test rows a ramp serializes. Each
// SaveTest re-encodes every compiled group script, so the per-ramp count is the
// cost the step bound exists to contain.
type countingRampTestPersistence struct {
	mu    sync.Mutex
	saves int
}

func (s *countingRampTestPersistence) SaveTest(*storage.TestRecord) error {
	s.mu.Lock()
	s.saves++
	s.mu.Unlock()
	return nil
}

func (s *countingRampTestPersistence) saveCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saves
}

func (s *countingRampTestPersistence) GetTest(string) (*storage.TestRecord, error) {
	return nil, sql.ErrNoRows
}

func (s *countingRampTestPersistence) ListTests(string, *string) ([]*storage.TestRecord, error) {
	return nil, nil
}

func (s *countingRampTestPersistence) ListTestsByContext(string, string, string) ([]*storage.TestRecord, error) {
	return nil, nil
}

func (s *countingRampTestPersistence) UpdateTestStatus(string, string, *time.Time, string) error {
	return nil
}

func (s *countingRampTestPersistence) DeleteTest(string) error { return nil }

func (s *countingRampTestPersistence) MoveTest(string, *string) error { return nil }

type countingRampScaleBackend struct {
	mu    sync.Mutex
	calls int
}

func (b *countingRampScaleBackend) Run(*TestRunner, *ManagedTest) {}

func (b *countingRampScaleBackend) ScaleVUsers(_ *TestRunner, _ *ManagedTest, _ string, _ int) error {
	b.mu.Lock()
	b.calls++
	b.mu.Unlock()
	return nil
}

func (b *countingRampScaleBackend) callCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls
}

// TestVUserScaleRampBoundsDurableTestRowWrites pins the durable cost of a large
// ramp end to end. The pure vuserRampTargets test only checks the target list,
// so it would still pass if the step bound stopped gating persistence.
func TestVUserScaleRampBoundsDurableTestRowWrites(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	store := &countingRampTestPersistence{}
	runner.testStore = store
	backend := &countingRampScaleBackend{}
	runner.setExecutionBackend(backend)
	runner.tests["test-ramp-writes"] = &ManagedTest{
		ID:           "test-ramp-writes",
		Status:       "RUNNING",
		ScenarioMode: "vuser_driven",
		ProjectID:    storage.DefaultProjectID,
		TotalVUsers:  1,
		Groups: []TestGroup{{
			Name:          "buyers",
			VUsers:        1,
			stageDriven:   true,
			initialVUsers: 1,
			currentVUsers: 1,
		}},
	}

	ramp, err := runner.startVUserScaleRamp(context.Background(), "test-ramp-writes", "buyers", 401, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("startVUserScaleRamp: %v", err)
	}
	select {
	case err := <-ramp.done:
		if err != nil {
			t.Fatalf("ramp completion error: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for the ramp to finish")
	}

	// The literal ceiling is deliberate: asserting only against
	// maxVUserRampSteps would keep passing if the bound itself were raised or
	// removed, which is exactly the regression this guards.
	const durableWriteCeiling = 128
	if got := backend.callCount(); got == 0 || got > durableWriteCeiling {
		t.Fatalf("backend scale calls = %d for a 400-VUser ramp, want a bounded non-zero step count", got)
	}
	// Every reserved step serializes one full test row (all compiled group
	// scripts included), so a 400-VUser ramp must not degrade to one write per
	// VUser.
	if got := store.saveCount(); got > maxVUserRampSteps || got > durableWriteCeiling {
		t.Fatalf("durable test-row writes = %d for a 400-VUser ramp, want at most %d", got, maxVUserRampSteps)
	}
	if vusers, ok := testGroupVUsers(runner.tests["test-ramp-writes"], "buyers"); !ok || vusers != 401 {
		t.Fatalf("ramp endpoint = %d ok=%v, want the exact target 401", vusers, ok)
	}
}

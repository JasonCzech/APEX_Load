package api

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

// narrowScaleTestPersistence records how a ramp publishes its reservations: a
// full-row upsert (SaveTest) or the narrow workload-only write.
type narrowScaleTestPersistence struct {
	*countingRampTestPersistence

	mu           sync.Mutex
	narrowWrites int
	lastTotal    int
	lastGroups   any
}

func (s *narrowScaleTestPersistence) UpdateTestVUserScale(update storage.TestVUserScaleUpdate) (bool, error) {
	s.mu.Lock()
	s.narrowWrites++
	s.lastTotal = update.TotalVUsers
	s.lastGroups = update.Groups
	s.mu.Unlock()
	return true, nil
}

func (s *narrowScaleTestPersistence) narrowCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.narrowWrites
}

func (s *narrowScaleTestPersistence) snapshot() (int, any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastTotal, s.lastGroups
}

// TestVUserScaleRampUsesNarrowWorkloadWrite proves the ramp no longer routes
// every reserved step through the full-row test upsert. Before the narrow
// durable projection existed, each of the ramp's steps re-validated tenant
// placement and re-serialized every compiled group script under tr.mu.
func TestVUserScaleRampUsesNarrowWorkloadWrite(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	store := &narrowScaleTestPersistence{countingRampTestPersistence: &countingRampTestPersistence{}}
	runner.testStore = store
	backend := &countingRampScaleBackend{}
	runner.setExecutionBackend(backend)
	runner.tests["test-narrow-scale"] = &ManagedTest{
		ID:           "test-narrow-scale",
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
			script:        &dsl.Script{ID: "script-1", Name: "checkout"},
		}},
	}

	ramp, err := runner.startVUserScaleRamp(context.Background(), "test-narrow-scale", "buyers", 401, 50*time.Millisecond)
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

	if got := store.narrowCount(); got == 0 {
		t.Fatal("ramp published no narrow workload writes")
	}
	if got := store.countingRampTestPersistence.saveCount(); got != 0 {
		t.Fatalf("full-row test upserts during a ramp = %d, want 0", got)
	}
	total, groups := store.snapshot()
	if total != 401 {
		t.Fatalf("last persisted total_vusers = %d, want the exact endpoint 401", total)
	}
	// The narrow write must still carry the complete durable group payload,
	// compiled scripts included, or a restart would lose the run's workload.
	payload, err := json.Marshal(groups)
	if err != nil {
		t.Fatalf("marshal narrow group payload: %v", err)
	}
	if !bytes.Contains(payload, []byte(`"script-1"`)) {
		t.Fatalf("narrow group payload dropped the compiled script: %s", payload)
	}
}

// TestStoredGroupsForLockedMarshalMatchesEncodeStoredGroups pins the narrow
// write's group payload to the full-row encoder byte for byte. The narrow path
// skips the defensive deep copy (it marshals while tr.mu is still held), and
// this guards that the skipped copy is the only difference.
func TestStoredGroupsForLockedMarshalMatchesEncodeStoredGroups(t *testing.T) {
	groups := []TestGroup{
		{
			Name:              "buyers",
			VUsers:            7,
			script:            &dsl.Script{ID: "script-1", Name: "checkout", Protocol: "http"},
			baseScript:        &dsl.Script{ID: "script-0", Name: "base", Protocol: "http"},
			sharedDataSources: []dsl.DataSource{{Name: "users"}},
			redactionRules:    []engine.RedactionRule{{Name: "token"}},
		},
		{Name: "browsers", VUsers: 2},
	}

	want, err := json.Marshal(encodeStoredGroups(groups))
	if err != nil {
		t.Fatalf("marshal encodeStoredGroups: %v", err)
	}
	got, err := json.Marshal(storedGroupsForLockedMarshal(groups))
	if err != nil {
		t.Fatalf("marshal storedGroupsForLockedMarshal: %v", err)
	}
	if !bytes.Equal(want, got) {
		t.Fatalf("narrow group payload diverged from the full-row encoding\n full: %s\nnarrow: %s", want, got)
	}

	if empty, err := json.Marshal(storedGroupsForLockedMarshal(nil)); err != nil || string(empty) != "[]" {
		t.Fatalf("empty group payload = %s err=%v, want []", empty, err)
	}
}

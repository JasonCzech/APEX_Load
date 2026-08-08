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

// terminalScaleCompatibilityStore applies the narrow VUser-scale update to the
// stored row exactly as UPDATE tests SET total_vusers=?, groups_json=? does:
// the groups column is replaced wholesale, with no status guard.
type terminalScaleCompatibilityStore struct {
	*terminalRuntimeCompatibilityStore

	narrowWrites int
}

func (s *terminalScaleCompatibilityStore) UpdateTestVUserScale(update storage.TestVUserScaleUpdate) (bool, error) {
	s.narrowWrites++
	if s.record == nil || s.record.ID != update.ID {
		return false, nil
	}
	s.record.TotalVUsers = update.TotalVUsers
	s.record.Groups = update.Groups
	return true, nil
}

// TestNarrowVUserScaleWritePreservesDurableTerminalRuntime proves the narrow
// workload write honours the terminal-compaction guard the full-row save has.
//
// Terminal compaction nils each group's compiled script, base script, shared
// data sources and redaction rules in memory and records that it did
// (terminalGroupRuntimeTrimmed). buildManagedTestRecordForSave substitutes the
// durable groups snapshot for exactly that reason. The narrow write marshals
// the live groups instead, so a scale write that lands after terminalization -
// rollbackVUserScaleReservation has no terminal-status guard, unlike
// failClosedUncertainVUserScale - overwrote groups_json with a runtime-less
// projection and permanently destroyed the run's durable workload.
func TestNarrowVUserScaleWritePreservesDurableTerminalRuntime(t *testing.T) {
	createdAt := time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)
	endedAt := createdAt.Add(time.Minute)
	group := func(name string) TestGroup {
		return TestGroup{
			Name:              name,
			VUsers:            3,
			ScriptID:          "runtime-script-" + name,
			script:            &dsl.Script{ID: "runtime-script-" + name, Name: "runtime " + name},
			baseScript:        &dsl.Script{ID: "base-script-" + name, Name: "base " + name},
			sharedDataSources: []dsl.DataSource{{Name: "accounts", Type: "csv", InlineData: "id\n1", Strategy: "sequential"}},
			redactionRules:    []engine.RedactionRule{{Name: "secret", Pattern: `secret-[0-9]+`}},
		}
	}
	seed := &ManagedTest{
		ID:          "terminal-narrow-scale",
		Name:        "terminal narrow scale",
		Status:      "COMPLETE",
		ProjectID:   storage.DefaultProjectID,
		CreatedAt:   createdAt,
		EndedAt:     &endedAt,
		TotalVUsers: 6,
		Groups:      []TestGroup{group("buyers"), group("browsers")},
	}
	record := buildManagedTestRecord(seed)
	store := &terminalScaleCompatibilityStore{
		terminalRuntimeCompatibilityStore: &terminalRuntimeCompatibilityStore{
			metricsStoreSpy: &metricsStoreSpy{listedTests: []*storage.TestRecord{record}},
			record:          record,
		},
	}
	runner := NewTestRunner(2, NewHub(), store)

	canonical := runner.tests[seed.ID]
	if canonical == nil || !canonical.terminalGroupRuntimeTrimmed {
		t.Fatalf("terminal canonical state was not compacted: %#v", canonical)
	}

	// A VUser-scale write reaching a run that terminalized underneath it: the
	// rollback of a reservation whose backend call failed.
	runner.mu.Lock()
	canonical.TotalVUsers = 4
	err := runner.persistManagedTestVUserScale(canonical)
	runner.mu.Unlock()
	if err != nil {
		t.Fatalf("persistManagedTestVUserScale: %v", err)
	}

	if store.record.TotalVUsers != 4 {
		t.Fatalf("durable total_vusers = %d, want the scale write to still land (4)", store.record.TotalVUsers)
	}
	groups := decodeStoredGroups(store.record.Groups)
	if len(groups) != 2 {
		t.Fatalf("durable groups = %#v, want both groups", groups)
	}
	for _, stored := range groups {
		if stored.script == nil || stored.script.ID != "runtime-script-"+stored.Name {
			t.Fatalf("durable runtime script for group %q = %#v, want the preserved snapshot", stored.Name, stored.script)
		}
		if stored.baseScript == nil || stored.baseScript.ID != "base-script-"+stored.Name {
			t.Fatalf("durable base script for group %q = %#v, want the preserved snapshot", stored.Name, stored.baseScript)
		}
		if len(stored.sharedDataSources) != 1 || stored.sharedDataSources[0].Name != "accounts" {
			t.Fatalf("durable shared data sources for group %q = %#v, want the preserved projection", stored.Name, stored.sharedDataSources)
		}
		if len(stored.redactionRules) != 1 || stored.redactionRules[0].Pattern != `secret-[0-9]+` {
			t.Fatalf("durable redaction rules for group %q = %#v, want the preserved policy", stored.Name, stored.redactionRules)
		}
	}
	if store.narrowWrites != 0 {
		t.Fatalf("narrow workload writes for a compacted terminal run = %d, want the full-row fallback that restores the durable groups", store.narrowWrites)
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
